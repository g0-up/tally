---
phase: 5
title: "Settings › Khởi tạo cửa hàng, tests, parity and docs"
status: pending
effort: 0.5d
---

# Phase 5 — Settings › Khởi tạo cửa hàng, tests, parity and docs

## Context

Prototype `V['thiet-lap.khoi-tao']` and the MENU entry `['khoi-tao', 'Khởi tạo cửa hàng']` between Tài khoản & bảo mật and Thuê bao. Existing tests: `pages/onboarding-page.test.tsx`, `steps/opening-balances.test.tsx`, `e2e/onboarding.spec.ts`, `e2e/acceptance/at-12-cutover-opening-balance.spec.ts`, `e2e/visual/parity-states.ts` (`onboarding.wizard` addition).

## Requirements

Settings page:
- `app/menu.ts`: `sub("khoi-tao", "Khởi tạo cửa hàng", "store-setup", "settings")` after tai-khoan.
- `features/settings/pages/store-setup-page.tsx` (default export; `menuRoutes()` lazy-loads `features/{feature}/pages/{segment}-page.tsx`, so no router edit): `PageHeader` eyebrow "Thiết lập", title "Khởi tạo cửa hàng", lead "Màn onboarding cho chủ cửa hàng mới. App tự chuyển tới màn này khi còn bước bắt buộc chưa xong; chỉ Danh mục được mở trong lúc đó.", action `btn-primary` link "Mở màn onboarding" → `/onboarding`.
- Card: title "Tiến độ cửa hàng mới", note "{d}/{n} bước bắt buộc"; flat table Bước ("{i}. {label}") | Loại (tag Bắt buộc / tag alt Tùy chọn) | Trạng thái (phase 1 status chip). No reset button (user decision).

Tests (rewrite, do not weaken):
- `onboarding-page.test.tsx`: rail lists 7 steps in display order with required/optional and status; opens the first pending step; skip via "Bỏ qua bước này"; API refusal to skip; profile save under If-Match then completes and moves to catalog; cutoff gate; totals shown as the API sends them (without advances); confirm after password then lands on first_sale with the ready banner; blockers named from the API; "Vào cửa hàng" once required steps are done; Escape closes the rail.
- `opening-balances.test.tsx`: stock posting with lot under Idempotency-Key + per-field errors; "Không có tồn kho đầu kỳ" dialog; debt posting, quick customer, direction chips keep amount; cash saves every fund in sequence under If-Match then completes; fund-with-entries refusal message.
- `steps.test.ts` from phase 1; a page test for `store-setup-page`.
- `e2e/onboarding.spec.ts` and `at-12` updated to the new labels and flow.
- `e2e/visual/parity-states.ts`: add route state `thiet-lap.khoi-tao` (path `/settings/store-setup`, prototype viewId `thiet-lap.khoi-tao`, mask the reset button region); keep `onboarding.wizard` as an addition (the prototype version carries the demo bar and its own seed) and re-baseline it after review.
- `e2e/a11y` covers both routes.

Docs:
- `docs/user-guide/bat-dau.md` § "Thiết lập cửa hàng": rail with 3 groups, required vs optional, "Bỏ qua bước này", "Không có tồn kho/công nợ đầu kỳ", saving all funds at once, confirmation leading to the first sale, and the new Thiết lập › Khởi tạo cửa hàng page. Remove "Đánh dấu xong".

## Validation

`pnpm lint && pnpm typecheck && pnpm test`; `pnpm e2e e2e/onboarding.spec.ts`; `pnpm e2e:a11y`; `pnpm e2e:visual` for the two states. Delegate a code review (`code-reviewer`) before merge: cross-module change (auth logout button, settings, menu, shared CSS).

## Rollback

Revert the phase commits; the menu entry and route are additive.
