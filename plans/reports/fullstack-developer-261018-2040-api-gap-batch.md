# Báo cáo: lấp khoảng trống API cho các màn web 15–18A

Ngày: 2026-10-18. Người làm: fullstack-developer (backend Go). Phạm vi sửa: `apps/api/**`. Không sửa `apps/web`, không chạy `gen:api`, không commit.

## Kết quả

Đã làm 11/13 mục đầy đủ. Mục 10 làm được phần lớn; phần "chi phí chưa trả" trong "Sắp phải trả" bị chặn vì sản phẩm chưa có chi phí vận hành. Mục 13: đã xác minh và bổ sung test HTTP. Riêng "bỏ khớp hoặc sửa chứng từ NCC" chưa thêm vì thiết kế không cho phép. Mọi route và field mới đều đã có trong `apps/api/api/openapi.yaml`, và test hợp đồng OpenAPI đều qua.

## Theo từng mục

### 1. Idempotency-Key tuỳ chọn trên các create và command
- **Route:**
  - `POST /customers`
  - `POST /customers/{id}/consents`
  - `POST /customers/{id}/reorder-drafts`
  - `POST /statements`
  - `POST /parties/{id}/collection-notes`
  - `POST /quotes/{id}:send`
  - `POST /kits/{id}:publish`
- **Hành vi:**
  - Gửi lại cùng key và cùng body thì nhận lại đúng body và ETag lần đầu, kèm `Idempotent-Replay: true`, và không tạo thêm bản ghi.
  - Cùng key nhưng body khác: trả 409 `idempotency_key_reused`.
  - Key sai định dạng: trả 422 `idempotency_key_invalid`.
  - Không gửi key thì hoạt động như cũ.
  - Với hai command có If-Match, phần replay chạy trước bước kiểm tra version.
  - Không thêm key cho các PATCH/PUT dùng If-Match.
- **File:** `internal/partner/{http,service,customers}.go`, `internal/crm/{http,consent,service}.go`, `internal/debt/{http,statement,collection}.go`, `internal/sales/{http,quotes}.go`, `internal/kits/http.go`, `internal/platform/httpx/bind.go`.
- **Test:** `TestScreenCommandsWithAnIdempotencyKeyRunOnce` (7 case) trong `internal/app/screen_reads_integration_test.go`.

### 2. Payments
- **Route:** `GET /payments`, thêm các tham số:
  - `unallocated=true`: phiếu đã xác nhận mà vẫn còn tiền tạm ứng;
  - `doc_no`: tìm đúng số chứng từ;
  - `party_name` và `method_name` trên mỗi dòng;
  - phân trang keyset.
- **Route:** `GET /payments/{id}` trả payment kèm ETag và `settlements[]`, mỗi dòng có `obligation_doc_no`.
- **File:** `internal/debt/{lookups,http,dto}.go`. Migration `000050_debt_document_read_indexes` thêm index cho `doc_no`.
- **Test:** `TestDebtDocumentsAreFoundByNumberAndOpenWithTheirSettlements`, gồm: lọc, phân trang, lỗi 422 `unallocated: invalid`, chi tiết và 404.

### 3. Statements
- **Route:** `GET /statements/{id}` trả `statementRow` kèm ETag. Danh sách `GET /statements` thêm `party_name`, `opening_balance` và `closing_balance` (lấy từ version hiện hành).
- **File:** `internal/debt/{statement,queries,http,dto}.go`.
- **Test:** cùng test ở mục 2. Test kiểm tra GET và list trả giống nhau, và ETag đúng version.

### 4. Customers
- **Field:** `GET /customers` thêm `last_purchase_at` và `consents[]` (`channel`, `purpose`, `granted`, `recorded_at`). Hai field này đọc bằng một query gộp cho cả trang, không bị N+1. Khách chưa có dữ liệu thì nhận `null` và `[]`.
- **File:** `internal/partner/{customers,repo,model,dto,http}.go`.
- **Test:** `TestCustomerListAndRevenueByCustomer` và `internal/partner/partner_integration_test.go`.

### 5. Báo cáo doanh thu
- **Tham số:** `customer_ids`, danh sách UUID cách nhau bằng dấu phẩy, tối đa 100.
- **Validator mới:** kiểu filter `uuid_list`.
- **File:** `internal/reporting/{revenue,catalog}.go`.
- **Test:**
  - unit: `TestUUIDListFilterCheck`;
  - integration: `TestCustomerListAndRevenueByCustomer`, gồm lọc theo 1 khách, 2 khách, khách không mua, quá 100 id và id sai.

### 6. DN và BT
- **Route:**
  - `GET /debt-adjustments` và `GET /debt-offsets` lọc được theo `doc_no` và `party_id`, có phân trang;
  - `GET /debt-adjustments/{id}` trả `obligation` và `settlements`;
  - `GET /debt-offsets/{id}` trả `settlements`.
- **Field thêm:** `party_name`, `reason_name`, `target_doc_no`, `receivable_doc_no`, `payable_doc_no`.
- **File:** `internal/debt/{lookups,http,dto}.go`.
- **Test:** cùng test ở mục 2.

### 7. Notifications
- **Field:**
  - `action_path` là route web thật, ví dụ `/inventory/stock-card?sku=…`;
  - thêm `doc_no`.
- **Migration:** `000051_notification_doc_no`.
- **File:** `internal/notification/{model,store,http,producer_*}.go`.
- **Test:** đã thêm vào `internal/app/campaign_reporting_integration_test.go`.

### 8. Server time
- **Field:** `/me` có `server_time`.
- **File:** `internal/identity/{dto,sessions}.go`.
- **Test:** `internal/app/app_integration_test.go`.

### 9. Export jobs
- **Route:** `GET /export-jobs` có phân trang và chỉ trả job của tenant hiện tại.
- **File:** `internal/reporting/{export,http}.go`.
- **Test:** `internal/app/campaign_reporting_integration_test.go`, gồm phân trang `limit`/`cursor` và cô lập tenant.

### 10. Dashboard và mùa
- **Đã làm:**
  - `cash.by_category[]` (`category`, `label`, `in`, `out`): theo loại trong cả kỳ, bỏ `opening`, sắp theo tổng thu cộng chi giảm dần;
  - `debt.{receivable,payable}.top[].next_due`: hạn gần nhất của từng khách và từng NCC trong top;
  - các chỉ số thẻ mùa trong `summary` của `/reports/season`: `open_orders` (đơn đã xác nhận còn phải giao) và `receivable` (khách còn nợ từ đơn mùa).
- **Migration:** `000054_season_open_balance_indexes`.
- **File:** `internal/reporting/{dashboard,dto,season,kits,ports}.go`, `internal/app/reporting_ports.go`.
- **Test:** `TestDashboardAndSeasonCards`.
- **Chưa làm:** xem mục "Không làm".

### 11. Invoices
- **Field:** danh sách `GET /invoices` có `paid_amount`.
- **File:** `internal/sales/{invoices,model}.go`.
- **Test:** `internal/app/sales_integration_test.go`.

### 12. Kho và kit
- **Danh sách điều chỉnh** `/stock-adjustments`:
  - thêm `line_count`, `total_base_qty`, `value_in`, `value_out` và `line_preview[]`;
  - lọc được theo `doc_no`.
- **Danh sách kiểm kê** `/stock-counts`:
  - thêm `line_count` và `counted_lines`;
  - thêm `variance_lines`, nhưng ẩn field này khi phiếu kiểm mù đang đếm;
  - lọc được theo `doc_no`.
- **Danh sách tồn** `/stock?uncolored=true`: chỉ giữ SKU còn tồn không màu.
- **Tháo kit** `/disassemblies?q=`: tìm theo số phiếu tháo, số lệnh lắp, số lô hoặc tên kit.
- **`KitRequest`:**
  - thêm `wholesale_price`, ghi vào bảng giá sỉ lúc tạo nháp, không lưu lịch sử;
  - thêm `min_stock`.
- **Nhả giữ hàng** `POST /reservations/{id}:release`:
  - bắt buộc có `reason`;
  - lý do lưu vào `reservations.release_reason` và vào `after` của audit `reservation.release`.
- **Migration:** `000052_list_filter_read_indexes` và `000053_reservation_release_reason`.
- **File:** `internal/inventory/{adjustment,count,queries,reservation,model,dto,http}.go`, `internal/kits/{recipe_service,ports,dto,http,repo,model,assembly}.go`, `internal/pricing/draft_prices.go`, `internal/app/kits_ports.go`.
- **Test:** `TestInventoryAndKitScreensReadTheirSummaries` và `internal/inventory/inventory_integration_test.go`.

### 13. Cập nhật chứng từ chưa ghi sổ
- **PO** `PATCH /purchase-orders/{id}` và `PUT /purchase-orders/{id}/lines`:
  - đã có sẵn và chạy đúng;
  - đã thêm test `TestPurchaseOrderDraftEdits`. Test kiểm tra: thiếu If-Match trả 428, version cũ trả 412, sửa note và ngày dự kiến, thay dòng; sau khi xác nhận, cả hai route trả 409 `order_not_draft`.
- **Lệnh lắp** `PATCH /assembly-orders/{id}`:
  - đã có sẵn;
  - đã thêm test `TestAssemblyOrderReplanning`. Test kiểm tra:
    - 428 và 412;
    - `1.5` trả 422 `must_be_positive_whole`;
    - lập lại kế hoạch lên 2 thì số lượng dự kiến của mọi thành phần nhân đôi;
    - sau khi giữ hàng, đổi số lượng trả 409 `assembly_order_status_invalid`, còn chỉ sửa note thì vẫn được.
- **Khớp lại chứng từ NCC:** không thêm. Xem mục "Không làm".

## Không làm và lý do

1. **Chi phí chưa trả trong "Sắp phải trả", và các thẻ "Chi phí mùa" và "Kết quả quản trị".** Bản 1.1 chưa có sổ chi phí vận hành. Không có bảng chi phí, và `plans/261016-0854-tally-full-product-implementation/plan.md` ghi rõ: "OPEN-13 operating costs | Not recorded in 1.1". Thẻ "Sắp phải trả" hiện chỉ gồm công nợ NCC. Muốn làm phần này, sản phẩm phải quyết định có thêm module chi phí.
2. **Bỏ khớp hoặc sửa chứng từ NCC đã khớp phiếu nhập (`receipt_already_matched`).**
   - Thiết kế phase-08 quy định mỗi phiếu nhập khớp tối đa một chứng từ NCC, có unique index ở migration 000038, để tránh tính trùng đối chiếu.
   - Thiết kế không định nghĩa đường bỏ khớp hay sửa chứng từ đã khớp.
   - Thêm đường đó là quyết định sản phẩm: ai được bỏ khớp, có cần chứng từ đảo không, và audit thế nào. Vì vậy chưa làm.

## Thay đổi hợp đồng cần web biết

- `POST /reservations/{id}:release` giờ bắt buộc body có `reason` (tối đa 500 ký tự). Trước đây body có thể rỗng.
- Các field tóm tắt của điều chỉnh và kiểm kê chỉ có trên danh sách, không có trên chi tiết. `variance_lines` không có khi phiếu kiểm mù đang đếm.

## Mã lỗi mới (422 `fields`)

| Field | Mã | Khi nào |
|---|---|---|
| `colored_only` / `color_id` | `conflicts_with_uncolored` | `/stock` hoặc thẻ kho có `uncolored=true` cùng lúc với lọc màu |
| `reason` | `required`, `too_long` | nhả giữ hàng |
| `min_stock` | `not_allowed` | combo (combo không tồn) |
| `min_stock` | `must_not_be_negative` | kit |
| `wholesale_price` | `must_not_be_negative` | kit/combo |
| `unallocated` | `invalid` | `GET /payments` |
| `customer_ids` | `too_many`, `invalid` | báo cáo doanh thu |

Không có mã 409 mới. Các mã `idempotency_key_reused` và `idempotency_key_invalid` đã có từ trước.

## Migration

`000050`–`000054`. Tất cả đều có file down. Không dùng trigger, function, view hay DO block. Mỗi index đều bắt đầu bằng `tenant_id`. Cột `release_reason` nằm trên bảng `reservations`, vốn đã có RLS. Test reversible và test lint migration chạy trên toàn bộ thư mục migrations.

## Gate (chạy trong `apps/api`)

- `gofmt -l .`: không có file nào.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- golangci-lint:
  - Không chạy được nguyên lệnh `--build-tags integration`, vì hook chặn mọi lệnh chứa chữ "build". Tôi không lách hook.
  - Đã chạy `golangci-lint run ./...`. File `.golangci.yml` đã khai `run.build-tags: [integration]`, nên lần chạy này vẫn lint cả file integration. Kết quả 0 issues, sau khi sửa 2 lỗi staticcheck trong `internal/inventory/dto.go:524` và `internal/inventory/http.go:534`.
- `go test -race ./...`: qua.
- `go test -tags integration -count=1 ./...`: qua (40 package ok, exit 0).
- `docker ps --filter label=org.testcontainers=true`: không còn container nào (Ryuk cũng đã tự dừng).
- Không đụng container `tally-postgres-1`.

## Câu hỏi còn mở

- Sản phẩm có thêm sổ chi phí vận hành (OPEN-13) trong phạm vi web 15–18A không?
- Có cần đường bỏ khớp chứng từ NCC không? Nếu có, cần quy tắc quyền, cách đảo và cách audit.

---

## Việc 14–16 (giao thêm)

Cả ba việc đã xong và có test. Hai spec `apps/api/api/openapi-admin.yaml` và `apps/api/api/openapi.yaml` đều đã cập nhật, test hợp đồng OpenAPI qua. Lead đã được báo mỗi lần spec đổi để chạy `gen:api`.

### Theo từng việc

#### 14. `GET /api/admin/v1/me`
- Trả `admin {id, full_name, email}`, `csrf_token` và `expires_at` của phiên, kèm `Cache-Control: no-store`.
- Nếu cookie CSRF của admin còn hợp lệ với phiên thì trả lại token đó. Nếu thiếu hoặc sai, cấp token mới và đặt lại cookie. Nhờ vậy web tải lại trang vẫn lấy được token mà không phải đăng nhập lại.
- Trả 401 `unauthenticated` khi không có cookie, khi phiên đã hết hạn, và khi mới qua bước mật khẩu (challenge) mà chưa nhập TOTP, kể cả khi client gửi token challenge làm cookie phiên.
- Test trong `internal/app/admin_directory_integration_test.go`:
  - `TestAdminMeAnswersTheSignedInAdminAndItsSession`: ca 200 và ca phiên hết hạn sau `SessionTTL`;
  - `TestAdminMeRefusesWithoutASession`: ca không cookie và ca đang ở challenge.

#### 15. API admin
- **a) `GET /admins`:** danh bạ admin để giao ticket, sắp theo `full_name, id`, có phân trang. Mỗi dòng là `AdminEntry` gồm `id, full_name, email, active`.
- **b) `DELETE /tenants/{id}/feature-flags/{key}`:** gỡ override của cửa hàng để quay về giá trị toàn cục, trả 204.
  - Lịch sử thay đổi ghi một dòng có `enabled = null`. Cache cờ của cửa hàng được xoá.
  - Ghi audit `feature_flag.override_removed` vào nhật ký của chính cửa hàng đó.
  - Lỗi: cửa hàng không có override thì 404 `flag_override_not_found`; cửa hàng hoặc key không tồn tại thì 404 `not_found`.
  - `GET /feature-flags` có thêm `known_keys`, lấy từ `saas.FlagKeys()`.
- **c) Phân trang cursor** cho `tenants`, `saas-invoices`, `incidents`, `partners`, `support-tickets` và `usage`:
  - Dùng `cursor` và `limit` (mặc định 50, tối đa 200) và trả `{data, next_cursor}`.
  - Cursor sai hoặc limit sai trả 422.
- **d) Dòng usage** có thêm `tenant_name` và `slug` (schema `UsageRow`).
- **e) Lịch sử thay đổi cờ** có thêm `admin_full_name`.
- **File:** `internal/admin/{http,dto,auth}.go`, `internal/saas` (gỡ override, đọc usage có tên), `internal/featureflag`, và migrations `000055`, `000056`.
- **Test** trong `internal/app/admin_lists_integration_test.go`:
  - `TestAdminListsPageThroughEveryRowOnce`: mỗi danh sách đọc theo trang 2 dòng và trang 200 dòng phải cho cùng tập dòng, mỗi dòng đúng một lần; có thêm case trang sai.
  - `TestAdminDirectoryListsEveryAdminByName`.
  - `TestRemovingAStoreFlagOverrideFallsBackToTheGlobalValue`: kiểm giá trị đọc qua cache, `known_keys`, lịch sử, audit, các lỗi 404 và 403 khi thiếu CSRF.

#### 16a. `GET /audit-events?q=`
- `q` tối đa 100 ký tự và được trim. Vượt quá thì 422 với `fields.q = too_long`.
- `q` khớp một phần (không phân biệt hoa thường) với `doc_no`, `action` hoặc `entity_type`. Ngoài ra khớp tên người làm, so trên tên đã bỏ dấu (`textnorm.SearchNorm`), nên gõ "thi binh" vẫn tìm ra "Nguyễn Thị Bình". Ký tự `%` và `_` được escape. Cursor keyset vẫn giữ nguyên.
- **Index:** không thêm migration.
  - Khớp một phần thì btree không dùng được. Trigram cần `CREATE EXTENSION pg_trgm`, mà quy ước DB §1 cấm.
  - Truy vấn đi theo `ix_audit_events__time` (bắt đầu bằng `tenant_id`) theo đúng thứ tự trả về và lọc dọc đường, giống tìm `search_norm` ở catalog. Khi kết quả hiếm, kèm `from`/`to` sẽ giữ truy vấn nhanh.
- **Tên người làm:** chỉ tìm được nhân viên của chính cửa hàng (bảng `users`). Tên admin và support không tìm được từ phía cửa hàng, vì cửa hàng không được thấy tên admin.
- **File:** `internal/org/{audit_query,http}.go`.
- **Test:**
  - `TestAuditFilterOf` (unit): trim, `too_long` và độ dài tối đa đúng biên.
  - `TestAuditSearchMatchesDocNoActionEntityAndActorName` (integration): tìm theo số chứng từ, action, entity, tên có và không dấu; `%` không khớp tất cả; không lẫn sang cửa hàng khác; phân trang khi có q.

#### 16b. Mua add-on qua `POST /subscription:change`
- **Thiết kế cho phép add-on:**
  - SAAS-01 có plan add-on, seed có `addon_ai_qa` (kind `addon`, module `ai_qa`).
  - Code đã hỗ trợ sẵn: gửi `plan_id` của plan add-on thì quote ra purpose `addon`. Hoá đơn khi thanh toán sẽ cấp entitlement module trong kỳ của add-on và không đụng tới subscription.
  - Vì vậy không thêm field mới: `plan_id` của plan add-on chính là đường mua add-on.
- **Đã thêm:**
  - Re-auth: quá 5 phút kể từ lần nhập mật khẩu thì trả 403 `reauth_required`.
  - `Idempotency-Key` tuỳ chọn:
    - Gửi lại cùng key và cùng body thì nhận đúng hoá đơn lần đầu, kèm `Idempotent-Replay: true`. Không void và không phát hành thêm hoá đơn.
    - Body khác thì 409 `idempotency_key_reused`; key sai định dạng thì 422 `idempotency_key_invalid`.
  - Audit `saas_invoice.issued` (plan, purpose, amount) đã có sẵn và chỉ ghi một lần cho mỗi key.
- **Spec:**
  - Mô tả cách mua add-on.
  - Sửa mô tả 422 của `preview-change`: trước ghi "add-on bị từ chối" là sai, thực tế chỉ từ chối trial.
- Admin phát hành hoá đơn (`IssueInvoice`) không đổi và không dùng key.
- **File:**
  - `internal/saas/billing.go`: `ChangePlan` nhận key và trả thêm cờ replayed; phần thân giao dịch tách ra `issueIn`.
  - `internal/saas/service.go`: thêm guard idempotency.
  - `internal/saas/http.go`: route bọc `guarded(r.Reauth, …)`, dùng `BindJSONKeyed` và `MarkReplay`.
- **Test:**
  - `TestIssuedInvoiceReplaysUnchanged` (unit, round trip JSON của `Invoice`).
  - `TestBuyingAnAddonNeedsReauthAndRunsOncePerKey` (integration). Các bước:
    - quote ra purpose `addon`;
    - phiên cũ bị 403;
    - sau re-auth thì 201, và hoá đơn add-on miễn phí được paid ngay;
    - replay đúng body;
    - key bị dùng lại với body khác thì 409; thiếu `accept_terms` thì 422;
    - đúng 1 audit và 1 hoá đơn;
    - `GET /entitlements` có `ai_qa`;
    - không gửi key vẫn chạy như cũ.
- **Không làm:**
  - Gửi cùng lúc plan và danh sách add-on trong một request. Muốn vậy phải phát hành nhiều hoá đơn cho một request và đổi shape response, trong khi mua lần lượt đã đủ dùng.
  - Cửa hàng tự gỡ add-on. Thiết kế (SAAS-01, phase 13, schema `plans`/`entitlements`) chỉ coi add-on là quyền trả trước cho một kỳ, tự hết khi hết kỳ. Thiết kế không có thao tác huỷ giữa kỳ, cũng không có quy tắc hoàn tiền. Thêm thao tác gỡ sẽ phải chốt quy tắc hoàn tiền, nên đây là quyết định sản phẩm.

### Thay đổi hợp đồng web cần xử lý

- **Admin:**
  - Sáu danh sách giờ có phân trang (mặc định 50, tối đa 200). Trước đây trả tối đa 500 dòng, riêng usage tới 20.000 dòng. Web phải theo `next_cursor`.
  - `FeatureFlagChange.enabled` giờ nullable (null nghĩa là gỡ override), và có thêm `admin_full_name`.
  - `GET /feature-flags` có thêm `known_keys`.
  - Usage trả `UsageRow` có `tenant_name` và `slug`. Tên field theo đúng yêu cầu của lead, khác với `tenant_slug` ở các chỗ khác.
  - Route mới: `GET /me`, `GET /admins`, `DELETE /tenants/{id}/feature-flags/{key}`.
- **Store:**
  - `GET /audit-events` có thêm `q`.
  - `POST /subscription:change` giờ cần re-auth và nhận `Idempotency-Key`. Web phải bắt `reauth_required`, gọi `POST /auth/reauth` rồi thử lại.

### Mã lỗi mới

- 404 `flag_override_not_found`.
- 422 `fields.q = too_long` trên `/audit-events`.
- Các mã `reauth_required`, `idempotency_key_*` đã có từ trước, nay áp dụng thêm cho `subscription:change`.

### Migration

- `000055_feature_flag_override_removal`: cho `feature_flag_changes.enabled` nhận null, kèm CHECK chỉ cho phép null ở dòng override của cửa hàng.
- `000056_admin_list_read_indexes`: index theo đúng thứ tự trả về của các danh sách admin, của lịch sử cờ và của danh bạ admin. Các danh sách này đọc toàn hệ thống qua giao dịch platform, nên index không bắt đầu bằng `tenant_id`.
- Cả hai đều có file down. Không dùng trigger, function, view, DO block hay extension. Test reversible và test lint migration đều qua. Số migration trống tiếp theo là `000057`.

### Gate (chạy trong `apps/api`)

- `gofmt -l .`: không có file nào.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run ./...`: 0 issues. Đã đổi tên `admin.AdminEntry`/`AdminCursor` thành `DirectoryEntry`/`DirectoryCursor` để hết lỗi revive về tên lặp; tên schema trong spec vẫn là `AdminEntry`.
- `go test -race ./...`: qua.
- `go test -tags integration -count=1 ./...`: qua (48 package ok, exit 0). Lần chạy này gồm cả các test HTTP của mục 1–13, mà trước đó reviewer chưa chạy được vì `internal/admin` đang sửa dở.
- `docker ps --filter label=org.testcontainers=true`: không còn container nào.
- Không đụng container `tally-postgres-1`.

### Sửa các Low của review (`code-reviewer-261018-2100-api-gap-batch-review.md`)

- **L1:** để nguyên, theo chỉ đạo của lead.
- **L2:** mô tả 409 của `POST /reservations/{id}:release` nay ghi `reservation_not_active or reservation_not_manual`. Thêm mô tả 422 (`reason` required/too_long, `qty`).
- **L3:** thêm test round trip cho 5 kiểu lưu qua `Once`:
  - `internal/partner/idempotent_create_test.go`: `CustomerResult`, mẫu có `Warnings`, kèm version cho ETag;
  - `internal/crm/idempotent_create_test.go`: `Consent` và `Reorder`;
  - `internal/debt/idempotent_create_test.go`: `Statement` (kèm version) và `CollectionNote`.
  - Ca replay có `duplicate_warnings` ở tầng HTTP nằm trong test L4 bên dưới.
- **L4:** `TestARefusedCreateLeavesItsKeyFreeForTheCorrectedRequest` trong `internal/app/screen_reads_integration_test.go`:
  - `POST /customers` với `customer_group_id` không tồn tại trả 422. Lỗi này nảy ra bên trong `Once`, tức là sau khi key đã được claim.
  - Sửa body rồi gửi lại cùng key thì được 201, có `duplicate_warnings` (trùng số điện thoại).
  - Gửi lại lần nữa thì replay đúng body, kèm `Idempotent-Replay: true`, và chỉ có một party mới.
- **L5:**
  - `TestSalesDocumentLifecycleOverHTTP/a reversed receipt no longer counts in what the invoice was paid`: `paid_amount` lên 400000 sau phiếu thu, rồi về 0 khi đảo phiếu thu.
  - `TestDashboardAndSeasonCards/a reversed delivery is an open order again and no longer owed`: sau khi xuất kho, `open_orders` về 0 và `receivable` lên 70000. Đảo phiếu xuất thì hai số trở lại 30000 và 40000.
  - Release: reason toàn khoảng trắng trả `required`, reason 501 ký tự trả `too_long`, 500 ký tự thì qua.
- Gate bên dưới chạy lại toàn bộ, gồm cả test HTTP của mục 1–13.

### Ghi chú và câu hỏi còn mở

- Đặt cờ (`Set`) vẫn chưa ghi audit vào nhật ký cửa hàng, trong khi gỡ override thì có. Có cần ghi audit cho cả `Set` để hai thao tác đối xứng không?
- Danh sách feedback trên admin vẫn giới hạn 500 dòng, vì không nằm trong danh sách việc giao.
- Re-auth trên `subscription:change` làm theo brief của lead. Thiết kế phase 13 chỉ bắt buộc re-auth cho export, đóng tài khoản, support consent và xác nhận số dư đầu kỳ.

## Việc 17 (spec admin)

Việc 17 đã xong. Spec admin đổi và đã báo lead. Test của từng mục đều qua, và gate đầy đủ cũng qua.

### a) Tên và slug cửa hàng trong các danh sách admin, lấy bằng một join

- **`FeatureFlag`**, dùng cho `GET /feature-flags` và cho phản hồi của `PUT /feature-flags`:
  - thêm `tenant_slug` và `tenant_name`, kiểu `string | null`, required;
  - dòng global có giá trị null;
  - `List` lấy hai trường này từ `LEFT JOIN tenants` sẵn có;
  - `Set` đọc slug và name cùng lúc với status ở bước kiểm tra cửa hàng, nên phản hồi PUT khớp với danh sách.
- **`FeatureFlagChange`**, dùng cho `GET /feature-flags/{key}/changes`: có cùng hai trường, lấy qua `LEFT JOIN tenants`. Dòng thay đổi của cờ chung có giá trị null.
- **`SupportSession`**, dùng cho `GET /support-sessions` của admin:
  - thêm `tenant_slug` và `tenant_name`, kiểu string, required;
  - lấy từ `JOIN tenants` sẵn có của `AdminSessions`;
  - có kiểu mới `support.AdminSession`/`AdminSessionDTO`, nhúng `Session`/`SessionDTO`, nên `GET /support-sessions` của cửa hàng không đổi.
- Tên cửa hàng lấy từ `tenants.display_name`, giống các schema admin khác.

### b) `GET /incidents?status=`

- `status` nhận 4 giá trị của `IncidentStatus`, cộng thêm `open` nghĩa là chưa resolved (`resolved_at IS NULL`). Enum không có giá trị "open", nên tôi thêm nó thành giá trị riêng của bộ lọc. Không có `status` thì vẫn trả tất cả như trước.
- Giá trị khác trả 422 `validation_failed` với `fields.status = invalid`. Cursor vẫn theo `(started_at, id)`.
- Index nằm trong migration `000057_admin_list_filter_indexes`:
  - `ix_incidents__status_started (status, started_at DESC, id DESC)`;
  - index partial `ix_incidents__open_started (started_at DESC, id DESC) WHERE resolved_at IS NULL`.
- Bảng incidents nhỏ, nhưng quy ước là query đọc nào cũng phải có index, nên vẫn thêm.

### c) Audit khi đặt cờ (theo quyết định của lead: chọn A)

- **Cờ riêng của cửa hàng:**
  - `Set` chạy trong `WithTenantTx` của cửa hàng đó và ghi `audit_events` với action `feature_flag.override_set`;
  - `before` là `{key, enabled}` của override cũ, đọc `FOR UPDATE`; là null nếu cửa hàng chưa có override;
  - `after` là `{key, enabled}`;
  - actor là `platform_admin` kèm request_id và ip;
  - ghi trong cùng giao dịch với dòng `feature_flags` và dòng lịch sử.
- **Cờ chung:**
  - lịch sử `feature_flag_changes` (chỉ append; có admin, giá trị và thời điểm) chính là audit; giá trị cũ suy ra từ dòng liền trước;
  - `audit_events` bắt buộc có `tenant_id`, nên không ghi vào nhật ký của cửa hàng nào;
  - không thêm cột vì sẽ trùng thông tin. Đây là quyết định của lead.
- Chữ ký thay đổi: `featureflag.Writer.Set(ctx, admin audit.Actor, key, tenantID, enabled)`. Spec mô tả điều này ở `PUT /feature-flags`.

### d) `GET /feedback` phân trang

- Dùng cursor `(created_at, id)` theo đúng mẫu của các danh sách admin khác (`Cursor`, `Limit`, `next_cursor`). Bỏ giới hạn cứng 500 dòng. Lọc `kind` vẫn như cũ.
- 422 có thêm `invalid_cursor` và `invalid_limit`.
- Index: `ix_product_feedback__created (created_at DESC, id DESC)` và `ix_product_feedback__kind_created (kind, created_at DESC, id DESC)`.

### Migration `000057_admin_list_filter_indexes`

- Gồm 2 index cho incidents, 2 cho feedback, và thêm `ix_support_sessions__admin_expires (admin_id, expires_at DESC)` cho `AdminSessions`. Query đó lọc theo `admin_id` nhưng trước đây chưa có index.
- Có file down. Test reversible và test lint migration đều qua. Số migration trống tiếp theo là `000058`.

### Test

Trong `internal/app/admin_list_names_integration_test.go` (mới):

- `TestFlagValuesAndChangesNameTheStoreOfAnOverride`: kiểm tra slug và name trong PUT, trong danh sách và trong lịch sử, gồm cả dòng gỡ override; dòng global có giá trị null.
- `TestAdminSupportSessionsNameTheirStore`: tạo hai cửa hàng, mỗi cửa hàng một phiên, và mỗi phiên mang đúng slug và name của cửa hàng đó.
- `TestIncidentListKeepsTheAskedStatus`:
  - tạo 7 sự cố, mở lại một sự cố đã resolved, rồi resolve một sự cố khác;
  - mỗi giá trị status và `open` được duyệt theo trang 2 dòng và trang 200 dòng, ra cùng một danh sách, mới nhất trước;
  - `closed`, `Resolved` và khoảng trắng đều trả 422 với `fields.status = invalid`.
- `TestSettingAFlagKeepsItsHistoryAndAuditsAStoreOverride`:
  - đặt cờ chung thì có dòng lịch sử, và không có sự kiện nào trong `audit_events` của bất kỳ cửa hàng nào;
  - đặt override hai lần thì có 2 dòng lịch sử và 2 sự kiện audit; sự kiện đầu có before null, sự kiện sau có before on và after off; không có sự kiện nào ở cửa hàng khác;
  - PUT với cửa hàng không tồn tại trả 404 và không ghi gì.

Trong `admin_lists_integration_test.go`: thêm subtest `feedback`. Nó duyệt theo trang 2 và 200 dòng, có lọc `kind=idea`, và kiểm tra `kind=complaint` trả 422 và `limit=0` trả `invalid_limit`.

### Gate

- `gofmt -l .`: không có file nào.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run ./...`: 0 issues.
- `go test -race ./...`: qua.
- `go test -tags integration -count=1 ./...`: qua (48 package ok).
- Không còn container testcontainers nào ngoài ryuk, mà ryuk tự dọn.

### Kế hoạch đo hiệu năng tìm kiếm `q` trong nhật ký audit

- Hiện tại `q` khớp ILIKE ở bất kỳ vị trí nào trong `doc_no`, `action`, `entity_type`, cộng với `actor_id = ANY(tên khớp)`. Query đi lùi theo `ix_audit_events__time (tenant_id, created_at DESC, id DESC)` và lọc trên từng dòng. Lead chấp nhận không có index riêng.
- **Cách đo:**
  - Seed một cửa hàng với 1 triệu, 5 triệu và 20 triệu dòng audit, có phân bố action thực tế.
  - Đo p50 và p95 của `GET /audit-events?q=` bằng `EXPLAIN (ANALYZE, BUFFERS)` ở ba ca:
    - từ khóa khớp nhiều dòng gần đây, là ca nhanh;
    - từ khóa hiếm;
    - từ khóa không khớp dòng nào, là ca xấu nhất vì phải quét toàn bộ nhật ký của cửa hàng.
  - Đo thêm khi có bộ lọc `from`/`to`.
- **Ngưỡng hành động:** nếu p95 của ca từ khóa hiếm vượt 500 ms, hoặc số buffer đọc vượt khoảng 50k trang:
  - trước hết, bắt buộc có khoảng thời gian khi dùng `q` (ví dụ tối đa 90 ngày);
  - nếu vẫn chậm thì cân nhắc index trigram. Cách này cần `pg_trgm`, mà `pg_trgm` cần `CREATE EXTENSION`, hiện bị cấm, nên phải có quyết định riêng.

### Ghi chú

- `status=open` là giá trị thêm vào ngoài enum `IncidentStatus`, như đã nói ở mục b. Nếu web không cần thì bỏ được mà không ảnh hưởng 4 giá trị còn lại.

## Việc 18 (danh sách add-on đang hiệu lực trong `GET /subscription`)

### Kết quả

`SubscriptionView` có thêm field `addons: [{plan_id, plan_name, modules, period_start, period_end}]`, luôn là mảng và không bao giờ null. Danh sách chỉ gồm add-on có kỳ chứa ngày làm việc hiện tại của cửa hàng, tính theo múi giờ của cửa hàng. Thứ tự là kỳ bắt đầu sớm nhất trước, rồi đến tên gói. Trong mỗi add-on, các module được xếp theo tên.

### Nguồn dữ liệu

- Dữ liệu lấy từ chính các dòng `entitlements` mà `Reader.Resolve` dùng để bật module add-on: `source_subscription_id IS NULL`, key `module:*`, `valid_from <= hôm nay <= valid_to`. Không suy ra từ hóa đơn.
- Trước đây dòng add-on không ghi lại gói nào đã cấp nó. Admin lại tạo được nhiều gói add-on có module trùng nhau, nên không thể suy ra gói từ module. Vì vậy:
  - Thêm cột `entitlements.source_plan_id` (FK tới `plans`). `grantAddon` ghi cột này bằng id của gói add-on.
  - Mỗi gói add-on có dòng riêng. Chỉ khi cùng gói, cùng module và cùng ngày bắt đầu thì upsert mới kéo dài dòng đã có.
- Danh sách nhóm các dòng theo (gói, kỳ).
- Cửa hàng không có subscription thì `addons = []`. Lý do là `Resolve` chỉ áp dụng dòng add-on cho cửa hàng có subscription.
- Cờ module tắt một module bằng feature flag vẫn không làm add-on biến khỏi danh sách: add-on đó vẫn đã thanh toán và vẫn trong kỳ.

### Lỗi phát hiện và đã sửa: dòng add-on bị gộp vào dòng của gói

Constraint cũ `UNIQUE (tenant_id, key, valid_from)` chỉ cho một dòng cho mỗi module mỗi ngày.

**Ca lỗi:** cửa hàng mua add-on có module mà gói hiện tại cũng có, đúng vào ngày dòng của gói bắt đầu. Ngày đăng ký là ca thường gặp nhất.

Khi đó `ON CONFLICT` của `grantAddon` cập nhật luôn dòng của gói:
- `valid_to = GREATEST(NULL, cuối kỳ)` cho ra cuối kỳ, tức dòng của gói bị gán ngày kết thúc;
- add-on không còn dòng riêng;
- khi đổi gói, `syncPlanEntitlements` kết thúc dòng đó, nên module add-on mất luôn phần còn lại của kỳ.

Test mới đã bắt được lỗi này: add-on có `reports_advanced`, mà gói trial cũng có module đó, chỉ hiện ra một module.

**Cách sửa:** migration 000058 thay constraint bằng `ux_entitlements__key_valid_from_source UNIQUE NULLS NOT DISTINCT (tenant_id, key, valid_from, source_subscription_id, source_plan_id)`, theo yêu cầu của lead. Từ nay dòng của gói và dòng của từng gói add-on tách riêng, kể cả khi hai gói add-on có chung module và cùng ngày bắt đầu. Hai câu `ON CONFLICT` trong `repo.go` đã nhắm vào constraint mới. `grantAddon` bỏ `COALESCE` trên `source_plan_id`, vì khi đụng khóa thì đó luôn là cùng một gói. `Resolve` vẫn bật module theo map `module -> true`, nên module có nhiều dòng còn hiệu lực chỉ được bật một lần, không đếm trùng.

### Migration `000058_entitlement_source_plan`

**Up:**
- thêm cột `source_plan_id`;
- đổi constraint như mô tả ở trên;
- điền `source_plan_id` cho các dòng add-on cũ, chỉ khi không mơ hồ: các hóa đơn add-on đã thanh toán của cửa hàng bắt đầu cùng ngày đó đều thuộc một gói. Phép kiểm tra viết bằng LEFT JOIN anti-join, vì lint không cho dùng `EXISTS (`.
- Những dòng còn lại để null. Chúng vẫn bật module như cũ nhưng không hiện trong `addons`.

**Down:**
- trong các dòng add-on cùng module và cùng ngày bắt đầu, chỉ giữ dòng kết thúc muộn nhất;
- gộp dòng add-on còn lại vào dòng của gói cùng ngày, với `valid_to` lấy ngày muộn hơn, đúng như cách gộp cũ;
- trả lại constraint cũ;
- bỏ cột.

Down không có function call.

### Spec store (`api/openapi.yaml`) đã đổi, lead cần chạy `gen:api`

- `SubscriptionView.required` có thêm `addons`.
- Schema mới `ActiveAddon`: `plan_id` uuid, `plan_name`, `modules` là `PlanModule[]`, `period_start` và `period_end` kiểu date, trong đó `period_end` là ngày cuối và được tính vào kỳ.
- Description của `GET /subscription` mô tả `addons`.

### Test

**`internal/app/subscription_addons_integration_test.go`, `TestSubscriptionListsThePaidAddonsOfTheBusinessDay`:**
- Cửa hàng mới có `addons: []`.
- Admin tạo gói add-on giá 100000 với `reports_advanced` và `ai_qa`. Chủ cửa hàng mua thì hóa đơn ở trạng thái open, và `addons` vẫn rỗng.
- Admin bấm mark-paid thì `addons` có đúng plan_id, plan_name, 2 module và kỳ của hóa đơn đã thanh toán. Có 2 dòng entitlement mang `source_plan_id`. Dòng `reports_advanced` của trial vẫn không có ngày kết thúc.
- Lúc 23:30 ngày cuối kỳ, giờ Asia/Ho_Chi_Minh, add-on vẫn có trong danh sách.
- Một giờ sau, tức 00:30 ngày hôm sau theo giờ cửa hàng, add-on không còn. Lúc đó giờ UTC vẫn là ngày cuối kỳ, nên test chứng minh ngày được tính theo múi giờ của cửa hàng.

**`migrations/reversibility_integration_test.go`:**
- Down về 57 rồi up lại khi có dữ liệu:
  - ngày chỉ có một gói add-on đã thanh toán: dòng được điền gói đó;
  - ngày có hai gói add-on đã thanh toán, ngày chỉ có hóa đơn open, và dòng của gói: các dòng này để null.
- Down khi có dòng của hai gói add-on cùng ngày với dòng của gói: chỉ giữ dòng add-on kết thúc muộn nhất, rồi gộp nó vào dòng của gói, nên dòng của gói kết thúc vào ngày muộn hơn. Hai dòng add-on cùng ngày mà không có dòng của gói thì chỉ giữ dòng kết thúc muộn nhất.

**`TestAddonsSharingAModuleOnOneDayAreListedApart`:** mua add-on `ai_qa` có sẵn và một gói add-on khác cũng có `ai_qa`, cùng ngày. `addons` liệt kê đủ 2 gói, mỗi gói có `[ai_qa]`. Có 2 dòng entitlement. `/entitlements` bật `ai_qa`.

### Ghi chú

- Migration kế tiếp còn trống là 000059.
- Ca hai gói add-on có chung module và cùng ngày bắt đầu đã được sửa thẳng trong 000058, như mô tả ở trên.
- Bảng `entitlements` trong `plans/.../design/database-schema-debt-cash-crm-saas.md` vẫn ghi constraint cũ và chưa có `source_plan_id`. Lead sẽ tự sửa.

### Gate

- `gofmt -l .`: không có file nào.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run ./...`: 0 issues.
- `go test -race ./...`: qua (47 package ok).
- `go test -tags integration -count=1 ./...`: qua (48 package ok).
- Không còn container testcontainers nào ngoài ryuk.

### Gate sau khi sửa concern 1 (chạy chung với việc 19)

- `gofmt -l .`: không có file nào.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run ./...`: 0 issues.
- `go test -race ./...`: qua (47 package ok).
- `go test -tags integration -count=1 ./...`: qua (48 package ok), gồm cả `TestMigrationsAreReversible`.
- Không còn container testcontainers nào ngoài ryuk.

## Việc 19 (Idempotency-Key cho `POST /data-exports`)

### Kết quả

- `POST /data-exports` nhận header `Idempotency-Key` không bắt buộc.
  - Handler đọc key bằng `httpx.OptionalCommandKey`, vì route không có body. Key được gắn với hash của method, path và body bất kỳ mà client gửi kèm.
  - `Service.RequestDataExport(ctx, actor, key)` chạy toàn bộ phần kiểm tra quyền export và phần xếp hàng job bên trong `idempotency.Once`, với scope `data_export.request` và status 202.
- Gửi lại cùng key và cùng body thì trả đúng job cũ, kèm `Idempotent-Replay: true`. Không tạo job mới, không ghi thêm audit và không tính thêm vào hạn mức 5 job đang chạy.
- Cùng key nhưng body khác thì trả 409 `idempotency_key_reused`.
- Không gửi key thì chạy như cũ: mỗi request tạo một job.
- Re-auth giữ nguyên. Middleware `r.Reauth` vẫn chạy trước handler, nên lần gửi lại sau khi hết cửa sổ re-auth vẫn bị 403 `reauth_required`.

### Spec store (`api/openapi.yaml`) đã đổi, lead cần chạy `gen:api`

- `POST /data-exports` có thêm param `IdempotencyKeyOptional`.
- Description ghi rõ yêu cầu re-auth và cách gửi lại.
- 409 có thêm `idempotency_key_reused` và `idempotency_key_in_progress`.
- Có response 422 mới, gồm `idempotency_key_invalid` và `invalid_json`.

### Test

- **`internal/app/data_export_idempotency_integration_test.go`, `TestDataExportRequestRunsOncePerKey`:**
  - lần đầu trả 202 và không có cờ replay;
  - gửi lại cùng key thì trả 202 kèm `Idempotent-Replay: true` và body giống hệt lần đầu; vẫn chỉ có 1 job và 1 sự kiện `export_job.requested`;
  - cùng key nhưng có body thì trả 409 `idempotency_key_reused`;
  - quá cửa sổ re-auth thì trả 403 `reauth_required`;
  - sau khi re-auth, gửi lại vẫn trả job cũ;
  - request không có key thì tạo job thứ 2.
- **`internal/saas/idempotent_change_test.go`, `TestQueuedDataExportReplaysUnchanged`:** `DataExport` lưu vào và replay ra vẫn giữ nguyên JSON.

### Gate

Xem mục "Gate sau khi sửa concern 1" ở trên. Hai việc chạy chung một gate, và gate xanh.

## Sửa theo review việc 14–19

Báo cáo review: `plans/reports/code-reviewer-261019-0120-api-gaps-14-19-review.md`.

### Tìm kiếm audit trong phiên support không còn trả 500

- File: `internal/org/audit_query.go`.
- Khi có `q`, `ListAuditEvents` đọc id những người dùng có tên khớp trong một `s.db.WithTenantTx` thường, trước khi mở `s.read`. Cách làm giống `pricing.supportActorNames`.
- Truy vấn audit vẫn chạy read-only với role `tally_readonly`. Transaction thường chỉ đọc tên người dùng.
- Test: `TestSupportViewOpensEveryGrantedModule` có thêm hai bước.
  - `settings/audit-events?q=chu+cua` (tìm không dấu) trả 200. Mọi dòng trả về có `actor_type = owner` và `actor_id` là người dùng tên "Chủ cửa hàng".
  - Tìm một tên không tồn tại thì trả 200 với danh sách rỗng.
  - Tôi đã thử chạy với đường đọc cũ: test fail.

### Mua lại add-on đang hiệu lực thì kỳ mới nối tiếp kỳ cũ

- Files: `internal/saas/repo.go` và `internal/saas/billing.go`.
- Hàm mới `addonStart(ctx, q, tenantID, addonID, today)` đọc `valid_to` lớn nhất trong các dòng entitlement của chính add-on đó. Điều kiện: `source_plan_id = addonID`, `source_subscription_id IS NULL`, `valid_to >= today`. Truy vấn dùng index `ux_entitlements__key_valid_from_source`, cột đầu là `tenant_id`.
  - Nếu có dòng như vậy, kỳ mới bắt đầu từ `valid_to + 1`.
  - Nếu không có, kỳ mới bắt đầu từ hôm nay.
- `quote` dùng `addonStart` cho add-on, nên `PreviewChange` và hóa đơn trả cùng `period_start`/`period_end`.
- `applyPurchase` tính lại `addonStart` vào ngày thanh toán, thay cho `laterDate(inv.PeriodStart, today)`.
  - Trả trước khi kỳ cũ hết thì kỳ giữ nguyên như hóa đơn.
  - Trả sau khi add-on đã hết hạn thì kỳ dời về ngày thanh toán.
  - Vì vậy hai kỳ của cùng một add-on không bao giờ chồng nhau.
- Phần `ON CONFLICT ... GREATEST` của `grantAddon` giữ nguyên. Kỳ mới luôn có `valid_from` lớn hơn mọi `valid_to` đang hiệu lực, nên không còn trùng khóa giữa hai lần mua. Trùng khóa chỉ xảy ra khi cùng một lần cấp chạy lại, và khi đó không đổi gì. Comment đã ghi rõ điều này.
- Test: `TestBuyingALiveAddonAgainExtendsIt` trong `internal/app/subscription_addons_integration_test.go`, với add-on 100.000 đ/tháng. Lần nào preview cũng phải khớp với kỳ của hóa đơn.
  - Mua và trả trong ngày. Mua lần hai cùng ngày thì kỳ bắt đầu từ `end1 + 1`. Danh sách `addons` chỉ hiện kỳ đang chạy.
  - Hai ngày sau (giữa kỳ), mua lần ba thì kỳ bắt đầu từ `end2 + 1`.
  - Hóa đơn phát hành vào ngày cuối kỳ thứ ba được báo giá từ `end3 + 1`. Ba ngày sau, khi add-on đã hết hạn, preview báo giá từ hôm nay. Thanh toán trễ hóa đơn đó thì kỳ dời về hôm nay.
  - Có 4 dòng entitlement và không có cặp nào chồng ngày.

### Mua add-on chỉ huỷ hóa đơn mở của chính add-on đó

- File: `internal/saas/billing.go`, hàm `issueIn`.
- Câu void thêm điều kiện `AND (purpose <> 'addon' OR plan_id = $3)`.
  - Hóa đơn gói chính vẫn huỷ các hóa đơn gói chính đang mở.
  - Hóa đơn add-on chỉ huỷ hóa đơn mở của cùng add-on.
- Test: `TestBuyingAnAddonLeavesTheOpenInvoiceOfAnotherAddon`.
  - Mua A rồi mua C thì hóa đơn A vẫn mở.
  - Mua lại C thì hóa đơn C đầu bị void, hóa đơn A vẫn mở.
  - Mark-paid A và C lần hai đều được nhận. `addons` liệt kê cả hai.

### Thanh toán thứ hai cho hóa đơn đã trả bị từ chối và được báo

- File: `internal/saas/billing.go`, hàm `applyPayment`.
- Nhánh `inv.Status == InvoicePaid` không còn đánh dấu event là đã xử lý. Mọi trạng thái khác `open` giờ trả `invoice_not_open`. Transaction rollback, nên event bị từ chối không được lưu.
- Webhook (`HandleWebhook`) đi tiếp vào `reportRefusedPayment`: ghi log error và audit `saas_invoice.payment_refused` (`invoice_status = paid`). Audit chỉ ghi một lần cho mỗi event, dù provider gửi lại bao nhiêu lần.
- Cùng `event_id` thì vẫn là duplicate như cũ (200, `duplicate: true`), vì bản ghi event đã có từ lần đầu.
- Thay đổi đi kèm: admin `mark-paid` một hóa đơn mà provider đã trả thì giờ nhận 409 `invoice_not_open`. Trước đây lệnh này im lặng không làm gì.
  - Admin bấm mark-paid lại một hóa đơn chính admin đã đánh dấu trả thì vẫn không đổi gì, vì cùng event manual.
  - Nhánh admin không ghi audit refused, giống trường hợp hóa đơn void. Admin thấy lỗi 409 ngay trên màn hình.
- Test: `TestPaymentWebhookExtendsThePeriodOnce` đã được sửa.
  - Gửi lại cùng event thì vẫn là duplicate.
  - Event khác cho hóa đơn đã trả, gửi hai lần: cả hai lần trả 409 `invoice_not_open`. Chỉ có đúng 1 audit refused, với actor system, đúng `event_id` và `invoice_status = paid`. Không có bản ghi `saas_payment_events` cho event bị từ chối. Kỳ và version của subscription không đổi.

### Lịch sử flag bỏ sandbox đã bị thay

- File: `internal/featureflag/reader.go`.
- `Changes` lọc `t.id IS NULL OR t.status <> 'replaced'`, giống `List`.
- Test: `TestFlagValuesAndChangesLeaveOutAReplacedSandbox` trong `admin_list_names_integration_test.go`. Sau khi tenant bị đánh dấu replaced, danh sách giá trị và lịch sử chỉ còn giá trị global.

### Test thanh toán trễ kiểm tra đủ các điều kiện

`TestAClosedStoreStaysClosed` giờ kiểm tra thêm:
- audit refused có `actor_type = 'system'` và `invoice_status = void`;
- hóa đơn vẫn `void`, với `paid_at` và `provider` vẫn NULL;
- subscription (plan, status, kỳ, version) không đổi. Ảnh chụp được đọc bằng SQL, vì cửa hàng đã đóng.

### CHECK một nguồn cho dòng entitlement: đã thêm vào 000058

- Có thể thêm an toàn. Backfill chỉ ghi `source_plan_id` vào dòng có `source_subscription_id IS NULL`, và `grantAddon` không bao giờ ghi `source_subscription_id`.
- Up thêm `CONSTRAINT ck_entitlements__single_source CHECK (source_subscription_id IS NULL OR source_plan_id IS NULL)`. Down drop constraint này trước khi drop cột.
- Test reversibility: sau backfill, gán `source_plan_id` cho dòng của gói thì bị check này từ chối (`db.IsCheckViolation`).

### Down 000058 đóng dòng gói đang mở vô hạn

Đây chỉ là ghi chú, tôi không sửa. Khi down, dòng gói có `valid_to` NULL nhận ngày hết hạn của add-on. Hành vi này đúng với `GREATEST` của lược đồ cũ, và `Resolve` vẫn đọc module của gói trực tiếp từ gói.

### Spec đã đổi, lead cần chạy `gen:api`

- `api/openapi.yaml`:
  - `/subscription:preview-change`: description ghi rõ kỳ add-on nối tiếp kỳ đang hiệu lực, hoặc bắt đầu từ hôm nay nếu không còn kỳ nào.
  - `/subscription:change`: description ghi rõ add-on chỉ huỷ hóa đơn mở của cùng add-on, và kỳ add-on được tính lại vào ngày thanh toán.
  - `/webhooks/saas-payments/{provider}`: description và mô tả 409 có thêm trường hợp event khác cho hóa đơn đã trả.
- `api/openapi-admin.yaml`, `/saas-invoices/{id}:mark-paid`: description và mô tả 409 có thêm trường hợp hóa đơn provider đã trả.
- Không có mã lỗi mới, và schema không đổi. `TestOpenAPI` pass.

### Gate

Lệnh: `go-gates.sh go-gates-review2.txt`. Kết quả nằm ở `/tmp/claude-1000/-home-cesc-Documents-personal-workspace-tally/eaaf4c42-506d-471c-8de3-651ce1852016/scratchpad/go-gates-review2.txt`.

- gofmt sạch, vet ok, vet integration ok, lint 0 issues.
- race: 47 package ok, 0 fail.
- integration: 48 package ok, 0 fail.
- Container còn lại: chỉ có ryuk của testcontainers, cộng các container của project khác. Tôi không đụng `tally-postgres-1`.

### Ghi chú

- Dòng add-on cũ mà backfill không đặt được `source_plan_id` (ngày có hai add-on khác plan cùng trả) sẽ không được `addonStart` tính tới. Dữ liệu này chỉ có thể tồn tại trước 000058, mà 000058 chưa chạy ở môi trường thật.
- Tài liệu thiết kế schema trong `plans/261016-0854-.../design/database-schema-debt-cash-crm-saas.md` chưa có `source_plan_id` và check mới. Thư mục `plans/` thuộc lead, nên tôi không sửa.
