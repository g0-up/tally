package catalog

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the catalog endpoints. Money, quantities and factors travel as decimal strings.

func mapSlice[T, D any](in []T, fn func(T) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

type colorDTO struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Hex       string    `json:"hex"`
	SortOrder int       `json:"sort_order"`
	IsSystem  bool      `json:"is_system"`
	Active    bool      `json:"active"`
}

func toColorDTO(c Color) colorDTO {
	return colorDTO(c)
}

type unitDTO struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

func toUnitDTO(u Unit) unitDTO {
	return unitDTO(u)
}

type unitRequest struct {
	Name   *string `json:"name"`
	Active *bool   `json:"active"`
}

type taxRateDTO struct {
	ID          uuid.UUID     `json:"id"`
	Name        string        `json:"name"`
	RatePercent money.Percent `json:"rate_percent"`
	Active      bool          `json:"active"`
	CreatedAt   time.Time     `json:"created_at"`
}

func toTaxRateDTO(t TaxRate) taxRateDTO {
	return taxRateDTO(t)
}

type taxRateRequest struct {
	Name        *string        `json:"name"`
	RatePercent *money.Percent `json:"rate_percent"`
	Active      *bool          `json:"active"`
}

type optionDTO struct {
	ID          uuid.UUID `json:"id"`
	AttributeID uuid.UUID `json:"attribute_id"`
	Label       string    `json:"label"`
	SortOrder   int       `json:"sort_order"`
	Active      bool      `json:"active"`
}

func toOptionDTO(o AttributeOption) optionDTO {
	return optionDTO(o)
}

type attributeDTO struct {
	ID           uuid.UUID   `json:"id"`
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	DataType     DataType    `json:"data_type"`
	UnitLabel    *string     `json:"unit_label"`
	IsFilterable bool        `json:"is_filterable"`
	IsSearchable bool        `json:"is_searchable"`
	Status       string      `json:"status"`
	Options      []optionDTO `json:"options"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Version      int         `json:"version"`
}

func toAttributeDTO(a Attribute) attributeDTO {
	return attributeDTO{ID: a.ID, Code: a.Code, Name: a.Name, DataType: a.DataType, UnitLabel: a.UnitLabel,
		IsFilterable: a.IsFilterable, IsSearchable: a.IsSearchable, Status: a.Status, Options: mapSlice(a.Options, toOptionDTO),
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Version: a.Version}
}

type attributeResultDTO struct {
	attributeDTO
	Warnings []string `json:"warnings"`
}

func toAttributeResultDTO(r AttributeResult) attributeResultDTO {
	w := r.Warnings
	if w == nil {
		w = []string{}
	}
	return attributeResultDTO{attributeDTO: toAttributeDTO(r.Attribute), Warnings: w}
}

type attributeRequest struct {
	Code         *string   `json:"code"`
	Name         *string   `json:"name"`
	DataType     *DataType `json:"data_type"`
	UnitLabel    *string   `json:"unit_label"`
	IsFilterable *bool     `json:"is_filterable"`
	IsSearchable *bool     `json:"is_searchable"`
	Status       *string   `json:"status"`
	Options      []string  `json:"options"`
}

func (r attributeRequest) input() AttributeInput {
	return AttributeInput(r)
}

type optionRequest struct {
	Label     *string `json:"label"`
	SortOrder *int    `json:"sort_order"`
	Active    *bool   `json:"active"`
}

type setItemDTO struct {
	AttributeID   uuid.UUID `json:"attribute_id"`
	AttributeCode string    `json:"attribute_code"`
	AttributeName string    `json:"attribute_name"`
	IsRequired    bool      `json:"is_required"`
	SortOrder     int       `json:"sort_order"`
}

type attributeSetDTO struct {
	ID        uuid.UUID    `json:"id"`
	Name      string       `json:"name"`
	Active    bool         `json:"active"`
	Items     []setItemDTO `json:"items"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func toAttributeSetDTO(s AttributeSet) attributeSetDTO {
	return attributeSetDTO{ID: s.ID, Name: s.Name, Active: s.Active, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		Items: mapSlice(s.Items, func(it AttributeSetItem) setItemDTO {
			return setItemDTO(it)
		})}
}

type setItemsResultDTO struct {
	attributeSetDTO
	HiddenAttributes []string `json:"hidden_attributes"`
}

type setItemRequest struct {
	AttributeID uuid.UUID `json:"attribute_id"`
	IsRequired  bool      `json:"is_required"`
	SortOrder   int       `json:"sort_order"`
}

func setItems(items []setItemRequest) []SetItem {
	if items == nil {
		return nil
	}
	return mapSlice(items, func(it setItemRequest) SetItem {
		return SetItem(it)
	})
}

type attributeSetRequest struct {
	Name   *string          `json:"name"`
	Active *bool            `json:"active"`
	Items  []setItemRequest `json:"items"`
}

type setItemsRequest struct {
	Items []setItemRequest `json:"items"`
}

type categoryDTO struct {
	ID             uuid.UUID  `json:"id"`
	ParentID       *uuid.UUID `json:"parent_id"`
	Name           string     `json:"name"`
	AttributeSetID *uuid.UUID `json:"attribute_set_id"`
	SortOrder      int        `json:"sort_order"`
	Active         bool       `json:"active"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toCategoryDTO(c Category) categoryDTO {
	return categoryDTO(c)
}

type categoryRequest struct {
	Name           *string        `json:"name"`
	ParentID       Opt[uuid.UUID] `json:"parent_id"`
	AttributeSetID Opt[uuid.UUID] `json:"attribute_set_id"`
	SortOrder      *int           `json:"sort_order"`
	Active         *bool          `json:"active"`
}

func (r categoryRequest) input() CategoryInput {
	return CategoryInput(r)
}

type productDTO struct {
	ID             uuid.UUID       `json:"id"`
	Type           string          `json:"type"`
	Name           string          `json:"name"`
	CategoryID     *uuid.UUID      `json:"category_id"`
	AttributeSetID *uuid.UUID      `json:"attribute_set_id"`
	Attributes     json.RawMessage `json:"attributes"`
	Description    *string         `json:"description"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Version        int             `json:"version"`
}

func toProductDTO(p Product) productDTO {
	attrs := json.RawMessage(p.Attributes)
	if len(attrs) == 0 {
		attrs = json.RawMessage("{}")
	}
	return productDTO{ID: p.ID, Type: p.Type, Name: p.Name, CategoryID: p.CategoryID, AttributeSetID: p.AttributeSetID,
		Attributes: attrs, Description: p.Description, Status: p.Status, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		Version: p.Version}
}

type productListItemDTO struct {
	productDTO
	SkuCount int `json:"sku_count"`
}

type axisDTO struct {
	Position  int          `json:"position"`
	Attribute attributeDTO `json:"attribute"`
}

func toAxisDTO(a AxisDetail) axisDTO {
	return axisDTO{Position: a.Position, Attribute: toAttributeDTO(a.Attribute)}
}

type skuUnitDTO struct {
	UnitID            uuid.UUID    `json:"unit_id"`
	UnitName          string       `json:"unit_name"`
	Factor            money.Factor `json:"factor"`
	IsBase            bool         `json:"is_base"`
	Active            bool         `json:"active"`
	IsDefaultSale     bool         `json:"is_default_sale"`
	IsDefaultPurchase bool         `json:"is_default_purchase"`
	RetailPrice       *money.Money `json:"retail_price"`
	WholesalePrice    *money.Money `json:"wholesale_price"`
	LockedBy          *string      `json:"locked_by"`
}

func toSkuUnitDTO(u SkuUnit) skuUnitDTO {
	return skuUnitDTO{UnitID: u.UnitID, UnitName: u.UnitName, Factor: u.Factor, IsBase: u.IsBase, Active: u.Active,
		IsDefaultSale: u.IsDefaultSale, IsDefaultPurchase: u.IsDefaultPurchase, RetailPrice: u.RetailPrice,
		WholesalePrice: u.WholesalePrice, LockedBy: u.LockedBy}
}

type barcodeDTO struct {
	ID        uuid.UUID `json:"id"`
	UnitID    uuid.UUID `json:"unit_id"`
	Code      string    `json:"code"`
	CreatedAt time.Time `json:"created_at"`
}

func toBarcodeDTO(b Barcode) barcodeDTO {
	return barcodeDTO{ID: b.ID, UnitID: b.UnitID, Code: b.Code, CreatedAt: b.CreatedAt}
}

type skuDTO struct {
	ID           uuid.UUID    `json:"id"`
	ProductID    uuid.UUID    `json:"product_id"`
	Code         string       `json:"code"`
	VariantKey   string       `json:"variant_key"`
	DisplayName  string       `json:"display_name"`
	VariantLabel string       `json:"variant_label"`
	BaseUnitID   uuid.UUID    `json:"base_unit_id"`
	QtyScale     int          `json:"qty_scale"`
	TrackLot     bool         `json:"track_lot"`
	TrackExpiry  bool         `json:"track_expiry"`
	RefCost      *money.Money `json:"ref_cost"`
	MinPrice     *money.Money `json:"min_price"`
	MinStock     money.Qty    `json:"min_stock"`
	TaxRateID    *uuid.UUID   `json:"tax_rate_id"`
	SpecText     *string      `json:"spec_text"`
	Status       string       `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	Version      int          `json:"version"`
}

func toSkuDTO(s Sku) skuDTO {
	return skuDTO{ID: s.ID, ProductID: s.ProductID, Code: s.Code, VariantKey: s.VariantKey, DisplayName: s.DisplayName,
		VariantLabel: s.VariantLabel, BaseUnitID: s.BaseUnitID, QtyScale: s.QtyScale, TrackLot: s.TrackLot,
		TrackExpiry: s.TrackExpiry, RefCost: s.RefCost, MinPrice: s.MinPrice, MinStock: s.MinStock, TaxRateID: s.TaxRateID,
		SpecText: s.SpecText, Status: s.Status, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Version: s.Version}
}

type skuDetailDTO struct {
	skuDTO
	Units     []skuUnitDTO `json:"units"`
	Barcodes  []barcodeDTO `json:"barcodes"`
	OptionIDs []uuid.UUID  `json:"option_ids"`
}

func toSkuDetailDTO(d SkuDetail) skuDetailDTO {
	opts := d.OptionIDs
	if opts == nil {
		opts = []uuid.UUID{}
	}
	return skuDetailDTO{skuDTO: toSkuDTO(d.Sku), Units: mapSlice(d.Units, toSkuUnitDTO), Barcodes: mapSlice(d.Barcodes, toBarcodeDTO),
		OptionIDs: opts}
}

type skuListItemDTO struct {
	skuDTO
	ProductType       string        `json:"product_type"`
	CategoryID        *uuid.UUID    `json:"category_id"`
	CategoryName      *string       `json:"category_name"`
	Units             []skuUnitDTO  `json:"units"`
	Barcode           *string       `json:"barcode"`
	RetailPrice       *money.Money  `json:"retail_price"`
	WholesalePrice    *money.Money  `json:"wholesale_price"`
	HasColorPrices    bool          `json:"has_color_prices"`
	OnHand            money.Qty     `json:"on_hand"`
	Sellable          money.Qty     `json:"sellable"`
	AvgCost           *money.Money  `json:"avg_cost"`
	LastPurchasePrice *money.Money  `json:"last_purchase_price"`
	InMixGroup        bool          `json:"in_mix_group"`
	OnHandByColor     []colorQtyDTO `json:"on_hand_by_color"`
}

func toSkuListItemDTO(it SkuListItem) skuListItemDTO {
	return skuListItemDTO{skuDTO: toSkuDTO(it.Sku), ProductType: it.ProductType, CategoryID: it.CategoryID,
		CategoryName: it.CategoryName, Units: mapSlice(it.Units, toSkuUnitDTO), Barcode: it.Barcode, RetailPrice: it.RetailPrice,
		WholesalePrice: it.WholesalePrice, HasColorPrices: it.HasColorPrices, OnHand: it.OnHand, Sellable: it.Sellable,
		AvgCost: it.AvgCost, LastPurchasePrice: it.LastPurchasePrice, InMixGroup: it.InMixGroup,
		OnHandByColor: mapSlice(it.OnHandByColor, func(c ColorQty) colorQtyDTO { return colorQtyDTO(c) })}
}

type imageDTO struct {
	ID           uuid.UUID  `json:"id"`
	ProductID    uuid.UUID  `json:"product_id"`
	SkuID        *uuid.UUID `json:"sku_id"`
	AttachmentID uuid.UUID  `json:"attachment_id"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toImageDTO(i ProductImage) imageDTO {
	return imageDTO(i)
}

type productDetailDTO struct {
	productDTO
	VariantAxes []axisDTO      `json:"variant_axes"`
	Skus        []skuDetailDTO `json:"skus"`
	Images      []imageDTO     `json:"images"`
}

func toProductDetailDTO(d ProductDetail) productDetailDTO {
	return productDetailDTO{productDTO: toProductDTO(d.Product), VariantAxes: mapSlice(d.Axes, toAxisDTO),
		Skus: mapSlice(d.Skus, toSkuDetailDTO), Images: mapSlice(d.Images, toImageDTO)}
}

// skuFieldsRequest are the SKU fields shared by a product's single SKU and generated variants.
type skuFieldsRequest struct {
	BaseUnitID  uuid.UUID    `json:"base_unit_id"`
	QtyScale    int          `json:"qty_scale"`
	TrackLot    bool         `json:"track_lot"`
	TrackExpiry bool         `json:"track_expiry"`
	RefCost     *money.Money `json:"ref_cost"`
	MinPrice    *money.Money `json:"min_price"`
	MinStock    *money.Qty   `json:"min_stock"`
	TaxRateID   *uuid.UUID   `json:"tax_rate_id"`
	SpecText    *string      `json:"spec_text"`
	Status      string       `json:"status"`
}

func (r skuFieldsRequest) input() SkuInput {
	return SkuInput{BaseUnitID: r.BaseUnitID, QtyScale: r.QtyScale, TrackLot: r.TrackLot, TrackExpiry: r.TrackExpiry,
		RefCost: r.RefCost, MinPrice: r.MinPrice, MinStock: r.MinStock, TaxRateID: r.TaxRateID, SpecText: r.SpecText, Status: r.Status}
}

// unitPriceRequest is a conversion unit of a new SKU with its own prices.
type unitPriceRequest struct {
	UnitID         uuid.UUID    `json:"unit_id"`
	Factor         *string      `json:"factor"`
	RetailPrice    *money.Money `json:"retail_price"`
	WholesalePrice *money.Money `json:"wholesale_price"`
}

func (r unitPriceRequest) input() UnitPriceInput { return UnitPriceInput(r) }

type skuCreateRequest struct {
	skuFieldsRequest
	Code           string             `json:"code"`
	Barcode        *string            `json:"barcode"`
	PurchaseUnitID *uuid.UUID         `json:"purchase_unit_id"`
	Factor         *string            `json:"factor"`
	Units          []unitPriceRequest `json:"units"`
	RetailPrice    *money.Money       `json:"retail_price"`
	WholesalePrice *money.Money       `json:"wholesale_price"`
}

type productCreateRequest struct {
	Type           string            `json:"type"`
	Name           string            `json:"name"`
	CategoryID     *uuid.UUID        `json:"category_id"`
	AttributeSetID Opt[uuid.UUID]    `json:"attribute_set_id"`
	Attributes     json.RawMessage   `json:"attributes"`
	Description    *string           `json:"description"`
	Status         string            `json:"status"`
	Sku            *skuCreateRequest `json:"sku"`
}

func (r productCreateRequest) input() ProductInput {
	in := ProductInput{Type: r.Type, Name: r.Name, CategoryID: r.CategoryID, AttributeSetID: r.AttributeSetID,
		Attributes: r.Attributes, Description: r.Description, Status: r.Status}
	if r.Sku != nil {
		sku := r.Sku.input()
		sku.Code, sku.Barcode = r.Sku.Code, r.Sku.Barcode
		in.Sku = &sku
		in.SkuExtras = SkuExtras{PurchaseUnitID: r.Sku.PurchaseUnitID, Factor: r.Sku.Factor,
			Units: mapSlice(r.Sku.Units, unitPriceRequest.input), RetailPrice: r.Sku.RetailPrice, WholesalePrice: r.Sku.WholesalePrice}
	}
	return in
}

type productPatchRequest struct {
	Type           *string         `json:"type"`
	Name           *string         `json:"name"`
	CategoryID     Opt[uuid.UUID]  `json:"category_id"`
	AttributeSetID Opt[uuid.UUID]  `json:"attribute_set_id"`
	Attributes     json.RawMessage `json:"attributes"`
	Description    Opt[string]     `json:"description"`
	Status         *string         `json:"status"`
}

func (r productPatchRequest) input() ProductPatch {
	return ProductPatch(r)
}

type imageRequest struct {
	AttachmentID uuid.UUID  `json:"attachment_id"`
	SkuID        *uuid.UUID `json:"sku_id"`
	SortOrder    *int       `json:"sort_order"`
}

type axesRequest struct {
	AttributeIDs []uuid.UUID `json:"attribute_ids"`
}

type variantRequest struct {
	OptionIDs  []uuid.UUID       `json:"option_ids"`
	CodePrefix string            `json:"code_prefix"`
	Codes      map[string]string `json:"codes"`
	Defaults   *skuFieldsRequest `json:"defaults"`
}

func (r variantRequest) input() VariantRequest {
	in := VariantRequest{OptionIDs: r.OptionIDs, CodePrefix: r.CodePrefix, Codes: r.Codes}
	if r.Defaults != nil {
		d := r.Defaults.input()
		in.Defaults = &d
	}
	return in
}

type variantRowDTO struct {
	VariantKey   string      `json:"variant_key"`
	VariantLabel string      `json:"variant_label"`
	DisplayName  string      `json:"display_name"`
	OptionIDs    []uuid.UUID `json:"option_ids"`
	Code         string      `json:"code"`
	SkuID        *uuid.UUID  `json:"sku_id"`
	Exists       bool        `json:"exists"`
}

func toVariantRowDTO(r VariantRow) variantRowDTO {
	opts := r.OptionIDs
	if opts == nil {
		opts = []uuid.UUID{}
	}
	return variantRowDTO{VariantKey: r.VariantKey, VariantLabel: r.VariantLabel, DisplayName: r.DisplayName, OptionIDs: opts,
		Code: r.Code, SkuID: r.SkuID, Exists: r.SkuID != nil}
}

type variantResultDTO struct {
	Created  []skuDetailDTO `json:"created"`
	Existing int            `json:"existing"`
}

type skuPatchRequest struct {
	Code        *string          `json:"code"`
	BaseUnitID  *uuid.UUID       `json:"base_unit_id"`
	QtyScale    *int             `json:"qty_scale"`
	TrackLot    *bool            `json:"track_lot"`
	TrackExpiry *bool            `json:"track_expiry"`
	RefCost     Opt[money.Money] `json:"ref_cost"`
	MinPrice    Opt[money.Money] `json:"min_price"`
	MinStock    *money.Qty       `json:"min_stock"`
	TaxRateID   Opt[uuid.UUID]   `json:"tax_rate_id"`
	SpecText    Opt[string]      `json:"spec_text"`
	Status      *string          `json:"status"`
}

func (r skuPatchRequest) input() SkuPatch {
	return SkuPatch(r)
}

type skuUnitPatchRequest struct {
	Factor            *string `json:"factor"`
	Active            *bool   `json:"active"`
	IsDefaultSale     *bool   `json:"is_default_sale"`
	IsDefaultPurchase *bool   `json:"is_default_purchase"`
}

type skuUnitRequest struct {
	skuUnitPatchRequest
	UnitID uuid.UUID `json:"unit_id"`
}

func (r skuUnitPatchRequest) input(unitID uuid.UUID) SkuUnitInput {
	return SkuUnitInput{UnitID: unitID, Factor: r.Factor, Active: r.Active, IsDefaultSale: r.IsDefaultSale,
		IsDefaultPurchase: r.IsDefaultPurchase}
}

// skuUnitRowRequest is a non-base row of a SKU's unit table; active defaults to true.
type skuUnitRowRequest struct {
	UnitID         uuid.UUID    `json:"unit_id"`
	Factor         *string      `json:"factor"`
	Active         *bool        `json:"active"`
	RetailPrice    *money.Money `json:"retail_price"`
	WholesalePrice *money.Money `json:"wholesale_price"`
}

// skuUnitsRequest is the whole unit table of a SKU (PUT /skus/{id}/units). Units is required, so
// a body without it never deletes the table; a row's omitted price is no own price, as null is.
type skuUnitsRequest struct {
	RetailPrice    *money.Money        `json:"retail_price"`
	WholesalePrice *money.Money        `json:"wholesale_price"`
	Units          []skuUnitRowRequest `json:"units" validate:"required"`
}

func (r skuUnitsRequest) input() SkuUnitsInput {
	return SkuUnitsInput{RetailPrice: r.RetailPrice, WholesalePrice: r.WholesalePrice,
		Units: mapSlice(r.Units, func(u skuUnitRowRequest) SkuUnitRow {
			return SkuUnitRow{UnitID: u.UnitID, Factor: u.Factor, Active: u.Active == nil || *u.Active, RetailPrice: u.RetailPrice,
				WholesalePrice: u.WholesalePrice}
		})}
}

type barcodeRequest struct {
	Code   string     `json:"code"`
	UnitID *uuid.UUID `json:"unit_id"`
}

// barcodeUnitRequest moves a barcode to another unit; a missing unit_id decodes to uuid.Nil.
type barcodeUnitRequest struct {
	UnitID uuid.UUID `json:"unit_id"`
}

type mixItemDTO struct {
	SkuID       uuid.UUID `json:"sku_id"`
	SkuCode     string    `json:"sku_code"`
	SkuName     string    `json:"sku_name"`
	FlavorLabel string    `json:"flavor_label"`
	SortOrder   int       `json:"sort_order"`
	Active      bool      `json:"active"`
}

type usageDTO struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type mixGroupDTO struct {
	ID              uuid.UUID    `json:"id"`
	Name            string       `json:"name"`
	PackUnitID      uuid.UUID    `json:"pack_unit_id"`
	UnitsPerPack    int          `json:"units_per_pack"`
	PartUnitID      uuid.UUID    `json:"part_unit_id"`
	UnitsPerPart    int          `json:"units_per_part"`
	PieceUnitID     uuid.UUID    `json:"piece_unit_id"`
	SourceProductID *uuid.UUID   `json:"source_product_id"`
	Status          string       `json:"status"`
	Items           []mixItemDTO `json:"items"`
	Usage           []usageDTO   `json:"usage"`
	UsedSkuIDs      []uuid.UUID  `json:"used_sku_ids"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	Version         int          `json:"version"`
}

func toMixGroupDTO(g MixGroup) mixGroupDTO {
	return mixGroupDTO{ID: g.ID, Name: g.Name, PackUnitID: g.PackUnitID, UnitsPerPack: g.UnitsPerPack, PartUnitID: g.PartUnitID,
		UnitsPerPart: g.UnitsPerPart, PieceUnitID: g.PieceUnitID, SourceProductID: g.SourceProductID, Status: g.Status,
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, Version: g.Version,
		Items: mapSlice(g.Items, func(it MixGroupItem) mixItemDTO {
			return mixItemDTO(it)
		}),
		Usage:      mapSlice(g.Usage, func(u UsageCount) usageDTO { return usageDTO(u) }),
		UsedSkuIDs: g.UsedSkuIDs}
}

type mixItemRequest struct {
	SkuID       uuid.UUID `json:"sku_id"`
	FlavorLabel string    `json:"flavor_label"`
	Active      *bool     `json:"active"`
}

type mixGroupRequest struct {
	Name            string           `json:"name"`
	PackUnitID      uuid.UUID        `json:"pack_unit_id"`
	UnitsPerPack    int              `json:"units_per_pack"`
	PartUnitID      uuid.UUID        `json:"part_unit_id"`
	UnitsPerPart    int              `json:"units_per_part"`
	SourceProductID *uuid.UUID       `json:"source_product_id"`
	Items           []mixItemRequest `json:"items"`
}

func (r mixGroupRequest) input() MixGroupInput {
	return MixGroupInput{Name: r.Name, PackUnitID: r.PackUnitID, UnitsPerPack: r.UnitsPerPack, PartUnitID: r.PartUnitID,
		UnitsPerPart: r.UnitsPerPart, SourceProductID: r.SourceProductID,
		Items: mapSlice(r.Items, func(it mixItemRequest) MixGroupItemDraft {
			return MixGroupItemDraft{SkuID: it.SkuID, FlavorLabel: it.FlavorLabel, Active: it.Active == nil || *it.Active}
		})}
}

type quickCreateRequest struct {
	Name           string             `json:"name"`
	Code           *string            `json:"code"`
	CategoryID     *uuid.UUID         `json:"category_id"`
	BaseUnitID     uuid.UUID          `json:"base_unit_id"`
	PurchaseUnitID *uuid.UUID         `json:"purchase_unit_id"`
	Factor         *string            `json:"factor"`
	Units          []unitPriceRequest `json:"units"`
	PurchasePrice  *money.Money       `json:"purchase_price"`
	SalePrice      *money.Money       `json:"sale_price"`
	WholesalePrice *money.Money       `json:"wholesale_price"`
	Barcode        *string            `json:"barcode"`
	TrackLot       bool               `json:"track_lot"`
}

func (r quickCreateRequest) input() QuickCreateInput {
	return QuickCreateInput{Name: r.Name, Code: r.Code, CategoryID: r.CategoryID, BaseUnitID: r.BaseUnitID,
		PurchaseUnitID: r.PurchaseUnitID, Factor: r.Factor, Units: mapSlice(r.Units, unitPriceRequest.input),
		PurchasePrice: r.PurchasePrice, SalePrice: r.SalePrice, WholesalePrice: r.WholesalePrice, Barcode: r.Barcode,
		TrackLot: r.TrackLot}
}

type quickCreateDTO struct {
	Product        productDetailDTO `json:"product"`
	SalePriceSaved bool             `json:"sale_price_saved"`
}

type colorQtyDTO struct {
	ColorID *uuid.UUID `json:"color_id"`
	Qty     money.Qty  `json:"qty"`
}

type searchItemDTO struct {
	Kind          string        `json:"kind"`
	ID            uuid.UUID     `json:"id"`
	Code          string        `json:"code"`
	Label         string        `json:"label"`
	Sublabel      *string       `json:"sublabel"`
	Unit          *string       `json:"unit"`
	PriceHint     *money.Money  `json:"price_hint"`
	OnHandByColor []colorQtyDTO `json:"on_hand_by_color"`
}

func toSearchItemDTO(it SearchItem) searchItemDTO {
	return searchItemDTO{Kind: it.Kind, ID: it.ID, Code: it.Code, Label: it.Label, Sublabel: it.Sublabel, Unit: it.Unit,
		PriceHint: it.PriceHint, OnHandByColor: mapSlice(it.OnHandByColor, func(c ColorQty) colorQtyDTO {
			return colorQtyDTO(c)
		})}
}
