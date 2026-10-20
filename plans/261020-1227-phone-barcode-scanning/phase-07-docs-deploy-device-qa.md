---
phase: 7
title: "Tài liệu, triển khai và chạy thử trên điện thoại thật"
status: pending
priority: P1
effort: 1d
dependencies: [1, 2, 3, 4, 5, 6]
---

# Phase 7: Tài liệu, triển khai và chạy thử trên điện thoại thật

## Mục tiêu và bối cảnh

Cập nhật tài liệu người dùng, ma trận thiết bị và runbook theo hành vi mới, đưa lên stack homelab (prod duy nhất, được
phép bật cho mọi tenant cùng lúc, không có cờ ẩn), rồi chạy thử trên Android và iOS thật trên một tenant thử của
`*.sidecup.app`. Không dùng quick tunnel tới máy dev, vì tenant lấy từ subdomain.

Bằng chứng:
- Hướng dẫn người dùng: `docs/user-guide/ban-hang-pos.md` (`:9`, `:16`, `:33`), `kho-kiem-ke-gio-qua.md` (`:28`, `:32`),
  `nhap-hang.md` (`:7`, `:20`), `danh-muc-bang-gia.md` (`:7`), mục lục `README.md` (`:23-34`).
- Ma trận thiết bị: kiểm tra tay ở `docs/operations/device-browser-matrix.md:42-52`. Câu “Scanners must work as a
  keyboard (HID keyboard wedge)… Tally has no scanner driver” ở `:85-86` mâu thuẫn với tính năng mới.
- Runbook: mục kiểm tra trình duyệt và thiết bị (`docs/runbook.md:143-147`), kiểm tra header qua edge (`:325-332`) chưa
  có `permissions-policy` và ghi “Tally's routers use priority 3 and 2”. Repo không có tài liệu kiến trúc riêng, nên
  ràng buộc một replica `api` ghi vào runbook. Truy vấn prod chạy bằng
  `docker compose -p tally-prod exec postgres psql -U postgres -d tally` (`docs/runbook.md:341`).
- `make prod-migrate` sao lưu DB trước khi chạy migration (`Makefile:227`). `make prod-up` dựng lại và khởi động lại
  container (`Makefile:216-217`). `make db-docs` sinh lại `docs/database.md`.

<!-- Updated: Red Team 2026-10-20 - QA khởi động lại khi đang ghép đôi, PWA cũ, truy vấn trùng biến thể trước triển khai, router /scan, lệnh compose của CI -->
<!-- Updated: Validation Session 1 - Kiểm kê cộng theo hệ số, bước Cho phép, bíp kèm nháy trên iOS -->

## Files

- Sửa `docs/user-guide/ban-hang-pos.md`, `kho-kiem-ke-gio-qua.md`, `nhap-hang.md`, `danh-muc-bang-gia.md`, `README.md`.
- Sửa `docs/operations/device-browser-matrix.md`.
- Sửa `docs/runbook.md`.
- Sinh lại `docs/database.md` bằng `make db-docs` cho `scanner_sessions`, `scanner_scans` và index mới.
- Tạo báo cáo chạy thử ở `plans/261020-1227-phone-barcode-scanning/reports/`.

## Các bước

1. **Hướng dẫn người dùng** (tiếng Việt, câu đầy đủ):
   - POS: mục “Quét bằng điện thoại”: quét QR, so mã xác minh trên hai màn hình rồi bấm **Cho phép** trên máy tính,
     cho phép camera, kết quả trên điện thoại (Android rung, iPhone bíp kèm nháy màn hình), ngắt từ hai bên, cần có
     mạng, Safari iOS hỏi quyền camera mỗi lần mở trang. Chỉ bấm Cho phép khi mã trên điện thoại giống mã trên máy
     tính. Mã vạch của thùng thêm dòng theo thùng; khi offline chỉ bán được mã của đơn vị cơ sở. Nếu điện thoại mở QR
     mà hiện trang cũ hoặc báo không tìm thấy, bấm “Tải lại để cập nhật” hoặc đóng mọi tab của cửa hàng rồi quét lại.
     Khi điện thoại báo “Máy tính chưa phản hồi”, nhìn máy tính trước khi quét lại.
   - Kiểm kê: quét bằng điện thoại khi đang sửa phiên; mỗi lần quét cộng theo hệ số của đơn vị mã (mã của thùng 24 hộp
     cộng 24), khớp mọi mã vạch của hàng.
   - Nhập hàng: quét thêm hoặc cộng dồn, mã lạ mở Hàng mới có ô Mã vạch, mã trùng báo ở ô Mã vạch, dòng hàng mới đã
     đổi sang thùng không tự cộng, Nhận hàng theo đơn chỉ nhận hàng có trong đơn và cộng theo hệ số của đơn vị mã.
   - Danh mục: nút “Mã vạch” và cách đổi đơn vị của mã.
   - Mục lục: nhắc quét bằng điện thoại ở các dòng liên quan.
2. **Ma trận thiết bị.** Thêm hàng kiểm tra tay “Điện thoại ghép đôi làm máy quét” cho Android Chrome và iOS Safari:
   ghép đôi có bước Cho phép, Từ chối, quét EAN-13, UPC-A, mã thùng, mã lạ, ngắt từ hai bên, khóa màn hình rồi mở lại,
   đổi từ POS sang Kiểm kê không ghép lại, khởi động lại `api` khi đang ghép đôi, điện thoại có PWA của bản trước. Sửa
   câu `:85-86`: máy quét cầm tay vẫn phải chạy như bàn phím kết thúc bằng Enter, còn điện thoại ghép đôi là cách quét
   thứ hai, không cần driver.
3. **Runbook.**
   - Mục kiểm tra thiết bị: nhắc hàng điện thoại ghép đôi.
   - Kiểm tra qua edge:
     - `curl -sI https://<tenant>.sidecup.app/scan` có `permissions-policy: camera=(self), …`, CSP có
       `script-src 'self' 'wasm-unsafe-eval'` và `referrer-policy: no-referrer`.
     - `curl -sI https://<tenant>.sidecup.app/` và `/sales/pos` vẫn có `camera=()` và CSP không có `script-src`.
     - `curl -sI https://<tenant>.sidecup.app/assets/<file>.wasm` trả `content-type: application/wasm`.
     - Sửa câu ưu tiên router thành “priority 4, 3 và 2” (`tally-web-scan`, `tally-api`, `tally-web`), và
       `tiendouong.sidecup.app/scan` vẫn trả từ Sidecup.
   - Lệnh kiểm tra compose:
     `docker compose -f infra/docker-compose.prod.yml -f infra/docker-compose.homelab.yml --env-file infra/.env.example config -q`.
   - Ghi ràng buộc: hub SSE nằm trong bộ nhớ, nên `api` chỉ chạy một replica. Tăng replica cần hub dùng chung (ví dụ
     LISTEN/NOTIFY) trước.
   - Khởi động lại `api` gửi `restarting` rồi đóng mọi luồng ghép đôi. Hai bên tự nối lại khi `api` sẵn sàng; điện
     thoại không phải quét lại QR nếu phiên còn sống (máy tính vắng quá 60 giây thì phiên kết thúc).
   - Phiên quét chết khi người tạo đăng xuất hoặc bị “Đăng xuất mọi thiết bị”, và worker xóa phiên cũ trong vòng dọn
     sẵn có.
4. **Kiểm tra trước khi triển khai.**
   - `make test`, `make test-integration`, `golangci-lint`, `corepack pnpm lint|typecheck|test|build`, kiểm tra bundle
     và `sw.js` như phase 4, lệnh kiểm tra compose ở bước 3, `make db-docs`.
   - Đếm cặp mã vạch chỉ khác số 0 đầu đã có trong prod:
     `docker compose -p tally-prod exec postgres psql -U postgres -d tally -c "select tenant_id, ltrim(code, '0') as gtin, count(*) from tally.sku_barcodes where code ~ '^[0-9]{12,14}$' group by 1, 2 having count(*) > 1"`.
     Kết quả khác rỗng không chặn triển khai, vì phép tra chọn mã khớp đúng như gõ trước, nhưng ghi số cặp và tenant
     vào báo cáo để báo chủ cửa hàng gộp lại. Cặp UPC-E hiếm hơn và không có trong truy vấn này.
5. **Triển khai.** `make prod-migrate` (sao lưu rồi chạy `000059` và `000060`), rồi `make prod-up`. Kiểm tra header qua
   edge theo runbook đã sửa.
6. **Chạy thử trên tenant thử** với một điện thoại Android (Chrome) và một iPhone (Safari) thật, ghi model, bản OS và
   trình duyệt:
   - Mọi tiêu chí hoàn thành trong brainstorm, gồm thời gian từ lúc quét tới lúc dòng hiện trên máy tính (mục tiêu
     khoảng một giây; ghi 10 lần đo).
   - Bước Cho phép: mã xác minh giống nhau trên hai màn hình; trước khi cho phép điện thoại không bật được camera;
     Từ chối đưa điện thoại về câu bị từ chối và máy tính hiện QR mới.
   - Mã của tenant A quét trên host tenant B bị từ chối.
   - Luồng SSE qua Cloudflare Tunnel sống quá 2 phút không có mã nào (ping giữ kết nối).
   - Đang ghép đôi thì chạy `make prod-up`: hai bên hiện “Đang kết nối lại…”, rồi quét tiếp được mà không quét lại QR.
     Ghi thời gian gián đoạn.
   - Điện thoại đã cài PWA từ bản trước triển khai: mở QR lần đầu, ghi điều gì hiện ra và làm theo hướng dẫn ở bước 1
     cho tới khi quét được.
   - Đăng xuất trên máy tính khi đang ghép đôi: điện thoại báo phiên kết thúc trong khoảng 15 giây.
   - iPhone: tiếng bíp và nháy màn hình xuất hiện sau lần chạm “Bật camera”.
7. **Parity và a11y.** Chạy lại parity và a11y cho POS, Kiểm kê và hai hộp thoại Nhập hàng ở 1440 và 390. Thay đổi chỉ
   được phép ở vùng card ghép đôi, và baseline mới phải được người xem duyệt trước khi chạy `--update-snapshots`.
8. **Báo cáo.** Ghi kết quả, các lần đo và số cặp mã trùng biến thể vào báo cáo, rồi thêm thiết bị đã qua vào bảng của
   ma trận thiết bị.

## Validation

- Liên kết trong tài liệu đã sửa trỏ đúng file và mục.
- Header qua edge khớp runbook: `/scan` có camera và `wasm-unsafe-eval`, mọi đường dẫn khác giữ header cũ. `.wasm` trả
  đúng kiểu và được nén.
- Báo cáo chạy thử có kết quả cho từng tiêu chí hoàn thành của brainstorm và từng mục ở bước 6 trên cả hai điện thoại.

## Rủi ro

- Lỗi chỉ hiện trên điện thoại thật (lấy nét, Safari, tunnel). Phát hiện ở đây thì sửa ở phase gốc và chạy lại bước 6.
- Lần mở đầu trên điện thoại giữ SW cũ có thể cần người dùng tải lại. Bước 6 xác nhận đường đi và hướng dẫn người dùng
  ghi cách xử lý.
- Hành vi của Traefik trong lúc `api` khởi động lại (404 không phải JSON) chưa được đo trên stack thật; client đã coi
  nó là tạm thời, bước 6 xác nhận.
- `make prod-migrate` lỗi giữa chừng. Bản sao lưu của lệnh này là đường quay lại.

## Bảo mật

Không ghi mã ghép đôi, mã xác minh, cookie hay mã phiên vào báo cáo hoặc ảnh chụp. Dùng tenant thử, không dùng dữ liệu
cửa hàng thật. Truy vấn trùng biến thể chỉ ghi số lượng và mã tenant, không ghi mã vạch.

## Rollback

Hoàn tác commit tính năng và `make prod-up`. Bảng `scanner_*` có thể giữ lại; nếu cần gỡ thì khôi phục từ bản sao lưu
của `make prod-migrate`.

## Việc cần làm

- [x] Hướng dẫn người dùng bốn trang và mục lục (Cho phép, cộng theo hệ số, PWA cũ, câu timeout)
- [x] Ma trận thiết bị và câu `:85-86`
- [x] Runbook (header `/scan`, ưu tiên router, lệnh compose, một replica, khởi động lại, đăng xuất)
- [ ] Kiểm tra trước triển khai và truy vấn trùng biến thể
- [ ] `make prod-migrate`, `make prod-up`, kiểm tra header qua edge
- [ ] Chạy thử trên Android và iPhone theo bước 6
- [ ] Parity và a11y, báo cáo chạy thử
