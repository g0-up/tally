package org

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// SeedContext is what a seeder needs to create the default data of a new tenant. Seeders run
// inside the provisioning tenant transaction and must be idempotent (a retried provisioning of
// the same tenant id runs them again).
type SeedContext struct {
	TenantID    uuid.UUID
	StoreID     uuid.UUID
	WarehouseID uuid.UUID
	IsSandbox   bool
	Now         time.Time
	// Actor is who provisions the tenant, for the audit trail of seeded records.
	Actor audit.Actor
	// ReferralCode is the partner code the store signed up with (empty for none).
	ReferralCode string
}

// Default data seeders, implemented by the domains that own the tables (catalog, pricing,
// partners, cash, control). The provisioner only orchestrates them.
type (
	// ColorSeeder creates the seven fixed colors.
	ColorSeeder interface {
		SeedColors(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// UnitSeeder creates the base units.
	UnitSeeder interface {
		SeedUnits(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// TaxSeeder creates the tax rates.
	TaxSeeder interface {
		SeedTaxRates(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// AttributeSetSeeder creates the base attribute sets.
	AttributeSetSeeder interface {
		SeedAttributeSets(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// PriceListSeeder creates the retail and wholesale price lists.
	PriceListSeeder interface {
		SeedPriceLists(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// PartnerSeeder creates the customer groups (Khách lẻ, Khách sỉ).
	PartnerSeeder interface {
		SeedPartnerDefaults(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// FundSeeder creates the fund accounts and payment methods.
	FundSeeder interface {
		SeedFunds(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// ReasonSeeder creates the default reasons.
	ReasonSeeder interface {
		SeedReasons(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// DemoSampleSeeder fills a sandbox tenant with the sample catalog and documents.
	DemoSampleSeeder interface {
		SeedDemoSample(ctx context.Context, q db.Querier, sc SeedContext) error
	}
	// SubscriptionSeeder starts a real store on the trial plan and records its referral.
	SubscriptionSeeder interface {
		SeedSubscription(ctx context.Context, q db.Querier, sc SeedContext) error
	}
)

// Seeders are the default data seeders wired by the composition root; a nil seeder is skipped
// until the domain that owns it is built.
type Seeders struct {
	Colors        ColorSeeder
	Units         UnitSeeder
	TaxRates      TaxSeeder
	AttributeSets AttributeSetSeeder
	PriceLists    PriceListSeeder
	Partners      PartnerSeeder
	Funds         FundSeeder
	Reasons       ReasonSeeder
	// DemoSample runs for sandbox tenants only.
	DemoSample DemoSampleSeeder
	// Subscription runs for real (non-sandbox) tenants only.
	Subscription SubscriptionSeeder
}

// run calls every wired seeder in dependency order (units before attribute sets, price lists
// before partners …).
func (s Seeders) run(ctx context.Context, q db.Querier, sc SeedContext) error {
	steps := []func() error{}
	if s.Colors != nil {
		steps = append(steps, func() error { return s.Colors.SeedColors(ctx, q, sc) })
	}
	if s.Units != nil {
		steps = append(steps, func() error { return s.Units.SeedUnits(ctx, q, sc) })
	}
	if s.TaxRates != nil {
		steps = append(steps, func() error { return s.TaxRates.SeedTaxRates(ctx, q, sc) })
	}
	if s.AttributeSets != nil {
		steps = append(steps, func() error { return s.AttributeSets.SeedAttributeSets(ctx, q, sc) })
	}
	if s.PriceLists != nil {
		steps = append(steps, func() error { return s.PriceLists.SeedPriceLists(ctx, q, sc) })
	}
	if s.Partners != nil {
		steps = append(steps, func() error { return s.Partners.SeedPartnerDefaults(ctx, q, sc) })
	}
	if s.Funds != nil {
		steps = append(steps, func() error { return s.Funds.SeedFunds(ctx, q, sc) })
	}
	if s.Reasons != nil {
		steps = append(steps, func() error { return s.Reasons.SeedReasons(ctx, q, sc) })
	}
	if sc.IsSandbox && s.DemoSample != nil {
		steps = append(steps, func() error { return s.DemoSample.SeedDemoSample(ctx, q, sc) })
	}
	if !sc.IsSandbox && s.Subscription != nil {
		steps = append(steps, func() error { return s.Subscription.SeedSubscription(ctx, q, sc) })
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
