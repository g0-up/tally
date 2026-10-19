package campaign

import (
	"fmt"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Error codes of the campaign module.
const (
	CodeNameTaken     = "campaign_name_taken"
	CodeStatusInvalid = "campaign_status_invalid"
	CodeClosed        = "campaign_closed"
	// WarnOutsidePeriod flags an attribution of a document dated outside the campaign period; it
	// is allowed and audited.
	WarnOutsidePeriod = "outside_period"
)

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

func errNameTaken() error {
	return apperr.NewConflict(CodeNameTaken, "Another campaign already has this name")
}

func errStatus(from, to string) error {
	return apperr.NewConflict(CodeStatusInvalid, fmt.Sprintf("A %s campaign cannot become %s", from, to))
}

func errClosed() error {
	return apperr.NewConflict(CodeClosed, "The campaign is closed")
}
