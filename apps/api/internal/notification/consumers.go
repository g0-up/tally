package notification

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Outbox topics the notification centre consumes. They are published by inventory (stock and
// lot), debt (statement and every change to a party's open obligations), sales (the offline sync
// exception queue) and reporting (export).
const (
	TopicStockChanged      = "stock.changed"
	TopicStockLow          = "stock.low"
	TopicStockOver         = "stock.over"
	TopicLotExpiring       = "lot.expiring"
	TopicLotExpired        = "lot.expired"
	TopicStatementDisputed = "statement.disputed"
	TopicExportFinished    = "export.finished"
	TopicSyncException     = "pos_sync.exception"
)

// obligationTopics are the debt events that create, settle, release or reverse a party's
// obligations, each naming the party: what moves its overdue and due-soon notices.
var obligationTopics = []string{
	"obligation.created", "obligation.reversed", "payment.posted", "payment.confirmed", "payment.reversed",
	"allocation.posted", "debt_adjustment.posted", "debt_offset.posted", "credit_note.recorded",
}

// Handlers are the outbox consumers by topic. Each re-reads the current state of the event's
// subject and reconciles its notifications, so a redelivered or out-of-order event converges to
// the same rows.
func (s *Service) Handlers() map[string]outbox.Handler {
	stock := s.onSubject("sku_id", func(ctx context.Context, q db.Querier, at SweepTime, id uuid.UUID) ([]string, []Notice, error) {
		notices, err := stockNotices(ctx, q, []uuid.UUID{id})
		if err != nil {
			return nil, nil, err
		}
		lotKeys, lots, err := skuLotNotices(ctx, q, at, id)
		return append([]string{key(KindBelowMinStock, id), key(KindSurplusStock, id)}, lotKeys...), append(notices, lots...), err
	})
	lot := s.onSubject("lot_id", func(ctx context.Context, q db.Querier, at SweepTime, id uuid.UUID) ([]string, []Notice, error) {
		notices, err := lotNotices(ctx, q, at, &id, nil)
		return []string{key(KindLotExpiring, id), key(KindLotExpired, id)}, notices, err
	})
	dues := s.onSubject("party_id", func(ctx context.Context, q db.Querier, at SweepTime, id uuid.UUID) ([]string, []Notice, error) {
		overdue, err := overdueReceivables(ctx, q, at, &id)
		if err != nil {
			return nil, nil, err
		}
		dueSoon, err := payablesDueSoon(ctx, q, at, &id)
		return []string{key(KindReceivableOverdue, id), key(KindPayableDueSoon, id)}, append(overdue, dueSoon...), err
	})
	handlers := map[string]outbox.Handler{
		TopicStockChanged: stock,
		TopicStockLow:     stock,
		TopicStockOver:    stock,
		TopicLotExpiring:  lot,
		TopicLotExpired:   lot,
		TopicStatementDisputed: s.onSubject("statement_id", func(ctx context.Context, q db.Querier, _ SweepTime, id uuid.UUID) ([]string, []Notice, error) {
			notices, err := disputedStatements(ctx, q, &id)
			return []string{key(KindStatementDisputed, id)}, notices, err
		}),
		TopicExportFinished: s.onSubject("export_job_id", func(ctx context.Context, q db.Querier, at SweepTime, id uuid.UUID) ([]string, []Notice, error) {
			notices, err := readyExports(ctx, q, at, &id)
			return []string{key(KindExportReady, id)}, notices, err
		}),
		TopicSyncException: s.onSubject("pos_sync_exception_id", func(ctx context.Context, q db.Querier, _ SweepTime, id uuid.UUID) ([]string, []Notice, error) {
			notices, err := openSyncExceptions(ctx, q, &id)
			return []string{key(KindPOSSyncException, id)}, notices, err
		}),
	}
	for _, topic := range obligationTopics {
		handlers[topic] = dues
	}
	return handlers
}

// subjectNotices returns the dedupe keys a subject can hold and the notices that hold now.
type subjectNotices func(ctx context.Context, q db.Querier, at SweepTime, id uuid.UUID) ([]string, []Notice, error)

// onSubject is a handler reading the subject id from the payload field and reconciling the
// subject's notices. A null subject (a payment of a walk-in customer) has none.
func (s *Service) onSubject(field string, fn subjectNotices) outbox.Handler {
	return func(ctx context.Context, q db.Querier, msg outbox.Message) error {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return fmt.Errorf("notification: decode %s %s: %w", msg.Topic, msg.ID, err)
		}
		var id uuid.UUID
		raw, ok := payload[field]
		if !ok {
			return fmt.Errorf("notification: %s %s has no %s", msg.Topic, msg.ID, field)
		}
		if string(raw) == "null" {
			return nil
		}
		if err := json.Unmarshal(raw, &id); err != nil {
			return fmt.Errorf("notification: %s %s: %s: %w", msg.Topic, msg.ID, field, err)
		}
		at, err := sweepTime(ctx, q, s.clock.Now())
		if err != nil {
			return err
		}
		candidates, notices, err := fn(ctx, q, at, id)
		if err != nil {
			return err
		}
		return reconcile(ctx, q, at.Now, candidates, notices)
	}
}
