package seedload

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// Document sizes, in lines.
const (
	receiptMinLines, receiptMaxLines = 50, 200
	posMinLines, posMaxLines         = 3, 25
	invoiceMinLines, invoiceMaxLines = 10, 60
	countMinSkus, countMaxSkus       = 10, 50
	// countEditBatch is the most count lines one update may change.
	countEditBatch = 500
)

// roundShape is what one round posts. Receipts come first so the sales find stock; returns follow
// the document they return from; payments follow the invoices they settle.
type roundShape struct {
	receipts, posSales, invoices                          int
	supplierReturn, salesReturn, payment, kitBatch, count bool
}

// shapeOf is the shape of round (0-based); round 0 posts every kind of document.
func shapeOf(round int) roundShape {
	return roundShape{
		receipts: 2, posSales: 8, invoices: 2,
		supplierReturn: round%5 == 0,
		salesReturn:    round%3 == 0,
		payment:        round%2 == 0,
		kitBatch:       round%3 == 0,
		count:          round%4 == 0,
	}
}

// stockLine is a document line the generator remembers: qty base units of goods in slot.
type stockLine struct {
	id    uuid.UUID
	goods int
	slot  int
	qty   int64
}

func (r *runner) round(ctx context.Context, round int) error {
	shape := shapeOf(round)
	// The kit of the round is drawn first and its components go into the first receipt, so the
	// batch finds them in stock however large the catalog is.
	kit := -1
	if shape.kitBatch {
		kit = r.pickKit()
	}
	for i := range shape.receipts {
		var force []int
		if i == 0 && kit >= 0 {
			for _, it := range r.plan.Kits[kit].Items {
				force = append(force, it.Goods)
			}
		}
		rec, err := r.receipt(ctx, force)
		if err != nil {
			return err
		}
		if i == 0 && shape.supplierReturn {
			if err := r.supplierReturn(ctx, rec); err != nil {
				return err
			}
		}
	}
	var sold []posted
	for range shape.posSales {
		sale, ok, err := r.posSale(ctx)
		if err != nil {
			return err
		}
		if ok {
			sold = append(sold, sale)
		}
	}
	if shape.salesReturn && len(sold) > 0 {
		if err := r.salesReturn(ctx, sold[r.rng.Intn(len(sold))]); err != nil {
			return err
		}
	}
	for range shape.invoices {
		if err := r.invoice(ctx); err != nil {
			return err
		}
	}
	if shape.payment {
		if err := r.payment(ctx); err != nil {
			return err
		}
	}
	if kit >= 0 {
		if err := r.kitBatch(ctx, kit); err != nil {
			return err
		}
	}
	if shape.count {
		return r.count(ctx)
	}
	return nil
}

// posted is a posted document with the lines the generator remembers.
type posted struct {
	id    uuid.UUID
	lines []stockLine
}

// receipt posts goods from a supplier, one line per SKU; the goods in force are always received.
func (r *runner) receipt(ctx context.Context, force []int) (posted, error) {
	n := min(r.rng.Between(receiptMinLines, receiptMaxLines), len(r.plan.Goods))
	supplier := r.suppliers[r.rng.Intn(len(r.suppliers))]
	in := procurement.ReceiptInput{SupplierPartyID: &supplier}
	planned := make([]stockLine, 0, n)
	for _, g := range withGoods(force, r.rng.Sample(len(r.plan.Goods), n), n) {
		p := r.plan.Goods[g]
		line := stockLine{goods: g, slot: r.rng.Intn(p.Slots()), qty: int64(r.rng.Between(10, 120))}
		cost := vnd(p.Cost)
		li := procurement.ReceiptLineInput{
			SkuID: r.goodsSku[g], Qty: money.NewQtyFromInt(line.qty), ColorID: r.colorID(g, line.slot), UnitPrice: &cost,
		}
		if p.Kind == GoodsLot {
			r.lotSeq[g]++
			expiry := expiryBase.AddDate(0, 0, 7*r.lotSeq[g])
			li.LotNo, li.ExpiryDate = fmt.Sprintf("L%05d", r.lotSeq[g]), &expiry
		}
		in.Lines = append(in.Lines, li)
		planned = append(planned, line)
	}
	key, err := r.key("receipt", http.MethodPost, "/api/v1/receipts", in)
	if err != nil {
		return posted{}, err
	}
	resp, err := r.deps.Domains.Procurement.PostReceipt(ctx, r.actor, key, in)
	if err != nil {
		return posted{}, fmt.Errorf("receipt: %w", err)
	}
	var body struct {
		ID    uuid.UUID `json:"id"`
		Lines []struct {
			ID    uuid.UUID `json:"id"`
			SkuID uuid.UUID `json:"sku_id"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return posted{}, fmt.Errorf("receipt: decode response: %w", err)
	}
	ids := make(map[uuid.UUID]uuid.UUID, len(body.Lines))
	for _, l := range body.Lines {
		ids[l.SkuID] = l.ID
	}
	for i := range planned {
		l := &planned[i]
		id, ok := ids[r.goodsSku[l.goods]]
		if !ok {
			return posted{}, fmt.Errorf("receipt %s: no line for sku %s", body.ID, r.plan.Goods[l.goods].Code)
		}
		l.id = id
		r.stock.Add(l.goods, l.slot, l.qty)
	}
	r.docs[DocReceipts]++
	return posted{id: body.ID, lines: planned}, nil
}

// supplierReturn sends a few units of a receipt just posted back to its supplier, from the lots
// they went into, against the payable.
func (r *runner) supplierReturn(ctx context.Context, rec posted) error {
	lines := rec.lines
	k := r.rng.Between(1, min(5, len(lines)))
	in := procurement.ReturnInput{ReceiptID: rec.id, ReasonID: r.purchaseReason, Settlement: procurement.SettlementCreditPayable}
	var taken []stockLine
	for _, at := range r.rng.Sample(len(lines), k) {
		l := lines[at]
		q := int64(r.rng.Between(1, int(min(l.qty, 5))))
		if r.stock.Avail(l.goods, l.slot) < q {
			continue
		}
		in.Lines = append(in.Lines, procurement.ReturnLineInput{ReceiptLineID: l.id, Qty: money.NewQtyFromInt(q)})
		taken = append(taken, stockLine{goods: l.goods, slot: l.slot, qty: q})
	}
	if len(in.Lines) == 0 {
		return nil
	}
	key, err := r.key("supplier-return", http.MethodPost, "/api/v1/purchase-returns", in)
	if err != nil {
		return err
	}
	if _, err := r.deps.Domains.Procurement.PostReturn(ctx, r.actor, key, in); err != nil {
		return fmt.Errorf("supplier return: %w", err)
	}
	if err := r.takeAll(taken); err != nil {
		return err
	}
	r.docs[DocSupplierReturns]++
	return nil
}

// pickLines draws up to n SKUs in stock, one line each of 1..maxQty units it holds.
func (r *runner) pickLines(n, maxQty int) []stockLine {
	n = min(n, len(r.plan.Goods))
	seen := make(map[int]struct{}, n)
	out := make([]stockLine, 0, n)
	for attempt := 0; len(out) < n && attempt < 4*n; attempt++ {
		g := r.rng.Intn(len(r.plan.Goods))
		if _, dup := seen[g]; dup {
			continue
		}
		slot, ok := r.stock.StockedSlot(r.rng, g)
		if !ok {
			continue
		}
		seen[g] = struct{}{}
		avail := r.stock.Avail(g, slot)
		out = append(out, stockLine{goods: g, slot: slot, qty: int64(r.rng.Between(1, int(min(int64(maxQty), avail))))})
	}
	return out
}

func (r *runner) takeAll(lines []stockLine) error {
	for _, l := range lines {
		if err := r.stock.Take(l.goods, l.slot, l.qty); err != nil {
			return err
		}
	}
	return nil
}

// draft turns picked lines into sales lines at the given price of each goods.
func (r *runner) draft(lines []stockLine, price func(GoodsPlan) int64) (sales.DraftInput, int64) {
	var d sales.DraftInput
	var total int64
	for _, l := range lines {
		p := r.plan.Goods[l.goods]
		unit := vnd(price(p))
		total += price(p) * l.qty
		d.Lines = append(d.Lines, sales.LineInput{
			SkuID: &r.goodsSku[l.goods], ColorID: r.colorID(l.goods, l.slot), Qty: money.NewQtyFromInt(l.qty), UnitPrice: &unit,
		})
	}
	return d, total
}

// posSale sells a basket at the counter to a walk-in customer paying the exact total in cash;
// false when nothing is in stock.
func (r *runner) posSale(ctx context.Context) (posted, bool, error) {
	lines := r.pickLines(r.rng.Between(posMinLines, posMaxLines), 5)
	if len(lines) == 0 {
		return posted{}, false, nil
	}
	d, total := r.draft(lines, func(p GoodsPlan) int64 { return p.Retail })
	in := sales.CheckoutInput{DraftInput: d, Payments: []sales.PosPayment{{MethodID: r.cashMethod, Amount: vnd(total)}}}
	key, err := r.key("pos-sale", http.MethodPost, "/api/v1/pos/checkout", in)
	if err != nil {
		return posted{}, false, err
	}
	resp, err := r.deps.Domains.Sales.Checkout(ctx, r.actor, key, in, sales.Exceptions{})
	if err != nil {
		return posted{}, false, fmt.Errorf("pos sale: %w", err)
	}
	var sale sales.PosSale
	if err := json.Unmarshal(resp.Body, &sale); err != nil {
		return posted{}, false, fmt.Errorf("pos sale: decode response: %w", err)
	}
	if err := r.takeAll(lines); err != nil {
		return posted{}, false, err
	}
	bySku := make(map[uuid.UUID]int, len(lines))
	for i, l := range lines {
		bySku[r.goodsSku[l.goods]] = i
	}
	out := posted{id: sale.Document.ID}
	for _, dl := range sale.Document.Lines {
		if dl.SkuID == nil {
			continue
		}
		at, ok := bySku[*dl.SkuID]
		if !ok {
			return posted{}, false, fmt.Errorf("pos sale %s: unexpected line for sku %s", sale.Document.DocNo, *dl.SkuID)
		}
		l := lines[at]
		l.id = dl.ID
		out.lines = append(out.lines, l)
	}
	r.docs[DocPosSales]++
	return out, true, nil
}

// salesReturn takes back a few whole lines of a POS sale and refunds them in cash.
func (r *runner) salesReturn(ctx context.Context, sale posted) error {
	if len(sale.lines) == 0 {
		return nil
	}
	k := r.rng.Between(1, min(3, len(sale.lines)))
	in := sales.ReturnInput{SalesDocumentID: &sale.id, ReasonID: r.salesReason, RefundMethodID: &r.cashMethod}
	var back []stockLine
	for _, at := range r.rng.Sample(len(sale.lines), k) {
		l := sale.lines[at]
		in.Lines = append(in.Lines, sales.ReturnLineInput{SalesDocumentLineID: &l.id, Qty: money.NewQtyFromInt(l.qty)})
		back = append(back, l)
	}
	key, err := r.key("sales-return", http.MethodPost, "/api/v1/sales-returns", in)
	if err != nil {
		return err
	}
	if _, err := r.deps.Domains.Sales.PostReturn(ctx, r.actor, key, in); err != nil {
		return fmt.Errorf("sales return: %w", err)
	}
	for _, l := range back {
		r.stock.Add(l.goods, l.slot, l.qty)
	}
	r.docs[DocSalesReturns]++
	return nil
}

// invoice sells on credit to a customer at the wholesale price: the invoice is created,
// confirmed (reserving the goods) and delivered whole by one stock out.
func (r *runner) invoice(ctx context.Context) error {
	lines := r.pickLines(r.rng.Between(invoiceMinLines, invoiceMaxLines), 20)
	c := r.rng.Intn(len(r.customers))
	if len(lines) == 0 {
		return nil
	}
	d, _ := r.draft(lines, func(p GoodsPlan) int64 { return p.Wholesale })
	in := sales.InvoiceInput{CustomerID: r.customers[c], DraftInput: d}
	key, err := r.key("invoice", http.MethodPost, "/api/v1/invoices", in)
	if err != nil {
		return err
	}
	inv, _, err := r.deps.Domains.Sales.CreateInvoice(ctx, r.actor, &key, in)
	if err != nil {
		return fmt.Errorf("create invoice: %w", err)
	}
	confirm, err := r.key("invoice-confirm", http.MethodPost, "/api/v1/invoices/"+inv.DocNo+"/confirm", struct{ Version int }{inv.Version})
	if err != nil {
		return err
	}
	if _, err := r.deps.Domains.Sales.ConfirmInvoice(ctx, r.actor, confirm, inv.ID, inv.Version, sales.Exceptions{}); err != nil {
		return fmt.Errorf("confirm invoice %s: %w", inv.DocNo, err)
	}
	var out sales.StockOutInput
	for _, l := range inv.Lines {
		out.Lines = append(out.Lines, sales.StockOutLineInput{InvoiceLineID: l.ID})
	}
	outKey, err := r.key("stock-out", http.MethodPost, "/api/v1/invoices/"+inv.DocNo+"/stock-outs", out)
	if err != nil {
		return err
	}
	if _, err := r.deps.Domains.Sales.PostStockOut(ctx, r.actor, outKey, inv.ID, out); err != nil {
		return fmt.Errorf("stock out of invoice %s: %w", inv.DocNo, err)
	}
	if err := r.takeAll(lines); err != nil {
		return err
	}
	total := inv.Total.Decimal().IntPart()
	r.receivable[c] += total
	r.docs[DocInvoices]++
	r.docs[DocStockOuts]++
	return nil
}

// payment collects half of what a customer owes in cash, applied oldest debt first.
func (r *runner) payment(ctx context.Context) error {
	start := r.rng.Intn(len(r.customers))
	for i := range r.customers {
		c := (start + i) % len(r.customers)
		amount := r.receivable[c] / 2 / 1000 * 1000
		if amount < 1000 {
			continue
		}
		party := r.customers[c]
		allocs, err := r.deps.Domains.Debt.SuggestAllocation(ctx, debt.Scope{TenantID: r.t.ID}, party, debt.Receivable, vnd(amount))
		if err != nil {
			return fmt.Errorf("suggest allocation: %w", err)
		}
		in := debt.PaymentInput{
			Direction: debt.In, PartyID: &party, Purpose: debt.PurposeSettlement, MethodID: r.cashMethod,
			Amount: vnd(amount), Allocations: allocs,
		}
		key, err := r.key("payment", http.MethodPost, "/api/v1/payments", in)
		if err != nil {
			return err
		}
		if _, err := r.deps.Domains.Debt.PostPayment(ctx, r.actor, key, in, nil); err != nil {
			return fmt.Errorf("payment: %w", err)
		}
		r.receivable[c] -= amount
		r.docs[DocPayments]++
		return nil
	}
	return nil
}

// withGoods is force followed by the goods of sampled not in force, n goods at most.
func withGoods(force, sampled []int, n int) []int {
	out := make([]int, 0, n)
	seen := make(map[int]struct{}, len(force))
	for _, g := range force {
		if _, dup := seen[g]; !dup && len(out) < n {
			seen[g] = struct{}{}
			out = append(out, g)
		}
	}
	for _, g := range sampled {
		if _, dup := seen[g]; !dup && len(out) < n {
			out = append(out, g)
		}
	}
	return out
}

// pickKit draws a kit (not a combo) to assemble; -1 when the catalog has none.
func (r *runner) pickKit() int {
	var choices []int
	for k, kp := range r.plan.Kits {
		if !kp.Combo {
			choices = append(choices, k)
		}
	}
	if len(choices) == 0 {
		return -1
	}
	return choices[r.rng.Intn(len(choices))]
}

// kitBatch assembles a batch of kit k from the components in stock.
func (r *runner) kitBatch(ctx context.Context, k int) error {
	kp := r.plan.Kits[k]
	planned := int64(10)
	for _, it := range kp.Items {
		planned = min(planned, r.stock.Total(it.Goods)/it.Qty)
	}
	if planned <= 0 {
		return nil
	}
	in := kits.OrderInput{KitID: r.kitProduct[k], PlannedQty: money.NewQtyFromInt(planned)}
	key, err := r.key("assembly-order", http.MethodPost, "/api/v1/assembly-orders", in)
	if err != nil {
		return err
	}
	order, _, err := r.deps.Domains.Kits.CreateOrder(ctx, r.actor, &key, in)
	if err != nil {
		return fmt.Errorf("create assembly order of %s: %w", kp.Code, err)
	}
	reserve, err := r.key("assembly-reserve", http.MethodPost, "/api/v1/assembly-orders/"+order.DocNo+"/reserve", struct{}{})
	if err != nil {
		return err
	}
	if _, err := r.deps.Domains.Kits.ReserveOrder(ctx, r.actor, reserve, order.ID); err != nil {
		return fmt.Errorf("reserve assembly order %s: %w", order.DocNo, err)
	}
	batch := kits.BatchInput{ProducedQty: money.NewQtyFromInt(planned)}
	batchKey, err := r.key("assembly-batch", http.MethodPost, "/api/v1/assembly-orders/"+order.DocNo+"/batches", batch)
	if err != nil {
		return err
	}
	if _, err := r.deps.Domains.Kits.PostBatch(ctx, r.actor, batchKey, order.ID, batch); err != nil {
		return fmt.Errorf("post batch of assembly order %s: %w", order.DocNo, err)
	}
	for _, it := range kp.Items {
		if err := r.stock.Take(it.Goods, 0, it.Qty*planned); err != nil {
			return err
		}
	}
	r.stock.AddKits(k, planned)
	r.docs[DocKitBatches]++
	return nil
}

// count counts a selection of SKUs in stock and finds exactly what the books hold, so the count
// posts without a variance.
func (r *runner) count(ctx context.Context) error {
	n := min(r.rng.Between(countMinSkus, countMaxSkus), len(r.plan.Goods))
	var skus []uuid.UUID
	for _, g := range r.rng.Sample(len(r.plan.Goods), n) {
		if r.stock.Total(g) > 0 {
			skus = append(skus, r.goodsSku[g])
		}
	}
	if len(skus) == 0 {
		return nil
	}
	r.keys["count"]++
	startKey := fmt.Sprintf("seed-load:%d:%d:count:%d", r.opts.Seed, r.t.Index, r.keys["count"])
	inv := r.deps.Domains.Inventory
	detail, _, err := inv.StartCount(ctx, r.actor, &startKey, inventory.CountInput{Scope: inventory.ScopeSelection, SkuIDs: skus})
	if err != nil {
		return fmt.Errorf("start count: %w", err)
	}
	edits := make([]inventory.CountLineInput, len(detail.Lines))
	for i, l := range detail.Lines {
		id, qty := l.ID, l.SystemQty
		edits[i] = inventory.CountLineInput{LineID: &id, CountedQty: &qty}
	}
	for from := 0; from < len(edits); from += countEditBatch {
		if detail, err = inv.UpdateCountLines(ctx, r.actor, detail.ID, detail.Version, edits[from:min(from+countEditBatch, len(edits))]); err != nil {
			return fmt.Errorf("count %s lines: %w", detail.DocNo, err)
		}
	}
	if detail, err = inv.FinishCount(ctx, r.actor, detail.ID, detail.Version); err != nil {
		return fmt.Errorf("finish count %s: %w", detail.DocNo, err)
	}
	in := inventory.CountPostInput{Version: detail.Version}
	key, err := r.key("count-post", http.MethodPost, "/api/v1/stock-counts/"+detail.DocNo+"/post", in)
	if err != nil {
		return err
	}
	if _, err := inv.PostCount(ctx, r.actor, key, detail.ID, in); err != nil {
		return fmt.Errorf("post count %s: %w", detail.DocNo, err)
	}
	r.docs[DocCounts]++
	return nil
}
