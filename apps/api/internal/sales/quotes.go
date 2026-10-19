package sales

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// QuoteInput creates a draft quote. QuoteDate nil is today.
type QuoteInput struct {
	CustomerID uuid.UUID `json:"customer_party_id"`
	QuoteDate  *Date     `json:"quote_date,omitempty"`
	ValidUntil *Date     `json:"valid_until"`
	Note       *string   `json:"note,omitempty"`
	DraftInput
}

// QuotePatch changes a draft or sent quote; nil fields are kept. Draft replaces every line; a new
// customer without a Draft reprices the current lines for that customer.
type QuotePatch struct {
	CustomerID *uuid.UUID  `json:"customer_party_id,omitempty"`
	QuoteDate  *Date       `json:"quote_date,omitempty"`
	ValidUntil *Date       `json:"valid_until,omitempty"`
	Note       *string     `json:"note,omitempty"`
	Draft      *DraftInput `json:"draft,omitempty"`
}

// QuoteFilter narrows the quote list. Status may be "expired" (derived).
type QuoteFilter struct {
	CustomerID *uuid.UUID
	Status     string
	From, To   *time.Time
	Page       ListPage
}

// HistoryEntry is one audited change of a document.
type HistoryEntry struct {
	ID        uuid.UUID  `db:"id" json:"id"`
	Action    string     `db:"action" json:"action"`
	ActorType string     `db:"actor_type" json:"actor_type"`
	ActorID   *uuid.UUID `db:"actor_id" json:"actor_id"`
	Source    string     `db:"source" json:"source"`
	Before    RawJSON    `db:"before" json:"before"`
	After     RawJSON    `db:"after" json:"after"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
}

func validateQuoteHeader(fields map[string]string, quoteDate Date, validUntil *Date, note *string) {
	switch {
	case validUntil == nil || validUntil.IsZero():
		fields["valid_until"] = "required"
	case validUntil.Before(quoteDate.Time):
		fields["valid_until"] = "before_quote_date"
	}
	checkText(fields, "note", note, maxNoteLen)
}

// CreateQuote saves a draft quote priced for its customer on its date. A quote reserves nothing
// and posts nothing. With a key it runs once per Idempotency-Key: a retry of the same request
// returns the stored quote (replayed true) instead of saving a second one.
func (s *Service) CreateQuote(ctx context.Context, actor audit.Actor, key *idempotency.Key, in QuoteInput) (Quote, bool, error) {
	var out Quote
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ActionQuoteCreate, key, http.StatusCreated, &out, func() error {
			today, err := s.today(ctx, q, actor.TenantID)
			if err != nil {
				return err
			}
			quoteDate := DateOf(today)
			if in.QuoteDate != nil && !in.QuoteDate.IsZero() {
				quoteDate = *in.QuoteDate
			}
			fields := map[string]string{}
			if in.CustomerID == uuid.Nil {
				fields["customer_party_id"] = "required"
			}
			validateQuoteHeader(fields, quoteDate, in.ValidUntil, in.Note)
			if len(fields) > 0 {
				return errInvalid(fields)
			}
			if err := checkCustomer(ctx, q, in.CustomerID, "customer_party_id"); err != nil {
				return err
			}
			built, err := s.buildLines(ctx, q, actor.TenantID, lineBuild{PartyID: &in.CustomerID, Date: quoteDate.Time, Draft: in.DraftInput})
			if err != nil {
				return err
			}
			campaign, err := s.campaigns.Attribute(ctx, q, &in.CustomerID, quoteDate.Time)
			if err != nil {
				return err
			}
			docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixQuote)
			if err != nil {
				return err
			}
			now := s.clock.Now()
			qt := Quote{
				ID: ids.New(), DocNo: docNo, CustomerID: in.CustomerID, QuoteDate: quoteDate, ValidUntil: *in.ValidUntil,
				StoredStatus: QuoteDraft, CampaignID: campaign, Note: in.Note, RevisionNo: 1, CreatedAt: now, UpdatedAt: now,
				Version: 1, Totals: built.Totals,
			}
			if err := insertQuote(ctx, q, actor.TenantID, qt); err != nil {
				return err
			}
			if err := quoteLines.insert(ctx, q, actor.TenantID, qt.ID, built.Lines, nil); err != nil {
				return err
			}
			if out, err = s.quoteDetail(ctx, q, actor.TenantID, qt.ID); err != nil {
				return err
			}
			return s.record(ctx, q, actor, audit.Event{Action: ActionQuoteCreate, EntityType: entityQuote, EntityID: qt.ID, DocNo: docNo, After: out})
		})
		return err
	})
	return out, replayed, err
}

func insertQuote(ctx context.Context, q db.Querier, tenantID uuid.UUID, qt Quote) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO quotes (id, tenant_id, doc_no, customer_party_id, quote_date, valid_until, status, campaign_id, note,
			copied_from_id, revision_no, created_at, updated_at, version, goods_amount, discount_amount, tax_amount, total_amount)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		qt.ID, tenantID, qt.DocNo, qt.CustomerID, qt.QuoteDate, qt.ValidUntil, qt.StoredStatus, qt.CampaignID, qt.Note,
		qt.CopiedFromID, qt.RevisionNo, qt.CreatedAt, qt.UpdatedAt, qt.Version, qt.Goods, qt.Discount, qt.Tax, qt.Total)
	if err != nil {
		return wrap("insert quote", err)
	}
	return nil
}

// lockQuote reads quote id for update, checks it is at version and that action is allowed on the
// status it shows today.
func (s *Service) lockQuote(ctx context.Context, q db.Querier, tenantID, id uuid.UUID, version int, action string) (Quote, time.Time, error) {
	qt, err := getQuote(ctx, q, id, true)
	if err != nil {
		return qt, time.Time{}, err
	}
	if qt.Version != version {
		return qt, time.Time{}, errStale()
	}
	today, err := s.today(ctx, q, tenantID)
	if err != nil {
		return qt, time.Time{}, err
	}
	qt.Status = quoteStatus(qt.StoredStatus, qt.ValidUntil, today)
	if err := checkQuoteAction(qt.Status, action); err != nil {
		return qt, today, err
	}
	return qt, today, nil
}

// UpdateQuote edits a draft or sent quote at version in place.
func (s *Service) UpdateQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in QuotePatch) (Quote, error) {
	return s.changeQuote(ctx, actor, id, version, in, false)
}

// ReviseQuote saves a new revision ("phiên bản N+1") of a draft or sent quote at version. The
// revision goes back to draft to be sent again; the prior revision stays in the audit log.
func (s *Service) ReviseQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in QuotePatch) (Quote, error) {
	return s.changeQuote(ctx, actor, id, version, in, true)
}

func (s *Service) changeQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in QuotePatch, revise bool) (Quote, error) {
	var out Quote
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		qt, _, err := s.lockQuote(ctx, q, actor.TenantID, id, version, quoteEdit)
		if err != nil {
			return err
		}
		before, err := s.quoteDetail(ctx, q, actor.TenantID, id)
		if err != nil {
			return err
		}
		var sets []db.Set
		fields := map[string]string{}
		customer, quoteDate, validUntil := qt.CustomerID, qt.QuoteDate, qt.ValidUntil
		draft := in.Draft
		if in.CustomerID != nil && *in.CustomerID != qt.CustomerID {
			customer = *in.CustomerID
			sets = append(sets, db.Set{Column: "customer_party_id", Value: customer})
		}
		if in.QuoteDate != nil && !in.QuoteDate.IsZero() {
			quoteDate = *in.QuoteDate
			sets = append(sets, db.Set{Column: "quote_date", Value: quoteDate})
		}
		if in.ValidUntil != nil {
			validUntil = *in.ValidUntil
			sets = append(sets, db.Set{Column: "valid_until", Value: validUntil})
		}
		validateQuoteHeader(fields, quoteDate, &validUntil, in.Note)
		if in.Note != nil {
			sets = append(sets, db.Set{Column: "note", Value: *in.Note})
		}
		if len(fields) > 0 {
			return errInvalid(fields)
		}
		if customer != qt.CustomerID {
			if err := checkCustomer(ctx, q, customer, "customer_party_id"); err != nil {
				return err
			}
			if draft == nil {
				// The new customer's prices apply: the same lines are priced again for them.
				lines := plainLines(before.Lines)
				draft = &DraftInput{Lines: draftOf(lines), OrderDiscount: orderDiscountOf(lines)}
			}
		}
		if draft != nil {
			built, err := s.buildLines(ctx, q, actor.TenantID, lineBuild{PartyID: &customer, Date: quoteDate.Time, Draft: *draft})
			if err != nil {
				return err
			}
			if err := quoteLines.remove(ctx, q, id); err != nil {
				return err
			}
			if err := quoteLines.insert(ctx, q, actor.TenantID, id, built.Lines, nil); err != nil {
				return err
			}
			sets = append(sets, totalSets(built.Totals)...)
		}
		if revise {
			sets = append(sets, db.Set{Column: "revision_no", Value: qt.RevisionNo + 1}, db.Set{Column: "status", Value: QuoteDraft})
		}
		if len(sets) == 0 {
			return errConflict(CodeNoChange, "The request changes nothing")
		}
		if _, err := db.UpdateWithVersion(ctx, q, "quotes", id, version, s.clock.Now(), sets...); err != nil {
			return err
		}
		if out, err = s.quoteDetail(ctx, q, actor.TenantID, id); err != nil {
			return err
		}
		action := ActionQuoteUpdate
		if revise {
			action = ActionQuoteRevise
		}
		return s.record(ctx, q, actor, audit.Event{Action: action, EntityType: entityQuote, EntityID: id, DocNo: qt.DocNo, Before: before, After: out})
	})
	return out, err
}

// totalSets are the column assignments of a document's totals.
func totalSets(t Totals) []db.Set {
	return []db.Set{
		{Column: "goods_amount", Value: t.Goods}, {Column: "discount_amount", Value: t.Discount},
		{Column: "tax_amount", Value: t.Tax}, {Column: "total_amount", Value: t.Total},
	}
}

// DeleteQuote deletes a draft or sent quote at version.
func (s *Service) DeleteQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		qt, _, err := s.lockQuote(ctx, q, actor.TenantID, id, version, quoteEdit)
		if err != nil {
			return err
		}
		before, err := s.quoteDetail(ctx, q, actor.TenantID, id)
		if err != nil {
			return err
		}
		if err := quoteLines.remove(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM quotes WHERE id = $1`, id); err != nil {
			return wrap("delete quote", err)
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionQuoteDelete, EntityType: entityQuote, EntityID: id, DocNo: qt.DocNo, Before: before})
	})
}

// SendQuote marks a draft quote sent to the customer. A key replays the first result of the
// request it identifies, before the version check, so a retry after the send succeeded does not
// fail on the version the send itself moved.
func (s *Service) SendQuote(ctx context.Context, actor audit.Actor, key *idempotency.Key, id uuid.UUID, version int) (Quote, bool, error) {
	return s.moveQuote(ctx, actor, key, id, version, quoteSend, QuoteSent, ActionQuoteSend)
}

// AcceptQuote records the customer's acceptance.
func (s *Service) AcceptQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Quote, error) {
	out, _, err := s.moveQuote(ctx, actor, nil, id, version, quoteAccept, QuoteAccepted, ActionQuoteAccept)
	return out, err
}

// RejectQuote records the customer's refusal.
func (s *Service) RejectQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Quote, error) {
	out, _, err := s.moveQuote(ctx, actor, nil, id, version, quoteReject, QuoteRejected, ActionQuoteReject)
	return out, err
}

// moveQuote changes the status of a quote; the audit action is also the idempotency scope.
func (s *Service) moveQuote(ctx context.Context, actor audit.Actor, key *idempotency.Key, id uuid.UUID, version int, action, to, auditAction string) (Quote, bool, error) {
	var out Quote
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, auditAction, key, http.StatusOK, &out, func() error {
			qt, _, err := s.lockQuote(ctx, q, actor.TenantID, id, version, action)
			if err != nil {
				return err
			}
			if _, err := db.UpdateWithVersion(ctx, q, "quotes", id, version, s.clock.Now(), db.Set{Column: "status", Value: to}); err != nil {
				return err
			}
			if out, err = s.quoteDetail(ctx, q, actor.TenantID, id); err != nil {
				return err
			}
			return s.record(ctx, q, actor, audit.Event{Action: auditAction, EntityType: entityQuote, EntityID: id, DocNo: qt.DocNo,
				Before: map[string]string{"status": qt.Status}, After: map[string]string{"status": out.Status}})
		})
		return err
	})
	return out, replayed, err
}

// CopyQuote starts a new draft quote from any quote (an expired one is quoted again this way),
// repriced today and valid for as many days as the original.
func (s *Service) CopyQuote(ctx context.Context, actor audit.Actor, id uuid.UUID) (Quote, error) {
	var out Quote
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		src, err := getQuote(ctx, q, id, false)
		if err != nil {
			return err
		}
		lines, err := quoteLinesOf(ctx, q, id)
		if err != nil {
			return err
		}
		today, err := s.today(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		if err := checkCustomer(ctx, q, src.CustomerID, "customer_party_id"); err != nil {
			return err
		}
		quoteDate := DateOf(today)
		days := int(src.ValidUntil.Sub(src.QuoteDate.Time).Hours() / 24)
		validUntil := DateOf(today.AddDate(0, 0, days))
		draft := DraftInput{Lines: draftOf(plainLines(lines)), OrderDiscount: orderDiscountOf(plainLines(lines))}
		built, err := s.buildLines(ctx, q, actor.TenantID, lineBuild{PartyID: &src.CustomerID, Date: today, Draft: draft})
		if err != nil {
			return err
		}
		docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixQuote)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		srcID := src.ID
		qt := Quote{
			ID: ids.New(), DocNo: docNo, CustomerID: src.CustomerID, QuoteDate: quoteDate, ValidUntil: validUntil,
			StoredStatus: QuoteDraft, CampaignID: src.CampaignID, Note: src.Note, CopiedFromID: &srcID, RevisionNo: 1,
			CreatedAt: now, UpdatedAt: now, Version: 1, Totals: built.Totals,
		}
		if err := insertQuote(ctx, q, actor.TenantID, qt); err != nil {
			return err
		}
		if err := quoteLines.insert(ctx, q, actor.TenantID, qt.ID, built.Lines, nil); err != nil {
			return err
		}
		if out, err = s.quoteDetail(ctx, q, actor.TenantID, qt.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionQuoteCreate, EntityType: entityQuote, EntityID: qt.ID, DocNo: docNo,
			After: out})
	})
	return out, err
}

// orderDiscountOf is the order discount spread over the priced lines (a mix pack header repeats
// what its flavors carry).
func orderDiscountOf(lines []Line) money.Money {
	out := money.ZeroMoney
	for _, l := range lines {
		if priced(l.Kind) {
			out = out.Add(l.OrderAlloc)
		}
	}
	return out
}

// ConvertQuote turns a quote into a draft invoice holding the quoted lines and prices; the quote
// becomes converted in the same transaction and can no longer change.
func (s *Service) ConvertQuote(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Invoice, error) {
	var out Invoice
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		qt, today, err := s.lockQuote(ctx, q, actor.TenantID, id, version, quoteConvert)
		if err != nil {
			return err
		}
		if err := checkCustomer(ctx, q, qt.CustomerID, "customer_party_id"); err != nil {
			return err
		}
		lines, err := quoteLinesOf(ctx, q, id)
		if err != nil {
			return err
		}
		docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixInvoice)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		quoteID := qt.ID
		inv := Invoice{
			ID: ids.New(), DocNo: docNo, CustomerID: qt.CustomerID, PayerID: qt.CustomerID, InvoiceDate: DateOf(today),
			Status: InvoiceDraft, QuoteID: &quoteID, CampaignID: qt.CampaignID, Note: qt.Note, CreatedAt: now, UpdatedAt: now,
			Version: 1, Totals: qt.Totals,
		}
		if err := insertInvoice(ctx, q, actor.TenantID, inv, cloneLines(plainLines(lines))); err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "quotes", id, version, now,
			db.Set{Column: "status", Value: QuoteConverted}, db.Set{Column: "converted_invoice_id", Value: inv.ID}); err != nil {
			return err
		}
		if out, err = s.invoiceDetail(ctx, q, inv.ID); err != nil {
			return err
		}
		if err := s.record(ctx, q, actor, audit.Event{Action: ActionInvoiceCreate, EntityType: entityInvoice, EntityID: inv.ID,
			DocNo: docNo, After: out}); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionQuoteConvert, EntityType: entityQuote, EntityID: id, DocNo: qt.DocNo,
			Before: map[string]string{"status": qt.Status}, After: map[string]any{"status": QuoteConverted, "converted_invoice_id": inv.ID}})
	})
	return out, err
}

// Quote returns a quote with its lines, the status it shows today and each line's retail price.
func (s *Service) Quote(ctx context.Context, scope Scope, id uuid.UUID) (Quote, error) {
	var out Quote
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = s.quoteDetail(ctx, q, scope.TenantID, id)
		return err
	})
	return out, err
}

func (s *Service) quoteDetail(ctx context.Context, q db.Querier, tenantID, id uuid.UUID) (Quote, error) {
	qt, err := getQuote(ctx, q, id, false)
	if err != nil {
		return qt, err
	}
	today, err := s.today(ctx, q, tenantID)
	if err != nil {
		return qt, err
	}
	qt.Status = quoteStatus(qt.StoredStatus, qt.ValidUntil, today)
	lines, err := quoteLinesOf(ctx, q, id)
	if err != nil {
		return qt, err
	}
	var queries []RetailQuery
	var at []int
	for i, l := range lines {
		if l.SkuID == nil || l.Kind == KindComboComponent {
			continue
		}
		unit := l.UnitID
		queries = append(queries, RetailQuery{SkuID: *l.SkuID, ColorID: l.ColorID, UnitID: &unit, Qty: l.Qty})
		at = append(at, i)
	}
	if len(queries) > 0 {
		prices, err := s.pricer.RetailPrices(ctx, q, tenantID, queries)
		if err != nil {
			return qt, err
		}
		if len(prices) != len(queries) {
			return qt, fmt.Errorf("sales: pricing returned %d retail prices for %d lines", len(prices), len(queries))
		}
		for k, i := range at {
			lines[i].RetailPrice = prices[k]
		}
	}
	qt.Lines = lines
	return qt, nil
}

// Quotes lists quotes newest first, without lines.
func (s *Service) Quotes(ctx context.Context, scope Scope, f QuoteFilter) ([]Quote, error) {
	var out []Quote
	err := s.read(ctx, scope, func(q db.Querier) error {
		today, err := s.today(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		var w where
		if f.CustomerID != nil {
			w.add("customer_party_id = $%d", *f.CustomerID)
		}
		switch f.Status {
		case "":
		case QuoteExpired:
			w.add("status IN ('draft', 'sent') AND valid_until < $%d", today)
		case QuoteDraft, QuoteSent:
			w.add("status = $%d AND valid_until >= $%d", f.Status, today)
		case QuoteAccepted, QuoteRejected, QuoteConverted:
			w.add("status = $%d", f.Status)
		default:
			return errInvalid(map[string]string{"status": "unknown"})
		}
		if f.From != nil {
			w.add("quote_date >= $%d", *f.From)
		}
		if f.To != nil {
			w.add("quote_date <= $%d", *f.To)
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+quoteColumns+` FROM quotes`+w.sql()+tail, w.args...); err != nil {
			return wrap("list quotes", err)
		}
		for i := range out {
			out[i].Status = quoteStatus(out[i].StoredStatus, out[i].ValidUntil, today)
		}
		return nil
	})
	return out, err
}

// QuoteHistory lists the audited changes of a quote, oldest first: its status changes and the
// prior content of each revision.
func (s *Service) QuoteHistory(ctx context.Context, scope Scope, id uuid.UUID) ([]HistoryEntry, error) {
	var out []HistoryEntry
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := getQuote(ctx, q, id, false); err != nil {
			return err
		}
		return historyOf(ctx, q, entityQuote, id, &out)
	})
	return out, err
}

func historyOf(ctx context.Context, q db.Querier, entityType string, id uuid.UUID, dst *[]HistoryEntry) error {
	if err := sqlx.SelectContext(ctx, q, dst, `
		SELECT id, action, actor_type, actor_id, source, before, after, created_at
		FROM audit_events WHERE entity_type = $1 AND entity_id = $2
		ORDER BY created_at, id`, entityType, id); err != nil {
		return wrap("read history", err)
	}
	return nil
}

// snapshotJSON marshals a document snapshot (invoice versions).
func snapshotJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("sales: snapshot: %w", err)
	}
	return b, nil
}
