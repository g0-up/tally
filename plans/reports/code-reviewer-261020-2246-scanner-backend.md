# Review backend: quét mã vạch bằng điện thoại, phase 1 và 2

Ngày: 2026-10-20. Phạm vi: chỉ đọc, không sửa file nào.

## Phạm vi

- File: `apps/api/internal/scanner/*` (mới), `internal/catalog/{code_lookup.go,code_variants.go,skus.go,http.go,errors.go}`, `internal/sales/pos_bootstrap.go`, `internal/app/{app.go,domains.go,routes.go}`, `internal/cli/{serve.go,worker.go}`, `internal/platform/middleware/session.go`, migrations `000059`, `000060`, `api/openapi.yaml`, cùng các test.
- LOC: khoảng 804 dòng thêm vào file đã có (16 file), khoảng 5.000 dòng trong file mới (gồm cả test).
- Trọng tâm: các route điện thoại công khai, đồng thời và vòng đời SSE, tra mã của phase 1, đối chiếu OpenAPI.
- Scout: xem phần "Edge case tìm được khi scout".

## Đánh giá tổng thể

Code chặt chẽ ở những điểm quan trọng nhất: chỉ lưu sha256 của secret, claim là một câu UPDATE có điều kiện, cookie `__Host-` với SameSite=Strict, cổng duyệt (approval) chặn scan, không giữ transaction khi đang chờ, hub dùng một mutex và gửi không chặn, RLS FORCE trên các bảng mới. Không có lỗi Critical. Có 1 lỗi High mà người dùng thật sẽ gặp: điện thoại đã ghép bị ngắt kết nối ngay khi máy bàn xác thực lại mật khẩu. Ngoài ra có vài lỗi Medium về race và thứ tự kiểm tra.

## Critical

Không có.

## High

### H1. Xác thực lại (reauth) trên máy bàn thu hồi nhầm phiên quét

- Bằng chứng:
  - `internal/scanner/http.go:306` lấy cookie đăng nhập một lần lúc mở stream: `token, _ := c.Cookie(h.cookies.Session)`.
  - `internal/scanner/http.go:324-334` (`desktopCheck`) gọi `ResolveSession(token)` mỗi tick (10 giây). Khi `!found`, nó gọi `RevokeForSignOut` và gửi `revoked` với `by=system`.
  - `internal/identity/service.go:781` (Reauth) chạy `UPDATE sessions SET token_hash = $4 ...`: token đổi nhưng session id giữ nguyên. `ResolveSession` tìm theo `token_hash` (`internal/identity/sessions.go:25-60`), nên token cũ trả về `found=false`.
  - Hộp thoại reauth là bắt buộc khi đảo chứng từ bán hàng hoặc phiếu xuất (`internal/sales/http.go:86,91`), khi xác nhận điều chỉnh, đảo hoặc tính lại giá vốn tồn kho, kể cả điều chỉnh từ kiểm kê (`internal/inventory/http.go:61,63,78,587-596`), và khi đảo phiếu nhập (`internal/procurement/http.go:66`).
- Hậu quả: thu ngân đang dùng điện thoại để quét, trên máy bàn đảo một hóa đơn và nhập lại mật khẩu. Trong vòng khoảng 10 giây phiên quét bị thu hồi vĩnh viễn với `revoked_by=system`. Người dùng phải ghép lại từ đầu mà không biết lý do. Kiểm kê là luồng chính của tính năng này, và nó lại đi qua reauth.
- Cách sửa (chọn một):
  1. Tốt nhất: thêm port kiểm tra phiên đăng nhập theo ID, ví dụ `SessionActive(ctx, tenantID, userID, sessionID) (bool, error)`. Câu truy vấn kiểm tra `revoked_at IS NULL`, `expires_at > now()`, chưa quá idle TTL và user còn active, và không cập nhật `last_seen_at`. `desktopCheck` dùng `o.LoginSessionID` thay cho token. Cách này cũng sửa luôn M5.
  2. Tối thiểu: khi `!found`, chỉ kết thúc stream mà không thu hồi (ví dụ gửi `restarting`). Client mở lại EventSource bằng cookie mới. Nếu phiên thật sự đã đăng xuất thì request mới bị 401, và `desktopGrace` (60 giây) sẽ kết thúc phiên quét. Nhược điểm: đăng xuất thì phiên quét kết thúc chậm hơn khoảng 60 giây.
- Test cần thêm (integration): mở stream máy bàn, gọi reauth bằng cùng session, chờ hơn 1 tick, rồi kiểm tra phiên quét vẫn ở `claimed` và điện thoại vẫn quét được.

## Medium

### M2. Scan gửi lại hoặc gửi đồng thời có thể bỏ lỡ kết quả (thiếu kiểm tra sau khi đăng ký chờ)

- Bằng chứng: `internal/scanner/service.go:320-377`. `Scan` đọc kết quả đã lưu của dòng trong transaction, commit, sau đó mới `hub.Await(scanID)`. Nếu cùng `Idempotency-Key` được gửi lại khi scan_id đã publish (điện thoại retry vì mạng chậm), máy bàn có thể gọi `Resolve` đúng trong khoảng giữa lúc commit và lúc Await. Khi đó waiter không nhận được gì, và request trả về `timeout` sau 8 giây dù DB đã có kết quả.
- Hậu quả: điện thoại báo "hết giờ" cho một scan đã được thêm. Nếu người dùng quét lại thì sản phẩm bị cộng hai lần, trừ khi phase 3 khử trùng theo scan_id rồi gửi lại kết quả.
- Cách sửa: khi `Await` trả về timeout (hoặc ngay sau khi đăng ký Await), đọc lại dòng `scanner_scans` trong một transaction ngắn chỉ đọc. Nếu đã có `result` thì trả về kết quả đó. Thứ tự đúng là đăng ký, sau đó kiểm tra lại, sau đó chờ.

### M3. Route ghi của điện thoại kiểm tra trạng thái tenant trước khi xác thực cookie

- Bằng chứng: `internal/scanner/http.go:407-415` (claim), `446-455` (phoneRevoke), `523-531` (scan) đều gọi `writable()` trước khi xác thực cookie. `writable()` gọi `InMaintenance` và `CanWrite`, mỗi hàm là một `WithPlatformTx` không có cache (`internal/saas/entitlement.go:42-78`).
- Hậu quả:
  - Lộ thông tin: người lạ, chỉ cần biết slug tenant, phân biệt được tenant đang bảo trì (423) hay đang chỉ đọc vì chưa trả tiền (403 `subscription_read_only`) với tenant bình thường (401 hoặc 410).
  - Tải: mỗi request ẩn danh tạo 2 transaction trên DB platform. `DELETE /scan/session` không có limiter riêng, chỉ có limiter chung theo IP ở edge (`internal/app/app.go:197-211`).
- Cách sửa: với `phoneRevoke` và `scan`, xác thực phiên quét (chỉ đọc) trước, sau đó mới gọi `writable()`. Với `claim`, kiểm tra độ dài và định dạng code trước. Nếu muốn giữ thứ tự hiện tại thì phải ghi rõ đây là rủi ro đã chấp nhận.

### M4. Race với giới hạn 3 phiên mở cho mỗi user

- Bằng chứng: `internal/scanner/service.go:89-115` và `internal/scanner/repo.go:302-313`. `lockOpenSessions ... FOR UPDATE` chỉ khóa những dòng đã có. Với READ COMMITTED, hai `Create` đồng thời của cùng user đều đếm được 2 và cùng insert, nên vượt giới hạn 3.
- Hậu quả: thấp, vì chỉ vượt giới hạn tài nguyên chứ không vượt quyền. Vẫn nên sửa vì đây là bất biến mà plan đã nêu.
- Cách sửa: chạy `SELECT pg_advisory_xact_lock(hashtextextended('scanner_sessions:' || $1 || ':' || $2, 0))` theo tenant và user trước khi đếm, giống cách `insertBarcode` đã làm ở phase 1.

### M5. Tick SSE giữ phiên đăng nhập không bao giờ hết idle

- Bằng chứng: `desktopCheck` gọi `ResolveSession` mỗi 10 giây. `ResolveSession` cập nhật `sessions.last_seen_at` khoảng mỗi phút (`internal/identity/sessions.go`). `SessionIdleTTL` là 12 giờ (`internal/identity/model.go:48`).
- Hậu quả: chỉ cần một tab máy bàn để mở với phiên quét là phiên đăng nhập không bao giờ hết idle, nên chính sách idle mất tác dụng. Phiên vẫn bị chặn bởi TTL tuyệt đối 12 giờ của phiên quét, vì thế xếp mức Medium hoặc Low.
- Cách sửa: dùng port kiểm tra theo ID không chạm `last_seen_at`, như cách 1 của H1.

## Low / ghi nhận

- L1. `Approve` không gắn với mã xác minh mà máy bàn hiển thị (`internal/scanner/service.go:138-160`). Có thể thêm `verify_code` (tùy chọn) vào body approve và so khớp với mã đang lưu, để phòng thủ nhiều lớp khi một điện thoại khác chen claim. Kịch bản tấn công khá gượng ép, vì claim code chỉ dùng một lần và chỉ hiển thị trên màn hình.
- L2. `verify_code = uint16 % 10000` lệch phân bố một chút (`internal/scanner/model.go:192-197`). Không đáng kể với mục đích so mắt.
- L3. `phoneRevoke` bị chặn khi tenant bảo trì hoặc chỉ đọc (`internal/scanner/http.go:446-463`): điện thoại không tự ngắt được và cookie không bị xóa. Nên cho phép DELETE bỏ qua `writable()`, hoặc ít nhất vẫn xóa cookie.
- L4. Goroutine tick dùng ctx của request. Nếu client đóng đúng lúc, `RevokeForSignOut` có thể đã commit nhưng sự kiện `ended()` chưa publish tới điện thoại (`internal/scanner/sse.go:395-399`). Điện thoại sẽ biết ở tick sau, khi nhận 401. Ảnh hưởng nhỏ.
- L5. `Resolve` không kiểm tra trạng thái phiên: phiên vừa bị thu hồi vẫn resolve được scan trong khoảng 10 giây còn lại. Vô hại. `in.check()` bị gọi hai lần (`service.go:416`, `489`).
- L6. Rate limit là quyết định của plan, không đề xuất đảo ngược mà chỉ nêu trade-off. Limiter `Commands` là một instance dùng chung cho create, refresh, approve và reject, 6 lần mỗi phút theo IP và áp chung mọi tenant (`internal/scanner/http.go:36-41,104-115`). Một cửa hàng có nhiều quầy sau cùng một NAT có thể bị 429. Limiter `Events` cho 10 lần mỗi phút, burst 5. EventSource không tự kết nối lại sau 429, 503 hoặc 401, nên phase 3 và 4 phải tự xử lý. Hub giới hạn 20 stream mỗi tenant, tính cả hai vai (`internal/scanner/hub.go:77`), nên từ cặp ghép đồng thời thứ 11 trở đi sẽ nhận 503.
- L7. Bảng `scanner_scans` của một phiên chỉ bị chặn tăng bởi rate limit, cho tới khi purge chạy 24 giờ sau khi phiên kết thúc. Chấp nhận được.
- L8. Migration `000059` dùng `CREATE INDEX` không có `CONCURRENTLY`, nên khóa ghi `catalog_sku_barcodes` trong thời gian ngắn. Chấp nhận được với kích thước bảng hiện tại.
- L9. Hub chạy trong bộ nhớ, nên API chỉ được chạy một instance. Plan đã ghi rủi ro này, và runbook phase 7 cần nêu rõ.
- L10. Tải tick: mỗi tick máy bàn gồm một platform tx cho ResolveSession, một platform tx cho InMaintenance và một tx cho DesktopTick. Mỗi tick điện thoại tra cứu tối đa 4 lần. Ổn ở quy mô hiện tại.

## Edge case tìm được khi scout

- Xoay token khi reauth, trong khi session id giữ nguyên: dẫn tới H1.
- Gửi lại cùng idempotency key giữa commit và Await: dẫn tới M2.
- Request ẩn danh tới tenant đang bảo trì hoặc chỉ đọc: dẫn tới M3.
- Hai lần Create đồng thời của cùng user: dẫn tới M4.
- Tab SSE mở lâu: dẫn tới M5.
- Đã kiểm tra và không có lỗi:
  - Claim đồng thời hai lần: UPDATE có điều kiện, chỉ một lần thắng, lần còn lại nhận 410 đồng nhất.
  - Reject rồi dùng cookie cũ: `phone_secret_hash` bị đặt NULL nên trả 401.
  - Gửi lại scan với payload khác: `request_hash` không khớp nên trả 409.
  - Scan đã hết hạn: trả `timeout`.
  - Resolve hai lần: lần đầu thắng, lần sau nhận 409 `scan_expired`.
  - Hub đóng khi shutdown: Subscribe nhận `ErrHubClosed` và trả 503. Await sau khi đóng nhận kênh đã đóng.
  - Goroutine pump: buffer 1 và chỉ một tick chạy tại một thời điểm, nên không rò goroutine.
  - Cùng một barcode ở dạng UPC-E, UPC-A và EAN-13 giữa hai SKU: advisory lock cộng kiểm tra biến thể dưới RLS chặn được.

## Đã xác minh đúng (để hiệu chỉnh mức rủi ro)

- Secret: `rand.Text` (khoảng 130 bit), chỉ lưu sha256 và tra theo hash qua unique index `(tenant_id, hash)`, nên không có vấn đề timing. Claim là `UPDATE ... RETURNING` nguyên tử, trả 410 đồng nhất (`service.go:269-300`, `repo.go:367-382`). `maxClaimCodeLen=64`. Thử tối đa 4 cookie (`service.go:535-557`).
- Cookie: `__Host-tally_scan` ở prod, `tally_scan` ở dev (`middleware/session.go`), HttpOnly, Secure theo môi trường, SameSite=Strict, Path=/, không có Domain, MaxAge 43200 (`http.go:391-396`). Cookie bị xóa khi điện thoại tự thu hồi.
- Cổng duyệt: Scan yêu cầu `StateClaimed`, còn `pending` trả 409 (`service.go:333-339`). Trạng thái được tính từ một nguồn duy nhất là `endedSQL`/`stateSQL` (`repo.go:260-274`), và mọi chuyển trạng thái đều dùng UPDATE có điều kiện.
- Không giữ transaction khi chờ: `putScan` commit trước Await và Publish, Await chạy trước Publish. `desktop_unavailable` và `timeout` không được lưu. Resolve commit trước `hub.Resolve`. Tên sản phẩm lấy từ catalog dưới RLS (`domains.go`, `scannerCatalog`).
- Hub: dùng một mutex, gửi không chặn, giữ slot thứ 17 cho sự kiện kết thúc, cancel có token bảo vệ, không đóng kênh hai lần.
- SSE: bỏ `SetReadDeadline`, gia hạn write deadline trước mỗi lần ghi. `responseWriter` của gin v1.12.0 có `Unwrap`, nên `ResponseController` hoạt động. Không có middleware bọc writer. `onShutdown` được đăng ký qua `RegisterOnShutdown`, chỉ cho server API (`cli/serve.go`).
- Worker purge chạy theo từng tenant đang active, và vẫn tiếp tục khi purge idempotency lỗi (`cli/worker.go`).
- Cô lập tenant: migration `000060` có RLS ENABLE và FORCE, policy `tenant_isolation` và grant. Khóa của hub chứa TenantID. Điện thoại dùng tenant theo host và từ chối slug lạ. Chủ sở hữu phía máy bàn gồm tenant, user và phiên đăng nhập (`model.go:186-188`); người khác nhận 404.
- Phase 1:
  - `CodeVariants` đối xứng, gồm cả UPC-E có kiểm tra check digit và chuyển đổi khứ hồi. `canonicalCode` là dạng 14 chữ số.
  - `insertBarcode` là đường insert duy nhất (`skus.go:240-258`, được gọi từ `createSku` ở dòng 202 và `AddBarcode` ở dòng 859), có advisory xact lock và kiểm tra biến thể đã tồn tại dưới RLS.
  - `LookupCode` sắp xếp theo `array_position` nên khớp chính xác đứng đầu, chỉ lấy SKU, sản phẩm và đơn vị đang active, và khi không thấy barcode thì tra mã SKU theo đúng chuỗi đã gõ rồi tới dạng chữ hoa.
  - `/sku-lookup` không bị chặn theo module.
  - `PosTile.barcodes` dùng một truy vấn cho tối đa 30 SKU, có index `000059` trên `(tenant_id, sku_id)`.
- OpenAPI: mọi path mới đều có mặt và mã trạng thái khớp với handler. Claim: 200/403/410/422/423/429. `GET /scan/session`: 200/401. `DELETE /scan/session`: 204/401/403/423. `/scan/events`: 200/204/401/429/503. `/scan/scans`: 200/401/403/409/422/423/429, kèm tham số `Idempotency-Key`. Route máy bàn có các 409 `not_pending`, `already_claimed`, `scan_expired`. Có security scheme `scannerCookie`. `SkuBarcodeRef` có thêm `unit_name` so với plan (`{code, unit_id}`), và schema với handler nhất quán với nhau. Các route không có limiter riêng không liệt kê 429 của limiter chung, theo đúng quy ước của repo.

## Khuyến nghị theo thứ tự

1. Sửa H1 bằng port kiểm tra phiên theo ID (cách này sửa luôn M5) và thêm integration test cho reauth khi stream đang mở.
2. Sửa M2: sau khi Await timeout, đọc lại dòng scan.
3. Sửa M3: xác thực cookie trước `writable()` ở `scan` và `phoneRevoke`. Xử lý L3 cùng lúc.
4. Sửa M4 bằng advisory lock theo tenant và user.
5. Đưa L6 (rate limit dùng chung, giới hạn 20 stream mỗi tenant) cho người dùng quyết định. Không tự thay đổi.

## Số liệu kiểm tra

- `go vet` cho scanner, catalog, cli, app, sales: sạch.
- `go test -race -count=1` cho scanner, catalog, cli: pass.
- `go test -race -tags integration ./internal/scanner/...`: pass (22,6 giây).
- `go test -race -count=1 -tags integration` cho catalog, sales, cli, app (gồm `openapi_contract_test`): tất cả pass (12,0 giây, 1,1 giây, 367,9 giây, 210,1 giây).
- Độ phủ test: chưa đo.

## Câu hỏi còn mở

- Phase 3 có gửi lại kết quả khi máy bàn nhận scan_id trùng không? Câu trả lời quyết định M2 nghiêm trọng tới mức nào.
- Giới hạn 20 stream mỗi tenant và limiter lệnh 6 lần mỗi phút dùng chung theo IP có phù hợp với cửa hàng nhiều quầy không? Đây là quyết định của người dùng.
