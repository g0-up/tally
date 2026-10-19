//go:build integration

package app_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
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
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// newSalesStack is the HTTP stack with the sales routes and every module they lean on, including
// procurement to receive the goods a test sells.
func newSalesStack(t *testing.T, opts ...app.DomainOption) (stack, *clock.Fixed) {
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
	domains := app.NewDomains(deps.DB, clk, deps.Audit, append([]app.DomainOption{app.WithEntitlements(entitlements)}, opts...)...)
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
			Printing: printing.NewHandler(domains.Printing),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk
}

// salesStore is one store's customer, supplier, payment methods and lookups for selling goods.
type salesStore struct {
	tenant                       uuid.UUID
	units, colors                map[string]uuid.UUID
	supplier, customer           uuid.UUID
	cashMethod, transferMethod   uuid.UUID
	returnReason, exchangeReason uuid.UUID
}

func buildSalesStore(t *testing.T, b *browser) salesStore {
	t.Helper()
	st := salesStore{tenant: tenantOf(t, b), units: unitsOf(t, b), colors: map[string]uuid.UUID{}}
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
		switch m.Code {
		case cash.MethodCash:
			st.cashMethod = m.ID
		case cash.MethodBankTransfer:
			st.transferMethod = m.ID
		}
	}
	st.supplier = send[withID](t, b, http.MethodPost, "/suppliers", map[string]any{"kind": "organization", "name": "Xưởng may Bình Tân"}).ID
	st.customer = send[withID](t, b, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Chị Lan"}).ID
	reasons := send[dataOf[withID]](t, b, http.MethodGet, "/reasons?kind=sales_return", nil).Data
	if len(reasons) < 2 || st.cashMethod == uuid.Nil || st.transferMethod == uuid.Nil || st.colors["do"] == uuid.Nil ||
		st.colors["tim"] == uuid.Nil {
		t.Fatalf("seeded lookups = %+v, reasons %v", st, reasons)
	}
	st.returnReason, st.exchangeReason = reasons[0].ID, reasons[1].ID
	return st
}

// newSku creates a goods SKU in "cái" sold at retail price.
func (st salesStore) newSku(t *testing.T, b *browser, code, price string) uuid.UUID {
	t.Helper()
	sku := send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"type": "goods", "name": "Hàng " + code, "sku": map[string]any{"code": code, "base_unit_id": st.units["cái"]},
	}).Skus[0].ID
	send[map[string]any](t, b, http.MethodPost, "/skus/"+sku.String()+"/sale-price",
		map[string]any{"kind": "retail", "price": price, "source": "catalog"})
	return sku
}

// receive posts a receipt of qty base units of sku in color (nil for none) at unitCost.
func (st salesStore) receive(t *testing.T, b *browser, sku uuid.UUID, color *uuid.UUID, qty, unitCost string) {
	t.Helper()
	line := map[string]any{"sku_id": sku, "qty": qty, "unit_price": unitCost}
	if color != nil {
		line["color_id"] = *color
	}
	postReceipt(t, b, map[string]any{"supplier_party_id": st.supplier, "lines": []map[string]any{line}})
}

// cashSale is a walk-in checkout of lines paid in full in cash.
func (st salesStore) cashSale(amount string, lines ...map[string]any) map[string]any {
	return map[string]any{"lines": lines, "payments": []map[string]any{{"payment_method_id": st.cashMethod, "amount": amount}}}
}

func saleLine(sku uuid.UUID, qty string, color *uuid.UUID) map[string]any {
	l := map[string]any{"sku_id": sku, "qty": qty}
	if color != nil {
		l["color_id"] = *color
	}
	return l
}

type salesLineOut struct {
	ID             uuid.UUID  `json:"id"`
	SkuID          *uuid.UUID `json:"sku_id"`
	ColorID        *uuid.UUID `json:"color_id"`
	BaseQty        string     `json:"base_qty"`
	LineAmount     string     `json:"line_amount"`
	Kind           string     `json:"line_kind"`
	MixKey         *string    `json:"mix_key"`
	ReservedQty    string     `json:"reserved_base_qty"`
	BackorderQty   string     `json:"backorder_base_qty"`
	DeliveredQty   string     `json:"delivered_base_qty"`
	ReturnedQty    string     `json:"returned_base_qty"`
	CogsAmount     string     `json:"cogs_amount"`
	InvoiceLineID  *uuid.UUID `json:"invoice_line_id"`
	StockOutLineID *uuid.UUID `json:"stock_out_line_id"`
}

type paymentStateOut struct {
	Paid string `json:"paid_amount"`
	Open string `json:"open_amount"`
	Code string `json:"code"`
}

type salesDocOut struct {
	ID           uuid.UUID       `json:"id"`
	DocNo        string          `json:"doc_no"`
	Source       string          `json:"source"`
	Status       string          `json:"status"`
	InvoiceID    *uuid.UUID      `json:"invoice_id"`
	StockOutID   *uuid.UUID      `json:"stock_out_id"`
	ShiftID      *uuid.UUID      `json:"shift_id"`
	TotalAmount  string          `json:"total_amount"`
	CogsAmount   string          `json:"cogs_amount"`
	CashTendered string          `json:"cash_tendered_amount"`
	Change       string          `json:"change_amount"`
	Lines        []salesLineOut  `json:"lines"`
	Payment      paymentStateOut `json:"payment"`
}

type salesPaymentOut struct {
	ID     uuid.UUID `json:"id"`
	DocNo  string    `json:"doc_no"`
	Amount string    `json:"amount"`
}

type posSaleOut struct {
	Document salesDocOut `json:"sales_document"`
	Shift    struct {
		ID     uuid.UUID `json:"id"`
		DocNo  string    `json:"doc_no"`
		Status string    `json:"status"`
	} `json:"shift"`
	Payments []salesPaymentOut `json:"payments"`
}

type quoteOut struct {
	ID                 uuid.UUID  `json:"id"`
	Status             string     `json:"status"`
	QuoteDate          string     `json:"quote_date"`
	ValidUntil         string     `json:"valid_until"`
	TotalAmount        string     `json:"total_amount"`
	ConvertedInvoiceID *uuid.UUID `json:"converted_invoice_id"`
}

type invoiceOut struct {
	ID              uuid.UUID       `json:"id"`
	Version         int             `json:"version"`
	CurrentVersion  int             `json:"current_version"`
	Status          string          `json:"status"`
	Payment         paymentStateOut `json:"payment"`
	QuoteID         *uuid.UUID      `json:"quote_id"`
	TotalAmount     string          `json:"total_amount"`
	DeliveredAmount string          `json:"delivered_amount"`
	Lines           []salesLineOut  `json:"lines"`
}

type stockOutOut struct {
	ID       uuid.UUID      `json:"id"`
	DocNo    string         `json:"doc_no"`
	Status   string         `json:"status"`
	Lines    []salesLineOut `json:"lines"`
	Document *salesDocOut   `json:"sales_document"`
}

type salesReturnOut struct {
	ID            uuid.UUID  `json:"id"`
	DocNo         string     `json:"doc_no"`
	TotalAmount   string     `json:"total_amount"`
	DebtReduction string     `json:"debt_reduction_amount"`
	RefundDue     string     `json:"refund_due_amount"`
	ExchangeID    *uuid.UUID `json:"exchange_id"`
	Lines         []struct {
		ColorID   *uuid.UUID `json:"color_id"`
		BaseQty   string     `json:"base_qty"`
		UnitCost  string     `json:"unit_cost"`
		RestockTo string     `json:"restock_to"`
	} `json:"lines"`
}

type exchangeSplitOut struct {
	Returned      string `json:"returned_amount"`
	Taken         string `json:"taken_amount"`
	DebtReduction string `json:"debt_reduction_amount"`
	CustomerPays  string `json:"customer_pays_amount"`
	StoreRefunds  string `json:"store_refunds_amount"`
}

type exchangePostedOut struct {
	Exchange struct {
		ID            uuid.UUID `json:"id"`
		DocNo         string    `json:"doc_no"`
		SalesReturnID uuid.UUID `json:"sales_return_id"`
		NewDocumentID uuid.UUID `json:"new_sales_document_id"`
		exchangeSplitOut
	} `json:"exchange"`
	Return   salesReturnOut   `json:"sales_return"`
	Document salesDocOut      `json:"sales_document"`
	Payment  *salesPaymentOut `json:"payment"`
}

type cartOut struct {
	ID              uuid.UUID      `json:"id"`
	Version         int            `json:"version"`
	Status          string         `json:"status"`
	SalesDocumentID *uuid.UUID     `json:"sales_document_id"`
	Lines           []salesLineOut `json:"lines"`
}

type printOut struct {
	Label   string `json:"label"`
	DocType string `json:"doc_type"`
	DocNo   string `json:"doc_no"`
	Groups  []struct {
		MixKey *string           `json:"mix_key"`
		Lines  []json.RawMessage `json:"lines"`
	} `json:"groups"`
	Returned []json.RawMessage `json:"returned_lines"`
	Related  []json.RawMessage `json:"related"`
}

type offlineItemOut struct {
	ClientID        string     `json:"offline_client_id"`
	Status          string     `json:"status"`
	SalesDocumentID *uuid.UUID `json:"sales_document_id"`
	ExceptionID     *uuid.UUID `json:"sync_exception_id"`
	ErrorCode       string     `json:"error_code"`
}

// checkout posts a POS sale that must be created.
func checkout(t *testing.T, b *browser, body map[string]any) posSaleOut {
	t.Helper()
	rec := post(b, "/pos/checkout", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout = %d %s", rec.Code, rec.Body)
	}
	return testkit.DecodeJSON[posSaleOut](t, rec)
}

// keyed adds a fresh Idempotency-Key to the headers of a posting.
func keyed(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		out = http.Header{}
	}
	out.Set("Idempotency-Key", ids.New().String())
	return out
}

// verifySalesLedgers runs every ledger verifier of a store and reports each mismatch.
func verifySalesLedgers(t *testing.T, tenant uuid.UUID) {
	t.Helper()
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		for name, verify := range map[string]func(context.Context, db.Querier) ([]string, error){
			"sales": sales.Verify, "procurement": procurement.Verify, "inventory": inventory.Verify, "debt": debt.Verify,
			"cash": cash.Verify,
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

// Every subtest builds the documents it works on, so any of them runs alone (go test -run).
func TestSalesOverHTTP(t *testing.T) {
	s, clk := newSalesStack(t)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	storeA, storeB := buildSalesStore(t, a), buildSalesStore(t, b)

	t.Run("a quote converted to an invoice is confirmed, reserved and delivered by a stock out", func(t *testing.T) {
		sku := storeA.newSku(t, a, "NUOC-SUOI", "12000")
		storeA.receive(t, a, sku, nil, "10", "8000")

		rec := a.do(http.MethodPost, v1+"/quotes", map[string]any{"customer_party_id": storeA.customer,
			"valid_until": "2026-03-31", "lines": []map[string]any{saleLine(sku, "4", nil)}})
		expect(t, rec, http.StatusCreated, sales.QuoteDraft)
		quote := testkit.DecodeJSON[quoteOut](t, rec)
		if quote.TotalAmount != "48000" {
			t.Fatalf("quote = %+v, want 4 x 12000", quote)
		}
		etag := rec.Header().Get("ETag")
		for _, verb := range []string{"send", "accept"} {
			rec = a.doWith(http.MethodPost, v1+"/quotes/"+quote.ID.String()+":"+verb, nil, http.Header{"If-Match": {etag}})
			expect(t, rec, http.StatusOK, "")
			etag = rec.Header().Get("ETag")
		}
		rec = a.doWith(http.MethodPost, v1+"/quotes/"+quote.ID.String()+":convert", nil, http.Header{"If-Match": {etag}})
		expect(t, rec, http.StatusCreated, sales.InvoiceDraft)
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		if inv.QuoteID == nil || *inv.QuoteID != quote.ID || inv.TotalAmount != "48000" {
			t.Fatalf("converted invoice = %+v", inv)
		}
		if got := send[quoteOut](t, a, http.MethodGet, "/quotes/"+quote.ID.String(), nil); got.Status != sales.QuoteConverted ||
			got.ConvertedInvoiceID == nil || *got.ConvertedInvoiceID != inv.ID {
			t.Errorf("converted quote = %+v", got)
		}

		path := v1 + "/invoices/" + inv.ID.String()
		expect(t, a.doWith(http.MethodPost, path+":confirm", nil, keyed(nil)), http.StatusPreconditionRequired, "")
		rec = a.doWith(http.MethodPost, path+":confirm", nil, keyed(http.Header{"If-Match": {rec.Header().Get("ETag")}}))
		expect(t, rec, http.StatusOK, sales.InvoiceConfirmed)
		if got := testkit.DecodeJSON[invoiceOut](t, rec); got.Lines[0].ReservedQty != "4" {
			t.Fatalf("confirmed invoice = %+v, want 4 reserved", got)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "10" {
			t.Errorf("stock after the reservation = %+v, want 10 on hand", st)
		}

		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec = a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{}, key)
		expect(t, rec, http.StatusCreated, "")
		out := testkit.DecodeJSON[stockOutOut](t, rec)
		doc := out.Document
		if !strings.HasPrefix(out.DocNo, "PX") || doc == nil || !strings.HasPrefix(doc.DocNo, "BH") ||
			doc.Source != sales.SourceStockOut || doc.TotalAmount != "48000" || doc.CogsAmount != "32000" ||
			doc.Payment.Open != "48000" {
			t.Fatalf("stock out = %+v %+v", out, doc)
		}
		moves := countRows(t, storeA.tenant, `SELECT count(*) FROM stock_moves`)
		again := a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{}, key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[withID](t, again).ID != out.ID {
			t.Fatalf("replay = %s %s", again.Header(), again.Body)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM stock_moves`); n != moves {
			t.Errorf("stock moves after the replay = %d, want %d", n, moves)
		}
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "6" || st.AvgCost.String() != "8000" {
			t.Errorf("stock after the stock out = %+v, want 6 at 8000", st)
		}
		got := send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
		if got.Status != sales.InvoiceCompleted || got.DeliveredAmount != "48000" || got.Lines[0].DeliveredQty != "4" ||
			got.Lines[0].ReservedQty != "0" {
			t.Errorf("delivered invoice = %+v", got)
		}
		expect(t, post(a, "/invoices/"+inv.ID.String()+"/stock-outs", map[string]any{}), http.StatusConflict, "")
	})

	t.Run("a POS sale takes the only color left and replays under its key", func(t *testing.T) {
		shirt := storeA.newSku(t, a, "AO-THUN", "150000")
		red, purple := storeA.colors["do"], storeA.colors["tim"]
		storeA.receive(t, a, shirt, &red, "3", "90000")
		storeA.receive(t, a, shirt, &purple, "2", "90000")

		expect(t, post(a, "/pos/checkout", storeA.cashSale("150000", saleLine(shirt, "1", nil))),
			http.StatusUnprocessableEntity, sales.CodeColorRequired)

		body := storeA.cashSale("300000", saleLine(shirt, "2", &purple))
		body["cash_tendered_amount"] = "500000"
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec := a.doWith(http.MethodPost, v1+"/pos/checkout", body, key)
		expect(t, rec, http.StatusCreated, "")
		sale := testkit.DecodeJSON[posSaleOut](t, rec)
		doc := sale.Document
		if !strings.HasPrefix(doc.DocNo, "HD") || doc.Source != sales.SourcePOS || doc.TotalAmount != "300000" ||
			doc.CashTendered != "500000" || doc.Change != "200000" || !strings.HasPrefix(sale.Shift.DocNo, "CA") ||
			doc.ShiftID == nil || *doc.ShiftID != sale.Shift.ID || len(sale.Payments) != 1 ||
			!strings.HasPrefix(sale.Payments[0].DocNo, "PT") || sale.Payments[0].Amount != "300000" {
			t.Fatalf("POS sale = %+v", sale)
		}
		documents := countRows(t, storeA.tenant, `SELECT count(*) FROM sales_documents`)
		again := a.doWith(http.MethodPost, v1+"/pos/checkout", body, key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[posSaleOut](t, again).Document.ID != doc.ID {
			t.Fatalf("replay = %s %s", again.Header(), again.Body)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM sales_documents`); n != documents {
			t.Errorf("sales documents after the replay = %d, want %d", n, documents)
		}

		// Purple is sold out: a line without a color takes red, the only color left.
		picked := checkout(t, a, storeA.cashSale("150000", saleLine(shirt, "1", nil)))
		if c := picked.Document.Lines[0].ColorID; c == nil || *c != red {
			t.Errorf("picked color = %v, want red %s", c, red)
		}
		if picked.Shift.ID != sale.Shift.ID {
			t.Errorf("shift = %s, want the day's shift %s", picked.Shift.ID, sale.Shift.ID)
		}
		if st := stockOf(t, storeA.tenant, shirt); st.OnHand.String() != "2" {
			t.Errorf("stock = %+v, want 2 on hand", st)
		}
		expect(t, post(a, "/pos/checkout", storeA.cashSale("150000", saleLine(shirt, "1", &purple))),
			http.StatusConflict, sales.CodeInsufficientStock)
		expect(t, post(a, "/pos/checkout", storeA.cashSale("100000", saleLine(shirt, "1", &red))),
			http.StatusUnprocessableEntity, sales.CodeWalkInPaysInFull)
	})

	t.Run("a return takes back at most what was sold of a color and refunds a walk-in customer", func(t *testing.T) {
		shirt := storeA.newSku(t, a, "AO-SOMI", "100000")
		red, purple := storeA.colors["do"], storeA.colors["tim"]
		storeA.receive(t, a, shirt, &red, "5", "60000")
		storeA.receive(t, a, shirt, &purple, "5", "60000")
		sale := checkout(t, a, storeA.cashSale("500000", saleLine(shirt, "2", &red), saleLine(shirt, "3", &purple)))
		redLine := sale.Document.Lines[0]
		if redLine.ColorID == nil || *redLine.ColorID != red || redLine.BaseQty != "2" {
			t.Fatalf("red line = %+v", redLine)
		}
		ret := func(qty string, refund bool) map[string]any {
			body := map[string]any{"sales_document_id": sale.Document.ID, "reason_id": storeA.returnReason,
				"lines": []map[string]any{{"sales_document_line_id": redLine.ID, "qty": qty}}}
			if refund {
				body["refund_payment_method_id"] = storeA.cashMethod
			}
			return body
		}
		returns := countRows(t, storeA.tenant, `SELECT count(*) FROM sales_returns`)
		expect(t, post(a, "/sales-returns", ret("3", true)), http.StatusConflict, sales.CodeReturnExceedsSold)
		expect(t, post(a, "/sales-returns", ret("1", false)), http.StatusUnprocessableEntity, sales.CodeRefundRequired)
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM sales_returns`); n != returns {
			t.Errorf("sales returns = %d, want %d: a refused return is not kept", n, returns)
		}

		rec := post(a, "/sales-returns", ret("1", true))
		expect(t, rec, http.StatusCreated, "")
		posted := testkit.DecodeJSON[struct {
			Return salesReturnOut   `json:"sales_return"`
			Refund *salesPaymentOut `json:"refund"`
		}](t, rec)
		th := posted.Return
		if !strings.HasPrefix(th.DocNo, "TH") || th.TotalAmount != "100000" || len(th.Lines) != 1 || th.Lines[0].ColorID == nil ||
			*th.Lines[0].ColorID != red || th.Lines[0].UnitCost != "60000" || th.Lines[0].RestockTo != sales.RestockSellable {
			t.Fatalf("return = %+v", th)
		}
		if posted.Refund == nil || !strings.HasPrefix(posted.Refund.DocNo, "PC") || posted.Refund.Amount != "100000" {
			t.Errorf("refund = %+v", posted.Refund)
		}
		if st := stockOf(t, storeA.tenant, shirt); st.OnHand.String() != "6" {
			t.Errorf("stock = %+v, want 10 - 5 + 1", st)
		}
		doc := send[salesDocOut](t, a, http.MethodGet, "/sales-documents/"+sale.Document.ID.String(), nil)
		if doc.Status != sales.DocPartiallyReversed || doc.Lines[0].ReturnedQty != "1" {
			t.Errorf("sales document after the return = %+v", doc)
		}
		for _, l := range send[dataOf[struct {
			LineID   uuid.UUID `json:"sales_document_line_id"`
			Sold     string    `json:"sold_base_qty"`
			Returned string    `json:"returned_base_qty"`
		}]](t, a, http.MethodGet, "/sales-documents/"+sale.Document.ID.String()+"/returnable-lines", nil).Data {
			if l.LineID == redLine.ID && (l.Sold != "2" || l.Returned != "1") {
				t.Errorf("returnable red line = %+v", l)
			}
		}
		expect(t, post(a, "/sales-returns", ret("2", true)), http.StatusConflict, sales.CodeReturnExceedsSold)

		// The last red shirt comes back; a retry of that return finds nothing left to return yet
		// still gets the posted return, while a new return of it is refused.
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec = a.doWith(http.MethodPost, v1+"/sales-returns", ret("1", true), key)
		expect(t, rec, http.StatusCreated, "")
		last := testkit.DecodeJSON[struct {
			Return salesReturnOut `json:"sales_return"`
		}](t, rec).Return
		again := a.doWith(http.MethodPost, v1+"/sales-returns", ret("1", true), key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[struct {
			Return salesReturnOut `json:"sales_return"`
		}](t, again).Return.ID != last.ID {
			t.Fatalf("replayed return = %s %s", again.Header(), again.Body)
		}
		expect(t, post(a, "/sales-returns", ret("1", true)), http.StatusConflict, sales.CodeReturnExceedsSold)
		if st := stockOf(t, storeA.tenant, shirt); st.OnHand.String() != "7" {
			t.Errorf("stock = %+v, want 7 after both returns", st)
		}
	})

	t.Run("an exchange of 500000 for 650000 has the customer pay 150000 once", func(t *testing.T) {
		small, large := storeA.newSku(t, a, "GIAY-38", "250000"), storeA.newSku(t, a, "GIAY-39", "650000")
		storeA.receive(t, a, small, nil, "5", "150000")
		storeA.receive(t, a, large, nil, "3", "400000")
		sale := checkout(t, a, storeA.cashSale("500000", saleLine(small, "2", nil)))
		body := map[string]any{
			"original_sales_document_id": sale.Document.ID, "reason_id": storeA.exchangeReason,
			"return_lines":      []map[string]any{{"sales_document_line_id": sale.Document.Lines[0].ID, "qty": "2"}},
			"lines":             []map[string]any{saleLine(large, "1", nil)},
			"payment_method_id": storeA.cashMethod,
		}
		preview := send[struct {
			exchangeSplitOut
			Message string `json:"message"`
		}](t, a, http.MethodPost, "/exchanges:preview", body)
		if preview.Returned != "500000" || preview.Taken != "650000" || preview.CustomerPays != "150000" ||
			preview.StoreRefunds != "0" || preview.Message != "Khách trả thêm 150 000" {
			t.Fatalf("preview = %+v", preview)
		}

		stale := maps.Clone(body)
		stale["expected_customer_pays_amount"] = "100000"
		expect(t, post(a, "/exchanges", stale), http.StatusConflict, sales.CodeExchangeChanged)

		body["expected_customer_pays_amount"] = "150000"
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec := a.doWith(http.MethodPost, v1+"/exchanges", body, key)
		expect(t, rec, http.StatusCreated, "")
		dh := testkit.DecodeJSON[exchangePostedOut](t, rec)
		x := dh.Exchange
		if !strings.HasPrefix(x.DocNo, "DH") || x.CustomerPays != "150000" || x.Returned != "500000" || x.Taken != "650000" ||
			x.DebtReduction != "0" || x.StoreRefunds != "0" {
			t.Fatalf("exchange = %+v", x)
		}
		if !strings.HasPrefix(dh.Return.DocNo, "TH") || dh.Return.ID != x.SalesReturnID || dh.Return.ExchangeID == nil ||
			*dh.Return.ExchangeID != x.ID || dh.Return.DebtReduction != "500000" || dh.Return.RefundDue != "0" {
			t.Errorf("exchange return = %+v", dh.Return)
		}
		if !strings.HasPrefix(dh.Document.DocNo, "BH") || dh.Document.ID != x.NewDocumentID ||
			dh.Document.Source != sales.SourceExchange || dh.Document.TotalAmount != "650000" {
			t.Errorf("exchange sale = %+v", dh.Document)
		}
		if dh.Payment == nil || !strings.HasPrefix(dh.Payment.DocNo, "PT") || dh.Payment.Amount != "150000" {
			t.Errorf("exchange payment = %+v", dh.Payment)
		}

		counts := func() []int {
			return []int{
				countRows(t, storeA.tenant, `SELECT count(*) FROM exchanges`),
				countRows(t, storeA.tenant, `SELECT count(*) FROM sales_returns`),
				countRows(t, storeA.tenant, `SELECT count(*) FROM sales_documents`),
				countRows(t, storeA.tenant, `SELECT count(*) FROM payments`),
				countRows(t, storeA.tenant, `SELECT count(*) FROM stock_moves`),
			}
		}
		before := counts()
		again := a.doWith(http.MethodPost, v1+"/exchanges", body, key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[exchangePostedOut](t, again).Exchange.ID != x.ID {
			t.Fatalf("replay = %s %s", again.Header(), again.Body)
		}
		if after := counts(); !slices.Equal(after, before) {
			t.Errorf("rows after the replay = %v, want %v", after, before)
		}
		if st := stockOf(t, storeA.tenant, small); st.OnHand.String() != "5" {
			t.Errorf("returned SKU stock = %+v, want 5", st)
		}
		if st := stockOf(t, storeA.tenant, large); st.OnHand.String() != "2" {
			t.Errorf("taken SKU stock = %+v, want 2", st)
		}
		got := send[struct {
			ID    uuid.UUID `json:"id"`
			DocNo string    `json:"doc_no"`
		}](t, a, http.MethodGet, "/exchanges/"+x.ID.String(), nil)
		if got.ID != x.ID || got.DocNo != x.DocNo {
			t.Errorf("exchange read back = %+v", got)
		}
		printed := send[printOut](t, a, http.MethodGet, "/print/exchange/"+x.ID.String(), nil)
		if printed.Label != sales.PrintLabel || printed.DocNo != x.DocNo || len(printed.Returned) != 1 || len(printed.Related) != 3 {
			t.Errorf("printed exchange = %+v", printed)
		}
	})

	t.Run("reversing a customer's POS sale books the stock back and keeps the money as an advance", func(t *testing.T) {
		cake := storeA.newSku(t, a, "BANH-BO", "20000")
		storeA.receive(t, a, cake, nil, "10", "12000")
		body := storeA.cashSale("60000", saleLine(cake, "3", nil))
		body["customer_party_id"] = storeA.customer
		sale := checkout(t, a, body)
		if sale.Document.Payment.Open != "0" {
			t.Fatalf("customer sale = %+v", sale.Document)
		}
		walkIn := checkout(t, a, storeA.cashSale("20000", saleLine(cake, "1", nil)))
		if st := stockOf(t, storeA.tenant, cake); st.OnHand.String() != "6" {
			t.Fatalf("stock after the sales = %+v", st)
		}

		confirmPassword(t, a)
		reverse := func(id uuid.UUID) *httptest.ResponseRecorder {
			return post(a, "/sales-documents/"+id.String()+":reverse", map[string]any{"reason": "Bán nhầm khách"})
		}
		expect(t, reverse(walkIn.Document.ID), http.StatusConflict, sales.CodeWalkInReverse)
		rec := reverse(sale.Document.ID)
		expect(t, rec, http.StatusCreated, "")
		out := testkit.DecodeJSON[struct {
			Document      salesDocOut `json:"sales_document"`
			ReversalDocNo string      `json:"reversal_doc_no"`
			Advance       string      `json:"advance_amount"`
		}](t, rec)
		if out.Document.Status != sales.DocReversed || !strings.HasPrefix(out.ReversalDocNo, "HD") || out.Advance != "60000" {
			t.Fatalf("reversal = %+v", out)
		}
		if st := stockOf(t, storeA.tenant, cake); st.OnHand.String() != "9" || st.AvgCost.String() != "12000" {
			t.Errorf("stock after the reversal = %+v, want 9 at 12000", st)
		}
		expect(t, reverse(sale.Document.ID), http.StatusConflict, sales.CodeDocumentReversed)
	})

	t.Run("an offline batch sent twice posts once and parks what needs the server", func(t *testing.T) {
		rec := a.do(http.MethodGet, v1+"/tenant/settings", nil)
		expect(t, rec, http.StatusOK, "")
		expect(t, a.doWith(http.MethodPatch, v1+"/tenant/settings", map[string]any{"offline_pos_enabled": true,
			"offline_pos_max_amount": "5000000", "offline_pos_max_hours": 12}, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
			http.StatusOK, "")
		expect(t, a.do(http.MethodPost, v1+"/pos/offline-session", nil), http.StatusOK, "offline_until")

		candy := storeA.newSku(t, a, "KEO-DUA", "10000")
		storeA.receive(t, a, candy, nil, "20", "6000")
		at := clk.Now().Add(-30 * time.Minute)
		batch := map[string]any{"sales": []map[string]any{
			{"offline_client_id": "may-1-001", "created_at": at, "lines": []map[string]any{saleLine(candy, "2", nil)},
				"payments": []map[string]any{{"payment_method_id": storeA.cashMethod, "amount": "20000"}}},
			{"offline_client_id": "may-1-002", "created_at": at, "customer_party_id": storeA.customer,
				"lines": []map[string]any{saleLine(candy, "1", nil)}, "payments": []map[string]any{}},
			{"offline_client_id": "may-1-003", "created_at": at, "lines": []map[string]any{saleLine(candy, "1", nil)},
				"payments": []map[string]any{{"payment_method_id": storeA.transferMethod, "amount": "10000"}}},
		}}
		sync := func() []offlineItemOut {
			t.Helper()
			return send[struct {
				Items []offlineItemOut `json:"items"`
			}](t, a, http.MethodPost, "/pos/offline-sync", batch).Items
		}
		first := sync()
		if len(first) != 3 || first[0].Status != sales.SyncPosted || first[0].SalesDocumentID == nil {
			t.Fatalf("first sync = %+v", first)
		}
		for _, item := range first[1:] {
			if item.Status != sales.SyncParked || item.ErrorCode != sales.CodeOfflineNotAllowed || item.ExceptionID == nil {
				t.Errorf("credit or transfer sale = %+v, want parked as %s", item, sales.CodeOfflineNotAllowed)
			}
		}
		second := sync()
		if len(second) != 3 || second[0].Status != sales.SyncReplayed || *second[0].SalesDocumentID != *first[0].SalesDocumentID {
			t.Fatalf("second sync = %+v", second)
		}
		for i, item := range second[1:] {
			if item.Status != sales.SyncParked || item.ExceptionID == nil || *item.ExceptionID != *first[i+1].ExceptionID {
				t.Errorf("parked sale sent again = %+v, want %+v", item, first[i+1])
			}
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM sales_documents WHERE offline_client_id IS NOT NULL`); n != 1 {
			t.Errorf("offline sales documents = %d, want 1", n)
		}
		if n := countRows(t, storeA.tenant, `SELECT count(*) FROM pos_sync_exceptions WHERE status = 'open'`); n != 2 {
			t.Errorf("open sync exceptions = %d, want 2", n)
		}
		doc := send[salesDocOut](t, a, http.MethodGet, "/sales-documents/"+first[0].SalesDocumentID.String(), nil)
		if doc.Source != sales.SourcePOSOffline || doc.TotalAmount != "20000" {
			t.Errorf("offline sale = %+v", doc)
		}
		if st := stockOf(t, storeA.tenant, candy); st.OnHand.String() != "18" {
			t.Errorf("stock = %+v, want 18", st)
		}
	})

	t.Run("ids of another store are refused", func(t *testing.T) {
		sku := storeA.newSku(t, a, "LA-LUNG", "5000")
		storeA.receive(t, a, sku, nil, "2", "3000")
		sale := checkout(t, a, storeA.cashSale("5000", saleLine(sku, "1", nil)))
		expect(t, b.do(http.MethodGet, v1+"/sales-documents/"+sale.Document.ID.String(), nil), http.StatusNotFound, "")
		expect(t, post(b, "/sales-returns", map[string]any{"sales_document_id": sale.Document.ID, "reason_id": storeB.returnReason,
			"refund_payment_method_id": storeB.cashMethod,
			"lines":                    []map[string]any{{"sales_document_line_id": sale.Document.Lines[0].ID, "qty": "1"}}}),
			http.StatusNotFound, "")
		expectRejected(t, "checkout of another store's SKU", post(b, "/pos/checkout", storeB.cashSale("5000", saleLine(sku, "1", nil))))
		if st := stockOf(t, storeA.tenant, sku); st.OnHand.String() != "1" {
			t.Errorf("stock = %+v, want 1", st)
		}
	})

	t.Run("every ledger verifies after the postings above", func(t *testing.T) {
		for _, tenant := range []uuid.UUID{storeA.tenant, storeB.tenant} {
			verifySalesLedgers(t, tenant)
		}
	})
}

func TestSalesPostingInLockedPeriodIsRefused(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "KHOA-SO", "10000")
	st.receive(t, a, sku, nil, "5", "6000")
	expect(t, post(a, "/period-locks", map[string]any{"locked_through": "2026-03-15"}), http.StatusCreated, "")

	documents := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`)
	expect(t, post(a, "/pos/checkout", st.cashSale("10000", saleLine(sku, "1", nil))), http.StatusLocked, "period_locked")
	if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != documents {
		t.Errorf("sales documents = %d, want %d", n, documents)
	}
	if got := stockOf(t, st.tenant, sku); got.OnHand.String() != "5" {
		t.Errorf("stock = %+v, want 5", got)
	}
	assertNoShiftOpened(t, st.tenant)
	verifySalesLedgers(t, st.tenant)
}

// assertNoShiftOpened checks that a refused posting left no shift behind: no row, no CA number
// taken and no audit of an opening.
func assertNoShiftOpened(t *testing.T, tenant uuid.UUID) {
	t.Helper()
	for query, what := range map[string]string{
		`SELECT count(*) FROM shifts`: "shifts",
		`SELECT count(*) FROM doc_sequences WHERE prefix = 'CA' AND next_value > 1`: "CA numbers taken",
		`SELECT count(*) FROM audit_events WHERE action = 'shift.open'`:             "shift openings audited",
	} {
		if n := countRows(t, tenant, query); n != 0 {
			t.Errorf("%s = %d, want 0", what, n)
		}
	}
}

// A store turned read-only still opens the POS and previews an exchange, but no checkout or
// offline batch posts: nothing is left behind, not even the day's shift.
func TestSalesPostingInReadOnlyStoreIsRefused(t *testing.T) {
	s, clk := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	rec := a.do(http.MethodGet, v1+"/tenant/settings", nil)
	expect(t, rec, http.StatusOK, "")
	expect(t, a.doWith(http.MethodPatch, v1+"/tenant/settings", map[string]any{"offline_pos_enabled": true,
		"offline_pos_max_amount": "5000000", "offline_pos_max_hours": 12}, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
		http.StatusOK, "")
	expect(t, a.do(http.MethodPost, v1+"/pos/offline-session", nil), http.StatusOK, "offline_until")
	sku := st.newSku(t, a, "CHI-DOC", "10000")
	unpriced := send[productRef](t, a, http.MethodPost, "/products", map[string]any{
		"type": "goods", "name": "Hàng chưa có giá", "sku": map[string]any{"code": "CHUA-GIA", "base_unit_id": st.units["cái"]},
	}).Skus[0].ID
	st.receive(t, a, sku, nil, "5", "6000")
	invoice := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(sku, "1", nil)}})
	expect(t, invoice, http.StatusCreated, "")
	rec = a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(sku, "2", nil)}})
	expect(t, rec, http.StatusCreated, "")
	delivered := testkit.DecodeJSON[invoiceOut](t, rec)
	expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+delivered.ID.String()+":confirm", nil, keyed(ifMatchOf(delivered.Version))),
		http.StatusOK, "")
	rec = a.doWith(http.MethodPost, v1+"/invoices/"+delivered.ID.String()+"/stock-outs", map[string]any{}, keyed(nil))
	expect(t, rec, http.StatusCreated, "")
	sold := testkit.DecodeJSON[stockOutOut](t, rec).Document
	if sold == nil {
		t.Fatal("stock out without its sales document")
	}
	if _, err := testkit.SuperDB(t).ExecContext(context.Background(),
		`UPDATE tally.tenants SET status = 'read_only' WHERE id = $1`, st.tenant); err != nil {
		t.Fatal(err)
	}
	documents := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`)

	t.Run("the POS still opens and shows no price for a SKU the retail list lacks", func(t *testing.T) {
		boot := send[struct {
			Tiles []struct {
				SkuID uuid.UUID `json:"sku_id"`
				Price *string   `json:"retail_price"`
			} `json:"tiles"`
		}](t, a, http.MethodGet, "/pos/bootstrap", nil)
		prices := map[uuid.UUID]*string{}
		for _, tile := range boot.Tiles {
			prices[tile.SkuID] = tile.Price
		}
		if p, ok := prices[sku]; !ok || p == nil || *p != "10000" {
			t.Errorf("priced tile = %v, want 10000", p)
		}
		if p, ok := prices[unpriced]; !ok || p != nil {
			t.Errorf("unpriced tile = %v %v, want null", ok, p)
		}
	})
	t.Run("checkout is refused and opens no shift", func(t *testing.T) {
		expect(t, post(a, "/pos/checkout", st.cashSale("10000", saleLine(sku, "1", nil))), http.StatusForbidden,
			"subscription_read_only")
		assertNoShiftOpened(t, st.tenant)
	})
	t.Run("an exchange is previewed but not posted", func(t *testing.T) {
		body := map[string]any{"original_sales_document_id": sold.ID, "reason_id": st.exchangeReason,
			"return_lines":      []map[string]any{{"sales_document_line_id": sold.Lines[0].ID, "qty": "1"}},
			"lines":             []map[string]any{saleLine(sku, "1", nil)},
			"payment_method_id": st.cashMethod}
		if p := send[exchangeSplitOut](t, a, http.MethodPost, "/exchanges:preview", body); p.Returned != "10000" ||
			p.Taken != "10000" {
			t.Errorf("preview = %+v", p)
		}
		expect(t, post(a, "/exchanges", body), http.StatusForbidden, "subscription_read_only")
	})
	t.Run("confirming an invoice is refused", func(t *testing.T) {
		inv := testkit.DecodeJSON[invoiceOut](t, invoice)
		expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version))),
			http.StatusForbidden, "subscription_read_only")
	})
	t.Run("an offline batch is refused whole and parks nothing", func(t *testing.T) {
		at := clk.Now().Add(-30 * time.Minute)
		expect(t, post(a, "/pos/offline-sync", map[string]any{"sales": []map[string]any{
			{"offline_client_id": "chi-doc-001", "created_at": at, "lines": []map[string]any{saleLine(sku, "1", nil)},
				"payments": []map[string]any{{"payment_method_id": st.cashMethod, "amount": "10000"}}},
			{"offline_client_id": "chi-doc-002", "created_at": at, "customer_party_id": st.customer,
				"lines": []map[string]any{saleLine(sku, "1", nil)}, "payments": []map[string]any{}},
		}}), http.StatusForbidden, "subscription_read_only")
		if n := countRows(t, st.tenant, `SELECT count(*) FROM pos_sync_exceptions`); n != 0 {
			t.Errorf("sync exceptions = %d, want 0", n)
		}
		assertNoShiftOpened(t, st.tenant)
	})
	if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != documents {
		t.Errorf("sales documents = %d, want %d", n, documents)
	}
	if got := stockOf(t, st.tenant, sku); got.OnHand.String() != "3" {
		t.Errorf("stock = %+v, want the 3 left after the delivered invoice", got)
	}
	verifySalesLedgers(t, st.tenant)
}

// A gift takes stock the way a sold line does: twelve tills selling the item a promotion gives a
// cup with share the cups not held for a confirmed invoice, and the invoice still gets its own.
func TestConcurrentGiftsNeverTakeReservedStock(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	item, cup := st.newSku(t, a, "BANH-QUA", "30000"), st.newSku(t, a, "LY-SU", "15000")
	st.receive(t, a, item, nil, "40", "20000")
	st.receive(t, a, cup, nil, "10", "8000")
	expect(t, post(a, "/promotions", map[string]any{"name": "Mua bánh tặng ly", "kind": "gift", "value": "0",
		"valid_from": "2026-03-01", "valid_to": "2026-03-31", "items": []map[string]any{
			{"role": "eligible", "sku_id": item, "qty": "1"}, {"role": "gift", "sku_id": cup, "qty": "1"}}}),
		http.StatusCreated, "")
	rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(cup, "4", nil)}})
	expect(t, rec, http.StatusCreated, "")
	inv := testkit.DecodeJSON[invoiceOut](t, rec)
	path := v1 + "/invoices/" + inv.ID.String()
	rec = a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version)))
	expect(t, rec, http.StatusOK, "")
	if got := testkit.DecodeJSON[invoiceOut](t, rec); got.Lines[0].ReservedQty != "4" {
		t.Fatalf("confirmed invoice = %+v, want 4 cups reserved", got)
	}

	first := checkout(t, a, st.cashSale("30000", saleLine(item, "1", nil)))
	if !slices.ContainsFunc(first.Document.Lines, func(l salesLineOut) bool {
		return l.Kind == sales.KindGift && l.SkuID != nil && *l.SkuID == cup && l.BaseQty == "1"
	}) {
		t.Fatalf("sale lines = %+v, want a gift cup", first.Document.Lines)
	}

	const tills = 12
	recs := make([]*httptest.ResponseRecorder, tills)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range tills {
		till := tillOf(a)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recs[i] = till.doWith(http.MethodPost, v1+"/pos/checkout", st.cashSale("30000", saleLine(item, "1", nil)),
				http.Header{"Idempotency-Key": {ids.New().String()}})
		}()
	}
	close(start)
	wg.Wait()

	sold := 0
	for _, rec := range recs {
		switch rec.Code {
		case http.StatusCreated:
			sold++
		case http.StatusConflict:
			if !strings.Contains(rec.Body.String(), `"`+sales.CodeInsufficientStock+`"`) {
				t.Errorf("refused checkout = %s, want %s", rec.Body, sales.CodeInsufficientStock)
			}
		default:
			t.Errorf("checkout = %d %s", rec.Code, rec.Body)
		}
	}
	if sold != 5 {
		t.Errorf("successful checkouts = %d, want the 5 cups left after the reservation and the first sale", sold)
	}
	var sellable, reserved money.Qty
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT sellable, reserved FROM sku_stock WHERE sku_id = $1`, cup).
			Scan(&sellable, &reserved)
	})
	if sellable.String() != "4" || reserved.String() != "4" {
		t.Errorf("cups sellable %s reserved %s, want 4 and 4", sellable, reserved)
	}
	expect(t, a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{}, keyed(nil)), http.StatusCreated, "")
	if got := stockOf(t, st.tenant, cup); !got.OnHand.IsZero() {
		t.Errorf("cups after the invoice is delivered = %+v, want 0", got)
	}
	verifySalesLedgers(t, st.tenant)
}

// holdLock runs query as superuser in a transaction left open until release, so a test can line
// requests up behind the lock it takes.
func holdLock(t *testing.T, query string, args ...any) (release func()) {
	t.Helper()
	tx, err := testkit.SuperDB(t).BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(query, args...); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			if err := tx.Commit(); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(release)
	return release
}

// waitForLockWaiters waits until n sessions of the database wait on a lock.
func waitForLockWaiters(t *testing.T, n int) {
	t.Helper()
	super := testkit.SuperDB(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := super.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions waiting on a lock = %d, want %d", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tillOf is another till on b's session, with its own cookie jar.
func tillOf(b *browser) *browser {
	return &browser{t: b.t, h: b.h, host: b.host, cookies: maps.Clone(b.cookies), csrf: b.csrf}
}

// The first two sales of a day both plan to open the shift. They queue on the CA counter; the
// second finds the shift the first opened, gives its CA number back and posts in that shift.
func TestFirstSalesOfTheDayOpenOneShift(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "MO-CA", "10000")
	st.receive(t, a, sku, nil, "5", "6000")
	release := holdLock(t, `SELECT 1 FROM tally.doc_sequences WHERE tenant_id = $1 AND prefix = 'CA' FOR UPDATE`, st.tenant)

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range recs {
		till := tillOf(a)
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = till.doWith(http.MethodPost, v1+"/pos/checkout", st.cashSale("10000", saleLine(sku, "1", nil)), keyed(nil))
		}()
	}
	waitForLockWaiters(t, 2)
	release()
	wg.Wait()

	var shifts []uuid.UUID
	for _, rec := range recs {
		expect(t, rec, http.StatusCreated, "")
		if rec.Code == http.StatusCreated {
			shifts = append(shifts, testkit.DecodeJSON[posSaleOut](t, rec).Shift.ID)
		}
	}
	if len(shifts) != 2 || shifts[0] != shifts[1] {
		t.Errorf("shifts of the sales = %v, want one shift", shifts)
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM shifts`:                                     1,
		`SELECT next_value - 1 FROM doc_sequences WHERE prefix = 'CA'`:    1,
		`SELECT count(*) FROM audit_events WHERE action = 'shift.open'`:   1,
		`SELECT count(*) FROM sales_documents WHERE shift_id IS NOT NULL`: 2,
	} {
		if n := countRows(t, st.tenant, query); n != want {
			t.Errorf("%s = %d, want %d", query, n, want)
		}
	}
	verifySalesLedgers(t, st.tenant)
}

// An offline sale posting and the same sale being parked (one upload made it in time, another
// too late) are lined up behind the sale's lock in either order: the sale ends up posted or
// parked, never both. A corrected sale is not posted for an exception whose sale already posted.
func TestOfflineSaleIsPostedOrParkedNeverBoth(t *testing.T) {
	s, clk := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	rec := a.do(http.MethodGet, v1+"/tenant/settings", nil)
	expect(t, rec, http.StatusOK, "")
	expect(t, a.doWith(http.MethodPatch, v1+"/tenant/settings", map[string]any{"offline_pos_enabled": true,
		"offline_pos_max_amount": "5000000", "offline_pos_max_hours": 12}, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
		http.StatusOK, "")
	expect(t, a.do(http.MethodPost, v1+"/pos/offline-session", nil), http.StatusOK, "offline_until")
	sku := st.newSku(t, a, "NUOC-MIA", "10000")
	st.receive(t, a, sku, nil, "20", "6000")

	type syncOut struct {
		Items []offlineItemOut `json:"items"`
	}
	offlineSale := func(clientID string, at time.Time) map[string]any {
		return map[string]any{"sales": []map[string]any{{"offline_client_id": clientID, "created_at": at,
			"lines":    []map[string]any{saleLine(sku, "1", nil)},
			"payments": []map[string]any{{"payment_method_id": st.cashMethod, "amount": "10000"}}}}}
	}
	inTime, tooLate := clk.Now().Add(-30*time.Minute), clk.Now().Add(-24*time.Hour)
	// race sends the uploads one after another, each once the one before waits on the sale's lock.
	race := func(t *testing.T, clientID string, uploads ...map[string]any) []offlineItemOut {
		t.Helper()
		release := holdLock(t, `SELECT pg_advisory_xact_lock(hashtextextended('offline_sale:' || $1::text || ':' || $2, 0))`,
			st.tenant, clientID)
		recs := make([]*httptest.ResponseRecorder, len(uploads))
		var wg sync.WaitGroup
		for i, body := range uploads {
			till := tillOf(a)
			wg.Add(1)
			go func() {
				defer wg.Done()
				recs[i] = till.doWith(http.MethodPost, v1+"/pos/offline-sync", body, nil)
			}()
			waitForLockWaiters(t, i+1)
		}
		release()
		wg.Wait()
		out := make([]offlineItemOut, len(recs))
		for i, rec := range recs {
			expect(t, rec, http.StatusOK, "")
			items := testkit.DecodeJSON[syncOut](t, rec).Items
			if len(items) != 1 {
				t.Fatalf("upload %d = %s", i, rec.Body)
			}
			out[i] = items[0]
		}
		return out
	}
	counts := func(clientID string) (docs, exceptions int) {
		return countRows(t, st.tenant, `SELECT count(*) FROM sales_documents WHERE offline_client_id = $1`, clientID),
			countRows(t, st.tenant, `SELECT count(*) FROM pos_sync_exceptions WHERE offline_client_id = $1`, clientID)
	}

	t.Run("a sale parked first is refused by the posting behind it", func(t *testing.T) {
		got := race(t, "may-2-001", offlineSale("may-2-001", tooLate), offlineSale("may-2-001", inTime))
		parked, posting := got[0], got[1]
		if parked.Status != sales.SyncParked || parked.ErrorCode != sales.CodeOfflineExpired || parked.ExceptionID == nil {
			t.Fatalf("late upload = %+v", parked)
		}
		if posting.Status != sales.SyncParked || posting.ExceptionID == nil || *posting.ExceptionID != *parked.ExceptionID ||
			posting.SalesDocumentID != nil {
			t.Errorf("upload in time = %+v, want the parked sale %v", posting, *parked.ExceptionID)
		}
		if docs, exceptions := counts("may-2-001"); docs != 0 || exceptions != 1 {
			t.Errorf("documents %d exceptions %d, want 0 and 1", docs, exceptions)
		}
	})

	t.Run("a sale posted first is replayed to the park behind it", func(t *testing.T) {
		got := race(t, "may-2-002", offlineSale("may-2-002", inTime), offlineSale("may-2-002", tooLate))
		posted, parking := got[0], got[1]
		if posted.Status != sales.SyncPosted || posted.SalesDocumentID == nil {
			t.Fatalf("upload in time = %+v", posted)
		}
		if parking.Status != sales.SyncReplayed || parking.SalesDocumentID == nil ||
			*parking.SalesDocumentID != *posted.SalesDocumentID || parking.ExceptionID != nil {
			t.Errorf("late upload = %+v, want the posted sale %v", parking, *posted.SalesDocumentID)
		}
		if docs, exceptions := counts("may-2-002"); docs != 1 || exceptions != 0 {
			t.Errorf("documents %d exceptions %d, want 1 and 0", docs, exceptions)
		}
	})

	t.Run("an exception whose sale already posted is not resolved by posting it again", func(t *testing.T) {
		items := send[syncOut](t, a, http.MethodPost, "/pos/offline-sync", offlineSale("may-2-003", tooLate)).Items
		if len(items) != 1 || items[0].ExceptionID == nil {
			t.Fatalf("late upload = %+v", items)
		}
		exception := *items[0].ExceptionID
		// A sale both posted and parked, as a park racing a posting could leave it before the
		// sale's lock: the exception is moved onto the client id posted above.
		if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.pos_sync_exceptions SET offline_client_id = 'may-2-002' WHERE id = $1`,
			exception); err != nil {
			t.Fatal(err)
		}
		documents := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`)
		expect(t, post(a, "/pos/sync-exceptions/"+exception.String()+":resolve", map[string]any{
			"lines":    []map[string]any{saleLine(sku, "1", nil)},
			"payments": []map[string]any{{"payment_method_id": st.cashMethod, "amount": "10000"}}}),
			http.StatusConflict, sales.CodeOfflineSalePosted)
		if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != documents {
			t.Errorf("sales documents = %d, want %d", n, documents)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM pos_sync_exceptions WHERE id = $1 AND status = 'open'`,
			exception); n != 1 {
			t.Errorf("open exception = %d, want it left open", n)
		}
	})
	if got := stockOf(t, st.tenant, sku); got.OnHand.String() != "19" {
		t.Errorf("stock = %+v, want the one offline sale posted", got)
	}
}

// Twenty tills sell the last ten units at once: the SKU locks let exactly ten through, the rest
// find the stock gone, and every ledger still agrees.
func TestConcurrentCheckoutsNeverOversell(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "HANG-HIEM", "50000")
	st.receive(t, a, sku, nil, "10", "30000")

	const tills = 20
	recs := make([]*httptest.ResponseRecorder, tills)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range tills {
		till := tillOf(a)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recs[i] = till.doWith(http.MethodPost, v1+"/pos/checkout", st.cashSale("50000", saleLine(sku, "1", nil)),
				http.Header{"Idempotency-Key": {ids.New().String()}})
		}()
	}
	close(start)
	wg.Wait()

	sold := 0
	for _, rec := range recs {
		switch rec.Code {
		case http.StatusCreated:
			sold++
		case http.StatusConflict:
			if !strings.Contains(rec.Body.String(), `"`+sales.CodeInsufficientStock+`"`) {
				t.Errorf("refused checkout = %s, want %s", rec.Body, sales.CodeInsufficientStock)
			}
		default:
			t.Errorf("checkout = %d %s", rec.Code, rec.Body)
		}
	}
	if sold != 10 {
		t.Errorf("successful checkouts = %d, want 10", sold)
	}
	if got := stockOf(t, st.tenant, sku); !got.OnHand.IsZero() || got.OnHand.IsNegative() {
		t.Errorf("stock = %+v, want 0 on hand", got)
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != 10 {
		t.Errorf("sales documents = %d, want 10", n)
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM shifts`); n != 1 {
		t.Errorf("shifts = %d, want the one shift of the day", n)
	}
	if n := countRows(t, st.tenant, `SELECT next_value - 1 FROM doc_sequences WHERE prefix = 'CA'`); n != 1 {
		t.Errorf("CA numbers taken = %d, want 1", n)
	}
	verifySalesLedgers(t, st.tenant)
}

// The verifier catches what only a bug could leave behind: each row is corrupted as superuser,
// reported, then put back, and the store verifies clean again.
func TestSalesVerifyReportsTamperedRows(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	super := testkit.SuperDB(t)
	verify := func(t *testing.T) []string {
		t.Helper()
		var out []string
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			var err error
			out, err = sales.Verify(context.Background(), q)
			return err
		})
		return out
	}
	tamper := func(t *testing.T, id uuid.UUID, set, undo string, want ...string) {
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
	}

	sku := st.newSku(t, a, "KT-BAN", "10000")
	st.receive(t, a, sku, nil, "10", "6000")
	body := st.cashSale("40000", saleLine(sku, "4", nil))
	body["customer_party_id"] = st.customer
	sale := checkout(t, a, body)
	rec := post(a, "/sales-returns", map[string]any{"sales_document_id": sale.Document.ID, "reason_id": st.returnReason,
		"lines": []map[string]any{{"sales_document_line_id": sale.Document.Lines[0].ID, "qty": "1"}}})
	expect(t, rec, http.StatusCreated, "")
	th := testkit.DecodeJSON[struct {
		Return salesReturnOut `json:"sales_return"`
	}](t, rec).Return
	owing := st.cashSale("20000", saleLine(sku, "3", nil))
	owing["customer_party_id"] = st.customer
	owed := checkout(t, a, owing)
	rec = post(a, "/exchanges", map[string]any{"original_sales_document_id": owed.Document.ID, "reason_id": st.exchangeReason,
		"return_lines": []map[string]any{{"sales_document_line_id": owed.Document.Lines[0].ID, "qty": "2"}},
		"lines":        []map[string]any{saleLine(sku, "1", nil)}, "payment_method_id": st.cashMethod})
	expect(t, rec, http.StatusCreated, "")
	dh := testkit.DecodeJSON[exchangePostedOut](t, rec)
	if x := dh.Exchange; x.Returned != "20000" || x.Taken != "10000" || x.DebtReduction != "10000" || x.CustomerPays != "0" ||
		x.StoreRefunds != "0" || dh.Return.DebtReduction != "20000" || dh.Return.RefundDue != "0" {
		t.Fatalf("exchange against an unpaid sale = %+v, return %+v", x, dh.Return)
	}
	if got := verify(t); len(got) > 0 {
		t.Fatalf("verify before tampering = %v", got)
	}
	doc, line, exchange := sale.Document.ID, sale.Document.Lines[0].ID, dh.Exchange.ID

	t.Run("a sales document whose cost is not the sum of its lines", func(t *testing.T) {
		tamper(t, doc,
			`UPDATE tally.sales_documents SET cogs_amount = cogs_amount + 1000 WHERE id = $1`,
			`UPDATE tally.sales_documents SET cogs_amount = cogs_amount - 1000 WHERE id = $1`,
			"sales document cogs "+doc.String())
	})
	t.Run("a returned quantity no return took back", func(t *testing.T) {
		tamper(t, line,
			`UPDATE tally.sales_document_lines SET returned_base_qty = returned_base_qty + 1 WHERE id = $1`,
			`UPDATE tally.sales_document_lines SET returned_base_qty = returned_base_qty - 1 WHERE id = $1`,
			"sales document line returned "+line.String())
	})
	t.Run("a customer's sale without its receivable", func(t *testing.T) {
		tamper(t, doc,
			`UPDATE tally.obligations SET source_id = id WHERE source_type = 'sales_document' AND source_id = $1`,
			`UPDATE tally.obligations SET source_id = $1 WHERE source_type = 'sales_document' AND source_id = id`,
			"sales document receivable "+doc.String())
	})
	t.Run("a return whose total is not the sum of its lines", func(t *testing.T) {
		tamper(t, th.ID,
			`UPDATE tally.sales_returns SET total_amount = total_amount + 1000, debt_reduction_amount = debt_reduction_amount + 1000 WHERE id = $1`,
			`UPDATE tally.sales_returns SET total_amount = total_amount - 1000, debt_reduction_amount = debt_reduction_amount - 1000 WHERE id = $1`,
			"sales return total "+th.ID.String())
	})
	t.Run("an exchange that reduces more of the original debt than its amounts settle", func(t *testing.T) {
		tamper(t, exchange,
			`UPDATE tally.exchanges SET debt_reduction_amount = debt_reduction_amount + 1000 WHERE id = $1`,
			`UPDATE tally.exchanges SET debt_reduction_amount = debt_reduction_amount - 1000 WHERE id = $1`,
			"exchange settlement "+exchange.String())
	})
	t.Run("an exchange refunding what its return does not", func(t *testing.T) {
		tamper(t, exchange,
			`UPDATE tally.exchanges SET store_refunds_amount = store_refunds_amount + 1000 WHERE id = $1`,
			`UPDATE tally.exchanges SET store_refunds_amount = store_refunds_amount - 1000 WHERE id = $1`,
			"exchange amounts "+exchange.String(), "exchange settlement "+exchange.String())
	})
}

// The document paths around the counter: mix packs, quote expiry and copy, deposits on an
// invoice, revisions and backorders, saved carts, quarantined returns and the printed copies.
func TestSalesDocumentLifecycleOverHTTP(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	printOf := func(t *testing.T, docType string, id uuid.UUID, docNo string) printOut {
		t.Helper()
		out := send[printOut](t, a, http.MethodGet, "/print/"+docType+"/"+id.String(), nil)
		if out.Label != sales.PrintLabel || out.DocType != docType || out.DocNo != docNo {
			t.Errorf("printed %s = %+v, want %s", docType, out, docNo)
		}
		return out
	}

	// A can of each flavor sells at 10000; a pack holds 4 six-can parts.
	flavor := func(code string) uuid.UUID {
		sku := send[productRef](t, a, http.MethodPost, "/products", map[string]any{
			"type": "goods", "name": "Nước ngọt " + code, "sku": map[string]any{"code": code, "base_unit_id": st.units["hộp"]},
		}).Skus[0].ID
		send[map[string]any](t, a, http.MethodPost, "/skus/"+sku.String()+"/sale-price",
			map[string]any{"kind": "retail", "price": "10000", "source": "catalog"})
		st.receive(t, a, sku, nil, "60", "7000")
		return sku
	}
	cola, cam := flavor("NG-COLA"), flavor("NG-CAM")
	group := send[withID](t, a, http.MethodPost, "/mix-groups", map[string]any{
		"name": "Nước ngọt lon", "pack_unit_id": st.units["thùng"], "units_per_pack": 24, "part_unit_id": st.units["lốc"],
		"units_per_part": 6, "items": []map[string]any{
			{"sku_id": cola, "flavor_label": "Cola", "active": true}, {"sku_id": cam, "flavor_label": "Cam", "active": true},
		},
	}).ID
	pack := func(packs string, colaParts, camParts int) map[string]any {
		return map[string]any{"mix_group_id": group, "qty": packs, "parts": []map[string]any{
			{"sku_id": cola, "parts": colaParts}, {"sku_id": cam, "parts": camParts}}}
	}
	onHand := func(t *testing.T, sku uuid.UUID) string {
		t.Helper()
		return stockOf(t, st.tenant, sku).OnHand.String()
	}

	t.Run("a mix pack sells its flavors by the part", func(t *testing.T) {
		expect(t, post(a, "/pos/checkout", st.cashSale("300000", pack("1", 4, 1))), http.StatusUnprocessableEntity,
			sales.CodeMixPartsExceedPack)

		cola0, cam0 := stockOf(t, st.tenant, cola).OnHand, stockOf(t, st.tenant, cam).OnHand
		short := checkout(t, a, st.cashSale("180000", pack("1", 2, 1))).Document
		if short.TotalAmount != "180000" || len(short.Lines) != 3 || short.Lines[0].LineAmount != "180000" ||
			short.Lines[1].BaseQty != "12" || short.Lines[2].BaseQty != "6" {
			t.Fatalf("short mix pack sale = %+v, want 3 of 4 parts priced at 18 cans", short)
		}
		if got := onHand(t, cola); got != cola0.Sub(money.MustQty("12")).String() {
			t.Errorf("cola stock after the short pack = %s, want %s - 12", got, cola0)
		}
		if got := onHand(t, cam); got != cam0.Sub(money.MustQty("6")).String() {
			t.Errorf("cam stock after the short pack = %s, want %s - 6", got, cam0)
		}

		cola0, cam0 = stockOf(t, st.tenant, cola).OnHand, stockOf(t, st.tenant, cam).OnHand
		sale := checkout(t, a, st.cashSale("240000", pack("1", 3, 1)))
		doc := sale.Document
		if doc.TotalAmount != "240000" || len(doc.Lines) != 3 || doc.Lines[0].Kind != sales.KindMixPack ||
			doc.Lines[1].Kind != sales.KindMixFlavor || doc.Lines[1].BaseQty != "18" || doc.Lines[2].BaseQty != "6" ||
			doc.Lines[0].MixKey == nil || doc.Lines[2].MixKey == nil || *doc.Lines[2].MixKey != *doc.Lines[0].MixKey {
			t.Fatalf("mix pack sale = %+v", doc)
		}
		if got := onHand(t, cola); got != cola0.Sub(money.MustQty("18")).String() {
			t.Errorf("cola stock = %s, want %s - 18", got, cola0)
		}
		if got := onHand(t, cam); got != cam0.Sub(money.MustQty("6")).String() {
			t.Errorf("cam stock = %s, want %s - 6", got, cam0)
		}
		printed := printOf(t, sales.PrintSalesDocument, doc.ID, doc.DocNo)
		if len(printed.Groups) != 1 || printed.Groups[0].MixKey == nil || len(printed.Groups[0].Lines) != 3 {
			t.Errorf("printed mix pack = %+v, want one group of the pack and its flavors", printed.Groups)
		}
	})

	t.Run("a short mix pack is quoted, invoiced and delivered by the parts chosen", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/quotes", map[string]any{"customer_party_id": st.customer,
			"valid_until": "2026-03-31", "lines": []map[string]any{pack("2", 3, 2)}})
		expect(t, rec, http.StatusCreated, sales.QuoteDraft)
		quote := testkit.DecodeJSON[quoteOut](t, rec)
		if quote.TotalAmount != "300000" {
			t.Fatalf("quote = %+v, want 5 of 8 parts priced at 30 cans", quote)
		}
		etag := rec.Header().Get("ETag")
		for _, verb := range []string{"send", "accept"} {
			rec = a.doWith(http.MethodPost, v1+"/quotes/"+quote.ID.String()+":"+verb, nil, http.Header{"If-Match": {etag}})
			expect(t, rec, http.StatusOK, "")
			etag = rec.Header().Get("ETag")
		}
		rec = a.doWith(http.MethodPost, v1+"/quotes/"+quote.ID.String()+":convert", nil, http.Header{"If-Match": {etag}})
		expect(t, rec, http.StatusCreated, sales.InvoiceDraft)
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		if inv.TotalAmount != "300000" || len(inv.Lines) != 3 || inv.Lines[0].Kind != sales.KindMixPack ||
			inv.Lines[1].BaseQty != "18" || inv.Lines[2].BaseQty != "12" {
			t.Fatalf("converted invoice = %+v, want the short pack with 18 and 12 cans", inv)
		}
		path := v1 + "/invoices/" + inv.ID.String()
		expect(t, a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK,
			sales.InvoiceConfirmed)

		cola0, cam0 := stockOf(t, st.tenant, cola).OnHand, stockOf(t, st.tenant, cam).OnHand
		rec = a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{"lines": []map[string]any{
			{"invoice_line_id": inv.Lines[0].ID}}}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		doc := testkit.DecodeJSON[stockOutOut](t, rec).Document
		if doc == nil || doc.TotalAmount != "300000" || len(doc.Lines) != 3 || doc.Lines[1].BaseQty != "18" ||
			doc.Lines[2].BaseQty != "12" {
			t.Fatalf("stock out of the short pack = %+v", doc)
		}
		if got := onHand(t, cola); got != cola0.Sub(money.MustQty("18")).String() {
			t.Errorf("cola stock after the stock out = %s, want %s - 18", got, cola0)
		}
		if got := onHand(t, cam); got != cam0.Sub(money.MustQty("12")).String() {
			t.Errorf("cam stock after the stock out = %s, want %s - 12", got, cam0)
		}
		got := send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
		if got.Status != sales.InvoiceCompleted || got.DeliveredAmount != "300000" {
			t.Errorf("delivered invoice = %+v, want completed at 300000", got)
		}
	})

	t.Run("reversing the stock out of a short mix pack books its flavors back and undelivers the pack", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{pack("1", 1, 1)}})
		expect(t, rec, http.StatusCreated, sales.InvoiceDraft)
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		path := v1 + "/invoices/" + inv.ID.String()
		expect(t, a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK,
			sales.InvoiceConfirmed)

		cola0, cam0 := onHand(t, cola), onHand(t, cam)
		rec = a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{"lines": []map[string]any{
			{"invoice_line_id": inv.Lines[0].ID}}}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		so := testkit.DecodeJSON[stockOutOut](t, rec)
		delivered := send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
		if delivered.Lines[0].DeliveredQty != "1" || delivered.DeliveredAmount != "120000" {
			t.Fatalf("delivered invoice = %+v, want the pack delivered at 120000", delivered)
		}

		expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		expect(t, post(a, "/stock-outs/"+so.ID.String()+":reverse", map[string]any{"reason": "Giao nhầm thùng"}),
			http.StatusCreated, "")
		got := send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
		if got.DeliveredAmount != "0" {
			t.Errorf("invoice delivered after the reversal = %s, want 0", got.DeliveredAmount)
		}
		for _, l := range got.Lines {
			if l.DeliveredQty != "0" {
				t.Errorf("%s line delivered after the reversal = %s, want 0", l.Kind, l.DeliveredQty)
			}
		}
		if c, m := onHand(t, cola), onHand(t, cam); c != cola0 || m != cam0 {
			t.Errorf("stock after the reversal = cola %s, cam %s; want %s and %s as before the stock out", c, m, cola0, cam0)
		}
		verifySalesLedgers(t, st.tenant)
	})

	sku := st.newSku(t, a, "XA-PHONG", "10000")
	st.receive(t, a, sku, nil, "5", "6000")

	t.Run("a quote past its date reads expired and is quoted again by a copy", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/quotes", map[string]any{"customer_party_id": st.customer, "quote_date": "2026-03-01",
			"valid_until": "2026-03-10", "lines": []map[string]any{saleLine(sku, "2", nil)}})
		expect(t, rec, http.StatusCreated, "")
		old := testkit.DecodeJSON[quoteOut](t, rec)
		if old.Status != sales.QuoteExpired {
			t.Fatalf("quote valid until 10/03 read on 15/03 = %+v, want expired", old)
		}
		expired := send[dataOf[withID]](t, a, http.MethodGet, "/quotes?status=expired", nil).Data
		if !slices.ContainsFunc(expired, func(q withID) bool { return q.ID == old.ID }) {
			t.Errorf("expired quotes = %v, want %s", expired, old.ID)
		}
		rec = a.do(http.MethodPost, v1+"/quotes/"+old.ID.String()+":copy", nil)
		expect(t, rec, http.StatusCreated, "")
		fresh := testkit.DecodeJSON[quoteOut](t, rec)
		if fresh.ID == old.ID || fresh.Status != sales.QuoteDraft || fresh.QuoteDate != "2026-03-15" ||
			fresh.ValidUntil != "2026-03-24" || fresh.TotalAmount != "20000" {
			t.Errorf("copy = %+v, want a draft of today valid for the same 9 days", fresh)
		}
		printOf(t, sales.PrintQuote, fresh.ID, send[struct {
			DocNo string `json:"doc_no"`
		}](t, a, http.MethodGet, "/quotes/"+fresh.ID.String(), nil).DocNo)
	})

	t.Run("a deposit on a draft invoice is consumed by its stock out", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(sku, "2", nil)}})
		expect(t, rec, http.StatusCreated, sales.InvoiceDraft)
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		path := "/invoices/" + inv.ID.String()
		body := map[string]any{"amount": "5000", "payment_method_id": st.cashMethod}
		key := keyed(nil)
		rec = a.doWith(http.MethodPut, v1+path+"/paid-amount", body, key)
		expect(t, rec, http.StatusOK, "")
		paid := testkit.DecodeJSON[invoiceOut](t, rec)
		if paid.Payment.Paid != "5000" || paid.Payment.Open != "15000" {
			t.Fatalf("invoice after the deposit = %+v", paid.Payment)
		}
		// The retry finds the amount already paid, yet gets the response of the deposit it made.
		payments := countRows(t, st.tenant, `SELECT count(*) FROM payments`)
		again := a.doWith(http.MethodPut, v1+path+"/paid-amount", body, key)
		expect(t, again, http.StatusOK, "")
		if again.Header().Get("Idempotent-Replay") != "true" {
			t.Errorf("replayed deposit = %s %s", again.Header(), again.Body)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM payments`); n != payments {
			t.Errorf("payments after the replay = %d, want %d", n, payments)
		}
		expect(t, a.doWith(http.MethodPut, v1+path+"/paid-amount", body, keyed(nil)), http.StatusConflict, sales.CodeNoChange)

		expect(t, a.doWith(http.MethodPost, v1+path+":confirm", nil, keyed(ifMatchOf(paid.Version))), http.StatusOK,
			sales.InvoiceConfirmed)
		expect(t, post(a, path+"/stock-outs", map[string]any{}), http.StatusCreated, "")
		out := send[dataOf[struct {
			ID    uuid.UUID `json:"id"`
			DocNo string    `json:"doc_no"`
		}]](t, a, http.MethodGet, "/sales-documents?invoice_id="+inv.ID.String(), nil).Data
		if len(out) != 1 {
			t.Fatalf("sales documents of the invoice = %+v", out)
		}
		doc := send[salesDocOut](t, a, http.MethodGet, "/sales-documents/"+out[0].ID.String(), nil)
		if doc.TotalAmount != "20000" || doc.Payment.Paid != "5000" || doc.Payment.Open != "15000" || doc.StockOutID == nil {
			t.Errorf("BH after the deposit = %+v, want 5000 of 20000 settled", doc)
		}
		printOf(t, sales.PrintInvoice, inv.ID, send[struct {
			DocNo string `json:"doc_no"`
		}](t, a, http.MethodGet, path, nil).DocNo)
		so := send[stockOutOut](t, a, http.MethodGet, "/stock-outs/"+doc.StockOutID.String(), nil)
		printOf(t, sales.PrintStockOut, so.ID, so.DocNo)
	})

	t.Run("a deposit is spread over the stock outs that deliver the invoice", func(t *testing.T) {
		tv := st.newSku(t, a, "TIVI-55", "1000000")
		st.receive(t, a, tv, nil, "10", "700000")
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(tv, "10", nil)}})
		expect(t, rec, http.StatusCreated, "")
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		path := "/invoices/" + inv.ID.String()
		rec = a.doWith(http.MethodPut, v1+path+"/paid-amount", map[string]any{"amount": "3000000",
			"payment_method_id": st.cashMethod}, keyed(nil))
		expect(t, rec, http.StatusOK, "")
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		expect(t, a.doWith(http.MethodPost, v1+path+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK, "")

		expect(t, post(a, path+"/stock-outs", map[string]any{"lines": []map[string]any{
			{"invoice_line_id": inv.Lines[0].ID, "base_qty": "6"}}}), http.StatusCreated, "")
		expect(t, post(a, path+"/stock-outs", map[string]any{}), http.StatusCreated, "")
		docs := send[dataOf[withID]](t, a, http.MethodGet, "/sales-documents?invoice_id="+inv.ID.String(), nil).Data
		if len(docs) != 2 {
			t.Fatalf("sales documents of the invoice = %+v, want two", docs)
		}
		total, paid, open := money.ZeroMoney, money.ZeroMoney, money.ZeroMoney
		for _, d := range docs {
			doc := send[salesDocOut](t, a, http.MethodGet, "/sales-documents/"+d.ID.String(), nil)
			total = total.Add(money.MustMoney(doc.TotalAmount))
			paid = paid.Add(money.MustMoney(doc.Payment.Paid))
			open = open.Add(money.MustMoney(doc.Payment.Open))
		}
		if total.String() != "10000000" || paid.String() != "3000000" || open.String() != "7000000" {
			t.Errorf("sales documents total %s paid %s open %s, want 10000000 with 3000000 paid", total, paid, open)
		}
		got := send[invoiceOut](t, a, http.MethodGet, path, nil)
		if got.Status != sales.InvoiceCompleted || got.Payment.Code != sales.PaymentPartial || got.Payment.Open != "7000000" {
			t.Errorf("delivered invoice = %s %+v, want partly paid with 7000000 open", got.Status, got.Payment)
		}
		// The list carries what was paid, counting the deposit once though it settled both stock outs.
		type listedInvoice struct {
			ID         uuid.UUID `json:"id"`
			PaidAmount *string   `json:"paid_amount"`
		}
		listed := send[dataOf[listedInvoice]](t, a, http.MethodGet, "/invoices?customer_id="+st.customer.String(), nil).Data
		i := slices.IndexFunc(listed, func(r listedInvoice) bool { return r.ID == inv.ID })
		if i < 0 || listed[i].PaidAmount == nil || *listed[i].PaidAmount != "3000000" {
			t.Errorf("invoice list = %+v, want %s with 3000000 paid", listed, inv.ID)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM payments WHERE amount = 3000000 AND allocated_amount = amount`); n != 1 {
			t.Errorf("fully allocated deposits = %d, want 1", n)
		}
	})

	t.Run("a reversed receipt no longer counts in what the invoice was paid", func(t *testing.T) {
		fan := st.newSku(t, a, "QUAT-TRAN", "500000")
		st.receive(t, a, fan, nil, "2", "300000")
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(fan, "2", nil)}})
		expect(t, rec, http.StatusCreated, "")
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		path := "/invoices/" + inv.ID.String()
		expect(t, a.doWith(http.MethodPost, v1+path+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK, "")
		expect(t, post(a, path+"/stock-outs", map[string]any{}), http.StatusCreated, "")
		docs := send[dataOf[struct {
			DocNo string `json:"doc_no"`
		}]](t, a, http.MethodGet, "/sales-documents?invoice_id="+inv.ID.String(), nil).Data
		if len(docs) != 1 {
			t.Fatalf("sales documents of the invoice = %+v", docs)
		}
		paidAmount := func() string {
			t.Helper()
			listed := send[dataOf[struct {
				ID         uuid.UUID `json:"id"`
				PaidAmount *string   `json:"paid_amount"`
			}]](t, a, http.MethodGet, "/invoices?customer_id="+st.customer.String(), nil).Data
			for _, r := range listed {
				if r.ID == inv.ID && r.PaidAmount != nil {
					return *r.PaidAmount
				}
			}
			t.Fatalf("invoice list = %+v, want %s with a paid amount", listed, inv.ID)
			return ""
		}
		rec = post(a, "/payments", map[string]any{"direction": "in", "party_id": st.customer, "purpose": "settlement",
			"payment_method_id": st.cashMethod, "amount": "400000",
			"allocations": []map[string]any{{"obligation_id": obligationOf(t, st.tenant, docs[0].DocNo), "amount": "400000"}}})
		expect(t, rec, http.StatusCreated, "")
		receipt := testkit.DecodeJSON[withID](t, rec)
		if got := paidAmount(); got != "400000" {
			t.Errorf("paid amount after the receipt = %s, want 400000", got)
		}

		expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		expect(t, post(a, "/payments/"+receipt.ID.String()+":reverse", nil), http.StatusOK, debt.PaymentReversed)
		if got := paidAmount(); got != "0" {
			t.Errorf("paid amount after the reversed receipt = %s, want 0", got)
		}
	})

	t.Run("a confirmed invoice is revised, reserves what arrives and cancels what is left", func(t *testing.T) {
		item := st.newSku(t, a, "BOT-GIAT", "30000")
		st.receive(t, a, item, nil, "5", "20000")
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(item, "8", nil)}})
		expect(t, rec, http.StatusCreated, "")
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		path := v1 + "/invoices/" + inv.ID.String()
		lines := func(want string, got invoiceOut, reserved, backorder, delivered string) {
			t.Helper()
			l := got.Lines[0]
			if got.Status != want || l.ReservedQty != reserved || l.BackorderQty != backorder || l.DeliveredQty != delivered {
				t.Fatalf("invoice = %s %+v, want %s reserved %s backorder %s delivered %s", got.Status, l, want, reserved,
					backorder, delivered)
			}
		}
		rec = a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version)))
		expect(t, rec, http.StatusOK, "")
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		lines(sales.InvoiceConfirmed, inv, "5", "3", "0")

		rec = a.doWith(http.MethodPost, path+":revise", map[string]any{"change_summary": "Khách giảm còn 6", "draft": map[string]any{
			"lines": []map[string]any{{"id": inv.Lines[0].ID, "sku_id": item, "qty": "6"}}}}, keyed(ifMatchOf(inv.Version)))
		expect(t, rec, http.StatusOK, "")
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		lines(sales.InvoiceConfirmed, inv, "5", "1", "0")
		if inv.TotalAmount != "180000" {
			t.Errorf("revised total = %s, want 6 x 30000", inv.TotalAmount)
		}

		expect(t, a.doWith(http.MethodPost, path+":reserve-more", nil, keyed(ifMatchOf(inv.Version))), http.StatusConflict,
			sales.CodeNothingToReserve)
		st.receive(t, a, item, nil, "1", "20000")
		rec = a.doWith(http.MethodPost, path+":reserve-more", nil, keyed(ifMatchOf(inv.Version)))
		expect(t, rec, http.StatusOK, "")
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		lines(sales.InvoiceConfirmed, inv, "6", "0", "0")

		expect(t, post(a, "/invoices/"+inv.ID.String()+"/stock-outs", map[string]any{"lines": []map[string]any{
			{"invoice_line_id": inv.Lines[0].ID, "base_qty": "2"}}}), http.StatusCreated, "")
		inv = send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
		lines(sales.InvoicePartiallyDelivered, inv, "4", "0", "2")

		rec = a.doWith(http.MethodPost, path+":cancel-remainder", nil, keyed(ifMatchOf(inv.Version)))
		expect(t, rec, http.StatusOK, "")
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		lines(sales.InvoiceCancelledRemaining, inv, "0", "0", "2")
		if inv.DeliveredAmount != "60000" {
			t.Errorf("delivered amount = %s, want 2 x 30000", inv.DeliveredAmount)
		}
		if got := stockOf(t, st.tenant, item); got.OnHand.String() != "4" {
			t.Errorf("stock = %+v, want 6 - 2", got)
		}
		// The released reservation is free stock again: a walk-in buys all four.
		checkout(t, a, st.cashSale("120000", saleLine(item, "4", nil)))
	})

	t.Run("an invoice confirms once it names its color and brings the owner's confirmations", func(t *testing.T) {
		shirt := st.newSku(t, a, "AO-THUN", "100000")
		red, purple := st.colors["do"], st.colors["tim"]
		st.receive(t, a, shirt, &red, "3", "60000")
		st.receive(t, a, shirt, &purple, "3", "60000")
		rec := a.do(http.MethodGet, v1+"/skus/"+shirt.String(), nil)
		expect(t, rec, http.StatusOK, "")
		expect(t, a.doWith(http.MethodPatch, v1+"/skus/"+shirt.String(), map[string]any{"min_price": "90000"},
			http.Header{"If-Match": {rec.Header().Get("ETag")}}), http.StatusOK, "")
		customer := send[withID](t, a, http.MethodPost, "/customers",
			map[string]any{"kind": "person", "name": "Anh Tuấn", "credit_limit": "100000"}).ID

		line := saleLine(shirt, "2", nil)
		line["unit_price"] = "80000"
		rec = a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": customer,
			"lines": []map[string]any{line}})
		expect(t, rec, http.StatusCreated, sales.InvoiceDraft)
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		path := v1 + "/invoices/" + inv.ID.String()
		check := func() sales.InvoiceCheck {
			t.Helper()
			rec := a.doWith(http.MethodPost, path+":check", nil, keyed(nil))
			expect(t, rec, http.StatusOK, "")
			return testkit.DecodeJSON[sales.InvoiceCheck](t, rec)
		}
		got := check()
		wantExceptions := []string{string(exception.CreditLimitExceeded), string(exception.BelowMinPrice)}
		if !slices.Equal(got.ColorRequired, []int{1}) || !slices.Equal(got.BelowMinPrice, []int{1}) ||
			!got.CreditLimitExceeded || !slices.Equal(got.ExceptionsRequired, wantExceptions) || got.Ready {
			t.Fatalf("check = %+v, want the color, the minimum price and the credit limit", got)
		}
		expect(t, a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version))),
			http.StatusUnprocessableEntity, sales.CodeColorRequired)

		// Unlike a POS sale, an invoice never picks a color itself, even the only one left.
		checkout(t, a, st.cashSale("300000", saleLine(shirt, "3", &purple)))
		if got := check(); !slices.Equal(got.ColorRequired, []int{1}) {
			t.Fatalf("check after the purple ones sold = %+v, want the color still required", got)
		}
		line["color_id"] = red
		rec = a.doWith(http.MethodPut, path+"/lines", map[string]any{"lines": []map[string]any{line}}, ifMatchOf(inv.Version))
		expect(t, rec, http.StatusOK, "")
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		if got := check(); len(got.ColorRequired) != 0 || !slices.Equal(got.ExceptionsRequired, wantExceptions) {
			t.Fatalf("check with the color = %+v", got)
		}
		expect(t, a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version))),
			http.StatusConflict, string(exception.CreditLimitExceeded))

		// The owner confirms each exception for this version of the invoice and the body the
		// confirm sends, which hashes without its confirmation ids.
		hash, err := exception.PayloadHash([]byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		confirmed := func(kind exception.Kind) uuid.UUID {
			t.Helper()
			return send[withID](t, a, http.MethodPost, "/exception-confirmations", map[string]any{
				"kind": kind, "doc_type": "invoice", "doc_id": inv.ID, "doc_version": inv.Version,
				"payload_hash": hex.EncodeToString(hash), "note": "Khách quen",
			}).ID
		}
		credit := confirmed(exception.CreditLimitExceeded)
		expect(t, a.doWith(http.MethodPost, path+":confirm", map[string]any{"exception_confirmation_ids": []uuid.UUID{credit}},
			keyed(ifMatchOf(inv.Version))), http.StatusConflict, string(exception.BelowMinPrice))
		both := []uuid.UUID{credit, confirmed(exception.BelowMinPrice)}
		rec = a.doWith(http.MethodPost, path+":confirm", map[string]any{"exception_confirmation_ids": both},
			keyed(ifMatchOf(inv.Version)))
		expect(t, rec, http.StatusOK, sales.InvoiceConfirmed)
		inv = testkit.DecodeJSON[invoiceOut](t, rec)
		if l := inv.Lines[0]; inv.CurrentVersion != 1 || l.ColorID == nil || *l.ColorID != red || l.ReservedQty != "2" ||
			l.BackorderQty != "0" {
			t.Fatalf("confirmed invoice = %+v, want version 1 with 2 red reserved", inv)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM exception_confirmation_uses WHERE used_by_doc_id = $1`,
			inv.ID); n != 2 {
			t.Errorf("confirmations used = %d, want 2", n)
		}
	})

	t.Run("a saved cart is sold once and copied after", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/pos/carts", map[string]any{"label": "Bàn 3", "lines": []map[string]any{saleLine(sku, "1", nil)}})
		expect(t, rec, http.StatusCreated, sales.CartOpen)
		cart := testkit.DecodeJSON[cartOut](t, rec)
		path := v1 + "/pos/carts/" + cart.ID.String()
		rec = a.doWith(http.MethodPatch, path, map[string]any{"label": "Bàn 3", "lines": []map[string]any{saleLine(sku, "2", nil)}},
			ifMatchOf(cart.Version))
		expect(t, rec, http.StatusOK, "")
		cart = testkit.DecodeJSON[cartOut](t, rec)

		pay := map[string]any{"payments": []map[string]any{{"payment_method_id": st.cashMethod, "amount": "20000"}}}
		rec = a.doWith(http.MethodPost, path+":checkout", pay, keyed(ifMatchOf(cart.Version)))
		expect(t, rec, http.StatusCreated, "")
		sale := testkit.DecodeJSON[posSaleOut](t, rec)
		if sale.Document.TotalAmount != "20000" || sale.Document.Lines[0].BaseQty != "2" {
			t.Fatalf("cart sale = %+v", sale.Document)
		}
		sold := send[cartOut](t, a, http.MethodGet, "/pos/carts/"+cart.ID.String(), nil)
		if sold.Status != sales.CartSold || sold.SalesDocumentID == nil || *sold.SalesDocumentID != sale.Document.ID {
			t.Errorf("sold cart = %+v", sold)
		}
		expect(t, a.doWith(http.MethodPatch, path, map[string]any{"lines": []map[string]any{saleLine(sku, "1", nil)}},
			ifMatchOf(sold.Version)), http.StatusConflict, sales.CodeCartSold)

		rec = a.do(http.MethodPost, path+":copy", nil)
		expect(t, rec, http.StatusCreated, "")
		cp := testkit.DecodeJSON[cartOut](t, rec)
		if cp.ID == cart.ID || cp.Status != sales.CartOpen || len(cp.Lines) != 1 || cp.Lines[0].BaseQty != "2" {
			t.Errorf("copied cart = %+v", cp)
		}
	})

	t.Run("a damaged return goes to quarantine and reduces what the customer owes", func(t *testing.T) {
		item := st.newSku(t, a, "NOI-CHIEN", "10000")
		st.receive(t, a, item, nil, "5", "6000")
		sale := checkout(t, a, map[string]any{"customer_party_id": st.customer, "lines": []map[string]any{saleLine(item, "2", nil)},
			"payments": []map[string]any{}})
		if sale.Document.Payment.Open != "20000" {
			t.Fatalf("credit sale = %+v", sale.Document.Payment)
		}
		rec := post(a, "/sales-returns", map[string]any{"sales_document_id": sale.Document.ID, "reason_id": st.returnReason,
			"lines": []map[string]any{{"sales_document_line_id": sale.Document.Lines[0].ID, "qty": "1", "restock_to": sales.RestockQuarantine}}})
		expect(t, rec, http.StatusCreated, "")
		posted := testkit.DecodeJSON[struct {
			Return salesReturnOut   `json:"sales_return"`
			Refund *salesPaymentOut `json:"refund"`
		}](t, rec)
		th := posted.Return
		if th.DebtReduction != "10000" || th.RefundDue != "0" || posted.Refund != nil || th.Lines[0].RestockTo != sales.RestockQuarantine {
			t.Fatalf("quarantined return = %+v, refund %+v", th, posted.Refund)
		}
		if got := stockOf(t, st.tenant, item); got.OnHand.String() != "4" || got.Quarantine.String() != "1" {
			t.Errorf("stock = %+v, want 4 on hand of which 1 quarantined", got)
		}
		if doc := send[salesDocOut](t, a, http.MethodGet, "/sales-documents/"+sale.Document.ID.String(), nil); doc.Payment.Open != "10000" {
			t.Errorf("open after the return = %+v, want 10000", doc.Payment)
		}
		printOf(t, sales.PrintSalesReturn, th.ID, th.DocNo)
		expect(t, a.do(http.MethodGet, v1+"/print/receipt/"+th.ID.String(), nil), http.StatusUnprocessableEntity, "")
	})

	verifySalesLedgers(t, st.tenant)
}

// A combo on an invoice holds its components' stock and is delivered whole through its header; the
// stock out and its reversal keep every ledger in step.
func TestComboInvoiceIsDeliveredThroughItsHeader(t *testing.T) {
	s := newKitsStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	bag, card := st.newSku(t, a, "TUI-GIAY", "10000"), st.newSku(t, a, "THIEP", "5000")
	st.receive(t, a, bag, nil, "10", "4000")
	st.receive(t, a, card, nil, "10", "1000")
	combo := createKit(t, a, map[string]any{"type": "combo", "name": "Túi quà kèm thiệp", "sku_code": "COMBO-TUI",
		"base_unit_id": st.units["cái"], "retail_price": "14000", "recipe": map[string]any{"items": []map[string]any{
			{"kind": "packaging", "sku_id": bag, "qty": "1"}, {"kind": "accessory", "sku_id": card, "qty": "2"}}}})

	rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(combo.SkuID, "3", nil)}})
	expect(t, rec, http.StatusCreated, sales.InvoiceDraft)
	inv := testkit.DecodeJSON[invoiceOut](t, rec)
	if inv.TotalAmount != "42000" || len(inv.Lines) != 3 || inv.Lines[0].Kind != sales.KindCombo ||
		inv.Lines[1].Kind != sales.KindComboComponent || inv.Lines[2].Kind != sales.KindComboComponent {
		t.Fatalf("combo invoice = %+v, want 3 combos at 42000 over their two components", inv)
	}
	path := v1 + "/invoices/" + inv.ID.String()
	expect(t, a.doWith(http.MethodPost, path+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK,
		sales.InvoiceConfirmed)
	header := inv.Lines[0].ID
	expect(t, a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{"lines": []map[string]any{
		{"invoice_line_id": inv.Lines[1].ID}}}, keyed(nil)), http.StatusUnprocessableEntity, "deliver_through_parent")

	rec = a.doWith(http.MethodPost, path+"/stock-outs", map[string]any{"lines": []map[string]any{
		{"invoice_line_id": header}}}, keyed(nil))
	expect(t, rec, http.StatusCreated, "")
	so := testkit.DecodeJSON[stockOutOut](t, rec)
	if doc := so.Document; doc == nil || doc.TotalAmount != "42000" || len(doc.Lines) != 3 {
		t.Fatalf("stock out of the combo = %+v, want the 3 combos at 42000 with their components", so.Document)
	}
	wantStock := func(t *testing.T, bags, cards string) {
		t.Helper()
		if b, c := stockOf(t, st.tenant, bag).OnHand.String(), stockOf(t, st.tenant, card).OnHand.String(); b != bags || c != cards {
			t.Errorf("stock = %s bags, %s cards; want %s and %s", b, c, bags, cards)
		}
	}
	wantStock(t, "7", "4")
	got := send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
	if got.Status != sales.InvoiceCompleted || got.DeliveredAmount != "42000" || got.Lines[0].DeliveredQty != "3" {
		t.Errorf("delivered combo invoice = %+v, want completed with the 3 combos delivered", got)
	}
	verifySalesLedgers(t, st.tenant)

	expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
	expect(t, post(a, "/stock-outs/"+so.ID.String()+":reverse", map[string]any{"reason": "Giao nhầm combo"}),
		http.StatusCreated, "")
	wantStock(t, "10", "10")
	got = send[invoiceOut](t, a, http.MethodGet, "/invoices/"+inv.ID.String(), nil)
	if got.DeliveredAmount != "0" || got.Lines[0].DeliveredQty != "0" {
		t.Errorf("combo invoice after the reversal = %+v, want nothing delivered", got)
	}
	verifySalesLedgers(t, st.tenant)
}
