---
phase: 1
title: "Shared CSS and markup fixes for the six causes"
status: completed
---

# Phase 1: shared CSS and markup fixes

## Context

See the cause table in [plan.md](plan.md). Every fix goes in the additions block of
`apps/web/src/shared/styles/globals.css`, after the prototype end marker. The ported rules stay as they are.

## Files

- Modify `apps/web/src/shared/styles/globals.css`:
  - the skip-link rules (lines ~510–511)
  - the `@media (max-width: 860px), (pointer: coarse)` block (lines ~527–538)
  - the onboarding overrides (lines ~447–448 and ~489–490)
- Modify `apps/web/src/app/route-fallbacks.tsx`: the "Về trang đầu" paragraph.
- Modify `apps/web/src/features/settings/pages/account-closure-page.tsx`: the "Về Tài khoản & bảo mật" link.

## Steps

1. **Before any change, record parity at 390** so phase 2 can compare against it. Run
   `--project visual --grep "parity at 390"` and save the diff ratios to the scratchpad.
2. **Skip link.**
   - Hide it in the visually-hidden way while it does not have focus: 1x1 box,
     `clip-path: inset(50%)`, `overflow: hidden`, `white-space: nowrap`. The spec ignores boxes of
     1px or less.
   - On `:focus`, restore the box at `top: 8px` with `min-height: 44px` (`display: inline-flex;
     align-items: center`).
   - Use the same rule for the admin shell, which uses the same class.
3. **`.btn`:** add `.btn` to the `min-height: 44px` list in the 44px block. `.btn` is already
   `inline-flex`.
4. **Links:**
   - In the 44px block, widen `button.lnk` to `.lnk` in the min-height, min-width and inline-flex
     lists. Leave `summary.lnk` out of scope; it is not in the spec's selector list.
   - Add `.auth-links a` to the min-height and inline-flex lists.
   - Every current `TextLink` stands alone (stock table, admin back links, dashboard debt band,
     onboarding catalog). No sentence wraps one, so `inline-flex` does not break text flow.
     Re-check with `grep -rn "<TextLink" src/features` before you finish.
5. **Lone links:**
   - Give the not-found paragraph `className="auth-links"`. It already sits in `.auth-body`.
   - Make the account-closure back link a `TextLink`, like the admin pages' "Về …" back links.
6. **`.cp`:** add `.cp` to the `min-width: 44px` list.
7. **Checkboxes.** In the 44px block, style `input[type="checkbox"]` so its box is 44x44 while the
   visible square stays about 20px:
   - `appearance: none; width: 44px; height: 44px; margin: -12px` (the negative margin keeps the
     row height).
   - Draw the visible square and the check mark with background layers (an SVG data URI for checked).
   - Add a `:focus-visible` ring on the drawn square and keep the `:disabled` look.
   - Under `@media (forced-colors: active)`, fall back to `appearance: auto` at the same 44px box.
   - Check that this beats `.onb .check input { width: 20px; height: 20px }`, which is ported from
     prototype line 391. Make onboarding use the global rule. Do not edit the ported line; override
     it in the additions block.
   - The raw checkbox in `features/catalog/attribute-field-registry.tsx:33` gets the same treatment
     through the element selector.
8. **Remove onboarding overrides that are now redundant:**
   - `.onb .btn` from line ~489, which the global `.btn` covers. Keep `.onb-out` and `.onb .chip`
     unless global rules cover them.
   - `.onb .btn-row > .lnk` from line ~490, which the global `.lnk` covers.

## Validation

- `corepack pnpm lint && corepack pnpm typecheck`
- `timeout 590 corepack pnpm exec vitest run src/app/layouts src/features/admin src/features/settings src/app`
- Rerun the 390 touch-target command from phase 2 step 1 on the fresh store. All six causes must be gone.

## Risks and rollback

- **Taller `.btn` at ≤860px shifts content in the 390 parity captures.** The app already differs
  from the prototype at 390 in the same way for `.btn-sm` and fields. Phase 2 compares diff ratios.
  If a state crosses 1.5% only because of this change, record it in the phase 2 report. Do not drop
  the fix: the fix is the requested outcome and parity is the secondary check.
- **The custom checkbox can look different from the native one.** Check it at 390 in light theme,
  checked, unchecked, disabled, with focus, and in forced colors.
- **Rollback:** revert the globals.css additions and the two TSX edits. No data or API impact.
