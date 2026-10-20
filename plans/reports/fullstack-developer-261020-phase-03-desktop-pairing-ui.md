# Báo cáo triển khai: giao diện ghép đôi điện thoại trên máy tính

- Phase: `plans/261020-1227-phone-barcode-scanning/phase-03-desktop-pairing-ui.md`
- Trạng thái: hoàn thành. Lint, typecheck, test (164 file, 1193 test) và build đều sạch.
- Chưa chạy: bộ Playwright a11y/parity (lý do ở mục Kiểm thử).
- Chưa commit. Không sửa `apps/api`, file plan hay docs.

## File đã đổi

Tạo mới trong `apps/web/src/features/scanner/`:

| File | Vai trò |
|------|---------|
| `api/schemas.ts`, `api/queries.ts`, `api/mutations.ts` | Zod schema và lời gọi API của phiên quét (tạo, đọc, làm mới mã, cho phép, từ chối, ngắt, gửi kết quả) |
| `session-store.ts` | Lưu `{id}` của phiên vào `sessionStorage` (khóa `tally.scanner-session`), bọc try/catch, nếu bị từ chối thì chỉ giữ trong bộ nhớ |
| `event-stream.ts` | Lớp mỏng quanh `EventSource`. `defaultEventStream()` trả `null` khi trình duyệt không có `EventSource` |
| `scanner-controller.ts` | Bộ điều khiển TS thuần, không phụ thuộc React: vòng đời phiên, stream, backoff, làm mới mã, hàng đợi mã, chồng đích, các lệnh |
| `scanner-context.ts` | `ScannerContext` và `useScanner()`. Tách riêng để giữ quy tắc react-refresh |
| `scanner-provider.tsx` | `ScannerProvider({children, openStream?, store?})`, nối controller với `useApiClient` và `useConnectivity` |
| `use-scan-target.ts` | `useScanTarget(handler \| null)`: ref cập nhật mỗi lần render, đăng ký một lần khi mount |
| `pairing-url.ts` | `CLAIM_HASH_KEY = "c"`, `pairingUrl(code)` cho ra `${origin}/scan#c=<code>` |
| `components/pairing-qr.tsx` | QR SVG dùng `uqr` (`ecc: "M"`, viền 4), `role="img"` có nhãn |
| `components/phone-pairing-card.tsx` | Card "Quét bằng điện thoại", vùng `role="status" aria-live="polite"` |
| `index.ts` | Barrel: `PhonePairingCard`, `ScannerProvider`, `useScanTarget`, `type ScanHandler`, `pairingUrl`, `CLAIM_HASH_KEY` |
| `testing/fake-event-stream.ts`, `testing/scanner-harness.ts` | Stream giả và các helper `withScanner`, `memorySessionStore`, `pairedStream` |
| `scanner-provider.test.tsx`, `use-scan-target.test.tsx`, `components/phone-pairing-card.test.tsx` | Test |

Tạo mới ở chỗ khác:

- `apps/web/src/test/msw/scanner.ts`: `scannerServer(initial)` là MSW có trạng thái cho các route phía máy tính. Nó ghi log `calls` và `results`, có `onGet`, `onResult` và `onCreate` để giả lỗi.
- `apps/web/src/features/pos/pages/pos-page.test.tsx`: test POS khi có điện thoại đã ghép đôi.

Sửa:

- `app/layouts/app-shell.tsx`: bọc `ShellFrame` trong `ScannerProvider`, nằm bên trong `TenantProvider`.
- `features/pos/pages/pos-page.tsx`: gọi `useScanTarget((code) => scanCode(code, "phone"))` và đặt `<PhonePairingCard />` ngay dưới ô quét.
- `features/inventory/components/count-drawer.tsx`: gọi `useScanTarget(editing ? countScan : null)` và chỉ hiện card khi đang sửa.
- `features/inventory/components/count-drawer.test.tsx`: thêm 2 test.
- `features/pos/index.ts`: export thêm `useConnectivity`.
- `shared/styles/globals.css`: thêm các class `.pair*` vào khối bổ sung. QR 112px, màu lấy từ token `--surface-0` và `--navy-deep`.
- `package.json` và `pnpm-lock.yaml`: thêm `uqr` bản chính xác `0.1.3`.

## Quyết định

- **Controller nằm ngoài React.** Backoff, hẹn giờ làm mới mã và hàng đợi theo `scan_id` là logic có trạng thái và có hẹn giờ. Để trong một class TS thì test được bằng đồng hồ giả, và không bị StrictMode chạy effect hai lần. Provider chỉ nối controller với React.
- **Card lấy trạng thái chỉ từ sự kiện `state`.** Trước mỗi lần mở lại stream, controller gọi GET phiên. Phiên chỉ bị bỏ khi server xác nhận: sự kiện `ended`, hoặc GET trả 404 dạng JSON. 404 không phải JSON (trang lỗi của proxy), 503 và `restarting` đều giữ phiên và kết nối lại sau 1, 2, 5, 10, rồi 30 giây, không giới hạn số lần.
- **Làm mới mã** 5 giây trước khi hết hạn, có tôn trọng `Retry-After`. Nếu gặp 409 `already_claimed` thì GET lại trạng thái và không báo lỗi.
- **`replaced`:** controller quên phiên, hiện "Phiên quét bằng điện thoại đang mở ở tab khác." kèm nút "Ghép đôi ở tab này", và không tự tạo phiên mới.
- **Offline:** chỉ trạng thái kết nối `"offline"` mới tính là mất mạng. `degraded` vẫn coi là có mạng. Khi offline, controller đóng stream và không tạo phiên. Khi có mạng lại, nó nối lại đúng phiên cũ.
- **Không có `EventSource`** thì card hiện "Trình duyệt này không hỗ trợ quét bằng điện thoại." và không gọi API nào. Nhờ vậy các test jsdom có render AppShell không phát sinh lời gọi scanner.
- **Chồng đích:** đích mount sau cùng thắng. Đích truyền `null` vẫn chiếm chỗ trên cùng và chặn đích bên dưới, nên Kiểm kê không sửa sẽ trả `not_accepting` thay vì để POS bên dưới nhận mã.
- **Hàng đợi:** controller xử lý từng mã một theo thứ tự đến. Nó nhớ 200 `scan_id` gần nhất cùng promise của chúng. Khi một `scan_id` đến lần hai, mã chỉ được xử lý một lần và cả hai lần nhận cùng kết quả. Mã đã quá `ttl_ms` khi tới lượt thì bị bỏ qua, không gọi handler và không gửi kết quả. Việc gửi kết quả chạy song song và không chặn mã kế tiếp.
- **Toast:**
  - Khi handler ném lỗi: toast "Không xử lý được mã {code} từ điện thoại: …" và gửi `not_found`.
  - Khi gửi kết quả thất bại: chỉ toast nếu handler đã chạy và kết quả là `added`, vì dòng đã vào giỏ nhưng điện thoại không biết. Nếu lỗi là 409 `scan_expired` thì báo "Mã {code} từ điện thoại đến muộn. Kiểm tra lại dòng vừa thêm.". Các lỗi khác báo "Không báo được kết quả mã {code} về điện thoại: …".
- **Cho phép / Từ chối:** khi bước này hiện, tiêu điểm vào nút Từ chối. Sau khi chọn xong, tiêu điểm được trả về. Nếu lệnh gặp 409, controller GET lại trạng thái. Nếu gặp 404 JSON, controller quên phiên.

## Khác với danh sách file trong plan

- Thêm 3 file ngoài danh sách là `scanner-controller.ts`, `scanner-context.ts` và `pairing-url.ts`. Controller tách khỏi provider vì lý do ở trên. Context tách riêng vì quy tắc react-refresh (file component chỉ export component). `pairing-url.ts` để phase 4 dùng chung định dạng QR.
- Thêm `features/pos/pages/pos-page.test.tsx`, vì POS chưa có file test trang mà Validation lại yêu cầu một test POS.
- Thêm helper test trong `features/scanner/testing/`.
- Không có thay đổi nào ngoài phạm vi phase.

## Kiểm thử

Mọi mục trong Validation đều có test:

- **Provider (18 test):**
  - khởi động với phiên đã lưu còn sống, và với phiên đã kết thúc;
  - tạo phiên khi card hiện;
  - làm mới mã bằng đồng hồ giả;
  - 409 `already_claimed`;
  - `state` khi tự kết nối lại;
  - `restarting`, 404 không phải JSON và 503 thì kết nối lại với backoff;
  - `replaced` không tự tạo phiên;
  - offline, `sessionStorage` từ chối, `revoked`.
- **Card (10 test):** QR khi chờ, mã xác minh với tiêu điểm ở Từ chối, Cho phép gọi `:approve`, Từ chối gọi `:reject` và hiện QR mới, Ngắt gọi DELETE, câu offline, Thử lại, trình duyệt không hỗ trợ.
- **Đích (9 test):** đích trên cùng thắng và trả lại khi gỡ, `not_accepting`, handler mới nhất, `scan_id` trùng, thứ tự, mã quá hạn, handler lỗi, `scan_expired`.
- **POS:** mã từ điện thoại thêm dòng Mứt gừng, gửi `added` với `sku-mut`/`unit-hop`, và chữ "kẹo" đang gõ vẫn còn.
- **Kiểm kê:** ba mã liền nhau cộng đủ 3. Khi phiếu ở `pending_post` thì trả `not_accepting` và ẩn card.

Lệnh đã chạy trong `apps/web`: `corepack pnpm lint`, `typecheck`, `test` (1193 test qua) và `build` đều sạch.

Chưa chạy Playwright a11y/parity (`e2e/a11y/*`). Bộ này cần API thật qua `make dev` và phiên owner đã seed. Bắt đầu nó sẽ tạo tiến trình chạy dài và migrate DB dev, trong khi backend đang được review và sửa song song. Plan cũng đã xếp parity và a11y vào phase 7. Card dùng `.btn-sm` (đã cao 44px trên màn hẹp hoặc màn cảm ứng), nút có nhãn chữ, QR có `aria-label` và màu chữ lấy từ token có sẵn, nhưng độ tương phản và parity bố cục POS vẫn cần phase 7 đo lại.

## Phase 4 cần biết

- **Định dạng QR** là `${origin}/scan#c=<claim_code>`, đã URL-encode. Đọc mã bằng `CLAIM_HASH_KEY` export từ `features/scanner`. Nên dùng `URLSearchParams(location.hash.slice(1)).get(CLAIM_HASH_KEY)`.
- Có thể dùng lại `apps/web/src/test/msw/scanner.ts` cho các route phía máy tính. Route phía điện thoại chưa có trong helper này.
- `event-stream.ts` là chỗ để thay stream trong test. `testing/fake-event-stream.ts` có `emit` và `fail` đã bọc trong `act`.
- Test có mount provider phải gọi `cleanup()` trước khi reset connectivity trong `afterEach`. Nếu không, provider còn mount ở test trước sẽ thấy có mạng lại và tạo phiên lẫn sang test sau.

## Quan sát về backend (không sửa, theo ràng buộc)

1. **Giới hạn tần suất dùng chung:** 6 lần/phút mỗi IP, chung cho tạo phiên, làm mới mã, cho phép và từ chối. Cửa hàng có nhiều quầy sau cùng một NAT có thể chạm giới hạn này.
2. **Mô tả OpenAPI lệch với code:** mô tả ghi QR là `/scan#<claim_code>`, còn frontend dùng `/scan#c=<claim_code>` theo plan. Nên sửa mô tả cho khớp.
3. **Stream đã kết thúc trả 204:** qua `EventSource`, phản hồi này không phân biệt được với các mã khác 200. Frontend đã xử lý bằng cách GET phiên trước mỗi lần mở lại stream.

## Điểm nhỏ còn lại (chấp nhận được)

- Sau khi bấm Ngắt, stream có thể gửi `revoked` trước, nên card hiện "Điện thoại đã ngắt kết nối" khoảng 4 giây rồi mới hiện QR mới.
- Tải lại trang khi đang chờ thì mã được làm mới ngay, vì mã ghép đôi không được lưu lại (đúng thiết kế: chỉ lưu `id`).

## Câu hỏi chưa giải quyết

Không có.
