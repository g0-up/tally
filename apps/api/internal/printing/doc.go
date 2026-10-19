// Package printing serves the print payloads of every printable document: one registry maps a
// document type to the provider that reads it, and the service adds the store header, party
// details and color names every printed copy shares. A sent version prints from its frozen
// snapshot, so printing it again shows the same numbers.
package printing

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Label marks every printed document as an internal record: Tally issues no e-invoices.
const Label = "Chứng từ nội bộ — không phải hóa đơn điện tử"

// Document types beyond the sales ones, which keep the names sales gave them.
const (
	DocPurchaseOrder   = "purchase_order"
	DocGoodsReceipt    = "goods_receipt"
	DocPurchaseReturn  = "purchase_return"
	DocStockCount      = "stock_count"
	DocStockAdjustment = "stock_adjustment"
	DocStatement       = "statement"
	DocKitLabel        = "kit_label"
)

// Doc is the data a printed document is rendered from. Dates are YYYY-MM-DD. The fields after
// PrintedAt are set only by the document types they describe.
type Doc struct {
	Label    string    `json:"label"`
	DocType  string    `json:"doc_type"`
	Title    string    `json:"title"`
	ID       uuid.UUID `json:"id"`
	DocNo    string    `json:"doc_no"`
	Date     string    `json:"date"`
	Status   string    `json:"status"`
	Tenant   *Tenant   `json:"tenant"`
	Customer *Party    `json:"customer"`
	Note     *string   `json:"note"`
	// Groups are the lines in order, the flavors of a mix pack together under one group.
	Groups []Group `json:"groups"`
	// Returned are the lines an exchange took back.
	Returned []Line    `json:"returned_lines,omitempty"`
	Totals   Totals    `json:"totals"`
	Payment  *Payment  `json:"payment"`
	Amounts  []Amount  `json:"amounts"`
	Related  []Related `json:"related"`
	// PrintedAt is when the data was read.
	PrintedAt time.Time `json:"printed_at"`

	// Version is the sent version printed (invoices, statements); SentAt when it was sent.
	Version *int       `json:"version,omitempty"`
	SentAt  *time.Time `json:"sent_at,omitempty"`
	// Supplier is the supplier of a purchase document.
	Supplier *Party `json:"supplier,omitempty"`
	// Statement is the frozen content of a statement version.
	Statement *Statement `json:"statement,omitempty"`
	// KitLots are the kit lots of an assembly order's label.
	KitLots []KitLot `json:"kit_lots,omitempty"`
}

// Tenant is the store header; nil under a role that cannot read the tenant (support).
type Tenant struct {
	Name    string  `db:"display_name" json:"name"`
	Legal   string  `db:"legal_name" json:"legal_name"`
	TaxCode *string `db:"tax_code" json:"tax_code"`
	Address *string `db:"address" json:"address"`
	Phone   *string `db:"phone" json:"phone"`
}

// Party is the customer or supplier of a printed document. A provider sets the ID alone; the
// service reads the rest.
type Party struct {
	ID      uuid.UUID `db:"id" json:"id"`
	Name    string    `db:"name" json:"name"`
	Phone   *string   `db:"phone" json:"phone"`
	TaxCode *string   `db:"tax_code" json:"tax_code"`
	Address *string   `db:"address" json:"address"`
}

// Group is one line, or the lines of one mix pack.
type Group struct {
	MixKey *string `json:"mix_key"`
	Lines  []Line  `json:"lines"`
}

// Line is a printed line. Purchase documents keep their tax per document, not per line: their
// lines carry the tax rate with a zero tax amount, and Totals.Tax is the document's tax.
type Line struct {
	LineNo       int           `json:"line_no"`
	Kind         string        `json:"line_kind"`
	SkuCode      *string       `json:"sku_code"`
	Name         string        `json:"name"`
	VariantLabel string        `json:"variant_label"`
	Color        *string       `json:"color"`
	SpecText     *string       `json:"spec_text"`
	UnitName     string        `json:"unit_name"`
	Qty          money.Qty     `json:"qty"`
	UnitPrice    money.Money   `json:"unit_price"`
	Discount     money.Money   `json:"discount_amount"`
	Tax          money.Money   `json:"tax_amount"`
	LineAmount   money.Money   `json:"line_amount"`
	TaxRate      money.Percent `json:"tax_rate"`
	// ColorID is the color the service names when Color is unset.
	ColorID *uuid.UUID `json:"-"`

	// LotNo and ExpiryDate are the lot of a received, returned, counted or adjusted line.
	LotNo      *string `json:"lot_no,omitempty"`
	ExpiryDate *string `json:"expiry_date,omitempty"`
	// SystemQty and CountedQty are what a count line expected and found; Qty is the variance.
	SystemQty  *money.Qty `json:"system_qty,omitempty"`
	CountedQty *money.Qty `json:"counted_qty,omitempty"`
}

// Totals are the document amounts.
type Totals struct {
	Goods    money.Money `json:"goods_amount"`
	Discount money.Money `json:"discount_amount"`
	Tax      money.Money `json:"tax_amount"`
	Total    money.Money `json:"total_amount"`
}

// ZeroTotals are the totals of a document without amounts.
var ZeroTotals = Totals{Goods: money.ZeroMoney, Discount: money.ZeroMoney, Tax: money.ZeroMoney, Total: money.ZeroMoney}

// Payment is what was paid toward a sales document.
type Payment struct {
	Total   money.Money `json:"total_amount"`
	Paid    money.Money `json:"paid_amount"`
	Open    money.Money `json:"open_amount"`
	Pending money.Money `json:"pending_amount"`
	Code    string      `json:"code"`
	Label   string      `json:"label"`
}

// Amount is a labelled amount below the totals.
type Amount struct {
	Code   string      `json:"code"`
	Label  string      `json:"label"`
	Amount money.Money `json:"amount"`
}

// Related is a document the printed one refers to.
type Related struct {
	DocType string    `json:"doc_type"`
	ID      uuid.UUID `json:"id"`
	DocNo   string    `json:"doc_no"`
}

// Statement is the frozen content of a statement version: the party's balance over the period
// line by line.
type Statement struct {
	Direction      string          `json:"direction"`
	PeriodFrom     string          `json:"period_from"`
	PeriodTo       string          `json:"period_to"`
	OpeningBalance money.Money     `json:"opening_balance"`
	ClosingBalance money.Money     `json:"closing_balance"`
	Lines          []StatementLine `json:"lines"`
}

// StatementLine is a document that raised the balance or a settlement that lowered it.
type StatementLine struct {
	Date     string      `json:"date"`
	Kind     string      `json:"kind"`
	DocNo    string      `json:"doc_no"`
	DueDate  *string     `json:"due_date,omitempty"`
	Increase money.Money `json:"increase"`
	Decrease money.Money `json:"decrease"`
	Balance  money.Money `json:"balance"`
}

// KitLot is one kit lot on a label: its expiry and what one kit of it holds.
type KitLot struct {
	LotNo      string         `json:"lot_no"`
	ExpiryDate *string        `json:"expiry_date"`
	Qty        money.Qty      `json:"qty"`
	SkuCode    string         `json:"sku_code"`
	Name       string         `json:"name"`
	Components []KitComponent `json:"components"`
}

// KitComponent is one component lot of a kit lot.
type KitComponent struct {
	SkuCode    string    `json:"sku_code"`
	Name       string    `json:"name"`
	LotNo      string    `json:"lot_no"`
	Color      *string   `json:"color"`
	ExpiryDate *string   `json:"expiry_date"`
	QtyPerKit  money.Qty `json:"qty_per_kit"`
}

// DateString formats a calendar date as YYYY-MM-DD.
func DateString(t time.Time) string { return t.Format(time.DateOnly) }

// DatePtr formats an optional calendar date.
func DatePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := DateString(*t)
	return &s
}
