package money

import (
	"database/sql/driver"
	"strings"

	"github.com/shopspring/decimal"
)

// Money is an amount in the tenant currency (VND) with at most 4 decimals.
// The zero value is 0.
type Money struct{ d decimal.Decimal }

// ZeroMoney is 0.
var ZeroMoney = Money{}

// NewMoney parses a plain decimal string such as "15000" or "-12.5".
func NewMoney(s string) (Money, error) {
	d, err := moneyLimits.parse(s)
	if err != nil {
		return Money{}, err
	}
	return Money{d: d}, nil
}

// MustMoney is NewMoney for constants and tests; it panics on invalid input.
func MustMoney(s string) Money {
	m, err := NewMoney(s)
	if err != nil {
		panic(err)
	}
	return m
}

// NewMoneyFromInt returns a whole amount.
func NewMoneyFromInt(v int64) Money { return Money{d: decimal.NewFromInt(v)} }

// RoundMoney rounds the result of a calculation the value object does not offer (a division, a
// ratio) half away from zero to 4 decimals; it fails when the amount is out of range.
func RoundMoney(d decimal.Decimal) (Money, error) {
	v, err := moneyLimits.check(d.Round(MoneyScale))
	if err != nil {
		return Money{}, err
	}
	return Money{d: v}, nil
}

// Decimal exposes the underlying value for calculations the value object does not offer.
func (m Money) Decimal() decimal.Decimal { return m.d }

// Add returns m + o.
func (m Money) Add(o Money) Money { return Money{d: m.d.Add(o.d)} }

// Sub returns m - o.
func (m Money) Sub(o Money) Money { return Money{d: m.d.Sub(o.d)} }

// Neg returns -m.
func (m Money) Neg() Money { return Money{d: m.d.Neg()} }

// Abs returns |m|.
func (m Money) Abs() Money { return Money{d: m.d.Abs()} }

// MulQty returns m × q rounded half away from zero to 4 decimals (unit price × quantity).
func (m Money) MulQty(q Qty) Money { return Money{d: m.d.Mul(q.d).Round(MoneyScale)} }

// MulFactor returns m × f rounded half away from zero to 4 decimals (base unit price × factor).
func (m Money) MulFactor(f Factor) Money { return Money{d: m.d.Mul(f.d).Round(MoneyScale)} }

// MulPercent returns m × p / 100 rounded half away from zero to 4 decimals (discounts, tax).
func (m Money) MulPercent(p Percent) Money {
	return Money{d: m.d.Mul(p.d).Div(decimal.NewFromInt(100)).Round(MoneyScale)}
}

// RoundVND rounds to whole đồng, half away from zero: the display and settlement boundary.
func (m Money) RoundVND() Money { return Money{d: m.d.Round(0)} }

// Cmp returns -1, 0 or +1 as m is less than, equal to or greater than o.
func (m Money) Cmp(o Money) int { return m.d.Cmp(o.d) }

// Equal reports whether m and o have the same value (scale-insensitive).
func (m Money) Equal(o Money) bool { return m.d.Equal(o.d) }

// IsZero reports m == 0.
func (m Money) IsZero() bool { return m.d.IsZero() }

// IsPositive reports m > 0.
func (m Money) IsPositive() bool { return m.d.IsPositive() }

// IsNegative reports m < 0.
func (m Money) IsNegative() bool { return m.d.IsNegative() }

// String returns the canonical decimal string without trailing zeros ("15000", "12.5").
func (m Money) String() string { return m.d.String() }

// FormatVND renders the amount rounded to whole đồng with "." thousands separators, as
// Vietnamese text shows money ("1.250.000").
func (m Money) FormatVND() string {
	s := m.RoundVND().String()
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// SumMoney adds all amounts.
func SumMoney(ms ...Money) Money {
	total := decimal.Zero
	for _, m := range ms {
		total = total.Add(m.d)
	}
	return Money{d: total}
}

// MarshalJSON writes m as a JSON string.
func (m Money) MarshalJSON() ([]byte, error) { return quote(m.d), nil }

// UnmarshalJSON accepts only a JSON string with a plain decimal; numbers are rejected.
func (m *Money) UnmarshalJSON(data []byte) error {
	d, ok, err := moneyLimits.unmarshalJSON(data, m)
	if err != nil || !ok {
		return err
	}
	m.d = d
	return nil
}

// Value implements driver.Valuer.
func (m Money) Value() (driver.Value, error) { return moneyLimits.value(m.d) }

// Scan implements sql.Scanner.
func (m *Money) Scan(src any) error {
	d, err := moneyLimits.scan(src)
	if err != nil {
		return err
	}
	m.d = d
	return nil
}
