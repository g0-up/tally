---
title: Kế hoạch quét mã vạch bằng điện thoại ghép đôi
date: 2026-10-20
summary: "Lập kế hoạch 7 phase cho điện thoại làm máy quét qua QR; red team ra 15 phát hiện, áp dụng hết, thêm bước Cho phép"
---

# Kế hoạch quét mã vạch bằng điện thoại ghép đôi

## Chuyện gì đã xảy ra

Lập kế hoạch ở chế độ hard từ brainstorm `plans/reports/brainstorm-261020-1826-phone-barcode-scanning.md`. Kết quả nằm ở
`plans/261020-1227-phone-barcode-scanning/`: 7 phase, ước tính 8.5 ngày công. Chưa viết dòng code nào.

Red team bốn góc nhìn (Assumption Destroyer, Failure Mode Analyst, Scope Critic, Security Adversary) gộp lại thành 15
phát hiện, và người dùng chọn áp dụng cả 15. Các phát hiện quan trọng nhất:
- Critical: bản nháp đầu INSERT dòng quét trong `WithTenantTx` rồi chờ máy tính xác nhận ngay trong tx đó
  (`platform/db/tx.go:107-136`). Máy tính xác nhận từ connection khác nên không thấy dòng, mọi lần quét sẽ timeout, và
  mỗi yêu cầu đang chờ giữ một connection trong pool 20. Sửa bằng cách commit một tx ngắn, rồi `Await`, rồi `Publish`,
  và chờ ngoài tx.
- Mã từ điện thoại suýt đi qua tìm kiếm mờ của POS, vốn lọc theo chữ trong ô `q` chứ không theo mã.
- Kết quả đến muộn sau timeout cộng với việc quét lại sẽ cộng hai lần. Giờ mỗi mã có hạn (`ttl_ms`), và server trả
  409 `scan_expired` cho kết quả muộn.
- Route công khai của điện thoại bỏ qua middleware bảo trì và gói của tenant (`routes.go:262-278`), và phiên quét sống
  tiếp sau khi người dùng đăng xuất.
- Service worker đã cài sẵn sẽ trả vỏ cũ cho `/scan`.

## Quyết định

- Thêm bước Cho phép trên máy tính, kèm mã xác minh 4 số lấy từ băm bí mật, thay cho ghép đôi chỉ bằng quét QR. Đây là
  lựa chọn của người dùng để chống chiếm phiên khi QR bị chụp.
- Chỉ có một phép tra `/sku-lookup` cho mọi màn. Bỏ `/pos/tile-lookup` và `SkuListItem.barcodes`.
- Biến thể GTIN chỉ xử lý ở Go (`CodeVariants`), web chỉ khớp chính xác. `insertBarcode` chặn mã trùng theo biến thể.
- Kiểm kê cộng theo hệ số của đơn vị mã. POS offline từ chối mã của đơn vị khác đơn vị cơ sở.
- Chỉ tài liệu `/scan` có `camera=(self)` và `'wasm-unsafe-eval'`, qua một router Traefik riêng.

## Việc tiếp theo

- `/ak:cook plans/261020-1227-phone-barcode-scanning/plan.md`, bắt đầu từ phase 1.
- Còn phải xác nhận trên máy thật: lần mở đầu trên điện thoại còn giữ service worker cũ, và hành vi 404 của Traefik
  trong lúc `api` khởi động lại.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
