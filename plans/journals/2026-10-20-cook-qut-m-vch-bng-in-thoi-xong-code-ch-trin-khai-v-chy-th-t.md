---
title: "Cook quét mã vạch bằng điện thoại: xong code, chờ triển khai và chạy thử thiết bị thật"
date: 2026-10-20
summary: "Phase 1 tới 6 xong và đã review, docs đã cập nhật; chưa triển khai prod, chưa chạy thử trên điện thoại thật, chưa commit."
---

# Cook quét mã vạch bằng điện thoại: xong code, chờ triển khai và chạy thử thiết bị thật

## Chuyện gì đã xảy ra
Chạy `/ak:cook` cho plan 261020-1227-phone-barcode-scanning ở chế độ `--auto`. Phase 1 tới 6 đã làm xong: tra mã `/sku-lookup` với biến thể GTIN, API ghép đôi điện thoại (SSE, Cho phép kèm mã xác minh), thẻ ghép đôi trên máy tính, trang `/scan` trên điện thoại (zxing-wasm, không precache), quét khi nhập hàng, và ngăn "Mã vạch" để đổi đơn vị của mã trong Danh mục.

Có bốn lượt review. Các lỗi đáng kể đã sửa:
- Phiên điện thoại không biết máy tính đã đăng xuất cho tới hết desktopGrace. Nay stream điện thoại cũng kiểm tra login session mỗi tick.
- Mở 4 tab gây vòng thu hồi lẫn nhau. Nay đóng phiên chưa có điện thoại cũ nhất, và tab bị đóng không tự tạo phiên mới.
- Nhận hàng theo đơn: lần quét cộng chồng lên số còn chờ đã điền sẵn nên không ghi sổ được. Nay lần quét đầu trên một dòng thay số, các lần sau cộng.
- Mã đến lúc đang lưu phiếu bị mất trong khi điện thoại báo thành công. Nay phiếu trả not_accepting khi đang lưu hoặc có hộp thoại đè lên.
- Ô chọn đơn vị mã vạch lưu ngay ở mỗi lần bấm mũi tên. Nay phải bấm Lưu.

## Quyết định
- Không chạy `make prod-migrate` hay `make prod-up`: code chưa commit, prod dùng chung cho mọi cửa hàng, và rollback giả định có commit.
- Giữ thứ tự tra mã của POS (ô hàng trước, `/sku-lookup` sau). Đổi đơn vị mã chỉ invalidate bootstrap POS của cùng tab. Docs đã ghi rõ các thời điểm tải lại.
- Giữ deadline 10 s ở máy tính so với 4 s ở điện thoại. Cái này cần người dùng xác nhận.

## Kiểm chứng
`make lint`, `make test` (web 174 file, 1320 test), `make test-integration` (51 package) và web build đều qua. Compose config và `nginx -t` OK. `make db-docs` đã chạy sau khi sao lưu và migrate DB dev lên 60.

## Việc tiếp theo
- Chạy truy vấn trùng biến thể trên prod.
- Commit nếu muốn, rồi triển khai.
- Chạy thử trên Android và iPhone thật.
- Chạy parity và a11y với `make dev`.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
