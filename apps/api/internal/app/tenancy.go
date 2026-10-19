package app

import (
	"context"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// NewTenantProvisioner adapts the organisation provisioner to the port the identity domain
// declares for creating tenants.
func NewTenantProvisioner(p *org.Provisioner) identity.TenantProvisioner {
	return tenantProvisioner{p: p}
}

type tenantProvisioner struct {
	p *org.Provisioner
}

func (t tenantProvisioner) ValidateName(field, name string) error {
	return org.ValidateName(field, name)
}

func (t tenantProvisioner) RequireFreeSlug(ctx context.Context, q db.Querier, slug string) error {
	if err := t.p.Slugs().Validate(slug); err != nil {
		return err
	}
	check, err := t.p.Slugs().Check(ctx, q, slug)
	if err != nil {
		return err
	}
	if !check.Available {
		return org.SlugTakenError()
	}
	return nil
}

func (t tenantProvisioner) CheckSlug(ctx context.Context, q db.Querier, slug string) (identity.SlugCheck, error) {
	c, err := t.p.Slugs().Check(ctx, q, slug)
	if err != nil {
		return identity.SlugCheck{}, err
	}
	return identity.SlugCheck{Available: c.Available, Reason: c.Reason, Suggestion: c.Suggestion}, nil
}

func (t tenantProvisioner) Provision(ctx context.Context, q db.Querier, spec identity.TenantSpec, actor audit.Actor) (identity.TenantInfo, bool, error) {
	out, err := t.p.Provision(ctx, q, org.TenantSpec{
		ID: spec.ID, Slug: spec.Slug, DisplayName: spec.DisplayName, LegalName: spec.LegalName,
		IsSandbox: spec.IsSandbox, SandboxOf: spec.SandboxOf, ParentSlug: spec.ParentSlug, ReferralCode: spec.ReferralCode,
	}, actor)
	if err != nil {
		return identity.TenantInfo{}, false, err
	}
	tn := out.Tenant
	return identity.TenantInfo{
		ID: tn.ID, Slug: tn.Slug, DisplayName: tn.DisplayName, Status: tn.Status, IsSandbox: tn.IsSandbox, SandboxOf: tn.SandboxOf,
	}, out.Created, nil
}
