// Package periodlock refuses postings whose business date falls inside a locked accounting
// period. period_locks is an append-only event log; the newest event carries the effective lock
// date.
package periodlock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// Guard checks business dates against the tenant's period lock.
type Guard struct{}

// NewGuard returns a guard.
func NewGuard() *Guard { return &Guard{} }

// LockedThrough returns the effective lock date (inclusive), or nil when nothing is locked (no
// event yet, or the newest event reopened every period). It
// takes FOR KEY SHARE on the tenant row: postings run concurrently with each other, while the
// lock/reopen service (which takes FOR UPDATE on the same row before appending an event) waits
// for in-flight postings, so no posting can slip into a period being locked.
func (*Guard) LockedThrough(ctx context.Context, q db.Querier, tenantID uuid.UUID) (*time.Time, error) {
	var one int
	if err := q.QueryRowxContext(ctx, `SELECT 1 FROM tenants WHERE id = $1 FOR KEY SHARE`, tenantID).Scan(&one); err != nil {
		if db.IsNoRows(err) {
			return nil, apperr.NewNotFound("tenant_not_found", "The store does not exist")
		}
		return nil, fmt.Errorf("period lock: lock tenant: %w", err)
	}
	// The newest event decides; a reopen that unlocked every period stores NULL.
	var through *time.Time
	err := q.QueryRowxContext(ctx, `
		SELECT locked_through FROM period_locks
		WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, tenantID).Scan(&through)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("period lock: read: %w", err)
	}
	return through, nil
}

// Check returns PeriodLocked when businessDate is on or before the effective lock date.
func (g *Guard) Check(ctx context.Context, q db.Querier, tenantID uuid.UUID, businessDate time.Time) error {
	through, err := g.LockedThrough(ctx, q, tenantID)
	if err != nil || through == nil {
		return err
	}
	if !dateOnly(businessDate).After(dateOnly(*through)) {
		return apperr.NewPeriodLocked(fmt.Sprintf("The period through %s is locked", through.Format(time.DateOnly)))
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
