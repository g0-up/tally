package kits

import (
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Assembly order statuses. Planning consumes nothing; reserving holds the components; each batch
// posted moves the order to partially completed or completed; a disassembly that takes kits of a
// finished order back below its plan puts it in progress again so it can be assembled anew.
const (
	OrderDraft              = "draft"
	OrderReserved           = "reserved"
	OrderInProgress         = "in_progress"
	OrderPartiallyCompleted = "partially_completed"
	OrderCompleted          = "completed"
	OrderClosed             = "closed"
)

// AssemblyOrderStatus is the transition table of assembly orders.
type AssemblyOrderStatus struct{}

var orderTransitions = map[string][]string{
	OrderDraft:              {OrderReserved, OrderPartiallyCompleted, OrderCompleted, OrderClosed},
	OrderReserved:           {OrderPartiallyCompleted, OrderCompleted, OrderClosed},
	OrderInProgress:         {OrderPartiallyCompleted, OrderCompleted, OrderClosed},
	OrderPartiallyCompleted: {OrderInProgress, OrderPartiallyCompleted, OrderCompleted, OrderClosed},
	OrderCompleted:          {OrderInProgress, OrderClosed},
	OrderClosed:             {},
}

// CanMove reports whether an order may move from one status to another.
func (AssemblyOrderStatus) CanMove(from, to string) bool {
	for _, s := range orderTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Move returns to, or 409 assembly_order_status_invalid when the table forbids it.
func (st AssemblyOrderStatus) Move(from, to string) (string, error) {
	if from == to && from != OrderClosed {
		return to, nil
	}
	if !st.CanMove(from, to) {
		return from, apperr.NewConflict(CodeOrderStatus, "The assembly order cannot move from "+from+" to "+to)
	}
	return to, nil
}

// Open reports whether kits may still be assembled on an order in status s.
func (AssemblyOrderStatus) Open(s string) bool {
	return s != OrderClosed && s != OrderCompleted
}

// AfterProgress is the status an order in status from takes once its net kits (completed less
// disassembled) are net out of planned: completed at or above the plan, partially completed
// below it after a batch, in progress below it after a disassembly. A closed order stays closed.
func (AssemblyOrderStatus) AfterProgress(from string, net, planned money.Qty, disassembly bool) string {
	switch {
	case from == OrderClosed:
		return OrderClosed
	case net.Cmp(planned) >= 0:
		return OrderCompleted
	case disassembly:
		if from == OrderCompleted || from == OrderPartiallyCompleted {
			return OrderInProgress
		}
		return from
	default:
		return OrderPartiallyCompleted
	}
}
