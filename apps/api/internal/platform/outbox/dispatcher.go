package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
)

// Delivery limits.
const (
	BatchSize   = 100
	MaxAttempts = 10
	baseBackoff = 5 * time.Second
	maxBackoff  = time.Hour
	maxErrorLen = 1000
)

// Message is a stored event handed to handlers.
type Message struct {
	ID        uuid.UUID       `db:"id"`
	TenantID  uuid.UUID       `db:"tenant_id"`
	Topic     string          `db:"topic"`
	Payload   json.RawMessage `db:"payload"`
	Attempts  int             `db:"attempts"`
	CreatedAt time.Time       `db:"created_at"`
}

// Handler processes one message inside the transaction that marks it processed (one transaction
// per message); its writes commit together with the processed mark. It may run more than once
// for a message (at-least-once), so external side effects must tolerate a repeat.
type Handler func(ctx context.Context, q db.Querier, msg Message) error

// Dispatcher delivers pending events tenant by tenant.
type Dispatcher struct {
	db      *db.DB
	clock   clock.Clock
	metrics *metrics.Metrics
	logger  *slog.Logger

	mu       sync.RWMutex
	handlers map[string][]Handler
}

// NewDispatcher builds a dispatcher. metrics may be nil.
func NewDispatcher(d *db.DB, clk clock.Clock, m *metrics.Metrics, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{db: d, clock: clk, metrics: m, logger: logger, handlers: map[string][]Handler{}}
}

// Subscribe adds h for topic. Handlers of a topic run in subscription order.
func (d *Dispatcher) Subscribe(topic string, h Handler) {
	if !ValidTopic(topic) {
		panic(fmt.Sprintf("outbox: invalid topic %q", topic))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[topic] = append(d.handlers[topic], h)
}

func (d *Dispatcher) handlersFor(topic string) []Handler {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.handlers[topic]
}

// Backoff is the delay before attempt n+1 after n failed attempts: 5 s doubling, capped at 1 h.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 20 {
		return maxBackoff
	}
	delay := baseBackoff << (attempts - 1)
	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}

// maxIdleInterval caps how far Run stretches its pause while there is nothing to deliver.
const maxIdleInterval = 10 * time.Second

// Run dispatches until ctx is cancelled. It pauses interval after a round that delivered
// something; after an idle round the pause doubles up to maxIdleInterval (never below interval),
// because every round costs one transaction per tenant. Errors are logged, never fatal.
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) error {
	pause := interval
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		n, err := d.DispatchOnce(ctx)
		if err != nil && ctx.Err() == nil {
			d.logger.Error("outbox dispatch failed", "error", err.Error())
		}
		pause = nextPause(pause, interval, n > 0)
		timer.Reset(pause)
	}
}

// nextPause is interval after a busy round, else the previous pause doubled and capped at
// max(interval, maxIdleInterval).
func nextPause(prev, interval time.Duration, busy bool) time.Duration {
	if busy || prev < interval {
		return interval
	}
	limit := max(interval, maxIdleInterval)
	return min(prev*2, limit)
}

// DispatchOnce delivers up to BatchSize due events per active tenant and returns how many were
// handled (successfully or not). A replaced sandbox is not visited: its reset closed the events it
// had, and one a request still in flight wrote afterwards stays pending, never handled. A failing
// tenant does not stop the others; every failure is returned joined. It also refreshes the outbox
// lag gauge.
func (d *Dispatcher) DispatchOnce(ctx context.Context) (int, error) {
	tenants, err := d.db.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	var oldest *time.Time
	var errs []error
	for _, tenantID := range tenants {
		if ctx.Err() != nil {
			break
		}
		n, pendingSince, err := d.dispatchTenant(ctx, tenantID)
		total += n
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", tenantID, err))
		}
		if pendingSince != nil && (oldest == nil || pendingSince.Before(*oldest)) {
			oldest = pendingSince
		}
	}
	lag := time.Duration(0)
	if oldest != nil {
		lag = max(d.clock.Now().Sub(*oldest), 0)
	}
	d.metrics.SetOutboxLag(lag)
	return total, errors.Join(errs...)
}

// dispatchTenant delivers the due events of one tenant, each in its own transaction: the
// handlers' side effects (an email) and the processed mark commit together, so a failed commit
// repeats only that one event, never the ones already delivered. It returns the delivery count
// and the available_at of the oldest event still due (lag), or nil when none is left.
func (d *Dispatcher) dispatchTenant(ctx context.Context, tenantID uuid.UUID) (int, *time.Time, error) {
	handled := 0
	for handled < BatchSize {
		var drained bool
		var pendingSince *time.Time
		err := d.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
			now := d.clock.Now()
			var msg Message
			err := q.QueryRowxContext(ctx, `
				SELECT id, tenant_id, topic, payload, attempts, created_at
				FROM outbox_events
				WHERE processed_at IS NULL AND attempts < $1 AND available_at <= $2
				ORDER BY available_at, id
				LIMIT 1
				FOR UPDATE SKIP LOCKED`, MaxAttempts, now).StructScan(&msg)
			if db.IsNoRows(err) {
				// Nothing claimable; events locked by a concurrent dispatcher still count as lag.
				drained = true
				var oldest error
				pendingSince, oldest = d.oldestDue(ctx, q, now)
				return oldest
			}
			if err != nil {
				return fmt.Errorf("select pending event: %w", err)
			}
			return d.deliver(ctx, q, msg)
		})
		if err != nil {
			return handled, nil, err
		}
		if drained {
			return handled, pendingSince, nil
		}
		handled++
	}
	// Batch limit reached: the rest waits for the next round, measured as lag.
	var pendingSince *time.Time
	err := d.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var err error
		pendingSince, err = d.oldestDue(ctx, q, d.clock.Now())
		return err
	})
	return handled, pendingSince, err
}

// oldestDue returns the available_at of the oldest undelivered event that is due at now. Events
// deferred into the future are not late, so they never count as lag.
func (d *Dispatcher) oldestDue(ctx context.Context, q db.Querier, now time.Time) (*time.Time, error) {
	var since *time.Time
	if err := q.QueryRowxContext(ctx, `
		SELECT min(available_at) FROM outbox_events
		WHERE processed_at IS NULL AND attempts < $1 AND available_at <= $2`, MaxAttempts, now).Scan(&since); err != nil {
		return nil, fmt.Errorf("outbox lag: %w", err)
	}
	return since, nil
}

// deliver runs the handlers of msg inside a savepoint: a failing handler rolls back only its own
// writes, and the failure is recorded on the event.
func (d *Dispatcher) deliver(ctx context.Context, q db.Querier, msg Message) error {
	if _, err := q.ExecContext(ctx, `SAVEPOINT outbox_event`); err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	handlerErr := d.runHandlers(ctx, q, msg)
	now := d.clock.Now()
	if handlerErr == nil {
		if _, err := q.ExecContext(ctx, `RELEASE SAVEPOINT outbox_event`); err != nil {
			return fmt.Errorf("release savepoint: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE outbox_events SET processed_at = $2, attempts = attempts + 1, last_error = NULL
			WHERE id = $1`, msg.ID, now); err != nil {
			return fmt.Errorf("mark event processed: %w", err)
		}
		d.metrics.CountOutbox(msg.Topic, "processed")
		return nil
	}

	if _, err := q.ExecContext(ctx, `ROLLBACK TO SAVEPOINT outbox_event`); err != nil {
		return fmt.Errorf("rollback to savepoint: %w", err)
	}
	attempts := msg.Attempts + 1
	outcome := "retry"
	if attempts >= MaxAttempts {
		outcome = "dead"
	}
	msgText := handlerErr.Error()
	if len(msgText) > maxErrorLen {
		msgText = msgText[:maxErrorLen]
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE outbox_events SET attempts = $2, last_error = $3, available_at = $4
		WHERE id = $1`, msg.ID, attempts, msgText, now.Add(Backoff(attempts))); err != nil {
		return fmt.Errorf("record event failure: %w", err)
	}
	d.metrics.CountOutbox(msg.Topic, outcome)
	level := slog.LevelWarn
	if outcome == "dead" {
		level = slog.LevelError
	}
	d.logger.Log(ctx, level, "outbox handler failed",
		"event_id", msg.ID.String(), "tenant_id", msg.TenantID.String(), "topic", msg.Topic,
		"attempts", attempts, "outcome", outcome, "error", msgText)
	return nil
}

func (d *Dispatcher) runHandlers(ctx context.Context, q db.Querier, msg Message) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	for _, h := range d.handlersFor(msg.Topic) {
		if err := h(ctx, q, msg); err != nil {
			return err
		}
	}
	return nil
}
