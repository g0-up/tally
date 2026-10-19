import { expect, test, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";

// The report screens on the seeded store's real session: every report opens on the season so far
// with the server's figures, and a CSV export of the stock report is written and offered.

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

const SCREENS = [
  { path: "/reports/stock-movement", title: "Nhập–xuất–tồn", table: "Nhập–xuất–tồn" },
  { path: "/reports/sales", title: "Bán hàng", table: "Doanh thu bán hàng" },
  { path: "/reports/gross-profit", title: "Lãi gộp & kết quả quản trị", table: "Lãi gộp theo hàng" },
  { path: "/reports/debt-aging", title: "Tuổi nợ", table: "Phải thu khách" },
  { path: "/reports/cash-flow", title: "Dòng tiền", table: "Thu chi theo loại" },
  { path: "/reports/gift-baskets", title: "Giỏ quà", table: "Giỏ quà theo mã" },
] as const;

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  for (const s of SCREENS) {
    test(`opens ${s.path} with the server's figures`, async ({ page }) => {
      await openScreen(page, s.path);
      await expect(page.locator("#view .ph h1")).toHaveText(s.title);
      await expect(page.getByText("Số liệu tạm tính, không phải báo cáo tài chính đã kiểm toán")).toBeVisible();
      // A table, or the empty state of a store with no movement in the period; never an error.
      await expect(page.getByRole("table", { name: s.table }).or(page.getByText("Chưa có dữ liệu.").first())).toBeVisible();
      await expect(page.getByRole("alert")).toHaveCount(0);
    });
  }

  test("exports the stock report to CSV and offers the file", async ({ page }) => {
    await openScreen(page, "/reports/stock-movement");
    const created = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname.endsWith("/export-jobs"));
    await page.getByRole("button", { name: "Xuất CSV" }).click();
    const { id } = (await (await created).json()) as { id: string };
    const status = page.getByRole("status", { name: "Tệp xuất CSV" });
    await expect(status).toBeVisible();
    // A button, not a link: the file is opened only after a fresh read of the export.
    await expect(status.getByRole("button", { name: "Tải tệp CSV" })).toBeVisible({ timeout: 60_000 });
    await expect(status).toContainText("Đã xuất xong");
    await expect(status.getByRole("link")).toHaveCount(0);
    const job = await apiGet<{ download_path: string | null }>(page, `/export-jobs/${id}`);
    expect(job.download_path).toMatch(/\/export-jobs\/[^/]+\/download$/);
    const res = await page.request.get(job.download_path ?? "", { maxRedirects: 0 });
    expect(res.status()).toBe(302);
  });
});
