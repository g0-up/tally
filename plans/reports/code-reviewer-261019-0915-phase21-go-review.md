# Review phase 21 (Go): ledger rebuild, expire-exports, seed, fault hook, isolation, phụ thuộc

Ngày: 2026-10-19. Người review: code-reviewer. Chỉ review, không sửa code.

## Phạm vi

- Báo cáo nguồn: `plans/reports/fullstack-developer-261019-0811-phase21-go-seed-isolation-security.md`. `apps/` không nằm trong git nên không có diff; đọc trực tiếp các tệp trong báo cáo.
- Đã đọc (gốc `apps/api/`):
  - `internal/cli/ledger_rebuild.go`, `internal/cli/ledger.go`, `internal/posting/rebuild.go`, `internal/{debt,cash,inventory,procurement,sales,kits}/{rebuild,verify}.go`, `internal/cli/ledger_rebuild_integration_test.go`
  - `internal/cli/maintenance.go`, `internal/reporting/export.go` (RunExports, ExpireExports), `internal/platform/objectstore/s3.go`, `internal/cli/maintenance_integration_test.go`
  - `internal/cli/seed.go`, `seed_dev.go`, `seed_load.go`, `internal/seedload/options.go`
  - `internal/platform/fault/fault.go`, `internal/platform/config/config.go`, `internal/cli/serve.go`, `internal/app/domains.go`, `infra/docker-compose.prod.yml`, `internal/app/posting_fault_integration_test.go`
  - `internal/app/tenant_isolation_integration_test.go`, `internal/cli/worker_tenant_integration_test.go`, `internal/cli/search_gate_benchmark_integration_test.go`
  - `go.mod`, `Dockerfile`
- Không chạm container `tally-postgres-1`. Mọi test tích hợp chạy bằng testcontainers.

## Kết quả kiểm tra tự động (apps/api)

| Bước | Kết quả |
|------|---------|
| `gofmt -l` | sạch |
| `go vet ./...` và `go vet -tags integration ./...` | ok |
| `golangci-lint run` | 0 issues |
| `go test -race -count=1` cho posting, cash, debt, inventory, procurement, kits, sales, cli, platform/fault, platform/config, seedload, reporting | ok hết |
| Tích hợp `./internal/cli/`, `-run 'TestLedgerRebuildCommand\|TestMaintenanceExpireExports\|TestSeedDevCreatesTheDevelopmentStoreOnce\|TestSeedCommandsRefuseProduction\|TestLedger'` | ok (26s) |
| Tích hợp `./internal/app/`, `-run 'TestPostingThatFailsBeforeCommitIsAllOrNothing\|TestServiceRolesCannotBypassRowLevelSecurity'` | ok (6s) |
| Tích hợp `./internal/app/`, `-run 'TestTenantIsolationAcrossEveryRoute$' -v` | ok (28s). Kết quả: 370 operation, 394 call, 220 call có id của B trong path, cả 220 trả 404. Hai field chưa map được kind và được gán một SKU của B: `id`, `plan_id`. |
| `go mod verify` | all modules verified |

Không chạy benchmark search gate (`-bench`), vì nó seed 20 000 SKU và chạy khá lâu.

## Đánh giá chung

Khung của `tally ledger rebuild` đúng ở những điểm quan trọng nhất:

- Dry-run là mặc định và luôn rollback (`errRebuildDryRun`). `--dry-run` và `--confirm` loại trừ nhau.
- Toàn bộ khóa → verify → rebuild → verify lại nằm trong một `WithTenantTx`.
- Nếu còn khác biệt sau rebuild thì rollback và không ghi audit.
- Giá trị được tính lại từ chính câu SQL check của verify (`WITH bad AS (<check>) <Stmt>`), nên không có nguồn tính thứ hai để lệch nhau.
- Các check "manual" không có `Repair`, nên không bị sửa tự động.
- Câu verify `invoice line delivered` sau khi chuyển lên package level khớp với bản sau fix L2 (CTE `delivered` bọc GROUP BY ngoài, nhánh mix/combo có `d.status <> 'reversed'`), không đổi logic.

Fault hook bị khóa đúng ngoài APP_ENV=test. Seed không in mật khẩu hay token.

Rủi ro chính nằm ở những gì rebuild **không** làm:

- không tính lại các trạng thái suy ra từ số lượng mà nó vừa sửa;
- chỉ dựa vào RLS mà không tự kiểm tra role;
- expire-exports chặn hàng đợi export của worker khi một tệp không xóa được.

## Phát hiện

### Cao

Không có.

### Trung bình

**TB-1. Rebuild sửa số lượng nhưng không sửa trạng thái suy ra từ số lượng đó, và verify cũng không có check trạng thái.**
- Vị trí:
  - `apps/api/internal/procurement/rebuild.go:17-22` (PO line received/returned). Trạng thái PO được suy ra ở `apps/api/internal/procurement/status.go:49` (`Progress`) và `:43` (`Receivable`).
  - `apps/api/internal/sales/rebuild.go:18-27` (invoice line delivered, invoice delivered). Trạng thái hóa đơn được suy ra ở `apps/api/internal/sales/status.go:102` (`deliveryStatus`).
  - `apps/api/internal/kits/rebuild.go:17-29` (assembly order completed/disassembled/consumed). Trạng thái lệnh lắp ráp (reserved/in_progress/partially_completed/completed) không được tính lại.
  - `procurement/verify.go` và `sales/verify.go` không có check nào so trạng thái PO hay hóa đơn với số lượng.
- Kịch bản: projection `received_base_qty` của một dòng PO bị ghi dư, nên PO ở trạng thái `completed`. Chạy `--confirm`: received được hạ về đúng giá trị, verify sau đó sạch, commit và ghi audit. PO vẫn `completed`, `Receivable` trả false, nên không thể nhận phần hàng còn thiếu. Lỗi này không bao giờ bị phát hiện vì verify không kiểm tra trạng thái. Tương tự, hóa đơn có `delivered` được nâng lên đủ nhưng vẫn ở `confirmed` hoặc `partially_delivered`, và ngược lại.
- Cách sửa:
  1. Thêm check vào verify: trạng thái PO khác `Progress(lines)` (với các trạng thái không phải cuối như confirmed/partially_received/completed; bỏ qua closed_short/cancelled), và tương tự cho `deliveryStatus` của hóa đơn và trạng thái lệnh lắp ráp.
  2. Thêm `Repair` cho các check đó, chạy sau repair số lượng trong cùng domain. Thứ tự trong slice `Repairs` đã cho phép việc này.

**TB-2. Rebuild chỉ dựa vào RLS để giới hạn tenant. Nếu CLI kết nối bằng role bỏ qua RLS, nó sẽ khóa và ghi đè projection của mọi tenant.**
- Vị trí:
  - `apps/api/internal/posting/rebuild.go:26-32`: `SELECT 1 FROM <table> ORDER BY key FOR UPDATE`, không có `WHERE tenant_id`.
  - Các câu check và repair trong `*/verify.go` và `*/rebuild.go` cũng không lọc tenant_id.
  - `apps/api/internal/cli/ledger.go` (`openLedgerDatabase` dùng `DATABASE_URL`).
- Kịch bản: operator chạy `tally ledger rebuild --tenant X --confirm` với `DATABASE_URL` trỏ tới `MIGRATE_DATABASE_URL` hoặc tới role `postgres` của dịch vụ managed (superuser hoặc BYPASSRLS). Khi đó:
  - LockTenant khóa toàn bộ bảng của mọi tenant;
  - các repair ghi đè projection của mọi tenant;
  - audit chỉ ghi cho tenant X.

  `TestServiceRolesCannotBypassRowLevelSecurity` chỉ chứng minh các role do migration tạo ra trong DB test, không chứng minh role mà CLI thực sự dùng lúc chạy.
- Cách sửa: ở đầu `rebuildLedgers` (và cả `verify`), chạy `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`; nếu true thì từ chối. Có thể thêm `AND tenant_id = current_setting('app.tenant_id')::uuid` vào `LockTenant` như một lớp phòng thủ thứ hai.

**TB-3. `RunExports` dừng toàn bộ export đang chờ của tenant khi một tệp hết hạn không xóa được.**
- Vị trí:
  - `apps/api/internal/reporting/export.go:320-322`: `if _, err := s.ExpireExports(...); err != nil { return 0, err }`.
  - `ExpireExports` gom lỗi xóa và trả về ở `:384-388` và `:409`.
- Kịch bản: một object bị từ chối xóa vĩnh viễn (policy bucket, object lock, quyền IAM thiếu `DeleteObject`). Từ đó mỗi tick worker của tenant đó trả lỗi trước khi claim export, nên mọi export `queued` của tenant bị kẹt cho tới khi có người sửa bucket. Người dùng thấy báo cáo không bao giờ xong. Test `TestMaintenanceExpireExports` xác nhận rằng `ExpireExports` trả lỗi trong trường hợp này, nhưng không có test nào cho đường đi của worker.
- Cách sửa: trong `RunExports`, log lỗi của `ExpireExports` (hoặc đưa vào `errs`) rồi vẫn tiếp tục vòng claim. Chỉ lỗi DB của bước tìm hoặc fail export cũ mới nên chặn.
- Ghi chú: báo cáo developer nói "RunExports vẫn gọi nó như trước". Không có diff nên không xác định được trước đây lỗi xóa có bị nuốt hay không (xem câu hỏi mở 2).

**TB-4. Seed chặn production theo kiểu denylist trên một biến mặc định là `development`.**
- Vị trí:
  - `apps/api/internal/cli/seed_load.go:75-84` (`refuseProduction`);
  - `apps/api/internal/platform/config/config.go:377`, nơi `APP_ENV` mặc định là `development`.
- Kịch bản: operator chạy binary `tally` trực tiếp trên host hoặc trong một container ad-hoc, với `DATABASE_URL` của production nhưng không đặt `APP_ENV`. Khi đó `seed load` tạo tới `MaxTenants` tenant giả, gửi mail xác minh và ghi hàng triệu stock move vào DB production. Compose production đặt `APP_ENV=production` nên đường chạy chuẩn thì an toàn; rủi ro nằm ở đường chạy ngoài compose.
- Cách sửa: dùng allowlist, chỉ cho chạy khi `APP_ENV` được đặt **tường minh** là `development` hoặc `test` (dùng `lookup("APP_ENV")` và kiểm tra `ok`), hoặc yêu cầu thêm `--i-know-this-is-not-production` khi APP_ENV không được đặt.

### Thấp

**T-1. Comment của `LockTenant` hứa nhiều hơn những gì nó làm.**
- Vị trí: `apps/api/internal/posting/rebuild.go:22-25`.
- Comment nói "no posting moves a ledger under it". Nhưng các writer không đi qua posting coordinator không bị chặn:
  - `procurement.MatchReceipts` và `CreateDocument` (matching.go) chỉ khóa receipts và supplier documents;
  - các hàng `sku_stock` mới do `ensureSkuStock` chèn vào.
- Dưới READ COMMITTED, repair có thể ghi một giá trị `bad` đã cũ. Lần verify thứ hai trong cùng tx sẽ bắt được và rollback, và khóa dòng được giữ tới commit, nên **không** có trường hợp commit dữ liệu sai. Hệ quả chỉ là rebuild thất bại hoặc deadlock rồi rollback.
- Cách sửa: sửa comment cho đúng, hoặc lấy thêm `pg_advisory_xact_lock` theo tenant ở các writer supplier document.

**T-2. Bump `version`/`updated_at` không nhất quán giữa các repair.**
- Có bump: debt, cash, invoices, assembly orders, supplier_documents (variance), sku_stock.
- Không bump:
  - `apps/api/internal/procurement/rebuild.go:17-31`: PO lines, receipt_lines, receipts landed cost, supplier_document_receipts;
  - `apps/api/internal/sales/rebuild.go:28-36`: sales document line returned, sales document status, cogs.
- Kịch bản: client đang giữ ETag hoặc `If-Match` của một chứng từ bán. Sau rebuild, PATCH vẫn lọt qua với version cũ, và UI cache không biết dữ liệu đã đổi.
- Cách sửa: bump `version` (với bảng có cột này) và `updated_at = $1` ở mọi repair.

**T-3. Repair `assembly order component consumed` nối theo chuỗi label.**
- Vị trí: `apps/api/internal/kits/rebuild.go:25-29`.
- Repair nối bằng `o.doc_no || ' ' || s.code = bad.label`, và chỉ chạy khi mỗi SKU có đúng một item. Nếu code SKU chứa khoảng trắng, hoặc label bị đổi format trong verify, repair không khớp dòng nào. Check đó khi ấy chuyển thành "manual" một cách âm thầm.
- Cách sửa: cho check trả thêm cột `sku_id` (hoặc id của item) và nối theo id.

**T-4. `differenceKey` gom nhiều hàng vào một khóa.**
- Vị trí: `apps/api/internal/cli/ledger_rebuild.go:149-158` và phần đếm ở `:195-196`.
- Khóa là "đến id đầu tiên". Các dòng "component consumed" cùng một lệnh lắp ráp có chung id lệnh, nên chỉ cần một SKU còn lệch là mọi dòng của lệnh đó bị đánh dấu "fix by hand". Kéo theo đó, số "to rebuild / to fix by hand" trong bản tóm tắt dry-run sai.
- Không ảnh hưởng tới tính đúng của commit, vì commit dựa vào `len(res.after)`.
- Cách sửa: tạo khóa từ toàn bộ phần định danh của dòng (id cộng label), hoặc để check trả một cột khóa riêng.

**T-5. Rebuild trên tenant lớn có thể bị `statement_timeout` (mặc định 15s).**
- Vị trí: `apps/api/internal/cli/ledger.go` (`openLedgerDatabase` dùng `cfg.Database.StatementTimeout`).
- `LockTenant` khóa mọi dòng của 7 bảng. Các check cũng quét toàn bảng. Một tenant cỡ 1 triệu stock move có thể vượt 15s ở một câu; khi đó toàn bộ rebuild rollback.
- Không mất dữ liệu, nhưng lệnh không dùng được nếu không biết đặt `DB_STATEMENT_TIMEOUT`.
- Cách sửa: thêm flag `--statement-timeout`, hoặc `SET LOCAL statement_timeout` cao hơn trong tx rebuild, và ghi rõ trong `--help`.

**T-6. Test rebuild chưa phủ một số repair.**
- Vị trí: `apps/api/internal/cli/ledger_rebuild_integration_test.go`.
- Chưa có test làm hỏng rồi sửa cho:
  - credit note;
  - PO line received/returned;
  - supplier doc matched/variance;
  - kits component consumed/disassembled;
  - nhánh INSERT … ON CONFLICT khi thiếu dòng `lot_balances`;
  - một posting chạy song song với rebuild.
- Mọi Stmt đều được thực thi mỗi lần chạy, nên lỗi cú pháp SQL sẽ bị bắt; nhưng tính đúng của giá trị và guard thì chưa được kiểm.
- Cách sửa: mở rộng bảng corrupt trong test với các trường hợp trên.

**T-7. Export hết hạn vẫn bị tính vào quota lưu trữ.**
- Vị trí:
  - `apps/api/internal/reporting/export.go:398-401`: chỉ đổi status, không xóa dòng `attachments`;
  - quota được tính ở `apps/api/internal/org/attachments.go:121` và `apps/api/internal/saas/usage_job.go:73,136`, bằng `SUM(attachments.size_bytes)`.
- Kịch bản: tệp đã bị xóa khỏi S3 nhưng `size_bytes` vẫn được cộng, nên tenant xuất báo cáo thường xuyên sẽ chạm quota vì những tệp không còn tồn tại.
- Lỗi này có từ trước, nhưng expire-exports là nơi tự nhiên để sửa.
- Cách sửa: trong tx đánh dấu expired, xóa (hoặc đánh dấu deleted) các dòng `attachments` tương ứng.

**T-8. expire-exports bỏ qua tenant đang bảo trì.**
- Vị trí: `apps/api/internal/cli/maintenance.go` (`ActiveTenantIDs`).
- Tenant ở trạng thái maintenance, hoặc sandbox đã bị thay thế, không được dọn tệp hết hạn. Đường link vẫn bị từ chối theo `expires_at`, nên không lộ dữ liệu; chỉ là tệp còn nằm lại.
- Cách sửa: xác nhận đây là chủ ý (câu hỏi mở 3). Nếu không phải, dùng danh sách mọi tenant chưa bị xóa.

**T-9. Isolation suite chỉ log, không fail, với các trường hợp có thể làm test rỗng.**
- Vị trí: `apps/api/internal/app/tenant_isolation_integration_test.go:1121-1124` và `:1306-1313`.
- Có hai chỗ:
  - `w.others` gồm các câu trả lời cho id của B trong path mà không phải 404 hoặc 403. Nó chỉ được `t.Logf`. Lần chạy hiện tại có 0 mục, nên hiện chưa sai; nhưng một route mới mà validate body trước khi tra record sẽ trả cùng 422 cho cả baseline lẫn foreign và vẫn qua, dù không chứng minh được gì về đường tra record.
  - `w.unmapped` (hiện là `id` và `plan_id`) cũng chỉ được log. Field chưa map được gán một SKU của B, nên nếu field đó thực ra trỏ tới loại record khác thì phép thử với field ấy là rỗng.
- Cách sửa:
  - fail khi `w.others` khác rỗng, trừ các mục trong allowlist có ghi lý do (giống `readsOfOwnRecords`);
  - fail khi xuất hiện field chưa map mà không có trong allowlist.

**T-10. "Search gate" không tự fail khi vượt ngưỡng.**
- Vị trí: `apps/api/internal/cli/search_gate_benchmark_integration_test.go:92-141`.
- Comment nói "release gate is a p95 of at most one second", nhưng benchmark chỉ gọi `ReportMetric`. Một p95 bằng 3s vẫn cho kết quả `ok`.
- Ngoài ra:
  - nhóm `instock` không kiểm có kết quả;
  - phép đo gọi thẳng `catalog.Service.Search`, bỏ qua tầng HTTP, middleware và JSON. Số đo vì thế là cận dưới của latency thật.
- Cách sửa: sau vòng đo, `b.Errorf` khi `at(0.95)` của nhóm `all` lớn hơn 1s; kiểm `len(items) > 0` cho `instock`; ghi rõ trong báo cáo rằng đây là latency của service, không phải end-to-end.

**T-11. `jobErr` có thể nuốt lỗi chung của job.**
- Vị trí: `apps/api/internal/cli/worker_tenant_integration_test.go:79-91`.
- Một lỗi không chứa id tenant (ví dụ lỗi khi liệt kê tenant, hay lỗi kết nối) chỉ được log. Job hỏng toàn bộ mà test vẫn qua, miễn là các assert về hiệu ứng phía sau không bắt được.
- Cách sửa: chỉ log khi lỗi chứa id của một tenant **khác**; mọi lỗi còn lại thì fail.

**T-12. Toolchain trong Dockerfile không được ghim theo `go 1.26.9`.**
- Vị trí:
  - `apps/api/Dockerfile:2-4`: `FROM golang:1.26-alpine` với `GOTOOLCHAIN=local`;
  - `apps/api/go.mod:3`: `go 1.26.9`.
- Kịch bản: máy build có cache cũ của image `golang:1.26-alpine` (ví dụ 1.26.5). Lệnh build image thất bại với "go.mod requires go >= 1.26.9 (running go 1.26.5; GOTOOLCHAIN=local)". CI dùng `go-version-file` nên không bị.
- Cách sửa: ghim `golang:1.26.9-alpine`, hoặc dùng digest.

## Kiểm tra theo các mục ưu tiên của lead

1. **ledger rebuild**
   - Dry-run mặc định và luôn rollback: **đạt** (`ledger_rebuild.go`, `errRebuildDryRun`).
   - `--confirm` mới ghi: **đạt**.
   - Một tx cho khóa → verify → rebuild → verify; còn lệch thì rollback và không ghi audit: **đạt**. Test xác nhận digest không đổi và 0 audit.
   - Không ảnh hưởng tenant khác: **đạt với role `tally_app`**. Test so digest toàn bộ hàng của tenant kia. Rủi ro còn lại khi dùng role bypass RLS: TB-2.
   - Tính lại từ chứng từ: **đạt** cho số lượng và tiền. **Chưa đạt** cho trạng thái suy ra: TB-1.
   - Check manual không bị sửa tự động: **đạt**.
   - Thứ tự domain: debt, cash, inventory, procurement, sales, kits. Không tìm thấy repair nào đọc projection mà một domain sau mới ghi. Inventory dựng sku_stock từ lots và lot_balances, những bảng do chính nó sửa trước; sales COGS đọc stock_moves, là nguồn chứ không phải projection.
   - Câu verify chuyển lên package level không đổi logic: **đạt**, gồm cả CTE `delivered` và nhánh mix/combo có `status <> 'reversed'`.
2. **expire-exports**
   - Chỉ xóa export có `expires_at <= now`: **đạt**. Giới hạn trong tenant qua `WithTenantTx` và `eachTenant`: **đạt**.
   - Idempotent: **đạt**. `RemoveObject` không lỗi khi key không tồn tại, và UPDATE có guard `status='succeeded'`.
   - Lỗi object storage giữa chừng: tệp lỗi giữ trạng thái để thử lại, các tệp khác vẫn hết hạn, lệnh exit khác 0: **đạt**. Ảnh hưởng tới worker: TB-3. Quota: T-7.
3. **seed dev / seed load**
   - Chặn production: **đạt khi APP_ENV=production**. Mặc định fail-open: TB-4.
   - Chạy lại:
     - `seed dev` chạy lại an toàn, và báo lỗi nếu store cũ thiếu dữ liệu;
     - `seed load` từ chối slug đã có và kiểm toàn bộ slug trước khi tạo, nhưng tenant đã fill dở thì phải đổi `--seed` hoặc `--slug-prefix`. Hành vi này có ghi trong `--help`.
   - Không in mật khẩu hay secret: **đạt**. Mật khẩu chỉ đọc từ env. Output chỉ có slug, id, email và trạng thái. `verifyToken` không đưa link (chứa token) vào thông báo lỗi.
4. **Fault hook**
   - `fault.Parse` trả nil trừ khi APP_ENV=test, nên cần cả `APP_ENV=test` và `TALLY_FAULT_INJECT`.
   - Compose production đặt `APP_ENV=${APP_ENV:-production}` và không truyền `TALLY_FAULT_INJECT`.
   - `serve` cảnh báo khi giá trị bị bỏ qua hoặc khi hook đang armed.
   - Đánh giá: **đạt**. Rủi ro còn lại chỉ là có người tự đặt `APP_ENV=test` trên production.
5. **Isolation suite và search gate**
   - Isolation suite chứng minh đúng điều nó tuyên bố:
     - 220/220 call với id của B trong path trả 404;
     - so với baseline có id không tồn tại;
     - quét UUID của B trong body và header;
     - lấy digest mọi bảng có `tenant_id` của B trước và sau;
     - kiểm support view.
   - Điểm yếu (fail-open trên log): T-9.
   - Worker suite kiểm `app.tenant_id` và các hàng nhìn thấy được trong từng handler và sweep: **đạt**. Điểm yếu: T-11.
   - Search gate không tự fail: T-10.
   - Posting fault test chứng minh all-or-nothing, retry đúng một lần và không có lỗ số chứng từ: **đạt**.
6. **go.mod**
   - Các bản nâng (x/net v0.60.0, quic-go v0.59.1, moby/go-archive v0.3.0, moby/sys/user v0.4.1) đều là phụ thuộc gián tiếp. quic-go đến qua gin/http3.
   - vet, lint, test và `go mod verify` đều sạch. Không thấy breaking change ở các package dùng trực tiếp.
   - Rủi ro build image: T-12.

## Thứ tự sửa khuyến nghị

1. TB-1: thêm check và repair trạng thái PO, hóa đơn và lệnh lắp ráp; thêm test.
2. TB-2: từ chối role superuser hoặc BYPASSRLS ở đầu `ledger verify` và `ledger rebuild`.
3. TB-3: không để lỗi xóa tệp chặn `RunExports`.
4. TB-4: chuyển chặn production sang allowlist APP_ENV tường minh.
5. T-9 và T-10: biến các log thành assert để suite không qua một cách rỗng.
6. Các mục Thấp còn lại.

## Câu hỏi mở

1. Rebuild có cố ý **không** sửa trạng thái chứng từ (PO, hóa đơn, lệnh lắp ráp), ví dụ để người vận hành tự xem lại? Nếu đúng vậy thì cần ít nhất một check verify để phát hiện, và phải ghi rõ trong `--help`.
2. Trước phase này, `expireExports` có trả lỗi xóa tệp lên `RunExports` không? Nếu không, TB-3 là hồi quy do phase này gây ra.
3. Việc expire-exports bỏ qua tenant đang maintenance có phải chủ ý không?
4. Vận hành thật sẽ chạy `tally ledger rebuild` bằng role nào (`tally_app` qua compose exec, hay một role admin)? Câu trả lời quyết định mức độ của TB-2.

```
Status: DONE_WITH_CONCERNS
Summary: Không có lỗi mức Cao. Rebuild đúng ở dry-run, tính nguyên tử, rollback khi còn lệch và cô lập tenant với role tally_app; fault hook bị khóa đúng; seed không lộ secret; mọi gate tự động và test tích hợp liên quan đều qua. Có 4 lỗi Trung bình: rebuild không sửa trạng thái suy ra, rebuild chỉ dựa vào RLS, lỗi xóa tệp export chặn worker, seed chặn production theo kiểu fail-open, cùng 12 mục Thấp.
Concerns/Blockers: TB-1 có thể để lại PO hoặc hóa đơn ở trạng thái sai sau một lần --confirm được báo là sạch. Nên sửa TB-1 và TB-2 trước khi đưa rebuild cho vận hành dùng trên production.
```

## Re-review (2026-10-19, các bản sửa của p21-go)

Phạm vi: TB-1..TB-4 và T-1..T-12, theo mục 11 và Gate trong `plans/reports/fullstack-developer-261019-0811-phase21-go-seed-isolation-security.md`. Chỉ đọc code. Không sửa code, không chạy `go build`, không chạy integration (lead đang chạy gate đầy đủ). Mọi đường dẫn dưới đây nằm trong `/home/cesc/Documents/personal-workspace/tally/apps/api/`, trừ khi ghi khác.

### Kết luận theo từng phát hiện

| Mã | Kết luận | Bằng chứng |
|---|---|---|
| TB-1 | Đã sửa | Có `StatusCheck` cho PO (`internal/procurement/verify.go:198-245`), hóa đơn (`internal/sales/verify.go:168-215`) và lệnh lắp ráp (`internal/kits/verify.go:199-262`). `Rebuild` chạy `RunRepairs` trước rồi mới tới `FixStatuses` (`internal/procurement/rebuild.go:62`, `internal/sales/rebuild.go:54`, `internal/kits/rebuild.go:47`). `FixStatuses` (`internal/posting/rebuild.go:173-193`) ghi qua `db.UpdateWithVersion`, nên bump `version` và `updated_at` và khóa lạc quan theo version. Chi tiết bên dưới. |
| TB-2 | Đã sửa | `requireAppRole` (`internal/cli/ledger.go:171-187`) đọc `rolsuper OR rolbypassrls` của `current_user` và từ chối role bypass. Hàm được gọi trong `openLedgerDatabase` (`internal/cli/ledger.go:161`), nên áp dụng cho cả `ledger verify` lẫn `ledger rebuild`. |
| TB-2 (lọc tenant) | Đã sửa | `posting.InTenant` (`internal/posting/rebuild.go:18`) có trong `LockTenant` và trong mọi câu `UPDATE` repair của procurement, sales, kits, debt, cash và inventory (inventory dùng `ofTenant`). Phần cập nhật parent của `Repair.Parent` cũng có `AND tenant_id = InTenant`. |
| TB-3 | Đã sửa | Lỗi của `ExpireExports` được thêm vào `errs` (`internal/reporting/export.go:324-325`), và vòng claim export vẫn chạy tiếp. |
| TB-4 | Đã sửa | `explicitDevelopmentOrTest` (`internal/cli/migrate.go:189-198`) yêu cầu `APP_ENV` được đặt, không rỗng, và là `development` hoặc `test`. Hàm này được dùng cho `seed load`, `seed dev` (`internal/cli/seed_load.go:76-85`, `internal/cli/seed_dev.go:48`) và cho `backupRequired` (`internal/cli/migrate.go:181-182`). Thiếu `APP_ENV` giờ bị coi là không an toàn (fail-closed). |
| T-1 | Đã sửa | Comment của `LockTenant` đã khớp với hành vi. Hàm cũng tự lọc theo `InTenant`. |
| T-2 | Đã sửa ở những bảng có cột | `purchase_orders`, `invoices`, `assembly_orders` và `supplier_documents` được bump qua `Repair.Parent` hoặc ngay trong repair. `sales_documents`, `receipts` và `receipt_lines` không có cột `version`/`updated_at`, nên không có gì để bump. |
| T-3 | Đã sửa | Repair consumed nối theo `i.sku_id = bad.sku_id`. Verify trả về `st.sku_id` (struct hàng có `SkuID`). Guard `count = 1` vẫn còn. |
| T-4 | Đã sửa | `differenceKey` (`internal/cli/ledger_rebuild.go:160-175`) giờ giữ cả phần ` (label)`. Còn một điểm phụ: nếu chính label chứa chuỗi `"): "` thì khóa bị cắt sớm. Không thực tế với các label hiện có. |
| T-5 | Đã sửa | Có cờ `--statement-timeout`: mặc định 10m, 0 là không giới hạn, giá trị âm bị từ chối (`internal/cli/ledger_rebuild.go:38,71,90`). Cờ được áp dụng bằng `set_config('statement_timeout', $1, true)` ngay đầu transaction (`:186`), nên chỉ có hiệu lực trong transaction đó. |
| T-6 | Còn mở một phần | Đã thêm test cho PO line received và cho trạng thái của PO, hóa đơn và lệnh lắp ráp. Vẫn chưa có test cho: credit note, supplier document matched/variance, kits consumed/disassembled, hàng `lot_balances` bị thiếu, và rebuild chạy đồng thời với posting. |
| T-7 | Đã sửa | Câu `UPDATE ... FROM export_jobs old ... RETURNING old.attachment_id` đặt `attachment_id = NULL` rồi xóa `attachments`. Chỉ `export_jobs` có FK tới các attachment này. |
| T-8 | Chấp nhận (chủ ý) | Hành vi có comment giải thích. |
| T-9 | Đã sửa | Có allowlist `otherPathAnswers` (rỗng) và `unmappedFields` kèm lý do (`internal/app/tenant_isolation_integration_test.go:1168-1180`). Trường hợp thiếu mapping giờ gây `t.Errorf` (`:1324-1332`). |
| T-10 | Đã sửa, chưa đo lại | Bench gọi `b.Errorf` khi p95 vượt ngưỡng (`internal/cli/search_gate_benchmark_integration_test.go:150`), và `b.Fatal` nếu cả nhóm instock không tìm thấy SKU nào (`:128-130`). Benchmark chưa được chạy lại sau khi thêm assert. |
| T-11 | Đã sửa | `jobErr` tách các lỗi đã join. Lỗi không có tiền tố tenant thì fail. Lỗi của tenant thuộc test này thì fail. Chỉ lỗi `tenant <uuid>: ` của tenant thuộc test khác được log (`internal/cli/worker_tenant_integration_test.go:80-110`). |
| T-12 | Đã sửa | `Dockerfile:3` dùng `golang:1.26.9-alpine`. |

### Trạng thái sau rebuild (trọng tâm 1)

- **PO.** Chỉ xét các PO `confirmed`, `partially_received` và `completed`. `AfterReceipts` giữ nguyên `closed_short`, còn lại suy trạng thái bằng `Progress` từ `received_base_qty` so với `base_qty`. PO `draft`, `cancelled` và `closed_short` không bao giờ bị đổi.
- **Hóa đơn.** Chỉ xét `confirmed`, `partially_delivered` và `completed`. `deliveryStatus` (`internal/sales/status.go:102`) giữ `cancelled_remaining` và bỏ qua dòng header mix_pack/combo. Hóa đơn đã hủy và hóa đơn nháp nằm ngoài tập được xét.
- **Lệnh lắp ráp.** Bỏ qua `closed`, và chỉ xét lệnh có posting hoặc disassembly. Các sự kiện được phát lại theo thứ tự `posted_at, created_at, id` qua `AfterProgress`, bắt đầu từ trạng thái đang lưu. Trạng thái được suy từ sự kiện nguồn, không từ số lượng đang lưu, nên chính xác hơn PO và hóa đơn.
- **Version và updated_at.** Được bump qua `UpdateWithVersion`. Nếu một posting chạy xen giữa, update theo version trả conflict và cả transaction rollback, nên không ghi đè lặng lẽ.

### Đánh giá concern của p21-go

> "verify suy trạng thái từ số lượng đang lưu, nên lỗi làm sai cả hai cùng chiều chỉ báo dòng số lượng"

**Chấp nhận được**, vì những lý do sau:

1. Check số lượng tính giá trị mong đợi từ chứng từ nguồn (receipt, delivery, posting), không từ số lượng đang lưu. Nếu số lượng sai, dòng số lượng luôn được báo, và rebuild luôn sửa số lượng trước.
2. `FixStatuses` chạy sau `RunRepairs`, trong cùng transaction. Vì vậy trạng thái được suy từ số lượng đã sửa, và một lần `--confirm` kết thúc với cả hai đều đúng.
3. Nếu số lượng không sửa được, nó vẫn nằm trong `after`, và lệnh rollback. Không thể có trường hợp số lượng sai mà trạng thái được ghi dựa theo nó rồi commit.
4. Chỗ hụt duy nhất là verify trước rebuild báo thiếu: chỉ có dòng số lượng, chưa có dòng trạng thái. Dry-run vẫn liệt kê sẽ sửa trạng thái, và lệnh lắp ráp không bị hạn chế này.

Khuyến nghị: ghi một câu trong `--help` của `ledger verify` hoặc trong runbook: "trạng thái PO và hóa đơn được kiểm tra dựa trên số lượng đang lưu; hãy sửa các dòng số lượng trước rồi chạy verify lại".

### CI e2e-nightly (trọng tâm 6)

**Đạt.** Job đặt `APP_ENV: test`, có comment lý do (`/home/cesc/Documents/personal-workspace/tally/.github/workflows/ci.yml:224-227`).

- `DATABASE_URL` dùng `tally_app:tally_app_dev@localhost:5442` (`:232`). Giá trị này khớp với mặc định của `apps/api/Makefile` (port 5442) và `TALLY_APP_PASSWORD` trong `infra/docker-compose.yml`.
- `MIGRATE_DATABASE_URL` vẫn dùng `tally_owner`, nên migrate không đụng role app.
- `ledger verify` chạy bằng `tally_app`, nên qua được `requireAppRole`.
- `APP_ENV=test` không mở fault injection, vì fault injection còn cần `TALLY_FAULT_INJECT`, và job không đặt biến này.
- `config.Test` chỉ được tham chiếu ở `internal/cli/migrate.go:197` ngoài code test, nên `APP_ENV=test` không bật thêm hành vi nào khác.

### Quan sát mới (Thấp)

- **N-1. `FixStatuses` không đi qua bảng chuyển trạng thái.** Ví dụ: kits `completed` → `partially_completed` không có trong bảng transition. Điều này chấp nhận được vì đây là sửa dữ liệu, không phải nghiệp vụ, và output có liệt kê số hàng bị sửa. Nên ghi vào runbook rằng rebuild có thể lùi trạng thái.
- **N-2. Hóa đơn chỉ có dòng không giữ hàng.** `deliveryStatus` sẽ cho `completed`, trong khi `ConfirmInvoice` (`internal/sales/confirm.go:114`) ghi `confirmed`. Trường hợp này hiện chỉ là lý thuyết, vì mix_pack/combo luôn có dòng con giữ hàng. Nếu sau này có loại dòng dịch vụ, verify sẽ báo sai.
- **N-3. `UpdateWithVersion` trong `FixStatuses` không tự lọc tenant.** Hiện được bảo vệ bởi ba lớp: `Find` có `InTenant`, RLS, và `requireAppRole`. Không cần sửa ngay.
- **N-4. Ngưỡng search gate chưa được đo lại.** Assert p95 mới chưa được chạy trên dữ liệu thật (T-10).
- **N-5. Runbook chưa cập nhật.** `docs/runbook.md` chưa nói về `--statement-timeout`, yêu cầu role `tally_app`, và việc rebuild sửa trạng thái. Đây là phạm vi của lead.
- **N-6. Ghi chú quy trình.** p21-go tự báo đã chạy `go build ./internal/procurement/` một lần, dù hook cấm. Không ảnh hưởng tới code.

### Finding còn mở theo mức độ

- **Cao:** không có.
- **Trung bình:** không có.
- **Thấp:**
  - T-6: còn thiếu test cho credit note, supplier document matched/variance, kits consumed/disassembled, `lot_balances` thiếu hàng, và rebuild chạy đồng thời;
  - T-4: điểm phụ khi label chứa `"): "`;
  - N-1 đến N-5.
- **Chấp nhận:** T-8.

```
Status: DONE
Summary: Cả 4 lỗi Trung bình (TB-1..TB-4) đã được sửa, kiểm chứng bằng đọc code. Rebuild sửa trạng thái PO, hóa đơn và lệnh lắp ráp sau khi sửa số lượng, bump version/updated_at và không đụng chứng từ đóng hoặc hủy. Role bypass RLS bị từ chối, mọi câu ghi đều lọc tenant tường minh, lỗi xóa tệp không còn chặn export, seed chặn theo allowlist APP_ENV. T-1..T-5, T-7, T-9..T-12 đã sửa, T-8 chấp nhận. Job e2e-nightly nhất quán. Concern của p21-go chấp nhận được.
Concerns/Blockers: Không có blocker. Còn mở mức Thấp: T-6 (thiếu test cho một số repair), N-1..N-5 (cần ghi runbook, assert p95 chưa đo lại). Kết luận dựa trên đọc code; kết quả gate đầy đủ do lead chạy.
```
