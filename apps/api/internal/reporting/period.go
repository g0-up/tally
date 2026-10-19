package reporting

import (
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
)

// Period kinds of the dashboard.
const (
	PeriodDay   = "day"
	PeriodWeek  = "7d"
	PeriodMonth = "month"
	PeriodYear  = "year"
)

// Period is a dashboard range of business dates in the tenant's calendar: one day, the seven days
// ending on Date, the month or the year holding Date. From and To are inclusive.
type Period struct {
	Kind string
	Date time.Time
	From time.Time
	To   time.Time
}

// Bucket is one bar of a dashboard series: a day, or a month of a year period.
type Bucket struct {
	Label string
	From  time.Time
	To    time.Time
}

func validPeriodKind(kind string) bool {
	switch kind {
	case PeriodDay, PeriodWeek, PeriodMonth, PeriodYear:
		return true
	}
	return false
}

// NewPeriod is the period of kind holding date. date is a business date (midnight UTC).
func NewPeriod(kind string, date time.Time) (Period, error) {
	if !validPeriodKind(kind) {
		return Period{}, errInvalid(map[string]string{"period": "invalid"})
	}
	d := clock.Date(date.Year(), date.Month(), date.Day())
	p := Period{Kind: kind, Date: d}
	switch kind {
	case PeriodDay:
		p.From, p.To = d, d
	case PeriodWeek:
		p.From, p.To = d.AddDate(0, 0, -6), d
	case PeriodMonth:
		p.From = clock.Date(d.Year(), d.Month(), 1)
		p.To = p.From.AddDate(0, 1, -1)
	case PeriodYear:
		p.From = clock.Date(d.Year(), time.January, 1)
		p.To = clock.Date(d.Year(), time.December, 31)
	}
	return p, nil
}

// Prev is the period of the same kind just before p: the day before, the seven days before, the
// previous month or year.
func (p Period) Prev() Period {
	switch p.Kind {
	case PeriodWeek:
		prev, _ := NewPeriod(p.Kind, p.Date.AddDate(0, 0, -7))
		return prev
	default:
		prev, _ := NewPeriod(p.Kind, p.From.AddDate(0, 0, -1))
		return prev
	}
}

// Next is the period of the same kind just after p.
func (p Period) Next() Period {
	switch p.Kind {
	case PeriodWeek:
		next, _ := NewPeriod(p.Kind, p.Date.AddDate(0, 0, 7))
		return next
	default:
		next, _ := NewPeriod(p.Kind, p.To.AddDate(0, 0, 1))
		return next
	}
}

// Days is the number of days in p.
func (p Period) Days() int { return daysBetween(p.From, p.To) }

// Buckets splits p into its chart bars: days, or months for a year.
func (p Period) Buckets() []Bucket {
	var out []Bucket
	if p.Kind == PeriodYear {
		for m := p.From; !m.After(p.To); m = m.AddDate(0, 1, 0) {
			out = append(out, Bucket{Label: m.Format("01/2006"), From: m, To: m.AddDate(0, 1, -1)})
		}
		return out
	}
	for d := p.From; !d.After(p.To); d = d.AddDate(0, 0, 1) {
		out = append(out, Bucket{Label: d.Format("02/01"), From: d, To: d})
	}
	return out
}

// daysBetween counts the days from from to to, both included.
func daysBetween(from, to time.Time) int {
	return int(to.Sub(from).Hours()/24) + 1
}
