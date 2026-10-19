package pricing

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

var calcDate = clock.Date(2026, time.March, 15)

// tline is one draft line of a hand-built calculation: the unit price the resolver gave, the
// quantity, the manual discount, the SKU's tax rate and minimum price ("" for none).
type tline struct {
	price, qty, disc, rate, minPrice string
	sku                              uuid.UUID
}

type tdoc struct {
	includeTax bool
	mode       string
	orderDisc  string
	lines      []tline
	promos     []Promotion
	gifts      map[uuid.UUID]string
}

func optMoney(s string) *money.Money {
	if s == "" {
		return nil
	}
	return ptr(money.MustMoney(s))
}

func orZeroString(s string) money.Money {
	if s == "" {
		return money.ZeroMoney
	}
	return money.MustMoney(s)
}

// input builds the calculator input of d as the database steps would.
func (d tdoc) input() calcInput {
	in := calcInput{
		Date:     calcDate,
		Settings: tenantPricing{Location: time.UTC, PricesIncludeTax: d.includeTax, RoundingMode: d.mode},
		Draft:    DocumentDraft{OrderDiscount: orZeroString(d.orderDisc)},
		Gifts:    map[uuid.UUID]giftPrice{},
	}
	for i, l := range d.lines {
		id := l.sku
		if id == uuid.Nil {
			id = ids.New()
		}
		rate := l.rate
		if rate == "" {
			rate = "0"
		}
		sku := &skuInfo{ID: id, BaseUnitID: ids.New(), MinPrice: optMoney(l.minPrice), TaxRate: money.MustPercent(rate)}
		qty := money.MustQty(l.qty)
		in.Draft.Lines = append(in.Draft.Lines, DraftLine{SkuID: id, Qty: qty, DiscountAmount: orZeroString(l.disc)})
		in.Lines = append(in.Lines, pricedLine{
			Index: i, Sku: sku, UnitID: sku.BaseUnitID, IsBase: true, Factor: money.FactorOne, Qty: qty, BaseQty: qty,
		})
		in.Prices = append(in.Prices, ResolvedPrice{
			Amount: money.MustMoney(l.price), Source: PriceSource{Kind: SourceBaseList},
			UnitID: sku.BaseUnitID, Factor: money.FactorOne, BaseQty: qty,
		})
	}
	in.Candidates = d.promos
	for id, price := range d.gifts {
		in.Gifts[id] = giftPrice{
			Sku:   &skuInfo{ID: id, BaseUnitID: ids.New(), TaxRate: money.MustPercent("8")},
			Price: ResolvedPrice{Amount: money.MustMoney(price), Source: PriceSource{Kind: SourceBaseList}},
		}
	}
	return in
}

func calc(t *testing.T, d tdoc) CalculatedDocument {
	t.Helper()
	doc, err := DocumentCalculator{Engine: NewPromotionEngine()}.compute(d.input())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	checkConsistent(t, doc)
	return doc
}

func mustEq(t *testing.T, what string, got money.Money, want string) {
	t.Helper()
	if !got.Equal(money.MustMoney(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// checkConsistent asserts the invariants every calculated document keeps: whole đồng amounts,
// each line = gross − discounts (+ tax when prices exclude it), and the totals add up.
func checkConsistent(t *testing.T, doc CalculatedDocument) {
	t.Helper()
	sum := struct{ goods, disc, tax, total money.Money }{}
	for i, l := range doc.Lines {
		for name, m := range map[string]money.Money{
			"gross": l.GrossAmount, "discount": l.DiscountAmount, "order alloc": l.OrderDiscountAlloc,
			"tax": l.TaxAmount, "line": l.LineAmount,
		} {
			if !m.Decimal().Equal(m.Decimal().Truncate(0)) {
				t.Fatalf("line %d %s = %s, not whole đồng", i, name, m)
			}
			if m.IsNegative() {
				t.Fatalf("line %d %s = %s, negative", i, name, m)
			}
		}
		want := l.GrossAmount.Sub(l.DiscountAmount).Sub(l.OrderDiscountAlloc)
		if !doc.PricesIncludeTax {
			want = want.Add(l.TaxAmount)
		}
		if !l.LineAmount.Equal(want) {
			t.Fatalf("line %d amount %s, want gross %s − discount %s − alloc %s (tax %s)", i, l.LineAmount,
				l.GrossAmount, l.DiscountAmount, l.OrderDiscountAlloc, l.TaxAmount)
		}
		sum.goods = sum.goods.Add(l.GrossAmount)
		sum.disc = sum.disc.Add(l.DiscountAmount).Add(l.OrderDiscountAlloc)
		sum.tax = sum.tax.Add(l.TaxAmount)
		sum.total = sum.total.Add(l.LineAmount)
	}
	if !sum.goods.Equal(doc.GoodsAmount) || !sum.disc.Equal(doc.DiscountAmount) || !sum.tax.Equal(doc.TaxAmount) ||
		!sum.total.Equal(doc.TotalAmount) {
		t.Fatalf("totals %+v do not add up to the lines %+v", doc, sum)
	}
	want := doc.GoodsAmount.Sub(doc.DiscountAmount)
	if !doc.PricesIncludeTax {
		want = want.Add(doc.TaxAmount)
	}
	if !doc.TotalAmount.Equal(want) {
		t.Fatalf("total %s, want goods − discount (+ tax) = %s", doc.TotalAmount, want)
	}
}

func TestCalculateGoldenPricesIncludeTax(t *testing.T) {
	for _, mode := range []string{RoundByLine, RoundByDocument} {
		t.Run(mode, func(t *testing.T) {
			doc := calc(t, tdoc{includeTax: true, mode: mode, orderDisc: "25000", lines: []tline{
				{price: "100000", qty: "2", rate: "10"},
				{price: "50000", qty: "1", rate: "8"},
			}})
			mustEq(t, "goods", doc.GoodsAmount, "250000")
			mustEq(t, "discount", doc.DiscountAmount, "25000")
			mustEq(t, "line 0 alloc", doc.Lines[0].OrderDiscountAlloc, "20000")
			mustEq(t, "line 1 alloc", doc.Lines[1].OrderDiscountAlloc, "5000")
			// 180 000 × 10 / 110 = 16 363.64; 45 000 × 8 / 108 = 3 333.33.
			mustEq(t, "line 0 tax", doc.Lines[0].TaxAmount, "16364")
			mustEq(t, "line 1 tax", doc.Lines[1].TaxAmount, "3333")
			mustEq(t, "tax", doc.TaxAmount, "19697")
			mustEq(t, "total", doc.TotalAmount, "225000")
			mustEq(t, "net unit price", doc.Lines[0].NetUnitPrice, "90000")
			if doc.RoundingMode != mode || !doc.PricesIncludeTax {
				t.Fatalf("settings echoed as %s/%v", doc.RoundingMode, doc.PricesIncludeTax)
			}
		})
	}
}

func TestCalculateGoldenPricesExcludeTax(t *testing.T) {
	for _, mode := range []string{RoundByLine, RoundByDocument} {
		t.Run(mode, func(t *testing.T) {
			doc := calc(t, tdoc{includeTax: false, mode: mode, orderDisc: "10000", lines: []tline{
				{price: "33333", qty: "3", rate: "10"},
				{price: "10001", qty: "1", rate: "10"},
			}})
			// 10 000 over 99 999 : 10 001 is 9 090.8182 : 909.1818, rounded keeping the total.
			mustEq(t, "line 0 alloc", doc.Lines[0].OrderDiscountAlloc, "9091")
			mustEq(t, "line 1 alloc", doc.Lines[1].OrderDiscountAlloc, "909")
			mustEq(t, "line 0 tax", doc.Lines[0].TaxAmount, "9091")
			mustEq(t, "line 1 tax", doc.Lines[1].TaxAmount, "909")
			mustEq(t, "line 0", doc.Lines[0].LineAmount, "99999")
			mustEq(t, "line 1", doc.Lines[1].LineAmount, "10001")
			mustEq(t, "tax", doc.TaxAmount, "10000")
			mustEq(t, "total", doc.TotalAmount, "110000")
		})
	}
}

// Three lines of 1 005 at 10 % tax included hold 91.36 tax each: rounding by line gives 273,
// rounding once per document gives round(274.09) = 274, the extra đồng on the first line.
func TestCalculateRoundingModesDiffer(t *testing.T) {
	lines := []tline{{price: "1005", qty: "1", rate: "10"}, {price: "1005", qty: "1", rate: "10"}, {price: "1005", qty: "1", rate: "10"}}
	byLine := calc(t, tdoc{includeTax: true, mode: RoundByLine, lines: lines})
	byDoc := calc(t, tdoc{includeTax: true, mode: RoundByDocument, lines: lines})
	mustEq(t, "line tax", byLine.TaxAmount, "273")
	mustEq(t, "document tax", byDoc.TaxAmount, "274")
	mustEq(t, "document first line tax", byDoc.Lines[0].TaxAmount, "92")
	mustEq(t, "line total", byLine.TotalAmount, "3015")
	mustEq(t, "document total", byDoc.TotalAmount, "3015")
}

func TestCalculateFractionalAmountsRoundPerMode(t *testing.T) {
	// 12 500.5 × 3 = 37 501.5 and 3 333.3333 × 3 = 9 999.9999: line rounding rounds each gross,
	// document rounding keeps the lines exact until the column totals.
	lines := []tline{{price: "12500.5", qty: "3", rate: "8"}, {price: "3333.3333", qty: "3", rate: "0"}}
	byLine := calc(t, tdoc{includeTax: true, mode: RoundByLine, lines: lines})
	mustEq(t, "line gross 0", byLine.Lines[0].GrossAmount, "37502")
	mustEq(t, "line gross 1", byLine.Lines[1].GrossAmount, "10000")
	mustEq(t, "line total", byLine.TotalAmount, "47502")
	byDoc := calc(t, tdoc{includeTax: true, mode: RoundByDocument, lines: lines})
	// round(37 501.5 + 9 999.9999) = 47 501.
	mustEq(t, "document total", byDoc.TotalAmount, "47501")
}

func TestCalculateTaxRateZeroAndSnapshot(t *testing.T) {
	doc := calc(t, tdoc{includeTax: false, mode: RoundByLine, lines: []tline{{price: "20000", qty: "1.5", rate: "0"}}})
	mustEq(t, "tax", doc.TaxAmount, "0")
	mustEq(t, "total", doc.TotalAmount, "30000")
	if doc.Lines[0].TaxRateSnap.String() != "0" || doc.Lines[0].LineKind != LineItem || doc.Lines[0].DraftIndex != 0 {
		t.Fatalf("line = %+v", doc.Lines[0])
	}
}

func TestCalculateLineDiscountAndValidation(t *testing.T) {
	doc := calc(t, tdoc{includeTax: true, mode: RoundByLine, lines: []tline{{price: "10000", qty: "2", disc: "1500"}}})
	mustEq(t, "discount", doc.Lines[0].DiscountAmount, "1500")
	mustEq(t, "total", doc.TotalAmount, "18500")
	mustEq(t, "net unit", doc.Lines[0].NetUnitPrice, "9250")

	_, err := DocumentCalculator{Engine: NewPromotionEngine()}.compute(
		tdoc{mode: RoundByLine, lines: []tline{{price: "10000", qty: "1", disc: "10001"}}}.input())
	wantField(t, err, "lines[0].discount_amount", "exceeds_amount")

	_, err = DocumentCalculator{Engine: NewPromotionEngine()}.compute(
		tdoc{mode: RoundByLine, orderDisc: "20001", lines: []tline{{price: "10000", qty: "2"}}}.input())
	wantField(t, err, "order_discount", "exceeds_amount")
}

func wantField(t *testing.T, err error, field, reason string) {
	t.Helper()
	var ae *apperr.Error
	if !asAppErr(err, &ae) || ae.Kind != apperr.Validation || ae.Fields[field] != reason {
		t.Fatalf("err = %v, want %s: %s", err, field, reason)
	}
}

func asAppErr(err error, target **apperr.Error) bool {
	return errors.As(err, target)
}

func TestValidateDraft(t *testing.T) {
	one := []DraftLine{{SkuID: ids.New(), Qty: money.MustQty("1")}}
	cases := []struct {
		name   string
		draft  DocumentDraft
		field  string
		reason string
	}{
		{"no lines", DocumentDraft{}, "lines", "required"},
		{"too many lines", DocumentDraft{Lines: make([]DraftLine, MaxDocumentLines+1)}, "lines", "too_many"},
		{"negative price", DocumentDraft{Lines: []DraftLine{{UnitPrice: ptr(money.MustMoney("-1"))}}}, "lines[0].unit_price", "negative"},
		{"negative discount", DocumentDraft{Lines: []DraftLine{{DiscountAmount: money.MustMoney("-1")}}}, "lines[0].discount_amount", "negative"},
		{"negative order discount", DocumentDraft{Lines: one, OrderDiscount: money.MustMoney("-5")}, "order_discount", "negative"},
		{"bad code", DocumentDraft{Lines: one, PromotionCodes: []string{"a b"}}, "promotion_codes[0]", "invalid"},
		{"too many codes", DocumentDraft{Lines: one, PromotionCodes: make([]string, MaxPromotionCodes+1)}, "promotion_codes", "too_many"},
		{"codes on a purchase", DocumentDraft{Lines: one, PromotionCodes: []string{"SALE10"}, SkipPromotions: true}, "promotion_codes", "not_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateDraft(tc.draft)
			wantField(t, err, tc.field, tc.reason)
		})
	}
	codes, err := validateDraft(DocumentDraft{Lines: one, PromotionCodes: []string{" sale10 ", "SALE10", "vip-2"}})
	if err != nil || fmt.Sprint(codes) != "[SALE10 VIP-2]" {
		t.Fatalf("codes = %v, %v; want normalized distinct codes", codes, err)
	}
}

func TestCalculateMinPriceFlag(t *testing.T) {
	doc := calc(t, tdoc{includeTax: true, mode: RoundByLine, orderDisc: "2000", lines: []tline{
		{price: "10000", qty: "2", minPrice: "9500"},
		{price: "10000", qty: "2", minPrice: "9000"},
		{price: "10000", qty: "2"},
	}})
	// 2 000 over three equal lines: 666.6667 off each line, net 9 666.67 per unit.
	if doc.Lines[0].BelowMinPrice || doc.Lines[1].BelowMinPrice || doc.Lines[2].BelowMinPrice {
		t.Fatalf("below flags = %v %v %v", doc.Lines[0].BelowMinPrice, doc.Lines[1].BelowMinPrice, doc.Lines[2].BelowMinPrice)
	}
	doc = calc(t, tdoc{includeTax: true, mode: RoundByLine, orderDisc: "2000", lines: []tline{
		{price: "10000", qty: "2", minPrice: "9500"},
	}})
	if !doc.Lines[0].BelowMinPrice {
		t.Fatal("9 000 net per unit under a 9 500 minimum is not flagged")
	}
	mustEq(t, "net unit price", doc.Lines[0].NetUnitPrice, "9000")
	mustEq(t, "min price snap", *doc.Lines[0].MinPriceSnap, "9500")
	v := MinPriceGuard{}.Violations(doc)
	if len(v) != 1 || v[0].DraftIndex != 0 || !v[0].MinPrice.Equal(money.MustMoney("9500")) {
		t.Fatalf("violations = %+v", v)
	}
	// Exactly the minimum is allowed.
	doc = calc(t, tdoc{includeTax: true, mode: RoundByLine, lines: []tline{{price: "9500", qty: "1", minPrice: "9500"}}})
	if doc.Lines[0].BelowMinPrice || len(MinPriceGuard{}.Violations(doc)) != 0 {
		t.Fatal("a price equal to the minimum is flagged")
	}
}

func TestErrBelowMinPriceNamesLines(t *testing.T) {
	err := errBelowMinPrice([]MinPriceViolation{{DraftIndex: 2}, {DraftIndex: 0}})
	var ae *apperr.Error
	if !asAppErr(err, &ae) || ae.Kind != apperr.ExceptionRequired || ae.ExceptionKind != "below_min_price" {
		t.Fatalf("err = %#v", err)
	}
	if ae.Fields["lines[2].unit_price"] != "below_min_price" || ae.Fields["lines[0].unit_price"] != "below_min_price" {
		t.Fatalf("fields = %v", ae.Fields)
	}
}

func TestFitWithin(t *testing.T) {
	m := func(ss ...string) []money.Money {
		out := make([]money.Money, len(ss))
		for i, s := range ss {
			out[i] = money.MustMoney(s)
		}
		return out
	}
	got := fitWithin(m("12", "3", "0"), m("10", "5", "8"))
	if fmt.Sprint(got) != "[10 3 2]" {
		t.Fatalf("fitWithin = %v, want the excess on the part with the most room", got)
	}
	got = fitWithin(m("4", "4"), m("5", "5"))
	if fmt.Sprint(got) != "[4 4]" {
		t.Fatalf("fitWithin within caps = %v", got)
	}
}

// Random documents in both rounding modes and both tax bases: every line stays consistent, the
// lines add up to the document total, and rounding by document never drifts from the exact total
// by more than half a đồng.
func TestPropertyRandomDocumentsReconcile(t *testing.T) {
	r := rand.New(rand.NewSource(20261017))
	prices := []string{"1000", "1005", "2500.5", "3333.3333", "12500", "15000", "99999", "0.5", "125000.75"}
	qtys := []string{"1", "2", "3", "0.5", "1.25", "7", "24", "48", "0.3333"}
	rates := []string{"0", "5", "8", "10"}
	for n := 0; n < 1000; n++ {
		var d tdoc
		d.includeTax = r.Intn(2) == 0
		d.mode = []string{RoundByLine, RoundByDocument}[n%2]
		lines := r.Intn(12) + 1
		exactGross := decimal.Zero
		for i := 0; i < lines; i++ {
			l := tline{price: prices[r.Intn(len(prices))], qty: qtys[r.Intn(len(qtys))], rate: rates[r.Intn(len(rates))]}
			gross := money.MustMoney(l.price).MulQty(money.MustQty(l.qty))
			if r.Intn(3) == 0 {
				l.disc = gross.Decimal().Mul(decimal.NewFromFloat(r.Float64() / 2)).Round(money.MoneyScale).String()
			}
			exactGross = exactGross.Add(gross.Decimal()).Sub(orZeroString(l.disc).Decimal())
			d.lines = append(d.lines, l)
		}
		if r.Intn(2) == 0 && exactGross.IsPositive() {
			d.orderDisc = exactGross.Mul(decimal.NewFromFloat(r.Float64() / 3)).Truncate(0).String()
		}
		t.Run(fmt.Sprintf("doc%d", n), func(t *testing.T) {
			doc := calc(t, d)
			if d.mode != RoundByDocument {
				return
			}
			exact, err := DocumentCalculator{}.exactTotal(d.input())
			if err != nil {
				t.Fatal(err)
			}
			if !doc.TotalAmount.Equal(exact.RoundVND()) {
				t.Fatalf("document total %s, exact %s", doc.TotalAmount, exact)
			}
		})
	}
}

// exactTotal is the unrounded document total of in (no promotions), for the reconcile test.
func (c DocumentCalculator) exactTotal(in calcInput) (money.Money, error) {
	basis := TaxBasis{IncludeTax: in.Settings.PricesIncludeTax}
	lines, err := priceLines(in)
	if err != nil {
		return money.Money{}, err
	}
	if err := discountLines(in.Draft, lines); err != nil {
		return money.Money{}, err
	}
	if err := allocateOrderDiscount(in.Draft.OrderDiscount, lines); err != nil {
		return money.Money{}, err
	}
	total := money.ZeroMoney
	for _, l := range lines {
		tax, err := basis.tax(l.amt.net(), l.amt.Rate)
		if err != nil {
			return money.Money{}, err
		}
		total = total.Add(basis.line(l.amt.net(), tax))
	}
	return total, nil
}
