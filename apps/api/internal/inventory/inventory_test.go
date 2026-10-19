package inventory

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func dayPtr(s string) *time.Time {
	t := day(s)
	return &t
}

func qty(s string) money.Qty     { return money.MustQty(s) }
func vnd(s string) money.Money   { return money.MustMoney(s) }
func qtyPtr(s string) *money.Qty { q := qty(s); return &q }

func stock(onHand, value, avg string) SkuStock {
	return SkuStock{SkuID: ids.New(), OnHand: qty(onHand), Sellable: qty(onHand), StockValue: vnd(value), AvgCost: vnd(avg)}
}

func wantCode(t *testing.T, err error, kind apperr.Kind, code string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Kind != kind || e.Code != code {
		t.Fatalf("error = %v, want %v %s", err, kind, code)
	}
	return e
}

// wantErr is wantCode for a caller that only checks the error.
func wantErr(t *testing.T, err error, kind apperr.Kind, code string) {
	t.Helper()
	_ = wantCode(t, err, kind, code)
}

func TestMovingAverageReceiveValuesQuantityAtUnitCost(t *testing.T) {
	v, err := MovingAverageCosting{}.OnReceive(SkuStock{}, qty("1.5"), vnd("333.3333"))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Equal(vnd("500")) {
		t.Errorf("value = %s, want 500 (499.99995 rounded to 4 places)", v)
	}
	_, err = MovingAverageCosting{}.OnReceive(SkuStock{}, qty("1"), vnd("-1"))
	if e := wantCode(t, err, apperr.Validation, "validation_failed"); e.Fields["unit_cost"] != "negative" {
		t.Errorf("fields = %v", e.Fields)
	}
}

func TestMovingAverageIssueTakesAverageAndTheLastIssueTakesTheRest(t *testing.T) {
	c := MovingAverageCosting{}
	s := stock("3", "1000", "333.3333")
	unit, v, err := c.OnIssue(s, qty("1"))
	if err != nil || !unit.Equal(vnd("333.3333")) || !v.Equal(vnd("333.3333")) {
		t.Fatalf("partial issue = %s %s %v", unit, v, err)
	}
	// The issue that empties the stock takes the whole remaining value, residue included.
	if _, v, _ = c.OnIssue(s, qty("3")); !v.Equal(vnd("1000")) {
		t.Errorf("emptying issue value = %s, want 1000", v)
	}
	// An average above the value never takes more than the stock is worth.
	if _, v, _ = c.OnIssue(stock("3", "100", "60"), qty("2")); !v.Equal(vnd("100")) {
		t.Errorf("capped issue value = %s, want 100", v)
	}
}

func TestMovingAverageCostAdjustNeedsStockAndKeepsValuePositive(t *testing.T) {
	c := MovingAverageCosting{}
	wantErr(t, c.OnCostAdjust(stock("0", "0", "10"), vnd("5")), apperr.Conflict, "no_stock_to_adjust")
	wantErr(t, c.OnCostAdjust(stock("2", "100", "50"), vnd("-101")), apperr.Conflict, "stock_value_negative")
	if err := c.OnCostAdjust(stock("2", "100", "50"), vnd("-100")); err != nil {
		t.Errorf("adjusting the value to zero = %v", err)
	}
}

func TestMovingAverageAverage(t *testing.T) {
	c := MovingAverageCosting{}
	if avg, err := c.Average(stock("20", "3000", "0")); err != nil || !avg.Equal(vnd("150")) {
		t.Errorf("avg = %s, %v; want 150", avg, err)
	}
	if avg, err := c.Average(stock("3", "1000", "0")); err != nil || !avg.Equal(vnd("333.3333")) {
		t.Errorf("avg = %s, %v; want 333.3333", avg, err)
	}
	// An empty stock keeps its last average.
	if avg, err := c.Average(stock("0", "0", "77")); err != nil || !avg.Equal(vnd("77")) {
		t.Errorf("empty avg = %s, %v; want 77", avg, err)
	}
	if _, err := c.Average(stock("1", "-1", "0")); err == nil {
		t.Error("a negative stock value must fail")
	}
}

func lotStock(color *uuid.UUID, expiry *time.Time, received string, q string) *LotStock {
	return &LotStock{Lot: Lot{ID: ids.New(), ColorID: color, ExpiryDate: expiry, ReceivedOn: day(received)}, Qty: qty(q)}
}

func TestPickersSelectLotsWithoutSubstitutingColors(t *testing.T) {
	blue, red := ids.New(), ids.New()
	lots := []*LotStock{
		lotStock(&blue, nil, "2026-03-01", "5"),
		lotStock(nil, nil, "2026-03-02", "2"),
		lotStock(&red, nil, "2026-03-03", "1"),
	}
	if got := (FefoLot{}).Candidates(lots); len(got) != 3 {
		t.Errorf("fefo = %d lots, want all 3", len(got))
	}
	if got := (ExactColorLot{ColorID: &red}).Candidates(lots); len(got) != 1 || got[0] != lots[2] {
		t.Errorf("exact red = %v", got)
	}
	if got := (ExactColorLot{}).Candidates(lots); len(got) != 1 || got[0] != lots[1] {
		t.Errorf("exact uncolored = %v, want the lot without a color", got)
	}
	other := ids.New()
	if got := (ExactColorLot{ColorID: &other}).Candidates(lots); len(got) != 0 {
		t.Errorf("a color without lots = %v, want none", got)
	}
	if got := (ExplicitLot{LotID: lots[1].ID}).Candidates(lots); len(got) != 1 || got[0] != lots[1] {
		t.Errorf("explicit = %v", got)
	}
	if got := (ExplicitLot{LotID: ids.New()}).Candidates(lots); got != nil {
		t.Errorf("explicit unknown = %v, want nil", got)
	}
}

func TestFefoOrderPutsFirstExpiringFirstAndUndatedLast(t *testing.T) {
	undated := lotStock(nil, nil, "2026-01-01", "1")
	late := lotStock(nil, dayPtr("2026-06-01"), "2026-01-01", "1")
	earlyOld := lotStock(nil, dayPtr("2026-04-01"), "2026-01-01", "1")
	earlyNew := lotStock(nil, dayPtr("2026-04-01"), "2026-02-01", "1")
	got := []*LotStock{undated, late, earlyNew, earlyOld}
	slices.SortFunc(got, fefoCompare)
	want := []*LotStock{earlyOld, earlyNew, late, undated}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d = %v, want %v", i, got[i].ReceivedOn, want[i].ReceivedOn)
		}
	}
}

func TestLotExpiresAfterItsExpiryDay(t *testing.T) {
	l := Lot{ExpiryDate: dayPtr("2026-03-15")}
	if l.Expired(day("2026-03-15")) {
		t.Error("a lot still sells on its expiry day")
	}
	if !l.Expired(day("2026-03-16")) {
		t.Error("a lot is expired the day after its expiry")
	}
	if (Lot{}).Expired(day("2100-01-01")) {
		t.Error("a lot without expiry never expires")
	}
}

func TestLotStateOn(t *testing.T) {
	today := day("2026-03-15")
	cases := []struct {
		lot  Lot
		want string
	}{
		{Lot{IsQuarantine: true, ExpiryDate: dayPtr("2026-01-01")}, LotStateQuarantine},
		{Lot{ExpiryDate: dayPtr("2026-03-14")}, LotStateExpired},
		{Lot{ExpiryDate: dayPtr("2026-04-14")}, LotStateExpiring},
		{Lot{ExpiryDate: dayPtr("2026-04-15")}, LotStateNormal},
		{Lot{}, LotStateNormal},
	}
	for _, c := range cases {
		if got := (LotView{LotStock: LotStock{Lot: c.lot}}).StateOn(today, 30); got != c.want {
			t.Errorf("lot %+v = %s, want %s", c.lot.ExpiryDate, got, c.want)
		}
	}
}

func TestAdjustmentPolicies(t *testing.T) {
	for _, kind := range []string{KindIncrease, KindOpening, KindDecrease, KindDamage, KindLoss, KindGift,
		KindInternalUse, KindDisposal, KindQuarantineIn, KindQuarantineRelease} {
		if _, ok := PolicyOf(kind); !ok {
			t.Errorf("%s has no policy", kind)
		}
	}
	if _, ok := PolicyOf(KindCount); ok {
		t.Error("count DCs are posted by stock counts only")
	}
	if p, _ := PolicyOf(KindDisposal); p.exception != exception.StockDisposal || p.side != sideIssue {
		t.Errorf("disposal policy = %+v", p)
	}
	if p, _ := PolicyOf(KindQuarantineRelease); p.exception != exception.QuarantineRelease || !p.release {
		t.Errorf("quarantine release policy = %+v", p)
	}
	if p, _ := PolicyOf(KindOpening); !p.costRequired || len(p.reasonKinds) != 0 || p.move != MoveOpening {
		t.Errorf("opening policy = %+v", p)
	}
	for kind, want := range map[string]bool{KindDisposal: true, KindQuarantineRelease: true, KindDamage: false,
		KindOpening: false, "nonsense": false} {
		if NeedsReauth(kind, "") != want {
			t.Errorf("NeedsReauth(%s) = %v", kind, !want)
		}
	}
	// A decrease or a quarantine citing a disposal reason disposes of stock: it needs the disposal
	// confirmation. An increase citing it does not.
	for kind, want := range map[string]exception.Kind{KindDecrease: exception.StockDisposal,
		KindQuarantineIn: exception.StockDisposal, KindIncrease: "", KindQuarantineRelease: exception.QuarantineRelease} {
		p, _ := PolicyOf(kind)
		if got := p.exceptionFor(KindDisposal); got != want {
			t.Errorf("%s citing disposal consumes %q, want %q", kind, got, want)
		}
		if NeedsReauth(kind, KindDisposal) != (want != "") {
			t.Errorf("NeedsReauth(%s, disposal) = %v", kind, want == "")
		}
	}
	if p, _ := PolicyOf(KindDecrease); p.exceptionFor("damage") != "" {
		t.Error("a decrease citing damage needs no confirmation")
	}
}

func TestAdjustmentValidation(t *testing.T) {
	reason, color := ids.New(), ids.New()
	cost := vnd("10")
	negative := vnd("-1")
	opening, _ := PolicyOf(KindOpening)
	damage, _ := PolicyOf(KindDamage)
	increase, _ := PolicyOf(KindIncrease)

	if err := (AdjustmentInput{Kind: KindDamage, ReasonID: &reason, Lines: []AdjustmentLineInput{{SkuID: ids.New(), Qty: qty("1")}}}).validate(damage); err != nil {
		t.Fatalf("valid damage = %v", err)
	}
	long := strings.Repeat("ồ", maxNoteLen+1)
	err := AdjustmentInput{Kind: KindOpening, ReasonID: &reason, Note: &long, Lines: []AdjustmentLineInput{
		{Qty: qty("0"), Uncolored: true},
		{SkuID: ids.New(), Qty: qty("1"), UnitCost: &negative},
	}}.validate(opening)
	e := wantCode(t, err, apperr.Validation, "validation_failed")
	for field, want := range map[string]string{
		"reason_id": "not_allowed", "note": "too_long", "lines.0.sku_id": "required", "lines.0.qty": "must_be_positive",
		"lines.0.uncolored": "not_allowed", "lines.0.unit_cost": "required", "lines.1.unit_cost": "must_not_be_negative",
	} {
		if e.Fields[field] != want {
			t.Errorf("%s = %q, want %q (fields %v)", field, e.Fields[field], want, e.Fields)
		}
	}

	err = AdjustmentInput{Kind: KindDamage, Lines: []AdjustmentLineInput{
		{SkuID: ids.New(), Qty: qty("1"), ColorID: &color, Uncolored: true, UnitCost: &cost, LotNo: "L1", ExpiryDate: dayPtr("2026-01-01")},
	}}.validate(damage)
	e = wantCode(t, err, apperr.Validation, "validation_failed")
	for field, want := range map[string]string{
		"reason_id": "required", "lines.0.uncolored": "conflicts_with_color", "lines.0.unit_cost": "not_allowed",
		"lines.0.lot_no": "not_allowed", "lines.0.expiry_date": "not_allowed",
	} {
		if e.Fields[field] != want {
			t.Errorf("%s = %q, want %q (fields %v)", field, e.Fields[field], want, e.Fields)
		}
	}

	e = wantCode(t, AdjustmentInput{Kind: KindIncrease, ReasonID: &reason}.validate(increase), apperr.Validation, "validation_failed")
	if e.Fields["lines"] != "required" {
		t.Errorf("no lines = %v", e.Fields)
	}
	many := make([]AdjustmentLineInput, maxAdjustmentLines+1)
	for i := range many {
		many[i] = AdjustmentLineInput{SkuID: ids.New(), Qty: qty("1")}
	}
	e = wantCode(t, AdjustmentInput{Kind: KindIncrease, ReasonID: &reason, Lines: many}.validate(increase), apperr.Validation, "validation_failed")
	if e.Fields["lines"] != "too_many" {
		t.Errorf("too many lines = %v", e.Fields)
	}
}

func TestInsufficientStockNamesEveryColorAndTheRequest(t *testing.T) {
	blue, red := "xanh", "do"
	err := errInsufficientStock("AO-01", &red, qty("3"), []ColorAvailability{
		{ColorCode: &blue, Qty: qty("12")}, {ColorCode: &red, Qty: qty("0")}, {Qty: qty("2")},
	})
	e := wantCode(t, err, apperr.Conflict, CodeInsufficientStock)
	for field, want := range map[string]string{
		"sku": "AO-01", "requested_qty": "3", "requested_color": "do",
		"available.xanh": "12", "available.do": "0", "available.none": "2",
	} {
		if e.Fields[field] != want {
			t.Errorf("%s = %q, want %q", field, e.Fields[field], want)
		}
	}
	e = wantCode(t, errInsufficientStock("AO-02", nil, qty("1"), nil), apperr.Conflict, CodeInsufficientStock)
	if _, ok := e.Fields["requested_color"]; ok {
		t.Errorf("an uncolored request names no color: %v", e.Fields)
	}
	empty := ""
	if colorKey(&empty) != "none" || colorKey(nil) != "none" {
		t.Error("an empty color code is the stock without a color")
	}
}

func TestAutoConvertWritesLargerUnitsFirst(t *testing.T) {
	a := AutoConvert{Base: "cái", Units: []cardUnit{
		{Name: "thùng", Factor: decimal.NewFromInt(100)},
		{Name: "bó", Factor: decimal.NewFromInt(10)},
		{Name: "cái", Factor: decimal.NewFromInt(1)},
	}}
	for in, want := range map[string]string{
		"180": "1 thùng 8 bó (180 cái)",
		"7":   "7 cái",
		"200": "2 thùng (200 cái)",
		"183": "1 thùng 8 bó 3 cái (183 cái)",
		"-15": "-1 bó 5 cái (-15 cái)",
	} {
		if got := a.Format(qty(in)); got != want {
			t.Errorf("Format(%s) = %q, want %q", in, got, want)
		}
	}
	f := FixedUnit{Unit: cardUnit{Name: "thùng", Factor: decimal.NewFromInt(100)}}
	if got := f.Format(qty("180")); got != "1.8 thùng" {
		t.Errorf("fixed = %q, want 1.8 thùng", got)
	}
}

func TestSignalOfComparesAvailableToMinAndOnHandToMax(t *testing.T) {
	s := SkuStock{OnHand: qty("10"), Sellable: qty("8"), Reserved: qty("3")} // available 5
	if got := signalOf(s, qty("6"), nil); !got.low || got.over {
		t.Errorf("available 5 under min 6 = %+v", got)
	}
	if got := signalOf(s, qty("5"), nil); got.low {
		t.Errorf("available equal to min is not low: %+v", got)
	}
	if got := signalOf(s, qty("0"), qtyPtr("9")); got.low || !got.over {
		t.Errorf("on hand 10 over max 9 = %+v", got)
	}
	if got := signalOf(s, qty("0"), qtyPtr("10")); got.over {
		t.Errorf("on hand equal to max is not over: %+v", got)
	}
}

func TestRecomputeSplitsLotsIntoQuarantineExpiredAndSellable(t *testing.T) {
	s := SkuStock{SkuID: ids.New(), OnHand: qty("10")}
	other := ids.New()
	lots := []LotStock{
		{Lot: Lot{SkuID: s.SkuID}, Qty: qty("4")},
		{Lot: Lot{SkuID: s.SkuID, IsQuarantine: true, ExpiryDate: dayPtr("2026-01-01")}, Qty: qty("1")},
		{Lot: Lot{SkuID: s.SkuID, ExpiryDate: dayPtr("2026-03-14")}, Qty: qty("2")},
		{Lot: Lot{SkuID: s.SkuID, ExpiryDate: dayPtr("2026-03-15")}, Qty: qty("3")},
		{Lot: Lot{SkuID: s.SkuID}, Qty: qty("0")},
		{Lot: Lot{SkuID: other}, Qty: qty("99")},
	}
	if err := recompute(&s, lots, day("2026-03-15")); err != nil {
		t.Fatal(err)
	}
	if !s.QuarantineQty.Equal(qty("1")) || !s.ExpiredQty.Equal(qty("2")) || !s.Sellable.Equal(qty("7")) {
		t.Errorf("split = quarantine %s expired %s sellable %s", s.QuarantineQty, s.ExpiredQty, s.Sellable)
	}
	s.OnHand = qty("11")
	if err := recompute(&s, lots, day("2026-03-15")); err == nil {
		t.Error("lots that disagree with on hand must fail")
	}
}

func TestBlindCountHidesSystemQuantitiesWhileCounting(t *testing.T) {
	counted := qty("4")
	variance := qty("-1")
	d := CountDetail{
		StockCount: StockCount{ID: ids.New(), Blind: true, Status: CountCounting, BusinessDate: day("2026-03-15")},
		Lines: []CountLineView{
			{StockCountLine: StockCountLine{ID: ids.New(), SystemQty: qty("5"), CountedQty: &counted, VarianceQty: &variance}},
			{StockCountLine: StockCountLine{ID: ids.New(), SystemQty: qty("2")}},
		},
	}
	out := toCountDTO(d)
	for _, l := range out.Lines {
		if l.SystemQty != nil || l.MovedSinceStartQty != nil || l.VarianceQty != nil {
			t.Errorf("blind counting line shows system figures: %+v", l)
		}
	}
	if out.CountedLines == nil || *out.CountedLines != 1 || out.VarianceLines != nil {
		t.Errorf("blind summary = counted %v variance %v", out.CountedLines, out.VarianceLines)
	}
	if out.Lines[0].CountedQty == nil || !out.Lines[0].CountedQty.Equal(counted) {
		t.Error("the counted quantity stays visible")
	}

	d.Status = CountPendingPost
	out = toCountDTO(d)
	if out.Lines[0].SystemQty == nil || !out.Lines[0].SystemQty.Equal(qty("5")) || out.Lines[0].VarianceQty == nil {
		t.Errorf("finished blind count hides figures: %+v", out.Lines[0])
	}
	if out.VarianceLines == nil || *out.VarianceLines != 1 {
		t.Errorf("variance lines = %v, want 1", out.VarianceLines)
	}
}

func move(t MoveType, q, value, date string) StockMove {
	return StockMove{ID: ids.New(), MoveType: t, DocType: DocStockAdjustment, Qty: qty(q), Value: vnd(value), BusinessDate: day(date)}
}

func TestReplayRevaluesABackdatedReceiptAndItsReversal(t *testing.T) {
	// Recorded: +10 @ 100 on 10/03, a sale of 5 on 12/03 at 100, then a receipt of +10 @ 200
	// backdated to 11/03. In business date order the sale should have taken the 150 average.
	in1 := move(MoveOpening, "10", "1000", "2026-03-10")
	in2 := move(MoveOpening, "10", "2000", "2026-03-11")
	out := move(MoveSale, "-5", "-500", "2026-03-12")
	r, err := replay([]StockMove{in1, in2, out})
	if err != nil {
		t.Fatal(err)
	}
	if !r.state.qty.Equal(qty("15")) || !r.state.value.Equal(vnd("2250")) || !r.state.avg.Equal(vnd("150")) {
		t.Errorf("state = %s / %s @ %s, want 15 / 2250 @ 150", r.state.qty, r.state.value, r.state.avg)
	}
	if !r.issuedDelta.Equal(vnd("250")) {
		t.Errorf("issued delta = %s, want 250", r.issuedDelta)
	}

	back := move(MoveReversal, "5", "500", "2026-03-13")
	back.ReversalOf = &out.ID
	r, err = replay([]StockMove{in1, in2, out, back})
	if err != nil {
		t.Fatal(err)
	}
	if !r.issuedDelta.IsZero() || !r.state.qty.Equal(qty("20")) || !r.state.value.Equal(vnd("3000")) {
		t.Errorf("after reversal = delta %s state %s / %s, want 0 and 20 / 3000", r.issuedDelta, r.state.qty, r.state.value)
	}

	// Earlier corrections are recomputed, not replayed.
	fix := move(MoveCostAdjustment, "0", "250", "2026-03-14")
	fix.DocType = DocCostRecalc
	r, err = replay([]StockMove{in1, in2, out, fix})
	if err != nil || !r.state.value.Equal(vnd("2250")) {
		t.Errorf("replay with an earlier recost = %s, %v; want 2250", r.state.value, err)
	}
}

func TestReplayStrandsCostThatMeetsNoStock(t *testing.T) {
	adj := move(MoveCostAdjustment, "0", "40", "2026-03-10")
	r, err := replay([]StockMove{adj})
	if err != nil {
		t.Fatal(err)
	}
	if !r.stranded.Equal(vnd("40")) || !r.state.value.IsZero() {
		t.Errorf("stranded = %s value = %s, want 40 and 0", r.stranded, r.state.value)
	}
}

// The SKU-restricted verifier reads the asked SKUs only: every grouped ledger subquery and the
// outer query carry the restriction, and no check keeps an "all SKUs" escape that would turn a
// one-SKU check into a full scan.
func TestVerifyChecksRestrictEverySubqueryToTheAskedSkus(t *testing.T) {
	for _, c := range verifyChecks {
		marks := len(skuFilterRe.FindAllString(c.query, -1))
		if want := 1 + strings.Count(c.query, "GROUP BY"); marks < want {
			t.Errorf("%s: %d SKU restrictions, want at least %d (one per grouped subquery and the outer query)", c.name, marks, want)
		}
		filtered := skuFilterRe.ReplaceAllString(c.query, "$1 = ANY($$1)")
		if strings.Contains(filtered, "{{") || strings.Count(filtered, "= ANY($1)") != marks {
			t.Errorf("%s: filtered query keeps a mark: %s", c.name, filtered)
		}
		if all := skuFilterRe.ReplaceAllString(c.query, "TRUE"); strings.Contains(all, "$1") {
			t.Errorf("%s: the unfiltered query still takes a parameter: %s", c.name, all)
		}
	}
}
