package debt

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// CreditCheckInput is the order a credit check is made for.
type CreditCheckInput struct {
	PartyID uuid.UUID
	// OrderID is the invoice being confirmed: deposits earmarked to it lower the exposure.
	OrderID *uuid.UUID
	// Undelivered is the value of the party's confirmed credit orders not delivered yet,
	// including this order (computed by sales, which owns orders and deliveries).
	Undelivered money.Money
	// AsOf is the business date overdue documents are counted on.
	AsOf time.Time
}

// CreditCheck is the outcome of a credit check, with every component the owner sees.
type CreditCheck struct {
	// Limit is the customer's credit limit; nil means no limit.
	Limit            *money.Money
	OpenReceivable   money.Money
	Undelivered      money.Money
	EarmarkedAdvance money.Money
	// Exposure = OpenReceivable + Undelivered − EarmarkedAdvance.
	Exposure money.Money
	Exceeded bool
	// Overdue is a warning, not a block: the party has documents past due on AsOf.
	Overdue          bool
	OverdueAmount    money.Money
	OldestOverdueDue *time.Time
}

// Err is the credit_limit_exceeded exception when the limit is exceeded (nil otherwise): the
// owner confirms it (exception_confirmation_id) to confirm the order anyway.
func (c CreditCheck) Err() error {
	if !c.Exceeded || c.Limit == nil {
		return nil
	}
	return apperr.NewExceptionRequired(string(exception.CreditLimitExceeded), fmt.Sprintf(
		"Credit limit exceeded: open receivable %s + undelivered orders %s − deposits for this order %s = %s, limit %s",
		c.OpenReceivable, c.Undelivered, c.EarmarkedAdvance, c.Exposure, *c.Limit))
}

// CreditLimitPolicy checks a customer's exposure against its credit limit. Only deposits
// earmarked to the order count against it: a general advance is the customer's money until it is
// allocated, so it does not hide an exposure.
type CreditLimitPolicy struct{}

// CreditLimit returns the credit limit policy.
func (s *Service) CreditLimit() CreditLimitPolicy { return CreditLimitPolicy{} }

// Check reads the party's limit, open receivable, overdue documents and the earmarked deposits
// of the order inside the caller's transaction (the caller's posting, so the reads see its
// locks).
func (CreditLimitPolicy) Check(ctx context.Context, q db.Querier, in CreditCheckInput) (CreditCheck, error) {
	if in.PartyID == uuid.Nil {
		return CreditCheck{}, errInvalid(map[string]string{"party_id": "required"})
	}
	if in.AsOf.IsZero() {
		return CreditCheck{}, errInvalid(map[string]string{"as_of": "required"})
	}
	if in.Undelivered.IsNegative() {
		return CreditCheck{}, errInvalid(map[string]string{"undelivered": "must_not_be_negative"})
	}
	var row struct {
		Limit       *money.Money `db:"credit_limit"`
		Open        money.Money  `db:"open"`
		Overdue     money.Money  `db:"overdue"`
		OldestDue   *time.Time   `db:"oldest_due"`
		Earmarked   money.Money  `db:"earmarked"`
		PartyExists bool         `db:"party_exists"`
	}
	err := getInto(ctx, q, &row, `
		SELECT EXISTS (SELECT 1 FROM parties WHERE id = $1) AS party_exists,
		       (SELECT credit_limit FROM customer_profiles WHERE party_id = $1) AS credit_limit,
		       COALESCE((SELECT SUM(o.amount - o.settled_amount) FROM obligations o
		                 WHERE o.party_id = $1 AND o.direction = 'receivable' AND o.status = 'open'), 0) AS open,
		       COALESCE((SELECT SUM(`+overdueExpr("$2")+`) FROM obligations o
		                 WHERE o.party_id = $1 AND o.direction = 'receivable' AND o.status = 'open'), 0) AS overdue,
		       (SELECT MIN(`+nextDueExpr+`) FROM obligations o
		         WHERE o.party_id = $1 AND o.direction = 'receivable' AND o.status = 'open' AND `+overdueExpr("$2")+` > 0) AS oldest_due,
		       COALESCE((SELECT SUM(p.amount - p.allocated_amount) FROM payments p
		                 WHERE p.party_id = $1 AND p.direction = 'in' AND p.status = 'confirmed'
		                   AND p.earmarked_invoice_id = $3), 0) AS earmarked`,
		in.PartyID, in.AsOf, in.OrderID)
	if err != nil {
		return CreditCheck{}, fmt.Errorf("debt: read credit exposure: %w", err)
	}
	if !row.PartyExists {
		return CreditCheck{}, errField("party_not_found", "party_id", "not_found", "The party does not exist")
	}
	out := CreditCheck{
		Limit: row.Limit, OpenReceivable: row.Open, Undelivered: in.Undelivered, EarmarkedAdvance: row.Earmarked,
		OverdueAmount: row.Overdue, Overdue: row.Overdue.IsPositive(), OldestOverdueDue: row.OldestDue,
	}
	out.Exposure = out.OpenReceivable.Add(out.Undelivered).Sub(out.EarmarkedAdvance)
	out.Exceeded = out.Limit != nil && out.Exposure.Cmp(*out.Limit) > 0
	return out, nil
}
