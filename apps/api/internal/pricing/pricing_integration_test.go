//go:build integration

package pricing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// raceEnabled is set in race builds (race_test.go).
var raceEnabled bool

type harness struct {
	svc *Service
	d   *db.DB
	clk *clock.Fixed
}

func newHarness(t testing.TB) harness {
	t.Helper()
	return newHarnessAt(t, testkit.Epoch)
}

func newHarnessAt(t testing.TB, now time.Time) harness {
	t.Helper()
	clk := clock.NewFixed(now)
	return harness{svc: NewService(Deps{DB: testkit.DB(t), Clock: clk, Audit: audit.NewRecorder(clk)}), d: testkit.DB(t), clk: clk}
}

// store is a provisioned tenant with the lookups the pricing tests need: the base lists, the
// units hộp (base) and thùng (24 hộp), the colors đỏ and trắng, a 10 % tax rate, a retail and a
// wholesale customer group, a VIP group on the wholesale list and one VIP customer.
type store struct {
	actor             audit.Actor
	scope             Scope
	hop, thung        uuid.UUID
	red, white        uuid.UUID
	vat10             uuid.UUID
	retail, wholesale uuid.UUID
	vip, customer     uuid.UUID
	skus              int
}

func (h harness) store(t testing.TB) *store {
	t.Helper()
	tn := testkit.NewTenant(t, h.d)
	st := &store{
		actor: audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb},
		scope: Scope{TenantID: tn.ID},
		hop:   ids.New(), thung: ids.New(), red: ids.New(), white: ids.New(), vat10: ids.New(),
		retail: ids.New(), wholesale: ids.New(), vip: ids.New(), customer: ids.New(),
	}
	now := testkit.Epoch
	ctx := context.Background()
	testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
		if err := NewSeeder().SeedPriceLists(ctx, q, SeedContext{TenantID: tn.ID, Now: now}); err != nil {
			return err
		}
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO units (id, tenant_id, name, name_key, created_at) VALUES ($2, $1, 'hộp', 'hộp', $4), ($3, $1, 'thùng', 'thùng', $4)`,
				[]any{tn.ID, st.hop, st.thung, now}},
			{`INSERT INTO colors (id, tenant_id, code, name, hex, created_at) VALUES ($2, $1, 'do', 'Đỏ', '#FF0000', $4), ($3, $1, 'trang', 'Trắng', '#FFFFFF', $4)`,
				[]any{tn.ID, st.red, st.white, now}},
			{`INSERT INTO tax_rates (id, tenant_id, name, name_key, rate_percent, created_at) VALUES ($2, $1, 'VAT 10%', 'vat 10%', 10, $3)`,
				[]any{tn.ID, st.vat10, now}},
			{`INSERT INTO customer_groups (id, tenant_id, name, name_key, base_price_kind, created_at, updated_at)
			  VALUES ($2, $1, 'Khách lẻ', 'khách lẻ', 'retail', $5, $5), ($3, $1, 'Khách sỉ', 'khách sỉ', 'wholesale', $5, $5),
			         ($4, $1, 'Đại lý VIP', 'đại lý vip', 'wholesale', $5, $5)`,
				[]any{tn.ID, st.retail, st.wholesale, st.vip, now}},
			{`INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($2, $1, 'Chị Lan', 'chị lan', $3, $3)`,
				[]any{tn.ID, st.customer, now}},
			{`INSERT INTO customer_profiles (tenant_id, party_id, customer_group_id, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)`,
				[]any{tn.ID, st.customer, st.vip, now}},
		} {
			if _, err := q.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
				return err
			}
		}
		return nil
	})
	return st
}

// sku creates a goods SKU sold by the hộp (base) and the thùng of 24, taxed 10 %, with an
// optional minimum price.
func (h harness) sku(t testing.TB, st *store, minPrice string) uuid.UUID {
	t.Helper()
	st.skus++
	product, sku := ids.New(), ids.New()
	name := fmt.Sprintf("Sữa tươi %d", st.skus)
	var floor *money.Money
	if minPrice != "" {
		floor = ptr(money.MustMoney(minPrice))
	}
	now := testkit.Epoch
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		ctx := context.Background()
		if _, err := q.ExecContext(ctx, `
			INSERT INTO products (id, tenant_id, type, name, name_key, created_at, updated_at) VALUES ($1, $2, 'goods', $3, $4, $5, $5)`,
			product, st.actor.TenantID, name, fmt.Sprintf("sua tuoi %d", st.skus), now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO skus (id, tenant_id, product_id, code, display_name, base_unit_id, min_price, tax_rate_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			sku, st.actor.TenantID, product, fmt.Sprintf("ST-%d", st.skus), name, st.hop, floor, st.vat10, now); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `
			INSERT INTO sku_units (tenant_id, sku_id, unit_id, factor, is_base, created_at, updated_at)
			VALUES ($1, $2, $3, 1, true, $5, $5), ($1, $2, $4, 24, false, $5, $5)`,
			st.actor.TenantID, sku, st.hop, st.thung, now)
		return err
	})
	return sku
}

// salePrice sets a base-list price (kind retail or wholesale) of sku, optionally of a color.
func (h harness) salePrice(t testing.TB, st *store, kind string, sku uuid.UUID, color *uuid.UUID, price string) []PriceChange {
	t.Helper()
	cmd := SalePriceCommand{SkuID: sku, ColorID: color, Kind: kind, Source: ChangeCatalog}
	if price != "" {
		cmd.Price = ptr(money.MustMoney(price))
	}
	changes, _, err := h.svc.SetSalePrice(context.Background(), st.actor, nil, cmd)
	if err != nil {
		t.Fatalf("set %s price %s: %v", kind, price, err)
	}
	return changes
}

func (h harness) list(t testing.TB, st *store, in PriceListInput, items ...ItemInput) PriceList {
	t.Helper()
	l, err := h.svc.CreatePriceList(context.Background(), st.actor, in)
	if err != nil {
		t.Fatalf("create list %s: %v", deref(in.Name), err)
	}
	if len(items) > 0 {
		if _, err := h.svc.PutPriceListItems(context.Background(), st.actor, l.ID, items); err != nil {
			t.Fatalf("put items of %s: %v", l.Name, err)
		}
	}
	return l
}

func priceItem(sku uuid.UUID, price string) ItemInput {
	return ItemInput{SkuID: sku, Price: ptr(money.MustMoney(price))}
}

// march is the validity of a season list running through March 2026.
var march = PriceListInput{ValidFrom: Some(clock.Date(2026, time.March, 1)), ValidTo: Some(clock.Date(2026, time.March, 31))}

func day(y int, m time.Month, d int) *time.Time { return ptr(clock.Date(y, m, d)) }

func (h harness) quote(t testing.TB, st *store, in QuoteInput) []ResolvedPrice {
	t.Helper()
	out, err := h.svc.Quote(context.Background(), st.scope, in)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	return out
}

func wantKind(t *testing.T, err error, kind apperr.Kind, code string) {
	t.Helper()
	if apperr.KindOf(err) != kind || (code != "" && !apperr.Is(err, code)) {
		t.Fatalf("err = %v, want %s %s", err, kind, code)
	}
}

func TestResolverChainPrecedence(t *testing.T) {
	h := newHarness(t)
	st := h.store(t)
	sku := h.sku(t, st, "")
	h.salePrice(t, st, KindWholesale, sku, nil, "100000")
	h.salePrice(t, st, KindRetail, sku, nil, "120000")
	season := h.list(t, st, PriceListInput{Name: ptr("Tết"), Kind: ptr(KindSeason),
		ValidFrom: Some(clock.Date(2026, time.March, 1)), ValidTo: Some(clock.Date(2026, time.March, 31))}, priceItem(sku, "99000"))
	group := h.list(t, st, PriceListInput{Name: ptr("Giá VIP"), Kind: ptr(KindGroup), CustomerGroupID: &st.vip}, priceItem(sku, "98000"))
	own := h.list(t, st, PriceListInput{Name: ptr("Giá chị Lan"), Kind: ptr(KindCustomer), PartyID: &st.customer}, priceItem(sku, "95000"))
	one := []PriceLine{{SkuID: sku, Qty: money.MustQty("1")}}

	for _, tc := range []struct {
		name  string
		in    QuoteInput
		want  string
		kind  string
		list  *uuid.UUID
		label string
	}{
		{"the customer's own list first", QuoteInput{PartyID: &st.customer}, "95000", SourceCustomerList, &own.ID, "Giá chị Lan"},
		{"then the group's list", QuoteInput{CustomerGroupID: &st.vip}, "98000", SourceGroupList, &group.ID, "Giá VIP"},
		{"then a season list", QuoteInput{CustomerGroupID: &st.wholesale}, "99000", SourceSeasonList, &season.ID, "Tết"},
		{"then the group's base list", QuoteInput{CustomerGroupID: &st.wholesale, BusinessDate: day(2026, time.April, 1)}, "100000", SourceBaseList, nil, ""},
		{"a walk-in customer gets the season list", QuoteInput{}, "99000", SourceSeasonList, &season.ID, "Tết"},
		{"and the retail list after the season", QuoteInput{BusinessDate: day(2026, time.April, 1)}, "120000", SourceBaseList, nil, ""},
		{"the season's last day still counts", QuoteInput{CustomerGroupID: &st.wholesale, BusinessDate: day(2026, time.March, 31)}, "99000", SourceSeasonList, &season.ID, "Tết"},
		{"and its first", QuoteInput{CustomerGroupID: &st.wholesale, BusinessDate: day(2026, time.March, 1)}, "99000", SourceSeasonList, &season.ID, "Tết"},
		{"but not the day before", QuoteInput{CustomerGroupID: &st.wholesale, BusinessDate: day(2026, time.February, 28)}, "100000", SourceBaseList, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Lines = one
			got := h.quote(t, st, tc.in)[0]
			if got.Amount.String() != tc.want || got.Source.Kind != tc.kind {
				t.Fatalf("price %s from %s, want %s from %s", got.Amount, got.Source.Kind, tc.want, tc.kind)
			}
			if tc.list != nil && (got.Source.PriceListID == nil || *got.Source.PriceListID != *tc.list ||
				got.Source.PriceListName == nil || *got.Source.PriceListName != tc.label) {
				t.Fatalf("source = %+v", got.Source)
			}
		})
	}

	// An inactive list is skipped.
	if _, err := h.svc.UpdatePriceList(context.Background(), st.actor, own.ID, own.Version, PriceListInput{Active: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	got := h.quote(t, st, QuoteInput{PartyID: &st.customer, Lines: one})[0]
	if got.Source.Kind != SourceGroupList || got.Amount.String() != "98000" {
		t.Fatalf("after deactivating the customer list: %s from %s", got.Amount, got.Source.Kind)
	}
	// A SKU no list prices is a manual price of 0.
	other := h.sku(t, st, "")
	got = h.quote(t, st, QuoteInput{Lines: []PriceLine{{SkuID: other, Qty: money.MustQty("1")}}})[0]
	if got.Source.Kind != SourceManual || !got.Amount.IsZero() {
		t.Fatalf("unpriced SKU: %s from %s", got.Amount, got.Source.Kind)
	}
}

// Within one level the lower priority wins, then the newest list; a list without a row for the
// SKU passes it on to the next list of the level.
func TestSameLevelListsByPriorityThenNewest(t *testing.T) {
	h := newHarness(t)
	st := h.store(t)
	sku, onlyOld := h.sku(t, st, ""), h.sku(t, st, "")
	season := func(name, price string, priority int, extra ...ItemInput) PriceList {
		h.clk.Advance(time.Minute)
		in := march
		in.Name, in.Kind, in.Priority = ptr(name), ptr(KindSeason), ptr(priority)
		return h.list(t, st, in, append([]ItemInput{priceItem(sku, price)}, extra...)...)
	}
	old := season("Cũ", "99000", 100, priceItem(onlyOld, "50000"))
	quote := func() []ResolvedPrice {
		return h.quote(t, st, QuoteInput{Lines: []PriceLine{{SkuID: sku, Qty: money.MustQty("1")}, {SkuID: onlyOld, Qty: money.MustQty("1")}}})
	}
	newer := season("Mới", "98500", 100)
	if got := quote(); *got[0].Source.PriceListID != newer.ID || got[0].Amount.String() != "98500" ||
		*got[1].Source.PriceListID != old.ID || got[1].Amount.String() != "50000" {
		t.Fatalf("equal priority: %s from %s, %s from %s", got[0].Amount, *got[0].Source.PriceListName, got[1].Amount, *got[1].Source.PriceListName)
	}
	first := season("Ưu tiên", "99500", 50)
	if got := quote(); *got[0].Source.PriceListID != first.ID || got[0].Amount.String() != "99500" {
		t.Fatalf("lower priority: %s from %s", got[0].Amount, *got[0].Source.PriceListName)
	}
}

// The business date is the tenant's (Asia/Ho_Chi_Minh), not UTC: at 00:30 on 1 April in Hà Nội it
// is still 31 March in UTC, yet the March season list no longer applies.
func TestSeasonEndsAtLocalMidnight(t *testing.T) {
	setup := newHarness(t)
	st := setup.store(t)
	sku := setup.sku(t, st, "")
	setup.salePrice(t, st, KindWholesale, sku, nil, "100000")
	setup.list(t, st, PriceListInput{Name: ptr("Tháng 3"), Kind: ptr(KindSeason),
		ValidFrom: Some(clock.Date(2026, time.March, 1)), ValidTo: Some(clock.Date(2026, time.March, 31))}, priceItem(sku, "99000"))
	in := QuoteInput{CustomerGroupID: &st.wholesale, Lines: []PriceLine{{SkuID: sku, Qty: money.MustQty("1")}}}
	for _, tc := range []struct {
		utc  time.Time
		want string
	}{
		{time.Date(2026, time.March, 31, 16, 59, 0, 0, time.UTC), "99000"},  // 23:59 on 31 March
		{time.Date(2026, time.March, 31, 17, 30, 0, 0, time.UTC), "100000"}, // 00:30 on 1 April
	} {
		h := newHarnessAt(t, tc.utc)
		if got := h.quote(t, st, in)[0].Amount.String(); got != tc.want {
			t.Errorf("at %s: %s, want %s", tc.utc, got, tc.want)
		}
	}
}

func TestTiersUnitsAndColors(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	sku := h.sku(t, st, "")
	retail := h.baseList(t, st, KindRetail)
	if _, err := h.svc.PutPriceListItems(ctx, st.actor, retail, []ItemInput{
		priceItem(sku, "10000"),
		{SkuID: sku, MinQty: ptr(money.MustQty("48")), Price: ptr(money.MustMoney("9000"))},
		{SkuID: sku, ColorID: &st.red, Price: ptr(money.MustMoney("11000"))},
	}); err != nil {
		t.Fatal(err)
	}
	boxed := h.sku(t, st, "")
	if _, err := h.svc.PutPriceListItems(ctx, st.actor, retail, []ItemInput{
		priceItem(boxed, "5000"), {SkuID: boxed, UnitID: &st.thung, Price: ptr(money.MustMoney("110000"))},
	}); err != nil {
		t.Fatal(err)
	}

	got := h.quote(t, st, QuoteInput{Lines: []PriceLine{
		{SkuID: sku, UnitID: &st.thung, Qty: money.MustQty("2")},
		{SkuID: sku, Qty: money.MustQty("47")},
		{SkuID: sku, Qty: money.MustQty("48")},
		{SkuID: sku, ColorID: &st.red, Qty: money.MustQty("1")},
		{SkuID: sku, ColorID: &st.white, Qty: money.MustQty("1")},
		{SkuID: boxed, UnitID: &st.thung, Qty: money.MustQty("1")},
		{SkuID: boxed, Qty: money.MustQty("3")},
	}})
	for i, want := range []string{"216000", "10000", "9000", "11000", "10000", "110000", "5000"} {
		if got[i].Amount.String() != want {
			t.Errorf("line %d: %s, want %s", i, got[i].Amount, want)
		}
	}
	if got[0].Source.MinQty == nil || got[0].Source.MinQty.String() != "48" || !got[0].BaseQty.Equal(money.MustQty("48")) ||
		got[0].UnitID != st.thung || got[0].Factor.String() != "24" {
		t.Errorf("2 thùng = %+v", got[0])
	}
	if got[3].Source.ColorID == nil || *got[3].Source.ColorID != st.red {
		t.Errorf("red line source = %+v", got[3].Source)
	}
	if got[4].Source.ColorID != nil {
		t.Errorf("a color without its own row reports color %v; the general price applies", got[4].Source.ColorID)
	}
	if got[5].Source.UnitID == nil || *got[5].Source.UnitID != st.thung {
		t.Errorf("thùng row source = %+v", got[5].Source)
	}

	// "Bỏ giá riêng": the red price goes, the history shows old → nothing for the color.
	changes := h.salePrice(t, st, KindRetail, sku, &st.red, "")
	if len(changes) != 1 || changes[0].OldPrice == nil || changes[0].OldPrice.String() != "11000" || changes[0].NewPrice != nil ||
		changes[0].ColorID == nil || *changes[0].ColorID != st.red || changes[0].ScopeLabel() != "Đỏ" {
		t.Fatalf("removal = %+v", changes)
	}
	red := h.quote(t, st, QuoteInput{Lines: []PriceLine{{SkuID: sku, ColorID: &st.red, Qty: money.MustQty("1")}}})[0]
	if red.Amount.String() != "10000" || red.Source.ColorID != nil {
		t.Fatalf("red after removal = %s %+v", red.Amount, red.Source)
	}
	history, err := h.svc.PriceHistory(ctx, st.scope, sku, HistoryFilter{Limit: 10})
	if err != nil || len(history) == 0 || history[0].NewPrice != nil || history[0].ColorName == nil || *history[0].ColorName != "Đỏ" ||
		history[0].PriceListKind != KindRetail {
		t.Fatalf("history = %+v, %v", history, err)
	}

	// "Áp dụng cho tất cả màu": a general price that also clears every color price.
	h.salePrice(t, st, KindRetail, sku, &st.red, "11500")
	h.salePrice(t, st, KindRetail, sku, &st.white, "10500")
	changes, _, err = h.svc.SetSalePrice(ctx, st.actor, nil, SalePriceCommand{SkuID: sku, Kind: KindRetail, Source: ChangeProductList,
		Price: ptr(money.MustMoney("10200")), ApplyAllColors: true})
	if err != nil || len(changes) != 3 {
		t.Fatalf("apply to all colors = %+v, %v", changes, err)
	}
	for _, color := range []uuid.UUID{st.red, st.white} {
		got := h.quote(t, st, QuoteInput{Lines: []PriceLine{{SkuID: sku, ColorID: &color, Qty: money.MustQty("1")}}})[0]
		if got.Amount.String() != "10200" || got.Source.ColorID != nil {
			t.Errorf("color %s after apply-all = %s %+v", color, got.Amount, got.Source)
		}
	}
	// A unit the SKU does not have is refused.
	_, err = h.svc.Quote(ctx, st.scope, QuoteInput{Lines: []PriceLine{{SkuID: boxed, UnitID: &st.red, Qty: money.MustQty("1")}}})
	wantKind(t, err, apperr.Validation, "")
}

func TestSalePriceNeedsTheBaseList(t *testing.T) {
	h := newHarness(t)
	st := h.store(t)
	sku := h.sku(t, st, "")
	changes := h.salePrice(t, st, KindRetail, sku, nil, "15000")
	if len(changes) != 1 || changes[0].OldPrice != nil || changes[0].NewPrice.String() != "15000" || changes[0].Source != ChangeCatalog {
		t.Fatalf("first price = %+v", changes)
	}
	// Setting the same price again writes no history.
	if again := h.salePrice(t, st, KindRetail, sku, nil, "15000"); len(again) != 0 {
		t.Fatalf("unchanged price wrote %+v", again)
	}
	_, _, err := h.svc.SetSalePrice(context.Background(), st.actor, nil, SalePriceCommand{SkuID: ids.New(), Kind: KindRetail,
		Source: ChangeCatalog, Price: ptr(money.MustMoney("1"))})
	wantKind(t, err, apperr.NotFound, "not_found")
}

func TestRemovingAUnitPriceFallsBackToBaseTimesFactor(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	sku := h.sku(t, st, "")
	h.salePrice(t, st, KindRetail, sku, nil, "10000")
	unitPrice := func(price *money.Money) ([]PriceChange, error) {
		changes, _, err := h.svc.SetSalePrice(ctx, st.actor, nil, SalePriceCommand{SkuID: sku, UnitID: &st.thung, Kind: KindRetail,
			Source: ChangeCatalog, Price: price})
		return changes, err
	}
	quoteCarton := func() ResolvedPrice {
		return h.quote(t, st, QuoteInput{Lines: []PriceLine{{SkuID: sku, UnitID: &st.thung, Qty: money.MustQty("1")}}})[0]
	}
	if _, err := unitPrice(ptr(money.MustMoney("230000"))); err != nil {
		t.Fatal(err)
	}
	if got := quoteCarton(); got.Amount.String() != "230000" || got.Source.UnitID == nil || *got.Source.UnitID != st.thung {
		t.Fatalf("carton with its own price = %s %+v", got.Amount, got.Source)
	}

	changes, err := unitPrice(nil)
	if err != nil || len(changes) != 1 || changes[0].OldPrice == nil || changes[0].OldPrice.String() != "230000" ||
		changes[0].NewPrice != nil || changes[0].UnitID == nil || *changes[0].UnitID != st.thung {
		t.Fatalf("removal = %+v, %v", changes, err)
	}
	if got := quoteCarton(); got.Amount.String() != "240000" || got.Source.UnitID != nil {
		t.Errorf("carton after removal = %s %+v, want 24 × 10000 from the base row", got.Amount, got.Source)
	}
	if again, err := unitPrice(nil); err != nil || len(again) != 0 {
		t.Errorf("removing a missing unit price = %+v, %v, want nothing", again, err)
	}

	// The general base price cannot be removed, and apply-all-colors needs a price.
	_, _, err = h.svc.SetSalePrice(ctx, st.actor, nil, SalePriceCommand{SkuID: sku, UnitID: &st.hop, Kind: KindRetail, Source: ChangeCatalog})
	wantKind(t, err, apperr.Validation, "")
	if fieldsOf(err)["price"] != "required" {
		t.Errorf("removing the base price = %v", err)
	}
	_, _, err = h.svc.SetSalePrice(ctx, st.actor, nil, SalePriceCommand{SkuID: sku, UnitID: &st.thung, Kind: KindRetail,
		Source: ChangeCatalog, ApplyAllColors: true})
	wantKind(t, err, apperr.Validation, "")
	if fieldsOf(err)["apply_all_colors"] != "requires_price" {
		t.Errorf("apply all colors without a price = %v", err)
	}
}

func fieldsOf(err error) map[string]string {
	if e, ok := apperr.As(err); ok {
		return e.Fields
	}
	return nil
}

// baseList returns the id of the store's base list of kind.
func (h harness) baseList(t testing.TB, st *store, kind string) uuid.UUID {
	t.Helper()
	var l PriceList
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		var ok bool
		var err error
		if l, ok, err = baseList(context.Background(), q, kind); err == nil && !ok {
			err = fmt.Errorf("no %s base list", kind)
		}
		return err
	})
	return l.ID
}

func TestBulkPutRecordsEachChangedRow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	a, b := h.sku(t, st, ""), h.sku(t, st, "")
	retail := h.baseList(t, st, KindRetail)
	first, err := h.svc.PutPriceListItems(ctx, st.actor, retail, []ItemInput{
		priceItem(a, "10000"),
		{SkuID: a, ColorID: &st.red, Price: ptr(money.MustMoney("11000"))},
		{SkuID: a, MinQty: ptr(money.MustQty("48")), Price: ptr(money.MustMoney("9000"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same general price, a new red price, the tier spelled with another scale, a new thùng row
	// and a first price of another SKU.
	second, err := h.svc.PutPriceListItems(ctx, st.actor, retail, []ItemInput{
		{SkuID: a, ColorID: &st.red, Price: ptr(money.MustMoney("11500"))},
		priceItem(a, "10000"),
		{SkuID: a, UnitID: &st.thung, Price: ptr(money.MustMoney("230000"))},
		{SkuID: a, MinQty: ptr(money.MustQty("48.00")), Price: ptr(money.MustMoney("9000"))},
		priceItem(b, "5000"),
	})
	if err != nil || len(second) != 5 {
		t.Fatalf("second put = %+v, %v", second, err)
	}
	if second[0].ID != first[1].ID || second[1].ID != first[0].ID || second[3].ID != first[2].ID {
		t.Fatalf("existing rows were not reused: %+v", second)
	}
	for i, want := range []string{"11500", "10000", "230000", "9000", "5000"} {
		if second[i].Price.String() != want {
			t.Errorf("row %d price %s, want %s", i, second[i].Price, want)
		}
	}
	history, err := h.svc.PriceHistory(ctx, st.scope, a, HistoryFilter{Limit: 10})
	if err != nil || len(history) != 5 {
		t.Fatalf("history = %+v, %v", history, err)
	}
	thung, red := history[0], history[1]
	if thung.OldPrice != nil || thung.NewPrice.String() != "230000" || thung.UnitID == nil || *thung.UnitID != st.thung ||
		thung.Source != ChangePriceList {
		t.Errorf("thùng change = %+v", thung)
	}
	if red.OldPrice == nil || red.OldPrice.String() != "11000" || red.NewPrice.String() != "11500" ||
		red.ColorID == nil || *red.ColorID != st.red {
		t.Errorf("red change = %+v", red)
	}
	if history, err := h.svc.PriceHistory(ctx, st.scope, b, HistoryFilter{Limit: 10}); err != nil || len(history) != 1 {
		t.Fatalf("history of the other SKU = %+v, %v", history, err)
	}
}

func TestPriceWritersOfOneListDoNotDeadlock(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	sku := h.sku(t, st, "")
	retail := h.baseList(t, st, KindRetail)
	h.salePrice(t, st, KindRetail, sku, nil, "10000")
	// One writer keeps setting both color prices of the SKU while another keeps clearing them with
	// "Áp dụng cho tất cả màu"; both touch the same rows in their own order.
	const rounds = 20
	errs := make(chan error, 2*rounds)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range rounds {
			_, err := h.svc.PutPriceListItems(ctx, st.actor, retail, []ItemInput{
				{SkuID: sku, ColorID: &st.white, Price: ptr(money.MustMoney(fmt.Sprint(12000 + i)))},
				{SkuID: sku, ColorID: &st.red, Price: ptr(money.MustMoney(fmt.Sprint(11000 + i)))},
			})
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		for i := range rounds {
			_, _, err := h.svc.SetSalePrice(ctx, st.actor, nil, SalePriceCommand{SkuID: sku, Kind: KindRetail, Source: ChangeProductList,
				Price: ptr(money.MustMoney(fmt.Sprint(10000 + i))), ApplyAllColors: true})
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (h harness) promotion(t testing.TB, st *store, in PromotionInput) Promotion {
	t.Helper()
	if in.ValidFrom == nil {
		in.ValidFrom, in.ValidTo = day(2026, time.March, 1), day(2026, time.March, 31)
	}
	p, err := h.svc.CreatePromotion(context.Background(), st.actor, in)
	if err != nil {
		t.Fatalf("create promotion: %v", err)
	}
	return p
}

func TestPromotionCodesAndUsageLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	sku := h.sku(t, st, "")
	h.salePrice(t, st, KindRetail, sku, nil, "50000")
	h.salePrice(t, st, KindWholesale, sku, nil, "45000")
	p := h.promotion(t, st, PromotionInput{Code: Some("sale10"), Name: ptr("Giảm 10%"), Kind: ptr(PromoOrderPercent),
		Value: ptr(money.MustMoney("10")), UsageLimit: Some(1)})
	if p.Code == nil || *p.Code != "SALE10" {
		t.Fatalf("code = %v, want it upper-cased", p.Code)
	}
	check, err := h.svc.ValidateCode(ctx, st.scope, CodeCheckInput{Code: "Sale10"})
	if err != nil || !check.Valid || check.Promotion == nil || check.Promotion.ID != p.ID {
		t.Fatalf("check = %+v, %v", check, err)
	}
	// Support sessions check codes too (a GET), through the read-only path.
	ro := Scope{TenantID: st.actor.TenantID, ReadOnly: true}
	if check, err := h.svc.ValidateCode(ctx, ro, CodeCheckInput{Code: "SALE10"}); err != nil || !check.Valid {
		t.Fatalf("read-only check = %+v, %v", check, err)
	}
	if check, err := h.svc.ValidateCode(ctx, st.scope, CodeCheckInput{Code: "NOPE1"}); err != nil || check.Valid || check.Reason != CodeNotFound || check.Promotion != nil {
		t.Fatalf("unknown code = %+v, %v", check, err)
	}
	if check, err := h.svc.ValidateCode(ctx, st.scope, CodeCheckInput{Code: "SALE10", BusinessDate: day(2026, time.April, 1)}); err != nil || check.Reason != CodeExpired {
		t.Fatalf("expired code = %+v, %v", check, err)
	}

	draft := DocumentDraft{Lines: []DraftLine{{SkuID: sku, Qty: money.MustQty("2")}}, PromotionCodes: []string{"sale10"}}
	doc, err := h.svc.Calculate(ctx, st.scope, draft)
	if err != nil || len(doc.Promotions) != 1 || doc.Promotions[0].ID != p.ID || doc.TotalAmount.String() != "90000" {
		t.Fatalf("calculate with code = %+v, %v", doc, err)
	}
	// Without the code the promotion does not apply: it is not automatic.
	doc, err = h.svc.Calculate(ctx, st.scope, DocumentDraft{Lines: draft.Lines})
	if err != nil || len(doc.Promotions) != 0 || doc.TotalAmount.String() != "100000" {
		t.Fatalf("calculate without code = %+v, %v", doc, err)
	}

	docA, docB := ids.New(), ids.New()
	record := func(doc uuid.UUID) error {
		return h.d.WithTenantTx(ctx, st.actor.TenantID, func(q db.Querier) error {
			return h.svc.RecordUsage(ctx, q, st.actor.TenantID, p.ID, DocInvoice, doc, money.MustMoney("10000"))
		})
	}
	if err := record(docA); err != nil {
		t.Fatal(err)
	}
	if err := record(docA); err != nil {
		t.Fatalf("reposting the same document: %v", err)
	}
	wantKind(t, record(docB), apperr.Conflict, "promotion_usage_limit_reached")
	var n int
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM promotion_usages WHERE promotion_id = $1`, p.ID).Scan(&n)
	})
	if n != 1 {
		t.Fatalf("%d usages recorded, want 1", n)
	}
	if check, err := h.svc.ValidateCode(ctx, st.scope, CodeCheckInput{Code: "SALE10"}); err != nil || check.Valid || check.Reason != SkipUsageLimitReached {
		t.Fatalf("used-up code = %+v, %v", check, err)
	}
	doc, err = h.svc.Calculate(ctx, st.scope, draft)
	if err != nil || len(doc.Promotions) != 0 || len(doc.SkippedPromotions) != 1 || doc.SkippedPromotions[0].Reason != SkipUsageLimitReached {
		t.Fatalf("calculate with a used-up code = %+v, %v", doc, err)
	}
	_, err = h.svc.Calculate(ctx, st.scope, DocumentDraft{Lines: draft.Lines, PromotionCodes: []string{"NOPE1"}})
	wantKind(t, err, apperr.Validation, "")

	// Deactivated, the code answers inactive; an automatic promotion of another group is skipped.
	if _, err := h.svc.SetPromotionStatus(ctx, st.actor, p.ID, PromoInactive); err != nil {
		t.Fatal(err)
	}
	if check, _ := h.svc.ValidateCode(ctx, st.scope, CodeCheckInput{Code: "SALE10"}); check.Reason != CodeInactive {
		t.Fatalf("inactive code = %+v", check)
	}
	vip := h.promotion(t, st, PromotionInput{Name: ptr("VIP"), Kind: ptr(PromoLineAmount), Value: ptr(money.MustMoney("1000")),
		CustomerGroupID: Some(st.vip)})
	doc, err = h.svc.Calculate(ctx, st.scope, DocumentDraft{Lines: draft.Lines})
	if err != nil || len(doc.SkippedPromotions) != 1 || doc.SkippedPromotions[0].ID != vip.ID || doc.SkippedPromotions[0].Reason != SkipCustomerGroup {
		t.Fatalf("other group's promotion = %+v, %v", doc.SkippedPromotions, err)
	}
	doc, err = h.svc.Calculate(ctx, st.scope, DocumentDraft{PartyID: &st.customer, Lines: draft.Lines})
	if err != nil || len(doc.Promotions) != 1 || doc.Promotions[0].ID != vip.ID || doc.Promotions[0].Amount.String() != "2000" {
		t.Fatalf("VIP customer = %+v skipped %+v, %v", doc.Promotions, doc.SkippedPromotions, err)
	}
	// A purchase document never takes promotions.
	doc, err = h.svc.Calculate(ctx, st.scope, DocumentDraft{PartyID: &st.customer, Lines: draft.Lines, SkipPromotions: true})
	if err != nil || len(doc.Promotions) != 0 || len(doc.SkippedPromotions) != 0 {
		t.Fatalf("purchase = %+v, %v", doc, err)
	}
}

func TestMinPriceNeedsTheOwnersConfirmation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	sku := h.sku(t, st, "9500")
	h.salePrice(t, st, KindRetail, sku, nil, "10000")
	draft := DocumentDraft{Lines: []DraftLine{{SkuID: sku, Qty: money.MustQty("2"), UnitPrice: ptr(money.MustMoney("9000"))}}}
	hash, err := exception.PayloadHash([]byte(`{"lines":[{"unit_price":"9000"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	docID := ids.New()
	enforce := func(conf MinPriceConfirmation) error {
		return h.d.WithTenantTx(ctx, st.actor.TenantID, func(q db.Querier) error {
			doc, err := h.svc.CalculateIn(ctx, q, st.actor.TenantID, draft)
			if err != nil {
				return err
			}
			if !doc.Lines[0].BelowMinPrice || doc.Lines[0].PriceSource.Kind != SourceManual {
				return fmt.Errorf("line = %+v", doc.Lines[0])
			}
			return h.svc.EnforceMinPrice(ctx, q, st.actor.TenantID, doc, conf)
		})
	}
	err = enforce(MinPriceConfirmation{})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Kind != apperr.ExceptionRequired || ae.ExceptionKind != string(exception.BelowMinPrice) ||
		ae.Fields["lines[0].unit_price"] != "below_min_price" {
		t.Fatalf("without confirmation: %v", err)
	}

	var conf exception.Confirmation
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		conf, err = exception.NewGuard(h.clk).Create(ctx, q, exception.NewConfirmation{
			TenantID: st.actor.TenantID, Kind: exception.BelowMinPrice, DocType: DocInvoice, DocID: docID, DocVersion: 1,
			PayloadHash: hash, ConfirmedBy: st.actor.ID,
		})
		return err
	})
	mine := MinPriceConfirmation{ConfirmationID: conf.ID, DocType: DocInvoice, DocID: docID, DocVersion: 1, PayloadHash: hash,
		UsedByDocType: DocInvoice, UsedByDocID: docID}
	stale := mine
	stale.DocVersion = 2
	wantKind(t, enforce(stale), apperr.ExceptionRequired, "below_min_price")
	if err := enforce(mine); err != nil {
		t.Fatalf("with the confirmation: %v", err)
	}
	var used int
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM exception_confirmation_uses WHERE confirmation_id = $1`, conf.ID).Scan(&used)
	})
	if used != 1 {
		t.Fatalf("confirmation used %d times", used)
	}
	wantKind(t, enforce(mine), apperr.Conflict, "exception_confirmation_used")

	// At or above the minimum there is nothing to confirm.
	draft.Lines[0].UnitPrice = ptr(money.MustMoney("9500"))
	if err := h.d.WithTenantTx(ctx, st.actor.TenantID, func(q db.Querier) error {
		doc, err := h.svc.CalculateIn(ctx, q, st.actor.TenantID, draft)
		if err != nil {
			return err
		}
		if doc.Lines[0].BelowMinPrice {
			return fmt.Errorf("a price at the minimum is flagged: %+v", doc.Lines[0])
		}
		return h.svc.EnforceMinPrice(ctx, q, st.actor.TenantID, doc, MinPriceConfirmation{})
	}); err != nil {
		t.Fatalf("at the minimum: %v", err)
	}
}

func TestPricingTenantIsolation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, b := h.store(t), h.store(t)
	sku := h.sku(t, a, "")
	h.salePrice(t, a, KindRetail, sku, nil, "10000")
	list := h.list(t, a, PriceListInput{Name: ptr("Riêng A"), Kind: ptr(KindSeason), ValidFrom: march.ValidFrom, ValidTo: march.ValidTo}, priceItem(sku, "9000"))
	p := h.promotion(t, a, PromotionInput{Code: Some("ONLYA"), Name: ptr("A"), Kind: ptr(PromoOrderAmount), Value: ptr(money.MustMoney("1000"))})

	_, err := h.svc.PriceList(ctx, b.scope, list.ID)
	wantKind(t, err, apperr.NotFound, "not_found")
	_, err = h.svc.PriceListItems(ctx, b.scope, list.ID, ItemFilter{})
	wantKind(t, err, apperr.NotFound, "not_found")
	_, err = h.svc.PutPriceListItems(ctx, b.actor, list.ID, []ItemInput{priceItem(sku, "1")})
	wantKind(t, err, apperr.NotFound, "not_found")
	wantKind(t, h.svc.DeletePriceList(ctx, b.actor, list.ID), apperr.NotFound, "not_found")
	_, err = h.svc.Promotion(ctx, b.scope, p.ID)
	wantKind(t, err, apperr.NotFound, "not_found")
	_, err = h.svc.SetPromotionStatus(ctx, b.actor, p.ID, PromoInactive)
	wantKind(t, err, apperr.NotFound, "not_found")
	_, err = h.svc.PriceHistory(ctx, b.scope, sku, HistoryFilter{})
	wantKind(t, err, apperr.NotFound, "not_found")
	_, _, err = h.svc.SetSalePrice(ctx, b.actor, nil, SalePriceCommand{SkuID: sku, Kind: KindRetail, Source: ChangeCatalog, Price: ptr(money.MustMoney("1"))})
	wantKind(t, err, apperr.NotFound, "not_found")
	_, err = h.svc.Quote(ctx, b.scope, QuoteInput{Lines: []PriceLine{{SkuID: sku, Qty: money.MustQty("1")}}})
	wantKind(t, err, apperr.Validation, "")
	// A's customer and group are unknown to B.
	_, err = h.svc.Quote(ctx, b.scope, QuoteInput{PartyID: &a.customer, Lines: []PriceLine{{SkuID: sku, Qty: money.MustQty("1")}}})
	wantKind(t, err, apperr.Validation, "")
	_, err = h.svc.CreatePriceList(ctx, b.actor, PriceListInput{Name: ptr("Mượn nhóm"), Kind: ptr(KindGroup), CustomerGroupID: &a.vip})
	wantKind(t, err, apperr.Validation, "")
	if check, err := h.svc.ValidateCode(ctx, b.scope, CodeCheckInput{Code: "ONLYA"}); err != nil || check.Reason != CodeNotFound {
		t.Fatalf("B checks A's code = %+v, %v", check, err)
	}
	err = h.d.WithTenantTx(ctx, b.actor.TenantID, func(q db.Querier) error {
		return h.svc.RecordUsage(ctx, q, b.actor.TenantID, p.ID, DocInvoice, ids.New(), money.ZeroMoney)
	})
	wantKind(t, err, apperr.NotFound, "not_found")
	for _, check := range []func() (int, error){
		func() (int, error) { l, err := h.svc.PriceLists(ctx, b.scope, PriceListFilter{}); return len(l), err },
		func() (int, error) { l, err := h.svc.Promotions(ctx, b.scope, PromotionFilter{}); return len(l), err },
	} {
		n, err := check()
		if err != nil {
			t.Fatal(err)
		}
		if n > 2 { // B sees its own two base lists only.
			t.Errorf("B lists %d records", n)
		}
	}
	lists, _ := h.svc.PriceLists(ctx, b.scope, PriceListFilter{})
	if slices.ContainsFunc(lists, func(l PriceList) bool { return l.ID == list.ID }) {
		t.Error("B lists A's price list")
	}
	var hints map[uuid.UUID]money.Money
	testkit.InTenant(t, h.d, b.actor.TenantID, func(q db.Querier) error {
		hints, err = h.svc.PriceHints(ctx, q, []uuid.UUID{sku})
		return err
	})
	if len(hints) != 0 {
		t.Errorf("B gets A's price hints %v", hints)
	}
}

func TestSeederCreatesTheBaseListsOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	seeder := NewSeeder()
	sc := SeedContext{TenantID: st.actor.TenantID, Now: testkit.Epoch}
	for range 2 {
		testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error { return seeder.SeedPriceLists(ctx, q, sc) })
	}
	lists, err := h.svc.PriceLists(ctx, st.scope, PriceListFilter{})
	if err != nil || len(lists) != 2 || lists[0].Kind != KindRetail || lists[1].Kind != KindWholesale ||
		lists[0].Name != "Giá bán lẻ" || lists[1].Name != "Giá bán sỉ" {
		t.Fatalf("seeded lists = %+v, %v", lists, err)
	}
}

func TestPriceHintsUseTheRetailGeneralPrice(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st := h.store(t)
	retail, colorOnly, wholesaleOnly := h.sku(t, st, ""), h.sku(t, st, ""), h.sku(t, st, "")
	h.salePrice(t, st, KindRetail, retail, nil, "12000")
	h.salePrice(t, st, KindRetail, retail, &st.red, "13000")
	h.salePrice(t, st, KindRetail, colorOnly, &st.red, "8000")
	h.salePrice(t, st, KindWholesale, wholesaleOnly, nil, "7000")
	// A season list does not change the hint.
	h.list(t, st, PriceListInput{Name: ptr("Mùa"), Kind: ptr(KindSeason), ValidFrom: march.ValidFrom, ValidTo: march.ValidTo}, priceItem(retail, "1000"))
	var hints map[uuid.UUID]money.Money
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		var err error
		hints, err = h.svc.PriceHints(ctx, q, []uuid.UUID{retail, colorOnly, wholesaleOnly, retail, ids.New()})
		return err
	})
	if len(hints) != 1 || hints[retail].String() != "12000" {
		t.Fatalf("hints = %v", hints)
	}
	// A read-only support transaction gets the hints too.
	if err := h.d.WithReadOnlyTenantTx(ctx, st.actor.TenantID, func(q db.Querier) error {
		var err error
		hints, err = h.svc.PriceHints(ctx, q, []uuid.UUID{retail})
		return err
	}); err != nil || hints[retail].String() != "12000" {
		t.Fatalf("read-only hints = %v, %v", hints, err)
	}
	// Calculations take the read-only path for a read-only scope.
	ro := Scope{TenantID: st.actor.TenantID, ReadOnly: true}
	doc, err := h.svc.Calculate(ctx, ro, DocumentDraft{Lines: []DraftLine{{SkuID: retail, Qty: money.MustQty("1")}}})
	if err != nil || doc.TotalAmount.String() != "1000" {
		t.Fatalf("read-only calculate = %+v, %v", doc, err)
	}
}

// fiftyLines is a POS-sized document over 50 SKUs, each with a general, a color, a unit and a
// tier row and a customer-group price for every other SKU.
func fiftyLines(t testing.TB, h harness) (*store, PriceContext, []PriceLine) {
	st := h.store(t)
	group := h.list(t, st, PriceListInput{Name: ptr("Giá VIP"), Kind: ptr(KindGroup), CustomerGroupID: &st.vip})
	var lines []PriceLine
	var base, vip []ItemInput
	for i := range 50 {
		sku := h.sku(t, st, "")
		base = append(base, priceItem(sku, "10000"), ItemInput{SkuID: sku, ColorID: &st.red, Price: ptr(money.MustMoney("11000"))},
			ItemInput{SkuID: sku, UnitID: &st.thung, Price: ptr(money.MustMoney("230000"))},
			ItemInput{SkuID: sku, MinQty: ptr(money.MustQty("48")), Price: ptr(money.MustMoney("9000"))})
		if i%2 == 0 {
			vip = append(vip, priceItem(sku, "9500"))
		}
		line := PriceLine{SkuID: sku, Qty: money.MustQty("3")}
		switch i % 3 {
		case 1:
			line.ColorID = &st.red
		case 2:
			line.UnitID = &st.thung
		}
		lines = append(lines, line)
	}
	var wholesale PriceList
	testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
		var err error
		wholesale, _, err = baseList(context.Background(), q, KindWholesale)
		return err
	})
	for _, put := range []struct {
		id    uuid.UUID
		items []ItemInput
	}{{wholesale.ID, base}, {group.ID, vip}} {
		if _, err := h.svc.PutPriceListItems(context.Background(), st.actor, put.id, put.items); err != nil {
			t.Fatal(err)
		}
	}
	return st, PriceContext{PartyID: &st.customer, BusinessDate: *day(2026, time.March, 15)}, lines
}

// ResolveMany prices a 50-line document in one query per step; the median stays under 20 ms.
func TestResolveManyFiftyLinesIsFast(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	st, pc, lines := fiftyLines(t, h)
	var took []time.Duration
	for range 21 {
		testkit.InTenant(t, h.d, st.actor.TenantID, func(q db.Querier) error {
			start := time.Now()
			out, err := h.svc.ResolveMany(ctx, q, st.actor.TenantID, pc, lines)
			took = append(took, time.Since(start))
			if err != nil {
				return err
			}
			if len(out) != 50 || out[0].Source.Kind != SourceGroupList || out[1].Amount.String() != "11000" || out[2].Amount.String() != "228000" {
				return fmt.Errorf("prices = %+v", out[:3])
			}
			return nil
		})
	}
	slices.Sort(took)
	if median := took[len(took)/2]; !raceEnabled && median > 20*time.Millisecond {
		t.Errorf("ResolveMany of 50 lines: median %s, want < 20ms (all %v)", median, took)
	}
}

func BenchmarkResolveManyFiftyLines(b *testing.B) {
	h := newHarness(b)
	ctx := context.Background()
	st, pc, lines := fiftyLines(b, h)
	b.ResetTimer()
	for b.Loop() {
		if err := h.d.WithTenantTx(ctx, st.actor.TenantID, func(q db.Querier) error {
			_, err := h.svc.ResolveMany(ctx, q, st.actor.TenantID, pc, lines)
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
}
