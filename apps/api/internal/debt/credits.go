package debt

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// TopicCreditNoteRecorded is published when a credit note is recorded.
const TopicCreditNoteRecorded = "credit_note.recorded" //nolint:gosec // an event topic, not a credential

// CreditNoteInput is a reduction of what a party owes (sales return, post-sale discount) or of
// what the store owes it (purchase return, purchase price adjustment).
type CreditNoteInput struct {
	PartyID uuid.UUID
	// Direction is the ledger the credit reduces: receivable for sales sources, payable for
	// purchase sources.
	Direction   string
	SourceType  string
	SourceID    uuid.UUID
	SourceDocNo string
	Amount      money.Money
	ReasonID    *uuid.UUID
	// Targets are the documents the credit reduces first, in order (the original sale of a
	// return or exchange). Without OnlyTargets the rest goes to the other open documents of the
	// party on the ledger, oldest due first, and only then becomes a refund due.
	Targets     []uuid.UUID
	OnlyTargets bool
}

// CreditNoteResult is a recorded credit note: the settlements it made and, when the credit was
// more than the party owed, the refund due created in the opposite ledger (the store now owes
// the customer, or the supplier owes the store).
type CreditNoteResult struct {
	Note        CreditNote
	Settlements []Settlement
	RefundDue   *Obligation
	Replayed    bool
}

func creditLedger(sourceType string) string {
	switch sourceType {
	case CreditSalesReturn, CreditPostSaleDiscount:
		return Receivable
	case CreditPurchaseReturn, CreditPurchasePriceAdjustment:
		return Payable
	}
	return ""
}

func checkCreditInput(in CreditNoteInput) error {
	fields := map[string]string{}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	ledger := creditLedger(in.SourceType)
	if ledger == "" {
		fields["source_type"] = "invalid"
	} else if in.Direction != ledger {
		fields["direction"] = "invalid"
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
	if len(in.Targets) != len(uniqueIDs(in.Targets...)) {
		fields["targets"] = "duplicate"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// recordCreditNote records a credit note once per source. It locks the targets and the open
// obligations of the party on the ledger (one statement, id order), settles the targets first,
// then the rest oldest due first unless OnlyTargets, and turns what is left into a refund due.
func recordCreditNote(ctx context.Context, w writeCtx, in CreditNoteInput) (CreditNoteResult, error) {
	if err := checkCreditInput(in); err != nil {
		return CreditNoteResult{}, err
	}
	if _, err := getParty(ctx, w.q, in.PartyID); err != nil {
		return CreditNoteResult{}, err
	}
	n := CreditNote{
		ID: ids.New(), TenantID: w.tenantID, PartyID: in.PartyID, Direction: in.Direction, SourceType: in.SourceType,
		SourceID: in.SourceID, SourceDocNo: in.SourceDocNo, Amount: in.Amount, BusinessDate: w.date, ReasonID: in.ReasonID,
		PostedAt: w.now, PostedBy: w.by, CreatedAt: w.now, Version: 1,
	}
	res, err := w.q.ExecContext(ctx, `
		INSERT INTO credit_notes (id, tenant_id, party_id, direction, source_type, source_id, source_doc_no, amount,
		                          allocated_amount, business_date, reason_id, posted_at, posted_by, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, $10, $11, $12, $11, $11, 1)
		ON CONFLICT ON CONSTRAINT ux_credit_notes__source_type_source_id DO NOTHING`,
		n.ID, n.TenantID, n.PartyID, n.Direction, n.SourceType, n.SourceID, n.SourceDocNo, n.Amount, n.BusinessDate,
		n.ReasonID, n.PostedAt, n.PostedBy)
	if err != nil {
		return CreditNoteResult{}, fmt.Errorf("debt: insert credit note: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil {
		return CreditNoteResult{}, fmt.Errorf("debt: insert credit note: %w", err)
	} else if rows == 0 {
		return existingCreditNote(ctx, w, in)
	}

	var locked []Candidate
	if err := selectAll(ctx, w.q, &locked, `SELECT `+obligationColumns+`, `+nextDueExpr+` AS next_due FROM obligations o
		WHERE o.id = ANY($3) OR (o.party_id = $1 AND o.direction = $2 AND o.status = 'open')
		ORDER BY o.id FOR UPDATE OF o`, in.PartyID, in.Direction, uniqueIDs(in.Targets...)); err != nil {
		return CreditNoteResult{}, fmt.Errorf("debt: lock credit documents: %w", err)
	}
	byID := map[uuid.UUID]*Obligation{}
	for i := range locked {
		byID[locked[i].ID] = &locked[i].Obligation
	}
	left := in.Amount
	var lines []Allocation
	for i, id := range in.Targets {
		ob, ok := byID[id]
		switch {
		case !ok:
			return CreditNoteResult{}, errField("obligation_not_found", fmt.Sprintf("targets[%d]", i), "not_found", "The document does not exist")
		case ob.PartyID != in.PartyID:
			return CreditNoteResult{}, errField("allocation_party_mismatch", fmt.Sprintf("targets[%d]", i), "other_party",
				"The document belongs to another party")
		case ob.Direction != in.Direction:
			return CreditNoteResult{}, errField("allocation_direction_mismatch", fmt.Sprintf("targets[%d]", i), "other_direction",
				"The document is on the other ledger (receivable/payable)")
		}
		if amount := minMoney(left, ob.Open()); amount.IsPositive() {
			lines = append(lines, Allocation{ObligationID: id, Amount: amount, Mode: ModeManual})
			left = left.Sub(amount)
		}
	}
	if !in.OnlyTargets && left.IsPositive() {
		targeted := map[uuid.UUID]bool{}
		for _, id := range in.Targets {
			targeted[id] = true
		}
		var rest []Candidate
		for _, c := range locked {
			if !targeted[c.ID] && c.PartyID == in.PartyID && c.Direction == in.Direction && c.Status == ObligationOpen {
				rest = append(rest, c)
			}
		}
		for _, l := range (OldestDueFirst{}).Plan(left, rest) {
			lines = append(lines, l)
			left = left.Sub(l.Amount)
		}
	}
	out := CreditNoteResult{Note: n}
	for _, l := range lines {
		s, err := settle(ctx, w, byID[l.ObligationID], SettledByCreditNote, n.ID, l.Amount, l.Mode)
		if err != nil {
			return CreditNoteResult{}, err
		}
		out.Settlements = append(out.Settlements, s)
	}
	allocated := in.Amount.Sub(left)
	if allocated.IsPositive() {
		if _, err := w.q.ExecContext(ctx, `
			UPDATE credit_notes SET allocated_amount = $2, updated_at = $3, version = version + 1 WHERE id = $1`,
			n.ID, allocated, w.now); err != nil {
			return CreditNoteResult{}, fmt.Errorf("debt: move credit note projection: %w", err)
		}
		out.Note.AllocatedAmount = allocated
		out.Note.Version++
	}
	if left.IsPositive() {
		due := w.date
		r, err := recordObligation(ctx, w, Opposite(in.Direction), ObligationInput{
			PartyID: in.PartyID, SourceType: SourceRefundDue, SourceID: n.ID, SourceDocNo: in.SourceDocNo, Amount: left, DueDate: &due,
		})
		if err != nil {
			return CreditNoteResult{}, err
		}
		out.RefundDue = &r.Obligation
	}
	return out, nil
}

func existingCreditNote(ctx context.Context, w writeCtx, in CreditNoteInput) (CreditNoteResult, error) {
	var n CreditNote
	if err := getInto(ctx, w.q, &n, `SELECT `+creditNoteColumns+` FROM credit_notes c WHERE c.source_type = $1 AND c.source_id = $2`,
		in.SourceType, in.SourceID); err != nil {
		return CreditNoteResult{}, fmt.Errorf("debt: read existing credit note: %w", err)
	}
	if n.PartyID != in.PartyID || n.Direction != in.Direction || !n.Amount.Equal(in.Amount) {
		return CreditNoteResult{}, errCreditNoteExists()
	}
	out := CreditNoteResult{Note: n, Replayed: true}
	if err := selectAll(ctx, w.q, &out.Settlements, `SELECT `+settlementColumns+` FROM obligation_settlements s
		WHERE s.settled_by_type = $1 AND s.settled_by_id = $2 ORDER BY s.created_at, s.id`, SettledByCreditNote, n.ID); err != nil {
		return CreditNoteResult{}, fmt.Errorf("debt: read credit note settlements: %w", err)
	}
	var due []Obligation
	if err := selectAll(ctx, w.q, &due, `SELECT `+obligationColumns+` FROM obligations o WHERE o.source_type = $1 AND o.source_id = $2`,
		SourceRefundDue, n.ID); err != nil {
		return CreditNoteResult{}, fmt.Errorf("debt: read refund due: %w", err)
	}
	if len(due) > 0 {
		out.RefundDue = &due[0]
	}
	return out, nil
}

func creditNoteEvent(r CreditNoteResult) outbox.Event {
	payload := map[string]any{
		"credit_note_id": r.Note.ID, "party_id": r.Note.PartyID, "direction": r.Note.Direction,
		"source_type": r.Note.SourceType, "source_id": r.Note.SourceID, "amount": r.Note.Amount,
		"allocated_amount": r.Note.AllocatedAmount,
	}
	if r.RefundDue != nil {
		payload["refund_due_obligation_id"] = r.RefundDue.ID
	}
	return outbox.Event{Topic: TopicCreditNoteRecorded, Payload: payload}
}
