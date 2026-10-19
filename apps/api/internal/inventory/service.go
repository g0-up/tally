package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Posting runs every stock posting (adjustments, count posts, recost applies).
	Posting *posting.Coordinator
	// Exceptions spends the owner's disposal, quarantine release and count variance
	// confirmations; nil uses exception.NewGuard(Clock).
	Exceptions *exception.Guard
	// Prices gives the sale price the stock list shows; nil shows no price.
	Prices PriceHints
}

// Service implements the inventory use cases: stock postings through the coordinator, manual
// reservations, stock counts, recost runs, the stock queries and the worker jobs. Other domains
// change stock through Ledger and Reservations inside their own postings.
type Service struct {
	db           *db.DB
	clock        clock.Clock
	audit        *audit.Recorder
	posting      *posting.Coordinator
	exceptions   *exception.Guard
	prices       PriceHints
	ledger       *StockLedger
	reservations *ReservationService
	docs         *docseq.Issuer
	outbox       *outbox.Publisher
}

// NewService returns the service.
func NewService(d Deps) *Service {
	exceptions := d.Exceptions
	if exceptions == nil {
		exceptions = exception.NewGuard(d.Clock)
	}
	return &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, posting: d.Posting, exceptions: exceptions, prices: d.Prices,
		ledger: NewStockLedger(), reservations: NewReservationService(), docs: docseq.NewIssuer(),
		outbox: outbox.NewPublisher(d.Clock),
	}
}

// Ledger is the stock ledger other domains post through.
func (s *Service) Ledger() *StockLedger { return s.ledger }

// Reservations is the reservation service other domains reserve through.
func (s *Service) Reservations() *ReservationService { return s.reservations }

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// read runs fn in a tenant transaction suited to scope.
func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// record appends an audit event.
func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, action, entity string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: id, Before: before, After: after})
}

// today is the tenant's business date now. It reads the tenant's time zone in a normal tenant
// transaction: the read-only role may not read tenants.
func (s *Service) today(ctx context.Context, tenantID uuid.UUID) (time.Time, error) {
	var out time.Time
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) (err error) {
		out, err = tenantToday(ctx, q, tenantID, s.clock.Now())
		return err
	})
	return out, err
}

// jobTx is a Tx for work outside the posting coordinator: events are buffered and published
// by flush in the same transaction.
type jobTx struct {
	db.Querier
	events []outbox.Event
}

// Publish implements Tx.
func (t *jobTx) Publish(evt outbox.Event) { t.events = append(t.events, evt) }

// flush writes the buffered events to the outbox.
func (s *Service) flush(ctx context.Context, tx *jobTx, tenantID uuid.UUID) error {
	for _, evt := range tx.events {
		if _, err := s.outbox.Publish(ctx, tx.Querier, tenantID, evt); err != nil {
			return err
		}
	}
	tx.events = nil
	return nil
}

// workerActor is the system actor of the worker jobs.
func workerActor(tenantID uuid.UUID) audit.Actor {
	return audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: audit.SourceWorker}
}

// HandleRecostSuggested is the outbox handler of TopicRecostSuggested: it builds (or widens) the
// tenant's open recost preview in the dispatcher's tenant transaction.
func (s *Service) HandleRecostSuggested(ctx context.Context, q db.Querier, msg outbox.Message) error {
	var sug recostSuggestion
	if err := json.Unmarshal(msg.Payload, &sug); err != nil {
		return fmt.Errorf("inventory: decode recost suggestion: %w", err)
	}
	from, err := time.Parse(time.DateOnly, sug.FromDate)
	if err != nil {
		return fmt.Errorf("inventory: recost suggestion date %q: %w", sug.FromDate, err)
	}
	tx := &jobTx{Querier: q}
	run, err := s.ledger.recost.preview(ctx, tx, msg.TenantID, sug.RequestedBy, sug.SkuIDs,
		clock.Date(from.Year(), from.Month(), from.Day()), s.clock.Now())
	if err != nil {
		return err
	}
	if run != nil {
		if err := s.record(ctx, q, workerActor(msg.TenantID), actionRecostPreview, "cost_recalculation", run.ID, nil,
			map[string]any{"sku_ids": run.SkuIDs, "from_date": run.FromDate.Format(time.DateOnly), "source_doc_type": sug.DocType,
				"source_doc_id": sug.DocID, "impact": run.Impact}); err != nil {
			return err
		}
	}
	return s.flush(ctx, tx, msg.TenantID)
}

// RollExpiry moves the stock of lots that expired by the tenant's business date out of
// sellable (see ExpiryRoller). It returns how many SKUs changed.
func (s *Service) RollExpiry(ctx context.Context, tenantID uuid.UUID) (int, error) {
	var n int
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		now := s.clock.Now()
		today, err := tenantToday(ctx, q, tenantID, now)
		if err != nil {
			return err
		}
		tx := &jobTx{Querier: q}
		if n, err = (ExpiryRoller{}).Roll(ctx, tx, now, today); err != nil {
			return err
		}
		return s.flush(ctx, tx, tenantID)
	})
	return n, err
}

// DetectSignals runs the daily stock signal scan of a tenant. It returns how many signals it
// published.
func (s *Service) DetectSignals(ctx context.Context, tenantID uuid.UUID) (int, error) {
	var n int
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		now := s.clock.Now()
		today, err := tenantToday(ctx, q, tenantID, now)
		if err != nil {
			return err
		}
		tx := &jobTx{Querier: q}
		if n, err = (StockSignalDetector{}).Detect(ctx, tx, now, today); err != nil {
			return err
		}
		return s.flush(ctx, tx, tenantID)
	})
	return n, err
}
