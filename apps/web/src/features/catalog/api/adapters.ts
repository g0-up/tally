import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { dec } from "../../../shared/lib/decimal";
import { num, qMix, vnd } from "../../../shared/lib/format";
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

/** One line of the "Đơn vị & quy đổi" column. */
export interface UnitConversionLine {
  key: string;
  /** The base unit's name, or "1 thùng = 12 hộp". */
  text: string;
  isBase: boolean;
  /** The unit's own retail price; null sells at base price × factor ("tự tính"). Null on the base line. */
  price: Money | null;
  /** Retired ("Ngừng dùng"): kept for old documents, offered on no new one. */
  inactive: boolean;
}

const byFactor = (a: SkuUnit, b: SkuUnit) => dec(a.factor).comparedTo(dec(b.factor));

/** Prototype `uomCol`: the base unit, then "1 thùng = 12 hộp" with its own price per larger unit, smallest first. */
export function unitConversionLines(sku: Pick<SkuListItem, "units">): UnitConversionLine[] {
  const base = baseUnitName(sku);
  return [...sku.units].sort(byFactor).map((u) => ({
    key: u.unit_id,
    text: u.is_base ? u.unit_name : `1 ${u.unit_name} = ${num(u.factor)} ${base}`,
    isBase: u.is_base,
    price: u.is_base ? null : u.retail_price,
    inactive: !u.active,
  }));
}

/** Active units larger than the base unit, smallest first. */
function largerUnits(sku: Pick<SkuListItem, "units">): SkuUnit[] {
  return sku.units.filter((u) => u.active && !u.is_base && dec(u.factor).gt(1)).sort(byFactor);
}

/** Prototype Tồn kho `unitLine`: " · 1 dây = 50 cái · 1 thùng = 500 cái", empty for a single-unit SKU. */
export function unitFactorLine(sku: Pick<SkuListItem, "units">): string {
  const base = baseUnitName(sku);
  return largerUnits(sku)
    .map((u) => ` · 1 ${u.unit_name} = ${num(u.factor)} ${base}`)
    .join("");
}

/**
 * A base-unit quantity in the SKU's larger units, as the stock card's automatic conversion writes it
 * ("1 thùng 8 bó", remainder in the base unit). Null when the SKU is not loaded, has no larger active
 * unit, or the quantity is below the smallest of them.
 */
export function autoConvertQty(sku: Pick<SkuListItem, "units"> | undefined, qty: Qty | string): string | null {
  if (sku === undefined) return null;
  const larger = largerUnits(sku);
  const smallest = larger[0];
  if (smallest === undefined || dec(qty).lt(smallest.factor)) return null;
  return qMix(
    qty,
    larger.map((u) => ({ name: u.unit_name, factor: u.factor })),
    baseUnitName(sku),
  );
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
