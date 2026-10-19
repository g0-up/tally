import { expect, test } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE } from "./support/real-api";
import { activeCustomers, docNoIn, escapeRe, figure, okToast, pickOption, posTiles, skuOption, uncoloredTile } from "./support/sales-data";

// A quote from creation to the invoice it becomes, against the running API with the seeded store's
// owner. The invoice is confirmed and its remainder cancelled, so the journey reserves nothing.

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

test("sends a quote, records the customer's acceptance and turns it into a confirmed invoice", async ({ page }) => {
  await page.goto("/sales/quotes");
  const tile = uncoloredTile(await posTiles(page), 2);
  test.skip(tile === null, "the seeded store has no priced product without colors with 2 or more in stock");
  const customer = (await activeCustomers(page)).find((c) => c.credit_limit === null);
  test.skip(customer === undefined, "the seeded store has no active customer without a credit limit");
  if (tile === null || customer === undefined) return;

  await page.getByRole("button", { name: "Tạo báo giá" }).click();
  const form = page.getByRole("dialog", { name: "Tạo báo giá" });
  await pickOption(form, form.getByRole("combobox", { name: "Khách hàng" }), customer.name, new RegExp(`^${escapeRe(customer.name)}`));
  await pickOption(form, form.getByRole("combobox", { name: "Tên sản phẩm – dòng 1", exact: true }), tile.sku_code, skuOption(tile.sku_code));
  const lineQty = form.getByRole("textbox", { name: `Số lượng – ${tile.name}`, exact: true });
  await lineQty.fill("2");
  await lineQty.press("Tab");
  await form.getByRole("textbox", { name: "Quy cách" }).fill("Giao trong tuần");
  await expect(figure(form, "Giá trị báo giá")).not.toHaveText(/^0\s₫$/);
  await expect(figure(form, "Giữ tồn")).toHaveText("Không");
  await form.getByRole("button", { name: "Lưu & gửi khách" }).click();

  const saved = okToast(page).filter({ hasText: /^Đã lưu / });
  await expect(saved).toBeVisible();
  const quoteNo = await docNoIn(saved);
  const quote = page.getByRole("dialog", { name: `Báo giá ${quoteNo}` });
  await expect(quote).toBeVisible();

  await quote.getByRole("button", { name: "Khách chấp nhận" }).click();
  await expect(okToast(page).filter({ hasText: "Đã ghi nhận khách chấp nhận." })).toBeVisible();

  await quote.getByRole("button", { name: "Chuyển thành hóa đơn" }).click();
  const converted = okToast(page).filter({ hasText: /^Đã tạo hóa đơn / });
  await expect(converted).toHaveText(/^Đã tạo hóa đơn \S+ \(nháp\) từ báo giá\. Xác nhận bán để giữ hàng\.$/);
  const invoiceNo = await docNoIn(converted);
  const invoice = page.getByRole("dialog", { name: `Hóa đơn ${invoiceNo}` });
  await expect(invoice).toBeVisible();

  await invoice.getByRole("button", { name: "Xác nhận bán" }).click();
  await expect(okToast(page).filter({ hasText: /^Đã xác nhận và giữ hàng\./ })).toBeVisible();
  await expect(invoice.getByRole("button", { name: "Xuất kho giao khách" })).toBeVisible();

  // Release the reservation the confirmation made.
  await invoice.getByRole("button", { name: "Hủy phần chưa giao" }).click();
  const cancel = page.getByRole("dialog", { name: `Hủy phần chưa giao của ${invoiceNo}` });
  await cancel.getByLabel("Lý do").fill("Khách đổi lịch nhận hàng");
  await cancel.getByRole("button", { name: "Hủy phần còn lại" }).click();
  await expect(okToast(page).filter({ hasText: "Đã hủy phần chưa giao." })).toBeVisible();
  await expect(invoice.getByRole("button", { name: "Xuất kho giao khách" })).toHaveCount(0);
});
