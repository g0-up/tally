package cash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

const (
	fundColumns = `f.id, f.tenant_id, f.kind, f.name, f.bank_name, f.account_no, f.opening_balance, f.opening_date,
		f.balance, f.active, f.created_at, f.updated_at, f.version`
	methodColumns = `m.id, m.tenant_id, m.code, m.name, m.fund_account_id, f.kind AS fund_kind, m.requires_verification,
		m.active, m.created_at`
	entryColumns = `e.id, e.tenant_id, e.fund_account_id, e.amount, e.category, e.payment_id, e.shift_id, e.business_date,
		e.posted_at, e.posted_by, e.reversal_of, e.created_at`
)

func sqlxSelect(ctx context.Context, q db.Querier, dst any, query string, args ...any) error {
	return sqlx.SelectContext(ctx, q, dst, query, args...)
}

func listFunds(ctx context.Context, q db.Querier) ([]FundAccount, error) {
	var out []FundAccount
	if err := sqlxSelect(ctx, q, &out, `SELECT `+fundColumns+` FROM fund_accounts f ORDER BY f.kind = 'bank', f.created_at, f.id`); err != nil {
		return nil, fmt.Errorf("cash: list funds: %w", err)
	}
	return out, nil
}

func getFund(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (FundAccount, error) {
	query := `SELECT ` + fundColumns + ` FROM fund_accounts f WHERE f.id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var f FundAccount
	err := sqlx.GetContext(ctx, q, &f, query, id)
	if db.IsNoRows(err) {
		return FundAccount{}, errNotFound()
	}
	if err != nil {
		return FundAccount{}, fmt.Errorf("cash: read fund: %w", err)
	}
	return f, nil
}

func listMethods(ctx context.Context, q db.Querier) ([]PaymentMethod, error) {
	var out []PaymentMethod
	if err := sqlxSelect(ctx, q, &out, `
		SELECT `+methodColumns+` FROM payment_methods m JOIN fund_accounts f ON f.id = m.fund_account_id
		ORDER BY m.code = 'bank_transfer', m.created_at, m.id`); err != nil {
		return nil, fmt.Errorf("cash: list payment methods: %w", err)
	}
	return out, nil
}

func getMethod(ctx context.Context, q db.Querier, id uuid.UUID) (PaymentMethod, error) {
	var m PaymentMethod
	err := sqlx.GetContext(ctx, q, &m, `
		SELECT `+methodColumns+` FROM payment_methods m JOIN fund_accounts f ON f.id = m.fund_account_id WHERE m.id = $1`, id)
	if db.IsNoRows(err) {
		return PaymentMethod{}, errNotFound()
	}
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("cash: read payment method: %w", err)
	}
	return m, nil
}

func fundHasEntries(ctx context.Context, q db.Querier, fundID uuid.UUID) (bool, error) {
	var has bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM cash_entries WHERE fund_account_id = $1)`, fundID).Scan(&has); err != nil {
		return false, fmt.Errorf("cash: check fund entries: %w", err)
	}
	return has, nil
}
