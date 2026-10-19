package kits

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the kits endpoints. Money and quantities travel as decimal strings; business
// dates as "YYYY-MM-DD".

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

func dateOf(t time.Time) string { return t.Format(time.DateOnly) }

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateOf(*t)
	return &s
}

// opt is a patch field that may also be cleared: set when given, value nil when given as null.
type opt[T any] struct {
	set   bool
	value *T
}

// UnmarshalJSON marks the field as given; JSON null clears it.
func (o *opt[T]) UnmarshalJSON(data []byte) error {
	o.set = true
	if string(bytes.TrimSpace(data)) == "null" {
		o.value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.value = &v
	return nil
}

// Requests.

type flavorRequest struct {
	SkuID uuid.UUID `json:"sku_id"`
	Parts money.Qty `json:"parts"`
}

type itemRequest struct {
	Kind       string          `json:"kind"`
	SkuID      *uuid.UUID      `json:"sku_id"`
	UnitID     *uuid.UUID      `json:"unit_id"`
	Qty        money.Qty       `json:"qty"`
	ColorID    *uuid.UUID      `json:"color_id"`
	MixGroupID *uuid.UUID      `json:"mix_group_id"`
	Flavors    []flavorRequest `json:"flavors"`
}

func (r itemRequest) input() ItemInput {
	return ItemInput{Kind: r.Kind, SkuID: r.SkuID, UnitID: r.UnitID, Qty: r.Qty, ColorID: r.ColorID, MixGroupID: r.MixGroupID,
		Flavors: mapSlice(r.Flavors, func(f flavorRequest) FlavorInput { return FlavorInput(f) })}
}

type recipeRequest struct {
	ValidFrom   *date         `json:"valid_from"`
	ValidTo     *date         `json:"valid_to"`
	PackingCost money.Money   `json:"packing_cost"`
	Note        *string       `json:"note"`
	Items       []itemRequest `json:"items"`
}

func (r recipeRequest) input() RecipeInput {
	return RecipeInput{ValidFrom: r.ValidFrom.time(), ValidTo: r.ValidTo.time(), PackingCost: r.PackingCost, Note: r.Note,
		Items: mapSlice(r.Items, itemRequest.input)}
}

type kitRequest struct {
	Type           string        `json:"type"`
	Name           string        `json:"name"`
	Description    *string       `json:"description"`
	CategoryID     *uuid.UUID    `json:"category_id"`
	SkuCode        string        `json:"sku_code"`
	BaseUnitID     uuid.UUID     `json:"base_unit_id"`
	TaxRateID      *uuid.UUID    `json:"tax_rate_id"`
	Barcode        *string       `json:"barcode"`
	RetailPrice    *money.Money  `json:"retail_price"`
	WholesalePrice *money.Money  `json:"wholesale_price"`
	MinStock       *money.Qty    `json:"min_stock"`
	Recipe         recipeRequest `json:"recipe"`
}

func (r kitRequest) input() KitInput {
	return KitInput{Type: r.Type, Name: r.Name, Description: r.Description, CategoryID: r.CategoryID, SkuCode: r.SkuCode,
		BaseUnitID: r.BaseUnitID, TaxRateID: r.TaxRateID, Barcode: r.Barcode, RetailPrice: r.RetailPrice,
		WholesalePrice: r.WholesalePrice, MinStock: r.MinStock, Recipe: r.Recipe.input()}
}

type kitPatchRequest struct {
	Name        *string        `json:"name"`
	Description opt[string]    `json:"description"`
	Recipe      *recipeRequest `json:"recipe"`
}

func (r kitPatchRequest) input() KitPatch {
	p := KitPatch{Name: r.Name, Description: r.Description.value, DescriptionSet: r.Description.set}
	if r.Recipe != nil {
		in := r.Recipe.input()
		p.Recipe = &in
	}
	return p
}

// recipePreviewRequest is an unsaved recipe and the kit price to weigh it against.
type recipePreviewRequest struct {
	recipeRequest
	KitPrice *money.Money `json:"kit_price"`
}

type suggestCandidateRequest struct {
	SkuID   uuid.UUID  `json:"sku_id"`
	ColorID *uuid.UUID `json:"color_id"`
	MinQty  money.Qty  `json:"min_qty"`
	MaxQty  *money.Qty `json:"max_qty"`
}

type suggestRequest struct {
	Budget      money.Money               `json:"budget"`
	PackingCost money.Money               `json:"packing_cost"`
	Candidates  []suggestCandidateRequest `json:"candidates"`
}

func (r suggestRequest) input() SuggestInput {
	return SuggestInput{Budget: r.Budget, PackingCost: r.PackingCost,
		Candidates: mapSlice(r.Candidates, func(c suggestCandidateRequest) SuggestCandidate { return SuggestCandidate(c) })}
}

type componentColorRequest struct {
	SkuID   uuid.UUID `json:"sku_id"`
	ColorID uuid.UUID `json:"color_id"`
}

type orderRequest struct {
	KitID       uuid.UUID               `json:"kit_id"`
	PlannedQty  money.Qty               `json:"planned_qty"`
	PlannedDate *date                   `json:"planned_date"`
	CampaignID  *uuid.UUID              `json:"campaign_id"`
	InvoiceID   *uuid.UUID              `json:"invoice_id"`
	Note        *string                 `json:"note"`
	Colors      []componentColorRequest `json:"colors"`
}

func (r orderRequest) input() OrderInput {
	return OrderInput{KitID: r.KitID, PlannedQty: r.PlannedQty, PlannedDate: r.PlannedDate.time(), CampaignID: r.CampaignID,
		InvoiceID: r.InvoiceID, Note: r.Note,
		Colors: mapSlice(r.Colors, func(c componentColorRequest) ComponentColor { return ComponentColor(c) })}
}

type orderPatchRequest struct {
	PlannedQty  *money.Qty  `json:"planned_qty"`
	PlannedDate opt[date]   `json:"planned_date"`
	Note        opt[string] `json:"note"`
}

func (r orderPatchRequest) input() OrderPatch {
	return OrderPatch{PlannedQty: r.PlannedQty, PlannedDate: r.PlannedDate.value.time(), PlannedDateSet: r.PlannedDate.set,
		Note: r.Note.value, NoteSet: r.Note.set}
}

type substituteRequest struct {
	ItemID        uuid.UUID  `json:"item_id"`
	SkuID         uuid.UUID  `json:"sku_id"`
	ColorID       *uuid.UUID `json:"color_id"`
	Qty           money.Qty  `json:"qty"`
	SubstituteQty money.Qty  `json:"substitute_qty"`
}

type batchLineRequest struct {
	OrderItemID uuid.UUID  `json:"order_item_id"`
	Qty         money.Qty  `json:"qty"`
	ScrapQty    money.Qty  `json:"scrap_qty"`
	ColorID     *uuid.UUID `json:"color_id"`
	LotID       *uuid.UUID `json:"lot_id"`
}

type batchRequest struct {
	ProducedQty  money.Qty          `json:"produced_qty"`
	BusinessDate *date              `json:"business_date"`
	Lines        []batchLineRequest `json:"lines"`
}

func (r batchRequest) input() BatchInput {
	return BatchInput{ProducedQty: r.ProducedQty, BusinessDate: r.BusinessDate.time(),
		Lines: mapSlice(r.Lines, func(l batchLineRequest) BatchLine { return BatchLine(l) })}
}

type damagedRequest struct {
	SkuID       uuid.UUID  `json:"sku_id"`
	SourceLotID *uuid.UUID `json:"source_lot_id"`
	Qty         money.Qty  `json:"qty"`
}

type disassemblyRequest struct {
	KitLotID     uuid.UUID        `json:"kit_lot_id"`
	Qty          money.Qty        `json:"qty"`
	ReasonID     uuid.UUID        `json:"reason_id"`
	BusinessDate *date            `json:"business_date"`
	Damaged      []damagedRequest `json:"damaged"`
}

func (r disassemblyRequest) input() DisassemblyInput {
	return DisassemblyInput{KitLotID: r.KitLotID, Qty: r.Qty, ReasonID: r.ReasonID, BusinessDate: r.BusinessDate.time(),
		Damaged: mapSlice(r.Damaged, func(d damagedRequest) DamagedInput { return DamagedInput(d) })}
}

// Responses.

type mixItemDTO struct {
	SkuID uuid.UUID `json:"sku_id"`
	Parts money.Qty `json:"parts"`
}

type recipeItemDTO struct {
	ID         uuid.UUID    `json:"id"`
	LineNo     int          `json:"line_no"`
	Kind       string       `json:"kind"`
	SkuID      *uuid.UUID   `json:"sku_id"`
	UnitID     uuid.UUID    `json:"unit_id"`
	Qty        money.Qty    `json:"qty"`
	UnitFactor money.Factor `json:"unit_factor"`
	BaseQty    money.Qty    `json:"base_qty"`
	ColorID    *uuid.UUID   `json:"color_id"`
	MixGroupID *uuid.UUID   `json:"mix_group_id"`
	Flavors    []mixItemDTO `json:"flavors"`
}

func toRecipeItemDTO(it RecipeItem) recipeItemDTO {
	return recipeItemDTO{ID: it.ID, LineNo: it.LineNo, Kind: it.Kind, SkuID: it.SkuID, UnitID: it.UnitID, Qty: it.Qty,
		UnitFactor: it.UnitFactor, BaseQty: it.BaseQty, ColorID: it.ColorID, MixGroupID: it.MixGroupID,
		Flavors: mapSlice(it.MixItems, func(m MixItem) mixItemDTO { return mixItemDTO{SkuID: m.SkuID, Parts: m.Parts} })}
}

type recipeDTO struct {
	ID          uuid.UUID       `json:"id"`
	VersionNo   int             `json:"version_no"`
	Status      string          `json:"status"`
	ValidFrom   *string         `json:"valid_from"`
	ValidTo     *string         `json:"valid_to"`
	PackingCost money.Money     `json:"packing_cost"`
	Note        *string         `json:"note"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Items       []recipeItemDTO `json:"items"`
}

func toRecipeDTO(r Recipe, items []RecipeItem) recipeDTO {
	return recipeDTO{ID: r.ID, VersionNo: r.VersionNo, Status: r.Status, ValidFrom: dateString(r.ValidFrom),
		ValidTo: dateString(r.ValidTo), PackingCost: r.PackingCost, Note: r.Note, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Items: mapSlice(items, toRecipeItemDTO)}
}

type kitDTO struct {
	ProductID     uuid.UUID `json:"product_id"`
	Type          string    `json:"type"`
	Name          string    `json:"name"`
	Description   *string   `json:"description"`
	Status        string    `json:"status"`
	SkuID         uuid.UUID `json:"sku_id"`
	SkuCode       string    `json:"sku_code"`
	BaseUnitID    uuid.UUID `json:"base_unit_id"`
	ActiveVersion *int      `json:"active_version"`
	Sold          bool      `json:"sold"`
	Latest        recipeDTO `json:"latest"`
	Version       int       `json:"version"`
}

func toKitDTO(k Kit) kitDTO {
	return kitDTO{ProductID: k.ProductID, Type: k.Type, Name: k.Name, Description: k.Description, Status: k.Status,
		SkuID: k.SkuID, SkuCode: k.SkuCode, BaseUnitID: k.BaseUnitID, ActiveVersion: k.ActiveVersion, Sold: k.Sold,
		Latest: toRecipeDTO(k.Latest, k.Items), Version: k.Latest.Version}
}

type kitRowDTO struct {
	ProductID     uuid.UUID `json:"product_id"`
	Type          string    `json:"type"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	SkuID         uuid.UUID `json:"sku_id"`
	SkuCode       string    `json:"sku_code"`
	LatestVersion int       `json:"latest_version"`
	LatestStatus  string    `json:"latest_status"`
	ActiveVersion *int      `json:"active_version"`
	UpdatedAt     time.Time `json:"updated_at"`
	Version       int       `json:"version"`
}

func toKitRowDTO(r KitRow) kitRowDTO { return kitRowDTO(r) }

type deleteDTO struct {
	Archived bool `json:"archived"`
}

type componentCostDTO struct {
	ItemID     uuid.UUID    `json:"item_id"`
	LineNo     int          `json:"line_no"`
	Kind       string       `json:"kind"`
	SkuID      uuid.UUID    `json:"sku_id"`
	SkuCode    string       `json:"sku_code"`
	Name       string       `json:"name"`
	UnitID     uuid.UUID    `json:"unit_id"`
	Qty        money.Qty    `json:"qty"`
	BaseQty    money.Qty    `json:"base_qty"`
	ColorID    *uuid.UUID   `json:"color_id"`
	MixGroupID *uuid.UUID   `json:"mix_group_id"`
	UnitPrice  *money.Money `json:"unit_price"`
	SaleAmount money.Money  `json:"sale_amount"`
	UnitCost   money.Money  `json:"unit_cost"`
	Cost       money.Money  `json:"cost"`
	Sellable   money.Qty    `json:"sellable"`
	Buildable  money.Qty    `json:"buildable"`
}

type costViewDTO struct {
	Components     []componentCostDTO `json:"components"`
	ComponentsSale money.Money        `json:"components_sale"`
	ComponentsCost money.Money        `json:"components_cost"`
	PackingCost    money.Money        `json:"packing_cost"`
	TotalCost      money.Money        `json:"total_cost"`
	KitPrice       *money.Money       `json:"kit_price"`
	Margin         *money.Money       `json:"margin"`
	Buildable      money.Qty          `json:"buildable"`
}

func toCostViewDTO(v CostView) costViewDTO {
	return costViewDTO{Components: mapSlice(v.Components, func(c ComponentCost) componentCostDTO { return componentCostDTO(c) }),
		ComponentsSale: v.ComponentsSale, ComponentsCost: v.ComponentsCost, PackingCost: v.PackingCost, TotalCost: v.TotalCost,
		KitPrice: v.KitPrice, Margin: v.Margin, Buildable: v.Buildable}
}

type suggestLineDTO struct {
	SkuID      uuid.UUID   `json:"sku_id"`
	SkuCode    string      `json:"sku_code"`
	Name       string      `json:"name"`
	ColorID    *uuid.UUID  `json:"color_id"`
	Qty        money.Qty   `json:"qty"`
	UnitPrice  money.Money `json:"unit_price"`
	Amount     money.Money `json:"amount"`
	UnitCost   money.Money `json:"unit_cost"`
	Cost       money.Money `json:"cost"`
	UnitMargin money.Money `json:"unit_margin"`
	Sellable   money.Qty   `json:"sellable"`
}

type suggestionDTO struct {
	Budget         money.Money      `json:"budget"`
	Lines          []suggestLineDTO `json:"lines"`
	ExpectedPrice  money.Money      `json:"expected_price"`
	ComponentsCost money.Money      `json:"components_cost"`
	PackingCost    money.Money      `json:"packing_cost"`
	TotalCost      money.Money      `json:"total_cost"`
	Margin         money.Money      `json:"margin"`
	BudgetLeft     money.Money      `json:"budget_left"`
}

func toSuggestionDTO(s Suggestion) suggestionDTO {
	return suggestionDTO{Budget: s.Budget, Lines: mapSlice(s.Lines, func(l SuggestLine) suggestLineDTO { return suggestLineDTO(l) }),
		ExpectedPrice: s.ExpectedPrice, ComponentsCost: s.ComponentsCost, PackingCost: s.PackingCost, TotalCost: s.TotalCost,
		Margin: s.Margin, BudgetLeft: s.BudgetLeft}
}

type progressDTO struct {
	ProductID       uuid.UUID `json:"product_id"`
	Name            string    `json:"name"`
	SkuID           uuid.UUID `json:"sku_id"`
	SkuCode         string    `json:"sku_code"`
	PlannedQty      money.Qty `json:"planned_qty"`
	CompletedQty    money.Qty `json:"completed_qty"`
	DisassembledQty money.Qty `json:"disassembled_qty"`
	OnHand          money.Qty `json:"on_hand"`
	InvoiceDemand   money.Qty `json:"invoice_demand"`
	OpenOrders      int       `json:"open_orders"`
}

func toProgressDTO(p Progress) progressDTO { return progressDTO(p) }

// Assembly orders.

type orderRowDTO struct {
	ID              uuid.UUID  `json:"id"`
	DocNo           string     `json:"doc_no"`
	ProductID       uuid.UUID  `json:"product_id"`
	KitName         string     `json:"kit_name"`
	KitSkuID        uuid.UUID  `json:"kit_sku_id"`
	RecipeVersion   int        `json:"recipe_version"`
	PlannedQty      money.Qty  `json:"planned_qty"`
	CompletedQty    money.Qty  `json:"completed_qty"`
	DisassembledQty money.Qty  `json:"disassembled_qty"`
	Status          string     `json:"status"`
	PlannedDate     *string    `json:"planned_date"`
	CampaignID      *uuid.UUID `json:"campaign_id"`
	InvoiceID       *uuid.UUID `json:"invoice_id"`
	Note            *string    `json:"note"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Version         int        `json:"version"`
}

func toOrderRowDTO(o AssemblyOrder) orderRowDTO {
	return orderRowDTO{ID: o.ID, DocNo: o.DocNo, ProductID: o.ProductID, KitName: o.KitName, KitSkuID: o.KitSkuID,
		RecipeVersion: o.RecipeVersion, PlannedQty: o.PlannedQty, CompletedQty: o.CompletedQty, DisassembledQty: o.DisassembledQty,
		Status: o.Status, PlannedDate: dateString(o.PlannedDate), CampaignID: o.CampaignID, InvoiceID: o.InvoiceID, Note: o.Note,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, Version: o.Version}
}

type orderItemDTO struct {
	ID            uuid.UUID  `json:"id"`
	RecipeItemID  *uuid.UUID `json:"recipe_item_id"`
	SubstitutesID *uuid.UUID `json:"substitutes_item_id"`
	SkuID         uuid.UUID  `json:"sku_id"`
	SkuCode       string     `json:"sku_code"`
	SkuName       string     `json:"sku_name"`
	ColorID       *uuid.UUID `json:"color_id"`
	PlannedQty    money.Qty  `json:"planned_qty"`
	ConsumedQty   money.Qty  `json:"consumed_qty"`
	RemainingQty  money.Qty  `json:"remaining_qty"`
	ReservedQty   money.Qty  `json:"reserved_qty"`
}

func toOrderItemDTO(it OrderItem) orderItemDTO {
	return orderItemDTO{ID: it.ID, RecipeItemID: it.RecipeItemID, SubstitutesID: it.SubstitutesID, SkuID: it.SkuID,
		SkuCode: it.SkuCode, SkuName: it.SkuName, ColorID: it.ColorID, PlannedQty: it.PlannedBaseQty,
		ConsumedQty: it.ConsumedBaseQty, RemainingQty: it.Remaining(), ReservedQty: it.Reserved}
}

type postingItemDTO struct {
	ID          uuid.UUID   `json:"id"`
	OrderItemID uuid.UUID   `json:"order_item_id"`
	SkuID       uuid.UUID   `json:"sku_id"`
	LotID       uuid.UUID   `json:"lot_id"`
	LotNo       string      `json:"lot_no"`
	ColorID     *uuid.UUID  `json:"color_id"`
	ConsumedQty money.Qty   `json:"consumed_qty"`
	ScrapQty    money.Qty   `json:"scrap_qty"`
	UnitCost    money.Money `json:"unit_cost"`
}

type postingDTO struct {
	ID             uuid.UUID        `json:"id"`
	OrderID        uuid.UUID        `json:"order_id"`
	ProducedQty    money.Qty        `json:"produced_qty"`
	OutputLotID    uuid.UUID        `json:"output_lot_id"`
	OutputLotNo    string           `json:"output_lot_no"`
	OutputExpiry   *string          `json:"output_expiry"`
	ComponentsCost money.Money      `json:"components_cost"`
	PackingCost    money.Money      `json:"packing_cost"`
	OutputUnitCost money.Money      `json:"output_unit_cost"`
	BusinessDate   string           `json:"business_date"`
	PostedAt       time.Time        `json:"posted_at"`
	PostedBy       uuid.UUID        `json:"posted_by"`
	Items          []postingItemDTO `json:"items"`
}

func toPostingDTO(p AssemblyPosting) postingDTO {
	return postingDTO{ID: p.ID, OrderID: p.OrderID, ProducedQty: p.ProducedQty, OutputLotID: p.OutputLotID,
		OutputLotNo: p.OutputLotNo, OutputExpiry: dateString(p.OutputExpiry), ComponentsCost: p.ComponentsCost,
		PackingCost: p.PackingCost, OutputUnitCost: p.OutputUnitCost, BusinessDate: dateOf(p.BusinessDate), PostedAt: p.PostedAt,
		PostedBy: p.PostedBy, Items: mapSlice(p.Items, func(it PostingItem) postingItemDTO { return postingItemDTO(it) })}
}

type orderDetailDTO struct {
	orderRowDTO
	Items          []orderItemDTO `json:"items"`
	Postings       []postingDTO   `json:"postings"`
	ComponentsCost money.Money    `json:"components_cost"`
	PackingCost    money.Money    `json:"packing_cost"`
	ExpectedCost   money.Money    `json:"expected_cost"`
}

func orderDTO(d OrderDetail) orderDetailDTO {
	return orderDetailDTO{orderRowDTO: toOrderRowDTO(d.AssemblyOrder), Items: mapSlice(d.Items, toOrderItemDTO),
		Postings: mapSlice(d.Postings, toPostingDTO), ComponentsCost: d.ComponentsCost, PackingCost: d.PackingCost,
		ExpectedCost: d.ExpectedCost}
}

type batchResultDTO struct {
	Posting postingDTO     `json:"posting"`
	Order   orderDetailDTO `json:"order"`
}

func batchDTO(r BatchResult) batchResultDTO {
	return batchResultDTO{Posting: toPostingDTO(r.Posting), Order: orderDTO(r.Order)}
}

type previewLineDTO struct {
	SkuID     uuid.UUID   `json:"sku_id"`
	SkuCode   string      `json:"sku_code"`
	Name      string      `json:"name"`
	ColorID   *uuid.UUID  `json:"color_id"`
	PerKit    money.Qty   `json:"per_kit"`
	Required  money.Qty   `json:"required"`
	Available money.Qty   `json:"available"`
	Shortage  money.Qty   `json:"shortage"`
	UnitCost  money.Money `json:"unit_cost"`
	Cost      money.Money `json:"cost"`
}

type orderPreviewDTO struct {
	KitID          uuid.UUID        `json:"kit_id"`
	RecipeVersion  int              `json:"recipe_version"`
	PlannedQty     money.Qty        `json:"planned_qty"`
	Lines          []previewLineDTO `json:"lines"`
	Buildable      money.Qty        `json:"buildable"`
	ComponentsCost money.Money      `json:"components_cost"`
	PackingCost    money.Money      `json:"packing_cost"`
	ExpectedCost   money.Money      `json:"expected_cost"`
}

func toOrderPreviewDTO(p OrderPreview) orderPreviewDTO {
	return orderPreviewDTO{KitID: p.KitID, RecipeVersion: p.RecipeVersion, PlannedQty: p.PlannedQty,
		Lines: mapSlice(p.Lines, func(l PreviewLine) previewLineDTO { return previewLineDTO(l) }), Buildable: p.Buildable,
		ComponentsCost: p.ComponentsCost, PackingCost: p.PackingCost, ExpectedCost: p.ExpectedCost}
}

// Disassemblies and traceability.

type disassemblyLineDTO struct {
	ID          uuid.UUID   `json:"id"`
	SkuID       uuid.UUID   `json:"sku_id"`
	SourceLotID uuid.UUID   `json:"source_lot_id"`
	LotNo       string      `json:"lot_no"`
	ColorID     *uuid.UUID  `json:"color_id"`
	ReturnQty   money.Qty   `json:"return_qty"`
	DamagedQty  money.Qty   `json:"damaged_qty"`
	UnitCost    money.Money `json:"unit_cost"`
}

type disassemblyOutDTO struct {
	ID           uuid.UUID            `json:"id"`
	DocNo        string               `json:"doc_no"`
	OrderID      uuid.UUID            `json:"order_id"`
	OrderDocNo   string               `json:"order_doc_no"`
	KitLotID     uuid.UUID            `json:"kit_lot_id"`
	KitLotNo     string               `json:"kit_lot_no"`
	Qty          money.Qty            `json:"qty"`
	ReasonID     uuid.UUID            `json:"reason_id"`
	BusinessDate string               `json:"business_date"`
	PostedAt     time.Time            `json:"posted_at"`
	PostedBy     uuid.UUID            `json:"posted_by"`
	Lines        []disassemblyLineDTO `json:"lines"`
}

func disassemblyDTO(d Disassembly) disassemblyOutDTO {
	return disassemblyOutDTO{ID: d.ID, DocNo: d.DocNo, OrderID: d.OrderID, OrderDocNo: d.OrderDocNo, KitLotID: d.KitLotID,
		KitLotNo: d.KitLotNo, Qty: d.Qty, ReasonID: d.ReasonID, BusinessDate: dateOf(d.BusinessDate), PostedAt: d.PostedAt,
		PostedBy: d.PostedBy, Lines: mapSlice(d.Lines, func(l DisassemblyLine) disassemblyLineDTO { return disassemblyLineDTO(l) })}
}

type genealogyLinkDTO struct {
	ParentLotID uuid.UUID  `json:"parent_lot_id"`
	ParentLotNo string     `json:"parent_lot_no"`
	ChildLotID  uuid.UUID  `json:"child_lot_id"`
	ChildLotNo  string     `json:"child_lot_no"`
	SkuID       uuid.UUID  `json:"sku_id"`
	SkuCode     string     `json:"sku_code"`
	ColorID     *uuid.UUID `json:"color_id"`
	Expiry      *string    `json:"expiry_date"`
	Qty         money.Qty  `json:"qty"`
	PostingID   uuid.UUID  `json:"assembly_posting_id"`
	OrderDocNo  string     `json:"order_doc_no"`
}

func toGenealogyLinkDTO(g GenealogyLink) genealogyLinkDTO {
	return genealogyLinkDTO{ParentLotID: g.ParentLotID, ParentLotNo: g.ParentLotNo, ChildLotID: g.ChildLotID,
		ChildLotNo: g.ChildLotNo, SkuID: g.SkuID, SkuCode: g.SkuCode, ColorID: g.ColorID, Expiry: dateString(g.Expiry), Qty: g.Qty,
		PostingID: g.PostingID, OrderDocNo: g.OrderDocNo}
}

type genealogyDTO struct {
	LotID      uuid.UUID          `json:"lot_id"`
	Components []genealogyLinkDTO `json:"components"`
	UsedIn     []genealogyLinkDTO `json:"used_in"`
}

func toGenealogyDTO(g Genealogy) genealogyDTO {
	return genealogyDTO{LotID: g.LotID, Components: mapSlice(g.Components, toGenealogyLinkDTO),
		UsedIn: mapSlice(g.UsedIn, toGenealogyLinkDTO)}
}

type labelComponentDTO struct {
	SkuID     uuid.UUID  `json:"sku_id"`
	SkuCode   string     `json:"sku_code"`
	Name      string     `json:"name"`
	LotNo     string     `json:"lot_no"`
	ColorID   *uuid.UUID `json:"color_id"`
	ColorName *string    `json:"color_name"`
	Expiry    *string    `json:"expiry_date"`
	QtyPerKit money.Qty  `json:"qty_per_kit"`
}

type labelLotDTO struct {
	LotID      uuid.UUID           `json:"lot_id"`
	LotNo      string              `json:"lot_no"`
	Expiry     *string             `json:"expiry_date"`
	Qty        money.Qty           `json:"qty"`
	Components []labelComponentDTO `json:"components"`
}

type labelDTO struct {
	KitName string        `json:"kit_name"`
	SkuCode string        `json:"sku_code"`
	DocNo   string        `json:"doc_no"`
	Lots    []labelLotDTO `json:"lots"`
}

func toLabelDTO(l Label) labelDTO {
	return labelDTO{KitName: l.KitName, SkuCode: l.SkuCode, DocNo: l.DocNo, Lots: mapSlice(l.Lots, func(lot LabelLot) labelLotDTO {
		return labelLotDTO{LotID: lot.LotID, LotNo: lot.LotNo, Expiry: dateString(lot.Expiry), Qty: lot.Qty,
			Components: mapSlice(lot.Components, func(c LabelComponent) labelComponentDTO {
				return labelComponentDTO{SkuID: c.SkuID, SkuCode: c.SkuCode, Name: c.Name, LotNo: c.LotNo, ColorID: c.ColorID,
					ColorName: c.ColorName, Expiry: dateString(c.Expiry), QtyPerKit: c.QtyPerKit}
			})}
	})}
}
