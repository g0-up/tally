package sales

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// InvoiceInput creates a draft invoice. PayerID nil is the customer; InvoiceDate nil is today.
type InvoiceInput struct {
	CustomerID  uuid.UUID  `json:"customer_party_id"`
	PayerID     *uuid.UUID `json:"payer_party_id,omitempty"`
	InvoiceDate *Date      `json:"invoice_date,omitempty"`
	DueDate     *Date      `json:"due_date,omitempty"`
	Note        *string    `json:"note,omitempty"`
	DraftInput
}

// InvoicePatch changes the header of a draft invoice; nil fields are kept. A new customer or date
// prices the lines again.
type InvoicePatch struct {
	CustomerID  *uuid.UUID `json:"customer_party_id,omitempty"`
	PayerID     *uuid.UUID `json:"payer_party_id,omitempty"`
	InvoiceDate *Date      `json:"invoice_date,omitempty"`
	DueDate     *Date      `json:"due_date,omitempty"`
	Note        *string    `json:"note,omitempty"`
}

// InvoiceFilter narrows the invoice list.
type InvoiceFilter struct {
	CustomerID *uuid.UUID
	Status     string
	From, To   *time.Time
	Page       ListPage
}

// PaidAmountInput sets "Khách đã thanh toán" on a draft invoice: the total the customer has paid
// toward it. MethodID is how an increase is paid; a decrease is refunded the way each deposit came.
type PaidAmountInput struct {
	Amount   money.Money `json:"amount"`
	MethodID *uuid.UUID  `json:"payment_method_id,omitempty"`
	Note     *string     `json:"note,omitempty"`
}

func validateInvoiceHeader(fields map[string]string, invoiceDate Date, dueDate *Date, note *string) {
	if dueDate != nil && !dueDate.IsZero() && dueDate.Before(invoiceDate.Time) {
		fields["due_date"] = "before_invoice_date"
	}
	checkText(fields, "note", note, maxNoteLen)
}

// CreateInvoice saves a draft invoice priced for its customer on its date. A draft reserves
// nothing and posts nothing. With a key it runs once per Idempotency-Key: a retry of the same
// request returns the stored invoice (replayed true) instead of saving a second one.
func (s *Service) CreateInvoice(ctx context.Context, actor audit.Actor, key *idempotency.Key, in InvoiceInput) (Invoice, bool, error) {
	var out Invoice
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ActionInvoiceCreate, key, http.StatusCreated, &out, func() error {
			out, err = s.CreateInvoiceIn(ctx, q, actor, in)
			return err
		})
		return err
	})
	return out, replayed, err
}

// CreateInvoiceIn is CreateInvoice without a key inside the caller's tenant transaction q, for a
// use case that drafts an invoice as one step of a larger command.
func (s *Service) CreateInvoiceIn(ctx context.Context, q db.Querier, actor audit.Actor, in InvoiceInput) (Invoice, error) {
	today, err := s.today(ctx, q, actor.TenantID)
	if err != nil {
		return Invoice{}, err
	}
	invoiceDate := DateOf(today)
	if in.InvoiceDate != nil && !in.InvoiceDate.IsZero() {
		invoiceDate = *in.InvoiceDate
	}
	fields := map[string]string{}
	if in.CustomerID == uuid.Nil {
		fields["customer_party_id"] = "required"
	}
	validateInvoiceHeader(fields, invoiceDate, in.DueDate, in.Note)
	if len(fields) > 0 {
		return Invoice{}, errInvalid(fields)
	}
	payer := in.CustomerID
	if in.PayerID != nil && *in.PayerID != uuid.Nil {
		payer = *in.PayerID
	}
	if err := checkParties(ctx, q, in.CustomerID, payer); err != nil {
		return Invoice{}, err
	}
	built, err := s.buildLines(ctx, q, actor.TenantID, lineBuild{PartyID: &in.CustomerID, Date: invoiceDate.Time, Draft: in.DraftInput})
	if err != nil {
		return Invoice{}, err
	}
	campaign, err := s.campaigns.Attribute(ctx, q, &in.CustomerID, invoiceDate.Time)
	if err != nil {
		return Invoice{}, err
	}
	docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixInvoice)
	if err != nil {
		return Invoice{}, err
	}
	now := s.clock.Now()
	inv := Invoice{
		ID: ids.New(), DocNo: docNo, CustomerID: in.CustomerID, PayerID: payer, InvoiceDate: invoiceDate,
		DueDate: nonZeroDate(in.DueDate), Status: InvoiceDraft, CampaignID: campaign, Note: in.Note,
		CreatedAt: now, UpdatedAt: now, Version: 1, Totals: built.Totals,
	}
	if err := insertInvoice(ctx, q, actor.TenantID, inv, built.Lines); err != nil {
		return Invoice{}, err
	}
	out, err := s.invoiceDetail(ctx, q, inv.ID)
	if err != nil {
		return Invoice{}, err
	}
	return out, s.record(ctx, q, actor, audit.Event{Action: ActionInvoiceCreate, EntityType: entityInvoice, EntityID: inv.ID, DocNo: docNo, After: out})
}

// checkParties verifies the customer and the payer of an invoice.
func checkParties(ctx context.Context, q db.Querier, customer, payer uuid.UUID) error {
	if err := checkCustomer(ctx, q, customer, "customer_party_id"); err != nil {
		return err
	}
	if payer == customer {
		return nil
	}
	return checkCustomer(ctx, q, payer, "payer_party_id")
}

func nonZeroDate(d *Date) *Date {
	if d == nil || d.IsZero() {
		return nil
	}
	return d
}

// insertInvoice stores a draft invoice and its lines; nothing of a draft is reserved or delivered.
func insertInvoice(ctx context.Context, q db.Querier, tenantID uuid.UUID, inv Invoice, lines []Line) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO invoices (id, tenant_id, doc_no, customer_party_id, payer_party_id, invoice_date, due_date, status, quote_id,
			campaign_id, copied_from_id, note, created_at, updated_at, version, goods_amount, discount_amount, tax_amount,
			total_amount)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		inv.ID, tenantID, inv.DocNo, inv.CustomerID, inv.PayerID, inv.InvoiceDate, inv.DueDate, inv.Status, inv.QuoteID,
		inv.CampaignID, inv.CopiedFromID, inv.Note, inv.CreatedAt, inv.UpdatedAt, inv.Version, inv.Goods, inv.Discount, inv.Tax,
		inv.Total)
	if err != nil {
		return wrap("insert invoice", err)
	}
	return invoiceLines.insert(ctx, q, tenantID, inv.ID, lines, newInvoiceLineExtras(len(lines)))
}

// newInvoiceLineExtras are the fulfilment quantities of new invoice lines: all zero.
func newInvoiceLineExtras(n int) [][]any {
	out := make([][]any, n)
	for i := range out {
		out[i] = []any{money.ZeroQty, money.ZeroQty, money.ZeroQty, money.ZeroQty}
	}
	return out
}

// lockDraft reads invoice id for update and checks that it is a draft at version.
func lockDraft(ctx context.Context, q db.Querier, id uuid.UUID, version int) (Invoice, error) {
	inv, err := getInvoice(ctx, q, id, true)
	if err != nil {
		return inv, err
	}
	if inv.Version != version {
		return inv, errStale()
	}
	if inv.Status != InvoiceDraft {
		return inv, errConflict(CodeInvoiceNotEditable, "Only a draft invoice can be edited; revise a confirmed one")
	}
	return inv, nil
}

// paidKept is what the customer still has paid toward an invoice: its deposits less what was
// refunded of them, pending transfers included.
func (s *Service) paidKept(ctx context.Context, q db.Querier, invoiceID uuid.UUID) (money.Money, []Deposit, error) {
	deposits, err := s.debt.Deposits(ctx, q, invoiceID)
	if err != nil {
		return money.Money{}, nil, err
	}
	kept := money.ZeroMoney
	for _, d := range deposits {
		kept = kept.Add(d.Kept())
	}
	return kept, deposits, nil
}

// requireUnpaid blocks a change of the parties or the lines of an invoice the customer has paid
// toward: the deposits were taken for that order from that payer. Refunding them first (paid
// amount 0) unblocks the change.
func (s *Service) requireUnpaid(ctx context.Context, q db.Querier, invoiceID uuid.UUID) error {
	kept, _, err := s.paidKept(ctx, q, invoiceID)
	if err != nil {
		return err
	}
	if kept.IsPositive() {
		return errConflict(CodeInvoiceHasPayments, "The customer has paid toward this invoice; set the paid amount to 0 before changing it")
	}
	return nil
}

// UpdateInvoice edits the header of a draft invoice at version. A new customer or invoice date
// reprices the lines; the customer, payer and lines of a paid invoice cannot change.
func (s *Service) UpdateInvoice(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in InvoicePatch) (Invoice, error) {
	var out Invoice
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		inv, err := lockDraft(ctx, q, id, version)
		if err != nil {
			return err
		}
		before, err := s.invoiceDetail(ctx, q, id)
		if err != nil {
			return err
		}
		var sets []db.Set
		customer, payer, invoiceDate, dueDate := inv.CustomerID, inv.PayerID, inv.InvoiceDate, inv.DueDate
		if in.CustomerID != nil && *in.CustomerID != uuid.Nil && *in.CustomerID != inv.CustomerID {
			customer = *in.CustomerID
			sets = append(sets, db.Set{Column: "customer_party_id", Value: customer})
			if in.PayerID == nil && inv.PayerID == inv.CustomerID {
				// A payer that was the customer follows the customer.
				payer = customer
			}
		}
		if in.PayerID != nil && *in.PayerID != uuid.Nil {
			payer = *in.PayerID
		}
		if payer != inv.PayerID {
			sets = append(sets, db.Set{Column: "payer_party_id", Value: payer})
		}
		if in.InvoiceDate != nil && !in.InvoiceDate.IsZero() && !in.InvoiceDate.Equal(inv.InvoiceDate.Time) {
			invoiceDate = *in.InvoiceDate
			sets = append(sets, db.Set{Column: "invoice_date", Value: invoiceDate})
		}
		if in.DueDate != nil {
			dueDate = nonZeroDate(in.DueDate)
			sets = append(sets, db.Set{Column: "due_date", Value: dueDate})
		}
		fields := map[string]string{}
		validateInvoiceHeader(fields, invoiceDate, dueDate, in.Note)
		if in.Note != nil {
			sets = append(sets, db.Set{Column: "note", Value: *in.Note})
		}
		if len(fields) > 0 {
			return errInvalid(fields)
		}
		partiesChanged := customer != inv.CustomerID || payer != inv.PayerID
		reprice := customer != inv.CustomerID || !invoiceDate.Equal(inv.InvoiceDate.Time)
		if partiesChanged || reprice {
			if err := s.requireUnpaid(ctx, q, id); err != nil {
				return err
			}
		}
		if partiesChanged {
			if err := checkParties(ctx, q, customer, payer); err != nil {
				return err
			}
		}
		if reprice {
			lines := plainLines(before.Lines)
			draft := DraftInput{Lines: draftOf(lines), OrderDiscount: orderDiscountOf(lines)}
			totals, err := s.replaceInvoiceLines(ctx, q, actor.TenantID, id, customer, invoiceDate, draft)
			if err != nil {
				return err
			}
			sets = append(sets, totalSets(totals)...)
		}
		if len(sets) == 0 {
			return errConflict(CodeNoChange, "The request changes nothing")
		}
		if _, err := db.UpdateWithVersion(ctx, q, "invoices", id, version, s.clock.Now(), sets...); err != nil {
			return err
		}
		if out, err = s.invoiceDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionInvoiceUpdate, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo,
			Before: before, After: out})
	})
	return out, err
}

// ReplaceInvoiceLines replaces every line of an unpaid draft invoice at version.
func (s *Service) ReplaceInvoiceLines(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in DraftInput) (Invoice, error) {
	var out Invoice
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		inv, err := lockDraft(ctx, q, id, version)
		if err != nil {
			return err
		}
		if err := s.requireUnpaid(ctx, q, id); err != nil {
			return err
		}
		before, err := s.invoiceDetail(ctx, q, id)
		if err != nil {
			return err
		}
		totals, err := s.replaceInvoiceLines(ctx, q, actor.TenantID, id, inv.CustomerID, inv.InvoiceDate, in)
		if err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "invoices", id, version, s.clock.Now(), totalSets(totals)...); err != nil {
			return err
		}
		if out, err = s.invoiceDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionInvoiceUpdate, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo,
			Before: before, After: out})
	})
	return out, err
}

// replaceInvoiceLines prices draft for customer on date and stores it as the lines of a draft
// invoice, returning the new totals.
func (s *Service) replaceInvoiceLines(ctx context.Context, q db.Querier, tenantID, id, customer uuid.UUID, date Date, draft DraftInput) (Totals, error) {
	built, err := s.buildLines(ctx, q, tenantID, lineBuild{PartyID: &customer, Date: date.Time, Draft: draft})
	if err != nil {
		return Totals{}, err
	}
	if err := invoiceLines.remove(ctx, q, id); err != nil {
		return Totals{}, err
	}
	if err := invoiceLines.insert(ctx, q, tenantID, id, built.Lines, newInvoiceLineExtras(len(built.Lines))); err != nil {
		return Totals{}, err
	}
	return built.Totals, nil
}

// DeleteInvoice deletes a draft invoice at version that never took a payment. An invoice converted
// from a quote gives the quote back as accepted, so it can be converted again.
func (s *Service) DeleteInvoice(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		inv, err := lockDraft(ctx, q, id, version)
		if err != nil {
			return err
		}
		var paid bool
		// Any payment ever earmarked for the invoice references it, refunded or not.
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM payments WHERE earmarked_invoice_id = $1)`, id).Scan(&paid); err != nil {
			return wrap("read invoice payments", err)
		}
		if paid {
			return errConflict(CodeInvoiceHasPayments, "The invoice has payments and cannot be deleted")
		}
		before, err := s.invoiceDetail(ctx, q, id)
		if err != nil {
			return err
		}
		if inv.QuoteID != nil {
			qt, err := getQuote(ctx, q, *inv.QuoteID, true)
			if err != nil {
				return err
			}
			if _, err := db.UpdateWithVersion(ctx, q, "quotes", qt.ID, qt.Version, s.clock.Now(),
				db.Set{Column: "status", Value: QuoteAccepted}, db.Set{Column: "converted_invoice_id", Value: nil}); err != nil {
				return err
			}
			if err := s.record(ctx, q, actor, audit.Event{Action: ActionQuoteAccept, EntityType: entityQuote, EntityID: qt.ID, DocNo: qt.DocNo,
				Before: map[string]any{"status": qt.StoredStatus, "converted_invoice_id": id},
				After:  map[string]any{"status": QuoteAccepted, "converted_invoice_id": nil}}); err != nil {
				return err
			}
		}
		if err := invoiceLines.remove(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM invoices WHERE id = $1`, id); err != nil {
			return wrap("delete invoice", err)
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionInvoiceDelete, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo, Before: before})
	})
}

// CopyInvoice starts a new draft invoice from any invoice, for the same customer and payer, priced
// today.
func (s *Service) CopyInvoice(ctx context.Context, actor audit.Actor, id uuid.UUID) (Invoice, error) {
	var out Invoice
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		src, err := getInvoice(ctx, q, id, false)
		if err != nil {
			return err
		}
		lines, err := invoiceLinesOf(ctx, q, id)
		if err != nil {
			return err
		}
		if err := checkParties(ctx, q, src.CustomerID, src.PayerID); err != nil {
			return err
		}
		today, err := s.today(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		plain := plainLines(lines)
		draft := DraftInput{Lines: draftOf(plain), OrderDiscount: orderDiscountOf(plain)}
		built, err := s.buildLines(ctx, q, actor.TenantID, lineBuild{PartyID: &src.CustomerID, Date: today, Draft: draft})
		if err != nil {
			return err
		}
		campaign, err := s.campaigns.Attribute(ctx, q, &src.CustomerID, today)
		if err != nil {
			return err
		}
		docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixInvoice)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		srcID := src.ID
		inv := Invoice{
			ID: ids.New(), DocNo: docNo, CustomerID: src.CustomerID, PayerID: src.PayerID, InvoiceDate: DateOf(today),
			Status: InvoiceDraft, CampaignID: campaign, CopiedFromID: &srcID, Note: src.Note, CreatedAt: now, UpdatedAt: now,
			Version: 1, Totals: built.Totals,
		}
		if err := insertInvoice(ctx, q, actor.TenantID, inv, built.Lines); err != nil {
			return err
		}
		if out, err = s.invoiceDetail(ctx, q, inv.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionInvoiceCreate, EntityType: entityInvoice, EntityID: inv.ID, DocNo: docNo, After: out})
	})
	return out, err
}

// Invoice returns an invoice with its lines and payment state.
func (s *Service) Invoice(ctx context.Context, scope Scope, id uuid.UUID) (Invoice, error) {
	var out Invoice
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = s.invoiceDetail(ctx, q, id)
		return err
	})
	return out, err
}

func (s *Service) invoiceDetail(ctx context.Context, q db.Querier, id uuid.UUID) (Invoice, error) {
	inv, err := getInvoice(ctx, q, id, false)
	if err != nil {
		return inv, err
	}
	if inv.Lines, err = invoiceLinesOf(ctx, q, id); err != nil {
		return inv, err
	}
	st, err := s.invoicePayment(ctx, q, inv)
	if err != nil {
		return inv, err
	}
	inv.setPayment(st)
	return inv, nil
}

// invoicePayment is the payment state of an invoice: its deposits and what other payments settled
// on the receivables of its sales documents, against its total (what was delivered, once the
// remainder is cancelled).
func (s *Service) invoicePayment(ctx context.Context, q db.Querier, inv Invoice) (PaymentState, error) {
	paid, err := s.debt.InvoicesPaid(ctx, q, []uuid.UUID{inv.ID})
	if err != nil {
		return PaymentState{}, err
	}
	return invoicePaymentOf(inv, paid[inv.ID]), nil
}

func invoicePaymentOf(inv Invoice, paid InvoicePaid) PaymentState {
	total := inv.Total
	if inv.Status == InvoiceCancelledRemaining {
		total = inv.Delivered
	}
	st := paymentStateOf(total, paid.Confirmed)
	st.Pending = paid.Pending
	return st
}

// Invoices lists invoices newest first, without lines.
func (s *Service) Invoices(ctx context.Context, scope Scope, f InvoiceFilter) ([]Invoice, error) {
	var out []Invoice
	err := s.read(ctx, scope, func(q db.Querier) error {
		var w where
		if f.CustomerID != nil {
			w.add("customer_party_id = $%d", *f.CustomerID)
		}
		switch f.Status {
		case "":
		case InvoiceDraft, InvoiceConfirmed, InvoicePartiallyDelivered, InvoiceCompleted, InvoiceCancelledRemaining:
			w.add("status = $%d", f.Status)
		default:
			return errInvalid(map[string]string{"status": "unknown"})
		}
		if f.From != nil {
			w.add("invoice_date >= $%d", *f.From)
		}
		if f.To != nil {
			w.add("invoice_date <= $%d", *f.To)
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+invoiceColumns+` FROM invoices`+w.sql()+tail, w.args...); err != nil {
			return wrap("list invoices", err)
		}
		ids := make([]uuid.UUID, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		paid, err := s.debt.InvoicesPaid(ctx, q, ids)
		if err != nil {
			return err
		}
		for i := range out {
			out[i].setPayment(invoicePaymentOf(out[i], paid[out[i].ID]))
		}
		return nil
	})
	return out, err
}

// InvoiceVersions lists the confirmed versions of an invoice, oldest first.
func (s *Service) InvoiceVersions(ctx context.Context, scope Scope, id uuid.UUID) ([]InvoiceVersion, error) {
	var out []InvoiceVersion
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := getInvoice(ctx, q, id, false); err != nil {
			return err
		}
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT id, version_no, snapshot, change_summary, material_change, created_by, created_at
			FROM invoice_versions WHERE invoice_id = $1 ORDER BY version_no`, id); err != nil {
			return wrap("read invoice versions", err)
		}
		return nil
	})
	return out, err
}

// SetPaidAmount brings what the customer paid toward a draft invoice to in.Amount. An increase is
// one deposit (PT) earmarked for the invoice, which its stock outs consume first; a decrease
// refunds the advance of the latest deposits (PC advance_refund). Money already allocated or a
// transfer still waiting for verification cannot be refunded here (409
// paid_amount_not_refundable).
func (s *Service) SetPaidAmount(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in PaidAmountInput) (posting.Response, error) {
	fields := map[string]string{}
	if in.Amount.IsNegative() {
		fields["amount"] = "negative"
	}
	checkText(fields, "note", in.Note, maxNoteLen)
	if len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	payKey := func(n int) string { return paymentKey(ScopeInvoicePay, key, n) }
	var plan []PaymentInput
	var date time.Time
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		inv, err := getInvoice(ctx, q, id, false)
		if err != nil {
			return err
		}
		if inv.Status != InvoiceDraft {
			return errConflict(CodeInvoiceNotEditable, "The paid amount can only be set on a draft invoice")
		}
		_, deposits, err := s.paidKept(ctx, q, id)
		if err != nil {
			return err
		}
		if plan, err = planPaid(inv, deposits, in, payKey); err != nil {
			return err
		}
		if in.MethodID != nil {
			if _, err := s.checkMethod(ctx, q, *in.MethodID, "payment_method_id"); err != nil {
				return err
			}
		}
		date, err = s.today(ctx, q, actor.TenantID)
		return err
	})
	if err != nil {
		return s.postRefused(ctx, actor, key, ScopeInvoicePay, err)
	}
	cmd := command{scope: ScopeInvoicePay, date: date, extra: paymentPrefixes(plan)}
	addPaymentLocks(&cmd.locks, plan)
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		inv, err := getInvoice(ctx, tx, id, true)
		if err != nil {
			return posting.Result{}, err
		}
		if inv.Status != InvoiceDraft {
			return posting.Result{}, errConflict(CodeInvoiceNotEditable, "The paid amount can only be set on a draft invoice")
		}
		kept, deposits, err := s.paidKept(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		again, err := planPaid(inv, deposits, in, payKey)
		if err != nil {
			return posting.Result{}, err
		}
		if !samePayments(plan, again) {
			return posting.Result{}, errConflict(CodePaidAmountChanged, "The payments of the invoice changed meanwhile; reload it and try again")
		}
		recorded := make([]Payment, 0, len(again))
		for _, p := range again {
			pay, err := s.debt.RecordPayment(ctx, tx, p)
			if err != nil {
				return posting.Result{}, err
			}
			recorded = append(recorded, pay)
		}
		out, err := s.invoiceDetail(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		tx.Audit(audit.Event{Action: ScopeInvoicePay, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo,
			Before: map[string]any{"paid_amount": kept},
			After:  map[string]any{"paid_amount": in.Amount, "payments": recorded}})
		return posting.Result{Status: http.StatusOK, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// planPaid lists the payments that bring the deposits of a draft invoice to in.Amount: one deposit
// for an increase, refunds of the latest confirmed deposits' advance for a decrease.
func planPaid(inv Invoice, deposits []Deposit, in PaidAmountInput, payKey func(int) string) ([]PaymentInput, error) {
	kept := money.ZeroMoney
	for _, d := range deposits {
		kept = kept.Add(d.Kept())
	}
	diff := in.Amount.Sub(kept)
	if diff.IsZero() {
		return nil, errConflict(CodeNoChange, "The customer has already paid this amount")
	}
	payer, invoiceID := inv.PayerID, inv.ID
	note := ""
	if in.Note != nil {
		note = *in.Note
	}
	if diff.IsPositive() {
		if in.MethodID == nil || *in.MethodID == uuid.Nil {
			return nil, errInvalid(map[string]string{"payment_method_id": "required"})
		}
		return []PaymentInput{{
			Direction: DirectionIn, PartyID: &payer, Purpose: PurposeDeposit, MethodID: *in.MethodID, Amount: diff,
			SourceType: PaymentSourceInvoice, SourceID: &invoiceID, EarmarkedInvoiceID: &invoiceID, Note: note, Key: payKey(0),
		}}, nil
	}
	need := diff.Neg()
	var out []PaymentInput
	for _, d := range slices.Backward(deposits) {
		if !need.IsPositive() {
			break
		}
		if d.Pending || !d.Unallocated.IsPositive() {
			continue
		}
		take := minMoney(need, d.Unallocated)
		refundOf := d.ID
		out = append(out, PaymentInput{
			Direction: DirectionOut, PartyID: &payer, Purpose: PurposeAdvanceRefund, MethodID: d.MethodID, Amount: take,
			SourceType: PaymentSourceInvoice, SourceID: &invoiceID, RefundOf: &refundOf, Note: note, Key: payKey(len(out)),
		})
		need = need.Sub(take)
	}
	if need.IsPositive() {
		return nil, errConflict(CodePaidNotRefundable,
			"Part of the paid amount cannot be refunded here: it is a transfer waiting for verification or was already used")
	}
	return out, nil
}

// samePayments reports whether two payment plans record the same payments.
func samePayments(a, b []PaymentInput) bool {
	return slices.EqualFunc(a, b, func(x, y PaymentInput) bool {
		return x.Direction == y.Direction && x.Purpose == y.Purpose && x.MethodID == y.MethodID && x.Amount.Equal(y.Amount) &&
			sameUUID(x.RefundOf, y.RefundOf) && x.Key == y.Key
	})
}
