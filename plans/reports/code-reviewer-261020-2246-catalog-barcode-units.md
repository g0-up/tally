# Review: đổi đơn vị của mã vạch (phase 6)

- Ngày: 2026-10-21 (Asia/Saigon). Chỉ review, không sửa mã nguồn.
- Plan: `plans/261020-1227-phone-barcode-scanning/phase-06-catalog-barcode-units.md`
- Báo cáo triển khai: `plans/reports/fullstack-developer-261020-phase-06-catalog-barcode-units.md`

## Phạm vi

- API: `apps/api/internal/catalog/skus.go` (`UpdateBarcodeUnit`, dòng 871-913), `dto.go` (`barcodeUnitRequest`),
  `http.go` (route dòng 93, handler `updateBarcodeUnit`), `apps/api/api/openapi.yaml` (`updateBarcodeUnit`,
  `BarcodeUnitUpdate`), test `catalog/code_lookup_integration_test.go:161-267`,
  `app/catalog_partner_integration_test.go:266`, `app/sku_lookup_integration_test.go:58-93`.
- Web: `features/catalog/containers/barcode-units-drawer.tsx` và test, `pages/products-page.tsx`, `api/queries.ts`,
  `api/mutations.ts`, `schemas.ts`, `index.ts`, `test/msw/barcodes.ts`, `shared/api/schema.d.ts`.
- Bỏ qua `features/procurement` và `internal/scanner` theo yêu cầu.

## Kiểm tra đã chạy

- `vitest run src/features/catalog`: 3 file, 11 test, đạt.
- `eslint src/features/catalog src/test/msw/barcodes.ts`: sạch. `tsc --noEmit`: không có lỗi ở các file catalog.
- `go vet ./internal/catalog/...` và `go vet -tags integration ./internal/catalog/ ./internal/app/`: sạch.
- `go test ./internal/catalog/...`: đạt.
- `go test -tags integration -run 'TestUpdateBarcodeUnit|TestLookupCode|TestBarcodes' ./internal/catalog/`: đạt.
- `go test -tags integration -run 'TestCatalogAndPartnersOverHTTP|TestScanLookupAndPosTileBarcodes|TestTenantIsolationAcrossEveryRoute' ./internal/app/`: đạt.
  Container testcontainers (postgres, ryuk) đã tự dừng sau khi chạy; không động tới container prod.
- Sinh lại `openapi-typescript` vào scratchpad rồi so với `src/shared/api/schema.d.ts`: trùng khớp từng byte.

## Đánh giá chung

Phần API đúng và chắc. `UpdateBarcodeUnit` khóa SKU bằng `FOR UPDATE` giống `AddBarcode` và `DeleteBarcode`, nên
các thao tác mã vạch của cùng SKU chạy tuần tự. Mã trong path được đọc theo `id + sku_id` trước khi kiểm đơn vị trong
body, nên mã của SKU khác hay của cửa hàng khác đều trả 404. Ràng buộc FK
`fk_sku_barcodes__sku_id_unit_id (tenant_id, sku_id, unit_id)` ở DB bảo đảm không thể gán đơn vị ngoài SKU kể cả khi
service có lỗi. RLS và `s.write` giữ tenant. Audit `sku.barcode_update` ghi `before/after` cùng kiểu `Barcode` với
`sku.barcode_add/remove`, và không ghi khi đơn vị không đổi. Route nằm cùng nhóm `r.Tenant` với POST và DELETE mã
vạch. Catalog không có phân quyền theo vai trò ở từng route, nên quyền của route mới đúng bằng hai route kia (phiên,
host guard, CSRF, entitlement, support chỉ đọc). OpenAPI và `schema.d.ts` khớp nhau.

Rủi ro chính nằm ở phía web và ở lời hứa của plan về POS: mã thuộc ô hàng POS không đi qua `/sku-lookup`, nên POS
online vẫn dùng đơn vị cũ cho tới khi bootstrap tải lại.

## Critical

Không có.

## High

### H1. POS online vẫn bán theo đơn vị cũ cho mã của 30 ô hàng, trái với mục tiêu của plan và hướng dẫn sử dụng

- Vị trí: `apps/web/src/features/pos/lib/scan-match.ts:75` (khớp cục bộ theo `tile.barcodes` trước khi gọi
  `/sku-lookup`), `apps/web/src/features/pos/api/queries.ts:21-27` (bootstrap `staleTime: 15_000`, không có
  `refetchInterval`), `apps/api/internal/sales/pos_bootstrap.go:16` (`maxTiles = 30`, xếp theo số bán 30 ngày).
  Lời hứa sai nằm ở `barcode-units-drawer.tsx:26-27`, ở mục "Mục tiêu" của plan, và ở
  `docs/user-guide/danh-muc-bang-gia.md` ("Từ lần quét sau, POS (khi có mạng) ... tính mã đó theo đơn vị mới").
- Kịch bản lỗi: quầy POS đang mở và giữ focus. Quản lý sửa mã `893...` của một hàng bán chạy từ `thùng` về `hộp` (sửa
  mã đăng ký nhầm). Thu ngân quét mã đó. `posScan` khớp mã trong `tile.barcodes` của bootstrap cũ và thêm dòng theo
  `thùng`, nên khách bị tính giá thùng cho một hộp. Hướng ngược lại (hộp sang thùng) thì một thùng bị bán với giá hộp.
  Lỗi kéo dài tới khi bootstrap tải lại, tức sau lần ghi sổ kế tiếp (`posKeys.all` bị invalidate), khi cửa sổ lấy lại
  focus, hoặc khi màn POS mount lại. Chính 30 hàng bán chạy nhất là những hàng được quét nhiều nhất. Gói offline cũng
  giữ ánh xạ cũ: mã cũ là đơn vị cơ sở vẫn được bán offline theo đơn vị cơ sở. Câu trong mục Rủi ro của plan ("mã đã
  chuyển sang thùng thì bị từ chối khi offline") chỉ đúng sau khi gói được tải lại.
- Đây không phải lỗi do phase 6 tạo ra, vì cách khớp ô có từ phase 1. Nhưng phase 6 tuyên bố tiêu chí "lần quét sau ở
  POS online lấy đơn vị mới", và tiêu chí đó không đạt với các mã thuộc ô hàng.
- Đề xuất (cần người dùng chọn):
  1. Khi online, gọi `/sku-lookup` trước cho mã quét bằng máy (source khác `keyboard`) và chỉ dùng `tile.barcodes`
     khi offline hoặc khi tra cứu lỗi mạng. Cách này tốn một round trip mỗi lần quét.
  2. Giữ thứ tự hiện tại nhưng thêm `refetchInterval` (ví dụ 60 giây) cho `usePosBootstrap`, và invalidate
     `["pos", "bootstrap"]` trong `onSuccess` của `useUpdateBarcodeUnit` cho trường hợp cùng một tab. Cách này rút
     ngắn cửa sổ lỗi nhưng không đóng hẳn.
  3. Nếu chấp nhận độ trễ, sửa docstring của drawer, câu phụ của drawer, hướng dẫn sử dụng và mục Rủi ro của plan cho
     đúng: "POS cập nhật mã của ô hàng sau khi tải lại".

## Medium

### M1. Ô chọn lưu ngay mỗi lần đổi giá trị, nên phím mũi tên trên Windows và Linux lưu cả đơn vị trung gian

- Vị trí: `barcode-units-drawer.tsx:101` (`onChange={(e) => change(e.target.value)}`), cùng với `unitChoices`
  (`:55`) sắp các lựa chọn theo hệ số.
- Kịch bản lỗi: trên Chrome và Firefox ở Windows hoặc Linux (máy quầy), người dùng bàn phím focus vào ô chọn đang đóng
  rồi bấm mũi tên xuống hai lần để đi từ `hộp` tới `thùng`. Lần bấm đầu phát `change` với `lốc`, nên PATCH `lốc` được
  gửi và ô bị `aria-disabled`. Lần bấm thứ hai bị bỏ qua. Kết quả là mã được lưu ở `lốc`, toast báo "nay là mã của lốc",
  audit có thêm một bản ghi thừa, và trong khoảng đó một lần quét ở quầy khác sẽ ra `lốc`. Người dùng phải đợi rồi bấm
  tiếp. Test hiện tại dùng `user.selectOptions`, nên không thấy được hành vi này.
- Đề xuất: lưu bằng thao tác xác nhận rõ ràng. Có thể giữ giá trị nháp trong state và thêm nút "Lưu" trên từng dòng.
  Hoặc dùng combobox hoặc listbox chung của dự án, chỉ commit khi bấm Enter hoặc click. Hoặc commit khi `blur` hay
  Enter thay vì ở mỗi `change`. Nên thêm một test bàn phím (`user.keyboard("{ArrowDown}{ArrowDown}")`) kiểm rằng chỉ
  có một PATCH với đơn vị cuối.

### M2. Drawer dùng bản chụp `SkuListItem` từ state của trang, nên ô chọn có thể hiện sai đơn vị

- Vị trí: `products-page.tsx:117` (`sku={barcodesOf}` là object giữ trong `useState`),
  `barcode-units-drawer.tsx:64-67`.
- Kịch bản lỗi: drawer đang mở thì một tab khác thêm đơn vị `két` cho SKU và chuyển một mã sang `két`.
  `invalidateQueries(skuKeys.all)` hoặc refetch khi lấy lại focus tải lại danh sách mã, nên `barcode.unit_id` là `két`.
  Nhưng `sku.units` vẫn là bản chụp cũ, nên `<select value="két">` không có option tương ứng. Trình duyệt hiện option
  đầu tiên (`hộp`), tức là hiện sai đơn vị. Chọn `hộp` lúc đó cũng không phát `change`, vì DOM đã đang ở `hộp`. Câu lỗi
  "Tải lại để xem bản mới nhất" cũng không giúp được, vì `sku` của drawer không bao giờ được làm mới. Toast thì hiện
  "nay là mã của ." khi không tìm thấy tên đơn vị (`:76`).
- Đề xuất: chỉ giữ `skuId` trong state của trang, rồi lấy SKU từ `list.items` (hoặc `useSkusByIds([id])`) ở mỗi lần
  render. Nếu `barcode.unit_id` không có trong `sku.units`, render thêm một option dự phòng ("Đơn vị khác, tải lại") để
  ô chọn không hiện sai, và bỏ câu toast khi không có tên.

### M3. API cho chuyển mã sang đơn vị đã ngừng, và mã đó không còn tra được

- Vị trí: `skus.go:894` chỉ kiểm đơn vị có thuộc SKU hay không. `code_lookup.go:97` lọc `AND su.active`.
- Kịch bản lỗi: một client gọi API (không phải web, vì web đã lọc ở `unitChoices`) chuyển mã sang đơn vị đã ngừng. Yêu
  cầu trả 200. Sau đó `/sku-lookup` trả `code_not_found` cho mã này, trong khi mã vẫn bị giữ chỗ trong bảng nên không
  thêm lại được. POS, Kiểm kê và Nhập hàng "mất" mã mà không có cảnh báo. `AddBarcode` cũng có cùng hành vi, nên đây là
  quyết định nhất quán nhưng chưa được ghi lại.
- Đề xuất: từ chối đơn vị đích không hoạt động bằng 422 `unit_id: unit_inactive`, nhưng vẫn cho phép khi đơn vị đích
  trùng đơn vị hiện tại (trường hợp không đổi). Nếu giữ hành vi hiện tại, ghi nó vào phần mô tả của
  `updateBarcodeUnit` trong OpenAPI. Đây là quyết định sản phẩm (xem Câu hỏi).

## Low

### L1. Chú thích "404 dù body chứa gì" không đúng với body thiếu `unit_id`

- Vị trí: `skus.go:874-876` và chú thích ở `:882-884`.
- Kịch bản: gửi PATCH với mã của cửa hàng khác và body `{}` thì nhận 422 `unit_id: required` trước khi tra mã. Câu trả
  lời này không phụ thuộc vào việc mã có tồn tại hay không, nên không thành kênh dò tồn tại. Tuy vậy, chú thích đang
  khẳng định một điều sai, và `TestTenantIsolationAcrossEveryRoute` chỉ đúng vì nó gửi body hợp lệ.
- Đề xuất: sửa chú thích thành "khi body hợp lệ". Hoặc chuyển kiểm `uuid.Nil` vào sau bước đọc mã nếu muốn 404 luôn
  thắng.

### L2. Mục PATCH trong test đối tác dùng đơn vị của cửa hàng bị nhắm, không dùng `own`

- Vị trí: `catalog_partner_integration_test.go:266` dùng `thung := st.units["thùng"]` (đơn vị của A), trong khi chú
  thích ở `:224-226` nói body nên dùng id hợp lệ của cửa hàng gọi (`own`) "để chỉ path quyết định câu trả lời". Plan
  cũng ghi `thung`, nên đây là lệch quy ước chứ không phải lỗi. Với body là đơn vị của B, test bắt được lỗi thứ tự
  kiểm tra rõ hơn: nếu service kiểm đơn vị trước, B gửi đơn vị của chính mình vẫn có thể ra 422 thay vì 404.
- Đề xuất: dùng `own.units["thùng"]`. Bước "Nothing of A changed" cũng nên kiểm `unit_id` của `storeA.barcode` vẫn
  là `hộp`.

### L3. Subtest HTTP phụ thuộc thứ tự để khôi phục dữ liệu

- Vị trí: `sku_lookup_integration_test.go:92` chuyển mã về `cái` ở cuối, và subtest POS tiles phía sau dựa vào điều
  đó. Nếu một `send` ở giữa gọi `t.Fatalf`, bước khôi phục bị bỏ qua và subtest POS tiles hỏng theo, làm nhiễu chẩn
  đoán.
- Đề xuất: khôi phục bằng `t.Cleanup` ngay sau khi PATCH thành công, hoặc dùng một SKU riêng cho subtest này.

### L4. Kiểu trạng thái chờ viết inline, trùng với quy tắc CSS sẵn có

- Vị trí: `barcode-units-drawer.tsx:16-17,98`, so với `shared/styles/globals.css:332`
  (`.btn[aria-disabled="true"]`).
- Đề xuất: mở rộng selector thành `.btn[aria-disabled="true"], .in[aria-disabled="true"]` và bỏ `PENDING_STYLE`. Trước
  khi thêm selector, grep `globals.css` để tránh trùng định nghĩa.

### L5. Thiếu test cho các nhánh có logic

- `unitChoices` (ẩn đơn vị đã ngừng, nhưng vẫn giữ đơn vị hiện tại khi nó đã ngừng) chưa có test. Fixture chỉ có đơn vị
  đang hoạt động.
- Chưa test rằng sau khi lưu thì `skuKeys.all` bị invalidate (cột "Mã vạch" của bảng Hàng hóa đổi khi mã cơ sở bị
  chuyển đi).
- Chưa test trả focus về nút "Mã vạch" khi đóng drawer. Drawer được render có điều kiện, nên trả focus dựa vào
  `onCloseAutoFocus` của Radix khi unmount. Cách này đã dùng ở nơi khác nhưng nên có một khẳng định.
- MSW `barcodeServer` không kiểm đơn vị có thuộc SKU hay không. Chấp nhận được với test hiện tại, vì test 422 dùng
  override.

## Các mục đã kiểm và không có vấn đề

- Cách ly tenant và ngữ nghĩa 404: `lockSku` chạy dưới RLS. Mã được đọc theo `id AND sku_id`. Câu `UPDATE` cũng lọc
  `sku_id`. Đã có test service (mã của SKU khác, của tenant khác, và mã của A dưới SKU của B), test đối tác qua HTTP,
  và quét tenant isolation theo OpenAPI.
- Khóa và đua: các thao tác mã vạch của cùng SKU đều lấy `skus ... FOR UPDATE` trước, nên lần đọc `before` vẫn đúng tới
  lúc `UPDATE`. Xóa đơn vị SKU cũng khóa SKU. FK composite chặn mọi trạng thái mồ côi.
- Audit: chỉ ghi khi đơn vị đổi. `before/after` đầy đủ. Test kiểm đúng một bản ghi sau hai lần gọi.
- Hợp đồng: `200 Barcode`, `401/404/422`, cùng cách mô tả lỗi như `addBarcode`. `barcodeSchema` ở web bỏ
  `created_at`, phù hợp với cách zod bỏ khóa thừa. `setQueryData` giữ đúng hình `{data}` của cache thô (`select` chạy
  sau).
- Invalidate: `skuKeys.barcodes` nằm dưới tiền tố `skuKeys.all`, nên một lệnh invalidate làm mới cả danh sách SKU lẫn
  danh sách mã. `/sku-lookup` là lời gọi trực tiếp (`lookupSkuCode`), không có cache React Query, nên không cần
  invalidate. Ô POS là vấn đề riêng (H1).
- Trợ năng: ô chọn có `aria-label` riêng cho từng mã. Lỗi gắn qua `role=alert`, `aria-invalid` và
  `aria-describedby`. `aria-disabled` hợp lệ trên vai trò combobox, và focus được giữ nguyên trong lúc chờ. React
  khôi phục giá trị của ô chọn có kiểm soát khi người dùng chọn trong lúc chờ (đã có test). Nút "Mã vạch {tên}" có
  nhãn bắt đầu bằng chữ hiển thị (WCAG 2.5.3). Cột thao tác có tiêu đề rỗng nên nhận `aria-label="Thao tác"` từ
  `DataTable`.

## Đối chiếu việc cần làm trong plan

- `UpdateBarcodeUnit` (khóa, kiểm đơn vị, `Reindex`, audit): xong. Có thêm một lần đọc trước `UPDATE`; đúng, vì nằm
  dưới khóa SKU.
- Route, OpenAPI, `gen:api`: xong, đã đối chiếu `schema.d.ts`.
- Mục mới trong `catalog_partner_integration_test.go`: xong (xem L2).
- `useSkuBarcodes`, `useUpdateBarcodeUnit`, drawer, nút "Mã vạch": xong (xem M1, M2).
- Test tích hợp và Vitest theo mục Validation: xong và đạt. Tiêu chí ở mục Mục tiêu, "POS online lấy đơn vị mới ở lần
  quét sau", chưa đạt với mã thuộc ô hàng (H1).
- Chưa chạy e2e. Snapshot đối chiếu giao diện của trang Hàng hóa có thể đổi vì có thêm cột thao tác.

## Câu hỏi còn mở

1. Với H1: chọn tra `/sku-lookup` trước khi online, hay thêm refetch định kỳ cho bootstrap, hay chấp nhận độ trễ và
   sửa lại tài liệu?
2. Với M3: API có nên từ chối chuyển mã sang đơn vị đã ngừng không? Hành vi này hiện giống `AddBarcode`.
3. Với M1: chấp nhận thêm một bước xác nhận (nút Lưu hoặc Enter) thay cho lưu ngay khi đổi không?

Status: DONE_WITH_CONCERNS
Summary: API đổi đơn vị mã vạch đúng về khóa, audit, cách ly tenant, 404, quyền và hợp đồng OpenAPI, và mọi test liên quan đều đạt. Phía web và lời hứa của plan còn một vấn đề High (POS online dùng ánh xạ cũ của 30 ô hàng) và ba vấn đề Medium (ô chọn lưu theo từng phím mũi tên, drawer dùng bản chụp SKU cũ, API nhận đơn vị đã ngừng).
Concerns/Blockers: H1 và M3 cần người dùng quyết định về sản phẩm trước khi sửa.
