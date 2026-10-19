---
phase: 1
title: "Step model and onboarding styles"
status: completed
effort: 0.5d
---

# Phase 1 — Step model and onboarding styles

## Context

Prototype `ONB_PH`, `ONB_STEPS`, `ONB_OPENING`, `obLock`, `obReqDone`, `obBlockers`, `obStatusChip` (prototype ~4189–4240) and the CSS block (~323–441). Current model: `apps/web/src/features/onboarding/steps.ts` orders steps by the API's `ONBOARDING_STEP_KEYS` (first_sale before confirm) and has no phases or locks.

## Requirements

- Display order: store_profile, catalog_setup, opening_stock, opening_debt, opening_cash, confirm_opening_balances, first_sale. Do not change `ONBOARDING_STEP_KEYS` in `shared/api/endpoints.ts` (API order).
- Phases: `["Cửa hàng", "Số dư đầu kỳ", "Chạy thử"]`; step → phase 0,0,1,1,1,1,2.
- Step notes (card lead) copied verbatim from `ONB_STEPS[].note`; labels from `ONB_STEPS[].label` (note: confirm becomes "Xác nhận và khóa số dư").
- Required = not `skippable` from the API (fallback set stays catalog_setup, first_sale).
- `stepLock(step, { cutoff, confirmed })` returns the prototype's lock text: opening steps → "Đã khóa sau xác nhận" once confirmed (confirm itself unlocked), "Cần ngày chốt ở bước 1" without cutoff; first_sale → "Mở sau khi xác nhận số dư" until confirmed.
- `blockers(steps)`: store_profile, opening_stock, opening_debt, opening_cash not completed (prototype `obBlockers`).
- Status chip text/tone: completed "Đã xong" ok; skipped "Đã bỏ qua" mute; locked → lock text mute; else "Chưa làm" neutral (`.st` without tone).
- Remove `leadingFinished`, `NOT_MANUAL`/`manual` if phase 2 no longer uses them.

## Files

- Modify `apps/web/src/features/onboarding/steps.ts`.
- Modify `apps/web/src/shared/styles/globals.css`: replace the 5 current `.onb*` rules (lines ~387–391) with the prototype block, minus `#onb-root`/`body.is-onb` (router replaces them) and `.onb-demo*` rules. Keep `.sr-only` only if not already defined. Keep the `.onb-top :focus-visible` accessibility override.
- Commit `docs/design/prototype-v6.14.html`.

## Steps

1. Port the CSS block; diff class names against the prototype to make sure none is missed.
2. Rewrite `steps.ts` with `ONBOARDING_PHASES`, `DISPLAY_ORDER`, `STEP_NOTES`, `stepLock`, `blockers`, `stepStatusChip`; keep `orderedSteps` returning the display order.
3. Unit tests in a new `steps.test.ts` for order, required count (5), locks and blockers.

## Validation

`pnpm vitest run src/features/onboarding/steps.test.ts`, `pnpm typecheck`.

## Rollback

Revert the two files; the API contract is untouched.
