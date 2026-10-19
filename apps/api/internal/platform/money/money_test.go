package money

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/shopspring/decimal"
)

func TestNewMoneyParsing(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr error
	}{
		{"0", "0", nil},
		{"15000", "15000", nil},
		{"-12.5", "-12.5", nil},
		{"0.0001", "0.0001", nil},
		{"1.50000", "1.5", nil},
		{"999999999999999.9999", "999999999999999.9999", nil},
		{"-999999999999999.9999", "-999999999999999.9999", nil},
		{"0.00001", "", ErrScale},
		{"1.23456", "", ErrScale},
		{"1000000000000000", "", ErrRange},
		{"", "", ErrFormat},
		{"abc", "", ErrFormat},
		{"1e5", "", ErrFormat},
		{"+1", "", ErrFormat},
		{".5", "", ErrFormat},
		{"1.", "", ErrFormat},
		{" 1", "", ErrFormat},
		{"1,000", "", ErrFormat},
		{"NaN", "", ErrFormat},
		{"--1", "", ErrFormat},
		{strings.Repeat("1", 65), "", ErrFormat},
	}
	for _, tc := range cases {
		got, err := NewMoney(tc.in)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("NewMoney(%q) error = %v, want %v", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("NewMoney(%q) error = %v", tc.in, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("NewMoney(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestMoneyArithmetic(t *testing.T) {
	a, b := MustMoney("100.25"), MustMoney("0.75")
	if got := a.Add(b).String(); got != "101" {
		t.Errorf("Add = %s", got)
	}
	if got := b.Sub(a).String(); got != "-99.5" {
		t.Errorf("Sub = %s", got)
	}
	if got := a.Neg().Abs(); !got.Equal(a) {
		t.Errorf("Neg/Abs = %s", got)
	}
	if got := SumMoney(a, b, NewMoneyFromInt(-1)).String(); got != "100" {
		t.Errorf("SumMoney = %s", got)
	}
	if !SumMoney().IsZero() || !ZeroMoney.IsZero() {
		t.Error("empty sum or zero value not zero")
	}
	if a.Cmp(b) != 1 || b.Cmp(a) != -1 || a.Cmp(MustMoney(a.String())) != 0 {
		t.Error("Cmp ordering wrong")
	}
	if !a.IsPositive() || a.IsNegative() || !a.Neg().IsNegative() {
		t.Error("sign predicates wrong")
	}
	if !a.Decimal().Equal(decimal.RequireFromString("100.25")) {
		t.Error("Decimal mismatch")
	}
}

func TestMoneyMultiplicationRoundsHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		name string
		got  Money
		want string
	}{
		{"qty exact", MustMoney("15000").MulQty(MustQty("2.5")), "37500"},
		{"qty half up", MustMoney("0.0001").MulQty(MustQty("0.5")), "0.0001"},
		{"qty half negative", MustMoney("-0.0001").MulQty(MustQty("0.5")), "-0.0001"},
		{"qty below half", MustMoney("0.0001").MulQty(MustQty("0.4")), "0"},
		{"factor", MustMoney("1234.5678").MulFactor(MustFactor("0.333333")), "411.5222"},
		{"percent", MustMoney("199999").MulPercent(MustPercent("10")), "19999.9"},
		{"percent fraction", MustMoney("1").MulPercent(MustPercent("0.005")), "0.0001"},
		{"percent negative", MustMoney("-1").MulPercent(MustPercent("0.005")), "-0.0001"},
	}
	for _, tc := range cases {
		if tc.got.String() != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

func TestRoundVND(t *testing.T) {
	cases := map[string]string{
		"0":          "0",
		"12345.4999": "12345",
		"12345.5":    "12346",
		"-12345.5":   "-12346",
		"-0.4999":    "0",
		"0.5":        "1",
		"99.9999":    "100",
	}
	for in, want := range cases {
		if got := MustMoney(in).RoundVND().String(); got != want {
			t.Errorf("RoundVND(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestMoneyJSON(t *testing.T) {
	type line struct {
		Price Money  `json:"price"`
		Disc  *Money `json:"disc,omitempty"`
	}
	b, err := json.Marshal(line{Price: MustMoney("15000.50")})
	if err != nil || string(b) != `{"price":"15000.5"}` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}
	var l line
	if err := json.Unmarshal([]byte(`{"price":"12.3456","disc":null}`), &l); err != nil || l.Price.String() != "12.3456" || l.Disc != nil {
		t.Fatalf("Unmarshal = %+v, %v", l, err)
	}
	for _, bad := range []string{`{"price":12}`, `{"price":"1.23456"}`, `{"price":"x"}`, `{"price":true}`, `{"price":{}}`, `{"price":[]}`} {
		err := json.Unmarshal([]byte(bad), &l)
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			t.Errorf("Unmarshal(%s) error = %v, want *json.UnmarshalTypeError", bad, err)
			continue
		}
		if typeErr.Field != "price" || typeErr.Type != reflect.TypeFor[Money]() {
			t.Errorf("Unmarshal(%s) field=%q type=%v", bad, typeErr.Field, typeErr.Type)
		}
	}
	var m Money
	if err := m.UnmarshalJSON(nil); err == nil {
		t.Error("empty input accepted")
	}
}

func TestMoneySQL(t *testing.T) {
	var m Money
	for _, src := range []any{"12.5", []byte("12.5")} {
		if err := m.Scan(src); err != nil || m.String() != "12.5" {
			t.Errorf("Scan(%v) = %s, %v", src, m, err)
		}
	}
	if err := m.Scan(int64(7)); err != nil || m.String() != "7" {
		t.Errorf("Scan(int64) = %s, %v", m, err)
	}
	for _, bad := range []any{nil, 1.5, "1.23456", true} {
		if err := m.Scan(bad); err == nil {
			t.Errorf("Scan(%v) accepted", bad)
		}
	}
	v, err := MustMoney("-3.25").Value()
	if err != nil || v != "-3.25" {
		t.Errorf("Value = %v, %v", v, err)
	}
	huge := MustMoney("999999999999999").Add(MustMoney("999999999999999"))
	if _, err := huge.Value(); !errors.Is(err, ErrRange) {
		t.Errorf("Value of out-of-range sum error = %v", err)
	}
}

func TestMustMoneyPanics(t *testing.T) {
	assertPanics(t, func() { MustMoney("1.0e3") })
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	fn()
}

// scaledDecimal builds value/10^scale so generated inputs respect a value object's scale.
func scaledDecimal(v int64, scale int32) decimal.Decimal { return decimal.New(v, -scale) }

func quickConfig() *quick.Config {
	return &quick.Config{MaxCount: 5000, Rand: rand.New(rand.NewSource(20261016))}
}

// Property: MulQty keeps at most 4 decimals and is within half a unit of the 4th decimal of the
// exact product; rounding is symmetric around zero.
func TestPropertyMulQtyRounding(t *testing.T) {
	half := decimal.New(5, -5)
	prop := func(rawPrice int64, rawQty int32) bool {
		price := Money{d: scaledDecimal(rawPrice%1_000_000_000_000, MoneyScale)}
		qty := Qty{d: scaledDecimal(int64(rawQty), QtyScale)}
		got := price.MulQty(qty)
		exact := price.d.Mul(qty.d)
		if !got.d.Equal(got.d.Truncate(MoneyScale)) || got.d.Sub(exact).Abs().GreaterThan(half) {
			return false
		}
		return price.Neg().MulQty(qty).Equal(got.Neg())
	}
	if err := quick.Check(prop, quickConfig()); err != nil {
		t.Fatal(err)
	}
}

// Property: RoundVND yields a whole number within 0.5 of the input and is symmetric around zero.
func TestPropertyRoundVND(t *testing.T) {
	half := decimal.New(5, -1)
	prop := func(raw int64) bool {
		m := Money{d: scaledDecimal(raw%10_000_000_000_000_000, MoneyScale)}
		r := m.RoundVND()
		if !r.d.Equal(r.d.Truncate(0)) || r.d.Sub(m.d).Abs().GreaterThan(half) {
			return false
		}
		return m.Neg().RoundVND().Equal(r.Neg())
	}
	if err := quick.Check(prop, quickConfig()); err != nil {
		t.Fatal(err)
	}
}
