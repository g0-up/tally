package catalog

import (
	"context"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// UsageCount is how many records of one kind use a catalog object, with the Vietnamese label of
// that kind ("hóa đơn", "báo giá" …).
type UsageCount struct {
	Label string
	Count int
}

// UsageReader answers the edit-rule questions about catalog objects used elsewhere (documents,
// stock, prices). Until the inventory, sales and procurement modules provide their own readers,
// SQLUsageReader answers them with read-only queries on their tables.
type UsageReader interface {
	// AttributeInUse reports whether a product stores a value for the attribute or a SKU uses it
	// as a variant axis.
	AttributeInUse(ctx context.Context, q db.Querier, attributeID uuid.UUID, code string) (bool, error)
	// OptionInUse reports whether a product value or a SKU variant references the option.
	OptionInUse(ctx context.Context, q db.Querier, attributeCode string, optionID uuid.UUID) (bool, error)
	// SkusHaveStockMoves reports whether any of the SKUs has a stock movement.
	SkusHaveStockMoves(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (bool, error)
	// SkuInUse reports whether a document, price, stock record or kit references the SKU.
	SkuInUse(ctx context.Context, q db.Querier, skuID uuid.UUID) (bool, error)
	// SkuUnitInUse reports whether a document line or price uses the SKU in that unit.
	SkuUnitInUse(ctx context.Context, q db.Querier, skuID, unitID uuid.UUID) (bool, error)
	// ProductInUse reports whether a kit recipe uses the product or any of its SKUs is in use.
	ProductInUse(ctx context.Context, q db.Querier, productID uuid.UUID) (bool, error)
	// SkuOnHand is the total on-hand quantity of the SKU over every warehouse.
	SkuOnHand(ctx context.Context, q db.Querier, skuID uuid.UUID) (money.Qty, error)
	// MixGroupUsage counts the documents that use the mix group, per document kind.
	MixGroupUsage(ctx context.Context, q db.Querier, mixGroupID uuid.UUID) ([]UsageCount, error)
	// MixGroupUsedSkus lists the flavor SKUs of the mix group that documents already used.
	MixGroupUsedSkus(ctx context.Context, q db.Querier, mixGroupID uuid.UUID) ([]uuid.UUID, error)
	// CategoryInUse reports whether a product, a sub-category or a stock count references the category.
	CategoryInUse(ctx context.Context, q db.Querier, categoryID uuid.UUID) (bool, error)
}

// ColorQty is the on-hand quantity of one color of a SKU; ColorID nil is "Chưa gán màu".
type ColorQty struct {
	ColorID *uuid.UUID
	Qty     money.Qty
}

// StockByColorReader returns on-hand quantities per color for SKUs. It is implemented by the
// inventory module; while it is not wired, search returns no per-color quantities and edit rules
// read the stock projection through UsageReader.SkuOnHand.
type StockByColorReader interface {
	OnHandByColor(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID][]ColorQty, error)
}

// PriceHintReader returns the base retail price of SKUs (one base unit, general color) for the
// search pickers. It is implemented by the pricing module; while it is not wired, search returns
// no price hints.
type PriceHintReader interface {
	PriceHints(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]money.Money, error)
}

// Sale price kinds a PriceWriter accepts.
const (
	PriceRetail    = "retail"
	PriceWholesale = "wholesale"
)

// SalePrice is a base sale price set from the catalog (quick-create, product form).
type SalePrice struct {
	SkuID  uuid.UUID
	UnitID uuid.UUID
	// Kind is PriceRetail or PriceWholesale.
	Kind  string
	Price money.Money
	// Source names the screen that set the price, e.g. "quick_create".
	Source string
}

// PriceWriter writes base sale prices. It is implemented by the pricing module; while it is not
// wired, sale prices given to the catalog are not stored and responses say so.
type PriceWriter interface {
	SetSalePrice(ctx context.Context, q db.Querier, actor audit.Actor, p SalePrice) error
}
