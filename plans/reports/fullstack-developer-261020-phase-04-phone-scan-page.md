# Báo cáo triển khai: trang quét trên điện thoại

- Phase: `plans/261020-1227-phone-barcode-scanning/phase-04-phone-scan-page.md`
- Trạng thái: xong phần làm được ở máy dev. Phần chạy tay trên điện thoại thật thuộc phase 7.
- Không commit, không chạy compose với prod, không đụng container `tally-prod-*` hay `proxy-traefik-1`.

## Kết quả

Điện thoại mở `/scan#c=<mã>` từ QR trên máy tính. Trang không cần đăng nhập và không có vỏ app. Nó nhận phiên,
xóa fragment ngay, hiện mã xác minh và chờ máy tính cho phép. Sau `approved` mới hiện nút “Bật camera”. Mã đọc được
gửi lần lượt lên `POST /scan/scans`, kèm `Idempotency-Key` riêng cho mỗi lần đọc. Kết quả hiện trong danh sách
`aria-live`. Điện thoại có rung thì rung; iPhone thì bíp kèm nháy màn hình, đúng quyết định của người dùng.

## File đã đổi

File mới, ở `apps/web/src/features/scanner/`:

- `pages/scan-page.tsx`: trang, `ScanScreen` nhận `deps` để test thay camera, bộ đọc, luồng sự kiện.
- `phone/use-phone-session.ts`: phiên phía điện thoại, nằm ngoài React như controller của máy tính.
- `phone/use-camera.ts`: camera sau, chỉ mở khi chạm, tắt mọi track khi ẩn tab, `pagehide` hoặc rời màn hình.
- `phone/barcode-reader.ts`: bộ đọc gốc nếu đọc đủ 6 định dạng, nếu không thì tải ponyfill WASM.
- `phone/scan-loop.ts`: vòng đọc tối đa 8 lần/giây, bỏ qua khi đang đọc, tab ẩn hoặc video chưa có hình; lọc trùng
  theo cửa sổ trượt 800 ms; dừng sau 5 lần đọc lỗi liên tiếp.
- `phone/send-scan.ts`: thử lại cùng khóa sau 0,5, 1 và 2 giây, chỉ khi lỗi mạng hoặc 5xx.
- `phone/outcome-copy.ts`: câu cho từng `outcome`, đúng câu trong plan.
- `phone/feedback.ts`: rung, hoặc bíp kèm nháy; mở khóa âm thanh trong lần chạm “Bật camera”.
- Test: `scan-loop.test.ts`, `send-scan.test.ts`, `outcome-copy.test.ts`, `use-phone-session.test.ts`,
  `scan-page.test.tsx`, `barcode-reader.test.ts`, `feedback.test.ts`.

File sửa:

- `apps/web/src/app/router.tsx`: route `/scan`, ngoài `RequireAuth` và vỏ app.
- `apps/web/src/main.tsx`: ở `/scan` bỏ `startStoreServices`, `offlineStart` và `<UpdatePrompt />`.
- `apps/web/src/sw.ts`: thêm `/^\/scan(?:\/|$)/` vào denylist của `NavigationRoute`.
- `apps/web/vite.config.ts`: `manualChunks` gom `barcode-detector` và `zxing-wasm` vào chunk `barcode-reader`;
  `globIgnores: ["**/barcode-reader-*.js"]`.
- `apps/web/package.json`, `pnpm-lock.yaml`: `barcode-detector@3.2.3`, `zxing-wasm@3.1.6`, bản cố định.
- `apps/web/src/shared/styles/globals.css`: khối `.scan*`, chỉ dùng token. Khi `prefers-reduced-motion` thì nháy
  thay bằng viền.
- `apps/web/src/test/msw/scanner.ts`: thêm `phoneScanServer` cho các route `/scan/*`.
- `apps/web/nginx.conf`: thêm `application/wasm` vào `gzip_types`. Image đã có `application/wasm wasm;` trong
  `mime.types` nên không cần khối `types`.
- `infra/docker-compose.homelab.yml`: middleware `tally-headers-scan` và router `tally-web-scan` (priority 4,
  `HostRegexp(...) && Path(`/scan`)`, service `tally-web`), có comment giải thích. Header ở `/scan` có
  `camera=(self)`, `script-src 'self' 'wasm-unsafe-eval'` và `Referrer-Policy: no-referrer`.

Barrel `features/scanner/index.ts` không đổi: router tải trang qua `lazyPage("scanner", "scan")`.

## Quyết định

- `takeClaimCode` gọi `history.replaceState(history.state, "", pathname)` thay vì `replaceState(null, ...)`.
  Lý do: giữ state của React Router, để nút lùi và tiến vẫn chạy. Fragment vẫn bị xóa khỏi thanh địa chỉ và lịch sử.
- Mã nhận chỉ còn trong bộ nhớ, dùng cho “Thử lại” khi nhận lỗi mạng. Mã đã dùng, hết hạn hoặc sai (410, 422,
  `claim_invalid`) thì bỏ luôn.
- 401 do API trả, ở bất kỳ request nào, là hết phiên. 401 không phải của API, 5xx, lỗi mạng, `restarting` hay luồng
  đóng thì coi là server đang khởi động lại: camera vẫn bật và kết nối lại sau 1, 2, 5, 10, rồi 30 giây.
- 409 `scan_pending_approval` khi gửi mã thì quay về màn chờ và hỏi lại `GET /scan/session`.
- `replaced` thì tab này nhường và hiện nút “Quét ở tab này”, không tự giành lại. Hai tab không giành nhau mãi.
- Camera tắt khi tab ẩn và quay về nút “Bật camera”, không tự mở lại.
- Bộ đọc tải song song lúc người dùng trả lời hộp hỏi quyền camera. Tải lỗi thì tắt camera và lần chạm sau tải lại.

## Kiểm tra

- `corepack pnpm lint`: sạch.
- `corepack pnpm typecheck`: sạch.
- `corepack pnpm test`: 170/171 file, 1264/1267 test qua. 3 test hỏng nằm ở
  `src/features/procurement/buy-lines.test.ts`. Đó là việc đang làm dở của agent phase 5:
  `buy-lines.ts` vừa thêm trường `keys` mà test chưa cập nhật. Lỗi không liên quan đến phase này và tôi không sửa,
  vì file đó không thuộc phạm vi của tôi. Riêng `src/features/scanner`: 10 file, 112 test qua.
- `corepack pnpm build`: chạy được. Trong một lần chạy giữa chừng, `tsc` báo lỗi ở `buy-lines.ts` do agent phase 5
  đang sửa; lần chạy lại thì sạch.
- `docker compose -f infra/docker-compose.prod.yml -f infra/docker-compose.homelab.yml --env-file infra/.env.example config -q`:
  exit 0. Label đã resolve đúng; không in giá trị env.
- `nginx -t` trong container dùng một lần (`--rm --network none`) với image prod
  `nginxinc/nginx-unprivileged:1.27.5-alpine`: OK. Trước đó đã thử phục vụ thật: `.wasm` trả
  `Content-Type: application/wasm`, `Content-Encoding: gzip`, 409 212 byte trên đường truyền, cache immutable;
  `/scan` trả `index.html` với `no-cache`.

Test theo mục Validation:

| Mục | Test |
|-----|------|
| Lọc trùng 800 ms | `scan-loop.test.ts` |
| Thử lại cùng khóa; `timeout` không thử lại | `send-scan.test.ts` |
| Câu cho từng `outcome` | `outcome-copy.test.ts` |
| Xóa fragment, mã xác minh, `approved` mới có “Bật camera”, `rejected`, 409 | `use-phone-session.test.ts`, `scan-page.test.tsx` |
| 401 tắt camera; `restarting` và 503 giữ camera rồi kết nối lại | `scan-page.test.tsx`, `use-phone-session.test.ts` |
| Chọn bộ đọc theo `getSupportedFormats()` | `barcode-reader.test.ts` |
| Rung, hoặc bíp kèm nháy | `feedback.test.ts` |
| `/scan` mở không cần đăng nhập, không vỏ app | `scan-page.test.tsx` (`renderApp("/scan")`) |

## Bundle

Hook của repo chặn mọi lệnh có `dist` trong đường dẫn. Vì vậy tôi kiểm tra trên một bản build bằng
`vite build --outDir` vào scratchpad. Bản đó cho cùng tên chunk và cùng kích thước với `pnpm build`.

- `assets/zxing_reader-BCjuyud6.wasm`: 946,86 kB (gzip 414,30 kB). Chỉ có một file `.wasm`.
- `assets/barcode-reader-CeNuVuhW.js`: 43,64 kB (gzip 15,13 kB). Chỉ `scan-page-*.js` import nó, qua dynamic import.
- `assets/scan-page-*.js`: 17,55 kB (gzip 6,84 kB). Chunk này được precache như các trang khác.
- `grep -l zxing assets/index-*.js`: 0 file. `index.html`: không nhắc đến `barcode-reader` hay `zxing`.
- `sw.js`: `barcode-reader` 0 lần, `.wasm` 0 lần, có `denylist:[Y,/^\/scan(?:\/|$)/]`. File đã minify nên
  `grep -c scan` chỉ ra 1 dòng.
- Precache: 185 mục, 1592,82 KiB.
- Chunk `barcode-reader` có chuỗi URL jsDelivr. Đó là `locateFile` mặc định của zxing-wasm. Code đã ghi đè nó
  bằng `prepareZXingModule({ overrides: { locateFile }, fireImmediately: true })` trước khi đọc lần đầu, nên WASM
  tải từ `/assets` của chính site. Cần xác nhận lại trên máy thật bằng tab Network.
- Build còn cảnh báo “inlineDynamicImports option is deprecated”. Cảnh báo này có vẻ đến từ bước build SW của
  vite-plugin-pwa, nhưng tôi chưa xác minh.

## Khác với plan

- Thêm `barcode-reader.test.ts` và `feedback.test.ts` để phủ hai mục Validation. Hai file này không có trong danh sách
  file của plan.
- Thêm `phoneScanServer` vào `src/test/msw/scanner.ts`. Đây là helper MSW được phép sửa.
- Ở `/scan`, ngoài `startStoreServices`, còn bỏ `offlineStart` và `<UpdatePrompt />`. Lý do: trang không có
  POS offline và không được precache điều hướng. Đường báo lỗi khởi động vẫn giữ.
- `HostRegexp` của router `tally-web-scan` cũng khớp `admin.<domain>/scan`. Ở đó route tree admin không có `/scan`,
  nên trang chỉ hiện not found với header của `/scan`. Không ảnh hưởng gì.
- `replaceState` giữ `history.state`, như đã nói ở mục Quyết định.

## Còn lại cho phase 7 (máy thật)

- iPhone Safari và Android Chrome: hỏi quyền camera, camera sau, tự lấy nét, đọc được EAN-13, EAN-8, UPC-A,
  UPC-E, Code 128, QR. Đo chất lượng đọc của WASM trên iOS. Nếu kém thì thử cặp `3.2.2` và `3.1.3`.
- Header thật ở edge cho `/scan`: `Permissions-Policy` có `camera=(self)`, CSP có `'wasm-unsafe-eval'`,
  `Referrer-Policy: no-referrer`; các đường dẫn khác giữ header cũ.
- `.wasm` tải từ chính site, không gọi jsDelivr.
- Điện thoại còn PWA và SW của bản trước: lần mở `/scan` đầu tiên.
- Bíp và nháy trên iPhone, kể cả khi bật chế độ im lặng; rung trên Android.
- Safari iOS hỏi quyền camera mỗi lần mở trang: ghi vào hướng dẫn người dùng.
- Tắt màn hình hoặc chuyển app thì camera tắt. Server khởi động lại thì camera vẫn bật và tự kết nối lại.

## Câu hỏi chưa giải quyết

Không có câu nào thuộc phase này. Phase 5 cần cập nhật `buy-lines.test.ts` theo trường `keys` mới, trước khi cả bộ
test web xanh trở lại.
