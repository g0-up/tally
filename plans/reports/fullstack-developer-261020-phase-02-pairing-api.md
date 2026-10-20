# Báo cáo triển khai: API ghép đôi máy quét bằng điện thoại

- Plan: `plans/261020-1227-phone-barcode-scanning/` — phase `phase-02-pairing-api.md`
- Trạng thái: hoàn thành, chưa commit (theo yêu cầu)
- Ngày: 2026-10-20

## Kết quả

Toàn bộ mục `## Việc cần làm` đã làm xong và mọi ca ở `## Validation` đều có test chạy qua: test đơn vị của hub,
service và HTTP (chạy với `-race`), cùng bộ test tích hợp `scanner_integration_test.go` trên Postgres thật và listener
thật để luồng SSE chạy đúng như production.

## File thay đổi

Tạo mới:

- `apps/api/migrations/000060_scanner_sessions.up.sql` / `.down.sql`: hai bảng `tally.scanner_sessions` và
  `tally.scanner_scans`, RLS `tenant_isolation` (ENABLE + FORCE), quyền cho `tally_app` và `tally_readonly`, FK
  `(tenant_id, session_id)` có `ON DELETE CASCADE`, unique `(tenant_id, session_id, idempotency_key)`.
- `apps/api/internal/scanner/`: `model.go`, `repo.go`, `hub.go`, `sse.go`, `service.go`, `http.go`, `device_label.go`
  và test `hub_test.go`, `service_test.go`, `http_test.go`, `scanner_integration_test.go`.

Sửa:

- `apps/api/internal/app/domains.go`: `Domains.Scanner`, adapter `scannerCatalog` gọi `catalog.ValidateBarcode` (sau khi
  trim) và đọc tên SKU / đơn vị qua `catalog.Service.Sku`.
- `apps/api/internal/app/app.go`: `Modules.Scanner`; `Maintenance` và `Entitlements` rỗng thì dùng mặc định
  (không bảo trì, được ghi).
- `apps/api/internal/app/routes.go` (ngoài danh sách file, xem mục Sai khác): đăng ký module trong `registerModules`.
- `apps/api/internal/app/openapi_contract_test.go`: fixture thêm `Scanner: scanner.NewHandler(nil)` để test hợp đồng
  OpenAPI thấy đủ route.
- `apps/api/internal/app/saas_control_integration_test.go`: control stack của test tích hợp gắn thêm module scanner
  (giới hạn tần suất nới rộng như `testAPIRateLimit`), để bài quét cách ly tenant gọi được route scanner thật.
- `apps/api/internal/app/tenant_isolation_integration_test.go`: `childKinds["scans"] = "scanner_scans"`, `childPairs`
  thêm `scanner_scans`, và store mẫu có một phiên scanner (đã ngắt, để luồng SSE trả 204 ngay) cùng một lần quét.
- `apps/api/internal/platform/middleware/session.go`: `Cookies.Scan` (`__Host-tally_scan` prod, `tally_scan` dev).
- `apps/api/internal/platform/middleware/middleware_test.go`: bảng `TestCookieNames` thêm `prod.Scan`.
- `apps/api/internal/cli/serve.go`: `namedServer.onShutdown`, `runHTTPServer` nhận `onShutdown` và gắn bằng
  `srv.RegisterOnShutdown`; server API truyền `Hub().CloseAll`.
- `apps/api/internal/cli/serve_test.go`: cập nhật lời gọi `runHTTPServer` và thêm
  `TestRunHTTPServerRunsOnShutdownBeforeDraining`.
- `apps/api/internal/cli/worker.go`: `purgeScannerSessions` chạy trong cùng nhịp `purge` sau khi dọn idempotency, độc lập
  với lỗi của bước trước.
- `apps/api/api/openapi.yaml`: tag `scanner`, security scheme `scannerCookie`, 13 path, schema `ScannerClaimCode`,
  `ScannerSessionCreated`, `ScannerSessionStatus`, `PhoneScannerSession`, `ScanResult`.
- `apps/web/src/shared/api/schema.d.ts`: sinh lại bằng `corepack pnpm gen:api`.

## Quyết định chính

- Trạng thái phiên chỉ tính từ timestamp qua một biểu thức SQL duy nhất (`endedSQL` / `stateSQL` trong `repo.go`). Mỗi
  vế của điều kiện `ended` tự chặn cột NULL, nên biểu thức không bao giờ là NULL và `NOT ended` đúng nghĩa "còn mở".
  Lỗi này được bộ test tích hợp bắt được (bản đầu dùng `NOT (... OR NULL)` nên không phiên nào khớp) và đã sửa.
- Mã nhận và bí mật điện thoại chỉ lưu SHA-256. Ngắt phiên xóa cả hai, nên cookie cũ của phiên bị ngắt trả 401.
- Mã xác minh: hai byte đầu của `phone_secret_hash` big-endian mod 10000, `%04d`.
- `desktop_seen_at` chỉ ghi khi đã cũ hơn 30 giây, và không ghi khi tenant đang bảo trì (nhịp đó dùng tx chỉ đọc).
- Gửi mã: ghi hàng `scanner_scans` trong một tx ngắn, rồi chờ hub tối đa 4 giây mà không giữ connection nào. Test chạy
  10 lần gửi đồng thời và khẳng định `db.Stats().InUse == 0` trong lúc chờ.
- `Resolve` đọc lần quét (phải thuộc phiên của chính người dùng, nếu không là 404) trước khi tra `sku_id`/`unit_id`
  của câu trả lời. Bài quét cách ly tenant bắt được thứ tự cũ: id lần quét của store khác cộng SKU lạ trả 422 thay vì
  404.
- Mỗi luồng SSE có nhịp kiểm tra riêng (mặc định 10 giây) chạy ngoài vòng ghi; ping mỗi 15 giây; `retry: 3000`. Deadline
  ghi được gia hạn 30 giây sau mỗi lần ghi, nên luồng sống qua `WriteTimeout` của server.
- Hub trong bộ nhớ: tối đa 20 luồng mỗi tenant và 2000 luồng tổng; buffer 16 sự kiện, gửi không chặn, sự kiện kết thúc
  luôn vừa buffer.

## Sai khác so với phase file và lý do

1. Đăng ký route nằm ở `internal/app/routes.go` (`registerModules`) chứ không ở `app.go`, vì mọi module khác đều đăng ký ở
   đó; `app.go` chỉ thêm trường `Modules.Scanner`.
2. `Hub.Await(key, scanID)` nhận thêm `Key` để `Close(key)` / `CloseAll` giải phóng đúng waiter của phiên.
3. Khi hệ thống ngắt phiên (đăng xuất, quá hạn), điện thoại nhận `ended`, còn máy tính nhận `revoked{by:"system"}`. Khi
   máy tính hoặc điện thoại tự ngắt, cả hai nhận `revoked{by}`.
4. `GET /scan/session` trả 200 `{state:"ended"}` cho phiên kết thúc theo thời gian (bí mật vẫn còn), còn phiên bị ngắt,
   bị từ chối hoặc cookie lạ trả 401 `scan_session_ended`.
5. Nhịp kiểm tra của máy tính gọi `ResolveSession` với cookie của request luồng, nên giữ cho phiên đăng nhập không bị hết
   hạn vì không hoạt động trong khi luồng còn mở.
6. Bốn lệnh `create`, `refresh-code`, `approve`, `reject` dùng chung một bucket 6/phút burst 6 theo IP. Luồng sự kiện của
   máy tính và của điện thoại có bucket riêng (mỗi bên 10/phút burst 5).
7. Test tích hợp dùng `http.Client` trên `httptest.Server` thay vì `testkit.Do`, vì SSE cần listener thật và
   `WriteTimeout`.

## Điều phase 3/4 cần biết

### Route của máy tính (host tenant, cần phiên đăng nhập owner/support, CSRF cho lệnh ghi)

Chỉ đúng người tạo và đúng phiên đăng nhập đã tạo mới thấy phiên; người khác nhận 404 `not_found`.

| Route | Thân gửi | Trả về |
|---|---|---|
| `POST /api/v1/scanner-sessions` | – | 201 `{id, claim_code, claim_expires_at}`, `Cache-Control: no-store`. Phiên mở thứ 4 của người dùng làm phiên cũ nhất bị ngắt (`revoked{by:"system"}`). |
| `POST /api/v1/scanner-sessions/{id}:refresh-code` | – | 200 `{claim_code, claim_expires_at}`; 409 `already_claimed` khi đã có điện thoại nhận. |
| `POST /api/v1/scanner-sessions/{id}:approve` | – | 200 `ScannerSessionStatus` (state `claimed`); 409 `not_pending`. |
| `POST /api/v1/scanner-sessions/{id}:reject` | – | 200 `{claim_code, claim_expires_at}` (mã mới, phiên về `waiting`); 409 `not_pending`. |
| `GET /api/v1/scanner-sessions/{id}` | – | 200 `{state, device_label?, verify_code?, claimed_at?, approved_at?}`. |
| `GET /api/v1/scanner-sessions/{id}/events` | – | SSE; 204 nếu phiên đã kết thúc. |
| `POST /api/v1/scanner-sessions/{id}/scans/{scanId}/result` | `{outcome, sku_id?, unit_id?}` | 200 `ScanResult`; trả lại kết quả cũ nếu đã xác nhận; 409 `scan_expired` khi quá 10 giây; 404 khi lần quét không thuộc phiên; 422 khi `outcome` sai (`oneof`), thiếu `sku_id`/`unit_id` với `added` (`required`) hoặc SKU/đơn vị không tồn tại (`not_found`). |
| `DELETE /api/v1/scanner-sessions/{id}` | – | 204 (gọi lại vẫn 204). |

`state` là một trong `waiting`, `pending`, `claimed`, `ended`. `outcome` máy tính được gửi: `added`, `not_found`,
`not_accepting`, `needs_desktop_edit`, `new_product`, `not_in_order`. Chỉ `added` mới có `product_name` và `unit_name`.

### Route của điện thoại (host tenant, không cần đăng nhập, CSRF cho lệnh ghi qua `GET /api/v1/host-context`)

| Route | Thân gửi | Trả về |
|---|---|---|
| `POST /api/v1/scan/claim` | `{code}` | 200 `{state:"pending", verify_code}` và đặt cookie; mọi lỗi là 410 `claim_invalid`. |
| `GET /api/v1/scan/session` | – | 200 `{state, verify_code?}`; 401 `scan_session_ended`. |
| `GET /api/v1/scan/events` | – | SSE; 204 nếu phiên đã kết thúc; 401 nếu cookie không hợp lệ. |
| `POST /api/v1/scan/scans` | `{code}` + header `Idempotency-Key` | 200 `{outcome, product_name?, unit_name?}`. Chờ tối đa 4 giây. `outcome` thêm `desktop_unavailable` (không có luồng máy tính) và `timeout`; hai giá trị này không được lưu, gửi lại cùng khóa sẽ hỏi lại máy tính với cùng `scan_id`. 409 `scan_pending_approval`, 409 `idempotency_key_reused`, 422 `idempotency_key_required`, 422 `validation_failed` (mã vạch sai định dạng). |
| `DELETE /api/v1/scan/session` | – | 204 và xóa cookie. |

Lệnh ghi của điện thoại trả 423 `tenant_maintenance` khi tenant bảo trì và 403 `subscription_read_only` khi gói không cho
ghi. Phản hồi cho điện thoại chỉ có các khóa `outcome`, `product_name`, `unit_name`: không bao giờ có giá hay tồn.

### Cookie

`__Host-tally_scan` ở production, `tally_scan` ở dev: HttpOnly, Secure theo môi trường, SameSite=Strict, Path=/, không
Domain, Max-Age 43200. Server đọc tối đa 4 cookie trùng tên, nên một cookie rác đứng trước cookie thật vẫn được.

### Sự kiện SSE

Mỗi sự kiện có dạng `event: <tên>` và `data: <json>`. Server gửi comment `: ping` mỗi 15 giây và `retry: 3000`. Sự kiện
đầu tiên khi mở luồng luôn là `state`.

- Máy tính: `state` (`ScannerSessionStatus`), `claim_requested` `{device_label?, verify_code}`,
  `scan` `{scan_id, code, ttl_ms}` (ttl_ms là 10000), `revoked` `{by: desktop|phone|system}`, `replaced` `{}` (tab khác mở
  luồng của cùng phiên), `restarting` `{}`.
- Điện thoại: `state` `{state, verify_code?}`, `approved` `{}`, `rejected` `{}`, `revoked` `{by}`, `ended` `{}`,
  `replaced` `{}`, `restarting` `{}`.

`revoked`, `rejected`, `ended`, `replaced` và `restarting` kết thúc luồng. Mở luồng có thể trả 503 `stream_limit` hoặc 503
`server_restarting`; EventSource không tự kết nối lại sau mã khác 200, nên phase 3/4 phải tự mở lại (có backoff), và cũng
phải tự mở lại sau `restarting`.

### Mã lỗi

| Mã | HTTP |
|---|---|
| `claim_invalid` | 410 |
| `scan_session_ended` | 401 |
| `already_claimed`, `not_pending`, `scan_pending_approval`, `idempotency_key_reused`, `scan_expired` | 409 |
| `stream_limit`, `server_restarting` | 503 |
| `tenant_maintenance` | 423 |
| `subscription_read_only` | 403 |
| `rate_limited` | 429, có `Retry-After` |

Giới hạn theo IP: claim 10/phút burst 5; scans 5/giây burst 10; mở luồng 10/phút burst 5 cho mỗi bên; bốn lệnh của máy
tính chung 6/phút burst 6.

### Thời hạn

Mã nhận sống 2 phút. Điện thoại chờ cho phép quá 2 phút thì phiên kết thúc. Luồng máy tính im quá 60 giây thì phiên kết
thúc. Phiên sống tối đa 12 giờ sau khi cho phép. Worker xóa phiên đã ngắt hoặc chết quá 24 giờ, cùng các lần quét.

## Kết quả kiểm thử

Tất cả đều chạy qua sau lần sửa cuối:

- `cd apps/api && go build ./... && go vet ./... && go vet -tags integration ./...`: sạch.
- `go test -race ./internal/scanner/... ./internal/app/... ./internal/cli/... ./internal/platform/middleware/... ./internal/catalog/...`: pass.
- `go test -tags integration -race -count=3 ./internal/scanner/`: pass cả ba lần (khoảng 18 giây mỗi lần), không flaky.
- `make test`: toàn bộ package Go pass (gồm test lint migration); web 160 file, 1153 test pass.
- `make test-integration` (chạy đủ `go test -race -count=1 -tags integration ./...`): exit 0, 51 package pass, gồm
  `internal/app` (bài quét cách ly tenant qua mọi route), `internal/scanner` và `migrations` (đảo chiều, catalog guard).
- `cd apps/web && corepack pnpm gen:api && corepack pnpm typecheck`: pass.
- `make lint`: golangci-lint 0 issues, eslint và tsc sạch.

Hai lỗi thật được test tích hợp tìm ra và đã sửa trong code (không nới test): biểu thức `ended` trả NULL cho phiên chưa
cho phép, làm `NOT ended` không khớp phiên nào (claim luôn 410, giới hạn 3 phiên không chạy); và thứ tự kiểm tra của
`Resolve` nói ở trên.

## Việc còn lại

Không có việc nào của phase này còn dở. Docs chưa cập nhật vì nằm ngoài phạm vi được giao; khi giao diện của phase 3/4
xong cần ghi route và luồng ghép đôi vào tài liệu kiến trúc.
