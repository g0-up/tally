// Package inventory is the stock ledger of a store: lots, the append-only stock_moves ledger
// with its lot_balances and sku_stock projections, moving-average costing, lot picking (exact
// color, FEFO), reservations, stock adjustments (DC), stock counts (KK), the daily expiry roll,
// stock signals, backdate recost runs and the ledger verifier. Every other domain changes stock
// only through StockLedger.
package inventory

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// MoveType is stock_moves.move_type.
type MoveType string

// Move types of the ledger.
const (
	MoveReceipt            MoveType = "receipt"
	MovePurchaseReturn     MoveType = "purchase_return"
	MoveSale               MoveType = "sale"
	MoveSalesReturn        MoveType = "sales_return"
	MoveAdjustmentIn       MoveType = "adjustment_in"
	MoveAdjustmentOut      MoveType = "adjustment_out"
	MoveCountGain          MoveType = "count_gain"
	MoveCountLoss          MoveType = "count_loss"
	MoveAssemblyConsume    MoveType = "assembly_consume"
	MoveAssemblyProduce    MoveType = "assembly_produce"
	MoveDisassemblyConsume MoveType = "disassembly_consume"
	MoveDisassemblyReturn  MoveType = "disassembly_return"
	MoveQuarantineOut      MoveType = "quarantine_out"
	MoveQuarantineIn       MoveType = "quarantine_in"
	MoveOpening            MoveType = "opening"
	MoveCostAdjustment     MoveType = "cost_adjustment"
	MoveReversal           MoveType = "reversal"
)

// inbound reports whether the move type adds quantity.
func (t MoveType) inbound() bool {
	switch t {
	case MoveReceipt, MoveSalesReturn, MoveAdjustmentIn, MoveCountGain, MoveAssemblyProduce,
		MoveDisassemblyReturn, MoveQuarantineIn, MoveOpening:
		return true
	}
	return false
}

// outbound reports whether the move type removes quantity.
func (t MoveType) outbound() bool {
	switch t {
	case MovePurchaseReturn, MoveSale, MoveAdjustmentOut, MoveCountLoss, MoveAssemblyConsume,
		MoveDisassemblyConsume, MoveQuarantineOut:
		return true
	}
	return false
}

// Document types the inventory module writes on stock_moves.doc_type.
const (
	DocStockAdjustment = "stock_adjustment"
	DocStockCount      = "stock_count"
	DocCostRecalc      = "cost_recalculation"
)

// skuActive is skus.status of a SKU in use.
const skuActive = "active"

// Lot is a batch of one SKU in the store's warehouse: lot number, expiry and color. A
// quarantine lot is the twin of a normal lot holding the quantity that may not be sold.
type Lot struct {
	ID              uuid.UUID  `db:"id"`
	WarehouseID     uuid.UUID  `db:"warehouse_id"`
	SkuID           uuid.UUID  `db:"sku_id"`
	LotNo           string     `db:"lot_no"`
	ExpiryDate      *time.Time `db:"expiry_date"`
	ColorID         *uuid.UUID `db:"color_id"`
	IsQuarantine    bool       `db:"is_quarantine"`
	SourceDocType   *string    `db:"source_doc_type"`
	SourceDocLineID *uuid.UUID `db:"source_doc_line_id"`
	ReceivedOn      time.Time  `db:"received_on"`
	CreatedAt       time.Time  `db:"created_at"`
}

// Expired reports whether the lot is past its expiry on day (expiry day itself still sells).
func (l Lot) Expired(day time.Time) bool {
	return l.ExpiryDate != nil && l.ExpiryDate.Before(day)
}

// LotStock is a lot with its balance.
type LotStock struct {
	Lot
	Qty money.Qty `db:"qty"`
}

// StockMove is one row of the ledger. Qty is signed in the SKU's base unit; Value is the signed
// inventory value delta.
type StockMove struct {
	ID           uuid.UUID   `db:"id"`
	WarehouseID  uuid.UUID   `db:"warehouse_id"`
	SkuID        uuid.UUID   `db:"sku_id"`
	LotID        uuid.UUID   `db:"lot_id"`
	ColorID      *uuid.UUID  `db:"color_id"`
	Qty          money.Qty   `db:"qty"`
	UnitCost     money.Money `db:"unit_cost"`
	Value        money.Money `db:"value_amount"`
	MoveType     MoveType    `db:"move_type"`
	DocType      string      `db:"doc_type"`
	DocID        uuid.UUID   `db:"doc_id"`
	DocLineID    *uuid.UUID  `db:"doc_line_id"`
	BusinessDate time.Time   `db:"business_date"`
	PostedAt     time.Time   `db:"posted_at"`
	PostedBy     uuid.UUID   `db:"posted_by"`
	ReversalOf   *uuid.UUID  `db:"reversal_of"`
	CreatedAt    time.Time   `db:"created_at"`
}

// SkuStock is the per-SKU projection and lock point.
type SkuStock struct {
	WarehouseID   uuid.UUID   `db:"warehouse_id"`
	SkuID         uuid.UUID   `db:"sku_id"`
	OnHand        money.Qty   `db:"on_hand"`
	QuarantineQty money.Qty   `db:"quarantine_qty"`
	ExpiredQty    money.Qty   `db:"expired_qty"`
	Sellable      money.Qty   `db:"sellable"`
	Reserved      money.Qty   `db:"reserved"`
	AvgCost       money.Money `db:"avg_cost"`
	StockValue    money.Money `db:"stock_value"`
	LastMoveAt    *time.Time  `db:"last_move_at"`
	UpdatedAt     time.Time   `db:"updated_at"`
	Version       int         `db:"version"`
}

// Available is sellable minus reserved; it is negative when an expiry roll or a quarantine
// lowered sellable below existing reservations (a shortage the UI shows as a warning).
func (s SkuStock) Available() money.Qty { return s.Sellable.Sub(s.Reserved) }

// Reservation sources.
const (
	ReserveInvoiceLine       = "invoice_line"
	ReserveAssemblyOrderItem = "assembly_order_item"
	ReserveManual            = "manual"
)

// Reservation statuses.
const (
	ReservationActive   = "active"
	ReservationConsumed = "consumed"
	ReservationReleased = "released"
)

// Reservation holds sellable quantity of a SKU (not of a color) for a source.
type Reservation struct {
	ID          uuid.UUID  `db:"id"`
	WarehouseID uuid.UUID  `db:"warehouse_id"`
	SkuID       uuid.UUID  `db:"sku_id"`
	SourceType  string     `db:"source_type"`
	SourceID    uuid.UUID  `db:"source_id"`
	SourceDocNo *string    `db:"source_doc_no"`
	Qty         money.Qty  `db:"qty"`
	ConsumedQty money.Qty  `db:"consumed_qty"`
	ReleasedQty money.Qty  `db:"released_qty"`
	Status      string     `db:"status"`
	ExpiresAt   *time.Time `db:"expires_at"`
	Note        *string    `db:"note"`
	// ReleaseReason is why a manual hold was last released.
	ReleaseReason *string   `db:"release_reason"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
	Version       int       `db:"version"`
}

// Open is the quantity still held.
func (r Reservation) Open() money.Qty { return r.Qty.Sub(r.ConsumedQty).Sub(r.ReleasedQty) }

// Adjustment kinds (stock_adjustments.kind).
const (
	KindIncrease          = "increase"
	KindDecrease          = "decrease"
	KindDamage            = "damage"
	KindLoss              = "loss"
	KindGift              = "gift"
	KindInternalUse       = "internal_use"
	KindDisposal          = "disposal"
	KindQuarantineIn      = "quarantine_in"
	KindQuarantineRelease = "quarantine_release"
	KindOpening           = "opening"
	KindCount             = "count"
)

// Adjustment statuses.
const (
	AdjustmentPosted   = "posted"
	AdjustmentReversed = "reversed"
)

// StockAdjustment is a DC document.
type StockAdjustment struct {
	ID             uuid.UUID  `db:"id"`
	WarehouseID    uuid.UUID  `db:"warehouse_id"`
	DocNo          string     `db:"doc_no"`
	Kind           string     `db:"kind"`
	ReasonID       *uuid.UUID `db:"reason_id"`
	StockCountID   *uuid.UUID `db:"stock_count_id"`
	BusinessDate   time.Time  `db:"business_date"`
	Note           *string    `db:"note"`
	Status         string     `db:"status"`
	IdempotencyKey string     `db:"idempotency_key"`
	PostedAt       time.Time  `db:"posted_at"`
	PostedBy       uuid.UUID  `db:"posted_by"`
	ReversalOf     *uuid.UUID `db:"reversal_of"`
	CreatedAt      time.Time  `db:"created_at"`
}

// StockAdjustmentLine is one line of a DC with its catalog snapshot.
type StockAdjustmentLine struct {
	ID                uuid.UUID    `db:"id"`
	StockAdjustmentID uuid.UUID    `db:"stock_adjustment_id"`
	SkuID             uuid.UUID    `db:"sku_id"`
	SkuCodeSnap       string       `db:"sku_code_snap"`
	NameSnap          string       `db:"name_snap"`
	VariantLabelSnap  string       `db:"variant_label_snap"`
	UnitID            uuid.UUID    `db:"unit_id"`
	UnitNameSnap      string       `db:"unit_name_snap"`
	UnitFactorSnap    money.Factor `db:"unit_factor_snap"`
	Qty               money.Qty    `db:"qty"`
	BaseQty           money.Qty    `db:"base_qty"`
	ColorID           *uuid.UUID   `db:"color_id"`
	SpecText          *string      `db:"spec_text"`
	Direction         int          `db:"direction"`
	LotID             *uuid.UUID   `db:"lot_id"`
	LotNo             *string      `db:"lot_no"`
	ExpiryDate        *time.Time   `db:"expiry_date"`
	UnitCost          *money.Money `db:"unit_cost"`
}

// Stock count statuses.
const (
	CountCounting    = "counting"
	CountPendingPost = "pending_post"
	CountPosted      = "posted"
)

// Stock count scopes.
const (
	ScopeAll       = "all"
	ScopeCategory  = "category"
	ScopeSelection = "selection"
)

// StockCount is a KK document.
type StockCount struct {
	ID             uuid.UUID  `db:"id"`
	WarehouseID    uuid.UUID  `db:"warehouse_id"`
	DocNo          string     `db:"doc_no"`
	Scope          string     `db:"scope"`
	CategoryID     *uuid.UUID `db:"category_id"`
	Blind          bool       `db:"blind"`
	Status         string     `db:"status"`
	BusinessDate   time.Time  `db:"business_date"`
	StartedAt      time.Time  `db:"started_at"`
	CountedAt      *time.Time `db:"counted_at"`
	Note           *string    `db:"note"`
	IdempotencyKey *string    `db:"idempotency_key"`
	PostedAt       *time.Time `db:"posted_at"`
	PostedBy       *uuid.UUID `db:"posted_by"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	Version        int        `db:"version"`
}

// StockCountLine is one SKU × color (× lot when tracked) of a count.
type StockCountLine struct {
	ID                 uuid.UUID  `db:"id"`
	StockCountID       uuid.UUID  `db:"stock_count_id"`
	SkuID              uuid.UUID  `db:"sku_id"`
	ColorID            *uuid.UUID `db:"color_id"`
	LotID              *uuid.UUID `db:"lot_id"`
	SystemQty          money.Qty  `db:"system_qty"`
	CountedQty         *money.Qty `db:"counted_qty"`
	MovedSinceStartQty money.Qty  `db:"moved_since_start_qty"`
	VarianceQty        *money.Qty `db:"variance_qty"`
	ReasonID           *uuid.UUID `db:"reason_id"`
	// NewLotNo and NewExpiryDate describe the new lot a gain on a line without a lot opens, as
	// read on the shelf (SKUs tracking lots or expiry).
	NewLotNo      *string    `db:"new_lot_no"`
	NewExpiryDate *time.Time `db:"new_expiry_date"`
}

// Recost run statuses.
const (
	RecostPreviewed = "previewed"
	RecostConfirmed = "confirmed"
	RecostApplied   = "applied"
	RecostDiscarded = "discarded"
)

// skuInfo is what the ledger needs of a catalog SKU.
type skuInfo struct {
	ID           uuid.UUID  `db:"id"`
	Code         string     `db:"code"`
	DisplayName  string     `db:"display_name"`
	VariantLabel string     `db:"variant_label"`
	BaseUnitID   uuid.UUID  `db:"base_unit_id"`
	BaseUnitName string     `db:"base_unit_name"`
	TrackLot     bool       `db:"track_lot"`
	TrackExpiry  bool       `db:"track_expiry"`
	MinStock     money.Qty  `db:"min_stock"`
	MaxStock     *money.Qty `db:"max_stock"`
	QtyScale     int32      `db:"qty_scale"`
	Status       string     `db:"status"`
	SpecText     *string    `db:"spec_text"`
	CategoryID   *uuid.UUID `db:"category_id"`
	ProductType  string     `db:"product_type"`
}
