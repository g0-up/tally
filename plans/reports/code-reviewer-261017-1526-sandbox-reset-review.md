# Review: sandbox reset tạo tenant mới (ORG-06, `POST /api/v1/tenant/sandbox/reset`)

## Phạm vi

- Code chính:
  - `apps/api/internal/org/sandbox.go`, `provisioner.go`, `http.go`, `service.go`, `model.go`
  - `apps/api/migrations/000034_tenant_replaced_status.{up,down}.sql`
  - `apps/api/internal/identity/sessions.go`, `apps/api/internal/platform/db/tenants.go`
  - `apps/api/internal/cli/worker.go`, `apps/api/internal/cli/serve.go`
  - `apps/api/api/openapi.yaml`
- Test:
  - `apps/api/internal/app/sandbox_reset_integration_test.go`
  - `TestSandboxResetReplacesTheTenant` trong `internal/org/org_integration_test.go`
- Đối chiếu thêm:
  - middleware `tenant.go`, `host_context.go`, `reauth.go`, `support_readonly.go`
  - outbox dispatcher; `identity/service.go` (login và `startSession`); `identity/mfa.go`
  - DDL của `000001` và `000025`
  - web: `util-bar.tsx`, `schema.d.ts`
- Quyết định đã chốt (plan.md:127): reset tạo tenant mới và vô hiệu hoá tenant cũ; không xoá, không có role purge, không cascade. Review này không đề xuất đảo ngược quyết định đó.

## Kết quả kiểm tra (chạy trong apps/api)

| Lệnh | Kết quả |
|---|---|
| `go vet ./...` | sạch (exit 0) |
| `go vet -tags integration` trên org, app, identity, cash, pricing, catalog, partner, cli, platform | sạch |
| `golangci-lint run ./...` | `0 issues.` |
| `go test -count=1 -tags integration ./internal/org/... ./internal/identity/... ./internal/app/... ./internal/platform/db/... ./internal/cli/...` | đều ok: org 6.3s, identity, app 12.9s, db 5.2s, cli 7.0s |
| `go test -tags integration ./migrations/ -run Reversib` | PASS, có gồm cả 000034 |
| `go test -tags integration ./migrations/` | FAIL, nhưng ngoài phạm vi review (xem dưới) |

Hai lỗi của `./migrations/` không do thay đổi này gây ra:

- `TestMigrationsLint` báo "version 000036 is missing": agent procurement vừa thêm `000040_procurement_receipt_line_lot`.
- `TestEveryTenantTableIsIsolated/receipt_lines` vi phạm `ck_receipt_lines__lot_id_condition`: cũng thuộc phần việc của procurement.

Mọi integration test đều chạy bằng testcontainers. Review không đụng `tally-postgres-1`.

## Trả lời nhanh các câu hỏi của lead

| Câu hỏi | Kết luận |
|---|---|
| Có chiếm được phiên, dữ liệu hay slug qua reset không? | **Không tìm thấy đường nào.** Chi tiết bên dưới. |
| Đổi `app.tenant_id` giữa transaction có giữ đúng RLS không? | **Đúng.** Không có chỗ ghi nhầm tenant. Chi tiết bên dưới. |
| Thao tác có atomic, có an toàn khi hai reset chạy đồng thời không? Có yêu cầu re-auth không? | Atomic: một transaction, purge cache chạy sau commit. Hai reset đồng thời được tuần tự hoá bằng `FOR UPDATE`, request đến sau nhận 409. Re-auth được gắn ở route (`http.go:46`) nhưng **không có test nào kiểm** (M3). Reset chạy đồng thời với login có thể trả 500 (L1). |
| Còn đường đọc/ghi vào tenant `replaced` không? | Host, slug và token: đã đóng. **Ghi vẫn còn:** request đang chạy dở (M1), handler outbox chưa được bọc (M2), job xoá idempotency (có chủ đích), CLI vận hành theo id. |
| Migration và CHECK có đúng không? Down có an toàn không? | Đúng. Down thất bại ồn ào khi còn tenant `replaced`, không mất dữ liệu (L6). |
| `SandboxResetter` cũ đã gỡ hết chưa? | Hết. `grep` không còn `SandboxResetter`, `SandboxResetters`, `unlockSandboxPeriods`, `sandboxResetReason`, `cash/reset.go` hay `Domains.clock`. |
| Test có thật sự kiểm các điểm trên không? | Kiểm tốt luồng chính và tính bất biến của sổ. **Thiếu:** test re-auth, test đồng thời thật, test CHECK constraint, test request đang chạy dở. |

### Vì sao không chiếm được phiên, dữ liệu hay slug

- Slug mới luôn lấy từ `old.Slug` của chính sandbox. Chỉ sandbox mới vào được nhánh reset, vì `!old.IsSandbox` thì trả 403 (`sandbox.go:47`).
- Slug của store thật không thể kết thúc bằng `-demo` (`slug.go:61`).
- `reset-<32 hex>` dài 38 ký tự, vẫn hợp regex `ck_tenants__slug` (tối đa 40) nhưng dài hơn mọi slug đăng ký (tối đa 30).
- Middleware `Tenant()` buộc tenant của phiên phải trùng tenant của host, nên chủ store thật không gọi reset được trên host demo, và ngược lại.
- Support bị `SupportReadOnly` chặn ở POST.
- Chỉ phiên của chính sandbox được chuyển: `moveAccount` lọc `WHERE tenant_id = old`.
- `ResolveSlug` (`sessions.go:78`) và `ResolveSession` (`sessions.go:46`) đều loại tenant `replaced`, làm lớp phòng thủ thứ hai.
- Challenge MFA ký `(old tenant, user)`. Sau reset, `userByID(old, user)` không tìm thấy user nên challenge bị từ chối, tức fail-closed.

### Vì sao RLS vẫn đúng khi đổi `app.tenant_id`

`tenants`, `users`, `sessions` và `auth_tokens` không chịu RLS (`000001:3-5`). 116 bảng tenant dùng cùng một policy `USING`/`WITH CHECK` trên `current_setting('app.tenant_id')`, và giá trị này được đọc lúc thực thi. Thứ tự ghi trong transaction:

1. `tenant.replace` được ghi trong tenant cũ.
2. `set_config(..., true)`: có hiệu lực cục bộ trong transaction, rollback sẽ khôi phục.
3. `Provision` và seeder ghi bằng `sc.TenantID = new` khi tenant hiện tại đã là tenant mới. `inventory/ports.go:51` cũng đọc lại `current_setting`, nên đi theo tenant mới.
4. `tenant.sandbox_reset` được ghi trong tenant mới.

Không có chỗ nào ghi nhầm tenant.

---

## Findings

### Critical

Không có.

### High

Không có.

### Medium

**M1. Request đang chạy dở vẫn ghi được vào tenant `replaced`, trái với invariant mà comment khẳng định**

- Vị trí:
  - `internal/org/sandbox.go:43,67-71`
  - `internal/platform/db/tenants.go:24-27`, comment "a replaced sandbox never changes again"
  - `internal/cli/worker.go:126-128`, comment "stays exactly as the reset left it"
- Kịch bản:
  1. Request A, ví dụ `POST /payments`, resolve session và host thành tenant cũ trước khi reset commit.
  2. Reset lấy khoá `FOR UPDATE` trên dòng `tenants(old)`. Việc đổi `slug`, một cột có unique index, cũng cần đúng mức khoá này.
  3. Mọi INSERT có FK tới `tenants(id)` của A, ví dụ `audit_events` hay `idempotency_keys`, phải chờ khoá `FOR KEY SHARE`.
  4. Reset commit. Khoá được nhả, `id` không đổi nên kiểm FK của A vẫn đạt.
  5. A commit chứng từ, sổ cái và outbox event vào tenant đã `replaced`.
- Hậu quả:
  - Không có rò rỉ chéo tenant.
  - Tuy nhiên tenant cũ vẫn thay đổi sau reset. Lúc đó user không còn thấy dữ liệu này, nhưng vẫn nhận 201.
  - Outbox event sinh ra sẽ chạy handler chưa bọc (xem M2).
- Mức độ: đây là suy luận từ ngữ nghĩa khoá của PostgreSQL ở READ COMMITTED, chưa chạy thực nghiệm. Cửa sổ lỗi bằng thời gian một request.
- Hướng sửa, chọn một:
  - (a) Thêm guard trong `setTenant` của `WithTenantTx`, chỉ cho transaction ghi:

    ```sql
    SELECT 1 FROM tenants WHERE id = $1 AND status <> 'replaced' FOR KEY SHARE
    ```

    Guard này chờ reset xong rồi từ chối bằng 401 hoặc 409. Dispatcher outbox và job xoá idempotency cần một biến thể bỏ qua guard.
  - (b) Nếu chấp nhận rủi ro: sửa hai comment ở trên thành "không còn host, phiên hay job tạo dữ liệu nào trỏ tới". Đừng hứa một invariant mà code không đảm bảo.

**M2. `skipReplaced` là opt-in; mọi subscriber outbox mới mặc định vẫn chạy cho tenant `replaced`**

- Vị trí: `internal/cli/worker.go:90,129-140`; `internal/platform/outbox/dispatcher.go:129` (`TenantIDs`).
- Kịch bản: hiện chỉ `HandleRecostSuggested` được bọc. Khi phase 07, 09 hoặc 13 subscribe thêm, chẳng hạn notification, email nhắc nợ hay tín hiệu, nếu quên `skipReplaced` thì:
  - handler chạy và ghi dữ liệu vào tenant `replaced`;
  - hoặc gửi email về một sandbox đã bị thay.
- Cơ chế bảo vệ hiện chỉ là tin nhắn gửi cho agent khác, không phải ràng buộc trong code.
- Hướng sửa:
  - Đảo mặc định: `Dispatcher` tự đánh dấu processed các event của tenant `replaced` mà không gọi handler, trừ handler đăng ký rõ là "finish-only".
  - Hoặc trong `ResetSandbox`, đánh dấu processed mọi event đang chờ của tenant cũ trong cùng transaction. Sau đó dispatcher có thể dùng `ActiveTenantIDs`, đồng thời giải quyết luôn M4.
  - Thêm test cấp dispatcher cho một topic không bọc.

**M3. Không có test nào chứng minh reset đòi re-auth**

- Vị trí: `internal/org/http.go:46`; các test reset trong `internal/app/sandbox_reset_integration_test.go`.
- Kịch bản:
  - `signIn` đặt `reauthenticated_at = now` (`identity/service.go`, `startSession`), và `testkit.Clock` là clock cố định, nên reset luôn rơi vào trong cửa sổ 5 phút.
  - Nếu ai đó bỏ `r.Reauth` khỏi route, không test nào fail.
  - Endpoint debt/cash có test này (`debt_cash_integration_test.go:230-233`); endpoint reset thì không.
- Hướng sửa: theo mẫu `debt_cash_integration_test.go:230-233`, thêm vào `TestSandboxResetProvisionsANewTenant`:
  1. `clk.Advance(middleware.ReauthWindow + time.Minute)`.
  2. Gọi reset, mong đợi `403 reauth_required`, và kiểm tenant không đổi.
  3. Gọi `/auth/reauth` rồi reset, mong đợi 200.

**M4. Số tenant tăng không giới hạn, và tải của worker tăng tuyến tính với số lần reset**

- Vị trí:
  - `internal/org/sandbox.go:39-109`: không có giới hạn tần suất.
  - `outbox/dispatcher.go:129`: duyệt `TenantIDs` mỗi 2 giây.
  - `worker.go:160`: `purgeIdempotency`.
- Kịch bản:
  - `reauthenticated_at` đi theo phiên được chuyển, nên sau một lần re-auth chủ sandbox có thể reset liên tục trong 5 phút.
  - Giới hạn duy nhất là 20 req/s theo IP (`app.go:32`).
  - Mỗi lần reset tạo vĩnh viễn một tenant đầy đủ: store, kho, seed và sample.
  - Dispatcher mở ít nhất một transaction (begin, set_config, select, select, commit) cho **mỗi** tenant, kể cả tenant `replaced`, mỗi 2 giây, mãi mãi.
  - Nếu tài khoản demo được chia sẻ, ví dụ demo công khai cho khách tiềm năng, đây là một đường khuếch đại tải hoặc DoS lưu trữ.
- Hướng sửa:
  - Giới hạn tần suất trong service: từ chối khi `now - old.CreatedAt < N phút`, trả 409 `sandbox_reset_too_soon` hoặc 429.
  - Dispatcher bỏ tenant `replaced` sau khi đã drain, như hướng sửa ở M2.
  - Cân nhắc giới hạn số sandbox `replaced` trên mỗi parent. Đây là câu hỏi sản phẩm (Q1).

**M5. UI nói dữ liệu demo "sẽ mất" trong khi backend giữ vĩnh viễn và người dùng không còn cách truy cập hay xoá**

- Vị trí:
  - `apps/web/src/app/layouts/util-bar.tsx:89`: "Mọi chứng từ bạn tạo trong bản demo này sẽ mất."
  - Mô tả trong `openapi.yaml:671-676`.
- Kịch bản:
  - Người dùng thử nhập khách hàng thật (tên, số điện thoại) vào demo, bấm "Đặt lại" vì tin dữ liệu bị xoá.
  - Thực tế dữ liệu nằm lại trong tenant `replaced`, không export được, không xoá được.
  - Đây là vấn đề minh bạch về dữ liệu cá nhân (Nghị định 13/2023), không phải lỗi logic.
- Quyết định "không xoá" là của chủ sản phẩm nên không đảo ngược ở đây.
- Hướng sửa:
  - Sửa copy UI, ví dụ "…sẽ không còn hiển thị (dữ liệu cũ được lưu trữ, không truy cập được)".
  - Đưa việc xử lý PII trong sandbox `replaced` vào phase 13: retention, hoặc purge khi đóng store cha. Xem Q2.

### Low

**L1. Reset chạy đồng thời với login hoặc tạo token có thể trả 500, kể cả deadlock**

- Vị trí: `sandbox.go:117-132`; `identity/service.go`, `startSession` (insert session, `UPDATE users`, ghi audit).
- Kịch bản theo thứ tự khoá ngược nhau:
  - Login giữ khoá dòng `users(old,u)`, rồi ghi audit, việc này cần `KEY SHARE` trên `tenants(old)`.
  - Reset đã giữ `FOR UPDATE` trên `tenants(old)` và đang chờ `UPDATE users`.
  - Kết quả là deadlock, PostgreSQL huỷ một bên với lỗi 40P01.
- Các biến thể khác:
  - Login commit sau snapshot của câu CTE: session mới không được chuyển, kiểm FK cuối câu lệnh thấy `sessions(old,u)`, gây lỗi 23503 và reset rollback.
  - Login chạy sau khi reset commit: insert session `(old,u)` vi phạm FK.
- Tất cả đều fail-closed, không hỏng dữ liệu, nhưng người dùng thấy 500. Đây là suy luận, chưa chạy thực nghiệm.
- Hướng sửa:
  - Trong reset, khoá `users` của tenant cũ (`SELECT … FOR UPDATE`) **trước** khi khoá `tenants`, hoặc ngay sau, theo một thứ tự cố định.
  - Map 23503 và 40P01 của reset sang 409 hoặc 503 kèm `Retry-After`.

**L2. Server bỏ qua `Idempotency-Key` của web, nên retry có thể gây reset hai lần**

- Vị trí: `apps/web/src/app/layouts/util-bar.tsx:79` có gửi `idempotencyKey`; `org.ResetSandbox` không dùng guard idempotency.
- Kịch bản: mất mạng sau khi server đã commit, client gửi lại cùng key. Phiên lúc này đã thuộc tenant mới, nên request reset tiếp tenant vừa tạo và sinh thêm một tenant `replaced`.
- Hướng sửa, chọn một:
  - Lưu kết quả theo key trong tenant mới.
  - Hoặc nhận một trường `expected_tenant_id` / `If-Match` theo id tenant hiện tại, khớp thì mới reset.

**L3. Host cache: hai kẽ hở cùng gốc "slug bất biến"**

- Vị trí: `middleware/host_context.go:30-31,66-74`; `cli/serve.go:84-90`.
- (a) Nhiều replica: replica khác giữ positive entry trỏ tới tenant cũ **vĩnh viễn**, vì không có TTL, nên host demo trả 401 cho tới khi restart. Rủi ro này đã ghi ở phase 21. Nếu triển khai một replica thì chấp nhận được.
- (b) Race giữa nạp cache và purge:
  - Một lần cache miss đọc DB trước khi reset commit, nhưng ghi cache sau khi `Purge` đã chạy, để lại entry cũ vĩnh viễn ngay cả với một replica.
  - Khả năng thấp, vì slug thường đã có sẵn trong cache trước reset.
  - Hướng sửa: dùng bộ đếm thế hệ (generation counter) khi purge, hoặc TTL ngắn cho positive entry.
- Comment "Slugs are immutable, so a resolved slug is cached forever" giờ đã sai. Cần sửa comment này và comment "immutable" ở `000001_platform_identity.up.sql:9`.

**L4. `auth_tokens` không có index theo `(tenant_id, user_id)`**

- Vị trí: `000001_platform_identity.up.sql:60-68`; câu `moveAccount`.
- Mỗi lần reset, `UPDATE auth_tokens WHERE tenant_id = $1` cùng việc kiểm RI khi khoá `users(tenant_id,id)` đổi đều quét toàn bảng `auth_tokens` toàn cục.
- Bảng nhỏ thì chưa đáng lo. Nên thêm `ix_auth_tokens__user (tenant_id, user_id)` khi có migration identity tiếp theo.

**L5. Lỗi 409 `sandbox_not_active` dùng cùng thông điệp cho mọi trạng thái khác `active`**

- Vị trí: `sandbox.go:50-53`.
- Sandbox ở trạng thái `read_only` hoặc `closing` cũng nhận thông điệp "The demo store was already reset", gây hiểu nhầm.
- Hướng sửa: tách riêng trường hợp `replaced` với các trạng thái còn lại.

**L6. Migration 000034**

- Up: đúng.
  - Có `ck_tenants__status_kinds`, gồm `replaced`.
  - Có `ck_tenants__replaced_sandbox (status <> 'replaced' OR is_sandbox)`.
  - `ADD CONSTRAINT` kiểm lại toàn bảng dưới khoá ACCESS EXCLUSIVE. Bảng `tenants` nhỏ nên ổn.
- Down: đúng thứ tự, và thất bại ồn ào khi còn dòng `replaced`. Không mất dữ liệu, test reversibility vẫn PASS.
- Lưu ý vận hành: sau lần reset đầu tiên trên production, down của 000034 không chạy được nữa. Nên ghi một dòng vào `docs/runbook.md`, phần rollback.
- Thiếu test âm: ví dụ `UPDATE` một store thật sang `replaced` phải vi phạm `ck_tenants__replaced_sandbox`.
- Ngoài ra, chưa có ràng buộc "`replaced` là trạng thái cuối". Phase 13 (admin) có thể vô tình đưa tenant `replaced` về `active`; một trigger chặn chuyển trạng thái ra khỏi `replaced` sẽ đóng hẳn kẽ hở này.

**L7. Tài liệu và contract bị lệch**

- `plans/.../design/database-schema-org-partners.md:18` vẫn ghi CHECK status cũ, chưa có `replaced`, và vẫn mô tả slug là bất biến.
- `apps/web/src/shared/api/schema.d.ts:2958` vẫn ghi `resetSandbox` trả `204`, trong khi OpenAPI đã đổi sang `200 Tenant`. Cần chạy lại `npm run gen:api`.
- `ResolveSession` nay chặn cả tenant `closed`. Thay đổi này hợp lý, nhưng là thay đổi hành vi với store thật nằm ngoài phạm vi reset. Xem Q3.

---

## Đánh giá test

| Điểm cần chứng minh | Đã có test? |
|---|---|
| Sổ append-only của tenant cũ giữ nguyên số dòng; verifier sạch cho 3 sandbox và store thật | Có (`sandbox_reset_integration_test.go:169-260`) |
| Slug cũ không resolve; tenant cũ không còn user hay session | Có |
| Phiên và CSRF cũ dùng được trên tenant mới; login lại vào tenant mới | Có |
| Wiring purge host cache | Có. Implementer cho biết đã thử bỏ `AddressMoved` và test fail |
| Store thật bị 403 `not_sandbox` | Có |
| Actor cũ sau reset nhận 409 | Có ở cấp service (`org_integration_test.go:457`), nhưng chạy tuần tự, không đồng thời thật |
| Re-auth bắt buộc | **Không** (M3) |
| Hai reset đồng thời: đúng một cái thành công | **Không**. Nên chạy 2 goroutine cùng gọi `ResetSandbox` và kiểm có đúng một kết quả 200 và một 409 |
| Request ghi đang chạy dở hoặc subscriber outbox không ghi vào tenant `replaced` | **Không** (M1, M2) |
| CHECK `ck_tenants__replaced_sandbox` | **Không** (L6) |

## Plan follow-up

- Phase 04: dòng ORG-06 và todo dòng 109 khớp với code. Có thể đánh dấu xong sau khi xử lý M3; M1, M2 và M4 nên có quyết định trước khi đóng phase.
- Phase 13: cần xử lý `support_sessions` (`tenant_id` cũ, trong khi `consent_token_id` đã chuyển sang tenant mới), màn admin lọc `replaced`, và PII của sandbox `replaced`.
- Phase 21: rủi ro host cache nhiều replica đã được ghi. Nên bổ sung race nạp/purge ở L3(b).

## Câu hỏi mở

1. **Q1.** Có giới hạn số lần reset không, theo thời gian hoặc theo số sandbox `replaced` trên mỗi parent? Câu này quyết định cách sửa M4.
2. **Q2.** PII người dùng nhập vào sandbox rồi bấm reset được giữ bao lâu? Khi store cha đóng hoặc bị purge, các sandbox `replaced` có bị purge theo không? FK `fk_tenants__sandbox_of` không cascade, nên quy trình purge của phase 13 phải xử lý N tenant con.
3. **Q3.** `ResolveSession` mới chặn tenant `closed`. Theo PRD, chủ store đã `closed` còn cần đăng nhập để tải bản export toàn bộ dữ liệu không? Nếu cần, phải tách điều kiện này ra.
4. **Q4.** Chọn hướng sửa M1 nào: guard trong `WithTenantTx` (a), hay chấp nhận rủi ro và sửa comment (b)?

---

Status: DONE_WITH_CONCERNS
Summary: Không có lỗi Critical hay High. Reset giữ đúng cách ly tenant và RLS, chạy atomic, và an toàn khi hai reset chạy đồng thời. Không có đường nào chiếm phiên, dữ liệu hay slug. vet và lint sạch, các integration test trong phạm vi đều xanh.
Concerns/Blockers:
- Tenant `replaced` vẫn nhận ghi từ request đang chạy dở và từ subscriber outbox chưa bọc (M1, M2).
- Thiếu test re-auth (M3).
- Số tenant tăng không giới hạn và tải dispatcher tăng theo (M4).
- Copy UI "sẽ mất" sai sự thật (M5).
- Lỗi của `./migrations/` (thiếu 000036, `receipt_lines`) thuộc agent procurement, ngoài phạm vi.

---

## Vòng 3: review đợt ba (2026-10-17)

### Phạm vi

- Đối chiếu với mục "Đợt ba" và "Đợt ba, bổ sung" trong `plans/reports/fullstack-developer-261017-1526-sandbox-reset.md`.
- Code đã đọc:
  - `internal/org/sandbox.go`: retry, `sandbox_busy`, cooldown, `closePendingEvents`.
  - `internal/org/errors.go`, `internal/org/http.go` (`Retry-After`).
  - `internal/identity/repo.go` (`lockTenant`), `service.go`, `mfa.go`, `ownership.go`, `sessions.go`.
  - `internal/platform/db/tenants.go`, `internal/platform/outbox/dispatcher.go`, `internal/cli/worker.go`, `internal/cli/maintenance.go`.
  - `internal/org/service.go` (`UpdateTenant`), `internal/platform/periodlock/guard.go`.
  - Migration `000036_auth_tokens_user_index`, `api/openapi.yaml`.
  - Web: `apps/web/src/shared/api/api-error.ts`, `http.test.ts`, `app/layouts/util-bar.tsx`.
- Đồ thị FK đã kiểm trong `000001_platform_identity.up.sql`:
  - `users.tenant_id → tenants`.
  - `sessions` và `auth_tokens` có FK `(tenant_id,user_id) → users`.
  - `mfa_*` chỉ có FK `user_id → users(id)`.
  - `login_attempts` không có FK.
  - Không bảng nghiệp vụ nào có FK tới `users`.

### Kết quả kiểm tra

| Lệnh | Kết quả |
|---|---|
| `go vet` (org, identity, app, platform, cli) | sạch |
| `golangci-lint run` (cùng phạm vi) | 0 issues |
| `go test -tags integration -count=1` cho org, identity, app, platform/db, platform/outbox, cli | tất cả `ok` |
| `TestSandboxResetSerialisesWithAccountChanges`, `-count=5` | 5/5 PASS (subtest deadlock khoảng 1,1 giây, đúng bằng `deadlock_timeout`) |
| `TestLoginRacingASandboxReset`, `TestDispatchSkipsReplacedSandboxes`, `TestPurgeIdempotencySkipsReplacedSandboxes`, `-count=5` | 5/5 PASS |
| `vitest run` cho `http.test.ts` và `app-shell.test.tsx` | 24/24 PASS |

- Không đụng `tally-postgres-1`: `testkit/postgres.go` tự dựng container qua testcontainers.

### Câu 1. Còn tx nào ghi users, sessions hoặc auth_tokens mà không gọi `lockTenant`?

**Đã gọi `lockTenant` (12 chỗ, đúng như implementer nêu):**

- `startSession`, dùng cho cả login và bước MFA.
- `VerifyEmail`, `RequestPasswordReset`, `ResetPassword`, `ChangePassword`.
- `ConfirmTOTP`, `DisableTOTP`.
- `RequestOwnerTransfer`, `AcceptOwnerTransfer`.
- `RevokeSession`, `RevokeOtherSessions`, `RevokeTenantSessions`.

**Ghi vào bảng tài khoản nhưng không gọi `lockTenant`:**

| Writer | Ghi gì | Đua với reset thì sao | Kết luận |
|---|---|---|---|
| `Logout` (`identity/service.go:720-727`) | `UPDATE sessions ... WHERE tenant_id = old` | Nếu reset commit trước, UPDATE khớp 0 dòng. `found` bị bỏ qua nên trả 200 và cookie bị xoá. Session đã chuyển sang tenant mới vẫn sống tới khi hết hạn (idle 12 giờ, tối đa 30 ngày). | **L8** |
| `Reauth`, nhánh xoay token (`service.go:773-786`) | `UPDATE sessions SET token_hash ... WHERE tenant_id = old` | Reset trước: 0 dòng, trả 401 `unauthenticated` thay vì 409. Reauth trước: reset chờ khoá dòng session rồi chuyển session cùng token mới. Không có chu trình khoá vì `login_attempts` không có FK. | **L9** (UX) |
| `Reauth`, nhánh sai mật khẩu; `loginFailed` (`service.go:637-660`) | `login_attempts` và audit, ghi vào tenant cũ | Audit bị chờ khoá `KEY SHARE` của FK tới tenants, rồi ghi vào tenant `replaced`. Không có chu trình khoá. | Chấp nhận (cùng loại M1, sổ append-only) |
| `EnrolTOTP` (`mfa.go:52-94`, `WithPlatformTx`) | `users` FOR UPDATE, rồi upsert `mfa_totp_secrets` | Reset trước: đọc users theo `tenant_id = old` không còn dòng, trả 401. Enrol trước: reset chờ dòng users. Enrol không bao giờ chờ dòng tenant nên không deadlock. | An toàn. Mâu thuẫn với comment, xem M6. |
| `VerifyChallenge` (`mfa.go:234-289`) | `mfa_totp_secrets.last_used_step` và `mfa_recovery_codes.used_at`, commit riêng | Sau đó `startSession` trả 409. Mã khôi phục đã bị tiêu. | **L10** |
| `ResolveSession` touch (`sessions.go:58`) | `UPDATE sessions SET last_seen_at WHERE id` | Chỉ khoá một dòng, lọc theo `id` nên vẫn đúng sau khi chuyển. | An toàn |
| `CreateTenantWithOwner` (signup) | `users` và `auth_tokens` của tenant **mới** | Reset không chạm tới tenant chưa tồn tại. `email_key` là unique toàn cục. | An toàn |

**Chưa có trong code:** mời nhân viên, đổi vai trò, sửa profile, gửi lại e-mail xác minh, và support consent của phase 13.

- Grep `INSERT|UPDATE|DELETE` trên `users`, `sessions`, `auth_tokens` và `mfa_*` trong toàn bộ `internal/`: chỉ thấy trong `identity/` và `org/sandbox.go`.
- Các writer này sẽ phải tự nhớ gọi `lockTenant` (M6).

### Câu 2. FOR KEY SHARE có tranh chấp với các UPDATE tenants khác, và có tạo đường deadlock mới không?

**Tranh chấp:**

- `UpdateTenant` (`org/service.go:166-167`), khoá kỳ (`org/control.go:109,174`) và reset đều gọi `readTenant(..., true)`, tức `SELECT ... FOR UPDATE`. Khoá này xung đột với `FOR KEY SHARE`.
  - Các thao tác này phải chờ login hoặc thay đổi tài khoản đang chạy, và ngược lại.
  - Tx identity rất ngắn: mọi hash argon2 đều chạy trước khi mở tx (`ChangePassword`, `ResetPassword`, `VerifyEmail`, `AcceptOwnerTransfer`), nên thời gian chờ rất nhỏ.
- Bản thân câu UPDATE trong `UpdateTenant` không đổi `slug` hay `id`, nên chỉ cần khoá `FOR NO KEY UPDATE`, mức không xung đột với `KEY SHARE`. Tranh chấp đến từ `FOR UPDATE` tường minh trong `readTenant`.
  - Có thể hạ `readTenant(forUpdate)` của `UpdateTenant` xuống `FOR NO KEY UPDATE` để login không phải chờ (tuỳ chọn, Info).
  - Khoá kỳ thì phải giữ `FOR UPDATE`, vì nó dựa vào xung đột với `KEY SHARE` của `periodlock.Guard`.
- Reset giữ `FOR UPDATE` suốt `Provision`. Dữ liệu mẫu dựng bằng nghiệp vụ thật, mất vài giây. Trong thời gian đó, login và quên mật khẩu trên sandbox đó phải chờ. Chỉ ảnh hưởng tenant sandbox, chấp nhận được.

**Deadlock:** không có đường mới giữa identity và org.

- Tx identity khoá theo thứ tự tenant (`KEY SHARE`), rồi users, sessions, auth_tokens, rồi audit (KEY SHARE tenant lần nữa, đã giữ sẵn).
- `UpdateTenant` và khoá kỳ khoá tenant `FOR UPDATE` rồi ghi bảng nghiệp vụ, không chạm bảng tài khoản.
- Hai tx identity cùng giữ `KEY SHARE` không xung đột nhau.
- `periodlock.Guard` cũng chỉ lấy `KEY SHARE`.

**Còn một đường deadlock, phát hiện được và đã được retry che:**

1. Dispatcher giữ `outbox_events` FOR UPDATE SKIP LOCKED và chạy handler trong cùng tx.
2. Reset giữ tenant `FOR UPDATE`, rồi `closePendingEvents` chờ đúng dòng event đó.
3. Nếu handler INSERT vào bảng có FK tới `tenants`, handler chờ reset, và hai bên tạo thành chu trình.

- Reset chờ trước nên thường bị PostgreSQL chọn làm nạn nhân, rồi chạy lại một lần. Lúc đó dispatcher đã commit, reset thành công.
- Nhiều nhất một event đang xử lý dở được giao trong khi reset diễn ra. Chấp nhận được.

### Câu 3. Có lộ thông tin không? 409 có xuất hiện trước khi tra e-mail không?

Không lộ.

- **Login:** `loginFailed` không gọi `lockTenant`. Sai mật khẩu hoặc e-mail không tồn tại luôn trả 401 `invalid_credentials`. 409 chỉ xuất hiện trong `startSession`, tức là sau khi mật khẩu **đúng**. Không thể dùng 409 để dò e-mail.
- **`RequestPasswordReset`** (`service.go:439-441`): `lockTenant` chạy trước `userByEmail`. Với tenant `replaced`, mọi e-mail đều nhận cùng 409, nên câu trả lời không phụ thuộc e-mail.
- **`VerifyEmail`, `ResetPassword`, `AcceptOwnerTransfer`:** 409 trả trước `consumeToken`, không tiết lộ token có hợp lệ hay không.
- **`VerifyChallenge`:** cần challenge đã ký và mã đúng.
- **Các route cần đăng nhập:** chỉ trả 409 cho chính phiên của người gọi.
- Thông tin duy nhất bị lộ là "tenant của host này đã bị thay". Chỉ có thể tới được điều này qua positive host cache cũ trên một replica khác, vì slug cũ đã đổi thành `reset-<32hex>` và không đoán được. Không đáng kể.

### Câu 4. Test có thật sự chứng minh fix không?

**`TestLoginRacingASandboxReset`** (`internal/app/sandbox_reset_integration_test.go:339-370`): **có**, chạy code thật.

- Login dừng trong `Verify` sau khi đã đọc account của tenant cũ. Reset chạy xong toàn bộ, rồi login chạy tiếp.
- Nếu bỏ `lockTenant` khỏi `startSession`, `insertSession(old, user)` vi phạm FK (23503) và trả 500. Test kỳ vọng 409, nên sẽ fail.
- Test còn kiểm tenant cũ không còn session nào, và lần login sau vào được sandbox mới.
- Test chỉ phủ một thứ tự xen kẽ: reset commit trong lúc login đang ở giữa chừng.

**`TestSandboxResetSerialisesWithAccountChanges`** (`internal/org/org_integration_test.go:785-848`): **có, ở mức giao thức khoá**.

- Subtest 1 dùng SQL thô thay cho identity: giữ `KEY SHARE`, reset chờ ở `FOR UPDATE` (`awaitLockWait`), rồi INSERT session và commit.
  - Test chứng minh reset đọc dữ liệu sau khi tx kia commit, và session mới được chuyển sang tenant mới.
  - Đây chính là lỗi snapshot của CTE trước đây: session commit muộn bị bỏ sót, và FK báo lỗi.
- Subtest 2: writer khoá users trước rồi mới khoá tenant, gây deadlock. Reset là nạn nhân, chạy lại và thành công; test cũng kiểm writer **không** bị huỷ.
  - Nếu bỏ retry, test fail với `sandbox_busy` (implementer đã thử, phù hợp với code).
  - Thứ tự chọn nạn nhân là tất định: reset bắt đầu chờ trước, nên `deadlock_timeout` của nó hết trước. 5/5 lần chạy đều xanh.
- Không subtest nào gọi code identity thật. Đường "login giữ khoá trước, reset phải chờ" chưa được kiểm bằng code thật.
- 11 call site còn lại của `lockTenant` không có test nào. Xoá `lockTenant` khỏi `ChangePassword` chẳng hạn, CI vẫn xanh (M6).

**Cooldown và outbox:** đã có test.

- `org_integration_test.go:428,486`: `RetryAfter` chính xác.
- `sandbox_reset_integration_test.go:214-218,296`: HTTP 429 và header `Retry-After`.
- `org_integration_test.go:463-472`: `closed_events` và `last_error`.
- `TestDispatchSkipsReplacedSandboxes`, `TestPurgeIdempotencySkipsReplacedSandboxes`.

**Thiếu:** test cho nhánh cuối `sandbox_busy` (thua deadlock hai lần liên tiếp) (L11).

### Trạng thái các finding cũ sau đợt ba

| Finding | Trạng thái |
|---|---|
| M1 (request đang chạy ghi vào tenant `replaced`) | Còn mở, nhưng comment `sandbox.go:47-50` và `db/tenants.go:24-28` nay đã nói đúng sự thật |
| M2 (subscriber outbox opt-in) | **Đã đóng.** Dispatcher và purge dùng `ActiveTenantIDs`, reset đóng các event còn treo; `skipReplaced` đã gỡ |
| M4 (tăng không giới hạn) | **Phần lớn đã xử lý.** Tải dispatcher không còn tăng theo số lần reset. Cooldown 10 phút giới hạn tối đa 144 tenant `replaced`/ngày/sandbox; số dòng vẫn tăng, Q2 về PII vẫn mở |
| L1 (500 khi đua với login hoặc token) | **Đã đóng** bằng `lockTenant` và retry. Còn L8 đến L10 ở các writer ngoài lề |
| L2 (bỏ qua `Idempotency-Key`) | **Đã đóng.** Web bỏ key; retry sau khi commit nhận 429. Xem thêm L12 về retry trong lúc còn chạy |
| L4 (index `auth_tokens`) | **Đã đóng** bằng 000036. Down migration đúng |

### Findings vòng 3

#### Critical

Không có.

#### High

Không có.

#### Medium

**M6. Invariant "mọi thay đổi tài khoản khoá tenant trước" chỉ dựa vào quy ước tại 12 call site, comment nói quá, và chỉ một call site có test**

- Vị trí:
  - `internal/identity/repo.go:48-54` ("lockTenant is the first statement of every transaction that changes an account...")
  - `internal/org/sandbox.go:52-55` ("Account changes lock the tenant row before any account row (identity)")
- Hiện trạng:
  - Sáu writer bảng tài khoản không gọi `lockTenant`. Bảng ở Câu 1 cho thấy hiện tại chúng không gây deadlock hay lỗi dữ liệu.
  - Riêng `EnrolTOTP` khoá dòng `users` FOR UPDATE mà không khoá tenant, trái với điều comment khẳng định. Nó không gây deadlock chỉ vì không bao giờ chờ dòng tenant sau đó.
- Kịch bản:
  1. Phase 13 (support consent) hoặc release sau thêm writer mới cho `sessions` hoặc `auth_tokens`, ví dụ mời nhân viên hay đổi vai trò, mà quên gọi `lockTenant`.
  2. Writer đó đua với reset.
  3. Kết quả là 23503 rồi 500, đúng lỗi L1 cũ.
  4. Không test nào bắt được, vì chỉ `startSession` có test bằng code thật.
- Hậu quả: không mất hay rò dữ liệu (FK chặn), nhưng là hồi quy âm thầm và comment dẫn sai người đọc.
- Hướng sửa (chọn một hoặc cả hai):
  1. Gom vào một helper, ví dụ `s.withAccountTx(ctx, tenantID, fn)` = `WithTenantTx` + `lockTenant`, rồi dùng cho mọi tx ghi bảng tài khoản, kể cả `Logout` và `Reauth`. Không thể quên một bước khi nó nằm sẵn trong hàm mở tx.
  2. Viết một test bảng rẻ, không cần race: đặt `status = 'replaced'` trực tiếp cho một tenant, rồi gọi từng endpoint đổi tài khoản và kỳ vọng 409 `sandbox_replaced`. Kết quả tất định, phủ đủ 12 call site và bắt được call site mới bị quên.
  3. Sửa comment cho đúng: các writer chỉ khoá một dòng account và không bao giờ chờ dòng tenant thì được miễn.

#### Low

**L8. Logout đua với reset trả 200 nhưng session vẫn sống**

- Vị trí: `internal/identity/service.go:720-727`; `repo.go:226-240`, kết quả `found` bị bỏ qua.
- Kịch bản:
  1. `ResolveSession` trả principal của tenant cũ.
  2. Reset commit và chuyển session sang tenant mới.
  3. `revokeSession(... tenant_id = old ...)` khớp 0 dòng. Handler xoá cookie và trả 200.
  4. Dòng session ở tenant mới vẫn hợp lệ tới 12 giờ idle hoặc 30 ngày tuyệt đối. Ai đã có token (cookie bị sao chép) vẫn dùng được.
- Cửa sổ race hẹp. Ngoài race thì logout hoạt động đúng, vì slug được dùng lại nên host và session cùng trỏ tenant mới.
- Hướng sửa: logout revoke theo `id` và `user_id`, không lọc `tenant_id`. `id` là PK và principal lấy từ chính session đó, nên an toàn. Hoặc dùng helper ở M6 rồi, khi gặp `replaced`, revoke theo id.

**L9. `Reauth` đua với reset trả 401 `unauthenticated`, không phải 409 `sandbox_replaced`**

- Vị trí: `internal/identity/service.go:755-786`.
- Web hiểu đây là phiên hết hạn (`isSessionExpired`), trong khi session thật ra vẫn sống ở tenant mới.
- Chỉ ảnh hưởng UX; dữ liệu vẫn đúng.
- Hướng sửa: gọi `lockTenant` đầu cả hai tx của `Reauth`, cho nhất quán với 12 chỗ còn lại và với openapi.

**L10. `VerifyChallenge` tiêu mã khôi phục hoặc bước TOTP trước khi `startSession` có thể trả 409**

- Vị trí: `internal/identity/mfa.go:234-289`; tx tiêu mã commit riêng, trước `startSession`.
- Kịch bản: reset commit giữa `userByID` và `startSession`. Người dùng nhận 409, còn mã khôi phục đã bị đánh dấu dùng.
- Cửa sổ race rất hẹp. Mẫu tiêu mã trước khi tạo session có từ trước đợt ba và áp dụng cho mọi lỗi của `startSession`.
- Hướng sửa: tiêu mã trong cùng tx với `startSession`, sau `lockTenant`. Hoặc chấp nhận và ghi chú lại.

**L11. Nhánh `sandbox_busy` chưa có test**

- Vị trí: `internal/org/sandbox.go:56-67`.
- Hai lần thua deadlock liên tiếp phải trả 409 `sandbox_busy` chứ không phải 500. Chưa có test nào chứng minh điều đó.
- Retry chỉ hoạt động khi lỗi 40P01 được bọc bằng `%w` tới tận `db.IsRetryable`. Nếu một service seed trong `Provision` đổi lỗi pg thành `apperr`, nhánh retry âm thầm biến mất.
- Hướng sửa:
  - Thêm subtest giữ khoá users xuyên qua cả hai lần chạy, kỳ vọng `apperr.Is(err, "sandbox_busy")`.
  - Thêm một case khiến lỗi deadlock nảy ra từ bên trong `Provision`.

**L12. Web hiển thị message tiếng Anh cho `sandbox_not_active`, và comment về retry nói chưa đủ**

- Vị trí:
  - `apps/web/src/shared/api/api-error.ts:188-205` thiếu `sandbox_not_active` và `not_sandbox`.
  - `apps/web/src/app/layouts/util-bar.tsx:78`.
- Kịch bản:
  1. Retry hoặc tab thứ hai gửi reset trong lúc lần đầu **còn đang chạy**.
  2. Request đó chờ ở `FOR UPDATE`, rồi thấy tenant đã `replaced`.
  3. Server trả 409 `sandbox_not_active` "The demo store was already reset", và UI tiếng Việt hiện nguyên câu tiếng Anh này.
- Comment "a retry reaches the new sandbox and the API refuses it (429)" chỉ đúng khi retry tới sau lúc commit.
- Ngoài ra, 429 `sandbox_reset_too_soon` hiện câu chung "Thao tác quá nhanh. Thử lại sau N phút.". Đúng nghĩa, nhưng có thể viết rõ hơn.
- Hướng sửa:
  - Thêm câu tiếng Việt cho `sandbox_not_active`, ví dụ "Cửa hàng mẫu vừa được tạo lại. Tải lại trang để vào cửa hàng mẫu mới.".
  - Có thể thêm câu riêng cho `sandbox_reset_too_soon`.
  - Sửa comment cho đủ cả hai trường hợp.

#### Info (không cần sửa)

- `UpdateTenant` có thể hạ xuống `FOR NO KEY UPDATE` để login và sửa hồ sơ store không phải chờ nhau (Câu 2).
- `tally maintenance revoke-sessions` trên tenant `replaced` báo lỗi "The demo store was just reset; sign in again". Câu này lạc ngữ cảnh với operator nhưng vô hại; tenant đó cũng không còn session nào.
- Cooldown tính từ `created_at` của sandbox, nên sandbox đầu tiên tạo lúc signup chỉ reset được sau 10 phút. Đúng quyết định đã chốt.
- `Retry-After` được làm tròn lên (`math.Ceil`), và web đổi ra phút.
- 000036 dùng `CREATE INDEX` không `CONCURRENTLY`, chặn ghi `auth_tokens` trong lúc build. Bảng còn nhỏ ở R1, chấp nhận được.
- Openapi khớp code: 409 `sandbox_replaced` nằm đúng trên 11 route có `lockTenant`, `/auth/reauth` không ghi 409 (khớp với L9 hiện tại), route reset ghi `sandbox_busy` và 429.

### Plan follow-up

- Phase 04: đợt ba có thể đóng sau khi chọn hướng cho M6. Nên làm ít nhất test bảng ở bước 2, vì rẻ và chặn được hồi quy.
- Phase 13: mọi writer mới cho `sessions`, `auth_tokens` hoặc `users` (support consent, mời nhân viên) phải đi qua helper ở M6 hoặc `lockTenant`. Nên ghi điều này vào phase file.

---

Status: DONE_WITH_CONCERNS
Summary: Đợt ba đóng được L1, L2, L4 và M2, và xử lý phần lớn M4. Không có lỗi Critical hay High. `lockTenant` không tạo deadlock mới giữa identity và org, và 409 không làm lộ e-mail hay token. vet và lint sạch; integration test và web test trong phạm vi đều xanh, test race chạy 5/5 lần ổn định.
Findings theo mức độ:
- Critical: không có.
- High: không có.
- Medium: M6. Invariant khoá tenant chỉ dựa vào quy ước tại 12 call site, comment nói quá, và chỉ `startSession` có test bằng code thật.
- Low:
  - L8: logout đua với reset để session sống.
  - L9: `Reauth` trả 401 thay vì 409.
  - L10: MFA tiêu mã khôi phục trước khi gặp 409.
  - L11: nhánh `sandbox_busy` chưa có test.
  - L12: web hiện tiếng Anh cho `sandbox_not_active`, và comment về retry chưa đủ.
Concerns/Blockers: không có blocker. M1 và Q2 của vòng 1 vẫn mở.
