# Báo cáo Phase 3: Web – bảng đơn vị trên màn tạo hàng, đơn vị ở Kho › Tồn kho

- Plan: `plans/261020-1801-multi-unit-conversion-and-unit-prices/`
- Phase: `phase-03-web-create-form-unit-table.md` (đã đặt `status: done`, tick đủ checkbox, thêm mục "Implementation notes")
- Trạng thái: hoàn thành, chưa commit

## Kết quả

Đã làm đủ 8 yêu cầu của phase-03. Mọi màn tạo hàng (Danh mục, Nhập hàng › Danh sách hàng hóa, các bước onboarding đi qua `ProductForm`, dòng hàng mới trong phiếu đặt/phiếu nhập) đều tạo SKU với N đơn vị và giá riêng từng đơn vị bằng một lần gửi. Ở Kho › Tồn kho, dòng phụ dưới tên hàng có thêm các quy đổi đơn vị, còn "Tồn thực" và "Có thể bán" có thêm dòng "= 2 thùng 5 dây".

## Thay đổi

### Catalog (`apps/web/src/features/catalog/`)

- `components/product-draft.ts`:
  - Bỏ `bigUnitId` và `factor`. Thêm `units: UnitRow[]` và `unitRow()`.
  - Thêm các hàm `unitTableErrors`, `unitPriceBelowBase`, `autoPrice`, `unitsRequest`, `defaultPurchaseRow` và `firstProductError` (có hậu tố "và N lỗi khác"). Thông báo lấy từ v6.15 (`UNIT_MESSAGES`).
  - `productRequest` gửi `units` và không gửi `purchase_unit_id`/`factor` nữa.
  - `quickCreateRequest` gửi `units` và `purchase_unit_id`. Giá nhập được quy theo đơn vị có hệ số lớn nhất.
- `components/unit-price-table.tsx` (mới):
  - `UnitPriceTable` là component controlled. Props gồm `value`, `onChange`, `errors`, `baseUnitName` và `purchaseUnitId`.
  - `UnitPricePreview` là phần xem trước giá theo đơn vị.
  - Markup và class `.ut` lấy 1:1 từ prototype. Ô đơn vị là `Combobox` lấy dữ liệu từ `useUnits()`, có tạo mới.
  - Ô giá để trống có placeholder "<giá> tự tính". Giá lẻ của đơn vị thấp hơn giá đơn vị cơ sở chỉ hiện callout cảnh báo, không chặn lưu.
  - Nút xóa dòng có `aria-label`. Thêm dòng thì focus ô đơn vị, xóa dòng thì focus về nút "+ Thêm đơn vị".
- `components/unit-conversion-list.tsx` (mới): danh sách quy đổi dùng chung cho hai trang danh sách.
- `components/product-form.tsx`:
  - Bỏ hai ô "Đơn vị lớn / Hệ số" và thay bằng `UnitPriceTable`.
  - Giá lẻ/sỉ của đơn vị cơ sở chỉ còn nằm trong dòng cơ sở của bảng.
  - `productFormErrors` đổi đường dẫn lỗi `sku.units[1].factor` của API thành khóa `units.1.factor`.
- `containers/new-product-dialog.tsx`, `quick-product-dialog.tsx`: dialog rộng hơn, có xem trước giá. Khi có lỗi, focus nhảy tới ô lỗi đầu tiên bằng hook dùng chung `useFocusFirstInvalid`.
- `api/mutations.ts`: thêm `useCreateUnit()`, gọi `POST /units` rồi invalidate `catalogKeys.units`. Thêm `createUnitErrorMessage` (409 `name_taken`).
- `api/adapters.ts`:
  - `unitConversionLines` trả về giá của từng đơn vị (`null` nghĩa là tự tính) và cờ ngừng dùng.
  - Thêm `unitFactorLine` và `autoConvertQty`. `autoConvertQty` theo `AutoConvert.Format` của API: đơn vị lớn đứng trước, phần dư tính bằng đơn vị cơ sở, không có đuôi "(… cái)".
- `schemas.ts`: `SkuUnit` có thêm `retail_price`, `wholesale_price` và `locked_by`.
- `index.ts`: export thêm các hàm và component mới.
- `pages/products-page.tsx`: hiện cột quy đổi kiểu "1 dây = 50 cái · 24.000" hoặc "· tự tính" (chữ mờ).

### Procurement (`apps/web/src/features/procurement/`)

- `buy-lines.ts`:
  - Dòng hàng mới có thêm `units`, `showUnits`, `unitsChecked` và `wholesale`.
  - Đơn vị của dòng có thể là bất kỳ đơn vị nào trong bảng.
  - Request gửi `units` và `purchase_unit_id`, với `purchase_unit_id` là đơn vị của dòng.
  - Khi bảng đơn vị có lỗi, builder trả về `rows` để mở bảng của dòng đó và đánh dấu lỗi.
- `components/buy-preset.tsx`:
  - Bỏ select "đơn vị lớn" và ô hệ số. Thêm link "Đơn vị & giá… (N đơn vị)" với `aria-expanded`/`aria-controls`, link này mở `UnitPriceTable` ở một dòng mở rộng ngay dưới dòng hàng.
  - Select đơn vị của dòng liệt kê mọi đơn vị trong bảng.
  - Có dòng phụ "= N <đơn vị cơ sở>". Đơn giá hiện theo "/<đơn vị>".
- `containers/new-po-dialog.tsx`, `new-receipt-dialog.tsx`: khi bảng đơn vị bị từ chối thì mở bảng và focus ô lỗi đầu tiên.
- `components/purchasing-product-table.tsx`: cột quy đổi giống trang Danh mục.
- `api/adapters.ts`: bỏ `factorPositive` vì không còn dùng.

### Inventory và style

- `inventory/components/stock-table.tsx`: dòng phụ dưới tên hàng là "<mã> · ĐV cơ sở: cái · 1 dây = 50 cái · …". Dòng chính của "Tồn thực" và "Có thể bán" có thêm `CellSub` "= …". Các dòng màu, cột Giá bán và footer giữ nguyên.
- `shared/styles/globals.css`: thêm khối `.ut` theo prototype.

## Test

Test mới:

- `catalog/components/product-draft.test.ts` (17 test). Bao gồm test bắt buộc "cái 500, thùng ×500 → 250.000", chọn hệ số lớn nhất, 0/1/N đơn vị và lỗi có chỉ số.
- `catalog/api/adapters.test.ts` (7 test). Gồm các case `autoConvertQty` lấy từ API (180 → "1 thùng 8 bó", dưới hệ số nhỏ nhất, bỏ đơn vị ngừng dùng, số lượng lẻ), cùng `unitFactorLine` và `unitConversionLines`.
- `catalog/components/unit-price-table.test.tsx` (7 test).
- `inventory/components/stock-table.test.tsx`: thêm 3 test.

Test sửa (cập nhật kỳ vọng theo hợp đồng mới, không nới lỏng):

- `buy-lines.test.ts`
- `buy-preset.test.tsx`
- `new-po-dialog.test.tsx`
- `new-receipt-dialog.test.tsx`
- `barcode-units-drawer.test.tsx`
- các fixture

Test mới trong các file đó:

- mua theo đơn vị bất kỳ;
- bảng bị từ chối thì mở ra và đánh dấu lỗi;
- tạo đơn vị "lốc" ngay trong phiếu đặt.

Kết quả (chạy trong `apps/web`):

- `grep -rn 'bigUnitId' apps/web/src`: không có kết quả.
- `corepack pnpm test -- product-draft buy-lines unit-price-table adapters stock-table`: đạt. Lệnh này thực tế chạy cả bộ test, 177 file và 1360 test đều đạt.
- `corepack pnpm typecheck`: sạch.
- `corepack pnpm lint`: sạch.
- `corepack pnpm test`: 177/177 file, 1360/1360 test đạt.

## Điểm lệch so với spec và lý do

1. **Bảng đơn vị trong phiếu đặt là dòng mở rộng, không phải dialog nhỏ.** Prototype v6.15 dùng dòng mở rộng, và yêu cầu là port 1:1 theo prototype.
2. **Breakpoint xếp dọc là 860px chứ không phải 600px.** Đây là breakpoint của `.ut` trong prototype.
3. **Đơn vị mới được tạo ngay khi chọn "+ Tạo mới" trong combobox**, không đợi đến lúc lưu sản phẩm. Trong lúc chờ, dòng hiện gợi ý "Đơn vị mới, sẽ thêm vào danh mục đơn vị.". Lỗi 409 hiện ngay dưới ô.
4. **Quick-create có đơn vị nhưng không đơn vị nào có hệ số lớn hơn 1** (ví dụ chỉ có "lạng" 0.5) thì gửi đơn vị cơ sở làm `purchase_unit_id`. Nếu không gửi, API sẽ lấy đơn vị có hệ số lớn nhất và giá nhập sẽ bị quy sai.
5. **Ở phiếu đặt/phiếu nhập, `purchase_unit_id` là đơn vị của dòng**, không phải đơn vị có hệ số lớn nhất. Cách này khớp với hành vi mặc định của API sau review Phase 2: giá trên dòng chính là giá vốn tham chiếu.
6. **Danh sách hiện cả đơn vị ngừng dùng, kèm pill "Ngừng dùng"** (`unitConversionLines` trả cờ `inactive`). Dòng phụ ở Tồn kho thì chỉ lấy đơn vị đang dùng, đúng spec.
7. **`SkuUnit` có thêm `locked_by`** để khớp schema mà Phase 2 sinh ra. Phase 4 sẽ cần trường này.
8. **Dùng hook dùng chung `useFocusFirstInvalid`.** Bản tự viết trước đó đã bị bỏ để tránh trùng lặp (DRY).
9. **Sửa file ngoài danh sách**, vì đổi hợp đồng thì các file này phải theo:
   - `globals.css`;
   - `catalog/pages/products-page.tsx`, `procurement/components/purchasing-product-table.tsx`;
   - `procurement/api/adapters.ts`;
   - `procurement/components/buy-preset.tsx` (component vẽ dòng hàng mới);
   - `procurement/containers/new-receipt-dialog.tsx`;
   - các fixture (catalog, procurement, onboarding) và các test dialog liên quan.

   Có hai file mới ngoài danh sách: `unit-conversion-list.tsx` và các file test.
10. **Không sửa `procurement/schemas.ts`**, vì không cần.
11. **Không sửa `apps/api`, `docs/design` hay e2e.** `schema.d.ts` đã được Phase 2 sinh lại, phase này không đụng tới.

## Cập nhật theo thay đổi API sau review Phase 2

- Đã chạy `corepack pnpm --dir apps/web gen:api`. `schema.d.ts` không đổi so với bản trước, vì openapi-typescript không sinh `maxItems`.
- Giới hạn 20 dòng đơn vị:
  - `MAX_UNIT_ROWS = 20` và `UNIT_MESSAGES.tooMany` nằm trong `product-draft.ts`.
  - `unitTableErrors` báo lỗi khóa `units` khi bảng có quá 20 dòng.
  - Trong `UnitPriceTable`, nút "+ Thêm đơn vị" bị vô hiệu khi đã đủ 20 dòng. Nút có ghi chú (`aria-describedby`) "Đã đủ 20 đơn vị, tối đa cho một hàng hóa.". Bảng cũng hiện lỗi khóa `units`.
  - `productFormErrors` đổi `sku.units` / `units` = `too_many` thành câu tiếng Việt và đặt dưới bảng.
  - Dòng hàng mới trong phiếu đặt dùng cùng bảng và cùng `unitTableErrors`, nên cũng bị chặn ở UI.
- `purchase_unit_id` của dòng hàng mới trong phiếu đặt luôn là đơn vị cơ sở hoặc một đơn vị có trong `units`: select chỉ liệt kê các đơn vị đó, và bảng có lỗi thì bị chặn trước khi gửi. Đã sửa doc comment của `newProductOf` cho đúng hành vi mặc định mới của API.
- Test mới:
  - `catalog/api/mutations.test.ts` (2 test): map lỗi theo dòng và `too_many`.
  - `product-draft.test.ts`: 20 dòng thì hợp lệ, 21 dòng thì báo lỗi.
  - `unit-price-table.test.tsx`: nút bị vô hiệu kèm ghi chú, lỗi hiện dưới bảng, xóa một dòng thì nút bật lại.
- Kết quả: typecheck và lint sạch, `corepack pnpm test` đạt 178/178 file, 1364/1364 test.

## Chưa kiểm tra / còn mở

- Chưa chạy thử thủ công trên dev server hay trình duyệt. Chưa làm kịch bản thủ công AC2/AC6 và chưa xem cột hẹp của onboarding ở 390px. Không có tiến trình nền nào được khởi động.
- `apps/web/e2e/procurement.spec.ts:114` vẫn tìm ô "Hệ số quy đổi" đã bị bỏ, nên test e2e này sẽ hỏng. Việc sửa thuộc Phase 5.
- `prettier --check` báo khác biệt, nhưng repo không khai báo prettier và HEAD cũng không sạch theo prettier, nên bỏ qua.

Status: DONE_WITH_CONCERNS
Summary: Phase 3 đã xong. Bảng đơn vị và giá có trên mọi màn tạo hàng và trong dòng hàng mới của phiếu đặt/phiếu nhập. Kho › Tồn kho hiện quy đổi đơn vị. Đã áp dụng giới hạn 20 đơn vị. Typecheck, lint và 1364/1364 test web đều đạt.
Concerns:
- Chưa kiểm tra thủ công trên trình duyệt, kể cả onboarding ở 390px.
- e2e `procurement.spec.ts` sẽ hỏng cho đến Phase 5.
- Có các điểm lệch có chủ đích so với spec: dòng mở rộng thay cho dialog, breakpoint 860px, tạo đơn vị ngay khi chọn.
