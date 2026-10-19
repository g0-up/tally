// Package saas is the operator side of the service: plans, subscriptions and their lifecycle,
// entitlements and limits, service billing with idempotent payment callbacks, daily usage,
// account closure and the full data export. Its tables are platform tables without row
// security; every query filters tenant_id explicitly.
package saas

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Subscription statuses, the states of SubscriptionLifecycle.
const (
	StatusTrialing = "trialing"
	StatusActive   = "active"
	StatusGrace    = "grace"
	StatusReadOnly = "read_only"
	StatusClosed   = "closed"
)

// Modules a plan may include. UI hiding is cosmetic; the server gates each one.
const (
	ModulePOS             = "pos"
	ModuleKits            = "kits"
	ModuleCampaign        = "campaign"
	ModuleCRM             = "crm"
	ModuleReportsAdvanced = "reports_advanced"
	ModuleAIQA            = "ai_qa"
)

// Modules lists every module, in display order.
var Modules = []string{ModulePOS, ModuleKits, ModuleCampaign, ModuleCRM, ModuleReportsAdvanced, ModuleAIQA}

// ModuleFlagKey is the feature flag that turns module m on or off for a store over its plan.
func ModuleFlagKey(m string) string { return "module:" + m }

// FlagKeys are the feature flag keys the server reads: one per module, in display order.
func FlagKeys() []string {
	out := make([]string, len(Modules))
	for i, m := range Modules {
		out[i] = ModuleFlagKey(m)
	}
	return out
}

// Entitlement limits a plan sets.
const (
	LimitSKUs      = "skus"
	LimitStorageMB = "storage_mb"
)

// entitlementLimits are the plan limits that become entitlement rows; the other limits keys are
// published terms (durations, SLA).
var entitlementLimits = []string{LimitSKUs, LimitStorageMB}

// Published plan terms kept in plans.limits, with the defaults of a plan that omits them.
const (
	TermTrialDays       = "trial_days"
	TermGraceDays       = "grace_days"
	TermReadOnlyDays    = "read_only_days"
	TermSupportSLAHours = "support_sla_hours"
	TermPeriodDays      = "period_days"
)

var termDefaults = map[string]int64{
	TermTrialDays: 14, TermGraceDays: 7, TermReadOnlyDays: 60, TermSupportSLAHours: 48, TermPeriodDays: 120,
}

// Plan kinds and billing periods.
const (
	KindYearRound = "year_round"
	KindSeasonal  = "seasonal"
	KindAddon     = "addon"
	KindTrial     = "trial"

	PeriodMonth  = "month"
	PeriodSeason = "season"
	PeriodYear   = "year"
	PeriodNone   = "none"
)

// Invoice statuses and purposes.
const (
	InvoiceOpen = "open"
	InvoicePaid = "paid"
	InvoiceVoid = "void"

	PurposeNewPeriod  = "new_period"
	PurposeRenewal    = "renewal"
	PurposePlanChange = "plan_change"
	PurposeAddon      = "addon"
)

// TrialPlanCode is the plan every new store starts on.
const TrialPlanCode = "trial"

// Limits are the numeric plan limits and terms (plans.limits).
type Limits map[string]int64

// Scan implements sql.Scanner for a jsonb object of numbers.
func (l *Limits) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	case nil:
		*l = Limits{}
		return nil
	default:
		return fmt.Errorf("saas: scan limits: unsupported type %T", src)
	}
	out := Limits{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("saas: scan limits: %w", err)
	}
	*l = out
	return nil
}

// Value implements driver.Valuer.
func (l Limits) Value() (driver.Value, error) {
	if l == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]int64(l))
}

// Plan is a package of modules and limits. Prices are admin-editable; no price is hard-coded.
type Plan struct {
	ID            uuid.UUID           `db:"id"`
	Code          string              `db:"code"`
	Name          string              `db:"name"`
	Kind          string              `db:"kind"`
	BillingPeriod string              `db:"billing_period"`
	Price         money.Money         `db:"price"`
	Modules       db.JSONList[string] `db:"modules"`
	Limits        Limits              `db:"limits"`
	Active        bool                `db:"active"`
	CreatedAt     time.Time           `db:"created_at"`
	UpdatedAt     time.Time           `db:"updated_at"`
}

// Term returns a published plan term (days or hours), or its default.
func (p Plan) Term(key string) int {
	if v, ok := p.Limits[key]; ok && v >= 0 {
		return int(v)
	}
	return int(termDefaults[key])
}

// Includes reports whether the plan lists the module.
func (p Plan) Includes(module string) bool {
	for _, m := range p.Modules {
		if m == module {
			return true
		}
	}
	return false
}

// PeriodEnd is the last day of a billing period of the plan starting on start.
func (p Plan) PeriodEnd(start time.Time) time.Time {
	switch p.BillingPeriod {
	case PeriodMonth:
		return start.AddDate(0, 1, -1)
	case PeriodYear:
		return start.AddDate(1, 0, -1)
	case PeriodSeason:
		return start.AddDate(0, 0, p.Term(TermPeriodDays)-1)
	default:
		return start.AddDate(0, 0, p.Term(TermTrialDays)-1)
	}
}

// Subscription is a tenant's plan and lifecycle state. At most one subscription per tenant is
// not closed.
type Subscription struct {
	ID                 uuid.UUID  `db:"id"`
	TenantID           uuid.UUID  `db:"tenant_id"`
	PlanID             uuid.UUID  `db:"plan_id"`
	Status             string     `db:"status"`
	CurrentPeriodStart time.Time  `db:"current_period_start"`
	CurrentPeriodEnd   time.Time  `db:"current_period_end"`
	TrialEndsOn        *time.Time `db:"trial_ends_on"`
	GraceEndsOn        *time.Time `db:"grace_ends_on"`
	ReadOnlyEndsOn     *time.Time `db:"read_only_ends_on"`
	CancelAtPeriodEnd  bool       `db:"cancel_at_period_end"`
	CreatedAt          time.Time  `db:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at"`
	Version            int        `db:"version"`
}

// Invoice is a service invoice of the platform, separate from the store's own finance.
type Invoice struct {
	ID             uuid.UUID   `db:"id"`
	TenantID       uuid.UUID   `db:"tenant_id"`
	Number         string      `db:"number"`
	SubscriptionID *uuid.UUID  `db:"subscription_id"`
	PlanID         *uuid.UUID  `db:"plan_id"`
	Purpose        string      `db:"purpose"`
	PeriodStart    time.Time   `db:"period_start"`
	PeriodEnd      time.Time   `db:"period_end"`
	Amount         money.Money `db:"amount"`
	ProrationNote  *string     `db:"proration_note"`
	Status         string      `db:"status"`
	Provider       *string     `db:"provider"`
	ProviderRef    *string     `db:"provider_ref"`
	IssuedAt       time.Time   `db:"issued_at"`
	PaidAt         *time.Time  `db:"paid_at"`
}

// Entitlements are a tenant's resolved modules and limits on one day.
type Entitlements struct {
	// Status is the effective subscription status; "" when the tenant has no subscription
	// (sandboxes, stores created before billing), which is unrestricted.
	Status    string           `json:"status"`
	Modules   map[string]bool  `json:"modules"`
	Limits    map[string]int64 `json:"limits"`
	CanPost   bool             `json:"can_post"`
	CanExport bool             `json:"can_export"`
}

// Has reports whether the module is enabled.
func (e Entitlements) Has(module string) bool {
	if e.Modules == nil {
		return true
	}
	return e.Modules[module]
}
