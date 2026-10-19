package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// TenantIDs lists every tenant id (platform query, no RLS) in a stable order. Background jobs
// use it to visit tenant tables one WithTenantTx at a time.
func (d *DB) TenantIDs(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := d.WithPlatformTx(ctx, func(q Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT id FROM tenants ORDER BY id`)
	})
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	return out, nil
}

// ActiveTenantIDs lists the tenants background jobs may write to, in a stable order: every
// tenant except a sandbox superseded by a reset and one an operator holds in maintenance (a
// restore copies its rows). Background jobs (outbox dispatch, expiry rolls, signals, reminders,
// purges, ...) use it, so no job writes to or sends for a replaced sandbox, the reset closes its
// pending outbox events, and a held tenant's jobs run on the first tick after its release.
// Checks that must cover every row ever written (ledger verification) use TenantIDs.
func (d *DB) ActiveTenantIDs(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := d.WithPlatformTx(ctx, func(q Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT id FROM tenants WHERE status <> 'replaced' AND NOT maintenance ORDER BY id`)
	})
	if err != nil {
		return nil, fmt.Errorf("list active tenants: %w", err)
	}
	return out, nil
}
