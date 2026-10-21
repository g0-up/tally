# Báo cáo triển khai Phase 2: API: N đơn vị và giá theo đơn vị

- Plan: `plans/261020-1801-multi-unit-conversion-and-unit-prices/`
- Phase: `phase-02-api-units-and-unit-prices.md` (đã đánh dấu `status: done`, mọi checkbox `[x]`)
- Trạng thái: hoàn tất, toàn bộ lệnh kiểm tra đều pass. Chưa commit (theo yêu cầu).

## Thay đổi chính

- **Tạo SKU kèm nhiều đơn vị.** `POST /products` (`sku.units`) và `POST /products:quick-create` (`units`) nhận `[{unit_id, factor, retail_price?, wholesale_price?}]`. Mỗi dòng được kiểm tra trước transaction (`planUnits` trong `catalog/quick_create.go`), và lỗi trỏ đúng dòng: `sku.units[1].unit_id` = `duplicate` / `is_base` / `unknown_unit`, `sku.units[i].factor` = `required` / `invalid` / `not_positive` / …, `sku.units` = `with_purchase_unit` khi gửi kèm cặp cũ `purchase_unit_id` + `factor`. Đơn vị nhập mặc định là đơn vị có hệ số lớn nhất. Giá của từng đơn vị được ghi qua `PriceWriter.SetSalePrice` với `UnitID` của đơn vị đó, trong cùng transaction. Cặp cũ vẫn chạy như trước.
- **Lưu cả bảng đơn vị.** `PUT /skus/{id}/units` (`ReplaceSkuUnits` trong `catalog/skus.go`) bắt buộc `If-Match` theo version của SKU và chạy trong một transaction:
  - thêm đơn vị mới, sửa factor/active của đơn vị đã có;
  - xoá đơn vị không còn trong body nếu không bị khoá; nếu bị khoá thì trả 409 `unit_in_use` với `fields.units[<unit_id>]` = lý do khoá và không thay đổi gì;
  - đặt hoặc xoá giá (null = xoá giá riêng của đơn vị);
  - khi đơn vị nhập mặc định bị tắt, chuyển mặc định sang đơn vị active có hệ số lớn nhất, hoặc về đơn vị gốc nếu không còn;
  - ghi audit `sku.units_replace` kèm before/after, và tăng version.
- **Xoá giá riêng của một đơn vị.** `SalePriceCommand` với `Price == nil` nay chấp nhận một `UnitID` không phải đơn vị gốc. Lệnh này xoá dòng giá và ghi `price_changes`, sau đó báo giá quay về giá gốc × hệ số. Nếu không có giá để xoá thì không ghi gì. Giá null cho đơn vị gốc trả `price` = `required`, giá null kèm `apply_all_colors` trả `requires_price`.
- **Đọc giá và lý do khoá.** `SkuUnit` có thêm `retail_price`, `wholesale_price` và `locked_by` (`documents` → `price_history` → `barcodes` → null). Các trường này được đọc bằng một truy vấn `ANY($1)` cho mỗi nguồn trên cả trang (`readSkuUnits`/`fillSkuUnits`, `UsageReader.UnitLocks`), và có mặt trong `GET /skus`, chi tiết SKU và chi tiết sản phẩm.
- **Procurement.** `NewProduct.Units` được map qua `app/procurement_ports.go` sang quick-create. Một dòng phiếu có thể tính theo bất kỳ đơn vị nào của sản phẩm mới (`hasUnit`).
- **Contract.** Cập nhật `api/openapi.yaml`: thân request tạo mới, `PUT /skus/{id}/units`, các schema `NewSkuUnit` và `SkuUnitsRequest`, các trường mới của `SkuUnit`, và quy tắc giá null. Thêm `TestOpenAPISkuUnitLockReasons` để cố định enum `locked_by`. Kiểu web được sinh lại bằng `corepack pnpm gen:api`.

## Endpoint / DTO

| Endpoint | Body | Kết quả |
|---|---|---|
| `POST /products` | `sku.units: NewSkuUnit[]` | 201; 422 với `sku.units[i].*` |
| `POST /products:quick-create` | `units: NewSkuUnit[]`; `purchase_unit_id` chọn trong các đơn vị | 201; 422 `purchase_unit_id` = `not_a_unit` |
| `PUT /skus/{id}/units` (`replaceSkuUnits`) | `SkuUnitsRequest {retail_price?, wholesale_price?, units: [{unit_id, factor, active=true, retail_price, wholesale_price}]}` | 200 `SkuDetail` + ETag; 409 `unit_in_use` / `sku_unit_in_use`; 412; 428; 422 `units[i].*` |
| `POST /purchase-orders`, phiếu nhập | `new_product.units` | sản phẩm được tạo kèm đơn vị; `base_qty` = số lượng × hệ số |

## Lệnh đã chạy

| Lệnh | Kết quả |
|---|---|
| `go build ./... && go vet ./... && go vet -tags integration ./...` | pass |
| `go test -count=1 ./internal/catalog/... ./internal/pricing/... ./internal/procurement/... ./internal/app/...` | pass |
| Như trên với `-tags integration` (testcontainers Postgres) | pass (app 110s); chạy lại catalog sau khi đổi tên audit action: pass |
| `go test -count=1 ./...` | lần đầu FAIL ở `TestAuditActionNamesAreEntityDotVerb` vì `sku.units.replace`; sau khi đổi thành `sku.units_replace` thì pass |
| `gofmt -l .` | sạch |
| `cd apps/web && corepack pnpm gen:api && corepack pnpm typecheck` | pass |

Các test mới:

- `catalog_test.go`: `TestPlanUnitsChecksEachRowAndPicksTheBiggestPurchaseUnit`.
- `catalog_integration_test.go`: `TestSkuUnitTableSavesAtomically`.
- `pricing_integration_test.go`: `TestRemovingAUnitPriceFallsBackToBaseTimesFactor`.
- `new_products_test.go`: thêm case cho `TestValidateNewProduct` và test mới `TestNewProductHasUnit`.
- `openapi_contract_test.go`: `TestOpenAPISkuUnitLockReasons`.
- `catalog_partner_integration_test.go`: `TestSkuUnitTableOverHTTP`, phủ mọi case trong mục Verification của phase:
  - Kẹo: 24.000 / 250.000, và chỉ 2 dòng giá;
  - 422 có chỉ số dòng;
  - xoá dây đã dùng trên phiếu nhập trả 409 và không đổi gì;
  - `locked_by` đủ 4 giá trị;
  - tắt thùng thì dây thành đơn vị nhập mặc định, thẻ kho không còn thùng, tra mã vạch của thùng trả 404;
  - xoá giá dây thì báo giá 25.000;
  - `If-Match` cũ trả 412;
  - PO có sản phẩm mới kèm đơn vị cho `base_qty` 1000, nhập kho xong tồn 1000.
- Route mới cũng được thêm vào `idRoutesOf` và `bodyHints` của test cô lập tenant.

## Sai khác so với plan và lý do

1. **Tên audit action là `sku.units_replace`, không phải `sku.units.replace`.** Test `TestAuditActionNamesAreEntityDotVerb` bắt buộc dạng `entity.verb`, và các action cùng nhóm đặt tên là `sku.unit_add` / `sku.unit_update`. Phase file đã được cập nhật theo.
2. **Factor bằng 0 trả `not_positive`, không phải `invalid` như ví dụ trong plan.** Đây là lý do `parseFactor` sẵn có trả về. Format sai vẫn trả `invalid`.
3. **Đơn vị bị xoá nhưng đang khoá được báo ở `units[<unit_id>]`.** Đơn vị đã bị xoá thì không còn chỉ số trong body.
4. **Dòng gốc với giá null trong `PUT` giữ nguyên giá gốc.** Giá gốc là bắt buộc. Riêng giá null ở dòng đơn vị khác thì xoá giá.
5. **`purchase_unit_id` của quick-create khi có `units`** chọn đơn vị nhập mặc định (gốc hoặc một trong các đơn vị), và `purchase_price` tính theo đơn vị đó. Dòng PO tính theo đơn vị khác đơn vị nhập thì không tính giá vốn tham chiếu, giống trước đây.
6. **Có thêm guard `apply_all_colors` + giá null → `requires_price`.**
7. **Sửa thêm file ngoài danh sách Files** vì cần cho build/test:
   - `catalog/model.go`: thêm trường cho `SkuUnit`;
   - `catalog/variant_service.go`: dùng reader mới;
   - `catalog/catalog_test.go`: stub `UnitLocks` và test mới;
   - `app/domains.go`: truyền con trỏ giá;
   - `app/tenant_isolation_integration_test.go`: được phép theo yêu cầu.

   Không cần sửa `procurement/ports.go`.
8. **Trong OpenAPI, `SkuUnit.retail_price`, `wholesale_price` và `locked_by` là required** (nullable). Typecheck web vẫn pass.

## Rủi ro còn lại

- Truy vấn khoá `documents` quét các bảng dòng chứng từ theo `sku_id`, mà một số bảng không có index trên cột này. Phase cấm thêm migration nên chưa xử lý. Truy vấn chỉ chạy một lần mỗi trang hoặc mỗi lần lưu, nhưng nên theo dõi khi dữ liệu lớn.
- `plan.md` vẫn ghi Phase 2 là "Pending". File này nằm ngoài quyền sửa của tôi, nên để orchestrator cập nhật.

## Câu hỏi chưa giải quyết

- Không có.
