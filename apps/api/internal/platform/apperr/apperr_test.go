package apperr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestKindString(t *testing.T) {
	for kind, name := range kindNames {
		if kind.String() != name {
			t.Errorf("%d.String() = %q, want %q", int(kind), kind.String(), name)
		}
	}
	if got := Kind(99).String(); got != "kind(99)" {
		t.Errorf("unknown kind = %q", got)
	}
}

func TestConstructors(t *testing.T) {
	cases := []struct {
		err  *Error
		kind Kind
		code string
	}{
		{New(Conflict, "dup", "Duplicate"), Conflict, "dup"},
		{NewValidation("invalid", "Invalid", map[string]string{"name": "required"}), Validation, "invalid"},
		{NewNotFound("missing", "Missing"), NotFound, "missing"},
		{NewConflict("taken", "Taken"), Conflict, "taken"},
		{NewForbidden("denied", "Denied"), Forbidden, "denied"},
		{NewExceptionRequired("credit_limit", "Over limit"), ExceptionRequired, "credit_limit"},
		{NewUnauthenticated(), Unauthenticated, "unauthenticated"},
		{NewPreconditionFailed("Stale"), PreconditionFailed, "precondition_failed"},
		{NewPeriodLocked("Locked"), PeriodLocked, "period_locked"},
		{NewReadOnlyTenant(), ReadOnlyTenant, "subscription_read_only"},
		{NewTenantMaintenance(), TenantMaintenance, "tenant_maintenance"},
		{NewGone("export_expired", "Expired"), Gone, "export_expired"},
		{NewTimeout("ai_timeout", "Too slow"), Timeout, "ai_timeout"},
	}
	for _, tc := range cases {
		if tc.err.Kind != tc.kind || tc.err.Code != tc.code || tc.err.Message == "" {
			t.Errorf("%+v: want kind %v code %q", tc.err, tc.kind, tc.code)
		}
	}
	if e := NewExceptionRequired("credit_limit", "x"); e.ExceptionKind != "credit_limit" {
		t.Errorf("exception kind = %q", e.ExceptionKind)
	}
}

func TestWrappingAndLookup(t *testing.T) {
	cause := errors.New("db down")
	base := NewNotFound("sku_not_found", "SKU not found")
	wrapped := base.WithCause(cause)
	if base.Unwrap() != nil {
		t.Error("WithCause mutated the original")
	}
	if !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), "db down") {
		t.Errorf("wrapped = %v", wrapped)
	}
	if base.Error() != "sku_not_found: SKU not found" {
		t.Errorf("Error() = %q", base.Error())
	}

	chain := fmt.Errorf("load: %w", wrapped)
	if e, ok := As(chain); !ok || e.Code != "sku_not_found" {
		t.Errorf("As = %v, %v", e, ok)
	}
	if KindOf(chain) != NotFound || !Is(chain, "sku_not_found") || Is(chain, "other") {
		t.Error("KindOf/Is through a wrapped chain")
	}
	plain := errors.New("boom")
	if _, ok := As(plain); ok || KindOf(plain) != Internal || Is(plain, "boom") {
		t.Error("a plain error must be internal")
	}
}
