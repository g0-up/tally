import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { colorLabels, coloredTile, posTiles, sellAtPos } from "../support/sales-data";
import { apiPostRaw, finishJourney, onHandByColor, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-16: at the counter a product with several colors cannot leave without a color. The API refuses a
// sale that names no color, and the POS sells only the color the cashier chose, so the stock of the
// other colors does not move. The combo half of AT-16 is covered by AT-05.

interface PaymentMethod {
  id: string;
  code: string;
}

test.beforeEach(() => requireOwner());

test("refuses an uncolored sale of a multi-color product and sells only the color chosen", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/sales/pos");

  const found = coloredTile(await posTiles(page), 2);
  test.skip(found === null, "the seeded store has no product stocked in two colors with 2 or more each");
  if (found === null) throw new Error("unreachable");
  const { tile, colorIds } = found;
  const [colorA = "", colorB = ""] = colorIds;
  const labelOf = await colorLabels(page);

  const methods = (await apiGet<{ data: PaymentMethod[] }>(page, "/payment-methods")).data;
  const cash = methods.find((m) => m.code === "cash") ?? methods[0];
  if (cash === undefined) throw new Error("the store has no payment method");

  const stockBefore = await onHandByColor(page, tile.sku_id);
  const refused = await apiPostRaw(page, "/pos/checkout", {
    lines: [{ sku_id: tile.sku_id, qty: "1" }],
    payments: [{ payment_method_id: cash.id, amount: String(tile.retail_price) }],
  }, { "Idempotency-Key": randomUUID() });
  expect(refused.status, `a sale with no color answered ${refused.status}: ${refused.text}`).toBe(422);
  expect(refused.text).toContain("color_required");
  expect(await onHandByColor(page, tile.sku_id), "a refused sale moves no stock").toEqual(stockBefore);

  await sellAtPos(page, tile, labelOf(colorB));
  const stockAfter = await onHandByColor(page, tile.sku_id);
  expect(dec(stockBefore[colorB] ?? "0").minus(stockAfter[colorB] ?? "0").eq("1"), "the chosen color gives up one unit").toBe(true);
  expect(stockAfter[colorA] ?? "0", "the other color does not move").toBe(stockBefore[colorA] ?? "0");

  await finishJourney(page, testInfo, errors);
});
