---
phase: 3
title: "Desktop 24px targets"
status: completed
---

# Phase 3: desktop 24px targets

## Context

Causes #6, #7 and #8 in [plan.md](plan.md). The owner decided on 2026-10-19 that targets are 24px
at every width and 44px at ≤860px. Desktop rules go in the "Every target is at least 24x24 CSS px"
lines of the `globals.css` additions block (~521–524). The 44px block stays as it is, and its
values override the desktop ones inside its media query.

## Files

- Modify `apps/web/src/shared/styles/globals.css`, additions block only:
  - the 24px lines (~521–524)
  - the checkbox rules (~539–551), when the drawn checkbox is shared

## Steps

1. **Before any change, record parity at 1440.** Run
   `--project visual --grep "parity at 1440"` on the demo store. Save the diff ratios to the
   scratchpad. Run it before phase 2 lands too, or use a stash of `src`, so the baseline is clean.
2. **Inline links.**
   - Make `a.lnk` and `.auth-links a` `display: inline-flex; align-items: center; min-height: 24px`.
   - Give `a.lnk` `min-width: 24px`.
   - This covers "Thẻ kho", "Về Tài khoản & bảo mật" (a `TextLink`), and "Về trang đầu" (inside
     `.auth-links`).
   - Check that a wrapped or truncated `.lnk` inside a table cell still lays out as before.
3. **Checkbox at 24px.**
   - Move the drawn checkbox out of the ≤860px block so it applies at every width, with a size
     variable: a 24px transparent box at desktop and 44px at ≤860px or coarse pointers.
   - Keep the drawn square at about 16px on desktop and 20px on mobile. Keep its checked, disabled
     and `:focus-visible` states.
   - Make the forced-colors fallback (native control) apply at every width.
   - Adjust the negative margin so the visible square keeps its place in `.check` rows.
4. **Run touch at 1440 and at 390** on the demo store.

## Validation

```
corepack pnpm exec playwright test --project a11y --grep "touch targets at" --reporter=line
```

Touch at 1440 has only the print states skipped and no failures. Touch at 390 still passes.

## Risks and rollback

- **Parity at 1440 changes** where the checkbox or inline links render: the auth states,
  `thiet-lap.tai-khoan`, the exchange dialog and `kho.ton`. Accept a small ratio change on those
  states only. Phase 4 compares.
- **Inline-flex** on an `a.lnk` inside running text would break the line flow. Search for
  `a.lnk`/`TextLink` inside paragraphs. If one exists, use `display: inline-block` with padding
  for that case instead.
- **Rollback:** revert the additions-block lines.
