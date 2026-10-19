// Package pricing owns sale prices: price lists and their rows by color, unit and quantity tier,
// the append-only price history, the price resolver every sales screen calls, promotions, the
// document calculator that turns a draft into priced, discounted, taxed and rounded lines, and
// the minimum-price guard.
package pricing

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Price list kinds. Retail and wholesale are the base lists (one of each per store); a customer
// group's base_price_kind picks which base list its customers fall back to.
const (
	KindRetail    = "retail"
	KindWholesale = "wholesale"
	KindGroup     = "group"
	KindCustomer  = "customer"
	KindSeason    = "season"
)

// isBaseKind reports a base list kind.
func isBaseKind(k string) bool { return k == KindRetail || k == KindWholesale }

func validListKind(k string) bool {
	switch k {
	case KindRetail, KindWholesale, KindGroup, KindCustomer, KindSeason:
		return true
	}
	return false
}

// Price source kinds: which level of the resolver chain gave a line its price.
const (
	SourceCustomerList = "customer_list"
	SourceGroupList    = "group_list"
	SourceSeasonList   = "season_list"
	SourceBaseList     = "base_list"
	// SourceManual is a price typed on the line, or no list price at all (price 0, the screen
	// asks for one).
	SourceManual = "manual"
)

// Price change sources: the screen a price was edited from (price_changes.source).
const (
	ChangeCatalog       = "catalog"
	ChangePurchaseOrder = "purchase_order"
	ChangeProductList   = "product_list"
	ChangeInventory     = "inventory"
	ChangePriceList     = "price_list"
)

func validChangeSource(s string) bool {
	switch s {
	case ChangeCatalog, ChangePurchaseOrder, ChangeProductList, ChangeInventory, ChangePriceList:
		return true
	}
	return false
}

// PriceList is a price list.
type PriceList struct {
	ID              uuid.UUID  `db:"id"`
	Name            string     `db:"name"`
	NameKey         string     `db:"name_key"`
	Kind            string     `db:"kind"`
	CustomerGroupID *uuid.UUID `db:"customer_group_id"`
	PartyID         *uuid.UUID `db:"party_id"`
	CampaignID      *uuid.UUID `db:"campaign_id"`
	Priority        int        `db:"priority"`
	ValidFrom       *time.Time `db:"valid_from"`
	ValidTo         *time.Time `db:"valid_to"`
	Active          bool       `db:"active"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	Version         int        `db:"version"`
	// ItemCount is how many price rows the list has (list views only).
	ItemCount int `db:"item_count"`
}

// PriceListItem is one price row of a list: a SKU, optionally in one color and one unit, from a
// minimum base quantity.
type PriceListItem struct {
	ID          uuid.UUID `db:"id"`
	PriceListID uuid.UUID `db:"price_list_id"`
	SkuID       uuid.UUID `db:"sku_id"`
	// ColorID nil is the general price of the SKU; set, the price of that color.
	ColorID *uuid.UUID `db:"color_id"`
	// UnitID nil is the base unit.
	UnitID *uuid.UUID `db:"unit_id"`
	// MinQty is the tier threshold in base quantity.
	MinQty    money.Qty   `db:"min_qty"`
	Price     money.Money `db:"price"`
	CreatedAt time.Time   `db:"created_at"`
	UpdatedAt time.Time   `db:"updated_at"`
	// Display fields joined from the catalog.
	SkuCode   string  `db:"sku_code"`
	SkuName   string  `db:"sku_name"`
	ColorName *string `db:"color_name"`
	UnitName  *string `db:"unit_name"`
}

// PriceChange is one row of the append-only price history. OldPrice nil is a price that was
// added; NewPrice nil is a price that was removed ("Bỏ giá riêng").
type PriceChange struct {
	ID            uuid.UUID    `db:"id"`
	PriceListID   uuid.UUID    `db:"price_list_id"`
	PriceListName string       `db:"price_list_name"`
	PriceListKind string       `db:"price_list_kind"`
	SkuID         uuid.UUID    `db:"sku_id"`
	SkuCode       string       `db:"sku_code"`
	SkuName       string       `db:"sku_name"`
	ColorID       *uuid.UUID   `db:"color_id"`
	ColorName     *string      `db:"color_name"`
	UnitID        *uuid.UUID   `db:"unit_id"`
	UnitName      *string      `db:"unit_name"`
	MinQty        money.Qty    `db:"min_qty"`
	OldPrice      *money.Money `db:"old_price"`
	NewPrice      *money.Money `db:"new_price"`
	Source        string       `db:"source"`
	SourceDocID   *uuid.UUID   `db:"source_doc_id"`
	ActorID       uuid.UUID    `db:"actor_id"`
	// ActorName is the user's full name; nil when the actor is not a user of the tenant.
	ActorName *string   `db:"actor_name"`
	CreatedAt time.Time `db:"created_at"`
}

// WholeProductLabel names a history row of the general price, as the history screen shows it.
const WholeProductLabel = "cả sản phẩm"

// ScopeLabel is what the change applies to: the color name, or "cả sản phẩm".
func (c PriceChange) ScopeLabel() string {
	if c.ColorID == nil || c.ColorName == nil {
		return WholeProductLabel
	}
	return *c.ColorName
}

// Promotion kinds.
const (
	PromoLinePercent  = "line_percent"
	PromoLineAmount   = "line_amount"
	PromoOrderPercent = "order_percent"
	PromoOrderAmount  = "order_amount"
	PromoGift         = "gift"
	PromoComboPrice   = "combo_price"
)

func validPromoKind(k string) bool {
	switch k {
	case PromoLinePercent, PromoLineAmount, PromoOrderPercent, PromoOrderAmount, PromoGift, PromoComboPrice:
		return true
	}
	return false
}

// Promotion statuses.
const (
	PromoDraft    = "draft"
	PromoActive   = "active"
	PromoInactive = "inactive"
)

// Promotion item roles.
const (
	RoleEligible       = "eligible"
	RoleGift           = "gift"
	RoleComboComponent = "combo_component"
)

// Document types a promotion usage is recorded against.
const (
	DocInvoice       = "invoice"
	DocSalesDocument = "sales_document"
)

// Promotion is a promotion with its items.
type Promotion struct {
	ID              uuid.UUID    `db:"id"`
	Code            *string      `db:"code"`
	Name            string       `db:"name"`
	Kind            string       `db:"kind"`
	Value           money.Money  `db:"value"`
	MaxDiscount     *money.Money `db:"max_discount"`
	MinOrderAmount  money.Money  `db:"min_order_amount"`
	Stackable       bool         `db:"stackable"`
	CustomerGroupID *uuid.UUID   `db:"customer_group_id"`
	CampaignID      *uuid.UUID   `db:"campaign_id"`
	ValidFrom       time.Time    `db:"valid_from"`
	ValidTo         time.Time    `db:"valid_to"`
	UsageLimit      *int         `db:"usage_limit"`
	Status          string       `db:"status"`
	CreatedAt       time.Time    `db:"created_at"`
	UpdatedAt       time.Time    `db:"updated_at"`
	Version         int          `db:"version"`
	// UsedCount is how many documents used the promotion.
	UsedCount int             `db:"used_count"`
	Items     []PromotionItem `db:"-"`
}

// PromotionItem is a SKU in a promotion: an eligible SKU, a gift or a combo component.
type PromotionItem struct {
	ID          uuid.UUID `db:"id"`
	PromotionID uuid.UUID `db:"promotion_id"`
	Role        string    `db:"role"`
	SkuID       uuid.UUID `db:"sku_id"`
	// Qty is in base units.
	Qty     money.Qty `db:"qty"`
	SkuCode string    `db:"sku_code"`
	SkuName string    `db:"sku_name"`
}

// itemsOf returns the items with role.
func (p Promotion) itemsOf(role string) []PromotionItem {
	var out []PromotionItem
	for _, it := range p.Items {
		if it.Role == role {
			out = append(out, it)
		}
	}
	return out
}

// AppliedPromotion is a promotion applied to a document or line, as snapshotted in the line's
// price source.
type AppliedPromotion struct {
	ID     uuid.UUID   `json:"id"`
	Code   *string     `json:"code"`
	Name   string      `json:"name"`
	Kind   string      `json:"kind"`
	Amount money.Money `json:"amount"`
}

// PriceSource is where a line price came from, snapshotted on the line (price_source jsonb):
// the list level and list, and the color, unit and tier of the row that matched.
type PriceSource struct {
	Kind          string             `json:"kind"`
	PriceListID   *uuid.UUID         `json:"price_list_id"`
	PriceListName *string            `json:"price_list_name"`
	ColorID       *uuid.UUID         `json:"color_id"`
	UnitID        *uuid.UUID         `json:"unit_id"`
	MinQty        *money.Qty         `json:"min_qty"`
	Promotions    []AppliedPromotion `json:"promotions"`
}

// MarshalJSON writes promotions as [] rather than null.
func (s PriceSource) MarshalJSON() ([]byte, error) {
	type plain PriceSource
	p := plain(s)
	if p.Promotions == nil {
		p.Promotions = []AppliedPromotion{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Opt is an optional field of a patch: Set false keeps the stored value; Set with a nil Value
// clears it (JSON null).
type Opt[T any] struct {
	Set   bool
	Value *T
}

// Some returns a set Opt holding v.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: &v} }

// UnmarshalJSON marks the field as given; JSON null clears it.
func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(bytes.TrimSpace(data)) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}
