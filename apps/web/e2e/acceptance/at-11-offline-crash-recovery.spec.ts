import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { posTiles, uncoloredTile } from "../support/sales-data";
import { apiPostRaw, finishJourney, onHandByColor, requireEnv, requireOwner, requireTestStack, watchPageErrors } from "./fixtures/acceptance";

// AT-11: the API fails a sale after its stock is taken but before it commits, the browser reloads as if
// it crashed, and the retry under the same Idempotency-Key posts the sale exactly once. The fault is
// injected by the API process, so the operator starts it with the fault armed and confirms it here.

interface PaymentMethod {
  id: string;
  code: string;
}

test.beforeEach(() => {
  requireOwner();
  requireTestStack();
});

test("posts a sale exactly once when the first attempt fails mid-transaction and the retry carries the same key", async ({ page }, testInfo) => {
  requireEnv("E2E_FAULT_INJECT_ARMED", "start the API with APP_ENV=test and TALLY_FAULT_INJECT=posting.before_commit@pos.checkout:1, then set this to 1");
  const errors = watchPageErrors(page);

  await page.goto("/sales/pos");
  const tile = uncoloredTile(await posTiles(page), 1);
  test.skip(tile === null, "the seeded store has no uncolored product in stock");
  if (tile === null) throw new Error("unreachable");

  const methods = (await apiGet<{ data: PaymentMethod[] }>(page, "/payment-methods")).data;
  const cash = methods.find((m) => m.code === "cash") ?? methods[0];
  if (cash === undefined) throw new Error("the store has no payment method");

  const body = {
    lines: [{ sku_id: tile.sku_id, qty: "1" }],
    payments: [{ payment_method_id: cash.id, amount: String(tile.retail_price) }],
  };
  const key = randomUUID();
  const stockBefore = await onHandByColor(page, tile.sku_id);

  const failed = await apiPostRaw(page, "/pos/checkout", body, { "Idempotency-Key": key });
  expect(failed.status, `the injected fault answered ${failed.status}: ${failed.text}`).toBeGreaterThanOrEqual(500);
  expect(await onHandByColor(page, tile.sku_id), "a failed attempt leaves no stock taken").toEqual(stockBefore);

  // The browser crashes and comes back; the cashier's retry carries the same key.
  await page.reload();
  const retried = await apiPostRaw(page, "/pos/checkout", body, { "Idempotency-Key": key });
  expect(retried.status, `the retry answered ${retried.status}: ${retried.text}`).toBe(201);

  const stockAfter = await onHandByColor(page, tile.sku_id);
  expect(dec(stockBefore.uncolored ?? "0").minus(stockAfter.uncolored ?? "0").eq("1"), "the sale took its stock exactly once").toBe(true);

  await finishJourney(page, testInfo, errors);
});
