package posting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/fault"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Ports the coordinator depends on (consumer-owned; the platform packages implement them).
type (
	// TxRunner opens the tenant transaction.
	TxRunner interface {
		WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q db.Querier) error) error
	}
	// IdempotencyGuard replays completed requests, claims keys and stores responses.
	IdempotencyGuard interface {
		Replay(ctx context.Context, q db.Querier, tenantID uuid.UUID, scope string, key idempotency.Key) (*idempotency.Stored, error)
		Begin(ctx context.Context, q db.Querier, tenantID uuid.UUID, scope string, key idempotency.Key) (*idempotency.Stored, error)
		Complete(ctx context.Context, q db.Querier, tenantID uuid.UUID, scope string, key idempotency.Key, status int, body json.RawMessage) error
	}
	// DocIssuer issues document numbers.
	DocIssuer interface {
		Next(ctx context.Context, q db.Querier, tenantID uuid.UUID, prefix string) (string, error)
	}
	// PeriodGuard rejects business dates inside a locked period.
	PeriodGuard interface {
		Check(ctx context.Context, q db.Querier, tenantID uuid.UUID, businessDate time.Time) error
	}
	// AuditRecorder writes audit events.
	AuditRecorder interface {
		Record(ctx context.Context, q db.Querier, actor audit.Actor, evt audit.Event) error
	}
	// OutboxPublisher writes outbox events.
	OutboxPublisher interface {
		Publish(ctx context.Context, q db.Querier, tenantID uuid.UUID, evt outbox.Event) (uuid.UUID, error)
	}
	// EntitlementGuard returns an error (403 subscription_read_only) when the tenant may not post.
	EntitlementGuard interface {
		CanPost(ctx context.Context, q db.Querier, tenantID uuid.UUID) error
	}
)

// Deps wires a Coordinator. Entitlements, Metrics and Faults may be nil (no subscription gate yet,
// no metrics, no injected failures: Faults is armed only by acceptance tests under APP_ENV=test).
type Deps struct {
	DB           TxRunner
	Idempotency  IdempotencyGuard
	Docs         DocIssuer
	Periods      PeriodGuard
	Audit        AuditRecorder
	Outbox       OutboxPublisher
	Entitlements EntitlementGuard
	Clock        clock.Clock
	Metrics      *metrics.Metrics
	Faults       *fault.Injector
}

// Coordinator runs posting commands.
type Coordinator struct {
	d      Deps
	locker *Locker
	// retryDelay returns the pause before the single deadlock retry.
	retryDelay func() time.Duration
}

// NewCoordinator validates deps.
func NewCoordinator(d Deps) (*Coordinator, error) {
	if d.DB == nil || d.Idempotency == nil || d.Docs == nil || d.Periods == nil || d.Audit == nil || d.Outbox == nil || d.Clock == nil {
		return nil, errors.New("posting: missing dependency")
	}
	return &Coordinator{
		d:      d,
		locker: NewLocker(),
		retryDelay: func() time.Duration {
			return 10*time.Millisecond + rand.N(40*time.Millisecond) //nolint:gosec // jitter, not security
		},
	}, nil
}

// RegisterLocker installs the row-lock callback of a lock kind (called by domain wiring).
func (c *Coordinator) RegisterLocker(kind string, fn LockFunc) { c.locker.Register(kind, fn) }

// Post runs cmd for actor under the idempotency key. A retry of a request that already completed
// replays its stored response before the entitlement and period guards run; a new request passes
// the guards before it claims the key. A deadlock or serialization failure is retried once after a
// short random pause.
func (c *Coordinator) Post(ctx context.Context, actor Actor, key idempotency.Key, cmd PostingCommand) (Response, error) {
	start := time.Now()
	resp, err := c.post(ctx, actor, key, cmd)
	if err != nil && db.IsRetryable(err) {
		select {
		case <-ctx.Done():
		case <-time.After(c.retryDelay()):
			resp, err = c.post(ctx, actor, key, cmd)
		}
	}
	outcome := "applied"
	switch {
	case err != nil && apperr.KindOf(err) != apperr.Internal:
		outcome = "rejected"
	case err != nil:
		outcome = "error"
	case resp.Replayed:
		outcome = "replayed"
	}
	c.d.Metrics.ObservePosting(cmd.Scope(), outcome, time.Since(start))
	return resp, err
}

func (c *Coordinator) post(ctx context.Context, actor Actor, key idempotency.Key, cmd PostingCommand) (Response, error) {
	if err := actor.Validate(); err != nil {
		return Response{}, err
	}
	if !key.Valid() {
		return Response{}, errors.New("posting: idempotency key is required")
	}
	scope := cmd.Scope()
	if scope == "" {
		return Response{}, errors.New("posting: command scope is empty")
	}
	tenantID := actor.TenantID

	var resp Response
	err := c.d.DB.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		resp = Response{}
		// A retry of a request that already committed gets its stored response even when the
		// period was locked or the subscription turned read-only after the commit: the client
		// must learn that the posting exists, or it would post it again.
		stored, err := c.d.Idempotency.Replay(ctx, q, tenantID, scope, key)
		if err != nil {
			return err
		}
		if stored != nil {
			resp = Response{Status: stored.Status, Body: stored.Body, Replayed: true}
			return nil
		}
		if c.d.Entitlements != nil {
			if err := c.d.Entitlements.CanPost(ctx, q, tenantID); err != nil {
				return err
			}
		}
		if err := c.d.Periods.Check(ctx, q, tenantID, cmd.BusinessDate()); err != nil {
			return err
		}
		// Begin also replays a same-key request that completed while this one ran its guards.
		if stored, err = c.d.Idempotency.Begin(ctx, q, tenantID, scope, key); err != nil {
			return err
		}
		if stored != nil {
			resp = Response{Status: stored.Status, Body: stored.Body, Replayed: true}
			return nil
		}

		tx := &postingTx{Querier: q, now: c.d.Clock.Now(), actor: actor, businessDate: cmd.BusinessDate()}
		if err := c.issueDocNos(ctx, q, cmd, tx); err != nil {
			return err
		}
		if err := c.locker.Acquire(ctx, q, cmd.LockKeys()); err != nil {
			return err
		}
		result, err := cmd.Apply(ctx, tx)
		if err != nil {
			return err
		}
		if left := tx.untakenDocNos(); len(left) > 0 {
			return fmt.Errorf("posting %s: Apply did not take the reserved document numbers %v", scope, left)
		}
		for _, evt := range tx.audits {
			if err := c.d.Audit.Record(ctx, q, actor, evt); err != nil {
				return err
			}
		}
		for _, evt := range tx.events {
			if _, err := c.d.Outbox.Publish(ctx, q, tenantID, evt); err != nil {
				return err
			}
		}

		status := result.Status
		if status == 0 {
			status = http.StatusCreated
		}
		if status < 200 || status > 299 {
			return fmt.Errorf("posting %s: Apply returned non-2xx status %d without an error", scope, status)
		}
		var body json.RawMessage
		if result.Body != nil {
			if body, err = json.Marshal(result.Body); err != nil {
				return fmt.Errorf("posting %s: marshal response: %w", scope, err)
			}
		}
		if err := c.d.Idempotency.Complete(ctx, q, tenantID, scope, key, status, body); err != nil {
			return err
		}
		if err := c.d.Faults.Hit(fault.PostingBeforeCommit, scope); err != nil {
			return err
		}
		resp = Response{Status: status, Body: body, DocNo: tx.docNo}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	return resp, nil
}

// issueDocNos issues the command's own number and those of its ExtraDocs, before any row lock and
// prefixes in ascending order; numbers of one prefix are consecutive, the command's own first.
func (c *Coordinator) issueDocNos(ctx context.Context, q db.Querier, cmd PostingCommand, tx *postingTx) error {
	own := cmd.DocPrefix()
	counts := map[string]int{}
	if own != "" {
		counts[own]++
	}
	if e, ok := cmd.(ExtraDocs); ok {
		for _, p := range e.ExtraDocPrefixes() {
			if p == "" {
				return fmt.Errorf("posting %s: empty extra document prefix", cmd.Scope())
			}
			counts[p]++
		}
	}
	for _, prefix := range slices.Sorted(maps.Keys(counts)) {
		for i := range counts[prefix] {
			no, err := c.d.Docs.Next(ctx, q, tx.TenantID(), prefix)
			if err != nil {
				return err
			}
			if prefix == own && i == 0 {
				tx.docNo = no
				continue
			}
			if tx.extraDocNos == nil {
				tx.extraDocNos = map[string][]string{}
			}
			tx.extraDocNos[prefix] = append(tx.extraDocNos[prefix], no)
		}
	}
	return nil
}
