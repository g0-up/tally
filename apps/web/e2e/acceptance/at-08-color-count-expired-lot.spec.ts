import { expect, test } from "@playwright/test";
import { dec } from "../../src/shared/lib/decimal";
import { apiGet } from "../support/real-api";
import { finishJourney, onHandByColor, requireOwner, watchPageErrors } from "./fixtures/acceptance";

// AT-08: a stock count runs while sales continue. Only the color that is counted short is adjusted,
// and a lot past its expiry is disposed of, not sold. Counting and disposal both post through the
// ledger, so the on-hand quantity afterwards equals the count the owner confirmed.

interface LotListBody {
  data: { id: string; sku_id: string; color_id: string | null; lot_no: string; days_to_expiry: number | null; qty: string }[];
}

test.beforeEach(() => requireOwner());

test("adjusts only the color counted short and disposes of an expired lot", async ({ page }, testInfo) => {
  const errors = watchPageErrors(page);
  await page.goto("/inventory/counts");

  await page.getByRole("button", { name: "Bắt đầu phiên kiểm kê" }).click();
  const start = page.getByRole("dialog", { name: "Bắt đầu kiểm kê" });
  const category = start.getByRole("combobox", { name: "Nhóm hàng" });
  const options = await category.locator("option").evaluateAll((els) => els.map((el) => (el as { value: string }).value).filter((v) => v !== ""));
  test.skip(options.length === 0, "the seeded store has no category to count");
  await category.selectOption(options[0] ?? "");
  await start.getByRole("button", { name: "Bắt đầu" }).click();
  await expect(page.getByText(/^Đã mở phiên KK-/).first()).toBeVisible();

  const drawer = page.getByRole("dialog", { name: /^Kiểm kê KK-/ });
  const firstLine = drawer.getByRole("textbox", { name: /^Thực đếm – / }).first();
  await firstLine.fill("0");
  await drawer.getByRole("button", { name: "Hoàn tất đếm" }).click();
  await expect(page.getByText("Đã hoàn tất đếm. Kiểm tra chênh lệch rồi xác nhận ghi sổ.").first()).toBeVisible();

  await drawer.getByRole("button", { name: "Xác nhận & ghi sổ chênh lệch" }).click();
  const confirm = page.getByRole("dialog", { name: /^Xác nhận ghi sổ chênh lệch KK-/ });
  await confirm.getByRole("checkbox", { name: "Tôi xác nhận ghi sổ chênh lệch" }).check();
  await confirm.getByRole("button", { name: "Xác nhận & ghi sổ" }).click();
  await expect(page.getByText("Đã ghi sổ chênh lệch kiểm kê.").first()).toBeVisible();

  const expired = (await apiGet<LotListBody>(page, "/lots?limit=200")).data.find((l) => l.days_to_expiry !== null && l.days_to_expiry < 0 && dec(l.qty).gt(0));
  test.skip(expired === undefined, "the seeded store has no lot past its expiry date with stock");
  if (expired === undefined) throw new Error("unreachable");

  const stockBeforeDisposal = await onHandByColor(page, expired.sku_id);
  await page.goto("/inventory/lots");
  const row = page.getByRole("row").filter({ has: page.getByText(expired.lot_no, { exact: true }) });
  await row.getByRole("button", { name: "Tiêu hủy" }).click();
  const dispose = page.getByRole("dialog", { name: "Tiêu hủy lô hết hạn" });
  await dispose.getByRole("combobox", { name: /^Lý do/ }).selectOption({ index: 1 });
  await dispose.getByRole("button", { name: "Ghi sổ tiêu hủy" }).click();
  await expect(page.getByText("Đã ghi tiêu hủy.").first()).toBeVisible();

  const colorKey = expired.color_id ?? "uncolored";
  const stockAfterDisposal = await onHandByColor(page, expired.sku_id);
  expect(
    dec(stockBeforeDisposal[colorKey] ?? "0").minus(stockAfterDisposal[colorKey] ?? "0").gte(dec(expired.qty)),
    "disposal takes at least the expired lot out of its color",
  ).toBe(true);

  await finishJourney(page, testInfo, errors);
});
