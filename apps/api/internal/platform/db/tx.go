package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// WithTenantTx runs fn in a transaction whose first statement sets app.tenant_id
// (transaction-local), so every RLS policy sees only that tenant. fn's error rolls back; a panic
// rolls back and is re-raised.
func (d *DB) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q Querier) error) error {
	if tenantID == uuid.Nil {
		return errors.New("tenant transaction: tenant id is empty")
	}
	return d.run(ctx, nil, fn, setTenant(tenantID))
}

// WithPlatformTx runs fn in a transaction without tenant context, for platform tables (tenants,
// users, sessions …). Queries must filter tenant_id explicitly; RLS tables return no rows here.
func (d *DB) WithPlatformTx(ctx context.Context, fn func(q Querier) error) error {
	return d.run(ctx, nil, fn)
}

// WithReadOnlyTenantTx is WithTenantTx for consented support sessions: the transaction is
// READ ONLY and switches to role tally_readonly, so even a buggy handler cannot write. The
// tenant's time zone is copied into app.tenant_timezone before the switch, since the role cannot
// read the tenants table (ReadOnlyTimezone).
func (d *DB) WithReadOnlyTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q Querier) error) error {
	if tenantID == uuid.Nil {
		return errors.New("read-only tenant transaction: tenant id is empty")
	}
	return d.run(ctx, &sql.TxOptions{ReadOnly: true}, fn, setTenant(tenantID), setTenantTimezone(tenantID), setLocalReadonlyRole)
}

// ReadOnlyTimezone is the tenant time zone a read-only tenant transaction carries, or fallback
// when q carries none.
func ReadOnlyTimezone(ctx context.Context, q Querier, fallback string) (string, error) {
	var name sql.NullString
	if err := q.QueryRowxContext(ctx, `SELECT NULLIF(current_setting('app.tenant_timezone', true), '')`).Scan(&name); err != nil {
		return "", fmt.Errorf("read tenant time zone setting: %w", err)
	}
	if !name.Valid {
		return fallback, nil
	}
	return name.String, nil
}

// WithSnapshotTenantTx is WithTenantTx for long reads that must see one state of the ledgers,
// such as a report export: the transaction is REPEATABLE READ READ ONLY, so every statement of fn
// reads the same snapshot. A positive statementTimeout replaces the pool's statement timeout for
// this transaction only.
func (d *DB) WithSnapshotTenantTx(ctx context.Context, tenantID uuid.UUID, statementTimeout time.Duration, fn func(q Querier) error) error {
	if tenantID == uuid.Nil {
		return errors.New("snapshot tenant transaction: tenant id is empty")
	}
	setup := []setupFunc{setTenant(tenantID)}
	if statementTimeout > 0 {
		setup = append(setup, setLocalStatementTimeout(statementTimeout))
	}
	return d.run(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, fn, setup...)
}

type setupFunc func(ctx context.Context, tx *sqlx.Tx) error

func setTenant(tenantID uuid.UUID) setupFunc {
	return func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}
		return nil
	}
}

func setTenantTimezone(tenantID uuid.UUID) setupFunc {
	return func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`SELECT set_config('app.tenant_timezone', timezone, true) FROM tenants WHERE id = $1`, tenantID); err != nil {
			return fmt.Errorf("set tenant time zone: %w", err)
		}
		return nil
	}
}

func setLocalStatementTimeout(timeout time.Duration) setupFunc {
	return func(ctx context.Context, tx *sqlx.Tx) error {
		ms := strconv.FormatInt(timeout.Milliseconds(), 10)
		if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, ms); err != nil {
			return fmt.Errorf("set statement timeout: %w", err)
		}
		return nil
	}
}

func setLocalReadonlyRole(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE tally_readonly`); err != nil {
		return fmt.Errorf("switch to read-only role: %w", err)
	}
	return nil
}

func (d *DB) run(ctx context.Context, opts *sql.TxOptions, fn func(q Querier) error, setup ...setupFunc) (err error) {
	tx, err := d.x.BeginTxx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
	}()

	for _, s := range setup {
		if err := s(ctx, tx); err != nil {
			return err
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}
