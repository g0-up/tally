package procurement

import (
	"slices"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// PurchaseOrderStatus is the state machine of a PO. A draft has no effect on the ledger and is
// confirmed without approval; receipts move a confirmed order through partially_received to
// completed, and an order still waiting for goods may be closed short with a reason. Reversing a
// receipt moves the order back to what its remaining receipts justify. closed_short and completed
// orders take no further receipts.
type PurchaseOrderStatus struct{}

var orderTransitions = map[string][]string{
	StatusDraft:             {StatusConfirmed},
	StatusConfirmed:         {StatusPartiallyReceived, StatusCompleted, StatusClosedShort},
	StatusPartiallyReceived: {StatusConfirmed, StatusCompleted, StatusClosedShort},
	// A receipt reversal can reopen a completed order.
	StatusCompleted:   {StatusConfirmed, StatusPartiallyReceived},
	StatusClosedShort: {},
}

// CanTransition reports whether an order may go from one status to another.
func (PurchaseOrderStatus) CanTransition(from, to string) bool {
	return slices.Contains(orderTransitions[from], to)
}

// Transition checks a move between statuses and returns 409 order_status_invalid otherwise.
// Staying in a receiving status is no move (a batch that does not finish the order).
func (m PurchaseOrderStatus) Transition(from, to string) error {
	if from == to && from != StatusDraft && from != StatusClosedShort {
		return nil
	}
	if !m.CanTransition(from, to) {
		return apperr.NewConflict(CodeOrderStatus, "A "+from+" purchase order cannot become "+to)
	}
	return nil
}

// Receivable reports whether goods may still be received against an order in the status.
func (PurchaseOrderStatus) Receivable(status string) bool {
	return status == StatusConfirmed || status == StatusPartiallyReceived
}

// Progress is the status the received quantities of the lines justify: completed once every line
// received at least what was ordered, partially_received once anything arrived, else confirmed.
func (PurchaseOrderStatus) Progress(lines []PurchaseOrderLine) string {
	some, all := false, len(lines) > 0
	for _, l := range lines {
		if l.ReceivedBaseQty.IsPositive() {
			some = true
		}
		if l.ReceivedBaseQty.Cmp(l.BaseQty) < 0 {
			all = false
		}
	}
	switch {
	case all:
		return StatusCompleted
	case some:
		return StatusPartiallyReceived
	}
	return StatusConfirmed
}

// AfterReceipts is the status of an order after its received quantities changed: a closed-short
// order stays closed (its receipts are kept as they are), any other order follows Progress.
func (m PurchaseOrderStatus) AfterReceipts(current string, lines []PurchaseOrderLine) string {
	if current == StatusClosedShort {
		return current
	}
	return m.Progress(lines)
}

// Follow is AfterReceipts checked as a move. Keeping the status is no move: a closed-short order
// whose receipt is reversed stays closed short with less received.
func (m PurchaseOrderStatus) Follow(current string, lines []PurchaseOrderLine) (string, error) {
	next := m.AfterReceipts(current, lines)
	if next == current {
		return next, nil
	}
	return next, m.Transition(current, next)
}
