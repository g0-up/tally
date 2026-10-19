# Code Review — Phase 06: Pricing & Promotions

- Thời điểm: 2026-10-17 (Asia/Saigon)
- Reviewer: code-reviewer (read-only; không sửa code, không commit, không chạm DB dev `tally-postgres-1`)
- Đối chiếu:
  - `phase-06-pricing.md`
  - `design/database-conventions.md`, `design/database-schema-catalog-pricing.md` §5–6, `design/database-schema-sales-kits.md` (header), `design/database-schema-org-partners.md`, `design/architecture.md`
  - Báo cáo implementer `plans/reports/fullstack-developer-261017-1240-pricing-promotions.md`

## Phạm vi

- `apps/api/internal/pricing/**`, `apps/api/internal/platform/money/**`
- `apps/api/internal/app/` (phần pricing): `domains.go`, `routes.go`, `app.go`, `pricing_integration_test.go`, `openapi_contract_test.go`
- `apps/api/internal/cli/serve.go`
- `apps/api/internal/catalog/` (phần `price_hint`, quick-create `SetSalePrice`)
- `apps/api/api/openapi.yaml` (phần pricing)
- `apps/api/migrations/000009_pricing.up.sql`

Kiểm tra đã chạy (Postgres riêng qua testcontainers, không chạm DB dev):

| Lệnh | Kết quả |
|------|---------|
| `go vet` pricing, money, app, catalog, cli (có và không `-tags integration`) | sạch |
| `golangci-lint run` pricing, money (có và không `--build-tags integration`) | 0 issue |
| `go test -race -count=1` pricing, money, catalog | ok |
| `go test -race -count=1 -tags integration` pricing, money | ok |
| `go test -race -count=1 -tags integration -run 'Pricing\|Price\|Promotion\|Quote\|Calculate' ./internal/app/` | ok (`TestPricingOverHTTP` PASS) |
| `go test ./internal/app/` (`TestOpenAPIContract`) | **FAIL**, nhưng nằm ngoài phạm vi: mọi route lỗi đều là debt/cash (`/funds`, `/statements`, `/allocations`, `/refunds`, …). Route pricing đã có đủ trong spec. |
| `go test ./internal/cli/` (`TestCommandsNotImplementedYet`) | **FAIL**, ngoài phạm vi: `ledger verify` vừa được agent khác implement (`cli/ledger.go` sửa lúc 13:59), test cũ còn chờ "not implemented". |

## Đánh giá tổng quan

Phần nền tảng chắc chắn:

- **Money**:
  - Kiểu giá trị 4dp, Factor 6dp; chỉ parse từ chuỗi JSON; làm tròn half-away-from-zero.
  - `Allocate` dùng largest remainder, không bao giờ cấp vượt phần gốc.
  - `RoundVNDKeepTotal` giữ đúng tổng và không làm net âm.
- **Resolver**: thứ tự Customer → Group → Season → Base khớp design §5.1.
  - Ưu tiên màu riêng > màu chung, dòng theo đơn vị > base×factor, bậc min_qty lớn nhất.
  - Load toàn bộ list và row của chứng từ trong 2 query, không có N+1.
- **RLS**: FORCE trên mọi bảng pricing. `price_changes` bị REVOKE UPDATE/DELETE (append-only).
- **Khoá và lượt dùng mã**:
  - `RecordUsage` khoá promotion `FOR UPDATE` và idempotent.
  - `putRow` retry khi race insert, chạy đúng dưới READ COMMITTED.

Có **một lỗi nghiệp vụ thật** trong engine khuyến mãi (H1): quà tặng không áp. Phần còn lại là rủi ro Low hoặc câu hỏi sản phẩm. Không có lỗi Critical. Không thấy lỗ tenant isolation.

---

## Critical

Không có.

---

## High

### H1. Khuyến mãi tặng quà không cộng dồn (mặc định) mà SKU quà không có giá bán thì không bao giờ được áp

- Vị trí:
  - `pricing/promotion.go:486-503` (`Evaluate`, chọn giữa "single" và "stack")
  - `pricing/promotion.go:283-306` (`giftRule.Apply`, `Benefit` = tổng giá quà)
  - `pricing/resolver.go:306-323` (không có dòng giá thì `Amount` = 0, source manual)
  - `pricing/calculator.go:375-405`, `511-516` (`giftPrices` lấy giá quà qua resolver)
- Bằng chứng:
  ```go
  if best >= 0 && singles[best].Benefit.Cmp(stackBenefit) > 0 {   // 0 > 0 == false
      out.Applied = []promoEffect{singles[best]} ...
  }
  out.Applied = stacked                                               // rỗng
  for _, eff := range singles {
      out.Skipped = append(out.Skipped, skipped(eff.Promotion, SkipNotStackable))
  }
  ```
- Kịch bản lỗi:
  1. Owner tạo khuyến mãi "Mua 6 tặng 1 móc khoá". Cột `stackable` mặc định `false` (migration 000009); SKU móc khoá là hàng tặng nên không có giá bán.
  2. `giftPrices` trả 0, nên `Benefit` = 0.
  3. Không có khuyến mãi cộng dồn nào, nên `stackBenefit` = 0. Phép so sánh `0 > 0` sai, nhánh stack (rỗng) thắng.
  4. Quà bị đẩy vào `skipped` với lý do `not_stackable`, dù chẳng có gì để cộng dồn cùng nó.
- Tác động:
  - Thu ngân không thấy dòng quà, khách mất quà.
  - Lý do trả về (`not_stackable`) gây hiểu nhầm.
  - Cùng khuyến mãi đó nếu đặt `stackable=true` thì lại được áp, vì nhánh stack không lọc theo benefit. Hành vi không nhất quán.
  - Trường hợp này phổ biến: hàng tặng thường không có giá bán.
- Test hiện có không bắt được: `TestGiftRuleSetsAndCap` và `TestCalculateGiftThenOrderDiscount` đều đặt giá quà dương và `stackable=true`.
- Fix khuyến nghị:
  - Khi `len(stacked) == 0 && best >= 0`, áp `singles[best]`.
  - Hoặc tổng quát hơn: single thắng khi `Benefit > stackBenefit`, hoặc khi bằng nhau mà stack rỗng.
  - Thêm 2 unit test: gift non-stackable giá 0 đứng một mình phải được áp; gift giá 0 so với một stack có benefit dương thì stack thắng (giữ hành vi hiện tại).
  - Cân nhắc thêm: lấy giá trị quà theo giá vốn (`ref_cost`) khi không có giá bán, để `max_discount` và so sánh benefit có nghĩa. Đây là quyết định sản phẩm.

---

## Medium

Không có.

---

## Low

### L1. Deadlock có thể xảy ra giữa `SetSalePrice` và `PutPriceListItems` trên bảng giá gốc, trả 500

- Vị trí:
  - `pricing/price_lists.go:317-345`: khoá dòng `price_lists` `FOR UPDATE` (`repo.go:130`), rồi `putRow` khoá từng dòng theo thứ tự trong request.
  - `pricing/writer.go:213-289` (`PriceWriter.SetSalePrice`): không khoá `price_lists`. Khoá dòng chung trước (`lockRow`, `writer.go:79-85`), rồi với `apply_all_colors` khoá mọi dòng màu `ORDER BY color_id` (`writer.go:271-277`).
- Kịch bản:
  1. Tab A gửi PUT `/price-lists/{retail}/items` với [dòng màu X, dòng chung G].
  2. Đồng thời, tab B lưu giá từ màn sản phẩm với `apply_all_colors` cho cùng SKU.
  3. A giữ X và chờ G; B giữ G và chờ X. Postgres trả 40P01.
  4. Pricing không retry (`db.IsRetryable` chỉ được dùng trong `posting/coordinator.go:103`), nên người dùng nhận 500 `internal_error`.
- Tác động thấp: hiện chỉ có principal owner, xác suất cần hai thiết bị cùng sửa một SKU. Không mất dữ liệu.
- Fix: cho `SetSalePrice` khoá dòng `price_lists` (`readPriceList(..., true)`) giống `PutPriceListItems`, để serialize mọi ghi vào một list. Cách khác: retry khi `IsRetryable` ở `Service.write`.

### L2. `PutPriceListItems` tốn khoảng 3 round-trip mỗi dòng

- Vị trí: `pricing/price_lists.go:337-341` gọi `putRow` (lock, insert/update, ghi `price_changes`) cho từng dòng.
- Kịch bản: import 500 dòng tốn khoảng 1500 query trong một transaction, giữ khoá list suốt thời gian đó. Ở RTT 1 ms, mất khoảng 1,5 s. Không sai dữ liệu.
- Fix (khi cần): load trước mọi dòng hiện có bằng một `SELECT … FOR UPDATE` theo `(sku, color, unit, min_qty)`, rồi batch insert/update và batch insert history.

### L3. Có thể dò mã khuyến mãi qua `/pricing/calculate`, vòng qua rate limit của validate-code

- Vị trí:
  - `pricing/http.go:64` (`POST /pricing/calculate` không có limiter riêng)
  - `pricing/http.go:68` (`validate-code` có limiter 1/s, burst 30)
  - `pricing/calculator.go:296`, `346-357` (tối đa 10 mã mỗi request; mã không tồn tại trả 422 `not_found` theo từng mã)
- Kịch bản: mỗi request `calculate` thử 10 mã, chỉ bị giới hạn bởi limiter chung của API. Tốc độ dò cao hơn khoảng 10 lần so với `validate-code`.
- Tác động thấp: principal duy nhất là owner, vốn đã liệt kê được mọi promotion qua `GET /promotions`. Rủi ro chỉ thành thật khi có role thu ngân hoặc khách tự áp mã.
- Fix (khi có role mới): dùng chung `codeLimiter` cho `calculate` khi body có `promotion_codes`, hoặc gộp lỗi mã sai thành một thông báo chung.

### L4. Resolver và calculator không kiểm trạng thái SKU; màu không được kiểm là thuộc SKU

- Vị trí:
  - `pricing/repo.go:40-64`: `loadSkuInfo` đọc `s.status` vào `skuInfo.Status`, nhưng không chỗ nào trong package đọc lại trường này.
  - `pricing/price_lists.go:361`, `387`: `itemKeys` chỉ kiểm màu tồn tại trong tenant (`existingIDs("colors")`).
- Kịch bản:
  - `POST /pricing/calculate` với SKU đã archived vẫn trả giá bình thường.
  - PUT dòng giá cho SKU áo với `color_id` của một màu SKU đó không có tạo được một dòng "chết", không bao giờ match.
- Tác động thấp: việc chặn SKU archived khi tạo chứng từ là trách nhiệm của sales (phase sau), và dòng màu chết không làm sai giá. Cần ghi nhận để phase sales không tưởng pricing đã chặn.
- Fix: hoặc bỏ đọc `Status` (dead data), hoặc trả lỗi `sku_inactive` trong `prepare`. Lead chốt chủ sở hữu quy tắc. Kiểm màu thuộc SKU qua `sku_colors` (hoặc bảng tương đương) nếu catalog có.

### L5. Nhánh `ReadOnly` của `readPriced` không bao giờ chạy cho quote/calculate

- Vị trí: `pricing/service.go:141-151`; `platform/middleware/support_readonly.go` trả 403 cho support trên mọi method không phải GET.
- Tác động: `POST /pricing/quote` và `POST /pricing/calculate` là thao tác chỉ đọc, nhưng support session không gọi được, nên nhánh này là code chết đối với hai route đó. Không gây lỗi.
- Fix: nếu muốn support xem được tính toán, cho phép hai route POST "safe" này trong `SupportReadOnly` (allowlist). Không thì ghi rõ trong doc rằng support không dùng được.

---

## Informational / câu hỏi sản phẩm

1. **Giá mùa áp cho mọi khách, kể cả khách sỉ.**
   - `resolver.go:132-133` và chuỗi tại `resolver.go:215` đặt Season trên Base cho mọi buyer, đúng design §5.1.
   - Hệ quả: nếu giá mùa cao hơn giá sỉ thì khách sỉ không có bảng giá nhóm sẽ trả giá mùa (đắt hơn).
   - Cần owner xác nhận đây là ý đồ, hoặc thêm `base_price_kind` cho season list.
2. **`DocumentRounding` suy ngược gross** (`calculator.go:176-179`), nên `gross` có thể lệch `unit_price × qty` vài đồng. Tổng chứng từ vẫn đúng, nhưng UI và in ấn nên hiển thị đúng con số API trả về.
3. **PATCH price list gửi lại đúng `kind`, `customer_group_id` hoặc `party_id` hiện có vẫn nhận 422 `immutable`** (`dto.go`). Client phải bỏ các trường này khỏi body. Nên ghi vào OpenAPI.
4. Các sai lệch implementer đã khai đều được chấp nhận:
   - 409 cho `exception_required` (đã chốt, không xét lại).
   - Ngân sách 20 ms chỉ đo khi không có `-race`.
   - DELETE `/price-lists/{id}` không có If-Match (chỉ xoá được list rỗng và chưa có lịch sử).

---

## Các hạng mục kiểm tra khác

- **Tenant isolation**: OK.
  - Mọi truy cập chạy trong `WithTenantTx` hoặc `WithReadOnlyTenantTx`; FORCE RLS trên `price_lists`, `price_list_items`, `price_changes`, `promotions`, `promotion_items`, `promotion_usages`.
  - FK composite `(tenant_id, id)`. Không có query nào thiếu ngữ cảnh tenant.
- **Tiền và làm tròn**: OK.
  - Không có float. Mọi phép nhân đều làm tròn về 4dp.
  - `BaseQty` từ chối kết quả không vừa 4dp.
  - Giảm tay ≤ gross. Phân bổ giảm đơn theo giá trị dòng có `fitWithin`, net không âm.
  - Kiểm min_price so net chính xác với `min_price × base_qty`.
- **Hiệu năng quote/calculate**: list và row của cả chứng từ load trong 2 query, giá quà chung một lượt resolver; giới hạn 500 dòng, 200 item khuyến mãi, 10 mã.
- **Contract**: route pricing có đủ trong `openapi.yaml`. Lỗi trả đúng `{"error":{code,message,fields}}`: 422 `validation_failed` có fields, 409 `base_price_list_exists`, `base_price_list_locked`, `price_list_in_use`, `code_taken`, `promotion_usage_limit_reached`. Không lộ lỗi pg thô.
- **Catalog price hint và quick-create**:
  - `catalog/search.go:406-415` dùng `hintResolver` (chỉ list gốc, giá lẻ). Ngày cắt theo clock không sai, vì list gốc không có ngày hiệu lực.
  - Quick-create gọi `SetSalePrice` trong cùng transaction catalog (`catalog/quick_create.go:136-141`), unit = đơn vị gốc, source `catalog`.
- **Idempotency và đồng thời mã khuyến mãi**: `RecordUsage` khoá promotion, đếm rồi insert idempotent, nên không vượt `usage_limit` khi post song song.

## Khoảng trống test

1. Gift non-stackable với giá quà = 0 (H1); gift non-stackable có giá dương mà không có khuyến mãi nào khác.
2. Ghi đồng thời `SetSalePrice(apply_all_colors)` và `PutPriceListItems` trên cùng SKU (L1).
3. Calculate với SKU archived, và PUT dòng giá với màu không thuộc SKU (L4).

## Danh sách sửa đề xuất (theo ưu tiên)

1. **H1**: sửa điều kiện chọn single và stack trong `Evaluate`, thêm test regression.
2. **L1**: khoá `price_lists` trong `SetSalePrice`, hoặc retry 40P01/40001 ở `Service.write`.
3. **L4**: chốt chủ sở hữu quy tắc SKU archived (pricing hay sales), bỏ dead field hoặc enforce.
4. **L5, L3, L2**: xử lý khi có role support hoặc thu ngân, hoặc khi đo được import chậm.
5. Câu hỏi sản phẩm 1 (giá mùa cho khách sỉ): cần owner chốt.

## Theo dõi plan

- Checklist phase 06 có file và test chứng minh: money VO, resolver chain, promotion engine, document calculator, min-price guard, price writer và history, seed, HTTP và OpenAPI, quick-create và price hint.
- Mục "PromotionEngine" chưa nên coi là xong hẳn cho tới khi sửa H1.
- Không sửa plan; để lead hoặc planner cập nhật.

## Câu hỏi còn mở

1. Giá mùa có áp cho khách sỉ không, hay season list cần `base_price_kind`?
2. Khi SKU quà không có giá bán, giá trị quà (dùng cho `max_discount` và so sánh benefit) nên lấy theo giá vốn hay giữ bằng 0?
3. Chặn SKU archived khi tính giá là việc của pricing hay của sales?

Status: DONE_WITH_CONCERNS
Summary: Pricing & Promotions chắc về tiền, làm tròn, RLS và đồng thời mã khuyến mãi; vet, lint và test (unit + integration) của pricing và money đều xanh. Có 1 lỗi High: khuyến mãi tặng quà non-stackable với SKU quà không có giá bán không bao giờ được áp.
Concerns/Blockers: H1 cần sửa trước khi đóng phase. `TestOpenAPIContract` (route debt/cash chưa có trong spec) và `TestCommandsNotImplementedYet` (`ledger verify` đã được implement) đang fail do các agent song song, ngoài phạm vi pricing.
