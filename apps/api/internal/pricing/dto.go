package pricing

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the pricing endpoints. Money, quantities, factors and rates travel as decimal
// strings; business dates as "YYYY-MM-DD".

func mapSlice[T, D any](in []T, fn func(T) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

// date is a civil date on the wire ("YYYY-MM-DD"), held as midnight UTC.
type date time.Time

// UnmarshalJSON reads a "YYYY-MM-DD" string.
func (d *date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: reflect.TypeFor[date]()}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string " + s, Type: reflect.TypeFor[date]()}
	}
	*d = date(clock.Date(t.Year(), t.Month(), t.Day()))
	return nil
}

func (d *date) time() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

func optDate(o Opt[date]) Opt[time.Time] {
	return Opt[time.Time]{Set: o.Set, Value: o.Value.time()}
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

// priceListRequest is the body of POST /price-lists and PATCH /price-lists/{id}.
type priceListRequest struct {
	Name            *string    `json:"name"`
	Kind            *string    `json:"kind"`
	CustomerGroupID *uuid.UUID `json:"customer_group_id"`
	PartyID         *uuid.UUID `json:"party_id"`
	Priority        *int       `json:"priority"`
	ValidFrom       Opt[date]  `json:"valid_from"`
	ValidTo         Opt[date]  `json:"valid_to"`
	Active          *bool      `json:"active"`
}

func (r priceListRequest) input() PriceListInput {
	return PriceListInput{
		Name: r.Name, Kind: r.Kind, CustomerGroupID: r.CustomerGroupID, PartyID: r.PartyID, Priority: r.Priority,
		ValidFrom: optDate(r.ValidFrom), ValidTo: optDate(r.ValidTo), Active: r.Active,
	}
}

type priceListDTO struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	CustomerGroupID *uuid.UUID `json:"customer_group_id"`
	PartyID         *uuid.UUID `json:"party_id"`
	CampaignID      *uuid.UUID `json:"campaign_id"`
	Priority        int        `json:"priority"`
	ValidFrom       *string    `json:"valid_from"`
	ValidTo         *string    `json:"valid_to"`
	Active          bool       `json:"active"`
	ItemCount       int        `json:"item_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Version         int        `json:"version"`
}

func toPriceListDTO(l PriceList) priceListDTO {
	return priceListDTO{
		ID: l.ID, Name: l.Name, Kind: l.Kind, CustomerGroupID: l.CustomerGroupID, PartyID: l.PartyID,
		CampaignID: l.CampaignID, Priority: l.Priority, ValidFrom: dateString(l.ValidFrom), ValidTo: dateString(l.ValidTo),
		Active: l.Active, ItemCount: l.ItemCount, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt, Version: l.Version,
	}
}

// priceListItemsRequest is the body of PUT /price-lists/{id}/items.
type priceListItemsRequest struct {
	Items []priceListItemRequest `json:"items"`
}

type priceListItemRequest struct {
	SkuID   uuid.UUID    `json:"sku_id"`
	ColorID *uuid.UUID   `json:"color_id"`
	UnitID  *uuid.UUID   `json:"unit_id"`
	MinQty  *money.Qty   `json:"min_qty"`
	Price   *money.Money `json:"price"`
}

func (r priceListItemsRequest) input() []ItemInput {
	return mapSlice(r.Items, func(it priceListItemRequest) ItemInput { return ItemInput(it) })
}

type priceListItemDTO struct {
	ID          uuid.UUID   `json:"id"`
	PriceListID uuid.UUID   `json:"price_list_id"`
	SkuID       uuid.UUID   `json:"sku_id"`
	SkuCode     string      `json:"sku_code"`
	SkuName     string      `json:"sku_name"`
	ColorID     *uuid.UUID  `json:"color_id"`
	ColorName   *string     `json:"color_name"`
	UnitID      *uuid.UUID  `json:"unit_id"`
	UnitName    *string     `json:"unit_name"`
	MinQty      money.Qty   `json:"min_qty"`
	Price       money.Money `json:"price"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func toPriceListItemDTO(i PriceListItem) priceListItemDTO {
	return priceListItemDTO{
		ID: i.ID, PriceListID: i.PriceListID, SkuID: i.SkuID, SkuCode: i.SkuCode, SkuName: i.SkuName,
		ColorID: i.ColorID, ColorName: i.ColorName, UnitID: i.UnitID, UnitName: i.UnitName, MinQty: i.MinQty,
		Price: i.Price, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

// salePriceRequest is the body of POST /skus/{id}/sale-price; a null price removes the row.
type salePriceRequest struct {
	Kind           string           `json:"kind"`
	ColorID        *uuid.UUID       `json:"color_id"`
	UnitID         *uuid.UUID       `json:"unit_id"`
	ApplyAllColors bool             `json:"apply_all_colors"`
	Price          Opt[money.Money] `json:"price"`
	Source         string           `json:"source"`
	SourceDocID    *uuid.UUID       `json:"source_doc_id"`
}

func (r salePriceRequest) command(skuID uuid.UUID) SalePriceCommand {
	return SalePriceCommand{
		SkuID: skuID, ColorID: r.ColorID, UnitID: r.UnitID, Kind: r.Kind, ApplyAllColors: r.ApplyAllColors,
		Price: r.Price.Value, Source: r.Source, SourceDocID: r.SourceDocID,
	}
}

type priceChangeDTO struct {
	ID            uuid.UUID    `json:"id"`
	PriceListID   uuid.UUID    `json:"price_list_id"`
	PriceListName string       `json:"price_list_name"`
	PriceListKind string       `json:"price_list_kind"`
	SkuID         uuid.UUID    `json:"sku_id"`
	SkuCode       string       `json:"sku_code"`
	SkuName       string       `json:"sku_name"`
	ColorID       *uuid.UUID   `json:"color_id"`
	ColorName     *string      `json:"color_name"`
	ScopeLabel    string       `json:"scope_label"`
	UnitID        *uuid.UUID   `json:"unit_id"`
	UnitName      *string      `json:"unit_name"`
	MinQty        money.Qty    `json:"min_qty"`
	OldPrice      *money.Money `json:"old_price"`
	NewPrice      *money.Money `json:"new_price"`
	Source        string       `json:"source"`
	SourceDocID   *uuid.UUID   `json:"source_doc_id"`
	ActorID       uuid.UUID    `json:"actor_id"`
	ActorName     *string      `json:"actor_name"`
	CreatedAt     time.Time    `json:"created_at"`
}

func toPriceChangeDTO(c PriceChange) priceChangeDTO {
	return priceChangeDTO{
		ID: c.ID, PriceListID: c.PriceListID, PriceListName: c.PriceListName, PriceListKind: c.PriceListKind,
		SkuID: c.SkuID, SkuCode: c.SkuCode, SkuName: c.SkuName, ColorID: c.ColorID, ColorName: c.ColorName, ScopeLabel: c.ScopeLabel(), UnitID: c.UnitID,
		UnitName: c.UnitName, MinQty: c.MinQty, OldPrice: c.OldPrice, NewPrice: c.NewPrice, Source: c.Source,
		SourceDocID: c.SourceDocID, ActorID: c.ActorID, ActorName: c.ActorName, CreatedAt: c.CreatedAt,
	}
}

type quoteLineRequest struct {
	SkuID   uuid.UUID  `json:"sku_id"`
	ColorID *uuid.UUID `json:"color_id"`
	UnitID  *uuid.UUID `json:"unit_id"`
	Qty     money.Qty  `json:"qty"`
}

// quoteRequest is the body of POST /pricing/quote.
type quoteRequest struct {
	PartyID         *uuid.UUID         `json:"party_id"`
	CustomerGroupID *uuid.UUID         `json:"customer_group_id"`
	BusinessDate    *date              `json:"business_date"`
	Lines           []quoteLineRequest `json:"lines"`
}

func (r quoteRequest) input() QuoteInput {
	return QuoteInput{
		PartyID: r.PartyID, CustomerGroupID: r.CustomerGroupID, BusinessDate: r.BusinessDate.time(),
		Lines: mapSlice(r.Lines, func(l quoteLineRequest) PriceLine { return PriceLine(l) }),
	}
}

type resolvedPriceDTO struct {
	Amount      money.Money  `json:"amount"`
	UnitID      uuid.UUID    `json:"unit_id"`
	UnitFactor  money.Factor `json:"unit_factor"`
	BaseQty     money.Qty    `json:"base_qty"`
	PriceSource PriceSource  `json:"price_source"`
}

func toResolvedPriceDTO(p ResolvedPrice) resolvedPriceDTO {
	return resolvedPriceDTO{Amount: p.Amount, UnitID: p.UnitID, UnitFactor: p.Factor, BaseQty: p.BaseQty, PriceSource: p.Source}
}

type calculateLineRequest struct {
	SkuID          uuid.UUID    `json:"sku_id"`
	ColorID        *uuid.UUID   `json:"color_id"`
	UnitID         *uuid.UUID   `json:"unit_id"`
	Qty            money.Qty    `json:"qty"`
	UnitPrice      *money.Money `json:"unit_price"`
	DiscountAmount *money.Money `json:"discount_amount"`
}

// calculateRequest is the body of POST /pricing/calculate.
type calculateRequest struct {
	PartyID         *uuid.UUID             `json:"party_id"`
	CustomerGroupID *uuid.UUID             `json:"customer_group_id"`
	BusinessDate    *date                  `json:"business_date"`
	Lines           []calculateLineRequest `json:"lines"`
	OrderDiscount   *money.Money           `json:"order_discount"`
	PromotionCodes  []string               `json:"promotion_codes"`
}

func orZero(m *money.Money) money.Money {
	if m == nil {
		return money.ZeroMoney
	}
	return *m
}

func (r calculateRequest) draft() DocumentDraft {
	return DocumentDraft{
		PartyID: r.PartyID, CustomerGroupID: r.CustomerGroupID, BusinessDate: r.BusinessDate.time(),
		Lines: mapSlice(r.Lines, func(l calculateLineRequest) DraftLine {
			return DraftLine{
				SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty, UnitPrice: l.UnitPrice,
				DiscountAmount: orZero(l.DiscountAmount),
			}
		}),
		OrderDiscount: orZero(r.OrderDiscount), PromotionCodes: r.PromotionCodes,
	}
}

type calculatedLineDTO struct {
	// DraftIndex is the request line the row prices; null for a gift line.
	DraftIndex         *int          `json:"draft_index"`
	LineKind           string        `json:"line_kind"`
	SkuID              uuid.UUID     `json:"sku_id"`
	ColorID            *uuid.UUID    `json:"color_id"`
	UnitID             uuid.UUID     `json:"unit_id"`
	UnitFactor         money.Factor  `json:"unit_factor"`
	Qty                money.Qty     `json:"qty"`
	BaseQty            money.Qty     `json:"base_qty"`
	UnitPrice          money.Money   `json:"unit_price"`
	GrossAmount        money.Money   `json:"gross_amount"`
	DiscountAmount     money.Money   `json:"discount_amount"`
	OrderDiscountAlloc money.Money   `json:"order_discount_alloc"`
	TaxRateSnap        money.Percent `json:"tax_rate_snap"`
	TaxAmount          money.Money   `json:"tax_amount"`
	LineAmount         money.Money   `json:"line_amount"`
	NetUnitPrice       money.Money   `json:"net_unit_price"`
	MinPriceSnap       *money.Money  `json:"min_price_snap"`
	BelowMinPrice      bool          `json:"below_min_price"`
	PriceSource        PriceSource   `json:"price_source"`
}

func toCalculatedLineDTO(l CalculatedLine) calculatedLineDTO {
	var idx *int
	if l.DraftIndex >= 0 {
		idx = ptr(l.DraftIndex)
	}
	return calculatedLineDTO{
		DraftIndex: idx, LineKind: l.LineKind, SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID,
		UnitFactor: l.UnitFactor, Qty: l.Qty, BaseQty: l.BaseQty, UnitPrice: l.UnitPrice, GrossAmount: l.GrossAmount,
		DiscountAmount: l.DiscountAmount, OrderDiscountAlloc: l.OrderDiscountAlloc, TaxRateSnap: l.TaxRateSnap,
		TaxAmount: l.TaxAmount, LineAmount: l.LineAmount, NetUnitPrice: l.NetUnitPrice, MinPriceSnap: l.MinPriceSnap,
		BelowMinPrice: l.BelowMinPrice, PriceSource: l.PriceSource,
	}
}

type skippedPromotionDTO struct {
	ID     uuid.UUID `json:"id"`
	Code   *string   `json:"code"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Reason string    `json:"reason"`
}

func toSkippedPromotionDTO(s SkippedPromotion) skippedPromotionDTO { return skippedPromotionDTO(s) }

type calculatedDocumentDTO struct {
	BusinessDate      string                `json:"business_date"`
	PricesIncludeTax  bool                  `json:"prices_include_tax"`
	RoundingMode      string                `json:"rounding_mode"`
	Lines             []calculatedLineDTO   `json:"lines"`
	GoodsAmount       money.Money           `json:"goods_amount"`
	DiscountAmount    money.Money           `json:"discount_amount"`
	TaxAmount         money.Money           `json:"tax_amount"`
	TotalAmount       money.Money           `json:"total_amount"`
	Promotions        []AppliedPromotion    `json:"promotions"`
	SkippedPromotions []skippedPromotionDTO `json:"skipped_promotions"`
}

func toCalculatedDocumentDTO(d CalculatedDocument) calculatedDocumentDTO {
	promos := d.Promotions
	if promos == nil {
		promos = []AppliedPromotion{}
	}
	return calculatedDocumentDTO{
		BusinessDate: d.BusinessDate.Format(time.DateOnly), PricesIncludeTax: d.PricesIncludeTax,
		RoundingMode: d.RoundingMode, Lines: mapSlice(d.Lines, toCalculatedLineDTO), GoodsAmount: d.GoodsAmount,
		DiscountAmount: d.DiscountAmount, TaxAmount: d.TaxAmount, TotalAmount: d.TotalAmount, Promotions: promos,
		SkippedPromotions: mapSlice(d.SkippedPromotions, toSkippedPromotionDTO),
	}
}

type promotionItemRequest struct {
	Role  string     `json:"role"`
	SkuID uuid.UUID  `json:"sku_id"`
	Qty   *money.Qty `json:"qty"`
}

// promotionRequest is the body of POST /promotions and PATCH /promotions/{id}.
type promotionRequest struct {
	Code            Opt[string]             `json:"code"`
	Name            *string                 `json:"name"`
	Kind            *string                 `json:"kind"`
	Value           *money.Money            `json:"value"`
	MaxDiscount     Opt[money.Money]        `json:"max_discount"`
	MinOrderAmount  *money.Money            `json:"min_order_amount"`
	Stackable       *bool                   `json:"stackable"`
	CustomerGroupID Opt[uuid.UUID]          `json:"customer_group_id"`
	ValidFrom       *date                   `json:"valid_from"`
	ValidTo         *date                   `json:"valid_to"`
	UsageLimit      Opt[int]                `json:"usage_limit"`
	Status          *string                 `json:"status"`
	Items           *[]promotionItemRequest `json:"items"`
}

func (r promotionRequest) input() PromotionInput {
	in := PromotionInput{
		Code: r.Code, Name: r.Name, Kind: r.Kind, Value: r.Value, MaxDiscount: r.MaxDiscount,
		MinOrderAmount: r.MinOrderAmount, Stackable: r.Stackable, CustomerGroupID: r.CustomerGroupID,
		ValidFrom: r.ValidFrom.time(), ValidTo: r.ValidTo.time(), UsageLimit: r.UsageLimit, Status: r.Status,
	}
	if r.Items != nil {
		items := mapSlice(*r.Items, func(it promotionItemRequest) PromotionItemInput { return PromotionItemInput(it) })
		in.Items = &items
	}
	return in
}

type promotionItemDTO struct {
	ID      uuid.UUID `json:"id"`
	Role    string    `json:"role"`
	SkuID   uuid.UUID `json:"sku_id"`
	SkuCode string    `json:"sku_code"`
	SkuName string    `json:"sku_name"`
	Qty     money.Qty `json:"qty"`
}

func toPromotionItemDTO(it PromotionItem) promotionItemDTO {
	return promotionItemDTO{ID: it.ID, Role: it.Role, SkuID: it.SkuID, SkuCode: it.SkuCode, SkuName: it.SkuName, Qty: it.Qty}
}

type promotionDTO struct {
	ID              uuid.UUID          `json:"id"`
	Code            *string            `json:"code"`
	Name            string             `json:"name"`
	Kind            string             `json:"kind"`
	Value           money.Money        `json:"value"`
	MaxDiscount     *money.Money       `json:"max_discount"`
	MinOrderAmount  money.Money        `json:"min_order_amount"`
	Stackable       bool               `json:"stackable"`
	CustomerGroupID *uuid.UUID         `json:"customer_group_id"`
	CampaignID      *uuid.UUID         `json:"campaign_id"`
	ValidFrom       string             `json:"valid_from"`
	ValidTo         string             `json:"valid_to"`
	UsageLimit      *int               `json:"usage_limit"`
	UsedCount       int                `json:"used_count"`
	Status          string             `json:"status"`
	Items           []promotionItemDTO `json:"items"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Version         int                `json:"version"`
}

func toPromotionDTO(p Promotion) promotionDTO {
	return promotionDTO{
		ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Value: p.Value, MaxDiscount: p.MaxDiscount,
		MinOrderAmount: p.MinOrderAmount, Stackable: p.Stackable, CustomerGroupID: p.CustomerGroupID,
		CampaignID: p.CampaignID, ValidFrom: p.ValidFrom.Format(time.DateOnly), ValidTo: p.ValidTo.Format(time.DateOnly),
		UsageLimit: p.UsageLimit, UsedCount: p.UsedCount, Status: p.Status, Items: mapSlice(p.Items, toPromotionItemDTO),
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Version: p.Version,
	}
}

type codeCheckDTO struct {
	Code   string `json:"code"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason"`
	// Promotion is set only for a usable code.
	Promotion *promotionDTO `json:"promotion"`
}

func toCodeCheckDTO(c CodeCheck) codeCheckDTO {
	out := codeCheckDTO{Code: c.Code, Valid: c.Valid, Reason: c.Reason}
	if c.Promotion != nil {
		out.Promotion = ptr(toPromotionDTO(*c.Promotion))
	}
	return out
}
