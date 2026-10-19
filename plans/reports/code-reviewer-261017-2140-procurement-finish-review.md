# Review code: hoàn tất Phase 08 (procurement)

Ngày: 2026-10-17 (Asia/Saigon). Người review: code-reviewer. Chỉ đọc, không sửa code.

## Phạm vi

- Nguồn phạm vi: `plans/reports/fullstack-developer-261017-2125-procurement-finish.md` + đọc file trực tiếp (repo chưa commit).
- Bỏ qua: `internal/sales/**`, `sales_ports.go`.
- File chính đã đọc:
  - `apps/api/internal/procurement/verify.go`, `matching.go`, `price_adjust.go`, `landed_cost.go`, `return_posting.go`, `receipt_posting.go`, `repo.go`
  - `apps/api/internal/org/attachments.go`, `org/http.go`
  - `apps/api/internal/app/domains.go`, `routes.go`, `app.go`, `procurement_integration_test.go`, `app_integration_test.go`
  - `apps/api/internal/debt/adjustments.go`, `apps/api/internal/posting/coordinator.go`, `apps/api/internal/cli/serve.go`
  - Migrations 000013, 000014, 000038, 000039
  - `api/openapi.yaml` (/attachments), `apps/web/src/shared/api/schema.d.ts`
- Lưu ý: `app/domains.go` và `debt/adjustments.go` thay đổi trên đĩa trong lúc review. Báo cáo này dựa trên trạng thái cuối cùng đã đọc lại.

## Kiểm chứng đã chạy

| Lệnh | Kết quả |
|------|---------|
| `go vet ./...` | sạch |
| `go test ./internal/org/ ./internal/app/ ./internal/procurement/ ./internal/posting/` (unit) | ok |
| `go test -tags integration -run 'TestProcurement\|TestAttachment' ./internal/app/` (3 lần) | 2 FAIL (429 rate_limited), 1 ok |
| `go test -tags integration ./internal/app/ ./internal/org/ ./internal/procurement/ ./internal/cli/` | ok (lần chạy cuối) |
| `tsc --noEmit` phía web | ok |

Không đụng tới DB dev `tally-postgres-1`; integration test dùng testcontainers.

## Đánh giá tổng quan

Logic nghiệp vụ của 4 check Verify, registry owner và gate entitlement về cơ bản đúng. SQL khớp schema. Tolerance là một cận chặt và có lý do rõ ràng. RLS cô lập đúng tenant. Replay đứng trước CanPost, nên store read-only lấy lại kết quả cũ mà không ghi thêm gì.

Có 1 lỗi High: integration test flaky vì rate limit, làm CI không ổn định. Có 3 lỗi Medium:
- Verify báo sai trên dữ liệu hợp lệ khi DG bị clamp về 0.
- Adapter entitlement bỏ qua `q`, dễ gây cạn pool khi Phase 13 cắm reader thật.
- Biên bản bù trừ chấp nhận attachment `pending` (chưa upload).

## Critical

Không có.

## High

### H1. `TestProcurementOverHTTP` flaky do 429 rate_limited

- Vị trí:
  - `apps/api/internal/app/procurement_integration_test.go:970` (subtest attachment) và `:905` (subtest "ids of another store")
  - Limiter: `apps/api/internal/app/app.go:33-34,139` (`apiRatePerSecond=20`, `apiRateBurst=100`, theo IP)
- Kịch bản:
  - Mỗi `browser` trong test có `RemoteAddr` cố định `10.x.y.z:40000`.
  - Toàn bộ subtest của `TestProcurementOverHTTP` chạy tuần tự trên cùng browser `a`.
  - Các subtest mới (read-only ở `:871`, attachment ở `:970`) đẩy tổng số request vượt burst 100 khi máy chạy nhanh. Không bật `-race` thì refill 20 req/s không theo kịp.
  - Kết quả: 2/3 lần chạy fail với 429 ở dòng 922 và 971. Lần chạy toàn package cuối cùng thì pass.
  - Đây là loại lỗi pass trên máy dev nhưng làm đỏ CI ngẫu nhiên.
- Đề xuất (chọn một):
  1. Cho test stack một option tắt hoặc nới limiter, ví dụ `app.WithRateLimit(rate, burst)`. Option này chỉ dùng trong `newTestStack`, giữ giá trị production.
  2. Tạo browser hoặc sign-in mới (IP khác) cho các subtest cuối: read-only, cross-tenant, attachment.
  3. Tách `TestProcurementOverHTTP` thành vài top-level test, mỗi test có browser riêng.

  Khuyến nghị (1): nó loại hẳn nguồn flaky cho mọi test HTTP về sau. Nên giữ một test riêng chứng minh limiter vẫn hoạt động.

## Medium

### M1. Verify "receipt line unit cost" báo sai khi DG đẩy giá vốn âm rồi bị clamp về 0

- Vị trí:
  - `apps/api/internal/procurement/price_adjust.go:260-263` (`if cost.IsNegative() { cost = money.ZeroMoney }`)
  - Check bị ảnh hưởng: `apps/api/internal/procurement/verify.go:123-130`
- Kịch bản:
  - Dòng PN có chiết khấu (line_amount < qty × unit_price), hoặc đã có một DG giảm trước đó.
  - Một DG mới hạ giá về 0, hoặc mức giảm lớn hơn giá trị còn lại trên dòng.
  - `perUnit` âm hơn `unit_cost` hiện tại nên cost bị clamp về 0. `on_hand_amount` vẫn ghi đủ delta âm.
  - Khi đó `keptCost` (line_amount + landed + Σ adj − Σ return) < 0, trong khi `unit_cost × kept = 0`. Chênh lệch vượt tolerance nên Verify báo lỗi trên dữ liệu do chính hệ thống ghi ra.
  - Thêm nữa, phần bị clamp không đi đâu cả: tồn kho giảm ít hơn số `on_hand_amount` ghi nhận. Đây là sai lệch giá trị thật, không chỉ là cảnh báo giả.
- Đề xuất:
  - Từ chối DG khi `UnitCost + perUnit < 0`: trả 422, field `lines[i].new_unit_price`, code `cost_below_zero`.
  - Hoặc: tách phần vượt quá sang `sold_amount`/COGS để `on_hand_amount` chỉ ghi phần đã thực sự rút khỏi tồn.
  - Dù chọn cách nào, thêm một ca integration "DG về 0 trên dòng có chiết khấu" rồi chạy Verify.

### M2. `postingEntitlements.CanPost` bỏ qua `q`, có nguy cơ cạn pool khi có reader dùng DB

- Vị trí: `apps/api/internal/app/domains.go:116-117`
- Kịch bản:
  - Coordinator gọi `CanPost` trong lúc đang giữ tx tenant (một connection).
  - Adapter gọi `middleware.EntitlementReader.CanWrite(ctx, tenantID)`. Reader của Phase 13 (đọc subscription từ DB) sẽ lấy thêm connection thứ hai từ pool.
  - Với `MaxConns 20` và khoảng 20 posting đồng thời, mỗi posting giữ 1 connection rồi chờ connection thứ 2, nên pool có thể kẹt cứng (deadlock theo kiểu pool exhaustion).
  - Hiện tại không xảy ra vì mặc định là `writesAllowed{}` và test dùng `statusEntitlements` không cần DB của request. Đây là cái bẫy dành cho Phase 13.
- Đề xuất:
  - Trong `postingEntitlements`, type-assert `posting.EntitlementGuard` trên reader: nếu reader cài sẵn thì gọi `CanPost(ctx, q, tenantID)`, chỉ fallback sang `CanWrite` khi không có.
  - Hoặc ghi rõ trong doc comment và trong plan Phase 13 rằng reader phải dùng `q` được truyền vào.
  - Plan Phase 13 đã nói Reader sẽ implement cả hai interface. Cần adapter ưu tiên nhánh đó, nếu không thì sau này vẫn đi đường `CanWrite`.

### M3. Bù trừ (BT) chấp nhận attachment `pending`, tức file có thể chưa từng được upload

- Vị trí: `apps/api/internal/debt/adjustments.go:256-263`
- Kịch bản:
  - Client gọi `POST /attachments` (owner_type `party`), nhận presigned URL nhưng không upload. Attachment ở trạng thái `scan_status='pending'`.
  - Client gọi tiếp API bù trừ với `attachment_id` đó. Check chỉ từ chối `rejected`, nên BT được ghi sổ mà không có biên bản thật.
  - Theo `org/attachments.go:195-225`, chỉ bước xác nhận upload mới chuyển `pending` sang `clean`/`rejected`.
  - Hệ quả: yêu cầu "phải có biên bản ký" bị lách chỉ bằng một request tạo metadata.
- Đề xuất:
  - Đổi điều kiện thành `scan != "clean"` thì trả `attachment_invalid`. Message gợi ý: "Upload the signed offset record before posting".
  - Thêm ca test với attachment pending.
  - Nếu sản phẩm cố ý cho phép pending, ví dụ để upload chạy bất đồng bộ, thì cần ghi rõ quyết định đó. Tôi không tìm thấy quyết định này trong report của dev hay plan Phase 10.
  - Đã kiểm lại sau bản sửa mục 2b: điều kiện owner đã đúng, nhưng lỗ `pending` vẫn còn.
  - Ca thành công trong `apps/api/internal/app/debt_cash_integration_test.go:296,318-320` dùng chính một attachment vừa `POST /attachments` mà chưa xác nhận upload, tức đang ở `pending`. Test này đang khẳng định hành vi lỗi.
  - Khi siết điều kiện thành `clean`, test phải upload thật (PUT lên object store giả của stack) rồi gọi bước xác nhận trước khi bù trừ.
  - Test `internal/debt/debt_integration_test.go:874-877` chèn thẳng `'clean'` nên không bị ảnh hưởng.

### M4. Hiệu năng Verify: subquery tương quan theo `receipt_line_id` nhưng không có index

- Vị trí:
  - `apps/api/internal/procurement/verify.go:41-48` (keptCost: 4 subquery tương quan mỗi dòng PN) và `:107-112` (matched)
  - Migrations 000013/000014: không có index trên `purchase_return_lines.receipt_line_id`, `landed_cost_allocation_lines.receipt_line_id`, `purchase_price_adjustment_lines.receipt_line_id`, `purchase_price_adjustments.receipt_id`.
- Kịch bản:
  - Tenant có khoảng 100k dòng PN. Check "receipt line unit cost" chạy 4 seq scan bảng con cho mỗi dòng, tức O(N×M).
  - Verify (dùng trong job đối soát hoặc lệnh CLI) có thể chạy hàng phút và giữ snapshot dài.
  - Các check cũ cũng dùng cùng pattern, nên đây không phải lỗi mới, nhưng check mới nhân số subquery lên.
- Đề xuất:
  - Viết lại thành CTE gom trước bằng `GROUP BY receipt_line_id`, rồi `LEFT JOIN`.
  - Hoặc thêm index `(tenant_id, receipt_line_id)` cho 3 bảng dòng con và `(tenant_id, receipt_id)` cho `purchase_price_adjustments`. Các index này cũng giúp đường posting, vì posting đọc các bảng này theo dòng.

## Low

### L1. Nhãn variance lệch định dạng giữa stored và expected
- Vị trí: `apps/api/internal/procurement/verify.go:114-116`
- Kịch bản: khi lệch, stored in ra `matched 0.0000` còn expected in ra `matched 0`. Không gây báo sai vì điều kiện WHERE so sánh số, nhưng người đọc log dễ tưởng sai lệch nằm ở định dạng.
- Đề xuất: ép cùng kiểu cho cả hai, ví dụ `d.variance_amount::numeric(20,4)::text` ở cả hai vế, hoặc `to_char`.

### L2. Test tamper chưa phủ đủ nhánh
- Vị trí: `apps/api/internal/app/procurement_integration_test.go:1117-1143`
- Kịch bản:
  - Ca 1 chỉ đổi `status='reversed'`, nên nhánh `r.reversal_of IS NOT NULL` (khớp với phiếu đảo) của check "supplier document receipt" không được chứng minh.
  - Không có ca DG nào trong dữ liệu mẫu của test tamper. Phần `purchase_price_adjustment_lines` trong `keptCost` và `payable_delta` trong `matched` chỉ được kiểm gián tiếp qua TestProcurementOverHTTP "every ledger verifies".
  - Ca 4 (unit_cost 10600) chứng minh tốt check unit cost và tolerance.
- Đề xuất:
  - Thêm ca chèn một dòng `supplier_document_receipts` trỏ vào phiếu đảo.
  - Thêm ca sửa `purchase_price_adjustment_lines.on_hand_amount` của một DG đã post.

### L3. `CreateAttachment` presign trước khi kiểm tra owner
- Vị trí: `apps/api/internal/org/attachments.go:136-140`
- Kịch bản: request với owner_id của tenant khác hoặc không tồn tại vẫn tạo ra một presigned URL, rồi mới bị 422 `owner_id not_found`. URL không được trả về và không có bản ghi DB, nên rủi ro thấp. Nhưng đó là một lời gọi object store thừa, và thứ tự này dễ thành lỗ hổng nếu sau này có ai trả URL sớm.
- Đề xuất: kiểm tra owner trong `WithTenantTx` trước, sau đó presign. Hoặc presign trong tx sau bước check owner.

### L4. Inventory posting mất replay khi store read-only (đã có từ trước)
- Vị trí: `apps/api/internal/app/routes.go` (nhóm inventory nằm trên `TenantAPI`, có middleware Entitlement)
- Kịch bản: store read-only gửi lại một phiếu inventory đã commit. Middleware chặn 403 trước khi tới coordinator, nên client không lấy lại được kết quả cũ. Debt và procurement thì đã đúng (201 replay).
- Đề xuất: chuyển các route posting inventory sang nhóm Posting để hành vi đồng nhất. Ngoài phạm vi Phase 08, nên ghi vào backlog.

### L5. `POST /attachments` không có RBAC ngoài auth (đã có từ trước)
- Vị trí: `apps/api/internal/org/http.go:60`
- Kịch bản: bất kỳ user nào của tenant (kể cả vai trò chỉ xem) cũng tạo được attachment cho receipt, supplier document hoặc party. Nhờ RLS nên không rò dữ liệu chéo tenant.
- Đề xuất: map quyền theo owner_type, ví dụ `receipt`/`supplier_document` thì yêu cầu quyền procurement write. Có thể gắn quyền vào registry: `AttachmentOwner{Exists, Permission}`.

### L6. Enum owner_type trong OpenAPI phải sửa tay mỗi khi thêm owner
- Vị trí: `api/openapi.yaml` khoảng dòng 960-1006 (request enum `[product, party, receipt, supplier_document]`), schema `Attachment.owner_type` ở khoảng dòng 6463 (string tự do)
- Kịch bản: thêm owner mới vào registry mà quên enum thì client TS sinh ra từ schema sẽ từ chối giá trị hợp lệ. `TestAttachmentOwnersRegistered` khóa danh sách phía Go nên dev sẽ nhớ, nhưng không có test đối chiếu với YAML.
- Đề xuất: thêm một unit test đọc `openapi.yaml` và so enum với các key của registry. Không bắt buộc.

### L7. OpenAPI không ghi mã 403 `subscription_read_only` cho các endpoint posting (đã có từ trước)
- Vị trí: `apps/api/api/openapi.yaml`, ví dụ `/debt-offsets` 403 ở khoảng dòng 4097-4099 ("reauth_required or support_read_only"). `grep subscription_read_only` trong file trả 0 kết quả. Mã lỗi đến từ `internal/platform/apperr/apperr.go:121`.
- Kịch bản: gate posting giờ trả 403 `subscription_read_only` cho debt và procurement, nhưng hợp đồng không nhắc tới. Web client sẽ không có nhánh xử lý riêng (ví dụ banner "store đang read-only") và hiển thị như lỗi chung.
- Đề xuất: thêm `subscription_read_only` vào mô tả 403 của các route posting, hoặc vào response `Error` dùng chung.

## Bổ sung phạm vi: BT chỉ nhận biên bản của chính đối tác (mục 2b report dev)

Đã đọc:
- `apps/api/internal/debt/adjustments.go:252-263`
- `apps/api/internal/debt/debt_integration_test.go:872-880`
- `apps/api/internal/app/debt_cash_integration_test.go:288-332`
- `apps/api/api/openapi.yaml:4077-4108`
- Migrations 000003 (attachments) và 000020 (FK `fk_debt_offsets__attachment_id`)

Đã chạy:
- `go vet ./internal/debt/ ./internal/app/`: sạch.
- `go test -tags integration -run 'TestDebt|Offset' ./internal/debt/ ./internal/app/`: ok.
- Sinh lại `schema.d.ts` vào scratchpad bằng `openapi-typescript ../api/api/openapi.yaml` rồi diff với `apps/web/src/shared/api/schema.d.ts`: trùng khớp.

Kết luận:
- **Đúng:**
  - SQL `WHERE id = $1 AND owner_type = 'party' AND owner_id = party_id` chạy trên `w.q` (tx tenant). Vì vậy attachment của tenant khác không thấy được do RLS, và FK `(tenant_id, attachment_id)` chặn thêm lần nữa.
  - Sai owner type, sai đối tác hoặc không tồn tại đều trả 422 `attachment_invalid`, `fields.attachment_id = invalid`, khớp với mô tả OpenAPI.
  - Check attachment chạy trước khi khóa obligation. Request sai biên bản không giữ lock và không ghi settlement nào; test có đếm `obligation_settlements` để chứng minh.
  - Hai ca âm (biên bản của NCC khác, ảnh sản phẩm) phủ đúng hai nhánh mới. Dev đã kiểm chứng bằng cách bỏ điều kiện và thấy test fail.
  - Chưa có dữ liệu production nên không cần migrate BT cũ có `owner_type 'debt_offset'`.
- **Còn tồn tại:** lỗ attachment `pending` (M3). Test HTTP mới đang dựa vào chính hành vi này cho ca thành công.
- **Phát hiện phụ:** L7 (OpenAPI thiếu `subscription_read_only`).

## Những điểm đã kiểm và OK

- SQL của 4 check mới khớp tên cột, kiểu và trạng thái trong migrations 000013/000014/000038/000039.
- Không báo sai trên dữ liệu hợp lệ:
  - DG đảo bị loại nhờ `a.status='posted' AND a.reversal_of IS NULL`.
  - PN trả một phần dùng `kept = base_qty − returned_base_qty`. XT rút đúng `unit_cost × qty`, nên sai số không đổi.
  - Nhiều landed cost: mỗi lần làm tròn 4 chữ số được tính vào `moves`.
  - Làm tròn: cận `0.00005 × base_qty × (1+moves)` đúng bằng sai số tối đa của mỗi lần `round(...,4)` nhân với số đơn vị. Đây là cận chặt, không lỏng.
  - Ngoại lệ duy nhất là M1.
- Tính `moves` có đếm cả dòng của DG đã đảo. Việc này chỉ nới cận một chút theo hướng an toàn và hợp lý, vì DG đảo cũng đã làm tròn lại unit_cost.
- matched/variance khớp với `refreshMatches` (`matching.go:237-260`), là nơi ghi duy nhất.
- Registry AttachmentOwners:
  - Owner check chạy trong `WithTenantTx` của tenant hiện tại, nên chịu FORCE RLS. Id của tenant khác trả 422 `owner_id`/`not_found`, đã có test ở `:905` và `:970`.
  - Type lạ trả 422 `owner_type`/`invalid`.
  - `product` cũ vẫn được đăng ký.
- Wiring `org.NewService` đầy đủ ở `cli/serve.go:107` và 5 test stack. `cli/seed.go:77` không cần vì seed không upload.
- Entitlements:
  - Mọi route posting của debt và procurement đều đi qua `s.posting.Post`.
  - Trong coordinator (`posting/coordinator.go:140-170`), Replay đứng trước CanPost. Store read-only gửi lại key đã commit nhận 201 và không ghi gì thêm. Key mới bị 403 `subscription_read_only`. Đã có test ở `:871-903`.
- OpenAPI và `schema.d.ts` đã đồng bộ, `tsc` pass. Code web hiện chưa dùng `/attachments` nên không có gì bị vỡ.

## Hành động đề xuất (theo thứ tự)

1. H1: thêm option nới limiter cho test stack (hoặc browser riêng cho mỗi nhóm subtest). Chạy lại integration 5 lần liên tiếp để xác nhận hết flaky.
2. M3: đòi `scan_status='clean'` cho biên bản bù trừ, sau khi dev hoàn tất sửa `debt/adjustments.go`.
3. M1: từ chối DG làm giá vốn âm (hoặc chuyển phần dư sang COGS), kèm test và Verify.
4. M2: adapter ưu tiên `posting.EntitlementGuard` dùng `q`. Ghi ràng buộc vào plan Phase 13.
5. M4: thêm index `receipt_line_id`/`receipt_id`, hoặc viết lại Verify bằng CTE gom trước.
6. Các mục Low: đưa vào backlog.

## Theo dõi plan

- Phạm vi 1-4 của Phase 08 đã được triển khai đủ theo report dev.
- Nên chưa đánh dấu Phase 08 hoàn tất cho tới khi xử lý H1 (CI ổn định) và M3.

## Câu hỏi còn mở

- Biên bản bù trừ có cố ý chấp nhận attachment `pending` không (M3)?
- Chính sách nghiệp vụ khi DG hạ giá xuống dưới giá vốn còn lại (M1): chặn, hay đẩy phần dư vào COGS?

---

## Re-review (2026-10-17, sau mục 7 và 8 của report dev)

### Kiểm chứng đã chạy (trong `apps/api`, testcontainers; MinIO dev `tally-minio-1` dùng cho upload; không đụng `tally-postgres-1`)

| Lệnh | Kết quả |
|------|---------|
| `go vet ./... && go vet -tags integration ./...` | sạch |
| `go test -race -count=1 ./internal/procurement/ ./internal/app/ ./internal/org/ ./internal/debt/` | ok |
| `go test -tags integration -count=3 -run 'TestProcurementOverHTTP$' ./internal/app/` | ok (16.2s cho 3 lần), không có 429 |
| `go test -race -tags integration -count=2 -run 'TestProcurementOverHTTP$\|TestProcurementVerifyReportsTamperedRows' ./internal/app/` | ok (24.8s) |
| `go test -tags integration -count=1 ./internal/app/... ./internal/debt/... ./internal/org/... ./internal/procurement/... ./internal/cli/... ./migrations/...` | tất cả ok |

### Xác minh từng bản sửa

| Mục | Kết luận | Bằng chứng |
|-----|----------|------------|
| H1 (429 flaky) | Đã sửa | `internal/app/app.go:37-38,74-76,141-152`: giá trị zero giữ đúng 20/s và burst 100 cho production; `cli/serve.go` không set field này. 4 test stack truyền `testAPIRateLimit` (`app_integration_test.go:36`). Đã chạy 3 lần không race và 2 lần có race, đều xanh. |
| M1 (DG kéo giá vốn xuống dưới 0) | Đã sửa, đúng sổ | Xem "Hạch toán DG" bên dưới. |
| M2 (adapter bỏ qua `q`) | Đã sửa | `internal/app/domains.go:117-120`: reader nào cũng là `posting.EntitlementGuard` thì chạy `CanPost(ctx, q, tenantID)` trong tx posting; reader khác mới fallback sang `CanWrite`. |
| M3 (BT nhận attachment pending) | Đã sửa | `internal/debt/adjustments.go:256-264`: đòi `owner_type='party'`, `owner_id = party_id`, `scan_status='clean'`, query chạy trên tx tenant. Helper `uploadAttachment` (`debt_cash_integration_test.go:122-155`) upload thật, gọi `:complete` và khẳng định `clean`; có thêm ca âm "never uploaded". |
| M4 (Verify chậm) | Đã sửa, ngữ nghĩa giữ nguyên | Xem "CTE trong Verify" bên dưới. |
| L1 | Đã sửa | `verify.go`, check variance: cả hai vế in qua `trim_scale(...)::text`. Hàm này có từ Postgres 13. |
| L2 | Đã sửa | `procurement_integration_test.go:1186` (khớp với phiếu đảo) và `:1193` (dòng DG không khớp unit cost). |
| L3 | Đã sửa | `internal/org/attachments.go:137-149`: presign trong tx, sau khi kiểm owner. `PresignPut` chỉ ký cục bộ, không gọi mạng, nên không kéo dài tx. |
| L6 | Đã sửa | `TestOpenAPIAttachmentOwnerTypes` (`openapi_contract_test.go:193`) |
| L7 | Đã sửa cho debt và procurement | `TestOpenAPIErrorResponses` (`openapi_contract_test.go:92-127`) đòi `subscription_read_only` trong 403 của mọi route posting debt/procurement. Inventory chưa có (cùng gốc với L4). |
| L4, L5 | Chưa sửa, lý do hợp lý | Đều có từ trước. L5 cần quyết định sản phẩm. Đề nghị đưa vào backlog phase 13/inventory. |

### Hạch toán DG: công nợ, on_hand, sold/COGS

Theo `internal/procurement/price_adjust.go:84-137,263-309` và `cost_split.go:60-110`, với mỗi dòng:
- `delta = adjustmentDelta(l, old, new)`;
- `(carried, residue) = carriedCost(l, delta)`, trong đó `carried + residue = delta`;
- `costShares` chia `carried` thành `on_hand + sold`. `on_hand` vào ledger qua `CostAdjust`; `sold` cộng với `residue` vào `cogs_adjustments` theo SKU, `insertCogs` bỏ qua dòng 0.

Do đó, cho từng chứng từ: `Σ on_hand + Σ sold + Σ residue = Σ delta = payable_delta`. Công nợ (payable mới hoặc credit note) ghi đúng `payable_delta`. Khớp.

Các điểm đối chiếu thêm:
- **Verify vẫn đúng.** `purchase_price_adjustment_lines` lưu `on_hand + sold = carried` (không lưu residue), còn `unit_cost` chuyển theo `carried / kept`. Check "receipt line unit cost" vì vậy vẫn khớp; `residue` chỉ nằm ở COGS. Check "matched amount" dùng `payable_delta` nên đã gồm residue.
- **Tính tay ca integration** (10 cái giá 10000, chiết khấu 20000, bán 4, DG về 0):
  - `line_amount = 80000`, `priceToAmount = 1`, `delta = −80000`;
  - `unit_cost = 8000`, sàn `−80000`, nên `carried = −80000` và `residue = 0`;
  - lot giữ 6: `on_hand = −48000`, `sold = −32000`;
  - khớp số liệu dev báo và assertion ở `procurement_integration_test.go:775-793`.
- **XT sau DG.** `returnCredit` (`return_posting.go:88-99`) cộng `(on_hand + sold) × q / base_qty` của từng DG. Với ca trên, trả 6 cái thì credit = `80000×6/10 − 80000×6/10 = 0` và rút kho ở `unit_cost 0`. Nhất quán.
- **Residue chỉ còn đến từ làm tròn** (delta làm tròn tới đồng, unit_cost làm tròn tới 4 chữ số), tức dưới 1 đồng mỗi dòng. Lập luận: `|delta| ≤ phần kept của line_amount` (đã gồm thuế) `≤ unit_cost × kept` (đã gồm landed cost).

### CTE trong Verify: so ngữ nghĩa với bản cũ

| Check | Bản cũ (subquery tương quan) | Bản mới (CTE) | Giống nhau? |
|-------|------------------------------|---------------|-------------|
| PO line received | Σ `base_qty`; điều kiện `<> wrong_item`, `posted`, không phải đảo | `received` cùng điều kiện, thêm `po_line_id IS NOT NULL` (vô hại) | Có |
| PO line returned | Σ dòng XT posted qua `receipt_lines` | `returned` rồi `ordered` gom theo PO line | Có |
| receipt line returned, landed, allocation | Σ theo dòng hoặc phiếu | CTE `GROUP BY` cùng khóa, `COALESCE(...,0)` | Có |
| matched | `total + Σ payable_delta` (DG posted, không đảo) | `owed`: LEFT JOIN với điều kiện nằm trong `ON` (không ở `WHERE`), nên phiếu không có DG vẫn ra `total` | Có |
| variance | `n = 0` → unmatched | `m.id IS NULL` → unmatched | Có |
| unit cost | amount: DG posted, không đảo; `moves` đếm cả DG đã đảo | `FILTER (WHERE posted AND reversal_of IS NULL)` cho amount, `COUNT(*)` cho `n` | Có |

Mỗi check giờ quét mỗi bảng con một lần, không còn phụ thuộc index `receipt_line_id`. Test tamper (6 ca) và "every ledger verifies" đều xanh.

### Phát hiện mới

#### N1 (Medium). DG giảm giá bị từ chối 409 `stock_value_negative` khi giá trị tồn của SKU thấp hơn giá vốn phần "held" của dòng

Không gây sai sổ, nhưng chặn một nghiệp vụ hợp lệ.

- **Vị trí:**
  - `internal/procurement/cost_split.go:84-104`: `on_hand` được tính theo giá vốn riêng của dòng PN và gửi thẳng sang `CostAdjust`;
  - `internal/inventory/ledger.go:730-732` và `costing.go:57-64`: `OnCostAdjust` kiểm `stock_value + delta ≥ 0` ở mức SKU (moving average).
- **Kịch bản (suy ra từ code, chưa chạy thực nghiệm):**
  1. Cùng một SKU: PN-B nhập trước 10 cái giá 2000 (20000), PN-A nhập sau 10 cái giá 8000 (80000).
  2. Bán 14 cái theo FIFO lot: hết lot B, thêm 4 cái của lot A. COGS tính theo giá trung bình 5000/cái, nên tồn SKU còn 6 cái, giá trị 30000.
  3. NCC của PN-A giảm giá về 0. Khi đó `held = min(lot A = 6, kept = 10) = 6`, `on_hand = −8000 × 6 = −48000`, trong khi tồn SKU chỉ có 30000.
  4. `OnCostAdjust` trả 409 `stock_value_negative`. Store không ghi nhận được credit note của NCC, dù credit đó là thật. Giảm về 2000 cũng bị chặn (`on_hand = −36000`).
- **Vì sao là lỗi:**
  - XT đã giải đúng bài này bằng `stockShare` (`return_posting.go:328-395`, migration 000039): phần tồn kho không gánh được thuộc về hàng đã bán theo giá trung bình, nên vào COGS.
  - `carriedCost` mới chỉ đặt sàn theo `unit_cost` của dòng, không theo giá trị tồn của SKU.
- **Đề xuất:**
  - Trong `costShares`, gom `on_hand` theo SKU, đọc `readSkuStock` và áp `stockShare`. Phần không gánh được cộng vào `sold_amount` của dòng (chuyển từ on_hand sang sold).
  - Cách này giữ nguyên `on_hand + sold = carried`, nên Verify và `returnCredit` không đổi.
  - Thêm ca integration với hai PN giá chênh nhau mạnh trên cùng SKU, bán qua lot rẻ, rồi DG mạnh PN đắt. Khẳng định 201, `payable_delta` đủ, `stock_value ≥ 0`, Verify sạch.
  - Landed cost cũng đi qua `costShares` nhưng luôn dương, nên không bị ảnh hưởng.

#### N2 (Low). `priceToAmount` bằng 1 khi dòng nhập với giá trị ròng 0, nên DG tăng giá sau đó không mang theo thuế
- **Vị trí:** `internal/procurement/price_adjust.go:96-102`
- **Kịch bản:** dòng nhập với chiết khấu bằng toàn bộ giá trị (net 0, line_amount 0). DG tăng giá lên trên chiết khấu thì delta = phần net tăng × 1, không cộng VAT. Kết quả là công nợ thấp hơn hóa đơn NCC đúng bằng phần thuế.
- **Đề xuất:** dùng thuế suất snapshot của dòng (nếu có) thay cho tỉ lệ `line_amount / net` khi net bằng 0, hoặc chặn ca này bằng 422. Ca hiếm, có thể đưa vào backlog.

#### N3 (Low). Integration test của debt và org giờ phụ thuộc MinIO dev dùng chung
- **Vị trí:** `internal/app/debt_cash_integration_test.go:122-155`
- **Kịch bản:** máy dev chưa chạy `make db-up` thì `TestDebtAndCashOverHTTP` fail (thông báo rõ ràng). Object test được xoá trong `t.Cleanup`, nhưng nếu test bị kill giữa chừng thì object còn lại trong bucket `tally-dev`. CI đã dựng MinIO nên không chặn.
- **Đề xuất:** về sau chuyển sang MinIO testcontainers như Postgres. Không gấp.

### Kết luận: Phase 08 có đóng được không?

**Có, đóng được.**
- H1 và M1-M4 đã sửa đúng và đủ, có test chứng minh, và tôi đã chạy lại xanh, kể cả `TestProcurementOverHTTP -count=3` và chạy với `-race`.
- Hạch toán DG khớp: `on_hand + sold + residue = payable_delta`. CTE của Verify giữ đúng ngữ nghĩa.
- N1 là lỗi mới tìm thấy. Nó không làm sai sổ: posting bị từ chối nguyên khối và không ghi gì. Nhưng nó chặn một credit note hợp lệ trong tình huống bán lẻ thực tế (cùng SKU nhập từ nhiều NCC với giá chênh nhau).
- Đề nghị sửa N1 trong một bản follow-up nhỏ (tái dùng `stockShare`) trước phase 21, hoặc ngay bây giờ nếu muốn đóng sạch. N2, N3, L4, L5 đưa vào backlog.

---

## Re-review N1/N2 (2026-10-17, sau mục 9 report dev)

Đã chạy:
- `go vet` (thường và `-tags integration`) cho procurement và app: sạch.
- `go test -race ./internal/procurement/`: ok.
- `go test -tags integration -count=2 -run 'TestProcurementOverHTTP$|TestProcurementVerifyReportsTamperedRows' ./internal/app/`: ok. Lần chạy này gồm hai subtest mới ở `procurement_integration_test.go:801` và `:838`.

### N1: đã sửa đúng

Vị trí: `internal/procurement/cost_split.go`, `costShares`.

- **`on_hand + sold = carried` luôn giữ.**
  - `splitShare` cho ra `onHand + sold = shares[i]`.
  - Sau đó `booked = stockShare(onHand, st)` và `sold += onHand − booked`, nên tổng không đổi.
  - `shares[i]` chính là `carried` của dòng. Vì vậy Verify (check unit cost) và `returnCredit` không đổi.
- **Không tính tồn hai lần khi nhiều dòng cùng SKU.**
  - `readSkuStock` đọc mỗi SKU một lần (`sortedUnique`). Mỗi dòng cộng `booked` vào `st.StockValue` rồi ghi lại map trước khi xét dòng sau.
  - Thứ tự `costs` gửi đi trùng thứ tự vòng lặp. Adapter `procurementStock.CostAdjust` (`app/procurement_ports.go:56-63`) và `StockLedger.CostAdjust` (`inventory/ledger.go:205-222`) giữ nguyên thứ tự này.
  - Ledger kiểm `OnCostAdjust` theo đúng chuỗi cộng dồn đó, nên không thể 409. Đúng cả khi một DG vừa có dòng tăng vừa có dòng giảm trên cùng SKU.
  - Khi `OnHand = 0`, `stockShare` trả 0, khớp với điều kiện `no_stock_to_adjust` của ledger.
- **Verify vẫn bắt được tamper.** Tamper `on_hand_amount` của dòng DG (`:1193`) vẫn ra đúng một mismatch, và test xanh.
  - Ghi chú (Low, có từ trước): Verify chỉ kiểm tổng `on_hand + sold`. Nó không đối chiếu `on_hand_amount` với move `cost_adjustment` trong ledger, cũng không đối chiếu `sold_amount + residue` với `cogs_adjustments` của chứng từ.
  - Vì vậy một lỗi tách sai giữa tồn kho và COGS (tổng vẫn đúng) sẽ không bị bắt.
  - Đề xuất: thêm check "price adjustment split" (Σ on_hand theo chứng từ = Σ move cost_adjustment; Σ sold + residue = Σ cogs của chứng từ). Có thể để backlog.
- **Landed cost:** dương, chỉ đổi hành vi khi SKU hết tồn (vào COGS thay vì 409). Cách này đúng quy ước của migration 000039.

### N2: đúng khi phiếu có thuế; có một trường hợp suy sai thực tế

Vị trí: `internal/procurement/price_adjust.go:98-122`.

- **Khi `tax_amount > 0`, suy luận basis là chính xác.**
  - Pricing dựng mỗi dòng theo `Gross = Line (− Tax nếu thuế cộng lên trên) + LineDiscount + OrderAlloc` (`pricing/calculator.go:195-201`). `receiptTotals` cộng đúng các dòng đó (`receipt_posting.go:348-359`).
  - Do đó `total = goods − discount` khi giá gồm thuế, và `total = goods − discount + tax` khi thuế cộng lên trên.
  - Hai đẳng thức chỉ trùng nhau khi `tax = 0`, mà trường hợp này đã bị loại. Dòng `wrong_item` bị loại ở cả hai vế.
  - Điều kiện: phiếu nhập không có chiết khấu cấp phiếu (OrderAlloc). Input PN hiện chỉ có chiết khấu dòng, nên điều kiện thỏa.
- **Suy sai khi `tax_amount = 0` và store đã đổi `prices_include_tax` sau khi post phiếu.** Trường hợp này xảy ra thực tế khi:
  1. phiếu chỉ gồm dòng net 0 có thuế suất > 0 (đúng ca team-lead hỏi): `TaxBasis.tax` trả 0 cho net ≤ 0, nên phiếu không có thuế;
  2. hoặc các dòng có net đều có thuế suất 0 (hàng không chịu VAT), chỉ dòng net 0 có thuế suất > 0.
  
  Khi đó hàm đọc setting hiện tại của store. Ví dụ: phiếu nhập lúc thuế cộng lên trên, sau đó store chuyển sang giá gồm thuế. DG tăng dòng net 0 từ 2000 lên 3000 (10 cái, VAT 10%) sẽ ra +10000 thay vì +11000, thiếu đúng phần VAT. Đổi chiều ngược lại thì ghi thừa.
  - Mức độ: **Low.** Cần ba điều kiện cùng lúc: dòng nhận miễn phí, phiếu không có thuế, và store đổi setting. Sai lệch bằng phần VAT của mức tăng, và công nợ có thể chỉnh bằng DG tiếp theo.
  - Đề xuất (cần migration nên để backlog): lưu snapshot `prices_include_tax` trên `receipts` lúc post. `PricedDoc.PricesIncludeTax` đã có sẵn ở `app/procurement_ports.go:152`, chỉ chưa được ghi. Có snapshot thì bỏ hẳn suy luận và fallback.
- **Dòng có net > 0 vẫn giữ tỉ lệ `line_amount / net`:** bằng 1 khi giá gồm thuế, `(net + tax) / net` khi thuế cộng lên trên. Một dòng từng có net > 0, bị DG về dưới chiết khấu rồi tăng lại, vẫn dùng tỉ lệ của giá nhận ban đầu. Đúng.

### Kết luận

- N1 sửa đúng và đủ.
- N2 đúng trong mọi phiếu có thuế. Chỉ còn trường hợp suy sai đã nêu ở trên (phiếu không có thuế và store đổi setting), mức Low, nên ghi backlog kèm migration snapshot.
- Không có gì chặn việc đóng Phase 08.
