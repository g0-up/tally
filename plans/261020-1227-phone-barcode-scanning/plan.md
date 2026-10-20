---
title: "Quét mã vạch bằng điện thoại ghép đôi qua QR"
description: "Điện thoại quét QR trên máy tính để ghép đôi, được máy tính cho phép, rồi dùng camera quét mã vạch vào POS, Kiểm kê và Nhập hàng; kèm tra mã theo đơn vị, mã vạch cho hàng mới từ đơn nhập và đổi đơn vị mã vạch ở Danh mục."
status: in-progress
priority: P1
effort: 8.5d
branch: master
tags: [api, frontend, pwa, realtime, security, migration, catalog, pos, inventory, procurement]
blockedBy: []
blocks: []
created: 2026-10-20
---

# Quét mã vạch bằng điện thoại ghép đôi qua QR

## Kết quả mong muốn

Trên POS, Kiểm kê và Nhập hàng, máy tính hiện QR “Quét bằng điện thoại”. Điện thoại quét QR, mở trang quét trên host
của cửa hàng mà không cần đăng nhập, và sau khi người dùng bấm **Cho phép** trên máy tính, mỗi mã vạch camera đọc được
đi vào đúng luồng Enter của màn đang mở trong khoảng một giây, với đơn vị của mã vạch đó. Hợp đồng đầy đủ (mục tiêu,
ràng buộc, không làm, tiêu chí) nằm trong [brainstorm](../reports/brainstorm-261020-1826-phone-barcode-scanning.md).
Kế hoạch này giữ nguyên toàn bộ phạm vi đó. Điểm khác duy nhất là bước Cho phép, do người dùng chọn ở Validation Log
thay cho “chỉ cần quét QR là ghép đôi”.

## Bằng chứng và nghiên cứu

- Nghiên cứu SSE và ghép đôi: [researcher-261020-1908-sse-pairing-design.md](../reports/researcher-261020-1908-sse-pairing-design.md).
- Nghiên cứu thư viện quét và camera: [researcher-261020-1908-browser-barcode-scanning.md](../reports/researcher-261020-1908-browser-barcode-scanning.md).
- Bằng chứng mã nguồn (đường dẫn và dòng) được trích trong từng phase. Ba điểm định hình kế hoạch:
  `PosTile` chỉ có 30 hàng bán chạy (`apps/api/internal/sales/pos_bootstrap.go:16`), nên POS cần tra mã trên server;
  `WriteTimeout` 60 giây (`apps/api/internal/cli/serve.go:266`) sẽ cắt luồng SSE; web chưa có màn sửa hàng nào.

## Ràng buộc chính

- Không có SSE, websocket hay LISTEN/NOTIFY nào sẵn. Hub SSE nằm trong bộ nhớ, sau một interface nhỏ, vì prod chỉ có
  một container `api`. Trạng thái phiên nằm trong Postgres có RLS.
- Mã ghép đôi chỉ lưu bản băm SHA-256, hạn nhận 2 phút, nhận bằng một câu `UPDATE` nguyên tử. Bí mật của điện thoại nằm
  trong cookie `__Host-tally_scan` HttpOnly, Secure, SameSite=Strict, cũng chỉ lưu bản băm.
- Phản hồi cho điện thoại có schema cố định `{outcome, product_name?, unit_name?}` do server dựng, không bao giờ chứa giá
  hay tồn kho.
- Luồng Enter của ô quét giữ nguyên hành vi. Mã từ điện thoại gọi đúng hàm xử lý Enter của màn, không qua tìm kiếm mờ.
- Không giao dịch DB nào mở trong lúc chờ máy tính xác nhận.
- Thư viện WASM chỉ tải ở trang quét, không vào bundle chính và không bị service worker precache. Chỉ tài liệu `/scan`
  có `camera=(self)` và `'wasm-unsafe-eval'`.
- Hai migration: `000059` (index mã vạch theo SKU, phase 1) và `000060` (bảng ghép đôi, phase 2). `make prod-migrate`
  sao lưu DB trước khi chạy.

## Quyết định thiết kế

- **Khớp mã:** mã vạch thắng `sku_code` ở mọi nơi. Biến thể GTIN (đệm 12, 13, 14 chữ số và UPC-E↔UPC-A) chỉ có ở Go
  (`CodeVariants`), vì WASM trả UPC thành 13 chữ số. Web chỉ khớp chính xác. `insertBarcode` chặn mã trùng theo biến
  thể bằng khóa giao dịch, nên mọi đường thêm mã vạch đều trả `barcode_taken`.
- **Tra mã:** một phép tra duy nhất `GET /sku-lookup?code=` trả SKU, đơn vị và `factor` cho POS, Kiểm kê và Nhập hàng.
  `PosTile` có thêm `barcodes: [{code, unit_id}]` để POS khớp ô và bán offline mã của đơn vị cơ sở. Không có
  `/pos/tile-lookup` và không đổi `SkuListItem`.
- **Kiểm kê** cộng theo hệ số của đơn vị mã (mã thùng 24 hộp cộng 24). **POS offline** từ chối mã của đơn vị khác đơn vị
  cơ sở và báo cần có mạng.
- **Ghép đôi có bước Cho phép:** điện thoại nhận mã thì phiên ở `pending`; hai màn hình cùng hiện mã xác minh 4 số lấy
  từ băm bí mật; máy tính bấm Cho phép thì điện thoại mới bật được camera và gửi mã.
- **Phản hồi đồng bộ:** điện thoại gửi mã và chờ máy tính xác nhận tối đa 4 giây trong cùng một yêu cầu. Dòng quét được
  commit trước khi phát tới máy tính; mã quá hạn bị máy tính bỏ qua và server từ chối kết quả muộn.
- **Phiên theo tab:** mã phiên nằm trong `sessionStorage`. Phiên chết khi luồng SSE của máy tính im quá 60 giây, khi
  một bên ngắt, khi phiên đăng nhập của người tạo hết, hoặc sau 12 giờ kể từ lúc cho phép. Trạng thái được kiểm tra lười
  qua điều kiện SQL; dữ liệu cũ được xóa trong vòng dọn sẵn có của worker, không thêm tiến trình nào.
- **Khởi động lại:** `api` gửi `restarting` trước khi đóng luồng; hai bên kết nối lại không giới hạn số lần và chỉ coi
  lỗi JSON của API là kết thúc phiên.
- **Phản hồi trên điện thoại:** Android rung; iOS bíp bằng Web Audio kèm nháy màn hình; luôn có dòng chữ.

## Không làm

Giữ nguyên danh sách “Không làm lần này” trong brainstorm. Thêm: không có màn sửa hàng đầy đủ (xem phase 6), không có
hub dùng chung cho nhiều replica `api`.

## Phases

| Phase | File | Phụ thuộc | Công | Trạng thái |
|-------|------|-----------|------|------------|
| 1. Tra mã vạch theo đơn vị cho POS, Kiểm kê và Nhập hàng | [phase-01-barcode-lookup.md](phase-01-barcode-lookup.md) | không | 1.5d | pending |
| 2. API ghép đôi: bảng, hub SSE, nhận mã, cho phép, gửi mã, ngắt | [phase-02-pairing-api.md](phase-02-pairing-api.md) | 1 | 2d | pending |
| 3. Web máy tính: khung QR, cho phép, phiên theo tab, đích nhận mã | [phase-03-desktop-pairing-ui.md](phase-03-desktop-pairing-ui.md) | 1, 2 | 1d | pending |
| 4. Trang quét trên điện thoại, CSP và Permissions-Policy | [phase-04-phone-scan-page.md](phase-04-phone-scan-page.md) | 2, 3 | 1.5d | pending |
| 5. Nhập hàng: luồng quét và mã vạch cho hàng mới | [phase-05-purchasing-scan-flow.md](phase-05-purchasing-scan-flow.md) | 1, 3 | 1d | pending |
| 6. Danh mục: đổi đơn vị của mã vạch | [phase-06-catalog-barcode-units.md](phase-06-catalog-barcode-units.md) | 1 | 0.5d | pending |
| 7. Tài liệu, triển khai và chạy thử trên điện thoại thật | [phase-07-docs-deploy-device-qa.md](phase-07-docs-deploy-device-qa.md) | 1–6 | 1d | pending |

Thứ tự: phase 1 làm trước phase 2 (phase 2 dùng `ValidateBarcode` và cùng sửa `openapi.yaml`), phase 3 làm trước phase 4
(cùng thư mục `features/scanner/` và handler MSW). Phase 6 chỉ cần phase 1 nên làm song song được với phase 2–5.

## Tiêu chí hoàn thành

1. Mọi tiêu chí hoàn thành trong brainstorm đều đạt, kể cả chạy tay trên Android và iOS thật trên tenant thử của stack
   homelab, ghi vào `docs/operations/device-browser-matrix.md`.
2. Test đơn vị và test tích hợp mới đều qua: khớp mã (POS, Kiểm kê cộng theo hệ số, Nhập hàng thêm, cộng dồn, hàng mới,
   trùng biến thể), vòng đời phiên có bước Cho phép, cách ly tenant, giới hạn tần suất và số luồng, idempotency, xác nhận
   từ connection khác, khởi động lại, và phản hồi điện thoại không có giá hay tồn kho.
3. `make test`, `make test-integration`, `golangci-lint`, `corepack pnpm lint|typecheck|test|build` đều sạch.
   `openapi_contract_test.go` qua và `schema.d.ts` được sinh lại.
4. Bundle chính không chứa `zxing`. Service worker không precache chunk quét hay file `.wasm`, và không phục vụ `/scan`.
5. Tài liệu người dùng, ma trận thiết bị và runbook phản ánh hành vi mới.

## Red Team Review

### Session 2026-10-20

Bốn góc nhìn: Assumption Destroyer (AD), Failure Mode Analyst (FMA), Scope Critic (Scope), Security Adversary (Sec).
Người dùng chọn áp dụng cả 15 phát hiện đã gộp.

| # | Mức | Phát hiện | Nguồn | Xử lý | Phase |
|---|-----|-----------|-------|-------|-------|
| 1 | Critical | INSERT dòng quét chưa commit khi chờ máy tính, xác nhận không thấy dòng và cạn pool | FMA1, AD3 | Accepted: tx ngắn rồi mới `Await` và `Publish`, chờ ngoài tx | 2 |
| 2 | High | Mã từ điện thoại đi qua tìm kiếm mờ của POS và lọc theo `q` | FMA3, Sec3 | Accepted: `scanCode(code, source)` không đọc `q`, điện thoại chỉ khớp chính xác hoặc `/sku-lookup` | 1, 3 |
| 3 | High | Handler bị đóng băng theo lần render cũ, lọc trùng không bắt mã đang xử lý | FMA2, AD7 | Accepted: handler qua ref, ref cho số đếm, bảng promise theo `scan_id` | 1, 3 |
| 4 | High | Kết quả đến muộn sau timeout cộng với quét lại làm cộng hai lần | FMA6 | Accepted: `ttl_ms`, máy tính bỏ mã quá hạn, 409 `scan_expired`, câu timeout mới, không tự thử lại | 2, 3, 4 |
| 5 | High | Kiểm kê không thấy SKU ngoài 200 hàng đã tải | Scope4, AD1 | Accepted: `countScan` dùng `/sku-lookup` và tìm dòng theo `sku_id` | 1 |
| 6 | High | Route điện thoại bỏ qua bảo trì, gói và chế độ hỗ trợ; phiên quét sống qua đăng xuất | AD2, Sec4, Sec6 | Accepted: gọi `InMaintenance`, `CanWrite`; kiểm tra lại phiên đăng nhập ở mỗi nhịp | 2 |
| 7 | High | Deploy cắt ghép đôi; `EventSource` tự nối lại lỡ `claimed` | FMA4, FMA7 | Accepted: `restarting`, sự kiện `state` khi mở luồng, backoff không giới hạn, chỉ lỗi JSON mới kết thúc | 2, 3, 4, 7 |
| 8 | High | Hub hủy nhầm luồng mới, waiter rò rỉ, `Resolve` có thể chặn | FMA5, AD6 | Accepted: token hủy, `Await` trả hàm hủy, gửi không chặn, `Close` dọn waiter | 2 |
| 9 | High | SW đã cài trả vỏ cũ cho `/scan` | AD5, FMA10 | Accepted: denylist `/scan`, QA với PWA cũ, hướng dẫn tải lại | 4, 7 |
| 10 | High | Biến thể GTIN lách được index unique nguyên chuỗi | AD4, Scope5 | Accepted: biến thể chỉ ở Go, web khớp chính xác, `insertBarcode` khóa và kiểm tra biến thể | 1, 5 |
| 11 | High | Chống chiếm phiên qua QR bị chụp chưa có | Sec1 | Accepted: bước Cho phép kèm mã xác minh (Validation Session 1) | 2, 3, 4 |
| 12 | High | Không giới hạn tài nguyên; câu về limiter sai; ghi `desktop_seen_at` trong vòng SSE | Sec2, FMA8 | Accepted: 3 phiên mỗi người, giới hạn luồng của hub, limiter mở luồng, ghi ngoài vòng, sửa câu rủi ro | 2 |
| 13 | Medium | Phạm vi thừa: `/pos/tile-lookup`, `SkuListItem.barcodes`, bước `search.go` | Scope1, Scope2 | Accepted: một `/sku-lookup`, giữ `PosTile.barcodes` cho offline | 1, 6 |
| 14 | Medium | Gói offline đã lưu không có `barcodes` | Scope6, AD8 | Accepted: `.default([])`, `?? []`, test gói cũ | 1 |
| 15 | Medium | Hợp đồng và việc lặt vặt: idempotency, `ValidateBarcode`, dọn dữ liệu, cookie `__Host-`, header edge, lệnh compose, thứ tự phase, hook tắt máy, test đối tác, số dòng | Scope, AD, Sec | Accepted: sửa trong từng phase | 2, 4, 5, 6, 7 |

### Whole-Plan Consistency Sweep

Sau khi áp dụng, đã rà toàn bộ thư mục kế hoạch để bỏ các tham chiếu cũ: `tile-lookup`, `PosTileMatch`,
`SkuListItem.barcodes`, `codeVariants` ở web, bước `search.go`, cookie không có `__Host-`, lệnh compose sai, câu “làm song
song” giữa phase 1 và 2, và câu “theo quy tắc đã chốt ở Validation Log”. Dòng phụ thuộc trong frontmatter của từng phase
khớp bảng Phases.

## Validation Log

### Session 1 (2026-10-20)

| # | Câu hỏi | Trả lời của người dùng | Ảnh hưởng |
|---|---------|------------------------|-----------|
| 1 | Áp dụng bao nhiêu phát hiện của red team? | Áp dụng cả 15 | Mọi phase được sửa theo bảng Red Team |
| 2 | Chống chiếm phiên qua QR bị chụp thế nào? | Thêm bước Cho phép | Trạng thái `pending`, `:approve`, `:reject`, mã xác minh 4 số ở phase 2, 3, 4 |
| 3 | Kiểm kê quét mã của thùng thì cộng bao nhiêu? | Cộng theo hệ số | `countScan` cộng `factor`, điện thoại hiện “{tên} · Thùng” |
| 4 | Đổi đơn vị mã vạch ở Danh mục đặt ở đâu? | Ngăn Mã vạch | Nút “Mã vạch” mở ngăn chỉ đổi đơn vị của từng mã (phase 6) |
| 5 | Phản hồi trên iOS (không rung được)? | Bíp kèm nháy màn hình | Web Audio và nháy xanh hoặc đỏ, luôn có dòng chữ (phase 4) |
| 6 | Giữ hai kết quả `new_product` và `not_in_order`? | Giữ cả hai | Enum `ScanResult` và câu điện thoại (phase 2, 4, 5) |
| 7 | POS offline gặp mã của đơn vị khác đơn vị cơ sở? | Từ chối và báo rõ | “Mã này là của {đơn vị}, cần có mạng để bán theo đơn vị này.” (phase 1) |
