package sales

import (
	"context"
	"fmt"
	"net/http"

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

// codeObligationHasCredits is the debt ledger's refusal to reverse a receivable that credit notes
// already settled.
const codeObligationHasCredits = "obligation_has_credits"

// StockOutInput is what a stock out (PX) delivers of a confirmed invoice. Without lines it delivers
// everything the invoice holds in stock: each line its reservation, and each mix pack or combo whose
// parts all hold what they still have to deliver.
type StockOutInput struct {
	Lines []StockOutLineInput `json:"lines"`
	Note  *string             `json:"note,omitempty"`
}

// StockOutLineInput delivers BaseQty of an invoice line; zero delivers what the line holds. A mix
// pack or combo is delivered whole, through its header line.
type StockOutLineInput struct {
	InvoiceLineID uuid.UUID `json:"invoice_line_id"`
	BaseQty       money.Qty `json:"base_qty"`
}

// StockOutDetail is a stock out with its lines and the sales document (BH) it posted.
type StockOutDetail struct {
	StockOut
	Document *DocumentDetail `json:"sales_document"`
}

// ReverseInput says why a stock out is reversed.
type ReverseInput struct {
	Reason *string `json:"reason,omitempty"`
}

// StockOutReversal is a reversed stock out, the stock out that reversed it, and what had been paid
// on its sales document: that money is now the payer's advance.
type StockOutReversal struct {
	StockOut      StockOutDetail `json:"stock_out"`
	ReversalID    uuid.UUID      `json:"reversal_id"`
	ReversalDocNo string         `json:"reversal_doc_no"`
	Advance       money.Money    `json:"advance_amount"`
}

// validateStockOut checks the shape of a stock out request.
func validateStockOut(in StockOutInput) error {
	fields := map[string]string{}
	if len(in.Lines) > maxLines {
		fields["lines"] = "too_many"
	}
	seen := make(map[uuid.UUID]bool, len(in.Lines))
	for i, l := range in.Lines {
		switch {
		case l.InvoiceLineID == uuid.Nil:
			fields[lineField(i, "invoice_line_id")] = "required"
		case seen[l.InvoiceLineID]:
			fields[lineField(i, "invoice_line_id")] = "duplicate"
		}
		seen[l.InvoiceLineID] = true
		if l.BaseQty.IsNegative() {
			fields[lineField(i, "base_qty")] = "negative"
		}
	}
	checkText(fields, "note", in.Note, maxNoteLen)
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// PostStockOut delivers goods of a confirmed invoice: it writes the PX with its lines and the BH
// sales document, consumes the reservations, issues the stock, and makes the payer owe the BH,
// settled first by the deposits paid toward the invoice.
func (s *Service) PostStockOut(ctx context.Context, actor audit.Actor, key idempotency.Key, invoiceID uuid.UUID, in StockOutInput) (posting.Response, error) {
	if err := validateStockOut(in); err != nil {
		return posting.Response{}, err
	}
	date, skus, err := s.invoiceSkus(ctx, actor.TenantID, invoiceID, nil)
	if err != nil {
		return posting.Response{}, err
	}
	cmd := command{scope: ScopeStockOutPost, date: date, prefix: "PX", extra: []string{"BH"}, locks: stockLocks(skus)}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		inv, err := getInvoice(ctx, tx, invoiceID, true)
		if err != nil {
			return posting.Result{}, err
		}
		if !invoiceOpen(inv.Status) {
			return posting.Result{}, errTransition(inv.Status, "delivered")
		}
		lines, err := lockInvoiceLines(ctx, tx, invoiceID)
		if err != nil {
			return posting.Result{}, err
		}
		plan, err := planDelivery(lines, in.Lines)
		if err != nil {
			return posting.Result{}, err
		}
		done, err := deliveredMoneyOf(ctx, tx, invoiceID)
		if err != nil {
			return posting.Result{}, err
		}
		docLines, err := deliveryLines(ctx, tx, lines, plan, done, inv.CampaignID)
		if err != nil {
			return posting.Result{}, err
		}
		warehouse, err := warehouseID(ctx, tx)
		if err != nil {
			return posting.Result{}, err
		}
		bhNo, err := posting.TakeDocNo(tx, "BH")
		if err != nil {
			return posting.Result{}, err
		}
		now := tx.Now()
		soID, docID := ids.New(), ids.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_outs (id, tenant_id, warehouse_id, doc_no, invoice_id, business_date, note, status, idempotency_key,
			                        posted_at, posted_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'posted', $8, $9, $10, $9)`,
			soID, tx.TenantID(), warehouse, tx.DocNo(), invoiceID, tx.BusinessDate(), in.Note, key.Value, now, tx.Actor().ID); err != nil {
			return posting.Result{}, wrap("insert stock out", err)
		}
		byInvoiceLine := make(map[uuid.UUID]InvoiceLine, len(lines))
		for _, l := range lines {
			byInvoiceLine[l.ID] = l
		}
		for i := range docLines {
			dl := &docLines[i]
			il := byInvoiceLine[*dl.InvoiceLineID]
			if !holdsStock(il) {
				continue
			}
			lineID := ids.New()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stock_out_lines (id, tenant_id, stock_out_id, invoice_line_id, sku_id, color_id, base_qty)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				lineID, tx.TenantID(), soID, il.ID, il.SkuID, il.ColorID, dl.BaseQty); err != nil {
				return posting.Result{}, wrap("insert stock out line", err)
			}
			dl.StockOutLineID = &lineID
		}

		includeTax := inv.Goods.Equal(totalsOf(plainLines(lines), true).Goods)
		totals := totalsOf(plainLines(docLines), includeTax)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sales_documents (id, tenant_id, doc_no, source, stock_out_id, invoice_id, customer_party_id, payer_party_id,
			                             business_date, goods_amount, discount_amount, tax_amount, total_amount, cogs_amount,
			                             campaign_id, status, idempotency_key, posted_at, posted_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 0, $14, 'posted', $15, $16, $17, $16)`,
			docID, tx.TenantID(), bhNo, SourceStockOut, soID, invoiceID, inv.CustomerID, inv.PayerID, tx.BusinessDate(),
			totals.Goods, totals.Discount, totals.Tax, totals.Total, inv.CampaignID, key.Value, now, tx.Actor().ID); err != nil {
			return posting.Result{}, wrap("insert sales document", err)
		}
		lineCampaigns, err := s.attributeLines(ctx, tx, docID, inv.CampaignID, docLines)
		if err != nil {
			return posting.Result{}, err
		}
		if err := documentLines.insert(ctx, tx, tx.TenantID(), docID, plainLines(docLines), documentExtras(docLines)); err != nil {
			return posting.Result{}, err
		}

		var issues []StockIssue
		for _, dl := range docLines {
			il := byInvoiceLine[*dl.InvoiceLineID]
			if !holdsStock(il) {
				continue
			}
			if err := s.consumeHeld(ctx, tx, il, dl.BaseQty); err != nil {
				return posting.Result{}, err
			}
			issues = append(issues, StockIssue{DocLineID: dl.ID, SkuID: *il.SkuID, BaseQty: dl.BaseQty, ColorID: il.ColorID})
		}
		moves, err := s.stock.Issue(ctx, tx, StockDoc{Type: StockDocSalesDocument, ID: docID}, issues)
		if err != nil {
			return posting.Result{}, err
		}
		cogs, err := storeCogs(ctx, tx, docID, moves)
		if err != nil {
			return posting.Result{}, err
		}

		before := fulfilmentOf(lines)
		for i := range lines {
			l := &lines[i]
			d, ok := plan[l.ID]
			if !ok {
				continue
			}
			l.Delivered = l.Delivered.Add(d)
			if holdsStock(*l) {
				l.Reserved = l.Reserved.Sub(d)
			}
			if err := updateFulfilment(ctx, tx, *l); err != nil {
				return posting.Result{}, err
			}
		}
		status := deliveryStatus(inv.Status, lines)
		if _, err := db.UpdateWithVersion(ctx, tx, "invoices", invoiceID, inv.Version, now,
			db.Set{Column: "delivered_amount", Value: inv.Delivered.Add(totals.Total)},
			db.Set{Column: "status", Value: status}); err != nil {
			return posting.Result{}, err
		}

		if totals.Total.IsPositive() {
			if _, err := s.debt.RecordReceivable(ctx, tx, ReceivableInput{
				PartyID: inv.PayerID, SourceID: docID, SourceDocNo: bhNo, Amount: totals.Total, DueDate: datePtr(inv.DueDate),
				CampaignID: inv.CampaignID, EarmarkedInvoiceID: &inv.ID,
			}); err != nil {
				return posting.Result{}, err
			}
		}

		out, err := s.stockOutDetail(ctx, tx, soID)
		if err != nil {
			return posting.Result{}, err
		}
		tx.Audit(audit.Event{Action: ScopeStockOutPost, EntityType: entityStockOut, EntityID: soID,
			Before: map[string]any{"invoice_status": inv.Status, "lines": before},
			After:  out})
		tx.Audit(audit.Event{Action: ActionDocumentPost, EntityType: entityDocument, EntityID: docID, DocNo: bhNo,
			After: map[string]any{"source": SourceStockOut, "stock_out_id": soID, "invoice_id": invoiceID,
				"total_amount": totals.Total, "cogs_amount": cogs, "invoice_status": status}})
		tx.Publish(outbox.Event{Topic: TopicSalePosted, Payload: map[string]any{
			"sales_document_id": docID, "doc_no": bhNo, "source": SourceStockOut, "stock_out_id": soID,
			"invoice_id": invoiceID, "customer_party_id": inv.CustomerID, "payer_party_id": inv.PayerID,
			"business_date": DateOf(tx.BusinessDate()), "total_amount": totals.Total, "cogs_amount": cogs,
			"campaign_id": inv.CampaignID, "line_campaigns": lineCampaigns,
		}})
		return posting.Result{Status: http.StatusCreated, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// planDelivery decides the base quantity each invoice line delivers. A stocked line delivers from
// what it holds, never more. A mix pack or combo is delivered whole: its header and every part
// deliver all they still have to, which every part must hold. Parts are never named on their own.
func planDelivery(lines []InvoiceLine, req []StockOutLineInput) (map[uuid.UUID]money.Qty, error) {
	byID := make(map[uuid.UUID]InvoiceLine, len(lines))
	children := map[uuid.UUID][]InvoiceLine{}
	for _, l := range lines {
		byID[l.ID] = l
		if l.ParentLineID != nil {
			children[*l.ParentLineID] = append(children[*l.ParentLineID], l)
		}
	}
	// ready reports a header whose parts hold everything they still have to deliver.
	ready := func(h InvoiceLine) bool {
		if !h.remaining().IsPositive() {
			return false
		}
		found := false
		for _, c := range children[h.ID] {
			rem := c.remaining()
			if !holdsStock(c) || !rem.IsPositive() {
				continue
			}
			if c.Reserved.Cmp(rem) != 0 {
				return false
			}
			found = true
		}
		return found
	}
	out := map[uuid.UUID]money.Qty{}
	addWhole := func(h InvoiceLine) {
		out[h.ID] = h.remaining()
		for _, c := range children[h.ID] {
			if rem := c.remaining(); holdsStock(c) && rem.IsPositive() {
				out[c.ID] = rem
			}
		}
	}
	header := func(l InvoiceLine) bool { return l.Kind == KindMixPack || l.Kind == KindCombo }

	if len(req) == 0 {
		for _, l := range lines {
			switch {
			case header(l):
				if ready(l) {
					addWhole(l)
				}
			case l.ParentLineID != nil:
			case holdsStock(l) && l.Reserved.IsPositive():
				out[l.ID] = l.Reserved
			}
		}
		if len(out) == 0 {
			return nil, errConflict(CodeNothingToDeliver, "Nothing on this invoice is reserved and ready to deliver")
		}
		return out, nil
	}

	for i, r := range req {
		l, ok := byID[r.InvoiceLineID]
		field := lineField(i, "invoice_line_id")
		switch {
		case !ok:
			return nil, errInvalid(map[string]string{field: "not_found"})
		case l.ParentLineID != nil:
			return nil, errInvalid(map[string]string{field: "deliver_through_parent"})
		case header(l):
			rem := l.remaining()
			if !rem.IsPositive() {
				return nil, errConflict(CodeNothingToDeliver, fmt.Sprintf("Line %d is already delivered", l.LineNo))
			}
			if !r.BaseQty.IsZero() && r.BaseQty.Cmp(rem) != 0 {
				return nil, apperr.NewValidation(CodeMixPackPartial, "A mix pack or combo is delivered whole",
					map[string]string{lineField(i, "base_qty"): CodeMixPackPartial})
			}
			if !ready(l) {
				return nil, errConflict(CodeDeliveryExceedsReserve,
					fmt.Sprintf("Line %d cannot be delivered whole: not all of its parts are reserved", l.LineNo))
			}
			addWhole(l)
		case !holdsStock(l):
			return nil, errInvalid(map[string]string{field: "not_deliverable"})
		default:
			qty := r.BaseQty
			if qty.IsZero() {
				qty = l.Reserved
			}
			if !qty.IsPositive() || qty.Cmp(l.Reserved) > 0 {
				return nil, errConflict(CodeDeliveryExceedsReserve,
					fmt.Sprintf("Line %d holds %s in stock; deliver at most that, or reserve more first", l.LineNo, l.Reserved.String()))
			}
			out[l.ID] = qty
		}
	}
	return out, nil
}

// deliveredMoney is what the earlier sales documents of an invoice that still stand carried of
// one invoice line.
type deliveredMoney struct {
	LineID     uuid.UUID   `db:"invoice_line_id"`
	Amount     money.Money `db:"line_amount"`
	Tax        money.Money `db:"tax_amount"`
	Discount   money.Money `db:"discount_amount"`
	OrderAlloc money.Money `db:"order_discount_alloc"`
}

func deliveredMoneyOf(ctx context.Context, q db.Querier, invoiceID uuid.UUID) (map[uuid.UUID]deliveredMoney, error) {
	var rows []deliveredMoney
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT l.invoice_line_id, SUM(l.line_amount) AS line_amount, SUM(l.tax_amount) AS tax_amount,
		       SUM(l.discount_amount) AS discount_amount, SUM(l.order_discount_alloc) AS order_discount_alloc
		FROM sales_document_lines l JOIN sales_documents d ON d.id = l.sales_document_id
		WHERE d.invoice_id = $1 AND d.status <> 'reversed' AND l.invoice_line_id IS NOT NULL
		GROUP BY l.invoice_line_id`, invoiceID); err != nil {
		return nil, wrap("read delivered amounts", err)
	}
	out := make(map[uuid.UUID]deliveredMoney, len(rows))
	for _, r := range rows {
		out[r.LineID] = r
	}
	return out, nil
}

// shareOf is the part of amount, a line's whole amount over base units, that delivering d more
// carries when delivered was delivered before and done was carried by it: everything left once
// the line is delivered in full, else the proportional share rounded to the đồng and never more
// than is left. The parts of a line therefore always add up to the line.
func shareOf(amount, done money.Money, d, delivered, base money.Qty) (money.Money, error) {
	left := amount.Sub(done)
	if delivered.Add(d).Cmp(base) >= 0 || !base.IsPositive() {
		return left, nil
	}
	share, err := money.RoundMoney(amount.Decimal().Mul(d.Decimal()).Div(base.Decimal()))
	if err != nil {
		return money.Money{}, err
	}
	share = share.RoundVND()
	if share.Cmp(left) > 0 {
		return left, nil
	}
	return share, nil
}

// deliveryLines builds the sales document lines of a delivery, in invoice line order: each line
// carries its prorated money and the quantity delivered in its unit (in the base unit when the
// delivery is not a whole number of units). A mix pack header carries the sum of its flavors.
func deliveryLines(ctx context.Context, q db.Querier, lines []InvoiceLine, plan map[uuid.UUID]money.Qty,
	done map[uuid.UUID]deliveredMoney, campaignID *uuid.UUID,
) ([]DocumentLine, error) {
	newIDs := map[uuid.UUID]uuid.UUID{}
	for _, l := range lines {
		if _, ok := plan[l.ID]; ok {
			newIDs[l.ID] = ids.New()
		}
	}
	var out []DocumentLine
	var toBase []int
	for _, l := range lines {
		d, ok := plan[l.ID]
		if !ok {
			continue
		}
		dl := DocumentLine{Line: l.Line, Returned: money.ZeroQty, CampaignID: campaignID}
		invoiceLineID := l.ID
		dl.ID, dl.InvoiceLineID, dl.LineNo = newIDs[l.ID], &invoiceLineID, len(out)+1
		if l.ParentLineID != nil {
			p, ok := newIDs[*l.ParentLineID]
			if !ok {
				return nil, fmt.Errorf("sales: invoice line %s is delivered without its header", l.ID)
			}
			dl.ParentLineID = &p
		}
		if l.MixKey != nil && l.MixGroupID != nil {
			head := l.ID
			if l.ParentLineID != nil {
				head = *l.ParentLineID
			}
			k := newIDs[head].String()
			dl.MixKey = &k
		}
		dm := done[l.ID]
		var err error
		share := func(amount, prior money.Money) money.Money {
			if err != nil {
				return money.Money{}
			}
			var v money.Money
			v, err = shareOf(amount, prior, d, l.Delivered, l.BaseQty)
			return v
		}
		dl.LineAmount = share(l.LineAmount, dm.Amount)
		dl.Tax = share(l.Tax, dm.Tax)
		dl.Discount = share(l.Discount, dm.Discount)
		dl.OrderAlloc = share(l.OrderAlloc, dm.OrderAlloc)
		if err != nil {
			return nil, wrap("prorate delivered amounts", err)
		}
		dl.BaseQty = d
		switch qty, exact := unitQty(d, l.UnitFactor); {
		case l.Delivered.IsZero() && d.Cmp(l.BaseQty) == 0:
			dl.Qty = l.Qty
		case exact:
			dl.Qty = qty
		default:
			toBase = append(toBase, len(out))
		}
		out = append(out, dl)
	}
	if len(out) == 0 {
		return nil, errConflict(CodeNothingToDeliver, "Nothing on this invoice is ready to deliver")
	}
	if err := toBaseUnit(ctx, q, out, toBase); err != nil {
		return nil, err
	}
	sumMixHeaders(out)
	return out, nil
}

// unitQty is base expressed in units of factor, false when it is not a number of units to four
// decimals (the precision of a quantity).
func unitQty(base money.Qty, factor money.Factor) (money.Qty, bool) {
	if factor.Decimal().IsZero() {
		return money.Qty{}, false
	}
	v := base.Decimal().Div(factor.Decimal()).Round(4)
	if !v.Mul(factor.Decimal()).Equal(base.Decimal()) {
		return money.Qty{}, false
	}
	q, err := money.NewQty(v.String())
	if err != nil {
		return money.Qty{}, false
	}
	return q, true
}

// toBaseUnit restates the lines at positions in the base unit of their SKU, at the unit price per
// base unit: a delivery that is not a whole number of the sold unit is recorded as base units.
func toBaseUnit(ctx context.Context, q db.Querier, lines []DocumentLine, positions []int) error {
	if len(positions) == 0 {
		return nil
	}
	var skus []uuid.UUID
	for _, i := range positions {
		if lines[i].SkuID == nil {
			return fmt.Errorf("sales: line %s without a SKU is not in whole units", lines[i].ID)
		}
		skus = append(skus, *lines[i].SkuID)
	}
	cat, err := loadCatalog(ctx, q, sortedUnique(skus), nil)
	if err != nil {
		return err
	}
	for _, i := range positions {
		l := &lines[i]
		sku, ok := cat.skus[*l.SkuID]
		if !ok {
			return fmt.Errorf("sales: sku %s not found", *l.SkuID)
		}
		u, ok := cat.unit(sku.ID, &sku.BaseUnitID)
		if !ok {
			return fmt.Errorf("sales: sku %s has no active base unit", sku.ID)
		}
		price, err := money.RoundMoney(l.UnitPrice.Decimal().Div(l.UnitFactor.Decimal()))
		if err != nil {
			return wrap("restate unit price", err)
		}
		l.UnitID, l.UnitName, l.UnitFactor, l.Qty, l.UnitPrice = u.UnitID, u.Name, money.FactorOne, l.BaseQty, price
	}
	return nil
}

// sumMixHeaders gives each mix pack header the sums of its delivered flavors, as on the invoice.
func sumMixHeaders(lines []DocumentLine) {
	for h := range lines {
		hl := &lines[h]
		if hl.Kind != KindMixPack {
			continue
		}
		var disc, alloc, tax, amount money.Money
		for _, c := range lines {
			if c.ParentLineID == nil || *c.ParentLineID != hl.ID {
				continue
			}
			disc, alloc = disc.Add(c.Discount), alloc.Add(c.OrderAlloc)
			tax, amount = tax.Add(c.Tax), amount.Add(c.LineAmount)
		}
		hl.Discount, hl.OrderAlloc, hl.Tax, hl.LineAmount = disc, alloc, tax, amount
	}
}

// documentExtras are the sales document line columns of lines (cost is set once the stock moved).
func documentExtras(lines []DocumentLine) [][]any {
	out := make([][]any, len(lines))
	for i, l := range lines {
		out[i] = []any{l.InvoiceLineID, l.StockOutLineID, l.Cogs, l.Returned, l.CampaignID}
	}
	return out
}

// consumeHeld takes qty out of what an invoice line's reservation holds, as it is delivered.
func (s *Service) consumeHeld(ctx context.Context, tx posting.Tx, l InvoiceLine, qty money.Qty) error {
	r, err := s.stock.ReservationOf(ctx, tx, l.ID)
	if err != nil {
		return err
	}
	if r == nil || r.Open().Cmp(qty) < 0 {
		return errConflict(CodeDeliveryExceedsReserve, fmt.Sprintf("Line %d does not hold %s in stock", l.LineNo, qty.String()))
	}
	return s.stock.Consume(ctx, tx, r.ID, qty)
}

// storeCogs sets the cost of goods of each line of a sales document from the stock moves its lines
// issued, and of the document as their sum, which it returns.
func storeCogs(ctx context.Context, q db.Querier, docID uuid.UUID, moves []StockMove) (money.Money, error) {
	perLine := map[uuid.UUID]money.Money{}
	var order []uuid.UUID
	total := money.Money{}
	for _, m := range moves {
		if m.DocLineID == nil {
			return money.Money{}, fmt.Errorf("sales: stock move %s has no document line", m.ID)
		}
		if _, ok := perLine[*m.DocLineID]; !ok {
			order = append(order, *m.DocLineID)
		}
		cost := m.Value.Neg()
		perLine[*m.DocLineID] = perLine[*m.DocLineID].Add(cost)
		total = total.Add(cost)
	}
	for _, id := range sortedUnique(order) {
		if _, err := q.ExecContext(ctx, `UPDATE sales_document_lines SET cogs_amount = $2 WHERE id = $1`, id, perLine[id]); err != nil {
			return money.Money{}, wrap("set sales document line cost", err)
		}
	}
	if _, err := q.ExecContext(ctx, `UPDATE sales_documents SET cogs_amount = $2 WHERE id = $1`, docID, total); err != nil {
		return money.Money{}, wrap("set sales document cost", err)
	}
	return total, nil
}

// StockOut returns a stock out with its lines and its sales document.
func (s *Service) StockOut(ctx context.Context, scope Scope, id uuid.UUID) (StockOutDetail, error) {
	var out StockOutDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = s.stockOutDetail(ctx, q, id)
		return err
	})
	return out, err
}

func (s *Service) stockOutDetail(ctx context.Context, q db.Querier, id uuid.UUID) (StockOutDetail, error) {
	so, err := getStockOut(ctx, q, id, false)
	if err != nil {
		return StockOutDetail{}, err
	}
	if so.Lines, err = stockOutLinesOf(ctx, q, id); err != nil {
		return StockOutDetail{}, err
	}
	out := StockOutDetail{StockOut: so}
	if so.SalesDocumentID != nil {
		d, err := s.documentDetail(ctx, q, *so.SalesDocumentID)
		if err != nil {
			return StockOutDetail{}, err
		}
		out.Document = &d
	}
	return out, nil
}

// ReverseStockOut undoes a stock out that nothing was returned from: a reversing PX books the stock
// back, the BH and its receivable are reversed (what had been paid on it becomes the payer's
// advance), and the invoice lines hold the goods again, or count them cancelled when the invoice
// stopped delivering. A receivable that credit notes settled cannot be reversed; the goods come
// back through a sales return instead.
func (s *Service) ReverseStockOut(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in ReverseInput) (posting.Response, error) {
	fields := map[string]string{}
	checkText(fields, "reason", in.Reason, maxNoteLen)
	if len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	var invoiceID uuid.UUID
	var receivable *Obligation
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		so, err := getStockOut(ctx, q, id, false)
		if err != nil {
			return err
		}
		invoiceID = so.InvoiceID
		if so.SalesDocumentID != nil {
			receivable, err = s.debt.ReceivableOf(ctx, q, *so.SalesDocumentID)
		}
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	date, skus, err := s.invoiceSkus(ctx, actor.TenantID, invoiceID, nil)
	if err != nil {
		return posting.Response{}, err
	}
	locks := stockLocks(skus)
	if receivable != nil {
		locks.Add(posting.LockObligations, receivable.ID)
	}
	cmd := command{scope: ScopeStockOutReverse, date: date, prefix: "PX", locks: locks}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		so, err := getStockOut(ctx, tx, id, true)
		if err != nil {
			return posting.Result{}, err
		}
		switch {
		case so.ReversalOf != nil:
			return posting.Result{}, errTransition("reversing", "reversed")
		case so.Status == DocReversed:
			return posting.Result{}, errConflict(CodeStockOutReversed, "The stock out was already reversed")
		case so.SalesDocumentID == nil:
			return posting.Result{}, fmt.Errorf("sales: stock out %s has no sales document", id)
		}
		doc, err := getDocument(ctx, tx, *so.SalesDocumentID, true)
		if err != nil {
			return posting.Result{}, err
		}
		docLines, err := lockDocumentLines(ctx, tx, doc.ID)
		if err != nil {
			return posting.Result{}, err
		}
		for _, l := range docLines {
			if l.Returned.IsPositive() {
				return posting.Result{}, errConflict(CodeDocumentHasReturns,
					"Goods of this stock out were returned; return the rest through a sales return instead of reversing it")
			}
		}
		inv, err := getInvoice(ctx, tx, so.InvoiceID, true)
		if err != nil {
			return posting.Result{}, err
		}
		lines, err := lockInvoiceLines(ctx, tx, so.InvoiceID)
		if err != nil {
			return posting.Result{}, err
		}

		reason := "Reversal of stock out " + so.DocNo
		if in.Reason != nil && *in.Reason != "" {
			reason = *in.Reason
		}
		advance := money.Money{}
		ob, err := s.debt.ReceivableOf(ctx, tx, doc.ID)
		if err != nil {
			return posting.Result{}, err
		}
		if ob != nil {
			advance, err = s.debt.ReverseReceivable(ctx, tx, doc.ID, reason)
			if e, ok := apperr.As(err); ok && e.Code == codeObligationHasCredits {
				return posting.Result{}, errConflict(codeObligationHasCredits,
					"Credit notes already settle this sale; post a sales return instead of reversing the stock out")
			}
			if err != nil {
				return posting.Result{}, err
			}
		}

		warehouse, err := warehouseID(ctx, tx)
		if err != nil {
			return posting.Result{}, err
		}
		now, revID := tx.Now(), ids.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_outs (id, tenant_id, warehouse_id, doc_no, invoice_id, business_date, note, status, idempotency_key,
			                        posted_at, posted_by, reversal_of, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'posted', $8, $9, $10, $11, $9)`,
			revID, tx.TenantID(), warehouse, tx.DocNo(), so.InvoiceID, tx.BusinessDate(), reason, key.Value, now,
			tx.Actor().ID, so.ID); err != nil {
			return posting.Result{}, wrap("insert reversing stock out", err)
		}
		if _, err := s.stock.Reverse(ctx, tx, StockDoc{Type: StockDocStockOut, ID: revID},
			StockDoc{Type: StockDocSalesDocument, ID: doc.ID}); err != nil {
			return posting.Result{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stock_outs SET status = 'reversed' WHERE id = $1`, so.ID); err != nil {
			return posting.Result{}, wrap("mark stock out reversed", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sales_documents SET status = 'reversed' WHERE id = $1`, doc.ID); err != nil {
			return posting.Result{}, wrap("mark sales document reversed", err)
		}

		before := fulfilmentOf(lines)
		if err := s.undeliver(ctx, tx, inv, lines, docLines); err != nil {
			return posting.Result{}, err
		}
		status := deliveryStatus(inv.Status, lines)
		if _, err := db.UpdateWithVersion(ctx, tx, "invoices", inv.ID, inv.Version, now,
			db.Set{Column: "delivered_amount", Value: inv.Delivered.Sub(doc.Total)},
			db.Set{Column: "status", Value: status}); err != nil {
			return posting.Result{}, err
		}

		detail, err := s.stockOutDetail(ctx, tx, so.ID)
		if err != nil {
			return posting.Result{}, err
		}
		out := StockOutReversal{StockOut: detail, ReversalID: revID, ReversalDocNo: tx.DocNo(), Advance: advance}
		tx.Audit(audit.Event{Action: ScopeStockOutReverse, EntityType: entityStockOut, EntityID: so.ID,
			Before: map[string]any{"status": so.Status, "invoice_status": inv.Status, "lines": before},
			After: map[string]any{"status": DocReversed, "reversal_id": revID, "invoice_status": status,
				"lines": fulfilmentOf(lines), "advance_amount": advance, "reason": reason}})
		tx.Audit(audit.Event{Action: ActionDocumentReverse, EntityType: entityDocument, EntityID: doc.ID, DocNo: doc.DocNo,
			Before: map[string]any{"status": doc.Status},
			After:  map[string]any{"status": DocReversed, "advance_amount": advance, "reason": reason}})
		tx.Publish(outbox.Event{Topic: TopicSaleReversed, Payload: map[string]any{
			"sales_document_id": doc.ID, "doc_no": doc.DocNo, "source": doc.Source, "stock_out_id": so.ID,
			"reversal_doc_no": tx.DocNo(), "invoice_id": inv.ID, "payer_party_id": inv.PayerID,
			"total_amount": doc.Total, "cogs_amount": doc.Cogs, "advance_amount": advance, "campaign_id": doc.CampaignID,
		}})
		return posting.Result{Status: http.StatusCreated, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// undeliver takes a reversed delivery off the invoice lines it delivered. On an invoice still
// delivering, the goods are held for it again; one that stopped delivering counts them cancelled.
func (s *Service) undeliver(ctx context.Context, tx posting.Tx, inv Invoice, lines []InvoiceLine, docLines []DocumentLine) error {
	at := make(map[uuid.UUID]int, len(lines))
	for i, l := range lines {
		at[l.ID] = i
	}
	for _, dl := range docLines {
		if dl.InvoiceLineID == nil {
			continue
		}
		i, ok := at[*dl.InvoiceLineID]
		if !ok {
			return fmt.Errorf("sales: invoice line %s of a sales document is gone", *dl.InvoiceLineID)
		}
		l := &lines[i]
		if l.Delivered.Cmp(dl.BaseQty) < 0 {
			return fmt.Errorf("sales: invoice line %s delivered %s, less than the %s reversed", l.ID, l.Delivered.String(), dl.BaseQty.String())
		}
		l.Delivered = l.Delivered.Sub(dl.BaseQty)
		switch {
		case inv.Status == InvoiceCancelledRemaining:
			l.Cancelled = l.Cancelled.Add(dl.BaseQty)
		case holdsStock(*l):
			if err := s.holdMore(ctx, tx, *l, dl.BaseQty, inv.DocNo); err != nil {
				return err
			}
			l.Reserved = l.Reserved.Add(dl.BaseQty)
		}
		if err := updateFulfilment(ctx, tx, *l); err != nil {
			return err
		}
	}
	return nil
}
