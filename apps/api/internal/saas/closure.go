package saas

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Closure request statuses.
const (
	ClosureRequested = "requested"
	ClosureVerified  = "verified"
	ClosureWaiting   = "waiting"
	// ClosureExporting is a closure whose wait ended: its final full data export is being taken,
	// the store only reads and the owner can no longer cancel.
	ClosureExporting = "exporting"
	// ClosureExportFailed is an exporting closure whose final exports kept failing; it waits for an
	// operator's retry, the store still only reading.
	ClosureExportFailed = "export_failed"
	ClosureCompleted    = "completed"
	ClosureCancelled    = "cancelled"
)

// ClosureWaitDays is how long a verified closure waits; the store keeps working and the owner can
// cancel until then. CompleteClosures then takes the final export and closes the store.
const ClosureWaitDays = 30

// ClosureExportAttempts is how many final exports of a closure may fail in a row before it stops
// at export_failed.
const ClosureExportAttempts = 3

// closureDownloadTTL bounds the signed download address a closure export link redirects to.
const closureDownloadTTL = 5 * time.Minute

// export_jobs.params reasons of a closure's exports.
const (
	exportReasonClosure      = "closure"
	exportReasonClosureFinal = "closure_final"
)

// ClosureRequest is an owner's request to close the store.
type ClosureRequest struct {
	ID           uuid.UUID  `db:"id"`
	TenantID     uuid.UUID  `db:"tenant_id"`
	RequestedBy  uuid.UUID  `db:"requested_by"`
	VerifiedAt   *time.Time `db:"verified_at"`
	ExportJobID  *uuid.UUID `db:"export_job_id"`
	WaitingUntil time.Time  `db:"waiting_until"`
	LegalHold    bool       `db:"legal_hold"`
	Status       string     `db:"status"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

// ClosureExport is the state of a closure's full data export.
type ClosureExport struct {
	ID        uuid.UUID  `db:"id"`
	Status    string     `db:"status"`
	ExpiresAt *time.Time `db:"expires_at"`
	// Reason is exportReasonClosure, or exportReasonClosureFinal for the export taken when the
	// wait ended.
	Reason string `db:"reason"`
	// Round numbers the final exports: the first round starts when the wait ends, each operator
	// retry starts the next one.
	Round int `db:"round"`
	// FailedAttempts counts the closure's failed or cancelled exports of this reason and round.
	FailedAttempts int `db:"failed_attempts"`
}

// Final reports the export taken when the closure's wait ended.
func (e ClosureExport) Final() bool { return e.Reason == exportReasonClosureFinal }

// downloadable reports a succeeded export whose link still works at now.
func (e ClosureExport) downloadable(now time.Time) bool {
	return e.Status == "succeeded" && e.ExpiresAt != nil && now.Before(*e.ExpiresAt)
}

// closureFreezes reports a closure status under which the store only reads and the owner cannot
// cancel: its final export is being taken.
func closureFreezes(status string) bool {
	return status == ClosureExporting || status == ClosureExportFailed
}

// ClosureView is the store's latest closure request with its export.
type ClosureView struct {
	Request *ClosureRequest
	Export  *ClosureExport
}

const closureColumns = `id, tenant_id, requested_by, verified_at, export_job_id, waiting_until, legal_hold, status,
	created_at, updated_at`

func (s *Service) closureTokens() (ClosureTokens, error) {
	if s.closure == nil {
		return nil, errors.New("saas: closure verification is not configured")
	}
	return s.closure, nil
}

// latestClosure returns the tenant's latest closure request; q is any transaction.
func latestClosure(ctx context.Context, q db.Querier, tenantID uuid.UUID, forUpdate bool) (ClosureRequest, bool, error) {
	query := `SELECT ` + closureColumns + ` FROM account_closure_requests WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var r ClosureRequest
	err := sqlx.GetContext(ctx, q, &r, query, tenantID)
	if db.IsNoRows(err) {
		return ClosureRequest{}, false, nil
	}
	if err != nil {
		return ClosureRequest{}, false, fmt.Errorf("saas: read closure request: %w", err)
	}
	return r, true, nil
}

// closedOnRequest reports whether the store's latest closure request completed: the store is
// closed for good, and no payment, plan change or lifecycle step reopens it.
func closedOnRequest(ctx context.Context, q db.Querier, tenantID uuid.UUID) (bool, error) {
	r, found, err := latestClosure(ctx, q, tenantID, false)
	return found && r.Status == ClosureCompleted, err
}

// closureExport returns the export a closure request waits for; q is the tenant transaction.
func closureExport(ctx context.Context, q db.Querier, r ClosureRequest) (ClosureExport, bool, error) {
	if r.ExportJobID == nil {
		return ClosureExport{}, false, nil
	}
	var e ClosureExport
	err := sqlx.GetContext(ctx, q, &e, `
		SELECT j.id, j.status, j.expires_at, coalesce(j.params->>'reason', '') AS reason,
		  coalesce((j.params->>'round')::int, 0) AS round,
		  (SELECT count(*) FROM export_jobs a WHERE a.kind = $2 AND a.params->>'closure_request_id' = $3::text
		     AND a.params->>'reason' IS NOT DISTINCT FROM j.params->>'reason'
		     AND coalesce((a.params->>'round')::int, 0) = coalesce((j.params->>'round')::int, 0)
		     AND a.status IN ('failed', 'cancelled')) AS failed_attempts
		FROM export_jobs j WHERE j.id = $1`, *r.ExportJobID, DataExportKind, r.ID)
	if db.IsNoRows(err) {
		return ClosureExport{}, false, nil
	}
	if err != nil {
		return ClosureExport{}, false, fmt.Errorf("saas: read closure export: %w", err)
	}
	return e, true, nil
}

// Closure returns the store's latest closure request and its export.
func (s *Service) Closure(ctx context.Context, tenantID uuid.UUID) (ClosureView, error) {
	var out ClosureView
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		r, found, err := latestClosure(ctx, q, tenantID, false)
		if err != nil || !found {
			return err
		}
		out.Request = &r
		e, found, err := closureExport(ctx, q, r)
		if found {
			out.Export = &e
		}
		return err
	})
	return out, err
}

// closureFrozen reports whether the tenant's latest closure is taking its final export, so the
// store only reads.
func closureFrozen(ctx context.Context, q db.Querier, tenantID uuid.UUID) (bool, error) {
	r, found, err := latestClosure(ctx, q, tenantID, false)
	return found && closureFreezes(r.Status), err
}

// RequestClosure opens (or re-opens) a closure request and mails the owner its one-time
// verification link. A request already verified must be cancelled first. The caller passed the
// re-authentication guard.
func (s *Service) RequestClosure(ctx context.Context, p middleware.Principal, actor audit.Actor) (ClosureRequest, error) {
	tokens, err := s.closureTokens()
	if err != nil {
		return ClosureRequest{}, err
	}
	var out ClosureRequest
	err = tokens.IssueClosureToken(ctx, p, func(q db.Querier) (uuid.UUID, error) {
		t, err := readTenantState(ctx, q, p.TenantID, false)
		if err != nil {
			return uuid.Nil, err
		}
		if t.IsSandbox {
			return uuid.Nil, errSandbox()
		}
		now := s.clock.Now()
		today, err := t.today(now)
		if err != nil {
			return uuid.Nil, err
		}
		r, found, err := latestClosure(ctx, q, p.TenantID, true)
		if err != nil {
			return uuid.Nil, err
		}
		switch {
		case found && (r.Status == ClosureVerified || r.Status == ClosureWaiting || closureFreezes(r.Status)):
			return uuid.Nil, errClosureState(r.Status)
		case found && r.Status == ClosureRequested:
			out = r
			return r.ID, nil
		}
		var hold bool
		if err := q.QueryRowxContext(ctx, `SELECT legal_hold FROM tenants WHERE id = $1`, p.TenantID).Scan(&hold); err != nil {
			return uuid.Nil, fmt.Errorf("saas: read legal hold: %w", err)
		}
		out = ClosureRequest{
			ID: ids.New(), TenantID: p.TenantID, RequestedBy: p.UserID, WaitingUntil: today.AddDate(0, 0, ClosureWaitDays),
			LegalHold: hold, Status: ClosureRequested, CreatedAt: now, UpdatedAt: now,
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO account_closure_requests (id, tenant_id, requested_by, waiting_until, legal_hold, status,
			  created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
			out.ID, out.TenantID, out.RequestedBy, out.WaitingUntil, out.LegalHold, out.Status, now); err != nil {
			return uuid.Nil, fmt.Errorf("saas: insert closure request: %w", err)
		}
		return out.ID, s.audit.Record(ctx, q, actor, audit.Event{
			Action: "account_closure.requested", EntityType: "account_closure_request", EntityID: out.ID,
		})
	})
	return out, err
}

// VerifyClosure confirms a closure request with its emailed token: the full data export is
// queued and the store waits ClosureWaitDays days (status closing; it still works and can
// cancel), then closes as CompleteClosures describes. The caller passed the re-authentication
// guard.
func (s *Service) VerifyClosure(ctx context.Context, p middleware.Principal, actor audit.Actor, token string) (ClosureRequest, error) {
	tokens, err := s.closureTokens()
	if err != nil {
		return ClosureRequest{}, err
	}
	var out ClosureRequest
	err = tokens.ConsumeClosureToken(ctx, p, token, func(q db.Querier, requestID uuid.UUID) error {
		r, found, err := latestClosure(ctx, q, p.TenantID, true)
		if err != nil {
			return err
		}
		if !found || r.ID != requestID {
			return errClosureState("superseded")
		}
		if r.Status != ClosureRequested {
			return errClosureState(r.Status)
		}
		t, err := readTenantState(ctx, q, p.TenantID, true)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		today, err := t.today(now)
		if err != nil {
			return err
		}
		export, err := s.queueDataExport(ctx, q, actor, actor.ID,
			dataExportParams{Reason: exportReasonClosure, ClosureRequestID: &r.ID}, false)
		if err != nil {
			return err
		}
		r.Status, r.VerifiedAt, r.ExportJobID = ClosureWaiting, &now, &export.ID
		r.WaitingUntil, r.UpdatedAt = today.AddDate(0, 0, ClosureWaitDays), now
		if _, err := q.ExecContext(ctx, `
			UPDATE account_closure_requests SET status = $2, verified_at = $3, export_job_id = $4, waiting_until = $5,
			  updated_at = $3
			WHERE id = $1`, r.ID, r.Status, now, export.ID, r.WaitingUntil); err != nil {
			return fmt.Errorf("saas: verify closure request: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE tenants SET status = 'closing', updated_at = $2, version = version + 1
			WHERE id = $1 AND status IN ('active', 'read_only')`, p.TenantID, now); err != nil {
			return fmt.Errorf("saas: mark tenant closing: %w", err)
		}
		out = r
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "account_closure.verified", EntityType: "account_closure_request", EntityID: r.ID,
			After: map[string]any{"waiting_until": r.WaitingUntil.Format(time.DateOnly), "export_job_id": export.ID},
		})
	})
	return out, err
}

// CancelClosure withdraws a closure request whose wait has not ended; a closing store returns to
// the status its subscription implies. A closure taking its final export (exporting,
// export_failed) can no longer be cancelled.
func (s *Service) CancelClosure(ctx context.Context, actor audit.Actor) (ClosureRequest, error) {
	var out ClosureRequest
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		r, found, err := latestClosure(ctx, q, actor.TenantID, true)
		if err != nil {
			return err
		}
		if !found {
			return errNotFound("Closure request")
		}
		if r.Status != ClosureRequested && r.Status != ClosureVerified && r.Status != ClosureWaiting {
			return errClosureState(r.Status)
		}
		now := s.clock.Now()
		if _, err := q.ExecContext(ctx, `UPDATE account_closure_requests SET status = 'cancelled', updated_at = $2
			WHERE id = $1`, r.ID, now); err != nil {
			return fmt.Errorf("saas: cancel closure request: %w", err)
		}
		status, err := s.subscriptionTenantStatus(ctx, q, actor.TenantID, now)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE tenants SET status = $2, updated_at = $3, version = version + 1 WHERE id = $1 AND status = 'closing'`,
			actor.TenantID, status, now); err != nil {
			return fmt.Errorf("saas: reopen tenant: %w", err)
		}
		r.Status, r.UpdatedAt = ClosureCancelled, now
		out = r
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "account_closure.cancelled", EntityType: "account_closure_request", EntityID: r.ID,
		})
	})
	return out, err
}

// subscriptionTenantStatus is the tenant status the tenant's subscription implies today.
func (s *Service) subscriptionTenantStatus(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) (string, error) {
	t, err := readTenantState(ctx, q, tenantID, false)
	if err != nil {
		return "", err
	}
	sub, found, err := readSubscription(ctx, q, tenantID, false)
	if err != nil || !found {
		return "active", err
	}
	plan, err := readPlan(ctx, q, sub.PlanID)
	if err != nil {
		return "", err
	}
	today, err := t.today(now)
	if err != nil {
		return "", err
	}
	sub, _ = s.lifecycle.Advance(sub, plan, today)
	return tenantStatusFor(sub.Status), nil
}

// closureStep is what one pass of CompleteClosures did to a store's closure.
type closureStep int

const (
	closureWaits closureStep = iota
	closureExportStarted
	closureRequeued
	closureGaveUp
	closureCompleted
)

// noExport is what a closure step returns when it issues no export link.
func noExport(err error) (uuid.UUID, time.Time, error) { return uuid.Nil, time.Time{}, err }

// CompleteClosures moves every waiting and exporting closure on and returns the number of stores
// closed:
//   - when the wait ended and no legal hold applies, the closure starts exporting: the store only
//     reads, the owner can no longer cancel, and a final full data export is queued, so the owner
//     takes the data written during the wait;
//   - a failed final export is queued again until ClosureExportAttempts of them failed; the
//     closure then stops at export_failed (error log, audit) until an operator retries it;
//   - once the final export succeeded and no legal hold applies, the store and its subscription
//     close, its open invoices are voided, and the owner is mailed a link to the export that
//     works as long as the export does. No data is deleted.
//
// A store held in maintenance waits for its release.
func (s *Service) CompleteClosures(ctx context.Context) (int, error) {
	tokens, err := s.closureTokens()
	if err != nil {
		return 0, err
	}
	var due []uuid.UUID
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &due, `
			SELECT DISTINCT r.tenant_id FROM account_closure_requests r JOIN tenants t ON t.id = r.tenant_id
			WHERE r.status IN ('waiting', 'exporting') AND t.status <> 'replaced' AND NOT t.maintenance
			ORDER BY r.tenant_id`)
	}); err != nil {
		return 0, fmt.Errorf("saas: list due closures: %w", err)
	}
	closed := 0
	for _, tenantID := range due {
		if err := ctx.Err(); err != nil {
			return closed, err
		}
		step, linked, err := s.completeClosure(ctx, tokens, tenantID)
		if err != nil {
			s.logger.ErrorContext(ctx, "account closure failed", "tenant_id", tenantID, "error", err)
			continue
		}
		switch step {
		case closureCompleted:
			closed++
			if !linked {
				s.logger.ErrorContext(ctx, "account closed without an export link: the store has no active owner",
					"tenant_id", tenantID)
			}
		case closureGaveUp:
			s.logger.ErrorContext(ctx, "account closure stopped: its final data export kept failing", "tenant_id", tenantID,
				"attempts", ClosureExportAttempts)
		}
	}
	return closed, nil
}

// completeClosure runs one step of the tenant's closure inside the account transaction that
// mails the export link of a completed closure; it reports the step and whether a link went out.
func (s *Service) completeClosure(ctx context.Context, tokens ClosureTokens, tenantID uuid.UUID) (closureStep, bool, error) {
	step := closureWaits
	linked, err := tokens.IssueExportLink(ctx, tenantID, func(q db.Querier) (uuid.UUID, time.Time, error) {
		if err := lockBilling(ctx, q, tenantID); err != nil {
			return noExport(err)
		}
		r, found, err := latestClosure(ctx, q, tenantID, true)
		if err != nil || !found || (r.Status != ClosureWaiting && r.Status != ClosureExporting) {
			return noExport(err)
		}
		var hold bool
		if err := q.QueryRowxContext(ctx, `SELECT legal_hold FROM tenants WHERE id = $1 FOR NO KEY UPDATE`, tenantID).
			Scan(&hold); err != nil {
			return noExport(fmt.Errorf("saas: read legal hold: %w", err))
		}
		hold = hold || r.LegalHold
		t, err := readTenantState(ctx, q, tenantID, false)
		if err != nil {
			return noExport(err)
		}
		now := s.clock.Now()
		today, err := t.today(now)
		if err != nil {
			return noExport(err)
		}
		actor := systemActor(tenantID, audit.SourceWorker)
		if r.Status == ClosureWaiting {
			if hold || !today.After(r.WaitingUntil) {
				return noExport(nil)
			}
			step = closureExportStarted
			return noExport(s.startFinalExport(ctx, q, actor, r, 1, "account_closure.final_export_queued"))
		}
		export, hasExport, err := closureExport(ctx, q, r)
		if err != nil {
			return noExport(err)
		}
		switch {
		case hasExport && (export.Status == "queued" || export.Status == "running"):
			return noExport(nil)
		case hasExport && (export.Status == "failed" || export.Status == "cancelled"):
			if export.FailedAttempts >= ClosureExportAttempts {
				step = closureGaveUp
				return noExport(s.stopClosureExport(ctx, q, actor, r, export))
			}
			step = closureRequeued
			return noExport(s.requeueClosureExport(ctx, q, actor, r, export))
		case hold:
			return noExport(nil)
		case !hasExport || !export.Final() || !export.downloadable(now):
			// The export's link ran out while a legal hold kept the store open: take it again.
			step = closureRequeued
			return noExport(s.requeueClosureExport(ctx, q, actor, r, export))
		}
		step = closureCompleted
		return export.ID, *export.ExpiresAt, s.closeOnRequest(ctx, q, actor, r, export.ID, today, now)
	})
	if err != nil {
		return closureWaits, false, err
	}
	return step, linked, nil
}

// startFinalExport moves the closure to exporting under a new round of final exports, queues the
// first of them and audits action.
func (s *Service) startFinalExport(ctx context.Context, q db.Querier, actor audit.Actor, r ClosureRequest, round int,
	action string) error {
	next, err := s.pointClosureAt(ctx, q, actor, r, round)
	if err != nil {
		return err
	}
	return s.audit.Record(ctx, q, actor, audit.Event{
		Action: action, EntityType: "account_closure_request", EntityID: r.ID,
		After: map[string]any{"previous_export_job_id": r.ExportJobID, "export_job_id": next, "round": round},
	})
}

// requeueClosureExport queues the closure's final export again in the same round.
func (s *Service) requeueClosureExport(ctx context.Context, q db.Querier, actor audit.Actor, r ClosureRequest,
	previous ClosureExport) error {
	round := max(previous.Round, 1)
	next, err := s.pointClosureAt(ctx, q, actor, r, round)
	if err != nil {
		return err
	}
	return s.audit.Record(ctx, q, actor, audit.Event{
		Action: "account_closure.export_requeued", EntityType: "account_closure_request", EntityID: r.ID,
		After: map[string]any{
			"previous_export_job_id": r.ExportJobID, "previous_status": previous.Status, "export_job_id": next,
			"round": round, "failed_attempts": previous.FailedAttempts,
		},
	})
}

// stopClosureExport stops a closure whose final exports kept failing at export_failed.
func (s *Service) stopClosureExport(ctx context.Context, q db.Querier, actor audit.Actor, r ClosureRequest,
	failed ClosureExport) error {
	if _, err := q.ExecContext(ctx, `UPDATE account_closure_requests SET status = $2, updated_at = $3 WHERE id = $1`,
		r.ID, ClosureExportFailed, s.clock.Now()); err != nil {
		return fmt.Errorf("saas: stop closure export: %w", err)
	}
	return s.audit.Record(ctx, q, actor, audit.Event{
		Action: "account_closure.export_failed", EntityType: "account_closure_request", EntityID: r.ID,
		After: map[string]any{"export_job_id": failed.ID, "round": failed.Round, "failed_attempts": failed.FailedAttempts},
	})
}

// pointClosureAt queues a final export of the closure in round, requested by the closure's
// requester (whose file it is), and makes the exporting closure wait for it.
func (s *Service) pointClosureAt(ctx context.Context, q db.Querier, actor audit.Actor, r ClosureRequest,
	round int) (uuid.UUID, error) {
	export, err := s.queueDataExport(ctx, q, actor, r.RequestedBy,
		dataExportParams{Reason: exportReasonClosureFinal, ClosureRequestID: &r.ID, Round: round}, false)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE account_closure_requests SET status = $2, export_job_id = $3, updated_at = $4 WHERE id = $1`,
		r.ID, ClosureExporting, export.ID, s.clock.Now()); err != nil {
		return uuid.Nil, fmt.Errorf("saas: point closure at its export: %w", err)
	}
	return export.ID, nil
}

// closeOnRequest completes the closure: the store and its subscription close and its open
// invoices are voided.
func (s *Service) closeOnRequest(ctx context.Context, q db.Querier, actor audit.Actor, r ClosureRequest,
	exportID uuid.UUID, today, now time.Time) error {
	if _, err := q.ExecContext(ctx, `UPDATE account_closure_requests SET status = 'completed', updated_at = $2
		WHERE id = $1`, r.ID, now); err != nil {
		return fmt.Errorf("saas: complete closure request: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE subscriptions SET status = 'closed', updated_at = $2, version = version + 1
		WHERE tenant_id = $1 AND status <> 'closed'`, r.TenantID, now); err != nil {
		return fmt.Errorf("saas: close subscription: %w", err)
	}
	voided, err := q.ExecContext(ctx, `UPDATE saas_invoices SET status = 'void' WHERE tenant_id = $1 AND status = 'open'`, r.TenantID)
	if err != nil {
		return fmt.Errorf("saas: void open invoices: %w", err)
	}
	nVoided, err := voided.RowsAffected()
	if err != nil {
		return fmt.Errorf("saas: void open invoices: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE tenants SET status = 'closed', updated_at = $2, version = version + 1
		WHERE id = $1`, r.TenantID, now); err != nil {
		return fmt.Errorf("saas: close tenant: %w", err)
	}
	return s.audit.Record(ctx, q, actor, audit.Event{
		Action: "account_closure.completed", EntityType: "account_closure_request", EntityID: r.ID,
		After: map[string]any{"closed_on": today.Format(time.DateOnly), "voided_invoices": nVoided, "export_job_id": exportID},
	})
}

// RetryClosureExport is an operator's retry of a closure's final export. An export_failed
// closure starts a new round of final exports (exporting again, the store still only reading); a
// completed closure whose final export can still be downloaded mails its owner a new link, the
// earlier ones stop working. Any other closure is 409 closure_state_invalid, as is a link the
// store has no active owner to receive.
func (s *Service) RetryClosureExport(ctx context.Context, admin audit.Actor, tenantID uuid.UUID) (ClosureView, error) {
	tokens, err := s.closureTokens()
	if err != nil {
		return ClosureView{}, err
	}
	if err := s.adminTenant(ctx, tenantID); err != nil {
		return ClosureView{}, err
	}
	admin.TenantID = tenantID
	resend := false
	linked, err := tokens.IssueExportLink(ctx, tenantID, func(q db.Querier) (uuid.UUID, time.Time, error) {
		if err := lockBilling(ctx, q, tenantID); err != nil {
			return noExport(err)
		}
		r, found, err := latestClosure(ctx, q, tenantID, true)
		if err != nil {
			return noExport(err)
		}
		if !found {
			return noExport(errNotFound("Closure request"))
		}
		export, hasExport, err := closureExport(ctx, q, r)
		if err != nil {
			return noExport(err)
		}
		switch {
		case r.Status == ClosureExportFailed:
			return noExport(s.startFinalExport(ctx, q, admin, r, export.Round+1, "account_closure.export_retried"))
		case r.Status != ClosureCompleted:
			return noExport(errClosureState(r.Status))
		case !hasExport || !export.Final() || !export.downloadable(s.clock.Now()):
			return noExport(errClosureState("completed and its final export can no longer be downloaded"))
		}
		resend = true
		return export.ID, *export.ExpiresAt, s.audit.Record(ctx, q, admin, audit.Event{
			Action: "account_closure.export_link_requested", EntityType: "account_closure_request", EntityID: r.ID,
			After: map[string]any{"export_job_id": export.ID},
		})
	})
	if err != nil {
		return ClosureView{}, err
	}
	if resend && !linked {
		return ClosureView{}, errClosureState("completed, but the store has no active owner to send the link to")
	}
	return s.Closure(ctx, tenantID)
}

// ClosureExportURL returns a short-lived download address of the final export a closure export
// link of the tenant opens. The link needs no session, since the store may be closed: an
// unknown, replaced or expired link is 404 closure_link_not_found, an export that is not (or no
// longer) downloadable is 409 export_not_ready.
func (s *Service) ClosureExportURL(ctx context.Context, tenantID uuid.UUID, token string) (string, error) {
	if s.objects == nil {
		return "", errors.New("saas: object store is not configured")
	}
	tokens, err := s.closureTokens()
	if err != nil {
		return "", err
	}
	exportID, err := tokens.ResolveExportLink(ctx, tenantID, token)
	if err != nil {
		return "", err
	}
	var key, fileName string
	err = s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var job struct {
			Status       string     `db:"status"`
			ExpiresAt    *time.Time `db:"expires_at"`
			AttachmentID *uuid.UUID `db:"attachment_id"`
		}
		err := sqlx.GetContext(ctx, q, &job, `SELECT status, expires_at, attachment_id FROM export_jobs
			WHERE id = $1 AND kind = $2`, exportID, DataExportKind)
		if db.IsNoRows(err) {
			return errClosureLinkNotFound()
		}
		if err != nil {
			return fmt.Errorf("saas: read closure export: %w", err)
		}
		e := ClosureExport{Status: job.Status, ExpiresAt: job.ExpiresAt}
		if job.AttachmentID == nil || !e.downloadable(s.clock.Now()) {
			return apperr.NewConflict(CodeExportNotReady, "The export cannot be downloaded")
		}
		if err := q.QueryRowxContext(ctx, `SELECT storage_key, file_name FROM attachments WHERE id = $1`,
			*job.AttachmentID).Scan(&key, &fileName); err != nil {
			return fmt.Errorf("saas: read closure export file: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	url, err := s.objects.PresignGet(ctx, key, fileName, closureDownloadTTL)
	if err != nil {
		return "", fmt.Errorf("saas: sign closure export download: %w", err)
	}
	return url, nil
}
