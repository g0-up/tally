---
title: Căn chỉnh bảng đơn vị trong dòng hàng nhập
date: 2026-10-22
summary: "Sửa nền tiêu đề cột Xóa, căn giữa badge/radio/Xóa theo ô nhập, và giữ bảng đơn vị trong vùng nhìn thấy trên mobile"
---

# Căn chỉnh bảng đơn vị trong dòng hàng nhập

## What happened
Ảnh chụp review bảng "Đơn vị tính & giá bán" trong dòng hàng mới (Tạo đơn nhập) cho thấy ba lỗi:
- ô tiêu đề cột thao tác nền trắng;
- badge "Đơn vị cơ sở" và radio "Nhập theo" nằm cao hơn tâm ô nhập;
- trên mobile bảng rộng 872px trong vùng cuộn ngang 308px, ô nhập bị cắt và phải kéo ngang mới tới "Xóa".

## Root cause
- Rule cũ `.bp .ut-t th:last-child, .bp .ut-t td:last-child { position: static; background: none }` đặt ra để gỡ cột ghim của bảng ngoài. Sau khi rule ghim đã giới hạn bằng child combinator, rule này chỉ còn xóa nền tiêu đề và hủy `position: absolute` của ô Xóa dạng thẻ trên mobile.
- Các ô không có input căn `vertical-align: top` mà không có chiều cao bằng ô nhập; radio còn margin mặc định 3px phía trên.
- Dòng bảng đơn vị là một ô colSpan của bảng hàng nhập nên rộng bằng cả bảng, không phải bằng vùng nhìn thấy.

## Fix
- Bỏ rule cũ.
- Thêm `--ut-ctl` (42px; trong `.bp` bằng `--bp-ctl`), áp chiều cao cho `.ut-t .in`, `.ut-mid` (bọc badge), `.ut-buy` và link Xóa. Radio bỏ margin.
- `.bp` thành container (`container-type: inline-size`); `.ut` trong dòng `.bp-ut` dùng `position: sticky; left: 8px; width: calc(100cqi - 16px)`.

## Verification
Đo tâm dọc bằng Playwright trên stack dev: ô nhập, badge, radio và Xóa trùng tâm cả ở đơn nhập lẫn danh mục. Mobile 390px: bảng rộng 292px trong vùng 308px, Xóa nằm góc phải thẻ. Typecheck, eslint, 1393/1393 vitest đạt. E2E chưa chạy vì thiếu E2E_OWNER_PASSWORD.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
