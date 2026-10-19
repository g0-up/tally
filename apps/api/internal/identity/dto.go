package identity

import (
	"time"

	"github.com/google/uuid"
)

// Wire formats of the identity endpoints. Passwords and tokens are bounded so a request cannot
// make the server hash megabytes; the service applies the precise rules.

type signupRequest struct {
	Email     string `json:"email" validate:"required,max=254"`
	Phone     string `json:"phone" validate:"omitempty,max=30"`
	Password  string `json:"password" validate:"required,max=256"`
	FullName  string `json:"full_name" validate:"required,max=200"`
	StoreName string `json:"store_name" validate:"required,max=200"`
	Slug      string `json:"slug" validate:"required,max=63"`
	// ReferralCode is the partner code of the sign-up link, if any.
	ReferralCode string `json:"referral_code" validate:"omitempty,max=40"`
}

type signupResponse struct {
	StoreURL string `json:"store_url"`
}

type slugCheckResponse struct {
	Available  bool    `json:"available"`
	Reason     *string `json:"reason"`
	Suggestion *string `json:"suggestion"`
}

type emailRequest struct {
	Email string `json:"email" validate:"required,max=254"`
}

type verifyRequest struct {
	Token    string `json:"token" validate:"required,max=128"`
	Password string `json:"password" validate:"omitempty,max=256"`
}

type tokenPasswordRequest struct {
	Token    string `json:"token" validate:"required,max=128"`
	Password string `json:"password" validate:"required,max=256"`
}

// loginRequest names the account by e-mail or phone number (login).
type loginRequest struct {
	Login    string `json:"login" validate:"required,max=254"`
	Password string `json:"password" validate:"required,max=256"`
}

type passwordRequest struct {
	Password string `json:"password" validate:"required,max=256"`
}

// mfaVerifyRequest is either the confirmation of a TOTP enrolment (code, signed in) or the MFA
// step of a login (challenge with code or recovery_code).
type mfaVerifyRequest struct {
	Challenge    string `json:"challenge" validate:"omitempty,max=512"`
	Code         string `json:"code" validate:"omitempty,max=16"`
	RecoveryCode string `json:"recovery_code" validate:"omitempty,max=32"`
}

type transferRequest struct {
	Email    string `json:"email" validate:"required,max=254"`
	FullName string `json:"full_name" validate:"required,max=200"`
}

// authResponse answers a login step: "signed_in" with the CSRF token bound to the new session,
// or "mfa_required" with the challenge to send back with a code.
type authResponse struct {
	Status    string     `json:"status"`
	Challenge *string    `json:"challenge,omitempty"`
	CSRFToken *string    `json:"csrf_token,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

const (
	authSignedIn    = "signed_in"
	authMFARequired = "mfa_required"
)

type csrfResponse struct {
	CSRFToken string `json:"csrf_token"`
}

type reauthResponse struct {
	ReauthenticatedUntil time.Time `json:"reauthenticated_until"`
}

type enrolmentResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
}

type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type userDTO struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	FullName        string     `json:"full_name"`
	Phone           *string    `json:"phone"`
	Status          string     `json:"status"`
	MFAEnabled      bool       `json:"mfa_enabled"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
}

type meTenantDTO struct {
	ID          uuid.UUID  `json:"id"`
	Slug        string     `json:"slug"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	IsSandbox   bool       `json:"is_sandbox"`
	SandboxOf   *uuid.UUID `json:"sandbox_of"`
}

type sessionDTO struct {
	ID                uuid.UUID  `json:"id"`
	DeviceLabel       string     `json:"device_label"`
	IP                *string    `json:"ip"`
	CreatedAt         time.Time  `json:"created_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	MFAVerified       bool       `json:"mfa_verified"`
	ReauthenticatedAt *time.Time `json:"reauthenticated_at"`
	Current           bool       `json:"current"`
}

type meResponse struct {
	User    userDTO     `json:"user"`
	Tenant  meTenantDTO `json:"tenant"`
	Session sessionDTO  `json:"session"`
	// ServerTime is RFC 3339 with the offset of the store's time zone.
	ServerTime string `json:"server_time"`
}

type sessionListResponse struct {
	Data []sessionDTO `json:"data"`
}

type revokedResponse struct {
	Revoked int64 `json:"revoked"`
}

func toSessionDTO(s Session, current uuid.UUID) sessionDTO {
	return sessionDTO{
		ID: s.ID, DeviceLabel: s.DeviceLabel, IP: s.IP, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt,
		ExpiresAt: s.ExpiresAt, MFAVerified: s.MFAVerifiedAt != nil, ReauthenticatedAt: s.ReauthenticatedAt,
		Current: s.ID == current,
	}
}

func toMeResponse(m Me) meResponse {
	u := m.User
	return meResponse{
		User: userDTO{
			ID: u.ID, Email: u.Email, FullName: u.FullName, Phone: u.Phone, Status: u.Status, MFAEnabled: u.MFAEnabled,
			EmailVerifiedAt: u.EmailVerifiedAt, LastLoginAt: u.LastLoginAt,
		},
		Tenant: meTenantDTO{
			ID: m.Tenant.ID, Slug: m.Tenant.Slug, DisplayName: m.Tenant.DisplayName, Status: m.Tenant.Status,
			IsSandbox: m.Tenant.IsSandbox, SandboxOf: m.Tenant.SandboxOf,
		},
		Session:    toSessionDTO(m.Session, m.Session.ID),
		ServerTime: m.ServerTime.Format(time.RFC3339),
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
