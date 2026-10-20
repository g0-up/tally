---
phase: 2
title: "API ghép đôi: bảng, hub SSE, nhận mã, cho phép, gửi mã, ngắt"
status: pending
priority: P1
effort: 2d
dependencies: [1]
---

# Phase 2: API ghép đôi

## Mục tiêu và bối cảnh

Tạo phiên ghép đôi giữa một tab trên máy tính và một điện thoại. Điện thoại quét QR để nhận mã, rồi máy tính phải bấm
**Cho phép** thì điện thoại mới gửi được mã quét. Máy tính nghe mã quét qua SSE. Điện thoại gửi mã bằng một yêu cầu
HTTP và chờ máy tính xác nhận trong cùng yêu cầu đó, rồi nhận một kết quả chỉ gồm trạng thái, tên hàng và đơn vị. Thiết
kế lấy từ [nghiên cứu SSE](../reports/researcher-261020-1908-sse-pairing-design.md).

Bằng chứng:
- API chưa có SSE, LISTEN/NOTIFY, websocket hay `http.Flusher` ở đâu cả.
- `http.Server` có `WriteTimeout: 60s` (`apps/api/internal/cli/serve.go:266`), sẽ cắt mọi luồng dài hơn 60 giây.
  `shutdownTimeout` là 15 giây (`serve.go:48`). `runHTTPServer(ctx, ln, handler, logger)` (`serve.go:261`) dùng chung cho
  server `api` và `metrics` (`serve.go:195-197`) và không có chỗ gắn hook tắt máy.
- Route công khai của tenant dùng `TenantPublic = a.API(httpx.HostTenant)` (`apps/api/internal/app/routes.go:58`), chỉ
  có Session và CSRF (`routes.go:212-217`). Maintenance, Entitlement và SupportReadOnly chỉ gắn trên `tenantGroup`
  (`routes.go:262-278`), và đọc `TenantID(c)` chỉ có sau `Tenant()`. Handler công khai lấy `httpx.HostOf(c).TenantID`
  và từ chối `uuid.Nil`, như `apps/api/internal/identity/http.go:108-115`.
- `MaintenanceReader.InMaintenance` và `EntitlementReader.CanWrite` là interface nhỏ, gọi trực tiếp được
  (`apps/api/internal/platform/middleware/entitlement.go:14-64`).
- `WithTenantTx` giữ một tx và một connection suốt hàm, chỉ commit khi hàm trả về
  (`apps/api/internal/platform/db/tx.go:107-136`). Pool mặc định 20 connection (`db.go:48-50`, `config.go:441`).
- Limiter của repo là token bucket theo IP, chỉ giới hạn tốc độ, không giới hạn số kết nối đồng thời
  (`apps/api/internal/platform/middleware/ratelimit.go:26-99`). Mẫu limiter riêng: `identity/http.go:75-89`.
- `httpx.IdempotencyKey(c, body)` kiểm tra độ dài (tối đa 200), ASCII in được và tính `RequestHash`
  (`apps/api/internal/platform/httpx/bind.go:29-30,153-167`). `openapi_contract_test.go:133-150` yêu cầu mọi route có
  tham số `IdempotencyKey` liệt kê các lỗi của nó.
- Cookie prod mang tiền tố `__Host-` (`apps/api/internal/platform/middleware/session.go:45-62`). `Principal.SessionID`
  là id phiên đăng nhập, và `SessionResolver.ResolveSession(token)` kiểm tra lại được một token (`session.go:28-42`).
- Lệnh tùy biến dùng `CommandRouter` dạng `POST /<resource>/{id}:<verb>` (`apps/api/internal/platform/httpx/command_router.go:54-70`).
- Vòng dọn của worker: `every` và `purgeIdempotency` duyệt `ActiveTenantIDs` (`apps/api/internal/cli/worker.go:161-173,253,269-289`).
- Quy ước bảng, RLS và quyền: `000023_campaign_crm.up.sql`, `000027_org_exception_confirmation_uses.up.sql` và
  `plans/261016-0854-tally-full-product-implementation/design/database-conventions.md` §3–5, §8.
- Module gate tra theo đoạn đầu của đường dẫn (`apps/api/internal/app/module_gate.go:13-23,36-56`). `scanner-sessions`
  và `scan` không thuộc module nào, vì Kiểm kê và Nhập hàng cũng dùng.

<!-- Updated: Red Team 2026-10-20 - ranh giới giao dịch, hub, cổng bảo trì, giới hạn tài nguyên, idempotency, cookie, khởi động lại -->
<!-- Updated: Validation Session 1 - thêm bước Cho phép trên máy tính kèm mã xác minh 4 số -->

## Files

- Tạo `apps/api/migrations/000060_scanner_sessions.up.sql` và `.down.sql`.
- Tạo package `apps/api/internal/scanner/`:
  - `model.go`: `Session`, `ScanResult`, các hằng `Outcome*`, trạng thái phiên.
  - `repo.go`: SQL, mỗi thao tác một tx ngắn.
  - `hub.go`: interface `Hub` và bản trong bộ nhớ.
  - `sse.go`: ghi sự kiện, heartbeat, gia hạn deadline ghi.
  - `service.go`: tạo, làm mới mã, nhận mã, cho phép, từ chối, gửi mã, xác nhận, ngắt, dọn.
  - `http.go`: route và handler, cookie điện thoại, limiter, kiểm tra bảo trì của host.
  - `device_label.go`: nhãn thiết bị từ User-Agent.
  - Test: `hub_test.go`, `service_test.go`, `http_test.go`, `scanner_integration_test.go`.
- Sửa `apps/api/internal/app/domains.go` và `app.go` (nối service, cổng catalog `ValidateBarcode` và tên hàng/đơn vị,
  `MaintenanceReader`, `EntitlementReader`, `SessionResolver`, đăng ký route).
- Sửa `apps/api/internal/cli/serve.go`: thêm trường `onShutdown func()` vào `namedServer` và tham số tương ứng cho
  `runHTTPServer`, gọi `srv.RegisterOnShutdown` khi khác nil. Server `api` truyền `hub.CloseAll`, `metrics` truyền nil.
- Sửa `apps/api/internal/cli/worker.go`: thêm `purgeScannerSessions` vào cùng vòng dọn với `purgeIdempotency`.
- Sửa `apps/api/api/openapi.yaml`: các đường dẫn và schema dưới đây, kèm mô tả sự kiện SSE.

## Bảng (migration 000060)

`scanner_sessions`:
- `id`, `tenant_id` (FK `tenants`), `created_by uuid NOT NULL`, `login_session_id uuid NOT NULL`, `created_at`.
- `claim_code_hash bytea`, `claim_expires_at timestamptz`: có giá trị khi chờ quét QR, về NULL khi điện thoại nhận.
- `phone_secret_hash bytea`, `claimed_at timestamptz`, `device_label text`.
- `approved_at timestamptz`: máy tính bấm Cho phép.
- `desktop_seen_at timestamptz NOT NULL`: lần cuối luồng SSE của máy tính còn sống.
- `revoked_at timestamptz`, `revoked_by text` với
  `ck_scanner_sessions__revoked_by CHECK (revoked_by IN ('desktop','phone','system'))`.
- `ux_scanner_sessions__id (tenant_id, id)`, `ux_scanner_sessions__claim_code_hash (tenant_id, claim_code_hash)`,
  `ux_scanner_sessions__phone_secret_hash (tenant_id, phone_secret_hash)`, `ix_scanner_sessions__creator (tenant_id, created_by, revoked_at)`.

`scanner_scans`:
- `id`, `tenant_id`, `session_id`, `idempotency_key text NOT NULL`, `request_hash bytea NOT NULL`, `code text NOT NULL`,
  `created_at`, `expires_at timestamptz NOT NULL`.
- `result jsonb`, `resolved_at timestamptz`: NULL khi máy tính chưa xác nhận.
- `ux_scanner_scans__session_key (tenant_id, session_id, idempotency_key)`,
  `fk_scanner_scans__session_id (tenant_id, session_id) REFERENCES scanner_sessions (tenant_id, id) ON DELETE CASCADE`.

Cả hai bảng có ENABLE + FORCE RLS và policy `tenant_isolation`, `GRANT SELECT, INSERT, UPDATE, DELETE` cho `tally_app`,
`GRANT SELECT` cho `tally_readonly`. Bản `down` xóa `scanner_scans` rồi `scanner_sessions`.

**Trạng thái phiên** do một hàm Go duy nhất dựng điều kiện SQL, mọi truy vấn dùng chung:
- `ended`: `revoked_at IS NOT NULL`, hoặc `desktop_seen_at <= now() - 60s`, hoặc chờ Cho phép quá 2 phút
  (`approved_at IS NULL AND claimed_at <= now() - 2m`), hoặc `approved_at <= now() - 12h`.
- `waiting`: chưa ended và `claimed_at IS NULL`.
- `pending`: chưa ended, `claimed_at IS NOT NULL`, `approved_at IS NULL`.
- `claimed`: chưa ended và `approved_at IS NOT NULL`.

**Mã xác minh** 4 chữ số lấy từ `phone_secret_hash` (hai byte đầu, mod 10000, đệm 0). Không lưu cột riêng. Máy tính
và điện thoại cùng hiện mã này để thu ngân biết đúng điện thoại nào đang xin kết nối.

## API

Phía máy tính dùng nhóm tenant đã đăng nhập (`TenantAPI`, có Maintenance, Entitlement và SupportReadOnly). Mỗi phiên
gắn `created_by` và `login_session_id`. Chỉ người tạo mới đọc, nghe, cho phép hay ngắt được phiên đó (người khác nhận 404).

| Route | Việc |
|-------|------|
| `POST /scanner-sessions` | Tạo phiên và mã nhận. Trả `{id, claim_code, claim_expires_at}`. Mã chỉ trả một lần. Người dùng đã có 3 phiên chưa kết thúc thì phiên cũ nhất bị ngắt (`revoked_by = 'system'`). |
| `POST /scanner-sessions/{id}:refresh-code` | Phiên `waiting`: thay mã, hạn mới 2 phút. Trạng thái khác: 409 `already_claimed`. |
| `POST /scanner-sessions/{id}:approve` | Phiên `pending`: đặt `approved_at`, phát `approved` tới điện thoại. Trạng thái khác: 409 `not_pending`. |
| `POST /scanner-sessions/{id}:reject` | Phiên `pending`: xóa băm bí mật, `claimed_at`, nhãn; tạo mã nhận mới; phát `rejected` tới điện thoại; trả `{claim_code, claim_expires_at}`. |
| `GET /scanner-sessions/{id}` | `{state: waiting|pending|claimed|ended, device_label?, verify_code?, claimed_at?, approved_at?}`. |
| `GET /scanner-sessions/{id}/events` | SSE: `state` (ngay khi mở), `claim_requested`, `scan`, `revoked`, `replaced`, `restarting`, heartbeat. Phiên đã kết thúc trả 204. |
| `POST /scanner-sessions/{id}/scans/{scanId}/result` | Máy tính báo kết quả `{outcome, sku_id?, unit_id?}`. Quá `expires_at`: 409 `scan_expired`. |
| `DELETE /scanner-sessions/{id}` | Ngắt từ máy tính. |

Phía điện thoại dùng `TenantPublic` (token CSRF của host), xác thực bằng cookie bí mật của phiên. Mọi handler ghi
(POST, DELETE) gọi `InMaintenance` và `CanWrite` với tenant của host trước khi chạm DB, trả 423 `tenant_maintenance`
hoặc 403 `subscription_read_only` như middleware.

| Route | Việc |
|-------|------|
| `POST /scan/claim` | `{code}`. Nhận mã, đặt cookie, trả `{state: "pending", verify_code}`. Mã sai, hết hạn, đã dùng hoặc của tenant khác đều trả cùng một lỗi 410 `claim_invalid`. |
| `GET /scan/session` | `{state: pending|claimed|ended, verify_code?}`, dùng khi mở lại trang. |
| `GET /scan/events` | SSE cho điện thoại: `state` (ngay khi mở), `approved`, `rejected`, `revoked`, `ended`, `restarting`, heartbeat. |
| `POST /scan/scans` | `{code}` + header `Idempotency-Key`. Phiên `pending`: 409 `scan_pending_approval`. Chờ tối đa 4 giây rồi trả `ScanResult`. Cùng khóa khác mã: 409 `idempotency_key_reused`. |
| `DELETE /scan/session` | Ngắt từ điện thoại và xóa cookie. |

`ScanResult` có `additionalProperties: false`, gồm `outcome` (enum `added`, `not_found`, `not_accepting`,
`needs_desktop_edit`, `new_product`, `not_in_order`, `desktop_unavailable`, `timeout`), `product_name?` và `unit_name?`.
Cookie hỏng, phiên bị ngắt, bị từ chối hoặc hết hạn trả 401 `scan_session_ended`.

## Các bước

1. **Migration 000060** theo mục Bảng. Chạy lint migration và test đảo chiều.
2. **Mã nhận và bí mật.** `crypto/rand.Text()` cho cả hai (ít nhất 128 bit). Chỉ lưu `sha256`. Tạo phiên: lưu băm mã,
   `claim_expires_at = now + 2 phút`, `desktop_seen_at = now`, `login_session_id = Principal.SessionID`. Trong cùng tx,
   khóa các phiên chưa kết thúc của người tạo (`FOR UPDATE`) và ngắt phiên cũ nhất nếu đã có 3, phát `revoked` cho nó.
3. **Nhận mã.** Lấy tenant từ host, từ chối `uuid.Nil`, kiểm tra bảo trì. Một tx ngắn chạy
   `UPDATE … SET claim_code_hash = NULL, claim_expires_at = NULL, phone_secret_hash = $2, claimed_at = now(), device_label = $3
   WHERE claim_code_hash = $1 AND claimed_at IS NULL AND claim_expires_at > now() AND <chưa ended> RETURNING id`.
   Không có dòng nào thì trả `claim_invalid`. Có thì đặt cookie và phát `claim_requested{device_label, verify_code}` tới
   máy tính. Cookie tên `__Host-tally_scan` ở prod và `tally_scan` ở dev (thêm vào `CookieNames`), HttpOnly, Secure theo
   môi trường, SameSite=Strict, `Path=/`, không có `Domain`, hết hạn sau 12 giờ. Khi đọc, server thử mọi cookie trùng
   tên trong yêu cầu, để một cookie rác đặt từ subdomain khác không che cookie thật.
4. **Cho phép và từ chối.** `:approve` và `:reject` theo bảng API, mỗi lệnh một tx ngắn có điều kiện `pending`. Phiên
   `pending` quá 2 phút tự thành `ended` theo điều kiện trạng thái.
5. **Nhãn thiết bị.** Rút gọn User-Agent thành dạng “iPhone · Safari” hoặc “Android · Chrome”. Không lưu User-Agent gốc.
   Nhãn chỉ để tham khảo; mã xác minh mới là thứ thu ngân đối chiếu.
6. **Hub.** Interface:
   - `Subscribe(key, role) (<-chan Event, cancel func(), error)`. Mỗi subscriber có một token riêng. `cancel` chỉ xóa
     mục trong map khi token ở đó vẫn là của chính nó, để handler cũ thoát muộn sau F5 không xóa luồng mới.
   - `Publish(key, role, Event) bool`: gửi không chặn vào kênh đệm 16.
   - `Await(scanID) (<-chan ScanResult, cancel func())`: kênh đệm 1. Handler luôn `defer cancel()`, và `cancel` xóa waiter.
   - `Resolve(scanID, ScanResult)`: gửi không chặn rồi xóa waiter; không có waiter thì bỏ qua.
   - `Close(key)`: đóng hai luồng và dọn waiter của phiên. `CloseAll()`: gửi `restarting` tới mọi luồng, đóng chúng,
     dọn mọi waiter và đặt cờ đã đóng; `Subscribe` sau đó trả lỗi để handler trả 503.
   - Giới hạn: tối đa 20 luồng mỗi tenant và 2000 luồng toàn tiến trình. Vượt thì `Subscribe` trả lỗi và handler trả 503
     `stream_limit`.
   Bản trong bộ nhớ dùng một mutex và không bao giờ gửi vào kênh khi có thể chặn trong lúc giữ mutex. Một luồng mới của
   cùng phiên và vai trò thay luồng cũ, luồng cũ nhận `replaced`.
7. **SSE.** Đặt `Content-Type: text/event-stream`, `Cache-Control: no-store`, flush header ngay, rồi gửi `state` với
   trạng thái hiện tại (cả lần kết nối lại tự động của `EventSource`, để client không lỡ `claim_requested` hay
   `approved`). Trước mỗi lần ghi gọi `http.NewResponseController(w).SetWriteDeadline(now + 30s)`. Gửi `: ping` mỗi 15
   giây. Vòng lặp ghi sự kiện không bao giờ chờ DB:
   - Luồng máy tính: ở mỗi nhịp, một goroutine riêng (timeout 2 giây, bỏ nhịp nếu nhịp trước chưa xong) kiểm tra lại
     phiên đăng nhập bằng `ResolveSession` với cookie của chính yêu cầu SSE, rồi cập nhật `desktop_seen_at` nếu đã cũ hơn
     30 giây và tenant không đang bảo trì. Phiên đăng nhập hết hoặc bị thu hồi (đăng xuất, “Đăng xuất mọi thiết bị”) thì
     ngắt phiên quét (`revoked_by = 'system'`), phát `revoked` tới hai bên và đóng. Ba lần cập nhật lỗi liên tiếp thì đóng
     luồng để client kết nối lại.
   - Luồng điện thoại: mỗi nhịp kiểm tra trạng thái theo cách tương tự; `ended` thì gửi `ended` rồi đóng.
   - Mở luồng cho phiên đã kết thúc trả 204, để `EventSource` ngừng kết nối lại.
8. **Gửi mã.** Xác thực cookie, kiểm tra bảo trì, lấy phiên `claimed`. Đọc khóa bằng `httpx.IdempotencyKey(c, body)`
   và kiểm tra mã bằng `catalog.ValidateBarcode` qua cổng. Trình tự bắt buộc:
   1. Một tx ngắn: `INSERT … (expires_at = now() + 10s) ON CONFLICT (tenant_id, session_id, idempotency_key) DO NOTHING`,
      đọc lại dòng, commit. `request_hash` khác thì trả 409 `idempotency_key_reused`. Dòng đã có `result` thì trả ngay.
      Dòng đã quá `expires_at` thì trả `timeout`.
   2. Ngoài mọi tx: `Await(scan_id)` để đăng ký waiter **trước**, rồi `Publish(scan{scan_id, code, ttl_ms})` với `ttl_ms`
      là thời gian còn lại tới `expires_at`. `Publish` trả false thì hủy waiter và trả `desktop_unavailable`.
   3. Chờ tối đa 4 giây hoặc tới khi yêu cầu bị hủy, không giữ connection DB nào, rồi trả kết quả hoặc `timeout`.
   `desktop_unavailable` và `timeout` không được lưu. Lần gửi lại với cùng khóa phát lại cùng `scan_id`, và máy tính tự
   lọc trùng theo `scan_id` (phase 3). Sau `expires_at`, server không phát lại và từ chối kết quả đến muộn.
9. **Máy tính xác nhận.** Kiểm tra phiên thuộc người gọi và `scan_id` thuộc phiên. Chỉ nhận `added`, `not_found`,
   `not_accepting`, `needs_desktop_edit`, `new_product`, `not_in_order`. Với `added`, đọc tên hiển thị của SKU và tên đơn
   vị qua cổng catalog dưới RLS (SKU hay đơn vị không thuộc tenant thì 422). Một tx ngắn
   `UPDATE … SET result, resolved_at WHERE id = $1 AND result IS NULL AND expires_at > now()`: không có dòng nào vì đã có
   kết quả thì trả kết quả cũ (lần xác nhận đầu thắng), vì quá hạn thì 409 `scan_expired`. Commit rồi mới
   `Resolve(scan_id, result)`.
10. **Ngắt.** Đặt `revoked_at`, `revoked_by`, xóa băm mã và băm bí mật. Phát `revoked` tới cả hai vai trò rồi `Close`.
    Ngắt từ điện thoại còn xóa cookie.
11. **Giới hạn tần suất.** Limiter riêng theo IP cho `POST /scan/claim` (10 lần mỗi phút, burst 5), `POST /scan/scans`
    (5 lần mỗi giây, burst 10), và mở luồng `…/events` ở cả hai phía (10 lần mỗi phút, burst 5). Tạo phiên, làm mới mã,
    cho phép và từ chối dùng limiter theo IP (6 lần mỗi phút). Số luồng đồng thời do giới hạn của hub ở bước 6 chặn, số
    phiên do giới hạn 3 phiên mỗi người ở bước 2 chặn.
12. **Tắt máy chủ.** Server `api` truyền `hub.CloseAll` làm `onShutdown`. Luồng nhận `restarting` rồi đóng, nên không
    giữ `Shutdown` hết 15 giây, và client coi đây là gián đoạn tạm thời chứ không phải kết thúc phiên. Phiên vẫn nằm
    trong bảng; `desktop_seen_at` chịu được 60 giây gián đoạn.
13. **Dọn dữ liệu.** `purgeScannerSessions` chạy trong cùng vòng `every` với `purgeIdempotency`, không thêm tiến trình
    nào. Mỗi tenant đang hoạt động, một tx xóa phiên có `revoked_at < now() - 24h OR desktop_seen_at < now() - 24h`
    (`scanner_scans` xóa theo `ON DELETE CASCADE`). Tenant không còn hoạt động giữ dữ liệu như các bảng khác của nó.
14. **OpenAPI** cho mọi route và schema. `POST /scan/scans` dùng tham số `IdempotencyKey` sẵn có và liệt kê lỗi
    `idempotency_key_reused`, `scan_pending_approval`, `scan_session_ended`. Chạy `openapi_contract_test.go`.

## Validation

- `go test ./internal/scanner/...`:
  - Hub: thay luồng; `cancel` muộn của luồng cũ không xóa luồng mới (Subscribe A, Subscribe B, cancel A, Publish tới B);
    gửi không chặn; sau timeout và sau hủy context map waiter rỗng; `Resolve` muộn không treo; `CloseAll` gửi
    `restarting` và chặn `Subscribe` mới; giới hạn luồng theo tenant và tổng.
  - Nhãn thiết bị, mã xác minh ổn định theo băm, kiểm tra outcome, điều kiện trạng thái.
- `make test-integration` với `scanner_integration_test.go` dùng testkit (`NewTenant`, `InTenant`, `Do`):
  - Vòng đời: tạo phiên, điện thoại nhận mã (phiên ở `pending`, mã xác minh khớp hai bên), máy tính cho phép, điện
    thoại gửi mã, máy tính xác nhận từ một connection khác, và điện thoại nhận `added` có tên và đơn vị. Chạy đồng thời
    10 lần gửi để chứng minh yêu cầu đang chờ không giữ pool.
  - Từ chối: điện thoại nhận `rejected`, cookie cũ trả 401, QR mới dùng được. Gửi mã khi `pending` trả 409.
    `pending` quá 2 phút thành `ended`.
  - Ngắt từ mỗi bên; 204 cho luồng của phiên đã kết thúc; người dùng tạo phiên thứ 4 thì phiên cũ nhất bị ngắt.
  - Mã hết hạn (đồng hồ giả), mã đã dùng, nhận lần hai bởi điện thoại khác, và mã cũ sau `refresh-code` đều trả
    `claim_invalid`.
  - Cách ly tenant: mã của tenant A gửi lên host tenant B trả `claim_invalid`; cookie của tenant A dùng trên host B trả 401.
  - Người dùng khác cùng tenant không đọc, nghe hay ngắt được phiên (404).
  - Phiên đăng nhập bị thu hồi thì nhịp kế tiếp ngắt phiên quét và điện thoại nhận `ended`.
  - Tenant đang bảo trì: `POST /scan/claim` và `POST /scan/scans` trả 423, nhịp SSE không ghi `desktop_seen_at`.
  - `desktop_seen_at` cũ hơn 60 giây làm phiên kết thúc; quá 12 giờ sau khi cho phép cũng vậy.
  - Idempotency: hai lần gửi cùng khóa chỉ phát một `scan_id` và trả cùng kết quả; cùng khóa khác mã trả 409; kết quả
    đến sau `expires_at` trả 409 `scan_expired`.
  - Cookie trùng tên: một cookie rác đứng trước cookie thật vẫn xác thực được.
  - Giới hạn tần suất: vượt ngưỡng ở `claim`, `scans` và mở luồng trả 429 có `Retry-After`.
  - Phản hồi cho điện thoại: so khóa JSON với tập `{outcome, product_name, unit_name}`, và khẳng định thân phản hồi không
    chứa giá hay tồn của SKU mẫu.
  - SSE qua `httptest.Server` có `WriteTimeout` 1 giây: luồng vẫn sống quá 3 giây nhờ gia hạn deadline. Mở lại luồng thì
    nhận `state` đầu tiên.
  - Dọn dữ liệu: phiên ngắt và phiên chết ngầm quá 24 giờ bị xóa cùng `scanner_scans`, phiên còn sống giữ lại.
- `make test`, `golangci-lint run`, test migration (lint, đảo chiều, catalog guard).

## Rủi ro

- Hub trong bộ nhớ hỏng khi chạy hơn một bản `api`. Prod hiện có một bản. Interface `Hub` cho phép đổi sang LISTEN/NOTIFY
  sau này. Phase 7 ghi ràng buộc “chỉ chạy một bản `api`” vào `docs/runbook.md`, vì repo không có tài liệu kiến
  trúc riêng.
- Mỗi yêu cầu gửi mã giữ một goroutine tối đa 4 giây nhưng không giữ connection DB. Limiter theo IP chỉ giới hạn tốc độ
  (khoảng 20 yêu cầu đang chờ mỗi IP ở 5 lần mỗi giây); số luồng SSE do giới hạn của hub chặn.
- Ghi `desktop_seen_at` tối đa mỗi 30 giây cho mỗi tab đang mở, ngoài vòng lặp ghi sự kiện. Mỗi người dùng có tối đa 3
  phiên.
- Bước Cho phép thêm một lần bấm trên máy tính cho mỗi lần ghép đôi. Đây là đánh đổi người dùng đã chọn để chống chiếm
  phiên qua QR bị chụp.

## Bảo mật

- Mã nhận và bí mật điện thoại chỉ lưu bản băm. Mã nhận nằm trong fragment URL (phase 3, 4) nên không vào log máy chủ hay
  Referer. Log của API không ghi thân yêu cầu `claim`.
- Ai quét được QR cũng chỉ đến được trạng thái `pending`; không gửi được mã nào cho tới khi máy tính bấm Cho phép sau
  khi đối chiếu mã xác minh.
- Mọi lỗi nhận mã đều giống nhau, để không dò được mã nào tồn tại hay thuộc tenant nào.
- Phản hồi cho điện thoại chỉ dựng từ `outcome`, tên SKU và tên đơn vị. Không trường nào do máy tính gửi được chuyển
  thẳng sang điện thoại.
- Phiên quét chết theo phiên đăng nhập của người tạo, và route ghi phía điện thoại tôn trọng bảo trì và gói của tenant.
- Cookie `__Host-` gắn đúng host của tenant. RLS chặn đọc chéo tenant ngay cả khi code lỗi.

## Rollback

Hoàn tác code và route. Prod không chạy `down`: nếu cần gỡ bảng thì khôi phục từ bản sao lưu của `make prod-migrate`,
hoặc để hai bảng lại (không code nào đọc nữa).

## Việc cần làm

- [x] Migration `000060` (bảng, RLS, quyền, đảo chiều)
- [x] Trạng thái phiên, mã nhận, bí mật, mã xác minh, giới hạn 3 phiên
- [x] Nhận mã, cho phép, từ chối, cookie `__Host-tally_scan`
- [x] Hub (token hủy, `Await` có hủy, `CloseAll` với `restarting`, giới hạn luồng)
- [x] SSE (`state` khi mở, nhịp ngoài vòng ghi, kiểm tra phiên đăng nhập)
- [x] Gửi mã với tx ngắn, `request_hash`, `expires_at`; xác nhận của máy tính
- [x] Kiểm tra bảo trì và gói cho route điện thoại
- [x] Limiter, `onShutdown` trong `serve.go`, `purgeScannerSessions`
- [x] OpenAPI và test ở mục Validation
