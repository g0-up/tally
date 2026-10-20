# Báo cáo triển khai — Đổi đơn vị của mã vạch trong Hàng hóa

- Plan: `plans/261020-1227-phone-barcode-scanning/` (phase-06-catalog-barcode-units.md)
- Trạng thái: hoàn thành. Toàn bộ cổng kiểm tra đều xanh.
- Ngày: 2026-10-21 (Asia/Saigon)

## Kết quả

Thêm `PATCH /skus/:id/barcodes/:barcodeId {unit_id}` để chuyển một mã vạch sang đơn vị khác của
cùng SKU. Lần tra mã sau (`/sku-lookup`) trả đơn vị mới. Web có nút "Mã vạch" trên mỗi dòng của
trang Hàng hóa; nút mở drawer `BarcodeUnitsDrawer` liệt kê các mã cùng đơn vị khi quét, và lưu
ngay khi người dùng chọn đơn vị khác.

## Tệp đã thay đổi

API
- `apps/api/internal/catalog/skus.go`: `UpdateBarcodeUnit(ctx, actor, skuID, barcodeID, unitID)`.
  Thiếu `unit_id` trả 422 `unit_id: required`. Trong giao dịch, service khóa SKU, đọc mã theo
  `id + sku_id` (không có thì 404), rồi kiểm đơn vị thuộc SKU (không thuộc thì 422
  `unit_id: not_a_unit_of_sku`). Nếu đơn vị không đổi thì trả nguyên mã, không ghi audit. Nếu đổi thì
  `UPDATE ... RETURNING`, chạy `Reindex`, rồi ghi audit `sku.barcode_update` (before/after).
- `apps/api/internal/catalog/dto.go`: `barcodeUnitRequest{unit_id}`.
- `apps/api/internal/catalog/http.go`: route PATCH, handler `updateBarcodeUnit` trả 200 `Barcode`.
- `apps/api/api/openapi.yaml`: operation `updateBarcodeUnit` (200/401/404/422) và schema
  `BarcodeUnitUpdate`.
- `apps/api/internal/catalog/code_lookup_integration_test.go`: thêm
  `TestUpdateBarcodeUnitMovesTheNextLookup`.
- `apps/api/internal/app/catalog_partner_integration_test.go`: thêm mục PATCH (đơn vị `thung`)
  ngay trước mục DELETE.
- `apps/api/internal/app/sku_lookup_integration_test.go`: thêm subtest HTTP "a barcode moved to the
  carton is looked up as the carton".

Web
- `apps/web/src/features/catalog/schemas.ts`: `barcodeSchema` / `Barcode`.
- `apps/web/src/features/catalog/api/queries.ts`: `skuKeys.barcodes(skuId)`, `useSkuBarcodes`.
- `apps/web/src/features/catalog/api/mutations.ts`: `useUpdateBarcodeUnit(skuId)`,
  `barcodeUnitErrorMessage`.
- `apps/web/src/features/catalog/containers/barcode-units-drawer.tsx` (mới): drawer và
  `BarcodeUnitPicker` cho từng dòng.
- `apps/web/src/features/catalog/containers/barcode-units-drawer.test.tsx` (mới): 5 test.
- `apps/web/src/features/catalog/pages/products-page.tsx`: cột thao tác `acts` với nút "Mã vạch".
- `apps/web/src/test/msw/barcodes.ts` (mới): MSW có trạng thái cho GET/PATCH barcodes.
- `apps/web/src/shared/api/schema.d.ts`: sinh lại bằng `corepack pnpm gen:api`.

## Quyết định

- Thứ tự kiểm tra: tra mã trong path trước, rồi mới kiểm đơn vị trong body. Nhờ vậy, mã ngoài SKU
  hoặc ngoài cửa hàng luôn trả 404, dù body chứa gì. `TestTenantIsolationAcrossEveryRoute` bắt
  đúng lỗi này: ở bản đầu, A gửi mã của B dưới SKU của A thì nhận 422 thay vì 404. Sau khi đổi thứ
  tự, test xanh.
- Có một lần đọc trước `UPDATE`, khác với "một câu UPDATE" trong plan. Lần đọc này cần để trả nguyên
  mã khi đơn vị không đổi, và để có giá trị `before` cho audit. Lần đọc vẫn đúng vì nằm dưới khóa SKU.
- API chấp nhận cả đơn vị đã ngừng, giống `AddBarcode`. Web chỉ đưa ra các đơn vị đang dùng, cộng
  với đơn vị hiện tại của mã nếu đơn vị đó đã ngừng. Danh sách sắp theo hệ số, nhãn dạng
  "thùng (12 hộp)".
- Trong lúc đang lưu, select bị khóa bằng `aria-disabled` chứ không dùng `disabled`. Như vậy focus
  bàn phím giữ nguyên chỗ, giống quy ước nút đang ghi. Lựa chọn trong lúc chờ bị bỏ qua.
- Lưu xong thì hiện toast "Mã {code} nay là mã của {đơn vị}." Nếu bị từ chối, lỗi hiện ngay dưới
  select (`role=alert`, `aria-invalid`, `aria-describedby`) và giá trị quay về đơn vị cũ.
- Mutation không gửi Idempotency-Key hay If-Match, vì route không yêu cầu (gán unit_id hai lần cho
  kết quả như nhau). Lưu xong thì cập nhật cache barcodes và làm mới `skuKeys.all`.

## Kiểm tra

- `go build ./... && go vet ./... && go vet -tags integration ./...`: đạt.
- `go test -race ./internal/catalog/... ./internal/app/...`: đạt.
- `make test-integration` (toàn bộ): đạt cả 52 package, gồm `internal/app` với
  `TestTenantIsolationAcrossEveryRoute`.
- `make lint`: đạt (golangci-lint 0 issues, eslint, tsc).
- Web: `gen:api`, `lint`, `typecheck` đạt; `test` đạt 174 file / 1308 test; `build` đạt.
- Đã thử đột biến: bỏ bước khôi phục giá trị khi lỗi thì test "puts the old unit back" fail. Đã
  khôi phục mã.

## Sai khác so với danh sách tệp của phase

- `schemas.ts`: cần schema zod cho `Barcode`.
- `sku_lookup_integration_test.go`: kiểm qua HTTP rằng `/sku-lookup` trả đơn vị mới. Subtest chuyển
  mã về `cái` khi xong để subtest POS tiles vẫn đúng.
- `test/msw/barcodes.ts`: fixture MSW mới cho test drawer.
- `tenant_isolation_integration_test.go`: không sửa. Test này tự quét từ openapi, nên route mới đã
  có trong lượt quét. Lỗi nằm ở thứ tự kiểm tra của service, và đã sửa ở service.

## Lưu ý còn lại

- Chưa chạy e2e. Snapshot đối chiếu giao diện của trang Hàng hóa có thể đổi vì thêm cột thao tác.
- POS tiles và gói offline nhận đơn vị mới ở lần bootstrap kế tiếp. POS online, Kiểm kê và Nhập hàng
  nhận ngay ở lần quét sau qua `/sku-lookup`.
- `make test-integration` bật Postgres dev (`make db-up`, compose project dev). Không động đến
  container prod.
