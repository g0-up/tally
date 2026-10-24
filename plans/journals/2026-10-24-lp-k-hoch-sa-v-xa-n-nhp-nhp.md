---
title: Lập kế hoạch sửa và xóa đơn nhập nháp
date: 2026-10-24
summary: "API đã có PATCH, PUT lines và DELETE cho đơn nháp nhưng chưa có test và web chưa gọi; plan 4 phase làm phía web và khóa hợp đồng API bằng test."
---

# Lập kế hoạch sửa và xóa đơn nhập nháp

## Chuyện gì đã xảy ra

Người dùng muốn sửa Đơn nhập khi còn ở trạng thái `draft`. Khi dò code thì thấy API đã đủ:

- `PATCH /purchase-orders/:id` (`UpdateOrder`) sửa NCC, ngày đặt, ngày dự kiến và ghi chú.
- `PUT /purchase-orders/:id/lines` (`ReplaceLines`) thay toàn bộ dòng hàng, kể cả dòng "+ Hàng hóa mới".
- `DELETE /purchase-orders/:id` (`DeleteOrder`) xóa đơn nháp.

Cả ba đều đi qua `lockDraft`: chỉ nhận đơn nháp, và chỉ khi đúng `If-Match` version. Nhưng chưa có test nào gọi ba endpoint này, và web cũng chưa dùng chúng. Bảng và drawer hiện chỉ có nút "Chốt đơn" cho đơn nháp. Prototype v6.15 cũng không có màn sửa đơn nhập.

## Quyết định

Plan nằm ở `plans/261024-1051-edit-draft-purchase-order/`, gồm 4 phase:

1. Viết test tích hợp để khóa hợp đồng của PATCH, PUT lines và DELETE: trường hợp thành công, 412 khi sai version, 409 `order_not_draft` khi đơn đã chốt.
2. Lớp dữ liệu web:
   - Thêm `draftOrderBuyLine` để dựng dòng từ đơn đã lưu.
   - Thêm nhánh sửa cho `useSaveOrder`, theo mẫu `useSaveInvoice`: chỉ PATCH hoặc PUT phần đã đổi, nối version giữa các bước, gặp 412 hoặc mất phản hồi thì đọc lại đơn.
   - Thêm `useDeleteOrder`.
   - Chuyển `isStale` sang `shared/api/api-error.ts`.
3. Giao diện:
   - Nút Sửa/Xóa trên dòng nháp của bảng và trong drawer.
   - `NewPoDialog` có thêm chế độ sửa, tiêu đề "Sửa đơn nhập PO-…".
   - Thêm `po-delete-dialog.tsx`.
4. Viết e2e cho luồng sửa và xóa, và cập nhật `docs/user-guide/nhap-hang.md`.

Người dùng quyết định ngày 2026-10-24:

- Nút đặt ở cả dòng của bảng và drawer.
- Không cập nhật prototype.
- Làm luôn chức năng xóa nháp.

Ngoài phạm vi: sửa ngày đặt và ghi chú (dialog tạo đơn không có hai trường này), và sửa đơn đã chốt.

Lần trả lời PUT lines bị mất thì không tạo trùng hàng mới: nhờ If-Match, lần thử lại nhận 412 rồi đọc lại đơn.

## Bước tiếp theo

Chạy `/ak:cook plans/261024-1051-edit-draft-purchase-order`. Nếu test hợp đồng ở Phase 1 lệch với mô tả thì dừng lại trước khi làm web.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
