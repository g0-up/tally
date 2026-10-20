---
title: "Fixed the desktop targets and the axe, keyboard and focus failures"
date: 2026-10-20
summary: "The whole a11y project now passes on the demo store; the focus and keyboard specs had never compared anything, and the review caught three checkbox defects."
---

# Fixed the desktop targets and the axe, keyboard and focus failures

## What happened

The `a11y` Playwright project had failed since before the 390px work: 13 touch-target states at
1440, three axe violations, and every focus and keyboard check. The root cause of the focus and
keyboard failures was in the specs. `locator.evaluate("(el) => …")` passes the function as a
string, and Playwright evaluates it as an expression that returns `undefined` without calling it.
So `tabTo` never saw focus, and the trap and ring checks compared nothing. Once repaired, the
specs found no app bug. They found one more spec bug: the sidebar chevron locator matched on
"Mở rộng Kho", which changes on expand.

## Changes

- e2e: real functions or `:focus` locators instead of string evaluates; `includeHidden` to find the
  opener behind the Radix modal. Every repaired check was shown to fail in a negative case.
- axe: the "Chi" heading uses `--accent-orange-strong`, with a contrast row in `tokens.test.ts`.
  The plan button's accessible name starts with its visible text. A new `TableWrap` turns the
  `.tw` wrapper into a focusable named region only while its table overflows.
- CSS (additions block only): `a.lnk` and `.auth-links a` are inline-flex at 24px. The drawn
  checkbox now applies at every width through `--cb-box` / `--cb-mark` (24/16 desktop, 44/20 at
  ≤860px).

## Review catch

The reviewer found three real checkbox defects:
- With `appearance: none`, the prototype `.f input` border drew a frame around the checkbox.
- The negative vertical margin let a box cover the option above it.
- In forced colours, the native control drew at the full box size.

Fixed with `border: 0` and the padding reset, a horizontal-only margin, and the forced-colours
control at the square size.

## Evidence

- a11y: 188 passed, 6 skipped (print).
- vitest: 1134 passed. Lint and typecheck are clean.
- Parity at 1440 compared on the same store: no newly failing state. `thiet-lap.tai-khoan` went from
  0.07 to 0.09 because the taller checkbox shifts the content below it.
- Parity at 390: identical before and after on the demo store. The before run temporarily reverted
  the CSS and the heading colour.

## Next steps

Nothing is committed. Production still runs the uncommitted 390 changes. The seeded `cua-hang-a`
run and the NFR-11 matrix cell are still open.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
