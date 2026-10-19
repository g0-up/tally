package partner

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the partner endpoints. Money travels as decimal strings.

func mapSlice[T, D any](in []T, fn func(T) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

type partyDTO struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Phone     *string   `json:"phone"`
	Email     *string   `json:"email"`
	TaxCode   *string   `json:"tax_code"`
	Address   *string   `json:"address"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

func toPartyDTO(p Party) partyDTO {
	return partyDTO{
		ID: p.ID, Kind: p.Kind, Name: p.Name, Phone: p.Phone, Email: p.Email, TaxCode: p.TaxCode,
		Address: p.Address, Note: p.Note, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Version: p.Version,
	}
}

type customerDTO struct {
	partyDTO
	CustomerGroupID   uuid.UUID    `json:"customer_group_id"`
	CustomerGroupName string       `json:"customer_group_name"`
	CreditLimit       *money.Money `json:"credit_limit"`
	PaymentTermsDays  int          `json:"payment_terms_days"`
	Status            string       `json:"status"`
	IsSupplier        bool         `json:"is_supplier"`
}

func toCustomerDTO(c Customer) customerDTO {
	return customerDTO{
		partyDTO: toPartyDTO(c.Party), CustomerGroupID: c.Profile.CustomerGroupID, CustomerGroupName: c.GroupName,
		CreditLimit: c.Profile.CreditLimit, PaymentTermsDays: c.Profile.PaymentTermsDays, Status: c.Profile.Status,
		IsSupplier: c.IsSupplier,
	}
}

type customerConsentDTO struct {
	Channel    string    `json:"channel"`
	Purpose    string    `json:"purpose"`
	Granted    bool      `json:"granted"`
	RecordedAt time.Time `json:"recorded_at"`
}

// customerListItemDTO is a customer row of the customer list.
type customerListItemDTO struct {
	customerDTO
	LastPurchaseAt *time.Time           `json:"last_purchase_at"`
	Consents       []customerConsentDTO `json:"consents"`
}

func toCustomerListItemDTO(c Customer) customerListItemDTO {
	out := customerListItemDTO{customerDTO: toCustomerDTO(c), Consents: []customerConsentDTO{}}
	if a := c.Activity; a != nil {
		out.LastPurchaseAt = a.LastPurchaseAt
		out.Consents = mapSlice(a.Consents, func(v CustomerConsent) customerConsentDTO { return customerConsentDTO(v) })
	}
	return out
}

type supplierDTO struct {
	partyDTO
	PaymentTermsDays  int     `json:"payment_terms_days"`
	SuppliedGoodsNote *string `json:"supplied_goods_note"`
	Status            string  `json:"status"`
	IsCustomer        bool    `json:"is_customer"`
}

func toSupplierDTO(s Supplier) supplierDTO {
	return supplierDTO{
		partyDTO: toPartyDTO(s.Party), PaymentTermsDays: s.Profile.PaymentTermsDays,
		SuppliedGoodsNote: s.Profile.SuppliedGoodsNote, Status: s.Profile.Status, IsCustomer: s.IsCustomer,
	}
}

// supplierListItemDTO is a supplier row of the supplier list. CanStop and CanDelete tell the
// screen which actions the server will refuse.
type supplierListItemDTO struct {
	supplierDTO
	ContactName     *string     `json:"contact_name"`
	OrderCount      int         `json:"order_count"`
	LastReceiptDate *string     `json:"last_receipt_date"`
	PayableOpen     money.Money `json:"payable_open"`
	CanStop         bool        `json:"can_stop"`
	CanDelete       bool        `json:"can_delete"`
}

func toSupplierListItemDTO(s Supplier) supplierListItemDTO {
	out := supplierListItemDTO{supplierDTO: toSupplierDTO(s)}
	if a := s.Activity; a != nil {
		out.ContactName, out.OrderCount, out.PayableOpen = a.ContactName, a.OrderCount, a.PayableOpen
		out.CanStop, out.CanDelete = !a.HasOpenOrders, !a.InUse
		if a.LastReceiptDate != nil {
			d := a.LastReceiptDate.Format(time.DateOnly)
			out.LastReceiptDate = &d
		}
	}
	return out
}

type duplicateDTO struct {
	PartyID    uuid.UUID `json:"party_id"`
	Name       string    `json:"name"`
	Matches    []string  `json:"matches"`
	IsCustomer bool      `json:"is_customer"`
	IsSupplier bool      `json:"is_supplier"`
}

func toDuplicateDTO(w DuplicateWarning) duplicateDTO {
	return duplicateDTO(w)
}

type customerResultDTO struct {
	customerDTO
	DuplicateWarnings []duplicateDTO `json:"duplicate_warnings"`
}

func toCustomerResultDTO(r CustomerResult) customerResultDTO {
	return customerResultDTO{customerDTO: toCustomerDTO(r.Customer), DuplicateWarnings: mapSlice(r.Warnings, toDuplicateDTO)}
}

type supplierResultDTO struct {
	supplierDTO
	DuplicateWarnings []duplicateDTO `json:"duplicate_warnings"`
}

func toSupplierResultDTO(r SupplierResult) supplierResultDTO {
	return supplierResultDTO{supplierDTO: toSupplierDTO(r.Supplier), DuplicateWarnings: mapSlice(r.Warnings, toDuplicateDTO)}
}

type customerGroupDTO struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	BasePriceKind string    `json:"base_price_kind"`
	IsSystem      bool      `json:"is_system"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toCustomerGroupDTO(g CustomerGroup) customerGroupDTO {
	return customerGroupDTO{
		ID: g.ID, Name: g.Name, BasePriceKind: g.BasePriceKind, IsSystem: g.IsSystem, Active: g.Active,
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

type contactDTO struct {
	ID        uuid.UUID `json:"id"`
	PartyID   uuid.UUID `json:"party_id"`
	Name      string    `json:"name"`
	Phone     *string   `json:"phone"`
	Email     *string   `json:"email"`
	RoleTitle *string   `json:"role_title"`
	IsPrimary bool      `json:"is_primary"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toContactDTO(c Contact) contactDTO {
	return contactDTO(c)
}

type addressDTO struct {
	ID          uuid.UUID `json:"id"`
	PartyID     uuid.UUID `json:"party_id"`
	Label       string    `json:"label"`
	AddressLine string    `json:"address_line"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
}

func toAddressDTO(a Address) addressDTO {
	return addressDTO(a)
}

// partyRequest are the party fields of the customer and supplier bodies. Optional texts may be
// null to clear them.
type partyRequest struct {
	Kind    *string     `json:"kind"`
	Name    *string     `json:"name"`
	Phone   Opt[string] `json:"phone"`
	Email   Opt[string] `json:"email"`
	TaxCode Opt[string] `json:"tax_code"`
	Address Opt[string] `json:"address"`
	Note    Opt[string] `json:"note"`
}

func (r partyRequest) input() PartyInput {
	return PartyInput(r)
}

// customerRequest is the body of POST /customers and PATCH /customers/{id}.
type customerRequest struct {
	partyRequest
	PartyID          *uuid.UUID       `json:"party_id"`
	CustomerGroupID  *uuid.UUID       `json:"customer_group_id"`
	CreditLimit      Opt[money.Money] `json:"credit_limit"`
	PaymentTermsDays *int             `json:"payment_terms_days"`
	Status           *string          `json:"status"`
}

func (r customerRequest) input() CustomerInput {
	return CustomerInput{
		PartyInput: r.partyRequest.input(), PartyID: r.PartyID, CustomerGroupID: r.CustomerGroupID,
		CreditLimit: r.CreditLimit, PaymentTermsDays: r.PaymentTermsDays, Status: r.Status,
	}
}

// supplierRequest is the body of POST /suppliers and PATCH /suppliers/{id}.
type supplierRequest struct {
	partyRequest
	PartyID           *uuid.UUID  `json:"party_id"`
	PaymentTermsDays  *int        `json:"payment_terms_days"`
	SuppliedGoodsNote Opt[string] `json:"supplied_goods_note"`
}

func (r supplierRequest) input() SupplierInput {
	return SupplierInput{
		PartyInput: r.partyRequest.input(), PartyID: r.PartyID, PaymentTermsDays: r.PaymentTermsDays,
		SuppliedGoodsNote: r.SuppliedGoodsNote,
	}
}

type customerGroupRequest struct {
	Name          *string `json:"name"`
	BasePriceKind *string `json:"base_price_kind"`
	Active        *bool   `json:"active"`
}

type contactRequest struct {
	Name      *string     `json:"name"`
	Phone     Opt[string] `json:"phone"`
	Email     Opt[string] `json:"email"`
	RoleTitle Opt[string] `json:"role_title"`
	IsPrimary *bool       `json:"is_primary"`
}

type addressRequest struct {
	Label       *string `json:"label"`
	AddressLine *string `json:"address_line"`
	IsDefault   *bool   `json:"is_default"`
}
