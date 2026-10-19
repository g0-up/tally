# Review: API gap batch (mục 1–13)

Ngày: 2026-10-18. Người review: code-reviewer. Chỉ đọc, không sửa code. Bỏ qua phần admin và audit (mục 14–16) do agent `api-gaps` đang sửa.

## Phạm vi

- Báo cáo dev: `plans/reports/fullstack-developer-261018-2040-api-gap-batch.md`.
- Migration `apps/api/migrations/000050`–`000054` (up và down).
- Code các module:
  - `internal/{debt,partner,crm,sales,kits,inventory,reporting,notification,identity,pricing}`;
  - `internal/platform/{httpx,idempotency}`;
  - `internal/app/sales_ports.go`, `internal/app/reporting_ports.go`.
- Test `internal/app/screen_reads_integration_test.go` (cả 736 dòng), cùng các đoạn liên quan trong `sales_integration_test.go`, `app_integration_test.go` và `campaign_reporting_integration_test.go`.
- Các đoạn tương ứng trong `api/openapi.yaml`.
- Phía web, chỉ để kiểm tra hợp đồng: `apps/web/src/features/inventory/**` và route của `apps/web/src/app`.

## Kết quả chạy test

Lệnh chạy trong `apps/api`, chỉ dùng testcontainers. Không đụng `tally-postgres-1`.

| Lệnh | Kết quả |
|---|---|
| `go test -tags integration -count=1 ./migrations/` | ok. Gồm lint migration và test reversible. |
| `go test -tags integration -count=1 ./internal/{debt,partner,crm,kits,sales,notification,platform/idempotency,pricing,identity,reporting,inventory}/` | ok, tất cả 11 package. |
| `go test -tags integration -count=1 -run '<các test mục 1–13>' ./internal/app/` | **Không compile được.** Lỗi nằm ở `internal/admin/http.go:228, 390, 496, 548, 633, 638` (sai chữ ký hàm, thiếu `httpx.Page`). Đây là phần `api-gaps` đang sửa dở (mục 14–16), không phải lỗi của mục 1–13. Vì vậy chưa chạy lại được các test HTTP của mục 1–13; báo cáo dev ghi là các test này đã qua trước đó. |

Sau khi chạy, chỉ còn container Ryuk reaper, và container này tự dừng.

## Đánh giá chung

Phần backend của mục 1–13 làm chắc:

- Mọi query đọc mới đều chạy trong tenant tx, qua `s.read(scope)` hoặc `WithTenantTx`.
- Không có N+1:
  - `customers` dùng subquery tương quan trong một query;
  - `paid_amount` gọi `InvoicesPaid` một lần cho mỗi trang;
  - preview dòng điều chỉnh đọc gộp bằng `row_number()`.
- Mọi khoá cursor đều NOT NULL (`posted_at`, `created_at`) và có `id` làm tie-break.
- Tiền dùng `money.Money`/decimal.

Có **một lỗi High**: hợp đồng `reservations:release` đổi, nhưng web hiện tại chưa theo. Các phát hiện còn lại là Low: thiếu test hoặc chênh lệch nhỏ trong spec.

## High

### H1. `POST /reservations/{id}:release` bắt buộc `reason`, nhưng web vẫn gửi body rỗng

- **Vị trí:**
  - API: `apps/api/internal/inventory/reservation.go:393-397` trả 422 `reason: required` khi reason rỗng.
  - Web: `apps/web/src/features/inventory/api/mutations.ts:134` gửi ``client.post(`/reservations/${v.id}:release`, {}, …)``.
  - Dialog: `apps/web/src/features/inventory/containers/release-reservation-dialog.tsx:30` gọi `release.run({ id: reservation.id })`. Lý do người dùng đã nhập (dòng 21–28) bị bỏ đi. Comment ở dòng 17 cũng ghi: "the release request has no field for it yet".
- **Kịch bản lỗi:** Nếu deploy API này cùng web hiện tại, mọi thao tác "Giải phóng" hàng giữ trên màn kho đều trả 422. Người dùng đã nhập lý do mà vẫn thấy lỗi, nên không nhả được hàng giữ thủ công nào.
- **Đề xuất:** Giao API và web trong cùng một đợt. Phía web:
  - đổi `mutationFn` thành `(v: { id: string; reason: string; qty?: string }) => client.post(..., { reason: v.reason, qty: v.qty }, …)`;
  - trong dialog, gọi `release.run({ id, reason: reason.trim() })`;
  - chạy lại `gen:api`;
  - map lỗi field `reason` (`required`, `too_long`) vào ô nhập.

  Nếu web chưa giao kịp, có thể tạm cho `reason` là tuỳ chọn ở API. Nhưng như vậy là đi ngược quyết định "bắt buộc lý do", nên cần lead hoặc sản phẩm chốt.

## Medium

Không có.

## Low

### L1. `release_reason` vẫn giữ lý do thủ công sau khi hàng giữ tự hết hạn

- **Vị trí:** `apps/api/internal/inventory/reservation.go:350-380`. `ReleaseExpiredReservations` không động tới `release_reason`.
- **Kịch bản:** Nhả tay một phần với lý do "Khách lấy bớt". Phần còn lại sau đó hết hạn và job tự nhả. Hàng giữ chuyển sang `released`, nhưng `release_reason` vẫn là "Khách lấy bớt". Màn hình hiện lý do đó như thể đây là lý do của lần nhả cuối.
- **Ghi chú:** Spec có ghi rõ ý nghĩa này: `openapi.yaml:12848` là "Why the hold was last released by hand.", và comment ở `reservation.go:389` cũng vậy. Nên đây không phải bug so với spec, mà là chỗ UI dễ hiểu sai.
- **Đề xuất:** Một trong hai cách:
  - web chỉ hiện `release_reason` kèm nhãn "lần nhả tay gần nhất", và lấy lý do hết hạn từ audit `reservation.expire`;
  - hoặc job ghi một lý do cố định khi tự nhả, ví dụ "Hết hạn giữ".

### L2. Spec 409 của release thiếu `reservation_not_manual`

- **Vị trí:**
  - `apps/api/api/openapi.yaml:5121-5122` chỉ ghi `description: reservation_not_active.`;
  - code trả thêm 409 `reservation_not_manual` ở `reservation.go:407`.
- **Kịch bản:** Client sinh từ spec không biết có mã này. Khi nhả một hàng giữ thuộc hoá đơn hoặc lệnh lắp, web sẽ chỉ hiện lỗi chung chung.
- **Đề xuất:** Đổi thành `description: reservation_not_active or reservation_not_manual.`

### L3. Chưa có test round-trip cho các kiểu mới được lưu qua `idempotency.Once`

- **Vị trí:** Các kiểu mới được lưu và replay:
  - `partner.CustomerResult` (có `Warnings`), từ `internal/partner/customers.go:112`;
  - `crm.Consent` (`crm/consent.go:63`);
  - kết quả reorder (`crm/care.go:324`);
  - `debt.Statement` (`debt/statement.go:153`);
  - `debt.CollectionNote` (`debt/collection.go:130`).

  Các module khác đều có `idempotencytest.AssertRoundTrip`, ví dụ `internal/sales/idempotent_create_test.go:12` và `internal/kits/idempotent_create_test.go:12`. Hai route quote và kit dùng lại `Quote`/`Kit` nên đã có test.
- **Kịch bản:** Sau này ai đó thêm một field không serialize được, hoặc một field `json:"-"`. Khi replay, client nhận body thiếu field đó, nhưng test vẫn qua. Test tích hợp hiện chỉ so body của happy path, và không chạy nhánh có `Warnings`, tức là khi tạo khách trùng số điện thoại.
- **Đề xuất:**
  - thêm `idempotent_create_test.go` cho `partner`, `crm` và `debt`, theo đúng mẫu `AssertRoundTrip(t, idempotencytest.Filled[T](t), toDTO)`;
  - trong `TestScreenCommandsWithAnIdempotencyKeyRunOnce`, thêm một ca tạo khách trùng số điện thoại, rồi replay và so `warnings`.

### L4. Test idempotency của 7 route chưa có ca chạy đồng thời và ca "thất bại rồi thử lại"

- **Vị trí:** `apps/api/internal/app/screen_reads_integration_test.go:64-200`.
- **Ghi chú:** Phần platform đúng, và test của `internal/platform/idempotency` đã qua:
  - `Begin` dùng `INSERT … ON CONFLICT DO NOTHING` rồi `FOR UPDATE`, nên request trùng chạy đồng thời sẽ chờ request đầu xong rồi replay;
  - `Complete` chỉ lưu key khi trả 2xx.

  Cả 7 route đều gọi `Once` bên trong tenant tx, và đều replay trước bước kiểm tra If-Match, nên rủi ro thấp.
- **Kịch bản chưa có test chứng minh:** Ví dụ `POST /statements` lần đầu trả 422, rồi client sửa body và gửi lại với cùng key. Kỳ vọng là 201, không phải 409. Hiện chưa có test nào khẳng định điều này ở tầng route.
- **Đề xuất:** Thêm một ca chung: lần đầu gửi body lỗi (422), sau đó gửi body đúng với cùng key và kỳ vọng 201.

### L5. Test của mục 10–12 chưa phủ các nhánh đảo, huỷ và cận biên

- **Vị trí:** `screen_reads_integration_test.go`:
  - release ở 499-517: chưa test `too_long`, reason chỉ toàn khoảng trắng, nhả một phần có `qty` kèm reason, và `reservation_not_manual`;
  - season ở 625-639: chỉ có thu một phần, chưa có ca huỷ hoá đơn hoặc đảo phiếu xuất (`stock_out.go:741` trừ `delivered_amount`);
  - `sales_integration_test.go:1520-1530` (`paid_amount`): chưa có ca đảo hoặc hoàn phiếu thu.
- **Ghi chú:** Mình đã đọc code và thấy cả ba chỗ đều đúng logic:
  - `InvoicesPaid` trừ refund và trừ dòng settlement âm của bút toán đảo, đồng thời bỏ qua chứng từ đã đảo (`internal/app/sales_ports.go:336-375`);
  - obligation bị đảo nhận một settlement bằng đúng `amount`, nên `status` chuyển sang `settled` và tự rơi khỏi `receivable` của mùa.

  Nhưng chưa có test giữ các tính chất này.
- **Đề xuất:** Thêm 3 ca ngắn:
  - đảo một phiếu thu đã phân bổ cho hoá đơn, rồi kiểm tra `paid_amount` giảm lại;
  - đảo phiếu xuất của hoá đơn mùa, rồi kiểm tra `open_orders` tăng lại;
  - release với reason dài 501 ký tự, kỳ vọng `too_long`.

## Đã kiểm và đạt (không phải phát hiện)

### RLS và tenant

Mọi read mới chạy trong tenant tx:

- `debt/lookups.go`;
- `statement.go:403-450`;
- `partner/repo.go:124-188` (`last_purchase_at`, consents);
- `reporting/revenue.go:83-88` (`customer_ids`);
- `dashboard.go`, `season.go:136-147`;
- `inventory/adjustment.go`, `count.go:371-388`, `queries.go:96-143`;
- `kits/repo.go:444-462`;
- `reporting/export.go:228-247`.

`GET /export-jobs` lọc theo `requested_by = actor.ID`, đúng với spec "The signed-in user's export jobs" (`openapi.yaml:10020`). Báo cáo dev ghi "job của tenant hiện tại", nhưng thực tế code còn hẹp hơn, chỉ lấy job của người dùng hiện tại.

### Idempotency

- Cả 7 route dùng `Once`, nên replay trả đúng body và ETag lần đầu.
- Body khác trả 409 `idempotency_key_reused`, key sai định dạng trả 422.
- Route reorder có body tuỳ chọn và xử lý đúng: dùng `BindJSONKeyed` khi có body, `OptionalIdempotencyKey(c, nil)` khi không có (`crm/http.go:318-325`).

### Index

- `doc_no` dùng các unique index `(tenant_id, doc_no)` đã có sẵn.
- `unallocated` dùng `ix_payments__posted`.
- `uncolored` dùng partial index `ix_lot_balances__sku_color (tenant_id, sku_id, color_id) WHERE qty > 0` (migration 000010).
- Stock counts dùng `ix_stock_counts__created_at`.
- Export jobs dùng `ix_export_jobs__requested_by`.
- `open_orders` và `receivable` của mùa dùng index mới ở 000054.
- Tìm `q` của disassemblies dùng `ILIKE` với wildcard đã escape (`kits/repo.go:210-213`), giống `listKits`/`listOrders`. Bảng này nhỏ và đã có `LIMIT`, nên chấp nhận được.

### Migration

- 000050–000054 đều có file down.
- Không có trigger, function, view hay DO block.
- Index đều bắt đầu bằng `tenant_id`.
- Không dùng `CONCURRENTLY`. Đây là quy ước của cả repo: chạy `CREATE INDEX` thường bên trong tx của migration.
- Tên và comment không chứa mã phase hay mã finding.
- Test lint và test reversible đều qua.

### Số liệu

- Số dư statement lấy từ version hiện hành (`statement_versions v ON v.version_no = st.current_version`).
- `by_category` dùng cùng bộ lọc với `cashTotals` và bỏ `opening`.
- `next_due` là hạn sớm nhất của phần chưa trả. Spec có ghi rõ "may be past" (`openapi.yaml:15371`).
- `open_orders` dùng `GREATEST(total - delivered, 0)` trên các hoá đơn `confirmed`/`partially_delivered`.

### Hợp đồng

- Mọi `action_path` của notification đều là route web có thật, và đúng tên query param:
  - `/debts/{receivables|payables}?party=`;
  - `/inventory/stock-card?sku=`;
  - `/inventory/lots`;
  - `/sales/pos?exceptions=open`;
  - `/debts/statements?id=`;
  - `/settings/subscription`.
- `doc_no` được upsert qua `EXCLUDED.doc_no`. `source_doc_no` có ràng buộc NOT NULL (migration 000020), nên scan không bị lỗi NULL.
- `server_time` dùng `clock.LoadLocation(tenant.Timezone)` (`identity/sessions.go:193-197`), và có test.
- Spec đã có các field mới: `customer_ids`, `unallocated`, `wholesale_price`, `min_stock`, `release_reason`, `variance_lines`, `conflicts_with_uncolored`.

### Kit

- `wholesale_price` âm hoặc `min_stock` âm trả 422 `must_not_be_negative`.
- Combo có `min_stock` trả `not_allowed`.
- Giá sỉ nháp được ghi qua `PriceWriter.setDraftPrice`, không ghi lịch sử; lịch sử bắt đầu khi publish (`RecordDraftPrices`).

### Kiểm mù

`variance_lines` bị ẩn khi phiếu kiểm mù đang đếm (`inventory/dto.go:519-527`). Có cả unit test và integration test.

### Mục 13

`TestPurchaseOrderDraftEdits` và `TestAssemblyOrderReplanning` là test thật. Chúng kiểm tra 428, 412, 422, nội dung sau khi sửa, và 409 sau khi chốt hoặc giữ hàng.

## Việc cần làm theo thứ tự

1. **(High)** Sửa web để gửi `reason` khi nhả hàng giữ, và giao cùng đợt với API (H1).
2. Thêm `reservation_not_manual` vào mô tả 409 của release (L2).
3. Thêm test round-trip cho 5 kiểu mới lưu qua `Once`, và ca replay có `warnings` (L3).
4. Thêm các ca test đảo, huỷ và cận biên (L4, L5).
5. Sau khi `api-gaps` sửa xong `internal/admin`, chạy lại `go test -tags integration -count=1 ./internal/app/` để xác nhận mục 1–13 vẫn qua.

## Plan follow-up

- Mục 1–13 khớp với báo cáo dev. Riêng hợp đồng release cần web theo kịp (H1).
- Phần "Không làm" của mục 10 (chi phí vận hành) và mục 13 (bỏ khớp chứng từ NCC) đúng là quyết định sản phẩm, đúng như báo cáo dev nêu.

Status: DONE_WITH_CONCERNS
Summary: Backend mục 1–13 đúng về RLS, idempotency, index, phân trang và cách tính tiền. Có 1 lỗi High: `reservations:release` giờ bắt buộc `reason`, nhưng web vẫn gửi `{}`, nên mọi lần nhả hàng giữ sẽ trả 422. Có 5 phát hiện Low về test và spec.
Concerns/Blockers: Package `internal/app` hiện không compile được, do `internal/admin/http.go` đang được `api-gaps` sửa dở (mục 14–16). Vì vậy chưa chạy lại được các test HTTP của mục 1–13. Các package module và migration đều đã chạy và qua.
