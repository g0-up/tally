---
title: "Phase 18: Web overview, debt, campaigns, reports, settings, onboarding & admin"
status: in-progress
priority: P1
effort: 10d
dependsOn: [phase-14-web-foundation-design-system, phase-10-debt-cash, phase-12-campaign-crm-notifications-reporting, phase-13-saas-control-settings]
---

# Phase 18 — Web overview, debt, campaigns, reports, settings, onboarding & admin

## Context links

- UI spec §5.1 Tổng quan, §5.6 Công nợ, §5.7 Chiến dịch & khách, §5.8 Báo cáo, §5.10 Thiết lập, §5.11 global modal, §8 gaps #14 (CSV textarea), #17 (toasts before commit)
- Prototype views 2528 (dashboard, `payModal`), 2110, 3139/3141 (`debtTable`, `openParty`, `debtNote`, `writeOff`), 3170 (`allocAdv`), 3179 (`newSTM`, `drawSTM`, `stmDispute`), 3192, 3246, 3253, 3264–3321 (`repFilter`), 3358, 3379, 3385, 1887
- Schema: `design/database-schema-debt-cash-crm-saas.md` (payments, allocations, advances, statements, write-offs, contact notes, campaigns, notifications, export_jobs, saved_reports, onboarding_steps, tenant_settings, subscriptions, support grants, admin tables)
- PRD DEBT-01..13, CASH-01..05, SEA-01..03, CRM-01..03, CAT-06, NTF-01, RPT-01..11, SAAS-01..10, ORG-02/05/07, SEC-04..07

## Overview

Builds the 18 remaining menu routes plus the non-menu surfaces: onboarding step bodies and the separate `/admin/*` operator console served only on `admin.DOMAIN` (architecture §7.1). Debt screens cover receivables/payables, party drawer, receipts/payments with allocation, advances, statements with versions and disputes, the collection schedule and write-offs under re-auth. Reports share one filter and export through async `export_jobs` downloads instead of the prototype textarea. Settings keep the three prototype screens and add the owner controls the PRD requires but the prototype lacks; those additions use new labels and are flagged.

## Key insights

- All aggregates (aging buckets, KPIs, forecast, report rows) are server-computed and money-exact; the client only formats. Report periods use the tenant business date in Asia/Saigon (default from 01/10 of the season to today, as prototype `repFilter`).
- payModal allocation auto-fills oldest-first on the client as a convenience, but the server re-validates; the blocking callouts "Tổng phân bổ vượt số tiền." and "Có dòng phân bổ vượt số còn nợ." mirror server errors.
- Write-off is an owner decision, not cash: requires recent auth (`RequireRecentAuth`, SEC-06) and never touches revenue (writeOff sub verbatim).
- Approvals were replaced by owner exception confirmations (SEC-03). The prototype notification chip "Chờ duyệt" is an approval remnant and is not built; notification kinds come from NTF-01 (overdue, upcoming AP, near expiry, expired-blocked, below minimum, POS sync exceptions from phase 19).
- Schedule "Chiều" value "Thu hộ về" is a COD remnant of the removed Tiền module and is not built; only Thu / Trả.
- CSV export: `POST /export-jobs` (kind `report_csv`) → poll `GET /export-jobs/{id}` → signed download link; status labels for queued/running/succeeded/failed/expired (gap #14).
- Admin console is a separate SPA area with its own session and TOTP (`RequireAdminAuth`, phase 14) and never shares tenant query caches.

## Requirements

| PRD ID | Behaviour |
|---|---|
| CASH-01, CASH-05, DEBT-12 | `/overview/dashboard` period bar, Thu – chi band, by-type lists, Công nợ band + forecast, top debtors/creditors with "Thu tiền"/"Trả tiền" |
| NTF-01 | `/overview/notifications` chips by kind with counts, "Mở" deep link, empty "Không có việc cần xử lý.", util badge count |
| DEBT-01..04, DEBT-07 | `/debts/receivables` + `/debts/payables` aging table, party drawer, payModal with allocation |
| DEBT-08 | Contact note "Ghi chú nhắc nợ" → debtNote "Lưu ghi chú" |
| DEBT-09, SEC-06 | writeOff "Đề nghị giảm/xóa nợ {id}" with re-auth |
| DEBT-05, DEBT-10 | `/debts/advances` + allocAdv "Phân bổ {id}" |
| DEBT-11 | `/debts/statements` newSTM, versions, send/confirm/dispute |
| DEBT-12 | `/debts/schedule` forecast KPIs and due list |
| SEA-01..03 | `/campaigns/seasons` cards, checklist, demand table "Nhu cầu hàng mùa Tết theo ngày" |
| CRM-01..03, CAT-06 | `/campaigns/customers` CRM list, "Tái đặt đơn" / "Tạo đơn", customer create/edit dialog, marketing consent |
| RPT-01..11 | Six report routes with `ReportFilter`, saved views (`saved_reports`), async CSV export |
| ORG-02, SEC-04, SEC-07 | `/settings/account` owner card, devices with "Đăng xuất", 2FA setup, store profile card (shows the read-only store address `https://<slug>.DOMAIN` with a copy button; new label, slug cannot be changed in 1.1) |
| SAAS-02, SAAS-03, SAAS-05 | `/settings/subscription` plans, current subscription, support access grant/revoke, support tickets |
| SEC-05 | `/settings/audit-log` search, 200-row pages, period lock card on account page |
| ORG-07, SAAS-08 | Ownership transfer, full data export (`export_jobs` kind `full_data_export`), account closure |
| SAAS-04 | Onboarding step bodies for the seven `onboarding_steps` |
| SAAS-06, SAAS-07, SAAS-09, SAAS-10 | `/admin/*` tenants, usage, billing, incidents, feature flags, support, partners |
| ORG-05 | Sandbox reset modal "Đặt lại dữ liệu mẫu?" (sandbox tenants only) |

## Architecture

```
features/overview     dashboard, notifications (summary query feeds util badge)
features/debt         receivables/payables, party drawer, PayDialog, allocation, advances, statements, schedule,
                      write-off, contact notes — exports PayDialog, PartyDrawer, useOpenParty
features/campaigns    seasons, demand, CRM list, CustomerDialog
features/reports      ReportFilter, six report pages, SavedReportsMenu, useExportJob
features/settings     account, subscription, audit log, store profile, period lock, ownership transfer,
                      data export/closure, offline policy slot (phase 19 mounts its card)
features/onboarding   step bodies registered into the phase 14 stepper
features/admin        AdminShell pages, admin api client (separate base path and session)
```

API (phase 10/12/13 OpenAPI authoritative):

| Area | Requests |
|---|---|
| Dashboard | `GET /dashboard?period=day|7d|month|year&date=` |
| Dashboard | `GET /reports/dashboard?period=&date=` |
| Notifications | `GET /notifications?state=` · `GET /notifications/unread-count` · `POST /notifications/{id}:read|resolve|snooze` |
| Debt | `GET /debts/parties?direction=&overdue=` · `GET /debts/parties/{partyId}` (documents, schedules, advances) · `POST /payments` (allocations[]) · `POST /payments/{id}:confirm|reverse` · `GET /payments/pending-verification` · `POST /allocations:suggest` · `POST /allocations` · `GET/POST /parties/{id}/collection-notes` · `POST /parties/{id}/reminders` · `POST /debt-adjustments` (write-off under re-auth) · `POST /debt-offsets` (re-auth) · `POST /refunds` |
| Statements | `GET/POST /statements` · `POST /statements/{id}:generate|send|confirm|dispute` · `GET /statements/{id}/versions/{n}` |
| Schedule / aging | `GET /debts/forecast?from=&to=` · `GET /debts/aging?direction=&as_of=` |
| Campaigns | `GET/POST /campaigns` · `GET/PATCH /campaigns/{id}` · `POST /campaigns/{id}:activate|close` · `GET/PATCH /campaigns/{id}/checklist` · `GET /campaigns/{id}/demand?from=&to=` · `POST /campaign-attributions` |
| CRM | `GET/POST /customers`, `GET/PATCH /customers/{id}` (phase 05) · `GET /customers/{id}/timeline` · `GET/POST /customers/{id}/consents` · `GET/POST /care-tasks`, `PATCH /care-tasks/{id}` · `GET /customers/last-season?campaign_id=` · `POST /customers/{id}/reorder-drafts` · `GET /customers/{id}/loyalty` |
| Reports | `GET /reports/{reportKey}?from=&to=&group_by=` · `GET /reports/suggestions/{buy|clearance}` · `GET /reports/cash-forecast` · `GET/POST /saved-reports`, `PATCH/DELETE /saved-reports/{id}` · `POST /export-jobs` · `GET /export-jobs/{id}` · `POST /export-jobs/{id}:cancel` · `GET /export-jobs/{id}/download` · `POST /analytics/questions` (only when enabled) |
| Settings | `GET/PATCH /tenant`, `GET/PATCH /tenant/settings` · `GET /sessions`, `DELETE /sessions/{id}` · `POST /auth/mfa/totp`, `POST /auth/mfa/totp/verify`, `DELETE /auth/mfa/totp` · `GET/POST /period-locks`, `POST /period-locks/reopen` · `POST /owner-transfer` · `GET /audit-events?q=&cursor=` (phase 04) · `GET /subscription`, `GET /plans`, `POST /subscription:change`, `GET /saas-invoices` · `GET/POST /support-sessions`, `POST /support-sessions/{id}:revoke` · `GET/POST /support-tickets`, `POST /support-tickets/{id}/messages` · `POST /data-exports` · `GET/POST /account-closure`, `POST /account-closure:cancel` · `POST /feedback` (phase 13) |
| Onboarding | `GET /onboarding` · `POST /onboarding/steps/{stepKey}:complete|skip` (phase 13) · opening stock/debt/cash endpoints from phases 07/10; no file import (PRD 1.1 removed DATA-01..03, OPEN-14 = manual wizard) |
| Admin | `/api/admin/v1/*` tenants, usage, invoices, incidents, flags, support, partners |

## Screens

### `/overview/dashboard` (2528)

- Eyebrow "Tổng quan", h1 "Thu, chi và công nợ"; acts "Thu tiền khách", "Trả tiền NCC" (PayDialog with no preselected party — the prototype hardcodes KH002/NCC001).
- Period bar chips Ngày / 7 ngày / Tháng / Năm; date label "Ngày" / "Đến ngày" / "Trong tháng có ngày" / "Trong năm có ngày"; "‹ Kỳ trước", "Kỳ sau ›", "Về hôm nay". Period in URL query.
- Band "Thu – chi": KPIs Tổng thu, Tổng chi, Chênh lệch thu – chi, Tiền trong quỹ (link `/reports/cash-flow`). Day period: Cols Chứng từ / Loại / Đối tượng / Quỹ / Thu / Chi; other periods: grouped bars Thu vs Chi + `<details>` "Xem dạng bảng". Card "Theo loại" with two lists.
- Band "Công nợ": Khách còn nợ, Nợ khách trong kỳ, Cửa hàng còn nợ NCC, Nợ NCC trong kỳ; forecast "Sắp phải thu (7 ngày tới)", "Sắp phải trả (7 ngày tới)". Tables "Khách nợ nhiều nhất" ("Thu tiền") and "Nợ nhà cung cấp" ("Trả tiền").
- `OnboardingChecklist` card (phase 14) shown while required steps remain.

### `/overview/notifications` (2110)

- Chips with counts: Nợ quá hạn, Sắp phải trả NCC, Sắp hết hạn (≤30d), Hết hạn – chặn bán, Dưới tồn tối thiểu, plus "Lỗi đồng bộ POS" when phase 19 kind exists (new label). Cols Loại / Nội dung / "Mở" (deep link to screen or drawer via `?open=DOC-NO`). Empty "Không có việc cần xử lý.".

### `/debts/receivables` and `/debts/payables` (3139/3141, one view `side`)

- Chips "Có số dư" / "Tất cả khách" (AP "Tất cả NCC"). Cols name (opens party drawer) / Khách còn nợ (AP "Cửa hàng còn nợ NCC") / Trong hạn / Quá 1–30 / Quá 31–60 / Quá > 60 / Tiền khách ứng trước / acts "Thu tiền" + "Bảng kê" (AP "Trả tiền"); footer totals.
- Party drawer: acts "Thu tiền" / "Lập bảng kê" / "Ghi chú nhắc nợ" (AP "Trả tiền"); impact incl. Hạn mức còn lại; callout when party is both customer and supplier; "Chứng từ còn mở" Cols Chứng từ (+tag "đầu kỳ") / Ngày / Hạn (+ "Quá hạn N ngày") / Phát sinh / Còn lại with row act "Giảm/xóa nợ"; "Thanh toán"; "Ghi chú liên hệ" timeline.
- PayDialog (wide): "Thu tiền khách" / "Trả tiền nhà cung cấp"; party Combobox, Số tiền, "Vào quỹ" / "Chi từ quỹ", Nội dung; allocation Cols Chứng từ / Hạn / Còn nợ / Phân bổ (oldest first); blocking callouts above; submit "Ghi phiếu thu" / "Ghi phiếu chi"; toast after 2xx with PT-/PC- number.
- debtNote: submit "Lưu ghi chú". writeOff: title "Đề nghị giảm/xóa nợ {id}", sub "Giảm/xóa nợ là quyết định của chủ cửa hàng, ghi nhật ký kèm lý do; không phải tiền thu và không làm tăng doanh thu.", fields "Số tiền", "Lý do"; submit "Ghi giảm nợ"; toast "Đã ghi giảm nợ.".

### `/debts/advances` (3170)

- Cols Phiếu / Đối tác / Chiều ("Khách ứng trước" / "Cửa hàng ứng trước NCC") / Loại / Số tiền / Chưa phân bổ / "Phân bổ"; empty "Không có khoản ứng trước chưa phân bổ.".
- allocAdv: title "Phân bổ {id}", sub "Còn {vnd} chưa phân bổ của {name}."; Cols Chứng từ / Còn nợ / Phân bổ; errors "Phân bổ vượt số tiền còn lại." / "Phân bổ vượt dư nợ {obl}"; submit "Ghi phân bổ"; toast "Đã phân bổ.".

### `/debts/statements` (3179)

- Acts "Lập bảng kê". Cols Bảng kê (sub vN) / Khách / Kỳ / Đầu kỳ / Cuối kỳ / Trạng thái / acts "Gửi khách" (Nháp), "Khách xác nhận" + "Tranh chấp" (Đã gửi).
- newSTM: Khách hàng / Từ ngày / Đến ngày; submit "Lập bảng kê". Drawer: running-balance table, "Lịch sử phiên bản", "In phiếu" (print stylesheet shared with phase 16).
- stmDispute: title "Ghi nhận tranh chấp {id}", "Nội dung khách phản hồi", submit "Ghi nhận", toast "Đã ghi nhận tranh chấp. Lập phiên bản mới sau khi điều chỉnh.".

### `/debts/schedule` (3192)

- KPIs Dự kiến thu 7 ngày / Dự kiến trả 7 ngày / Tiền đang có. Cols Hạn / Chiều (Thu / Trả) / Đối tác / Chứng từ / Số tiền / Cam kết.

### `/campaigns/seasons` (3246)

- `.g2` campaign cards with meter + impact and checklist "Checklist trước cao điểm & kết mùa" (items toggle via PATCH). Table "Nhu cầu hàng mùa Tết theo ngày" Cols Hàng / Đơn đã chốt còn giao / Có thể bán / PO dự kiến về / Ngày về sớm nhất / Thiếu sau khi về.

### `/campaigns/customers` (3253)

- Search "Tìm tên, điện thoại, nhóm…"; Cols Khách / Nhóm / Doanh thu từ 01/10 / Lần mua gần nhất / Đồng ý nhận tin / "Tái đặt đơn" (opens phase 16 invoice dialog prefilled from last order, toast) or "Tạo đơn". Customer create/edit dialog (CAT-06; new labels: "Thêm khách hàng", "Sửa khách hàng") with group, price list, credit limit, terms, consent.

### Reports (3264–3321)

- `ReportFilter`: Từ / Đến (default season start 01/10 to today), hint "Số liệu tạm tính…", "Xuất CSV" → export job toast with progress and download link (replaces "Dữ liệu CSV: x" textarea), saved views menu (new labels "Lưu bộ lọc", "Bộ lọc đã lưu").
- Nhập–xuất–tồn: status pill; Cols Hàng / Đầu kỳ / Nhập / Xuất / Cuối kỳ / Giá trị cuối kỳ.
- Bán hàng: chips Theo hàng / Theo khách / Theo kênh; Cols Nhóm / Doanh thu / Trả hàng / Doanh thu thuần; reconciliation hint.
- Lãi gộp & kết quả quản trị: KPIs Doanh thu thuần / Giá vốn / Lãi gộp (sub Biên) / Sau chi phí hoạt động; Cols Hàng / Doanh thu thuần / Giá vốn / Lãi gộp / Biên.
- Tuổi nợ: tables "Phải thu khách" and "Phải trả nhà cung cấp", Cols name / Trong hạn / 1–30 ngày / 31–60 ngày / > 60 ngày / Tổng.
- Dòng tiền: KPIs Đầu kỳ / Tổng thu / Tổng chi / Cuối kỳ; Cols Phân loại / Thu / Chi / Ròng.
- Giỏ quà: lead "Doanh thu giỏ ghi cho giỏ; thành phần đã tiêu hao lúc đóng nên không cộng doanh thu hay xuất kho lần nữa."; Cols Chỉ tiêu / Giá trị; rows Giỏ đã đóng, Giỏ đã bán (xuất giao), Giỏ tồn, Giỏ còn phải đóng cho đơn, Doanh thu giỏ, Giá vốn giỏ đã bán, Lãi gộp giỏ, Combo bán tại quầy ("n combo · vnd").

### Settings (3358, 3379, 3385 + additions)

- `/settings/account`: card "Chủ cửa hàng" kv Họ tên / Cửa hàng / Đăng nhập gần nhất / Xác thực 2 lớp (+ setup/disable TOTP under re-auth); card "Thiết bị đang đăng nhập" rows with "Đăng xuất" or st "Thiết bị này". Additions (new labels, flagged): "Thông tin cửa hàng" (ORG-02), "Khóa sổ kỳ" (SEC-05), "Chuyển quyền chủ cửa hàng" (ORG-07), "Xuất toàn bộ dữ liệu" and "Đóng tài khoản" (SAAS-08), all sensitive actions under `RequireRecentAuth`; slot for the phase 19 offline policy card.
- `/settings/subscription`: plan cards Cửa hàng / Bán sỉ / Giỏ quà / Nhiều điểm with "Đang dùng" / "Gói nền"; "Thuê bao hiện tại" kv; "Truy cập hỗ trợ" → "Cấp quyền hỗ trợ 2 giờ (chỉ đọc)" then callout + "Thu hồi"; addition "Yêu cầu hỗ trợ" ticket list/create (SAAS-05).
- `/settings/audit-log`: search "Tìm thao tác, chứng từ, người dùng…"; Cols Thời điểm / Người dùng / Thao tác / Chứng từ / Chi tiết; cursor pages of 200.

### Onboarding step bodies (`/onboarding`)

store_profile (profile form), catalog_setup (guided manual entry: categories, products, units, prices through the phase 15 forms; no file import), opening_stock (opening adjustment lines with lots), opening_debt (party opening balances, tagged "đầu kỳ" later), opening_cash (fund opening balances), first_sale (link to POS, completes on first `HD-`), confirm_opening_balances (summary + confirm under re-auth). Skippable steps show "Bỏ qua"; labels are new.

### Admin console (`/admin/*`)

AdminShell on `admin.DOMAIN` (distinct header color, no tenant data cache) with pages: Tenants (search by name or slug, store link, plan, status, suspend/reactivate), Usage, Billing/invoices, Incidents (status banner text), Feature flags (per tenant), Support (grants and tickets, read-only tenant view only while a grant is active), Partners. All writes audited; destructive actions require typed confirmation.

### Global

- Sandbox only: util "Đặt lại mẫu" → modal "Đặt lại dữ liệu mẫu?" → `POST /sandbox/reset` → invalidate all queries.

## Related files

Create under `apps/web/src/features/`:

- `overview/{api,components,pages}/…` — `dashboard-page.tsx`, `notifications-page.tsx`, `period-bar.tsx`, `cash-band.tsx`, `debt-band.tsx`, `grouped-bars.tsx`
- `debt/{api,components,containers,pages}/…` — `debt-table.tsx`, `party-drawer.tsx`, `pay-dialog.tsx`, `allocation-table.tsx`, `debt-note-dialog.tsx`, `write-off-dialog.tsx`, `advances-page.tsx`, `allocate-advance-dialog.tsx`, `statements-page.tsx`, `statement-dialog.tsx`, `statement-drawer.tsx`, `dispute-dialog.tsx`, `schedule-page.tsx`, `receivables-page.tsx`, `payables-page.tsx`, `index.ts`
- `campaigns/…` — `seasons-page.tsx`, `campaign-card.tsx`, `demand-table.tsx`, `customers-page.tsx`, `customer-dialog.tsx`, `index.ts`
- `reports/…` — `report-filter.tsx`, `use-export-job.ts`, `saved-reports-menu.tsx`, `{stock-movement,sales,gross-profit,debt-aging,cash-flow,gift-baskets}-report-page.tsx`
- `settings/…` — `account-page.tsx`, `subscription-page.tsx`, `audit-log-page.tsx`, `store-profile-card.tsx`, `period-lock-card.tsx`, `ownership-transfer-card.tsx`, `data-export-card.tsx`, `account-closure-card.tsx`, `support-tickets-card.tsx`, `mfa-setup-dialog.tsx`
- `onboarding/steps/*.tsx`
- `admin/{api,pages,components}/…`
- `shared/ui/sandbox-reset-dialog.tsx`
- Tests beside each; `e2e/dashboard.spec.ts`, `e2e/debts.spec.ts`, `e2e/campaigns.spec.ts`, `e2e/reports.spec.ts`, `e2e/settings.spec.ts`, `e2e/onboarding.spec.ts`, `e2e/admin.spec.ts`

Modify: `src/app/router.tsx`, `src/app/shell/util-bar.tsx` (badge source), `src/test/msw/handlers.ts`.

## Implementation steps

1. Debt module first (PayDialog is reused by the dashboard): queries, aging table, party drawer, payment with allocation, note, write-off with re-auth, advances, statements, schedule.
2. Overview: dashboard bands and period bar; notifications with deep links; util badge from summary.
3. Campaigns and CRM with customer dialog and reorder hand-off to the invoice dialog.
4. Reports: `ReportFilter` in URL, six pages, saved views, export job hook.
5. Settings: three prototype screens, then additions behind re-auth.
6. Onboarding step bodies wired to the stepper; gate completion.
7. Admin console with separate API client and session.
8. Sandbox reset dialog.

## Todo

- [x] Debt: tables, party drawer, PayDialog + allocation, note, write-off (re-auth)
  - Evidence (2026-10-18): `apps/web/src/features/debt` (receivables/payables pages, party drawer, `containers/pay-dialog.tsx` with Decimal `allocation.ts`, debt-note and write-off dialogs; write-off goes reauth → `debt_write_off` exception with one Idempotency-Key); tests `allocation.test.ts`, `containers/*.test.tsx`, `pages/receivables-page.test.tsx`.
- [x] Advances + allocation; statements + dispute + print; schedule
  - Evidence (2026-10-18): `features/debt/pages/{advances,statements,schedule,print-statement}-page.tsx`, PT/PC/DS/DN openers in `features/debt/doc-openers.ts`; tests in `features/debt/pages/*.test.tsx`.
- [x] Dashboard + notifications + util badge
  - Evidence (2026-10-18): `features/dashboard` (`GET /reports/dashboard`, period in the URL), `features/notifications` (chips per kind incl. "Lỗi đồng bộ POS", read/resolve/snooze refresh the util-bar unread count); tests `pages/dashboard-page.test.tsx`, `pages/notifications-page.test.tsx`.
- [x] Campaigns + demand; CRM + customer dialog + reorder
  - Evidence (2026-10-18): `features/campaign` (cards, activate/close with If-Match, checklist PATCH, demand table), `features/crm` (list, customer dialog with consents, reorder drafts); tests `campaign/**/*.test.ts(x)`, `crm/**/*.test.ts(x)`.
- [x] Reports ×6 + ReportFilter + saved views + export jobs
  - Evidence (2026-10-18): `features/reports` (six pages, cursor paging keeping first-page totals, filter in the URL, saved reports CRUD, `use-export-job.ts` with bounded polling, cancel and expiry); tests `features/reports/**/*.test.ts(x)`.
- [x] Settings ×3 + additions (profile, period lock, ownership, export, closure, tickets, MFA)
  - Evidence (2026-10-18): `features/settings/pages/{account,account-closure,subscription,audit-log}-page.tsx` with owner, devices, TOTP (re-auth), store profile, period lock (reopen = re-auth + `period_reopen` exception), owner transfer, full export, closure lifecycle, plan cards, current subscription, support access grant/revoke, support tickets; audit log cursor pages of 200; tests `features/settings/pages/*.test.tsx`.
- [x] Onboarding steps ×7
  - Evidence (2026-10-18): `features/onboarding/steps/*-step.tsx` (seven bodies, skip only on `catalog_setup`/`first_sale`, opening steps behind the cut-off gate); tests `pages/onboarding-page.test.tsx`, `steps/opening-balances.test.tsx`.
- [x] Admin console pages
  - Evidence (2026-10-18): `features/admin/pages/*` (tenants, tenant, usage, invoices, incidents, feature flags, support, ticket, support view, partners) on its own client (`shared/api/schema-admin.d.ts`, `gen:api:admin`) and session; dark header in `app/layouts/admin-shell.tsx`; typed confirmation for destructive actions; session from `GET /me` (`adminGetMe`, `useAdminSession`) with the admin name in the header and "Giao cho tôi" on tickets; 429 on login in `features/auth/pages/admin-login-page.test.tsx`; tests `features/admin/**/*.test.tsx` cover every console page.
- [x] Sandbox reset
  - Evidence (2026-10-18): already in `app/layouts/util-bar.tsx` (`POST /tenant/sandbox/reset`), covered by `app/layouts/app-shell.test.tsx`; no change needed.
- [ ] Unit + e2e tests (unit done; e2e written, not run)
  - Evidence (2026-10-18): unit tests for debt, dashboard, notifications, campaign, crm and reports pass (`apps/web` 106 files / 715 tests after the review fixes, 3 consecutive green runs; typecheck and lint clean). Review fixes: export job kept in an app-level store (`features/reports/export-job-store.ts`, job id in sessionStorage) and downloadable from the `export_ready` notification; server error codes always read in Vietnamese with a generic fallback (`shared/api/api-error.ts`, feature adapters); field errors marked in debt and campaign dialogs (`features/debt/field-errors.ts`); per-row reads bounded by `shared/lib/concurrency.ts`; header payment picks any party through server search; `shared/ui/period-bar.test.tsx` added. Details and API gaps: `plans/reports/fullstack-developer-261018-1230-web-debt-campaign-reports.md` (Review fixes). E2E specs `apps/web/e2e/{debt,overview,campaigns,reports}.spec.ts` are written but not run (no `E2E_OWNER_PASSWORD`). Settings, onboarding and admin: `apps/web` 122 files / 842 tests, 2 consecutive green runs, typecheck and lint clean; Vietnamese texts for the store/platform error codes in `shared/api/api-error.ts`; E2E `apps/web/e2e/{settings,onboarding,admin}.spec.ts` written but not run. Details and API gaps: `plans/reports/fullstack-developer-261018-2040-web-settings-onboarding-admin.md`.
  - Review fixes (2026-10-19, after `plans/reports/code-reviewer-261018-2300-web-settings-onboarding-admin-review.md`): the onboarding gate lets `/catalog/*` through and the opening-debt step creates a customer in place (`features/crm/containers/customer-quick-dialog.tsx`); "Đổi mật khẩu" (`POST /auth/password`) and "Đăng xuất các thiết bị khác" (`POST /sessions:revoke-all`) under re-auth; owner transfer sends the invite only, per ORG-07; the business date runs on the server clock offset and turns at the store's midnight (`shared/api/platform-queries.ts`); one `GET /tenant` cache; add-ons in force read from `SubscriptionView.addons`; `POST /data-exports` and `subscription:change` send one Idempotency-Key per intent; tests run with `testTimeout` 15 s and no query retry. `apps/web` 130 files / 908 tests, two consecutive green runs by the lead, typecheck and lint clean.
  - API adoption (2026-10-19, report `plans/reports/fullstack-developer-261019-0242-web-adopt-api-gaps.md`): advances from `GET /payments?unallocated=true`, PT/PC/DN/BT drawers from their detail routes, statements and CRM from list fields (no per-row reads), revenue by customer in pages of 100 `customer_ids`, notifications follow `action_path`, "Nhắc lại mai" on server time, export jobs (report CSV and full data export) found again through `GET /export-jobs`, dashboard by-category cash, next due dates and season cards; the three OPEN-13 cost tiles stay without numbers. Idempotency-Key per intent on customers, consents, reorder drafts, statements and collection notes. `apps/web` 132 files / 948 tests, 10 consecutive green runs (agent).
  - Current gate (2026-10-19, lead): `apps/web` typecheck, lint and vitest green (156 files / 1109 tests). The e2e specs above are still not run, so this item stays open.

## Success criteria & verification

- `pnpm -C apps/web typecheck && pnpm -C apps/web lint && pnpm -C apps/web test` green.
- Unit: `pay-dialog.test.tsx` (oldest-first fill, both blocking callouts disable submit), `write-off-dialog.test.tsx` (re-auth prompt before submit), `allocate-advance-dialog.test.tsx` (two error messages), `period-bar.test.tsx` (labels per period, Asia/Saigon boundaries), `use-export-job.test.ts` (poll until succeeded/failed/expired), `notifications-page.test.tsx` (no "Chờ duyệt" chip).
- Playwright `debts.spec.ts`: receive payment allocated to two invoices → aging updates → statement → dispute; write-off with re-auth. `dashboard.spec.ts`: period switching and KPI drill to cash-flow. `reports.spec.ts`: each report renders; CSV export downloads a file. `settings.spec.ts`: sign out another device, support grant/revoke, period lock blocks a back-dated posting toast. `onboarding.spec.ts`: new tenant reaches dashboard after required steps. `admin.spec.ts` (baseURL `http://admin.localhost:5173`): admin login with TOTP, suspend tenant, flag toggle; tenant search by slug.
- Visual parity vs views 2528, 2110, 3139, 3141, 3170, 3179, 3192, 3246, 3253, 3264–3321, 3358, 3379, 3385 at 1440 and 390; additions are reviewed in phase 20 without a prototype baseline.

## Risks

- Scope is the largest web phase → debt and reports first; admin console can slip to the end of the phase without blocking tenant flows.
- Export job latency on large tenants → toast shows progress and survives navigation (job id kept in query cache).
- Re-auth friction on frequent owner actions → server-defined recent-auth window, not per click.

## Security

- Write-off, period lock, ownership transfer, data export, closure, MFA changes and support grants require recent auth.
- Export download links are short-lived signed URLs from the API; never cached by the service worker (phase 19 excludes `/export-jobs` and download paths).
- Admin API uses a separate cookie path and CSRF token; tenant pages never call it.
- Audit log is read-only in the UI.

## Rollback

Revert web image; affected routes fall back to placeholders. Admin console can be disabled independently by removing the `/admin` route in config.

## Next

Phase 19 mounts the offline policy card in `/settings/account` and adds the POS sync-exception notification kind. Phase 20 audits all screens including additions without prototype baselines.
