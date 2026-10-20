//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/seedload"
	"github.com/g0-up/tally/apps/api/internal/support"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// The tenant isolation suite reads the operation list from the OpenAPI documents, so a route
// added to the API is swept without anyone remembering to list it: every operation is called by a
// signed-in owner of store A (or of the empty prober store C) with the ids of store B in its path,
// query and body, and must answer exactly what it answers for ids that exist nowhere, never leak
// an id of B, and never change B.

// apiSpec is an OpenAPI document as plain YAML values.
type apiSpec struct{ doc map[string]any }

func loadSpec(t *testing.T, name string) apiSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return apiSpec{doc: doc}
}

// deref follows local $refs to the object they name.
func (s apiSpec) deref(node any) map[string]any {
	m, _ := node.(map[string]any)
	for m != nil {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var cur any = s.doc
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			next, _ := cur.(map[string]any)
			cur = next[part]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

type specParam struct {
	name, in string
	required bool
	schema   map[string]any
}

type specOp struct {
	method, path string
	public       bool
	params       []specParam
	body         map[string]any
}

func (o specOp) String() string { return o.method + " " + o.path }

// specMethods are the HTTP methods an OpenAPI path item may hold.
var specMethods = []string{"get", "put", "post", "patch", "delete"}

func (s apiSpec) operations() []specOp {
	paths, _ := s.doc["paths"].(map[string]any)
	var out []specOp
	for path, item := range paths {
		item := s.deref(item)
		for _, method := range specMethods {
			raw, ok := item[method]
			if !ok {
				continue
			}
			op := s.deref(raw)
			o := specOp{method: strings.ToUpper(method), path: path}
			// An operation is public when its own security names no session cookie.
			if sec, ok := op["security"].([]any); ok {
				o.public = true
				for _, req := range sec {
					for scheme := range s.deref(req) {
						if strings.Contains(strings.ToLower(scheme), "session") {
							o.public = false
						}
					}
				}
			}
			params, _ := op["parameters"].([]any)
			for _, p := range params {
				p := s.deref(p)
				name, _ := p["name"].(string)
				in, _ := p["in"].(string)
				req, _ := p["required"].(bool)
				o.params = append(o.params, specParam{name: name, in: in, required: req || in == "path", schema: s.deref(p["schema"])})
			}
			if rb := s.deref(op["requestBody"]); rb != nil {
				content, _ := rb["content"].(map[string]any)
				if media := s.deref(content["application/json"]); media != nil {
					o.body = s.deref(media["schema"])
				}
			}
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].method < out[j].method
	})
	return out
}

// isoStore is one store filled with at least one record of every kind a route can name.
type isoStore struct {
	owner  owner
	b      *browser
	tenant uuid.UUID
	// kinds holds one id per kind: a table name, or customers, suppliers and kits.
	kinds map[string]uuid.UUID
	// pairs holds a parent and child id of the same store, keyed by the child kind.
	pairs map[string][2]uuid.UUID
	// all is every id the store owns, its tenant id included.
	all map[uuid.UUID]string
	// invoiceNumber is the number of the store's SaaS invoice.
	invoiceNumber string
	// sessionID is the store owner's support session.
	supportSession uuid.UUID
}

// fillIsolationStore creates a store and fills it through its services and its API: a generated
// load (catalog, parties, receipts, sales, invoices, returns, payments, counts, kits and lots),
// then one record of each kind the load leaves out.
func fillIsolationStore(t *testing.T, s controlStack, adm *browser, adminID uuid.UUID, index int) isoStore {
	t.Helper()
	ctx := context.Background()
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := isoStore{owner: o, b: b, tenant: tenantOf(t, b)}
	super := testkit.SuperDB(t)
	var ownerID uuid.UUID
	if err := super.Get(&ownerID, `SELECT id FROM tally.users WHERE tenant_id = $1`, st.tenant); err != nil {
		t.Fatal(err)
	}
	orgSvc := org.NewService(org.Deps{DB: testkit.DB(t), Clock: s.clk, Audit: audit.NewRecorder(s.clk)})
	if _, err := seedload.Run(ctx, seedload.Deps{DB: testkit.DB(t), Domains: s.domains, Org: orgSvc},
		seedload.Options{Tenants: 1, Skus: seedload.MinSkus, Moves: 150, Seed: int64(index), SlugPrefix: "iso"},
		seedload.Tenant{Index: 1, ID: st.tenant, OwnerID: ownerID, Slug: o.slug}, nil); err != nil {
		t.Fatalf("seed load: %v", err)
	}

	cat := buildCatalogStore(t, b)
	pr := buildPricingStore(t, b, "PRC-1")
	sku, customer, supplier := pr.sku, cat.customer, cat.supplier
	var cashMethod uuid.UUID
	for _, m := range send[dataOf[struct {
		ID   uuid.UUID `json:"id"`
		Code string    `json:"code"`
	}]](t, b, http.MethodGet, "/payment-methods", nil).Data {
		if m.Code == "cash" {
			cashMethod = m.ID
		}
	}
	reasonOf := func(kind string, n int) uuid.UUID {
		t.Helper()
		rs := send[dataOf[withID]](t, b, http.MethodGet, "/reasons?kind="+kind, nil).Data
		for len(rs) <= n {
			rs = append(rs, send[withID](t, b, http.MethodPost, "/reasons", map[string]any{"kind": kind, "name": "Lý do " + unique()}))
		}
		return rs[n].ID
	}
	created := func(rec *httptest.ResponseRecorder, what string) uuid.UUID {
		t.Helper()
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK && rec.Code != http.StatusAccepted {
			t.Fatalf("%s = %d %s", what, rec.Code, rec.Body)
		}
		return testkit.DecodeJSON[withID](t, rec).ID
	}

	// Purchasing: a receipt with a final price, its supplier invoice and an order.
	receipt := postReceipt(t, b, map[string]any{"supplier_party_id": supplier,
		"lines": []map[string]any{{"sku_id": sku, "qty": "20", "unit_price": "8000"}}})
	created(post(b, "/purchase-price-adjustments", map[string]any{"receipt_id": receipt.ID, "kind": procurement.KindFinalPrice,
		"reason_id": reasonOf("price_adjustment", 0), "lines": []map[string]any{{"receipt_line_id": receipt.Lines[0].ID,
			"new_unit_price": "7500"}}}), "price adjustment")
	created(b.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": supplier,
		"document_no": "HD-ISO", "document_date": "2026-03-15", "goods_amount": "150000", "tax_amount": "0",
		"receipt_ids": []uuid.UUID{receipt.ID}}), "supplier document")
	confirmedOrder(t, b, map[string]any{"supplier_party_id": supplier,
		"lines": []map[string]any{{"sku_id": sku, "qty": "4", "unit_price": "8000"}}})

	// Selling: a quote, a cart, a reservation, an exchange of a sale and an offline sale parked
	// as a sync exception.
	created(b.do(http.MethodPost, v1+"/quotes", map[string]any{"customer_party_id": customer, "valid_until": "2026-03-31",
		"lines": []map[string]any{saleLine(sku, "1", nil)}}), "quote")
	created(b.do(http.MethodPost, v1+"/pos/carts", map[string]any{"label": "Bàn 1", "lines": []map[string]any{saleLine(sku, "1", nil)}}), "cart")
	created(post(b, "/reservations", map[string]any{"sku_id": sku, "qty": "1", "note": "Giữ cho chị Lan"}), "reservation")
	created(post(b, "/stock-adjustments", map[string]any{"kind": inventory.KindOpening,
		"lines": []map[string]any{{"sku_id": sku, "qty": "1", "unit_cost": "8000"}}}), "stock adjustment")
	sale := checkout(t, b, map[string]any{"lines": []map[string]any{saleLine(sku, "2", nil)},
		"payments": []map[string]any{{"payment_method_id": cashMethod, "amount": "19000"}}})
	exchange := map[string]any{
		"original_sales_document_id": sale.Document.ID, "reason_id": reasonOf("sales_return", 1),
		"return_lines":      []map[string]any{{"sales_document_line_id": sale.Document.Lines[0].ID, "qty": "1"}},
		"lines":             []map[string]any{saleLine(sku, "1", nil)},
		"payment_method_id": cashMethod,
	}
	exchange["expected_customer_pays_amount"] = send[exchangeSplitOut](t, b, http.MethodPost, "/exchanges:preview", exchange).CustomerPays
	created(post(b, "/exchanges", exchange), "exchange")
	parked := send[struct {
		Items []offlineItemOut `json:"items"`
	}](t, b, http.MethodPost, "/pos/offline-sync", map[string]any{"sales": []map[string]any{{
		"offline_client_id": "iso-" + unique(), "created_at": s.clk.Now().Add(-24 * time.Hour),
		"lines":    []map[string]any{saleLine(sku, "1", nil)},
		"payments": []map[string]any{{"payment_method_id": cashMethod, "amount": "9500"}}}}}).Items
	if len(parked) != 1 || parked[0].ExceptionID == nil {
		t.Fatalf("late offline sale = %+v, want a sync exception", parked)
	}

	// Debt: an offset of a receivable and a payable backed by a clean attachment, a statement and
	// a collection note.
	obligation := func(direction string) uuid.UUID {
		t.Helper()
		rec := post(b, "/debt-adjustments", map[string]any{"party_id": customer, "direction": direction,
			"kind": debt.AdjustIncrease, "amount": "400000", "reason_id": reasonOf("debt_adjustment", 0)})
		if rec.Code != http.StatusCreated {
			t.Fatalf("debt adjustment = %d %s", rec.Code, rec.Body)
		}
		return testkit.DecodeJSON[struct {
			Obligation withID `json:"obligation"`
		}](t, rec).Obligation.ID
	}
	receivable, payable := obligation(debt.Receivable), obligation(debt.Payable)
	record, _ := declareAttachment(t, b, debt.AttachmentOwnerParty, customer, []byte("%PDF-1.4 bien ban "+ids.New().String()))
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `UPDATE attachments SET scan_status = 'clean' WHERE id = $1`, record)
		return err
	})
	created(post(b, "/debt-offsets", map[string]any{"party_id": customer, "receivable_obligation_id": receivable,
		"payable_obligation_id": payable, "amount": "400000", "attachment_id": record}), "debt offset")
	created(b.do(http.MethodPost, v1+"/statements", map[string]any{"party_id": customer, "direction": "receivable",
		"period_from": "2026-03-01", "period_to": "2026-03-15"}), "statement")
	created(b.do(http.MethodPost, v1+"/parties/"+customer.String()+"/collection-notes",
		map[string]any{"direction": "receivable", "kind": "note", "body": "Hẹn trả cuối tháng"}), "collection note")

	// Campaigns, care, reports and exports.
	created(b.do(http.MethodPost, v1+"/campaigns", map[string]any{"name": "Tết 2026", "kind": campaign.KindTet,
		"starts_on": "2026-03-15", "ends_on": "2026-03-31"}), "campaign")
	created(b.do(http.MethodPost, v1+"/care-tasks", map[string]any{"party_id": customer, "due_on": "2026-03-20",
		"title": "Gọi lại chị Lan"}), "care task")
	created(b.do(http.MethodPost, v1+"/saved-reports", map[string]any{"report_key": "revenue", "name": "Theo chứng từ",
		"config": map[string]any{"columns": []string{"code", "net_revenue"}, "group_by": "document"}}), "saved report")
	created(b.do(http.MethodPost, v1+"/export-jobs", map[string]any{"report_key": "nxt",
		"params": map[string]string{"from": "2026-03-01"}}), "export job")
	if n, err := s.domains.Reporting.RunExports(ctx, st.tenant, slog.New(slog.DiscardHandler)); err != nil || n != 1 {
		t.Fatalf("run exports = %d, %v", n, err)
	}
	// A previewed cost recalculation, written the way its service stores it, and an owner's
	// confirmation of an exception on an invoice of the load.
	var invoice struct {
		ID      uuid.UUID `db:"id"`
		Version int       `db:"version"`
	}
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO cost_recalculation_runs (id, tenant_id, sku_ids, from_date, status, impact, run_version, requested_by, created_at)
			VALUES ($1, $2, $3, '2026-03-01', 'previewed', '{}', 1, $4, $5)`, ids.New(), st.tenant, []uuid.UUID{sku}, ownerID, s.clk.Now())
		return err
	})
	if err := super.Get(&invoice, `SELECT id, version FROM tally.invoices WHERE tenant_id = $1 ORDER BY id LIMIT 1`, st.tenant); err != nil {
		t.Fatal(err)
	}
	created(b.do(http.MethodPost, v1+"/exception-confirmations", map[string]any{"kind": "below_min_price", "doc_type": "invoice",
		"doc_id": invoice.ID, "doc_version": invoice.Version, "payload_hash": strings.Repeat("ab", 32), "note": "Khách quen"}),
		"exception confirmation")
	reportingStack{domains: s.domains, clk: s.clk}.drainOutbox(t, st.tenant)
	// A phone scanner session of the owner's desktop with one scan, ended so that its event stream
	// answers at once instead of staying open.
	scannerSession := send[withID](t, b, http.MethodPost, "/scanner-sessions", nil).ID
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO scanner_scans (id, tenant_id, session_id, idempotency_key, request_hash, code, created_at, expires_at)
			VALUES ($1, $2, $3, 'iso-scan', '\x00', '8935001234567', $4, $4)`, ids.New(), st.tenant, scannerSession, s.clk.Now())
		return err
	})
	if rec := b.do(http.MethodDelete, v1+"/scanner-sessions/"+scannerSession.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("end scanner session = %d %s", rec.Code, rec.Body)
	}

	// Platform records about the store: a support ticket with a consented session, and an invoice.
	ticket := send[support.TicketDTO](t, b, http.MethodPost, "/support-tickets",
		map[string]any{"subject": "Không in được hóa đơn", "body": "Máy in báo lỗi khi in hóa đơn bán lẻ."})
	adminSend[support.TicketDTO](t, adm, http.MethodPatch, "/support-tickets/"+ticket.ID.String(),
		map[string]any{"assigned_admin_id": adminID, "status": "in_progress"})
	st.supportSession = send[support.SessionDTO](t, b, http.MethodPost, "/support-sessions",
		map[string]any{"ticket_id": ticket.ID, "modules": []string{"catalog"}, "hours": 1}).ID
	st.invoiceNumber = adminSend[saas.InvoiceDTO](t, adm, http.MethodPost, "/saas-invoices",
		map[string]any{"tenant_id": st.tenant, "plan_id": storePlanID, "amount": "250000"}).Number

	st.collectIDs(t)
	return st
}

// tenantTables are the tables that hold rows of a store.
func tenantTables(t *testing.T) []string {
	t.Helper()
	var tables []string
	if err := testkit.SuperDB(t).Select(&tables, `
		SELECT c.table_name FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'tally' AND c.column_name = 'tenant_id' AND t.table_type = 'BASE TABLE'
		ORDER BY 1`); err != nil {
		t.Fatal(err)
	}
	return tables
}

// tenantSnapshot fingerprints every row of a store, table by table.
func tenantSnapshot(t *testing.T, tenant uuid.UUID) map[string]string {
	t.Helper()
	super := testkit.SuperDB(t)
	out := map[string]string{}
	for _, table := range tenantTables(t) {
		var sum string
		if err := super.Get(&sum, `SELECT md5(COALESCE(string_agg(x::text, ',' ORDER BY x::text), ''))
			FROM tally.`+table+` x WHERE tenant_id = $1`, tenant); err != nil {
			t.Fatal(err)
		}
		out[table] = sum
	}
	return out
}

// childPairs read a parent and child of the same store, for the routes that name both, keyed by
// the child kind.
var childPairs = map[string]struct{ table, parent, child string }{
	"attribute_options": {"attribute_options", "attribute_id", "id"},
	"party_addresses":   {"party_addresses", "party_id", "id"},
	"contacts":          {"contacts", "party_id", "id"},
	"price_list_items":  {"price_list_items", "price_list_id", "id"},
	"product_images":    {"product_images", "product_id", "id"},
	"sku_barcodes":      {"sku_barcodes", "sku_id", "id"},
	"sku_units":         {"sku_units", "sku_id", "unit_id"},
	"scanner_scans":     {"scanner_scans", "session_id", "id"},
}

// collectIDs reads one id of every kind and every id the store owns, from every table with a
// tenant column.
func (st *isoStore) collectIDs(t *testing.T) {
	t.Helper()
	super := testkit.SuperDB(t)
	st.kinds, st.pairs, st.all = map[string]uuid.UUID{}, map[string][2]uuid.UUID{}, map[uuid.UUID]string{st.tenant: "tenants"}
	for _, table := range tenantTables(t) {
		var cols []string
		if err := super.Select(&cols, `SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'tally' AND table_name = $1 AND data_type = 'uuid' AND column_name <> 'tenant_id'`, table); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(cols, "id") {
			continue
		}
		var got []uuid.UUID
		if err := super.Select(&got, `SELECT id FROM tally.`+table+` WHERE tenant_id = $1 ORDER BY id`, st.tenant); err != nil {
			t.Fatal(err)
		}
		for _, id := range got {
			st.all[id] = table
		}
		if len(got) > 0 {
			st.kinds[table] = got[0]
		}
	}
	for kind, query := range map[string]string{
		"customers": `SELECT party_id FROM tally.customer_profiles WHERE tenant_id = $1 ORDER BY party_id LIMIT 1`,
		"suppliers": `SELECT party_id FROM tally.supplier_profiles WHERE tenant_id = $1 ORDER BY party_id LIMIT 1`,
		"kits":      `SELECT product_id FROM tally.kit_recipes WHERE tenant_id = $1 ORDER BY product_id LIMIT 1`,
		// A confirmed order can be received, a finished export downloaded.
		"purchase_orders": `SELECT id FROM tally.purchase_orders WHERE tenant_id = $1 ORDER BY status = 'confirmed' DESC, id LIMIT 1`,
		"export_jobs":     `SELECT id FROM tally.export_jobs WHERE tenant_id = $1 ORDER BY status = 'succeeded' DESC, id LIMIT 1`,
		// A lot made by an assembly has a genealogy.
		"lots": `SELECT l.id FROM tally.lots l WHERE l.tenant_id = $1
			ORDER BY EXISTS (SELECT 1 FROM tally.lot_genealogy g WHERE g.tenant_id = l.tenant_id AND g.parent_lot_id = l.id) DESC, l.id LIMIT 1`,
	} {
		var id uuid.UUID
		if err := super.Get(&id, query, st.tenant); err != nil {
			t.Fatalf("%s of %s: %v", kind, st.owner.slug, err)
		}
		st.kinds[kind] = id
	}
	for kind, c := range childPairs {
		var pair struct {
			Parent uuid.UUID `db:"parent"`
			Child  uuid.UUID `db:"child"`
		}
		if err := super.Get(&pair, `SELECT `+c.parent+` AS parent, `+c.child+` AS child FROM tally.`+c.table+
			` WHERE tenant_id = $1 ORDER BY 2 LIMIT 1`, st.tenant); err != nil {
			t.Fatalf("%s of %s: %v", kind, st.owner.slug, err)
		}
		st.pairs[kind] = [2]uuid.UUID{pair.Parent, pair.Child}
	}
}

// pathKinds name the kind of a path parameter by the collection before it, where the table is not
// the collection's own name.
var pathKinds = map[string]string{
	"carts":               "pos_carts",
	"sync-exceptions":     "pos_sync_exceptions",
	"funds":               "fund_accounts",
	"cost-recalculations": "cost_recalculation_runs",
	"care-tasks":          "customer_care_tasks",
	"parties":             "customers",
	"stock":               "skus",
	"options":             "attribute_options",
	"images":              "product_images",
	"barcodes":            "sku_barcodes",
	"addresses":           "party_addresses",
	"items":               "price_list_items",
	"tickets":             "support_tickets",
}

// docTypeKinds are the tables behind the print document types.
var docTypeKinds = map[string]string{
	"quote": "quotes", "invoice": "invoices", "stock_out": "stock_outs", "sales_document": "sales_documents",
	"sales_return": "sales_returns", "exchange": "exchanges", "purchase_order": "purchase_orders", "goods_receipt": "receipts",
	"purchase_return": "purchase_returns", "stock_count": "stock_counts", "stock_adjustment": "stock_adjustments",
	"statement": "statements", "kit_label": "assembly_orders",
}

// fieldKinds name the kind of a body or query id field whose stem is not a table name.
var fieldKinds = map[string]string{
	"customer_party": "customers", "supplier_party": "suppliers", "party": "customers", "kit": "kits",
	"fund": "fund_accounts", "fund_account": "fund_accounts", "ticket": "support_tickets", "user": "users",
	"assigned_user": "users", "new_owner": "users", "base_unit": "units", "pack_unit": "units", "part_unit": "units",
	"unit": "units", "source_campaign": "campaigns", "compare_campaign": "campaigns", "original_sales_document": "sales_documents",
	"customer": "customers", "supplier": "suppliers", "warehouse": "warehouses", "obligation": "obligations",
	"receivable_obligation": "obligations", "payable_obligation": "obligations", "component_sku": "skus",
	"substitute_sku": "skus", "output_lot": "lots", "lot": "lots", "color": "colors", "option": "attribute_options",
	"image": "product_images", "barcode": "sku_barcodes", "address": "party_addresses", "care_task": "customer_care_tasks",
	"sync_exception": "pos_sync_exceptions", "cart": "pos_carts", "cost_recalculation": "cost_recalculation_runs",
	"run": "cost_recalculation_runs", "attribute_set": "attribute_sets", "tax_rate": "tax_rates", "mix_group": "mix_groups",
	"price_list": "price_lists", "export_job": "export_jobs", "saved_report": "saved_reports", "stock_count": "stock_counts",
	"recipe": "kit_recipes", "assembly_order": "assembly_orders", "debt_adjustment": "debt_adjustments",
	"actor": "users", "doc": "sales_documents", "source_doc": "sales_documents", "doc_line": "sales_document_lines",
	"line": "sales_document_lines", "order": "purchase_orders", "item": "price_list_items", "parent": "categories",
	"owner": "customers", "entity": "skus", "source": "receipts", "exception_confirmation": "exception_confirmations",
}

// kindOfField picks the kind a uuid field names, by its stem, then by the shorter stems from the
// right: receivable_obligation_id names an obligation.
func kindOfField(kinds map[string]uuid.UUID, field string) (string, bool) {
	stem := strings.TrimSuffix(strings.TrimSuffix(field, "_ids"), "_id")
	if k, ok := fieldKinds[stem]; ok {
		return k, true
	}
	words := strings.Split(stem, "_")
	for i := range words {
		cand := strings.Join(words[i:], "_")
		if k, ok := fieldKinds[cand]; ok {
			return k, true
		}
		for _, k := range []string{cand, cand + "s", cand + "es", strings.TrimSuffix(cand, "y") + "ies"} {
			if _, ok := kinds[k]; ok {
				return k, true
			}
		}
	}
	return "", false
}

// patternSamples make a value of each string pattern the API documents. A value differs by n, so
// two requests that create a record do not collide on a code.
var patternSamples = map[string]func(n int) string{
	`^-?[0-9]+(\.[0-9]+)?$`:              func(int) string { return "1" },
	`^[A-Z]{2,3}-[0-9]{4,}$`:             func(n int) string { return fmt.Sprintf("PN-%04d", n) },
	`^[a-z][a-z0-9_]{0,39}$`:             func(n int) string { return fmt.Sprintf("thu_%d", n) },
	`^[0-9]{10}(-[0-9]{3})?$`:            func(n int) string { return fmt.Sprintf("%010d", n) },
	`^[0-9a-f]{64}$`:                     func(n int) string { return fmt.Sprintf("%064x", n) },
	`^[0-9a-fA-F]{64}$`:                  func(n int) string { return fmt.Sprintf("%064x", n) },
	`^[a-z0-9]+(-[a-z0-9]+)*$`:           func(n int) string { return fmt.Sprintf("thu-%d", n) },
	`^[A-Za-z0-9_-]{3,32}$`:              func(n int) string { return fmt.Sprintf("THU-%d", n) },
	`^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$`: func(n int) string { return fmt.Sprintf("THU-%d", n) },
	`^#[0-9A-Fa-f]{6}$`:                  func(int) string { return "#AA3300" },
	`^sha256=[0-9a-f]{64}$`:              func(n int) string { return fmt.Sprintf("sha256=%064x", n) },
}

// filler builds a request from a schema. Every uuid field gets an id from idOf: the ids of store
// B for the foreign request, ids that exist nowhere for the baseline one. Everything else is the
// same in both, so only the ids can make their answers differ.
type filler struct {
	t    *testing.T
	spec apiSpec
	// n tells the values of one request from another's.
	n    int
	idOf func(field string) uuid.UUID
	sent map[uuid.UUID]bool
}

func (f *filler) id(field string) uuid.UUID {
	id := f.idOf(field)
	f.sent[id] = true
	return id
}

func typeOf(schema map[string]any) string {
	switch v := schema["type"].(type) {
	case string:
		return v
	case []any:
		for _, x := range v {
			if s, _ := x.(string); s != "null" {
				return s
			}
		}
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	return ""
}

func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// hasUUID reports whether a schema is, or is an array of, uuids.
func (f *filler) hasUUID(schema map[string]any) bool {
	schema = f.spec.deref(schema)
	if typeOf(schema) == "array" {
		return f.hasUUID(f.spec.deref(schema["items"]))
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		for _, alt := range listOf(schema[k]) {
			if f.hasUUID(f.spec.deref(alt)) {
				return true
			}
		}
	}
	return schema["format"] == "uuid"
}

func listOf(v any) []any {
	l, _ := v.([]any)
	return l
}

func (f *filler) value(field string, schema map[string]any, depth int) any {
	schema = f.spec.deref(schema)
	if schema == nil || depth > 8 {
		return nil
	}
	if parts := listOf(schema["allOf"]); len(parts) > 0 {
		merged := map[string]any{}
		props := map[string]any{}
		var required []any
		for k, v := range schema {
			if k != "allOf" {
				merged[k] = v
			}
		}
		for _, p := range parts {
			p := f.spec.deref(p)
			for k, v := range p {
				switch k {
				case "properties":
					maps.Copy(props, v.(map[string]any))
				case "required":
					required = append(required, listOf(v)...)
				default:
					merged[k] = v
				}
			}
		}
		if own, ok := schema["properties"].(map[string]any); ok {
			maps.Copy(props, own)
		}
		required = append(required, listOf(schema["required"])...)
		if len(props) > 0 {
			merged["properties"] = props
		}
		merged["required"] = required
		return f.value(field, merged, depth)
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if alts := listOf(schema[k]); len(alts) > 0 {
			for _, alt := range alts {
				if alt := f.spec.deref(alt); typeOf(alt) != "null" {
					return f.value(field, alt, depth)
				}
			}
			return nil
		}
	}
	for _, v := range listOf(schema["enum"]) {
		if v != nil {
			return v
		}
	}
	if c, ok := schema["const"]; ok {
		return c
	}
	switch typeOf(schema) {
	case "object":
		out := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		required := map[string]bool{}
		for _, r := range listOf(schema["required"]) {
			required[r.(string)] = true
		}
		for _, name := range slices.Sorted(maps.Keys(props)) {
			p := f.spec.deref(props[name])
			if required[name] || f.hasUUID(p) {
				out[name] = f.value(name, p, depth+1)
			}
		}
		return out
	case "array":
		n := 1
		if m, ok := numberOf(schema["minItems"]); ok && int(m) > n {
			n = int(m)
		}
		items := make([]any, n)
		for i := range items {
			items[i] = f.value(field, f.spec.deref(schema["items"]), depth+1)
		}
		return items
	case "integer", "number":
		n := 1.0
		if m, ok := numberOf(schema["minimum"]); ok && m > n {
			n = m
		}
		if m, ok := numberOf(schema["maximum"]); ok && m < n {
			n = m
		}
		return int(n)
	case "boolean":
		return false
	case "string":
		return f.str(field, schema)
	}
	return nil
}

func (f *filler) str(field string, schema map[string]any) string {
	switch schema["format"] {
	case "uuid":
		return f.id(field).String()
	case "date":
		return "2026-03-15"
	case "date-time":
		return "2026-03-15T03:00:00Z"
	case "email":
		return fmt.Sprintf("iso-%d@example.test", f.n)
	case "uri":
		return fmt.Sprintf("https://example.test/%d", f.n)
	}
	if p, ok := schema["pattern"].(string); ok {
		if sample, ok := patternSamples[p]; ok {
			return sample(f.n)
		}
		f.t.Errorf("no sample value for the pattern %s of %s", p, field)
	}
	v := fmt.Sprintf("Thử %d", f.n)
	if strings.Contains(field, "password") {
		v = fmt.Sprintf("sai-mat-khau-%d", f.n)
	}
	if m, ok := numberOf(schema["minLength"]); ok && len(v) < int(m) {
		v += strings.Repeat("x", int(m)-len(v))
	}
	if m, ok := numberOf(schema["maxLength"]); ok && len(v) > int(m) {
		v = strconv.Itoa(f.n)
		v = v[max(0, len(v)-int(m)):]
	}
	return v
}

var (
	uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	csrfRE = regexp.MustCompile(`"csrf_token":"[^"]*"`)
)

// normalize replaces the ids a request sent, which an error may quote, and the CSRF token each
// answer of host-context carries, so the answers to the foreign and the baseline request compare
// equal when they say the same.
func normalize(body string, sent map[uuid.UUID]bool) string {
	body = csrfRE.ReplaceAllString(body, `"csrf_token":""`)
	return uuidRE.ReplaceAllStringFunc(body, func(s string) string {
		if id, err := uuid.Parse(s); err == nil && sent[id] {
			return "<sent>"
		}
		return s
	})
}

func errorCodeOf(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

var pathParamRE = regexp.MustCompile(`\{\w+\}`)

// childKinds name the kind of the second id of a path by the collection before it.
var childKinds = map[string]string{
	"options": "attribute_options", "images": "product_images", "barcodes": "sku_barcodes", "units": "sku_units",
	"contacts": "contacts", "addresses": "party_addresses", "items": "price_list_items", "scans": "scanner_scans",
}

// reportRef is a report of the catalogue with the filters it takes.
type reportRef struct {
	Slug    string `json:"slug"`
	Range   string `json:"range"`
	Filters []struct {
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		Values   []string `json:"values"`
		Min      *int     `json:"min"`
		Required bool     `json:"required"`
	} `json:"filters"`
}

// isoCall is an operation with every path parameter that does not name a record set: one call per
// print document type, suggestion kind and report.
type isoCall struct {
	op    specOp
	label string
	path  string
	// kinds are the kinds of the ids left in the path, in order; a second id is a child of the first.
	kinds []string
	// docKind is the kind the print document type of the path names.
	docKind string
	query   url.Values
	// queryIDs are the report filters that name records.
	queryIDs []string
}

func callsOf(t *testing.T, op specOp, reports []reportRef) []isoCall {
	t.Helper()
	calls := []isoCall{{op: op, label: op.String(), path: op.path, query: url.Values{}}}
	expand := func(param string, values []string, each func(c *isoCall, i int)) {
		var next []isoCall
		for _, c := range calls {
			for i, v := range values {
				n := c
				n.path = strings.Replace(c.path, param, v, 1)
				n.kinds = slices.Clone(c.kinds)
				n.query = maps.Clone(c.query)
				if len(values) > 1 {
					n.label += " " + v
				}
				if each != nil {
					each(&n, i)
				}
				next = append(next, n)
			}
		}
		calls = next
	}
	segs := strings.Split(op.path, "/")
	for i, seg := range segs {
		param := pathParamRE.FindString(seg)
		if param == "" {
			continue
		}
		name := strings.Trim(param, "{}")
		var p specParam
		for _, q := range op.params {
			if q.in == "path" && q.name == name {
				p = q
			}
		}
		if p.schema == nil {
			t.Fatalf("%s: the path parameter %s is not documented", op, name)
		}
		var enum []string
		for _, v := range listOf(p.schema["enum"]) {
			enum = append(enum, fmt.Sprint(v))
		}
		prev := segs[i-1]
		switch {
		case p.schema["format"] == "uuid":
			for j := range calls {
				c := &calls[j]
				kind := pathKinds[prev]
				switch {
				case len(c.kinds) == 1:
					kind = childKinds[prev]
				case strings.HasPrefix(prev, "{"):
					kind = c.docKind
				case kind == "":
					kind = strings.ReplaceAll(prev, "-", "_")
				}
				if kind == "" {
					t.Fatalf("%s: no kind for the id after %s", op, prev)
				}
				c.kinds = append(c.kinds, kind)
			}
		case name == "docType":
			expand(param, enum, func(c *isoCall, i int) {
				if c.docKind = docTypeKinds[enum[i]]; c.docKind == "" {
					t.Fatalf("the print document type %s has no table", enum[i])
				}
			})
		case name == "kind":
			expand(param, enum, nil)
		case name == "stepKey":
			expand(param, enum[:1], nil)
		case name == "n" || name == "version":
			expand(param, []string{"1"}, nil)
		case name == "provider":
			expand(param, []string{testProvider}, nil)
		case name == "reportKey":
			var slugs []string
			for _, r := range reports {
				slugs = append(slugs, r.Slug)
			}
			expand(param, slugs, func(c *isoCall, i int) {
				r := reports[i]
				switch r.Range {
				case "period":
					c.query.Set("from", "2026-03-01")
					c.query.Set("to", "2026-03-15")
				case "as_of":
					c.query.Set("as_of", "2026-03-15")
				case "future":
					c.query.Set("days", "30")
				}
				for _, f := range r.Filters {
					switch {
					case f.Kind == "uuid" || f.Kind == "uuid_list":
						c.queryIDs = append(c.queryIDs, f.Name)
					case !f.Required:
					case f.Kind == "enum" && len(f.Values) > 0:
						c.query.Set(f.Name, f.Values[0])
					case f.Kind == "int":
						c.query.Set(f.Name, strconv.Itoa(max(1, ptrOr(f.Min))))
					default:
						c.query.Set(f.Name, "thu")
					}
				}
			})
		default:
			t.Fatalf("%s: no value for the path parameter %s", op, name)
		}
	}
	return calls
}

func ptrOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// pathWith puts args in the id parameters of the path, in order.
func (c isoCall) pathWith(args []uuid.UUID) string {
	i := 0
	return pathParamRE.ReplaceAllStringFunc(c.path, func(string) string {
		i++
		return args[i-1].String()
	})
}

// isoAnswer is the answer to one request and every id the request sent.
type isoAnswer struct {
	rec  *httptest.ResponseRecorder
	sent map[uuid.UUID]bool
}

func (a isoAnswer) ok() bool { return a.rec.Code >= 200 && a.rec.Code < 300 }

func (a isoAnswer) String() string {
	body := a.rec.Body.String()
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return strconv.Itoa(a.rec.Code) + " " + body
}

// queryValue writes a filled value as a query parameter; a list is comma separated.
func queryValue(v any) string {
	if l, ok := v.([]any); ok {
		parts := make([]string, len(l))
		for i, x := range l {
			parts[i] = fmt.Sprint(x)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

// request makes the request of the call from b, with args in its path; idOf fills every id of
// its query and body, n every other value. It sends every required query parameter, every query
// parameter that names a record, an If-Match and an Idempotency-Key.
func (c isoCall) request(t *testing.T, sp apiSpec, b *browser, args []uuid.UUID, n int, idOf func(string) uuid.UUID) isoAnswer {
	t.Helper()
	f := &filler{t: t, spec: sp, n: n, idOf: idOf, sent: map[uuid.UUID]bool{}}
	for _, id := range args {
		f.sent[id] = true
	}
	q := url.Values{}
	for k, v := range c.query {
		q[k] = slices.Clone(v)
	}
	for _, name := range c.queryIDs {
		q.Set(name, f.id(name).String())
	}
	for _, p := range c.op.params {
		if p.in == "query" && !q.Has(p.name) && (p.required || f.hasUUID(p.schema)) {
			q.Set(p.name, queryValue(f.value(p.name, p.schema, 0)))
		}
	}
	path := v1 + c.pathWith(args)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var body any
	if c.op.body != nil {
		body = f.body(c.op)
	}
	rec := b.doWith(c.op.method, path, body, http.Header{"If-Match": {`"1"`}, "Idempotency-Key": {ids.New().String()}})
	return isoAnswer{rec: rec, sent: f.sent}
}

// bodyHints are the bodies of the operations whose generated body is refused before the record
// in the path is looked up: the schema allows fields the handler refuses together, or a value it
// cannot tell from its type. Each body reaches the lookup and still names every record it can.
var bodyHints = map[string]func(f *filler) any{
	"POST /attributes/{id}/options": func(f *filler) any { return map[string]any{"label": f.str("label", nil)} },
	"PATCH /campaigns/{id}/checklist": func(f *filler) any {
		return map[string]any{"add": []any{map[string]any{"phase": "before_peak", "title": f.str("title", nil)}}}
	},
	"PATCH /customers/{id}": func(f *filler) any {
		return map[string]any{"name": f.str("name", nil), "customer_group_id": f.id("customer_group_id")}
	},
	"POST /kits/{id}/recipes": func(f *filler) any {
		return map[string]any{"items": []any{map[string]any{"kind": "goods", "sku_id": f.id("sku_id"), "unit_id": f.id("unit_id"), "qty": "1"}}}
	},
	"POST /parties/{id}/collection-notes": func(f *filler) any {
		return map[string]any{"kind": "note", "direction": "receivable", "body": f.str("body", nil)}
	},
	"PATCH /pos/carts/{id}": func(f *filler) any {
		return map[string]any{"customer_party_id": f.id("customer_party_id"), "lines": f.saleLines()}
	},
	"POST /pos/sync-exceptions/{id}:resolve": func(f *filler) any {
		return map[string]any{"customer_party_id": f.id("customer_party_id"), "lines": f.saleLines(),
			"payments": []any{map[string]any{"payment_method_id": f.id("payment_method_id"), "amount": "1"}}}
	},
	"PATCH /price-lists/{id}": func(f *filler) any { return map[string]any{"name": f.str("name", nil)} },
	"POST /skus/{id}/barcodes": func(f *filler) any {
		return map[string]any{"code": fmt.Sprintf("893%010d", f.n), "unit_id": f.id("unit_id")}
	},
	"POST /skus/{id}/units": func(f *filler) any { return map[string]any{"unit_id": f.id("unit_id"), "factor": "12"} },
	"PUT /stock-counts/{id}/lines": func(f *filler) any {
		return map[string]any{"lines": []any{map[string]any{"sku_id": f.id("sku_id"), "counted_qty": "1", "reason_id": f.id("reason_id")}}}
	},
	"PATCH /suppliers/{id}": func(f *filler) any { return map[string]any{"name": f.str("name", nil)} },
}

// saleLines is one line of an SKU in its unit, the shape every sale line accepts.
func (f *filler) saleLines() []any {
	return []any{map[string]any{"sku_id": f.id("sku_id"), "unit_id": f.id("unit_id"), "qty": "1"}}
}

// body is the request body of an operation: its hint, or one generated from its schema.
func (f *filler) body(op specOp) any {
	if hint, ok := bodyHints[op.String()]; ok {
		return hint(f)
	}
	return f.value("", op.body, 0)
}

// namesRecords reports whether the query or body of the call names a record.
func (c isoCall) namesRecords(t *testing.T, sp apiSpec) bool {
	if len(c.queryIDs) > 0 {
		return true
	}
	f := &filler{t: t, spec: sp, idOf: func(string) uuid.UUID { return ids.New() }, sent: map[uuid.UUID]bool{}}
	for _, p := range c.op.params {
		if p.in == "query" && f.hasUUID(p.schema) {
			return true
		}
	}
	if c.op.body != nil {
		f.body(c.op)
	}
	return len(f.sent) > 0
}

// isoSweep calls every operation as store A, or as the empty prober store C, with the ids of
// store B, and compares each answer with the answer to the same request with ids that exist
// nowhere.
type isoSweep struct {
	t    *testing.T
	s    controlStack
	spec apiSpec
	// a and c ask, b owns the ids.
	a, b, c *isoStore
	n       int
	// statuses counts the answers to another store's ids in a path, by status.
	statuses map[int]int
	// unmapped are body and query fields whose kind is not known; they get an SKU of B.
	unmapped map[string]bool
	// others are the answers to an id of B in a path other than 404 and 403, each the answer to
	// an id that exists nowhere too, by call label.
	others map[string]string
}

// clientFor is the client of a request: the store's signed-in browser, signed in again when an
// earlier request (a sign-out, a password or MFA change) ended its session; for a public
// operation a new client of the store's host, so the per-address limit of the credential
// endpoints answers neither request.
func (w *isoSweep) clientFor(st *isoStore, c isoCall) *browser {
	w.t.Helper()
	if c.op.public {
		return w.s.browser(w.t, st.owner.host).withContext()
	}
	if st.b.do(http.MethodGet, v1+"/me", nil).Code == http.StatusUnauthorized {
		st.b = w.s.signIn(w.t, st.owner)
	}
	return st.b
}

// foreignID is the id of store B of the kind a field names.
func (w *isoSweep) foreignID(field string) uuid.UUID {
	kind, ok := kindOfField(w.b.kinds, field)
	if !ok {
		w.unmapped[field] = true
		kind = "skus"
	}
	id, ok := w.b.kinds[kind]
	if !ok {
		w.t.Errorf("store B has no %s, named by %s", kind, field)
		return ids.New()
	}
	return id
}

// argsOf are the ids of the store for the path of the call: one record, or a parent and child.
func (w *isoSweep) argsOf(st *isoStore, c isoCall) []uuid.UUID {
	w.t.Helper()
	switch len(c.kinds) {
	case 0:
		return nil
	case 1:
		id, ok := st.kinds[c.kinds[0]]
		if !ok {
			w.t.Fatalf("%s: store %s has no %s", c.label, st.owner.slug, c.kinds[0])
		}
		return []uuid.UUID{id}
	default:
		pair, ok := st.pairs[c.kinds[1]]
		if !ok {
			w.t.Fatalf("%s: store %s has no pair of %s", c.label, st.owner.slug, c.kinds[1])
		}
		return pair[:]
	}
}

func randomIDs(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = ids.New()
	}
	return out
}

// compare sends the baseline request, then the foreign one, from the store, and checks that the
// foreign ids changed nothing: the same status and error, the same body for a read, and no id of
// B the request did not send. inPath marks requests with an id of B in their path, which must be
// refused.
func (w *isoSweep) compare(st *isoStore, c isoCall, variant string, baseArgs, foreignArgs []uuid.UUID, inPath bool) {
	t := w.t
	t.Helper()
	label := c.label + " (" + variant + ")"
	w.n++
	n := w.n
	if c.op.method != http.MethodGet {
		n = 2 * w.n
	}
	base := c.request(t, w.spec, w.clientFor(st, c), baseArgs, n, func(string) uuid.UUID { return ids.New() })
	if c.op.method != http.MethodGet {
		n++
	}
	foreign := c.request(t, w.spec, w.clientFor(st, c), foreignArgs, n, w.foreignID)
	w.leaks(label, foreign)
	if inPath {
		w.statuses[foreign.rec.Code]++
		if code := foreign.rec.Code; code != http.StatusNotFound && code != http.StatusForbidden {
			w.others[c.label] = label + " = " + foreign.String()
		}
	}
	switch {
	case foreign.rec.Code >= 500 || base.rec.Code >= 500:
		t.Errorf("%s: server error: foreign %s, baseline %s", label, foreign, base)
	case foreign.ok() && !base.ok():
		t.Errorf("%s accepts the ids of another store: %s, but %s for ids that exist nowhere", label, foreign, base)
	case !foreign.ok() && !base.ok() &&
		(foreign.rec.Code != base.rec.Code || errorCodeOf(foreign.rec) != errorCodeOf(base.rec)):
		t.Errorf("%s tells the ids of another store apart: %s, but %s for ids that exist nowhere", label, foreign, base)
	case inPath && foreign.ok():
		t.Errorf("%s answers %s for the record of another store", label, foreign)
	case c.op.method == http.MethodGet && foreign.ok() && base.ok() &&
		normalize(foreign.rec.Body.String(), foreign.sent) != normalize(base.rec.Body.String(), base.sent):
		t.Errorf("%s reads differently for the ids of another store:\n%s\nfor ids that exist nowhere:\n%s", label, foreign, base)
	}
}

// leaks fails when an answer names a record of store B the request did not name itself.
func (w *isoSweep) leaks(label string, a isoAnswer) {
	var text strings.Builder
	text.WriteString(a.rec.Body.String())
	for k, vs := range a.rec.Header() {
		text.WriteString("\n" + k + ": " + strings.Join(vs, ","))
	}
	for _, s := range uuidRE.FindAllString(text.String(), -1) {
		id := uuid.MustParse(s)
		if kind, ok := w.b.all[id]; ok && !a.sent[id] {
			w.t.Errorf("%s leaks %s %s of another store: %s", label, kind, id, a)
		}
	}
}

// endsSession are the paths whose operations may end the session or change the store, plan or
// account of the prober; they run after every other operation.
var endsSession = []string{"/auth", "/me", "/sessions", "/owner-transfer", "/account-closure", "/tenant", "/subscription"}

// readsOfOwnRecords are the reads with a path id that answer an owner without 2xx even for a
// record of its own, and why.
var readsOfOwnRecords = map[string]string{}

// notApplicable are the operations the sweep does not call, and why.
var notApplicable = map[string]string{}

// otherPathAnswers are the calls that answer an id of B in their path with neither 404 nor 403,
// and why that still proves the record of B is out of reach.
var otherPathAnswers = map[string]string{}

// unmappedFields are the body and query fields that name no kind of record of B, and why the SKU
// of B they get instead leaves no gap.
var unmappedFields = map[string]string{
	"id": "on GET /skus it is the SKU filter, which an SKU of B fits; in a body it names a line or " +
		"checklist item of the document or campaign in the path, looked up among that record's own " +
		"items, so an id of B is as unknown there as an id of nowhere",
	"plan_id": "plans belong to the platform and every store shares them: there is no plan of B",
}

// Every operation of the tenant API, called by another store with the ids of store B in its path,
// query and body, answers exactly what it answers for ids that exist nowhere: 404 or 403 for a
// record of B in the path, the same error for an id of B in the body, the same rows for an id of
// B in a filter. No answer names a record of B, and B is unchanged. The support view of B stays
// closed to an operator whose session was granted by store A.
func TestTenantIsolationAcrossEveryRoute(t *testing.T) {
	s := newControlStack(t)
	admA, adminA := s.adminSignIn(t)
	admB, adminB := s.adminSignIn(t)
	a := fillIsolationStore(t, s, admA, adminA, 1)
	b := fillIsolationStore(t, s, admB, adminB, 2)
	prober := s.signUp(t)
	c := &isoStore{owner: prober, b: s.signIn(t, prober)}
	spec := loadSpec(t, "openapi.yaml")
	w := &isoSweep{t: t, s: s, spec: spec, a: &a, b: &b, c: c, statuses: map[int]int{}, unmapped: map[string]bool{}, others: map[string]string{}}

	ops := spec.operations()
	for op := range bodyHints {
		if !slices.ContainsFunc(ops, func(o specOp) bool { return o.String() == op }) {
			t.Errorf("%s has a body hint but is not an operation", op)
		}
	}
	for op := range notApplicable {
		if !slices.ContainsFunc(ops, func(o specOp) bool { return o.String() == op }) {
			t.Errorf("%s is listed as not applicable but is not an operation", op)
		}
	}
	reports := send[dataOf[reportRef]](t, a.b, http.MethodGet, "/reports", nil).Data
	if len(reports) == 0 {
		t.Fatal("the store lists no reports")
	}
	var calls []isoCall
	called := map[string]bool{}
	for _, op := range ops {
		if notApplicable[op.String()] != "" {
			continue
		}
		called[op.String()] = true
		calls = append(calls, callsOf(t, op, reports)...)
	}

	// B reads each of its own records through every read that names one, so a kind mapped to the
	// wrong table shows up here rather than as a vacuous 404 below.
	for _, call := range calls {
		if call.op.method != http.MethodGet || len(call.kinds) == 0 {
			continue
		}
		rec := call.request(t, spec, b.b, w.argsOf(&b, call), 0, func(field string) uuid.UUID {
			kind, _ := kindOfField(b.kinds, field)
			return b.kinds[kind]
		}).rec
		if rec.Code >= 200 && rec.Code < 400 {
			continue
		}
		if why := readsOfOwnRecords[call.label]; why == "" {
			t.Errorf("store B reading its own record: %s = %d %s", call.label, rec.Code, rec.Body)
		}
	}
	before := tenantSnapshot(t, b.tenant)

	var last []isoCall
	for _, call := range calls {
		switch {
		case len(call.kinds) > 0:
			// A record of B in the path, and for a child, a record of B under a parent of A.
			w.compare(&a, call, "path", randomIDs(len(call.kinds)), w.argsOf(&b, call), true)
			if len(call.kinds) == 2 {
				own := w.argsOf(&a, call)
				w.compare(&a, call, "child", []uuid.UUID{own[0], ids.New()}, []uuid.UUID{own[0], w.argsOf(&b, call)[1]}, true)
			}
			// A record of A in the path, with the ids of B in the query and body.
			if call.namesRecords(t, spec) {
				own := w.argsOf(&a, call)
				w.compare(&a, call, "body", own, own, false)
			}
		case call.op.method == http.MethodGet:
			w.compare(&a, call, "query", nil, nil, false)
		case slices.ContainsFunc(endsSession, func(p string) bool { return strings.HasPrefix(call.op.path, p) }):
			last = append(last, call)
		default:
			w.compare(c, call, "body", nil, nil, false)
		}
	}
	// A change of plan can close modules to the prober: it goes last of all.
	rank := func(c isoCall) int {
		if strings.HasPrefix(c.op.path, "/subscription") {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(last, func(x, y isoCall) int { return rank(x) - rank(y) })
	for _, call := range last {
		w.compare(c, call, "body", nil, nil, false)
	}

	if after := tenantSnapshot(t, b.tenant); !maps.Equal(before, after) {
		for table := range before {
			if before[table] != after[table] {
				t.Errorf("the sweep changed the %s of store B", table)
			}
		}
	}
	if rec := b.b.do(http.MethodGet, v1+"/me", nil); rec.Code != http.StatusOK {
		t.Errorf("store B after the sweep: /me = %d %s", rec.Code, rec.Body)
	}

	// The operator of A's ticket holds a session of A only: the support view of B stays closed to
	// it, and nothing is recorded as an access of B.
	accesses := func() int {
		return countRows(t, b.tenant, `SELECT count(*) FROM audit_events WHERE action = 'support_session.access'`)
	}
	seen := accesses()
	if rec := admA.do(http.MethodGet, adminV1+"/tenants/"+a.tenant.String()+"/support-view/catalog/products", nil); rec.Code != http.StatusOK {
		t.Fatalf("the operator's own session of A = %d %s", rec.Code, rec.Body)
	}
	view := adminV1 + "/tenants/" + b.tenant.String() + "/support-view/catalog/products"
	if rec := admA.do(http.MethodGet, view, nil); rec.Code != http.StatusForbidden || errorCodeOf(rec) != support.CodeSessionInactive {
		t.Errorf("support view of B with a session of A = %d %s, want 403 %s", rec.Code, rec.Body, support.CodeSessionInactive)
	}
	// A write is refused before any session is looked up: every support session is read-only.
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		if rec := admA.do(method, view, nil); rec.Code != http.StatusForbidden {
			t.Errorf("support view of B with a session of A: %s = %d %s, want 403", method, rec.Code, rec.Body)
		}
	}
	if got := accesses(); got != seen {
		t.Errorf("support accesses recorded in B = %d, want %d", got, seen)
	}

	for _, op := range ops {
		if !called[op.String()] && notApplicable[op.String()] == "" {
			t.Errorf("%s was not called", op)
		}
	}
	inPath := 0
	for _, n := range w.statuses {
		inPath += n
	}
	t.Logf("%d operations, %d calls, %d with an id of B in the path; their answers by status: %v",
		len(ops), len(calls), inPath, w.statuses)
	// An answer other than 404 or 403, as one that matches the answer for an id of nowhere
	// because both fail validation first, proves nothing about the lookup of the record of B.
	for _, label := range slices.Sorted(maps.Keys(w.others)) {
		if otherPathAnswers[label] == "" {
			t.Errorf("an id of B in the path answers neither 404 nor 403: %s", w.others[label])
		}
	}
	// A field of no known kind is probed with an SKU of B, which proves nothing when it names
	// another kind of record.
	for _, field := range slices.Sorted(maps.Keys(w.unmapped)) {
		if unmappedFields[field] == "" {
			t.Errorf("field %s names no kind of record of B: map it in fieldKinds or explain it in unmappedFields", field)
		}
	}
}

// fillPath puts a value in every path parameter: an id that exists nowhere, or a valid name.
func fillPath(path string) string {
	return pathParamRE.ReplaceAllStringFunc(path, func(p string) string {
		switch p {
		case "{module}":
			return "catalog"
		case "{rest}":
			return "products"
		case "{docType}":
			return "quote"
		case "{kind}":
			return "buy"
		case "{reportKey}":
			return "revenue"
		case "{provider}":
			return testProvider
		case "{stepKey}":
			return "store_profile"
		case "{n}", "{version}":
			return "1"
		case "{key}":
			return "thu"
		}
		return ids.New().String()
	})
}

// Every operation of the admin API refuses a store owner's session cookie on the admin host with
// the 401 it gives a client with no cookie, and does not exist on a store host or the root host.
func TestAdminAPIRefusesTenantSessionsAndHosts(t *testing.T) {
	s := newControlStack(t)
	owner := s.signIn(t, s.signUp(t))
	root := s.browser(t, "tally.test")
	root.hostContext()
	ops := loadSpec(t, "openapi-admin.yaml").operations()
	if len(ops) == 0 {
		t.Fatal("the admin API has no operations")
	}
	for _, op := range ops {
		path := adminV1 + fillPath(op.path)
		// The store's session cookie as it is, and its value under the admin cookie's name.
		for _, name := range []string{"tally_session", "tally_admin_session"} {
			stolen, anon := s.adminBrowser(t), s.adminBrowser(t)
			stolen.cookies[name] = &http.Cookie{Name: name, Value: owner.cookies["tally_session"].Value}
			got, want := stolen.do(op.method, path, nil), anon.do(op.method, path, nil)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Errorf("%s on the admin host with a store session as %s = %d %s, without = %d %s", op, name,
					got.Code, got.Body, want.Code, want.Body)
			}
			if !op.public && got.Code != http.StatusUnauthorized {
				t.Errorf("%s on the admin host with a store session as %s = %d %s, want 401", op, name, got.Code, got.Body)
			}
		}
		for name, b := range map[string]*browser{"store": owner, "root": root} {
			if rec := b.do(op.method, path, nil); rec.Code != http.StatusNotFound {
				t.Errorf("%s on the %s host = %d %s, want 404", op, name, rec.Code, rec.Body)
			}
		}
	}
}

// hostAgnostic are the tenant API operations that answer on every known host, and why.
var hostAgnostic = map[string]string{
	"GET /host-context": "the first request of every app on every host: it tells the admin app it is on the admin host " +
		"(kind admin) and hands out the CSRF token of that host",
}

// A store host answers only for its own store: another store's session cookie is no session
// there, an unknown store answers exactly like a real one, the tenant API does not exist on the
// admin host, and a host that is not a store, the root or the admin host is refused with 421.
func TestHostBoundaries(t *testing.T) {
	s := newControlStack(t)
	a, b := s.signUp(t), s.signUp(t)
	ab := s.signIn(t, a)

	stolen, anon := s.browser(t, b.host), s.browser(t, b.host)
	anon.hostContext()
	stolen.hostContext()
	stolen.cookies["tally_session"] = ab.cookies["tally_session"]
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/me", nil},
		{http.MethodGet, "/products", nil},
		{http.MethodGet, "/subscription", nil},
		{http.MethodGet, "/sessions", nil},
		{http.MethodPost, "/units", map[string]any{"name": "Thùng"}},
		{http.MethodPost, "/auth/reauth", map[string]any{"password": password}},
	} {
		got, want := stolen.do(r.method, v1+r.path, r.body), anon.do(r.method, v1+r.path, r.body)
		if got.Code != http.StatusUnauthorized || got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("%s %s on B with the session of A = %d %s, without a session = %d %s", r.method, r.path,
				got.Code, got.Body, want.Code, want.Body)
		}
	}
	if rec := ab.do(http.MethodGet, v1+"/me", nil); rec.Code != http.StatusOK {
		t.Errorf("A's own /me = %d %s", rec.Code, rec.Body)
	}

	// An unknown store answers host-context, /me and sign-in exactly like a real one.
	ghost := owner{slug: "khong-co-" + unique()}
	ghost.host = ghost.slug + ".tally.test"
	existing, fake := s.browser(t, b.host), s.browser(t, ghost.host)
	contextOf := func(br *browser) string {
		t.Helper()
		rec := br.do(http.MethodGet, v1+"/host-context", nil)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("host-context of %s = %d %s", br.host, rec.Code, rec.Body)
		}
		br.csrf, _ = body["csrf_token"].(string)
		delete(body, "csrf_token")
		out, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return strconv.Itoa(rec.Code) + " " + string(out)
	}
	if got, want := contextOf(fake), contextOf(existing); got != want {
		t.Errorf("host-context of an unknown store = %s, of a real one = %s", got, want)
	}
	same := func(what string, got, want *httptest.ResponseRecorder) {
		t.Helper()
		if got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("%s: unknown store = %d %s, real store = %d %s", what, got.Code, got.Body, want.Code, want.Body)
		}
	}
	same("/me", fake.do(http.MethodGet, v1+"/me", nil), existing.do(http.MethodGet, v1+"/me", nil))
	same("sign-in of an unknown account", fake.do(http.MethodPost, v1+"/auth/login", map[string]string{"login": "ai-do@example.test", "password": password}),
		existing.do(http.MethodPost, v1+"/auth/login", map[string]string{"login": "ai-do@example.test", "password": password}))
	same("sign-in of B's owner", s.browser(t, ghost.host).withContext().do(http.MethodPost, v1+"/auth/login", map[string]string{"login": b.email, "password": password}),
		s.browser(t, b.host).withContext().do(http.MethodPost, v1+"/auth/login", map[string]string{"login": b.email, "password": "sai-mat-khau-1"}))

	// The tenant API does not exist on the admin host.
	adm := s.adminBrowser(t)
	for _, op := range loadSpec(t, "openapi.yaml").operations() {
		rec := adm.do(op.method, v1+fillPath(op.path), nil)
		if why := hostAgnostic[op.String()]; why != "" {
			continue
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s on the admin host = %d %s, want 404", op, rec.Code, rec.Body)
		}
	}

	// A host under a store, or outside the domain, is refused before anything reads it.
	for _, host := range []string{"x." + b.host, "a.b." + b.host, "evil.example", b.host + ".evil.example", "admin." + b.host} {
		for _, path := range []string{v1 + "/host-context", v1 + "/me", v1 + "/auth/login", adminV1 + "/me"} {
			br := s.browser(t, host)
			br.cookies["tally_session"] = ab.cookies["tally_session"]
			if rec := br.do(http.MethodGet, path, nil); rec.Code != http.StatusMisdirectedRequest {
				t.Errorf("GET %s on %s = %d %s, want 421", path, host, rec.Code, rec.Body)
			}
		}
	}
}

// withContext fetches the CSRF token of a new client and returns it.
func (b *browser) withContext() *browser {
	b.hostContext()
	return b
}

// Signing in on store B with the e-mail and password of store A's owner, a real account of
// another store, cannot be told apart from a wrong password of B's owner: the same generic answer,
// one password hash of the same cost, and the same time within 10%.
func TestLoginOfAnotherStoresOwnerTakesAsLongAsAWrongPassword(t *testing.T) {
	var hasher *countingHasher
	s := newStack(t, func(d *identity.Deps) {
		h, err := authn.NewHasher(authn.DefaultParams)
		if err != nil {
			t.Fatal(err)
		}
		hasher = &countingHasher{PasswordHasher: h}
		d.Hasher = hasher
	})
	a, b := s.signUp(t), s.signUp(t)
	login := func(email, pw string) (time.Duration, *httptest.ResponseRecorder, int32) {
		t.Helper()
		// A new client per attempt: the per-address throttle must not decide the answer.
		br := s.browser(t, b.host).withContext()
		hasher.verify.Store(0)
		hasher.dummy.Store(0)
		start := time.Now()
		rec := br.do(http.MethodPost, v1+"/auth/login", map[string]string{"login": email, "password": pw})
		return time.Since(start), rec, hasher.verify.Load() + hasher.dummy.Load()
	}
	login(a.email, password)
	login(b.email, "sai-mat-khau-0")
	const samples = 30
	var other, wrong []time.Duration
	for i := range samples {
		// Alternate which goes first, so neither always pays for the other's garbage collection.
		var (
			d1, d2 time.Duration
			r1, r2 *httptest.ResponseRecorder
			w1, w2 int32
		)
		if i%2 == 0 {
			d1, r1, w1 = login(a.email, password)
			d2, r2, w2 = login(b.email, "sai-mat-khau-"+strconv.Itoa(i+1))
		} else {
			d2, r2, w2 = login(b.email, "sai-mat-khau-"+strconv.Itoa(i+1))
			d1, r1, w1 = login(a.email, password)
		}
		if errorCodeOf(r1) != "invalid_credentials" || r1.Code != r2.Code || r1.Body.String() != r2.Body.String() {
			t.Fatalf("sign-in with another store's owner = %d %s, with a wrong password = %d %s", r1.Code, r1.Body, r2.Code, r2.Body)
		}
		if w1 != 1 || w2 != 1 {
			t.Fatalf("password checks: another store's owner %d, wrong password %d; want one each", w1, w2)
		}
		other, wrong = append(other, d1), append(wrong, d2)
	}
	median := func(ds []time.Duration) time.Duration {
		ds = slices.Clone(ds)
		slices.Sort(ds)
		return ds[len(ds)/2]
	}
	mo, mw := median(other), median(wrong)
	ratio := float64(mo) / float64(mw)
	t.Logf("median sign-in: another store's owner %s, wrong password %s (ratio %.3f)", mo, mw, ratio)
	if ratio < 0.9 || ratio > 1.1 {
		t.Errorf("median sign-in of another store's owner %s differs from a wrong password %s by more than 10%%", mo, mw)
	}
}
