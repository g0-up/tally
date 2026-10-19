# Báo cáo Phase 11: Bộ quà, combo và lắp ráp

- Plan: `plans/261016-0854-tally-full-product-implementation/`
- Phase: `phase-11-kits-assembly.md`
- Trạng thái: **BLOCKED**. Lần giao đầu, toàn bộ KIT-01..10 đã xong và mọi gate Go đều xanh. Các sửa sau review (H1, M3, M4, L1-L4, chi phí đóng gói khi tháo kit) được làm khi Bash hỏng (E2BIG) nên **chưa compile, chưa chạy test**. Đã thêm migration 000042 mở rộng CHECK `cogs_adjustments`. Danh sách gate ở cuối báo cáo.
- Frontmatter phase vẫn để nguyên. Tôi không đổi `plan.md` và không commit. Lần giao đầu không cần migration (các bảng có sẵn trong 000019); bản sửa sau review cần một migration (xem mục "Chi phí đóng gói khi tháo kit").

## File đã làm

**Domain `apps/api/internal/kits/`** (mới, khoảng 6 200 dòng chưa tính test)

- Nghiệp vụ:
  - `model.go`, `status.go`, `errors.go`, `ports.go`, `service.go`, `repo.go`, `command.go`;
  - `recipe_build.go`, `recipe_service.go`, `combo.go`, `suggestion.go`, `label.go`;
  - `assembly.go`, `assembly_posting.go`, `lot_factory.go`, `disassembly.go`;
  - `verify.go`, `dto.go`, `http.go`.
- Unit test: `helpers_test.go`, `assembly_test.go`, `disassembly_test.go`, `recipe_test.go`, `suggestion_test.go`, `lot_factory_test.go`.

**Wiring**

- `internal/app/kits_ports.go`: mới. Là adapter cho catalog, inventory (giữ chỗ, xuất/nhập theo lô và màu, cách ly) và pricing.
- `internal/app/domains.go`: thêm `Domains.Kits` và nối `ComboExpander` vào sales.
- `internal/app/app.go`: thêm `Modules.Kits`.
- `internal/app/routes.go`: đăng ký route kits.
- `internal/cli/serve.go`: khởi tạo handler kits.
- `internal/cli/ledger.go`: `ledger verify` chạy thêm `kits.Verify`.

**Sửa nhỏ ở domain khác** (tương thích ngược, đã báo phase liên quan)

- `internal/catalog/products.go`: thêm `CreateProductIn`, `UpdateProductIn` và `DeleteProductIn`. Chúng chạy use case product trong transaction của bên gọi, để kit ghi product cùng recipe một lần.
- `internal/sales/lines.go`: thêm trường dòng `component_colors` để quầy chọn màu cho thành phần combo.
- `internal/sales/pos_bootstrap.go`: tile POS hiện combo đã publish.
- `internal/sales/combo_colors_test.go`: mới, unit test cho chọn màu thành phần.

**Contract**

- `apps/api/api/openapi.yaml`: thêm toàn bộ path và schema kits, assembly-orders, disassemblies và `/lots/{id}/genealogy`.
- `internal/app/openapi_contract_test.go`: thêm Kits vào stack kiểm tra contract.
- `apps/web/src/shared/api/schema.d.ts`: sinh lại bằng openapi-typescript.

**Integration test**

`internal/app/kits_integration_test.go`: mới, khoảng 800 dòng, gồm các test sau.

- `TestKitsOverHTTP`:
  - kit nháp bị ẩn khỏi tìm kiếm;
  - preview giá vốn và biên lợi nhuận;
  - publish bằng If-Match;
  - xoá kit đã publish trả về 409 `kit_published`;
  - xoá kit nháp chưa có giá thì xoá hẳn;
  - xoá kit nháp có giá cũng xoá hẳn, tạo lại cùng mã được, publish thì có đúng 1 dòng lịch sử giá;
  - lập lệnh không làm phát sinh stock move;
  - reserve;
  - posting cân bằng (Σ giá trị xuất thành phần + chi phí đóng gói = giá trị nhập kit);
  - hạn kit = 2026-12-31, tức hạn sớm nhất của các thành phần;
  - genealogy và nhãn;
  - bán kit chỉ xuất kit;
  - thay thế thành phần;
  - tháo 2 kit trả đúng lô và màu, 1 ruy băng hỏng vào cách ly;
  - lắp lại;
  - combo nháp bị ẩn khỏi search và POS, publish thì hiện;
  - combo thiếu màu trả về 422 `color_required` tại `lines.0.component_colors`.
- `TestConcurrentComboSalesNeverTakeReservedStock`: 12 quầy bán combo đồng thời. Đúng 3 lần thành công, các lần còn lại trả về 409 `insufficient_stock`. 2 túi mà hoá đơn đã xác nhận giữ chỗ không bị đụng tới.
- `TestKitsVerifyReportsTamperedRows`: sửa từng dòng bằng superuser rồi kiểm tra `kits.Verify` báo đúng check, rồi khôi phục. Có 13 trường hợp (thêm case sửa `cogs_adjustments` của lần tháo), phủ 12/12 check.

## Lệnh đã chạy và kết quả

Các lệnh Go chạy trong `apps/api`.

| Lệnh | Kết quả |
|---|---|
| `gofmt -l .` | Không có file nào |
| `go vet ./...` và `go vet -tags integration ./...` | Sạch |
| `golangci-lint run --build-tags integration ./...` | `0 issues` (đã sửa 3 lỗi revive về doc comment trong `catalog/products.go`) |
| `go test -race ./...` | Tất cả `ok` |
| `go test -tags integration -count=1 ./...` | Tất cả `ok`: app 34.6s, inventory, catalog, sales, procurement, cash, debt, migrations… |
| `go test -tags integration ./internal/app/ -run 'TestKits\|TestConcurrentCombo\|OpenAPI'` | `ok`, chạy lại sau khi thêm test thay thế thành phần |
| `openapi-typescript ../api/api/openapi.yaml -o src/shared/api/schema.d.ts` (gen:api) | Sinh lại thành công. Máy không có `pnpm` trong PATH nên tôi dùng `npx` và `corepack pnpm` |
| `corepack pnpm typecheck` (web) | Sạch |
| `corepack pnpm test` (web) | 232/234 pass. 2 test đỏ, xem mục Rủi ro |
| `docker ps --filter label=org.testcontainers=true` | Không còn container nào. Tôi không động vào tally-postgres-1 hay dev MinIO |

## Đối chiếu tiêu chí thành công

- **Ledger verify sạch, posting cân bằng**: `verifyKitLedgers` chạy cả `sales.Verify`, inventory và `kits.Verify` sau mỗi kịch bản. Test cũng kiểm tra trực tiếp trên `stock_moves`: 213 000 + 30 000 = 243 000.
- **Hạn lô kit bằng hạn sớm nhất của thành phần**: test HTTP xác nhận, và có check verify `kit lot expiry` kèm tamper test.
- **Tháo 2 kit**: các dòng tháo trả về đúng `source_lot_id` và màu trong posting. Ruy băng hỏng làm `quarantine_qty` tăng 1, `on_hand` tăng 2, và lô hỏng không bán được.
- **Combo nháp**: không xuất hiện trong `GET /search?type=sku&sellable=true`, trong tile `/pos/bootstrap` hay ở checkout (422 `combo_not_sellable`). Sau khi publish thì combo xuất hiện.
- **Combo thiếu màu**: trả về 422 `color_required`. Khi gửi `component_colors` thì sale thành công: màu cố định của recipe được giữ, màu quầy chọn được áp dụng.
- **Bán combo đồng thời**: thành phần combo nằm trong lock set của sale, nên không bao giờ bán lấn vào hàng đã giữ chỗ.

## Quyết định và sai lệch so với mô tả phase

- **Xoá kit**: kit đã publish không xoá được, muốn ngừng thì chuyển sang inactive. Kit đã bán hoặc đã lắp cũng không xoá được. Kit nháp xoá hẳn cùng giá nháp của nó; chỉ khi SKU đã có lịch sử giá (giá bị sửa ở màn hình sản phẩm) thì mới archive (chuyển inactive), vì lịch sử giá là append-only (xem M4).
- **Lệnh lắp ráp**: chỉ tạo từ recipe đang active và còn hiệu lực trong ngày. Combo cũng vậy; nếu không thoả thì trả về 422 `combo_not_sellable`.
- **Hao hụt (scrap)**: không tính vào `consumed` của dòng lệnh. Giá trị phần hao hụt vẫn được tính vào giá vốn kit.
- **Tháo kit**:
  - Giảm `consumed` của dòng lệnh, nhờ đó lệnh quay về `in_progress` và lắp lại được. Tháo được cả trên lệnh đã đóng.
  - Thành phần trở về lô nguồn đúng giá vốn gốc lúc lắp; phần còn lại của giá trị kit (đóng gói, phần hao hụt, chênh bình quân) ghi thành `cogs_adjustment` (người dùng quyết định 2026-10-18, xem mục "Chi phí đóng gói khi tháo kit").
  - Chỉ nhận lý do đang active thuộc loại damage, disposal, internal_use hoặc write_off (đã sửa sau review, xem mục cuối).
- **Gợi ý theo ngân sách**: chỉ đọc, giá tính theo số lượng 1, không bao giờ sửa hoá đơn đã xác nhận.
- **Tiến độ**: `/kits/progress` đếm các lệnh chưa đóng.
- **Re-authentication**: route kits không yêu cầu, vì không có thao tác nào thuộc nhóm nhạy cảm.
- **Search catalog**: đã ẩn hàng nháp từ trước nên tham số `sellable` không cần xử lý thêm.
- **Genealogy**: route `/lots/{id}/genealogy` do module kits phục vụ.
- **Màu thành phần combo**: đi qua trường `component_colors` của dòng sales. Màu trùng với màu cố định của recipe thì được bỏ qua.
- **Verify `disassembly balance`**: thông báo hiển thị cả số kit lẫn giá trị trả về, để sai lệch về số lượng không bị hiện thành "81000 vs 81000".
- **Đảo posting lắp ráp**: mục Rollback nhắc tới thao tác `Đảo`, nhưng không có trong todo hay requirement nên tôi chưa làm endpoint đảo. Gỡ một lần lắp sai thì dùng disassembly. Verify đã bỏ qua các posting có `reversal_of`, sẵn cho lúc thêm sau.

## Rủi ro và concern

- **Web test đỏ (không thuộc phase này)**: `src/app/router.test.tsx` và `src/app/layouts/app-shell.test.tsx`. Khi mở nhóm "Bán hàng", điều hướng đi tới `/inventory/counts` thay vì `/sales/pos`. Cả `apps/web` đang untracked và do phase web sửa song song. File `schema.d.ts` mà tôi sinh lại chỉ chứa type, không ảnh hưởng runtime nên không thể gây ra lỗi này. Typecheck web vẫn sạch.
- **Thứ tự subtest**: các subtest trong `TestKitsOverHTTP` dùng chung kho của một store, và kho được nhận dư cho đủ tất cả. Số `buildable` = 10 trong preview phụ thuộc vào việc subtest preview chạy đầu tiên, và nó luôn chạy đầu tiên, kể cả khi chạy lẻ bằng `-run`.
- **Giá vốn thành phần**: đã đổi theo quyết định của người dùng. Tháo kit không còn đẩy chi phí đóng gói vào giá vốn thành phần (xem mục "Chi phí đóng gói khi tháo kit").

## Sửa theo review (code-reviewer-261018-kits-assembly-review.md)

Bash lỗi E2BIG cho mọi agent và không có Grep, nên các sửa dưới đây làm bằng Read/Edit/Write. **Chưa compile, chưa chạy test nào.** Danh sách gate cần chạy ở cuối mục.

**H1: tháo kit lấn vào kit đã giữ chỗ**

- Đã sửa trong `internal/kits/disassembly.go`.
  - `kitStock` đọc sellable/reserved của SKU kit trước khi tháo. Lúc này đã giữ lock sku_stock của kit qua `postOnOrder(withKit=true)`.
  - `checkKitsFree` chạy sau `moveDisassembly`: sellable chỉ được giảm tối đa bằng số kit không ai giữ (`sellable trước - reserved`, không âm). Vượt thì trả 409 `insufficient_stock`, transaction rollback.
  - So trước/sau thay cho `sellable - reserved ≥ qty`, để kit hết hạn hoặc đang cách ly (không nằm trong sellable) vẫn tháo được.
- `internal/kits/assembly_posting.go`: tách `errInsufficientStock(ctx, q, sku, need, free)` để `checkFree` và tháo kit dùng chung.
- Đã xác nhận không cần constraint mới. Ledger issue không đọc `reserved` (`inventory/ledger.go:633-699`), và migration 000010 cố ý không có CHECK `reserved <= sellable` (dòng 88-89).
- Test: `TestKitRulesOverHTTP`, subtest đầu. Lắp 6 kit, hoá đơn xác nhận giữ 5.
  - Tháo 5 hoặc 2 thì 409 `insufficient_stock`; tháo 1 thì 201.
  - Sau đó sellable = 5, reserved = 5, tức hoá đơn vẫn đủ hàng để xuất. Tháo thêm 1 lại 409.

**M2: mix group bị đổi khi recipe kit đang dùng**

- Không cần sửa, cũng không cần migration. Catalog đã chặn sẵn:
  - `catalog/usage_repo.go`: `mixGroupReferences` đếm cả `kit_recipe_items` ("công thức giỏ"), và `MixGroupUsedSkus` đọc cả `kit_recipe_mix_items`.
  - `catalog/edit_rules.go`: `MixGroupChangeSpec.CheckUpdate` trả `errMixPackLocked` / `errMixFlavorInUse` khi nhóm đang được dùng.

**M3: trạng thái kit/combo đi theo recipe**

- `catalog/products.go`: `UpdateProduct` trả 422 `status: set_by_recipe` khi đổi status của kit/combo.
  - Chỉ đường `UpdateKitProductIn` (cờ `recipeStatus` trong `catalog/service.go`) được đổi.
  - `app/kits_ports.go` dùng đường này cho publish và archive.
- `sales/pos_bootstrap.go`: tile kit/combo chỉ hiện khi có `kit_recipes` đang active.
- OpenAPI: PATCH `/products/{id}` có mô tả 422.
- Test (subtest 2):
  - PATCH status trên kit nháp và trên kit đã publish đều 422 `set_by_recipe`; kit nháp vẫn bị ẩn khỏi search.
  - Combo tạo từ form product (không có recipe) không hiện trên POS.

**M4: xoá kit nháp đã có giá**

- **Chọn phương án C** (team-lead chỉ đạo; A bị loại vì rule cấm DB function, B không cần).
  - Lý do C khả thi: resolver giá chỉ đọc `price_lists` và `price_list_items` (`pricing/repo.go:158-197`), không bao giờ đọc `price_changes`. Vì vậy giá nháp chỉ cần nằm ở `price_list_items` là preview và bán hàng vẫn thấy giá. `tally_app` có quyền DELETE trên `price_list_items`; chỉ `price_changes` là append-only.
- `internal/pricing/draft_prices.go` (file mới, không sửa `writer.go`), thêm 3 method của `PriceWriter`:
  - `SetDraftRetailPrice`: ghi dòng giá bán lẻ gốc (không màu, đơn vị gốc, min_qty 0) mà không ghi lịch sử. Khoá list trước dòng, giống mọi writer.
  - `RecordDraftPrices`: lúc publish, ghi dòng lịch sử đầu tiên (old_price NULL, new_price = giá hiện tại, source `catalog`) cho mỗi dòng giá của SKU chưa có lịch sử. Publish lại không ghi gì thêm.
  - `DeleteDraftPrices`: xoá `price_list_items` của SKU. Bên gọi đã kiểm tra SKU không có lịch sử.
- `internal/kits`:
  - `ports.go`: port mới `DraftPrices`; bỏ `NewProduct.RetailPrice`.
  - `service.go`: `Deps.DraftPrices`.
  - `recipe_service.go`:
    - `CreateKit` không đưa giá cho catalog nữa mà gọi `SetDraftRetailPrice`.
    - `PublishKit` gọi `RecordDraftPrices`.
    - `DeleteKit` chỉ archive khi có `price_changes` (fallback cho dữ liệu cũ hoặc giá bị sửa ở màn hình sản phẩm). Ngược lại thì xoá giá nháp, rồi recipe items, recipes và product.
- `internal/app/kits_ports.go`: `kitsCatalog.CreateProduct` không truyền `SkuExtras.RetailPrice`. `internal/app/domains.go`: `DraftPrices: pricing.NewPriceWriter(pri)`.
- OpenAPI: mô tả createKit, publishKit, deleteKit.
- Test: `TestKitsOverHTTP`, subtest "a priced draft is deleted without price history and its history starts when published".
  - Tạo nháp GIO-GIA có giá, xoá thì 200 `"archived":false` và SKU không có `price_changes`.
  - Tạo lại cùng tên và mã thì 201, vẫn chưa có lịch sử.
  - Publish thì có đúng 1 dòng `price_changes`: old_price NULL, new_price 200000, source `catalog`.
  - Preview của subtest đầu (KitPrice 200000) vẫn đọc giá nháp.
- Hệ quả đã biết: nếu giá kit nháp bị sửa ở `/skus/{id}/sale-price` thì dòng lịch sử đầu có old_price = giá nháp, và xoá kit đó sẽ archive. Chấp nhận được vì đó là sửa giá có chủ ý.

**Chi phí đóng gói khi tháo kit** (người dùng quyết định 2026-10-18: ghi vào chi phí)

- `internal/kits/disassembly.go`:
  - `childLot.Cost` là giá vốn Total đã lấy khỏi lô lúc lắp (Σ UnitCost × lượng dùng).
  - `splitDisassemblyValue`: mỗi thành phần trở về với `round(Cost × Share / Total)`. Phần còn lại của giá trị kit V (đóng gói, phần hao hụt, chênh bình quân) là chi phí. Nếu phần còn lại âm (bình quân kit đã giảm), V được chia theo tỷ lệ giá trị gốc bằng `money.Allocate` và không ghi chi phí.
  - `insertDisassemblyCogs`: INSERT cục bộ trong kits vào `cogs_adjustments` (sku = SKU kit, `source_doc_type` = `disassembly`, `source_doc_id` = id phiếu tháo, business_date của phiếu). Không export `procurement.insertCogs`.
- `internal/kits/verify.go`: check `disassembly balance` thành "giá trị lấy ra = giá trị trả về + chi phí đã ghi" và so cả số kit.
- Quarantine_in: không phải lỗi. Chuyển sang lô cách ly là chuyển không đổi giá trị, nên giá trị trả về đã đầy đủ.
- OpenAPI: mô tả `postDisassembly`.
- Test:
  - Unit `TestSplitDisassemblyValue` (3 case: có chi phí, chi phí 0, phần còn lại âm) và `childLots` kiểm tra `Cost`.
  - Integration `TestKitRulesOverHTTP`, subtest "taking a kit apart gives its components back at their cost and expenses the packing": trả về 71000, chi phí 10000, verify sạch.
  - Tamper: sửa `amount` của `cogs_adjustments` thì báo `disassembly balance`.
- **Migration `apps/api/migrations/000042_cogs_adjustment_disassembly_source.{up,down}.sql`** (team-lead cấp số 000042; phase15-web dùng từ 000043):
  - up: DROP `ck_cogs_adjustments__source_doc_types`, ADD `ck_cogs_adjustments__source_doc_type_values` với 5 giá trị (4 giá trị của 000039 cộng `disassembly`), kèm comment lý do.
  - down: DROP `ck_cogs_adjustments__source_doc_type_values`, ADD lại `ck_cogs_adjustments__source_doc_types` đúng 4 giá trị của 000039. Nếu đã có dòng `disassembly` thì down bị CHECK từ chối, đúng như mong muốn (không mất dữ liệu âm thầm).
  - Không trigger, function, view, DO hay CASCADE; tên constraint mới không trùng tên cũ; tên file không chứa mã phase hay finding.
  - `internal/inventory/inventory_integration_test.go` (`TestRollingBackMaxStockRefusesAppliedCostRecalculations`): chạy 000042.down trước 000039.down và 000031.down, như một rollback thật (theo chỉ đạo team-lead).

**L1: publish mix pack có vị ngừng bán**

- `kits/recipe_service.go` `checkPublishable` kiểm tra thêm SKU vị (`kit_recipe_mix_items`): SKU/product phải active và là `goods`. Vị sai báo ở `items.N.flavors`.
- Mix group inactive giờ báo `items.N.mix_group_id` thay vì `sku_id`.
- OpenAPI publish có mô tả các field.
- Chưa có integration test, vì cần dựng mix group.

**L2: lý do tháo kit**

- `checkReason` chỉ nhận `damage`, `disposal`, `internal_use`, `write_off` (`disassemblyReasonKinds`).
- Lỗi 422 tại `reason_id`: `not_found`, `wrong_kind` hoặc `inactive`.
- Test: lý do loại `gift` trả 422 `wrong_kind`.

**L3: phần dư làm tròn**

- `shareOut` giờ tính luỹ kế. Phần lô thành phần của `taken+qty` kit (cắt theo scale) trừ đi phần đã trả. Nhờ vậy làm tròn không dồn qua nhiều lần tháo, và lần tháo kit cuối của lô trả hết phần còn lại.
- Khi đã bán bớt kit, phần dư thuộc về các kit đã bán, vì chúng mang giá vốn của mình ra khỏi kho. Nên không cần trả phần dư đó về lô.
- Unit test: `TestShareOut` (4 case).

**L4: thay bằng hàng quản lý theo màu**

- `kits/assembly.go` `checkSubstituteSku`: không gửi `color_id` cho SKU có tồn theo màu thì trả 422 `color_id: color_required` (`CodeColorRequired` trong `errors.go`).
- Test (subtest 3): thay kẹo bằng nơ đỏ. Thiếu màu thì 422; có màu thì 201.

**Không sửa theo chỉ đạo**

- M1 và L5 là giới hạn đã biết.
- Quyết định (b) (chi phí đóng gói khi tháo kit) đã có và đã làm, xem mục riêng ở trên.

**Concern mới**

- Đã xử lý: `POST /products` tạo kit/combo với status active giờ trả 422 `set_by_recipe`; tạo không có status thì là nháp (test ở subtest 2 của `TestKitRulesOverHTTP`).
- L4 chặt hơn lúc lập lệnh: lệnh cho phép thành phần chưa chọn màu, còn người thay thế thì phải chọn màu.
- `openapi.yaml` cũng đang được phase15-web sửa. Tôi chỉ đổi mô tả: POST và PATCH products, createKit, deleteKit, publishKit, substitutions, postDisassembly; không đổi schema.
- Chưa kiểm được bằng grep (agent tìm kiếm cũng BLOCKED): còn chỗ nào khác dùng `childLot.UnitCost` hoặc `kits.NewProduct{RetailPrice: ...}` ngoài các file đã sửa không. Compile sẽ bắt ngay nếu có.

**Gate cần chạy khi shell hoạt động lại** (trong `apps/api`, trừ dòng web)

1. `gofmt -l .`
2. `go vet ./...` và `go vet -tags integration ./...`
3. `golangci-lint run --build-tags integration ./...`
4. `go test -race ./...`
5. `go test -tags integration -count=1 ./...` (gồm test migrations up/down và `TestRollingBackMaxStockRefusesAppliedCostRecalculations`)
6. `go test -race -tags integration -count=1 ./internal/app/ -run 'TestConcurrentGifts|TestConcurrentCheckouts|TestFirstSales|TestSalesOverHTTP|TestConcurrentComboSales|TestKitsOverHTTP|TestKitRulesOverHTTP|TestKitsVerifyReportsTamperedRows'`
7. `grep -rn 'UnitCost\|RetailPrice' internal/kits internal/app/kits_ports.go` để chắc không còn chỗ dùng cũ.
8. Trong `apps/web`:
   - `openapi-typescript ../api/api/openapi.yaml -o src/shared/api/schema.d.ts`
   - `corepack pnpm typecheck`
   - `corepack pnpm test`
9. `docker ps --filter label=org.testcontainers=true` phải rỗng.

Status: BLOCKED
Summary: Phase 11 đã xong KIT-01..10 và các sửa sau review: H1, M3, M4 (phương án C: giá nháp không có lịch sử, xoá hẳn được, lịch sử bắt đầu khi publish), L1-L4, và chi phí đóng gói khi tháo kit ghi vào `cogs_adjustments`. Các sửa này chưa compile và chưa chạy test vì Bash hỏng (E2BIG).
Concerns/Blockers: Bash E2BIG nên chưa chạy được gate nào. Migration 000042 (mở rộng CHECK `cogs_adjustments.source_doc_type`) đã tạo. 2 test web (router, app-shell) đỏ từ trước do phase web làm dở.
