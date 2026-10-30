//go:build integration

package org

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/platform/periodlock"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type harness struct {
	svc  *Service
	prov *Provisioner
	clk  *clock.Fixed
	d    *db.DB
}

func newHarness(t *testing.T, objects objectstore.Store) harness {
	t.Helper()
	clk := testkit.Clock()
	rec := audit.NewRecorder(clk)
	prov := NewProvisioner(NewSlugPolicy(nil), rec, clk, Seeders{})
	d := testkit.DB(t)
	return harness{
		svc: NewService(Deps{
			DB: d, Clock: clk, Audit: rec, Exceptions: exception.NewGuard(clk), Provisioner: prov, Objects: objects,
			AttachmentOwners: AttachmentOwners{reasonOwner: reasonExists},
		}),
		prov: prov, clk: clk, d: d,
	}
}

// reasonOwner is the one owner type these tests upload files for: the organisation owns no
// document a file belongs to, and the registry needs a record a tenant can see or not.
const reasonOwner = "reason"

func reasonExists(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM reasons WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

// reason creates a reason of the actor's tenant for files to be uploaded for.
func (h harness) reason(t *testing.T, actor audit.Actor) uuid.UUID {
	t.Helper()
	kind, name := "damage", "Hư hỏng "+unique()
	r, err := h.svc.CreateReason(context.Background(), actor, ReasonInput{Kind: &kind, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func unique() string { return strings.ReplaceAll(ids.New().String(), "-", "")[20:] }

// provision creates a tenant through the provisioner and returns an owner actor for it.
func (h harness) provision(t *testing.T, spec TenantSpec) audit.Actor {
	t.Helper()
	if spec.ID == uuid.Nil {
		spec.ID = ids.New()
	}
	if spec.Slug == "" {
		spec.Slug = "s-" + unique()
	}
	if spec.DisplayName == "" {
		spec.DisplayName = "Cửa hàng " + spec.Slug
	}
	actor := audit.Actor{TenantID: spec.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb}
	testkit.InTenant(t, h.d, spec.ID, func(q db.Querier) error {
		p, err := h.prov.Provision(context.Background(), q, spec, audit.Actor{TenantID: spec.ID, Type: audit.ActorSystem, Source: audit.SourceCLI})
		if err == nil && !p.Created {
			t.Error("a new tenant id must be created")
		}
		return err
	})
	return actor
}

func scopeFor(a audit.Actor) Scope { return Scope{TenantID: a.TenantID} }

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Errorf("error = %v, want %s", err, code)
	}
}

func TestProvisionCreatesTheStoreSkeleton(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a := h.provision(t, TenantSpec{Slug: "s-" + unique(), DisplayName: "Cửa hàng A"})
	tenant, err := h.svc.Tenant(ctx, a.TenantID)
	if err != nil || tenant.DisplayName != "Cửa hàng A" || tenant.LegalName != "Cửa hàng A" || tenant.Timezone != "Asia/Ho_Chi_Minh" ||
		tenant.Currency != "VND" || tenant.IsSandbox {
		t.Fatalf("tenant = %+v, %v", tenant, err)
	}
	store, wh, err := h.svc.StoreAndWarehouse(ctx, scopeFor(a))
	if err != nil || store.Name != "Cửa hàng A" || wh.StoreID != store.ID {
		t.Errorf("store = %+v, warehouse = %+v, %v", store, wh, err)
	}
	settings, err := h.svc.Settings(ctx, scopeFor(a))
	if err != nil || settings.TenantID != a.TenantID || settings.Version != 1 {
		t.Errorf("settings = %+v, %v", settings, err)
	}
	var prefixes int
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM doc_sequences WHERE tenant_id = $1`, a.TenantID).Scan(&prefixes)
	})
	if prefixes == 0 {
		t.Error("no document number prefixes were seeded")
	}

	// Provisioning the same id again is recognised and changes nothing.
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		p, err := h.prov.Provision(ctx, q, TenantSpec{ID: a.TenantID, Slug: tenant.Slug, DisplayName: "Đổi tên"}, a)
		if err == nil && (p.Created || p.Tenant.DisplayName != "Cửa hàng A") {
			t.Errorf("retried provisioning = %+v", p)
		}
		return err
	})

	// A taken slug is slug_taken.
	other := ids.New()
	err = h.d.WithTenantTx(ctx, other, func(q db.Querier) error {
		_, err := h.prov.Provision(ctx, q, TenantSpec{ID: other, Slug: tenant.Slug, DisplayName: "Khác"},
			audit.Actor{TenantID: other, Type: audit.ActorSystem, Source: audit.SourceCLI})
		return err
	})
	wantCode(t, err, "slug_taken")
}

func TestUpdateTenantAndSettingsUseOptimisticVersions(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a := h.provision(t, TenantSpec{})
	name, phone := "Cửa hàng mới", "0901234567"
	updated, err := h.svc.UpdateTenant(ctx, a, 1, TenantPatch{DisplayName: &name, Phone: &phone})
	if err != nil || updated.DisplayName != name || updated.Version != 2 || updated.Phone == nil || *updated.Phone != phone {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	_, err = h.svc.UpdateTenant(ctx, a, 1, TenantPatch{DisplayName: &name})
	wantCode(t, err, "precondition_failed")

	days := 45
	s, err := h.svc.UpdateSettings(ctx, a, 1, SettingsPatch{DefaultPaymentTermsDays: &days})
	if err != nil || s.DefaultPaymentTermsDays != 45 || s.Version != 2 {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	_, err = h.svc.UpdateSettings(ctx, a, 1, SettingsPatch{DefaultPaymentTermsDays: &days})
	wantCode(t, err, "precondition_failed")

	events, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{EntityType: "tenant", Action: "tenant.update"}, httpx.Page{Limit: 10})
	if err != nil || len(events) != 1 || events[0].ActorID == nil || *events[0].ActorID != a.ID {
		t.Errorf("tenant.update events = %+v, %v", events, err)
	}
}

// probe is the smallest posting: it writes nothing and only exercises the coordinator guards.
type probe struct{ date time.Time }

func (probe) Scope() string             { return "probe.post" }
func (p probe) BusinessDate() time.Time { return p.date }
func (probe) DocPrefix() string         { return "" }
func (probe) LockKeys() posting.LockSet { return posting.LockSet{} }
func (probe) Apply(context.Context, posting.Tx) (posting.Result, error) {
	return posting.Result{Body: map[string]string{"ok": "yes"}}, nil
}

type allowAll struct{}

func (allowAll) CanPost(context.Context, db.Querier, uuid.UUID) error { return nil }

func TestPeriodLockGuardsPostingsAndReopenNeedsAConfirmation(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a := h.provision(t, TenantSpec{})
	coord, err := posting.NewCoordinator(posting.Deps{
		DB: h.d, Idempotency: idempotency.NewGuard(h.clk), Docs: docseq.NewIssuer(), Periods: periodlock.NewGuard(),
		Audit: audit.NewRecorder(h.clk), Outbox: outbox.NewPublisher(h.clk), Entitlements: allowAll{}, Clock: h.clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(key string, date time.Time) error {
		sum := sha256.Sum256([]byte(key))
		_, err := coord.Post(ctx, posting.Actor{TenantID: a.TenantID, Type: a.Type, ID: a.ID, Source: a.Source},
			idempotency.Key{Value: key, RequestHash: sum[:]}, probe{date: date})
		return err
	}
	march10, march12 := clock.Date(2026, 3, 10), clock.Date(2026, 3, 12)
	if err := post("before-lock", march10); err != nil {
		t.Fatalf("posting into an open period = %v", err)
	}

	_, err = h.svc.LockPeriod(ctx, a, clock.Date(2026, 3, 16))
	wantCode(t, err, "lock_date_in_future")
	if _, err := h.svc.LockPeriod(ctx, a, march12); err != nil {
		t.Fatal(err)
	}
	err = post("inside-lock", march10)
	if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.PeriodLocked || ae.Code != "period_locked" || httpx.StatusOf(ae.Kind) != http.StatusLocked {
		t.Fatalf("posting into a locked period = %v, want 423 period_locked", err)
	}
	if err := post("after-lock", clock.Date(2026, 3, 13)); err != nil {
		t.Errorf("posting after the lock date = %v", err)
	}
	h.clk.Advance(time.Minute)
	_, err = h.svc.LockPeriod(ctx, a, march10)
	wantCode(t, err, "lock_not_forward")

	locks, err := h.svc.PeriodLocks(ctx, scopeFor(a), httpx.Page{Limit: 10})
	if err != nil || locks.Version != 1 || locks.LockedThrough == nil || !locks.LockedThrough.Equal(march12) {
		t.Fatalf("locks = %+v, %v", locks, err)
	}

	body := []byte(`{"locked_through":"2026-03-09","reason":"Sửa phiếu nhập sai"}`)
	hash, err := exception.PayloadHash(body)
	if err != nil {
		t.Fatal(err)
	}
	march9 := clock.Date(2026, 3, 9)
	reopen := ReopenInput{LockedThrough: &march9, Reason: "Sửa phiếu nhập sai", PayloadHash: hash}
	_, err = h.svc.ReopenPeriod(ctx, a, reopen)
	wantCode(t, err, string(exception.PeriodReopen))

	confirm := func(version int) uuid.UUID {
		t.Helper()
		c, err := h.svc.ConfirmException(ctx, a, ConfirmInput{
			Kind: exception.PeriodReopen, DocType: PeriodLockDocType, DocID: a.TenantID, DocVersion: version,
			PayloadHash: hash, Note: "Chủ cửa hàng đồng ý",
		})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	stale := confirm(0)
	reopen.ConfirmationID = stale
	_, err = h.svc.ReopenPeriod(ctx, a, reopen)
	wantCode(t, err, string(exception.PeriodReopen))

	reopen.ConfirmationID = confirm(locks.Version)
	tampered := reopen
	march1 := clock.Date(2026, 3, 1)
	tampered.LockedThrough = &march1
	tampered.PayloadHash = make([]byte, 32)
	_, err = h.svc.ReopenPeriod(ctx, a, tampered)
	wantCode(t, err, string(exception.PeriodReopen))

	h.clk.Advance(time.Minute)
	if _, err := h.svc.ReopenPeriod(ctx, a, reopen); err != nil {
		t.Fatalf("reopen = %v", err)
	}
	if err := post("reopened", march10); err != nil {
		t.Errorf("posting into the reopened period = %v", err)
	}

	// The spent confirmation does not reopen anything again: the lock moved to a new version.
	h.clk.Advance(time.Minute)
	if _, err := h.svc.LockPeriod(ctx, a, march12); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Minute)
	_, err = h.svc.ReopenPeriod(ctx, a, reopen)
	wantCode(t, err, string(exception.PeriodReopen))

	locks, err = h.svc.PeriodLocks(ctx, scopeFor(a), httpx.Page{Limit: 10})
	if err != nil || locks.Version != 3 || len(locks.Events) != 3 || locks.Events[1].Action != PeriodLockActionReopen {
		t.Errorf("history = %+v, %v", locks, err)
	}

	// A reopen without a date unlocks every period; with nothing locked there is nothing to reopen.
	fullBody := []byte(`{"locked_through":null,"reason":"Khoá nhầm kỳ đầu tiên"}`)
	fullHash, err := exception.PayloadHash(fullBody)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := h.svc.ConfirmException(ctx, a, ConfirmInput{
		Kind: exception.PeriodReopen, DocType: PeriodLockDocType, DocID: a.TenantID, DocVersion: locks.Version, PayloadHash: fullHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Minute)
	full := ReopenInput{Reason: "Khoá nhầm kỳ đầu tiên", ConfirmationID: conf.ID, PayloadHash: fullHash}
	ev, err := h.svc.ReopenPeriod(ctx, a, full)
	if err != nil || ev.LockedThrough != nil {
		t.Fatalf("full reopen = %+v, %v", ev, err)
	}
	if err := post("fully-reopened", clock.Date(2026, 3, 1)); err != nil {
		t.Errorf("posting after a full reopen = %v", err)
	}
	locks, err = h.svc.PeriodLocks(ctx, scopeFor(a), httpx.Page{Limit: 10})
	if err != nil || locks.Version != 4 || locks.LockedThrough != nil {
		t.Fatalf("after a full reopen: locks = %+v, %v", locks, err)
	}
	_, err = h.svc.ReopenPeriod(ctx, a, full)
	wantCode(t, err, "period_not_locked")
	h.clk.Advance(time.Minute)
	if _, err := h.svc.LockPeriod(ctx, a, march10); err != nil {
		t.Fatalf("lock after a full reopen = %v", err)
	}
}

func TestExceptionConfirmationIsSpentOnce(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a := h.provision(t, TenantSpec{})
	docID := ids.New()
	hash := make([]byte, 32)
	c, err := h.svc.ConfirmException(ctx, a, ConfirmInput{
		Kind: exception.CreditLimitExceeded, DocType: "sales_invoice", DocID: docID, DocVersion: 2, PayloadHash: hash,
		Impact: []byte(`{"over_by":"150000"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	guard := exception.NewGuard(h.clk)
	want := exception.Expectation{
		ConfirmationID: c.ID, Kind: exception.CreditLimitExceeded, DocType: "sales_invoice", DocID: docID, DocVersion: 2,
		PayloadHash: hash, UsedByDocType: "sales_invoice", UsedByDocID: docID,
	}
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error { return guard.Consume(ctx, q, a.TenantID, want) })
	err = h.d.WithTenantTx(ctx, a.TenantID, func(q db.Querier) error { return guard.Consume(ctx, q, a.TenantID, want) })
	wantCode(t, err, "exception_confirmation_used")

	// Another tenant cannot see, let alone spend, the confirmation.
	b := h.provision(t, TenantSpec{})
	err = h.d.WithTenantTx(ctx, b.TenantID, func(q db.Querier) error { return guard.Consume(ctx, q, b.TenantID, want) })
	wantCode(t, err, string(exception.CreditLimitExceeded))

	_, err = h.svc.ConfirmException(ctx, a, ConfirmInput{Kind: "made_up", DocType: "x", DocID: docID, PayloadHash: hash})
	wantCode(t, err, "validation_failed")
}

func TestAuditSearchMatchesDocNoActionEntityAndActorName(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a, b := h.provision(t, TenantSpec{}), h.provision(t, TenantSpec{})
	now := h.clk.Now()
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO users (id, tenant_id, email, email_key, full_name, password_hash, created_at, updated_at)
			VALUES ($1, $2, $3, $3, 'Nguyễn Thị Bình', 'x', $4, $4)`, a.ID, a.TenantID, "owner-"+unique()+"@example.test", now)
		return err
	})
	system := audit.Actor{TenantID: a.TenantID, Type: audit.ActorSystem, Source: audit.SourceWorker}
	rec := audit.NewRecorder(h.clk)
	record := func(actor audit.Actor, action, entity, docNo string) {
		t.Helper()
		h.clk.Advance(time.Second)
		testkit.InTenant(t, h.d, actor.TenantID, func(q db.Querier) error {
			return rec.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: ids.New(), DocNo: docNo})
		})
	}
	record(a, "sales_invoice.post", "sales_invoice", "HD-0042")
	record(system, "stock_out.post", "stock_out", "PX-0007")
	record(system, "price.change", "price_list", "")
	record(audit.Actor{TenantID: b.TenantID, Type: audit.ActorSystem, Source: audit.SourceWorker}, "sales_invoice.post", "sales_invoice", "HD-0042")

	search := func(q string, page httpx.Page) []AuditEvent {
		t.Helper()
		events, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{Query: q}, page)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		for _, ev := range events {
			if ev.TenantID != a.TenantID {
				t.Errorf("search %q found an event of another store: %+v", q, ev)
			}
		}
		return events
	}
	actions := func(events []AuditEvent) []string {
		out := make([]string, len(events))
		for i, ev := range events {
			out[i] = ev.Action
		}
		return out
	}
	for _, c := range []struct {
		q    string
		want []string
	}{
		{"hd-004", []string{"sales_invoice.post"}},   // part of the document number, any case
		{"STOCK_OUT", []string{"stock_out.post"}},    // the action or the entity type
		{"price_l", []string{"price.change"}},        // the entity type alone
		{"thi binh", []string{"sales_invoice.post"}}, // the actor's name without diacritics
		{"Bình", []string{"sales_invoice.post"}},     // and with them
		{"post", []string{"stock_out.post", "sales_invoice.post"}},
		{"%", nil}, // LIKE wildcards are literal
		{"không có", nil},
	} {
		got := actions(search(c.q, httpx.Page{Limit: 50}))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("search %q = %v, want %v", c.q, got, c.want)
		}
	}

	// The search keeps the keyset pages of the log.
	first := search("post", httpx.Page{Limit: 1})
	if len(first) != 2 || first[0].Action != "stock_out.post" {
		t.Fatalf("first page of post = %v, want stock_out.post and one more row", actions(first))
	}
	cursor := AuditEventCursor(first[0])
	second := search("post", httpx.Page{Limit: 1, After: &cursor})
	if got := actions(second); len(got) != 1 || got[0] != "sales_invoice.post" {
		t.Errorf("second page of post = %v, want sales_invoice.post alone", got)
	}
}

func TestAuditQueryIsTenantScoped(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a, b := h.provision(t, TenantSpec{}), h.provision(t, TenantSpec{})
	if _, err := h.svc.LockPeriod(ctx, a, clock.Date(2026, 3, 1)); err != nil {
		t.Fatal(err)
	}
	all, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{}, httpx.Page{Limit: 50})
	if err != nil || len(all) < 2 {
		t.Fatalf("events = %d, %v", len(all), err)
	}
	for _, ev := range all {
		if ev.TenantID != a.TenantID {
			t.Errorf("event of another tenant: %+v", ev)
		}
	}
	locks, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{Action: "period.lock", ActorID: a.ID,
		From: clock.Date(2026, 3, 15), To: clock.Date(2026, 3, 15)}, httpx.Page{Limit: 50})
	if err != nil || len(locks) != 1 {
		t.Fatalf("period.lock events = %d, %v", len(locks), err)
	}
	none, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{From: clock.Date(2026, 3, 16)}, httpx.Page{Limit: 50})
	if err != nil || len(none) != 0 {
		t.Errorf("events from tomorrow = %d, %v", len(none), err)
	}
	_, err = h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{From: clock.Date(2026, 3, 16), To: clock.Date(2026, 3, 1)}, httpx.Page{Limit: 50})
	wantCode(t, err, "validation_failed")

	// Paging is keyset based and stable.
	first, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{}, httpx.Page{Limit: 1})
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d rows, %v (want limit+1)", len(first), err)
	}
	cursor := AuditEventCursor(first[0])
	second, err := h.svc.AuditEvents(ctx, scopeFor(a), AuditFilter{}, httpx.Page{Limit: 1, After: &cursor})
	if err != nil || len(second) == 0 || second[0].ID != first[1].ID {
		t.Errorf("second page = %+v, %v", second, err)
	}

	one, err := h.svc.AuditEvent(ctx, scopeFor(a), locks[0].ID)
	if err != nil || one.Action != "period.lock" {
		t.Errorf("event = %+v, %v", one, err)
	}
	_, err = h.svc.AuditEvent(ctx, scopeFor(b), locks[0].ID)
	wantCode(t, err, "not_found")

	// A support session reads through a read-only transaction.
	ro, err := h.svc.AuditEvents(ctx, Scope{TenantID: a.TenantID, ReadOnly: true}, AuditFilter{}, httpx.Page{Limit: 50})
	if err != nil || len(ro) != len(all) {
		t.Errorf("read-only events = %d, %v", len(ro), err)
	}
}

func TestSandboxResetReplacesTheTenant(t *testing.T) {
	h := newHarness(t, nil)
	var moved []string
	h.svc.addressMoved = func(slug string) { moved = append(moved, slug) }
	ctx := context.Background()
	parent := h.provision(t, TenantSpec{})
	parentTenant, err := h.svc.Tenant(ctx, parent.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	address := parentTenant.Slug + SandboxSuffix
	sandbox := h.provision(t, TenantSpec{
		Slug: address, DisplayName: "Cửa hàng demo", IsSandbox: true,
		SandboxOf: &parent.TenantID, ParentSlug: parentTenant.Slug,
	})
	days := 7
	if _, err := h.svc.UpdateSettings(ctx, sandbox, 1, SettingsPatch{DefaultPaymentTermsDays: &days}); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Minute)
	if _, err := h.svc.LockPeriod(ctx, sandbox, clock.TruncateDate(h.clk.Now().AddDate(0, 0, -2))); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Minute)
	var pending uuid.UUID
	testkit.InTenant(t, h.d, sandbox.TenantID, func(q db.Querier) error {
		var err error
		pending, err = outbox.NewPublisher(h.clk).Publish(ctx, q, sandbox.TenantID, outbox.Event{Topic: "sandbox.probe"})
		return err
	})

	// A sandbox younger than the reset interval refuses, saying how long to wait.
	_, err = h.svc.ResetSandbox(ctx, sandbox)
	var tooSoon *ResetTooSoonError
	if !errors.As(err, &tooSoon) || !apperr.Is(err, "sandbox_reset_too_soon") || tooSoon.RetryAfter != SandboxResetInterval-2*time.Minute {
		t.Fatalf("young sandbox reset = %v", err)
	}
	h.clk.Advance(SandboxResetInterval)

	first, err := h.svc.ResetSandbox(ctx, sandbox)
	if err != nil {
		t.Fatalf("reset = %v", err)
	}
	if first.ID == sandbox.TenantID || first.Slug != address || !first.IsSandbox || first.SandboxOf == nil ||
		*first.SandboxOf != parent.TenantID || first.Status != TenantActive || first.DisplayName != "Cửa hàng demo" {
		t.Fatalf("new sandbox = %+v", first)
	}
	old, err := h.svc.Tenant(ctx, sandbox.TenantID)
	if err != nil || old.Status != TenantReplaced || old.Slug != replacedSlug(sandbox.TenantID) || !old.IsSandbox {
		t.Fatalf("replaced sandbox = %+v, %v", old, err)
	}
	// The old tenant keeps its settings and its lock history; the new one starts from the defaults.
	if s, err := h.svc.Settings(ctx, scopeFor(sandbox)); err != nil || s.DefaultPaymentTermsDays != 7 {
		t.Errorf("old settings = %+v, %v", s, err)
	}
	if locks, err := h.svc.PeriodLocks(ctx, scopeFor(sandbox), httpx.Page{Limit: 5}); err != nil || locks.LockedThrough == nil {
		t.Errorf("old period locks = %+v, %v", locks, err)
	}
	next := audit.Actor{TenantID: first.ID, Type: audit.ActorOwner, ID: sandbox.ID, Source: audit.SourceWeb}
	if s, err := h.svc.Settings(ctx, scopeFor(next)); err != nil || s.DefaultPaymentTermsDays == 7 || s.Version != 1 {
		t.Errorf("new settings = %+v, %v", s, err)
	}
	if locks, err := h.svc.PeriodLocks(ctx, scopeFor(next), httpx.Page{Limit: 5}); err != nil || locks.LockedThrough != nil || len(locks.Events) != 0 {
		t.Errorf("new period locks = %+v, %v", locks, err)
	}
	if _, _, err := h.svc.StoreAndWarehouse(ctx, scopeFor(next)); err != nil {
		t.Errorf("new store = %v", err)
	}
	replaced, err := h.svc.AuditEvents(ctx, scopeFor(sandbox), AuditFilter{Action: "tenant.replace"}, httpx.Page{Limit: 5})
	if err != nil || len(replaced) != 1 || !bytes.Contains(replaced[0].After, []byte(`"closed_events": 1`)) {
		t.Errorf("replace events = %+v, %v", replaced, err)
	}
	// The reset closed the undelivered event of the old tenant without handling it.
	var processed bool
	var reason *string
	testkit.InTenant(t, h.d, sandbox.TenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT processed_at IS NOT NULL, last_error FROM outbox_events WHERE id = $1`, pending).Scan(&processed, &reason)
	})
	if !processed || reason == nil || *reason != "tenant replaced by a sandbox reset" {
		t.Errorf("pending event after reset: processed=%v reason=%v", processed, reason)
	}
	reset, err := h.svc.AuditEvents(ctx, scopeFor(next), AuditFilter{Action: "tenant.sandbox_reset"}, httpx.Page{Limit: 5})
	if err != nil || len(reset) != 1 {
		t.Errorf("reset events = %d, %v", len(reset), err)
	}

	// A request still acting for the replaced tenant cannot reset it again.
	_, err = h.svc.ResetSandbox(ctx, sandbox)
	wantCode(t, err, "sandbox_not_active")

	// A retry right after the reset reaches the new sandbox and is refused instead of resetting it.
	_, err = h.svc.ResetSandbox(ctx, next)
	wantCode(t, err, "sandbox_reset_too_soon")

	// Resets repeat: the address moves on every time and no slug is retired.
	h.clk.Advance(SandboxResetInterval)
	second, err := h.svc.ResetSandbox(ctx, next)
	if err != nil || second.Slug != address || second.ID == first.ID {
		t.Fatalf("second reset = %+v, %v", second, err)
	}
	if moved := len(moved); moved != 2 {
		t.Errorf("address moved %d times, want 2", moved)
	}
	var tenants, retired int
	testkit.InTenant(t, h.d, second.ID, func(q db.Querier) error {
		if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM tenants WHERE sandbox_of = $1`, parent.TenantID).Scan(&tenants); err != nil {
			return err
		}
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM retired_tenant_slugs WHERE slug = $1`, address).Scan(&retired)
	})
	if tenants != 3 || retired != 0 {
		t.Errorf("sandboxes of the parent = %d, retired addresses = %d", tenants, retired)
	}

	_, err = h.svc.ResetSandbox(ctx, parent)
	wantCode(t, err, "not_sandbox")
	// Only a sandbox can ever be replaced.
	err = h.d.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `UPDATE tenants SET status = 'replaced' WHERE id = $1`, parent.TenantID)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "ck_tenants__replaced_sandbox") {
		t.Errorf("replace a real store = %v, want ck_tenants__replaced_sandbox", err)
	}

	// The standalone sandbox has no parent and the address demo.
	standalone := h.provision(t, TenantSpec{ID: ids.New(), Slug: StandaloneSandboxSlug, DisplayName: "Demo", IsSandbox: true})
	h.clk.Advance(SandboxResetInterval)
	if again, err := h.svc.ResetSandbox(ctx, standalone); err != nil || again.Slug != StandaloneSandboxSlug || again.SandboxOf != nil {
		t.Errorf("standalone reset = %+v, %v", again, err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// devStore connects to the MinIO of the dev stack (make db-up).
func devStore(t *testing.T) *objectstore.S3 {
	t.Helper()
	endpoint, err := url.Parse(env("OBJECT_STORE_ENDPOINT", "http://localhost:9000"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: endpoint, Region: env("OBJECT_STORE_REGION", "us-east-1"), Bucket: env("OBJECT_STORE_BUCKET", "tally-dev"),
		AccessKey: env("OBJECT_STORE_ACCESS_KEY", "tally-dev"), SecretKey: env("OBJECT_STORE_SECRET_KEY", "tally-dev-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("MinIO unreachable (start it with `make db-up`): %v", err)
	}
	return s
}

func upload(t *testing.T, req objectstore.PresignedRequest, body []byte) int {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), req.Method, req.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// fetch downloads a presigned URL and fails the test unless it answers 200.
func fetch(t *testing.T, u string) []byte {
	t.Helper()
	resp, err := http.Get(u) //nolint:noctx // test download of a presigned URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("download = %d %q, %v", resp.StatusCode, got, err)
	}
	return got
}

// download signs a fresh URL for a clean attachment and fetches its content.
func download(t *testing.T, h harness, actor audit.Actor, id uuid.UUID) []byte {
	t.Helper()
	link, err := h.svc.AttachmentURL(context.Background(), scopeFor(actor), id)
	if err != nil {
		t.Fatal(err)
	}
	return fetch(t, link.URL)
}

// removeAttachmentObjects deletes both objects an attachment may have in the dev bucket.
func removeAttachmentObjects(t *testing.T, store objectstore.Store, a Attachment) {
	t.Helper()
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), a.StorageKey)
		_ = store.Delete(context.Background(), uploadKey(a.TenantID, a.ID))
	})
}

// An upload is accepted only for a registered owner type and an owner the tenant has.
func TestAttachmentOwnerIsRegisteredAndOfTheTenant(t *testing.T) {
	// Presigning needs no connection: a refused upload never reaches the object store.
	s3, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: &url.URL{Scheme: "http", Host: "127.0.0.1:9000"}, Region: "us-east-1", Bucket: "tally-dev",
		AccessKey: "tally-dev", SecretKey: "tally-dev-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := &presignCounter{Store: s3}
	h := newHarness(t, objects)
	ctx := context.Background()
	a, b := h.provision(t, TenantSpec{}), h.provision(t, TenantSpec{})
	ofA, ofB := h.reason(t, a), h.reason(t, b)
	sum := sha256.Sum256([]byte("hoá đơn"))
	create := func(ownerType string, ownerID uuid.UUID) error {
		_, err := h.svc.CreateAttachment(ctx, a, NewAttachment{OwnerType: ownerType, OwnerID: ownerID, FileName: "hoa-don.pdf",
			ContentType: "application/pdf", SizeBytes: 9, SHA256: sum[:]})
		return err
	}
	attachments := func() int {
		var n int
		testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
			return q.QueryRowxContext(ctx, `SELECT count(*) FROM attachments`).Scan(&n)
		})
		return n
	}

	for _, c := range []struct {
		what, ownerType string
		ownerID         uuid.UUID
		field, code     string
	}{
		{"an unregistered owner type", "purchase_receipt", ofA, "owner_type", "invalid"},
		{"an owner that does not exist", reasonOwner, ids.New(), "owner_id", "not_found"},
		{"an owner of another store", reasonOwner, ofB, "owner_id", "not_found"},
	} {
		err := create(c.ownerType, c.ownerID)
		if e, ok := apperr.As(err); !ok || e.Kind != apperr.Validation || e.Fields[c.field] != c.code {
			t.Errorf("%s: error = %v, want %s %s", c.what, err, c.field, c.code)
		}
	}
	if n := attachments(); n != 0 || objects.n != 0 {
		t.Errorf("after refused uploads: %d attachments, %d uploads signed, want none", n, objects.n)
	}
	if err := create(reasonOwner, ofA); err != nil {
		t.Fatalf("upload for an owner of the store: %v", err)
	}
	if n := attachments(); n != 1 || objects.n != 1 {
		t.Errorf("%d attachments, %d uploads signed, want 1", n, objects.n)
	}
}

// presignCounter counts the uploads signed through the store.
type presignCounter struct {
	objectstore.Store
	n int
}

func (p *presignCounter) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (objectstore.PresignedRequest, error) {
	p.n++
	return p.Store.PresignPut(ctx, key, contentType, size, ttl)
}

func TestAttachmentRoundTripThroughMinIO(t *testing.T) {
	store := devStore(t)
	h := newHarness(t, store)
	ctx := context.Background()
	a, b := h.provision(t, TenantSpec{}), h.provision(t, TenantSpec{})
	owner := h.reason(t, a)
	content := []byte("%PDF-1.7\nhoá đơn nhập hàng\n")
	sum := sha256.Sum256(content)
	up, err := h.svc.CreateAttachment(ctx, a, NewAttachment{
		OwnerType: reasonOwner, OwnerID: owner, FileName: "Hoá đơn tháng 3.pdf",
		ContentType: "application/pdf", SizeBytes: int64(len(content)), SHA256: sum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	removeAttachmentObjects(t, store, up.Attachment)
	if !strings.HasPrefix(up.Attachment.StorageKey, "tenants/"+a.TenantID.String()+"/") {
		t.Errorf("storage key %q is not under the tenant prefix", up.Attachment.StorageKey)
	}

	_, err = h.svc.CompleteAttachment(ctx, a, up.Attachment.ID)
	wantCode(t, err, "upload_missing")
	_, err = h.svc.AttachmentURL(ctx, scopeFor(a), up.Attachment.ID)
	wantCode(t, err, "attachment_not_ready")

	if code := upload(t, up.Request, content); code != http.StatusOK {
		t.Fatalf("presigned upload = %d", code)
	}
	_, err = h.svc.CompleteAttachment(ctx, b, up.Attachment.ID)
	wantCode(t, err, "not_found")
	done, err := h.svc.CompleteAttachment(ctx, a, up.Attachment.ID)
	if err != nil || done.ScanStatus != ScanClean {
		t.Fatalf("complete = %+v, %v", done, err)
	}
	if again, err := h.svc.CompleteAttachment(ctx, a, up.Attachment.ID); err != nil || again.ScanStatus != ScanClean {
		t.Errorf("complete twice = %+v, %v", again, err)
	}

	link, err := h.svc.AttachmentURL(ctx, scopeFor(a), up.Attachment.ID)
	if err != nil || !link.ExpiresAt.Equal(h.clk.Now().Add(AttachmentDownloadTTL)) {
		t.Fatalf("url = %+v, %v", link, err)
	}
	if got := fetch(t, link.URL); !bytes.Equal(got, content) {
		t.Errorf("download = %q", got)
	}
	_, err = h.svc.AttachmentURL(ctx, scopeFor(b), up.Attachment.ID)
	wantCode(t, err, "not_found")

	// The upload URL is still valid after completion, but the verified content is a copy it
	// cannot reach: uploading other bytes through it and completing again changes nothing.
	if _, err := store.Stat(ctx, uploadKey(a.TenantID, up.Attachment.ID)); err == nil {
		t.Error("the upload object must be removed once its copy is verified")
	}
	if code := upload(t, up.Request, bytes.ToUpper(content)); code != http.StatusOK {
		t.Fatalf("second presigned upload = %d", code)
	}
	if again, err := h.svc.CompleteAttachment(ctx, a, up.Attachment.ID); err != nil || again.ScanStatus != ScanClean {
		t.Errorf("complete after a second upload = %+v, %v", again, err)
	}
	if got := download(t, h, a, up.Attachment.ID); !bytes.Equal(got, content) {
		t.Errorf("verified content changed after a second upload: %q", got)
	}

	// An upload that does not match its declaration is rejected for good and deleted.
	bad, err := h.svc.CreateAttachment(ctx, a, NewAttachment{
		OwnerType: reasonOwner, OwnerID: owner, FileName: "anh.png", ContentType: "image/png",
		SizeBytes: 4, SHA256: sum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	removeAttachmentObjects(t, store, bad.Attachment)
	if err := store.Put(ctx, uploadKey(a.TenantID, bad.Attachment.ID), bytes.NewReader([]byte("12345")), 5, "image/png"); err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.CompleteAttachment(ctx, a, bad.Attachment.ID)
	wantCode(t, err, "upload_rejected")
	for _, key := range []string{bad.Attachment.StorageKey, uploadKey(a.TenantID, bad.Attachment.ID)} {
		if _, err := store.Stat(ctx, key); err == nil {
			t.Errorf("a rejected upload must be deleted: %s", key)
		}
	}

	// Right size and type but other bytes than the declared digest: rejected and deleted too.
	forged, err := h.svc.CreateAttachment(ctx, a, NewAttachment{
		OwnerType: reasonOwner, OwnerID: owner, FileName: "hoa-don.pdf", ContentType: "application/pdf",
		SizeBytes: int64(len(content)), SHA256: sum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	removeAttachmentObjects(t, store, forged.Attachment)
	other := bytes.ToUpper(content)
	if code := upload(t, forged.Request, other); code != http.StatusOK {
		t.Fatalf("presigned upload = %d", code)
	}
	_, err = h.svc.CompleteAttachment(ctx, a, forged.Attachment.ID)
	wantCode(t, err, "upload_rejected")
	for _, key := range []string{forged.Attachment.StorageKey, uploadKey(a.TenantID, forged.Attachment.ID)} {
		if _, err := store.Stat(ctx, key); err == nil {
			t.Errorf("an upload with another digest must be deleted: %s", key)
		}
	}
	_, err = h.svc.AttachmentURL(ctx, scopeFor(a), forged.Attachment.ID)
	wantCode(t, err, "attachment_not_ready")

	_, err = h.svc.CreateAttachment(ctx, a, NewAttachment{
		OwnerType: reasonOwner, OwnerID: owner, FileName: "virus.exe", ContentType: "application/x-msdownload",
		SizeBytes: 10, SHA256: sum[:],
	})
	if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.Validation {
		t.Errorf("unsupported type = %v", err)
	}
}

// sandboxWithOwner provisions a sandbox old enough to reset, with an owner account.
func sandboxWithOwner(t *testing.T, h harness) (sandbox audit.Actor, userID uuid.UUID) {
	t.Helper()
	parent := h.provision(t, TenantSpec{})
	parentTenant, err := h.svc.Tenant(context.Background(), parent.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	sandbox = h.provision(t, TenantSpec{
		Slug: parentTenant.Slug + SandboxSuffix, DisplayName: "Cửa hàng demo", IsSandbox: true,
		SandboxOf: &parent.TenantID, ParentSlug: parentTenant.Slug,
	})
	userID = ids.New()
	email := "demo-" + unique() + "@example.test"
	testkit.SuperDB(t).MustExec(`
		INSERT INTO tally.users (id, tenant_id, email, email_key, full_name, password_hash, status, created_at, updated_at)
		VALUES ($1, $2, $3, $3, 'Chủ demo', 'x', 'active', $4, $4)`, userID, sandbox.TenantID, email, h.clk.Now())
	h.clk.Advance(SandboxResetInterval)
	return sandbox, userID
}

// awaitLockWait returns once a statement containing fragment waits for a row lock.
func awaitLockWait(t *testing.T, fragment string) {
	t.Helper()
	super := testkit.SuperDB(t)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var n int
		if err := super.QueryRowx(`
			SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND position($1 in query) > 0`,
			fragment).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
	}
	t.Fatalf("no statement with %q waits for a lock", fragment)
}

type resetResult struct {
	tenant Tenant
	err    error
}

func resetAsync(h harness, actor audit.Actor) <-chan resetResult {
	done := make(chan resetResult, 1)
	go func() {
		out, err := h.svc.ResetSandbox(context.Background(), actor)
		done <- resetResult{out, err}
	}()
	return done
}

func awaitReset(t *testing.T, done <-chan resetResult) resetResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("the reset did not finish")
		return resetResult{}
	}
}

func TestSandboxResetSerialisesWithAccountChanges(t *testing.T) {
	h := newHarness(t, nil)
	super := testkit.SuperDB(t)

	t.Run("a change that locked the tenant first finishes and its session moves", func(t *testing.T) {
		sandbox, userID := sandboxWithOwner(t, h)
		// An account change (a login) takes the tenant lock first, as identity does.
		tx := super.MustBegin()
		defer func() { _ = tx.Rollback() }()
		tx.MustExec(`SELECT 1 FROM tally.tenants WHERE id = $1 FOR KEY SHARE`, sandbox.TenantID)
		done := resetAsync(h, sandbox)
		awaitLockWait(t, "FROM tenants WHERE id = $1 FOR UPDATE")
		now := h.clk.Now()
		tx.MustExec(`
			INSERT INTO tally.sessions (id, tenant_id, user_id, token_hash, device_label, created_at, last_seen_at, expires_at)
			VALUES ($1, $2, $3, $4, 'Thử', $5, $5, $6)`,
			ids.New(), sandbox.TenantID, userID, ids.New().String(), now, now.Add(time.Hour))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		r := awaitReset(t, done)
		if r.err != nil {
			t.Fatalf("reset = %v", r.err)
		}
		var oldSessions, newSessions int
		if err := super.QueryRowx(`
			SELECT count(*) FILTER (WHERE tenant_id = $1), count(*) FILTER (WHERE tenant_id = $2)
			FROM tally.sessions WHERE user_id = $3`, sandbox.TenantID, r.tenant.ID, userID).Scan(&oldSessions, &newSessions); err != nil {
			t.Fatal(err)
		}
		if oldSessions != 0 || newSessions != 1 {
			t.Errorf("sessions old/new = %d/%d, want 0/1", oldSessions, newSessions)
		}
	})

	t.Run("a reset that loses a deadlock runs once more", func(t *testing.T) {
		sandbox, userID := sandboxWithOwner(t, h)
		// A writer that locks the account row before the tenant row: the reset holds the tenant row
		// and waits for the account, then the writer waits for the tenant. PostgreSQL breaks the cycle
		// in the backend whose deadlock_timeout expires first; the writer's long timeout makes that the
		// reset even when a loaded machine delays the writer's wait.
		tx := super.MustBegin()
		defer func() { _ = tx.Rollback() }()
		tx.MustExec(`SET LOCAL deadlock_timeout = '30s'`)
		tx.MustExec(`SELECT 1 FROM tally.users WHERE id = $1 FOR NO KEY UPDATE`, userID)
		done := resetAsync(h, sandbox)
		awaitLockWait(t, "UPDATE users SET tenant_id")
		if _, err := tx.Exec(`SELECT 1 FROM tally.tenants WHERE id = $1 FOR KEY SHARE`, sandbox.TenantID); err != nil {
			t.Fatalf("the writer lost the deadlock: %v", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		r := awaitReset(t, done)
		if r.err != nil {
			t.Fatalf("reset = %v", r.err)
		}
		var status string
		if err := super.QueryRowx(`SELECT status FROM tally.tenants WHERE id = $1`, sandbox.TenantID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != TenantReplaced {
			t.Errorf("old sandbox status = %s", status)
		}
	})

	t.Run("a reset that loses the deadlock twice answers sandbox_busy and changes nothing", func(t *testing.T) {
		sandbox, userID := sandboxWithOwner(t, h)
		tx := super.MustBegin()
		defer func() { _ = tx.Rollback() }()
		tx.MustExec(`SET LOCAL deadlock_timeout = '30s'`) // the reset is the victim, as above
		tx.MustExec(`SELECT 1 FROM tally.users WHERE id = $1 FOR NO KEY UPDATE`, userID)
		done := resetAsync(h, sandbox)
		// Each round closes the cycle again, then lets the tenant row go (a row lock taken after a
		// savepoint ends when the transaction rolls back to it) so the next attempt of the reset can
		// reach the account row and wait for it.
		for round := 1; round <= 2; round++ {
			awaitLockWait(t, "UPDATE users SET tenant_id")
			tx.MustExec(`SAVEPOINT tenant_lock`)
			if _, err := tx.Exec(`SELECT 1 FROM tally.tenants WHERE id = $1 FOR KEY SHARE`, sandbox.TenantID); err != nil {
				t.Fatalf("round %d: the writer lost the deadlock: %v", round, err)
			}
			tx.MustExec(`ROLLBACK TO SAVEPOINT tenant_lock`)
		}
		r := awaitReset(t, done)
		if !apperr.Is(r.err, "sandbox_busy") {
			t.Fatalf("reset = %v, want sandbox_busy", r.err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var status string
		var owner uuid.UUID
		if err := super.QueryRowx(`
			SELECT t.status, u.tenant_id FROM tally.tenants t, tally.users u WHERE t.id = $1 AND u.id = $2`,
			sandbox.TenantID, userID).Scan(&status, &owner); err != nil {
			t.Fatal(err)
		}
		if status != TenantActive || owner != sandbox.TenantID {
			t.Errorf("after sandbox_busy: status %s, account in %s, want active in %s", status, owner, sandbox.TenantID)
		}
	})
}
