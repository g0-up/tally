package debt

import (
	"context"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// The recorders are how procurement and sales write to the debt ledger from inside their own
// posting (posting.Coordinator): every method runs in the caller's transaction, on its business
// date and actor, and adds its own audit rows and outbox events to it. They never commit, so the
// caller's document and its debt rows are written together or not at all.
//
// Lock order: the caller's LockKeys should include the debt rows a call will settle
// (PaymentInput.AddLocks); rows not declared there are locked in id order when they are reached,
// and a deadlock between two postings is retried once by the coordinator. Document numbers come
// before every row lock, so a caller recording payments lists their prefixes in its
// ExtraDocPrefixes (posting.ExtraDocs, one PaymentInput.DocPrefix per payment) and the recorder
// takes the number the coordinator issued.

// ReceivableRecorder records what a party owes the store (a sales document).
type ReceivableRecorder struct{ s *Service }

// Record creates the receivable of a source document once (see PayableRecorder.Record).
func (r ReceivableRecorder) Record(ctx context.Context, tx posting.Tx, in ObligationInput) (ObligationResult, error) {
	return r.s.recordObligationIn(ctx, tx, Receivable, in)
}

// PayableRecorder records what the store owes a party (a receipt, landed cost, purchase price
// adjustment).
type PayableRecorder struct{ s *Service }

// Record creates the payable of a source document once. The due date is in.DueDate, else the
// last installment of in.Schedule, else the business date plus the party's payment terms. A
// second call for the same source with the same party and amount returns the first obligation
// with Replayed set and writes nothing; with other values it fails with 409 obligation_exists,
// and once the obligation was reversed (ObligationReverser) with 409 obligation_reversed.
func (r PayableRecorder) Record(ctx context.Context, tx posting.Tx, in ObligationInput) (ObligationResult, error) {
	return r.s.recordObligationIn(ctx, tx, Payable, in)
}

func (s *Service) recordObligationIn(ctx context.Context, tx posting.Tx, direction string, in ObligationInput) (ObligationResult, error) {
	r, err := recordObligation(ctx, fromTx(tx), direction, in)
	if err != nil || r.Replayed {
		return r, err
	}
	tx.Audit(audit.Event{Action: "obligation.record", EntityType: "obligation", EntityID: r.Obligation.ID,
		DocNo: r.Obligation.SourceDocNo, After: map[string]any{"obligation": r.Obligation, "installments": r.Installments}})
	tx.Publish(obligationCreated(r.Obligation))
	return r, nil
}

// PaymentRecorder records the money a document takes or pays out (POS tender, invoice deposit,
// supplier payment on a receipt, refund of a return).
type PaymentRecorder struct{ s *Service }

// Record writes one PT/PC under the number the coordinator reserved for it: the caller's command
// lists in.DocPrefix() in its ExtraDocPrefixes once per payment it records (posting.ExtraDocs), and
// a call without a reserved number fails. Confirmed payments settle their allocations (checked
// against what is open, 422 allocation_exceeds_open / allocation_exceeds_payment) and write their
// cash entry; a payment in through a method that requires verification is pending_verification
// and moves nothing until it is confirmed.
//
// With in.Key set, a second call with the same key and the same payment (direction, party,
// purpose, method, amount, source, earmark, refunded payment and, while they are still the
// recorded ones, allocations) returns the first payment with Replayed set and takes no number, so
// its posting must not reserve one; a different payment under the key fails with 409
// payment_exists, and a key whose payment was reversed with 409 payment_reversed.
func (r PaymentRecorder) Record(ctx context.Context, tx posting.Tx, in PaymentInput) (PaymentResult, error) {
	w := fromTx(tx)
	if err := checkPaymentInput(in); err != nil {
		return PaymentResult{}, err
	}
	if in.Key != "" {
		prior, err := paymentByKey(ctx, w, in.Key)
		if err != nil {
			return PaymentResult{}, err
		}
		if prior != nil {
			return replayPayment(*prior, in)
		}
	} else {
		in.Key = "payment:" + ids.New().String()
	}
	docNo, err := posting.TakeDocNo(tx, in.DocPrefix())
	if err != nil {
		return PaymentResult{}, err
	}
	res, err := recordPayment(ctx, w, r.s.paymentDeps(), in, docNo, in.Key)
	if err != nil {
		return PaymentResult{}, err
	}
	publishPayment(tx, ScopePaymentPost, res)
	return res, nil
}

// DocPrefix is the prefix of the payment's number (PT in, PC out), which the recording posting
// lists in its ExtraDocPrefixes.
func (in PaymentInput) DocPrefix() string { return paymentPrefix(in.Direction) }

// AddLocks declares the debt rows recording in will lock: the obligations it allocates to and
// the payment whose advance it refunds.
func (in PaymentInput) AddLocks(set *posting.LockSet) {
	set.Add(posting.LockObligations, allocationIDs(in.Allocations)...)
	if in.RefundOf != nil {
		set.Add(posting.LockPayments, *in.RefundOf)
	}
}

// CreditNoteRecorder records a reduction of what a party owes or is owed (sales return,
// post-sale discount, purchase return, purchase price adjustment).
type CreditNoteRecorder struct{ s *Service }

// Record writes the credit note of a source once: it settles in.Targets first, then (unless
// OnlyTargets) the other open documents of the party on the ledger oldest due first, and turns
// what is left into a refund_due obligation on the opposite ledger, which a refund (PostRefund or
// a PaymentRecorder refund allocated to it) pays out. A second call for the same source with the
// same values returns the first note with Replayed set; other values give 409 credit_note_exists.
func (r CreditNoteRecorder) Record(ctx context.Context, tx posting.Tx, in CreditNoteInput) (CreditNoteResult, error) {
	res, err := recordCreditNote(ctx, fromTx(tx), in)
	if err != nil || res.Replayed {
		return res, err
	}
	tx.Audit(audit.Event{Action: "credit_note.record", EntityType: "credit_note", EntityID: res.Note.ID,
		DocNo: res.Note.SourceDocNo, After: res.Note})
	tx.Publish(creditNoteEvent(res))
	if res.RefundDue != nil {
		tx.Publish(obligationCreated(*res.RefundDue))
	}
	return res, nil
}

// AllocationRecorder applies the advance of a confirmed payment to open documents of its party
// (a supplier advance used on a receipt, a customer deposit used on an invoice).
type AllocationRecorder struct{ s *Service }

// Record settles in.Allocations from the unallocated part of in.PaymentID, like PostAllocation
// but inside the caller's posting: no number, no idempotency key of its own (the caller's posting
// is the unit that replays). It fails with 422 validation_failed for a missing payment or lines,
// 422 payment_not_found, 422 payment_pending_verification, 409 payment_reversed, 422
// payment_without_party, and on the lines 422 obligation_not_found, allocation_party_mismatch,
// allocation_direction_mismatch, obligation_settled, allocation_exceeds_open and
// allocation_exceeds_payment. An obligation recorded earlier in the
// same posting can be allocated to; it is already locked by that posting.
func (r AllocationRecorder) Record(ctx context.Context, tx posting.Tx, in AllocationInput) (PaymentResult, error) {
	in, err := checkAllocationInput(in)
	if err != nil {
		return PaymentResult{}, err
	}
	return allocateAdvance(ctx, tx, in)
}

// AddLocks declares the debt rows allocating in will lock: the payment and the obligations its
// lines name (an obligation the posting creates itself needs no declaration).
func (in AllocationInput) AddLocks(set *posting.LockSet) {
	set.Add(posting.LockObligations, allocationIDs(in.Allocations)...)
	set.Add(posting.LockPayments, in.PaymentID)
}

// Allocations returns the recorder of advance allocations.
func (s *Service) Allocations() AllocationRecorder { return AllocationRecorder{s: s} }

// Receivables returns the recorder of receivables.
func (s *Service) Receivables() ReceivableRecorder { return ReceivableRecorder{s: s} }

// Payables returns the recorder of payables.
func (s *Service) Payables() PayableRecorder { return PayableRecorder{s: s} }

// Payments returns the recorder of payments.
func (s *Service) Payments() PaymentRecorder { return PaymentRecorder{s: s} }

// CreditNotes returns the recorder of credit notes.
func (s *Service) CreditNotes() CreditNoteRecorder { return CreditNoteRecorder{s: s} }
