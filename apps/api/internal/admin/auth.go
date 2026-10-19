// Package admin is the platform operator side of Tally, served only on the admin host under
// /api/admin/v1: admin sign-in with a mandatory TOTP code, the tenant directory (metadata,
// subscription and usage, never sales data), incidents and the public status, partners and
// referrals, and product feedback.
package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Admin sign-in durations.
const (
	// SessionTTL is the absolute lifetime of an admin session; there is no remember-me.
	SessionTTL = 8 * time.Hour
	// ChallengeTTL bounds the time between the password step and the TOTP step.
	ChallengeTTL = 5 * time.Minute
	// TOTPIssuer names the account in the operator's authenticator app.
	TOTPIssuer = "Tally Admin"
	// MinPasswordBytes is the shortest admin password.
	MinPasswordBytes = 12
	// MaxTOTPFailures wrong codes in a row lock the account for TOTPLockDuration. The lock outlasts
	// ChallengeTTL and a password step does not reset the count, so no challenge takes more than
	// MaxTOTPFailures codes.
	MaxTOTPFailures  = 5
	TOTPLockDuration = 15 * time.Minute
)

// Sign-in throttle per account (e-mail for the password step, admin for the TOTP step), on top of
// the throttle per client address: ten attempts a minute.
const (
	accountPerSecond = 10.0 / 60
	accountBurst     = 10
)

const (
	challengePurpose = "admin-mfa-challenge"
	// tokenLen is the length of an authn.NewToken value; other cookie values are never looked up.
	tokenLen    = 43
	maxEmailLen = 254
)

// Keys are the derived keys of admin sign-in, apart from the owner keys of the same secrets.
type Keys struct {
	// Tokens hashes admin session tokens.
	Tokens *authn.Keyed
	// Challenge signs the TOTP step of a sign-in.
	Challenge *authn.Signer
	// TOTP encrypts the admin TOTP secrets at rest.
	TOTP *authn.Cipher
}

// NewKeys derives the keys from SESSION_HASH_KEY and APP_ENCRYPTION_KEY.
func NewKeys(sessionHashKey, encryptionKey string) (Keys, error) {
	if sessionHashKey == "" || encryptionKey == "" {
		return Keys{}, errors.New("admin: the session hash key and the encryption key are required")
	}
	tokens, err := authn.NewKeyed(authn.DeriveKey(sessionHashKey, "admin-session-token"))
	if err != nil {
		return Keys{}, fmt.Errorf("admin: %w", err)
	}
	challenge, err := authn.NewSigner(authn.DeriveKey(sessionHashKey, "admin-mfa-challenge"))
	if err != nil {
		return Keys{}, fmt.Errorf("admin: %w", err)
	}
	totp, err := authn.NewCipher(authn.DeriveKey(encryptionKey, "admin-totp-secret"))
	if err != nil {
		return Keys{}, fmt.Errorf("admin: %w", err)
	}
	return Keys{Tokens: tokens, Challenge: challenge, TOTP: totp}, nil
}

// AuthDeps are the collaborators of AuthService.
type AuthDeps struct {
	DB     *db.DB
	Clock  clock.Clock
	Hasher *authn.Hasher
	Keys   Keys
	Logger *slog.Logger
}

// AuthService signs platform admins in with a password and a mandatory TOTP code, and resolves
// their sessions. It implements middleware.SessionResolver for the admin cookie.
type AuthService struct {
	db      *db.DB
	clock   clock.Clock
	hasher  *authn.Hasher
	keys    Keys
	logger  *slog.Logger
	limiter *middleware.RateLimiter
}

// NewAuthService returns the service.
func NewAuthService(d AuthDeps) (*AuthService, error) {
	if d.DB == nil || d.Clock == nil || d.Hasher == nil || d.Keys.Tokens == nil || d.Keys.Challenge == nil || d.Keys.TOTP == nil {
		return nil, errors.New("admin: auth needs the database, clock, hasher and keys")
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthService{
		db: d.DB, clock: d.Clock, hasher: d.Hasher, keys: d.Keys, logger: logger,
		limiter: middleware.NewRateLimiter(accountPerSecond, accountBurst),
	}, nil
}

// account is a platform admin row.
type account struct {
	ID           uuid.UUID  `db:"id"`
	Email        string     `db:"email"`
	FullName     string     `db:"full_name"`
	PasswordHash string     `db:"password_hash"`
	MFASecret    []byte     `db:"mfa_secret_ciphertext"`
	Status       string     `db:"status"`
	LockedUntil  *time.Time `db:"locked_until"`
}

// lockedFor is the rest of the TOTP lock of the account at now; zero when not locked.
func (a account) lockedFor(now time.Time) time.Duration {
	if a.LockedUntil == nil || !a.LockedUntil.After(now) {
		return 0
	}
	return a.LockedUntil.Sub(now)
}

const accountColumns = `id, email, full_name, password_hash, mfa_secret_ciphertext, status, locked_until`

func errInvalidCredentials() error {
	return apperr.New(apperr.Unauthenticated, "invalid_credentials", "The e-mail or password is not correct")
}

func errChallengeInvalid() error {
	return apperr.New(apperr.Unauthenticated, "challenge_invalid", "The sign-in took too long; enter your password again")
}

func errInvalidCode() error {
	return apperr.New(apperr.Unauthenticated, "invalid_code", "The code is not correct")
}

// throttledError is a sign-in step refused by a throttle or the TOTP lock; RetryAfter is the wait.
type throttledError struct {
	RetryAfter time.Duration
	err        *apperr.Error
}

func errThrottled(wait time.Duration) *throttledError {
	return &throttledError{
		RetryAfter: wait,
		err:        apperr.New(apperr.RateLimited, "rate_limited", "Too many failed attempts; try again later"),
	}
}

func (e *throttledError) Error() string { return e.err.Error() }

// Unwrap exposes the apperr for rendering.
func (e *throttledError) Unwrap() error { return e.err }

// throttle takes an attempt of the account key.
func (s *AuthService) throttle(key, ip string) error {
	if ok, wait := s.limiter.Allow(key); !ok {
		s.logger.Warn("admin sign-in refused", "reason", "account_throttle", "key", key, "ip", ip)
		return errThrottled(wait)
	}
	return nil
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

// Login checks the password and answers the signed challenge of the TOTP step; no session
// exists before the code. An unknown e-mail costs the same hashing time as a wrong password.
func (s *AuthService) Login(ctx context.Context, email, password, ip string) (string, error) {
	key := textnorm.EmailKey(email)
	if key == "" || len(key) > maxEmailLen || password == "" || len(password) > authn.MaxPasswordBytes {
		return "", errInvalidCredentials()
	}
	if err := s.throttle("login:"+key, ip); err != nil {
		return "", err
	}
	var a account
	found := false
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &a, `SELECT `+accountColumns+` FROM platform_admins WHERE email_key = $1`, key)
		if db.IsNoRows(err) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return "", fmt.Errorf("admin: read account: %w", err)
	}
	if !found {
		s.hasher.DummyVerify(password)
		s.logger.Warn("admin sign-in refused", "reason", "unknown_email", "ip", ip)
		return "", errInvalidCredentials()
	}
	ok, err := s.hasher.Verify(a.PasswordHash, password)
	if err != nil {
		return "", fmt.Errorf("admin: verify password: %w", err)
	}
	if !ok || a.Status != "active" {
		s.logger.Warn("admin sign-in refused", "reason", "password_or_status", "admin_id", a.ID.String(), "ip", ip)
		return "", errInvalidCredentials()
	}
	now := s.clock.Now()
	if wait := a.lockedFor(now); wait > 0 {
		s.logger.Warn("admin sign-in refused", "reason", "locked", "admin_id", a.ID.String(), "ip", ip)
		return "", errThrottled(wait)
	}
	return s.keys.Challenge.Sign(challengePurpose, a.ID[:], now.Add(ChallengeTTL)), nil
}

// IssuedSession is a new admin session; Token goes into the admin cookie only.
type IssuedSession struct {
	ID        uuid.UUID
	AdminID   uuid.UUID
	Token     string
	ExpiresAt time.Time
}

// VerifyTOTP completes a sign-in: the challenge must be valid and the code must match a step
// later than the last accepted one, so a code never signs in twice. MaxTOTPFailures wrong codes in
// a row lock the account for TOTPLockDuration; a locked account refuses even the right code.
func (s *AuthService) VerifyTOTP(ctx context.Context, challenge, code, ip string) (IssuedSession, error) {
	now := s.clock.Now()
	payload, err := s.keys.Challenge.Verify(challengePurpose, challenge, now)
	if err != nil || len(payload) != 16 {
		return IssuedSession{}, errChallengeInvalid()
	}
	var adminID uuid.UUID
	copy(adminID[:], payload)
	if err := s.throttle("totp:"+adminID.String(), ip); err != nil {
		return IssuedSession{}, err
	}
	token, err := authn.NewToken()
	if err != nil {
		return IssuedSession{}, fmt.Errorf("admin: session token: %w", err)
	}
	out := IssuedSession{ID: ids.New(), AdminID: adminID, Token: token, ExpiresAt: now.Add(SessionTTL)}
	var wrong bool
	var lockedUntil *time.Time
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var a account
		err := sqlx.GetContext(ctx, q, &a, `SELECT `+accountColumns+` FROM platform_admins WHERE id = $1 FOR UPDATE`, adminID)
		if db.IsNoRows(err) {
			return errChallengeInvalid()
		}
		if err != nil {
			return fmt.Errorf("admin: read account: %w", err)
		}
		if a.Status != "active" {
			return errChallengeInvalid()
		}
		if wait := a.lockedFor(now); wait > 0 {
			return errThrottled(wait)
		}
		secret, err := s.keys.TOTP.Open(a.MFASecret, a.ID[:])
		if err != nil {
			return fmt.Errorf("admin: open TOTP secret: %w", err)
		}
		if step, ok := authn.MatchTOTP(secret, code, now); ok {
			res, err := q.ExecContext(ctx, `
				UPDATE platform_admins SET last_totp_step = $2, totp_failed_count = 0, locked_until = NULL, updated_at = $3
				WHERE id = $1 AND (last_totp_step IS NULL OR last_totp_step < $2)`, a.ID, step, now)
			if err != nil {
				return fmt.Errorf("admin: use TOTP code: %w", err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("admin: use TOTP code: %w", err)
			}
			wrong = n != 1
		} else {
			wrong = true
		}
		if wrong {
			// The count commits with the refusal; the lock starts the count again.
			err := q.QueryRowxContext(ctx, `
				UPDATE platform_admins SET
				  totp_failed_count = CASE WHEN totp_failed_count + 1 >= $2 THEN 0 ELSE totp_failed_count + 1 END,
				  locked_until = CASE WHEN totp_failed_count + 1 >= $2 THEN $3 ELSE locked_until END,
				  updated_at = $4
				WHERE id = $1 RETURNING locked_until`, a.ID, MaxTOTPFailures, now.Add(TOTPLockDuration), now).Scan(&lockedUntil)
			if err != nil {
				return fmt.Errorf("admin: count wrong TOTP code: %w", err)
			}
			return nil
		}
		_, err = q.ExecContext(ctx, `
			INSERT INTO platform_admin_sessions (id, admin_id, token_hash, ip, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, out.ID, a.ID, s.keys.Tokens.Sum(token), ipParam(ip), now, out.ExpiresAt)
		if err != nil {
			return fmt.Errorf("admin: insert session: %w", err)
		}
		return nil
	})
	if err != nil {
		if e, ok := apperr.As(err); ok && (e.Kind == apperr.Unauthenticated || e.Kind == apperr.RateLimited) {
			s.logger.Warn("admin sign-in refused", "reason", e.Code, "admin_id", adminID.String(), "ip", ip)
		}
		return IssuedSession{}, err
	}
	if wrong {
		if lockedUntil != nil && lockedUntil.After(now) {
			s.logger.Warn("admin account locked", "reason", "totp_failures", "admin_id", adminID.String(), "ip", ip,
				"failures", MaxTOTPFailures, "locked_until", lockedUntil.UTC())
		} else {
			s.logger.Warn("admin sign-in refused", "reason", "invalid_code", "admin_id", adminID.String(), "ip", ip)
		}
		return IssuedSession{}, errInvalidCode()
	}
	s.logger.Info("admin signed in", "admin_id", adminID.String(), "session_id", out.ID.String(), "ip", ip)
	return out, nil
}

// ResolveSession implements middleware.SessionResolver: the token must belong to an unrevoked,
// unexpired session of an active admin. Every admin session passed the TOTP step.
func (s *AuthService) ResolveSession(ctx context.Context, token string) (middleware.Principal, bool, error) {
	if len(token) != tokenLen {
		return middleware.Principal{}, false, nil
	}
	var row struct {
		ID        uuid.UUID `db:"id"`
		AdminID   uuid.UUID `db:"admin_id"`
		CreatedAt time.Time `db:"created_at"`
	}
	found := false
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &row, `
			SELECT s.id, s.admin_id, s.created_at FROM platform_admin_sessions s
			JOIN platform_admins a ON a.id = s.admin_id
			WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2 AND a.status = 'active'`,
			s.keys.Tokens.Sum(token), s.clock.Now())
		if db.IsNoRows(err) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return middleware.Principal{}, false, fmt.Errorf("admin: resolve session: %w", err)
	}
	if !found {
		return middleware.Principal{}, false, nil
	}
	verified := row.CreatedAt
	return middleware.Principal{
		Kind: middleware.PrincipalPlatformAdmin, SessionID: row.ID, UserID: row.AdminID, MfaVerifiedAt: &verified,
	}, true, nil
}

// Logout revokes the admin session.
func (s *AuthService) Logout(ctx context.Context, p middleware.Principal) error {
	if p.Kind != middleware.PrincipalPlatformAdmin {
		return nil
	}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			UPDATE platform_admin_sessions SET revoked_at = $3 WHERE id = $1 AND admin_id = $2 AND revoked_at IS NULL`,
			p.SessionID, p.UserID, s.clock.Now())
		return err
	})
	if err != nil {
		return fmt.Errorf("admin: revoke session: %w", err)
	}
	s.logger.Info("admin signed out", "admin_id", p.UserID.String(), "session_id", p.SessionID.String())
	return nil
}

// SignedIn is the admin behind a session and when that session ends.
type SignedIn struct {
	AdminID   uuid.UUID `db:"id"`
	FullName  string    `db:"full_name"`
	Email     string    `db:"email"`
	ExpiresAt time.Time `db:"expires_at"`
}

// Me answers the signed-in admin of p and the expiry of its session; a session revoked or expired
// since the request resolved it answers 401.
func (s *AuthService) Me(ctx context.Context, p middleware.Principal) (SignedIn, error) {
	if p.Kind != middleware.PrincipalPlatformAdmin {
		return SignedIn{}, apperr.NewUnauthenticated()
	}
	var out SignedIn
	found := false
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &out, `
			SELECT a.id, a.full_name, a.email, s.expires_at FROM platform_admin_sessions s
			JOIN platform_admins a ON a.id = s.admin_id
			WHERE s.id = $1 AND s.admin_id = $2 AND s.revoked_at IS NULL AND s.expires_at > $3 AND a.status = 'active'`,
			p.SessionID, p.UserID, s.clock.Now())
		if db.IsNoRows(err) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return SignedIn{}, fmt.Errorf("admin: read session: %w", err)
	}
	if !found {
		return SignedIn{}, apperr.NewUnauthenticated()
	}
	out.ExpiresAt = out.ExpiresAt.UTC()
	return out, nil
}

// DirectoryEntry is a platform admin in the admin directory.
type DirectoryEntry struct {
	ID       uuid.UUID `db:"id"`
	FullName string    `db:"full_name"`
	Email    string    `db:"email"`
	Active   bool      `db:"active"`
}

// DirectoryCursor is the keyset cursor of an admin entry: its name, then its id.
func DirectoryCursor(a DirectoryEntry) httpx.Cursor {
	return httpx.Cursor{SortKey: a.FullName, ID: a.ID}
}

// Admins lists one page of the platform admins, active or not, by name; it reads page.FetchLimit
// rows.
func (s *AuthService) Admins(ctx context.Context, page httpx.Page) ([]DirectoryEntry, error) {
	var afterName *string
	afterID := uuid.Nil
	if page.After != nil {
		afterName, afterID = &page.After.SortKey, page.After.ID
	}
	out := []DirectoryEntry{}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT id, full_name, email, status = 'active' AS active FROM platform_admins
			WHERE $1::text IS NULL OR (full_name, id) > ($1, $2)
			ORDER BY full_name, id LIMIT $3`, afterName, afterID, page.FetchLimit())
	})
	if err != nil {
		return nil, fmt.Errorf("admin: list admins: %w", err)
	}
	return out, nil
}

// NewAdmin is an operator account created from the CLI.
type NewAdmin struct {
	Email    string
	FullName string
	Password string
}

// Enrolment is a new admin and its TOTP secret, shown once to the operator terminal.
type Enrolment struct {
	AdminID uuid.UUID
	Email   string
	// Secret is the base32 secret for manual entry; URI is the otpauth:// enrolment link.
	Secret string
	URI    string
}

func (in NewAdmin) validate() (string, error) {
	fields := map[string]string{}
	email := strings.TrimSpace(in.Email)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Name != "" || addr.Address != email || len(email) > maxEmailLen {
		fields["email"] = "invalid"
	}
	if name := strings.TrimSpace(in.FullName); name == "" || utf8.RuneCountInString(name) > 200 || strings.ContainsAny(name, "\r\n") {
		fields["full_name"] = "invalid"
	}
	switch {
	case len(in.Password) < MinPasswordBytes:
		fields["password"] = "too_short"
	case len(in.Password) > authn.MaxPasswordBytes:
		fields["password"] = "too_long"
	}
	if len(fields) > 0 {
		return "", errInvalid(fields)
	}
	return email, nil
}

// CreateAdmin adds an active platform admin with a fresh TOTP secret (MFA is mandatory, so the
// secret exists from the start); a taken e-mail answers 409 email_in_use.
func (s *AuthService) CreateAdmin(ctx context.Context, in NewAdmin) (Enrolment, error) {
	email, err := in.validate()
	if err != nil {
		return Enrolment{}, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return Enrolment{}, fmt.Errorf("admin: hash password: %w", err)
	}
	secret, err := authn.NewTOTPSecret()
	if err != nil {
		return Enrolment{}, fmt.Errorf("admin: TOTP secret: %w", err)
	}
	id := ids.New()
	sealed, err := s.keys.TOTP.Seal(secret, id[:])
	if err != nil {
		return Enrolment{}, fmt.Errorf("admin: seal TOTP secret: %w", err)
	}
	now := s.clock.Now()
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO platform_admins (id, email, email_key, full_name, password_hash, mfa_secret_ciphertext, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $7)`,
			id, email, textnorm.EmailKey(email), strings.TrimSpace(in.FullName), hash, sealed, now)
		if db.IsUniqueViolation(err, "ux_platform_admins__email_key") {
			return apperr.NewConflict("email_in_use", "An admin with this e-mail already exists")
		}
		return err
	})
	if err != nil {
		return Enrolment{}, err
	}
	return Enrolment{
		AdminID: id, Email: email, Secret: authn.EncodeTOTPSecret(secret), URI: authn.TOTPURI(TOTPIssuer, email, secret),
	}, nil
}

// ipParam turns a client address into an inet parameter (NULL when it is not an address).
func ipParam(ip string) any {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	return addr.Unmap().WithZone("").String()
}
