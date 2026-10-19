package saas

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

const planColumns = `id, code, name, kind, billing_period, price, to_jsonb(modules) AS modules, limits, active, created_at, updated_at`

const subscriptionColumns = `id, tenant_id, plan_id, status, current_period_start, current_period_end, trial_ends_on,
	grace_ends_on, read_only_ends_on, cancel_at_period_end, created_at, updated_at, version`

const invoiceColumns = `id, tenant_id, number, subscription_id, plan_id, purpose, period_start, period_end, amount,
	proration_note, status, provider, provider_ref, issued_at, paid_at`

func readPlan(ctx context.Context, q db.Querier, id uuid.UUID) (Plan, error) {
	var p Plan
	err := sqlx.GetContext(ctx, q, &p, `SELECT `+planColumns+` FROM plans WHERE id = $1`, id)
	if db.IsNoRows(err) {
		return Plan{}, errNotFound("Plan")
	}
	if err != nil {
		return Plan{}, fmt.Errorf("saas: read plan: %w", err)
	}
	return p, nil
}

func readPlanByCode(ctx context.Context, q db.Querier, code string) (Plan, error) {
	var p Plan
	err := sqlx.GetContext(ctx, q, &p, `SELECT `+planColumns+` FROM plans WHERE code = $1`, code)
	if db.IsNoRows(err) {
		return Plan{}, errNotFound("Plan")
	}
	if err != nil {
		return Plan{}, fmt.Errorf("saas: read plan: %w", err)
	}
	return p, nil
}

func listPlans(ctx context.Context, q db.Querier, activeOnly bool) ([]Plan, error) {
	query := `SELECT ` + planColumns + ` FROM plans`
	if activeOnly {
		query += ` WHERE active`
	}
	query += ` ORDER BY kind = 'trial' DESC, kind = 'addon', code`
	var out []Plan
	if err := sqlx.SelectContext(ctx, q, &out, query); err != nil {
		return nil, fmt.Errorf("saas: list plans: %w", err)
	}
	return out, nil
}

// readSubscription returns the tenant's current subscription: the one that is not closed, else
// the latest closed one. found is false for a tenant without any.
func readSubscription(ctx context.Context, q db.Querier, tenantID uuid.UUID, forUpdate bool) (Subscription, bool, error) {
	query := `SELECT ` + subscriptionColumns + ` FROM subscriptions WHERE tenant_id = $1
		ORDER BY status <> 'closed' DESC, created_at DESC, id DESC LIMIT 1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var s Subscription
	err := sqlx.GetContext(ctx, q, &s, query, tenantID)
	if db.IsNoRows(err) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, fmt.Errorf("saas: read subscription: %w", err)
	}
	return s, true, nil
}

func insertSubscription(ctx context.Context, q db.Querier, s Subscription) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO subscriptions (id, tenant_id, plan_id, status, current_period_start, current_period_end, trial_ends_on,
		  grace_ends_on, read_only_ends_on, cancel_at_period_end, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, 1)`,
		s.ID, s.TenantID, s.PlanID, s.Status, s.CurrentPeriodStart, s.CurrentPeriodEnd, s.TrialEndsOn, s.GraceEndsOn,
		s.ReadOnlyEndsOn, s.CancelAtPeriodEnd, s.CreatedAt); err != nil {
		return fmt.Errorf("saas: insert subscription: %w", err)
	}
	return nil
}

// updateSubscription writes s over its row and bumps the version.
func updateSubscription(ctx context.Context, q db.Querier, s *Subscription, now time.Time) error {
	if err := q.QueryRowxContext(ctx, `
		UPDATE subscriptions SET plan_id = $2, status = $3, current_period_start = $4, current_period_end = $5,
		  trial_ends_on = $6, grace_ends_on = $7, read_only_ends_on = $8, cancel_at_period_end = $9, updated_at = $10,
		  version = version + 1
		WHERE id = $1 RETURNING version`,
		s.ID, s.PlanID, s.Status, s.CurrentPeriodStart, s.CurrentPeriodEnd, s.TrialEndsOn, s.GraceEndsOn, s.ReadOnlyEndsOn,
		s.CancelAtPeriodEnd, now).Scan(&s.Version); err != nil {
		return fmt.Errorf("saas: update subscription: %w", err)
	}
	s.UpdatedAt = now
	return nil
}

// tenantState is what the gates read of the tenant row.
type tenantState struct {
	ID          uuid.UUID `db:"id"`
	Slug        string    `db:"slug"`
	DisplayName string    `db:"display_name"`
	Status      string    `db:"status"`
	Maintenance bool      `db:"maintenance"`
	Timezone    string    `db:"timezone"`
	IsSandbox   bool      `db:"is_sandbox"`
}

func readTenantState(ctx context.Context, q db.Querier, tenantID uuid.UUID, forUpdate bool) (tenantState, error) {
	query := `SELECT id, slug, display_name, status, maintenance, timezone, is_sandbox FROM tenants WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var t tenantState
	err := sqlx.GetContext(ctx, q, &t, query, tenantID)
	if db.IsNoRows(err) {
		return tenantState{}, errNotFound("Tenant")
	}
	if err != nil {
		return tenantState{}, fmt.Errorf("saas: read tenant: %w", err)
	}
	return t, nil
}

// today is the tenant's business date at now.
func (t tenantState) today(now time.Time) (time.Time, error) {
	loc, err := clock.LoadLocation(t.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}

// syncTenantStatus moves tenants.status to the one the subscription status implies. A tenant
// that is closing or replaced keeps its status, and so does one whose latest closure request
// completed (closedOnRequest).
func syncTenantStatus(ctx context.Context, q db.Querier, tenantID uuid.UUID, subStatus string, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE tenants SET status = $2, updated_at = $3, version = version + 1
		WHERE id = $1 AND status IN ('active', 'read_only', 'closed') AND status <> $2
		  AND NOT EXISTS (
		    SELECT 1 FROM (
		      SELECT status FROM account_closure_requests WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1
		    ) latest WHERE latest.status = 'completed')`,
		tenantID, tenantStatusFor(subStatus), now); err != nil {
		return fmt.Errorf("saas: update tenant status: %w", err)
	}
	return nil
}

// entitlementRow is one active entitlements row.
type entitlementRow struct {
	Key   string          `db:"key"`
	Value json.RawMessage `db:"value"`
}

func activeEntitlementRows(ctx context.Context, q db.Querier, tenantID uuid.UUID, today time.Time) ([]entitlementRow, error) {
	var rows []entitlementRow
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT key, value FROM entitlements
		WHERE tenant_id = $1 AND valid_from <= $2 AND (valid_to IS NULL OR valid_to >= $2)
		ORDER BY valid_from, created_at`, tenantID, today); err != nil {
		return nil, fmt.Errorf("saas: read entitlements: %w", err)
	}
	return rows, nil
}

// activeAddons reads the add-ons in force on day today from the entitlement rows their purchases
// granted, the rows Resolve enables their modules from: one per add-on plan and period, its
// modules by name, earliest period first.
func activeAddons(ctx context.Context, q db.Querier, tenantID uuid.UUID, today time.Time) ([]ActiveAddon, error) {
	var rows []struct {
		PlanID    uuid.UUID `db:"source_plan_id"`
		PlanName  string    `db:"name"`
		Key       string    `db:"key"`
		ValidFrom time.Time `db:"valid_from"`
		ValidTo   time.Time `db:"valid_to"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT e.source_plan_id, p.name, e.key, e.valid_from, e.valid_to
		FROM entitlements e JOIN plans p ON p.id = e.source_plan_id
		WHERE e.tenant_id = $1 AND e.source_subscription_id IS NULL AND e.key LIKE 'module:%' AND e.value = 'true'
		  AND e.valid_from <= $2 AND e.valid_to >= $2
		ORDER BY e.valid_from, e.valid_to, p.name, e.source_plan_id, e.key`, tenantID, today); err != nil {
		return nil, fmt.Errorf("saas: read add-ons: %w", err)
	}
	out := []ActiveAddon{}
	for _, r := range rows {
		module := strings.TrimPrefix(r.Key, "module:")
		if n := len(out); n > 0 && out[n-1].PlanID == r.PlanID &&
			out[n-1].PeriodStart.Equal(r.ValidFrom) && out[n-1].PeriodEnd.Equal(r.ValidTo) {
			out[n-1].Modules = append(out[n-1].Modules, module)
			continue
		}
		out = append(out, ActiveAddon{
			PlanID: r.PlanID, PlanName: r.PlanName, Modules: []string{module},
			PeriodStart: r.ValidFrom, PeriodEnd: r.ValidTo,
		})
	}
	return out, nil
}

// syncPlanEntitlements makes the plan's modules and limits the plan-sourced entitlement rows of
// the tenant from day from: later plan rows are dropped, current ones end the day before and the
// plan's rows start on from. Add-on rows (no source subscription) are kept.
func syncPlanEntitlements(ctx context.Context, q db.Querier, s Subscription, p Plan, from time.Time, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		DELETE FROM entitlements WHERE tenant_id = $1 AND source_subscription_id IS NOT NULL AND valid_from >= $2`,
		s.TenantID, from); err != nil {
		return fmt.Errorf("saas: drop plan entitlements: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE entitlements SET valid_to = $2::date - 1
		WHERE tenant_id = $1 AND source_subscription_id IS NOT NULL AND (valid_to IS NULL OR valid_to >= $2)`,
		s.TenantID, from); err != nil {
		return fmt.Errorf("saas: end plan entitlements: %w", err)
	}
	type row struct {
		key   string
		value any
	}
	var rows []row
	for _, m := range p.Modules {
		rows = append(rows, row{"module:" + m, true})
	}
	for _, k := range entitlementLimits {
		if v, ok := p.Limits[k]; ok {
			rows = append(rows, row{"limit:" + k, v})
		}
	}
	for _, r := range rows {
		v, err := json.Marshal(r.value)
		if err != nil {
			return fmt.Errorf("saas: encode entitlement: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO entitlements (id, tenant_id, key, value, source_subscription_id, valid_from, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT ON CONSTRAINT ux_entitlements__key_valid_from_source DO NOTHING`,
			ids.New(), s.TenantID, r.key, v, s.ID, from, now); err != nil {
			return fmt.Errorf("saas: insert entitlement: %w", err)
		}
	}
	return nil
}

// addonStart is the first day a new purchase of the add-on addonID covers: the day after the
// tenant's period of that add-on ends when the period runs through today or later, else today.
// Purchases of one add-on so follow each other and never overlap.
func addonStart(ctx context.Context, q db.Querier, tenantID, addonID uuid.UUID, today time.Time) (time.Time, error) {
	var ends []time.Time
	if err := sqlx.SelectContext(ctx, q, &ends, `
		SELECT valid_to FROM entitlements
		WHERE tenant_id = $1 AND source_plan_id = $2 AND source_subscription_id IS NULL AND valid_to >= $3
		ORDER BY valid_to DESC LIMIT 1`, tenantID, addonID, today); err != nil {
		return time.Time{}, fmt.Errorf("saas: read add-on period: %w", err)
	}
	if len(ends) == 0 {
		return today, nil
	}
	return ends[0].AddDate(0, 0, 1), nil
}

// grantAddon enables module from day from through day to, apart from the plan, as granted by
// the add-on plan addonID: a row of its own per period. Periods of one add-on start after one
// another (addonStart), so a grant meeting a row of the same start is that grant again and keeps
// the longer end.
func grantAddon(ctx context.Context, q db.Querier, tenantID, addonID uuid.UUID, module string, from, to, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO entitlements (id, tenant_id, key, value, source_plan_id, valid_from, valid_to, created_at)
		VALUES ($1, $2, $3, 'true', $4, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT ux_entitlements__key_valid_from_source DO UPDATE
		  SET valid_to = GREATEST(entitlements.valid_to, EXCLUDED.valid_to), value = 'true'`,
		ids.New(), tenantID, "module:"+module, addonID, from, to, now); err != nil {
		return fmt.Errorf("saas: grant add-on: %w", err)
	}
	return nil
}

func readInvoice(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Invoice, error) {
	query := `SELECT ` + invoiceColumns + ` FROM saas_invoices WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var inv Invoice
	err := sqlx.GetContext(ctx, q, &inv, query, id)
	if db.IsNoRows(err) {
		return Invoice{}, errNotFound("Invoice")
	}
	if err != nil {
		return Invoice{}, fmt.Errorf("saas: read invoice: %w", err)
	}
	return inv, nil
}

func readInvoiceByNumber(ctx context.Context, q db.Querier, number string, forUpdate bool) (Invoice, error) {
	query := `SELECT ` + invoiceColumns + ` FROM saas_invoices WHERE number = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var inv Invoice
	err := sqlx.GetContext(ctx, q, &inv, query, number)
	if db.IsNoRows(err) {
		return Invoice{}, errNotFound("Invoice")
	}
	if err != nil {
		return Invoice{}, fmt.Errorf("saas: read invoice: %w", err)
	}
	return inv, nil
}
