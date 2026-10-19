package pricing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// matcherFixture is a SKU sold by the unit (base) and by the case of 24.
type matcherFixture struct {
	sku        *skuInfo
	caseUnit   uuid.UUID
	red, white uuid.UUID
}

func newMatcherFixture() matcherFixture {
	f := matcherFixture{caseUnit: ids.New(), red: ids.New(), white: ids.New()}
	f.sku = &skuInfo{ID: ids.New(), BaseUnitID: ids.New(), Units: map[uuid.UUID]money.Factor{}}
	f.sku.Units[f.caseUnit] = money.MustFactor("24")
	return f
}

func (f matcherFixture) baseLine(qty string, color *uuid.UUID) pricedLine {
	q := money.MustQty(qty)
	return pricedLine{Sku: f.sku, ColorID: color, UnitID: f.sku.BaseUnitID, IsBase: true, Factor: money.FactorOne, Qty: q, BaseQty: q}
}

func (f matcherFixture) caseLine(qty string, color *uuid.UUID) pricedLine {
	q := money.MustQty(qty)
	factor := money.MustFactor("24")
	base, err := money.BaseQty(q, factor)
	if err != nil {
		panic(err)
	}
	return pricedLine{Sku: f.sku, ColorID: color, UnitID: f.caseUnit, Factor: factor, Qty: q, BaseQty: base}
}

func row(color, unit *uuid.UUID, minQty, price string) priceRow {
	return priceRow{ID: ids.New(), ColorID: color, UnitID: unit, MinQty: money.MustQty(minQty), Price: money.MustMoney(price)}
}

func match(t *testing.T, rows []priceRow, l pricedLine) (priceRow, string) {
	t.Helper()
	r, price, ok := ListRowMatcher{}.Match(rows, l)
	if !ok {
		return priceRow{}, ""
	}
	return r, price.String()
}

func TestMatcherColorBeforeGeneral(t *testing.T) {
	f := newMatcherFixture()
	general := row(nil, nil, "0", "10000")
	red := row(&f.red, nil, "0", "11000")
	rows := []priceRow{general, red}
	if r, p := match(t, rows, f.baseLine("1", &f.red)); r.ID != red.ID || p != "11000" {
		t.Fatalf("red line priced %s by %v", p, r.ColorID)
	}
	if r, p := match(t, rows, f.baseLine("1", &f.white)); r.ID != general.ID || p != "10000" {
		t.Fatalf("white line priced %s, want the general price", p)
	}
	if r, _ := match(t, rows, f.baseLine("1", nil)); r.ID != general.ID {
		t.Fatal("a line without color is not priced by the general row")
	}
	// A color row in the base unit beats a general row in the line's own unit.
	caseGeneral := row(nil, &f.caseUnit, "0", "200000")
	if r, p := match(t, []priceRow{caseGeneral, red}, f.caseLine("1", &f.red)); r.ID != red.ID || p != "264000" {
		t.Fatalf("red case priced %s, want the red base price × 24", p)
	}
}

func TestMatcherUnitRowBeforeBaseTimesFactor(t *testing.T) {
	f := newMatcherFixture()
	base := row(nil, nil, "0", "10000")
	caseRow := row(nil, &f.caseUnit, "0", "220000")
	if r, p := match(t, []priceRow{base, caseRow}, f.caseLine("1", nil)); r.ID != caseRow.ID || p != "220000" {
		t.Fatalf("case priced %s, want its own row", p)
	}
	if r, p := match(t, []priceRow{base}, f.caseLine("1", nil)); r.ID != base.ID || p != "240000" {
		t.Fatalf("case priced %s, want base × 24", p)
	}
	// A base line never takes a case row.
	if r, _ := match(t, []priceRow{caseRow}, f.baseLine("30", nil)); r.ID != uuid.Nil {
		t.Fatal("a base-unit line was priced by a case row")
	}
	// A row whose unit is the base unit is a base row.
	explicit := row(nil, &f.sku.BaseUnitID, "0", "9000")
	if _, p := match(t, []priceRow{explicit}, f.baseLine("1", nil)); p != "9000" {
		t.Fatalf("explicit base row priced %s", p)
	}
}

func TestMatcherTiersInBaseUnits(t *testing.T) {
	f := newMatcherFixture()
	rows := []priceRow{row(nil, nil, "0", "10000"), row(nil, nil, "48", "9000"), row(nil, nil, "100", "8500")}
	for _, tc := range []struct {
		name string
		line pricedLine
		want string
	}{
		{"47 units stay below the 48 tier", f.baseLine("47", nil), "10000"},
		{"exactly 48 units reach it", f.baseLine("48", nil), "9000"},
		{"2 cases are 48 base units", f.caseLine("2", nil), "216000"},
		{"1.5 cases are 36 base units", f.caseLine("1.5", nil), "240000"},
		{"the highest tier reached wins", f.baseLine("250", nil), "8500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, p := match(t, rows, tc.line); p != tc.want {
				t.Fatalf("priced %s, want %s", p, tc.want)
			}
		})
	}
	if r, _ := match(t, []priceRow{row(nil, nil, "10", "9000")}, f.baseLine("9", nil)); r.ID != uuid.Nil {
		t.Fatal("a line below every tier matched")
	}
}

func TestListSourcesFilterByBuyer(t *testing.T) {
	party, group, other := ids.New(), ids.New(), ids.New()
	lists := []PriceList{
		{ID: ids.New(), Kind: KindCustomer, PartyID: &party},
		{ID: ids.New(), Kind: KindCustomer, PartyID: &other},
		{ID: ids.New(), Kind: KindGroup, CustomerGroupID: &group},
		{ID: ids.New(), Kind: KindSeason},
		{ID: ids.New(), Kind: KindWholesale},
		{ID: ids.New(), Kind: KindRetail},
	}
	b := buyer{PartyID: &party, GroupID: &group, BaseKind: KindWholesale}
	for _, tc := range []struct {
		src  ListSource
		kind string
		want uuid.UUID
	}{
		{CustomerListSource(), SourceCustomerList, lists[0].ID},
		{GroupListSource(), SourceGroupList, lists[2].ID},
		{SeasonListSource(), SourceSeasonList, lists[3].ID},
		{BaseListSource(), SourceBaseList, lists[4].ID},
	} {
		got := tc.src.Lists(lists, b)
		if tc.src.Kind() != tc.kind || len(got) != 1 || got[0].ID != tc.want {
			t.Errorf("%s: lists %+v", tc.kind, got)
		}
	}
	walkIn := buyer{BaseKind: KindRetail}
	if len(CustomerListSource().Lists(lists, walkIn)) != 0 || len(GroupListSource().Lists(lists, walkIn)) != 0 {
		t.Fatal("a walk-in buyer got customer or group lists")
	}
	if got := BaseListSource().Lists(lists, walkIn); len(got) != 1 || got[0].Kind != KindRetail {
		t.Fatalf("walk-in base lists %+v", got)
	}
}

func TestDateUnmarshal(t *testing.T) {
	var d date
	if err := json.Unmarshal([]byte(`"2026-03-31"`), &d); err != nil {
		t.Fatal(err)
	}
	if !time.Time(d).Equal(clock.Date(2026, time.March, 31)) {
		t.Fatalf("date = %v", time.Time(d))
	}
	for _, bad := range []string{`"2026-02-30"`, `"31/03/2026"`, `20260331`, `null`} {
		if err := json.Unmarshal([]byte(bad), &d); err == nil && bad != `null` {
			t.Errorf("%s accepted", bad)
		}
	}
	var r struct {
		D *date `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"d":null}`), &r); err != nil || r.D != nil {
		t.Fatalf("null date = %v, %v", r.D, err)
	}
}

func TestSalePriceCommandValidate(t *testing.T) {
	color := ids.New()
	price := ptr(money.MustMoney("10000"))
	ok := SalePriceCommand{SkuID: ids.New(), Kind: KindRetail, Source: ChangeCatalog, Price: price}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid command: %v", err)
	}
	removeColor := SalePriceCommand{SkuID: ids.New(), Kind: KindWholesale, Source: ChangePriceList, ColorID: &color}
	if err := removeColor.validate(); err != nil {
		t.Fatalf("removing a color price: %v", err)
	}
	for _, tc := range []struct {
		name   string
		cmd    func(c SalePriceCommand) SalePriceCommand
		field  string
		reason string
	}{
		{"kind", func(c SalePriceCommand) SalePriceCommand { c.Kind = KindSeason; return c }, "kind", "invalid"},
		{"source", func(c SalePriceCommand) SalePriceCommand { c.Source = "pos"; return c }, "source", "invalid"},
		{"price", func(c SalePriceCommand) SalePriceCommand { c.Price = nil; return c }, "price", "required"},
		{"negative", func(c SalePriceCommand) SalePriceCommand { c.Price = ptr(money.MustMoney("-1")); return c }, "price", "negative"},
		{"all colors with a color", func(c SalePriceCommand) SalePriceCommand {
			c.ColorID, c.ApplyAllColors = &color, true
			return c
		}, "apply_all_colors", "not_allowed_with_color"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantField(t, tc.cmd(ok).validate(), tc.field, tc.reason)
		})
	}
}

func TestScopeLabel(t *testing.T) {
	color, name := ids.New(), "Đỏ"
	if got := (PriceChange{}).ScopeLabel(); got != WholeProductLabel {
		t.Fatalf("general = %q", got)
	}
	if got := (PriceChange{ColorID: &color, ColorName: &name}).ScopeLabel(); got != name {
		t.Fatalf("color = %q", got)
	}
}
