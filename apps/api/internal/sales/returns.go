package sales

import (
	"context"
	"fmt"
	"net/http"
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

// Where returned goods go back to, and where their cost comes from.
const (
	RestockSellable   = "sellable"
	RestockQuarantine = "quarantine"

	CostOriginal       = "original"
	CostCurrentAverage = "current_average"
)

// reasonSalesReturn is the reason kind of a sales return.
const reasonSalesReturn = "sales_return"

// ReturnLineInput is one returned item. A line of a sales document is returned in base units at
// what it was sold for, in its color, into the lots it came from; a return without a document
// names the SKU, unit, color and price, and the goods enter a lot at the SKU's average cost.
type ReturnLineInput struct {
	SalesDocumentLineID *uuid.UUID   `json:"sales_document_line_id,omitempty"`
	SkuID               *uuid.UUID   `json:"sku_id,omitempty"`
	UnitID              *uuid.UUID   `json:"unit_id,omitempty"`
	ColorID             *uuid.UUID   `json:"color_id,omitempty"`
	Qty                 money.Qty    `json:"qty"`
	UnitPrice           *money.Money `json:"unit_price,omitempty"`
	// RestockTo is sellable (the default) or quarantine for goods to inspect.
	RestockTo  string `json:"restock_to,omitempty"`
	LotNo      string `json:"lot_no,omitempty"`
	ExpiryDate *Date  `json:"expiry_date,omitempty"`
}

// ReturnInput is a sales return (TH): from a sales document, or without one (CustomerID nil is a
// walk-in customer). The return first reduces what the customer owes; RefundMethodID pays back
// the rest at once, and a walk-in customer, who owes nothing, is always paid back.
type ReturnInput struct {
	SalesDocumentID *uuid.UUID        `json:"sales_document_id,omitempty"`
	CustomerID      *uuid.UUID        `json:"customer_party_id,omitempty"`
	ReasonID        uuid.UUID         `json:"reason_id"`
	Lines           []ReturnLineInput `json:"lines"`
	RefundMethodID  *uuid.UUID        `json:"refund_payment_method_id,omitempty"`
}

// ReturnPosted is a posted sales return and the refund paid with it, if any.
type ReturnPosted struct {
	Return SalesReturn `json:"sales_return"`
	Refund *Payment    `json:"refund"`
}

// ReturnFilter narrows the return list.
type ReturnFilter struct {
	SalesDocumentID *uuid.UUID
	CustomerID      *uuid.UUID
	Page            ListPage
}

func validateReturn(in ReturnInput) error {
	fields := map[string]string{}
	if in.ReasonID == uuid.Nil {
		fields["reason_id"] = "required"
	}
	if in.SalesDocumentID != nil && in.CustomerID != nil {
		fields["customer_party_id"] = "not_allowed"
	}
	validateReturnLines(fields, "lines", in.SalesDocumentID != nil, in.Lines)
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// validateReturnLines checks the shape of returned lines under field prefix name.
func validateReturnLines(fields map[string]string, name string, sourced bool, lines []ReturnLineInput) {
	switch {
	case len(lines) == 0:
		fields[name] = "required"
	case len(lines) > maxLines:
		fields[name] = "too_many"
	}
	seen := map[uuid.UUID]bool{}
	for i, l := range lines {
		f := func(n string) string { return name + "." + strconv.Itoa(i) + "." + n }
		if !l.Qty.IsPositive() {
			fields[f("qty")] = "must_be_positive"
		}
		if l.RestockTo != "" && l.RestockTo != RestockSellable && l.RestockTo != RestockQuarantine {
			fields[f("restock_to")] = "unknown"
		}
		if sourced {
			switch {
			case l.SalesDocumentLineID == nil:
				fields[f("sales_document_line_id")] = "required"
			case seen[*l.SalesDocumentLineID]:
				fields[f("sales_document_line_id")] = "duplicate"
			default:
				seen[*l.SalesDocumentLineID] = true
			}
			for n, set := range map[string]bool{
				"sku_id": l.SkuID != nil, "unit_id": l.UnitID != nil, "unit_price": l.UnitPrice != nil,
				"lot_no": l.LotNo != "", "expiry_date": l.ExpiryDate != nil,
			} {
				if set {
					fields[f(n)] = "not_allowed"
				}
			}
			continue
		}
		if l.SalesDocumentLineID != nil {
			fields[f("sales_document_line_id")] = "not_allowed"
		}
		if l.SkuID == nil {
			fields[f("sku_id")] = "required"
		}
		switch {
		case l.UnitPrice == nil:
			fields[f("unit_price")] = "required"
		case l.UnitPrice.IsNegative():
			fields[f("unit_price")] = "negative"
		}
		if len(l.LotNo) > maxLabelLen {
			fields[f("lot_no")] = "too_long"
		}
	}
}

// plannedLine is a return line ready to post with the stock it puts back.
type plannedLine struct {
	Line    ReturnLine
	Receive StockReceive
}

// returnPlan is a sales return worked out from its request: the lines with their lots, costs and
// amounts, and what it takes off each sold line.
type returnPlan struct {
	Doc      *SalesDocument
	PartyID  *uuid.UUID
	ReasonID uuid.UUID
	Lines    []plannedLine
	Taken    map[uuid.UUID]money.Qty
	Total    money.Money
}

// soldLot is what one lot gave a sold line and how much of it came back already.
type soldLot struct {
	LotID    uuid.UUID
	MoveID   uuid.UUID
	ColorID  *uuid.UUID
	UnitCost money.Money
	Sold     money.Qty
	Returned money.Qty
}

// lotTake is the part of a returned quantity that goes back into one lot.
type lotTake struct {
	Lot soldLot
	Qty money.Qty
}

// spreadLots returns qty into the lots a line was sold from, in the order they were issued, each
// taking back at most what it gave less what came back already; false when the lots cannot take
// all of it.
func spreadLots(lots []soldLot, qty money.Qty) ([]lotTake, bool) {
	var out []lotTake
	left := qty
	for _, l := range lots {
		if !left.IsPositive() {
			break
		}
		room := l.Sold.Sub(l.Returned)
		if !room.IsPositive() {
			continue
		}
		take := minQty(room, left)
		out = append(out, lotTake{Lot: l, Qty: take})
		left = left.Sub(take)
	}
	return out, !left.IsPositive()
}

// returnAmount is what qty base units of a sold line are returned for: their share of the line
// amount, or all that is left of it when the return takes the rest of the line, so the returns of
// a line never add up to more or less than it was sold for.
func returnAmount(lineAmount, returnedAmount money.Money, baseQty, returned, qty money.Qty) (money.Money, error) {
	if qty.Equal(baseQty.Sub(returned)) {
		return lineAmount.Sub(returnedAmount), nil
	}
	share, err := money.RoundMoney(lineAmount.Decimal().Mul(qty.Decimal()).Div(baseQty.Decimal()))
	if err != nil {
		return money.Money{}, err
	}
	return share.RoundVND(), nil
}

// splitAmount spreads amount over parts of total in proportion, the last part taking the rest.
func splitAmount(amount money.Money, parts []money.Qty, total money.Qty) ([]money.Money, error) {
	out := make([]money.Money, len(parts))
	left := amount
	for i, p := range parts {
		if i == len(parts)-1 {
			out[i] = left
			break
		}
		share, err := money.RoundMoney(amount.Decimal().Mul(p.Decimal()).Div(total.Decimal()))
		if err != nil {
			return nil, err
		}
		out[i] = share.RoundVND()
		left = left.Sub(out[i])
	}
	return out, nil
}

// planReturn works a return out against the current data; lock reads the sold lines for update so
// concurrent returns of one document run one after the other.
func (s *Service) planReturn(ctx context.Context, q db.Querier, docID *uuid.UUID, customer *uuid.UUID, reasonID uuid.UUID,
	lines []ReturnLineInput, field string, lock bool,
) (returnPlan, error) {
	if err := checkReason(ctx, q, reasonID, reasonSalesReturn, "reason_id"); err != nil {
		return returnPlan{}, err
	}
	if docID == nil {
		return s.planSourceless(ctx, q, customer, reasonID, lines, field)
	}
	doc, err := getDocument(ctx, q, *docID, lock)
	if err != nil {
		return returnPlan{}, err
	}
	switch {
	case doc.ReversalOf != nil:
		return returnPlan{}, errTransition("reversing", "returned")
	case doc.Status == DocReversed:
		return returnPlan{}, errConflict(CodeDocumentReversed, "The sales document was reversed; nothing of it can be returned")
	}
	var sold []DocumentLine
	if lock {
		sold, err = lockDocumentLines(ctx, q, doc.ID)
	} else {
		sold, err = documentLinesOf(ctx, q, doc.ID)
	}
	if err != nil {
		return returnPlan{}, err
	}
	byID := make(map[uuid.UUID]DocumentLine, len(sold))
	var skus []uuid.UUID
	for _, l := range sold {
		byID[l.ID] = l
		if l.SkuID != nil {
			skus = append(skus, *l.SkuID)
		}
	}
	lots, err := s.soldLots(ctx, q, doc.ID)
	if err != nil {
		return returnPlan{}, err
	}
	amounts, err := returnedAmounts(ctx, q, doc.ID)
	if err != nil {
		return returnPlan{}, err
	}
	cat, err := loadCatalog(ctx, q, sortedUnique(skus), nil)
	if err != nil {
		return returnPlan{}, err
	}
	plan := returnPlan{Doc: &doc, PartyID: doc.PayerID, ReasonID: reasonID, Taken: map[uuid.UUID]money.Qty{}, Total: money.ZeroMoney}
	for i, in := range lines {
		f := func(n string) string { return field + "." + strconv.Itoa(i) + "." + n }
		l, ok := byID[*in.SalesDocumentLineID]
		if !ok {
			return returnPlan{}, errInvalid(map[string]string{f("sales_document_line_id"): "not_found"})
		}
		if !returnable(l.Kind) || l.SkuID == nil {
			return returnPlan{}, errInvalid(map[string]string{f("sales_document_line_id"): "not_returnable"})
		}
		if in.ColorID != nil && l.ColorID != nil && *in.ColorID != *l.ColorID {
			return returnPlan{}, errInvalid(map[string]string{f("color_id"): "differs_from_sale"})
		}
		left := l.BaseQty.Sub(l.Returned)
		if in.Qty.Cmp(left) > 0 {
			return returnPlan{}, errConflict(CodeReturnExceedsSold,
				fmt.Sprintf("Only %s of %s can still be returned from this sale", left.String(), l.Name))
		}
		takes, ok := spreadLots(lots[l.ID], in.Qty)
		if !ok {
			return returnPlan{}, errConflict(CodeReturnExceedsSold, "The stock issued for "+l.Name+" cannot take this return back")
		}
		amount, err := returnAmount(l.LineAmount, amounts[l.ID], l.BaseQty, l.Returned, in.Qty)
		if err != nil {
			return returnPlan{}, err
		}
		parts := make([]money.Qty, len(takes))
		for k, t := range takes {
			parts[k] = t.Qty
		}
		shares, err := splitAmount(amount, parts, in.Qty)
		if err != nil {
			return returnPlan{}, err
		}
		base, ok := baseUnit(cat, *l.SkuID)
		if !ok {
			return returnPlan{}, fmt.Errorf("sales: sku %s has no base unit", *l.SkuID)
		}
		unitPrice := money.ZeroMoney
		if r, ok := returnableOf(l); ok {
			unitPrice = r.UnitAmount
		}
		restock := in.RestockTo
		if restock == "" {
			restock = RestockSellable
		}
		code := ""
		if l.SkuCode != nil {
			code = *l.SkuCode
		}
		for k, t := range takes {
			color := l.ColorID
			if color == nil {
				color = t.Lot.ColorID
			}
			lineID, lot, move := ids.New(), t.Lot.LotID, t.Lot.MoveID
			srcLine := l.ID
			plan.Lines = append(plan.Lines, plannedLine{
				Line: ReturnLine{
					ID: lineID, SalesDocumentLineID: &srcLine, SkuID: *l.SkuID, SkuCode: code, Name: l.Name,
					VariantLabel: l.VariantLabel, UnitID: base.UnitID, UnitName: base.Name, UnitFactor: money.FactorOne,
					Qty: t.Qty, BaseQty: t.Qty, ColorID: color, SpecText: l.SpecText, UnitPrice: unitPrice,
					LineAmount: shares[k], UnitCost: t.Lot.UnitCost, CostSource: CostOriginal, RestockTo: restock, LotID: lot,
				},
				Receive: StockReceive{DocLineID: lineID, SkuID: *l.SkuID, BaseQty: t.Qty, ColorID: color, LotID: &lot, SourceMoveID: &move},
			})
		}
		plan.Taken[l.ID] = in.Qty
		plan.Total = plan.Total.Add(amount)
	}
	return plan, nil
}

// planSourceless works out a return without a sales document.
func (s *Service) planSourceless(ctx context.Context, q db.Querier, customer *uuid.UUID, reasonID uuid.UUID,
	lines []ReturnLineInput, field string,
) (returnPlan, error) {
	if customer != nil {
		if err := checkCustomer(ctx, q, *customer, "customer_party_id"); err != nil {
			return returnPlan{}, err
		}
	}
	skus := make([]uuid.UUID, len(lines))
	var colors []uuid.UUID
	for i, l := range lines {
		skus[i] = *l.SkuID
		if l.ColorID != nil {
			colors = append(colors, *l.ColorID)
		}
	}
	cat, err := loadCatalog(ctx, q, sortedUnique(skus), nil)
	if err != nil {
		return returnPlan{}, err
	}
	if err := checkColors(ctx, q, sortedUnique(colors)); err != nil {
		return returnPlan{}, err
	}
	plan := returnPlan{PartyID: customer, ReasonID: reasonID, Taken: map[uuid.UUID]money.Qty{}, Total: money.ZeroMoney}
	for i, in := range lines {
		f := func(n string) string { return field + "." + strconv.Itoa(i) + "." + n }
		sku, ok := cat.skus[*in.SkuID]
		if !ok {
			return returnPlan{}, errInvalid(map[string]string{f("sku_id"): "not_found"})
		}
		if sku.ProductType == "material" || sku.ProductType == "combo" {
			return returnPlan{}, errInvalid(map[string]string{f("sku_id"): "not_returnable"})
		}
		unit, ok := cat.unit(sku.ID, in.UnitID)
		if !ok {
			return returnPlan{}, errInvalid(map[string]string{f("unit_id"): "not_found"})
		}
		base, err := money.BaseQty(in.Qty, unit.Factor)
		if err != nil {
			return returnPlan{}, errInvalid(map[string]string{f("qty"): "invalid"})
		}
		amount := in.UnitPrice.MulQty(in.Qty).RoundVND()
		restock := in.RestockTo
		if restock == "" {
			restock = RestockSellable
		}
		lineID := ids.New()
		plan.Lines = append(plan.Lines, plannedLine{
			Line: ReturnLine{
				ID: lineID, SkuID: sku.ID, SkuCode: sku.Code, Name: sku.Name, VariantLabel: sku.Variant, UnitID: unit.UnitID,
				UnitName: unit.Name, UnitFactor: unit.Factor, Qty: in.Qty, BaseQty: base, ColorID: in.ColorID, SpecText: sku.SpecText,
				UnitPrice: *in.UnitPrice, LineAmount: amount, CostSource: CostCurrentAverage, RestockTo: restock,
			},
			Receive: StockReceive{DocLineID: lineID, SkuID: sku.ID, BaseQty: base, ColorID: in.ColorID, LotNo: in.LotNo,
				ExpiryDate: datePtr(in.ExpiryDate)},
		})
		plan.Total = plan.Total.Add(amount)
	}
	return plan, nil
}

// baseUnit is the base unit of a SKU in cat.
func baseUnit(cat *catalog, skuID uuid.UUID) (unitInfo, bool) {
	for _, u := range cat.units[skuID] {
		if u.IsBase {
			return u, true
		}
	}
	return unitInfo{}, false
}

func checkColors(ctx context.Context, q db.Querier, colors []uuid.UUID) error {
	if len(colors) == 0 {
		return nil
	}
	var found []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &found, `SELECT id FROM colors WHERE id = ANY($1)`, colors); err != nil {
		return wrap("read colors", err)
	}
	if len(found) != len(colors) {
		return errInvalid(map[string]string{"color_id": "not_found"})
	}
	return nil
}

// soldLots are the lots each line of a sales document was issued from, in issue order, with what
// posted returns took back into each.
func (s *Service) soldLots(ctx context.Context, q db.Querier, docID uuid.UUID) (map[uuid.UUID][]soldLot, error) {
	moves, err := s.stock.MovesOf(ctx, q, StockDoc{Type: StockDocSalesDocument, ID: docID})
	if err != nil {
		return nil, err
	}
	var back []struct {
		LineID uuid.UUID `db:"sales_document_line_id"`
		LotID  uuid.UUID `db:"lot_id"`
		Qty    money.Qty `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &back, `
		SELECT rl.sales_document_line_id, rl.lot_id, SUM(rl.base_qty) AS qty
		FROM sales_return_lines rl JOIN sales_returns r ON r.id = rl.sales_return_id
		WHERE r.sales_document_id = $1 AND r.status = 'posted'
		GROUP BY rl.sales_document_line_id, rl.lot_id`, docID); err != nil {
		return nil, wrap("read returned lots", err)
	}
	returned := map[[2]uuid.UUID]money.Qty{}
	for _, b := range back {
		returned[[2]uuid.UUID{b.LineID, b.LotID}] = b.Qty
	}
	out := map[uuid.UUID][]soldLot{}
	at := map[[2]uuid.UUID]int{}
	for _, m := range moves {
		if m.DocLineID == nil || !m.Qty.IsNegative() {
			continue
		}
		k := [2]uuid.UUID{*m.DocLineID, m.LotID}
		if i, ok := at[k]; ok {
			out[*m.DocLineID][i].Sold = out[*m.DocLineID][i].Sold.Add(m.Qty.Neg())
			continue
		}
		at[k] = len(out[*m.DocLineID])
		out[*m.DocLineID] = append(out[*m.DocLineID], soldLot{
			LotID: m.LotID, MoveID: m.ID, ColorID: m.ColorID, UnitCost: m.UnitCost, Sold: m.Qty.Neg(), Returned: returned[k],
		})
	}
	return out, nil
}

// returnedAmounts is what posted returns gave back on each line of a sales document.
func returnedAmounts(ctx context.Context, q db.Querier, docID uuid.UUID) (map[uuid.UUID]money.Money, error) {
	var rows []struct {
		LineID uuid.UUID   `db:"sales_document_line_id"`
		Amount money.Money `db:"amount"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT rl.sales_document_line_id, SUM(rl.line_amount) AS amount
		FROM sales_return_lines rl JOIN sales_returns r ON r.id = rl.sales_return_id
		WHERE r.sales_document_id = $1 AND r.status = 'posted'
		GROUP BY rl.sales_document_line_id`, docID); err != nil {
		return nil, wrap("read returned amounts", err)
	}
	out := make(map[uuid.UUID]money.Money, len(rows))
	for _, r := range rows {
		out[r.LineID] = r.Amount
	}
	return out, nil
}

// skus are the SKUs a return puts back.
func (p returnPlan) skus() []uuid.UUID {
	out := make([]uuid.UUID, len(p.Lines))
	for i, l := range p.Lines {
		out[i] = l.Line.SkuID
	}
	return sortedUnique(out)
}

// writeReturn writes a planned return under docNo with its debt split: the header, the stock it
// puts back (and quarantines), its lines and what it took off the sold lines.
func (s *Service) writeReturn(ctx context.Context, tx posting.Tx, id uuid.UUID, docNo, idemKey string, p returnPlan,
	debt, refund money.Money,
) (SalesReturn, error) {
	warehouse, err := warehouseID(ctx, tx)
	if err != nil {
		return SalesReturn{}, err
	}
	var docID *uuid.UUID
	if p.Doc != nil {
		docID = &p.Doc.ID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sales_returns (id, tenant_id, warehouse_id, doc_no, customer_party_id, sales_document_id, reason_id,
		                           business_date, total_amount, debt_reduction_amount, refund_due_amount, status,
		                           idempotency_key, posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'posted', $12, $13, $14, $13)`,
		id, tx.TenantID(), warehouse, docNo, p.PartyID, docID, p.ReasonID, tx.BusinessDate(), p.Total, debt, refund,
		idemKey, tx.Now(), tx.Actor().ID); err != nil {
		return SalesReturn{}, wrap("insert sales return", err)
	}
	stockDoc := StockDoc{Type: StockDocSalesReturn, ID: id}
	receives := make([]StockReceive, len(p.Lines))
	for i, l := range p.Lines {
		receives[i] = l.Receive
	}
	moves, err := s.stock.Receive(ctx, tx, stockDoc, receives)
	if err != nil {
		return SalesReturn{}, err
	}
	byLine := make(map[uuid.UUID]StockMove, len(moves))
	for _, m := range moves {
		if m.DocLineID != nil {
			byLine[*m.DocLineID] = m
		}
	}
	var quarantine []StockQuarantine
	for i := range p.Lines {
		l := &p.Lines[i].Line
		m, ok := byLine[l.ID]
		if !ok {
			return SalesReturn{}, fmt.Errorf("sales: stock booked no move for return line %s", l.ID)
		}
		if l.CostSource == CostCurrentAverage {
			l.LotID, l.UnitCost = m.LotID, m.UnitCost
		}
		if l.RestockTo == RestockQuarantine {
			quarantine = append(quarantine, StockQuarantine{DocLineID: l.ID, SkuID: l.SkuID, BaseQty: l.BaseQty, ColorID: l.ColorID, LotID: l.LotID})
		}
	}
	if len(quarantine) > 0 {
		if _, err := s.stock.Quarantine(ctx, tx, stockDoc, quarantine); err != nil {
			return SalesReturn{}, err
		}
	}
	for _, pl := range p.Lines {
		l := pl.Line
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sales_return_lines (id, tenant_id, sales_return_id, sales_document_line_id, sku_id, sku_code_snap,
			                                name_snap, variant_label_snap, unit_id, unit_name_snap, unit_factor_snap, qty,
			                                base_qty, color_id, spec_text, unit_price, line_amount, unit_cost, cost_source,
			                                restock_to, lot_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
			l.ID, tx.TenantID(), id, l.SalesDocumentLineID, l.SkuID, l.SkuCode, l.Name, l.VariantLabel, l.UnitID, l.UnitName,
			l.UnitFactor, l.Qty, l.BaseQty, l.ColorID, l.SpecText, l.UnitPrice, l.LineAmount, l.UnitCost, l.CostSource,
			l.RestockTo, l.LotID); err != nil {
			return SalesReturn{}, wrap("insert sales return line", err)
		}
	}
	if p.Doc != nil {
		for _, lineID := range sortedKeys(p.Taken) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE sales_document_lines SET returned_base_qty = returned_base_qty + $2 WHERE id = $1`,
				lineID, p.Taken[lineID]); err != nil {
				return SalesReturn{}, wrap("count returned quantity", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sales_documents SET status = 'partially_reversed' WHERE id = $1 AND status = 'posted'`, p.Doc.ID); err != nil {
			return SalesReturn{}, wrap("mark sales document returned", err)
		}
	}
	return returnDetail(ctx, tx, id)
}

func sortedKeys[V any](m map[uuid.UUID]V) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return sortedUnique(out)
}

// cost is what the returned goods cost.
func (p returnPlan) cost() money.Money {
	out := money.ZeroMoney
	for _, l := range p.Lines {
		out = out.Add(l.Line.UnitCost.MulQty(l.Line.BaseQty))
	}
	return out
}

// PostReturn posts a sales return (TH): the goods go back to stock, into the lots they were sold
// from at their cost (or to quarantine), and their value first reduces what the customer owes;
// what is left is refunded at once with RefundMethodID or stays due to the customer.
func (s *Service) PostReturn(ctx context.Context, actor audit.Actor, key idempotency.Key, in ReturnInput) (posting.Response, error) {
	in.CustomerID = nonNilID(in.CustomerID)
	if err := validateReturn(in); err != nil {
		return posting.Response{}, err
	}
	var date time.Time
	var plan returnPlan
	var receivable *Obligation
	refund := false
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		var err error
		if plan, err = s.planReturn(ctx, q, in.SalesDocumentID, in.CustomerID, in.ReasonID, in.Lines, "lines", false); err != nil {
			return err
		}
		if plan.Doc != nil {
			if receivable, err = s.debt.ReceivableOf(ctx, q, plan.Doc.ID); err != nil {
				return err
			}
		}
		if refund, err = s.refunds(ctx, q, plan, in.RefundMethodID); err != nil {
			return err
		}
		date, err = s.today(ctx, q, actor.TenantID)
		return err
	}); err != nil {
		return s.postRefused(ctx, actor, key, ScopeReturnPost, err)
	}
	cmd := command{scope: ScopeReturnPost, date: date, prefix: "TH", locks: stockLocks(plan.skus())}
	if refund {
		cmd.extra = []string{"PC"}
	}
	if receivable != nil {
		cmd.locks.Add(posting.LockObligations, receivable.ID)
	}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		p, err := s.planReturn(ctx, tx, in.SalesDocumentID, in.CustomerID, in.ReasonID, in.Lines, "lines", true)
		if err != nil {
			return posting.Result{}, err
		}
		var method *PaymentMethod
		if in.RefundMethodID != nil {
			m, err := s.checkMethod(ctx, tx, *in.RefundMethodID, "refund_payment_method_id")
			if err != nil {
				return posting.Result{}, err
			}
			method = &m
		}
		id := ids.New()
		debt, due, refundDue := money.ZeroMoney, p.Total, (*Obligation)(nil)
		if p.PartyID != nil && p.Total.IsPositive() {
			var targets []uuid.UUID
			if p.Doc != nil {
				ob, err := s.debt.ReceivableOf(ctx, tx, p.Doc.ID)
				if err != nil {
					return posting.Result{}, err
				}
				if ob != nil {
					targets = []uuid.UUID{ob.ID}
				}
			}
			cn, err := s.debt.RecordCreditNote(ctx, tx, CreditNoteInput{
				PartyID: *p.PartyID, SourceID: id, SourceDocNo: tx.DocNo(), Amount: p.Total, ReasonID: &p.ReasonID, Targets: targets,
			})
			if err != nil {
				return posting.Result{}, err
			}
			debt, due, refundDue = cn.Settled, p.Total.Sub(cn.Settled), cn.RefundDue
		}
		payNow := due.IsPositive() && method != nil
		if payNow != refund {
			return posting.Result{}, errConflict(CodeReturnChanged, "What the customer owes changed while the return was posted; post it again")
		}
		ret, err := s.writeReturn(ctx, tx, id, tx.DocNo(), key.Value, p, debt, due)
		if err != nil {
			return posting.Result{}, err
		}
		out := ReturnPosted{Return: ret}
		if payNow {
			pay, err := s.payRefund(ctx, tx, ScopeReturnPost, key, p.PartyID, method.ID, due, PaymentSourceSalesReturn, id, refundDue)
			if err != nil {
				return posting.Result{}, err
			}
			out.Refund = &pay
		}
		s.publishReturn(tx, ret, p)
		return posting.Result{Status: http.StatusCreated, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// refunds tells whether a return pays money back at once: a walk-in customer is always paid
// back (and must say how), a named customer only what the return leaves after their open
// receivables, and only with a refund method.
func (s *Service) refunds(ctx context.Context, q db.Querier, p returnPlan, method *uuid.UUID) (bool, error) {
	if !p.Total.IsPositive() {
		return false, nil
	}
	if p.PartyID == nil {
		if method == nil {
			return false, refundRequired("refund_payment_method_id")
		}
		return true, nil
	}
	if method == nil {
		return false, nil
	}
	open, err := s.debt.OpenReceivables(ctx, q, *p.PartyID)
	if err != nil {
		return false, err
	}
	return p.Total.Cmp(open) > 0, nil
}

func refundRequired(field string) error {
	return apperr.NewValidation(CodeRefundRequired, "A walk-in customer is paid back at once; choose how",
		map[string]string{field: "required"})
}

// payRefund records the PC that pays a refund: for a named customer it settles the refund the
// credit note left due.
func (s *Service) payRefund(ctx context.Context, tx posting.Tx, scope string, key idempotency.Key, party *uuid.UUID,
	methodID uuid.UUID, amount money.Money, source string, sourceID uuid.UUID, due *Obligation,
) (Payment, error) {
	in := PaymentInput{
		Direction: DirectionOut, PartyID: party, Purpose: PurposeRefund, MethodID: methodID, Amount: amount,
		SourceType: source, SourceID: &sourceID, Key: paymentKey(scope, key, 0),
	}
	if party != nil {
		if due == nil || !due.Open().Equal(amount) {
			return Payment{}, fmt.Errorf("sales: credit note left no refund due of %s", amount.String())
		}
		in.Allocations = []PaymentAllocation{{ObligationID: due.ID, Amount: amount}}
	}
	return s.debt.RecordPayment(ctx, tx, in)
}

func (s *Service) publishReturn(tx posting.Tx, ret SalesReturn, p returnPlan) {
	tx.Audit(audit.Event{Action: ScopeReturnPost, EntityType: entityReturn, EntityID: ret.ID, DocNo: ret.DocNo, After: ret})
	tx.Publish(outbox.Event{Topic: TopicReturnPosted, Payload: map[string]any{
		"sales_return_id": ret.ID, "doc_no": ret.DocNo, "sales_document_id": ret.SalesDocumentID,
		"customer_party_id": ret.CustomerID, "business_date": ret.BusinessDate, "total_amount": ret.Total,
		"debt_reduction_amount": ret.DebtReduction, "refund_due_amount": ret.RefundDue, "cost_amount": p.cost(),
		"exchange_id": ret.ExchangeID,
	}})
}

func returnDetail(ctx context.Context, q db.Querier, id uuid.UUID) (SalesReturn, error) {
	r, err := getReturn(ctx, q, id)
	if err != nil {
		return r, err
	}
	r.Lines, err = returnLinesOf(ctx, q, id)
	return r, err
}

// SalesReturns lists sales returns newest first, with their lines.
func (s *Service) SalesReturns(ctx context.Context, scope Scope, f ReturnFilter) ([]SalesReturn, error) {
	var out []SalesReturn
	err := s.read(ctx, scope, func(q db.Querier) error {
		var w where
		if f.SalesDocumentID != nil {
			w.add("sales_document_id = $%d", *f.SalesDocumentID)
		}
		if f.CustomerID != nil {
			w.add("customer_party_id = $%d", *f.CustomerID)
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+returnColumns+` FROM sales_returns`+w.sql()+tail, w.args...); err != nil {
			return wrap("list sales returns", err)
		}
		for i := range out {
			lines, err := returnLinesOf(ctx, q, out[i].ID)
			if err != nil {
				return err
			}
			out[i].Lines = lines
		}
		return nil
	})
	return out, err
}
