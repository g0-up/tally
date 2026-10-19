# Code Review — Phase 05: Catalog & Partners

- Thời điểm: 2026-10-17 03:30 (Asia/Saigon)
- Reviewer: code-reviewer (read-only, không sửa code, không commit, không chạm DB)
- Đối chiếu: `phase-05-catalog-partners.md`, `design/architecture.md` (§3.6), `design/database-schema-catalog-pricing.md`, `design/database-schema-org-partners.md`, `design/database-schema-sales-kits.md`, `docs/prd.md`, `docs/design/prototype-v6.14.html`

## Phạm vi

- `apps/api/internal/catalog/**`, `apps/api/internal/partner/**`
- `apps/api/internal/org/reasons.go`, `reasons_repo.go`, `reasons_http.go`
- `apps/api/internal/app/domains.go`
- Migrations 000004–000008
- Phần catalog/partners/reasons của `openapi.yaml`

Kiểm tra đã chạy (đều read-only):

| Lệnh | Kết quả |
|------|---------|
| `go vet ./internal/catalog/... ./internal/partner/... ./internal/org/...` | sạch |
| `go test ./internal/catalog/... ./internal/partner/... ./internal/org/... ./migrations/...` | ok |
| `golangci-lint run ./internal/catalog/... ./internal/partner/... ./internal/org/...` | 0 issue |

Integration test (cần DB) không chạy, để không đụng DB dev. Đánh giá integration test dựa trên việc đọc source.

## Đánh giá tổng quan

Nền tảng chắc:
- Tenant isolation dựa vào RLS, FK composite `(tenant_id, id)` và các test tách tenant.
- Search normalisation đi qua `textnorm`, chỉ giữ chữ cái và chữ số, nên không có ký tự wildcard LIKE nào lọt vào pattern.
- Contact/address kiểm tra chủ sở hữu party, và attachment ảnh kiểm tra owner, trạng thái scan và kiểu ảnh.
- Không thấy SQL injection: tên bảng profile là hằng, mọi giá trị đều bind.

Lỗi thật nằm ở **quy tắc nghiệp vụ khi đổi đơn vị** (base unit và factor của đơn vị lẻ trong mix group). Ngoài ra có vài race và vài chỗ lệch nhẹ với contract §3.6. Không có lỗi Critical.

---

## Critical

Không có.

---

## High

### H1. Đổi đơn vị gốc (base unit) chỉ chặn khi có stock_moves; giá, chứng từ nháp và mix group bị lệch âm thầm

- Vị trí:
  - `catalog/skus.go:498-586` (`changeBaseUnit`), được gọi từ `UpdateSku` (`skus.go:398-496`)
  - `catalog/edit_rules.go:209-218` (`UnitConversionSpec.CheckBaseChange`)
- Bằng chứng: `CheckBaseChange` chỉ query `stock_moves`. Sau khi qua guard đó có 2 nhánh.
  - **Nhánh A** (đơn vị mới chưa thuộc SKU): insert đơn vị gốc mới, chuyển barcode, rồi xoá dòng `sku_units` cũ.
    - Cột `unit_id` trong các bảng dòng (giá, đơn nháp, PO, phiếu kiểm…) không có FK tới `sku_units`.
    - Hệ quả: các dòng đó trỏ vào unit đã bị xoá mà không có lỗi nào.
  - **Nhánh B** (đơn vị mới là một unit sẵn có với factor f): chia mọi factor cho f, nhân `ref_cost` và `min_price` với f, chia `min_stock`.
    - `price_list_items` và `price_changes` (Phase 06) không được quy đổi.
    - `base_qty` của các dòng chứng từ nháp (báo giá, đơn bán nháp, PO nháp) vẫn tính theo đơn vị gốc cũ.
    - `piece_unit_id` của mix group có thể không còn là factor 1.
- Tác động: bảng giá theo đơn vị gốc sai bội số f. Đơn nháp khi post sẽ xuất/nhập sai số lượng. Mix group vi phạm policy "đơn vị lẻ factor 1". Đây là sai lệch dữ liệu tiền và tồn, không ném lỗi, nên CI không bắt được.
- Fix khuyến nghị:
  - Cách đơn giản và an toàn nhất: chặn đổi base unit khi `SkuInUse` (danh sách `skuReferences` đã có ở `usage_repo.go`) trả true. Lỗi trả về là `edit_locked` kèm lý do.
  - Nếu muốn linh hoạt hơn: `CheckBaseChange` cần kiểm thêm `price_list_items`, `price_changes`, mọi bảng dòng có `unit_id` hoặc `sku_id` ở trạng thái draft, và `mix_group_items`.
  - Bổ sung test cho cả 2 nhánh A/B, mỗi nhánh có giá và đơn nháp tồn tại.

### H2. Sửa factor, vô hiệu hoá hoặc xoá đơn vị SKU không kiểm `piece_unit_id` của mix group

- Vị trí: `catalog/skus.go:735-787` (`UpdateSkuUnit`), `skus.go:790-827` (`DeleteSkuUnit`), và nhánh B của `changeBaseUnit`.
- Bằng chứng:
  - Policy mix group (`catalog/mix.go`, `mix_groups.go`) yêu cầu đơn vị lẻ là unit factor 1 dùng chung cho mọi vị.
  - `MixGroupChangeSpec` (`edit_rules.go` khoảng 220-265) chỉ chạy khi sửa mix group.
  - Không có kiểm tra ngược nào khi sửa unit của SKU. `UpdateSkuUnit` cho đổi factor của unit factor 1 thành ≠1, hoặc set inactive, trong khi SKU đó đang là một vị của mix group active.
  - `DeleteSkuUnit` có kiểm `skuUnitReferences`, nhưng danh sách này không có mix group. FK `fk_mix_groups__piece_unit_id` (`migrations/000008_catalog_mix_groups.up.sql:17`) trỏ tới `units(tenant_id, id)` (danh mục đơn vị chung), **không** trỏ tới `sku_units`. Vì vậy DB không chặn được việc xoá `sku_units` của vị. Cả ba thao tác (đổi factor, inactive, xoá) đều lọt.
- Tác động: mix group active chứa vị mà "đơn vị lẻ" không còn factor 1. Khi bán lẻ theo mix (Phase 09), số lượng quy đổi sẽ sai.
- Fix: trong `UpdateSkuUnit`, `DeleteSkuUnit` và `changeBaseUnit`, nếu SKU là vị trong `mix_group_items` của một mix group có `piece_unit_id = sku_units.unit_id` (bất kể status), thì từ chối đổi factor khỏi 1, inactive hoặc xoá. Trả `edit_locked` với message verbatim kiểu "Đơn vị đang dùng làm đơn vị lẻ của nhóm bán trộn …". Thêm test.

---

## Medium

### M1. SKU code "unique không phân biệt hoa thường" chỉ được kiểm bằng Go (sai lệch #1 đã khai báo)

- Vị trí:
  - `catalog/skus.go:185-194`: `SELECT 1 FROM skus WHERE upper(code) = upper($1) AND id <> $2`
  - Cùng mẫu ở `variant_service.go:171,216` và `quick_create.go:105`
  - Constraint DB là `ux_skus__code UNIQUE (tenant_id, code)`, chỉ chặn trùng đúng chính tả.
- Vấn đề:
  1. **Race**: hai request song song tạo `ab-1` và `AB-1` đều qua check rồi đều insert thành công. Constraint không bắt được vì khác chính tả.
  2. **Hiệu năng**: `upper(code)` không có index hỗ trợ nên seq scan trên skus của tenant. Với 20k SKU còn chấp nhận được, nhưng chạy trên mọi lần tạo/sửa/generate.
  3. **Không nhất quán với search**: exact match ở `search.go:291,296` là `s.code = $1 OR s.code = upper($1)`. Code lưu dạng hỗn hợp (`Ab-1`) sẽ không match exact khi gõ `ab-1`.
- Lưu ý: lint migration cấm expression index (`migrations_lint_test.go`), nên **không** thể dùng `UNIQUE (tenant_id, upper(code))`.
- Fix (theo convention `name_key`):
  - Migration mới thêm cột `code_key text NOT NULL`, giá trị do Go tính bằng `strings.ToUpper(strings.TrimSpace(code))`.
  - Backfill, thêm `UNIQUE (tenant_id, code_key)`, map `ux_skus__code_key` → `sku_code_taken`.
  - Thay mọi `upper(code)` bằng `code_key`. Search exact dùng `s.code_key = upper($1)`; prefix LIKE trên `code_key` cần `text_pattern_ops` hoặc collation C.
  - Phương án rẻ hơn: chuẩn hoá code về chữ hoa khi ghi (cần PO quyết định UX, vì người dùng có thể muốn giữ chữ thường).

### M2. `ReplaceVariantAxes` giữ SKU và variant_key cũ, không version, không If-Match (sai lệch #4)

- Vị trí: `catalog/variant_service.go:36-82`; test `catalog_integration_test.go:204-213`.
- Bằng chứng:
  - Sau khi thay trục, SKU cũ vẫn giữ `variant_key` theo trục cũ, và generate tạo thêm SKU theo trục mới.
  - Chính integration test tạo "KH-1" (key `""`) rồi generate biến thể, nên để lại SKU mồ côi không thuộc ma trận mới.
  - Product không bị bump version. Route `PUT /products/{id}/variant-axes` không yêu cầu If-Match. Audit là `product.axes_replace`.
- Tác động:
  - Ma trận biến thể chứa SKU "ma" không ánh xạ được về tổ hợp trục hiện tại.
  - UI preview và generate có thể hiển thị sai.
  - Hai tab sửa trục song song thì last-write-wins mà không có 412.
- Fix:
  - Khi `VariantAxisChangeSpec` cho phép thay (chưa có stock_moves): hoặc (a) chặn thay trục khi đã có SKU có variant_key khác rỗng, hoặc chỉ cho phép khi mọi SKU chưa dùng (`SkuInUse` false); hoặc (b) xoá hoặc archive SKU không thuộc ma trận mới, rồi tính lại `variant_key`.
  - Bump `products.version`, yêu cầu If-Match trên PUT variant-axes và trả ETag mới.

### M3. Độ phủ If-Match chưa đủ so với §3.6 ("mutations require If-Match")

- Vị trí:
  - `catalog/http.go`: If-Match chỉ có ở updateAttribute (~347), updateProduct (~624), updateSku (~824), updateMixGroup (~1005).
  - `partner/http.go`: chỉ có ở updateCustomer (234) và updateSupplier (316).
- Thiếu ở:
  - DELETE (products, skus, mix groups, customers, suppliers)
  - `:deactivate`/`:reactivate` của mix group (`SetMixGroupStatus`, `mix_groups.go:304-330`, bump version nhưng không nhận expected version)
  - `:stop`/`:reopen` của supplier (`suppliers.go` khoảng 450-487, gọi `updateParty(..., p.Version, ...)`, tức là tự lấy version hiện tại làm expected)
  - PUT variant-axes
- Đánh giá: `org/http.go` cũng làm vậy (chỉ PATCH có If-Match), nên đây là **convention toàn dự án**, không riêng phase 05.
  - Rủi ro thực tế thấp vì mô hình single owner, nhưng contract test §3.6 ("If-Match routes list 412 and 428") hiện chỉ đúng cho tập con.
- Fix: lead chọn một trong hai.
  - (a) Ghi rõ trong `architecture.md` §3.6 rằng If-Match chỉ áp dụng cho PATCH các aggregate có version, còn command và DELETE thì không.
  - (b) Mở rộng If-Match cho DELETE và các command thay đổi status. Với `:stop`, `:reopen`, `:deactivate`, `:reactivate` nên truyền expected từ header.

### M4. Search của catalog query thẳng bảng của partner (sai lệch #2)

- Vị trí: `catalog/search.go` khoảng 307-327 (`SearchParties`) đọc `parties`, `customer_profiles`, `supplier_profiles`.
- Đánh giá: không có lỗ bảo mật (RLS, tên bảng là hằng, tham số bind), nhưng vi phạm ranh giới module. Thay đổi schema partner (ví dụ thêm cột code, đổi `search_norm`) sẽ làm hỏng catalog mà compiler không báo.
- Fix:
  - Thêm port `PartySearcher` (khai báo trong catalog, partner implement) và wire ở `internal/app/domains.go`.
  - Chấp nhận tạm thời vì đã đi qua `SearchRepository`, nhưng nên tạo ticket refactor trước Phase 08/09 (khi POS và PO phụ thuộc nặng vào search).

### M5. Sandbox reset xoá thẳng dữ liệu catalog (sai lệch #8)

- Vị trí: `catalog/seed.go:151-164`.
- Bằng chứng: hàm xoá `skus`, `sku_units` và các bảng catalog khác. Contract `org/ports.go:65-72` nói dòng append-only được giữ, và resetter của domain sổ cái chỉ xoá dòng không bị tham chiếu.
- Tác động: khi đã có `stock_moves` hoặc chứng từ (Phase 07+), reset sẽ lỗi FK, làm hỏng toàn bộ thao tác reset sandbox. Nếu thứ tự resetter khác đi thì còn có thể xoá một phần.
- Ghi chú: đã có agent nền "Sandbox reset with ledger history" xử lý. Cần xác nhận fix đó bao phủ catalog: chỉ xoá SKU/product không có tham chiếu, các SKU còn lại thì archive/inactive. Cần thêm integration test reset sau khi có stock move.

### M6. Danh sách "đang sử dụng" là danh sách tĩnh, không có test chống trôi so với schema

- Vị trí: `catalog/usage_repo.go` (`skuReferences` 27 mục, `skuUnitReferences` 12 bảng, `mixGroupReferences`); `partner/repo.go:325-372` (SupplierInUse, CustomerInUse, CustomerGroupInUse); `org/reasons_repo.go` (`reasonInUse`, 9 bảng).
- Hiện trạng: đã đối chiếu tay. `skuUnitReferences` khớp mọi bảng có `unit_id` ở 000009–000029, và `reasonInUse` khớp FK. Nhưng Phase 06/07/10 đang thêm bảng song song.
  - Bảng có FK sẽ rơi về lỗi FK, được map thành `*_in_use`, nên vẫn an toàn.
  - Bảng có `unit_id` không FK thì lọt âm thầm (liên quan H1).
- Fix: thêm test migration-level so sánh `pg_constraint` (FK tới skus, products, parties, reasons, mix_groups) và `information_schema.columns` (cột `unit_id`) với các danh sách tĩnh, fail khi thiếu.

### M7. Quick-create sinh mã `SP-N` có race và scan toàn bảng

- Vị trí: `catalog/quick_create.go:105`: `SELECT code FROM skus WHERE upper(code) LIKE $1 || '-%'`, sau đó tính max+1 trong Go.
- Vấn đề: hai quick-create song song (hai thu ngân hoặc hai tab PO) nhận cùng `SP-N`. Request sau lỗi `sku_code_taken` mà không retry. Đây là luồng nhanh từ dòng PO, nên người dùng sẽ thấy lỗi khó hiểu.
- Fix: dùng sequence hoặc counter theo tenant (bảng `code_sequences` nếu đã có ở org, hoặc `pg_advisory_xact_lock(hashtext('sku-code-SP'))` trong tx), hoặc retry 3 lần khi gặp `ux_skus__code`. Kết hợp M1 để LIKE dùng `code_key`.

---

## Low

| # | Vị trí | Vấn đề | Fix |
|---|--------|--------|-----|
| L1 | `partner/dedupe.go:104-112` | `ORDER BY p.id LIMIT 50` chạy **trước** khi chấm điểm. Khi có >50 party trùng tên gần đúng, các ứng viên trùng SĐT hoặc MST có thể bị cắt. Không có index `tax_code` (000004 chỉ có `ix_parties__name`, `ix_parties__phone`). | Ưu tiên `ORDER BY (phone_key = $2) DESC, (tax_code = $3) DESC` trước LIMIT, hoặc tách query exact (phone/tax) khỏi query fuzzy (name). Thêm `ix_parties__tax_code`. |
| L2 | Audit: `sku.unit_update`, `sku.unit_remove`, `sku.barcode_add`, `sku.barcode_remove`, `product.image_add`, `product.image_remove`, `product.axes_replace`, `product.quick_create` | Không đúng dạng `entity.verb`. Dạng đang dùng là `entity.noun_verb`. | Đổi sang `sku_unit.update`, `sku_barcode.add`, `product_image.add`, `product_variant_axes.replace`…, hoặc chính thức chấp nhận dạng `entity.sub_verb` trong design doc. Nên chốt trước khi log audit tích luỹ. |
| L3 | `partner` (`Opt[T]`) | Trùng với `Opt[T]` của catalog (sai lệch #9). Agent Phase 06 cũng đang đọc helper này, nên khả năng nhân bản lần 3. | Chuyển vào `internal/platform/httpx` (hoặc `platform/opt`) và dùng chung. |
| L4 | `catalog/search.go` (`RankCandidates`, khoảng 353-415) | `SearchNorm` được gọi trong comparator của sort (O(n log n) lần normalise). Pre-limit 200 dựa trên `length(search_norm)` có thể đẩy kết quả khớp tốt nhưng tên dài ra ngoài top 200. Benchmark p95 66–94 ms/20k SKU vẫn đạt. | Tính norm một lần trước khi sort. Theo dõi chất lượng ranking. |
| L5 | `catalog/search.go` `SearchParties` | Tax-code exact match dùng input thô, không uppercase hoặc trim như lúc ghi (`service.go` regex `^[0-9A-Z]…`). | Áp cùng hàm normalise tax code trước khi so sánh. |
| L6 | `catalog/skus.go` `UpdateSku` | `qty_scale` đổi được khi đã có tồn, nên số lẻ lịch sử có thể hiển thị hoặc làm tròn sai. | Đưa `qty_scale` vào `UnitConversionSpec`, khoá khi có stock_moves. |
| L7 | `catalog/mix.go` `checkMixGroup` | Chấp nhận SKU vị đang inactive. | Từ chối SKU inactive khi tạo hoặc sửa mix group (hoặc chỉ cảnh báo). |
| L8 | `org/reasons.go` `UpdateReason` | Cho đổi tên lý do hệ thống (seed), có thể làm sai báo cáo nếu báo cáo dựa trên tên. | Khoá `name` với `is_system`, chỉ cho bật/tắt. |
| L9 | `catalog/quick_create.go` | `s.prices` là nil đến Phase 06, nên giá bán bị bỏ âm thầm (`SalePriceSaved=false`). Đúng plan bước 11, nhưng UI cần đọc cờ này. | Đảm bảo Phase 06 wire `PriceWriter` trong `domains.go`, và UI hiển thị cảnh báo khi `SalePriceSaved=false`. |
| L10 | `migrations/000004` | Comment `search_norm` ghi "name + phone + tax code" nhưng code còn gộp ghi chú hàng cung cấp (`partySearchNorm`). | Sửa comment hoặc doc schema. |
| L11 | `design/architecture.md` (attribute registry) | Ghi "number(+unit_label, min/max)", plan dòng 51 cũng vậy, nhưng schema và code không có min/max. CAT-03 ghi "min/max stock" nhưng chỉ có `min_stock`. | Cập nhật doc cho khớp quyết định (xem sai lệch #7), hoặc lập ticket bổ sung. |

---

## Đánh giá các sai lệch executor đã khai báo

| # | Sai lệch | Kết luận | Ghi chú |
|---|----------|----------|---------|
| 1 | SKU code unique case-insensitive chỉ bằng Go check | **Không chấp nhận như hiện tại**. Xem M1. | Lint cấm expression index, nên dùng cột `code_key` theo convention `name_key`. |
| 2 | Catalog search query thẳng `parties` | **Chấp nhận tạm thời**. Xem M4. | Không có rủi ro bảo mật; nợ kiến trúc, nên refactor sang port `PartySearcher`. |
| 3 | Không có kind `exchange`, bỏ lý do "Đổi màu / đổi mẫu" | **Chấp nhận không thêm kind**, nhưng **không nên bỏ lý do**. | Design `sales_returns` có `reason_id NOT NULL` và `exchange_id`, nên phần trả của đổi hàng chính là một sales_return. Prototype (`prototype-v6.14.html:4033`) dùng lý do này trong nháp đổi hàng. Khuyến nghị seed "Đổi màu / đổi mẫu" với kind `sales_return` trong `reasons.go` (khoảng 188-200). |
| 4a | Thay trục biến thể giữ variant_key cũ | **Lỗi**. Xem M2. | |
| 4b | Quick-create không có attribute set | **Chấp nhận**. | CAT-11 chỉ yêu cầu tên, đơn vị, quy đổi và giá. Code ghi chú rõ đây là cố ý. |
| 4c | Quy tắc đổi base unit | **Lỗi**. Xem H1, H2. | Guard chỉ kiểm stock_moves là không đủ. |
| 5a | `parties` không có cột code | **Chấp nhận**. | Design schema không có cột code cho parties. |
| 5b | Customer đổi status qua PATCH, supplier qua `:stop`/`:reopen` | **Chấp nhận, nên ghi vào doc**. | Khớp plan (route `:stop|reopen` chỉ cho suppliers) và PRD CAT-05. Không đồng nhất nhưng có chủ đích. |
| 6a | Reasons không có version nên không có If-Match | **Chấp nhận**. | Bảng `reasons` trong design không có version. Rủi ro thấp (single owner). |
| 6b | Catalog/partner không có reauth | **Đúng**. | §3.6 và SEC-06 chỉ yêu cầu reauth cho chuyển quyền sở hữu, mở lại kỳ, write-off, export dữ liệu và reset MFA. |
| 7 | Không có min/max cho thuộc tính số, không có max stock | **Chấp nhận theo schema**, nhưng doc lệch. Xem L11. | Cần chốt: cập nhật architecture và plan, hoặc lập ticket. |
| 8 | Sandbox reset xoá thẳng catalog | **Rủi ro thật**. Xem M5. | Đang có agent xử lý; cần test sau khi có stock move. |
| 9a | `Opt[T]` trùng trong partner | **Low**. Xem L3. | |
| 9b | `parties.search_norm` gồm ghi chú hàng cung cấp | **Chấp nhận** (PRD CAT-05 cho tìm NCC theo mặt hàng). | Sửa comment migration (L10). |

---

## Các hạng mục kiểm tra khác

- **Tenant isolation**: OK.
  - Mọi query chạy trong `WithTenantTx` hoặc `WithReadOnlyTenantTx`, FORCE RLS, role NOBYPASSRLS. Có test tách tenant.
  - Contact và address dùng `WHERE id=$1 AND party_id=$2` (`partner/repo.go:268-292`).
- **Injection**: OK.
  - `textnorm` loại bỏ `%` và `_`, nên không có LIKE wildcard injection.
  - Filter thuộc tính kiểm key thuộc tập filterable trước khi build `@>` JSON.
  - `searchPatterns` giới hạn 400 byte.
- **Mass assignment**: OK. Input là struct riêng (`SkuInput`, `SkuPatch`, `Opt[T]`), không bind thẳng vào model; `version`, `tenant_id` và `id` không nhận từ body.
- **ETag/If-Match**: hoạt động đúng trên các PATCH (412 khi lệch, 428 khi thiếu, có test ma trận ở `app/catalog_partner_integration_test.go`). Độ phủ xem M3.
- **Idempotency**: catalog/partner không thuộc nhóm posting/payment, nên không bắt buộc Idempotency-Key theo §3.6. Ngoại lệ đáng cân nhắc là quick-create (gọi từ dòng PO, retry mạng tạo trùng product). `name_key` unique sẽ chặn trùng, nhưng request retry nhận 409 thay vì kết quả cũ. Đây là UX, không phải dữ liệu sai.
- **Chuẩn hoá tiếng Việt**: `textnorm.Key`, `SearchNorm` và `PhoneKey` bỏ dấu (bao gồm đ→d) và hạ chữ thường. `name_key` unique cho product khớp CAT-11.
- **Hiệu năng search**: p95 66–94 ms ở 20k SKU (`plans/reports/fullstack-developer-261017-0148-search-benchmark-p95.md`), đạt mục tiêu. Điểm nóng là các query `upper(code)` (M1) và dedupe không có index tax_code (L1).
- **Error contract §3.6**: các lỗi domain trả đúng `{"error":{"code","message","fields"}}`, 422 `validation_failed` có `fields`, 409 `*_in_use` và `sku_code_taken`. Không thấy lộ stack trace hay lỗi pg thô.

## Khoảng trống test

1. Không có test cho `UpdateSku` với `BaseUnitID` (nhánh A và B của `changeBaseUnit`), nhất là khi đã có giá hoặc đơn nháp (H1).
2. Không có test sửa factor, inactive hoặc xoá unit đang là `piece_unit_id` của mix group (H2).
3. Không có test race hai code chỉ khác hoa thường (M1), và race quick-create `SP-N` (M7).
4. Không có test khẳng định số SKU sau `ReplaceVariantAxes` rồi generate; test hiện tại vô tình chứng minh có SKU mồ côi (M2).
5. Không có guard test giữa các danh sách usage tĩnh và schema (M6).
6. Không có test sandbox reset khi đã có stock move hoặc chứng từ (M5).
7. Không có test dedupe với >50 ứng viên trùng tên (L1).

---

## Danh sách sửa đề xuất (theo ưu tiên)

1. **H1**: Chặn đổi base unit khi SKU đang được dùng (`SkuInUse`), hoặc mở rộng `CheckBaseChange` sang giá, dòng chứng từ nháp và mix group. Thêm test cả 2 nhánh.
2. **H2**: Kiểm `mix_groups.piece_unit_id` trong `UpdateSkuUnit`, `DeleteSkuUnit` và `changeBaseUnit`. Thêm test.
3. **M1**: Migration thêm `skus.code_key` + `UNIQUE (tenant_id, code_key)`, map lỗi `sku_code_taken`, thay mọi `upper(code)`, sửa exact match trong search.
4. **M2**: Xử lý SKU mồ côi khi thay trục biến thể. Bump version product, yêu cầu If-Match trên PUT variant-axes.
5. **M5**: Xác nhận fix sandbox reset (agent nền) bao phủ catalog, kèm test reset sau stock move.
6. **M6**: Thêm test đối chiếu danh sách usage tĩnh với `pg_constraint` và cột `unit_id`.
7. **M3**: Lead chốt chính sách If-Match cho DELETE và command. Cập nhật §3.6 hoặc mở rộng code và contract test.
8. **M4**: Tách `PartySearcher` port do partner implement, wire ở `domains.go`.
9. **M7**: Sinh mã `SP-N` bằng sequence hoặc advisory lock, hoặc retry khi trùng.
10. **Sai lệch #3**: Seed lý do "Đổi màu / đổi mẫu" với kind `sales_return`.
11. **Low**: L1 (dedupe + index tax_code), L2 (chốt quy ước tên audit), L3 (gộp `Opt[T]`), L5, L6, L7, L8, L4, L9, rồi L10/L11 (doc).

## Theo dõi plan

- Checklist phase 05 đánh dấu xong đều có file và test chứng minh tồn tại. Tuy vậy, mục "edit-rule Specifications" chưa thể coi là xong hoàn toàn cho tới khi H1/H2 được sửa, vì spec đổi đơn vị đang thiếu điều kiện.
- Không sửa plan; để lead hoặc planner cập nhật.

## Câu hỏi còn mở

1. Code SKU có nên lưu nguyên chữ người dùng gõ (cần `code_key`) hay chuẩn hoá chữ hoa khi ghi?
2. Chính sách If-Match cho DELETE và command (M3) là quyết định toàn dự án. Lead hay user chốt?
3. Khi thay trục biến thể, SKU cũ nên bị chặn, archive hay xoá (M2)? Đây là quyết định UX.
