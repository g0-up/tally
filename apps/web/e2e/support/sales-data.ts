import { expect, type Locator, type Page } from "@playwright/test";
import { colorDef, isColorCode } from "../../src/shared/domain/color";

// Seed discovery and shared steps of the sales journeys against the running API (see real-api.ts).
// Journeys pick what the seeded store has in stock instead of relying on fixed codes, and skip
// with a reason when the store lacks what a journey needs.

export interface PosTile {
  sku_id: string;
  sku_code: string;
  name: string;
  variant_label: string;
  unit_id: string;
  unit_name: string;
  retail_price: string | null;
  stock: { color_id: string | null; qty: string }[];
}

export interface SeedCustomer {
  id: string;
  name: string;
  credit_limit: string | null;
  payment_terms_days: number;
  status: string;
}

interface ApiColor {
  id: string;
  code: string;
}

/** GET from the API of the page's host with the session cookies of the signed-in owner. */
export async function apiGet<T>(page: Page, path: string): Promise<T> {
  const origin = new URL(page.url()).origin;
  const res = await page.request.get(`${origin}/api/v1${path}`);
  expect(res.ok(), `GET ${path} answered ${res.status()}: ${await res.text()}`).toBe(true);
  return (await res.json()) as T;
}

/** Sellable tiles of the POS screen with their retail price and stock by color. */
export async function posTiles(page: Page): Promise<PosTile[]> {
  const r = await apiGet<{ tiles: PosTile[] }>(page, "/pos/bootstrap");
  return r.tiles;
}

/** Name of a product as cart lines and dialogs show it ("Bánh quy bơ" or "Bánh quy bơ Hộp 500g"). */
export function tileName(t: PosTile): string {
  return t.variant_label ? `${t.name} ${t.variant_label}` : t.name;
}

/** A code typed in the scan field that matches no other tile, so Enter adds this one. */
function scansUniquely(t: PosTile, tiles: readonly PosTile[]): boolean {
  const code = t.sku_code.toLowerCase();
  return tiles.every((o) => o === t || ![o.sku_code, o.name, o.variant_label].some((s) => s.toLowerCase().includes(code)));
}

/** Sellable quantity per color of a tile (colored stock only). */
function coloredStock(t: PosTile): { color_id: string; qty: number }[] {
  return t.stock.flatMap((s) => (s.color_id !== null ? [{ color_id: s.color_id, qty: Number(s.qty) }] : []));
}

/** A priced tile stocked in at least two colors with `min` or more of each. */
export function coloredTile(tiles: readonly PosTile[], min: number): { tile: PosTile; colorIds: [string, string] } | null {
  for (const t of tiles) {
    if (t.retail_price === null || !scansUniquely(t, tiles)) continue;
    const rich = coloredStock(t).filter((s) => s.qty >= min);
    const [a, b] = rich;
    if (a && b) return { tile: t, colorIds: [a.color_id, b.color_id] };
  }
  return null;
}

/** A priced tile without colors that has `min` or more in stock. */
export function uncoloredTile(tiles: readonly PosTile[], min: number): PosTile | null {
  return (
    tiles.find(
      (t) =>
        t.retail_price !== null &&
        scansUniquely(t, tiles) &&
        t.stock.every((s) => s.color_id === null) &&
        t.stock.reduce((sum, s) => sum + Number(s.qty), 0) >= min,
    ) ?? null
  );
}

export async function activeCustomers(page: Page): Promise<SeedCustomer[]> {
  const r = await apiGet<{ data: SeedCustomer[] }>(page, "/customers?status=active&limit=200");
  return r.data.filter((c) => c.status === "active");
}

export async function receivableOf(page: Page, partyId: string): Promise<number> {
  const r = await apiGet<{ receivable: string }>(page, `/debts/parties/${partyId}`);
  return Number(r.receivable);
}

/** Color names by API color id, as the color pickers label them ("Đỏ"). */
export async function colorLabels(page: Page): Promise<(colorId: string) => string> {
  const r = await apiGet<{ data: ApiColor[] }>(page, "/colors");
  const byId = new Map(r.data.flatMap((c) => (isColorCode(c.code) ? [[c.id, colorDef(c.code).label] as const] : [])));
  return (colorId) => {
    const label = byId.get(colorId);
    if (label === undefined) throw new Error(`no fixed color for ${colorId}`);
    return label;
  };
}

export function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Digits of a VND amount as shown ("185.000 ₫" → 185000). */
export function vndValue(text: string | null): number {
  return Number((text ?? "").replace(/\D/g, ""));
}

/** Success toasts (errors stay as `.toast.err`). */
export function okToast(page: Page): Locator {
  return page.locator(".toasts .toast:not(.err)");
}

export function errToast(page: Page): Locator {
  return page.locator(".toasts .toast.err");
}

/** Value of one figure of an impact summary or pay box ("Tổng tiền", "Khách trả thêm"). */
export function figure(scope: Page | Locator, label: string): Locator {
  return scope.locator(`.impact > div:has(> b:text-is("${label}")) > span`).first();
}

/** Document number a toast names in bold ("Đã ghi sổ <b>HD-…</b> …"). */
export async function docNoIn(toast: Locator): Promise<string> {
  const text = (await toast.locator("b").first().textContent())?.trim() ?? "";
  expect(text, "toast names a document").not.toBe("");
  return text;
}

/** Types into a search field and picks the option whose name matches. */
export async function pickOption(scope: Page | Locator, combobox: Locator, query: string, option: RegExp): Promise<void> {
  await combobox.click();
  await combobox.fill(query);
  await scope.getByRole("option", { name: option }).first().click();
}

/** Option of a product picker: its detail starts with the SKU code. */
export function skuOption(code: string): RegExp {
  return new RegExp(`${escapeRe(code)}( · |$)`);
}

/** Radio of a color picker ("Đỏ 12"). */
export function colorRadio(group: Locator, label: string): Locator {
  return group.getByRole("radio", { name: new RegExp(`^${escapeRe(label)}( |$)`) });
}

/** POS: scans a tile's code into the cart and, for a colored product, picks the color the customer takes. */
export async function scanIntoCart(page: Page, tile: PosTile, colorLabel: string | null): Promise<void> {
  const scan = page.getByLabel("Tìm hoặc quét hàng");
  await scan.fill(tile.sku_code);
  await scan.press("Enter");
  await expect(page.getByRole("textbox", { name: `Số lượng – ${tileName(tile)}` })).toBeVisible();
  if (colorLabel !== null) await colorRadio(page.getByRole("radiogroup", { name: "Màu khách lấy" }).last(), colorLabel).click();
  await expect(page.locator(".cart-total")).not.toHaveText(/^0\s₫$/);
}

/** Sells one unit at the POS to a walk-in buyer who pays exactly; returns the sale document number. */
export async function sellAtPos(page: Page, tile: PosTile, colorLabel: string | null): Promise<string> {
  await page.goto("/sales/pos");
  await scanIntoCart(page, tile, colorLabel);
  await page.getByRole("group", { name: "Tiền khách đưa nhanh" }).getByRole("button").first().click();
  await page.getByRole("button", { name: "Thanh toán · F9" }).click();
  const toast = okToast(page).filter({ hasText: /^Đã ghi sổ / });
  await expect(toast).toBeVisible();
  return docNoIn(toast);
}
