import { expect, test, type Page } from "@playwright/test";
import { dec } from "../src/shared/lib/decimal";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";
import { escapeRe } from "./support/sales-data";
import { CANDY_PRICES, createCandy, receiveInUnits, skuByCode, unitOf } from "./support/units-data";

// Inventory journeys on the seeded store's real session: a blind count posted with a variance
// behind its exception confirmation and read back on the stock card, an expired lot disposed of,
// a quarantined lot released, and a decrease adjustment. Each journey picks its goods from the
// API and skips when the seeded store has none that fit; each moves at most one unit of stock.
// Goods kept in several units are created and received through the API for their own journey.

interface Page_<T> {
  data: T[];
}
interface StockRow {
  sku_id: string;
  code: string;
  name: string;
  unit_name: string;
  category_id: string | null;
  track_lot: boolean;
  on_hand: string;
  sellable: string;
  colors: { color_id: string | null }[];
}
interface Category {
  id: string;
  name: string;
}
interface LotRow {
  id: string;
  lot_no: string;
  sku_name: string;
  qty: string;
}

/** Stock without a color still lists as one entry of `colors`, under a null color. */
const uncolored = (r: StockRow): boolean => r.colors.every((c) => c.color_id === null);

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/**
 * A category whose stock is a handful of plain goods (no lots, no colors, one line each on a count)
 * with at least one unit on hand, so the count can be keyed in full from the stock rows.
 */
async function countableCategory(page: Page): Promise<{ category: Category; rows: StockRow[] } | null> {
  const categories = await apiGet<Page_<Category>>(page, "/categories");
  for (const category of categories.data) {
    const rows = (await apiGet<Page_<StockRow>>(page, `/stock?category_id=${category.id}&limit=10`)).data;
    const plain = rows.every((r) => !r.track_lot && uncolored(r));
    if (rows.length > 0 && rows.length <= 5 && plain && Number(rows[0]?.on_hand ?? 0) >= 1) return { category, rows };
  }
  return null;
}

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("posts a blind count with a variance behind its confirmation and reads it on the stock card", async ({ page }) => {
    await openScreen(page, "/inventory/counts");
    const pick = await countableCategory(page);
    test.skip(pick === null, "the seeded store has no small category of plain goods in stock");
    if (pick === null) return;
    const [short, ...rest] = pick.rows as [StockRow, ...StockRow[]];

    await page.getByRole("button", { name: "Bắt đầu phiên kiểm kê" }).click();
    const start = page.getByRole("dialog", { name: "Bắt đầu kiểm kê" });
    await start.getByRole("combobox", { name: "Nhóm hàng" }).selectOption(pick.category.id);
    await start.getByRole("button", { name: "Bắt đầu" }).click();
    const opened = page.getByText(/^Đã mở phiên KK-/).first();
    await expect(opened).toBeVisible();
    const docNo = /KK-[\w-]+/.exec((await opened.textContent()) ?? "")?.[0] ?? "";

    const drawer = page.getByRole("dialog", { name: `Kiểm kê ${docNo}` });
    await expect(drawer.getByText("Ẩn khi đếm mù").first()).toBeVisible();
    // One unit missing on the first product; the rest counted as the books say.
    const counts = [{ row: short, qty: Number(short.on_hand) - 1 }, ...rest.map((r) => ({ row: r, qty: Number(r.on_hand) }))];
    for (const { row, qty } of counts) {
      const box = drawer.getByRole("textbox", { name: `Thực đếm – ${row.name}` });
      await box.fill(String(qty));
      await box.blur();
    }
    await drawer.getByRole("button", { name: "Hoàn tất đếm" }).click();
    await expect(page.getByText("Đã hoàn tất đếm. Kiểm tra chênh lệch rồi xác nhận ghi sổ.").first()).toBeVisible();

    await drawer.getByRole("button", { name: "Xác nhận & ghi sổ chênh lệch" }).click();
    const confirm = page.getByRole("dialog", { name: `Xác nhận ghi sổ chênh lệch ${docNo}` });
    await expect(confirm).toContainText("Chênh lệch -1");
    await confirm.getByRole("checkbox", { name: "Tôi xác nhận ghi sổ chênh lệch" }).check();
    await confirm.getByRole("button", { name: "Xác nhận & ghi sổ" }).click();
    await expect(page.getByText("Đã ghi sổ chênh lệch kiểm kê.").first()).toBeVisible();

    await openScreen(page, `/inventory/stock-card?sku=${short.sku_id}`);
    await expect(page.getByRole("table", { name: `Thẻ kho ${short.code}` })).toContainText(docNo);
    await expect(page.getByText("Đã khớp sổ và tồn")).toBeVisible();
  });

  test("disposes of an expired lot with no revenue", async ({ page }) => {
    await openScreen(page, "/inventory/lots?loc=expired");
    const lot = (await apiGet<Page_<LotRow>>(page, "/lots?state=expired&limit=1")).data[0];
    test.skip(lot === undefined, "the seeded store has no expired lot");
    if (lot === undefined) return;

    const row = page.getByRole("row").filter({ has: page.getByText(lot.lot_no, { exact: true }) });
    await row.getByRole("button", { name: "Tiêu hủy" }).click();
    const dialog = page.getByRole("dialog", { name: "Tiêu hủy lô hết hạn" });
    await expect(dialog).toContainText("Không phát sinh");
    const reason = dialog.getByRole("combobox", { name: /^Lý do/ });
    const first = await reason.locator("option:not([value=''])").first().getAttribute("value");
    await reason.selectOption(first ?? "");
    await dialog.getByRole("button", { name: "Ghi sổ tiêu hủy" }).click();
    await expect(page.getByText("Đã ghi tiêu hủy.").first()).toBeVisible();
    await expect(dialog).toBeHidden();
  });

  test("releases a quarantined lot back to sellable stock", async ({ page }) => {
    await openScreen(page, "/inventory/lots?loc=quarantine");
    const lot = (await apiGet<Page_<LotRow>>(page, "/lots?state=quarantine&limit=1")).data[0];
    test.skip(lot === undefined, "the seeded store has no quarantined lot");
    if (lot === undefined) return;

    await expect(page.getByRole("button", { name: "Khu cách ly" })).toHaveAttribute("aria-pressed", "true");
    const row = page.getByRole("row").filter({ has: page.getByText(lot.lot_no, { exact: true }) });
    await row.getByRole("button", { name: "Đạt – cho bán" }).click();
    await expect(page.getByText("Hàng đã trở lại tồn bán được.").first()).toBeVisible();
  });

  test("writes one unit off with a decrease adjustment that books no revenue", async ({ page }) => {
    await openScreen(page, "/inventory/stock");
    const rows = (await apiGet<Page_<StockRow>>(page, "/stock?limit=50")).data;
    const row = rows.find((r) => Number(r.sellable) >= 1 && uncolored(r) && !r.track_lot);
    test.skip(row === undefined, "the seeded store has no plain goods with sellable stock");
    if (row === undefined) return;

    await page.getByRole("button", { name: "Điều chỉnh kho" }).click();
    const dialog = page.getByRole("dialog", { name: "Điều chỉnh giảm kho" });
    const goods = dialog.getByRole("combobox", { name: "Hàng" });
    await goods.fill(row.code);
    await page.getByRole("option").filter({ hasText: row.name }).first().click();
    await dialog.getByRole("textbox", { name: /^Số lượng/ }).fill("1");
    const reason = dialog.getByRole("combobox", { name: /^Lý do/ });
    const first = await reason.locator("option:not([value=''])").first().getAttribute("value");
    await reason.selectOption(first ?? "");
    await expect(dialog).toContainText("Không phát sinh");
    await dialog.getByRole("button", { name: "Ghi sổ điều chỉnh" }).click();
    await expect(page.getByText("Đã ghi điều chỉnh.").first()).toBeVisible();
    await expect(dialog).toBeHidden();
  });

  test("shows stock of goods kept in several units, drops a unit that stops being used and still edits its price", async ({ page }) => {
    await openScreen(page, "/inventory/stock");
    const candy = await createCandy(page);
    await receiveInUnits(page, candy.skuId, [
      { unitId: candy.units.thung, qty: "2", unitPrice: "200000" },
      { unitId: candy.units.day, qty: "5", unitPrice: "20000" },
    ]);

    // Tồn kho: the unit line under the name, and 1.250 cái written as 2 thùng 5 dây.
    const stockRow = page.getByRole("table", { name: "Tồn kho" }).getByRole("row").filter({ hasText: candy.code });
    await openScreen(page, `/inventory/stock?q=${encodeURIComponent(candy.code)}`);
    await expect(stockRow).toHaveCount(1);
    await expect(stockRow).toContainText(`${candy.code} · ĐV cơ sở: cái`);
    await expect(stockRow).toContainText("1 dây = 50 cái");
    await expect(stockRow).toContainText("1 thùng = 500 cái");
    await expect(stockRow).toContainText("1.250");
    await expect(stockRow).toContainText("= 2 thùng 5 dây");

    // Thẻ kho offers a fixed display in each larger unit.
    const quantities = page.getByRole("group", { name: "Hiển thị số lượng" });
    await openScreen(page, `/inventory/stock-card?sku=${candy.skuId}`);
    await expect(quantities.getByRole("button", { name: "Theo dây (50 cái)" })).toBeVisible();
    await expect(quantities.getByRole("button", { name: "Theo thùng (500 cái)" })).toBeVisible();

    // The receipt locks thùng, so "Đơn vị & giá" offers "Ngừng dùng" instead of removal.
    await openScreen(page, "/catalog/products");
    await page.getByPlaceholder("Tìm mã, tên, mã vạch…").fill(candy.code);
    const listed = page.getByRole("table", { name: "Hàng hóa" }).getByRole("row").filter({ hasText: candy.code });
    await expect(listed).toHaveCount(1);
    await listed.getByRole("button", { name: new RegExp(`^Đơn vị & giá ${escapeRe(candy.name)}`) }).click();
    const units = page.getByRole("dialog", { name: new RegExp(`^Đơn vị & giá · ${escapeRe(candy.name)}`) });
    const table = units.getByRole("group", { name: "Đơn vị tính & giá bán" });
    await expect(table).toBeVisible();
    await expect(table.getByRole("button", { name: "Xóa đơn vị thùng" })).toHaveCount(0);
    await table.getByRole("checkbox", { name: "Ngừng dùng thùng" }).check();
    await units.getByRole("button", { name: "Lưu đơn vị & giá" }).click();
    await expect(page.getByText("Đã lưu đơn vị & giá. Bán hàng, báo giá và đơn hàng mới dùng ngay.").first()).toBeVisible();
    await expect(units).toBeHidden();
    expect(unitOf(await skuByCode(page, candy.code), "thùng").active, "thùng stopped").toBe(false);

    // Both screens leave the stopped unit out: 1.250 cái is now 25 dây.
    await openScreen(page, `/inventory/stock?q=${encodeURIComponent(candy.code)}`);
    await expect(stockRow).toContainText("1 dây = 50 cái");
    await expect(stockRow).toContainText("= 25 dây");
    await expect(stockRow).not.toContainText("thùng");
    await openScreen(page, `/inventory/stock-card?sku=${candy.skuId}`);
    await expect(quantities.getByRole("button", { name: "Theo dây (50 cái)" })).toBeVisible();
    await expect(quantities.getByRole("button", { name: /^Theo thùng/ })).toHaveCount(0);

    // "Sửa giá" from Tồn kho still saves the base price and keeps the dây's own price.
    await openScreen(page, `/inventory/stock?q=${encodeURIComponent(candy.code)}`);
    await stockRow.getByRole("button", { name: `Sửa giá – ${candy.name}` }).click();
    const price = page.getByRole("dialog", { name: new RegExp(`^Sửa giá bán · ${escapeRe(candy.name)}`) });
    await expect(price.getByRole("textbox", { name: "Giá bán lẻ / thùng" })).toHaveCount(0);
    await price.getByRole("textbox", { name: "Giá bán lẻ / cái" }).fill("520");
    await price.getByRole("button", { name: "Lưu giá" }).click();
    await expect(page.getByText("Đã cập nhật giá chung.").first()).toBeVisible();
    await expect(price).toBeHidden();

    const sku = await skuByCode(page, candy.code);
    expect(dec(sku.retail_price ?? "0").eq(520), "the new price of one cái").toBe(true);
    expect(dec(unitOf(sku, "dây").retail_price ?? "0").eq(CANDY_PRICES.day), "the dây keeps its own price").toBe(true);
  });
});
