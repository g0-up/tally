---
title: "Gom quản lý đơn vị của hàng nhập vào bảng Đơn vị & giá"
date: 2026-10-22
summary: "Bỏ cột Đơn vị tính ở bảng hàng nhập; ĐV cơ sở và đơn vị nhập của hàng mới chọn trong bảng Đơn vị & giá; nút xóa đơn vị thành link Xóa"
---

# Gom quản lý đơn vị của hàng nhập vào bảng Đơn vị & giá

## What happened
Bảng hàng nhập (Đơn nhập, Nhận hàng) có ba chỗ đụng tới đơn vị: cột "Đơn vị tính", ô "ĐV cơ sở" dưới tên hàng mới, và bảng "Đơn vị & giá". Người dùng muốn chỉ còn một chỗ quản lý đơn vị và nút xóa giống nhau ở mọi dòng.

## Changes
- `apps/web/src/features/procurement/components/buy-preset.tsx`: bỏ cột Đơn vị tính và ô ĐV cơ sở. Với hàng có sẵn nhiều đơn vị, ô chọn đơn vị nằm dưới ô Số lượng; hàng chỉ có một đơn vị thì hiện tên đơn vị.
- `apps/web/src/features/catalog/components/unit-price-table.tsx`: ĐV cơ sở chọn bằng combobox trong bảng, thêm prop `onPurchaseUnitChange` cho cột radio "Nhập theo", nút × đổi thành LinkButton "Xóa" (cả ở danh mục).
- `apps/web/src/features/procurement/buy-lines.ts`: `lineUnitErrors` giờ báo cả lỗi thiếu ĐV cơ sở; khi bị từ chối thì bảng đơn vị mở ra và thông báo nêu tên dòng.
- `apps/web/src/shared/styles/globals.css`: cột thao tác ghim chỉ áp cho bảng ngoài (dùng child selector và loại `tr.bp-ut`), để bảng đơn vị lồng bên trong không còn bị dính kiểu sticky.

## Decision
Hàng có sẵn vẫn giữ ô chọn đơn vị nhập (đặt dưới Số lượng) để không mất chức năng đổi thùng/hộp trên đơn. Đây là quyết định cần người dùng xác nhận.

## Verification
Typecheck và eslint sạch, 1393/1393 unit test pass. Đã cập nhật `e2e/procurement.spec.ts` nhưng chưa chạy vì stack dev không bật.

## Next steps
Chạy e2e procurement và catalog khi bật stack dev, rồi kiểm tra trực quan bảng đơn vị ở màn hình hẹp.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
