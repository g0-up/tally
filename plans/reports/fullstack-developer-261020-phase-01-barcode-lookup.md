# Báo cáo triển khai: tra mã vạch theo đơn vị (phase 1)

Plan: `plans/261020-1227-phone-barcode-scanning/phase-01-barcode-lookup.md`. Trạng thái: xong. Mọi mục trong
"Việc cần làm" và mọi ca trong "Validation" đã có code và test. Chưa commit, chưa sửa file plan.

## File đã đổi

API (Go):
- Tạo `internal/catalog/code_variants.go`, `code_variants_test.go`. `CodeVariants` sinh mã đúng như gõ trước, rồi các
  dạng GTIN 12/13/14 và UPC-E⇄UPC-A (UPC-E chỉ nhận khi số kiểm tra đúng và nén ngược ra đúng mã đó).
  `canonicalCode` lấy dạng 14 chữ số làm khóa.
- Tạo `internal/catalog/code_lookup.go`: `LookupCode`, `LookupCodeIn(ctx, q, code)`, `LookupSkuCode` (kèm
  `SkuListItem` trong cùng transaction). Thứ tự: mã vạch theo thứ tự biến thể, sau đó `sku_code` (không phân biệt hoa
  thường) theo đơn vị cơ sở. Chỉ khớp SKU, sản phẩm và đơn vị SKU còn hoạt động.
- Sửa `skus.go`: export `ValidateBarcode`. `insertBarcode` lấy advisory lock theo tenant + mã chuẩn, sau đó chặn mọi
  biến thể đã có (`barcode_taken`). Vẫn giữ việc bắt lỗi unique.
- Sửa `errors.go` (`code_not_found`) và `http.go` (`GET /sku-lookup`).
- Sửa `internal/sales/pos_bootstrap.go`: `PosTile.Barcodes` (`code`, `unit_id`, `unit_name`), lấy bằng một truy vấn,
  mặc định là mảng rỗng.
- Tạo migration `000059_catalog_sku_barcodes_sku_index` (up/down), index `ix_sku_barcodes__sku (tenant_id, sku_id)`.
- Sửa `api/openapi.yaml`: `/sku-lookup`, `SkuCodeMatch`, `SkuBarcodeRef`, `PosTile.barcodes` (bắt buộc). Mô tả
  `addBarcode` nói thêm về việc chặn biến thể.
- Test tích hợp: `internal/catalog/code_lookup_integration_test.go`, `internal/app/sku_lookup_integration_test.go`
  (HTTP 200/404/422/409 và bootstrap POS có barcodes).

Web:
- Tạo `shared/lib/barcode.ts` + test: `ScanSource`, `ScanOutcome` (đủ 5 giá trị), `matchCode`.
- Tạo `features/catalog/api/sku-lookup.ts`: `lookupSkuCode(client, code)`.
- Tạo `features/pos/lib/scan-match.ts` + `scan-match.test.ts`: `posScan(code, source, deps)` và `tileProduct`.
- Sửa `features/pos/pages/pos-page.tsx`: thêm `scanCode(code, source)`. Enter gọi nó với `"keyboard"`, và ô quét chỉ
  xóa khi đã thêm dòng.
- Sửa `features/inventory/components/count-drawer.tsx`: thêm `countScan(code)` đi qua `/sku-lookup`, cộng theo
  `factor`, và giữ một ref chứa lines/edits mới nhất. `count-drawer.test.tsx` có 6 ca mới, và ca cũ được sửa sang
  thông báo mới.
- Sửa `features/sales/schemas.ts` (`barcodes` `.default([])`), fixture `pos/offline/testing/handlers.ts`, và sinh lại
  `shared/api/schema.d.ts`.

## Quyết định

- `SkuBarcodeRef`/`TileBarcode` có thêm `unit_name`, vì POS offline phải in “Mã này là của {đơn vị}…” mà không gọi
  được server.
- `lookupSkuCode` trả `null` cho cả 404 `code_not_found` lẫn mọi 422: chuỗi không thể là mã vạch thì cũng không SKU nào
  mang được. Các lỗi khác vẫn ném ra. POS báo “Không tra được mã “…”: …”, Kiểm kê báo “Không tra được mã …: …”, và cả
  hai trả `not_found`.
- POS: mã trống trả `not_found` mà không báo gì. Khi lookup lỗi, nguồn bàn phím vẫn thử khớp theo từ.
- Kiểm kê: một SKU có nhiều dòng (màu, lô) thì cộng vào dòng đầu tiên, giống hành vi cũ. `useSkusByIds` giữ lại, chỉ
  dùng cho `avg_cost`.
- Helper `countServer` trong test nay áp lần lưu vào đúng dòng theo `line_id`, thay vì thay cả phiên bằng một dòng.
  Các ca một dòng cho kết quả như cũ; ca 201 SKU cần cách mới.
- Ca “hai lần quét liền nhau” giữ cả hai lookup lại cho đến khi lookup thứ hai được gọi, nên cả hai lần quét bắt đầu
  từ cùng một render. Tôi đã thử cho tạm `countScan` đọc giá trị của render: test đỏ (ra 1 thay vì 2). Sau đó đã khôi
  phục code.

## Lệch so với phase file

- `features/catalog/index.ts` có thêm một dòng export. File này không nằm trong danh sách, nhưng mọi import chéo
  feature trong repo đều đi qua barrel này.
- `features/pos/api/adapters.ts`: `tileColors` nhận `Pick<PosTile, "stock">`, để SKU tra từ server dùng lại được.
  File này cũng không nằm trong danh sách.
- Hầu hết fixture liệt kê trong phase không phải sửa. `product-tile.test.tsx` và gói offline đi qua `posTileSchema`
  (có `.default([])`), còn fixture procurement/catalog/kits là `SkuListItem`, không phải `PosTile`. Chỉ handler offline
  của POS được thêm barcodes. Typecheck qua.
- Test gói offline cũ nằm trong `scan-match.test.ts`. Ca này bỏ khóa `barcodes` khỏi tile mà không parse lại.
- Test HTTP và bootstrap POS nằm ở `internal/app/sku_lookup_integration_test.go`, cạnh các test HTTP khác.

## Kết quả kiểm tra

- `go build ./... && go vet ./...` (kể cả `-tags integration`): qua.
- `go test ./internal/catalog/... ./internal/sales/...`: qua.
- `make test` (api + web): qua, web 160 file / 1153 test.
- `make test-integration` (đầy đủ, `go test -race -count=1 -tags integration ./...`): qua 50 gói, trong đó có
  `internal/app` (gồm contract test OpenAPI), `internal/catalog`, `internal/sales`, `migrations`.
- `corepack pnpm gen:api && lint && typecheck && test`: qua.
- `make lint` (go vet, golangci-lint 0 issues, eslint, tsc): qua.

## Ghi chú còn lại

- Comment của `PosProduct` trong `features/pos/hooks/use-pos-cart.ts` vẫn ghi “the SKU in its base unit”, nay không
  còn đúng vì dòng có thể ở đơn vị thùng. File này không thuộc phạm vi nên tôi chưa sửa. Reducer của giỏ gộp dòng theo
  skuId + unitId nên hành vi vẫn đúng.
- `apps/web/e2e/visual/parity.spec.ts-snapshots/` chưa được track nhưng có từ sáng 20/10, không phải do phase này tạo.
