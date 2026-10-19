package sales

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// DocumentFilter narrows the sales document list.
type DocumentFilter struct {
	Source     string
	Status     string
	CustomerID *uuid.UUID
	InvoiceID  *uuid.UUID
	ShiftID    *uuid.UUID
	From, To   *time.Time
	// Page pages on posted_at, which a sales document is created at.
	Page ListPage
}

// DocumentDetail is a sales document with its lines and what the customer paid of it.
type DocumentDetail struct {
	SalesDocument
	Payment PaymentState `json:"payment"`
}

// ReturnableLine is a line of a sales document that can still be returned, in base units, with what
// one base unit was sold for after every discount.
type ReturnableLine struct {
	LineID       uuid.UUID    `json:"sales_document_line_id"`
	LineNo       int          `json:"line_no"`
	Kind         string       `json:"line_kind"`
	ParentLineID *uuid.UUID   `json:"parent_line_id"`
	SkuID        uuid.UUID    `json:"sku_id"`
	SkuCode      string       `json:"sku_code"`
	Name         string       `json:"name"`
	VariantLabel string       `json:"variant_label"`
	ColorID      *uuid.UUID   `json:"color_id"`
	UnitID       uuid.UUID    `json:"unit_id"`
	UnitName     string       `json:"unit_name"`
	UnitFactor   money.Factor `json:"unit_factor"`
	Sold         money.Qty    `json:"sold_base_qty"`
	Returned     money.Qty    `json:"returned_base_qty"`
	Returnable   money.Qty    `json:"returnable_base_qty"`
	LineAmount   money.Money  `json:"line_amount"`
	UnitAmount   money.Money  `json:"unit_amount"`
}

// SalesDocuments lists sales documents newest first, without lines.
func (s *Service) SalesDocuments(ctx context.Context, scope Scope, f DocumentFilter) ([]SalesDocument, error) {
	out := []SalesDocument{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		var w where
		switch f.Source {
		case "":
		case SourceStockOut, SourcePOS, SourcePOSOffline, SourceExchange:
			w.add("source = $%d", f.Source)
		default:
			return errInvalid(map[string]string{"source": "unknown"})
		}
		switch f.Status {
		case "":
		case DocPosted, DocReversed, DocPartiallyReversed:
			w.add("status = $%d", f.Status)
		default:
			return errInvalid(map[string]string{"status": "unknown"})
		}
		if f.CustomerID != nil {
			w.add("customer_party_id = $%d", *f.CustomerID)
		}
		if f.InvoiceID != nil {
			w.add("invoice_id = $%d", *f.InvoiceID)
		}
		if f.ShiftID != nil {
			w.add("shift_id = $%d", *f.ShiftID)
		}
		if f.From != nil {
			w.add("business_date >= $%d", *f.From)
		}
		if f.To != nil {
			w.add("business_date <= $%d", *f.To)
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+documentColumns+` FROM sales_documents`+w.sql()+tail, w.args...); err != nil {
			return wrap("list sales documents", err)
		}
		return nil
	})
	return out, err
}

// SalesDocument returns a sales document with its lines and payment state.
func (s *Service) SalesDocument(ctx context.Context, scope Scope, id uuid.UUID) (DocumentDetail, error) {
	var out DocumentDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = s.documentDetail(ctx, q, id)
		return err
	})
	return out, err
}

func (s *Service) documentDetail(ctx context.Context, q db.Querier, id uuid.UUID) (DocumentDetail, error) {
	d, err := getDocument(ctx, q, id, false)
	if err != nil {
		return DocumentDetail{}, err
	}
	if d.Lines, err = documentLinesOf(ctx, q, id); err != nil {
		return DocumentDetail{}, err
	}
	st, err := s.documentPayment(ctx, q, d)
	if err != nil {
		return DocumentDetail{}, err
	}
	return DocumentDetail{SalesDocument: d, Payment: st}, nil
}

// documentPayment is the payment state of a sales document. A document with a receivable is paid
// by what settled it; one without (a walk-in sale, paid at the counter) was paid in full. A reversed
// document is owed nothing: what had been paid became the payer's advance.
func (s *Service) documentPayment(ctx context.Context, q db.Querier, d SalesDocument) (PaymentState, error) {
	if d.Status == DocReversed {
		return paymentStateOf(money.ZeroMoney, money.ZeroMoney), nil
	}
	ob, err := s.debt.ReceivableOf(ctx, q, d.ID)
	if err != nil {
		return PaymentState{}, err
	}
	if ob == nil {
		return paymentStateOf(d.Total, d.Total), nil
	}
	return paymentStateOf(d.Total, d.Total.Sub(ob.Open())), nil
}

// ReturnableLines lists what of a sales document can still be returned: every line that moved
// stock and has some of it left. Combo components are returned with their combo, which no sale
// can hold yet, so they are not listed.
func (s *Service) ReturnableLines(ctx context.Context, scope Scope, id uuid.UUID) ([]ReturnableLine, error) {
	out := []ReturnableLine{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		d, err := getDocument(ctx, q, id, false)
		if err != nil {
			return err
		}
		if d.Status == DocReversed {
			return errConflict(CodeDocumentReversed, "The sales document was reversed; nothing of it can be returned")
		}
		lines, err := documentLinesOf(ctx, q, id)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if r, ok := returnableOf(l); ok {
				out = append(out, r)
			}
		}
		return nil
	})
	return out, err
}

// returnableOf is the returnable view of a document line, false for a line that cannot be returned
// or has nothing left.
func returnableOf(l DocumentLine) (ReturnableLine, bool) {
	if !returnable(l.Kind) || l.SkuID == nil {
		return ReturnableLine{}, false
	}
	left := l.BaseQty.Sub(l.Returned)
	if !left.IsPositive() {
		return ReturnableLine{}, false
	}
	code := ""
	if l.SkuCode != nil {
		code = *l.SkuCode
	}
	unit := money.ZeroMoney
	if l.BaseQty.IsPositive() {
		if u, err := money.RoundMoney(l.LineAmount.Decimal().Div(l.BaseQty.Decimal())); err == nil {
			unit = u
		}
	}
	return ReturnableLine{
		LineID: l.ID, LineNo: l.LineNo, Kind: l.Kind, ParentLineID: l.ParentLineID, SkuID: *l.SkuID, SkuCode: code,
		Name: l.Name, VariantLabel: l.VariantLabel, ColorID: l.ColorID, UnitID: l.UnitID, UnitName: l.UnitName,
		UnitFactor: l.UnitFactor, Sold: l.BaseQty, Returned: l.Returned, Returnable: left, LineAmount: l.LineAmount,
		UnitAmount: unit,
	}, true
}

// returnable reports the kinds a return takes back: the lines that moved stock and carry their own
// price (a gift is returned at no value).
func returnable(kind string) bool {
	return kind == KindItem || kind == KindGift || kind == KindMixFlavor
}
