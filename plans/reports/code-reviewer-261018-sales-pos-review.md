# Review phase 09: Sales, POS, đổi trả

Ngày: 2026-10-18. Người review: code-reviewer (review-phase09). Chỉ đọc code và chạy test, không sửa file nguồn.

## Phạm vi

- `apps/api/internal/sales/**`
- `apps/api/internal/app/sales_ports.go`
- Phần wiring sales trong `internal/app/domains.go`, `internal/app/routes.go` và `internal/cli/{serve,worker}.go`
- Migration `000040`, `000041`
- `internal/app/sales_integration_test.go`
- Phần sales trong `api/openapi.yaml`
- `internal/cli/cli_integration_test.go:186`

Toàn bộ `apps/api/` vẫn untracked so với `6cf630b`, nên không xem được diff theo dòng. Tôi review trên trạng thái file hiện tại.

## Kiểm tra đã chạy

| Lệnh | Kết quả |
|---|---|
| `go vet -tags integration ./...` | ok |
| `go test -race -count=1 ./internal/sales/...` | ok |
| `go test -tags integration -count=1 ./internal/sales/... ./internal/app/... ./migrations/...` | ok (app 29.4s, migrations 13.5s) |
| `golangci-lint run ./internal/sales/... ./internal/app/...` | 0 issues |
| `docker ps` sau khi chạy | Không còn testcontainer nào của tôi. Tôi không động vào `tally-postgres-1`. |

## Đánh giá chung

Phần lõi posting chắc chắn:
- Mọi route có `Idempotency-Key` đều đi qua `posting.Coordinator`: replay, rồi entitlement, rồi period lock, rồi `Begin` trong cùng transaction, rồi lock theo thứ tự đã sắp xếp, rồi audit và outbox.
- `return_exceeds_sold` được kiểm tra hai lớp: theo dòng và theo lot.
- Đổi hàng ghi TH, BH và DH trong cùng một transaction và replay đúng.
- Tiền cọc chuyển khoản đang chờ xác minh được xử lý đúng.
- Migration đạt yêu cầu.

Vẫn còn một lỗ hổng tính đúng thật sự về tồn kho: các SKU mà server tự sinh (thành phần combo, quà khuyến mãi) không nằm trong lock set. Ngoài ra có một điểm ngữ nghĩa dữ liệu của đổi hàng lệch với tài liệu thiết kế.

## Critical

Không có.

## High

### H1. Thành phần combo và quà khuyến mãi không được lock tồn kho, nên POS có thể bán vào hàng đã giữ (reserved)

**Bằng chứng:**
- `internal/sales/confirm.go:637-651`: `draftSkus` chỉ lấy `l.SkuID` và `l.Parts` (vị mix) của request.
- `internal/sales/pos.go:199`: `postSale` lock đúng tập SKU đó. Các nơi gọi:
  - `pos.go:190`
  - `carts.go:292`
  - `offline_sync.go:304,481`
- `internal/sales/exchange.go:271`: đổi hàng dùng cùng cách.
- Server tự sinh thêm dòng có tồn kho sau khi đã lock:
  - Thành phần combo từ `s.combos.Expand` (`lines.go:450-457`, `comboComponents` `lines.go:708-733`, kind `combo_component` là `stocked`, xem `model.go:30`).
  - Quà khuyến mãi (`lines.go:475-507`).
- `checkStock` (`pos.go:366-389`) đọc `Available = sellable − reserved` của các SKU này mà không giữ lock.
- `StockLedger.Issue` sau đó mới `FOR UPDATE` dòng `sku_stock` (`inventory/repo.go:117-135`). Nó chỉ chặn tồn âm theo lot và bỏ qua `reserved` (`inventory/ledger.go:615-674`). Không có CHECK `sellable >= reserved` trong DB.

**Kịch bản lỗi:**
1. SKU thành phần C có `sellable = 6` và `reserved = 5`, vì một hoá đơn sỉ đã xác nhận và đã nhận cọc. Như vậy `available = 1`.
2. Hai quầy POS cùng bán một combo chứa 1 C. Cả hai `checkStock` đều thấy `available = 1` và cho qua.
3. `Issue` xếp hàng tuần tự trên `FOR UPDATE`, và cả hai đều thành công. Kết quả: `sellable = 4 < reserved = 5`.
4. Phiếu xuất kho của hoá đơn sỉ sau đó bị 409 thiếu hàng, dù hàng đã được giữ cho khách.

Quà khuyến mãi hỏng theo cùng cách. Thêm một hệ quả phụ: lock lấy ngoài thứ tự có thể gây deadlock. Ví dụ, sale A lock X rồi `Issue` quà G; sale B lock G rồi `Issue` thành phần X. Coordinator retry một lần, nếu vẫn deadlock thì trả 500.

`TestConcurrentCheckoutsNeverOversell` chỉ phủ hàng thường, không phủ combo hay quà.

Hai đường cùng lỗ hổng (đã xác minh): `ReviseInvoice` khi draft mới có combo (`confirm.go:229`), và đổi hàng có combo/quà ở phần hàng mới. Ở đây là giữ hàng cho thành phần chưa lock, nên có thể giữ vượt mức.

**Cách sửa đề xuất** (chọn một):
1. Build draft một lần ngoài transaction (`prepareSale` read-only) để lấy đủ tập SKU, gồm thành phần combo và quà, rồi đưa vào `LockSet`. Trong `apply`, khi build lại, nếu xuất hiện SKU nằm ngoài tập đã lock thì trả 409 `sale_changed` để client gửi lại. Cách này giống pattern `CodeExchangeChanged` / `CodeReturnChanged` đang dùng.
2. Tối thiểu: trong `applySale` và `applyExchange`, sau `prepareSale`, gọi `LockSkuStock` cho các SKU chưa có trong tập lock, rồi mới chạy `checkStock`. Cách này chấp nhận deadlock và retry.

Thêm test integration: N quầy cùng bán combo, có thành phần đang bị reserved; sau đó kiểm tra `sellable >= reserved`. Nên thêm kiểm tra này vào `Verify`.

## Medium

### M1. `exchanges.debt_reduction_amount` không thoả phương trình của thiết kế

**Bằng chứng:**
- `internal/sales/exchange.go:82-99`.
- Với khách vãng lai: `DebtReduction = min(returned, taken)`.
- Với khách có tên: `DebtReduction = original + onNew`, tức gồm cả phần cấn trừ vào hoá đơn mới.
- `design/database-schema-sales-kits.md:295` yêu cầu `returned − debt_reduction − store_refunds + customer_pays = taken`.

**Kịch bản lỗi:**
- Khách vãng lai trả 500k, lấy 650k. Code lưu `DR = 500k`, `pays = 150k`, `refunds = 0`. Phương trình cho 500 − 500 − 0 + 150 = 150, không bằng 650.
- Khách có tên, hoá đơn gốc đã trả đủ (open = 0): tương tự, `DR = 500k` dù không giảm đồng công nợ cũ nào.
- Báo cáo ở phase 12 đọc `debt_reduction_amount` như "giảm nợ cũ" sẽ sai. Khách vãng lai không có công nợ mà vẫn hiện "giảm nợ 500k".
- `Verify` (`verify.go`) không có check cho phương trình này, nên sai lệch không bị phát hiện.

Chính tài liệu thiết kế cũng không nhất quán:
- Câu "new sale collected in full" cộng với CHECK `(customer_pays = 0 OR store_refunds = 0)` chỉ đúng khi có cấn trừ.
- Phương trình chỉ đúng khi `DR` là phần giảm nợ của **chứng từ gốc**.

Hành vi tiền thực tế (150k khách trả thêm, một TH/BH/DH) vẫn đúng và test đã phủ.

**Cách sửa đề xuất:**
- Lưu `exchanges.debt_reduction_amount = original`, tức chỉ phần giảm nợ chứng từ gốc. Giữ `sales_returns.debt_reduction_amount` của TH như hiện tại, vì CHECK `debt_reduction + refund_due = total` của TH cần phần cấn trừ.
- Thêm check `exchange equation` vào `Verify`.
- Hoặc, nếu chủ dự án muốn giữ ngữ nghĩa hiện tại, sửa phương trình trong design doc.

Đây là quyết định về ngữ nghĩa dữ liệu, cần lead hoặc người dùng chốt (xem Câu hỏi mở).

### M2. `ensureShift` ghi dữ liệu trước guard entitlement/period; `offline-sync` lệch hợp đồng 403

**Bằng chứng:**
- `pos.go:182`, `carts.go:283`, `offline_sync.go:216,473` gọi `ensureShift` trước `postSale`.
- `openShiftOnce` (`shift.go:56-92`) chạy trong transaction riêng: cấp số `CA`, có thể đóng ca của ngày trước, insert `shifts` và ghi audit. Tất cả xảy ra trước khi coordinator kiểm tra `CanPost` và `Periods.Check`.
- Route `/pos/offline-sync` nằm trên nhóm `Posting`, không có middleware entitlement (`routes.go:170-171`, `http.go:104`). OpenAPI lại ghi `403 support_read_only or subscription_read_only`.

**Kịch bản lỗi:**
1. Tenant hết hạn subscription (read-only) mở POS và bấm thanh toán. Server mở ca mới (ghi `shifts`, tiêu số `CA`, audit `shift.open`, đóng ca hôm qua), rồi mới trả 403. Như vậy tenant read-only vẫn ghi được dữ liệu. Kỳ đã khoá (423) cũng vậy.
2. Thiết bị đồng bộ offline khi tenant read-only. Response là 200; từng item có `status: failed`, `error_code: subscription_read_only`; và vẫn mở ca. Client làm theo OpenAPI sẽ chờ 403 cho cả batch.
3. Retry một checkout đã commit sau nửa đêm: `ensureShift` đóng ca cũ và mở ca mới trước khi replay trả response cũ. Đây là side effect trên một request replay.

**Cách sửa đề xuất:**
- Mở ca bên trong posting: lock dòng ca hoặc counter CA trong `apply`, sau khi coordinator đã qua replay và các guard.
- Tối thiểu: kiểm tra `Replay` và `CanPost` trước `ensureShift`.
- Với `offline-sync`, chọn một trong hai: thêm `guarded(r.Entitled, ...)` để cả batch trả 403, hoặc sửa OpenAPI để mô tả 200 kèm item `failed/subscription_read_only`.

## Low

### L1. Đưa vào hàng đợi ngoại lệ mà không kiểm tra lại chứng từ đã có; xử lý ngoại lệ có thể đăng trùng

**Bằng chứng:**
- `offline_sync.go:331-342`: `parkable` coi mọi `Conflict` là lỗi nghiệp vụ, kể cả `idempotency_key_reused` và `idempotency_key_in_progress`.
- `parkSale` (`offline_sync.go:346-390`) chèn `pos_sync_exceptions` mà không kiểm tra `sales_documents.offline_client_id`.
- `ResolveSyncException` (`offline_sync.go:456-481`) đăng lại theo scope `pos.checkout` mà không kiểm tra `offline_client_id` đã có chứng từ hay chưa.

**Kịch bản lỗi:**
Hai batch chứa cùng `client_id = c1` chạy đồng thời.
- Batch A: `apply` hết hàng, rollback, rồi park c1.
- Batch B: chạy ngay sau khi hàng được nhập hoặc trả lại, đăng thành công c1.

Kết quả là có cả HD c1 lẫn một exception đang mở cho c1. Lần sync sau, `priorOutcome` trả `replayed` vì nó đọc `sales_documents` trước. Nhưng exception vẫn nằm trong hàng đợi; chủ cửa hàng bấm "resolve" thì sale bị đăng lần hai, trừ kho và thu tiền hai lần. Thiết bị gửi lại c1 với payload đã sửa cũng rơi vào nhánh `idempotency_key_reused` rồi bị park.

Xác suất thấp. Với cùng payload, `Begin` chạy trong cùng transaction nên tuần tự hoá đúng và trả replay (`posting/coordinator.go:160-166`).

**Cách sửa đề xuất:**
- Loại hai mã idempotency khỏi `parkable`.
- Trong transaction của `parkSale`, nếu đã có `sales_documents` với `offline_client_id` thì trả `replayed` thay vì insert.
- Trong `applySale`, khi `sl.resolves != nil`, từ chối (409) nếu `offline_client_id` của exception đã có chứng từ.

### L2. Không phân biệt được "chưa có giá bán lẻ" với "giá 0" trong response

**Bằng chứng:**
- `app/sales_ports.go:58-73` trả `0` khi pricing rơi về `SourceManual`.
- `PosTile.RetailPrice` (`pos_bootstrap.go:49,135`) là kiểu `money.Money`, không nullable.
- `QuoteLine.RetailPrice` (`model.go:194`, `quotes.go:458-468`) luôn được gán.
- Contract của pricing (`pricing/model.go:44-45`) nói "price 0, the screen asks for one", nhưng API không đưa ra cờ nào để màn hình biết phải hỏi.

**Kịch bản:** SKU mới chưa có trong bảng giá hiện tile 0₫. Thu ngân bấm bán, POS gửi không kèm `unit_price`, và server ghi doanh thu 0 mà không cảnh báo (`MinPrice` nil nên không chặn).

**Cách sửa đề xuất:** Trả `retail_price: null` (hoặc thêm `price_source`) khi không có giá niêm yết. POS bắt buộc nhập giá trong trường hợp này. Nếu muốn, server có thể từ chối dòng `manual` có giá 0 khi không có `unit_price` gõ tay.

### L3. Khoảng trống OpenAPI và guard ở các POST chỉ đọc

**Bằng chứng:**
- `POST /exchanges:preview` (`http.go:113`) nằm trên nhóm `Tenant`, chịu cả `Entitlement` lẫn `SupportReadOnly`. Vì vậy tenant read-only và phiên support nhận 403 cho một thao tác chỉ đọc, mà OpenAPI không khai báo 403.
- `POST /invoices/{id}:check` nằm trên nhóm `Posting`; `SupportReadOnly` chặn support và cũng không khai báo 403.
- Các POST, PATCH, DELETE nháp trên nhóm `Tenant` chỉ ghi `403: support_read_only.` dù middleware entitlement có thể trả `subscription_read_only`. Đây là pattern chung của repo (41 chỗ), không riêng sales.

**Cách sửa đề xuất:** Khai báo 403 cho hai route trên. Hoặc đánh dấu chúng là thao tác đọc: miễn `SupportReadOnly` và entitlement theo route. Nếu muốn đồng bộ thì sửa pattern 403 trong một lần riêng.

### L4. `CodeInvoiceChanged` được khai báo nhưng không dùng

Hằng này ở `errors.go:24`, không có ai tham chiếu và không có trong OpenAPI. Nên xoá hoặc dùng nó.

## Đã kiểm tra, không có lỗi

- **Kiểm tra entitlement:** mọi op có `IdempotencyKey` (13 op sales) đều liệt kê `subscription_read_only` dưới 403. Tôi kiểm tra bằng script đọc `openapi.yaml`. Coordinator chạy `CanPost` sau `Replay` (`coordinator.go:144-156`).
- **`postRefused`:** chỉ bao lỗi `Conflict`.
  - Apply trả lỗi thì `Begin` bị rollback, không lưu response, nên request mới vẫn bị từ chối đúng lỗi gốc.
  - Retry của một posting đã commit sẽ nhận lại response cũ (test `deposit ... replayed`).
  - Việc trả 403/423 thay vì 409 cho request mới khi tenant read-only hoặc kỳ đã khoá là hợp lý, vì cùng thứ tự guard với mọi posting.
  - `date = todayTx` chỉ dùng cho period check của lệnh bị từ chối, không ghi gì.
- **Trùng `offline_client_id` cùng payload gửi đồng thời:** batch thứ hai chặn ở `INSERT ... ON CONFLICT` của `Begin`, sau đó replay. Unique index `ux_sales_documents__offline_client_id` là lưới an toàn. Tôi không tái hiện được trạng thái `failed` mà implementer nêu bằng cách đọc code; đường lệch duy nhất tìm thấy là L1.
- **Câu hỏi (a), tiền cọc chuyển khoản pending:** xử lý đúng theo CASH-02/04.
  - `invoicePayment` tách `Pending` (`invoices.go:474-475`).
  - `planPaid` không hoàn phần pending (`invoices.go:652`).
  - Phía debt, `applyEarmarked` (`debt/factory.go:217-250`) lập kế hoạch phân bổ cho cọc pending và áp khi xác nhận, có lock dòng payment để tránh race.
- **Migration 000040/000041:** version liên tục, có cặp up/down, không trigger/function/view/extension/DO/CASCADE, tên constraint mới. `migrations_lint_test` và `reversibility_integration_test` đều pass.
- **Cô lập tenant:** mọi đọc/ghi đi qua `WithTenantTx` (RLS). Sessions adapter dùng platform tx có lọc `tenant_id` tường minh. Có test "ids of another store are refused".
- **`cli_integration_test.go:186`:** thêm `shift: time.Hour` là đúng. `worker.go:51` từ chối interval ≤ 0 nên không thể panic ở runtime.

## Đánh giá các lo ngại của implementer

| Lo ngại | Kết luận |
|---|---|
| Tiền cọc chuyển khoản pending không tính vào paid | Đúng thiết kế, giữ nguyên. |
| Quà không được lock | Có thật và rộng hơn: cả thành phần combo. Xem H1. |
| Trùng `offline_client_id` gửi đồng thời ra `failed` | Không tái hiện được với cùng payload. Có đường đăng trùng khác, xem L1. |
| `RetailPrices` trả 0 | Đúng contract pricing, nhưng API cần cờ hoặc null. Xem L2. |
| `postRefused` trả 403/423 thay vì 409 | Chấp nhận được. |
| `CodeInvoiceChanged` không dùng | Xem L4. |

## Việc nên làm, theo thứ tự ưu tiên

1. H1: đưa thành phần combo và quà vào lock set (hoặc lock bổ sung trước `checkStock`), thêm test combo chạy đồng thời với hàng reserved và một check `sellable >= reserved` trong `Verify`.
2. M1: chốt ngữ nghĩa `debt_reduction_amount`, sửa code hoặc doc, thêm check phương trình đổi hàng vào `Verify`.
3. M2: chuyển việc mở ca vào sau các guard; thống nhất hợp đồng 403 của `offline-sync`.
4. L1: siết `parkable`, `parkSale` và resolve theo `offline_client_id`.
5. L2–L4.

## Theo dõi plan

Các tiêu chí thành công của phase 09 đều đã có test integration pass:
- Hoá đơn 10M, cọc 3M, còn nợ 7M.
- `color_required` 422.
- 20 quầy bán 10 đơn vị.
- Trả 3 khi chỉ bán 2 thì 409.
- Đổi 500k lấy 650k: khách trả thêm 150 000, đúng một TH/BH/DH.
- Đồng bộ offline gửi lại chỉ đăng một lần.
- Bán chịu và chuyển khoản offline bị chặn với `offline_payment_not_allowed`.

Đề xuất: chưa đánh dấu phase hoàn thành cho tới khi xử lý xong H1.

## Câu hỏi mở

1. `exchanges.debt_reduction_amount` nên là "phần giảm nợ của chứng từ gốc" (khớp phương trình trong design) hay "tổng phần cấn trừ" (như code hiện tại)?
2. Với `offline-sync` khi tenant read-only: trả 403 cho cả batch, hay giữ 200 kèm item `failed` để thiết bị giữ sale và gửi lại sau?

## Re-review (2026-10-18)

Phạm vi: các mục sửa trong `plans/reports/fullstack-developer-261017-2125-sales-pos.md`. Chỉ đọc và chạy test, không sửa nguồn.

### Kiểm chứng đã chạy

| Lệnh | Kết quả |
|------|---------|
| `go vet ./...` và `go vet -tags integration ./...` | sạch |
| `go test -race -count=1 ./internal/sales/...` | ok |
| `go test -tags integration -count=1 ./internal/sales/... ./internal/app/... ./internal/inventory/...` | ok (app 30.5s, inventory 17.8s) |
| `docker ps` sau khi chạy | không còn testcontainers/ryuk; không đụng `tally-postgres-1` |

### Kết luận từng mục

| Mục | Kết luận | Bằng chứng |
|-----|----------|------------|
| H1 | Đã sửa | Lock set = SKU trong request + `stockedSkus(built.Lines)`, tức gồm thành phần combo và quà. Áp dụng ở POS, giỏ và resolve (`pos.go:224`, cùng đi qua `postSaleOnce`), đổi hàng (`exchange.go:272`, `exchangeSkus`), confirm/revise/reserve-more/cancel-remainder và stock-out (`invoiceSkus` + `revisionSkus`, `confirm.go:628`). Trong tx, apply dựng lại dòng rồi gọi `covers` (`pos.go:421`, `exchange.go:303`, `confirm.go:842`); thiếu SKU thì trả 409 `sale_changed`. Thứ tự khóa chuẩn do `posting.LockSet` lo: theo rank loại khóa, rồi theo byte UUID (`locks.go:70,79`). Lock set được tính lại ở mỗi lần retry. Đường giỏ hàng an toàn: draft đọc từ giỏ, còn version kiểm dưới `FOR UPDATE` (`pos.go:279`). Có `TestConcurrentGiftsNeverTakeReservedStock` và unit test cho `covers`. |
| H1, lý do không thêm `sellable >= reserved` vào Verify | Đồng ý | `000010_inventory_ledger.up.sql:88-89` ghi rõ: hết hạn hoặc cách ly có thể làm sellable thấp hơn reserved, và đó là cảnh báo thiếu hàng, không phải dữ liệu hỏng. Đưa vào Verify sẽ báo sai. |
| M1 | Đã sửa | `verify.go:117` "exchange amounts" so `store_refunds` với `refund_due`. `verify.go:125` "exchange settlement" kiểm `returned − DR − refunds + pays = taken`. Integration có 2 case tamper (`sales_integration_test.go:1123,1129`). |
| M2 | Đã sửa, an toàn | `dayShift` chạy trong tx posting sau mọi guard (entitlement, kỳ khóa, idempotency, kiểm tồn), xem `pos.go:332`. `errShiftMoved` làm cả tx rollback: dòng idempotency `Begin`, số CA/HD/PT, audit và outbox cùng rollback, nên không trùng và không rò. Hai sale đầu ngày đua nhau: counter CA tuần tự hoá; bên thua thấy ca mới, rollback và lấy lại số; lần sau plan `open = ca đó`. Không hở số, không mở 2 ca (`ux_shifts__one_open`). Offline-sync có `Entitled` (`http.go:106`), nên cả batch bị 403. |
| L1 | Đã sửa | Khóa advisory có scope tenant: key là `'offline_sale:' \|\| tenant_id \|\| ':' \|\| client_id` (`offline_sync.go:494`). Không có vòng deadlock: trong posting, advisory lấy sau counter và SKU; `parkSale` chỉ lấy advisory rồi insert exception, không chờ khóa tồn kho hay counter. Resolve đi `lockOpenException` + `checkNotPosted`; offline post bị `checkNotParked` chặn khi exception tồn tại, nên hai đường loại trừ nhau. `parkable` loại 2 mã mới (`TestParkable`). |
| L2 | Đã sửa | `PosTile.RetailPrice *money.Money`; adapter trả nil khi nguồn giá là manual (`app/sales_ports.go:60`); OpenAPI `oneOf Money/null`. |
| L3 | Đã sửa | `invoices:check` và `exchanges:preview` không còn qua guard entitlement (`http.go:76,119`); OpenAPI ghi 403 `support_read_only`. |
| L4 | Đã sửa | Không còn `CodeInvoiceChanged` hay `invoice_changed` trong code. |

### Phát hiện mới (đều Low, không chặn)

- **R1. Hết 3 lần retry thì trả 500.** `postSale` trả `errors.New(...)` sau `shiftAttempts`, nên client nhận `internal_error`. Thực tế gần như không xảy ra (cần 3 lần đổi ca liên tiếp), nhưng nếu xảy ra thì nên là 409 hoặc 503 có thể thử lại, ví dụ dùng lại `sale_changed` hoặc mã `shift_changed`.
- **R2. `dayShift` giữ `FOR UPDATE` trên dòng ca đang mở** từ `pos.go:332` tới commit, nên mọi sale POS của tenant tuần tự hoá ở đuôi tx (Issue, công nợ, audit, outbox). Đúng, nhưng giới hạn throughput khi nhiều quầy cùng bán. Khi `plan.open != nil` chỉ cần `FOR SHARE`, đủ để chặn đóng ca đồng thời mà không chặn các sale khác. Theo dõi khi load test.
- **R3. Thiếu integration test cho các đường mới:**
  - 409 `offline_sale_parked`
  - 409 `offline_sale_already_posted`
  - `parkSale` trả `replayed`
  - retry `errShiftMoved` khi hai sale đầu ngày đua nhau

  Logic đọc thấy đúng, nhưng chưa có test chứng minh.
- **R4. Replay tốn thêm một lần build.** `saleSkus` dựng dòng (giá, khuyến mãi) trước khi coordinator replay, nên request lặp idempotency phải trả thêm một lần build. Chỉ ảnh hưởng hiệu năng.

### Theo dõi plan

H1, M1, M2, L1–L4 đã đóng. Hai câu hỏi mở đã được lead chốt và code khớp quyết định. Phase 09 có thể đánh dấu hoàn thành. R1–R4 có thể để backlog.
