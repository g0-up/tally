package debt

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// TopicObligationReversed is published when the obligation of a reversed source is closed.
const TopicObligationReversed = "obligation.reversed"

// maxReversalReason bounds the reason kept on a reversed obligation.
const maxReversalReason = 500

// SourceRef names the obligation of a source document on one ledger.
type SourceRef struct {
	// Direction is the ledger: Receivable or Payable.
	Direction  string
	SourceType string
	SourceID   uuid.UUID
}

// ObligationReversal is what reversing the obligation of a source produced. Replayed is true
// when it was already reversed: nothing was written.
type ObligationReversal struct {
	Obligation Obligation
	// Released are the reversal rows of the payment settlements taken off the obligation; the
	// money of those payments is the party's advance again.
	Released []Settlement
	// Closing is the obligation_reversal settlement that closed the whole amount.
	Closing  Settlement
	Replayed bool
}

// ObligationReverser closes the obligation of a source document that its domain reverses (a
// receipt reversed by procurement, a stock-out reversed by sales). Like the recorders it runs in
// the caller's posting transaction and never commits.
type ObligationReverser struct{ s *Service }

// Obligations returns the reverser of obligations.
func (s *Service) Obligations() ObligationReverser { return ObligationReverser{s: s} }

// ReverseBySource reverses the obligation ref names, dated tx.BusinessDate() and numbered by
// tx.DocNo() (the reversing document, when the posting issued one). The ledger stays append-only:
// every payment settlement still standing on the obligation gets its negative mirror, so that
// money becomes the party's advance (allocate or refund it like any advance; an earmarked deposit
// reaches the invoice again when it gets a new obligation), then one obligation_reversal
// settlement closes the whole amount and the obligation records the date, reason and number.
// Nothing is deleted, so statements of earlier periods keep the document.
//
// It fails with 422 validation_failed for a ledger or source type a source document cannot
// reverse (only sales_document receivables and receipt, landed_cost_allocation and
// purchase_price_adjustment payables) or an empty reason, 422 obligation_not_found when the
// source has no obligation on the ledger, 422 reversal_before_document when tx is dated before
// the obligation, and 409 obligation_has_credits while a credit note, offset or adjustment
// settles it (those are not reversible, so the document is not either). A second call for a
// reversed obligation returns it with Replayed set. Recording the source again afterwards fails
// with 409 obligation_reversed.
//
// Lock order: the obligation row, then its payments in id order, are locked inside the call. A
// caller should not declare those payments in its LockKeys; declaring the obligation
// (posting.LockObligations) is fine.
func (r ObligationReverser) ReverseBySource(ctx context.Context, tx posting.Tx, ref SourceRef, reason string) (ObligationReversal, error) {
	res, before, err := reverseObligation(ctx, fromTx(tx), ref, reason, tx.DocNo())
	if err != nil || res.Replayed {
		return res, err
	}
	tx.Audit(audit.Event{Action: "obligation.reverse", EntityType: "obligation", EntityID: res.Obligation.ID,
		DocNo: res.Obligation.SourceDocNo, Before: before,
		After: map[string]any{"obligation": res.Obligation, "released": res.Released, "closing": res.Closing}})
	tx.Publish(obligationReversed(res))
	return res, nil
}

func checkSourceRef(ref SourceRef, reason string) error {
	fields := map[string]string{}
	switch ref.SourceType {
	case SourceSalesDocument, SourceReceipt, SourceLandedCostAllocation, SourcePurchasePriceAdjustment:
		if !validSource(ref.Direction, ref.SourceType) {
			fields["direction"] = "invalid"
		}
	default:
		fields["source_type"] = "not_reversible"
	}
	if ref.SourceID == uuid.Nil {
		fields["source_id"] = "required"
	}
	if strings.TrimSpace(reason) == "" {
		fields["reason"] = "required"
	} else if utf8.RuneCountInString(reason) > maxReversalReason {
		fields["reason"] = "too_long"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// reverseObligation does the work of ReverseBySource and also returns the obligation as it was.
func reverseObligation(ctx context.Context, w writeCtx, ref SourceRef, reason, docNo string) (ObligationReversal, Obligation, error) {
	if err := checkSourceRef(ref, reason); err != nil {
		return ObligationReversal{}, Obligation{}, err
	}
	var ob Obligation
	err := getInto(ctx, w.q, &ob, `SELECT `+obligationColumns+` FROM obligations o
		WHERE o.source_type = $1 AND o.source_id = $2 AND o.direction = $3 FOR UPDATE`, ref.SourceType, ref.SourceID, ref.Direction)
	if db.IsNoRows(err) {
		return ObligationReversal{}, Obligation{}, errObligationNotFound()
	}
	if err != nil {
		return ObligationReversal{}, Obligation{}, fmt.Errorf("debt: lock obligation: %w", err)
	}
	if ob.ReversedOn != nil {
		return ObligationReversal{Obligation: ob, Replayed: true}, ob, nil
	}
	before := ob
	if w.date.Before(ob.BusinessDate) {
		return ObligationReversal{}, Obligation{}, errField("reversal_before_document", "business_date", "before_document",
			"The reversal is dated before the document it reverses")
	}
	standing, err := obligationSettlements(ctx, w.q, ob.ID)
	if err != nil {
		return ObligationReversal{}, Obligation{}, err
	}
	paymentIDs := make([]uuid.UUID, 0, len(standing))
	for _, s := range standing {
		if s.SettledByType != SettledByPayment {
			return ObligationReversal{}, Obligation{}, errObligationHasCredits()
		}
		paymentIDs = append(paymentIDs, s.SettledByID)
	}
	payments, err := lockPayments(ctx, w.q, uniqueIDs(paymentIDs...))
	if err != nil {
		return ObligationReversal{}, Obligation{}, err
	}
	out := ObligationReversal{}
	for _, s := range standing {
		p := payments[s.SettledByID]
		if p == nil {
			return ObligationReversal{}, Obligation{}, fmt.Errorf("debt: settlement %s names a missing payment %s", s.ID, s.SettledByID)
		}
		mirror, err := reverseSettlement(ctx, w, &ob, s)
		if err != nil {
			return ObligationReversal{}, Obligation{}, err
		}
		if err := moveAllocated(ctx, w, p, s.Amount.Neg()); err != nil {
			return ObligationReversal{}, Obligation{}, err
		}
		out.Released = append(out.Released, mirror)
	}
	if out.Closing, err = settle(ctx, w, &ob, SettledByReversal, ob.ID, ob.Open(), ModeManual); err != nil {
		return ObligationReversal{}, Obligation{}, err
	}
	day := w.date
	var number *string
	if docNo != "" {
		number = &docNo
	}
	if _, err := w.q.ExecContext(ctx, `
		UPDATE obligations SET reversed_on = $2, reversal_reason = $3, reversal_doc_no = $4, updated_at = $5, version = version + 1
		WHERE id = $1`, ob.ID, day, reason, number, w.now); err != nil {
		return ObligationReversal{}, Obligation{}, fmt.Errorf("debt: mark obligation reversed: %w", err)
	}
	ob.ReversedOn, ob.ReversalReason, ob.ReversalDocNo, ob.UpdatedAt = &day, &reason, number, w.now
	ob.Version++
	out.Obligation = ob
	return out, before, nil
}

// obligationSettlements are the settlements of an obligation that were not reversed yet.
func obligationSettlements(ctx context.Context, q db.Querier, obligationID uuid.UUID) ([]Settlement, error) {
	var out []Settlement
	if err := selectAll(ctx, q, &out, `SELECT `+settlementColumns+` FROM obligation_settlements s
		WHERE s.obligation_id = $1 AND s.reversal_of IS NULL
		  AND NOT EXISTS (SELECT 1 FROM obligation_settlements r WHERE r.reversal_of = s.id)
		ORDER BY s.created_at, s.id`, obligationID); err != nil {
		return nil, fmt.Errorf("debt: read obligation settlements: %w", err)
	}
	return out, nil
}

// lockPayments locks the payments in id order and returns them by id.
func lockPayments(ctx context.Context, q db.Querier, idList []uuid.UUID) (map[uuid.UUID]*Payment, error) {
	out := map[uuid.UUID]*Payment{}
	if len(idList) == 0 {
		return out, nil
	}
	var rows []Payment
	if err := selectAll(ctx, q, &rows, `SELECT `+paymentColumns+` FROM payments p WHERE p.id = ANY($1) ORDER BY p.id FOR UPDATE`,
		idList); err != nil {
		return nil, fmt.Errorf("debt: lock payments: %w", err)
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

func obligationReversed(r ObligationReversal) outbox.Event {
	released := make([]map[string]any, 0, len(r.Released))
	for _, s := range r.Released {
		released = append(released, map[string]any{"payment_id": s.SettledByID, "amount": s.Amount.Neg()})
	}
	ob := r.Obligation
	return outbox.Event{Topic: TopicObligationReversed, Payload: map[string]any{
		"obligation_id": ob.ID, "party_id": ob.PartyID, "direction": ob.Direction, "source_type": ob.SourceType,
		"source_id": ob.SourceID, "amount": ob.Amount, "released_payments": released,
		"reversal_doc_no": ob.ReversalDocNo, "reversed_on": dateString(ob.ReversedOn),
	}}
}
