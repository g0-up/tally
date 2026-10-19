package pricing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Reasons a promotion did not apply to a document, or a code is not usable.
const (
	// SkipNotStackable: a non-stackable promotion that lost to the promotion set that applied;
	// non-stackable promotions never combine with any other promotion.
	SkipNotStackable = "not_stackable"
	// SkipLowerBenefit: a stackable promotion that lost to a single non-stackable promotion
	// giving the customer more.
	SkipLowerBenefit = "lower_benefit"
	// SkipBelowMinOrder: the document is below the promotion's minimum order amount.
	SkipBelowMinOrder = "below_min_order"
	// SkipNoEligibleLines: the document lacks the SKUs (or quantities) the promotion needs.
	SkipNoEligibleLines = "no_eligible_lines"
	// SkipNoBenefit: the promotion would give nothing (a combo price above the lines' value).
	SkipNoBenefit = "no_benefit"
	// SkipMaxDiscount: the promotion's cap is below the value of a single gift set.
	SkipMaxDiscount = "max_discount_too_low"
	// SkipCustomerGroup: the promotion is for another customer group.
	SkipCustomerGroup = "customer_group"
	// SkipUsageLimitReached: the promotion was used as often as allowed.
	SkipUsageLimitReached = "usage_limit_reached"
	// CodeNotFound, CodeInactive, CodeNotStarted and CodeExpired are code check reasons.
	CodeNotFound   = "not_found"
	CodeInactive   = "inactive"
	CodeNotStarted = "not_started"
	CodeExpired    = "expired"
)

// SkippedPromotion is a candidate promotion that did not apply, with why.
type SkippedPromotion struct {
	ID     uuid.UUID
	Code   *string
	Name   string
	Kind   string
	Reason string
}

// GiftLine is a gift a promotion adds to a document, in the gift SKU's base unit.
type GiftLine struct {
	PromotionID uuid.UUID
	SkuID       uuid.UUID
	Qty         money.Qty
	// Value is the gift's list value (its resolved price × quantity), what the customer is given.
	Value money.Money
}

// promoLine is a document line as the promotion engine sees it: its SKU, its quantity in base
// units and its amount after the line's manual discount.
type promoLine struct {
	SkuID   uuid.UUID
	BaseQty money.Qty
	Net     money.Money
}

// promoEffect is what one promotion does to a document. Line and OrderAlloc are parallel to the
// document lines: the line-level discount and the share of the order-level discount of each line.
type promoEffect struct {
	Promotion  Promotion
	Line       []money.Money
	OrderAlloc []money.Money
	Gifts      []GiftLine
	// Benefit is the customer's gain: every discount plus the value of the gifts.
	Benefit money.Money
}

func newEffect(p Promotion, n int) promoEffect {
	return promoEffect{Promotion: p, Line: make([]money.Money, n), OrderAlloc: make([]money.Money, n)}
}

// PromotionRule is the strategy of one promotion kind: the effect of p on lines, given what is
// still left to discount on each line (left is parallel to lines). reason is set when the
// promotion gives nothing.
type PromotionRule interface {
	Apply(p Promotion, lines []promoLine, left []money.Money, giftPrices map[uuid.UUID]money.Money) (effect promoEffect, reason string)
}

// eligibleLines returns the positions of the lines a promotion covers: the lines of its eligible
// SKUs, or every line when it lists none.
func eligibleLines(p Promotion, lines []promoLine) []int {
	scope := p.itemsOf(RoleEligible)
	out := make([]int, 0, len(lines))
	for i, l := range lines {
		if len(scope) == 0 || containsSku(scope, l.SkuID) {
			out = append(out, i)
		}
	}
	return out
}

func containsSku(items []PromotionItem, sku uuid.UUID) bool {
	for _, it := range items {
		if it.SkuID == sku {
			return true
		}
	}
	return false
}

// percentOf reads a percent promotion value (validated 0 < v ≤ 100 on save).
func percentOf(v money.Money) (money.Percent, bool) {
	p, err := money.NewPercent(v.String())
	return p, err == nil && v.IsPositive()
}

// capParts scales parts down to total at most limit, keeping their proportions exactly.
func capParts(parts []money.Money, limit *money.Money) []money.Money {
	total := money.SumMoney(parts...)
	if limit == nil || total.Cmp(*limit) <= 0 {
		return parts
	}
	capped, err := money.Allocate(*limit, parts)
	if err != nil { // total > limit ≥ 0 means some part is positive
		return parts
	}
	return capped
}

// spread allocates amount over the lines at positions by their left amounts.
func spread(amount money.Money, positions []int, left []money.Money, n int) ([]money.Money, error) {
	weights := make([]money.Money, len(positions))
	for k, i := range positions {
		weights[k] = left[i]
	}
	parts, err := money.Allocate(amount, weights)
	if err != nil {
		return nil, err
	}
	out := make([]money.Money, n)
	for k, i := range positions {
		out[i] = parts[k]
	}
	return out, nil
}

// lineRule discounts each eligible line by perLine(line, left).
type lineRule struct {
	perLine func(p Promotion, l promoLine, left money.Money) money.Money
}

func (r lineRule) Apply(p Promotion, lines []promoLine, left []money.Money, _ map[uuid.UUID]money.Money) (promoEffect, string) {
	e := newEffect(p, len(lines))
	positions := eligibleLines(p, lines)
	parts := make([]money.Money, len(positions))
	for k, i := range positions {
		if left[i].IsPositive() {
			parts[k] = minMoney(r.perLine(p, lines[i], left[i]), left[i])
		}
	}
	parts = capParts(parts, p.MaxDiscount)
	for k, i := range positions {
		e.Line[i] = parts[k]
	}
	e.Benefit = money.SumMoney(parts...)
	if !e.Benefit.IsPositive() {
		return e, SkipNoEligibleLines
	}
	return e, ""
}

// LinePercentRule takes a percentage off every eligible line.
func LinePercentRule() PromotionRule {
	return lineRule{perLine: func(p Promotion, _ promoLine, left money.Money) money.Money {
		pct, ok := percentOf(p.Value)
		if !ok {
			return money.ZeroMoney
		}
		return left.MulPercent(pct)
	}}
}

// LineAmountRule takes a fixed amount per base unit off every eligible line, at most the line.
func LineAmountRule() PromotionRule {
	return lineRule{perLine: func(p Promotion, l promoLine, _ money.Money) money.Money {
		return p.Value.MulQty(l.BaseQty)
	}}
}

// orderRule discounts the eligible lines together and spreads the discount over them by value.
type orderRule struct {
	amount func(p Promotion, base money.Money) money.Money
}

func (r orderRule) Apply(p Promotion, lines []promoLine, left []money.Money, _ map[uuid.UUID]money.Money) (promoEffect, string) {
	e := newEffect(p, len(lines))
	positions := eligibleLines(p, lines)
	base := money.ZeroMoney
	for _, i := range positions {
		base = base.Add(left[i])
	}
	if !base.IsPositive() {
		return e, SkipNoEligibleLines
	}
	amount := minMoney(r.amount(p, base), base)
	if p.MaxDiscount != nil {
		amount = minMoney(amount, *p.MaxDiscount)
	}
	if !amount.IsPositive() {
		return e, SkipNoBenefit
	}
	alloc, err := spread(amount, positions, left, len(lines))
	if err != nil {
		return e, SkipNoBenefit
	}
	e.OrderAlloc, e.Benefit = alloc, amount
	return e, ""
}

// OrderPercentRule takes a percentage off the eligible lines' total.
func OrderPercentRule() PromotionRule {
	return orderRule{amount: func(p Promotion, base money.Money) money.Money {
		pct, ok := percentOf(p.Value)
		if !ok {
			return money.ZeroMoney
		}
		return base.MulPercent(pct)
	}}
}

// OrderAmountRule takes a fixed amount off the eligible lines' total, at most that total.
func OrderAmountRule() PromotionRule {
	return orderRule{amount: func(p Promotion, _ money.Money) money.Money { return p.Value }}
}

// skuQty sums the base quantity of the lines per SKU.
func skuQty(lines []promoLine) map[uuid.UUID]decimal.Decimal {
	out := map[uuid.UUID]decimal.Decimal{}
	for _, l := range lines {
		out[l.SkuID] = out[l.SkuID].Add(l.BaseQty.Decimal())
	}
	return out
}

// sets is how many whole times the lines hold every item (each in its quantity); 0 when one is
// missing.
func sets(items []PromotionItem, have map[uuid.UUID]decimal.Decimal) int64 {
	k := int64(-1)
	for _, it := range items {
		n := have[it.SkuID].Div(it.Qty.Decimal()).Floor().IntPart()
		if k < 0 || n < k {
			k = n
		}
	}
	if k < 0 {
		return 0
	}
	return k
}

// GiftRule gives the gift items once per set of eligible items bought (once per document when the
// promotion lists no eligible items), as zero-price gift lines. The cap limits the gift sets.
func GiftRule() PromotionRule { return giftRule{} }

type giftRule struct{}

func (giftRule) Apply(p Promotion, lines []promoLine, _ []money.Money, giftPrices map[uuid.UUID]money.Money) (promoEffect, string) {
	e := newEffect(p, len(lines))
	gifts := p.itemsOf(RoleGift)
	if len(gifts) == 0 {
		return e, SkipNoBenefit
	}
	k := int64(1)
	if scope := p.itemsOf(RoleEligible); len(scope) > 0 {
		k = sets(scope, skuQty(lines))
	}
	if k == 0 {
		return e, SkipNoEligibleLines
	}
	setValue := money.ZeroMoney
	for _, g := range gifts {
		setValue = setValue.Add(giftPrices[g.SkuID].MulQty(g.Qty))
	}
	if p.MaxDiscount != nil && setValue.IsPositive() {
		limit := p.MaxDiscount.Decimal().Div(setValue.Decimal()).Floor().IntPart()
		if limit < k {
			k = limit
		}
		if k == 0 {
			return e, SkipMaxDiscount
		}
	}
	times := money.NewQtyFromInt(k)
	for _, g := range gifts {
		qty, err := money.NewQty(g.Qty.Decimal().Mul(times.Decimal()).String())
		if err != nil {
			return e, SkipNoBenefit
		}
		value := giftPrices[g.SkuID].MulQty(qty)
		e.Gifts = append(e.Gifts, GiftLine{PromotionID: p.ID, SkuID: g.SkuID, Qty: qty, Value: value})
		e.Benefit = e.Benefit.Add(value)
	}
	return e, ""
}

// ComboPriceRule sells each full set of the combo components at the promotion price: the
// discount is the components' value in the sets minus the combo price per set, spread over the
// component lines.
func ComboPriceRule() PromotionRule { return comboRule{} }

type comboRule struct{}

func (comboRule) Apply(p Promotion, lines []promoLine, left []money.Money, _ map[uuid.UUID]money.Money) (promoEffect, string) {
	e := newEffect(p, len(lines))
	components := p.itemsOf(RoleComboComponent)
	have := skuQty(lines)
	k := sets(components, have)
	if k == 0 {
		return e, SkipNoEligibleLines
	}
	inSets := map[uuid.UUID]decimal.Decimal{}
	for _, c := range components {
		inSets[c.SkuID] = c.Qty.Decimal().Mul(decimal.NewFromInt(k))
	}
	// Each component line contributes the part of its value that falls inside the sets.
	weights := make([]money.Money, len(lines))
	for i, l := range lines {
		need, ok := inSets[l.SkuID]
		if !ok || !left[i].IsPositive() {
			continue
		}
		w, err := money.RoundMoney(left[i].Decimal().Mul(need).Div(have[l.SkuID]))
		if err != nil {
			return e, SkipNoBenefit
		}
		weights[i] = w
	}
	value := money.SumMoney(weights...)
	discount := value.Sub(p.Value.MulQty(money.NewQtyFromInt(k)))
	if p.MaxDiscount != nil {
		discount = minMoney(discount, *p.MaxDiscount)
	}
	if !discount.IsPositive() {
		return e, SkipNoBenefit
	}
	parts, err := money.Allocate(discount, weights)
	if err != nil {
		return e, SkipNoBenefit
	}
	e.Line, e.Benefit = parts, discount
	return e, ""
}

// kindOrder is the order stackable promotions apply in: combos and line discounts first, gifts,
// then order discounts on what is left.
var kindOrder = map[string]int{
	PromoComboPrice: 0, PromoLinePercent: 1, PromoLineAmount: 2, PromoGift: 3, PromoOrderPercent: 4, PromoOrderAmount: 5,
}

// PromotionEngine applies promotions to a document. Every candidate is first gated (status,
// validity, customer group, usage limit, minimum order on the lines' value after manual line
// discounts). Then the StackingPolicy: the stackable promotions apply together, each on what the
// previous ones left (combos and line discounts, gifts, then order discounts), and the best single
// non-stackable promotion applies alone instead when it gives the customer more. Each
// promotion's max_discount caps its own discount.
type PromotionEngine struct {
	rules map[string]PromotionRule
}

// NewPromotionEngine returns the engine with a rule per promotion kind.
func NewPromotionEngine() PromotionEngine {
	return PromotionEngine{rules: map[string]PromotionRule{
		PromoLinePercent: LinePercentRule(), PromoLineAmount: LineAmountRule(),
		PromoOrderPercent: OrderPercentRule(), PromoOrderAmount: OrderAmountRule(),
		PromoGift: GiftRule(), PromoComboPrice: ComboPriceRule(),
	}}
}

// promoInput is a document as the engine sees it.
type promoInput struct {
	Lines   []promoLine
	GroupID *uuid.UUID
	Date    time.Time
	// GiftPrices is the resolved base-unit price of every gift SKU of the candidates.
	GiftPrices map[uuid.UUID]money.Money
}

// promoOutcome is the promotions that applied, in the order they applied, and those that did not.
type promoOutcome struct {
	Applied []promoEffect
	Skipped []SkippedPromotion
}

func skipped(p Promotion, reason string) SkippedPromotion {
	return SkippedPromotion{ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Reason: reason}
}

// gate is why p cannot apply to the document at all, or "".
func gate(p Promotion, in promoInput, goods money.Money) string {
	switch {
	case p.Status != PromoActive:
		return CodeInactive
	case in.Date.Before(p.ValidFrom):
		return CodeNotStarted
	case in.Date.After(p.ValidTo):
		return CodeExpired
	case p.CustomerGroupID != nil && !sameID(p.CustomerGroupID, in.GroupID):
		return SkipCustomerGroup
	case p.UsageLimit != nil && p.UsedCount >= *p.UsageLimit:
		return SkipUsageLimitReached
	case goods.Cmp(p.MinOrderAmount) < 0:
		return SkipBelowMinOrder
	}
	return ""
}

// apply runs p's rule on left and, when it gives something, takes its discounts off left.
func (e PromotionEngine) apply(p Promotion, in promoInput, left []money.Money) (promoEffect, string) {
	rule, ok := e.rules[p.Kind]
	if !ok {
		return promoEffect{}, SkipNoBenefit
	}
	eff, reason := rule.Apply(p, in.Lines, left, in.GiftPrices)
	if reason != "" {
		return eff, reason
	}
	for i := range left {
		left[i] = left[i].Sub(eff.Line[i]).Sub(eff.OrderAlloc[i])
	}
	return eff, ""
}

// Evaluate applies the candidates (in creation order) to the document.
func (e PromotionEngine) Evaluate(candidates []Promotion, in promoInput) promoOutcome {
	var out promoOutcome
	base := make([]money.Money, len(in.Lines))
	goods := money.ZeroMoney
	for i, l := range in.Lines {
		base[i] = l.Net
		goods = goods.Add(l.Net)
	}
	var stackable, single []Promotion
	for _, p := range candidates {
		if reason := gate(p, in, goods); reason != "" {
			out.Skipped = append(out.Skipped, skipped(p, reason))
			continue
		}
		if p.Stackable {
			stackable = append(stackable, p)
		} else {
			single = append(single, p)
		}
	}
	// The best single non-stackable promotion, each evaluated alone.
	best := -1
	var singles []promoEffect
	for _, p := range single {
		eff, reason := e.apply(p, in, append([]money.Money(nil), base...))
		if reason != "" {
			out.Skipped = append(out.Skipped, skipped(p, reason))
			continue
		}
		singles = append(singles, eff)
		if best < 0 || eff.Benefit.Cmp(singles[best].Benefit) > 0 {
			best = len(singles) - 1
		}
	}
	// The stackable set, cumulatively.
	sort.SliceStable(stackable, func(a, b int) bool { return kindOrder[stackable[a].Kind] < kindOrder[stackable[b].Kind] })
	left := append([]money.Money(nil), base...)
	var stacked []promoEffect
	stackBenefit := money.ZeroMoney
	for _, p := range stackable {
		eff, reason := e.apply(p, in, left)
		if reason != "" {
			out.Skipped = append(out.Skipped, skipped(p, reason))
			continue
		}
		stacked = append(stacked, eff)
		stackBenefit = stackBenefit.Add(eff.Benefit)
	}
	// The best single beats a stack worth less; with nothing stacked it applies even at no value
	// (a gift whose SKU has no sale price). Ties among singles keep the oldest, as best is only
	// replaced by a strictly greater benefit.
	if best >= 0 && (len(stacked) == 0 || singles[best].Benefit.Cmp(stackBenefit) > 0) {
		out.Applied = []promoEffect{singles[best]}
		for k, eff := range singles {
			if k != best {
				out.Skipped = append(out.Skipped, skipped(eff.Promotion, SkipNotStackable))
			}
		}
		for _, eff := range stacked {
			out.Skipped = append(out.Skipped, skipped(eff.Promotion, SkipLowerBenefit))
		}
		return out
	}
	out.Applied = stacked
	for _, eff := range singles {
		out.Skipped = append(out.Skipped, skipped(eff.Promotion, SkipNotStackable))
	}
	return out
}

func minMoney(a, b money.Money) money.Money {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// candidatePromotions loads the promotions a document may use: the automatic ones (no code)
// active and valid on date, and the ones whose code the document carries, whatever their state
// (codeProblem tells why a code is not usable). Oldest first.
func candidatePromotions(ctx context.Context, q db.Querier, date time.Time, codes []string) ([]Promotion, error) {
	if codes == nil {
		codes = []string{}
	}
	var out []Promotion
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT `+promotionColumns+` FROM promotions p
		WHERE (p.code IS NULL AND p.status = 'active' AND p.valid_from <= $1 AND p.valid_to >= $1)
		   OR p.code = ANY($2)
		ORDER BY p.created_at, p.id`, date, codes); err != nil {
		return nil, fmt.Errorf("pricing: read candidate promotions: %w", err)
	}
	if err := loadPromotionItems(ctx, q, out); err != nil {
		return nil, err
	}
	return out, nil
}

// codeProblem is why the promotion of a code cannot be used on date by a customer of groupID,
// or "".
func codeProblem(p Promotion, date time.Time, groupID *uuid.UUID) string {
	switch {
	case p.Status != PromoActive:
		return CodeInactive
	case date.Before(p.ValidFrom):
		return CodeNotStarted
	case date.After(p.ValidTo):
		return CodeExpired
	case p.UsageLimit != nil && p.UsedCount >= *p.UsageLimit:
		return SkipUsageLimitReached
	case p.CustomerGroupID != nil && !sameID(p.CustomerGroupID, groupID):
		return SkipCustomerGroup
	}
	return ""
}

// CodeCheck is the answer to a promotion code lookup. Promotion is set only for a usable code.
type CodeCheck struct {
	Code      string
	Valid     bool
	Reason    string
	Promotion *Promotion
}

// CodeCheckInput is a promotion code to check for a customer on a business date (nil is today).
type CodeCheckInput struct {
	Code            string
	PartyID         *uuid.UUID
	CustomerGroupID *uuid.UUID
	BusinessDate    *time.Time
}

// ValidateCode tells whether a promotion code can be used now by the customer. Only a usable
// code reveals its promotion; the endpoint is rate limited against enumeration.
func (s *Service) ValidateCode(ctx context.Context, scope Scope, in CodeCheckInput) (CodeCheck, error) {
	code, ok := normalizeCode(in.Code)
	if !ok {
		return CodeCheck{}, errInvalid(map[string]string{"code": "invalid"})
	}
	out := CodeCheck{Code: code}
	err := s.readPriced(ctx, scope, func(q db.Querier, tp tenantPricing) error {
		b, err := resolveBuyer(ctx, q, in.PartyID, in.CustomerGroupID)
		if err != nil {
			return err
		}
		var p Promotion
		err = q.QueryRowxContext(ctx, `SELECT `+promotionColumns+` FROM promotions p WHERE p.code = $1`, code).StructScan(&p)
		if db.IsNoRows(err) {
			out.Reason = CodeNotFound
			return nil
		}
		if err != nil {
			return fmt.Errorf("pricing: read promotion by code: %w", err)
		}
		if out.Reason = codeProblem(p, s.businessDate(in.BusinessDate, tp), b.GroupID); out.Reason != "" {
			return nil
		}
		promos := []Promotion{p}
		if err := loadPromotionItems(ctx, q, promos); err != nil {
			return err
		}
		out.Valid, out.Promotion = true, &promos[0]
		return nil
	})
	return out, err
}

// RecordUsage counts one use of a promotion by a posted document inside the posting transaction.
// The promotion row is locked so concurrent postings cannot exceed its usage limit; recording the
// same document again changes nothing (reposting is idempotent).
func (s *Service) RecordUsage(ctx context.Context, q db.Querier, tenantID, promotionID uuid.UUID, docType string, docID uuid.UUID, amount money.Money) error {
	f := fieldErrors{}
	if docType != DocInvoice && docType != DocSalesDocument {
		f.add("doc_type", "invalid")
	}
	if docID == uuid.Nil {
		f.add("doc_id", "required")
	}
	if amount.IsNegative() {
		f.add("discount_amount", "negative")
	}
	if err := f.err(); err != nil {
		return err
	}
	var limit *int
	err := q.QueryRowxContext(ctx, `SELECT usage_limit FROM promotions WHERE id = $1 FOR UPDATE`, promotionID).Scan(&limit)
	if db.IsNoRows(err) {
		return errNotFound()
	}
	if err != nil {
		return fmt.Errorf("pricing: lock promotion: %w", err)
	}
	var already bool
	var used int
	if err := q.QueryRowxContext(ctx, `
		SELECT count(*) FILTER (WHERE doc_type = $2 AND doc_id = $3) > 0, count(*)
		FROM promotion_usages WHERE promotion_id = $1`, promotionID, docType, docID).Scan(&already, &used); err != nil {
		return fmt.Errorf("pricing: count promotion usages: %w", err)
	}
	if already {
		return nil
	}
	if limit != nil && used >= *limit {
		return errUsageLimitReached()
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO promotion_usages (id, tenant_id, promotion_id, doc_type, doc_id, discount_amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT ux_promotion_usages__promotion_id_doc_type_doc_id DO NOTHING`,
		ids.New(), tenantID, promotionID, docType, docID, amount, s.clock.Now()); err != nil {
		return fmt.Errorf("pricing: record promotion usage: %w", err)
	}
	return nil
}
