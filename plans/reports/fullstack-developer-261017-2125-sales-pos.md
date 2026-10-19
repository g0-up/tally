# Báo cáo Phase 09: Bán hàng, POS, trả hàng và đổi hàng

- Plan: `plans/261016-0854-tally-full-product-implementation/`
- Phase: `phase-09-sales-pos.md`
- Trạng thái: **DONE_WITH_CONCERNS**. Toàn bộ phạm vi sales đã xong, mọi gate của sales đều xanh, và cả 12 todo đã được tick dựa trên test thật. Còn một test inventory đang đỏ, nguyên nhân là migration procurement 000039 (không thuộc phase này, chi tiết ở mục Rủi ro).
- Frontmatter phase vẫn để `status: todo`. Tôi không đổi Status trong `plan.md`.

## File đã làm

**Domain `apps/api/internal/sales/`** (đã bỏ build tag)

- Nghiệp vụ: `carts.go`, `command.go`, `confirm.go`, `date.go`, `documents.go`, `dto.go`, `errors.go`, `exchange.go`, `http.go`, `invoices.go`, `lines.go`, `model.go`, `offline_sync.go`, `ports.go`, `pos.go`, `pos_bootstrap.go`, `print.go`, `quotes.go`, `repo.go`, `returns.go`, `service.go`, `shift.go`, `status.go`, `stock_out.go`, `verify.go`.
- Unit test: `exchange_test.go`, `helpers_test.go`, `offline_sync_test.go`, `pos_test.go`, `print_test.go`, `returns_test.go`.

**Migrations**

- `apps/api/migrations/000040_sales_document_cash_tendered.{up,down}.sql`
- `apps/api/migrations/000041_sales_document_offline_client.{up,down}.sql`

**Wiring**

- `internal/app/sales_ports.go`: mới, 480 dòng. Là adapter cho các port inventory, debt, cash, pricing, catalog, partner và sessions.
- `internal/app/app.go`: thêm `Modules.Sales`.
- `internal/app/domains.go`: thêm `Domains.Sales`.
- `internal/app/routes.go`: đăng ký route sales.
- `internal/cli/ledger.go`: `ledger verify` chạy thêm `sales.Verify`.
- `internal/cli/worker.go`: thêm job `--shift-interval` để đóng các ca POS còn mở từ ngày kinh doanh trước.

**Contract**

- `apps/api/api/openapi.yaml`
- `apps/web/src/shared/api/schema.d.ts` (sinh lại)

**Integration test**

- `internal/app/sales_integration_test.go`: mới, khoảng 1 285 dòng.

**Sửa nhỏ ngoài phạm vi chính** (tương thích ngược, đã có test)

- `internal/cash/queries.go`: wrapper `PaymentMethods`.
- `internal/cli/serve.go`: `Modules.Sales`.
- `internal/cli/serve_test.go`: thêm `--shift-interval=0s`.
- `internal/cli/cli_integration_test.go`: thêm field `shift: time.Hour` vào literal `workerSchedule`. Thiếu field này thì `NewTicker` panic vì interval bằng 0.
- `openapi_contract_test.go`: thêm handler Sales.

**Phase file**

- `phase-09-sales-pos.md`: tick 12 todo (dòng 105-116).

## Lệnh đã chạy và kết quả thật

| Lệnh (trong `apps/api` hoặc `apps/web`) | Kết quả |
|---|---|
| `gofmt -l ./internal` | rỗng |
| `golangci-lint run --build-tags integration ./...` | `0 issues.` |
| `go build ./...` và `go test -race -count=1 ./...` | toàn bộ `ok` |
| `go test -tags integration -count=1 ./internal/app/... ./internal/cli/... ./internal/sales/... ./migrations/...` | toàn bộ `ok` (app 29.5s, cli 7.7s, migrations 13.6s) |
| `go test -tags integration -count=1 -v -run 'Sales\|Concurrent' ./internal/app/` | 25 PASS, 0 FAIL |
| `go test -tags integration -run TestRollingBackMaxStockRefusesAppliedCostRecalculations ./internal/inventory/` | **FAIL** (không do sales gây ra, xem Rủi ro 1) |
| `npx tsc -b` | exit 0 |
| `npx vitest run` | 26 file, 234 test pass |
| `npx eslint .` | sạch |

Integration test chạy trên testcontainers (`postgres:16-alpine`). Tôi không đụng DB dev `tally-postgres-1`, và không để lại process nền nào; ryuk tự thoát.

## Bằng chứng cho từng tiêu chí thành công

Các test dưới đây nằm trong `sales_integration_test.go`.

- **Trả 3 "đỏ" khi chỉ bán 2**: `TestSalesOverHTTP/a return takes back at most…` nhận `409 return_exceeds_sold`.
- **Đổi hàng 500 000 lấy 650 000**: subtest exchange kiểm:
  - preview ra "Khách trả thêm 150 000";
  - posting tạo đúng một TH, một BH (`source = exchange`) và một DH;
  - replay cùng key không tạo thêm dòng nào (đã đếm số dòng).
- **Batch offline gửi hai lần**:
  - chỉ post một lần;
  - bán nợ và chuyển khoản bị đưa vào hàng ngoại lệ với `offline_payment_not_allowed`.
- **20 lượt checkout đồng thời trên 10 đơn vị**: `TestConcurrentCheckoutsNeverOversell` cho kết quả:
  - đúng 10 lượt thành công, các lượt còn lại `409 insufficient_stock`;
  - tồn về 0, không âm;
  - 10 chứng từ, 1 ca;
  - verify sạch.
- **POS tự chọn màu duy nhất còn lại**: subtest POS.
- **Confirm khi dòng có màu mà chưa chọn màu → `422 color_required`**: subtest mới `an invoice confirms once it names its color and brings the owner's confirmations` kiểm toàn bộ luồng:
  1. `:check` báo đủ ba thứ: `color_required_line_nos=[1]`, `below_min_price_line_nos=[1]` và `credit_limit_exceeded`.
  2. Confirm khi chưa chọn màu nhận `422 color_required`. Hóa đơn không tự chọn màu, kể cả khi chỉ còn một màu; đây là chủ ý theo spec.
  3. Sau khi chọn màu, confirm không kèm xác nhận nhận `409 credit_limit_exceeded`.
  4. Chỉ kèm xác nhận credit thì nhận `409 below_min_price`.
  5. Kèm đủ hai xác nhận của chủ (POST `/exception-confirmations`, `payload_hash` ràng vào body) thì nhận `200`. Kết quả: `current_version=1`, dòng có màu đỏ, giữ chỗ 2, và 2 bản ghi `exception_confirmation_uses`.
- **Hóa đơn 10 000 000 đã trả 3 000 000, xuất kho 60% rồi 40%**: subtest mới `a deposit is spread over the stock outs that deliver the invoice` kiểm:
  - hai BH, tổng đã trả 3 000 000, tổng còn nợ 7 000 000;
  - hóa đơn `completed` ở trạng thái `partial`;
  - khoản đặt cọc được phân bổ hết.
- **Kỳ đã khóa**: `TestSalesPostingInLockedPeriodIsRefused` nhận `423 period_locked`.
- **Verify bắt được dữ liệu bị sửa**: `TestSalesVerifyReportsTamperedRows` phát hiện sai ở cogs, `returned_base_qty`, `source_id` của khoản phải thu và tổng tiền phiếu trả.
- **Sổ cái khớp sau mỗi luồng**: sales, procurement, inventory, debt và cash đều verify sạch.

## Sai khác so với plan hoặc spec

1. **Thêm `postRefused`** (`sales/command.go`). Khi kiểm tra trước giao dịch của return, exchange hoặc thanh toán hóa đơn trả về Conflict, request vẫn được đưa qua coordinator. Nhờ vậy, retry của một posting đã xong vẫn nhận lại response đã lưu. Trước khi sửa, replay exchange trả về `409 return_exceeds_sold`.
2. Thêm một số route:
   - `:reverse` trên sales-documents;
   - `POST /pos/offline-session`;
   - `GET /exchanges/{id}`.
3. `offline-sync` không nhận `Idempotency-Key`; mỗi item idempotent theo `offline_client_id`.
4. `:check` nằm trên nhóm route posting, nên cần `Idempotency-Key`.
5. Thêm `Routes.Entitled`.
6. `PATCH` giỏ hàng thay toàn bộ giỏ.
7. Adapter sessions dùng SQL trực tiếp trên bảng platform.
8. Thêm migration 000041 (`offline_client_id`).
9. BH sinh ra từ exchange dùng key `key+":sale"`.
10. Thêm `CodeReturnChanged` và `Debt.OpenReceivables`.
11. Đổi tên schema `SalesQuoteRequest` trong OpenAPI.
12. `lines` có `omitempty`.
13. Schema web nằm ở `src/shared/api/schema.d.ts`.
14. Dùng `nolint:revive` cho `SalesDocument` và `SalesReturn` vì đây là thuật ngữ của design.
15. Đổi chữ ký helper test `wantCode`: giờ trả về `map[string]string`.

## Rủi ro

1. **Test inventory đỏ, nằm ngoài quyền sở hữu của phase này.**
   - Test: `internal/inventory` `TestRollingBackMaxStockRefusesAppliedCostRecalculations`.
   - Nguyên nhân: migration procurement 000039 xóa `ck_cogs_adjustments__source_doc_type_kinds`.
   - Phase08 cần xử lý; tôi không sửa.
2. Combo hiện vẫn là `NoCombos`, đúng theo plan: phase 11 sẽ cung cấp `ComboExpander`.
3. SKU quà tặng chưa có trong tập khóa trước giao dịch. Nếu tranh chấp, lỗi chỉ lộ ra ở bước trừ tồn trong giao dịch.
4. Một `offline_client_id` trùng gửi đồng thời sẽ cho item đó trạng thái `failed` thay vì `replayed`. Client gửi lại thì sẽ nhận `replayed`.
5. Cache `sku_stock` có thể hết hạn trễ.
6. `RetailPrices` trả 0 cho SKU chưa có giá lẻ.
7. `CodeInvoiceChanged` có vẻ chưa được dùng.
8. Ảnh hưởng phụ của `postRefused`: request mới trong kỳ đã khóa hoặc ở tenant chỉ đọc nhận 423/403 thay vì conflict nghiệp vụ.
9. Ghi nhận thêm: đặt cọc bằng chuyển khoản ở trạng thái `pending` (chưa tính vào `paid_amount`) cho đến khi được xác nhận. Vì vậy test 10 000 000 dùng tiền mặt.

## Câu hỏi mở

- Phase08 có khôi phục constraint `ck_cogs_adjustments__source_doc_type_kinds` (hoặc cập nhật test inventory) không?
- Đặt cọc bằng chuyển khoản ở trạng thái pending có đúng ý đồ sản phẩm cho "Khách đã thanh toán" không?

## Sửa theo review 261018 (code-reviewer-261018-sales-pos-review.md)

### Đã sửa

1. **H1: khóa thành phần combo và quà tặng.**
   - Tập SKU bị khóa giờ là toàn bộ SKU mà chứng từ trừ tồn, xếp theo thứ tự chuẩn: SKU dòng yêu cầu, thành phần combo và quà do khuyến mãi thêm (`saleSkus`, `exchangeSkus`, `revisionSkus`).
   - Trong giao dịch, `skuSet.covers` kiểm lại các dòng đã dựng. Nếu có SKU nằm ngoài tập khóa (công thức combo hoặc khuyến mãi đổi giữa lúc đọc và lúc ghi sổ), trả 409 `sale_changed` và rollback toàn bộ.
   - Áp dụng cho POS, giỏ hàng, xử lý ngoại lệ offline, đổi hàng, cũng như confirm, revise và reserve-more của hóa đơn.
   - Test `TestConcurrentGiftsNeverTakeReservedStock`: 12 quầy bán đồng thời món được tặng ly, trong khi 4 ly đã giữ cho một hóa đơn. Kết quả đúng 5 quầy thành công, các quầy còn lại nhận 409 `insufficient_stock`, ly còn `sellable = reserved = 4`, và hóa đơn vẫn xuất kho đủ.
   - **Không thêm `sellable >= reserved` vào Verify.** Lý do: schema cho phép `sellable < reserved` một cách hợp lệ khi lô hết hạn hoặc bị cách ly sau khi đã giữ hàng (xem comment của migration `sku_stock` và `inventory.Available`). Thêm vào sẽ báo sai. Bất biến này được assert trong test tranh chấp.
2. **M1: phần giảm nợ của phiếu đổi.**
   - `exchanges.debt_reduction_amount` giờ chỉ là phần giảm nợ của chứng từ gốc. Khách lẻ luôn là 0.
   - `sales_returns.debt_reduction_amount` giữ nguyên ý nghĩa cũ, tức phần trả hàng không hoàn tiền.
   - Verify:
     - "exchange amounts" so `store_refunds_amount` với `refund_due_amount` của phiếu trả.
     - Thêm kiểm tra mới "exchange settlement": `returned − debt_reduction − store_refunds + customer_pays = taken`.
   - Test tamper có 2 ca: sửa DR, và sửa store_refunds. Không cần migration.
3. **M2: mở ca.**
   - Ca được mở trong chính giao dịch ghi sổ: sau guard entitlement, guard khóa kỳ và claim idempotency. CA là prefix phụ của posting.
   - Nếu ca đổi giữa lúc lập kế hoạch và lúc ghi (`errShiftMoved`), toàn bộ bị rollback rồi lập lại, tối đa 3 lần.
   - Tenant chỉ đọc hoặc kỳ đã khóa không để lại ca, số CA hay audit `shift.open`. Đã assert trong `TestSalesPostingInLockedPeriodIsRefused` và `TestSalesPostingInReadOnlyStoreIsRefused`.
   - `/pos/offline-sync` đặt sau `Entitled`, nên cả batch nhận 403 `subscription_read_only` và không park gì.
4. **L1: an toàn của offline-sync.**
   - `parkable` không coi `idempotency_in_progress` hay `idempotency_key_reused` là lỗi nghiệp vụ, kèm unit test `TestParkable`.
   - Mỗi `offline_client_id` có một advisory xact lock (`pg_advisory_xact_lock(hashtextextended(...))`, theo tiền lệ `inventory/recost.go`). Lock này được giữ ở cả `parkSale` và posting offline:
     - `parkSale` thấy chứng từ đã ghi sổ thì trả `replayed`.
     - Posting offline thấy đã park thì trả 409 `offline_sale_parked`.
     - `ResolveSyncException` thấy client id đã có chứng từ thì trả 409 `offline_sale_already_posted`.
5. **L2:** `retail_price` của tile POS là `null` khi bảng giá lẻ không có giá (OpenAPI nullable). Đã assert qua `/pos/bootstrap`.
6. **L3:** `/exchanges:preview` và `:check` không còn bị chặn bởi entitlement ghi. OpenAPI khai báo 403 `support_read_only`. Đã assert: tenant chỉ đọc vẫn preview được, còn `POST /exchanges` nhận 403.
7. **L4:** Đã xóa `CodeInvoiceChanged`.

Rủi ro 3, 6 và 7 ở mục Rủi ro phía trên đã được giải quyết bởi H1, L2 và L4.

### OpenAPI và web

- Thêm `sale_changed` vào các 409 của:
  - confirm, revise, reserve-more
  - pos/checkout, carts checkout
  - resolve, exchanges
- Thêm `offline_sale_already_posted` vào resolve.
- `retail_price` là nullable.
- Mô tả DR của Exchange và ExchangePreview.
- Mô tả 403 cho offline-sync.
- Đã chạy `npm run gen:api`; chạy lại thì schema không đổi.

### Gate đã chạy lại (18/10/2026)

- `gofmt -l .`: rỗng.
- `go vet ./...` và `go vet -tags integration ./...`: đạt.
- `golangci-lint run ./...` và `--build-tags integration`: 0 issues.
- `go test -race -count=1 ./...`: mọi package đạt.
- `go test -race -tags integration` cho sales, app, inventory, migrations và cli: đều đạt (app 57s, inventory 31s, migrations 22s, cli 11s).
- Các test tranh chấp, read-only, khóa kỳ và tamper đã chạy thêm `-count=3 -race`: đạt.
- Web:
  - `gen:api`: schema không đổi khi sinh lại.
  - `lint`, `typecheck`: đạt.
  - `test`: 234/234.
  - `build`: đạt.
- Testcontainers đã tự dọn; không còn container `org.testcontainers`. Không đụng `tally-postgres-1`.

## Sửa theo re-review 261018 (R1–R3; R4 bỏ qua theo lead)

1. **R1: hết lượt retry ca trả 409.**
   - Sau 3 lần `errShiftMoved`, `postSale` trả 409 `shift_changed` thay vì 500.
   - Không có gì được giữ lại vì tx đã rollback cả claim idempotency, nên client gửi lại với cùng key.
   - OpenAPI khai báo mã này ở 409 của `pos/checkout`, `pos/carts/{id}:checkout` và `pos/sync-exceptions/{id}:resolve`. Mô tả offline-sync ghi rằng item gặp `sale_changed` hoặc `shift_changed` thì `failed`, không bị park.
   - `parkable` loại `shift_changed` và cả `sale_changed`, vì cả hai là lỗi tạm thời: lần upload sau sẽ post được. Park những lỗi này sẽ đẩy sale hợp lệ vào hàng ngoại lệ. Có unit test trong `TestParkable`.
2. **R2: không giữ `FOR UPDATE` trên ca đang mở.**
   - Khi plan đã thấy ca của ngày, `dayShift` chỉ chạy `SELECT ... WHERE id = $1 AND status = 'open' FOR SHARE`. Khóa này đủ để chặn việc đóng ca tới khi sale commit, mà các sale khác cùng ca không phải xếp hàng.
   - Nếu ca đã bị đóng thì trả `errShiftMoved`; ở READ COMMITTED, `FOR SHARE` kiểm lại điều kiện trên bản dòng mới nhất.
   - `FOR UPDATE` chỉ còn dùng khi phải mở ca (và đóng ca cũ của ngày trước).
3. **R3: integration test cho các đường mới.**
   - Thứ tự được dựng tất định bằng `holdLock` (superuser giữ khóa trong một tx mở) và `waitForLockWaiters` (chờ theo `pg_stat_activity`).
   - `TestFirstSalesOfTheDayOpenOneShift`:
     - Hai sale đầu ngày cùng xếp hàng trên counter CA.
     - Kết quả: cả hai 201, cùng một ca, `shifts = 1`, đúng một số CA được lấy, một audit `shift.open`.
     - Test 20 checkout đồng thời cũng assert thêm đúng 1 số CA.
   - `TestOfflineSaleIsPostedOrParkedNeverBoth`:
     - Park trước rồi post: posting sau nhận 409 `offline_sale_parked`, và sync báo đúng exception đó. Kết quả 0 chứng từ, 1 exception.
     - Post trước rồi park: `parkSale` trả `replayed` cùng chứng từ. Kết quả 1 chứng từ, 0 exception.
     - Resolve một exception mà client id đã post: 409 `offline_sale_already_posted`, không thêm chứng từ, exception vẫn mở.
       - Trạng thái "vừa post vừa park" không còn đạt được bằng đường thường, nên test dựng nó bằng superuser: chuyển exception sang client id đã post. Đây là trạng thái mà race trước khi có khóa per-client có thể để lại.
   - Đã kiểm ngược: tắt guard `offline_sale_parked` thì test đỏ; bật lại thì xanh.

### Gate (18/10/2026, sau R1–R3)

- `gofmt`: rỗng.
- `go vet` (cả tag integration): đạt.
- `golangci-lint` (cả tag integration): 0 issues.
- `go test -race ./...`: đạt.
- Web:
  - `gen:api`: đã chạy.
  - `lint`, `typecheck`: đạt.
  - `test`: 234/234.
  - `build`: đạt.
  - Chỉ `schema.d.ts` thay đổi, và chỉ qua `gen:api`.
- Integration `-race -tags integration`: đạt cho sales, app (59s), inventory (33s), migrations (23s) và cli (12s).
- Testcontainers đã tự dọn; không đụng `tally-postgres-1`.
