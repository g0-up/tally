# Báo cáo: e2e và parity cho hàng nhiều đơn vị

Phạm vi là Yêu cầu 1 và 3 của `plans/261020-1801-multi-unit-conversion-and-unit-prices/phase-05-tests-parity-docs.md`. Tôi chỉ sửa trong `apps/web/e2e/**`, không sửa `src` và không commit. Typecheck, lint và `playwright test --list` đều chạy sạch. Các spec chưa chạy với stack thật vì chưa có `E2E_OWNER_PASSWORD`.

## File đã sửa

| File | Thay đổi |
|------|----------|
| `apps/web/e2e/support/units-data.ts` (mới) | Helper dữ liệu nhiều đơn vị đi qua API thật. `createCandy` tạo sản phẩm có cái 500, dây = 50 cái giá 24.000, thùng = 500 cái để trống giá. Ngoài ra có `candyUnits`, `skuByCode`, `unitOf`, `quotedPrice`, `receiveInUnits` (gửi kèm `Idempotency-Key`) và `pickUnit` cho combobox đơn vị. |
| `apps/web/e2e/support/real-api.ts` | `apiPost` nhận thêm tham số header, dùng cho `Idempotency-Key`. |
| `apps/web/e2e/catalog.spec.ts` | Ba journey mới. AC2: tạo "Kẹo X" bằng UI, rồi kiểm tra đơn vị và giá qua API và quote, dây ra 24.000 và thùng ra 250.000. AC3: trong "Đơn vị & giá", xóa giá dây rồi lưu; quote kế tiếp ra 25.000. AC4: đơn vị trùng và hệ số 0 hiện lỗi dưới đúng ô, qua `aria-describedby`; dialog vẫn mở và không có POST `/products` nào. |
| `apps/web/e2e/procurement.spec.ts` | Sửa assertion cũ dùng "Hệ số quy đổi": nay kiểm tra select đơn vị cơ sở "hộp", đơn vị tính "thùng", và dòng hiện "= 24 hộp". Thêm journey PO có hàng mới với bảng đơn vị (cái, thùng 500, dây 50), đặt 2 thùng, nhận đủ. Tồn kho tăng 1.000 cái; API trả về thùng là đơn vị nhập mặc định. |
| `apps/web/e2e/pos.spec.ts` | Bán 1 dây ra 24.000 ₫ và 1 thùng ra 250.000 ₫, tổng 274.000. Checkout gửi đúng `unit_id`, tồn giảm 550 cái. |
| `apps/web/e2e/inventory.spec.ts` | AC6, kiểm tra theo thứ tự: (1) Tồn kho hiện dòng đơn vị và "= 2 thùng 5 dây" dưới 1.250; (2) Thẻ kho có "Theo dây (50 cái)" và "Theo thùng (500 cái)"; (3) thùng đã có chứng từ nên chỉ có "Ngừng dùng", không có nút xóa; (4) sau "Ngừng dùng" thùng, cả hai màn không còn thùng; (5) "Sửa giá" từ Tồn kho vẫn lưu được và giá riêng của dây được giữ. |
| `apps/web/e2e/visual/parity-states.ts` | Thêm ba state ở mục bên dưới. `requires` có thêm `E2E_MULTI_UNIT_SKU_CODE`. |
| `apps/web/e2e/a11y/axe.spec.ts` | Thêm kiểm tra cho các dialog `danh-muc.hang-hoa.*`. Mọi nút "×" trong bảng "Đơn vị tính & giá bán" phải có tên `Xóa đơn vị …`, và dialog tạo mới phải có ít nhất một nút như vậy. |

Touch-target (`touch-targets.spec.ts`) và axe tự phủ ba state mới vì cả hai lặp qua `PARITY_STATES`. CSS hiện tại đặt `.ut-x` tối thiểu 44px ở màn hẹp và 32px ở desktop, đủ ngưỡng 44/24.

## Parity states mới (1440 và 390)

| id | App | Prototype v6.15 |
|----|-----|-----------------|
| `danh-muc.hang-hoa.new-product-dialog` | `/catalog/products`, bấm "Thêm hàng hóa", chờ bảng đơn vị hiện ra | `A.newSKU()` |
| `danh-muc.hang-hoa.units-dialog` | `/catalog/products`, tìm theo `E2E_MULTI_UNIT_SKU_CODE`, mở "Đơn vị & giá" của dòng đó | `UI.f.hq='TH-CD'; render(); A.editUnits('TH-CD')` |
| `kho.ton.multi-unit` | `/inventory/stock?q=<E2E_MULTI_UNIT_SKU_CODE>` | `UI.f.tq='TH-CD'; render()` |

Hai state sau cần một SKU nhiều đơn vị có sẵn. Cửa hàng dev cua-hang-a được seed bằng `seedload` (40 SKU, mỗi SKU một đơn vị). Danh mục demo có TH-CD (hộp, dây 4, thùng 48) chỉ được seed cho tenant sandbox. Vì vậy hai state này dùng `requires: "E2E_MULTI_UNIT_SKU_CODE"` và tự skip khi biến này chưa đặt. Đây là cùng cơ chế với `E2E_SALES_DOC_ID`.

Không có snapshot nào được tạo. Cả ba state đều map sang prototype, nên ảnh prototype được chụp lại mỗi lần chạy và không cần commit baseline.

## Lệnh chạy (từ `apps/web`, khi stack `make dev` đang chạy)

```sh
# Journeys với API thật (AC2, AC3, AC4, AC6, PO, POS)
E2E_OWNER_PASSWORD='…' corepack pnpm exec playwright test --project=chromium \
  e2e/catalog.spec.ts e2e/procurement.spec.ts e2e/pos.spec.ts e2e/inventory.spec.ts

# Axe, tên nút xóa đơn vị, touch targets 44/24px
E2E_OWNER_PASSWORD='…' E2E_MULTI_UNIT_SKU_CODE='TH-CD' corepack pnpm exec playwright test --project=a11y \
  e2e/a11y -g "danh-muc.hang-hoa|kho.ton.multi-unit"

# Parity 1440 và 390 với prototype v6.15
E2E_OWNER_PASSWORD='…' E2E_MULTI_UNIT_SKU_CODE='TH-CD' corepack pnpm exec playwright test --project=visual \
  e2e/visual/parity.spec.ts -g "danh-muc.hang-hoa|kho.ton.multi-unit"
```

Nếu muốn chạy cả mobile, thêm `--project=mobile-390` vào lệnh đầu. Bỏ `-g` để chạy toàn bộ.

## Kiểm tra đã chạy

- `corepack pnpm typecheck` (`tsc -b`, có `e2e/**/*.ts` qua `tsconfig.node.json`): pass.
- `corepack pnpm lint` (`eslint .`): pass.
- `corepack pnpm exec playwright test --list` cho catalog, procurement, pos, inventory, a11y và visual liệt kê 409 test trong 10 file, không có lỗi cú pháp hay import.

Status: DONE_WITH_CONCERNS
Summary: Đã viết đủ các journey e2e cho AC2, AC3, AC4 và AC6, PO nhận hàng theo thùng, POS bán theo dây và thùng, ba parity state mới, và kiểm tra tên nút xóa đơn vị. Typecheck, lint và `--list` đều pass; chưa chạy với stack thật.
Concerns:
- Chưa chạy Playwright với stack thật vì thiếu `E2E_OWNER_PASSWORD`. Selector được dựng theo source hiện tại (tên ARIA, placeholder, `aria-describedby`) nhưng chưa được xác nhận trên trình duyệt.
- `danh-muc.hang-hoa.units-dialog` và `kho.ton.multi-unit` cần `E2E_MULTI_UNIT_SKU_CODE`. cua-hang-a không có TH-CD, nên khi biến chưa đặt hai state này bị skip. Để parity pixel khớp, SKU đó phải là TH-CD với dữ liệu giống prototype (hộp, dây 4, thùng 48, cùng lượng tồn), ví dụ một tenant sandbox có danh mục demo. Nếu muốn chạy được trên cua-hang-a, cần seed thêm một SKU nhiều đơn vị vào `seed_dev`; việc này ngoài phạm vi `e2e/**`.
- Mỗi lần chạy, các journey tạo một sản phẩm "Kẹo e2e …" mới qua API, và một NCC e2e nếu cửa hàng chưa có. Dữ liệu dev sẽ tích lũy dần, giống các journey hiện có.
- Có thể là lỗi sẵn có: `POST /receipts` và `POST /stock-adjustments` bắt buộc `Idempotency-Key`. `acceptance/at-01-supplier-receipts-advance.spec.ts` (dòng 66) và `addStockApi` gọi các endpoint này mà không gửi header đó. Tôi không sửa vì ngoài phạm vi được giao.
- Phase file chưa được đánh dấu hoàn thành; việc này để lead cập nhật.
