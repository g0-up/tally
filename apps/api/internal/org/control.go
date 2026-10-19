package org

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// PeriodLockDocType is the document type a period_reopen confirmation is bound to; its document
// id is the tenant id and its version the number of period lock events (PeriodLocks.Version).
const PeriodLockDocType = "period_lock"

const maxReasonLen = 500

// PeriodLocks is the current lock and one page of the lock history (newest first).
type PeriodLocks struct {
	// LockedThrough is the effective lock date (inclusive), nil when nothing was ever locked.
	LockedThrough *time.Time
	// Version counts the lock events; a period_reopen confirmation must carry it.
	Version int
	Events  []PeriodLockEvent
	Page    httpx.Page
}

const periodLockColumns = `id, tenant_id, action, locked_through, reason, actor_id, created_at`

// latestLock returns the newest event and the number of events.
func latestLock(ctx context.Context, q db.Querier, tenantID uuid.UUID) (*PeriodLockEvent, int, error) {
	var count int
	if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM period_locks WHERE tenant_id = $1`, tenantID).Scan(&count); err != nil {
		return nil, 0, fmt.Errorf("org: count period locks: %w", err)
	}
	if count == 0 {
		return nil, 0, nil
	}
	var ev PeriodLockEvent
	if err := q.QueryRowxContext(ctx, `SELECT `+periodLockColumns+` FROM period_locks WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, tenantID).StructScan(&ev); err != nil {
		return nil, 0, fmt.Errorf("org: read period lock: %w", err)
	}
	return &ev, count, nil
}

// PeriodLocks returns the current lock and a page of its history.
func (s *Service) PeriodLocks(ctx context.Context, scope Scope, page httpx.Page) (PeriodLocks, error) {
	out := PeriodLocks{Page: page}
	err := s.read(ctx, scope, func(q db.Querier) error {
		latest, count, err := latestLock(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		out.Version = count
		if latest != nil {
			out.LockedThrough = latest.LockedThrough
		}
		args := []any{scope.TenantID, page.FetchLimit()}
		where := ""
		if page.After != nil {
			after, err := time.Parse(time.RFC3339Nano, page.After.SortKey)
			if err != nil {
				return apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
			}
			args = append(args, after, page.After.ID)
			where = ` AND (created_at, id) < ($3, $4)`
		}
		rows, err := q.QueryxContext(ctx, `SELECT `+periodLockColumns+` FROM period_locks WHERE tenant_id = $1`+where+`
			ORDER BY created_at DESC, id DESC LIMIT $2`, args...)
		if err != nil {
			return fmt.Errorf("org: list period locks: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var ev PeriodLockEvent
			if err := rows.StructScan(&ev); err != nil {
				return fmt.Errorf("org: scan period lock: %w", err)
			}
			out.Events = append(out.Events, ev)
		}
		return rows.Err()
	})
	return out, err
}

// PeriodLockCursor is the keyset cursor of a lock event.
func PeriodLockCursor(ev PeriodLockEvent) httpx.Cursor {
	return httpx.Cursor{SortKey: ev.CreatedAt.UTC().Format(time.RFC3339Nano), ID: ev.ID}
}

// LockPeriod locks every business date up to and including through. The lock only moves
// forward, never past today in the tenant's time zone. The tenant row is taken FOR UPDATE, so
// the lock waits for in-flight postings (which hold FOR KEY SHARE through periodlock.Guard).
func (s *Service) LockPeriod(ctx context.Context, actor audit.Actor, through time.Time) (PeriodLockEvent, error) {
	through = clock.TruncateDate(through)
	var ev PeriodLockEvent
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		tenant, err := readTenant(ctx, q, actor.TenantID, true)
		if err != nil {
			return err
		}
		loc, err := tenantLocation(tenant)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if through.After(clock.BusinessDate(now, loc)) {
			return apperr.NewValidation("lock_date_in_future", "A period can only be locked up to today",
				map[string]string{"locked_through": "in_future"})
		}
		latest, _, err := latestLock(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		current := currentLock(latest)
		if current != nil && !through.After(*current) {
			return apperr.NewValidation("lock_not_forward", "The new lock date must be after the current one",
				map[string]string{"locked_through": "not_after_current"})
		}
		ev, err = s.insertLock(ctx, q, actor, current, through, now)
		return err
	})
	return ev, err
}

// insertLock appends the lock event moving the lock from current to through and audits it, in
// the caller's transaction (which holds the tenant row FOR UPDATE).
func (s *Service) insertLock(ctx context.Context, q db.Querier, actor audit.Actor, current *time.Time, through, now time.Time) (PeriodLockEvent, error) {
	ev := PeriodLockEvent{
		ID: ids.New(), TenantID: actor.TenantID, Action: PeriodLockActionLock, LockedThrough: &through,
		ActorID: actor.ID, CreatedAt: now,
	}
	if err := insertLockEvent(ctx, q, ev); err != nil {
		return PeriodLockEvent{}, err
	}
	err := s.audit.Record(ctx, q, actor, audit.Event{
		Action: "period.lock", EntityType: PeriodLockDocType, EntityID: ev.ID,
		Before: map[string]any{"locked_through": dateString(current)},
		After:  map[string]any{"locked_through": dateString(&through)},
	})
	return ev, err
}

// ReopenInput reopens part of a locked period, or all of it.
type ReopenInput struct {
	// LockedThrough is the new, earlier lock date; nil unlocks every period.
	LockedThrough  *time.Time
	Reason         string
	ConfirmationID uuid.UUID
	// PayloadHash is exception.PayloadHash of the request body.
	PayloadHash []byte
}

// ReopenPeriod moves the lock back to in.LockedThrough, or removes it when that is nil. It needs
// a reason and a period_reopen exception confirmation bound to the current lock version and to
// this exact request.
func (s *Service) ReopenPeriod(ctx context.Context, actor audit.Actor, in ReopenInput) (PeriodLockEvent, error) {
	if in.LockedThrough != nil {
		through := clock.TruncateDate(*in.LockedThrough)
		in.LockedThrough = &through
	}
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case in.Reason == "":
		return PeriodLockEvent{}, errInvalid(map[string]string{"reason": "required"})
	case utf8.RuneCountInString(in.Reason) > maxReasonLen:
		return PeriodLockEvent{}, errInvalid(map[string]string{"reason": "too_long"})
	}
	var ev PeriodLockEvent
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if _, err := readTenant(ctx, q, actor.TenantID, true); err != nil {
			return err
		}
		latest, count, err := latestLock(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		current := currentLock(latest)
		if current == nil {
			return apperr.NewConflict("period_not_locked", "No period is locked")
		}
		if in.LockedThrough != nil && !in.LockedThrough.Before(*current) {
			return apperr.NewValidation("reopen_not_backward", "The new lock date must be before the current one",
				map[string]string{"locked_through": "not_before_current"})
		}
		reason := in.Reason
		ev = PeriodLockEvent{
			ID: ids.New(), TenantID: actor.TenantID, Action: PeriodLockActionReopen, LockedThrough: in.LockedThrough,
			Reason: &reason, ActorID: actor.ID, CreatedAt: s.clock.Now(),
		}
		if err := s.exceptions.Consume(ctx, q, actor.TenantID, exception.Expectation{
			ConfirmationID: in.ConfirmationID, Kind: exception.PeriodReopen,
			DocType: PeriodLockDocType, DocID: actor.TenantID, DocVersion: count, PayloadHash: in.PayloadHash,
			UsedByDocType: PeriodLockDocType, UsedByDocID: ev.ID,
		}); err != nil {
			return err
		}
		if err := insertLockEvent(ctx, q, ev); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "period.reopen", EntityType: PeriodLockDocType, EntityID: ev.ID,
			Before: map[string]any{"locked_through": dateString(current)},
			After: map[string]any{
				"locked_through": dateString(in.LockedThrough), "reason": in.Reason,
				"exception_confirmation_id": in.ConfirmationID,
			},
		})
	})
	return ev, err
}

// currentLock is the lock date in effect after latest, nil when nothing is locked.
func currentLock(latest *PeriodLockEvent) *time.Time {
	if latest == nil {
		return nil
	}
	return latest.LockedThrough
}

func insertLockEvent(ctx context.Context, q db.Querier, ev PeriodLockEvent) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO period_locks (id, tenant_id, action, locked_through, reason, actor_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.ID, ev.TenantID, ev.Action, ev.LockedThrough, ev.Reason, ev.ActorID, ev.CreatedAt); err != nil {
		return fmt.Errorf("org: insert period lock: %w", err)
	}
	return nil
}

// ConfirmInput is what the owner confirms.
type ConfirmInput struct {
	Kind        exception.Kind
	DocType     string
	DocID       uuid.UUID
	DocVersion  int
	PayloadHash []byte
	Impact      json.RawMessage
	Note        string
}

// ConfirmException stores an exception confirmation the next command may consume once.
func (s *Service) ConfirmException(ctx context.Context, actor audit.Actor, in ConfirmInput) (exception.Confirmation, error) {
	var c exception.Confirmation
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		var err error
		c, err = s.exceptions.Create(ctx, q, exception.NewConfirmation{
			TenantID: actor.TenantID, Kind: in.Kind, DocType: in.DocType, DocID: in.DocID, DocVersion: in.DocVersion,
			PayloadHash: in.PayloadHash, Impact: in.Impact, Note: strings.TrimSpace(in.Note), ConfirmedBy: actor.ID,
		})
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "exception.confirm", EntityType: "exception_confirmation", EntityID: c.ID,
			After: map[string]any{
				"kind": c.Kind, "doc_type": c.DocType, "doc_id": c.DocID, "doc_version": c.DocVersion,
				"impact": c.Impact, "note": c.Note,
			},
		})
	})
	return c, err
}
