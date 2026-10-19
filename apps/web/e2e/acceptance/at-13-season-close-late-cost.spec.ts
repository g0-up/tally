import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { okToast } from "../support/sales-data";
import { cashFundBalance, finishJourney, requireOwner, requireTestStack, watchPageErrors } from "./fixtures/acceptance";

// AT-13: a season is closed. Closing it moves no money, and the documents already posted under it stay
// posted. The late-cost half of AT-13 (a cost that arrives after the close) is not covered yet. The
// journey closes a real season, so it runs only on the disposable APP_ENV=test stack.

test.beforeEach(() => {
  requireOwner();
  requireTestStack();
});

test("closes a season without moving cash", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/campaign/seasons");

  const close = page.getByRole("button", { name: /^Kết mùa / }).first();
  test.skip((await close.count()) === 0, "the store has no open season to close");

  const cashBefore = await cashFundBalance(page);
  await close.click();
  const dialog = page.getByRole("dialog", { name: /^Kết mùa/ });
  for (const box of await dialog.getByRole("checkbox").all()) {
    await box.check();
  }
  await dialog.getByRole("button", { name: /^Kết mùa/ }).last().click();

  await expect(okToast(page).first()).toBeVisible();
  expect(dec(await cashFundBalance(page)).eq(cashBefore), "closing a season moves no cash").toBe(true);

  await finishJourney(page, testInfo, errors);
});
