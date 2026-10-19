import { expect, test, type Locator, type Page } from "@playwright/test";
import { HOME_PATH } from "../../src/app/menu";

// Keyboard-only journeys on the seeded store (NFR-11). Every step moves with Tab, Shift+Tab, Enter
// and Escape; the pointer is never used. Journeys that start a commit (count, sale, receipt) stop
// at the confirmation step so the sandbox data the other specs compare against stays unchanged.

const MAX_TABS = 120;

/** Presses Tab until the target has focus; fails if the focus order never reaches it. */
async function tabTo(page: Page, target: Locator): Promise<void> {
  for (let i = 0; i < MAX_TABS; i++) {
    if (await target.evaluate("(el) => el === document.activeElement")) return;
    await page.keyboard.press("Tab");
  }
  throw new Error(`focus never reached ${target}`);
}

const mainNav = (page: Page) => page.getByRole("navigation", { name: "Menu chính" });

test("opens a sidebar group and reaches one of its screens with the keyboard", async ({ page }) => {
  await page.goto(HOME_PATH);
  await expect(page.locator("#view h1").first()).toBeVisible();

  const chevron = mainNav(page).getByRole("button", { name: "Mở rộng Kho", exact: true });
  await tabTo(page, chevron);
  await page.keyboard.press("Enter");
  await expect(chevron).toHaveAccessibleName("Thu gọn Kho");

  const stock = mainNav(page).getByRole("link", { name: "Tồn kho", exact: true });
  await tabTo(page, stock);
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL("/inventory/stock");
  await expect(stock).toHaveAttribute("aria-current", "page");
});

test("opens the stock adjustment dialog from the keyboard and closes it back to the same button", async ({ page }) => {
  await page.goto("/inventory/stock");
  await expect(page.locator("#view h1").first()).toBeVisible();

  const opener = page.getByRole("button", { name: "Điều chỉnh kho", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");

  const dialog = page.getByRole("dialog").first();
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(opener).toBeFocused();
});

test("reaches the confirmation button of the stock count dialog without a mouse", async ({ page }) => {
  await page.goto("/inventory/counts");
  await expect(page.locator("#view h1").first()).toBeVisible();

  const opener = page.getByRole("button", { name: "Bắt đầu phiên kiểm kê", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");

  const dialog = page.getByRole("dialog").first();
  await expect(dialog).toBeVisible();
  await tabTo(page, dialog.getByRole("button", { name: "Bắt đầu", exact: true }));
  await expect(dialog.getByRole("button", { name: "Bắt đầu", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});
