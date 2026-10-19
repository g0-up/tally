import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import {
  activeCustomers,
  colorLabels,
  colorRadio,
  coloredTile,
  escapeRe,
  okToast,
  pickOption,
  posTiles,
  skuOption,
  tileName,
  uncoloredTile,
} from "../support/sales-data";
import { expectOnHandByColor, finishJourney, onHandByColor, partyBalance, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-02: an invoice with a colored and an uncolored line, a part paid in advance, delivered in two
// parts and settled at the debt ledger. The deposit is not revenue: the value recognised across the
// deliveries is the invoice total, and the advance is used up by the deliveries.

/** The confirmed invoice, as the confirm answer carries it. */
interface ConfirmedInvoice {
  id: string;
  doc_no: string;
}

test.beforeEach(() => requireOwner());

test("takes a deposit, delivers in two parts and recognises the invoice value once", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/sales/invoices");

  const tiles = await posTiles(page);
  const colored = coloredTile(tiles, 2);
  const plain = uncoloredTile(tiles, 2);
  test.skip(colored === null || plain === null, "the seeded store lacks a colored and an uncolored priced product with 2 or more in stock");
  if (colored === null || plain === null) throw new Error("unreachable");

  const customer = (await activeCustomers(page)).find((c) => c.credit_limit === null);
  test.skip(customer === undefined, "the seeded store has no active customer without a credit limit");
  if (customer === undefined) throw new Error("unreachable");

  const [colorIdA = ""] = colored.colorIds;
  const labelOf = await colorLabels(page);
  const stockBefore = await onHandByColor(page, colored.tile.sku_id);
  const balanceBefore = await partyBalance(page, customer.id);

  const total = dec(colored.tile.retail_price ?? "0")
    .times(2)
    .plus(dec(plain.retail_price ?? "0").times(2));
  const deposit = total.dividedToIntegerBy(2);

  await page.getByRole("button", { name: "Tạo hóa đơn" }).click();
  const form = page.getByRole("dialog", { name: "Tạo hóa đơn" });
  await pickOption(form, form.getByRole("combobox", { name: "Khách hàng" }), customer.name, new RegExp(`^${escapeRe(customer.name)}`));
  await form.getByLabel(/^Số ngày nợ/).fill("30");

  await pickOption(form, form.getByRole("combobox", { name: "Tên sản phẩm" }).last(), colored.tile.sku_code, skuOption(colored.tile.sku_code));
  await colorRadio(form.getByRole("radiogroup", { name: "Màu khách lấy" }).last(), labelOf(colorIdA)).click();
  await form.getByRole("textbox", { name: `Số lượng – ${tileName(colored.tile)}`, exact: true }).fill("2");

  await pickOption(form, form.getByRole("combobox", { name: "Tên sản phẩm" }).last(), plain.sku_code, skuOption(plain.sku_code));
  await form.getByRole("textbox", { name: `Số lượng – ${tileName(plain)}`, exact: true }).fill("2");

  await form.getByLabel("Khách đã thanh toán").fill(deposit.toFixed());
  const confirm = page.waitForResponse((r) => r.request().method() === "POST" && /\/invoices\/[^/]+:confirm$/.test(new URL(r.url()).pathname));
  await form.getByRole("button", { name: "Lưu & xác nhận bán" }).click();
  await expect(okToast(page).filter({ hasText: /^Đã xác nhận bán / })).toBeVisible();
  const confirmed = await confirm;
  expect(confirmed.status(), `confirming the invoice answered ${confirmed.status()}: ${await confirmed.text()}`).toBeLessThan(300);
  const invoice = (await confirmed.json()) as ConfirmedInvoice;

  const drawer = page.getByRole("dialog", { name: new RegExp(`^Hóa đơn ${escapeRe(invoice.doc_no)}`) });
  for (const part of ["1", "1"]) {
    await drawer.getByRole("button", { name: "Xuất kho giao khách" }).click();
    const delivery = page.getByRole("dialog", { name: /^Xuất kho/ });
    await delivery.getByRole("textbox", { name: /^Số lượng/ }).first().fill(part);
    await delivery.getByRole("button", { name: /^Ghi sổ/ }).click();
    await expect(okToast(page).filter({ hasText: /^Đã ghi / })).toBeVisible();
  }

  await expectOnHandByColor(page, colored.tile.sku_id, {
    [colorIdA]: dec(stockBefore[colorIdA] ?? "0").minus(2).toFixed(),
  });

  const after = await partyBalance(page, customer.id);
  const advanceUsed = dec(balanceBefore.advances).plus(deposit).minus(after.advances);
  const recognised = dec(after.receivable).minus(balanceBefore.receivable).plus(advanceUsed);
  expect(recognised.eq(total), "delivered value equals the invoice total; the deposit is not counted twice").toBe(true);

  await finishJourney(page, testInfo, errors);
});
