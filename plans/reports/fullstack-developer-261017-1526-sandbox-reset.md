# Báo cáo: sandbox reset bằng cách tạo tenant mới

## Kết quả

`POST /api/v1/tenant/sandbox/reset` không còn xoá dữ liệu. Mỗi lần reset, một tenant sandbox mới (id mới) được tạo bằng provisioner hiện có và nhận lại slug `<parent>-demo` (hoặc `demo` với sandbox độc lập). Tenant cũ chuyển sang trạng thái `replaced` và đổi slug thành `reset-<32 hex của id>`. Tenant cũ giữ nguyên mọi dòng dữ liệu nhưng không còn host, tài khoản hay phiên nào trỏ tới. Tài khoản chủ cùng mọi phiên đăng nhập và token được chuyển sang tenant mới, nên người gọi vẫn đăng nhập trên cùng địa chỉ. Response là `200` kèm `Tenant` mới (có `id`, `slug`) và `ETag`. Toàn bộ chạy trong một transaction.

## Đã làm

- **Migration 000034 `tenant_replaced_status`** (up/down):
  - Thêm trạng thái `replaced` vào CHECK status của `tenants`. Constraint mới tên `ck_tenants__status_kinds`, theo quy ước của 000031/000033 vì lint cấm tạo lại tên constraint đã có.
  - Thêm `ck_tenants__replaced_sandbox`: chỉ sandbox mới được ở trạng thái `replaced`.
  - Down sẽ thất bại khi còn tenant `replaced`, giống cách 000029 làm.
- **`org.ResetSandbox(ctx, actor) (Tenant, error)`**, các bước trong một `WithTenantTx(old)`:
  1. `readTenant FOR UPDATE`. Không phải sandbox thì trả 403 `not_sandbox`. Status khác `active` thì trả 409 `sandbox_not_active`, trường hợp một reset đồng thời đã thay tenant này.
  2. Đổi slug và status của tenant cũ, rồi ghi audit `tenant.replace` trong tenant cũ.
  3. Chuyển `set_config('app.tenant_id', new)` và gọi `Provision` (`IsSandbox=true`, cùng `SandboxOf`/`ParentSlug`, chạy seed mặc định và demo sample).
  4. Chuyển tài khoản: một câu lệnh CTE cập nhật `users`, `sessions`, `auth_tokens` từ tenant cũ sang tenant mới.
  5. Ghi audit `tenant.sandbox_reset` trong tenant mới.
  6. Sau commit, gọi callback `org.Deps.AddressMoved(slug)` để host cache quên id cũ.
- Đã xoá `unlockSandboxPeriods` và `sandboxResetReason`: tenant mới chưa có khoá kỳ nào.
- **Đã xoá cơ chế cũ**:
  - interface `SandboxResetter` và trường `Seeders.SandboxResetters`;
  - `ResetSandbox` của pricing, catalog, partner;
  - `cash/reset.go` cùng test của nó;
  - dòng wiring cùng comment trong `domains.go`, và trường `Domains.clock` vì chỉ resetter dùng nó.

  Các test seeder của pricing, catalog, partner được bỏ phần reset và đổi tên.
- **Identity**:
  - `ResolveSession` loại tenant `closed` và `replaced`.
  - `ResolveSlug` loại tenant `replaced`.
- **`cli/serve.go`**: gom callback purge host thành `purgeHost` và truyền cho cả identity (`TenantCreated`) lẫn org (`AddressMoved`).
- **OpenAPI**: `resetSandbox` giờ trả `200 Tenant` kèm ETag, có thêm `409 sandbox_not_active`, phần mô tả viết lại.
- **Plan**: sửa dòng ORG-06 và hai todo liên quan trong `phase-04-identity-organization.md`. `docs/database.md` và `docs/runbook.md` không mô tả kiểu reset xoá dữ liệu nên không sửa.
- **Đã nhắn**: `phase10-debt-cash` về việc xoá file trong cash, `phase06-pricing` về việc sửa `seed.go` và test của pricing.

## File thay đổi

- Mới: `apps/api/migrations/000034_tenant_replaced_status.up.sql`, `apps/api/migrations/000034_tenant_replaced_status.down.sql`, `apps/api/internal/app/sandbox_reset_integration_test.go`
- Viết lại: `apps/api/internal/org/sandbox.go`
- Sửa ở `internal/org`: `model.go` (`TenantReplaced`), `service.go` (`AddressMoved`), `ports.go`, `http.go`, `org_integration_test.go` (test mới là `TestSandboxResetReplacesTheTenant`)
- Sửa ở identity và CLI: `apps/api/internal/identity/sessions.go`, `apps/api/internal/cli/serve.go`
- Sửa ở `internal/app` và OpenAPI: `apps/api/internal/app/domains.go` (chỉ sửa bằng Edit), `apps/api/api/openapi.yaml`
- Chỉ xoá:
  - `apps/api/internal/cash/reset.go`, cùng test trong `cash_integration_test.go`;
  - `ResetSandbox` trong `pricing/seed.go`, `catalog/seed.go`, `partner/seed.go`, cùng phần reset trong các integration test của ba package này.
- Plan: `plans/261016-0854-tally-full-product-implementation/phase-04-identity-organization.md`

## Lệnh kiểm tra và kết quả thật (chạy trong apps/api)

| Lệnh | Kết quả |
|---|---|
| `gofmt -l .` | không có file |
| `go vet ./...` / `go vet -tags integration ./...` | sạch |
| `golangci-lint run ./...` | `0 issues.` |
| `go test ./internal/org/... ./internal/app/... ./internal/identity/... ./migrations/...` | ok (gồm lint migration và OpenAPI contract test) |
| `go test ./internal/platform/audit/ -run ActionName` | ok |
| `go test -tags integration ./internal/org/... ./internal/app/... ./internal/identity/...` | ok: org 5.8s, app 13.5s, identity ok |
| `go test -tags integration ./internal/catalog/ ./internal/partner/ -run TestSeedersAreIdempotent` | ok |
| `go test -tags integration ./internal/pricing/ -run TestSeederCreatesTheBaseListsOnce` | ok |
| `go test -tags integration ./internal/cash/` | ok |
| `go test -tags integration ./migrations/ ./internal/cli/` | ok (gồm reversibility up/down) |

Mọi integration test chạy bằng testcontainers, không đụng `tally-postgres-1`.

`TestSandboxResetProvisionsANewTenant` (trong `internal/app`) chạy hoàn toàn qua HTTP và các service thật:

- Sandbox của một store thật được tạo như `seed demo --parent`. Trong sandbox có price_changes (`buildPricingStore`), stock_moves (stock adjustment opening), obligation (`buildLedgerStore`), payment tiền mặt kèm cash_entries và obligation_settlements. Sau đó gọi reset.
- Kiểm tra trên tenant cũ:
  - mọi bảng ledger và tài liệu giữ nguyên số dòng;
  - slug đã đổi, status là `replaced`;
  - `ResolveSlug` của slug cũ không ra kết quả;
  - tenant cũ không còn user hay session nào.
- Kiểm tra trên tenant mới:
  - `/me` trên cùng cookie trả tenant mới;
  - có sample, không có SKU cũ, ledger trống;
  - ghi được dữ liệu bằng phiên và CSRF cũ;
  - đăng nhập lại cũng vào tenant mới.
- Reset lần hai thành công. Lúc đó có hai sandbox `replaced` và slug không bị đưa vào `retired_tenant_slugs`.
- Verifier của debt, cash và inventory (cùng logic với `tally ledger verify --tenant`) sạch cho cả ba tenant sandbox và tenant thật.
- Reset tenant thật trả 403 `not_sandbox`.
- Kiểm chứng thêm: tạm bỏ `AddressMoved` thì test fail ở `/me` (401), nghĩa là test thật sự bao phủ phần wiring host cache. Sau đó đã khôi phục.

## Quyết định và lý do

- **Chuyển tài khoản thay vì chép membership và vai trò.** Schema không có bảng membership hay vai trò. Mỗi tenant có đúng một user chủ, và `email_key` là UNIQUE toàn cục, nên không thể có bản sao. Chuyển user thì buộc phải chuyển cả sessions và auth_tokens vì FK `(tenant_id, user_id)`. Ba bảng này được cập nhật trong một câu lệnh, và FK được kiểm khi câu lệnh kết thúc; điều này đã được kiểm chứng bằng integration test.
- **Slug cũ là `reset-<hex>` (38 ký tự)** và không ghi vào `retired_tenant_slugs`. Slug đăng ký tối đa 30 ký tự nên không thể trùng. `retired_tenant_slugs` chỉ dành cho store bị purge, mà reset không purge gì. Nếu retire `<parent>-demo` thì `Provision` sẽ báo `slug_taken` từ lần reset kế tiếp.
- **Thêm trạng thái mới `replaced`** thay vì dùng `closed`. `closed` có ngữ nghĩa vòng đời của store thật (closing → closed). `replaced` thì nói rõ tenant đã bị thay và cho phép ràng buộc "chỉ sandbox mới bị thay".
- **Chuyển `app.tenant_id` giữa transaction.** RLS của `audit_events` yêu cầu tenant hiện tại phải bằng `actor.TenantID`, nên mỗi audit được ghi khi tenant của nó đang là tenant hiện tại. Provisioner vẫn được dùng nguyên vẹn, đúng như yêu cầu `actor.TenantID == spec.ID`.
- **Host cache được purge sau commit** qua `org.Deps.AddressMoved`, theo đúng mẫu `identity.Deps.TenantCreated`.
- **Tenant mới nhận `DisplayName`/`LegalName` hiện tại của sandbox**; settings và onboarding về mặc định.
- **Test cấp HTTP nằm trong file riêng `sandbox_reset_integration_test.go`** với stack riêng có cả pricing, inventory, debt và cash. Cách này tránh sửa `newLedgerStack` hay `newStack` mà agent khác đang dùng.

## Đợt hai: xử lý các câu hỏi mở

1. **Job nền bỏ qua tenant `replaced`.**
   - Thêm `db.ActiveTenantIDs` vào `internal/platform/db/tenants.go`, cạnh `TenantIDs`. Hàm này trả mọi tenant trừ `status = 'replaced'`. Comment của nó ghi rõ invariant: job tạo hoặc gửi dữ liệu dùng `ActiveTenantIDs`, còn job chỉ hoàn tất hoặc kiểm tra dữ liệu đã ghi (outbox dispatch, ledger verify, purge) dùng `TenantIDs`.
   - Trong `internal/cli/worker.go`, `runStockJobs` (roll hạn dùng, tín hiệu tồn) chuyển sang `ActiveTenantIDs`. `purgeIdempotency` giữ `TenantIDs`, vì nó chỉ xoá key đã hết hạn. Outbox `DispatchOnce` và `cli/ledger.go verifyLedgers` cũng giữ `TenantIDs`.
   - Gợi ý recost là subscriber outbox, không phải job lặp tenant. Vì vậy tôi thêm wrapper `skipReplaced(outbox.Handler)` trong `worker.go` và bọc `stock.HandleRecostSuggested`. Event của tenant `replaced` vẫn được drain (đánh dấu processed) nhưng handler không chạy. Cách này không phải sửa `internal/inventory`.
   - Test mới: `TestActiveTenantIDsSkipsReplacedSandboxes` (db) kiểm tra `ActiveTenantIDs` loại tenant replaced, còn `TenantIDs` vẫn trả nó. `TestSkipReplacedDrainsWithoutHandling` (cli) kiểm tra handler chỉ chạy cho tenant active.
   - Đã nhắn `phase07-inventory` (job nhả giữ hàng) và `phase09-sales` (job đóng ca): dùng `db.ActiveTenantIDs`, và bọc bằng `skipReplaced` subscriber outbox nào tạo dữ liệu.
   - Các job nhắc nợ và đóng ca chưa có trong worker, nên hiện không còn điểm gọi nào khác. Ngoài `db/tenants.go`, code không có truy vấn liệt kê tenant nào khác.
2. **Host cache nhiều replica**: đã thêm một dòng vào Risks của `phase-21-acceptance-load-security-release.md`. Không sửa code.
3. và 4. **Màn hình admin và support_sessions**: đã thêm một dòng vào Key insights của `phase-13-saas-control-settings.md`. Admin lọc `status <> 'replaced'`. `support_consent` token đã chuyển sang tenant mới, nên phase 13 không được giả định tenant của token trùng tenant của session.

File sửa thêm trong đợt hai:

- `apps/api/internal/platform/db/tenants.go`
- `apps/api/internal/platform/db/db_integration_test.go`
- `apps/api/internal/cli/worker.go`
- `apps/api/internal/cli/cli_integration_test.go`
- `plans/261016-0854-tally-full-product-implementation/phase-21-acceptance-load-security-release.md`
- `plans/261016-0854-tally-full-product-implementation/phase-13-saas-control-settings.md`

Kiểm tra lại sau đợt hai (chạy trong apps/api):

| Lệnh | Kết quả |
|---|---|
| `gofmt -l .` | không có file |
| `go vet ./...` / `go vet -tags integration ./...` | sạch |
| `golangci-lint run ./...` | `0 issues.` |
| `go test ./internal/cli/... ./internal/platform/db/...` | ok |
| `go test -tags integration ./internal/org/... ./internal/app/... ./internal/identity/... ./internal/cli/... ./internal/platform/db/... ./internal/platform/outbox/...` | ok: org 6.0s, app 14.1s, identity ok, cli 7.6s, db 5.5s, outbox 6.1s |

## Đợt ba: sửa theo review `code-reviewer-261017-1526-sandbox-reset-review.md`

- **M1 (chọn b).** Đã sửa comment ở `db/tenants.go` và `cli/worker.go`. Comment giờ chỉ khẳng định điều code đảm bảo: không còn host, phiên hay background job nào ghi vào hoặc gửi đi cho tenant `replaced`. Cửa sổ một request đang chạy dở được chấp nhận và ghi trong comment của `ResetSandbox` và `DispatchOnce`.
- **M2 và M4 (outbox).**
  - `ResetSandbox` gọi `closePendingEvents` trong cùng transaction. Hàm này đặt `processed_at = now` và `last_error = 'tenant replaced by a sandbox reset'` cho mọi event chưa xử lý của tenant cũ, rồi ghi số event đã đóng vào `closed_events` của audit `tenant.replace`.
  - `outbox.DispatchOnce` và `purgeIdempotency` chuyển sang `ActiveTenantIDs`. `ledger verify` vẫn dùng `TenantIDs`.
  - Đã bỏ `skipReplaced` cùng test của nó. Comment của `DispatchOnce` ghi rõ: event do request đang chạy dở ghi sau reset sẽ nằm yên và không bao giờ được xử lý.
  - Đã báo lại cho `phase07-inventory` và `phase09-sales`.
- **M4 (giới hạn tần suất).**
  - `org.SandboxResetInterval = 10 * time.Minute`. Sandbox có `created_at` chưa quá 10 phút bị từ chối bằng `ResetTooSoonError`, có `Unwrap` ra apperr `RateLimited` với mã `sandbox_reset_too_soon`.
  - Handler đặt header `Retry-After` theo số giây làm tròn lên.
  - Thứ tự kiểm tra: `not_sandbox` (403), rồi `sandbox_not_active` (409), rồi `sandbox_reset_too_soon` (429).
  - Không giới hạn tổng số lần reset. OpenAPI thêm response 429 kèm header.
- **M3.** `TestSandboxResetProvisionsANewTenant` giờ kiểm theo thứ tự:
  1. Reset sandbox mới tạo nhận 429 có `Retry-After`.
  2. Sau `clk.Advance(SandboxResetInterval)`, reset nhận 403 `reauth_required` và phiên vẫn ở tenant cũ.
  3. Gọi `/auth/reauth` xong thì reset nhận 200.

  Bước 2 cũng chứng minh route có `Reauth`.
- **M5.**
  - Copy trong `util-bar.tsx`: "Dữ liệu demo hiện tại sẽ được thay bằng dữ liệu mẫu mới; dữ liệu cũ được lưu trữ và không còn truy cập được. Thao tác này chỉ áp dụng cho sandbox."
  - Mô tả trong OpenAPI được viết lại theo cùng ý.
- **Q2.** Đã thêm vào Key insights của `phase-13-saas-control-settings.md` một dòng về PII trong sandbox `replaced`:
  - xoá vật lý theo OPEN-11, bằng bước CLI của operator;
  - cùng quy trình purge khi đóng store cha;
  - FK `sandbox_of` không cascade, nên purge phải duyệt tenant con trước.
- **Q3.** Không phải sửa thêm. Trước thay đổi của tôi, `ResolveSession` đã có `t.status <> 'closed'`, nên `closed` vốn đã bị chặn; tôi chỉ thêm `replaced`. `ResolveSlug` trước đây không lọc status, giờ chỉ loại `replaced`.
- **Low.**
  - L2 (`Idempotency-Key`): route reset không đăng ký idempotency, và contract không khai báo header này. Web thôi gửi key cho reset; test web giờ kiểm header vắng mặt và mock trả `200 Tenant`. Retry sau khi server đã commit sẽ chạm sandbox mới tạo và nhận 429, nên không reset lần hai. Test org và app có kiểm trường hợp này.
  - L4: migration `000036_auth_tokens_user_index` thêm `ix_auth_tokens__user (tenant_id, user_id)`. Design doc đã cập nhật.
  - L5: `replaced` trả "The demo store was already reset"; các trạng thái khác `active` trả "The demo store is not active". Mã lỗi vẫn là `sandbox_not_active`.
  - L6: thêm test âm. `UPDATE` một store thật sang `replaced` phải vi phạm `ck_tenants__replaced_sandbox`. `docs/runbook.md` đã có dòng nói down migration không chạy được khi còn sandbox replaced. Ràng buộc "`replaced` là trạng thái cuối" cần trigger, mà trigger bị cấm, nên tôi để phase 13 tôn trọng trạng thái này.
  - L7, phần docs và contract lệch:
    - `design/database-schema-org-partners.md` có CHECK status mới, CHECK `replaced` chỉ cho sandbox, ghi chú slug của sandbox reset, và index `ix_auth_tokens__user`.
    - `apps/web/src/shared/api/schema.d.ts` được sinh lại bằng `npm run gen:api`. Diff lớn vì gồm cả thay đổi OpenAPI của các agent khác.
    - Comment của `TenantHostCache` (`middleware/host_context.go`) không còn ghi "slugs are immutable". Comment mới ghi rằng reset purge địa chỉ, và purge chỉ có hiệu lực trong process.
  - L1, race login và reset: **không sửa**. Mọi biến thể đều fail-closed, trả 500 hoặc deadlock 40P01 rồi rollback, không hỏng dữ liệu; cửa sổ là thời gian một request. Cách sửa (khoá `users` trước `tenants`, map 23503/40P01 sang 409 hoặc 503) cần test đồng thời, nên để lại.
  - L3(b), race nạp cache và purge: không sửa. Đã ghi vào comment của cache; phase 21 đã có dòng rủi ro về nhiều replica.
  - `phase-04-identity-organization.md`: dòng ORG-06 thêm re-auth, giới hạn 10 phút, 429 và việc đóng event outbox.

File sửa trong đợt ba:

- API:
  - `apps/api/internal/org/sandbox.go`, `errors.go`, `http.go`, `org_integration_test.go`
  - `apps/api/internal/cli/worker.go`, `cli_integration_test.go`
  - `apps/api/internal/platform/db/tenants.go`
  - `apps/api/internal/platform/outbox/dispatcher.go`, `outbox_integration_test.go`
  - `apps/api/internal/platform/middleware/host_context.go` (chỉ comment)
  - `apps/api/internal/app/sandbox_reset_integration_test.go`
  - `apps/api/api/openapi.yaml`
- Migration mới: `apps/api/migrations/000036_auth_tokens_user_index.{up,down}.sql`
- Web: `apps/web/src/app/layouts/util-bar.tsx`, `app-shell.test.tsx`, `src/shared/api/schema.d.ts` (sinh lại)
- Plan: `phase-04-identity-organization.md`, `phase-13-saas-control-settings.md`, `design/database-schema-org-partners.md`

Test regression mới hoặc mở rộng:

- `TestDispatchSkipsReplacedSandboxes` (outbox): dispatcher không xử lý event của tenant replaced, event đó vẫn pending.
- `TestPurgeIdempotencySkipsReplacedSandboxes` (cli).
- `TestSandboxResetReplacesTheTenant` (org) kiểm thêm:
  - 429 kèm `RetryAfter` đúng 8 phút;
  - event đang chờ bị đóng kèm lý do, và `closed_events: 1` trong audit;
  - retry ngay sau reset nhận 429;
  - CHECK `ck_tenants__replaced_sandbox`.
- `TestSandboxResetProvisionsANewTenant` (app) kiểm thêm: 429 với `Retry-After`, `reauth_required` rồi reauth thành 200, retry nhận 429, và store thật cần reauth trước khi nhận `not_sandbox`.

Kiểm tra lại sau đợt ba:

| Lệnh | Kết quả |
|---|---|
| `gofmt -l .` | không có file |
| `go vet ./...` / `go vet -tags integration ./...` | sạch |
| `golangci-lint run ./...` | `0 issues.` |
| `go test ./internal/org/... ./internal/identity/... ./internal/app/... ./internal/cli/... ./internal/platform/...` | ok |
| `go test -tags integration` cùng tập package trên | ok: org 7.5s, app 15.4s, cli 8.4s, db 7.0s, outbox 7.5s, các package platform còn lại ok |
| `go test -tags integration ./migrations/ -run Reversib` | ok, có 000036 |
| `go test ./migrations/` (lint) | FAIL vì "version 000037 is missing". Lỗi này đã có từ trước, do `000040_procurement_receipt_line_lot` của agent procurement; 000036 của tôi lấp một khoảng trống. |
| web: `npm run typecheck` | sạch |
| web: `npx vitest run src/app/layouts/app-shell.test.tsx` | 6/6 pass |
| web: `npm test` | 222 pass, 9 fail. Cả 9 đều ở `features/auth` và `shared/api/http.test.ts` (Retry-After, auth schemas, login page). Với `schema.d.ts` cũ, kết quả vẫn là đúng 9 fail đó, nên chúng có từ trước và không do thay đổi này. |

### Đợt ba, bổ sung: sửa race login và reset (L1); index `auth_tokens` (L4)

Lead yêu cầu sửa hẳn L1 thay vì chỉ ghi lại. Mục L1 ở trên ("không sửa") không còn đúng nữa.

- **Đã chứng minh nguyên nhân.** Test mới `TestLoginRacingASandboxReset` (app) dừng login ngay sau khi đọc tài khoản của tenant cũ, bằng một hasher có thể tạm dừng. Trong lúc đó test chạy reset thật, rồi cho login chạy tiếp. Trước khi sửa, login trả **500**: insert session `(old,u)` vi phạm FK vì tài khoản đã chuyển sang tenant mới.
- **Cách sửa ở identity: một thứ tự khoá cố định.**
  - Thêm `lockTenant` trong `identity/repo.go`, chạy `SELECT status = 'replaced' FROM tenants WHERE id = $1 FOR KEY SHARE`. Đây là câu lệnh đầu tiên của mọi transaction thay đổi tài khoản, session hoặc token: `startSession` (gồm cả login và MFA), `RequestPasswordReset`, `VerifyEmail`, `ResetPassword`, `ChangePassword`, `ConfirmTOTP`, `DisableTOTP`, yêu cầu và chấp nhận chuyển chủ, `RevokeSession`, `RevokeOtherSessions`, `RevokeTenantSessions`.
  - Reset giữ `FOR UPDATE` trên dòng tenant, xung đột với `KEY SHARE`, nên hai bên khoá tenant trước rồi mới khoá dòng tài khoản. Do đó không còn deadlock:
    - thao tác khoá trước sẽ commit trước, và reset chuyển luôn session hoặc token nó vừa tạo;
    - thao tác phải chờ sẽ thấy `replaced` và trả **409 `sandbox_replaced`** ("The demo store was just reset; sign in again"). Đăng nhập lại sẽ vào sandbox mới.
  - Tenant không tồn tại thì không khoá gì, giữ nguyên câu trả lời cũ (`token_invalid`, ...).
  - `lockTenant` khoá trước khi tra e-mail, nên với `RequestPasswordReset` mã 409 không tiết lộ e-mail nào tồn tại.
- **Phía reset.** Một writer khoá theo thứ tự ngược (dòng tài khoản trước, tenant sau) vẫn có thể biến reset thành nạn nhân deadlock (40P01). Khi đó `ResetSandbox` chạy lại một lần sau 10–50 ms ngẫu nhiên, theo cùng mẫu với `posting.Coordinator`. Nếu lần chạy lại vẫn lỗi như vậy, reset trả **409 `sandbox_busy`** thay vì 500. Thân reset chuyển sang `resetSandbox`, và `addressMoved` chỉ chạy sau khi reset thành công.
- **Test regression:**
  - `TestLoginRacingASandboxReset` (app): reset trả 200, login đang chạy dở trả 409 `sandbox_replaced`, tenant cũ không còn session nào, và đăng nhập lại vào sandbox mới. Trước khi sửa, login trả 500.
  - `TestSandboxResetSerialisesWithAccountChanges` (org), gồm hai trường hợp:
    1. Một transaction khoá `KEY SHARE` tenant trước (như login), chờ reset bị chặn rồi mới insert session. Reset thành công và session đó được chuyển: cũ/mới = 0/1.
    2. Một transaction khoá dòng `users` trước, chờ reset bị chặn ở `UPDATE users`, rồi xin `KEY SHARE` tenant, tạo deadlock. PostgreSQL huỷ reset vì reset chờ trước, sau đó reset chạy lại và thành công. Khi tạm tắt nhánh retry, test fail với `sandbox_busy`; điều này xác nhận test thực sự đi qua đường retry.
- **Contract.**
  - `openapi.yaml` khai báo 409 `sandbox_replaced` cho `/auth/login`, `/auth/verify`, `/auth/password-reset`, `/auth/password-reset/confirm`, `/auth/mfa/totp/verify`, `DELETE /auth/mfa/totp`, `/auth/password`, `/sessions/{id}`, `/sessions:revoke-all`, `/owner-transfer` và `/owner-transfer/accept`.
  - Route reset khai báo thêm `sandbox_busy`.
  - `schema.d.ts` đã được sinh lại. Dòng ORG-06 trong `phase-04` ghi thứ tự khoá và hai mã lỗi mới.
- **Index `auth_tokens` (điểm 2 của lead).** Đã làm trong đợt ba: `000036_auth_tokens_user_index` tạo `ix_auth_tokens__user (tenant_id, user_id)`, cho cả `UPDATE auth_tokens ... WHERE tenant_id` của reset lẫn việc kiểm FK `fk_auth_tokens__user_id`. Test reversibility pass.
- **File sửa thêm:**
  - `apps/api/internal/identity/repo.go`, `errors.go`, `service.go`, `mfa.go`, `ownership.go`, `sessions.go`
  - `apps/api/internal/org/sandbox.go`, `org_integration_test.go`
  - `apps/api/internal/app/sandbox_reset_integration_test.go`
  - `apps/api/api/openapi.yaml`
  - `apps/web/src/shared/api/schema.d.ts` (sinh lại)
  - `plans/.../phase-04-identity-organization.md`
- **Kiểm tra lại:**
  - gofmt sạch; `go vet` sạch với cả hai tag; golangci-lint `0 issues`.
  - `go test`, có và không có `-tags integration`, cho org, identity, app, cli và platform/...: ok (org 7.7s, app 15.9s, cli 8.1s).
  - Migration reversibility: ok.
  - Web: `npm run typecheck` sạch; `app-shell.test.tsx` 6/6.
- **Đối chiếu với yêu cầu nhắc lại của lead** (tin nhắn này đến sau khi phần sửa trên đã xong):
  - Lỗi được map sang 409 với mã rõ ràng. `sandbox_replaced` cho request chạy trùng reset; `sandbox_busy` cho reset vẫn thua deadlock sau một lần chạy lại. Tên mã theo convention `sandbox_*` đang có.
  - Deadlock được tránh bằng thứ tự khoá cố định, không chỉ bắt lỗi: tenant trước, dòng tài khoản sau, ở cả identity lẫn reset. 40P01/40001 chỉ còn là đường dự phòng phía reset (chạy lại một lần, rồi 409).
  - Test tái hiện ổn định nhờ dùng điểm dừng tất định (hasher tạm dừng, chờ `pg_stat_activity` báo đang chờ khoá) thay vì đua thời gian ngẫu nhiên. Chạy `-count=5` cho `TestLoginRacingASandboxReset` và `TestSandboxResetSerialisesWithAccountChanges` đều pass.
  - Theo lead, khoảng trống migration 000037–000039 và 9 test web sẽ do người khác xử lý, nên không còn là concern của báo cáo này.
- **Còn lại:**
  - Web chưa có câu tiếng Việt cho `sandbox_replaced`. `authErrorMessage` rơi về message chung của API. Mã này nên được thêm vào `features/auth/lib/auth-errors.ts`, nhưng file đó thuộc phase web khác nên tôi không sửa.
  - Race nạp và purge host cache (L3b) vẫn chỉ được ghi lại.


## Đợt bốn: sửa theo review vòng 3 (M6, L8 đến L11)

- **M6, gom khoá tenant vào một helper:**
  - `identity.Service.withAccountTx(ctx, tenantID, fn)` nằm trong `internal/identity/repo.go`. Helper mở `WithTenantTx` và gọi `lockTenant` ở câu lệnh đầu tiên. `lockTenant` không còn được gọi trực tiếp ở đâu khác.
  - 12 call site cũ đã chuyển sang helper. Thêm vào đó: cả ba tx của `Reauth` (đọc account, nhánh sai mật khẩu, xoay token), `EnrolTOTP` (trước dùng `WithPlatformTx`) và tx tạo session của `VerifyChallenge`.
  - Comment của helper liệt kê đúng ba ngoại lệ và lý do:
    - Touch của `ResolveSession` và `Logout` chỉ sửa một dòng session theo `id`, không bao giờ chờ dòng tenant.
    - `CreateTenantWithOwner` ghi account của tenant do chính nó tạo.
    - `loginFailed` chỉ ghi `login_attempts` (không có FK) và audit, không ghi dòng account nào. Sai mật khẩu phải luôn ra `invalid_credentials`.
  - Comment ở `org/sandbox.go` đã sửa cho khớp: các ngoại lệ không bao giờ vừa giữ dòng account vừa chờ dòng tenant, nên không tạo được chu trình khoá với reset.
  - **Test bảng tất định** `TestAccountChangesRefuseAReplacedSandbox` (`internal/app/sandbox_reset_integration_test.go`):
    - Chuẩn bị session, token reset, token chuyển quyền, một owner có MFA và một challenge. Sau đó đặt `is_sandbox = true, status = 'replaced'` qua SuperDB.
    - Gọi 17 thao tác theo thứ tự cố định: verify e-mail, xin và đặt lại mật khẩu, login, bước MFA bằng TOTP và bằng mã khôi phục, reauth đúng và sai mật khẩu, đổi mật khẩu, enrol/confirm/disable TOTP, xin và nhận chuyển quyền, ba loại revoke. Thao tác nào cũng phải trả `sandbox_replaced`.
    - Tiếp đó kiểm: login sai mật khẩu vẫn trả `invalid_credentials`; không session nào bị tạo hay revoke; logout vẫn đóng được session.
    - Cuối cùng đưa tenant về `active` rồi dùng lại đúng token reset, mã TOTP và mã khôi phục cũ, tất cả đều thành công. Điều này chứng minh không có gì bị tiêu mất.
    - Thử đột biến: đổi tx của `ChangePassword` về `WithTenantTx` thì test fail ngay ("change password = <nil>, want sandbox_replaced"). Sau đó đã khôi phục.
- **L8, Logout:**
  - Logout giờ revoke theo `id` và `user_id`, không lọc `tenant_id`. Session mà reset đã chuyển sang tenant mới vẫn bị đóng. Session đã đóng sẵn thì không cần làm gì, và logout vẫn trả 200.
  - Thứ tự khoá vẫn an toàn: logout chỉ khoá một dòng session. Nếu reset đang giữ dòng đó, logout chờ reset commit rồi đánh giá lại `WHERE id`.
  - Test `TestSessionCallsAfterASandboxResetMovedTheSession`:
    - Lấy principal trước khi reset, rồi chạy reset thật qua HTTP và kiểm session đã chuyển tenant.
    - Gọi `Logout(p)`, kiểm session đã bị revoke, và `GET /me` bằng cookie đó trả 401.
- **L9, Reauth:** cả ba tx đều qua helper. Principal cũ sau khi reset nhận 409 `sandbox_replaced`, không còn 401 như trước; test nằm trong cùng test với L8.
  - Đã thêm 409 vào `/auth/reauth` trong `api/openapi.yaml`.
  - **Cần sinh lại `apps/web/src/shared/api/schema.d.ts` (`npm run gen:api`).** Tôi không động vào apps/web theo chỉ đạo.
- **L10, VerifyChallenge:**
  - `startSession` nhận thêm tham số `admit func(q, now) error`. Hàm này chạy trong cùng tx tạo session, ngay sau `lockTenant`. Login truyền `nil`.
  - `VerifyChallenge` tiêu bước TOTP hoặc mã khôi phục trong `admit` (tách thành `useSecondFactor`). Mã sai trả sentinel `errSecondFactorRejected` để rollback, rồi đi tiếp qua `loginFailed` như cũ.
  - Kết quả: trên tenant `replaced`, request trả 409 trước khi tiêu mã. Mọi lỗi khác của `startSession` cũng không còn làm mất mã.
  - Hành vi "mỗi mã chỉ dùng một lần" giữ nguyên: update có điều kiện vẫn khoá dòng, hai sign-in đồng thời vẫn chỉ một bên thắng. `TestTOTPAndRecoveryCodes` pass.
- **L11, nhánh `sandbox_busy`:**
  - Thêm subtest thứ ba vào `TestSandboxResetSerialisesWithAccountChanges`.
  - Writer giữ users `FOR NO KEY UPDATE`. Ở mỗi vòng, writer đặt `SAVEPOINT`, lấy `KEY SHARE` trên tenant để gây deadlock, rồi `ROLLBACK TO SAVEPOINT` để nhả riêng khoá tenant. Khoá users được giữ, nên lần chạy lại của reset lại chờ ở `UPDATE users`.
  - Sau hai vòng, reset trả `sandbox_busy`. Tenant vẫn `active` và account vẫn ở tenant cũ.
  - Nếu bỏ retry, vòng 2 sẽ timeout ở `awaitLockWait`, nên test cũng chứng minh reset chạy đúng hai lần. Subtest mất khoảng 2,5 giây.
- **EnrolTOTP và loginFailed:**
  - EnrolTOTP ghi `mfa_totp_secrets`, nên đã qua helper.
  - `loginFailed` không ghi bảng tài khoản, nên được miễn có chủ đích. Test bảng kiểm rằng login sai mật khẩu trên tenant `replaced` vẫn trả `invalid_credentials`: 409 chỉ xuất hiện sau khi mật khẩu đúng.
- **Plan:**
  - Phase 04, dòng ORG-06: ghi rõ helper, việc request không tiêu mã, và logout theo id.
  - Phase 13: writer mới cho bảng tài khoản (support consent, mời nhân viên, đổi vai trò, sửa profile) phải đi qua `withAccountTx` và thêm một dòng vào test bảng.
- **File thay đổi trong đợt này:**
  - `apps/api/internal/identity/repo.go`, `service.go`, `mfa.go`, `ownership.go`, `sessions.go`
  - `apps/api/internal/org/sandbox.go` (chỉ comment)
  - `apps/api/internal/org/org_integration_test.go`
  - `apps/api/internal/app/sandbox_reset_integration_test.go`
  - `apps/api/api/openapi.yaml`
  - `plans/.../phase-04-identity-organization.md`, `plans/.../phase-13-saas-control-settings.md`
- **Gate (apps/api):**
  - gofmt sạch.
  - `go vet ./...` sạch; `go vet -tags integration` cho org, identity, app sạch.
  - `golangci-lint run`, có và không có `--build-tags integration`, cho org, identity, app: `0 issues`.
  - `go test -count=1` cho org, identity, app: ok.
  - `go test -tags integration -count=1` cho org, identity, app: ok (org 9.4s, identity 0.0s, app 18.5s).
  - `-count=5` cho `TestSandboxResetSerialisesWithAccountChanges` (org) và cho `TestAccountChangesRefuseAReplacedSandbox`, `TestSessionCallsAfterASandboxResetMovedTheSession`, `TestLoginRacingASandboxReset` (app): đều ok.

Status: DONE_WITH_CONCERNS
Summary: Đã sửa M6 và L8 đến L11 theo review vòng 3.
- Mọi tx ghi bảng tài khoản đi qua `withAccountTx`, helper khoá tenant trước. Ba ngoại lệ được ghi rõ lý do.
- Test bảng tất định phủ 17 thao tác. Logout đóng được session đã bị chuyển tenant. Reauth trả 409.
- VerifyChallenge không còn tiêu mã khi kết quả là 409. Nhánh `sandbox_busy` đã có test.
Concerns/Blockers:
- Cần chạy `npm run gen:api` ở apps/web để đưa 409 mới của `/auth/reauth` vào `schema.d.ts`. Phần web do lead làm.
- Còn mở từ trước: race nạp/purge host cache (L3b) mới chỉ được ghi lại. "`replaced` là trạng thái cuối" cần trigger, mà trigger bị cấm.

Không có blocker.
