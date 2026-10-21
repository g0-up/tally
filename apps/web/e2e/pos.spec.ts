import { expect, test, type Page, type Request } from "@playwright/test";
import { dec } from "../src/shared/lib/decimal";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE } from "./support/real-api";
import {
  activeCustomers,
  colorLabels,
  coloredTile,
  docNoIn,
  escapeRe,
  okToast,
  pickOption,
  posTiles,
  scanIntoCart,
  tileName,
  vndValue,
  type PosTile,
} from "./support/sales-data";
import { createCandy, receiveInUnits, skuByCode } from "./support/units-data";

// POS checkout against the running API with the seeded store's owner. Each journey sells or parks a
// real cart; the server answers that the browser cannot provoke on demand (a shift that changed
// under the sale, a credit limit the owner must confirm) are injected on the checkout route only.
// Goods sold in several units are created and stocked through the API for their own journey.

test.use(OWNER.password === "" ? {} : { storageState: OWNER_STATE_FILE });

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

const CHECKOUT_PATH = "/api/v1/pos/checkout";
const PAY = "Thanh toán · F9";

const saleToast = (page: Page) => okToast(page).filter({ hasText: /^Đã ghi sổ / });
const payButton = (page: Page) => page.getByRole("button", { name: PAY });
const exactChip = (page: Page) => page.getByRole("group", { name: "Tiền khách đưa nhanh" }).getByRole("button").first();

/** Checkout requests of an unsaved cart sent by the page from now on. */
function recordCheckouts(page: Page): Request[] {
  const sent: Request[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === CHECKOUT_PATH) sent.push(r);
  });
  return sent;
}

/** A product sold in two colors with at least two of each, and the names of those colors. */
async function colorScenario(page: Page): Promise<{ tile: PosTile; colorA: string; colorB: string }> {
  await page.goto("/sales/pos");
  const found = coloredTile(await posTiles(page), 2);
  test.skip(found === null, "the seeded store has no priced product stocked in two colors with 2 or more of each");
  if (found === null) throw new Error("unreachable");
  const label = await colorLabels(page);
  return { tile: found.tile, colorA: label(found.colorIds[0]), colorB: label(found.colorIds[1]) };
}

test("sells a scanned product in the color picked, gives change and confirms only after the server posted", async ({ page }) => {
  const { tile, colorA } = await colorScenario(page);
  await scanIntoCart(page, tile, colorA);
  const total = vndValue(await page.locator(".cart-total").textContent());

  await page.getByLabel("Tiền mặt khách đưa").fill(String(total + 50_000));
  await page.getByLabel("Tiền mặt khách đưa").press("Tab");
  await expect(page.getByRole("status").filter({ hasText: /^Trả lại khách:/ })).toContainText(/Trả lại khách: 50\.000\s₫/);

  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**${CHECKOUT_PATH}`, async (route) => {
    await held;
    await route.continue();
  });
  const sent = page.waitForRequest((r) => r.method() === "POST" && new URL(r.url()).pathname === CHECKOUT_PATH);
  await payButton(page).click();
  const request = await sent;
  const body = request.postDataJSON() as { lines: { qty: string; color_id?: string }[]; cash_tendered_amount?: string };
  expect(body.lines).toHaveLength(1);
  expect(body.lines[0]?.color_id).toBeTruthy();
  expect(Number(body.cash_tendered_amount)).toBe(total + 50_000);
  expect(request.headers()["idempotency-key"]).toBeTruthy();

  // Nothing says "sold" while the server has not answered.
  await expect(payButton(page)).toBeDisabled();
  await expect(saleToast(page)).toHaveCount(0);
  release();

  await expect(saleToast(page)).toHaveText(/^Đã ghi sổ \S+ · .+ · trả lại 50\.000\s₫\.$/);
  await expect(page.getByRole("heading", { name: "Giỏ hàng mới" })).toBeVisible();
  await expect(page.getByText("Giỏ hàng trống.", { exact: false })).toBeVisible();
});

test("posts a double-clicked checkout once", async ({ page }) => {
  const { tile, colorA } = await colorScenario(page);
  await scanIntoCart(page, tile, colorA);
  await exactChip(page).click();
  const sent = recordCheckouts(page);

  await payButton(page).dblclick();
  await expect(saleToast(page)).toBeVisible();
  expect(sent).toHaveLength(1);
});

test("resends a checkout refused for a changed shift with the same key and sells once", async ({ page }) => {
  const { tile, colorA } = await colorScenario(page);
  await scanIntoCart(page, tile, colorA);
  await exactChip(page).click();
  const sent = recordCheckouts(page);

  let refused = false;
  await page.route(`**${CHECKOUT_PATH}`, async (route) => {
    if (refused) return route.continue();
    refused = true;
    return route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "shift_changed", message: "Ca bán hàng vừa được đóng và mở lại." } }),
    });
  });
  await payButton(page).click();

  await expect(saleToast(page)).toBeVisible();
  expect(sent).toHaveLength(2);
  const keys = sent.map((r) => r.headers()["idempotency-key"]);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  await expect(page.locator(".toasts .toast.err")).toHaveCount(0);
});

test("sells the quantity typed right before the click", async ({ page }) => {
  const { tile, colorA } = await colorScenario(page);
  await scanIntoCart(page, tile, colorA);
  const unitTotal = vndValue(await page.locator(".cart-total").textContent());
  await page.getByLabel("Tiền mặt khách đưa").fill(String(unitTotal * 3));
  await page.getByLabel("Tiền mặt khách đưa").press("Tab");

  // The quantity is still in the field (not committed) when "Thanh toán" is clicked.
  await page.getByRole("textbox", { name: `Số lượng – ${tileName(tile)}` }).fill("2");
  const sent = page.waitForRequest((r) => r.method() === "POST" && new URL(r.url()).pathname === CHECKOUT_PATH);
  await payButton(page).click();

  const body = (await sent).postDataJSON() as { lines: { qty: string }[] };
  expect(Number(body.lines[0]?.qty)).toBe(2);
  await expect(saleToast(page)).toBeVisible();
});

test("parks a cart, edits and sells it, and then only lets it be copied", async ({ page }) => {
  const { tile, colorA } = await colorScenario(page);
  await scanIntoCart(page, tile, colorA);
  await page.getByRole("button", { name: "Lưu giỏ · F4" }).click();
  const heldToast = okToast(page).filter({ hasText: /^Đã lưu giỏ / });
  await expect(heldToast).toHaveText(/của Khách lẻ\. Giỏ lưu chưa giữ hàng\.$/);
  const cartNo = await docNoIn(heldToast);
  await expect(page.getByRole("heading", { name: "Giỏ hàng mới" })).toBeVisible();

  const saved = page.getByRole("table", { name: "Giỏ hàng đã lưu" });
  await saved.getByRole("row").filter({ hasText: cartNo }).getByRole("button", { name: `Sửa giỏ ${cartNo}`, exact: true }).click();
  await expect(page.getByRole("heading", { name: `Giỏ hàng ${cartNo}` })).toBeVisible();
  await expect(page.getByRole("button", { name: "Lưu thay đổi · F4" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: `Số lượng – ${tileName(tile)}` })).toBeVisible();
  await expect(page.locator(".cart-total")).not.toHaveText(/^0\s₫$/);

  await exactChip(page).click();
  await payButton(page).click();
  await expect(saleToast(page)).toContainText(`Giỏ ${cartNo} đã khóa.`);

  await page.getByRole("group", { name: "Lọc giỏ hàng" }).getByRole("button", { name: /^Đã bán/ }).click();
  const soldRow = saved.getByRole("row").filter({ hasText: cartNo });
  await expect(soldRow.getByRole("button", { name: `Sao chép giỏ ${cartNo}`, exact: true })).toBeVisible();
  await expect(soldRow.getByRole("button", { name: `Sửa giỏ ${cartNo}`, exact: true })).toHaveCount(0);
  await expect(soldRow.getByRole("button", { name: `Xóa giỏ ${cartNo}`, exact: true })).toHaveCount(0);
});

test("asks the owner to confirm a credit sale over the customer's limit and sells nothing when cancelled", async ({ page }) => {
  const { tile, colorA } = await colorScenario(page);
  const customer = (await activeCustomers(page))[0];
  test.skip(customer === undefined, "the seeded store has no active customer");
  if (customer === undefined) return;

  await pickOption(page, page.getByRole("combobox", { name: "Đổi khách hàng" }), customer.name, new RegExp(`^${escapeRe(customer.name)}`));
  await scanIntoCart(page, tile, colorA);
  const credit = page.getByRole("checkbox", { name: /^Bán chịu phần còn lại / });
  await credit.check();

  const sent = recordCheckouts(page);
  await page.route(`**${CHECKOUT_PATH}`, (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "exception_required", message: "Vượt hạn mức nợ của khách.", exception_kind: "credit_limit_exceeded" } }),
    }),
  );
  await payButton(page).click();

  const dialog = page.getByRole("dialog", { name: "Xác nhận ngoại lệ cho giỏ hàng mới" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Vượt hạn mức nợ:");
  await expect(dialog.getByRole("checkbox", { name: "Tôi xác nhận vẫn bán" })).not.toBeChecked();
  await dialog.getByRole("button", { name: "Hủy" }).click();

  await expect(dialog).toHaveCount(0);
  expect(sent).toHaveLength(1);
  await expect(saleToast(page)).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: `Số lượng – ${tileName(tile)}` })).toBeVisible();
});

test("sells a dây at its own price and a thùng at base price × factor, taking 550 cái off stock", async ({ page }) => {
  await page.goto("/sales/pos");
  const candy = await createCandy(page);
  await receiveInUnits(page, candy.skuId, [{ unitId: candy.units.thung, qty: "2", unitPrice: "200000" }]);
  await page.reload();
  const before = dec((await skuByCode(page, candy.code)).on_hand);
  expect(before.gte(550), "the 2 thùng received are on hand").toBe(true);

  const unitSelects = page.getByRole("combobox", { name: new RegExp(`^Đơn vị – ${escapeRe(candy.name)}`) });
  const lines = page.getByRole("row").filter({ has: unitSelects });
  /** The cart line of the candy that is counted in this unit. */
  const lineIn = (unit: string) => lines.filter({ has: page.locator("option:checked").filter({ hasText: new RegExp(`^${escapeRe(unit)}$`) }) });
  const scan = async () => {
    await page.getByLabel("Tìm hoặc quét hàng").fill(candy.code);
    await page.getByLabel("Tìm hoặc quét hàng").press("Enter");
  };

  // A scan adds one cái; switching the line's unit prices it again for that unit.
  await scan();
  await expect(unitSelects).toHaveCount(1);
  await expect(lineIn("cái")).toContainText(/500\s₫/);
  await lineIn("cái").getByRole("combobox").selectOption({ label: "dây" });
  await expect(lineIn("dây")).toContainText(/24\.000\s₫/);

  // The next scan of a cái is a line of its own, since the first one is now counted in dây.
  await scan();
  await expect(unitSelects).toHaveCount(2);
  await lineIn("cái").getByRole("combobox").selectOption({ label: "thùng" });
  await expect(lineIn("thùng")).toContainText(/250\.000\s₫/);
  await expect(lineIn("dây")).toContainText(/24\.000\s₫/);
  await expect.poll(async () => vndValue(await page.locator(".cart-total").textContent())).toBe(274_000);

  const sent = recordCheckouts(page);
  await exactChip(page).click();
  await payButton(page).click();
  await expect(saleToast(page)).toBeVisible();
  expect(sent).toHaveLength(1);
  const body = sent[0]?.postDataJSON() as { lines: { sku_id: string; unit_id?: string; qty: string }[] };
  expect(body.lines.map((l) => l.unit_id).sort()).toEqual([candy.units.day, candy.units.thung].sort());
  await expect.poll(async () => dec((await skuByCode(page, candy.code)).on_hand).toFixed()).toBe(before.minus(550).toFixed());
});
