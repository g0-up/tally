package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// salesPricing is the pricing engine in sales mode: price lists, promotions and tax per line.
type salesPricing struct{ svc *pricing.Service }

func (p salesPricing) Price(ctx context.Context, q db.Querier, tenantID uuid.UUID, d sales.PriceDraft) (sales.PricedDoc, error) {
	draft := pricing.DocumentDraft{PartyID: d.PartyID, BusinessDate: d.BusinessDate, OrderDiscount: d.OrderDiscount,
		PromotionCodes: d.PromotionCodes, Lines: make([]pricing.DraftLine, len(d.Lines))}
	for i, l := range d.Lines {
		draft.Lines[i] = pricing.DraftLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty, UnitPrice: l.UnitPrice,
			DiscountAmount: l.Discount}
	}
	doc, err := p.svc.CalculateIn(ctx, q, tenantID, draft)
	if err != nil {
		return sales.PricedDoc{}, err
	}
	out := sales.PricedDoc{PricesIncludeTax: doc.PricesIncludeTax, Lines: make([]sales.PricedLine, len(doc.Lines)),
		Goods: doc.GoodsAmount, Discount: doc.DiscountAmount, Tax: doc.TaxAmount, Total: doc.TotalAmount}
	for i, l := range doc.Lines {
		source, err := json.Marshal(l.PriceSource)
		if err != nil {
			return sales.PricedDoc{}, fmt.Errorf("app: encode price source: %w", err)
		}
		promotions := make([]sales.PromotionShare, len(l.PriceSource.Promotions))
		for k, pr := range l.PriceSource.Promotions {
			promotions[k] = sales.PromotionShare{ID: pr.ID, Amount: pr.Amount}
		}
		out.Lines[i] = sales.PricedLine{DraftIndex: l.DraftIndex, Gift: l.LineKind == pricing.LineGift, SkuID: l.SkuID,
			ColorID: l.ColorID, UnitID: l.UnitID, UnitFactor: l.UnitFactor, Qty: l.Qty, BaseQty: l.BaseQty, UnitPrice: l.UnitPrice,
			Gross: l.GrossAmount, Discount: l.DiscountAmount, OrderAlloc: l.OrderDiscountAlloc, TaxRate: l.TaxRateSnap,
			Tax: l.TaxAmount, LineAmount: l.LineAmount, NetUnitPrice: l.NetUnitPrice, MinPrice: l.MinPriceSnap, Source: source,
			Manual: l.PriceSource.Kind == pricing.SourceManual, Promotions: promotions}
	}
	return out, nil
}

// RetailPrices prices each query for a walk-in customer on today's retail list; a SKU without a
// retail price resolves to the manual source and has none.
func (p salesPricing) RetailPrices(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []sales.RetailQuery) ([]*money.Money, error) {
	in := make([]pricing.PriceLine, len(lines))
	for i, l := range lines {
		in[i] = pricing.PriceLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty}
	}
	resolved, err := p.svc.ResolveMany(ctx, q, tenantID, pricing.PriceContext{}, in)
	if err != nil {
		return nil, err
	}
	out := make([]*money.Money, len(resolved))
	for i, r := range resolved {
		if r.Source.Kind != pricing.SourceManual {
			amount := r.Amount
			out[i] = &amount
		}
	}
	return out, nil
}

func (p salesPricing) RecordUsage(ctx context.Context, q db.Querier, tenantID, promotionID uuid.UUID, docType string, docID uuid.UUID, amount money.Money) error {
	return p.svc.RecordUsage(ctx, q, tenantID, promotionID, docType, docID, amount)
}

// salesStock is the inventory ledger and its reservations as sales sees them, written inside the
// sales posting.
type salesStock struct {
	l            *inventory.StockLedger
	reader       inventory.StockReader
	reservations *inventory.ReservationService
}

func (s salesStock) Issue(ctx context.Context, tx posting.Tx, doc sales.StockDoc, lines []sales.StockIssue) ([]sales.StockMove, error) {
	in := make([]inventory.IssueLine, len(lines))
	for i, l := range lines {
		lineID := l.DocLineID
		in[i] = inventory.IssueLine{DocLineID: &lineID, SkuID: l.SkuID, Qty: l.BaseQty, MoveType: inventory.MoveSale,
			ColorID: l.ColorID, Uncolored: l.ColorID == nil}
	}
	moves, err := s.l.Issue(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return salesMoves(moves), err
}

func (s salesStock) Receive(ctx context.Context, tx posting.Tx, doc sales.StockDoc, lines []sales.StockReceive) ([]sales.StockMove, error) {
	in := make([]inventory.ReceiveLine, len(lines))
	for i, l := range lines {
		lineID := l.DocLineID
		in[i] = inventory.ReceiveLine{DocLineID: &lineID, SkuID: l.SkuID, Qty: l.BaseQty, MoveType: inventory.MoveSalesReturn,
			ColorID: l.ColorID, LotNo: l.LotNo, ExpiryDate: l.ExpiryDate, LotID: l.LotID, SourceMoveID: l.SourceMoveID,
			AtAverageCost: l.LotID == nil && l.SourceMoveID == nil}
	}
	moves, err := s.l.Receive(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return salesMoves(moves), err
}

func (s salesStock) Quarantine(ctx context.Context, tx posting.Tx, doc sales.StockDoc, lines []sales.StockQuarantine) ([]sales.StockMove, error) {
	in := make([]inventory.QuarantineLine, len(lines))
	for i, l := range lines {
		lineID, lotID := l.DocLineID, l.LotID
		in[i] = inventory.QuarantineLine{DocLineID: &lineID, SkuID: l.SkuID, Qty: l.BaseQty, ColorID: l.ColorID, LotID: &lotID}
	}
	moves, err := s.l.Quarantine(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return salesMoves(moves), err
}

func (s salesStock) Reverse(ctx context.Context, tx posting.Tx, by, doc sales.StockDoc) ([]sales.StockMove, error) {
	moves, err := s.l.Reverse(ctx, tx, inventory.PostingOf(tx, by.Type, by.ID), doc.Type, doc.ID)
	return salesMoves(moves), err
}

func (s salesStock) MovesOf(ctx context.Context, q db.Querier, doc sales.StockDoc) ([]sales.StockMove, error) {
	var rows []struct {
		ID         uuid.UUID   `db:"id"`
		SkuID      uuid.UUID   `db:"sku_id"`
		LotID      uuid.UUID   `db:"lot_id"`
		ColorID    *uuid.UUID  `db:"color_id"`
		DocLineID  *uuid.UUID  `db:"doc_line_id"`
		Qty        money.Qty   `db:"qty"`
		UnitCost   money.Money `db:"unit_cost"`
		Value      money.Money `db:"value_amount"`
		MoveType   string      `db:"move_type"`
		LotNo      string      `db:"lot_no"`
		Expiry     *time.Time  `db:"expiry_date"`
		Quarantine bool        `db:"is_quarantine"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT m.id, m.sku_id, m.lot_id, m.color_id, m.doc_line_id, m.qty, m.unit_cost, m.value_amount, m.move_type,
		       l.lot_no, l.expiry_date, l.is_quarantine
		FROM stock_moves m JOIN lots l ON l.id = m.lot_id
		WHERE m.doc_type = $1 AND m.doc_id = $2
		ORDER BY m.created_at, m.id`, doc.Type, doc.ID); err != nil {
		return nil, fmt.Errorf("app: read stock moves of %s %s: %w", doc.Type, doc.ID, err)
	}
	out := make([]sales.StockMove, len(rows))
	for i, r := range rows {
		out[i] = sales.StockMove{ID: r.ID, SkuID: r.SkuID, LotID: r.LotID, ColorID: r.ColorID, DocLineID: r.DocLineID, Qty: r.Qty,
			UnitCost: r.UnitCost, Value: r.Value, MoveType: r.MoveType, LotNo: r.LotNo, Expiry: r.Expiry, Quarantine: r.Quarantine}
	}
	return out, nil
}

func (s salesStock) OnHandByColor(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID][]sales.ColorStock, error) {
	stock, err := s.reader.OnHandByColor(ctx, q, skuIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]sales.ColorStock, len(stock))
	for skuID, colors := range stock {
		for _, c := range colors {
			out[skuID] = append(out[skuID], sales.ColorStock{ColorID: c.ColorID, Qty: c.Qty})
		}
	}
	return out, nil
}

// Available reads the stock projection: sellable less what reservations hold, over every
// warehouse of the tenant.
func (s salesStock) Available(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]money.Qty, error) {
	out := map[uuid.UUID]money.Qty{}
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SkuID uuid.UUID `db:"sku_id"`
		Qty   money.Qty `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT sku_id, SUM(sellable - reserved) AS qty FROM sku_stock WHERE sku_id = ANY($1) GROUP BY sku_id`,
		skuIDs); err != nil {
		return nil, fmt.Errorf("app: read available stock: %w", err)
	}
	for _, r := range rows {
		out[r.SkuID] = r.Qty
	}
	return out, nil
}

func (s salesStock) Reserve(ctx context.Context, tx posting.Tx, skuID uuid.UUID, qty money.Qty, invoiceLineID uuid.UUID, docNo string) (sales.Reservation, error) {
	r, err := s.reservations.Reserve(ctx, tx, tx.TenantID(), tx.Now(), inventory.ReserveRequest{SkuID: skuID, Qty: qty,
		SourceType: inventory.ReserveInvoiceLine, SourceID: invoiceLineID, SourceDocNo: &docNo})
	return salesReservation(r), err
}

func (s salesStock) ReservationOf(ctx context.Context, q db.Querier, invoiceLineID uuid.UUID) (*sales.Reservation, error) {
	r, err := s.reservations.BySource(ctx, q, inventory.ReserveInvoiceLine, invoiceLineID)
	if err != nil || r == nil {
		return nil, err
	}
	out := salesReservation(*r)
	return &out, nil
}

func (s salesStock) Consume(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty money.Qty) error {
	_, err := s.reservations.Consume(ctx, tx, tx.TenantID(), tx.Now(), reservationID, qty)
	return err
}

func (s salesStock) Release(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty *money.Qty) error {
	_, err := s.reservations.Release(ctx, tx, tx.TenantID(), tx.Now(), reservationID, qty)
	return err
}

func (s salesStock) Adjust(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, total money.Qty) error {
	_, err := s.reservations.Adjust(ctx, tx, tx.TenantID(), tx.Now(), reservationID, total)
	return err
}

func salesReservation(r inventory.Reservation) sales.Reservation {
	return sales.Reservation{ID: r.ID, Qty: r.Qty, Consumed: r.ConsumedQty, Released: r.ReleasedQty,
		Active: r.Status == inventory.ReservationActive}
}

func salesMoves(moves []inventory.PostedMove) []sales.StockMove {
	out := make([]sales.StockMove, len(moves))
	for i, m := range moves {
		out[i] = sales.StockMove{ID: m.ID, SkuID: m.SkuID, LotID: m.LotID, ColorID: m.ColorID, DocLineID: m.DocLineID, Qty: m.Qty,
			UnitCost: m.UnitCost, Value: m.Value, MoveType: string(m.MoveType), LotNo: m.LotNo, Expiry: m.ExpiryDate,
			Quarantine: m.IsQuarantine}
	}
	return out
}

// salesDebt is the receivable side of the debt ledger as sales sees it, written inside the sales
// posting.
type salesDebt struct{ s *debt.Service }

func (d salesDebt) RecordReceivable(ctx context.Context, tx posting.Tx, in sales.ReceivableInput) (sales.Obligation, error) {
	var schedule []debt.InstallmentInput
	for _, s := range in.Schedule {
		schedule = append(schedule, debt.InstallmentInput{DueDate: s.DueDate.Time, Amount: s.Amount})
	}
	r, err := d.s.Receivables().Record(ctx, tx, debt.ObligationInput{PartyID: in.PartyID, SourceType: debt.SourceSalesDocument,
		SourceID: in.SourceID, SourceDocNo: in.SourceDocNo, Amount: in.Amount, DueDate: in.DueDate, Schedule: schedule,
		CampaignID: in.CampaignID, EarmarkedInvoiceID: in.EarmarkedInvoiceID})
	if err != nil {
		return sales.Obligation{}, err
	}
	return salesObligation(r.Obligation), nil
}

func (d salesDebt) RecordPayment(ctx context.Context, tx posting.Tx, in sales.PaymentInput) (sales.Payment, error) {
	var allocations []debt.Allocation
	for _, a := range in.Allocations {
		allocations = append(allocations, debt.Allocation{ObligationID: a.ObligationID, Amount: a.Amount, Mode: debt.ModeManual})
	}
	direction := debt.In
	if in.Direction == sales.DirectionOut {
		direction = debt.Out
	}
	r, err := d.s.Payments().Record(ctx, tx, debt.PaymentInput{Direction: direction, PartyID: in.PartyID, Purpose: in.Purpose,
		MethodID: in.MethodID, Amount: in.Amount, SourceType: in.SourceType, SourceID: in.SourceID,
		EarmarkedInvoiceID: in.EarmarkedInvoiceID, ShiftID: in.ShiftID, RefundOf: in.RefundOf, Note: in.Note,
		Allocations: allocations, Key: in.Key})
	if err != nil {
		return sales.Payment{}, err
	}
	return sales.Payment{ID: r.Payment.ID, DocNo: r.Payment.DocNo, Status: r.Payment.Status, Amount: r.Payment.Amount}, nil
}

func (d salesDebt) RecordCreditNote(ctx context.Context, tx posting.Tx, in sales.CreditNoteInput) (sales.CreditNote, error) {
	r, err := d.s.CreditNotes().Record(ctx, tx, debt.CreditNoteInput{PartyID: in.PartyID, Direction: debt.Receivable,
		SourceType: debt.CreditSalesReturn, SourceID: in.SourceID, SourceDocNo: in.SourceDocNo, Amount: in.Amount,
		ReasonID: in.ReasonID, Targets: in.Targets, OnlyTargets: in.OnlyTargets})
	if err != nil {
		return sales.CreditNote{}, err
	}
	out := sales.CreditNote{ID: r.Note.ID, Settled: r.Note.AllocatedAmount}
	if r.RefundDue != nil {
		due := salesObligation(*r.RefundDue)
		out.RefundDue = &due
	}
	return out, nil
}

// ReverseReceivable returns what the payments had settled on the receivable: the reversal rows
// of their settlements are negative, so the advance given back is minus their sum.
func (d salesDebt) ReverseReceivable(ctx context.Context, tx posting.Tx, salesDocumentID uuid.UUID, reason string) (money.Money, error) {
	r, err := d.s.Obligations().ReverseBySource(ctx, tx,
		debt.SourceRef{Direction: debt.Receivable, SourceType: debt.SourceSalesDocument, SourceID: salesDocumentID}, reason)
	if err != nil {
		return money.ZeroMoney, err
	}
	advance := money.ZeroMoney
	for _, s := range r.Released {
		advance = advance.Sub(s.Amount)
	}
	return advance, nil
}

func (d salesDebt) CheckCredit(ctx context.Context, q db.Querier, in sales.CreditQuery) error {
	c, err := d.s.CreditLimit().Check(ctx, q, debt.CreditCheckInput{PartyID: in.PartyID, OrderID: in.OrderID,
		Undelivered: in.Undelivered, AsOf: in.AsOf})
	if err != nil {
		return err
	}
	return c.Err()
}

func (d salesDebt) ReceivableOf(ctx context.Context, q db.Querier, salesDocumentID uuid.UUID) (*sales.Obligation, error) {
	var o sales.Obligation
	err := q.QueryRowxContext(ctx, `
		SELECT id, amount, settled_amount FROM obligations
		WHERE direction = $1 AND source_type = $2 AND source_id = $3`,
		debt.Receivable, debt.SourceSalesDocument, salesDocumentID).Scan(&o.ID, &o.Amount, &o.Settled)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("app: read receivable of sales document %s: %w", salesDocumentID, err)
	}
	return &o, nil
}

// InvoicesPaid counts, for each invoice, the confirmed deposits earmarked to it less their
// refunds, plus what other payments settled on the receivables of its sales documents that still
// stand (net of reversed settlements); the deposits' own settlements are already counted as
// deposits. One query serves a whole page of invoices.
func (d salesDebt) InvoicesPaid(ctx context.Context, q db.Querier, invoiceIDs []uuid.UUID) (map[uuid.UUID]sales.InvoicePaid, error) {
	out := make(map[uuid.UUID]sales.InvoicePaid, len(invoiceIDs))
	if len(invoiceIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID        uuid.UUID   `db:"id"`
		Confirmed money.Money `db:"confirmed"`
		Pending   money.Money `db:"pending"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		WITH deposits AS (
		  SELECT p.earmarked_invoice_id AS invoice_id, p.id, p.amount, p.status,
		         COALESCE((SELECT SUM(r.amount) FROM payments r WHERE r.refund_of = p.id AND r.status <> $3), 0) AS refunded
		  FROM payments p
		  WHERE p.earmarked_invoice_id = ANY($1) AND p.direction = $4 AND p.purpose = $5 AND p.status <> $3),
		deposited AS (
		  SELECT invoice_id, COALESCE(SUM(amount - refunded) FILTER (WHERE status = $2), 0) AS confirmed,
		         COALESCE(SUM(amount) FILTER (WHERE status = $9), 0) AS pending
		  FROM deposits GROUP BY invoice_id),
		settled AS (
		  SELECT sd.invoice_id, SUM(s.amount) AS amount
		  FROM sales_documents sd
		  JOIN obligations o ON o.source_id = sd.id AND o.direction = $6 AND o.source_type = $7
		  JOIN obligation_settlements s ON s.obligation_id = o.id
		  WHERE sd.invoice_id = ANY($1) AND sd.status <> $10 AND s.settled_by_type = $8
		    AND NOT EXISTS (SELECT 1 FROM deposits x WHERE x.id = s.settled_by_id AND x.invoice_id = sd.invoice_id)
		  GROUP BY sd.invoice_id)
		SELECT i.id, COALESCE(dp.confirmed, 0) + COALESCE(st.amount, 0) AS confirmed, COALESCE(dp.pending, 0) AS pending
		FROM unnest($1::uuid[]) AS i(id)
		LEFT JOIN deposited dp ON dp.invoice_id = i.id LEFT JOIN settled st ON st.invoice_id = i.id`,
		invoiceIDs, debt.PaymentConfirmed, debt.PaymentReversed, debt.In, debt.PurposeDeposit, debt.Receivable,
		debt.SourceSalesDocument, debt.SettledByPayment, debt.PaymentPending, sales.DocReversed); err != nil {
		return nil, fmt.Errorf("app: read what invoices were paid: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = sales.InvoicePaid{Confirmed: r.Confirmed, Pending: r.Pending}
	}
	return out, nil
}

// Deposits lists the deposits earmarked to an invoice. A refund of a deposit counts in its
// allocated amount, so what is still free is the amount less the allocated amount; a pending
// transfer has nothing free until it is verified.
func (d salesDebt) Deposits(ctx context.Context, q db.Querier, invoiceID uuid.UUID) ([]sales.Deposit, error) {
	var rows []struct {
		ID        uuid.UUID   `db:"id"`
		DocNo     string      `db:"doc_no"`
		Amount    money.Money `db:"amount"`
		Allocated money.Money `db:"allocated_amount"`
		Refunded  money.Money `db:"refunded"`
		Status    string      `db:"status"`
		MethodID  uuid.UUID   `db:"payment_method_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT p.id, p.doc_no, p.amount, p.allocated_amount, p.status, p.payment_method_id,
		       COALESCE((SELECT SUM(r.amount) FROM payments r WHERE r.refund_of = p.id AND r.status <> $2), 0) AS refunded
		FROM payments p
		WHERE p.earmarked_invoice_id = $1 AND p.direction = $3 AND p.purpose = $4 AND p.status <> $2
		ORDER BY p.posted_at, p.id`, invoiceID, debt.PaymentReversed, debt.In, debt.PurposeDeposit); err != nil {
		return nil, fmt.Errorf("app: read deposits of invoice %s: %w", invoiceID, err)
	}
	out := make([]sales.Deposit, len(rows))
	for i, r := range rows {
		pending := r.Status == debt.PaymentPending
		free := money.ZeroMoney
		if !pending {
			free = r.Amount.Sub(r.Allocated)
		}
		out[i] = sales.Deposit{ID: r.ID, DocNo: r.DocNo, Amount: r.Amount, Refunded: r.Refunded, Unallocated: free,
			Pending: pending, MethodID: r.MethodID}
	}
	return out, nil
}

func (d salesDebt) OpenReceivables(ctx context.Context, q db.Querier, partyID uuid.UUID) (money.Money, error) {
	var open money.Money
	if err := q.QueryRowxContext(ctx, `
		SELECT COALESCE(SUM(amount - settled_amount), 0) FROM obligations
		WHERE party_id = $1 AND direction = $2 AND status = $3`,
		partyID, debt.Receivable, debt.ObligationOpen).Scan(&open); err != nil {
		return money.ZeroMoney, fmt.Errorf("app: read open receivables of party %s: %w", partyID, err)
	}
	return open, nil
}

func salesObligation(o debt.Obligation) sales.Obligation {
	return sales.Obligation{ID: o.ID, Amount: o.Amount, Settled: o.SettledAmount}
}

// salesMethods reads the payment methods of the cash domain in the caller's transaction.
type salesMethods struct{}

func (salesMethods) Method(ctx context.Context, q db.Querier, id uuid.UUID) (sales.PaymentMethod, error) {
	m, err := cash.MethodFund(ctx, q, id)
	if err != nil {
		return sales.PaymentMethod{}, err
	}
	return salesMethod(m), nil
}

func (salesMethods) ActiveMethods(ctx context.Context, q db.Querier) ([]sales.PaymentMethod, error) {
	methods, err := cash.PaymentMethods(ctx, q)
	if err != nil {
		return nil, err
	}
	out := []sales.PaymentMethod{}
	for _, m := range methods {
		if m.Active {
			out = append(out, salesMethod(m))
		}
	}
	return out, nil
}

func salesMethod(m cash.PaymentMethod) sales.PaymentMethod {
	return sales.PaymentMethod{ID: m.ID, Code: m.Code, Name: m.Name, FundID: m.FundAccountID, FundKind: m.FundKind,
		Cash: m.FundKind == cash.FundCash, Active: m.Active}
}

// salesSessions keeps the offline POS window on the login session. Sessions are platform rows
// outside row level security, so every query filters the tenant explicitly.
type salesSessions struct {
	db    *db.DB
	clock clock.Clock
}

func (s salesSessions) OfflineUntil(ctx context.Context, tenantID, sessionID uuid.UUID) (*time.Time, error) {
	var until *time.Time
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			SELECT offline_until FROM sessions
			WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL AND expires_at > $3`,
			sessionID, tenantID, s.clock.Now()).Scan(&until)
		if db.IsNoRows(err) {
			return nil
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("app: read offline window of session %s: %w", sessionID, err)
	}
	return until, nil
}

func (s salesSessions) GrantOffline(ctx context.Context, tenantID, sessionID uuid.UUID, until time.Time) (time.Time, error) {
	var set time.Time
	found := true
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			UPDATE sessions SET offline_until = LEAST($3, expires_at)
			WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL AND expires_at > $4
			RETURNING offline_until`, sessionID, tenantID, until, s.clock.Now()).Scan(&set)
		if db.IsNoRows(err) {
			found = false
			return nil
		}
		return err
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("app: grant offline window to session %s: %w", sessionID, err)
	}
	if !found {
		return time.Time{}, apperr.NewUnauthenticated()
	}
	return set, nil
}
