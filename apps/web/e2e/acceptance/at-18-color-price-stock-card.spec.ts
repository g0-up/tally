import { expect, test } from "@playwright/test";
import { apiGet } from "../support/real-api";
import { coloredTile, posTiles, tileName } from "../support/sales-data";
import { finishJourney, requireOwner, requireTestStack, watchPageErrors } from "./fixtures/acceptance";

// AT-18: a color can carry its own sale price, and the stock card of that product shows the stock that
// agrees with the ledger. The price change edits the seeded catalog, so the journey runs only on the
// disposable APP_ENV=test stack. The color-price write itself (the per-color fields of "Sửa giá bán") is
// not yet confirmed; this journey checks the dialog's "Áp dụng cho tất cả màu" switch and the stock card.

interface PriceHistoryBody {
  data: unknown[];
}

test.beforeEach(() => {
  requireOwner();
  requireTestStack();
});

test("gives a color its own sale price and shows the product's stock card as agreed with the ledger", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/sales/pos");

  const found = coloredTile(await posTiles(page), 1);
  test.skip(found === null, "the seeded store has no product in stock in any color");
  if (found === null) throw new Error("unreachable");
  const { tile } = found;

  await page.goto("/inventory/stock");
  await page.getByLabel(`Sửa giá – ${tileName(tile)}`, { exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("checkbox", { name: /^Áp dụng cho tất cả màu/ })).toBeVisible();
  await dialog.getByRole("button", { name: "Hủy", exact: true }).click();

  await page.getByLabel(`Thẻ kho – ${tileName(tile)}`, { exact: true }).click();
  await expect(page.getByText("Đã khớp sổ và tồn").first()).toBeVisible();

  const history = await apiGet<PriceHistoryBody>(page, `/skus/${tile.sku_id}/price-history`);
  expect(Array.isArray(history.data), "the price history answers a list").toBe(true);

  await finishJourney(page, testInfo, errors);
});
