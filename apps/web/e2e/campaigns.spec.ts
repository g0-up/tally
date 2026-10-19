import { expect, test, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE } from "./support/real-api";

// The campaign screens on the seeded store's real session: the seasons with their checklist and
// demand, and the customer list with its season revenue and re-order action.

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("shows the seasons and the season demand", async ({ page }) => {
    await openScreen(page, "/campaigns/seasons");
    await expect(page.getByText("Chiến dịch là nhãn phân tích, không phải sổ riêng. Mỗi dòng doanh thu/chi phí có một chiến dịch chính. Kết mùa không xóa tồn, tiền hay nợ.")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Nhu cầu hàng mùa Tết theo ngày" })).toBeVisible();
  });

  test("lists customers with their season revenue and order actions", async ({ page }) => {
    await openScreen(page, "/campaigns/customers");
    await expect(page.getByPlaceholder("Tìm tên, điện thoại, mã số thuế…")).toBeVisible();
    const table = page.getByRole("table", { name: "Khách hàng" });
    await expect(table.getByRole("columnheader", { name: "Doanh thu từ 01/10" })).toBeVisible();
    await expect(table.getByRole("columnheader", { name: "Đồng ý nhận tin" })).toBeVisible();

    await page.getByRole("button", { name: "Thêm khách hàng" }).click();
    const dialog = page.getByRole("dialog", { name: "Thêm khách hàng" });
    await expect(dialog.getByRole("textbox", { name: /^Tên khách hàng \*/ })).toBeVisible();
    await dialog.getByRole("button", { name: "Hủy" }).click();
    await expect(dialog).toBeHidden();
  });
});
