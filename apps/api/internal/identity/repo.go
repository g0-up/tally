package identity

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Data access of the identity tables. Every function takes the caller's transaction; none of
// these tables has row level security, so tenant_id is always filtered explicitly.

// userByEmail returns the user of tenantID whose e-mail key is emailKey.
func userByEmail(ctx context.Context, q db.Querier, tenantID uuid.UUID, emailKey string) (User, bool, error) {
	var u User
	err := q.QueryRowxContext(ctx, `SELECT `+userColumns+` FROM users WHERE tenant_id = $1 AND email_key = $2`,
		tenantID, emailKey).StructScan(&u)
	if db.IsNoRows(err) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("identity: read user: %w", err)
	}
	return u, true, nil
}

// userByPhone returns the one account of tenantID that is not disabled and has phone (the
// normalizePhone form). Nothing makes a phone unique, so two such accounts match neither: they
// sign in with their e-mail.
func userByPhone(ctx context.Context, q db.Querier, tenantID uuid.UUID, phone string) (User, bool, error) {
	var users []User
	if err := sqlx.SelectContext(ctx, q, &users, `SELECT `+userColumns+` FROM users
		WHERE tenant_id = $1 AND phone = $2 AND status <> $3 LIMIT 2`, tenantID, phone, StatusDisabled); err != nil {
		return User{}, false, fmt.Errorf("identity: read user by phone: %w", err)
	}
	if len(users) != 1 {
		return User{}, false, nil
	}
	return users[0], true, nil
}

// withAccountTx runs fn in a transaction of tenantID that changes (or reads ahead of changing) an
// account of the tenant, its sessions, its tokens or its MFA secrets and codes. Its first statement is lockTenant, so a sandbox
// reset and the change take the tenant row before any account row, in the same order: a change
// that locks first commits before the reset moves its rows; one that waits finds the tenant
// replaced and stops with sandbox_replaced instead of failing on the moved account's foreign keys.
//
// Every such transaction goes through here, except three that need no tenant lock:
//   - ResolveSession's touch and Logout update one session row by its id alone. They never wait
//     for the tenant row, so they cannot close a lock cycle with the reset, and the id follows the
//     row wherever the reset moves it.
//   - CreateTenantWithOwner writes the account of the tenant it creates itself.
//   - loginFailed writes a failed attempt (login_attempts has no foreign key) and an audit event,
//     no account row; a wrong password must answer invalid_credentials on any tenant.
func (s *Service) withAccountTx(ctx context.Context, tenantID uuid.UUID, fn func(q db.Querier) error) error {
	return s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if err := lockTenant(ctx, q, tenantID); err != nil {
			return err
		}
		return fn(q)
	})
}

// lockTenant takes a key-share lock on the tenant row and refuses a tenant a sandbox reset has
// replaced; withAccountTx is its only caller. An unknown tenant has nothing to lock; the caller
// answers as it does for an unknown account or token.
func lockTenant(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	var replaced bool
	err := q.QueryRowxContext(ctx, `SELECT status = 'replaced' FROM tenants WHERE id = $1 FOR KEY SHARE`, tenantID).Scan(&replaced)
	if db.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity: lock tenant: %w", err)
	}
	if replaced {
		return errSandboxReplaced()
	}
	return nil
}

// userByID returns a user of tenantID; forUpdate locks the row for the rest of the transaction.
func userByID(ctx context.Context, q db.Querier, tenantID, userID uuid.UUID, forUpdate bool) (User, bool, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE tenant_id = $1 AND id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var u User
	err := q.QueryRowxContext(ctx, query, tenantID, userID).StructScan(&u)
	if db.IsNoRows(err) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("identity: read user: %w", err)
	}
	return u, true, nil
}

// accountByEmail finds the account of an e-mail key in any store, with the store slug.
func accountByEmail(ctx context.Context, q db.Querier, emailKey string) (u User, slug string, found bool, err error) {
	row := struct {
		User
		Slug string `db:"slug"`
	}{}
	err = q.QueryRowxContext(ctx, `
		SELECT u.id, u.tenant_id, u.email, u.email_key, u.phone, u.full_name, u.password_hash, u.email_verified_at,
		       u.mfa_enabled, u.status, u.last_login_at, u.created_at, u.updated_at, u.version, t.slug
		FROM users u JOIN tenants t ON t.id = u.tenant_id
		WHERE u.email_key = $1`, emailKey).StructScan(&row)
	if db.IsNoRows(err) {
		return User{}, "", false, nil
	}
	if err != nil {
		return User{}, "", false, fmt.Errorf("identity: read account: %w", err)
	}
	return row.User, row.Slug, true, nil
}

// emailTaken reports whether any account (any store, any status) uses emailKey.
func emailTaken(ctx context.Context, q db.Querier, emailKey string) (bool, error) {
	var taken bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email_key = $1)`, emailKey).Scan(&taken); err != nil {
		return false, fmt.Errorf("identity: check e-mail: %w", err)
	}
	return taken, nil
}

func insertUser(ctx context.Context, q db.Querier, u User) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO users (id, tenant_id, email, email_key, phone, full_name, password_hash, email_verified_at, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		u.ID, u.TenantID, u.Email, u.EmailKey, u.Phone, u.FullName, u.PasswordHash, u.EmailVerifiedAt, u.Status, u.CreatedAt); err != nil {
		return fmt.Errorf("identity: insert user: %w", err)
	}
	return nil
}

// insertToken stores the keyed hash of a single-use token.
func insertToken(ctx context.Context, q db.Querier, tenantID, userID uuid.UUID, purpose string, hash []byte,
	payload []byte, expires, now time.Time,
) error {
	_, err := insertTokenID(ctx, q, tenantID, userID, purpose, hash, payload, expires, now)
	return err
}

// insertTokenID is insertToken returning the new token's id.
func insertTokenID(ctx context.Context, q db.Querier, tenantID, userID uuid.UUID, purpose string, hash []byte,
	payload []byte, expires, now time.Time,
) (uuid.UUID, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	id := ids.New()
	if _, err := q.ExecContext(ctx, `
		INSERT INTO auth_tokens (id, tenant_id, user_id, purpose, token_hash, payload, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenantID, userID, purpose, hash, payload, expires, now); err != nil {
		return uuid.Nil, fmt.Errorf("identity: insert %s token: %w", purpose, err)
	}
	return id, nil
}

// consumeToken marks an unused, unexpired token of tenantID used and returns it; a token that
// is unknown, of another purpose or store, used or expired yields found=false. The UPDATE makes
// concurrent uses of one token race on the row: only one of them sees it unused.
func consumeToken(ctx context.Context, q db.Querier, tenantID uuid.UUID, purpose string, hash []byte, now time.Time) (authToken, bool, error) {
	var t authToken
	err := q.QueryRowxContext(ctx, `
		UPDATE auth_tokens SET used_at = $4
		WHERE token_hash = $1 AND purpose = $2 AND tenant_id = $3 AND used_at IS NULL AND expires_at > $4
		RETURNING id, tenant_id, user_id, purpose, payload, expires_at, used_at`,
		hash, purpose, tenantID, now).StructScan(&t)
	if db.IsNoRows(err) {
		return authToken{}, false, nil
	}
	if err != nil {
		return authToken{}, false, fmt.Errorf("identity: consume %s token: %w", purpose, err)
	}
	return t, true, nil
}

// retireTokens marks every open token of a user and purpose used (a newer one replaces them).
func retireTokens(ctx context.Context, q db.Querier, tenantID, userID uuid.UUID, purpose string, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE auth_tokens SET used_at = $4
		WHERE tenant_id = $1 AND user_id = $2 AND purpose = $3 AND used_at IS NULL`,
		tenantID, userID, purpose, now); err != nil {
		return fmt.Errorf("identity: retire %s tokens: %w", purpose, err)
	}
	return nil
}

// ipParam turns a client address into an inet parameter (NULL when it is not an address).
func ipParam(ip string) any {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	return addr.Unmap().WithZone("").String()
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// insertSession stores a new session.
func insertSession(ctx context.Context, q db.Querier, s Session, tokenHash []byte) error {
	var ua, ip string
	if s.UserAgent != nil {
		ua = *s.UserAgent
	}
	if s.IP != nil {
		ip = *s.IP
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO sessions (id, tenant_id, user_id, token_hash, device_label, user_agent, ip, created_at, last_seen_at,
		                      expires_at, mfa_verified_at, reauthenticated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $10, $11)`,
		s.ID, s.TenantID, s.UserID, tokenHash, s.DeviceLabel, nullText(ua), ipParam(ip), s.CreatedAt, s.ExpiresAt,
		s.MFAVerifiedAt, s.ReauthenticatedAt); err != nil {
		return fmt.Errorf("identity: insert session: %w", err)
	}
	return nil
}

// revokeSessions revokes the open sessions of a user except keep (uuid.Nil keeps none) and
// returns how many were revoked.
func revokeSessions(ctx context.Context, q db.Querier, tenantID, userID, keep uuid.UUID, reason string, now time.Time) (int64, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $4, revoked_reason = $5
		WHERE tenant_id = $1 AND user_id = $2 AND id <> $3 AND revoked_at IS NULL`,
		tenantID, userID, keep, now, reason)
	if err != nil {
		return 0, fmt.Errorf("identity: revoke sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("identity: revoke sessions: %w", err)
	}
	return n, nil
}

// revokeSession revokes one open session of a user; found is false when it is not open.
func revokeSession(ctx context.Context, q db.Querier, tenantID, userID, sessionID uuid.UUID, reason string, now time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $4, revoked_reason = $5
		WHERE tenant_id = $1 AND user_id = $2 AND id = $3 AND revoked_at IS NULL`,
		tenantID, userID, sessionID, now, reason)
	if err != nil {
		return false, fmt.Errorf("identity: revoke session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("identity: revoke session: %w", err)
	}
	return n == 1, nil
}

// deviceHistory reports whether the user ever had a session, and one on a device with label.
func deviceHistory(ctx context.Context, q db.Querier, tenantID, userID uuid.UUID, label string) (anySession, known bool, err error) {
	if err := q.QueryRowxContext(ctx, `
		SELECT count(*) > 0, COALESCE(bool_or(device_label = $3), false)
		FROM sessions WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID, label).Scan(&anySession, &known); err != nil {
		return false, false, fmt.Errorf("identity: read devices: %w", err)
	}
	return anySession, known, nil
}

// recordAttempt appends one login attempt for the throttle.
func recordAttempt(ctx context.Context, q db.Querier, emailKey, ip string, succeeded bool, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO login_attempts (id, email_key, ip, succeeded, created_at) VALUES ($1, $2, $3, $4, $5)`,
		ids.New(), emailKey, ipParam(ip), succeeded, now); err != nil {
		return fmt.Errorf("identity: record login attempt: %w", err)
	}
	return nil
}

// lockedUntil returns when the throttle lock of an e-mail and client address ends (zero when
// not locked): the last MaxFailedLogins attempts since the last success are all failures that
// fit in ThrottleWindow, and the newest one is less than LockDuration old.
func lockedUntil(ctx context.Context, q db.Querier, emailKey, ip string, now time.Time) (time.Time, error) {
	var failures []time.Time
	err := sqlx.SelectContext(ctx, q, &failures, `
		SELECT created_at FROM login_attempts
		WHERE email_key = $1 AND ip IS NOT DISTINCT FROM $2::inet AND NOT succeeded AND created_at > $3
		  AND created_at > COALESCE((SELECT max(created_at) FROM login_attempts
		                              WHERE email_key = $1 AND ip IS NOT DISTINCT FROM $2::inet AND succeeded), '-infinity')
		ORDER BY created_at DESC
		LIMIT $4`,
		emailKey, ipParam(ip), now.Add(-(ThrottleWindow + LockDuration)), MaxFailedLogins)
	if err != nil {
		return time.Time{}, fmt.Errorf("identity: read login attempts: %w", err)
	}
	if len(failures) < MaxFailedLogins {
		return time.Time{}, nil
	}
	newest, oldest := failures[0], failures[len(failures)-1]
	until := newest.Add(LockDuration)
	if newest.Sub(oldest) > ThrottleWindow || !now.Before(until) {
		return time.Time{}, nil
	}
	return until, nil
}
