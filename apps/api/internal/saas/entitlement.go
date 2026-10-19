package saas

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// FlagReader answers a feature flag for a tenant (tenant override, else global); found is false
// when neither is set.
type FlagReader interface {
	Flag(ctx context.Context, q db.Querier, key string, tenantID uuid.UUID) (enabled, found bool, err error)
}

// Reader resolves a tenant's entitlements on the server: the subscription's plan modules and
// limits are the default, active entitlement rows (by valid_from/valid_to on the tenant's
// business date) override them and tenant feature flags "module:<name>" override last. The
// subscription state is evaluated at read time, so a lapsed period blocks postings even before
// the daily lifecycle job records it. A tenant without a subscription (sandboxes, stores created
// before billing) is unrestricted.
//
// Reader implements middleware.EntitlementReader and posting.EntitlementGuard.
type Reader struct {
	db        *db.DB
	clock     clock.Clock
	flags     FlagReader
	lifecycle SubscriptionLifecycle
}

// NewReader returns the entitlement reader; flags may be nil.
func NewReader(d *db.DB, clk clock.Clock, flags FlagReader) *Reader {
	return &Reader{db: d, clock: clk, flags: flags}
}

// CanWrite reports whether the tenant may change data now.
func (r *Reader) CanWrite(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.WithPlatformTx(ctx, func(q db.Querier) error {
		g, err := r.gate(ctx, q, tenantID)
		ok = g.canPost
		return err
	})
	return ok, err
}

// CanPost answers the posting coordinator inside the posting transaction: 423
// tenant_maintenance while an operator holds the tenant, 403 subscription_read_only when the
// tenant may not post.
func (r *Reader) CanPost(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	g, err := r.gate(ctx, q, tenantID)
	if err != nil {
		return err
	}
	if g.tenant.Maintenance {
		return apperr.NewTenantMaintenance()
	}
	if !g.canPost {
		return apperr.NewReadOnlyTenant()
	}
	return nil
}

// InMaintenance reports whether an operator holds the tenant in maintenance.
func (r *Reader) InMaintenance(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var held bool
	err := r.db.WithPlatformTx(ctx, func(q db.Querier) error {
		t, err := readTenantState(ctx, q, tenantID, false)
		held = t.Maintenance
		return err
	})
	return held, err
}

// HasModule reports whether the tenant's entitlements include module.
func (r *Reader) HasModule(ctx context.Context, tenantID uuid.UUID, module string) (bool, error) {
	var ok bool
	err := r.db.WithPlatformTx(ctx, func(q db.Querier) error {
		e, err := r.Resolve(ctx, q, tenantID)
		ok = e.Has(module)
		return err
	})
	return ok, err
}

// Within answers 409 plan_limit_reached when used exceeds the tenant's limit key; a tenant
// without the limit is unbounded. q is any transaction (platform tables have no row security).
func (r *Reader) Within(ctx context.Context, q db.Querier, tenantID uuid.UUID, key string, used int64) error {
	e, err := r.Resolve(ctx, q, tenantID)
	if err != nil {
		return err
	}
	if limit, ok := e.Limits[key]; ok && used > limit {
		return errLimitReached(key)
	}
	return nil
}

// Limit returns the tenant's limit key, if any.
func (r *Reader) Limit(ctx context.Context, q db.Querier, tenantID uuid.UUID, key string) (int64, bool, error) {
	e, err := r.Resolve(ctx, q, tenantID)
	if err != nil {
		return 0, false, err
	}
	v, ok := e.Limits[key]
	return v, ok, nil
}

// gateState is what the write gate reads.
type gateState struct {
	tenant  tenantState
	sub     Subscription
	plan    Plan
	hasSub  bool
	status  string
	canPost bool
	export  bool
}

func (r *Reader) gate(ctx context.Context, q db.Querier, tenantID uuid.UUID) (gateState, error) {
	t, err := readTenantState(ctx, q, tenantID, false)
	if err != nil {
		return gateState{}, err
	}
	g := gateState{tenant: t, canPost: true, export: true}
	switch t.Status {
	case "read_only":
		g.canPost = false
	case "closed", "replaced":
		g.canPost, g.export = false, false
	case "closing":
		frozen, err := closureFrozen(ctx, q, tenantID)
		if err != nil {
			return gateState{}, err
		}
		g.canPost = !frozen
	}
	if t.Maintenance {
		g.canPost = false
	}
	sub, found, err := readSubscription(ctx, q, tenantID, false)
	if err != nil || !found {
		return g, err
	}
	plan, err := readPlan(ctx, q, sub.PlanID)
	if err != nil {
		return gateState{}, err
	}
	today, err := t.today(r.clock.Now())
	if err != nil {
		return gateState{}, err
	}
	effective, _ := r.lifecycle.Advance(sub, plan, today)
	state := r.lifecycle.StateOf(effective.Status)
	g.sub, g.plan, g.hasSub, g.status = effective, plan, true, effective.Status
	g.canPost = g.canPost && state.CanPost()
	g.export = g.export && state.CanExport()
	return g, nil
}

// Resolve returns the tenant's entitlements now, read in q.
func (r *Reader) Resolve(ctx context.Context, q db.Querier, tenantID uuid.UUID) (Entitlements, error) {
	g, err := r.gate(ctx, q, tenantID)
	if err != nil {
		return Entitlements{}, err
	}
	e := Entitlements{Status: g.status, CanPost: g.canPost, CanExport: g.export}
	if !g.hasSub {
		return e, nil
	}
	today, err := g.tenant.today(r.clock.Now())
	if err != nil {
		return Entitlements{}, err
	}
	e.Modules, e.Limits = map[string]bool{}, map[string]int64{}
	for _, m := range g.plan.Modules {
		e.Modules[m] = true
	}
	for _, k := range entitlementLimits {
		if v, ok := g.plan.Limits[k]; ok {
			e.Limits[k] = v
		}
	}
	rows, err := activeEntitlementRows(ctx, q, tenantID, today)
	if err != nil {
		return Entitlements{}, err
	}
	for _, row := range rows {
		switch {
		case strings.HasPrefix(row.Key, "module:"):
			var on bool
			if json.Unmarshal(row.Value, &on) == nil {
				e.Modules[strings.TrimPrefix(row.Key, "module:")] = on
			}
		case strings.HasPrefix(row.Key, "limit:"):
			var n int64
			if json.Unmarshal(row.Value, &n) == nil {
				e.Limits[strings.TrimPrefix(row.Key, "limit:")] = n
			}
		}
	}
	if r.flags != nil {
		for _, m := range Modules {
			on, found, err := r.flags.Flag(ctx, q, ModuleFlagKey(m), tenantID)
			if err != nil {
				return Entitlements{}, err
			}
			if found {
				e.Modules[m] = on
			}
		}
	}
	for m, on := range e.Modules {
		if !on {
			delete(e.Modules, m)
		}
	}
	return e, nil
}
