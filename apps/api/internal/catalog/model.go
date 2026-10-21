// Package catalog owns the product catalog of a tenant: lookups (colors, units, tax rates),
// dynamic attributes and attribute sets, categories, products and their SKUs (variants, units,
// barcodes, images), mix groups and the shared search behind every product, customer and
// supplier picker.
package catalog

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Statuses of attributes, SKUs and mix groups.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	// StatusDraft is a product not offered for sale yet.
	StatusDraft = "draft"
)

// Product types.
const (
	ProductGoods    = "goods"
	ProductMaterial = "material"
	ProductKit      = "kit"
	ProductCombo    = "combo"
)

// Color is one of the seven fixed colors stock and document lines carry (not an attribute).
type Color struct {
	ID        uuid.UUID `db:"id"`
	Code      string    `db:"code"`
	Name      string    `db:"name"`
	Hex       string    `db:"hex"`
	SortOrder int       `db:"sort_order"`
	IsSystem  bool      `db:"is_system"`
	Active    bool      `db:"active"`
}

// Unit is a unit of measure (cái, hộp, thùng …).
type Unit struct {
	ID        uuid.UUID `db:"id"`
	Name      string    `db:"name"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
}

// TaxRate is a VAT rate lines snapshot.
type TaxRate struct {
	ID          uuid.UUID     `db:"id"`
	Name        string        `db:"name"`
	RatePercent money.Percent `db:"rate_percent"`
	Active      bool          `db:"active"`
	CreatedAt   time.Time     `db:"created_at"`
}

// Attribute is a dynamic product attribute; its code is the key of products.attributes.
type Attribute struct {
	ID           uuid.UUID `db:"id"`
	Code         string    `db:"code"`
	Name         string    `db:"name"`
	DataType     DataType  `db:"data_type"`
	UnitLabel    *string   `db:"unit_label"`
	IsFilterable bool      `db:"is_filterable"`
	IsSearchable bool      `db:"is_searchable"`
	Status       string    `db:"status"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
	Version      int       `db:"version"`
	Options      []AttributeOption
}

// AttributeOption is one choice of a select or multiselect attribute.
type AttributeOption struct {
	ID          uuid.UUID `db:"id"`
	AttributeID uuid.UUID `db:"attribute_id"`
	Label       string    `db:"label"`
	SortOrder   int       `db:"sort_order"`
	Active      bool      `db:"active"`
}

// AttributeSet is a "mẫu ngành": the ordered attributes a product of that kind fills in.
type AttributeSet struct {
	ID        uuid.UUID `db:"id"`
	Name      string    `db:"name"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
	Items     []AttributeSetItem
}

// AttributeSetItem places an attribute in a set.
type AttributeSetItem struct {
	AttributeID   uuid.UUID `db:"attribute_id"`
	AttributeCode string    `db:"code"`
	AttributeName string    `db:"name"`
	IsRequired    bool      `db:"is_required"`
	SortOrder     int       `db:"sort_order"`
}

// Category is a node of the category tree (at most MaxCategoryDepth levels).
type Category struct {
	ID             uuid.UUID  `db:"id"`
	ParentID       *uuid.UUID `db:"parent_id"`
	Name           string     `db:"name"`
	AttributeSetID *uuid.UUID `db:"attribute_set_id"`
	SortOrder      int        `db:"sort_order"`
	Active         bool       `db:"active"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

// Product groups the SKUs (variants) sold under one name.
type Product struct {
	ID             uuid.UUID  `db:"id"`
	Type           string     `db:"type"`
	Name           string     `db:"name"`
	NameKey        string     `db:"name_key"`
	CategoryID     *uuid.UUID `db:"category_id"`
	AttributeSetID *uuid.UUID `db:"attribute_set_id"`
	Attributes     JSONObject `db:"attributes"`
	Description    *string    `db:"description"`
	Status         string     `db:"status"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	Version        int        `db:"version"`
}

// VariantAxis is a select attribute whose options span the SKU matrix of a product.
type VariantAxis struct {
	AttributeID uuid.UUID `db:"attribute_id"`
	Position    int       `db:"position"`
}

// Sku is the sellable, stocked unit of the catalog.
type Sku struct {
	ID           uuid.UUID    `db:"id"`
	ProductID    uuid.UUID    `db:"product_id"`
	Code         string       `db:"code"`
	VariantKey   string       `db:"variant_key"`
	DisplayName  string       `db:"display_name"`
	VariantLabel string       `db:"variant_label"`
	BaseUnitID   uuid.UUID    `db:"base_unit_id"`
	QtyScale     int          `db:"qty_scale"`
	TrackLot     bool         `db:"track_lot"`
	TrackExpiry  bool         `db:"track_expiry"`
	RefCost      *money.Money `db:"ref_cost"`
	MinPrice     *money.Money `db:"min_price"`
	MinStock     money.Qty    `db:"min_stock"`
	TaxRateID    *uuid.UUID   `db:"tax_rate_id"`
	SpecText     *string      `db:"spec_text"`
	Status       string       `db:"status"`
	SearchNorm   string       `db:"search_norm"`
	CreatedAt    time.Time    `db:"created_at"`
	UpdatedAt    time.Time    `db:"updated_at"`
	Version      int          `db:"version"`
}

// SkuUnit converts a unit of a SKU into its base unit.
type SkuUnit struct {
	SkuID             uuid.UUID    `db:"sku_id"`
	UnitID            uuid.UUID    `db:"unit_id"`
	UnitName          string       `db:"unit_name"`
	Factor            money.Factor `db:"factor"`
	IsBase            bool         `db:"is_base"`
	Active            bool         `db:"active"`
	IsDefaultSale     bool         `db:"is_default_sale"`
	IsDefaultPurchase bool         `db:"is_default_purchase"`
	// RetailPrice and WholesalePrice are the unit's own base list prices (general color, no tier);
	// nil on a non-base unit means the price is the base price × factor.
	RetailPrice    *money.Money `db:"-"`
	WholesalePrice *money.Money `db:"-"`
	// LockedBy is why the unit cannot be deleted (UnitLockedBy…), nil when it can; always nil on
	// the base unit, which is never deleted.
	LockedBy *string `db:"-"`
}

// Barcode is a scannable code of one unit of a SKU.
type Barcode struct {
	ID        uuid.UUID `db:"id"`
	SkuID     uuid.UUID `db:"sku_id"`
	UnitID    uuid.UUID `db:"unit_id"`
	Code      string    `db:"code"`
	CreatedAt time.Time `db:"created_at"`
}

// ProductImage links an uploaded attachment to a product (optionally to one SKU).
type ProductImage struct {
	ID           uuid.UUID  `db:"id"`
	ProductID    uuid.UUID  `db:"product_id"`
	SkuID        *uuid.UUID `db:"sku_id"`
	AttachmentID uuid.UUID  `db:"attachment_id"`
	SortOrder    int        `db:"sort_order"`
	CreatedAt    time.Time  `db:"created_at"`
}

// MixGroup lets a pack (thùng) be filled with parts (dây) of different flavors.
type MixGroup struct {
	ID              uuid.UUID  `db:"id"`
	Name            string     `db:"name"`
	PackUnitID      uuid.UUID  `db:"pack_unit_id"`
	UnitsPerPack    int        `db:"units_per_pack"`
	PartUnitID      uuid.UUID  `db:"part_unit_id"`
	UnitsPerPart    int        `db:"units_per_part"`
	PieceUnitID     uuid.UUID  `db:"piece_unit_id"`
	SourceProductID *uuid.UUID `db:"source_product_id"`
	Status          string     `db:"status"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	Version         int        `db:"version"`
	Items           []MixGroupItem
	// Usage counts the documents that use the group per kind; UsedSkuIDs are the flavors they
	// used. Both are read, never written, and fix what an edit may still change.
	Usage      []UsageCount
	UsedSkuIDs []uuid.UUID
}

// MixGroupItem is one flavor (a SKU with a short label) of a mix group.
type MixGroupItem struct {
	SkuID       uuid.UUID `db:"sku_id"`
	SkuCode     string    `db:"sku_code"`
	SkuName     string    `db:"sku_name"`
	FlavorLabel string    `db:"flavor_label"`
	SortOrder   int       `db:"sort_order"`
	Active      bool      `db:"active"`
}

// JSONObject is a jsonb object column (products.attributes). Scan copies the driver buffer.
type JSONObject json.RawMessage

// Scan implements sql.Scanner.
func (j *JSONObject) Scan(src any) error {
	switch v := src.(type) {
	case []byte:
		*j = append(JSONObject(nil), v...)
	case string:
		*j = JSONObject(v)
	case nil:
		*j = JSONObject("{}")
	default:
		return fmt.Errorf("catalog: cannot scan %T into a JSON object", src)
	}
	return nil
}

// Value implements driver.Valuer; an empty value is the empty object.
func (j JSONObject) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	return string(j), nil
}

// MarshalJSON writes the object as is.
func (j JSONObject) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("{}"), nil
	}
	return j, nil
}
