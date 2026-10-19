package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// SweepTime is the moment a sweep runs, with the tenant's time zone and business date at that
// moment.
type SweepTime struct {
	Now   time.Time
	Today time.Time
	Loc   *time.Location
}

// Producer reports every condition of its kinds that holds now in the tenant of q. The sweep
// raises what a producer returns and clears the notifications of its kinds it no longer returns,
// so a producer must return all current conditions, never a page of them.
type Producer interface {
	Kinds() []string
	Sweep(ctx context.Context, q db.Querier, at SweepTime) ([]Notice, error)
}

// ProducerRegistry holds the producers the sweep runs; a new kind of notification is a new
// producer registered here.
type ProducerRegistry struct {
	producers []Producer
	kinds     map[string]bool
}

// NewProducerRegistry returns a registry holding producers.
func NewProducerRegistry(producers ...Producer) *ProducerRegistry {
	r := &ProducerRegistry{kinds: map[string]bool{}}
	for _, p := range producers {
		r.Register(p)
	}
	return r
}

// Register adds p. Two producers of the same kind would clear each other's notifications, so a
// kind registered twice panics at start-up.
func (r *ProducerRegistry) Register(p Producer) {
	for _, k := range p.Kinds() {
		if r.kinds[k] {
			panic(fmt.Sprintf("notification: kind %q registered twice", k))
		}
		r.kinds[k] = true
	}
	r.producers = append(r.producers, p)
}

// Producers returns the registered producers in registration order.
func (r *ProducerRegistry) Producers() []Producer { return r.producers }

// DefaultProducers are the producers of every kind of notification.
func DefaultProducers() *ProducerRegistry {
	return NewProducerRegistry(
		ReceivableOverdue{}, PayableDueSoon{}, StockThresholds{}, LotExpiry{},
		POSSyncExceptions{}, DisputedStatements{}, SubscriptionStatus{}, ExportReady{},
	)
}
