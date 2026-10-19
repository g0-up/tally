package catalog

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxFlavorLabelLen caps the short flavor label ("Dâu", "Có đường").
const MaxFlavorLabelLen = 40

// MaxMixGroupItems bounds the flavors of one mix group.
const MaxMixGroupItems = 50

// MixGroupDraft is a mix group as the owner submits it.
type MixGroupDraft struct {
	Name         string
	PackUnitID   uuid.UUID
	PartUnitID   uuid.UUID
	UnitsPerPack int
	UnitsPerPart int
	Items        []MixGroupItemDraft
}

// MixGroupItemDraft is one requested flavor.
type MixGroupItemDraft struct {
	SkuID       uuid.UUID
	FlavorLabel string
	Active      bool
}

// MixSku is what the policy needs to know about a flavor SKU.
type MixSku struct {
	ID          uuid.UUID
	Name        string
	ProductType string
	BaseUnitID  uuid.UUID
	// FactorOneUnits are the units of the SKU with factor 1 (the base unit included).
	FactorOneUnits []uuid.UUID
}

// MixFacts are the catalog facts a mix group draft is checked against.
type MixFacts struct {
	// NameTaken is set when another mix group already has the draft's name key.
	NameTaken bool
	// UnitNames maps the tenant's unit ids to their names.
	UnitNames map[uuid.UUID]string
	// Skus maps the draft's flavor SKU ids to their facts; a missing id is an unknown SKU.
	Skus map[uuid.UUID]MixSku
}

// MixGroupPolicy validates a mix group: units and pack spec, at least two active flavors that
// are retail goods sharing one piece unit, each with a short label. Messages and their order are
// the prototype's. It returns the shared piece unit.
type MixGroupPolicy struct{}

// Check validates d against facts.
func (MixGroupPolicy) Check(d MixGroupDraft, facts MixFacts) (uuid.UUID, error) {
	if strings.TrimSpace(d.Name) == "" {
		return uuid.Nil, errMixNameRequired()
	}
	if facts.NameTaken {
		return uuid.Nil, errMixNameTaken()
	}
	pack, packOK := facts.UnitNames[d.PackUnitID]
	part, partOK := facts.UnitNames[d.PartUnitID]
	if !packOK || !partOK {
		return uuid.Nil, errMixUnitsRequired()
	}
	if d.UnitsPerPart <= 0 {
		return uuid.Nil, errMixPartSize(part)
	}
	if d.UnitsPerPack <= 0 {
		return uuid.Nil, errMixPackSize(pack)
	}
	if d.UnitsPerPack%d.UnitsPerPart != 0 {
		return uuid.Nil, errMixPackNotDivisible(pack, d.UnitsPerPack, part, d.UnitsPerPart)
	}
	if d.UnitsPerPack/d.UnitsPerPart < 2 {
		return uuid.Nil, errMixTooFewParts(pack, part)
	}
	if len(d.Items) > MaxMixGroupItems {
		return uuid.Nil, errInvalid(map[string]string{"items": "too_many"})
	}
	seen := map[uuid.UUID]bool{}
	active := 0
	for _, it := range d.Items {
		if seen[it.SkuID] {
			return uuid.Nil, errMixDuplicateFlavor()
		}
		seen[it.SkuID] = true
		if it.Active {
			active++
		}
	}
	if active < 2 {
		return uuid.Nil, errMixTooFewFlavors()
	}
	skus := make([]MixSku, 0, len(d.Items))
	for i, it := range d.Items {
		s, ok := facts.Skus[it.SkuID]
		if !ok {
			return uuid.Nil, errInvalid(map[string]string{fmt.Sprintf("items[%d].sku_id", i): "unknown_sku"})
		}
		skus = append(skus, s)
	}
	for _, s := range skus {
		if s.ProductType != ProductGoods {
			return uuid.Nil, errMixFlavorNotGoods(s.Name)
		}
	}
	piece, ok := sharedPieceUnit(skus)
	if !ok {
		var bases []string
		for _, s := range skus {
			name := facts.UnitNames[s.BaseUnitID]
			if !slices.Contains(bases, name) {
				bases = append(bases, name)
			}
		}
		return uuid.Nil, errMixMixedPieceUnits(bases)
	}
	for i, it := range d.Items {
		label := strings.TrimSpace(it.FlavorLabel)
		if label == "" {
			return uuid.Nil, errMixFlavorLabelRequired(skus[i].Name)
		}
		if utf8.RuneCountInString(label) > MaxFlavorLabelLen {
			return uuid.Nil, errInvalid(map[string]string{fmt.Sprintf("items[%d].flavor_label", i): "too_long"})
		}
	}
	return piece, nil
}

// sharedPieceUnit is the unit every flavor counts in with factor 1: the common base unit, or
// else a unit each flavor converts at factor 1.
func sharedPieceUnit(skus []MixSku) (uuid.UUID, bool) {
	if len(skus) == 0 {
		return uuid.Nil, false
	}
	base := skus[0].BaseUnitID
	if !slices.ContainsFunc(skus, func(s MixSku) bool { return s.BaseUnitID != base }) {
		return base, true
	}
	candidates := slices.Clone(skus[0].FactorOneUnits)
	slices.SortFunc(candidates, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, c := range candidates {
		if !slices.ContainsFunc(skus, func(s MixSku) bool { return !slices.Contains(s.FactorOneUnits, c) }) {
			return c, true
		}
	}
	return uuid.Nil, false
}
