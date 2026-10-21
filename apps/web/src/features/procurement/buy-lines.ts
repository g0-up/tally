import { fieldReasonMessage, isApiError } from "../../shared/api/api-error";
import type { ColorCode } from "../../shared/domain/color";
import { toMoney, type Money } from "../../shared/domain/money";
import { toQty, type Qty } from "../../shared/domain/qty";
import type { ScanOutcome } from "../../shared/lib/barcode";
import { dec, sumDec, type Decimal } from "../../shared/lib/decimal";
import { baseUnitName, defaultPurchaseUnit, firstProductError, initialUnitTable, unitsRequest, unitTableErrors, type SkuCodeMatch, type SkuListItem, type UnitRow } from "../catalog";
import { PROCUREMENT_MESSAGES } from "./api/adapters";
import type { OrderLine } from "./schemas";

type NewSkuUnit = ReturnType<typeof unitsRequest>[number];

// The line table of Đơn nhập and Nhận hàng (prototype `buyEditor`, `buyOnChange`, `buyLines`,
// `receive`): a row is an existing SKU, an inline new product, or — when receiving against an
// order — an ordered line counted in base units at the ordered price. The functions below are
// pure so the dialogs only hold the rows and the request bodies are built in one place.

/** Receiving tolerance over what is still pending on an ordered line (prototype 5%). */
export const RECEIVE_TOLERANCE = "1.05";

/** The ordered line a receipt row receives against; quantities on such a row are in base units. */
export interface OrderRef {
  lineId: string;
  /** SKU of the ordered line, known even while its catalog row is not loaded. */
  skuId: string;
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
  /** Unit the quantity and price are in: of an existing SKU, or a unit row of a new product (null = its base unit). */
  unitId: string | null;
  isNew: boolean;
  /** New product: name, units and lot tracking. */
  name: string;
  baseUnitId: string | null;
  /** New product: its conversion units with their own prices (the unit table under the row). */
  units: UnitRow[];
  /** New product: the unit table is open under the row. */
  showUnits: boolean;
  /** New product: a submit found errors in its unit table, which then shows them. */
  unitsChecked: boolean;
  trackLot: boolean;
  /** New product: barcode of its base unit, blank for none. */
  barcode: string;
  qty: Qty | null;
  /** Price of one unit of the row. */
  price: Money | null;
  /** "Giá bán lẻ" of one base unit. */
  sell: Money | null;
  /** New product: "Giá sỉ" of one base unit. */
  wholesale: Money | null;
  spec: string;
  color: ColorCode | null;
  lotNo: string;
  expiry: string | null;
  order: OrderRef | null;
  /**
   * Receiving against an order: a phone scan has landed on this row since the dialog opened. The
   * first scan replaces the prefilled pending quantity, the next ones add; typing does not set it.
   */
  scanned: boolean;
}

let lastKey = 0;
const nextKey = () => `buy-${++lastKey}`;

const BLANK: Omit<BuyLine, "key"> = {
  sku: null,
  unitId: null,
  isNew: false,
  name: "",
  baseUnitId: null,
  units: [],
  showUnits: false,
  unitsChecked: false,
  trackLot: false,
  barcode: "",
  qty: toQty(1),
  price: toMoney(0),
  sell: null,
  wholesale: null,
  spec: "",
  color: null,
  lotNo: "",
  expiry: null,
  order: null,
  scanned: false,
};

/** "+ Hàng có sẵn": an empty row waiting for a SKU. */
export function emptyBuyLine(): BuyLine {
  return { ...BLANK, key: nextKey() };
}

/** "+ Hàng hóa mới": prototype defaults (hộp, thùng = 12, bought by the thùng, lot tracked). */
export function newProductLine(unitIdByName: (name: string) => string | null): BuyLine {
  const table = initialUnitTable(unitIdByName);
  return {
    ...BLANK,
    key: nextKey(),
    isNew: true,
    baseUnitId: table.baseUnitId,
    units: table.units,
    unitId: table.units[0]?.unitId ?? null,
    trackLot: true,
    sell: toMoney(0),
  };
}

/**
 * A new-product row for a scanned code the catalog does not have: the "+ Hàng hóa mới" defaults
 * with the code as its barcode, bought by the base unit so each further scan of it adds one.
 */
export function scannedNewLine(code: string, unitIdByName: (name: string) => string | null): BuyLine {
  return { ...newProductLine(unitIdByName), barcode: code.trim(), unitId: null };
}

/** Goods and materials are bought; kits and combos are not. */
export const isBuyable = (sku: SkuListItem | undefined) => sku?.product_type === "goods" || sku?.product_type === "material";

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
    order: { lineId: ol.id, skuId: ol.sku_id, name: ol.name, pending: ol.pending_base_qty, unitFactor: ol.unit_factor, unitName: ol.unit_name, basePrice },
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
  if (line.isNew) {
    const row = newProductUnit(line);
    return row ? dec(row.factor ?? 0) : dec(1);
  }
  return line.sku ? unitFactor(line.sku, line.unitId) : dec(1);
}

/** The unit row a new-product row is counted in; undefined for its base unit. */
export function newProductUnit(line: BuyLine): UnitRow | undefined {
  return line.unitId === null ? undefined : line.units.find((u) => u.unitId === line.unitId);
}

/**
 * A new-product row with a changed unit table. The row keeps counting in its unit while that unit
 * stays in the table (followed when its name is changed). When the unit is removed, the row moves to
 * the base unit with its quantity and price converted, so the line still buys the same goods.
 */
export function withNewProductUnits(line: BuyLine, units: UnitRow[]): BuyLine {
  const was = newProductUnit(line);
  if (was === undefined) return { ...line, units, unitId: null };
  const kept = units.find((u) => u.key === was.key);
  if (kept !== undefined) return { ...line, units, unitId: kept.unitId };
  const factor = dec(was.factor ?? 0);
  if (!factor.gt(0)) return { ...line, units, unitId: null };
  return {
    ...line,
    units,
    unitId: null,
    qty: line.qty === null ? null : toQty(dec(line.qty).times(factor)),
    price: line.price === null ? null : toMoney(dec(line.price).div(factor)),
  };
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

/* ---------- scans ---------- */

/** The rows after a scan (null when it changes none), what the scanner is told, and the error to show on this screen. */
export interface ScanStep {
  lines: BuyLine[] | null;
  outcome: ScanOutcome;
  /** New-product row the scan opened; its name field takes focus. */
  created?: string;
  notice?: string;
}

const isEmptyRow = (l: BuyLine) => !l.isNew && l.sku === null && l.order === null;

const plusQty = (l: BuyLine, n: string | number): BuyLine => ({ ...l, qty: toQty(dec(l.qty ?? 0).plus(n)) });

/** The rows with row i changed. */
const changeRow = (lines: readonly BuyLine[], i: number, change: (l: BuyLine) => BuyLine): BuyLine[] => lines.map((l, j) => (j === i ? change(l) : l));

/**
 * The ordered row of a SKU a scan lands on: the first one whose scanned quantity is still below what
 * is pending (a row no scan reached yet has scanned nothing), else the first one; -1 when the order
 * has no row of that SKU. One SKU can sit on several rows (another colour or price).
 */
function orderRowOf(lines: readonly BuyLine[], skuId: string): number {
  let first = -1;
  for (const [i, l] of lines.entries()) {
    if (l.order?.skuId !== skuId) continue;
    if (!l.scanned || dec(l.qty ?? 0).lt(l.order.pending)) return i;
    if (first < 0) first = i;
  }
  return first;
}

/** A scan counted on an ordered row: the first replaces the prefilled pending quantity, the next ones add. */
const countOnOrderRow = (l: BuyLine, factor: string): BuyLine => (l.scanned ? plusQty(l, factor) : { ...l, qty: toQty(factor), scanned: true });

/** A scanned row takes the place of an empty last row, else goes after the others. */
function placeRow(lines: readonly BuyLine[], row: BuyLine): BuyLine[] {
  const last = lines.at(-1);
  return last !== undefined && isEmptyRow(last) ? [...lines.slice(0, -1), row] : [...lines, row];
}

/**
 * A code a new-product row already carries (exact match): one more in the base unit, or nothing
 * when that row is bought by a larger unit, which only the desktop can change. Null when no
 * new-product row carries it.
 */
export function scanNewProduct(lines: readonly BuyLine[], code: string): ScanStep | null {
  const wanted = code.trim();
  const i = wanted === "" ? -1 : lines.findIndex((l) => l.isNew && l.barcode.trim() === wanted);
  const row = lines[i];
  if (row === undefined) return null;
  if (row.unitId !== null) return { lines: null, outcome: { outcome: "needs_desktop_edit" } };
  return { lines: changeRow(lines, i, (l) => plusQty(l, 1)), outcome: { outcome: "new_product" } };
}

/**
 * Adds a scanned code to the rows. Receiving against an order (`fixed`), the base units of the code's
 * unit are counted on an ordered row of its SKU (see `orderRowOf`): the row's first scan replaces the
 * prefilled pending quantity, later ones add; a code of no ordered SKU adds nothing. Otherwise a known
 * code adds one to the row of the same SKU and unit, else a row of that SKU in that unit; an
 * unknown code adds one to the new-product row carrying it, else opens one with it as barcode.
 * `found` is the catalog's answer for the code, null for a code it does not have.
 */
export function addScan(lines: readonly BuyLine[], code: string, found: Pick<SkuCodeMatch, "sku" | "unit_id" | "factor"> | null, opts: { fixed: boolean; unitIdByName: (name: string) => string | null }): ScanStep {
  const wanted = code.trim();
  if (opts.fixed) {
    const i = found ? orderRowOf(lines, found.sku.id) : -1;
    if (!found || i < 0) return { lines: null, outcome: { outcome: "not_in_order" }, notice: PROCUREMENT_MESSAGES.scanNotInOrder(wanted) };
    return { lines: changeRow(lines, i, (l) => countOnOrderRow(l, found.factor)), outcome: { outcome: "added", skuId: found.sku.id, unitId: found.unit_id } };
  }
  if (!found) {
    const again = scanNewProduct(lines, wanted);
    if (again) return again;
    const row = scannedNewLine(wanted, opts.unitIdByName);
    return { lines: placeRow(lines, row), outcome: { outcome: "new_product" }, created: row.key };
  }
  if (!isBuyable(found.sku)) return { lines: null, outcome: { outcome: "not_found" }, notice: PROCUREMENT_MESSAGES.scanNotBuyable(wanted, found.sku.display_name) };
  const outcome: ScanOutcome = { outcome: "added", skuId: found.sku.id, unitId: found.unit_id };
  const i = lines.findIndex((l) => !l.isNew && l.order === null && l.sku?.id === found.sku.id && l.unitId === found.unit_id);
  if (i >= 0) return { lines: changeRow(lines, i, (l) => plusQty(l, 1)), outcome };
  return { lines: placeRow(lines, withUnit(withSku(emptyBuyLine(), found.sku), found.unit_id)), outcome };
}

/* ---------- request bodies ---------- */

/**
 * Request lines built from the rows, with the key of the row each one came from; or why they cannot
 * be, with the rows to show when the refusal is in a new product's unit table (opened under its row).
 */
export type Built<T> = { ok: true; lines: T[]; keys: string[] } | { ok: false; message: string; rows?: BuyLine[] };

export interface NewProductRequest {
  name: string;
  base_unit_id: string;
  purchase_unit_id?: string;
  units?: NewSkuUnit[];
  track_lot: boolean;
  retail_price?: Money;
  wholesale_price?: Money;
  barcode?: string;
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

/** Errors of a new-product row's unit table; its base unit is picked on the row and checked with the row. */
export function lineUnitErrors(line: BuyLine): Record<string, string> {
  const { base_unit_id: _, ...errors } = unitTableErrors({ baseUnitId: line.baseUnitId, retailPrice: line.sell, wholesalePrice: line.wholesale, units: line.units });
  return errors;
}

const inUse = (l: BuyLine) => l.isNew && (l.name.trim() !== "" || isPositive(l.qty));

/**
 * Prototype `buyLines` → `utCheck`: the new-product rows in use whose unit table has errors are
 * opened with their errors shown, and the first error is the refusal. Null when every table is valid.
 */
function unitTableRefusal(lines: readonly BuyLine[]): { ok: false; message: string; rows: BuyLine[] } | null {
  let message: string | undefined;
  const rows = lines.map((l) => {
    if (!inUse(l)) return l;
    const errors = lineUnitErrors(l);
    if (Object.keys(errors).length === 0) return l;
    message ??= firstProductError(errors);
    return { ...l, showUnits: true, unitsChecked: true };
  });
  return message === undefined ? null : { ok: false, message, rows };
}

/**
 * The product a new-product row creates, and the unit its quantity is in; or the reason it cannot.
 * With conversion units the line's unit (the base unit or one of the table's) is sent as the
 * purchase unit, which is also what the API takes when it is omitted: the line's price becomes
 * that unit's reference cost.
 */
function newProductOf(line: BuyLine): { product: NewProductRequest; unitId: string } | string {
  const name = line.name.trim();
  if (line.baseUnitId === null) return PROCUREMENT_MESSAGES.newProductUnit(name);
  const units = unitsRequest(line.units);
  const unitId = newProductUnit(line)?.unitId ?? line.baseUnitId;
  const barcode = line.barcode.trim();
  const product: NewProductRequest = {
    name,
    base_unit_id: line.baseUnitId,
    track_lot: line.trackLot,
    ...(units.length > 0 ? { units, purchase_unit_id: unitId } : {}),
    ...(isPositive(line.sell) && line.sell !== null ? { retail_price: line.sell } : {}),
    ...(isPositive(line.wholesale) && line.wholesale !== null ? { wholesale_price: line.wholesale } : {}),
    ...(barcode === "" ? {} : { barcode }),
  };
  return { product, unitId };
}

/** The barcode two new products of a request carry, compared exactly; null when none is shared. */
function sharedBarcode(lines: readonly { new_product?: NewProductRequest }[]): string | null {
  const seen = new Set<string>();
  for (const l of lines) {
    const code = l.new_product?.barcode;
    if (code === undefined) continue;
    if (seen.has(code)) return code;
    seen.add(code);
  }
  return null;
}

/** The built lines, unless two new products carry one barcode. */
function built<T extends { new_product?: NewProductRequest }>(lines: T[], keys: string[]): Built<T> {
  const shared = sharedBarcode(lines);
  return shared === null ? { ok: true, lines, keys } : { ok: false, message: PROCUREMENT_MESSAGES.barcodeShared(shared) };
}

/**
 * Rows of a new order (prototype `buyLines`): empty rows are skipped, a new product needs a valid
 * unit table and its price; an order without rows is refused, and so are two new products with one barcode.
 */
export function orderLinesRequest(lines: readonly BuyLine[], colorIdOf: ColorIdOf): Built<OrderLineRequest> {
  const refused = unitTableRefusal(lines);
  if (refused) return refused;
  const out: OrderLineRequest[] = [];
  const keys: string[] = [];
  for (const l of lines) {
    if (l.isNew) {
      const name = l.name.trim();
      if (name === "" && !isPositive(l.qty)) continue;
      if (!isPositive(l.price) || l.price === null) return { ok: false, message: PROCUREMENT_MESSAGES.newProductPrice(name) };
      if (!isPositive(l.qty) || l.qty === null) return { ok: false, message: PROCUREMENT_MESSAGES.newProductQty(name) };
      const np = newProductOf(l);
      if (typeof np === "string") return { ok: false, message: np };
      out.push({ unit_id: np.unitId, qty: l.qty, unit_price: l.price, discount_amount: toMoney(0), new_product: np.product, ...common(l, colorIdOf) });
      keys.push(l.key);
    } else if (l.sku && isPositive(l.qty) && l.qty !== null) {
      out.push({
        sku_id: l.sku.id,
        ...(l.unitId === null ? {} : { unit_id: l.unitId }),
        qty: l.qty,
        unit_price: l.price ?? toMoney(0),
        discount_amount: toMoney(0),
        ...common(l, colorIdOf),
      });
      keys.push(l.key);
    }
  }
  if (out.length === 0) return { ok: false, message: PROCUREMENT_MESSAGES.orderEmpty };
  return built(out, keys);
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
 * Rows of a receipt (prototype `buyLines` then `receive`): new products need a valid unit table
 * and a price, at least one
 * row must carry a quantity, tracked SKUs need lot and expiry, and an ordered line may be received
 * up to 5% over what is pending — the part above pending is sent as excess of the same line. Two
 * new products with one barcode are refused.
 */
export function receiptLinesRequest(lines: readonly BuyLine[], colorIdOf: ColorIdOf): Built<ReceiptLineRequest> {
  const refused = unitTableRefusal(lines);
  if (refused) return refused;
  for (const l of lines) {
    if (inUse(l) && !isPositive(l.price)) {
      return { ok: false, message: PROCUREMENT_MESSAGES.newProductPrice(l.name.trim()) };
    }
  }
  const kept = lines.filter((l) => (l.isNew || l.sku !== null || l.order !== null) && isPositive(l.qty));
  if (kept.length === 0) return { ok: false, message: PROCUREMENT_MESSAGES.receiptEmpty };

  const out: ReceiptLineRequest[] = [];
  const keys: string[] = [];
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
        keys.push(l.key);
      }
    } else if (l.isNew) {
      const np = newProductOf(l);
      if (typeof np === "string") return { ok: false, message: np };
      out.push({ unit_id: np.unitId, qty, unit_price: l.price ?? toMoney(0), new_product: np.product, ...shared });
      keys.push(l.key);
    } else if (l.sku) {
      out.push({ sku_id: l.sku.id, ...(l.unitId === null ? {} : { unit_id: l.unitId }), qty, unit_price: l.price ?? toMoney(0), ...shared });
      keys.push(l.key);
    }
  }
  return built(out, keys);
}

/** Request lines a write sent, kept to place the barcode refusals of its answer on their rows. */
export type SentLines = Extract<Built<{ new_product?: NewProductRequest }>, { ok: true }>;

/**
 * A write's lines and the error the form held when it was sent. That error answers an earlier write
 * and can outlive the send by a render (the form's state and the mutation's update separately), so
 * it is never placed on these lines.
 */
export interface SentWrite {
  lines: SentLines;
  before: unknown;
}

/** A refusal of the barcode a new-product row sent; shown under its "Mã vạch" while the row still holds that code. */
export interface BarcodeError {
  code: string;
  message: string;
}

const BARCODE_FIELD = /^lines\.(\d+)\.new_product\.barcode$/;

/**
 * The barcode refusals of a write, by the key of the row each refused line came from. `sent` is
 * what that write sent; an error it was sent over belongs to an earlier write and places nothing. A
 * field whose reason is the error's own code (barcode_taken) gets the server's sentence; other
 * reasons the shared reason text.
 */
export function barcodeErrors(error: unknown, write: SentWrite | null): ReadonlyMap<string, BarcodeError> {
  const out = new Map<string, BarcodeError>();
  if (write === null || error === write.before || !isApiError(error)) return out;
  const sent = write.lines;
  for (const [field, reason] of Object.entries(error.fields)) {
    const m = BARCODE_FIELD.exec(field);
    if (m === null) continue;
    const i = Number(m[1]);
    const key = sent.keys[i];
    const code = sent.lines[i]?.new_product?.barcode;
    if (key === undefined || code === undefined) continue;
    out.set(key, { code, message: reason === error.code ? error.message : fieldReasonMessage(reason) });
  }
  return out;
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
