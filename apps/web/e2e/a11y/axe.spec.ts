import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { PARITY_STATES, openAppState, skipReason } from "../visual/parity-states";

// Automated WCAG 2.2 AA scan of every parity state (screens, dialogs, additions). Only serious and
// critical violations fail the test; minor ones are reported by the scan but do not block.

const WCAG_TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const BLOCKING = new Set(["serious", "critical"]);

// Data tables need a caption that names them, even when it is visually hidden. Layout tables
// (role presentation or none) are exempt. Runs in the page, so it is a string (no DOM types here).
const TABLES_WITHOUT_CAPTION = `Array.from(document.querySelectorAll("table"))
  .filter((t) => t.getAttribute("role") !== "presentation" && t.getAttribute("role") !== "none")
  .filter((t) => {
    const caption = t.querySelector("caption");
    const named = caption !== null && (caption.textContent ?? "").trim() !== "";
    return !named && !t.hasAttribute("aria-label") && !t.hasAttribute("aria-labelledby");
  })
  .map((t) => t.outerHTML.slice(0, 120))`;

for (const state of PARITY_STATES) {
  test(`${state.id} has no serious or critical accessibility violations`, async ({ page }) => {
    const reason = skipReason(state);
    test.skip(reason !== undefined, reason);

    await openAppState(page, state);
    const results = await new AxeBuilder({ page }).withTags(WCAG_TAGS).analyze();
    const blocking = results.violations
      .filter((v) => v.impact !== undefined && v.impact !== null && BLOCKING.has(v.impact))
      .map((v) => `${v.id} (${v.impact}): ${v.nodes.length} node(s), see ${v.helpUrl}`);
    expect(blocking, "serious or critical axe violations").toEqual([]);

    const untitledTables = (await page.evaluate(TABLES_WITHOUT_CAPTION)) as string[];
    expect(untitledTables, "tables without a caption or accessible name").toEqual([]);
  });
}

// The unit table's "Xóa" links say the same word on every row, so each must name the unit it removes.
// The new-product dialog always draws one removable row; a saved product's units may all be locked by documents.
const UNIT_TABLE = "Đơn vị tính & giá bán";

for (const state of PARITY_STATES.filter((s) => s.id.startsWith("danh-muc.hang-hoa."))) {
  test(`${state.id} names every unit remove button`, async ({ page }) => {
    const reason = skipReason(state);
    test.skip(reason !== undefined, reason);

    await openAppState(page, state);
    const removes = page.getByRole("dialog").getByRole("group", { name: UNIT_TABLE }).getByRole("button").filter({ hasText: /^Xóa$/ });
    if (state.id === "danh-muc.hang-hoa.new-product-dialog") await expect(removes.first()).toBeVisible();
    for (const remove of await removes.all()) await expect(remove).toHaveAccessibleName(/^Xóa đơn vị \S/);
  });
}
