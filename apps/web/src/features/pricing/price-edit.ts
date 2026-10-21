import { dec } from "../../shared/lib/decimal";
import type { Money } from "../../shared/domain/money";
import type { Qty } from "../../shared/domain/qty";
import { autoPrice } from "../catalog";
import type { PriceChange, PriceChangeSource, PriceListItem } from "./schemas";

// Rules of the sell-price dialog and the price-history table: the prototype `setPrice` checks, the
// requests one save sends, and the grouping of retail and wholesale history rows into one edit.

export const PRICE_MESSAGES = {
  retailPositive: "Giá bán lẻ phải lớn hơn 0.",
  wholesaleAboveRetail: "Giá sỉ không được cao hơn giá bán lẻ.",
  unchanged: "Giá không thay đổi.",
  unitPositive: (unit: string) => `Giá theo ${unit} phải lớn hơn 0; bỏ trống để tự tính theo hệ số.`,
  unitWholesaleAboveRetail: (unit: string) => `Giá sỉ / ${unit} không được cao hơn giá bán lẻ / ${unit}.`,
  belowMin: "Giá sỉ thấp hơn giá tối thiểu: hóa đơn dùng giá này sẽ hiện cảnh báo khi xác nhận.",
} as const;

/** "Nơi sửa" of a history row. */
export const PRICE_SOURCE_LABELS: Readonly<Record<PriceChangeSource, string>> = {
  catalog: "Danh mục",
  purchase_order: "Đơn nhập",
  product_list: "Nhập hàng",
  inventory: "Kho",
  price_list: "Bảng giá",
};

export interface PriceDraft {
  retail: Money | null;
  /** Empty keeps the current wholesale price, as in the prototype. */
  wholesale: Money | null;
  /** "Áp dụng cho tất cả màu": also drop every color price. */
  allColors: boolean;
  /** Own prices of the SKU's other units by unit id; a unit left out keeps its prices. */
  units?: Readonly<Record<string, UnitPriceDraft>>;
}

/** Own prices of a non-base unit; null sells it at the base price × factor. */
export interface UnitPriceDraft {
  retail: Money | null;
  wholesale: Money | null;
}

/** A unit of the SKU with its own prices (catalog `SkuUnit`). */
export interface UnitPrices {
  unit_id: string;
  unit_name: string;
  factor: string;
  is_base: boolean;
  active: boolean;
  retail_price: Money | null;
  wholesale_price: Money | null;
}

export interface CurrentPrices {
  retail_price: Money | null;
  wholesale_price: Money | null;
  units?: readonly UnitPrices[];
}

export interface SalePriceRequest {
  kind: "retail" | "wholesale";
  /** Null removes the own price of `unit_id`. */
  price: Money | null;
  /** A non-base unit; absent prices the base unit. */
  unit_id?: string;
  apply_all_colors?: boolean;
  source: PriceChangeSource;
}

const same = (a: Money | null, b: Money | null) => (a === null || b === null ? a === b : dec(a).eq(b));

/** Wholesale price the save writes: the typed one, else the current one. */
export function effectiveWholesale(draft: PriceDraft, current: CurrentPrices): Money | null {
  return draft.wholesale ?? current.wholesale_price;
}

/** True when the wholesale price would be above the retail price (warned live, refused on save). */
export function wholesaleAboveRetail(draft: PriceDraft, current: CurrentPrices): boolean {
  const whole = effectiveWholesale(draft, current);
  return draft.retail !== null && whole !== null && dec(whole).gt(draft.retail);
}

/** True when the wholesale price is below the server's minimum price. */
export function wholesaleBelowMin(draft: PriceDraft, current: CurrentPrices, minPrice: Money | null): boolean {
  const whole = effectiveWholesale(draft, current);
  return minPrice !== null && whole !== null && dec(whole).gt(0) && dec(whole).lt(minPrice);
}

/** Units whose own prices the dialog edits: the active units other than the base one. */
export function priceableUnits(current: CurrentPrices): UnitPrices[] {
  return (current.units ?? []).filter((u) => !u.is_base && u.active);
}

/** The draft of the units' own prices as they are now. */
export function unitPriceDrafts(current: CurrentPrices): Record<string, UnitPriceDraft> {
  return Object.fromEntries(priceableUnits(current).map((u) => [u.unit_id, { retail: u.retail_price, wholesale: u.wholesale_price }]));
}

/** What one unit sells at with the draft: its own price, else the draft's base price × factor. */
export function unitAutoPrices(draft: PriceDraft, current: CurrentPrices, unit: UnitPrices): UnitPriceDraft {
  const factor = unit.factor as Qty;
  return { retail: autoPrice(draft.retail, factor), wholesale: autoPrice(effectiveWholesale(draft, current), factor) };
}

/** Units with a draft whose own prices differ from the current ones. */
function changedUnits(draft: PriceDraft, current: CurrentPrices): { unit: UnitPrices; next: UnitPriceDraft }[] {
  return priceableUnits(current).flatMap((unit) => {
    const next = draft.units?.[unit.unit_id];
    if (next === undefined || (same(next.retail, unit.retail_price) && same(next.wholesale, unit.wholesale_price))) return [];
    return [{ unit, next }];
  });
}

/** The first problem of the units' own prices: a zero price, or an own wholesale above the own retail (as in the unit table). */
function unitPriceError(draft: PriceDraft, current: CurrentPrices): string | null {
  for (const unit of priceableUnits(current)) {
    const next = draft.units?.[unit.unit_id];
    if (next === undefined) continue;
    if ((next.retail !== null && !dec(next.retail).gt(0)) || (next.wholesale !== null && !dec(next.wholesale).gt(0))) {
      return PRICE_MESSAGES.unitPositive(unit.unit_name);
    }
    if (next.retail !== null && next.wholesale !== null && dec(next.wholesale).gt(next.retail)) return PRICE_MESSAGES.unitWholesaleAboveRetail(unit.unit_name);
  }
  return null;
}

/** The prototype `setPrice` checks, in order, then the units' own prices; null when the draft can be saved. */
export function validatePriceDraft(draft: PriceDraft, current: CurrentPrices): string | null {
  if (draft.retail === null || !dec(draft.retail).gt(0)) return PRICE_MESSAGES.retailPositive;
  if (wholesaleAboveRetail(draft, current)) return PRICE_MESSAGES.wholesaleAboveRetail;
  const unitError = unitPriceError(draft, current);
  if (unitError !== null) return unitError;
  const baseSame = same(draft.retail, current.retail_price) && same(effectiveWholesale(draft, current), current.wholesale_price);
  if (!draft.allColors && baseSame && changedUnits(draft, current).length === 0) return PRICE_MESSAGES.unchanged;
  return null;
}

/**
 * `POST /skus/{id}/sale-price` bodies of one save: a request per base list whose general price
 * changes, or both when every color price is dropped, then one per changed own price of a unit
 * (null removes it, so the unit sells at base price × factor). Call only with a draft that validated.
 */
export function salePriceRequests(draft: PriceDraft, current: CurrentPrices, source: PriceChangeSource): SalePriceRequest[] {
  const out: SalePriceRequest[] = [];
  const all = draft.allColors;
  if (draft.retail !== null && (all || !same(draft.retail, current.retail_price))) {
    out.push({ kind: "retail", price: draft.retail, apply_all_colors: all, source });
  }
  const whole = effectiveWholesale(draft, current);
  if (whole !== null && (all || !same(whole, current.wholesale_price))) {
    out.push({ kind: "wholesale", price: whole, apply_all_colors: all, source });
  }
  for (const { unit, next } of changedUnits(draft, current)) {
    if (!same(next.retail, unit.retail_price)) out.push({ kind: "retail", price: next.retail, unit_id: unit.unit_id, source });
    if (!same(next.wholesale, unit.wholesale_price)) out.push({ kind: "wholesale", price: next.wholesale, unit_id: unit.unit_id, source });
  }
  return out;
}

/** Margin of a price over the average cost, "12%"; "—" without a price or a cost. */
export function marginText(price: Money | null, cost: Money | null): string {
  if (price === null || cost === null || !dec(price).gt(0)) return "—";
  return `${dec(price).minus(cost).div(price).times(100).toDecimalPlaces(0).toString()}%`;
}

/** Colors with an own retail or wholesale price, in first-seen order, with their names. */
export function colorPricesOf(items: readonly PriceListItem[]): { colorId: string; name: string }[] {
  const seen = new Map<string, string>();
  for (const it of items) {
    if (it.color_id !== null && !seen.has(it.color_id)) seen.set(it.color_id, it.color_name ?? "Chưa gán");
  }
  return [...seen].map(([colorId, name]) => ({ colorId, name }));
}

export interface PriceMove {
  old: Money | null;
  next: Money | null;
}

/** One line of the history table: the retail and wholesale change of one edit of one SKU color. */
export interface PriceLogRow {
  key: string;
  at: string;
  skuId: string;
  skuCode: string;
  skuName: string;
  colorId: string | null;
  retail: PriceMove | null;
  wholesale: PriceMove | null;
  source: PriceChangeSource;
  actor: string | null;
  /** A color price was removed: the color is back on the general price. */
  cleared: boolean;
  /** The general price was set with "Áp dụng cho tất cả màu". */
  allColors: boolean;
}

/** Retail and wholesale halves of one save are separate requests; they land within this window. */
const SAME_EDIT_MS = 5_000;

const instant = (at: string) => new Date(at).getTime();

/**
 * Groups base-list history rows (newest first) into edits: the retail and the wholesale change of
 * the same SKU color, source and person made together form one row. Tiered and per-unit rows are
 * not sell-price edits and are left out.
 */
export function priceLogRows(changes: readonly PriceChange[]): PriceLogRow[] {
  const rows: PriceLogRow[] = [];
  for (const c of changes) {
    if ((c.price_list_kind !== "retail" && c.price_list_kind !== "wholesale") || c.unit_id !== null || dec(c.min_qty).gt(0)) continue;
    const kind = c.price_list_kind;
    const move: PriceMove = { old: c.old_price, next: c.new_price };
    const into = rows.find(
      (r) =>
        r.skuId === c.sku_id &&
        r.colorId === c.color_id &&
        r.source === c.source &&
        r.actor === c.actor_name &&
        r[kind] === null &&
        Math.abs(instant(r.at) - instant(c.created_at)) <= SAME_EDIT_MS,
    );
    if (into) {
      into[kind] = move;
      into.cleared = into.cleared || (c.color_id !== null && c.new_price === null);
      continue;
    }
    rows.push({
      key: c.id,
      at: c.created_at,
      skuId: c.sku_id,
      skuCode: c.sku_code,
      skuName: c.sku_name,
      colorId: c.color_id,
      retail: kind === "retail" ? move : null,
      wholesale: kind === "wholesale" ? move : null,
      source: c.source,
      actor: c.actor_name,
      cleared: c.color_id !== null && c.new_price === null,
      allColors: false,
    });
  }
  for (const r of rows) {
    if (r.colorId !== null) continue;
    r.allColors = rows.some((x) => x.cleared && x.skuId === r.skuId && x.source === r.source && Math.abs(instant(x.at) - instant(r.at)) <= SAME_EDIT_MS);
  }
  return rows;
}
