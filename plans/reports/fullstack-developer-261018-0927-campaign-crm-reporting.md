# Báo cáo triển khai: Campaign, CRM, Thông báo, Báo cáo, AI Q&A, Print payloads (backend)

- Plan: `plans/261016-0854-tally-full-product-implementation/phase-12-campaign-crm-notifications-reporting.md`
- Phạm vi: chỉ backend `apps/api`. Trong `apps/web` chỉ chạy lại `pnpm gen:api` để sinh `apps/web/src/shared/api/schema.d.ts`.
- Trạng thái: **DONE_WITH_CONCERNS**. Mọi gate đều xanh. Còn hai Todo chưa tick: Loyalty (redeem chưa nối vào checkout) và RPT-02..08 (chưa chạy `EXPLAIN` trên load dataset).

## Kết quả

Đã làm đủ backend cho campaign và checklist, attribution, demand tracking, CRM (timeline, consent, care task, re-order draft), loyalty, notification (registry, producer, consumer, sweeper), reporting (Period, registry, dashboard, RPT-02..10, saved report, export CSV bất đồng bộ), AI Q&A có gate, và print payloads cho PO/PN/XT/KK/DC/DS/kit label. Trên file phase đã tick 12/14 Todo và thêm một đoạn Evidence. Không commit và không sửa `plan.md`.

## File chính

- Package mới hoặc hoàn thiện trong `apps/api/internal/`: `campaign/`, `crm/`, `notification/`, `reporting/`, `analytics/`, `printing/`. Composition và ports nằm trong `internal/app/`, wiring worker nằm trong `internal/cli/worker.go`.
- Migrations:
  - `000043_reporting_exports_and_saved_report_keys`
  - `000044_notification_resolution_source`
  - `000045_reporting_read_indexes`: index theo ngày trên `sales_returns`, `obligations`, `obligation_settlements`, `cogs_adjustments`; index theo status trên `export_jobs`; index theo thời điểm tạo trên `analytics_queries`.
  - Không dùng trigger, function, view hay extension.
- OpenAPI đã cập nhật cho mọi route mới, contract test xanh.
- Unit test:
  - `campaign/campaign_test.go`: validate, chiếu demand và tìm ngày thiếu đầu tiên, summary.
  - `crm/care_test.go`: re-order draft.
  - `notification/producer_test.go`: registry đủ kind, đăng ký trùng thì panic, states.
  - Có sẵn trước đó: `reporting/reporting_test.go`, `analytics/qa_test.go`, `printing/registry_test.go`.
- Integration test:
  - `internal/app/campaign_reporting_integration_test.go`:
    - attribution không đếm trùng;
    - dashboard loại khách lẻ và nợ đầu kỳ;
    - đẳng thức NXT;
    - doanh thu tính theo BH chứ không theo tổng SO;
    - nhãn margin tạm tính;
    - `below_min_stock` dedupe qua nhiều lần sweep và được hệ thống resolve;
    - consent chặn email;
    - loyalty bị đảo khi trả hàng, điều chỉnh tay, và `insufficient_points`;
    - export: cancel, lần cancel thứ hai trả 409, hết hạn trả 410;
    - cô lập tenant cho report và export;
    - AI tắt trả 403 `feature_disabled`; AI bật thì có citation và ghi một dòng `analytics_queries`;
    - print payloads.
  - `internal/app/campaign_care_integration_test.go`:
    - checklist, demand và close campaign; campaign đã close trả 409 `campaign_closed`, nhưng checklist vẫn sửa được (đúng thiết kế);
    - từ timeline sang care task, rồi last-season, rồi re-order draft có dòng `discontinued`;
    - saved report: validate, cô lập theo store, xóa trả 204;
    - gợi ý mua và gợi ý xả hàng;
    - dự báo dòng tiền.

## Gates (chạy lại lần cuối, 2026-10-18)

| Gate | Kết quả |
|------|---------|
| `gofmt -l .` | rỗng |
| `go vet ./...` và `go vet -tags integration ./...` | pass |
| `golangci-lint run --build-tags integration ./...` | 0 issues |
| `go test -race -count=1 ./...` | toàn bộ ok |
| `go test -tags integration -count=1 ./...` | toàn bộ ok, gồm OpenAPI contract |
| `corepack pnpm gen:api` + `corepack pnpm typecheck` (apps/web) | đã sinh lại, typecheck sạch |
| `docker ps --filter label=org.testcontainers=true` | rỗng |

Không động tới `tally-postgres-1`, MinIO dev hay `make migrate-up`. Chỉ dùng testcontainers và object store in-memory.

## Hạn chế và lệch so với spec

1. **Loyalty redeem chưa nối vào checkout.** Đã có `crm.Service.Redeem`, nhưng sales checkout chưa có nguồn giảm giá cấp chứng từ hay nguồn loyalty. Muốn nối thì phải đổi sales, pricing, posting và contract checkout, mà phần này chồng với POS của phase web đang chạy song song. Vì vậy Todo Loyalty để là partial.
2. **Chưa chạy `EXPLAIN` trên load dataset NFR-04 và chưa đo p95.** Index được rà bằng cách đọc predicate của các query (migration 000045). Export 200 000 dòng được thu nhỏ xuống dataset nhỏ, chưa chạy ở quy mô tải. Todo RPT-02..08 để chưa tick.
3. Chưa có integration test cho quyền đọc của support. `Scope.ReadOnly` đã được set từ principal support nhưng chưa được kiểm thử end-to-end.
4. Tên màu của invoice version được đọc live khi in, không đóng băng vào snapshot.
5. Dòng của chứng từ mua có thuế bằng 0; thuế nằm ở cấp chứng từ (`Totals.Tax`), đúng với mô hình dữ liệu hiện có.
6. Việc dashboard loại khách lẻ dựa vào cấu trúc dữ liệu: khách lẻ không có công nợ theo party. Không có cờ lọc riêng.
7. AI Q&A luôn giữ lại các cột thông tin cá nhân, không có cấu hình để mở. Env của AI provider chưa được thêm vào `infra/`, vì infra nằm ngoài phạm vi sở hữu; provider tự tắt khi env trống.
8. Trong test margin, recost run được insert thẳng vào DB vì preview không cho kết quả xác định.
9. Trong test, export runner được gọi theo từng tenant thay vì qua vòng lặp worker.

## Bước tiếp theo

- Nếu muốn đóng Todo Loyalty: thống nhất contract giảm giá cấp chứng từ ở checkout với phase POS/web, rồi gọi `crm.Service.Redeem` trong posting.
- Chạy `EXPLAIN` và đo p95 trên dataset NFR-04 (đã dự kiến ở phase 21), đồng thời chạy export 200 000 dòng NXT.

## Câu hỏi còn mở

- Redeem điểm loyalty ở checkout có thuộc phase này không, hay chuyển sang phase POS hoặc pricing?
