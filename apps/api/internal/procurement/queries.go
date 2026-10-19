package procurement

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// maxSearchLen bounds a search term.
const maxSearchLen = 200

// PayableInfo is what remains of the payable of a receipt: what was owed, what is settled, and
// its installments.
type PayableInfo struct {
	ID            uuid.UUID   `db:"id"`
	Amount        money.Money `db:"amount"`
	SettledAmount money.Money `db:"settled_amount"`
	DueDate       time.Time   `db:"due_date"`
	Status        string      `db:"status"`
	ReversedOn    *time.Time  `db:"reversed_on"`
	Schedule      []Installment
}

// OpenAmount is what is still owed.
func (p PayableInfo) OpenAmount() money.Money { return p.Amount.Sub(p.SettledAmount) }

// PaymentInfo is a PC paid with a procurement document.
type PaymentInfo struct {
	ID           uuid.UUID   `db:"id"`
	DocNo        string      `db:"doc_no"`
	Amount       money.Money `db:"amount"`
	BusinessDate time.Time   `db:"business_date"`
	Status       string      `db:"status"`
}

// ReceiptDetail is a PN with its lines, payable, the payments made with it, its landed costs and
// document photos. ReversalDocNo is the PN that reversed it.
type ReceiptDetail struct {
	Receipt
	OrderDocNo    *string
	ReversalDocNo *string
	Lines         []ReceiptLine
	Payable       *PayableInfo
	Payments      []PaymentInfo
	LandedCosts   []LandedCost
	Attachments   []Attachment
}

// OrderRow is a PO in a list with the progress of its lines in base units.
type OrderRow struct {
	PurchaseOrder
	OrderedBaseQty  money.Qty `db:"ordered_base_qty"`
	ReceivedBaseQty money.Qty `db:"received_base_qty"`
	PendingBaseQty  money.Qty `db:"pending_base_qty"`
}

// ReceiptRow is a PN in a list: the order it received, the supplier invoice matched to it and
// what the store still owes on its payable.
type ReceiptRow struct {
	Receipt
	OrderDocNo         *string     `db:"order_doc_no"`
	SupplierDocumentID *uuid.UUID  `db:"supplier_document_id"`
	SupplierDocumentNo *string     `db:"supplier_document_no"`
	OpenAmount         money.Money `db:"open_amount"`
}

// ReturnRow is an XT in a list with its receipt, its lines and how its credit settled: Credited
// reduced what the store owed, RefundDue is what the supplier must pay back.
type ReturnRow struct {
	PurchaseReturn
	ReceiptDocNo string      `db:"receipt_doc_no"`
	Credited     money.Money `db:"credited_amount"`
	RefundDue    money.Money `db:"refund_due_amount"`
	Lines        []PurchaseReturnLine
}

// LateOrder is a PO whose expected date has passed while goods are still pending.
type LateOrder struct {
	OrderRow
	DaysLate int
}

// List filters. AfterAt/AfterID is the newest-first cursor.
type (
	// OrderFilter lists purchase orders.
	OrderFilter struct {
		Status     string
		SupplierID *uuid.UUID
		Q          string
		AfterAt    *time.Time
		AfterID    uuid.UUID
		Limit      int
	}
	// ReceiptFilter lists receipts.
	ReceiptFilter struct {
		Q               string
		Status          string
		SupplierID      *uuid.UUID
		PurchaseOrderID *uuid.UUID
		From, To        *time.Time
		AfterAt         *time.Time
		AfterID         uuid.UUID
		Limit           int
	}
	// DocFilter lists supplier documents, purchase returns or price adjustments.
	DocFilter struct {
		// Q searches the number and the supplier (purchase returns only).
		Q          string
		SupplierID *uuid.UUID
		ReceiptID  *uuid.UUID
		Status     string
		From, To   *time.Time
		AfterAt    *time.Time
		AfterID    uuid.UUID
		Limit      int
	}
	// LateFilter pages late orders by expected date, oldest first.
	LateFilter struct {
		SupplierID *uuid.UUID
		AfterDate  *time.Time
		AfterID    uuid.UUID
		Limit      int
	}
)

const orderProgress = `,
	COALESCE((SELECT SUM(l.base_qty) FROM purchase_order_lines l WHERE l.purchase_order_id = o.id), 0) AS ordered_base_qty,
	COALESCE((SELECT SUM(l.received_base_qty) FROM purchase_order_lines l WHERE l.purchase_order_id = o.id), 0) AS received_base_qty,
	COALESCE((SELECT SUM(GREATEST(l.base_qty - l.received_base_qty, 0)) FROM purchase_order_lines l
	          WHERE l.purchase_order_id = o.id), 0) AS pending_base_qty`

func searchPatterns(q string) ([]string, error) {
	if len(q) > maxSearchLen {
		return nil, errInvalid(map[string]string{"q": "too_long"})
	}
	words := strings.Fields(textnorm.SearchNorm(q))
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = "%" + w + "%"
	}
	return out, nil
}

// Orders lists POs newest first; q searches the number and the supplier.
func (s *Service) Orders(ctx context.Context, scope Scope, f OrderFilter) ([]OrderRow, error) {
	patterns, err := searchPatterns(f.Q)
	if err != nil {
		return nil, err
	}
	out := []OrderRow{}
	err = s.read(ctx, scope, func(q db.Querier) error {
		return wrapErr("list purchase orders", sqlx.SelectContext(ctx, q, &out, `SELECT `+orderColumns+orderProgress+orderFrom+`
			WHERE ($1 = '' OR o.status = $1) AND ($2::uuid IS NULL OR o.supplier_party_id = $2)
			  AND (lower(o.doc_no) || ' ' || p.search_norm) LIKE ALL ($3::text[])
			  AND ($4::timestamptz IS NULL OR (o.created_at, o.id) < ($4, $5::uuid))
			ORDER BY o.created_at DESC, o.id DESC LIMIT $6`, f.Status, f.SupplierID, patterns, f.AfterAt, f.AfterID, f.Limit))
	})
	return out, err
}

// Order is a PO with its lines.
func (s *Service) Order(ctx context.Context, scope Scope, id uuid.UUID) (OrderDetail, error) {
	var out OrderDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = orderDetail(ctx, q, id)
		return err
	})
	return out, err
}

// ReceivableLines are the lines of a confirmed or partly received PO with goods still pending:
// what a receipt against it starts from.
func (s *Service) ReceivableLines(ctx context.Context, scope Scope, id uuid.UUID) ([]PurchaseOrderLine, error) {
	out := []PurchaseOrderLine{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		o, err := getOrder(ctx, q, id, false)
		if err != nil {
			return err
		}
		if !s.status.Receivable(o.Status) {
			return apperr.NewConflict(CodeOrderNotReceivable, "Only a confirmed or partly received purchase order can be received")
		}
		lines, err := orderLines(ctx, q, id)
		for _, l := range lines {
			if l.PendingBaseQty().IsPositive() {
				out = append(out, l)
			}
		}
		return err
	})
	return out, err
}

// LateOrders are the POs still waiting for goods after their expected date, the most late first.
func (s *Service) LateOrders(ctx context.Context, scope Scope, f LateFilter) ([]LateOrder, error) {
	today, err := s.today(ctx, scope.TenantID)
	if err != nil {
		return nil, err
	}
	var rows []OrderRow
	err = s.read(ctx, scope, func(q db.Querier) error {
		return wrapErr("list late purchase orders", sqlx.SelectContext(ctx, q, &rows, `SELECT `+orderColumns+orderProgress+orderFrom+`
			WHERE o.status IN ('confirmed', 'partially_received') AND o.expected_date < $1
			  AND ($2::uuid IS NULL OR o.supplier_party_id = $2)
			  AND ($3::date IS NULL OR (o.expected_date, o.id) > ($3, $4::uuid))
			ORDER BY o.expected_date, o.id LIMIT $5`, today, f.SupplierID, f.AfterDate, f.AfterID, f.Limit))
	})
	out := make([]LateOrder, len(rows))
	for i, r := range rows {
		out[i] = LateOrder{OrderRow: r, DaysLate: int(today.Sub(*r.ExpectedDate).Hours() / 24)}
	}
	return out, err
}

// Receipts lists PNs (reversals included) newest first.
func (s *Service) Receipts(ctx context.Context, scope Scope, f ReceiptFilter) ([]ReceiptRow, error) {
	patterns, err := searchPatterns(f.Q)
	if err != nil {
		return nil, err
	}
	out := []ReceiptRow{}
	err = s.read(ctx, scope, func(q db.Querier) error {
		return wrapErr("list receipts", sqlx.SelectContext(ctx, q, &out, `SELECT `+receiptColumns+`,
			  (SELECT o.doc_no FROM purchase_orders o WHERE o.id = r.purchase_order_id) AS order_doc_no,
			  m.supplier_document_id, m.supplier_document_no,
			  COALESCE((SELECT b.amount - b.settled_amount FROM obligations b
			            WHERE b.source_type = 'receipt' AND b.source_id = r.id AND b.direction = 'payable'
			              AND b.reversed_on IS NULL), 0) AS open_amount`+
			receiptFrom+`
			LEFT JOIN LATERAL (SELECT d.id AS supplier_document_id, d.document_no AS supplier_document_no
			  FROM supplier_document_receipts dr JOIN supplier_documents d ON d.id = dr.supplier_document_id
			  WHERE dr.receipt_id = r.id ORDER BY d.document_date DESC, d.id LIMIT 1) m ON true
			WHERE ($1 = '' OR r.status = $1) AND ($2::uuid IS NULL OR r.supplier_party_id = $2)
			  AND ($3::uuid IS NULL OR r.purchase_order_id = $3)
			  AND ($4::date IS NULL OR r.business_date >= $4) AND ($5::date IS NULL OR r.business_date <= $5)
			  AND ($6::timestamptz IS NULL OR (r.created_at, r.id) < ($6, $7::uuid))
			  AND (lower(r.doc_no) || ' ' || p.search_norm) LIKE ALL ($9::text[])
			ORDER BY r.created_at DESC, r.id DESC LIMIT $8`,
			f.Status, f.SupplierID, f.PurchaseOrderID, f.From, f.To, f.AfterAt, f.AfterID, f.Limit, patterns))
	})
	return out, err
}

// Receipt is a PN with everything recorded with it.
func (s *Service) Receipt(ctx context.Context, scope Scope, id uuid.UUID) (ReceiptDetail, error) {
	var out ReceiptDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = receiptDetail(ctx, q, id)
		return err
	})
	return out, err
}

func receiptDetail(ctx context.Context, q db.Querier, id uuid.UUID) (ReceiptDetail, error) {
	r, err := getReceipt(ctx, q, id, false)
	if err != nil {
		return ReceiptDetail{}, err
	}
	d := ReceiptDetail{Receipt: r, Payments: []PaymentInfo{}, LandedCosts: []LandedCost{}}
	if d.Lines, err = receiptLines(ctx, q, id); err != nil {
		return d, err
	}
	if r.PurchaseOrderID != nil {
		var no string
		if err := q.QueryRowxContext(ctx, `SELECT doc_no FROM purchase_orders WHERE id = $1`, *r.PurchaseOrderID).Scan(&no); err != nil {
			return d, wrap("read receipt order", err)
		}
		d.OrderDocNo = &no
	}
	var rev []string
	if err := sqlx.SelectContext(ctx, q, &rev, `SELECT doc_no FROM receipts WHERE reversal_of = $1`, id); err != nil {
		return d, wrap("read receipt reversal", err)
	}
	if len(rev) > 0 {
		d.ReversalDocNo = &rev[0]
	}
	if d.Payable, err = payableOf(ctx, q, DocReceipt, id); err != nil {
		return d, err
	}
	if err := sqlx.SelectContext(ctx, q, &d.Payments, `
		SELECT id, doc_no, amount, business_date, status FROM payments
		WHERE source_type = $1 AND source_id = $2 AND reversal_of IS NULL ORDER BY posted_at, id`,
		PaymentSourcePurchase, id); err != nil {
		return d, wrap("read receipt payments", err)
	}
	if err := sqlx.SelectContext(ctx, q, &d.LandedCosts, `SELECT `+landedColumns+` FROM landed_cost_allocations
		WHERE receipt_id = $1 ORDER BY posted_at, id`, id); err != nil {
		return d, wrap("read receipt landed costs", err)
	}
	d.Attachments, err = attachmentsOf(ctx, q, DocReceipt, id)
	return d, err
}

// payableOf is the payable a procurement document recorded, nil when it owed nothing.
func payableOf(ctx context.Context, q db.Querier, sourceType string, sourceID uuid.UUID) (*PayableInfo, error) {
	var p PayableInfo
	err := sqlx.GetContext(ctx, q, &p, `SELECT id, amount, settled_amount, due_date, status, reversed_on FROM obligations
		WHERE direction = 'payable' AND source_type = $1 AND source_id = $2`, sourceType, sourceID)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap("read payable", err)
	}
	var rows []struct {
		DueDate time.Time   `db:"due_date"`
		Amount  money.Money `db:"amount"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT due_date, amount FROM obligation_schedules WHERE obligation_id = $1
		ORDER BY installment_no`, p.ID); err != nil {
		return nil, wrap("read payable schedule", err)
	}
	p.Schedule = make([]Installment, len(rows))
	for i, r := range rows {
		p.Schedule[i] = Installment{DueDate: r.DueDate, Amount: r.Amount}
	}
	return &p, nil
}

// creditNoteOf is the credit note a procurement document recorded, nil when none.
func creditNoteOf(ctx context.Context, q db.Querier, sourceType string, sourceID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `SELECT id FROM credit_notes WHERE source_type = $1 AND source_id = $2 AND reversal_of IS NULL`,
		sourceType, sourceID).Scan(&id)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap("read credit note", err)
	}
	return &id, nil
}

// Supplier documents.

// Documents lists supplier documents newest first; Status filters the match status.
func (s *Service) Documents(ctx context.Context, scope Scope, f DocFilter) ([]SupplierDocument, error) {
	out := []SupplierDocument{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		return wrapErr("list supplier documents", sqlx.SelectContext(ctx, q, &out, `SELECT `+documentColumns+documentFrom+`
			WHERE ($1 = '' OR d.match_status = $1) AND ($2::uuid IS NULL OR d.supplier_party_id = $2)
			  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM supplier_document_receipts l
			                                   WHERE l.supplier_document_id = d.id AND l.receipt_id = $3))
			  AND ($4::date IS NULL OR d.document_date >= $4) AND ($5::date IS NULL OR d.document_date <= $5)
			  AND ($6::timestamptz IS NULL OR (d.created_at, d.id) < ($6, $7::uuid))
			ORDER BY d.created_at DESC, d.id DESC LIMIT $8`,
			f.Status, f.SupplierID, f.ReceiptID, f.From, f.To, f.AfterAt, f.AfterID, f.Limit))
	})
	return out, err
}

// Document is a supplier document with its matched receipts.
func (s *Service) Document(ctx context.Context, scope Scope, id uuid.UUID) (DocumentDetail, error) {
	var out DocumentDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = documentDetail(ctx, q, id)
		return err
	})
	return out, err
}

func getDocument(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (SupplierDocument, error) {
	query := `SELECT ` + documentColumns + documentFrom + ` WHERE d.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF d`
	}
	var d SupplierDocument
	err := sqlx.GetContext(ctx, q, &d, query, id)
	if db.IsNoRows(err) {
		return d, errNotFound("Supplier document")
	}
	return d, wrapErr("read supplier document", err)
}

func documentDetail(ctx context.Context, q db.Querier, id uuid.UUID) (DocumentDetail, error) {
	d, err := getDocument(ctx, q, id, false)
	if err != nil {
		return DocumentDetail{}, err
	}
	out := DocumentDetail{SupplierDocument: d, Receipts: []MatchedReceipt{}}
	if err := sqlx.SelectContext(ctx, q, &out.Receipts, `
		SELECT l.receipt_id, r.doc_no, r.business_date, l.matched_amount
		FROM supplier_document_receipts l JOIN receipts r ON r.id = l.receipt_id
		WHERE l.supplier_document_id = $1 ORDER BY r.business_date, r.doc_no`, id); err != nil {
		return out, wrap("read matched receipts", err)
	}
	out.Attachments, err = attachmentsOf(ctx, q, AttachmentOwnerSupplierDocument, id)
	return out, err
}

// Purchase returns.

// Returns lists XTs newest first.
func (s *Service) Returns(ctx context.Context, scope Scope, f DocFilter) ([]ReturnRow, error) {
	patterns, err := searchPatterns(f.Q)
	if err != nil {
		return nil, err
	}
	out := []ReturnRow{}
	err = s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+returnColumns+`, r.doc_no AS receipt_doc_no,
			  COALESCE(c.allocated_amount, 0) AS credited_amount, COALESCE(c.amount - c.allocated_amount, 0) AS refund_due_amount`+
			returnFrom+`
			JOIN receipts r ON r.id = x.receipt_id
			LEFT JOIN credit_notes c ON c.source_type = 'purchase_return' AND c.source_id = x.id AND c.reversal_of IS NULL
			WHERE ($1 = '' OR x.status = $1) AND ($2::uuid IS NULL OR x.supplier_party_id = $2)
			  AND ($3::uuid IS NULL OR x.receipt_id = $3)
			  AND ($4::date IS NULL OR x.business_date >= $4) AND ($5::date IS NULL OR x.business_date <= $5)
			  AND ($6::timestamptz IS NULL OR (x.created_at, x.id) < ($6, $7::uuid))
			  AND (lower(x.doc_no) || ' ' || lower(r.doc_no) || ' ' || p.search_norm) LIKE ALL ($9::text[])
			ORDER BY x.created_at DESC, x.id DESC LIMIT $8`,
			f.Status, f.SupplierID, f.ReceiptID, f.From, f.To, f.AfterAt, f.AfterID, f.Limit, patterns); err != nil {
			return wrap("list purchase returns", err)
		}
		if len(out) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(out))
		byID := make(map[uuid.UUID]*ReturnRow, len(out))
		for i := range out {
			ids[i], out[i].Lines = out[i].ID, []PurchaseReturnLine{}
			byID[out[i].ID] = &out[i]
		}
		var lines []PurchaseReturnLine
		if err := sqlx.SelectContext(ctx, q, &lines, `SELECT `+returnLineColumns+returnLineFrom+`
			WHERE xl.purchase_return_id = ANY($1) ORDER BY xl.id`, ids); err != nil {
			return wrap("read purchase return lines", err)
		}
		for _, l := range lines {
			byID[l.PurchaseReturnID].Lines = append(byID[l.PurchaseReturnID].Lines, l)
		}
		return nil
	})
	return out, err
}

// Return is an XT with its lines.
func (s *Service) Return(ctx context.Context, scope Scope, id uuid.UUID) (ReturnDetail, error) {
	var out ReturnDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = returnDetail(ctx, q, id)
		return err
	})
	return out, err
}

func returnDetail(ctx context.Context, q db.Querier, id uuid.UUID) (ReturnDetail, error) {
	var x PurchaseReturn
	err := sqlx.GetContext(ctx, q, &x, `SELECT `+returnColumns+returnFrom+` WHERE x.id = $1`, id)
	if db.IsNoRows(err) {
		return ReturnDetail{}, errNotFound("Purchase return")
	}
	if err != nil {
		return ReturnDetail{}, wrap("read purchase return", err)
	}
	d := ReturnDetail{PurchaseReturn: x}
	if err := sqlx.SelectContext(ctx, q, &d.Lines, `SELECT `+returnLineColumns+returnLineFrom+`
		WHERE xl.purchase_return_id = $1 ORDER BY xl.id`, id); err != nil {
		return d, wrap("read purchase return lines", err)
	}
	d.CreditNoteID, err = creditNoteOf(ctx, q, DocPurchaseReturn, id)
	return d, err
}

// Price adjustments.

// Adjustments lists DGs newest first.
func (s *Service) Adjustments(ctx context.Context, scope Scope, f DocFilter) ([]PriceAdjustment, error) {
	out := []PriceAdjustment{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		return wrapErr("list purchase price adjustments", sqlx.SelectContext(ctx, q, &out, `SELECT `+adjustmentColumns+adjustmentFrom+`
			WHERE ($1 = '' OR a.status = $1) AND ($2::uuid IS NULL OR a.supplier_party_id = $2)
			  AND ($3::uuid IS NULL OR a.receipt_id = $3)
			  AND ($4::date IS NULL OR a.business_date >= $4) AND ($5::date IS NULL OR a.business_date <= $5)
			  AND ($6::timestamptz IS NULL OR (a.created_at, a.id) < ($6, $7::uuid))
			ORDER BY a.created_at DESC, a.id DESC LIMIT $8`,
			f.Status, f.SupplierID, f.ReceiptID, f.From, f.To, f.AfterAt, f.AfterID, f.Limit))
	})
	return out, err
}

// Adjustment is a DG with its lines and the debt it moved.
func (s *Service) Adjustment(ctx context.Context, scope Scope, id uuid.UUID) (AdjustmentDetail, error) {
	var out AdjustmentDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = adjustmentDetail(ctx, q, id)
		return err
	})
	return out, err
}

func adjustmentDetail(ctx context.Context, q db.Querier, id uuid.UUID) (AdjustmentDetail, error) {
	var a PriceAdjustment
	err := sqlx.GetContext(ctx, q, &a, `SELECT `+adjustmentColumns+adjustmentFrom+` WHERE a.id = $1`, id)
	if db.IsNoRows(err) {
		return AdjustmentDetail{}, errNotFound("Purchase price adjustment")
	}
	if err != nil {
		return AdjustmentDetail{}, wrap("read purchase price adjustment", err)
	}
	d := AdjustmentDetail{PriceAdjustment: a}
	if err := sqlx.SelectContext(ctx, q, &d.Lines, `
		SELECT l.receipt_line_id, l.old_unit_price, l.new_unit_price, l.base_qty, l.on_hand_amount, l.sold_amount
		FROM purchase_price_adjustment_lines l JOIN receipt_lines r ON r.id = l.receipt_line_id
		WHERE l.purchase_price_adjustment_id = $1 ORDER BY r.line_no`, id); err != nil {
		return d, wrap("read purchase price adjustment lines", err)
	}
	payable, err := payableOf(ctx, q, DocPriceAdjustment, id)
	if err != nil {
		return d, err
	}
	if payable != nil {
		d.PayableID = &payable.ID
	}
	d.CreditNoteID, err = creditNoteOf(ctx, q, DocPriceAdjustment, id)
	return d, err
}

// wrapErr wraps a read error, nil staying nil.
func wrapErr(what string, err error) error {
	if err == nil {
		return nil
	}
	return wrap(what, err)
}
