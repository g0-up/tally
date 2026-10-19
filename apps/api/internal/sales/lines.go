package sales

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Line limits of one document.
const (
	maxLines       = 200
	maxSpecLen     = 500
	maxPromoCodes  = 10
	maxNoteLen     = 2000
	maxLabelLen    = 200
	maxFlavorParts = 100
	// maxComponentColors bounds the colors one combo line chooses for its components.
	maxComponentColors = 100
)

// LineInput is a requested line: an item (or combo) by SKU, or a mix pack by mix group with its
// flavor parts. ID names an existing line a revision keeps. A combo line chooses the colors of
// the components its recipe leaves to the counter in ComponentColors.
type LineInput struct {
	ID         *uuid.UUID   `json:"id,omitempty"`
	SkuID      *uuid.UUID   `json:"sku_id,omitempty"`
	ColorID    *uuid.UUID   `json:"color_id,omitempty"`
	UnitID     *uuid.UUID   `json:"unit_id,omitempty"`
	Qty        money.Qty    `json:"qty"`
	UnitPrice  *money.Money `json:"unit_price,omitempty"`
	Discount   money.Money  `json:"discount_amount"`
	SpecText   *string      `json:"spec_text,omitempty"`
	MixGroupID *uuid.UUID   `json:"mix_group_id,omitempty"`
	Parts      []MixPart    `json:"parts,omitempty"`

	ComponentColors []ComponentColor `json:"component_colors,omitempty"`
}

// ComponentColor is the color the counter chose for the components of one SKU of a combo.
type ComponentColor struct {
	SkuID   uuid.UUID `json:"sku_id"`
	ColorID uuid.UUID `json:"color_id"`
}

// MixPart is one flavor of a mix pack: Parts parts ("dây") of a flavor SKU.
type MixPart struct {
	SkuID   uuid.UUID  `json:"sku_id"`
	ColorID *uuid.UUID `json:"color_id,omitempty"`
	Parts   int        `json:"parts"`
}

// DraftInput is the priced content of a document: its lines, order discount and promotion codes.
type DraftInput struct {
	Lines          []LineInput `json:"lines"`
	OrderDiscount  money.Money `json:"order_discount_amount"`
	PromotionCodes []string    `json:"promotion_codes"`
}

// builtDoc is a priced, expanded set of lines ready to store.
type builtDoc struct {
	Lines []Line
	// Origin is the request line each line came from (-1 for a promotion gift).
	Origin     []int
	IncludeTax bool
	Totals     Totals
	// Promotions is what each applied promotion gave the document.
	Promotions map[uuid.UUID]money.Money
}

// lineBuild is the input of buildLines.
type lineBuild struct {
	PartyID *uuid.UUID
	Date    time.Time
	Draft   DraftInput
	// Keep are the existing lines a revision may keep by id (mix pack children found under their
	// header); nil builds every line new.
	Keep map[uuid.UUID]Line
}

// skuInfo is what a line snapshots of a SKU and its product.
type skuInfo struct {
	ID            uuid.UUID `db:"id"`
	Code          string    `db:"code"`
	Name          string    `db:"display_name"`
	Variant       string    `db:"variant_label"`
	BaseUnitID    uuid.UUID `db:"base_unit_id"`
	SpecText      *string   `db:"spec_text"`
	Status        string    `db:"status"`
	ProductStatus string    `db:"product_status"`
	ProductType   string    `db:"product_type"`
}

// unitInfo is a unit a SKU is sold in.
type unitInfo struct {
	SkuID       uuid.UUID    `db:"sku_id"`
	UnitID      uuid.UUID    `db:"unit_id"`
	Name        string       `db:"name"`
	Factor      money.Factor `db:"factor"`
	IsBase      bool         `db:"is_base"`
	DefaultSale bool         `db:"is_default_sale"`
	Active      bool         `db:"active"`
}

// mixGroup is a mix group with its flavors.
type mixGroup struct {
	ID           uuid.UUID `db:"id"`
	Name         string    `db:"name"`
	PackUnitID   uuid.UUID `db:"pack_unit_id"`
	PackUnitName string    `db:"pack_unit_name"`
	UnitsPerPack int       `db:"units_per_pack"`
	UnitsPerPart int       `db:"units_per_part"`
	PieceUnitID  uuid.UUID `db:"piece_unit_id"`
	Status       string    `db:"status"`
	flavors      map[uuid.UUID]bool
}

// catalog is the catalog data a set of lines needs.
type catalog struct {
	skus  map[uuid.UUID]skuInfo
	units map[uuid.UUID][]unitInfo
	mixes map[uuid.UUID]*mixGroup
}

func (c *catalog) unit(skuID uuid.UUID, unitID *uuid.UUID) (unitInfo, bool) {
	var base, sale *unitInfo
	for i, u := range c.units[skuID] {
		if !u.Active {
			continue
		}
		if unitID != nil && u.UnitID == *unitID {
			return u, true
		}
		if u.IsBase {
			base = &c.units[skuID][i]
		}
		if u.DefaultSale {
			sale = &c.units[skuID][i]
		}
	}
	switch {
	case unitID != nil:
		return unitInfo{}, false
	case sale != nil:
		return *sale, true
	case base != nil:
		return *base, true
	}
	return unitInfo{}, false
}

// loadCatalog reads the SKUs, their units and the mix groups named.
func loadCatalog(ctx context.Context, q db.Querier, skuIDs, mixIDs []uuid.UUID) (*catalog, error) {
	c := &catalog{skus: map[uuid.UUID]skuInfo{}, units: map[uuid.UUID][]unitInfo{}, mixes: map[uuid.UUID]*mixGroup{}}
	if err := c.addSkus(ctx, q, skuIDs); err != nil {
		return nil, err
	}
	if len(mixIDs) == 0 {
		return c, nil
	}
	var groups []*mixGroup
	if err := sqlx.SelectContext(ctx, q, &groups, `
		SELECT g.id, g.name, g.pack_unit_id, u.name AS pack_unit_name, g.units_per_pack, g.units_per_part,
		       g.piece_unit_id, g.status
		FROM mix_groups g JOIN units u ON u.id = g.pack_unit_id
		WHERE g.id = ANY($1)`, mixIDs); err != nil {
		return nil, fmt.Errorf("sales: read mix groups: %w", err)
	}
	for _, g := range groups {
		g.flavors = map[uuid.UUID]bool{}
		c.mixes[g.ID] = g
	}
	var items []struct {
		GroupID uuid.UUID `db:"mix_group_id"`
		SkuID   uuid.UUID `db:"sku_id"`
		Active  bool      `db:"active"`
	}
	if err := sqlx.SelectContext(ctx, q, &items, `
		SELECT mix_group_id, sku_id, active FROM mix_group_items WHERE mix_group_id = ANY($1)`, mixIDs); err != nil {
		return nil, fmt.Errorf("sales: read mix group flavors: %w", err)
	}
	for _, it := range items {
		if g := c.mixes[it.GroupID]; g != nil {
			g.flavors[it.SkuID] = it.Active
		}
	}
	return c, nil
}

// addSkus loads the SKUs not loaded yet.
func (c *catalog) addSkus(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) error {
	var missing []uuid.UUID
	for _, id := range skuIDs {
		if _, ok := c.skus[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var skus []skuInfo
	if err := sqlx.SelectContext(ctx, q, &skus, `
		SELECT s.id, s.code, s.display_name, s.variant_label, s.base_unit_id, s.spec_text, s.status,
		       p.status AS product_status, p.type AS product_type
		FROM skus s JOIN products p ON p.id = s.product_id
		WHERE s.id = ANY($1)`, missing); err != nil {
		return fmt.Errorf("sales: read skus: %w", err)
	}
	for _, s := range skus {
		c.skus[s.ID] = s
	}
	var units []unitInfo
	if err := sqlx.SelectContext(ctx, q, &units, `
		SELECT su.sku_id, su.unit_id, u.name, su.factor, su.is_base, su.is_default_sale, su.active
		FROM sku_units su JOIN units u ON u.id = su.unit_id
		WHERE su.sku_id = ANY($1)
		ORDER BY su.sku_id, su.factor, u.name`, missing); err != nil {
		return fmt.Errorf("sales: read sku units: %w", err)
	}
	for _, u := range units {
		c.units[u.SkuID] = append(c.units[u.SkuID], u)
	}
	return nil
}

// sellable checks that a SKU may be sold: it exists, it and its product are active, and it is not
// a material. Combos are sold through the combo expander.
func (c *catalog) sellable(id uuid.UUID, field string) error {
	s, ok := c.skus[id]
	if !ok {
		return errInvalid(map[string]string{field: "not_found"})
	}
	if s.Status != "active" || s.ProductStatus != "active" {
		return apperr.NewValidation(CodeSkuInactive, "The product is not active and cannot be sold", map[string]string{field: CodeSkuInactive})
	}
	if s.ProductType == "material" {
		return apperr.NewValidation(CodeSkuNotSellable, "Materials cannot be sold", map[string]string{field: CodeSkuNotSellable})
	}
	return nil
}

// validateDraft checks the shape of the request lines.
func validateDraft(in DraftInput) error {
	fields := map[string]string{}
	switch {
	case len(in.Lines) == 0:
		fields["lines"] = "required"
	case len(in.Lines) > maxLines:
		fields["lines"] = "too_many"
	}
	if in.OrderDiscount.IsNegative() {
		fields["order_discount_amount"] = "negative"
	}
	if len(in.PromotionCodes) > maxPromoCodes {
		fields["promotion_codes"] = "too_many"
	}
	for i, l := range in.Lines {
		mix := l.MixGroupID != nil
		if (l.SkuID != nil) == mix {
			fields[lineField(i, "sku_id")] = "sku_or_mix_group_required"
		}
		if !l.Qty.IsPositive() {
			fields[lineField(i, "qty")] = "must_be_positive"
		}
		if l.Discount.IsNegative() {
			fields[lineField(i, "discount_amount")] = "negative"
		}
		if l.UnitPrice != nil && l.UnitPrice.IsNegative() {
			fields[lineField(i, "unit_price")] = "negative"
		}
		if l.SpecText != nil && utf8.RuneCountInString(*l.SpecText) > maxSpecLen {
			fields[lineField(i, "spec_text")] = "too_long"
		}
		validateComponentColors(fields, i, l.ComponentColors)
		if !mix {
			if len(l.Parts) > 0 {
				fields[lineField(i, "parts")] = "mix_pack_only"
			}
			continue
		}
		if l.UnitPrice != nil {
			fields[lineField(i, "unit_price")] = "priced_by_flavor"
		}
		if len(l.ComponentColors) > 0 {
			fields[lineField(i, "component_colors")] = "combo_only"
		}
		if l.UnitID != nil || l.ColorID != nil {
			fields[lineField(i, "unit_id")] = "set_by_mix_group"
		}
		if len(l.Parts) == 0 || len(l.Parts) > maxFlavorParts {
			fields[lineField(i, "parts")] = "required"
		}
		for k, p := range l.Parts {
			if p.SkuID == uuid.Nil {
				fields[lineField(i, "parts."+strconv.Itoa(k)+".sku_id")] = "required"
			}
			if p.Parts <= 0 {
				fields[lineField(i, "parts."+strconv.Itoa(k)+".parts")] = "must_be_positive"
			}
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

func validateComponentColors(fields map[string]string, i int, colors []ComponentColor) {
	if len(colors) > maxComponentColors {
		fields[lineField(i, "component_colors")] = "too_many"
	}
	seen := make(map[ComponentColor]bool, len(colors))
	for k, c := range colors {
		f := func(name string) string { return lineField(i, "component_colors."+strconv.Itoa(k)+"."+name) }
		if c.SkuID == uuid.Nil {
			fields[f("sku_id")] = "required"
		}
		if c.ColorID == uuid.Nil {
			fields[f("color_id")] = "required"
		}
		if seen[c] {
			fields[f("sku_id")] = "duplicate"
		}
		seen[c] = true
	}
}

// mergeKey identifies request lines that are one line: same SKU, color, unit, typed price, spec
// and combo component colors. Lines of another color stay apart ("+ Thêm màu khác").
func mergeKey(l LineInput) string {
	var b strings.Builder
	b.WriteString(l.SkuID.String())
	for _, p := range []*uuid.UUID{l.ColorID, l.UnitID} {
		b.WriteByte('|')
		if p != nil {
			b.WriteString(p.String())
		}
	}
	b.WriteByte('|')
	if l.UnitPrice != nil {
		b.WriteString(l.UnitPrice.String())
	}
	b.WriteByte('|')
	if l.SpecText != nil {
		b.WriteString(*l.SpecText)
	}
	for _, c := range l.ComponentColors {
		b.WriteString("|" + c.SkuID.String() + "/" + c.ColorID.String())
	}
	return b.String()
}

// mergeLines merges request item lines of the same SKU, color, unit, price and spec into the first
// of them (quantities and discounts add up). Kept lines and mix packs are never merged. It returns
// the merged lines and the request index of each.
func mergeLines(in []LineInput) ([]LineInput, []int) {
	out := make([]LineInput, 0, len(in))
	origin := make([]int, 0, len(in))
	at := map[string]int{}
	for i, l := range in {
		if l.SkuID == nil || l.ID != nil {
			out = append(out, l)
			origin = append(origin, i)
			continue
		}
		k := mergeKey(l)
		if j, ok := at[k]; ok {
			out[j].Qty = out[j].Qty.Add(l.Qty)
			out[j].Discount = out[j].Discount.Add(l.Discount)
			continue
		}
		at[k] = len(out)
		out = append(out, l)
		origin = append(origin, i)
	}
	return out, origin
}

// draftLine is a line under construction with the pricing draft line that prices it (-1 none).
type draftLine struct {
	line   Line
	origin int
	draft  int
}

// buildLines validates, expands (mix packs, combos) and prices request lines. Every line gets
// its id, kind, snapshot and server-side price; gifts the promotions add come last.
func (s *Service) buildLines(ctx context.Context, q db.Querier, tenantID uuid.UUID, b lineBuild) (builtDoc, error) {
	if err := validateDraft(b.Draft); err != nil {
		return builtDoc{}, err
	}
	reqs, reqOrigin := mergeLines(b.Draft.Lines)
	var skuIDs, mixIDs []uuid.UUID
	for _, r := range reqs {
		if r.SkuID != nil {
			skuIDs = append(skuIDs, *r.SkuID)
		}
		if r.MixGroupID != nil {
			mixIDs = append(mixIDs, *r.MixGroupID)
			for _, p := range r.Parts {
				skuIDs = append(skuIDs, p.SkuID)
			}
		}
	}
	cat, err := loadCatalog(ctx, q, skuIDs, mixIDs)
	if err != nil {
		return builtDoc{}, err
	}
	keepChildren := map[uuid.UUID][]Line{}
	for _, l := range b.Keep {
		if l.ParentLineID != nil {
			keepChildren[*l.ParentLineID] = append(keepChildren[*l.ParentLineID], l)
		}
	}

	var lines []draftLine
	var draft []PriceDraftLine
	var draftOrigin []int
	addDraft := func(dl *draftLine, pl PriceDraftLine) {
		dl.draft = len(draft)
		draft = append(draft, pl)
		draftOrigin = append(draftOrigin, dl.origin)
	}
	for k, r := range reqs {
		i := reqOrigin[k]
		id := ids.New()
		if r.ID != nil {
			if _, ok := b.Keep[*r.ID]; !ok {
				return builtDoc{}, errInvalid(map[string]string{lineField(i, "id"): "not_found"})
			}
			id = *r.ID
		}
		if r.MixGroupID != nil {
			out, err := s.expandMix(cat, r, i, id, keepChildren[id])
			if err != nil {
				return builtDoc{}, err
			}
			for j := range out {
				if out[j].line.Kind == KindMixFlavor {
					fl := out[j].line
					addDraft(&out[j], PriceDraftLine{SkuID: *fl.SkuID, ColorID: fl.ColorID, UnitID: &fl.UnitID, Qty: fl.Qty, Discount: fl.Discount})
				}
			}
			lines = append(lines, out...)
			continue
		}
		field := lineField(i, "sku_id")
		sku, ok := cat.skus[*r.SkuID]
		if !ok {
			return builtDoc{}, errInvalid(map[string]string{field: "not_found"})
		}
		if sku.ProductType != "combo" {
			if err := cat.sellable(sku.ID, field); err != nil {
				return builtDoc{}, err
			}
		}
		u, ok := cat.unit(sku.ID, r.UnitID)
		if !ok {
			return builtDoc{}, errInvalid(map[string]string{lineField(i, "unit_id"): "not_a_unit_of_sku"})
		}
		kind := KindItem
		if sku.ProductType == "combo" {
			kind = KindCombo
		}
		dl := draftLine{origin: i, line: snapshotLine(id, sku, u, kind, r.ColorID, r.SpecText)}
		dl.line.Qty, dl.line.Discount = r.Qty, r.Discount
		addDraft(&dl, PriceDraftLine{SkuID: sku.ID, ColorID: r.ColorID, UnitID: &u.UnitID, Qty: r.Qty, UnitPrice: r.UnitPrice, Discount: r.Discount})
		lines = append(lines, dl)
		if kind != KindCombo {
			if len(r.ComponentColors) > 0 {
				return builtDoc{}, errInvalid(map[string]string{lineField(i, "component_colors"): "combo_only"})
			}
			continue
		}
		base, err := money.BaseQty(r.Qty, u.Factor)
		if err != nil {
			return builtDoc{}, errInvalid(map[string]string{lineField(i, "qty"): "too_precise"})
		}
		parts, err := s.combos.Expand(ctx, q, sku.ID, base)
		if err != nil {
			return builtDoc{}, remapLines(withField(err, lineField(0, "sku_id")), []int{i})
		}
		if err := chooseComponentColors(parts, r.ComponentColors, i); err != nil {
			return builtDoc{}, err
		}
		comps, err := comboComponents(ctx, q, cat, id, i, parts)
		if err != nil {
			return builtDoc{}, err
		}
		lines = append(lines, comps...)
	}

	priced, err := s.pricer.Price(ctx, q, tenantID, PriceDraft{
		PartyID: b.PartyID, BusinessDate: &b.Date, Lines: draft,
		OrderDiscount: b.Draft.OrderDiscount, PromotionCodes: b.Draft.PromotionCodes,
	})
	if err != nil {
		return builtDoc{}, remapLines(err, draftOrigin)
	}
	byDraft := make(map[int]int, len(lines))
	for j, dl := range lines {
		if dl.draft >= 0 && (dl.line.Kind == KindItem || dl.line.Kind == KindCombo || dl.line.Kind == KindMixFlavor) {
			byDraft[dl.draft] = j
		}
	}
	promos := map[uuid.UUID]money.Money{}
	var gifts []PricedLine
	for _, pl := range priced.Lines {
		for _, p := range pl.Promotions {
			promos[p.ID] = promos[p.ID].Add(p.Amount)
		}
		if pl.Gift || pl.DraftIndex < 0 {
			gifts = append(gifts, pl)
			continue
		}
		j, ok := byDraft[pl.DraftIndex]
		if !ok {
			return builtDoc{}, fmt.Errorf("sales: pricing returned an unknown draft line %d", pl.DraftIndex)
		}
		applyPrice(&lines[j].line, pl)
	}
	if len(gifts) > 0 {
		giftIDs := make([]uuid.UUID, len(gifts))
		for k, g := range gifts {
			giftIDs[k] = g.SkuID
		}
		if err := cat.addSkus(ctx, q, giftIDs); err != nil {
			return builtDoc{}, err
		}
		for _, g := range gifts {
			sku, ok := cat.skus[g.SkuID]
			if !ok {
				return builtDoc{}, fmt.Errorf("sales: gift sku %s not found", g.SkuID)
			}
			u, ok := cat.unit(sku.ID, &g.UnitID)
			if !ok {
				return builtDoc{}, fmt.Errorf("sales: gift unit %s is not a unit of sku %s", g.UnitID, g.SkuID)
			}
			dl := draftLine{origin: -1, draft: -1, line: snapshotLine(ids.New(), sku, u, KindGift, g.ColorID, nil)}
			applyPrice(&dl.line, g)
			lines = append(lines, dl)
		}
	}
	finishMixHeaders(lines, priced.PricesIncludeTax)
	if err := checkColorIDs(ctx, q, lines); err != nil {
		return builtDoc{}, err
	}
	out := builtDoc{IncludeTax: priced.PricesIncludeTax, Promotions: promos}
	for j, dl := range lines {
		dl.line.LineNo = j + 1
		out.Lines = append(out.Lines, dl.line)
		out.Origin = append(out.Origin, dl.origin)
	}
	out.Totals = totalsOf(out.Lines, out.IncludeTax)
	return out, nil
}

// chooseComponentColors gives each combo component whose color the recipe leaves open the color
// the request chose for its SKU; the ledger then issues exactly that color. A chosen color equal
// to a fixed color of the SKU only repeats it: a stored combo copied back into a request lists
// the color of every component.
func chooseComponentColors(parts []ComboLine, chosen []ComponentColor, i int) error {
	fixed := map[ComponentColor]bool{}
	open := map[uuid.UUID]int{}
	for k, p := range parts {
		if p.ColorID != nil {
			fixed[ComponentColor{SkuID: p.SkuID, ColorID: *p.ColorID}] = true
		} else {
			open[p.SkuID] = k
		}
	}
	fields := map[string]string{}
	done := map[uuid.UUID]bool{}
	for k, c := range chosen {
		if fixed[c] {
			continue
		}
		field := lineField(i, "component_colors."+strconv.Itoa(k)+".sku_id")
		at, ok := open[c.SkuID]
		switch {
		case !ok:
			fields[field] = "not_a_component"
		case done[c.SkuID]:
			fields[field] = "duplicate"
		default:
			color := c.ColorID
			parts[at].ColorID = &color
			done[c.SkuID] = true
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// withField puts a field-less validation error of a combo on field.
func withField(err error, field string) error {
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.Validation || len(e.Fields) > 0 {
		return err
	}
	out := *e
	out.Fields = map[string]string{field: e.Code}
	return out.WithCause(err)
}

// snapshotLine starts a line with the SKU and unit snapshot.
func snapshotLine(id uuid.UUID, sku skuInfo, u unitInfo, kind string, color *uuid.UUID, spec *string) Line {
	skuID, code := sku.ID, sku.Code
	if spec == nil {
		spec = sku.SpecText
	}
	return Line{
		ID: id, SkuID: &skuID, SkuCode: &code, Name: sku.Name, VariantLabel: sku.Variant,
		UnitID: u.UnitID, UnitName: u.Name, UnitFactor: u.Factor, ColorID: color, SpecText: spec, Kind: kind,
	}
}

// applyPrice copies a priced line onto a line.
func applyPrice(l *Line, pl PricedLine) {
	l.UnitID, l.UnitFactor, l.Qty, l.BaseQty = pl.UnitID, pl.UnitFactor, pl.Qty, pl.BaseQty
	l.ColorID = pl.ColorID
	l.UnitPrice, l.Discount, l.OrderAlloc = pl.UnitPrice, pl.Discount, pl.OrderAlloc
	l.TaxRate, l.Tax, l.LineAmount, l.MinPrice = pl.TaxRate, pl.Tax, pl.LineAmount, pl.MinPrice
	l.PriceSource = withPromotions(pl.Source, pl.Promotions)
}

// withPromotions is the price source of a line with the promotions that gave it something under
// "promotions", so the document can count their use when it is confirmed or sold.
func withPromotions(src json.RawMessage, promos []PromotionShare) RawJSON {
	if len(src) == 0 {
		src = json.RawMessage(`{}`)
	}
	if len(promos) == 0 {
		return RawJSON(src)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(src, &obj); err != nil || obj == nil {
		obj = map[string]json.RawMessage{}
	}
	list, err := json.Marshal(promos)
	if err != nil {
		return RawJSON(src)
	}
	obj["promotions"] = list
	out, err := json.Marshal(obj)
	if err != nil {
		return RawJSON(src)
	}
	return RawJSON(out)
}

// promotionsOf adds up what each promotion gave the lines, from their price sources.
func promotionsOf(lines []Line) map[uuid.UUID]money.Money {
	out := map[uuid.UUID]money.Money{}
	for _, l := range lines {
		var src struct {
			Promotions []PromotionShare `json:"promotions"`
		}
		if len(l.PriceSource) == 0 || json.Unmarshal(l.PriceSource, &src) != nil {
			continue
		}
		for _, p := range src.Promotions {
			out[p.ID] = out[p.ID].Add(p.Amount)
		}
	}
	return out
}

// recordPromotions counts one use of every promotion that gave the lines something, by the
// document docType/docID, in a stable order.
func (s *Service) recordPromotions(ctx context.Context, q db.Querier, tenantID uuid.UUID, docType string, docID uuid.UUID, promos map[uuid.UUID]money.Money) error {
	keys := make([]uuid.UUID, 0, len(promos))
	for id := range promos {
		keys = append(keys, id)
	}
	for _, id := range sortedUnique(keys) {
		if err := s.pricer.RecordUsage(ctx, q, tenantID, id, docType, docID, promos[id]); err != nil {
			return err
		}
	}
	return nil
}

// expandMix turns a mix pack request into its header line and one flavor line per flavor. The
// pack count must be whole and the parts may not exceed what the packs hold; a pack left short is
// sold as chosen, each flavor priced for its own pieces in the group's piece unit. A kept header
// keeps the ids of its flavors.
func (s *Service) expandMix(cat *catalog, r LineInput, i int, headerID uuid.UUID, kept []Line) ([]draftLine, error) {
	field := lineField(i, "mix_group_id")
	g, ok := cat.mixes[*r.MixGroupID]
	if !ok {
		return nil, errInvalid(map[string]string{field: "not_found"})
	}
	if g.Status != "active" {
		return nil, apperr.NewValidation(CodeSkuInactive, "The mix group is not active", map[string]string{field: CodeSkuInactive})
	}
	if !r.Qty.IsWhole() {
		return nil, apperr.NewValidation(CodeMixPackPartial, "Mix packs are sold in whole packs",
			map[string]string{lineField(i, "qty"): CodeMixPackPartial})
	}
	packs := r.Qty.Decimal().IntPart()
	capacity := packs * int64(g.UnitsPerPack/g.UnitsPerPart)
	type flavor struct {
		sku   uuid.UUID
		color *uuid.UUID
		parts int64
	}
	var flavors []flavor
	var total int64
	for k, p := range r.Parts {
		pf := lineField(i, "parts."+strconv.Itoa(k)+".sku_id")
		active, ok := g.flavors[p.SkuID]
		if !ok || !active {
			return nil, errInvalid(map[string]string{pf: "not_a_flavor"})
		}
		if err := cat.sellable(p.SkuID, pf); err != nil {
			return nil, err
		}
		// checked before adding so the running total stays within capacity and cannot overflow
		if int64(p.Parts) > capacity-total {
			return nil, apperr.NewValidation(CodeMixPartsExceedPack, "The flavors add up to more parts than the packs hold",
				map[string]string{lineField(i, "parts"): CodeMixPartsExceedPack})
		}
		total += int64(p.Parts)
		merged := false
		for f := range flavors {
			if flavors[f].sku == p.SkuID && sameUUID(flavors[f].color, p.ColorID) {
				flavors[f].parts += int64(p.Parts)
				merged = true
			}
		}
		if !merged {
			flavors = append(flavors, flavor{sku: p.SkuID, color: p.ColorID, parts: int64(p.Parts)})
		}
	}
	weights := make([]money.Money, len(flavors))
	for f, fl := range flavors {
		weights[f] = money.NewMoneyFromInt(fl.parts)
	}
	discounts, err := money.Allocate(r.Discount, weights)
	if err != nil {
		return nil, errInvalid(map[string]string{lineField(i, "discount_amount"): "invalid"})
	}
	groupID := g.ID
	key := headerID.String()
	header := Line{
		ID: headerID, Name: g.Name, UnitID: g.PackUnitID, UnitName: g.PackUnitName, UnitFactor: money.FactorOne,
		Qty: r.Qty, BaseQty: r.Qty, SpecText: r.SpecText, Kind: KindMixPack, MixKey: &key, MixGroupID: &groupID,
		// The header keeps the part size so a copy can turn the flavor pieces back into parts.
		PriceSource: RawJSON(fmt.Sprintf(`{"kind":"mix_pack","units_per_part":%d}`, g.UnitsPerPart)),
	}
	out := []draftLine{{line: header, origin: i, draft: -1}}
	for f, fl := range flavors {
		u, ok := cat.unit(fl.sku, &g.PieceUnitID)
		if !ok {
			return nil, errInvalid(map[string]string{lineField(i, "parts."+strconv.Itoa(f)+".sku_id"): "piece_unit_missing"})
		}
		id := ids.New()
		for _, k := range kept {
			if k.SkuID != nil && *k.SkuID == fl.sku && sameUUID(k.ColorID, fl.color) {
				id = k.ID
			}
		}
		pieces := money.NewQtyFromInt(fl.parts * int64(g.UnitsPerPart))
		child := snapshotLine(id, cat.skus[fl.sku], u, KindMixFlavor, fl.color, r.SpecText)
		parent := headerID
		child.ParentLineID, child.MixKey, child.MixGroupID = &parent, &key, &groupID
		child.Qty, child.Discount = pieces, discounts[f]
		out = append(out, draftLine{line: child, origin: i})
	}
	return out, nil
}

// comboComponents snapshots the components of a combo line; they carry stock, not price.
func comboComponents(ctx context.Context, q db.Querier, cat *catalog, comboID uuid.UUID, i int, parts []ComboLine) ([]draftLine, error) {
	ids0 := make([]uuid.UUID, len(parts))
	for k, p := range parts {
		ids0[k] = p.SkuID
	}
	if err := cat.addSkus(ctx, q, ids0); err != nil {
		return nil, err
	}
	out := make([]draftLine, 0, len(parts))
	for _, p := range parts {
		sku, ok := cat.skus[p.SkuID]
		if !ok {
			return nil, fmt.Errorf("sales: combo component %s not found", p.SkuID)
		}
		u, ok := cat.unit(sku.ID, &sku.BaseUnitID)
		if !ok {
			return nil, fmt.Errorf("sales: combo component %s has no base unit", p.SkuID)
		}
		l := snapshotLine(ids.New(), sku, u, KindComboComponent, p.ColorID, nil)
		parent := comboID
		l.ParentLineID = &parent
		l.Qty, l.BaseQty = p.BaseQty, p.BaseQty
		l.PriceSource = RawJSON(`{"kind":"combo_component"}`)
		out = append(out, draftLine{line: l, origin: i, draft: -1})
	}
	return out, nil
}

// finishMixHeaders gives each mix pack header the sums of its flavors: amounts, tax and the
// pack price (gross per pack).
func finishMixHeaders(lines []draftLine, includeTax bool) {
	for h := range lines {
		hl := &lines[h].line
		if hl.Kind != KindMixPack {
			continue
		}
		var gross, disc, alloc, tax, amount money.Money
		var rate *money.Percent
		mixedRates := false
		for _, dl := range lines {
			c := dl.line
			if c.ParentLineID == nil || *c.ParentLineID != hl.ID {
				continue
			}
			gross = gross.Add(c.gross(includeTax))
			disc, alloc = disc.Add(c.Discount), alloc.Add(c.OrderAlloc)
			tax, amount = tax.Add(c.Tax), amount.Add(c.LineAmount)
			switch {
			case rate == nil:
				r := c.TaxRate
				rate = &r
			case rate.Cmp(c.TaxRate) != 0:
				mixedRates = true
			}
		}
		hl.Discount, hl.OrderAlloc, hl.Tax, hl.LineAmount = disc, alloc, tax, amount
		if rate != nil && !mixedRates {
			hl.TaxRate = *rate
		}
		if hl.Qty.IsPositive() {
			if p, err := money.RoundMoney(gross.Decimal().Div(hl.Qty.Decimal())); err == nil {
				hl.UnitPrice = p
			}
		}
	}
}

// checkColorIDs rejects colors that do not exist or are inactive.
func checkColorIDs(ctx context.Context, q db.Querier, lines []draftLine) error {
	var want []uuid.UUID
	for _, dl := range lines {
		if dl.line.ColorID != nil {
			want = append(want, *dl.line.ColorID)
		}
	}
	if len(want) == 0 {
		return nil
	}
	var found []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &found, `SELECT id FROM colors WHERE id = ANY($1) AND active`, want); err != nil {
		return fmt.Errorf("sales: read colors: %w", err)
	}
	ok := make(map[uuid.UUID]bool, len(found))
	for _, id := range found {
		ok[id] = true
	}
	fields := map[string]string{}
	for _, dl := range lines {
		if dl.line.ColorID != nil && !ok[*dl.line.ColorID] {
			fields[originField(dl.origin, "color_id")] = "not_found"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

func originField(origin int, name string) string {
	if origin < 0 {
		return "lines"
	}
	return lineField(origin, name)
}

func sameUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// colorPolicy says how a missing color is resolved when stock is checked.
type colorPolicy int

const (
	// colorStrict requires a color on every stocked line of a SKU held in colors.
	colorStrict colorPolicy = iota
	// colorAutoPick takes the only color a SKU has in stock (POS).
	colorAutoPick
)

// applyColors enforces the color requirement on stocked lines: a line of a SKU whose sellable
// stock is held in colors must name one (422 color_required). With colorAutoPick a SKU with stock
// of a single color takes it; a gift always takes the first color with enough stock.
func (s *Service) applyColors(ctx context.Context, q db.Querier, lines []Line, origin []int, policy colorPolicy) error {
	var skuIDs []uuid.UUID
	for _, l := range lines {
		if stocked(l.Kind) && l.SkuID != nil && l.ColorID == nil {
			skuIDs = append(skuIDs, *l.SkuID)
		}
	}
	if len(skuIDs) == 0 {
		return nil
	}
	stock, err := s.stock.OnHandByColor(ctx, q, skuIDs)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	for i := range lines {
		l := &lines[i]
		if !stocked(l.Kind) || l.SkuID == nil || l.ColorID != nil {
			continue
		}
		var colored []ColorStock
		for _, c := range stock[*l.SkuID] {
			if c.ColorID != nil && c.Qty.IsPositive() {
				colored = append(colored, c)
			}
		}
		if len(colored) == 0 {
			continue
		}
		if l.Kind == KindGift {
			for _, c := range colored {
				if c.Qty.Cmp(l.BaseQty) >= 0 {
					l.ColorID = c.ColorID
					break
				}
			}
			if l.ColorID == nil {
				l.ColorID = colored[0].ColorID
			}
			continue
		}
		if policy == colorAutoPick && len(colored) == 1 && !hasUncolored(stock[*l.SkuID]) {
			l.ColorID = colored[0].ColorID
			continue
		}
		name := "color_id"
		if l.Kind == KindComboComponent {
			name = "component_colors"
		}
		fields[originField(origin[i], name)] = CodeColorRequired
	}
	if len(fields) > 0 {
		return apperr.NewValidation(CodeColorRequired, "Choose a color for each colored product", fields)
	}
	return nil
}

func hasUncolored(stock []ColorStock) bool {
	for _, c := range stock {
		if c.ColorID == nil && c.Qty.IsPositive() {
			return true
		}
	}
	return false
}

// lineTable is a table of sales lines: the shared columns plus the table's own.
type lineTable struct {
	name   string
	parent string
	extra  []string
	sel    string
}

// lineColumns are the columns every sales line table shares, in Line field order.
const lineColumns = `id, line_no, sku_id, sku_code_snap, name_snap, variant_label_snap, unit_id, unit_name_snap,
	unit_factor_snap, qty, base_qty, color_id, spec_text, line_kind, parent_line_id, mix_key, mix_group_id,
	unit_price, discount_amount, order_discount_alloc, tax_rate_snap, tax_amount, line_amount, min_price_snap, price_source`

const lineColumnCount = 25

func newLineTable(name, parent string, extra ...string) lineTable {
	cols := lineColumns
	if len(extra) > 0 {
		cols += ", " + strings.Join(extra, ", ")
	}
	return lineTable{
		name: name, parent: parent, extra: extra,
		sel: "SELECT " + cols + " FROM " + name + " WHERE " + parent + " = $1 ORDER BY line_no",
	}
}

var (
	quoteLines    = newLineTable("quote_lines", "quote_id")
	invoiceLines  = newLineTable("invoice_lines", "invoice_id", "reserved_base_qty", "backorder_base_qty", "delivered_base_qty", "cancelled_base_qty")
	documentLines = newLineTable("sales_document_lines", "sales_document_id", "invoice_line_id", "stock_out_line_id", "cogs_amount", "returned_base_qty", "campaign_id")
	cartLines     = newLineTable("pos_cart_lines", "pos_cart_id")
)

func (l Line) values() []any {
	src := []byte(l.PriceSource)
	if len(src) == 0 {
		src = []byte(`{}`)
	}
	return []any{
		l.ID, l.LineNo, l.SkuID, l.SkuCode, l.Name, l.VariantLabel, l.UnitID, l.UnitName,
		l.UnitFactor, l.Qty, l.BaseQty, l.ColorID, l.SpecText, l.Kind, l.ParentLineID, l.MixKey, l.MixGroupID,
		l.UnitPrice, l.Discount, l.OrderAlloc, l.TaxRate, l.Tax, l.LineAmount, l.MinPrice, src,
	}
}

// insert stores lines of one parent; extra holds the values of the table's own columns per line.
func (t lineTable) insert(ctx context.Context, q db.Querier, tenantID, parentID uuid.UUID, lines []Line, extra [][]any) error {
	if len(lines) == 0 {
		return nil
	}
	cols := "tenant_id, " + t.parent + ", " + lineColumns
	if len(t.extra) > 0 {
		cols += ", " + strings.Join(t.extra, ", ")
	}
	per := 2 + lineColumnCount + len(t.extra)
	var b strings.Builder
	args := make([]any, 0, per*len(lines))
	for i, l := range lines {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for c := range per {
			if c > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(i*per + c + 1))
		}
		b.WriteByte(')')
		args = append(args, tenantID, parentID)
		args = append(args, l.values()...)
		if len(t.extra) > 0 {
			if i >= len(extra) || len(extra[i]) != len(t.extra) {
				return fmt.Errorf("sales: %s line %d: want %d extra values", t.name, i, len(t.extra))
			}
			args = append(args, extra[i]...)
		}
	}
	if _, err := q.ExecContext(ctx, "INSERT INTO "+t.name+" ("+cols+") VALUES "+b.String(), args...); err != nil {
		return fmt.Errorf("sales: insert %s: %w", t.name, err)
	}
	return nil
}

// remove deletes the lines of a parent, children before their parents.
func (t lineTable) remove(ctx context.Context, q db.Querier, parentID uuid.UUID) error {
	for _, cond := range []string{" AND parent_line_id IS NOT NULL", ""} {
		if _, err := q.ExecContext(ctx, "DELETE FROM "+t.name+" WHERE "+t.parent+" = $1"+cond, parentID); err != nil {
			return fmt.Errorf("sales: delete %s: %w", t.name, err)
		}
	}
	return nil
}

// load reads the lines of a parent into dst (a pointer to a slice of Line or of a type embedding it).
func (t lineTable) load(ctx context.Context, q db.Querier, dst any, parentID uuid.UUID) error {
	if err := sqlx.SelectContext(ctx, q, dst, t.sel, parentID); err != nil {
		return fmt.Errorf("sales: read %s: %w", t.name, err)
	}
	return nil
}

// plainLines strips the table's own columns of invoice lines.
func plainLines[T interface{ base() Line }](in []T) []Line {
	out := make([]Line, len(in))
	for i, l := range in {
		out[i] = l.base()
	}
	return out
}

func (l InvoiceLine) base() Line  { return l.Line }
func (l DocumentLine) base() Line { return l.Line }
func (l QuoteLine) base() Line    { return l.Line }

// draftOf turns stored lines back into request lines (copy, convert): items with their color,
// unit, typed price and discount; mix packs with their flavors. Gifts and combo components are
// rebuilt by pricing and the expander.
func draftOf(lines []Line) []LineInput {
	var out []LineInput
	children := map[uuid.UUID][]Line{}
	for _, l := range lines {
		if l.ParentLineID != nil {
			children[*l.ParentLineID] = append(children[*l.ParentLineID], l)
		}
	}
	for _, l := range lines {
		switch l.Kind {
		case KindItem, KindCombo:
			unit := l.UnitID
			in := LineInput{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: &unit, Qty: l.Qty, Discount: l.Discount, SpecText: l.SpecText}
			if manualPrice(l.PriceSource) {
				p := l.UnitPrice
				in.UnitPrice = &p
			}
			for _, c := range children[l.ID] {
				if c.SkuID != nil && c.ColorID != nil {
					in.ComponentColors = append(in.ComponentColors, ComponentColor{SkuID: *c.SkuID, ColorID: *c.ColorID})
				}
			}
			out = append(out, in)
		case KindMixPack:
			in := LineInput{MixGroupID: l.MixGroupID, Qty: l.Qty, Discount: l.Discount, SpecText: l.SpecText}
			for _, c := range children[l.ID] {
				if c.SkuID == nil {
					continue
				}
				parts, ok := flavorParts(c, l)
				if !ok {
					continue
				}
				in.Parts = append(in.Parts, MixPart{SkuID: *c.SkuID, ColorID: c.ColorID, Parts: parts})
			}
			out = append(out, in)
		}
	}
	return out
}

// flavorParts is the number of parts a flavor line holds: its pieces over the pieces per part,
// read from the header's packs and the flavors' total.
func flavorParts(child, header Line) (int, bool) {
	var src struct {
		UnitsPerPart int `json:"units_per_part"`
	}
	_ = json.Unmarshal(header.PriceSource, &src)
	if src.UnitsPerPart <= 0 {
		return 0, false
	}
	n := child.Qty.Decimal().Div(decimal.NewFromInt(int64(src.UnitsPerPart)))
	if !n.IsInteger() {
		return 0, false
	}
	return int(n.IntPart()), true
}

// manualPrice reports a price typed on the line rather than resolved from the price lists.
func manualPrice(src RawJSON) bool {
	var ps struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(src, &ps); err != nil {
		return false
	}
	return ps.Kind == "manual"
}
