package kits

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// DamagedInput names Qty (base units) of a component found damaged while taking kits apart: from
// SourceLotID when given, else from the component lots of SkuID in the kit lot, in lot order.
type DamagedInput struct {
	SkuID       uuid.UUID
	SourceLotID *uuid.UUID
	Qty         money.Qty
}

// DisassemblyInput takes Qty kits of the kit lot KitLotID of an order apart.
type DisassemblyInput struct {
	KitLotID     uuid.UUID
	Qty          money.Qty
	ReasonID     uuid.UUID
	BusinessDate *time.Time
	Damaged      []DamagedInput
}

func validateDisassembly(in DisassemblyInput) map[string]string {
	fields := map[string]string{}
	if in.KitLotID == uuid.Nil {
		fields["kit_lot_id"] = "required"
	}
	if !in.Qty.IsPositive() || !in.Qty.IsWhole() {
		fields["qty"] = "must_be_positive_whole"
	}
	if in.ReasonID == uuid.Nil {
		fields["reason_id"] = "required"
	}
	if len(in.Damaged) > MaxItems {
		fields["damaged"] = "too_many"
	}
	for i, d := range in.Damaged {
		if d.SkuID == uuid.Nil {
			fields[fmt.Sprintf("damaged.%d.sku_id", i)] = "required"
		}
		if !d.Qty.IsPositive() {
			fields[fmt.Sprintf("damaged.%d.qty", i)] = "must_be_positive"
		}
	}
	return fields
}

// childLot is one component lot of a kit lot: what the batch put into its kits and what of it a
// disassembly gives back.
type childLot struct {
	LotID    uuid.UUID
	SkuID    uuid.UUID
	ColorID  *uuid.UUID
	Total    money.Qty
	Returned money.Qty
	// Cost is what Total cost when the batch took it from the lot.
	Cost money.Money
	// Items are the posting items of the lot with what each put into the kits.
	Items []PostingItem
	Share money.Qty
	// Damaged is the part of Share that goes to quarantine.
	Damaged money.Qty
}

// childLots groups what a batch put into its kits by component lot, in the batch's item order.
func childLots(p AssemblyPosting) []childLot {
	at := map[uuid.UUID]int{}
	var out []childLot
	for _, it := range p.Items {
		used := it.ConsumedQty.Sub(it.ScrapQty)
		if !used.IsPositive() {
			continue
		}
		i, ok := at[it.LotID]
		if !ok {
			i = len(out)
			at[it.LotID] = i
			out = append(out, childLot{LotID: it.LotID, SkuID: it.SkuID, ColorID: it.ColorID, Total: money.ZeroQty,
				Returned: money.ZeroQty, Cost: money.ZeroMoney, Share: money.ZeroQty, Damaged: money.ZeroQty})
		}
		out[i].Total = out[i].Total.Add(used)
		out[i].Cost = out[i].Cost.Add(it.UnitCost.MulQty(used))
		out[i].Items = append(out[i].Items, it)
	}
	return out
}

// shareOut sets what taking qty more kits apart, after taken of a lot of produced kits, gives back
// of each component lot. Shares are cumulative: the lot's part of taken+qty kits, cut to the
// quantity scale, less what it already gave back. Cutting never adds up across disassemblies, the
// last kits of the lot give back all of it, and kits sold from the lot keep their own part.
func shareOut(children []childLot, qty, taken, produced money.Qty) error {
	for i := range children {
		c := &children[i]
		target, err := share(c.Total, taken.Add(qty), produced)
		if err != nil {
			return err
		}
		s := target.Sub(c.Returned)
		if s.IsNegative() {
			s = money.ZeroQty
		}
		c.Share = minQty(s, c.Total.Sub(c.Returned))
	}
	return nil
}

// allotDamaged puts each damaged quantity on the component lots it names, never beyond what a
// lot gives back.
func allotDamaged(children []childLot, damaged []DamagedInput) error {
	fields := map[string]string{}
	for i, d := range damaged {
		f := func(name string) string { return fmt.Sprintf("damaged.%d.%s", i, name) }
		rest := d.Qty
		component := false
		for j := range children {
			c := &children[j]
			if c.SkuID != d.SkuID || (d.SourceLotID != nil && c.LotID != *d.SourceLotID) {
				continue
			}
			component = true
			take := minQty(c.Share.Sub(c.Damaged), rest)
			if take.IsPositive() {
				c.Damaged = c.Damaged.Add(take)
				rest = rest.Sub(take)
			}
		}
		switch {
		case !component && d.SourceLotID != nil:
			fields[f("source_lot_id")] = "not_a_component"
		case !component:
			fields[f("sku_id")] = "not_a_component"
		case rest.IsPositive():
			fields[f("qty")] = CodeExceedsRemaining
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// PostDisassembly takes kits of a kit lot apart (TG): the kits leave the kit lot, each component
// goes back to the exact lot (and so color) it was taken from at the cost it went in at, the
// packing cost of the kits is expensed as a COGS adjustment, and damaged parts go on to the
// quarantine twin of their lot. What the order's items consumed is
// taken back so the kits can be assembled again.
func (s *Service) PostDisassembly(ctx context.Context, actor audit.Actor, key idempotency.Key, orderID uuid.UUID, in DisassemblyInput) (posting.Response, error) {
	if fields := validateDisassembly(in); len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	date, err := s.postingDate(ctx, actor.TenantID, in.BusinessDate)
	if err != nil {
		return posting.Response{}, err
	}
	idemKey := ScopeDisassemble + ":" + key.Value
	return s.postOnOrder(ctx, actor, key, ScopeDisassemble, orderID, date, docseq.PrefixKitDisassembly, true, nil,
		func(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem) (posting.Result, error) {
			return s.disassemble(ctx, tx, o, items, in, idemKey)
		})
}

func (s *Service) disassemble(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem, in DisassemblyInput, idemKey string) (posting.Result, error) {
	p, found, err := postingOfLot(ctx, tx, o.ID, in.KitLotID)
	if err != nil {
		return posting.Result{}, err
	}
	if !found {
		return posting.Result{}, errInvalid(map[string]string{"kit_lot_id": "not_of_order"})
	}
	children := childLots(p)
	taken, returned, err := disassembledOf(ctx, tx, in.KitLotID)
	if err != nil {
		return posting.Result{}, err
	}
	left := p.ProducedQty.Sub(taken)
	if !left.IsPositive() {
		return posting.Result{}, apperr.NewConflict(CodeKitLotExhausted, "Every kit of the lot was already taken apart")
	}
	if in.Qty.Cmp(left) > 0 {
		return posting.Result{}, errInvalid(map[string]string{"qty": CodeExceedsRemaining})
	}
	if err := checkReason(ctx, tx, in.ReasonID); err != nil {
		return posting.Result{}, err
	}
	sellable, reserved, err := kitStock(ctx, tx, o.KitSkuID)
	if err != nil {
		return posting.Result{}, err
	}
	for i := range children {
		children[i].Returned = returned[children[i].LotID]
	}
	if err := shareOut(children, in.Qty, taken, p.ProducedQty); err != nil {
		return posting.Result{}, err
	}
	if err := allotDamaged(children, in.Damaged); err != nil {
		return posting.Result{}, err
	}

	d := Disassembly{ID: ids.New(), WarehouseID: o.WarehouseID, DocNo: tx.DocNo(), OrderID: o.ID, KitLotID: in.KitLotID, Qty: in.Qty,
		ReasonID: in.ReasonID, BusinessDate: tx.BusinessDate(), PostedAt: tx.Now(), PostedBy: tx.Actor().ID, CreatedAt: tx.Now()}
	if err := s.moveDisassembly(ctx, tx, o, &d, children); err != nil {
		return posting.Result{}, err
	}
	if err := checkKitsFree(ctx, tx, o.KitSkuID, in.Qty, sellable, reserved); err != nil {
		return posting.Result{}, err
	}
	if err := insertDisassembly(ctx, tx, d, idemKey); err != nil {
		return posting.Result{}, err
	}
	if err := takeBackConsumed(ctx, tx, items, children); err != nil {
		return posting.Result{}, err
	}

	next := o
	next.DisassembledQty = o.DisassembledQty.Add(in.Qty)
	if o.Status != OrderClosed {
		if next.Status, err = s.status.Move(o.Status, s.status.AfterProgress(o.Status, next.Net(), o.PlannedQty, true)); err != nil {
			return posting.Result{}, err
		}
	}
	if err := saveOrderProgress(ctx, tx, next, tx.Now()); err != nil {
		return posting.Result{}, err
	}
	out, err := readDisassembly(ctx, tx, d.ID)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(audit.Event{Action: "disassembly.create", EntityType: "disassembly", EntityID: d.ID, After: out})
	return posting.Result{Status: http.StatusCreated, Body: disassemblyDTO(out)}, nil
}

// moveDisassembly books the stock of a disassembly and fills its lines: the kits leave the kit
// lot, each component lot gets its share back at the cost it went into the kits at, and the
// damaged part moves on to quarantine.
func (s *Service) moveDisassembly(ctx context.Context, tx posting.Tx, o AssemblyOrder, d *Disassembly, children []childLot) error {
	doc := StockDoc{Type: DocDisassembly, ID: d.ID}
	kitLot := d.KitLotID
	kitMoves, err := s.stock.Issue(ctx, tx, doc, MoveDisassemblyConsume, []IssueLine{{SkuID: o.KitSkuID, Qty: d.Qty, LotID: &kitLot}})
	if err != nil {
		return remapLines(err, []int{-1}, "kit_lot_id")
	}
	value := money.ZeroMoney
	for _, m := range kitMoves {
		value = value.Add(m.Value.Abs())
	}
	var back []childLot
	for _, c := range children {
		if c.Share.IsPositive() {
			back = append(back, c)
		}
	}
	if len(back) == 0 {
		return apperr.NewConflict(CodeKitLotExhausted, "Every component of the kit lot was already given back")
	}
	values, expense, err := splitDisassemblyValue(value, back)
	if err != nil {
		return err
	}
	if expense.IsPositive() {
		if err := insertDisassemblyCogs(ctx, tx, o.KitSkuID, *d, expense); err != nil {
			return err
		}
	}
	receive := make([]ReceiveLine, len(back))
	var quarantine []QuarantineLine
	d.Lines = make([]DisassemblyLine, len(back))
	for i, c := range back {
		lineID, lotID := ids.New(), c.LotID
		unitCost, err := money.RoundMoney(values[i].Decimal().Div(c.Share.Decimal()))
		if err != nil {
			return err
		}
		d.Lines[i] = DisassemblyLine{ID: lineID, SkuID: c.SkuID, SourceLotID: c.LotID, ColorID: c.ColorID,
			ReturnQty: c.Share.Sub(c.Damaged), DamagedQty: c.Damaged, UnitCost: unitCost}
		receive[i] = ReceiveLine{DocLineID: &lineID, SkuID: c.SkuID, Qty: c.Share, ColorID: c.ColorID, LotID: &lotID, Value: values[i]}
		if c.Damaged.IsPositive() {
			quarantine = append(quarantine, QuarantineLine{DocLineID: &lineID, SkuID: c.SkuID, Qty: c.Damaged, ColorID: c.ColorID,
				LotID: c.LotID})
		}
	}
	if _, err := s.stock.Receive(ctx, tx, doc, MoveDisassemblyReturn, receive); err != nil {
		return err
	}
	if len(quarantine) > 0 {
		if _, err := s.stock.Quarantine(ctx, tx, doc, quarantine); err != nil {
			return err
		}
	}
	return nil
}

// splitDisassemblyValue splits value, what the kits left the kit lot at, over the component lots
// that give a share back: each share returns at the cost it went into the kits at (its part of
// the lot's Cost), and the rest of value is expensed: the packing cost of the kits, their part of
// the batch's scrap and any drift of the kit SKU's moving average from the batch's cost. When the
// kits left at less than their components cost, value is split over the shares by that cost and
// nothing is expensed, so the stock never gains value.
func splitDisassemblyValue(value money.Money, back []childLot) ([]money.Money, money.Money, error) {
	values := make([]money.Money, len(back))
	components := money.ZeroMoney
	for i, c := range back {
		v, err := money.RoundMoney(c.Cost.Decimal().Mul(c.Share.Decimal()).Div(c.Total.Decimal()))
		if err != nil {
			return nil, money.Money{}, err
		}
		values[i] = v
		components = components.Add(v)
	}
	expense := value.Sub(components)
	if !expense.IsNegative() {
		return values, expense, nil
	}
	values, err := money.Allocate(value, values)
	if err != nil {
		return nil, money.Money{}, fmt.Errorf("kits: split disassembly value: %w", err)
	}
	return values, money.ZeroMoney, nil
}

// CogsSourceDisassembly is the source_doc_type of the COGS adjustment a disassembly writes.
const CogsSourceDisassembly = "disassembly"

// insertDisassemblyCogs writes what a disassembly expensed as a COGS adjustment of the kit SKU.
func insertDisassemblyCogs(ctx context.Context, tx posting.Tx, kitSkuID uuid.UUID, d Disassembly, amount money.Money) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cogs_adjustments (id, tenant_id, sku_id, amount, source_doc_type, source_doc_id, business_date, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ids.New(), tx.TenantID(), kitSkuID, amount, CogsSourceDisassembly, d.ID, d.BusinessDate, tx.Now()); err != nil {
		return fmt.Errorf("kits: insert disassembly cogs adjustment: %w", err)
	}
	return nil
}

// takeBackConsumed lowers what the order's items consumed by what each component lot gave back,
// split over the items that took from the lot by what each put into the kits.
func takeBackConsumed(ctx context.Context, q db.Querier, items []OrderItem, children []childLot) error {
	consumed := make(map[uuid.UUID]money.Qty, len(items))
	for _, it := range items {
		consumed[it.ID] = it.ConsumedBaseQty
	}
	for _, c := range children {
		if !c.Share.IsPositive() {
			continue
		}
		weights := make([]money.Qty, len(c.Items))
		for i, it := range c.Items {
			weights[i] = it.ConsumedQty.Sub(it.ScrapQty)
		}
		parts, err := proportional(c.Share, weights)
		if err != nil {
			return err
		}
		for i, it := range c.Items {
			back := minQty(parts[i], consumed[it.OrderItemID])
			if !back.IsPositive() {
				continue
			}
			consumed[it.OrderItemID] = consumed[it.OrderItemID].Sub(back)
			if err := addItemConsumed(ctx, q, it.OrderItemID, back.Neg()); err != nil {
				return err
			}
		}
	}
	return nil
}

// disassembledOf reads how many kits of a kit lot were taken apart and what each component lot
// gave back so far.
func disassembledOf(ctx context.Context, q db.Querier, kitLotID uuid.UUID) (money.Qty, map[uuid.UUID]money.Qty, error) {
	var taken money.Qty
	if err := q.QueryRowxContext(ctx, `SELECT COALESCE(sum(qty), 0) FROM disassemblies WHERE kit_lot_id = $1 AND reversal_of IS NULL`,
		kitLotID).Scan(&taken); err != nil {
		return money.Qty{}, nil, fmt.Errorf("kits: read disassembled kits: %w", err)
	}
	var rows []struct {
		LotID uuid.UUID `db:"source_lot_id"`
		Qty   money.Qty `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT dl.source_lot_id, sum(dl.return_base_qty + dl.damaged_base_qty) AS qty
		FROM disassembly_lines dl JOIN disassemblies d ON d.id = dl.disassembly_id
		WHERE d.kit_lot_id = $1 AND d.reversal_of IS NULL
		GROUP BY dl.source_lot_id`, kitLotID); err != nil {
		return money.Qty{}, nil, fmt.Errorf("kits: read returned components: %w", err)
	}
	out := make(map[uuid.UUID]money.Qty, len(rows))
	for _, r := range rows {
		out[r.LotID] = r.Qty
	}
	return taken, out, nil
}

// kitStock reads the sellable stock of a kit SKU and what reservations hold of it. The caller
// holds the SKU's sku_stock lock, which every reservation change takes first.
func kitStock(ctx context.Context, q db.Querier, skuID uuid.UUID) (sellable, reserved money.Qty, _ error) {
	if err := q.QueryRowxContext(ctx, `SELECT COALESCE(sum(sellable), 0), COALESCE(sum(reserved), 0) FROM sku_stock WHERE sku_id = $1`,
		skuID).Scan(&sellable, &reserved); err != nil {
		return money.Qty{}, money.Qty{}, fmt.Errorf("kits: read kit stock: %w", err)
	}
	return sellable, reserved, nil
}

// checkKitsFree refuses a disassembly that took kits an invoice or a manual hold reserved: the
// sellable stock of the kit SKU may only drop by the kits no one holds. Expired or quarantined
// kits are not sellable, so taking them apart never touches a hold.
func checkKitsFree(ctx context.Context, q db.Querier, skuID uuid.UUID, qty, sellableBefore, reserved money.Qty) error {
	after, _, err := kitStock(ctx, q, skuID)
	if err != nil {
		return err
	}
	free := sellableBefore.Sub(reserved)
	if free.IsNegative() {
		free = money.ZeroQty
	}
	if sellableBefore.Sub(after).Cmp(free) <= 0 {
		return nil
	}
	return errInsufficientStock(ctx, q, skuID, qty, free)
}

// disassemblyReasonKinds are the reason kinds that explain taking kits apart: kits found damaged,
// cleared after the season, used in the store or written off.
var disassemblyReasonKinds = []string{"damage", "disposal", "internal_use", "write_off"}

// checkReason checks the reason of a disassembly exists, is active and is of a disassembly kind.
func checkReason(ctx context.Context, q db.Querier, id uuid.UUID) error {
	var r struct {
		Kind   string `db:"kind"`
		Active bool   `db:"active"`
	}
	err := q.QueryRowxContext(ctx, `SELECT kind, active FROM reasons WHERE id = $1`, id).StructScan(&r)
	if db.IsNoRows(err) {
		return errInvalid(map[string]string{"reason_id": "not_found"})
	}
	if err != nil {
		return fmt.Errorf("kits: read reason: %w", err)
	}
	switch {
	case !slices.Contains(disassemblyReasonKinds, r.Kind):
		return errInvalid(map[string]string{"reason_id": "wrong_kind"})
	case !r.Active:
		return errInvalid(map[string]string{"reason_id": "inactive"})
	}
	return nil
}

func insertDisassembly(ctx context.Context, q posting.Tx, d Disassembly, idemKey string) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO disassemblies (id, tenant_id, warehouse_id, doc_no, assembly_order_id, kit_lot_id, qty, reason_id, business_date,
		  idempotency_key, posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		d.ID, q.TenantID(), d.WarehouseID, d.DocNo, d.OrderID, d.KitLotID, d.Qty, d.ReasonID, d.BusinessDate, idemKey, d.PostedAt,
		d.PostedBy, d.CreatedAt); err != nil {
		return fmt.Errorf("kits: insert disassembly: %w", err)
	}
	for _, l := range d.Lines {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO disassembly_lines (id, tenant_id, disassembly_id, sku_id, source_lot_id, color_id, return_base_qty,
			  damaged_base_qty, unit_cost)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			l.ID, q.TenantID(), d.ID, l.SkuID, l.SourceLotID, l.ColorID, l.ReturnQty, l.DamagedQty, l.UnitCost); err != nil {
			return fmt.Errorf("kits: insert disassembly line: %w", err)
		}
	}
	return nil
}

// Disassemblies lists disassemblies newest first.
func (s *Service) Disassemblies(ctx context.Context, scope Scope, f DisassemblyFilter) ([]Disassembly, error) {
	var out []Disassembly
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = listDisassemblies(ctx, q, f)
		return err
	})
	return out, err
}
