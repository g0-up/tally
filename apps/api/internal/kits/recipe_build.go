package kits

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Recipe limits.
const (
	MaxItems   = 100
	MaxFlavors = 50
	maxNoteLen = 2000
)

// FlavorInput is one flavor of a mix pack: Parts parts of the mix group's part unit.
type FlavorInput struct {
	SkuID uuid.UUID
	Parts money.Qty
}

// ItemInput is one component of a recipe. A mix pack names MixGroupID and its Flavors; every
// other kind names SkuID. Qty is in UnitID: the SKU's base unit (the mix group's piece unit for a
// mix pack) when nil. ColorID fixes the color; nil lets the counter or the assembler choose.
type ItemInput struct {
	Kind       string
	SkuID      *uuid.UUID
	UnitID     *uuid.UUID
	Qty        money.Qty
	ColorID    *uuid.UUID
	MixGroupID *uuid.UUID
	Flavors    []FlavorInput
}

// RecipeInput is the composition of a recipe version.
type RecipeInput struct {
	ValidFrom   *time.Time
	ValidTo     *time.Time
	PackingCost money.Money
	Note        *string
	Items       []ItemInput
}

// validateRecipeShape checks what needs no database. prefix is put before the field names
// ("recipe." when the recipe is part of a kit request).
func validateRecipeShape(fields map[string]string, prefix string, in *RecipeInput) {
	switch {
	case len(in.Items) == 0:
		fields[prefix+"items"] = "required"
	case len(in.Items) > MaxItems:
		fields[prefix+"items"] = "too_many"
	}
	if in.PackingCost.IsNegative() {
		fields[prefix+"packing_cost"] = "must_not_be_negative"
	}
	if in.ValidFrom != nil && in.ValidTo != nil && in.ValidTo.Before(*in.ValidFrom) {
		fields[prefix+"valid_to"] = "before_valid_from"
	}
	in.Note = optionalText(fields, prefix+"note", in.Note, maxNoteLen)
	for i, it := range in.Items {
		f := func(name string) string { return itemField(prefix, i, name) }
		if !validKind(it.Kind) {
			fields[f("kind")] = "invalid"
			continue
		}
		if !it.Qty.IsPositive() {
			fields[f("qty")] = "must_be_positive"
		}
		if it.Kind == KindMixPack {
			switch {
			case it.MixGroupID == nil:
				fields[f("mix_group_id")] = "required"
			case it.SkuID != nil:
				fields[f("sku_id")] = "not_allowed_with_mix_group"
			case it.ColorID != nil:
				fields[f("color_id")] = "not_allowed_with_mix_group"
			}
			switch {
			case len(it.Flavors) == 0:
				fields[f("flavors")] = "required"
			case len(it.Flavors) > MaxFlavors:
				fields[f("flavors")] = "too_many"
			}
			for k, fl := range it.Flavors {
				name := fmt.Sprintf("%sflavors.%d.parts", f(""), k)
				if !fl.Parts.IsPositive() || !fl.Parts.IsWhole() {
					fields[name] = "must_be_positive_whole"
				}
			}
			continue
		}
		switch {
		case it.SkuID == nil:
			fields[f("sku_id")] = "required"
		case it.MixGroupID != nil:
			fields[f("mix_group_id")] = "only_for_mix_pack"
		case len(it.Flavors) > 0:
			fields[f("flavors")] = "only_for_mix_pack"
		}
	}
}

// optionalText trims an optional text, checks its length and control characters and turns ""
// into nil.
func optionalText(fields map[string]string, field string, v *string, maxLen int) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	switch {
	case s == "":
		return nil
	case utf8.RuneCountInString(s) > maxLen:
		fields[field] = "too_long"
	case !utf8.ValidString(s) || strings.ContainsFunc(s, isControl):
		fields[field] = "invalid_text"
	}
	return &s
}

// isControl reports control characters other than line breaks and tabs.
func isControl(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f
}

type skuFacts struct {
	ID            uuid.UUID `db:"id"`
	Status        string    `db:"status"`
	BaseUnitID    uuid.UUID `db:"base_unit_id"`
	ProductType   string    `db:"product_type"`
	ProductStatus string    `db:"product_status"`
}

type mixGroupFacts struct {
	ID           uuid.UUID `db:"id"`
	PackUnitID   uuid.UUID `db:"pack_unit_id"`
	UnitsPerPack int       `db:"units_per_pack"`
	PartUnitID   uuid.UUID `db:"part_unit_id"`
	UnitsPerPart int       `db:"units_per_part"`
	PieceUnitID  uuid.UUID `db:"piece_unit_id"`
	Status       string    `db:"status"`
}

type unitKey struct{ sku, unit uuid.UUID }

// recipeFacts is what building a recipe reads: the SKUs, their active units, the active colors
// and the mix groups with their active flavors.
type recipeFacts struct {
	skus    map[uuid.UUID]skuFacts
	units   map[unitKey]money.Factor
	colors  map[uuid.UUID]bool
	groups  map[uuid.UUID]mixGroupFacts
	flavors map[uuid.UUID]map[uuid.UUID]bool
}

func loadRecipeFacts(ctx context.Context, q db.Querier, items []ItemInput) (recipeFacts, error) {
	var skuIDs, colorIDs, groupIDs []uuid.UUID
	for _, it := range items {
		if it.SkuID != nil {
			skuIDs = append(skuIDs, *it.SkuID)
		}
		if it.ColorID != nil {
			colorIDs = append(colorIDs, *it.ColorID)
		}
		if it.MixGroupID != nil {
			groupIDs = append(groupIDs, *it.MixGroupID)
		}
		for _, fl := range it.Flavors {
			skuIDs = append(skuIDs, fl.SkuID)
		}
	}
	f := recipeFacts{skus: map[uuid.UUID]skuFacts{}, units: map[unitKey]money.Factor{}, colors: map[uuid.UUID]bool{},
		groups: map[uuid.UUID]mixGroupFacts{}, flavors: map[uuid.UUID]map[uuid.UUID]bool{}}
	var skus []skuFacts
	if err := sqlx.SelectContext(ctx, q, &skus, `
		SELECT s.id, s.status, s.base_unit_id, p.type AS product_type, p.status AS product_status
		FROM skus s JOIN products p ON p.id = s.product_id WHERE s.id = ANY($1)`, skuIDs); err != nil {
		return f, fmt.Errorf("kits: read component skus: %w", err)
	}
	for _, s := range skus {
		f.skus[s.ID] = s
	}
	var units []struct {
		SkuID  uuid.UUID    `db:"sku_id"`
		UnitID uuid.UUID    `db:"unit_id"`
		Factor money.Factor `db:"factor"`
	}
	if err := sqlx.SelectContext(ctx, q, &units, `
		SELECT sku_id, unit_id, factor FROM sku_units WHERE sku_id = ANY($1) AND active`, skuIDs); err != nil {
		return f, fmt.Errorf("kits: read component units: %w", err)
	}
	for _, u := range units {
		f.units[unitKey{u.SkuID, u.UnitID}] = u.Factor
	}
	var colors []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &colors, `SELECT id FROM colors WHERE id = ANY($1) AND active`, colorIDs); err != nil {
		return f, fmt.Errorf("kits: read component colors: %w", err)
	}
	for _, c := range colors {
		f.colors[c] = true
	}
	var groups []mixGroupFacts
	if err := sqlx.SelectContext(ctx, q, &groups, `
		SELECT id, pack_unit_id, units_per_pack, part_unit_id, units_per_part, piece_unit_id, status
		FROM mix_groups WHERE id = ANY($1)`, groupIDs); err != nil {
		return f, fmt.Errorf("kits: read mix groups: %w", err)
	}
	for _, g := range groups {
		f.groups[g.ID] = g
	}
	var flavors []struct {
		GroupID uuid.UUID `db:"mix_group_id"`
		SkuID   uuid.UUID `db:"sku_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &flavors, `
		SELECT mix_group_id, sku_id FROM mix_group_items WHERE mix_group_id = ANY($1) AND active`, groupIDs); err != nil {
		return f, fmt.Errorf("kits: read mix group flavors: %w", err)
	}
	for _, fl := range flavors {
		if f.flavors[fl.GroupID] == nil {
			f.flavors[fl.GroupID] = map[uuid.UUID]bool{}
		}
		f.flavors[fl.GroupID][fl.SkuID] = true
	}
	return f, nil
}

// componentSku reports why a SKU cannot be a component ("" when it can): it must be an active
// goods or material SKU of an active product, and not the kit itself.
func (f recipeFacts) componentSku(id, outputSkuID uuid.UUID) string {
	s, ok := f.skus[id]
	switch {
	case !ok:
		return "unknown_sku"
	case id == outputSkuID:
		return "self_reference"
	case s.ProductType != "goods" && s.ProductType != "material":
		return "not_a_component"
	case s.Status != ProductActive || s.ProductStatus != ProductActive:
		return "inactive"
	}
	return ""
}

func factorOf(n int) money.Factor {
	f, err := money.NewFactor(strconv.Itoa(n))
	if err != nil {
		return money.FactorOne // n > 0 is a CHECK of mix_groups
	}
	return f
}

// buildRecipe checks a recipe against the catalog and returns its items (in request order,
// numbered from 1) and the stocked components one kit takes. outputSkuID is the kit's SKU
// (uuid.Nil before it exists).
func buildRecipe(ctx context.Context, q db.Querier, outputSkuID uuid.UUID, prefix string, in RecipeInput) ([]RecipeItem, []Component, error) {
	facts, err := loadRecipeFacts(ctx, q, in.Items)
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]string{}
	items := make([]RecipeItem, 0, len(in.Items))
	var comps []Component
	seen := map[string]int{}
	for i, it := range in.Items {
		f := func(name string) string { return itemField(prefix, i, name) }
		ri := RecipeItem{ID: ids.New(), LineNo: i + 1, Kind: it.Kind, Qty: it.Qty, ColorID: it.ColorID, MixGroupID: it.MixGroupID}
		if it.ColorID != nil && !facts.colors[*it.ColorID] {
			fields[f("color_id")] = "unknown_color"
		}
		if it.Kind == KindMixPack {
			flavorComps, ok := buildMixItem(fields, f, facts, &ri, it)
			if ok {
				comps = append(comps, flavorComps...)
			}
			items = append(items, ri)
			continue
		}
		skuID := *it.SkuID
		if reason := facts.componentSku(skuID, outputSkuID); reason != "" {
			fields[f("sku_id")] = reason
			continue
		}
		ri.SkuID = &skuID
		ri.UnitID = facts.skus[skuID].BaseUnitID
		if it.UnitID != nil {
			ri.UnitID = *it.UnitID
		}
		factor, ok := facts.units[unitKey{skuID, ri.UnitID}]
		if !ok {
			fields[f("unit_id")] = "unknown_unit"
			continue
		}
		ri.UnitFactor = factor
		if ri.BaseQty, err = money.BaseQty(it.Qty, factor); err != nil {
			fields[f("qty")] = "too_precise"
			continue
		}
		key := skuID.String()
		if it.ColorID != nil {
			key += "/" + it.ColorID.String()
		}
		if _, dup := seen[key]; dup {
			fields[f("sku_id")] = "duplicate"
			continue
		}
		seen[key] = i
		items = append(items, ri)
		comps = append(comps, Component{ItemID: ri.ID, LineNo: ri.LineNo, Kind: ri.Kind, SkuID: skuID, ColorID: ri.ColorID,
			BaseQty: ri.BaseQty})
	}
	if len(fields) > 0 {
		return nil, nil, errInvalid(fields)
	}
	return items, comps, nil
}

// buildMixItem completes a mix pack item: its unit is the group's pack, part or piece unit, and
// its flavors, in whole parts, must add up to its quantity.
func buildMixItem(fields map[string]string, f func(string) string, facts recipeFacts, ri *RecipeItem, it ItemInput) ([]Component, bool) {
	g, ok := facts.groups[*it.MixGroupID]
	switch {
	case !ok:
		fields[f("mix_group_id")] = "unknown_mix_group"
		return nil, false
	case g.Status != ProductActive:
		fields[f("mix_group_id")] = "inactive"
		return nil, false
	}
	ri.UnitID = g.PieceUnitID
	if it.UnitID != nil {
		ri.UnitID = *it.UnitID
	}
	switch ri.UnitID {
	case g.PackUnitID:
		ri.UnitFactor = factorOf(g.UnitsPerPack)
	case g.PartUnitID:
		ri.UnitFactor = factorOf(g.UnitsPerPart)
	case g.PieceUnitID:
		ri.UnitFactor = money.FactorOne
	default:
		fields[f("unit_id")] = "not_a_mix_group_unit"
		return nil, false
	}
	var err error
	if ri.BaseQty, err = money.BaseQty(it.Qty, ri.UnitFactor); err != nil || !ri.BaseQty.IsWhole() {
		fields[f("qty")] = "not_whole_pieces"
		return nil, false
	}
	perPart := money.NewQtyFromInt(int64(g.UnitsPerPart))
	pieces := money.ZeroQty
	comps := make([]Component, 0, len(it.Flavors))
	seen := map[uuid.UUID]bool{}
	ok = true
	for k, fl := range it.Flavors {
		field := fmt.Sprintf("%sflavors.%d.sku_id", f(""), k)
		switch {
		case !facts.flavors[g.ID][fl.SkuID]:
			fields[field] = "not_a_flavor"
			ok = false
			continue
		case seen[fl.SkuID]:
			fields[field] = "duplicate"
			ok = false
			continue
		}
		seen[fl.SkuID] = true
		flavorPieces, err := decQty(fl.Parts.Decimal().Mul(perPart.Decimal()))
		if err != nil {
			fields[field] = "too_large"
			ok = false
			continue
		}
		pieces = pieces.Add(flavorPieces)
		pieceFactor, found := facts.units[unitKey{fl.SkuID, g.PieceUnitID}]
		if !found {
			fields[field] = "piece_unit_missing"
			ok = false
			continue
		}
		base, err := money.BaseQty(flavorPieces, pieceFactor)
		if err != nil {
			fields[field] = "too_precise"
			ok = false
			continue
		}
		ri.MixItems = append(ri.MixItems, MixItem{ItemID: ri.ID, SkuID: fl.SkuID, Parts: fl.Parts})
		comps = append(comps, Component{ItemID: ri.ID, LineNo: ri.LineNo, Kind: ri.Kind, SkuID: fl.SkuID, BaseQty: base})
	}
	if ok && !pieces.Equal(ri.BaseQty) {
		fields[f("flavors")] = "parts_mismatch"
		ok = false
	}
	return comps, ok
}

// decQty is d as a quantity, rounded half away from zero to the quantity scale.
func decQty(d decimal.Decimal) (money.Qty, error) {
	return money.NewQty(d.Round(4).String())
}
