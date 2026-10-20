---
phase: 4
title: "Trang quét trên điện thoại, CSP và Permissions-Policy"
status: pending
priority: P1
effort: 1.5d
dependencies: [2, 3]
---

# Phase 4: Trang quét trên điện thoại

## Mục tiêu và bối cảnh

Điện thoại mở `https://<slug>.<domain>/scan#c=<mã>` từ camera mặc định. Trang nhận mã ghép đôi, hiện mã xác minh và chờ
máy tính bấm Cho phép, rồi mở camera sau một lần chạm, đọc EAN-13, EAN-8, UPC-A, UPC-E, Code 128 và QR liên tục, gửi
từng mã lên API và báo kết quả bằng rung (Android) hoặc tiếng bíp kèm nháy màn hình (iOS), luôn kèm một dòng chữ. Trang
không cần đăng nhập và không hiện dữ liệu nào của cửa hàng ngoài tên hàng và đơn vị trong kết quả. Thiết kế lấy từ
[nghiên cứu quét mã](../reports/researcher-261020-1908-browser-barcode-scanning.md).

Bằng chứng:
- Trang được tải lười theo tên file `features/<f>/pages/<x>-page.tsx` (`apps/web/src/app/router.tsx:22-31`). Nhóm route
  công khai nằm ở `tenantRoutes()` (`:72-83`).
- `main.tsx` chạy `startStoreServices` trên mọi host tenant: heartbeat kết nối, xóa dữ liệu offline theo phiên và đăng
  ký service worker (`apps/web/src/main.tsx:79-86`). Token CSRF của host được đặt cho yêu cầu ẩn danh (`:105-106`).
- Service worker precache mọi file JS (`apps/web/vite.config.ts:41`), và trả `index.html` đã precache cho mọi điều
  hướng trừ `/api/` (`apps/web/src/sw.ts:21,27`). `registerType: "prompt"` (`vite.config.ts:22`): bản SW mới chỉ thay
  bản cũ sau khi người dùng bấm “Tải lại để cập nhật” (`main.tsx:39-47`). Một điện thoại đã cài PWA từ bản trước sẽ
  nhận vỏ cũ (chưa có route `/scan`, header cũ) cho tới khi SW cập nhật.
- Edge chặn camera: `permissionsPolicy: "camera=(), microphone=(), geolocation=()"` và CSP không có `script-src`
  (`infra/docker-compose.homelab.yml:32-33`), nên WASM bị chặn bởi `default-src 'self'`. Router `tally-web` có
  `priority: "2"`, `tally-api` có `"3"`, để router Host chính xác của stack khác luôn thắng (`:13-16,51-70`).
- nginx nén gzip nhưng chưa có `application/wasm` (`apps/web/nginx.conf:9-12`).
- iOS 26 và 27 không có `BarcodeDetector`. Chrome Android có nhưng phải kiểm tra `getSupportedFormats()`. Đường WASM trả
  UPC-A thành 13 chữ số. Điện thoại gửi mã thô; chỉ server so biến thể GTIN (phase 1), web không chuẩn hóa mã.
- CI kiểm tra compose bằng `docker compose -f infra/docker-compose.prod.yml -f infra/docker-compose.homelab.yml --env-file infra/.env.example config -q`
  (`.github/workflows/ci.yml:161-164`).

<!-- Updated: Red Team 2026-10-20 - denylist /scan trong SW, router edge riêng cho /scan, sự kiện restarting, câu timeout, lệnh compose của CI -->
<!-- Updated: Validation Session 1 - chờ Cho phép kèm mã xác minh; bíp kèm nháy màn hình trên iOS -->

## Files

- Tạo `apps/web/src/features/scanner/pages/scan-page.tsx`.
- Tạo `apps/web/src/features/scanner/phone/`:
  - `use-phone-session.ts`: nhận mã từ fragment, trạng thái phiên (`pending`, `claimed`, `ended`), luồng `/scan/events`, ngắt.
  - `use-camera.ts`: mở và dừng camera.
  - `barcode-reader.ts`: chọn `BarcodeDetector` gốc hoặc ponyfill WASM, nạp lười.
  - `scan-loop.ts`: vòng đọc khung hình và lọc trùng.
  - `send-scan.ts`: gửi mã với khóa idempotency và thử lại.
  - `feedback.ts`: rung, tiếng bíp bằng Web Audio, nháy màn hình.
  - `outcome-copy.ts`: câu hiển thị cho từng `outcome`.
  - Test: `scan-loop.test.ts`, `send-scan.test.ts`, `outcome-copy.test.ts`, `use-phone-session.test.ts`, và
    `scan-page.test.tsx` với camera và bộ đọc giả.
- Sửa `apps/web/src/app/router.tsx`: route `/scan` trong `tenantRoutes()`, ngoài `RequireAuth` và `AuthLayout`.
- Sửa `apps/web/src/main.tsx`: không chạy `startStoreServices` khi đường dẫn là `/scan`.
- Sửa `apps/web/src/sw.ts`: thêm `/^\/scan(?:\/|$)/` vào `denylist` của `NavigationRoute`.
- Sửa `apps/web/vite.config.ts`: gom `barcode-detector` và `zxing-wasm` vào chunk `barcode-reader`, thêm
  `globIgnores: ["**/barcode-reader-*.js"]`.
- Sửa `apps/web/package.json`, `pnpm-lock.yaml`: `pnpm add -E barcode-detector@3.2.3 zxing-wasm@3.1.6`.
- Sửa `infra/docker-compose.homelab.yml`: middleware `tally-headers-scan` và router `tally-web-scan`.
- Sửa `apps/web/nginx.conf`: thêm `application/wasm` vào `gzip_types`, và khai báo kiểu `application/wasm` cho `.wasm`
  nếu `mime.types` của image chưa có.

## Các bước

1. **Route và khởi động.** Thêm `{ path: "/scan", handle: { title: "Quét mã vạch" }, lazy: lazyPage("scanner", "scan") }`.
   Ở `main.tsx`, đường dẫn `/scan` vẫn tải host context và đặt token CSRF, nhưng bỏ qua `startStoreServices`, nên
   không đăng ký service worker, không gọi heartbeat `/me` và không đụng dữ liệu offline. Trang không có thanh điều
   hướng của ứng dụng.
2. **Service worker.** Thêm `/scan` vào `denylist` để điều hướng tới `/scan` luôn đi mạng, nhận đúng `index.html` mới
   và header của router `/scan`, kể cả trên điện thoại đã cài PWA. Với điện thoại đang giữ SW của bản trước khi tính
   năng ra mắt, SW cũ vẫn trả vỏ cũ cho lần mở đầu; vỏ cũ phát hiện SW mới và hiện nút “Tải lại để cập nhật”. Phase 7
   kiểm tra đường này trên máy thật, và hướng dẫn người dùng ghi cách xử lý: bấm “Tải lại để cập nhật”, hoặc đóng mọi
   tab của cửa hàng rồi quét lại QR.
3. **Nhận mã.** Đọc `c` từ `location.hash`, gọi ngay `history.replaceState(null, "", "/scan")` để mã không nằm lại trong
   lịch sử, rồi `POST /scan/claim`. Không có mã trong fragment thì `GET /scan/session`: còn phiên thì vào đúng trạng thái,
   không thì hiện “Quét mã QR trên màn hình máy tính để bắt đầu.” `claim_invalid` hiện “Mã QR đã hết hạn hoặc đã được
   dùng. Quét lại mã QR trên máy tính.”
4. **Chờ cho phép.** Trạng thái `pending` hiện mã xác minh lớn và “Chờ máy tính cho phép… Kiểm tra mã trên máy tính
   giống mã này.” Nút “Bật camera” chưa hiện. Sự kiện `approved` (hoặc `state` = `claimed` khi kết nối lại) chuyển sang
   màn quét. `rejected` hiện “Máy tính đã từ chối kết nối. Quét lại mã QR nếu đây là điện thoại của bạn.” và dừng.
   `ended` khi đang chờ (quá 2 phút) hiện câu phiên kết thúc ở bước 9.
5. **Camera.** Nút “Bật camera” (lần chạm này cũng mở khóa Web Audio trên iOS). `getUserMedia({ video: { facingMode: {
   ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 } }, audio: false })`. Thẻ `video` có
   `playsInline`, `muted`, `autoPlay`. Nếu `getCapabilities().focusMode` có `continuous` thì áp dụng. Lỗi quyền hiện
   hướng dẫn mở quyền camera cho trang trong cài đặt trình duyệt. Rời trang hoặc ẩn tab thì dừng mọi track.
6. **Bộ đọc.** Có `BarcodeDetector` và `getSupportedFormats()` chứa đủ sáu định dạng thì dùng bản gốc. Nếu không thì
   `import("barcode-detector/ponyfill")`, và trước lần dùng đầu gọi `prepareZXingModule` với `locateFile` trỏ tới URL
   tự host từ `import wasmUrl from "zxing-wasm/reader/zxing_reader.wasm?url"`. Không tải gì từ CDN.
7. **Vòng đọc.** `requestVideoFrameCallback` (thiếu thì `requestAnimationFrame`), giới hạn khoảng 8 lần đọc mỗi giây,
   có cờ bận để không chồng lần đọc. Lọc trùng theo cửa sổ trượt: một mã vẫn còn trong khung hình thì không gửi lại; mã
   đó phải vắng ít nhất 800 ms mới được gửi lần nữa. Nhiều mã trong một khung thì gửi lần lượt. Mã gửi đi đúng như bộ
   đọc trả về, chỉ cắt khoảng trắng.
8. **Gửi mã.** Mỗi lần phát hiện tạo một `crypto.randomUUID()` làm `Idempotency-Key`. Lỗi mạng thì thử lại cùng khóa sau
   0,5, 1 và 2 giây. Kết quả `timeout` không tự thử lại, vì máy tính có thể đã áp dụng mã; điện thoại báo để người dùng
   nhìn máy tính trước. 409 `scan_pending_approval` đưa trang về màn chờ cho phép.
9. **Phản hồi.** Theo `outcome`:
   - `added`: “{tên hàng} · {đơn vị}”, rung ngắn hoặc bíp cao kèm nháy xanh.
   - `not_found`: “Không có trong danh mục”, rung ba nhịp hoặc bíp thấp kèm nháy đỏ.
   - `not_accepting`: “Màn hình trên máy tính không nhận mã quét”.
   - `needs_desktop_edit`: “Dòng này nhập theo thùng, sửa số lượng trên máy tính”.
   - `new_product`: “Hàng mới, chưa có trong danh mục. Nhập thông tin trên máy tính.”, phản hồi kiểu thành công.
   - `not_in_order`: “Hàng này không có trong đơn nhập.”, phản hồi kiểu lỗi.
   - `desktop_unavailable`: “Máy tính chưa sẵn sàng. Kiểm tra màn hình trên máy tính.”
   - `timeout`: “Máy tính chưa phản hồi. Kiểm tra màn hình máy tính trước khi quét lại.”
   Android dùng `navigator.vibrate` (chỉ gọi khi tồn tại). iOS và máy không rung được dùng bíp Web Audio kèm nháy toàn
   màn hình xanh hoặc đỏ khoảng 150 ms. Dòng chữ luôn hiện. Danh sách năm kết quả gần nhất nằm dưới khung camera, có
   vùng `aria-live="polite"`. Nháy màn hình tôn trọng `prefers-reduced-motion` (thay bằng viền màu).
10. **Trạng thái và ngắt.** Luồng `GET /scan/events`:
    - `revoked` hoặc `ended`: dừng camera và hiện “Máy tính đã ngắt kết nối. Quét lại mã QR để tiếp tục.” 401
      `scan_session_ended` ở bất kỳ yêu cầu nào cũng vậy.
    - `restarting`, luồng `CLOSED`, 404 không phải JSON, 5xx và lỗi mạng là gián đoạn tạm thời: giữ camera, thanh trên
      cùng hiện “Đang kết nối lại…”, kết nối lại với backoff 1, 2, 5, 10 rồi tối đa 30 giây không giới hạn số lần, hỏi
      `GET /scan/session` trước mỗi lần mở.
    - `state` khi mở luồng là nguồn trạng thái duy nhất.
    Nút “Ngắt kết nối” gọi `DELETE /scan/session`. Thanh trên cùng luôn hiện “Đã kết nối với máy tính” hoặc trạng thái
    hiện tại.
11. **Bundle và service worker.** `manualChunks` gom `barcode-detector` và `zxing-wasm` vào `barcode-reader`. Sau
    `corepack pnpm build`, kiểm tra chunk chính không chứa `zxing`, file `.wasm` có trong `dist/assets`, và manifest
    precache của `sw.js` không có `barcode-reader` hay `.wasm`.
12. **Edge.** Trong `docker-compose.homelab.yml`, nhãn của `web`:
    - Middleware `tally-headers-scan` sao chép `tally-headers` nhưng `permissionsPolicy: "camera=(self), microphone=(), geolocation=()"`,
      CSP thêm `script-src 'self' 'wasm-unsafe-eval'` (không cần `worker-src`; `connect-src 'self'` đã cho phép
      `EventSource`), và `referrerPolicy: no-referrer`.
    - Router `tally-web-scan` có rule `HostRegexp(`^[a-z0-9-]+[.]${DOMAIN}$$`) && Path(`/scan`)`, `priority: "4"`, cùng
      service `tally-web` và middleware `tally-headers-scan`. Mọi đường dẫn khác giữ `camera=()` và CSP cũ. Ưu tiên 4 vẫn
      thấp hơn ưu tiên theo độ dài rule của router Host chính xác của stack khác.
    - Header chỉ áp dụng cho tài liệu `/scan` cấp cao nhất; đó là tài liệu duy nhất gọi camera và biên dịch WASM. Safari
      bỏ qua Permissions-Policy, nên trên iOS rào chắn là quyền camera của trình duyệt.
    - Ghi chú phía trên nhãn giải thích vì sao `/scan` có router riêng.
13. **nginx.** Thêm `application/wasm` vào `gzip_types` và bảo đảm `.wasm` trả `Content-Type: application/wasm`.

## Validation

- Vitest:
  - Lọc trùng theo cửa sổ trượt (mã giữ trong khung không gửi lại; vắng 800 ms thì gửi lại).
  - Thử lại cùng khóa khi lỗi mạng; `timeout` không tự thử lại và hiện đúng câu.
  - Câu hiển thị cho từng `outcome`.
  - Xóa fragment sau khi nhận mã; màn chờ hiện mã xác minh; `approved` mới hiện “Bật camera”; `rejected` dừng;
    409 `scan_pending_approval` quay về màn chờ.
  - 401 dừng camera; `restarting` và 503 giữ camera và kết nối lại.
  - Chọn bộ đọc gốc hoặc WASM theo `getSupportedFormats()`.
  - Phản hồi: có `vibrate` thì rung, không có thì bíp và nháy.
- `corepack pnpm lint|typecheck|test|build`, rồi `grep -l zxing dist/assets/index-*.js` không ra gì,
  `grep -c barcode-reader dist/sw.js` bằng 0, và `grep -c 'scan' dist/sw.js` cho thấy denylist có `/scan`.
- `docker compose -f infra/docker-compose.prod.yml -f infra/docker-compose.homelab.yml --env-file infra/.env.example config -q`
  chạy được, giống job `docker` của CI.
- Chạy tay trên điện thoại thật ở phase 7, gồm điện thoại có PWA của bản trước.

## Rủi ro

- `barcode-detector@3.2.3` hoặc `zxing-wasm@3.1.6` đọc kém trên iOS. Cặp dự phòng đã thử là `3.2.2` và `3.1.3`.
- `'wasm-unsafe-eval'` chỉ áp dụng cho tài liệu `/scan` và chỉ cho phép biên dịch WebAssembly, không cho `eval` JS.
- Lần mở đầu trên điện thoại giữ SW cũ phụ thuộc vào nút cập nhật của vỏ cũ. Phase 7 kiểm tra trên máy thật.
- Safari iOS hỏi quyền camera mỗi lần mở trang. Ghi vào hướng dẫn người dùng ở phase 7.

## Bảo mật

- Mã nhận bị xóa khỏi URL ngay khi đọc và không được lưu ở đâu trên điện thoại. Bí mật phiên nằm trong cookie HttpOnly.
  `Referrer-Policy: no-referrer` ở `/scan`.
- Camera chỉ mở sau khi máy tính cho phép.
- Trang chỉ hiện `product_name` và `unit_name` mà API trả về.
- Không tải script hay WASM từ bên thứ ba.

## Rollback

Hoàn tác route, trang, `sw.ts`, cấu hình Vite và gói. Gỡ router `tally-web-scan` và middleware `tally-headers-scan` bằng
một lần `make prod-up`; mọi đường dẫn khác chưa từng đổi header.

## Việc cần làm

- [x] Route `/scan`, bỏ `startStoreServices` ở `/scan`, denylist `/scan` trong `sw.ts`
- [x] `use-phone-session` (nhận mã, màn chờ cho phép, sự kiện, kết nối lại)
- [x] Camera, bộ đọc gốc hoặc WASM, vòng đọc và lọc trùng
- [x] Gửi mã, câu kết quả, rung hoặc bíp kèm nháy
- [x] Chunk `barcode-reader`, `globIgnores`, kiểm tra bundle và `sw.js`
- [x] Router edge `/scan`, nginx `application/wasm`
- [x] Vitest ở mục Validation và lệnh compose của CI
