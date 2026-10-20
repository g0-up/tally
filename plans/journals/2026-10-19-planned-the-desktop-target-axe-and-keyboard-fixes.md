---
title: "Planned the desktop target, axe and keyboard fixes"
date: 2026-10-19
summary: "Plan 261020-0021: five focus/keyboard failures are a spec bug (string functions in locator.evaluate return undefined); three axe nodes and the 1440 targets have known sources."
---

# Planned the desktop target, axe and keyboard fixes

## What happened
Scouted the failures left over from the 390 touch-target work: 13 touch-target failures at 1440, and 9 axe, keyboard and focus failures.
- **Five focus and keyboard checks never compared anything.** They pass the function to `locator.evaluate` as a string, `"(el) => …"`. On Playwright 1.64.1 that returns `undefined`; a probe confirmed a real function returns `true`. So `tabTo` never saw focus.
- **The opener-removed test times out** because `getByRole` cannot see elements that Radix hides with `aria-hidden` while the modal is open.
- **An axe probe on the demo store printed the nodes:**
  - the "Chi" heading in `cash-band.tsx` is #f45d29 on white, 3.25:1;
  - the `.tw` table wrapper scrolls but cannot take focus (`data-table.tsx`, `matrix-table.tsx`);
  - the plan buttons have `aria-label` "Đổi sang gói {tên}" while the visible text is "Đổi sang gói này".
- **The 1440 targets fail** because inline `a.lnk` and `.auth-links a` ignore `min-height`, and the drawn checkbox exists only at ≤860px.

## Decision
Plan `plans/261020-0021-desktop-targets-axe-keyboard`, 4 phases: spec repair, axe, desktop 24px, full verification. Owner choices:
- a 16px drawn checkbox in a 24px box;
- the button name "Đổi sang gói này: {tên}";
- the table wrapper is focusable only while it overflows;
- `--accent-orange-strong` for the small heading only.

The repaired specs must still be shown to fail in a negative case, and any real app failure they reveal is in scope.

## Next steps
`/ak:cook plans/261020-0021-desktop-targets-axe-keyboard/plan.md`. Run the full suite in the background on the demo sandbox store so it does not stop at 137/194 again.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
