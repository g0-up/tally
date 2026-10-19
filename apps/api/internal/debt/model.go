// Package debt keeps what parties owe the store and what the store owes them. An obligation
// (receivable or payable) is created once per source document; payments (PT in, PC out), credit
// notes, debt adjustments (DN) and offsets (BT) settle it through append-only settlement rows.
// Balances are never typed in: open = amount − Σ settlements, an advance = confirmed payment −
// Σ its allocations. The settled and allocated amounts on the rows are projections maintained in
// the posting transaction and checked by the ledger verifier. Every money movement goes through
// posting.Coordinator.
package debt

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Ledger directions (obligations, credit notes, adjustments, statements, collection notes).
const (
	Receivable = "receivable"
	Payable    = "payable"
)

// Payment directions: money in (PT) settles receivables, money out (PC) settles payables.
const (
	In  = "in"
	Out = "out"
)

// Obligation sources (obligations.source_type).
const (
	SourceSalesDocument           = "sales_document"
	SourceReceipt                 = "receipt"
	SourceLandedCostAllocation    = "landed_cost_allocation"
	SourcePurchasePriceAdjustment = "purchase_price_adjustment"
	SourceDebtAdjustment          = "debt_adjustment"
	SourceOpeningBalance          = "opening_balance"
	SourceRefundDue               = "refund_due"
	SourceDebtOffset              = "debt_offset"
)

// Obligation statuses.
const (
	ObligationOpen    = "open"
	ObligationSettled = "settled"
)

// Payment purposes.
const (
	PurposeSettlement     = "settlement"
	PurposeAdvance        = "advance"
	PurposeDeposit        = "deposit"
	PurposeRefund         = "refund"
	PurposeAdvanceRefund  = "advance_refund"
	PurposeOpeningAdvance = "opening_advance"
)

// Payment statuses.
const (
	PaymentPending   = "pending_verification"
	PaymentConfirmed = "confirmed"
	PaymentReversed  = "reversed"
)

// Payment sources (payments.source_type): the document or screen that took or paid the money.
const (
	PaymentSourcePOS         = "pos"
	PaymentSourceInvoice     = "invoice"
	PaymentSourceDebt        = "debt"
	PaymentSourceSalesReturn = "sales_return"
	PaymentSourceExchange    = "exchange"
	PaymentSourcePurchase    = "purchase"
	PaymentSourceLandedCost  = "landed_cost"
)

// Settlement sources and allocation modes.
const (
	SettledByPayment        = "payment"
	SettledByCreditNote     = "credit_note"
	SettledByDebtOffset     = "debt_offset"
	SettledByDebtAdjustment = "debt_adjustment"
	// SettledByReversal closes an obligation whose source document was reversed; it is settled
	// by the obligation itself.
	SettledByReversal = "obligation_reversal"

	ModeManual        = "manual"
	ModeSuggested     = "suggested_oldest_due"
	ModeAutoEarmarked = "auto_earmarked"
)

// Credit note sources.
const (
	CreditSalesReturn             = "sales_return"
	CreditPurchaseReturn          = "purchase_return"
	CreditPurchasePriceAdjustment = "purchase_price_adjustment"
	CreditPostSaleDiscount        = "post_sale_discount" //nolint:gosec // a credit note source, not a credential
)

// Debt adjustment kinds (DN).
const (
	AdjustIncrease          = "increase"
	AdjustDecrease          = "decrease"
	AdjustWriteOff          = "write_off"
	AdjustDisputeSettlement = "dispute_settlement"
	AdjustOpeningBalance    = "opening_balance"
)

// Statement statuses (StatementStatus). A version is "superseded" once a later version exists.
const (
	StatementDraft      = "draft"
	StatementSent       = "sent"
	StatementConfirmed  = "confirmed"
	StatementDisputed   = "disputed"
	StatementSuperseded = "superseded"
)

// Collection note kinds and channels.
const (
	NoteNote         = "note"
	NoteCall         = "call"
	NotePromise      = "promise"
	NoteReminderSent = "reminder_sent"

	ChannelPhone    = "phone"
	ChannelSMS      = "sms"
	ChannelZalo     = "zalo"
	ChannelEmail    = "email"
	ChannelInPerson = "in_person"
)

// Onboarding steps the debt domain completes.
const (
	StepOpeningDebt = "opening_debt"
)

// PaymentDirectionOf is the payment direction that settles a ledger direction.
func PaymentDirectionOf(ledger string) string {
	if ledger == Payable {
		return Out
	}
	return In
}

// LedgerOf is the ledger direction a payment direction settles.
func LedgerOf(paymentDirection string) string {
	if paymentDirection == Out {
		return Payable
	}
	return Receivable
}

// Opposite returns the other ledger direction.
func Opposite(ledger string) string {
	if ledger == Payable {
		return Receivable
	}
	return Payable
}

// Obligation is one amount a party owes the store (receivable) or the store owes it (payable).
type Obligation struct {
	ID            uuid.UUID   `db:"id"`
	TenantID      uuid.UUID   `db:"tenant_id"`
	PartyID       uuid.UUID   `db:"party_id"`
	Direction     string      `db:"direction"`
	SourceType    string      `db:"source_type"`
	SourceID      uuid.UUID   `db:"source_id"`
	SourceDocNo   string      `db:"source_doc_no"`
	BusinessDate  time.Time   `db:"business_date"`
	DueDate       time.Time   `db:"due_date"`
	Amount        money.Money `db:"amount"`
	SettledAmount money.Money `db:"settled_amount"`
	Status        string      `db:"status"`
	CampaignID    *uuid.UUID  `db:"campaign_id"`
	// ReversedOn, ReversalReason and ReversalDocNo are set when the source document was reversed
	// (ObligationReverser): the obligation is then settled by an obligation_reversal settlement.
	ReversedOn     *time.Time `db:"reversed_on"`
	ReversalReason *string    `db:"reversal_reason"`
	ReversalDocNo  *string    `db:"reversal_doc_no"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	Version        int        `db:"version"`
}

// Open is the unpaid part.
func (o Obligation) Open() money.Money { return o.Amount.Sub(o.SettledAmount) }

// Installment is one scheduled part of an obligation.
type Installment struct {
	ID            uuid.UUID   `db:"id"`
	ObligationID  uuid.UUID   `db:"obligation_id"`
	InstallmentNo int         `db:"installment_no"`
	DueDate       time.Time   `db:"due_date"`
	Amount        money.Money `db:"amount"`
}

// Payment is a PT (in) or PC (out).
type Payment struct {
	ID                 uuid.UUID   `db:"id"`
	TenantID           uuid.UUID   `db:"tenant_id"`
	DocNo              string      `db:"doc_no"`
	Direction          string      `db:"direction"`
	PartyID            *uuid.UUID  `db:"party_id"`
	Purpose            string      `db:"purpose"`
	FundAccountID      uuid.UUID   `db:"fund_account_id"`
	PaymentMethodID    uuid.UUID   `db:"payment_method_id"`
	Amount             money.Money `db:"amount"`
	AllocatedAmount    money.Money `db:"allocated_amount"`
	Status             string      `db:"status"`
	EarmarkedInvoiceID *uuid.UUID  `db:"earmarked_invoice_id"`
	SourceType         *string     `db:"source_type"`
	SourceID           *uuid.UUID  `db:"source_id"`
	BusinessDate       time.Time   `db:"business_date"`
	Reference          *string     `db:"reference"`
	Note               *string     `db:"note"`
	PostedAt           time.Time   `db:"posted_at"`
	PostedBy           uuid.UUID   `db:"posted_by"`
	RefundOf           *uuid.UUID  `db:"refund_of"`
	ConfirmedAt        *time.Time  `db:"confirmed_at"`
	ReversedOn         *time.Time  `db:"reversed_on"`
	// Planned are the allocations chosen for a payment waiting for verification; they are applied
	// when it is confirmed.
	Planned   PlannedAllocations `db:"planned_allocations"`
	CreatedAt time.Time          `db:"created_at"`
	UpdatedAt time.Time          `db:"updated_at"`
	Version   int                `db:"version"`
}

// Unallocated is the part of a confirmed payment not yet applied to an obligation: an advance.
func (p Payment) Unallocated() money.Money {
	if p.Status != PaymentConfirmed {
		return money.Money{}
	}
	return p.Amount.Sub(p.AllocatedAmount)
}

// CreditNote reduces a party's receivable (sales return, post-sale discount) or payable
// (purchase return, purchase price adjustment).
type CreditNote struct {
	ID              uuid.UUID   `db:"id"`
	TenantID        uuid.UUID   `db:"tenant_id"`
	PartyID         uuid.UUID   `db:"party_id"`
	Direction       string      `db:"direction"`
	SourceType      string      `db:"source_type"`
	SourceID        uuid.UUID   `db:"source_id"`
	SourceDocNo     string      `db:"source_doc_no"`
	Amount          money.Money `db:"amount"`
	AllocatedAmount money.Money `db:"allocated_amount"`
	BusinessDate    time.Time   `db:"business_date"`
	ReasonID        *uuid.UUID  `db:"reason_id"`
	PostedAt        time.Time   `db:"posted_at"`
	PostedBy        uuid.UUID   `db:"posted_by"`
	CreatedAt       time.Time   `db:"created_at"`
	Version         int         `db:"version"`
}

// Adjustment is a DN.
type Adjustment struct {
	ID                 uuid.UUID   `db:"id"`
	TenantID           uuid.UUID   `db:"tenant_id"`
	DocNo              string      `db:"doc_no"`
	PartyID            uuid.UUID   `db:"party_id"`
	Direction          string      `db:"direction"`
	Kind               string      `db:"kind"`
	Amount             money.Money `db:"amount"`
	TargetObligationID *uuid.UUID  `db:"target_obligation_id"`
	ReasonID           uuid.UUID   `db:"reason_id"`
	OpeningCutoffDate  *time.Time  `db:"opening_cutoff_date"`
	BusinessDate       time.Time   `db:"business_date"`
	Note               *string     `db:"note"`
	PostedAt           time.Time   `db:"posted_at"`
	PostedBy           uuid.UUID   `db:"posted_by"`
	CreatedAt          time.Time   `db:"created_at"`
}

// Offset is a BT: the same amount settles a receivable and a payable of one party.
type Offset struct {
	ID                     uuid.UUID   `db:"id"`
	TenantID               uuid.UUID   `db:"tenant_id"`
	DocNo                  string      `db:"doc_no"`
	PartyID                uuid.UUID   `db:"party_id"`
	Amount                 money.Money `db:"amount"`
	ReceivableObligationID uuid.UUID   `db:"receivable_obligation_id"`
	PayableObligationID    uuid.UUID   `db:"payable_obligation_id"`
	AttachmentID           *uuid.UUID  `db:"attachment_id"`
	BusinessDate           time.Time   `db:"business_date"`
	Note                   *string     `db:"note"`
	PostedAt               time.Time   `db:"posted_at"`
	PostedBy               uuid.UUID   `db:"posted_by"`
	CreatedAt              time.Time   `db:"created_at"`
}

// Settlement is one append-only application of a payment, credit note, adjustment or offset to
// an obligation; a reversal is a negative row naming the reversed one.
type Settlement struct {
	ID             uuid.UUID   `db:"id"`
	TenantID       uuid.UUID   `db:"tenant_id"`
	ObligationID   uuid.UUID   `db:"obligation_id"`
	SettledByType  string      `db:"settled_by_type"`
	SettledByID    uuid.UUID   `db:"settled_by_id"`
	Amount         money.Money `db:"amount"`
	BusinessDate   time.Time   `db:"business_date"`
	AllocationMode string      `db:"allocation_mode"`
	PostedAt       time.Time   `db:"posted_at"`
	PostedBy       uuid.UUID   `db:"posted_by"`
	ReversalOf     *uuid.UUID  `db:"reversal_of"`
	CreatedAt      time.Time   `db:"created_at"`
}

// Statement is a DS: the account of one party and ledger over a period, with frozen versions.
type Statement struct {
	ID             uuid.UUID  `db:"id"`
	TenantID       uuid.UUID  `db:"tenant_id"`
	DocNo          string     `db:"doc_no"`
	PartyID        uuid.UUID  `db:"party_id"`
	Direction      string     `db:"direction"`
	PeriodFrom     time.Time  `db:"period_from"`
	PeriodTo       time.Time  `db:"period_to"`
	Status         string     `db:"status"`
	CurrentVersion int        `db:"current_version"`
	SentVersion    *int       `db:"sent_version"`
	SentAt         *time.Time `db:"sent_at"`
	DisputeNote    *string    `db:"dispute_note"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	Version        int        `db:"version"`
}

// StatementVersion is one immutable generation of a statement.
type StatementVersion struct {
	ID             uuid.UUID       `db:"id"`
	StatementID    uuid.UUID       `db:"statement_id"`
	VersionNo      int             `db:"version_no"`
	OpeningBalance money.Money     `db:"opening_balance"`
	ClosingBalance money.Money     `db:"closing_balance"`
	Snapshot       json.RawMessage `db:"snapshot"`
	CreatedBy      uuid.UUID       `db:"created_by"`
	CreatedAt      time.Time       `db:"created_at"`
}

// VersionStatus derives the status of version n of s: earlier versions are superseded, the
// current one carries the statement status.
func (s Statement) VersionStatus(n int) string {
	if n < s.CurrentVersion {
		return StatementSuperseded
	}
	return s.Status
}

// CollectionNote is a contact note, a payment promise or a logged reminder.
type CollectionNote struct {
	ID             uuid.UUID    `db:"id"`
	TenantID       uuid.UUID    `db:"tenant_id"`
	PartyID        uuid.UUID    `db:"party_id"`
	Direction      string       `db:"direction"`
	Kind           string       `db:"kind"`
	Body           *string      `db:"body"`
	PromisedDate   *time.Time   `db:"promised_date"`
	PromisedAmount *money.Money `db:"promised_amount"`
	Channel        *string      `db:"channel"`
	DedupeKey      *string      `db:"dedupe_key"`
	CreatedBy      uuid.UUID    `db:"created_by"`
	CreatedAt      time.Time    `db:"created_at"`
}
