package debt

import (
	"fmt"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errField(code, field, reason, message string) error {
	return apperr.NewValidation(code, message, map[string]string{field: reason})
}

func lineField(i int, name string) string { return fmt.Sprintf("allocations[%d].%s", i, name) }

func errExceedsOpen(i int) error {
	return errField("allocation_exceeds_open", lineField(i, "amount"), "exceeds_open",
		"The amount is more than what is still open on the document")
}

func errExceedsPayment() error {
	return errField("allocation_exceeds_payment", "allocations", "exceeds_payment",
		"The allocations add up to more than the unallocated part of the payment")
}

func errPartyMismatch(i int) error {
	return errField("allocation_party_mismatch", lineField(i, "obligation_id"), "other_party",
		"The document belongs to another party")
}

func errDirectionMismatch(i int) error {
	return errField("allocation_direction_mismatch", lineField(i, "obligation_id"), "other_direction",
		"The document is on the other ledger (receivable/payable)")
}

func errPaymentPending() error {
	return apperr.NewValidation("payment_pending_verification",
		"The payment is waiting for verification; confirm it before allocating", map[string]string{"payment_id": "pending_verification"})
}

func errPaymentReversed() error {
	return apperr.NewConflict("payment_reversed", "The payment has been reversed")
}

func errPaymentNotPending() error {
	return apperr.NewConflict("payment_not_pending", "The payment is not waiting for verification")
}

func errPaymentHasSuccessor() error {
	return apperr.NewConflict("payment_has_successor",
		"The money of this payment was refunded; reverse the refund first")
}

func errInsufficientAdvance(field string) error {
	return errField("insufficient_advance", field, "exceeds_advance",
		"The amount is more than the unallocated advance of the payment")
}

func errObligationSettled(field string) error {
	return errField("obligation_settled", field, "settled", "The document has nothing left to settle")
}

func errStatementConfirmed() error {
	return apperr.NewConflict("statement_confirmed", "The statement was confirmed by the party and can no longer change")
}

func errStatementState(code, message string) error { return apperr.NewConflict(code, message) }

func errNothingOwed() error {
	return apperr.NewValidation("nothing_owed", "Nothing is open on this ledger for the party", map[string]string{"direction": "nothing_owed"})
}

func isNotFound(err error) bool { return apperr.Is(err, "not_found") }

func errObligationExists() error {
	return apperr.NewConflict("obligation_exists", "The source document already has a different obligation on this ledger")
}

func errPaymentExists() error {
	return apperr.NewConflict("payment_exists", "Another payment was already recorded under this key")
}

func errCreditNoteExists() error {
	return apperr.NewConflict("credit_note_exists", "The source document already has a different credit note")
}

func errPaymentChanged() error {
	return apperr.NewConflict("payment_changed", "The payment changed while it was being reversed; try again")
}

func errObligationReversed() error {
	return apperr.NewConflict("obligation_reversed", "The source document's obligation was reversed and cannot be recorded again")
}

func errObligationNotFound() error {
	return errField("obligation_not_found", "source_id", "not_found", "The source document has no obligation on this ledger")
}

func errObligationHasCredits() error {
	return apperr.NewConflict("obligation_has_credits",
		"A credit note, offset or adjustment settles this document; the document cannot be reversed while it does")
}
