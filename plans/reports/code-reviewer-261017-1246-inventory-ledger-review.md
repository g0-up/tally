# Code Review — Phase 07: Inventory Ledger

- Thời điểm: 2026-10-17 (Asia/Saigon)
- Reviewer: code-reviewer (read-only; không sửa code, không commit, không chạm DB dev `tally-postgres-1`)
- Đối chiếu:
  - `phase-07-inventory-ledger.md`
  - `design/database-conventions.md`, `design/database-schema-inventory-procurement.md`, `design/database-schema-debt-cash-crm-saas.md` (`cost_recalculation_runs`), `design/architecture.md`, `design/ubiquitous-language.md`
  - Báo cáo implementer `plans/reports/fullstack-developer-261017-1240-inventory-ledger.md`

## Phạm vi

- `apps/api/internal/inventory/**`: ledger, repo, costing, lotpicker, lockers, expiry, signals, reservation, adjustment, count, recost, verify, queries, dto, http, ports, model, errors
- `apps/api/migrations/000031_*`, đọc kèm `000010`, `000011`, `000012` để đối chiếu RLS, grant và constraint
- `internal/app/domains.go` (wiring inventory, adapter `catalogStock`), `internal/cli/ledger.go` (verify), `internal/cli/worker.go` (expiry roll và signals), `internal/platform/exception`, `api/openapi.yaml` (phần inventory)

Các lệnh đã chạy (testcontainers dùng Postgres riêng, không chạm DB dev):

| Lệnh | Kết quả |
|------|---------|
| `go vet ./internal/inventory/...` (có và không có `-tags integration`) | sạch |
| `golangci-lint run ./internal/inventory/...` (có và không có `--build-tags integration`) | 0 issue |
| `go test -race -count=1 ./internal/inventory/...` | ok (1.0s) |
| `go test -race -count=1 -tags integration ./internal/inventory/...` | ok (27.5s) |
| `go test -count=1 ./internal/app/... ./internal/cli/...` | ok, gồm cả `TestOpenAPIContract`. 24 route inventory mà review debt/cash báo thiếu nay đã có trong `openapi.yaml`. |
| `go test -race -count=1 -tags integration ./internal/app/... ./internal/cli/...` | ok |

## Đánh giá tổng quan

Phần lõi sổ cái làm chắc tay:

- `stock_moves` chỉ được append: RLS FORCE và `REVOKE UPDATE, DELETE`.
- `lot_balances` và `sku_stock` được tính lại trong cùng transaction qua `recompute`.
- `sku_stock` có CHECK `sellable = on_hand − quarantine − expired` và mọi đại lượng ≥ 0. Âm kho bị chặn hai lớp: kiểm trong bộ nhớ và CHECK của DB.
- Thứ tự lock `sku_stock` (sắp theo id) được giữ ở mọi đường ghi.
- Reversal bị chặn đảo hai lần, nhờ `FOR UPDATE` cộng với unique `ux_stock_moves__one_reversal`.
- Picker theo màu không bao giờ thay bằng màu khác.
- Recost replay idempotent: trừ đi COGS của các run trước, rồi so lại impact dưới lock.

Tôi không tìm thấy lỗi Critical hay High về đúng/sai số liệu.

Có sáu điểm Medium:

- Hai đường lách confirmation (disposal và nhả quarantine).
- Một chỗ trôi giá trị khi đảo DC quarantine. Verifier không bắt được.
- Một đường post kiểm kê bị kẹt mà người dùng không gỡ được.
- Giữ hàng thủ công (manual hold) không bao giờ hết hạn.
- Verifier chạy full-tenant trên mỗi lần mở thẻ kho.

## Critical

Không có.

## High

Không có.

## Medium

### M1. DC `decrease` (và quarantine) nhận lý do loại `disposal` mà không cần confirmation `stock_disposal`, nên lách được INV-07

Bằng chứng:

- `internal/inventory/adjustment.go:43`: `generalReasonKinds` có chứa `"disposal"`.
- `adjustment.go:79-80`: `KindDecrease` dùng `generalReasonKinds`, có `exception: ""`.
- `adjustment.go:83-85`: chỉ `KindDisposal` mới đòi `exception.StockDisposal`.
- `NeedsReauth` (`adjustment.go:97-100`) chỉ bật khi policy có exception, nên `decrease` cũng không cần re-auth.

Kịch bản:

1. `POST /stock-adjustments` với `kind=decrease` và `reason_id` trỏ tới lý do kind `disposal`.
2. Một `adjustment_out` được ghi, không cần `exception_confirmation_id` và không cần re-auth.
3. Hàng bị tiêu huỷ mà không đi qua cổng xác nhận mà INV-07 yêu cầu ("disposal needs `stock_disposal` exception"). Báo cáo theo lý do vẫn ghi là "disposal", nên nhìn từ báo cáo không phân biệt được hai đường.

Sửa: bỏ `"disposal"` khỏi danh sách lý do của `increase`, `decrease` và `quarantine_in`. Tách một slice riêng cho từng kind, hoặc bắt `Apply` đòi `StockDisposal` bất cứ khi nào `reason.kind = disposal`. Kèm một integration test: decrease với lý do disposal phải trả `exception_required`.

### M2. Đảo DC `quarantine_in` đưa hàng về sellable mà không cần confirmation `quarantine_release`, nên lách được INV-11

Bằng chứng:

- `adjustment.go:503-577`: `ReverseAdjustment` và `adjustmentReverse.Apply` không xét `orig.Kind`, không gọi `exception.Guard.Consume`.
- `ledger.Reverse` mirror cặp `quarantine_out`/`quarantine_in`, nên hàng quay lại lot thường.
- Route reverse chỉ đòi re-auth.
- `adjustment.go:87-89`: `KindQuarantineRelease` lại đòi `exception.QuarantineRelease`.

Kịch bản:

1. Hàng nghi lỗi được cách ly bằng DC `quarantine_in`.
2. Thay vì DC `quarantine_release` (phải xác nhận ngoại lệ), người dùng gọi `POST /stock-adjustments/{id}:reverse`.
3. Hàng về sellable mà không có bản ghi confirmation nào.

Sửa: trong `adjustmentReverse.Apply`, nếu `orig.Kind == KindQuarantineIn` thì consume `QuarantineRelease`, bind với doc `stock_adjustment` id và hash của body reverse. Cách khác là cấm reverse `quarantine_in` và bắt đi qua `quarantine_release`. Chọn hướng nào cần chủ sản phẩm quyết. Đảo một DC `disposal` (đưa hàng trở lại) không cần confirmation; theo tôi chấp nhận được.

### M3. Đảo DC quarantine khi toàn bộ tồn đang bị cách ly làm mất phần giá trị đã cộng vào lot cách ly; verifier không phát hiện

Bằng chứng:

- `ledger.go:719-748`: `costAdjust` không có `lot_id` sẽ chọn lot thường còn hàng. Nếu chỉ còn lot cách ly có hàng thì nó cộng giá trị vào lot cách ly.
- `ledger.go:775-779`: trong `mirror`, khi đảo một move inbound, nếu `m.Qty ≥ s.OnHand` thì `value = −s.StockValue` (lấy hết giá trị SKU).
- Quy tắc này đúng cho phiếu nhập, nhưng cũng áp lên move `quarantine_in`. Với move này, `on_hand` của SKU không đổi giữa hai chân của cặp.

Kịch bản:

1. Nhập 10 @100, V = 1000.
2. QC cách ly cả 10.
3. Chi phí landed cost +50 về sau, rơi vào lot cách ly, V = 1050.
4. Phát hiện cách ly nhầm, reverse DC quarantine. Reverse chạy newest-first:
   - mirror `quarantine_in`: qty −10 ≥ on_hand 10, nên value = −1050.
   - mirror `quarantine_out`: +10, value = +1000.
5. Kết quả: on_hand 10, V = 1000. 50 biến mất khỏi tồn kho mà không vào COGS.

`verify.go` không bắt được: "sku stock value" so tổng `value_amount` của move, mà các move đều tự nhất quán. "Reversal mirror" chỉ đòi value ≤ 0 cho chiều inbound. Recost replay (`recost.go:217-221`) coi đảo quarantine là reprice nên sẽ ra 1050. Vậy recost chỉ sửa được nếu có một posting backdate kích hoạt nó.

Sửa: trong `mirror`, với `MoveQuarantineIn`/`MoveQuarantineOut` thì mirror đúng `value` gốc. Clamp chỉ áp cho receipt thật. Phần landed cost nằm trên lot cách ly phải đi theo hàng: chuyển theo tỷ lệ giá trị của lot cách ly. Thêm một integration test cho đúng kịch bản trên.

### M4. Kiểm kê thừa (gain) trên SKU `track_lot` hoặc `track_expiry` chưa từng có lot (với màu đó) làm `:post` lỗi validation mà người dùng không sửa được

Bằng chứng:

- `count.go:779-790`: gain không có `lot_id` lấy `latestLot`. Hàm này trả nil khi SKU và màu chưa từng có lot thường.
- Khi đó `ledger.receive` (`ledger.go:458-474`) đòi `lot_no` (track_lot) hoặc `expiry_date` (track_expiry) và trả `validation_failed`.
- Count line không có trường nào để nhập hai giá trị đó. `UpdateCountLines` chặn thêm dòng cho SKU track_lot, nhưng snapshot vẫn tạo dòng zero `lot_id NULL` cho SKU không tồn.

Kịch bản:

1. SKU hạn dùng mới tạo, chưa nhập. Hoặc SKU `track_expiry` (không track_lot) được thêm một dòng màu mới.
2. Kiểm kê đếm được 5, rồi finish, rồi post.
3. Post trả `422 expiry_date required` ở `lines[i]`, một chỉ số không khớp với dòng nào trên phiếu.
4. Lối duy nhất là recount về 0, tức là sai số thực tế. Implementer có nhắc một phần ở báo cáo nhưng chưa có hướng xử lý.

Sửa: cho count line nhận `lot_no` và `expiry_date` khi gain mở lot mới, và kiểm ngay ở `FinishCount` để trả lỗi theo `line_id`. Cách tối thiểu là chặn ở `FinishCount` với thông điệp rõ ràng, thay vì để lỗi xuất hiện lúc post.

### M5. `expires_at` của giữ hàng thủ công không bao giờ được thực thi

Bằng chứng:

- `reservation.go:304-332` nhận và lưu `expires_at` (chỉ kiểm không ở quá khứ).
- Không có job, query hay filter nào dùng tới nó. `grep` cả `internal/` chỉ thấy trường được ghi và đọc ra, không có chỗ release. Worker (`cli/worker.go`) chỉ chạy expiry roll và signals.

Kịch bản:

1. Nhân viên giữ 10 cái cho khách, `expires_at` = ngày mai.
2. Một tuần sau, hold vẫn `active`, `reserved` vẫn trừ vào `available`.
3. `Reserve` của nguồn khác bị `insufficient_stock`. Phase 09 sẽ chặn bán theo `available`, nên lỗi sẽ lan sang bán hàng.

Sửa: thêm một bước trong `runStockJobs` để release các manual reservation có `expires_at ≤ now`. Đi qua `change` để giữ thứ tự lock `sku_stock`, rồi publish `stock.changed`. Nếu không định làm thì bỏ trường `expires_at` khỏi API và OpenAPI.

### M6. `GET /stock-card` chạy `VerifySkus` mỗi request, và mọi check aggregate trên toàn bộ ledger của tenant

Bằng chứng:

- `queries.go:679` gọi `VerifySkus` để tính `reconciled`.
- `verify.go:20-90`: các check join với subquery `(SELECT sku_id|lot_id, SUM(...) FROM stock_moves GROUP BY ...)` hoặc `lot_balances GROUP BY`.
- Filter `(NOT $1 OR s.id = ANY($2))` nằm ở query ngoài. Postgres không đẩy được điều kiện `ANY`/`OR` qua equivalence class vào trong subquery có `GROUP BY`. Mỗi lần mở thẻ một SKU vì vậy phải quét và gom toàn bộ `stock_moves` của tenant, ít nhất bốn lần (lot balance, sku on hand, stock value, ...), cộng các lần trên `lot_balances`.

Kịch bản: tenant có vài trăm nghìn move sau một hai năm. Mỗi lần mở thẻ kho tốn hàng trăm ms đến vài giây, tăng tuyến tính theo tuổi dữ liệu, trên một đường đọc nóng. Số liệu không sai; đây là vấn đề hiệu năng và độ trễ.

Sửa: khi `only` thì đặt filter vào trong subquery, ví dụ `(SELECT sku_id, SUM(qty) FROM stock_moves WHERE ($1 = false OR sku_id = ANY($2)) GROUP BY sku_id)`. Cũng có thể viết hai biến thể query (theo SKU và toàn tenant), hoặc dùng `LATERAL`. Đường `ledger verify` toàn tenant giữ nguyên.

## Low

### L1. Điểm (1) của implementer: confirmation disposal và quarantine release bind vào SKU của dòng đầu

Bằng chứng: `adjustment.go:309-388`: `Consume` dùng `DocType "sku"`, `DocID = in.Lines[0].SkuID`, `DocVersion 0`, và `PayloadHash` của toàn bộ body.

Kết luận: **không thể dùng confirmation này để xác nhận nhầm một phiếu nhiều SKU khác.**

- `exception.PayloadHash` băm canonical JSON của cả body (chỉ bỏ `exception_confirmation_id`). Mọi dòng, số lượng, lot và lý do đều nằm trong hash.
- Một confirmation tạo cho bộ dòng A không consume được cho bộ dòng B, kể cả khi B có cùng SKU đầu.
- TTL 30 phút và single-use (`pk_exception_confirmation_uses`) vẫn giữ.

Hạn chế còn lại chỉ thuộc về audit và báo cáo. `exception_confirmations.doc_id` chỉ trỏ tới SKU đầu, nên tra "ai xác nhận huỷ SKU X" cho SKU ở dòng 2 trở đi sẽ không ra. Đề xuất (không gấp): bind `DocType "stock_adjustment"` với id client sinh trước, hoặc lưu danh sách `sku_ids` trong payload audit của confirmation.

### L2. `sellable` lưu trong `sku_stock` có thể còn tính lot vừa hết hạn tới khoảng 1 giờ sau nửa đêm theo giờ tenant

- `cli/worker.go`: `runStockJobs` chạy mỗi giờ.
- `expiry.go` roll theo ngày của tenant. Bán hàng chọn lot theo `today` sống, nên không bán được lot hết hạn.
- Riêng `Reserve` (`reservation.go`, check `reserved + qty ≤ sellable`) dùng `sellable` lưu, nên trong khung đó có thể giữ hàng vượt phần còn bán được.
- Phase file dòng 28 chấp nhận việc "shortage after an expiry roll" chỉ hiện thành cảnh báo, nên đây là Low. Muốn khít hơn thì cho `Reserve` gọi `ExpiryRoller` cho SKU đang lock trước khi kiểm.

### L3. `StockReader.OnHandByColor` loại hàng cách ly nhưng vẫn tính lot hết hạn

`ports.go:34-55` lọc `NOT l.is_quarantine` mà không lọc `expiry_date < today`. Comment giải thích việc loại quarantine là vì hàng đó "not on the shelf to sell". Lý do đó cũng đúng cho hàng hết hạn. Catalog search (`on_hand_by_color`) vì vậy hiện số màu lớn hơn số bán được. Nên chọn một nghĩa nhất quán: hoặc là on-hand thật (gồm cả cách ly), hoặc là sellable (loại cả hai).

### L4. Down migration `000031` thất bại khi đã có `cogs_adjustments.source_doc_type = 'cost_recalculation'`

Down khôi phục CHECK cũ, không có `cost_recalculation`, nên sẽ fail trên dữ liệu đã có run được apply. Nên ghi chú điều này trong `docs/runbook.md`, hoặc cho down xử lý rõ ràng (fail sớm kèm thông điệp).

## Ghi chú cho phase 09 (không phải lỗi phase 07)

`StockLedger.Issue` với `MoveSale` không xét `reserved`. Nó chỉ chặn âm `sellable` theo lot. Sales phải tự kiểm `available` (hoặc consume reservation của chính đơn) trong cùng lock `sku_stock`, nếu không bán lẻ sẽ ăn vào hàng đã giữ cho đơn khác. AT-03 hiện chỉ được bảo đảm ở `Reserve`.

## Các hạng mục đã kiểm, không thấy lỗi

- **Thứ tự lock:**
  - `posting.Coordinator` lock `sku_stock` trước. `ledger.open` gọi `lockSkuStock` sắp theo `sku_id`.
  - Reservation `update` đọc trước không lock, rồi lock `sku_stock`, rồi mới `FOR UPDATE` reservation, đúng thứ tự toàn cục.
  - `confirmRecost` lock `sku_stock` rồi tới run. `preview` lấy advisory lock rồi tới run và không lock `sku_stock`. Không có chu trình.
- **Idempotency và atomicity:**
  - Mọi đường ghi (DC, reverse, count post, recost confirm) đi qua coordinator, key scope theo body hash.
  - `ux_stock_adjustments__idempotency_key` chặn trùng.
  - `ExtraDocs`/`TakeDocNo` dùng đúng (đã quyết định, không báo lại).
- **Đảo chứng từ:**
  - `adjustmentReverse.Apply` đọc bản gốc `FOR UPDATE`, chặn reversal-of-reversal và already-reversed. `insertMoves` map unique one-reversal sang `already_reversed`.
  - `count.go:327` lọc `reversal_of IS NULL`, nên KK có DC bị đảo vẫn hiện đúng DC gốc.
- **Moving average:**
  - Issue lấy avg × qty. Issue hết on-hand lấy toàn bộ giá trị.
  - Chỉ receipt, cost adjust và reversal mới tính lại avg.
  - Replay của recost dùng cùng quy tắc.
- **Recost:**
  - `impactOf` trừ COGS của các run trước. Replay bỏ qua move `cost_recalculation` nên apply hai lần không ghi trùng.
  - `confirmRecost` replay lại dưới lock và so `sameImpact`, nên trả `recost_stale` đúng lúc.
  - COGS ghi vào ngày hôm nay (kỳ đang mở).
  - `suggest` chỉ publish. `preview` chạy ở worker nên không giữ lock run trong transaction posting.
- **Lot picker:**
  - Explicit lot chặn lot cách ly. Lot hết hạn trả `exception_required` khi bật `block_expired_lot_sale` (409, đã quyết, không báo lại).
  - FEFO loại lot cách ly và hết hạn. `color_required` khi SKU có tồn theo màu.
  - `Lot.Expired` coi ngày hết hạn vẫn bán được, nhất quán với `expiry.go`.
- **Kiểm kê:**
  - Blind mode ẩn `system_qty`, `moved_since_start_qty`, `variance_qty` và `variance_lines` khi `counting` (`dto.go:486-512`).
  - Variance theo cutoff: `counted − (system + moved)`, có CHECK công thức ở DB.
  - Unique `NULLS NOT DISTINCT` nên `ON CONFLICT` của dòng `lot_id NULL` hoạt động.
  - `:post` consume `count_variance` bind với (stock_count, id, version, hash).
- **Expiry roller:** idempotent (chỉ sửa SKU có `expired_qty` lệch), dùng ngày theo timezone tenant, lock `sku_stock` trước khi ghi.
- **Tenant isolation:** RLS FORCE và policy `tenant_isolation` trên mọi bảng inventory. Mọi đường đọc và ghi đi qua `WithTenantTx` hoặc `WithReadOnlyTenantTx`. CLI verify chạy từng tenant trong read-only tx và exit khác 0 khi lệch.
- **OpenAPI:** `TestOpenAPIContract` pass.

## Khoảng trống test

- Không có test cho `decrease` kèm lý do `disposal` (M1), và reverse `quarantine_in` không qua confirmation (M2).
- Không có test cho cost adjust khi toàn bộ tồn đang cách ly rồi reverse quarantine (M3).
- Không có test count gain trên SKU `track_expiry`/`track_lot` chưa có lot (M4).
- Không có test hết hạn manual hold (M5).
- Không có benchmark hay `EXPLAIN` cho `VerifySkus` trên ledger lớn (M6).

## Danh sách sửa đề xuất (theo ưu tiên)

1. M1: bỏ `disposal` khỏi `generalReasonKinds`, hoặc đòi `StockDisposal` theo `reason.kind`.
2. M2: đòi `QuarantineRelease` khi reverse `quarantine_in`, hoặc cấm reverse loại này.
3. M3: mirror quarantine đúng giá trị gốc, chỉ clamp cho receipt.
4. M4: chặn hoặc nhận `lot_no` và `expiry_date` ở `FinishCount`, lỗi theo `line_id`.
5. M5: thêm job release manual hold hết hạn, hoặc bỏ `expires_at`.
6. M6: đẩy filter SKU vào trong các subquery của verifier.
7. L1 đến L4 khi tiện.

## Theo dõi plan

- Các mục chính trong phase 07 đã có code và test: ledger, picker, reservation, DC, KK, expiry, signals, recost, verify, route và OpenAPI.
- Chưa đạt trọn INV-07 (M1) và INV-11 (M2).
- AT-03 mới đủ ở phía `Reserve`; phía bán hàng thuộc phase 09.
- Đề xuất: giữ phase 07 ở trạng thái "cần sửa" cho tới khi xử lý M1 đến M4. M5 và M6 có thể theo sau, nhưng nên xong trước phase 09.

## Câu hỏi còn mở

- M2: reverse một DC `quarantine_in` nên đòi confirmation `quarantine_release`, hay cấm hẳn và bắt đi qua DC `quarantine_release`?
- M5: manual hold có thật sự cần tự hết hạn ở MVP không, hay bỏ trường `expires_at`?
