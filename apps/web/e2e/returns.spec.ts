import { expect, test, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE } from "./support/real-api";
import {
  colorLabels,
  colorRadio,
  coloredTile,
  escapeRe,
  figure,
  okToast,
  pickOption,
  posTiles,
  sellAtPos,
  tileName,
  uncoloredTile,
  vndValue,
  type PosTile,
} from "./support/sales-data";

// Returns and exchanges against the running API with the seeded store's owner. Each journey first
// sells one walk-in sale at the POS, then takes goods back against that sale document, so it never
// depends on documents the seed may or may not hold.

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

/** Opens a returns-screen dialog and points it at the sale document just posted. */
async function openAgainst(page: Page, button: string, title: string, docNo: string) {
  await page.goto("/sales/returns");
  await page.getByRole("button", { name: button, exact: true }).click();
  // Exact: the drawer opened after posting may carry the same words before its number.
  const dialog = page.getByRole("dialog", { name: title, exact: true });
  await pickOption(dialog, dialog.getByRole("combobox", { name: "Chứng từ bán gốc" }), docNo, new RegExp(`^${escapeRe(docNo)}`));
  await expect(dialog.getByRole("combobox", { name: "Chứng từ bán gốc" })).toHaveValue(docNo);
  return dialog;
}

test("takes one unit back from a walk-in sale and pays the customer back from the fund", async ({ page }) => {
  await page.goto("/sales/pos");
  const tiles = await posTiles(page);
  const plain = uncoloredTile(tiles, 1);
  const colored = plain === null ? coloredTile(tiles, 1) : null;
  test.skip(plain === null && colored === null, "the seeded store has no priced product in stock");
  const tile: PosTile | null = plain ?? colored?.tile ?? null;
  if (tile === null) return;
  const colorLabel = colored === null ? null : (await colorLabels(page))(colored.colorIds[0]);
  const docNo = await sellAtPos(page, tile, colorLabel);

  const dialog = await openAgainst(page, "Tạo phiếu trả", "Tạo phiếu trả hàng", docNo);
  const qty = dialog.getByRole("table", { name: "Hàng còn được trả" }).getByRole("textbox", { name: `Số lượng trả – ${tileName(tile)}` });
  await qty.fill("1");
  await qty.press("Tab");

  // A walk-in sale leaves no debt to reduce: the whole value is paid back.
  await expect(figure(dialog, "Giá trị trả (theo giá gốc)")).not.toHaveText(/^0\s₫$/);
  await expect(figure(dialog, "Giảm nợ khách")).toHaveText(/^0\s₫$/);
  const value = vndValue(await figure(dialog, "Giá trị trả (theo giá gốc)").textContent());
  expect(vndValue(await figure(dialog, "Khoản cần hoàn").textContent())).toBe(value);
  await expect(dialog.getByLabel("Hoàn tiền từ quỹ")).toBeVisible();
  await expect(figure(dialog, "Kho")).toHaveText("Vào khu cách ly");

  await dialog.getByRole("button", { name: "Ghi sổ trả hàng" }).click();
  await expect(okToast(page).filter({ hasText: /^Đã ghi sổ / })).toHaveText(/^Đã ghi sổ \S+, hoàn khách bằng \S+\.$/);
  await expect(dialog).toHaveCount(0);
});

test("exchanges a unit for another color, takes one more and collects the difference", async ({ page }) => {
  await page.goto("/sales/pos");
  const found = coloredTile(await posTiles(page), 2);
  test.skip(found === null, "the seeded store has no priced product stocked in two colors with 2 or more of each");
  if (found === null) return;
  const label = await colorLabels(page);
  const [colorA, colorB] = [label(found.colorIds[0]), label(found.colorIds[1])];
  const docNo = await sellAtPos(page, found.tile, colorA);

  const dialog = await openAgainst(page, "Đổi hàng", "Đổi hàng", docNo);
  const back = dialog.getByRole("table", { name: "Hàng khách trả lại" });
  const qty = back.getByRole("textbox", { name: `Số lượng trả – ${tileName(found.tile)}` });
  await qty.fill("1");
  await qty.press("Tab");
  await back.getByRole("button", { name: "Đổi sang màu khác" }).click();
  await colorRadio(dialog.getByRole("radiogroup", { name: "Màu khách lấy" }).last(), colorB).click();
  await expect(colorRadio(dialog.getByRole("radiogroup", { name: "Màu khách lấy" }).last(), colorB)).toBeChecked();

  // Two units taken for one returned: the customer pays the difference, as the server previews it.
  const previewed = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname.endsWith("/exchanges:preview") && r.ok());
  const taken = dialog.getByRole("textbox", { name: `Số lượng – ${tileName(found.tile)} ${colorB}`, exact: true });
  await taken.fill("2");
  await taken.press("Tab");
  await previewed;
  await expect(figure(dialog, "Khách trả thêm")).not.toHaveText(/^0\s₫$/);
  await expect(figure(dialog, "Giá trị hàng lấy")).not.toHaveText(/^0\s₫$/);

  await dialog.getByRole("button", { name: "Ghi sổ đổi hàng" }).click();
  await expect(okToast(page).filter({ hasText: /^Đã ghi sổ / })).toContainText("chứng từ bán mới");
  await expect(dialog).toHaveCount(0);
});
