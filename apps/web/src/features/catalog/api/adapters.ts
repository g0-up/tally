import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import type { ComboItem } from "../../../shared/ui/combobox";
import type { ProductType, SkuListItem, SkuUnit } from "../schemas";

// View helpers over the SKU list DTO: labels, unit conversions and the default purchase unit.

export const PRODUCT_TYPE_LABELS: Readonly<Record<ProductType, string>> = {
  goods: "Hàng hóa",
  material: "Vật tư",
  kit: "Giỏ đóng sẵn",
  combo: "Combo ảo",
};

export const SKU_STATUS_LABELS = { active: "Đang bán", inactive: "Ngừng bán" } as const;

export function baseUnitOf(sku: Pick<SkuListItem, "units">): SkuUnit | undefined {
  return sku.units.find((u) => u.is_base);
}

export function baseUnitName(sku: Pick<SkuListItem, "units">): string {
  return baseUnitOf(sku)?.unit_name ?? "";
}

/** Prototype "Đơn vị & quy đổi": the base unit, then "1 thùng = 12 hộp" per larger unit. */
export function unitConversionLines(sku: Pick<SkuListItem, "units">): string[] {
  const base = baseUnitName(sku);
  return sku.units
    .filter((u) => u.active)
    .sort((a, b) => dec(a.factor).comparedTo(dec(b.factor)))
    .map((u) => (u.is_base ? u.unit_name : `1 ${u.unit_name} = ${num(u.factor)} ${base}`));
}

/** Unit a purchase line starts with: the default purchase unit, else the largest active unit. */
export function defaultPurchaseUnit(sku: Pick<SkuListItem, "units">): SkuUnit | undefined {
  const active = sku.units.filter((u) => u.active);
  return active.find((u) => u.is_default_purchase) ?? [...active].sort((a, b) => dec(b.factor).comparedTo(dec(a.factor)))[0];
}

/** Picker option for a SKU: "SP001 · name" once picked, stock and retail price on the second line. */
export function skuComboItem(s: SkuListItem): ComboItem {
  const price = s.retail_price === null ? "" : ` · ${vnd(s.retail_price)}`;
  return {
    id: s.id,
    name: s.display_name,
    inputLabel: `${s.code} · ${s.display_name}`,
    detail: `${s.code} · có thể bán ${num(s.sellable)} ${baseUnitName(s)}${price}`,
    keywords: s.barcode === null ? [s.code] : [s.code, s.barcode],
  };
}
