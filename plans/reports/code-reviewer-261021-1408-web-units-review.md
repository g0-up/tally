# Review web: nhiều đơn vị cho mỗi SKU, giá bán theo từng đơn vị (Phase 3 + 4)

Ngày: 2026-10-21 · Người review: code-reviewer (chỉ đọc, không sửa code)

## Phạm vi

- `git diff -- apps/web` + file chưa track trong `apps/web/src` (bỏ qua `shared/api/schema.d.ts`).
- Trọng tâm: `catalog/components/product-draft.ts`, `catalog/components/unit-price-table.tsx`,
  `catalog/containers/{new-product,quick-product,edit-units}-dialog.tsx`, `catalog/api/{mutations,adapters}.ts`,
  `procurement/{buy-lines.ts,components/buy-preset.tsx}`, `pricing/{price-edit.ts,api/mutations.ts,components/edit-price-dialog.tsx}`,
  `inventory/components/stock-table.tsx`, `shared/styles/globals.css`.
- Đối chiếu với API: `apps/api/api/openapi.yaml`, `internal/catalog/{skus.go,quick_create.go,dto.go,errors.go}`,
  `internal/inventory/queries.go` (AutoConvert.Format), `internal/procurement/dto.go`.
- Đối chiếu prototype `docs/design/prototype-v6.15.html` (khối `.ut`).

## Kết quả chạy

- `corepack pnpm typecheck` (apps/web): sạch, exit 0.
- `corepack pnpm test` (apps/web): 182 file / 1390 test đạt.
- Không chạy dev server, không kiểm tra trên trình duyệt.

## Đánh giá chung

Luồng chính đúng hợp đồng API: payload tạo hàng, quick-create, PUT `/skus/{id}/units` (If-Match theo version, null = bỏ giá riêng),
ánh xạ lỗi 409/422 về đúng ô, xử lý 412 giữ bản nháp. Test cho bảng đơn vị, dialog và draft khá đầy đủ. Còn 3 lỗi Medium ảnh hưởng
dữ liệu/hành vi thật (cache bán hàng cũ, ghi đè giá cơ sở, dòng nhập hàng tự đổi đơn vị) và một số lỗi Low về a11y/hiển thị lỗi.

## Critical

Không có.

## High

Không có.

## Medium

### M1. Không invalidate cache tra cứu SKU của Bán hàng/POS sau khi lưu đơn vị

- `apps/web/src/features/catalog/api/mutations.ts:152`
- `SKU_UNITS_KEYS` gồm `skuKeys.all`, `["pricing"]`, `posKeys.all`, `["inventory","stock"]`, `["inventory","stock-card"]`
  nhưng thiếu `["sales","lookup","sku"]` (`salesKeys.lookup("sku", id)`, `features/sales/api/queries.ts:198-208`, `staleTime` 5 phút).
  Query này cấp danh sách đơn vị cho ô chọn đơn vị ở `pos/components/cart-line-row.tsx:32-36`,
  `sales/components/line-items/item-line-row.tsx:45,65`, `sales/containers/return-dialog.tsx:99`.
- Kịch bản: thu ngân đã mở dòng "Sữa" trong giỏ POS (query đã cache). Quản lý thêm đơn vị "thùng" và ngừng dùng "lốc" rồi lưu;
  toast báo dùng được ngay. Trong 5 phút, ô đơn vị trên giỏ/báo giá vẫn không có "thùng" và vẫn cho chọn "lốc" — server sẽ từ chối
  (đơn vị ngừng dùng) hoặc người dùng tưởng chưa lưu.
- Sửa: thêm `["sales", "lookup", "sku"]` (hoặc `salesKeys.lookup("sku")` nếu có prefix helper) vào `SKU_UNITS_KEYS`; thêm 1 assert vào
  test invalidation hiện có.

### M2. PUT units luôn gửi lại giá cơ sở đã tải, ghi đè giá người khác vừa đổi

- `apps/web/src/features/catalog/components/product-draft.ts:323-333` (`skuUnitsRequest`)
- `retail_price`/`wholesale_price` của đơn vị cơ sở luôn được gửi bằng giá trị lúc mở dialog. Theo openapi `SkuUnitsRequest`,
  "prices sent here win"; còn If-Match chỉ chặn khi `version` của SKU đổi — đổi giá qua `EditPriceDialog` (Tồn kho/Nhập hàng)
  hoặc giá bán theo phiếu nhập không tăng version SKU (giá nằm ở bảng giá).
- Kịch bản: A mở "Đơn vị & giá" (giá lẻ 10.000). B đổi giá lẻ thành 12.000 từ Tồn kho. A chỉ thêm đơn vị "thùng" rồi lưu →
  PUT gửi `retail_price: "10000"` → giá lẻ quay về 10.000, không ai được báo.
- Sửa: chỉ gửi giá cơ sở khi khác `current` (gửi `null` theo hợp đồng "base null keeps"); làm tương tự cho giá từng đơn vị nếu
  API hỗ trợ "giữ nguyên", nếu không thì ghi rõ giới hạn trong spec. Thêm test: sửa chỉ đơn vị → request có `retail_price: null`.

### M3. Dòng nhập hàng mới lặng lẽ đổi về đơn vị cơ sở khi xoá đơn vị đang dùng

- `apps/web/src/features/procurement/components/buy-preset.tsx:195-198` (`patchTable`)
- `unitId = was === undefined ? null : (units.find((u) => u.key === was.key)?.unitId ?? null)`: khi dòng đơn vị mà dòng nhập đang
  dùng bị xoá khỏi bảng con, `unitId` về `null` (đơn vị cơ sở) nhưng `qty` và đơn giá giữ nguyên.
- Kịch bản: hàng mới "Bia", dòng nhập 10 × thùng (1 thùng = 24 lon), giá 240.000/thùng. Người dùng xoá dòng "thùng" trong bảng
  đơn vị (định đặt lại) → dòng thành 10 lon × 240.000 → tồn nhập 10 lon thay vì 240, giá vốn sai 24 lần; không có cảnh báo.
- Sửa: chặn xoá đơn vị đang là đơn vị của dòng (hoặc hiện cảnh báo/toast rõ ràng), hoặc quy đổi qty/giá theo hệ số khi về đơn vị
  cơ sở. Thêm test cho trường hợp này.

## Low

### L1. Lỗi theo chỉ số dòng không bị xoá/dời khi bỏ dòng, hiện sai dòng

- `apps/web/src/features/catalog/containers/edit-units-dialog.tsx:90` (`units.${i}.unit`), cùng cơ chế với lỗi server ánh xạ về
  `units.<i>.*` ở `new-product-dialog.tsx:42` và `quick-product-dialog.tsx:47`; `unit-price-table.tsx:145-147` (`remove`) chỉ xoá
  `createErrors[row.key]`, không đụng `errors`.
- Kịch bản: lưu báo lỗi ở dòng 2 (`units.0.factor`). Người dùng xoá dòng 2 → dòng 3 cũ trở thành chỉ số 0 và hiện lỗi của dòng đã xoá,
  với `aria-invalid` sai.
- Sửa: khoá lỗi theo `row.key` (như `createErrors`), hoặc xoá các khoá `units.*` khi bảng thay đổi cấu trúc.

### L2. Lỗi giá theo đơn vị trong EditPriceDialog chỉ hiện bằng toast; cache bán hàng không làm mới

- `apps/web/src/features/pricing/components/edit-price-dialog.tsx` (lỗi đơn vị chỉ qua toast, ô không có `aria-invalid`/`aria-describedby`);
  `apps/web/src/features/pricing/api/mutations.ts:116` chỉ invalidate `[skuKeys.all, pricingKeys.all]`.
- Kịch bản: nhập giá thùng sai → chỉ có toast, người dùng trình đọc màn hình không biết ô nào; giá đơn vị vừa sửa chưa hiện ở POS
  trong tối đa 5 phút (lookup bán hàng). Phần giá cơ sở là lỗi có sẵn, nay lan sang giá đơn vị.
- Sửa: gắn lỗi theo ô như `UnitPriceTable` (`described()`); thêm `posKeys.all` và `["sales","lookup","sku"]` vào invalidate.

### L3. A11y trong bảng đơn vị

- `apps/web/src/features/catalog/components/unit-price-table.tsx:208-213`: dòng bị khoá hiển thị lỗi `err(\`${at}unit\`)` (vd. lỗi
  "đã dùng, không xóa được") nhưng không control nào trỏ `aria-describedby` tới nó; checkbox "Ngừng dùng" (l.270) không có
  `aria-invalid`/`aria-describedby` → trình đọc màn hình không đọc lỗi. Sửa: truyền `described(\`${at}unit\`)` vào checkbox của dòng khoá.
- `unit-price-table.tsx:234`: `aria-label="Hệ số: 1 X bằng bao nhiêu Y"` không chứa nhãn hiển thị của cột ("Quy đổi") → Voice Control
  "click Quy đổi" không trúng (WCAG 2.5.3). Sửa: bắt đầu aria-label bằng chữ nhãn hiển thị, vd. "Quy đổi: 1 X = ? Y".

### L4. CSS: vị trí rule và sticky ở dòng mở rộng (chưa kiểm tra trên trình duyệt)

- `apps/web/src/shared/styles/globals.css:297`: rule `.bp .ut-t ...:last-child { position: static }` nằm giữa khối port prototype trong
  khi đầu file quy ước phần bổ sung của React đặt sau marker (l.356). Chỉ là vị trí, chuyển xuống dưới marker.
- `globals.css:274,276`: `.bp .t td:last-child` (sticky, `box-shadow` viền trái) và `.bp .t tr.bp-new td:last-child` (nền `--warn-bg`)
  cũng khớp ô `colSpan` duy nhất của dòng mở rộng `<tr className="bp-new">` (`buy-preset.tsx`). Có thể tạo viền trái/nền vàng cho cả
  khung bảng đơn vị và sticky lạ khi cuộn ngang ở 390px. Sửa: dùng class riêng cho dòng mở rộng (vd. `bp-units`) hoặc loại trừ
  `td[colspan]`; kiểm tra Playwright ở 390px.

### L5. `autoConvertQty` lệch nhỏ so với `AutoConvert.Format` của API

- `apps/web/src/features/catalog/api/adapters.ts:71-81`: trả `null` khi qty âm (API vẫn format phần trị tuyệt đối); thứ tự khi hai
  đơn vị cùng hệ số (`localeCompare("vi")` vs `ORDER BY name` của SQL) có thể khác. Đúng theo spec phase 4 nhưng sub-line ở Tồn kho
  và Thẻ kho có thể khác nhau với tồn âm. Sửa nếu muốn nhất quán: xử lý qty âm như API; hoặc ghi rõ trong spec.

### L6. Gợi ý "Đơn vị nhập mặc định chuyển sang X" chỉ hiện khi ngừng dùng, không hiện khi xoá

- `apps/web/src/features/catalog/components/unit-price-table.tsx:242`: khi xoá (chưa khoá) đơn vị đang là đơn vị nhập mặc định, server
  vẫn chuyển mặc định (`movePurchaseDefault`) nhưng người dùng không được báo; chỉ có toast "Đã bỏ đơn vị". Sửa: thêm câu này vào toast xoá
  khi `row.unitId === purchaseUnitId`.

### L7. Tạo đơn vị mới đang chờ có thể ghi đè đơn vị người dùng chọn lại

- `apps/web/src/features/catalog/components/unit-price-table.tsx:118-128` (`createRowUnit`): callback chỉ kiểm tra dòng còn tồn tại.
  Nếu trong lúc POST `/units` đang chờ, người dùng chọn đơn vị khác cho cùng dòng, kết quả POST đến sau sẽ ghi đè lựa chọn đó.
  Sửa: chỉ áp dụng khi dòng vẫn ở trạng thái chờ (`unitId === null && unitName === name`).

## Đã kiểm tra, không phải lỗi

- Schema zod `SkuUnit`: API luôn phát `locked_by`, `retail_price`, `wholesale_price` (`dto.go:256-272`), parse không vỡ với dữ liệu cũ.
- Đường dẫn lỗi: `units[<uuid>]` (errUnitsLocked) và `sku.units.<i>.*` / `units.<i>.*` (planUnits có/không prefix) được ánh xạ đúng ô;
  chỉ số request khớp chỉ số dòng vì dòng chưa có `unitId`/`factor` bị chặn ở validate trước khi gửi.
- 409 `unit_in_use` → dòng chuyển "Ngừng dùng" + lỗi; 412 → giữ bản nháp, lần lưu sau mang version mới (có test).
- `purchaseRowAfterSave` khớp `movePurchaseDefault` (hệ số giảm dần, rồi tên).
- `unitFactorLine`/`autoConvertQty` khớp API và prototype với qty ≥ 0.
- `quickCreateRequest` quy đổi giá vốn theo đơn vị nhập đúng.
- Giới hạn 20 dòng (`MAX_UNIT_ROWS`) khớp openapi; nút thêm bị disable có `aria-describedby`.
- null = bỏ giá riêng: đúng ở PUT units và ở sale-price (`unit_id` + `price: null`); slot idempotency `:unit-<id>` tách theo đơn vị.
- Payload nhập hàng hàng mới (`units`, `purchase_unit_id`, `wholesale_price`) được `newProductRequest` (`procurement/dto.go:80-91`) nhận.
- CSS `.ut*` giống prototype dòng 444-469, chỉ dùng token có trong `:root`; mục tiêu chạm 44px dưới 860px/coarse do rule chung
  (`globals.css:577-590`).
- Nhãn: mọi input có `aria-label`, nút xoá có tên "Xóa đơn vị X", `invalid` + `aria-describedby` được chuyển tiếp xuống input.
- `colSpan` dòng mở rộng (9 khi nhận hàng, 8 khi không) khớp số cột.
- Onboarding dùng `QuickProductDialog` nên hưởng cùng thay đổi; EditPriceDialog từ Tồn kho và Nhập hàng vẫn dùng chung luồng.

## Test còn thiếu

- Invalidate `["sales","lookup","sku"]` sau khi lưu đơn vị (M1).
- Request PUT khi chỉ sửa đơn vị, giá cơ sở không đổi (M2).
- Xoá đơn vị đang dùng ở dòng nhập hàng mới (M3).
- Xoá dòng khi đang có lỗi theo chỉ số (L1).
- Đã biết: `e2e/procurement.spec.ts:114` hỏng, thuộc Phase 5.

## Việc nên làm (theo thứ tự)

1. M1: thêm key lookup bán hàng vào `SKU_UNITS_KEYS`.
2. M2: gửi `null` cho giá cơ sở không đổi.
3. M3: chặn hoặc cảnh báo khi xoá đơn vị đang dùng ở dòng nhập.
4. L1, L3: khoá lỗi theo `row.key`; nối `aria-describedby` cho dòng khoá.
5. Còn lại khi tiện.

Status: DONE_WITH_CONCERNS
Summary: Typecheck sạch, 1390/1390 test đạt; không có Critical/High, có 3 lỗi Medium: thiếu invalidate lookup SKU của bán hàng, PUT units ghi đè giá cơ sở bằng giá cũ, dòng nhập hàng mới lặng lẽ về đơn vị cơ sở khi xoá đơn vị.
Concerns/Blockers: Chưa kiểm tra layout trên trình duyệt (sticky `.bp td:last-child` ở dòng mở rộng, 390px).

## Xử lý của lead (2026-10-21)

- M1: đã sửa. `SKU_UNITS_KEYS` thêm `["sales","lookup","sku"]`; test của dialog kiểm key này.
- M2: đã sửa. `skuUnitsRequest(t, started)` gửi null cho giá cơ sở không đổi so với lúc mở dialog (API hiểu null là giữ nguyên). Giá theo đơn vị vẫn có race như đã ghi trong `openapi.yaml`, vì ở đó null nghĩa là xoá giá riêng.
- M3: đã sửa. `withNewProductUnits` (`buy-lines.ts`): xoá đơn vị mà dòng đang dùng thì dòng về đơn vị cơ sở, số lượng × hệ số, giá ÷ hệ số. Đã có test.
- L1: đã sửa. Bảng ghi lại thứ tự khoá dòng tại thời điểm lỗi được tìm ra, nên lỗi đi theo đúng dòng. Đã có test.
- L2: đã sửa phần làm mới cache (`["pos"]`, `["sales","lookup","sku"]`). Phần lỗi theo ô trong EditPriceDialog giữ toast như phần giá cơ sở hiện có.
- L3: đã sửa. Nhãn đổi thành "Quy đổi: 1 X bằng bao nhiêu Y"; checkbox của dòng khoá có `aria-invalid` và `aria-describedby` khi có lỗi.
- L4: đã chuyển rule xuống dưới marker. Sticky và nền vàng ở dòng mở rộng giống prototype (`buyEditor` dòng `ut-row` dùng nền `--warn-bg` trong cùng `.bp .t`), nên giữ nguyên.
- L5, L6: giữ nguyên. L5 đúng spec, và tồn âm bị sổ kho chặn khi bán. L6 giống prototype (`A.utDel` chỉ hiện toast).
- L7: đã sửa. Kết quả tạo đơn vị chỉ áp dụng khi dòng vẫn đang chờ đúng tên đó.
