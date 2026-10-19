package sales

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Limits of an offline sync batch.
const (
	maxOfflineBatch    = 100
	maxOfflineClientID = 100
	// offlineClockSkew is how far ahead of the server a device clock may run.
	offlineClockSkew = 5 * time.Minute
)

// Statuses of the sales of an offline sync batch.
const (
	SyncPosted   = "posted"
	SyncReplayed = "replayed"
	SyncParked   = "exception"
	SyncFailed   = "failed"
)

// Statuses of a sync exception.
const (
	ExceptionOpen      = "open"
	ExceptionResolved  = "resolved"
	ExceptionDiscarded = "discarded"
)

// OfflineSale is a sale a POS device made without a connection. ClientID is the device's id of the
// sale: the server posts each one once, however many times it is sent.
type OfflineSale struct {
	ClientID  string    `json:"offline_client_id"`
	CreatedAt time.Time `json:"created_at"`
	CheckoutInput
}

// OfflineSyncInput is a batch of offline sales, in the order the device made them.
type OfflineSyncInput struct {
	Sales []OfflineSale `json:"sales"`
}

// OfflineSyncItem is what became of one offline sale: posted (now or by an earlier sync), parked
// in the exception queue, or failed for a reason the device should send it again for.
type OfflineSyncItem struct {
	ClientID        string     `json:"offline_client_id"`
	Status          string     `json:"status"`
	SalesDocumentID *uuid.UUID `json:"sales_document_id,omitempty"`
	DocNo           string     `json:"doc_no,omitempty"`
	ExceptionID     *uuid.UUID `json:"sync_exception_id,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorDetail     string     `json:"error_detail,omitempty"`
}

// OfflineSyncResult lists what became of each sale of a batch, in its order.
type OfflineSyncResult struct {
	Items []OfflineSyncItem `json:"items"`
}

// OfflineSession is the offline window granted to a login session.
type OfflineSession struct {
	OfflineUntil time.Time `json:"offline_until"`
}

// ResolveInput resolves a sync exception by posting the corrected sale, paid in any way, today.
type ResolveInput struct {
	CheckoutInput
}

// SyncExceptionFilter narrows the exception queue.
type SyncExceptionFilter struct {
	Status string
	Page   ListPage
}

func validateOfflineBatch(in OfflineSyncInput) error {
	fields := map[string]string{}
	switch {
	case len(in.Sales) == 0:
		fields["sales"] = "required"
	case len(in.Sales) > maxOfflineBatch:
		fields["sales"] = "too_many"
	}
	seen := map[string]bool{}
	for i, sl := range in.Sales {
		field := "sales." + strconv.Itoa(i) + ".offline_client_id"
		switch {
		case sl.ClientID == "":
			fields[field] = "required"
		case len(sl.ClientID) > maxOfflineClientID:
			fields[field] = "too_long"
		case seen[sl.ClientID]:
			fields[field] = "duplicate"
		}
		seen[sl.ClientID] = true
		if sl.CreatedAt.IsZero() {
			fields["sales."+strconv.Itoa(i)+".created_at"] = "required"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// offlineWindow tells why an offline sale made at createdAt can no longer be posted as one, ""
// when it can: offline selling must be on, the session must hold an offline window covering the
// sale, and the sale must be at most maxHours old.
func offlineWindow(enabled bool, maxHours int, until *time.Time, createdAt, now time.Time) string {
	switch {
	case !enabled:
		return CodeOfflineDisabled
	case until == nil, createdAt.After(*until), createdAt.After(now.Add(offlineClockSkew)),
		now.Sub(createdAt) > time.Duration(maxHours)*time.Hour:
		return CodeOfflineExpired
	}
	return ""
}

// checkOfflineTotal enforces what an offline sale may be: paid in full (no credit) and at most
// the tenant's offline cap.
func checkOfflineTotal(total, paid, maxAmount money.Money) error {
	if !paid.Equal(total) {
		return apperr.NewValidation(CodeOfflineNotAllowed, "An offline sale is paid in full in cash; credit needs the server",
			map[string]string{"payments": CodeOfflineNotAllowed})
	}
	if total.Cmp(maxAmount) > 0 {
		return apperr.NewConflict(CodeOfflineAmount, "The sale is over the most an offline sale may be: "+maxAmount.String())
	}
	return nil
}

// GrantOfflineSession opens an offline window on the caller's session for as long as the tenant
// allows offline selling, capped at the session's expiry.
func (s *Service) GrantOfflineSession(ctx context.Context, actor audit.Actor, sessionID uuid.UUID) (OfflineSession, error) {
	if sessionID == uuid.Nil {
		return OfflineSession{}, apperr.NewValidation(CodeOfflineDisabled, "Offline selling needs a login session", nil)
	}
	var st settings
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		st, err = loadSettings(ctx, q)
		return err
	}); err != nil {
		return OfflineSession{}, err
	}
	if !st.OfflineEnabled || st.OfflineMaxHours <= 0 {
		return OfflineSession{}, errConflict(CodeOfflineDisabled, "Offline selling is turned off for this store")
	}
	until, err := s.sessions.GrantOffline(ctx, actor.TenantID, sessionID, s.clock.Now().Add(time.Duration(st.OfflineMaxHours)*time.Hour))
	if err != nil {
		return OfflineSession{}, err
	}
	out := OfflineSession{OfflineUntil: until}
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		return s.record(ctx, q, actor, audit.Event{Action: ActionOfflineSession, EntityType: "session", EntityID: sessionID, After: out})
	}); err != nil {
		return OfflineSession{}, err
	}
	return out, nil
}

// SyncOffline posts a batch of offline sales made under the caller's session. Each sale posts
// once, keyed by its client id, as a cash sale on the date it was made, in today's shift. A sale
// the offline policy or the posting rejects goes to the exception queue for the owner; a sale
// sent again comes back with what became of it the first time.
func (s *Service) SyncOffline(ctx context.Context, actor audit.Actor, sessionID uuid.UUID, in OfflineSyncInput) (OfflineSyncResult, error) {
	if err := validateOfflineBatch(in); err != nil {
		return OfflineSyncResult{}, err
	}
	actor.Source = audit.SourcePOSOfflineSync
	var st settings
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		st, err = loadSettings(ctx, q)
		return err
	}); err != nil {
		return OfflineSyncResult{}, err
	}
	var until *time.Time
	if sessionID != uuid.Nil {
		var err error
		if until, err = s.sessions.OfflineUntil(ctx, actor.TenantID, sessionID); err != nil {
			return OfflineSyncResult{}, err
		}
	}
	out := OfflineSyncResult{Items: make([]OfflineSyncItem, 0, len(in.Sales))}
	for _, sl := range in.Sales {
		item, done, err := s.priorOutcome(ctx, actor.TenantID, sl.ClientID)
		if err != nil {
			return OfflineSyncResult{}, err
		}
		if !done {
			if code := offlineWindow(st.OfflineEnabled, st.OfflineMaxHours, until, sl.CreatedAt, s.clock.Now()); code != "" {
				item, err = s.parkSale(ctx, actor, sessionID, sl, apperr.NewConflict(code, offlineMessage(code)))
			} else {
				item, err = s.postOffline(ctx, actor, sessionID, sl)
			}
			if err != nil {
				return OfflineSyncResult{}, err
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func offlineMessage(code string) string {
	if code == CodeOfflineDisabled {
		return "Offline selling is turned off for this store"
	}
	return "The sale was made outside the session's offline window"
}

// priorOutcome is what an earlier sync made of a client sale: its sales document or its open or
// closed exception.
func (s *Service) priorOutcome(ctx context.Context, tenantID uuid.UUID, clientID string) (OfflineSyncItem, bool, error) {
	item := OfflineSyncItem{ClientID: clientID}
	found := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		doc, posted, err := offlineSaleDoc(ctx, q, clientID)
		if err != nil {
			return err
		}
		if posted {
			item.Status, item.SalesDocumentID, item.DocNo, found = SyncReplayed, &doc.ID, doc.DocNo, true
			return nil
		}
		var exc SyncException
		err = sqlx.GetContext(ctx, q, &exc, `SELECT `+syncExceptionColumns+` FROM pos_sync_exceptions WHERE offline_client_id = $1`, clientID)
		if err == nil {
			item.Status, item.ExceptionID, item.ErrorCode, found = SyncParked, &exc.ID, exc.ErrorCode, true
			item.SalesDocumentID = exc.ResolutionDocID
			if exc.ErrorDetail != nil {
				item.ErrorDetail = *exc.ErrorDetail
			}
			return nil
		}
		if !db.IsNoRows(err) {
			return wrap("read sync exception", err)
		}
		return nil
	})
	return item, found, err
}

// postOffline posts one offline sale; a rule it breaks parks it in the exception queue.
func (s *Service) postOffline(ctx context.Context, actor audit.Actor, sessionID uuid.UUID, sl OfflineSale) (OfflineSyncItem, error) {
	item := OfflineSyncItem{ClientID: sl.ClientID}
	sl.CustomerID = nonNilID(sl.CustomerID)
	if err := validateCheckout(sl.CheckoutInput); err != nil {
		return s.parkSale(ctx, actor, sessionID, sl, err)
	}
	if sl.LoyaltyPoints != nil {
		// The device could not see the balance: points are spent only online.
		return s.parkSale(ctx, actor, sessionID, sl, errInvalid(map[string]string{"loyalty_points": "online_only"}))
	}
	body, err := json.Marshal(sl)
	if err != nil {
		return item, err
	}
	hash, err := idempotency.RequestHash("POST", "/pos/offline-sync/"+sl.ClientID, body)
	if err != nil {
		return item, err
	}
	key := idempotency.Key{Value: "offline:" + sl.ClientID, RequestHash: hash}
	spec := sale{
		scope: ScopePOSOfflineSale, source: SourcePOSOffline, customer: sl.CustomerID, draft: sl.DraftInput,
		payments: sl.Payments, tendered: sl.CashTendered,
		offline: &offlineMark{ClientID: sl.ClientID, CreatedAt: sl.CreatedAt},
	}
	resp, err := s.postSale(ctx, actor, key, spec)
	if err != nil {
		if parkable(err) {
			return s.parkSale(ctx, actor, sessionID, sl, err)
		}
		if ctx.Err() != nil {
			return item, err
		}
		item.Status, item.ErrorCode = SyncFailed, "internal_error"
		if e, ok := apperr.As(err); ok {
			item.ErrorCode, item.ErrorDetail = e.Code, e.Message
		}
		return item, nil
	}
	var posted PosSale
	if err := json.Unmarshal(resp.Body, &posted); err != nil {
		return item, wrap("read offline sale response", err)
	}
	item.Status, item.SalesDocumentID, item.DocNo = SyncPosted, &posted.Document.ID, posted.Document.DocNo
	if resp.Replayed {
		item.Status = SyncReplayed
	}
	return item, nil
}

// parkable tells a business rule the sale broke, which the owner settles, from a failure the
// device retries. A clash on the sale's idempotency key (the same sale posting from another sync,
// or sent again with other contents) is not the sale's fault: parking it would queue a sale that
// may already be posted. Nor is a shift or a promotion that changed while the sale posted: sent
// again, it posts.
func parkable(err error) bool {
	e, ok := apperr.As(err)
	if !ok {
		return false
	}
	switch e.Code {
	case idempotency.CodeInProgress, idempotency.CodeKeyReused, CodeShiftChanged, CodeSaleChanged:
		return false
	}
	switch e.Kind {
	case apperr.Validation, apperr.Conflict, apperr.NotFound, apperr.ExceptionRequired, apperr.PreconditionFailed,
		apperr.PeriodLocked:
		return true
	}
	return false
}

// parkSale puts an offline sale in the exception queue with why it could not post; a sale already
// parked stays as it was, and one that posted meanwhile comes back as replayed.
func (s *Service) parkSale(ctx context.Context, actor audit.Actor, sessionID uuid.UUID, sl OfflineSale, cause error) (OfflineSyncItem, error) {
	item := OfflineSyncItem{ClientID: sl.ClientID, Status: SyncParked, ErrorCode: "internal_error"}
	var e *apperr.Error
	if errors.As(cause, &e) {
		item.ErrorCode, item.ErrorDetail = e.Code, e.Message
	}
	payload, err := json.Marshal(sl)
	if err != nil {
		return item, err
	}
	var session *uuid.UUID
	if sessionID != uuid.Nil {
		session = &sessionID
	}
	err = s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if err := lockOfflineSale(ctx, q, actor.TenantID, sl.ClientID); err != nil {
			return err
		}
		posted, found, err := offlineSaleDoc(ctx, q, sl.ClientID)
		if err != nil {
			return err
		}
		if found {
			item = OfflineSyncItem{ClientID: sl.ClientID, Status: SyncReplayed, SalesDocumentID: &posted.ID, DocNo: posted.DocNo}
			return nil
		}
		id := ids.New()
		res, err := q.ExecContext(ctx, `
			INSERT INTO pos_sync_exceptions (id, tenant_id, offline_client_id, device_session_id, payload, error_code,
			                                 error_detail, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'open', $8)
			ON CONFLICT (tenant_id, offline_client_id) DO NOTHING`,
			id, actor.TenantID, sl.ClientID, session, payload, item.ErrorCode, nullText(item.ErrorDetail), s.clock.Now())
		if err != nil {
			return wrap("insert sync exception", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return wrap("insert sync exception", err)
		} else if n == 0 {
			var prior SyncException
			if err := sqlx.GetContext(ctx, q, &prior, `SELECT `+syncExceptionColumns+` FROM pos_sync_exceptions WHERE offline_client_id = $1`, sl.ClientID); err != nil {
				return wrap("read sync exception", err)
			}
			item.ExceptionID, item.ErrorCode = &prior.ID, prior.ErrorCode
			item.ErrorDetail = ""
			if prior.ErrorDetail != nil {
				item.ErrorDetail = *prior.ErrorDetail
			}
			return nil
		}
		item.ExceptionID = &id
		if err := s.publishSyncException(ctx, q, actor.TenantID, id, ExceptionOpen); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionSyncException, EntityType: entitySyncException, EntityID: id,
			After: map[string]any{"offline_client_id": sl.ClientID, "error_code": item.ErrorCode, "error_detail": item.ErrorDetail}})
	})
	return item, err
}

func (s *Service) publishSyncException(ctx context.Context, q db.Querier, tenantID, id uuid.UUID, status string) error {
	if _, err := s.outbox.Publish(ctx, q, tenantID, syncExceptionEvent(id, status)); err != nil {
		return wrap("publish sync exception", err)
	}
	return nil
}

func syncExceptionEvent(id uuid.UUID, status string) outbox.Event {
	return outbox.Event{Topic: TopicSyncException, Payload: map[string]any{"pos_sync_exception_id": id, "status": status}}
}

func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// SyncExceptions lists the exception queue newest first.
func (s *Service) SyncExceptions(ctx context.Context, scope Scope, f SyncExceptionFilter) ([]SyncException, error) {
	var out []SyncException
	err := s.read(ctx, scope, func(q db.Querier) error {
		var w where
		switch f.Status {
		case "":
		case ExceptionOpen, ExceptionResolved, ExceptionDiscarded:
			w.add("status = $%d", f.Status)
		default:
			return errInvalid(map[string]string{"status": "unknown"})
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+syncExceptionColumns+` FROM pos_sync_exceptions`+w.sql()+tail, w.args...); err != nil {
			return wrap("list sync exceptions", err)
		}
		return nil
	})
	return out, err
}

// DiscardSyncException closes an open exception without posting anything: the sale is dropped.
func (s *Service) DiscardSyncException(ctx context.Context, actor audit.Actor, id uuid.UUID) (SyncException, error) {
	var out SyncException
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		exc, err := lockOpenException(ctx, q, id)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := q.ExecContext(ctx, `
			UPDATE pos_sync_exceptions SET status = 'discarded', resolved_by = $2, resolved_at = $3 WHERE id = $1`,
			id, actorID(actor), now); err != nil {
			return wrap("discard sync exception", err)
		}
		if out, err = getSyncException(ctx, q, id, false); err != nil {
			return err
		}
		if err := s.publishSyncException(ctx, q, actor.TenantID, id, out.Status); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionSyncDiscard, EntityType: entitySyncException, EntityID: id,
			Before: map[string]any{"status": exc.Status}, After: map[string]any{"status": out.Status}})
	})
	return out, err
}

func lockOpenException(ctx context.Context, q db.Querier, id uuid.UUID) (SyncException, error) {
	exc, err := getSyncException(ctx, q, id, true)
	if err != nil {
		return exc, err
	}
	if exc.Status != ExceptionOpen {
		return exc, errConflict(CodeExceptionClosed, "The sync exception was already "+exc.Status)
	}
	return exc, nil
}

// ResolveSyncException resolves an open exception by posting the corrected sale as an online POS
// sale today; the exception records the sales document in the same posting.
func (s *Service) ResolveSyncException(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in ResolveInput,
	exc Exceptions,
) (posting.Response, error) {
	in.CustomerID = nonNilID(in.CustomerID)
	if err := validateCheckout(in.CheckoutInput); err != nil {
		return posting.Response{}, err
	}
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		open, err := lockOpenException(ctx, q, id)
		if err != nil {
			return err
		}
		return checkNotPosted(ctx, q, open.OfflineClientID)
	}); err != nil {
		return posting.Response{}, err
	}
	sl := sale{
		scope: ScopePOSCheckout, source: SourcePOS, customer: in.CustomerID, draft: in.DraftInput, payments: in.Payments,
		tendered: in.CashTendered, points: in.LoyaltyPoints, resolves: &id, exc: exc.bind(entitySyncException, id, 0),
	}
	return s.postSale(ctx, actor, key, sl)
}

// postedSale is the sales document an offline sale posted as.
type postedSale struct {
	ID    uuid.UUID `db:"id"`
	DocNo string    `db:"doc_no"`
}

// offlineSaleDoc reads the sales document an offline sale posted as, if any.
func offlineSaleDoc(ctx context.Context, q db.Querier, clientID string) (postedSale, bool, error) {
	var doc postedSale
	err := sqlx.GetContext(ctx, q, &doc, `SELECT id, doc_no FROM sales_documents WHERE offline_client_id = $1`, clientID)
	if db.IsNoRows(err) {
		return doc, false, nil
	}
	if err != nil {
		return doc, false, wrap("read offline sale", err)
	}
	return doc, true, nil
}

// lockOfflineSale serializes, per client id, posting an offline sale with parking it in the
// exception queue until the transaction ends, so a sale never ends up both posted and parked.
func lockOfflineSale(ctx context.Context, q db.Querier, tenantID uuid.UUID, clientID string) error {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('offline_sale:' || $1::text || ':' || $2, 0))`,
		tenantID, clientID); err != nil {
		return wrap("lock offline sale", err)
	}
	return nil
}

// checkNotParked refuses to post an offline sale that is in the exception queue: the owner settles
// it there (409 offline_sale_parked, which the sync reports as the parked sale).
func checkNotParked(ctx context.Context, tx posting.Tx, clientID string) error {
	if err := lockOfflineSale(ctx, tx, tx.TenantID(), clientID); err != nil {
		return err
	}
	var parked bool
	if err := sqlx.GetContext(ctx, tx, &parked, `SELECT EXISTS (SELECT 1 FROM pos_sync_exceptions WHERE offline_client_id = $1)`, clientID); err != nil {
		return wrap("read sync exception", err)
	}
	if parked {
		return errConflict(CodeOfflineSaleParked, "The offline sale is in the sync exception queue")
	}
	return nil
}

// checkNotPosted refuses to resolve an exception whose offline sale already posted: posting the
// corrected sale would sell the goods twice (409 offline_sale_already_posted; discard it instead).
func checkNotPosted(ctx context.Context, q db.Querier, clientID string) error {
	_, posted, err := offlineSaleDoc(ctx, q, clientID)
	if err != nil {
		return err
	}
	if posted {
		return errConflict(CodeOfflineSalePosted, "The offline sale already posted; discard the exception instead")
	}
	return nil
}

// resolveException closes the exception a corrected sale resolves, inside the sale's posting.
func resolveException(ctx context.Context, tx posting.Tx, id, docID uuid.UUID) error {
	exc, err := lockOpenException(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE pos_sync_exceptions SET status = 'resolved', resolution_doc_id = $2, resolved_by = $3, resolved_at = $4
		WHERE id = $1`, id, docID, actorID(tx.Actor()), tx.Now()); err != nil {
		return wrap("resolve sync exception", err)
	}
	tx.Audit(audit.Event{Action: ActionSyncResolve, EntityType: entitySyncException, EntityID: id,
		Before: map[string]any{"status": exc.Status}, After: map[string]any{"status": ExceptionResolved, "resolution_doc_id": docID}})
	tx.Publish(syncExceptionEvent(id, ExceptionResolved))
	return nil
}
