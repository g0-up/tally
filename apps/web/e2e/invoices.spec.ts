import { expect, test, type Locator, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE } from "./support/real-api";
import {
  activeCustomers,
  docNoIn,
  escapeRe,
  figure,
  okToast,
  pickOption,
  posTiles,
  receivableOf,
  skuOption,
  uncoloredTile,
  vndValue,
  type PosTile,
  type SeedCustomer,
} from "./support/sales-data";

// Wholesale invoices against the running API with the seeded store's owner: an invoice is saved
// and confirmed (stock reserved), paid in advance, delivered in parts and its remainder cancelled,
// which also releases what the journey reserved.

test.use(OWNER.password === "" ? {} : { storageState: OWNER_STATE_FILE });

const pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
});

const OVERRIDE = "Tôi xác nhận vẫn bán nếu vượt hạn mức nợ hoặc dưới giá tối thiểu (ghi vào nhật ký)";

interface Scenario {
  tile: PosTile;
  customer: SeedCustomer;
}

/** A product without colors with `min` in stock and an active customer the predicate accepts. */
async function invoiceScenario(page: Page, min: number, wanted: (c: SeedCustomer) => boolean, why: string): Promise<Scenario> {
  await page.goto("/sales/invoices");
  const tile = uncoloredTile(await posTiles(page), min);
  test.skip(tile === null, `the seeded store has no priced product without colors with ${min} or more in stock`);
  const customer = (await activeCustomers(page)).find(wanted);
  test.skip(customer === undefined, why);
  if (tile === null || customer === undefined) throw new Error("unreachable");
  return { tile, customer };
}

/** Fills "Tạo hóa đơn" with the customer, 30 days of credit and one line of the product. */
async function fillInvoice(page: Page, { tile, customer }: Scenario, qty: number): Promise<Locator> {
  await page.getByRole("button", { name: "Tạo hóa đơn" }).click();
  const form = page.getByRole("dialog", { name: "Tạo hóa đơn" });
  await pickOption(form, form.getByRole("combobox", { name: "Khách hàng" }), customer.name, new RegExp(`^${escapeRe(customer.name)}`));
  await form.getByLabel(/^Số ngày nợ/).fill("30");
  await pickOption(form, form.getByRole("combobox", { name: "Tên sản phẩm – dòng 1", exact: true }), tile.sku_code, skuOption(tile.sku_code));
  const lineQty = form.getByRole("textbox", { name: `Số lượng – ${tile.name}`, exact: true });
  await lineQty.fill(String(qty));
  await lineQty.press("Tab");
  return form;
}

/** Releases what the invoice still reserves, as the owner does from the drawer. */
async function cancelRemainder(page: Page, docNo: string): Promise<void> {
  const drawer = page.getByRole("dialog", { name: `Hóa đơn ${docNo}` });
  await drawer.getByRole("button", { name: "Hủy phần chưa giao" }).click();
  const cancel = page.getByRole("dialog", { name: `Hủy phần chưa giao của ${docNo}` });
  await cancel.getByLabel("Lý do").fill("Khách không lấy phần còn lại");
  await cancel.getByRole("button", { name: "Hủy phần còn lại" }).click();
  await expect(okToast(page).filter({ hasText: "Đã hủy phần chưa giao." })).toBeVisible();
  await expect(cancel).toHaveCount(0);
  await expect(drawer.getByRole("button", { name: "Xuất kho giao khách" })).toHaveCount(0);
}

test("confirms an invoice, takes a deposit, delivers it in two parts and cancels the rest", async ({ page }) => {
  const scenario = await invoiceScenario(page, 3, (c) => c.credit_limit === null, "the seeded store has no active customer without a credit limit");
  const form = await fillInvoice(page, scenario, 3);
  await expect(figure(form, "Tổng tiền")).not.toHaveText(/^0\s₫$/);
  await expect(form.getByRole("checkbox", { name: OVERRIDE })).toHaveCount(0);
  await form.getByRole("button", { name: "Lưu & xác nhận bán" }).click();

  const confirmed = okToast(page).filter({ hasText: /^Đã xác nhận / });
  await expect(confirmed).toHaveText(/^Đã xác nhận \S+ và giữ hàng\.$/);
  const docNo = await docNoIn(confirmed);
  const drawer = page.getByRole("dialog", { name: `Hóa đơn ${docNo}` });
  await expect(drawer).toBeVisible();
  const total = vndValue(await figure(drawer, "Tổng tiền").textContent());
  expect(total).toBeGreaterThan(0);

  // Paid in advance: the money waits for the deliveries.
  const deposit = Math.max(1000, Math.floor(total / 6 / 1000) * 1000);
  await drawer.getByRole("button", { name: "Ghi nhận thanh toán" }).click();
  const pay = page.getByRole("dialog", { name: `Ghi nhận thanh toán ${docNo}` });
  await pay.getByLabel("Số tiền khách trả").fill(String(deposit));
  await pay.getByLabel("Số tiền khách trả").press("Tab");
  await pay.getByRole("button", { name: "Ghi phiếu thu" }).click();
  await expect(okToast(page).filter({ hasText: /^Đã ghi \S+\.$/ })).toBeVisible();
  await expect(pay).toHaveCount(0);
  await expect(figure(drawer, "Đã thanh toán")).not.toHaveText(/^0\s₫$/);

  const deliveries = drawer.getByRole("table", { name: `Phiếu xuất kho của ${docNo}` }).getByRole("row").filter({ hasText: "Đã xuất kho" });
  for (const n of [1, 2]) {
    await drawer.getByRole("button", { name: "Xuất kho giao khách" }).click();
    const out = page.getByRole("dialog", { name: `Xuất kho giao khách · ${docNo}` });
    const qty = out.getByRole("textbox", { name: /^Xuất lần này – / });
    await expect(qty).toBeEnabled();
    await qty.fill("1");
    await qty.press("Tab");
    // The first delivery is worth more than the deposit, so the deposit is taken off in full.
    if (n === 1) await expect.poll(async () => vndValue(await figure(out, "Trừ tiền trả trước").textContent())).toBe(deposit);
    await out.getByRole("button", { name: "Ghi sổ xuất kho" }).click();
    await expect(okToast(page).filter({ hasText: /^Đã xuất kho / }).last()).toBeVisible();
    await expect(out).toHaveCount(0);
    await expect(deliveries).toHaveCount(n);
  }

  await cancelRemainder(page, docNo);
  await expect(deliveries).toHaveCount(2);
});

test("confirms an invoice over the customer's credit limit only with the owner's override", async ({ page }) => {
  const scenario = await invoiceScenario(page, 1, (c) => c.credit_limit !== null, "the seeded store has no active customer with a credit limit");
  const limit = Number(scenario.customer.credit_limit);
  const receivable = await receivableOf(page, scenario.customer.id);
  const form = await fillInvoice(page, scenario, 1);

  // One unit priced so that the open amount takes the customer over the limit.
  const price = Math.max(0, limit - receivable) + 100_000;
  await form.getByRole("textbox", { name: "Giá", exact: true }).fill(String(price));
  await form.getByRole("textbox", { name: "Giá", exact: true }).press("Tab");
  await expect(form.getByText(/^Vượt hạn mức nợ:/)).toBeVisible();

  const override = form.getByRole("checkbox", { name: OVERRIDE });
  await expect(override).not.toBeChecked();
  await form.getByRole("button", { name: "Lưu & xác nhận bán" }).click();
  await expect(page.locator(".toasts .toast.err")).toContainText("Tích “Tôi xác nhận vẫn bán” để tiếp tục.");
  await expect(form).toBeVisible();

  await override.check();
  await form.getByRole("button", { name: "Lưu & xác nhận bán" }).click();
  const confirmed = okToast(page).filter({ hasText: /^Đã xác nhận / });
  await expect(confirmed).toHaveText(/^Đã xác nhận \S+ và giữ hàng\. Ngoại lệ đã ghi nhật ký\.$/);
  const docNo = await docNoIn(confirmed);
  await expect(page.getByRole("dialog", { name: `Hóa đơn ${docNo}` })).toBeVisible();

  await cancelRemainder(page, docNo);
});
