package sales

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Shift statuses.
const (
	ShiftOpen   = "open"
	ShiftClosed = "closed"
)

// errShiftMoved rolls back a POS posting whose day's shift changed after the read that planned it
// (another sale opened it, or the worker closed it); the sale is planned and posted again.
var errShiftMoved = errors.New("sales: the day's shift changed while the sale posted")

// shiftPlan is what the read before a POS posting found of the shift of date, the day the sale is
// made: open is that shift when it is open, nil when the posting opens it (and issues its CA).
type shiftPlan struct {
	date time.Time
	open *uuid.UUID
}

// planShift reads the open shift and plans the shift of date for a POS posting.
func planShift(ctx context.Context, q db.Querier, date time.Time) (shiftPlan, error) {
	plan := shiftPlan{date: date}
	var sh Shift
	err := sqlx.GetContext(ctx, q, &sh, `SELECT `+shiftColumns+` FROM shifts WHERE status = 'open'`)
	switch {
	case db.IsNoRows(err):
	case err != nil:
		return plan, wrap("read open shift", err)
	case sameDay(sh.BusinessDate.Time, date):
		plan.open = &sh.ID
	}
	return plan, nil
}

// dayShift returns the shift a POS sale posts in, inside its posting: the open shift of the day,
// or a new one the posting opens with its CA number when the sale is the first of the day. A shift
// left open from an earlier day is closed first. When the open shift is not the one plan read, the
// posting rolls back with errShiftMoved, giving its numbers back.
//
// A sale in the day's open shift only share-locks it: that keeps the shift from closing before
// the sale commits without making the other sales of the shift wait for it. Opening or closing a
// shift locks the open one for update.
func dayShift(ctx context.Context, tx posting.Tx, plan shiftPlan) (Shift, error) {
	var cur Shift
	if plan.open != nil {
		err := sqlx.GetContext(ctx, tx, &cur, `SELECT `+shiftColumns+` FROM shifts WHERE id = $1 AND status = 'open' FOR SHARE`,
			*plan.open)
		switch {
		case db.IsNoRows(err):
			return Shift{}, errShiftMoved
		case err != nil:
			return Shift{}, wrap("lock open shift", err)
		}
		return cur, nil
	}
	err := sqlx.GetContext(ctx, tx, &cur, `SELECT `+shiftColumns+` FROM shifts WHERE status = 'open' FOR UPDATE`)
	found := err == nil
	if err != nil && !db.IsNoRows(err) {
		return Shift{}, wrap("lock open shift", err)
	}
	if found {
		if sameDay(cur.BusinessDate.Time, plan.date) {
			return Shift{}, errShiftMoved
		}
		ev, err := closeShift(ctx, tx, cur, tx.Now())
		if err != nil {
			return Shift{}, err
		}
		tx.Audit(ev)
	}
	docNo, err := posting.TakeDocNo(tx, "CA")
	if err != nil {
		return Shift{}, err
	}
	out := Shift{ID: ids.New(), DocNo: docNo, BusinessDate: DateOf(plan.date), OpenedAt: tx.Now(), Status: ShiftOpen}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO shifts (id, tenant_id, doc_no, business_date, opened_at, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'open', $5, $5)`,
		out.ID, tx.TenantID(), docNo, plan.date, out.OpenedAt); err != nil {
		return Shift{}, wrap("insert shift", err)
	}
	tx.Audit(audit.Event{Action: ActionShiftOpen, EntityType: entityShift, EntityID: out.ID, DocNo: docNo, After: out})
	return out, nil
}

// closeShift closes a shift of an earlier day at now and returns the audit event of it.
func closeShift(ctx context.Context, q db.Querier, sh Shift, now time.Time) (audit.Event, error) {
	if _, err := q.ExecContext(ctx, `
		UPDATE shifts SET status = 'closed', closed_at = $2, updated_at = $2, version = version + 1
		WHERE id = $1 AND status = 'open'`, sh.ID, now); err != nil {
		return audit.Event{}, wrap("close shift", err)
	}
	return audit.Event{Action: ActionShiftClose, EntityType: entityShift, EntityID: sh.ID, DocNo: sh.DocNo,
		Before: map[string]any{"status": sh.Status}, After: map[string]any{"status": ShiftClosed, "closed_at": now}}, nil
}

// CloseStaleShifts closes the shift left open from an earlier business date of a tenant (the
// worker runs it after midnight); it reports whether one was closed.
func (s *Service) CloseStaleShifts(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	actor := audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: audit.SourceWorker}
	closed := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		today, err := s.today(ctx, q, tenantID)
		if err != nil {
			return err
		}
		var sh Shift
		err = sqlx.GetContext(ctx, q, &sh, `SELECT `+shiftColumns+` FROM shifts WHERE status = 'open' AND business_date < $1 FOR UPDATE`, today)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return wrap("lock stale shift", err)
		}
		closed = true
		ev, err := closeShift(ctx, q, sh, s.clock.Now())
		if err != nil {
			return err
		}
		return s.record(ctx, q, actor, ev)
	})
	return closed, err
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
