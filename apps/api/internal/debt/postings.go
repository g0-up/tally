package debt

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Posting scopes of the debt commands (idempotency scopes and audit actions).
const (
	ScopePaymentPost    = "payment.post"
	ScopePaymentConfirm = "payment.confirm"
	ScopePaymentReverse = "payment.reverse"
	ScopeAllocationPost = "allocation.post"
	ScopeAdjustmentPost = "debt_adjustment.post"
	ScopeOffsetPost     = "debt_offset.post"
	ScopeRefundPost     = "refund.post"
)

// Outbox topics of allocations, DN and BT.
const (
	TopicAllocationPosted = "allocation.posted"
	TopicAdjustmentPosted = "debt_adjustment.posted"
	TopicOffsetPosted     = "debt_offset.posted"
)

// documentKey is the idempotency key stored on the document row (payments, DN, BT): the request
// key under its scope, so the same client key used for two kinds of documents does not collide.
func documentKey(scope string, key idempotency.Key) string { return scope + ":" + key.Value }

// PostPayment records a PT/PC from the Công nợ screen (settlement, advance, deposit, or an
// opening advance on openingDate). Allocations are optional: what is not allocated stays an
// advance. A payment through a method that requires verification waits in the verification queue.
func (s *Service) PostPayment(ctx context.Context, actor audit.Actor, key idempotency.Key, in PaymentInput,
	openingDate *time.Time,
) (posting.Response, error) {
	switch in.Purpose {
	case PurposeSettlement, PurposeAdvance, PurposeDeposit, PurposeOpeningAdvance:
	default:
		return posting.Response{}, errField("validation_failed", "purpose", "invalid",
			"Record a settlement, an advance, a deposit or an opening advance here")
	}
	if in.SourceType == "" && in.Purpose != PurposeOpeningAdvance {
		in.SourceType = PaymentSourceDebt
	}
	if in.SourceType != "" && in.SourceType != PaymentSourceDebt && in.SourceType != PaymentSourceInvoice {
		return posting.Response{}, errField("validation_failed", "source_type", "invalid",
			"Payments of other documents are recorded by those documents")
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	if in.Purpose == PurposeOpeningAdvance {
		if date, err = s.openingDate(ctx, actor.TenantID, openingDate, date); err != nil {
			return posting.Response{}, err
		}
	} else if openingDate != nil {
		return posting.Response{}, errField("validation_failed", "opening_cutoff_date", "opening_advance_only",
			"Only an opening advance has a cutoff date")
	}
	in.Key = documentKey(ScopePaymentPost, key)
	return s.posting.Post(ctx, actor, key, paymentPost{s: s, date: date, in: in})
}

// openingDate is the cutoff of an opening balance: the given date, else the store's opening
// cutoff. It may not be after today.
func (s *Service) openingDate(ctx context.Context, tenantID uuid.UUID, given *time.Time, today time.Time) (time.Time, error) {
	if given == nil {
		var cutoff *time.Time
		if err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
			return q.QueryRowxContext(ctx, `SELECT opening_cutoff_date FROM tenants WHERE id = $1`, tenantID).Scan(&cutoff)
		}); err != nil {
			return time.Time{}, err
		}
		if cutoff == nil {
			return time.Time{}, errField("validation_failed", "opening_cutoff_date", "required", "Choose the cutoff date of the opening balance")
		}
		given = cutoff
	}
	if given.After(today) {
		return time.Time{}, errField("validation_failed", "opening_cutoff_date", "in_future", "The cutoff date cannot be in the future")
	}
	return *given, nil
}

type paymentPost struct {
	s    *Service
	date time.Time
	in   PaymentInput
}

func (c paymentPost) Scope() string           { return ScopePaymentPost }
func (c paymentPost) BusinessDate() time.Time { return c.date }
func (c paymentPost) DocPrefix() string       { return paymentPrefix(c.in.Direction) }

func (c paymentPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockObligations, allocationIDs(c.in.Allocations)...)
	if c.in.RefundOf != nil {
		set.Add(posting.LockPayments, *c.in.RefundOf)
	}
	return set
}

func (c paymentPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := recordPayment(ctx, fromTx(tx), c.s.paymentDeps(), c.in, tx.DocNo(), c.in.Key)
	if err != nil {
		return posting.Result{}, err
	}
	publishPayment(tx, ScopePaymentPost, r)
	return posting.Result{Body: toPaymentResponse(r)}, nil
}

// publishPayment adds the audit row and the outbox event of a recorded payment.
func publishPayment(tx posting.Tx, action string, r PaymentResult) {
	tx.Audit(paymentAudit(action, nil, &r.Payment))
	tx.Publish(paymentEvent(TopicPaymentPosted, r.Payment))
}

// ConfirmPayment confirms a payment waiting for verification: its planned allocations (capped by
// what is still open) are applied and its cash entry is written, once.
func (s *Service) ConfirmPayment(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID) (posting.Response, error) {
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var p Payment
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		p, err = getPayment(ctx, q, id, false)
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, paymentConfirm{s: s, date: date, id: id, planned: allocationIDs(p.Planned)})
}

type paymentConfirm struct {
	s       *Service
	date    time.Time
	id      uuid.UUID
	planned []uuid.UUID
}

func (c paymentConfirm) Scope() string           { return ScopePaymentConfirm }
func (c paymentConfirm) BusinessDate() time.Time { return c.date }
func (c paymentConfirm) DocPrefix() string       { return "" }

func (c paymentConfirm) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockObligations, c.planned...)
	set.Add(posting.LockPayments, c.id)
	return set
}

func (c paymentConfirm) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	w := fromTx(tx)
	p, err := getPayment(ctx, tx, c.id, true)
	if err != nil {
		return posting.Result{}, err
	}
	if p.Status != PaymentPending {
		return posting.Result{}, errPaymentNotPending()
	}
	if planned := allocationIDs(p.Planned); !sameIDs(planned, c.planned) {
		return posting.Result{}, errPaymentChanged()
	}
	obs, err := lockObligations(ctx, tx, c.planned)
	if err != nil {
		return posting.Result{}, err
	}
	before := p
	st, err := confirmPayment(ctx, w, c.s.cash, &p, obs)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(paymentAudit(ScopePaymentConfirm, &before, &p))
	tx.Publish(paymentEvent(TopicPaymentConfirmed, p))
	return posting.Result{Status: http.StatusOK, Body: toPaymentResponse(PaymentResult{Payment: p, Settlements: st})}, nil
}

// ReversePayment reverses a payment on today's date: its settlements get negative mirrors, its
// cash entries are mirrored and the balances return to what they were before it. A standing
// refund of its advance blocks it (payment_has_successor).
func (s *Service) ReversePayment(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID) (posting.Response, error) {
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var cmd paymentReverse
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		p, err := getPayment(ctx, q, id, false)
		if err != nil {
			return err
		}
		st, err := standingSettlements(ctx, writeCtx{q: q}, SettledByPayment, id)
		if err != nil {
			return err
		}
		cmd = paymentReverse{s: s, date: date, id: id, refundOf: p.RefundOf, settlements: st}
		return nil
	}); err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, cmd)
}

type paymentReverse struct {
	s           *Service
	date        time.Time
	id          uuid.UUID
	refundOf    *uuid.UUID
	settlements []Settlement
}

func (c paymentReverse) Scope() string           { return ScopePaymentReverse }
func (c paymentReverse) BusinessDate() time.Time { return c.date }
func (c paymentReverse) DocPrefix() string       { return "" }

func (c paymentReverse) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockObligations, settlementObligations(c.settlements)...)
	set.Add(posting.LockPayments, c.id)
	if c.refundOf != nil {
		set.Add(posting.LockPayments, *c.refundOf)
	}
	return set
}

func (c paymentReverse) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	w := fromTx(tx)
	p, err := getPayment(ctx, tx, c.id, true)
	if err != nil {
		return posting.Result{}, err
	}
	var source *Payment
	if p.RefundOf != nil {
		if c.refundOf == nil || *c.refundOf != *p.RefundOf {
			return posting.Result{}, errPaymentChanged()
		}
		src, err := getPayment(ctx, tx, *p.RefundOf, true)
		if err != nil {
			return posting.Result{}, err
		}
		source = &src
	}
	st, err := standingSettlements(ctx, w, SettledByPayment, c.id)
	if err != nil {
		return posting.Result{}, err
	}
	if !sameSettlements(st, c.settlements) {
		return posting.Result{}, errPaymentChanged()
	}
	obs, err := lockObligations(ctx, tx, settlementObligations(st))
	if err != nil {
		return posting.Result{}, err
	}
	before := p
	mirrors, err := reversePayment(ctx, w, c.s.cash, &p, source, st, obs)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(paymentAudit(ScopePaymentReverse, &before, &p))
	tx.Publish(paymentEvent(TopicPaymentReversed, p))
	return posting.Result{Status: http.StatusOK, Body: toPaymentResponse(PaymentResult{Payment: p, Settlements: mirrors})}, nil
}

// AllocationInput applies the advance of a confirmed payment to open documents of its party.
type AllocationInput struct {
	PaymentID   uuid.UUID
	Allocations []Allocation
}

// PostAllocation allocates the unallocated part of a confirmed payment (re-allocation of an
// advance). Lines are manual or accepted from the oldest-due suggestion.
func (s *Service) PostAllocation(ctx context.Context, actor audit.Actor, key idempotency.Key, in AllocationInput) (posting.Response, error) {
	in, err := checkAllocationInput(in)
	if err != nil {
		return posting.Response{}, err
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, allocationPost{date: date, in: in})
}

type allocationPost struct {
	date time.Time
	in   AllocationInput
}

func (c allocationPost) Scope() string           { return ScopeAllocationPost }
func (c allocationPost) BusinessDate() time.Time { return c.date }
func (c allocationPost) DocPrefix() string       { return "" }

func (c allocationPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	c.in.AddLocks(&set)
	return set
}

func (c allocationPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := allocateAdvance(ctx, tx, c.in)
	if err != nil {
		return posting.Result{}, err
	}
	return posting.Result{Body: toPaymentResponse(r)}, nil
}

// checkAllocationInput validates the shape of an allocation and defaults its line modes to manual.
func checkAllocationInput(in AllocationInput) (AllocationInput, error) {
	fields := map[string]string{}
	if in.PaymentID == uuid.Nil {
		fields["payment_id"] = "required"
	}
	if len(in.Allocations) == 0 {
		fields["allocations"] = "required"
	}
	lines := slices.Clone(in.Allocations)
	for i, l := range lines {
		if l.Mode == "" {
			lines[i].Mode = ModeManual
		} else if l.Mode != ModeManual && l.Mode != ModeSuggested {
			fields[lineField(i, "mode")] = "invalid"
		}
	}
	if len(fields) > 0 {
		return AllocationInput{}, errInvalid(fields)
	}
	if err := checkLines(lines); err != nil {
		return AllocationInput{}, err
	}
	in.Allocations = lines
	return in, nil
}

// allocateAdvance settles the lines of in from the unallocated part of its payment inside tx and
// adds the audit row and outbox event.
func allocateAdvance(ctx context.Context, tx posting.Tx, in AllocationInput) (PaymentResult, error) {
	w := fromTx(tx)
	obs, err := lockObligations(ctx, tx, allocationIDs(in.Allocations))
	if err != nil {
		return PaymentResult{}, err
	}
	p, err := getPayment(ctx, tx, in.PaymentID, true)
	if isNotFound(err) {
		return PaymentResult{}, errField("payment_not_found", "payment_id", "not_found", "The payment does not exist")
	}
	if err != nil {
		return PaymentResult{}, err
	}
	switch {
	case p.Status == PaymentPending:
		return PaymentResult{}, errPaymentPending()
	case p.Status == PaymentReversed:
		return PaymentResult{}, errPaymentReversed()
	case p.PartyID == nil:
		return PaymentResult{}, errField("payment_without_party", "payment_id", "no_party", "The payment has no party to allocate to")
	}
	if err := checkAllocations(in.Allocations, *p.PartyID, LedgerOf(p.Direction), p.Unallocated(), obs); err != nil {
		return PaymentResult{}, err
	}
	before := p
	st, err := applyAllocations(ctx, w, &p, in.Allocations, obs)
	if err != nil {
		return PaymentResult{}, err
	}
	tx.Audit(paymentAudit(ScopeAllocationPost, &before, &p))
	tx.Publish(outbox.Event{Topic: TopicAllocationPosted, Payload: map[string]any{
		"payment_id": p.ID, "party_id": p.PartyID, "amount": sumAllocations(in.Allocations),
	}})
	return PaymentResult{Payment: p, Settlements: st}, nil
}

// SuggestAllocation is the oldest-due-first plan of amount over the open documents of a party on
// a ledger; the owner confirms it with PostAllocation or PostPayment. It writes nothing.
func (s *Service) SuggestAllocation(ctx context.Context, scope Scope, partyID uuid.UUID, ledger string, amount money.Money) ([]Allocation, error) {
	fields := map[string]string{}
	if partyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if ledger != Receivable && ledger != Payable {
		fields["direction"] = "invalid"
	}
	if !amount.IsPositive() {
		fields["amount"] = "must_be_positive"
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	var out []Allocation
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := getParty(ctx, q, partyID); err != nil {
			return err
		}
		open, err := openCandidates(ctx, q, partyID, ledger, false)
		if err != nil {
			return err
		}
		out = OldestDueFirst{}.Plan(amount, open)
		return nil
	})
	if out == nil {
		out = []Allocation{}
	}
	return out, err
}

// PostAdjustment records a DN. payload is the request body: a write-off spends the owner's
// debt_write_off confirmation bound to it.
func (s *Service) PostAdjustment(ctx context.Context, actor audit.Actor, key idempotency.Key, in AdjustmentInput) (posting.Response, error) {
	if err := checkAdjustmentInput(in); err != nil {
		return posting.Response{}, err
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	if in.Kind == AdjustOpeningBalance {
		if date, err = s.openingDate(ctx, actor.TenantID, in.OpeningCutoffDate, date); err != nil {
			return posting.Response{}, err
		}
		in.OpeningCutoffDate = &date
	}
	return s.posting.Post(ctx, actor, key, adjustmentPost{s: s, date: date, in: in, key: documentKey(ScopeAdjustmentPost, key)})
}

type adjustmentPost struct {
	s    *Service
	date time.Time
	in   AdjustmentInput
	key  string
}

func (c adjustmentPost) Scope() string           { return ScopeAdjustmentPost }
func (c adjustmentPost) BusinessDate() time.Time { return c.date }
func (c adjustmentPost) DocPrefix() string       { return docseq.PrefixDebtAdjustment }

func (c adjustmentPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	if c.in.TargetObligationID != nil {
		set.Add(posting.LockObligations, *c.in.TargetObligationID)
	}
	return set
}

func (c adjustmentPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := recordAdjustment(ctx, fromTx(tx), c.s.exceptions, c.s.onboarding, c.in, tx.DocNo(), c.key)
	if err != nil {
		return posting.Result{}, err
	}
	after := map[string]any{"adjustment": r.Adjustment}
	if c.in.Kind == AdjustWriteOff {
		after["exception_confirmation_id"] = c.in.ConfirmationID
	}
	tx.Audit(audit.Event{Action: ScopeAdjustmentPost, EntityType: "debt_adjustment", EntityID: r.Adjustment.ID,
		DocNo: r.Adjustment.DocNo, After: after})
	tx.Publish(outbox.Event{Topic: TopicAdjustmentPosted, Payload: map[string]any{
		"debt_adjustment_id": r.Adjustment.ID, "doc_no": r.Adjustment.DocNo, "party_id": r.Adjustment.PartyID,
		"direction": r.Adjustment.Direction, "kind": r.Adjustment.Kind, "amount": r.Adjustment.Amount,
	}})
	if r.Obligation != nil {
		tx.Publish(obligationCreated(*r.Obligation))
	}
	return posting.Result{Body: toAdjustmentResponse(r)}, nil
}

// PostOffset records a BT between a receivable and a payable of one party.
func (s *Service) PostOffset(ctx context.Context, actor audit.Actor, key idempotency.Key, in OffsetInput) (posting.Response, error) {
	if err := checkOffsetInput(in); err != nil {
		return posting.Response{}, err
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, offsetPost{date: date, in: in, key: documentKey(ScopeOffsetPost, key)})
}

type offsetPost struct {
	date time.Time
	in   OffsetInput
	key  string
}

func (c offsetPost) Scope() string           { return ScopeOffsetPost }
func (c offsetPost) BusinessDate() time.Time { return c.date }
func (c offsetPost) DocPrefix() string       { return docseq.PrefixDebtOffset }

func (c offsetPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockObligations, c.in.ReceivableObligationID, c.in.PayableObligationID)
	return set
}

func (c offsetPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := recordOffset(ctx, fromTx(tx), c.in, tx.DocNo(), c.key)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(audit.Event{Action: ScopeOffsetPost, EntityType: "debt_offset", EntityID: r.Offset.ID, DocNo: r.Offset.DocNo,
		After: r.Offset})
	tx.Publish(outbox.Event{Topic: TopicOffsetPosted, Payload: map[string]any{
		"debt_offset_id": r.Offset.ID, "doc_no": r.Offset.DocNo, "party_id": r.Offset.PartyID, "amount": r.Offset.Amount,
	}})
	return posting.Result{Body: toOffsetResponse(r)}, nil
}

// RefundInput pays back money: either a refund due (ObligationID, the store owes a customer
// after a return, or a supplier owes the store) or the unallocated advance of a payment
// (PaymentID). Amount defaults to all that is open or unallocated.
type RefundInput struct {
	ObligationID *uuid.UUID
	PaymentID    *uuid.UUID
	MethodID     uuid.UUID
	Amount       *money.Money
	Reference    string
	Note         string
}

// PostRefund records the refund payment: PC when the store pays back, PT when a supplier does.
func (s *Service) PostRefund(ctx context.Context, actor audit.Actor, key idempotency.Key, in RefundInput) (posting.Response, error) {
	if (in.ObligationID == nil) == (in.PaymentID == nil) {
		return posting.Response{}, errInvalid(map[string]string{"obligation_id": "one_of", "payment_id": "one_of"})
	}
	if in.Amount != nil && !in.Amount.IsPositive() {
		return posting.Response{}, errInvalid(map[string]string{"amount": "must_be_positive"})
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var pin PaymentInput
	err = s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		pin, err = refundPayment(ctx, q, in)
		return err
	})
	if err != nil {
		return posting.Response{}, err
	}
	pin.Key = documentKey(ScopeRefundPost, key)
	return s.posting.Post(ctx, actor, key, refundPost{paymentPost{s: s, date: date, in: pin}})
}

// refundPayment builds the payment of a refund from what it pays back (read before posting; the
// posting locks and checks it again).
func refundPayment(ctx context.Context, q db.Querier, in RefundInput) (PaymentInput, error) {
	out := PaymentInput{MethodID: in.MethodID, Reference: in.Reference, Note: in.Note, SourceType: PaymentSourceDebt}
	if in.ObligationID != nil {
		ob, err := getObligation(ctx, q, *in.ObligationID)
		if isNotFound(err) {
			return PaymentInput{}, errField("obligation_not_found", "obligation_id", "not_found", "The document does not exist")
		}
		if err != nil {
			return PaymentInput{}, err
		}
		if ob.SourceType != SourceRefundDue {
			return PaymentInput{}, errField("refund_not_due", "obligation_id", "not_refund_due", "The document is not a refund due")
		}
		if !ob.Open().IsPositive() {
			return PaymentInput{}, errObligationSettled("obligation_id")
		}
		amount := ob.Open()
		if in.Amount != nil {
			amount = *in.Amount
		}
		party := ob.PartyID
		out.Direction, out.PartyID, out.Purpose, out.Amount = PaymentDirectionOf(ob.Direction), &party, PurposeRefund, amount
		out.Allocations = []Allocation{{ObligationID: ob.ID, Amount: amount, Mode: ModeManual}}
		return out, nil
	}
	p, err := getPayment(ctx, q, *in.PaymentID, false)
	if isNotFound(err) {
		return PaymentInput{}, errField("payment_not_found", "payment_id", "not_found", "The payment does not exist")
	}
	if err != nil {
		return PaymentInput{}, err
	}
	if p.PartyID == nil {
		return PaymentInput{}, errField("payment_without_party", "payment_id", "no_party", "The payment has no advance to refund")
	}
	amount := p.Unallocated()
	if in.Amount != nil {
		amount = *in.Amount
	}
	if !amount.IsPositive() {
		return PaymentInput{}, errInsufficientAdvance("amount")
	}
	direction := In
	if p.Direction == In {
		direction = Out
	}
	id := p.ID
	out.Direction, out.PartyID, out.Purpose, out.Amount, out.RefundOf = direction, p.PartyID, PurposeAdvanceRefund, amount, &id
	return out, nil
}

// refundPost is a payment posting under the refund scope.
type refundPost struct{ paymentPost }

func (c refundPost) Scope() string { return ScopeRefundPost }

func (c refundPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := recordPayment(ctx, fromTx(tx), c.s.paymentDeps(), c.in, tx.DocNo(), c.in.Key)
	if err != nil {
		return posting.Result{}, err
	}
	publishPayment(tx, ScopeRefundPost, r)
	return posting.Result{Body: toPaymentResponse(r)}, nil
}

func sameIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
	}
	return true
}
