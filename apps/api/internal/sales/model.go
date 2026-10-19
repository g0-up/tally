// Package sales owns quotes, invoices (sales orders), stock outs, sales documents, the POS,
// returns and exchanges. Every document that moves stock or money posts through
// posting.Coordinator and reaches the other ledgers through the ports in ports.go.
package sales

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Line kinds (the line_kind column of every sales line table).
const (
	KindItem           = "item"
	KindMixPack        = "mix_pack"
	KindMixFlavor      = "mix_flavor"
	KindCombo          = "combo"
	KindComboComponent = "combo_component"
	KindGift           = "gift"
)

// priced reports the kinds whose line_amount counts in the document total. A mix pack header
// repeats the sum of its flavors and a combo component is priced on its combo line.
func priced(kind string) bool { return kind != KindMixPack && kind != KindComboComponent }

// stocked reports the kinds that move stock: header lines carry none.
func stocked(kind string) bool { return kind != KindMixPack && kind != KindCombo }

// Quote statuses; "expired" is derived from valid_until, never stored.
const (
	QuoteDraft     = "draft"
	QuoteSent      = "sent"
	QuoteAccepted  = "accepted"
	QuoteRejected  = "rejected"
	QuoteConverted = "converted"
	QuoteExpired   = "expired"
)

// Invoice statuses.
const (
	InvoiceDraft              = "draft"
	InvoiceConfirmed          = "confirmed"
	InvoicePartiallyDelivered = "partially_delivered"
	InvoiceCompleted          = "completed"
	InvoiceCancelledRemaining = "cancelled_remaining"
)

// Sales document sources and statuses.
const (
	SourceStockOut   = "stock_out"
	SourcePOS        = "pos"
	SourcePOSOffline = "pos_offline"
	SourceExchange   = "exchange"

	DocPosted            = "posted"
	DocReversed          = "reversed"
	DocPartiallyReversed = "partially_reversed"
)

// Line is a sales line as stored in quote_lines, invoice_lines, sales_document_lines and
// pos_cart_lines (the shared goods snapshot and pricing columns).
type Line struct {
	ID           uuid.UUID     `db:"id" json:"id"`
	LineNo       int           `db:"line_no" json:"line_no"`
	SkuID        *uuid.UUID    `db:"sku_id" json:"sku_id"`
	SkuCode      *string       `db:"sku_code_snap" json:"sku_code"`
	Name         string        `db:"name_snap" json:"name"`
	VariantLabel string        `db:"variant_label_snap" json:"variant_label"`
	UnitID       uuid.UUID     `db:"unit_id" json:"unit_id"`
	UnitName     string        `db:"unit_name_snap" json:"unit_name"`
	UnitFactor   money.Factor  `db:"unit_factor_snap" json:"unit_factor"`
	Qty          money.Qty     `db:"qty" json:"qty"`
	BaseQty      money.Qty     `db:"base_qty" json:"base_qty"`
	ColorID      *uuid.UUID    `db:"color_id" json:"color_id"`
	SpecText     *string       `db:"spec_text" json:"spec_text"`
	Kind         string        `db:"line_kind" json:"line_kind"`
	ParentLineID *uuid.UUID    `db:"parent_line_id" json:"parent_line_id"`
	MixKey       *string       `db:"mix_key" json:"mix_key"`
	MixGroupID   *uuid.UUID    `db:"mix_group_id" json:"mix_group_id"`
	UnitPrice    money.Money   `db:"unit_price" json:"unit_price"`
	Discount     money.Money   `db:"discount_amount" json:"discount_amount"`
	OrderAlloc   money.Money   `db:"order_discount_alloc" json:"order_discount_alloc"`
	TaxRate      money.Percent `db:"tax_rate_snap" json:"tax_rate"`
	Tax          money.Money   `db:"tax_amount" json:"tax_amount"`
	LineAmount   money.Money   `db:"line_amount" json:"line_amount"`
	MinPrice     *money.Money  `db:"min_price_snap" json:"min_price"`
	PriceSource  RawJSON       `db:"price_source" json:"price_source"`
}

// net is the line amount after every discount, before tax is added: the line amount itself when
// prices include tax.
func (l Line) net(includeTax bool) money.Money {
	if includeTax {
		return l.LineAmount
	}
	return l.LineAmount.Sub(l.Tax)
}

// gross is the line's amount before discounts, as the pricing rounding left it consistent:
// net plus the line and order discounts.
func (l Line) gross(includeTax bool) money.Money {
	return l.net(includeTax).Add(l.Discount).Add(l.OrderAlloc)
}

// belowMin reports a net price per base unit under the minimum price snapshot.
func (l Line) belowMin(includeTax bool) bool {
	if l.MinPrice == nil || !l.BaseQty.IsPositive() || !priced(l.Kind) || l.Kind == KindGift {
		return false
	}
	// net < min × base_qty, compared exactly.
	return l.net(includeTax).Decimal().Cmp(l.MinPrice.Decimal().Mul(l.BaseQty.Decimal())) < 0
}

// RawJSON is a jsonb column passed through as JSON.
type RawJSON json.RawMessage

// MarshalJSON implements json.Marshaler.
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("{}"), nil
	}
	return r, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *RawJSON) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// Scan implements sql.Scanner.
func (r *RawJSON) Scan(src any) error {
	switch v := src.(type) {
	case []byte:
		*r = append((*r)[:0], v...)
	case string:
		*r = RawJSON(v)
	case nil:
		*r = nil
	}
	return nil
}

// Totals are the money totals of a document.
type Totals struct {
	Goods    money.Money `db:"goods_amount" json:"goods_amount"`
	Discount money.Money `db:"discount_amount" json:"discount_amount"`
	Tax      money.Money `db:"tax_amount" json:"tax_amount"`
	Total    money.Money `db:"total_amount" json:"total_amount"`
}

// totalsOf sums the priced lines.
func totalsOf(lines []Line, includeTax bool) Totals {
	var t Totals
	for _, l := range lines {
		if !priced(l.Kind) {
			continue
		}
		t.Goods = t.Goods.Add(l.gross(includeTax))
		t.Discount = t.Discount.Add(l.Discount).Add(l.OrderAlloc)
		t.Tax = t.Tax.Add(l.Tax)
		t.Total = t.Total.Add(l.LineAmount)
	}
	return t
}

// Quote is a BG.
type Quote struct {
	ID                 uuid.UUID  `db:"id" json:"id"`
	DocNo              string     `db:"doc_no" json:"doc_no"`
	CustomerID         uuid.UUID  `db:"customer_party_id" json:"customer_party_id"`
	QuoteDate          Date       `db:"quote_date" json:"quote_date"`
	ValidUntil         Date       `db:"valid_until" json:"valid_until"`
	StoredStatus       string     `db:"status" json:"-"`
	Status             string     `db:"-" json:"status"`
	CampaignID         *uuid.UUID `db:"campaign_id" json:"campaign_id"`
	Note               *string    `db:"note" json:"note"`
	ConvertedInvoiceID *uuid.UUID `db:"converted_invoice_id" json:"converted_invoice_id"`
	CopiedFromID       *uuid.UUID `db:"copied_from_id" json:"copied_from_id"`
	RevisionNo         int        `db:"revision_no" json:"revision_no"`
	CreatedAt          time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at" json:"updated_at"`
	Version            int        `db:"version" json:"version"`
	Totals
	Lines []QuoteLine `db:"-" json:"lines,omitempty"`
}

// QuoteLine is a quote line with the retail price shown next to the quoted one ("Giá bán lẻ").
type QuoteLine struct {
	Line
	RetailPrice *money.Money `db:"-" json:"retail_price"`
}

// Invoice is an SO, the internal sales order.
type Invoice struct {
	ID             uuid.UUID   `db:"id" json:"id"`
	DocNo          string      `db:"doc_no" json:"doc_no"`
	CustomerID     uuid.UUID   `db:"customer_party_id" json:"customer_party_id"`
	PayerID        uuid.UUID   `db:"payer_party_id" json:"payer_party_id"`
	InvoiceDate    Date        `db:"invoice_date" json:"invoice_date"`
	DueDate        *Date       `db:"due_date" json:"due_date"`
	Status         string      `db:"status" json:"status"`
	Delivered      money.Money `db:"delivered_amount" json:"delivered_amount"`
	QuoteID        *uuid.UUID  `db:"quote_id" json:"quote_id"`
	CampaignID     *uuid.UUID  `db:"campaign_id" json:"campaign_id"`
	CopiedFromID   *uuid.UUID  `db:"copied_from_id" json:"copied_from_id"`
	Note           *string     `db:"note" json:"note"`
	CurrentVersion int         `db:"current_version" json:"current_version"`
	ConfirmedAt    *time.Time  `db:"confirmed_at" json:"confirmed_at"`
	ConfirmedBy    *uuid.UUID  `db:"confirmed_by" json:"confirmed_by"`
	CreatedAt      time.Time   `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time   `db:"updated_at" json:"updated_at"`
	Version        int         `db:"version" json:"version"`
	Totals
	Lines   []InvoiceLine `db:"-" json:"lines,omitempty"`
	Payment *PaymentState `db:"-" json:"payment,omitempty"`
	// PaidAmount is Payment.Paid, flat for the invoice list.
	PaidAmount *money.Money `db:"-" json:"paid_amount,omitempty"`
}

// setPayment attaches the payment state of an invoice.
func (inv *Invoice) setPayment(st PaymentState) {
	inv.Payment, inv.PaidAmount = &st, &st.Paid
}

// InvoiceLine is an invoice line with its fulfilment quantities (base units).
type InvoiceLine struct {
	Line
	Reserved  money.Qty `db:"reserved_base_qty" json:"reserved_base_qty"`
	Backorder money.Qty `db:"backorder_base_qty" json:"backorder_base_qty"`
	Delivered money.Qty `db:"delivered_base_qty" json:"delivered_base_qty"`
	Cancelled money.Qty `db:"cancelled_base_qty" json:"cancelled_base_qty"`
}

// remaining is what is still to deliver.
func (l InvoiceLine) remaining() money.Qty { return l.BaseQty.Sub(l.Delivered).Sub(l.Cancelled) }

// InvoiceVersion is a confirmed state of an invoice.
type InvoiceVersion struct {
	ID             uuid.UUID `db:"id" json:"id"`
	VersionNo      int       `db:"version_no" json:"version_no"`
	Snapshot       RawJSON   `db:"snapshot" json:"snapshot"`
	ChangeSummary  *string   `db:"change_summary" json:"change_summary"`
	MaterialChange bool      `db:"material_change" json:"material_change"`
	CreatedBy      uuid.UUID `db:"created_by" json:"created_by"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
}

// StockOut is a PX.
type StockOut struct {
	ID           uuid.UUID      `db:"id" json:"id"`
	DocNo        string         `db:"doc_no" json:"doc_no"`
	InvoiceID    uuid.UUID      `db:"invoice_id" json:"invoice_id"`
	BusinessDate Date           `db:"business_date" json:"business_date"`
	Note         *string        `db:"note" json:"note"`
	Status       string         `db:"status" json:"status"`
	PostedAt     time.Time      `db:"posted_at" json:"posted_at"`
	PostedBy     uuid.UUID      `db:"posted_by" json:"posted_by"`
	ReversalOf   *uuid.UUID     `db:"reversal_of" json:"reversal_of"`
	Lines        []StockOutLine `db:"-" json:"lines,omitempty"`
	// SalesDocumentID is the BH the stock out posted.
	SalesDocumentID *uuid.UUID `db:"sales_document_id" json:"sales_document_id"`
}

// StockOutLine is the base quantity of one invoice line a stock out delivered.
type StockOutLine struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	InvoiceLineID uuid.UUID  `db:"invoice_line_id" json:"invoice_line_id"`
	SkuID         uuid.UUID  `db:"sku_id" json:"sku_id"`
	ColorID       *uuid.UUID `db:"color_id" json:"color_id"`
	BaseQty       money.Qty  `db:"base_qty" json:"base_qty"`
}

// SalesDocument is a BH (or HD at the POS): the revenue and receivable event. The name is the
// design's term for the record, so the package-name stutter is intended.
type SalesDocument struct { //nolint:revive // see above
	ID               uuid.UUID   `db:"id" json:"id"`
	DocNo            string      `db:"doc_no" json:"doc_no"`
	Source           string      `db:"source" json:"source"`
	StockOutID       *uuid.UUID  `db:"stock_out_id" json:"stock_out_id"`
	InvoiceID        *uuid.UUID  `db:"invoice_id" json:"invoice_id"`
	CartID           *uuid.UUID  `db:"pos_cart_id" json:"pos_cart_id"`
	ShiftID          *uuid.UUID  `db:"shift_id" json:"shift_id"`
	CustomerID       *uuid.UUID  `db:"customer_party_id" json:"customer_party_id"`
	PayerID          *uuid.UUID  `db:"payer_party_id" json:"payer_party_id"`
	BusinessDate     Date        `db:"business_date" json:"business_date"`
	Cogs             money.Money `db:"cogs_amount" json:"cogs_amount"`
	CampaignID       *uuid.UUID  `db:"campaign_id" json:"campaign_id"`
	OfflineClientID  *string     `db:"offline_client_id" json:"offline_client_id"`
	OfflineCreatedAt *time.Time  `db:"offline_created_at" json:"offline_created_at"`
	Status           string      `db:"status" json:"status"`
	PostedAt         time.Time   `db:"posted_at" json:"posted_at"`
	PostedBy         uuid.UUID   `db:"posted_by" json:"posted_by"`
	ReversalOf       *uuid.UUID  `db:"reversal_of" json:"reversal_of"`
	CashTendered     money.Money `db:"cash_tendered_amount" json:"cash_tendered_amount"`
	Change           money.Money `db:"change_amount" json:"change_amount"`
	Totals
	Lines []DocumentLine `db:"-" json:"lines,omitempty"`
}

// DocumentLine is a sales document line with its cost and what was returned of it.
type DocumentLine struct {
	Line
	InvoiceLineID  *uuid.UUID  `db:"invoice_line_id" json:"invoice_line_id"`
	StockOutLineID *uuid.UUID  `db:"stock_out_line_id" json:"stock_out_line_id"`
	Cogs           money.Money `db:"cogs_amount" json:"cogs_amount"`
	Returned       money.Qty   `db:"returned_base_qty" json:"returned_base_qty"`
	CampaignID     *uuid.UUID  `db:"campaign_id" json:"campaign_id"`
}

// Shift is a CA, opened by the first POS sale of a business date.
type Shift struct {
	ID           uuid.UUID  `db:"id" json:"id"`
	DocNo        string     `db:"doc_no" json:"doc_no"`
	BusinessDate Date       `db:"business_date" json:"business_date"`
	OpenedAt     time.Time  `db:"opened_at" json:"opened_at"`
	ClosedAt     *time.Time `db:"closed_at" json:"closed_at"`
	Status       string     `db:"status" json:"status"`
}

// Cart is a saved POS cart (GH): no reservation, no posting.
type Cart struct {
	ID              uuid.UUID   `db:"id" json:"id"`
	DocNo           string      `db:"doc_no" json:"doc_no"`
	CustomerID      *uuid.UUID  `db:"customer_party_id" json:"customer_party_id"`
	Label           *string     `db:"label" json:"label"`
	Status          string      `db:"status" json:"status"`
	Total           money.Money `db:"total_amount" json:"total_amount"`
	SalesDocumentID *uuid.UUID  `db:"sales_document_id" json:"sales_document_id"`
	CopiedFromID    *uuid.UUID  `db:"copied_from_id" json:"copied_from_id"`
	CreatedAt       time.Time   `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time   `db:"updated_at" json:"updated_at"`
	Version         int         `db:"version" json:"version"`
	Lines           []Line      `db:"-" json:"lines,omitempty"`
}

// SalesReturn is a TH. The name is the design's term for the record, so the package-name stutter
// is intended.
type SalesReturn struct { //nolint:revive // see above
	ID              uuid.UUID    `db:"id" json:"id"`
	DocNo           string       `db:"doc_no" json:"doc_no"`
	CustomerID      *uuid.UUID   `db:"customer_party_id" json:"customer_party_id"`
	SalesDocumentID *uuid.UUID   `db:"sales_document_id" json:"sales_document_id"`
	ReasonID        uuid.UUID    `db:"reason_id" json:"reason_id"`
	BusinessDate    Date         `db:"business_date" json:"business_date"`
	Total           money.Money  `db:"total_amount" json:"total_amount"`
	DebtReduction   money.Money  `db:"debt_reduction_amount" json:"debt_reduction_amount"`
	RefundDue       money.Money  `db:"refund_due_amount" json:"refund_due_amount"`
	ExchangeID      *uuid.UUID   `db:"exchange_id" json:"exchange_id"`
	Status          string       `db:"status" json:"status"`
	PostedAt        time.Time    `db:"posted_at" json:"posted_at"`
	PostedBy        uuid.UUID    `db:"posted_by" json:"posted_by"`
	Lines           []ReturnLine `db:"-" json:"lines,omitempty"`
}

// ReturnLine is one lot of returned goods.
type ReturnLine struct {
	ID                  uuid.UUID    `db:"id" json:"id"`
	SalesDocumentLineID *uuid.UUID   `db:"sales_document_line_id" json:"sales_document_line_id"`
	SkuID               uuid.UUID    `db:"sku_id" json:"sku_id"`
	SkuCode             string       `db:"sku_code_snap" json:"sku_code"`
	Name                string       `db:"name_snap" json:"name"`
	VariantLabel        string       `db:"variant_label_snap" json:"variant_label"`
	UnitID              uuid.UUID    `db:"unit_id" json:"unit_id"`
	UnitName            string       `db:"unit_name_snap" json:"unit_name"`
	UnitFactor          money.Factor `db:"unit_factor_snap" json:"unit_factor"`
	Qty                 money.Qty    `db:"qty" json:"qty"`
	BaseQty             money.Qty    `db:"base_qty" json:"base_qty"`
	ColorID             *uuid.UUID   `db:"color_id" json:"color_id"`
	SpecText            *string      `db:"spec_text" json:"spec_text"`
	UnitPrice           money.Money  `db:"unit_price" json:"unit_price"`
	LineAmount          money.Money  `db:"line_amount" json:"line_amount"`
	UnitCost            money.Money  `db:"unit_cost" json:"unit_cost"`
	CostSource          string       `db:"cost_source" json:"cost_source"`
	RestockTo           string       `db:"restock_to" json:"restock_to"`
	LotID               uuid.UUID    `db:"lot_id" json:"lot_id"`
}

// Exchange is a DH: one return and one new sale settled together.
type Exchange struct {
	ID                 uuid.UUID   `db:"id" json:"id"`
	DocNo              string      `db:"doc_no" json:"doc_no"`
	CustomerID         *uuid.UUID  `db:"customer_party_id" json:"customer_party_id"`
	OriginalDocumentID uuid.UUID   `db:"original_sales_document_id" json:"original_sales_document_id"`
	SalesReturnID      uuid.UUID   `db:"sales_return_id" json:"sales_return_id"`
	NewDocumentID      uuid.UUID   `db:"new_sales_document_id" json:"new_sales_document_id"`
	Returned           money.Money `db:"returned_amount" json:"returned_amount"`
	Taken              money.Money `db:"taken_amount" json:"taken_amount"`
	DebtReduction      money.Money `db:"debt_reduction_amount" json:"debt_reduction_amount"`
	CustomerPays       money.Money `db:"customer_pays_amount" json:"customer_pays_amount"`
	StoreRefunds       money.Money `db:"store_refunds_amount" json:"store_refunds_amount"`
	FundAccountID      *uuid.UUID  `db:"fund_account_id" json:"fund_account_id"`
	BusinessDate       Date        `db:"business_date" json:"business_date"`
	PostedAt           time.Time   `db:"posted_at" json:"posted_at"`
	PostedBy           uuid.UUID   `db:"posted_by" json:"posted_by"`
	CreatedAt          time.Time   `db:"created_at" json:"created_at"`
}

// SyncException is an offline POS sale the server could not post.
type SyncException struct {
	ID              uuid.UUID  `db:"id" json:"id"`
	OfflineClientID string     `db:"offline_client_id" json:"offline_client_id"`
	DeviceSessionID *uuid.UUID `db:"device_session_id" json:"device_session_id"`
	Payload         RawJSON    `db:"payload" json:"payload"`
	ErrorCode       string     `db:"error_code" json:"error_code"`
	ErrorDetail     *string    `db:"error_detail" json:"error_detail"`
	Status          string     `db:"status" json:"status"`
	ResolutionDocID *uuid.UUID `db:"resolution_doc_id" json:"resolution_doc_id"`
	ResolvedBy      *uuid.UUID `db:"resolved_by" json:"resolved_by"`
	ResolvedAt      *time.Time `db:"resolved_at" json:"resolved_at"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
}
