---
phase: 3
title: "Web máy tính: khung QR, cho phép, phiên theo tab, đích nhận mã"
status: pending
priority: P1
effort: 1d
dependencies: [1, 2]
---

# Phase 3: Web máy tính

## Mục tiêu và bối cảnh

Mỗi tab trên máy tính giữ tối đa một phiên ghép đôi, dùng chung cho POS, Kiểm kê và Nhập hàng. Điện thoại quét QR thì
card hỏi “Cho phép” kèm mã xác minh. Màn đang mở đăng ký một hàm nhận mã. Mã từ điện thoại gọi đúng hàm xử lý Enter
của màn đó (`scanCode(code, "phone")` ở POS, `countScan` ở Kiểm kê từ phase 1, `scanCode` của `BuyEditor` ở phase 5),
rồi máy tính báo kết quả về API.

Bằng chứng:
- `AppShell` bọc `ShellFrame`, và `ShellFrame` đã có mẫu đăng ký dùng chung cho cả vỏ ứng dụng (`useProcurementDocOpeners`,
  `useInventoryDocOpeners`) ở `apps/web/src/app/layouts/app-shell.tsx:28-40`.
- Ô quét POS ở `apps/web/src/features/pos/pages/pos-page.tsx:131`. Ngăn Kiểm kê chỉ hiện ô quét khi đang sửa
  (`apps/web/src/features/inventory/components/count-drawer.tsx:196`).
- Kết nối: `useConnectivity` với ba trạng thái online, degraded, offline (`apps/web/src/features/pos/offline/connectivity.ts`).
  ESLint không chặn import giữa các feature (`apps/web/eslint.config.js`), nhưng cấm màu thô trong `src/features/**`.
- Client HTTP gửi CSRF cho yêu cầu không phải GET và hỗ trợ `Idempotency-Key` (`apps/web/src/shared/api/http.ts`).
  Web chưa dùng `EventSource` ở đâu.
- jsdom không có `EventSource`, nên luồng sự kiện phải đi qua một hàm mở luồng thay được trong test.
- Khi `api` khởi động lại, Traefik trả 404 không phải JSON trong lúc container vắng mặt
  (`plans/reports/researcher-261020-1908-sse-pairing-design.md:14`), và healthcheck của `api` có `interval: 10s`
  (`infra/docker-compose.prod.yml:77-82`).

<!-- Updated: Red Team 2026-10-20 - handler qua ref, hàng đợi giữ promise theo scan_id, hạn của mã, đồng bộ trạng thái khi mở luồng, kết nối lại không giới hạn -->
<!-- Updated: Validation Session 1 - card hỏi Cho phép kèm mã xác minh -->

## Files

- Tạo `apps/web/src/features/scanner/`:
  - `api/schemas.ts`, `api/queries.ts`, `api/mutations.ts` theo mẫu `features/pos/api/`.
  - `scanner-provider.tsx`: context, vòng đời phiên, luồng SSE, hàng đợi mã.
  - `event-stream.ts`: `openEventStream(url, handlers)` bọc `EventSource`, thay được trong test.
  - `use-scan-target.ts`: `useScanTarget(handler | null)`.
  - `session-store.ts`: đọc và ghi mã phiên trong `sessionStorage`, bọc try/catch.
  - `components/phone-pairing-card.tsx` và `components/pairing-qr.tsx` (vẽ QR bằng `uqr`).
  - Test: `scanner-provider.test.tsx`, `phone-pairing-card.test.tsx`, `use-scan-target.test.tsx`.
- Sửa `apps/web/src/app/layouts/app-shell.tsx`: bọc `ShellFrame` trong `ScannerProvider`.
- Sửa `apps/web/src/features/pos/pages/pos-page.tsx`: đặt card cạnh ô quét, đăng ký `scanCode` với nguồn điện thoại.
- Sửa `apps/web/src/features/inventory/components/count-drawer.tsx`: đặt card trên ô quét khi đang sửa, đăng ký `countScan`.
- Sửa `apps/web/src/shared/styles/globals.css` (khối additions) cho card và QR, nếu class có sẵn không đủ.
- Sửa `apps/web/package.json` và `pnpm-lock.yaml`: `pnpm add -E uqr@0.1.3`.
- Sửa `apps/web/src/test/msw/` để giả các route `scanner-sessions`.

## Các bước

1. **Lưu phiên theo tab.** `sessionStorage["tally.scanner-session"] = {id}`. Mất `sessionStorage` (chế độ riêng tư, bị
   chặn) thì phiên chỉ sống trong bộ nhớ của trang.
2. **`ScannerProvider`.** Chỉ hoạt động khi có ít nhất một card đang hiện hoặc có phiên đã lưu:
   - Khởi động: có mã phiên đã lưu thì `GET /scanner-sessions/{id}`. `waiting`, `pending` hoặc `claimed` thì mở lại
     luồng. `ended`, hoặc 404 có thân JSON với `error.code` của API (phiên của người dùng khác sau khi đổi tài khoản),
     thì xóa mã đã lưu.
   - Một card hiện lên mà không có phiên sống thì `POST /scanner-sessions`. Mã nhận chỉ giữ trong bộ nhớ.
   - Mã nhận còn 2 phút. Khi hết hạn mà phiên vẫn `waiting` thì `:refresh-code`. QR đổi theo mã mới. 409
     `already_claimed` nghĩa là đã lỡ một sự kiện: lấy lại trạng thái bằng `GET`, không báo lỗi.
   - Offline (theo `useConnectivity`) thì đóng luồng, không tạo phiên, và card báo “Quét bằng điện thoại cần có mạng”.
     Online lại thì làm lại bước khởi động.
3. **Kết nối lại.** Sự kiện `state` mà server gửi mỗi lần luồng mở (kể cả lần `EventSource` tự kết nối lại) là nguồn
   trạng thái duy nhất của card. Khi luồng ở trạng thái `CLOSED` hoặc nhận `restarting`: mở lại với backoff 1, 2, 5, 10
   rồi tối đa 30 giây, không giới hạn số lần, và trước mỗi lần mở hỏi `GET /scanner-sessions/{id}`. Chỉ xóa phiên khi
   API trả `ended`, 204 cho luồng, hoặc 404 có thân JSON của API. 404 không phải JSON, 5xx và lỗi mạng đều là tạm thời.
   Card hiện “Đang kết nối lại…” trong lúc chờ.
4. **Sự kiện.**
   - `state`: đặt card theo `waiting` / `pending` / `claimed`.
   - `claim_requested{device_label, verify_code}`: card hiện “Điện thoại {thiết bị} muốn kết nối. Mã xác minh
     {verify_code}. Chỉ cho phép nếu mã trên điện thoại giống mã này.” với hai nút **Cho phép** (`:approve`) và
     **Từ chối** (`:reject`, QR mới hiện ngay). Tiêu điểm đặt vào nút Từ chối để Enter vô tình không cho phép.
   - Sau khi cho phép: card thu lại thành “Điện thoại đã kết nối · {thiết bị} · Ngắt”.
   - `revoked`: xóa phiên. Card hiện “Điện thoại đã ngắt kết nối” trong vài giây rồi tạo QR mới nếu vẫn đang hiện.
   - `replaced`: tab khác (ví dụ tab nhân bản) đã mở cùng phiên. Tab này xóa mã đã lưu, đóng luồng và card hiện nút
     “Ghép đôi ở tab này”, không tự tạo phiên mới để hai tab không giành nhau.
5. **Đích nhận mã.** `useScanTarget(handler)` giữ handler trong một ref và cập nhật ref ở mỗi lần render, nên provider
   luôn gọi bản handler mới nhất (thấy `tiles`, `lines` và trạng thái kết nối hiện tại). Ngăn xếp trong provider chứa
   các ref: màn mount thì đẩy vào, unmount thì gỡ ra. Đích trên cùng thắng, nên một hộp thoại Nhập hàng mở trên trang
   Đơn nhập nhận mã, đóng hộp thoại thì đích trước đó (nếu có) nhận lại. Truyền `null` nghĩa là màn không nhận mã lúc
   này, ví dụ Kiểm kê khi chưa bấm sửa.
6. **Xử lý một mã.** Mỗi sự kiện `scan{scan_id, code, ttl_ms}` được ghi nhận ngay khi đến:
   - Bảng `scan_id → promise kết quả` (giữ 200 mục gần nhất). `scan_id` đã có thì chờ đúng promise đó rồi gửi lại kết
     quả, không gọi handler lần nữa, kể cả khi lần đầu còn đang chạy.
   - Mã mới vào hàng đợi, xử lý lần lượt. Hạn của mã là thời điểm nhận cộng `ttl_ms` theo đồng hồ của trình duyệt. Lấy
     ra khỏi hàng đợi mà đã quá hạn thì bỏ qua, không gọi handler và không gửi kết quả.
   - Không có đích thì kết quả là `not_accepting`.
   - Có đích thì `await handler(code)` và gửi kết quả bằng `POST …/scans/{scanId}/result`. Handler ném lỗi thì báo lỗi
     bằng toast và gửi `not_found`, để điện thoại không chờ hết 4 giây. 409 `scan_expired` khi gửi kết quả thì toast
     “Mã {code} từ điện thoại đến muộn. Kiểm tra lại dòng vừa thêm.”, vì handler đã chạy.
   - Handler gọi đúng logic của Enter nhưng **không** đụng tới chữ đang gõ trong ô quét, để mã từ điện thoại không gửi
     kèm hay xóa mã gõ dở.
7. **`PhonePairingCard`.** Một thành phần dùng chung, có các trạng thái: đang tạo phiên, QR “Quét bằng điện thoại” kèm
   dòng hướng dẫn ngắn, chờ cho phép (bước 4), đã kết nối (nhãn và nút Ngắt), đang kết nối lại, offline, lỗi tạo phiên
   (nút “Thử lại”). Nút Ngắt gọi `DELETE /scanner-sessions/{id}`. QR chứa `window.location.origin + "/scan#c=" + mã
   nhận`. Màu lấy từ token, nền QR luôn sáng và có viền trống bốn ô để camera đọc được cả ở giao diện tối. QR có nhãn
   truy cập “Mã QR ghép đôi điện thoại”. Vùng trạng thái của card có `aria-live="polite"`.
8. **Đặt card.** POS: cạnh ô quét, gọn để không đẩy lưới ô xuống quá nhiều ở 1440 và 390. Kiểm kê: trên ô quét, chỉ khi
   đang sửa. Nhập hàng: phase 5 đặt trong `BuyEditor`.
9. **POS và Kiểm kê đăng ký đích.** POS đăng ký `(code) => scanCode(code, "phone")` khi trang mở. Kiểm kê đăng ký
   `countScan` khi đang sửa và `null` khi không sửa. Kết quả `added` gửi kèm `sku_id` và `unit_id` để API dựng tên cho
   điện thoại.

## Validation

- Vitest:
  - Provider: khởi động với phiên đã lưu còn sống và đã kết thúc; tạo phiên khi card hiện; làm mới mã khi hết hạn (đồng
    hồ giả); 409 `already_claimed` lấy lại trạng thái; `state` khi tự kết nối lại cập nhật card; `restarting`, 404 không
    phải JSON và 503 giữ phiên và kết nối lại với backoff; `replaced` không tự tạo phiên.
  - Cho phép: `claim_requested` hiện mã xác minh và hai nút, tiêu điểm ở Từ chối; Cho phép gọi `:approve`; Từ chối gọi
    `:reject` và hiện QR mới.
  - Đích: đích trên cùng thắng và đích trước nhận lại khi gỡ; không có đích thì gửi `not_accepting`; handler đổi sau
    khi mount thì bản mới được gọi; cùng `scan_id` đến lần hai khi lần đầu chưa xong thì handler chỉ chạy một lần và hai
    lần nhận cùng kết quả; mã quá hạn không gọi handler; hai mã đến liền nhau được xử lý theo thứ tự.
  - Card: QR hiện khi chờ, nhãn “Điện thoại đã kết nối” sau khi cho phép, nút Ngắt gọi DELETE, offline hiện đúng câu.
  - POS: mã từ điện thoại thêm đúng dòng và không xóa chữ đang gõ. Kiểm kê: ba mã cùng hàng đến liền nhau cộng đủ 3;
    khi không sửa thì trả `not_accepting`.
- `corepack pnpm lint`, `typecheck`, `test`, `build` sạch. Parity và a11y của POS và Kiểm kê chạy lại ở phase 7.

## Rủi ro

- Card làm thay đổi bố cục POS và có thể làm lệch parity với prototype ở 1440. Ghi tỉ lệ parity trước và sau, và chấp
  nhận thay đổi chỉ ở vùng card.
- Hàng đợi tuần tự làm mã thứ hai chờ mã thứ nhất (ví dụ một lần `/sku-lookup`). Mỗi mã vẫn cần xong trong khoảng một
  giây, nên đo thời gian ở phase 7. Mã quá hạn bị bỏ thay vì áp dụng muộn.

## Bảo mật

- Mã nhận không được lưu vào `sessionStorage`, `localStorage` hay log. Chỉ mã phiên (không đủ để gửi mã quét) được lưu.
- Card chỉ hiện khi người dùng đã đăng nhập, vì nằm trong `AppShell`.
- Điện thoại chỉ gửi được mã sau khi người dùng bấm Cho phép trên máy tính.

## Rollback

Hoàn tác các file trên và gỡ `uqr`. API của phase 2 vẫn đứng riêng được.

## Việc cần làm

- [x] `session-store`, `event-stream`, API client của scanner
- [x] `ScannerProvider` (khởi động, tạo phiên, làm mới mã, kết nối lại, sự kiện `state` và `restarting`)
- [x] Bước Cho phép / Từ chối với mã xác minh
- [x] `useScanTarget` qua ref, hàng đợi giữ promise theo `scan_id`, bỏ mã quá hạn
- [x] `PhonePairingCard` và `PairingQr`
- [x] Đặt card và đăng ký đích ở POS và Kiểm kê
- [x] Vitest ở mục Validation
