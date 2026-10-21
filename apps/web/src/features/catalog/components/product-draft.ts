import type { components } from "../../../shared/api/schema";
import { toMoney, type Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { dec, toDecimalString } from "../../../shared/lib/decimal";
import { useCategories } from "../api/queries";
import { attributeRequest, missingRequired, useSetAttributes, type AttributeValues, type SetAttribute } from "../attribute-values";
import type { SkuDetail, SkuUnit } from "../schemas";

type NewSkuUnit = components["schemas"]["NewSkuUnit"];
type SkuUnitsRequest = components["schemas"]["SkuUnitsRequest"];

// One product form for both entry points: "Thêm hàng hóa" in Danh mục (code, barcode, reference
// cost, min stock, attributes) and "Thêm hàng hóa mới" in Nhập hàng (purchase price instead), each
// with the unit table (prototype `uomTable`): the base unit and its prices, then any number of
// conversion units with their own prices. Error keys are the API field names with the "sku."
// prefix removed; unit rows use "units.<i>.unit|factor|retail|wholesale".

export type ProductFormVariant = "catalog" | "quick";

/** A conversion unit of the unit table. A null (or zero) price sells at base price × factor. */
export interface UnitRow {
  key: string;
  /** Null until a unit is picked, and while a typed new unit is being added to the directory. */
  unitId: string | null;
  unitName: string;
  /** Base units per unit. */
  factor: Qty | null;
  retail: Money | null;
  wholesale: Money | null;
  /** False on a saved unit set to "Ngừng dùng"; a new row is active. */
  active?: boolean;
}

/** The unit table: the base unit with its prices, then the conversion units. */
export interface UnitTable {
  baseUnitId: string | null;
  retailPrice: Money | null;
  wholesalePrice: Money | null;
  units: UnitRow[];
}

export interface ProductDraft extends UnitTable {
  code: string;
  name: string;
  categoryId: string | null;
  barcode: string;
  /** Catalog: reference purchase cost per base unit. Quick: purchase price per base unit. */
  cost: Money | null;
  minStock: Qty | null;
  trackLot: boolean;
  attributes: AttributeValues;
}

export type ProductErrors = Readonly<Record<string, string>>;

export const PRODUCT_MESSAGES = {
  codeAndName: "Cần mã và tên hàng.",
  required: "Bắt buộc nhập.",
} as const;

/** Prototype `unitErrs` and `utCheck` wording. */
/** Unit rows one product may hold besides its base unit; the API refuses more (`units` = `too_many`). */
export const MAX_UNIT_ROWS = 20;

export const UNIT_MESSAGES = {
  base: "Nhập đơn vị cơ sở.",
  unit: "Nhập tên đơn vị hoặc xóa dòng này.",
  isBase: (name: string) => `“${name}” đã là đơn vị cơ sở.`,
  duplicate: (name: string) => `Đơn vị “${name}” bị trùng.`,
  factor: "Hệ số phải lớn hơn 0.",
  wholesale: "Giá sỉ không được cao hơn giá lẻ.",
  more: (n: number) => ` Còn ${n} lỗi khác trong bảng đơn vị.`,
  tooMany: `Một hàng hóa có tối đa ${MAX_UNIT_ROWS} đơn vị quy đổi.`,
} as const;

let lastUnitKey = 0;

/** A new unit row ("+ Thêm đơn vị"): blank unless given. */
export function unitRow(init: Partial<Omit<UnitRow, "key">> = {}): UnitRow {
  return { unitId: null, unitName: "", factor: null, retail: null, wholesale: null, ...init, key: `unit-${++lastUnitKey}` };
}

/** Prototype `utDraft`: hộp with thùng = 12 hộp, prices blank (units picked by name when they exist). */
export function initialUnitTable(unitIdByName: (name: string) => string | null): UnitTable {
  const thung = unitIdByName("thùng");
  return {
    baseUnitId: unitIdByName("hộp"),
    retailPrice: null,
    wholesalePrice: null,
    units: thung === null ? [] : [unitRow({ unitId: thung, unitName: "thùng", factor: "12" as Qty })],
  };
}

/** Prototype draft: the unit table, min 20, lot tracking on. */
export function initialProductDraft(variant: ProductFormVariant, unitIdByName: (name: string) => string | null): ProductDraft {
  return {
    code: "",
    name: "",
    categoryId: null,
    barcode: "",
    ...initialUnitTable(unitIdByName),
    cost: null,
    minStock: variant === "catalog" ? ("20" as Qty) : null,
    trackLot: true,
    attributes: {},
  };
}

const positive = (v: string | null): v is string => v !== null && dec(v).gt(0);

/**
 * Prototype `unitErrs`: the base unit is set; each row names a unit that is neither the base unit
 * nor one named above it, has a positive factor, and a wholesale price not above its own retail one.
 */
export function unitTableErrors(t: UnitTable): Record<string, string> {
  const out: Record<string, string> = {};
  if (t.baseUnitId === null) out.base_unit_id = UNIT_MESSAGES.base;
  if (t.units.length > MAX_UNIT_ROWS) out.units = UNIT_MESSAGES.tooMany;
  const seen = new Set<string>();
  t.units.forEach((u, i) => {
    const at = `units.${i}.`;
    if (u.unitId === null) out[`${at}unit`] = UNIT_MESSAGES.unit;
    else if (u.unitId === t.baseUnitId) out[`${at}unit`] = UNIT_MESSAGES.isBase(u.unitName);
    else if (seen.has(u.unitId)) out[`${at}unit`] = UNIT_MESSAGES.duplicate(u.unitName);
    if (u.unitId !== null) seen.add(u.unitId);
    if (!positive(u.factor)) out[`${at}factor`] = UNIT_MESSAGES.factor;
    if (positive(u.retail) && u.wholesale !== null && dec(u.wholesale).gt(u.retail)) out[`${at}wholesale`] = UNIT_MESSAGES.wholesale;
  });
  return out;
}

const isUnitError = (key: string) => key === "base_unit_id" || key === "units" || key.startsWith("units.");

/** Prototype `utCheck` toast: the first error, plus how many more the unit table holds when it is one of them. */
export function firstProductError(errors: ProductErrors): string | undefined {
  const [first] = Object.entries(errors);
  if (first === undefined) return undefined;
  const rest = Object.keys(errors).filter(isUnitError).length - 1;
  return isUnitError(first[0]) && rest > 0 ? first[1] + UNIT_MESSAGES.more(rest) : first[1];
}

/** The cheap checks the server repeats, with its wording. */
export function validateProductDraft(d: ProductDraft, variant: ProductFormVariant, requiredAttributes: readonly string[] = []): ProductErrors {
  const out: Record<string, string> = {};
  if (variant === "catalog" && (d.code.trim() === "" || d.name.trim() === "")) {
    out.code = PRODUCT_MESSAGES.codeAndName;
    out.name = PRODUCT_MESSAGES.codeAndName;
  }
  if (variant === "quick" && d.name.trim() === "") out.name = PRODUCT_MESSAGES.required;
  Object.assign(out, unitTableErrors(d));
  if (variant === "catalog") {
    for (const code of missingRequired(d.attributes, requiredAttributes)) out[`attributes.${code}`] = PRODUCT_MESSAGES.required;
  }
  return out;
}

/** A unit row's own price, or null when it sells at base price × factor. */
export const ownPrice = (v: Money | null): Money | null => (positive(v) ? v : null);

/** Price of one unit at base price × factor (prototype `utAuto`), rounded to whole dong. */
export function autoPrice(basePrice: Money | null, factor: Qty | null): Money {
  return toMoney(dec(basePrice ?? 0).times(factor ?? 0).toDecimalPlaces(0));
}

/** Rows whose own retail price is below the base unit's: likely a base-unit price typed on a larger unit. Not an error. */
export function unitPriceBelowBase(t: UnitTable): UnitRow[] {
  const base = t.retailPrice;
  if (!positive(base)) return [];
  return t.units.filter((u) => positive(u.retail) && dec(u.retail).lt(base));
}

/** `units` of the create requests (API `NewSkuUnit`); blank prices are left to base price × factor. */
export function unitsRequest(units: readonly UnitRow[]): NewSkuUnit[] {
  return units.flatMap((u) => {
    if (u.unitId === null || u.factor === null) return [];
    const retail = ownPrice(u.retail);
    const wholesale = ownPrice(u.wholesale);
    return [{ unit_id: u.unitId, factor: u.factor, ...(retail === null ? {} : { retail_price: retail }), ...(wholesale === null ? {} : { wholesale_price: wholesale }) }];
  });
}

/** The API's default purchase unit: the row with the largest factor above 1, the first on a tie; undefined for the base unit. */
export function defaultPurchaseRow(units: readonly UnitRow[]): UnitRow | undefined {
  let best: UnitRow | undefined;
  for (const u of units) {
    if (u.unitId !== null && positive(u.factor) && dec(u.factor).gt(best?.factor ?? "1")) best = u;
  }
  return best;
}

function text(v: string): string | null {
  const t = v.trim();
  return t === "" ? null : t;
}

/** `POST /products` body: one goods product with its single SKU and its units. */
export function productRequest(d: ProductDraft, attributes: readonly SetAttribute[]) {
  return {
    type: "goods" as const,
    name: d.name.trim(),
    category_id: d.categoryId,
    attributes: attributeRequest(
      d.attributes,
      attributes.map((a) => a.attribute),
    ),
    sku: {
      code: d.code.trim(),
      base_unit_id: d.baseUnitId,
      track_lot: d.trackLot,
      track_expiry: d.trackLot,
      ref_cost: d.cost,
      ...(d.minStock === null ? {} : { min_stock: d.minStock }),
      barcode: text(d.barcode),
      units: unitsRequest(d.units),
      retail_price: d.retailPrice,
      wholesale_price: d.wholesalePrice,
    },
  };
}

/**
 * `POST /products:quick-create` body. The form asks the purchase price per base unit; the API
 * takes it per purchase unit (it divides by the factor for the reference cost). The purchase unit
 * is the unit with the largest factor above 1, else the base unit, named so both sides agree on it.
 */
export function quickCreateRequest(d: ProductDraft) {
  const purchase = defaultPurchaseRow(d.units);
  const purchasePrice = d.cost === null ? null : purchase?.factor ? toMoney(dec(d.cost).times(purchase.factor)) : d.cost;
  const units = unitsRequest(d.units);
  // With no unit above the base one the base unit is named, since the API would take the largest.
  const purchaseUnitId = purchase?.unitId ?? d.baseUnitId;
  return {
    name: d.name.trim(),
    category_id: d.categoryId,
    base_unit_id: d.baseUnitId,
    units,
    ...(units.length > 0 && purchaseUnitId !== null ? { purchase_unit_id: purchaseUnitId } : {}),
    purchase_price: purchasePrice,
    sale_price: d.retailPrice,
    wholesale_price: d.wholesalePrice,
    track_lot: d.trackLot,
  };
}

/* ---------- "Đơn vị & giá" of a saved SKU ---------- */

export type UnitLock = NonNullable<SkuUnit["locked_by"]>;

/** Why a saved unit can only be deactivated (prototype `editUnits` wording). */
export const UNIT_LOCK_LABELS: Readonly<Record<UnitLock, string>> = {
  documents: "Đã dùng trên chứng từ",
  price_history: "Đã có lịch sử giá",
  barcodes: "Có mã vạch",
};

export const UNIT_EDIT_MESSAGES = {
  unchanged: "Đơn vị và giá không thay đổi.",
  basePrice: "Giá của đơn vị cơ sở không bỏ trống được; nhập giá mới.",
  inUse: (lock: string) => `${lock}, không xóa được; đã chuyển sang Ngừng dùng.`,
} as const;

/** A unit as the SKU has it saved: what the editor's hints compare the table with. */
export interface SavedUnit {
  factor: Qty;
  active: boolean;
  lockedBy: UnitLock | null;
  /** A barcode of the SKU scans to this unit. */
  barcode: boolean;
}

export interface SavedUnits {
  /** Non-base units by unit id. */
  units: ReadonlyMap<string, SavedUnit>;
  /** The saved default purchase unit; the base unit's id when purchases default to it. */
  purchaseUnitId: string | null;
}

/** A row of the table holding a saved unit as it is saved. */
export function savedUnitRow(u: SkuUnit, over: Partial<Omit<UnitRow, "key">> = {}): UnitRow {
  return unitRow({ unitId: u.unit_id, unitName: u.unit_name, factor: u.factor as Qty, retail: u.retail_price, wholesale: u.wholesale_price, active: u.active, ...over });
}

/** The unit table of a saved SKU: the base unit's prices, then every other unit, inactive ones included. */
export function savedUnitTable(sku: SkuDetail): UnitTable {
  const base = sku.units.find((u) => u.is_base);
  return {
    baseUnitId: sku.base_unit_id,
    retailPrice: base?.retail_price ?? null,
    wholesalePrice: base?.wholesale_price ?? null,
    units: sku.units.filter((u) => !u.is_base).map((u) => savedUnitRow(u)),
  };
}

/** The saved state of the SKU's units; `locks` adds the lock a refused save reported for a unit. */
export function savedUnitsOf(sku: SkuDetail, locks: Readonly<Record<string, UnitLock>> = {}): SavedUnits {
  const scanned = new Set(sku.barcodes.map((b) => b.unit_id));
  const units = new Map<string, SavedUnit>();
  for (const u of sku.units) {
    if (u.is_base) continue;
    units.set(u.unit_id, { factor: u.factor as Qty, active: u.active, lockedBy: u.locked_by ?? locks[u.unit_id] ?? null, barcode: scanned.has(u.unit_id) });
  }
  return { units, purchaseUnitId: sku.units.find((u) => u.is_default_purchase)?.unit_id ?? null };
}

/**
 * The row purchases default to once the table is saved, as the API decides it: the saved default
 * while it stays in the table and active, else the active unit with the largest factor above 1 (by
 * name on a tie), else the base unit (undefined).
 */
export function purchaseRowAfterSave(t: UnitTable, savedPurchaseUnitId: string | null): UnitRow | undefined {
  if (savedPurchaseUnitId === null || savedPurchaseUnitId === t.baseUnitId) return undefined;
  const saved = t.units.find((u) => u.unitId === savedPurchaseUnitId);
  if (saved !== undefined && saved.active !== false) return saved;
  const candidates = t.units.flatMap((u) => (u.unitId !== null && u.active !== false && u.factor !== null && positive(u.factor) && dec(u.factor).gt(1) ? [{ row: u, factor: dec(u.factor) }] : []));
  candidates.sort((a, b) => b.factor.cmp(a.factor) || a.row.unitName.localeCompare(b.row.unitName, "vi"));
  return candidates[0]?.row;
}

const canonical = (v: string | null | undefined) => (v === null || v === undefined ? null : toDecimalString(v));

/**
 * `PUT /skus/{id}/units` body: the whole table. A blank unit price is sent as null (sold at base
 * price × factor, removing an own price); a null base price keeps the saved one. With `started`
 * (the table the editor opened with), a base price left as it was is sent as null: a base price
 * saved elsewhere meanwhile does not move the SKU version, so resending it would overwrite that.
 */
export function skuUnitsRequest(t: UnitTable, started?: Pick<UnitTable, "retailPrice" | "wholesalePrice">): SkuUnitsRequest {
  const changed = (now: Money | null, was: Money | null | undefined) => (started !== undefined && canonical(now) === canonical(was) ? null : ownPrice(now));
  return {
    retail_price: changed(t.retailPrice, started?.retailPrice),
    wholesale_price: changed(t.wholesalePrice, started?.wholesalePrice),
    units: t.units.flatMap((u) =>
      u.unitId === null || u.factor === null
        ? []
        : [{ unit_id: u.unitId, factor: u.factor, active: u.active !== false, retail_price: ownPrice(u.retail), wholesale_price: ownPrice(u.wholesale) }],
    ),
  };
}

/** Whether saving `t` would change nothing of the saved table `saved` (decimals compared by value). */
export function sameUnitTable(t: UnitTable, saved: UnitTable): boolean {
  const key = (x: UnitTable) => {
    const r = skuUnitsRequest(x);
    const units = r.units
      .map((u) => [u.unit_id, canonical(u.factor), u.active, canonical(u.retail_price), canonical(u.wholesale_price)] as const)
      .sort((a, b) => a[0].localeCompare(b[0]));
    return JSON.stringify([canonical(r.retail_price), canonical(r.wholesale_price), units]);
  };
  return key(t) === key(saved);
}

/** The unit table checks, plus a saved base price left blank (the API would keep it, not clear it). */
export function editUnitTableErrors(t: UnitTable, saved: UnitTable): Record<string, string> {
  const out = unitTableErrors(t);
  if (ownPrice(t.retailPrice) === null && ownPrice(saved.retailPrice) !== null) out.retail_price = UNIT_EDIT_MESSAGES.basePrice;
  if (ownPrice(t.wholesalePrice) === null && ownPrice(saved.wholesalePrice) !== null) out.wholesale_price = UNIT_EDIT_MESSAGES.basePrice;
  return out;
}

/** Attribute set of the chosen category, for the attribute section and its required checks. */
export function useProductAttributes(categoryId: string | null): SetAttribute[] {
  const categories = useCategories();
  const setId = categories.data?.find((c) => c.id === categoryId)?.attribute_set_id ?? null;
  return useSetAttributes(setId);
}
