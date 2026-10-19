package cash

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// EntryInput is one fund movement to append. Amount is signed: positive money in, negative money
// out.
type EntryInput struct {
	FundAccountID uuid.UUID
	Amount        money.Money
	Category      string
	PaymentID     *uuid.UUID
	// ShiftID is the POS shift the movement belongs to, when the sale opened one.
	ShiftID      *uuid.UUID
	BusinessDate time.Time
	PostedAt     time.Time
	PostedBy     uuid.UUID
	ReversalOf   *uuid.UUID
}

// Writer appends cash entries and keeps the fund balance projection in the same transaction.
// It is called by posting commands only, after the fund rows were locked in the global lock
// order (fund_accounts last); the balance update itself would take the row lock otherwise.
type Writer struct{}

// NewWriter returns the cash entry writer.
func NewWriter() Writer { return Writer{} }

// Write appends one entry and moves the fund balance by its amount.
func (Writer) Write(ctx context.Context, q db.Querier, tenantID uuid.UUID, in EntryInput) (Entry, error) {
	if in.Amount.IsZero() {
		return Entry{}, fmt.Errorf("cash: entry amount is zero")
	}
	if !validCategory(in.Category) {
		return Entry{}, fmt.Errorf("cash: unknown entry category %q", in.Category)
	}
	e := Entry{
		ID: ids.New(), TenantID: tenantID, FundAccountID: in.FundAccountID, Amount: in.Amount, Category: in.Category,
		PaymentID: in.PaymentID, ShiftID: in.ShiftID, BusinessDate: in.BusinessDate, PostedAt: in.PostedAt,
		PostedBy: in.PostedBy, ReversalOf: in.ReversalOf, CreatedAt: in.PostedAt,
	}
	res, err := q.ExecContext(ctx, `
		UPDATE fund_accounts SET balance = balance + $2 WHERE id = $1`, e.FundAccountID, e.Amount)
	if err != nil {
		return Entry{}, fmt.Errorf("cash: move fund balance: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return Entry{}, fmt.Errorf("cash: fund %s not found", e.FundAccountID)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO cash_entries (id, tenant_id, fund_account_id, amount, category, payment_id, shift_id, business_date,
		                          posted_at, posted_by, reversal_of, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		e.ID, e.TenantID, e.FundAccountID, e.Amount, e.Category, e.PaymentID, e.ShiftID, e.BusinessDate,
		e.PostedAt, e.PostedBy, e.ReversalOf, e.CreatedAt); err != nil {
		return Entry{}, fmt.Errorf("cash: insert entry: %w", err)
	}
	return e, nil
}

// ReversePaymentEntries appends a mirror entry for every not yet reversed entry of a payment and
// moves the fund balances back. The mirrors carry no shift: the shift of the original sale may be
// closed by now.
func (w Writer) ReversePaymentEntries(ctx context.Context, q db.Querier, tenantID, paymentID uuid.UUID,
	businessDate, at time.Time, by uuid.UUID,
) ([]Entry, error) {
	var open []Entry
	if err := sqlxSelect(ctx, q, &open, `
		SELECT `+entryColumns+` FROM cash_entries e
		WHERE e.payment_id = $1 AND e.reversal_of IS NULL
		  AND NOT EXISTS (SELECT 1 FROM cash_entries r WHERE r.reversal_of = e.id)
		ORDER BY e.created_at, e.id`, paymentID); err != nil {
		return nil, fmt.Errorf("cash: read payment entries: %w", err)
	}
	out := make([]Entry, 0, len(open))
	for _, e := range open {
		id := e.ID
		m, err := w.Write(ctx, q, tenantID, EntryInput{
			FundAccountID: e.FundAccountID, Amount: e.Amount.Neg(), Category: e.Category, PaymentID: e.PaymentID,
			BusinessDate: businessDate, PostedAt: at, PostedBy: by, ReversalOf: &id,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
