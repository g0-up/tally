# Sửa các phát hiện của review web/edge cho tính năng quét bằng điện thoại

Ngày: 2026-10-21 · Review gốc: `plans/reports/code-reviewer-261020-2246-scanner-web-edge.md`

## Kết quả

Đã sửa và kiểm thử tất cả các mục được giao, kể cả mục bổ sung của coordinator về việc phía điện thoại kiểm tra lại phiên đăng nhập. Mọi lệnh kiểm tra đều chạy sạch, không có lỗi nào phát sinh từ file catalog của agent chạy song song.

## Từng mục

| Mục | Đã xác minh là thật | Cách sửa | Test |
|---|---|---|---|
| Trùng class `.scan` | Có. `.scan` của ô quét POS và `.scan` của trang quét trên điện thoại cùng nằm trong `globals.css` | Đổi riêng các rule của trang điện thoại sang `.scan-page…`, keyframes sang `scan-page-flash`. Giữ nguyên `.scan` gốc. Sửa bằng Edit có chủ đích, không ghi đè cả file | `phone/scan-page.test.tsx` cập nhật selector |
| Thu hồi lẫn nhau vô tận khi mở từ 4 tab | Có | Web: khi nhận `revoked` với `by:"system"` thì không tự tạo phiên mới mà hiện `CLOSED_TEXT` cùng nút "Ghép lại". Các giá trị `by` khác giữ hành vi cũ. Backend: `lockOpenSessions` trả thêm cờ `waiting`; hàm mới `sessionToClose` chọn phiên chưa có điện thoại nhận cũ nhất, chỉ khi mọi phiên đều claimed/pending mới chọn phiên cũ nhất | Vitest: thu hồi bởi system thì 10 s không có POST, bấm "Ghép lại" thì tạo phiên mới. Go: unit `TestSessionToCloseSparesClaimedSessions`; integration `TestFourthSessionRevokesTheOldestWaitingOne` và `TestFourthSessionRevokesTheOldestWhenPhonesClaimedThemAll`. Hai test cũ `TestFourthSessionRevokesTheOldest` và `TestConcurrentOpensKeepThreeSessions` vẫn pass |
| Enter gửi trùng hoặc dồn mã ở POS | Có | `submitScan` lấy chữ trong ô qua ref, xóa ô ngay lúc gửi, rồi xếp các lần gửi thành một hàng đợi promise: không lần nào bị bỏ, hai lần quét cùng một mã đều được cộng. Mã không thêm được dòng nào sẽ trả lại ô nếu ô vẫn trống. Không gọi `/sku-lookup` khi chuỗi không thể là mã vạch (`canLookUpCode`: ASCII in được, không có khoảng trắng, tối đa 64 ký tự) | `pos-page.test.tsx`: Enter hai lần chỉ thêm một; hai mã quét liền nhau thành hai dòng, không bị nối; mã không thêm được thì trả lại ô; tên có khoảng trắng thì không gọi `/sku-lookup`. Thêm test trong `scan-match.test.ts` |
| Live region của thẻ ghép đôi | Có | Một phần tử `role="status"` duy nhất, tồn tại suốt vòng đời của thẻ. Nút "Cho phép"/"Từ chối" do component render, để truyền ref mà không vi phạm `react-hooks/refs` | `phone-pairing-card.test.tsx`: vẫn là cùng một node qua các trạng thái waiting, pending, claimed |
| POS nhận mã từ điện thoại khi đang thanh toán hoặc đang mở hộp thoại | Có | Chuyển target vào `PosCartContainer` (prop mới `onPhoneScan`). Target là `null` khi checkout, offline checkout hoặc lưu giỏ đang chạy. Handler trả `NOT_ACCEPTING` khi `hasBlockingLayer()`, cùng quy tắc với phím tắt POS. `ScanHandler` được mở rộng để trả `ScanAnswer` | `pos-cart-container.test.tsx`: khi checkout đang chạy thì trả `not_accepting` và nhận lại sau khi xong; khi có lớp chặn thì trả `not_accepting`, gỡ lớp thì `added` |
| Nhóm mục nhỏ (Low) | Có | Xóa `noticeTimer` khi nhận `revoked` lặp lại, và trong `pairHere`. Jitter ±20% cho backoff (`reconnect.ts`, có thể inject `random`). Timeout riêng cho từng mã trong hàng đợi desktop, giới hạn bởi ttl của mã. Comment của `useScanTarget` nay khớp với thứ tự effect thực tế | `scanner-provider.test.tsx` (thông báo sau giữ đủ thời gian và chỉ tạo một phiên), `reconnect.test.ts`, `use-scan-target.test.tsx` (handler bị treo thì bỏ qua khi hết ttl, mã sau vẫn được xử lý) |
| Coordinator: đăng xuất khi stream desktop đã đóng | Có | `phoneCheck` gọi `SessionActive` với `created_by` và `login_session_id` của phiên đã nạp (chỉ đọc, không chạm `last_seen_at`). Nếu phiên đăng nhập không còn thì gọi `RevokeForSignOut` với `context.WithTimeout(context.WithoutCancel(ctx), tickTimeout)` | `TestSignOutAfterTheDesktopStreamClosedEndsThePhoneStream`: test fail khi tắt đoạn kiểm tra mới, pass khi bật. `TestPhoneStreamOutlivesAReauthenticatedDesktop` xác nhận cookie bị xoay vòng không làm ngắt điện thoại |

**Khác với mô tả của coordinator:** khi bị thu hồi bởi `system`, điện thoại nhận sự kiện `ended`, không phải `revoked{by:"system"}`. Desktop nhận `revoked{by:"system"}`. Đây là contract sẵn có trong `Service.ended`, và `TestSignedOutDesktopEndsTheSession` đã khẳng định nó, nên tôi giữ nguyên. Với `tickEvery` = 10 s, điện thoại được báo trong khoảng 10 s sau khi đăng xuất. "Đăng xuất mọi thiết bị" đi qua cùng `SessionActive` nên cũng được báo như vậy.

## File đã sửa

- Web: `apps/web/src/shared/styles/globals.css` (chỉ khối rule của trang điện thoại và `.pair-asking`); `features/scanner/{scanner-controller.ts, reconnect.ts (mới), reconnect.test.ts (mới), scanner-provider.tsx, scanner-provider.test.tsx, use-scan-target.ts, use-scan-target.test.tsx, index.ts, api/schemas.ts, api/mutations.ts, components/phone-pairing-card.tsx, components/phone-pairing-card.test.tsx, pages/scan-page.tsx, phone/use-phone-session.ts, phone/use-phone-session.test.ts, phone/scan-page.test.tsx, testing/scanner-harness.ts}`; `features/pos/{pages/pos-page.tsx, pages/pos-page.test.tsx, containers/pos-cart-container.tsx, containers/pos-cart-container.test.tsx, lib/scan-match.ts, lib/scan-match.test.ts}`.
- API: `apps/api/internal/scanner/{repo.go, service.go, model.go, http.go, service_test.go, scanner_integration_test.go}`.

## Kiểm tra

- `corepack pnpm lint`, `typecheck`: pass. `test`: 174 file, 1308 test, pass. `build`: pass.
- `go vet ./...`: pass. `go test -race ./internal/scanner/...`: pass. `go test -race -tags integration ./internal/scanner/...`: pass. Testcontainers đã dừng.
- `make lint`: golangci-lint báo 0 issues; eslint và tsc pass.

## Không đổi theo chỉ thị

scanTTL/scanWait, router và middleware Traefik, `/scan/` và cách khớp host, skip-waiting của service worker. Vì vậy cửa sổ ttl và service worker trong review vẫn để nguyên. Phần rỗng của `verify_code` cũng không làm vì không nằm trong danh sách được giao.

## Câu hỏi còn mở

- Nội dung `CLOSED_TEXT` nói rằng tài khoản vừa mở phiên ở tab hoặc máy khác. Thu hồi bởi `system` cũng xảy ra khi đăng xuất, nhưng lúc đó trang desktop thường đã rời đi nên người dùng gần như không thấy câu này. Nếu muốn một câu chung hơn thì cần quyết định về nội dung.
