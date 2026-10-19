package catalog

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Edit rules of the catalog, as Specifications over a UsageReader: what may change once an
// object is used by products, stock or documents.

// AttributeChange describes an attribute update or delete.
type AttributeChange struct {
	CodeChanged     bool
	DataTypeChanged bool
	Delete          bool
}

// AttributeChangeSpec blocks changing the code or data type of an attribute, or deleting it,
// once any product uses it. Renaming, filter/search flags, new options and deactivation are
// always allowed.
type AttributeChangeSpec struct{ Usage UsageReader }

// Check returns attribute_in_use when the change is not allowed.
func (s AttributeChangeSpec) Check(ctx context.Context, q db.Querier, attr Attribute, change AttributeChange) error {
	if !change.CodeChanged && !change.DataTypeChanged && !change.Delete {
		return nil
	}
	used, err := s.Usage.AttributeInUse(ctx, q, attr.ID, attr.Code)
	if err != nil {
		return err
	}
	if used {
		return errAttributeInUse()
	}
	return nil
}

// OptionDeleteSpec blocks deleting an option a product value or SKU variant references;
// relabelling and deactivation are always allowed.
type OptionDeleteSpec struct{ Usage UsageReader }

// Check returns option_in_use when the option is referenced.
func (s OptionDeleteSpec) Check(ctx context.Context, q db.Querier, attr Attribute, optionID uuid.UUID) error {
	used, err := s.Usage.OptionInUse(ctx, q, attr.Code, optionID)
	if err != nil {
		return err
	}
	if used {
		return errOptionInUse()
	}
	return nil
}

// SetItem is one requested item of an attribute set.
type SetItem struct {
	AttributeID uuid.UUID
	IsRequired  bool
	SortOrder   int
}

// MaxAttributeSetItems bounds an attribute set.
const MaxAttributeSetItems = 50

// AttributeSetChangeSpec validates the new items of an attribute set. Removing an attribute
// never deletes the values products store for it: they are hidden, and come back if the
// attribute is added again. It returns the codes of the hidden attributes.
type AttributeSetChangeSpec struct{}

// Check validates items against the known attributes (by id) and the current items.
func (AttributeSetChangeSpec) Check(current []AttributeSetItem, items []SetItem, attrs map[uuid.UUID]Attribute) (hidden []string, err error) {
	if len(items) > MaxAttributeSetItems {
		return nil, errInvalid(map[string]string{"items": "too_many"})
	}
	fields := map[string]string{}
	seen := map[uuid.UUID]bool{}
	wasIn := map[uuid.UUID]bool{}
	for _, it := range current {
		wasIn[it.AttributeID] = true
	}
	for i, it := range items {
		field := fmt.Sprintf("items[%d].attribute_id", i)
		a, ok := attrs[it.AttributeID]
		switch {
		case !ok:
			fields[field] = "unknown_attribute"
		case seen[it.AttributeID]:
			fields[field] = "duplicate"
		case a.Status != StatusActive && !wasIn[it.AttributeID]:
			fields[field] = "attribute_inactive"
		}
		seen[it.AttributeID] = true
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	for _, it := range current {
		if !seen[it.AttributeID] {
			hidden = append(hidden, it.AttributeCode)
		}
	}
	slices.Sort(hidden)
	return hidden, nil
}

// VariantAxisChangeSpec checks the variant axes of a product: at most MaxVariantAxes active
// select attributes, each once; once any SKU of the product has a stock movement the axes are
// locked (adding an option to an axis, which only adds SKUs, stays allowed).
type VariantAxisChangeSpec struct{ Usage UsageReader }

// Check validates axes (attribute ids in position order) against the current axes.
func (s VariantAxisChangeSpec) Check(ctx context.Context, q db.Querier, skuIDs []uuid.UUID, current []VariantAxis,
	axes []uuid.UUID, attrs map[uuid.UUID]Attribute) error {
	if len(axes) > MaxVariantAxes {
		return errInvalid(map[string]string{"axes": "too_many"})
	}
	fields := map[string]string{}
	for i, id := range axes {
		field := fmt.Sprintf("axes[%d].attribute_id", i)
		a, ok := attrs[id]
		switch {
		case !ok:
			fields[field] = "unknown_attribute"
		case slices.Index(axes, id) != i:
			fields[field] = "duplicate"
		case a.DataType != DataSelect:
			fields[field] = "not_select"
		case a.Status != StatusActive:
			fields[field] = "attribute_inactive"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	if sameAxes(current, axes) || len(skuIDs) == 0 {
		return nil
	}
	moved, err := s.Usage.SkusHaveStockMoves(ctx, q, skuIDs)
	if err != nil {
		return err
	}
	if moved {
		return errVariantAxesLocked()
	}
	return nil
}

func sameAxes(current []VariantAxis, axes []uuid.UUID) bool {
	if len(current) != len(axes) {
		return false
	}
	sorted := slices.Clone(current)
	slices.SortFunc(sorted, func(a, b VariantAxis) int { return a.Position - b.Position })
	for i, ax := range sorted {
		if ax.AttributeID != axes[i] {
			return false
		}
	}
	return true
}

// OnHandFunc returns the on-hand quantity of a SKU.
type OnHandFunc func(ctx context.Context, q db.Querier, skuID uuid.UUID) (money.Qty, error)

// SKUChangeSpec blocks deleting a SKU used anywhere, and toggling lot or expiry tracking while
// it has stock on hand. Deactivation is always allowed.
type SKUChangeSpec struct {
	Usage  UsageReader
	OnHand OnHandFunc
}

// CheckDelete returns sku_in_use when the SKU is referenced.
func (s SKUChangeSpec) CheckDelete(ctx context.Context, q db.Querier, skuID uuid.UUID) error {
	used, err := s.Usage.SkuInUse(ctx, q, skuID)
	if err != nil {
		return err
	}
	if used {
		return errSkuInUse()
	}
	return nil
}

// CheckTracking returns tracking_locked when a tracking flag changes while stock is on hand.
func (s SKUChangeSpec) CheckTracking(ctx context.Context, q db.Querier, sku Sku, trackLot, trackExpiry bool) error {
	if sku.TrackLot == trackLot && sku.TrackExpiry == trackExpiry {
		return nil
	}
	onHand, err := s.OnHand(ctx, q, sku.ID)
	if err != nil {
		return err
	}
	if !onHand.IsZero() {
		return errTrackingLocked()
	}
	return nil
}

// UnitConversionSpec blocks changing the base unit of a SKU once it has stock movements;
// conversion factors may change at any time (document lines keep their factor snapshot).
type UnitConversionSpec struct{ Usage UsageReader }

// CheckBaseChange returns base_unit_locked when the SKU has stock movements.
func (s UnitConversionSpec) CheckBaseChange(ctx context.Context, q db.Querier, skuID uuid.UUID) error {
	moved, err := s.Usage.SkusHaveStockMoves(ctx, q, []uuid.UUID{skuID})
	if err != nil {
		return err
	}
	if moved {
		return errBaseUnitLocked()
	}
	return nil
}

// MixGroupChangeSpec blocks, once a mix group is used on a document, changing its pack spec
// and removing a flavor already sold, and deleting it (deactivation stays allowed). Renaming,
// relabelling and adding flavors are always allowed.
type MixGroupChangeSpec struct{ Usage UsageReader }

// CheckUpdate compares the stored group with the requested one; skuName names a flavor SKU.
func (s MixGroupChangeSpec) CheckUpdate(ctx context.Context, q db.Querier, old, next MixGroup, skuName func(uuid.UUID) string) error {
	usage, err := s.Usage.MixGroupUsage(ctx, q, old.ID)
	if err != nil {
		return err
	}
	if len(usage) == 0 {
		return nil
	}
	if old.PackUnitID != next.PackUnitID || old.PartUnitID != next.PartUnitID ||
		old.UnitsPerPack != next.UnitsPerPack || old.UnitsPerPart != next.UnitsPerPart {
		return errMixPackLocked()
	}
	used, err := s.Usage.MixGroupUsedSkus(ctx, q, old.ID)
	if err != nil {
		return err
	}
	var gone []string
	for _, id := range used {
		if !slices.ContainsFunc(next.Items, func(it MixGroupItem) bool { return it.SkuID == id }) {
			gone = append(gone, skuName(id))
		}
	}
	if len(gone) > 0 {
		slices.Sort(gone)
		return errMixFlavorInUse(gone)
	}
	return nil
}

// CheckDelete returns mix_group_in_use with the usage counts when documents use the group.
func (s MixGroupChangeSpec) CheckDelete(ctx context.Context, q db.Querier, id uuid.UUID) error {
	usage, err := s.Usage.MixGroupUsage(ctx, q, id)
	if err != nil {
		return err
	}
	if len(usage) > 0 {
		return errMixGroupInUse(usage)
	}
	return nil
}
