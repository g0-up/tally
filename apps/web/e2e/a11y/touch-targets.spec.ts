import { expect, test } from "@playwright/test";
import { PARITY_STATES, openAppState, skipReason } from "../visual/parity-states";

// Target size of every control on each state (WCAG 2.5.8 and the owner decision of 2026-10-19): at
// least 24×24 CSS px everywhere, and at least 44 px on a phone-width viewport (≤ 860 px). The check
// measures the element's box, so a control that is smaller than its hit area needs padding to pass.

const VIEWPORTS = [
  { name: "390", width: 390, height: 844, minSide: 44 },
  { name: "1440", width: 1440, height: 900, minSide: 24 },
] as const;

const CONTROLS =
  'a[href], button, input:not([type="hidden"]), select, textarea, [role="button"], [role="link"], [role="checkbox"], [role="radio"], [role="switch"], [role="tab"], [role="combobox"], [role="menuitem"]';

/** Controls below the minimum side, as "label WxH". Runs in the page, so it is a string (no DOM types here). */
function undersized(minSide: number): string {
  return `Array.from(document.querySelectorAll(${JSON.stringify(CONTROLS)}))
    .filter((el) => !el.closest('[aria-hidden="true"], [inert]'))
    .filter((el) => getComputedStyle(el).visibility !== "hidden")
    .map((el) => ({ el, box: el.getBoundingClientRect() }))
    .filter(({ box }) => box.width > 1 && box.height > 1)
    .filter(({ box }) => box.width < ${minSide} || box.height < ${minSide})
    .map(({ el, box }) => {
      const label = (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 40);
      return label + " " + Math.round(box.width) + "x" + Math.round(box.height);
    })`;
}

for (const vp of VIEWPORTS) {
  test.describe(`touch targets at ${vp.name}`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } });

    for (const state of PARITY_STATES) {
      test(`${state.id} keeps every control at least ${vp.minSide}px`, async ({ page }) => {
        const reason = skipReason(state);
        test.skip(reason !== undefined, reason);

        await openAppState(page, state);
        const small = (await page.evaluate(undersized(vp.minSide))) as string[];
        expect(small, `controls smaller than ${vp.minSide}px`).toEqual([]);
      });
    }
  });
}
