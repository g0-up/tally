import type { ColorCode } from "../../shared/domain/color";
import { toMoney, type Money } from "../../shared/domain/money";
import { toQty, type Qty } from "../../shared/domain/qty";
import { dec, sumDec, type Decimal } from "../../shared/lib/decimal";
import { baseUnitName, defaultPurchaseUnit, type SkuListItem } from "../catalog";
import { PROCUREMENT_MESSAGES } from "./api/adapters";
import type { OrderLine } from "./schemas";

// The line table of Đơn nhập and Nhận hàng (prototype `buyEditor`, `buyOnChange`, `buyLines`,
// `receive`): a row is an existing SKU, an inline new product, or — when receiving against an
// order — an ordered line counted in base units at the ordered price. The functions below are
// pure so the dialogs only hold the rows and the request bodies are built in one place.

/** Receiving tolerance over what is still pending on an ordered line (prototype 5%). */
export const RECEIVE_TOLERANCE = "1.05";

/** The ordered line a receipt row receives against; quantities on such a row are in base units. */
export interface OrderRef {
  lineId: string;
  /** Ordered product name, shown while the SKU row is not loaded. */
  name: string;
  pending: Qty;
  /** Base units in one order unit. */
  unitFactor: string;
  unitName: string;
  /** Ordered price of one base unit. */
  basePrice: Money;
}

export interface BuyLine {
  key: string;
  /** Existing SKU; null on an empty row and on a new product. */
  sku: SkuListItem | null;
  /** Unit of an existing SKU the quantity and price are in. */
  unitId: string | null;
  isNew: boolean;
  /** New product: name, units and lot tracking. */
  name: string;
  baseUnitId: string | null;
  bigUnitId: string | null;
  factor: Qty | null;
  /** New product bought in its big unit rather than the base unit. */
  inBig: boolean;
  trackLot: boolean;
  qty: Qty | null;
  /** Price of one unit of the row. */
  price: Money | null;
  /** "Giá bán lẻ" of one base unit. */
  sell: Money | null;
  spec: string;
  color: ColorCode | null;
  lotNo: string;
  expiry: string | null;
  order: OrderRef | null;
}

let lastKey = 0;
const nextKey = () => `buy-${++lastKey}`;

const BLANK: Omit<BuyLine, "key"> = {
  sku: null,
  unitId: null,
  isNew: false,
  name: "",
  baseUnitId: null,
  bigUnitId: null,
  factor: null,
  inBig: false,
  trackLot: false,
  qty: toQty(1),
  price: toMoney(0),
  sell: null,
  spec: "",
  color: null,
  lotNo: "",
  expiry: null,
  order: null,
};

/** "+ Hàng có sẵn": an empty row waiting for a SKU. */
export function emptyBuyLine(): BuyLine {
  return { ...BLANK, key: nextKey() };
}

/** "+ Hàng hóa mới": prototype defaults (hộp, thùng = 12, bought by the thùng, lot tracked). */
export function newProductLine(unitIdByName: (name: string) => string | null): BuyLine {
  const base = unitIdByName("hộp");
  const big = unitIdByName("thùng");
  return {
    ...BLANK,
    key: nextKey(),
    isNew: true,
    baseUnitId: base,
    bigUnitId: big,
    factor: toQty(12),
    inBig: big !== null,
    trackLot: true,
    sell: toMoney(0),
  };
}

const unitFactor = (sku: SkuListItem, unitId: string | null): Decimal => {
  const u = sku.units.find((x) => x.unit_id === unitId);
  return u ? dec(u.factor) : dec(1);
};

/** Price of one unit of a SKU: the last purchase price (else the reference cost) per base unit × factor. */
function purchasePriceOf(sku: SkuListItem, unitId: string | null): Money {
  const perBase = sku.last_purchase_price ?? sku.ref_cost ?? "0";
  return toMoney(dec(perBase).times(unitFactor(sku, unitId)).toDecimalPlaces(0));
}

/** A SKU picked on a row: the default purchase unit, its purchase price and the current retail price. */
export function withSku(line: BuyLine, sku: SkuListItem): BuyLine {
  const unitId = defaultPurchaseUnit(sku)?.unit_id ?? null;
  return { ...line, sku, unitId, price: purchasePriceOf(sku, unitId), sell: sku.retail_price, lotNo: "", expiry: null };
}

/** Another unit picked for an existing SKU: the price follows the unit. */
export function withUnit(line: BuyLine, unitId: string): BuyLine {
  return line.sku ? { ...line, unitId, price: purchasePriceOf(line.sku, unitId) } : line;
}

/** A row receiving the pending quantity of an ordered line; sku is the catalog row of its SKU when known. */
export function orderBuyLine(ol: OrderLine, sku: SkuListItem | null, color: ColorCode | null): BuyLine {
  const basePrice = toMoney(dec(ol.unit_price).div(ol.unit_factor));
  return {
    ...BLANK,
    key: nextKey(),
    sku,
    qty: ol.pending_base_qty,
    price: basePrice,
    sell: sku?.retail_price ?? null,
    spec: ol.spec_text ?? "",
    color,
    order: { lineId: ol.id, name: ol.name, pending: ol.pending_base_qty, unitFactor: ol.unit_factor, unitName: ol.unit_name, basePrice },
  };
}

/** Name the row is reported under. */
export function lineName(line: BuyLine, fallback = ""): string {
  if (line.isNew) return line.name.trim() || "Hàng mới";
  return line.sku?.display_name ?? line.order?.name ?? fallback;
}

/** Base units in one unit of the row. */
export function lineFactor(line: BuyLine): Decimal {
  if (line.order) return dec(1);
  if (line.isNew) return line.inBig ? dec(line.factor ?? 0) : dec(1);
  return line.sku ? unitFactor(line.sku, line.unitId) : dec(1);
}

/** "Thành tiền" of a row (quantity × unit price). */
export function lineAmount(line: BuyLine): Decimal {
  return dec(line.qty ?? 0).times(line.price ?? 0);
}

export function buyTotal(lines: readonly BuyLine[]): Decimal {
  return sumDec(lines.map(lineAmount));
}

/** "Hàng hóa mới sẽ tạo": new-product rows that have a name. */
export function newProductCount(lines: readonly BuyLine[]): number {
  return lines.filter((l) => l.isNew && l.name.trim() !== "").length;
}

/** The lot number is asked for on this row. */
export function tracksLot(line: BuyLine): boolean {
  return line.isNew ? line.trackLot : (line.sku?.track_lot ?? false);
}

/** The expiry date is asked for on this row. */
export function tracksExpiry(line: BuyLine): boolean {
  return line.isNew ? line.trackLot : (line.sku?.track_expiry ?? false);
}

/** Received more than pending × 1.05 of an ordered line. */
export function overTolerance(line: BuyLine): boolean {
  return line.order !== null && dec(line.qty ?? 0).gt(dec(line.order.pending).times(RECEIVE_TOLERANCE));
}

/** Base unit name of a row, for the "/hộp" hints. */
export function lineBaseUnit(line: BuyLine, unitName: (id: string | null) => string): string {
  if (line.isNew) return unitName(line.baseUnitId);
  return line.sku ? baseUnitName(line.sku) : "";
}

/* ---------- request bodies ---------- */

export type Built<T> = { ok: true; lines: T[] } | { ok: false; message: string };

export interface NewProductRequest {
  name: string;
  base_unit_id: string;
  purchase_unit_id?: string;
  factor?: Qty;
  track_lot: boolean;
  retail_price?: Money;
}

export interface OrderLineRequest {
  sku_id?: string;
  unit_id?: string;
  qty: Qty;
  color_id?: string;
  unit_price: Money;
  discount_amount: Money;
  spec_text?: string;
  new_product?: NewProductRequest;
}

export interface ReceiptLineRequest {
  purchase_order_line_id?: string;
  sku_id?: string;
  unit_id?: string;
  qty: Qty;
  color_id?: string;
  condition?: "excess";
  lot_no?: string;
  expiry_date?: string;
  unit_price?: Money;
  spec_text?: string;
  new_product?: NewProductRequest;
}

type ColorIdOf = (color: ColorCode | null) => string | null;

const isPositive = (v: string | null) => v !== null && dec(v).gt(0);

/** Fields every kind of row sends: color and "Quy cách". */
function common(line: BuyLine, colorIdOf: ColorIdOf) {
  const colorId = colorIdOf(line.color);
  const spec = line.spec.trim();
  return { ...(colorId === null ? {} : { color_id: colorId }), ...(spec === "" ? {} : { spec_text: spec }) };
}

/** The product a new-product row creates, and the unit its quantity is in; or the reason it cannot. */
function newProductOf(line: BuyLine): { product: NewProductRequest; unitId: string } | string {
  const name = line.name.trim();
  if (line.baseUnitId === null) return PROCUREMENT_MESSAGES.newProductUnit(name);
  const big = line.bigUnitId !== null && line.bigUnitId !== line.baseUnitId ? line.bigUnitId : null;
  if (big !== null && !isPositive(line.factor)) return PROCUREMENT_MESSAGES.factorPositive;
  const product: NewProductRequest = {
    name,
    base_unit_id: line.baseUnitId,
    track_lot: line.trackLot,
    ...(big !== null && line.factor !== null ? { purchase_unit_id: big, factor: line.factor } : {}),
    ...(isPositive(line.sell) && line.sell !== null ? { retail_price: line.sell } : {}),
  };
  return { product, unitId: big !== null && line.inBig ? big : line.baseUnitId };
}

/**
 * Rows of a new order (prototype `buyLines`): empty rows are skipped, a new product needs its
 * price; an order without rows is refused.
 */
export function orderLinesRequest(lines: readonly BuyLine[], colorIdOf: ColorIdOf): Built<OrderLineRequest> {
  const out: OrderLineRequest[] = [];
  for (const l of lines) {
    if (l.isNew) {
      const name = l.name.trim();
      if (name === "" && !isPositive(l.qty)) continue;
      if (!isPositive(l.price) || l.price === null) return { ok: false, message: PROCUREMENT_MESSAGES.newProductPrice(name) };
      if (!isPositive(l.qty) || l.qty === null) return { ok: false, message: PROCUREMENT_MESSAGES.newProductQty(name) };
      const np = newProductOf(l);
      if (typeof np === "string") return { ok: false, message: np };
      out.push({ unit_id: np.unitId, qty: l.qty, unit_price: l.price, discount_amount: toMoney(0), new_product: np.product, ...common(l, colorIdOf) });
    } else if (l.sku && isPositive(l.qty) && l.qty !== null) {
      out.push({
        sku_id: l.sku.id,
        ...(l.unitId === null ? {} : { unit_id: l.unitId }),
        qty: l.qty,
        unit_price: l.price ?? toMoney(0),
        discount_amount: toMoney(0),
        ...common(l, colorIdOf),
      });
    }
  }
  if (out.length === 0) return { ok: false, message: PROCUREMENT_MESSAGES.orderEmpty };
  return { ok: true, lines: out };
}

/** Lot and expiry of a row: sent only when its SKU tracks them (the server refuses them otherwise). */
function lotFields(line: BuyLine): Pick<ReceiptLineRequest, "lot_no" | "expiry_date"> {
  const lot = line.lotNo.trim();
  return {
    ...(tracksLot(line) && lot !== "" ? { lot_no: lot } : {}),
    ...(tracksExpiry(line) && line.expiry ? { expiry_date: line.expiry } : {}),
  };
}

/** A base quantity in the order unit, or null when it does not fit four decimals. */
function inOrderUnit(baseQty: Decimal, ref: OrderRef): Qty | null {
  const q = baseQty.div(ref.unitFactor);
  return q.toDecimalPlaces(4).eq(q) ? toQty(q) : null;
}

/**
 * Rows of a receipt (prototype `buyLines` then `receive`): new products need a price, at least one
 * row must carry a quantity, tracked SKUs need lot and expiry, and an ordered line may be received
 * up to 5% over what is pending — the part above pending is sent as excess of the same line.
 */
export function receiptLinesRequest(lines: readonly BuyLine[], colorIdOf: ColorIdOf): Built<ReceiptLineRequest> {
  for (const l of lines) {
    if (l.isNew && (l.name.trim() !== "" || isPositive(l.qty)) && !isPositive(l.price)) {
      return { ok: false, message: PROCUREMENT_MESSAGES.newProductPrice(l.name.trim()) };
    }
  }
  const kept = lines.filter((l) => (l.isNew || l.sku !== null || l.order !== null) && isPositive(l.qty));
  if (kept.length === 0) return { ok: false, message: PROCUREMENT_MESSAGES.receiptEmpty };

  const out: ReceiptLineRequest[] = [];
  for (const l of kept) {
    const qty = l.qty ?? toQty(0);
    const name = lineName(l);
    if (tracksLot(l) && l.lotNo.trim() === "") return { ok: false, message: PROCUREMENT_MESSAGES.lotRequired(name) };
    if (tracksExpiry(l) && !l.expiry) return { ok: false, message: PROCUREMENT_MESSAGES.expiryRequired(name) };
    const shared = { ...common(l, colorIdOf), ...lotFields(l) };

    if (l.order) {
      const ref = l.order;
      if (overTolerance(l)) return { ok: false, message: PROCUREMENT_MESSAGES.overTolerance(name, qty, ref.pending) };
      const pending = dec(ref.pending);
      const accepted = dec(qty).gt(pending) ? pending : dec(qty);
      const excess = dec(qty).minus(accepted);
      for (const [part, excessPart] of [
        [accepted, false],
        [excess, true],
      ] as const) {
        if (!part.gt(0)) continue;
        const inUnit = inOrderUnit(part, ref);
        if (inUnit === null) return { ok: false, message: PROCUREMENT_MESSAGES.notInOrderUnit(name, ref.unitName) };
        out.push({ purchase_order_line_id: ref.lineId, qty: inUnit, ...(excessPart ? { condition: "excess" as const } : {}), ...shared });
      }
    } else if (l.isNew) {
      const np = newProductOf(l);
      if (typeof np === "string") return { ok: false, message: np };
      out.push({ unit_id: np.unitId, qty, unit_price: l.price ?? toMoney(0), new_product: np.product, ...shared });
    } else if (l.sku) {
      out.push({ sku_id: l.sku.id, ...(l.unitId === null ? {} : { unit_id: l.unitId }), qty, unit_price: l.price ?? toMoney(0), ...shared });
    }
  }
  return { ok: true, lines: out };
}

/** A retail price typed on a row of an existing SKU that differs from its current one. */
export interface SellChange {
  sku: SkuListItem;
  retail: Money;
}

/**
 * "Giá bán lẻ" edits of the rows (prototype `setPrice(sku, sell, min(whole, sell))`): one per SKU,
 * the last row winning, only positive prices that differ from the current one.
 */
export function sellChanges(lines: readonly BuyLine[]): SellChange[] {
  const bySku = new Map<string, SellChange>();
  for (const l of lines) {
    if (l.isNew || !l.sku || !isPositive(l.qty) || l.sell === null || !dec(l.sell).gt(0)) continue;
    if (l.sku.retail_price !== null && dec(l.sku.retail_price).eq(l.sell)) {
      bySku.delete(l.sku.id);
      continue;
    }
    bySku.set(l.sku.id, { sku: l.sku, retail: l.sell });
  }
  return [...bySku.values()];
}
