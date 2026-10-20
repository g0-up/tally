---
phase: 6
title: "Danh mục: đổi đơn vị của mã vạch"
status: pending
priority: P2
effort: 0.5d
dependencies: [1]
---

# Phase 6: Danh mục, đổi đơn vị của mã vạch

## Mục tiêu và bối cảnh

Ở Danh mục, người dùng xem mọi mã vạch của một SKU kèm đơn vị và đổi đơn vị của từng mã, ví dụ chuyển mã in trên thùng
từ hộp sang thùng. Đổi đơn vị là một thao tác trong một giao dịch. Đổi lỗi thì mã vẫn ở đơn vị cũ. Lần quét sau ở POS (online),
Kiểm kê và Nhập hàng lấy đơn vị mới, vì mọi màn đều tra `/sku-lookup` của phase 1.

Bằng chứng:
- API đã có `GET`, `POST`, `DELETE /skus/{id}/barcodes` (`apps/api/internal/catalog/http.go:90-92`) và
  `PATCH /skus/{id}/units/{unitId}` theo cùng mẫu (`:88`). Chưa có thao tác đổi đơn vị.
- `AddBarcode` khóa SKU, kiểm tra đơn vị thuộc SKU bằng `readSkuUnit`, gọi `Reindex` và ghi audit `sku.barcode_add`
  (`apps/api/internal/catalog/skus.go:813-846`). `DeleteBarcode` theo cùng mẫu (`:849-870`).
- Web chưa có màn sửa hàng nào. Trang Hàng hóa chỉ có bảng và hộp thoại “Thêm hàng hóa”
  (`apps/web/src/features/catalog/pages/products-page.tsx:82-106`). Bảng hỗ trợ cột thao tác (`ACTIONS_HEADER`,
  `apps/web/src/shared/ui/data-table/data-table.tsx:11`). Ngăn chi tiết dùng `DetailDrawer`
  (`apps/web/src/shared/ui/detail-drawer.tsx:25`).
- `SkuListItem.units` đã có `unit_id`, `unit_name`, `factor`. `SkuListItem` không có mã vạch, nên ngăn đọc
  `GET /skus/{id}/barcodes` để có `id` và đơn vị của từng mã.
- Test phân quyền đối tác liệt kê mọi route của catalog kèm thân mẫu, gồm ba route mã vạch
  (`apps/api/internal/app/catalog_partner_integration_test.go:264-266`); route mới phải có mục tương ứng.
- POS online tra `/sku-lookup` ở mỗi lần quét không khớp ô. `PosTile.barcodes` chỉ đổi ở lần tải bootstrap kế tiếp,
  và gói offline giữ bản đã tải cho tới khi tải lại.

<!-- Updated: Red Team 2026-10-20 - một phép tra /sku-lookup, bỏ SkuListItem.barcodes và tile-lookup, thêm test phân quyền đối tác -->
<!-- Updated: Validation Session 1 - ngăn Mã vạch riêng chỉ đổi đơn vị -->

## Files

- Sửa `apps/api/internal/catalog/skus.go`: `UpdateBarcodeUnit(ctx, actor, skuID, barcodeID, unitID)`.
- Sửa `apps/api/internal/catalog/http.go` (route `PATCH /skus/:id/barcodes/:barcodeId`, handler), `dto.go` nếu cần.
- Sửa `apps/api/api/openapi.yaml`: đường dẫn mới, schema `BarcodeUnitUpdate {unit_id}`.
- Test: `apps/api/internal/catalog/*_integration_test.go` cho đổi đơn vị.
- Sửa `apps/api/internal/app/catalog_partner_integration_test.go`: thêm mục
  `{method: "PATCH", path: "/skus/{id}/barcodes/{barcodeId}", args: u(st.sku, st.barcode), body: map[string]any{"unit_id": thung}}`, đặt trước mục `DELETE` của cùng mã
  vì `DELETE` xóa `st.barcode`.
- Sửa `apps/web/src/features/catalog/api/queries.ts` (`useSkuBarcodes`) và `api/mutations.ts` (`useUpdateBarcodeUnit`).
- Tạo `apps/web/src/features/catalog/containers/barcode-units-drawer.tsx` và `barcode-units-drawer.test.tsx`.
- Sửa `apps/web/src/features/catalog/pages/products-page.tsx`: cột thao tác có nút “Mã vạch”.
- Sửa `apps/web/src/test/msw/` cho các route mã vạch.

## Các bước

1. **Service.** `UpdateBarcodeUnit` trong `s.write`:
   - `lockSku`, rồi `readSkuUnit(skuID, unitID)`. Đơn vị không thuộc SKU trả 422 `{unit_id: not_a_unit_of_sku}`.
   - Một câu `UPDATE sku_barcodes SET unit_id = $1 WHERE id = $2 AND sku_id = $3 RETURNING …`. Không có dòng trả 404.
   - `Reindex` SKU, audit `sku.barcode_update` với bản trước và sau.
   - Đơn vị không đổi thì trả mã hiện tại, không ghi audit.
2. **HTTP.** `PATCH /skus/:id/barcodes/:barcodeId`, body `{unit_id}`, trả `Barcode`. Cùng quyền và nhóm route với
   `addBarcode`.
3. **OpenAPI** và `corepack pnpm gen:api`.
4. **Ngăn “Mã vạch”.** Nút “Mã vạch” ở cột thao tác của mỗi dòng mở `BarcodeUnitsDrawer` cho SKU đó. Ngăn liệt kê từng
   mã với một ô chọn đơn vị lấy từ `sku.units`. Đổi ô chọn thì gọi `PATCH` ngay, khóa ô trong lúc chờ, thành công thì
   làm mới (invalidate) `/skus/{id}/barcodes` và `/skus`. POS online, Kiểm kê và Nhập hàng thấy đơn vị mới ngay ở lần
   quét sau qua `/sku-lookup`; ô POS và gói offline cập nhật ở lần tải bootstrap kế tiếp. Lỗi thì trả ô chọn về đơn vị
   cũ và hiện lỗi dưới ô. SKU chưa có mã vạch thì hiện “Hàng này chưa có mã vạch.”
5. Thêm, xóa mã vạch không nằm trong phạm vi phase này.

## Validation

- `make test-integration` cho catalog: đổi hộp sang thùng thì `LookupCode` trả thùng; đơn vị của SKU khác trả 422 và mã
  giữ đơn vị cũ; mã của SKU khác trả 404; mã của tenant khác trả 404; audit `sku.barcode_update` được ghi.
- Test tích hợp với dữ liệu đã đổi: `/sku-lookup` trả đơn vị thùng và `factor` của thùng.
- `catalog_partner_integration_test.go` qua với route `PATCH` mới.
- Vitest `barcode-units-drawer.test.tsx`: liệt kê mã kèm đơn vị, đổi đơn vị gọi `PATCH` đúng thân, lỗi trả ô chọn về
  đơn vị cũ, SKU không có mã hiện đúng câu.
- `openapi_contract_test.go`, `corepack pnpm lint|typecheck|test` sạch.

## Rủi ro

- Ngăn Mã vạch là màn sửa đầu tiên ở Danh mục. Phạm vi giữ ở đổi đơn vị để không thành một màn sửa hàng đầy đủ.
- Phiên POS offline đã tải `barcodes` cũ vẫn dùng đơn vị cũ cho đến khi tải lại gói offline. Mã đã chuyển sang thùng
  thì bị từ chối khi offline theo quyết định của phase 1, nên không bán nhầm giá.

## Bảo mật

Route mới nằm trong nhóm tenant đã đăng nhập, ghi dưới RLS và có audit như các thao tác mã vạch hiện có.

## Rollback

Hoàn tác các file trên. Không có migration.

## Việc cần làm

- [x] `UpdateBarcodeUnit` (khóa SKU, kiểm tra đơn vị, `Reindex`, audit)
- [x] Route `PATCH /skus/:id/barcodes/:barcodeId`, OpenAPI, `gen:api`
- [x] Mục mới trong `catalog_partner_integration_test.go`
- [x] `useSkuBarcodes`, `useUpdateBarcodeUnit`, `BarcodeUnitsDrawer`, nút “Mã vạch”
- [x] Test tích hợp và Vitest ở mục Validation
