# Báo cáo: Web danh mục & mua hàng

Ngày: 2026-10-18 (Asia/Saigon)
Trạng thái: **BLOCKED**. Code đã viết xong nhưng chưa chạy được gate nào. Bash lỗi E2BIG ở mọi agent trong phiên, do file session-env phình ra.

## 1. Kết quả

- Đã viết code và test cho mọi màn hình trong phạm vi: hàng hóa, nhà cung cấp, bảng giá, nhóm mix vị, đơn nhập, danh sách hàng hóa mua, nhận hàng, trả NCC.
- Chưa có lệnh nào chạy được trong phần cuối phiên: typecheck, lint, vitest, build, Playwright, Go test, gen:api.
- Vì vậy **chưa tick todo nào** trong file phase, và frontmatter giữ nguyên.

## 2. File đã thay đổi (web)

Danh sách đầy đủ cần xác nhận bằng `git status --short apps/web`, vì shell không chạy được để liệt kê. Những file biết chắc:

**Feature**
- `apps/web/src/features/procurement/**`:
  - `index.ts` export `useProcurementDocOpeners`;
  - `doc-openers`, `api/{queries,mutations,adapters}`, `schemas`, `buy-lines`;
  - `components/{buy-preset,po-table,po-drawer,doc-not-found-drawer,supplier-field,…}`;
  - `containers/{new-po-dialog,new-receipt-dialog,po-advance-dialog,po-close-dialog,…}`;
  - `pages/{orders,products,receipts,supplier-returns}-page.tsx` và các test đi kèm, trong đó có `pages/orders-page.test.tsx`.
- `apps/web/src/features/{catalog,partners,pricing}/**`:
  - các trang `products`, `mix-groups`, `suppliers`, `price-lists`;
  - các dialog (newSKU/QuickProductDialog, supModal, SupplierQuickDialog, mixModal, EditPriceDialog, PriceHistoryTable) và test.

**File dùng chung**
- `apps/web/src/app/layouts/app-shell.tsx`: một dòng gọi `useProcurementDocOpeners()`. Đã báo phase09-sales trước khi sửa.

**E2E**
- `apps/web/e2e/support/real-api.ts`: thêm `apiGet<T>(page, path)`.
- `apps/web/e2e/procurement.spec.ts` (mới), luồng mua hàng:
  1. tạo PO kèm NCC mới và hàng mới, chọn "Lưu & chốt đơn";
  2. kiểm tra NCC bị khóa "Ngừng" khi còn PO mở;
  3. nhận 26 bị chặn vì vượt dung sai 5%;
  4. nhận 12 có lô và hạn, kiểm tra tồn và nợ NCC qua API;
  5. trả NCC 2;
  6. đóng phần còn thiếu, sau đó "Ngừng" mở lại.
- `apps/web/e2e/catalog.spec.ts` (mới), vòng đời nhóm mix vị: tạo, sửa, ngừng dùng, dùng lại, xóa.

## 3. Thay đổi backend

Các thay đổi Go được làm trước khi ngữ cảnh bị nén, nên danh sách chính xác **phải xác nhận bằng `git diff --stat -- apps/api`**. Theo các tin nhắn phối hợp, gồm:

- **procurement:** `spec_text` (quy cách, tùy chọn) trên dòng PO và dòng phiếu nhập; `lot_no` trên dòng phiếu trả NCC (`PurchaseReturn` lines).
- **catalog:** bộ lọc và field liên quan nhóm mix trên danh sách SKU (`in_mix_group`).
- **`apps/api/api/openapi.yaml`:** chỉ sửa các path của procurement và catalog nói trên.

Ngoài ra:
- **Migration:** không dùng số 000042, số này thuộc kits (`000042_cogs_adjustment_disassembly_source`). Nếu diff có migration của tôi, nó phải mang số 000043 trở lên.
- **`schema.d.ts`:** chưa sinh lại cho các type mới (`lot_no` trên dòng trả NCC, `spec_text`), cần chạy `pnpm gen:api`. Tôi không sửa tay file generated.

## 4. Khác với file phase

1. **Nhận vượt dung sai 5%:** chặn ngay ở client với thông báo "`{tên}: nhận {sl} vượt dung sai 5% so với còn chờ {còn}. Cần duyệt nhận vượt.`". Không có dialog ngoại lệ để duyệt nhận vượt như success criteria mô tả.
2. **Giá bán sửa trên dòng mua:** được ghi bằng các lệnh set price riêng sau khi chứng từ đã lưu, nên không atomic với chứng từ.
3. **Phí mua (vận chuyển):** ghi bằng landed cost, phân bổ `by_value`.
4. **Hóa đơn NCC** đi qua `/supplier-documents`. **Ứng trước cho PO** đi qua `POST /payments` với `purpose=advance`, `source_type=purchase`.
5. **Lý do đóng thiếu:** là ô chọn trong danh sách reason `close_short`. Dữ liệu seed chưa có reason loại này.
6. **Số lượng trả NCC:** tính theo đơn vị cơ bản. Nhận hàng theo PO cũng đếm theo đơn vị cơ bản, vì các dòng cố định theo đơn và đơn giá tính trên đơn vị cơ bản.

## 5. Phần UI còn thiếu so với prototype

- Danh sách không có cột Kho, và không ẩn giá vốn theo quyền.
- Drawer PO không hiện người tạo và mục "Lịch sử". Drawer phiếu nhập không có audit.
- Không xóa được tệp đính kèm, vì API không có route DELETE.
- Chưa đăng ký opener cho mã PC.
- Ô nhà cung cấp của PO mới để trống, trừ khi mở bằng `?supplier=`.

## 6. Ghi chú triển khai

- **Điều hướng:**
  - drawer mở bằng `?doc=<số chứng từ>`; số không tồn tại thì hiện "Không tìm thấy {doc}." (câu chữ mới, không có trong prototype);
  - `?supplier=<id>` mở PO mới cho NCC đó;
  - opener PO/PN/XT đăng ký qua `useProcurementDocOpeners`.
- **Trang hàng hóa mua:** lọc ở phía client.
- **Hàng mới:** QuickProductDialog của catalog được dùng lại cho "+ Hàng hóa mới".
- **Line items:** chưa có base dùng chung `shared/ui/line-items/line-items-editor.tsx`. Editor dòng mua nằm riêng ở `features/procurement/components/buy-preset.tsx`, kèm logic ở `features/procurement/buy-lines.ts`. Đã báo phase16-web để nó giữ preset bán của mình tự chứa. Tách base dùng chung là việc tiếp theo nếu lead vẫn muốn.
- **E2E:** tự skip khi thiếu `E2E_OWNER_PASSWORD`. Các bước cần reason `purchase_return` hoặc `close_short` chỉ chạy khi seed có reason đó; nếu không có thì ghi annotation "skipped step".
- **Tiến trình:** không khởi động dev server hay tiến trình nền nào, nên không có gì phải dọn.

## 7. Lệnh cần chạy khi shell hoạt động lại

Chạy từ `/home/cesc/Documents/personal-workspace/tally`:

```sh
git status --short apps/web apps/api
git diff --stat -- apps/api
pnpm -C apps/web gen:api && git diff --stat -- apps/web/src/shared/api/schema.d.ts
pnpm -C apps/web typecheck
pnpm -C apps/web lint
pnpm -C apps/web test
pnpm -C apps/web build
# E2E: cần stack local và E2E_OWNER_PASSWORD
pnpm -C apps/web exec playwright test e2e/catalog.spec.ts e2e/procurement.spec.ts --project=chromium
# Backend (nếu git diff cho thấy có thay đổi)
cd apps/api && gofmt -l ./internal && go vet ./... && go vet -tags integration ./...
cd apps/api && golangci-lint run
cd apps/api && go test -race ./internal/procurement/... ./internal/catalog/... ./migrations/...
cd apps/api && go test -race -tags integration ./internal/app/ -run 'Procurement|Catalog|MixGroup|TestSalesDocumentLifecycleOverHTTP'
```

Lỗi nào phát sinh thì gửi lại cho tôi để sửa. Sau khi các gate xanh mới tick todo trong file phase.

---

Status: BLOCKED
Summary: Code và test của phần web danh mục và mua hàng đã viết xong, nhưng chưa chạy được typecheck, lint, test, build, e2e hay Go test vì Bash lỗi E2BIG trong toàn phiên.
Concerns/Blockers:
- Cần shell hoạt động để chạy các gate ở mục 7.
- Danh sách thay đổi backend phải xác nhận bằng `git diff`.
- `schema.d.ts` cần chạy `gen:api`.
- Nhận vượt dung sai đang bị chặn ở client, chưa có dialog duyệt.
- Chưa có base line-items dùng chung.
