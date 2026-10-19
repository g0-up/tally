---
title: "Phase 20: UI/UX review & accessibility"
status: in-progress
priority: P1
effort: 5d
dependsOn: [phase-15-web-catalog-procurement, phase-16-web-sales-pos, phase-17-web-inventory-kits, phase-18-web-debt-campaign-reports-settings, phase-19-pos-offline-pwa]
---

# Phase 20 — UI/UX review & accessibility

## Context links

- UI spec `plans/reports/design-261016-1549-prototype-ui-spec.md` §1 tokens, §1.4 breakpoints, §3 components, §5 per-screen, §8 18 gaps, §9 dead views
- Prototype `docs/design/prototype-v6.14.html` (visual baseline, seed business date 2026-10-12)
- Phase 14 visual harness `apps/web/e2e/visual/prototype-harness.ts`
- PRD NFR-10 (last two major browser versions, real Android/iOS, certified printers/scanners), NFR-11 (keyboard-accessible core flows, contrast, visible focus, WCAG 2.2 AA), §9.2 screen patterns, §9.3 UX rules

## Overview

A gate phase: no new features. It reviews every built screen against the prototype and the PRD UX rules, closes the 18 known a11y/UX gaps, enforces automated visual-parity and accessibility thresholds in CI, and runs the browser/device matrix. Findings are fixed in the owning feature folder; anything that changes behaviour or labels beyond a defect fix goes back to the owner for a decision.

## Key insights

- Parity is measured against the prototype rendered from its own seed, and the React app rendered from a sandbox tenant seeded with the same data and business date 2026-10-12. Dynamic values (timestamps, generated document numbers, relative "Quá N ngày") are masked, not faked.
- Additions beyond the prototype (auth, onboarding, admin, print, settings cards, offline states, exception drawer) have no baseline; they get a design review plus their own committed Playwright snapshots after approval.
- Most gaps were addressed at build time in phases 14–19; this phase verifies each one with an explicit test so regressions fail CI.
- Accessibility automation catches roughly a third of issues; keyboard journeys and screen-reader passes are manual and recorded.

## Owner decisions (2026-10-19)

Taken after the module reviews `plans/reports/ui-ux-designer-261019-1837-ui-review-group-a.md` and `plans/reports/ui-ux-designer-261019-1839-ui-review-group-b.md`:

- Contrast: failing pairs get new darker tokens added after the prototype marker in `tokens.css` (white on orange and crimson buttons, input border, placeholder, focus ring on dark headers); prototype values stay, before/after recorded in the report.
- Confirmations: "Khóa sổ" (date, reason, impact, like reopen), "Gửi lời mời chuyển quyền" (recipient and effect), supplier "Ngừng", closing a receipt with unsaved lines; the MFA recovery-code dialog cannot be dismissed until "Tôi đã lưu mã" is ticked and offers copy and .txt download.
- Sidebar groups: the group name is a link; a separate chevron button expands or collapses its children.
- Touch targets: ≥ 24×24 px everywhere; ≥ 44 px hit area at viewport width ≤ 860 px.

## Requirements

| PRD ID | Behaviour |
|---|---|
| NFR-11 | Zero axe-core `serious`/`critical` violations on every route and primary dialog/drawer; keyboard-only completion of core journeys; visible focus; WCAG 2.2 AA contrast |
| NFR-10 | Pass on last two major versions of Chrome, Edge, Safari, Firefox; real Android (Chrome) and iOS (Safari); barcode scanner (HID keyboard wedge) and receipt/A4 printer smoke |
| §9.2, §9.3 | Success only after server confirmation, no click swallowed after blur, long exports with progress, error toasts persist |
| UI spec §8 | All 18 gaps closed and tested (table below) |

## Gap closure matrix

| # | Gap | Fix (owner phase) | Verification |
|---|---|---|---|
| 1 | POS customer native select | `Combobox` party pool (16) | `pos.spec.ts` keyboard pick |
| 2 | Table inputs lack row context | aria "SL – {tên hàng}" etc. (14 kit, 15–18 usage) | axe + unit label queries |
| 3 | Duplicate "Chưa thanh toán" tone | typed `status-tone.ts` (14) | type test |
| 4 | Accordion nav navigates on expand | separate expand/navigate, `aria-current` (14) | `shell.spec.ts` |
| 5 | No focus trap/restore | Radix Dialog/Sheet (14) | `focus.spec.ts` |
| 6 | Error toasts auto-dismiss | errors persist; doc numbers kept in drawers/lists (14) | unit + e2e |
| 7 | Chart tooltip hover-only | "Xem dạng bảng" + focusable bars (18) | keyboard e2e |
| 8 | Decorative `.st` glyph | `aria-hidden` glyph (14) | axe |
| 9 | Mix-group link actions without confirm | `Button size=sm` + confirm dialogs (15) | `catalog.spec.ts` |
| 10 | Swatch radios no roving | Radix RadioGroup (14) | arrow-key unit test |
| 11 | colorPrice `disabled` inputs | `readOnly` + explanation (17; same pattern in 15 mix-group) | unit |
| 12 | Tables lack caption | visually hidden caption from section title (14) | axe `table-caption` rule custom check |
| 13 | Native date inputs | dd/mm/yyyy DatePicker (14) | unit + e2e |
| 14 | CSV copy-only | async export job + download (18) | `reports.spec.ts` |
| 15 | Blur re-render swallows click | controlled inputs (14, 16) | `pos.spec.ts` blur-then-click |
| 16 | POS shift opens silently | Decision: keep implicit shift per CASH-06; fallback callout only (16). Owner to confirm | design review note |
| 17 | Success toast before commit | `useCommit` toasts after 2xx (14) | MSW delayed-response test |
| 18 | Small touch targets | ≥24×24 CSS px everywhere (WCAG 2.5.8), ≥44px hit area ≤860px (14) | `touch-targets.spec.ts` bounding-box scan |

## Architecture

```
apps/web/e2e/visual/
  prototype-harness.ts         (phase 14) load prototype with seed, freeze clock, navigate by hash
  parity.spec.ts               route + state list → screenshot prototype and app, compare
  parity-states.ts             per route: default view, primary dialog, drawer, empty, error states
  masks.ts                     selectors for dates, doc numbers, relative-day text
apps/web/e2e/a11y/
  axe.spec.ts                  @axe-core/playwright over every route/state in parity-states + additions
  keyboard.spec.ts             core journeys with keyboard only
  touch-targets.spec.ts        bounding-box scan at 390px
apps/web/e2e/matrix/           project config for browsers; device runbook in docs
```

Thresholds (Playwright `toHaveScreenshot` on the app compared with a prototype capture):

| Viewport | maxDiffPixelRatio | threshold | Notes |
|---|---|---|---|
| 1440×900 | 0.01 | 0.2 | fonts loaded, animations disabled |
| 390×844 | 0.015 | 0.2 | off-canvas nav closed |

Prototype captures are generated in CI from the prototype file (not committed) to avoid drift; app snapshots for additions are committed under `e2e/visual/__snapshots__/`.

## Review procedure

1. Run the installed UI/UX review skill (resolved from the live skill catalog; e.g. `/ak-ui-ux-pro-max` for visual/interaction review and `/ak-enhance-ux-ax` for UX/AX enhancement) per module: Tổng quan, Bán hàng, Nhập hàng, Kho, Giỏ quà, Công nợ, Chiến dịch & khách, Báo cáo, Danh mục, Thiết lập, plus auth, onboarding, admin, print, offline. Record findings in `plans/reports/` with severity.
2. Triage: defect (fix now, owner folder), label/behaviour change (owner decision), enhancement (backlog).
3. Fix defects; re-run parity and axe for the touched routes.
4. Keyboard journeys (manual + `keyboard.spec.ts`): login → POS sale with color pick and cash change → hold cart → invoice create/confirm with exception → delivery → receipt with allocation → stock count → report export → settings device sign-out.
5. Screen-reader pass with NVDA (Windows/Firefox) and VoiceOver (iOS Safari) on POS, invoice dialog, exception dialog, debt payment, offline banner (`role="status"`, sync results `aria-live="polite"`, errors `role="alert"`).
6. WCAG 2.2 AA specifics: contrast of tokens incl. muted text and tag colors (fix tokens centrally in `tokens.css` if any fail, recording the deviation from prototype), focus not obscured by sticky util bar/drawer headers (2.4.11), dragging alternatives (none expected), consistent help location, redundant entry avoided in multi-step dialogs (3.3.7), accessible authentication without cognitive tests (3.3.8: password managers and paste allowed; TOTP paste allowed).
7. Browser/device matrix: Playwright projects chromium, firefox, webkit, plus msedge channel in CI; manual on real Android and iOS devices; HID barcode scanner on POS scan input and stock count; print smoke (80mm receipt and A4) for `/print/sales-documents/:id` and statements.
8. Performance sanity in the same pass: route chunk sizes, POS interaction latency (scan → line < 100 ms local), no layout shift on tile grid.

## Related files

Create: `apps/web/e2e/visual/{parity.spec.ts,parity-states.ts,masks.ts}`, `apps/web/e2e/a11y/{axe.spec.ts,keyboard.spec.ts,touch-targets.spec.ts,focus.spec.ts}`, review reports under `plans/reports/`, device/browser runbook `docs/` page (location per docs navigation at that time).

Modify: `apps/web/playwright.config.ts` (projects, viewports, snapshot settings), CI workflow (parity + axe jobs, phase 01 owns the file; coordinate), feature files only for defect fixes.

## Implementation steps

1. Enumerate parity states for all 40 routes and their primary dialogs/drawers in `parity-states.ts`.
2. Implement masks and the parity spec; first run produces a diff report; triage.
3. Add axe spec over the same states plus additions; fail on serious/critical.
4. Add keyboard, focus and touch-target specs.
5. Run review skills per module; write reports; fix defects.
6. Contrast audit of tokens; adjust centrally if required.
7. Browser matrix in CI; manual device and peripheral runbook executed and recorded.
8. Lock thresholds in CI as required checks.

## Todo

- [ ] Parity states list (40 routes + dialogs/drawers) (`parity-states.ts` written: 61 states = 40 routes + 5 dialogs + 16 additions; drawers and 10 admin pages not covered; not run)
- [ ] Parity spec + masks + CI job (spec and masks written, typecheck and `--list` ok; nightly `e2e-nightly` job written, never run; prototype captures never generated)
- [ ] axe spec (all states + additions) + CI job (spec written, not run; same nightly job)
- [ ] Keyboard, focus, touch-target specs (written; keyboard covers 3 of 9 journeys; none run)
- [x] UI/UX review per module + reports
  - Evidence: `plans/reports/ui-ux-designer-261019-1837-ui-review-group-a.md`, `plans/reports/ui-ux-designer-261019-1839-ui-review-group-b.md`; fixes in `plans/reports/fullstack-developer-261019-0014-ui-a11y-fixes-group-a.md` and `plans/reports/fullstack-developer-261019-0649-ui-a11y-fixes-group-b.md`. High and medium findings fixed with unit tests; web typecheck, lint and vitest (156 files, 1109 tests) green on 2026-10-19. Low leftovers deferred with reasons in the fix reports.
- [ ] Gap matrix: every row verified by a test (unit test per row, vitest green; rows whose verification is e2e or axe not run; row 16 awaits owner confirmation)
- [ ] Contrast + WCAG 2.2 AA specifics (contrast tokens before/after asserted in `tokens.test.ts`, green; 3.3.7 and 3.3.8 pass in review; 2.4.11 and 2.5.8 need axe/e2e runs, not run)
- [ ] Screen-reader pass recorded (not run; runbook `docs/operations/device-browser-matrix.md` written)
- [ ] Browser/device/peripheral matrix recorded (runbook written; matrix not run)
- [ ] Thresholds set as required CI checks (no e2e job has run, so nothing to lock yet)

## Success criteria & verification

- `pnpm -C apps/web typecheck && pnpm -C apps/web lint && pnpm -C apps/web test` green.
- `pnpm -C apps/web exec playwright test e2e/visual/parity.spec.ts` passes at 1440 and 390 within thresholds for every listed state.
- `pnpm -C apps/web exec playwright test e2e/a11y` passes: zero serious/critical axe violations; keyboard journeys complete; no target < 24×24, mobile hit areas ≥ 44px.
- Every gap matrix row links to a passing test or an owner-approved decision (row 16).
- Review reports list no open defect of severity high; medium items have owners.
- Matrix: chromium/firefox/webkit/msedge green in CI; manual Android/iOS, scanner and printer checks recorded with device/browser versions.

## Risks

- Pixel diffs from font rendering across OS → run parity only on the Linux CI image; other browsers run functional + axe, not pixel parity.
- Token contrast fixes change look vs prototype → keep the change minimal and record in the report; owner sees before/after.
- Review uncovers behaviour gaps late → time-box; non-defects go to backlog rather than block release.

## Security

- Parity and axe runs use the sandbox tenant only; no production data in screenshots or reports.
- Review reports must not contain credentials, tokens or customer personal data.

## Rollback

Tests and thresholds can be relaxed only by an explicit reviewed change to `playwright.config.ts`; defect fixes revert per feature commit.

## Next

Phase 21 (acceptance, load, security, release) runs the acceptance suite including AT-11, load and security tests, and release on top of the green UI gate.
