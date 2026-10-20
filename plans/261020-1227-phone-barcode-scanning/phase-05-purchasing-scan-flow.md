---
phase: 5
title: "Nhập hàng: luồng quét và mã vạch cho hàng mới"
status: pending
priority: P1
effort: 1d
dependencies: [1, 3]
---

# Phase 5: Nhập hàng

## Mục tiêu và bối cảnh

Ở Tạo đơn nhập và Nhận hàng, mã từ điện thoại đi vào bảng dòng của `BuyEditor`. Mã đã có trong danh mục thì thêm dòng
hàng đó theo đơn vị của mã, hoặc cộng 1 vào dòng đang có cùng hàng và cùng đơn vị. Mã lạ thì mở dòng **Hàng mới** có
ô **Mã vạch** điền sẵn và con trỏ ở ô Tên. Lưu đơn tạo hàng kèm mã vạch (gắn đơn vị cơ sở) trong cùng giao dịch; mã
trùng trả `barcode_taken` trên đúng ô Mã vạch của dòng.

Bằng chứng:
- `BuyEditor` dùng chung cho hai hộp thoại (`apps/web/src/features/procurement/components/buy-preset.tsx:46`;
  `containers/new-po-dialog.tsx:87`, `containers/new-receipt-dialog.tsx:127`). Nhận hàng theo đơn chạy ở chế độ
  `fixed`: không thêm dòng, số lượng theo đơn vị cơ sở (`buy-preset.tsx:184-303`).
- `BuyLine` không có trường mã vạch (`apps/web/src/features/procurement/buy-lines.ts:30-55`). `withSku` chọn đơn vị
  mua mặc định, `withUnit` đổi đơn vị và giá (`:114-123`). `newProductLine` mặc định nhập theo thùng (`:86-101`).
- `NewProductRequest` ở web (`buy-lines.ts:192-199`) và OpenAPI (`apps/api/api/openapi.yaml:13086-13101`) không có
  `barcode`. OpenAPI cấm `new_product` trên dòng nhận theo đơn.
- Go: `newProductRequest` đổi thẳng sang `NewProduct` bằng chuyển kiểu struct (`apps/api/internal/procurement/dto.go:79-97`),
  nên hai struct phải thêm trường giống nhau, cùng thứ tự. `procurementCatalog.CreateProduct` gọi
  `QuickCreateProductIn` (`apps/api/internal/app/procurement_ports.go:198-207`), mà `QuickCreateInput` **đã có**
  `Barcode *string` và gắn vào đơn vị cơ sở (`apps/api/internal/catalog/quick_create.go:35,57`).
- `errBarcodeTaken` là lỗi conflict không có trường (`apps/api/internal/catalog/errors.go:32`). `newProductError` đặt
  lỗi không có trường lên `name` (`apps/api/internal/procurement/new_products.go:94-110`).
- Phase 1 cho `insertBarcode` bắt cả trùng theo biến thể GTIN, nên va chạm biến thể cũng trả `barcode_taken`. Web chỉ
  khớp mã chính xác (`matchCode`) và tra server bằng `lookupSkuCode`.

<!-- Updated: Red Team 2026-10-20 - khớp mã chính xác ở web, ValidateBarcode, dòng lỗi errors.go -->
<!-- Updated: Validation Session 1 - giữ cả hai kết quả new_product và not_in_order -->

## Files

- Sửa `apps/api/internal/procurement/new_products.go` (`NewProduct.Barcode`, `newProductError`), `dto.go`
  (`newProductRequest.Barcode`), `apps/api/internal/app/procurement_ports.go` (truyền `Barcode`).
- Sửa `apps/api/api/openapi.yaml`: `NewProductRequest.barcode`.
- Sửa `apps/api/internal/procurement/new_products_test.go` và test tích hợp của đơn nhập hoặc phiếu nhận có hàng mới.
- Sửa `apps/web/src/features/procurement/buy-lines.ts`: `BuyLine.barcode`, `scannedNewLine`, `addScan`,
  `newProductOf`, kiểm tra trùng mã vạch giữa các dòng hàng mới.
- Sửa `apps/web/src/features/procurement/components/buy-preset.tsx`: ô Mã vạch ở dòng Hàng mới, `scanCode`,
  `useScanTarget`, `PhonePairingCard`.
- Sửa `apps/web/src/features/procurement/api/` (nếu cần) để gọi `GET /sku-lookup`, và nơi map lỗi
  `lines.i.new_product.barcode` lên dòng.
- Test: `buy-lines.test.ts`, `components/buy-preset.test.tsx`, `containers/new-receipt-dialog.test.tsx`,
  `containers/new-po-dialog.test.tsx` (tạo nếu chưa có), handler MSW cho `/sku-lookup`.

## Các bước

1. **API nhận mã vạch.** Thêm `Barcode *string` (json `barcode`) vào `newProductRequest` và `NewProduct` ở cùng vị trí,
   truyền qua `CreateProduct` vào `QuickCreateInput.Barcode`. Chuỗi rỗng sau khi cắt khoảng trắng coi như không có.
   Kiểm tra định dạng gọi `catalog.ValidateBarcode` (export ở phase 1) qua cổng của procurement, không chép lại luật.
2. **Lỗi trùng mã.** Trong `newProductError`, lỗi `barcode_taken` đặt vào `lines.i.new_product.barcode` thay vì `name`.
   Các lỗi không trường khác giữ nguyên chỗ cũ.
3. **OpenAPI.** `barcode: {type: [string, 'null'], maxLength: 64, description: …gắn vào đơn vị cơ sở…}`. Chạy
   `openapi_contract_test.go` và `corepack pnpm gen:api`.
4. **Dòng Hàng mới có mã vạch.** `BuyLine.barcode: string` (mặc định rỗng). `scannedNewLine(code, unitIdByName)` dựa
   trên `newProductLine` nhưng đặt `barcode = code` và `inBig = false`, để mỗi lần quét lại cộng 1 theo đơn vị cơ sở.
   `newProductOf` gửi `barcode` khi khác rỗng. Hai dòng hàng mới cùng mã vạch (so chính xác sau khi cắt khoảng trắng)
   bị chặn trước khi gửi với câu “Mã vạch “…” bị trùng ở hai dòng hàng mới.” Hai mã chỉ khác nhau theo biến thể GTIN
   (ví dụ UPC-A 12 số và EAN-13 có số 0 đầu) không bị web chặn; server trả `barcode_taken` trên dòng thứ hai khi lưu.
5. **Ô Mã vạch.** Dòng Hàng mới có thêm một ô “Mã vạch” (nhãn truy cập theo `rowLabel("Mã vạch", l, i)`), đặt cạnh khu
   đơn vị. Không có ô chọn đơn vị cho mã vạch. Lỗi `barcode_taken` từ server hiện dưới ô này.
6. **`scanCode(code): Promise<ScanOutcome>`** trong `BuyEditor`, theo thứ tự:
   1. **Dòng hàng mới đã có mã đó** (so chính xác sau khi cắt khoảng trắng): dòng nhập theo đơn vị cơ sở thì cộng 1 vào `qty`, kết quả
      `new_product`; dòng đã đổi sang nhập theo thùng thì không đổi gì, kết quả `needs_desktop_edit`.
   2. **Chế độ thường:** gọi `lookupSkuCode(code)` (`GET /sku-lookup`, phase 1). Thấy thì tìm dòng có cùng `sku.id` và cùng `unitId` với đơn vị
      của mã: có thì cộng 1, không thì thay dòng trống cuối bảng (nếu có) hoặc thêm dòng mới bằng
      `withUnit(withSku(line, sku), unit_id)`. Kết quả `added` kèm `skuId` và `unitId`.
   3. **Chế độ thường, 404 `code_not_found`:** thêm `scannedNewLine(code)`, đặt con trỏ vào ô Tên của dòng đó (cơ chế
      `created` sẵn có). Kết quả `new_product`.
   4. **Nhận hàng theo đơn (`fixed`):** khớp mã với các dòng của đơn bằng `lookupSkuCode`. Dòng cùng SKU thì cộng số
      lượng theo đơn vị cơ sở: cộng `factor` của đơn vị mã (1 với mã của đơn vị cơ sở). Kết quả `added`. Mã
      không thuộc SKU nào trong đơn (kể cả mã lạ) thì không thêm gì, toast “Mã “…” không có trong đơn nhập này.”, kết
      quả `not_in_order`.
   5. Lỗi mạng khi tra mã thì toast lỗi và kết quả `not_found`.
   Mỗi lần gọi đọc danh sách dòng mới nhất từ một ref mà chính `scanCode` cập nhật ngay khi tính xong, rồi mới gọi
   `onChange`, để hai mã đến liền nhau không đọc cùng một bản cũ.
7. **Kết quả mới cho điện thoại.** Thêm hai giá trị vào `ScanOutcome` (`apps/web/src/shared/lib/barcode.ts`), enum
   `ScanResult` của API và câu hiển thị trên điện thoại:
   - `new_product`: “Hàng mới, chưa có trong danh mục. Nhập thông tin trên máy tính.” (phản hồi kiểu thành công).
   - `not_in_order`: “Hàng này không có trong đơn nhập.” (phản hồi kiểu lỗi).
   Hai giá trị này không kèm `product_name` hay `unit_name`. API nhận chúng ở `POST …/scans/{scanId}/result`.
8. **Đích và card.** `BuyEditor` gọi `useScanTarget(scanCode)` và hiện `PhonePairingCard` trên bảng dòng. Hộp thoại
   đóng thì đích được gỡ. Không thêm ô quét gõ tay; ô chọn hàng “Mã, tên hoặc mã vạch…” giữ nguyên.

## Validation

- Go: `go test ./internal/procurement/...` với `NewProduct.Barcode` truyền tới catalog; `make test-integration` tạo đơn
  nhập có hàng mới kèm mã vạch, rồi `LookupCode` ra đúng SKU ở đơn vị cơ sở; mã trùng trả 409 với trường
  `lines.0.new_product.barcode`.
- Vitest `buy-lines.test.ts`: `scannedNewLine`, `newProductOf` có `barcode`, chặn trùng mã chính xác giữa hai dòng hàng
  mới.
- Test tích hợp: hai dòng hàng mới mang `012345678905` và `0012345678905` trả 409 với trường
  `lines.1.new_product.barcode`, và không SKU nào được tạo.
- Vitest `buy-preset.test.tsx` (MSW `/sku-lookup`): mã có sẵn thêm đúng SKU và đơn vị; quét lại cùng mã cộng dồn; mã
  thùng thêm dòng theo thùng; mã lạ mở Hàng mới có mã vạch và con trỏ ở Tên; quét lại mã lạ cộng 1; đổi sang thùng rồi
  quét lại trả `needs_desktop_edit`; hai mã liền nhau đều được tính.
- Vitest `new-receipt-dialog.test.tsx`: nhận theo đơn cộng đúng dòng, mã ngoài đơn trả `not_in_order`.
- `corepack pnpm lint|typecheck|test`, `go vet`, `golangci-lint` sạch.

## Rủi ro

- Thêm trường vào hai struct dùng chuyển kiểu: lệch thứ tự hay kiểu sẽ không biên dịch, nên lỗi lộ ngay khi build.
- Mã lạ mở dòng mới và dời con trỏ vào ô Tên, có thể cắt ngang việc người dùng đang gõ ở ô khác. Đây là hành vi hợp
  đồng yêu cầu.

## Bảo mật

Mã vạch đi qua cùng kiểm tra định dạng và RLS của catalog. Kết quả `new_product` và `not_in_order` không mang dữ liệu
cửa hàng nào tới điện thoại.

## Rollback

Hoàn tác các file trên. Trường `barcode` là trường tùy chọn mới nên client cũ vẫn gửi được đơn.

## Việc cần làm

- [x] `Barcode` trong `newProductRequest`, `NewProduct`, `CreateProduct`; `ValidateBarcode` qua cổng
- [x] `barcode_taken` đặt lên `lines.i.new_product.barcode`; OpenAPI và `gen:api`
- [x] `BuyLine.barcode`, `scannedNewLine`, chặn trùng chính xác, ô Mã vạch
- [x] `scanCode` của `BuyEditor` (cộng dồn, hàng mới, theo đơn cộng `factor`, ref dòng mới nhất)
- [x] `new_product`, `not_in_order` trong `ScanOutcome`, enum API, câu điện thoại
- [x] `useScanTarget(scanCode)` và `PhonePairingCard` trong `BuyEditor`
- [x] Test Go, tích hợp và Vitest ở mục Validation
