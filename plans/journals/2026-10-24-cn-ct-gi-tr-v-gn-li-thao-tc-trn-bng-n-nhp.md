---
title: Căn cột Giá trị và gọn lại thao tác trên bảng đơn nhập
date: 2026-10-24
summary: "Tiêu đề cột số lệch vì .t th đè th.num; mỗi dòng còn một nút bước tiếp theo, Sửa/Xóa thành icon; app chỉ hỗ trợ máy tính và máy tính bảng."
---

# Căn cột Giá trị và gọn lại thao tác trên bảng đơn nhập

# Căn cột Giá trị và gọn lại thao tác trên bảng đơn nhập

## Chuyện gì đã xảy ra

Trên bảng Đơn nhập, tiêu đề "GIÁ TRỊ" căn trái trong khi số tiền bên dưới căn phải. Nguyên nhân: `.t th { text-align: left }` và `th.num { text-align: right }` có cùng độ ưu tiên, mà `.t th` khai báo sau trong `globals.css` nên thắng. Lỗi này có từ prototype và ảnh hưởng mọi bảng `.t` có cột số.

Mỗi dòng nháp có tới ba nút cùng nổi (Chốt đơn, Sửa, Xóa), nên khó thấy đâu là bước tiếp theo. Các nút "Nhận hàng" cũng không thẳng hàng giữa các dòng.

## Quyết định

- Thêm rule `.t th.num { text-align: right; }` dùng chung cho mọi bảng.
- Mỗi dòng chỉ còn một nút đặc màu navy cho bước tiếp theo ("Chốt đơn" hoặc "Nhận hàng"), có icon lucide. Nút này rộng tối thiểu 124px và nằm sát mép phải, nên các dòng thẳng hàng với nhau.
- "Sửa" và "Xóa" thành `IconButton` mới (`.btn-ic`): nút chỉ có icon, nhãn và tooltip là "Sửa PO-xxxx" / "Xóa PO-xxxx". Riêng Xóa chuyển sang màu đỏ khi rê chuột.
- Máy tính bảng: icon có vùng chạm 44px khi dùng `pointer: coarse`. Bảng rộng hơn màn hình thì cuộn ngang, còn cột thao tác dính ở mép phải (sticky, cần `:has()`).
- Người dùng xác nhận app chỉ hỗ trợ máy tính và máy tính bảng; điện thoại chỉ dùng làm máy quét đã ghép cặp. Đã cập nhật PRD NFR-10, `docs/operations/device-browser-matrix.md` và hướng dẫn sử dụng.

## Còn để ngỏ

- `e2e/visual/parity.spec.ts` vẫn chụp ở khung 390px (điện thoại). Nên đổi sang một khổ máy tính bảng.
- Prototype có cùng lỗi tiêu đề cột số, nên kiểm tra parity có thể lệch ở các ô đó.
- `e2e/support/mock-api.ts` có vẻ đã cũ so với schema hiện tại: `/me` thiếu `server_time`, body của tenant chưa đủ trường.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
