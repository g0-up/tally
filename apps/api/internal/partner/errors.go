package partner

import "github.com/g0-up/tally/apps/api/internal/platform/apperr"

// errNotFound is the single answer for a record that does not exist or belongs to another
// tenant, so ids of other stores are indistinguishable from unknown ids.
func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

// errInvalid reports field-level validation failures (field path → reason code).
func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errAlreadyCustomer() error {
	return apperr.NewConflict("already_customer", "The party is already a customer")
}

func errAlreadySupplier() error {
	return apperr.NewConflict("already_supplier", "The party is already a supplier")
}

func errCustomerInUse() error {
	return apperr.NewConflict("customer_in_use",
		"The customer has documents, debts or prices; it can only be marked inactive")
}

func errSupplierInUse() error {
	return apperr.NewConflict("supplier_in_use",
		"The supplier has documents or debts; it can only be marked as no longer trading")
}

func errSupplierHasOpenOrders() error {
	return apperr.NewConflict("supplier_has_open_orders",
		"The supplier has open purchase orders; receive or close them before stopping trading")
}

func errGroupNameTaken() error {
	return apperr.NewConflict("name_taken", "Another customer group already uses this name")
}

func errGroupInUse() error {
	return apperr.NewConflict("customer_group_in_use",
		"Customers, price lists or promotions use the group; deactivate it instead")
}

func errSystemGroup() error {
	return apperr.NewForbidden("read_only", "A built-in customer group cannot be deleted, deactivated or repriced")
}

func errGroupInactive() error {
	return errInvalid(map[string]string{"customer_group_id": "inactive"})
}

// errPartyInUse is a party another module still references after its last profile went away.
// The usage readers normally catch it first; the foreign key is the backstop.
func errPartyInUse() error {
	return apperr.NewConflict("party_in_use", "Other records still reference this partner; deactivate it instead")
}

func isNotFound(err error) bool { return apperr.KindOf(err) == apperr.NotFound }
