package sales

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Column lists of the sales headers, in model field order.
const (
	quoteColumns = `id, doc_no, customer_party_id, quote_date, valid_until, status, campaign_id, note, converted_invoice_id,
	copied_from_id, revision_no, created_at, updated_at, version, goods_amount, discount_amount, tax_amount, total_amount`

	invoiceColumns = `id, doc_no, customer_party_id, payer_party_id, invoice_date, due_date, status, delivered_amount, quote_id,
	campaign_id, copied_from_id, note, current_version, confirmed_at, confirmed_by, created_at, updated_at, version,
	goods_amount, discount_amount, tax_amount, total_amount`

	// A stock out does not store its BH: the BH points at the stock out.
	stockOutColumns = `so.id, so.doc_no, so.invoice_id, so.business_date, so.note, so.status, so.posted_at, so.posted_by,
	so.reversal_of, (SELECT d.id FROM sales_documents d WHERE d.stock_out_id = so.id) AS sales_document_id`

	documentColumns = `id, doc_no, source, stock_out_id, invoice_id, pos_cart_id, shift_id, customer_party_id, payer_party_id,
	business_date, cogs_amount, campaign_id, offline_client_id, offline_created_at, status, posted_at, posted_by,
	reversal_of, cash_tendered_amount, change_amount, goods_amount, discount_amount, tax_amount, total_amount`

	shiftColumns = `id, doc_no, business_date, opened_at, closed_at, status`

	cartColumns = `id, doc_no, customer_party_id, label, status, total_amount, sales_document_id, copied_from_id, created_at,
	updated_at, version`

	returnColumns = `id, doc_no, customer_party_id, sales_document_id, reason_id, business_date, total_amount,
	debt_reduction_amount, refund_due_amount, exchange_id, status, posted_at, posted_by`

	returnLineColumns = `id, sales_document_line_id, sku_id, sku_code_snap, name_snap, variant_label_snap, unit_id,
	unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, unit_price, line_amount, unit_cost, cost_source,
	restock_to, lot_id`

	exchangeColumns = `id, doc_no, customer_party_id, original_sales_document_id, sales_return_id, new_sales_document_id,
	returned_amount, taken_amount, debt_reduction_amount, customer_pays_amount, store_refunds_amount, fund_account_id,
	business_date, posted_at, posted_by, created_at`

	syncExceptionColumns = `id, offline_client_id, device_session_id, payload, error_code, error_detail, status,
	resolution_doc_id, resolved_by, resolved_at, created_at`
)

// getRow reads one row of table by id into dst; forUpdate locks it (alias names the table in a
// FROM with an alias).
func getRow(ctx context.Context, q db.Querier, dst any, what, columns, from, alias string, id uuid.UUID, forUpdate bool) error {
	query := `SELECT ` + columns + ` FROM ` + from + ` WHERE ` + alias + `id = $1`
	if forUpdate {
		lock := " FOR UPDATE"
		if alias != "" {
			lock += " OF " + alias[:len(alias)-1]
		}
		query += lock
	}
	err := sqlx.GetContext(ctx, q, dst, query, id)
	if db.IsNoRows(err) {
		return errNotFound(what)
	}
	if err != nil {
		return fmt.Errorf("sales: read %s: %w", what, err)
	}
	return nil
}

func getQuote(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Quote, error) {
	var out Quote
	err := getRow(ctx, q, &out, "Quote", quoteColumns, "quotes", "", id, forUpdate)
	return out, err
}

func getInvoice(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Invoice, error) {
	var out Invoice
	err := getRow(ctx, q, &out, "Invoice", invoiceColumns, "invoices", "", id, forUpdate)
	return out, err
}

func getStockOut(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (StockOut, error) {
	var out StockOut
	err := getRow(ctx, q, &out, "Stock out", stockOutColumns, "stock_outs so", "so.", id, forUpdate)
	return out, err
}

func getDocument(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (SalesDocument, error) {
	var out SalesDocument
	err := getRow(ctx, q, &out, "Sales document", documentColumns, "sales_documents", "", id, forUpdate)
	return out, err
}

func getCart(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Cart, error) {
	var out Cart
	err := getRow(ctx, q, &out, "Cart", cartColumns, "pos_carts", "", id, forUpdate)
	return out, err
}

func getReturn(ctx context.Context, q db.Querier, id uuid.UUID) (SalesReturn, error) {
	var out SalesReturn
	err := getRow(ctx, q, &out, "Sales return", returnColumns, "sales_returns", "", id, false)
	return out, err
}

func getExchange(ctx context.Context, q db.Querier, id uuid.UUID) (Exchange, error) {
	var out Exchange
	err := getRow(ctx, q, &out, "Exchange", exchangeColumns, "exchanges", "", id, false)
	return out, err
}

func getSyncException(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (SyncException, error) {
	var out SyncException
	err := getRow(ctx, q, &out, "Sync exception", syncExceptionColumns, "pos_sync_exceptions", "", id, forUpdate)
	return out, err
}

// Lines.

func quoteLinesOf(ctx context.Context, q db.Querier, quoteID uuid.UUID) ([]QuoteLine, error) {
	var out []QuoteLine
	err := quoteLines.load(ctx, q, &out, quoteID)
	return out, err
}

func invoiceLinesOf(ctx context.Context, q db.Querier, invoiceID uuid.UUID) ([]InvoiceLine, error) {
	var out []InvoiceLine
	err := invoiceLines.load(ctx, q, &out, invoiceID)
	return out, err
}

// lockInvoiceLines reads the lines of an invoice and locks them, for a posting that changes their
// fulfilment quantities.
func lockInvoiceLines(ctx context.Context, q db.Querier, invoiceID uuid.UUID) ([]InvoiceLine, error) {
	var out []InvoiceLine
	if err := sqlx.SelectContext(ctx, q, &out, invoiceLines.sel+" FOR UPDATE", invoiceID); err != nil {
		return nil, fmt.Errorf("sales: lock invoice lines: %w", err)
	}
	return out, nil
}

func documentLinesOf(ctx context.Context, q db.Querier, documentID uuid.UUID) ([]DocumentLine, error) {
	var out []DocumentLine
	err := documentLines.load(ctx, q, &out, documentID)
	return out, err
}

func lockDocumentLines(ctx context.Context, q db.Querier, documentID uuid.UUID) ([]DocumentLine, error) {
	var out []DocumentLine
	if err := sqlx.SelectContext(ctx, q, &out, documentLines.sel+" FOR UPDATE", documentID); err != nil {
		return nil, fmt.Errorf("sales: lock sales document lines: %w", err)
	}
	return out, nil
}

func cartLinesOf(ctx context.Context, q db.Querier, cartID uuid.UUID) ([]Line, error) {
	var out []Line
	err := cartLines.load(ctx, q, &out, cartID)
	return out, err
}

func stockOutLinesOf(ctx context.Context, q db.Querier, stockOutID uuid.UUID) ([]StockOutLine, error) {
	var out []StockOutLine
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT id, invoice_line_id, sku_id, color_id, base_qty FROM stock_out_lines WHERE stock_out_id = $1 ORDER BY id`,
		stockOutID); err != nil {
		return nil, fmt.Errorf("sales: read stock out lines: %w", err)
	}
	return out, nil
}

func returnLinesOf(ctx context.Context, q db.Querier, returnID uuid.UUID) ([]ReturnLine, error) {
	var out []ReturnLine
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+returnLineColumns+` FROM sales_return_lines
		WHERE sales_return_id = $1 ORDER BY id`, returnID); err != nil {
		return nil, fmt.Errorf("sales: read sales return lines: %w", err)
	}
	return out, nil
}

// checkReason verifies that id names an active reason of kind.
func checkReason(ctx context.Context, q db.Querier, id uuid.UUID, kind, field string) error {
	var got string
	var active bool
	err := q.QueryRowxContext(ctx, `SELECT kind, active FROM reasons WHERE id = $1`, id).Scan(&got, &active)
	if db.IsNoRows(err) {
		return errInvalid(map[string]string{field: "not_found"})
	}
	if err != nil {
		return fmt.Errorf("sales: read reason: %w", err)
	}
	switch {
	case !active:
		return errInvalid(map[string]string{field: "inactive"})
	case got != kind:
		return errInvalid(map[string]string{field: "wrong_kind"})
	}
	return nil
}

// checkCustomer verifies that partyID is a customer the store still sells to.
func checkCustomer(ctx context.Context, q db.Querier, partyID uuid.UUID, field string) error {
	var status string
	err := q.QueryRowxContext(ctx, `SELECT status FROM customer_profiles WHERE party_id = $1`, partyID).Scan(&status)
	if db.IsNoRows(err) {
		return errInvalid(map[string]string{field: "not_a_customer"})
	}
	if err != nil {
		return fmt.Errorf("sales: read customer: %w", err)
	}
	if status != "active" {
		return errInvalid(map[string]string{field: "inactive"})
	}
	return nil
}

// paymentTerms is the payment terms of a customer, falling back to the tenant default.
func paymentTerms(ctx context.Context, q db.Querier, partyID uuid.UUID, fallback int) (int, error) {
	var days int
	err := q.QueryRowxContext(ctx, `SELECT payment_terms_days FROM customer_profiles WHERE party_id = $1`, partyID).Scan(&days)
	if db.IsNoRows(err) {
		return fallback, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sales: read payment terms: %w", err)
	}
	if days == 0 {
		return fallback, nil
	}
	return days, nil
}

// where builds the WHERE clause of a list query. Each condition holds one %d verb per argument,
// replaced by the argument's placeholder number.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, args ...any) {
	nums := make([]any, len(args))
	for i, a := range args {
		w.args = append(w.args, a)
		nums[i] = len(w.args)
	}
	w.conds = append(w.conds, fmt.Sprintf(cond, nums...))
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// page appends the newest-first keyset condition of p on the created_at and id columns of alias
// ("" or "x.") and returns the ORDER BY and LIMIT tail.
func (w *where) page(p ListPage, alias string) string {
	if p.AfterAt != nil {
		w.add("("+alias+"created_at, "+alias+"id) < ($%d, $%d)", *p.AfterAt, p.AfterID)
	}
	limit := p.Limit
	if limit <= 0 || limit > maxListLimit {
		limit = maxListLimit
	}
	return fmt.Sprintf(" ORDER BY %screated_at DESC, %sid DESC LIMIT %d", alias, alias, limit)
}

// ListPage is the keyset page of a newest-first list: Limit rows created before (AfterAt,
// AfterID).
type ListPage struct {
	Limit   int
	AfterAt *time.Time
	AfterID uuid.UUID
}

const maxListLimit = 201

// cloneLines copies lines under new ids, keeping each child under its copied parent and each mix
// pack under its copied key.
func cloneLines(in []Line) []Line {
	newIDs := make(map[uuid.UUID]uuid.UUID, len(in))
	for _, l := range in {
		newIDs[l.ID] = ids.New()
	}
	out := make([]Line, len(in))
	for i, l := range in {
		l.ID = newIDs[l.ID]
		if l.ParentLineID != nil {
			p := newIDs[*l.ParentLineID]
			l.ParentLineID = &p
		}
		if l.MixKey != nil {
			if old, err := uuid.Parse(*l.MixKey); err == nil {
				if n, ok := newIDs[old]; ok {
					k := n.String()
					l.MixKey = &k
				}
			}
		}
		out[i] = l
	}
	return out
}

// Helpers.

func wrap(what string, err error) error { return fmt.Errorf("sales: %s: %w", what, err) }

// checkText flags a text field longer than limit characters.
func checkText(fields map[string]string, name string, v *string, limit int) {
	if v != nil && utf8.RuneCountInString(*v) > limit {
		fields[name] = "too_long"
	}
}

// actorID is the user a document is posted by (nil for the system actor).
func actorID(a audit.Actor) *uuid.UUID {
	if a.ID == uuid.Nil {
		return nil
	}
	id := a.ID
	return &id
}

// Audit entity types of the sales documents.
const (
	entityQuote         = "quote"
	entityInvoice       = "invoice"
	entityStockOut      = "stock_out"
	entityDocument      = "sales_document"
	entityCart          = "pos_cart"
	entityShift         = "shift"
	entityReturn        = "sales_return"
	entityExchange      = "exchange"
	entitySyncException = "pos_sync_exception"
)

func sortedUnique(in []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if id != uuid.Nil {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, compareUUID)
	return slices.Compact(out)
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func minQty(a, b money.Qty) money.Qty {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func minMoney(a, b money.Money) money.Money {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func ptr[T any](v T) *T { return &v }
