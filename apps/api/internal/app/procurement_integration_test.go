//go:build integration

package app_test

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

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
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// statusEntitlements lets a tenant write unless its status is read_only, so a test can turn a
// store read-only by changing its row. It reads the row as superuser: the subscription gate of
// the product does not exist yet and the application role's view of tenants is not its concern.
type statusEntitlements struct{ db *sqlx.DB }

func (e statusEntitlements) CanWrite(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var status string
	if err := e.db.QueryRowContext(ctx, `SELECT status FROM tally.tenants WHERE id = $1`, tenantID).Scan(&status); err != nil {
		return false, err
	}
	return status != "read_only", nil
}

// newProcurementStack is the HTTP stack with the procurement routes and every module they lean
// on, plus the domain services so a test can post a sale the way the sales domain will. Postings
// and writes share one subscription gate, as in production.
func newProcurementStack(t *testing.T) (stack, *clock.Fixed, app.Domains) {
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
	// Presigning an upload needs no connection; no test here reaches the object store.
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: &url.URL{Scheme: "http", Host: "127.0.0.1:9000"}, Region: "us-east-1", Bucket: "tally-dev",
		AccessKey: "tally-dev", SecretKey: "tally-dev-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	entitlements := statusEntitlements{db: testkit.SuperDB(t)}
	domains := app.NewDomains(deps.DB, clk, deps.Audit, app.WithEntitlements(entitlements))
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
			Procurement: procurement.NewHandler(domains.Procurement),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk, domains
}

// purchaseStore is one store's suppliers, reasons and lookups for buying goods.
type purchaseStore struct {
	tenant                                 uuid.UUID
	units, colors                          map[string]uuid.UUID
	supplier, shipper, cashMethod          uuid.UUID
	returnReason, closeReason, priceReason uuid.UUID
}

func buildPurchaseStore(t *testing.T, b *browser) purchaseStore {
	t.Helper()
	st := purchaseStore{tenant: tenantOf(t, b), units: unitsOf(t, b), colors: map[string]uuid.UUID{}}
	for _, c := range send[dataOf[struct {
		ID   uuid.UUID `json:"id"`
		Code string    `json:"code"`
	}]](t, b, http.MethodGet, "/colors", nil).Data {
		st.colors[c.Code] = c.ID
	}
	for _, m := range send[dataOf[struct {
		ID   uuid.UUID `json:"id"`
		Code string    `json:"code"`
	}]](t, b, http.MethodGet, "/payment-methods", nil).Data {
		if m.Code == cash.MethodCash {
			st.cashMethod = m.ID
		}
	}
	st.supplier = st.newSupplier(t, b, "Công ty Nước Giải Khát")
	st.shipper = st.newSupplier(t, b, "Xe tải Anh Ba")
	st.returnReason = send[dataOf[withID]](t, b, http.MethodGet, "/reasons?kind=purchase_return", nil).Data[0].ID
	st.closeReason = send[withID](t, b, http.MethodPost, "/reasons", map[string]any{"kind": "close_short", "name": "Nhà cung cấp hết hàng"}).ID
	st.priceReason = send[withID](t, b, http.MethodPost, "/reasons", map[string]any{"kind": "price_adjustment", "name": "Giá chốt cuối tháng"}).ID
	if st.cashMethod == uuid.Nil || st.colors["tim"] == uuid.Nil || st.units["thùng"] == uuid.Nil {
		t.Fatalf("seeded lookups = %+v", st)
	}
	return st
}

// newSupplier creates a supplier organisation, so a subtest can read that party's debt alone.
func (st purchaseStore) newSupplier(t *testing.T, b *browser, name string) uuid.UUID {
	t.Helper()
	return send[withID](t, b, http.MethodPost, "/suppliers", map[string]any{"kind": "organization", "name": name}).ID
}

// newSku creates a goods product with one SKU in "cái".
func (st purchaseStore) newSku(t *testing.T, b *browser, code string) uuid.UUID {
	t.Helper()
	return send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"type": "goods", "name": "Hàng " + code, "sku": map[string]any{"code": code, "base_unit_id": st.units["cái"]},
	}).Skus[0].ID
}

// receiveDirect posts a receipt without an order of qty base units of sku at unitPrice.
func (st purchaseStore) receiveDirect(t *testing.T, b *browser, supplier, sku uuid.UUID, qty, unitPrice string) receiptOut {
	t.Helper()
	return postReceipt(t, b, map[string]any{"supplier_party_id": supplier,
		"lines": []map[string]any{{"sku_id": sku, "qty": qty, "unit_price": unitPrice}}})
}

type purchaseOrderOut struct {
	ID              uuid.UUID `json:"id"`
	Status          string    `json:"status"`
	Version         int       `json:"version"`
	OrderedBaseQty  *string   `json:"ordered_base_qty"`
	ReceivedBaseQty *string   `json:"received_base_qty"`
	PendingBaseQty  *string   `json:"pending_base_qty"`
	DaysLate        *int      `json:"days_late"`
	Lines           []struct {
		ID              uuid.UUID `json:"id"`
		BaseQty         string    `json:"base_qty"`
		ReceivedBaseQty string    `json:"received_base_qty"`
		PendingBaseQty  string    `json:"pending_base_qty"`
	} `json:"lines"`
}

type payableOut struct {
	ID            uuid.UUID `json:"id"`
	Amount        string    `json:"amount"`
	SettledAmount string    `json:"settled_amount"`
	OpenAmount    string    `json:"open_amount"`
	ReversedOn    *string   `json:"reversed_on"`
	Schedule      []struct {
		DueDate string `json:"due_date"`
		Amount  string `json:"amount"`
	} `json:"schedule"`
}

type receiptOut struct {
	ID          uuid.UUID  `json:"id"`
	Status      string     `json:"status"`
	TotalAmount string     `json:"total_amount"`
	ReversalOf  *uuid.UUID `json:"reversal_of"`
	Lines       []struct {
		ID                uuid.UUID  `json:"id"`
		ColorID           *uuid.UUID `json:"color_id"`
		LotID             *uuid.UUID `json:"lot_id"`
		Condition         string     `json:"condition"`
		LandedCostAmount  string     `json:"landed_cost_amount"`
		UnitCost          string     `json:"unit_cost"`
		ReturnableBaseQty string     `json:"returnable_base_qty"`
	} `json:"lines"`
	Payable  *payableOut `json:"payable"`
	Payments []struct {
		Amount string `json:"amount"`
	} `json:"payments"`
	LandedCosts []withID `json:"landed_costs"`
	Attachments []withID `json:"attachments"`
}

type documentOut struct {
	ID             uuid.UUID `json:"id"`
	MatchStatus    string    `json:"match_status"`
	VarianceAmount string    `json:"variance_amount"`
	Version        int       `json:"version"`
	Attachments    []withID  `json:"attachments"`
}

type returnOut struct {
	ID           uuid.UUID  `json:"id"`
	Settlement   string     `json:"settlement"`
	TotalAmount  string     `json:"total_amount"`
	CreditNoteID *uuid.UUID `json:"credit_note_id"`
	Lines        []struct {
		LotID    uuid.UUID  `json:"lot_id"`
		ColorID  *uuid.UUID `json:"color_id"`
		UnitCost string     `json:"unit_cost"`
	} `json:"lines"`
}

type adjustmentOut struct {
	PayableDelta string     `json:"payable_delta"`
	PayableID    *uuid.UUID `json:"payable_id"`
	CreditNoteID *uuid.UUID `json:"credit_note_id"`
	Lines        []struct {
		OnHandAmount string `json:"on_hand_amount"`
		SoldAmount   string `json:"sold_amount"`
	} `json:"lines"`
}

// postReceipt posts a PN that must be created.
func postReceipt(t *testing.T, b *browser, body map[string]any) receiptOut {
	t.Helper()
	rec := post(b, "/receipts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("receipt = %d %s", rec.Code, rec.Body)
	}
	return testkit.DecodeJSON[receiptOut](t, rec)
}

func getReceiptOut(t *testing.T, b *browser, id uuid.UUID) receiptOut {
	t.Helper()
	return send[receiptOut](t, b, http.MethodGet, "/receipts/"+id.String(), nil)
}

func getOrderOut(t *testing.T, b *browser, id uuid.UUID) purchaseOrderOut {
	t.Helper()
	return send[purchaseOrderOut](t, b, http.MethodGet, "/purchase-orders/"+id.String(), nil)
}

// confirmedOrder creates a PO and confirms it.
func confirmedOrder(t *testing.T, b *browser, body map[string]any) purchaseOrderOut {
	t.Helper()
	rec := b.do(http.MethodPost, v1+"/purchase-orders", body)
	expect(t, rec, http.StatusCreated, procurement.StatusDraft)
	id := testkit.DecodeJSON[withID](t, rec).ID
	rec = b.doWith(http.MethodPost, v1+"/purchase-orders/"+id.String()+":confirm", nil, http.Header{"If-Match": {rec.Header().Get("ETag")}})
	expect(t, rec, http.StatusOK, procurement.StatusConfirmed)
	return testkit.DecodeJSON[purchaseOrderOut](t, rec)
}

// receiveOrder posts a receipt of qty (in the order unit) of the first line of an order.
func receiveOrder(t *testing.T, b *browser, order purchaseOrderOut, qty string) receiptOut {
	t.Helper()
	return postReceipt(t, b, map[string]any{"purchase_order_id": order.ID,
		"lines": []map[string]any{{"purchase_order_line_id": order.Lines[0].ID, "qty": qty}}})
}

func ifMatchOf(version int) http.Header {
	return http.Header{"If-Match": {`"` + strconv.Itoa(version) + `"`}}
}

// postReplayed sends body to path twice under one Idempotency-Key, checks that the second answer
// replays the first and returns the id both answered.
func postReplayed(t *testing.T, b *browser, path string, body map[string]any) uuid.UUID {
	t.Helper()
	key := http.Header{"Idempotency-Key": {ids.New().String()}}
	rec := b.doWith(http.MethodPost, v1+path, body, key)
	expect(t, rec, http.StatusCreated, "")
	id := testkit.DecodeJSON[withID](t, rec).ID
	again := b.doWith(http.MethodPost, v1+path, body, key)
	expect(t, again, http.StatusCreated, "")
	if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[withID](t, again).ID != id {
		t.Fatalf("replay of %s = %s %s", path, again.Header(), again.Body)
	}
	return id
}

// expectRejected checks that a request was refused as the client's fault.
func expectRejected(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code < 400 || rec.Code > 499 {
		t.Errorf("%s = %d %s, want a 4xx", what, rec.Code, rec.Body)
	}
}

// confirmPassword re-authenticates the session, as a sensitive action asks.
func confirmPassword(t *testing.T, b *browser) {
	t.Helper()
	expect(t, b.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
}

// stockRow is the sku_stock projection of a SKU.
type stockRow struct {
	OnHand     money.Qty   `db:"on_hand"`
	Quarantine money.Qty   `db:"quarantine_qty"`
	AvgCost    money.Money `db:"avg_cost"`
	StockValue money.Money `db:"stock_value"`
}

func stockOf(t *testing.T, tenant, sku uuid.UUID) stockRow {
	t.Helper()
	var r stockRow
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(),
			`SELECT on_hand, quarantine_qty, avg_cost, stock_value FROM sku_stock WHERE sku_id = $1`, sku).StructScan(&r)
	})
	return r
}

func countRows(t *testing.T, tenant uuid.UUID, query string, args ...any) int {
	t.Helper()
	var n int
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), query, args...).Scan(&n)
	})
	return n
}

// partyOpen is what a store owes a supplier now, as the payables list shows it.
func partyOpen(t *testing.T, b *browser, party uuid.UUID) string {
	t.Helper()
	for _, p := range send[dataOf[struct {
		PartyID uuid.UUID `json:"party_id"`
		Open    string    `json:"open"`
	}]](t, b, http.MethodGet, "/debts/parties?direction=payable", nil).Data {
		if p.PartyID == party {
			return p.Open
		}
	}
	return "0"
}

// sell takes base units of a SKU out the way a sale does, through the posting coordinator.
func sell(t *testing.T, d app.Domains, tenant, sku uuid.UUID, qty string) {
	t.Helper()
	ctx := context.Background()
	v := ids.New().String()
	h := sha256.Sum256([]byte(v))
	owner := audit.Actor{TenantID: tenant, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb}
	doc := sourceDocument{date: clock.Date(2026, time.March, 15), apply: func(ctx context.Context, tx posting.Tx) error {
		_, err := d.Inventory.Ledger().Issue(ctx, tx, inventory.PostingOf(tx, "test_sale", ids.New()),
			[]inventory.IssueLine{{SkuID: sku, Qty: money.MustQty(qty)}})
		return err
	}}
	if _, err := d.Posting.Post(ctx, owner, idempotency.Key{Value: v, RequestHash: h[:]}, doc); err != nil {
		t.Fatalf("sell %s of %s: %v", qty, sku, err)
	}
}

// Every subtest builds the documents it works on, so any of them runs alone (go test -run).
func TestProcurementOverHTTP(t *testing.T) {
	s, clk, domains := newProcurementStack(t)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	storeA, storeB := buildPurchaseStore(t, a), buildPurchaseStore(t, b)
	tenants := []uuid.UUID{storeA.tenant, storeB.tenant}

	t.Run("a PO of 100 cartons received as 60 then 40 completes", func(t *testing.T) {
		can := storeA.newSku(t, a, "NGK-LON")
		send[map[string]any](t, a, http.MethodPost, "/skus/"+can.String()+"/units", map[string]any{"unit_id": storeA.units["thùng"], "factor": "24"})
		order := confirmedOrder(t, a, map[string]any{"supplier_party_id": storeA.supplier, "expected_date": "2026-03-20",
			"lines": []map[string]any{{"sku_id": can, "unit_id": storeA.units["thùng"], "qty": "100", "unit_price": "240000"}}})
		line := order.Lines[0].ID
		receive := func(qty string) map[string]any {
			return map[string]any{"purchase_order_id": order.ID, "lines": []map[string]any{{"purchase_order_line_id": line, "qty": qty}}}
		}
		first := postReceipt(t, a, receive("60"))
		if first.TotalAmount != "14400000" || first.Payable == nil || first.Payable.OpenAmount != "14400000" {
			t.Fatalf("first receipt = %+v", first)
		}
		got := getOrderOut(t, a, order.ID)
		if got.Status != procurement.StatusPartiallyReceived || got.Lines[0].ReceivedBaseQty != "1440" || got.Lines[0].PendingBaseQty != "960" {
			t.Fatalf("after 60 cartons = %+v", got)
		}
		expect(t, post(a, "/receipts", receive("41")), http.StatusConflict, procurement.CodeReceiptExceedsOrder)

		// The second batch, then the same request again under its key.
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec := a.doWith(http.MethodPost, v1+"/receipts", receive("40"), key)
		expect(t, rec, http.StatusCreated, "")
		second := testkit.DecodeJSON[receiptOut](t, rec)
		moves := countRows(t, storeA.tenant, `SELECT count(*) FROM stock_moves`)
		again := a.doWith(http.MethodPost, v1+"/receipts", receive("40"), key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[withID](t, again).ID != second.ID {
			t.Fatalf("replay = %s %s", again.Header(), again.Body)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM stock_moves`); n != moves {
			t.Errorf("stock moves after the replay = %d, want %d", n, moves)
		}

		var row *purchaseOrderOut
		list := send[dataOf[purchaseOrderOut]](t, a, http.MethodGet, "/purchase-orders", nil).Data
		for i := range list {
			if list[i].ID == order.ID {
				row = &list[i]
			}
		}
		if row == nil || row.Status != procurement.StatusCompleted || *row.OrderedBaseQty != "2400" || *row.ReceivedBaseQty != "2400" ||
			*row.PendingBaseQty != "0" {
			t.Fatalf("completed order row = %+v", row)
		}
		if st := stockOf(t, storeA.tenant, can); st.OnHand.String() != "2400" || st.AvgCost.String() != "10000" {
			t.Errorf("stock = %+v, want 2400 at 10000", st)
		}
		expect(t, post(a, "/receipts", receive("1")), http.StatusConflict, procurement.CodeOrderNotReceivable)
	})

	t.Run("a supplier document over two receipts leaves their payables alone", func(t *testing.T) {
		sku := storeA.newSku(t, a, "HD-HAI-PN")
		first := storeA.receiveDirect(t, a, storeA.supplier, sku, "6", "10000")
		second := storeA.receiveDirect(t, a, storeA.supplier, sku, "4", "10000")
		obligations := countRows(t, storeA.tenant, `SELECT count(*) FROM obligations`)
		rec := a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0001", "document_date": "2026-03-15", "goods_amount": "100000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{first.ID}})
		expect(t, rec, http.StatusCreated, procurement.MatchVariance)
		doc := testkit.DecodeJSON[documentOut](t, rec)
		if doc.VarianceAmount != "40000" {
			t.Fatalf("one receipt of two = %+v", doc)
		}
		path := v1 + "/supplier-documents/" + doc.ID.String() + "/receipts"
		body := map[string]any{"receipt_ids": []uuid.UUID{second.ID}}
		expect(t, a.do(http.MethodPost, path, body), http.StatusPreconditionRequired, "")
		rec = a.doWith(http.MethodPost, path, body, http.Header{"If-Match": {rec.Header().Get("ETag")}})
		expect(t, rec, http.StatusOK, procurement.MatchMatched)
		if got := testkit.DecodeJSON[documentOut](t, rec); got.VarianceAmount != "0" {
			t.Errorf("matched document = %+v", got)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM obligations`); n != obligations {
			t.Errorf("obligations = %d, want %d: a document adds no payable", n, obligations)
		}
		for r, want := range map[uuid.UUID]string{first.ID: "60000", second.ID: "40000"} {
			if p := getReceiptOut(t, a, r).Payable; p.Amount != want || p.OpenAmount != want {
				t.Errorf("payable of %s = %+v, want %s", r, p, want)
			}
		}
		expect(t, a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0001", "document_date": "2026-03-15", "goods_amount": "1", "tax_amount": "0"}),
			http.StatusConflict, procurement.CodeDocumentExists)
	})

	t.Run("a receipt is matched to one supplier document only", func(t *testing.T) {
		r := storeA.receiveDirect(t, a, storeA.supplier, storeA.newSku(t, a, "HD-MOT-PN"), "2", "10000")
		expect(t, a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0101", "document_date": "2026-03-15", "goods_amount": "20000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{r.ID}}), http.StatusCreated, procurement.MatchMatched)
		documents := countRows(t, storeA.tenant, `SELECT count(*) FROM supplier_documents`)

		// Neither on creation nor by matching it later can a second document take the receipt.
		expect(t, a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0102", "document_date": "2026-03-15", "goods_amount": "20000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{r.ID}}), http.StatusConflict, procurement.CodeReceiptMatched)
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM supplier_documents`); n != documents {
			t.Errorf("supplier documents = %d, want %d: the refused one is not kept", n, documents)
		}
		rec := a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0103", "document_date": "2026-03-15", "goods_amount": "20000", "tax_amount": "0"})
		expect(t, rec, http.StatusCreated, "")
		other := testkit.DecodeJSON[documentOut](t, rec)
		expect(t, a.doWith(http.MethodPost, v1+"/supplier-documents/"+other.ID.String()+"/receipts",
			map[string]any{"receipt_ids": []uuid.UUID{r.ID}}, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
			http.StatusConflict, procurement.CodeReceiptMatched)
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM supplier_document_receipts WHERE receipt_id = $1`, r.ID); n != 1 {
			t.Errorf("documents of the receipt = %d, want 1", n)
		}
	})

	t.Run("a landed cost of 100000 over three equal lines splits 33334/33333/33333", func(t *testing.T) {
		shipper := storeA.newSupplier(t, a, "Xe ba gác Chú Tư")
		costSkus := []uuid.UUID{storeA.newSku(t, a, "LC-1"), storeA.newSku(t, a, "LC-2"), storeA.newSku(t, a, "LC-3")}
		var lines []map[string]any
		for _, sku := range costSkus {
			lines = append(lines, map[string]any{"sku_id": sku, "qty": "10", "unit_price": "50000"})
		}
		costed := postReceipt(t, a, map[string]any{"supplier_party_id": storeA.supplier, "lines": lines})
		rec := post(a, "/receipts/"+costed.ID.String()+"/landed-costs", map[string]any{"cost_type": procurement.CostShipping,
			"amount": "100000", "payee_party_id": shipper, "due_date": "2026-03-31"})
		expect(t, rec, http.StatusCreated, "")
		got := getReceiptOut(t, a, costed.ID)
		var shares []string
		for _, l := range got.Lines {
			shares = append(shares, l.LandedCostAmount)
		}
		if !slices.Equal(shares, []string{"33334", "33333", "33333"}) {
			t.Fatalf("shares = %v", shares)
		}
		if st := stockOf(t, storeA.tenant, costSkus[0]); st.AvgCost.String() != "53333.4" {
			t.Errorf("average cost = %s, want (500000 + 33334) / 10", st.AvgCost)
		}
		if st := stockOf(t, storeA.tenant, costSkus[1]); st.AvgCost.String() != "53333.3" {
			t.Errorf("average cost = %s, want (500000 + 33333) / 10", st.AvgCost)
		}
		if open := partyOpen(t, a, shipper); open != "100000" {
			t.Errorf("owed to the shipper = %s, want 100000", open)
		}
	})

	t.Run("a landed cost after a partial return lands on the kept units and leaves with them", func(t *testing.T) {
		sku := storeA.newSku(t, a, "LC-TRA")
		r := storeA.receiveDirect(t, a, storeA.supplier, sku, "10", "10000")
		ret := func(qty string) returnOut {
			rec := post(a, "/purchase-returns", map[string]any{"receipt_id": r.ID, "reason_id": storeA.returnReason,
				"settlement": procurement.SettlementCreditPayable, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "qty": qty}}})
			expect(t, rec, http.StatusCreated, "")
			return testkit.DecodeJSON[returnOut](t, rec)
		}
		ret("4")
		expect(t, post(a, "/receipts/"+r.ID.String()+"/landed-costs", map[string]any{"cost_type": procurement.CostShipping,
			"amount": "6000", "payee_party_id": storeA.shipper, "due_date": "2026-03-31"}), http.StatusCreated, "")
		if l := getReceiptOut(t, a, r.ID).Lines[0]; l.UnitCost != "11000" {
			t.Fatalf("unit cost = %s, want 10000 + 6000 over the 6 kept", l.UnitCost)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || st.AvgCost.String() != "11000" {
			t.Fatalf("stock = %+v, want 6 at 11000", st)
		}
		// The rest leaves at the cost it carries in stock: nothing stays behind and nothing is
		// pushed onto goods sold.
		if x := ret("6"); x.Lines[0].UnitCost != "11000" || x.TotalAmount != "60000" {
			t.Errorf("second return = %+v, want 6 at 11000 credited at the 10000 paid", x)
		}
		if st := stockOf(t, storeA.tenant, sku); !st.OnHand.IsZero() || !st.StockValue.IsZero() {
			t.Errorf("stock after returning everything = %+v, want nothing worth nothing", st)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM cogs_adjustments WHERE sku_id = $1`, sku); n != 0 {
			t.Errorf("cogs adjustments = %d, want none", n)
		}
	})

	t.Run("a purple return leaves from the purple lot only", func(t *testing.T) {
		shirt := storeA.newSku(t, a, "AO-MAU")
		purple, red := storeA.colors["tim"], storeA.colors["do"]
		r := postReceipt(t, a, map[string]any{"supplier_party_id": storeA.supplier, "lines": []map[string]any{
			{"sku_id": shirt, "color_id": purple, "qty": "10", "unit_price": "50000"},
			{"sku_id": shirt, "color_id": red, "qty": "10", "unit_price": "50000"},
		}})
		purpleLine, redLine := r.Lines[0], r.Lines[1]
		if *purpleLine.ColorID != purple || *purpleLine.LotID == *redLine.LotID {
			t.Fatalf("lines = %+v", r.Lines)
		}
		ret := func(qty string) map[string]any {
			return map[string]any{"receipt_id": r.ID, "reason_id": storeA.returnReason, "settlement": procurement.SettlementCreditPayable,
				"lines": []map[string]any{{"receipt_line_id": purpleLine.ID, "qty": qty}}}
		}
		rec := post(a, "/purchase-returns", ret("4"))
		expect(t, rec, http.StatusCreated, "")
		xt := testkit.DecodeJSON[returnOut](t, rec)
		if xt.TotalAmount != "200000" || xt.CreditNoteID == nil || xt.Lines[0].LotID != *purpleLine.LotID || *xt.Lines[0].ColorID != purple {
			t.Fatalf("return = %+v", xt)
		}
		for lot, want := range map[uuid.UUID]string{*purpleLine.LotID: "6", *redLine.LotID: "10"} {
			var qty money.Qty
			testkit.InTenant(t, testkit.DB(t), storeA.tenant, func(q db.Querier) error {
				return q.QueryRowxContext(context.Background(), `SELECT qty FROM lot_balances WHERE lot_id = $1`, lot).Scan(&qty)
			})
			if qty.String() != want {
				t.Errorf("lot %s = %s, want %s", lot, qty, want)
			}
		}
		expect(t, post(a, "/purchase-returns", ret("7")), http.StatusConflict, procurement.CodeReturnExceedsKept)
		got := getReceiptOut(t, a, r.ID)
		if got.Payable.OpenAmount != "800000" || got.Lines[0].ReturnableBaseQty != "6" {
			t.Errorf("after the return = %+v %+v", got.Payable, got.Lines[0])
		}
	})

	t.Run("a refund return leaves the payable and is due back from the supplier", func(t *testing.T) {
		supplier := storeA.newSupplier(t, a, "Xưởng hoàn tiền")
		r := storeA.receiveDirect(t, a, supplier, storeA.newSku(t, a, "XT-HOAN"), "10", "10000")
		rec := post(a, "/purchase-returns", map[string]any{"receipt_id": r.ID, "reason_id": storeA.returnReason,
			"settlement": procurement.SettlementRefund, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "qty": "3"}}})
		expect(t, rec, http.StatusCreated, "")
		xt := testkit.DecodeJSON[returnOut](t, rec)
		if xt.Settlement != procurement.SettlementRefund || xt.TotalAmount != "30000" || xt.CreditNoteID == nil {
			t.Fatalf("return = %+v", xt)
		}
		if p := getReceiptOut(t, a, r.ID).Payable; p.OpenAmount != "100000" || p.SettledAmount != "0" {
			t.Errorf("payable = %+v, want it untouched: the supplier pays the 30000 back", p)
		}
	})

	t.Run("a replacement return credits now and the replacement receipt owes it again", func(t *testing.T) {
		supplier := storeA.newSupplier(t, a, "Xưởng đổi hàng")
		sku := storeA.newSku(t, a, "XT-DOI")
		r := storeA.receiveDirect(t, a, supplier, sku, "10", "10000")
		rec := post(a, "/purchase-returns", map[string]any{"receipt_id": r.ID, "reason_id": storeA.returnReason,
			"settlement": procurement.SettlementReplacement, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "qty": "2"}}})
		expect(t, rec, http.StatusCreated, "")
		if xt := testkit.DecodeJSON[returnOut](t, rec); xt.TotalAmount != "20000" || xt.CreditNoteID == nil {
			t.Fatalf("return = %+v", xt)
		}
		if p := getReceiptOut(t, a, r.ID).Payable; p.OpenAmount != "80000" {
			t.Errorf("payable after the return = %+v, want the credit applied", p)
		}
		storeA.receiveDirect(t, a, supplier, sku, "2", "10000")
		if open := partyOpen(t, a, supplier); open != "100000" {
			t.Errorf("owed to the supplier = %s, want 100000 as before the exchange", open)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "10" {
			t.Errorf("stock = %+v, want 10 after the exchange", st)
		}
	})

	t.Run("damaged goods go to quarantine and wrong items are not owed", func(t *testing.T) {
		sku := storeA.newSku(t, a, "HONG")
		r := postReceipt(t, a, map[string]any{"supplier_party_id": storeA.supplier, "paid_now_amount": "20000",
			"payment_method_id": storeA.cashMethod, "schedule": []map[string]any{{"due_date": "2026-04-15", "amount": "60000"}},
			"lines": []map[string]any{
				{"sku_id": sku, "qty": "5", "unit_price": "10000"},
				{"sku_id": sku, "qty": "3", "unit_price": "10000", "condition": procurement.ConditionDamaged},
				{"sku_id": sku, "qty": "2", "unit_price": "10000", "condition": procurement.ConditionWrongItem},
			}})
		if r.TotalAmount != "80000" || r.Lines[2].LotID != nil || r.Lines[2].ReturnableBaseQty != "0" {
			t.Fatalf("receipt = %+v", r)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "8" || st.Quarantine.String() != "3" {
			t.Errorf("stock = %+v, want 8 on hand of which 3 quarantined", st)
		}
		p := r.Payable
		if p.SettledAmount != "20000" || p.OpenAmount != "60000" || len(p.Schedule) != 2 || p.Schedule[1].DueDate != "2026-04-15" ||
			len(r.Payments) != 1 || r.Payments[0].Amount != "20000" {
			t.Errorf("payable = %+v payments = %+v", p, r.Payments)
		}
	})

	t.Run("an advance to the supplier settles a receipt", func(t *testing.T) {
		rec := post(a, "/payments", map[string]any{"direction": "out", "party_id": storeA.supplier, "purpose": "advance",
			"payment_method_id": storeA.cashMethod, "amount": "500000"})
		expect(t, rec, http.StatusCreated, "")
		advance := testkit.DecodeJSON[withID](t, rec).ID
		r := postReceipt(t, a, map[string]any{"supplier_party_id": storeA.supplier,
			"use_advances": []map[string]any{{"payment_id": advance, "amount": "500000"}},
			"lines":        []map[string]any{{"sku_id": storeA.newSku(t, a, "TAM-UNG"), "qty": "10", "unit_price": "100000"}}})
		if r.Payable.SettledAmount != "500000" || r.Payable.OpenAmount != "500000" {
			t.Errorf("payable = %+v", r.Payable)
		}
		expect(t, post(a, "/receipts", map[string]any{"supplier_party_id": storeA.supplier,
			"use_advances": []map[string]any{{"payment_id": advance, "amount": "1"}},
			"lines":        []map[string]any{{"sku_id": storeA.newSku(t, a, "TAM-UNG-2"), "qty": "1", "unit_price": "1000"}}}),
			http.StatusUnprocessableEntity, "allocation_exceeds_payment")
	})

	t.Run("late orders list what is overdue until it is closed short", func(t *testing.T) {
		late := confirmedOrder(t, a, map[string]any{"supplier_party_id": storeA.supplier, "order_date": "2026-03-01",
			"expected_date": "2026-03-10", "lines": []map[string]any{{"sku_id": storeA.newSku(t, a, "TRE-HAN"), "qty": "48", "unit_price": "10000"}}})
		lateRow := func() *purchaseOrderOut {
			rows := send[dataOf[purchaseOrderOut]](t, a, http.MethodGet, "/procurement/late-orders", nil).Data
			for i := range rows {
				if rows[i].ID == late.ID {
					return &rows[i]
				}
			}
			return nil
		}
		if row := lateRow(); row == nil || *row.DaysLate != 5 || *row.PendingBaseQty != "48" {
			t.Fatalf("late order = %+v", row)
		}
		path := v1 + "/purchase-orders/" + late.ID.String() + ":close-short"
		expect(t, a.doWith(http.MethodPost, path, map[string]any{"reason_id": storeA.returnReason},
			ifMatchOf(late.Version)), http.StatusUnprocessableEntity, "wrong_kind")
		rec := a.doWith(http.MethodPost, path, map[string]any{"reason_id": storeA.closeReason},
			ifMatchOf(late.Version))
		expect(t, rec, http.StatusOK, procurement.StatusClosedShort)
		if row := lateRow(); row != nil {
			t.Errorf("late order after closing = %+v", row)
		}
		expect(t, post(a, "/receipts", map[string]any{"purchase_order_id": late.ID,
			"lines": []map[string]any{{"purchase_order_line_id": late.Lines[0].ID, "qty": "1"}}}),
			http.StatusConflict, procurement.CodeOrderNotReceivable)
	})

	t.Run("a price adjustment after a partial sale splits stock and sold goods and clears the variance", func(t *testing.T) {
		sku := storeA.newSku(t, a, "DG-1")
		r := storeA.receiveDirect(t, a, storeA.supplier, sku, "10", "10000")
		sell(t, domains, storeA.tenant, sku, "4")
		rec := a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0002", "document_date": "2026-03-15", "goods_amount": "90000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{r.ID}})
		expect(t, rec, http.StatusCreated, procurement.MatchVariance)
		doc := testkit.DecodeJSON[documentOut](t, rec)
		if doc.VarianceAmount != "-10000" {
			t.Fatalf("document = %+v", doc)
		}
		rec = post(a, "/purchase-price-adjustments", map[string]any{"receipt_id": r.ID, "kind": procurement.KindFinalPrice,
			"reason_id": storeA.priceReason, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "new_unit_price": "9000"}}})
		expect(t, rec, http.StatusCreated, "")
		dg := testkit.DecodeJSON[adjustmentOut](t, rec)
		if dg.PayableDelta != "-10000" || dg.CreditNoteID == nil || dg.Lines[0].OnHandAmount != "-6000" || dg.Lines[0].SoldAmount != "-4000" {
			t.Fatalf("adjustment = %+v", dg)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || st.AvgCost.String() != "9000" {
			t.Errorf("stock = %+v, want 6 at 9000", st)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM cogs_adjustments WHERE sku_id = $1`, sku); n != 1 {
			t.Errorf("cogs adjustments = %d, want the sold part booked once", n)
		}
		if got := send[documentOut](t, a, http.MethodGet, "/supplier-documents/"+doc.ID.String(), nil); got.MatchStatus != procurement.MatchMatched {
			t.Errorf("document after the adjustment = %+v", got)
		}
		if p := getReceiptOut(t, a, r.ID).Payable; p.OpenAmount != "90000" {
			t.Errorf("payable = %+v", p)
		}
	})

	t.Run("a price increase records a new payable and raises the cost", func(t *testing.T) {
		sku := storeA.newSku(t, a, "DG-TANG")
		r := storeA.receiveDirect(t, a, storeA.supplier, sku, "10", "10000")
		sell(t, domains, storeA.tenant, sku, "4")
		obligations := countRows(t, storeA.tenant, `SELECT count(*) FROM obligations`)
		rec := post(a, "/purchase-price-adjustments", map[string]any{"receipt_id": r.ID, "kind": procurement.KindFinalPrice,
			"reason_id": storeA.priceReason, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "new_unit_price": "11000"}}})
		expect(t, rec, http.StatusCreated, "")
		dg := testkit.DecodeJSON[adjustmentOut](t, rec)
		if dg.PayableDelta != "10000" || dg.PayableID == nil || dg.CreditNoteID != nil || dg.Lines[0].OnHandAmount != "6000" ||
			dg.Lines[0].SoldAmount != "4000" {
			t.Fatalf("adjustment = %+v", dg)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM obligations`); n != obligations+1 {
			t.Errorf("obligations = %d, want one more for the increase", n)
		}
		if p := getReceiptOut(t, a, r.ID).Payable; p.OpenAmount != "100000" {
			t.Errorf("receipt payable = %+v, want it untouched", p)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || st.AvgCost.String() != "11000" {
			t.Errorf("stock = %+v, want 6 at 11000", st)
		}
		expect(t, post(a, "/purchase-price-adjustments", map[string]any{"receipt_id": r.ID, "kind": procurement.KindPostPurchaseDiscount,
			"reason_id": storeA.priceReason, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "new_unit_price": "12000"}}}),
			http.StatusUnprocessableEntity, "not_a_discount")
	})

	t.Run("a price drop to zero on a discounted line credits at most what the line is worth", func(t *testing.T) {
		sku := storeA.newSku(t, a, "DG-VE-0")
		r := postReceipt(t, a, map[string]any{"supplier_party_id": storeA.supplier, "lines": []map[string]any{
			{"sku_id": sku, "qty": "10", "unit_price": "10000", "discount_amount": "20000"}}})
		if r.Lines[0].UnitCost != "8000" {
			t.Fatalf("receipt line = %+v, want a unit cost of 8000 after the discount", r.Lines[0])
		}
		sell(t, domains, storeA.tenant, sku, "4")
		rec := post(a, "/purchase-price-adjustments", map[string]any{"receipt_id": r.ID, "kind": procurement.KindFinalPrice,
			"reason_id": storeA.priceReason, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "new_unit_price": "0"}}})
		expect(t, rec, http.StatusCreated, "")
		dg := testkit.DecodeJSON[adjustmentOut](t, rec)
		// The line is worth 80000 after its discount, so dropping the price to zero credits 80000, not
		// 10 x 10000: 48000 off the six held, which end at zero, and 32000 off the four sold.
		if dg.PayableDelta != "-80000" || dg.Lines[0].OnHandAmount != "-48000" || dg.Lines[0].SoldAmount != "-32000" {
			t.Fatalf("adjustment = %+v", dg)
		}
		if got := getReceiptOut(t, a, r.ID).Lines[0].UnitCost; got != "0" {
			t.Errorf("unit cost = %s, want 0", got)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || !st.AvgCost.IsZero() || !st.StockValue.IsZero() {
			t.Errorf("stock = %+v, want 6 at 0", st)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM cogs_adjustments WHERE sku_id = $1`, sku); n != 1 {
			t.Errorf("cogs adjustments = %d, want one row for the sold part", n)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM cogs_adjustments
			WHERE sku_id = $1 AND source_doc_type = 'purchase_price_adjustment' AND amount = -32000`, sku); n != 1 {
			t.Errorf("cogs adjustment of the price drop is not -32000")
		}
		testkit.InTenant(t, testkit.DB(t), storeA.tenant, func(q db.Querier) error {
			mismatches, err := procurement.Verify(context.Background(), q)
			if len(mismatches) > 0 {
				t.Errorf("procurement verify: %v", mismatches)
			}
			return err
		})
	})

	t.Run("a price drop on the dear receipt of a SKU whose cheap lot was sold first books what stock cannot carry to COGS", func(t *testing.T) {
		sku := storeA.newSku(t, a, "DG-HAI-LO")
		storeA.receiveDirect(t, a, storeA.supplier, sku, "10", "2000")
		dear := storeA.receiveDirect(t, a, storeA.supplier, sku, "10", "8000")
		// The sale takes the cheap lot and 4 of the dear one at the 5000 average: 6 left worth 30000.
		sell(t, domains, storeA.tenant, sku, "14")
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || st.StockValue.String() != "30000" {
			t.Fatalf("stock after the sale = %+v, want 6 worth 30000", st)
		}
		rec := post(a, "/purchase-price-adjustments", map[string]any{"receipt_id": dear.ID, "kind": procurement.KindFinalPrice,
			"reason_id": storeA.priceReason, "lines": []map[string]any{{"receipt_line_id": dear.Lines[0].ID, "new_unit_price": "0"}}})
		expect(t, rec, http.StatusCreated, "")
		dg := testkit.DecodeJSON[adjustmentOut](t, rec)
		// The dear lot still holds 6, a 48000 share of the 80000 credit, but the SKU's stock is worth
		// 30000: that much leaves stock, the other 18000 joins the 32000 of the sold units on COGS.
		if dg.PayableDelta != "-80000" || dg.Lines[0].OnHandAmount != "-30000" || dg.Lines[0].SoldAmount != "-50000" {
			t.Fatalf("adjustment = %+v", dg)
		}
		if got := getReceiptOut(t, a, dear.ID).Lines[0].UnitCost; got != "0" {
			t.Errorf("unit cost = %s, want 0", got)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || !st.StockValue.IsZero() {
			t.Errorf("stock = %+v, want 6 worth 0", st)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM cogs_adjustments
			WHERE sku_id = $1 AND source_doc_type = 'purchase_price_adjustment' AND amount = -50000`, sku); n != 1 {
			t.Errorf("cogs adjustment of the price drop is not -50000")
		}
		testkit.InTenant(t, testkit.DB(t), storeA.tenant, func(q db.Querier) error {
			mismatches, err := procurement.Verify(context.Background(), q)
			if len(mismatches) > 0 {
				t.Errorf("procurement verify: %v", mismatches)
			}
			return err
		})
	})

	t.Run("a price rise on a line received at nothing carries the line's tax", func(t *testing.T) {
		d := s.signIn(t, s.signUp(t))
		storeD := buildPurchaseStore(t, d)
		tenants = append(tenants, storeD.tenant)
		setIncludeTax := func(include bool) {
			version := d.do(http.MethodGet, v1+"/tenant/settings", nil).Header().Get("ETag")
			expect(t, d.doWith(http.MethodPatch, v1+"/tenant/settings", map[string]any{"prices_include_tax": include},
				http.Header{"If-Match": {version}}), http.StatusOK, "")
		}
		setIncludeTax(false)
		vat := send[withID](t, d, http.MethodPost, "/tax-rates", map[string]any{"name": "VAT 10", "rate_percent": "10"}).ID
		taxed := func(code string) uuid.UUID {
			return send[productRef](t, d, http.MethodPost, "/products", map[string]any{"type": "goods", "name": "Hàng " + code,
				"sku": map[string]any{"code": code, "base_unit_id": storeD.units["cái"], "tax_rate_id": vat}}).Skus[0].ID
		}
		free := map[string]any{"sku_id": taxed("DG-THUE-0"), "qty": "10", "unit_price": "2000", "discount_amount": "20000"}
		raise := func(r receiptOut, line int, from, to, want string) {
			t.Helper()
			rec := post(d, "/purchase-price-adjustments", map[string]any{"receipt_id": r.ID, "kind": procurement.KindFinalPrice,
				"reason_id": storeD.priceReason, "lines": []map[string]any{{"receipt_line_id": r.Lines[line].ID, "new_unit_price": to}}})
			expect(t, rec, http.StatusCreated, "")
			if dg := testkit.DecodeJSON[adjustmentOut](t, rec); dg.PayableDelta != want {
				t.Errorf("raising %s to %s: payable delta = %s, want %s", from, to, dg.PayableDelta, want)
			}
		}
		// The receipt holds no tax to tell its basis, so the store's (tax added on top) decides:
		// 10 × 1000 more net and 10% on it.
		alone := postReceipt(t, d, map[string]any{"supplier_party_id": storeD.supplier, "lines": []map[string]any{free}})
		if alone.TotalAmount != "0" {
			t.Fatalf("receipt total = %s, want 0", alone.TotalAmount)
		}
		raise(alone, 0, "2000", "3000", "11000")
		// A receipt that added tax on its other line keeps that basis after the store switches to
		// prices holding the tax.
		mixed := postReceipt(t, d, map[string]any{"supplier_party_id": storeD.supplier, "lines": []map[string]any{free,
			{"sku_id": taxed("DG-THUE-1"), "qty": "1", "unit_price": "10000"}}})
		if mixed.TotalAmount != "11000" {
			t.Fatalf("receipt total = %s, want 10000 + 10%%", mixed.TotalAmount)
		}
		setIncludeTax(true)
		raise(mixed, 0, "2000", "3000", "11000")
		later := postReceipt(t, d, map[string]any{"supplier_party_id": storeD.supplier, "lines": []map[string]any{free}})
		raise(later, 0, "2000", "3000", "10000")
		testkit.InTenant(t, testkit.DB(t), storeD.tenant, func(q db.Querier) error {
			mismatches, err := procurement.Verify(context.Background(), q)
			if len(mismatches) > 0 {
				t.Errorf("procurement verify: %v", mismatches)
			}
			return err
		})
	})

	t.Run("a return, a price adjustment and a landed cost sent twice post once", func(t *testing.T) {
		sku := storeA.newSku(t, a, "PHAT-LAI")
		r := storeA.receiveDirect(t, a, storeA.supplier, sku, "10", "10000")
		returns := countRows(t, storeA.tenant, `SELECT count(*) FROM purchase_returns`)
		postReplayed(t, a, "/purchase-returns", map[string]any{"receipt_id": r.ID, "reason_id": storeA.returnReason,
			"settlement": procurement.SettlementCreditPayable, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "qty": "2"}}})
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM purchase_returns`); n != returns+1 {
			t.Errorf("purchase returns = %d, want %d", n, returns+1)
		}
		adjustments := countRows(t, storeA.tenant, `SELECT count(*) FROM purchase_price_adjustments`)
		postReplayed(t, a, "/purchase-price-adjustments", map[string]any{"receipt_id": r.ID, "kind": procurement.KindFinalPrice,
			"reason_id": storeA.priceReason, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "new_unit_price": "9500"}}})
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM purchase_price_adjustments`); n != adjustments+1 {
			t.Errorf("price adjustments = %d, want %d", n, adjustments+1)
		}
		postReplayed(t, a, "/receipts/"+r.ID.String()+"/landed-costs", map[string]any{"cost_type": procurement.CostShipping,
			"amount": "8000", "payee_party_id": storeA.shipper, "due_date": "2026-03-31"})
		if got := getReceiptOut(t, a, r.ID); len(got.LandedCosts) != 1 || got.Lines[0].LandedCostAmount != "8000" {
			t.Errorf("receipt after the replays = %+v", got)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "8" || st.StockValue.String() != "84000" {
			t.Errorf("stock = %+v, want 8 worth 8 x 9500 + 8000", st)
		}
	})

	t.Run("a posting dated in a locked period is refused", func(t *testing.T) {
		expect(t, post(a, "/period-locks", map[string]any{"locked_through": "2026-02-28"}), http.StatusCreated, "")
		sku := storeA.newSku(t, a, "KHOA-SO")
		receipts := countRows(t, storeA.tenant, `SELECT count(*) FROM receipts`)
		body := func(date string) map[string]any {
			return map[string]any{"supplier_party_id": storeA.supplier, "business_date": date,
				"lines": []map[string]any{{"sku_id": sku, "qty": "1", "unit_price": "1000"}}}
		}
		expect(t, post(a, "/receipts", body("2026-02-20")), http.StatusLocked, "period_locked")
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM receipts`); n != receipts {
			t.Errorf("receipts = %d, want %d", n, receipts)
		}
		postReceipt(t, a, body("2026-03-01"))
	})

	t.Run("only a receipt nothing builds on and whose goods are all there can be reversed", func(t *testing.T) {
		plain := storeA.newSku(t, a, "HUY-1")
		r := storeA.receiveDirect(t, a, storeA.supplier, plain, "5", "20000")
		reverse := func(id uuid.UUID) *httptest.ResponseRecorder {
			return post(a, "/receipts/"+id.String()+":reverse", map[string]any{"reason": "Nhập nhầm nhà cung cấp"})
		}
		clk.Advance(middleware.ReauthWindow + time.Minute)
		expect(t, reverse(r.ID), http.StatusForbidden, "reauth_required")
		confirmPassword(t, a)

		rec := reverse(r.ID)
		expect(t, rec, http.StatusCreated, "")
		rev := testkit.DecodeJSON[receiptOut](t, rec)
		if rev.ReversalOf == nil || *rev.ReversalOf != r.ID {
			t.Fatalf("reversal = %+v", rev)
		}
		got := getReceiptOut(t, a, r.ID)
		if got.Status != procurement.DocReversed || got.Payable == nil || got.Payable.ReversedOn == nil {
			t.Errorf("reversed receipt = %+v %+v", got, got.Payable)
		}
		if st := stockOf(t, storeA.tenant, plain); !st.OnHand.IsZero() {
			t.Errorf("stock after reversal = %+v", st)
		}
		expect(t, reverse(r.ID), http.StatusConflict, procurement.CodeAlreadyReversed)
		expect(t, reverse(rev.ID), http.StatusConflict, procurement.CodeReversalNotReversed)

		// A landed cost and a supplier document both build on a receipt.
		costed := storeA.receiveDirect(t, a, storeA.supplier, storeA.newSku(t, a, "HUY-CP"), "5", "20000")
		expect(t, post(a, "/receipts/"+costed.ID.String()+"/landed-costs", map[string]any{"cost_type": procurement.CostShipping,
			"amount": "5000", "payee_party_id": storeA.shipper, "due_date": "2026-03-31"}), http.StatusCreated, "")
		expect(t, reverse(costed.ID), http.StatusConflict, procurement.CodeReceiptHasFollowups)
		matched := storeA.receiveDirect(t, a, storeA.supplier, storeA.newSku(t, a, "HUY-HD"), "5", "20000")
		expect(t, a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-0201", "document_date": "2026-03-15", "goods_amount": "100000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{matched.ID}}), http.StatusCreated, "")
		expect(t, reverse(matched.ID), http.StatusConflict, procurement.CodeReceiptHasFollowups)

		sold := storeA.newSku(t, a, "HUY-2")
		consumed := storeA.receiveDirect(t, a, storeA.supplier, sold, "5", "20000")
		sell(t, domains, storeA.tenant, sold, "2")
		expect(t, reverse(consumed.ID), http.StatusConflict, procurement.CodeReceiptStockConsumed)

		// Reversing the receipt of an order gives the order its pending quantity back.
		po := confirmedOrder(t, a, map[string]any{"supplier_party_id": storeA.supplier,
			"lines": []map[string]any{{"sku_id": storeA.newSku(t, a, "HUY-3"), "qty": "10", "unit_price": "1000"}}})
		pr := receiveOrder(t, a, po, "10")
		expect(t, a.do(http.MethodGet, v1+"/purchase-orders/"+po.ID.String(), nil), http.StatusOK, procurement.StatusCompleted)
		expect(t, reverse(pr.ID), http.StatusCreated, "")
		back := getOrderOut(t, a, po.ID)
		if back.Status != procurement.StatusConfirmed || back.Lines[0].ReceivedBaseQty != "0" || back.Lines[0].PendingBaseQty != "10" {
			t.Errorf("order after reversal = %+v", back)
		}
	})

	t.Run("reversing a receipt of an order closed short keeps the order closed", func(t *testing.T) {
		po := confirmedOrder(t, a, map[string]any{"supplier_party_id": storeA.supplier,
			"lines": []map[string]any{{"sku_id": storeA.newSku(t, a, "DONG-THIEU"), "qty": "10", "unit_price": "1000"}}})
		r := receiveOrder(t, a, po, "4")
		open := getOrderOut(t, a, po.ID)
		expect(t, a.doWith(http.MethodPost, v1+"/purchase-orders/"+po.ID.String()+":close-short",
			map[string]any{"reason_id": storeA.closeReason}, ifMatchOf(open.Version)), http.StatusOK, procurement.StatusClosedShort)

		confirmPassword(t, a)
		expect(t, post(a, "/receipts/"+r.ID.String()+":reverse", map[string]any{"reason": "Nhập sai số lượng"}), http.StatusCreated, "")
		got := getOrderOut(t, a, po.ID)
		if got.Status != procurement.StatusClosedShort || got.Lines[0].ReceivedBaseQty != "0" {
			t.Errorf("order after the reversal = %+v, want it closed short with nothing received", got)
		}
	})

	t.Run("a read-only store gets its committed receipt back but posts nothing else", func(t *testing.T) {
		ownerC := s.signUp(t)
		c := s.signIn(t, ownerC)
		storeC := buildPurchaseStore(t, c)
		tenants = append(tenants, storeC.tenant)
		sku := storeC.newSku(t, c, "CHI-DOC")
		body := map[string]any{"supplier_party_id": storeC.supplier, "lines": []map[string]any{{"sku_id": sku, "qty": "1", "unit_price": "1000"}}}
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec := c.doWith(http.MethodPost, v1+"/receipts", body, key)
		expect(t, rec, http.StatusCreated, "")
		r := testkit.DecodeJSON[withID](t, rec)

		if _, err := testkit.SuperDB(t).ExecContext(context.Background(),
			`UPDATE tally.tenants SET status = 'read_only' WHERE id = $1`, storeC.tenant); err != nil {
			t.Fatal(err)
		}
		again := c.doWith(http.MethodPost, v1+"/receipts", body, key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[withID](t, again).ID != r.ID {
			t.Fatalf("replay = %s %s", again.Header(), again.Body)
		}
		// A new posting is refused by the posting coordinator: posting routes skip the write
		// gate so that the replay above gets through.
		receipts := countRows(t, storeC.tenant, `SELECT count(*) FROM receipts`)
		expect(t, post(c, "/receipts", body), http.StatusForbidden, "subscription_read_only")
		if n := countRows(t, storeC.tenant, `SELECT count(*) FROM receipts`); n != receipts {
			t.Errorf("receipts of the read-only store = %d, want %d", n, receipts)
		}
		expect(t, c.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeC.supplier,
			"document_no": "HD-RO", "document_date": "2026-03-15", "goods_amount": "1000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{r.ID}}), http.StatusForbidden, "subscription_read_only")
		expect(t, c.do(http.MethodGet, v1+"/receipts/"+r.ID.String(), nil), http.StatusOK, "")
	})

	t.Run("ids of another store are refused", func(t *testing.T) {
		skuA := storeA.newSku(t, a, "CHI-A")
		orderA := confirmedOrder(t, a, map[string]any{"supplier_party_id": storeA.supplier,
			"lines": []map[string]any{{"sku_id": skuA, "qty": "5", "unit_price": "1000"}}})
		receiptA := receiveOrder(t, a, orderA, "5")
		rec := post(a, "/payments", map[string]any{"direction": "out", "party_id": storeA.supplier, "purpose": "advance",
			"payment_method_id": storeA.cashMethod, "amount": "1000"})
		expect(t, rec, http.StatusCreated, "")
		advanceA := testkit.DecodeJSON[withID](t, rec).ID

		for _, path := range []string{"/purchase-orders/" + orderA.ID.String(), "/receipts/" + receiptA.ID.String(),
			"/purchase-orders/" + orderA.ID.String() + "/receivable-lines"} {
			expect(t, b.do(http.MethodGet, v1+path, nil), http.StatusNotFound, "not_found")
		}
		for _, path := range []string{"/purchase-orders", "/receipts", "/supplier-documents", "/purchase-returns",
			"/purchase-price-adjustments", "/procurement/late-orders"} {
			ofA := map[uuid.UUID]bool{}
			for _, row := range send[dataOf[withID]](t, a, http.MethodGet, path, nil).Data {
				ofA[row.ID] = true
			}
			for _, row := range send[dataOf[withID]](t, b, http.MethodGet, path, nil).Data {
				if ofA[row.ID] {
					t.Errorf("B lists %s of A in %s", row.ID, path)
				}
			}
		}

		skuB := storeB.newSku(t, b, "CHI-B")
		receiptB := storeB.receiveDirect(t, b, storeB.supplier, skuB, "1", "1000")
		receipts := countRows(t, storeB.tenant, `SELECT count(*) FROM receipts`)
		returns := countRows(t, storeB.tenant, `SELECT count(*) FROM purchase_returns`)
		receiveB := func(line map[string]any, extra map[string]any) *httptest.ResponseRecorder {
			body := map[string]any{"supplier_party_id": storeB.supplier, "lines": []map[string]any{line}}
			for k, v := range extra {
				body[k] = v
			}
			return post(b, "/receipts", body)
		}
		expect(t, post(b, "/receipts", map[string]any{"supplier_party_id": storeA.supplier,
			"lines": []map[string]any{{"sku_id": skuB, "qty": "1", "unit_price": "1000"}}}),
			http.StatusUnprocessableEntity, "not_a_supplier")
		expectRejected(t, "receipt of A's SKU", receiveB(map[string]any{"sku_id": skuA, "qty": "1", "unit_price": "1000"}, nil))
		expectRejected(t, "receipt in A's color", receiveB(map[string]any{"sku_id": skuB, "color_id": storeA.colors["tim"],
			"qty": "1", "unit_price": "1000"}, nil))
		expectRejected(t, "receipt settled by A's advance", receiveB(map[string]any{"sku_id": skuB, "qty": "1", "unit_price": "1000"},
			map[string]any{"use_advances": []map[string]any{{"payment_id": advanceA, "amount": "1000"}}}))
		expectRejected(t, "receipt of A's order", post(b, "/receipts", map[string]any{"purchase_order_id": orderA.ID,
			"lines": []map[string]any{{"purchase_order_line_id": orderA.Lines[0].ID, "qty": "1"}}}))
		expectRejected(t, "return with A's reason", post(b, "/purchase-returns", map[string]any{"receipt_id": receiptB.ID,
			"reason_id": storeA.returnReason, "settlement": procurement.SettlementRefund,
			"lines": []map[string]any{{"receipt_line_id": receiptB.Lines[0].ID, "qty": "1"}}}))
		expect(t, post(b, "/purchase-returns", map[string]any{"receipt_id": receiptA.ID, "reason_id": storeB.returnReason,
			"settlement": procurement.SettlementRefund, "lines": []map[string]any{{"receipt_line_id": receiptA.Lines[0].ID, "qty": "1"}}}),
			http.StatusNotFound, "not_found")
		if n := countRows(t, storeB.tenant, `SELECT count(*) FROM receipts`); n != receipts {
			t.Errorf("B receipts = %d, want %d", n, receipts)
		}
		if n := countRows(t, storeB.tenant, `SELECT count(*) FROM purchase_returns`); n != returns {
			t.Errorf("B purchase returns = %d, want %d", n, returns)
		}
		if got := getReceiptOut(t, a, receiptA.ID); got.Lines[0].ReturnableBaseQty != "5" {
			t.Errorf("A's receipt after B's attempts = %+v", got.Lines[0])
		}
	})

	t.Run("a document photo is uploaded for a receipt or supplier document of the store only", func(t *testing.T) {
		r := storeA.receiveDirect(t, a, storeA.supplier, storeA.newSku(t, a, "ANH-PN"), "1", "1000")
		rec := a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": storeA.supplier,
			"document_no": "HD-ANH", "document_date": "2026-03-15", "goods_amount": "1000", "tax_amount": "0"})
		expect(t, rec, http.StatusCreated, "")
		doc := testkit.DecodeJSON[documentOut](t, rec)
		receiptB := storeB.receiveDirect(t, b, storeB.supplier, storeB.newSku(t, b, "ANH-B"), "1", "1000")
		upload := func(ownerType string, ownerID uuid.UUID) *httptest.ResponseRecorder {
			return a.do(http.MethodPost, v1+"/attachments", map[string]any{"owner_type": ownerType, "owner_id": ownerID,
				"file_name": "phieu-nhap.jpg", "content_type": "image/jpeg", "size_bytes": 2048, "sha256": strings.Repeat("ab", 32)})
		}
		attachments := countRows(t, storeA.tenant, `SELECT count(*) FROM attachments`)
		for _, c := range []struct {
			what, ownerType string
			ownerID         uuid.UUID
			field, code     string
		}{
			{"an unregistered owner type", "purchase_receipt", r.ID, "owner_type", "invalid"},
			{"a receipt that does not exist", procurement.DocReceipt, ids.New(), "owner_id", "not_found"},
			{"a receipt of another store", procurement.DocReceipt, receiptB.ID, "owner_id", "not_found"},
			{"a supplier document given as a receipt", procurement.DocReceipt, doc.ID, "owner_id", "not_found"},
		} {
			rec := upload(c.ownerType, c.ownerID)
			if rec.Code != http.StatusUnprocessableEntity || testkit.DecodeJSON[httpx.ErrorBody](t, rec).Error.Fields[c.field] != c.code {
				t.Errorf("upload for %s = %d %s, want 422 %s %s", c.what, rec.Code, rec.Body, c.field, c.code)
			}
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM attachments`); n != attachments {
			t.Errorf("attachments after refused uploads = %d, want %d", n, attachments)
		}

		photo := func(ownerType string, ownerID uuid.UUID) uuid.UUID {
			rec := upload(ownerType, ownerID)
			expect(t, rec, http.StatusCreated, "")
			return testkit.DecodeJSON[struct {
				Attachment withID `json:"attachment"`
			}](t, rec).Attachment.ID
		}
		ofReceipt, ofDocument := photo(procurement.DocReceipt, r.ID), photo(procurement.AttachmentOwnerSupplierDocument, doc.ID)
		if got := getReceiptOut(t, a, r.ID).Attachments; len(got) != 1 || got[0].ID != ofReceipt {
			t.Errorf("receipt attachments = %+v, want %s", got, ofReceipt)
		}
		if got := send[documentOut](t, a, http.MethodGet, "/supplier-documents/"+doc.ID.String(), nil).Attachments; len(got) != 1 ||
			got[0].ID != ofDocument {
			t.Errorf("supplier document attachments = %+v, want %s", got, ofDocument)
		}
	})

	t.Run("every ledger verifies after the postings above", func(t *testing.T) {
		for _, tenant := range tenants {
			testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
				for name, verify := range map[string]func(context.Context, db.Querier) ([]string, error){
					"procurement": procurement.Verify, "inventory": inventory.Verify, "debt": debt.Verify, "cash": cash.Verify,
				} {
					mismatches, err := verify(context.Background(), q)
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
	})
}

// The verifier catches what only a bug could leave behind: each row is corrupted as superuser,
// reported, then put back, and the store verifies clean again.
func TestProcurementVerifyReportsTamperedRows(t *testing.T) {
	s, _, domains := newProcurementStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildPurchaseStore(t, a)
	super := testkit.SuperDB(t)
	verify := func(t *testing.T) []string {
		t.Helper()
		var out []string
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			var err error
			out, err = procurement.Verify(context.Background(), q)
			return err
		})
		return out
	}
	// tamper runs set on id, checks that Verify reports exactly the mismatches starting with want,
	// and runs undo on id.
	tamper := func(t *testing.T, id uuid.UUID, set, undo string, want ...string) []string {
		t.Helper()
		if _, err := super.Exec(set, id); err != nil {
			t.Fatal(err)
		}
		got := verify(t)
		if _, err := super.Exec(undo, id); err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !slices.ContainsFunc(got, func(m string) bool { return strings.HasPrefix(m, w+" ") }) {
				t.Errorf("verify = %v, want a mismatch %q", got, w)
			}
		}
		if len(got) != len(want) {
			t.Errorf("verify = %v, want %d mismatches", got, len(want))
		}
		if again := verify(t); len(again) > 0 {
			t.Fatalf("verify after putting the row back = %v", again)
		}
		return got
	}
	document := func(t *testing.T, no string, r receiptOut) documentOut {
		t.Helper()
		rec := a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": st.supplier,
			"document_no": no, "document_date": "2026-03-15", "goods_amount": r.TotalAmount, "tax_amount": "0",
			"receipt_ids": []uuid.UUID{r.ID}})
		expect(t, rec, http.StatusCreated, procurement.MatchMatched)
		return testkit.DecodeJSON[documentOut](t, rec)
	}
	returned := func(t *testing.T, r receiptOut, qty string) {
		t.Helper()
		expect(t, post(a, "/purchase-returns", map[string]any{"receipt_id": r.ID, "reason_id": st.returnReason,
			"settlement": procurement.SettlementCreditPayable, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "qty": qty}}}),
			http.StatusCreated, "")
	}
	landed := func(t *testing.T, r receiptOut, amount string) {
		t.Helper()
		expect(t, post(a, "/receipts/"+r.ID.String()+"/landed-costs", map[string]any{"cost_type": procurement.CostShipping,
			"amount": amount, "payee_party_id": st.shipper, "due_date": "2026-03-31"}), http.StatusCreated, "")
	}

	matchedReceipt := st.receiveDirect(t, a, st.supplier, st.newSku(t, a, "KT-HD"), "5", "20000")
	matchedDoc := document(t, "HD-KT-1", matchedReceipt)
	// 10 received at 10000, 4 sent back, then 6000 of freight over the 6 kept: 11000 each.
	costed := st.receiveDirect(t, a, st.supplier, st.newSku(t, a, "KT-GV"), "10", "10000")
	returned(t, costed, "4")
	landed(t, costed, "6000")
	if got := getReceiptOut(t, a, costed.ID).Lines[0].UnitCost; got != "11000" {
		t.Fatalf("unit cost = %s, want 11000", got)
	}
	// 10000 of freight over 3 units costs 3333.3333 each; after 2 go back the one kept carries
	// 13333.3334, a rounding the verifier tolerates.
	rounded := st.receiveDirect(t, a, st.supplier, st.newSku(t, a, "KT-LT"), "3", "10000")
	landed(t, rounded, "10000")
	returned(t, rounded, "2")
	// 10 received at 10000 and priced 9000 after 4 were sold.
	pricedSku := st.newSku(t, a, "KT-DG")
	priced := st.receiveDirect(t, a, st.supplier, pricedSku, "10", "10000")
	sell(t, domains, st.tenant, pricedSku, "4")
	expect(t, post(a, "/purchase-price-adjustments", map[string]any{"receipt_id": priced.ID, "kind": procurement.KindFinalPrice,
		"reason_id": st.priceReason, "lines": []map[string]any{{"receipt_line_id": priced.Lines[0].ID, "new_unit_price": "9000"}}}),
		http.StatusCreated, "")
	// The reversal of a receipt of the same total as the matched one.
	rec := post(a, "/receipts/"+st.receiveDirect(t, a, st.supplier, st.newSku(t, a, "KT-DAO"), "5", "20000").ID.String()+":reverse",
		map[string]any{"reason": "Nhập nhầm nhà cung cấp"})
	expect(t, rec, http.StatusCreated, "")
	reversal := testkit.DecodeJSON[receiptOut](t, rec)
	if got := verify(t); len(got) > 0 {
		t.Fatalf("verify before tampering = %v", got)
	}

	t.Run("a reversed receipt still matched to a supplier document", func(t *testing.T) {
		tamper(t, matchedReceipt.ID,
			`UPDATE tally.receipts SET status = 'reversed' WHERE id = $1`, `UPDATE tally.receipts SET status = 'posted' WHERE id = $1`,
			"supplier document receipt "+matchedDoc.ID.String(), "reversed receipt payable "+matchedReceipt.ID.String())
	})
	t.Run("a matched amount that is not the receipt total moved by its price adjustments", func(t *testing.T) {
		tamper(t, matchedReceipt.ID,
			`UPDATE tally.supplier_document_receipts SET matched_amount = matched_amount - 1000 WHERE receipt_id = $1`,
			`UPDATE tally.supplier_document_receipts SET matched_amount = matched_amount + 1000 WHERE receipt_id = $1`,
			"supplier document matched amount "+matchedDoc.ID.String(), "supplier document variance "+matchedDoc.ID.String())
	})
	t.Run("a document variance that does not follow from its matches", func(t *testing.T) {
		tamper(t, matchedDoc.ID,
			`UPDATE tally.supplier_documents SET match_status = 'variance', variance_amount = 1000 WHERE id = $1`,
			`UPDATE tally.supplier_documents SET match_status = 'matched', variance_amount = 0 WHERE id = $1`,
			"supplier document variance "+matchedDoc.ID.String())
	})
	t.Run("a supplier document matched to the reversal of a receipt", func(t *testing.T) {
		matched := matchedReceipt.ID.String()
		tamper(t, reversal.ID,
			`UPDATE tally.supplier_document_receipts SET receipt_id = $1 WHERE receipt_id = '`+matched+`'`,
			`UPDATE tally.supplier_document_receipts SET receipt_id = '`+matched+`' WHERE receipt_id = $1`,
			"supplier document receipt "+matchedDoc.ID.String())
	})
	t.Run("a price adjustment line that does not carry what it took off the unit cost", func(t *testing.T) {
		line := priced.Lines[0].ID
		tamper(t, line,
			`UPDATE tally.purchase_price_adjustment_lines SET on_hand_amount = on_hand_amount + 1000 WHERE receipt_line_id = $1`,
			`UPDATE tally.purchase_price_adjustment_lines SET on_hand_amount = on_hand_amount - 1000 WHERE receipt_line_id = $1`,
			"receipt line unit cost "+line.String())
	})
	t.Run("a unit cost spreading the freight over the units received instead of the units kept", func(t *testing.T) {
		line := costed.Lines[0].ID
		got := tamper(t, line,
			`UPDATE tally.receipt_lines SET unit_cost = 10600 WHERE id = $1`, `UPDATE tally.receipt_lines SET unit_cost = 11000 WHERE id = $1`,
			"receipt line unit cost "+line.String())
		if len(got) == 1 && !strings.Contains(got[0], "expected 11000") {
			t.Errorf("mismatch = %s, want the unit cost of the kept units", got[0])
		}
	})
}

// A received quantity that a bug pushed up to the ordered one leaves the order completed, so it
// takes no more goods. The rebuild recomputes the quantity from the receipts and gives the order
// the status the receipt posting gives that quantity, and the order takes the rest.
func TestProcurementRebuildReopensAnOrderLeftCompleted(t *testing.T) {
	s, clk, _ := newProcurementStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildPurchaseStore(t, a)
	super := testkit.SuperDB(t)
	inTenant := func(fn func(q db.Querier) error) { testkit.InTenant(t, testkit.DB(t), st.tenant, fn) }
	verify := func() []string {
		var out []string
		inTenant(func(q db.Querier) error {
			var err error
			out, err = procurement.Verify(context.Background(), q)
			return err
		})
		return out
	}

	po := confirmedOrder(t, a, map[string]any{"supplier_party_id": st.supplier,
		"lines": []map[string]any{{"sku_id": st.newSku(t, a, "XAY-LAI"), "qty": "10", "unit_price": "1000"}}})
	receiveOrder(t, a, po, "4")
	before := getOrderOut(t, a, po.ID)
	if before.Status != procurement.StatusPartiallyReceived {
		t.Fatalf("order after 4 of 10 = %+v", before)
	}
	if _, err := super.Exec(`UPDATE tally.purchase_order_lines SET received_base_qty = base_qty WHERE purchase_order_id = $1`, po.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(`UPDATE tally.purchase_orders SET status = 'completed' WHERE id = $1`, po.ID); err != nil {
		t.Fatal(err)
	}
	expect(t, post(a, "/receipts", map[string]any{"purchase_order_id": po.ID,
		"lines": []map[string]any{{"purchase_order_line_id": po.Lines[0].ID, "qty": "1"}}}), http.StatusConflict, procurement.CodeOrderNotReceivable)
	// Verify derives the status from the stored quantity, which the bug made agree with it, so only
	// the quantity is reported; the rebuild derives the status again once the quantity is right.
	if got := verify(); len(got) != 1 || !strings.HasPrefix(got[0], "purchase order line received "+po.Lines[0].ID.String()+" ") {
		t.Errorf("verify = %v, want the received quantity of the line", got)
	}
	rebuild := func() []posting.Rebuilt {
		var out []posting.Rebuilt
		inTenant(func(q db.Querier) error {
			if err := posting.LockTenant(context.Background(), q); err != nil {
				return err
			}
			var err error
			out, err = procurement.Rebuild(context.Background(), q, clk.Now())
			return err
		})
		return out
	}
	want := []posting.Rebuilt{{Check: "purchase order line received", Rows: 1}, {Check: "purchase order status", Rows: 1}}
	if got := rebuild(); !slices.Equal(got, want) {
		t.Errorf("rebuild = %+v, want %+v", got, want)
	}
	if got := verify(); len(got) > 0 {
		t.Fatalf("verify after the rebuild = %v", got)
	}
	after := getOrderOut(t, a, po.ID)
	if after.Status != procurement.StatusPartiallyReceived || after.Lines[0].ReceivedBaseQty != "4" || after.Lines[0].PendingBaseQty != "6" {
		t.Fatalf("order after the rebuild = %+v", after)
	}
	// The line rewrite and the status write each move the order on: a client holding the
	// corrupted order's version has to read it again.
	if after.Version != before.Version+2 {
		t.Errorf("order version after the rebuild = %d, want %d", after.Version, before.Version+2)
	}

	// A status written wrong over right quantities is a mismatch of its own.
	if _, err := super.Exec(`UPDATE tally.purchase_orders SET status = 'completed' WHERE id = $1`, po.ID); err != nil {
		t.Fatal(err)
	}
	if got := verify(); len(got) != 1 || !strings.HasPrefix(got[0], "purchase order status "+po.ID.String()+" ") {
		t.Errorf("verify = %v, want the status of the order", got)
	}
	if got := rebuild(); !slices.Equal(got, want[1:]) {
		t.Errorf("rebuild = %+v, want %+v", got, want[1:])
	}
	if again := getOrderOut(t, a, po.ID); again.Status != procurement.StatusPartiallyReceived || again.Version != after.Version+1 {
		t.Fatalf("order after the status rebuild = %+v", again)
	}
	receiveOrder(t, a, po, "6")
	if done := getOrderOut(t, a, po.ID); done.Status != procurement.StatusCompleted || done.Lines[0].PendingBaseQty != "0" {
		t.Errorf("order after the rest = %+v", done)
	}
}
