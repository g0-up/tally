package money

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/shopspring/decimal"
)

// Qty is a quantity with at most 4 decimals. Sign rules (> 0 for goods lines, signed for moves)
// belong to the caller.
type Qty struct{ d decimal.Decimal }

// ZeroQty is 0.
var ZeroQty = Qty{}

// NewQty parses a plain decimal string.
func NewQty(s string) (Qty, error) {
	d, err := qtyLimits.parse(s)
	if err != nil {
		return Qty{}, err
	}
	return Qty{d: d}, nil
}

// MustQty is NewQty for constants and tests; it panics on invalid input.
func MustQty(s string) Qty {
	q, err := NewQty(s)
	if err != nil {
		panic(err)
	}
	return q
}

// NewQtyFromInt returns a whole quantity.
func NewQtyFromInt(v int64) Qty { return Qty{d: decimal.NewFromInt(v)} }

// Decimal exposes the underlying value.
func (q Qty) Decimal() decimal.Decimal { return q.d }

// Add returns q + o.
func (q Qty) Add(o Qty) Qty { return Qty{d: q.d.Add(o.d)} }

// Sub returns q - o.
func (q Qty) Sub(o Qty) Qty { return Qty{d: q.d.Sub(o.d)} }

// Neg returns -q.
func (q Qty) Neg() Qty { return Qty{d: q.d.Neg()} }

// Cmp returns -1, 0 or +1 as q is less than, equal to or greater than o.
func (q Qty) Cmp(o Qty) int { return q.d.Cmp(o.d) }

// Equal reports whether q and o have the same value.
func (q Qty) Equal(o Qty) bool { return q.d.Equal(o.d) }

// IsZero reports q == 0.
func (q Qty) IsZero() bool { return q.d.IsZero() }

// IsPositive reports q > 0.
func (q Qty) IsPositive() bool { return q.d.IsPositive() }

// IsNegative reports q < 0.
func (q Qty) IsNegative() bool { return q.d.IsNegative() }

// IsWhole reports whether q has no fractional part (items sold only in whole units).
func (q Qty) IsWhole() bool { return q.d.Equal(q.d.Truncate(0)) }

// String returns the canonical decimal string.
func (q Qty) String() string { return q.d.String() }

// MarshalJSON writes q as a JSON string.
func (q Qty) MarshalJSON() ([]byte, error) { return quote(q.d), nil }

// UnmarshalJSON accepts only a JSON string with a plain decimal.
func (q *Qty) UnmarshalJSON(data []byte) error {
	d, ok, err := qtyLimits.unmarshalJSON(data, q)
	if err != nil || !ok {
		return err
	}
	q.d = d
	return nil
}

// Value implements driver.Valuer.
func (q Qty) Value() (driver.Value, error) { return qtyLimits.value(q.d) }

// Scan implements sql.Scanner.
func (q *Qty) Scan(src any) error {
	d, err := qtyLimits.scan(src)
	if err != nil {
		return err
	}
	q.d = d
	return nil
}

// Factor converts a unit into the base unit (1 thùng = 24 gói → 24). Always > 0, at most 6 decimals.
type Factor struct{ d decimal.Decimal }

// FactorOne is the factor of the base unit itself.
var FactorOne = Factor{d: decimal.NewFromInt(1)}

// NewFactor parses a positive plain decimal string.
func NewFactor(s string) (Factor, error) {
	d, err := factorLimits.parse(s)
	if err != nil {
		return Factor{}, err
	}
	if !d.IsPositive() {
		return Factor{}, fmt.Errorf("factor %q: %w", s, ErrNotPositive)
	}
	return Factor{d: d}, nil
}

// MustFactor is NewFactor for constants and tests; it panics on invalid input.
func MustFactor(s string) Factor {
	f, err := NewFactor(s)
	if err != nil {
		panic(err)
	}
	return f
}

// Decimal exposes the underlying value.
func (f Factor) Decimal() decimal.Decimal { return f.d }

// Equal reports whether f and o have the same value.
func (f Factor) Equal(o Factor) bool { return f.d.Equal(o.d) }

// String returns the canonical decimal string.
func (f Factor) String() string { return f.d.String() }

// MarshalJSON writes f as a JSON string.
func (f Factor) MarshalJSON() ([]byte, error) { return quote(f.d), nil }

// UnmarshalJSON accepts only a JSON string with a positive plain decimal.
func (f *Factor) UnmarshalJSON(data []byte) error {
	d, ok, err := factorLimits.unmarshalJSON(data, f)
	if err != nil || !ok {
		return err
	}
	if !d.IsPositive() {
		return &json.UnmarshalTypeError{Value: "string " + d.String() + " (" + ErrNotPositive.Error() + ")", Type: reflect.TypeFor[Factor]()}
	}
	f.d = d
	return nil
}

// Value implements driver.Valuer.
func (f Factor) Value() (driver.Value, error) {
	if !f.d.IsPositive() {
		return nil, fmt.Errorf("factor %s: %w", f.d.String(), ErrNotPositive)
	}
	return factorLimits.value(f.d)
}

// Scan implements sql.Scanner.
func (f *Factor) Scan(src any) error {
	d, err := factorLimits.scan(src)
	if err != nil {
		return err
	}
	if !d.IsPositive() {
		return fmt.Errorf("factor %s: %w", d.String(), ErrNotPositive)
	}
	f.d = d
	return nil
}

// BaseQty converts q in a unit with factor f into base units. The product must fit 4 decimals;
// otherwise the line cannot be stored exactly and is rejected rather than rounded.
func BaseQty(q Qty, f Factor) (Qty, error) {
	d, err := qtyLimits.check(q.d.Mul(f.d))
	if err != nil {
		return Qty{}, fmt.Errorf("base quantity of %s × %s: %w", q.d.String(), f.d.String(), err)
	}
	return Qty{d: d}, nil
}

// Percent is a percentage (12.5 means 12.5 %) with at most 4 decimals, |p| < 1000. Bounds such as
// 0–100 for discounts belong to the caller.
type Percent struct{ d decimal.Decimal }

// NewPercent parses a plain decimal string.
func NewPercent(s string) (Percent, error) {
	d, err := percentLimits.parse(s)
	if err != nil {
		return Percent{}, err
	}
	return Percent{d: d}, nil
}

// MustPercent is NewPercent for constants and tests; it panics on invalid input.
func MustPercent(s string) Percent {
	p, err := NewPercent(s)
	if err != nil {
		panic(err)
	}
	return p
}

// Decimal exposes the underlying value.
func (p Percent) Decimal() decimal.Decimal { return p.d }

// Cmp returns -1, 0 or +1 as p is less than, equal to or greater than o.
func (p Percent) Cmp(o Percent) int { return p.d.Cmp(o.d) }

// String returns the canonical decimal string.
func (p Percent) String() string { return p.d.String() }

// MarshalJSON writes p as a JSON string.
func (p Percent) MarshalJSON() ([]byte, error) { return quote(p.d), nil }

// UnmarshalJSON accepts only a JSON string with a plain decimal.
func (p *Percent) UnmarshalJSON(data []byte) error {
	d, ok, err := percentLimits.unmarshalJSON(data, p)
	if err != nil || !ok {
		return err
	}
	p.d = d
	return nil
}

// Value implements driver.Valuer.
func (p Percent) Value() (driver.Value, error) { return percentLimits.value(p.d) }

// Scan implements sql.Scanner.
func (p *Percent) Scan(src any) error {
	d, err := percentLimits.scan(src)
	if err != nil {
		return err
	}
	p.d = d
	return nil
}
