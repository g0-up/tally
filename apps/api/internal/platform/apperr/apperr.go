// Package apperr defines the application error model shared by every layer. Domain code returns
// *Error values with a stable machine-readable Code; the HTTP layer maps Kind to a status code.
// Any other error is treated as internal and never shown to clients.
package apperr

import (
	"errors"
	"fmt"
)

// Kind classifies an error for transport mapping. The zero value is Internal.
type Kind int

// Error kinds. Keep the HTTP mapping in httpx.StatusOf in sync when adding one.
const (
	Internal Kind = iota
	Validation
	NotFound
	Conflict
	ExceptionRequired
	PreconditionFailed
	PreconditionRequired
	PeriodLocked
	ReadOnlyTenant
	TenantMaintenance
	Forbidden
	Unauthenticated
	RateLimited
	MisdirectedRequest
	Gone
	Timeout
)

var kindNames = map[Kind]string{
	Internal:             "internal",
	Validation:           "validation",
	NotFound:             "not_found",
	Conflict:             "conflict",
	ExceptionRequired:    "exception_required",
	PreconditionFailed:   "precondition_failed",
	PreconditionRequired: "precondition_required",
	PeriodLocked:         "period_locked",
	ReadOnlyTenant:       "read_only_tenant",
	TenantMaintenance:    "tenant_maintenance",
	Forbidden:            "forbidden",
	Unauthenticated:      "unauthenticated",
	RateLimited:          "rate_limited",
	MisdirectedRequest:   "misdirected_request",
	Gone:                 "gone",
	Timeout:              "timeout",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Error is an expected, client-facing failure.
type Error struct {
	Kind    Kind
	Code    string            // stable snake_case code, e.g. "credit_limit_exceeded"
	Message string            // human readable, safe to show
	Fields  map[string]string // field path -> reason code, for validation errors
	// ExceptionKind is set for ExceptionRequired errors: the owner may confirm and retry.
	ExceptionKind string
	cause         error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause, if any.
func (e *Error) Unwrap() error { return e.cause }

// WithCause returns a copy of e that wraps cause (kept for logs, never rendered).
func (e *Error) WithCause(cause error) *Error {
	c := *e
	c.cause = cause
	return &c
}

// New builds an error of the given kind.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// NewValidation builds a validation error; fields maps field paths to reason codes and may be nil.
func NewValidation(code, message string, fields map[string]string) *Error {
	return &Error{Kind: Validation, Code: code, Message: message, Fields: fields}
}

// NewNotFound builds a not-found error.
func NewNotFound(code, message string) *Error { return New(NotFound, code, message) }

// NewConflict builds a conflict error.
func NewConflict(code, message string) *Error { return New(Conflict, code, message) }

// NewForbidden builds a forbidden error.
func NewForbidden(code, message string) *Error { return New(Forbidden, code, message) }

// NewGone reports a resource that existed but is no longer available, such as an expired export.
func NewGone(code, message string) *Error { return New(Gone, code, message) }

// NewTimeout reports work that a dependency of the server did not finish in time.
func NewTimeout(code, message string) *Error { return New(Timeout, code, message) }

// NewExceptionRequired asks the owner to confirm an exception of the given kind before retrying.
func NewExceptionRequired(exceptionKind, message string) *Error {
	return &Error{Kind: ExceptionRequired, Code: exceptionKind, Message: message, ExceptionKind: exceptionKind}
}

// NewUnauthenticated is the single response for every missing or unusable session.
func NewUnauthenticated() *Error {
	return New(Unauthenticated, "unauthenticated", "Authentication required")
}

// NewPreconditionFailed reports a stale If-Match / optimistic version.
func NewPreconditionFailed(message string) *Error {
	return New(PreconditionFailed, "precondition_failed", message)
}

// NewPeriodLocked reports a business date inside a locked accounting period.
func NewPeriodLocked(message string) *Error { return New(PeriodLocked, "period_locked", message) }

// NewReadOnlyTenant reports a tenant whose subscription only allows reads.
func NewReadOnlyTenant() *Error {
	return New(ReadOnlyTenant, "subscription_read_only", "The store is read-only; changes are not allowed")
}

// NewTenantMaintenance reports a tenant an operator holds (a restore copy runs): no change is
// allowed until it is released.
func NewTenantMaintenance() *Error {
	return New(TenantMaintenance, "tenant_maintenance", "The store is under maintenance; try again shortly")
}

// As returns the *Error in err's chain.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// KindOf returns the kind of err, Internal when err is not an *Error.
func KindOf(err error) Kind {
	if e, ok := As(err); ok {
		return e.Kind
	}
	return Internal
}

// Is reports whether err carries an *Error with the given code.
func Is(err error, code string) bool {
	e, ok := As(err)
	return ok && e.Code == code
}
