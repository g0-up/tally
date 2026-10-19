package saas

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// StartTrial puts a new store on the trial plan from its first business day and records the
// partner referral of its sign-up code; an unknown or inactive code is ignored. q is the
// provisioning transaction, in which the tenant row already exists.
func (s *Service) StartTrial(ctx context.Context, q db.Querier, tenantID uuid.UUID, referralCode string, now time.Time) error {
	t, err := readTenantState(ctx, q, tenantID, false)
	if err != nil {
		return err
	}
	today, err := t.today(now)
	if err != nil {
		return err
	}
	plan, err := readPlanByCode(ctx, q, TrialPlanCode)
	if err != nil {
		return err
	}
	end := plan.PeriodEnd(today)
	sub := Subscription{
		ID: ids.New(), TenantID: tenantID, PlanID: plan.ID, Status: StatusTrialing, CurrentPeriodStart: today,
		CurrentPeriodEnd: end, TrialEndsOn: &end, CreatedAt: now,
	}
	if err := insertSubscription(ctx, q, sub); err != nil {
		return err
	}
	if err := syncPlanEntitlements(ctx, q, sub, plan, today, now); err != nil {
		return err
	}
	code := strings.ToUpper(strings.TrimSpace(referralCode))
	if code == "" {
		return nil
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO tenant_referrals (tenant_id, partner_id, referred_at)
		SELECT $1, id, $3 FROM partners WHERE upper(referral_code) = $2 AND active
		ON CONFLICT ON CONSTRAINT pk_tenant_referrals DO NOTHING`, tenantID, code, now); err != nil {
		return fmt.Errorf("saas: record referral: %w", err)
	}
	return nil
}

// AdminChangePlan switches a tenant to a plan at once, outside billing (a goodwill change, a
// migration); a tenant without a live subscription gets an active one for the plan's period.
func (s *Service) AdminChangePlan(ctx context.Context, admin audit.Actor, tenantID, planID uuid.UUID) (Subscription, error) {
	return s.adminSubscription(ctx, admin, tenantID, "subscription.plan_changed",
		func(q db.Querier, sub *Subscription, live bool, today time.Time) error {
			plan, err := readPlan(ctx, q, planID)
			if err != nil {
				return errInvalid(map[string]string{"plan_id": "invalid"})
			}
			if plan.Kind == KindAddon {
				return errInvalid(map[string]string{"plan_id": "addon"})
			}
			if closed, err := closedOnRequest(ctx, q, tenantID); err != nil || closed {
				if err == nil {
					err = errTenantClosed()
				}
				return err
			}
			if !live {
				end := plan.PeriodEnd(today)
				*sub = Subscription{ID: ids.New(), TenantID: tenantID, Status: StatusActive, CurrentPeriodStart: today,
					CurrentPeriodEnd: end, CreatedAt: s.clock.Now()}
				if plan.Kind == KindTrial {
					sub.Status, sub.TrialEndsOn = StatusTrialing, &end
				}
			}
			sub.PlanID = plan.ID
			if !live {
				if err := insertSubscription(ctx, q, *sub); err != nil {
					return err
				}
			}
			return syncPlanEntitlements(ctx, q, *sub, plan, today, s.clock.Now())
		})
}

// Extend adds days to the tenant's current period (or trial); a lapsed subscription becomes
// active again from today.
func (s *Service) Extend(ctx context.Context, admin audit.Actor, tenantID uuid.UUID, days int) (Subscription, error) {
	if days < 1 || days > 366 {
		return Subscription{}, errInvalid(map[string]string{"days": "range"})
	}
	return s.adminSubscription(ctx, admin, tenantID, "subscription.extended",
		func(_ db.Querier, sub *Subscription, live bool, today time.Time) error {
			if !live {
				return errSubscriptionState(StatusClosed)
			}
			if sub.Status == StatusTrialing {
				end := laterDate(sub.CurrentPeriodEnd, today.AddDate(0, 0, -1)).AddDate(0, 0, days)
				sub.CurrentPeriodEnd, sub.TrialEndsOn = end, &end
				return nil
			}
			sub.CurrentPeriodEnd = laterDate(sub.CurrentPeriodEnd, today.AddDate(0, 0, -1)).AddDate(0, 0, days)
			sub.Status, sub.GraceEndsOn, sub.ReadOnlyEndsOn = StatusActive, nil, nil
			return nil
		})
}

// SetStatus moves the tenant's subscription to a status by hand (grace and read-only start
// today with the plan's durations) and sets or lifts the legal hold.
func (s *Service) SetStatus(ctx context.Context, admin audit.Actor, tenantID uuid.UUID, status string, legalHold *bool) (Subscription, error) {
	switch status {
	case "", StatusActive, StatusGrace, StatusReadOnly, StatusClosed:
	default:
		return Subscription{}, errInvalid(map[string]string{"status": "oneof"})
	}
	if status == "" && legalHold == nil {
		return Subscription{}, errInvalid(map[string]string{"status": "required"})
	}
	return s.adminSubscription(ctx, admin, tenantID, "subscription.status_set",
		func(q db.Querier, sub *Subscription, live bool, today time.Time) error {
			if legalHold != nil {
				if err := setLegalHold(ctx, q, tenantID, *legalHold, s.clock.Now()); err != nil {
					return err
				}
				a := admin
				a.TenantID = tenantID
				if err := s.audit.Record(ctx, q, a, audit.Event{
					Action: "tenant.legal_hold_set", EntityType: "tenant", EntityID: tenantID,
					After: map[string]any{"legal_hold": *legalHold},
				}); err != nil {
					return err
				}
			}
			if status != "" && !live {
				return errSubscriptionState(StatusClosed)
			}
			if status == "" || status == sub.Status {
				return nil
			}
			plan, err := readPlan(ctx, q, sub.PlanID)
			if err != nil {
				return err
			}
			switch status {
			case StatusActive:
				if sub.CurrentPeriodEnd.Before(today) {
					return errSubscriptionState("past its period; extend it instead")
				}
				sub.GraceEndsOn, sub.ReadOnlyEndsOn = nil, nil
			case StatusGrace:
				sub.GraceEndsOn, sub.ReadOnlyEndsOn = addDays(today, plan.Term(TermGraceDays)), nil
			case StatusReadOnly:
				sub.ReadOnlyEndsOn = addDays(today, plan.Term(TermReadOnlyDays))
			}
			sub.Status = status
			return nil
		})
}

// setLegalHold sets the tenant's legal hold and mirrors it on its open closure requests.
func setLegalHold(ctx context.Context, q db.Querier, tenantID uuid.UUID, hold bool, now time.Time) error {
	if _, err := q.ExecContext(ctx, `UPDATE tenants SET legal_hold = $2, updated_at = $3, version = version + 1
		WHERE id = $1 AND legal_hold <> $2`, tenantID, hold, now); err != nil {
		return fmt.Errorf("saas: set legal hold: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE account_closure_requests SET legal_hold = $2, updated_at = $3
		WHERE tenant_id = $1 AND status IN ('requested', 'verified', 'waiting', 'exporting', 'export_failed')`,
		tenantID, hold, now); err != nil {
		return fmt.Errorf("saas: set closure legal hold: %w", err)
	}
	return nil
}

// adminSubscription runs an admin change of the tenant's subscription: the subscription is
// locked and advanced to today, fn changes it, and the result is written, mirrored on the tenant
// status and audited in the tenant's log.
func (s *Service) adminSubscription(ctx context.Context, admin audit.Actor, tenantID uuid.UUID, action string,
	fn func(q db.Querier, sub *Subscription, live bool, today time.Time) error) (Subscription, error) {
	if err := s.adminTenant(ctx, tenantID); err != nil {
		return Subscription{}, err
	}
	admin.TenantID = tenantID
	var out Subscription
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if err := lockBilling(ctx, q, tenantID); err != nil {
			return err
		}
		t, err := readTenantState(ctx, q, tenantID, false)
		if err != nil {
			return err
		}
		if t.IsSandbox {
			return errSandbox()
		}
		now := s.clock.Now()
		today, err := t.today(now)
		if err != nil {
			return err
		}
		sub, found, err := readSubscription(ctx, q, tenantID, true)
		if err != nil {
			return err
		}
		if found {
			plan, err := readPlan(ctx, q, sub.PlanID)
			if err != nil {
				return err
			}
			sub, _ = s.lifecycle.Advance(sub, plan, today)
		}
		live := found && sub.Status != StatusClosed
		before := sub
		if err := fn(q, &sub, live, today); err != nil {
			return err
		}
		if sub.ID == uuid.Nil {
			if action != "subscription.status_set" {
				return errNoSubscription()
			}
			return nil
		}
		if live {
			if err := updateSubscription(ctx, q, &sub, now); err != nil {
				return err
			}
		}
		if err := syncTenantStatus(ctx, q, tenantID, sub.Status, now); err != nil {
			return err
		}
		out = sub
		return s.audit.Record(ctx, q, admin, audit.Event{
			Action: action, EntityType: "subscription", EntityID: sub.ID,
			Before: subscriptionSnapshot(before), After: subscriptionSnapshot(sub),
		})
	})
	return out, err
}

func subscriptionSnapshot(s Subscription) map[string]any {
	if s.ID == uuid.Nil {
		return nil
	}
	return map[string]any{
		"plan_id": s.PlanID, "status": s.Status, "current_period_start": s.CurrentPeriodStart.Format(time.DateOnly),
		"current_period_end": s.CurrentPeriodEnd.Format(time.DateOnly),
	}
}

// RunLifecycle records the subscription transitions due today for every live store (in each
// store's own timezone) and mirrors them on the tenant status. The gates already evaluate the
// lifecycle at read time; the job makes the stored state, the tenant status and the audit trail
// follow. A store held in maintenance waits for its release. It returns the number of
// subscriptions that moved.
func (s *Service) RunLifecycle(ctx context.Context) (int, error) {
	var tenants []uuid.UUID
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &tenants, `
			SELECT DISTINCT s.tenant_id FROM subscriptions s JOIN tenants t ON t.id = s.tenant_id
			WHERE s.status <> 'closed' AND t.status <> 'replaced' AND NOT t.maintenance ORDER BY s.tenant_id`)
	}); err != nil {
		return 0, fmt.Errorf("saas: list live subscriptions: %w", err)
	}
	moved := 0
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			return moved, err
		}
		changed, err := s.advanceTenant(ctx, tenantID)
		if err != nil {
			s.logger.ErrorContext(ctx, "subscription lifecycle failed", "tenant_id", tenantID, "error", err)
			continue
		}
		if changed {
			moved++
		}
	}
	return moved, nil
}

func (s *Service) advanceTenant(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var changed bool
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if err := lockBilling(ctx, q, tenantID); err != nil {
			return err
		}
		t, err := readTenantState(ctx, q, tenantID, false)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		today, err := t.today(now)
		if err != nil {
			return err
		}
		sub, found, err := readSubscription(ctx, q, tenantID, true)
		if err != nil || !found || sub.Status == StatusClosed {
			return err
		}
		plan, err := readPlan(ctx, q, sub.PlanID)
		if err != nil {
			return err
		}
		next, transitions := s.lifecycle.Advance(sub, plan, today)
		if len(transitions) == 0 {
			return syncTenantStatus(ctx, q, tenantID, sub.Status, now)
		}
		if err := updateSubscription(ctx, q, &next, now); err != nil {
			return err
		}
		if err := syncTenantStatus(ctx, q, tenantID, next.Status, now); err != nil {
			return err
		}
		changed = true
		actor := systemActor(tenantID, audit.SourceWorker)
		for _, tr := range transitions {
			if err := s.audit.Record(ctx, q, actor, audit.Event{
				Action: "subscription.transition", EntityType: "subscription", EntityID: sub.ID,
				Before: map[string]any{"status": tr.From},
				After:  map[string]any{"status": tr.To, "on": tr.On.Format(time.DateOnly)},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, err
}
