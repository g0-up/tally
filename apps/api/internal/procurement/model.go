// Package procurement is Nhập hàng: purchase orders (PO), receipts (PN) with or without an
// order, landed cost, supplier documents matched to receipts, purchase returns (XT) and purchase
// price adjustments (DG). Stock moves go through the inventory ledger and payables through the
// debt recorders, both inside the procurement posting (posting.Coordinator).
package procurement

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Purchase order statuses (purchase_orders.status).
const (
	StatusDraft             = "draft"
	StatusConfirmed         = "confirmed"
	StatusPartiallyReceived = "partially_received"
	StatusCompleted         = "completed"
	StatusClosedShort       = "closed_short"
)

// Receipt line conditions (receipt_lines.condition).
const (
	ConditionAccepted = "accepted"
	// ConditionDamaged is received into the quarantine twin of its lot.
	ConditionDamaged = "damaged"
	// ConditionWrongItem is recorded for the supplier claim and never enters stock.
	ConditionWrongItem = "wrong_item"
	// ConditionExcess is received beyond the order and flagged.
	ConditionExcess = "excess"
)

// Statuses of the posted procurement documents.
const (
	DocPosted   = "posted"
	DocReversed = "reversed"
)

// Document types procurement writes on stock_moves.doc_type and cogs_adjustments.
const (
	DocReceipt         = "receipt"
	DocLandedCost      = "landed_cost_allocation"
	DocPurchaseReturn  = "purchase_return"
	DocPriceAdjustment = "purchase_price_adjustment"
)

// Purchase return settlements.
const (
	SettlementCreditPayable = "credit_payable"
	SettlementRefund        = "refund"
	SettlementReplacement   = "replacement"
)

// Purchase price adjustment kinds.
const (
	KindFinalPrice           = "final_price"
	KindPostPurchaseDiscount = "post_purchase_discount"
)

// Landed cost types and allocation methods.
const (
	CostShipping = "shipping"
	CostLoading  = "loading"
	CostOther    = "other"

	MethodByValue = "by_value"
	MethodByQty   = "by_qty"
)

// Supplier document match statuses.
const (
	MatchUnmatched = "unmatched"
	MatchMatched   = "matched"
	MatchVariance  = "variance"
)

// Reason kinds procurement cites.
const (
	ReasonCloseShort      = "close_short"
	ReasonPurchaseReturn  = "purchase_return"
	ReasonPriceAdjustment = "price_adjustment"
)

// PurchaseOrder is a PO header.
type PurchaseOrder struct {
	ID              uuid.UUID   `db:"id"`
	DocNo           string      `db:"doc_no"`
	SupplierPartyID uuid.UUID   `db:"supplier_party_id"`
	SupplierName    string      `db:"supplier_name"`
	OrderDate       time.Time   `db:"order_date"`
	ExpectedDate    *time.Time  `db:"expected_date"`
	Status          string      `db:"status"`
	GoodsAmount     money.Money `db:"goods_amount"`
	DiscountAmount  money.Money `db:"discount_amount"`
	TaxAmount       money.Money `db:"tax_amount"`
	TotalAmount     money.Money `db:"total_amount"`
	Note            *string     `db:"note"`
	CloseReasonID   *uuid.UUID  `db:"close_reason_id"`
	ClosedAt        *time.Time  `db:"closed_at"`
	ConfirmedAt     *time.Time  `db:"confirmed_at"`
	ConfirmedBy     *uuid.UUID  `db:"confirmed_by"`
	CreatedAt       time.Time   `db:"created_at"`
	UpdatedAt       time.Time   `db:"updated_at"`
	Version         int         `db:"version"`
}

// PurchaseOrderLine is a PO line in the screen's column order: Tên SP, ĐVT, Số lượng, Quy cách,
// Đơn giá, Thành tiền, Giá bán lẻ. Quantities in base units carry the progress of the line.
type PurchaseOrderLine struct {
	ID               uuid.UUID     `db:"id"`
	PurchaseOrderID  uuid.UUID     `db:"purchase_order_id"`
	LineNo           int           `db:"line_no"`
	SkuID            uuid.UUID     `db:"sku_id"`
	SkuCodeSnap      string        `db:"sku_code_snap"`
	NameSnap         string        `db:"name_snap"`
	VariantLabelSnap string        `db:"variant_label_snap"`
	UnitID           uuid.UUID     `db:"unit_id"`
	UnitNameSnap     string        `db:"unit_name_snap"`
	UnitFactorSnap   money.Factor  `db:"unit_factor_snap"`
	Qty              money.Qty     `db:"qty"`
	BaseQty          money.Qty     `db:"base_qty"`
	ColorID          *uuid.UUID    `db:"color_id"`
	SpecText         *string       `db:"spec_text"`
	UnitPrice        money.Money   `db:"unit_price"`
	DiscountAmount   money.Money   `db:"discount_amount"`
	TaxRateSnap      money.Percent `db:"tax_rate_snap"`
	LineAmount       money.Money   `db:"line_amount"`
	RetailPriceSnap  *money.Money  `db:"retail_price_snap"`
	ReceivedBaseQty  money.Qty     `db:"received_base_qty"`
	ReturnedBaseQty  money.Qty     `db:"returned_base_qty"`
}

// PendingBaseQty is what the supplier still has to deliver: never negative, an excess delivery
// does not make another line pending.
func (l PurchaseOrderLine) PendingBaseQty() money.Qty {
	p := l.BaseQty.Sub(l.ReceivedBaseQty)
	if p.IsNegative() {
		return money.ZeroQty
	}
	return p
}

// Receipt is a PN header. A reversal is a header-only receipt naming the original in ReversalOf.
type Receipt struct {
	ID               uuid.UUID   `db:"id"`
	WarehouseID      uuid.UUID   `db:"warehouse_id"`
	DocNo            string      `db:"doc_no"`
	SupplierPartyID  uuid.UUID   `db:"supplier_party_id"`
	SupplierName     string      `db:"supplier_name"`
	PurchaseOrderID  *uuid.UUID  `db:"purchase_order_id"`
	BusinessDate     time.Time   `db:"business_date"`
	GoodsAmount      money.Money `db:"goods_amount"`
	DiscountAmount   money.Money `db:"discount_amount"`
	TaxAmount        money.Money `db:"tax_amount"`
	LandedCostAmount money.Money `db:"landed_cost_amount"`
	TotalAmount      money.Money `db:"total_amount"`
	PaidNowAmount    money.Money `db:"paid_now_amount"`
	DueDate          *time.Time  `db:"due_date"`
	Status           string      `db:"status"`
	Note             *string     `db:"note"`
	IdempotencyKey   string      `db:"idempotency_key"`
	PostedAt         time.Time   `db:"posted_at"`
	PostedBy         uuid.UUID   `db:"posted_by"`
	ReversalOf       *uuid.UUID  `db:"reversal_of"`
	CreatedAt        time.Time   `db:"created_at"`
}

// ReceiptLine is a received line. LotID is the lot the goods went into (the quarantine twin for a
// damaged line); a wrong_item line has none.
type ReceiptLine struct {
	ID                  uuid.UUID     `db:"id"`
	ReceiptID           uuid.UUID     `db:"receipt_id"`
	LineNo              int           `db:"line_no"`
	PurchaseOrderLineID *uuid.UUID    `db:"purchase_order_line_id"`
	SkuID               uuid.UUID     `db:"sku_id"`
	SkuCodeSnap         string        `db:"sku_code_snap"`
	NameSnap            string        `db:"name_snap"`
	VariantLabelSnap    string        `db:"variant_label_snap"`
	UnitID              uuid.UUID     `db:"unit_id"`
	UnitNameSnap        string        `db:"unit_name_snap"`
	UnitFactorSnap      money.Factor  `db:"unit_factor_snap"`
	Qty                 money.Qty     `db:"qty"`
	BaseQty             money.Qty     `db:"base_qty"`
	ColorID             *uuid.UUID    `db:"color_id"`
	SpecText            *string       `db:"spec_text"`
	Condition           string        `db:"condition"`
	LotNo               string        `db:"lot_no"`
	ExpiryDate          *time.Time    `db:"expiry_date"`
	LotID               *uuid.UUID    `db:"lot_id"`
	UnitPrice           money.Money   `db:"unit_price"`
	DiscountAmount      money.Money   `db:"discount_amount"`
	TaxRateSnap         money.Percent `db:"tax_rate_snap"`
	LineAmount          money.Money   `db:"line_amount"`
	LandedCostAmount    money.Money   `db:"landed_cost_amount"`
	UnitCost            money.Money   `db:"unit_cost"`
	ReturnedBaseQty     money.Qty     `db:"returned_base_qty"`
}

// KeptBaseQty is the received quantity not returned to the supplier yet.
func (l ReceiptLine) KeptBaseQty() money.Qty { return l.BaseQty.Sub(l.ReturnedBaseQty) }

// LandedCost is a landed cost allocation on a receipt.
type LandedCost struct {
	ID           uuid.UUID   `db:"id"`
	ReceiptID    uuid.UUID   `db:"receipt_id"`
	CostType     string      `db:"cost_type"`
	Description  *string     `db:"description"`
	Amount       money.Money `db:"amount"`
	Method       string      `db:"method"`
	PayeePartyID *uuid.UUID  `db:"payee_party_id"`
	BusinessDate time.Time   `db:"business_date"`
	PostedAt     time.Time   `db:"posted_at"`
	PostedBy     uuid.UUID   `db:"posted_by"`
	CreatedAt    time.Time   `db:"created_at"`
}

// SplitLine is the share of a landed cost or price adjustment on one receipt line: the part on
// stock still held (a cost_adjustment move) and the part already sold (a COGS adjustment).
type SplitLine struct {
	ReceiptLineID uuid.UUID   `db:"receipt_line_id"`
	Amount        money.Money `db:"amount"`
	OnHandAmount  money.Money `db:"on_hand_amount"`
	SoldAmount    money.Money `db:"sold_amount"`
}

// SupplierDocument is the supplier's invoice, matched to receipts. It never creates a payable.
type SupplierDocument struct {
	ID              uuid.UUID   `db:"id"`
	SupplierPartyID uuid.UUID   `db:"supplier_party_id"`
	SupplierName    string      `db:"supplier_name"`
	DocumentNo      string      `db:"document_no"`
	DocumentDate    time.Time   `db:"document_date"`
	GoodsAmount     money.Money `db:"goods_amount"`
	TaxAmount       money.Money `db:"tax_amount"`
	TotalAmount     money.Money `db:"total_amount"`
	MatchStatus     string      `db:"match_status"`
	VarianceAmount  money.Money `db:"variance_amount"`
	Note            *string     `db:"note"`
	CreatedAt       time.Time   `db:"created_at"`
	UpdatedAt       time.Time   `db:"updated_at"`
	Version         int         `db:"version"`
}

// MatchedReceipt is a receipt linked to a supplier document.
type MatchedReceipt struct {
	ReceiptID     uuid.UUID   `db:"receipt_id"`
	DocNo         string      `db:"doc_no"`
	BusinessDate  time.Time   `db:"business_date"`
	MatchedAmount money.Money `db:"matched_amount"`
}

// PurchaseReturn is an XT header.
type PurchaseReturn struct {
	ID              uuid.UUID   `db:"id"`
	WarehouseID     uuid.UUID   `db:"warehouse_id"`
	DocNo           string      `db:"doc_no"`
	SupplierPartyID uuid.UUID   `db:"supplier_party_id"`
	SupplierName    string      `db:"supplier_name"`
	ReceiptID       uuid.UUID   `db:"receipt_id"`
	ReasonID        uuid.UUID   `db:"reason_id"`
	Settlement      string      `db:"settlement"`
	BusinessDate    time.Time   `db:"business_date"`
	TotalAmount     money.Money `db:"total_amount"`
	Status          string      `db:"status"`
	IdempotencyKey  string      `db:"idempotency_key"`
	PostedAt        time.Time   `db:"posted_at"`
	PostedBy        uuid.UUID   `db:"posted_by"`
	ReversalOf      *uuid.UUID  `db:"reversal_of"`
	CreatedAt       time.Time   `db:"created_at"`
}

// PurchaseReturnLine is a returned quantity of one receipt line, from exactly its lot.
type PurchaseReturnLine struct {
	ID               uuid.UUID    `db:"id"`
	PurchaseReturnID uuid.UUID    `db:"purchase_return_id"`
	ReceiptLineID    uuid.UUID    `db:"receipt_line_id"`
	LotID            uuid.UUID    `db:"lot_id"`
	SkuID            uuid.UUID    `db:"sku_id"`
	SkuCodeSnap      string       `db:"sku_code_snap"`
	NameSnap         string       `db:"name_snap"`
	VariantLabelSnap string       `db:"variant_label_snap"`
	UnitID           uuid.UUID    `db:"unit_id"`
	UnitNameSnap     string       `db:"unit_name_snap"`
	UnitFactorSnap   money.Factor `db:"unit_factor_snap"`
	Qty              money.Qty    `db:"qty"`
	BaseQty          money.Qty    `db:"base_qty"`
	ColorID          *uuid.UUID   `db:"color_id"`
	SpecText         *string      `db:"spec_text"`
	UnitCost         money.Money  `db:"unit_cost"`
	LineAmount       money.Money  `db:"line_amount"`
	// LotNo is the lot number of the receipt line the goods went back from (read only).
	LotNo string `db:"lot_no"`
}

// PriceAdjustment is a DG header.
type PriceAdjustment struct {
	ID              uuid.UUID   `db:"id"`
	DocNo           string      `db:"doc_no"`
	SupplierPartyID uuid.UUID   `db:"supplier_party_id"`
	SupplierName    string      `db:"supplier_name"`
	ReceiptID       uuid.UUID   `db:"receipt_id"`
	Kind            string      `db:"kind"`
	ReasonID        uuid.UUID   `db:"reason_id"`
	BusinessDate    time.Time   `db:"business_date"`
	PayableDelta    money.Money `db:"payable_delta"`
	Status          string      `db:"status"`
	IdempotencyKey  string      `db:"idempotency_key"`
	PostedAt        time.Time   `db:"posted_at"`
	PostedBy        uuid.UUID   `db:"posted_by"`
	ReversalOf      *uuid.UUID  `db:"reversal_of"`
	CreatedAt       time.Time   `db:"created_at"`
}

// PriceAdjustmentLine is the new unit price of one receipt line. BaseQty is the quantity the
// delta was applied to (received minus returned at posting); OnHandAmount + SoldAmount is the
// part of the delta the line's cost took (all of it unless the cost reached zero).
type PriceAdjustmentLine struct {
	ReceiptLineID uuid.UUID   `db:"receipt_line_id"`
	OldUnitPrice  money.Money `db:"old_unit_price"`
	NewUnitPrice  money.Money `db:"new_unit_price"`
	BaseQty       money.Qty   `db:"base_qty"`
	OnHandAmount  money.Money `db:"on_hand_amount"`
	SoldAmount    money.Money `db:"sold_amount"`
}
