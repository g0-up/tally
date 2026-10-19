import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { okToast, posTiles, sellAtPos } from "../support/sales-data";
import { finishJourney, onHandByColor, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-04: a prepacked gift basket. Its components leave stock once, when the basket is assembled.
// Selling the basket does not take them again, and breaking up the rest returns what is still usable.

interface KitListBody {
  data: { id: string; name: string; status: string }[];
}

interface KitRecipe {
  components: { sku_id: string; color_id: string | null; qty: string }[];
}

test.beforeEach(() => requireOwner());

test("assembles a basket once, sells it without a second consumption and breaks up the rest", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/kits");

  const kit = (await apiGet<KitListBody>(page, "/kits?limit=50")).data.find((k) => k.status === "active");
  test.skip(kit === undefined, "the seeded store has no active gift-basket recipe");
  if (kit === undefined) throw new Error("unreachable");

  const recipe = await apiGet<KitRecipe>(page, `/kits/${kit.id}`);
  const component = recipe.components.find((c) => c.color_id !== null) ?? recipe.components[0];
  test.skip(component === undefined, "the recipe has no component");
  if (component === undefined) throw new Error("unreachable");

  const stockBefore = await onHandByColor(page, component.sku_id);

  await page.getByRole("row").filter({ hasText: kit.name }).getByRole("button", { name: /Tạo lệnh đóng/ }).click();
  const assembly = page.getByRole("dialog", { name: "Tạo lệnh đóng giỏ" });
  await assembly.getByRole("button", { name: "Tạo lệnh" }).click();
  await expect(okToast(page).first()).toBeVisible();

  const afterAssembly = await onHandByColor(page, component.sku_id);
  const colorKey = component.color_id ?? "uncolored";
  expect(
    dec(stockBefore[colorKey] ?? "0").minus(afterAssembly[colorKey] ?? "0").eq(component.qty),
    "assembly consumes each component once",
  ).toBe(true);

  const tiles = await posTiles(page);
  const basket = tiles.find((t) => t.name === kit.name);
  test.skip(basket === undefined, "the basket is not sellable at the POS");
  if (basket === undefined) throw new Error("unreachable");

  await sellAtPos(page, basket, null);
  const afterSale = await onHandByColor(page, component.sku_id);
  expect(dec(afterSale[colorKey] ?? "0").eq(afterAssembly[colorKey] ?? "0"), "selling the basket takes no component again").toBe(true);

  await finishJourney(page, testInfo, errors);
});
