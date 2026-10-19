//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// reportingStack is the HTTP stack with every module plus the domains behind it, so a test can
// run the worker's jobs (outbox consumers, notification sweep, exports) for one store.
type reportingStack struct {
	stack
	clk     *clock.Fixed
	domains app.Domains
	objects *memStore
}

// newReportingStack builds the stack over an in-memory object store; model answers analytics
// questions, nil leaves the Q&A unconfigured.
func newReportingStack(t *testing.T, model analytics.ModelClient, extra ...app.DomainOption) reportingStack {
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
	objects := newMemStore()
	entitlements := statusEntitlements{db: testkit.SuperDB(t)}
	opts := []app.DomainOption{app.WithEntitlements(entitlements), app.WithObjectStore(objects), app.WithMailer(capture)}
	if model != nil {
		opts = append(opts, app.WithModel(model))
	}
	opts = append(opts, extra...)
	domains := app.NewDomains(deps.DB, clk, deps.Audit, opts...)
	orgSvc := org.NewService(org.Deps{
		DB: deps.DB, Clock: clk, Audit: deps.Audit, Exceptions: exception.NewGuard(clk),
		Provisioner: org.NewProvisioner(org.NewSlugPolicy(nil), deps.Audit, clk, domains.Seeders()), Objects: objects,
		AttachmentOwners: domains.AttachmentOwners(),
	})
	a, err := app.New(app.Options{
		PublicBaseURL: testBaseURL,
		Logger:        slog.New(slog.DiscardHandler),
		Ports:         app.Ports{Sessions: idSvc, TenantHosts: idSvc, Entitlements: entitlements},
		CSRFKey:       []byte("0123456789abcdef0123456789abcdef"),
		Now:           clk.Now,
		APIRateLimit:  testAPIRateLimit,
		Modules: app.Modules{
			Identity: identity.NewHandler(idSvc), Org: org.NewHandler(orgSvc), Reasons: org.NewReasonHandler(orgSvc),
			Catalog: catalog.NewHandler(domains.Catalog), Partner: partner.NewHandler(domains.Partner),
			Pricing: pricing.NewHandler(domains.Pricing), Inventory: inventory.NewHandler(domains.Inventory),
			Debt: debt.NewHandler(domains.Debt), Cash: cash.NewHandler(domains.Cash),
			Procurement: procurement.NewHandler(domains.Procurement), Sales: sales.NewHandler(domains.Sales),
			Kits: kits.NewHandler(domains.Kits), Campaign: campaign.NewHandler(domains.Campaign),
			CRM: crm.NewHandler(domains.CRM), Notification: notification.NewHandler(domains.Notification),
			Reporting: reporting.NewHandler(domains.Reporting), Analytics: analytics.NewHandler(domains.Analytics),
			Printing: printing.NewHandler(domains.Printing),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return reportingStack{stack: stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk: clk, domains: domains, objects: objects}
}

// drainOutbox runs the CRM and notification consumers over the pending outbox events of one
// store, as the worker does, and marks them processed. Other stores' events stay pending.
func (s reportingStack) drainOutbox(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	handlers := map[string][]outbox.Handler{}
	for _, m := range []map[string]outbox.Handler{s.domains.CRM.Handlers(), s.domains.Notification.Handlers()} {
		for topic, h := range m {
			handlers[topic] = append(handlers[topic], h)
		}
	}
	topics := make([]string, 0, len(handlers))
	for topic := range handlers {
		topics = append(topics, topic)
	}
	n := 0
	ctx := context.Background()
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		var msgs []outbox.Message
		if err := sqlx.SelectContext(ctx, q, &msgs, `
			SELECT id, tenant_id, topic, payload, attempts, created_at FROM outbox_events
			WHERE processed_at IS NULL AND topic = ANY($1) ORDER BY available_at, id`, topics); err != nil {
			return err
		}
		for _, msg := range msgs {
			for _, h := range handlers[msg.Topic] {
				if err := h(ctx, q, msg); err != nil {
					return fmt.Errorf("%s: %w", msg.Topic, err)
				}
			}
			if _, err := q.ExecContext(ctx, `UPDATE outbox_events SET processed_at = $2 WHERE id = $1`, msg.ID, s.clk.Now()); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n
}

// memStore is an in-memory object store; download URLs point at a host no test calls.
type memStore struct {
	mu      sync.Mutex
	objects map[string]memObject
}

type memObject struct {
	data        []byte
	contentType string
}

func newMemStore() *memStore { return &memStore{objects: map[string]memObject{}} }

const memStoreURL = "https://objects.test/"

func (m *memStore) Put(_ context.Context, key string, r io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("memstore: put %s: %d bytes, declared %d", key, len(data), size)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{data: data, contentType: contentType}
	return nil
}

func (m *memStore) PresignPut(_ context.Context, key, contentType string, _ int64, ttl time.Duration) (objectstore.PresignedRequest, error) {
	return objectstore.PresignedRequest{Method: http.MethodPut, URL: memStoreURL + key,
		Headers: http.Header{"Content-Type": {contentType}}, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (m *memStore) PresignGet(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return memStoreURL + key, nil
}

func (m *memStore) Stat(_ context.Context, key string) (objectstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return objectstore.ObjectInfo{}, objectstore.ErrNotFound
	}
	return objectstore.ObjectInfo{Key: key, Size: int64(len(o.data)), ContentType: o.contentType}, nil
}

func (m *memStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(o.data)), nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memStore) Copy(_ context.Context, srcKey, dstKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[srcKey]
	if !ok {
		return objectstore.ErrNotFound
	}
	m.objects[dstKey] = o
	return nil
}

// object returns the content stored under key.
func (m *memStore) object(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	return o.data, ok
}

type reportOut struct {
	ReportKey   string               `json:"report_key"`
	Scope       string               `json:"scope"`
	Notes       []string             `json:"notes"`
	Provisional bool                 `json:"provisional"`
	Rows        []map[string]*string `json:"rows"`
	Totals      map[string]*string   `json:"totals"`
	NextCursor  *string              `json:"next_cursor"`
}

// reportPages reads every page of a paged report, limit rows at a time.
func reportPages(t *testing.T, b *browser, query string, limit int) []reportOut {
	t.Helper()
	sep := "?"
	if strings.Contains(query, "?") {
		sep = "&"
	}
	base := query + sep + "limit=" + strconv.Itoa(limit)
	pages := []reportOut{report(t, b, base)}
	for pages[len(pages)-1].NextCursor != nil {
		if len(pages) > 50 {
			t.Fatalf("%s does not end", query)
		}
		pages = append(pages, report(t, b, base+"&cursor="+url.QueryEscape(*pages[len(pages)-1].NextCursor)))
	}
	return pages
}

// report reads a report; query is the path after /reports/ with its parameters.
func report(t *testing.T, b *browser, query string) reportOut {
	t.Helper()
	return send[reportOut](t, b, http.MethodGet, "/reports/"+query, nil)
}

// rowOf is the report row whose code is code.
func (r reportOut) rowOf(t *testing.T, code string) map[string]*string {
	t.Helper()
	for _, row := range r.Rows {
		if c := row["code"]; c != nil && *c == code {
			return row
		}
	}
	t.Fatalf("%s has no row %s: %+v", r.ReportKey, code, r.Rows)
	return nil
}

// num reads a report cell as a number; an empty cell is zero.
func num(t *testing.T, cell *string) decimal.Decimal {
	t.Helper()
	if cell == nil {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(*cell)
	if err != nil {
		t.Fatalf("cell %q: %v", *cell, err)
	}
	return d
}

func wantNum(t *testing.T, what string, cell *string, want string) {
	t.Helper()
	if got := num(t, cell); !got.Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

type campaignOut struct {
	ID      uuid.UUID `json:"id"`
	Status  string    `json:"status"`
	Version int       `json:"version"`
}

// activeCampaign creates a campaign of kind over starts..ends and activates it.
func activeCampaign(t *testing.T, b *browser, name, kind, starts, ends string) uuid.UUID {
	t.Helper()
	c := send[campaignOut](t, b, http.MethodPost, "/campaigns", map[string]any{
		"name": name, "kind": kind, "starts_on": starts, "ends_on": ends})
	rec := b.doWith(http.MethodPost, v1+"/campaigns/"+c.ID.String()+":activate", nil, ifMatchOf(c.Version))
	expect(t, rec, http.StatusOK, campaign.StatusActive)
	return c.ID
}

// creditSale is a checkout of lines on the customer's account, nothing paid.
func creditSale(t *testing.T, b *browser, customer uuid.UUID, lines ...map[string]any) salesDocOut {
	t.Helper()
	return checkout(t, b, map[string]any{"customer_party_id": customer, "lines": lines, "payments": []map[string]any{}}).Document
}

func TestCampaignAttributionNeverCountsALineTwice(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	tet := activeCampaign(t, a, "Tết 2026", campaign.KindTet, "2026-03-01", "2026-03-31")
	promo := activeCampaign(t, a, "Khuyến mãi giữa tháng", campaign.KindPromotion, "2026-03-10", "2026-03-20")

	sku := st.newSku(t, a, "BANH-CHUNG", "10000")
	st.receive(t, a, sku, nil, "10", "6000")
	sale := creditSale(t, a, st.customer, saleLine(sku, "2", nil))
	primaries := func() []uuid.UUID {
		var out []uuid.UUID
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			return sqlx.SelectContext(context.Background(), q, &out, `
				SELECT campaign_id FROM campaign_attributions
				WHERE doc_type = 'sales_document' AND doc_id = $1 AND role = 'primary'`, sale.ID)
		})
		return out
	}
	// Tết outranks the promotion covering the same day.
	if got := primaries(); len(got) != 1 || got[0] != tet {
		t.Fatalf("primary attributions at posting = %v, want the Tết campaign once", got)
	}
	season := func(id uuid.UUID) decimal.Decimal {
		return num(t, report(t, a, "season?campaign_id="+id.String()).Totals["net_revenue"])
	}

	rec := a.do(http.MethodPost, v1+"/campaign-attributions", map[string]any{
		"campaign_id": promo, "doc_type": campaign.DocSalesDocument, "doc_id": sale.ID, "role": campaign.RoleTag})
	expect(t, rec, http.StatusOK, "")
	if tetNet, promoNet := season(tet), season(promo); !tetNet.Equal(decimal.NewFromInt(20000)) || !promoNet.IsZero() {
		t.Errorf("after the tag: Tết %s, promotion %s; want 20000 and 0, a tag is never counted", tetNet, promoNet)
	}

	rec = a.do(http.MethodPost, v1+"/campaign-attributions", map[string]any{
		"campaign_id": promo, "doc_type": campaign.DocSalesDocument, "doc_id": sale.ID, "role": campaign.RolePrimary})
	expect(t, rec, http.StatusOK, "")
	if got := primaries(); len(got) != 1 || got[0] != promo {
		t.Fatalf("primary attributions after moving the sale = %v, want the promotion once", got)
	}
	if tetNet, promoNet := season(tet), season(promo); !tetNet.IsZero() || !promoNet.Equal(decimal.NewFromInt(20000)) {
		t.Errorf("after the move: Tết %s, promotion %s; want 0 and 20000", tetNet, promoNet)
	}

	// A document dated outside the campaign is moved with a warning.
	later := activeCampaign(t, a, "Hè 2026", campaign.KindSeason, "2026-06-01", "2026-08-31")
	rec = a.do(http.MethodPost, v1+"/campaign-attributions", map[string]any{
		"campaign_id": later, "doc_type": campaign.DocSalesDocument, "doc_id": sale.ID, "role": campaign.RolePrimary})
	expect(t, rec, http.StatusOK, campaign.WarnOutsidePeriod)
	total := season(tet).Add(season(promo)).Add(season(later))
	if !total.Equal(decimal.NewFromInt(20000)) {
		t.Errorf("season totals add up to %s, want the sale counted once (20000)", total)
	}
}

type fundOut struct {
	ID      uuid.UUID `json:"id"`
	Kind    string    `json:"kind"`
	Balance string    `json:"balance"`
	Version int       `json:"version"`
}

type dashboardOut struct {
	Cash struct {
		In      string `json:"in"`
		Out     string `json:"out"`
		Balance string `json:"balance"`
	} `json:"cash"`
	Debt struct {
		Receivable struct {
			Balance  string `json:"balance"`
			Increase string `json:"increase"`
			Top      []struct {
				PartyID uuid.UUID `json:"party_id"`
				Amount  string    `json:"amount"`
			} `json:"top"`
		} `json:"receivable"`
	} `json:"debt"`
}

func TestDashboardAndStockReportsOverHTTP(t *testing.T) {
	s := newReportingStack(t, nil)
	owner := s.signUp(t)
	a := s.signIn(t, owner)
	st := buildSalesStore(t, a)

	// The cash fund opens with 1,000,000 and the customer with an opening debt of 3,000,000:
	// both are balances, not movements of the period.
	openCashFund(t, a, "1000000", "2026-03-14")
	openingReceivable(t, a, st.customer, "3000000", "2026-03-01")

	sku := st.newSku(t, a, "NUOC-MAM", "10000")
	st.receive(t, a, sku, nil, "10", "8000")
	checkout(t, a, st.cashSale("20000", saleLine(sku, "2", nil)))
	creditSale(t, a, st.customer, saleLine(sku, "5", nil))

	t.Run("the dashboard counts movements of the period, not opening balances", func(t *testing.T) {
		d := send[dashboardOut](t, a, http.MethodGet, "/reports/dashboard?period=month&date=2026-03-15", nil)
		var funds decimal.Decimal
		for _, f := range send[dataOf[fundOut]](t, a, http.MethodGet, "/funds", nil).Data {
			funds = funds.Add(decimal.RequireFromString(f.Balance))
		}
		if d.Cash.In != "20000" || d.Cash.Out != "0" || d.Cash.Balance != "1020000" || !funds.Equal(decimal.NewFromInt(1020000)) {
			t.Errorf("cash card = %+v, funds hold %s; want in 20000, out 0, balance 1020000", d.Cash, funds)
		}
		r := d.Debt.Receivable
		if r.Increase != "50000" || r.Balance != "3050000" {
			t.Errorf("receivable card = %+v, want increase 50000 (opening excluded) and balance 3050000", r)
		}
		// The walk-in sale was paid in full: no walk-in debtor shows among the top debtors.
		if len(r.Top) != 1 || r.Top[0].PartyID != st.customer {
			t.Errorf("top debtors = %+v, want the customer alone", r.Top)
		}
	})

	t.Run("revenue counts posted sales, gross margin reads their cost", func(t *testing.T) {
		rev := report(t, a, "revenue?from=2026-03-15&to=2026-03-15")
		wantNum(t, "revenue", rev.Totals["net_revenue"], "70000")
		wantNum(t, "documents", rev.Totals["documents"], "2")
		margin := report(t, a, "gross-margin?from=2026-03-15&to=2026-03-15")
		wantNum(t, "cogs", margin.Totals["cogs"], "56000")
		wantNum(t, "gross margin", margin.Totals["gross_margin"], "14000")
	})

	// Two days later more stock comes in and goes out.
	s.clk.Advance(48 * time.Hour)
	a = s.signIn(t, owner)
	st.receive(t, a, sku, nil, "5", "9000")
	checkout(t, a, st.cashSale("40000", saleLine(sku, "4", nil)))

	t.Run("NXT opening plus in minus out is closing, and periods chain", func(t *testing.T) {
		first := report(t, a, "nxt?from=2026-03-01&to=2026-03-15")
		second := report(t, a, "nxt?from=2026-03-16&to=2026-03-17")
		whole := report(t, a, "nxt?from=2026-03-01&to=2026-03-17")
		for _, r := range []reportOut{first, second, whole} {
			for _, row := range r.Rows {
				for _, k := range []string{"qty", "value"} {
					open, in, out, closing := num(t, row["opening_"+k]), num(t, row["in_"+k]), num(t, row["out_"+k]), num(t, row["closing_"+k])
					if !open.Add(in).Sub(out).Equal(closing) {
						t.Errorf("%s %s %s: %s + %s - %s != %s", r.ReportKey, *row["code"], k, open, in, out, closing)
					}
				}
			}
		}
		a1, a2, all := first.rowOf(t, "NUOC-MAM"), second.rowOf(t, "NUOC-MAM"), whole.rowOf(t, "NUOC-MAM")
		wantNum(t, "closing 15/03", a1["closing_qty"], "3")
		wantNum(t, "closing value 15/03", a1["closing_value"], "24000")
		for _, k := range []string{"qty", "value"} {
			if !num(t, a2["opening_"+k]).Equal(num(t, a1["closing_"+k])) {
				t.Errorf("opening %s 16/03 = %s, closing 15/03 = %s", k, *a2["opening_"+k], *a1["closing_"+k])
			}
			if !num(t, all["closing_"+k]).Equal(num(t, a2["closing_"+k])) || !num(t, all["opening_"+k]).Equal(num(t, a1["opening_"+k])) {
				t.Errorf("whole range %s = %v, want the two periods chained", k, all)
			}
		}
		wantNum(t, "in 01–17/03", all["in_qty"], "15")
		wantNum(t, "out 01–17/03", all["out_qty"], "11")
		wantNum(t, "closing 17/03", all["closing_qty"], "4")
	})
}

func TestRevenueCountsDeliveriesNotOrdersAndMarginIsLabelledProvisional(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "GAO-ST25", "10000")
	st.receive(t, a, sku, nil, "10", "8000")

	// An order (invoice) of 6, delivered as 2 then 3; 1 of the first delivery comes back.
	rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(sku, "6", nil)}})
	expect(t, rec, http.StatusCreated, "")
	inv := testkit.DecodeJSON[invoiceOut](t, rec)
	rec = a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version)))
	expect(t, rec, http.StatusOK, "")
	inv = testkit.DecodeJSON[invoiceOut](t, rec)
	if inv.TotalAmount != "60000" {
		t.Fatalf("invoice = %+v", inv)
	}
	deliver := func(qty string) salesDocOut {
		rec := post(a, "/invoices/"+inv.ID.String()+"/stock-outs", map[string]any{
			"lines": []map[string]any{{"invoice_line_id": inv.Lines[0].ID, "base_qty": qty}}})
		expect(t, rec, http.StatusCreated, "")
		out := testkit.DecodeJSON[stockOutOut](t, rec)
		if out.Document == nil {
			t.Fatal("stock out without its sales document")
		}
		return *out.Document
	}
	first, second := deliver("2"), deliver("3")
	expect(t, post(a, "/sales-returns", map[string]any{"sales_document_id": first.ID, "reason_id": st.returnReason,
		"lines": []map[string]any{{"sales_document_line_id": first.Lines[0].ID, "qty": "1"}}}), http.StatusCreated, "")

	rev := report(t, a, "revenue?group_by=document")
	if !strings.Contains(rev.Scope, "đơn đặt hàng") {
		t.Errorf("revenue scope = %q, want it to say order totals are out", rev.Scope)
	}
	wantNum(t, "net revenue", rev.Totals["net_revenue"], "40000")
	wantNum(t, "sales", rev.Totals["sales_amount"], "50000")
	wantNum(t, "returns", rev.Totals["returns_amount"], "10000")
	wantNum(t, "first delivery", rev.rowOf(t, first.DocNo)["net_revenue"], "10000")
	wantNum(t, "second delivery", rev.rowOf(t, second.DocNo)["net_revenue"], "30000")
	if len(rev.Rows) != 2 {
		t.Errorf("revenue rows = %+v, want the two deliveries only", rev.Rows)
	}

	margin := report(t, a, "gross-margin")
	wantNum(t, "cogs", margin.Totals["cogs"], "32000")
	wantNum(t, "gross margin", margin.Totals["gross_margin"], "8000")
	if margin.Provisional {
		t.Errorf("gross margin is provisional without a pending recost: %+v", margin.Notes)
	}

	// A recost waiting for the owner may still change the cost of these sales.
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `
			INSERT INTO cost_recalculation_runs (id, tenant_id, sku_ids, from_date, status, impact, run_version, requested_by, created_at)
			VALUES ($1, $2, $3, '2026-03-01', 'previewed', '{}', 1, $1, $4)`, ids.New(), st.tenant, []uuid.UUID{sku}, s.clk.Now())
		return err
	})
	margin = report(t, a, "gross-margin")
	if !margin.Provisional || len(margin.Notes) == 0 {
		t.Errorf("gross margin with a pending recost = provisional %v, notes %v", margin.Provisional, margin.Notes)
	}
}

type noticeOut struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	DedupeKey  string     `json:"dedupe_key"`
	EntityID   *uuid.UUID `json:"entity_id"`
	ActionPath *string    `json:"action_path"`
	DocNo      *string    `json:"doc_no"`
	ResolvedAt *time.Time `json:"resolved_at"`
	ResolvedBy *string    `json:"resolved_by"`
}

func TestBelowMinStockIsRaisedOnceAndResolvedByTheSystem(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := send[productRef](t, a, http.MethodPost, "/products", map[string]any{"type": "goods", "name": "Dầu ăn",
		"sku": map[string]any{"code": "DAU-AN", "base_unit_id": st.units["cái"], "min_stock": "5"}}).Skus[0].ID
	send[map[string]any](t, a, http.MethodPost, "/skus/"+sku.String()+"/sale-price",
		map[string]any{"kind": "retail", "price": "50000", "source": "catalog"})
	st.receive(t, a, sku, nil, "10", "40000")
	checkout(t, a, st.cashSale("300000", saleLine(sku, "6", nil)))

	dedupe := notification.KindBelowMinStock + ":" + sku.String()
	notices := func(state string) []noticeOut {
		var out []noticeOut
		for _, n := range send[dataOf[noticeOut]](t, a, http.MethodGet, "/notifications?state="+state, nil).Data {
			if n.DedupeKey == dedupe {
				out = append(out, n)
			}
		}
		return out
	}
	s.drainOutbox(t, st.tenant)
	for range 2 {
		if _, err := s.domains.Notification.Sweep(context.Background(), st.tenant); err != nil {
			t.Fatal(err)
		}
	}
	s.drainOutbox(t, st.tenant)
	got := notices("active")
	if len(got) != 1 || got[0].Kind != notification.KindBelowMinStock {
		t.Fatalf("active notices = %+v, want one below-min-stock notice", got)
	}
	if want := "/inventory/stock-card?sku=" + sku.String(); got[0].ActionPath == nil || *got[0].ActionPath != want {
		t.Errorf("action path = %v, want %s", got[0].ActionPath, want)
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM notifications WHERE dedupe_key = $1`, dedupe); n != 1 {
		t.Errorf("notification rows = %d, want 1 after the consumers and two sweeps", n)
	}

	st.receive(t, a, sku, nil, "6", "40000")
	s.drainOutbox(t, st.tenant)
	if got := notices("active"); len(got) != 0 {
		t.Errorf("active notices after restocking = %+v", got)
	}
	resolved := notices("resolved")
	if len(resolved) != 1 || resolved[0].ResolvedAt == nil || resolved[0].ResolvedBy == nil || *resolved[0].ResolvedBy != "system" {
		t.Errorf("resolved notices = %+v, want one resolved by the system", resolved)
	}
	if _, err := s.domains.Notification.Sweep(context.Background(), st.tenant); err != nil {
		t.Fatal(err)
	}
	if got := notices("active"); len(got) != 0 {
		t.Errorf("a sweep raised the resolved notice again: %+v", got)
	}
}

// Overdue debts and parked offline sales are raised and cleared by the events that change them,
// not only by the hourly sweep; a sweep never clears a notice raised after it started.
func TestNoticesFollowDebtAndSyncExceptionEvents(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "MUOI", "10000")
	st.receive(t, a, sku, nil, "10", "6000")
	find := func(dedupe string) *noticeOut {
		t.Helper()
		for _, n := range send[dataOf[noticeOut]](t, a, http.MethodGet, "/notifications?state=active", nil).Data {
			if n.DedupeKey == dedupe {
				return &n
			}
		}
		return nil
	}
	active := func(dedupe string) bool { t.Helper(); return find(dedupe) != nil }
	// opens checks the web route and document number a notice links to.
	opens := func(t *testing.T, n *noticeOut, path string, docNo *string) {
		t.Helper()
		if n.ActionPath == nil || *n.ActionPath != path {
			t.Errorf("%s action path = %v, want %s", n.Kind, n.ActionPath, path)
		}
		if (n.DocNo == nil) != (docNo == nil) || (docNo != nil && *n.DocNo != *docNo) {
			t.Errorf("%s doc no = %v, want %v", n.Kind, n.DocNo, docNo)
		}
	}
	sweep := func() {
		t.Helper()
		if _, err := s.domains.Notification.Sweep(context.Background(), st.tenant); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("an overdue debt is raised by its obligation and cleared by its payment", func(t *testing.T) {
		sale := creditSale(t, a, st.customer, saleLine(sku, "2", nil))
		var ob struct {
			ID     uuid.UUID `db:"id"`
			Amount string    `db:"amount"`
			DocNo  string    `db:"source_doc_no"`
		}
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			if _, err := q.ExecContext(context.Background(),
				`UPDATE obligations SET due_date = DATE '2026-01-01' WHERE source_id = $1`, sale.ID); err != nil {
				return err
			}
			return q.QueryRowxContext(context.Background(),
				`SELECT id, amount::text AS amount, source_doc_no FROM obligations WHERE source_id = $1`, sale.ID).StructScan(&ob)
		})
		dedupe := notification.KindReceivableOverdue + ":" + st.customer.String()
		s.drainOutbox(t, st.tenant)
		n := find(dedupe)
		if n == nil {
			t.Fatal("the overdue debt raised no notice")
		}
		opens(t, n, "/debts/receivables?party="+st.customer.String(), &ob.DocNo)
		expect(t, post(a, "/payments", map[string]any{"direction": "in", "party_id": st.customer, "purpose": "settlement",
			"payment_method_id": st.cashMethod, "amount": ob.Amount,
			"allocations": []map[string]any{{"obligation_id": ob.ID, "amount": ob.Amount}}}), http.StatusCreated, "")
		s.drainOutbox(t, st.tenant)
		if active(dedupe) {
			t.Error("the paid debt is still notified")
		}
	})

	t.Run("a parked offline sale is raised and cleared when it is discarded", func(t *testing.T) {
		rec := a.do(http.MethodGet, v1+"/tenant/settings", nil)
		expect(t, rec, http.StatusOK, "")
		expect(t, a.doWith(http.MethodPatch, v1+"/tenant/settings", map[string]any{"offline_pos_enabled": true,
			"offline_pos_max_amount": "5000000", "offline_pos_max_hours": 12}, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
			http.StatusOK, "")
		expect(t, a.do(http.MethodPost, v1+"/pos/offline-session", nil), http.StatusOK, "offline_until")
		items := send[struct {
			Items []offlineItemOut `json:"items"`
		}](t, a, http.MethodPost, "/pos/offline-sync", map[string]any{"sales": []map[string]any{{
			"offline_client_id": "muoi-001", "created_at": s.clk.Now().Add(-24 * time.Hour),
			"lines":    []map[string]any{saleLine(sku, "1", nil)},
			"payments": []map[string]any{{"payment_method_id": st.cashMethod, "amount": "10000"}}}}}).Items
		if len(items) != 1 || items[0].ExceptionID == nil {
			t.Fatalf("late upload = %+v", items)
		}
		dedupe := notification.KindPOSSyncException + ":" + items[0].ExceptionID.String()
		s.drainOutbox(t, st.tenant)
		n := find(dedupe)
		if n == nil {
			t.Fatal("the parked sale raised no notice")
		}
		opens(t, n, "/sales/pos?exceptions=open", nil)
		expect(t, a.do(http.MethodPost, v1+"/pos/sync-exceptions/"+items[0].ExceptionID.String()+":discard", nil), http.StatusOK, "")
		s.drainOutbox(t, st.tenant)
		if active(dedupe) {
			t.Error("the discarded sale is still notified")
		}
	})

	t.Run("an expiring lot loses its notice when the lot sells out", func(t *testing.T) {
		milk := send[productRef](t, a, http.MethodPost, "/products", map[string]any{
			"type": "goods", "name": "Sữa tươi",
			"sku": map[string]any{"code": "SUA", "base_unit_id": st.units["cái"], "track_lot": true, "track_expiry": true},
		}).Skus[0].ID
		send[map[string]any](t, a, http.MethodPost, "/skus/"+milk.String()+"/sale-price",
			map[string]any{"kind": "retail", "price": "30000", "source": "catalog"})
		postReceipt(t, a, map[string]any{"supplier_party_id": st.supplier, "lines": []map[string]any{{"sku_id": milk,
			"qty": "2", "unit_price": "20000", "lot_no": "S-01", "expiry_date": s.clk.Now().AddDate(0, 0, 5).Format(time.DateOnly)}}})
		var lot uuid.UUID
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			return q.QueryRowxContext(context.Background(), `SELECT id FROM lots WHERE sku_id = $1`, milk).Scan(&lot)
		})
		dedupe := notification.KindLotExpiring + ":" + lot.String()
		s.drainOutbox(t, st.tenant)
		sweep()
		n := find(dedupe)
		if n == nil {
			t.Fatal("the expiring lot raised no notice")
		}
		lotNo := "S-01"
		opens(t, n, "/inventory/lots", &lotNo)
		checkout(t, a, st.cashSale("60000", saleLine(milk, "2", nil)))
		s.drainOutbox(t, st.tenant)
		if active(dedupe) {
			t.Error("the sold-out lot is still notified")
		}
	})

	t.Run("a sweep leaves a notice raised after it started for the next sweep", func(t *testing.T) {
		ghost := ids.New()
		dedupe := notification.KindReceivableOverdue + ":" + ghost.String()
		// Raised by a consumer that read newer state than this sweep's scan.
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			return notification.Raise(context.Background(), q, notification.Notice{
				Kind: notification.KindReceivableOverdue, DedupeKey: dedupe, Severity: notification.SeverityWarn, Title: "Quá hạn",
			}, s.clk.Now().Add(time.Minute))
		})
		sweep()
		if !active(dedupe) {
			t.Fatal("the sweep cleared a notice raised after it started")
		}
		s.clk.Advance(2 * time.Minute)
		sweep()
		if active(dedupe) {
			t.Error("the next sweep left a notice whose condition does not hold")
		}
	})
}

type reminderOut struct {
	Duplicate bool   `json:"duplicate"`
	Email     string `json:"email"`
}

type loyaltyEntryOut struct {
	ID         uuid.UUID `json:"id"`
	Points     string    `json:"points"`
	Kind       string    `json:"kind"`
	SourceType string    `json:"source_type"`
}

type loyaltyOut struct {
	Balance string            `json:"balance"`
	Entries []loyaltyEntryOut `json:"entries"`
}

func TestCustomerConsentGatesEmailAndLoyaltyFollowsReturns(t *testing.T) {
	s := newReportingStack(t, nil)
	owner := s.signUp(t)
	a := s.signIn(t, owner)
	st := buildSalesStore(t, a)
	const email = "mai@example.test"
	mai := send[withID](t, a, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Chị Mai", "email": email}).ID
	sku := st.newSku(t, a, "NUOC-NGOT", "10000")
	st.receive(t, a, sku, nil, "40", "6000")

	t.Run("an email reminder goes out only under the latest granted consent", func(t *testing.T) {
		creditSale(t, a, mai, saleLine(sku, "2", nil))
		remind := func() reminderOut {
			rec := post(a, "/parties/"+mai.String()+"/reminders", map[string]any{"channel": "email"})
			if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
				t.Fatalf("reminder = %d %s", rec.Code, rec.Body)
			}
			return testkit.DecodeJSON[reminderOut](t, rec)
		}
		consent := func(granted bool) {
			send[map[string]any](t, a, http.MethodPost, "/customers/"+mai.String()+"/consents", map[string]any{
				"channel": crm.ChannelEmail, "purpose": crm.PurposeDebtReminder, "granted": granted, "source": "Tại quầy"})
		}
		nextDay := func() {
			s.clk.Advance(24 * time.Hour)
			a = s.signIn(t, owner)
		}

		if got := remind(); got.Email != debt.EmailSkippedNoConsent {
			t.Errorf("reminder without consent = %+v", got)
		}
		consent(true)
		nextDay()
		if got := remind(); got.Email != debt.EmailSent || len(s.mail.to(email)) != 1 {
			t.Errorf("reminder with consent = %+v, mails %d", got, len(s.mail.to(email)))
		}
		if got := remind(); !got.Duplicate || len(s.mail.to(email)) != 1 {
			t.Errorf("second reminder of the day = %+v, mails %d", got, len(s.mail.to(email)))
		}
		consent(false)
		nextDay()
		if got := remind(); got.Email != debt.EmailSkippedNoConsent || len(s.mail.to(email)) != 1 {
			t.Errorf("reminder after the refusal = %+v, mails %d", got, len(s.mail.to(email)))
		}
	})

	t.Run("loyalty points are earned once and a return takes back its share", func(t *testing.T) {
		s.drainOutbox(t, st.tenant)
		startLoyalty(t, a)
		sale := creditSale(t, a, st.customer, saleLine(sku, "10", nil))
		loyalty := func() loyaltyOut {
			return send[loyaltyOut](t, a, http.MethodGet, "/customers/"+st.customer.String()+"/loyalty", nil)
		}
		s.drainOutbox(t, st.tenant)
		if got := loyalty(); got.Balance != "100" {
			t.Fatalf("loyalty after a 100000 sale = %+v, want 100 points", got)
		}
		expect(t, post(a, "/sales-returns", map[string]any{"sales_document_id": sale.ID, "reason_id": st.returnReason,
			"lines": []map[string]any{{"sales_document_line_id": sale.Lines[0].ID, "qty": "5"}}}), http.StatusCreated, "")
		s.drainOutbox(t, st.tenant)
		got := loyalty()
		if got.Balance != "50" || len(got.Entries) != 2 {
			t.Fatalf("loyalty after returning half = %+v, want 50 points over two entries", got)
		}
		if !slices.ContainsFunc(got.Entries, func(e loyaltyEntryOut) bool { return e.Points == "-50" }) {
			t.Errorf("entries = %+v, want a reversal of 50", got.Entries)
		}
		// Redelivered events change nothing.
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			_, err := q.ExecContext(context.Background(), `UPDATE outbox_events SET processed_at = NULL`)
			return err
		})
		s.drainOutbox(t, st.tenant)
		if again := loyalty(); again.Balance != "50" || len(again.Entries) != 2 {
			t.Errorf("loyalty after redelivery = %+v", again)
		}
		gift := map[string]any{"party_id": st.customer, "points": "-10", "reason": "Đổi quà"}
		expect(t, a.do(http.MethodPost, v1+"/loyalty/adjustments", gift), http.StatusUnprocessableEntity,
			"idempotency_key_required")
		expect(t, post(a, "/loyalty/adjustments", map[string]any{"party_id": st.customer, "points": "-60",
			"reason": "Đổi quà"}), http.StatusConflict, "insufficient_points")
		// A double submit under one key adjusts once and answers the same entry twice.
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		first := a.doWith(http.MethodPost, v1+"/loyalty/adjustments", gift, key)
		expect(t, first, http.StatusCreated, "manual")
		again := a.doWith(http.MethodPost, v1+"/loyalty/adjustments", gift, key)
		expect(t, again, http.StatusCreated, "manual")
		if again.Header().Get("Idempotent-Replay") != "true" ||
			testkit.DecodeJSON[withID](t, again).ID != testkit.DecodeJSON[withID](t, first).ID {
			t.Errorf("double submit = %s %s, want a replay of %s", again.Header(), again.Body, first.Body)
		}
		if adjusted := loyalty(); adjusted.Balance != "40" || len(adjusted.Entries) != 3 {
			t.Errorf("loyalty after a manual adjustment = %+v", adjusted)
		}
	})
}

type exportOut struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at"`
	DownloadPath *string    `json:"download_path"`
}

func TestExportsExpireAndReportsStayInTheirStore(t *testing.T) {
	s := newReportingStack(t, nil)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	storeA, storeB := buildSalesStore(t, a), buildSalesStore(t, b)
	sku := storeA.newSku(t, a, "CA-PHE", "50000")
	storeA.receive(t, a, sku, nil, "10", "30000")
	sale := checkout(t, a, storeA.cashSale("100000", saleLine(sku, "2", nil))).Document

	rec := a.do(http.MethodPost, v1+"/export-jobs", map[string]any{"report_key": "nxt", "params": map[string]string{"from": "2026-03-01"}})
	expect(t, rec, http.StatusAccepted, reporting.ExportQueued)
	job := testkit.DecodeJSON[exportOut](t, rec)
	path := v1 + "/export-jobs/" + job.ID.String()
	expect(t, a.do(http.MethodGet, path+"/download", nil), http.StatusConflict, reporting.CodeExportNotDone)

	t.Run("another store sees neither the job nor the figures", func(t *testing.T) {
		expect(t, b.do(http.MethodGet, path, nil), http.StatusNotFound, "")
		expect(t, b.do(http.MethodGet, path+"/download", nil), http.StatusNotFound, "")
		if rev := report(t, b, "revenue?group_by=document"); len(rev.Rows) != 0 || !num(t, rev.Totals["net_revenue"]).IsZero() {
			t.Errorf("store B revenue = %+v", rev)
		}
		if nxt := report(t, b, "nxt"); slices.ContainsFunc(nxt.Rows, func(r map[string]*string) bool {
			return r["code"] != nil && *r["code"] == "CA-PHE"
		}) {
			t.Errorf("store B NXT shows store A's goods: %+v", nxt.Rows)
		}
		wantNum(t, "store A revenue", report(t, a, "revenue?group_by=document").rowOf(t, sale.DocNo)["net_revenue"], "100000")
	})

	if _, err := s.domains.Reporting.RunExports(context.Background(), storeB.tenant, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if got := send[exportOut](t, a, http.MethodGet, "/export-jobs/"+job.ID.String(), nil); got.Status != reporting.ExportQueued {
		t.Fatalf("store B's run touched store A's job: %+v", got)
	}
	rec = a.do(http.MethodPost, v1+"/export-jobs", map[string]any{"report_key": "revenue", "params": map[string]string{}})
	expect(t, rec, http.StatusAccepted, reporting.ExportQueued)
	dropped := v1 + "/export-jobs/" + testkit.DecodeJSON[exportOut](t, rec).ID.String()
	expect(t, a.do(http.MethodPost, dropped+":cancel", nil), http.StatusOK, reporting.ExportCancelled)

	t.Run("the export list holds the user's own jobs, newest first, a page at a time", func(t *testing.T) {
		type exportPage struct {
			Data       []exportOut `json:"data"`
			NextCursor *string     `json:"next_cursor"`
		}
		first := send[exportPage](t, a, http.MethodGet, "/export-jobs?limit=1", nil)
		if len(first.Data) != 1 || first.NextCursor == nil {
			t.Fatalf("first page = %+v", first)
		}
		second := send[exportPage](t, a, http.MethodGet, "/export-jobs?limit=1&cursor="+*first.NextCursor, nil)
		if len(second.Data) != 1 || second.NextCursor != nil {
			t.Fatalf("second page = %+v", second)
		}
		if v1+"/export-jobs/"+first.Data[0].ID.String() != dropped || second.Data[0].ID != job.ID {
			t.Errorf("pages = %+v then %+v, want the newer export first", first.Data, second.Data)
		}
		if other := send[exportPage](t, b, http.MethodGet, "/export-jobs", nil); len(other.Data) != 0 {
			t.Errorf("store B lists store A's exports: %+v", other.Data)
		}
	})
	if n, err := s.domains.Reporting.RunExports(context.Background(), storeA.tenant, slog.New(slog.DiscardHandler)); err != nil || n != 1 {
		t.Fatalf("run exports = %d, %v; want the cancelled export skipped", n, err)
	}
	expect(t, a.do(http.MethodGet, dropped, nil), http.StatusOK, reporting.ExportCancelled)
	expect(t, a.do(http.MethodPost, path+":cancel", nil), http.StatusConflict, reporting.CodeExportState)
	done := send[exportOut](t, a, http.MethodGet, "/export-jobs/"+job.ID.String(), nil)
	if done.Status != reporting.ExportSucceeded || done.DownloadPath == nil || done.ExpiresAt == nil ||
		!done.ExpiresAt.Equal(s.clk.Now().Add(reporting.ExportLinkTTL)) {
		t.Fatalf("finished export = %+v", done)
	}
	rec = a.do(http.MethodGet, path+"/download", nil)
	expect(t, rec, http.StatusFound, "")
	location := rec.Header().Get("Location")
	csv, ok := s.objects.object(strings.TrimPrefix(location, memStoreURL))
	if !strings.HasPrefix(location, memStoreURL) || !ok || !strings.Contains(string(csv), "CA-PHE") {
		t.Errorf("download %q holds %q", location, csv)
	}
	if !strings.Contains(strings.TrimPrefix(location, memStoreURL), storeA.tenant.String()) {
		t.Errorf("export key %q is not under store A", location)
	}

	s.clk.Advance(reporting.ExportLinkTTL + time.Second)
	a = s.signIn(t, ownerA)
	expect(t, a.do(http.MethodGet, path+"/download", nil), http.StatusGone, reporting.CodeExportExpired)
	if got := send[exportOut](t, a, http.MethodGet, "/export-jobs/"+job.ID.String(), nil); got.DownloadPath != nil {
		t.Errorf("expired export still offers a download: %+v", got)
	}
}

func TestPagedReportsAndTheirExportsAgree(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	for i, code := range []string{"BANH-A", "BANH-B", "BANH-C", "BANH-D", "BANH-E"} {
		sku := st.newSku(t, a, code, "10000")
		st.receive(t, a, sku, nil, "20", "7000")
		checkout(t, a, st.cashSale(strconv.Itoa((i+1)*10000), saleLine(sku, strconv.Itoa(i+1), nil)))
	}

	t.Run("NXT returns its totals with the first page and every row once", func(t *testing.T) {
		pages := reportPages(t, a, "nxt?from=2026-03-01&to=2026-03-15", 2)
		if len(pages) != 3 {
			t.Fatalf("pages = %d, want 3 of at most 2 rows", len(pages))
		}
		if pages[0].Totals == nil || pages[1].Totals != nil || pages[2].Totals != nil {
			t.Errorf("totals by page = %v, %v, %v; want the first page only", pages[0].Totals, pages[1].Totals, pages[2].Totals)
		}
		var codes []string
		closing := decimal.Zero
		for _, p := range pages {
			for _, r := range p.Rows {
				codes = append(codes, *r["code"])
				closing = closing.Add(num(t, r["closing_qty"]))
			}
		}
		if !slices.Equal(codes, []string{"BANH-A", "BANH-B", "BANH-C", "BANH-D", "BANH-E"}) {
			t.Errorf("rows = %v", codes)
		}
		wantNum(t, "closing total", pages[0].Totals["closing_qty"], closing.String())
		wantNum(t, "out total", pages[0].Totals["out_qty"], "15")
	})

	t.Run("revenue by product carries the totals of every group on each page", func(t *testing.T) {
		pages := reportPages(t, a, "revenue?from=2026-03-15&to=2026-03-15&group_by=product", 2)
		var codes []string
		share := decimal.Zero
		for _, p := range pages {
			wantNum(t, "net revenue total", p.Totals["net_revenue"], "150000")
			for _, r := range p.Rows {
				codes = append(codes, *r["code"])
				share = share.Add(num(t, r["share"]))
			}
		}
		if !slices.Equal(codes, []string{"BANH-E", "BANH-D", "BANH-C", "BANH-B", "BANH-A"}) {
			t.Errorf("rows by revenue = %v", codes)
		}
		if share.Sub(decimal.NewFromInt(100)).Abs().GreaterThan(decimal.RequireFromString("0.1")) {
			t.Errorf("shares add up to %s, want 100", share)
		}
	})

	t.Run("an export writes every row and the totals once", func(t *testing.T) {
		var jobs []string
		for _, key := range []string{"nxt", "revenue"} {
			params := map[string]string{"from": "2026-03-01", "to": "2026-03-15"}
			if key == "revenue" {
				params["group_by"] = "product"
			}
			rec := a.do(http.MethodPost, v1+"/export-jobs", map[string]any{"report_key": key, "params": params})
			expect(t, rec, http.StatusAccepted, reporting.ExportQueued)
			jobs = append(jobs, testkit.DecodeJSON[exportOut](t, rec).ID.String())
		}
		if n, err := s.domains.Reporting.RunExports(context.Background(), st.tenant, slog.New(slog.DiscardHandler)); err != nil || n != 2 {
			t.Fatalf("run exports = %d, %v", n, err)
		}
		for i, id := range jobs {
			rec := a.do(http.MethodGet, v1+"/export-jobs/"+id+"/download", nil)
			expect(t, rec, http.StatusFound, "")
			data, ok := s.objects.object(strings.TrimPrefix(rec.Header().Get("Location"), memStoreURL))
			if !ok {
				t.Fatalf("export %d has no file", i)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			// The header, five goods and the totals.
			if len(lines) != 7 || !strings.HasPrefix(lines[6], "Tổng cộng") {
				t.Errorf("export %d = %q", i, lines)
			}
		}
	})
}

// salesQAModel asks for today's revenue by document, then answers by naming the documents.
type salesQAModel struct {
	mu       sync.Mutex
	requests []analytics.Request
}

func (m *salesQAModel) Model() string { return "test-model" }

func (m *salesQAModel) Complete(_ context.Context, req analytics.Request) (analytics.Reply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	last := req.Messages[len(req.Messages)-1]
	if last.Role != analytics.RoleTool {
		return analytics.Reply{ToolCalls: []analytics.ToolCall{{ID: "c1", Name: "revenue",
			Arguments: map[string]string{"from": "2026-03-15", "to": "2026-03-15", "group_by": "document"}}}}, nil
	}
	var result struct {
		Rows []map[string]*string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(last.Content), &result); err != nil {
		return analytics.Reply{}, err
	}
	var codes []string
	for _, r := range result.Rows {
		if r["code"] != nil {
			codes = append(codes, *r["code"])
		}
	}
	return analytics.Reply{Content: "Hôm nay bán " + strings.Join(codes, ", ")}, nil
}

// seen reports whether any message sent to the model contains s.
func (m *salesQAModel) seen(s string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, req := range m.requests {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, s) {
				return true
			}
		}
	}
	return false
}

type questionOut struct {
	Answer    *string `json:"answer"`
	Model     string  `json:"model"`
	Citations []struct {
		ReportKey string            `json:"report_key"`
		Params    map[string]string `json:"params"`
		Refs      []string          `json:"refs"`
	} `json:"citations"`
}

func TestAnalyticsQuestionsAreGatedAndCiteTheirDocuments(t *testing.T) {
	question := map[string]any{"question": "Hôm nay bán được những đơn nào?"}

	t.Run("without a configured model the feature is disabled", func(t *testing.T) {
		s := newReportingStack(t, nil)
		a := s.signIn(t, s.signUp(t))
		expect(t, a.do(http.MethodPost, v1+"/analytics/questions", question), http.StatusForbidden, analytics.CodeFeatureDisabled)
	})

	model := &salesQAModel{}
	s := newReportingStack(t, model)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "TRA-XANH", "15000")
	st.receive(t, a, sku, nil, "10", "9000")
	walkIn := checkout(t, a, st.cashSale("15000", saleLine(sku, "1", nil))).Document
	lan := creditSale(t, a, st.customer, saleLine(sku, "2", nil))

	expect(t, a.do(http.MethodPost, v1+"/analytics/questions", question), http.StatusForbidden, analytics.CodeFeatureDisabled)
	// A store without the Q&A hears that first, whatever the question.
	expect(t, a.do(http.MethodPost, v1+"/analytics/questions", map[string]any{"question": " "}),
		http.StatusForbidden, analytics.CodeFeatureDisabled)
	enableAI(t, st.tenant, false)
	// The setting alone is not enough: the subscription must carry the entitlement.
	expect(t, a.do(http.MethodPost, v1+"/analytics/questions", question), http.StatusForbidden, analytics.CodeFeatureDisabled)
	enableAI(t, st.tenant, true)
	expect(t, a.do(http.MethodPost, v1+"/analytics/questions", map[string]any{"question": " "}),
		http.StatusUnprocessableEntity, analytics.CodeInvalidQuestion)
	if n := countRows(t, st.tenant, `SELECT count(*) FROM analytics_queries`); n != 0 {
		t.Fatalf("refused questions were logged: %d", n)
	}

	rec := a.do(http.MethodPost, v1+"/analytics/questions", question)
	expect(t, rec, http.StatusCreated, "")
	got := testkit.DecodeJSON[questionOut](t, rec)
	if got.Answer == nil || !strings.Contains(*got.Answer, walkIn.DocNo) || !strings.Contains(*got.Answer, lan.DocNo) {
		t.Fatalf("answer = %v, want both documents of today", got.Answer)
	}
	if len(got.Citations) != 1 || got.Citations[0].ReportKey != "revenue" || got.Citations[0].Params["group_by"] != "document" ||
		!slices.Contains(got.Citations[0].Refs, walkIn.DocNo) || !slices.Contains(got.Citations[0].Refs, lan.DocNo) {
		t.Errorf("citations = %+v", got.Citations)
	}
	if model.seen("Chị Lan") {
		t.Error("the customer's name reached the model")
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM analytics_queries`); n != 1 {
		t.Errorf("logged questions = %d, want 1", n)
	}
	if list := send[dataOf[questionOut]](t, a, http.MethodGet, "/analytics/questions", nil).Data; len(list) != 1 || list[0].Model != "test-model" {
		t.Errorf("question log = %+v", list)
	}
}

// enableAI turns the Q&A on in the store's settings and, with entitled, adds the subscription
// entitlement.
func enableAI(t *testing.T, tenant uuid.UUID, entitled bool) {
	t.Helper()
	super := testkit.SuperDB(t)
	if _, err := super.ExecContext(context.Background(),
		`UPDATE tally.tenant_settings SET analytics_ai_enabled = true WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	if !entitled {
		return
	}
	if _, err := super.ExecContext(context.Background(), `
		INSERT INTO tally.entitlements (id, tenant_id, key, value, valid_from, created_at)
		VALUES ($1, $2, $3, 'true'::jsonb, '2026-03-01', now())`, ids.New(), tenant, analytics.EntitlementKey); err != nil {
		t.Fatal(err)
	}
}

// stallingModel never answers: it waits for its call to be cancelled.
type stallingModel struct{}

func (stallingModel) Model() string { return "stalling-model" }

func (stallingModel) Complete(ctx context.Context, _ analytics.Request) (analytics.Reply, error) {
	<-ctx.Done()
	return analytics.Reply{}, ctx.Err()
}

func TestAnalyticsQuestionPastItsDeadlineTimesOut(t *testing.T) {
	s := newReportingStack(t, stallingModel{}, app.WithQuestionDeadline(200*time.Millisecond))
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	enableAI(t, st.tenant, true)

	start := time.Now()
	rec := a.do(http.MethodPost, v1+"/analytics/questions", map[string]any{"question": "Hôm nay bán được bao nhiêu?"})
	expect(t, rec, http.StatusGatewayTimeout, analytics.CodeTimeout)
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the question ran for %s", waited)
	}
	// The question is still logged, unanswered.
	list := send[dataOf[questionOut]](t, a, http.MethodGet, "/analytics/questions", nil).Data
	if len(list) != 1 || list[0].Answer != nil || list[0].Model != "stalling-model" {
		t.Errorf("question log = %+v", list)
	}
}

type printDocOut struct {
	Label    string `json:"label"`
	DocType  string `json:"doc_type"`
	DocNo    string `json:"doc_no"`
	Version  *int   `json:"version"`
	Supplier *struct {
		Name string `json:"name"`
	} `json:"supplier"`
	Customer *struct {
		Name string `json:"name"`
	} `json:"customer"`
	Groups []struct {
		Lines []struct {
			SkuCode *string `json:"sku_code"`
			Color   *string `json:"color"`
			LotNo   *string `json:"lot_no"`
		} `json:"lines"`
	} `json:"groups"`
	Related []struct {
		DocType string `json:"doc_type"`
	} `json:"related"`
	Statement *struct {
		Direction string `json:"direction"`
	} `json:"statement"`
	KitLots []struct {
		LotNo      string `json:"lot_no"`
		Components []struct {
			LotNo string `json:"lot_no"`
		} `json:"components"`
	} `json:"kit_lots"`
}

func TestPrintPayloadsOfPurchaseStockDebtAndKitDocuments(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	red := st.colors["do"]
	printed := func(docType string, id uuid.UUID, query string) printDocOut {
		t.Helper()
		d := send[printDocOut](t, a, http.MethodGet, "/print/"+docType+"/"+id.String()+query, nil)
		if d.Label != printing.Label || d.DocType != docType || d.DocNo == "" {
			t.Errorf("%s header = %+v", docType, d)
		}
		return d
	}
	lineCount := func(d printDocOut) int {
		n := 0
		for _, g := range d.Groups {
			n += len(g.Lines)
		}
		return n
	}

	order := confirmedOrder(t, a, map[string]any{"supplier_party_id": st.supplier, "expected_date": "2026-03-20",
		"lines": []map[string]any{{"sku_id": st.ribbon, "unit_id": st.units["cái"], "qty": "5", "unit_price": "2000", "color_id": red}}})
	po := printed(printing.DocPurchaseOrder, order.ID, "")
	if po.Supplier == nil || po.Supplier.Name != "Xưởng may Bình Tân" || lineCount(po) != 1 || po.Groups[0].Lines[0].Color == nil {
		t.Errorf("purchase order = %+v", po)
	}

	receipt := receiveOrder(t, a, order, "5")
	pn := printed(printing.DocGoodsReceipt, receipt.ID, "")
	if lineCount(pn) != 1 || len(pn.Related) != 1 || pn.Related[0].DocType != printing.DocPurchaseOrder {
		t.Errorf("goods receipt = %+v", pn)
	}

	returnReason := send[dataOf[withID]](t, a, http.MethodGet, "/reasons?kind=purchase_return", nil).Data[0].ID
	rec := post(a, "/purchase-returns", map[string]any{"receipt_id": receipt.ID, "reason_id": returnReason,
		"settlement": procurement.SettlementCreditPayable, "lines": []map[string]any{{"receipt_line_id": receipt.Lines[0].ID, "qty": "1"}}})
	expect(t, rec, http.StatusCreated, "")
	xt := printed(printing.DocPurchaseReturn, testkit.DecodeJSON[returnOut](t, rec).ID, "")
	if lineCount(xt) != 1 || len(xt.Related) != 1 || xt.Related[0].DocType != printing.DocGoodsReceipt {
		t.Errorf("purchase return = %+v", xt)
	}

	label := st.newSku(t, a, "TEM-NHAN", "1000")
	rec = post(a, "/stock-adjustments", map[string]any{"kind": inventory.KindOpening,
		"lines": []map[string]any{{"sku_id": label, "qty": "10", "unit_cost": "500"}}})
	expect(t, rec, http.StatusCreated, "")
	dc := printed(printing.DocStockAdjustment, testkit.DecodeJSON[withID](t, rec).ID, "")
	if lineCount(dc) != 1 {
		t.Errorf("stock adjustment = %+v", dc)
	}

	count := send[withID](t, a, http.MethodPost, "/stock-counts", map[string]any{"scope": inventory.ScopeSelection, "sku_ids": []uuid.UUID{label}})
	if kk := printed(printing.DocStockCount, count.ID, ""); lineCount(kk) != 1 {
		t.Errorf("stock count = %+v", kk)
	}

	creditSale(t, a, st.customer, saleLine(label, "2", nil))
	rec = a.do(http.MethodPost, v1+"/statements", map[string]any{
		"party_id": st.customer, "direction": "receivable", "period_from": "2026-03-01", "period_to": "2026-03-15"})
	expect(t, rec, http.StatusCreated, "draft")
	statement := testkit.DecodeJSON[withID](t, rec).ID
	expect(t, a.doWith(http.MethodPost, v1+"/statements/"+statement.String()+":send", nil, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
		http.StatusOK, "sent")
	bk := printed(printing.DocStatement, statement, "?version=1")
	if bk.Version == nil || *bk.Version != 1 || bk.Statement == nil || bk.Statement.Direction != "receivable" ||
		bk.Customer == nil || bk.Customer.Name != "Chị Lan" {
		t.Errorf("statement version 1 = %+v", bk)
	}
	expect(t, a.do(http.MethodGet, v1+"/print/statement/"+statement.String()+"?version=9", nil), http.StatusNotFound, "")

	rec = a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(st.ribbon, "1", &red)}})
	expect(t, rec, http.StatusCreated, "")
	inv := testkit.DecodeJSON[invoiceOut](t, rec)
	expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK, "")
	if hd := printed(sales.PrintInvoice, inv.ID, "?version=1"); hd.Version == nil || *hd.Version != 1 || lineCount(hd) != 1 {
		t.Errorf("invoice version 1 = %+v", hd)
	}
	expect(t, a.do(http.MethodGet, v1+"/print/"+printing.DocGoodsReceipt+"/"+receipt.ID.String()+"?version=1", nil),
		http.StatusUnprocessableEntity, "")

	kit := createKit(t, a, st.giftBasket("GIO-TET"))
	assembly, _ := st.assemble(t, a, kit, "2", red)
	kitLabel := printed(printing.DocKitLabel, assembly.ID, "")
	if len(kitLabel.KitLots) != 1 || !slices.ContainsFunc(kitLabel.KitLots[0].Components, func(c struct {
		LotNo string `json:"lot_no"`
	}) bool {
		return c.LotNo == "KS-01"
	}) {
		t.Errorf("kit label = %+v, want one kit lot holding candy lot KS-01", kitLabel.KitLots)
	}
}
