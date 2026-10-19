package reporting

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Debt directions.
const (
	Receivable = "receivable"
	Payable    = "payable"
)

// AgingBuckets are the unpaid amounts of a party by days past due.
type AgingBuckets struct {
	Current money.Money
	D1To30  money.Money
	D31To60 money.Money
	D61To90 money.Money
	Over90  money.Money
}

// Over60 is everything more than sixty days past due, the last column of the aging screen.
func (b AgingBuckets) Over60() money.Money { return b.D61To90.Add(b.Over90) }

// Total is the sum of the buckets.
func (b AgingBuckets) Total() money.Money {
	return money.SumMoney(b.Current, b.D1To30, b.D31To60, b.D61To90, b.Over90)
}

// AgingRow is one party's aging.
type AgingRow struct {
	PartyID uuid.UUID
	Name    string
	Buckets AgingBuckets
	// NextDue is the earliest due date of the party's unpaid parts.
	NextDue time.Time
}

// Aging is the aging of one ledger as of a date.
type Aging struct {
	Rows   []AgingRow
	Totals AgingBuckets
}

// ForecastDay is the expected and confirmed money of one day.
type ForecastDay struct {
	Date          time.Time
	ReceivableDue money.Money
	PayableDue    money.Money
	// Promised is what customers promised to pay that day; it is never mixed into confirmed cash.
	Promised     money.Money
	ConfirmedIn  money.Money
	ConfirmedOut money.Money
}

// Forecast is the debt schedule of a date range.
type Forecast struct {
	OverdueReceivable money.Money
	OverduePayable    money.Money
	Days              []ForecastDay
}

// DebtReader is the debt ledger as reporting reads it: the aging replayed as of a date and the
// collection and payment schedule. The composition root adapts the debt domain to it.
type DebtReader interface {
	Aging(ctx context.Context, scope Scope, direction string, asOf time.Time) (Aging, error)
	Forecast(ctx context.Context, scope Scope, from, to time.Time) (Forecast, error)
}
