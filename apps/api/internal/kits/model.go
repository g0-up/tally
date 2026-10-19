// Package kits implements gift kits and combos: versioned recipes with goods, packaging, basket,
// accessory and mix pack components; the draft → publish lifecycle; assembly orders (LD) whose
// balanced postings consume component lots and produce kit lots with their genealogy;
// disassembly (TG) back to the exact component lots; and the combo expander sales uses to sell a
// combo as its components.
package kits

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Product types a recipe belongs to: a pre-packed kit has its own stock, a combo is expanded into
// its components at sale.
const (
	TypeKit   = "kit"
	TypeCombo = "combo"
)

// Product statuses (catalog products.status).
const (
	ProductDraft    = "draft"
	ProductActive   = "active"
	ProductInactive = "inactive"
)

// Recipe statuses. One version of a product is active at a time; publishing a draft supersedes it.
const (
	RecipeDraft      = "draft"
	RecipeActive     = "active"
	RecipeSuperseded = "superseded"
)

// Component kinds of a recipe item.
const (
	KindGoods     = "goods"
	KindPackaging = "packaging"
	KindBasket    = "basket"
	KindAccessory = "accessory"
	KindMixPack   = "mix_pack"
)

func validKind(k string) bool {
	switch k {
	case KindGoods, KindPackaging, KindBasket, KindAccessory, KindMixPack:
		return true
	}
	return false
}

// Recipe is one version of the composition of a kit or combo.
type Recipe struct {
	ID          uuid.UUID   `db:"id"`
	ProductID   uuid.UUID   `db:"product_id"`
	VersionNo   int         `db:"version_no"`
	Status      string      `db:"status"`
	ValidFrom   *time.Time  `db:"valid_from"`
	ValidTo     *time.Time  `db:"valid_to"`
	PackingCost money.Money `db:"packing_cost"`
	OutputSkuID uuid.UUID   `db:"output_sku_id"`
	Note        *string     `db:"note"`
	CreatedAt   time.Time   `db:"created_at"`
	UpdatedAt   time.Time   `db:"updated_at"`
	Version     int         `db:"version"`
}

// ValidOn reports whether the recipe's validity window holds day.
func (r Recipe) ValidOn(day time.Time) bool {
	if r.ValidFrom != nil && day.Before(*r.ValidFrom) {
		return false
	}
	return r.ValidTo == nil || !day.After(*r.ValidTo)
}

// RecipeItem is one component of a recipe. A mix pack names its mix group and holds its flavors
// in MixItems; every other kind names a SKU.
type RecipeItem struct {
	ID         uuid.UUID    `db:"id"`
	RecipeID   uuid.UUID    `db:"kit_recipe_id"`
	LineNo     int          `db:"line_no"`
	Kind       string       `db:"component_kind"`
	SkuID      *uuid.UUID   `db:"sku_id"`
	UnitID     uuid.UUID    `db:"unit_id"`
	Qty        money.Qty    `db:"qty"`
	UnitFactor money.Factor `db:"unit_factor_snap"`
	BaseQty    money.Qty    `db:"base_qty"`
	// ColorID is the fixed color; nil lets the counter (or the assembler) choose.
	ColorID    *uuid.UUID `db:"color_id"`
	MixGroupID *uuid.UUID `db:"mix_group_id"`
	MixItems   []MixItem  `db:"-"`
}

// MixItem is one flavor of a mix pack component: Parts parts of the mix group's part unit.
type MixItem struct {
	ItemID uuid.UUID `db:"kit_recipe_item_id"`
	SkuID  uuid.UUID `db:"sku_id"`
	Parts  money.Qty `db:"parts_qty"`
}

// Component is one stocked SKU a recipe needs per kit: a SKU item, or one flavor of a mix pack.
type Component struct {
	ItemID  uuid.UUID
	LineNo  int
	Kind    string
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	// BaseQty is the quantity per kit in the SKU's base unit.
	BaseQty money.Qty
}

// Kit is a kit or combo product with its SKU and latest recipe version.
type Kit struct {
	ProductID   uuid.UUID `db:"product_id"`
	Type        string    `db:"type"`
	Name        string    `db:"name"`
	Description *string   `db:"description"`
	Status      string    `db:"status"`
	SkuID       uuid.UUID `db:"sku_id"`
	SkuCode     string    `db:"sku_code"`
	BaseUnitID  uuid.UUID `db:"base_unit_id"`
	// ActiveVersion is the version_no of the active recipe, nil before the first publish.
	ActiveVersion *int `db:"active_version"`
	// Sold is set once the SKU is on a sales document or an assembly order.
	Sold bool `db:"sold"`
	// Latest is the newest recipe version; its version column is the kit's ETag.
	Latest Recipe       `db:"-"`
	Items  []RecipeItem `db:"-"`
}

// KitRow is a kit list entry.
type KitRow struct {
	ProductID     uuid.UUID `db:"product_id"`
	Type          string    `db:"type"`
	Name          string    `db:"name"`
	Status        string    `db:"status"`
	SkuID         uuid.UUID `db:"sku_id"`
	SkuCode       string    `db:"sku_code"`
	LatestVersion int       `db:"latest_version"`
	LatestStatus  string    `db:"latest_status"`
	ActiveVersion *int      `db:"active_version"`
	UpdatedAt     time.Time `db:"updated_at"`
	Version       int       `db:"version"`
}

// KitFilter selects kits.
type KitFilter struct {
	Type   string
	Status string
	Q      string
	// AfterName/AfterID continue a name-ordered page.
	AfterName *string
	AfterID   uuid.UUID
	Limit     int
}

// ComponentCost is one row of the component table of a recipe (KIT-01): sale price, amount at
// sale price, cost and the quantity that can still be sold.
type ComponentCost struct {
	ItemID     uuid.UUID
	LineNo     int
	Kind       string
	SkuID      uuid.UUID
	SkuCode    string
	Name       string
	UnitID     uuid.UUID
	Qty        money.Qty
	BaseQty    money.Qty
	ColorID    *uuid.UUID
	MixGroupID *uuid.UUID
	// UnitPrice is the retail price per base unit, nil when the SKU has none.
	UnitPrice  *money.Money
	SaleAmount money.Money
	// UnitCost is the moving average cost per base unit (the reference cost before any stock).
	UnitCost money.Money
	Cost     money.Money
	// Sellable is the SKU's stock not held by reservations.
	Sellable money.Qty
	// Buildable is how many kits this component's sellable stock covers.
	Buildable money.Qty
}

// CostView is the economics of a recipe: its component table and totals.
type CostView struct {
	Components     []ComponentCost
	ComponentsSale money.Money
	ComponentsCost money.Money
	PackingCost    money.Money
	TotalCost      money.Money
	// KitPrice is the kit's own retail price, nil when it has none.
	KitPrice *money.Money
	// Margin is KitPrice − TotalCost, nil without a kit price.
	Margin *money.Money
	// Buildable is how many kits the sellable stock of every component covers.
	Buildable money.Qty
}

// AssemblyOrder is an LD: kits planned from a recipe version, its components and progress.
type AssemblyOrder struct {
	ID              uuid.UUID  `db:"id"`
	WarehouseID     uuid.UUID  `db:"warehouse_id"`
	DocNo           string     `db:"doc_no"`
	KitRecipeID     uuid.UUID  `db:"kit_recipe_id"`
	RecipeVersion   int        `db:"recipe_version_snap"`
	PlannedQty      money.Qty  `db:"planned_qty"`
	CompletedQty    money.Qty  `db:"completed_qty"`
	DisassembledQty money.Qty  `db:"disassembled_qty"`
	Status          string     `db:"status"`
	PlannedDate     *time.Time `db:"planned_date"`
	CampaignID      *uuid.UUID `db:"campaign_id"`
	InvoiceID       *uuid.UUID `db:"invoice_id"`
	Note            *string    `db:"note"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	Version         int        `db:"version"`
	// Kit fields joined from the recipe.
	ProductID uuid.UUID `db:"product_id"`
	KitName   string    `db:"kit_name"`
	KitSkuID  uuid.UUID `db:"kit_sku_id"`
}

// Net is the kits of the order still assembled: completed less disassembled.
func (o AssemblyOrder) Net() money.Qty { return o.CompletedQty.Sub(o.DisassembledQty) }

// OrderItem is a planned component of an order; a substitute names the item it replaces.
type OrderItem struct {
	ID              uuid.UUID  `db:"id"`
	OrderID         uuid.UUID  `db:"assembly_order_id"`
	RecipeItemID    *uuid.UUID `db:"kit_recipe_item_id"`
	SubstitutesID   *uuid.UUID `db:"substitutes_item_id"`
	SkuID           uuid.UUID  `db:"sku_id"`
	ColorID         *uuid.UUID `db:"color_id"`
	PlannedBaseQty  money.Qty  `db:"planned_base_qty"`
	ConsumedBaseQty money.Qty  `db:"consumed_base_qty"`
	SkuCode         string     `db:"sku_code"`
	SkuName         string     `db:"sku_name"`
	// Reserved is the quantity its reservation still holds.
	Reserved money.Qty `db:"reserved"`
	// ReservationID is the item's reservation, nil when none was made.
	ReservationID *uuid.UUID `db:"reservation_id"`
	// ReservationQty is the reservation's total, consumed and released included.
	ReservationQty money.Qty `db:"reservation_qty"`
}

// Remaining is what the item still needs: planned less consumed, never negative.
func (i OrderItem) Remaining() money.Qty {
	r := i.PlannedBaseQty.Sub(i.ConsumedBaseQty)
	if r.IsNegative() {
		return money.ZeroQty
	}
	return r
}

// OrderDetail is an order with its items and its expected cost: the planned components at
// today's cost plus the recipe's packing cost, recomputed as components are substituted.
type OrderDetail struct {
	AssemblyOrder
	Items          []OrderItem
	Postings       []AssemblyPosting
	ComponentsCost money.Money
	PackingCost    money.Money
	ExpectedCost   money.Money
}

// OrderFilter selects assembly orders.
type OrderFilter struct {
	Status    string
	ProductID *uuid.UUID
	Q         string
	AfterAt   *time.Time
	AfterID   uuid.UUID
	Limit     int
}

// AssemblyPosting is one completed batch of an order.
type AssemblyPosting struct {
	ID             uuid.UUID   `db:"id"`
	OrderID        uuid.UUID   `db:"assembly_order_id"`
	ProducedQty    money.Qty   `db:"produced_qty"`
	OutputLotID    uuid.UUID   `db:"output_lot_id"`
	OutputLotNo    string      `db:"output_lot_no"`
	OutputExpiry   *time.Time  `db:"output_expiry"`
	ComponentsCost money.Money `db:"components_cost"`
	PackingCost    money.Money `db:"packing_cost"`
	OutputUnitCost money.Money `db:"output_unit_cost"`
	BusinessDate   time.Time   `db:"business_date"`
	PostedAt       time.Time   `db:"posted_at"`
	PostedBy       uuid.UUID   `db:"posted_by"`
	Items          []PostingItem
}

// PostingItem is what a batch consumed of one component lot; scrap is part of the consumption.
type PostingItem struct {
	ID          uuid.UUID   `db:"id"`
	OrderItemID uuid.UUID   `db:"assembly_order_item_id"`
	SkuID       uuid.UUID   `db:"sku_id"`
	LotID       uuid.UUID   `db:"lot_id"`
	LotNo       string      `db:"lot_no"`
	ColorID     *uuid.UUID  `db:"color_id"`
	ConsumedQty money.Qty   `db:"consumed_base_qty"`
	ScrapQty    money.Qty   `db:"scrap_base_qty"`
	UnitCost    money.Money `db:"unit_cost"`
}

// Disassembly is a TG: kits of one kit lot taken apart back into their component lots.
type Disassembly struct {
	ID           uuid.UUID  `db:"id"`
	WarehouseID  uuid.UUID  `db:"warehouse_id"`
	DocNo        string     `db:"doc_no"`
	OrderID      uuid.UUID  `db:"assembly_order_id"`
	OrderDocNo   string     `db:"order_doc_no"`
	KitLotID     uuid.UUID  `db:"kit_lot_id"`
	KitLotNo     string     `db:"kit_lot_no"`
	Qty          money.Qty  `db:"qty"`
	ReasonID     uuid.UUID  `db:"reason_id"`
	BusinessDate time.Time  `db:"business_date"`
	PostedAt     time.Time  `db:"posted_at"`
	PostedBy     uuid.UUID  `db:"posted_by"`
	ReversalOf   *uuid.UUID `db:"reversal_of"`
	CreatedAt    time.Time  `db:"created_at"`
	Lines        []DisassemblyLine
}

// DisassemblyLine is what a disassembly gave back to one component lot; the damaged part went on
// to the lot's quarantine twin.
type DisassemblyLine struct {
	ID          uuid.UUID   `db:"id"`
	SkuID       uuid.UUID   `db:"sku_id"`
	SourceLotID uuid.UUID   `db:"source_lot_id"`
	LotNo       string      `db:"lot_no"`
	ColorID     *uuid.UUID  `db:"color_id"`
	ReturnQty   money.Qty   `db:"return_base_qty"`
	DamagedQty  money.Qty   `db:"damaged_base_qty"`
	UnitCost    money.Money `db:"unit_cost"`
}

// DisassemblyFilter selects disassemblies. Q matches the TG number, the order number, the kit lot
// number or the kit name.
type DisassemblyFilter struct {
	OrderID *uuid.UUID
	Q       string
	From    *time.Time
	To      *time.Time
	AfterAt *time.Time
	AfterID uuid.UUID
	Limit   int
}

// GenealogyLink is one component lot that went into a kit lot.
type GenealogyLink struct {
	ParentLotID uuid.UUID  `db:"parent_lot_id"`
	ParentLotNo string     `db:"parent_lot_no"`
	ChildLotID  uuid.UUID  `db:"child_lot_id"`
	ChildLotNo  string     `db:"child_lot_no"`
	SkuID       uuid.UUID  `db:"sku_id"`
	SkuCode     string     `db:"sku_code"`
	ColorID     *uuid.UUID `db:"color_id"`
	Expiry      *time.Time `db:"expiry_date"`
	Qty         money.Qty  `db:"qty"`
	PostingID   uuid.UUID  `db:"assembly_posting_id"`
	OrderDocNo  string     `db:"order_doc_no"`
}

// Genealogy traces a lot both ways: the component lots of a kit lot, and the kit lots a component
// lot went into.
type Genealogy struct {
	LotID      uuid.UUID
	Components []GenealogyLink
	UsedIn     []GenealogyLink
}

// Progress is the KIT-07 progress of one kit: planned, completed and disassembled kits on its
// open orders and what open invoices still ask for.
type Progress struct {
	ProductID       uuid.UUID `db:"product_id"`
	Name            string    `db:"name"`
	SkuID           uuid.UUID `db:"sku_id"`
	SkuCode         string    `db:"sku_code"`
	PlannedQty      money.Qty `db:"planned_qty"`
	CompletedQty    money.Qty `db:"completed_qty"`
	DisassembledQty money.Qty `db:"disassembled_qty"`
	OnHand          money.Qty `db:"on_hand"`
	InvoiceDemand   money.Qty `db:"invoice_demand"`
	OpenOrders      int       `db:"open_orders"`
}

// Label is the KIT-07 label payload of an assembly order: the kit, its LD number and the kit lots
// with their component lots and colors.
type Label struct {
	KitName string
	SkuCode string
	DocNo   string
	Lots    []LabelLot
}

// LabelLot is one kit lot on a label.
type LabelLot struct {
	LotID      uuid.UUID
	LotNo      string
	Expiry     *time.Time
	Qty        money.Qty
	Components []LabelComponent
}

// LabelComponent is one component lot of a kit lot.
type LabelComponent struct {
	SkuID     uuid.UUID
	SkuCode   string
	Name      string
	LotNo     string
	ColorID   *uuid.UUID
	ColorName *string
	Expiry    *time.Time
	// QtyPerKit is the component quantity in one kit of the lot.
	QtyPerKit money.Qty
}
