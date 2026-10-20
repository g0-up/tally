# Báo cáo triển khai: Phase 05 – Luồng quét mã khi nhập hàng

- Plan: `plans/261020-1227-phone-barcode-scanning/` (phase-05-purchasing-scan-flow.md)
- Trạng thái: hoàn thành, mọi bước kiểm tra đều xanh (xem mục Kiểm tra)

## Kết quả

Đơn nhập và Phiếu nhận giờ nhận mã quét từ điện thoại đã ghép. Mã đã có trong danh mục được cộng
vào dòng của đúng SKU và đơn vị. Mã lạ mở một dòng "Hàng mới" mang sẵn mã vạch đó, focus vào ô Tên.
Khi nhận hàng theo đơn, mã quét được cộng vào dòng đã đặt; mã ngoài đơn không thêm gì và trả về
`not_in_order`. Backend nhận `new_product.barcode`, gắn nó vào đơn vị cơ sở của SKU mới và trả lỗi
`barcode_taken` về đúng ô `lines.{i}.new_product.barcode`. Trên web, lỗi này hiện ngay dưới ô
"Mã vạch" của dòng đã gửi.

## File đã sửa

Backend:
- `apps/api/internal/procurement/new_products.go`:
  - thêm `NewProduct.Barcode` và hằng `codeBarcodeTaken`;
  - `createProduct` gọi `checkBarcode`, hàm này trim mã, coi mã rỗng là không có, và kiểm định dạng qua port;
  - `newProductError` map `barcode_taken` sang `…new_product.barcode`.
- `apps/api/internal/procurement/dto.go`: thêm `newProductRequest.Barcode`.
- `apps/api/internal/procurement/ports.go`: port `Catalog` có thêm `ValidateBarcode`. Điều này cần để dùng lại quy tắc của catalog mà không chép lại.
- `apps/api/internal/app/procurement_ports.go`: truyền `Barcode` vào `QuickCreateInput` và cài `ValidateBarcode` bằng `catalog.ValidateBarcode`.
- `apps/api/internal/procurement/new_products_test.go`: thêm case `barcode_taken` và `TestCreateProductPassesTheBarcode` (mã được trim, mã rỗng coi như không có, mã sai định dạng báo về đúng dòng và không tạo hàng).
- `apps/api/internal/app/purchasing_screens_integration_test.go`: thêm 2 subtest tích hợp (xem mục Kiểm tra).
- `apps/api/api/openapi.yaml`:
  - thêm `NewProductRequest.barcode` (`[string, 'null']`, maxLength 64);
  - mô tả lỗi 409/422 theo `lines.{i}.new_product.*`.

Web:
- `apps/web/src/shared/api/schema.d.ts`: sinh lại bằng `corepack pnpm gen:api`.
- `apps/web/src/features/procurement/buy-lines.ts`:
  - `BuyLine.barcode`, `scannedNewLine`, `isBuyable` (chuyển từ buy-preset sang);
  - `scanNewProduct` và `addScan`, hai hàm thuần chứa toàn bộ quy tắc quét;
  - `newProductOf` gửi `barcode` khi có;
  - chặn hai dòng hàng mới trùng mã (so khớp chính xác sau trim);
  - `Built.keys`, `SentLines` và `barcodeErrors`.
- `apps/web/src/features/procurement/api/adapters.ts`: thêm các câu `barcodeShared`, `scanNotInOrder`, `scanNotBuyable`, `scanLookupFailed`.
- `apps/web/src/features/procurement/components/buy-preset.tsx`:
  - ô "Mã vạch" trong vùng đơn vị của dòng Hàng mới;
  - lỗi hiện dưới ô, nối qua `aria-describedby` và `invalid`;
  - `scanCode`, `useScanTarget(scanCode)`, `<PhonePairingCard />` đặt trên bảng dòng.
- `apps/web/src/features/procurement/containers/new-po-dialog.tsx` và `new-receipt-dialog.tsx`: giữ các dòng của lần gửi cuối rồi truyền `barcodeErrors(save.error | post.error, sent)` vào BuyEditor.
- Test:
  - sửa `buy-lines.test.ts`, `components/buy-preset.test.tsx`, `containers/new-receipt-dialog.test.tsx`;
  - tạo mới `containers/new-po-dialog.test.tsx`;
  - handler MSW `/sku-lookup` nằm ngay trong từng file test, theo cách count-drawer đang làm.

## Quyết định và chỗ lệch so với phase

1. **Bước 7 đã xong từ các phase trước.**
   - `new_product` và `not_in_order` đã có trong `shared/lib/barcode.ts`, enum `ScanResult` của OpenAPI và `scanner/model.go`.
   - Câu hiển thị trên điện thoại nằm trong `scanner/phone/outcome-copy.ts`, thuộc agent khác.
   - Phase này không sửa gì ở bước 7.
2. **Kiểm định dạng mã vạch nằm trong `createProduct`** (trong transaction), không nằm trong các hàm
   validate thuần.
   - Lý do: luồn port vào `validateNewProduct`, `validateOrderLines` và `validateReceiptInput` sẽ phải sửa 3 chỗ gọi cùng test của chúng.
   - Kết quả vẫn là 422 trên `lines.{i}.new_product.barcode`, và không tạo hàng nào.
   - Quick-create của catalog kiểm lại lần nữa, việc này vô hại.
3. **`OrderRef.skuId`**: dòng nhận theo đơn giờ nhớ `sku_id` của dòng đặt. Nhờ vậy việc khớp mã quét
   vẫn đúng cả khi hàng của SKU đó chưa tải về danh mục (trường hợp `sku` là null).
4. **`Built.keys`**: mỗi dòng request ghi lại key của dòng giao diện đã sinh ra nó.
   - Các dòng rỗng bị bỏ qua, và một dòng nhận vượt tách thành 2 dòng, nên chỉ số `i` trong lỗi server không trùng với chỉ số dòng trên màn hình.
   - `barcodeErrors` dùng `keys` để đưa lỗi về đúng dòng.
   - Lỗi chỉ hiện khi dòng vẫn giữ đúng mã đã bị từ chối; người dùng sửa mã thì lỗi biến mất.
   - Đã cập nhật 3 test `toEqual({ ok: true, … })` cũ cho có thêm `keys`.
5. **Vị trí dòng quét**: cả bước 2 (mã đã có) lẫn bước 3 (mã lạ) đều thay dòng trống cuối bảng nếu
   có, nếu không thì thêm dòng mới.
   - Phase ghi bước 3 là "thêm". Tôi làm thống nhất cho cả hai bước để dialog mới mở, vốn có sẵn một dòng trống, không bị dư dòng rỗng.
6. **SKU kit/combo** khi quét: không thêm dòng, hiện toast "Mã “…” là …, không phải hàng nhập." và trả
   `not_found`. Cách này khớp với combobox, vốn chỉ cho chọn goods và material.
7. **Mã lạ khi nhận theo đơn**: tra danh mục trả 404 thì cũng coi là `not_in_order`, đúng như phase yêu
   cầu.
8. Lỗi `barcode_taken` hiện câu tiếng Việt của server ("Mã vạch đã được dùng cho hàng khác."), giống
   `productFormErrors` của catalog. Lý do 422 (`invalid_format`, `too_long`) dùng câu chung của
   `fieldReasonMessage`.
9. File ngoài danh sách `## Files` mà tôi đã sửa:
   - `procurement/ports.go`: cần cho port.
   - Hai dialog: đây là "nơi map lỗi lên dòng" mà phase nhắc tới.
   - `schema.d.ts`: được phép sửa qua gen.
   - Không đụng file nào của agent điện thoại.

## Kiểm tra

- `go build ./... && go vet ./... && go vet -tags integration ./...`: sạch.
- `go test -race ./internal/procurement/... ./internal/catalog/... ./internal/app/...`: pass.
- Test tích hợp mới, nằm trong `TestPurchasingScreensOverHTTP`:
  - PO có hàng mới kèm mã " 8936000111222 " → `/sku-lookup` trả đúng SKU ở đơn vị cơ sở, factor 1. Tạo lại với cùng mã → 409 trên `lines.0.new_product.barcode`. Mã "8936 000" → 422 trên cùng ô. Số sản phẩm không đổi.
  - Phiếu nhận có hai hàng mới mang `012345678905` và `0012345678905` → 409 `barcode_taken` trên `lines.1.new_product.barcode`, không tạo SKU nào.
- `make test-integration` (toàn bộ): pass.
- `make lint` (go vet, golangci-lint báo 0 issues, eslint, tsc): sạch.
- `corepack pnpm gen:api && corepack pnpm lint && corepack pnpm typecheck && corepack pnpm test`: sạch, 172 file / 1286 test pass.
- Vitest mới:
  - `buy-lines.test.ts`:
    - `scannedNewLine`;
    - `newProductOf` gửi barcode đã trim, không gửi khi rỗng;
    - chặn trùng chính xác ở cả đơn và phiếu nhận;
    - hai dạng của cùng một UPC để server xử lý;
    - `barcodeErrors` đặt lỗi về đúng dòng (cả 409 lẫn 422);
    - `addScan` cho mã đã có, carton, mã lạ, đơn vị lớn → `needs_desktop_edit`, combo, và nhận theo đơn.
  - `buy-preset.test.tsx`, qua điện thoại đã ghép:
    - thẻ ghép đặt trên bảng;
    - mã có sẵn thêm đúng SKU và đơn vị, quét lại thì cộng dồn, mã carton thêm dòng thùng;
    - mã lạ mở hàng mới có mã vạch và focus ô Tên, quét lại +1, đổi sang thùng rồi quét → `needs_desktop_edit`;
    - 4 mã đến liền nhau đều được tính;
    - lỗi mạng hiện toast và trả `not_found`.
  - `new-receipt-dialog.test.tsx`: mã thùng cộng 12 vào dòng đặt (24 → 36); mã SKU ngoài đơn và mã lạ đều trả `not_in_order` kèm toast, không thêm dòng.
  - `new-po-dialog.test.tsx`:
    - body gửi đi có `barcode` đã trim;
    - server trả 409 thì lỗi hiện dưới ô Mã vạch của đúng dòng (dòng trống đầu bảng bị bỏ qua khi gửi), và mất khi sửa mã;
    - hai dòng trùng mã bị chặn, không gửi.

Lint và typecheck web không báo lỗi nào trong file của agent điện thoại.

## Việc tiếp theo

- Phase 5 xong, không còn phụ thuộc mở. Thay đổi chưa commit, theo đúng chỉ dẫn.

Status: DONE
Summary: Backend nhận và kiểm tra `new_product.barcode`, trả `barcode_taken` về `lines.i.new_product.barcode`. Đơn nhập và Phiếu nhận nhận mã quét từ điện thoại theo đủ các bước 1–6, kèm ô Mã vạch và lỗi hiện trên từng dòng. Toàn bộ kiểm tra Go, tích hợp, lint và test web đều pass.
Concerns/Blockers: Không có.
