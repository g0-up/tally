package kits

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// ComboPart is one stocked component a combo sale issues. ColorID is the recipe's fixed color;
// nil leaves the color to the counter.
type ComboPart struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	BaseQty money.Qty
}

func errComboNotSellable(message string) error {
	return apperr.NewValidation(CodeComboNotSellable, message, nil)
}

// ExpandCombo expands qty (in the combo's base unit) of a combo SKU into its components by the
// active recipe valid today, in the caller's transaction q. A combo without one, or a draft or
// inactive combo, is refused with 422 combo_not_sellable.
func (s *Service) ExpandCombo(ctx context.Context, q db.Querier, comboSkuID uuid.UUID, qty money.Qty) ([]ComboPart, error) {
	var p struct {
		ProductID     uuid.UUID `db:"product_id"`
		Type          string    `db:"type"`
		ProductStatus string    `db:"product_status"`
		SkuStatus     string    `db:"sku_status"`
	}
	err := q.QueryRowxContext(ctx, `
		SELECT p.id AS product_id, p.type, p.status AS product_status, s.status AS sku_status
		FROM skus s JOIN products p ON p.id = s.product_id WHERE s.id = $1`, comboSkuID).StructScan(&p)
	if db.IsNoRows(err) {
		return nil, errComboNotSellable("The combo does not exist")
	}
	if err != nil {
		return nil, fmt.Errorf("kits: read combo: %w", err)
	}
	switch {
	case p.Type != TypeCombo:
		return nil, errComboNotSellable("The SKU is not a combo")
	case p.ProductStatus != ProductActive || p.SkuStatus != ProductActive:
		return nil, errComboNotSellable("The combo is not published")
	}
	r, found, err := activeRecipe(ctx, q, p.ProductID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errComboNotSellable("The combo is not published")
	}
	today, err := s.todayIn(ctx, q)
	if err != nil {
		return nil, err
	}
	if !r.ValidOn(today) {
		return nil, errComboNotSellable("The combo's recipe is not valid today")
	}
	comps, err := readComponents(ctx, q, r.ID)
	if err != nil {
		return nil, err
	}
	return expandComponents(comps, qty)
}

// expandComponents multiplies each component's quantity per combo by qty, merging a SKU and color
// that appears twice (a SKU item and a mix pack flavor).
func expandComponents(comps []Component, qty money.Qty) ([]ComboPart, error) {
	out := make([]ComboPart, 0, len(comps))
	at := map[string]int{}
	for _, c := range comps {
		n, ok := mulExact(qty, c.BaseQty)
		if !ok {
			return nil, apperr.NewValidation("validation_failed", "The combo quantity is too precise",
				map[string]string{lineField(0, "qty"): "too_precise"})
		}
		key := c.SkuID.String()
		if c.ColorID != nil {
			key += "/" + c.ColorID.String()
		}
		if j, ok := at[key]; ok {
			out[j].BaseQty = out[j].BaseQty.Add(n)
			continue
		}
		at[key] = len(out)
		out = append(out, ComboPart{SkuID: c.SkuID, ColorID: c.ColorID, BaseQty: n})
	}
	return out, nil
}

// mulExact is a × b when the product fits the quantity scale exactly.
func mulExact(a, b money.Qty) (money.Qty, bool) {
	d := a.Decimal().Mul(b.Decimal())
	if !d.Equal(d.Round(4)) {
		return money.Qty{}, false
	}
	n, err := money.NewQty(d.String())
	return n, err == nil
}
