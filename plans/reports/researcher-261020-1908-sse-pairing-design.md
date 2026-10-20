# Nghiên cứu: SSE + ghép đôi điện thoại cho Go API sau Traefik v3 và Cloudflare Tunnel

Ngày: 2026-10-20 · Người viết: researcher · Đầu vào: `plans/reports/brainstorm-261020-1826-phone-barcode-scanning.md`

## Kết luận trước

Thiết kế khả thi trên hạ tầng hiện có và đạt mục tiêu dưới 1 giây. Không cần đổi Traefik hay cloudflared. Có bốn việc
**bắt buộc** mà làm theo mặc định sẽ hỏng:

1. **`WriteTimeout: 60s` của API cắt luồng SSE đúng lúc 60 giây** (`apps/api/internal/cli/serve.go:266`). Handler SSE
   phải gọi `http.NewResponseController(w).SetWriteDeadline(...)`. Đã đo: không ghi đè thì luồng chết theo WriteTimeout,
   ghi đè thì luồng sống. `ReadTimeout` (30s ở API, 60s mặc định ở Traefik v3) **không** cắt luồng GET.
2. **EventSource ngừng hẳn, không tự nối lại, khi gặp 502/503/204/401.** Đã đo trên Chromium và Firefox. Khi `api`
   khởi động lại, Traefik trả 502 (hoặc 404 khi container biến mất khỏi Docker provider), và tab máy tính sẽ treo ở
   `readyState=CLOSED`. Phía web phải tự nối lại khi `CLOSED`, nếu không thì quy tắc "đứt < 60 giây vẫn giữ phiên"
   không bao giờ kích hoạt.
3. **`srv.Shutdown` sẽ chờ luồng SSE tới hết `shutdownTimeout` (15s)** rồi báo lỗi, vì luồng không bao giờ về trạng
   thái idle. Hãy đăng ký `srv.RegisterOnShutdown(hub.CloseAll)`.
4. **Heartbeat dạng comment mỗi 15 giây** và flush header ngay khi mở luồng. Timeout đọc của Cloudflare hiện là
   **125 giây** chứ không phải 100 giây. Mốc 15 giây còn dùng để cập nhật `desktop_seen_at`.

Khuyến nghị thiết kế, xếp hạng ở từng mục: hub trong bộ nhớ sau một interface nhỏ. Hạn 60 giây kiểm tra lười
(lazy) bằng cột `desktop_seen_at`, không cần sweeper. Mã nhận 128 bit đặt trong **fragment** của URL, chỉ lưu SHA-256,
nhận bằng một `UPDATE … RETURNING` nguyên tử. Bí mật điện thoại để trong **cookie HttpOnly** thay vì localStorage. Bảng
idempotency trong Postgres có unique `(session_id, key)`. Luồng gửi mã quét chạy theo kiểu **request–reply** (điện
thoại chờ máy tính xác nhận), vì hợp đồng yêu cầu điện thoại hiển thị trạng thái của màn trên máy tính.

## Bằng chứng tự đo (scratchpad, không đổi repo)

| Thử nghiệm | Kết quả |
|---|---|
| Go `http.Server{ReadTimeout:2s, WriteTimeout:3s}`, SSE 500ms/nhịp | Không ghi đè: chết ở 3.0s (`i/o timeout`). Chỉ `SetWriteDeadline(time.Time{})`: sống hết 6s. Chỉ ghi đè read deadline: vẫn chết. ReadTimeout không cắt luồng. |
| Client đóng sau khi ReadTimeout đã qua | `r.Context().Done()` vẫn kích hoạt ngay (2.5s) |
| Traefik `v3.6` (image trong homelab), `readTimeout=3s` | Luồng 8s đi qua trọn vẹn, mỗi sự kiện tới đúng 1s/lần |
| Traefik `writeTimeout=3s` | Luồng bị cắt ở 3.0s, backend thấy context bị hủy |
| Traefik `compress` mặc định, client gửi `Accept-Encoding: gzip` | Luồng **bị gzip** nhưng vẫn flush theo từng sự kiện (không dồn) |
| Traefik `compress{excludedContentTypes:[text/event-stream]}` | Không nén |
| EventSource, Chromium + Firefox (Playwright) | Server đóng sạch: tự nối lại sau `retry`, gửi `Last-Event-ID`. Lần nối lại nhận 502/503/204/401: `error` với `readyState=2`, **không thử lại nữa**. WebKit không chạy được vì máy thiếu thư viện. |

## 1. SSE trong Go/gin

**Header.** `Content-Type: text/event-stream` là bắt buộc: thiếu header này hoặc status khác 200 thì trình duyệt
"fail the connection" ([WHATWG SSE](https://html.spec.whatwg.org/multipage/server-sent-events.html)). Thêm
`Cache-Control: no-cache, no-transform`. `no-transform` chặn Cloudflare nén
([Cloudflare compression](https://developers.cloudflare.com/speed/optimization/content/compression/)), dù
`text/event-stream` vốn không nằm trong danh sách nén mặc định của Cloudflare. `X-Accel-Buffering: no` chỉ có tác
dụng với nginx ([nginx proxy module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)). Header này vô hại,
nên giữ để phòng sau này có nginx. Không đặt `Connection: keep-alive`, vì header này bị cấm trong HTTP/2.

**Flush và deadline.** `http.ResponseController` hoạt động xuyên qua gin. `gin@v1.12.0` có `responseWriter.Unwrap()`,
thỏa yêu cầu "ResponseWriter … have an Unwrap method" của `NewResponseController`
([pkg.go.dev net/http](https://pkg.go.dev/net/http#NewResponseController)). Repo không có middleware nào bọc writer.
Nên dùng deadline **trượt** thay vì vô hạn: trước mỗi lần ghi gọi `rc.SetWriteDeadline(time.Now().Add(30*time.Second))`.
Client treo (bộ đệm TCP đầy) sẽ bị cắt thay vì giữ goroutine mãi mãi. Tài liệu Go nói "A zero value means no deadline.
Setting the write deadline after it has been exceeded will not extend it", nên phải đặt deadline trước khi
WriteTimeout 60s của server hết, tức là ngay đầu handler
([Alex Edwards](https://www.alexedwards.net/blog/how-to-use-the-http-responsecontroller-type)).

**Khung handler khuyến nghị** (vòng lặp tự viết, không dùng `c.Stream`):

```go
rc := http.NewResponseController(c.Writer)
_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
h := c.Writer.Header()
h.Set("Content-Type", "text/event-stream"); h.Set("Cache-Control", "no-cache, no-transform"); h.Set("X-Accel-Buffering", "no")
c.Status(http.StatusOK)
fmt.Fprint(c.Writer, "retry: 2000\n\n"); _ = rc.Flush()      // header đi ngay, tránh 524
sub := hub.Subscribe(sessionID, RoleDesktop)                 // thay luồng cũ nếu có
defer sub.Close()
tick := time.NewTicker(15 * time.Second); defer tick.Stop()
for {
    select {
    case <-c.Request.Context().Done(): return                 // client đi (đã đo: kích hoạt ngay)
    case <-sub.Done(): writeEvent("revoked"/"replaced"); return
    case ev := <-sub.C: write "id: <seq>\nevent: scan\ndata: <json>\n\n"
    case <-tick.C:      write ": ping\n\n"; touchDesktopSeen(ctx, sessionID)
    }
    // trước mỗi lần ghi: rc.SetWriteDeadline(now+30s); sau ghi: rc.Flush(); lỗi thì return
}
```

**`c.Stream` và tự viết.** `c.Stream` dùng `CloseNotify()`
([gin context.go](https://github.com/gin-gonic/gin/blob/master/context.go)), và Go ghi chú interface này là
"Deprecated: … New code should use Request.Context instead"
([pkg.go.dev CloseNotifier](https://pkg.go.dev/net/http#CloseNotifier)). Hàm `step` còn phải tự chặn, nếu không sẽ
quay vòng liên tục. Vì vậy nên tự viết vòng `select` như trên. `c.SSEvent` dùng được để định dạng, nhưng không có `id:`
hay `retry:` riêng, nên tự viết chuỗi là đơn giản nhất.

**`retry:` và `id:`/`Last-Event-ID`.** Trình duyệt chỉ nhận `retry` khi giá trị toàn chữ số. Trình duyệt gửi lại
`Last-Event-ID` khi tự nối lại, và id rỗng thì reset ([WHATWG](https://html.spec.whatwg.org/multipage/server-sent-events.html)).
Đã đo: Chromium và Firefox đều gửi `Last-Event-ID` khi nối lại sau lúc server đóng sạch. Với thiết kế request–reply
ở mục 6–7, **không cần replay** theo `Last-Event-ID`: mã quét chưa được xác nhận thì điện thoại gửi lại. Vẫn nên gửi
`id:` (seq hoặc idempotency key) để desktop khử trùng.

**Ràng buộc riêng của repo.**
- Không giữ kết nối hoặc transaction pgx suốt luồng. Pool chỉ có 20 kết nối (`DB_MAX_CONNS`). Chỉ mở transaction RLS
  ngắn để kiểm tra lúc mở luồng và lúc cập nhật `desktop_seen_at`.
- Thêm `srv.RegisterOnShutdown(hub.CloseAll)`. `Shutdown` "waiting indefinitely for connections to return to idle",
  và `RegisterOnShutdown` dành cho các kết nối kiểu này ([pkg.go.dev Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown)).

## 2. Cloudflare Tunnel, Traefik v3 và SSE

**cloudflared không dồn SSE.** Hàm `shouldFlush` của cloudflared flush mỗi lần ghi khi response thiếu `Content-Length`,
dùng `Transfer-Encoding: chunked`, hoặc có `Content-Type` bắt đầu bằng `text/event-stream`
([cloudflared connection.go](https://github.com/cloudflare/cloudflared/blob/master/connection/connection.go)). Response
SSE của Go thỏa cả ba điều kiện. Giữ `disableChunkedEncoding=false`, là giá trị mặc định
([origin parameters](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/origin-parameters/)).
Các báo cáo "Cloudflare dồn SSE" trên cộng đồng phần lớn là lỗi của edge khác hoặc do nén
([Railway](https://station.railway.com/questions/edge-proxy-buffers-streaming-responses-4084b96c),
[Render](https://community.render.com/t/server-sent-events-and-cloudflare/3238)). Các báo cáo này cũ và không đáng tin
bằng mã nguồn.

**Timeout Cloudflare.** Bảng giới hạn hiện ghi "Proxy Read Timeout **125 seconds**" (chỉ Enterprise chỉnh được) và
"Proxy Write Timeout 30s" ([connection limits](https://developers.cloudflare.com/fundamentals/reference/connection-limits/),
[error 524](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-524/)).
Con số 100 giây trong yêu cầu đã cũ. Tài liệu không nói rõ timeout tính từ byte cuối hay chỉ tới header, nên phải flush
header ngay và gửi heartbeat. **Chọn 15 giây**: spec SSE gợi ý "every 15 seconds or so" cho proxy cũ, mức này nhỏ hơn
nhiều so với 125s, và đủ dày để `desktop_seen_at` phản ánh đúng trong cửa sổ 60s. Mỗi giờ tốn khoảng 240 dòng
`: ping\n\n` mỗi tab, không đáng kể. Kết nối trình duyệt tới Cloudflare idle 400s nên không ảnh hưởng.

**Nén.** Traefik `compress` **không** loại `text/event-stream` mặc định. Mã nguồn v3.6 chỉ loại sẵn `application/grpc`
([compress.go](https://github.com/traefik/traefik/blob/v3.6/pkg/middlewares/compress/compress.go),
[tài liệu compress](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/compress/)). Đã đo:
luồng bị gzip nhưng vẫn flush từng sự kiện. Router `tally-api` hiện **không** dùng `compress`
(`infra/docker-compose.homelab.yml:55`) và API không có gzip, nên không cần làm gì. Nếu sau này thêm `compress` thì
đặt `excludedContentTypes: [text/event-stream]`. Cloudflare không nén `text/event-stream` (không có trong danh sách),
và `no-transform` là lớp bảo hiểm thêm.

**Traefik `respondingTimeouts` (v3.6 thật trong homelab, `proxy/config/traefik.yml` không ghi đè).** Mặc định trong mã
nguồn là `readTimeout=60s`, `idleTimeout=180s`, `writeTimeout=0`
([static_config.go](https://github.com/traefik/traefik/blob/v3.6/pkg/config/static/static_config.go),
[entrypoints doc](https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/)). Đã đo `readTimeout`
không cắt luồng GET. `writeTimeout>0` thì cắt, nên phải giữ 0. `idleTimeout` chỉ áp dụng khi kết nối keep-alive rảnh
giữa các request. Không cần đổi cấu hình homelab.

## 3. Giới hạn của EventSource và cách báo phiên bị thu hồi

| Giới hạn | Tác động | Xử lý |
|---|---|---|
| Không gửi được header tùy ý, chỉ có `withCredentials` ([WHATWG](https://html.spec.whatwg.org/multipage/server-sent-events.html), [MDN](https://developer.mozilla.org/en-US/docs/Web/API/EventSource)) | Không dùng `Authorization` | Máy tính: cookie phiên same-origin (`/api/` cùng host), CSP `connect-src 'self'` đã cho phép. Điện thoại: cookie HttpOnly (mục 5). |
| HTTP/1.1: tối đa 6 kết nối mỗi trình duyệt + domain, "Won't fix" ([MDN](https://developer.mozilla.org/en-US/docs/Web/API/EventSource)) | Nhiều tab có thể nghẽn | Cloudflare bật HTTP/2 mặc định, gói Free không tắt được ([Cloudflare HTTP/2](https://developers.cloudflare.com/speed/optimization/protocol/http2/)); HTTP/2 mặc định cho 100 stream. Dev qua Vite HTTP/1.1 có thể gặp nếu mở nhiều tab. |
| Tự nối lại chỉ khi lỗi mạng hoặc server đóng sạch | Mất phiên khi restart | **Mọi** status khác 200 (502, 503, 204, 401…) làm kết nối ngừng hẳn, đã đo trên Chromium và Firefox. Phía web cần một lớp bọc: khi `readyState===CLOSED`, gọi `GET /scan-sessions/{id}`. Phiên còn sống thì tạo `EventSource` mới theo backoff 1–2–4s, tối đa 5s. Phiên đã thu hồi hoặc hết hạn thì xóa `sessionStorage` và hiện lại QR. |

**Báo thu hồi:** khi còn kết nối, gửi `event: revoked\ndata: {"reason":"…"}` rồi đóng. Client bắt sự kiện này và gọi
`es.close()`. Khi trình duyệt tự nối lại vào một phiên đã chết, server trả **204**. Spec định nghĩa 204 là "stop
reconnecting", nhưng EventSource không cho client thấy status, nên lý do phải lấy qua `GET` trạng thái. Với
`event: replaced` (tab khác chiếm luồng), client cũng gọi `es.close()`. Nếu không, hai tab dùng chung `sessionStorage`
(tab mở bằng `window.open` sao chép `sessionStorage` của opener, [MDN](https://developer.mozilla.org/en-US/docs/Web/API/Window/sessionStorage))
sẽ giành luồng của nhau liên tục.

## 4. Hub pub/sub trong bộ nhớ và quy tắc "đứt quá 60 giây"

**Interface** (đủ để sau này đổi sang LISTEN/NOTIFY):

```go
type Hub interface {
    Subscribe(sessionID uuid.UUID, role Role) *Subscription // role: desktop | phone; thay subscriber cũ cùng role
    Publish(sessionID uuid.UUID, role Role, ev Event) bool  // không chặn; false = không có subscriber hoặc buffer đầy
    Close(sessionID uuid.UUID, reason string)               // thu hồi: báo cả hai role rồi ngắt
    CloseAll()                                              // dùng cho RegisterOnShutdown
}
```

Bản in-memory: `map[key]*subscriber` (`key={sessionID, role}`), bảo vệ bằng `sync.Mutex`. Mỗi subscriber có `C chan Event`
buffer 16 và `done chan struct{}`. `Publish` dùng `select { case s.C <- ev: default: drop+close slow subscriber }` để
không bao giờ chặn request của điện thoại. `Subscribe` khi đã có subscriber thì `close(old.done)`, và handler cũ ghi
`event: replaced` rồi thoát. Unsubscribe chỉ xóa nếu con trỏ trong map vẫn là của chính nó, để tránh xóa nhầm
subscriber mới. Request–reply (mục 6) cần thêm `pending map[scanKey]chan Result` trong hub.

**Đổi sang Postgres LISTEN/NOTIFY sau này** (chỉ khi chạy nhiều hơn một bản `api`): dùng một kết nối riêng ngoài pool
để `LISTEN`, rồi `(*pgx.Conn).WaitForNotification(ctx)` ([pgx](https://pkg.go.dev/github.com/jackc/pgx/v5#Conn.WaitForNotification)).
`NOTIFY` chỉ giao khi transaction commit, payload dưới 8000 byte, và các payload trùng trong một transaction bị gộp
([PostgreSQL NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)), nên chỉ gửi id rồi đọc bảng. Repo đang
dùng `database/sql` (`db.go:63`), nên lúc đó cần thêm một `pgx.Conn` riêng. Rủi ro này nằm sau interface và chưa cần.

**Quy tắc 60 giây, chịu được restart: dùng kiểm tra lười, không cần sweeper.**

| Phương án | Chịu restart | Độ phức tạp | Đánh giá |
|---|---|---|---|
| **A. `desktop_seen_at` + kiểm tra lười trong mọi truy vấn** | Có (trạng thái nằm trong DB) | Thấp | **Chọn** |
| B. A + sweeper ghi `revoked_at` | Có | Thêm job ở `worker` | Chỉ để dọn dẹp, không cần cho tính đúng |
| C. Timer trong bộ nhớ | Không: restart làm mất timer | Thấp | Loại |

Cách làm: mở luồng thì đặt `desktop_seen_at=now()`, mỗi heartbeat 15s cập nhật lại, và đặt lần cuối khi luồng đóng.
Mọi thao tác (claim, gửi mã, mở luồng, `GET` trạng thái) dùng chung một vị từ:
`revoked_at IS NULL AND now() < hard_expires_at AND desktop_seen_at > now() - interval '60 seconds'`.
Phiên bị coi là chết thì ghi `revoked_at` ngay lúc phát hiện, để có lý do rõ ràng. API restart vài giây không làm mất
phiên, vì desktop nối lại (nhờ lớp bọc ở mục 3) trước khi qua 60 giây. Có thể giảm số lần ghi bằng cách cập nhật mỗi
30s, vẫn nằm trong cửa sổ 60s. Dọn hàng cũ (phiên quá 12 giờ, khóa idempotency) để cho job `worker` có sẵn.

## 5. Bảo mật giao thức ghép đôi

Tham chiếu: RFC 8628 (device flow), OWASP, và nghiên cứu USENIX Security 2025 về QR login. Nghiên cứu này thấy 43% số
site được thử dính ít nhất một trong sáu lỗi: SessionId không gắn, QrId dùng lại được, QrId đoán được, QrId do client
tạo, xác minh danh tính yếu, lộ dữ liệu
([USENIX'25 Zhang et al.](https://www.usenix.org/conference/usenixsecurity25/presentation/zhang-xin)).

| Biện pháp | Lý do và nguồn |
|---|---|
| Server sinh mã nhận bằng `crypto/rand.Text()` (26 ký tự base32, "at least 128 bits", [pkg.go.dev](https://pkg.go.dev/crypto/rand#Text)) | Chống lỗi đoán được và lỗi client tạo. RFC 8628 §5.2: "a very high entropy code SHOULD be used" ([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)). OWASP yêu cầu tối thiểu 64 bit ([Session Mgmt CS](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)). |
| Đặt mã trong **fragment**: `https://<slug>.sidecup.app/scan#c=<code>`. Trang đọc `location.hash`, gửi trong body POST, rồi `history.replaceState` để xóa fragment | Fragment "dereferenced solely by the user agent" ([RFC 3986 §3.5](https://www.rfc-editor.org/rfc/rfc3986#section-3.5)), và Referer "may not contain URL fragments" ([MDN Referer](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Referer)). Mã không lọt vào log của Cloudflare, Traefik hay request logger. Query string thì lọt vào log. |
| Chỉ lưu `SHA-256(code)`, không dùng bcrypt hay argon2 | Mã 128 bit ngẫu nhiên không cần hàm băm chậm. Hàm chậm dành cho mật khẩu ít entropy. OWASP: token "Stored securely … Single use and expire" ([Forgot Password CS](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html)). |
| Nhận mã nguyên tử: `UPDATE scan_sessions SET claimed_at=now(), claim_code_hash=NULL, phone_secret_hash=$2, phone_label=$3, hard_expires_at=now()+interval '12 hours' WHERE claim_code_hash=$1 AND claimed_at IS NULL AND claim_expires_at>now() AND revoked_at IS NULL RETURNING id` (trong transaction RLS theo tenant của host) | Read Committed: UPDATE chạy đồng thời phải chờ, rồi "WHERE clause is re-evaluated" trên bản đã cập nhật ([PostgreSQL 13.2.1](https://www.postgresql.org/docs/current/transaction-iso.html)), nên chỉ một điện thoại thắng. RLS tự loại mã của tenant khác. Xóa hash để chống dùng lại. |
| QR làm mới 2 phút: desktop gọi xoay mã, ghi đè hash | Mã cũ hết hiệu lực ngay. Hạn ngắn là điều nghiên cứu QRLjacking coi là chuẩn ([OWASP QRLJacking](https://community.owasp.org/attacks/Qrljacking)). |
| Bí mật điện thoại: `rand.Text()`, chỉ lưu SHA-256, trả qua **cookie `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/scan…; Max-Age=43200`** | OWASP: "Do not store session identifiers in local storage" ([HTML5 CS](https://cheatsheetseries.owasp.org/cheatsheets/HTML5_Security_Cheat_Sheet.html)). Cookie cho phép điện thoại dùng EventSource để biết bị ngắt ngay. CSRF ẩn danh theo host đã có sẵn cho POST. Nếu buộc phải để trong JS thì dùng `sessionStorage` (sống qua tải lại, mất khi đóng tab), không dùng `localStorage`. |
| So sánh | Tra theo hash qua index: timing chỉ lộ tiền tố của hash, vô dụng với kẻ tấn công. Chỉ khi so hai secret trong Go mới cần `subtle.ConstantTimeCompare` ([pkg.go.dev](https://pkg.go.dev/crypto/subtle#ConstantTimeCompare)). |
| Rate limit bằng `RateLimiter` có sẵn: claim theo IP (khoảng 10/phút), scan theo phiên (khoảng 10/s, burst 20), mở luồng theo phiên | RFC 8628 §5.1: "rate-limit … attempts". Với 128 bit, rate limit chủ yếu chống làm ngập hệ thống. |
| Chống chụp lại QR rồi dùng: hạn 2 phút, dùng một lần, xoay mã, desktop hiện **thiết bị đã ghép** (nhãn từ UA như "Android · Chrome", giờ ghép, lần quét cuối) và nút Ngắt | RFC 8628 §5.4 khuyên cho người dùng biết đang cấp quyền cho thiết bị. OWASP QRLJacking khuyên hiện "characteristic information about the session". Thêm bước bấm "Cho phép" trên desktop là mạnh nhất, nhưng đổi quyết định UX đã chốt (xem câu hỏi). |
| `Referrer-Policy: no-referrer` cho trang `/scan`, `Cache-Control: no-store` cho response claim | OWASP Forgot Password CS khuyên `no-referrer` cho trang nhận token. Repo đang dùng `strict-origin-when-cross-origin`. Fragment vốn không bị gửi, nên đây là phòng thủ thêm. |
| Response cho điện thoại chỉ trả tên + đơn vị | Lỗi "lộ dữ liệu ngoài ý muốn" trong USENIX'25. Hợp đồng cấm lộ giá và tồn kho. |

## 6. Idempotency cho POST mã quét

| Tiêu chí | **Bảng Postgres unique `(session_id, key)`** | LRU trong bộ nhớ |
|---|---|---|
| Chịu restart `api` | Có | Không: lần gửi lại sau restart thành dòng trùng |
| Phát lại response | Lưu `response jsonb`, `status` | Có, nhưng mất khi restart |
| Chi phí | Một `INSERT … ON CONFLICT DO NOTHING RETURNING` mỗi lần quét, lưu lượng rất nhỏ | Gần như 0 |
| Đi theo nhiều bản `api` sau này | Có | Không |
| Dọn dẹp | `ON DELETE CASCADE` theo phiên, cộng job `worker` | LRU tự đẩy ra |

**Chọn bảng Postgres** (có RLS như bảng phiên). Đây là cách Stripe và bản nháp IETF làm: client sinh UUID, server lưu
status + body của lần đầu và phát lại, công bố thời hạn lưu (Stripe: tối thiểu 24 giờ), và so payload để báo lỗi khi
dùng sai ([Stripe](https://docs.stripe.com/api/idempotent_requests),
[draft-ietf-httpapi-idempotency-key-header-07](https://www.ietf.org/archive/id/draft-ietf-httpapi-idempotency-key-header-07.html)).
Bản nháp đã hết hạn ngày 2026-04-18 và chưa thành RFC, nên chỉ coi là tham khảo về hình dạng.

Luồng request–reply: `INSERT` khóa với trạng thái `pending`. Nếu khóa đã tồn tại ở `pending` thì trả `409
scan_in_progress` (điện thoại thử lại sau). Nếu đã xong thì phát lại response đã lưu. Publish tới desktop rồi chờ
`ack` tối đa 3s. Có ack thì lưu response. Hết giờ thì **xóa** khóa và trả `504 desktop_no_ack`, để lần gửi lại được xử
lý lại. Desktop **phải** khử trùng theo key, giữ khoảng 200 key gần nhất trong bộ nhớ của tab: nếu desktop đã xử lý
nhưng ack bị mất, lần push lại chỉ ack lại kết quả cũ chứ không thêm dòng. Key là UUID v4 lấy từ
`crypto.randomUUID()` trên điện thoại. Cấm dùng mã vạch làm key, vì quét cùng một mã hai lần là hai lần quét hợp lệ.

## 7. Ngân sách độ trễ dưới 1 giây

Không có số đo trên đường thật. Các con số dưới đây là **ước lượng** để lập kế hoạch, cần đo lại khi chạy thử
trên homelab.

| Chặng | Fire-and-forget | Request–reply (khuyến nghị) |
|---|---|---|
| Điện thoại → Cloudflare edge → tunnel → Traefik → API (đã có kết nối HTTP/2 hoặc HTTP/3 sẵn) | 30–150 ms | 30–150 ms |
| API: xác thực secret, insert idempotency, tra mã vạch, kiểm tra hiệu lực | 5–20 ms | 5–20 ms |
| Hub → SSE → Traefik → cloudflared (flush ngay, đã xác minh) → edge → desktop | 30–150 ms | 30–150 ms |
| Desktop xử lý luồng Enter | <50 ms | <50 ms |
| Desktop POST ack → API → trả lời điện thoại | (không có) | 30–150 ms |
| **Tổng tới khi điện thoại có kết quả** | ~70–300 ms | **~150–500 ms** |

Cả hai đều nằm trong 1 giây ở điều kiện thường. Rủi ro lớn nhất là mạng di động chập chờn và request đầu tiên sau
lúc idle phải bắt tay TLS lại, chứ không phải mã nguồn. Request–reply gấp đôi số chặng mạng nhưng là cách duy nhất để
điện thoại hiện đúng "Màn hình trên máy tính không nhận mã quét" hoặc "Dòng này nhập theo thùng…". Hai thông báo này
phụ thuộc vào trạng thái nằm trong tab máy tính. Cách này cũng xác nhận được mã đã giao tới nơi, nên không cần replay
`Last-Event-ID`. Đo bằng cách ghi mốc thời gian lúc server nhận, lúc publish và lúc nhận ack vào log, cộng
`performance.now()` quanh `fetch` trên điện thoại. Tiêu chí chấp nhận: p95 dưới 1 giây.

## Ma trận quyết định tổng hợp

| Quyết định | Chọn | Phương án thay thế (xếp sau) | Rủi ro áp dụng |
|---|---|---|---|
| Kênh đẩy | SSE + EventSource tự bọc nối lại | WebSocket (cần hijack, auth phức tạp hơn); polling (tốn, chậm) | Thấp: chuẩn ổn định, gin và Go stdlib đủ dùng |
| Phân phối | Hub in-memory sau interface | LISTEN/NOTIFY (khi có hơn một bản `api`) | Thấp ở mức một container |
| Hạn 60 giây | `desktop_seen_at` + kiểm tra lười | Sweeper; timer in-memory (loại) | Thấp |
| Bí mật điện thoại | Cookie HttpOnly | `sessionStorage` + polling; `localStorage` (loại) | Thấp. Cần kiểm tra trên iOS Safari (ITP không ảnh hưởng cookie first-party). |
| Idempotency | Bảng Postgres | LRU (loại vì restart) | Thấp |
| Gửi mã quét | Request–reply + ack | Fire-and-forget + replay `Last-Event-ID` | Trung bình: thêm endpoint ack và map pending |

## Giới hạn của nghiên cứu

- Không đo được qua Cloudflare edge thật: cần token tunnel, mà phần này nằm ngoài phạm vi chỉ đọc. Hành vi của
  cloudflared dựa trên mã nguồn `master`, không phải đúng bản `2026.6.0` đang chạy.
- EventSource chỉ đo trên Chromium và Firefox. WebKit/Safari iOS chưa đo vì máy thiếu thư viện. Safari iOS cần chạy
  tay theo ma trận thiết bị, nhất là việc luồng bị treo khi màn hình khóa.
- Chưa tìm được tài liệu chính thức nào nói Proxy Read Timeout 125s tính giữa các byte hay chỉ tới header. Heartbeat
  15s an toàn trong cả hai trường hợp.
- Chưa xác minh bằng nguồn chính thức việc "Duplicate tab" của Chrome có sao chép `sessionStorage` hay không. MDN chỉ
  nói về tab có `opener`.
- Số liệu độ trễ là ước lượng, không phải số đo.

## Câu hỏi chưa giải quyết

1. Chọn request–reply (khuyến nghị) hay fire-and-forget? Brainstorm có thông báo phụ thuộc vào trạng thái của tab máy
   tính, nên nếu bỏ request–reply thì phải bỏ các thông báo đó hoặc chuyển sang báo không đồng bộ qua luồng của điện
   thoại.
2. Có thêm bước bấm "Cho phép" trên desktop khi điện thoại nhận mã không? Đây là biện pháp mạnh nhất chống ảnh QR bị
   chụp lại, nhưng thay đổi UX đã chốt ("máy tính hiện rõ … và ngắt được ngay").
3. Điện thoại biết bị ngắt bằng EventSource (dùng cookie, tức thì) hay polling 3s (đơn giản hơn ở server)? Khuyến nghị
   EventSource, vì hub đã có sẵn khái niệm role.
4. Thời hạn lưu khóa idempotency: theo phiên (tối đa 12 giờ) là đủ, hay cần lâu hơn?

Status: DONE_WITH_CONCERNS
Summary: Thiết kế khả thi dưới 1 giây trên hạ tầng hiện tại. Đã đo được bốn bẫy bắt buộc phải xử lý: WriteTimeout 60s
của API, EventSource ngừng hẳn khi gặp 5xx/204, Shutdown bị treo vì luồng SSE, và timeout 125s của Cloudflare.
Concerns: chưa đo qua Cloudflare edge thật và Safari iOS. Cách gửi mã quét (request–reply hay fire-and-forget) và bước
"Cho phép" trên desktop cần người dùng hoặc người lập kế hoạch quyết định.
