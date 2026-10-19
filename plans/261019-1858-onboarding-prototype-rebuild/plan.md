---
title: "Rebuild the onboarding screen from prototype v6.14"
description: "Port the new #onboarding screen of docs/design/prototype-v6.14.html into apps/web: frame with step rail, per-step card, new step bodies, and the Thiết lập › Khởi tạo cửa hàng page."
status: completed
priority: P1
effort: 4d
branch: master
tags: [frontend, onboarding, prototype-parity]
blockedBy: []
blocks: []
created: 2026-10-19
---

# Rebuild the onboarding screen from prototype v6.14

## Source of truth

The uncommitted change to `docs/design/prototype-v6.14.html` (+529 lines) adds:

- CSS block `/* ---------- onboarding: cửa hàng mới ... */` (prototype lines ~323–441): every `.onb-*` class.
- JS block `ONBOARDING: cửa hàng mới` (prototype lines ~4189–4596): `ONB_STEPS`, `renderOnb`, `obRail`, `obCard`, `OB_BODY.*`, `A.onb*`, `V['thiet-lap.khoi-tao']`.
- Menu entry `['khoi-tao', 'Khởi tạo cửa hàng']` under Thiết lập.

Commit the prototype change together with phase 1 so the source and the port land side by side.

## Outcome

`/onboarding` looks and behaves like prototype `#onboarding`: navy sticky top bar, page head with the required-step meter, the "Cửa hàng đã sẵn sàng bán" banner, a left rail of 7 steps grouped in 3 phases (collapsible on mobile), a step card with eyebrow/h2/note/tags/status, the step body, and a prev/next footer. Each step body matches the prototype's layout, labels, buttons, validation and messages, backed by the real API. A new settings page `/settings/store-setup` mirrors `V['thiet-lap.khoi-tao']`.

## Constraints

- API contracts stay unchanged (`GET /onboarding`, step commands, `PATCH /tenant`, `POST /stock-adjustments`, `POST /debt-adjustments`, `PATCH /funds/{id}`). Every gap is closed on the web side.
- Reuse shared UI (`Field`, `Combobox`, `MoneyInput`, `QtyInput`, `DatePicker`, `Callout`, `Button`, `KeyValueGrid`, `CommitDialog`, `useCommit`, toasts) and the existing mutations in `features/onboarding/api/mutations.ts`.
- WCAG 2.2 AA as the prototype encodes it: visible labels, error under the field with `aria-invalid` + `aria-describedby`, `aria-current="step"`, `aria-pressed` chips, progressbar values, focus moves to the step `h2` on step change, Escape closes the mobile rail, 44px targets.
- One gradient CTA per screen: when every required step is done, "Vào cửa hàng" is `btn-primary` and the step buttons drop to `btn-navy` (prototype `P`).

## Non-goals

- The prototype's demo bar (`.onb-demo`: "Bản mẫu · cửa hàng mới", "Điền mẫu bước này", "Đặt lại onboarding", "Về cửa hàng mẫu") and the "Đặt lại onboarding" button on the settings page: sandbox tooling only.
- Backend changes, new endpoints, or a kind filter on `/debt-adjustments`.
- Per-product color lists (the app's colors are the global `COLORS` directory).

## User decisions (2026-10-19)

1. Build the Thiết lập › Khởi tạo cửa hàng page in the real app (without the reset button).
2. Drop "Khách ứng trước" and "Ứng trước nhà cung cấp" from the confirmation totals, as the prototype does.

## Known contract-driven deviations (accepted as constraints)

| Prototype | App | Why |
|---|---|---|
| Debt "Lý do" is free text | `<select>` of active `debt_adjustment` reasons, default "Số dư đầu kỳ" | API takes `reason_id` |
| Party is a `<select>` of local parties, callout when none | `Combobox` (remote search) with the "+ Khách hàng mới / + Nhà cung cấp mới" link in the field hint | Parties live in CRM/partners, searched server-side |
| Color select only for items that have colors; required then | Color select (global `COLORS`, "Không màu" allowed) once a SKU is picked | No per-SKU color set in the API |
| One "Theo lô" flag → lot + expiry | `track_lot` → lot input, `track_expiry` → date, independently | SKU contract has both flags |
| Cash: one local save | Sequential `PATCH /funds/{id}` for every fund, then complete the step | One fund per request under If-Match |

## Phases

| # | Phase | Status | Files owned |
|---|---|---|---|
| 1 | [Step model and onboarding styles](phase-01-step-model-and-styles.md) | completed | `features/onboarding/steps.ts`, `shared/styles/globals.css`, prototype commit |
| 2 | [Frame: top bar, page head, rail, step card](phase-02-frame-rail-and-step-card.md) | completed | `app/layouts/onboarding-layout.tsx`, `pages/onboarding-page.tsx`, `components/*` |
| 3 | [Profile, catalog, confirmation and first-sale steps](phase-03-profile-catalog-confirm-first-sale.md) | completed | `steps/store-profile-step.tsx`, `catalog-setup-step.tsx`, `confirm-opening-step.tsx`, `first-sale-step.tsx`, `shared/ui/reauth-dialog.tsx` |
| 4 | [Opening stock, debt and cash steps](phase-04-opening-balance-steps.md) | completed | `steps/opening-stock-step.tsx`, `opening-debt-step.tsx`, `opening-cash-step.tsx`, `api/queries.ts`, `api/mutations.ts` |
| 5 | [Settings › Khởi tạo cửa hàng, tests, parity and docs](phase-05-settings-page-tests-docs.md) | completed | `app/menu.ts`, `features/settings/pages/store-setup-page.tsx`, tests, `e2e/*`, `docs/user-guide/bat-dau.md` |

Phases run in order; 3 and 4 may run in parallel after 2 (disjoint files; both consume the phase 2 `StepFrame` contract).

## Acceptance criteria

1. Side-by-side with prototype `#onboarding` (demo bar excluded) at 1440px and 390px: same structure, labels, button variants, spacing classes and states for each of the 7 steps, including locked, skipped, completed and post-confirmation views.
2. Display order is store_profile, catalog_setup, opening_stock, opening_debt, opening_cash, confirm_opening_balances, first_sale; the meter counts 5 required steps.
3. Opening steps are locked without a cutoff ("Cần ngày chốt ở bước 1") and after confirmation ("Đã khóa sau xác nhận"); first_sale is locked until confirmation.
4. Validation shows each error under its field, toasts "Chưa lưu. Còn N chỗ cần sửa, đã đưa con trỏ tới chỗ đầu tiên." and focuses the first invalid field.
5. Confirmation goes through the password re-auth, then lands on the first_sale step with the ready banner.
6. `/settings/store-setup` lists the 7 steps with type and status and links to `/onboarding`.
7. `pnpm lint`, `pnpm typecheck`, `pnpm test` pass; `e2e/onboarding.spec.ts` and `e2e/a11y` pass; the `onboarding.wizard` baseline is re-reviewed.
8. `docs/user-guide/bat-dau.md` describes the new flow.

## Validation decisions (2026-10-19)

Checked against source; resolved with the default that follows the prototype most closely:

- Reauth copy: `ReauthDialog` has no copy props → add optional `title`/`sub` (phase 3) so the confirm step reads "Nhập lại mật khẩu để khóa sổ".
- Logout: `LogoutButton` already takes `className` → reuse as-is.
- Settings route: menu subs auto-register lazy routes → only `menu.ts` + the page file.
- Visual parity: `onboarding.wizard` stays an addition (the prototype view carries the demo bar and a demo seed); `thiet-lap.khoi-tao` becomes a mapped state with the reset button masked.

## Risks

- Posted-line tables need extra reads (`GET /stock-adjustments?kind=opening` + one detail per document; `GET /debt-adjustments` filtered to `opening_balance`). A new store has few documents; cap at the first page and say so if more exist.
- Saving all funds in sequence can fail half-way; report which funds were saved and keep the rest editable.
- Existing tests assert the old labels ("Đánh dấu xong", the checklist card, navigating to the dashboard after confirmation); they are rewritten, not weakened.
