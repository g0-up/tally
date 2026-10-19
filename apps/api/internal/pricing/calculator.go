package pricing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Line kinds the calculator produces (sales line_kind).
const (
	LineItem = "item"
	// LineGift is a zero-price line a gift promotion adds; it still issues stock and carries COGS.
	LineGift = "gift"
)

// DraftLine is a document line to calculate.
type DraftLine struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	// UnitID nil is the base unit.
	UnitID *uuid.UUID
	Qty    money.Qty
	// UnitPrice, when set, is a price typed on the line (a manual price source); nil resolves it.
	UnitPrice *money.Money
	// DiscountAmount is the manual line discount, at most the line's gross amount.
	DiscountAmount money.Money
}

// DocumentDraft is a document to calculate: a quote, invoice, POS cart or exchange draft, or a
// purchase document with SkipPromotions. BusinessDate nil is today in the tenant's time zone.
type DocumentDraft struct {
	PartyID         *uuid.UUID
	CustomerGroupID *uuid.UUID
	BusinessDate    *time.Time
	Lines           []DraftLine
	// OrderDiscount is a manual order discount, spread over the lines by their value after the
	// promotions.
	OrderDiscount  money.Money
	PromotionCodes []string
	// SkipPromotions prices the lines without any promotion (purchase documents).
	SkipPromotions bool
}

// CalculatedLine is a priced line with the sales pricing columns. Amounts are in whole đồng.
type CalculatedLine struct {
	// DraftIndex is the position of the draft line; -1 for a gift line.
	DraftIndex  int
	LineKind    string
	SkuID       uuid.UUID
	ColorID     *uuid.UUID
	UnitID      uuid.UUID
	UnitFactor  money.Factor
	Qty         money.Qty
	BaseQty     money.Qty
	UnitPrice   money.Money
	GrossAmount money.Money
	// DiscountAmount is the line discount: the manual discount plus line promotions.
	DiscountAmount     money.Money
	OrderDiscountAlloc money.Money
	TaxRateSnap        money.Percent
	TaxAmount          money.Money
	// LineAmount is the amount after every discount, tax included (prices including tax) or
	// added (prices excluding tax).
	LineAmount money.Money
	// NetUnitPrice is the exact price per base unit after every discount, before tax is added; the
	// minimum-price guard compares it with MinPriceSnap.
	NetUnitPrice  money.Money
	MinPriceSnap  *money.Money
	BelowMinPrice bool
	PriceSource   PriceSource
}

// CalculatedDocument is a calculated document. TotalAmount is the sum of the line amounts and
// equals GoodsAmount − DiscountAmount (+ TaxAmount when prices exclude tax).
type CalculatedDocument struct {
	BusinessDate     time.Time
	PricesIncludeTax bool
	RoundingMode     string
	Lines            []CalculatedLine
	GoodsAmount      money.Money
	DiscountAmount   money.Money
	TaxAmount        money.Money
	TotalAmount      money.Money
	// Promotions are the promotions that applied, with what each gave (exact, before rounding).
	Promotions        []AppliedPromotion
	SkippedPromotions []SkippedPromotion
}

// lineAmounts are the money amounts of one line: exact (4 decimals) until the rounding step,
// whole đồng after it.
type lineAmounts struct {
	Gross        money.Money
	LineDiscount money.Money
	OrderAlloc   money.Money
	Tax          money.Money
	Line         money.Money
	Rate         money.Percent
}

// net is the amount after every discount, before tax is added.
func (a lineAmounts) net() money.Money { return a.Gross.Sub(a.LineDiscount).Sub(a.OrderAlloc) }

// TaxBasis computes the tax of a line from its net amount under the tenant's prices_include_tax:
// prices including tax hold the tax (net × r / (100 + r)), prices excluding tax add it
// (net × r / 100).
type TaxBasis struct{ IncludeTax bool }

func (t TaxBasis) tax(net money.Money, rate money.Percent) (money.Money, error) {
	if !net.IsPositive() || !rate.Decimal().IsPositive() {
		return money.ZeroMoney, nil
	}
	div := decimal.NewFromInt(100)
	if t.IncludeTax {
		div = div.Add(rate.Decimal())
	}
	return money.RoundMoney(net.Decimal().Mul(rate.Decimal()).Div(div))
}

// line is the line amount of a net amount and its tax.
func (t TaxBasis) line(net, tax money.Money) money.Money {
	if t.IncludeTax {
		return net
	}
	return net.Add(tax)
}

// RoundingStrategy turns the exact amounts of the lines (tax already applied) into whole đồng,
// keeping every line consistent (line = gross − discounts, plus tax when prices exclude it) and
// the lines adding up to the document total.
type RoundingStrategy interface {
	Round(lines []lineAmounts, basis TaxBasis) error
}

// LineRounding rounds each line on its own: the gross and the amount after the line discount
// are rounded, the order discount is rounded keeping its total (remainder on the largest share),
// and the tax is computed on the rounded net and rounded. The document total is the sum of the
// rounded lines.
type LineRounding struct{}

// Round implements RoundingStrategy.
func (LineRounding) Round(lines []lineAmounts, basis TaxBasis) error {
	afterLine := make([]money.Money, len(lines))
	allocs := make([]money.Money, len(lines))
	for i := range lines {
		l := &lines[i]
		gross := l.Gross.RoundVND()
		afterLine[i] = l.Gross.Sub(l.LineDiscount).RoundVND()
		l.Gross, l.LineDiscount = gross, gross.Sub(afterLine[i])
		allocs[i] = l.OrderAlloc
	}
	allocs = fitWithin(money.RoundVNDKeepTotal(allocs), afterLine)
	for i := range lines {
		l := &lines[i]
		net := afterLine[i].Sub(allocs[i])
		tax, err := basis.tax(net, l.Rate)
		if err != nil {
			return err
		}
		l.OrderAlloc, l.Tax = allocs[i], tax.RoundVND()
		l.Line = basis.line(net, l.Tax)
	}
	return nil
}

// DocumentRounding keeps every line exact and rounds once per document: each column (line
// discount, order discount, tax, line amount) is rounded keeping its document total, the
// remainder going to the largest lines; the gross is what makes the rounded line consistent.
type DocumentRounding struct{}

// Round implements RoundingStrategy.
func (DocumentRounding) Round(lines []lineAmounts, basis TaxBasis) error {
	column := func(get func(lineAmounts) money.Money) []money.Money {
		out := make([]money.Money, len(lines))
		for i, l := range lines {
			out[i] = get(l)
		}
		return money.RoundVNDKeepTotal(out)
	}
	discounts := column(func(l lineAmounts) money.Money { return l.LineDiscount })
	allocs := column(func(l lineAmounts) money.Money { return l.OrderAlloc })
	taxes := column(func(l lineAmounts) money.Money { return l.Tax })
	amounts := column(func(l lineAmounts) money.Money { return l.Line })
	for i := range lines {
		l := &lines[i]
		l.LineDiscount, l.OrderAlloc, l.Tax, l.Line = discounts[i], allocs[i], taxes[i], amounts[i]
		net := l.Line
		if !basis.IncludeTax {
			net = net.Sub(l.Tax)
		}
		l.Gross = net.Add(l.LineDiscount).Add(l.OrderAlloc)
	}
	return nil
}

// roundingFor is the strategy of a tenant rounding mode.
func roundingFor(mode string) RoundingStrategy {
	if mode == RoundByDocument {
		return DocumentRounding{}
	}
	return LineRounding{}
}

// fitWithin lowers the parts above their cap to the cap and hands the excess to the parts with
// the most room left (the earlier on a tie). Excess no part has room for is dropped; that only
// happens when the caps add up to less than the parts, which whole-đồng lines never do.
func fitWithin(parts, caps []money.Money) []money.Money {
	excess := money.ZeroMoney
	for i := range parts {
		if parts[i].Cmp(caps[i]) > 0 {
			excess = excess.Add(parts[i].Sub(caps[i]))
			parts[i] = caps[i]
		}
	}
	if !excess.IsPositive() {
		return parts
	}
	order := make([]int, len(parts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return caps[order[a]].Sub(parts[order[a]]).Cmp(caps[order[b]].Sub(parts[order[b]])) > 0
	})
	for _, i := range order {
		room := caps[i].Sub(parts[i])
		if !room.IsPositive() || !excess.IsPositive() {
			break
		}
		take := minMoney(room, excess)
		parts[i], excess = parts[i].Add(take), excess.Sub(take)
	}
	return parts
}

// DocumentCalculator turns a draft into priced, discounted, taxed and rounded lines. Calculate
// is a template method: price the lines → line discounts (manual, then line promotions) → order
// discounts (promotions, then the manual one) allocated by eligible value → tax per
// prices_include_tax → rounding per rounding_mode → totals.
type DocumentCalculator struct {
	Resolver PriceResolver
	Engine   PromotionEngine
}

// calcInput is a draft with everything the calculation reads from the database.
type calcInput struct {
	Draft      DocumentDraft
	Date       time.Time
	Settings   tenantPricing
	GroupID    *uuid.UUID
	Lines      []pricedLine
	Prices     []ResolvedPrice
	Candidates []Promotion
	// Gifts are the gift SKUs of the candidates, priced one base unit each.
	Gifts map[uuid.UUID]giftPrice
}

type giftPrice struct {
	Sku   *skuInfo
	Price ResolvedPrice
}

// calcLine is a line under calculation.
type calcLine struct {
	CalculatedLine
	amt lineAmounts
}

// validateDraft checks the draft shape and returns its normalized, distinct promotion codes.
func validateDraft(d DocumentDraft) ([]string, error) {
	f := fieldErrors{}
	switch {
	case len(d.Lines) == 0:
		f.add("lines", "required")
	case len(d.Lines) > MaxDocumentLines:
		f.add("lines", "too_many")
	}
	for i, l := range d.Lines {
		if l.UnitPrice != nil && l.UnitPrice.IsNegative() {
			f.add(lineField(i, "unit_price"), "negative")
		}
		if l.DiscountAmount.IsNegative() {
			f.add(lineField(i, "discount_amount"), "negative")
		}
	}
	if d.OrderDiscount.IsNegative() {
		f.add("order_discount", "negative")
	}
	if len(d.PromotionCodes) > MaxPromotionCodes {
		f.add("promotion_codes", "too_many")
	}
	var codes []string
	seen := map[string]bool{}
	for i, raw := range d.PromotionCodes {
		code, ok := normalizeCode(raw)
		if !ok {
			f.add(fmt.Sprintf("promotion_codes[%d]", i), "invalid")
			continue
		}
		if !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	if d.SkipPromotions && len(codes) > 0 {
		f.add("promotion_codes", "not_allowed")
	}
	return codes, f.err()
}

// Calculate calculates d for business date date inside the caller's tenant transaction.
func (c DocumentCalculator) Calculate(ctx context.Context, q db.Querier, tp tenantPricing, date time.Time, d DocumentDraft) (CalculatedDocument, error) {
	codes, err := validateDraft(d)
	if err != nil {
		return CalculatedDocument{}, err
	}
	date = clock.TruncateDate(date)
	b, err := resolveBuyer(ctx, q, d.PartyID, d.CustomerGroupID)
	if err != nil {
		return CalculatedDocument{}, err
	}
	lines := make([]PriceLine, len(d.Lines))
	for i, l := range d.Lines {
		lines[i] = PriceLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty}
	}
	prepared, err := c.Resolver.prepare(ctx, q, lines)
	if err != nil {
		return CalculatedDocument{}, err
	}
	prices, err := c.Resolver.resolve(ctx, q, b, date, prepared)
	if err != nil {
		return CalculatedDocument{}, err
	}
	in := calcInput{Draft: d, Date: date, Settings: tp, GroupID: b.GroupID, Lines: prepared, Prices: prices}
	if !d.SkipPromotions {
		if in.Candidates, err = candidatePromotions(ctx, q, date, codes); err != nil {
			return CalculatedDocument{}, err
		}
		if err := missingCodes(codes, in.Candidates); err != nil {
			return CalculatedDocument{}, err
		}
		if in.Gifts, err = c.giftPrices(ctx, q, b, date, in.Candidates); err != nil {
			return CalculatedDocument{}, err
		}
	}
	return c.compute(in)
}

// missingCodes rejects the codes no promotion carries.
func missingCodes(codes []string, candidates []Promotion) error {
	known := map[string]bool{}
	for _, p := range candidates {
		if p.Code != nil {
			known[*p.Code] = true
		}
	}
	f := fieldErrors{}
	for i, code := range codes {
		if !known[code] {
			f.add(fmt.Sprintf("promotion_codes[%d]", i), CodeNotFound)
		}
	}
	return f.err()
}

// giftPrices prices one base unit of every gift SKU of the candidates for the buyer: the value a
// gift line carries.
func (c DocumentCalculator) giftPrices(ctx context.Context, q db.Querier, b buyer, date time.Time, candidates []Promotion) (map[uuid.UUID]giftPrice, error) {
	var skus []uuid.UUID
	for _, p := range candidates {
		if p.Kind != PromoGift {
			continue
		}
		for _, it := range p.itemsOf(RoleGift) {
			skus = append(skus, it.SkuID)
		}
	}
	skus = uniqueIDs(skus)
	out := make(map[uuid.UUID]giftPrice, len(skus))
	if len(skus) == 0 {
		return out, nil
	}
	lines := make([]PriceLine, len(skus))
	for i, id := range skus {
		lines[i] = PriceLine{SkuID: id, Qty: money.NewQtyFromInt(1)}
	}
	prepared, err := c.Resolver.prepare(ctx, q, lines)
	if err != nil {
		return nil, fmt.Errorf("pricing: gift skus: %w", err)
	}
	prices, err := c.Resolver.resolve(ctx, q, b, date, prepared)
	if err != nil {
		return nil, err
	}
	for i, l := range prepared {
		out[l.Sku.ID] = giftPrice{Sku: l.Sku, Price: prices[i]}
	}
	return out, nil
}

// compute runs the calculation steps on in; it does not touch the database.
func (c DocumentCalculator) compute(in calcInput) (CalculatedDocument, error) {
	basis := TaxBasis{IncludeTax: in.Settings.PricesIncludeTax}
	mode := in.Settings.RoundingMode
	if mode != RoundByDocument {
		mode = RoundByLine
	}
	doc := CalculatedDocument{BusinessDate: in.Date, PricesIncludeTax: basis.IncludeTax, RoundingMode: mode}
	lines, err := priceLines(in)
	if err != nil {
		return doc, err
	}
	if err := discountLines(in.Draft, lines); err != nil {
		return doc, err
	}
	if !in.Draft.SkipPromotions {
		lines = c.applyPromotions(in, lines, &doc)
	}
	if err := allocateOrderDiscount(in.Draft.OrderDiscount, lines); err != nil {
		return doc, err
	}
	amounts := make([]lineAmounts, len(lines))
	for i := range lines {
		if err := minPriceCheck(&lines[i]); err != nil {
			return doc, err
		}
		a := &lines[i].amt
		if a.Tax, err = basis.tax(a.net(), a.Rate); err != nil {
			return doc, errOutOfRange(lines[i].DraftIndex)
		}
		a.Line = basis.line(a.net(), a.Tax)
		amounts[i] = *a
	}
	if err := roundingFor(mode).Round(amounts, basis); err != nil {
		return doc, errOutOfRange(-1)
	}
	doc.Lines = make([]CalculatedLine, len(lines))
	for i, l := range lines {
		a := amounts[i]
		l.GrossAmount, l.DiscountAmount, l.OrderDiscountAlloc, l.TaxAmount, l.LineAmount = a.Gross, a.LineDiscount, a.OrderAlloc, a.Tax, a.Line
		doc.Lines[i] = l.CalculatedLine
		doc.GoodsAmount = doc.GoodsAmount.Add(a.Gross)
		doc.DiscountAmount = doc.DiscountAmount.Add(a.LineDiscount).Add(a.OrderAlloc)
		doc.TaxAmount = doc.TaxAmount.Add(a.Tax)
		doc.TotalAmount = doc.TotalAmount.Add(a.Line)
	}
	return doc, nil
}

func errOutOfRange(draftIndex int) error {
	if draftIndex < 0 {
		return errInvalid(map[string]string{"lines": "out_of_range"})
	}
	return errInvalid(map[string]string{lineField(draftIndex, "qty"): "out_of_range"})
}

// priceLines is the first step: each line's unit price (typed or resolved) and gross amount.
func priceLines(in calcInput) ([]calcLine, error) {
	out := make([]calcLine, len(in.Lines))
	for i, l := range in.Lines {
		draft := in.Draft.Lines[i]
		price := in.Prices[i]
		if draft.UnitPrice != nil {
			price.Amount = *draft.UnitPrice
			price.Source = PriceSource{Kind: SourceManual}
		}
		gross, err := money.RoundMoney(price.Amount.Decimal().Mul(l.Qty.Decimal()))
		if err != nil {
			return nil, errOutOfRange(i)
		}
		out[i] = calcLine{
			CalculatedLine: CalculatedLine{
				DraftIndex: i, LineKind: LineItem, SkuID: l.Sku.ID, ColorID: l.ColorID, UnitID: l.UnitID,
				UnitFactor: l.Factor, Qty: l.Qty, BaseQty: l.BaseQty, UnitPrice: price.Amount,
				TaxRateSnap: l.Sku.TaxRate, MinPriceSnap: l.Sku.MinPrice, PriceSource: price.Source,
			},
			amt: lineAmounts{Gross: gross, Rate: l.Sku.TaxRate},
		}
	}
	return out, nil
}

// discountLines is the second step: the manual line discounts, at most each line's gross.
func discountLines(d DocumentDraft, lines []calcLine) error {
	f := fieldErrors{}
	for i := range lines {
		disc := d.Lines[i].DiscountAmount
		if disc.Cmp(lines[i].amt.Gross) > 0 {
			f.add(lineField(i, "discount_amount"), "exceeds_amount")
			continue
		}
		lines[i].amt.LineDiscount = disc
	}
	return f.err()
}

// applyPromotions runs the promotion engine on the lines' value after manual discounts: line
// promotions add to the line discount, order promotions to the order allocation, and gifts become
// zero-price gift lines appended after the document lines.
func (c DocumentCalculator) applyPromotions(in calcInput, lines []calcLine, doc *CalculatedDocument) []calcLine {
	if len(in.Candidates) == 0 {
		return lines
	}
	promo := promoInput{Lines: make([]promoLine, len(lines)), GroupID: in.GroupID, Date: in.Date, GiftPrices: map[uuid.UUID]money.Money{}}
	for i, l := range lines {
		promo.Lines[i] = promoLine{SkuID: l.SkuID, BaseQty: l.BaseQty, Net: l.amt.Gross.Sub(l.amt.LineDiscount)}
	}
	for id, g := range in.Gifts {
		promo.GiftPrices[id] = g.Price.Amount
	}
	outcome := c.Engine.Evaluate(in.Candidates, promo)
	doc.SkippedPromotions = outcome.Skipped
	for _, eff := range outcome.Applied {
		p := eff.Promotion
		doc.Promotions = append(doc.Promotions, AppliedPromotion{ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Amount: eff.Benefit})
		// Effects cover the document lines only, not the gift lines earlier effects appended.
		for i := range eff.Line {
			share := eff.Line[i].Add(eff.OrderAlloc[i])
			if !share.IsPositive() {
				continue
			}
			lines[i].amt.LineDiscount = lines[i].amt.LineDiscount.Add(eff.Line[i])
			lines[i].amt.OrderAlloc = lines[i].amt.OrderAlloc.Add(eff.OrderAlloc[i])
			lines[i].PriceSource.Promotions = append(lines[i].PriceSource.Promotions,
				AppliedPromotion{ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Amount: share})
		}
		for _, g := range eff.Gifts {
			gift := in.Gifts[g.SkuID]
			source := gift.Price.Source
			source.Promotions = []AppliedPromotion{{ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Amount: g.Value}}
			lines = append(lines, calcLine{
				CalculatedLine: CalculatedLine{
					DraftIndex: -1, LineKind: LineGift, SkuID: g.SkuID, UnitID: gift.Sku.BaseUnitID,
					UnitFactor: money.FactorOne, Qty: g.Qty, BaseQty: g.Qty, TaxRateSnap: gift.Sku.TaxRate,
					PriceSource: source,
				},
				amt: lineAmounts{Rate: gift.Sku.TaxRate},
			})
		}
	}
	return lines
}

// allocateOrderDiscount is the third step's manual part: the order discount spread over the item
// lines by what is left of each after the promotions.
func allocateOrderDiscount(amount money.Money, lines []calcLine) error {
	if !amount.IsPositive() {
		return nil
	}
	left := make([]money.Money, len(lines))
	total := money.ZeroMoney
	for i, l := range lines {
		if l.LineKind == LineItem {
			left[i] = l.amt.net()
			total = total.Add(left[i])
		}
	}
	if amount.Cmp(total) > 0 {
		return errInvalid(map[string]string{"order_discount": "exceeds_amount"})
	}
	parts, err := money.Allocate(amount, left)
	if err != nil {
		return errInvalid(map[string]string{"order_discount": "exceeds_amount"})
	}
	for i := range lines {
		lines[i].amt.OrderAlloc = lines[i].amt.OrderAlloc.Add(parts[i])
	}
	return nil
}

// minPriceCheck sets the exact net price per base unit of an item line and whether it is below
// the SKU's minimum price. Gift lines are never checked.
func minPriceCheck(l *calcLine) error {
	if l.LineKind != LineItem {
		return nil
	}
	net := l.amt.net()
	unit, err := money.RoundMoney(net.Decimal().Div(l.BaseQty.Decimal()))
	if err != nil {
		return errOutOfRange(l.DraftIndex)
	}
	l.NetUnitPrice = unit
	l.BelowMinPrice = l.MinPriceSnap != nil && net.Decimal().Cmp(l.MinPriceSnap.Decimal().Mul(l.BaseQty.Decimal())) < 0
	return nil
}

// Calculate previews a document for the sales screens without saving anything.
func (s *Service) Calculate(ctx context.Context, scope Scope, d DocumentDraft) (CalculatedDocument, error) {
	var out CalculatedDocument
	err := s.readPriced(ctx, scope, func(q db.Querier, tp tenantPricing) error {
		var err error
		out, err = s.calculator.Calculate(ctx, q, tp, s.businessDate(d.BusinessDate, tp), d)
		return err
	})
	return out, err
}

// CalculateIn calculates a document inside the caller's tenant transaction: the posting commands
// recalculate server-side and ignore client totals.
func (s *Service) CalculateIn(ctx context.Context, q db.Querier, tenantID uuid.UUID, d DocumentDraft) (CalculatedDocument, error) {
	tp, err := loadTenantPricing(ctx, q, tenantID)
	if err != nil {
		return CalculatedDocument{}, err
	}
	return s.calculator.Calculate(ctx, q, tp, s.businessDate(d.BusinessDate, tp), d)
}
