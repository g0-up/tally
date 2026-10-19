# Báo cáo triển khai: Web tồn kho & giỏ quà

- Plan: `plans/261016-0854-tally-full-product-implementation/phase-17-web-inventory-kits.md`
- Trạng thái: hoàn thành phần code và unit test. Spec e2e đã viết nhưng **chưa chạy** vì thiếu `E2E_OWNER_PASSWORD`.

## Kết quả

Đã làm đủ 11/12 mục Todo của phase và đánh dấu chúng là xong. Mỗi mục đều có unit test xanh làm bằng chứng.

Mục "Unit + e2e tests" vẫn để trống. Lý do: phần unit đã xong, nhưng phần e2e mới chỉ viết, chưa chạy được.

## Lệnh đã chạy (trong `apps/web`)

| Lệnh | Kết quả |
|------|---------|
| `corepack pnpm typecheck` (tsc -b) | Sạch, không lỗi |
| `corepack pnpm lint` (eslint) | Sạch, không lỗi |
| `corepack pnpm test` (vitest) | 73 file, **439/439 pass** |
| `corepack pnpm playwright test --list` | Liệt kê được các spec mới (chỉ kiểm tra cú pháp và đăng ký test, không chạy) |

Không khởi động dev server nào, và không còn process vite/vitest/playwright nào chạy nền.

## File đã thay đổi

### Feature tồn kho: `apps/web/src/features/inventory/` (tạo mới)

- `schemas.ts`, `view-model.ts`, `use-doc-param.ts`, `use-url-filters.ts`, `doc-openers.ts`, `index.ts`
- `api/`: `adapters.ts`, `queries.ts`, `mutations.ts`
  - Chứa query keys và các thao tác ghi sổ.
  - Sau mỗi lần ghi sổ, invalidate tồn, lô, thẻ kho, dashboard và thông báo.
- `components/`: `stock-table`, `reservation-table`, `count-table`, `count-drawer`, `lot-table`, `adjustment-table`, `stock-card-*` (filters, kpis, table), `reason-field`, `doc-not-found-drawer`
- `containers/`: `adjustment-dialog`, `color-price-dialog`, `dispose-lot-dialog`, `new-count-dialog`, `release-reservation-dialog`
- `pages/`: `stock-page`, `reservations-page`, `counts-page`, `lots-page`, `adjustments-page`, `stock-card-page`
- `testing/fixtures.ts`
- Test (9 file): `stock-table`, `count-drawer`, `lot-table`, `color-price-dialog`, `stock-page`, `reservations-page`, `counts-page`, `adjustments-page`, `stock-card-page`
  - Test tiêu hủy lô hết hạn mới được thêm trong `lot-table.test.tsx`.

### Feature giỏ quà: `apps/web/src/features/kits/` (tạo mới)

- `schemas.ts`, `view-model.ts`, `doc-openers.ts`, `index.ts`
- `api/`: `adapters.ts`, `queries.ts`, `mutations.ts`, `requests.ts`
- `components/`: `recipe-card`, `recipe-editor`, `mix-line-row`, `kit-economics`, `component-table`, `assembly-table`, `shortage-table`, `demand-card`, `finished-lots-card`, `genealogy-table`, `disassembly-table`, `seed-pending-dialog`
- `containers/`: `kit-dialog`, `kit-preview-drawer`, `recipe-version-dialog`, `combo-edit-dialog`, `kit-delete-dialog`, `assembly-dialog`, `assembly-complete-dialog`, `assembly-drawer`, `disassembly-dialog`, `use-recipe-editor.ts`
- `pages/`: `recipes-page`, `assembly-orders-page`, `progress-page`, `disassembly-page`
- `testing/fixtures.ts` (có thêm `recipeEditorHandlers` dùng chung cho các test dialog công thức)
- Test (8 file, 15 test):
  - `kit-dialog`, `assembly-complete-dialog`, `recipe-version-dialog`, `combo-edit-dialog`
  - `recipes-page`: lọc nháp, phát hành, xem trước, xóa nháp
  - `assembly-orders-page`, `progress-page`, `disassembly-page`

### E2E: `apps/web/e2e/` (tạo mới, chưa chạy)

- `inventory.spec.ts` có 4 hành trình:
  - Kiểm kê mù tới xác nhận chênh lệch, rồi xem thẻ kho "Đã khớp sổ và tồn".
  - Tiêu hủy lô hết hạn.
  - Trả hàng cách ly về tồn bán được.
  - Điều chỉnh giảm.
- `gift-baskets.spec.ts` có 1 hành trình:
  - Tạo và phát hành giỏ, tạo lệnh đóng (thấy "Thiếu" khi vượt tồn).
  - Chốt và giữ nguyên liệu, rồi hoàn thành một phần.
  - Truy xuất lô, rồi tháo giỏ với bao bì hỏng.
- Cả hai theo mẫu sẵn có: `test.skip(OWNER.password === "", OWNER_PASSWORD_MISSING)` và `storageState` của owner.

### File dùng chung (chỉ thêm, không sửa logic cũ)

- `apps/web/src/app/layouts/app-shell.tsx`: đăng ký `useInventoryDocOpeners` và `useKitsDocOpeners`, để mở chứng từ KK/DC/LD/TG từ mọi màn hình.
- Không sửa `router.tsx`, `menu.ts` và `src/test/msw/handlers.ts`.
  - Route được sinh tự động từ menu qua `import.meta.glob("../features/*/pages/*-page.tsx")`.
  - Menu đã có sẵn các mục kho và giỏ quà.

## Sai lệch so với phase

1. **File phase liệt kê nhưng không thuộc quyền sở hữu của tôi**: `shared/ui/line-items/recipe-preset.tsx`, `catalog/pages/products-page.tsx`, `shared/domain/doc-no.ts`.
   - Tôi không sửa các file này.
   - Bộ soạn công thức nằm trong `kits/components/recipe-editor.tsx`.
2. **Bỏ giá riêng theo màu**: gửi `POST /skus/{id}/sale-price` với `color_id`, `source: "inventory"` và `price: null`, không gửi `DELETE`.
   - Lý do: OpenAPI định nghĩa thao tác này như vậy.
   - Spec của phase ghi `source: stock` và "sends DELETE"; test kiểm tra đúng theo hợp đồng API, nên spec cần cập nhật theo OpenAPI.
3. **EditPriceDialog**: dùng lại dialog của feature pricing, không viết bản mới.
4. **Không có trường kho**: hệ thống chỉ có một kho, API không có tham số warehouse.
5. **Drawer lệnh đóng**: các bước soạn hàng và QC đều hiển thị theo `completed_qty`, vì API không theo dõi riêng từng bước. Không có phần nhật ký (audit).
6. **Đơn vị "giỏ"**: chọn đơn vị gốc theo tên ("giỏ", "hộp" cho combo), vì API không bảo đảm có sẵn đơn vị này.
7. **Nhu cầu trên màn hình tiến độ**: hiển thị theo từng giỏ, không theo từng hóa đơn (API không trả về).
8. **Ô chọn hóa đơn khi tạo lệnh đóng**: không lọc được các hóa đơn có dòng giỏ.
9. **Xem phiên bản công thức cũ**: mỗi phiên bản cần thêm một request.
10. **E2E giỏ quà**: lập kế hoạch vượt tồn để thấy "Thiếu", sau đó đặt về 2 giỏ rồi mới tạo lệnh. Cách này tránh giữ lượng tồn lớn trong dữ liệu seed.

## Khoảng trống API (gửi chủ sở hữu `apps/api`)

### Tồn kho

- Danh sách điều chỉnh và kiểm kê không trả về các dòng.
- Không có bộ lọc `doc_no` trên các danh sách.
- Nhả giữ hàng không có trường lý do. Màn hình vẫn bắt nhập lý do, nhưng lý do không được gửi lên API.
- Giá trị chênh lệch kiểm kê phải tự tính từ `avg_cost` phía client.
- Không có số lượng khả dụng của combo.
- API bỏ qua `color_hex` của lô.

### Giỏ quà

- Danh sách kit không có giá.
- `KitRequest` không có giá sỉ và tồn tối thiểu. Client phải gọi thêm các bước sale-price, rồi `GET /skus/{id}` và `PATCH` (If-Match).
- `KitPatch` không có giá.
- Không bảo đảm có đơn vị "giỏ".
- `KitProgress` không có nhu cầu theo hóa đơn.
- Lô thành phẩm không có id lệnh đóng. Client đang ánh xạ qua `posting.output_lot_id`.
- Danh sách hóa đơn không có khách hàng và dòng hàng.
- Catalog không export query mix-group.
- Không theo dõi các bước soạn và QC của lệnh đóng.

## Câu hỏi còn mở

1. Ai sẽ chạy hai spec e2e (`inventory.spec.ts`, `gift-baskets.spec.ts`) khi có `E2E_OWNER_PASSWORD`? Sau khi chạy xanh mới nên tick mục "Unit + e2e tests".
2. Có cần đổi tiêu chí "color-price sends DELETE" trong phase cho khớp OpenAPI (`POST /skus/{id}/sale-price` với `color_id` và `price: null`), hay API sẽ thêm `DELETE`?
3. Ai xử lý các khoảng trống API ở trên, đặc biệt `KitRequest` thiếu giá sỉ và tồn tối thiểu (hiện tốn nhiều request không nguyên tử khi tạo giỏ)?

## Sửa sau review

Đã sửa theo `plans/reports/code-reviewer-261018-1110-web-inventory-kits-review.md`: hai mục High, cả 11 mục Medium và các mục Low hợp lý. Mọi thay đổi nằm trong `features/inventory/**`, `features/kits/**` và `e2e/gift-baskets.spec.ts`; không đụng `shared/**`, `features/pos/**`, `features/settings/**` hay `apps/api`.

### Kết quả kiểm tra (trong `apps/web`)

- `corepack pnpm lint`: sạch.
- `corepack pnpm typecheck`: sạch.
- `corepack pnpm test`: 76 file, 492/492 test đạt.
- E2E chưa chạy vì không có `E2E_OWNER_PASSWORD`. Hai spec vẫn theo mẫu tự bỏ qua khi thiếu biến này.

### High

- **H1 (giỏ có thành phần màu chưa cố định không đóng được)**:
  - Dialog "Tạo lệnh đóng giỏ" hiện một `ColorSwatchGroup` cho mỗi thành phần mà công thức để mở màu, chỉ đưa ra các màu còn tồn (`kits/components/open-color-fields.tsx`, `openColors` trong `kits/view-model.ts`).
  - Phải chọn đủ màu mới tạo lệnh. Cả preview lẫn lệnh tạo đều gửi `colors: [{sku_id, color_id}]`. Dialog chờ công thức và SKU tải xong trước khi cho gửi.
  - Lệnh cũ đã tạo khi chưa có màu cũng gỡ được: dialog "Hoàn tất" cho chọn màu và gửi `lines` kèm `color_id`.
  - Dialog cũng tải đủ mọi giỏ đang bán (không chỉ trang đầu), và giỏ được chọn sẵn từ thẻ vẫn hiện dù nằm ngoài trang đầu.
  - E2E `gift-baskets.spec.ts` chỉ chọn SKU không có tồn theo màu để hành trình không phụ thuộc màu.
- **H2 (bản nháp của giỏ đang bán không có đường xử lý)**:
  - Thẻ công thức nhận ra giỏ đang bán có nháp mới. Thẻ vẫn hiện tiêu đề, thành phần và kinh tế của phiên bản đang bán (lấy qua `useSellingVersions`), kèm callout "Đang bán vX · có nháp vN chưa phát hành" với các nút "Xem trước vN", "Sửa nháp", "Phát hành vN".
  - Drawer xem trước ghi rõ bản nháp và phiên bản đang bán; toast phát hành nêu số phiên bản.
  - Thông báo `recipe_draft_exists` chỉ người dùng tới bản nháp trên thẻ.
  - "Phiên bản cũ (N)" tải các phiên bản cũ khi bấm (`kits/containers/old-recipe-versions.tsx`).

### Medium

- **M1 (hoàn tất đếm xong nhưng ghi sổ lỗi)**: `useFinishCount` giữ tiến độ. Phiếu đã hoàn tất được ghi vào cache ngay, bước ghi sổ chạy qua `runFollowUps`, lỗi hiện "Đã hoàn tất đếm KK-… nhưng chưa ghi sổ: …". Thử lại chỉ ghi sổ, không hoàn tất lần nữa.
- **M2 (dùng chung một key idempotency cho nhiều dòng)**: thêm `usePerRowCommit` trong `inventory/api/mutations.ts`. Mỗi dòng có intent và key riêng; áp dụng cho đạt/loại lô và giữ hàng lệnh đóng.
- **M3 (luồng nhiều bước bỏ qua chỉnh sửa sau khi lưu một phần)**:
  - Tạo/sửa giỏ, phiên bản mới và giá theo màu nhớ giá trị đã lưu của từng bước. Một bước chỉ được bỏ qua khi giá trị đã lưu trùng với giá trị form đang yêu cầu; nếu người dùng sửa sau lần lỗi, lần thử lại gửi chỉnh sửa đó.
  - Giỏ đã được tạo thì không POST lại; Loại và Mã bị khóa kèm gợi ý.
  - Nếu phiên bản đã phát hành rồi mới sửa thành phần, dialog báo rõ thành phần chưa được lưu thay vì báo thành công.
- **M4 (luôn ghim giá sỉ riêng)**: dialog giá màu chỉ gửi giá sỉ riêng khi người dùng thực sự nhập. Test cũ khẳng định hành vi sai đã sửa lại. Khi giá lẻ mới thấp hơn giá sỉ hiện tại, giá sỉ được gửi trước.
- **M5 (mã lỗi server chưa dịch)**: thêm câu tiếng Việt cho các mã còn thiếu trong `KIT_MESSAGES` và `INVENTORY_MESSAGES`, gồm cả các mã nền tảng (`idempotency_key_reused`, `idempotency_key_in_progress`, …).
- **M6 (dòng mix của nhóm đã ngừng bị mất)**: `useMixGroups` trả về mọi nhóm. Dòng mix của nhóm đã ngừng vẫn hiện với nhãn "(đã ngừng)" và cảnh báo; lưu bị chặn với thông báo yêu cầu chọn nhóm khác hoặc xóa dòng.
- **M7 (lọc màu trên dữ liệu đã phân trang)**:
  - Trang Tồn kho gửi `color_id` và `colored_only` cho `GET /stock`.
  - Bỏ số đếm trên chip màu vì chỉ đúng với trang đã tải.
  - "Chưa gán màu" vẫn lọc phía client (API không có bộ lọc này), kèm gợi ý bấm "Xem thêm" khi còn trang.
- **M8 (N+1 request)**:
  - Chi tiết điều chỉnh, kiểm kê, giá theo màu, công thức và tháo giỏ chỉ tải cho các dòng hoặc thẻ đã cuộn tới (`useSeenIds` + `SeenMarker`, dùng IntersectionObserver).
  - Các query chi tiết có `staleTime` 30 giây.
  - Màn tiến độ vẫn cần một request chi tiết cho mỗi lệnh đã ghi sổ trên các trang đã tải, vì danh sách lệnh không trả về posting.
- **M9 (tháo giỏ và truy xuất lô chỉ thấy trang đầu)**:
  - Dialog tháo giỏ tải hết lô giỏ (`useLoadAll`). Lệnh đóng gốc được tìm qua genealogy (`order_doc_no`), rồi `GET /assembly-orders?q=`, rồi chi tiết lệnh, nên lệnh cũ ngoài trang đầu vẫn tháo được.
  - Màn tiến độ có "Xem thêm" để đọc các lệnh cũ hơn và tự tải hết lô giỏ.
- **M10 (link `?doc=` không tìm thấy lệnh cũ)**:
  - Lệnh đóng giỏ tìm theo `q` từ `?doc=`.
  - Kiểm kê, điều chỉnh và tháo giỏ tự tải thêm trang cho tới khi thấy chứng từ (API không có bộ lọc `doc_no` cho các danh sách này). Trong lúc tìm, màn hình hiện "Đang tìm …".
- **M11 (thiếu test cho luồng rủi ro)**: xem mục test bên dưới.

### Low

- **L1**: cột khả dụng tách "cách ly N" và "hết hạn N".
- **L2**: so sánh `min_qty` bằng `dec(...).gt(1)`.
- **L3**:
  - Line saver của phiếu đếm không hiện toast riêng khi lỗi 412 hay phiên hết hạn; thông báo chung của app đã lo.
  - Phần trong `runFollowUps` nằm ở `shared/hooks/use-commit.ts`, ngoài phạm vi được sửa nên chưa làm.
- **L4**: tổng số dòng và "Giá trị tồn" ghi "(đã tải)" / "trên các dòng đã tải" khi danh sách còn trang.
- **L5**: xóa export thừa `RELEASE_REASON_KINDS`. Vẫn giữ bắt buộc nhập lý do nhả giữ theo prototype; khoảng trống API đã ghi ở trên.
- **L6**: nếu `POST /kits` mất phản hồi do lỗi mạng rồi lần sau gặp `sku_code_taken`, dialog báo giỏ có thể đã được tạo và hướng dẫn tải lại danh sách. API vẫn chưa nhận Idempotency-Key cho `POST /kits` và `POST /assembly-orders`.
- **L7**: đã sửa mục "Sai lệch" #2 ở trên.
- **L8**: chưa làm. Danh sách đơn mở vẫn tối đa 100 mỗi trạng thái; cách sửa đúng là thêm `invoice_doc_no` vào `AssemblyOrder` phía API.
- **L9**: dialog tạo lệnh đóng tự tải hết các giỏ đang bán (không dùng Combobox tìm phía server).

### Test thêm hoặc sửa

- `kits/containers/assembly-dialog.test.tsx` (mới): hỏi màu cho thành phần mở màu rồi gửi `colors`; không hỏi màu khi công thức đã cố định.
- `kits/containers/assembly-complete-dialog.test.tsx`: hoàn tất lệnh cũ kèm màu chọn trong `lines`.
- `kits/pages/recipes-page.test.tsx`: giỏ đang bán có nháp mới vẫn hiện phiên bản đang bán và phát hành được nháp.
- `kits/containers/kit-dialog.test.tsx`: tạo giỏ một lần khi bước sau lỗi và gửi chỉnh sửa trước lần thử lại; báo giỏ có thể đã có khi mã bị trùng sau lỗi mạng.
- `kits/containers/recipe-version-dialog.test.tsx`: thử lại thì phát hành phiên bản đã lưu, không tạo thêm; dòng mix của nhóm đã ngừng.
- `kits/pages/assembly-orders-page.test.tsx`: mỗi lệnh có key giữ hàng riêng; mở lệnh cũ từ URL bằng số chứng từ.
- `kits/pages/disassembly-page.test.tsx`: tháo lô giỏ ngoài trang đầu, có lệnh gốc cũ hơn trang đầu.
- `kits/pages/progress-page.test.tsx`: đọc thêm lệnh cũ vào truy xuất lô.
- `inventory/containers/color-price-dialog.test.tsx`: chỉ gửi giá lẻ khi không nhập giá sỉ; hạ giá sỉ trước; giữ bước đã ghi và gửi chỉnh sửa sau lần lỗi.
- `inventory/components/count-drawer.test.tsx`: hoàn tất đếm rồi ghi sổ lỗi, thử lại chỉ ghi sổ; lỗi 412 của line saver chỉ hiện thông báo chung.
- `inventory/pages/stock-page.test.tsx`: lọc màu và "chỉ hàng có màu" gửi lên server.
- `inventory/components/stock-table.test.tsx`: tách cách ly với hết hạn, ghi chú tổng trên danh sách chưa tải hết.
- `inventory/components/seen-marker.test.tsx` (mới): báo khi dòng vào khung nhìn và khi trình duyệt không có IntersectionObserver.

### Khoảng trống API mới phát hiện

- `GET /stock` không có bộ lọc "chưa gán màu".
- Danh sách kiểm kê và điều chỉnh không có tóm tắt dòng và không lọc theo `doc_no`.
- `GET /disassemblies` không có `q`.
- Danh sách lệnh đóng không trả về posting, nên truy xuất lô vẫn cần một request mỗi lệnh.
- `POST /kits` và `POST /assembly-orders` không nhận Idempotency-Key.
- `AssemblyOrder` không có `invoice_doc_no` (liên quan L8).
