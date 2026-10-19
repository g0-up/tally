package money

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/shopspring/decimal"
)

func TestQty(t *testing.T) {
	q := MustQty("2.5")
	if got := q.Add(MustQty("0.5")).String(); got != "3" {
		t.Errorf("Add = %s", got)
	}
	if got := q.Sub(MustQty("3")).String(); got != "-0.5" {
		t.Errorf("Sub = %s", got)
	}
	if !q.Neg().IsNegative() || !q.IsPositive() || q.IsZero() || !ZeroQty.IsZero() {
		t.Error("sign predicates wrong")
	}
	if q.Cmp(NewQtyFromInt(2)) != 1 || !NewQtyFromInt(3).Equal(MustQty("3.000")) {
		t.Error("Cmp/Equal wrong")
	}
	if q.IsWhole() || !NewQtyFromInt(4).IsWhole() {
		t.Error("IsWhole wrong")
	}
	if !q.Decimal().Equal(decimal.RequireFromString("2.5")) {
		t.Error("Decimal mismatch")
	}
	for in, wantErr := range map[string]error{"0.00001": ErrScale, "100000000000000": ErrRange, "1e2": ErrFormat} {
		if _, err := NewQty(in); !errors.Is(err, wantErr) {
			t.Errorf("NewQty(%q) error = %v, want %v", in, err, wantErr)
		}
	}
	assertPanics(t, func() { MustQty("x") })
}

func TestFactor(t *testing.T) {
	f := MustFactor("24")
	if !f.Equal(MustFactor("24.000000")) || f.String() != "24" || !FactorOne.Equal(MustFactor("1")) {
		t.Error("factor equality wrong")
	}
	if !f.Decimal().Equal(decimal.NewFromInt(24)) {
		t.Error("Decimal mismatch")
	}
	for in, wantErr := range map[string]error{
		"0": ErrNotPositive, "-1": ErrNotPositive, "0.0000001": ErrScale, "1000000000000": ErrRange, "x": ErrFormat,
	} {
		if _, err := NewFactor(in); !errors.Is(err, wantErr) {
			t.Errorf("NewFactor(%q) error = %v, want %v", in, err, wantErr)
		}
	}
	assertPanics(t, func() { MustFactor("0") })
	if _, err := (Factor{}).Value(); !errors.Is(err, ErrNotPositive) {
		t.Errorf("zero Factor Value error = %v", err)
	}
	var s Factor
	if err := s.Scan("0"); !errors.Is(err, ErrNotPositive) {
		t.Errorf("Scan(0) error = %v", err)
	}
	if err := s.Scan("x"); err == nil {
		t.Error("Scan(x) accepted")
	}
	if err := s.Scan("0.5"); err != nil || s.String() != "0.5" {
		t.Errorf("Scan = %s, %v", s, err)
	}
	if v, err := s.Value(); err != nil || v != "0.5" {
		t.Errorf("Value = %v, %v", v, err)
	}
}

func TestBaseQty(t *testing.T) {
	cases := []struct {
		qty, factor, want string
		wantErr           error
	}{
		{"2", "24", "48", nil},
		{"1.5", "12", "18", nil},
		{"0.25", "0.5", "0.125", nil},
		{"0.0001", "0.5", "", ErrScale},
		{"1", "0.333333", "", ErrScale},
		{"3", "0.333333", "", ErrScale},
		{"99999999999", "10000", "", ErrRange},
		{"-2", "6", "-12", nil},
	}
	for _, tc := range cases {
		got, err := BaseQty(MustQty(tc.qty), MustFactor(tc.factor))
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("BaseQty(%s, %s) error = %v, want %v", tc.qty, tc.factor, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got.String() != tc.want {
			t.Errorf("BaseQty(%s, %s) = %s, %v; want %s", tc.qty, tc.factor, got, err, tc.want)
		}
	}
}

// Property: BaseQty either returns the exact product (which then fits 4 decimals) or reports
// ErrScale/ErrRange exactly when the product does not fit.
func TestPropertyBaseQty(t *testing.T) {
	prop := func(rawQty int32, rawFactor uint32) bool {
		q := Qty{d: scaledDecimal(int64(rawQty), QtyScale)}
		f := Factor{d: scaledDecimal(int64(rawFactor%1_000_000_000)+1, FactorScale)}
		exact := q.d.Mul(f.d)
		got, err := BaseQty(q, f)
		fits := exact.Equal(exact.Truncate(QtyScale)) && exact.Abs().LessThan(qtyLimits.maxAbs)
		if !fits {
			return errors.Is(err, ErrScale) || errors.Is(err, ErrRange)
		}
		return err == nil && got.d.Equal(exact)
	}
	if err := quick.Check(prop, quickConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestPercent(t *testing.T) {
	p := MustPercent("12.5")
	if p.String() != "12.5" || p.Cmp(MustPercent("10")) != 1 || !p.Decimal().Equal(decimal.RequireFromString("12.5")) {
		t.Error("percent basics wrong")
	}
	for in, wantErr := range map[string]error{"1000": ErrRange, "0.00001": ErrScale, "%5": ErrFormat} {
		if _, err := NewPercent(in); !errors.Is(err, wantErr) {
			t.Errorf("NewPercent(%q) error = %v, want %v", in, err, wantErr)
		}
	}
	assertPanics(t, func() { MustPercent("") })
}

func TestValueObjectsJSONAndSQLRoundTrip(t *testing.T) {
	type payload struct {
		Qty     Qty     `json:"qty"`
		Factor  Factor  `json:"factor"`
		Percent Percent `json:"percent"`
	}
	in := payload{Qty: MustQty("1.25"), Factor: MustFactor("0.5"), Percent: MustPercent("7.5")}
	b, err := json.Marshal(in)
	if err != nil || string(b) != `{"qty":"1.25","factor":"0.5","percent":"7.5"}` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}
	var out payload
	if err := json.Unmarshal(b, &out); err != nil || !out.Qty.Equal(in.Qty) || !out.Factor.Equal(in.Factor) || out.Percent.Cmp(in.Percent) != 0 {
		t.Fatalf("Unmarshal = %+v, %v", out, err)
	}
	if err := json.Unmarshal([]byte(`{"qty":null,"factor":null,"percent":null}`), &out); err != nil {
		t.Fatalf("null rejected: %v", err)
	}
	bad := []struct {
		body  string
		field string
		typ   reflect.Type
	}{
		{`{"qty":1}`, "qty", reflect.TypeFor[Qty]()},
		{`{"factor":"0"}`, "factor", reflect.TypeFor[Factor]()},
		{`{"factor":2}`, "factor", reflect.TypeFor[Factor]()},
		{`{"percent":"x"}`, "percent", reflect.TypeFor[Percent]()},
	}
	for _, tc := range bad {
		var typeErr *json.UnmarshalTypeError
		if err := json.Unmarshal([]byte(tc.body), &out); !errors.As(err, &typeErr) || typeErr.Field != tc.field || typeErr.Type != tc.typ {
			t.Errorf("Unmarshal(%s) error = %v", tc.body, err)
		}
	}

	var q Qty
	var p Percent
	if err := q.Scan("3.5"); err != nil || q.String() != "3.5" {
		t.Errorf("Qty Scan = %s, %v", q, err)
	}
	if err := q.Scan(nil); err == nil {
		t.Error("Qty Scan(nil) accepted")
	}
	if err := p.Scan([]byte("5")); err != nil || p.String() != "5" {
		t.Errorf("Percent Scan = %s, %v", p, err)
	}
	if err := p.Scan("1000"); err == nil {
		t.Error("Percent Scan out of range accepted")
	}
	if v, err := q.Value(); err != nil || v != "3.5" {
		t.Errorf("Qty Value = %v, %v", v, err)
	}
	if v, err := p.Value(); err != nil || v != "5" {
		t.Errorf("Percent Value = %v, %v", v, err)
	}
}
