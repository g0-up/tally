import { expect, test, type Page } from "@playwright/test";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE, apiGet } from "./support/real-api";

// The Tổng quan screens on the seeded store's real session: the dashboard moves between periods
// through the URL, and the notification chips count what the API reports as active.

interface Dashboard {
  /** The period before the one shown; its `date` is where "‹ Kỳ trước" goes. */
  previous: { date: string };
}
interface NotificationPage {
  data: { kind: string }[];
  next_cursor: string | null;
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("shows cash and debt of the month, then of a day and the day before", async ({ page }) => {
    await openScreen(page, "/overview/dashboard");
    await expect(page.locator("#view .ph h1")).toHaveText("Thu, chi và công nợ");
    await expect(page.getByRole("heading", { name: "Thu – chi", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Công nợ", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Tháng", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByText("Tổng thu", { exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Theo loại", exact: true })).toBeVisible();
    await expect(page.getByText("Khách còn nợ", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Ngày", exact: true }).click();
    await expect(page).toHaveURL(/[?&]period=day/);
    await expect(page.getByText("Giao dịch thu – chi trong ngày")).toBeVisible();

    const day = await apiGet<Dashboard>(page, "/reports/dashboard?period=day");
    await page.getByRole("button", { name: "‹ Kỳ trước" }).click();
    await expect(page).toHaveURL(new RegExp(`[?&]date=${day.previous.date}`));
    await expect(page.getByRole("button", { name: "Về hôm nay" })).toBeVisible();
  });

  test("counts the active notifications by kind, keeping the daily chips at 0, with no approval chip", async ({ page }) => {
    await openScreen(page, "/overview/notifications");
    const first = await apiGet<NotificationPage>(page, "/notifications?state=active&limit=200");
    const chips = page.getByRole("group", { name: "Loại thông báo" });
    await expect(chips.getByRole("button", { name: /^Chờ duyệt/ })).toHaveCount(0);
    // More than one page: the counts depend on pages read after this one.
    if (first.next_cursor !== null) return;
    await expect(chips.getByRole("button").first()).toHaveText(`Tất cả (${first.data.length})`);
    // The owner's daily kinds keep a chip even at 0.
    for (const label of ["Nợ quá hạn", "Sắp phải trả NCC", "Sắp hết hạn", "Hết hạn – chặn bán", "Dưới tồn tối thiểu", "Lỗi đồng bộ POS"]) {
      await expect(chips.getByRole("button", { name: new RegExp(`^${label} \\(\\d+\\)$`) })).toBeVisible();
    }
    if (first.data.length === 0) await expect(page.getByText("Không có việc cần xử lý.")).toBeVisible();
    else await expect(page.getByRole("table", { name: "Thông báo" }).getByRole("row")).toHaveCount(first.data.length + 1);
  });
});
