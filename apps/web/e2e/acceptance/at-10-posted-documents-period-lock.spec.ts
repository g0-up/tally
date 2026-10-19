import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { apiGet } from "../support/real-api";
import { posTiles, uncoloredTile } from "../support/sales-data";
import { apiPostRaw, evidenceDate, finishJourney, onHandByColor, requireOwner, requireTestStack, watchPageErrors } from "./fixtures/acceptance";

// AT-10: once a period is locked, a sale dated inside it is refused and the stock stays as it was.
// The lock runs through today (the lock never moves past today), so this journey needs a disposable
// store: it runs only on the APP_ENV=test stack.

interface PeriodLocksBody {
  locked_through: string | null;
}

interface PaymentMethod {
  id: string;
  code: string;
}

test.beforeEach(() => {
  requireOwner();
  requireTestStack();
});

test("refuses a sale inside a locked period and leaves the stock as it was", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  const today = evidenceDate();

  await page.goto("/settings/account");
  const before = await apiGet<PeriodLocksBody>(page, "/period-locks?limit=1");
  test.skip(before.locked_through === today, "the store is already locked through today");

  await page.getByLabel("Khóa sổ đến hết ngày", { exact: true }).fill(today);
  await page.getByRole("button", { name: "Khóa sổ", exact: true }).click();
  await page.getByRole("button", { name: "Xác nhận khóa sổ", exact: true }).click();
  await expect.poll(async () => (await apiGet<PeriodLocksBody>(page, "/period-locks?limit=1")).locked_through).toBe(today);

  await page.goto("/sales/pos");
  const tile = uncoloredTile(await posTiles(page), 1);
  test.skip(tile === null, "the seeded store has no uncolored product in stock");
  if (tile === null) throw new Error("unreachable");

  const methods = (await apiGet<{ data: PaymentMethod[] }>(page, "/payment-methods")).data;
  const cash = methods.find((m) => m.code === "cash") ?? methods[0];
  if (cash === undefined) throw new Error("the store has no payment method");

  const stockBefore = await onHandByColor(page, tile.sku_id);
  const refused = await apiPostRaw(page, "/pos/checkout", {
    lines: [{ sku_id: tile.sku_id, qty: "1" }],
    payments: [{ payment_method_id: cash.id, amount: String(tile.retail_price) }],
  }, { "Idempotency-Key": randomUUID() });

  expect(refused.status, `a sale inside the lock answered ${refused.status}: ${refused.text}`).toBe(423);
  expect(refused.text).toContain("period_locked");
  expect(await onHandByColor(page, tile.sku_id), "a refused sale moves no stock").toEqual(stockBefore);

  await finishJourney(page, testInfo, errors);
});
