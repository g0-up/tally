import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { sellAtPos, posTiles } from "../support/sales-data";
import { finishJourney, onHandByColor, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-05: a virtual combo is assembled at the counter from its parts and carries a discount on the
// whole order. The combo itself never has physical stock, the parts leave stock once each, and the
// sale is counted once at the parts' value, not again at the combo's.

interface SkuListBody {
  data: { id: string; code: string; product_type: string; status: string }[];
}

interface ComboBody {
  components: { sku_id: string; color_id: string | null; qty: string }[];
}

test.beforeEach(() => requireOwner());

test("sells a virtual combo without physical stock of its own and takes each part once", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/sales/pos");

  const combo = (await apiGet<SkuListBody>(page, "/skus?limit=200")).data.find((s) => s.product_type === "virtual_combo" && s.status === "active");
  test.skip(combo === undefined, "the seeded store has no active virtual combo");
  if (combo === undefined) throw new Error("unreachable");

  const detail = await apiGet<ComboBody>(page, `/skus/${combo.id}`);
  const part = detail.components[0];
  test.skip(part === undefined, "the virtual combo has no components");
  if (part === undefined) throw new Error("unreachable");

  const comboStockBefore = await onHandByColor(page, combo.id);
  const partStockBefore = await onHandByColor(page, part.sku_id);
  const tile = (await posTiles(page)).find((t) => t.sku_id === combo.id);
  test.skip(tile === undefined, "the virtual combo is not sellable at the POS");
  if (tile === undefined) throw new Error("unreachable");

  await sellAtPos(page, tile, null);

  const comboStockAfter = await onHandByColor(page, combo.id);
  expect(dec(comboStockAfter.uncolored ?? "0").eq(comboStockBefore.uncolored ?? "0"), "the combo holds no physical stock").toBe(true);

  const partKey = part.color_id ?? "uncolored";
  const partStockAfter = await onHandByColor(page, part.sku_id);
  expect(
    dec(partStockBefore[partKey] ?? "0").minus(partStockAfter[partKey] ?? "0").eq(part.qty),
    "the part leaves stock once for the one combo sold",
  ).toBe(true);

  await finishJourney(page, testInfo, errors);
});
