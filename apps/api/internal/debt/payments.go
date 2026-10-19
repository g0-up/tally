package debt

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Payment topics.
const (
	TopicPaymentPosted    = "payment.posted"
	TopicPaymentConfirmed = "payment.confirmed"
	TopicPaymentReversed  = "payment.reversed"
)

const (
	maxReferenceLen = 200
	maxNoteLen      = 1000
)

// PaymentInput is one PT (money in) or PC (money out). The business date is the posting's.
type PaymentInput struct {
	Direction string
	// PartyID is nil only for a walk-in POS sale, its return or exchange (purpose settlement or
	// refund): the money is used in full by that document and never becomes an advance.
	PartyID *uuid.UUID
	Purpose string
	// MethodID decides the fund and whether an incoming payment waits for verification.
	MethodID uuid.UUID
	// Amount is the money kept or paid out (tendered minus change), > 0.
	Amount money.Money
	// SourceType is the document or screen taking the money (PaymentSource*); empty for none.
	SourceType string
	SourceID   *uuid.UUID
	// EarmarkedInvoiceID reserves an incoming deposit for one invoice: it counts against that
	// order's credit check and is applied to its documents automatically.
	EarmarkedInvoiceID *uuid.UUID
	// ShiftID is the POS shift the cash entry belongs to.
	ShiftID *uuid.UUID
	// RefundOf names the payment whose advance an advance_refund pays back.
	RefundOf  *uuid.UUID
	Reference string
	Note      string
	// Allocations apply the payment to obligations of the party on the payment's ledger; the rest
	// is an advance. A pending payment keeps them as its plan.
	Allocations []Allocation
	// Key makes the payment idempotent across retries of the caller's posting: a second call with
	// the same key returns the first payment (Replayed). Empty keys are generated.
	Key string
}

// PaymentResult is a recorded payment and the settlements it wrote.
type PaymentResult struct {
	Payment     Payment
	Settlements []Settlement
	Replayed    bool
}

func validPurpose(p string) bool {
	switch p {
	case PurposeSettlement, PurposeAdvance, PurposeDeposit, PurposeRefund, PurposeAdvanceRefund, PurposeOpeningAdvance:
		return true
	}
	return false
}

func validPaymentSource(s string) bool {
	switch s {
	case "", PaymentSourcePOS, PaymentSourceInvoice, PaymentSourceDebt, PaymentSourceSalesReturn, PaymentSourceExchange,
		PaymentSourcePurchase, PaymentSourceLandedCost:
		return true
	}
	return false
}

// anonymousSource reports the sources that may take money without a party.
func anonymousSource(s string) bool {
	return s == PaymentSourcePOS || s == PaymentSourceSalesReturn || s == PaymentSourceExchange
}

// checkPaymentInput validates the shape of in before anything is read.
func checkPaymentInput(in PaymentInput) error {
	fields := map[string]string{}
	if in.Direction != In && in.Direction != Out {
		fields["direction"] = "invalid"
	}
	if !validPurpose(in.Purpose) {
		fields["purpose"] = "invalid"
	}
	if in.MethodID == uuid.Nil {
		fields["method_id"] = "required"
	}
	if !in.Amount.IsPositive() {
		fields["amount"] = "must_be_positive"
	}
	if !validPaymentSource(in.SourceType) {
		fields["source_type"] = "invalid"
	}
	if in.SourceID != nil && in.SourceType == "" {
		fields["source_type"] = "required"
	}
	if utf8.RuneCountInString(in.Reference) > maxReferenceLen {
		fields["reference"] = "too_long"
	}
	if utf8.RuneCountInString(in.Note) > maxNoteLen {
		fields["note"] = "too_long"
	}
	if in.PartyID == nil {
		if !anonymousSource(in.SourceType) || (in.Purpose != PurposeSettlement && in.Purpose != PurposeRefund) {
			fields["party_id"] = "required"
		}
		if len(in.Allocations) > 0 {
			fields["allocations"] = "party_required"
		}
	}
	switch in.Purpose {
	case PurposeOpeningAdvance, PurposeAdvanceRefund:
		if len(in.Allocations) > 0 {
			fields["allocations"] = "not_allowed"
		}
	}
	if (in.Purpose == PurposeAdvanceRefund) != (in.RefundOf != nil) {
		fields["refund_of"] = "advance_refund_only"
	}
	if in.Purpose == PurposeOpeningAdvance && in.SourceType != "" {
		fields["source_type"] = "not_allowed"
	}
	if in.EarmarkedInvoiceID != nil && (in.Direction != In || in.Purpose != PurposeDeposit) {
		fields["earmarked_invoice_id"] = "incoming_deposit_only"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return checkLines(in.Allocations)
}

// paymentDeps are the collaborators of a payment write.
type paymentDeps struct {
	cash       CashBook
	onboarding Onboarding
}

// recordPayment writes one payment inside the caller's posting: it resolves the method, checks
// the allocations against the obligations (locked here in id order, then the payment rows), and
// for a confirmed payment writes the settlements and the cash entry (the fund row is locked
// last by the cash book). A payment through a method that requires verification waits instead:
// it keeps its allocations as a plan and moves nothing until it is confirmed.
func recordPayment(ctx context.Context, w writeCtx, d paymentDeps, in PaymentInput, docNo, key string) (PaymentResult, error) {
	if err := checkPaymentInput(in); err != nil {
		return PaymentResult{}, err
	}
	if in.PartyID != nil {
		if _, err := getParty(ctx, w.q, *in.PartyID); err != nil {
			return PaymentResult{}, err
		}
	}
	method, err := d.cash.Method(ctx, w.q, in.MethodID)
	if err != nil {
		return PaymentResult{}, err
	}
	if !method.Active {
		return PaymentResult{}, errField("payment_method_inactive", "method_id", "inactive", "The payment method is not active")
	}
	// An opening advance moves no money today, and an advance refund is checked against the
	// advance it returns now, so neither waits for verification.
	pending := in.Direction == In && method.RequiresVerification && in.Purpose != PurposeOpeningAdvance &&
		in.Purpose != PurposeAdvanceRefund
	ledger := LedgerOf(in.Direction)

	obs, err := lockObligations(ctx, w.q, allocationIDs(in.Allocations))
	if err != nil {
		return PaymentResult{}, err
	}
	var source *Payment
	if in.RefundOf != nil {
		src, err := getPayment(ctx, w.q, *in.RefundOf, true)
		if err != nil {
			if isNotFound(err) {
				return PaymentResult{}, errField("payment_not_found", "refund_of", "not_found", "The payment to refund does not exist")
			}
			return PaymentResult{}, err
		}
		switch {
		case src.Status == PaymentPending:
			return PaymentResult{}, errPaymentPending()
		case src.Status == PaymentReversed:
			return PaymentResult{}, errPaymentReversed()
		case src.PartyID == nil || *src.PartyID != *in.PartyID:
			return PaymentResult{}, errField("refund_party_mismatch", "refund_of", "other_party", "The payment belongs to another party")
		case src.Direction == in.Direction:
			return PaymentResult{}, errField("refund_direction_mismatch", "refund_of", "same_direction",
				"An advance is refunded in the opposite direction")
		case in.Amount.Cmp(src.Unallocated()) > 0:
			return PaymentResult{}, errInsufficientAdvance("amount")
		}
		source = &src
	}
	if in.PartyID != nil {
		if err := checkAllocations(in.Allocations, *in.PartyID, ledger, in.Amount, obs); err != nil {
			return PaymentResult{}, err
		}
	}
	if in.Purpose == PurposeRefund && in.PartyID != nil && !sumAllocations(in.Allocations).Equal(in.Amount) {
		return PaymentResult{}, errField("refund_must_settle", "allocations", "must_cover_amount",
			"A refund pays back documents the store owes: allocate the whole amount")
	}

	p := Payment{
		ID: ids.New(), TenantID: w.tenantID, DocNo: docNo, Direction: in.Direction, PartyID: in.PartyID, Purpose: in.Purpose,
		FundAccountID: method.FundAccountID, PaymentMethodID: method.ID, Amount: in.Amount, Status: PaymentConfirmed,
		EarmarkedInvoiceID: in.EarmarkedInvoiceID, SourceID: in.SourceID, BusinessDate: w.date,
		Reference: optString(in.Reference), Note: optString(in.Note), PostedAt: w.now, PostedBy: w.by,
		RefundOf: in.RefundOf, CreatedAt: w.now, UpdatedAt: w.now, Version: 1,
	}
	if in.SourceType != "" {
		st := in.SourceType
		p.SourceType = &st
	}
	if pending {
		p.Status, p.Planned = PaymentPending, PlannedAllocations(in.Allocations)
	} else {
		at := w.now
		p.ConfirmedAt = &at
		if in.PartyID == nil || in.Purpose == PurposeAdvanceRefund {
			p.AllocatedAmount = in.Amount
		}
	}
	if err := insertPayment(ctx, w, p, key); err != nil {
		return PaymentResult{}, err
	}
	out := PaymentResult{Payment: p}
	if !pending {
		if out.Settlements, err = applyAllocations(ctx, w, &out.Payment, in.Allocations, obs); err != nil {
			return PaymentResult{}, err
		}
		if source != nil {
			if err := moveAllocated(ctx, w, source, in.Amount); err != nil {
				return PaymentResult{}, err
			}
		}
		if in.Purpose != PurposeOpeningAdvance {
			if err := d.cash.Record(ctx, w.q, w.tenantID, cashEntryOf(out.Payment, in.ShiftID, w)); err != nil {
				return PaymentResult{}, err
			}
		}
	}
	if in.Purpose == PurposeOpeningAdvance && d.onboarding != nil {
		if err := d.onboarding.CompleteStep(ctx, w.q, w.tenantID, StepOpeningDebt, w.by, w.now); err != nil {
			return PaymentResult{}, fmt.Errorf("debt: complete onboarding: %w", err)
		}
	}
	return out, nil
}

// applyAllocations settles each line against its locked obligation and moves the payment's
// allocated projection by their total.
func applyAllocations(ctx context.Context, w writeCtx, p *Payment, lines []Allocation, obs map[uuid.UUID]*Obligation) ([]Settlement, error) {
	var out []Settlement
	total := money.Money{}
	for _, l := range lines {
		s, err := settle(ctx, w, obs[l.ObligationID], SettledByPayment, p.ID, l.Amount, l.Mode)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		total = total.Add(l.Amount)
	}
	if total.IsPositive() {
		if err := moveAllocated(ctx, w, p, total); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func insertPayment(ctx context.Context, w writeCtx, p Payment, key string) error {
	if _, err := w.q.ExecContext(ctx, `
		INSERT INTO payments (id, tenant_id, doc_no, direction, party_id, purpose, fund_account_id, payment_method_id, amount,
		                      allocated_amount, status, earmarked_invoice_id, source_type, source_id, business_date, reference,
		                      note, idempotency_key, posted_at, posted_by, refund_of, confirmed_at, planned_allocations,
		                      created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23,
		        $24, $24, 1)`,
		p.ID, p.TenantID, p.DocNo, p.Direction, p.PartyID, p.Purpose, p.FundAccountID, p.PaymentMethodID, p.Amount,
		p.AllocatedAmount, p.Status, p.EarmarkedInvoiceID, p.SourceType, p.SourceID, p.BusinessDate, p.Reference,
		p.Note, key, p.PostedAt, p.PostedBy, p.RefundOf, p.ConfirmedAt, p.Planned, p.CreatedAt); err != nil {
		return fmt.Errorf("debt: insert payment: %w", err)
	}
	return nil
}

// paymentByKey returns the payment recorded under an idempotency key, if any.
func paymentByKey(ctx context.Context, w writeCtx, key string) (*PaymentResult, error) {
	var rows []Payment
	if err := selectAll(ctx, w.q, &rows, `SELECT `+paymentColumns+` FROM payments p WHERE p.idempotency_key = $1`, key); err != nil {
		return nil, fmt.Errorf("debt: read payment by key: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var st []Settlement
	if err := selectAll(ctx, w.q, &st, `SELECT `+settlementColumns+` FROM obligation_settlements s
		WHERE s.settled_by_type = $1 AND s.settled_by_id = $2 ORDER BY s.created_at, s.id`, SettledByPayment, rows[0].ID); err != nil {
		return nil, fmt.Errorf("debt: read payment settlements: %w", err)
	}
	return &PaymentResult{Payment: rows[0], Settlements: st, Replayed: true}, nil
}

// replayPayment returns prior, the payment recorded under in.Key, when in describes the same
// payment. Allocations are compared while they are still the recorded ones (the plan of a pending
// payment, the settlements of a payment confirmed when it was recorded); automatic earmark lines
// are the system's, not the caller's, and are left out.
func replayPayment(prior PaymentResult, in PaymentInput) (PaymentResult, error) {
	p := prior.Payment
	if p.Status == PaymentReversed {
		return PaymentResult{}, errPaymentReversed()
	}
	source := ""
	if p.SourceType != nil {
		source = *p.SourceType
	}
	same := p.Direction == in.Direction && sameRef(p.PartyID, in.PartyID) && p.Purpose == in.Purpose &&
		p.PaymentMethodID == in.MethodID && p.Amount.Equal(in.Amount) && source == in.SourceType &&
		sameRef(p.SourceID, in.SourceID) && sameRef(p.EarmarkedInvoiceID, in.EarmarkedInvoiceID) && sameRef(p.RefundOf, in.RefundOf)
	var recorded []Allocation
	compare := true
	switch {
	case p.Status == PaymentPending:
		recorded = p.Planned
	case p.ConfirmedAt != nil && p.ConfirmedAt.Equal(p.PostedAt):
		for _, s := range prior.Settlements {
			if s.ReversalOf == nil && s.CreatedAt.Equal(p.PostedAt) {
				recorded = append(recorded, Allocation{ObligationID: s.ObligationID, Amount: s.Amount, Mode: s.AllocationMode})
			}
		}
	default:
		compare = false
	}
	if same && compare && !sameLines(recorded, in.Allocations) {
		same = false
	}
	if !same {
		return PaymentResult{}, errPaymentExists()
	}
	return prior, nil
}

// sameLines reports two sets of allocation lines as equal, automatic earmark lines left out.
func sameLines(a, b []Allocation) bool {
	ordered := func(lines []Allocation) []Allocation {
		var out []Allocation
		for _, l := range lines {
			if l.Mode != ModeAutoEarmarked {
				out = append(out, l)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].ObligationID != out[j].ObligationID {
				return out[i].ObligationID.String() < out[j].ObligationID.String()
			}
			return out[i].Amount.Cmp(out[j].Amount) < 0
		})
		return out
	}
	x, y := ordered(a), ordered(b)
	return slices.EqualFunc(x, y, func(l, m Allocation) bool {
		return l.ObligationID == m.ObligationID && l.Amount.Equal(m.Amount) && l.Mode == m.Mode
	})
}

func sameRef(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// cashEntryOf is the signed fund movement of a confirmed payment.
func cashEntryOf(p Payment, shiftID *uuid.UUID, w writeCtx) CashEntry {
	amount := p.Amount
	if p.Direction == Out {
		amount = amount.Neg()
	}
	source := ""
	if p.SourceType != nil {
		source = *p.SourceType
	}
	return CashEntry{
		FundAccountID: p.FundAccountID, Amount: amount, Direction: p.Direction, Purpose: p.Purpose, SourceType: source,
		PaymentID: p.ID, ShiftID: shiftID, BusinessDate: w.date, PostedAt: w.now, PostedBy: w.by,
	}
}

// paymentPrefix is the document prefix of a payment direction: PT in, PC out.
func paymentPrefix(direction string) string {
	if direction == Out {
		return docseq.PrefixCashPayment
	}
	return docseq.PrefixCashReceipt
}

// confirmPayment applies a pending payment: its plan, capped by what is still open on each
// document and by the payment, is allocated on the confirmation date, and its cash entry is
// written. The caller holds the obligation locks (planned documents) and the payment lock.
func confirmPayment(ctx context.Context, w writeCtx, cash CashBook, p *Payment, obs map[uuid.UUID]*Obligation) ([]Settlement, error) {
	var lines []Allocation
	left := p.Amount
	if p.PartyID != nil {
		for _, l := range p.Planned {
			ob, ok := obs[l.ObligationID]
			if !ok || ob.PartyID != *p.PartyID || ob.Direction != LedgerOf(p.Direction) {
				continue
			}
			amount := minMoney(minMoney(l.Amount, ob.Open()), left)
			if !amount.IsPositive() {
				continue
			}
			lines = append(lines, Allocation{ObligationID: l.ObligationID, Amount: amount, Mode: l.Mode})
			left = left.Sub(amount)
		}
	}
	allocated := p.Amount.Sub(left)
	if p.PartyID == nil || p.Purpose == PurposeAdvanceRefund {
		allocated = p.Amount
	}
	if _, err := w.q.ExecContext(ctx, `
		UPDATE payments SET status = $2, confirmed_at = $3, planned_allocations = NULL, updated_at = $3, version = version + 1
		WHERE id = $1`, p.ID, PaymentConfirmed, w.now); err != nil {
		return nil, fmt.Errorf("debt: confirm payment: %w", err)
	}
	at := w.now
	p.Status, p.ConfirmedAt, p.Planned, p.UpdatedAt = PaymentConfirmed, &at, nil, w.now
	p.Version++
	var out []Settlement
	for _, l := range lines {
		s, err := settle(ctx, w, obs[l.ObligationID], SettledByPayment, p.ID, l.Amount, l.Mode)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if allocated.IsPositive() {
		if err := moveAllocated(ctx, w, p, allocated); err != nil {
			return nil, err
		}
	}
	if err := cash.Record(ctx, w.q, w.tenantID, cashEntryOf(*p, nil, w)); err != nil {
		return nil, err
	}
	return out, nil
}

// reversePayment reverses p in place: every settlement it wrote gets its negative mirror, the
// advance it refunded goes back to its source payment, its cash entries are mirrored and it is
// marked reversed on the business date. A refund of its advance that is still standing blocks it.
// The caller holds the locks of the settled obligations and of p and its refund source.
func reversePayment(ctx context.Context, w writeCtx, cash CashBook, p *Payment, source *Payment,
	settlements []Settlement, obs map[uuid.UUID]*Obligation,
) ([]Settlement, error) {
	if p.Status == PaymentReversed {
		return nil, errPaymentReversed()
	}
	var successors int
	if err := w.q.QueryRowxContext(ctx, `SELECT COUNT(*) FROM payments WHERE refund_of = $1 AND status <> $2`,
		p.ID, PaymentReversed).Scan(&successors); err != nil {
		return nil, fmt.Errorf("debt: read payment successors: %w", err)
	}
	if successors > 0 {
		return nil, errPaymentHasSuccessor()
	}
	var out []Settlement
	if p.Status == PaymentConfirmed {
		for _, s := range settlements {
			r, err := reverseSettlement(ctx, w, obs[s.ObligationID], s)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		if source != nil {
			if err := moveAllocated(ctx, w, source, p.Amount.Neg()); err != nil {
				return nil, err
			}
		}
		if err := cash.ReversePayment(ctx, w.q, w.tenantID, p.ID, w.date, w.now, w.by); err != nil {
			return nil, err
		}
	}
	if _, err := w.q.ExecContext(ctx, `
		UPDATE payments SET status = $2, reversed_on = $3, allocated_amount = 0, planned_allocations = NULL,
		       updated_at = $4, version = version + 1
		WHERE id = $1`, p.ID, PaymentReversed, w.date, w.now); err != nil {
		return nil, fmt.Errorf("debt: reverse payment: %w", err)
	}
	day := w.date
	p.Status, p.ReversedOn, p.AllocatedAmount, p.Planned, p.UpdatedAt = PaymentReversed, &day, money.Money{}, nil, w.now
	p.Version++
	return out, nil
}

// standingSettlements are the settlements of a payment that were not reversed yet.
func standingSettlements(ctx context.Context, w writeCtx, byType string, byID uuid.UUID) ([]Settlement, error) {
	var out []Settlement
	if err := selectAll(ctx, w.q, &out, `SELECT `+settlementColumns+` FROM obligation_settlements s
		WHERE s.settled_by_type = $1 AND s.settled_by_id = $2 AND s.reversal_of IS NULL
		  AND NOT EXISTS (SELECT 1 FROM obligation_settlements r WHERE r.reversal_of = s.id)
		ORDER BY s.created_at, s.id`, byType, byID); err != nil {
		return nil, fmt.Errorf("debt: read settlements: %w", err)
	}
	return out, nil
}

func settlementObligations(st []Settlement) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(st))
	for _, s := range st {
		out = append(out, s.ObligationID)
	}
	return uniqueIDs(out...)
}

func sameSettlements(a, b []Settlement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func sortPaymentsByPosting(ps []Payment) {
	sort.SliceStable(ps, func(i, j int) bool {
		if !ps[i].PostedAt.Equal(ps[j].PostedAt) {
			return ps[i].PostedAt.Before(ps[j].PostedAt)
		}
		return ps[i].ID.String() < ps[j].ID.String()
	})
}

func optString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func paymentAudit(action string, before, after *Payment) audit.Event {
	evt := audit.Event{Action: action, EntityType: "payment"}
	if after != nil {
		evt.EntityID, evt.DocNo, evt.After = after.ID, after.DocNo, after
	}
	if before != nil {
		evt.EntityID, evt.DocNo, evt.Before = before.ID, before.DocNo, before
	}
	return evt
}

func paymentEvent(topic string, p Payment) outbox.Event {
	return outbox.Event{Topic: topic, Payload: map[string]any{
		"payment_id": p.ID, "doc_no": p.DocNo, "direction": p.Direction, "party_id": p.PartyID, "purpose": p.Purpose,
		"amount": p.Amount, "status": p.Status, "business_date": p.BusinessDate.Format(time.DateOnly),
	}}
}
