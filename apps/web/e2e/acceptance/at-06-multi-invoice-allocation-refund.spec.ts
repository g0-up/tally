import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { activeCustomers } from "../support/sales-data";
import { cashFundBalance, finishJourney, partyBalance, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-06: one receipt from a customer who owes several documents. The receipt is spread over them
// without going past what they owe, and the fund receives all of it. The refund half of AT-06 (a
// returned invoice refunded from a fund) is not covered by this journey yet.

interface DebtLedger {
  documents: { id: string; doc_no: string }[];
}

const RECEIPT = 100000;

test.beforeEach(() => requireOwner());

test("spreads one receipt over several documents without over-allocating and the fund takes all of it", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/debt/receivables");

  const customer = (await activeCustomers(page)).find((c) => c.credit_limit === null);
  test.skip(customer === undefined, "the seeded store has no active customer without a credit limit");
  if (customer === undefined) throw new Error("unreachable");

  const ledger = await apiGet<DebtLedger>(page, `/debts/parties/${customer.id}`);
  test.skip(ledger.documents.length < 2, "the customer does not owe two documents");

  const before = await partyBalance(page, customer.id);
  const fundBefore = await cashFundBalance(page);

  await page.getByRole("row").filter({ hasText: customer.name }).getByRole("button", { name: /^Thu tiền/ }).click();
  const pay = page.getByRole("dialog", { name: "Thu tiền khách" });
  await pay.getByRole("textbox", { name: "Số tiền" }).fill(String(RECEIPT));
  await pay.getByRole("button", { name: "Ghi phiếu thu" }).click();
  await expect(page.getByText(/^Đã ghi PT-/).first()).toBeVisible();

  const after = await partyBalance(page, customer.id);
  const settled = dec(before.receivable).minus(after.receivable).plus(dec(after.advances).minus(before.advances));
  expect(settled.eq(RECEIPT), "the receipt settles documents or becomes an advance, never more").toBe(true);
  expect(dec(after.receivable).gte(0), "no document is over-allocated").toBe(true);

  const fundAfter = await cashFundBalance(page);
  expect(dec(fundAfter).minus(fundBefore).eq(RECEIPT), "the cash fund receives the receipt in full").toBe(true);

  await finishJourney(page, testInfo, errors);
});
