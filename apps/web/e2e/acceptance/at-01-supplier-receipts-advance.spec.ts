import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet, apiPost } from "../support/real-api";
import { coloredTile, posTiles } from "../support/sales-data";
import { finishJourney, onHandByColor, partyBalance, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-01: receipts of a colored product in two lots, with a supplier advance. The receipts are not
// linked to a purchase order here, so the advance allocation is checked by its invariant only: the
// payable that grew plus the advance that was used equals the value received.

interface Party {
  id: string;
  name: string;
  status: string;
}

interface PaymentMethod {
  id: string;
  code: string;
}

interface Doc {
  doc_no: string;
}

const ADVANCE = "100000";
const UNIT_PRICE = "100000";

test.beforeEach(() => requireOwner());

test("receives a colored product in two lots, pays an advance and keeps payables and stock by color", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/procurement/receipts");

  const found = coloredTile(await posTiles(page), 0);
  test.skip(found === null, "the seeded store has no priced product stocked in two colors");
  if (found === null) throw new Error("unreachable");
  const { tile, colorIds } = found;

  const supplier = (await apiGet<{ data: Party[] }>(page, "/suppliers?limit=200")).data.find((s) => s.status === "active");
  test.skip(supplier === undefined, "the seeded store has no active supplier");
  if (supplier === undefined) throw new Error("unreachable");

  const methods = (await apiGet<{ data: PaymentMethod[] }>(page, "/payment-methods")).data;
  const cash = methods.find((m) => m.code === "cash") ?? methods[0];
  if (cash === undefined) throw new Error("the store has no payment method");

  const before = await partyBalance(page, supplier.id);
  const stockBefore = await onHandByColor(page, tile.sku_id);

  await apiPost(page, "/payments", {
    direction: "out",
    party_id: supplier.id,
    purpose: "advance",
    payment_method_id: cash.id,
    amount: ADVANCE,
  });

  const stamp = Date.now().toString(36);
  const lots = [
    { colorId: colorIds[0], qty: "2", lotNo: `AT01-${stamp}-1` },
    { colorId: colorIds[1], qty: "3", lotNo: `AT01-${stamp}-2` },
  ];
  const docNos: string[] = [];
  for (const lot of lots) {
    const doc = await apiPost<Doc>(page, "/receipts", {
      supplier_party_id: supplier.id,
      paid_now_amount: "0",
      lines: [{ sku_id: tile.sku_id, color_id: lot.colorId, qty: lot.qty, unit_price: UNIT_PRICE, lot_no: lot.lotNo }],
    });
    docNos.push(doc.doc_no);
  }

  await page.reload();
  for (const docNo of docNos) {
    await expect(page.getByRole("row").filter({ hasText: docNo }).first()).toBeVisible();
  }

  const stockAfter = await onHandByColor(page, tile.sku_id);
  const gained = (color: string) => dec(stockAfter[color] ?? "0").minus(stockBefore[color] ?? "0");
  expect(gained(colorIds[0]).eq("2"), "color one gained the first lot").toBe(true);
  expect(gained(colorIds[1]).eq("3"), "color two gained the second lot").toBe(true);

  const after = await partyBalance(page, supplier.id);
  const payableGrowth = dec(after.payable).minus(before.payable);
  const advanceUsed = dec(before.advances).plus(ADVANCE).minus(after.advances);
  expect(payableGrowth.plus(advanceUsed).eq(dec(UNIT_PRICE).times(5)), "payable plus advance used equals the value received").toBe(true);

  await finishJourney(page, testInfo, errors);
});
