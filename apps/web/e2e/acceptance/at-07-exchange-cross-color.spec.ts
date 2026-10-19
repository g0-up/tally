import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { colorLabels, colorRadio, coloredTile, escapeRe, okToast, pickOption, posTiles, sellAtPos, tileName } from "../support/sales-data";
import { finishJourney, onHandByColor, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-07: a walk-in buyer exchanges a unit for the same product in another color, at the counter.
// The stock moves between the two colors, the price difference is zero and the exchange is linked
// to the sale it came from.

test.beforeEach(() => requireOwner());

test("exchanges one unit to another color with no money moving and the stock following the color", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/sales/pos");

  const found = coloredTile(await posTiles(page), 2);
  test.skip(found === null, "the seeded store has no priced product stocked in two colors with 2 or more each");
  if (found === null) throw new Error("unreachable");
  const { tile, colorIds } = found;
  const [colorA = "", colorB = ""] = colorIds;
  const labelOf = await colorLabels(page);

  const saleDoc = await sellAtPos(page, tile, labelOf(colorA));
  const stockBefore = await onHandByColor(page, tile.sku_id);

  await page.goto("/sales/returns");
  await page.getByRole("button", { name: "Đổi hàng", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Đổi hàng", exact: true });
  await pickOption(dialog, dialog.getByRole("combobox", { name: "Chứng từ bán gốc" }), saleDoc, new RegExp(`^${escapeRe(saleDoc)}`));

  const returned = dialog.getByRole("table", { name: "Hàng còn được trả" }).getByRole("textbox", { name: `Số lượng trả – ${tileName(tile)}` });
  await returned.fill("1");
  await returned.press("Tab");

  const taken = dialog.getByRole("textbox", { name: `Số lượng – ${tileName(tile)} ${labelOf(colorB)}`, exact: true });
  await dialog.getByRole("button", { name: "Đổi sang màu khác" }).click();
  await colorRadio(dialog.getByRole("radiogroup", { name: "Màu khách lấy" }).last(), labelOf(colorB)).click();
  await taken.fill("1");
  await taken.press("Tab");
  await dialog.getByRole("button", { name: "Ghi sổ đổi hàng" }).click();
  await expect(okToast(page).filter({ hasText: /^Đã ghi / })).toBeVisible();

  const stockAfter = await onHandByColor(page, tile.sku_id);
  const delta = (color: string) => dec(stockAfter[color] ?? "0").minus(stockBefore[color] ?? "0");
  expect(delta(colorA).eq("1"), "the returned color gains one unit").toBe(true);
  expect(delta(colorB).eq("-1"), "the new color gives up one unit").toBe(true);
  await expect(dialog.getByText(/Thu \/ trả chênh lệch/)).toHaveCount(0);

  await finishJourney(page, testInfo, errors);
});
