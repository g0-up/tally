package procurement

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// ReturnLineInput returns qty of one receipt line, in UnitID: the line's unit when nil, or the
// SKU's base unit.
type ReturnLineInput struct {
	ReceiptLineID uuid.UUID
	UnitID        *uuid.UUID
	Qty           money.Qty
}

// ReturnInput posts an XT: goods of one receipt going back to the supplier, each line from
// exactly the lot (and so the color) it was received into.
type ReturnInput struct {
	ReceiptID    uuid.UUID
	ReasonID     uuid.UUID
	Settlement   string
	BusinessDate *time.Time
	Lines        []ReturnLineInput
}

// ReturnDetail is an XT with its lines.
type ReturnDetail struct {
	PurchaseReturn
	Lines []PurchaseReturnLine
	// CreditNoteID is the credit note the return gave the store (none for a zero amount).
	CreditNoteID *uuid.UUID
}

func validateReturn(in ReturnInput) map[string]string {
	fields := map[string]string{}
	if in.ReceiptID == uuid.Nil {
		fields["receipt_id"] = "required"
	}
	if in.ReasonID == uuid.Nil {
		fields["reason_id"] = "required"
	}
	switch in.Settlement {
	case SettlementCreditPayable, SettlementRefund, SettlementReplacement:
	case "":
		fields["settlement"] = "required"
	default:
		fields["settlement"] = "invalid"
	}
	switch {
	case len(in.Lines) == 0:
		fields["lines"] = "required"
	case len(in.Lines) > MaxLines:
		fields["lines"] = "too_many"
	}
	seen := map[uuid.UUID]bool{}
	for i, l := range in.Lines {
		switch {
		case l.ReceiptLineID == uuid.Nil:
			fields[lineField(i, "receipt_line_id")] = "required"
		case seen[l.ReceiptLineID]:
			fields[lineField(i, "receipt_line_id")] = "duplicate"
		}
		seen[l.ReceiptLineID] = true
		if !l.Qty.IsPositive() {
			fields[lineField(i, "qty")] = "must_be_positive"
		}
	}
	return fields
}

// returnCredit is what the supplier owes back for base units of a line: the line's price per unit
// (after discount and tax) plus what price adjustments changed it by, in whole đồng. Landed cost
// was paid to others and is not credited.
func returnCredit(l ReceiptLine, adjustments []adjustedUnits, base money.Qty) (money.Money, error) {
	if !l.BaseQty.IsPositive() {
		return money.ZeroMoney, nil
	}
	v := l.LineAmount.Decimal().Mul(base.Decimal()).Div(l.BaseQty.Decimal())
	for _, a := range adjustments {
		if a.BaseQty.IsPositive() {
			v = v.Add(a.Amount.Decimal().Mul(base.Decimal()).Div(a.BaseQty.Decimal()))
		}
	}
	return money.RoundMoney(v.Round(0))
}

// adjustedUnits is the delta a price adjustment applied to the kept quantity of a receipt line.
type adjustedUnits struct {
	ReceiptLineID uuid.UUID   `db:"receipt_line_id"`
	Amount        money.Money `db:"amount"`
	BaseQty       money.Qty   `db:"base_qty"`
}

func lineAdjustments(ctx context.Context, q db.Querier, receiptID uuid.UUID) (map[uuid.UUID][]adjustedUnits, error) {
	var rows []adjustedUnits
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT l.receipt_line_id, l.on_hand_amount + l.sold_amount AS amount, l.base_qty
		FROM purchase_price_adjustment_lines l JOIN purchase_price_adjustments a ON a.id = l.purchase_price_adjustment_id
		WHERE a.receipt_id = $1 AND a.status = 'posted' AND a.reversal_of IS NULL
		ORDER BY a.posted_at, a.id`, receiptID); err != nil {
		return nil, wrap("read price adjustments", err)
	}
	out := map[uuid.UUID][]adjustedUnits{}
	for _, r := range rows {
		out[r.ReceiptLineID] = append(out[r.ReceiptLineID], r)
	}
	return out, nil
}

// receiptPayableID is the payable a receipt recorded (nil for a receipt of nothing owed).
func receiptPayableID(ctx context.Context, q db.Querier, receiptID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `SELECT id FROM obligations WHERE direction = 'payable' AND source_type = $1 AND source_id = $2`,
		DocReceipt, receiptID).Scan(&id)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap("read receipt payable", err)
	}
	return &id, nil
}

// PostReturn posts an XT. The goods leave the exact lot of each receipt line, valued at the
// line's unit cost; the store gets a credit note settling the receipt's payable first
// (credit_payable, replacement) or a refund due from the supplier (refund).
//
// replacement is booked exactly like credit_payable: the credit note is issued now, and the
// replacement goods arrive later on a new receipt at the original price, which records a new
// payable. The two cancel out, so what the store owes in the end is unchanged; the client must
// lead the user to post that replacement receipt.
func (s *Service) PostReturn(ctx context.Context, actor audit.Actor, key idempotency.Key, in ReturnInput) (posting.Response, error) {
	if fields := validateReturn(in); len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	date, err := s.postingDate(ctx, actor.TenantID, in.BusinessDate)
	if err != nil {
		return posting.Response{}, err
	}
	skus, err := s.receiptSkus(ctx, actor.TenantID, in.ReceiptID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, returnPost{s: s, date: date, in: in, skus: skus, key: ScopeReturnPost + ":" + key.Value})
}

type returnPost struct {
	s    *Service
	date time.Time
	in   ReturnInput
	skus []uuid.UUID
	key  string
}

func (c returnPost) Scope() string           { return ScopeReturnPost }
func (c returnPost) BusinessDate() time.Time { return c.date }
func (c returnPost) DocPrefix() string       { return docseq.PrefixSupplierReturn }

func (c returnPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	return set
}

func (c returnPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := getReceipt(ctx, tx, c.in.ReceiptID, true)
	if err != nil {
		return posting.Result{}, err
	}
	if r.Status != DocPosted || r.ReversalOf != nil {
		return posting.Result{}, errReceiptNotPosted()
	}
	if c.date.Before(r.BusinessDate) {
		return posting.Result{}, errInvalid(map[string]string{"business_date": "before_receipt"})
	}
	if err := checkReason(ctx, tx, c.in.ReasonID, ReasonPurchaseReturn, "reason_id"); err != nil {
		return posting.Result{}, err
	}
	all, err := receiptLines(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}
	byID := make(map[uuid.UUID]ReceiptLine, len(all))
	skuIDs := make([]uuid.UUID, 0, len(all))
	for _, l := range all {
		byID[l.ID] = l
		skuIDs = append(skuIDs, l.SkuID)
	}
	infos, err := readSkuInfos(ctx, tx, skuIDs)
	if err != nil {
		return posting.Result{}, err
	}
	adjusted, err := lineAdjustments(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}

	x := PurchaseReturn{ID: ids.New(), WarehouseID: r.WarehouseID, DocNo: tx.DocNo(), SupplierPartyID: r.SupplierPartyID,
		SupplierName: r.SupplierName, ReceiptID: r.ID, ReasonID: c.in.ReasonID, Settlement: c.in.Settlement, BusinessDate: c.date,
		TotalAmount: money.ZeroMoney, Status: DocPosted, IdempotencyKey: c.key, PostedAt: tx.Now(), PostedBy: tx.Actor().ID,
		CreatedAt: tx.Now()}
	fields := map[string]string{}
	lines := make([]PurchaseReturnLine, 0, len(c.in.Lines))
	unitIDs := []uuid.UUID{}
	for i, in := range c.in.Lines {
		rl, ok := byID[in.ReceiptLineID]
		switch {
		case !ok:
			fields[lineField(i, "receipt_line_id")] = "not_on_receipt"
			continue
		case !inStock(rl.Condition):
			fields[lineField(i, "receipt_line_id")] = "not_returnable"
			continue
		}
		unitID, factor := rl.UnitID, rl.UnitFactorSnap
		if in.UnitID != nil && *in.UnitID != rl.UnitID {
			if *in.UnitID != infos[rl.SkuID].BaseUnitID {
				fields[lineField(i, "unit_id")] = "not_receipt_unit"
				continue
			}
			unitID, factor = *in.UnitID, money.FactorOne
		}
		base, err := money.BaseQty(in.Qty, factor)
		if err != nil {
			fields[lineField(i, "qty")] = "invalid"
			continue
		}
		if base.Cmp(rl.KeptBaseQty()) > 0 {
			return posting.Result{}, apperr.NewConflict(CodeReturnExceedsKept, fmt.Sprintf(
				"Line %d of the receipt has %s left to return but %s was asked", rl.LineNo, rl.KeptBaseQty(), base))
		}
		credit, err := returnCredit(rl, adjusted[rl.ID], base)
		if err != nil {
			return posting.Result{}, err
		}
		lines = append(lines, PurchaseReturnLine{ID: ids.New(), PurchaseReturnID: x.ID, ReceiptLineID: rl.ID, LotID: *rl.LotID,
			SkuID: rl.SkuID, SkuCodeSnap: rl.SkuCodeSnap, NameSnap: rl.NameSnap, VariantLabelSnap: rl.VariantLabelSnap,
			UnitID: unitID, UnitFactorSnap: factor, Qty: in.Qty, BaseQty: base, ColorID: rl.ColorID, SpecText: rl.SpecText,
			UnitCost: rl.UnitCost, LineAmount: credit, LotNo: rl.LotNo})
		unitIDs = append(unitIDs, unitID)
		x.TotalAmount = x.TotalAmount.Add(credit)
	}
	if len(fields) > 0 {
		return posting.Result{}, errInvalid(fields)
	}
	names, err := unitNames(ctx, tx, unitIDs)
	if err != nil {
		return posting.Result{}, err
	}
	for i := range lines {
		lines[i].UnitNameSnap = names[lines[i].UnitID]
	}

	if err := c.issueStock(ctx, tx, x.ID, lines); err != nil {
		return posting.Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO purchase_returns (id, tenant_id, warehouse_id, doc_no, supplier_party_id, receipt_id, reason_id, settlement,
		  business_date, total_amount, status, idempotency_key, posted_at, posted_by, reversal_of, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NULL, $15)`,
		x.ID, tx.TenantID(), x.WarehouseID, x.DocNo, x.SupplierPartyID, x.ReceiptID, x.ReasonID, x.Settlement, x.BusinessDate,
		x.TotalAmount, x.Status, x.IdempotencyKey, x.PostedAt, x.PostedBy, x.CreatedAt); err != nil {
		return posting.Result{}, wrap("insert purchase return", err)
	}
	for _, l := range lines {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO purchase_return_lines (id, tenant_id, purchase_return_id, receipt_line_id, lot_id, sku_id, sku_code_snap,
			  name_snap, variant_label_snap, unit_id, unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, unit_cost,
			  line_amount)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			l.ID, tx.TenantID(), l.PurchaseReturnID, l.ReceiptLineID, l.LotID, l.SkuID, l.SkuCodeSnap, l.NameSnap,
			l.VariantLabelSnap, l.UnitID, l.UnitNameSnap, l.UnitFactorSnap, l.Qty, l.BaseQty, l.ColorID, l.SpecText, l.UnitCost,
			l.LineAmount); err != nil {
			return posting.Result{}, wrap("insert purchase return line", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE receipt_lines SET returned_base_qty = returned_base_qty + $2 WHERE id = $1`,
			l.ReceiptLineID, l.BaseQty); err != nil {
			return posting.Result{}, wrap("move receipt line returned quantity", err)
		}
		if ol := byID[l.ReceiptLineID].PurchaseOrderLineID; ol != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE purchase_order_lines SET returned_base_qty = returned_base_qty + $2 WHERE id = $1`,
				*ol, l.BaseQty); err != nil {
				return posting.Result{}, wrap("move purchase order returned quantity", err)
			}
		}
	}

	d := ReturnDetail{PurchaseReturn: x, Lines: lines}
	if x.TotalAmount.IsPositive() {
		credit := CreditInput{PartyID: x.SupplierPartyID, SourceType: DocPurchaseReturn, SourceID: x.ID, SourceDocNo: x.DocNo,
			Amount: x.TotalAmount, ReasonID: &x.ReasonID}
		if c.in.Settlement == SettlementRefund {
			// The supplier pays the money back: nothing is settled, all of it is a refund due.
			credit.OnlyTargets = true
		} else {
			payable, err := receiptPayableID(ctx, tx, r.ID)
			if err != nil {
				return posting.Result{}, err
			}
			if payable != nil {
				credit.Targets = []uuid.UUID{*payable}
			}
		}
		note, err := c.s.debt.Credit(ctx, tx, credit)
		if err != nil {
			return posting.Result{}, err
		}
		d.CreditNoteID = &note.ID
	}
	tx.Audit(audit.Event{Action: "purchase_return.post", EntityType: DocPurchaseReturn, EntityID: x.ID, DocNo: x.DocNo, After: d})
	return posting.Result{Status: http.StatusCreated, Body: toReturnDTO(d)}, nil
}

// issueStock takes each line out of its receipt lot. The ledger values an issue at the SKU's
// average cost; a purchase return leaves at what the goods cost on the receipt, so the difference
// is booked back on the SKU's stock value. Stock value never goes below zero and cannot move once
// the SKU holds nothing: the part the stock cannot carry belongs to goods already sold at the
// average cost, so it is a COGS adjustment of the return. Stock adjustment + COGS adjustment is the
// difference exactly, and the return leaves at its receipt cost.
func (c returnPost) issueStock(ctx context.Context, tx posting.Tx, returnID uuid.UUID, lines []PurchaseReturnLine) error {
	doc := StockDoc{Type: DocPurchaseReturn, ID: returnID}
	issues := make([]StockIssue, len(lines))
	for i, l := range lines {
		issues[i] = StockIssue{LineID: l.ID, SkuID: l.SkuID, Qty: l.BaseQty, LotID: l.LotID}
	}
	moves, err := c.s.stock.Issue(ctx, tx, doc, issues)
	if err != nil {
		return remapLines(err, nil)
	}
	issued := map[uuid.UUID]money.Money{}
	for _, m := range moves {
		if m.LineID != nil {
			issued[*m.LineID] = issued[*m.LineID].Add(m.Value.Neg())
		}
	}
	bySku := map[uuid.UUID]money.Money{}
	lineOf := map[uuid.UUID]uuid.UUID{}
	for _, l := range lines {
		basis := l.UnitCost.MulQty(l.BaseQty)
		bySku[l.SkuID] = bySku[l.SkuID].Add(issued[l.ID].Sub(basis))
		lineOf[l.SkuID] = l.ID
	}
	stock, err := readSkuStock(ctx, tx, sortedKeys(bySku))
	if err != nil {
		return err
	}
	var costs []StockCost
	cogs := map[uuid.UUID]money.Money{}
	for _, sku := range sortedKeys(bySku) {
		delta := bySku[sku]
		if delta.IsZero() {
			continue
		}
		booked := stockShare(delta, stock[sku])
		cogs[sku] = delta.Sub(booked)
		if booked.IsZero() {
			continue
		}
		lineID := lineOf[sku]
		costs = append(costs, StockCost{LineID: &lineID, SkuID: sku, Value: booked})
	}
	if len(costs) > 0 {
		if _, err := c.s.stock.CostAdjust(ctx, tx, doc, costs); err != nil {
			return err
		}
	}
	return insertCogs(ctx, tx, tx.TenantID(), DocPurchaseReturn, returnID, c.date, tx.Now(), cogs, ids.New)
}

// stockShare is the part of a cost difference the SKU's stock value can carry: none once it holds
// nothing, and never more than takes the value to zero.
func stockShare(delta money.Money, st skuStock) money.Money {
	switch {
	case !st.OnHand.IsPositive():
		return money.ZeroMoney
	case st.StockValue.Add(delta).IsNegative():
		return st.StockValue.Neg()
	}
	return delta
}
