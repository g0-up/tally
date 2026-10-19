# Review phase 17: Web tồn kho và giỏ quà

- Người review: code-reviewer
- Ngày: 2026-10-18
- Phạm vi:
  - `apps/web/src/features/inventory/**`
  - `apps/web/src/features/kits/**`
  - Doc openers trong `app-shell.tsx`
  - `e2e/inventory.spec.ts`, `e2e/gift-baskets.spec.ts`
- Đối chiếu với:
  - `apps/api/api/openapi.yaml`
  - Handler Go `apps/api/internal/{inventory,kits}`, `platform/idempotency/guard.go`
  - `shared/hooks/use-commit.ts`, `shared/lib/decimal`
- Bỏ qua `features/pos` và `features/settings` vì đang có agent khác sửa.
- Chỉ review, không sửa source.

## Kiểm tra đã chạy (trong `apps/web`)

| Lệnh | Kết quả |
|---|---|
| `corepack pnpm exec vitest run src/features/inventory src/features/kits` | 17 file, 37/37 test pass |
| `corepack pnpm exec eslint src/features/inventory src/features/kits src/app` | Sạch |
| `corepack pnpm typecheck` | **Lỗi**, nhưng ngoài phase 17 (xem dưới) |
| E2E | Không chạy. Dev report cũng nói chưa chạy vì thiếu `E2E_OWNER_PASSWORD` |

Chi tiết lỗi typecheck:
- Lỗi nằm ở `src/features/pos/offline/sync-service.test.ts(11,7)`: thiếu `markBlocked`, `recordFailure`, `listIntents`, `forgetIntent` trên `SyncStore`.
- Đây là phần agent phase 19 đang sửa, không thuộc phase 17.
- Dù vậy, hiện tại tiêu chí "typecheck xanh" chưa đạt ở mức repo.

## Đánh giá tổng quan

Phần nền khá chắc:
- Mọi lệnh ghi sổ đều gửi `Idempotency-Key` đúng như OpenAPI quy định: adjust, count post, reserve, posting, disassembly.
- If-Match được dùng đúng chỗ.
- Số lượng và tiền đi qua `dec()`/`toQty` với tối đa 4 chữ số thập phân. Không thấy float trong logic nghiệp vụ.

Rủi ro thật nằm ở ba chỗ:
1. **Luồng giỏ quà có màu**: không đóng được giỏ.
2. **Phiên bản công thức nháp bị "mồ côi"**: không còn đường thao tác nào trên UI.
3. **Các luồng nhiều bước sau khi lưu một phần**: idempotency trên client chưa khép kín.

Ngoài ra còn N+1 và lọc phía client trên dữ liệu phân trang. Test không phủ các luồng retry hay lưu một phần.

---

## Critical

Không có.

## High

### H1. Giỏ đóng sẵn có thành phần màu chưa cố định thì không bao giờ đóng được

**Vị trí:**
- `kits/containers/assembly-dialog.tsx:35-36`
- `kits/components/recipe-editor.tsx:82-84`
- `kits/view-model.ts:80-82, 109-128`

**Tình huống lỗi:**
1. Trong kitModal, chọn một hàng có tồn theo màu. Ô chọn màu (`ColorSwatchGroup`) chỉ là tùy chọn, `recipeItems` không bắt buộc màu, nên recipe lưu với `color_id` rỗng. Theo OpenAPI, đây là "màu chọn sau".
2. "Tạo lệnh" gửi `{kit_id, planned_qty, invoice_id?}`, không bao giờ gửi `colors`. `AssemblyOrderRequest.colors` dành đúng cho trường hợp "the color of each recipe component whose color the recipe leaves open".
3. Server giữ item với `ColorID = nil` (`kits/assembly.go:179-213`).
4. "Ghi nhận hoàn thành" chỉ gửi `produced_qty`, không có `lines[].color_id`. Posting vì thế tạo `batchIssue` với `ColorID: it.ColorID = nil` (`kits/assembly_posting.go:125`), không set `Uncolored`.
5. Ledger rơi vào nhánh `hasColorStock` và trả `color_required` (`inventory/ledger.go:559-561`).
6. UI hiện "Chọn màu cho thành phần chưa cố định màu.", nhưng không có chỗ nào cho chọn màu, cả trong newASM lẫn asmDone.
7. Lệnh kẹt ở trạng thái "Đang đóng" vĩnh viễn.

Thêm nữa, e2e `gift-baskets.spec.ts:29-32` chọn goods bất kỳ có `sellable >= 2`. Nếu seed trả về hàng có màu thì chính e2e sẽ đỏ ở bước hoàn thành.

**Đề xuất:**
- Cách tối thiểu: với `type === "kit"`, bắt buộc chọn màu cho mọi thành phần có `on_hand_by_color` trong `recipeItems` hoặc `validItems`. Chỉ combo mới được để màu "khách chọn tại quầy".
- Cách đầy đủ hơn: trong `AssemblyDialog`, liệt kê các item mở màu của recipe đang active, mỗi item một ô `StockColorPicker`, rồi gửi `colors: [{sku_id, color_id}]`. Đồng thời lấy preview theo màu đã chọn.
- Thêm unit test cho cả hai nhánh. Cho e2e lọc bỏ hàng có màu, hoặc chọn màu cụ thể.

### H2. Phiên bản công thức nháp của giỏ đang bán không có đường xử lý trên UI

**Vị trí:**
- `kits/components/recipe-card.tsx:36, 75-110`
- `kits/api/mutations.ts:206-216`

**Tình huống lỗi:**
1. Ở "Tạo phiên bản mới", `POST /kits/{id}/recipes` thành công.
2. Bước follow-up `:publish` thất bại. Ví dụ `checkPublishable` của server từ chối (`kits/recipe_service.go:298`), mất mạng, hoặc 412. Hoặc response của POST bị mất.
3. Người dùng đóng dialog.
4. Kit lúc này có `status = active` nhưng `latest.status = draft`.
5. Card chỉ rẽ nhánh theo `kit.status`. Vì vậy:
   - Không có nút "Phát hành" hay "Sửa nháp" cho bản nháp.
   - Bấm "Tạo phiên bản mới" thì nhận `recipe_draft_exists`. UI dịch thành "Giỏ đang có một phiên bản nháp chưa phát hành." Message gốc của server còn nói "edit or publish it", nhưng UI không có nút nào làm việc đó.
6. Card còn hiển thị `v{latest}`, thành phần và kinh tế của bản nháp dưới nhãn "Đang bán". Người dùng hiểu nhầm đây là công thức đang bán, trong khi lệnh đóng mới vẫn dùng bản active.
7. Kit bị khóa cho tới khi có người gọi API tay.

**Đề xuất:**
- Rẽ nhánh theo `kit.latest.status === "draft"`. Khi đó hiện "Phát hành phiên bản vN" (gọi `usePublishKit`) và "Sửa nháp" (dùng `PATCH /kits/{id}` với `recipe`, theo `KitPatch`: "Replaces the draft latest version").
- Ghi rõ "Đang bán vX · có nháp vN".
- Khi nhận `recipe_draft_exists`, gợi ý mở bản nháp.

## Medium

### M1. "Hoàn tất đếm" xong nhưng ghi sổ lỗi thì báo sai và retry không hoạt động

**Vị trí:** `inventory/api/mutations.ts:256-269`

**Tình huống lỗi:**
1. `:finish` thành công, count chuyển sang `pending_post` với version mới.
2. Bước `:post` (khi không có chênh lệch) thất bại: mất mạng, 423 read-only, hoặc `reauth` bị hủy.
3. `useCommit` hiện "**Chưa ghi sổ.** …", trong khi phiên đã đóng.
4. `invalidate` là function nên không có refetch (`use-commit.ts:149`). Drawer vẫn hiện "Hoàn tất đếm".
5. Bấm lại thì gửi `:finish` với If-Match cũ, nhận 412 (toast reload chung) hoặc `count_not_counting`.

**Đề xuất:**
- Đưa `:post` vào `runFollowUps` với progress theo key. Câu báo: "Đã hoàn tất đếm nhưng chưa ghi sổ: …".
- Ngay sau finish, gọi `setQueryData(inventoryKeys.count(id), finished)`.
- Khi PartialCommit, invalidate `[counts, count(id)]`.

### M2. Một `useCommit` dùng chung cho nhiều dòng nên key có thể bị dùng lại cho chứng từ khác

**Vị trí:**
- `inventory/pages/lots-page.tsx:30-31, 45-46`: `pass` và `reject` dùng chung cho mọi lô.
- `kits/pages/assembly-orders-page.tsx:21, 57`: `reserve` dùng chung cho mọi lệnh.

**Tình huống lỗi:**
1. Bấm "Đạt" cho lô A. Server commit xong nhưng response bị mất, ví dụ do timeout.
2. Key được giữ lại vì chỉ reset khi nhận 2xx.
3. Bấm "Đạt" cho lô B. Request gửi cùng key nhưng body khác.
4. Guard trả 409 `idempotency_key_reused` với câu tiếng Anh "This Idempotency-Key was already used for a different request".
5. Người dùng kẹt cho tới khi reload. Với reserve, lệnh B cũng không chốt được.

**Đề xuất:**
- Một intent cho mỗi chứng từ. Có thể gọi `resetKey()` khi id đích khác lần trước, hoặc tách thành dialog hay hook riêng cho từng lần mở, giống `AdjustmentDialog`.
- Map `idempotency_key_reused` sang tiếng Việt (xem M5).

### M3. Luồng nhiều bước lặng lẽ bỏ qua chỉnh sửa sau khi đã lưu một phần

**Vị trí:**
- `kits/api/mutations.ts:113-114` (`useCreateKit`), `151-154` (`useUpdateKitDraft`), `207-208` (`useNewRecipeVersion`)
- `inventory/api/mutations.ts:57-80` (`useSetColorPrice`)

**Tình huống lỗi:**
1. Tạo giỏ. `POST /kits` OK nhưng bước "lưu giá sỉ" lỗi, và dialog vẫn mở như thiết kế.
2. Người dùng sửa tên, công thức, mã hoặc giá lẻ rồi bấm lại.
3. Key cũ được dùng lại, nên `p.kit` đã memo và lần POST/PATCH không chạy lại.
4. Mọi chỉnh sửa header và recipe bị bỏ, nhưng toast vẫn báo "Đã tạo và phát hành …".
5. Tương tự ở color price: bước retail đã nằm trong `done` thì giá lẻ mới bị bỏ qua.

**Đề xuất:**
- Sau khi tài liệu chính đã lưu, chuyển dialog sang chế độ sửa (`kit` = bản đã lưu). Hoặc khóa các field đã ghi kèm ghi chú "Đã lưu, bấm để tiếp tục các bước còn lại".
- Nếu không làm vậy, so sánh vars với vars của lần lưu: khác thì `resetKey()` và chạy lại các bước tương ứng (PATCH draft hoặc ghi lại giá).

### M4. Dialog giá màu luôn ghim giá sỉ riêng dù chỉ sửa giá lẻ

**Vị trí:**
- `inventory/containers/color-price-dialog.tsx:45, 66, 71`
- `inventory/api/mutations.ts:71-73`

**Tình huống lỗi:**
1. Màu Đỏ chưa có giá riêng. Người dùng chỉ sửa giá lẻ thành 18.000.
2. `draft.wholesale` đã được prefill bằng `sku.wholesale_price`, nên mutation ghi luôn dòng giá sỉ riêng 12.000 cho màu Đỏ.
3. Về sau đổi giá sỉ chung thì màu Đỏ không theo nữa, và người dùng không biết.
4. Kiểm tra "không đổi" ở dòng 71 cũng luôn sai khi `own.wholesale === null`, vì `same(12000, null)` là false. Lưu lại giá cũ vẫn ghi đè.
5. Test `color-price-dialog.test.tsx:43-46` đang khẳng định đúng hành vi này, tức test đóng băng lỗi.

**Đề xuất:**
- Prefill wholesale bằng `own?.wholesale ?? null` và hiện giá chung làm placeholder.
- Chỉ ghi wholesale khi người dùng nhập giá trị khác giá hiện tại.
- Sửa test cho khớp.

### M5. Mã lỗi server chưa dịch nên hiện câu tiếng Anh

**Vị trí:**
- `inventory/api/adapters.ts:102-115`
- `kits/api/adapters.ts:85-99`

`errorMessage()` rơi về `e.message` của API, vốn là tiếng Anh. Các mã có thật phía server nhưng chưa map:

| Nhóm | Mã chưa map |
|---|---|
| Inventory (`inventory/errors.go:11-28`) | `expired_lot_blocked`, `color_required`, `lot_mismatch`, `sku_not_stockable`, `already_reversed` |
| Platform | `idempotency_key_reused` (guard), `exception_confirmation_used` (`platform/...:302`, "confirm again") |
| Kits (`kits/errors.go:13-28`) | `assembly_order_changed` (server bảo "send the request again", retry cùng key là an toàn vì key chỉ lưu khi 2xx), `assembly_order_has_postings`, `substitute_exceeds_remaining`, `combo_not_sellable` |

Ngoài ra `inventory/api/mutations.ts:262` ném `new Error("count not loaded")`, nên người dùng thấy câu tiếng Anh.

**Đề xuất:**
- Bổ sung các mã trên vào map.
- Đưa `idempotency_key_reused` và `exception_confirmation_used` vào `CODE_MESSAGES` dùng chung.

### M6. Sửa nháp hoặc tạo phiên bản mới làm mất dòng mix của nhóm mix đã ngừng

**Vị trí:**
- `kits/view-model.ts:161-163`
- `kits/api/queries.ts:207`

**Tình huống lỗi:**
1. `useMixGroups` lọc chỉ lấy nhóm `active`.
2. Khi `linesOf` dựng draft từ recipe đã lưu, dòng `mix_pack` thuộc nhóm đã inactive bị `return []`.
3. Lưu nháp gọi `PATCH recipe`, thay toàn bộ recipe. Phiên bản mới cũng không có dòng đó.
4. Dòng mix bị xóa mà không có cảnh báo.

**Đề xuất:**
- Seed bằng toàn bộ mix group (kể cả inactive) để đọc cấu trúc.
- Nếu muốn chặn nhóm inactive, hiện dòng ở trạng thái lỗi "Nhóm mix đã ngừng" và bắt người dùng xử lý. Không được bỏ lặng lẽ.

### M7. Lọc màu ở trang Tồn kho chạy trên dữ liệu đã phân trang

**Vị trí:** `inventory/pages/stock-page.tsx:39-40, 46-47`

**Tình huống lỗi:**
1. Chip màu, "Chỉ hàng có màu" và số đếm "Màu (n)" đều tính trên `list.items`, tức các trang đã tải (50 dòng mỗi trang).
2. Chọn "Đỏ" trên kho 300 mã thì chỉ thấy mã Đỏ trong 50 dòng đầu. Số đếm sai, và danh sách có thể rỗng dù còn trang sau.
3. API đã hỗ trợ `color_id`, `colored_only`, `below_min`, như spec ghi trong `GET /stock?...`.

**Đề xuất:** Đưa các filter này thành params của `useCursorList`, giữ trong URL như spec yêu cầu. Số đếm màu thì lấy từ server, hoặc bỏ số.

### M8. N+1 request trên các màn danh sách

| Vị trí | Mô tả |
|---|---|
| `inventory/api/queries.ts:144-170` | `useAdjustmentDetails` và `useCountDetails`: 1 GET cho mỗi dòng, tối đa 50 mỗi trang, cộng dồn khi "Tải thêm" |
| `inventory/api/queries.ts:218-247` | `useColorPrices`: 2 GET cho mỗi SKU có giá màu |
| `kits/api/queries.ts:55-104` | Trang công thức gọi `GET /kits/{id}`, `GET /kits/{id}/preview` và 1 GET cho mỗi phiên bản cũ của từng kit. 50 kit có 3 phiên bản là khoảng 200 request |
| `useAssemblies(ids)` | Dùng ở `assembly-orders-page.tsx:26-27`, `progress-page.tsx:179-180`, `disassembly-dialog.tsx:27-29`, `disassembly-page.tsx:224-225` |

Một phần là khoảng trống API mà dev report đã ghi, nhưng client chưa có giới hạn nào.

**Đề xuất:**
- Ngắn hạn: chỉ tải chi tiết cho dòng hiển thị hoặc dòng mở. Đặt `staleTime`.
- Dài hạn: đề nghị API trả số liệu tóm tắt trong list (số dòng, dòng lệch, thiếu hụt, giá kit), và `GET /price-lists/{id}/items?sku_id=a,b,c`.

### M9. Tháo giỏ và truy xuất lô chỉ thấy lệnh trong trang đầu

**Vị trí:**
- `kits/containers/disassembly-dialog.tsx:27-43`
- `kits/pages/progress-page.tsx:176-188`

**Tình huống lỗi:**
- Option lô giỏ được ghép qua `posting.output_lot_id` của các lệnh trong 50 lệnh mới nhất. Comment trong code cũng thừa nhận điều này.
- Lô giỏ của lệnh cũ hơn vẫn còn tồn nhưng không tháo được.
- Bảng "Truy xuất lô thành phần" không có `ListFooter`, nên lệnh cũ biến mất khỏi truy xuất.

**Đề xuất:**
- Dùng `GET /lots/{id}/genealogy`, vốn đã có trong `useGenealogies`, để lấy lệnh hoặc posting theo lô được chọn, thay vì quét danh sách lệnh.
- Thêm phân trang cho bảng truy xuất.

### M10. Link `?doc=LD-…` báo không tìm thấy với lệnh cũ dù API tìm được

**Vị trí:** `kits/pages/assembly-orders-page.tsx:18-19`

**Tình huống lỗi:**
- `useDocParam` chỉ dò trong `list.items` (trang đầu).
- `GET /assembly-orders` có `q` ("Document number or kit name", openapi.yaml:8272), và `useAssemblyList(q)` đã hỗ trợ, nhưng trang lại gọi không có `q`.
- Mở DocLink LD từ thẻ kho cho một lệnh cũ thì hiện `DocNotFoundDrawer`.
- Dev report ghi "Không có bộ lọc `doc_no` trên các danh sách", điều này sai với `/assembly-orders`.

**Đề xuất:** Khi có `?doc=`, gọi thêm `useAssemblyList(doc)` hoặc truyền `q` để resolve.

### M11. Test không phủ các luồng rủi ro

- Không có test cho các luồng retry hoặc lưu một phần:
  - `useCreateKit` khi follow-up lỗi rồi retry: không tạo kit lần hai, các bước còn lại chạy tiếp.
  - `useNewRecipeVersion` khi publish lỗi.
  - `useFinishCount` khi finish OK nhưng post lỗi.
  - `useSetColorPrice` thứ tự retail và wholesale khi hạ giá.
- Không có test cho reserve nhiều dòng (M2) và kit có màu mở (H1).
- `color-price-dialog.test.tsx:43-46` đóng băng hành vi sai ở M4.
- E2E chưa chạy lần nào. Kịch bản giỏ quà phụ thuộc seed như đã nói ở H1.
- Spec yêu cầu test `stock-table`, `count-drawer`, `lot-table`, `kit-dialog`, `assembly-complete-dialog`. Các test này có tồn tại, nhưng tổng cộng 37 test cho khoảng 10 màn hình là mỏng so với độ phức tạp.

## Low

| # | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|
| L1 | `inventory/components/stock-table.tsx:96-102` | Sub "cách ly N" cộng cả `expired`. Lô hết hạn bị gọi là cách ly. | Tách "hết hạn N", hoặc đổi nhãn thành "khóa N". |
| L2 | `inventory/api/queries.ts:238` | `Number(it.min_qty) > 1` so sánh decimal bằng float. | `dec(it.min_qty).gt(1)`. |
| L3 | `inventory/api/mutations.ts:222-224`, `shared/hooks/use-commit.ts:93-94` | Lỗi 412 ở line saver hiện hai toast: global stale và `commitError`. Lỗi 412 trong follow-up cũng thành PartialCommit kèm toast stale chung. | Bỏ qua 412 trong catch của saver. Trong `runFollowUps`, coi 412 là "handled elsewhere" hoặc dùng câu riêng. |
| L4 | `inventory/components/stock-table.tsx:188`, footer thẻ kho | Tổng "Giá trị tồn" và số dòng chỉ tính trên các trang đã tải, nhưng hiển thị như tổng toàn kho. | Ghi "(đã tải)" hoặc lấy tổng từ server. |
| L5 | `release-reservation-dialog.tsx`, `inventory/api/adapters.ts:31` | Lý do nhả giữ bắt buộc nhập nhưng không gửi lên (API không có field, đã ghi là khoảng trống). `RELEASE_REASON_KINDS` export nhưng không dùng. | Bỏ ràng buộc bắt buộc cho tới khi API có field, hoặc xóa export thừa. |
| L6 | `kits/api/mutations.ts:114, 233` | `POST /kits` và `POST /assembly-orders` không có Idempotency-Key (đúng theo OpenAPI). Mất response rồi retry thì kit báo `sku_code_taken` dù kit đã được tạo, còn lệnh đóng bị tạo trùng bản nháp. | Đề nghị API nhận key. Tạm thời, khi gặp `sku_code_taken` sau lỗi mạng thì gợi ý tải lại danh sách. |
| L7 | Dev report mục "Sai lệch" #2 | Report ghi `POST /skus/{id}/colors/{color}/price`, nhưng code thực tế gửi `POST /skus/{id}/sale-price` với `color_id`, `source: "inventory"`, `price: null`. Spec ghi `source: stock` và DELETE. Code đúng theo OpenAPI, chỉ report và spec lệch. | Sửa report. Cập nhật spec theo OpenAPI. |
| L8 | `kits/api/queries.ts:220`, `assembly-orders-page.tsx:36` | Danh sách đơn mở lấy tối đa 100 mỗi trạng thái, không phân trang. Lệnh gắn đơn đã giao thì mất nhãn "cho SP-…". | Thêm phân trang. Hoặc thêm `invoice_doc_no` vào `AssemblyOrder` (khoảng trống API). |
| L9 | `kits/containers/assembly-dialog.tsx:25-26` | Ô chọn công thức chỉ có kit trong trang đầu của `/kits`. | Dùng Combobox có tìm kiếm phía server. |

## Các phần đã kiểm và thấy ổn

- **Idempotency theo OpenAPI**:
  - Có key: adjust, count create/post, reserve, postings, disassemblies.
  - Không gửi key ở các endpoint OpenAPI không nhận.
  - Dialog mount theo mỗi lần mở nên mỗi lần có intent mới.
- **Exception binding**:
  - Adjustment: `doc_type: sku`, `doc_id: lines[0].sku_id`, `version 0`, payload là body bỏ `exception_confirmation_id`. Khớp `inventory/adjustment.go`.
  - Count post: `version = count.version`. Khớp `count.go`.
- **Decimal**:
  - `QtyInput` chặn quá 4 chữ số thập phân (`parseQty`).
  - Phép nhân trong logic dùng `toQty` (làm tròn về 4 chữ số).
  - Không có float trong tính toán nghiệp vụ, ngoại trừ L2 và `marginRatio` (chỉ để hiển thị).
- **Phân biệt màu cố định và màu chọn sau**:
  - Subrow màu chỉ hiện tồn và bán được. "giữ N" chỉ ở dòng cha (OPEN-17).
  - Gỡ giá màu bằng `price: null` đúng OpenAPI.
- **Debounce preview**: kinh tế giỏ và preview lệnh đóng đều debounce 300ms. `signal` hủy request cũ, `retry: false`, `silent: true`.
- **Giỏ nháp và phát hành**:
  - Thứ tự các bước là tạo, rồi giá sỉ và tồn tối thiểu, rồi phát hành.
  - Version của kit lấy theo recipe, không bị ảnh hưởng bởi PATCH SKU (`kits/dto.go:316`), nên If-Match của publish đúng.
- **Hạn sử dụng giỏ**: hiển thị `output_expiry` của posting. Server tính hạn bằng hạn sớm nhất của thành phần. UI không tự tính lại.
- **Doc openers**: KK/DC/LD/TG đăng ký trong shell và gỡ khi unmount.

## Việc nên làm theo thứ tự ưu tiên

1. H1: bắt buộc chọn màu cho thành phần có màu của giỏ đóng sẵn, hoặc thêm `colors` vào newASM.
2. H2: thêm nút phát hành và sửa cho phiên bản nháp của kit đang bán.
3. M1 và M3: khép kín luồng nhiều bước sau khi lưu một phần.
4. M2: reset key khi chuyển sang dòng khác.
5. M4: bỏ ghim giá sỉ khi chỉ sửa giá lẻ, và sửa test tương ứng.
6. M5: dịch các mã lỗi còn thiếu.
7. M6: không được làm mất dòng mix khi nhóm mix đã ngừng.
8. M7, M9, M10: đưa filter và tra cứu lên server.
9. M8: giảm N+1. M11: bổ sung test cho các luồng retry và màu.
10. Chạy hai spec e2e khi có `E2E_OWNER_PASSWORD`.

## Theo dõi kế hoạch

- Các mục code của phase 17 đã có đủ.
- Mục "Unit + e2e tests" chưa nên tick:
  - E2E chưa chạy.
  - Thiếu test cho các luồng rủi ro (M11).
- Typecheck toàn repo đang đỏ do `features/pos`. Cần phase 19 xử lý trước khi xác nhận tiêu chí thành công.

## Câu hỏi còn mở

1. Với giỏ đóng sẵn, sản phẩm có muốn cho phép "màu chọn khi đóng" không (tức cần picker trong newASM)? Hay mọi thành phần có màu phải cố định trong công thức? Câu trả lời quyết định cách sửa H1.
2. Spec ghi "Bỏ giá riêng" là `DELETE /price-lists/{id}/items/{itemId}` với `source: stock`. Code theo OpenAPI (`price: null`, `source: inventory`). Cần chốt để cập nhật spec.

Status: DONE_WITH_CONCERNS
Summary: Review xong phase 17. Có 2 lỗi High: giỏ đóng sẵn có thành phần màu chưa cố định thì không đóng được, và phiên bản công thức nháp của kit đang bán không có đường xử lý trên UI. Có 11 Medium, chủ yếu về idempotency và retry nhiều bước, ghim giá sỉ màu, mã lỗi chưa dịch, N+1 và lọc trên dữ liệu phân trang. Lint sạch, 37/37 unit test pass.
Concerns/Blockers: Typecheck repo đỏ ở `features/pos/offline/sync-service.test.ts` (ngoài phase 17). E2E chưa chạy. H1 cần quyết định sản phẩm (câu hỏi 1).
