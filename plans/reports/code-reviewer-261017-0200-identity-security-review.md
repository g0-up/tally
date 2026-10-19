# Review bảo mật Identity & Organization: phát hiện, bản sửa, error contract

Ngày: 2026-10-17 (Asia/Saigon). Người review: code-reviewer. Phạm vi: `apps/api` (identity, org, platform authn/exception/objectstore/periodlock/middleware), `api/openapi.yaml`, design docs. Không sửa `apps/web`. Không commit.

Đối chiếu spec: `phase-04-identity-organization.md`, `design/architecture.md` §3.6/§6/§7, `design/database-schema-org-partners.md`, `prd.md` (ORG-*, SEC-*).

## Tóm tắt

- Đã sửa 6 lỗi có bằng chứng: TOTP replay, không mở khóa kỳ hoàn toàn được, sandbox reset bỏ qua khóa kỳ, attachment không kiểm sha256, canonical JSON lệch với web, tên audit action sai quy ước `entity.verb`.
- OpenAPI lệch với Go ở 3 chỗ (csrf/reauth/owner_only). Đã sửa spec theo Go vì web đã khớp Go. Đã thêm khối error contract và test hợp đồng.
- 2 migration mới: `000028_identity_totp_last_used_step`, `000029_org_period_lock_full_reopen`. Đã backup trước khi migrate (`infra/backups/tally-20261017015321.dump`, `infra/backups/tally-20261017020356.dump`). Dev DB hiện ở version 29 (clean). Không dùng trigger/function/DO block.
- Gate: `gofmt -l` sạch, `go vet` (cả `-tags integration`) sạch, `golangci-lint` 0 issues, `make test` và `make test-integration` xanh.

## Phát hiện đã sửa

### 1. High: Mã TOTP dùng lại được (replay)

- Bằng chứng: `VerifyChallenge` chỉ gọi `ValidateTOTP` và không lưu bước thời gian đã dùng. Một mã 6 số còn hiệu lực khoảng 90 giây (skew ±1) và đăng nhập lại được nhiều lần. Challenge token là stateless (ký HMAC) nên dùng lại được trong 5 phút. Kẻ nhìn trộm hoặc phishing relay chỉ cần một mã là mở được nhiều phiên. Mã dùng lúc xác nhận enrol cũng dùng lại được để đăng nhập.
- Sửa:
  - `internal/platform/authn/totp.go:69` thêm `MatchTOTP` trả về step khớp. So sánh constant-time trên mọi candidate và chọn step không rẽ nhánh. `ValidateTOTP` giờ bọc hàm này.
  - Migration `000028`: thêm `mfa_totp_secrets.last_used_step bigint` với `CHECK (last_used_step >= 0)`.
  - `internal/identity/mfa.go:79`: enrol đặt lại `last_used_step = NULL`. `mfa.go:124`: confirm lưu step.
  - `mfa.go:264` `useTOTPStep` là UPDATE có điều kiện `last_used_step IS NULL OR last_used_step < $2`. 0 dòng cập nhật nghĩa là `invalid_code`, và lần thử này được tính vào login throttle. Điều kiện nằm ngay trong UPDATE nên hai request đồng thời dùng cùng mã chỉ một cái thắng (atomic, không race).
- Test: `TestMatchTOTPReturnsTheMatchedStep`. `internal/app/identity_integration_test.go` (`TestTOTPAndRecoveryCodes`) kiểm 4 ca: mã enrol bị từ chối khi đăng nhập, mã mới hợp lệ, dùng lại cùng mã bị từ chối, mã cũ hơn còn trong skew bị từ chối.

### 2. High: Không mở khóa kỳ hoàn toàn được

- Bằng chứng: `period_locks.locked_through NOT NULL` và reopen bắt buộc có một ngày. Sau lần khóa đầu, owner không bao giờ về được trạng thái "không khóa". PRD SEC-05 ("Mở → khóa → mở lại có lý do") cần trạng thái này.
- Sửa:
  - Migration `000029`: `locked_through DROP NOT NULL` và thêm `ck_period_locks__lock_date CHECK (action = 'reopen' OR locked_through IS NOT NULL)`. Bản down sẽ fail nếu đã có dòng full-reopen; comment trong file ghi rõ điều này.
  - `org/model.go`: `LockedThrough *time.Time`. `org/control.go:217` `currentLock`.
  - `ReopenPeriod` (`control.go:160`) trả `409 period_not_locked` khi không có khóa.
  - `org/dto.go:123`: `reopenRequest.locked_through` là `json.RawMessage` bắt buộc có mặt, nhận một ngày hoặc `null` tường minh. Thiếu trường thì trả 422, nên client không mở khóa toàn bộ do vô tình bỏ trường. Parse bằng `parseOptionalDate` (`dto.go:251`).
  - `platform/periodlock/guard.go:28` scan `*time.Time`; NULL nghĩa là không khóa.
- Test: `periodlock/guard_integration_test.go` (full reopen rồi relock, `TestLockEventNeedsADate`); `org/org_integration_test.go`; `org/org_test.go` (`TestParseOptionalDate`, `TestReopenRequestNeedsLockedThroughPresent`).
- `migrations/isolation_integration_test.go`: thêm override mẫu `period_locks.locked_through` vì cột giờ nullable nhưng CHECK yêu cầu ngày khi `action = 'lock'`. Đây là file dùng chung với Phase 05; chỉ thêm 1 dòng.

### 3. High: Sandbox reset và dữ liệu append-only (quyết định thiết kế)

- Bằng chứng: `ResetSandbox` trả settings về mặc định nhưng không chạm `period_locks`. Kỳ đã khóa trong sandbox vẫn khóa sau reset. App role có `REVOKE UPDATE, DELETE` trên bảng append-only, nên không xóa được.
- Đã làm: `org/sandbox.go:76` `unlockSandboxPeriods` chỉ khi đang có khóa mới append một sự kiện full-reopen với lý do `"Sandbox reset"`. Audit `tenant.sandbox_reset` ghi `After{"period_lock_reopened": bool}`. Không xóa dòng append-only nào.
- `org/ports.go`: comment `SandboxResetter` ghi hợp đồng cho các domain ledger sau này. App role không bao giờ có DELETE trên bảng append-only. Resetter chỉ xóa dòng không bị tham chiếu và archive phần còn lại.
- Các phương án đã cân nhắc và loại:
  - (a) CLI purge chạy bằng owner role. Phải có quyền DB đặc quyền trong luồng người dùng, phá ranh giới đặc quyền.
  - (b) Thay tenant sandbox bằng tenant mới. Xung đột với slug bất biến (§7.2), cache host, session và PWA đã cài.
  - (c) Thêm RLS policy cho phép DELETE. Làm yếu bảo đảm append-only ở mức privilege.
- Test: `TestSandboxReset` kiểm lịch sử khóa còn nguyên kèm sự kiện reopen, reset lần 2 không append thêm, settings version 4 và có 2 audit reset.
- Còn mở: các domain ledger (Phase 07+) phải theo hợp đồng resetter. Route lệch: API là `POST /tenant/sandbox/reset`, còn plan web `phase-18` dòng 156 ghi `POST /sandbox/reset`. Web phải dùng route của API.

### 4. Medium: Hoàn tất attachment không kiểm sha256

- Bằng chứng: `CompleteAttachment` chỉ so size và content-type qua HEAD. Client khai một sha256 nhưng upload nội dung khác cùng kích thước thì vẫn được chấp nhận, và digest lưu trong DB sai.
- Sửa:
  - `platform/objectstore/store.go:49` thêm `Open`. `s3.go:134` cài bằng GetObject + Stat, map not-found sang `ErrNotFound`. S3 là implementer duy nhất.
  - `org/attachments.go:237` `objectDigestIs` stream `io.LimitReader(size+1)` qua SHA-256 rồi so constant-time. Nếu lệch thì trạng thái thành `ScanRejected`, audit `attachment.reject` và object bị xóa.
- Test: thêm ca nội dung giả mạo vào `TestAttachmentRoundTripThroughMinIO` (upload_rejected, object đã xóa, attachment_not_ready). Round-trip S3 kiểm Open và Open sau khi xóa.
- Rủi ro còn lại: presigned PUT còn hiệu lực đến hết TTL, nên chủ phiên có thể ghi đè object sau khi complete với cùng size/type. Đề xuất ký header `x-amz-checksum-sha256` vào presigned PUT khi viết uploader web (web chưa có code upload).

### 5. Medium: Canonical JSON của exception payload hash lệch với web

- Bằng chứng: Go dùng `encoding/json`, vốn escape `<`, `>`, `&`, U+2028/U+2029. Web `canonicalJson` (`apps/web/src/shared/lib/canonical-hash.ts`) theo `JSON.stringify` và không escape các ký tự này. Thứ tự khóa cũng khác (Go theo byte UTF-8, JS theo UTF-16 code unit). Payload có các ký tự đó (ví dụ lý do có "&") sẽ có hash khác nhau, nên confirmation luôn bị từ chối. Đây là lỗi availability theo hướng fail-closed, không phải lỗ hổng.
- Sửa: `platform/exception/exception.go:67` `PayloadHash` dùng `writeCanonical`, `writeJSString` (escape đúng như JSON.stringify) và `compareUTF16`.
- Test: `TestPayloadHashMatchesTheWebClient` với 3 fixture có digest tính bằng Node từ chính `canonicalJson` của web.
- Lệch còn lại (chấp nhận): lone surrogate, và dạng số khi body client không do JSON.stringify tạo ra. Idempotency RequestHash chỉ chạy phía server nên không bị ảnh hưởng.

### 6. Low: Tên audit action sai quy ước `entity.verb`

- Đổi tên: `user.enable_mfa`, `user.disable_mfa`, `user.verify_email`, `user.reset_password`, `user.reject_login`, `session.create`, `session.revoke`, `tenant.request_owner_transfer`, `tenant.transfer_owner`. Toàn bộ action trong identity/org giờ theo `entity.verb`.
- Cập nhật query test (`identity_integration_test.go`), ví dụ trong OpenAPI và dòng Security trong phase-04.
- Không ép định dạng trong `audit.Recorder` vì test của posting dùng action "x"/"a". Đề xuất thêm validate regex `^[a-z_]+\.[a-z_]+$` ở Recorder sau khi sửa các test đó.

## Error contract cuối cùng (Go là nguồn chuẩn, web đã khớp)

| Status | code | Khi nào | Client xử lý |
|---|---|---|---|
| 401 | `unauthenticated` | Không có session hợp lệ | Đăng nhập lại |
| 403 | `csrf_failed` | Thiếu hoặc sai CSRF (`middleware/csrf.go:111`) | `GET /host-context`, retry 1 lần |
| 403 | `reauth_required` | Route nhạy cảm, chưa reauth trong 5 phút (`middleware/reauth.go:27`) | `POST /auth/reauth`, retry |
| 403 | `invalid_password` | Reauth sai mật khẩu (`identity/errors.go:41`) | Báo lỗi |
| 403 | `not_sandbox` | Reset store không phải sandbox | Báo lỗi |
| 409 | `exception_required` + `exception_kind` | Cần owner xác nhận ngoại lệ | `POST /exception-confirmations`, retry với id |
| 412 | `precondition_failed` | `If-Match` lệch version (`httpx/etag.go`) | Tải lại |
| 428 | `precondition_required` | Thiếu `If-Match` | Lỗi lập trình client |
| 422 | `validation_failed` + `fields` / mã domain | Input sai | Hiện lỗi theo field |
| 423 | `period_locked` | Ngày chứng từ thuộc kỳ đã khóa | Báo lỗi |
| 429 | `rate_limited` + `Retry-After` | Throttle | Chờ |

Các sửa trong `apps/api/api/openapi.yaml` (sửa tối thiểu vì Phase 05 cũng sửa file này):

- `info.description` thêm khối "Error contract" như bảng trên.
- `/host-context`: `csrf_invalid` đổi thành `csrf_failed`.
- `/auth/reauth`: "401 reauth_required" đổi thành 403. `owner_only` bị bỏ vì mã này không tồn tại trong Go.
- Thêm `'403'` cho POST/DELETE `/auth/mfa/totp`, `/owner-transfer`, `/period-locks/reopen`. Mô tả `/tenant/sandbox/reset` thêm `reauth_required`.
- Thêm `'428'` cho PATCH `/tenant` và PATCH `/tenant/settings`.
- `locked_through` nullable trong request reopen và trong schema `PeriodLockEvent`.
- `/store-lookup` trả 422 với email sai định dạng. Lỗi này chỉ phụ thuộc input, không lộ việc tồn tại cửa hàng, nên giữ nguyên.
- Test mới `TestOpenAPIErrorResponses` (`internal/app/openapi_contract_test.go`) kiểm hai điều: operation có summary "re-authentication" phải liệt kê 403, và operation có tham số `IfMatch` phải liệt kê 412 và 428.
- Ghi chú: `apps/web/src/shared/api/schema.d.ts` còn mô tả cũ (`csrf_invalid`, `owner_only`). Cần chạy `pnpm gen:api` phía web. Web chưa dùng type period lock nên regenerate không gây vỡ.
- Contract đã ghi vào `design/architecture.md` §3.6. §6 bổ sung chống replay TOTP và kiểm SHA-256 upload.

## Phát hiện chưa sửa (đề xuất)

| Mức | Phát hiện | Vị trí | Đề xuất |
|---|---|---|---|
| Medium | `GET /signup/slug-check` không có limiter riêng (chỉ global 20 rps/burst 100 mỗi IP), dễ dò danh sách cửa hàng | `identity/http.go:69` | Gắn `limit` như signup/store-lookup |
| Medium | Timing oracle khi signup: email đã tồn tại chỉ chạy DummyVerify, email mới chạy Hash + provisioning | `identity/service.go:220`, `:291` | Đưa provisioning ra job hoặc đệm thời gian phản hồi cố định |
| Medium | Bật MFA không thu hồi các session khác | `identity/mfa.go:95` | Revoke các session khác khi confirm TOTP |
| Low | Reauth sai có ghi throttle nhưng không ghi audit | `identity/service.go:698-727` | Thêm audit `user.reject_reauth` |
| Low | Presigned PUT ghi đè được sau khi complete (xem mục 4) | `org/attachments.go` | Ký checksum header |
| Low | `audit.Recorder` không ép định dạng action | `platform/audit` | Xem mục 6 |
| Info | Migration 000027 `payload_hash NOT NULL` không có default, chỉ an toàn vì bảng rỗng | `migrations/000027_*` | Không cần làm gì trên dev; lưu ý khi đã có dữ liệu prod |
| Info | Đăng nhập platform admin chưa được cài | | Thuộc phase admin |

Đã kiểm và đạt: argon2id m=64MiB t=3 p=2; cookie `__Host-` host-only HttpOnly/Secure/SameSite=Lax; CSRF gắn với session; token dùng một lần và có hạn; login throttle 5 lần/15 phút theo email+IP; client IP lấy từ CF-Connecting-IP qua trusted proxies; reset mật khẩu thu hồi session; regex slug khớp design §7.2 (3-30 ký tự).

## File đã chạm

- Go: `internal/platform/authn/{totp.go,authn_test.go}`, `internal/identity/{mfa.go,service.go,sessions.go,ownership.go}`, `internal/org/{model.go,control.go,dto.go,http.go,sandbox.go,ports.go,attachments.go,org_test.go,org_integration_test.go}`, `internal/platform/periodlock/{guard.go,guard_integration_test.go}`, `internal/platform/objectstore/{store.go,s3.go,*_test.go}`, `internal/platform/exception/{exception.go,exception_test.go}`, `internal/app/{identity_integration_test.go,openapi_contract_test.go}`.
- Dùng chung với Phase 05 (thay đổi tối thiểu): `api/openapi.yaml`, `migrations/isolation_integration_test.go` (1 dòng override).
- Migrations: `000028_identity_totp_last_used_step.{up,down}.sql`, `000029_org_period_lock_full_reopen.{up,down}.sql`.
- Docs: `docs/database.md` (sinh bằng `make db-docs`), `design/architecture.md`, `design/database-schema-org-partners.md`, `phase-04-identity-organization.md` (dòng Security, bước 6, thêm 1 todo hardening đã tick; frontmatter giữ `status: done`).

Status: DONE_WITH_CONCERNS
Summary: Đã sửa và có test cho TOTP replay, full reopen khóa kỳ, sandbox reset (không xóa append-only), kiểm sha256 attachment, parity canonical JSON với web, tên audit. OpenAPI đã khớp error contract của Go và có test hợp đồng. Mọi gate đều xanh.
Concerns/Blockers: Web cần regenerate `schema.d.ts` và dùng route `POST /tenant/sandbox/reset` (plan phase-18 ghi `/sandbox/reset`). Các domain ledger sau này phải theo hợp đồng `SandboxResetter`. Presigned PUT còn ghi đè được trong TTL. Còn 3 phát hiện Medium chưa sửa (rate limit slug-check, timing signup, MFA không revoke session).
