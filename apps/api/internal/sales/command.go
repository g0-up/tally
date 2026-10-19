package sales

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// command is a sales posting: the coordinator issues its numbers, locks its rows and runs apply in
// the posting transaction. apply may run twice (the coordinator retries a deadlock once), so it
// reads everything it decides on again inside the transaction.
type command struct {
	scope  string
	date   time.Time
	prefix string
	// extra lists one prefix per further numbered document apply writes (payments, the TH and BH
	// of an exchange); apply must write every one of them.
	extra []string
	locks posting.LockSet
	apply func(ctx context.Context, tx posting.Tx) (posting.Result, error)
}

var (
	_ posting.PostingCommand = command{}
	_ posting.ExtraDocs      = command{}
)

func (c command) Scope() string              { return c.scope }
func (c command) BusinessDate() time.Time    { return c.date }
func (c command) DocPrefix() string          { return c.prefix }
func (c command) LockKeys() posting.LockSet  { return c.locks }
func (c command) ExtraDocPrefixes() []string { return c.extra }

func (c command) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	return c.apply(ctx, tx)
}

// post runs cmd through the coordinator.
func (s *Service) post(ctx context.Context, actor audit.Actor, key idempotency.Key, cmd command) (posting.Response, error) {
	return s.posting.Post(ctx, actor, key, cmd)
}

// postRefused settles a posting whose check before the transaction failed with err. A conflict
// with the current state may be the posting's own doing (the goods a return took back are no
// longer returnable), so the request still goes through the coordinator: a retry of a posting that
// completed gets its stored response, and a new one is refused with err in place of applying.
func (s *Service) postRefused(ctx context.Context, actor audit.Actor, key idempotency.Key, scope string, err error) (posting.Response, error) {
	if apperr.KindOf(err) != apperr.Conflict {
		return posting.Response{}, err
	}
	date, dateErr := s.todayTx(ctx, actor.TenantID)
	if dateErr != nil {
		return posting.Response{}, dateErr
	}
	cmd := command{scope: scope, date: date}
	cmd.apply = func(context.Context, posting.Tx) (posting.Result, error) { return posting.Result{}, err }
	return s.post(ctx, actor, key, cmd)
}

// paymentPrefixes are the number prefixes of payments, one per payment.
func paymentPrefixes(payments []PaymentInput) []string {
	out := make([]string, len(payments))
	for i, p := range payments {
		out[i] = p.DocPrefix()
	}
	return out
}

// addPaymentLocks declares the debt rows recording payments locks: the obligations they settle and
// the payments whose advance they refund.
func addPaymentLocks(set *posting.LockSet, payments []PaymentInput) {
	for _, p := range payments {
		for _, a := range p.Allocations {
			set.Add(posting.LockObligations, a.ObligationID)
		}
		if p.RefundOf != nil {
			set.Add(posting.LockPayments, *p.RefundOf)
		}
	}
}

// paymentKey is the idempotency key of the n-th payment a posting records, derived from the
// posting's own key so a replayed posting finds its payments again.
func paymentKey(scope string, key idempotency.Key, n int) string {
	return scope + ":" + key.Value + ":pay" + strconv.Itoa(n)
}

// checkMethod verifies a payment method may take or pay money now.
func (s *Service) checkMethod(ctx context.Context, q db.Querier, id uuid.UUID, field string) (PaymentMethod, error) {
	m, err := s.methods.Method(ctx, q, id)
	if err != nil {
		if apperr.KindOf(err) == apperr.NotFound {
			return m, errInvalid(map[string]string{field: "not_found"})
		}
		return m, err
	}
	if !m.Active {
		return m, apperr.NewValidation(CodeMethodInactive, "The payment method is not active", map[string]string{field: CodeMethodInactive})
	}
	return m, nil
}
