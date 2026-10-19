// Package cash keeps the store's two funds (cash drawer and bank account), the payment methods
// that pay into them and the append-only cash entries every confirmed payment writes. A fund's
// balance is a projection: opening balance + Σ cash entries, maintained in the posting
// transaction and checked by the ledger verifier. There is no cash screen: payments come from
// POS, invoices, debts, returns, exchanges and purchases, and the balances feed the dashboard and
// the cash-flow report.
package cash

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Fund kinds (fund_accounts.kind); a tenant has one fund of each.
const (
	FundCash = "cash"
	FundBank = "bank"
)

// Payment method codes (payment_methods.code).
const (
	MethodCash         = "cash"
	MethodBankTransfer = "bank_transfer"
)

// Cash entry categories (cash_entries.category), the classification of the cash-flow report.
const (
	CategorySales           = "sales"
	CategoryCustomerDeposit = "customer_deposit"
	CategoryDebtCollection  = "debt_collection"
	CategorySupplierPayment = "supplier_payment"
	CategorySupplierAdvance = "supplier_advance"
	CategoryRefund          = "refund"
	CategoryLandedCost      = "landed_cost"
	CategoryOpening         = "opening"
	CategoryOther           = "other"
)

// Categories lists every category in report order.
var Categories = []string{
	CategorySales, CategoryCustomerDeposit, CategoryDebtCollection, CategorySupplierPayment,
	CategorySupplierAdvance, CategoryRefund, CategoryLandedCost, CategoryOpening, CategoryOther,
}

// FundAccount is a fund. Version is the token of the owner-editable fields (name, bank details,
// opening balance); balance changes from postings do not bump it.
type FundAccount struct {
	ID             uuid.UUID   `db:"id"`
	TenantID       uuid.UUID   `db:"tenant_id"`
	Kind           string      `db:"kind"`
	Name           string      `db:"name"`
	BankName       *string     `db:"bank_name"`
	AccountNo      *string     `db:"account_no"`
	OpeningBalance money.Money `db:"opening_balance"`
	OpeningDate    *time.Time  `db:"opening_date"`
	Balance        money.Money `db:"balance"`
	Active         bool        `db:"active"`
	CreatedAt      time.Time   `db:"created_at"`
	UpdatedAt      time.Time   `db:"updated_at"`
	Version        int         `db:"version"`
}

// PaymentMethod pays into one fund. A method that requires verification (bank transfer) makes
// incoming payments wait in the verification queue before they count as money received.
type PaymentMethod struct {
	ID                   uuid.UUID `db:"id"`
	TenantID             uuid.UUID `db:"tenant_id"`
	Code                 string    `db:"code"`
	Name                 string    `db:"name"`
	FundAccountID        uuid.UUID `db:"fund_account_id"`
	FundKind             string    `db:"fund_kind"`
	RequiresVerification bool      `db:"requires_verification"`
	Active               bool      `db:"active"`
	CreatedAt            time.Time `db:"created_at"`
}

// Entry is one append-only movement of a fund: positive in, negative out. A reversal is a mirror
// entry naming the reversed one in ReversalOf.
type Entry struct {
	ID            uuid.UUID   `db:"id"`
	TenantID      uuid.UUID   `db:"tenant_id"`
	FundAccountID uuid.UUID   `db:"fund_account_id"`
	Amount        money.Money `db:"amount"`
	Category      string      `db:"category"`
	PaymentID     *uuid.UUID  `db:"payment_id"`
	ShiftID       *uuid.UUID  `db:"shift_id"`
	BusinessDate  time.Time   `db:"business_date"`
	PostedAt      time.Time   `db:"posted_at"`
	PostedBy      uuid.UUID   `db:"posted_by"`
	ReversalOf    *uuid.UUID  `db:"reversal_of"`
	CreatedAt     time.Time   `db:"created_at"`
}

// Payment directions and purposes, as stored on payments (the debt domain owns that table; cash
// only needs them to classify entries).
const (
	DirectionIn  = "in"
	DirectionOut = "out"

	PurposeSettlement     = "settlement"
	PurposeAdvance        = "advance"
	PurposeDeposit        = "deposit"
	PurposeRefund         = "refund"
	PurposeAdvanceRefund  = "advance_refund"
	PurposeOpeningAdvance = "opening_advance"
)

// CategoryFor classifies the cash entry of a payment by direction, purpose and the document
// that produced it (payments.source_type, "" when none):
//
//	refund, advance_refund                      → refund (either direction)
//	in  settlement from pos, invoice, exchange  → sales
//	in  settlement otherwise                    → debt_collection
//	in  advance, deposit, opening_advance       → customer_deposit
//	out settlement from landed_cost             → landed_cost
//	out settlement otherwise                    → supplier_payment
//	out advance, deposit, opening_advance       → supplier_advance
func CategoryFor(direction, purpose, sourceType string) (string, bool) {
	if direction != DirectionIn && direction != DirectionOut {
		return "", false
	}
	switch purpose {
	case PurposeRefund, PurposeAdvanceRefund:
		return CategoryRefund, true
	case PurposeAdvance, PurposeDeposit, PurposeOpeningAdvance:
		if direction == DirectionIn {
			return CategoryCustomerDeposit, true
		}
		return CategorySupplierAdvance, true
	case PurposeSettlement:
		if direction == DirectionIn {
			switch sourceType {
			case "pos", "invoice", "exchange":
				return CategorySales, true
			}
			return CategoryDebtCollection, true
		}
		if sourceType == "landed_cost" {
			return CategoryLandedCost, true
		}
		return CategorySupplierPayment, true
	}
	return "", false
}

func validCategory(c string) bool {
	for _, v := range Categories {
		if v == c {
			return true
		}
	}
	return false
}
