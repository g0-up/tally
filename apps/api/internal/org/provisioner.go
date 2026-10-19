package org

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// maxNameLen bounds tenant and store names.
const maxNameLen = 200

// TenantSpec describes the tenant to provision.
type TenantSpec struct {
	// ID is chosen by the caller so a retried provisioning is recognised.
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
	// ReferralCode is the partner code the store signed up with (empty for none).
	ReferralCode string
}

// Provisioned is the outcome of Provision.
type Provisioned struct {
	Tenant      Tenant
	StoreID     uuid.UUID
	WarehouseID uuid.UUID
	// Created is false when the tenant already existed (a retried provisioning).
	Created bool
}

// Provisioner creates a tenant with its single store and warehouse, its settings, onboarding
// checklist, document counters and the default data of every domain (through the Seeders).
// It is the only way tenants are created: sign-up, `tally tenant create` and `tally seed demo`
// all call it, so they share validation, ids, clock and audit.
type Provisioner struct {
	slugs   *SlugPolicy
	audit   *audit.Recorder
	clock   clock.Clock
	seeders Seeders
}

// NewProvisioner returns a provisioner.
func NewProvisioner(slugs *SlugPolicy, rec *audit.Recorder, clk clock.Clock, seeders Seeders) *Provisioner {
	return &Provisioner{slugs: slugs, audit: rec, clock: clk, seeders: seeders}
}

// Slugs returns the slug policy the provisioner enforces.
func (p *Provisioner) Slugs() *SlugPolicy { return p.slugs }

// Provision creates the tenant described by spec inside q, which must be a tenant transaction of
// spec.ID (db.WithTenantTx), so the whole tenant appears atomically. It is idempotent by
// spec.ID: provisioning an existing tenant again only adds what is missing. actor is recorded
// on the tenant.create audit event and must belong to spec.ID.
func (p *Provisioner) Provision(ctx context.Context, q db.Querier, spec TenantSpec, actor audit.Actor) (Provisioned, error) {
	spec.Slug = strings.TrimSpace(spec.Slug)
	spec.DisplayName = strings.TrimSpace(spec.DisplayName)
	spec.LegalName = strings.TrimSpace(spec.LegalName)
	if spec.LegalName == "" {
		spec.LegalName = spec.DisplayName
	}
	if err := p.validate(spec); err != nil {
		return Provisioned{}, err
	}
	if actor.TenantID != spec.ID {
		return Provisioned{}, fmt.Errorf("provision tenant: actor belongs to tenant %s, not %s", actor.TenantID, spec.ID)
	}
	now := p.clock.Now()

	retired, err := isRetired(ctx, q, spec.Slug)
	if err != nil {
		return Provisioned{}, err
	}
	if retired {
		return Provisioned{}, SlugTakenError()
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO tenants (id, slug, legal_name, display_name, currency, timezone, is_sandbox, sandbox_of, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (id) DO NOTHING`,
		spec.ID, spec.Slug, spec.LegalName, spec.DisplayName, DefaultCurrency, DefaultTimezone, spec.IsSandbox, spec.SandboxOf, now)
	if err != nil {
		if db.IsUniqueViolation(err, "ux_tenants__slug") {
			return Provisioned{}, SlugTakenError()
		}
		return Provisioned{}, fmt.Errorf("provision tenant: insert tenant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Provisioned{}, fmt.Errorf("provision tenant: insert tenant: %w", err)
	}
	created := n == 1

	tenant, err := readTenant(ctx, q, spec.ID, false)
	if err != nil {
		return Provisioned{}, err
	}
	if tenant.Slug != spec.Slug || tenant.IsSandbox != spec.IsSandbox {
		return Provisioned{}, fmt.Errorf("provision tenant: tenant %s already exists with another slug or kind", spec.ID)
	}

	out := Provisioned{Tenant: tenant, Created: created}
	if out.StoreID, out.WarehouseID, err = ensureStore(ctx, q, spec.ID, spec.DisplayName, now); err != nil {
		return Provisioned{}, err
	}
	if err := ensureDefaults(ctx, q, spec.ID, now); err != nil {
		return Provisioned{}, err
	}
	sc := SeedContext{
		TenantID: spec.ID, StoreID: out.StoreID, WarehouseID: out.WarehouseID, IsSandbox: spec.IsSandbox,
		Now: now, Actor: actor, ReferralCode: strings.TrimSpace(spec.ReferralCode),
	}
	if err := p.seeders.run(ctx, q, sc); err != nil {
		return Provisioned{}, fmt.Errorf("provision tenant: seed default data: %w", err)
	}
	if created {
		if err := p.audit.Record(ctx, q, actor, audit.Event{
			Action: "tenant.create", EntityType: "tenant", EntityID: spec.ID,
			After: map[string]any{
				"slug": spec.Slug, "display_name": spec.DisplayName, "is_sandbox": spec.IsSandbox,
				"store_id": out.StoreID, "warehouse_id": out.WarehouseID,
			},
		}); err != nil {
			return Provisioned{}, err
		}
	}
	return out, nil
}

func (p *Provisioner) validate(spec TenantSpec) error {
	if spec.ID == uuid.Nil {
		return fmt.Errorf("provision tenant: id is empty")
	}
	if err := ValidateName("display_name", spec.DisplayName); err != nil {
		return err
	}
	if err := ValidateName("legal_name", spec.LegalName); err != nil {
		return err
	}
	if spec.IsSandbox {
		if (spec.SandboxOf == nil) != (spec.ParentSlug == "") {
			return fmt.Errorf("provision tenant: a demo tenant needs both its parent id and slug, or neither")
		}
		return p.slugs.ValidateSandbox(spec.Slug, spec.ParentSlug)
	}
	if spec.SandboxOf != nil {
		return fmt.Errorf("provision tenant: only a demo tenant can have a parent")
	}
	return p.slugs.Validate(spec.Slug)
}

// ValidateName checks a required display name (store, legal or owner name).
func ValidateName(field, v string) error {
	switch {
	case strings.TrimSpace(v) == "":
		return apperr.NewValidation("validation_failed", "Some fields are not valid", map[string]string{field: "required"})
	case utf8.RuneCountInString(v) > maxNameLen || strings.ContainsAny(v, "\r\n\x00"):
		return apperr.NewValidation("validation_failed", "Some fields are not valid", map[string]string{field: "invalid"})
	}
	return nil
}

func isRetired(ctx context.Context, q db.Querier, slug string) (bool, error) {
	var retired bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM retired_tenant_slugs WHERE slug = $1)`, slug).Scan(&retired); err != nil {
		return false, fmt.Errorf("provision tenant: read retired slugs: %w", err)
	}
	return retired, nil
}

// ensureStore creates the single store and warehouse unless they exist and returns their ids.
func ensureStore(ctx context.Context, q db.Querier, tenantID uuid.UUID, name string, now time.Time) (storeID, warehouseID uuid.UUID, err error) {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT ON CONSTRAINT ux_stores__tenant_id DO NOTHING`, ids.New(), tenantID, name, now); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("provision tenant: insert store: %w", err)
	}
	if err := q.QueryRowxContext(ctx, `SELECT id FROM stores WHERE tenant_id = $1`, tenantID).Scan(&storeID); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("provision tenant: read store: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO warehouses (id, tenant_id, store_id, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT ON CONSTRAINT ux_warehouses__tenant_id DO NOTHING`, ids.New(), tenantID, storeID, WarehouseName, now); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("provision tenant: insert warehouse: %w", err)
	}
	if err := q.QueryRowxContext(ctx, `SELECT id FROM warehouses WHERE tenant_id = $1`, tenantID).Scan(&warehouseID); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("provision tenant: read warehouse: %w", err)
	}
	return storeID, warehouseID, nil
}

// ensureDefaults creates the settings row, the onboarding checklist and a counter for every
// document prefix (so numbering never has to create rows under concurrency).
func ensureDefaults(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO tenant_settings (tenant_id, updated_at) VALUES ($1, $2)
		ON CONFLICT ON CONSTRAINT pk_tenant_settings DO NOTHING`, tenantID, now); err != nil {
		return fmt.Errorf("provision tenant: insert settings: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO onboarding_steps (tenant_id, step_key) SELECT $1, unnest($2::text[])
		ON CONFLICT ON CONSTRAINT pk_onboarding_steps DO NOTHING`, tenantID, OnboardingSteps); err != nil {
		return fmt.Errorf("provision tenant: insert onboarding steps: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO doc_sequences (tenant_id, prefix) SELECT $1, unnest($2::text[])
		ON CONFLICT ON CONSTRAINT pk_doc_sequences DO NOTHING`, tenantID, docseq.Prefixes()); err != nil {
		return fmt.Errorf("provision tenant: insert document counters: %w", err)
	}
	return nil
}
