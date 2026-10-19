package pricing

import "github.com/g0-up/tally/apps/api/internal/platform/apperr"

// errNotFound is the single answer for a record that does not exist or belongs to another
// tenant, so ids of other stores are indistinguishable from unknown ids.
func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

// errInvalid reports field-level validation failures (field path → reason code).
func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errListNameTaken() error {
	return apperr.NewConflict("name_taken", "Another price list already uses this name")
}

func errBaseListExists() error {
	return apperr.NewConflict("base_price_list_exists",
		"The store already has this base price list; each store has one retail and one wholesale list")
}

func errBaseListMissing() error {
	return apperr.NewConflict("base_price_list_missing",
		"The base price list could not be created because another list uses its name; rename that list")
}

func errBaseListLocked() error {
	return apperr.NewConflict("base_price_list_locked",
		"A base price list cannot be deleted, deactivated or given a validity range")
}

func errListInUse() error {
	return apperr.NewConflict("price_list_in_use",
		"The price list has prices or price history; deactivate it instead")
}

func errCodeTaken() error {
	return apperr.NewConflict("code_taken", "Another promotion already uses this code")
}

func errUsageLimitReached() error {
	return apperr.NewConflict("promotion_usage_limit_reached", "The promotion has been used as many times as allowed")
}

func isNotFound(err error) bool { return apperr.KindOf(err) == apperr.NotFound }
