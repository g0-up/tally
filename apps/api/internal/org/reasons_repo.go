package org

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

const reasonColumns = `id, kind, name, name_key, is_system, active, created_at`

func listReasons(ctx context.Context, q db.Querier, f ReasonFilter) ([]Reason, error) {
	var out []Reason
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT `+reasonColumns+` FROM reasons
		WHERE ($1 = '' OR kind = $1) AND (NOT $2 OR active)
		ORDER BY kind, name_key, id`, f.Kind, f.ActiveOnly); err != nil {
		return nil, fmt.Errorf("org: list reasons: %w", err)
	}
	return out, nil
}

func readReason(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Reason, error) {
	query := `SELECT ` + reasonColumns + ` FROM reasons WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var r Reason
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&r); err != nil {
		if db.IsNoRows(err) {
			return Reason{}, errNotFound()
		}
		return Reason{}, fmt.Errorf("org: read reason: %w", err)
	}
	return r, nil
}

// insertReason adds r; skipExisting keeps an existing reason of the same kind and name (seeding).
func insertReason(ctx context.Context, q db.Querier, tenantID uuid.UUID, r Reason, skipExisting bool) error {
	query := `
		INSERT INTO reasons (id, tenant_id, kind, name, name_key, is_system, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	if skipExisting {
		query += ` ON CONFLICT ON CONSTRAINT ux_reasons__kind_name_key DO NOTHING`
	}
	if _, err := q.ExecContext(ctx, query, r.ID, tenantID, r.Kind, r.Name, r.NameKey, r.IsSystem, r.Active, r.CreatedAt); err != nil {
		if db.IsUniqueViolation(err, "ux_reasons__kind_name_key") {
			return errReasonNameTaken()
		}
		return fmt.Errorf("org: insert reason: %w", err)
	}
	return nil
}

func updateReason(ctx context.Context, q db.Querier, r Reason) error {
	if _, err := q.ExecContext(ctx, `UPDATE reasons SET name = $2, name_key = $3, active = $4 WHERE id = $1`,
		r.ID, r.Name, r.NameKey, r.Active); err != nil {
		if db.IsUniqueViolation(err, "ux_reasons__kind_name_key") {
			return errReasonNameTaken()
		}
		return fmt.Errorf("org: update reason: %w", err)
	}
	return nil
}

func deleteReason(ctx context.Context, q db.Querier, id uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM reasons WHERE id = $1`, id); err != nil {
		if db.IsForeignKeyViolation(err, "") {
			return errReasonInUse()
		}
		return fmt.Errorf("org: delete reason: %w", err)
	}
	return nil
}

// reasonInUse reports whether a document of any module points at the reason. The foreign keys
// are the backstop; this check gives the friendly conflict before the delete is tried.
func reasonInUse(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error) {
	var used bool
	if err := q.QueryRowxContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM stock_count_lines WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM stock_adjustments WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM purchase_orders WHERE close_reason_id = $1)
		    OR EXISTS (SELECT 1 FROM purchase_returns WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM purchase_price_adjustments WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM sales_returns WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM disassemblies WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM credit_notes WHERE reason_id = $1)
		    OR EXISTS (SELECT 1 FROM debt_adjustments WHERE reason_id = $1)`, id).Scan(&used); err != nil {
		return false, fmt.Errorf("org: reason usage: %w", err)
	}
	return used, nil
}
