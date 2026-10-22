---
title: Chuyển test token và harness sang prototype bản minify
date: 2026-10-22
summary: "tokens.test.ts đọc được khối :root đã minify; mọi tham chiếu trỏ sang prototype-v6.15.min.html"
---

# Chuyển test token và harness sang prototype bản minify

## What happened
`docs/design/prototype-v6.15.html` được thay bằng `prototype-v6.15.min.html`. `tokens.test.ts` lỗi ENOENT vì vẫn đọc đường dẫn cũ; trỏ sang bản minify thì vẫn lỗi "no :root block".

## Root cause
- `rootBlock` tìm đúng chuỗi `":root {"` có dấu cách. Bản minify viết `:root{`, và khối `:root{` đầu tiên là khối khởi tạo (color-scheme, safe-area) không có token.
- Regex thuộc tính bắt buộc `;` ở cuối, trong khi bản minify bỏ dấu `;` của khai báo cuối (`--series-2`).
- Minify bỏ dấu cách sau dấu phẩy (gradient, font stack) nên giá trị lệch chuỗi với `tokens.css` dù tương đương.

## Fix
- `rootBlock` lấy khối `:root` đầu tiên có khai báo custom property, chấp nhận có hoặc không có dấu cách.
- Khai báo cuối không có `;` vẫn được đọc; khoảng trắng quanh dấu phẩy được chuẩn hóa ở cả hai phía so sánh.
- `prototype-harness.ts`, `parity.spec.ts`, `tokens.css`, `globals.css` trỏ sang `prototype-v6.15.min.html`.

## Verification
`tokens.test.ts` 17/17, toàn bộ vitest 1393/1393, typecheck và eslint sạch. Mở bản minify bằng Chromium headless: h1, sidebar hiện, không có lỗi trang, giống bản gốc. Suite parity chưa chạy vì project visual cần phiên đăng nhập (thiếu E2E_OWNER_PASSWORD).

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
