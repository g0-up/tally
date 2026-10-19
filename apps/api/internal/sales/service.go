package sales

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Posting scopes and audit actions of the sales documents. A posting scope doubles as the audit
// action of the document it writes.
const (
	ActionQuoteCreate  = "quote.create"
	ActionQuoteUpdate  = "quote.update"
	ActionQuoteRevise  = "quote.revise"
	ActionQuoteDelete  = "quote.delete"
	ActionQuoteSend    = "quote.send"
	ActionQuoteAccept  = "quote.accept"
	ActionQuoteReject  = "quote.reject"
	ActionQuoteConvert = "quote.convert"

	ActionInvoiceCreate = "invoice.create"
	ActionInvoiceUpdate = "invoice.update"
	ActionInvoiceDelete = "invoice.delete"

	ScopeInvoicePay         = "invoice.pay"
	ScopeInvoiceConfirm     = "invoice.confirm"
	ScopeInvoiceRevise      = "invoice.revise"
	ScopeInvoiceReserveMore = "invoice.reserve_more"
	ScopeInvoiceCancel      = "invoice.cancel_remainder"
	ScopeStockOutPost       = "stock_out.post"
	ScopeStockOutReverse    = "stock_out.reverse"
	ScopePOSCheckout        = "pos.checkout"
	ScopePOSOfflineSale     = "pos.offline_sale"
	ScopeReturnPost         = "sales_return.post"
	ScopeExchangePost       = "exchange.post"

	ActionDocumentPost    = "sales_document.post"
	ActionDocumentReverse = "sales_document.reverse"

	ActionCartCreate     = "pos_cart.create"
	ActionCartUpdate     = "pos_cart.update"
	ActionCartDelete     = "pos_cart.delete"
	ActionShiftOpen      = "shift.open"
	ActionShiftClose     = "shift.close"
	ActionSyncException  = "pos_sync_exception.create"
	ActionSyncDiscard    = "pos_sync_exception.discard"
	ActionSyncResolve    = "pos_sync_exception.resolve"
	ActionOfflineSession = "pos_session.grant_offline"
)

// Outbox topics of sales.
const (
	TopicSalePosted     = "sale.posted"
	TopicSaleReversed   = "sale.reversed"
	TopicReturnPosted   = "sales_return.posted"
	TopicExchangePosted = "exchange.posted"
	TopicInvoiceConfirm = "invoice.confirmed"
	// TopicSyncException is published when an offline sale is parked in the sync exception queue
	// and when its exception is resolved or discarded.
	TopicSyncException = "pos_sync.exception"
)

// DocIssuer issues document numbers outside postings (BG, SO, GH, CA).
type DocIssuer interface {
	Next(ctx context.Context, q db.Querier, tenantID uuid.UUID, prefix string) (string, error)
}

// ExceptionGuard spends the owner's exception confirmations (exception.Guard).
type ExceptionGuard interface {
	Consume(ctx context.Context, q db.Querier, tenantID uuid.UUID, want exception.Expectation) error
}

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Posting runs every document that moves stock or money.
	Posting    *posting.Coordinator
	Docs       DocIssuer
	Exceptions ExceptionGuard
	Pricer     Pricer
	Stock      Stock
	Debt       Debt
	Methods    PaymentMethods
	// Combos expands combo SKUs; nil rejects every combo.
	Combos ComboExpander
	// Campaigns attributes sales to a campaign; nil attributes none.
	Campaigns CampaignAttributor
	Sessions  OfflineSessions
	// Loyalty spends loyalty points at checkout; nil refuses every redemption.
	Loyalty Loyalty
}

// Service implements quotes, invoices, stock outs, the POS, returns and exchanges.
type Service struct {
	db         *db.DB
	clock      clock.Clock
	audit      *audit.Recorder
	posting    *posting.Coordinator
	docs       DocIssuer
	exceptions ExceptionGuard
	pricer     Pricer
	stock      Stock
	debt       Debt
	methods    PaymentMethods
	combos     ComboExpander
	campaigns  CampaignAttributor
	sessions   OfflineSessions
	loyalty    Loyalty
	outbox     *outbox.Publisher
	idem       *idempotency.Guard
}

// NewService returns the service.
func NewService(d Deps) *Service {
	s := &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, posting: d.Posting, docs: d.Docs, exceptions: d.Exceptions,
		pricer: d.Pricer, stock: d.Stock, debt: d.Debt, methods: d.Methods, combos: d.Combos, campaigns: d.Campaigns,
		sessions: d.Sessions, loyalty: d.Loyalty, outbox: outbox.NewPublisher(d.Clock), idem: idempotency.NewGuard(d.Clock),
	}
	if s.loyalty == nil {
		s.loyalty = NoLoyalty{}
	}
	if s.combos == nil {
		s.combos = NoCombos{}
	}
	if s.campaigns == nil {
		s.campaigns = NoCampaigns{}
	}
	if s.docs == nil {
		s.docs = docseq.NewIssuer()
	}
	return s
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

// readOnlyZone dates reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// today is the tenant's business date now, read in the caller's tenant transaction.
func (s *Service) today(ctx context.Context, q db.Querier, tenantID uuid.UUID) (time.Time, error) {
	return s.dateAt(ctx, q, tenantID, s.clock.Now())
}

// dateAt is the tenant's business date at t.
func (s *Service) dateAt(ctx context.Context, q db.Querier, tenantID uuid.UUID, t time.Time) (time.Time, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return time.Time{}, fmt.Errorf("sales: read tenant privileges: %w", err)
	}
	name, err := db.ReadOnlyTimezone(ctx, q, readOnlyZone)
	if err != nil {
		return time.Time{}, err
	}
	if readsTenant {
		if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = $1`, tenantID).Scan(&name); err != nil {
			return time.Time{}, fmt.Errorf("sales: read tenant time zone: %w", err)
		}
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(t, loc), nil
}

// todayTx is the tenant's business date now in its own transaction, for commands that need the
// date before the posting starts.
func (s *Service) todayTx(ctx context.Context, tenantID uuid.UUID) (time.Time, error) {
	var out time.Time
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) (err error) {
		out, err = s.today(ctx, q, tenantID)
		return err
	})
	return out, err
}

// settings are the tenant settings sales reads.
type settings struct {
	PricesIncludeTax bool        `db:"prices_include_tax"`
	PaymentTermsDays int         `db:"default_payment_terms_days"`
	OfflineEnabled   bool        `db:"offline_pos_enabled"`
	OfflineMaxAmount money.Money `db:"offline_pos_max_amount"`
	OfflineMaxHours  int         `db:"offline_pos_max_hours"`
}

func loadSettings(ctx context.Context, q db.Querier) (settings, error) {
	var st settings
	err := q.QueryRowxContext(ctx, `
		SELECT prices_include_tax, default_payment_terms_days, offline_pos_enabled, offline_pos_max_amount, offline_pos_max_hours
		FROM tenant_settings`).StructScan(&st)
	if err != nil {
		return settings{}, fmt.Errorf("sales: read tenant settings: %w", err)
	}
	return st, nil
}

// warehouseID is the store's single warehouse.
func warehouseID(ctx context.Context, q db.Querier) (uuid.UUID, error) {
	var id uuid.UUID
	if err := q.QueryRowxContext(ctx, `SELECT id FROM warehouses ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("sales: read warehouse: %w", err)
	}
	return id, nil
}

// record writes the audit row of a change made outside a posting.
func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, evt audit.Event) error {
	return s.audit.Record(ctx, q, actor, evt)
}

// ConfirmationsField is the request field listing the owner's exception confirmations; it is left
// out of the payload hash a confirmation binds.
const ConfirmationsField = "exception_confirmation_ids"

// PayloadHash is the hash an exception confirmation of a sales command binds: the canonical hash
// of the request body without its confirmation ids.
func PayloadHash(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, apperr.NewValidation("invalid_payload", "The payload must be a JSON document", nil)
	}
	if obj, ok := v.(map[string]any); ok {
		delete(obj, ConfirmationsField)
		stripped, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("sales: canonical payload: %w", err)
		}
		body = stripped
	}
	return exception.PayloadHash(body)
}

// confirmations carries what an exception check needs: the confirmation ids the client sent, the
// document they bind and the hash of the request.
type confirmations struct {
	IDs        []uuid.UUID
	DocType    string
	DocID      uuid.UUID
	DocVersion int
	Hash       []byte
}

// spend consumes one of the given confirmations for kind on behalf of the used-by document. The
// first confirmation that matches is spent; with none matching the owner is asked (exception
// required). It runs inside the posting, so a failed posting gives the confirmation back.
func (s *Service) spend(ctx context.Context, q db.Querier, tenantID uuid.UUID, c confirmations, kind exception.Kind,
	usedByType string, usedByID uuid.UUID,
) error {
	required := apperr.NewExceptionRequired(string(kind), "This action needs the owner's confirmation")
	for _, id := range c.IDs {
		err := s.exceptions.Consume(ctx, q, tenantID, exception.Expectation{
			ConfirmationID: id, Kind: kind, DocType: c.DocType, DocID: c.DocID, DocVersion: c.DocVersion,
			PayloadHash: c.Hash, UsedByDocType: usedByType, UsedByDocID: usedByID,
		})
		if err == nil {
			return nil
		}
		if apperr.KindOf(err) != apperr.ExceptionRequired {
			return err
		}
	}
	return required
}
