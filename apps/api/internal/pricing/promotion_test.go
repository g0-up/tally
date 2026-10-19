package pricing

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// promo is an active promotion valid around calcDate.
func promo(kind, value string, stackable bool, items ...PromotionItem) Promotion {
	return Promotion{
		ID: ids.New(), Name: kind, Kind: kind, Value: money.MustMoney(value), Stackable: stackable,
		Status: PromoActive, ValidFrom: calcDate.AddDate(0, 0, -1), ValidTo: calcDate.AddDate(0, 0, 1), Items: items,
	}
}

func item(role string, sku uuid.UUID, qty string) PromotionItem {
	return PromotionItem{ID: ids.New(), Role: role, SkuID: sku, Qty: money.MustQty(qty)}
}

func withCap(p Promotion, limit string) Promotion {
	p.MaxDiscount = ptr(money.MustMoney(limit))
	return p
}

func pline(sku uuid.UUID, qty, net string) promoLine {
	return promoLine{SkuID: sku, BaseQty: money.MustQty(qty), Net: money.MustMoney(net)}
}

func evaluate(candidates []Promotion, lines ...promoLine) promoOutcome {
	return NewPromotionEngine().Evaluate(candidates, promoInput{Lines: lines, Date: calcDate, GiftPrices: map[uuid.UUID]money.Money{}})
}

func onlyApplied(t *testing.T, out promoOutcome, want ...Promotion) {
	t.Helper()
	if len(out.Applied) != len(want) {
		t.Fatalf("applied %d promotions (skipped %+v), want %d", len(out.Applied), out.Skipped, len(want))
	}
	for i, p := range want {
		if out.Applied[i].Promotion.ID != p.ID {
			t.Fatalf("applied[%d] = %s, want %s", i, out.Applied[i].Promotion.Kind, p.Kind)
		}
	}
}

func skipReason(t *testing.T, out promoOutcome, p Promotion) string {
	t.Helper()
	for _, s := range out.Skipped {
		if s.ID == p.ID {
			return s.Reason
		}
	}
	t.Fatalf("%s not skipped (skipped %+v)", p.Kind, out.Skipped)
	return ""
}

func TestLinePercentRuleWithCap(t *testing.T) {
	a, b, c := ids.New(), ids.New(), ids.New()
	p := promo(PromoLinePercent, "10", true, item(RoleEligible, a, "1"), item(RoleEligible, b, "1"))
	out := evaluate([]Promotion{p}, pline(a, "2", "100000"), pline(b, "1", "50000"), pline(c, "1", "70000"))
	onlyApplied(t, out, p)
	eff := out.Applied[0]
	mustEq(t, "line a", eff.Line[0], "10000")
	mustEq(t, "line b", eff.Line[1], "5000")
	mustEq(t, "ineligible line", eff.Line[2], "0")
	mustEq(t, "benefit", eff.Benefit, "15000")

	// A 9 000 cap scales the parts down keeping their 2:1 proportion.
	out = evaluate([]Promotion{withCap(p, "9000")}, pline(a, "2", "100000"), pline(b, "1", "50000"))
	mustEq(t, "capped a", out.Applied[0].Line[0], "6000")
	mustEq(t, "capped b", out.Applied[0].Line[1], "3000")
	mustEq(t, "capped benefit", out.Applied[0].Benefit, "9000")

	// A promotion without eligible items covers every line; one with no matching line gives nothing.
	all := promo(PromoLinePercent, "5", true)
	mustEq(t, "every line", evaluate([]Promotion{all}, pline(c, "1", "70000")).Applied[0].Benefit, "3500")
	out = evaluate([]Promotion{p}, pline(c, "1", "70000"))
	if skipReason(t, out, p) != SkipNoEligibleLines {
		t.Fatalf("skipped %+v", out.Skipped)
	}
}

func TestLineAmountRulePerBaseUnitAtMostTheLine(t *testing.T) {
	a := ids.New()
	p := promo(PromoLineAmount, "2000", true)
	out := evaluate([]Promotion{p}, pline(a, "3", "30000"))
	mustEq(t, "per base unit", out.Applied[0].Line[0], "6000")
	out = evaluate([]Promotion{p}, pline(a, "3", "5000"))
	mustEq(t, "at most the line", out.Applied[0].Line[0], "5000")
}

func TestOrderRules(t *testing.T) {
	a, b := ids.New(), ids.New()
	pct := promo(PromoOrderPercent, "10", true)
	out := evaluate([]Promotion{pct}, pline(a, "1", "30000"), pline(b, "1", "10000"))
	eff := out.Applied[0]
	mustEq(t, "order percent", eff.Benefit, "4000")
	mustEq(t, "alloc a", eff.OrderAlloc[0], "3000")
	mustEq(t, "alloc b", eff.OrderAlloc[1], "1000")

	out = evaluate([]Promotion{withCap(pct, "2500")}, pline(a, "1", "30000"), pline(b, "1", "10000"))
	mustEq(t, "capped order percent", out.Applied[0].Benefit, "2500")

	amt := promo(PromoOrderAmount, "50000", true)
	out = evaluate([]Promotion{amt}, pline(a, "1", "30000"), pline(b, "1", "10000"))
	mustEq(t, "order amount at most the goods", out.Applied[0].Benefit, "40000")
}

func TestGiftRuleSetsAndCap(t *testing.T) {
	buy, gift := ids.New(), ids.New()
	p := promo(PromoGift, "0", true, item(RoleEligible, buy, "6"), item(RoleGift, gift, "1"))
	in := promoInput{Lines: []promoLine{pline(buy, "13", "130000")}, Date: calcDate,
		GiftPrices: map[uuid.UUID]money.Money{gift: money.MustMoney("5000")}}
	out := NewPromotionEngine().Evaluate([]Promotion{p}, in)
	eff := out.Applied[0]
	if len(eff.Gifts) != 1 || !eff.Gifts[0].Qty.Equal(money.MustQty("2")) {
		t.Fatalf("gifts = %+v, want 2 sets out of 13", eff.Gifts)
	}
	mustEq(t, "gift value", eff.Benefit, "10000")
	mustEq(t, "no line discount", eff.Line[0], "0")

	out = NewPromotionEngine().Evaluate([]Promotion{withCap(p, "7000")}, in)
	if !out.Applied[0].Gifts[0].Qty.Equal(money.MustQty("1")) {
		t.Fatalf("capped gifts = %+v, want one set", out.Applied[0].Gifts)
	}
	capped := withCap(p, "4999")
	out = NewPromotionEngine().Evaluate([]Promotion{capped}, in)
	if skipReason(t, out, capped) != SkipMaxDiscount {
		t.Fatalf("skipped %+v", out.Skipped)
	}
	in.Lines = []promoLine{pline(buy, "5", "50000")}
	out = NewPromotionEngine().Evaluate([]Promotion{p}, in)
	if skipReason(t, out, p) != SkipNoEligibleLines {
		t.Fatalf("skipped %+v", out.Skipped)
	}
}

func TestComboPriceRule(t *testing.T) {
	a, b := ids.New(), ids.New()
	p := promo(PromoComboPrice, "25000", true, item(RoleComboComponent, a, "1"), item(RoleComboComponent, b, "2"))
	// Two full sets (2 a, 4 b) out of 3 a and 4 b: in-set value 20 000 + 20 000, combo 50 000 → no gain.
	out := evaluate([]Promotion{p}, pline(a, "3", "30000"), pline(b, "4", "20000"))
	if skipReason(t, out, p) != SkipNoBenefit {
		t.Fatalf("skipped %+v", out.Skipped)
	}
	cheap := promo(PromoComboPrice, "15000", true, item(RoleComboComponent, a, "1"), item(RoleComboComponent, b, "2"))
	out = evaluate([]Promotion{cheap}, pline(a, "3", "30000"), pline(b, "4", "20000"))
	eff := out.Applied[0]
	mustEq(t, "combo discount", eff.Benefit, "10000")
	mustEq(t, "a share", eff.Line[0], "5000")
	mustEq(t, "b share", eff.Line[1], "5000")
	out = evaluate([]Promotion{cheap}, pline(a, "3", "30000"))
	if skipReason(t, out, cheap) != SkipNoEligibleLines {
		t.Fatalf("skipped %+v", out.Skipped)
	}
}

func TestStackingAppliesInKindOrderOnWhatIsLeft(t *testing.T) {
	a := ids.New()
	order := promo(PromoOrderPercent, "10", true)
	line := promo(PromoLinePercent, "10", true)
	// Given in creation order order-first, the line discount still applies first.
	out := evaluate([]Promotion{order, line}, pline(a, "1", "100000"))
	onlyApplied(t, out, line, order)
	mustEq(t, "line", out.Applied[0].Benefit, "10000")
	mustEq(t, "order on what is left", out.Applied[1].Benefit, "9000")
}

func TestStackingBestSingleAgainstTheStack(t *testing.T) {
	a := ids.New()
	s1 := promo(PromoLinePercent, "10", true)
	s2 := promo(PromoOrderAmount, "5000", true)
	small := promo(PromoOrderAmount, "12000", false)
	big := promo(PromoOrderPercent, "20", false)
	// Stack: 10 000 + 5 000 = 15 000; best single 20 000 wins.
	out := evaluate([]Promotion{s1, s2, small, big}, pline(a, "1", "100000"))
	onlyApplied(t, out, big)
	if skipReason(t, out, small) != SkipNotStackable || skipReason(t, out, s1) != SkipLowerBenefit ||
		skipReason(t, out, s2) != SkipLowerBenefit {
		t.Fatalf("skipped %+v", out.Skipped)
	}
	// Without the big one the stack (15 000) beats the single 12 000.
	out = evaluate([]Promotion{s1, s2, small}, pline(a, "1", "100000"))
	onlyApplied(t, out, s1, s2)
	if skipReason(t, out, small) != SkipNotStackable {
		t.Fatalf("skipped %+v", out.Skipped)
	}
	// A tie keeps the stack.
	tie := promo(PromoOrderAmount, "15000", false)
	onlyApplied(t, evaluate([]Promotion{s1, s2, tie}, pline(a, "1", "100000")), s1, s2)
}

func TestUnpricedGiftStillApplies(t *testing.T) {
	buy, gift := ids.New(), ids.New()
	first := promo(PromoGift, "0", false, item(RoleEligible, buy, "1"), item(RoleGift, gift, "1"))
	second := promo(PromoGift, "0", false, item(RoleEligible, buy, "1"), item(RoleGift, gift, "2"))
	// The gift SKU has no sale price, so the gift is worth nothing but is still handed out.
	out := evaluate([]Promotion{first}, pline(buy, "1", "10000"))
	onlyApplied(t, out, first)
	if g := out.Applied[0].Gifts; len(g) != 1 || g[0].SkuID != gift || !g[0].Qty.Equal(money.MustQty("1")) {
		t.Fatalf("gifts = %+v, want one unpriced gift", g)
	}
	mustEq(t, "gift value", out.Applied[0].Benefit, "0")
	// Equal zero-value singles keep the older one.
	out = evaluate([]Promotion{first, second}, pline(buy, "1", "10000"))
	onlyApplied(t, out, first)
	if skipReason(t, out, second) != SkipNotStackable {
		t.Fatalf("skipped %+v", out.Skipped)
	}
	// A stack worth something beats the unpriced gift.
	stack := promo(PromoOrderAmount, "1000", true)
	out = evaluate([]Promotion{first, stack}, pline(buy, "1", "10000"))
	onlyApplied(t, out, stack)
	if skipReason(t, out, first) != SkipNotStackable {
		t.Fatalf("skipped %+v", out.Skipped)
	}
}

func TestPromotionGates(t *testing.T) {
	a, group := ids.New(), ids.New()
	inactive := promo(PromoOrderAmount, "1000", true)
	inactive.Status = PromoInactive
	future := promo(PromoOrderAmount, "1000", true)
	future.ValidFrom = calcDate.AddDate(0, 0, 1)
	past := promo(PromoOrderAmount, "1000", true)
	past.ValidTo = calcDate.AddDate(0, 0, -1)
	vip := promo(PromoOrderAmount, "1000", true)
	vip.CustomerGroupID = &group
	used := promo(PromoOrderAmount, "1000", true)
	used.UsageLimit, used.UsedCount = ptr(3), 3
	minOrder := promo(PromoOrderAmount, "1000", true)
	minOrder.MinOrderAmount = money.MustMoney("100001")
	edge := promo(PromoOrderAmount, "1000", true)
	edge.ValidFrom, edge.ValidTo, edge.MinOrderAmount = calcDate, calcDate, money.MustMoney("100000")

	out := evaluate([]Promotion{inactive, future, past, vip, used, minOrder, edge}, pline(a, "1", "100000"))
	for p, want := range map[*Promotion]string{
		&inactive: CodeInactive, &future: CodeNotStarted, &past: CodeExpired, &vip: SkipCustomerGroup,
		&used: SkipUsageLimitReached, &minOrder: SkipBelowMinOrder,
	} {
		if got := skipReason(t, out, *p); got != want {
			t.Errorf("%s: reason %q, want %q", p.Name, got, want)
		}
	}
	// Valid on both bounds and exactly at the minimum order.
	onlyApplied(t, out, edge)

	in := promoInput{Lines: []promoLine{pline(a, "1", "100000")}, Date: calcDate, GroupID: &group}
	onlyApplied(t, NewPromotionEngine().Evaluate([]Promotion{vip}, in), vip)
}

func TestCodeProblem(t *testing.T) {
	group := ids.New()
	p := promo(PromoOrderAmount, "1000", true)
	if r := codeProblem(p, calcDate, nil); r != "" {
		t.Fatalf("usable code: %q", r)
	}
	p.CustomerGroupID = &group
	if r := codeProblem(p, calcDate, nil); r != SkipCustomerGroup {
		t.Fatalf("other group: %q", r)
	}
	if r := codeProblem(p, calcDate.Add(-time.Nanosecond).AddDate(0, 0, -1), &group); r != CodeNotStarted {
		t.Fatalf("not started: %q", r)
	}
}

// A stackable gift appends gift lines before a stackable order discount applies; the order
// discount spreads over the document lines only and the gift line stays at zero.
func TestCalculateGiftThenOrderDiscount(t *testing.T) {
	buy, gift := ids.New(), ids.New()
	g := promo(PromoGift, "0", true, item(RoleEligible, buy, "2"), item(RoleGift, gift, "1"))
	o := promo(PromoOrderPercent, "10", true)
	doc := calc(t, tdoc{includeTax: true, mode: RoundByLine, promos: []Promotion{o, g},
		gifts: map[uuid.UUID]string{gift: "7000"},
		lines: []tline{{sku: buy, price: "10000", qty: "4", rate: "10"}, {price: "20000", qty: "1", rate: "10"}}})
	if len(doc.Lines) != 3 || doc.Lines[2].LineKind != LineGift || !doc.Lines[2].Qty.Equal(money.MustQty("2")) {
		t.Fatalf("lines = %+v, want two items and a gift of 2", doc.Lines)
	}
	if doc.Lines[2].DraftIndex != -1 || !doc.Lines[2].LineAmount.IsZero() || doc.Lines[2].TaxRateSnap.String() != "8" {
		t.Fatalf("gift line = %+v", doc.Lines[2])
	}
	mustEq(t, "order alloc 0", doc.Lines[0].OrderDiscountAlloc, "4000")
	mustEq(t, "order alloc 1", doc.Lines[1].OrderDiscountAlloc, "2000")
	mustEq(t, "total", doc.TotalAmount, "54000")
	if len(doc.Promotions) != 2 || doc.Promotions[0].ID != g.ID || !doc.Promotions[0].Amount.Equal(money.MustMoney("14000")) {
		t.Fatalf("promotions = %+v", doc.Promotions)
	}
}
