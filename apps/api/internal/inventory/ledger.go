package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Tx is what the ledger needs of the caller's transaction: posting.Tx satisfies it, and
// non-posting jobs (expiry roll, recost previews) use a buffered publisher.
type Tx interface {
	db.Querier
	Publish(evt outbox.Event)
}

// Posting names the document a ledger call books, its business date and who posted it.
type Posting struct {
	TenantID     uuid.UUID
	DocType      string
	DocID        uuid.UUID
	BusinessDate time.Time
	PostedAt     time.Time
	// PostedBy is the acting user, uuid.Nil for the system.
	PostedBy uuid.UUID
}

// PostingOf is the Posting of a document posted through the coordinator.
func PostingOf(tx posting.Tx, docType string, docID uuid.UUID) Posting {
	return Posting{TenantID: tx.TenantID(), DocType: docType, DocID: docID, BusinessDate: tx.BusinessDate(),
		PostedAt: tx.Now(), PostedBy: tx.Actor().ID}
}

func (p Posting) validate() error {
	if p.TenantID == uuid.Nil || p.DocType == "" || p.DocID == uuid.Nil || p.PostedAt.IsZero() || p.BusinessDate.IsZero() {
		return fmt.Errorf("inventory: incomplete posting %+v", p)
	}
	return nil
}

// ReceiveLine adds stock to a lot.
type ReceiveLine struct {
	DocLineID *uuid.UUID
	SkuID     uuid.UUID
	// Qty is in the SKU's base unit and positive.
	Qty      money.Qty
	UnitCost money.Money
	// AtAverageCost values the receipt at the SKU's current average cost (count gains, increases
	// without a cost) instead of UnitCost.
	AtAverageCost bool
	// MoveType is an inbound type other than quarantine_in; empty means receipt.
	MoveType MoveType
	// ColorID, LotNo and ExpiryDate identify the lot (created when new); LotNo is required for
	// SKUs tracking lots and ExpiryDate for SKUs tracking expiry.
	ColorID    *uuid.UUID
	LotNo      string
	ExpiryDate *time.Time
	// LotID puts the stock back into an existing lot instead (returns, disassembly).
	LotID *uuid.UUID
	// SourceMoveID values the receipt at the unit cost of an earlier move (returns).
	SourceMoveID *uuid.UUID
	// Value books the receipt at exactly this value (never negative) instead of qty × unit cost;
	// the unit cost recorded is value / qty. Assembly and disassembly carry the value consumed.
	Value *money.Money
}

// IssueLine takes stock out.
type IssueLine struct {
	DocLineID *uuid.UUID
	SkuID     uuid.UUID
	Qty       money.Qty
	// MoveType is an outbound type other than quarantine_out; empty means sale.
	MoveType MoveType
	// ColorID issues exactly that color; Uncolored issues the stock without a color. A SKU with
	// color stock needs one of the two (or LotID).
	ColorID   *uuid.UUID
	Uncolored bool
	LotID     *uuid.UUID
	// AllowExpired lets a sale take expired lots when the tenant does not block them; the caller
	// has consumed the expired_lot_sale confirmation.
	AllowExpired bool
}

// QuarantineLine moves stock between normal lots and their quarantine twins.
type QuarantineLine struct {
	DocLineID *uuid.UUID
	SkuID     uuid.UUID
	Qty       money.Qty
	ColorID   *uuid.UUID
	Uncolored bool
	LotID     *uuid.UUID
	// Release moves quarantined stock back to normal lots; false quarantines normal stock.
	Release bool
}

// CostLine changes the value of the stock without changing its quantity.
type CostLine struct {
	DocLineID *uuid.UUID
	SkuID     uuid.UUID
	// LotID carries the delta on that lot; nil uses the first lot holding stock.
	LotID *uuid.UUID
	// Value is the signed value delta.
	Value money.Money
}

// PostedMove is a move the ledger wrote, with the lot it touched.
type PostedMove struct {
	StockMove
	LotNo        string
	ExpiryDate   *time.Time
	IsQuarantine bool
}

// StockLedger is the only writer of stock: it appends stock_moves and keeps lot_balances and
// sku_stock in step inside the caller's transaction, after locking sku_stock rows in id order.
// Callers that lock rows of later kinds (reservations, obligations …) list the SKUs in their
// posting.LockSet so that the coordinator locks sku_stock first.
type StockLedger struct {
	costing CostingMethod
	lots    LotFactory
	signals StockSignalDetector
	recost  RecostPlanner
}

// NewStockLedger returns the ledger with moving-average costing.
func NewStockLedger() *StockLedger {
	return &StockLedger{costing: MovingAverageCosting{}}
}

// Receive books inbound lines.
func (l *StockLedger) Receive(ctx context.Context, tx Tx, p Posting, lines []ReceiveLine) ([]PostedMove, error) {
	skus, lots := make([]uuid.UUID, 0, len(lines)), []uuid.UUID{}
	for _, ln := range lines {
		skus = append(skus, ln.SkuID)
		if ln.LotID != nil {
			lots = append(lots, *ln.LotID)
		}
	}
	b, err := l.open(ctx, tx, p, skus, lots)
	if err != nil {
		return nil, err
	}
	for i, ln := range lines {
		if err := b.receive(ctx, i, ln); err != nil {
			return nil, err
		}
	}
	return b.finish(ctx)
}

// Issue books outbound lines. Lots are picked per line: the named lot, the exact color (FEFO
// within it) or FEFO across lots of a SKU without color stock. Stock never goes negative.
func (l *StockLedger) Issue(ctx context.Context, tx Tx, p Posting, lines []IssueLine) ([]PostedMove, error) {
	skus, lots := make([]uuid.UUID, 0, len(lines)), []uuid.UUID{}
	for _, ln := range lines {
		skus = append(skus, ln.SkuID)
		if ln.LotID != nil {
			lots = append(lots, *ln.LotID)
		}
	}
	b, err := l.open(ctx, tx, p, skus, lots)
	if err != nil {
		return nil, err
	}
	for i, ln := range lines {
		if err := b.issue(ctx, i, ln); err != nil {
			return nil, err
		}
	}
	return b.finish(ctx)
}

// Quarantine moves stock into or out of quarantine as paired quarantine_out / quarantine_in
// moves between a lot and its twin; on-hand quantity and value do not change.
func (l *StockLedger) Quarantine(ctx context.Context, tx Tx, p Posting, lines []QuarantineLine) ([]PostedMove, error) {
	skus, lots := make([]uuid.UUID, 0, len(lines)), []uuid.UUID{}
	for _, ln := range lines {
		skus = append(skus, ln.SkuID)
		if ln.LotID != nil {
			lots = append(lots, *ln.LotID)
		}
	}
	b, err := l.open(ctx, tx, p, skus, lots)
	if err != nil {
		return nil, err
	}
	for i, ln := range lines {
		if err := b.quarantine(ctx, i, ln); err != nil {
			return nil, err
		}
	}
	return b.finish(ctx)
}

// CostAdjust books cost-only moves (landed cost, final purchase price, recost).
func (l *StockLedger) CostAdjust(ctx context.Context, tx Tx, p Posting, lines []CostLine) ([]PostedMove, error) {
	skus, lots := make([]uuid.UUID, 0, len(lines)), []uuid.UUID{}
	for _, ln := range lines {
		skus = append(skus, ln.SkuID)
		if ln.LotID != nil {
			lots = append(lots, *ln.LotID)
		}
	}
	b, err := l.open(ctx, tx, p, skus, lots)
	if err != nil {
		return nil, err
	}
	for i, ln := range lines {
		if err := b.costAdjust(ctx, i, ln); err != nil {
			return nil, err
		}
	}
	return b.finish(ctx)
}

// Reverse mirrors every move of the document (docType, docID) under p, newest first, each
// pointing at its original through reversal_of. Inbound moves are taken back out of their lot
// (the lot must still hold the quantity); outbound moves are put back at their original unit
// cost. The legs into a quarantine twin are mirrored after the legs out of it, so the stock
// value of the SKU never dips between the two legs of a pair. A document is reversed at most
// once. A document without moves returns no moves.
func (l *StockLedger) Reverse(ctx context.Context, tx Tx, p Posting, docType string, docID uuid.UUID) ([]PostedMove, error) {
	orig, err := movesOfDoc(ctx, tx, docType, docID)
	if err != nil || len(orig) == 0 {
		return nil, err
	}
	moveIDs := make([]uuid.UUID, len(orig))
	skus, lots := make([]uuid.UUID, len(orig)), make([]uuid.UUID, len(orig))
	for i, m := range orig {
		moveIDs[i], skus[i], lots[i] = m.ID, m.SkuID, m.LotID
	}
	var reversed bool
	if err := tx.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM stock_moves WHERE reversal_of = ANY($1))`, moveIDs).
		Scan(&reversed); err != nil {
		return nil, fmt.Errorf("inventory: check reversals: %w", err)
	}
	if reversed {
		return nil, errAlreadyReversed()
	}
	b, err := l.open(ctx, tx, p, skus, lots)
	if err != nil {
		return nil, err
	}
	for _, intoTwin := range []bool{false, true} {
		for i := len(orig) - 1; i >= 0; i-- {
			if (orig[i].MoveType == MoveQuarantineIn) != intoTwin {
				continue
			}
			if err := b.mirror(ctx, orig[i]); err != nil {
				return nil, err
			}
		}
	}
	return b.finish(ctx)
}

// book is one ledger call in progress: the locked projections, the lots of the touched SKUs
// and the moves applied so far, all in memory until finish writes them.
type book struct {
	l        *StockLedger
	tx       Tx
	p        Posting
	today    time.Time
	settings tenantSettings
	infos    map[uuid.UUID]skuInfo
	stocks   map[uuid.UUID]*SkuStock
	// before holds the projections as they were locked, for the threshold crossings.
	before map[uuid.UUID]SkuStock
	lots   map[uuid.UUID]*LotStock
	// fefo holds every loaded lot in FEFO order.
	fefo    []*LotStock
	touched map[uuid.UUID]bool
	moves   []PostedMove
	colors  map[uuid.UUID]string
}

func (l *StockLedger) open(ctx context.Context, tx Tx, p Posting, skuIDs, lotIDs []uuid.UUID) (*book, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	ids := sortedUnique(skuIDs)
	if len(ids) == 0 {
		return nil, errInvalid(map[string]string{"lines": "required"})
	}
	infos, err := readSkuInfos(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		info, ok := infos[id]
		if !ok {
			return nil, errInvalid(map[string]string{"sku_id": "not_found"})
		}
		if info.ProductType == "combo" {
			return nil, errNotStockable(info.Code)
		}
	}
	locked, err := lockSkuStock(ctx, tx, ids, p.PostedAt)
	if err != nil {
		return nil, err
	}
	b := &book{l: l, tx: tx, p: p, infos: infos, stocks: make(map[uuid.UUID]*SkuStock, len(locked)), before: locked,
		lots: map[uuid.UUID]*LotStock{}, touched: map[uuid.UUID]bool{}, colors: map[uuid.UUID]string{}}
	for id := range locked {
		s := locked[id]
		b.stocks[id] = &s
	}
	if b.settings, err = readSettings(ctx, tx); err != nil {
		return nil, err
	}
	if b.today, err = tenantToday(ctx, tx, p.TenantID, p.PostedAt); err != nil {
		return nil, err
	}
	rows, err := readLotStocks(ctx, tx, ids, sortedUnique(lotIDs))
	if err != nil {
		return nil, err
	}
	for i := range rows {
		b.add(&rows[i])
	}
	return b, nil
}

// add registers a loaded lot.
func (b *book) add(l *LotStock) *LotStock {
	if have, ok := b.lots[l.ID]; ok {
		return have
	}
	b.lots[l.ID] = l
	i, _ := slices.BinarySearchFunc(b.fefo, l, fefoCompare)
	b.fefo = slices.Insert(b.fefo, i, l)
	return l
}

// lot returns a loaded lot, reading it when it was not loaded yet.
func (b *book) lot(ctx context.Context, id uuid.UUID) (*LotStock, error) {
	if l, ok := b.lots[id]; ok {
		return l, nil
	}
	l, err := readLot(ctx, b.tx, id)
	if err != nil {
		return nil, err
	}
	return b.add(&l), nil
}

// lotOf returns the lot with identity l, creating it when new.
func (b *book) lotOf(ctx context.Context, l Lot) (*LotStock, error) {
	if have, ok := b.lots[l.ID]; ok {
		return have, nil
	}
	return b.lot(ctx, l.ID)
}

// lotsOf returns the loaded lots of a SKU in FEFO order.
func (b *book) lotsOf(skuID uuid.UUID) []*LotStock {
	out := make([]*LotStock, 0, 4)
	for _, l := range b.fefo {
		if l.SkuID == skuID {
			out = append(out, l)
		}
	}
	return out
}

// hasColorStock reports whether a SKU holds stock with a color on the given side of quarantine.
func (b *book) hasColorStock(skuID uuid.UUID, quarantine bool) bool {
	for _, l := range b.lotsOf(skuID) {
		if l.IsQuarantine == quarantine && l.ColorID != nil && l.Qty.IsPositive() {
			return true
		}
	}
	return false
}

// colorCode returns the code of a color, checking that it exists.
func (b *book) colorCode(ctx context.Context, id *uuid.UUID) (*string, error) {
	if id == nil {
		return nil, nil
	}
	if code, ok := b.colors[*id]; ok {
		return &code, nil
	}
	codes, err := colorCodes(ctx, b.tx, []uuid.UUID{*id})
	if err != nil {
		return nil, err
	}
	code, ok := codes[*id]
	if !ok {
		return nil, nil
	}
	b.colors[*id] = code
	return &code, nil
}

func lineField(i int, name string) string { return fmt.Sprintf("lines.%d.%s", i, name) }

// apply books one move on the in-memory state.
func (b *book) apply(lot *LotStock, qty money.Qty, unitCost, value money.Money, mt MoveType, lineID, reversalOf *uuid.UUID) error {
	s := b.stocks[lot.SkuID]
	next := lot.Qty.Add(qty)
	if next.IsNegative() {
		return fmt.Errorf("inventory: lot %s would go negative", lot.ID)
	}
	if unitCost.IsNegative() {
		return fmt.Errorf("inventory: negative unit cost on lot %s", lot.ID)
	}
	lot.Qty = next
	s.OnHand = s.OnHand.Add(qty)
	s.StockValue = s.StockValue.Add(value)
	if s.OnHand.IsNegative() || s.StockValue.IsNegative() {
		return fmt.Errorf("inventory: stock of sku %s would go negative", lot.SkuID)
	}
	// Plain issues leave the average where it is; everything else re-prices the stock.
	if !qty.IsNegative() || mt == MoveReversal {
		avg, err := b.l.costing.Average(*s)
		if err != nil {
			return err
		}
		s.AvgCost = avg
	}
	b.touched[lot.ID] = true
	b.moves = append(b.moves, PostedMove{
		StockMove: StockMove{ID: ids.New(), WarehouseID: lot.WarehouseID, SkuID: lot.SkuID, LotID: lot.ID, ColorID: lot.ColorID,
			Qty: qty, UnitCost: unitCost, Value: value, MoveType: mt, DocType: b.p.DocType, DocID: b.p.DocID, DocLineID: lineID,
			BusinessDate: b.p.BusinessDate, PostedAt: b.p.PostedAt, PostedBy: b.p.PostedBy, ReversalOf: reversalOf,
			CreatedAt: b.p.PostedAt},
		LotNo: lot.LotNo, ExpiryDate: lot.ExpiryDate, IsQuarantine: lot.IsQuarantine,
	})
	return nil
}

func (b *book) receive(ctx context.Context, i int, ln ReceiveLine) error {
	mt := ln.MoveType
	if mt == "" {
		mt = MoveReceipt
	}
	if !mt.inbound() || mt == MoveQuarantineIn {
		return fmt.Errorf("inventory: %s is not a receipt move type", mt)
	}
	if !ln.Qty.IsPositive() {
		return errInvalid(map[string]string{lineField(i, "qty"): "must_be_positive"})
	}
	info, s := b.infos[ln.SkuID], b.stocks[ln.SkuID]
	var lot *LotStock
	if ln.LotID != nil {
		l, err := b.lot(ctx, *ln.LotID)
		if err != nil {
			return err
		}
		if l.SkuID != ln.SkuID || l.IsQuarantine || (ln.ColorID != nil && !sameColor(l.ColorID, ln.ColorID)) {
			return errLotMismatch(lineField(i, "lot_id"))
		}
		lot = l
	} else {
		fields := map[string]string{}
		lotNo := strings.TrimSpace(ln.LotNo)
		// A kit lot always carries the assembly's lot number and the expiry of its earliest
		// component, whether or not the kit SKU tracks lots.
		produced := mt == MoveAssemblyProduce
		switch {
		case len(lotNo) > 64:
			fields[lineField(i, "lot_no")] = "too_long"
		case produced:
		case info.TrackLot && lotNo == "":
			fields[lineField(i, "lot_no")] = "required"
		case !info.TrackLot && lotNo != "":
			fields[lineField(i, "lot_no")] = "not_tracked"
		}
		switch {
		case produced:
		case info.TrackExpiry && ln.ExpiryDate == nil:
			fields[lineField(i, "expiry_date")] = "required"
		case !info.TrackExpiry && ln.ExpiryDate != nil:
			fields[lineField(i, "expiry_date")] = "not_tracked"
		}
		code, err := b.colorCode(ctx, ln.ColorID)
		if err != nil {
			return err
		}
		if ln.ColorID != nil && code == nil {
			fields[lineField(i, "color_id")] = "not_found"
		}
		if len(fields) > 0 {
			return errInvalid(fields)
		}
		docType := b.p.DocType
		l, err := b.l.lots.FindOrCreate(ctx, b.tx, lotKey{WarehouseID: s.WarehouseID, SkuID: ln.SkuID, LotNo: lotNo,
			ExpiryDate: ln.ExpiryDate, ColorID: ln.ColorID}, b.p.BusinessDate, b.p.PostedAt, &docType, ln.DocLineID)
		if err != nil {
			return err
		}
		if lot, err = b.lotOf(ctx, l); err != nil {
			return err
		}
	}
	unitCost := ln.UnitCost
	switch {
	case ln.SourceMoveID != nil:
		src, err := readMove(ctx, b.tx, *ln.SourceMoveID)
		if err != nil {
			return err
		}
		if src.SkuID != ln.SkuID {
			return errInvalid(map[string]string{lineField(i, "source_move_id"): "other_sku"})
		}
		unitCost = src.UnitCost
	case ln.AtAverageCost:
		unitCost = s.AvgCost
	}
	if ln.Value != nil {
		if ln.Value.IsNegative() {
			return errInvalid(map[string]string{lineField(i, "value"): "negative"})
		}
		unitCost, err := moneyOf(ln.Value.Decimal().DivRound(ln.Qty.Decimal(), money.MoneyScale))
		if err != nil {
			return err
		}
		return b.apply(lot, ln.Qty, unitCost, *ln.Value, mt, ln.DocLineID, nil)
	}
	value, err := b.l.costing.OnReceive(*s, ln.Qty, unitCost)
	if err != nil {
		return err
	}
	return b.apply(lot, ln.Qty, unitCost, value, mt, ln.DocLineID, nil)
}

// take is a planned quantity out of one lot.
type take struct {
	lot *LotStock
	qty money.Qty
}

// plan picks lots for qty of one SKU: from the named lot, the exact color, or FEFO when the SKU
// holds no color stock on that side of quarantine. eligible filters the automatic picks.
func (b *book) plan(ctx context.Context, skuID uuid.UUID, qty money.Qty, colorID *uuid.UUID, uncolored bool,
	explicit *LotStock, quarantine bool, eligible func(*LotStock) bool) ([]take, error) {
	info := b.infos[skuID]
	lots := b.lotsOf(skuID)
	var picker LotPicker
	switch {
	case explicit != nil:
		picker = ExplicitLot{LotID: explicit.ID}
	case colorID != nil:
		picker = ExactColorLot{ColorID: colorID}
	case uncolored:
		picker = ExactColorLot{}
	case b.hasColorStock(skuID, quarantine):
		return nil, errColorRequired(info.Code)
	default:
		picker = FefoLot{}
	}
	remaining := qty
	var out []take
	for _, l := range picker.Candidates(lots) {
		if !remaining.IsPositive() {
			break
		}
		if !l.Qty.IsPositive() || (explicit == nil && !eligible(l)) {
			continue
		}
		n := minQty(remaining, l.Qty)
		out = append(out, take{lot: l, qty: n})
		remaining = remaining.Sub(n)
	}
	if !remaining.IsPositive() {
		return out, nil
	}
	// Shortage: list what each color could give under the same rules, the requested color first.
	byColor := map[uuid.UUID]money.Qty{}
	var none money.Qty
	var order []*uuid.UUID
	seen := map[uuid.UUID]bool{}
	noneSeen := false
	for _, l := range lots {
		if !l.Qty.IsPositive() || !eligible(l) {
			continue
		}
		if l.ColorID == nil {
			none, noneSeen = none.Add(l.Qty), true
			continue
		}
		if !seen[*l.ColorID] {
			seen[*l.ColorID] = true
			order = append(order, l.ColorID)
		}
		byColor[*l.ColorID] = byColor[*l.ColorID].Add(l.Qty)
	}
	avail := make([]ColorAvailability, 0, len(order)+2)
	requested, err := b.colorCode(ctx, colorID)
	if err != nil {
		return nil, err
	}
	if colorID != nil && !seen[*colorID] {
		avail = append(avail, ColorAvailability{ColorID: colorID, ColorCode: requested, Qty: money.ZeroQty})
	}
	if uncolored && !noneSeen {
		avail = append(avail, ColorAvailability{Qty: money.ZeroQty})
	}
	for _, id := range order {
		code, err := b.colorCode(ctx, id)
		if err != nil {
			return nil, err
		}
		avail = append(avail, ColorAvailability{ColorID: id, ColorCode: code, Qty: byColor[*id]})
	}
	if noneSeen {
		avail = append(avail, ColorAvailability{Qty: none})
	}
	if explicit != nil {
		avail = []ColorAvailability{{ColorID: explicit.ColorID, ColorCode: requested, Qty: explicit.Qty}}
	}
	reqColor := requested
	if uncolored && colorID == nil {
		empty := ""
		reqColor = &empty
	}
	return nil, errInsufficientStock(info.Code, reqColor, qty, avail)
}

func (b *book) issue(ctx context.Context, i int, ln IssueLine) error {
	mt := ln.MoveType
	if mt == "" {
		mt = MoveSale
	}
	if !mt.outbound() || mt == MoveQuarantineOut {
		return fmt.Errorf("inventory: %s is not an issue move type", mt)
	}
	if !ln.Qty.IsPositive() {
		return errInvalid(map[string]string{lineField(i, "qty"): "must_be_positive"})
	}
	saleLike := mt == MoveSale || mt == MoveAssemblyConsume
	var explicit *LotStock
	if ln.LotID != nil {
		l, err := b.lot(ctx, *ln.LotID)
		if err != nil {
			return err
		}
		if l.SkuID != ln.SkuID || (ln.ColorID != nil && !sameColor(l.ColorID, ln.ColorID)) {
			return errLotMismatch(lineField(i, "lot_id"))
		}
		if saleLike && l.IsQuarantine {
			return errLotQuarantined(l.LotNo)
		}
		if saleLike && l.Expired(b.today) {
			if b.settings.BlockExpiredLotSale {
				return errExpiredLot(l.LotNo)
			}
			if !ln.AllowExpired {
				return apperr.NewExceptionRequired(string(exception.ExpiredLotSale), "Lot "+l.LotNo+" is expired; selling it needs the owner's confirmation")
			}
		}
		explicit = l
	}
	if ln.ColorID != nil {
		code, err := b.colorCode(ctx, ln.ColorID)
		if err != nil {
			return err
		}
		if code == nil {
			return errInvalid(map[string]string{lineField(i, "color_id"): "not_found"})
		}
	}
	eligible := func(l *LotStock) bool {
		if l.IsQuarantine {
			return false
		}
		if saleLike && l.Expired(b.today) {
			return !b.settings.BlockExpiredLotSale && ln.AllowExpired
		}
		return true
	}
	takes, err := b.plan(ctx, ln.SkuID, ln.Qty, ln.ColorID, ln.Uncolored, explicit, false, eligible)
	if err != nil {
		return err
	}
	for _, t := range takes {
		unitCost, value, err := b.l.costing.OnIssue(*b.stocks[ln.SkuID], t.qty)
		if err != nil {
			return err
		}
		if err := b.apply(t.lot, t.qty.Neg(), unitCost, value.Neg(), mt, ln.DocLineID, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *book) quarantine(ctx context.Context, i int, ln QuarantineLine) error {
	if !ln.Qty.IsPositive() {
		return errInvalid(map[string]string{lineField(i, "qty"): "must_be_positive"})
	}
	var explicit *LotStock
	if ln.LotID != nil {
		l, err := b.lot(ctx, *ln.LotID)
		if err != nil {
			return err
		}
		if l.SkuID != ln.SkuID || l.IsQuarantine != ln.Release || (ln.ColorID != nil && !sameColor(l.ColorID, ln.ColorID)) {
			return errLotMismatch(lineField(i, "lot_id"))
		}
		explicit = l
	}
	eligible := func(l *LotStock) bool { return l.IsQuarantine == ln.Release }
	takes, err := b.plan(ctx, ln.SkuID, ln.Qty, ln.ColorID, ln.Uncolored, explicit, ln.Release, eligible)
	if err != nil {
		return err
	}
	for _, t := range takes {
		twin, err := b.l.lots.Twin(ctx, b.tx, t.lot.Lot, b.p.PostedAt)
		if err != nil {
			return err
		}
		to, err := b.lotOf(ctx, twin)
		if err != nil {
			return err
		}
		unitCost, value, err := b.l.costing.OnIssue(*b.stocks[ln.SkuID], t.qty)
		if err != nil {
			return err
		}
		if err := b.apply(t.lot, t.qty.Neg(), unitCost, value.Neg(), MoveQuarantineOut, ln.DocLineID, nil); err != nil {
			return err
		}
		if err := b.apply(to, t.qty, unitCost, value, MoveQuarantineIn, ln.DocLineID, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *book) costAdjust(ctx context.Context, i int, ln CostLine) error {
	if ln.Value.IsZero() {
		return nil
	}
	s := b.stocks[ln.SkuID]
	if err := b.l.costing.OnCostAdjust(*s, ln.Value); err != nil {
		return err
	}
	var lot *LotStock
	if ln.LotID != nil {
		l, err := b.lot(ctx, *ln.LotID)
		if err != nil {
			return err
		}
		if l.SkuID != ln.SkuID {
			return errLotMismatch(lineField(i, "lot_id"))
		}
		lot = l
	} else {
		for _, l := range b.lotsOf(ln.SkuID) {
			if l.Qty.IsPositive() && (lot == nil || (lot.IsQuarantine && !l.IsQuarantine)) {
				lot = l
			}
		}
	}
	if lot == nil {
		return apperr.NewConflict("no_stock_to_adjust", "There is no stock left to carry a cost adjustment")
	}
	return b.apply(lot, money.ZeroQty, money.ZeroMoney, ln.Value, MoveCostAdjustment, ln.DocLineID, nil)
}

func (b *book) mirror(ctx context.Context, m StockMove) error {
	lot, err := b.lot(ctx, m.LotID)
	if err != nil {
		return err
	}
	s := b.stocks[m.SkuID]
	id := m.ID
	if m.MoveType == MoveCostAdjustment {
		delta := m.Value.Neg()
		if err := b.l.costing.OnCostAdjust(*s, delta); err != nil {
			return err
		}
		return b.apply(lot, money.ZeroQty, money.ZeroMoney, delta, MoveCostAdjustment, m.DocLineID, &id)
	}
	qty := m.Qty.Neg()
	value := m.Value.Neg()
	if qty.IsNegative() {
		if lot.Qty.Cmp(m.Qty) < 0 {
			code, err := b.colorCode(ctx, lot.ColorID)
			if err != nil {
				return err
			}
			return errInsufficientStock(b.infos[m.SkuID].Code, code, m.Qty,
				[]ColorAvailability{{ColorID: lot.ColorID, ColorCode: code, Qty: lot.Qty}})
		}
		// Taking a receipt back out never leaves value behind an empty stock, nor takes more
		// value than there is. A quarantine leg moves stock between lots of the same SKU at its
		// pooled value, so its mirror takes back exactly what it moved: the value of the SKU,
		// cost adjusted since included, stays with its stock.
		if m.MoveType != MoveQuarantineIn && (m.Qty.Cmp(s.OnHand) >= 0 || m.Value.Cmp(s.StockValue) > 0) {
			value = s.StockValue.Neg()
		}
	}
	return b.apply(lot, qty, m.UnitCost, value, MoveReversal, m.DocLineID, &id)
}

// finish writes the moves, the lot balances and the recomputed sku_stock rows, then publishes
// stock.changed, the stock signals and, for backdated cost changes, a recost suggestion.
func (b *book) finish(ctx context.Context) ([]PostedMove, error) {
	moves := make([]StockMove, len(b.moves))
	for i, m := range b.moves {
		moves[i] = m.StockMove
	}
	if err := insertMoves(ctx, b.tx, b.p.TenantID, moves); err != nil {
		return nil, err
	}
	lotIDs := make([]uuid.UUID, 0, len(b.touched))
	for id := range b.touched {
		lotIDs = append(lotIDs, id)
	}
	for _, id := range sortedUnique(lotIDs) {
		if err := upsertLotBalance(ctx, b.tx, *b.lots[id], b.p.PostedAt); err != nil {
			return nil, err
		}
	}
	skuIDs := make([]uuid.UUID, 0, len(b.stocks))
	for id := range b.stocks {
		skuIDs = append(skuIDs, id)
	}
	skuIDs = sortedUnique(skuIDs)
	changed := make([]SkuStock, 0, len(skuIDs))
	for _, id := range skuIDs {
		s := b.stocks[id]
		lots := make([]LotStock, 0, 4)
		for _, l := range b.lotsOf(id) {
			lots = append(lots, *l)
		}
		if err := recompute(s, lots, b.today); err != nil {
			return nil, err
		}
		at := b.p.PostedAt
		s.LastMoveAt, s.UpdatedAt = &at, at
		if err := writeSkuStock(ctx, b.tx, *s); err != nil {
			return nil, err
		}
		s.Version++
		changed = append(changed, *s)
		b.tx.Publish(outbox.Event{Topic: TopicStockChanged, Payload: map[string]any{
			"sku_id": id, "on_hand": s.OnHand, "sellable": s.Sellable, "reserved": s.Reserved, "available": s.Available(),
			"doc_type": b.p.DocType, "doc_id": b.p.DocID,
		}})
	}
	if err := b.l.signals.Check(ctx, b.tx, b.p.PostedAt, b.today, b.before, changed, b.infos); err != nil {
		return nil, err
	}
	if err := b.l.recost.suggest(ctx, b.tx, b.p, skuIDs); err != nil {
		return nil, err
	}
	return b.moves, nil
}

// recompute derives the quantities of s from the balances of all its lots holding stock:
// quarantine is the quarantine lots, expired the normal lots past expiry on today, sellable the
// rest. The ledger total must equal the lot total.
func recompute(s *SkuStock, lots []LotStock, today time.Time) error {
	var total, quarantine, expired money.Qty
	for _, l := range lots {
		if l.SkuID != s.SkuID || !l.Qty.IsPositive() {
			continue
		}
		total = total.Add(l.Qty)
		switch {
		case l.IsQuarantine:
			quarantine = quarantine.Add(l.Qty)
		case l.Expired(today):
			expired = expired.Add(l.Qty)
		}
	}
	if !total.Equal(s.OnHand) {
		return errors.New("inventory: on-hand quantity of sku " + s.SkuID.String() + " differs from its lots")
	}
	s.QuarantineQty, s.ExpiredQty = quarantine, expired
	s.Sellable = total.Sub(quarantine).Sub(expired)
	return nil
}
