---
title: Plan the onboarding rebuild from prototype v6.14
date: 2026-10-19
summary: "Five-phase plan to port the new #onboarding screen and the Khởi tạo cửa hàng settings page into apps/web"
---

# Plan the onboarding rebuild from prototype v6.14

## What happened

The uncommitted prototype change (docs/design/prototype-v6.14.html, +529 lines) adds a full #onboarding screen: sticky navy top bar, required-step meter, ready banner, a 7-step rail in 3 phases (collapsible on mobile), a step card with prev/next/skip footer, and new bodies for every step, plus a Thiết lập › Khởi tạo cửa hàng view. The current app uses a Stepper + checklist card with generic "Đánh dấu xong" / "Bỏ qua" actions.

Plan: plans/261019-1858-onboarding-prototype-rebuild/plan.md (5 phases: step model + CSS, frame, profile/catalog/confirm/first-sale, opening stock/debt/cash, settings page + tests + docs).

## Decision

- Display order is a web constant (confirm before first_sale); the API key order stays unchanged.
- Contract gaps are closed web-side, with no API changes: the debt reason stays a select (the API needs reason_id), colors come from the global directory, cash saves every fund with sequential PATCH /funds/{id}, and "Tồn kho theo màu" is grouped client-side from the posted opening stock lines.
- The user chose to drop the advances rows from the confirmation totals and to build the settings page without the reset button.
- ReauthDialog gains optional title/sub so the confirm step can read "Nhập lại mật khẩu để khóa sổ".
- After confirmation, the flow lands on the first_sale step instead of the dashboard.

## Next steps

Run /ak:cook on the plan; commit the prototype with phase 1; rewrite (not weaken) the onboarding unit and e2e tests; re-baseline the onboarding.wizard visual state.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
