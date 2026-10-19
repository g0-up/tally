package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// Error codes of the notification centre.
const (
	CodeResolved = "notification_resolved"
)

// maxSnooze bounds how far ahead a notification may be snoozed.
const maxSnooze = 30 * 24 * time.Hour

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	// Producers are the producers the sweep runs; nil runs DefaultProducers.
	Producers *ProducerRegistry
}

// Service is the notification centre: the inbox, its read/resolve/snooze commands, the sweep and
// the outbox consumers.
type Service struct {
	db        *db.DB
	clock     clock.Clock
	producers *ProducerRegistry
}

// NewService returns the service.
func NewService(d Deps) *Service {
	producers := d.Producers
	if producers == nil {
		producers = DefaultProducers()
	}
	return &Service{db: d.DB, clock: d.Clock, producers: producers}
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// Cursor is the position after the last row of an inbox page (newest first).
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// stateFilter is the WHERE clause of an inbox state; $1 is now.
func stateFilter(state string) string {
	switch state {
	case StateUnread:
		return `resolved_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= $1) AND read_at IS NULL`
	case StateSnoozed:
		return `resolved_at IS NULL AND snoozed_until > $1`
	case StateResolved:
		return `resolved_at IS NOT NULL AND $1::timestamptz IS NOT NULL`
	case StateAll:
		return `$1::timestamptz IS NOT NULL`
	default:
		return `resolved_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= $1)`
	}
}

// List pages the inbox of state, newest first. An empty state is StateActive.
func (s *Service) List(ctx context.Context, scope Scope, state string, after *Cursor, limit int) ([]Notification, error) {
	if state == "" {
		state = StateActive
	}
	if !validState(state) {
		return nil, apperr.NewValidation("validation_failed", "Some fields are not valid", map[string]string{"state": "invalid"})
	}
	var rows []Notification
	err := s.read(ctx, scope, func(q db.Querier) error {
		args := []any{s.clock.Now(), limit}
		where := stateFilter(state)
		if after != nil {
			where += ` AND (created_at, id) < ($3, $4)`
			args = append(args, after.CreatedAt, after.ID)
		}
		if err := sqlx.SelectContext(ctx, q, &rows, `SELECT `+notificationColumns+` FROM notifications WHERE `+where+`
			ORDER BY created_at DESC, id DESC LIMIT $2`, args...); err != nil {
			return fmt.Errorf("notification: list: %w", err)
		}
		return nil
	})
	return rows, err
}

// UnreadCount counts the active notifications not read yet.
func (s *Service) UnreadCount(ctx context.Context, scope Scope) (int, error) {
	var n int
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM notifications WHERE `+stateFilter(StateUnread), s.clock.Now()).Scan(&n); err != nil {
			return fmt.Errorf("notification: count unread: %w", err)
		}
		return nil
	})
	return n, err
}

// MarkRead marks a notification read; reading it again keeps the first read time.
func (s *Service) MarkRead(ctx context.Context, actor audit.Actor, id uuid.UUID) (Notification, error) {
	return s.update(ctx, actor, id, func(_ Notification, now time.Time) (string, []any, error) {
		return `read_at = COALESCE(read_at, $2)`, []any{now}, nil
	})
}

// Resolve closes a notification for the owner. It stays closed while its condition holds; the
// condition clearing and coming back opens it again.
func (s *Service) Resolve(ctx context.Context, actor audit.Actor, id uuid.UUID) (Notification, error) {
	return s.update(ctx, actor, id, func(n Notification, now time.Time) (string, []any, error) {
		if n.ResolvedAt != nil {
			return "", nil, nil
		}
		return `resolved_at = $2, resolved_by = 'user', read_at = COALESCE(read_at, $2)`, []any{now}, nil
	})
}

// Snooze hides an open notification from the active inbox until until.
func (s *Service) Snooze(ctx context.Context, actor audit.Actor, id uuid.UUID, until time.Time) (Notification, error) {
	return s.update(ctx, actor, id, func(n Notification, now time.Time) (string, []any, error) {
		if !until.After(now) || until.Sub(now) > maxSnooze {
			return "", nil, apperr.NewValidation("validation_failed", "Snooze until must be within the next 30 days",
				map[string]string{"until": "out_of_range"})
		}
		if n.ResolvedAt != nil {
			return "", nil, apperr.NewConflict(CodeResolved, "The notification is already resolved")
		}
		return `snoozed_until = $3`, []any{now, until}, nil
	})
}

// update locks the notification, asks change for the SET clause and its arguments ($2 on) and
// applies it; an empty clause leaves the row as it is.
func (s *Service) update(ctx context.Context, actor audit.Actor, id uuid.UUID,
	change func(n Notification, now time.Time) (string, []any, error)) (Notification, error) {
	var out Notification
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `SELECT `+notificationColumns+` FROM notifications WHERE id = $1 FOR UPDATE`, id).StructScan(&out)
		if db.IsNoRows(err) {
			return apperr.NewNotFound("not_found", "notification not found")
		}
		if err != nil {
			return fmt.Errorf("notification: read: %w", err)
		}
		set, args, err := change(out, s.clock.Now())
		if err != nil || set == "" {
			return err
		}
		if err := q.QueryRowxContext(ctx, `UPDATE notifications SET `+set+`, updated_at = $2 WHERE id = $1 RETURNING `+notificationColumns,
			append([]any{id}, args...)...).StructScan(&out); err != nil {
			return fmt.Errorf("notification: update: %w", err)
		}
		return nil
	})
	return out, err
}

// sweepTime reads the tenant's time zone in the tenant transaction q.
func sweepTime(ctx context.Context, q db.Querier, now time.Time) (SweepTime, error) {
	var name string
	if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = current_setting('app.tenant_id')::uuid`).Scan(&name); err != nil {
		return SweepTime{}, fmt.Errorf("notification: read tenant time zone: %w", err)
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return SweepTime{}, err
	}
	return SweepTime{Now: now, Today: clock.BusinessDate(now, loc), Loc: loc}, nil
}

// Sweep runs every producer for the tenant in one transaction: it raises each condition that
// holds and clears the notifications whose condition no longer does. Running it twice changes
// nothing but updated_at. It returns how many conditions hold.
func (s *Service) Sweep(ctx context.Context, tenantID uuid.UUID) (int, error) {
	n := 0
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		n = 0
		at, err := sweepTime(ctx, q, s.clock.Now())
		if err != nil {
			return err
		}
		for _, p := range s.producers.Producers() {
			notices, err := p.Sweep(ctx, q, at)
			if err != nil {
				return err
			}
			current := map[string][]string{}
			for _, k := range p.Kinds() {
				current[k] = []string{}
			}
			for _, notice := range notices {
				keys, ok := current[notice.Kind]
				if !ok {
					return fmt.Errorf("notification: producer of %v returned kind %q", p.Kinds(), notice.Kind)
				}
				if err := Raise(ctx, q, notice, at.Now); err != nil {
					return err
				}
				current[notice.Kind] = append(keys, notice.DedupeKey)
			}
			for kind, keys := range current {
				if err := clearAbsent(ctx, q, kind, keys, at.Now); err != nil {
					return err
				}
			}
			n += len(notices)
		}
		return nil
	})
	return n, err
}

// reconcile raises notices and clears the candidate keys not among them: the targeted form of the
// sweep an outbox consumer runs for one subject.
func reconcile(ctx context.Context, q db.Querier, now time.Time, candidates []string, notices []Notice) error {
	raised := map[string]bool{}
	for _, n := range notices {
		if err := Raise(ctx, q, n, now); err != nil {
			return err
		}
		raised[n.DedupeKey] = true
	}
	var cleared []string
	for _, k := range candidates {
		if !raised[k] {
			cleared = append(cleared, k)
		}
	}
	return Clear(ctx, q, cleared, now)
}
