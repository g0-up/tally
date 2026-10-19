package cash

import "github.com/g0-up/tally/apps/api/internal/platform/apperr"

func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errFundHasEntries() error {
	return apperr.NewConflict("fund_has_entries",
		"The fund already has receipts or payments; its opening balance can no longer change")
}
