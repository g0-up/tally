# Review: quét mã vạch bằng điện thoại, phần web và edge

Ngày: 2026-10-20 · Người review: code-reviewer · Chế độ: chỉ báo cáo, không sửa mã nguồn

## Phạm vi

- Ghép nối trên máy tính: `apps/web/src/features/scanner/` (trừ `phone/`, `pages/`), `app/layouts/app-shell.tsx`, `features/pos/pages/pos-page.tsx`, `features/inventory/components/count-drawer.tsx`, `test/msw/scanner.ts`.
- Trang điện thoại: `features/scanner/pages/scan-page.tsx`, `features/scanner/phone/*`, `app/router.tsx`, `main.tsx`, `sw.ts`, `vite.config.ts`, `package.json` (uqr 0.1.3, barcode-detector 3.2.3, zxing-wasm 3.1.6).
- Edge: `infra/docker-compose.homelab.yml` (router `tally-web-scan`, middleware `tally-headers-scan`), `apps/web/nginx.conf`.
- Phần web của phase 1: `shared/lib/barcode.ts`, `features/pos/lib/scan-match.ts`, `features/catalog/api/sku-lookup.ts`.
- Đối chiếu contract với `apps/api/internal/scanner/{http,service,model,repo,hub}.go`.

Đã kiểm chứng:
- `vite build` vào scratchpad: build thành công. `barcode-reader-*.js` (43,6 kB) và `zxing_reader-*.wasm` (946.860 B) KHÔNG nằm trong precache của `sw.js`. Chunk `index-*.js` chính không có tham chiếu zxing nào. Reader chỉ được nạp lazy qua dynamic import.
- `corepack pnpm vitest run src/features/scanner src/features/pos src/features/inventory src/shared/lib`: 45 file, 337 test, tất cả pass.

## Đánh giá chung

Về bảo mật, route `/scan` ổn: claim code được đọc từ fragment rồi xoá ngay, không lưu ở đâu, không đi vào Referer. WASM tự host. SW bỏ qua `/scan`. CSP chỉ nới `'wasm-unsafe-eval'` cho đúng một path. Event và payload khớp với backend. Có 2 lỗi High mà jsdom và test đơn vị không bắt được: va chạm class CSS `.scan` làm hỏng layout cả trang điện thoại lẫn ô quét của POS và màn kiểm kê, và vòng thu hồi qua lại vô hạn khi một user mở từ 4 thẻ có thẻ ghép nối trở lên. Ngoài ra còn vài lỗi Medium về khoảng ttl, hồi quy luồng bàn phím POS, SW cũ và live region.

## Critical

Không có.

## High

### H1. Class `.scan` bị định nghĩa hai lần: trang điện thoại vỡ layout, ô quét của POS và màn kiểm kê bị đổi kiểu

Bằng chứng:
- `apps/web/src/shared/styles/globals.css:164`: `.scan { display: flex; gap: 10px; }` (rule cũ, dùng cho hàng ô quét: `features/pos/components/scan-input.tsx:15`, `features/inventory/components/count-drawer.tsx:229`).
- `apps/web/src/shared/styles/globals.css:579` (mới thêm trong diff): `.scan { max-width: 560px; margin: 0 auto; padding: 0 12px calc(24px + env(safe-area-inset-bottom)); min-height: 100%; box-sizing: border-box; }`.
- `features/scanner/pages/scan-page.tsx:276`: `<main className="scan">`.

Tác động: hai rule gộp lại trên mọi phần tử `.scan`.
- Trang điện thoại: `<main>` thành flex row, nên header `.scan-bar` (sticky) và `.scan-body` nằm cạnh nhau theo chiều ngang thay vì xếp dọc. Trên màn hình hẹp, camera và danh sách kết quả bị bóp lại.
- POS và màn kiểm kê: hàng ô quét bị giới hạn 560px, căn giữa (`margin: 0 auto`), có thêm padding hai bên và padding đáy 24px, cộng `min-height: 100%`. Bố cục quầy POS bị đổi.

Test jsdom không tính layout, và ảnh chụp visual có từ trước thay đổi này, nên CI không bắt được.

Cách sửa: đổi tên class gốc của trang điện thoại, ví dụ `<main className="scan-page">` và `.scan-page { max-width: 560px; ... }`, rồi giữ nguyên `.scan` cũ. Sau khi sửa, chụp lại `/sales/pos`, drawer kiểm kê và `/scan` ở viewport 375px.

### H2. Thu hồi phiên qua lại vô hạn khi một user có từ 4 thẻ hoặc thiết bị đang hiện thẻ ghép nối

Bằng chứng:
- `apps/api/internal/scanner/model.go:34-36`: `maxOpenSessions = 3`. `service.go:88-114`: mỗi lần Create, nếu đã có từ 3 phiên mở thì thu hồi phiên cũ nhất. `repo.go:64-79` (`lockOpenSessions`) đếm mọi phiên chưa kết thúc, kể cả phiên đã ghép với điện thoại.
- `apps/web/src/features/scanner/scanner-controller.ts:395-404`: handler `revoked` bỏ qua `by`, hiện thông báo 4 giây (`REVOKED_NOTICE_MS`, dòng 29), rồi gọi `reconcile()`. Nếu thẻ vẫn còn hiện thì hàm này lại `create()`.

Kịch bản: một tài khoản thu ngân dùng chung cho 4 quầy, hoặc 4 thẻ POS/kiểm kê cùng mở. Thẻ thứ 4 tạo phiên, phiên cũ nhất A bị thu hồi. Sau 4 giây A tạo lại và thu hồi B, rồi B thu hồi C, cứ thế mãi. Không thẻ nào ổn định được QR. Một điện thoại đã ghép cũng bị ngắt ngay khi phiên của nó thành phiên cũ nhất. Ngoài ra còn tốn một lượt ghi DB và SSE khoảng mỗi 4 giây.

Cách sửa (nên làm cả hai):
1. Web: đọc `by` trong payload `revoked`. Với `by: "system"` (thu hồi do giới hạn phiên), làm như `replaced`: không tự tạo lại, chỉ hiện nút "Ghép lại" để người dùng bấm.
2. API: khi vượt giới hạn, ưu tiên thu hồi phiên `waiting` (chưa ghép) trước, chỉ đụng tới phiên `claimed` khi không còn lựa chọn nào khác. Phần này thuộc backend, ngoài phạm vi review web, nhưng là gốc của vòng lặp.

## Medium

### M1. Khoảng 4 đến 10 giây: desktop vẫn áp dụng mã trong khi điện thoại đã báo hết giờ, dễ dẫn tới cộng hai lần

Bằng chứng: `model.go:30-33` có `scanTTL = 10s` và `scanWait = 4s`. `service.go:386` gửi `ttl_ms` bằng thời gian còn lại tới `expires_at` (khoảng 10 giây). Desktop dùng `ttl_ms` làm hạn chót. Điện thoại hiện "hết giờ" ở giây thứ 4.

Tác động: desktop đang bận (ví dụ lookup chậm) và trả lời ở giây 4 đến 10, khi đó Resolve vẫn thành công và mặt hàng được cộng mà không có toast nào trên desktop (toast chỉ hiện khi gặp `scan_expired`). Người dùng thấy điện thoại báo hết giờ nên quét lại, kết quả là cộng hai lần. Lớp chống trùng theo `scan_id` không che được trường hợp này vì lần quét lại có `scan_id` mới.

Đây là thiết kế đã chốt trong phase 02 bước 8, nên cần user quyết định. Hai lựa chọn:
- (a) Server gửi `ttl_ms = min(scanWait, remaining)`, để desktop bỏ qua mã đúng lúc điện thoại báo hết giờ.
- (b) Giữ 10 giây nhưng desktop hiện toast "Đã thêm (điện thoại báo hết giờ)" cho câu trả lời trễ quá `scanWait`, và điện thoại đổi chữ thành "Chưa rõ, kiểm tra trên máy tính trước khi quét lại".

### M2. Hồi quy luồng bàn phím POS: Enter giờ phải chờ lookup, chưa có chặn trùng, ô quét chỉ được xoá sau khi await xong

Bằng chứng: `features/pos/pages/pos-page.tsx:96-101`. `submitScan` gọi `await scanCode(code, "keyboard")`. Với mã không có trong 30 ô đang hiện, hàm này gọi `/sku-lookup`. Ô quét chỉ được xoá sau khi await xong (`setQ((cur) => (cur.trim() === code ? "" : cur))`).

Tác động:
- Máy quét USB bắn hai mã liên tiếp trong lúc lookup đang chạy: mã thứ hai được gõ nối vào phần text cũ ("AB"). Điều kiện `cur.trim() === code` sai nên ô không bị xoá, và lần Enter sau tra "AB", ra không tìm thấy.
- Bấm Enter hai lần nhanh với cùng một mã thì có hai lookup và hai lần cộng, vì không có cờ in-flight.
- Trước thay đổi, `addFirst` xoá ô quét đồng bộ.
- Mỗi lần Enter để tìm theo tên giờ cũng tốn một round trip `/sku-lookup` (422 rồi mới fallback).

Cách sửa: xoá ô quét ngay khi submit (lạc quan) và nhớ `code`. Nếu kết quả là `not_found` mà ô vẫn trống thì trả lại text. Thêm `Set` mã đang tra để bỏ qua Enter trùng. Chỉ gọi lookup khi `code` trông giống mã vạch hoặc mã SKU (không có khoảng trắng), tái dùng predicate trong `shared/lib/barcode.ts`.

### M3. Điện thoại đã cài SW cũ có thể bị kẹt bản `/scan` cũ, và "tải lại" không giải quyết được

Bằng chứng: SW cũ không có denylist cho `/scan`, nên trả `index.html` cũ từ cache. `main.tsx` không gọi `startStoreServices`/`UpdatePrompt` trên trang scan. `sw.ts:37-41` chỉ `skipWaiting` khi nhận message. Trình duyệt có tự cài SW mới, nhưng SW đó nằm ở trạng thái waiting, và reload không kích hoạt một SW đang waiting. Bản bundle cũ không có route `/scan` nên người dùng thấy trang 404 hoặc trang chủ.

Plan đã ghi rủi ro này (#9), nhưng cách xử lý chưa đủ. Cách sửa: trong `main.tsx`, nhánh `isScanPage`, nếu `navigator.serviceWorker.getRegistration()` có `waiting` thì gửi `{type:"SKIP_WAITING"}` rồi reload một lần. Lưu ý là bước này không cứu được bundle cũ. Bổ sung tài liệu hướng dẫn: "đóng hết các thẻ hoặc app Tally trên điện thoại rồi quét lại QR", không chỉ ghi "tải lại".

### M4. Live region của thẻ ghép nối bị mount lại mỗi khi đổi trạng thái, nên thông báo không đáng tin

Bằng chứng: `features/scanner/components/phone-pairing-card.tsx:57-59`, component `Status` (`role="status" aria-live="polite"`) được render trong từng nhánh view, ở các vị trí cây khác nhau. React tạo node mới mỗi lần đổi trạng thái (waiting, pending, claimed, revoked). Screen reader thường bỏ qua nội dung có sẵn ngay lúc một live region vừa được chèn vào.

Cách sửa: giữ một `<div role="status" aria-live="polite">` cố định ở gốc thẻ và chỉ đổi text bên trong. Phần hiển thị trực quan có thể để riêng với `aria-hidden`. Ghi chú thêm: khi chuyển sang `pending`, focus được đưa vào nút "Từ chối". Lúc nút bị `disabled` vì bận, focus rơi về `body`. Nên dùng `aria-disabled` thay cho `disabled` để focus không bị mất.

### M5. Mã quét từ điện thoại vẫn vào giỏ POS trong lúc thanh toán đang chạy hoặc đang mở dialog, và có thể bị mất khi `cart.reset()`

Bằng chứng: `pos-page.tsx:95` đăng ký `useScanTarget` vô điều kiện. `pos/containers/pos-cart-container.tsx:86` khai báo `useCheckout(() => cart.reset())`. `pay()` (dòng 129-161) chốt `lines` trong `draft`, và trong lúc request chạy hoặc dialog xác nhận ngoại lệ (`useConfirmException`) đang mở, `cart.add` từ điện thoại vẫn chạy. Khi thanh toán xong, `cart.reset()` xoá luôn dòng vừa thêm, trong khi điện thoại đã báo "đã thêm". `DeleteCartDialog` và overlay của `SalesOverlayHost` cũng không chặn mã từ điện thoại, trong khi bàn phím thì bị focus trap chặn.

Luồng bàn phím và bấm ô cũng có race này từ trước. Điểm mới là điện thoại là nguồn ở xa: người quét không nhìn màn hình POS và tin vào xác nhận "đã thêm".

Cách sửa: `useScanTarget(checkout.pending || modalOpen ? null : handler)`. Giá trị `null` đã có nghĩa "không nhận" và chặn target bên dưới, nên điện thoại sẽ nhận `not_accepting` hoặc lỗi rõ ràng thay vì một dòng bị mất. Muốn vậy cần đưa trạng thái `pending` của `PosCartContainer` lên trên, hoặc gọi hook bên trong container.

## Low

- **L1. Router `tally-web-scan` khớp cả `admin.${DOMAIN}`** (`infra/docker-compose.homelab.yml:88`). `HostRegexp(^[a-z0-9-]+[.]${DOMAIN}$)` bao cả admin, nên tài liệu `/scan` trên host admin nhận `camera=(self)` và `wasm-unsafe-eval`. Trang đó chỉ hiện 404 nên vô hại, nhưng chính sách bị nới không cần thiết. Thêm `&& !Host(`admin.${DOMAIN}`)`. `Path(/scan)` không khớp `/scan/`, trong khi `main.tsx` (`^/scan/?$`) và router lại chấp nhận `/scan/`, nên URL đó nhận `camera=()`. Trường hợp này fail closed nên an toàn, nhưng camera sẽ không chạy. Nên dùng `PathRegexp(^/scan/?$)` hoặc redirect `/scan/` về `/scan`.
- **L2. `tally-headers-scan` chép lại toàn bộ `tally-headers`** (dòng 79-87 so với 25-34), nên dễ lệch nhau khi sau này sửa CSP hoặc HSTS. Thêm ghi chú "giữ đồng bộ với x-tally-headers". Tốt hơn nữa là dùng chuỗi middleware `tally-headers` + một middleware chỉ ghi đè `Permissions-Policy`/`CSP`/`Referrer-Policy`. Traefik cho phép middleware sau ghi đè header của middleware trước, nhưng cần thử lại thực tế.
- **L3. Reconnect không có jitter** (`phone/use-phone-session.ts:23,155` và controller desktop dùng cùng dãy `[1000,2000,5000,10000,30000]`). Sau sự kiện `restarting` lúc deploy, mọi client kết nối lại cùng một nhịp. Quy mô hiện tại còn nhỏ, nhưng thêm ±20% ngẫu nhiên là đủ.
- **L4. Handler `revoked` không xoá `noticeTimer` đang chạy** (`scanner-controller.ts:395-404`). Hai lần `revoked` liên tiếp để lại timer cũ, gây `reconcile()` hai lần. Gọi `clearTimeout(noticeTimer)` trước khi đặt timer mới. View `pending` hiện mã xác minh rỗng khi thiếu `verify_code` (`verifyCode ?? ""`). Nên coi đó là lỗi giao thức và hiện thông báo thay vì ô trống.
- **L5. Thứ tự ưu tiên của `useScanTarget` theo thứ tự effect** (con chạy trước cha). Nếu cha và con cùng đăng ký thì cha thắng, ngược với mô tả "mount sau cùng thắng". Hiện chưa màn nào bị ảnh hưởng. Nên ghi vào doc của hook hoặc thêm tham số ưu tiên rõ ràng.
- **L6. Hàng đợi quét trên desktop chạy tuần tự.** Một handler treo (request không có timeout) chặn mọi mã sau. Các mã đó lần lượt quá hạn và bị bỏ qua, điện thoại báo hết giờ. Nên bọc handler trong timeout bằng thời gian còn lại của `ttl_ms`.
- **L7. nginx: `gzip_types` có `application/wasm`** (`nginx.conf:12`), nhưng phải dựa vào `mime.types` của image để trả đúng `Content-Type: application/wasm`. Nếu thiếu, `instantiateStreaming` thất bại và emscripten lùi về ArrayBuffer, chậm hơn nhưng vẫn chạy. Kiểm tra bằng `curl -I https://<slug>.${DOMAIN}/assets/zxing_reader-*.wasm` sau deploy.

## Đã kiểm chứng, không có lỗi

- Claim code: đọc từ `#c=` rồi xoá bằng `history.replaceState` trong `takeClaimCode` (`phone/use-phone-session.ts:384-392`), chỉ giữ trong bộ nhớ. `session-store.ts` chỉ lưu session id vào `sessionStorage`. Fragment không bao giờ rời trình duyệt. Edge gửi `Referrer-Policy: no-referrer` cho `/scan`.
- `/scan` nằm ngoài `RequireAuth` (`router.tsx`), không kích hoạt chuyển hướng đăng nhập.
- XSS: không có `dangerouslySetInnerHTML`. Mã quét, `device_label` và tên hàng đều render dưới dạng text React.
- WASM: `locateFile` trỏ asset tự host. zxing cache `[overrides, promise]`, nên các lần đọc sau dùng lại override và không gọi jsDelivr, dù URL mặc định vẫn còn trong bundle. Khi thất bại, lần thử lại tạo closure mới nên được chuẩn bị lại. Không có `eval`/`new Function` trong chunk reader hay scan-page.
- SW: denylist `/^\/scan(?:\/|$)/` (`sw.ts:24,30`). `vite.config.ts` có `globIgnores` cho `barcode-reader-*.js` và `manualChunks`. Đã xác nhận trên bản build.
- CSP của route scan chỉ thêm `'wasm-unsafe-eval'` vào `script-src`, và `Permissions-Policy` có `camera=(self)`. Mọi path khác giữ `camera=()`.
- Contract: tên event (`state`, `claim_requested`, `scan`, `approved`, `rejected`, `revoked`, `ended`, `replaced`, `restarting`) và payload (`scan_id`, `code`, `ttl_ms`, `device_label?`, `verify_code`) khớp `hub.go`/`service.go`. Mã lỗi khớp: `claim_invalid` 410, `scan_session_ended` 401, `already_claimed`/`not_pending`/`scan_pending_approval`/`idempotency_key_reused`/`scan_expired` 409, `stream_limit`/`server_restarting` 503.
- Điện thoại chỉ thử lại khi lỗi mạng hoặc 5xx và giữ nguyên `Idempotency-Key` (`phone/send-scan.ts:26-50`). Không thử lại khi `timeout` hoặc `desktop_unavailable`.
- Desktop chống trùng theo `scan_id` bằng map 200 mục. Mã bị bỏ qua vì quá ttl được cache `null`, nên lần thử lại không áp dụng lại.
- POS: luồng điện thoại không đọc `q`. `scan-match.ts` chỉ so token cho luồng bàn phím. Giỏ dùng `useReducer` dispatch nên quét liên tiếp không dính stale closure.
- Kiểm kê: chỉ nhận mã khi đang sửa (`useScanTarget(editing ? countScan : null)`). Dùng ref `latest` và hàng đợi lưu tuần tự (`inventory/api/mutations.ts:241-285`). Số lượng được cộng theo hệ số đơn vị.
- Camera: track bị dừng khi unmount, khi `visibilitychange` sang hidden, khi `pagehide` và khi `ended` (`phone/use-camera.ts:164-230`). Panel unmount khi không còn ghép.
- StrictMode: chuỗi start, stop, start an toàn cho cả controller lẫn phiên điện thoại. Handler của `useScanTarget` đi qua ref được cập nhật trong `useLayoutEffect`.
- 401 ở GET trên desktop làm mới `/me` qua `handleApiError`.

## Việc nên làm (theo thứ tự ưu tiên)

1. H1: đổi `.scan` của trang điện thoại thành `.scan-page`, rồi kiểm tra trực quan POS, kiểm kê và `/scan` ở viewport 375px.
2. H2: không tự tạo lại phiên khi `revoked{by:"system"}`. Backend ưu tiên thu hồi phiên chưa ghép.
3. M5: chặn nhận mã (`null` target) khi đang thanh toán hoặc đang mở modal trên POS.
4. M2: xoá ô quét lạc quan, chặn Enter trùng, chỉ lookup khi chuỗi trông giống mã.
5. M1: user chọn (a) hoặc (b) cho khoảng ttl 4 đến 10 giây.
6. M4: dùng một live region cố định cho thẻ ghép nối.
7. M3: tự gửi `SKIP_WAITING` trên `/scan` và sửa tài liệu hướng dẫn.
8. Các mục Low khi tiện.

## Số liệu

- Typecheck: không chạy riêng. `vite build` thành công (tsc nằm trong pipeline build của dự án; chưa xác nhận lại riêng).
- Test: 45 file, 337 test pass (scanner, pos, inventory, shared/lib).
- Lint: không chạy.
- Coverage: không đo.

## Câu hỏi còn mở

- M1 là thiết kế đã chốt. User có muốn thu hẹp hạn chót của desktop về `scanWait` không?
- H2: có triển khai thực tế nào dùng chung một tài khoản cho nhiều quầy không? Nếu có thì mức độ là High chắc chắn, nếu không thì có thể hạ xuống Medium.
- Khi dialog xác nhận ngoại lệ thanh toán đang mở, điện thoại nên nhận mã và giữ lại, hay báo `not_accepting`?

Status: DONE_WITH_CONCERNS
Summary: Không có lỗi Critical. Bảo mật của route /scan (fragment, CSP, Permissions-Policy, WASM tự host, SW denylist) đã kiểm chứng ổn. Có 2 lỗi High: class `.scan` trùng tên làm vỡ layout trang điện thoại cùng ô quét của POS và màn kiểm kê, và vòng thu hồi phiên vô hạn khi một user mở từ 4 thẻ trở lên. Ngoài ra có 5 lỗi Medium.
Concerns/Blockers: M1 cần user quyết định vì đụng thiết kế đã chốt. H2 cần sửa cả phía backend (ngoài phạm vi review web).
