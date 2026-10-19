package org

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// replacedSlugPrefix starts the slug a replaced sandbox keeps. With the 32 hex digits of its id the
// slug is 38 characters long: longer than any sign-up slug (at most 30), so it can never collide
// with a store address, and unique without consulting retired_tenant_slugs.
const replacedSlugPrefix = "reset-"

// SandboxResetInterval is how long a sandbox must exist before it can be reset. Every reset keeps
// a whole tenant forever, so the interval bounds how fast one account can add tenants; it also
// turns a retried reset (the session already belongs to the new sandbox) into a 429 instead of a
// second reset.
const SandboxResetInterval = 10 * time.Minute

// replacedSlug is the slug a sandbox gives up its address for.
func replacedSlug(id uuid.UUID) string {
	return replacedSlugPrefix + fmt.Sprintf("%x", id[:])
}

// ResetSandbox replaces a sandbox tenant with a freshly provisioned one and returns the new tenant.
//
// Nothing is deleted: ledgers, audit events, period locks and document counters are append-only
// for every tenant, a sandbox included. The old tenant is marked replaced and renamed, so it keeps
// all of its rows but answers to no host and no session; the new tenant takes over its address
// (`<parent>-demo`, or `demo` for the standalone sandbox) with the default and sample data. The
// owner account moves to the new tenant with every session and token, so the caller stays signed
// in on the same address. The address is reused, not retired: retired_tenant_slugs holds the
// addresses of purged stores only, and a reset purges nothing, so any number of resets succeed.
//
// It runs as one transaction that starts in the old tenant and switches app.tenant_id to the new
// one once the old tenant has given up its address: each audit event is written while its own
// tenant is the current one, which row-level security requires.
//
// The outbox events the old tenant has not delivered yet are closed in the same transaction: a
// replaced tenant has no reader left for their side effects, and the dispatcher only visits active
// tenants. An event a request still in flight writes after the reset commits stays pending and is
// never handled.
//
// Identity opens its account changes through one helper that locks the tenant row before any
// account row, as the reset does; the few it exempts never wait for the tenant row while they hold
// an account row. Neither can close a lock cycle with the reset. A writer elsewhere that locks in
// the other order can still make the reset the deadlock victim; the reset then runs once more after
// a short random pause and answers sandbox_busy (409) if that fails the same way.
func (s *Service) ResetSandbox(ctx context.Context, actor audit.Actor) (Tenant, error) {
	out, address, err := s.resetSandbox(ctx, actor)
	if err != nil && db.IsRetryable(err) {
		select {
		case <-ctx.Done():
		case <-time.After(10*time.Millisecond + rand.N(40*time.Millisecond)): //nolint:gosec // jitter, not security
			out, address, err = s.resetSandbox(ctx, actor)
		}
	}
	if err != nil && db.IsRetryable(err) {
		return Tenant{}, apperr.NewConflict("sandbox_busy", "The demo store is busy; try the reset again")
	}
	if err != nil {
		return Tenant{}, err
	}
	if s.addressMoved != nil {
		s.addressMoved(address)
	}
	return out, nil
}

// resetSandbox runs the reset transaction and returns the new tenant and the address it took over.
func (s *Service) resetSandbox(ctx context.Context, actor audit.Actor) (Tenant, string, error) {
	var out Tenant
	var address string
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		old, err := readTenant(ctx, q, actor.TenantID, true)
		if err != nil {
			return err
		}
		if !old.IsSandbox {
			return errNotSandbox()
		}
		if old.Status == TenantReplaced {
			// A concurrent reset replaced it while this one waited for the row lock.
			return apperr.NewConflict("sandbox_not_active", "The demo store was already reset")
		}
		if old.Status != TenantActive {
			return apperr.NewConflict("sandbox_not_active", "The demo store is not active")
		}
		now := s.clock.Now()
		if wait := old.CreatedAt.Add(SandboxResetInterval).Sub(now); wait > 0 {
			return newResetTooSoon(wait)
		}
		address = old.Slug
		spec := TenantSpec{
			ID: ids.New(), Slug: old.Slug, DisplayName: old.DisplayName, LegalName: old.LegalName,
			IsSandbox: true, SandboxOf: old.SandboxOf,
		}
		if old.SandboxOf != nil {
			if err := q.QueryRowxContext(ctx, `SELECT slug FROM tenants WHERE id = $1`, *old.SandboxOf).Scan(&spec.ParentSlug); err != nil {
				return fmt.Errorf("org: read sandbox parent: %w", err)
			}
		}

		retiredAs := replacedSlug(old.ID)
		if _, err := q.ExecContext(ctx, `
			UPDATE tenants SET slug = $2, status = $3, updated_at = $4, version = version + 1 WHERE id = $1`,
			old.ID, retiredAs, TenantReplaced, now); err != nil {
			return fmt.Errorf("org: replace sandbox: %w", err)
		}
		closed, err := closePendingEvents(ctx, q, old.ID, now)
		if err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{
			Action: "tenant.replace", EntityType: "tenant", EntityID: old.ID,
			Before: map[string]any{"slug": old.Slug, "status": old.Status},
			After:  map[string]any{"slug": retiredAs, "status": TenantReplaced, "replaced_by": spec.ID, "closed_events": closed},
		}); err != nil {
			return err
		}

		if _, err := q.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, spec.ID.String()); err != nil {
			return fmt.Errorf("org: switch to the new sandbox: %w", err)
		}
		next := actor
		next.TenantID = spec.ID
		provisioned, err := s.provisioner.Provision(ctx, q, spec, next)
		if err != nil {
			return err
		}
		sessions, err := moveAccount(ctx, q, old.ID, spec.ID, now)
		if err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, next, audit.Event{
			Action: "tenant.sandbox_reset", EntityType: "tenant", EntityID: spec.ID,
			After: map[string]any{"slug": spec.Slug, "replaces": old.ID, "moved_sessions": sessions},
		}); err != nil {
			return err
		}
		out = provisioned.Tenant
		return nil
	})
	if err != nil {
		return Tenant{}, "", err
	}
	return out, address, nil
}

// closePendingEvents marks every undelivered outbox event of tenant as processed without running
// its handlers and returns how many it closed; last_error records why for operators.
func closePendingEvents(ctx context.Context, q db.Querier, tenant uuid.UUID, now time.Time) (int64, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE outbox_events SET processed_at = $2, last_error = 'tenant replaced by a sandbox reset'
		WHERE tenant_id = $1 AND processed_at IS NULL`, tenant, now)
	if err != nil {
		return 0, fmt.Errorf("org: close pending events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("org: close pending events: %w", err)
	}
	return n, nil
}

// moveAccount moves the owner account of tenant from to tenant to, with every session and
// single-use token, and returns how many sessions moved. Sessions and tokens reference the account
// by (tenant_id, user_id), so all three move in one statement: the foreign keys are checked when
// the statement ends, once every row carries the new tenant (PostgreSQL runs every data-modifying
// WITH clause to completion whether or not the outer query reads it). The new tenant has no
// account yet, so its single active owner is the one that moves.
func moveAccount(ctx context.Context, q db.Querier, from, to uuid.UUID, now time.Time) (int, error) {
	var sessions int
	err := q.QueryRowxContext(ctx, `
		WITH moved_users AS (
		  UPDATE users SET tenant_id = $2, updated_at = $3, version = version + 1 WHERE tenant_id = $1
		), moved_sessions AS (
		  UPDATE sessions SET tenant_id = $2 WHERE tenant_id = $1 RETURNING id
		), moved_tokens AS (
		  UPDATE auth_tokens SET tenant_id = $2 WHERE tenant_id = $1
		)
		SELECT count(*) FROM moved_sessions`,
		from, to, now).Scan(&sessions)
	if err != nil {
		return 0, fmt.Errorf("org: move sandbox account: %w", err)
	}
	return sessions, nil
}
