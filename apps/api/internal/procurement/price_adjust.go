package procurement

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// AdjustmentLineInput is the new unit price of a receipt line, in the line's unit and on the
// same basis as its unit price (before discount, tax as the line was priced).
type AdjustmentLineInput struct {
	ReceiptLineID uuid.UUID
	NewUnitPrice  money.Money
}

// AdjustmentInput posts a DG on one receipt: the supplier's final price (up or down) or a
// discount granted after the purchase (down only).
type AdjustmentInput struct {
	ReceiptID    uuid.UUID
	Kind         string
	ReasonID     uuid.UUID
	BusinessDate *time.Time
	Lines        []AdjustmentLineInput
}

// AdjustmentDetail is a DG with its lines and the debt it moved: a payable when the price went
// up, a credit note when it went down.
type AdjustmentDetail struct {
	PriceAdjustment
	Lines        []PriceAdjustmentLine
	PayableID    *uuid.UUID
	CreditNoteID *uuid.UUID
}

func validateAdjustment(in AdjustmentInput) map[string]string {
	fields := map[string]string{}
	if in.ReceiptID == uuid.Nil {
		fields["receipt_id"] = "required"
	}
	if in.ReasonID == uuid.Nil {
		fields["reason_id"] = "required"
	}
	switch in.Kind {
	case KindFinalPrice, KindPostPurchaseDiscount:
	case "":
		fields["kind"] = "required"
	default:
		fields["kind"] = "invalid"
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
		if l.NewUnitPrice.IsNegative() {
			fields[lineField(i, "new_unit_price")] = "must_not_be_negative"
		}
	}
	return fields
}

// lineNet is what a line is owed before tax at unit price p: p × qty less the line discount, and
// never below zero, since a discount cannot make the supplier owe the store for the line.
func lineNet(l ReceiptLine, p money.Money) money.Money {
	net := p.MulQty(l.Qty).Sub(l.DiscountAmount)
	if net.IsNegative() {
		return money.ZeroMoney
	}
	return net
}

// priceToAmount is how much the line amount moves per đồng of its net: the line's amount over its
// net before tax (so tax added on top moves with the price). A line received at a zero net has no
// amount to read that from, so its tax rate gives it: 1 + rate / 100 when the receipt added tax on
// top, 1 when its prices held the tax.
func priceToAmount(l ReceiptLine, taxOnTop bool) decimal.Decimal {
	net := lineNet(l, l.UnitPrice)
	switch {
	case net.IsPositive():
		return l.LineAmount.Decimal().Div(net.Decimal())
	case taxOnTop:
		return decimal.NewFromInt(1).Add(l.TaxRateSnap.Decimal().Div(decimal.NewFromInt(100)))
	}
	return decimal.NewFromInt(1)
}

// taxAddedOnTop reports whether receipt r priced its lines without tax and added it on top. A
// receipt that carries tax shows which in its totals (total = goods − discount + tax only when
// tax was added); one without falls back to the store's current setting.
func taxAddedOnTop(ctx context.Context, q db.Querier, tenantID uuid.UUID, r Receipt) (bool, error) {
	if r.TaxAmount.IsPositive() {
		return r.TotalAmount.Equal(r.GoodsAmount.Sub(r.DiscountAmount).Add(r.TaxAmount)), nil
	}
	var includeTax bool
	if err := q.QueryRowxContext(ctx, `SELECT prices_include_tax FROM tenant_settings WHERE tenant_id = $1`, tenantID).
		Scan(&includeTax); err != nil {
		return false, wrap("read prices_include_tax", err)
	}
	return !includeTax, nil
}

// adjustmentDelta is what changing the unit price of a line from oldPrice to newPrice changes
// the amount owed for its kept quantity by (whole đồng): the kept share of how far the line's net
// moves. A drop therefore takes at most what the kept units are still owed.
func adjustmentDelta(l ReceiptLine, oldPrice, newPrice money.Money, taxOnTop bool) (money.Money, error) {
	if !l.BaseQty.IsPositive() {
		return money.ZeroMoney, nil
	}
	moved := lineNet(l, newPrice).Sub(lineNet(l, oldPrice)).Decimal().Mul(priceToAmount(l, taxOnTop))
	return money.RoundMoney(moved.Mul(l.KeptBaseQty().Decimal()).Div(l.BaseQty.Decimal()).Round(0))
}

// carriedCost is the part of a DG's delta the cost of a line takes, spread over its kept units
// like a landed cost, and the unit cost it leaves. A drop is at most what the kept units are
// owed, which their cost includes, so it takes their cost to zero at most; should the cost they
// carry still fall short (it is rounded to 4 decimals a unit), the unit cost stops at zero and the
// rest is a COGS adjustment of the DG, as for the part of a purchase return the stock cannot carry.
func carriedCost(l ReceiptLine, delta money.Money) (carried, residue, unitCost money.Money, err error) {
	floor := l.UnitCost.MulQty(l.KeptBaseQty()).Neg()
	if delta.Cmp(floor) < 0 {
		return floor, delta.Sub(floor), money.ZeroMoney, nil
	}
	per, err := perKeptUnit(l, delta)
	if err != nil {
		return money.ZeroMoney, money.ZeroMoney, money.ZeroMoney, err
	}
	unitCost = l.UnitCost.Add(per)
	if unitCost.IsNegative() {
		// Only the rounding of per to 4 decimals can take a cost carried to the floor below zero.
		unitCost = money.ZeroMoney
	}
	return delta, money.ZeroMoney, unitCost, nil
}

// currentPrices is the unit price each line of a receipt is owed at now: the last adjustment's
// new price, else the received price.
func currentPrices(ctx context.Context, q db.Querier, receiptID uuid.UUID) (map[uuid.UUID]money.Money, error) {
	var rows []struct {
		ReceiptLineID uuid.UUID   `db:"receipt_line_id"`
		Price         money.Money `db:"new_unit_price"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT DISTINCT ON (l.receipt_line_id) l.receipt_line_id, l.new_unit_price
		FROM purchase_price_adjustment_lines l JOIN purchase_price_adjustments a ON a.id = l.purchase_price_adjustment_id
		WHERE a.receipt_id = $1 AND a.status = 'posted' AND a.reversal_of IS NULL
		ORDER BY l.receipt_line_id, a.posted_at DESC, a.id DESC`, receiptID); err != nil {
		return nil, wrap("read adjusted prices", err)
	}
	out := make(map[uuid.UUID]money.Money, len(rows))
	for _, r := range rows {
		out[r.ReceiptLineID] = r.Price
	}
	return out, nil
}

// PostAdjustment posts a DG. The difference on the kept quantity moves the payable (a new payable
// when up, a credit note on the receipt's payable when down) and the cost of the goods: the part
// still in stock through the ledger, the part sold as a COGS adjustment. The difference is the
// kept share of how far the line's net (price × qty less its discount, never below zero) moves,
// so a drop never credits more than the line is still owed.
func (s *Service) PostAdjustment(ctx context.Context, actor audit.Actor, key idempotency.Key, in AdjustmentInput) (posting.Response, error) {
	if fields := validateAdjustment(in); len(fields) > 0 {
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
	return s.posting.Post(ctx, actor, key, adjustmentPost{s: s, date: date, in: in, skus: skus,
		key: ScopeAdjustmentPost + ":" + key.Value})
}

type adjustmentPost struct {
	s    *Service
	date time.Time
	in   AdjustmentInput
	skus []uuid.UUID
	key  string
}

func (c adjustmentPost) Scope() string           { return ScopeAdjustmentPost }
func (c adjustmentPost) BusinessDate() time.Time { return c.date }
func (c adjustmentPost) DocPrefix() string       { return docseq.PrefixCostAdjustment }

func (c adjustmentPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	return set
}

func (c adjustmentPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
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
	if err := checkReason(ctx, tx, c.in.ReasonID, ReasonPriceAdjustment, "reason_id"); err != nil {
		return posting.Result{}, err
	}
	all, err := receiptLines(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}
	byID := make(map[uuid.UUID]ReceiptLine, len(all))
	for _, l := range all {
		byID[l.ID] = l
	}
	prices, err := currentPrices(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}
	taxOnTop, err := taxAddedOnTop(ctx, tx, tx.TenantID(), r)
	if err != nil {
		return posting.Result{}, err
	}

	a := PriceAdjustment{ID: ids.New(), DocNo: tx.DocNo(), SupplierPartyID: r.SupplierPartyID, SupplierName: r.SupplierName,
		ReceiptID: r.ID, Kind: c.in.Kind, ReasonID: c.in.ReasonID, BusinessDate: c.date, PayableDelta: money.ZeroMoney,
		Status: DocPosted, IdempotencyKey: c.key, PostedAt: tx.Now(), PostedBy: tx.Actor().ID, CreatedAt: tx.Now()}
	fields := map[string]string{}
	lines := make([]ReceiptLine, 0, len(c.in.Lines))
	out := make([]PriceAdjustmentLine, 0, len(c.in.Lines))
	carried, costs := []money.Money{}, []money.Money{}
	residues := map[uuid.UUID]money.Money{}
	for i, in := range c.in.Lines {
		l, ok := byID[in.ReceiptLineID]
		switch {
		case !ok:
			fields[lineField(i, "receipt_line_id")] = "not_on_receipt"
			continue
		case !inStock(l.Condition):
			fields[lineField(i, "receipt_line_id")] = "not_adjustable"
			continue
		case !l.KeptBaseQty().IsPositive():
			fields[lineField(i, "receipt_line_id")] = "nothing_kept"
			continue
		}
		old, ok := prices[l.ID]
		if !ok {
			old = l.UnitPrice
		}
		switch cmp := in.NewUnitPrice.Cmp(old); {
		case cmp == 0:
			fields[lineField(i, "new_unit_price")] = "unchanged"
			continue
		case cmp > 0 && c.in.Kind == KindPostPurchaseDiscount:
			fields[lineField(i, "new_unit_price")] = "not_a_discount"
			continue
		}
		delta, err := adjustmentDelta(l, old, in.NewUnitPrice, taxOnTop)
		if err != nil {
			return posting.Result{}, err
		}
		onCost, residue, cost, err := carriedCost(l, delta)
		if err != nil {
			return posting.Result{}, err
		}
		lines = append(lines, l)
		carried, costs = append(carried, onCost), append(costs, cost)
		residues[l.SkuID] = residues[l.SkuID].Add(residue)
		out = append(out, PriceAdjustmentLine{ReceiptLineID: l.ID, OldUnitPrice: old, NewUnitPrice: in.NewUnitPrice,
			BaseQty: l.KeptBaseQty()})
		a.PayableDelta = a.PayableDelta.Add(delta)
	}
	if len(fields) > 0 {
		return posting.Result{}, errInvalid(fields)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO purchase_price_adjustments (id, tenant_id, doc_no, supplier_party_id, receipt_id, kind, reason_id, business_date,
		  payable_delta, status, idempotency_key, posted_at, posted_by, reversal_of, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULL, $14)`,
		a.ID, tx.TenantID(), a.DocNo, a.SupplierPartyID, a.ReceiptID, a.Kind, a.ReasonID, a.BusinessDate, a.PayableDelta, a.Status,
		a.IdempotencyKey, a.PostedAt, a.PostedBy, a.CreatedAt); err != nil {
		return posting.Result{}, wrap("insert purchase price adjustment", err)
	}
	splits, err := c.s.costShares(ctx, tx, DocPriceAdjustment, a.ID, c.date, lines, carried, residues)
	if err != nil {
		return posting.Result{}, err
	}
	for i := range out {
		out[i].OnHandAmount, out[i].SoldAmount = splits[i].OnHandAmount, splits[i].SoldAmount
		l := out[i]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO purchase_price_adjustment_lines (tenant_id, purchase_price_adjustment_id, receipt_line_id, old_unit_price,
			  new_unit_price, base_qty, on_hand_amount, sold_amount)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, tx.TenantID(), a.ID, l.ReceiptLineID, l.OldUnitPrice, l.NewUnitPrice,
			l.BaseQty, l.OnHandAmount, l.SoldAmount); err != nil {
			return posting.Result{}, wrap("insert purchase price adjustment line", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE receipt_lines SET unit_cost = $2 WHERE id = $1`, l.ReceiptLineID, costs[i]); err != nil {
			return posting.Result{}, wrap("move receipt line unit cost", err)
		}
	}

	d := AdjustmentDetail{PriceAdjustment: a, Lines: out}
	switch {
	case a.PayableDelta.IsPositive():
		p, err := c.s.debt.RecordPayable(ctx, tx, PayableInput{PartyID: a.SupplierPartyID, SourceType: DocPriceAdjustment,
			SourceID: a.ID, SourceDocNo: a.DocNo, Amount: a.PayableDelta})
		if err != nil {
			return posting.Result{}, err
		}
		d.PayableID = &p.ID
	case a.PayableDelta.IsNegative():
		credit := CreditInput{PartyID: a.SupplierPartyID, SourceType: DocPriceAdjustment, SourceID: a.ID, SourceDocNo: a.DocNo,
			Amount: a.PayableDelta.Neg(), ReasonID: &a.ReasonID}
		payable, err := receiptPayableID(ctx, tx, r.ID)
		if err != nil {
			return posting.Result{}, err
		}
		if payable != nil {
			credit.Targets = []uuid.UUID{*payable}
		}
		note, err := c.s.debt.Credit(ctx, tx, credit)
		if err != nil {
			return posting.Result{}, err
		}
		d.CreditNoteID = &note.ID
	}
	// A DG is how a variance against the supplier's document is resolved: the documents matched
	// to the receipt are matched again at the adjusted amount.
	docs, err := documentsOfReceipt(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}
	if err := refreshMatches(ctx, tx, docs, tx.Now()); err != nil {
		return posting.Result{}, err
	}
	tx.Audit(audit.Event{Action: "purchase_price_adjustment.post", EntityType: DocPriceAdjustment, EntityID: a.ID, DocNo: a.DocNo,
		After: d})
	return posting.Result{Status: http.StatusCreated, Body: toAdjustmentDTO(d)}, nil
}
