package kits

import (
	"context"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// command is a kits posting: the coordinator issues its number, locks its rows and runs apply in
// the posting transaction. apply may run twice (the coordinator retries a deadlock once), so it
// reads everything it decides on again inside the transaction.
type command struct {
	scope  string
	date   time.Time
	prefix string
	locks  posting.LockSet
	apply  func(ctx context.Context, tx posting.Tx) (posting.Result, error)
}

var _ posting.PostingCommand = command{}

func (c command) Scope() string             { return c.scope }
func (c command) BusinessDate() time.Time   { return c.date }
func (c command) DocPrefix() string         { return c.prefix }
func (c command) LockKeys() posting.LockSet { return c.locks }

func (c command) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	return c.apply(ctx, tx)
}

// postRefused settles a posting whose check before the transaction failed with err. A conflict
// with the current state may be the posting's own doing (the batch that completed the order), so
// the request still goes through the coordinator: a retry of a posting that completed gets its
// stored response, and a new one is refused with err in place of applying.
func (s *Service) postRefused(ctx context.Context, actor audit.Actor, key idempotency.Key, scope string, err error) (posting.Response, error) {
	if apperr.KindOf(err) != apperr.Conflict && apperr.KindOf(err) != apperr.NotFound {
		return posting.Response{}, err
	}
	date, dateErr := s.today(ctx, actor.TenantID)
	if dateErr != nil {
		return posting.Response{}, dateErr
	}
	cmd := command{scope: scope, date: date}
	cmd.apply = func(context.Context, posting.Tx) (posting.Result, error) { return posting.Result{}, err }
	return s.posting.Post(ctx, actor, key, cmd)
}
