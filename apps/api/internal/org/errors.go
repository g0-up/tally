package org

import (
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// errNotFound is the single answer for a record that does not exist or belongs to another
// tenant, so ids of other stores are indistinguishable from unknown ids.
func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

// errInvalid is a 422 with per-field reason codes.
func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

// errNotSandbox rejects demo-only actions on a real store.
func errNotSandbox() error {
	return apperr.NewForbidden("not_sandbox", "Only a demo store can be reset")
}

// ResetTooSoonError refuses a reset of a sandbox younger than SandboxResetInterval; RetryAfter is
// how long until it may be reset.
type ResetTooSoonError struct {
	RetryAfter time.Duration
	err        *apperr.Error
}

func newResetTooSoon(wait time.Duration) *ResetTooSoonError {
	return &ResetTooSoonError{
		RetryAfter: wait,
		err:        apperr.New(apperr.RateLimited, "sandbox_reset_too_soon", "The demo store was reset moments ago; try again later"),
	}
}

func (e *ResetTooSoonError) Error() string { return e.err.Error() }

// Unwrap exposes the apperr for rendering.
func (e *ResetTooSoonError) Unwrap() error { return e.err }
