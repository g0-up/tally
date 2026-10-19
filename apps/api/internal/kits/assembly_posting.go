package kits

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// BatchLine is what a batch took for one order item: Qty went into the kits, ScrapQty was lost
// on the way (hao hụt). LotID picks the component lot, else ColorID the color (the item's fixed
// color when it has one), else the lot is taken first expiry first.
type BatchLine struct {
	OrderItemID uuid.UUID
	Qty         money.Qty
	ScrapQty    money.Qty
	ColorID     *uuid.UUID
	LotID       *uuid.UUID
}

// BatchInput posts a batch of ProducedQty kits. An item without lines takes its share of what it
// still needs: all of it when the batch completes the order.
type BatchInput struct {
	ProducedQty  money.Qty
	BusinessDate *time.Time
	Lines        []BatchLine
}

// BatchResult is a posted batch and the order after it.
type BatchResult struct {
	Posting AssemblyPosting
	Order   OrderDetail
}

func validateBatch(in BatchInput) map[string]string {
	fields := map[string]string{}
	if !in.ProducedQty.IsPositive() || !in.ProducedQty.IsWhole() {
		fields["produced_qty"] = "must_be_positive_whole"
	}
	if len(in.Lines) > MaxItems {
		fields["lines"] = "too_many"
	}
	for i, l := range in.Lines {
		if l.OrderItemID == uuid.Nil {
			fields[lineField(i, "order_item_id")] = "required"
		}
		switch {
		case l.Qty.IsNegative():
			fields[lineField(i, "qty")] = "must_not_be_negative"
		case l.ScrapQty.IsNegative():
			fields[lineField(i, "scrap_qty")] = "must_not_be_negative"
		case !l.Qty.IsPositive() && !l.ScrapQty.IsPositive():
			fields[lineField(i, "qty")] = "must_be_positive"
		}
	}
	return fields
}

// batchIssue is one stock issue of a batch. Line is the request line, -1 for the share of an
// item the request named no line for.
type batchIssue struct {
	Line    int
	Item    OrderItem
	Qty     money.Qty
	Scrap   money.Qty
	ColorID *uuid.UUID
	LotID   *uuid.UUID
}

// planBatch turns the request lines of a batch of produced kits into stock issues. left is the
// kits the order still plans. What a line puts into the kits counts against what its item still
// needs; scrap does not. An item the request names no line for takes remaining × produced / left.
func planBatch(items []OrderItem, produced, left money.Qty, lines []BatchLine) ([]batchIssue, error) {
	byID := make(map[uuid.UUID]OrderItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	fields := map[string]string{}
	used := map[uuid.UUID]money.Qty{}
	out := make([]batchIssue, 0, len(items)+len(lines))
	for i, l := range lines {
		it, ok := byID[l.OrderItemID]
		if !ok {
			fields[lineField(i, "order_item_id")] = "not_found"
			continue
		}
		color := l.ColorID
		if it.ColorID != nil {
			if l.ColorID != nil && *l.ColorID != *it.ColorID {
				fields[lineField(i, "color_id")] = "color_fixed"
				continue
			}
			color = it.ColorID
		}
		total := used[it.ID].Add(l.Qty)
		used[it.ID] = total
		if total.Cmp(it.Remaining()) > 0 {
			fields[lineField(i, "qty")] = CodeExceedsRemaining
			continue
		}
		out = append(out, batchIssue{Line: i, Item: it, Qty: l.Qty, Scrap: l.ScrapQty, ColorID: color, LotID: l.LotID})
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	for _, it := range items {
		if _, named := used[it.ID]; named {
			continue
		}
		qty, err := share(it.Remaining(), produced, left)
		if err != nil {
			return nil, err
		}
		if qty.IsPositive() {
			out = append(out, batchIssue{Line: -1, Item: it, Qty: qty, Scrap: money.ZeroQty, ColorID: it.ColorID})
		}
	}
	return out, nil
}

// PostBatch posts a batch of an order in one balanced posting: the component lots it took (scrap
// included) are issued, one kit lot is produced at their value plus the packing cost, carrying
// the earliest expiry of the components, and its genealogy is recorded.
func (s *Service) PostBatch(ctx context.Context, actor audit.Actor, key idempotency.Key, orderID uuid.UUID, in BatchInput) (posting.Response, error) {
	if fields := validateBatch(in); len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	date, err := s.postingDate(ctx, actor.TenantID, in.BusinessDate)
	if err != nil {
		return posting.Response{}, err
	}
	idemKey := ScopePost + ":" + key.Value
	return s.postOnOrder(ctx, actor, key, ScopePost, orderID, date, "", true, nil,
		func(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem) (posting.Result, error) {
			return s.postBatch(ctx, tx, o, items, in, idemKey)
		})
}

func (s *Service) postBatch(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem, in BatchInput, idemKey string) (posting.Result, error) {
	if !s.status.Open(o.Status) {
		return posting.Result{}, errOrderNotOpen()
	}
	left := o.PlannedQty.Sub(o.Net())
	if in.ProducedQty.Cmp(left) > 0 {
		return posting.Result{}, errInvalid(map[string]string{"produced_qty": CodeExceedsRemaining})
	}
	issues, err := planBatch(items, in.ProducedQty, left, in.Lines)
	if err != nil {
		return posting.Result{}, err
	}
	consumedRes, err := s.takeHolds(ctx, tx, items, issues)
	if err != nil {
		return posting.Result{}, err
	}
	if err := checkFreeStock(ctx, tx, issues); err != nil {
		return posting.Result{}, err
	}

	postingID := ids.New()
	doc := StockDoc{Type: DocAssemblyPosting, ID: postingID}
	usedMoves, scrapMoves, err := s.issueBatch(ctx, tx, doc, issues)
	if err != nil {
		return posting.Result{}, err
	}
	if len(usedMoves) == 0 {
		return posting.Result{}, errInvalid(map[string]string{"lines": "nothing_to_consume"})
	}
	r, err := recipeByID(ctx, tx, o.KitRecipeID)
	if err != nil {
		return posting.Result{}, err
	}
	p := AssemblyPosting{ID: postingID, OrderID: o.ID, ProducedQty: in.ProducedQty, ComponentsCost: money.ZeroMoney,
		PackingCost: r.PackingCost.MulQty(in.ProducedQty), BusinessDate: tx.BusinessDate(), PostedAt: tx.Now(),
		PostedBy: tx.Actor().ID}
	if p.Items, err = postingItems(usedMoves, scrapMoves); err != nil {
		return posting.Result{}, err
	}
	for _, m := range append(append([]Move{}, usedMoves...), scrapMoves...) {
		p.ComponentsCost = p.ComponentsCost.Add(m.Value.Abs())
	}
	total := p.ComponentsCost.Add(p.PackingCost)
	if p.OutputUnitCost, err = money.RoundMoney(total.Decimal().Div(in.ProducedQty.Decimal())); err != nil {
		return posting.Result{}, err
	}

	var batches int
	if err := tx.QueryRowxContext(ctx, `SELECT count(*) FROM assembly_postings WHERE assembly_order_id = $1`, o.ID).Scan(&batches); err != nil {
		return posting.Result{}, fmt.Errorf("kits: count assembly postings: %w", err)
	}
	kitMoves, err := s.stock.Receive(ctx, tx, doc, MoveAssemblyProduce, []ReceiveLine{{SkuID: o.KitSkuID, Qty: in.ProducedQty,
		LotNo: kitLotNo(o.DocNo, batches+1), ExpiryDate: kitExpiry(usedMoves), Value: total}})
	if err != nil {
		return posting.Result{}, err
	}
	p.OutputLotID = kitMoves[0].LotID
	if err := insertPosting(ctx, tx, p, idemKey); err != nil {
		return posting.Result{}, err
	}
	if err := insertGenealogy(ctx, tx, p, usedMoves); err != nil {
		return posting.Result{}, err
	}
	for id, qty := range usedByItem(issues) {
		if err := addItemConsumed(ctx, tx, id, qty); err != nil {
			return posting.Result{}, err
		}
	}

	next := o
	next.CompletedQty = o.CompletedQty.Add(in.ProducedQty)
	if next.Status, err = s.status.Move(o.Status, s.status.AfterProgress(o.Status, next.Net(), o.PlannedQty, false)); err != nil {
		return posting.Result{}, err
	}
	if err := saveOrderProgress(ctx, tx, next, tx.Now()); err != nil {
		return posting.Result{}, err
	}
	if err := s.trimHolds(ctx, tx, items, issues, consumedRes, next.Status == OrderCompleted); err != nil {
		return posting.Result{}, err
	}

	if p, err = readPosting(ctx, tx, postingID); err != nil {
		return posting.Result{}, err
	}
	d, err := readOrderDetail(ctx, tx, o.ID, false)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(audit.Event{Action: "assembly_posting.create", EntityType: "assembly_posting", EntityID: p.ID, After: p})
	return posting.Result{Status: http.StatusCreated, Body: batchDTO(BatchResult{Posting: p, Order: d})}, nil
}

// usedByItem is what a batch put into its kits of each item.
func usedByItem(issues []batchIssue) map[uuid.UUID]money.Qty {
	out := map[uuid.UUID]money.Qty{}
	for _, is := range issues {
		out[is.Item.ID] = out[is.Item.ID].Add(is.Qty)
	}
	return out
}

// takeHolds turns the reservations of the items into the issues: each gives what it holds, up to
// what its item takes (scrap included). It returns what each reservation gave.
func (s *Service) takeHolds(ctx context.Context, tx posting.Tx, items []OrderItem, issues []batchIssue) (map[uuid.UUID]money.Qty, error) {
	take := map[uuid.UUID]money.Qty{}
	for _, is := range issues {
		take[is.Item.ID] = take[is.Item.ID].Add(is.Qty).Add(is.Scrap)
	}
	out := map[uuid.UUID]money.Qty{}
	for _, it := range items {
		if it.ReservationID == nil || !it.Reserved.IsPositive() || !take[it.ID].IsPositive() {
			continue
		}
		qty := minQty(it.Reserved, take[it.ID])
		if err := s.reservations.Consume(ctx, tx, *it.ReservationID, qty); err != nil {
			return nil, err
		}
		out[it.ID] = qty
	}
	return out, nil
}

// trimHolds releases what the reservations hold beyond what their items still need: all of it
// when the order is completed.
func (s *Service) trimHolds(ctx context.Context, tx posting.Tx, items []OrderItem, issues []batchIssue,
	consumedRes map[uuid.UUID]money.Qty, completed bool) error {
	used := usedByItem(issues)
	for _, it := range items {
		if it.ReservationID == nil {
			continue
		}
		held := it.Reserved.Sub(consumedRes[it.ID])
		if !held.IsPositive() {
			continue
		}
		want := it.PlannedBaseQty.Sub(it.ConsumedBaseQty).Sub(used[it.ID])
		if completed || want.IsNegative() {
			want = money.ZeroQty
		}
		if want.Cmp(held) >= 0 {
			continue
		}
		excess := held.Sub(want)
		if err := s.reservations.Release(ctx, tx, *it.ReservationID, &excess); err != nil {
			return err
		}
	}
	return nil
}

// checkFreeStock refuses a batch that would take stock other documents hold: per SKU, what it
// issues must fit in the sellable stock less what reservations still hold (the batch's own were
// consumed first).
func checkFreeStock(ctx context.Context, q db.Querier, issues []batchIssue) error {
	need := map[uuid.UUID]money.Qty{}
	var order []uuid.UUID
	for _, is := range issues {
		if _, ok := need[is.Item.SkuID]; !ok {
			order = append(order, is.Item.SkuID)
		}
		need[is.Item.SkuID] = need[is.Item.SkuID].Add(is.Qty).Add(is.Scrap)
	}
	for _, skuID := range order {
		if err := checkFree(ctx, q, skuID, need[skuID]); err != nil {
			return err
		}
	}
	return nil
}

// checkFree refuses taking need of a SKU beyond its sellable stock less what reservations hold.
// The caller holds the SKU's sku_stock lock, which every reservation change takes first, so no
// hold can appear between the check and the issue.
func checkFree(ctx context.Context, q db.Querier, skuID uuid.UUID, need money.Qty) error {
	var free money.Qty
	if err := q.QueryRowxContext(ctx, `SELECT COALESCE(sum(sellable - reserved), 0) FROM sku_stock WHERE sku_id = $1`,
		skuID).Scan(&free); err != nil {
		return fmt.Errorf("kits: read free stock: %w", err)
	}
	if need.Cmp(free) <= 0 {
		return nil
	}
	return errInsufficientStock(ctx, q, skuID, need, free)
}

// errInsufficientStock is the 409 refusing need of a SKU of which only free is not held.
func errInsufficientStock(ctx context.Context, q db.Querier, skuID uuid.UUID, need, free money.Qty) error {
	var code string
	if err := q.QueryRowxContext(ctx, `SELECT code FROM skus WHERE id = $1`, skuID).Scan(&code); err != nil {
		return fmt.Errorf("kits: read sku code: %w", err)
	}
	if free.IsNegative() {
		free = money.ZeroQty
	}
	e := apperr.NewConflict(CodeInsufficientStock, "Not enough free stock of "+code+": other documents hold the rest")
	e.Fields = map[string]string{"sku": code, "requested_qty": need.String(), "available": free.String()}
	return e
}

// issueBatch issues what goes into the kits, then the scrap, each line from its item's lot or
// color; the moves name the item as their document line.
func (s *Service) issueBatch(ctx context.Context, tx posting.Tx, doc StockDoc, issues []batchIssue) (used, scrap []Move, err error) {
	var usedLines, scrapLines []IssueLine
	var usedIdx, scrapIdx []int
	for _, is := range issues {
		itemID := is.Item.ID
		if is.Qty.IsPositive() {
			usedLines = append(usedLines, IssueLine{DocLineID: &itemID, SkuID: is.Item.SkuID, Qty: is.Qty, ColorID: is.ColorID, LotID: is.LotID})
			usedIdx = append(usedIdx, is.Line)
		}
		if is.Scrap.IsPositive() {
			scrapLines = append(scrapLines, IssueLine{DocLineID: &itemID, SkuID: is.Item.SkuID, Qty: is.Scrap, ColorID: is.ColorID,
				LotID: is.LotID})
			scrapIdx = append(scrapIdx, is.Line)
		}
	}
	if len(usedLines) > 0 {
		if used, err = s.stock.Issue(ctx, tx, doc, MoveAssemblyConsume, usedLines); err != nil {
			return nil, nil, remapLines(err, usedIdx, "lines")
		}
	}
	if len(scrapLines) > 0 {
		if scrap, err = s.stock.Issue(ctx, tx, doc, MoveAssemblyConsume, scrapLines); err != nil {
			return nil, nil, remapLines(err, scrapIdx, "lines")
		}
	}
	return used, scrap, nil
}

// postingItems sums the moves of a batch per item and lot: what went into the kits and the scrap
// make the consumption, the scrap is noted apart.
func postingItems(used, scrap []Move) ([]PostingItem, error) {
	type key struct{ item, lot uuid.UUID }
	at := map[key]int{}
	var out []PostingItem
	values := []money.Money{}
	add := func(m Move, isScrap bool) error {
		if m.DocLineID == nil {
			return fmt.Errorf("kits: stock move %s names no order item", m.ID)
		}
		k := key{*m.DocLineID, m.LotID}
		i, ok := at[k]
		if !ok {
			i = len(out)
			at[k] = i
			out = append(out, PostingItem{ID: ids.New(), OrderItemID: k.item, SkuID: m.SkuID, LotID: m.LotID, LotNo: m.LotNo,
				ColorID: m.ColorID, ConsumedQty: money.ZeroQty, ScrapQty: money.ZeroQty})
			values = append(values, money.ZeroMoney)
		}
		qty := m.Qty.Neg()
		out[i].ConsumedQty = out[i].ConsumedQty.Add(qty)
		if isScrap {
			out[i].ScrapQty = out[i].ScrapQty.Add(qty)
		}
		values[i] = values[i].Add(m.Value.Abs())
		return nil
	}
	for _, m := range used {
		if err := add(m, false); err != nil {
			return nil, err
		}
	}
	for _, m := range scrap {
		if err := add(m, true); err != nil {
			return nil, err
		}
	}
	for i := range out {
		cost, err := money.RoundMoney(values[i].Decimal().Div(out[i].ConsumedQty.Decimal()))
		if err != nil {
			return nil, err
		}
		out[i].UnitCost = cost
	}
	return out, nil
}

func insertPosting(ctx context.Context, q posting.Tx, p AssemblyPosting, idemKey string) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO assembly_postings (id, tenant_id, assembly_order_id, produced_qty, output_lot_id, components_cost, packing_cost,
		  output_unit_cost, business_date, idempotency_key, posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $11)`,
		p.ID, q.TenantID(), p.OrderID, p.ProducedQty, p.OutputLotID, p.ComponentsCost, p.PackingCost, p.OutputUnitCost,
		p.BusinessDate, idemKey, p.PostedAt, p.PostedBy); err != nil {
		return fmt.Errorf("kits: insert assembly posting: %w", err)
	}
	for _, it := range p.Items {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO assembly_posting_items (id, tenant_id, assembly_posting_id, assembly_order_item_id, sku_id, lot_id, color_id,
			  consumed_base_qty, scrap_base_qty, unit_cost)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			it.ID, q.TenantID(), p.ID, it.OrderItemID, it.SkuID, it.LotID, it.ColorID, it.ConsumedQty, it.ScrapQty, it.UnitCost); err != nil {
			return fmt.Errorf("kits: insert assembly posting item: %w", err)
		}
	}
	return nil
}

// insertGenealogy links the kit lot of a batch to each component lot that went into its kits.
func insertGenealogy(ctx context.Context, q posting.Tx, p AssemblyPosting, used []Move) error {
	qty := map[uuid.UUID]money.Qty{}
	var lots []uuid.UUID
	for _, m := range used {
		if _, ok := qty[m.LotID]; !ok {
			lots = append(lots, m.LotID)
		}
		qty[m.LotID] = qty[m.LotID].Add(m.Qty.Neg())
	}
	for _, lotID := range lots {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO lot_genealogy (id, tenant_id, parent_lot_id, child_lot_id, assembly_posting_id, child_base_qty, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			ids.New(), q.TenantID(), p.OutputLotID, lotID, p.ID, qty[lotID], p.PostedAt); err != nil {
			return fmt.Errorf("kits: insert lot genealogy: %w", err)
		}
	}
	return nil
}

func minQty(a, b money.Qty) money.Qty {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}
