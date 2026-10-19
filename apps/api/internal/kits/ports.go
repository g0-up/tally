package kits

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Stock move types kits books (inventory stock_moves.move_type).
const (
	MoveAssemblyConsume    = "assembly_consume"
	MoveAssemblyProduce    = "assembly_produce"
	MoveDisassemblyConsume = "disassembly_consume"
	MoveDisassemblyReturn  = "disassembly_return"
)

// Document types of the stock moves kits books.
const (
	DocAssemblyPosting = "assembly_posting"
	DocDisassembly     = "disassembly"
)

// StockDoc is the document a stock call books.
type StockDoc struct {
	Type string
	ID   uuid.UUID
}

// IssueLine takes component or kit stock out: from LotID when given, else from ColorID exactly,
// else first expiry first (a SKU holding color stock then needs a color).
type IssueLine struct {
	DocLineID *uuid.UUID
	SkuID     uuid.UUID
	Qty       money.Qty
	ColorID   *uuid.UUID
	LotID     *uuid.UUID
}

// ReceiveLine books stock in at exactly Value: into LotID when given (a disassembly returning a
// component to its lot), else into a new lot LotNo with ExpiryDate (a kit lot).
type ReceiveLine struct {
	DocLineID  *uuid.UUID
	SkuID      uuid.UUID
	Qty        money.Qty
	ColorID    *uuid.UUID
	LotNo      string
	ExpiryDate *time.Time
	LotID      *uuid.UUID
	Value      money.Money
}

// QuarantineLine moves Qty of a lot to its quarantine twin.
type QuarantineLine struct {
	DocLineID *uuid.UUID
	SkuID     uuid.UUID
	Qty       money.Qty
	ColorID   *uuid.UUID
	LotID     uuid.UUID
}

// Move is a stock move the ledger wrote; Qty and Value are signed.
type Move struct {
	ID         uuid.UUID
	SkuID      uuid.UUID
	LotID      uuid.UUID
	ColorID    *uuid.UUID
	DocLineID  *uuid.UUID
	Qty        money.Qty
	Value      money.Money
	LotNo      string
	Expiry     *time.Time
	Quarantine bool
}

// Stock is the inventory ledger, written inside the kits posting transaction.
type Stock interface {
	Issue(ctx context.Context, tx posting.Tx, doc StockDoc, moveType string, lines []IssueLine) ([]Move, error)
	Receive(ctx context.Context, tx posting.Tx, doc StockDoc, moveType string, lines []ReceiveLine) ([]Move, error)
	Quarantine(ctx context.Context, tx posting.Tx, doc StockDoc, lines []QuarantineLine) ([]Move, error)
}

// Reservations holds component stock for assembly order items (inventory reservations of source
// assembly_order_item), inside the posting transaction.
type Reservations interface {
	// Reserve holds qty of skuID for the order item itemID.
	Reserve(ctx context.Context, tx posting.Tx, itemID, skuID uuid.UUID, qty money.Qty, docNo string) error
	// Consume turns qty of the reservation into an issue.
	Consume(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty money.Qty) error
	// Release frees qty of the reservation; nil frees all it holds.
	Release(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty *money.Qty) error
	// Adjust sets the reservation's total (consumed and released included), reopening it.
	Adjust(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, total money.Qty) error
}

// PriceQuery asks the retail price of qty of a SKU in a unit.
type PriceQuery struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  uuid.UUID
	Qty     money.Qty
}

// Prices resolves retail unit prices for a walk-in customer today; nil when a SKU has none.
type Prices interface {
	RetailPrices(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []PriceQuery) ([]*money.Money, error)
}

// DraftPrices keeps the retail price of a draft kit out of the append-only price history: the
// price is readable at once (the preview shows it), its history starts when the kit is published,
// and a draft deleted before then leaves no history behind.
type DraftPrices interface {
	// SetDraftRetailPrice sets the base retail price per base unit without history.
	SetDraftRetailPrice(ctx context.Context, q db.Querier, actor audit.Actor, skuID uuid.UUID, price money.Money) error
	// SetDraftWholesalePrice sets the base wholesale price per base unit without history.
	SetDraftWholesalePrice(ctx context.Context, q db.Querier, actor audit.Actor, skuID uuid.UUID, price money.Money) error
	// RecordDraftPrices writes the first history row of each price of the SKU that has none.
	RecordDraftPrices(ctx context.Context, q db.Querier, actor audit.Actor, skuID uuid.UUID) error
	// DeleteDraftPrices removes the prices of a SKU without history.
	DeleteDraftPrices(ctx context.Context, q db.Querier, skuID uuid.UUID) error
}

// NewProduct is the catalog product of a new kit or combo with its single SKU; its retail price
// is set through DraftPrices.
type NewProduct struct {
	Type        string
	Name        string
	Description *string
	CategoryID  *uuid.UUID
	SkuCode     string
	BaseUnitID  uuid.UUID
	TaxRateID   *uuid.UUID
	Barcode     *string
	// TrackLot keeps the kit's own lots (pre-packed kits, whose lots carry the earliest
	// component expiry).
	TrackLot bool
	// MinStock is the stock of pre-packed kits below which the kit is restocked.
	MinStock *money.Qty
}

// ProductChange edits a kit product; nil fields are kept and DescriptionSet replaces the
// description (nil clears it).
type ProductChange struct {
	Name           *string
	Description    *string
	DescriptionSet bool
	Status         *string
}

// Catalog writes the kit's catalog product in the caller's transaction.
type Catalog interface {
	CreateProduct(ctx context.Context, q db.Querier, actor audit.Actor, in NewProduct) (productID, skuID uuid.UUID, err error)
	UpdateProduct(ctx context.Context, q db.Querier, actor audit.Actor, productID uuid.UUID, in ProductChange) error
	DeleteProduct(ctx context.Context, q db.Querier, actor audit.Actor, productID uuid.UUID) error
}
