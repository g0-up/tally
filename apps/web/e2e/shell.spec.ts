import { expect, test, type Page } from "@playwright/test";
import { HOME_PATH, MENU, subPath } from "../src/app/menu";
import { MOCK_OWNER, MOCK_STORE, mockSignedInStore, type MockApi } from "./support/mock-api";
import { OWNER, OWNER_PASSWORD_MISSING, OWNER_STATE_FILE } from "./support/real-api";

// Store shell journeys. Most run on a signed-in session served by the browser-level API mock,
// which also stands in for the platform endpoints the API does not serve yet (business date,
// unread count). "with the real API" repeats the navigation on the seeded store's real session
// (saved by auth.setup.ts); signing in itself is covered by auth.spec.ts.

const MOBILE = { width: 390, height: 844 };

const menuButton = (page: Page) => page.getByRole("button", { name: "Mở menu" });
const sidebar = (page: Page) => page.getByRole("complementary", { name: "Điều hướng" });
const mainNav = (page: Page) => page.getByRole("navigation", { name: "Menu chính" });
const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
/** A group's fold chevron, whichever way it points ("Mở rộng {nhóm}" or "Thu gọn {nhóm}"). */
const groupToggle = (page: Page, label: string) => mainNav(page).getByRole("button", { name: new RegExp(`^(Mở rộng|Thu gọn) ${escapeRegExp(label)}$`) });
/** The group's own screen links: some share the group's name, so they are looked up inside its list. */
const groupLinks = (page: Page, id: string) => mainNav(page).locator(`#nav-sub-${id}`);

/** Opens the off-canvas sidebar on narrow screens unless it is open; wide screens always show it. */
async function showSidebar(page: Page): Promise<void> {
  const button = menuButton(page);
  if ((await button.isVisible()) && (await button.getAttribute("aria-expanded")) !== "true") {
    await button.click();
    await expect(button).toHaveAttribute("aria-expanded", "true");
  }
  await expectSidebarOnScreen(page, true);
}

/**
 * Whether the sidebar sits fully on screen (open, or always on wide screens) or fully off it
 * (closed off-canvas menu). Measured from its box: it slides with a transform transition.
 */
async function expectSidebarOnScreen(page: Page, onScreen: boolean): Promise<void> {
  await expect
    .poll(async () => {
      const box = await sidebar(page).boundingBox();
      if (!box) return "missing";
      if (box.x >= 0) return "on";
      return box.x + box.width <= 0 ? "off" : "moving";
    })
    .toBe(onScreen ? "on" : "off");
}

/** Opens a store screen and waits until the shell and the screen header are rendered. */
async function openScreen(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator("#view .ph h1")).toBeVisible();
}

let api: MockApi | undefined;
const pageErrors: string[] = [];

test.beforeEach(async ({ page }) => {
  api = undefined;
  pageErrors.length = 0;
  page.on("pageerror", (err) => pageErrors.push(err.message));
});

test.afterEach(() => {
  expect(pageErrors, "uncaught errors in the page").toEqual([]);
  expect(api?.unhandled ?? [], "API calls the shell made that no endpoint serves").toEqual([]);
});

test("opens every screen from the sidebar with the current link marked", async ({ page }) => {
  api = await mockSignedInStore(page);
  await openScreen(page, "/");
  await expect(page).toHaveURL(HOME_PATH);
  await expect(page.locator(".util")).toContainText(MOCK_STORE.display_name);

  for (const group of MENU) {
    const toggle = groupToggle(page, group.label);
    const link = (label: string) => groupLinks(page, group.id).getByRole("link", { name: label, exact: true });
    for (const sub of group.subs) {
      await showSidebar(page);
      // Another group's links start folded; its chevron unfolds them without leaving the screen.
      if ((await toggle.getAttribute("aria-expanded")) !== "true") {
        await toggle.click();
        await expect(toggle).toHaveAttribute("aria-expanded", "true");
      }
      await link(sub.label).click();

      await expect(page).toHaveURL(subPath(group, sub));
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(sub.title);
      await expect(link(sub.label)).toHaveAttribute("aria-current", "page");
      await expect(mainNav(page).locator('[aria-current="page"]')).toHaveCount(1);
      await expect(toggle).toHaveAttribute("aria-expanded", "true");
    }
  }
});

test("folds and unfolds the current group without leaving the screen", async ({ page }) => {
  api = await mockSignedInStore(page);
  await openScreen(page, "/inventory/stock");
  await showSidebar(page);
  const toggle = groupToggle(page, "Kho");
  await expect(toggle).toHaveAccessibleName("Thu gọn Kho");
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await toggle.click();
  await expect(toggle).toHaveAccessibleName("Mở rộng Kho");
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(groupLinks(page, "kho")).toBeHidden();
  await expect(page).toHaveURL("/inventory/stock");
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(groupLinks(page, "kho")).toBeVisible();
});

test("shows the store, owner and business date in the util bar", async ({ page }) => {
  api = await mockSignedInStore(page, { unread: 7 });
  await openScreen(page, HOME_PATH);
  const util = page.locator(".util");
  await expect(util).toContainText(MOCK_STORE.display_name);
  await expect(util).toContainText(MOCK_OWNER.full_name);
  await expect(util).toContainText("12/10/2026");
  await expect(util.getByRole("link", { name: /Việc cần xử lý/ })).toContainText("7");
});

test.describe("at 390px", () => {
  test.use({ viewport: MOBILE });

  test("opens the menu, navigates and closes it", async ({ page }) => {
    api = await mockSignedInStore(page);
    await openScreen(page, HOME_PATH);
    await expectSidebarOnScreen(page, false);
    await showSidebar(page);
    // The group name is a link to the group's first screen.
    await mainNav(page).getByRole("link", { name: "Kho", exact: true }).click();
    await expect(page).toHaveURL("/inventory/stock");
    await expect(menuButton(page)).toHaveAttribute("aria-expanded", "false");
    await expectSidebarOnScreen(page, false);
  });

  test("closes the menu when the scrim is tapped", async ({ page }) => {
    api = await mockSignedInStore(page);
    await openScreen(page, HOME_PATH);
    await showSidebar(page);
    await page.mouse.click(MOBILE.width - 20, MOBILE.height / 2);
    await expect(menuButton(page)).toHaveAttribute("aria-expanded", "false");
    await expect(page).toHaveURL(HOME_PATH);
  });

  test("Esc closes the dialog first, then the menu", async ({ page }) => {
    api = await mockSignedInStore(page, { sandbox: true });
    await openScreen(page, HOME_PATH);
    await showSidebar(page);

    // The scrim covers the util bar for the pointer, but keyboard users can still reach it.
    const reset = page.getByRole("button", { name: "Đặt lại mẫu" });
    await reset.focus();
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "Đặt lại dữ liệu mẫu?" });
    await expect(dialog).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(menuButton(page)).toHaveAttribute("aria-expanded", "true");
    await expect(reset).toBeFocused();

    await page.keyboard.press("Escape");
    await expect(menuButton(page)).toHaveAttribute("aria-expanded", "false");
    await expectSidebarOnScreen(page, false);
  });
});

test.describe("with the real API", () => {
  test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING);
  test.use({ storageState: OWNER_STATE_FILE });

  test("opens every screen of the seeded store from the sidebar", async ({ page }) => {
    await openScreen(page, "/");
    await expect(page).toHaveURL(HOME_PATH);
    // The store name comes from the real session; the API has no business date or unread count yet.
    await expect(page.locator(".util .tenant")).not.toBeEmpty();
    await expect(page.getByRole("button", { name: "Đăng xuất" })).toBeVisible();

    for (const group of MENU) {
      for (const sub of group.subs) {
        await showSidebar(page);
        const toggle = groupToggle(page, group.label);
        if ((await toggle.getAttribute("aria-expanded")) !== "true") {
          await toggle.click();
          await expect(toggle).toHaveAttribute("aria-expanded", "true");
        }
        await groupLinks(page, group.id).getByRole("link", { name: sub.label, exact: true }).click();
        await expect(page).toHaveURL(subPath(group, sub));
        await expect(page.getByRole("heading", { level: 1 })).toHaveText(sub.title);
      }
    }
  });
});
