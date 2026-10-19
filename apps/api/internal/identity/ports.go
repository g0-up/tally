package identity

import (
	"context"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// TenantSpec describes the tenant to create with its owner.
type TenantSpec struct {
	// ID is chosen by the caller so a retried provisioning is recognised; empty gets a new id.
	ID          uuid.UUID
	Slug        string
	DisplayName string
	// LegalName defaults to DisplayName.
	LegalName string
	IsSandbox bool
	// SandboxOf and ParentSlug identify the real tenant of a demo tenant (both empty for the
	// standalone development demo).
	SandboxOf  *uuid.UUID
	ParentSlug string
	// ReferralCode is the partner code the owner signed up with; an unknown code is ignored.
	ReferralCode string
}

// SlugCheck is the answer of the live store address check.
type SlugCheck struct {
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// PasswordHasher hashes and checks passwords (argon2id in production, authn.Hasher).
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
	// DummyVerify spends the time of a Verify, so an unknown account answers as slowly as a
	// known one.
	DummyVerify(password string)
}

// TenantProvisioner creates tenants and owns the store address and store name rules. The
// organisation domain implements it; the composition root wires the concrete type.
type TenantProvisioner interface {
	// ValidateName checks a store name; the error is a 422 on field.
	ValidateName(field, name string) error
	// RequireFreeSlug returns a 422 (slug_invalid, slug_reserved, slug_taken) unless slug may be
	// issued to a new store.
	RequireFreeSlug(ctx context.Context, q db.Querier, slug string) error
	// CheckSlug reports whether slug is free and suggests a free address when it is not.
	CheckSlug(ctx context.Context, q db.Querier, slug string) (SlugCheck, error)
	// Provision creates the tenant with its store, warehouse, settings and default data inside
	// the caller's tenant transaction. created is false when the tenant already existed.
	Provision(ctx context.Context, q db.Querier, spec TenantSpec, actor audit.Actor) (tenant TenantInfo, created bool, err error)
}
