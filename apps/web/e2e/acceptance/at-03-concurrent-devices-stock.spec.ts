import { expect, test, type Page } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { OWNER_STATE_FILE } from "../support/real-api";
import { colorLabels, coloredTile, errToast, okToast, posTiles, scanIntoCart, tileName, type PosTile } from "../support/sales-data";
import { finishJourney, onHandByColor, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-03: two devices of the same owner sell the last units of one color at the same moment. The
// stock of that color may not go below zero, exactly one sale wins, and the loser gets a clear error.

test.beforeEach(() => requireOwner());

/** One POS screen on its own device: scans the tile, takes every remaining unit of the color and pays. */
async function sellEverything(page: Page, tile: PosTile, colorLabel: string, qty: string): Promise<"sold" | "refused"> {
  await page.goto("/sales/pos");
  await scanIntoCart(page, tile, colorLabel);
  await page.getByRole("textbox", { name: `Số lượng – ${tileName(tile)}` }).fill(qty);
  await page.getByRole("textbox", { name: `Số lượng – ${tileName(tile)}` }).press("Tab");
  await page.getByRole("button", { name: "Thanh toán · F9" }).click();
  const sold = okToast(page).filter({ hasText: /^Đã ghi sổ / });
  const refused = errToast(page);
  await expect(sold.or(refused).first()).toBeVisible();
  return (await sold.count()) > 0 ? "sold" : "refused";
}

test("two devices selling the last units of one color never oversell it", async ({ page, browser }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/sales/pos");

  const found = coloredTile(await posTiles(page), 1);
  test.skip(found === null, "the seeded store has no priced product stocked in two colors");
  if (found === null) throw new Error("unreachable");
  const { tile, colorIds } = found;
  const [colorId = ""] = colorIds;
  const remaining = tile.stock.find((s) => s.color_id === colorId)?.qty ?? "0";
  const colorLabel = (await colorLabels(page))(colorId);

  const stockBefore = await onHandByColor(page, tile.sku_id);
  const second = await browser.newContext({ storageState: OWNER_STATE_FILE, baseURL: new URL(page.url()).origin });
  const otherDevice = await second.newPage();
  try {
    const outcomes = await Promise.all([
      sellEverything(page, tile, colorLabel, remaining),
      sellEverything(otherDevice, tile, colorLabel, remaining),
    ]);
    expect(outcomes.filter((o) => o === "sold"), "exactly one device sells the remaining units").toHaveLength(1);
  } finally {
    await second.close();
  }

  const stockAfter = await onHandByColor(page, tile.sku_id);
  expect(dec(stockAfter[colorId] ?? "0").gte(0), "the color never goes below zero").toBe(true);
  expect(dec(stockBefore[colorId] ?? "0").minus(stockAfter[colorId] ?? "0").eq(remaining), "the stock falls by exactly one sale").toBe(true);

  await finishJourney(page, testInfo, errors);
});
