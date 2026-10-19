import { mkdir, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { HOME_PATH, MENU_ROUTES } from "../../src/app/menu";
import { mockSignedInStore } from "../support/mock-api";
import { CONTENT_SELECTOR, SHELL_REGIONS, openPrototype, pinSeedClock, region, waitForFonts, type ShellRegion } from "./prototype-harness";
import { PARITY_STATES, PROTOTYPE_STATES, openAppState, skipReason, type ParityState } from "./parity-states";
import { maskLocators } from "./masks";

// Visual parity of the app with the prototype (docs/design/prototype-v6.14.html) at a desktop and a
// phone width. Prototype states are captured at run time and written to the snapshot path of their
// comparison, so they are never committed; the app is compared with that capture. Additions have no
// prototype, so they are compared with a snapshot committed after design review.

const VIEWPORTS = [
  { name: "1440", width: 1440, height: 900, maxDiffPixelRatio: 0.01 },
  // The sidebar is off-canvas on phones; the menu stays closed for the captures.
  { name: "390", width: 390, height: 844, maxDiffPixelRatio: 0.015 },
] as const;

const PIXEL_THRESHOLD = 0.2;

const SHOT_OPTIONS = { animations: "disabled", caret: "hide" } as const;

// Shell parity: the sidebar, util bar and page header of the dashboard, with the screen content masked.
// Each region is captured from both sides and their boxes must line up.

const SHELL_VIEWPORTS = [
  { name: "1440", width: 1440, height: 900, regions: ["sidebar", "utilBar", "pageHeader"] },
  // The sidebar is off-canvas on phones, so only the bars are compared.
  { name: "390", width: 390, height: 844, regions: ["utilBar", "pageHeader"] },
] as const satisfies readonly { name: string; width: number; height: number; regions: readonly ShellRegion[] }[];

const dashboard = MENU_ROUTES.find((r) => r.path === HOME_PATH);
if (!dashboard) throw new Error(`No menu route for ${HOME_PATH}`);

type Box = { x: number; y: number; width: number; height: number };

async function capture(page: Page, testInfo: TestInfo, side: "prototype" | "app", name: ShellRegion): Promise<Box> {
  const target = region(page, name);
  await expect(target).toBeVisible();
  const box = await target.boundingBox();
  if (!box) throw new Error(`${side} ${name} has no box`);
  const shot = await target.screenshot({ animations: "disabled", mask: [page.locator(CONTENT_SELECTOR)] });
  await testInfo.attach(`${side}-${name}.png`, { body: shot, contentType: "image/png" });
  return box;
}

for (const vp of SHELL_VIEWPORTS) {
  test.describe(`shell at ${vp.name}`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } });

    test(`matches prototype ${dashboard.viewId}`, async ({ page }, testInfo) => {
      await openPrototype(page, dashboard.viewId);
      const prototype = new Map<ShellRegion, Box>();
      for (const name of vp.regions) prototype.set(name, await capture(page, testInfo, "prototype", name));

      // The prototype always runs as the demo store: sandbox pill, footnote and "Đặt lại mẫu".
      await mockSignedInStore(page, { sandbox: true, unread: 8 });
      await pinSeedClock(page);
      await page.goto(HOME_PATH);
      await expect(page.locator(SHELL_REGIONS.pageHeader).locator("h1")).toHaveText(dashboard.sub.title);
      await waitForFonts(page);

      for (const name of vp.regions) {
        const app = await capture(page, testInfo, "app", name);
        const expected = prototype.get(name);
        expect.soft(app.x, `${name} left edge`).toBeCloseTo(expected?.x ?? Number.NaN, 0);
        expect.soft(app.y, `${name} top edge`).toBeCloseTo(expected?.y ?? Number.NaN, 0);
        expect.soft(app.width, `${name} width`).toBeCloseTo(expected?.width ?? Number.NaN, 0);
        if (name !== "pageHeader") expect.soft(app.height, `${name} height`).toBeCloseTo(expected?.height ?? Number.NaN, 0);
      }
    });
  });
}

// Full-viewport parity of every prototype-mapped state. The prototype capture is written to the
// snapshot path that toMatchSnapshot reads, so the comparison runs against this run's capture.
// Regenerate with --update-snapshots only for the app side; the prototype side is always rewritten.
for (const vp of VIEWPORTS) {
  test.describe(`parity at ${vp.name}`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } });

    for (const state of PROTOTYPE_STATES) {
      test(`${state.id} matches the prototype`, async ({ page }, testInfo) => {
        const reason = skipReason(state);
        test.skip(reason !== undefined, reason);
        const proto = state.prototype;
        if (!proto) throw new Error(`${state.id} has no prototype view`);

        await openPrototype(page, proto.viewId);
        if (proto.open) await page.evaluate(proto.open);
        await waitForFonts(page);
        const expected = await page.screenshot({ ...SHOT_OPTIONS, mask: [...maskLocators(page), ...(proto.mask ? [page.locator(proto.mask)] : [])] });

        const name = `prototype/${state.id}-${vp.name}.png`;
        const file = testInfo.snapshotPath(name);
        await mkdir(dirname(file), { recursive: true });
        await writeFile(file, expected);

        await openAppState(page, state);
        const app = await page.screenshot({ ...SHOT_OPTIONS, mask: maskLocators(page) });
        expect(app).toMatchSnapshot(name, { maxDiffPixelRatio: vp.maxDiffPixelRatio, threshold: PIXEL_THRESHOLD });
      });
    }

    for (const state of PARITY_STATES.filter((s): s is ParityState => !s.prototype)) {
      test(`${state.id} matches its committed baseline`, async ({ page }) => {
        const reason = skipReason(state);
        test.skip(reason !== undefined, reason);

        await openAppState(page, state);
        const app = await page.screenshot({ ...SHOT_OPTIONS, mask: maskLocators(page) });
        expect(app).toMatchSnapshot(`addition/${state.id}-${vp.name}.png`, {
          maxDiffPixelRatio: vp.maxDiffPixelRatio,
          threshold: PIXEL_THRESHOLD,
        });
      });
    }
  });
}
