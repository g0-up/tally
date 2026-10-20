# Báo cáo triển khai: sửa luồng quét mã khi nhập hàng và đặt hàng

## Kết quả

Đã sửa xong cả sáu mục mà lead giao. Toàn bộ kiểm tra web (lint, typecheck, 174 file test với 1320 test, build) đều qua. Kiểm tra Go (`go vet ./...`, `go test -race ./internal/scanner/...`, `go test -race -tags integration ./internal/scanner/...`) cũng qua. Riêng `make lint` ở thư mục gốc có phần Go báo 0 lỗi, nhưng phần web dừng vì 5 lỗi `no-non-null-assertion` trong `apps/web/src/features/catalog/containers/barcode-units-drawer.test.tsx`. File đó thuộc agent sửa catalog đang chạy song song, không thuộc phạm vi của tôi. Chạy eslint riêng trên `src/features/procurement` và `src/test/msw` thì sạch.

## File đã sửa

- `apps/web/src/features/procurement/buy-lines.ts`: thêm cờ `scanned` vào `BuyLine` (mặc định `false` trong `BLANK`, nên mọi cách dựng dòng đều bắt đầu chưa quét). Thêm `orderRowOf` để chọn dòng của đơn mà một lần quét rơi vào, và `countOnOrderRow` để lần quét đầu thay số lượng còn các lần sau thì cộng thêm. Thêm kiểu `SentWrite { lines, before }`. `barcodeErrors` không trả lỗi nào khi lỗi hiện tại vẫn là lỗi của lần lưu trước.
- `apps/web/src/features/procurement/components/buy-preset.tsx`: thêm prop `busy`. Trình xử lý quét trả `NOT_ACCEPTING` khi đang lưu hoặc khi bảng đang bị một hộp thoại modal khác che. Điều kiện này được kiểm tra hai lần, trước khi tra mã và sau khi tra mã xong. Khi `busy` thì bỏ đăng ký đích quét (`useScanTarget(null)`).
- `apps/web/src/features/procurement/containers/new-po-dialog.tsx` và `new-receipt-dialog.tsx`: lưu `SentWrite` kèm lỗi trước khi gửi, rồi truyền `busy={save.pending}` hoặc `busy={post.pending}`.
- `apps/api/internal/scanner/service.go`: `RevokeForSignOut` chỉ báo và đóng stream khi chính lần gọi này đổi được dòng. Bên thua không gọi `hub.Close` nữa.
- Test: `buy-lines.test.ts`, `containers/new-receipt-dialog.test.tsx`, `containers/new-po-dialog.test.tsx` (file mới, chưa được git theo dõi), `apps/api/internal/scanner/scanner_integration_test.go`.

## Hành vi người dùng thấy (dùng cho docs/user-guide/nhap-hang.md)

### Quy tắc 1: nhập theo đơn đặt hàng, lần quét đầu thay số, các lần sau cộng thêm

Khi mở phiếu nhập theo một đơn đặt hàng, mỗi dòng được điền sẵn số "còn chờ". Lần quét điện thoại đầu tiên vào một dòng sẽ thay số đó bằng đúng lượng vừa quét, tính theo đơn vị gốc. Ví dụ một dòng còn chờ 24 hộp, quét mã thùng (1 thùng = 12 hộp) thì dòng thành 12, quét thêm một thùng nữa thì thành 24, quét tiếp một mã hộp lẻ thì thành 25. Số gõ tay không được tính là đã quét. Vì vậy một dòng đã sửa tay vẫn bị lần quét đầu tiên thay số. Nếu một dòng đã được quét rồi mới sửa tay, các lần quét sau sẽ cộng vào số đã gõ. Đóng rồi mở lại phiếu thì mọi dòng trở về trạng thái chưa quét. Mã của hàng không có trong đơn vẫn bị báo "không có trong đơn" như trước, và số lượng không đổi.

### Quy tắc 2: không nhận mã khi đang lưu hoặc khi có hộp thoại khác đè lên

Trong lúc nút "Ghi sổ nhập kho", "Lưu nháp" hoặc "Lưu & chốt đơn" đang chạy, điện thoại nhận thông báo máy tính chưa nhận mã và phiếu không thêm dòng nào. Điều này áp dụng cả với mã đã bắt đầu tra trước khi bấm lưu mà kết quả tra về sau khi việc lưu đã bắt đầu. Khi lưu xong (thành công hoặc lỗi), phiếu nhận mã trở lại nếu vẫn đang mở. Phiếu cũng từ chối mã khi một hộp thoại khác đang mở đè lên nó, ví dụ "Thêm nhà cung cấp mới" sau khi bấm "+ NCC mới". Đóng hộp thoại đó (ví dụ bấm "Quay lại phiếu") thì quét lại bình thường. Lịch chọn ngày không phải hộp thoại modal nên không chặn việc quét.

Cách nhận biết "bị đè" như sau. `hasBlockingLayer` là cờ toàn cục "đang có lớp chặn nào đó", mà chính hộp thoại phiếu cũng là một lớp chặn, nên dùng nó thì sẽ luôn từ chối. Tôi dựa vào việc hộp thoại modal của Radix gắn `aria-hidden="true"` lên mọi thứ nằm ngoài modal trên cùng. Bảng dòng hàng chỉ nằm trong vùng `aria-hidden` khi có một modal khác mở phía trên hộp thoại phiếu.

### Quy tắc 3: cùng một hàng nằm trên nhiều dòng của đơn

Khi một mã hàng xuất hiện trên nhiều dòng của đơn (ví dụ hai màu, hoặc đặt hai lần), mỗi lần quét rơi vào dòng đầu tiên của mã đó mà lượng đã quét còn dưới số "còn chờ". Một dòng chưa được quét lần nào được tính là đã quét 0. Khi mọi dòng của mã đó đã đủ, phần quét thêm dồn vào dòng đầu tiên. Quy tắc 1 vẫn áp dụng cho từng dòng: lần quét đầu tiên vào mỗi dòng thay số điền sẵn. Ví dụ hai dòng còn chờ 2 và 2 thì năm lần quét hộp lẻ lần lượt cho ra [1, 2], [2, 2], [2, 1], [2, 2], [3, 2]. Dung sai 5% khi ghi sổ vẫn được kiểm tra như cũ.

## Chi tiết các mục khác

- Mục 4 (lỗi mã vạch của lần lưu trước hiện thoáng trên dòng khác): `sent` giờ lưu kèm lỗi đang có lúc bấm lưu (`before`). `barcodeErrors` bỏ qua lỗi đó, nên trong lần render mà `sent` đã đổi còn `error` chưa đổi thì không lỗi nào bị gắn nhầm dòng. Có test riêng trong `buy-lines.test.ts`.
- Mục 5 (`RevokeForSignOut`): mọi lần thu hồi phiên quét đều đi qua `Service`, và nhánh đổi được dòng luôn gọi `ended` (gửi sự kiện rồi `Close`). Hub là bộ nhớ trong một tiến trình. Stream của desktop và của điện thoại tự kết thúc qua kết quả tick, và kiểm tra lại trạng thái sau khi Subscribe. Vì vậy bên thua (không đổi dòng nào) bỏ `Close` là an toàn. Test tích hợp mới `TestLosingSignOutRevokeLeavesTheStreamsOpen` chạy như sau: thu hồi lần một, mở một stream mới đại diện cho stream chưa nhận được sự kiện kết thúc, thu hồi lần hai, rồi kiểm tra stream không bị đóng, không nhận sự kiện nào và vẫn nhận được Publish. Tôi đã thử khôi phục nhánh `else { s.hub.Close(key) }` thì test này thất bại, sau đó hoàn tác về bản sửa.
- Mục 6 (test):
  - Nhập nhanh với mã vạch hàng mới được lưu thành công: chọn NCC, quét mã chưa có (tra mã trả 404) để tạo dòng hàng mới, nhập tên và giá, bỏ quản lý lô, rồi ghi sổ. Test kiểm tra body gửi đi có `barcode`, đơn vị và đơn vị mua với hệ số 12.
  - Quét trong lúc ghi sổ: giữ lượt tra mã và giữ `POST /receipts`, hai mã đều nhận `not_accepting` và không dòng nào bị thêm. Có test tương tự cho "Lưu nháp" của đơn đặt hàng.
  - Hộp thoại "Thêm nhà cung cấp mới" đè lên phiếu thì mã bị từ chối, quay lại phiếu thì mã được thêm.
  - Đơn có cùng một mã hàng trên hai dòng được điền theo thứ tự.
  - Đã thử bỏ điều kiện chặn thì các test chặn thất bại, và bỏ lần kiểm tra lại sau khi tra mã thì test ghi sổ thất bại.

## Lưu ý

- Vẫn còn một khoảng hở rất ngắn (một lần render) giữa lúc bấm nút và lúc `pending` được render. Khoảng hở này giống màn POS, và lần kiểm tra lại sau khi tra mã đã bịt trường hợp mã đang được tra dở.
- Test tích hợp Go cho nhập nhanh với mã vạch hàng mới (`apps/api/internal/app/purchasing_screens_integration_test.go`) nằm ngoài quyền sửa file của tôi, nên hiện mới chỉ được phủ ở tầng web.
- Vấn đề có sẵn, ngoài phạm vi: sau một lần ghi sổ thành công một phần (phiếu đã lưu nhưng bước sau lỗi), các mã quét thêm trước khi thử lại không được gửi lại.
- Không còn container testcontainers nào từ lần chạy này, và không để lại tiến trình nền.

Status: DONE_WITH_CONCERNS
Summary: Đã làm xong quy tắc 1 đến 3, mục 4, mục 5 (bên thua không đóng stream, có test tích hợp) và các test yêu cầu. Kiểm tra web và Go đều qua.
Concerns/Blockers: `make lint` dừng ở `apps/web/src/features/catalog/containers/barcode-units-drawer.test.tsx`, file của agent catalog chạy song song, không phải file của tôi. Test tích hợp Go cho nhập nhanh hàng mới nằm ngoài phạm vi file của tôi.
