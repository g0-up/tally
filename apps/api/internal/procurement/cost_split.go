package procurement

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// A landed cost or price adjustment changes what the goods of a receipt cost after they were
// received. Some of them may already be sold: the part of a line's share on the units its lot
// still holds goes into stock value (a cost_adjustment move), the rest is a COGS adjustment of
// the period. on_hand + sold is the line's share exactly.

// keptValue is the value of what a line still holds for the supplier: its amount over the
// received quantity, for the quantity not returned.
func keptValue(l ReceiptLine) (money.Money, error) {
	if !l.BaseQty.IsPositive() {
		return money.ZeroMoney, nil
	}
	return money.RoundMoney(l.LineAmount.Decimal().Mul(l.KeptBaseQty().Decimal()).Div(l.BaseQty.Decimal()))
}

// perKeptUnit is what booking amount on a line adds to the cost of each unit it still keeps. A
// landed cost or price adjustment is spread over the kept units only (returned goods already left
// at their old cost), so the unit cost moves by amount / kept, not by amount / received: a return
// of the rest then takes out exactly what stock was given.
func perKeptUnit(l ReceiptLine, amount money.Money) (money.Money, error) {
	kept := l.KeptBaseQty()
	if !kept.IsPositive() {
		return money.ZeroMoney, nil
	}
	return money.RoundMoney(amount.Decimal().Div(kept.Decimal()))
}

// splitShare splits share between the held units of kept (still in the lot) and the sold ones.
// The held part is rounded to whole đồng; the sold part takes the remainder.
func splitShare(share money.Money, held, kept money.Qty) (onHand, sold money.Money, err error) {
	switch {
	case !kept.IsPositive() || !held.IsPositive():
		return money.ZeroMoney, share, nil
	case held.Cmp(kept) >= 0:
		return share, money.ZeroMoney, nil
	}
	onHand, err = money.RoundMoney(share.Decimal().Mul(held.Decimal()).Div(kept.Decimal()).Round(0))
	if err != nil {
		return money.ZeroMoney, money.ZeroMoney, err
	}
	return onHand, share.Sub(onHand), nil
}

// costShares books shares[i] on lines[i]: the held part on the line's lot through the ledger, the
// sold part as COGS adjustments of docType per SKU, together with the residue the document books
// on COGS directly. The lot balance is shared by every receipt of the same lot, so "held" is what
// the lot holds up to the line's kept quantity. The held part moves the SKU's average-cost stock
// value, which other lots sold at that average may have left below the line's own cost: what the
// stock value cannot carry belongs to goods already sold and joins the sold part, as for a
// purchase return. Lines are taken in the order the ledger books them.
func (s *Service) costShares(ctx context.Context, tx posting.Tx, docType string, docID uuid.UUID, date time.Time,
	lines []ReceiptLine, shares []money.Money, residue map[uuid.UUID]money.Money) ([]SplitLine, error) {
	lotIDs := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		if l.LotID != nil {
			lotIDs = append(lotIDs, *l.LotID)
		}
	}
	held, err := lotQtys(ctx, tx, lotIDs)
	if err != nil {
		return nil, err
	}
	skuIDs := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		skuIDs[i] = l.SkuID
	}
	stock, err := readSkuStock(ctx, tx, skuIDs)
	if err != nil {
		return nil, err
	}
	out := make([]SplitLine, len(lines))
	var costs []StockCost
	cogs := map[uuid.UUID]money.Money{}
	for sku, amount := range residue {
		cogs[sku] = amount
	}
	for i, l := range lines {
		h := money.ZeroQty
		if l.LotID != nil {
			h = minQty(held[*l.LotID], l.KeptBaseQty())
		}
		onHand, sold, err := splitShare(shares[i], h, l.KeptBaseQty())
		if err != nil {
			return nil, err
		}
		st := stock[l.SkuID]
		booked := stockShare(onHand, st)
		onHand, sold = booked, sold.Add(onHand.Sub(booked))
		st.StockValue = st.StockValue.Add(booked)
		stock[l.SkuID] = st
		out[i] = SplitLine{ReceiptLineID: l.ID, Amount: shares[i], OnHandAmount: onHand, SoldAmount: sold}
		if !onHand.IsZero() {
			lineID, lotID := l.ID, *l.LotID
			costs = append(costs, StockCost{LineID: &lineID, SkuID: l.SkuID, LotID: &lotID, Value: onHand})
		}
		cogs[l.SkuID] = cogs[l.SkuID].Add(sold)
	}
	if len(costs) > 0 {
		if _, err := s.stock.CostAdjust(ctx, tx, StockDoc{Type: docType, ID: docID}, costs); err != nil {
			return nil, err
		}
	}
	if err := insertCogs(ctx, tx, tx.TenantID(), docType, docID, date, tx.Now(), cogs, ids.New); err != nil {
		return nil, err
	}
	return out, nil
}
