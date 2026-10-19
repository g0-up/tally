package debt

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Method is a payment method as the debt domain sees it: which fund it pays into and whether a
// payment through it waits for verification.
type Method struct {
	ID                   uuid.UUID
	FundAccountID        uuid.UUID
	Code                 string
	RequiresVerification bool
	Active               bool
}

// CashEntry is one movement of money a confirmed payment puts into or takes out of a fund.
type CashEntry struct {
	FundAccountID uuid.UUID
	// Amount is signed: positive money in, negative money out.
	Amount       money.Money
	Direction    string
	Purpose      string
	SourceType   string
	PaymentID    uuid.UUID
	ShiftID      *uuid.UUID
	BusinessDate time.Time
	PostedAt     time.Time
	PostedBy     uuid.UUID
}

// CashBook is the cash domain seen from debt (implemented by an adapter in the composition
// root): it resolves payment methods and writes the fund entries of confirmed payments. Every
// call runs inside the caller's posting transaction, after the obligation and payment rows were
// locked, so the fund row it updates is locked last.
type CashBook interface {
	Method(ctx context.Context, q db.Querier, methodID uuid.UUID) (Method, error)
	Record(ctx context.Context, q db.Querier, tenantID uuid.UUID, e CashEntry) error
	ReversePayment(ctx context.Context, q db.Querier, tenantID, paymentID uuid.UUID, businessDate, at time.Time, by uuid.UUID) error
}

// Onboarding completes a step of the tenant's onboarding checklist.
type Onboarding interface {
	CompleteStep(ctx context.Context, q db.Querier, tenantID uuid.UUID, step string, by uuid.UUID, at time.Time) error
}

// DocIssuer issues document numbers inside the caller's transaction (docseq.Issuer). Payments
// recorded by another domain's posting, and refunds, take their PT/PC number from it.
type DocIssuer interface {
	Next(ctx context.Context, q db.Querier, tenantID uuid.UUID, prefix string) (string, error)
}

// ExceptionGuard spends an owner confirmation (exception.Guard): a write-off needs one.
type ExceptionGuard interface {
	Consume(ctx context.Context, q db.Querier, tenantID uuid.UUID, want exception.Expectation) error
}

// ConsentChecker reads the latest consent a party gave for a channel and purpose (the CRM consent
// log, where the latest row wins), in the caller's transaction.
type ConsentChecker interface {
	Granted(ctx context.Context, q db.Querier, partyID uuid.UUID, channel, purpose string) (bool, error)
}
