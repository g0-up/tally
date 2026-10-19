# Review Phase 13: SaaS control và settings (backend `apps/api`)

Ngày: 2026-10-18. Người review: code-reviewer. Chỉ review, không sửa code.

## Phạm vi

- Migrations `000047_saas_billing_closure_and_admin_mfa`, `000048` (legal_hold).
- `internal/saas/**` (lifecycle, entitlement, subscription, billing, closure, data_export, usage_job, provider), `internal/support/**`, `internal/admin/**`, `internal/org/onboarding.go` và `service.go` (opening cutoff), `internal/app/routes.go`, `internal/platform/{db/tx.go,db/tenant_tables.go,middleware/*}`, `internal/cli/tenant_restore.go`, `.golangci.yml` (depguard), `api/openapi.yaml`, `api/openapi-admin.yaml`, `docs/operations/tenant-restore.md`.
- `apps/api` vẫn untracked trong git nên tôi review trực tiếp nội dung file, không có diff.

Test đã chạy (không đụng container):
- `go test ./internal/saas/...`: pass.
- `go test -tags integration -count=1 ./internal/app/ -run SaaS`: báo **`[no tests to run]`**, vì không test nào chứa chuỗi "SaaS". Tôi chạy lại theo tên thật: `TestReadOnlySubscription…`, `TestPaymentWebhook…`, `TestPlanSkuLimit…`, `TestSupportSession…`, `TestAdminTenantList…`, `TestAdminAPIOnly…`, `TestAccountClosure…`, `TestOpeningBalancesConfirmation…`. Cả 8 pass trong 7.9s.

## Critical

Không có.

## High

### H1. Có thể brute-force TOTP của admin: không đếm số lần sai theo admin hay theo challenge
- Vị trí: `internal/admin/auth.go:184-245` (`VerifyTOTP`), `internal/admin/auth.go:35` (`ChallengeTTL = 5m`), `internal/admin/http.go:21-22,61,82-84`.
- Hiện trạng:
  - Challenge đã ký dùng lại được không giới hạn số lần trong 5 phút.
  - Giới hạn duy nhất là limiter theo IP (10 lần/phút, burst 10), dùng chung cho `/auth/login` và `/auth/totp`.
  - Không có bộ đếm lỗi hay khóa theo `admin_id`.
- Kịch bản: kẻ tấn công đã có mật khẩu admin (lộ hoặc phishing) và một botnet hay proxy pool khoảng 1.000 IP.
  - Mỗi IP thử được 10 lần/phút, tổng cộng khoảng 10.000 lần/phút.
  - Hết 5 phút thì login lại lấy challenge mới (vì đã biết mật khẩu).
  - Cửa sổ skew ±1 cho khoảng 3 mã hợp lệ trên 10^6, nên kỳ vọng trúng sau khoảng 30–35 phút.
  - Guard replay `last_totp_step` không giúp được, vì nó chỉ chặn dùng lại một mã đã thành công.
- Đề xuất:
  - Thêm `platform_admins.totp_failed_count` và `totp_locked_until` (hoặc một bảng attempts). Tăng trong cùng câu UPDATE khi `MatchTOTP` sai. Khóa 15 phút sau 5 lần sai và ghi audit/slog cảnh báo.
  - Hoặc gắn nonce dùng một lần vào challenge, lưu trong DB, cho tối đa 3–5 lần thử, sau đó phải login lại.
  - Thêm limiter theo email/admin_id cho `/auth/login`, không chỉ theo IP.

### H2. Store đã đóng (closure đã được owner xác minh) có thể tự mở lại
- Vị trí:
  - `internal/saas/repo.go:146-149`: `syncTenantStatus` có `WHERE status IN ('active','read_only','closed')`, tức là cho phép chuyển từ `closed` sang `active`.
  - `internal/saas/subscription.go:61-83`: `AdminChangePlan`, khi subscription không live thì insert một subscription active mới.
  - `internal/saas/billing.go:531-605`: `applyPurchase`, khi `!live` thì ép `PurposeNewPeriod`, insert subscription mới rồi sync tenant status.
  - `internal/saas/closure.go:309-370`: `completeClosure` không void các `saas_invoices` còn `open`.
- Kịch bản:
  1. Owner yêu cầu đóng tài khoản, xác minh qua email, chờ 30 ngày, và job `CompleteClosures` đưa tenant về `closed`.
  2. Hóa đơn SV-… phát hành trước đó vẫn `open`. Một chuyển khoản tự động hoặc chuyển khoản muộn khớp số hóa đơn, webhook gọi `applyPayment`. Một subscription mới được tạo, tenant quay lại `active` và bị tính phí một kỳ mới.
  3. Admin đổi plan cho tenant đã đóng (thao tác nhầm trong console) cũng mở lại store y như vậy.
  4. Lúc này `account_closure_requests` vẫn là `completed` nhưng store đang hoạt động. Quyết định retention/purge của operator dựa trên trạng thái "đã đóng" sẽ sai. Nếu operator chạy purge đúng lúc store bị mở lại, purge từ chối (FOR UPDATE + `status='closed'`). Còn nếu store bị mở lại sau khi operator đã quyết purge, dữ liệu kỳ mới vẫn thuộc diện bị xóa.
- Đề xuất:
  - Phân biệt "closed do closure" với "closed do hết hạn".
  - `applyPurchase` và `AdminChangePlan` phải trả 409 `tenant_closed` khi closure mới nhất là `completed`. Mở lại chỉ được qua một lệnh admin tường minh, có audit.
  - `completeClosure` void mọi invoice `open` của tenant trong cùng transaction.
  - Webhook đến cho invoice đã void thì ghi event `rejected` và báo cho admin để hoàn tiền, không áp dụng.

### H3. Support view module `debt_cash` lỗi 500: debt đọc bảng `tenants` trong transaction read-only
- Vị trí: `internal/debt/queries.go:59` (`Parties`) và `internal/debt/queries.go:120` (`Ledger`) gọi `tenantToday` (`internal/debt/repo.go:202-206`, `SELECT timezone FROM tenants`) bên trong `s.read(ctx, scope, …)`. Với principal support, hàm này chạy `WithReadOnlyTenantTx` (`internal/debt/service.go:74-79`).
- Nguyên nhân: `000026_privileges_assertions.up.sql:20-26` thu hồi toàn quyền trên `tenants` từ `tally_readonly`. Bản fix timezone của phase này chỉ áp cho inventory, sales, reporting, crm và kits (dùng `db.ReadOnlyTimezone`). Debt bị bỏ sót.
- Kịch bản: owner cấp support session cho module `debt_cash`. Admin mở `GET /support-view/debt_cash/debts/parties?direction=receivable`, Postgres trả `permission denied for table tenants` (42501), API trả 500 và log lỗi. `GET /debts/parties/:id` cũng vậy.
- Test chỉ cover `catalog/*` (`internal/app/saas_control_integration_test.go:369-456`), nên CI không bắt được.
- Đề xuất:
  - Cho `debt.tenantToday` dùng cùng mẫu với `inventory/ports.go:85-108`: kiểm `has_table_privilege('tenants','SELECT')`, nếu không có quyền thì dùng `db.ReadOnlyTimezone`. Hoặc đọc `today` trước bằng `s.today(ctx, tenantID)` (tx thường), như `procurement/queries.go:204` đang làm.
  - Thêm integration test dạng bảng: với mỗi module trong `support.ViewModules`, gọi ít nhất một GET mỗi segment qua support view và kỳ vọng khác 5xx.
  - Grep lại các đường đọc khác có thể chạm `tenants` dưới read-only. Tôi đã kiểm pricing (`readPriced` đọc trước bằng tx thường, OK), org `Tenant` (tx thường, OK), procurement (`s.today`, OK). `debt/collection.go:305` chỉ chạy từ POST reminder nên không bị.

## Medium

### M1. "Maintenance" không chặn mọi ghi trong lúc `tenant copy`
- Vị trí: `internal/saas/entitlement.go:122` (chỉ đặt `canPost=false`), `internal/app/routes.go:131-144,248-261`. `TenantAccountAPI` không có middleware Entitlement.
- Runbook `docs/operations/tenant-restore.md` §3 ghi "Hold the store so nothing posts while rows are copied".
- Hiện trạng: trong maintenance vẫn có các đường ghi sau.
  - Qua `TenantAccountAPI`: tạo ticket, support message, `POST /data-exports`, account-closure request/verify/cancel, onboarding complete/skip (bao gồm `ConfirmOpeningBalances`, chèn `period_locks`), `POST /feedback`, `PATCH /tenant`, settings.
  - Các worker: outbox handlers (ví dụ `handleFirstSale`), notifications, export jobs, closure, lifecycle.
- Kịch bản:
  - Operator bật maintenance rồi chạy `tenant copy`. Song song, owner xác nhận số dư đầu kỳ, chèn một `period_locks` mới vào cùng bảng mà copy đang chèn bản ghi từ backup.
  - Với `ON CONFLICT DO NOTHING`, dòng từ scratch bị bỏ lặng lẽ nếu trùng khóa. Kiểm tra `after >= scratch` vẫn qua vì live có thêm dòng khác, nên dữ liệu khôi phục bị thiếu mà không báo.
- Đề xuất:
  - Khi `tenants.maintenance` bật, cho middleware chặn mọi method không an toàn trên cả `TenantAccountAPI` (trả 423 `tenant_maintenance`).
  - Cho job runner bỏ qua tenant đang maintenance.
  - Đổi so sánh của copy thành đếm các khóa scratch có mặt trong live (anti-join theo PK), thay cho so `count(*)`.

### M2. Closure có thể kẹt im lặng, và export của closure không đầy đủ
- Vị trí: `internal/saas/closure.go:336-345`, `closure.go:201-208`, `data_export.go:36`, `entitlement.go:115-124`.
- Kẹt im lặng: nếu export job của closure có status `failed`, `completeClosure` cứ `return nil` mỗi giờ mãi mãi. Không ai re-queue, không alert, và owner tưởng store đã đóng.
- Export thiếu dữ liệu:
  - Export được snapshot lúc verify (ngày 0), nhưng tenant ở trạng thái `closing` vẫn được posting suốt 30 ngày, vì gate không xử lý `closing`. Mọi chứng từ của 30 ngày đó không có trong bản export mà closure dựa vào.
  - Link tải hết hạn sau 7 ngày, và `completeClosure` chấp nhận trạng thái `expired`.
- Đề xuất:
  - Khi export `failed`: re-queue có giới hạn (ví dụ 3 lần), sau đó ghi audit/slog error và hiển thị trên admin console.
  - Hoặc chặn posting khi `tenants.status='closing'`, hoặc chạy một export cuối ngay trước khi complete và đợi nó `succeeded`.
  - Ghi rõ trong UI và email rằng link chỉ sống 7 ngày.

### M3. Purge: điều kiện an toàn còn mỏng
- Vị trí: `internal/cli/tenant_restore.go:544-653`.
- Hiện trạng:
  - Chỉ kiểm `status='closed'` và `!legal_hold`. Trạng thái `closed` cũng sinh ra khi subscription quá hạn read_only (không trả tiền), không cần owner yêu cầu closure và không có export.
  - Không có `--dry-run`. Không có bước xác nhận như gõ lại slug.
  - Không kiểm `legal_hold` của các sandbox.
  - File trên object store không bị xóa. Plan đã ghi deviation này, nhưng có nghĩa là các zip "full data export" chứa toàn bộ dữ liệu khách hàng vẫn còn sau purge.
- Kịch bản: operator dán nhầm tenant id của một store chỉ quá hạn thanh toán và có backup mới (điều kiện dễ đạt). Toàn bộ dữ liệu bị xóa trong một transaction và slug bị retire vĩnh viễn.
- Đề xuất:
  - Yêu cầu closure request mới nhất là `completed` và đã qua retention, hoặc thêm cờ tường minh `--lapsed` kèm lý do.
  - Thêm `--confirm-slug <slug>` phải khớp.
  - Thêm `--dry-run` in số dòng sẽ xóa (dùng `purgeRows` trong một tx rồi rollback).
  - Từ chối nếu sandbox nào có legal_hold.
  - Thêm bước xóa object (attachments, export zip) hoặc ghi danh sách key cần xóa vào output và runbook.

## Low

- **L1.** `000047_….up.sql:32`: `platform_admins.last_totp_step bigint` thiếu `CHECK (last_totp_step >= 0)`, trong khi cột tương ứng của owner ở 000028 có. Nên thêm cho nhất quán.
- **L2.** `internal/inventory/inventory_integration_test.go:994`: comment "still 23:00 on the expiry day in Ho Chi Minh" đã lỗi thời. Kỳ vọng `"3"` đúng là vì 16:00 UTC là 01:00 ngày hôm sau ở Tokyo. Nên sửa comment thành "01:00 the day after in Tokyo (Ho Chi Minh would still sell it)". Xem phần Đã kiểm.
- **L3.** `lifecycle.go:15-16` `CanLogin` là code chết, không ai gọi. Login chỉ bị chặn bởi `identity/sessions.go:46` (`t.status NOT IN ('closed','replaced')`) sau khi job hằng giờ sync status. Khoảng trễ tối đa khoảng 1 giờ, chỉ cho đọc, vì gate tính subscription tại thời điểm đọc nên posting và export đã bị chặn. Nên xóa `CanLogin` hoặc dùng nó trong `ResolveSession`.
- **L4.** Plan mục 11 nói purge "evicting it from the host cache", nhưng code yêu cầu restart API (`tenant_restore.go:551,649`). Đến khi restart, host cache vẫn resolve slug cũ về một tenant id đã xóa. Rủi ro thấp vì các query sẽ không tìm thấy dòng nào, nhưng nên sửa plan hoặc runbook cho khớp.
- **L5.** `000047`: 5 `CREATE INDEX` không dùng `CONCURRENTLY`, và `ADD CONSTRAINT CHECK` trên `auth_tokens` quét bảng dưới ACCESS EXCLUSIVE. Hiện các bảng đều nhỏ và chưa có prod nên chấp nhận được. Ghi chú cho các migration sau trên bảng lớn.
- **L6.** Lệnh test lead gợi ý (`-run SaaS`) không khớp test nào. Nên dùng `-run 'TestReadOnlySubscription|TestPaymentWebhook|TestPlanSkuLimit|TestSupportSession|TestAdmin|TestAccountClosure|TestOpeningBalances'`.

## Trả lời các câu hỏi cụ thể

- **Timezone trong `WithReadOnlyTenantTx`** (`internal/platform/db/tx.go:33-48,84`):
  - Đúng hướng. `app.tenant_timezone` được set bằng tally_app trước khi chuyển role, và chỉ là GUC local của transaction.
  - Các reader dùng mẫu kiểm `has_table_privilege` rồi fallback `ReadOnlyTimezone`: inventory `ports.go:99`, sales `service.go:180`, reporting `service.go:92`, crm `service.go:111`, kits `service.go:138`.
  - Tenant không tồn tại thì setting rỗng và rơi về Asia/Ho_Chi_Minh, chấp nhận được.
  - Chỗ còn thiếu là debt (H3).
- **`TestStockReaderShowsShelfStockByColor`**: thay đổi kỳ vọng là hợp lệ, không che bug. Ca `16:00 UTC, readOnly=true` phân biệt được hai zone: theo Ho Chi Minh (23:00 ngày hết hạn) sẽ là `"5"`, theo Tokyo (01:00 ngày sau) là `"3"`. Kỳ vọng `"3"` chứng minh tx read-only giờ dùng zone của tenant. Chỉ comment là sai (L2).
- **depguard** (`.golangci.yml`): không bị nới quá tay.
  - `list-mode: lax` là bắt buộc khi thêm danh sách `allow`. Ở mode `original`, có allow list nghĩa là mọi package ngoài danh sách đều bị cấm.
  - Deny list giữ nguyên đủ 18 domain. Allow duy nhất là `internal/saas/provider`, và có rule `saas-provider` cấm mọi file ngoài `saas`, `app`, `cli` import nó.
- **423 hay 409 cho `period_locked`**: giữ **423**. Đây là mã dùng chung toàn platform: `httpx/render.go:39` map `PeriodLocked` sang `StatusLocked`, và `periodlock/guard.go:59`. `api/openapi.yaml:34` cùng nhiều response 423 đã công bố từ các phase trước. Đổi riêng cho onboarding sẽ làm client phải xử lý hai mã cho cùng một lỗi. Nên sửa text plan phase 13, không sửa code.
- **OpenAPI so với handler**: đã đối chiếu mọi route trong `saas/http.go`, `support/http.go` (tenant và admin), `org/onboarding.go`, `admin/http.go` (bao gồm `/feedback` và `/status`) với `openapi.yaml:9963-10644` và `openapi-admin.yaml`. Không thấy route thiếu spec hay spec thiếu route.

## Đã kiểm, ổn

- **TOTP:**
  - So sánh constant-time (`authn/totp.go:69`).
  - Chống replay bằng `UPDATE … WHERE last_totp_step < $2` (`admin/auth.go:218-219`).
  - Secret được seal AES-GCM với AD là admin id.
  - Login với email không tồn tại vẫn hash dummy, nên không lộ email qua timing.
  - Log chỉ chứa IP và admin_id, không có secret hay mã.
- **Tách admin và tenant:**
  - Cookie, CSRF và host group riêng. Admin API chỉ trả lời trên admin host (có test).
  - Middleware `Tenant` chỉ nhận Owner/Support khi `p.TenantID == host.TenantID`.
  - Session token được lưu dạng hash, TTL tuyệt đối 8 giờ, và join `a.status='active'`.
- **Support session:**
  - Cấp quyền cần owner, re-auth và consent token. Ticket phải open và đã được assign.
  - Thời hạn 1–8 giờ, mặc định 2 giờ. Revoke có audit một lần.
  - `OpenAccess` kiểm session chưa revoke và chưa hết hạn, kiểm consent, và tenant không closed/replaced. Mỗi request được audit.
  - Chỉ cho GET, kiểm ở hai lớp. Có chặn `..` và trailing slash. Header Cookie bị xóa, Host bị ép về tenant host.
  - Không segment nào trong `ViewModules` dẫn tới route file (download, print, export).
  - Mọi domain xem được đều dùng `WithReadOnlyTenantTx`. DB từ chối ghi trong tx support (có test).
- **IDOR:**
  - Ticket phía tenant lọc `tenant_id = $2` và từ chối `uuid.Nil` (`support/service.go:181-183,310-315`).
  - Admin thao tác ticket qua `withTicketTenant`, rồi chuyển sang tenant tx.
  - Attachment được kiểm owner bằng ticket id dưới RLS.
- **RLS:** không có bảng mới. Các bảng platform không có RLS là có chủ đích (header 000025) và đã bị REVOKE khỏi `tally_readonly` (000026).
- **Migrations:**
  - Không có trigger, function, view hay DO block.
  - Down của 000047 đảo đúng thứ tự (xóa token `closure_verify` trước khi khôi phục CHECK cũ). Các key limits chưa từng tồn tại trước 000047, nên xóa chúng khi down là an toàn.
  - 000048 up và down đối xứng.
  - `ADD COLUMN … NOT NULL DEFAULT const` chỉ đổi metadata.
- **Billing:**
  - Event được insert trước, có `UNIQUE(provider,event_id)`. Thêm advisory lock `saas_billing:` và lock hàng invoice FOR UPDATE.
  - Kiểm tenant khớp và số tiền khớp chính xác. Lỗi trước khi commit thì rollback cả event, nên provider retry được.
  - `MarkPaid` dùng một event id cố định cho mỗi invoice.
  - Proration dùng decimal và `RoundVND`.
  - Webhook xác minh HMAC-SHA256 bằng `hmac.Equal` trước khi parse, secret ít nhất 32 byte, body tối đa 64KB. Không có timestamp, nhưng replay một body đã ký bị dedupe theo `event_id`, và số invoice nằm trong phần được ký.
  - Có test webhook trùng chỉ gia hạn một lần.
- **Data export:**
  - Không export các bảng identity (users, sessions, auth_tokens, mfa_*).
  - Chống CSV formula injection.
  - Dùng snapshot tx, cap 512MB, có heartbeat. Quota 5 export đang chạy, kiểm dưới advisory lock.
- **Closure:** không bao giờ xóa dữ liệu. Tôn trọng legal hold (snapshot trên request cùng cờ tenant, khóa FOR UPDATE). Cancel khôi phục status theo subscription.
- **Restore CLI:**
  - `restore-check` chỉ đọc, trong tx read-only.
  - `copy` từ chối nếu không ở maintenance, chạy bằng tally_app dưới RLS cho đúng một tenant. Nó all-or-nothing, và có verify ledger trước khi commit.
  - `purge` có FOR UPDATE, từ chối sandbox, không closed hoặc legal hold. Ngoài dev/test nó yêu cầu backup pg_dump mới. Slug được retire trong cùng tx.
- **Onboarding:**
  - `ConfirmOpeningBalances` lock hàng tenant và step. Cutoff không được ở tương lai, và period lock chỉ tiến về phía trước.
  - Sau khi xác nhận, `PATCH /tenant` không đổi được `opening_cutoff_date` nữa (`org/service.go:259-286`).
  - Re-auth chỉ áp cho bước confirm.
- **Admin tenant directory** không đọc bảng nghiệp vụ (có test). Usage job ghi bảng platform, mỗi tenant một tx, lỗi một tenant không dừng cả vòng.

## Đề xuất ưu tiên

1. H3: sửa `debt.tenantToday` cho tx read-only, và thêm test support-view cho mọi module.
2. H1: khóa TOTP theo admin hoặc challenge, và thêm limiter theo email.
3. H2: không cho mở lại store đã đóng qua payment hoặc đổi plan; void invoice open khi complete closure.
4. M1: maintenance chặn cả `TenantAccountAPI` và worker; copy so khóa thay vì so count.
5. M2, M3: re-queue/alert export của closure; bổ sung confirm, dry-run và xử lý object store cho purge.

```
Status: DONE_WITH_CONCERNS
Summary: Không có lỗi Critical. Có 3 High: brute-force TOTP admin (không khóa theo admin/challenge), store đã đóng tự mở lại qua thanh toán hóa đơn còn open hoặc qua admin đổi plan, và support view module debt_cash lỗi 500 vì debt đọc bảng tenants trong tx read-only. Có 3 Medium về maintenance/copy, closure export và độ an toàn của purge.
Concerns/Blockers: Lệnh `-run SaaS` không chạy test nào, tôi đã chạy 8 test phase 13 theo tên thật và đều pass. H3 kết luận từ đọc code (REVOKE trên tenants ở 000026 và debt/queries.go:59,120) vì không có test nào cover; nên viết test xác nhận trước khi sửa.
```
