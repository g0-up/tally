import { expect, test } from "@playwright/test";
import { apiGet } from "../support/real-api";
import { finishJourney, openStoreAs, requireEnv, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-09: two stores, two owners. The second owner signs in on the second store and sees nothing of the
// first: not in the product list, not on the stock page, and not through the API by a direct product id.
// The check is browser-side only: the second store is a browser context with its own cookies.

interface SkuListBody {
  data: { id: string; code: string }[];
}

test.beforeEach(() => requireOwner());

test("keeps the second store's owner out of the first store's products and stock", async ({ page, browser }, testInfo) => {
  const errors = watchPageErrors(page);
  const tenantB = requireEnv("E2E_TENANT_B_URL", "the web address of the second store");
  const emailB = requireEnv("E2E_OWNER_B_EMAIL", "the owner email of the second store");
  const passwordB = requireEnv("E2E_OWNER_B_PASSWORD", "the owner password of the second store");

  await page.goto("/inventory/stock");
  const skuA = (await apiGet<SkuListBody>(page, "/skus?limit=200")).data[0];
  test.skip(skuA === undefined, "the first store has no product to look for");
  if (skuA === undefined) throw new Error("unreachable");

  const pageB = await openStoreAs(browser, tenantB, emailB, passwordB);
  const errorsB = watchPageErrors(pageB);

  const listB = await apiGet<SkuListBody>(pageB, "/skus?limit=200");
  expect(listB.data.some((s) => s.id === skuA.id), "the second store's product list has no product of the first store").toBe(false);

  const direct = await pageB.request.get(`${new URL(pageB.url()).origin}/api/v1/stock/${skuA.id}`);
  expect(direct.ok(), `a direct request for a first-store product answered ${direct.status()}`).toBe(false);

  await pageB.goto("/inventory/stock");
  await expect(pageB.getByText(skuA.code, { exact: true })).toHaveCount(0);
  expect(errorsB, "uncaught errors in the second store").toEqual([]);
  await pageB.context().close();

  await finishJourney(page, testInfo, errors);
});
