package support

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// NewSession is the owner's grant of a support session for a ticket.
type NewSession struct {
	TicketID uuid.UUID
	Modules  []string
	// Hours is the session length, 1 to MaxSessionHours; 0 means DefaultSessionHours.
	Hours int
}

func (in *NewSession) validate() error {
	fields := map[string]string{}
	if in.TicketID == uuid.Nil {
		fields["ticket_id"] = "required"
	}
	if len(in.Modules) == 0 {
		fields["modules"] = "required"
	}
	for _, m := range in.Modules {
		if _, ok := ViewModules[m]; !ok {
			fields["modules"] = "invalid"
		}
	}
	if in.Hours == 0 {
		in.Hours = DefaultSessionHours
	}
	if in.Hours < 1 || in.Hours > MaxSessionHours {
		fields["hours"] = "invalid"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

func sortedModules(in []string) []string {
	out := []string{}
	for _, m := range in {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	slices.Sort(out)
	return out
}

func sessionSnapshot(s Session) map[string]any {
	return map[string]any{
		"admin_id": s.AdminID, "ticket_id": s.TicketID, "scope": s.Scope, "modules": []string(s.Modules),
		"expires_at": s.ExpiresAt.UTC(),
	}
}

// GrantSession lets the admin assigned to one of the store's open tickets read the chosen modules
// until the session expires. The consent token and the session commit together; the caller
// passed the re-authentication guard.
func (s *Service) GrantSession(ctx context.Context, p middleware.Principal, actor audit.Actor, in NewSession) (Session, error) {
	if err := requireOwner(actor); err != nil {
		return Session{}, err
	}
	if s.consent == nil {
		return Session{}, fmt.Errorf("support: grant session: consent is not configured")
	}
	if err := in.validate(); err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	expires := now.Add(time.Duration(in.Hours) * time.Hour)
	var out Session
	_, err := s.consent.GrantSupportConsent(ctx, p, expires, func(q db.Querier, tokenID uuid.UUID) error {
		t, err := readTicket(ctx, q, actor.TenantID, in.TicketID, true)
		if err != nil {
			return err
		}
		if t.Status == TicketClosed {
			return errTicketClosed()
		}
		if t.AssignedAdminID == nil {
			return apperr.NewConflict(CodeNoAssignedAdmin, "No support agent is assigned to the ticket yet")
		}
		ticketID := t.ID
		out = Session{
			ID: ids.New(), TenantID: actor.TenantID, AdminID: *t.AssignedAdminID, TicketID: &ticketID, Scope: "read_only",
			Modules: sortedModules(in.Modules), ConsentTokenID: tokenID, GrantedAt: now, ExpiresAt: expires,
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO support_sessions (id, tenant_id, admin_id, ticket_id, scope, modules, consent_token_id, granted_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			out.ID, out.TenantID, out.AdminID, out.TicketID, out.Scope, []string(out.Modules), out.ConsentTokenID,
			out.GrantedAt, out.ExpiresAt); err != nil {
			return fmt.Errorf("support: insert session: %w", err)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "support_session.grant", EntityType: "support_session", EntityID: out.ID, DocNo: t.Number,
			After: sessionSnapshot(out),
		})
	})
	if err != nil {
		return Session{}, err
	}
	return out, nil
}

// Sessions lists the store's support sessions, newest first.
func (s *Service) Sessions(ctx context.Context, tenantID uuid.UUID) ([]Session, error) {
	out := []Session{}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT `+sessionColumns+` FROM support_sessions WHERE tenant_id = $1 ORDER BY granted_at DESC, id DESC LIMIT 200`, tenantID)
	})
	if err != nil {
		return nil, fmt.Errorf("support: list sessions: %w", err)
	}
	return out, nil
}

// RevokeSession ends a session at once and uses up its consent token. Revoking an ended session
// answers it unchanged.
func (s *Service) RevokeSession(ctx context.Context, p middleware.Principal, actor audit.Actor, id uuid.UUID) (Session, error) {
	if err := requireOwner(actor); err != nil {
		return Session{}, err
	}
	if s.consent == nil {
		return Session{}, fmt.Errorf("support: revoke session: consent is not configured")
	}
	var cur Session
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &cur, `SELECT `+sessionColumns+` FROM support_sessions WHERE id = $1 AND tenant_id = $2`,
			id, actor.TenantID)
		if db.IsNoRows(err) {
			return apperr.NewNotFound("not_found", "Support session not found")
		}
		return err
	})
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	out := cur
	err = s.consent.RevokeSupportConsent(ctx, p, cur.ConsentTokenID, func(q db.Querier) error {
		var revokedAt *time.Time
		err := q.QueryRowxContext(ctx, `
			UPDATE support_sessions SET revoked_at = COALESCE(revoked_at, $3)
			WHERE id = $1 AND tenant_id = $2 RETURNING revoked_at`, id, actor.TenantID, now).Scan(&revokedAt)
		if err != nil {
			return fmt.Errorf("support: revoke session: %w", err)
		}
		out.RevokedAt = revokedAt
		if cur.RevokedAt != nil {
			return nil
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "support_session.revoke", EntityType: "support_session", EntityID: id, After: sessionSnapshot(out),
		})
	})
	if err != nil {
		return Session{}, err
	}
	return out, nil
}

// AdminSessions lists the support sessions granted to admin, active ones first.
func (s *Service) AdminSessions(ctx context.Context, adminID uuid.UUID) ([]AdminSession, error) {
	out := []AdminSession{}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT s.id, s.tenant_id, s.admin_id, s.ticket_id, s.scope, to_jsonb(s.modules) AS modules, s.consent_token_id,
			  s.granted_at, s.expires_at, s.revoked_at, t.slug AS tenant_slug, t.display_name AS tenant_name
			FROM support_sessions s JOIN tenants t ON t.id = s.tenant_id
			WHERE s.admin_id = $1 AND t.status <> 'replaced'
			ORDER BY s.revoked_at IS NULL DESC, s.expires_at DESC LIMIT 200`, adminID)
	})
	if err != nil {
		return nil, fmt.Errorf("support: list admin sessions: %w", err)
	}
	return out, nil
}

// Access is one admin read through a support session.
type Access struct {
	AdminID   uuid.UUID
	TenantID  uuid.UUID
	Module    string
	Path      string // the tenant API path under /api/v1, without the query
	RequestID string
	IP        string
}

// Segment returns the first path segment of a.Path.
func (a Access) Segment() string {
	seg, _, _ := strings.Cut(strings.TrimPrefix(a.Path, "/"), "/")
	seg, _, _ = strings.Cut(seg, ":")
	return seg
}

// OpenSession is the session an access runs under, with the slug of its store.
type OpenSession struct {
	Session
	TenantSlug string `db:"tenant_slug"`
}

func errSessionInactive() error {
	return apperr.NewForbidden(CodeSessionInactive, "No active support session grants this part of the store")
}

// OpenAccess checks, at the moment of the request, that admin holds an unrevoked, unexpired
// session of the store granting the module, that the path belongs to it, that the owner's
// consent token is still unused and that the store is neither replaced nor closed; it then
// records the access in the store's audit log and returns the session. Anything else answers
// 403 support_session_inactive.
func (s *Service) OpenAccess(ctx context.Context, a Access) (OpenSession, error) {
	if _, ok := ViewModules[a.Module]; !ok || !ModuleOfSegment(a.Module, a.Segment()) {
		return OpenSession{}, errSessionInactive()
	}
	now := s.clock.Now()
	var out OpenSession
	err := s.db.WithTenantTx(ctx, a.TenantID, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &out, `
			SELECT s.id, s.tenant_id, s.admin_id, s.ticket_id, s.scope, to_jsonb(s.modules) AS modules, s.consent_token_id,
			  s.granted_at, s.expires_at, s.revoked_at, t.slug AS tenant_slug
			FROM support_sessions s
			JOIN tenants t ON t.id = s.tenant_id
			JOIN auth_tokens k ON k.id = s.consent_token_id
			WHERE s.tenant_id = $1 AND s.admin_id = $2 AND $3 = ANY (s.modules)
			  AND s.revoked_at IS NULL AND s.expires_at > $4
			  AND k.used_at IS NULL AND k.expires_at > $4 AND k.purpose = 'support_consent'
			  AND t.status NOT IN ('replaced', 'closed')
			ORDER BY s.expires_at DESC LIMIT 1`, a.TenantID, a.AdminID, a.Module, now)
		if db.IsNoRows(err) {
			return errSessionInactive()
		}
		if err != nil {
			return fmt.Errorf("support: read session: %w", err)
		}
		return s.audit.Record(ctx, q, audit.Actor{
			TenantID: a.TenantID, Type: audit.ActorSupport, ID: a.AdminID, Source: audit.SourceSupport,
			RequestID: a.RequestID, IP: a.IP,
		}, audit.Event{
			Action: "support_session.access", EntityType: "support_session", EntityID: out.ID,
			After: map[string]any{"module": a.Module, "path": a.Path},
		})
	})
	if err != nil {
		return OpenSession{}, err
	}
	return out, nil
}
