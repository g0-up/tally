// Package partner owns the trading partners of a tenant: parties, their customer and supplier
// profiles (one party may be both, with separate ledgers), customer groups, contacts and
// addresses.
package partner

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Profile statuses: a supplier "Đang giao dịch" is active, "Ngừng giao dịch" is inactive.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

// Party kinds.
const (
	KindPerson       = "person"
	KindOrganization = "organization"
)

// Base price kinds of a customer group: which base price list its customers buy at.
const (
	PriceRetail    = "retail"
	PriceWholesale = "wholesale"
)

// Party is a person or organisation the store trades with.
type Party struct {
	ID         uuid.UUID `db:"id"`
	Kind       string    `db:"kind"`
	Name       string    `db:"name"`
	NameKey    string    `db:"name_key"`
	Phone      *string   `db:"phone"`
	PhoneKey   *string   `db:"phone_key"`
	Email      *string   `db:"email"`
	TaxCode    *string   `db:"tax_code"`
	Address    *string   `db:"address"`
	Note       *string   `db:"note"`
	SearchNorm string    `db:"search_norm"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
	// Version is the optimistic version of the party; every change of a customer or supplier
	// bumps it, so it is the ETag of both.
	Version int `db:"version"`
}

// CustomerProfile is the customer side of a party.
type CustomerProfile struct {
	PartyID         uuid.UUID `db:"party_id"`
	CustomerGroupID uuid.UUID `db:"customer_group_id"`
	// CreditLimit nil means no limit; zero means no credit sales.
	CreditLimit      *money.Money `db:"credit_limit"`
	PaymentTermsDays int          `db:"payment_terms_days"`
	Status           string       `db:"status"`
	CreatedAt        time.Time    `db:"created_at"`
	UpdatedAt        time.Time    `db:"updated_at"`
	Version          int          `db:"version"`
}

// SupplierProfile is the supplier side of a party.
type SupplierProfile struct {
	PartyID          uuid.UUID `db:"party_id"`
	PaymentTermsDays int       `db:"payment_terms_days"`
	// SuppliedGoodsNote is the free-text "mặt hàng cung cấp", searchable.
	SuppliedGoodsNote *string   `db:"supplied_goods_note"`
	Status            string    `db:"status"`
	CreatedAt         time.Time `db:"created_at"`
	UpdatedAt         time.Time `db:"updated_at"`
	Version           int       `db:"version"`
}

// Customer is a party with its customer profile.
type Customer struct {
	Party
	Profile   CustomerProfile
	GroupName string
	// IsSupplier reports whether the party also has a supplier profile.
	IsSupplier bool
	// Activity is set by the customer list only.
	Activity *CustomerActivity
}

// CustomerActivity is what the customer list shows of a customer beyond its profile.
type CustomerActivity struct {
	// LastPurchaseAt is when the latest sale to the customer that is not fully reversed posted.
	LastPurchaseAt *time.Time
	// Consents is the latest consent per channel and purpose.
	Consents []CustomerConsent
}

// CustomerConsent is the current state of one channel and purpose of a customer's consent log.
type CustomerConsent struct {
	Channel    string    `json:"channel"`
	Purpose    string    `json:"purpose"`
	Granted    bool      `json:"granted"`
	RecordedAt time.Time `json:"recorded_at"`
}

// SupplierActivity is what the supplier list shows of the store's trading with a supplier.
type SupplierActivity struct {
	// ContactName is the primary contact, else the first one added.
	ContactName *string `db:"contact_name"`
	OrderCount  int     `db:"order_count"`
	// LastReceiptDate is the business date of the latest posted receipt.
	LastReceiptDate *time.Time `db:"last_receipt_date"`
	// PayableOpen is what the store still owes the supplier over its open payables.
	PayableOpen money.Money `db:"payable_open"`
	// HasOpenOrders blocks stopping the supplier; InUse blocks deleting it.
	HasOpenOrders bool `db:"has_open_orders"`
	InUse         bool `db:"in_use"`
}

// Supplier is a party with its supplier profile.
type Supplier struct {
	Party
	Profile SupplierProfile
	// IsCustomer reports whether the party also has a customer profile.
	IsCustomer bool
	// Activity is set by the supplier list only.
	Activity *SupplierActivity
}

// CustomerGroup groups customers by the base price list they buy at.
type CustomerGroup struct {
	ID            uuid.UUID `db:"id"`
	Name          string    `db:"name"`
	NameKey       string    `db:"name_key"`
	BasePriceKind string    `db:"base_price_kind"`
	IsSystem      bool      `db:"is_system"`
	Active        bool      `db:"active"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

// Contact is a person to talk to at a party.
type Contact struct {
	ID        uuid.UUID `db:"id"`
	PartyID   uuid.UUID `db:"party_id"`
	Name      string    `db:"name"`
	Phone     *string   `db:"phone"`
	Email     *string   `db:"email"`
	RoleTitle *string   `db:"role_title"`
	IsPrimary bool      `db:"is_primary"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Address is a delivery or billing address of a party.
type Address struct {
	ID          uuid.UUID `db:"id"`
	PartyID     uuid.UUID `db:"party_id"`
	Label       string    `db:"label"`
	AddressLine string    `db:"address_line"`
	IsDefault   bool      `db:"is_default"`
	CreatedAt   time.Time `db:"created_at"`
}

// Opt is an optional field of a partial update that may also be cleared: Set reports whether
// the field was given, Value is nil when it was given as null.
type Opt[T any] struct {
	Set   bool
	Value *T
}

// Some returns a set Opt holding v.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: &v} }

// UnmarshalJSON marks the field as given; JSON null clears it.
func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(bytes.TrimSpace(data)) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}
