import { expect, test, type Locator, type Page } from "@playwright/test";
import { dec } from "../src/shared/lib/decimal";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";
import { escapeRe } from "./support/sales-data";
import { CANDY_PRICES, candyNames, candyUnits, createCandy, pickUnit, quotedPrice, skuByCode, unitOf } from "./support/units-data";

// Catalog journeys on the seeded store's real session. A mix group is built from two goods of the
// store that share a base unit, renamed, deactivated behind its confirmation, used again and
// deleted, so every run leaves the catalog as it found it. (Stopping a supplier that still has an
// open order is part of procurement.spec.ts, which creates that order.) Goods sold in several units
// are created fresh on every run, through the form or the API, and read back from the API.

interface SkuRow {
  id: string;
  code: string;
  display_name: string;
  base_unit_id: string;
  in_mix_group: boolean;
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/** Two active goods with the same base unit, preferring ones in no mix group yet. */
async function twoFlavors(page: Page): Promise<[SkuRow, SkuRow] | null> {
  const r = await apiGet<{ data: SkuRow[] }>(page, "/skus?type=goods&status=active&limit=200");
  const byUnit = new Map<string, SkuRow[]>();
  for (const s of [...r.data].sort((a, b) => Number(a.in_mix_group) - Number(b.in_mix_group))) {
    byUnit.set(s.base_unit_id, [...(byUnit.get(s.base_unit_id) ?? []), s]);
  }
  for (const skus of byUnit.values()) {
    const [a, b] = skus;
    if (a && b) return [a, b];
  }
  return null;
}

/** Picks a SKU on a flavor row through the remote goods search. */
async function pickFlavor(dialog: Locator, index: number, sku: SkuRow): Promise<void> {
  const box = dialog.getByRole("combobox", { name: `Mặt hàng vị ${index + 1}`, exact: true });
  await box.click();
  await box.fill(sku.code);
  await dialog.page().getByRole("option").filter({ hasText: sku.code }).first().click();
  await expect(box).toHaveValue(`${sku.code} · ${sku.display_name}`);
}

const UNIT_TABLE = "Đơn vị tính & giá bán";

/**
 * Opens the products screen with the units the candy needs already in the directory, so the form's
 * default rows (base hộp, thùng = 12 hộp) and the unit lists are drawn from the directory as it is now.
 */
async function openProductsWithUnits(page: Page): Promise<void> {
  await openScreen(page, "/catalog/products");
  await candyUnits(page);
  await page.reload();
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

/** The "Thêm hàng hóa" dialog once its unit table is drawn (a loading dialog comes first). */
async function openNewProduct(page: Page): Promise<{ dialog: Locator; table: Locator }> {
  await page.getByRole("button", { name: "Thêm hàng hóa" }).click();
  const dialog = page.getByRole("dialog", { name: "Thêm hàng hóa" });
  const table = dialog.getByRole("group", { name: UNIT_TABLE });
  await expect(table).toBeVisible();
  return { dialog, table };
}

/** Product creations the page sends from now on. */
function recordProductPosts(page: Page): string[] {
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/products") sent.push(r.url());
  });
  return sent;
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

  test("creates, renames, deactivates, reuses and deletes a mix group", async ({ page }) => {
    const stamp = Date.now().toString(36);
    const name = `Mix e2e ${stamp}`;
    const renamed = `Mix e2e ${stamp} (sửa)`;

    await openScreen(page, "/catalog/mix-groups");
    const flavors = await twoFlavors(page);
    test.skip(flavors === null, "the seeded store has no two active goods with the same base unit");
    if (flavors === null) return;
    const [first, second] = flavors;

    await page.getByRole("button", { name: "Thêm nhóm mix" }).click();
    const create = page.getByRole("dialog", { name: "Thêm nhóm mix vị" });
    await expect(create.getByRole("combobox", { name: "Đơn vị thùng" })).toHaveValue("thùng");
    await expect(create.getByRole("combobox", { name: "Đơn vị chọn vị" })).toHaveValue("dây");
    await create.getByRole("textbox", { name: "Tên nhóm mix" }).fill(name);
    await pickFlavor(create, 0, first);
    await pickFlavor(create, 1, second);
    await create.getByRole("textbox", { name: `Tên vị ngắn ${first.code}`, exact: true }).fill("Vị A");
    await create.getByRole("textbox", { name: `Tên vị ngắn ${second.code}`, exact: true }).fill("Vị B");
    await create.getByRole("button", { name: "Tạo nhóm" }).click();
    await expect(page.getByText(/^Đã lưu nhóm mix/).first()).toContainText(name);
    await expect(create).toBeHidden();

    const row = (groupName: string) => page.getByRole("row").filter({ has: page.getByText(groupName, { exact: true }) });
    await expect(row(name)).toContainText("Vị A");
    await expect(row(name)).toContainText("Vị B");
    await expect(row(name)).toContainText("Đang dùng");

    await row(name).getByRole("button", { name: "Sửa" }).click();
    const edit = page.getByRole("dialog", { name: "Sửa nhóm mix vị" });
    await expect(edit.getByRole("textbox", { name: "Tên nhóm mix" })).toHaveValue(name);
    await edit.getByRole("textbox", { name: "Tên nhóm mix" }).fill(renamed);
    await edit.getByRole("button", { name: "Lưu", exact: true }).click();
    await expect(edit).toBeHidden();
    await expect(row(renamed)).toHaveCount(1);

    await row(renamed).getByRole("button", { name: "Ngừng dùng" }).click();
    const stop = page.getByRole("dialog", { name: `Ngừng dùng nhóm mix ${renamed}?` });
    await expect(stop).toContainText("Chứng từ cũ vẫn giữ nguyên.");
    await stop.getByRole("button", { name: "Ngừng dùng" }).click();
    await expect(page.getByText("Đã ngừng dùng nhóm mix. Chứng từ cũ vẫn giữ nguyên.").first()).toBeVisible();
    await expect(stop).toBeHidden();
    await expect(row(renamed)).toContainText("Ngừng dùng");
    await expect(row(renamed).getByRole("button", { name: "Dùng lại" })).toBeVisible();

    await row(renamed).getByRole("button", { name: "Dùng lại" }).click();
    await expect(page.getByText("Đã dùng lại nhóm mix.").first()).toBeVisible();
    await expect(row(renamed).getByRole("button", { name: "Ngừng dùng" })).toBeVisible();

    await row(renamed).getByRole("button", { name: "Xóa" }).click();
    const remove = page.getByRole("dialog", { name: `Xóa nhóm mix ${renamed}?` });
    await remove.getByRole("button", { name: "Xóa", exact: true }).click();
    await expect(page.getByText("Đã xóa nhóm mix.").first()).toBeVisible();
    await expect(row(renamed)).toHaveCount(0);
  });

  test("creates a product sold by the cái, the dây at its own price and the thùng at base price × factor", async ({ page }) => {
    await openProductsWithUnits(page);
    const { code, name } = candyNames();
    const { dialog, table } = await openNewProduct(page);

    await dialog.getByRole("textbox", { name: "Mã SKU" }).fill(code);
    await dialog.getByRole("textbox", { name: "Tên hàng" }).fill(name);
    await pickUnit(table.getByRole("combobox", { name: "Đơn vị cơ sở" }), "cái");
    await table.getByRole("textbox", { name: "Giá bán lẻ / cái" }).fill(CANDY_PRICES.cai);

    // The form starts with thùng as its second unit; it becomes 500 cái with no price of its own.
    await expect(table.getByRole("combobox", { name: "Tên đơn vị 2" })).toHaveValue("thùng");
    await table.getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu cái" }).fill("500");
    await table.getByRole("button", { name: "+ Thêm đơn vị" }).click();
    await expect(table.getByRole("combobox", { name: "Tên đơn vị 3" })).toBeFocused();
    await pickUnit(table.getByRole("combobox", { name: "Tên đơn vị 3" }), "dây");
    await table.getByRole("textbox", { name: "Quy đổi: 1 dây bằng bao nhiêu cái" }).fill("50");
    await table.getByRole("textbox", { name: "Giá bán lẻ / dây" }).fill(CANDY_PRICES.day);

    await expect(table.getByRole("textbox", { name: "Giá bán lẻ / thùng" })).toHaveValue("");
    await expect(table.getByRole("textbox", { name: "Giá bán lẻ / thùng" })).toHaveAttribute("placeholder", "250.000 tự tính");
    await expect(dialog).toContainText("1 thùng = 500 cái");
    await expect(dialog).toContainText("1 dây = 50 cái");

    await dialog.getByRole("button", { name: "Lưu", exact: true }).click();
    await expect(page.getByText("Đã thêm hàng hóa.").first()).toBeVisible();
    await expect(dialog).toBeHidden();

    const sku = await skuByCode(page, code);
    expect(sku.units).toHaveLength(3);
    expect(unitOf(sku, "cái").is_base).toBe(true);
    expect(dec(sku.retail_price ?? "0").eq(CANDY_PRICES.cai), "retail price of one cái").toBe(true);
    const day = unitOf(sku, "dây");
    expect(dec(day.factor).eq(50), "1 dây = 50 cái").toBe(true);
    expect(dec(day.retail_price ?? "0").eq(CANDY_PRICES.day), "the dây's own price").toBe(true);
    const thung = unitOf(sku, "thùng");
    expect(dec(thung.factor).eq(500), "1 thùng = 500 cái").toBe(true);
    expect(thung.retail_price, "the thùng has no price of its own").toBeNull();
    expect(dec(await quotedPrice(page, sku.id, day.unit_id)).eq(CANDY_PRICES.day), "a dây sells at its own price").toBe(true);
    expect(dec(await quotedPrice(page, sku.id, thung.unit_id)).eq(CANDY_PRICES.thung), "a thùng sells at 500 × 500").toBe(true);
  });

  test("clears the dây's own price in Đơn vị & giá so the next sale prices it at base price × factor", async ({ page }) => {
    await openScreen(page, "/catalog/products");
    const candy = await createCandy(page);

    await page.getByPlaceholder("Tìm mã, tên, mã vạch…").fill(candy.code);
    const listed = page.getByRole("table", { name: "Hàng hóa" }).getByRole("row").filter({ hasText: candy.code });
    await expect(listed).toHaveCount(1);
    await listed.getByRole("button", { name: new RegExp(`^Đơn vị & giá ${escapeRe(candy.name)}`) }).click();
    const dialog = page.getByRole("dialog", { name: new RegExp(`^Đơn vị & giá · ${escapeRe(candy.name)}`) });
    const table = dialog.getByRole("group", { name: UNIT_TABLE });
    await expect(table).toBeVisible();

    const dayPrice = table.getByRole("textbox", { name: "Giá bán lẻ / dây" });
    await expect(dayPrice).not.toHaveValue("");
    await dayPrice.fill("");
    await expect(dayPrice).toHaveAttribute("placeholder", "25.000 tự tính");
    await dialog.getByRole("button", { name: "Lưu đơn vị & giá" }).click();
    await expect(page.getByText("Đã lưu đơn vị & giá. Bán hàng, báo giá và đơn hàng mới dùng ngay.").first()).toBeVisible();
    await expect(dialog).toBeHidden();

    const sku = await skuByCode(page, candy.code);
    expect(unitOf(sku, "dây").retail_price, "the dây has no price of its own any more").toBeNull();
    expect(dec(await quotedPrice(page, candy.skuId, candy.units.day)).eq(CANDY_PRICES.dayAuto), "a dây now sells at 50 × 500").toBe(true);
  });

  test("shows a duplicate unit and a zero factor under their fields and sends nothing", async ({ page }) => {
    await openProductsWithUnits(page);
    const { code, name } = candyNames();
    const posts = recordProductPosts(page);
    const { dialog, table } = await openNewProduct(page);

    await dialog.getByRole("textbox", { name: "Mã SKU" }).fill(code);
    await dialog.getByRole("textbox", { name: "Tên hàng" }).fill(name);
    await pickUnit(table.getByRole("combobox", { name: "Đơn vị cơ sở" }), "cái");
    await table.getByRole("textbox", { name: "Giá bán lẻ / cái" }).fill(CANDY_PRICES.cai);
    await expect(table.getByRole("combobox", { name: "Tên đơn vị 2" })).toHaveValue("thùng");
    // Both rows are named thùng now, so the first of the two factor fields is the second row's.
    await table.getByRole("button", { name: "+ Thêm đơn vị" }).click();
    await pickUnit(table.getByRole("combobox", { name: "Tên đơn vị 3" }), "thùng");
    const factors = table.getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu cái" });
    await factors.first().fill("0");
    await factors.last().fill("500");

    await dialog.getByRole("button", { name: "Lưu", exact: true }).click();
    await expect(factors.first()).toHaveAccessibleDescription("Hệ số phải lớn hơn 0.");
    await expect(table.getByRole("combobox", { name: "Tên đơn vị 3" })).toHaveAccessibleDescription("Đơn vị “thùng” bị trùng.");
    await expect(table.getByText("Hệ số phải lớn hơn 0.", { exact: true })).toBeVisible();
    await expect(table.getByText("Đơn vị “thùng” bị trùng.", { exact: true })).toBeVisible();
    await expect(dialog).toBeVisible();
    expect(posts, "a product with unit errors is not sent").toEqual([]);
  });
});
