import { expect, test, type Locator, type Page } from "@playwright/test";

// Focus management of dialogs and the focus indicator (WCAG 2.4.3, 2.4.7). Dialogs come from Radix,
// which traps focus while open and restores it to the opener on close; these specs check that the
// app keeps that behaviour where it matters, including when the opener leaves the page.

const STOCK = "/inventory/stock";
const ADJUST_BUTTON = "Điều chỉnh kho";

const adjustOpener = (page: Page) => page.getByRole("button", { name: ADJUST_BUTTON, exact: true });

async function openAdjustDialog(page: Page): Promise<Locator> {
  await page.goto(STOCK);
  await expect(page.locator("#view h1").first()).toBeVisible();
  await adjustOpener(page).click();
  const dialog = page.getByRole("dialog").first();
  await expect(dialog).toBeVisible();
  return dialog;
}

test("keeps keyboard focus inside the open dialog", async ({ page }) => {
  const dialog = await openAdjustDialog(page);
  for (let i = 0; i < 25; i++) {
    await page.keyboard.press("Tab");
    expect(await dialog.evaluate("(el) => el.contains(document.activeElement)"), `focus after Tab ${i + 1}`).toBe(true);
  }
});

test("returns focus to the opener when the dialog closes", async ({ page }) => {
  const dialog = await openAdjustDialog(page);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(adjustOpener(page)).toBeFocused();
});

test("moves focus into the page, not to the body, when the opener was removed while the dialog was open", async ({ page }) => {
  const dialog = await openAdjustDialog(page);
  // A refresh that drops the button that opened the dialog: the dialog is still open when it happens.
  await adjustOpener(page).evaluate("(el) => el.remove()");
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();

  const focusedInView = await page.evaluate(
    "(() => { const a = document.activeElement; return a !== null && a !== document.body && a.closest('#view') !== null; })()",
  );
  expect(focusedInView, "focus after the dialog closed").toBe(true);
});

test("gives keyboard focus a visible indicator", async ({ page }) => {
  await page.goto(STOCK);
  await expect(page.locator("#view h1").first()).toBeVisible();
  const opener = adjustOpener(page);
  await opener.focus();
  const visible = await opener.evaluate(
    "(el) => { const s = getComputedStyle(el); return (s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0) || s.boxShadow !== 'none'; }",
  );
  expect(visible, "focus outline or ring on the focused button").toBe(true);
});
