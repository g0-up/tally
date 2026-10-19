// Package identity owns the store owner account: sign-up, e-mail verification, login with its
// brute-force throttle, password reset, TOTP MFA with recovery codes, sessions and devices,
// re-authentication for sensitive actions and ownership transfer. It implements the session and
// tenant-host resolvers behind the platform middleware.
//
// The identity tables (users, sessions, auth_tokens, mfa_*, login_attempts) are outside row level
// security, so every query here filters tenant_id explicitly.
package identity

import (
	"time"

	"github.com/google/uuid"
)

// User statuses (users.status). Exactly one non-disabled user exists per tenant.
const (
	StatusPending  = "pending_verification"
	StatusActive   = "active"
	StatusLocked   = "locked"
	StatusDisabled = "disabled"
)

// Token purposes (auth_tokens.purpose).
const (
	PurposeVerifyEmail   = "verify_email"
	PurposeResetPassword = "reset_password"
	PurposeOwnerTransfer = "owner_transfer"
	// PurposeSupportConsent records the owner's consent to a support session; its value is never
	// sent anywhere, the row is the consent and using it up revokes the session.
	PurposeSupportConsent = "support_consent"
	// PurposeClosureVerify confirms an account closure request from the owner's mailbox.
	PurposeClosureVerify = "closure_verify"
	// PurposeClosureExport opens the final full data export of a closed store from the owner's
	// mailbox; it can be used again until the export's link ends.
	PurposeClosureExport = "closure_export"
)

// Lifetimes.
const (
	VerifyTokenTTL   = 24 * time.Hour
	ResetTokenTTL    = time.Hour
	OwnerTransferTTL = 72 * time.Hour
	ClosureTokenTTL  = 24 * time.Hour
	// SessionAbsoluteTTL ends a session whatever its activity.
	SessionAbsoluteTTL = 30 * 24 * time.Hour
	// SessionIdleTTL ends a session nobody used for that long.
	SessionIdleTTL = 12 * time.Hour
	// ChallengeTTL bounds the time between the password and the MFA code of a login.
	ChallengeTTL = 5 * time.Minute
	// lastSeenResolution limits last_seen_at writes to one per minute and session.
	lastSeenResolution = time.Minute
)

// Login throttle: MaxFailedLogins failures of one e-mail from one address within
// ThrottleWindow lock that pair for LockDuration after the last failure. A success resets it.
const (
	MaxFailedLogins = 5
	ThrottleWindow  = 15 * time.Minute
	LockDuration    = 15 * time.Minute
)

// Password length bounds, in bytes.
const (
	MinPasswordBytes = 8
	maxEmailLen      = 254
)

// User is the owner account of a tenant.
type User struct {
	ID              uuid.UUID  `db:"id"`
	TenantID        uuid.UUID  `db:"tenant_id"`
	Email           string     `db:"email"`
	EmailKey        string     `db:"email_key"`
	Phone           *string    `db:"phone"`
	FullName        string     `db:"full_name"`
	PasswordHash    string     `db:"password_hash"`
	EmailVerifiedAt *time.Time `db:"email_verified_at"`
	MFAEnabled      bool       `db:"mfa_enabled"`
	Status          string     `db:"status"`
	LastLoginAt     *time.Time `db:"last_login_at"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	Version         int        `db:"version"`
}

const userColumns = `id, tenant_id, email, email_key, phone, full_name, password_hash, email_verified_at, mfa_enabled,
	status, last_login_at, created_at, updated_at, version`

// Session is one signed-in device.
type Session struct {
	ID                uuid.UUID  `db:"id"`
	TenantID          uuid.UUID  `db:"tenant_id"`
	UserID            uuid.UUID  `db:"user_id"`
	DeviceLabel       string     `db:"device_label"`
	UserAgent         *string    `db:"user_agent"`
	IP                *string    `db:"ip"`
	CreatedAt         time.Time  `db:"created_at"`
	LastSeenAt        time.Time  `db:"last_seen_at"`
	ExpiresAt         time.Time  `db:"expires_at"`
	MFAVerifiedAt     *time.Time `db:"mfa_verified_at"`
	ReauthenticatedAt *time.Time `db:"reauthenticated_at"`
}

const sessionColumns = `id, tenant_id, user_id, device_label, user_agent, host(ip) AS ip, created_at, last_seen_at,
	expires_at, mfa_verified_at, reauthenticated_at`

// authToken is a consumed single-use token.
type authToken struct {
	ID        uuid.UUID  `db:"id"`
	TenantID  uuid.UUID  `db:"tenant_id"`
	UserID    uuid.UUID  `db:"user_id"`
	Purpose   string     `db:"purpose"`
	Payload   []byte     `db:"payload"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
}

// Client describes the caller of an anonymous or session request.
type Client struct {
	IP        string
	UserAgent string
	RequestID string
}

// IssuedSession is a new session; Token goes into the session cookie and is never stored.
type IssuedSession struct {
	ID        uuid.UUID
	Token     string
	ExpiresAt time.Time
}
