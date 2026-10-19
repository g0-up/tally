package kits

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// SuggestCandidate is a component the customer may get in a budget kit: at least MinQty and at
// most MaxQty (nil: no cap) base units, of ColorID when given.
type SuggestCandidate struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	MinQty  money.Qty
	MaxQty  *money.Qty
}

// SuggestInput asks for a kit whose components cost the customer at most Budget at retail
// prices, packed at PackingCost.
type SuggestInput struct {
	Budget      money.Money
	PackingCost money.Money
	Candidates  []SuggestCandidate
}

// SuggestLine is one component of a suggested kit.
type SuggestLine struct {
	SkuID      uuid.UUID
	SkuCode    string
	Name       string
	ColorID    *uuid.UUID
	Qty        money.Qty
	UnitPrice  money.Money
	Amount     money.Money
	UnitCost   money.Money
	Cost       money.Money
	UnitMargin money.Money
	// Sellable is the free stock the quantity was capped by.
	Sellable money.Qty
}

// Suggestion is a proposed budget kit with its expected price and margin. It is a quote only:
// nothing is saved, reserved or changed on any document.
type Suggestion struct {
	Budget         money.Money
	Lines          []SuggestLine
	ExpectedPrice  money.Money
	ComponentsCost money.Money
	PackingCost    money.Money
	TotalCost      money.Money
	Margin         money.Money
	BudgetLeft     money.Money
}

func validateSuggest(in SuggestInput) map[string]string {
	fields := map[string]string{}
	if !in.Budget.IsPositive() {
		fields["budget"] = "must_be_positive"
	}
	if in.PackingCost.IsNegative() {
		fields["packing_cost"] = "must_not_be_negative"
	}
	switch {
	case len(in.Candidates) == 0:
		fields["candidates"] = "required"
	case len(in.Candidates) > MaxItems:
		fields["candidates"] = "too_many"
	}
	seen := map[[2]uuid.UUID]bool{}
	for i, c := range in.Candidates {
		f := func(name string) string { return fmt.Sprintf("candidates.%d.%s", i, name) }
		if c.SkuID == uuid.Nil {
			fields[f("sku_id")] = "required"
		}
		k := [2]uuid.UUID{c.SkuID}
		if c.ColorID != nil {
			k[1] = *c.ColorID
		}
		if seen[k] {
			fields[f("sku_id")] = "duplicate"
		}
		seen[k] = true
		if c.MinQty.IsNegative() || !c.MinQty.IsWhole() {
			fields[f("min_qty")] = "must_be_whole"
		}
		if c.MaxQty != nil && (!c.MaxQty.IsPositive() || !c.MaxQty.IsWhole() || c.MaxQty.Cmp(c.MinQty) < 0) {
			fields[f("max_qty")] = "invalid"
		}
	}
	return fields
}

// suggestOption is a candidate with its price, cost and free stock.
type suggestOption struct {
	index int
	cand  SuggestCandidate
	line  SuggestLine
	// limit is the most the kit may hold: the free stock, capped by MaxQty.
	limit money.Qty
}

// Suggest proposes a budget kit (KIT-09): every candidate at its minimum, then, best unit margin
// first, as many whole units of each as the budget, its cap and its free stock allow.
func (s *Service) Suggest(ctx context.Context, scope Scope, in SuggestInput) (Suggestion, error) {
	if fields := validateSuggest(in); len(fields) > 0 {
		return Suggestion{}, errInvalid(fields)
	}
	var out Suggestion
	err := s.read(ctx, scope, func(q db.Querier) error {
		opts, err := s.suggestOptions(ctx, q, scope.TenantID, in.Candidates)
		if err != nil {
			return err
		}
		out, err = suggest(in, opts)
		return err
	})
	return out, err
}

func (s *Service) suggestOptions(ctx context.Context, q db.Querier, tenantID uuid.UUID, cands []SuggestCandidate) ([]suggestOption, error) {
	items := make([]ItemInput, len(cands))
	skuIDs := make([]uuid.UUID, len(cands))
	for i, c := range cands {
		sku := c.SkuID
		items[i] = ItemInput{SkuID: &sku, ColorID: c.ColorID}
		skuIDs[i] = c.SkuID
	}
	facts, err := loadRecipeFacts(ctx, q, items)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{}
	for i, c := range cands {
		if why := facts.componentSku(c.SkuID, uuid.Nil); why != "" {
			fields[fmt.Sprintf("candidates.%d.sku_id", i)] = why
		}
		if c.ColorID != nil && !facts.colors[*c.ColorID] {
			fields[fmt.Sprintf("candidates.%d.color_id", i)] = "unknown_color"
		}
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	stock, err := readComponentStock(ctx, q, skuIDs)
	if err != nil {
		return nil, err
	}
	today, err := s.todayIn(ctx, q)
	if err != nil {
		return nil, err
	}
	colors, err := readColorStock(ctx, q, today, skuIDs)
	if err != nil {
		return nil, err
	}
	queries := make([]PriceQuery, len(cands))
	for i, c := range cands {
		queries[i] = PriceQuery{SkuID: c.SkuID, ColorID: c.ColorID, UnitID: stock[c.SkuID].BaseUnitID, Qty: money.NewQtyFromInt(1)}
	}
	prices, err := s.prices.RetailPrices(ctx, q, tenantID, queries)
	if err != nil {
		return nil, err
	}
	opts := make([]suggestOption, len(cands))
	for i, c := range cands {
		if prices[i] == nil {
			fields[fmt.Sprintf("candidates.%d.sku_id", i)] = "no_price"
			continue
		}
		st := stock[c.SkuID]
		free := st.Sellable
		if c.ColorID != nil {
			free = minQty(free, colors[[2]uuid.UUID{c.SkuID, *c.ColorID}])
		}
		if free, err = decQty(free.Decimal().Floor()); err != nil {
			return nil, err
		}
		limit := free
		if c.MaxQty != nil {
			limit = minQty(limit, *c.MaxQty)
		}
		opts[i] = suggestOption{index: i, cand: c, limit: limit, line: SuggestLine{SkuID: c.SkuID, SkuCode: st.Code, Name: st.Name,
			ColorID: c.ColorID, Qty: money.ZeroQty, UnitPrice: *prices[i], Amount: money.ZeroMoney, UnitCost: st.UnitCost,
			Cost: money.ZeroMoney, UnitMargin: prices[i].Sub(st.UnitCost), Sellable: free}}
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	return opts, nil
}

// suggest fills the kit: the minimums first (refused when stock or the budget cannot hold them),
// then the best unit margin first, cheaper first on a tie.
func suggest(in SuggestInput, opts []suggestOption) (Suggestion, error) {
	left := in.Budget
	fields := map[string]string{}
	for i := range opts {
		o := &opts[i]
		if o.cand.MinQty.Cmp(o.line.Sellable) > 0 {
			fields[fmt.Sprintf("candidates.%d.min_qty", o.index)] = "exceeds_sellable"
			continue
		}
		o.line.Qty = o.cand.MinQty
		left = left.Sub(o.line.UnitPrice.MulQty(o.cand.MinQty))
	}
	if len(fields) > 0 {
		return Suggestion{}, errInvalid(fields)
	}
	if left.IsNegative() {
		return Suggestion{}, errInvalid(map[string]string{"budget": "below_minimum"})
	}
	order := make([]int, len(opts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := opts[order[a]].line, opts[order[b]].line
		if c := x.UnitMargin.Cmp(y.UnitMargin); c != 0 {
			return c > 0
		}
		return x.UnitPrice.Cmp(y.UnitPrice) < 0
	})
	for _, i := range order {
		o := &opts[i]
		room := o.limit.Sub(o.line.Qty)
		if !room.IsPositive() {
			continue
		}
		add := room
		if o.line.UnitPrice.IsPositive() {
			fit, err := decQty(left.Decimal().Div(o.line.UnitPrice.Decimal()).Floor())
			if err != nil {
				return Suggestion{}, err
			}
			add = minQty(add, fit)
		}
		if !add.IsPositive() {
			continue
		}
		o.line.Qty = o.line.Qty.Add(add)
		left = left.Sub(o.line.UnitPrice.MulQty(add))
	}
	out := Suggestion{Budget: in.Budget, Lines: []SuggestLine{}, ExpectedPrice: money.ZeroMoney, ComponentsCost: money.ZeroMoney,
		PackingCost: in.PackingCost, BudgetLeft: left}
	for _, o := range opts {
		if !o.line.Qty.IsPositive() {
			continue
		}
		l := o.line
		l.Amount = l.UnitPrice.MulQty(l.Qty)
		l.Cost = l.UnitCost.MulQty(l.Qty)
		out.ExpectedPrice = out.ExpectedPrice.Add(l.Amount)
		out.ComponentsCost = out.ComponentsCost.Add(l.Cost)
		out.Lines = append(out.Lines, l)
	}
	out.TotalCost = out.ComponentsCost.Add(in.PackingCost)
	out.Margin = out.ExpectedPrice.Sub(out.TotalCost)
	return out, nil
}
