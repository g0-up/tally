package org

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Onboarding step keys.
const (
	StepStoreProfile    = "store_profile"
	StepCatalogSetup    = "catalog_setup"
	StepOpeningStock    = "opening_stock"
	StepOpeningDebt     = "opening_debt"
	StepOpeningCash     = "opening_cash"
	StepFirstSale       = "first_sale"
	StepConfirmOpenings = "confirm_opening_balances"
)

// Onboarding step statuses.
const (
	StepPending   = "pending"
	StepCompleted = "completed"
	StepSkipped   = "skipped"
)

var (
	// skippableSteps may be skipped; every other step is completed.
	skippableSteps = []string{StepCatalogSetup, StepFirstSale}
	// eventSteps complete only from what the store does (the first POS sale), never by hand.
	eventSteps = []string{StepFirstSale}
	// openingSteps must be completed before the opening balances are confirmed.
	openingSteps = []string{StepOpeningStock, StepOpeningDebt, StepOpeningCash}
)

// TopicSalePosted is the sales event the first_sale step listens to.
const TopicSalePosted = "sale.posted"

// posSources are the sale sources that issue an HD (POS sale).
var posSources = []string{"pos", "pos_offline"}

// OnboardingStep is one checklist step.
type OnboardingStep struct {
	Key         string     `db:"step_key"`
	CompletedAt *time.Time `db:"completed_at"`
	CompletedBy *uuid.UUID `db:"completed_by"`
	SkippedAt   *time.Time `db:"skipped_at"`
	SkippedBy   *uuid.UUID `db:"skipped_by"`
}

// Status is pending, completed or skipped.
func (s OnboardingStep) Status() string {
	switch {
	case s.CompletedAt != nil:
		return StepCompleted
	case s.SkippedAt != nil:
		return StepSkipped
	}
	return StepPending
}

// Skippable reports whether the step may be skipped.
func (s OnboardingStep) Skippable() bool { return slices.Contains(skippableSteps, s.Key) }

// ColorValue is the stock value of one colour (nil colour for goods without one) at the cutoff.
type ColorValue struct {
	ColorID   *uuid.UUID  `db:"color_id"`
	ColorCode *string     `db:"color_code"`
	ColorName *string     `db:"color_name"`
	Qty       money.Qty   `db:"qty"`
	Value     money.Money `db:"value"`
}

// FundValue is the balance of one fund at the end of the cutoff date.
type FundValue struct {
	ID      uuid.UUID   `db:"id"`
	Name    string      `db:"name"`
	Kind    string      `db:"kind"`
	Balance money.Money `db:"balance"`
}

// OpeningTotals are the balances the owner confirms, all at the end of the cutoff date.
type OpeningTotals struct {
	Cutoff           time.Time
	StockByColor     []ColorValue
	StockValue       money.Money
	Receivables      money.Money
	Payables         money.Money
	CustomerAdvances money.Money
	SupplierAdvances money.Money
	Funds            []FundValue
}

// Onboarding is the checklist with its progress and, once the cutoff date is set, the opening
// totals to confirm.
type Onboarding struct {
	Steps    []OnboardingStep
	Done     int
	Total    int
	Complete bool
	// LockedThrough is the period lock in effect, if any.
	LockedThrough *time.Time
	Totals        *OpeningTotals
}

const onboardingColumns = `step_key, completed_at, completed_by, skipped_at, skipped_by`

func stepOrder(key string) int { return slices.Index(OnboardingSteps, key) }

func readSteps(ctx context.Context, q db.Querier, tenantID uuid.UUID) ([]OnboardingStep, error) {
	var steps []OnboardingStep
	if err := sqlx.SelectContext(ctx, q, &steps, `SELECT `+onboardingColumns+` FROM onboarding_steps WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("org: read onboarding: %w", err)
	}
	slices.SortFunc(steps, func(a, b OnboardingStep) int { return stepOrder(a.Key) - stepOrder(b.Key) })
	return steps, nil
}

// Onboarding returns the tenant's checklist.
func (s *Service) Onboarding(ctx context.Context, scope Scope) (Onboarding, error) {
	var out Onboarding
	err := s.read(ctx, scope, func(q db.Querier) error {
		steps, err := readSteps(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		out.Steps, out.Total = steps, len(steps)
		for _, st := range steps {
			if st.Status() != StepPending {
				out.Done++
			}
		}
		out.Complete = out.Total > 0 && out.Done == out.Total
		latest, _, err := latestLock(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		out.LockedThrough = currentLock(latest)
		tenant, err := readTenant(ctx, q, scope.TenantID, false)
		if err != nil {
			return err
		}
		if tenant.OpeningCutoffDate == nil {
			return nil
		}
		totals, err := openingTotals(ctx, q, clock.TruncateDate(*tenant.OpeningCutoffDate))
		if err != nil {
			return err
		}
		out.Totals = &totals
		return nil
	})
	return out, err
}

// openingTotals sums the opening position at the end of cutoff: stock value per colour, open
// receivables and payables, unallocated advances and fund balances. Advances use the current
// allocation of the payments dated up to the cutoff.
func openingTotals(ctx context.Context, q db.Querier, cutoff time.Time) (OpeningTotals, error) {
	out := OpeningTotals{Cutoff: cutoff, StockByColor: []ColorValue{}, Funds: []FundValue{}}
	if err := sqlx.SelectContext(ctx, q, &out.StockByColor, `
		SELECT m.color_id, c.code AS color_code, c.name AS color_name, SUM(m.qty) AS qty, SUM(m.value_amount) AS value
		FROM stock_moves m LEFT JOIN colors c ON c.id = m.color_id
		WHERE m.business_date <= $1
		GROUP BY m.color_id, c.code, c.name, c.sort_order
		HAVING SUM(m.qty) <> 0 OR SUM(m.value_amount) <> 0
		ORDER BY c.sort_order NULLS LAST, c.code NULLS LAST`, cutoff); err != nil {
		return OpeningTotals{}, fmt.Errorf("org: opening stock by colour: %w", err)
	}
	for _, c := range out.StockByColor {
		out.StockValue = out.StockValue.Add(c.Value)
	}
	err := q.QueryRowxContext(ctx, `
		SELECT
		  COALESCE(SUM(o.amount) FILTER (WHERE o.direction = 'receivable'), 0)
		    - COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s JOIN obligations so ON so.id = s.obligation_id
		                WHERE so.direction = 'receivable' AND so.business_date <= $1 AND s.business_date <= $1), 0),
		  COALESCE(SUM(o.amount) FILTER (WHERE o.direction = 'payable'), 0)
		    - COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s JOIN obligations so ON so.id = s.obligation_id
		                WHERE so.direction = 'payable' AND so.business_date <= $1 AND s.business_date <= $1), 0),
		  (SELECT COALESCE(SUM(p.amount - p.allocated_amount), 0) FROM payments p
		   WHERE p.direction = 'in' AND p.status = 'confirmed' AND p.party_id IS NOT NULL AND p.business_date <= $1),
		  (SELECT COALESCE(SUM(p.amount - p.allocated_amount), 0) FROM payments p
		   WHERE p.direction = 'out' AND p.status = 'confirmed' AND p.party_id IS NOT NULL AND p.business_date <= $1)
		FROM obligations o WHERE o.business_date <= $1`, cutoff).
		Scan(&out.Receivables, &out.Payables, &out.CustomerAdvances, &out.SupplierAdvances)
	if err != nil {
		return OpeningTotals{}, fmt.Errorf("org: opening debt totals: %w", err)
	}
	if err := sqlx.SelectContext(ctx, q, &out.Funds, `
		SELECT f.id, f.name, f.kind,
		  f.opening_balance + COALESCE((SELECT SUM(e.amount) FROM cash_entries e
		    WHERE e.fund_account_id = f.id AND e.business_date <= $1), 0) AS balance
		FROM fund_accounts f ORDER BY f.kind, f.name, f.id`, cutoff); err != nil {
		return OpeningTotals{}, fmt.Errorf("org: opening fund balances: %w", err)
	}
	return out, nil
}

func errUnknownStep() error { return apperr.NewNotFound("not_found", "Onboarding step not found") }

// stepForUpdate locks and returns one step of the tenant.
func stepForUpdate(ctx context.Context, q db.Querier, tenantID uuid.UUID, key string) (OnboardingStep, error) {
	var st OnboardingStep
	err := sqlx.GetContext(ctx, q, &st, `SELECT `+onboardingColumns+` FROM onboarding_steps
		WHERE tenant_id = $1 AND step_key = $2 FOR UPDATE`, tenantID, key)
	if db.IsNoRows(err) {
		return OnboardingStep{}, errUnknownStep()
	}
	if err != nil {
		return OnboardingStep{}, fmt.Errorf("org: read onboarding step: %w", err)
	}
	return st, nil
}

// CompleteStep marks a step done by hand: the owner declares the store profile, catalogue or an
// opening balance finished (an opening step also completes itself when the balance is posted).
// first_sale completes only from a POS sale; confirm_opening_balances goes through
// ConfirmOpeningBalances. Completing a done step is a no-op.
func (s *Service) CompleteStep(ctx context.Context, actor audit.Actor, key string) (OnboardingStep, error) {
	if !slices.Contains(OnboardingSteps, key) {
		return OnboardingStep{}, errUnknownStep()
	}
	if key == StepConfirmOpenings {
		return OnboardingStep{}, apperr.NewConflict("confirmation_required", "Confirm the opening balances to complete this step")
	}
	if slices.Contains(eventSteps, key) {
		return OnboardingStep{}, apperr.NewConflict("step_not_manual", "This step completes with the first sale; it can be skipped")
	}
	return s.changeStep(ctx, actor, key, func(_ db.Querier, st *OnboardingStep, now time.Time) error {
		if st.CompletedAt != nil {
			return nil
		}
		st.CompletedAt, st.CompletedBy = &now, &actor.ID
		return nil
	})
}

// SkipStep skips catalog_setup or first_sale; other steps answer 409 step_not_skippable.
// Skipping a done or skipped step is a no-op.
func (s *Service) SkipStep(ctx context.Context, actor audit.Actor, key string) (OnboardingStep, error) {
	if !slices.Contains(OnboardingSteps, key) {
		return OnboardingStep{}, errUnknownStep()
	}
	if !slices.Contains(skippableSteps, key) {
		return OnboardingStep{}, apperr.NewConflict("step_not_skippable", "This onboarding step cannot be skipped")
	}
	return s.changeStep(ctx, actor, key, func(_ db.Querier, st *OnboardingStep, now time.Time) error {
		if st.CompletedAt != nil || st.SkippedAt != nil {
			return nil
		}
		st.SkippedAt, st.SkippedBy = &now, &actor.ID
		return nil
	})
}

// changeStep applies fn to the locked step and stores and audits the change when there is one.
func (s *Service) changeStep(ctx context.Context, actor audit.Actor, key string,
	fn func(q db.Querier, st *OnboardingStep, now time.Time) error,
) (OnboardingStep, error) {
	var out OnboardingStep
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		st, err := stepForUpdate(ctx, q, actor.TenantID, key)
		if err != nil {
			return err
		}
		before := st.Status()
		if err := fn(q, &st, s.clock.Now()); err != nil {
			return err
		}
		out = st
		if st.Status() == before {
			return nil
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE onboarding_steps SET completed_at = $3, completed_by = $4, skipped_at = $5, skipped_by = $6
			WHERE tenant_id = $1 AND step_key = $2`,
			actor.TenantID, key, st.CompletedAt, st.CompletedBy, st.SkippedAt, st.SkippedBy); err != nil {
			return fmt.Errorf("org: update onboarding step: %w", err)
		}
		action := "onboarding.step_completed"
		if st.Status() == StepSkipped {
			action = "onboarding.step_skipped"
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: action, EntityType: "onboarding_step", EntityID: actor.TenantID,
			Before: map[string]any{"step": key, "status": before},
			After:  map[string]any{"step": key, "status": st.Status()},
		})
	})
	return out, err
}

// ConfirmOpeningBalances confirms the opening totals and locks every period up to the cutoff
// date, so nothing can be posted before the confirmed opening position. The opening stock, debt
// and cash steps must be completed first. The lock only moves forward: a lock already at or past
// the cutoff is kept. Confirming twice is a no-op. The caller has re-authenticated.
func (s *Service) ConfirmOpeningBalances(ctx context.Context, actor audit.Actor) (OnboardingStep, OpeningTotals, error) {
	var (
		out    OnboardingStep
		totals OpeningTotals
	)
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		tenant, err := readTenant(ctx, q, actor.TenantID, true)
		if err != nil {
			return err
		}
		st, err := stepForUpdate(ctx, q, actor.TenantID, StepConfirmOpenings)
		if err != nil {
			return err
		}
		if st.CompletedAt != nil {
			out = st
			return nil
		}
		if tenant.OpeningCutoffDate == nil {
			return apperr.NewValidation("opening_cutoff_required", "Set the opening cutoff date first",
				map[string]string{"opening_cutoff_date": "required"})
		}
		cutoff := clock.TruncateDate(*tenant.OpeningCutoffDate)
		loc, err := tenantLocation(tenant)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if cutoff.After(clock.BusinessDate(now, loc)) {
			return apperr.NewValidation("opening_cutoff_in_future", "The opening cutoff date cannot be in the future",
				map[string]string{"opening_cutoff_date": "in_future"})
		}
		steps, err := readSteps(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		var pending []string
		for _, other := range steps {
			if slices.Contains(openingSteps, other.Key) && other.CompletedAt == nil {
				pending = append(pending, other.Key)
			}
		}
		if len(pending) > 0 {
			e := apperr.NewConflict("onboarding_steps_pending", "Finish the opening stock, debt and cash steps first")
			e.Fields = map[string]string{}
			for _, k := range pending {
				e.Fields[k] = "pending"
			}
			return e
		}
		if totals, err = openingTotals(ctx, q, cutoff); err != nil {
			return err
		}
		latest, _, err := latestLock(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		current := currentLock(latest)
		if current == nil || cutoff.After(*current) {
			if _, err := s.insertLock(ctx, q, actor, current, cutoff, now); err != nil {
				return err
			}
		}
		st.CompletedAt, st.CompletedBy = &now, &actor.ID
		if _, err := q.ExecContext(ctx, `
			UPDATE onboarding_steps SET completed_at = $3, completed_by = $4 WHERE tenant_id = $1 AND step_key = $2`,
			actor.TenantID, StepConfirmOpenings, now, actor.ID); err != nil {
			return fmt.Errorf("org: complete opening confirmation: %w", err)
		}
		out = st
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "onboarding.opening_confirmed", EntityType: "onboarding_step", EntityID: actor.TenantID,
			After: map[string]any{
				"cutoff": cutoff.Format(time.DateOnly), "stock_value": totals.StockValue, "receivables": totals.Receivables,
				"payables": totals.Payables, "customer_advances": totals.CustomerAdvances,
				"supplier_advances": totals.SupplierAdvances,
			},
		})
	})
	return out, totals, err
}

// OnboardingHandlers are the outbox handlers of the checklist: the first POS sale (HD) completes
// first_sale, even when it was skipped.
func OnboardingHandlers() map[string]outbox.Handler {
	return map[string]outbox.Handler{TopicSalePosted: handleFirstSale}
}

func handleFirstSale(ctx context.Context, q db.Querier, msg outbox.Message) error {
	var ev struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(msg.Payload, &ev); err != nil {
		return fmt.Errorf("org: decode sale event: %w", err)
	}
	if !slices.Contains(posSources, ev.Source) {
		return nil
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE onboarding_steps SET completed_at = $3 WHERE tenant_id = $1 AND step_key = $2 AND completed_at IS NULL`,
		msg.TenantID, StepFirstSale, msg.CreatedAt); err != nil {
		return fmt.Errorf("org: complete first sale step: %w", err)
	}
	return nil
}

// OnboardingHandler serves the onboarding checklist.
type OnboardingHandler struct {
	svc *Service
}

// NewOnboardingHandler returns the HTTP adapter of the checklist.
func NewOnboardingHandler(svc *Service) *OnboardingHandler { return &OnboardingHandler{svc: svc} }

// OnboardingRoutes are the route groups the composition root hands to Register.
type OnboardingRoutes struct {
	Tenant *gin.RouterGroup
	// Keyed returns the custom-method router of a resource whose items are keys.
	Keyed  func(group *gin.RouterGroup, resource, keyName string) *httpx.CommandRouter
	Reauth gin.HandlerFunc
}

// Register adds GET /onboarding and POST /onboarding/steps/{stepKey}:complete|skip.
func (h *OnboardingHandler) Register(r OnboardingRoutes) {
	if r.Tenant == nil || r.Keyed == nil || r.Reauth == nil {
		panic("org: onboarding Register needs the tenant group, the keyed command router and the re-auth guard")
	}
	r.Tenant.GET("/onboarding", h.get)
	steps := r.Keyed(r.Tenant, "onboarding/steps", "stepKey")
	steps.Item("complete", func(c *gin.Context) {
		if httpx.CommandKey(c) == StepConfirmOpenings {
			r.Reauth(c)
			if c.IsAborted() {
				return
			}
		}
		h.complete(c)
	})
	steps.Item("skip", h.skip)
}

func (h *OnboardingHandler) get(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	o, err := h.svc.Onboarding(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toOnboardingDTO(o))
}

func (h *OnboardingHandler) complete(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	key := httpx.CommandKey(c)
	if key == StepConfirmOpenings {
		st, totals, err := h.svc.ConfirmOpeningBalances(c.Request.Context(), actor)
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		out := toStepDTO(st)
		t := toTotalsDTO(totals)
		if !totals.Cutoff.IsZero() {
			out.Totals = &t
		}
		c.JSON(http.StatusOK, out)
		return
	}
	st, err := h.svc.CompleteStep(c.Request.Context(), actor, key)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStepDTO(st))
}

func (h *OnboardingHandler) skip(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	st, err := h.svc.SkipStep(c.Request.Context(), actor, httpx.CommandKey(c))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStepDTO(st))
}

type stepDTO struct {
	Key         string            `json:"key"`
	Status      string            `json:"status"`
	Skippable   bool              `json:"skippable"`
	CompletedAt *time.Time        `json:"completed_at"`
	SkippedAt   *time.Time        `json:"skipped_at"`
	Totals      *openingTotalsDTO `json:"totals,omitempty"`
}

func toStepDTO(s OnboardingStep) stepDTO {
	return stepDTO{Key: s.Key, Status: s.Status(), Skippable: s.Skippable(), CompletedAt: s.CompletedAt, SkippedAt: s.SkippedAt}
}

type colorValueDTO struct {
	ColorID   *uuid.UUID  `json:"color_id"`
	ColorCode *string     `json:"color_code"`
	ColorName *string     `json:"color_name"`
	Qty       money.Qty   `json:"qty"`
	Value     money.Money `json:"value"`
}

type fundValueDTO struct {
	ID      uuid.UUID   `json:"id"`
	Name    string      `json:"name"`
	Kind    string      `json:"kind"`
	Balance money.Money `json:"balance"`
}

type openingTotalsDTO struct {
	Cutoff           string          `json:"cutoff"`
	StockByColor     []colorValueDTO `json:"stock_by_color"`
	StockValue       money.Money     `json:"stock_value"`
	Receivables      money.Money     `json:"receivables"`
	Payables         money.Money     `json:"payables"`
	CustomerAdvances money.Money     `json:"customer_advances"`
	SupplierAdvances money.Money     `json:"supplier_advances"`
	Funds            []fundValueDTO  `json:"funds"`
}

func toTotalsDTO(t OpeningTotals) openingTotalsDTO {
	out := openingTotalsDTO{
		Cutoff: t.Cutoff.Format(time.DateOnly), StockByColor: make([]colorValueDTO, len(t.StockByColor)),
		StockValue: t.StockValue, Receivables: t.Receivables, Payables: t.Payables,
		CustomerAdvances: t.CustomerAdvances, SupplierAdvances: t.SupplierAdvances, Funds: make([]fundValueDTO, len(t.Funds)),
	}
	for i, c := range t.StockByColor {
		out.StockByColor[i] = colorValueDTO(c)
	}
	for i, f := range t.Funds {
		out.Funds[i] = fundValueDTO(f)
	}
	return out
}

type onboardingDTO struct {
	Steps         []stepDTO         `json:"steps"`
	Done          int               `json:"done"`
	Total         int               `json:"total"`
	Complete      bool              `json:"complete"`
	LockedThrough *string           `json:"locked_through"`
	Totals        *openingTotalsDTO `json:"totals"`
}

func toOnboardingDTO(o Onboarding) onboardingDTO {
	out := onboardingDTO{
		Steps: make([]stepDTO, len(o.Steps)), Done: o.Done, Total: o.Total, Complete: o.Complete,
		LockedThrough: dateString(o.LockedThrough),
	}
	for i, s := range o.Steps {
		out.Steps[i] = toStepDTO(s)
	}
	if o.Totals != nil {
		t := toTotalsDTO(*o.Totals)
		out.Totals = &t
	}
	return out
}
