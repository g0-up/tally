# Báo cáo Phase 4: Sửa đơn vị & giá, giá theo đơn vị (web)

Plan: `plans/261020-1801-multi-unit-conversion-and-unit-prices/`. Trạng thái: hoàn tất. Chỉ sửa trong `apps/web`, chưa commit.

## Kết quả

### Danh mục › Hàng hóa

Dòng hàng hóa loại goods có thêm nút "Đơn vị & giá", mở `EditUnitsDialog`.

- Dialog đọc `GET /skus/{id}` qua `useSkuDetail` để lấy version, `locked_by` và mã vạch. Bảng đơn vị là `UnitPriceTable` ở chế độ sửa (prop `saved`), đơn vị cơ sở chỉ đọc.
- Đơn vị bị khóa hiện lý do ("Đã dùng trên chứng từ", "Đã có lịch sử giá", "Có mã vạch") và chỉ có ô "Ngừng dùng", không có nút xóa.
- Các gợi ý theo prototype:
  - "Chứng từ cũ giữ hệ số cũ (n)."
  - "Kiểm kê quét mã này sẽ cộng theo hệ số mới."
  - "Mã vạch của đơn vị này sẽ không quét được ở bán hàng và kiểm kê."
  - "Đơn vị nhập mặc định chuyển sang X." (theo đúng quy tắc của API)
- Khi lưu, dialog gửi `PUT /skus/{id}/units` với `If-Match: etagOf(version)` và luôn gửi toàn bộ bảng; giá đơn vị để trống được gửi là `null`.
- Xử lý lỗi:
  - 412: hiện toast "dữ liệu đã thay đổi" và tải lại, bản nháp vẫn giữ, lần lưu sau dùng version mới.
  - 409 `unit_in_use`: đưa đơn vị trở lại bảng ở trạng thái "Ngừng dùng" và báo lỗi ngay trên dòng đó.
  - 422: lỗi hiện theo từng dòng.
- Khi lưu thành công, làm mới cache SKU, pricing, POS, tồn kho và thẻ kho.

### Sửa giá bán

`EditPriceDialog` (mở từ Kho › Tồn kho và Nhập hàng › Danh sách hàng hóa) liệt kê giá lẻ và giá sỉ riêng của các đơn vị quy đổi đang dùng. Ô trống hiện giá tự tính làm placeholder.

- Xóa giá của một đơn vị sẽ gửi `price: null` kèm `unit_id`.
- Idempotency key của giá cơ sở không đổi; giá theo đơn vị có slot riêng `:unit-<id>`.

## Tệp đã sửa / tạo (apps/web/src)

- Tạo mới:
  - `features/catalog/containers/edit-units-dialog.tsx` cùng test
  - `features/catalog/pages/products-page.test.tsx`
  - `features/pricing/price-edit.test.ts`
  - `features/procurement/pages/products-page.test.tsx`
- Sửa:
  - catalog: `schemas.ts`, `api/queries.ts`, `api/mutations.ts`, `components/product-draft.ts`, `components/unit-price-table.tsx`, `pages/products-page.tsx`, `index.ts`
  - pricing: `price-edit.ts`, `api/mutations.ts`, `components/edit-price-dialog.tsx`
  - style: `shared/styles/globals.css` (`.ut-off`, `.ut-dim`)
- Test bổ sung trong các file có sẵn: `product-draft.test.ts`, `catalog/api/mutations.test.ts`, `save-sale-price.test.tsx`, `inventory/pages/stock-page.test.tsx`.

## Kiểm tra

- `corepack pnpm typecheck`: đạt.
- `corepack pnpm lint`: đạt.
- `corepack pnpm test`: đạt, 182 file và 1390 test.
- Không chạy dev server hay trình duyệt, nên không có tiến trình nào cần dừng.

## Lưu ý

- Quyền truy cập: web chưa có mô hình quyền (role/permission) và API của các route này cũng không kiểm tra role, nên không làm lại được cổng `can('settings')`. Hiện nút hiện trên mọi dòng goods. Đã ghi chú trong phase-04.
- `SkuListItem` không có `version`; dialog lấy version từ `GET /skus/{id}` thay vì sửa schema danh sách.
- Phần kiểm tra thủ công (AC3, AC5, AC6) để lại cho e2e ở Phase 5.

Status: DONE_WITH_CONCERNS
Summary: Đã xong dialog "Đơn vị & giá" (PUT units, If-Match, xử lý 412, unit_in_use, 422, làm mới cache) và giá theo đơn vị trong dialog Sửa giá bán (null để xóa), kèm test; typecheck, lint và test đều đạt.
Concerns: Không có cổng quyền `can('settings')` vì web và API chưa có mô hình quyền, nên nút hiện cho mọi goods. Kiểm tra thủ công AC3/AC5/AC6 chưa chạy.
