package debt

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Allocation applies part of a payment (or credit) to one obligation. Mode records how the line
// was chosen: typed by the user (manual), accepted from the oldest-due suggestion, or applied
// automatically from a deposit earmarked to the invoice.
type Allocation struct {
	ObligationID uuid.UUID   `json:"obligation_id"`
	Amount       money.Money `json:"amount"`
	Mode         string      `json:"mode"`
}

// PlannedAllocations is the jsonb list of allocations a payment waiting for verification will
// apply when it is confirmed.
type PlannedAllocations []Allocation

// Value stores the plan as JSON; an empty plan is NULL.
func (p PlannedAllocations) Value() (driver.Value, error) {
	if len(p) == 0 {
		return nil, nil
	}
	b, err := json.Marshal([]Allocation(p))
	if err != nil {
		return nil, fmt.Errorf("debt: encode planned allocations: %w", err)
	}
	return b, nil
}

// Scan reads the jsonb column.
func (p *PlannedAllocations) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		*p = nil
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("debt: cannot scan %T into planned allocations", src)
	}
	var out []Allocation
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("debt: decode planned allocations: %w", err)
	}
	*p = out
	return nil
}

// Candidate is an open obligation offered to an allocation strategy, with the due date of its
// oldest unpaid installment (the obligation's due date when it has no schedule).
type Candidate struct {
	Obligation
	NextDue time.Time `db:"next_due"`
}

// AllocationStrategy chooses how much of budget goes to which open obligation. A strategy only
// proposes lines; checkAllocations guards every line before anything is written.
type AllocationStrategy interface {
	Plan(budget money.Money, open []Candidate) []Allocation
}

// OldestDueFirst fills the obligations whose oldest unpaid installment is due first (then the
// oldest document); it is the suggestion the owner confirms on the Công nợ screen.
type OldestDueFirst struct{}

// Plan implements AllocationStrategy.
func (OldestDueFirst) Plan(budget money.Money, open []Candidate) []Allocation {
	return fillOldestFirst(budget, open, ModeSuggested)
}

// Earmarked applies a deposit reserved for an invoice to that invoice's documents, oldest first.
type Earmarked struct{}

// Plan implements AllocationStrategy.
func (Earmarked) Plan(budget money.Money, open []Candidate) []Allocation {
	return fillOldestFirst(budget, open, ModeAutoEarmarked)
}

// Manual returns the lines the user typed, defaulting their mode to manual.
type Manual struct {
	Lines []Allocation
}

// Plan implements AllocationStrategy; the budget and candidates are checked afterwards.
func (m Manual) Plan(money.Money, []Candidate) []Allocation {
	out := make([]Allocation, len(m.Lines))
	for i, l := range m.Lines {
		if l.Mode == "" {
			l.Mode = ModeManual
		}
		out[i] = l
	}
	return out
}

func fillOldestFirst(budget money.Money, open []Candidate, mode string) []Allocation {
	sorted := append([]Candidate(nil), open...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if !a.NextDue.Equal(b.NextDue) {
			return a.NextDue.Before(b.NextDue)
		}
		if !a.BusinessDate.Equal(b.BusinessDate) {
			return a.BusinessDate.Before(b.BusinessDate)
		}
		return a.ID.String() < b.ID.String()
	})
	var out []Allocation
	left := budget
	for _, c := range sorted {
		if !left.IsPositive() {
			break
		}
		amount := minMoney(left, c.Open())
		if !amount.IsPositive() {
			continue
		}
		out = append(out, Allocation{ObligationID: c.ID, Amount: amount, Mode: mode})
		left = left.Sub(amount)
	}
	return out
}

// minMoney is the smaller of a and b (the money package has no Min).
func minMoney(a, b money.Money) money.Money {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func validMode(mode string) bool {
	return mode == ModeManual || mode == ModeSuggested || mode == ModeAutoEarmarked
}

// checkLines checks the allocation lines on their own: an obligation, a positive amount, a known
// mode and each obligation once.
func checkLines(lines []Allocation) error {
	fields := map[string]string{}
	seen := map[uuid.UUID]bool{}
	for i, l := range lines {
		switch {
		case l.ObligationID == uuid.Nil:
			fields[lineField(i, "obligation_id")] = "required"
		case seen[l.ObligationID]:
			fields[lineField(i, "obligation_id")] = "duplicate"
		}
		seen[l.ObligationID] = true
		if !l.Amount.IsPositive() {
			fields[lineField(i, "amount")] = "must_be_positive"
		}
		if !validMode(l.Mode) {
			fields[lineField(i, "mode")] = "invalid"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// checkAllocations guards lines against the locked obligations: every document belongs to the
// party and to the ledger, none gets more than is open on it, and together they stay within
// budget (the unallocated part of the payment or credit).
func checkAllocations(lines []Allocation, partyID uuid.UUID, ledger string, budget money.Money, obs map[uuid.UUID]*Obligation) error {
	if err := checkLines(lines); err != nil {
		return err
	}
	total := money.Money{}
	for i, l := range lines {
		ob, ok := obs[l.ObligationID]
		if !ok {
			return errField("obligation_not_found", lineField(i, "obligation_id"), "not_found", "The document does not exist")
		}
		if ob.PartyID != partyID {
			return errPartyMismatch(i)
		}
		if ob.Direction != ledger {
			return errDirectionMismatch(i)
		}
		if !ob.Open().IsPositive() {
			return errObligationSettled(lineField(i, "obligation_id"))
		}
		if l.Amount.Cmp(ob.Open()) > 0 {
			return errExceedsOpen(i)
		}
		total = total.Add(l.Amount)
	}
	if total.Cmp(budget) > 0 {
		return errExceedsPayment()
	}
	return nil
}

func allocationIDs(lines []Allocation) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.ObligationID)
	}
	return uniqueIDs(out...)
}

func sumAllocations(lines []Allocation) money.Money {
	total := money.Money{}
	for _, l := range lines {
		total = total.Add(l.Amount)
	}
	return total
}
