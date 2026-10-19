# Review phase 12 — Campaign, CRM, Notifications, Reporting, AI Q&A, Printing (backend)

Ngày: 2026-10-18 · Reviewer: code-reviewer · Chỉ review, không sửa source.

## Phạm vi

- Code: `apps/api/internal/{campaign,crm,notification,reporting,analytics,printing}`, wiring `internal/app` (`analytics_ports.go`, routes) và `internal/cli` (`worker.go`, `serve.go`), dispatcher `internal/platform/outbox/dispatcher.go`.
- Migrations `000043`–`000045` (up/down).
- `apps/api/openapi.yaml` (các path campaign/crm/loyalty/notifications/reports/export-jobs/analytics/print).
- Integration tests: `campaign_reporting_integration_test.go` (1010 dòng), `campaign_care_integration_test.go` (357 dòng).
- Đối chiếu: spec `phase-12-campaign-crm-notifications-reporting.md`, báo cáo dev `fullstack-developer-261018-0927-campaign-crm-reporting.md`, design docs (architecture §3.6 Idempotency-Key, database-conventions).
- Kiểm chứng chạy: `go test -race -count=1 ./internal/...` — tất cả `ok`. Integration (testcontainers) không chạy lại trong lượt review này; dựa vào kết quả dev báo và đọc test.

## Đánh giá tổng quan

Nền tảng đúng: RLS phủ toàn bộ bảng mới, support read-only bị chặn ở middleware và ở service (AI Ask), notification dedupe/system-resolve đúng theo spec, consumer replay an toàn, AI gate + giữ lại cột PII hoạt động, SQL tool là tĩnh. Không có finding Critical.

Rủi ro chính nằm ở **hiệu năng export/report trên dữ liệu lớn** (mỗi trang tính lại toàn bộ aggregate + totals) và **tính đúng của loyalty khi sự kiện outbox xử lý sai thứ tự**. Ngoài ra AI Q&A thiếu deadline tổng, và `POST /loyalty/adjustments` (ghi ledger) thiếu Idempotency-Key.

## Critical

Không có.

## High

### H1. Export/report trang hoá tính lại toàn bộ aggregate và totals ở mỗi trang — export 200k dòng là O(trang × toàn ledger)

- Vị trí:
  - `apps/api/internal/reporting/nxt.go:124-158` — câu rows dùng LATERAL per-SKU quét toàn bộ lịch sử `stock_moves` ≤ `to`.
  - `apps/api/internal/reporting/nxt.go:166-170` — câu totals `SELECT … FROM stock_moves … WHERE sm.business_date <= $2 GROUP BY sm.sku_id` chạy **ở mọi trang**, quét mọi move từ đầu lịch sử.
  - `apps/api/internal/reporting/revenue.go:152-205` (`loadSalesGroups`) — CTE aggregate chạy một lần cho rows và một lần nữa cho totals, mỗi trang.
  - `apps/api/internal/reporting/export.go:429-450` — runner gọi `runResolved` theo trang `exportPageSize = 1000` (`export.go:43`), mỗi trang một transaction riêng.
- Kịch bản lỗi: tenant có 200k SKU (hoặc 200k khách) và vài triệu `stock_moves`. Export NXT = 200 trang × (LATERAL + full-ledger GROUP BY). Mỗi câu bị chặn bởi `DB_STATEMENT_TIMEOUT` mặc định 15s (`platform/config/config.go:407`) → khi ledger đủ lớn, một trang vượt 15s, export `failed`. Nếu không vượt timeout thì tổng thời gian có thể > `exportStaleAfter = 1h` (`export.go:48`) và bị `expireExports` (`export.go:318-321`) đánh failed khi có replica worker thứ hai. HTTP list cũng chịu chi phí totals full-ledger mỗi lần bấm "trang sau". Dev đã ghi nhận chưa load-test 200k dòng; spec yêu cầu streaming 200k dòng.
- Đề xuất:
  1. Thêm cờ trong `Params` (ví dụ `SkipTotals`) — runner chỉ tính totals ở trang cuối (hoặc trang đầu), HTTP chỉ tính khi `After == nil`.
  2. Với export: chạy một transaction `REPEATABLE READ READ ONLY` và đọc bằng server-side cursor / một câu aggregate duy nhất, ghi CSV theo stream, thay vì keyset nhiều lần trên giá trị aggregate.
  3. Với NXT: tính opening một lần bằng aggregate `≤ from-1` rồi cộng moves trong kỳ; tránh LATERAL toàn lịch sử per-SKU.
  4. Thêm benchmark/integration seed ≥ 200k dòng (ít nhất cho NXT và revenue) để chốt SLO.

## Medium

### M1. Loyalty sai khi sự kiện outbox xử lý sai thứ tự

- Vị trí: `apps/api/internal/crm/loyalty.go:66-99` (`earn`), `:125-162` (`reverseReturn`), `:164-183` (`reverseSale`); dispatcher `apps/api/internal/platform/outbox/dispatcher.go` (backoff đặt `available_at = now + Backoff(attempts)` khi handler lỗi).
- Kịch bản lỗi: `sale.posted` của đơn A lỗi tạm thời (lock timeout, DB blip) → bị lùi `available_at`. Trong lúc đó `sales_return.posted` (hoặc `sale.reversed`) của A được claim và xử lý trước: `reverseReturn`/`reverseSale` gọi `earnOf` không thấy earn → no-op, message đánh processed. Sau đó `sale.posted` retry thành công → `earn` cộng **đủ** điểm cho toàn bộ đơn, không kiểm tra trạng thái đơn đã reversed hay đã có return posted. Kết quả: khách giữ điểm của hàng đã trả / đơn đã huỷ, vĩnh viễn (không có sự kiện nào kích hoạt lại reverse). Cùng kịch bản xảy ra khi chạy ≥ 2 replica dispatcher (SKIP LOCKED cho phép hai message của cùng đơn chạy song song).
- Đề xuất: cho `earn` hội tụ theo trạng thái: bỏ qua nếu `sales_documents.status` đã reversed; sau khi earn, trừ ngay phần của các `sales_returns` đã posted (gọi logic `reverseReturn` cho từng return). Hoặc ngược lại: `reverseReturn`/`reverseSale` trả lỗi (để dispatcher retry) khi đơn gốc đủ điều kiện earn nhưng chưa có earn entry. Thêm integration test xử lý return trước sale.

### M2. AI Q&A không có deadline tổng — worst case vượt WriteTimeout của server

- Vị trí: `apps/api/internal/analytics/qa.go:41` (`maxSteps = 6`), vòng `qa.go:286`; `apps/api/internal/analytics/model_client.go:88` (`http.Client{Timeout: 60 * time.Second}` cho mỗi lần gọi); `apps/api/internal/cli/serve.go:235` (`WriteTimeout: 60 * time.Second`). Không có middleware deadline request (`internal/app/routes.go` chỉ có `readyTimeout`).
- Kịch bản lỗi: model provider chậm (40s/lượt) và model dùng 3–6 tool step → handler chạy 2–6 phút. Sau 60s `http.Server` đã đóng write; client nhận lỗi kết nối thay vì 504/`ai_timeout`, nhưng goroutine vẫn tiếp tục gọi model (tốn token, giữ kết nối) cho đến khi xong; query record vẫn ghi kết quả mà người dùng không nhận được.
- Đề xuất: bọc `conv.run` bằng `context.WithTimeout(ctx, ~45s)` (nhỏ hơn WriteTimeout), giảm timeout per-call (ví dụ 20s) và truyền ctx vào request (`NewRequestWithContext`) để huỷ thật; map `context.DeadlineExceeded` sang mã lỗi rõ ràng (đã có nhánh `ctx.Err() != nil` ở `qa.go:184`) và khai báo mã đó trong OpenAPI.

### M3. Export theo nhiều transaction + keyset trên giá trị aggregate → trùng/sót dòng, totals lệch rows

- Vị trí: `apps/api/internal/reporting/export.go:429-450` (mỗi trang `runResolved` mở transaction mới); keyset trên net/margin aggregate ở `apps/api/internal/reporting/revenue.go:152-205`.
- Kịch bản lỗi: export "doanh thu theo khách" sắp xếp theo net giảm dần đang chạy ở trang 50; một hoá đơn mới post cho khách X (đã ở trang 10) làm net của X tăng — không ảnh hưởng; nhưng khách Y ở trang 60 nhận đơn mới làm net vượt cursor → Y bị **sót**; ngược lại return làm net giảm dưới cursor → khách đã xuất bị **xuất lại**. Totals ở trang cuối tính trên snapshot khác với các trang trước → tổng file ≠ tổng các dòng. Đây là file kế toán người dùng tin là nhất quán.
- Đề xuất: gộp với H1 — chạy toàn bộ export trong một snapshot `REPEATABLE READ READ ONLY` (một transaction, cursor phía server), hoặc cố định `as_of` (posted_at ≤ thời điểm tạo job) trong params đã resolve.

### M4. `POST /loyalty/adjustments` ghi ledger nhưng không có Idempotency-Key

- Vị trí: `apps/api/internal/crm/loyalty.go:319-385` (`Adjust` — `SourceID` = id mới, không có khoá dedupe); route ở `apps/api/internal/crm/http.go` (~400); OpenAPI `POST /loyalty/adjustments` không khai báo header `Idempotency-Key`.
- Kịch bản lỗi: người dùng bấm "Điều chỉnh +500 điểm" hai lần hoặc client retry sau timeout mạng → hai `loyalty_entries` kind adjust, khách được +1000. `loyalty_entries` là ledger append-only nên chỉ sửa được bằng một điều chỉnh bù thủ công. Architecture §3.6 yêu cầu Idempotency-Key cho endpoint posting/ghi ledger.
- Đề xuất: áp middleware Idempotency-Key cho route này (như các endpoint payment), khai báo header `required` trong OpenAPI, thêm test double-submit.

### M5. Migration `000043` down không đảo ngược được khi đã có dữ liệu

- Vị trí: `apps/api/migrations/000043_reporting_exports_and_saved_report_keys.down.sql` — tạo lại CHECK hẹp cho `export_jobs.status` (bỏ `'cancelled'`), giới hạn attachment cũ, `saved_reports` keys bỏ `lot_stock`/`debt_schedule`, nhưng không xử lý dữ liệu đã dùng giá trị mới.
- Kịch bản lỗi: sau khi chạy phase 12 có một job `cancelled` hoặc một saved report `lot_stock` hoặc attachment export > giới hạn cũ → `migrate down` lỗi `check constraint … is violated by some row`, rollback kẹt giữa chừng.
- Đề xuất: trong down, trước khi ADD CONSTRAINT: `UPDATE export_jobs SET status='failed' WHERE status='cancelled'`, `DELETE FROM saved_reports WHERE report_key IN ('lot_stock','debt_schedule')`, xử lý attachment vượt cỡ (hoặc thêm `NOT VALID`). Ghi rõ trong down nếu chủ đích là mất dữ liệu.

## Low

### L1. Race quota export

- Vị trí: `apps/api/internal/reporting/export.go:171-177` — `SELECT count(*) … status IN ('queued','running')` rồi INSERT, không lock.
- Kịch bản: hai request đồng thời cùng thấy `active = limit-1` → cả hai được tạo, vượt quota 1 job. Ảnh hưởng nhỏ (chỉ tải worker).
- Đề xuất: `pg_advisory_xact_lock(hashtext('export_quota:'||tenant_id))` trước khi đếm, hoặc lock dòng tenant.

### L2. Stale-running fail không an toàn khi nhiều replica worker

- Vị trí: `apps/api/internal/reporting/export.go:318-321` (`started_at < now - 1h` → failed).
- Kịch bản: replica A đang chạy export dài > 1h (xem H1); replica B chạy `expireExports` đánh failed. A tiếp tục, `stillRunning` trả false → bỏ kết quả. Với một worker thì vô hại. Đề xuất: heartbeat (`updated_at` mỗi trang trong `stillRunning`) và xét stale theo heartbeat.

### L3. Runner export/sweep tuần tự theo tenant → head-of-line blocking

- Vị trí: `apps/api/internal/cli/worker.go` (vòng `ActiveTenantIDs` cho `runExports` và `sweepNotifications`).
- Kịch bản: một tenant có export NXT 30 phút → export của mọi tenant khác chờ; sweep giờ trễ tương ứng. Đề xuất: giới hạn thời gian/số job mỗi tenant mỗi vòng, hoặc worker pool nhỏ.

### L4. File export hết hạn có thể mồ côi

- Vị trí: `apps/api/internal/reporting/export.go:347-350` — job đã chuyển `expired` và commit trước khi xoá object; nếu `Delete` lỗi, không có lần thử lại nào (vòng sau không chọn lại job đã `expired`).
- Đề xuất: xoá object trước rồi mới đổi trạng thái, hoặc giữ trạng thái trung gian / quét định kỳ object không có attachment.

### L5. `Reattribute` cho phép role primary trên receipt/payment/assembly_posting; reattribute cấp chứng từ của sales_return bị che bởi primary cấp dòng

- Vị trí: `apps/api/internal/campaign/attribution.go:220-290` (map `docTables`).
- Kịch bản: reattribute cấp chứng từ cho một return có primary cấp dòng → báo cáo campaign vẫn đọc primary cấp dòng, thay đổi không có hiệu lực mà API trả 200. Đề xuất: hoặc reattribute luôn cập nhật cả line-level primaries của chứng từ, hoặc từ chối (422) khi chứng từ có attribution cấp dòng; giới hạn `docTables` cho primary chỉ ở loại chứng từ có doanh thu theo spec.

### L6. Sai số làm tròn loyalty sau nhiều lần trả hàng một phần

- Vị trí: `apps/api/internal/crm/loyalty.go:152` — `points = min(round(earn × returnNet / saleNet), remaining)`.
- Kịch bản: earn 10 điểm, ba lần trả mỗi lần 1/3 → 3 + 3 + 3 = 9, trả hết đơn vẫn còn 1 điểm. Đề xuất: tính theo luỹ kế (`round(earn × cumulativeReturnNet / saleNet) − reversedSoFar`).

### L7. Thiếu consumer `obligation.due_changed` và `pos_sync.exception`; `stock.changed` không reconcile lot

- Vị trí: `apps/api/internal/notification/consumers.go` (spec bước 7 liệt kê hai topic này).
- Kịch bản: khách trả hết công nợ quá hạn → notification "quá hạn" vẫn mở đến lần sweep giờ kế tiếp (`worker.go`, interval 1h); POS exception đã xử lý cũng vậy. Đề xuất: đăng ký consumer cho hai topic, re-read và gọi reconcile như các consumer hiện có.

### L8. Sweep `clearAbsent` có thể clear notification consumer vừa raise

- Vị trí: `apps/api/internal/notification/service.go` (Sweep chạy một transaction cho mọi producer) + `store.go` (`clearAbsent`).
- Kịch bản: Sweep đọc snapshot trước khi consumer commit một raise mới, rồi clear nó (resolved_by='system'). Tự hồi phục ở sự kiện/sweep sau; chỉ gây nháy notification. Đề xuất: `clearAbsent` chỉ clear dòng có `updated_at < sweep_started_at`.

### L9. `PUT /loyalty/program` If-Match là tuỳ chọn

- Vị trí: `apps/api/internal/crm/http.go:343-350`. Quy ước dự án: mutation thiếu If-Match → 428. Hai admin cùng sửa thì last-write-wins. Đề xuất: bắt buộc If-Match, cập nhật OpenAPI.

### L10. Thứ tự kiểm tra trong `Ask`

- Vị trí: `apps/api/internal/analytics/qa.go:161-180` — validate câu hỏi trước gate. Tenant tắt AI gửi câu hỏi rỗng nhận 422 thay vì 403 `feature_disabled`. Không lộ dữ liệu; chỉ lệch hợp đồng. Đề xuất: kiểm tra support/model/gate trước validate.

## Edge case từ scout

- Out-of-order outbox (backoff) → M1.
- Multi-replica worker → L1, L2, M1.
- Statement timeout 15s × full-ledger scan mỗi trang → H1.
- Snapshot nhiều transaction trong export → M3.
- Walk-in key `ffffffff-…` (`revenue.go:26`) là UUID hợp lệ; `uuid.MustParse` trong cursor (`service.go:163-170`) không panic; nhóm channel không phân trang — đã kiểm, không phải lỗi.

## Đã kiểm chứng đạt (giúp hiệu chỉnh rủi ro)

- RLS `tenant_isolation` có trên mọi bảng phase 12; migration không có trigger/function/view/DO; mọi migration có down.
- Support session: middleware chặn method không an toàn; `Ask` chặn thêm ở service (`qa.go:166`).
- AI: `analytics_ai_enabled` mặc định false (`000002:38`), gate kiểm cả cài đặt và entitlement `ai_qa`; khi tắt không gọi model; tool SQL tĩnh (chỉ chọn report key + params đã validate); `toolView` giữ lại cột Personal; `analytics_ports.go` không gửi Sections (tên đối tác ở phần advances); body lỗi model không vào message; giới hạn 1 MiB response.
- Notification: upsert theo `ux_notifications__dedupe_key`; system-resolved mở lại, user-resolved giữ nguyên; `resolved_by` (000044) an toàn vì chưa có writer trước đó.
- Consumer CRM timeline/loyalty idempotent (INSERT … WHERE NOT EXISTS / ON CONFLICT DO NOTHING) với dispatcher claim một message/transaction.
- Doanh thu không đọc từ tổng SO; walk-in không thể phát sinh công nợ; demand không đếm trùng reservation.
- `DownloadURL`: 409 khi chưa xong, 410 sau hết hạn, presign TTL 5 phút; export của tenant khác trả 404 (test có).
- `go test -race -count=1 ./internal/...` xanh.

## Chất lượng test

- Có: attribution, dashboard/NXT, revenue vs SO + provisional margin, below_min dedupe + system resolve, consent + loyalty, export expiry + isolation, AI gating, print, care tasks.
- Thiếu: export nhiều trang (> 1000 dòng) và tính đúng totals; return/reversal xử lý trước sale.posted; support read-only trên các route phase 12; double-submit loyalty adjustment; AI timeout.

## Hành động đề xuất (theo ưu tiên)

1. H1 + M3: totals chỉ tính một lần; export chạy trong một snapshot với cursor server-side; thêm test/bench ≥ 200k dòng.
2. M1: cho earn hội tụ theo trạng thái đơn/return; test out-of-order.
3. M4: Idempotency-Key cho `POST /loyalty/adjustments` + OpenAPI.
4. M2: deadline tổng cho AI Q&A < WriteTimeout, timeout per-call nhỏ hơn.
5. M5: sửa down 000043 để xử lý dữ liệu giá trị mới.
6. Low: L7 (consumer thiếu theo spec), L9, L6, sau đó các mục còn lại.

## Số liệu

- Unit + race: pass (`go test -race -count=1 ./internal/...`).
- Integration: không chạy lại trong review (không động `tally-postgres-1`).
- Coverage/lint: không đo trong lượt này.

## Câu hỏi mở

- Mục tiêu SLO cho export 200k dòng (thời gian tối đa) là bao nhiêu? Quyết định cách sửa H1.
- Production có chạy > 1 replica worker không? Nếu có, L1/L2 và phần song song của M1 nâng mức.
- Màu sắc trên bản in theo `?version=` đang đọc live (dev đã ghi nhận) — có chấp nhận không, hay cần snapshot?

---

Status: DONE_WITH_CONCERNS
Summary: Phase 12 backend đúng về tenant isolation, RLS, notification dedupe và AI gating; không có Critical. Có 1 High (export/report tính lại full aggregate + totals mỗi trang, không đạt quy mô 200k dòng) và 5 Medium (loyalty sai khi outbox out-of-order, AI thiếu deadline tổng, export không nhất quán snapshot, adjustment thiếu Idempotency-Key, down 000043 không đảo được).
Concerns/Blockers: Integration test không chạy lại trong review; hiệu năng export chưa có số đo thực tế.
