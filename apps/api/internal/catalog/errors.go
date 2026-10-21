package catalog

import (
	"fmt"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

// errInvalid reports field-level validation failures (field path → reason code).
func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

// Product form messages, shown verbatim by the catalog screens.
const (
	msgCodeAndNameRequired = "Cần mã và tên hàng."
	msgSkuCodeTaken        = "Mã SKU đã tồn tại trong cửa hàng."
	msgBarcodeTaken        = "Mã vạch đã được dùng cho hàng khác."
	msgFactorNotPositive   = "Hệ số quy đổi phải dương."
	msgProductNameTaken    = "Tên hàng đã tồn tại trong cửa hàng."
)

func errCodeAndNameRequired(fields map[string]string) error {
	return apperr.NewValidation("code_and_name_required", msgCodeAndNameRequired, fields)
}

func errSkuCodeTaken() error { return apperr.NewConflict("sku_code_taken", msgSkuCodeTaken) }

func errBarcodeTaken() error { return apperr.NewConflict("barcode_taken", msgBarcodeTaken) }

func errCodeNotFound() error {
	return apperr.NewNotFound("code_not_found", "No active SKU has this barcode or SKU code")
}

func errFactorNotPositive(field string) error {
	return apperr.NewValidation("factor_not_positive", msgFactorNotPositive, map[string]string{field: "not_positive"})
}

func errProductNameTaken() error {
	return apperr.NewConflict("product_name_taken", msgProductNameTaken)
}

func errNameTaken() error {
	return apperr.NewConflict("name_taken", "Another record already uses this name")
}

func errAttributeCodeTaken() error {
	return apperr.NewConflict("attribute_code_taken", "Another attribute already uses this code")
}

func errOptionLabelTaken() error {
	return apperr.NewConflict("option_label_taken", "The attribute already has an option with this label")
}

func errAttributeInUse() error {
	return apperr.NewConflict("attribute_in_use",
		"The attribute is used by products; its code and data type are fixed and it can only be deactivated")
}

func errOptionInUse() error {
	return apperr.NewConflict("option_in_use", "The option is used by products; deactivate it instead")
}

func errCategoryCycle() error {
	return apperr.NewValidation("category_cycle", "A category cannot be moved under itself or one of its children",
		map[string]string{"parent_id": "cycle"})
}

func errCategoryTooDeep() error {
	return apperr.NewValidation("category_too_deep",
		fmt.Sprintf("Categories can be nested at most %d levels deep", MaxCategoryDepth),
		map[string]string{"parent_id": "too_deep"})
}

func errCategoryInUse() error {
	return apperr.NewConflict("category_in_use", "The category has sub-categories or products; deactivate it instead")
}

func errVariantAxesLocked() error {
	return apperr.NewConflict("variant_axes_locked",
		"A variant of this product already has stock movements; its variant axes can no longer change")
}

func errTrackingLocked() error {
	return apperr.NewConflict("tracking_locked",
		"Lot or expiry tracking can only change while the SKU has no stock on hand")
}

func errBaseUnitLocked() error {
	return apperr.NewConflict("base_unit_locked",
		"The SKU already has stock movements; its base unit can no longer change")
}

func errSkuInUse() error {
	return apperr.NewConflict("sku_in_use", "The SKU is used on documents or stock; deactivate it instead")
}

func errProductInUse() error {
	return apperr.NewConflict("product_in_use", "A SKU of the product is used on documents or stock; deactivate it instead")
}

func errUnitInUse() error {
	return apperr.NewConflict("unit_in_use", "The unit has barcodes; remove them first")
}

// errUnitsLocked refuses to delete the units in fields (units[<unit id>] → UnitLockedBy… reason).
func errUnitsLocked(fields map[string]string) error {
	return &apperr.Error{Kind: apperr.Conflict, Code: "unit_in_use", Fields: fields,
		Message: "Documents, price history or barcodes use the unit; make it inactive instead"}
}

func errSkuUnitInUse() error {
	return apperr.NewConflict("sku_unit_in_use",
		"Documents or prices use the SKU in this unit; deactivate the unit instead")
}

func errBaseUnitRequired() error {
	return apperr.NewConflict("base_unit_required", "The base unit of a SKU cannot be removed or deactivated")
}

// apperrNotFound reports whether err is a not-found application error.
func apperrNotFound(err error) bool { return err != nil && apperr.KindOf(err) == apperr.NotFound }

func errProductTypeLocked() error {
	return apperr.NewConflict("product_type_locked", "The product type can no longer change once it has SKUs")
}

// Mix group messages are the prototype's (saveMixGroup), verbatim.
func errMix(code, message, field string) error {
	return apperr.NewValidation(code, message, map[string]string{field: code})
}

func errMixNameRequired() error {
	return errMix("mix_group_name_required", "Cần tên nhóm mix.", "name")
}

func errMixNameTaken() error {
	return apperr.NewConflict("mix_group_name_taken", "Tên nhóm mix đã tồn tại.")
}

func errMixUnitsRequired() error {
	return errMix("mix_group_units_required", "Cần đơn vị thùng và đơn vị chọn vị (VD: thùng / dây).", "pack_unit_id")
}

func errMixPartSize(part string) error {
	return errMix("mix_group_part_size_invalid", fmt.Sprintf("Số đơn vị lẻ trong 1 %s phải là số nguyên dương.", part), "units_per_part")
}

func errMixPackSize(pack string) error {
	return errMix("mix_group_pack_size_invalid", fmt.Sprintf("Số đơn vị lẻ trong 1 %s phải là số nguyên dương.", pack), "units_per_pack")
}

func errMixPackNotDivisible(pack string, packF int, part string, partF int) error {
	return errMix("mix_group_pack_not_divisible",
		fmt.Sprintf("1 %s (%d) phải chia hết cho 1 %s (%d).", pack, packF, part, partF), "units_per_pack")
}

func errMixTooFewParts(pack, part string) error {
	return errMix("mix_group_too_few_parts", fmt.Sprintf("1 %s phải gồm ít nhất 2 %s thì mới mix được.", pack, part), "units_per_pack")
}

func errMixDuplicateFlavor() error {
	return errMix("mix_group_duplicate_flavor", "Một mặt hàng đang được chọn hai lần.", "items")
}

func errMixTooFewFlavors() error {
	return errMix("mix_group_too_few_flavors", "Nhóm mix cần ít nhất 2 vị.", "items")
}

func errMixFlavorNotGoods(name string) error {
	return errMix("mix_group_flavor_not_goods", fmt.Sprintf("%s không phải hàng hóa bán lẻ.", name), "items")
}

func errMixMixedPieceUnits(units []string) error {
	return errMix("mix_group_mixed_piece_units", fmt.Sprintf("Các vị phải cùng đơn vị lẻ, đang lẫn: %s.", strings.Join(units, ", ")), "items")
}

func errMixFlavorLabelRequired(name string) error {
	return errMix("mix_group_flavor_label_required", fmt.Sprintf("Cần tên vị ngắn cho %s.", name), "items")
}

func errMixPackLocked() error {
	return apperr.NewConflict("mix_group_pack_locked",
		"Nhóm đã dùng trên chứng từ nên không đổi được quy cách thùng. Tạo nhóm mới nếu quy cách khác.")
}

func errMixFlavorInUse(names []string) error {
	return apperr.NewConflict("mix_group_flavor_in_use",
		"Không gỡ được vị đã dùng trên chứng từ: "+strings.Join(names, ", ")+".")
}

func errMixGroupInUse(usage []UsageCount) error {
	parts := make([]string, 0, len(usage))
	for _, u := range usage {
		parts = append(parts, fmt.Sprintf("%d %s", u.Count, u.Label))
	}
	return apperr.NewConflict("mix_group_in_use", "Nhóm đã dùng trên "+strings.Join(parts, ", ")+". Chỉ có thể ngừng dùng.")
}

func errMixGroupReferenced() error {
	return apperr.NewConflict("mix_group_in_use", "Nhóm đã dùng trên chứng từ. Chỉ có thể ngừng dùng.")
}
