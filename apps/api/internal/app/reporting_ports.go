package app

import (
	"context"
	"time"

	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/reporting"
)

// reportingDebt is the debt ledger as reporting reads it: the aging as of a date and the
// collection and payment schedule.
type reportingDebt struct{ s *debt.Service }

func debtScope(scope reporting.Scope) debt.Scope {
	return debt.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}
}

func agingBuckets(b debt.AgingBuckets) reporting.AgingBuckets {
	return reporting.AgingBuckets{Current: b.Current, D1To30: b.D1To30, D31To60: b.D31To60, D61To90: b.D61To90, Over90: b.Over90}
}

func (r reportingDebt) Aging(ctx context.Context, scope reporting.Scope, direction string, asOf time.Time) (reporting.Aging, error) {
	a, err := r.s.Aging(ctx, debtScope(scope), direction, asOf)
	if err != nil {
		return reporting.Aging{}, err
	}
	out := reporting.Aging{Rows: make([]reporting.AgingRow, len(a.Rows)), Totals: agingBuckets(a.Totals)}
	for i, row := range a.Rows {
		out.Rows[i] = reporting.AgingRow{PartyID: row.PartyID, Name: row.Name, Buckets: agingBuckets(row.Buckets), NextDue: row.NextDue}
	}
	return out, nil
}

func (r reportingDebt) Forecast(ctx context.Context, scope reporting.Scope, from, to time.Time) (reporting.Forecast, error) {
	f, err := r.s.Forecast(ctx, debtScope(scope), from, to)
	if err != nil {
		return reporting.Forecast{}, err
	}
	out := reporting.Forecast{
		OverdueReceivable: f.OverdueReceivable, OverduePayable: f.OverduePayable,
		Days: make([]reporting.ForecastDay, len(f.Days)),
	}
	for i, d := range f.Days {
		out.Days[i] = reporting.ForecastDay{
			Date: d.Date, ReceivableDue: d.ReceivableDue, PayableDue: d.PayableDue, Promised: d.Promised,
			ConfirmedIn: d.ConfirmedIn, ConfirmedOut: d.ConfirmedOut,
		}
	}
	return out, nil
}
