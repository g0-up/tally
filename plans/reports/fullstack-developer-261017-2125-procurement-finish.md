# Phase 08 Procurement: hoàn tất phần còn lại

Ngày: 2026-10-17 (Asia/Saigon). Agent: fullstack-developer. Trạng thái: DONE_WITH_CONCERNS.

## Đã làm

### 1. Hai nhóm kiểm tra mới trong `procurement.Verify` (`tally ledger verify`)

Thêm 4 check vào `apps/api/internal/procurement/verify.go`:

- **`supplier document receipt`**: một hoá đơn NCC chỉ được gắn với PN đã ghi sổ. Check báo lỗi khi PN đang gắn có `status <> 'posted'`, hoặc khi PN gắn vào là phiếu đảo.
- **`supplier document matched amount`**: `matched_amount` của mỗi liên kết phải bằng tổng PN cộng `payable_delta` của các DG đã ghi sổ và chưa bị đảo. Công thức này giống `refreshMatches`.
- **`supplier document variance`**: `match_status` và `variance_amount` của hoá đơn phải khớp với các liên kết:
  - không có liên kết → `unmatched 0`;
  - tổng `matched` bằng tổng hoá đơn → `matched 0`;
  - còn lại → `variance` = tổng hoá đơn − tổng `matched`.
- **`receipt line unit cost`**: so theo giá trị. `unit_cost × (base_qty − returned_base_qty)` phải bằng:
  - `line_amount + landed_cost_amount + Σ(on_hand_amount + sold_amount)` của DG còn hiệu lực,
  - trừ `Σ(base_qty × unit_cost)` của các dòng XT.

  Dung sai là `0.00005 × base_qty × (1 + số lần landed cost/DG)`. Đây là sai số làm tròn 4 chữ số khi nhập kho và ở mỗi lần dịch giá vốn. XT lấy ra đúng `unit_cost × qty` nên không làm sai số lớn thêm. Khi dòng không còn giữ lại đơn vị nào, check vẫn báo nếu còn sót giá trị lớn hơn dung sai.

**Test âm.** `TestProcurementVerifyReportsTamperedRows` trong `internal/app/procurement_integration_test.go` dùng một store riêng nên không ảnh hưởng subtest "every ledger verifies".

Trước khi làm hỏng dữ liệu, test dựng các tình huống thật và xác nhận Verify sạch:
- PN đã gắn hoá đơn;
- PN 10 cái @10000, trả 4, landed cost 6000, ra giá vốn 11000;
- ca làm tròn: 3 cái, landed cost 10000, trả 2.

Sau đó test làm hỏng từng dòng bằng superuser, giống pattern của inventory. Ở mỗi ca, Verify phải báo **đúng** các dòng lệch mong đợi (đúng số lượng, đúng id). Test khôi phục dữ liệu rồi xác nhận Verify sạch lại. Bốn ca:

| Ca | Làm hỏng | Verify phải báo |
|---|---|---|
| 1 | PN đã gắn bị chuyển sang `reversed` | `supplier document receipt` + `reversed receipt payable` |
| 2 | Lệch `matched_amount` | `matched amount` + `variance` |
| 3 | Lệch `variance` | `variance` |
| 4 | `unit_cost` = 10600 (chia landed cost cho số nhận thay vì số giữ lại) | `receipt line unit cost`, kèm `expected 11000` |

### 2. Registry owner của attachment (`org.Deps.AttachmentOwners`)

- **`org/attachments.go`**:
  - Thêm `AttachmentOwner func(ctx, q, id) (bool, error)` và `AttachmentOwners map[string]AttachmentOwner`.
  - Bỏ regex `owner_type`.
  - `owner_type` không có trong registry → 422 `validation_failed`, `fields.owner_type = invalid`.
  - Trong transaction tenant, trước khi INSERT, kiểm tra owner có tồn tại. Kiểm tra chạy qua RLS, nên owner của tenant khác cũng là "không có" → 422 `fields.owner_id = not_found`.
- **`org/service.go`**: thêm `Deps.AttachmentOwners`. Thiếu registry thì không nhận upload nào.
- **`procurement/repo.go`**: thêm `AttachmentOwners()`, đăng ký `receipt` và `supplier_document` bằng hàm thường, không import org. Thêm hằng `AttachmentOwnerSupplierDocument`, và `queries.go` dùng hằng này.
- **`app/domains.go`**: thêm `Domains.AttachmentOwners()`, gộp:
  - `product` (ảnh sản phẩm, owner type đang dùng);
  - `party` (biên bản cấn trừ công nợ đã ký; team-lead đã duyệt: BT bù trừ phải thu và phải trả của cùng một đối tác, và biên bản có trước BT);
  - hai owner của procurement.

  Hàm kiểm tra `products`/`parties` nằm ở app vì catalog không thuộc phạm vi của tôi.
- **Nối registry**: `internal/cli/serve.go` và các test stack (`app_integration_test`, `debt_cash_integration_test`, `sandbox_reset_integration_test`, `procurement_integration_test`). Procurement stack giờ có object store giả, chỉ dùng để presign, không kết nối.
- **Test**:
  - Unit `org_test.go` (`TestNewAttachmentValidate`) và `app_test.go` (`TestAttachmentOwnersRegistered`).
  - Integration org `TestAttachmentOwnerIsRegisteredAndOfTheTenant` kiểm 4 ca: type lạ, id không tồn tại, owner của tenant khác, và một ca hợp lệ. Test xác nhận không có dòng `attachments` nào được ghi khi bị từ chối.
  - Test MinIO round-trip chuyển sang owner `reason` có thật.
  - HTTP integration "a document photo is uploaded for a receipt or supplier document of the store only":
    - 422 cho `purchase_receipt` → `owner_type invalid`;
    - 422 cho id ngẫu nhiên, PN của store B, và id hoá đơn gửi với type `receipt` → `owner_id not_found`;
    - 201 cho `receipt` và `supplier_document`, và ảnh hiện trong chi tiết PN và chi tiết hoá đơn.
  - HTTP integration "an offset settles both ledgers of the customer against the signed record uploaded for them" (`debt_cash_integration_test.go`), làm theo yêu cầu của team-lead:
    - tạo một khoản phải thu và một khoản phải trả 400000 cho cùng khách;
    - upload biên bản với `owner_type` `party` cho khách đó → 201;
    - reauth, rồi POST /debt-offsets dùng attachment đó → 201, `attachment_id` đúng, hai settlement 400000 (phải thu trước);
    - subtest "both ledgers verify" chạy sau vẫn sạch.

### 2b. BT chỉ nhận biên bản của chính đối tác (team-lead mở rộng phạm vi sang debt)

- **`internal/debt/adjustments.go`**:
  - Thêm hằng `AttachmentOwnerParty = "party"`; `app/domains.go` dùng hằng này khi đăng ký owner.
  - `recordOffset` giờ chỉ nhận attachment có `owner_type = 'party'` và `owner_id = party_id` của BT, ngoài điều kiện cũ (có trong tenant, không `rejected`). Sai owner thì trả lỗi field sẵn có của debt: 422 `attachment_invalid`, `fields.attachment_id = invalid`.
- **`internal/debt/debt_integration_test.go`**: test BT đang chèn attachment `owner_type 'debt_offset'` với owner là chính nó. Đã đổi sang `'party'` với owner là đối tác của BT.
- **HTTP integration (subtest BT ở trên)** thêm hai ca âm, chạy trước ca thành công:
  - biên bản upload cho một NCC khác cùng store → 422 `attachment_id invalid`;
  - ảnh sản phẩm (`owner_type product`) → 422 `attachment_id invalid`;
  - số dòng `obligation_settlements` không đổi sau hai lần bị từ chối.
- **Kiểm chứng test**: tôi tạm bỏ điều kiện owner trong câu SQL. Subtest fail: ảnh sản phẩm được nhận (201, BT-0001). Sau đó tôi khôi phục file từ bản sao lưu.
- **`api/openapi.yaml` /debt-offsets**: `attachment_id` có mô tả "uploaded with owner_type party for party_id". Mô tả 422 nói rõ khi nào trả `attachment_invalid`. Đã chạy lại `npm run gen:api`.

### 3. Nối `posting.Deps.Entitlements`

- **`app/domains.go`**:
  - Thêm option `WithEntitlements(middleware.EntitlementReader)`, mặc định `writesAllowed{}`.
  - Thêm adapter `postingEntitlements`: `CanWrite` false → `apperr.NewReadOnlyTenant()` (403 `subscription_read_only`); lỗi đọc thì wrap và trả về.
- **`app/app.go`**: comment trên `Ports.Entitlements` ghi rõ phải đưa cùng reader đó vào `NewDomains`.
- **Production**: `serve.go` hiện chưa có reader (Ports.Entitlements nil, cho ghi). Vì vậy production chưa đổi hành vi, chỉ sẵn đường nối cho phase 13.
- **Test**:
  - Unit `TestPostingEntitlements` kiểm 4 ca: active, read-only → 403, lỗi đọc được wrap, mặc định cho phép.
  - Integration: procurement stack truyền cùng `statusEntitlements` cho Ports và `WithEntitlements`. Subtest read-only kiểm thêm: POST /receipts với key mới → 403 `subscription_read_only` và số PN không đổi. Replay với key cũ vẫn trả 201.
  - Tôi đã thử bỏ `WithEntitlements`: subtest fail (201, PN tăng lên 2). Vậy test bắt đúng lỗ hổng.

### 4. OpenAPI và web

- **`apps/api/api/openapi.yaml`, `POST /attachments`**:
  - `owner_type` đổi từ pattern sang enum `[product, party, receipt, supplier_document]`;
  - mô tả owner type;
  - 422 ghi rõ `fields.owner_type = invalid` và `fields.owner_id = not_found`.

  Contract test xanh.
- **`apps/web`**: đã chạy `npm run gen:api`. File sinh ra nằm ở `apps/web/src/shared/api/schema.d.ts`, không phải `src/api/schema.d.ts`. File này trước đó **chưa được sinh lại cho các route procurement**, nên diff có thêm toàn bộ `/purchase-orders`, `/receipts`, `/supplier-documents`, `/purchase-returns`, `/purchase-price-adjustments` và `/procurement/late-orders`, ngoài phần attachments. Diff không có route sales.

### 5. `docs/database.md`

Repo có generator: `make db-docs`. Nhưng nó đọc từ DB dev qua `$(COMPOSE) exec postgres`, tức `tally-postgres-1`, nên tôi **không chạy**.

Tôi cũng không cần sửa tay. File này chỉ liệt kê bảng, số cột và loại isolation theo migration tạo bảng. Migration 000037–000039 không tạo bảng, không thêm hay bớt cột: 000037 chỉ DROP NOT NULL và thêm CHECK, 000038 thêm unique index, 000039 đổi CHECK. Vì vậy nội dung sinh lại sẽ y hệt. Trong `docs/` không có dòng nào nhắc `ck_cogs_adjustments__*` hay `ux_supplier_document_receipts__receipt_id`; file ghi rõ tên constraint lấy theo migration.

### 6. Plan và report cũ

- **`phase-08-procurement.md`**:
  - Key insights về attachments: thay câu "Org does not check…" bằng mô tả registry và hai lỗi 422.
  - Key insights về posting: thêm phần gate entitlement trong coordinator, và thêm một insight về các check Verify mới.
  - Thêm 3 todo đã xong.
- **`fullstack-developer-261017-1526-procurement.md`**:
  - Số migration đổi 000040/41/42 → 000037/38/39.
  - Tên constraint đổi `ck_cogs_adjustments__source_document_type` → `ck_cogs_adjustments__source_doc_types`.
  - Ghi chú rằng lỗi lint "000037 is missing" thuộc lúc chưa đánh số lại.

### 7. Sửa theo review (`code-reviewer-261017-2140-procurement-finish-review.md`)

- **H1, 429 flaky.**
  - `app.Options` có thêm field `APIRateLimit app.RateLimit{PerSecond, Burst}`. Giá trị zero vẫn là limit production (20/s, burst 100), nên production không đổi.
  - Mọi test stack HTTP (`app_integration_test.go`, `debt_cash_integration_test.go`, `procurement_integration_test.go`, `sandbox_reset_integration_test.go`) đều truyền `testAPIRateLimit` (1000/s, burst 10000). Stack sales dựng qua các hàm này cũng được hưởng. Limiter riêng của các endpoint credential giữ nguyên.
  - Unit `TestAPIRateLimitOverridesTheDefault` kiểm hai chiều: limit nhỏ chặn đúng, limit lớn không chặn. Test cũ `TestRateLimitRunsBeforeSlugLookup` vẫn phủ giá trị mặc định.
  - `TestProcurementOverHTTP` chạy 5 lần liên tiếp (lặp `-count=1`) đều `ok`, khoảng 9–10s mỗi lần, không có `rate_limited`.
- **M1, DG kéo unit_cost xuống dưới 0.**
  - `price_adjust.go`: hàm mới `carriedCost`. Phần chênh lệch mà các đơn vị còn giữ chịu được là tối đa `−unit_cost × kept`. Phần đó đi vào giá vốn của dòng (`on_hand + sold`), unit_cost dừng ở 0. Phần âm còn lại ghi vào `cogs_adjustments` source `purchase_price_adjustment`, cùng quy ước với residue của XT.
  - `costShares` (`cost_split.go`) nhận thêm `residue` theo SKU và gộp vào cùng map COGS, nên mỗi chứng từ vẫn chỉ có một dòng cogs mỗi SKU. Landed cost truyền `nil`.
  - Công nợ vẫn ghi đủ chênh lệch, vì credit của NCC là thật.
  - Số liệu test ở đây đã được thay theo quyết định giá trị dòng ở mục 8.
  - Kiểm chứng test: tôi tạm bỏ nhánh sàn. DG bị từ chối 409 `stock_value_negative` và test fail. Sau đó tôi khôi phục file.
- **M2.** `postingEntitlements.CanPost` giờ type-assert reader sang `posting.EntitlementGuard` trước và gọi `CanPost(ctx, q, tenantID)` trong tx posting. Nếu không assert được thì dùng `CanWrite` như cũ. `TestPostingEntitlements` có thêm fake implement cả hai interface. `CanWrite` của fake trả lỗi; test khẳng định guard được gọi với đúng `q` và tenant, và lỗi của guard được trả nguyên.
- **M3, BT nhận attachment pending.**
  - `:complete` chuyển `pending` sang `clean` hoặc `rejected` (`org.CompleteAttachment`, `checkUpload`).
  - `recordOffset` giờ đòi `owner_type = 'party'`, `owner_id = party_id` và `scan_status = 'clean'`. Ngoài các điều kiện đó thì trả 422 `attachment_invalid`, `fields.attachment_id = invalid`, message "Upload the signed offset record for the party before posting".
  - HTTP test:
    - helper mới `uploadAttachment` khai báo file thật (sha256 thật), PUT lên presigned URL của MinIO dev, gọi `:complete` và khẳng định `scan_status` là `clean`;
    - `t.Cleanup` xoá object;
    - ca thành công và hai ca âm cũ dùng helper này;
    - ca âm mới "a record never uploaded" (chỉ khai báo, không upload) → 422 `attachment_id invalid`, không thêm settlement.
  - Kiểm chứng test: đổi điều kiện về `<> 'rejected'` thì ca pending nhận 201 và test fail. Sau đó tôi khôi phục file.
  - `debt_integration_test.go` vẫn chèn `'clean'`.
  - OpenAPI `/debt-offsets`: mô tả `attachment_id` và 422 nói rõ attachment phải đã complete. Đã chạy lại `gen:api`.
- **M4.** Mọi check trong `procurement.Verify` giờ gom bảng con một lần bằng CTE (`returned`, `landed`, `priced`, `owed` và CTE riêng của từng check), rồi `LEFT JOIN`. Không còn subquery tương quan theo từng dòng. Ngữ nghĩa giữ nguyên:
  - `moves` vẫn đếm cả dòng DG đã đảo;
  - lọc `posted`/`reversal_of` như cũ.

  Không tạo migration. Test tamper và "every ledger verifies" đều xanh.
- **Low đã sửa:**
  - L1: check `supplier document variance` in cả stored lẫn expected qua `trim_scale(...)::text`, nên hai vế cùng định dạng (ví dụ `matched 0`). `trim_scale` là hàm có sẵn của Postgres ≥ 13, không phải function tự tạo.
  - L2: test tamper thêm hai ca:
    - liên kết hoá đơn trỏ vào phiếu đảo → đúng một mismatch `supplier document receipt`;
    - sửa `on_hand_amount` của một dòng DG đã post → đúng một mismatch `receipt line unit cost`.
  - L3: `CreateAttachment` giờ presign trong tx, sau khi kiểm owner. Test org bọc store bằng `presignCounter`: ba upload bị từ chối không ký URL nào, upload hợp lệ ký đúng một.
  - L6: `TestOpenAPIAttachmentOwnerTypes` (unit, `openapi_contract_test.go`) so enum `owner_type` của POST /attachments với key của `Domains{}.AttachmentOwners()`.
- **Low chưa sửa, kèm lý do:**
  - L4 (inventory posting mất replay khi read-only): lỗi có từ trước. Sửa nghĩa là đổi nhóm route và hành vi của domain inventory, nằm ngoài phạm vi procurement. Nên đưa vào backlog phase 13 hoặc inventory.
  - L5 (RBAC cho POST /attachments): lỗi có từ trước. Cần quyết định sản phẩm về quyền cho từng owner type, ví dụ procurement write cho `receipt`/`supplier_document`, nên không tự chọn.
  - L7: đã sửa cho các route posting của debt và procurement theo yêu cầu bổ sung của team-lead (xem mục 8).

### 8. Bổ sung sau review và quyết định của team-lead

- **Ca BT thành công dùng attachment đã complete.** Việc này đã xong từ mục 7 (M3). Subtest BT trong `debt_cash_integration_test.go` gọi `uploadAttachment`: khai báo file thật, PUT lên MinIO của stack, gọi `:complete`, khẳng định `clean`, rồi mới post BT. Ca "a record never uploaded" khẳng định attachment pending bị từ chối.
- **Giá trị dòng sau DG = max(0, giá mới × qty − chiết khấu dòng).**
  - `price_adjust.go` có hàm mới `lineNet(l, p)`, trả `p × qty − discount` và không xuống dưới 0. `adjustmentDelta` giờ là phần kept của `lineNet(new) − lineNet(old)`, nhân tỉ lệ `priceToAmount` (cũng tính qua `lineNet`). Nhờ vậy credit của một dòng không bao giờ vượt giá trị ròng hiện tại của dòng đó.
  - `carriedCost` giữ làm chốt chặn: với quy tắc mới, phần âm dư chỉ còn có thể đến từ làm tròn và vẫn đi vào `cogs_adjustments`.
  - Credit vượt phần PN còn nợ (vì đã trả một phần) vẫn đi luồng credit note sẵn có. Không chặn.
  - Unit `TestAdjustmentDeltaStopsAtTheLineValue` dùng 10 cái giá 10000, chiết khấu 20000, trả 4:
    - DG về 0 → −48000;
    - DG về 2000 (chiết khấu ăn hết dòng) → −48000;
    - DG về 5000 → −30000;
    - tăng từ 1000 lên 3000 → +6000;
    - tăng từ 0 lên 1500 (vẫn dưới chiết khấu) → 0;
    - DG về 0 thì toàn bộ đi vào giá vốn, cost về đúng 0, residue 0.
  - `TestCarriedCostStopsAtZero` thử trực tiếp chốt chặn: −48001 → carried −48000, residue −1, cost 0; đúng −48000 → residue 0.
  - Integration (subtest "a price drop to zero on a discounted line credits at most what the line is worth"):
    - 10 cái giá 10000, chiết khấu 20000, bán 4, DG về 0;
    - payable_delta −80000 (không phải −100000), on_hand −48000, sold −32000, unit_cost 0, tồn 6 cái giá trị 0;
    - đúng một dòng `cogs_adjustments` −32000;
    - `procurement.Verify` sạch.
  - `phase-08-procurement.md` (Key insights, dòng 31): thay câu cũ bằng quy tắc mới.
- **403 `subscription_read_only` trong OpenAPI.**
  - Khi tenant read-only, middleware entitlement và posting coordinator trả 403 `subscription_read_only`.
  - Mô tả 403 của 12 route posting đã có thêm mã này. Các route vẫn dùng component chung `#/components/responses/Error`; spec chỉ có component lỗi này, nên tôi không tạo component mới.
    - debt: `/payments`, `/payments/{id}:confirm`, `/payments/{id}:reverse`, `/allocations`, `/debt-adjustments`, `/debt-offsets`, `/refunds`;
    - procurement: `/receipts`, `/receipts/{id}:reverse`, `/receipts/{id}/landed-costs`, `/purchase-returns`, `/purchase-price-adjustments`.
  - Theo yêu cầu tiếp theo của team-lead, đã thêm 4 route posting của inventory: `/stock-adjustments`, `/stock-adjustments/{id}:reverse`, `/stock-counts/{id}:post`, `/cost-recalculations/{id}:confirm`. Tổng cộng 16 route.
  - `TestOpenAPIErrorResponses` đòi mọi operation dùng `#/components/parameters/IdempotencyKey` liệt kê `subscription_read_only` trong 403, không lọc theo tag, nên route posting của sales sau này tự bị kiểm.
    - Kiểm chứng: bỏ mã khỏi `/refunds`, rồi khỏi `/stock-counts/{id}:post`, thì test đều fail; sau đó tôi khôi phục spec.
    - `POST /stock-counts` (bắt đầu kiểm kê) khai báo `Idempotency-Key` inline và không bắt buộc. Route này nằm trên nhóm tenant thường chứ không qua posting coordinator, nên guard không bắt nó. Bản thân middleware vẫn có thể trả `subscription_read_only` cho route này như mọi request ghi.
  - Đã chạy lại `gen:api`.
- **CI MinIO.** Không cần sửa `.github/workflows/ci.yml`. Job `api` đã có:
  - bước "Start MinIO and Mailpit" chạy `docker compose -f infra/docker-compose.yml up -d --wait mailpit minio`;
  - service `minio-init` tạo bucket `tally-dev`.

  Compose publish `127.0.0.1:9000` với `tally-dev` / `tally-dev-secret`, đúng giá trị test org và debt dùng (region `us-east-1` cố định trong test). Bước "Integration tests" chạy `go test -race -count=1 -tags integration ./...`. Không test nào gọi `t.Skip`.

### 9. Sửa N1 và N2 theo re-review

- **N1: DG giảm giá bị 409 khi giá trị tồn của SKU thấp hơn phần giá vốn của lot.**
  - Đã tái hiện bằng subtest "a price drop on the dear receipt of a SKU whose cheap lot was sold first books what stock cannot carry to COGS":
    1. Nhập PN rẻ trước (10 cái giá 2000), PN đắt sau (10 cái giá 8000).
    2. Bán 14 cái: hết lot rẻ, thêm 4 cái của lot đắt, theo giá trung bình 5000. Tồn còn 6 cái, giá trị 30000.
    3. DG giảm PN đắt về 0. Trước khi sửa, API trả `409 stock_value_negative`; chính subtest này đã fail như vậy.
  - Sửa trong `costShares` (`cost_split.go`). Phần held của mỗi dòng chỉ nhận đúng phần mà `stockShare` cho phép, tính theo giá trị tồn của SKU đọc bằng `readSkuStock`. Phần không gánh được chuyển sang `sold_amount`, tức vào COGS, như XT đã làm.
  - Các dòng được duyệt theo đúng thứ tự ledger ghi, và giá trị tồn tạm được cộng dồn qua từng dòng. Vì vậy kiểm tra từng dòng của `CostAdjust` không thể fail.
  - `on_hand + sold = carried` vẫn giữ, nên Verify và `returnCredit` không đổi.
  - Kết quả sau khi sửa:
    - 201, payable_delta −80000;
    - on_hand −30000, sold −50000;
    - unit_cost 0;
    - tồn 6 cái giá trị 0;
    - một dòng cogs −50000;
    - Verify sạch.
  - Landed cost cũng đi qua `costShares`. Giá trị luôn dương, nên chỉ bị đổi khi SKU không còn tồn: khi đó phần này vào COGS thay vì 409 `no_stock_to_adjust`.
- **N2: dòng nhập có net 0, DG tăng giá phải tính thuế theo thuế suất của dòng.**
  - `priceToAmount(l, taxOnTop)`:
    - dòng có net > 0 giữ tỉ lệ `line_amount / net` như cũ;
    - dòng net 0 dùng `1 + tax_rate_snap / 100` khi phiếu cộng thuế lên trên, và 1 khi giá đã gồm thuế.
  - Receipt không lưu `prices_include_tax`, và tôi không được thêm migration. `taxAddedOnTop` vì vậy suy ra basis từ tổng của phiếu, vốn không đổi sau khi post: nếu phiếu có thuế, cộng thuế lên trên khi và chỉ khi `total = goods − discount + tax`. Nếu phiếu không có thuế thì dùng `tenant_settings.prices_include_tax` hiện tại.
  - Unit test:
    - `TestPriceToAmountCarriesTheTax` (dòng có net, và dòng net 0 với cả hai basis);
    - `TestAdjustmentDeltaTaxesARiseOnAFreeLine`: 10 cái, 2000 → 3000, thuế 10%, ra 11000 khi thuế cộng lên trên và 10000 khi giá gồm thuế.
  - Subtest HTTP "a price rise on a line received at nothing carries the line's tax" (store riêng, VAT 10%):
    - phiếu chỉ có dòng net 0, store cộng thuế lên trên: DG +11000;
    - phiếu có thêm một dòng có thuế (total 11000), sau đó store chuyển sang giá gồm thuế: DG vẫn +11000, vì tổng của phiếu quyết định;
    - phiếu mới sau khi chuyển: +10000;
    - Verify sạch.
  - Kiểm chứng: bỏ nhánh thuế thì subtest fail ở hai ca 11000 (ra 10000). Sau đó tôi khôi phục file.
- **N3:** team-lead chấp nhận, không làm gì.
- Không đổi OpenAPI, nên không chạy lại `gen:api`. Không có migration mới.

## File đã sửa

`apps/api`:
- `internal/procurement/verify.go`
- `internal/procurement/repo.go`
- `internal/procurement/queries.go`
- `internal/procurement/price_adjust.go`
- `internal/procurement/price_adjust_test.go`
- `internal/procurement/cost_split.go`
- `internal/procurement/landed_cost.go`
- `internal/procurement/model.go`
- `internal/org/attachments.go`
- `internal/org/service.go`
- `internal/org/org_test.go`
- `internal/org/org_integration_test.go`
- `internal/app/domains.go`
- `internal/app/app.go`
- `internal/app/app_test.go`
- `internal/app/openapi_contract_test.go`
- `internal/app/procurement_integration_test.go`
- `internal/app/app_integration_test.go`
- `internal/app/debt_cash_integration_test.go`
- `internal/app/sandbox_reset_integration_test.go`
- `internal/cli/serve.go`
- `internal/debt/adjustments.go`
- `internal/debt/debt_integration_test.go`
- `api/openapi.yaml`

`apps/web`:
- `src/shared/api/schema.d.ts` (file sinh ra)

Plan và report:
- `plans/261016-0854-tally-full-product-implementation/phase-08-procurement.md`
- `plans/reports/fullstack-developer-261017-1526-procurement.md`

Không sửa `internal/sales/**`, `sales_ports.go`, migration hay catalog. Trong debt chỉ sửa phần kiểm attachment của BT (owner và trạng thái clean), theo chỉ đạo của team-lead. Không có DB trigger, function, view hay DO block mới.

## Lệnh kiểm tra (đã chạy, kết quả thật)

Trong `apps/api`:

| Lệnh | Kết quả |
|---|---|
| `gofmt -l internal` | không in gì |
| `go vet ./... && go vet -tags integration ./...` | sạch |
| `golangci-lint run --build-tags integration ./...` | `0 issues.` |
| `go test -race -count=1 ./...` | tất cả `ok` |
| `go test -tags integration -count=1 ./internal/app/... ./migrations/... ./internal/org/... ./internal/procurement/... ./internal/cli/...` | app ok (20.8s), migrations ok, org ok, procurement ok, cli ok |

Sau lần sửa cuối (đổi nhãn stored của check `supplier document receipt`), tôi chạy lại:
- `-run TestProcurement` của app: ok;
- golangci-lint trên procurement và app: 0 issues.

Sau khi thêm case BT qua HTTP, tôi chạy lại:
- `go vet ./... && go vet -tags integration ./...`: sạch;
- `gofmt -l internal`: không in gì;
- `golangci-lint run --build-tags integration ./internal/app/...`: `0 issues.`;
- `go test -tags integration -count=1 ./internal/app/...`: ok (20.8s). Subtest BT mới PASS (đã xem bằng `-v`).

Sau khi sửa kiểm owner trong debt (lần chạy cuối):

| Lệnh | Kết quả |
|---|---|
| `gofmt -l internal` | không in gì |
| `go vet ./... && go vet -tags integration ./...` | sạch |
| `golangci-lint run --build-tags integration ./...` | `0 issues.` |
| `go test -race -count=1 ./...` | exit 0, 33 package `ok` |
| `go test -tags integration -count=1 ./internal/app/... ./internal/debt/...` | app ok (21.3s), debt ok (7.0s) |
| `npm run gen:api` rồi `npx tsc -b` | exit 0 |
| `npx vitest run` | 26 file, 234 test pass |
| `npx eslint .` | exit 0 |

Sau khi sửa theo review (lần chạy cuối):

| Lệnh | Kết quả |
|---|---|
| `gofmt -l internal` | không in gì |
| `go vet ./... && go vet -tags integration ./...` | sạch |
| `golangci-lint run --build-tags integration ./...` | `0 issues.` |
| `go test -race -count=1 ./...` | tất cả `ok` |
| `go test -tags integration -count=1 ./internal/app/... ./internal/debt/... ./internal/org/... ./internal/procurement/... ./migrations/... ./internal/cli/...` | app ok (24.9s), debt ok, org ok, procurement ok, migrations ok, cli ok |
| `TestProcurementOverHTTP` 5 lần liên tiếp (`-count=1` trong vòng lặp) | 5/5 `ok` (9.0–9.9s) |
| `npm run gen:api`, `npx tsc -b`, `npx vitest run`, `npx eslint .` (apps/web) | exit 0; 26 file, 234 test pass |

Sau khi thêm `TestOpenAPIAttachmentOwnerTypes`, tôi chạy lại gofmt, vet, golangci-lint và `go test -race` của `internal/app`: sạch và ok.

Sau mục 8 (lần chạy cuối):

| Lệnh | Kết quả |
|---|---|
| `gofmt -l internal` | không in gì |
| `go vet ./... && go vet -tags integration ./...` | sạch |
| `golangci-lint run --build-tags integration ./...` | `0 issues.` |
| `go test -race -count=1 ./...` | tất cả `ok` |
| `go test -tags integration -count=1 ./internal/app/... ./internal/debt/... ./internal/org/... ./internal/procurement/... ./migrations/... ./internal/cli/...` | app ok (22.8s), debt ok (7.3s), org ok (9.6s), procurement ok, migrations ok (13.1s), cli ok (7.7s) |
| `npm run gen:api`, `npx tsc -b`, `npx vitest run`, `npx eslint .` (apps/web) | exit 0; 26 file, 234 test pass |
| `docker ps --filter label=org.testcontainers=true` (sau 15s) | rỗng |

Sau mục 9 (lần chạy cuối):
- gofmt: không in gì.
- vet (cả `-tags integration`): sạch.
- golangci-lint: `0 issues.`
- `go test -race -count=1 ./...`: ok.
- Integration: app ok (22.2s); debt, org, procurement, migrations, cli đều ok.
- testcontainers: rỗng.
- Web không đổi.

Sau khi mở rộng sang inventory: cùng bộ lệnh trên đều xanh. Kết quả integration: app ok (19.9s), debt, org, procurement, migrations và cli ok. Web: tsc ok, vitest 234 test pass, eslint ok. Container của testcontainers đã tự dọn sau khoảng 35s.

Trong `apps/web`:

| Lệnh | Kết quả |
|---|---|
| `npx tsc -b` | exit 0 |
| `npx vitest run` | 26 file, 234 test pass |
| `npx eslint .` | exit 0 |

**Process nền:** tôi không khởi động process nền nào. Container testcontainers (postgres và ryuk) do test tự dọn; sau lần chạy cuối `docker ps --filter label=org.testcontainers=true` rỗng. Test org MinIO round-trip và giờ cả subtest BT của `TestDebtAndCashOverHTTP` dùng MinIO dev (`tally-minio-1`), object được xoá trong `t.Cleanup`; không đụng `tally-postgres-1`.

## Rủi ro

- **`internal/app/domains.go` có thể bị sửa song song.** Phase sales có thể cũng thêm Sales vào `Domains`/`NewDomains`. Thay đổi của tôi nằm ở `domainOptions`, `WithEntitlements`, khối `posting.Deps`, `postingEntitlements` và `AttachmentOwners()`. Khi gộp cần đối chiếu.
- **Enum `owner_type` trong OpenAPI** phải cập nhật mỗi khi một domain đăng ký thêm owner type, ví dụ sales hay support_ticket sau này. `TestAttachmentOwnersRegistered` khoá đúng 4 type để nhắc việc này.
- **Dữ liệu BT cũ**: nếu DB nào đã có BT gắn attachment `owner_type` khác `party`, chúng vẫn giữ nguyên, vì kiểm tra mới chỉ áp dụng khi ghi BT mới. Trong repo, API chỉ nhận các owner type đã đăng ký, và web chưa có màn hình nào upload attachment, nên rủi ro chỉ nằm ở dữ liệu tạo trước khi có registry.
- **`TestDebtAndCashOverHTTP` giờ cần MinIO dev** (`make db-up`) để upload và complete biên bản thật, giống test round-trip của org. Nếu MinIO không chạy, test fail với thông báo rõ. CI đã dựng MinIO (mục 8).
- **Route ghi không qua posting** (ví dụ tạo PO, `POST /stock-counts`) vẫn có thể nhận 403 `subscription_read_only` từ middleware entitlement, nhưng OpenAPI chưa ghi mã này và guard không kiểm các route đó.

## Câu hỏi mở

Không có.

Status: DONE
