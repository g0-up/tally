// Package clock is the single source of time. The database never computes timestamps; repositories
// take them from a Clock so tests can pin time with Fixed.
package clock

import (
	"fmt"
	"sync"
	"time"
)

// Clock returns the current instant in UTC.
type Clock interface {
	Now() time.Time
}

// System reads the machine clock.
type System struct{}

// Now returns the current time in UTC, truncated to microseconds (PostgreSQL timestamptz precision)
// so values round-trip through the database unchanged.
func (System) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Fixed is a manually controlled clock for tests and replays. It is safe for concurrent use.
type Fixed struct {
	mu sync.Mutex
	t  time.Time
}

// NewFixed returns a clock frozen at t.
func NewFixed(t time.Time) *Fixed { return &Fixed{t: t.UTC().Truncate(time.Microsecond)} }

// Now returns the frozen instant.
func (f *Fixed) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Set moves the clock to t.
func (f *Fixed) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t.UTC().Truncate(time.Microsecond)
}

// Advance moves the clock forward by d.
func (f *Fixed) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// Date returns the civil date y-m-d as midnight UTC, the representation used for every
// business date (PostgreSQL `date` columns scan to the same value).
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// BusinessDate returns the civil date of instant t in the tenant's time zone, as midnight UTC.
// Deadlines and period locks use this, never the timezone of the server or the browser.
func BusinessDate(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return Date(y, m, d)
}

// TruncateDate drops the time of day of t, keeping its UTC civil date.
func TruncateDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return Date(y, m, d)
}

// LoadLocation loads an IANA time zone and rejects the empty name, which time.LoadLocation
// silently maps to UTC.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" {
		return nil, fmt.Errorf("time zone name is empty")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load time zone %q: %w", name, err)
	}
	return loc, nil
}
