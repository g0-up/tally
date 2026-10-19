import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { cashFundBalance, finishJourney, openStoreAs, requireEnv, watchPageErrors } from "./fixtures/acceptance";

// AT-12: a store that was never used is moved onto Tally, and its opening balances agree with the
// figures its owner entered. The cutover itself is run by hand on a fresh store (OPEN-14 default: the
// manual onboarding wizard); this journey checks the outcome, not the wizard's screens, which are not
// covered yet. It needs a fresh store with its own owner.

interface StockListBody {
  data: unknown[];
}

test("the opening cash of a fresh store matches the figure its owner entered on cutover", async ({ browser }, testInfo) => {
  const baseURL = requireEnv("E2E_FRESH_TENANT_URL", "the web address of a fresh store that has never been used");
  const email = requireEnv("E2E_FRESH_OWNER_EMAIL", "the owner email of the fresh store");
  const password = requireEnv("E2E_FRESH_OWNER_PASSWORD", "the owner password of the fresh store");
  const openingCash = requireEnv("E2E_CUTOVER_OPENING_CASH", "the opening cash the owner entered on cutover");
  requireEnv("E2E_CUTOVER_DONE", "the cutover was run by hand on the fresh store");

  const page = await openStoreAs(browser, baseURL, email, password);
  const errors = watchPageErrors(page);

  expect(dec(await cashFundBalance(page)).eq(openingCash), "the cash fund holds the opening cash").toBe(true);
  const stock = await apiGet<StockListBody>(page, "/stock?limit=1");
  expect(stock.data.length, "the fresh store has stock rows after cutover").toBeGreaterThan(0);

  await finishJourney(page, testInfo, errors);
  await page.context().close();
});
