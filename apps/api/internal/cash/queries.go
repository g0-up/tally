package cash

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// FundFlow is the movement of one fund over a date range: the balance before the range, the money
// in and out within it, the net per category, and the balance after.
type FundFlow struct {
	Fund       FundAccount
	Opening    money.Money
	Inflow     money.Money
	Outflow    money.Money
	Closing    money.Money
	Categories []CategoryFlow
}

// CategoryFlow is the net movement of one category (a reversal nets against its original).
type CategoryFlow struct {
	Category string      `db:"category"`
	Net      money.Money `db:"net"`
}

// maxFlowDays bounds a cash-flow range (a little over five years).
const maxFlowDays = 1900

// CashFlow reports every fund between from and to inclusive (business dates). The opening
// balance of a fund counts from the start; the dashboard and the cash-flow report build on it.
func (s *Service) CashFlow(ctx context.Context, scope Scope, from, to time.Time) ([]FundFlow, error) {
	if to.Before(from) {
		return nil, errInvalid(map[string]string{"to": "before_from"})
	}
	if to.Sub(from) > maxFlowDays*24*time.Hour {
		return nil, apperr.NewValidation("range_too_long", "The date range is too long", map[string]string{"to": "too_far"})
	}
	var out []FundFlow
	err := s.read(ctx, scope, func(q db.Querier) error {
		funds, err := listFunds(ctx, q)
		if err != nil {
			return err
		}
		for _, f := range funds {
			flow, err := fundFlow(ctx, q, f, from, to)
			if err != nil {
				return err
			}
			out = append(out, flow)
		}
		return nil
	})
	return out, err
}

func fundFlow(ctx context.Context, q db.Querier, f FundAccount, from, to time.Time) (FundFlow, error) {
	flow := FundFlow{Fund: f}
	var sums struct {
		Before  money.Money `db:"before"`
		Inflow  money.Money `db:"inflow"`
		Outflow money.Money `db:"outflow"`
	}
	if err := q.QueryRowxContext(ctx, `
		SELECT COALESCE(SUM(amount) FILTER (WHERE business_date < $2), 0) AS before,
		       COALESCE(SUM(amount) FILTER (WHERE business_date BETWEEN $2 AND $3 AND amount > 0), 0) AS inflow,
		       COALESCE(-SUM(amount) FILTER (WHERE business_date BETWEEN $2 AND $3 AND amount < 0), 0) AS outflow
		FROM cash_entries WHERE fund_account_id = $1`, f.ID, from, to).StructScan(&sums); err != nil {
		return FundFlow{}, fmt.Errorf("cash: cash flow: %w", err)
	}
	if err := sqlxSelect(ctx, q, &flow.Categories, `
		SELECT category, SUM(amount) AS net FROM cash_entries
		WHERE fund_account_id = $1 AND business_date BETWEEN $2 AND $3
		GROUP BY category ORDER BY category`, f.ID, from, to); err != nil {
		return FundFlow{}, fmt.Errorf("cash: cash flow by category: %w", err)
	}
	flow.Opening = f.OpeningBalance.Add(sums.Before)
	flow.Inflow = sums.Inflow
	flow.Outflow = sums.Outflow
	flow.Closing = flow.Opening.Add(sums.Inflow).Sub(sums.Outflow)
	return flow, nil
}

// MethodFund resolves a payment method to the fund it pays into; the debt domain reads it inside
// its own transaction when it records a payment.
func MethodFund(ctx context.Context, q db.Querier, methodID uuid.UUID) (PaymentMethod, error) {
	return getMethod(ctx, q, methodID)
}

// PaymentMethods lists the payment methods in display order inside the caller's transaction; the
// POS reads them inside its own.
func PaymentMethods(ctx context.Context, q db.Querier) ([]PaymentMethod, error) {
	return listMethods(ctx, q)
}
