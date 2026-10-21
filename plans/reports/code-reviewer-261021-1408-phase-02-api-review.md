# Review Phase 2: API nhiều đơn vị mỗi SKU, giá theo đơn vị

- Phạm vi: `git diff -- apps/api` (24 file, +1446/-113), chưa commit.
- Spec: `plans/261020-1801-multi-unit-conversion-and-unit-prices/phase-02-api-units-and-unit-prices.md`.
- Các sai khác đã được chấp nhận (không báo lại): audit `sku.units_replace`; factor 0 → `not_positive`; đơn vị bị xoá nhưng khoá báo ở `units[<unit_id>]`; giá null ở dòng gốc giữ nguyên giá gốc.
- Đã chạy: `go build ./...`, `go vet` (catalog, pricing, procurement, app), `go test -count=1 ./internal/catalog/... ./internal/pricing/... ./internal/procurement/...` đều pass; `go test -tags integration ./internal/catalog/ -run TestSkuUnitTableSavesAtomically` pass.

## Tổng quan

Phần lõi của `ReplaceSkuUnits` đúng: chạy trong một transaction tenant (RLS), `lockSku` rồi mới so version (412), khoá `sku_units FOR UPDATE`, kiểm tra hợp lệ (422) trước khi kiểm tra khoá (409), xoá dòng giá trước khi xoá đơn vị, chuyển đơn vị nhập mặc định, tăng version. `locked_by` theo đúng thứ tự documents → price_history → barcodes (vòng lặp giữ lý do đầu tiên). Danh sách bảng chứng từ khớp schema: mọi bảng có `unit_id` theo SKU đều nằm trong `skuUnitReferences`, và chỉ `sku_barcodes` có FK tới `sku_units`. Xoá giá theo đơn vị đi qua `deleteRow`, nên có ghi `price_changes`. Cặp cũ `purchase_unit_id` + `factor` vẫn đi qua `purchaseUnitOf` như trước.

Còn 2 lỗi mức High và 2 vấn đề mức Medium cần xử lý trước khi merge.

## High

### H1. Phiếu mua tạo sản phẩm mới có `units` mà không có `purchase_unit_id`: giá vốn tham chiếu sai hệ số

- `apps/api/internal/procurement/new_products.go:110-116`: dòng phiếu được tính theo `p.lineUnit()`. Khi không có `PurchaseUnitID`, đơn vị này là đơn vị gốc.
- `apps/api/internal/catalog/quick_create.go:264-269`: `planUnits` lại chọn đơn vị có hệ số lớn nhất làm đơn vị nhập mặc định.
- `apps/api/internal/catalog/quick_create.go:88`: giá nhập được chia cho hệ số của đơn vị nhập mặc định đó.

Kịch bản A:
- Input: `POST /purchase-orders`, dòng `{qty: "10", unit_price: "1000", new_product: {base_unit_id: cái, units: [{dây, 50}, {thùng, 500}]}}`, không gửi `unit_id` của dòng và không gửi `purchase_unit_id`.
- Dòng được tính theo cái (giá 1000/cái), nên `PurchasePrice` = 1000 được giữ.
- Quick-create đặt thùng làm đơn vị nhập mặc định, nên `ref_cost` = 1000 / 500 = 2/cái. Giá đúng là 1000/cái, tức sai 500 lần.
- Đơn vị nhập mặc định của SKU (thùng) cũng khác đơn vị của dòng (cái).

Kịch bản B:
- Input: như trên nhưng dòng gửi `unit_id: thùng`.
- `unit` (thùng) khác `lineUnit()` (cái), nên giá bị bỏ (`price = nil`), dù thùng chính là đơn vị nhập mặc định. SKU được tạo mà không có giá vốn tham chiếu.

OpenAPI (`purchase_unit_id`: "With `units`, ... the line buys in it") và rule 2 của spec đều nói dòng được tính theo `PurchaseUnitID`. Test `TestSkuUnitTableOverHTTP` chỉ phủ trường hợp có `purchase_unit_id`.

Đề xuất: trong `createProduct`, khi `len(p.Units) > 0`, đặt `p.PurchaseUnitID = &unit` (đơn vị của dòng, đã kiểm qua `hasUnit`) trước khi gọi catalog. Khi đó đơn vị nhập mặc định, đơn vị của dòng và phép chia giá vốn dùng cùng một đơn vị. Cách khác: bắt buộc `purchase_unit_id` khi có `units` trong `validateNewProduct`. Nên thêm test integration cho cả hai kịch bản.

### H2. `GET /skus` (bộ chọn SKU ở nhiều màn hình) giờ quét toàn bộ 10 bảng dòng chứng từ không có index ở mỗi request

- `apps/api/internal/catalog/sku_list.go:151` gọi `readSkuUnits` → `fillSkuUnits` (`skus.go:342`) → `UnitLocks` (`usage_repo.go:108-145`).
- Truy vấn `documents` là `UNION` của 10 câu `SELECT sku_id, unit_id FROM <bảng> WHERE sku_id = ANY($1) AND unit_id = ANY($2)` trên invoice_lines, kit_recipe_items, pos_cart_lines, purchase_order_lines, purchase_return_lines, quote_lines, receipt_lines, sales_document_lines, sales_return_lines và stock_adjustment_lines.
- Migrations không có index nào trên `sku_id` của các bảng này. Chỉ có `(tenant_id, id)` và `(tenant_id, <doc>_id, line_no)`; đã kiểm bằng grep `INDEX`/`UNIQUE` trong `migrations/*.up.sql`. `price_changes` và `sku_barcodes` thì có index.

Kịch bản:
- Tenant có hàng trăm nghìn dòng POS và chứng từ bán. Mỗi lần gõ tìm trong bộ chọn SKU (phiếu nhập, kiểm kê, PO, tồn kho; web gọi `/skus` ở tất cả các màn hình này) chạy 10 lần seq-scan theo tenant, rồi `UNION` khử trùng trên toàn bộ dòng khớp.
- Chi phí tăng tuyến tính theo lịch sử giao dịch, không theo kích thước trang.
- Mỗi lần ghi trả về `SkuDetail` (PATCH SKU, PUT units, tạo sản phẩm, sinh biến thể) cũng gánh truy vấn này.

Đề xuất (lead hoặc người dùng chọn, vì phase cấm migration):
1. Thêm migration index `(tenant_id, sku_id, unit_id)` cho 10 bảng dòng. Đây là cách sửa đúng gốc.
2. Hoặc chỉ tính `locked_by` ở chi tiết SKU và PUT, không tính ở `GET /skus`. Cách này đổi spec rule 5, nên cần xác nhận phase 4 có đọc `locked_by` từ danh sách hay không.
3. Dù chọn cách nào, nên viết lại thành `unnest($1::uuid[], $2::uuid[]) k(sku_id, unit_id) WHERE EXISTS (...)` cho từng cặp. Cách này dừng ở dòng đầu tiên và bỏ được tích chéo `ANY × ANY` cùng bước `UNION` khử trùng.

## Medium

### M1. Bảng đơn vị cũ ghi đè thay đổi đồng thời: version của SKU không bảo vệ giá và đơn vị

`ReplaceSkuUnits` chỉ so `skus.version` (`skus.go:952`). Tuy vậy, các thao tác sau không tăng version (chỉ `UpdateSku` và `ReplaceSkuUnits` gọi `UpdateWithVersion`):
- `AddSkuUnit`, `UpdateSkuUnit`, `DeleteSkuUnit`;
- thêm barcode;
- đặt giá bán từ PO, danh sách sản phẩm hoặc màn tồn kho (`pricing.SetSalePrice`);
- sửa bảng giá.

Kịch bản:
- A mở bảng đơn vị ở v5, lúc đó dây = 24.000.
- Trong lúc A đang sửa, nhận hàng theo PO đặt giá bán dây = 26.000 qua `SetRetailPrice`, và version không đổi.
- A bấm lưu mà không sửa gì. `replaceUnitPrices` (`skus.go:1113-1131`) thấy 24.000 (từ form cũ) khác 26.000 (hiện tại), nên ghi lại 24.000. Giá mới bị đảo ngược âm thầm, và lịch sử ghi nguồn `catalog`.
- Tương tự, một đơn vị chưa khoá mà người khác vừa thêm bằng `POST /skus/{id}/units` sẽ bị xoá vì "không có trong body".

Đề xuất: tăng version của SKU trong các thao tác đơn vị và barcode của catalog. Với giá, cho client gửi giá đã thấy (ví dụ `expected_retail_price`) và trả 412 khi giá hiện tại khác. Ít nhất cần ghi rõ giới hạn này trong OpenAPI.

### M2. Bỏ trống trường thì xoá dữ liệu: `units` thiếu thì xoá mọi đơn vị chưa khoá, giá dòng thiếu thì xoá giá riêng

- `apps/api/internal/catalog/dto.go:554-567`: `skuUnitsRequest.Units` không có tag `validate`. Body `{}` hoặc `{"retail_price":"600"}` kèm `If-Match` hợp lệ sẽ xoá mọi đơn vị không khoá (cùng các dòng giá không có lịch sử). OpenAPI ghi `required: [units]` nhưng server không kiểm.
- Ở dòng đơn vị, `retail_price`/`wholesale_price` bị bỏ trống thì ra `nil` và bị xoá (`skus.go:1121`). Trong khi đó, ở cấp gốc, bỏ trống nghĩa là giữ nguyên. Hai ngữ nghĩa ngược nhau trong cùng một body.

Kịch bản:
- Client chỉ muốn đổi hệ số: `{"units":[{"unit_id": dây, "factor": "48"}]}`.
- Giá riêng 24.000 của dây bị xoá, và thùng (nếu chưa khoá) cũng bị xoá.

Đề xuất:
- Thêm `validate:"required"` cho `Units` (validator phân biệt slice nil với `[]`).
- Ở dòng, dùng kiểu ba trạng thái (thiếu, null, giá trị) hoặc bắt buộc gửi cả hai khoá giá; trả 422 `units[i].retail_price` = `required` khi thiếu.
- Hoặc ít nhất ghi rõ trong OpenAPI rằng "bỏ trống = null = xoá".

## Low

### L1. Race giữa kiểm tra khoá và xoá đơn vị (cùng mẫu với `DeleteSkuUnit` hiện có)

Các bảng dòng chứng từ tham chiếu `units`, không tham chiếu `sku_units`, và mã ghi chứng từ không đọc `sku_units` với `FOR SHARE` (đã grep, không có). Một chứng từ tạo đồng thời với đơn vị sắp bị xoá có thể commit sau khi `UnitLocks` (`skus.go:962`) đã trả "không khoá". Kết quả là dòng chứng từ trỏ tới một đơn vị mà SKU không còn. Snapshot `unit_factor_snap` và `unit_name_snap` giữ được lịch sử, nên tác động nhỏ. Không phải lỗi mới; ghi lại để biết.

### L2. Không giới hạn số dòng `units`

Mỗi dòng của PUT, create và quick-create sinh khoảng 1 UPDATE/INSERT cộng 2 × `SetSalePrice` (mỗi lần khoảng 5–6 truy vấn, khoá dòng `price_lists` gốc). Body rất dài sẽ giữ khoá bảng giá gốc của tenant lâu. Nên đặt trần (ví dụ 20 dòng → 422 `units` = `too_many`).

## Đã kiểm, không phải lỗi

- Atomic: mọi bước nằm trong `s.write` (một tx). Lỗi 409, 422 hoặc FK giữa chừng đều rollback; test integration xác nhận bảng không đổi.
- `If-Match`: thiếu thì 428 (`ifMatch`), cũ thì 412 sau `lockSku`; ETag lấy theo version mới.
- Chuyển đơn vị nhập mặc định: `movePurchaseDefault` chạy sau khi tắt đơn vị, sau đó `restoreDefaults` trả mặc định bán và nhập về đơn vị gốc khi trống. Đơn vị bị xoá cũng được xử lý.
- Xoá giá trước khi xoá unit không khoá (`skus.go:991-1003`). Đơn vị không khoá thì không có `price_changes`. Ngoài `writer.go` và `draft_prices.go`, không có chỗ nào khác ghi `price_list_items`, và cả hai đều ghi lịch sử, nên câu DELETE không làm mất lịch sử.
- Validation lúc tạo: `duplicate`, `is_base`, `required`, lý do của factor, giá âm, `with_purchase_unit`, `not_a_unit` đều đúng chỉ số dòng. `unknown_unit` (đơn vị không tồn tại hoặc không active) kiểm trong tx ở `addUnits` và ở `checkUnitRows`.
- Giá đọc được: chỉ từ hai bảng giá gốc (có `ux_price_list_items__base` theo kind), general color, `min_qty = 0`. Dòng `unit_id IS NULL` thắng dòng ghi rõ đơn vị gốc.
- Tenant/RLS: các truy vấn mới đều chạy trong tx tenant. Role `tally_readonly` có `SELECT ON ALL TABLES`, nên phiên support đọc được `locked_by`. Route mới đã nằm trong test cô lập tenant.
- N+1: giá và khoá đều là 1 truy vấn mỗi nguồn cho cả trang. Vấn đề nằm ở chi phí từng truy vấn (H2), không phải số truy vấn.
- Tương thích ngược: trường mới trong `SkuUnit` chỉ là thêm vào. `price: null` kèm `unit_id` không phải gốc trước đây là 422, nay là xoá giá, nên client cũ không gửi kiểu này. Cặp `purchase_unit_id` + `factor` giữ nguyên đường đi và tên lỗi.

## Hành động đề xuất (theo thứ tự)

1. H1: sửa `procurement.createProduct` cho đơn vị dòng thành `PurchaseUnitID` khi có `Units`, và thêm test 2 kịch bản.
2. H2: quyết định giữa thêm index (migration) và bỏ `locked_by` khỏi `GET /skus`; viết lại `UnitLocks` theo cặp với `EXISTS`.
3. M2: `validate:"required"` cho `units`, xử lý giá dòng bị bỏ trống.
4. M1: tăng version trong các thao tác đơn vị/barcode, hoặc ghi rõ giới hạn; cân nhắc giá kỳ vọng.
5. L2: trần số dòng `units`.

## Câu hỏi chưa giải quyết

- Phase 4 (web) có cần `locked_by` từ `GET /skus` không, hay chỉ cần ở chi tiết SKU? Câu trả lời quyết định hướng sửa H2.
- Có được thêm migration index trong đợt này không? Phase 2 ghi "No migration is needed", không phải là cấm hẳn.
