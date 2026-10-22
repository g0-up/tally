import { fileURLToPath, pathToFileURL } from "node:url";
import { expect, type Locator, type Page } from "@playwright/test";

// Opens the interactive prototype (docs/design/prototype-v6.15.min.html) the app is measured against.
// The prototype seeds its demo data on first load into localStorage; each Playwright test gets a
// fresh browser context, so every visit starts from the same seed. The clock is pinned to the
// seed's business date so nothing drifts with the real date.

export const PROTOTYPE_FILE = fileURLToPath(new URL("../../../../docs/design/prototype-v6.15.min.html", import.meta.url));

/** Business date of the prototype seed. */
export const SEED_DATE = "2026-10-12";

/** Shell regions present in both the prototype and the app, by the class names they share. */
export const SHELL_REGIONS = {
  sidebar: ".side",
  utilBar: ".util",
  pageHeader: "#view .ph",
} as const;
export type ShellRegion = keyof typeof SHELL_REGIONS;

/** Everything on the screen except its page header: the part each feature screen owns. */
export const CONTENT_SELECTOR = "#view > :not(.ph)";

export async function pinSeedClock(page: Page): Promise<void> {
  await page.clock.setFixedTime(new Date(`${SEED_DATE}T09:00:00+07:00`));
}

/** Opens prototype view `mod.sub` (for example "tong-quan.dashboard") and waits for its header. */
export async function openPrototype(page: Page, viewId: string): Promise<void> {
  await pinSeedClock(page);
  await page.goto(`${pathToFileURL(PROTOTYPE_FILE).href}#${viewId}`);
  await expect(page.locator(SHELL_REGIONS.pageHeader).locator("h1")).toBeVisible();
  await waitForFonts(page);
}

/** Web fonts change text metrics; capture only after they are in. */
export async function waitForFonts(page: Page): Promise<void> {
  // A string expression: it runs in the page, and this file is typed without the DOM library.
  await page.waitForFunction("document.fonts.status === 'loaded'");
}

export function region(page: Page, name: ShellRegion): Locator {
  return page.locator(SHELL_REGIONS[name]).first();
}
