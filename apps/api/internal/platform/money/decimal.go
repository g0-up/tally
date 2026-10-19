// Package money holds the decimal value objects of the domain: Money, Qty, Factor and Percent.
// Values are built only from plain decimal strings or integers (never floats), carry a bounded
// scale and range that match their PostgreSQL columns, travel as JSON strings and round half
// away from zero.
package money

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	"github.com/shopspring/decimal"
)

// Scale (fractional digits) of each value object, matching numeric(19,4), numeric(18,4),
// numeric(18,6) and numeric(7,4).
const (
	MoneyScale   = 4
	QtyScale     = 4
	FactorScale  = 6
	PercentScale = 4
)

// Validation failures. Wrapped errors carry the offending input.
var (
	ErrFormat      = errors.New("not a plain decimal string")
	ErrScale       = errors.New("too many decimal places")
	ErrRange       = errors.New("out of range")
	ErrNotPositive = errors.New("must be greater than zero")
)

// plainDecimal accepts "-12", "0.5", "15000.0000"; rejects exponents, signs other than a leading
// minus, bare points, separators and whitespace.
var plainDecimal = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// limits bounds a value object: |v| < maxAbs and at most scale significant fractional digits.
type limits struct {
	name   string
	scale  int32
	maxAbs decimal.Decimal
}

var (
	moneyLimits   = limits{name: "money", scale: MoneyScale, maxAbs: decimal.New(1, 15)}    // numeric(19,4)
	qtyLimits     = limits{name: "quantity", scale: QtyScale, maxAbs: decimal.New(1, 14)}   // numeric(18,4)
	factorLimits  = limits{name: "factor", scale: FactorScale, maxAbs: decimal.New(1, 12)}  // numeric(18,6)
	percentLimits = limits{name: "percent", scale: PercentScale, maxAbs: decimal.New(1, 3)} // numeric(7,4)
)

// parse validates s against the plain decimal syntax and the limits.
func (l limits) parse(s string) (decimal.Decimal, error) {
	if len(s) > 64 || !plainDecimal.MatchString(s) {
		return decimal.Decimal{}, fmt.Errorf("%s %q: %w", l.name, s, ErrFormat)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%s %q: %w", l.name, s, ErrFormat)
	}
	return l.check(d)
}

// check enforces the scale (trailing zeros do not count) and the range.
func (l limits) check(d decimal.Decimal) (decimal.Decimal, error) {
	if !d.Equal(d.Truncate(l.scale)) {
		return decimal.Decimal{}, fmt.Errorf("%s %s: %w (max %d)", l.name, d.String(), ErrScale, l.scale)
	}
	if d.Abs().GreaterThanOrEqual(l.maxAbs) {
		return decimal.Decimal{}, fmt.Errorf("%s %s: %w", l.name, d.String(), ErrRange)
	}
	return d, nil
}

// scan converts a database value into a decimal checked against the limits.
func (l limits) scan(src any) (decimal.Decimal, error) {
	switch v := src.(type) {
	case string:
		return l.parse(v)
	case []byte:
		return l.parse(string(v))
	case int64:
		return l.check(decimal.NewFromInt(v))
	case nil:
		return decimal.Decimal{}, fmt.Errorf("%s: cannot scan NULL; use sql.Null", l.name)
	default:
		return decimal.Decimal{}, fmt.Errorf("%s: cannot scan %T", l.name, src)
	}
}

// value converts a decimal to the driver representation, rejecting values the column cannot hold
// (sums can leave the range).
func (l limits) value(d decimal.Decimal) (driver.Value, error) {
	if _, err := l.check(d); err != nil {
		return nil, err
	}
	return d.String(), nil
}

// unmarshalJSON accepts only a JSON string holding a plain decimal. Failures are reported as
// *json.UnmarshalTypeError so encoding/json adds the field path for the API error.
func (l limits) unmarshalJSON(data []byte, target any) (decimal.Decimal, bool, error) {
	if string(data) == "null" {
		return decimal.Decimal{}, false, nil
	}
	typ := reflect.TypeOf(target).Elem()
	var s string
	if len(data) == 0 || data[0] != '"' || json.Unmarshal(data, &s) != nil {
		return decimal.Decimal{}, false, &json.UnmarshalTypeError{Value: jsonKind(data), Type: typ}
	}
	d, err := l.parse(s)
	if err != nil {
		return decimal.Decimal{}, false, &json.UnmarshalTypeError{Value: "string " + err.Error(), Type: typ}
	}
	return d, true, nil
}

func jsonKind(data []byte) string {
	if len(data) == 0 {
		return "empty"
	}
	switch data[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	default:
		return "number"
	}
}

func quote(d decimal.Decimal) []byte {
	return []byte(`"` + d.String() + `"`)
}
