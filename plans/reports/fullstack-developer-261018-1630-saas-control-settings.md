# Phase 13 backend: SaaS control, hỗ trợ và thiết lập

Đã làm đủ 12 Todo của phase 13. Mọi gate xanh (chi tiết ở mục Gate). Không commit, không đụng `apps/web` hay `tally-postgres-1`. Lượt sửa theo review thêm migration `000049_admin_lockout_closure_export` (xem mục Sửa theo review). Lead cần chạy `gen:api` vì cả hai spec đều đổi.

## Đã làm

| Phần | Nội dung chính | Test |
|---|---|---|
| Plan, subscription, provisioning | Plan lấy từ seed của migration 000025. Provisioner luôn tạo subscription `trialing` và entitlement cho store mới. Trial không có `ai_qa`, chỉ `addon_ai_qa` mới mở; AI Q&A còn bị gate bởi `analytics_ai_enabled`. | `TestPaymentWebhookExtendsThePeriodOnce` kiểm tra store mới đang trial và không có `ai_qa` |
| SubscriptionLifecycle và job | Bảng chuyển trạng thái trial → active → grace → read_only → closed, mỗi state trả `CanPost/CanExport` (`CanLogin` đã xoá khi sửa theo review). Thời hạn lấy từ `plans.limits`. Job chạy hằng giờ, theo ngày của từng tenant. | `TestSubscriptionLifecycleTransitionTable`, `TestLifecycleStatesAllowWhatTheTermsPublish` |
| Entitlement, limit, posting guard | `saas.Reader` cài `EntitlementGuard` cho coordinator (read_only → 403 `subscription_read_only`) và module gate (403 `module_not_in_plan`). Limit SKU và storage cho 409 `plan_limit_reached`. | `TestReadOnlySubscriptionRefusesPostingsButKeepsReadsAndExports`, `TestPlanSkuLimitRefusesTheSkuPastIt`, `TestRouteModuleNamesThePlanModuleOfEachRoute` |
| Billing, webhook | Số hoá đơn `SV-…`, preview proration theo ngày kèm terms. Có port provider với hai adapter: chuyển khoản thủ công và signed webhook (HMAC, so sánh constant-time). Event được insert trước (UNIQUE `(provider, event_id)`), rồi khoá dòng invoice, chuyển `open → paid` và gia hạn đúng một lần. | `TestPaymentWebhookExtendsThePeriodOnce`: cùng event hai lần, rồi event khác cho invoice đã trả, vẫn chỉ một lần gia hạn, một dòng `saas_payment_events`, một audit `saas_invoice.paid`; chữ ký sai cho 403 |
| Ticket hỗ trợ | SLA theo plan. Bộ dò cụm từ giống mật khẩu cảnh báo chủ store trước khi gửi; admin gửi tin như vậy thì bị từ chối. | `TestLooksLikePasswordCatchesCredentialPhrases`, `TestNewMessageValidate` |
| Phiên hỗ trợ | Grant cần re-auth, consent token, chọn module, tối đa 8h. Revoke được. Admin đọc qua `WithReadOnlyTenantTx`, chỉ GET của module đã cấp, mỗi lần truy cập đều ghi audit vào nhật ký của tenant. | `TestSupportSessionIsReadOnlyScopedAndAudited`: GET 200; POST/PATCH/DELETE 403 `support_read_only`; module chưa cấp, đã revoke hay hết hạn đều 403 `support_session_inactive`; ghi trong tx read-only lỗi PG read-only; đếm audit theo từng lần truy cập |
| Feature flag, admin auth, danh bạ tenant | Flag có lịch sử thay đổi. Admin bắt buộc TOTP, có cookie và CSRF riêng, route `/api/admin/v1` chỉ chạy trên host admin. Danh bạ chỉ đọc bảng platform và lọc `status <> 'replaced'`. | `TestAdminAPIOnlyAnswersOnTheAdminHost`, `TestAdminTenantListCarriesNoBusinessData` (contract test), `TestDirectoryQueriesReadOnlyPlatformTables` |
| Usage, sự cố, partner, feedback | Job usage hằng ngày, `GET /status` công khai, mã giới thiệu lưu lúc đăng ký, hoa hồng tính trên hoá đơn dịch vụ, feedback không chứa nội dung chứng từ. | `TestCommissionIsPercentOfPaidServiceInvoicesRoundedToDong`, `TestIncidentFieldChecks`, `TestFeedbackInputValidate`, `TestReferralCodePattern` |
| Đóng tài khoản, export toàn bộ | Request, verify (re-auth và link một lần qua mail), rồi `waiting` kèm job `full_data_export`; legal hold chặn việc đóng. Export ghi mỗi bảng tenant một CSV UTF-8 có BOM, thêm `README.csv` làm từ điển cột, gói thành zip. | `TestAccountClosureQueuesTheFullDataExport`: số entry bằng số bảng tenant cộng README, entry nào cũng có BOM, tên "Chị Lan", "Xưởng may Bình Tân", "cái" giữ nguyên |
| Onboarding | Bảy bước, có quy tắc skip. `confirm_opening_balances` cần re-auth, cutoff và ba bước opening; sau đó trả tổng và đặt period lock tới cutoff. `PATCH /tenant` nhận `opening_cutoff_date` và khoá trường này sau khi xác nhận. | `TestOpeningBalancesConfirmationLocksThePeriod`, `TestOnboardingStepStatusAndSkippable` |
| CLI và runbook | `tally tenant restore-check`, `maintenance on/off`, `copy`, `purge`; `tally admin create` đọc mật khẩu từ stdin. Runbook ở `docs/operations/tenant-restore.md`. | `TestTenantRestoreCheckAndCopy`, `TestTenantPurgeRetiresTheAddress` (slug bị retire, đăng ký lại trả `taken`), `TestReadPasswordTakesTheFirstLine` |

Sandbox: grant/revoke consent và issue/consume link đóng tài khoản đều đi qua `withAccountTx` và có dòng trong `TestAccountChangesRefuseAReplacedSandbox`. Lookup và revoke consent không giả định tenant của token trùng tenant của session.

Phần dọn thêm trong lượt này:
- **Lint (9 lỗi có sẵn từ phần implement trước).** Domain-isolation của depguard deny theo prefix `internal/saas`, nên chặn luôn `saas` import package con `saas/provider` của chính nó. Tôi đặt rule đó sang `list-mode: lax`, cho phép riêng `saas/provider`, và thêm rule `saas-provider` để chỉ saas, `internal/app` và `internal/cli` được import package này. Đã thử bằng file probe: domain khác import `saas` hay `saas/provider` vẫn bị báo. Các lỗi còn lại đã sửa: hai tham số không dùng ở onboarding, bốn struct literal ở `support/http.go` chuyển thành type conversion, và `errLegalHold` / `CodeLegalHold` không dùng nên đã xoá.
- **`TestStockReaderShowsShelfStockByColor` (inventory) fail ở lần chạy integration đầu.** `WithReadOnlyTenantTx` giờ chép múi giờ của tenant vào transaction trước khi đổi role, đúng như key insight của plan, nên phiên hỗ trợ dùng múi giờ của tenant thay vì mặc định Hồ Chí Minh. Tôi cập nhật expectation theo hành vi mới chứ không nới lỏng test.

## File đã sửa

Tất cả nằm trong `apps/api/` trừ runbook và file plan.
- **Tạo mới:**
  - `internal/saas/` (billing, closure, data_export, dto, entitlement, errors, http, lifecycle, model, plans_admin, repo, service, subscription, usage_job, saas_test) và `internal/saas/provider/`;
  - `internal/support/`, `internal/featureflag/`, `internal/admin/`;
  - `internal/cli/admin.go`, `internal/cli/tenant_restore.go` và test của chúng;
  - `internal/app/module_gate.go`, `internal/app/saas_control_integration_test.go`;
  - `api/openapi-admin.yaml`;
  - `docs/operations/tenant-restore.md`.
- **Sửa:**
  - `internal/app/` (app, domains, routes, tenancy, openapi_contract_test, sandbox_reset_integration_test);
  - `internal/cli/` (control, identity, root, serve, tenant, worker, cli_integration_test);
  - `internal/identity/` (consent, dto, http, mail, model, ports, repo, service);
  - `internal/org/` (attachments, control, onboarding, ports, provisioner, service);
  - `internal/catalog/` (service, skus);
  - `internal/platform/db/` (tenant_tables, tx), `platform/middleware/` (entitlement, session), `platform/config`, `platform/httpx/command_router.go`, `platform/ids`;
  - `inventory/ports.go`, `sales/service.go`, `reporting/service.go`, `crm/service.go`, `kits/service.go`;
  - `internal/inventory/inventory_integration_test.go`, `internal/testkit/postgres.go`;
  - `api/openapi.yaml`, `.golangci.yml`.
- **Plan:** `phase-13-saas-control-settings.md`, với 12 Todo đã tick và mục Evidence.

## Gate

Chạy trong `apps/api`:
- `gofmt -l .`: rỗng.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run --build-tags integration ./...`: 0 issues.
- `go test -race ./...`: pass.
- `go test -tags integration -count=1 ./...`: pass.
- `docker ps --filter label=org.testcontainers=true`: không còn container nào.

## Lệch so với spec

- `period_locked` trả **423**, không phải 409 như success criteria ghi. 423 là mã chung của period lock trên toàn platform từ các phase trước.
- Phiên hỗ trợ hết hạn được kiểm tra lúc đọc; không có job sweep.
- Phiên hỗ trợ thuộc admin đang được assign ticket. Chưa assign thì grant trả 409 `ticket_not_assigned`.
- Module của support-view là một map cố định trong `support` (module → path segment). Non-GET trả 403 `support_read_only`.
- Tenant copy chỉ thêm dòng thiếu (`ON CONFLICT DO NOTHING`) và chỉ các bảng tenant có RLS. Nó fail nếu có bảng ít dòng hơn scratch hoặc ledger lệch. Với foreign key nullable tạo vòng, copy insert cột đó rỗng trước rồi điền lại sau.
- Purge không xoá file trên object store và không xoá `login_attempts`. Sau purge phải restart API để xoá host cache.
- Scratch DB được nối qua DSN (`--scratch-url` / `--from`); CLI không tự dựng container. Runbook hướng dẫn dựng bằng compose.
- Legal hold lưu ở `tenants.legal_hold` và đổi qua `subscription:set-status`.
- Thanh toán `new_period` dời kỳ về ngày thanh toán. Hoá đơn 0đ tự trả lúc phát hành.
- Link đóng tài khoản là `/settings/account-closure?token=`. Export khi đóng tài khoản không tính vào quota export.
- Audit của webhook có source `web`. Chữ ký webhook sai trả 403 `signature_invalid`.
- Subscription gate luôn bật; tenant chưa có subscription thì không bị giới hạn. Worker dựng domains không có gate.
- Limit SKU là giới hạn mềm: đếm rồi so trong cùng transaction, không có ràng buộc DB.
- Thay đổi plan, flag, sự cố và partner được ghi bằng slog, không vào `audit_events` (là bảng tenant).
- Login admin throttle theo IP và theo e-mail (thêm khi sửa H1). Phiên admin hết hạn tuyệt đối sau 8h. Danh sách admin giới hạn 500 dòng.
- Job lifecycle và closure chạy hằng giờ. Thông báo subscription dùng notification sweep có sẵn. Hoa hồng partner tính lúc đọc.
- Ba bước opening hoàn tất trong transaction của posting tương ứng.
- Tin nhắn đầu của ticket mới không có đính kèm; đính kèm đi qua `POST /support-tickets/{id}/messages`.
- Env mới: `SAAS_BANK_ACCOUNT`, `SAAS_WEBHOOK_PROVIDER`, `SAAS_WEBHOOK_SECRET`. Lệnh mới: `tally tenant maintenance on|off`.
- **Đổi hợp đồng, web cần `gen:api`:**
  - `ExportJob` có thêm enum `kind`; `report_key` rỗng khi kind là `full_data_export`.
  - `PATCH /tenant` nhận `opening_cutoff_date` và trả 409 `opening_balances_confirmed` sau khi xác nhận.
- `openapi-admin.yaml` tự chứa schema của nó (lặp lại một số schema của spec tenant), để không phụ thuộc chéo giữa hai file.
- Chi tiết tenant trong admin có plan (kèm giá gói) bên trong `subscription`. Đó là dữ liệu thuê bao, không phải dữ liệu kinh doanh của store. Contract test kiểm danh sách, các key cấp ngoài cùng của chi tiết và các key usage.
- Sửa plan áp dụng module và limit ngay lập tức.
- Runbook nằm ở `docs/operations/tenant-restore.md` và link sang `docs/runbook.md` §3. `docs/runbook.md` chưa link ngược sang trang mới, vì file đó không nằm trong danh sách được sửa.

## Chưa làm và lý do

Không có Todo nào bỏ dở. Hai việc nằm ngoài phạm vi được phép sửa:
- Thêm link từ `docs/runbook.md` sang `docs/operations/tenant-restore.md`, và thêm các lệnh CLI mới (`tenant restore-check|maintenance|copy|purge`, `admin create`) vào bảng lệnh của runbook.
- Màn hình web (phase 18) và chạy `gen:api`.

## Sửa theo review

Đã sửa đủ các finding trong `plans/reports/code-reviewer-261018-1800-saas-control-settings-review.md` (H1–H3, M1–M3, L1–L4). Mỗi lỗi có test đỏ trước rồi mới sửa, trừ lượt làm lại M2 theo phương án A: lượt đó tôi viết code trước, test sau (xem cuối mục Quyết định M2). Phase 13 vẫn để `status: in-progress`. Không commit, không đụng `apps/web` hay `tally-postgres-1`. Lead cần chạy lại `gen:api`, vì cả hai spec đổi tiếp.

### Từng finding

- **H1, brute-force TOTP admin.**
  - Sai 5 mã liên tiếp thì khoá tài khoản 15 phút (`MaxTOTPFailures`, `TOTPLockDuration` trong `internal/admin/auth.go`). Đếm theo tài khoản admin chứ không theo challenge, nên mở challenge mới không reset bộ đếm. Đang khoá thì mã đúng cũng bị từ chối, trả 429 `rate_limited` kèm `Retry-After`.
  - Đăng nhập bằng mật khẩu có thêm limiter theo e-mail, ngoài limiter theo IP.
  - Lúc khoá, API ghi slog Warn `admin account locked`. Không ghi audit vì không có bảng audit cho platform (`audit_events` là bảng của tenant).
  - Migration **000049_admin_lockout_closure_export** (có down) thêm `totp_failed_count` (CHECK ≥ 0), `locked_until`, và `CHECK (last_totp_step >= 0)` (L1). Cùng migration này đổi hai CHECK cho M2 (xem dưới).
  - Test: `TestAdminTOTPLocksTheAccountAfterFiveWrongCodes`, `TestAdminLoginIsLimitedPerEmail`.
- **H2, store đã đóng tự mở lại.**
  - Thanh toán hoá đơn và admin đổi plan hay đổi trạng thái của store đã đóng theo yêu cầu đều trả **409 `tenant_closed`**.
  - Lúc complete closure, mọi hoá đơn `open` chuyển sang `void`, nên webhook đến muộn nhận 409 `invoice_not_open` và rollback.
  - Thanh toán đến sau khi hoá đơn đã void (lead chấp nhận hoàn tiền thủ công): webhook vẫn trả 409 và rollback, nhưng giờ báo cho operator. Mỗi lần bị từ chối thì log ERROR `payment refused: its invoice is not open, refund it by hand`. Audit `saas_invoice.payment_refused` ghi trong transaction riêng, mỗi event của provider đúng một lần dù provider gửi lại bao nhiêu lần, gồm provider, event id, số tiền, mã tham chiếu. Runbook `docs/operations/tenant-restore.md` §4 có hướng dẫn hoàn tiền. Test viết đỏ trước.
  - Test: `TestAClosedStoreStaysClosed`.
- **H3, support view `debt_cash` trả 500.**
  - `debt.tenantToday` giờ dùng `db.ReadOnlyTimezone` khi transaction không đọc được `tenants`.
  - Test bảng mới gọi qua support view một GET cho mỗi segment của mọi module. Test này lộ thêm hai chỗ đọc hỏng ở pricing: `tenants` (múi giờ) và `users` (tên người đổi giá). Cả hai đã sửa theo cùng mẫu `has_table_privilege`.
  - Test: `TestSupportViewOpensEveryGrantedModule`. Test này cũng kiểm rằng mọi segment của `support.ViewModules` đều được phủ.
- **M1, maintenance.**
  - Đang maintenance thì mọi unsafe method trả **423 `tenant_maintenance`**: posting, settings, billing, support, onboarding, feedback, export, closure.
  - Lệch có chủ ý: nhóm route bảo mật tài khoản (`TenantSecurityAPI`: re-auth, mật khẩu, 2FA, phiên, chuyển quyền owner) vẫn chạy, để chủ store xử lý được sự cố bảo mật ngay giữa lúc restore.
  - Các job export, lifecycle, closure và notification bỏ qua store đang bị hold.
  - `tenant copy` chỉ commit khi mọi dòng của scratch đều có trong live theo khoá chính. Nếu thiếu, nó in `MISSING n` và từng dòng `missing <table> key`.
  - Test: `TestMaintenanceRefusesEveryChangeAndHoldsTheJobs`, `TestTenantCopyRefusesAScratchRowTheLiveDatabaseCannotTake`.
- **M2, export lúc đóng tài khoản.** Làm theo phương án A mà lead đã duyệt; lý do chọn ở mục Quyết định M2.
  - Máy trạng thái: `requested → verified → waiting → exporting ⇄ export_failed → completed`. `cancelled` chỉ đi được từ `requested`, `verified`, `waiting`.
  - Export cuối lỗi được queue lại cho tới khi đủ `ClosureExportAttempts` = 3 lần lỗi trong một vòng. Số lần đếm từ `export_jobs` theo `params->>'closure_request_id'`, `reason` và `round` (không thêm cột). Mỗi lần queue lại có audit `account_closure.export_requeued`.
  - Lần lỗi thứ 3 đưa closure sang `export_failed`: audit `account_closure.export_failed` một lần, worker log `ERROR account closure stopped`. Store vẫn `closing` và chỉ đọc.
  - Route admin mới `POST /tenants/{id}/account-closure:retry-export`: closure `export_failed` thì mở vòng mới (`round` + 1, audit `account_closure.export_retried`); closure `completed` mà export còn tải được thì gửi lại link (audit `account_closure.export_link_requested`), link cũ hết hiệu lực. Admin tenant detail hiện trạng thái closure (`closure.request.status`) và `export.failed_attempts`.
  - Test: `TestAClosureWhoseFinalExportKeepsFailingWaitsForAnOperator`, `TestClosureEndsWithAFinalExportTheOwnerCanDownload`, `TestMaintenanceRefusesEveryChangeAndHoldsTheJobs`, unit `TestClosureExportIsDownloadableOnlyWhileItsSucceededLinkWorks`, `TestOnlyAClosureTakingItsFinalExportFreezesTheStore`.
- **M3, độ an toàn của purge.**
  - Purge giờ bắt buộc `--confirm-slug`, và có `--dry-run` (chạy mọi bước kiểm và lệnh xoá rồi rollback).
  - Chỉ purge được store đã đóng theo yêu cầu: closure gần nhất phải `completed`. Store đóng vì hết hạn thuê bao bị từ chối.
  - Legal hold của sandbox cũng chặn purge.
  - Purge in prefix object store (`tenants/<id>/`) của store và của từng sandbox, vì file vẫn chưa bị xoá (OPEN-11).
  - Test: `TestTenantPurgeRetiresTheAddress`.
- **L2–L4.**
  - Sửa comment ở test inventory.
  - Xoá `CanLogin`.
  - Plan bước 11 và runbook giờ ghi rõ: sau khi purge phải restart API để host cache quên địa chỉ store.
  - L5 và L6 chỉ là ghi chú, không cần sửa code.

### Quyết định M2: phương án A, máy trạng thái `exporting` / `export_failed`

Lead chọn A. Cách làm:
- **Đóng băng theo trạng thái, không theo ngày.** Gate của store `closing` chỉ chặn posting khi closure gần nhất là `exporting` hoặc `export_failed` (`closureFrozen`). Hết ngày chờ, store vẫn ghi được cho tới lượt worker kế tiếp (job chạy hằng giờ, nên tối đa khoảng 1 giờ). Login và đọc vẫn mở. `CancelClosure` từ chối 409 `closure_state_invalid` khi đang `exporting` hoặc `export_failed`.
- **Bước hết chờ:** worker chuyển `waiting → exporting` và queue export cuối (`reason = closure_final`, `round = 1`, audit `account_closure.final_export_queued`). Legal hold giữ closure ở `waiting` hoặc `exporting`.
- **Chỉ complete khi job `succeeded`** và còn trong hạn. Đã bỏ nhánh chấp nhận `expired`. Export hết hạn trong lúc bị legal hold giữ thì được queue lại, không tính là lỗi.
- **Token `closure_export`:**
  - Lưu hash trong `auth_tokens`, hết hạn cùng `expires_at` của job (7 ngày). Dùng lại được nhiều lần. Phát link mới thì retire mọi link cũ của tenant.
  - Route công khai `GET /api/v1/account-closure/export?token=`, không cần session, chạy được trên tenant đã `closed` (`ResolveSlug` vẫn resolve store đã đóng). Rate limit theo IP: burst 10, hồi 10 lần mỗi phút.
  - Token sai, bị thay hoặc hết hạn, hay mở trên host của store khác: 404 `closure_link_not_found`. Job không còn tải được: 409 `export_not_ready`. Thành công: 302 tới presigned URL 5 phút, kèm `Cache-Control: no-store` và `Referrer-Policy: no-referrer`.
  - Token không bị log (request logger không ghi query string) và không xuất hiện trong response nào.
- **Mail sau commit.** Cả bước complete và admin retry chạy bên trong transaction của `IssueExportLink` (interface `ClosureTokens` mở rộng với `IssueExportLink` và `ResolveExportLink`; lead đặt tên này). Identity insert token trong cùng transaction rồi gửi mail sau khi commit. Code mail vẫn nằm trong identity (template `closure_export`).
- **Worker có mailer.** Worker dựng identity qua helper `newIdentity` dùng chung với `serve` (SMTP, khoá ký, URL builder); `config.Worker` có thêm `Secrets`. Compose prod dùng chung `app-env`, nên không cần thêm biến môi trường.
- **Migration:** gộp vào 000049 cùng H1, đổi tên thành `000049_admin_lockout_closure_export`. Đổi CHECK trạng thái closure (`ck_account_closure_requests__statuses`) và CHECK `auth_tokens.purpose` (`ck_auth_tokens__purposes`); lint cấm tạo lại tên constraint cũ nên phải dùng tên mới. Down xoá token `closure_export`, đưa closure `exporting`/`export_failed` về `waiting`, rồi dựng lại hai CHECK cũ với đúng tên cũ.

Vì sao không chọn B hay C:
- **B (B' của lượt trước):** giữ store `closing` và chỉ đọc thêm 7 ngày cho link tải hết hạn, tổng khoảng 37 ngày. Việc chặn ghi tính theo ngày, nên không có trạng thái rõ ràng. Export lỗi quá số lần thì closure "stall" lơ lửng, operator không có đường xử lý ngoài việc chờ owner huỷ rồi yêu cầu lại.
- **C (đóng mà không có export cuối, hoặc chỉ dùng export đầu):** mất dữ liệu ghi trong 30 ngày chờ. Test có kiểm: khách tạo ở ngày 15 và ngay trước lượt worker đều có trong export cuối, không có trong export đầu.
- **A:** trạng thái tường minh, cancel bị chặn theo trạng thái, retry có giới hạn và có đường cho operator. Store đóng đúng lúc export cuối xong, owner vẫn tải được sau khi đóng nhờ token.

**Thứ tự test:** lượt làm lại này tôi viết code trước, test integration sau, chứ không theo red-first. Để bù lại, tôi chạy thử mutation. Bỏ freeze trong gate thì cả hai test closure fail. Cho complete khi job còn `queued` thì `TestClosureEndsWithAFinalExportTheOwnerCanDownload` fail. Sau khi thử, code đã trả về như cũ.

### Mã lỗi và hợp đồng mới (web cần `gen:api`)

- **Mã mới:**
  - 409 `tenant_closed`;
  - 423 `tenant_maintenance`;
  - 429 `rate_limited` kèm `Retry-After` khi admin bị khoá TOTP;
  - 403 `subscription_read_only` giờ cũng áp cho store có closure `exporting` hoặc `export_failed`;
  - 404 `closure_link_not_found` và 409 `export_not_ready` ở route tải export của store đã đóng.
- **`api/openapi.yaml`:**
  - Contract lỗi chung có `tenant_maintenance` và nghĩa mới của `subscription_read_only`.
  - Payment và plan change có thêm `tenant_closed` và `invoice_not_open`.
  - Mô tả của `verifyAccountClosure` theo máy trạng thái mới; `cancelAccountClosure` ghi rõ chỉ huỷ được trước khi lấy export cuối.
  - `AccountClosureRequest.status` thêm `exporting`, `export_failed`; `waiting_until` và `export_job_id` có mô tả mới.
  - Schema mới `AccountClosureExport` có `final`, `failed_attempts` (đều required). Không còn `attempts`, `stalled`.
  - Route công khai mới `GET /account-closure/export` (`downloadAccountClosureExport`, `security: []`): 302, 404, 409, 429.
- **`api/openapi-admin.yaml`:**
  - Login và TOTP ghi rõ 429 khi bị khoá.
  - Đổi plan, đổi trạng thái và hoá đơn có `tenant_closed`.
  - `TenantDetail` có `closure` (nullable, `AccountClosureView`), kèm các schema closure chép sang (cùng thay đổi status và `failed_attempts`).
  - Route mới `POST /tenants/{id}/account-closure:retry-export` (`adminRetryTenantClosureExport`): 200 `AccountClosureView`, 404, 409 `closure_state_invalid`.
- **Migration:** chỉ có `000049_admin_lockout_closure_export`, không có 000050. Không trigger, function, view hay DO block.

### File chạm thêm trong lượt sửa

Ngoài danh sách file của phase:
- `platform/apperr`, `platform/httpx/render.go` (`TenantMaintenance` → 423);
- `platform/middleware` (gate maintenance);
- `platform/db/tenants.go`;
- `cli/serve.go`, `cli/worker.go`, `cli/identity.go` (helper `newIdentity`, worker có mailer);
- `internal/identity` (`consent.go`, `model.go`, `mail.go`, `errors.go`: token và mail `closure_export`);
- `platform/mailer/templates/closure_export.{subject,html,text}.tmpl`;
- `platform/config/config.go` (`Worker.Secrets`);
- `internal/admin/http.go`, `internal/admin/tenants.go` (route retry);
- `internal/app/routes.go` (nhóm `Public` cho route tải export);
- `internal/debt` (H3, lead đã cho phép);
- `internal/pricing` (lỗi H3 thứ hai mà test lộ ra);
- comment ở `inventory_integration_test.go`;
- `docs/operations/tenant-restore.md`;
- `phase-13-saas-control-settings.md` (chỉ text bước 11, giữ nguyên status).

### Gate sau khi sửa

Chạy trong `apps/api`:
- `gofmt -l .`: rỗng.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run ./...` và `golangci-lint run --build-tags integration ./...`: 0 issues.
- `go test -race -count=1 ./...`: pass.
- `go test -tags integration -count=1 ./...`: mọi package pass (`internal/app` 58s), gồm `TestMigrationsAreReversible` và lint migration.
- `docker ps --filter label=org.testcontainers=true`: không còn container nào (ryuk tự thoát sau lượt chạy).

### Còn lưu ý

- **Closure `export_failed`:** operator được báo qua log ERROR một lần mỗi lần dừng, audit và trạng thái trên admin tenant detail. Chưa có kênh notification riêng cho admin.
- **Store không có owner active lúc complete:** store vẫn đóng, nhưng không có link nào được gửi; worker log ERROR. Admin retry trên closure đó trả 409.
- **Purge xoá dòng export**, nên link tải ngừng chạy. Runbook dặn chờ tới `expires_at` của export cuối.
- **Thanh toán đến sau khi hoá đơn đã void:** webhook nhận 409 và rollback; operator thấy qua log ERROR và audit `saas_invoice.payment_refused`, rồi hoàn tiền thủ công theo runbook. Thanh toán admin xác nhận tay (`MarkPaid`) trên hoá đơn đã void thì admin nhận 409 ngay, nên không ghi thêm.
- **Khoá admin:** chỉ ghi bằng slog, không có bảng audit cho platform.
- **Purge:** vẫn không xoá file trên object store (OPEN-11).

## Câu hỏi mở

1. Giữ `423 period_locked` (thống nhất với platform) hay sửa success criteria của phase 13 thành 423?
2. Có cần job sweep đánh dấu phiên hỗ trợ hết hạn, hay kiểm tra lúc đọc là đủ?
3. Purge có cần xoá file trên object store của tenant không? Việc này phụ thuộc chính sách lưu giữ (OPEN-11).
4. M2 (đã chốt phương án A): link tải export cuối sống 7 ngày, bằng `DataExportLinkTTL`. Có cần cho owner thời gian dài hơn không? Nếu cần thì phải kéo dài `expires_at` của export cuối, vì token hết hạn cùng job.

Status: DONE_WITH_CONCERNS
Summary: Phase 13 backend đã đủ: plan, subscription, lifecycle, entitlement, billing với webhook idempotent, ticket và phiên hỗ trợ read-only có audit, admin API bắt buộc TOTP, đóng tài khoản kèm export toàn bộ, onboarding với period lock, CLI restore/copy/purge cùng runbook, OpenAPI tenant và admin. Mọi gate xanh.
Concerns/Blockers: Đã sửa đủ các finding của review; M2 làm theo phương án A, test của lượt này viết sau code (đã chạy mutation để kiểm). `period_locked` là 423 chứ không phải 409. Hai spec đã đổi nên web cần chạy `gen:api`. `docs/runbook.md` chưa link sang runbook mới.
