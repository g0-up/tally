---
phase: 2
title: "Fix the three axe violations"
status: completed
---

# Phase 2: axe violations

## Context

Causes #3, #4 and #5 in [plan.md](plan.md). Each one is a single node pattern, found with an axe
probe on the demo store.

## Files

- Modify `apps/web/src/features/dashboard/components/cash-band.tsx`: the "Chi" mini heading
- Modify `apps/web/src/shared/styles/tokens.css`: companions section, only if a named alias is added
- Modify `apps/web/src/shared/styles/tokens.test.ts`: one contrast row
- Modify `apps/web/src/shared/ui/data-table/data-table.tsx` and `apps/web/src/shared/ui/matrix-table.tsx`:
  the `.tw` wrapper
- Possibly create a small hook next to the tables, if the overflow check is shared by both
  wrappers. Reuse an existing hook if one fits.
- Modify `apps/web/src/features/settings/components/plan-cards.tsx`
- Modify `apps/web/src/features/settings/pages/subscription-page.test.tsx`: the button name
- Add or extend unit tests for the table wrapper

## Steps

1. **Contrast.**
   - Colour the "Chi …" heading (`cash-band.tsx:101`) with the existing companion token
     `--accent-orange-strong` (#cb4d22). It is 4.54:1 on white.
   - Leave the chart series, the legend swatch, and the large KPI value (`cash-band.tsx:64`) as
     they are. Large text passes at 3:1.
   - Add a `tokens.test.ts` row: "orange series heading on white", `--accent-orange-strong` on
     `--surface-0`, 4.5.
2. **Scrollable table wrapper.**
   - When the `.tw` wrapper overflows horizontally (`scrollWidth > clientWidth`), give it
     `tabIndex={0}`, `role="region"`, and an accessible name from the table caption, for example
     through `aria-labelledby` pointing at the caption id.
   - Recompute on resize with a `ResizeObserver`.
   - A wrapper that does not overflow stays as it is, so it adds no tab stop.
   - Apply this to both `DataTable` and `MatrixTable`.
   - It receives focus through the existing global `:focus-visible` ring.
3. **Plan button name.**
   - Keep the visible text "Đổi sang gói này".
   - Make the accessible name start with that text, as `aria-label={`Đổi sang gói này: ${p.name}`}`.
   - Update the queries in `subscription-page.test.tsx` that use `Đổi sang gói ${name}` for the
     button. The dialog title stays `Đổi sang gói ${plan.name}`.
4. **Unit tests.**
   - An overflowing wrapper is a focusable named region. A non-overflowing one is not.
   - In jsdom, mock `scrollWidth` and `clientWidth`, as other tests in the repo mock layout.
   - Then run `vitest run` on the touched files.
5. **Rerun axe** on `tong-quan.dashboard`, `danh-muc.hang-hoa` and `thiet-lap.thue-bao`.

## Validation

```
corepack pnpm exec vitest run src/shared/ui src/features/settings src/features/dashboard src/shared/styles/tokens.test.ts
corepack pnpm exec playwright test --project a11y e2e/a11y/axe.spec.ts --grep "tong-quan.dashboard |danh-muc.hang-hoa |thiet-lap.thue-bao " --reporter=line
```

## Risks and rollback

- **A new tab stop** on scrolling tables lengthens the Tab order. `keyboard.spec.ts` allows 120
  tabs. Check that its journeys still pass, which phase 4 does.
- **Heading colour change at 1440** is a few pixels in parity on `tong-quan.dashboard`. Compare it
  in phase 4.
- **Rollback:** revert the files listed above.
