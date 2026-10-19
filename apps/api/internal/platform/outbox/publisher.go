// Package outbox implements the transactional outbox: Publisher writes events in the same
// transaction as the change that caused them, Dispatcher (run by `tally worker`) delivers them
// to subscribed handlers at least once. Handlers dedupe on Message.ID.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

var topicPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// ValidTopic reports a dotted lower-case topic such as "stock.changed".
func ValidTopic(topic string) bool { return topicPattern.MatchString(topic) }

// Event is a fact to deliver after commit.
type Event struct {
	Topic   string
	Payload any
	// Delay postpones the first delivery attempt (zero delivers as soon as possible).
	Delay time.Duration
}

// Publisher writes outbox rows through the caller's querier.
type Publisher struct {
	clock clock.Clock
}

// NewPublisher returns a publisher stamping rows with clk.
func NewPublisher(clk clock.Clock) *Publisher { return &Publisher{clock: clk} }

// Publish appends evt for tenantID and returns the event id.
func (p *Publisher) Publish(ctx context.Context, q db.Querier, tenantID uuid.UUID, evt Event) (uuid.UUID, error) {
	if !ValidTopic(evt.Topic) {
		return uuid.Nil, fmt.Errorf("outbox: invalid topic %q", evt.Topic)
	}
	if evt.Delay < 0 {
		return uuid.Nil, fmt.Errorf("outbox: negative delay for %s", evt.Topic)
	}
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("outbox: payload of %s: %w", evt.Topic, err)
	}
	if string(payload) == "null" {
		payload = []byte("{}")
	}
	id := ids.New()
	now := p.clock.Now()
	if _, err := q.ExecContext(ctx, `
		INSERT INTO outbox_events (id, tenant_id, topic, payload, created_at, available_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, tenantID, evt.Topic, payload, now, now.Add(evt.Delay)); err != nil {
		return uuid.Nil, fmt.Errorf("outbox: publish %s: %w", evt.Topic, err)
	}
	return id, nil
}
