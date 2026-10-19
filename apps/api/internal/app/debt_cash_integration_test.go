//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// newLedgerStack is the HTTP stack with the debt and cash routes, on a clock the test moves (the
// re-authentication window is measured on it).
func newLedgerStack(t *testing.T) (stack, *clock.Fixed) {
	t.Helper()
	clk := testkit.Clock()
	capture := &captureMailer{}
	deps := identityDeps(t, capture, clk)
	var hosts *middleware.TenantHostCache
	deps.TenantCreated = func(slug string) {
		if hosts != nil {
			hosts.Purge(slug)
		}
	}
	idSvc, err := identity.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	domains := app.NewDomains(deps.DB, clk, deps.Audit)
	orgSvc := org.NewService(org.Deps{
		DB: deps.DB, Clock: clk, Audit: deps.Audit, Exceptions: exception.NewGuard(clk),
		Provisioner: org.NewProvisioner(org.NewSlugPolicy(nil), deps.Audit, clk, domains.Seeders()), Objects: ledgerObjects(t),
		AttachmentOwners: domains.AttachmentOwners(),
	})
	a, err := app.New(app.Options{
		PublicBaseURL: testBaseURL,
		Logger:        slog.New(slog.DiscardHandler),
		Ports:         app.Ports{Sessions: idSvc, TenantHosts: idSvc},
		CSRFKey:       []byte("0123456789abcdef0123456789abcdef"),
		Now:           clk.Now,
		APIRateLimit:  testAPIRateLimit,
		Modules: app.Modules{
			Identity: identity.NewHandler(idSvc), Org: org.NewHandler(orgSvc), Reasons: org.NewReasonHandler(orgSvc),
			Catalog: catalog.NewHandler(domains.Catalog), Partner: partner.NewHandler(domains.Partner),
			Debt: debt.NewHandler(domains.Debt), Cash: cash.NewHandler(domains.Cash),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk
}

// ledgerObjects is the dev MinIO (make db-up). Presigning needs no connection, so only a test that
// uploads a file reaches it.
func ledgerObjects(t *testing.T) *objectstore.S3 {
	t.Helper()
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: &url.URL{Scheme: "http", Host: "127.0.0.1:9000"}, Region: "us-east-1", Bucket: "tally-dev",
		AccessKey: "tally-dev", SecretKey: "tally-dev-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return objects
}

// declareAttachment declares a PDF of body for an owner and returns the attachment and the
// presigned upload the API answered with. The attachment stays pending until it is completed.
func declareAttachment(t *testing.T, b *browser, ownerType string, ownerID uuid.UUID, body []byte) (uuid.UUID, uploadOut) {
	t.Helper()
	sum := sha256.Sum256(body)
	rec := b.do(http.MethodPost, v1+"/attachments", map[string]any{"owner_type": ownerType, "owner_id": ownerID,
		"file_name": "bien-ban-bu-tru.pdf", "content_type": "application/pdf", "size_bytes": len(body), "sha256": hex.EncodeToString(sum[:])})
	expect(t, rec, http.StatusCreated, "")
	out := testkit.DecodeJSON[struct {
		Attachment withID    `json:"attachment"`
		Upload     uploadOut `json:"upload"`
	}](t, rec)
	return out.Attachment.ID, out.Upload
}

type uploadOut struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// uploadAttachment uploads a PDF for an owner the way the web client does: it declares the file,
// PUTs it to the presigned URL and completes it, so the attachment is clean.
func uploadAttachment(t *testing.T, b *browser, ownerType string, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	body := []byte("%PDF-1.4 bien ban bu tru " + ids.New().String())
	id, up := declareAttachment(t, b, ownerType, ownerID, body)
	tenant := tenantOf(t, b)
	t.Cleanup(func() {
		objects := ledgerObjects(t)
		_ = objects.Delete(context.Background(), objectstore.TenantKey(tenant, "attachments", id.String()))
		_ = objects.Delete(context.Background(), objectstore.TenantKey(tenant, "attachments", "uploads", id.String()))
	})
	req, err := http.NewRequestWithContext(context.Background(), up.Method, up.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload to the dev MinIO (start it with `make db-up`): %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	rec := b.do(http.MethodPost, v1+"/attachments/"+id.String()+":complete", nil)
	expect(t, rec, http.StatusOK, "")
	if got := testkit.DecodeJSON[struct {
		ScanStatus string `json:"scan_status"`
	}](t, rec).ScanStatus; got != org.ScanClean {
		t.Fatalf("completed attachment scan status = %q, want clean", got)
	}
	return id
}

// post sends a posting request under its own Idempotency-Key.
func post(b *browser, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.doWith(http.MethodPost, v1+path, body, http.Header{"Idempotency-Key": {ids.New().String()}})
}

type ledgerStore struct {
	customer, cashMethod, transferMethod, reason uuid.UUID
	cashFund                                     struct {
		ID      uuid.UUID `json:"id"`
		Kind    string    `json:"kind"`
		Version int       `json:"version"`
	}
	opening, payment, statement uuid.UUID
}

type paymentOut struct {
	ID          uuid.UUID `json:"id"`
	Status      string    `json:"status"`
	Unallocated string    `json:"unallocated"`
}

func buildLedgerStore(t *testing.T, b *browser) ledgerStore {
	t.Helper()
	var st ledgerStore
	for _, m := range send[dataOf[struct {
		ID   uuid.UUID `json:"id"`
		Code string    `json:"code"`
	}]](t, b, http.MethodGet, "/payment-methods", nil).Data {
		switch m.Code {
		case cash.MethodCash:
			st.cashMethod = m.ID
		case cash.MethodBankTransfer:
			st.transferMethod = m.ID
		}
	}
	for _, f := range send[dataOf[struct {
		ID      uuid.UUID `json:"id"`
		Kind    string    `json:"kind"`
		Version int       `json:"version"`
	}]](t, b, http.MethodGet, "/funds", nil).Data {
		if f.Kind == cash.FundCash {
			st.cashFund = f
		}
	}
	st.reason = send[dataOf[withID]](t, b, http.MethodGet, "/reasons?kind=debt_adjustment", nil).Data[0].ID
	group := send[withID](t, b, http.MethodPost, "/customer-groups", map[string]any{"name": "Đại lý", "base_price_kind": "wholesale"}).ID
	st.customer = send[withID](t, b, http.MethodPost, "/customers", map[string]any{
		"kind": "person", "name": "Chị Lan", "phone": "0901234567", "customer_group_id": group}).ID
	if st.cashMethod == uuid.Nil || st.transferMethod == uuid.Nil || st.cashFund.ID == uuid.Nil {
		t.Fatalf("seeded methods and funds = %+v", st)
	}

	// The opening debt of the customer, overdue since 01/03.
	rec := post(b, "/debt-adjustments", map[string]any{
		"party_id": st.customer, "direction": "receivable", "kind": "opening_balance", "amount": "3000000",
		"reason_id": st.reason, "opening_cutoff_date": "2026-03-14", "due_date": "2026-03-01"})
	expect(t, rec, http.StatusCreated, "")
	st.opening = testkit.DecodeJSON[struct {
		Obligation withID `json:"obligation"`
	}](t, rec).Obligation.ID
	return st
}

func TestDebtAndCashOverHTTP(t *testing.T) {
	s, clk := newLedgerStack(t)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	storeA, storeB := buildLedgerStore(t, a), buildLedgerStore(t, b)

	t.Run("postings need an Idempotency-Key and replay under it", func(t *testing.T) {
		body := map[string]any{"direction": "in", "party_id": storeA.customer, "purpose": "settlement",
			"payment_method_id": storeA.cashMethod, "amount": "1000000",
			"allocations": []map[string]any{{"obligation_id": storeA.opening, "amount": "1000000"}}}
		expect(t, a.do(http.MethodPost, v1+"/payments", body), http.StatusUnprocessableEntity, "idempotency_key_required")

		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		first := a.doWith(http.MethodPost, v1+"/payments", body, key)
		expect(t, first, http.StatusCreated, "")
		again := a.doWith(http.MethodPost, v1+"/payments", body, key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" ||
			testkit.DecodeJSON[withID](t, again).ID != testkit.DecodeJSON[withID](t, first).ID {
			t.Fatalf("replay = %s %s", again.Header(), again.Body)
		}
		body["amount"] = "2000000"
		expect(t, a.doWith(http.MethodPost, v1+"/payments", body, key), http.StatusConflict, "idempotency_key_reused")
		storeA.payment = testkit.DecodeJSON[withID](t, first).ID

		expect(t, post(a, "/payments", map[string]any{"direction": "in", "party_id": storeA.customer, "purpose": "settlement",
			"payment_method_id": storeA.cashMethod, "amount": "9000000",
			"allocations": []map[string]any{{"obligation_id": storeA.opening, "amount": "9000000"}}}),
			http.StatusUnprocessableEntity, "allocation_exceeds_open")
	})

	t.Run("the Công nợ screen reads open, overdue and the suggestion", func(t *testing.T) {
		rows := send[dataOf[struct {
			PartyID uuid.UUID `json:"party_id"`
			Open    string    `json:"open"`
			Overdue string    `json:"overdue"`
		}]](t, a, http.MethodGet, "/debts/parties?direction=receivable&overdue=true", nil).Data
		if len(rows) != 1 || rows[0].PartyID != storeA.customer || rows[0].Open != "2000000" || rows[0].Overdue != "2000000" {
			t.Fatalf("parties = %+v", rows)
		}
		got := send[struct {
			Allocations []struct {
				ObligationID uuid.UUID `json:"obligation_id"`
				Amount       string    `json:"amount"`
				Mode         string    `json:"mode"`
			} `json:"allocations"`
		}](t, a, http.MethodPost, "/allocations:suggest", map[string]any{"party_id": storeA.customer, "direction": "receivable", "amount": "5000000"})
		if len(got.Allocations) != 1 || got.Allocations[0].Amount != "2000000" || got.Allocations[0].Mode != debt.ModeSuggested {
			t.Fatalf("suggestion = %+v", got)
		}
		aging := send[struct {
			Totals struct {
				D1To30 string `json:"d1_30"`
			} `json:"totals"`
		}](t, a, http.MethodGet, "/debts/aging?direction=receivable&as_of=2026-03-15", nil)
		if aging.Totals.D1To30 != "2000000" {
			t.Errorf("aging = %+v", aging)
		}
		expect(t, a.do(http.MethodGet, v1+"/debts/aging?direction=receivable", nil), http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("a transfer waits in the queue until confirmed", func(t *testing.T) {
		p := testkit.DecodeJSON[paymentOut](t, post(a, "/payments", map[string]any{"direction": "in", "party_id": storeA.customer,
			"purpose": "advance", "payment_method_id": storeA.transferMethod, "amount": "500000"}))
		if p.Status != debt.PaymentPending {
			t.Fatalf("transfer = %+v", p)
		}
		queue := send[dataOf[withID]](t, a, http.MethodGet, "/payments/pending-verification", nil).Data
		if len(queue) != 1 || queue[0].ID != p.ID {
			t.Fatalf("queue = %+v", queue)
		}
		confirmed := testkit.DecodeJSON[paymentOut](t, post(a, "/payments/"+p.ID.String()+":confirm", nil))
		if confirmed.Status != debt.PaymentConfirmed {
			t.Fatalf("confirmed = %+v", confirmed)
		}
		expect(t, post(a, "/payments/"+p.ID.String()+":confirm", nil), http.StatusConflict, "payment_not_pending")
	})

	t.Run("reversing a payment needs a fresh password", func(t *testing.T) {
		clk.Advance(middleware.ReauthWindow + time.Minute)
		path := "/payments/" + storeA.payment.String() + ":reverse"
		expect(t, post(a, path, nil), http.StatusForbidden, "reauth_required")
		expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		rec := post(a, path, nil)
		expect(t, rec, http.StatusOK, "")
		if got := testkit.DecodeJSON[paymentOut](t, rec); got.Status != debt.PaymentReversed {
			t.Fatalf("reversed = %+v", got)
		}
	})

	t.Run("statements move under If-Match and keep their versions", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/statements", map[string]any{
			"party_id": storeA.customer, "direction": "receivable", "period_from": "2026-03-01", "period_to": "2026-03-15"})
		expect(t, rec, http.StatusCreated, "draft")
		storeA.statement = testkit.DecodeJSON[withID](t, rec).ID
		etag := rec.Header().Get("ETag")
		send := v1 + "/statements/" + storeA.statement.String() + ":send"
		expect(t, a.do(http.MethodPost, send, nil), http.StatusPreconditionRequired, "")
		expect(t, a.doWith(http.MethodPost, send, nil, http.Header{"If-Match": {`"99"`}}), http.StatusPreconditionFailed, "precondition_failed")
		sent := a.doWith(http.MethodPost, send, nil, http.Header{"If-Match": {etag}})
		expect(t, sent, http.StatusOK, "sent")
		generate := v1 + "/statements/" + storeA.statement.String() + ":generate"
		expect(t, a.doWith(http.MethodPost, generate, nil, http.Header{"If-Match": {sent.Header().Get("ETag")}}), http.StatusOK, "draft")
		expect(t, a.do(http.MethodGet, v1+"/statements/"+storeA.statement.String()+"/versions/1", nil), http.StatusOK, "superseded")
		expect(t, a.do(http.MethodGet, v1+"/statements/"+storeA.statement.String()+"/versions/3", nil), http.StatusNotFound, "not_found")
	})

	t.Run("fund opening balances take If-Match", func(t *testing.T) {
		path := v1 + "/funds/" + storeB.cashFund.ID.String()
		expect(t, b.do(http.MethodPatch, path, map[string]any{"opening_balance": "1000000"}), http.StatusPreconditionRequired, "")
		etag := `"` + strconv.Itoa(storeB.cashFund.Version) + `"`
		rec := b.doWith(http.MethodPatch, path, map[string]any{"opening_balance": "1000000", "opening_date": "2026-03-01"}, http.Header{"If-Match": {etag}})
		expect(t, rec, http.StatusOK, "")
		if got := testkit.DecodeJSON[struct {
			Balance string `json:"balance"`
		}](t, rec); got.Balance != "1000000" {
			t.Errorf("fund = %s", rec.Body)
		}
		// The cash fund of A has entries by now.
		etagA := `"` + strconv.Itoa(storeA.cashFund.Version) + `"`
		expect(t, a.doWith(http.MethodPatch, v1+"/funds/"+storeA.cashFund.ID.String(), map[string]any{"opening_balance": "1"},
			http.Header{"If-Match": {etagA}}), http.StatusConflict, "fund_has_entries")
	})

	t.Run("an offset settles both ledgers of the customer against the signed record uploaded for them", func(t *testing.T) {
		obligation := func(direction string) uuid.UUID {
			rec := post(a, "/debt-adjustments", map[string]any{"party_id": storeA.customer, "direction": direction,
				"kind": debt.AdjustIncrease, "amount": "400000", "reason_id": storeA.reason})
			expect(t, rec, http.StatusCreated, "")
			return testkit.DecodeJSON[struct {
				Obligation withID `json:"obligation"`
			}](t, rec).Obligation.ID
		}
		receivable, payable := obligation(debt.Receivable), obligation(debt.Payable)
		upload := func(ownerType string, ownerID uuid.UUID) uuid.UUID { return uploadAttachment(t, a, ownerType, ownerID) }
		record := upload(debt.AttachmentOwnerParty, storeA.customer)
		pending, _ := declareAttachment(t, a, debt.AttachmentOwnerParty, storeA.customer, []byte("%PDF-1.4 never uploaded"))
		supplier := send[withID](t, a, http.MethodPost, "/suppliers", map[string]any{"kind": "organization", "name": "Công ty Minh Phát"}).ID
		product := send[withID](t, a, http.MethodPost, "/products", map[string]any{"name": "Nước suối"}).ID
		offset := func(attachment uuid.UUID) *httptest.ResponseRecorder {
			return post(a, "/debt-offsets", map[string]any{"party_id": storeA.customer, "receivable_obligation_id": receivable,
				"payable_obligation_id": payable, "amount": "400000", "attachment_id": attachment})
		}

		expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		settlements := countRows(t, tenantOf(t, a), `SELECT count(*) FROM obligation_settlements`)
		for what, attachment := range map[string]uuid.UUID{
			"the record of another party": upload(debt.AttachmentOwnerParty, supplier),
			"a product image":             upload("product", product),
			"a record never uploaded":     pending,
		} {
			rec := offset(attachment)
			if rec.Code != http.StatusUnprocessableEntity || testkit.DecodeJSON[httpx.ErrorBody](t, rec).Error.Fields["attachment_id"] != "invalid" {
				t.Errorf("offset backed by %s = %d %s, want 422 attachment_id invalid", what, rec.Code, rec.Body)
			}
		}
		if n := countRows(t, tenantOf(t, a), `SELECT count(*) FROM obligation_settlements`); n != settlements {
			t.Errorf("settlements after refused offsets = %d, want %d", n, settlements)
		}

		rec := offset(record)
		expect(t, rec, http.StatusCreated, "")
		got := testkit.DecodeJSON[struct {
			AttachmentID *uuid.UUID `json:"attachment_id"`
			Settlements  []struct {
				ObligationID uuid.UUID `json:"obligation_id"`
				Amount       string    `json:"amount"`
			} `json:"settlements"`
		}](t, rec)
		if got.AttachmentID == nil || *got.AttachmentID != record || len(got.Settlements) != 2 ||
			got.Settlements[0].ObligationID != receivable || got.Settlements[1].ObligationID != payable ||
			got.Settlements[0].Amount != "400000" || got.Settlements[1].Amount != "400000" {
			t.Fatalf("offset = %s", rec.Body)
		}
	})

	t.Run("ids of another store answer 404", func(t *testing.T) {
		b := s.signIn(t, ownerB)
		header := http.Header{"Idempotency-Key": {ids.New().String()}, "If-Match": {`"1"`}}
		for _, r := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/debts/parties/" + storeA.customer.String(), nil},
			{http.MethodPost, "/payments/" + storeA.payment.String() + ":confirm", nil},
			{http.MethodPatch, "/funds/" + storeA.cashFund.ID.String(), map[string]any{"name": "x"}},
			{http.MethodGet, "/statements/" + storeA.statement.String() + "/versions/1", nil},
			{http.MethodPost, "/statements/" + storeA.statement.String() + ":send", nil},
			{http.MethodGet, "/parties/" + storeA.customer.String() + "/collection-notes", nil},
			{http.MethodPost, "/parties/" + storeA.customer.String() + "/reminders", map[string]any{"channel": "zalo"}},
		} {
			rec := b.doWith(r.method, v1+r.path, r.body, header)
			expect(t, rec, http.StatusNotFound, "not_found")
		}
		// Naming A's documents in B's postings finds nothing either.
		expect(t, post(b, "/payments", map[string]any{"direction": "in", "party_id": storeA.customer, "purpose": "advance",
			"payment_method_id": storeB.cashMethod, "amount": "1000"}), http.StatusUnprocessableEntity, "party_not_found")
		expect(t, post(b, "/refunds", map[string]any{"obligation_id": storeA.opening, "payment_method_id": storeB.cashMethod}),
			http.StatusUnprocessableEntity, "obligation_not_found")
		for _, p := range send[dataOf[struct {
			PartyID uuid.UUID `json:"party_id"`
		}]](t, b, http.MethodGet, "/debts/parties?direction=receivable", nil).Data {
			if p.PartyID == storeA.customer {
				t.Error("B lists A's customer")
			}
		}
	})

	t.Run("both ledgers verify after every posting above", func(t *testing.T) {
		for _, br := range []*browser{s.signIn(t, ownerA), s.signIn(t, ownerB)} {
			testkit.InTenant(t, testkit.DB(t), tenantOf(t, br), func(q db.Querier) error {
				for name, verify := range map[string]func(context.Context, db.Querier) ([]string, error){"debt": debt.Verify, "cash": cash.Verify} {
					mismatches, err := verify(context.Background(), q)
					if err != nil {
						return err
					}
					if len(mismatches) > 0 {
						t.Errorf("%s verify of %s: %v", name, br.host, mismatches)
					}
				}
				return nil
			})
		}
	})
}

// sourceDocument stands for a sale or receipt of another domain posting its debt rows (and
// reversing them) through the debt ports.
type sourceDocument struct {
	date   time.Time
	prefix string
	apply  func(ctx context.Context, tx posting.Tx) error
}

func (sourceDocument) Scope() string             { return "test.source_document" }
func (c sourceDocument) BusinessDate() time.Time { return c.date }
func (c sourceDocument) DocPrefix() string       { return c.prefix }
func (sourceDocument) LockKeys() posting.LockSet { return posting.LockSet{} }

func (c sourceDocument) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	if err := c.apply(ctx, tx); err != nil {
		return posting.Result{}, err
	}
	return posting.Result{Body: map[string]bool{"ok": true}}, nil
}

func TestReversedSaleLeavesEveryLedgerVerified(t *testing.T) {
	ctx := context.Background()
	clk := testkit.Clock()
	d := testkit.DB(t)
	domains := app.NewDomains(d, clk, audit.NewRecorder(clk))
	tn := testkit.NewTenant(t, d)
	owner := audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb}
	newKey := func() idempotency.Key {
		v := ids.New().String()
		h := sha256.Sum256([]byte(v))
		return idempotency.Key{Value: v, RequestHash: h[:]}
	}
	party := ids.New()
	var method, fund uuid.UUID
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		if err := cash.NewSeeder().SeedFunds(ctx, q, cash.SeedContext{TenantID: tn.ID, Now: clk.Now()}); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at)
			VALUES ($1, $2, 'Anh Tuấn', 'anh tuan', $3, $3)`, party, tn.ID, clk.Now()); err != nil {
			return err
		}
		return q.QueryRowxContext(ctx, `SELECT id, fund_account_id FROM payment_methods WHERE code = $1`, cash.MethodCash).
			Scan(&method, &fund)
	})
	today := clock.Date(2026, time.March, 15)
	run := func(prefix string, fn func(ctx context.Context, tx posting.Tx) error) error {
		_, err := domains.Posting.Post(ctx, owner, newKey(), sourceDocument{date: today, prefix: prefix, apply: fn})
		return err
	}

	sale := debt.ObligationInput{PartyID: party, SourceType: debt.SourceSalesDocument, SourceID: ids.New(),
		SourceDocNo: "BH-0001", Amount: money.MustMoney("2000000"), DueDate: &today}
	var ob debt.Obligation
	if err := run("", func(ctx context.Context, tx posting.Tx) error {
		r, err := domains.Debt.Receivables().Record(ctx, tx, sale)
		ob = r.Obligation
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := domains.Debt.PostPayment(ctx, owner, newKey(), debt.PaymentInput{
		Direction: debt.In, PartyID: &party, Purpose: debt.PurposeSettlement, MethodID: method, Amount: money.MustMoney("800000"),
		Allocations: []debt.Allocation{{ObligationID: ob.ID, Amount: money.MustMoney("800000"), Mode: debt.ModeManual}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	var reversal debt.ObligationReversal
	if err := run("XH", func(ctx context.Context, tx posting.Tx) (err error) {
		reversal, err = domains.Debt.Obligations().ReverseBySource(ctx, tx,
			debt.SourceRef{Direction: debt.Receivable, SourceType: sale.SourceType, SourceID: sale.SourceID}, "Huỷ phiếu xuất")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(reversal.Released) != 1 || reversal.Obligation.ReversalDocNo == nil {
		t.Fatalf("reversal = %+v, want the payment released under the reversing number", reversal)
	}
	// The released money is an advance like any other: refunding it moves the fund back.
	payment := reversal.Released[0].SettledByID
	if _, err := domains.Debt.PostRefund(ctx, owner, newKey(), debt.RefundInput{PaymentID: &payment, MethodID: method}); err != nil {
		t.Fatalf("refund the released advance: %v", err)
	}

	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		var balance money.Money
		if err := q.QueryRowxContext(ctx, `SELECT balance FROM fund_accounts WHERE id = $1`, fund).Scan(&balance); err != nil {
			return err
		}
		if !balance.IsZero() {
			t.Errorf("cash fund = %s, want 0 after the refund", balance)
		}
		// The verifiers of tally ledger verify.
		for name, verify := range map[string]func(context.Context, db.Querier) ([]string, error){
			"debt": debt.Verify, "cash": cash.Verify, "inventory": inventory.Verify,
		} {
			mismatches, err := verify(ctx, q)
			if err != nil {
				return err
			}
			if len(mismatches) > 0 {
				t.Errorf("%s verify: %v", name, mismatches)
			}
		}
		return nil
	})
}
