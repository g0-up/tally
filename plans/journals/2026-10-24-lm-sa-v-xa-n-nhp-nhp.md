---
title: Làm sửa và xóa đơn nhập nháp
date: 2026-10-24
summary: "Web sửa và xóa được đơn nhập nháp, chỉ gửi phần đã đổi theo If-Match; review bắt hai lỗi mất dữ liệu ở form sửa và cả hai đã sửa; e2e viết xong nhưng chưa chạy."
---

# Làm sửa và xóa đơn nhập nháp

## Chuyện gì đã xảy ra

Đã làm xong plan `plans/261024-1051-edit-draft-purchase-order/`.

- **API:** không đổi gì. `procurement_integration_test.go` giờ khóa hợp đồng của PATCH, PUT lines và DELETE: trường hợp thành công, 412 khi sai version, 409 `order_not_draft`.
- **Dữ liệu web:**
  - `useSaveOrder` có nhánh sửa nháp (`saveDraftEdits`). Nó gửi PATCH phần đầu đơn rồi PUT dòng hàng, mỗi bước chỉ khi có thay đổi. Sau đó là `:confirm` và giá bán. Version được nối qua từng bước. Gặp 412 hoặc mất phản hồi thì đọc lại đơn rồi làm tiếp từ bước còn dở.
  - `supplier_party_id` chỉ được gửi khi NCC đổi.
  - Thêm `useDeleteOrder`. `isStale` chuyển sang `shared/api/api-error.ts`.
- **Giao diện:**
  - Dòng nháp có Sửa và Xóa. Drawer của đơn nháp có Sửa và Xóa nháp.
  - `NewPoDialog` mở "Sửa đơn nhập PO-…" đã điền sẵn.
  - Thêm `po-delete-dialog.tsx`.
- **Tài liệu:** `docs/user-guide/nhap-hang.md` bước 2 mô tả Sửa và Xóa.

## Review bắt được gì

1. **Mất màu.** Form sửa dựng dòng khi danh sách màu chưa tải xong. Lúc đó mọi `color_id` đọc ra "không màu", nên Lưu nháp sẽ xóa màu của dòng. Đã sửa: `ColorDirectory` có thêm `pending` và `error`, và form chờ danh sách màu khi đơn có dòng mang màu.
2. **Mất tiến độ lưu.** Form được key theo `id:version`. Một lần lưu thành công đổi version, nên form remount, mất chữ người dùng đã gõ và mất bước đã xong. Vì vậy lần thử lại "chỉ còn giá bán" lại gửi lại PUT. Đã sửa: form giữ bản nháp lúc mở (setState trong render) và không remount nữa. Có test "retries only the price…" chứng minh.
3. Đơn đã chốt ở nơi khác mà vẫn mở được form, và SKU tải lỗi thì làm rớt dòng. Cả hai đã chặn bằng thông báo.

Không sửa, đã ghi lại:

- Ngày dự kiến không xóa được, vì API không nhận giá trị rỗng.
- Gửi lại dòng thì `discount_amount` về 0, vì UI chưa có ô chiết khấu.
- Thử lại sau 412 ghi đè bản mới hơn. Đây là thiết kế theo plan, giống `useSaveInvoice`.

## Đo giao diện

Đo ở 390px bằng Chromium với danh sách giả. Trang không cuộn ngang. Bảng vốn đã cuộn trong card, rộng 775px, và giờ thành 872px. Phương án dự phòng là chỉ giữ Sửa, nhưng vẫn rộng 819px nên cũng không đạt mốc "không cuộn hơn hiện tại". Vì người dùng đã chọn có cả hai nút, nên giữ cả hai. Ở 1280px bảng vẫn rộng 960px, các nút nằm trên một dòng.

## Kiểm tra

- `make test` qua: API, và web với 183 file / 1410 test.
- Test tích hợp qua. eslint và tsc của web sạch.
- `make lint` dừng ở một lỗi errcheck có sẵn tại `apps/api/internal/catalog/catalog_test.go:541`, file mà plan này không đụng tới.
- E2e "edits a draft order, confirms it, and deletes another draft" đã typecheck và được Playwright liệt kê, nhưng chưa chạy vì máy này không có `E2E_OWNER_PASSWORD`.

## Bước tiếp

- Chạy e2e với tài khoản thật, rồi đánh dấu tiêu chí 7 của plan.
- Sửa lỗi errcheck ở `catalog_test.go:541`.
- Ngoài phạm vi, cùng một kiểu lỗi: `new-receipt-dialog.tsx` và `useColorMaps` của hóa đơn bán cũng điền màu trước khi danh sách màu tải xong. Mock `/me` trong `e2e/support/mock-api.ts` thiếu `server_time`.
- Còn treo: nếu PUT có dòng hàng mới bị mất phản hồi, lần thử lại có thể bị từ chối vì trùng tên hàng.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
