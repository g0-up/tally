# Báo cáo triển khai — Phase 07: Sổ kho (Inventory Ledger)

- Plan: `plans/261016-0854-tally-full-product-implementation/plan.md`
- Phase: `phase-07-inventory-ledger.md` (12/12 todo đã tick, `status: done`)
- Trạng thái: **DONE_WITH_CONCERNS**. Toàn bộ phạm vi đã có test chạy thật. Các điểm lệch so với spec ghi ở mục "Rủi ro và giới hạn".

## Đã làm

Tôi đọc lại code `internal/inventory` có sẵn và đối chiếu với phase file và design. Phần đúng giữ nguyên. Chỉ sửa các lỗi dưới đây rồi viết bổ sung phần còn thiếu, chủ yếu là integration test trên PostgreSQL thật.

Các lỗi tìm ra và đã sửa (trong file thuộc quyền của tôi):

1. **Đếm lại phiếu kiểm kê (`RecountCount`) luôn lỗi 23514.**
   - Nguyên nhân: code xoá `counted_qty` trước rồi mới xoá `variance_qty`, vi phạm CHECK `ck_stock_count_lines__variance_qty_counted_qty`.
   - Cách sửa: đảo thứ tự, xoá chênh lệch trước (`count.go`).
2. **LedgerVerifier báo sai khi đảo phiếu nhập.**
   - Khi đảo một move nhập lấy hết tồn hoặc lấy nhiều giá trị hơn mức đang có, `book.mirror` cố ý kẹp giá trị bằng toàn bộ `stock_value`. Mục đích là để không còn giá trị treo trên tồn bằng 0 và không làm giá trị âm. Check "reversal mirror" lại đòi giá trị khớp tuyệt đối nên báo mismatch giả. Property test đã phát hiện ra lỗi này.
   - Cách sửa: check vẫn so chính xác số lượng và lô với move gốc. Với move gốc là nhập, giá trị chỉ cần là dòng ra (≤ 0). Move xuất và move điều chỉnh giá vẫn phải đối xứng tuyệt đối. Tính đúng của tổng giá trị đã có check `sku stock value` đảm nhiệm (`verify.go`).
3. **Đã sửa ở phần trước (vẫn giữ):**
   - Dấu âm trong `AutoConvert.Format` (`queries.go`).
   - `publishOnce` trả về số tín hiệu thật (`signals.go`).
   - Wiring app/CLI/worker/OpenAPI.

Viết mới:

- `inventory_integration_test.go` (`//go:build integration`, testcontainers, seed bằng SQL thô vì depguard cấm import catalog/pricing). Có 14 test:
  - **Tồn đầu kỳ:** quy đổi đơn vị (2 hộp × 10 thành 20 cái), bắt buộc `unit_cost`, onboarding `opening_stock` được hoàn tất.
  - **Không thay màu khi xuất:** xuất "đỏ" khi chỉ có "xanh" trả `409 insufficient_stock` với các field `available.xanh=12`, `requested_color=do`, `requested_qty=1`, `sku=SP-COLOR`. Thiếu màu trả `color_required`.
  - **Lô và hạn dùng:**
    - FEFO lấy lô hết hạn sớm nhất.
    - Lô đã hết hạn chỉ định rõ trả `409 expired_lot_blocked`; FEFO bỏ qua lô hết hạn nên trả `insufficient_stock`.
    - `RollExpiry` trả 1 rồi 0 khi chạy lại.
    - Lọc lô `expired` và trace lô.
  - **Tín hiệu kho:** lô sắp hết hạn và tồn thấp chỉ publish một lần mỗi ngày (`stock.low`, `lot.expiring`).
  - **Giữ hàng đồng thời:** 20 goroutine giữ 1 trên 10 đơn vị cho đúng 10 thành công và Σ reserved = 10 (khớp `sku_stock.reserved`). Chạy lặp `-count=5` đều xanh. Release được, release lần hai trả `reservation_not_active`.
  - **Đảo phiếu:**
    - Đảo khôi phục tồn và giá trị, `ReversedBy` được gán.
    - Đảo lần hai trả `already_reversed`; đảo phiếu đảo trả `reversal_not_reversible`.
    - Idempotency replay trả cùng phiếu.
  - **Huỷ hàng và cách ly:** huỷ hàng không có xác nhận trả `exception_required{stock_disposal}`; có xác nhận thì thành công, và một xác nhận không dùng lại được. Cách ly vào rồi giải phóng có xác nhận `quarantine_release`.
  - **Kiểm kê:**
    - Blind count ẩn `system_qty`.
    - Chênh lệch tính tại thời điểm chốt (moved −1, variance −1).
    - Sai version trả `precondition_failed`; thiếu dòng trả `count_incomplete`.
    - Recount mở lại dòng.
    - Post không có xác nhận trả `count_variance`, có xác nhận thì sinh DC kind `count`; post lại trả `count_already_posted`.
  - **Tính lại giá vốn:**
    - Phiếu nhập lùi ngày: preview cho Δ giá trị −250 và Δ giá vốn +250; confirm cho giá trị 2250, giá bình quân 150.
    - Preview lại không còn gì cần sửa.
    - Preview bị cũ trả `recost_stale`; discard xong confirm trả `recost_not_pending`.
  - **Thẻ kho:** đầu kỳ 10 + nhập 5 − xuất 4 = cuối kỳ 11 = on_hand, `reconciled=true`. Kỳ quá khứ cũng khớp. Sửa lén `sku_stock` thì `reconciled=false`.
  - **Property test:** 520 thao tác ngẫu nhiên có seed, gồm nhập, xuất bằng DC hoặc ledger bán, cách ly, giải phóng, giữ, nhả và đảo phiếu.
    - Ít nhất một nửa số thao tác thành công. Verifier sạch sau mỗi 100 thao tác và ở cuối.
    - Sau khi sửa lén `sku_stock`, `Verify` và `VerifySkus` bắt được lỗi, còn `VerifySkus` của SKU khác vẫn sạch.
  - **Port catalog:** `StockReader.OnHandByColor` trả tồn theo màu, bỏ hàng cách ly và bỏ SKU không có tồn.
  - **Cách ly tenant:** tenant B không thấy, không xuất, không đảo, không giữ và không recost được dữ liệu của tenant A (trả `not_found` hoặc bị từ chối). Verifier của cả hai tenant sạch.
  - **Routes HTTP:**
    - Không có session trả 401; thiếu `Idempotency-Key` bị từ chối; replay trả cùng phiếu.
    - `GET /stock`, `/stock-card` (reconciled), `/lots` và `GET /stock-adjustments/{id}` đều chạy.
    - Huỷ hàng: thiếu reauth trả 403 `reauth_required`; có reauth nhưng chưa xác nhận trả 409 `exception_kind=stock_disposal`; có xác nhận (hash = `exception.PayloadHash(body)`) trả 201.
    - `:reverse` không có reauth trả 403, có reauth trả 201.
    - Giữ quá số lượng trả 409 `insufficient_sellable`; giữ hợp lệ trả 201, `:release` trả 200.
    - Blind count trả `system_qty: null`. Preview recost trả 204/201.
- `inventory_test.go`: thêm helper `wantErr` (bọc `wantCode`) để errcheck không báo khi chỉ kiểm lỗi.
- `internal/cli/ledger_integration_test.go` (file chia sẻ, chỉ chèn một khối bằng Edit): test `ledger verify --sku`. Cờ này chỉ kiểm sổ kho của SKU đó (quỹ bị sửa lén không bị kiểm). `--sku` sai định dạng thì báo lỗi.

## File thay đổi

Thuộc quyền tôi:

- `apps/api/internal/inventory/count.go`: đổi thứ tự xoá trong `RecountCount`.
- `apps/api/internal/inventory/verify.go`: nới check "reversal mirror" cho move nhập.
- `apps/api/internal/inventory/inventory_test.go`: thêm helper `wantErr`.
- `apps/api/internal/inventory/inventory_integration_test.go`: file mới, khoảng 1.170 dòng.
- Ở phần trước: `queries.go`, `signals.go` và phần còn lại của package.

File chia sẻ (chỉ chèn khối nhỏ bằng Edit):

- `internal/app/domains.go`: wiring inventory và adapter `catalogStock`.
- `internal/cli/serve.go`, `worker.go`, `ledger.go`: handler, job kho, verifier và cờ `--sku`.
- `internal/cli/cli_integration_test.go`, `ledger_integration_test.go`.
- `api/openapi.yaml`: các path inventory.

## Migration

- Chỉ có `000031_inventory_max_stock_recost` (up/down). Migration này chưa chạy ngoài test.
- Không tạo `000033` vì không cần.
- Không có trigger, function, procedure, view, rule, extension hay DO.
- Migration lint chỉ báo lỗi "version 000032 is missing (contiguous)". Đây là lỗi được phép bỏ qua, 000032 thuộc agent khác.

## Lệnh kiểm tra và kết quả thật

Chạy trong `apps/api`, ngày 2026-10-17:

| Lệnh | Kết quả |
|---|---|
| `go build ./...` | sạch |
| `go vet ./...` và `go vet -tags integration ./...` | sạch |
| `golangci-lint run ./internal/inventory/...` (có và không `--build-tags integration`) | `0 issues.` |
| `go test -race -count=1 ./internal/inventory/...` | `ok` (17 unit test) |
| `go test -race -count=1 -tags integration ./internal/inventory/...` | `ok` khoảng 29.8s (14 integration test + unit) |
| `go test -race -count=5 -tags integration -run TestReservationsNeverExceed ./internal/inventory/` | `ok` |
| `go test -race -count=1 -tags integration ./internal/cli/... ./internal/app/...` | `ok` / `ok` |
| `go test -race -count=1 ./...` | tất cả `ok`, trừ 2 lỗi không thuộc phase này (xem dưới) |

Lỗi không thuộc phase 07 (chỉ ghi nhận, không sửa):

- `internal/platform/audit` `TestAuditActionNamesAreEntityDotVerb`: `debt/statement.go:253:62: action is not a resolvable string constant`. Lỗi này thuộc agent debt/cash.
- `migrations` `TestMigrationsLint`: `version 000032 is missing`. Lỗi được phép bỏ qua theo chỉ dẫn.

Test hợp đồng OpenAPI của app (`internal/app`, không tag) pass.

## Rủi ro và giới hạn

1. **`exception_required` trả 409, phase ghi 422.** Platform `apperr.ExceptionRequired` map sang 409 cho mọi domain. Tôi giữ hành vi chung và không sửa platform.
2. **Xác nhận huỷ hàng khi post** gắn với `doc_type=sku`, `doc_id` = SKU của dòng đầu, `doc_version=0`; payload hash bao trọn body, audit ghi đủ `sku_ids` (xem "Sửa theo review", mục L1). Xác nhận giải phóng cách ly khi đảo phiếu gắn với chính DC.
3. **Đảo phiếu nhập khi tồn đã giảm** lấy ra toàn bộ giá trị còn lại (kẹp giá trị). Verifier đã chấp nhận trường hợp này có chủ đích. Biên lợi nhuận của các lần xuất trước đó không được sửa lại; muốn sửa thì dùng recost.
4. **Check `sku expired` của verifier là chặn trên.** Verifier chạy bằng role read-only không đọc được múi giờ tenant, nên expired_qty có thể trễ hơn lô nhưng không được vượt.
5. **Giới hạn kiểm kê:**
   - Dòng tăng chênh lệch trên SKU theo lô hoặc hạn dùng mà chưa ghi lô/hạn bị chặn ở finish và post bằng `count_lot_required`; người đếm ghi `lot_no`/`expiry_date` rồi finish lại.
   - Lô nhập sau khi bắt đầu kiểm kê không có dòng trong phiếu.
6. **Preview recost** trả 204 khi không có gì cần sửa, ngược lại trả 201.
7. **Service inventory trong worker không có `Posting`.** Worker chỉ chạy roll hạn dùng, giải phóng hold hết hạn, tín hiệu và gợi ý recost, không ghi sổ.
8. **Giá theo màu** dùng `PriceHints` chung. Catalog chưa có input `max_stock` (cột đã có ở 000031).
9. **Chưa có port** sales, procurement và kits; các phase 08, 09 và 11 sẽ dùng `svc.Ledger()`. Chưa có sandbox resetter cho inventory.
10. **Route** dùng `TenantAPI` thay vì `TenantPostingAPI`; idempotency được kiểm trong handler qua coordinator.

## Câu hỏi mở

- Đã chốt: `exception_required` giữ 409; xác nhận theo payload hash toàn phiếu là đủ. Phase/PRD còn ghi 422 cần được sửa cho khớp.
- Bảng trong `plan.md` vẫn ghi phase 07 "In progress". Tôi không sửa vì đó là file chung; team-lead cập nhật giúp.

## Sửa theo review

Nguồn: `plans/reports/code-reviewer-261017-1246-inventory-ledger-review.md`. Đã sửa M1–M6 và L2–L4, mỗi mục có test regression. L1 giữ nguyên theo quyết định của team-lead. Mục 2, 5 và 7 của phần "Rủi ro và giới hạn" và phần "Câu hỏi mở" ở trên đã được cập nhật theo các sửa này.

| Mục | Sửa | Test regression |
|-----|-----|-----------------|
| M1 | DC `decrease` hoặc `quarantine_in` có lý do kind `disposal` phải có xác nhận `stock_disposal` và re-auth (`AdjustmentPolicy.exceptionFor`, `Service.AdjustmentNeedsReauth` đọc kind của lý do trước khi chọn route có re-auth). Audit `after` ghi `exception_kind`, `exception_confirmation_id` và `sku_ids` (đủ danh sách SKU, đã sort). | `TestAdjustmentPolicies` (unit); `TestStockLeavingTheShopForDisposalNeedsTheOwnersConfirmation`; `TestRoutes` (decrease kèm lý do disposal, session cũ → 403 `reauth_required`) |
| M2 | Đảo DC `quarantine_in` phải tiêu một xác nhận `quarantine_release` gắn với chính DC đó (`doc_type=stock_adjustment`, `doc_id`=DC gốc, payload hash của body lệnh reverse). Route reverse nhận body tuỳ chọn `{exception_confirmation_id}`. | `TestReversingAQuarantineNeedsAReleaseConfirmation`; `TestRoutes` (reverse quarantine qua HTTP: 409 khi chưa xác nhận, 201 khi có) |
| M3 | Có hai thay đổi. `Reverse` mirror chân vào lot cách ly sau cùng, nên giá trị SKU không bao giờ hụt giữa hai chân. Clamp giá trị chỉ còn áp cho đảo phiếu nhập thật; chân quarantine mirror đúng giá trị gốc. Check `reversal mirror` của verifier đòi chân `quarantine_in` mirror đúng giá trị. | `TestReversingAQuarantineKeepsTheCostAddedWhileQuarantined`: đúng kịch bản reviewer (nhập 10@100, cách ly 10, landed cost +50, đảo → V=1050, avg 105, verify sạch). Test còn sửa tay move đảo thành −1050 và projection thành 1000; verifier báo đúng một lỗi `reversal mirror`. |
| M4 | Dòng kiểm kê không có lot của SKU theo lô hoặc theo hạn dùng nhận `lot_no`/`expiry_date` (migration 000035: `new_lot_no`, `new_expiry_date`, kèm 2 CHECK). `FinishCount` và `:post` trả 422 `count_lot_required`, field theo `lines.<line_id>.lot_no` hoặc `.expiry_date`, nên người đếm sửa được dòng rồi finish lại; `:post` không bao giờ kẹt. Validation: `not_allowed` (dòng đã có lot), `not_tracked` (SKU không theo lô hoặc hạn), `too_long` (quá 64 ký tự). Response dòng thêm `track_expiry`; `lot_no`/`expiry_date` hiển thị lot mới. | `TestCountGainOfAnUntrackedLotIsBookedIntoTheLotTheCounterRecords` |
| M5 | Giữ `expires_at`. Thêm `Service.ReleaseExpiredReservations`: giải phóng hold thủ công đã hết hạn theo thứ tự sku_id; mỗi hold một audit `reservation.expire` (actor system, source worker, có before/after); idempotent vì hold đã giải phóng bị bỏ qua. Worker gọi hàm này trong `runStockJobs`, giữa roll hạn dùng và tín hiệu, và log `reservations_released`. | `TestExpiredManualHoldsAreReleasedOnce` (chạy trước hạn → 0; sau hạn → 1; chạy lại → 0; reserved giảm; đúng 1 dòng audit) |
| M6 | Chọn **đẩy filter SKU vào mọi subquery**, giữ verify theo request. Lý do: phase 17 và INV-08 cần badge "Đã khớp sổ và tồn" trên thẻ kho, nên verify theo request là yêu cầu spec. Mỗi check đánh dấu `{{cột}}` ở query ngoài và ở từng subquery GROUP BY. Có hai biến thể SQL: không filter (`TRUE`, không tham số) và filter (`cột = ANY($1)`). Không dùng dạng `NOT $1 OR …`, nên generic plan vẫn dùng được index. Check `reversal mirror` cũng lọc move gốc. | `TestVerifyChecksRestrictEverySubqueryToTheAskedSkus` (unit, cấu trúc); `TestVerifySkusReadsOnlyTheAskedSkusRows` (EXPLAIN ANALYZE từng check cho 1 SKU: không scan `stock_moves`/`reservations` nào trả dòng của SKU khác). Đã thử bỏ filter ở một subquery: cả hai test đều fail. |
| L2 | `ReservationService.change` tính lại sellable của SKU (lots và hạn dùng hôm nay) sau khi lock `sku_stock`, trước khi kiểm. Lot hết hạn mà roller chưa chạy không còn được giữ hàng. | `TestReservingCountsLotsExpiredSinceTheLastRoll` (thử bỏ recompute: test fail) |
| L3 | Chọn nghĩa **sellable**. `StockReader.OnHandByColor` loại cả lot cách ly lẫn lot quá hạn; ngày hết hạn vẫn bán. `StockReader` phải tạo bằng `(*Service).StockReader()`. Ngày nghiệp vụ được tính ngay trên `q` của caller: tenant lấy từ `app.tenant_id`, múi giờ đọc từ `tenants`. Role read-only (phiên support) không đọc được `tenants`, nên dùng múi giờ mặc định `Asia/Ho_Chi_Minh`. Vì vậy, lot vào đúng ngày hết hạn của tenant ở múi giờ khác có thể lệch một ngày khi support xem. Zero value trả lỗi. `app/domains.go` đổi thành `catalogStock{r: inv.StockReader()}`. | `TestStockReaderShowsShelfStockByColor`: chạy trên pool 1 connection, có deadline. Tenant ở Asia/Tokyo: role app tính theo Tokyo, role read-only tính theo Ho Chi Minh; zero value trả lỗi. |
| L4 | Down 000031 thêm rồi bỏ ngay một CHECK tên `ck_cogs_adjustments__down_refused_cost_recalculation_applied`. Khi đã có dòng `cost_recalculation`, down fail sớm và tên constraint nói rõ lý do; khi chưa có, down chạy như cũ. | `TestRollingBackMaxStockRefusesAppliedCostRecalculations`: chạy file down trong tx superuser rồi rollback; có dòng → lỗi nêu tên constraint; không có → chạy được. |

L1 vẫn gắn xác nhận với `doc_type=sku` và SKU dòng đầu, không gắn với DC. Lý do là id của DC chỉ sinh trong `Apply`, khi lệnh post đã chạy. Chủ cửa hàng xác nhận trước khi DC tồn tại, nên không có id DC để gắn. Payload hash đã bao trọn body (mọi SKU và số lượng), nên xác nhận không dùng lại được cho phiếu khác. Audit nay ghi đủ `sku_ids`, nên tra lại được mọi SKU mà xác nhận đã cho phép.

L4 và tài liệu: `docs/database.md` là file sinh tự động, không có mục rollback, nên tôi không sửa. `docs/runbook.md` không thuộc quyền sở hữu của tôi. Đề nghị team-lead thêm một dòng vào mục rollback của runbook: "down 000031 bị từ chối khi đã áp dụng recost; khôi phục từ backup trước migration".

Thay đổi chữ ký nội bộ (cùng module):

- `ReverseAdjustment(ctx, actor, key, id, ReverseInput)`, trước đây không có `ReverseInput`.
- `NeedsReauth(kind, reasonKind)`; route post dùng `Service.AdjustmentNeedsReauth`.
- `checkReason` trả thêm kind.
- `StockReader` không còn dùng zero value được.

Không có port nào của phase 08/09 bị đổi chữ ký: `Ledger()`, `Receive`, `Issue`, `Quarantine`, `CostAdjust`, `Reverse` và `ReservationService` giữ nguyên.

Ghi chú cho phase 09: `StockLedger.Issue` (`MoveSale`) không xét `reserved` và cũng không kiểm `available`; ledger chỉ chặn âm theo lot sellable. Sales phải tự kiểm `available` hoặc consume reservation của chính đơn, trong cùng lock `sku_stock`.

File dùng chung đã sửa bằng Edit:

- `internal/app/domains.go`: 1 dòng.
- `internal/cli/worker.go`: `runStockJobs` và doc comment.
- `api/openapi.yaml`: body reverse, mô tả M1/M2, `lot_no`/`expiry_date` của dòng kiểm kê, `track_expiry`, `count_lot_required`.

Kiểm tra lại sau khi sửa:

| Lệnh | Kết quả |
|------|---------|
| `go build ./...`, `go vet ./...`, `go vet -tags integration` (inventory, app, cli, migrations) | sạch |
| `golangci-lint run` inventory, app, cli (có và không `--build-tags integration`) | `0 issues` |
| `go test -race -count=1` inventory, app, cli | `ok` |
| `go test -race -count=1 -tags integration` inventory, app, cli | `ok` |
| `go test -race -count=1 [-tags integration] ./migrations/...` | `TestMigrationsLint`: chỉ lỗi `version 000036 is missing` (được phép). Có tag integration thì thêm `TestEveryTenantTableIsIsolated/receipt_lines` fail ở `ck_receipt_lines__lot_id_condition`, là constraint của migration procurement 000040, không thuộc phase 07. Test reversibility pass. |

Giới hạn còn lại:

- `StockReader` không còn mở tx riêng. Bản cũ lồng một tx thứ hai trong tx của catalog search, có thể làm pool kẹt khi cạn connection; đã kiểm bằng probe tạm, trên pool 1 connection bản cũ chờ tới deadline. Các lời gọi `Service.today` còn lại chạy trước tx chính, không lồng nhau. Không còn port nào nhận `q` của caller mà lại mở tx riêng.
- Phiên support (role read-only) xem tồn theo màu bằng múi giờ mặc định, không theo múi giờ của tenant.
- Test M3 không phân biệt được hai thay đổi M3, vì mỗi thay đổi một mình đã sửa được kịch bản. Test chỉ fail khi bỏ cả hai. Phần sửa tay dữ liệu trong test bảo đảm verifier bắt được loại lệch này.

Bổ sung theo yêu cầu của phase09-sales: `ReservationService.Adjust` nay mở lại reservation của chứng từ đã `consumed`/`released` khi tổng mới lớn hơn phần đã tất toán. Phần tăng phải nằm trong sellable, và status trở lại `active`. Hold thủ công đã kết thúc thì không mở lại (`reservation_not_active`). Chữ ký không đổi. Test: `TestABackorderedLineHoldsMoreAfterItsReservationSettled`. Gate inventory, app, cli chạy lại đều sạch ở cả hai chế độ tag.

```text
Status: DONE_WITH_CONCERNS
Summary: Đã sửa M1–M6 và L2–L4 theo review, mỗi mục có test regression; vet, lint và test -race (có và không tag integration) của inventory, app, cli đều sạch.
Concerns/Blockers: Test migrations còn lỗi ngoài phạm vi (contiguity được phép; receipt_lines isolation của procurement 000040). Runbook cần ghi chú rollback 000031 (không thuộc quyền sửa của tôi). Support xem tồn theo màu bằng múi giờ mặc định.
```
