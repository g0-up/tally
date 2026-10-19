# Review code — Phase 08 Nhập hàng (Procurement)

- Thời điểm: 2026-10-17 (Asia/Saigon)
- Người review: code-reviewer (chỉ đọc, không sửa code)
- Kế hoạch: `plans/261016-0854-tally-full-product-implementation/phase-08-procurement.md`
- Báo cáo dev: `plans/reports/fullstack-developer-261017-1526-procurement.md`

## Phạm vi

- `apps/api/internal/procurement/`, gồm `receipt_posting.go`, `return_posting.go`, `landed_cost.go`, `cost_split.go`, `price_adjust.go`, `matching.go`, `orders.go`, `status.go`, `repo.go`, `queries.go`, `http.go`, `dto.go`, `errors.go`, `model.go`, `service.go`, `ports.go` và các test `status_test.go`.
- `apps/api/internal/app/procurement_ports.go`, `procurement_integration_test.go` và `routes.go`.
- Migration `000040_procurement_receipt_line_lot` (up và down), đối chiếu thêm với `000014_procurement_documents_returns.up.sql`.
- Design: `database-schema-inventory-procurement.md` §3–4 và `database-conventions.md`.
- Đã đọc thêm `posting/coordinator.go`, `posting/locks.go`, `debt/payments.go`, `debt/recorders.go` và `debt/http.go` để kiểm tra hành vi của các port.

## Lệnh kiểm tra: CHƯA CHẠY ĐƯỢC

Trong phiên review, Bash lỗi ở mọi lệnh, kể cả `echo hi`. Lỗi là `Could not start /usr/bin/zsh … E2BIG (command line 152KB)`, do môi trường chạy tool chứ không phải do repo. Vì vậy tôi **chưa chạy** ba lệnh sau:

- `go vet -tags integration ./internal/procurement/... ./internal/app/...`
- `golangci-lint run ./internal/procurement/...`
- `go test -race -tags integration -count=1 ./internal/procurement/... ./internal/app/...`

Mọi kết luận dưới đây đến từ việc đọc code. Báo cáo dev ghi là các lệnh này đều xanh, nhưng tôi chưa tự xác minh. Lead cần chạy lại trước khi đóng phase. Tôi cũng không đụng tới container `tally-postgres-1`.

## Đánh giá chung

Phần lõi sổ cái đúng thiết kế:

- Thứ tự trong coordinator: replay → entitlement → khoá kỳ → idempotency → số chứng từ → lock → apply → audit → outbox.
- Lock sku_stock luôn lấy trước row PO/PN, rồi mới tới obligations/payments.
- Payable là duy nhất theo nguồn.
- XT xuất đúng `receipt_lines.lot_id`.
- Giới hạn số lượng trả được kiểm dưới `FOR UPDATE` của PN.
- Projection của PO được cập nhật dưới `FOR UPDATE` của PO.
- Audit dùng dạng `entity.verb`.
- Migration 000040 sạch.

Tuy vậy có **1 bug chắc chắn** (đảo PN của PO đã đóng thiếu), **2 lỗ hổng đối chiếu hoá đơn NCC**, **1 lệch hợp đồng route posting** và **1 sai số giá vốn sau trả hàng**. Tôi không thấy lỗi Critical về bảo mật hay mất dữ liệu.

## Critical

Không có.

## High

Không có. Finding M1 là bug chắc chắn nhưng có đường vòng (XT), nên xếp Medium.

## Medium

### M1. Đảo PN thuộc PO đã `closed_short` luôn trả 409 `order_status_invalid`

- **Vị trí:**
  - `internal/procurement/receipt_posting.go:640-643` (`addOrderProgress`)
  - `internal/procurement/status.go` (`AfterReceipts`, `Transition`)
  - Test khẳng định hành vi: `status_test.go:37` (`{closed_short, closed_short}` bị từ chối) và `status_test.go:89-91` (`AfterReceipts(closed_short)` = `closed_short`)
- **Kịch bản:**
  1. PO 100 cái, nhận 60.
  2. Đóng thiếu với lý do "NCC hết hàng", PO chuyển sang `closed_short`.
  3. Phát hiện PN 60 nhập nhầm NCC, gọi `POST /receipts/{id}:reverse`.
  4. `receiptReverse.Apply` gọi `addOrderProgress(o, −60)`, `AfterReceipts` trả `closed_short`, rồi `Transition(closed_short, closed_short)` trả lỗi.
  5. Cả giao dịch rollback và người dùng nhận 409 "A closed_short purchase order cannot become closed_short".
- **Hậu quả:**
  - Bất kỳ PN nào gắn với PO đã đóng thiếu đều không đảo được.
  - Thông báo lỗi khó hiểu.
  - Người dùng buộc phải làm XT, việc này sinh credit note thay vì huỷ payable.
- **Đề xuất sửa:**
  - Trong `addOrderProgress`, bỏ qua `Transition` khi `next == o.Status`, vì nhánh `next == o.Status` ở dòng 644 đã xử lý việc chỉ tăng version.
  - Hoặc cho `Transition` chấp nhận tự chuyển với `closed_short`.
  - Thêm integration test: PO nhận một phần → close-short → reverse PN → 201, PO vẫn `closed_short`, `received_base_qty` giảm.

### M2. Đối chiếu hoá đơn NCC đọc PN không khoá, có thể khớp với PN vừa bị đảo

- **Vị trí:** `internal/procurement/matching.go:170` (`getReceipt(ctx, q, rid, false)`), `matching.go:184-187` (INSERT link) và `receipt_posting.go:745` (`checkNoFollowups`).
- **Kịch bản:** hai giao dịch chạy song song trên cùng PN X.
  - T1 là `POST /supplier-documents/{id}/receipts` với X. T1 đọc X khi X còn `posted` (không khoá).
  - T2 là `:reverse` X. T2 lấy `FOR UPDATE` trên X, `checkNoFollowups` chưa thấy link nào vì T1 chưa commit, rồi T2 đảo X và commit.
  - T1 INSERT `supplier_document_receipts`. Kiểm tra FK chỉ cần X còn tồn tại (KEY SHARE), mà X vẫn tồn tại với status `reversed`, nên INSERT thành công.
  - `refreshMatches` cộng `total_amount` của X vào `matched_amount`.
- **Hậu quả:**
  - Hoá đơn NCC báo `matched` với một PN đã đảo, tức payable đã bị huỷ.
  - Kế toán sẽ thanh toán theo hoá đơn trong khi công nợ thực tế bằng 0.
  - `procurement.Verify` không bắt được trường hợp này. Đã đọc `verify.go:30-81`: không có check nào trên `supplier_document_receipts` hay `matched_amount`.
- **Đề xuất sửa:**
  - Trong `linkReceipts`, khoá các PN theo thứ tự id: `getReceipt(ctx, q, rid, true)`, hoặc `SELECT … FOR SHARE` cho cả danh sách đã sắp xếp. Nhờ đó reverse và link tuần tự hoá trên row PN.
  - Hoặc, trong `refreshMatches`, chỉ tính `r.status = 'posted'`, và khi đảo PN thì refresh các hoá đơn đã gắn.

### M3. Một PN có thể được khớp vào nhiều hoá đơn NCC, nên hoá đơn trùng không bị phát hiện

- **Vị trí:**
  - `migrations/000014_procurement_documents_returns.up.sql:56-62`: PK là `(tenant_id, supplier_document_id, receipt_id)`, không có UNIQUE trên `(tenant_id, receipt_id)`.
  - `matching.go:184-187`: `ON CONFLICT DO NOTHING` chỉ chặn trùng trong cùng một hoá đơn.
- **Kịch bản:** NCC gửi hai lần cùng một hoá đơn với hai số khác nhau, HD-0001 và HD-0001A, mỗi hoá đơn 14.400.000. Nhân viên gắn PN-1 (14.400.000) vào cả hai, và cả hai đều báo `matched`, variance 0.
- **Hậu quả:**
  - PUR-08 đặt ra đối chiếu để phát hiện chênh lệch, nhưng ở đây hệ thống xác nhận khớp cho một khoản bị tính hai lần.
  - Khi DG thay đổi PN, `refreshMatches` bump version của cả hai hoá đơn.
- **Đề xuất sửa:**
  - Nếu nghiệp vụ là một PN chỉ thuộc một hoá đơn, thêm `UNIQUE (tenant_id, receipt_id)` bằng migration mới, hoặc kiểm tra trong `linkReceipts` và trả 409 `receipt_already_matched`.
  - Nếu cho phép nhiều hoá đơn (ví dụ hoá đơn hàng tách khỏi hoá đơn thuế), cần phân bổ `matched_amount` thay vì cộng đủ tổng PN cho mỗi hoá đơn.
  - Cần lead hoặc PO xác nhận nghiệp vụ.

### M4. Route posting của procurement nằm trên `TenantAPI()`, trái hợp đồng `TenantPostingAPI()`

- **Vị trí:**
  - `internal/app/routes.go:83-85`: chỉ truyền `Tenant: a.TenantAPI()`.
  - `internal/procurement/http.go:28-32, 58, 60, 62, 70, 74`.
  - So với hợp đồng ở `routes.go:155-163` và cách debt làm ở `debt/http.go:37-39, 57-68`.
- **Kịch bản:**
  1. Cửa hàng gửi `POST /receipts` với `Idempotency-Key` K. Request commit, nhưng mạng rớt nên client không nhận được 201.
  2. Ngay sau đó subscription chuyển sang read-only, ví dụ do hết hạn lúc nửa đêm.
  3. Client retry với K và `middleware.Entitlement` trả 403 trước khi coordinator kịp replay.
  4. UI báo lỗi. Người dùng nghĩ PN chưa lên, nên sau khi gia hạn lại tạo PN mới với key mới. Kết quả là nhập kho hai lần và payable hai lần.
- **Hậu quả:** vi phạm quy tắc ở conventions §7 ("a retry of a committed request is never turned into an error by … a read-only subscription that arrived after the commit"). Báo cáo dev có ghi nhận lệch này nhưng không đánh giá rủi ro.
- **Đề xuất sửa:**
  - Thêm trường `Posting *gin.RouterGroup` vào `procurement.Routes`, và truyền `a.TenantPostingAPI()` từ `routes.go`.
  - Đăng ký trên group Posting: `POST /receipts`, `:reverse`, `POST /receipts/{id}/landed-costs`, `POST /purchase-returns` và `POST /purchase-price-adjustments`.
  - Thêm test: post PN → chuyển entitlement sang read-only → replay cùng key → 201 kèm `Idempotent-Replay: true`.

### M5. Landed cost tính `unit_cost` trên số lượng nhận, trong khi phân bổ trên số lượng còn giữ

- **Vị trí:** `internal/procurement/landed_cost.go:240` (`unitCost(l.LineAmount, landed, l.BaseQty)`), so với trọng số kept value hoặc kept qty trong `cost_split.go`.
- **Kịch bản:**
  1. PN có dòng 10 cái × 50.000 (500.000).
  2. XT trả 4 cái, còn giữ 6.
  3. Landed cost 60.000 phân bổ hết cho 6 cái giữ lại, tức 10.000 mỗi cái.
  4. `unit_cost` mới = (500.000 + 60.000) / **10** = 56.000. Giá đúng của hàng còn giữ là 50.000 + 10.000 = 60.000.
  5. Sau đó XT 6 cái còn lại. `issueStock` dùng `unit_cost` 56.000 để điều chỉnh chênh lệch so với giá bình quân, nên chỉ rút 336.000 thay vì 360.000.
  6. 24.000 giá trị tồn bị kẹt lại trên SKU khi on_hand có thể đã về 0. Kết quả là `avg_cost` của lần nhập sau bị méo, hoặc bị kẹp mất như L1.
- **Đề xuất sửa:**
  - Cộng `share / KeptBaseQty` vào `unit_cost` hiện tại: `unit_cost += landed_share / kept`, cùng cách DG dùng `adjustedUnitDelta`.
  - Hoặc tính `(LineAmount × kept/BaseQty + landed) / kept`.
  - Thêm unit test: trả một phần → landed cost → kiểm `unit_cost` → XT phần còn lại → `stock_value` = 0.
  - `procurement.Verify` (`verify.go:30-81`) không kiểm `receipt_lines.unit_cost`, nên lỗi này lọt qua ledger verify. Nên thêm check `unit_cost = (line_amount + landed_cost_amount + Σ DG) / base_qty` theo công thức đã chọn.

## Low

### L1. XT kẹp chênh lệch giá vốn tại 0 và làm mất phần dư

- **Vị trí:** `internal/procurement/return_posting.go:356-371`.
- **Kịch bản:** SKU dùng chung, avg_cost thấp hơn `unit_cost` của dòng PN. Phần điều chỉnh âm bị kẹp để `stock_value ≥ 0`, nên phần chênh không được ghi vào `cogs_adjustments` hay bất kỳ đâu. Tổng giá trị hàng trả khác `line_amount` của XT.
- **Ghi chú:** dev đã nêu. Đề xuất ghi phần bị kẹp vào `cogs_adjustments` (source `purchase_return` cần mở rộng CHECK bằng migration mới), hoặc ít nhất log và verify.

### L2. Đọc SKU của PO ngoài lock, trước khi coordinator khoá

- **Vị trí:** `internal/procurement/receipt_posting.go:365-377`.
- **Kịch bản:**
  1. Client gửi PN cho một PO còn `draft`. Pre-read lấy SKU X.
  2. Đồng thời, người khác `PUT lines` đổi sang SKU Y và `:confirm`.
  3. `Apply` thấy PO `confirmed` và nhận SKU Y, mà sku_stock của Y không nằm trong LockSet.
- **Mức:** hiếm. Inventory có lẽ vẫn tự `FOR UPDATE` sku_stock nên không sai số liệu, nhưng thứ tự lock toàn cục không còn được bảo đảm.
- **Đề xuất:** trong `Apply`, sau `getOrder`, so tập SKU thực tế với `c.skus` và trả 409 để client retry nếu lệch.

### L3. Phiếu đảo PN là header `posted` với `total_amount` dương

- **Vị trí:** `internal/procurement/receipt_posting.go:781-787`.
- **Rủi ro:** báo cáo phase 12 lọc `status = 'posted'` sẽ bỏ PN gốc (đã `reversed`) nhưng lại cộng phiếu đảo với tổng dương. Doanh số nhập sẽ hiện như chưa đảo.
- **Đề xuất:** lưu `total_amount` âm cho phiếu đảo (CHECK `paid_now_amount <= total_amount` vẫn qua vì paid_now = 0). Hoặc ghi rõ trong docs và test của phase 12 rằng mọi truy vấn phải loại `reversal_of IS NOT NULL`.

### L4. Chiết khấu PO phân bổ theo từng đợt, sai số làm tròn bị mất

- **Vị trí:** `internal/procurement/receipt_posting.go:257-267`.
- **Kịch bản:** dòng PO 3 cái, chiết khấu 100 đ, nhận 3 đợt mỗi đợt 1 cái. Mỗi đợt được 33 đ, tổng 99 đ, nên NCC bị ghi nợ thừa 1 đ.
- **Đề xuất:** đợt nào làm số nhận đạt đủ thì lấy phần chiết khấu còn lại: `ol.DiscountAmount − Σ đã phân bổ`.

### L5. Reauth chỉ có ở `:reverse`

- **Vị trí:** `internal/procurement/http.go:62`.
- **Ghi chú:** `close-short` (đóng thiếu, ảnh hưởng PUR-11) và DG giảm công nợ cũng nhạy cảm. Debt đã yêu cầu reauth cho write-off và offset. Đề xuất cân nhắc thêm cho DG có `payable_delta < 0`. Đây là quyết định sản phẩm, cần lead xác nhận.

### L6. `SetRetailPrice` đổi giá bán lẻ thật từ một PO nháp

- **Vị trí:** `internal/procurement/orders.go` (`buildOrderLines`) và `app/procurement_ports.go:186-191`.
- **Ghi chú:** đúng theo PRC-08 và plan. Tuy nhiên PO nháp bị xoá thì giá vẫn giữ (dev đã nêu). Hiện không có RBAC riêng cho việc đổi giá, nhưng toàn dự án cũng chưa có RBAC theo route, nên không tính là lỗi riêng của phase này.

### L7. Test tích hợp phụ thuộc thứ tự và endpoint dev

- **Vị trí:**
  - `internal/app/procurement_integration_test.go:59-62`: `objectstore.NewS3` trỏ cứng `127.0.0.1:9000` với credential dev.
  - Dòng 274-321 cùng 563-564 và 586: các subtest dùng chung `order`, `first`, `second`, `costed`.
- **Rủi ro:**
  - Nếu subtest đầu fail, các subtest sau panic do `first.Lines[0]` rỗng, che mất lỗi gốc.
  - Endpoint MinIO dev được hardcode. Nếu sau này luồng procurement thật sự gọi object store, test sẽ phụ thuộc môi trường dev.
  - Tôi chưa xác minh `testkit.DB` có dùng testcontainers hay không, vì Bash không chạy được.
- **Đề xuất:** dùng `t.Fatal` sớm khi state chung rỗng (`if first.ID == uuid.Nil { t.Skip(...) }`), và dùng object store của testkit nếu có.

## Ghi chú, cần lead xác nhận nghiệp vụ

- Settlement `replacement` hiện xử lý giống hệt `credit_payable` (`return_posting.go`). Cần xác nhận "đổi hàng" có cần giữ payable mở chờ hàng thay thế hay không.
- Landed cost không có số chứng từ riêng (`DocPrefix ""`) và audit dùng số PN. Thiết kế §3 cũng không có cột `doc_no`, nên chấp nhận được.
- Phiếu đảo PN dùng tiền tố PN. Cần xác nhận với ubiquitous-language §5.
- `matched_amount` không trừ hàng trả. Comment ở `matching.go:196-200` giải thích lý do là NCC xuất hoá đơn điều chỉnh riêng. Hợp lý nhưng cần PO xác nhận (đây cũng là câu hỏi mở số 1 của dev).
- Dev sửa thêm `migrations/isolation_integration_test.go`, là file dùng chung. Thay đổi nhỏ và đúng mục đích, nhưng owner của migrations nên biết.

## Đã kiểm tra và thấy đúng

| Hạng mục | Bằng chứng |
|---|---|
| Thứ tự coordinator | `posting/coordinator.go`: replay → `CanPost` → `Periods.Check` → `Idempotency.Begin` → `issueDocNos` (sắp xếp, trước lock) → `locker.Acquire` → `Apply` → audit → outbox → `Complete`, retry một lần khi deadlock |
| Thứ tự lock | sku_stock (advisory, tăng dần) → row PO/PN (`FOR UPDATE OF o/r`) → obligations/payments do debt khoá theo id. Mọi luồng procurement đều lấy sku_stock trước PO/PN, nên không thấy vòng lock |
| Payable một lần | `RecordPayable` theo `(receipt, id)`. Debt trả 409 `obligation_exists` nếu khác giá trị. Hoá đơn NCC không tạo obligation (có integration test) |
| XT đúng lô/màu | `return_posting.go` xuất từ `*rl.LotID`. Integration test kiểm lô tím còn 6, lô đỏ còn 10 |
| Giới hạn trả | `return_posting.go:237` kiểm dưới `getReceipt(…, true)` và trả 409 `return_exceeds_received` |
| Điều kiện đảo PN | Không đảo được phiếu đảo, phiếu đã đảo, phiếu có follow-up hoặc hàng đã tiêu thụ (`insufficient_stock` → 409). Có integration test cho cả 4 trường hợp |
| Landed cost / DG | Chia phần tồn/đã bán theo `lot_balances`. Phần tồn làm tròn đồng, phần đã bán là phần dư, nên CHECK `on_hand + sold = amount` luôn đúng. Đơn giá lưu 4 chữ số thập phân |
| Tenant | Mọi query chạy qua `WithTenantTx` và RLS. FK đều dạng composite `(tenant_id, id)` (000014). Id của tenant khác trả 404 hoặc 422 `not_a_supplier`. Advance được debt kiểm party |
| Ràng buộc dự án | Migration 000040 chỉ có `ALTER COLUMN` và CHECK, kèm down migration. Không có trigger, function hay CASCADE. `cogs_adjustments` chỉ INSERT. Các UPDATE trên `receipts.status` và `receipt_lines.returned_base_qty/unit_cost/landed_cost_amount` là projection đã có trong thiết kế. Audit dùng dạng `entity.verb`. Không thấy mã plan, phase hay finding trong code |
| Dữ liệu NCC | Procurement chỉ đọc `supplier_profiles.status`. DTO và audit không chứa thông tin ngân hàng |
| HTTP | Posting đọc `Idempotency-Key` qua `httpx.IdempotencyKey`. PATCH, DELETE, PUT lines, confirm, close-short và match yêu cầu `If-Match` (thiếu thì 428, có test). PO và hoá đơn NCC trả ETag |

Phần procurement của OpenAPI chưa được đối chiếu chi tiết từng schema. Tôi dựa vào `openapi_contract_test.go` mà dev báo là xanh, nhưng chưa chạy lại.

## Test còn thiếu

1. Đảo PN của PO `closed_short` (M1).
2. Replay posting sau khi entitlement chuyển sang read-only (M4).
3. Landed cost sau khi đã trả một phần, rồi XT phần còn lại và kiểm `stock_value` (M5).
4. Gắn cùng một PN vào hai hoá đơn (M3).
5. Concurrency:
   - hai PN song song trên cùng dòng PO (kiểm 409 `receipt_exceeds_order` dưới lock);
   - reverse song song với match (M2);
   - XT song song trên cùng dòng.
6. XT với settlement `refund` và `replacement`. Hiện chỉ có `credit_payable`.
7. DG tăng giá (`payable_delta > 0`, tạo obligation mới), và DG kind `post_purchase_discount`.
8. Kỳ đã khoá: post PN với `business_date` trong kỳ khoá phải trả 409.
9. Replay cho XT, DG và landed cost. Hiện chỉ có replay cho PN.
10. Tenant isolation với `sku_id`, `color_id`, `reason_id`, `payment_id` (advance) và `payment_method_id` của tenant khác. Hiện chỉ có supplier và receipt.

## Đề xuất hành động (theo thứ tự)

1. Sửa M1 và thêm test.
2. Chuyển route posting sang `TenantPostingAPI()` (M4).
3. Khoá PN trong `linkReceipts` (M2). Chốt nghiệp vụ một PN thuộc một hay nhiều hoá đơn (M3).
4. Sửa công thức `unit_cost` của landed cost (M5).
5. Bổ sung các test ở mục trên, nhất là 1–5.
6. Chạy lại `go vet`, `golangci-lint` và `go test -race -tags integration` vì reviewer không chạy được.

## Câu hỏi chưa giải quyết

- Một PN có được thuộc nhiều hoá đơn NCC không (M3)?
- Settlement `replacement` có cần hành vi khác `credit_payable` không?
- Có cần reauth cho close-short và cho DG giảm công nợ không?

Status: DONE_WITH_CONCERNS
Summary: Lõi sổ cái của procurement đúng thiết kế (thứ tự coordinator, thứ tự lock, payable một lần, XT đúng lô, RLS và FK composite), nhưng có 5 lỗi Medium: không đảo được PN của PO đã đóng thiếu, race giữa đối chiếu và đảo PN, một PN khớp được nhiều hoá đơn, route posting đặt sai group entitlement, và `unit_cost` của landed cost sai sau khi trả hàng.
Concerns/Blockers: Bash lỗi E2BIG trong suốt phiên nên chưa chạy go vet, golangci-lint và go test; các kết luận dựa trên đọc code. Lead cần chạy lại ba lệnh trước khi đóng phase.
