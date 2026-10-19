# Review Phase 11: Bộ quà, combo và lắp ráp

- Ngày: 2026-10-18 (Asia/Saigon)
- Người review: code-reviewer
- Base: `6cf630b`. Toàn bộ thay đổi chưa commit.
- Plan: `plans/261016-0854-tally-full-product-implementation/phase-11-kits-assembly.md`
- Báo cáo của người làm: `plans/reports/fullstack-developer-261018-kits-assembly.md`

## Phạm vi

Đã review các phần sau:

- `apps/api/internal/kits/**`, gồm `assembly.go`, `assembly_posting.go`, `disassembly.go`, `recipe_service.go`, `recipe_build.go`, `repo.go`, `verify.go`, `http.go` và `combo.go`.
- Wiring:
  - `internal/app/kits_ports.go`, `domains.go`, `app.go`, `routes.go`;
  - `internal/cli/serve.go`, `ledger.go`.
- Tích hợp `ComboExpander` với sales: `sales/ports.go`, `sales/pos.go`, `sales/pos_bootstrap.go`, `sales/lines.go`.
- Thay đổi ở catalog: `catalog/products.go` (kind kit/combo, ẩn hàng nháp, các hàm `*In`) và `catalog/mix_groups.go`.
- Phần kits trong `apps/api/api/openapi.yaml` (gồm `component_colors` trong `SalesLineInput`).
- `internal/app/kits_integration_test.go` (chỉ đọc).

Tôi bỏ qua phần catalog và procurement mà agent phase15-web đang sửa song song.

**Giới hạn quan trọng:** Bash trong phiên review này hỏng hoàn toàn. Mọi lệnh đều lỗi `E2BIG` khi khởi động zsh vì command line 164 KB. Vì vậy tôi **không chạy được** `go vet`, `go test -race` hay integration test. Đây là review tĩnh hoàn toàn bằng cách đọc code. Mọi finding bên dưới đều đã được đối chiếu với code, nhưng chưa có test tái hiện. Tôi cũng không khởi động testcontainer nào và không động vào `tally-postgres-1`.

## Đánh giá tổng quan

Chất lượng tốt so với độ phức tạp:

- Posting đi qua `posting.Coordinator` đúng cách: idempotency, khoá kỳ, audit, outbox và kiểm tra entitlement.
- Lock set chuẩn, và trong tx có re-check (`errOrderChanged`).
- Posting lắp ráp cân bằng chính xác.
- Genealogy và hạn sớm nhất lấy từ chính các move đã dùng, có check verify kèm tamper test.

Lỗi thật nằm ở biên giữa kits và giữ chỗ. Disassembly xuất kit mà không tôn trọng phần kit đã được giữ chỗ cho chứng từ khác. Ngoài ra vòng đời kit/combo có thể bị lách qua route catalog chung.

Không có Critical. Có 1 High, 4 Medium và 5 Low.

## Critical

Không có.

## High

### H1. Tháo kit lấy cả kit đang được giữ chỗ cho chứng từ khác

**Vị trí:** `apps/api/internal/kits/disassembly.go:245`

```go
kitMoves, err := s.stock.Issue(ctx, tx, doc, MoveDisassemblyConsume, []IssueLine{{SkuID: o.KitSkuID, Qty: d.Qty, LotID: &kitLot}})
```

`disassemble` (`disassembly.go:176-237`) chỉ kiểm tra hai điều:

- `qty <= produced - disassembled` của lô kit (dòng 189-195);
- tồn của lô, ngầm qua `Issue`.

Nó không làm phép kiểm tra hàng tự do `sum(sellable - reserved)` như `checkFreeStock` ở `assembly_posting.go:302-332` làm cho thành phần. `inventory.StockLedger.Issue` không xét `reserved`. `disassembly_consume` cũng không phải move kiểu sale, nên không có gì chặn cả.

**Kịch bản lỗi:**

1. Lô kit K còn 5 giỏ.
2. Một hoá đơn đã xác nhận giữ chỗ 5 giỏ, nên `sku_stock.reserved = 5`.
3. Thủ kho tạo TG tháo 5 giỏ của lô K và thành công. `sellable = 0` nhưng `reserved = 5` vẫn còn.
4. Khi xuất kho cho hoá đơn thì gặp `409 insufficient_stock`.
5. Hàng đã cam kết với khách đã bị tháo ra mà không có cảnh báo. Giữ chỗ bị vỡ âm thầm (reserved > sellable), đúng loại lỗi mà test `TestConcurrentComboSalesNeverTakeReservedStock` được viết để ngăn ở luồng sale.

**Đề xuất sửa:**

- Trong `disassemble`, trước khi gọi `moveDisassembly`, kiểm tra hàng tự do của `o.KitSkuID` theo cùng cách `checkFreeStock`: `sum(sellable - reserved) >= in.Qty`. Nếu thiếu thì trả về `409 insufficient_stock`.
- `postOnOrder(..., withKit=true, ...)` đã khoá SKU kit. Cần thêm `LockReservations` cho SKU kit để phép kiểm tra không bị race với một reserve song song.
- Thêm integration test: giữ chỗ N kit, tháo N kit và kỳ vọng 409.

## Medium

### M1. Giữ chỗ ở mức SKU nên thành phần màu cố định không được bảo vệ theo màu

**Vị trí:**

- `apps/api/internal/app/kits_ports.go:67-70`: `Reserve` truyền `SkuID`/`Qty`, không có màu.
- `apps/api/internal/kits/assembly_posting.go:302-332`: `checkFreeStock` tính `sum(sellable - reserved)` trên toàn SKU.

**Kịch bản lỗi:**

1. Ruy băng tồn 10 đỏ và 10 xanh.
2. Lệnh LD (recipe ruy băng màu đỏ cố định) reserve 10, nên `reserved = 10`.
3. POS bán 10 ruy băng đỏ. Kiểm tra hàng khả dụng mức SKU cho 20 − 10 = 10, nên sale thành công.
4. Khi post batch, `Issue` với `ColorID = đỏ` lỗi vì không còn đỏ.
5. Kết quả là lệnh đã "reserved" nhưng không hoàn thành được. Điều này trái KIT-04 ("reserve components on `reserved`") và KIT-02 ("fixed colors consumed exactly").

**Đề xuất sửa:** Hạn chế này kế thừa từ thiết kế giữ chỗ của inventory (không có chiều màu). Có hai lựa chọn:

- (a) Thêm `color_id` (nullable) vào `stock_reservations` và cho available theo màu tính trừ đi phần đó. Cách này cần migration và thay đổi contract inventory.
- (b) Ghi rõ là giới hạn đã biết trong PRD/architecture. Đồng thời khi reserve, cảnh báo nếu tồn màu cố định < planned.

Cần lead hoặc người dùng chọn. Ít nhất phải được ghi lại, không nên để ẩn.

### M2. Số lượng vị của gói mix trong recipe tính lại live, không snapshot

**Vị trí:** `apps/api/internal/kits/repo.go:134-161` (`readComponents`). Số lượng cơ sở của từng vị là:

```sql
round(m.parts_qty * g.units_per_part * u.factor, 4)
```

Nó join trực tiếp `mix_groups` và `sku_units` hiện tại. `insertItems` chỉ lưu `parts_qty`.

**Kịch bản lỗi:**

1. Recipe v1 (đã publish) có gói mix 2 phần, `units_per_part = 3`, tức 6 viên mỗi vị.
2. Ai đó sửa nhóm mix ở catalog thành `units_per_part = 5`. `UpdateMixGroup` ở `catalog/mix_groups.go:287-329` cho phép, trừ khi `s.mixSpec.CheckUpdate` chặn dựa trên `MixGroupUsage`.
3. Recipe đang active "đổi" thành 10 viên mỗi vị mà không tạo version mới.
4. Cost view, preview và lệnh LD tạo sau đó dùng số mới. Recipe đã publish lẽ ra bất biến, và nguyên tắc version (KIT-01) bị phá.

Tôi chưa xác minh được `MixGroupUsage` có đếm `kit_recipe_mix_items` hay không, vì không tìm thấy implementation trong `app/domains.go`; catalog dùng `s.usage` nội bộ. Nếu nó có đếm thì kịch bản bị chặn ở catalog. Khi đó finding hạ xuống Low (vẫn nên snapshot cho rõ ràng).

**Đề xuất sửa:** Snapshot `base_qty` (hoặc `units_per_part` và `factor`) vào `kit_recipe_mix_items` lúc tạo hay sửa nháp, rồi đọc từ snapshot. Nếu chưa làm vậy thì thêm test xác nhận `UpdateMixGroup` trả 409 khi nhóm đang được recipe kit dùng.

### M3. `PATCH /products/{id}` của catalog lách được vòng đời kit/combo

**Vị trí:** `apps/api/internal/catalog/products.go:444-446`. `UpdateProduct` cho đổi `status` trên mọi loại product, không có guard cho `kit`/`combo`.

**Kịch bản lỗi:**

1. Tạo combo nháp (product `draft`, recipe chưa publish).
2. Gọi `PATCH /api/v1/products/{id}` với `{"status":"active"}`, không qua `POST /kits/{id}:publish`.
3. Combo hiện trên tile POS: `sales/pos_bootstrap.go:110` chỉ lọc `p.status='active' AND p.type IN ('goods','kit','combo')`. Nó cũng hiện trong search.
4. Thu ngân bấm vào thì checkout trả `422 combo_not_sellable`, vì không có recipe active.
5. Theo chiều ngược lại, PATCH `status: draft` lên kit đã publish sẽ ẩn kit mà recipe vẫn active. Điều đó làm lệch bất biến "publish = recipe active + product active" (KIT-10).

**Đề xuất sửa:**

- Trong `catalog.UpdateProduct`, từ chối đổi `status` khi `type IN ('kit','combo')`. Trả 409 với code như `kit_lifecycle_managed` và hướng dẫn dùng `/kits`.
- `kitsCatalog.UpdateProduct` gọi `UpdateProductIn` thì cần một cờ để bỏ qua guard này.
- Một cách khác là cho tile/search yêu cầu tồn tại recipe active với kit/combo. Cách này tốn query hơn.

### M4. Quyết định (a): kit nháp có lịch sử giá thì archive chứ không xoá, và chiếm tên/mã vĩnh viễn

**Vị trí:**

- `apps/api/internal/kits/recipe_service.go:366-381`.
- `catalog/products.go:254-344` (`CreateProduct` gọi `setBasePrices`).
- `catalog/products.go:352-354` (`ux_products__name_key`).

`kitsCatalog.CreateProduct` (`app/kits_ports.go`) truyền `RetailPrice` vào `SkuExtras`, nên **mọi kit tạo kèm giá bán đều có dòng `price_changes`/`price_list_items` ngay khi tạo**. Vì thế `DeleteKit` gần như không bao giờ xoá hẳn được, chỉ archive (`inactive`).

**Kịch bản lỗi:**

1. Tạo nháp "Giỏ Tết 500k" có giá 500 000.
2. Phát hiện sai nên bấm xoá. Kết quả là archive.
3. Tạo lại "Giỏ Tết 500k" thì nhận `409` vì trùng `ux_products__name_key`. Mã SKU cũng bị giữ.

KIT-10 ghi "delete (if never sold)". Kit này chưa bán, chưa lắp, chỉ có giá do chính màn tạo nháp ghi. Integration test chỉ phủ trường hợp "kit nháp chưa có giá thì xoá hẳn", mà đây không phải đường chính của UI.

**Đề xuất sửa (cần lead/người dùng quyết):**

- (1) Khi product còn `draft` và chưa từng publish, cho xoá luôn các dòng giá do lần tạo nháp sinh ra. Lịch sử giá là append-only cho hàng đang bán, không phải cho bản nháp chưa bao giờ hiển thị.
- (2) Hoặc khi archive thì đổi tên/mã, ví dụ thêm hậu tố `~archived-<id>`, để giải phóng ràng buộc unique.
- (3) Tối thiểu cũng phải ghi hành vi này vào OpenAPI (`DELETE /kits/{id}` trả `archived: true`) và vào PRD.

## Low

### L1. SKU vị trong gói mix không được kiểm tra trạng thái/loại khi build và publish

**Vị trí:** `kits/recipe_build.go` (`buildMixItem`) và `kits/recipe_service.go:326-346` (`checkPublishable`). Chỉ kiểm tra `g.status` của nhóm mix, không kiểm tra từng SKU vị (`status`, `type = goods`). Một vị đã ngừng (`inactive`) vẫn publish được, và chỉ lộ ra khi post batch thiếu hàng.

**Sửa:** Trong `checkPublishable`, thêm điều kiện mọi SKU vị đều `active` và thuộc product `goods`.

### L2. Disassembly chấp nhận mọi reason đang active, không xét loại

**Vị trí:** `kits/disassembly.go:367-380` (`checkReason`). Có thể chọn một reason dành cho điều chỉnh tồn hoặc trả hàng cho TG, làm sai báo cáo theo lý do. Người làm đã ghi đây là quyết định có chủ ý.

**Sửa:** Lọc theo `kind` (ví dụ `disassembly`/`kit`), nếu bảng reason có chiều này.

### L3. Phần dư làm tròn của thành phần không được trả về khi có kit đã bán

**Vị trí:** `kits/disassembly.go:105-120` (`shareOut`). Quy tắc "phần dư về lần cuối" chỉ kích hoạt khi `qty >= left`, với `left = produced - disassembled`. `left` không trừ kit đã bán, nên khi lô kit đã bán ít nhất 1 giỏ thì nhánh này không bao giờ chạy. Phần dư làm tròn (theo scale 4 chữ số) ở genealogy không bao giờ được trả.

Ảnh hưởng nhỏ (≤ 0.0001 × số dòng). Tuy nhiên rủi ro mà plan nêu ("remainder to last") chỉ đúng khi chưa bán kit nào.

**Sửa:** Dùng `left = produced − disassembled − sold` (đọc từ move `sale` của lô kit), hoặc ghi rõ giới hạn.

### L4. Thay thế thành phần không bắt chọn màu với SKU có tồn theo màu

**Vị trí:** `kits/assembly.go:747` (`Substitute`). Nếu SKU thay thế có tồn theo màu và request không gửi `color_id`, item mới không có màu cố định. Batch sau đó lấy màu theo FEFO, không theo ý người lập lệnh. Điều này không nhất quán với quy tắc `color_required` của combo (KIT-03).

**Sửa:** Gọi cùng `ColorRequirementSpec` khi substitute, và trả `422 color_required` ở `color_id`.

### L5. Giá trị tháo kit lấy theo bình quân SKU kit, không theo giá vốn của lô kit

**Vị trí:** `kits/disassembly.go:245-252`. Giá trị là `|value|` của move `disassembly_consume`, tức bình quân gia quyền của SKU kit. Khi SKU kit có nhiều lô với giá vốn khác nhau, thành phần được trả về với tổng giá trị khác với lúc đưa vào. Posting vẫn cân bằng nên không phải lỗi sổ, chỉ là giá vốn thành phần bị trộn giữa các lô. Đây là hệ quả của mô hình moving-average và nên được ghi vào tài liệu costing (§7.5).

### Ghi chú về `kits.Verify`

Trong 12 check của `kits/verify.go`, chỉ 3 check theo lệnh (completed, disassembled, component consumed) lọc `reversal_of IS NULL`. Hiện chưa có endpoint đảo nên chưa sai. Khi thêm `Đảo`, cần rà lại cả 9 check còn lại: posting balance, components cost, unit cost, consumption, genealogy, expiry, disassembly balance/returned/damaged.

## Nhận định về các quyết định của người làm

| # | Quyết định | Nhận định |
|---|---|---|
| (a) | Kit nháp có lịch sử giá thì archive thay vì xoá | **Chưa chấp nhận nguyên trạng.** Xem M4: vì giá được ghi ngay khi tạo, gần như mọi kit nháp đều không xoá được và chiếm tên/mã. Cần sửa hoặc ít nhất ghi tài liệu. |
| (b) | Disassembly phân bổ toàn bộ giá trị, kể cả chi phí đóng gói, về lô thành phần | **Cân bằng và nhất quán sổ, nhưng là quyết định nghiệp vụ.** Hệ quả là giá vốn bình quân của thành phần bị đội lên bằng phần chi phí đóng gói, mà chi phí đó đã tiêu (giấy gói, công). Cách khác là trả về theo giá vốn thành phần gốc (`unit_cost` × share trong posting items) và ghi phần đóng gói vào một move điều chỉnh/hao hụt riêng. Cần người dùng chọn; đây không phải lỗi. |
| (c) | Cho tháo kit trên lệnh đã đóng | **Chấp nhận.** `disassembly.go:223` giữ trạng thái `closed`. `takeBackConsumed` giảm `consumed`, nhưng `ReserveOrder`/`postBatch` từ chối lệnh closed qua `status.Open`, nên không lắp lại được trên lệnh đã đóng. Hợp lý cho trường hợp tháo giỏ tồn sau mùa. |
| (d) | Không có endpoint đảo posting lắp ráp | **Chấp nhận**, theo mục Rollback của plan. Lưu ý: disassembly không hoàn được phần hao hụt (scrap) của batch, và `kits.Verify` cần mở rộng `reversal_of` khi thêm endpoint (xem ghi chú trên). |

## Đã kiểm chứng là đúng

- **Thành phần combo có trong lock set của sale:** `sales/pos.go:207-261`. `saleSkus` gồm SKU trên dòng cộng `stockedSkus(built.Lines)`, tức thành phần combo và quà tặng. Chúng được khoá qua `stockLocks`, và `prepareSale(locked)` kiểm tra lại trong tx.
- **Lock order của assembly/disassembly:** `kits/assembly.go:542-600` (`postOnOrder`):
  - đọc trước order và items;
  - khoá các SKU thành phần, kit và phần thêm, cùng reservations, qua coordinator theo thứ tự chuẩn;
  - trong apply thì `readOrder ... FOR UPDATE` rồi trả `errOrderChanged` nếu có SKU hay reservation nằm ngoài lock set.
- **Posting cân bằng:** `assembly_posting.go:188-201`. Giá trị lô kit = Σ |value| các move consume + `PackingCost × qty`. Recipe active là bất biến nên chi phí đóng gói ổn định.
- **Hạn sớm nhất và genealogy:** chỉ lấy từ các move thực sự dùng (`insertGenealogy` ở `assembly_posting.go:434-452`). Verify có check `kit lot expiry = MIN(child expiry)`.
- **Tháo kit về đúng lô/màu, hàng hỏng vào cách ly:** `disassembly.go:282-303`. Receive vào `LotID`/`ColorID` gốc, rồi `Quarantine` phần hỏng.
- **Entitlement:**
  - Route posting (reserve, close, substitutions, postings, disassemblies) đi qua coordinator, có `postingEntitlements` và trả 403 `subscription_read_only`.
  - Route CRUD nằm trong nhóm Tenant có middleware entitlement.
  - Principal hỗ trợ (support) bị đặt `ReadOnly` qua `scopeFor`.
- **Cô lập tenant:** mọi truy cập dùng `WithTenantTx`/`WithReadOnlyTenantTx` (RLS). Adapter catalog chạy trong tx của bên gọi (`CreateProductIn`/`UpdateProductIn`).
- **`ComboExpander` tất định:** sắp theo `line_no, sku_id`. Màu cố định được giữ, còn màu quầy đi qua `component_colors` (`openapi.yaml:11751-11762`).
- **Combo nháp ẩn khỏi POS:** `pos_bootstrap.go:110` lọc `p.status='active'`. Điều này đúng khi đi qua `/kits`; xem M3 cho đường lách.

## Edge case từ scout

- Disassembly so với giữ chỗ của kit (H1).
- Giữ chỗ theo SKU so với màu cố định (M1).
- Spec nhóm mix thay đổi sau khi publish (M2).
- Route catalog chung đổi trạng thái kit/combo (M3).
- Unique tên/mã sau khi archive (M4).
- Phần dư làm tròn khi đã bán kit (L3).
- Nhiều lô kit với giá vốn khác nhau khi tháo (L5).

## Hành động đề xuất (theo thứ tự)

1. **H1:** Thêm kiểm tra hàng tự do và lock reservations cho SKU kit trong disassembly, kèm integration test (giữ chỗ N kit rồi tháo N kit thì nhận 409).
2. **M3:** Chặn `PATCH /products/{id}` đổi `status` của kit/combo.
3. **M4:** Chọn phương án (xoá giá của bản nháp, hoặc đổi tên khi archive) và ghi vào OpenAPI.
4. **M2:** Snapshot số lượng vị, hoặc xác nhận và test rằng `MixGroupUsage` chặn sửa nhóm đang dùng.
5. **M1:** Lead hoặc người dùng chọn giữa giữ chỗ theo màu và ghi giới hạn.
6. **L1, L4:** Validate SKU vị và màu khi thay thế.
7. Nhờ người dùng chốt quyết định (b) về chi phí đóng gói khi tháo kit.
8. **Chạy lại toàn bộ gate** (`go vet -tags integration`, `go test -race`, integration) sau khi sửa, vì review này chưa chạy được lệnh nào.

## Phần chưa được kiểm bằng test (cần chạy bổ sung khi Bash hoạt động lại)

| Hạng mục | Cách kiểm đề xuất |
|---|---|
| Gate chung | `cd apps/api && go vet -tags integration ./... && go test -race ./internal/kits/... ./internal/sales/... && go test -tags integration -count=1 ./internal/kits/... ./internal/app/... ./internal/inventory/...`. Sau đó `docker ps --filter label=org.testcontainers=true` phải rỗng. |
| H1 | Integration test: giữ chỗ N kit bằng hoá đơn đã xác nhận, rồi `POST /assembly-orders/{id}/disassemblies` với qty N. Hiện tại dự đoán trả 201; kỳ vọng sau khi sửa là 409. |
| M1 | Integration test: lệnh LD có màu cố định đỏ đã reserve, POS bán hết màu đỏ, rồi post batch. Dự đoán batch lỗi thiếu màu. |
| M2 | Test `UpdateMixGroup` trên nhóm đang được recipe kit dùng: 409 hay thành công. Nếu thành công thì preview recipe đổi số lượng vị. |
| M3 | Integration test: `PATCH /products/{comboId}` `{"status":"active"}` khi combo còn nháp. Kiểm tra tile `/pos/bootstrap` có combo, và checkout trả 422. |
| M4 | Integration test: tạo kit nháp có `retail_price`, `DELETE`, rồi tạo lại cùng tên. Dự đoán nhận 409. |
| L3 | Unit test `shareOut`: một phần lô kit đã bán, tháo phần còn lại, kiểm tra phần dư của genealogy. |
| Mục "Đã kiểm chứng là đúng" | Chỉ kiểm bằng đọc code. Báo cáo của người làm ghi có test phủ, nhưng reviewer chưa tự chạy lại. |

## Số liệu

- Type coverage: không đo được (Bash hỏng).
- Test coverage: không đo được. Báo cáo của người làm ghi mọi gate xanh, nhưng **reviewer chưa tự xác minh**.
- Lint: không chạy được.

## Câu hỏi còn mở

1. `catalog` `MixGroupUsage` (`s.usage`) có đếm `kit_recipe_mix_items`/`kit_recipe_items` không? Nếu có thì M2 hạ xuống Low.
2. `price_changes` có bắt buộc append-only cả với product còn `draft` chưa từng publish không? Câu trả lời quyết định phương án cho M4.
3. Có chấp nhận việc giá vốn thành phần bị đội lên bởi chi phí đóng gói khi tháo kit, tức quyết định (b), không?

Status: DONE_WITH_CONCERNS
Summary: Review tĩnh phase 11 cho thấy luồng chính (posting cân bằng, genealogy, hạn sớm nhất, lock set combo, coordinator và entitlement) đúng. Có 1 lỗi High: tháo kit lấy cả kit đang được giữ chỗ. Có thêm 4 Medium về giữ chỗ theo màu, snapshot gói mix, lách vòng đời qua catalog PATCH, và archive chiếm tên/mã.
Concerns/Blockers: Không chạy được vet, test hay integration vì Bash lỗi E2BIG trong phiên review, nên mọi kết luận dựa trên đọc code. Cần chạy lại gate sau khi sửa H1, M3 và M4.
