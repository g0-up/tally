package identity

import (
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// errInvalidCredentials is the single answer for an unknown e-mail, an e-mail of another store,
// a wrong password and an account without a password yet.
func errInvalidCredentials() error {
	return apperr.New(apperr.Unauthenticated, "invalid_credentials", "The e-mail or password is not correct")
}

func errTokenInvalid() error {
	return apperr.NewValidation("token_invalid", "This link is invalid, was already used or has expired", nil)
}

// errClosureLinkNotFound answers a closure export link that is unknown, replaced, expired or
// another store's alike.
func errClosureLinkNotFound() error {
	return apperr.NewNotFound("closure_link_not_found", "This download link is invalid or has expired")
}

func errEmailNotVerified() error {
	return apperr.NewForbidden("email_not_verified", "Confirm your e-mail address with the link we sent before signing in")
}

func errAccountLocked() error {
	return apperr.NewForbidden("account_locked", "This account is locked; contact support")
}

func errEmailInUse() error {
	return apperr.NewValidation("email_in_use", "This e-mail address already has a Tally account",
		map[string]string{"email": "in_use"})
}

func errInvalidCode() error {
	return apperr.New(apperr.Unauthenticated, "invalid_code", "The code is not correct")
}

func errChallengeInvalid() error {
	return apperr.New(apperr.Unauthenticated, "challenge_invalid", "The sign-in took too long; enter your password again")
}

func errInvalidPassword() error {
	return apperr.NewForbidden("invalid_password", "The password is not correct")
}

func errSandboxReplaced() error {
	return apperr.NewConflict("sandbox_replaced", "The demo store was just reset; sign in again")
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

// ThrottledError is a login refused by the brute-force throttle; RetryAfter is the remaining lock.
type ThrottledError struct {
	RetryAfter time.Duration
	err        *apperr.Error
}

func newThrottled(wait time.Duration) *ThrottledError {
	return &ThrottledError{
		RetryAfter: wait,
		err:        apperr.New(apperr.RateLimited, "rate_limited", "Too many failed attempts; try again later"),
	}
}

func (e *ThrottledError) Error() string { return e.err.Error() }

// Unwrap exposes the apperr for rendering.
func (e *ThrottledError) Unwrap() error { return e.err }
