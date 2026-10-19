package saas

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

var planCodePattern = regexp.MustCompile(`^[a-z0-9_]{2,40}$`)

// PlanInput is a new plan or the full new state of an edited one. Prices are decided by the
// operator; nothing is hard-coded.
type PlanInput struct {
	Code          string
	Name          string
	Kind          string
	BillingPeriod string
	Price         money.Money
	Modules       []string
	// Limits holds the entitlement limits (skus, storage_mb) and the published terms (trial,
	// grace and read-only days, support SLA hours, season length).
	Limits map[string]int64
	Active bool
}

func (in PlanInput) validate() error {
	fields := map[string]string{}
	if !planCodePattern.MatchString(in.Code) {
		fields["code"] = "invalid"
	}
	if name := strings.TrimSpace(in.Name); name == "" || utf8.RuneCountInString(name) > 120 {
		fields["name"] = "invalid"
	}
	if !slices.Contains([]string{KindYearRound, KindSeasonal, KindAddon, KindTrial}, in.Kind) {
		fields["kind"] = "invalid"
	}
	if !slices.Contains([]string{PeriodMonth, PeriodSeason, PeriodYear, PeriodNone}, in.BillingPeriod) {
		fields["billing_period"] = "invalid"
	}
	if in.Price.IsNegative() {
		fields["price"] = "negative"
	}
	for _, m := range in.Modules {
		if !slices.Contains(Modules, m) {
			fields["modules"] = "invalid"
		}
	}
	for k, v := range in.Limits {
		_, term := termDefaults[k]
		if (!term && !slices.Contains(entitlementLimits, k)) || v < 0 {
			fields["limits"] = "invalid"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

func uniqueModules(in []string) []string {
	out := []string{}
	for _, m := range Modules {
		if slices.Contains(in, m) {
			out = append(out, m)
		}
	}
	return out
}

// CreatePlan adds a plan (409 plan_code_taken when the code exists). Plans are platform rows
// without a tenant, so the change is logged rather than written to a tenant audit log.
func (s *Service) CreatePlan(ctx context.Context, adminID uuid.UUID, in PlanInput) (Plan, error) {
	if err := in.validate(); err != nil {
		return Plan{}, err
	}
	now := s.clock.Now()
	p := Plan{
		ID: ids.New(), Code: in.Code, Name: strings.TrimSpace(in.Name), Kind: in.Kind, BillingPeriod: in.BillingPeriod,
		Price: in.Price, Modules: uniqueModules(in.Modules), Limits: Limits(in.Limits), Active: in.Active,
		CreatedAt: now, UpdatedAt: now,
	}
	if p.Limits == nil {
		p.Limits = Limits{}
	}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO plans (id, code, name, kind, billing_period, price, modules, limits, active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
			p.ID, p.Code, p.Name, p.Kind, p.BillingPeriod, p.Price, []string(p.Modules), p.Limits, p.Active, now)
		if db.IsUniqueViolation(err, "ux_plans__code") {
			return errPlanCodeTaken()
		}
		if err != nil {
			return fmt.Errorf("saas: insert plan: %w", err)
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	s.logger.Info("plan created", "plan_id", p.ID.String(), "code", p.Code, "admin_id", adminID.String())
	return p, nil
}

// UpdatePlan replaces a plan's editable state. Running subscriptions keep their period; the new
// modules and limits apply to them at once (entitlements read the plan live) and the new terms
// from their next lifecycle transition.
func (s *Service) UpdatePlan(ctx context.Context, adminID, id uuid.UUID, in PlanInput) (Plan, error) {
	if err := in.validate(); err != nil {
		return Plan{}, err
	}
	var out Plan
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		cur, err := readPlan(ctx, q, id)
		if err != nil {
			return err
		}
		limits := Limits(in.Limits)
		if limits == nil {
			limits = Limits{}
		}
		now := s.clock.Now()
		_, err = q.ExecContext(ctx, `
			UPDATE plans SET code = $2, name = $3, kind = $4, billing_period = $5, price = $6, modules = $7, limits = $8,
			  active = $9, updated_at = $10
			WHERE id = $1`,
			id, in.Code, strings.TrimSpace(in.Name), in.Kind, in.BillingPeriod, in.Price, uniqueModules(in.Modules), limits,
			in.Active, now)
		if db.IsUniqueViolation(err, "ux_plans__code") {
			return errPlanCodeTaken()
		}
		if err != nil {
			return fmt.Errorf("saas: update plan: %w", err)
		}
		out, err = readPlan(ctx, q, id)
		if err == nil {
			s.logger.Info("plan updated", "plan_id", id.String(), "code_before", cur.Code, "code", out.Code,
				"price_before", cur.Price.String(), "price", out.Price.String(), "admin_id", adminID.String())
		}
		return err
	})
	return out, err
}

// Plan returns one plan.
func (s *Service) Plan(ctx context.Context, id uuid.UUID) (Plan, error) {
	var out Plan
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		out, err = readPlan(ctx, q, id)
		return err
	})
	return out, err
}
