# Báo cáo triển khai — Nhập hàng (Procurement)

- Thời điểm: 2026-10-17 (Asia/Saigon). Bản đầu lúc 18:10; bản này cập nhật sau review `plans/reports/code-reviewer-261017-1600-procurement-review.md`. Không lấy được giờ chính xác vì shell hỏng.
- Kế hoạch: `plans/261016-0854-tally-full-product-implementation/phase-08-procurement.md`
- Trạng thái: cả 10 todo đã xong, frontmatter là `status: done`. Các sửa theo review đã viết xong nhưng **chưa kiểm chứng** (xem mục cuối). Chưa commit.

## Kết quả

Đã làm xong toàn bộ nghiệp vụ nhập hàng:
- đơn đặt hàng (PO) và phiếu nhập (PN);
- công nợ phải trả, gồm trả ngay, tạm ứng và lịch trả;
- chi phí nhập (landed cost);
- hoá đơn nhà cung cấp và đối chiếu;
- trả hàng nhà cung cấp (XT) kèm phiếu giảm nợ;
- điều chỉnh giá (DG);
- đóng thiếu, đơn trễ hạn và đảo phiếu nhập.

Mỗi todo có unit test trong `internal/procurement`, và có kịch bản HTTP đầu-cuối trong `internal/app/procurement_integration_test.go`.

Trước review, mọi tiêu chí thành công của phase đã được kiểm chứng bằng integration test (11 subtest PASS):

| Tiêu chí | Kết quả kiểm chứng |
|---|---|
| PO 100 thùng (×24), nhận 60 rồi 40 | ordered 2400, received 2400, pending 0, trạng thái `completed`. Sau lần nhận 60, nhận thêm 41 thì trả 409 `receipt_exceeds_order`; nhận khi đã đủ thì trả 409 `order_not_receivable` |
| Gửi lại cùng `Idempotency-Key` | 201, `Idempotent-Replay: true`, cùng id, số dòng `stock_moves` không đổi |
| Landed cost 100000 trên 3 dòng 1:1:1 | Chia 33334/33333/33333; avg_cost lên 53333.4 và 53333.3; người vận chuyển được ghi nợ 100000 |
| Trả hàng dòng màu tím | XT xuất đúng lô tím (lô tím còn 6, lô đỏ giữ 10), công nợ còn mở 800000. Trả 7 thì 409 `return_exceeds_received` |
| Hoá đơn NCC gắn 2 phiếu nhập | Ban đầu `variance`; gắn thêm phiếu thứ hai (If-Match) thì `matched`. Số obligations và công nợ của từng PN không đổi |
| Ledger verify | `procurement.Verify`, `inventory.Verify`, `debt.Verify` và `cash.Verify` sạch cho cả hai cửa hàng |

## Sửa theo review (chưa kiểm chứng)

| Mục | Đã sửa | Test |
|---|---|---|
| M1 | Đảo PN của PO đã đóng thiếu không còn lỗi 409. Trạng thái giữ nguyên thì không coi là chuyển trạng thái (`PurchaseOrderStatus.Follow`); PO vẫn `closed_short` và số đã nhận giảm | Integration: nhận 4/10 → đóng thiếu → reauth → đảo PN → 201; PO vẫn `closed_short`, `received_base_qty` "0" |
| M2 | Khi gắn PN, hoá đơn NCC khoá các PN `FOR SHARE` theo thứ tự id, trước khi khoá hoá đơn. Lệnh đảo PN tìm liên kết hoá đơn trong cùng khoá của PN, nên đảo PN và gắn PN được thực hiện tuần tự | Chặn bằng thứ tự khoá; integration kiểm `receipt_has_followups` khi PN đã gắn hoá đơn |
| M3 | Mỗi PN chỉ gắn vào tối đa một hoá đơn NCC. Đã thêm unique index `ux_supplier_document_receipts__receipt_id` (migration 000038). `linkReceipts` kiểm trước và đổi lỗi unique violation thành 409 `receipt_already_matched`. Quyết định này đã ghi vào Key insights của phase-08 | Integration: tạo hoá đơn thứ hai cho PN đã gắn → 409, không lưu hoá đơn; gắn PN vào một hoá đơn khác có If-Match → 409; PN vẫn chỉ có 1 liên kết |
| M4 | Các route posting của procurement chuyển sang `TenantPostingAPI()`: `POST /receipts`, landed cost, `:reverse`, XT, DG | Integration: tenant C bị chuyển `read_only` → replay PN vẫn 201 kèm header replay, `POST /supplier-documents` → 403 `subscription_read_only`, GET → 200 |
| M5 | Landed cost và DG chia theo số lượng còn giữ (`perKeptUnit`), không chia theo số đã nhận | Integration: nhận 10 @10000, trả 4, landed cost 6000 → unit_cost 11000, tồn 6 @11000. Trả nốt 6 → line unit_cost 11000, credit 60000, tồn 0 với giá trị 0, không có dòng `cogs_adjustments` nào |
| L1 | XT xuất ở giá vốn khác giá bình quân thì phần chênh ghi vào `cogs_adjustments` với source `purchase_return` (migration 000039 mở rộng CHECK). Không còn kẹp phần chênh | Như M5 (chênh 0 thì không ghi dòng nào) |
| L2 | Nhận theo PO: nếu SKU của PO không nằm trong tập SKU đã khoá thì trả 409 `purchase_order_changed` | Chưa có test riêng: khó dựng race qua HTTP |
| L3 | Quy ước đảo chứng từ đã ghi vào Key insights của phase-12 | Không áp dụng |
| L4 | Chiết khấu PO phân bổ luỹ kế theo các đợt nhận, nên tổng đúng bằng chiết khấu đặt | Unit test: `TestProratedDiscountOfTheBatchesAddsUpToTheOrderedDiscount`, `TestResolveReceiptLinesSplitsTheDiscountOverLinesOfTheSameOrderedLine` |
| L7 | Các subtest giờ độc lập: mỗi subtest tự tạo SKU, PO, PN và NCC riêng. Không còn hardcode MinIO; org chạy không có object store vì không test nào upload | Toàn bộ file integration đã viết lại |

Các test còn thiếu, nay đã bổ sung (chưa chạy):
- **XT refund:** công nợ PN vẫn mở 100000, có credit note.
- **XT replacement:** công nợ PN giảm còn 80000; PN thay thế 2 @10000 đưa tổng nợ NCC về 100000; tồn 10.
- **DG tăng giá:** `payable_delta` 10000, có payable mới, không có credit note; phần tồn 6000, phần đã bán 4000; avg 11000. `post_purchase_discount` tăng giá thì trả 422 `not_a_discount`.
- **Kỳ đã khoá:** khoá đến 2026-02-28 rồi nhập PN ngày 2026-02-20 → 423 `period_locked`, không ghi gì; nhập ngày 2026-03-01 → 201.
- **Replay XT, DG và landed cost:** mỗi loại chỉ ghi một lần; giá trị tồn đúng 84000.
- **Id của tenant khác:** B dùng sku_id, color_id, reason_id, payment_id (tạm ứng) và PO của A → 4xx. Số PN và XT của B không đổi, PN của A không bị đụng. Danh sách của B không chứa id nào của A.

### Giữ nguyên (L5, L6), có lý do

- **Reauth chỉ ở `:reverse`, đúng spec.** Đóng thiếu không ghi sổ gì. DG làm giảm phải trả thì có audit và đảo được.
- **`SetRetailPrice` từ PO nháp là đúng theo PRC-08.** Giá bán lẻ chỉ áp dụng cho giao dịch mới, nên xoá PO nháp không cần thu hồi giá.

### Wording đã cập nhật

- **`return_posting.go`:** comment về settlement `replacement` đã sửa.
- **OpenAPI, `PurchaseReturnRequest.settlement`:** mỗi settlement đều tạo credit note. `credit_payable` trừ vào công nợ PN. `refund` giữ nguyên công nợ, credit note là khoản NCC phải hoàn. `replacement` trừ vào công nợ ngay; hàng thay thế về trên một PN mới với giá gốc, PN đó tạo công nợ mới, nên tổng nợ NCC trở lại như cũ. UI phase 15 sẽ hướng dẫn người dùng tạo PN thay thế.
- **OpenAPI, mô tả `postPurchaseReturn`:** đã sửa câu cũ sai ("replacement ... with no debt").
- **OpenAPI, `matched_amount`:** là tổng PN cộng các DG. Không trừ XT, vì NCC xuất hoá đơn điều chỉnh riêng, đối chiếu với credit note của XT.
- **OpenAPI, các mã 409:**
  - thêm `receipt_already_matched` cho `POST /supplier-documents` và `POST /supplier-documents/{id}/receipts`;
  - thêm `purchase_order_changed` cho `POST /receipts`;
  - summary của endpoint gắn PN cũ ghi sai là "Replace", nay sửa thành "Match more receipts".

## Quyết định về tệp đính kèm

Dùng luồng chung `POST /attachments` của org, với owner_type `receipt` hoặc `supplier_document`. Procurement chỉ liệt kê tệp trong chi tiết PN và chi tiết hoá đơn. Có một lỗ hổng thuộc org: org không kiểm tra owner_id có tồn tại trong tenant hay không. Việc này cần chủ sở hữu org quyết định. Quyết định đã ghi trong Key insights của phase-08.

## File và migration

- `apps/api/internal/procurement/`: mã nguồn và test của gói, do tôi sở hữu. Lần này sửa:
  - `cost_split.go`, `landed_cost.go`, `price_adjust.go`;
  - `return_posting.go`, `status.go`, `matching.go`;
  - `errors.go`, `http.go`;
  - `receipt_posting.go`, `receipt_posting_test.go`;
  - các test của matching.
- `apps/api/internal/app/procurement_ports.go`: adapter nối inventory, debt và pricing vào các port của procurement.
- `apps/api/internal/app/procurement_integration_test.go`: viết lại thành các subtest độc lập. Test này dùng một `EntitlementReader` riêng, đọc `tally.tenants.status` qua kết nối superuser.
- Sửa nhỏ ở các file dùng chung:
  - `internal/app/routes.go`: procurement nhận thêm `Posting: a.TenantPostingAPI()`;
  - `api/openapi.yaml`: các mô tả kể trên;
  - từ bản đầu: `domains.go`, `app.go`, `openapi_contract_test.go`, `internal/cli/serve.go`, `internal/cli/ledger.go`.
- Migration, mỗi cái có up và down, không dùng trigger, function, view hay CASCADE:
  - `000037_procurement_receipt_line_lot`: `receipt_lines.lot_id` được phép NULL, kèm CHECK `(lot_id IS NULL) = (condition = 'wrong_item')`;
  - `000038_procurement_receipt_single_document`: unique `(tenant_id, receipt_id)` trên `supplier_document_receipts`;
  - `000039_procurement_return_cogs_adjustment`: đổi CHECK của `cogs_adjustments.source_doc_type`, thêm `purchase_return`. Constraint mới có tên mới (`ck_cogs_adjustments__source_doc_types`) để không trùng tên với lint.
- `docs/database.md` có thể cần sinh lại cho 000038 và 000039.
- `apps/api/migrations/isolation_integration_test.go`: lần này không sửa. Bản đầu có thêm một override `receipt_lines.lot_id`, team-lead sẽ đối chiếu khi gộp.
- Plan:
  - Key insights của phase-08: thêm M3, M5/L1, đối chiếu và XT, replacement, chiết khấu luỹ kế, tệp đính kèm, route posting/reauth;
  - Key insights của phase-12: thêm quy ước đảo chứng từ.

## Lệnh kiểm tra

**Bản đầu (đã chạy, trong `apps/api`):**
- gofmt, go vet và golangci-lint trên procurement, app và cli: sạch.
- `go test -race ./internal/procurement/...`: ok.
- `go test -tags integration` cho procurement, app và cli: ok.
- Riêng `TestMigrationsLint` báo "version 000037 is missing". Lỗi này đã biết trước, xảy ra khi migration còn mang số 000040–000042; sau khi đánh số lại thành 000037–000039 thì hết.

**Bản sau review: chưa kiểm chứng.** Shell lỗi E2BIG ở mọi lệnh, nên chưa build, chưa vet, chưa lint, chưa chạy test nào. Đề nghị chạy gate sau khi khởi động lại:

```
cd apps/api
gofmt -l internal/procurement internal/app
go vet ./internal/procurement/... ./internal/app/... && go vet -tags integration ./internal/app/...
golangci-lint run --build-tags integration ./internal/procurement/... ./internal/app/...
go test -race -count=1 ./internal/procurement/...
go test -tags integration -count=1 -run 'TestProcurementOverHTTP|TestMigrations' ./internal/app/... ./migrations/...
```

Những chỗ có thể vỡ khi chạy:
- Test kỳ khoá giả định `POST /period-locks` không cần reauth ngay sau khi đăng nhập.
- Test DG tăng giá giả định payable mới là một dòng `obligations`.
- Test replacement đọc `open` của NCC qua `/debts/parties?direction=payable`, mặc định 50 dòng; tenant A có ít NCC hơn mức này.
- Việc chuyển tenant C sang `read_only` giả định session và tenant-host resolver vẫn chấp nhận tenant ở trạng thái đó.

## Sai lệch và rủi ro

- **Tồn dùng chung một lô:** khi DG hoặc landed cost chia phần tồn và phần đã bán, phần tồn tính theo lô của dòng PN. Nếu lô đó đã nhận thêm từ phiếu khác (SKU không theo dõi lô, lot_no rỗng), con số chỉ là xấp xỉ.
- **unit_cost gồm thuế:** giá vốn nhập kho tính trên thành tiền dòng, đã có thuế, đúng như cách công nợ ghi nhận.
- **Phiếu đảo chỉ có header:** dòng hàng được đảo qua `inventory.Reverse`, không chép dòng PN.
- **DG làm tăng version hoá đơn:** DG cập nhật lại các hoá đơn NCC đã gắn, nên version tăng và client giữ ETag cũ sẽ nhận 412.
- **Entitlement của posting chưa nối (vấn đề chung, không riêng procurement):** `app.NewDomains` không truyền `posting.Deps.Entitlements`, nên `Coordinator` bỏ qua `CanPost`. Route posting đã ở `TenantPostingAPI()`, nên khi có cổng subscription thật, một tenant read-only vẫn tạo được posting mới qua mọi module dùng route này (debt, procurement). Hiện production chưa có cổng này (`Ports.Entitlements` nil thì cho ghi), nên chưa gây hại. Cần nối khi làm phase 13.

Status: DONE_WITH_CONCERNS
Summary: Đã viết xong các sửa theo review: M1–M5, L1–L4, L7, test còn thiếu, wording OpenAPI và Key insights của phase-08/12. L5/L6 giữ nguyên, có lý do. Tất cả chưa kiểm chứng vì shell hỏng.
Concerns/Blockers:
- Chưa build và chưa chạy test nào sau review; cần chạy gate ở trên.
- Có 2 migration mới (000038, 000039); `docs/database.md` có thể cần sinh lại.
- `posting.Deps.Entitlements` chưa được nối trong `NewDomains`, cần làm ở phase 13.
- Org không kiểm tra owner_id của attachment tồn tại; cần chủ sở hữu org quyết định.
