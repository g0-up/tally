package org

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// AuditFilter narrows the audit log. From and To are business dates (inclusive) in the tenant's
// time zone; zero values do not filter.
type AuditFilter struct {
	EntityType string
	EntityID   uuid.UUID
	ActorID    uuid.UUID
	Action     string
	From       time.Time
	To         time.Time
	// Query matches part of the document number, the action, the entity type or the name of the
	// store's user who acted (without diacritics or case); empty does not filter.
	Query string
}

// AuditQueryMaxLen is the longest search text of the audit log, in characters.
const AuditQueryMaxLen = 100

const auditEventColumns = `id, tenant_id, actor_type, actor_id, action, entity_type, entity_id, doc_no, before, after,
	source, request_id, host(ip) AS ip, created_at`

// AuditEvents returns one page of the audit log, newest first. Snapshots were redacted when the
// events were recorded.
func (s *Service) AuditEvents(ctx context.Context, scope Scope, f AuditFilter, page httpx.Page) ([]AuditEvent, error) {
	if !f.From.IsZero() && !f.To.IsZero() && f.To.Before(f.From) {
		return nil, errInvalid(map[string]string{"to": "before_from"})
	}
	var loc *time.Location
	if !f.From.IsZero() || !f.To.IsZero() {
		tenant, err := s.Tenant(ctx, scope.TenantID)
		if err != nil {
			return nil, err
		}
		if loc, err = tenantLocation(tenant); err != nil {
			return nil, err
		}
	}
	conds := []string{"tenant_id = $1"}
	args := []any{scope.TenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.EntityType != "" {
		add("entity_type = ?", f.EntityType)
	}
	if f.EntityID != uuid.Nil {
		add("entity_id = ?", f.EntityID)
	}
	if f.ActorID != uuid.Nil {
		add("actor_id = ?", f.ActorID)
	}
	if f.Action != "" {
		add("action = ?", f.Action)
	}
	if !f.From.IsZero() {
		add("created_at >= ?", startOfDay(f.From, loc))
	}
	if !f.To.IsZero() {
		add("created_at < ?", startOfDay(f.To.AddDate(0, 0, 1), loc))
	}
	actorsAt := -1
	if f.Query != "" {
		// The text matches anywhere in the field, so no b-tree index serves it: the search walks
		// the tenant's log newest first on ix_audit_events__time, like the rest of this query.
		args = append(args, likePattern(f.Query), nil)
		actorsAt = len(args) - 1
		conds = append(conds, fmt.Sprintf(`(doc_no ILIKE $%[1]d OR action ILIKE $%[1]d OR entity_type ILIKE $%[1]d
			OR actor_id = ANY($%[2]d))`, len(args)-1, len(args)))
	}
	if page.After != nil {
		after, err := time.Parse(time.RFC3339Nano, page.After.SortKey)
		if err != nil {
			return nil, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
		}
		args = append(args, after, page.After.ID)
		conds = append(conds, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, page.FetchLimit())
	query := `SELECT ` + auditEventColumns + ` FROM audit_events WHERE ` + strings.Join(conds, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))

	if actorsAt >= 0 {
		// tally_readonly cannot read users, so a support session reads the names in an ordinary
		// tenant transaction; it reads nothing but the names.
		err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
			actors, err := usersNamed(ctx, q, scope.TenantID, f.Query)
			args[actorsAt] = actors
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	var out []AuditEvent
	err := s.read(ctx, scope, func(q db.Querier) error {
		rows, err := q.QueryxContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("org: list audit events: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var ev AuditEvent
			if err := rows.StructScan(&ev); err != nil {
				return fmt.Errorf("org: scan audit event: %w", err)
			}
			out = append(out, ev)
		}
		return rows.Err()
	})
	return out, err
}

// usersNamed returns the ids of the tenant's users whose name contains text, compared without
// diacritics or case. A store has a handful of users (its owner and earlier owners), so the
// names are matched here rather than in SQL, which cannot fold diacritics without an extension.
func usersNamed(ctx context.Context, q db.Querier, tenantID uuid.UUID, text string) ([]uuid.UUID, error) {
	var users []struct {
		ID       uuid.UUID `db:"id"`
		FullName string    `db:"full_name"`
	}
	if err := sqlx.SelectContext(ctx, q, &users, `SELECT id, full_name FROM users WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("org: read users for the audit search: %w", err)
	}
	want := textnorm.SearchNorm(text)
	out := []uuid.UUID{}
	for _, u := range users {
		if want != "" && strings.Contains(textnorm.SearchNorm(u.FullName), want) {
			out = append(out, u.ID)
		}
	}
	return out, nil
}

// likePattern is a case-insensitive substring pattern of s with LIKE wildcards escaped.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// AuditEvent returns one event of the tenant; another tenant's id answers 404.
func (s *Service) AuditEvent(ctx context.Context, scope Scope, id uuid.UUID) (AuditEvent, error) {
	var ev AuditEvent
	err := s.read(ctx, scope, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `SELECT `+auditEventColumns+` FROM audit_events WHERE tenant_id = $1 AND id = $2`,
			scope.TenantID, id).StructScan(&ev)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("org: read audit event: %w", err)
		}
		return nil
	})
	return ev, err
}

// AuditEventCursor is the keyset cursor of an audit event.
func AuditEventCursor(ev AuditEvent) httpx.Cursor {
	return httpx.Cursor{SortKey: ev.CreatedAt.UTC().Format(time.RFC3339Nano), ID: ev.ID}
}

// startOfDay is midnight of civil date d (as returned by clock.Date) in loc.
func startOfDay(d time.Time, loc *time.Location) time.Time {
	y, m, day := d.Date()
	return time.Date(y, m, day, 0, 0, 0, 0, loc)
}
