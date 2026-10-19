---
title: "Phase 14: Web foundation & design system"
status: done
priority: P1
effort: 8d
dependsOn: [phase-02-api-platform-foundation, phase-04-identity-organization]
---

# Phase 14 — Web foundation & design system

## Context links

- UI spec (authority for every label, token, component): `plans/reports/design-261016-1549-prototype-ui-spec.md` §1 tokens, §2 shell, §3 components, §4 route map, §7 formatting, §8 gaps, §9 dead views
- Prototype: `docs/design/prototype-v6.14.html` (`:root` lines 9–25, shell 36–72 / 324–337, router 1742–1787, `MOVED` 3471, `post()` pipeline, `combo()` 3781)
- Architecture: `design/architecture.md` §3.6 HTTP conventions, §4.1 source tree, §4.2 patterns, §4.3 fidelity rules, §6 security baseline, §7 tenant hosts (host kinds, cookies, dev `*.localhost`)
- Glossary: `design/ubiquitous-language.md`
- PRD: §9.1 menu, §9.2 screen patterns, §9.3 UX rules, SEC-01, SEC-06, CAT-08, CAT-09, ORG-01, ORG-06, NFR-10, NFR-11

## Overview

Builds everything the feature phases (15–18) stand on: tokens, the app shell, the router with all 40 menu routes and `MOVED` redirects, the HTTP client that speaks the API contract, the shared UI kit that mirrors the prototype markup, the commit pipeline, auth screens, guards and the onboarding shell. After this phase every menu route renders its page header and a "Đang xây dựng." placeholder inside the real shell, signed in against the real API.

## Key insights

- The prototype is a single-owner demo (`role = owner`). SEC-01 makes that the product: no role or permission UI anywhere. `can('viewCost')` / `can('sellCredit')` gates disappear; cost is always visible, and "Ẩn theo quyền" is never rendered.
- `post(btn, fn, okMsg, after)` is the one write contract. Re-implement it as `useCommit()` over `useIdempotentMutation`: pending label "Đang ghi sổ…", error toast "<b>Chưa ghi sổ.</b> {message}", success toast only after the server responds 2xx (gap #17).
- One `oneStore` flag (ORG-03: always true in 1.1) hides `Kho`, `Kho · phạm vi`, `Chi nhánh` columns and "Kho nhận" / "Kho đóng" fields. Build it into `DataTable` column metadata (`hideWhenOneStore`) and `Field`, not per screen.
- Tokens are copied byte-for-byte from prototype `:root`; a lint rule bans raw hex and arbitrary Tailwind colors under `src/features` so fidelity cannot drift.
- Combobox (CAT-08) is the most reused interactive control; get keyboard behaviour right once (↑/↓, Enter, Esc, Tab auto-pick single match, max 50, accent-insensitive).
- Group nav in the prototype both expands and navigates (gap #4). Keep the visual accordion; the group button toggles `aria-expanded` and navigates to its first sub, and each sub link carries `aria-current="page"`. Phase 20 audits the pattern.

## Requirements

| PRD ID | Behaviour in this phase |
|---|---|
| ORG-01 | Login, sign-up, contact verification, forgot/reset password, sign-out, MFA challenge screens on `AuthLayout` |
| ORG-06 | Sandbox tenant shows util pill "Cửa hàng mẫu · dữ liệu demo" and "Đặt lại mẫu" → modal "Đặt lại dữ liệu mẫu?" calling the sandbox reset endpoint; hidden for real tenants |
| SEC-01 | Single owner; util label "Chủ cửa hàng · {full_name}"; no role/permission screens or gates |
| SEC-06 | `RequireRecentAuth` re-auth dialog (password + TOTP) when the API answers `401 reauth_required`; reused by write-off, period reopen, export, ownership transfer, MFA reset |
| SAAS-03 | `RequireEntitlement` + read-only banner when subscription is `read_only`/`grace`; posting buttons disabled with explanation, export still allowed (PRD §9.3) |
| SAAS-04 | Onboarding shell route `/onboarding` with stepper frame and checklist widget; step bodies land in phase 18 |
| CAT-08 | Shared `Combobox` (pools sell/buy/stock/kit/party) with exact prototype behaviour |
| CAT-09 | `ColorSwatchGroup`, `ColorChip`, `ColorQtyPill`, `StockColorPicker` over the 7 fixed colors + "Chưa gán màu" hatch; never a text input |
| PRD §9.3 | No double submit; processing state; doc number in result; no success before server; blur must not swallow the next click (controlled inputs, gap #15) |
| NFR-10 | Browserslist = last 2 major Chrome/Edge/Firefox/Safari + iOS Safari 17+ + Android Chrome; Playwright projects chromium/firefox/webkit + mobile 390 |
| NFR-11 | Focus-visible outline navy-700 2px offset 2px; Radix primitives for dialog/sheet/radio/popover (focus trap + restore, gap #5) |

## Architecture

```
main.tsx ─ GET /api/v1/host-context → kind (root | tenant | admin) ─ providers (QueryClient, RouterProvider, Toaster, SessionProvider, TenantProvider)
app/router.tsx ─ createBrowserRouter(routesFor(kind))   one bundle, three route trees; other paths → 404 page
  root host  (DOMAIN):          AuthLayout: /signup, /find-store; / → /signup
  tenant host (<slug>.DOMAIN):
  ├─ AuthLayout: /login /verify /forgot-password /reset-password /mfa
  ├─ RequireAuth ─ OnboardingGate ─ AppShell (sidebar + util bar + <Outlet/>)
  │     ├─ 40 MENU routes (lazy: features/<f>/pages/*) ── RequireEntitlement(module)
  │     ├─ group paths → <Navigate to first sub>
  │     └─ legacy hash shim: "#mod.sub" → path via LEGACY_HASH_MAP (mirrors MOVED)
  ├─ RequireAuth ─ /onboarding (OnboardingLayout, stepper shell)
  admin host (admin.DOMAIN):
  └─ /admin/* ─ RequireAdminAuth ─ AdminShell (pages in phase 18)
shared/api/http.ts ─ fetch wrapper → ApiError{code,message,fields,exceptionKind,status}
shared/hooks/useIdempotentMutation ─ one key per intent ─ useCommit (pending/toast contract)
shared/hooks/useConfirmException ─ 409 exception_required → ExceptionConfirmDialog → retry
```

HTTP client contract (architecture §3.6):

| Concern | Behaviour |
|---|---|
| Base | `/api/v1` same-origin on the current host (never an absolute API URL), `credentials: 'include'`, JSON; cross-host links ("Đăng nhập" after sign-up, admin → store link) use URLs returned by the API, not string building in the SPA |
| CSRF | read token from `GET /api/v1/session` (`csrf_token`) into memory; send `X-CSRF-Token` on non-GET |
| Idempotency | `Idempotency-Key: crypto.randomUUID()` created when the intent starts (dialog open), reused on every retry of that intent |
| Concurrency | adapters keep `etag`; mutations send `If-Match`; `412` → toast "Dữ liệu đã thay đổi ở nơi khác. Tải lại để tiếp tục." (new label) + refetch |
| Errors | parse `{"error":{code,message,fields,exception_kind}}`; `fields` map onto react-hook-form `setError`; `409` with `exception_kind` → `useConfirmException`; `401 session_expired` → `/login?next=`; `401 reauth_required` → re-auth dialog; `402/403 entitlement_*` → read-only banner |
| Money/qty | decimal strings only; `shared/lib/decimal.ts` (decimal.js-light) and `Money`/`Qty` value types; never `Number()` on money |
| Types | `pnpm gen:api` → `src/shared/api/schema.d.ts`; CI fails if regenerated file differs |

Exception confirmation (SEC-03, PRC-04, DEBT-06): `ExceptionConfirmDialog` renders the prototype `soConfirm` shape — title "Xác nhận ngoại lệ cho {doc_no}", sub "Hóa đơn vượt kiểm soát. Bạn là người quyết định; lựa chọn được ghi vào nhật ký.", warn callout listing `<b>{type}:</b> {detail}` per issue, checkbox "Tôi xác nhận vẫn bán", submit "Xác nhận bán"; unchecked submit shows "Tích “Tôi xác nhận vẫn bán” để tiếp tục.". Confirming calls `POST /api/v1/exception-confirmations` and retries the original request (same `Idempotency-Key`) with `exception_confirmation_id`. Title/sub/checkbox text are props so phases 15–18 reuse it for other kinds (tiêu hủy, chênh lệch kiểm kê).

## Route → phase coverage (40 MENU routes)

| # | Hash (prototype) | Path | View line | Phase |
|---|---|---|---|---|
| 1 | tong-quan.dashboard | /overview/dashboard | 2528 | 18 |
| 2 | tong-quan.thong-bao | /overview/notifications | 2110 | 18 |
| 3 | ban-hang.pos | /sales/pos | 3834 | 16 (+19 offline) |
| 4 | ban-hang.bao-gia | /sales/quotes | 2444 | 16 |
| 5 | ban-hang.don-ban | /sales/invoices | 2323 | 16 |
| 6 | ban-hang.tra-hang | /sales/returns | 4058 | 16 |
| 7 | mua-hang.po | /purchasing/orders | 2701 | 15 |
| 8 | mua-hang.hang-hoa | /purchasing/products | 2733 | 15 |
| 9 | mua-hang.nhan-hang | /purchasing/receipts | 2756 | 15 |
| 10 | mua-hang.tra-ncc | /purchasing/supplier-returns | 2774 | 15 |
| 11 | kho.ton | /inventory/stock | 3910 | 17 |
| 12 | kho.giu-hang | /inventory/reservations | 2804 | 17 |
| 13 | kho.kiem-ke | /inventory/counts | 2816 | 17 |
| 14 | kho.lo-han | /inventory/lots | 2830 | 17 |
| 15 | kho.dieu-chinh | /inventory/adjustments | 2840 | 17 |
| 16 | kho.the-kho | /inventory/stock-card | 3671 | 17 |
| 17 | gio-qua.cong-thuc | /gift-baskets/recipes | 2845 | 17 |
| 18 | gio-qua.lenh-dong | /gift-baskets/assembly-orders | 2975 | 17 |
| 19 | gio-qua.tien-do | /gift-baskets/progress | 2989 | 17 |
| 20 | gio-qua.thao-gio | /gift-baskets/disassembly | 2997 | 17 |
| 21 | cong-no.khach | /debts/receivables | 3139 | 18 |
| 22 | cong-no.ncc | /debts/payables | 3141 | 18 |
| 23 | cong-no.ung-truoc | /debts/advances | 3170 | 18 |
| 24 | cong-no.doi-soat | /debts/statements | 3179 | 18 |
| 25 | cong-no.lich | /debts/schedule | 3192 | 18 |
| 26 | chien-dich.mua | /campaigns/seasons | 3246 | 18 |
| 27 | chien-dich.khach-hang | /campaigns/customers | 3253 | 18 |
| 28 | bao-cao.nxt | /reports/stock-movement | 3264 | 18 |
| 29 | bao-cao.doanh-thu | /reports/sales | 3273 | 18 |
| 30 | bao-cao.lai-gop | /reports/gross-profit | 3288 | 18 |
| 31 | bao-cao.tuoi-no | /reports/debt-aging | 3301 | 18 |
| 32 | bao-cao.dong-tien | /reports/cash-flow | 3310 | 18 |
| 33 | bao-cao.gio | /reports/gift-baskets | 3321 | 18 |
| 34 | danh-muc.hang-hoa | /catalog/products | 3337 | 15 |
| 35 | danh-muc.nha-cung-cap | /catalog/suppliers | 3425 | 15 |
| 36 | danh-muc.bang-gia | /catalog/price-lists | 3353 | 15 |
| 37 | danh-muc.nhom-mix | /catalog/mix-groups | 3574 | 15 |
| 38 | thiet-lap.tai-khoan | /settings/account | 3358 | 18 |
| 39 | thiet-lap.thue-bao | /settings/subscription | 3379 | 18 |
| 40 | thiet-lap.nhat-ky | /settings/audit-log | 3385 | 18 |

Totals: phase 15 = 8, phase 16 = 4, phase 17 = 10, phase 18 = 18 → 40. Non-menu routes: auth pages and `/onboarding` shell (14), onboarding step bodies and `/admin/*` (18), `/print/sales-documents/:id` (16).

Redirects (`MOVED` + legacy hashes): `/settings/suppliers` & `#thiet-lap.nha-cung-cap` → `/catalog/suppliers`; `#danh-muc.doi-tac` → `/campaigns/customers`; `#thiet-lap.tich-hop`, `#thiet-lap.du-lieu` → `/settings/account`; `#kho.mau` → `/inventory/stock`; `#tien.quy|thu-chi|chuyen-tien|sao-ke|ca|chi-phi` → `/reports/cash-flow`. Group paths `/overview`, `/sales`, `/purchasing`, `/inventory`, `/gift-baskets`, `/debts`, `/campaigns`, `/reports`, `/catalog`, `/settings` → first sub; unknown sub → first sub of its module; `/` → `/overview/dashboard`. Dead views (§9) get no route.

## Related files

Create (all under `apps/web/`):

- `src/shared/styles/tokens.css`, `src/shared/styles/globals.css`, `src/shared/styles/theme.css` (Tailwind 4 `@theme` mapping)
- `src/app/router.tsx`, `src/app/menu.ts` (MENU: group label, path, sub label, h1, view id, owning feature), `src/app/legacy-redirects.ts`
- `src/app/layouts/app-shell.tsx`, `sidebar.tsx`, `util-bar.tsx`, `auth-layout.tsx`, `admin-shell.tsx`, `onboarding-layout.tsx`
- `src/app/guards/require-auth.tsx`, `require-entitlement.tsx`, `require-recent-auth.tsx`, `require-admin-auth.tsx`, `onboarding-gate.tsx`
- `src/app/providers/query-provider.tsx`, `session-provider.tsx`, `tenant-provider.tsx`, `hotkeys-provider.tsx`
- `src/app/route-fallbacks.tsx` ("Đang xây dựng.", error boundary callout "Không hiển thị được màn hình: {message}")
- `src/shared/api/http.ts`, `api-error.ts`, `csrf.ts`, `schema.d.ts` (generated)
- `src/shared/hooks/use-idempotent-mutation.ts`, `use-commit.ts`, `use-confirm-exception.ts`, `use-hotkeys.ts`, `use-debounce.ts`, `use-cursor-list.ts`, `use-one-store.ts`
- `src/shared/lib/format.ts` (`vnd`, `money`, `num`, `fd`, `fdS`, axis `tr`/`k`, `qMix`), `decimal.ts`, `dates.ts` (Asia/Saigon business date), `textnorm.ts` (accent-insensitive)
- `src/shared/domain/money.ts`, `qty.ts`, `doc-no.ts` (prefix → drawer registry), `color.ts` (7 colors + none), `status-tone.ts` (single typed map, dedupes "Chưa thanh toán", gap #3)
- `src/shared/ui/` — `button.tsx`, `link-button.tsx`, `card.tsx`, `grid.tsx`, `band.tsx`, `kpi-card.tsx`, `page-header.tsx`, `tabs.tsx`, `data-table/` (`data-table.tsx`, `expandable-row.tsx`, `cell-sub.tsx`, `table-caption.tsx`), `filter-bar.tsx`, `toggle-group.tsx`, `status-pill.tsx`, `badge.tsx`, `form-grid.tsx`, `field.tsx`, `input.tsx`, `money-input.tsx`, `qty-input.tsx`, `checkbox.tsx`, `callout.tsx`, `impact-summary.tsx`, `key-value-grid.tsx`, `progress.tsx`, `stepper.tsx`, `timeline.tsx`, `todo-list.tsx`, `matrix-table.tsx`, `period-bar.tsx`, `delta-indicator.tsx`, `category-list.tsx`, `charts/bar-chart.tsx`, `charts/grouped-bar-chart.tsx`, `color/color-swatch-group.tsx`, `color/color-chip.tsx`, `color/color-qty-pill.tsx`, `color/stock-color-picker.tsx`, `combobox.tsx`, `date-picker.tsx` (dd/mm/yyyy, gap #13), `commit-dialog.tsx`, `detail-drawer.tsx`, `exception-confirm-dialog.tsx`, `reauth-dialog.tsx`, `toaster.tsx`, `doc-no.tsx`, `read-only-banner.tsx`
- `src/app/host-context.ts` — fetch once before router creation, `routesFor(kind)`
- `src/features/auth/` — `pages/login-page.tsx`, `signup-page.tsx` (with `components/slug-field.tsx`), `find-store-page.tsx`, `verify-page.tsx`, `forgot-password-page.tsx`, `reset-password-page.tsx`, `mfa-page.tsx`, `api/queries.ts`, `api/mutations.ts`, `schemas.ts`, `index.ts`
- `src/features/onboarding/pages/onboarding-page.tsx` (shell), `components/onboarding-checklist.tsx`
- `src/features/<f>/index.ts` stubs + `pages/<route>-page.tsx` placeholders for all 40 routes
- `src/test/msw/handlers.ts`, `src/test/render.tsx`, `src/test/setup.ts`
- `e2e/visual/prototype-harness.ts` (opens prototype at `#mod.sub` with seed), `e2e/visual/parity.spec.ts` skeleton, `e2e/shell.spec.ts`, `e2e/auth.spec.ts`
- `eslint/no-raw-color.js` local rule; `.browserslistrc`

Modify: `apps/web/package.json` (deps, `gen:api`, `test`, `typecheck`, `lint`, `e2e` scripts), `apps/web/vite.config.ts`, `apps/web/tsconfig.json`, `apps/web/eslint.config.js`, `apps/web/playwright.config.ts`, `apps/web/index.html` (Google Fonts Inter Tight 400/500/600/700, Manrope 600/700, Roboto 400/700, `subset=vietnamese`, `lang="vi"`).

## Implementation steps

1. **Tooling.** Pin React 19, react-router 7, TanStack Query 5, radix/shadcn, react-hook-form + zod, sonner, lucide, Tailwind 4, Recharts, decimal.js-light, vitest + RTL + msw, Playwright, `@axe-core/playwright`, openapi-typescript. Add `gen:api` reading `apps/api/api/openapi.yaml`.
2. **Tokens.** Port every `:root` value from UI spec §1 into `tokens.css` (navy-900 `#0a0d53`, navy-700 `#233c65`, navy-deep `#001a2d`, accent-orange `#f45d29`, accent-crimson `#f2295b`, on-accent `#eaeaea`, slate-600, gray-800/600/500/300/200, surface-0/50/100, blue-50/100, surface-dark, series-1/2, row hover `#fafbfd`, status ink/bg/line triplets, product colors incl. `trang` border and hatch, shadows, `--grad-accent`, `--sp-1..7`, radii, fonts). Map into Tailwind `@theme`. Breakpoints 1100/860/520; `prefers-reduced-motion` kills transitions; `:focus-visible` rule.
3. **Lint guard.** Local ESLint rule rejects hex/rgb literals and arbitrary color classes in `src/features/**`; tokens only.
4. **HTTP client + errors** per the contract table; unit-test CSRF header, idempotency reuse on retry, `If-Match`, error parsing, 412/401/409 branches.
5. **Commit pipeline.** `useIdempotentMutation(intent)` holds the key for the life of a dialog; `useCommit()` returns `{ run, pending }`, sets button label "Đang ghi sổ…", disables it, toasts the server-built success (doc numbers bold), keeps error toasts until dismissed (gap #6), invalidates the query-key factories passed in, closes or keeps the dialog per `after`.
6. **Shared UI kit** with exact specs from UI spec §3 (button variants and sizes, card, KPI incl. `.forecast` dashed blue-50, DataTable th/td/tfoot/empty "Chưa có dữ liệu.", `.flat`, visually hidden `<caption>` from section title (gap #12), `numeric` cells tabular-nums, `hideWhenOneStore`; ExpandableRow 24×24 visual toggle inside 44px hit area (gap #18) with aria-label "Xem chi tiết màu của X" / "Thu gọn màu của X"; StatusPill glyph `aria-hidden` (gap #8); ImpactSummary + paybox; charts always followed by `<details>` "Xem dạng bảng" and focusable bars (gap #7)).
7. **Color controls.** `ColorSwatchGroup` on Radix RadioGroup (arrow-key roving, gap #10), 22×22 swatches, double ring on checked, fallback "Chưa chọn"; `StockColorPicker` label "Màu khách lấy", per-option name + available qty, disabled at 0, hint "Chọn màu" / "Màu này đã hết", auto-select when one color left.
8. **Combobox.** Command + Popover, fixed popup z 70, max-height 320, min-width 300, flip near bottom; accent-insensitive search via `textnorm`, server search for large pools (`?q=` debounced 150ms) with client cap 50; hint "Gõ thêm để thu hẹp kết quả…"; empty "Không tìm thấy “x”."; ↑/↓/Enter/Esc; Tab auto-picks single match; product options show color dots; placeholders "Gõ tên hoặc số điện thoại để tìm…" (party) / "Gõ mã, tên hoặc mã vạch để tìm…" (product). Pools: `sell`, `buy`, `stock`, `kit`, `party` → query adapters registered by features.
9. **CommitDialog / DetailDrawer.** Radix Dialog width 760 / wide 1040, scrim `rgba(0,26,45,.45)`, header h3 Manrope 22 + "×", footer "Hủy" (line), extra, submit default "Ghi sổ"; `live` region renders from `watch()` without remounting fields (gap #15). Drawer = Sheet right `min(820px,100%)`, scrim .25, sticky header, preserved scroll; focus trap + restore (gap #5). Esc order: modal → drawer → mobile sidebar.
10. **Shell.** Grid `264px 1fr`; sidebar navy-700 sticky with brand "Tết Sales" + tagline "Bán sỉ · giỏ quà · kho · công nợ", accordion groups (Roboto 13/700 uppercase), sub links `aria-current="page"`; ≤860 off-canvas 280px with "☰ Menu". Util bar left→right: "☰ Menu", sandbox pill (ORG-06 only), tenant display name (`.hide-s`), "Ngày nghiệp vụ dd/mm/yyyy" from `GET /api/v1/tenant` business date, spacer, "Chủ cửa hàng · {full_name}", "Việc cần xử lý **n**" (count from `GET /api/v1/notifications/summary`, links `/overview/notifications`), "Đặt lại mẫu" (sandbox only). Sidebar footer shows "Bản mẫu tương tác theo PRD 1.0. Dữ liệu demo chỉ lưu trên trình duyệt này." only in sandbox tenants; production tenants show nothing. Branch select never renders (ORG-03). Main padding 26px 28px 60px, max-width 1480.
11. **Router.** `menu.ts` is the single source of the 40 routes (coverage table above); route `handle` carries eyebrow/title/view id for `PageHeader` and visual tests; lazy feature pages; group redirects; legacy hash shim on first load; route change closes drawer/modal and scrolls to top; error boundary fallback.
12. **Hotkeys.** `useHotkeys` registry scoped to route; POS reserves F2/F4/F9 (bound in phase 16); `Esc` chain global.
13. **Guards.** `RequireAuth` (session query; redirect `/login?next=`), `OnboardingGate` (incomplete required steps → `/onboarding`, skippable steps do not block), `RequireEntitlement(module)` (upgrade explanation + keep read access), `RequireRecentAuth` (wraps sensitive mutations, opens `ReauthDialog`), `RequireAdminAuth` (separate admin session + TOTP).
14. **Auth screens** on `AuthLayout` (card on surface-100, brand header): login on the tenant host (email/phone + password, lockout message from API, store name from `/host-context` shown in the header only after login so unknown slugs look the same as real ones), sign-up on the root host (owner name, store name, "Địa chỉ cửa hàng" = slug input with fixed `.DOMAIN` suffix, prefilled from the store name suggestion, debounced `GET /signup/slug-check` showing free / invalid / reserved / taken + suggestion, contact, password; success screen tells the owner to open the mail and shows `https://<slug>.DOMAIN`), "Tìm cửa hàng của bạn" on the root host (email → neutral confirmation whatever the result), verify code, forgot/reset password, MFA TOTP challenge. Labels are new (prototype has none); keep tone and component styles of the prototype; zod schemas mirror API field errors.
15. **Onboarding shell.** `/onboarding` renders `Stepper` with the seven `onboarding_steps` keys and an `OnboardingChecklist` card reused on the dashboard; step bodies are registered by phase 18.
16. **Test harness.** MSW handlers per feature, render helper with providers; Playwright projects (chromium, firefox, webkit, mobile-390); `prototype-harness.ts` loads `docs/design/prototype-v6.14.html` with seed date 2026-10-12 and navigates by hash; `parity.spec.ts` skeleton compares shell only in this phase.

## Todo

- [x] Dependencies, scripts, browserslist, Playwright projects
- [x] `tokens.css` + Tailwind `@theme` + fonts + reduced motion + focus-visible
- [x] No-raw-color lint rule wired into `pnpm -C apps/web lint`
- [x] `http.ts`, `ApiError`, CSRF, idempotency, If-Match, 401/409/412 branches
- [x] `useIdempotentMutation`, `useCommit`, `useConfirmException`, `ExceptionConfirmDialog`, `ReauthDialog`
- [x] Formatters (`vnd` with NBSP ₫, `fd` dd/mm/yyyy, `qMix`), decimal, Asia/Saigon dates, textnorm
- [x] Status tone map (typed, deduped)
- [x] Shared UI kit (§3 catalogue) incl. DataTable caption + oneStore columns
- [x] Color controls on Radix RadioGroup
- [x] Combobox with all five pools' adapter interface
- [x] CommitDialog, DetailDrawer, Toaster (error toasts persistent)
- [x] AppShell, Sidebar accordion, UtilBar, AuthLayout, AdminShell, OnboardingLayout
- [x] Router: 40 routes, group redirects, MOVED + legacy hash redirects, fallbacks
- [x] Guards: RequireAuth, OnboardingGate, RequireEntitlement, RequireRecentAuth, RequireAdminAuth
- [x] Auth pages + onboarding shell — onboarding shell (`/onboarding` Stepper + `OnboardingChecklist`); auth screens in `src/features/auth/pages/` on the real routes: tenant `/login`, `/mfa` (TOTP + recovery code), `/verify` (explicit button; asks for a first password when the account has none), `/forgot-password`, `/reset-password`; root `/signup` (slug follows the store name until edited, live `slug-check` with free/invalid/reserved/taken + suggestion, success shows the store URL) and `/find-store` (neutral answer); admin `/admin/login` (password then TOTP). Logout control added to the util bar. Login takes email only (the API has no phone sign-in). Admin sign-in uses an assumed contract (UI + MSW only): the admin auth API and `/api/admin/v1/me` do not exist yet. Evidence: unit tests per page in `src/features/auth/**` (`pnpm test` 214/214).
- [x] MSW harness, prototype visual harness, shell/auth e2e — `authHandlers` in `src/test/msw/handlers.ts`; `e2e/auth.spec.ts` against the running API + mailpit (login, wrong password, `next`, logout, session ended on the server → `/login?next=`, client validation, `/mfa` without challenge, `/admin` + `/signup` 404 on a store host, root-host 404 for `/login`, sign-up with taken/reserved/invalid/free slug, verify from the mailed link then sign-in on the new store host, forgot/reset with a single-use link, TOTP turned on via the API then the second sign-in step); `auth.setup.ts` saves the owner session and `shell.spec.ts` also walks every screen with the real API. Run with `make dev` and `E2E_OWNER_PASSWORD` set: chromium, firefox, mobile-390, root-chromium, visual all green (44 passed). webkit needs host libraries (`sudo pnpm exec playwright install-deps`); admin-chromium has no journey until the admin auth API exists. Each root run leaves an `e2e-*` store in the dev database.

## Success criteria & verification

- `pnpm -C apps/web typecheck`, `pnpm -C apps/web lint`, `pnpm -C apps/web test` pass; `pnpm gen:api` leaves no diff.
- Unit tests: `http.test.ts` (CSRF, key reuse, If-Match, error parsing), `use-commit.test.tsx` (pending label, no success before 2xx, error toast persists), `combobox.test.tsx` (keys, Tab auto-pick, cap 50, accent-insensitive "banh" finds "Bánh"), `color-swatch-group.test.tsx` (arrow roving, no text input), `router.test.tsx` (all 40 paths resolve to a page; every MOVED and group redirect lands on its target), `format.test.ts` (`1.250.000 ₫`, `12/10/2026`, "1 thùng 3 dây 2 hộp").
- Playwright runs tenant journeys against `http://<seeded-slug>.localhost:5173`, sign-up/find-store against `http://localhost:5173` and admin against `http://admin.localhost:5173` (`playwright.config.ts` projects set `baseURL` per host kind). `shell.spec.ts`: sign in, open every route from the sidebar, `aria-current` correct, Esc chain, mobile menu at 390. `auth.spec.ts`: login, wrong password, MFA, logout, session expiry redirect with `next`; sign-up with slug check (taken/reserved/invalid) then verify link lands on the new subdomain; `/admin` on a tenant host and `/login` on the root host render 404.
- Visual: `parity.spec.ts` shell regions (sidebar, util bar, page header) at 1440 and 390 vs prototype `#tong-quan.dashboard` with content area masked; threshold per phase 20.
- Token test asserts every `:root` variable of the prototype exists in `tokens.css` with the same value.

## Risks

- **Tailwind 4 / shadcn defaults leak non-token styles** → shadcn components are restyled once in `shared/ui`; lint blocks raw colors in features.
- **Retry with same `Idempotency-Key` after 409** requires the API not to store 409 as the replay body → agree in phase 02 that only 2xx/terminal 4xx are stored; contract test there.
- **Combobox on large catalogs** → server-side search with cursor, client cap 50; measure under NFR-03 in phase 21.
- **Fonts with Vietnamese subset** add latency → `preconnect`, `font-display: swap`, subset only.

## Security

- Session cookie `__Host-tally_session` is HttpOnly and host-only, so each store's host has its own session; the SPA never reads it. CSRF token lives in memory only, refreshed from `/session`.
- No secrets, tokens or credentials in `localStorage`; React Query cache is memory-only (offline cache rules are phase 19's).
- Admin console uses a separate cookie and route group; `AdminShell` never shares query cache with tenant routes (separate `QueryClient`).
- Error boundary shows the message, never stack traces or request payloads.
- CSP-friendly build: no inline scripts; fonts from Google Fonts only.

## Rollback

Pure frontend; revert the web build image to the previous tag (`apps/web` Dockerfile → nginx). No data migrations. Feature pages land behind the router, so a broken page can be reverted to its placeholder without touching the shell.

## Next

Phases 15–18 implement the route pages listed in the coverage table, importing only from `shared/*` and other features' `index.ts`. Phase 19 extends POS with offline. Phase 20 enforces visual and accessibility thresholds on everything built here.
