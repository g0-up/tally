import { randomUUID } from "node:crypto";
import { expect, type Locator, type Page } from "@playwright/test";
import { apiGet, apiPost } from "./real-api";
import { escapeRe } from "./sales-data";

// Goods sold and stocked in several units, prepared through the running API for the journeys that
// read them back on screen: a candy counted in cái, sold by the dây of 50 at its own price and by
// the thùng of 500 at the base price × factor. Every run creates its own product, so runs never collide.

interface Unit {
  id: string;
  name: string;
  active: boolean;
}

interface Supplier {
  id: string;
  status: string;
}

export interface SkuUnitRow {
  unit_id: string;
  unit_name: string;
  factor: string;
  is_base: boolean;
  active: boolean;
  is_default_purchase: boolean;
  retail_price: string | null;
  wholesale_price: string | null;
}

export interface UnitSkuRow {
  id: string;
  code: string;
  display_name: string;
  on_hand: string;
  retail_price: string | null;
  units: SkuUnitRow[];
}

/** Unit ids of the candy: cái is the base unit, 1 dây = 50 cái, 1 thùng = 500 cái. */
export interface CandyUnits {
  cai: string;
  day: string;
  thung: string;
}

export interface Candy {
  skuId: string;
  code: string;
  name: string;
  units: CandyUnits;
}

/** Base price of one cái, the dây's own price, and the thùng's price at 500 × the base price. */
export const CANDY_PRICES = { cai: "500", day: "24000", thung: "250000", dayAuto: "25000" } as const;

/** A fresh code and name for one run ("KX…", "Kẹo e2e …"). */
export function candyNames(): { code: string; name: string } {
  const stamp = Date.now().toString(36);
  return { code: `KX${stamp.toUpperCase()}`, name: `Kẹo e2e ${stamp}` };
}

/** Id of the active unit with this name, added to the store's unit directory when it is missing. */
export async function unitId(page: Page, name: string): Promise<string> {
  const units = (await apiGet<{ data: Unit[] }>(page, "/units")).data;
  const found = units.find((u) => u.active && u.name === name);
  if (found) return found.id;
  return (await apiPost<Unit>(page, "/units", { name })).id;
}

/** The candy's units, each added to the directory first when the store lacks it. */
export async function candyUnits(page: Page): Promise<CandyUnits> {
  return { cai: await unitId(page, "cái"), day: await unitId(page, "dây"), thung: await unitId(page, "thùng") };
}

/** Creates the candy with its three units: dây at its own price, thùng with no price of its own. */
export async function createCandy(page: Page): Promise<Candy> {
  const units = await candyUnits(page);
  const { code, name } = candyNames();
  const product = await apiPost<{ skus: { id: string; code: string }[] }>(page, "/products", {
    type: "goods",
    name,
    sku: {
      code,
      base_unit_id: units.cai,
      track_lot: false,
      track_expiry: false,
      retail_price: CANDY_PRICES.cai,
      units: [
        { unit_id: units.day, factor: "50", retail_price: CANDY_PRICES.day },
        { unit_id: units.thung, factor: "500" },
      ],
    },
  });
  const sku = product.skus[0];
  if (sku === undefined) throw new Error(`the product ${name} came back without a SKU`);
  return { skuId: sku.id, code: sku.code, name, units };
}

/** The catalog row of the SKU with this exact code, with its units and on-hand quantity. */
export async function skuByCode(page: Page, code: string): Promise<UnitSkuRow> {
  const r = await apiGet<{ data: UnitSkuRow[] }>(page, `/skus?q=${encodeURIComponent(code)}&limit=10`);
  const sku = r.data.find((s) => s.code === code);
  expect(sku, `SKU ${code} listed`).toBeDefined();
  if (sku === undefined) throw new Error("unreachable");
  return sku;
}

/** The unit of a SKU row with this name. */
export function unitOf(sku: UnitSkuRow, name: string): SkuUnitRow {
  const unit = sku.units.find((u) => u.unit_name === name);
  expect(unit, `unit ${name} of ${sku.code}`).toBeDefined();
  if (unit === undefined) throw new Error("unreachable");
  return unit;
}

/** Price of one unit of the SKU as a new sale would quote it. */
export async function quotedPrice(page: Page, skuId: string, unit: string): Promise<string> {
  const r = await apiPost<{ data: { amount: string }[] }>(page, "/pricing/quote", { lines: [{ sku_id: skuId, unit_id: unit, qty: "1" }] });
  const line = r.data[0];
  if (line === undefined) throw new Error(`no quote for unit ${unit} of SKU ${skuId}`);
  return line.amount;
}

/** An active supplier of the store, or a new one for this run when the store has none. */
async function supplierId(page: Page): Promise<string> {
  const active = (await apiGet<{ data: Supplier[] }>(page, "/suppliers?limit=200")).data.find((s) => s.status === "active");
  if (active) return active.id;
  return (await apiPost<{ id: string }>(page, "/suppliers", { name: `NCC e2e ${Date.now().toString(36)}` })).id;
}

/** Posts a receipt of the SKU counted in the given units (a stock-in the screens then convert). */
export async function receiveInUnits(page: Page, skuId: string, lines: readonly { unitId: string; qty: string; unitPrice: string }[]): Promise<void> {
  await apiPost(
    page,
    "/receipts",
    {
      supplier_party_id: await supplierId(page),
      paid_now_amount: "0",
      lines: lines.map((l) => ({ sku_id: skuId, unit_id: l.unitId, qty: l.qty, unit_price: l.unitPrice })),
    },
    { "Idempotency-Key": randomUUID() },
  );
}

/**
 * Picks a unit by name in a unit field of the unit table: the directory entry when the store has it,
 * else its "+ Tạo mới …" option, which adds it to the directory.
 */
export async function pickUnit(box: Locator, name: string): Promise<void> {
  await box.click();
  await box.fill(name);
  const option = new RegExp(`^(${escapeRe(name)}|\\+ Tạo mới “${escapeRe(name)}”)$`);
  await box.page().getByRole("option", { name: option }).first().click();
  await expect(box).toHaveValue(name);
}
