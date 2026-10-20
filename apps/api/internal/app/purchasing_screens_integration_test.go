//go:build integration

package app_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

type skuListRow struct {
	ID                uuid.UUID `json:"id"`
	Code              string    `json:"code"`
	DisplayName       string    `json:"display_name"`
	TrackLot          bool      `json:"track_lot"`
	RetailPrice       *string   `json:"retail_price"`
	WholesalePrice    *string   `json:"wholesale_price"`
	OnHand            string    `json:"on_hand"`
	LastPurchasePrice *string   `json:"last_purchase_price"`
	Units             []struct {
		UnitID            uuid.UUID `json:"unit_id"`
		Factor            string    `json:"factor"`
		IsDefaultPurchase bool      `json:"is_default_purchase"`
	} `json:"units"`
}

// skuNamed finds the one listed SKU whose name is name.
func skuNamed(t *testing.T, b *browser, name string) skuListRow {
	t.Helper()
	var found []skuListRow
	for _, s := range send[dataOf[skuListRow]](t, b, http.MethodGet, "/skus?limit=200", nil).Data {
		if s.DisplayName == name {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("SKUs named %q = %+v, want one", name, found)
	}
	return found[0]
}

// The purchasing screens create products from document lines and read their lists with the
// columns they show.
func TestPurchasingScreensOverHTTP(t *testing.T) {
	s, _, _ := newProcurementStack(t)
	owner := s.signUp(t)
	a := s.signIn(t, owner)
	store := buildPurchaseStore(t, a)

	t.Run("a PO line creates its product with a purchase unit and base prices", func(t *testing.T) {
		order := confirmedOrder(t, a, map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "5", "unit_price": "240000", "new_product": map[string]any{
				"name": "Nước cam lon mới", "base_unit_id": store.units["cái"], "purchase_unit_id": store.units["thùng"],
				"factor": "24", "retail_price": "15000", "wholesale_price": "12000"}}}})
		if order.Lines[0].BaseQty != "120" {
			t.Fatalf("order line = %+v, want 5 cartons of 24", order.Lines[0])
		}
		sku := skuNamed(t, a, "Nước cam lon mới")
		if sku.RetailPrice == nil || *sku.RetailPrice != "15000" || sku.WholesalePrice == nil || *sku.WholesalePrice != "12000" {
			t.Errorf("prices = %v / %v", sku.RetailPrice, sku.WholesalePrice)
		}
		var carton bool
		for _, u := range sku.Units {
			carton = carton || (u.UnitID == store.units["thùng"] && u.Factor == "24" && u.IsDefaultPurchase)
		}
		if !carton {
			t.Errorf("units = %+v, want the carton of 24 as purchase unit", sku.Units)
		}

		type priceChange struct {
			SkuID     uuid.UUID `json:"sku_id"`
			SkuCode   string    `json:"sku_code"`
			Source    string    `json:"source"`
			ActorName *string   `json:"actor_name"`
		}
		var changes []priceChange
		for _, c := range send[dataOf[priceChange]](t, a, http.MethodGet, "/price-changes", nil).Data {
			if c.SkuID == sku.ID {
				changes = append(changes, c)
			}
		}
		if len(changes) != 2 {
			t.Fatalf("price changes of the new SKU = %+v, want retail and wholesale", changes)
		}
		for _, c := range changes {
			if c.SkuCode != sku.Code || c.Source != pricing.ChangePurchaseOrder || c.ActorName == nil || *c.ActorName != "Chủ cửa hàng" {
				t.Errorf("price change = %+v", c)
			}
		}
	})

	t.Run("a new product line is checked before anything is created", func(t *testing.T) {
		products := countRows(t, store.tenant, `SELECT count(*) FROM products`)
		expect(t, a.do(http.MethodPost, v1+"/purchase-orders", map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "1", "unit_price": "1000", "new_product": map[string]any{"name": " "}}}}),
			http.StatusUnprocessableEntity, "lines.0.new_product.name")
		sku := store.newSku(t, a, "MOI-VA-CU")
		expect(t, a.do(http.MethodPost, v1+"/purchase-orders", map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"sku_id": sku, "qty": "1", "unit_price": "1000", "new_product": map[string]any{
				"name": "Hàng thừa", "base_unit_id": store.units["cái"]}}}}),
			http.StatusUnprocessableEntity, "not_allowed_with_sku_id")
		if n := countRows(t, store.tenant, `SELECT count(*) FROM products`); n != products+1 {
			t.Errorf("products = %d, want only the SKU made on purpose", n)
		}
	})

	t.Run("a new product line attaches its barcode to the base unit", func(t *testing.T) {
		confirmedOrder(t, a, map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "2", "unit_price": "96000", "new_product": map[string]any{
				"name": "Sữa đậu nành hộp", "base_unit_id": store.units["cái"], "purchase_unit_id": store.units["thùng"],
				"factor": "24", "barcode": " 8936000111222 "}}}})
		sku := skuNamed(t, a, "Sữa đậu nành hộp")
		m := send[struct {
			Sku struct {
				ID uuid.UUID `json:"id"`
			} `json:"sku"`
			UnitID    uuid.UUID `json:"unit_id"`
			Factor    string    `json:"factor"`
			MatchedBy string    `json:"matched_by"`
		}](t, a, http.MethodGet, "/sku-lookup?code=8936000111222", nil)
		if m.Sku.ID != sku.ID || m.UnitID != store.units["cái"] || m.Factor != "1" || m.MatchedBy != "barcode" {
			t.Errorf("lookup = %+v, want the new SKU in its base unit", m)
		}

		products := countRows(t, store.tenant, `SELECT count(*) FROM products`)
		expect(t, a.do(http.MethodPost, v1+"/purchase-orders", map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "1", "unit_price": "4000", "new_product": map[string]any{
				"name": "Sữa đậu nành chai", "base_unit_id": store.units["cái"], "barcode": "8936000111222"}}}}),
			http.StatusConflict, "lines.0.new_product.barcode")
		expect(t, a.do(http.MethodPost, v1+"/purchase-orders", map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "1", "unit_price": "4000", "new_product": map[string]any{
				"name": "Sữa đậu nành chai", "base_unit_id": store.units["cái"], "barcode": "8936 000"}}}}),
			http.StatusUnprocessableEntity, "lines.0.new_product.barcode")
		if n := countRows(t, store.tenant, `SELECT count(*) FROM products`); n != products {
			t.Errorf("products = %d, want %d: a refused line creates nothing", n, products)
		}
	})

	t.Run("two new products whose barcodes are forms of one UPC are refused on the second", func(t *testing.T) {
		products := countRows(t, store.tenant, `SELECT count(*) FROM products`)
		rec := post(a, "/receipts", map[string]any{"supplier_party_id": store.supplier, "lines": []map[string]any{
			{"qty": "1", "unit_price": "5000", "new_product": map[string]any{
				"name": "Kẹo dẻo gói nhỏ", "base_unit_id": store.units["cái"], "barcode": "012345678905"}},
			{"qty": "1", "unit_price": "5000", "new_product": map[string]any{
				"name": "Kẹo dẻo gói lớn", "base_unit_id": store.units["cái"], "barcode": "0012345678905"}}}})
		expect(t, rec, http.StatusConflict, "barcode_taken")
		expect(t, rec, http.StatusConflict, "lines.1.new_product.barcode")
		if n := countRows(t, store.tenant, `SELECT count(*) FROM products`); n != products {
			t.Errorf("products = %d, want %d: no SKU of the refused receipt is kept", n, products)
		}
	})

	t.Run("a quick receipt creates a lot-tracked product and lists its receipt", func(t *testing.T) {
		supplier := store.newSupplier(t, a, "Kho Bánh Kẹo Sỉ")
		r := postReceipt(t, a, map[string]any{"supplier_party_id": supplier, "lines": []map[string]any{{
			"qty": "10", "unit_price": "8000", "lot_no": "L-0326", "expiry_date": "2026-12-31",
			"new_product": map[string]any{"name": "Bánh quy bơ hộp", "base_unit_id": store.units["cái"], "track_lot": true}}}})
		sku := skuNamed(t, a, "Bánh quy bơ hộp")
		if !sku.TrackLot || sku.OnHand != "10" || sku.LastPurchasePrice == nil || *sku.LastPurchasePrice != "8000" {
			t.Errorf("received SKU = %+v", sku)
		}
		if r.Lines[0].LotID == nil {
			t.Errorf("receipt line = %+v, want a lot", r.Lines[0])
		}

		expect(t, a.do(http.MethodPost, v1+"/supplier-documents", map[string]any{"supplier_party_id": supplier,
			"document_no": "HD-BK-01", "document_date": "2026-03-15", "goods_amount": "80000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{r.ID}}), http.StatusCreated, procurement.MatchMatched)
		rows := send[dataOf[struct {
			ID                 uuid.UUID `json:"id"`
			SupplierDocumentNo *string   `json:"supplier_document_no"`
			OpenAmount         string    `json:"open_amount"`
		}]](t, a, http.MethodGet, "/receipts?q=banh+keo", nil).Data
		if len(rows) != 1 || rows[0].ID != r.ID || rows[0].SupplierDocumentNo == nil || *rows[0].SupplierDocumentNo != "HD-BK-01" ||
			rows[0].OpenAmount != "80000" {
			t.Errorf("receipts of the supplier = %+v", rows)
		}
	})

	t.Run("a receipt line of an order cannot create a product", func(t *testing.T) {
		order := confirmedOrder(t, a, map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"sku_id": store.newSku(t, a, "DH-CO-SAN"), "qty": "2", "unit_price": "1000"}}})
		expect(t, post(a, "/receipts", map[string]any{"purchase_order_id": order.ID, "lines": []map[string]any{{
			"purchase_order_line_id": order.Lines[0].ID, "qty": "2",
			"new_product": map[string]any{"name": "Không được", "base_unit_id": store.units["cái"]}}}}),
			http.StatusUnprocessableEntity, "not_allowed_with_order_line")
	})

	t.Run("returns list their receipt, lines and how the credit settled", func(t *testing.T) {
		supplier := store.newSupplier(t, a, "Xưởng Trả Hàng Đôi")
		r := store.receiveDirect(t, a, supplier, store.newSku(t, a, "XT-DS"), "10", "10000")
		for qty, settlement := range map[string]string{"2": procurement.SettlementCreditPayable, "3": procurement.SettlementRefund} {
			expect(t, post(a, "/purchase-returns", map[string]any{"receipt_id": r.ID, "reason_id": store.returnReason,
				"settlement": settlement, "lines": []map[string]any{{"receipt_line_id": r.Lines[0].ID, "qty": qty}}}),
				http.StatusCreated, "")
		}
		rows := send[dataOf[struct {
			Settlement      string `json:"settlement"`
			ReceiptDocNo    string `json:"receipt_doc_no"`
			CreditedAmount  string `json:"credited_amount"`
			RefundDueAmount string `json:"refund_due_amount"`
			Lines           []struct {
				SkuCode string `json:"sku_code"`
				Qty     string `json:"qty"`
			} `json:"lines"`
		}]](t, a, http.MethodGet, "/purchase-returns?supplier_id="+supplier.String(), nil).Data
		if len(rows) != 2 {
			t.Fatalf("returns = %+v", rows)
		}
		receipt := send[struct {
			DocNo string `json:"doc_no"`
		}](t, a, http.MethodGet, "/receipts/"+r.ID.String(), nil)
		want := map[string][2]string{procurement.SettlementCreditPayable: {"20000", "0"}, procurement.SettlementRefund: {"0", "30000"}}
		for _, x := range rows {
			if x.ReceiptDocNo != receipt.DocNo || len(x.Lines) != 1 || x.Lines[0].SkuCode != "XT-DS" ||
				[2]string{x.CreditedAmount, x.RefundDueAmount} != want[x.Settlement] {
				t.Errorf("return row = %+v", x)
			}
		}
		if n := len(send[dataOf[withID]](t, a, http.MethodGet, "/purchase-returns?q=tra+hang+doi", nil).Data); n != 2 {
			t.Errorf("returns found by supplier words = %d, want 2", n)
		}
	})

	t.Run("a supplier with an open order cannot be stopped", func(t *testing.T) {
		supplier := store.newSupplier(t, a, "Nhà Phân Phối Đang Giao")
		sku := store.newSku(t, a, "NCC-DANG-GIAO")
		order := confirmedOrder(t, a, map[string]any{"supplier_party_id": supplier,
			"lines": []map[string]any{{"sku_id": sku, "qty": "4", "unit_price": "5000"}}})
		type supplierRow struct {
			ID              uuid.UUID `json:"id"`
			OrderCount      int       `json:"order_count"`
			LastReceiptDate *string   `json:"last_receipt_date"`
			PayableOpen     string    `json:"payable_open"`
			CanStop         bool      `json:"can_stop"`
			CanDelete       bool      `json:"can_delete"`
		}
		row := func() supplierRow {
			t.Helper()
			for _, s := range send[dataOf[supplierRow]](t, a, http.MethodGet, "/suppliers?q=dang+giao", nil).Data {
				if s.ID == supplier {
					return s
				}
			}
			t.Fatal("supplier not listed")
			return supplierRow{}
		}
		if got := row(); got.OrderCount != 1 || got.CanStop || got.CanDelete || got.LastReceiptDate != nil {
			t.Errorf("supplier with an open order = %+v", got)
		}
		expect(t, post(a, "/suppliers/"+supplier.String()+":stop", nil), http.StatusConflict, "supplier_has_open_orders")

		receiveOrder(t, a, order, "4")
		if got := row(); !got.CanStop || got.PayableOpen != "20000" || got.LastReceiptDate == nil {
			t.Errorf("supplier after receiving = %+v", got)
		}
		rec := post(a, "/suppliers/"+supplier.String()+":stop", nil)
		expect(t, rec, http.StatusOK, "")
		if got := testkit.DecodeJSON[struct {
			Status string `json:"status"`
		}](t, rec); got.Status != partner.StatusInactive {
			t.Errorf("stopped supplier = %+v", got)
		}
	})
}
