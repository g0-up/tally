package debt

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Posting runs every money movement (payments, allocations, DN, BT, refunds).
	Posting *posting.Coordinator
	// Cash resolves payment methods and writes the fund entries of confirmed payments.
	Cash CashBook
	// Docs issues the PT/PC numbers of payments recorded inside another domain's posting and the
	// DS numbers of statements.
	Docs DocIssuer
	// Exceptions spends the owner's write-off confirmations.
	Exceptions ExceptionGuard
	// Onboarding is told when an opening balance or advance is entered; nil skips it.
	Onboarding Onboarding
	// Mailer sends email reminders to parties that consented; nil leaves reminders to the owner.
	Mailer mailer.Mailer
	// Consent tells whether a party accepts email reminders; nil treats every party as not
	// consenting, so no reminder is emailed.
	Consent ConsentChecker
}

// Service implements the debt use cases: postings through the coordinator, statements,
// collection notes and reminders, and the ledger queries. Other domains reach it through the
// recorders, the obligation reverser and the credit limit policy (Receivables, Payables,
// Payments, CreditNotes, Obligations, CreditLimit).
type Service struct {
	db         *db.DB
	clock      clock.Clock
	audit      *audit.Recorder
	posting    *posting.Coordinator
	cash       CashBook
	docs       DocIssuer
	exceptions ExceptionGuard
	onboarding Onboarding
	mailer     mailer.Mailer
	consent    ConsentChecker
	renderer   *mailer.Renderer
	outbox     *outbox.Publisher
	// idem makes the creates outside the coordinator (statements, collection notes) safe to retry
	// under an optional Idempotency-Key.
	idem *idempotency.Guard
}

// Idempotency scopes of the creates that run outside the posting coordinator.
const (
	ScopeStatementCreate = "statement.create"
	ScopeNoteCreate      = "collection_note.create"
)

// NewService returns the service.
func NewService(d Deps) *Service {
	return &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, posting: d.Posting, cash: d.Cash, docs: d.Docs,
		exceptions: d.Exceptions, onboarding: d.Onboarding, mailer: d.Mailer, consent: d.Consent, renderer: mailer.NewRenderer(reminderTemplates()),
		outbox: outbox.NewPublisher(d.Clock), idem: idempotency.NewGuard(d.Clock),
	}
}

// Scope is the tenant a request acts on; ReadOnly runs reads under the read-only role (support).
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

func (s *Service) paymentDeps() paymentDeps {
	return paymentDeps{cash: s.cash, onboarding: s.onboarding}
}

// today is the tenant's business date now.
func (s *Service) today(ctx context.Context, tenantID uuid.UUID) (time.Time, error) {
	var out time.Time
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) (err error) {
		out, err = tenantToday(ctx, q, tenantID, s.clock.Now())
		return err
	})
	return out, err
}

// writeCtx opens the write context of a non-posting tenant transaction (statements, notes).
func (s *Service) writeCtx(ctx context.Context, q db.Querier, actor audit.Actor) (writeCtx, error) {
	now := s.clock.Now()
	today, err := tenantToday(ctx, q, actor.TenantID, now)
	if err != nil {
		return writeCtx{}, err
	}
	return writeCtx{q: q, tenantID: actor.TenantID, now: now, by: actor.ID, date: today}, nil
}
