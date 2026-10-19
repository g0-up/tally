package pricing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// PriceContext is who a document is priced for and on which business date (a civil date as
// midnight UTC). A party must be a customer; an explicit group overrides the party's group.
type PriceContext struct {
	PartyID         *uuid.UUID
	CustomerGroupID *uuid.UUID
	BusinessDate    time.Time
}

// PriceLine is one line to price: a SKU, optionally in a color, in a unit (nil is the base unit),
// for a quantity in that unit.
type PriceLine struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  *uuid.UUID
	Qty     money.Qty
}

// PriceQuery prices a single line; BusinessDate nil is today in the tenant's time zone.
type PriceQuery struct {
	PartyID         *uuid.UUID
	CustomerGroupID *uuid.UUID
	SkuID           uuid.UUID
	ColorID         *uuid.UUID
	UnitID          *uuid.UUID
	Qty             money.Qty
	BusinessDate    *time.Time
}

// ResolvedPrice is the unit price of a line in the line's unit and where it came from.
type ResolvedPrice struct {
	Amount money.Money
	Source PriceSource
	// UnitID is the line's unit (the base unit when the line gave none), Factor its factor and
	// BaseQty the line quantity in base units.
	UnitID  uuid.UUID
	Factor  money.Factor
	BaseQty money.Qty
}

// buyer is the customer a document is priced for: the party, its group and the base list kind
// the group falls back to.
type buyer struct {
	PartyID  *uuid.UUID
	GroupID  *uuid.UUID
	BaseKind string
}

// resolveBuyer reads the party's customer group and the group's base price kind. A party without
// a customer profile is rejected; no party and no group is a walk-in customer on the retail list.
func resolveBuyer(ctx context.Context, q db.Querier, partyID, groupID *uuid.UUID) (buyer, error) {
	b := buyer{PartyID: partyID, GroupID: groupID, BaseKind: KindRetail}
	if partyID != nil {
		var profileGroup uuid.UUID
		err := q.QueryRowxContext(ctx, `SELECT customer_group_id FROM customer_profiles WHERE party_id = $1`, *partyID).Scan(&profileGroup)
		if db.IsNoRows(err) {
			return buyer{}, errInvalid(map[string]string{"party_id": "not_a_customer"})
		}
		if err != nil {
			return buyer{}, fmt.Errorf("pricing: read customer profile: %w", err)
		}
		if b.GroupID == nil {
			b.GroupID = &profileGroup
		}
	}
	if b.GroupID != nil {
		err := q.QueryRowxContext(ctx, `SELECT base_price_kind FROM customer_groups WHERE id = $1`, *b.GroupID).Scan(&b.BaseKind)
		if db.IsNoRows(err) {
			return buyer{}, errInvalid(map[string]string{"customer_group_id": "not_found"})
		}
		if err != nil {
			return buyer{}, fmt.Errorf("pricing: read customer group: %w", err)
		}
	}
	return b, nil
}

// ListSource is one level of the resolver chain: it picks, among the candidate lists, the lists
// of its level in the order they are tried.
type ListSource interface {
	// Kind is the price source kind a price found at this level reports.
	Kind() string
	// Lists returns the lists of this level for b, keeping the candidates' order (priority,
	// newest first, id).
	Lists(candidates []PriceList, b buyer) []PriceList
}

type listFilter struct {
	kind string
	keep func(l PriceList, b buyer) bool
}

func (f listFilter) Kind() string { return f.kind }

func (f listFilter) Lists(candidates []PriceList, b buyer) []PriceList {
	var out []PriceList
	for _, l := range candidates {
		if f.keep(l, b) {
			out = append(out, l)
		}
	}
	return out
}

// CustomerListSource is the party's own lists.
func CustomerListSource() ListSource {
	return listFilter{kind: SourceCustomerList, keep: func(l PriceList, b buyer) bool {
		return l.Kind == KindCustomer && b.PartyID != nil && sameID(l.PartyID, b.PartyID)
	}}
}

// GroupListSource is the lists of the customer's group.
func GroupListSource() ListSource {
	return listFilter{kind: SourceGroupList, keep: func(l PriceList, b buyer) bool {
		return l.Kind == KindGroup && b.GroupID != nil && sameID(l.CustomerGroupID, b.GroupID)
	}}
}

// SeasonListSource is the season lists valid on the date.
func SeasonListSource() ListSource {
	return listFilter{kind: SourceSeasonList, keep: func(l PriceList, _ buyer) bool { return l.Kind == KindSeason }}
}

// BaseListSource is the base list (retail or wholesale) of the customer's group.
func BaseListSource() ListSource {
	return listFilter{kind: SourceBaseList, keep: func(l PriceList, b buyer) bool { return l.Kind == b.BaseKind }}
}

// pricedLine is a validated line: its SKU, unit, factor and base quantity.
type pricedLine struct {
	Index   int
	Sku     *skuInfo
	ColorID *uuid.UUID
	UnitID  uuid.UUID
	IsBase  bool
	Factor  money.Factor
	Qty     money.Qty
	BaseQty money.Qty
}

// ListRowMatcher picks the row of one list that prices a line: a row of the line's color before
// a general row; within each, a row in the line's unit before the base unit price × factor; and
// among those the highest tier whose min_qty (in base units) the line's base quantity reaches.
type ListRowMatcher struct{}

// Match returns the matching row and the unit price it gives the line.
func (ListRowMatcher) Match(rows []priceRow, l pricedLine) (priceRow, money.Money, bool) {
	colors := []*uuid.UUID{nil}
	if l.ColorID != nil {
		colors = []*uuid.UUID{l.ColorID, nil}
	}
	for _, color := range colors {
		if row, ok := bestTier(rows, l, color, true); ok {
			return row, row.Price, true
		}
		if l.IsBase {
			continue
		}
		if row, ok := bestTier(rows, l, color, false); ok {
			return row, row.Price.MulFactor(l.Factor), true
		}
	}
	return priceRow{}, money.Money{}, false
}

// bestTier returns the row of color with the highest min_qty ≤ the line's base quantity, among the
// rows in the line's unit (inUnit) or in the base unit.
func bestTier(rows []priceRow, l pricedLine, color *uuid.UUID, inUnit bool) (priceRow, bool) {
	var best priceRow
	found := false
	for _, r := range rows {
		if !sameID(r.ColorID, color) {
			continue
		}
		rowIsBase := r.UnitID == nil || *r.UnitID == l.Sku.BaseUnitID
		if inUnit {
			if l.IsBase != rowIsBase || (!rowIsBase && *r.UnitID != l.UnitID) {
				continue
			}
		} else if !rowIsBase {
			continue
		}
		if r.MinQty.Cmp(l.BaseQty) > 0 {
			continue
		}
		if !found || r.MinQty.Cmp(best.MinQty) > 0 {
			best, found = r, true
		}
	}
	return best, found
}

// PriceResolver prices lines through a chain of list levels — customer, group, season, base —
// stopping at the first list with a matching row. A line no list prices gets a manual price of 0.
type PriceResolver struct {
	chain   []ListSource
	matcher ListRowMatcher
}

// NewPriceResolver returns the resolver with the standard chain.
func NewPriceResolver() PriceResolver {
	return PriceResolver{chain: []ListSource{CustomerListSource(), GroupListSource(), SeasonListSource(), BaseListSource()}}
}

// lineField names a field of line i in validation errors.
func lineField(i int, name string) string { return fmt.Sprintf("lines[%d].%s", i, name) }

// prepare validates lines and loads their SKUs: the SKU exists, the unit is one the SKU sells in,
// the color exists and the quantity is positive and converts to base units exactly.
func (r PriceResolver) prepare(ctx context.Context, q db.Querier, lines []PriceLine) ([]pricedLine, error) {
	skuIDs := make([]uuid.UUID, 0, len(lines))
	var colorIDs []uuid.UUID
	for _, l := range lines {
		skuIDs = append(skuIDs, l.SkuID)
		if l.ColorID != nil {
			colorIDs = append(colorIDs, *l.ColorID)
		}
	}
	skus, err := loadSkuInfo(ctx, q, uniqueIDs(skuIDs))
	if err != nil {
		return nil, err
	}
	colors, err := existingIDs(ctx, q, "colors", uniqueIDs(colorIDs))
	if err != nil {
		return nil, err
	}
	f := fieldErrors{}
	out := make([]pricedLine, len(lines))
	for i, l := range lines {
		sku, ok := skus[l.SkuID]
		if !ok {
			f.add(lineField(i, "sku_id"), "not_found")
			continue
		}
		if l.ColorID != nil && !colors[*l.ColorID] {
			f.add(lineField(i, "color_id"), "not_found")
		}
		unitID, factor, ok := sku.unitOf(l.UnitID)
		if !ok {
			f.add(lineField(i, "unit_id"), "unknown_unit")
			continue
		}
		if !l.Qty.IsPositive() {
			f.add(lineField(i, "qty"), "must_be_positive")
			continue
		}
		baseQty, err := money.BaseQty(l.Qty, factor)
		if err != nil {
			f.add(lineField(i, "qty"), "out_of_range")
			continue
		}
		out[i] = pricedLine{
			Index: i, Sku: sku, ColorID: l.ColorID, UnitID: unitID, IsBase: unitID == sku.BaseUnitID,
			Factor: factor, Qty: l.Qty, BaseQty: baseQty,
		}
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	return out, nil
}

// resolve prices prepared lines for b on date with two queries: the candidate lists and their
// rows for every SKU of the lines.
func (r PriceResolver) resolve(ctx context.Context, q db.Querier, b buyer, date time.Time, lines []pricedLine) ([]ResolvedPrice, error) {
	lists, err := candidateLists(ctx, q, date, b.BaseKind, b.GroupID, b.PartyID)
	if err != nil {
		return nil, err
	}
	listIDs := make([]uuid.UUID, len(lists))
	for i, l := range lists {
		listIDs[i] = l.ID
	}
	skuIDs := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		skuIDs[i] = l.Sku.ID
	}
	rows, err := candidateRows(ctx, q, listIDs, uniqueIDs(skuIDs))
	if err != nil {
		return nil, err
	}
	type listSku struct{ list, sku uuid.UUID }
	byListSku := make(map[listSku][]priceRow, len(rows))
	for _, row := range rows {
		k := listSku{row.PriceListID, row.SkuID}
		byListSku[k] = append(byListSku[k], row)
	}
	levels := make([][]PriceList, len(r.chain))
	for i, src := range r.chain {
		levels[i] = src.Lists(lists, b)
	}
	out := make([]ResolvedPrice, len(lines))
	for i, l := range lines {
		out[i] = ResolvedPrice{Source: PriceSource{Kind: SourceManual}, UnitID: l.UnitID, Factor: l.Factor, BaseQty: l.BaseQty}
	levels:
		for lv, src := range r.chain {
			for _, list := range levels[lv] {
				row, price, ok := r.matcher.Match(byListSku[listSku{list.ID, l.Sku.ID}], l)
				if !ok {
					continue
				}
				minQty := row.MinQty
				out[i].Amount = price
				out[i].Source = PriceSource{
					Kind: src.Kind(), PriceListID: ptr(list.ID), PriceListName: ptr(list.Name),
					ColorID: row.ColorID, UnitID: row.UnitID, MinQty: &minQty,
				}
				break levels
			}
		}
	}
	return out, nil
}

// ResolveMany prices lines for a customer on pc.BusinessDate inside the caller's tenant
// transaction, loading the candidate lists and rows of every line at once.
func (r PriceResolver) ResolveMany(ctx context.Context, q db.Querier, pc PriceContext, lines []PriceLine) ([]ResolvedPrice, error) {
	if len(lines) == 0 {
		return []ResolvedPrice{}, nil
	}
	b, err := resolveBuyer(ctx, q, pc.PartyID, pc.CustomerGroupID)
	if err != nil {
		return nil, err
	}
	prepared, err := r.prepare(ctx, q, lines)
	if err != nil {
		return nil, err
	}
	return r.resolve(ctx, q, b, clock.TruncateDate(pc.BusinessDate), prepared)
}

// ResolveMany prices lines inside the caller's tenant transaction; a zero BusinessDate is today in
// the tenant's time zone. Sales documents call it through the composition root.
func (s *Service) ResolveMany(ctx context.Context, q db.Querier, tenantID uuid.UUID, pc PriceContext, lines []PriceLine) ([]ResolvedPrice, error) {
	if pc.BusinessDate.IsZero() {
		tp, err := loadTenantPricing(ctx, q, tenantID)
		if err != nil {
			return nil, err
		}
		pc.BusinessDate = s.businessDate(nil, tp)
	}
	return s.resolver.ResolveMany(ctx, q, pc, lines)
}

// Resolve prices one line inside the caller's tenant transaction.
func (s *Service) Resolve(ctx context.Context, q db.Querier, tenantID uuid.UUID, pq PriceQuery) (ResolvedPrice, error) {
	pc := PriceContext{PartyID: pq.PartyID, CustomerGroupID: pq.CustomerGroupID}
	if pq.BusinessDate != nil {
		pc.BusinessDate = *pq.BusinessDate
	}
	out, err := s.ResolveMany(ctx, q, tenantID, pc, []PriceLine{{SkuID: pq.SkuID, ColorID: pq.ColorID, UnitID: pq.UnitID, Qty: pq.Qty}})
	if err != nil {
		return ResolvedPrice{}, err
	}
	return out[0], nil
}

// QuoteInput is a batch of lines to price for the sales screens, without saving anything.
type QuoteInput struct {
	PartyID         *uuid.UUID
	CustomerGroupID *uuid.UUID
	BusinessDate    *time.Time
	Lines           []PriceLine
}

// Quote prices up to MaxDocumentLines lines for the sales screens.
func (s *Service) Quote(ctx context.Context, scope Scope, in QuoteInput) ([]ResolvedPrice, error) {
	switch {
	case len(in.Lines) == 0:
		return nil, errInvalid(map[string]string{"lines": "required"})
	case len(in.Lines) > MaxDocumentLines:
		return nil, errInvalid(map[string]string{"lines": "too_many"})
	}
	var out []ResolvedPrice
	err := s.readPriced(ctx, scope, func(q db.Querier, tp tenantPricing) error {
		pc := PriceContext{PartyID: in.PartyID, CustomerGroupID: in.CustomerGroupID, BusinessDate: s.businessDate(in.BusinessDate, tp)}
		var err error
		out, err = s.resolver.ResolveMany(ctx, q, pc, in.Lines)
		return err
	})
	return out, err
}
