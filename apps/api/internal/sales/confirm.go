package sales

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Exceptions are the owner's confirmations a command brings (exception_confirmation_ids) and the
// payload hash they must bind.
type Exceptions struct {
	IDs  []uuid.UUID
	Hash []byte
}

// bind ties the confirmations to the document state they were given for.
func (e Exceptions) bind(docType string, docID uuid.UUID, version int) confirmations {
	return confirmations{IDs: e.IDs, DocType: docType, DocID: docID, DocVersion: version, Hash: e.Hash}
}

// ReviseInput revises a confirmed invoice into a new version. Draft nil keeps the lines; a line
// kept by id with no unit price keeps the price it was confirmed at.
type ReviseInput struct {
	ChangeSummary string      `json:"change_summary"`
	DueDate       *Date       `json:"due_date,omitempty"`
	Note          *string     `json:"note,omitempty"`
	Draft         *DraftInput `json:"draft,omitempty"`
}

// InvoiceCheck is the dry run of a confirmation: what would block it, which owner confirmations it
// needs and how much of each line the free stock covers.
type InvoiceCheck struct {
	ColorRequired       []int       `json:"color_required_line_nos"`
	BelowMinPrice       []int       `json:"below_min_price_line_nos"`
	CreditLimitExceeded bool        `json:"credit_limit_exceeded"`
	ExceptionsRequired  []string    `json:"exceptions_required"`
	Lines               []LineCheck `json:"lines"`
	// Ready is true when the invoice confirms without any confirmation or change.
	Ready bool `json:"ready"`
}

// LineCheck is how much of a stocked line confirmation would reserve and backorder.
type LineCheck struct {
	LineID     uuid.UUID `json:"line_id"`
	LineNo     int       `json:"line_no"`
	SkuID      uuid.UUID `json:"sku_id"`
	Remaining  money.Qty `json:"remaining_base_qty"`
	Reservable money.Qty `json:"reservable_base_qty"`
	Backorder  money.Qty `json:"backorder_base_qty"`
}

// ConfirmInvoice confirms a draft invoice at version as version 1: every stocked line must have its
// color, a payer over the credit limit or a line under its minimum price needs the owner's
// confirmation, and the free stock is reserved line by line with the rest backordered.
func (s *Service) ConfirmInvoice(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, version int, exc Exceptions) (posting.Response, error) {
	date, skus, err := s.invoiceSkus(ctx, actor.TenantID, id, nil)
	if err != nil {
		return posting.Response{}, err
	}
	cmd := command{scope: ScopeInvoiceConfirm, date: date, locks: stockLocks(skus)}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		inv, err := getInvoice(ctx, tx, id, true)
		if err != nil {
			return posting.Result{}, err
		}
		if inv.Version != version {
			return posting.Result{}, errStale()
		}
		if inv.Status != InvoiceDraft {
			return posting.Result{}, errTransition(inv.Status, "confirmed")
		}
		if err := checkParties(ctx, tx, inv.CustomerID, inv.PayerID); err != nil {
			return posting.Result{}, err
		}
		st, err := loadSettings(ctx, tx)
		if err != nil {
			return posting.Result{}, err
		}
		lines, err := lockInvoiceLines(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		if err := s.fillColors(ctx, tx, lines); err != nil {
			return posting.Result{}, err
		}
		c := exc.bind(entityInvoice, id, inv.Version)
		if err := s.guard(ctx, tx, inv, plainLines(lines), st.PricesIncludeTax, inv.Total, c); err != nil {
			return posting.Result{}, err
		}
		// An order uses its promotions once, when it is confirmed; its deliveries do not count again.
		if err := s.recordPromotions(ctx, tx, tx.TenantID(), entityInvoice, id, promotionsOf(plainLines(lines))); err != nil {
			return posting.Result{}, err
		}
		if _, err := s.rebalance(ctx, tx, lines, inv.DocNo, setOf(skus)); err != nil {
			return posting.Result{}, err
		}
		sets := []db.Set{
			{Column: "status", Value: InvoiceConfirmed}, {Column: "current_version", Value: 1},
			{Column: "confirmed_at", Value: tx.Now()}, {Column: "confirmed_by", Value: actorID(tx.Actor())},
			{Column: "idempotency_key", Value: key.Value},
		}
		if inv.DueDate == nil {
			due, err := dueDateOf(ctx, tx, inv, st)
			if err != nil {
				return posting.Result{}, err
			}
			sets = append(sets, db.Set{Column: "due_date", Value: due})
		}
		if _, err := db.UpdateWithVersion(ctx, tx, "invoices", id, version, tx.Now(), sets...); err != nil {
			return posting.Result{}, err
		}
		out, err := s.saveVersion(ctx, tx, id, 1, nil, false)
		if err != nil {
			return posting.Result{}, err
		}
		tx.Audit(audit.Event{Action: ScopeInvoiceConfirm, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo,
			Before: map[string]any{"status": inv.Status}, After: out})
		tx.Publish(outbox.Event{Topic: TopicInvoiceConfirm, Payload: map[string]any{
			"invoice_id": id, "doc_no": inv.DocNo, "payer_party_id": inv.PayerID, "total_amount": inv.Total,
		}})
		return posting.Result{Status: http.StatusOK, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// CheckInvoice runs the checks of ConfirmInvoice on a draft invoice without writing anything.
func (s *Service) CheckInvoice(ctx context.Context, scope Scope, id uuid.UUID) (InvoiceCheck, error) {
	out := InvoiceCheck{ColorRequired: []int{}, BelowMinPrice: []int{}, ExceptionsRequired: []string{}, Lines: []LineCheck{}}
	err := s.read(ctx, scope, func(q db.Querier) error {
		inv, err := getInvoice(ctx, q, id, false)
		if err != nil {
			return err
		}
		if inv.Status != InvoiceDraft {
			return errTransition(inv.Status, "checked")
		}
		lines, err := invoiceLinesOf(ctx, q, id)
		if err != nil {
			return err
		}
		st, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		plain := plainLines(lines)
		if err := s.applyColors(ctx, q, plain, identityOrigin(len(plain)), colorStrict); err != nil {
			e, ok := apperr.As(err)
			if !ok || e.Code != CodeColorRequired {
				return err
			}
			for k := range e.Fields {
				if n, _, ok := parseLineField(k); ok && n >= 0 && n < len(plain) {
					out.ColorRequired = append(out.ColorRequired, plain[n].LineNo)
				}
			}
			slices.Sort(out.ColorRequired)
		}
		for _, l := range plain {
			if l.belowMin(st.PricesIncludeTax) {
				out.BelowMinPrice = append(out.BelowMinPrice, l.LineNo)
			}
		}
		today, err := s.today(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		switch err := s.debt.CheckCredit(ctx, q, CreditQuery{PartyID: inv.PayerID, OrderID: &inv.ID, Undelivered: inv.Total, AsOf: today}); {
		case err == nil:
		case apperr.KindOf(err) == apperr.ExceptionRequired:
			out.CreditLimitExceeded = true
			out.ExceptionsRequired = append(out.ExceptionsRequired, string(exception.CreditLimitExceeded))
		default:
			return err
		}
		if len(out.BelowMinPrice) > 0 {
			out.ExceptionsRequired = append(out.ExceptionsRequired, string(exception.BelowMinPrice))
		}
		avail, err := s.stock.Available(ctx, q, lineSkus(lines))
		if err != nil {
			return err
		}
		holds := planHolds(lines, avail)
		for i, l := range lines {
			if !holdsStock(l) {
				continue
			}
			rem := l.remaining()
			out.Lines = append(out.Lines, LineCheck{
				LineID: l.ID, LineNo: l.LineNo, SkuID: *l.SkuID, Remaining: rem, Reservable: holds[i], Backorder: rem.Sub(holds[i]),
			})
		}
		out.Ready = len(out.ColorRequired) == 0 && len(out.ExceptionsRequired) == 0
		return nil
	})
	return out, err
}

// ReviseInvoice saves a new version of a confirmed or partially delivered invoice at version. A
// line with deliveries keeps its SKU, color, unit and price and cannot drop below what was
// delivered; a removed line gives its reservation back. A change of the lines or totals is
// material: the color, minimum price and credit checks run again and earlier confirmations no
// longer count, and the reservations follow the new quantities.
func (s *Service) ReviseInvoice(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, version int, in ReviseInput, exc Exceptions) (posting.Response, error) {
	fields := map[string]string{}
	if strings.TrimSpace(in.ChangeSummary) == "" {
		fields["change_summary"] = "required"
	}
	checkText(fields, "change_summary", &in.ChangeSummary, maxNoteLen)
	checkText(fields, "note", in.Note, maxNoteLen)
	if len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	date, skus, err := s.invoiceSkus(ctx, actor.TenantID, id, in.Draft)
	if err != nil {
		return posting.Response{}, err
	}
	cmd := command{scope: ScopeInvoiceRevise, date: date, locks: stockLocks(skus)}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		inv, err := getInvoice(ctx, tx, id, true)
		if err != nil {
			return posting.Result{}, err
		}
		if inv.Version != version {
			return posting.Result{}, errStale()
		}
		if !invoiceOpen(inv.Status) {
			return posting.Result{}, errTransition(inv.Status, "revised")
		}
		before, err := s.invoiceDetail(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		var sets []db.Set
		dueDate := inv.DueDate
		if in.DueDate != nil {
			dueDate = nonZeroDate(in.DueDate)
			if !sameDate(dueDate, inv.DueDate) {
				sets = append(sets, db.Set{Column: "due_date", Value: dueDate})
			}
		}
		hdr := map[string]string{}
		validateInvoiceHeader(hdr, inv.InvoiceDate, dueDate, nil)
		if len(hdr) > 0 {
			return posting.Result{}, errInvalid(hdr)
		}
		if in.Note != nil && !sameText(in.Note, inv.Note) {
			sets = append(sets, db.Set{Column: "note", Value: *in.Note})
		}
		material := false
		if in.Draft != nil {
			old, err := lockInvoiceLines(ctx, tx, id)
			if err != nil {
				return posting.Result{}, err
			}
			changed, totals, err := s.reviseLines(ctx, tx, inv, old, *in.Draft, exc.bind(entityInvoice, id, inv.Version))
			if err != nil {
				return posting.Result{}, err
			}
			if changed {
				material = true
				sets = append(sets, totalSets(totals)...)
			}
		}
		if len(sets) == 0 {
			return posting.Result{}, errConflict(CodeNoChange, "The revision changes nothing")
		}
		status := inv.Status
		if material {
			lines, err := lockInvoiceLines(ctx, tx, id)
			if err != nil {
				return posting.Result{}, err
			}
			if _, err := s.rebalance(ctx, tx, lines, inv.DocNo, setOf(skus)); err != nil {
				return posting.Result{}, err
			}
			status = deliveryStatus(inv.Status, lines)
		}
		next := inv.CurrentVersion + 1
		sets = append(sets, db.Set{Column: "current_version", Value: next}, db.Set{Column: "status", Value: status})
		if _, err := db.UpdateWithVersion(ctx, tx, "invoices", id, version, tx.Now(), sets...); err != nil {
			return posting.Result{}, err
		}
		summary := strings.TrimSpace(in.ChangeSummary)
		out, err := s.saveVersion(ctx, tx, id, next, &summary, material)
		if err != nil {
			return posting.Result{}, err
		}
		tx.Audit(audit.Event{Action: ScopeInvoiceRevise, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo, Before: before, After: out})
		return posting.Result{Status: http.StatusOK, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// reviseLines prices draft as the new lines of a confirmed invoice and stores them, reporting
// whether any line or total changed. Lines are matched to the stored ones by id; gifts and combo
// components, which pricing and the expander rebuild, are matched by SKU so their deliveries stay
// with them.
func (s *Service) reviseLines(ctx context.Context, tx posting.Tx, inv Invoice, old []InvoiceLine, draft DraftInput, c confirmations) (bool, Totals, error) {
	byID, keep := keptLines(old)
	draft, injected := keptPrices(draft, byID)
	built, err := s.buildLines(ctx, tx, tx.TenantID(), lineBuild{PartyID: &inv.CustomerID, Date: inv.InvoiceDate.Time, Draft: draft, Keep: keep})
	if err != nil {
		return false, Totals{}, err
	}
	lines := built.Lines
	for i := range lines {
		if injected[lines[i].ID] {
			lines[i].PriceSource = byID[lines[i].ID].PriceSource
		}
	}

	fields := map[string]string{}
	for i, l := range lines {
		o, ok := byID[l.ID]
		if !ok {
			continue
		}
		closed := o.Delivered.Add(o.Cancelled)
		if !closed.IsPositive() {
			continue
		}
		if !sameUUID(l.SkuID, o.SkuID) || !sameUUID(l.ColorID, o.ColorID) || l.UnitID != o.UnitID || l.UnitPrice.Cmp(o.UnitPrice) != 0 {
			fields[originField(built.Origin[i], "sku_id")] = "delivered_line_fixed"
		}
		if l.BaseQty.Cmp(closed) < 0 {
			fields[originField(built.Origin[i], "qty")] = "below_delivered"
		}
	}
	if len(fields) > 0 {
		return false, Totals{}, errInvalid(fields)
	}
	renewChangedSkus(lines, byID)
	matchRebuilt(lines, old)
	if err := s.applyColors(ctx, tx, lines, built.Origin, colorStrict); err != nil {
		return false, Totals{}, err
	}

	seen := make(map[uuid.UUID]bool, len(lines))
	for _, l := range lines {
		seen[l.ID] = true
	}
	var removed []InvoiceLine
	for _, o := range old {
		if seen[o.ID] {
			continue
		}
		if o.Delivered.Add(o.Cancelled).IsPositive() {
			return false, Totals{}, errInvalid(map[string]string{"lines": "delivered_line_removed"})
		}
		removed = append(removed, o)
	}
	if len(removed) == 0 && len(lines) == len(old) && sameTotals(built.Totals, inv.Totals) && sameLineSet(lines, byID) {
		return false, built.Totals, nil
	}
	if err := s.guard(ctx, tx, inv, lines, built.IncludeTax, built.Totals.Total.Sub(inv.Delivered), c); err != nil {
		return false, Totals{}, err
	}
	if err := s.storeRevisedLines(ctx, tx, inv.ID, lines, byID, removed); err != nil {
		return false, Totals{}, err
	}
	return true, built.Totals, nil
}

// keptLines indexes the stored lines of an invoice by id, as stored and as the line build keeps them.
func keptLines(old []InvoiceLine) (map[uuid.UUID]InvoiceLine, map[uuid.UUID]Line) {
	byID := make(map[uuid.UUID]InvoiceLine, len(old))
	keep := make(map[uuid.UUID]Line, len(old))
	for _, l := range old {
		byID[l.ID] = l
		keep[l.ID] = l.Line
	}
	return byID, keep
}

// keptPrices gives a kept item or combo the price it was confirmed at unless the request types one;
// the price lists of today must not reprice what the customer agreed to. It returns the priced
// copy of draft and the ids of the lines that got their stored price.
func keptPrices(draft DraftInput, byID map[uuid.UUID]InvoiceLine) (DraftInput, map[uuid.UUID]bool) {
	draft.Lines = slices.Clone(draft.Lines)
	injected := map[uuid.UUID]bool{}
	for i, r := range draft.Lines {
		if r.ID == nil || r.SkuID == nil || r.UnitPrice != nil {
			continue
		}
		o, ok := byID[*r.ID]
		if !ok || (o.Kind != KindItem && o.Kind != KindCombo) || o.SkuID == nil || *o.SkuID != *r.SkuID {
			continue
		}
		if r.UnitID != nil && *r.UnitID != o.UnitID {
			continue
		}
		draft.Lines[i].UnitPrice = ptr(o.UnitPrice)
		injected[o.ID] = true
	}
	return draft, injected
}

// renewChangedSkus gives a kept line whose SKU changed a new id: its reservation holds the old SKU,
// so the line is replaced rather than edited. Lines with deliveries never get here.
func renewChangedSkus(lines []Line, old map[uuid.UUID]InvoiceLine) {
	for i := range lines {
		o, ok := old[lines[i].ID]
		if !ok || sameUUID(lines[i].SkuID, o.SkuID) {
			continue
		}
		prev, next := lines[i].ID, ids.New()
		lines[i].ID = next
		for j := range lines {
			if lines[j].ParentLineID != nil && *lines[j].ParentLineID == prev {
				lines[j].ParentLineID = ptr(next)
			}
		}
	}
}

// matchRebuilt gives a rebuilt gift or combo component the id of the stored line of the same
// kind, SKU, unit and parent, so its reservation and deliveries carry over; a gift without a
// color keeps the stored one.
func matchRebuilt(lines []Line, old []InvoiceLine) {
	taken := make(map[uuid.UUID]bool, len(lines))
	for _, l := range lines {
		taken[l.ID] = true
	}
	for i := range lines {
		l := &lines[i]
		if l.Kind != KindGift && l.Kind != KindComboComponent {
			continue
		}
		for _, o := range old {
			if taken[o.ID] || o.Kind != l.Kind || !sameUUID(o.SkuID, l.SkuID) || o.UnitID != l.UnitID || !sameUUID(o.ParentLineID, l.ParentLineID) {
				continue
			}
			taken[o.ID] = true
			l.ID = o.ID
			if l.ColorID == nil {
				l.ColorID = o.ColorID
			}
			break
		}
	}
}

// storeRevisedLines writes the revised lines: new lines are inserted, kept lines are updated with
// their reservation cut to what they still have to deliver, and removed lines give their
// reservation back and are deleted. Line numbers are moved out of the way first because
// (invoice_id, line_no) is unique row by row.
func (s *Service) storeRevisedLines(ctx context.Context, tx posting.Tx, invoiceID uuid.UUID, lines []Line, old map[uuid.UUID]InvoiceLine, removed []InvoiceLine) error {
	if _, err := tx.ExecContext(ctx, `UPDATE invoice_lines SET line_no = line_no + $2 WHERE invoice_id = $1`, invoiceID, maxLines*10); err != nil {
		return wrap("renumber invoice lines", err)
	}
	var fresh []Line
	for _, l := range lines {
		if _, ok := old[l.ID]; !ok {
			fresh = append(fresh, l)
		}
	}
	if err := invoiceLines.insert(ctx, tx, tx.TenantID(), invoiceID, fresh, newInvoiceLineExtras(len(fresh))); err != nil {
		return err
	}
	for _, l := range lines {
		o, ok := old[l.ID]
		if !ok {
			continue
		}
		reserved := o.Reserved
		if need := l.BaseQty.Sub(o.Delivered).Sub(o.Cancelled); reserved.Cmp(need) > 0 {
			excess := reserved.Sub(need)
			if err := s.releaseHeld(ctx, tx, l.ID, &excess); err != nil {
				return err
			}
			reserved = need
		}
		if err := invoiceLines.update(ctx, tx, l, []any{reserved, money.ZeroQty, o.Delivered, o.Cancelled}); err != nil {
			return err
		}
	}
	if len(removed) == 0 {
		return nil
	}
	removedIDs := make([]uuid.UUID, len(removed))
	for i, o := range removed {
		removedIDs[i] = o.ID
		if o.Reserved.IsPositive() {
			if err := s.releaseHeld(ctx, tx, o.ID, nil); err != nil {
				return err
			}
		}
	}
	// A line a reversed stock out once delivered is still referenced by that stock out.
	var used bool
	if err := tx.QueryRowxContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM stock_out_lines WHERE invoice_line_id = ANY($1))
		    OR EXISTS (SELECT 1 FROM sales_document_lines WHERE invoice_line_id = ANY($1))`, removedIDs).Scan(&used); err != nil {
		return wrap("read invoice line deliveries", err)
	}
	if used {
		return errInvalid(map[string]string{"lines": "delivered_line_removed"})
	}
	for _, cond := range []string{" AND parent_line_id IS NOT NULL", ""} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_lines WHERE id = ANY($1)`+cond, removedIDs); err != nil {
			return wrap("delete invoice lines", err)
		}
	}
	return nil
}

// ReserveMore reserves backordered quantity of an open invoice at version from stock that became
// free (409 nothing_to_reserve when none did).
func (s *Service) ReserveMore(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, version int) (posting.Response, error) {
	date, skus, err := s.invoiceSkus(ctx, actor.TenantID, id, nil)
	if err != nil {
		return posting.Response{}, err
	}
	cmd := command{scope: ScopeInvoiceReserveMore, date: date, locks: stockLocks(skus)}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		inv, lines, err := lockOpenInvoice(ctx, tx, id, version, "reserved")
		if err != nil {
			return posting.Result{}, err
		}
		held, err := s.rebalance(ctx, tx, lines, inv.DocNo, setOf(skus))
		if err != nil {
			return posting.Result{}, err
		}
		if !held.IsPositive() {
			return posting.Result{}, errConflict(CodeNothingToReserve, "No backordered goods are in stock yet")
		}
		if _, err := db.UpdateWithVersion(ctx, tx, "invoices", id, version, tx.Now(), db.Set{Column: "status", Value: inv.Status}); err != nil {
			return posting.Result{}, err
		}
		out, err := s.invoiceDetail(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		tx.Audit(audit.Event{Action: ScopeInvoiceReserveMore, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo,
			Before: fulfilmentOf(lines), After: map[string]any{"reserved_base_qty": held, "lines": fulfilmentOf(out.Lines)}})
		return posting.Result{Status: http.StatusOK, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// CancelRemainder stops delivering the rest of an open invoice at version: every reservation is
// released and what was not delivered is cancelled. The invoice then counts only what was
// delivered; deposits beyond that stay the payer's advance.
func (s *Service) CancelRemainder(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, version int) (posting.Response, error) {
	date, skus, err := s.invoiceSkus(ctx, actor.TenantID, id, nil)
	if err != nil {
		return posting.Response{}, err
	}
	cmd := command{scope: ScopeInvoiceCancel, date: date, locks: stockLocks(skus)}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		inv, lines, err := lockOpenInvoice(ctx, tx, id, version, "cancelled")
		if err != nil {
			return posting.Result{}, err
		}
		before := fulfilmentOf(lines)
		cancelled := false
		for i := range lines {
			l := &lines[i]
			rem := l.remaining()
			if !holdsStock(*l) || !rem.IsPositive() {
				continue
			}
			if l.Reserved.IsPositive() {
				if err := s.releaseHeld(ctx, tx, l.ID, ptr(l.Reserved)); err != nil {
					return posting.Result{}, err
				}
			}
			l.Cancelled, l.Reserved, l.Backorder = l.Cancelled.Add(rem), money.ZeroQty, money.ZeroQty
			if err := updateFulfilment(ctx, tx, *l); err != nil {
				return posting.Result{}, err
			}
			cancelled = true
		}
		if !cancelled {
			return posting.Result{}, errConflict(CodeNothingToCancel, "Nothing is left to deliver on this invoice")
		}
		if _, err := db.UpdateWithVersion(ctx, tx, "invoices", id, version, tx.Now(),
			db.Set{Column: "status", Value: InvoiceCancelledRemaining}); err != nil {
			return posting.Result{}, err
		}
		out, err := s.invoiceDetail(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		tx.Audit(audit.Event{Action: ScopeInvoiceCancel, EntityType: entityInvoice, EntityID: id, DocNo: inv.DocNo,
			Before: map[string]any{"status": inv.Status, "lines": before},
			After:  map[string]any{"status": out.Status, "lines": fulfilmentOf(out.Lines)}})
		return posting.Result{Status: http.StatusOK, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// lockOpenInvoice locks an invoice at version that still has goods to deliver, and its lines.
func lockOpenInvoice(ctx context.Context, q db.Querier, id uuid.UUID, version int, action string) (Invoice, []InvoiceLine, error) {
	inv, err := getInvoice(ctx, q, id, true)
	if err != nil {
		return inv, nil, err
	}
	if inv.Version != version {
		return inv, nil, errStale()
	}
	if !invoiceOpen(inv.Status) {
		return inv, nil, errTransition(inv.Status, action)
	}
	lines, err := lockInvoiceLines(ctx, q, id)
	return inv, lines, err
}

// invoiceSkus reads, before a posting starts, the business date and the SKUs whose stock rows an
// invoice posting locks: those of its lines plus, for a revision to draft, those of the lines the
// draft builds.
func (s *Service) invoiceSkus(ctx context.Context, tenantID, id uuid.UUID, draft *DraftInput) (time.Time, []uuid.UUID, error) {
	var date time.Time
	var skus []uuid.UUID
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		inv, err := getInvoice(ctx, q, id, false)
		if err != nil {
			return err
		}
		if err := sqlx.SelectContext(ctx, q, &skus, `
			SELECT DISTINCT sku_id FROM invoice_lines WHERE invoice_id = $1 AND sku_id IS NOT NULL`, id); err != nil {
			return wrap("read invoice skus", err)
		}
		if date, err = s.today(ctx, q, tenantID); err != nil {
			return err
		}
		if draft == nil {
			return nil
		}
		more, err := s.revisionSkus(ctx, q, tenantID, inv, *draft)
		skus = append(skus, more...)
		return err
	})
	if err != nil {
		return time.Time{}, nil, err
	}
	return date, sortedUnique(skus), nil
}

// revisionSkus are the SKUs revising inv to draft takes stock of (see saleSkus).
func (s *Service) revisionSkus(ctx context.Context, q db.Querier, tenantID uuid.UUID, inv Invoice, draft DraftInput) ([]uuid.UUID, error) {
	old, err := invoiceLinesOf(ctx, q, inv.ID)
	if err != nil {
		return nil, err
	}
	byID, keep := keptLines(old)
	priced, _ := keptPrices(draft, byID)
	return s.saleSkus(ctx, q, tenantID, lineBuild{PartyID: &inv.CustomerID, Date: inv.InvoiceDate.Time, Draft: priced, Keep: keep})
}

// draftSkus are the SKUs a request draft names.
func draftSkus(d *DraftInput) []uuid.UUID {
	if d == nil {
		return nil
	}
	var out []uuid.UUID
	for _, l := range d.Lines {
		if l.SkuID != nil {
			out = append(out, *l.SkuID)
		}
		for _, p := range l.Parts {
			out = append(out, p.SkuID)
		}
	}
	return out
}

// stockLocks locks the stock rows of skus, the first rows any sales posting takes.
func stockLocks(skus []uuid.UUID) posting.LockSet {
	var set posting.LockSet
	if len(skus) > 0 {
		set.Add(posting.LockSkuStock, skus...)
	}
	return set
}

// stockedSkus are the SKUs whose stock the stocked lines of a built document take: the goods the
// request names, the components of its combos and the gifts its promotions add.
func stockedSkus(lines []Line) []uuid.UUID {
	var out []uuid.UUID
	for _, l := range lines {
		if stocked(l.Kind) && l.SkuID != nil {
			out = append(out, *l.SkuID)
		}
	}
	return out
}

// skuSet is the set of SKUs whose stock rows a posting locked.
type skuSet map[uuid.UUID]bool

func setOf(skus []uuid.UUID) skuSet {
	out := make(skuSet, len(skus))
	for _, id := range skus {
		out[id] = true
	}
	return out
}

// covers fails with 409 sale_changed when skus need the stock of a SKU the posting did not lock:
// a combo recipe or a promotion changed between the read that chose the locks and the posting.
// Taking that stock unlocked could sell into what reservations hold, so the client sends the
// request again and its posting locks what the lines now need.
func (k skuSet) covers(skus []uuid.UUID) error {
	for _, id := range skus {
		if !k[id] {
			return errConflict(CodeSaleChanged, "The goods of the sale changed while it was posting (combo or promotion); send it again")
		}
	}
	return nil
}

// fillColors enforces the color of every stocked line of a confirming invoice; a gift takes a color
// in stock and keeps it.
func (s *Service) fillColors(ctx context.Context, tx posting.Tx, lines []InvoiceLine) error {
	plain := plainLines(lines)
	if err := s.applyColors(ctx, tx, plain, identityOrigin(len(plain)), colorStrict); err != nil {
		return err
	}
	for i := range lines {
		if lines[i].ColorID != nil || plain[i].ColorID == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoice_lines SET color_id = $2 WHERE id = $1`, lines[i].ID, plain[i].ColorID); err != nil {
			return wrap("set invoice line color", err)
		}
		lines[i].ColorID = plain[i].ColorID
	}
	return nil
}

// identityOrigin maps stored line i to field "lines.i".
func identityOrigin(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// guard spends the owner's confirmations an invoice needs to be confirmed as it stands: one when
// undelivered more would take the payer over the credit limit, one when any line sells under its
// minimum price. Without a matching confirmation the posting fails with 409 exception_required.
func (s *Service) guard(ctx context.Context, tx posting.Tx, inv Invoice, lines []Line, includeTax bool, undelivered money.Money, c confirmations) error {
	credit := s.debt.CheckCredit(ctx, tx, CreditQuery{PartyID: inv.PayerID, OrderID: &inv.ID, Undelivered: undelivered, AsOf: tx.BusinessDate()})
	if credit != nil {
		if apperr.KindOf(credit) != apperr.ExceptionRequired {
			return credit
		}
		if err := s.spend(ctx, tx, tx.TenantID(), c, exception.CreditLimitExceeded, entityInvoice, inv.ID); err != nil {
			if apperr.KindOf(err) == apperr.ExceptionRequired {
				// The ledger's error says by how much the limit is exceeded.
				return credit
			}
			return err
		}
	}
	if slices.ContainsFunc(lines, func(l Line) bool { return l.belowMin(includeTax) }) {
		return s.spend(ctx, tx, tx.TenantID(), c, exception.BelowMinPrice, entityInvoice, inv.ID)
	}
	return nil
}

// holdsStock reports an invoice line that reserves and delivers stock.
func holdsStock(l InvoiceLine) bool { return stocked(l.Kind) && l.SkuID != nil }

// lineSkus are the SKUs of the stocked lines.
func lineSkus(lines []InvoiceLine) []uuid.UUID {
	var out []uuid.UUID
	for _, l := range lines {
		if holdsStock(l) {
			out = append(out, *l.SkuID)
		}
	}
	return sortedUnique(out)
}

// planHolds is how much more each stocked line can reserve from the free stock avail, served in
// line order: a line wants what it still has to deliver beyond what it holds.
func planHolds(lines []InvoiceLine, avail map[uuid.UUID]money.Qty) []money.Qty {
	free := make(map[uuid.UUID]money.Qty, len(avail))
	for k, v := range avail {
		free[k] = v
	}
	out := make([]money.Qty, len(lines))
	for i, l := range lines {
		out[i] = money.ZeroQty
		if !holdsStock(l) {
			continue
		}
		want := l.remaining().Sub(l.Reserved)
		f, ok := free[*l.SkuID]
		if !ok || !want.IsPositive() || !f.IsPositive() {
			continue
		}
		take := minQty(want, f)
		out[i] = take
		free[*l.SkuID] = f.Sub(take)
	}
	return out
}

// rebalance brings the reservation of every stocked line to what it still has to deliver: what a
// line holds beyond that is released, then the free stock is reserved in line order and the rest
// is backordered. It updates lines in place and returns the quantity newly reserved. Every SKU it
// reserves must be among the locked ones.
func (s *Service) rebalance(ctx context.Context, tx posting.Tx, lines []InvoiceLine, docNo string, locked skuSet) (money.Qty, error) {
	orig := slices.Clone(lines)
	for i := range lines {
		l := &lines[i]
		if !holdsStock(*l) {
			continue
		}
		if need := l.remaining(); l.Reserved.Cmp(need) > 0 {
			excess := l.Reserved.Sub(need)
			if err := s.releaseHeld(ctx, tx, l.ID, &excess); err != nil {
				return money.ZeroQty, err
			}
			l.Reserved = need
		}
	}
	skus := lineSkus(lines)
	if len(skus) == 0 {
		return money.ZeroQty, nil
	}
	if err := locked.covers(skus); err != nil {
		return money.ZeroQty, err
	}
	avail, err := s.stock.Available(ctx, tx, skus)
	if err != nil {
		return money.ZeroQty, err
	}
	holds := planHolds(lines, avail)
	held := money.ZeroQty
	for i := range lines {
		l := &lines[i]
		if !holdsStock(*l) {
			continue
		}
		if holds[i].IsPositive() {
			if err := s.holdMore(ctx, tx, *l, holds[i], docNo); err != nil {
				return money.ZeroQty, err
			}
			l.Reserved = l.Reserved.Add(holds[i])
			held = held.Add(holds[i])
		}
		l.Backorder = l.remaining().Sub(l.Reserved)
		if l.Reserved.Cmp(orig[i].Reserved) != 0 || l.Backorder.Cmp(orig[i].Backorder) != 0 {
			if err := updateFulfilment(ctx, tx, *l); err != nil {
				return money.ZeroQty, err
			}
		}
	}
	return held, nil
}

// holdMore reserves qty more of a line's SKU: a new reservation for a line that never held stock,
// else its reservation grows (reopening it when it was settled).
func (s *Service) holdMore(ctx context.Context, tx posting.Tx, l InvoiceLine, qty money.Qty, docNo string) error {
	r, err := s.stock.ReservationOf(ctx, tx, l.ID)
	if err != nil {
		return err
	}
	if r == nil {
		_, err = s.stock.Reserve(ctx, tx, *l.SkuID, qty, l.ID, docNo)
		return err
	}
	return s.stock.Adjust(ctx, tx, r.ID, r.Qty.Add(qty))
}

// releaseHeld frees qty of what a line's reservation still holds (nil: all of it).
func (s *Service) releaseHeld(ctx context.Context, tx posting.Tx, lineID uuid.UUID, qty *money.Qty) error {
	r, err := s.stock.ReservationOf(ctx, tx, lineID)
	if err != nil {
		return err
	}
	if r == nil || !r.Open().IsPositive() {
		if qty != nil && qty.IsPositive() {
			return fmt.Errorf("sales: invoice line %s holds no reservation to release %s from", lineID, qty.String())
		}
		return nil
	}
	return s.stock.Release(ctx, tx, r.ID, qty)
}

// updateFulfilment stores the fulfilment quantities of an invoice line.
func updateFulfilment(ctx context.Context, q db.Querier, l InvoiceLine) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE invoice_lines SET reserved_base_qty = $2, backorder_base_qty = $3, delivered_base_qty = $4, cancelled_base_qty = $5
		WHERE id = $1`, l.ID, l.Reserved, l.Backorder, l.Delivered, l.Cancelled); err != nil {
		return wrap("update invoice line quantities", err)
	}
	return nil
}

// fulfilmentOf is the audit view of the fulfilment quantities of the stocked lines.
func fulfilmentOf(lines []InvoiceLine) []map[string]any {
	out := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		if !holdsStock(l) {
			continue
		}
		out = append(out, map[string]any{
			"line_id": l.ID, "reserved_base_qty": l.Reserved, "backorder_base_qty": l.Backorder,
			"delivered_base_qty": l.Delivered, "cancelled_base_qty": l.Cancelled,
		})
	}
	return out
}

// dueDateOf is the due date of an invoice confirmed without one: the invoice date plus the payer's
// payment terms (the tenant default when the payer has none).
func dueDateOf(ctx context.Context, q db.Querier, inv Invoice, st settings) (Date, error) {
	days, err := paymentTerms(ctx, q, inv.PayerID, st.PaymentTermsDays)
	if err != nil {
		return Date{}, err
	}
	return DateOf(inv.InvoiceDate.AddDate(0, 0, days)), nil
}

// saveVersion records the invoice as it now stands as confirmed version no and returns it.
func (s *Service) saveVersion(ctx context.Context, tx posting.Tx, id uuid.UUID, no int, summary *string, material bool) (Invoice, error) {
	out, err := s.invoiceDetail(ctx, tx, id)
	if err != nil {
		return out, err
	}
	snap, err := snapshotJSON(out)
	if err != nil {
		return out, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO invoice_versions (id, tenant_id, invoice_id, version_no, snapshot, change_summary, material_change, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ids.New(), tx.TenantID(), id, no, string(snap), summary, material, tx.Actor().ID, tx.Now()); err != nil {
		return out, wrap("insert invoice version", err)
	}
	return out, nil
}

// sameLineSet reports whether lines are the stored lines unchanged.
func sameLineSet(lines []Line, old map[uuid.UUID]InvoiceLine) bool {
	for _, l := range lines {
		o, ok := old[l.ID]
		if !ok || !sameLine(l, o.Line) {
			return false
		}
	}
	return true
}

// sameLine compares what a line sells and charges.
func sameLine(a, b Line) bool {
	return a.ID == b.ID && a.LineNo == b.LineNo && a.Kind == b.Kind && sameUUID(a.SkuID, b.SkuID) &&
		sameUUID(a.ColorID, b.ColorID) && sameUUID(a.ParentLineID, b.ParentLineID) && a.UnitID == b.UnitID &&
		a.Qty.Cmp(b.Qty) == 0 && a.UnitPrice.Cmp(b.UnitPrice) == 0 && a.Discount.Cmp(b.Discount) == 0 &&
		a.OrderAlloc.Cmp(b.OrderAlloc) == 0 && a.Tax.Cmp(b.Tax) == 0 && a.LineAmount.Cmp(b.LineAmount) == 0 &&
		sameText(a.SpecText, b.SpecText)
}

func sameTotals(a, b Totals) bool {
	return a.Goods.Cmp(b.Goods) == 0 && a.Discount.Cmp(b.Discount) == 0 && a.Tax.Cmp(b.Tax) == 0 && a.Total.Cmp(b.Total) == 0
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameDate(a, b *Date) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(b.Time)
}

// lineColumnNames are the shared line columns one by one.
var lineColumnNames = func() []string {
	parts := strings.Split(lineColumns, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}()

// update rewrites a stored line in place (every shared column but its id); extra holds the values
// of the table's own columns, nil to leave them.
func (t lineTable) update(ctx context.Context, q db.Querier, l Line, extra []any) error {
	if extra != nil && len(extra) != len(t.extra) {
		return fmt.Errorf("sales: %s update: want %d extra values", t.name, len(t.extra))
	}
	vals := l.values()
	args := []any{l.ID}
	var b strings.Builder
	set := func(col string, v any) {
		if len(args) > 1 {
			b.WriteString(", ")
		}
		args = append(args, v)
		fmt.Fprintf(&b, "%s = $%d", col, len(args))
	}
	for i, col := range lineColumnNames {
		if col == "id" {
			continue
		}
		set(col, vals[i])
	}
	for i, v := range extra {
		set(t.extra[i], v)
	}
	if _, err := q.ExecContext(ctx, "UPDATE "+t.name+" SET "+b.String()+" WHERE id = $1", args...); err != nil {
		return fmt.Errorf("sales: update %s: %w", t.name, err)
	}
	return nil
}
