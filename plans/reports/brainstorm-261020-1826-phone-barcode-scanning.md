# Brainstorm: quét mã vạch bằng điện thoại ghép đôi qua QR

Ngày: 2026-10-20 · Trạng thái: đã chốt toàn bộ (sửa lúc 19:02), chờ lập kế hoạch

## Quyết định của người dùng

- Cửa hàng chỉ có **một máy tính** mở web app. Không có máy POS riêng và không có máy quét HID. Điện thoại là thiết bị
  quét duy nhất.
- Bỏ chế độ A (camera trên chính máy đang mở màn hình). Chỉ làm chế độ B: màn hình trên máy tính hiện mã QR, điện thoại
  quét QR để ghép đôi rồi dùng camera điện thoại quét mã vạch trên hàng.
- Màn có QR ghép đôi: **POS & giỏ hàng**, **Kiểm kê kho** và **Nhập hàng** (Tạo đơn nhập, Nhận hàng).
- Điện thoại **không cần đăng nhập**. QR chứa mã ghép đôi ngắn hạn, và mã này chỉ cho phép gửi mã quét vào đúng phiên đó.
- Giữ nguyên quyết định cũ ở Nhập hàng: gặp mã vạch chưa có trong danh mục thì mở dòng **Hàng mới** với mã vạch đã điền
  sẵn và con trỏ ở ô Tên. Khi lưu đơn, mã vạch được ghi cùng hàng mới trong cùng một giao dịch.

## Bằng chứng hiện trạng

- POS không tìm được hàng theo mã vạch: ô quét chỉ khớp `sku_code`, `name`, `variant_label`
  (`apps/web/src/features/pos/pages/pos-page.tsx:69`). `PosTile` (bootstrap và gói offline) không có mã vạch.
  Mã vạch nằm ở `sku_barcodes` (unique theo tenant, gắn `unit_id`).
- Kiểm kê đã khớp `sku.barcode` (`apps/web/src/features/inventory/components/count-drawer.tsx:78`), nhưng chỉ một mã.
- Nhập hàng không có ô SKU riêng. Mỗi dòng dùng ô chọn hàng “Mã, tên hoặc mã vạch…” (`buy-preset.tsx:199`, dùng
  chung cho Tạo đơn nhập và Nhận hàng), gọi `GET /skus?q=`. Server khớp chính xác `code` hoặc `sku_barcodes.code`
  và xếp lên đầu (`apps/api/internal/catalog/search.go:291-297`), nên đã tìm được hàng có sẵn theo mã vạch.
- Chỗ hở ở Nhập hàng: mã lạ bị đề xuất “+ Tạo hàng mới” với chính chuỗi mã làm **tên**. `NewProductRequest`
  không có `barcode` (`apps/api/api/openapi.yaml:13085`), nên hàng mới tạo từ đơn nhập không bao giờ quét lại được.
  `SkuListItem` chỉ có một `barcode` và không cho biết mã đó gắn đơn vị nào.
- Xác thực dùng cookie phiên và CSRF theo host (`apps/api/internal/platform/middleware/session.go`, `csrf.go`). Tenant
  lấy từ host `<slug>.<domain>` (`host_context.go`, `tenant.go`). Yêu cầu ẩn danh trên host tenant lấy được token CSRF
  của host đó và ghi được vào route công khai (`apps/api/internal/app/app_test.go:210`), nên trang quét trên điện thoại
  không cần phiên đăng nhập. Đã có middleware giới hạn tần suất (`ratelimit.go`).
- Production chặn camera: `permissionsPolicy: "camera=(), ..."` ở `infra/docker-compose.homelab.yml:32`.
- API chưa có kênh realtime (không SSE/websocket/LISTEN-NOTIFY). Prod chạy một container `api` và một `worker`,
  đi qua Cloudflare Tunnel + Traefik.
- iOS Safari không có `BarcodeDetector`; Chrome Android có. Camera chỉ mở được trong ngữ cảnh HTTPS.
- Dev chạy ở `http://<slug>.localhost:5173` (`apps/web/vite.config.ts:54`). Điện thoại không truy cập được địa chỉ này
  và không mở được camera qua HTTP, nên chạy thử trên điện thoại thật cần một host HTTPS (staging hoặc tunnel).

## Hợp đồng

**Mục tiêu**
- Mở POS, Kiểm kê hoặc Nhập hàng trên máy tính khi chưa có điện thoại kết nối: màn hình hiện sẵn khung QR “Quét bằng
  điện thoại”. Khi đã kết nối, khung thu lại thành nhãn trạng thái “Điện thoại đã kết nối · Ngắt”.
- Điện thoại (Chrome Android hoặc Safari iOS) quét QR bằng camera mặc định thì mở trang quét trên host của cửa hàng,
  không cần đăng nhập. Trang này mở camera, đọc EAN-13/EAN-8/UPC/Code128/QR và quét liên tục.
- Mỗi mã quét đi qua đúng luồng của Enter trong ô quét của màn hình đang mở trên máy tính, trong khoảng một giây. Mã vạch
  của đơn vị nào thì dòng lấy đơn vị đó.
- Một lần ghép đôi gắn với tab trình duyệt trên máy tính và giữ nguyên khi chuyển qua lại giữa ba màn. Mã quét luôn
  vào màn đang mở. Nếu màn đang mở không nhận mã quét, điện thoại báo “Màn hình trên máy tính không nhận mã quét”.
- Sau mỗi lần quét, điện thoại rung và hiện kết quả: tên hàng và đơn vị nếu tìm thấy, “Không có trong danh mục” nếu
  không. Điện thoại không thấy giá, tồn kho hay dữ liệu khác của cửa hàng.
- Nhập hàng: mã đã có thì thêm dòng hàng đó, hoặc cộng 1 vào dòng đang có cùng hàng và cùng đơn vị. Mã lạ thì mở dòng
  Hàng mới có ô **Mã vạch** điền sẵn. Lưu đơn sẽ tạo hàng kèm mã vạch, và mã trùng thì trả `barcode_taken` như ở Danh mục.
- Cả hai bên đều thấy trạng thái kết nối và đều ngắt được. Phiên hết hạn hoặc bị thu hồi thì điện thoại bị từ chối và
  được hướng dẫn quét lại QR.

**Ràng buộc**
- PWA React 19 + Vite. Trang quét chạy được trên Chrome Android và Safari iOS (2 bản gần nhất). Dùng `BarcodeDetector`
  khi có, nếu không thì dùng thư viện WASM dự phòng. Thư viện này chỉ tải ở route trang quét, không vào bundle chính.
- Đổi Permissions-Policy thành `camera=(self)`.
- Chỉ chạy khi online. Máy tính mất mạng thì khung QR nói rõ “Quét bằng điện thoại cần có mạng”. Bán hàng offline bằng
  cách gõ mã vẫn chạy như hiện nay.
- Luồng Enter trong ô quét giữ nguyên hành vi (mỗi mã một dòng, không gửi mã gõ dở). Mã từ điện thoại đi vào chính luồng này.
- Mã ghép đôi: ngẫu nhiên đủ dài, chỉ lưu bản băm, có hạn ngắn để nhận (QR tự làm mới khi hết hạn), chỉ một điện thoại
  nhận được. Sau khi nhận, điện thoại giữ một bí mật riêng của phiên; QR cũ hết hiệu lực. Thu hồi được từ cả hai bên.
- Phiên gắn tenant theo host và RLS. Mã của tenant này đưa lên host tenant khác thì bị từ chối. Không gửi mã quét sang
  tenant khác hay tab khác. Route nhận và gửi mã có giới hạn tần suất.
- Mỗi lần gửi mã có khóa idempotency, để gửi lại khi mạng chập chờn không tạo dòng trùng.
- Luồng SSE tới máy tính qua Cloudflare Tunnel cần heartbeat.

**Không làm lần này**
- Quét bằng webcam của máy tính (chế độ A cũ).
- Đăng nhập trên điện thoại, hoặc xem dữ liệu cửa hàng từ trang quét.
- Quét ở Hóa đơn, Báo giá, Trả hàng NCC, Trả/Đổi hàng.
- Gắn mã lạ vào một hàng có sẵn ngay từ đơn nhập (vẫn làm ở Danh mục như hiện nay).
- In tem mã vạch. Tạo hàng mới từ mã lạ ở POS hoặc Kiểm kê. QR thanh toán.
- Ghép đôi khi offline. Một điện thoại phục vụ nhiều máy tính, hoặc nhiều điện thoại cho một máy tính.
- Ứng dụng native.

**Tiêu chí hoàn thành**
- Mở POS khi chưa kết nối thì thấy QR. Điện thoại quét QR bằng camera mặc định, mở trang quét không cần đăng nhập, và cả
  hai bên báo đã kết nối.
- Quét một mã vạch đã khai báo: giỏ POS có thêm đúng một dòng, đúng SKU và đúng đơn vị của mã, trong khoảng một giây.
  Điện thoại hiện tên hàng và đơn vị. Ở Kiểm kê, số đếm của dòng tương ứng tăng thêm 1.
- Mã chưa khai báo ở POS và Kiểm kê: máy tính báo “Không tìm thấy mã …”, điện thoại báo “Không có trong danh mục”,
  không thêm gì.
- Nhập hàng: quét mã có sẵn thì ra đúng hàng và đúng đơn vị, quét lại thì cộng dồn. Quét mã lạ, nhập tên rồi lưu đơn
  thì lần quét sau ở POS ra đúng hàng mới đó, theo đơn vị cơ sở.
- Ở Danh mục, đổi đơn vị một mã vạch từ hộp sang thùng, thì lần quét sau ở POS thêm 1 thùng với giá thùng. Nếu đổi
  đơn vị lỗi thì mã vẫn còn nguyên ở đơn vị cũ.
- Chuyển từ POS sang Kiểm kê mà không phải ghép đôi lại; mã quét sau đó vào Kiểm kê.
- Ngắt ở một bên thì bên kia biết trong vài giây. Mã đã hết hạn, đã bị thu hồi hoặc đã có điện thoại khác nhận thì bị
  từ chối. Mã của tenant A dùng trên host tenant B bị từ chối.
- Gửi lại cùng một lần quét (cùng khóa idempotency) không tạo thêm dòng.
- Có test đơn vị cho khớp mã vạch (POS, Kiểm kê nhiều mã, Nhập hàng thêm/cộng dồn/hàng mới) và test API cho vòng đời
  phiên ghép đôi, cách ly tenant, giới hạn tần suất, và kiểm tra phản hồi cho điện thoại không lộ giá hay tồn kho.
  Chạy tay trên điện thoại Android và iOS thật theo `docs/operations/device-browser-matrix.md`.

## Đánh đổi

- **Mã ghép đôi trong QR thay cho đăng nhập.** Dựa trên giả định người cầm điện thoại trong cửa hàng được phép thêm hàng
  vào giỏ (không được thanh toán hay xem dữ liệu). Hỏng trước tiên khi ảnh QR bị chụp lại và dùng trước người thu ngân.
  Giảm nhẹ bằng hạn nhận ngắn, chỉ một điện thoại nhận được, máy tính hiện rõ điện thoại nào đang kết nối và ngắt được ngay.
- **Hub SSE trong bộ nhớ, trạng thái phiên trong Postgres (đề xuất).** Dựa trên việc prod chỉ có một container `api`.
  Hỏng trước tiên khi chạy hơn một bản `api`, vì mã quét đến một bản còn máy tính nghe ở bản khác. Khởi động lại `api`
  chỉ làm các bên kết nối lại, phiên vẫn còn trong bảng. Postgres LISTEN/NOTIFY chịu được nhiều bản nhưng tốn một kết
  nối riêng và thêm độ phức tạp chưa cần. Đặt hub sau một interface nhỏ để đổi sau này.
- **Bỏ chế độ A.** Máy tính mất mạng thì không quét được bằng điện thoại, chỉ còn gõ mã. Người dùng chấp nhận vì cửa
  hàng không có thiết bị quét nào khác.
- Phương án ứng dụng ngoài giả bàn phím Bluetooth vẫn bị loại: iOS không hỗ trợ, và bộ gõ Telex có thể làm sai mã.

Phương án tốt hơn: không có. Hướng chọn đúng như yêu cầu, vì điện thoại là thiết bị có camera duy nhất trong cửa hàng
và hạ tầng đã có sẵn CSRF ẩn danh theo host cho trang quét không cần đăng nhập.

## Thứ tự đề xuất

1. Tra mã vạch chính xác, trả về SKU và đơn vị của mã (endpoint tra cứu riêng, hoặc thêm `barcodes[{code, unit_id}]`
   vào danh sách SKU; kế hoạch chọn). Thêm mã vạch theo đơn vị vào `PosTile` (bootstrap và gói offline); khớp chính
   xác mã vạch trước rồi mới tìm theo từ. Kiểm kê khớp mọi mã vạch của SKU.
2. API ghép đôi: bảng phiên ghép đôi có RLS (cần migration), tạo phiên và mã QR, điện thoại nhận mã, gửi mã quét
   (idempotent, trả tên hàng và đơn vị), luồng SSE cho máy tính kèm heartbeat, ngắt từ hai bên, giới hạn tần suất.
   Đổi Permissions-Policy.
3. Web phía máy tính: khung QR và nhãn trạng thái dùng chung cho ba màn, giữ phiên theo tab, chuyển mã vào luồng Enter
   của màn đang mở.
4. Trang quét trên điện thoại: route công khai trên host tenant, thành phần camera (`BarcodeDetector`, WASM tải lười),
   phản hồi rung và kết quả, gửi lại khi mạng chập chờn.
5. Nhập hàng: luồng quét ở `BuyEditor` (thêm hoặc cộng dồn, mã lạ thì mở Hàng mới); thêm ô Mã vạch vào dòng Hàng mới;
   thêm `barcode` vào `NewProductRequest` và ghi vào `sku_barcodes` (đơn vị cơ sở) trong cùng giao dịch.
6. Danh mục: khi sửa hàng, liệt kê mã vạch của SKU kèm đơn vị và cho đổi đơn vị của từng mã trong một giao dịch.
7. Cập nhật tài liệu: `docs/user-guide/ban-hang-pos.md`, hướng dẫn kiểm kê, hướng dẫn nhập hàng, hướng dẫn Danh mục
   (đổi đơn vị mã vạch), và ma trận thiết bị (thêm dòng “Điện thoại ghép đôi làm máy quét”).

## Quyết định bổ sung (19:02)

- **Mã vạch thắng `sku_code`.** Khi một mã quét vừa là mã vạch của SKU X vừa là `sku_code` của SKU Y, mọi màn (POS,
  Kiểm kê, Nhập hàng, endpoint tra cứu) chọn X theo mã vạch, kèm đơn vị của mã đó.
- **Thời hạn phiên ghép đôi.** QR có hạn nhận 2 phút và tự làm mới khi hết hạn. Phiên kết thúc khi đóng tab trên máy
  tính: luồng SSE của tab đứt quá 60 giây thì server thu hồi phiên. Tải lại trang (F5) hoặc mạng chập chờn dưới 60 giây
  không làm mất phiên, vì tab giữ mã phiên trong `sessionStorage`. Giới hạn tối đa 12 giờ kể từ lúc ghép đôi.
- **Chạy thử trên điện thoại thật.** Triển khai lên stack homelab sau Cloudflare Tunnel (`*.sidecup.app`), rồi người
  phụ trách kiểm thử chạy trên một tenant thử. Không dùng quick tunnel tới máy dev, vì tenant lấy từ subdomain
  `<slug>.<domain>` mà quick tunnel chỉ cho một hostname cố định.

- **Đơn vị của mã vạch hàng mới từ đơn nhập.** Mã vạch luôn gắn **đơn vị cơ sở**, giống “Thêm hàng hóa” ở Danh mục
  (`apps/api/internal/catalog/skus.go:190`). Dòng Hàng mới không có ô chọn đơn vị cho mã vạch. Dòng tạo từ một lần
  quét bắt đầu ở chế độ nhập theo đơn vị cơ sở, nên mỗi lần quét lại cộng 1. Nếu nhân viên đổi dòng sang nhập theo
  thùng thì quét lại không tự cộng nữa, và điện thoại báo “Dòng này nhập theo thùng, sửa số lượng trên máy tính”.
- **Đổi đơn vị mã vạch sau đó ở Danh mục (thêm vào phạm vi).** Khi sửa hàng, Danh mục liệt kê mọi mã vạch của SKU kèm
  đơn vị, và cho đổi đơn vị của từng mã (ví dụ chuyển mã in trên thùng sang thùng). API đã có `listBarcodes`,
  `addBarcode` (nhận `unit_id`) và `deleteBarcode`, nhưng chưa có thao tác đổi đơn vị. Đổi bằng cách xóa rồi thêm qua
  hai lệnh gọi có thể làm mất mã nếu lệnh thứ hai lỗi, nên kế hoạch phải chọn một thao tác đổi đơn vị trong một giao
  dịch.
- **Lên prod.** Stack homelab là prod duy nhất. Được phép đưa tính năng lên prod cho mọi tenant cùng lúc (kể cả
  `camera=(self)` và các route công khai mới) rồi kiểm thử trên tenant thử. Không thêm cờ ẩn tính năng.

## Câu hỏi còn mở

Không còn.
