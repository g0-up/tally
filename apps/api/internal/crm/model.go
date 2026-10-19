// Package crm keeps the customer relationship: the timeline of purchases, returns, payments,
// notes and care calls, the consent log, care tasks, re-order drafts and the optional loyalty
// ledger.
package crm

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Activity kinds of the customer timeline.
const (
	ActivityPurchase      = "purchase"
	ActivityReturn        = "return"
	ActivityPayment       = "payment"
	ActivityNote          = "note"
	ActivityCareCall      = "care_call"
	ActivityReorder       = "reorder"
	ActivityConsentChange = "consent_change"
)

// Consent channels and purposes.
const (
	ChannelPhone = "phone"
	ChannelSMS   = "sms"
	ChannelZalo  = "zalo"
	ChannelEmail = "email"

	PurposeDebtReminder = "debt_reminder"
	PurposeMarketing    = "marketing"
	PurposeCare         = "care"
)

// Care task statuses.
const (
	TaskOpen    = "open"
	TaskDone    = "done"
	TaskSkipped = "skipped"
)

// Loyalty entry kinds.
const (
	EntryEarn    = "earn"
	EntryRedeem  = "redeem"
	EntryReverse = "reverse"
	EntryManual  = "manual"
)

// Reference types of timeline rows and loyalty entries.
const (
	RefSalesDocument = "sales_document"
	RefSaleReversal  = "sales_document_reversal"
	RefSalesReturn   = "sales_return"
	// The points redeemed on a sale that its returns and its reversal give back.
	RefSalesReturnRedemption  = "sales_return_redemption"
	RefSaleReversalRedemption = "sales_document_reversal_redemption"
	RefPayment                = "payment"
	RefPaymentReverse         = "payment_reversal"
	RefInvoice                = "invoice"
	RefConsent                = "consent"
	RefManual                 = "manual"
)

func validChannel(c string) bool {
	switch c {
	case ChannelPhone, ChannelSMS, ChannelZalo, ChannelEmail:
		return true
	}
	return false
}

func validPurpose(p string) bool {
	switch p {
	case PurposeDebtReminder, PurposeMarketing, PurposeCare:
		return true
	}
	return false
}

func validTaskStatus(s string) bool {
	switch s {
	case TaskOpen, TaskDone, TaskSkipped:
		return true
	}
	return false
}

// Activity is one row of a customer's timeline.
type Activity struct {
	ID        uuid.UUID  `db:"id"`
	PartyID   uuid.UUID  `db:"party_id"`
	Kind      string     `db:"kind"`
	RefType   *string    `db:"ref_type"`
	RefID     *uuid.UUID `db:"ref_id"`
	Summary   string     `db:"summary"`
	CreatedBy *uuid.UUID `db:"created_by"`
	CreatedAt time.Time  `db:"created_at"`
}

// Consent is one entry of the consent log; the latest per party, channel and purpose wins.
type Consent struct {
	ID         uuid.UUID `db:"id"`
	PartyID    uuid.UUID `db:"party_id"`
	Channel    string    `db:"channel"`
	Purpose    string    `db:"purpose"`
	Granted    bool      `db:"granted"`
	Source     string    `db:"source"`
	Evidence   *string   `db:"evidence"`
	RecordedBy uuid.UUID `db:"recorded_by"`
	RecordedAt time.Time `db:"recorded_at"`
}

// CareTask is a scheduled follow-up with a customer.
type CareTask struct {
	ID               uuid.UUID  `db:"id"`
	PartyID          uuid.UUID  `db:"party_id"`
	PartyName        string     `db:"party_name"`
	DueOn            time.Time  `db:"due_on"`
	Title            string     `db:"title"`
	SourceCampaignID *uuid.UUID `db:"source_campaign_id"`
	Status           string     `db:"status"`
	DoneAt           *time.Time `db:"done_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

// LastSeasonCustomer is a customer who bought lines attributed (primary) to a campaign.
type LastSeasonCustomer struct {
	PartyID        uuid.UUID   `db:"party_id"`
	Name           string      `db:"name"`
	NameKey        string      `db:"name_key"`
	Phone          *string     `db:"phone"`
	Documents      int         `db:"documents"`
	NetRevenue     money.Money `db:"net_revenue"`
	LastPurchaseOn time.Time   `db:"last_purchase_on"`
	OpenTasks      int         `db:"open_tasks"`
}

// Program is the tenant's loyalty program; it is off until the owner enables it.
type Program struct {
	Enabled bool `db:"enabled"`
	// PointsPerAmount is the points earned per đồng of net revenue.
	PointsPerAmount money.Money `db:"points_per_amount"`
	// PointValue is the đồng a point is worth when redeemed as a discount.
	PointValue money.Money `db:"point_value"`
	UpdatedAt  *time.Time  `db:"updated_at"`
	// Version is 0 while the program was never saved.
	Version int `db:"version"`
}

// LoyaltyEntry is one row of the append-only loyalty ledger.
type LoyaltyEntry struct {
	ID           uuid.UUID  `db:"id"`
	PartyID      uuid.UUID  `db:"party_id"`
	Points       money.Qty  `db:"points"`
	Kind         string     `db:"kind"`
	SourceType   string     `db:"source_type"`
	SourceID     uuid.UUID  `db:"source_id"`
	ReversalOf   *uuid.UUID `db:"reversal_of"`
	BusinessDate time.Time  `db:"business_date"`
	CreatedBy    *uuid.UUID `db:"created_by"`
	CreatedAt    time.Time  `db:"created_at"`
}

// Loyalty is a customer's balance (Σ entries) with a page of entries, newest first.
type Loyalty struct {
	PartyID uuid.UUID
	Balance money.Qty
	Entries []LoyaltyEntry
}
