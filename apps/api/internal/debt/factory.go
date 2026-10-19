package debt

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// TopicObligationCreated is published when a new obligation is recorded.
const TopicObligationCreated = "obligation.created"

// maxInstallments bounds a payment schedule.
const maxInstallments = 60

// ObligationInput is one source document that creates an obligation.
type ObligationInput struct {
	PartyID    uuid.UUID
	SourceType string
	SourceID   uuid.UUID
	// SourceDocNo is the number of the source document, shown in the ledger and on statements.
	SourceDocNo string
	Amount      money.Money
	// DueDate overrides the payment terms (business date + the party's terms days). Ignored when
	// Schedule is set: the last installment is then the due date.
	DueDate *time.Time
	// Schedule splits the amount into installments; their amounts add up to Amount and their due
	// dates do not go back in time.
	Schedule   []InstallmentInput
	CampaignID *uuid.UUID
	// EarmarkedInvoiceID applies the confirmed deposits of the party earmarked to that invoice to
	// the new receivable right away (allocation mode auto_earmarked).
	EarmarkedInvoiceID *uuid.UUID
}

// InstallmentInput is one part of a schedule.
type InstallmentInput struct {
	DueDate time.Time
	Amount  money.Money
}

// ObligationResult is what recording an obligation produced. Replayed is true when the source
// already had its obligation (same party and amount): nothing was written.
type ObligationResult struct {
	Obligation   Obligation
	Installments []Installment
	// Settlements are the earmarked deposits applied to the new receivable.
	Settlements []Settlement
	Replayed    bool
}

// ObligationFactory builds obligations and their schedules from a source document. It is pure:
// recording happens in recordObligation.
type ObligationFactory struct{}

// validSource reports the obligation sources each ledger accepts.
func validSource(direction, sourceType string) bool {
	switch sourceType {
	case SourceDebtAdjustment, SourceOpeningBalance, SourceRefundDue, SourceDebtOffset:
		return true
	case SourceSalesDocument:
		return direction == Receivable
	case SourceReceipt, SourceLandedCostAllocation, SourcePurchasePriceAdjustment:
		return direction == Payable
	}
	return false
}

// Build validates in and returns the obligation (without its row metadata) and installments for
// direction, dated businessDate, with termsDays as the default payment term.
func (ObligationFactory) Build(direction string, in ObligationInput, businessDate time.Time, termsDays int) (Obligation, []Installment, error) {
	fields := map[string]string{}
	if direction != Receivable && direction != Payable {
		fields["direction"] = "invalid"
	}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if !validSource(direction, in.SourceType) {
		fields["source_type"] = "invalid"
	}
	if in.SourceID == uuid.Nil {
		fields["source_id"] = "required"
	}
	if in.SourceDocNo == "" {
		fields["source_doc_no"] = "required"
	}
	if !in.Amount.IsPositive() {
		fields["amount"] = "must_be_positive"
	}
	if len(in.Schedule) > maxInstallments {
		fields["schedule"] = "too_many"
	}
	if in.EarmarkedInvoiceID != nil && direction != Receivable {
		fields["earmarked_invoice_id"] = "receivable_only"
	}
	due := businessDate.AddDate(0, 0, termsDays)
	if in.DueDate != nil {
		due = *in.DueDate
	}
	var inst []Installment
	if len(in.Schedule) > 0 && len(in.Schedule) <= maxInstallments {
		total := money.Money{}
		for i, s := range in.Schedule {
			if !s.Amount.IsPositive() {
				fields[fmt.Sprintf("schedule[%d].amount", i)] = "must_be_positive"
			}
			if s.DueDate.Before(businessDate) {
				fields[fmt.Sprintf("schedule[%d].due_date", i)] = "before_business_date"
			}
			if i > 0 && s.DueDate.Before(in.Schedule[i-1].DueDate) {
				fields[fmt.Sprintf("schedule[%d].due_date", i)] = "not_in_order"
			}
			total = total.Add(s.Amount)
			inst = append(inst, Installment{ID: ids.New(), InstallmentNo: i + 1, DueDate: s.DueDate, Amount: s.Amount})
		}
		if !total.Equal(in.Amount) {
			fields["schedule"] = "total_mismatch"
		}
		due = in.Schedule[len(in.Schedule)-1].DueDate
	}
	if due.Before(businessDate) && in.SourceType != SourceOpeningBalance {
		fields["due_date"] = "before_business_date"
	}
	if len(fields) > 0 {
		return Obligation{}, nil, errInvalid(fields)
	}
	ob := Obligation{
		ID: ids.New(), PartyID: in.PartyID, Direction: direction, SourceType: in.SourceType, SourceID: in.SourceID,
		SourceDocNo: in.SourceDocNo, BusinessDate: businessDate, DueDate: due, Amount: in.Amount,
		Status: ObligationOpen, CampaignID: in.CampaignID, Version: 1,
	}
	for i := range inst {
		inst[i].ObligationID = ob.ID
	}
	return ob, inst, nil
}

// recordObligation creates the obligation of a source once. A second call for the same source
// and direction with the same party and amount is an idempotent no-op; with other values it is a
// conflict (the source document changed after it was posted).
func recordObligation(ctx context.Context, w writeCtx, direction string, in ObligationInput) (ObligationResult, error) {
	if in.PartyID != uuid.Nil {
		if _, err := getParty(ctx, w.q, in.PartyID); err != nil {
			return ObligationResult{}, err
		}
	}
	terms := 0
	if in.DueDate == nil && len(in.Schedule) == 0 && in.PartyID != uuid.Nil {
		var err error
		if terms, err = termsDays(ctx, w.q, w.tenantID, in.PartyID, direction); err != nil {
			return ObligationResult{}, err
		}
	}
	ob, inst, err := ObligationFactory{}.Build(direction, in, w.date, terms)
	if err != nil {
		return ObligationResult{}, err
	}
	ob.TenantID, ob.CreatedAt, ob.UpdatedAt = w.tenantID, w.now, w.now
	res, err := w.q.ExecContext(ctx, `
		INSERT INTO obligations (id, tenant_id, party_id, direction, source_type, source_id, source_doc_no, business_date,
		                         due_date, amount, settled_amount, status, campaign_id, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 0, $11, $12, $13, $13, 1)
		ON CONFLICT ON CONSTRAINT ux_obligations__source_type_source_id_direction DO NOTHING`,
		ob.ID, ob.TenantID, ob.PartyID, ob.Direction, ob.SourceType, ob.SourceID, ob.SourceDocNo, ob.BusinessDate,
		ob.DueDate, ob.Amount, ob.Status, ob.CampaignID, ob.CreatedAt)
	if err != nil {
		return ObligationResult{}, fmt.Errorf("debt: insert obligation: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return ObligationResult{}, fmt.Errorf("debt: insert obligation: %w", err)
	} else if n == 0 {
		return existingObligation(ctx, w.q, direction, in)
	}
	for _, i := range inst {
		if _, err := w.q.ExecContext(ctx, `
			INSERT INTO obligation_schedules (id, tenant_id, obligation_id, installment_no, due_date, amount)
			VALUES ($1, $2, $3, $4, $5, $6)`, i.ID, w.tenantID, i.ObligationID, i.InstallmentNo, i.DueDate, i.Amount); err != nil {
			return ObligationResult{}, fmt.Errorf("debt: insert installment: %w", err)
		}
	}
	out := ObligationResult{Obligation: ob, Installments: inst}
	if in.EarmarkedInvoiceID != nil {
		if out.Settlements, err = applyEarmarked(ctx, w, &out.Obligation, *in.EarmarkedInvoiceID); err != nil {
			return ObligationResult{}, err
		}
	}
	return out, nil
}

func existingObligation(ctx context.Context, q db.Querier, direction string, in ObligationInput) (ObligationResult, error) {
	var ob Obligation
	if err := getInto(ctx, q, &ob, `SELECT `+obligationColumns+` FROM obligations o
		WHERE o.source_type = $1 AND o.source_id = $2 AND o.direction = $3`, in.SourceType, in.SourceID, direction); err != nil {
		return ObligationResult{}, fmt.Errorf("debt: read existing obligation: %w", err)
	}
	if ob.ReversedOn != nil {
		return ObligationResult{}, errObligationReversed()
	}
	if ob.PartyID != in.PartyID || !ob.Amount.Equal(in.Amount) {
		return ObligationResult{}, errObligationExists()
	}
	inst, err := installmentsOf(ctx, q, []uuid.UUID{ob.ID})
	if err != nil {
		return ObligationResult{}, err
	}
	return ObligationResult{Obligation: ob, Installments: inst[ob.ID], Replayed: true}, nil
}

// applyEarmarked applies the deposits of the party earmarked to invoiceID to ob (whose row the
// caller created in this transaction), oldest payment first: the confirmed deposits are settled
// now, then each one still waiting for verification gets an automatic line in its plan, applied
// when it is confirmed. The deposit row lock orders this against the deposit's confirmation, so
// the deposit reaches the receivable whichever of the two commits first (a confirmation that read
// the plan before it grew fails with payment_changed and is retried).
func applyEarmarked(ctx context.Context, w writeCtx, ob *Obligation, invoiceID uuid.UUID) ([]Settlement, error) {
	var payments []Payment
	if err := selectAll(ctx, w.q, &payments, `SELECT `+paymentColumns+` FROM payments p
		WHERE p.party_id = $1 AND p.direction = $2 AND p.earmarked_invoice_id = $3
		  AND (p.status = $4 AND p.allocated_amount < p.amount OR p.status = $5)
		ORDER BY p.id FOR UPDATE`, ob.PartyID, In, invoiceID, PaymentConfirmed, PaymentPending); err != nil {
		return nil, fmt.Errorf("debt: lock earmarked deposits: %w", err)
	}
	sortPaymentsByPosting(payments)
	var out []Settlement
	for i := range payments {
		p := &payments[i]
		if p.Status != PaymentConfirmed {
			continue
		}
		lines := Earmarked{}.Plan(p.Unallocated(), []Candidate{{Obligation: *ob, NextDue: ob.DueDate}})
		for _, l := range lines {
			s, err := settle(ctx, w, ob, SettledByPayment, p.ID, l.Amount, l.Mode)
			if err != nil {
				return nil, err
			}
			if err := moveAllocated(ctx, w, p, l.Amount); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
	}
	// Pending deposits plan only what the confirmed ones left open.
	var planned money.Money
	for i := range payments {
		if p := &payments[i]; p.Status == PaymentPending {
			var err error
			if planned, err = planEarmarked(ctx, w, p, ob, planned); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// planEarmarked adds to the plan of the pending deposit p an automatic line on ob for what is
// left of both, after the planned amount other pending deposits already cover; it returns the new
// planned total. The line is capped again by what is open when p is confirmed.
func planEarmarked(ctx context.Context, w writeCtx, p *Payment, ob *Obligation, planned money.Money) (money.Money, error) {
	budget := p.Amount
	for _, l := range p.Planned {
		budget = budget.Sub(l.Amount)
	}
	amount := minMoney(budget, ob.Open().Sub(planned))
	if !amount.IsPositive() {
		return planned, nil
	}
	plan := append(slices.Clone(p.Planned), Allocation{ObligationID: ob.ID, Amount: amount, Mode: ModeAutoEarmarked})
	if _, err := w.q.ExecContext(ctx, `
		UPDATE payments SET planned_allocations = $2, updated_at = $3, version = version + 1 WHERE id = $1`,
		p.ID, PlannedAllocations(plan), w.now); err != nil {
		return planned, fmt.Errorf("debt: plan earmarked deposit: %w", err)
	}
	p.Planned, p.UpdatedAt = plan, w.now
	p.Version++
	return planned.Add(amount), nil
}

// openCandidates lists the open obligations of a party on a ledger with the due date of their
// oldest unpaid installment.
func openCandidates(ctx context.Context, q db.Querier, partyID uuid.UUID, ledger string, forUpdate bool) ([]Candidate, error) {
	query := `SELECT ` + obligationColumns + `, ` + nextDueExpr + ` AS next_due FROM obligations o
		WHERE o.party_id = $1 AND o.direction = $2 AND o.status = 'open' ORDER BY o.id`
	if forUpdate {
		query += ` FOR UPDATE OF o`
	}
	var out []Candidate
	if err := selectAll(ctx, q, &out, query, partyID, ledger); err != nil {
		return nil, fmt.Errorf("debt: read open obligations: %w", err)
	}
	return out, nil
}

// nextDueExpr is the due date of the oldest installment of o not covered by its settled amount
// (installments are paid oldest first), or o's due date without a schedule.
const nextDueExpr = `COALESCE((
	SELECT MIN(i.due_date) FROM obligation_schedules i WHERE i.obligation_id = o.id
	  AND (SELECT SUM(j.amount) FROM obligation_schedules j
	       WHERE j.obligation_id = o.id AND j.installment_no <= i.installment_no) > o.settled_amount), o.due_date)`

func installmentsOf(ctx context.Context, q db.Querier, obligationIDs []uuid.UUID) (map[uuid.UUID][]Installment, error) {
	out := map[uuid.UUID][]Installment{}
	if len(obligationIDs) == 0 {
		return out, nil
	}
	var rows []Installment
	if err := selectAll(ctx, q, &rows, `SELECT id, obligation_id, installment_no, due_date, amount FROM obligation_schedules
		WHERE obligation_id = ANY($1) ORDER BY obligation_id, installment_no`, obligationIDs); err != nil {
		return nil, fmt.Errorf("debt: read installments: %w", err)
	}
	for _, r := range rows {
		out[r.ObligationID] = append(out[r.ObligationID], r)
	}
	return out, nil
}

func obligationCreated(ob Obligation) outbox.Event {
	return outbox.Event{Topic: TopicObligationCreated, Payload: map[string]any{
		"obligation_id": ob.ID, "party_id": ob.PartyID, "direction": ob.Direction, "source_type": ob.SourceType,
		"source_id": ob.SourceID, "amount": ob.Amount, "due_date": ob.DueDate.Format(time.DateOnly),
	}}
}
