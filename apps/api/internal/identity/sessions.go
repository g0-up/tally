package identity

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
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// tokenLen is the length of an authn.NewToken value; other cookie values are never looked up.
const tokenLen = 43

// ResolveSession implements middleware.SessionResolver: the token must belong to an open session
// (not revoked, not past its absolute expiry, used within the idle timeout) of an active owner of
// a tenant that is neither closed nor replaced (a reset sandbox whose account moved on). last_seen_at
// moves at most once a minute.
func (s *Service) ResolveSession(ctx context.Context, token string) (middleware.Principal, bool, error) {
	if len(token) != tokenLen {
		return middleware.Principal{}, false, nil
	}
	now := s.clock.Now()
	var row struct {
		ID                uuid.UUID  `db:"id"`
		TenantID          uuid.UUID  `db:"tenant_id"`
		UserID            uuid.UUID  `db:"user_id"`
		LastSeenAt        time.Time  `db:"last_seen_at"`
		MFAVerifiedAt     *time.Time `db:"mfa_verified_at"`
		ReauthenticatedAt *time.Time `db:"reauthenticated_at"`
		IsSandbox         bool       `db:"is_sandbox"`
	}
	found := false
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			SELECT s.id, s.tenant_id, s.user_id, s.last_seen_at, s.mfa_verified_at, s.reauthenticated_at, t.is_sandbox
			FROM sessions s
			JOIN users u ON u.tenant_id = s.tenant_id AND u.id = s.user_id
			JOIN tenants t ON t.id = s.tenant_id
			WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2 AND s.last_seen_at > $3
			  AND u.status = $4 AND t.status NOT IN ('closed', 'replaced')`,
			s.keys.Tokens.Sum(token), now, now.Add(-SessionIdleTTL), StatusActive).StructScan(&row)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: resolve session: %w", err)
		}
		found = true
		if now.Sub(row.LastSeenAt) < lastSeenResolution {
			return nil
		}
		if _, err := q.ExecContext(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`, row.ID, now); err != nil {
			return fmt.Errorf("identity: touch session: %w", err)
		}
		return nil
	})
	if err != nil || !found {
		return middleware.Principal{}, false, err
	}
	return middleware.Principal{
		Kind: middleware.PrincipalOwner, SessionID: row.ID, UserID: row.UserID, TenantID: row.TenantID,
		ReauthenticatedAt: row.ReauthenticatedAt, MfaVerifiedAt: row.MFAVerifiedAt, IsSandbox: row.IsSandbox,
	}, true, nil
}

// ResolveSlug implements middleware.TenantHostResolver. A replaced sandbox answers to no host: its
// address belongs to the tenant that replaced it.
func (s *Service) ResolveSlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	found := false
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `SELECT id FROM tenants WHERE slug = $1 AND status <> 'replaced'`, slug).Scan(&id)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: resolve tenant host: %w", err)
		}
		found = true
		return nil
	})
	return id, found, err
}

// Sessions returns the open sessions of the signed-in owner, newest activity first.
func (s *Service) Sessions(ctx context.Context, p middleware.Principal) ([]Session, error) {
	now := s.clock.Now()
	var out []Session
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+sessionColumns+` FROM sessions
			WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL AND expires_at > $3 AND last_seen_at > $4
			ORDER BY last_seen_at DESC, id`, p.TenantID, p.UserID, now, now.Add(-SessionIdleTTL)); err != nil {
			return fmt.Errorf("identity: list sessions: %w", err)
		}
		return nil
	})
	return out, err
}

// RevokeSession signs out one session of the owner (404 when it is not an open session of theirs).
func (s *Service) RevokeSession(ctx context.Context, p middleware.Principal, sessionID uuid.UUID, c Client) error {
	now := s.clock.Now()
	return s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		found, err := revokeSession(ctx, q, p.TenantID, p.UserID, sessionID, "revoked_by_owner", now)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NewNotFound("not_found", "Not found")
		}
		return s.audit.Record(ctx, q, ownerActor(p.TenantID, p.UserID, c), audit.Event{
			Action: "session.revoke", EntityType: "session", EntityID: sessionID,
		})
	})
}

// RevokeOtherSessions signs out every session of the owner except the current one.
func (s *Service) RevokeOtherSessions(ctx context.Context, p middleware.Principal, c Client) (int64, error) {
	now := s.clock.Now()
	var n int64
	err := s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		var err error
		if n, err = revokeSessions(ctx, q, p.TenantID, p.UserID, p.SessionID, "revoked_by_owner", now); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, ownerActor(p.TenantID, p.UserID, c), audit.Event{
			Action: "session.revoke", EntityType: "user", EntityID: p.UserID, After: map[string]any{"count": n, "scope": "others"},
		})
	})
	return n, err
}

// RevokeTenantSessions signs out every session of a tenant (operator command
// `tally maintenance revoke-sessions`).
func (s *Service) RevokeTenantSessions(ctx context.Context, tenantID uuid.UUID, reason string) (int64, error) {
	now := s.clock.Now()
	var n int64
	err := s.withAccountTx(ctx, tenantID, func(q db.Querier) error {
		res, err := q.ExecContext(ctx, `
			UPDATE sessions SET revoked_at = $2, revoked_reason = $3 WHERE tenant_id = $1 AND revoked_at IS NULL`,
			tenantID, now, reason)
		if err != nil {
			return fmt.Errorf("identity: revoke tenant sessions: %w", err)
		}
		if n, err = res.RowsAffected(); err != nil {
			return fmt.Errorf("identity: revoke tenant sessions: %w", err)
		}
		return s.audit.Record(ctx, q, audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: audit.SourceCLI}, audit.Event{
			Action: "session.revoke", EntityType: "tenant", EntityID: tenantID,
			After: map[string]any{"count": n, "scope": "tenant", "reason": reason},
		})
	})
	return n, err
}

// Me is the signed-in owner with their store.
type Me struct {
	User    User
	Tenant  TenantInfo
	Session Session
	// ServerTime is the server's clock in the store's time zone, so a browser with a wrong clock
	// or zone still dates documents and reports by the store's day.
	ServerTime time.Time
}

// Me returns the owner, store and session of a principal.
func (s *Service) Me(ctx context.Context, p middleware.Principal) (Me, error) {
	var out Me
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		u, found, err := userByID(ctx, q, p.TenantID, p.UserID, false)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NewUnauthenticated()
		}
		out.User = u
		if out.Tenant, err = readTenantInfo(ctx, q, p.TenantID); err != nil {
			return err
		}
		if err := q.QueryRowxContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE tenant_id = $1 AND id = $2`,
			p.TenantID, p.SessionID).StructScan(&out.Session); err != nil {
			return fmt.Errorf("identity: read session: %w", err)
		}
		loc, err := clock.LoadLocation(out.Tenant.Timezone)
		if err != nil {
			return fmt.Errorf("identity: tenant time zone: %w", err)
		}
		out.ServerTime = s.clock.Now().In(loc)
		return nil
	})
	return out, err
}
