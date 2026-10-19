package sales

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
)

// Date is a calendar date (a date column, "YYYY-MM-DD" in JSON), held at midnight UTC.
type Date struct{ time.Time }

// DateOf returns the date of t as a Date.
func DateOf(t time.Time) Date { return Date{clock.Date(t.Year(), t.Month(), t.Day())} }

// String formats the date as YYYY-MM-DD.
func (d Date) String() string { return d.Format(time.DateOnly) }

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("date: want a YYYY-MM-DD string")
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return fmt.Errorf("date: want a YYYY-MM-DD string")
	}
	*d = DateOf(t)
	return nil
}

// Scan implements sql.Scanner.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*d = DateOf(v)
	case string:
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			return err
		}
		*d = DateOf(t)
	case nil:
		*d = Date{}
	default:
		return fmt.Errorf("date: cannot scan %T", src)
	}
	return nil
}

// Value implements driver.Valuer.
func (d Date) Value() (driver.Value, error) { return d.Time, nil }

// datePtr returns the date of t, nil for nil.
func datePtr(t *Date) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.Time
	return &v
}
