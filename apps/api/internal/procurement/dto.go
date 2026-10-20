package procurement

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the procurement endpoints. Money, quantities and factors travel as decimal
// strings; business dates as "YYYY-MM-DD".

func mapSlice[T, D any](in []T, fn func(T) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

// date is a civil date on the wire ("YYYY-MM-DD"), held as midnight UTC.
type date time.Time

// UnmarshalJSON reads a "YYYY-MM-DD" string.
func (d *date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: reflect.TypeFor[date]()}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string " + s, Type: reflect.TypeFor[date]()}
	}
	*d = date(clock.Date(t.Year(), t.Month(), t.Day()))
	return nil
}

func (d *date) time() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

func dateOf(t time.Time) string { return t.Format(time.DateOnly) }

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateOf(*t)
	return &s
}

// Requests.

type orderLineRequest struct {
	SkuID          uuid.UUID          `json:"sku_id"`
	UnitID         *uuid.UUID         `json:"unit_id"`
	Qty            money.Qty          `json:"qty"`
	ColorID        *uuid.UUID         `json:"color_id"`
	UnitPrice      *money.Money       `json:"unit_price"`
	DiscountAmount money.Money        `json:"discount_amount"`
	RetailPrice    *money.Money       `json:"retail_price"`
	SpecText       *string            `json:"spec_text"`
	NewProduct     *newProductRequest `json:"new_product"`
}

func (l orderLineRequest) input() OrderLineInput {
	return OrderLineInput{SkuID: l.SkuID, UnitID: l.UnitID, Qty: l.Qty, ColorID: l.ColorID, UnitPrice: l.UnitPrice,
		DiscountAmount: l.DiscountAmount, RetailPrice: l.RetailPrice, SpecText: l.SpecText, NewProduct: l.NewProduct.input()}
}

// newProductRequest is a product a purchase line creates.
type newProductRequest struct {
	Name           string       `json:"name"`
	CategoryID     *uuid.UUID   `json:"category_id"`
	BaseUnitID     uuid.UUID    `json:"base_unit_id"`
	PurchaseUnitID *uuid.UUID   `json:"purchase_unit_id"`
	Factor         *string      `json:"factor"`
	TrackLot       bool         `json:"track_lot"`
	RetailPrice    *money.Money `json:"retail_price"`
	WholesalePrice *money.Money `json:"wholesale_price"`
	Barcode        *string      `json:"barcode"`
}

func (r *newProductRequest) input() *NewProduct {
	if r == nil {
		return nil
	}
	p := NewProduct(*r)
	return &p
}

type orderRequest struct {
	SupplierPartyID uuid.UUID          `json:"supplier_party_id"`
	OrderDate       *date              `json:"order_date"`
	ExpectedDate    *date              `json:"expected_date"`
	Note            *string            `json:"note"`
	Lines           []orderLineRequest `json:"lines"`
}

func (r orderRequest) input() OrderInput {
	return OrderInput{SupplierPartyID: r.SupplierPartyID, OrderDate: r.OrderDate.time(), ExpectedDate: r.ExpectedDate.time(),
		Note: r.Note, Lines: mapSlice(r.Lines, orderLineRequest.input)}
}

type orderPatchRequest struct {
	SupplierPartyID *uuid.UUID `json:"supplier_party_id"`
	OrderDate       *date      `json:"order_date"`
	ExpectedDate    *date      `json:"expected_date"`
	Note            *string    `json:"note"`
}

func (r orderPatchRequest) input() OrderPatch {
	return OrderPatch{SupplierPartyID: r.SupplierPartyID, OrderDate: r.OrderDate.time(), ExpectedDate: r.ExpectedDate.time(),
		Note: r.Note}
}

type orderLinesRequest struct {
	Lines []orderLineRequest `json:"lines"`
}

type closeShortRequest struct {
	ReasonID uuid.UUID `json:"reason_id"`
}

type installmentRequest struct {
	DueDate date        `json:"due_date"`
	Amount  money.Money `json:"amount"`
}

type advanceRequest struct {
	PaymentID uuid.UUID   `json:"payment_id"`
	Amount    money.Money `json:"amount"`
}

type receiptLineRequest struct {
	PurchaseOrderLineID *uuid.UUID         `json:"purchase_order_line_id"`
	SkuID               uuid.UUID          `json:"sku_id"`
	UnitID              *uuid.UUID         `json:"unit_id"`
	Qty                 money.Qty          `json:"qty"`
	ColorID             *uuid.UUID         `json:"color_id"`
	Condition           string             `json:"condition"`
	LotNo               string             `json:"lot_no"`
	ExpiryDate          *date              `json:"expiry_date"`
	UnitPrice           *money.Money       `json:"unit_price"`
	DiscountAmount      *money.Money       `json:"discount_amount"`
	SpecText            *string            `json:"spec_text"`
	NewProduct          *newProductRequest `json:"new_product"`
}

type receiptRequest struct {
	SupplierPartyID *uuid.UUID           `json:"supplier_party_id"`
	PurchaseOrderID *uuid.UUID           `json:"purchase_order_id"`
	BusinessDate    *date                `json:"business_date"`
	DueDate         *date                `json:"due_date"`
	Note            *string              `json:"note"`
	PaidNowAmount   money.Money          `json:"paid_now_amount"`
	PaymentMethodID *uuid.UUID           `json:"payment_method_id"`
	Schedule        []installmentRequest `json:"schedule"`
	UseAdvances     []advanceRequest     `json:"use_advances"`
	Lines           []receiptLineRequest `json:"lines"`
	CampaignID      *uuid.UUID           `json:"campaign_id"`
}

func (r receiptRequest) input() ReceiptInput {
	return ReceiptInput{
		SupplierPartyID: r.SupplierPartyID, PurchaseOrderID: r.PurchaseOrderID, BusinessDate: r.BusinessDate.time(),
		DueDate: r.DueDate.time(), Note: r.Note, PaidNowAmount: r.PaidNowAmount, PaymentMethodID: r.PaymentMethodID,
		CampaignID: r.CampaignID,
		Schedule: mapSlice(r.Schedule, func(i installmentRequest) Installment {
			return Installment{DueDate: time.Time(i.DueDate), Amount: i.Amount}
		}),
		UseAdvances: mapSlice(r.UseAdvances, func(a advanceRequest) AdvanceUse { return AdvanceUse(a) }),
		Lines: mapSlice(r.Lines, func(l receiptLineRequest) ReceiptLineInput {
			return ReceiptLineInput{PurchaseOrderLineID: l.PurchaseOrderLineID, SkuID: l.SkuID, UnitID: l.UnitID, Qty: l.Qty,
				ColorID: l.ColorID, Condition: l.Condition, LotNo: l.LotNo, ExpiryDate: l.ExpiryDate.time(), UnitPrice: l.UnitPrice,
				DiscountAmount: l.DiscountAmount, SpecText: l.SpecText, NewProduct: l.NewProduct.input()}
		}),
	}
}

type reverseRequest struct {
	Reason string `json:"reason"`
}

type landedCostRequest struct {
	CostType        string      `json:"cost_type"`
	Description     *string     `json:"description"`
	Amount          money.Money `json:"amount"`
	Method          string      `json:"method"`
	PayeePartyID    *uuid.UUID  `json:"payee_party_id"`
	DueDate         *date       `json:"due_date"`
	PaidNowAmount   money.Money `json:"paid_now_amount"`
	PaymentMethodID *uuid.UUID  `json:"payment_method_id"`
}

func (r landedCostRequest) input() LandedCostInput {
	return LandedCostInput{CostType: r.CostType, Description: r.Description, Amount: r.Amount, Method: r.Method,
		PayeePartyID: r.PayeePartyID, DueDate: r.DueDate.time(), PaidNowAmount: r.PaidNowAmount, PaymentMethodID: r.PaymentMethodID}
}

type documentRequest struct {
	SupplierPartyID uuid.UUID    `json:"supplier_party_id"`
	DocumentNo      string       `json:"document_no"`
	DocumentDate    *date        `json:"document_date"`
	GoodsAmount     money.Money  `json:"goods_amount"`
	TaxAmount       money.Money  `json:"tax_amount"`
	TotalAmount     *money.Money `json:"total_amount"`
	Note            *string      `json:"note"`
	ReceiptIDs      []uuid.UUID  `json:"receipt_ids"`
}

func (r documentRequest) input() DocumentInput {
	return DocumentInput{SupplierPartyID: r.SupplierPartyID, DocumentNo: r.DocumentNo, DocumentDate: r.DocumentDate.time(),
		GoodsAmount: r.GoodsAmount, TaxAmount: r.TaxAmount, TotalAmount: r.TotalAmount, Note: r.Note, ReceiptIDs: r.ReceiptIDs}
}

type matchRequest struct {
	ReceiptIDs []uuid.UUID `json:"receipt_ids"`
}

type returnLineRequest struct {
	ReceiptLineID uuid.UUID  `json:"receipt_line_id"`
	UnitID        *uuid.UUID `json:"unit_id"`
	Qty           money.Qty  `json:"qty"`
}

type returnRequest struct {
	ReceiptID    uuid.UUID           `json:"receipt_id"`
	ReasonID     uuid.UUID           `json:"reason_id"`
	Settlement   string              `json:"settlement"`
	BusinessDate *date               `json:"business_date"`
	Lines        []returnLineRequest `json:"lines"`
}

func (r returnRequest) input() ReturnInput {
	return ReturnInput{ReceiptID: r.ReceiptID, ReasonID: r.ReasonID, Settlement: r.Settlement, BusinessDate: r.BusinessDate.time(),
		Lines: mapSlice(r.Lines, func(l returnLineRequest) ReturnLineInput { return ReturnLineInput(l) })}
}

type adjustmentLineRequest struct {
	ReceiptLineID uuid.UUID   `json:"receipt_line_id"`
	NewUnitPrice  money.Money `json:"new_unit_price"`
}

type adjustmentRequest struct {
	ReceiptID    uuid.UUID               `json:"receipt_id"`
	Kind         string                  `json:"kind"`
	ReasonID     uuid.UUID               `json:"reason_id"`
	BusinessDate *date                   `json:"business_date"`
	Lines        []adjustmentLineRequest `json:"lines"`
}

func (r adjustmentRequest) input() AdjustmentInput {
	return AdjustmentInput{ReceiptID: r.ReceiptID, Kind: r.Kind, ReasonID: r.ReasonID, BusinessDate: r.BusinessDate.time(),
		Lines: mapSlice(r.Lines, func(l adjustmentLineRequest) AdjustmentLineInput { return AdjustmentLineInput(l) })}
}

// Responses.

type orderDTO struct {
	ID              uuid.UUID   `json:"id"`
	DocNo           string      `json:"doc_no"`
	SupplierPartyID uuid.UUID   `json:"supplier_party_id"`
	SupplierName    string      `json:"supplier_name"`
	OrderDate       string      `json:"order_date"`
	ExpectedDate    *string     `json:"expected_date"`
	Status          string      `json:"status"`
	GoodsAmount     money.Money `json:"goods_amount"`
	DiscountAmount  money.Money `json:"discount_amount"`
	TaxAmount       money.Money `json:"tax_amount"`
	TotalAmount     money.Money `json:"total_amount"`
	Note            *string     `json:"note"`
	CloseReasonID   *uuid.UUID  `json:"close_reason_id"`
	ClosedAt        *time.Time  `json:"closed_at"`
	ConfirmedAt     *time.Time  `json:"confirmed_at"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	Version         int         `json:"version"`
	// Progress of the lines in base units (lists and late orders).
	OrderedBaseQty  *money.Qty     `json:"ordered_base_qty,omitempty"`
	ReceivedBaseQty *money.Qty     `json:"received_base_qty,omitempty"`
	PendingBaseQty  *money.Qty     `json:"pending_base_qty,omitempty"`
	DaysLate        *int           `json:"days_late,omitempty"`
	Lines           []orderLineDTO `json:"lines,omitempty"`
}

type orderLineDTO struct {
	ID              uuid.UUID     `json:"id"`
	LineNo          int           `json:"line_no"`
	SkuID           uuid.UUID     `json:"sku_id"`
	SkuCode         string        `json:"sku_code"`
	Name            string        `json:"name"`
	VariantLabel    string        `json:"variant_label"`
	UnitID          uuid.UUID     `json:"unit_id"`
	UnitName        string        `json:"unit_name"`
	UnitFactor      money.Factor  `json:"unit_factor"`
	Qty             money.Qty     `json:"qty"`
	BaseQty         money.Qty     `json:"base_qty"`
	ColorID         *uuid.UUID    `json:"color_id"`
	SpecText        *string       `json:"spec_text"`
	UnitPrice       money.Money   `json:"unit_price"`
	DiscountAmount  money.Money   `json:"discount_amount"`
	TaxRate         money.Percent `json:"tax_rate"`
	LineAmount      money.Money   `json:"line_amount"`
	RetailPrice     *money.Money  `json:"retail_price"`
	ReceivedBaseQty money.Qty     `json:"received_base_qty"`
	ReturnedBaseQty money.Qty     `json:"returned_base_qty"`
	PendingBaseQty  money.Qty     `json:"pending_base_qty"`
}

func toOrderDTO(o PurchaseOrder) orderDTO {
	return orderDTO{ID: o.ID, DocNo: o.DocNo, SupplierPartyID: o.SupplierPartyID, SupplierName: o.SupplierName,
		OrderDate: dateOf(o.OrderDate), ExpectedDate: dateString(o.ExpectedDate), Status: o.Status, GoodsAmount: o.GoodsAmount,
		DiscountAmount: o.DiscountAmount, TaxAmount: o.TaxAmount, TotalAmount: o.TotalAmount, Note: o.Note,
		CloseReasonID: o.CloseReasonID, ClosedAt: o.ClosedAt, ConfirmedAt: o.ConfirmedAt, CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt, Version: o.Version}
}

func toOrderRowDTO(r OrderRow) orderDTO {
	d := toOrderDTO(r.PurchaseOrder)
	d.OrderedBaseQty, d.ReceivedBaseQty, d.PendingBaseQty = &r.OrderedBaseQty, &r.ReceivedBaseQty, &r.PendingBaseQty
	return d
}

func toLateOrderDTO(r LateOrder) orderDTO {
	d := toOrderRowDTO(r.OrderRow)
	d.DaysLate = &r.DaysLate
	return d
}

func toOrderDetailDTO(o OrderDetail) orderDTO {
	d := toOrderDTO(o.PurchaseOrder)
	d.Lines = mapSlice(o.Lines, toOrderLineDTO)
	return d
}

func toOrderLineDTO(l PurchaseOrderLine) orderLineDTO {
	return orderLineDTO{ID: l.ID, LineNo: l.LineNo, SkuID: l.SkuID, SkuCode: l.SkuCodeSnap, Name: l.NameSnap,
		VariantLabel: l.VariantLabelSnap, UnitID: l.UnitID, UnitName: l.UnitNameSnap, UnitFactor: l.UnitFactorSnap, Qty: l.Qty,
		BaseQty: l.BaseQty, ColorID: l.ColorID, SpecText: l.SpecText, UnitPrice: l.UnitPrice, DiscountAmount: l.DiscountAmount,
		TaxRate: l.TaxRateSnap, LineAmount: l.LineAmount, RetailPrice: l.RetailPriceSnap, ReceivedBaseQty: l.ReceivedBaseQty,
		ReturnedBaseQty: l.ReturnedBaseQty, PendingBaseQty: l.PendingBaseQty()}
}

type receiptDTO struct {
	ID               uuid.UUID        `json:"id"`
	DocNo            string           `json:"doc_no"`
	WarehouseID      uuid.UUID        `json:"warehouse_id"`
	SupplierPartyID  uuid.UUID        `json:"supplier_party_id"`
	SupplierName     string           `json:"supplier_name"`
	PurchaseOrderID  *uuid.UUID       `json:"purchase_order_id"`
	OrderDocNo       *string          `json:"purchase_order_doc_no,omitempty"`
	BusinessDate     string           `json:"business_date"`
	GoodsAmount      money.Money      `json:"goods_amount"`
	DiscountAmount   money.Money      `json:"discount_amount"`
	TaxAmount        money.Money      `json:"tax_amount"`
	LandedCostAmount money.Money      `json:"landed_cost_amount"`
	TotalAmount      money.Money      `json:"total_amount"`
	PaidNowAmount    money.Money      `json:"paid_now_amount"`
	DueDate          *string          `json:"due_date"`
	Status           string           `json:"status"`
	Note             *string          `json:"note"`
	ReversalOf       *uuid.UUID       `json:"reversal_of"`
	ReversalDocNo    *string          `json:"reversal_doc_no,omitempty"`
	PostedAt         time.Time        `json:"posted_at"`
	PostedBy         uuid.UUID        `json:"posted_by"`
	Lines            []receiptLineDTO `json:"lines,omitempty"`
	Payable          *payableDTO      `json:"payable,omitempty"`
	Payments         []paymentInfoDTO `json:"payments,omitempty"`
	LandedCosts      []landedCostDTO  `json:"landed_costs,omitempty"`
	Attachments      []attachmentDTO  `json:"attachments,omitempty"`
}

type receiptLineDTO struct {
	ID                  uuid.UUID     `json:"id"`
	LineNo              int           `json:"line_no"`
	PurchaseOrderLineID *uuid.UUID    `json:"purchase_order_line_id"`
	SkuID               uuid.UUID     `json:"sku_id"`
	SkuCode             string        `json:"sku_code"`
	Name                string        `json:"name"`
	VariantLabel        string        `json:"variant_label"`
	UnitID              uuid.UUID     `json:"unit_id"`
	UnitName            string        `json:"unit_name"`
	UnitFactor          money.Factor  `json:"unit_factor"`
	Qty                 money.Qty     `json:"qty"`
	BaseQty             money.Qty     `json:"base_qty"`
	ColorID             *uuid.UUID    `json:"color_id"`
	SpecText            *string       `json:"spec_text"`
	Condition           string        `json:"condition"`
	LotNo               string        `json:"lot_no"`
	ExpiryDate          *string       `json:"expiry_date"`
	LotID               *uuid.UUID    `json:"lot_id"`
	UnitPrice           money.Money   `json:"unit_price"`
	DiscountAmount      money.Money   `json:"discount_amount"`
	TaxRate             money.Percent `json:"tax_rate"`
	LineAmount          money.Money   `json:"line_amount"`
	LandedCostAmount    money.Money   `json:"landed_cost_amount"`
	UnitCost            money.Money   `json:"unit_cost"`
	ReturnedBaseQty     money.Qty     `json:"returned_base_qty"`
	ReturnableBaseQty   money.Qty     `json:"returnable_base_qty"`
}

type payableDTO struct {
	ID            uuid.UUID        `json:"id"`
	Amount        money.Money      `json:"amount"`
	SettledAmount money.Money      `json:"settled_amount"`
	OpenAmount    money.Money      `json:"open_amount"`
	DueDate       string           `json:"due_date"`
	Status        string           `json:"status"`
	ReversedOn    *string          `json:"reversed_on"`
	Schedule      []installmentDTO `json:"schedule"`
}

type installmentDTO struct {
	DueDate string      `json:"due_date"`
	Amount  money.Money `json:"amount"`
}

type paymentInfoDTO struct {
	ID           uuid.UUID   `json:"id"`
	DocNo        string      `json:"doc_no"`
	Amount       money.Money `json:"amount"`
	BusinessDate string      `json:"business_date"`
	Status       string      `json:"status"`
}

type attachmentDTO struct {
	ID          uuid.UUID `json:"id"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	ScanStatus  string    `json:"scan_status"`
	CreatedAt   time.Time `json:"created_at"`
}

func toAttachmentDTO(a Attachment) attachmentDTO { return attachmentDTO(a) }

func toReceiptSummaryDTO(r Receipt) receiptDTO {
	return receiptDTO{ID: r.ID, DocNo: r.DocNo, WarehouseID: r.WarehouseID, SupplierPartyID: r.SupplierPartyID,
		SupplierName: r.SupplierName, PurchaseOrderID: r.PurchaseOrderID, BusinessDate: dateOf(r.BusinessDate),
		GoodsAmount: r.GoodsAmount, DiscountAmount: r.DiscountAmount, TaxAmount: r.TaxAmount, LandedCostAmount: r.LandedCostAmount,
		TotalAmount: r.TotalAmount, PaidNowAmount: r.PaidNowAmount, DueDate: dateString(r.DueDate), Status: r.Status, Note: r.Note,
		ReversalOf: r.ReversalOf, PostedAt: r.PostedAt, PostedBy: r.PostedBy}
}

// receiptListItemDTO is a PN row in the receipt list.
type receiptListItemDTO struct {
	receiptDTO
	SupplierDocumentID *uuid.UUID  `json:"supplier_document_id"`
	SupplierDocumentNo *string     `json:"supplier_document_no"`
	OpenAmount         money.Money `json:"open_amount"`
}

func toReceiptListItemDTO(r ReceiptRow) receiptListItemDTO {
	out := receiptListItemDTO{receiptDTO: toReceiptSummaryDTO(r.Receipt), SupplierDocumentID: r.SupplierDocumentID,
		SupplierDocumentNo: r.SupplierDocumentNo, OpenAmount: r.OpenAmount}
	out.OrderDocNo = r.OrderDocNo
	return out
}

func toReceiptDTO(d ReceiptDetail) receiptDTO {
	out := toReceiptSummaryDTO(d.Receipt)
	out.OrderDocNo, out.ReversalDocNo = d.OrderDocNo, d.ReversalDocNo
	out.Lines = mapSlice(d.Lines, toReceiptLineDTO)
	if d.Payable != nil {
		p := toPayableDTO(*d.Payable)
		out.Payable = &p
	}
	out.Payments = mapSlice(d.Payments, func(p PaymentInfo) paymentInfoDTO {
		return paymentInfoDTO{ID: p.ID, DocNo: p.DocNo, Amount: p.Amount, BusinessDate: dateOf(p.BusinessDate), Status: p.Status}
	})
	out.LandedCosts = mapSlice(d.LandedCosts, func(l LandedCost) landedCostDTO { return toLandedCostDTO(LandedCostDetail{LandedCost: l}) })
	out.Attachments = mapSlice(d.Attachments, toAttachmentDTO)
	return out
}

func toReceiptLineDTO(l ReceiptLine) receiptLineDTO {
	returnable := money.ZeroQty
	if inStock(l.Condition) {
		returnable = l.KeptBaseQty()
	}
	return receiptLineDTO{ID: l.ID, LineNo: l.LineNo, PurchaseOrderLineID: l.PurchaseOrderLineID, SkuID: l.SkuID,
		SkuCode: l.SkuCodeSnap, Name: l.NameSnap, VariantLabel: l.VariantLabelSnap, UnitID: l.UnitID, UnitName: l.UnitNameSnap,
		UnitFactor: l.UnitFactorSnap, Qty: l.Qty, BaseQty: l.BaseQty, ColorID: l.ColorID, SpecText: l.SpecText,
		Condition: l.Condition, LotNo: l.LotNo, ExpiryDate: dateString(l.ExpiryDate), LotID: l.LotID, UnitPrice: l.UnitPrice,
		DiscountAmount: l.DiscountAmount, TaxRate: l.TaxRateSnap, LineAmount: l.LineAmount, LandedCostAmount: l.LandedCostAmount,
		UnitCost: l.UnitCost, ReturnedBaseQty: l.ReturnedBaseQty, ReturnableBaseQty: returnable}
}

func toPayableDTO(p PayableInfo) payableDTO {
	return payableDTO{ID: p.ID, Amount: p.Amount, SettledAmount: p.SettledAmount, OpenAmount: p.OpenAmount(),
		DueDate: dateOf(p.DueDate), Status: p.Status, ReversedOn: dateString(p.ReversedOn),
		Schedule: mapSlice(p.Schedule, func(i Installment) installmentDTO {
			return installmentDTO{DueDate: dateOf(i.DueDate), Amount: i.Amount}
		})}
}

type landedCostDTO struct {
	ID           uuid.UUID      `json:"id"`
	ReceiptID    uuid.UUID      `json:"receipt_id"`
	CostType     string         `json:"cost_type"`
	Description  *string        `json:"description"`
	Amount       money.Money    `json:"amount"`
	Method       string         `json:"method"`
	PayeePartyID *uuid.UUID     `json:"payee_party_id"`
	BusinessDate string         `json:"business_date"`
	PostedAt     time.Time      `json:"posted_at"`
	Lines        []splitLineDTO `json:"lines,omitempty"`
}

type splitLineDTO struct {
	ReceiptLineID uuid.UUID   `json:"receipt_line_id"`
	Amount        money.Money `json:"amount"`
	OnHandAmount  money.Money `json:"on_hand_amount"`
	SoldAmount    money.Money `json:"sold_amount"`
}

func toLandedCostDTO(d LandedCostDetail) landedCostDTO {
	return landedCostDTO{ID: d.ID, ReceiptID: d.ReceiptID, CostType: d.CostType, Description: d.Description, Amount: d.Amount,
		Method: d.Method, PayeePartyID: d.PayeePartyID, BusinessDate: dateOf(d.BusinessDate), PostedAt: d.PostedAt,
		Lines: mapSlice(d.Lines, func(l SplitLine) splitLineDTO { return splitLineDTO(l) })}
}

type documentDTO struct {
	ID              uuid.UUID           `json:"id"`
	SupplierPartyID uuid.UUID           `json:"supplier_party_id"`
	SupplierName    string              `json:"supplier_name"`
	DocumentNo      string              `json:"document_no"`
	DocumentDate    string              `json:"document_date"`
	GoodsAmount     money.Money         `json:"goods_amount"`
	TaxAmount       money.Money         `json:"tax_amount"`
	TotalAmount     money.Money         `json:"total_amount"`
	MatchStatus     string              `json:"match_status"`
	VarianceAmount  money.Money         `json:"variance_amount"`
	Note            *string             `json:"note"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Version         int                 `json:"version"`
	Receipts        []matchedReceiptDTO `json:"receipts,omitempty"`
	Attachments     []attachmentDTO     `json:"attachments,omitempty"`
}

type matchedReceiptDTO struct {
	ReceiptID     uuid.UUID   `json:"receipt_id"`
	DocNo         string      `json:"doc_no"`
	BusinessDate  string      `json:"business_date"`
	MatchedAmount money.Money `json:"matched_amount"`
}

func toDocumentSummaryDTO(d SupplierDocument) documentDTO {
	return documentDTO{ID: d.ID, SupplierPartyID: d.SupplierPartyID, SupplierName: d.SupplierName, DocumentNo: d.DocumentNo,
		DocumentDate: dateOf(d.DocumentDate), GoodsAmount: d.GoodsAmount, TaxAmount: d.TaxAmount, TotalAmount: d.TotalAmount,
		MatchStatus: d.MatchStatus, VarianceAmount: d.VarianceAmount, Note: d.Note, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		Version: d.Version}
}

func toDocumentDTO(d DocumentDetail) documentDTO {
	out := toDocumentSummaryDTO(d.SupplierDocument)
	out.Receipts = mapSlice(d.Receipts, func(r MatchedReceipt) matchedReceiptDTO {
		return matchedReceiptDTO{ReceiptID: r.ReceiptID, DocNo: r.DocNo, BusinessDate: dateOf(r.BusinessDate),
			MatchedAmount: r.MatchedAmount}
	})
	out.Attachments = mapSlice(d.Attachments, toAttachmentDTO)
	return out
}

type returnDTO struct {
	ID              uuid.UUID       `json:"id"`
	DocNo           string          `json:"doc_no"`
	WarehouseID     uuid.UUID       `json:"warehouse_id"`
	SupplierPartyID uuid.UUID       `json:"supplier_party_id"`
	SupplierName    string          `json:"supplier_name"`
	ReceiptID       uuid.UUID       `json:"receipt_id"`
	ReasonID        uuid.UUID       `json:"reason_id"`
	Settlement      string          `json:"settlement"`
	BusinessDate    string          `json:"business_date"`
	TotalAmount     money.Money     `json:"total_amount"`
	Status          string          `json:"status"`
	PostedAt        time.Time       `json:"posted_at"`
	PostedBy        uuid.UUID       `json:"posted_by"`
	CreditNoteID    *uuid.UUID      `json:"credit_note_id,omitempty"`
	Lines           []returnLineDTO `json:"lines,omitempty"`
}

type returnLineDTO struct {
	ID            uuid.UUID    `json:"id"`
	ReceiptLineID uuid.UUID    `json:"receipt_line_id"`
	LotID         uuid.UUID    `json:"lot_id"`
	SkuID         uuid.UUID    `json:"sku_id"`
	SkuCode       string       `json:"sku_code"`
	Name          string       `json:"name"`
	VariantLabel  string       `json:"variant_label"`
	UnitID        uuid.UUID    `json:"unit_id"`
	UnitName      string       `json:"unit_name"`
	UnitFactor    money.Factor `json:"unit_factor"`
	Qty           money.Qty    `json:"qty"`
	BaseQty       money.Qty    `json:"base_qty"`
	ColorID       *uuid.UUID   `json:"color_id"`
	SpecText      *string      `json:"spec_text"`
	UnitCost      money.Money  `json:"unit_cost"`
	LineAmount    money.Money  `json:"line_amount"`
	LotNo         string       `json:"lot_no"`
}

func toReturnSummaryDTO(x PurchaseReturn) returnDTO {
	return returnDTO{ID: x.ID, DocNo: x.DocNo, WarehouseID: x.WarehouseID, SupplierPartyID: x.SupplierPartyID,
		SupplierName: x.SupplierName, ReceiptID: x.ReceiptID, ReasonID: x.ReasonID, Settlement: x.Settlement,
		BusinessDate: dateOf(x.BusinessDate), TotalAmount: x.TotalAmount, Status: x.Status, PostedAt: x.PostedAt, PostedBy: x.PostedBy}
}

func toReturnDTO(d ReturnDetail) returnDTO {
	out := toReturnSummaryDTO(d.PurchaseReturn)
	out.CreditNoteID = d.CreditNoteID
	out.Lines = mapSlice(d.Lines, toReturnLineDTO)
	return out
}

func toReturnLineDTO(l PurchaseReturnLine) returnLineDTO {
	return returnLineDTO{ID: l.ID, ReceiptLineID: l.ReceiptLineID, LotID: l.LotID, SkuID: l.SkuID, SkuCode: l.SkuCodeSnap,
		Name: l.NameSnap, VariantLabel: l.VariantLabelSnap, UnitID: l.UnitID, UnitName: l.UnitNameSnap,
		UnitFactor: l.UnitFactorSnap, Qty: l.Qty, BaseQty: l.BaseQty, ColorID: l.ColorID, SpecText: l.SpecText,
		UnitCost: l.UnitCost, LineAmount: l.LineAmount, LotNo: l.LotNo}
}

// returnListItemDTO is an XT row in the return list: credited reduced the payable, refund_due is
// what the supplier still has to pay back.
type returnListItemDTO struct {
	returnDTO
	ReceiptDocNo    string      `json:"receipt_doc_no"`
	CreditedAmount  money.Money `json:"credited_amount"`
	RefundDueAmount money.Money `json:"refund_due_amount"`
}

func toReturnListItemDTO(x ReturnRow) returnListItemDTO {
	out := returnListItemDTO{returnDTO: toReturnSummaryDTO(x.PurchaseReturn), ReceiptDocNo: x.ReceiptDocNo,
		CreditedAmount: x.Credited, RefundDueAmount: x.RefundDue}
	out.Lines = mapSlice(x.Lines, toReturnLineDTO)
	return out
}

type adjustmentDTO struct {
	ID              uuid.UUID           `json:"id"`
	DocNo           string              `json:"doc_no"`
	SupplierPartyID uuid.UUID           `json:"supplier_party_id"`
	SupplierName    string              `json:"supplier_name"`
	ReceiptID       uuid.UUID           `json:"receipt_id"`
	Kind            string              `json:"kind"`
	ReasonID        uuid.UUID           `json:"reason_id"`
	BusinessDate    string              `json:"business_date"`
	PayableDelta    money.Money         `json:"payable_delta"`
	Status          string              `json:"status"`
	PostedAt        time.Time           `json:"posted_at"`
	PostedBy        uuid.UUID           `json:"posted_by"`
	PayableID       *uuid.UUID          `json:"payable_id,omitempty"`
	CreditNoteID    *uuid.UUID          `json:"credit_note_id,omitempty"`
	Lines           []adjustmentLineDTO `json:"lines,omitempty"`
}

type adjustmentLineDTO struct {
	ReceiptLineID uuid.UUID   `json:"receipt_line_id"`
	OldUnitPrice  money.Money `json:"old_unit_price"`
	NewUnitPrice  money.Money `json:"new_unit_price"`
	BaseQty       money.Qty   `json:"base_qty"`
	OnHandAmount  money.Money `json:"on_hand_amount"`
	SoldAmount    money.Money `json:"sold_amount"`
}

func toAdjustmentSummaryDTO(a PriceAdjustment) adjustmentDTO {
	return adjustmentDTO{ID: a.ID, DocNo: a.DocNo, SupplierPartyID: a.SupplierPartyID, SupplierName: a.SupplierName,
		ReceiptID: a.ReceiptID, Kind: a.Kind, ReasonID: a.ReasonID, BusinessDate: dateOf(a.BusinessDate),
		PayableDelta: a.PayableDelta, Status: a.Status, PostedAt: a.PostedAt, PostedBy: a.PostedBy}
}

func toAdjustmentDTO(d AdjustmentDetail) adjustmentDTO {
	out := toAdjustmentSummaryDTO(d.PriceAdjustment)
	out.PayableID, out.CreditNoteID = d.PayableID, d.CreditNoteID
	out.Lines = mapSlice(d.Lines, func(l PriceAdjustmentLine) adjustmentLineDTO { return adjustmentLineDTO(l) })
	return out
}
