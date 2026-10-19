# Phase 12 backend: sửa finding review, loyalty redeem ở checkout, Idempotency-Key

Phần A và B xong, mọi gate đều xanh. Phần C chưa làm: sửa đúng thì phải đổi chữ ký service của bốn domain khác (sales, procurement, pricing, kits). Thiết kế đề xuất ở mục C để team-lead quyết. Không commit, không có migration mới (cao nhất vẫn là 000046), không đụng `apps/web` hay `tally-postgres-1`.

## A. Finding của review

H1, M3, M5, L1, L2 và L4 đã xong từ trước; số đo tải ghi ở cuối báo cáo.

| Finding | Cách sửa | Test |
|---|---|---|
| M1: loyalty sai khi event đến sai thứ tự | `earn` khoá khách, bỏ qua đơn đã bị đảo, ghi điểm earn rồi gọi `settleReturns` để khấu trừ ngay các phiếu trả đã post. Phiếu trả hoặc lệnh đảo xử lý trước earn thì không làm gì, vì earn đến sau sẽ tự quyết toán. | `TestLoyaltyPointsSettleWhicheverEventIsHandledFirst`: đẩy `sale.posted` xuống cuối hàng đợi. Trả trước earn cho số dư 50 (+100/−50); đảo đơn trước earn cho 0 entry. |
| M2: AI Q&A không có deadline | Deadline tổng 45s (`analytics.Deps.Deadline`, option `app.WithQuestionDeadline`) và timeout 20s cho mỗi lần gọi model (`HTTPModelConfig.CallTimeout`). Hết hạn thì câu hỏi vẫn được ghi log là chưa trả lời, rồi trả 504 `ai_timeout`. Thêm kind `apperr.Timeout`, httpx map sang 504. OpenAPI có thêm response `'504'`. | `TestAnalyticsQuestionPastItsDeadlineTimesOut` (integration) và `TestHTTPModelCallStopsAtItsTimeout` (unit) |
| M4: `POST /loyalty/adjustments` không có Idempotency-Key | Key là bắt buộc, đi qua `idempotency.Guard` với Begin/Complete trong cùng transaction. Gửi lại cùng key thì trả lại response đã lưu kèm `Idempotent-Replay: true`. OpenAPI đã cập nhật. | Subtest loyalty trong `TestCustomerConsentGatesEmailAndLoyaltyFollowsReturns`: thiếu key cho 422; gửi hai lần cùng key ra cùng id, số dư không đổi. |
| L6: làm tròn sai sau nhiều lần trả một phần | Tính luỹ kế: sau k phiếu trả, số điểm đã hoàn tác là `min(round(tổng × net đã trả ÷ net đơn), tổng)`. | "returning a sale in thirds…": 3 × 3334đ = 10 điểm, ba lần trả hoàn tác hết 10. |
| L7: thiếu consumer | (1) Không có ai publish `obligation.due_changed`. Thay vào đó, notification tiêu thụ 9 topic debt sẵn có, topic nào cũng mang `party_id`: `obligation.created/reversed`, `payment.posted/confirmed/reversed`, `allocation.posted`, `debt_adjustment.posted`, `debt_offset.posted`, `credit_note.recorded`. Rồi reconcile quá hạn / sắp đến hạn của party đó. Domain debt không phải sửa. (2) Sales publish `pos_sync.exception` khi park, discard và resolve, notification tiêu thụ topic này. (3) `stock.changed` giờ reconcile cả notice hết hạn của các lô thuộc SKU, nên lô bán hết hay huỷ hết sẽ mất notice ngay. | `TestNoticesFollowDebtAndSyncExceptionEvents` có 4 subtest: nợ quá hạn, phiếu offline bị park, lô bán hết, sweep. Đã kiểm tra subtest lô fail khi bỏ phần sửa. |
| L8: sweep clear notice consumer vừa raise | `clearAbsent` chỉ clear những dòng có `updated_at < sweptAt`. | Subtest "a sweep leaves a notice raised after it started…" |
| L9: If-Match tuỳ chọn ở `PUT /loyalty/program` | If-Match bắt buộc, thiếu thì 428. Lần lưu đầu gửi `"0"`. Version cũ thì 412. GET trả ETag. OpenAPI đã cập nhật. | `TestLoyaltyProgramSavesOnlyOverTheVersionItWasRead` |
| L10: thứ tự kiểm tra trong Ask | Kiểm tra read-only, rồi model, rồi gate, cuối cùng mới validate câu hỏi. | Test gating: câu hỏi rỗng khi AI tắt cho 403 `feature_disabled`; khi AI bật cho 422 `invalid_question`. |

**Bỏ qua (theo chỉ đạo của team-lead):**
- **L3** (head-of-line blocking giữa các tenant): cần thiết kế worker pool hoặc giới hạn mỗi tenant cho cả runner export lẫn sweep. Đây là thay đổi vận hành và nên đi cùng việc scale worker. Nó không gây sai dữ liệu, export đã có heartbeat và tự fail khi chạy quá lâu.
- **L5** (Reattribute với role primary và return): đụng tới hợp đồng attribution của campaign (nên từ chối 422 hay cascade xuống cấp dòng) và cần quyết định sản phẩm. Lỗi này không làm hỏng dữ liệu, chỉ làm thao tác reattribute không có hiệu lực ở một tình huống hẹp.

## B. Đổi điểm loyalty ở checkout (chỉ backend)

- `loyalty_points` là trường tuỳ chọn, phải là số nguyên dương, trên `POST /pos/checkout`, checkout từ cart và resolve sync exception. Điểm được đổi trong cùng posting qua `crm.Redeem` và cộng vào `OrderDiscount`. Response `PosSale` có thêm `loyalty_redemption {points, amount}`.
- **Lỗi trả về:**
  - 422 `customer_required` khi là khách lẻ;
  - 422 `must_be_positive_whole`;
  - 409 `insufficient_points`;
  - 409 `loyalty_disabled`;
  - 422 `loyalty_points: exceeds_amount` khi giá trị điểm lớn hơn tổng đơn; transaction rollback nên điểm không bị trừ.
  - Phiếu offline có điểm thì bị park với lỗi `loyalty_points: online_only`.
- **Hoàn điểm khi trả hàng và khi đảo đơn:**
  - Phiếu trả hoàn theo tỷ lệ, dùng cùng công thức luỹ kế (`sales_return_redemption`).
  - Đảo đơn hoàn phần còn lại (`sales_document_reversal_redemption`).
  - Cả hai đều idempotent theo `(kind, source_type, source_id)`.
- Hợp đồng vẫn tương thích ngược: không gửi `loyalty_points` thì hành vi giữ nguyên như trước.
- **Test** `TestLoyaltyPointsPayForPartOfASale`:
  - các ca lỗi không trừ điểm nào;
  - 50 điểm thành giảm 5.000đ, tổng còn 15.000đ, số dư giảm ngay;
  - gửi lại cùng key không đổi điểm lần hai;
  - earn 15 điểm trên số tiền thực trả;
  - trả 1/2 đơn: hoàn +25 điểm đã đổi và thu lại −8 điểm earn;
  - đảo đơn: hoàn đủ 100 điểm, số dư về như trước khi bán.

## C. Idempotency-Key tuỳ chọn cho 7 POST: chưa làm, đề xuất thiết kế

**Đánh giá:**
- Guard chỉ chống trùng đúng khi key được claim trong cùng transaction với thao tác ghi. Một middleware HTTP thì không làm được điều đó.
- Vì vậy phải sửa cả handler lẫn service ở 4 domain:
  - sales: `CreateQuote`, `CreateInvoice`;
  - procurement: `CreateOrder`, `CreateDocument`;
  - pricing: `SetSalePrice`;
  - kits: `CreateKit`, `CreateOrder`.
- Phải đổi chữ ký các service này và mọi chỗ đang gọi chúng.
- Khi replay phải khôi phục cả ETag, vì các create trả về `versioned(...)` hoặc `orderResponse` kèm ETag.
- Theo chỉ đạo, tôi báo lại thay vì làm dở.

**Đề xuất:**
1. `httpx.OptionalIdempotencyKey(c, body) (*idempotency.Key, error)`: không có header thì trả nil, header sai định dạng thì 422 `idempotency_key_invalid`.
2. Trong package idempotency, thêm helper generic
   `Once[T any](ctx, q, guard, tenantID, scope string, key *Key, run func() (T, error)) (T, bool, error)`.
   - Key nil: chỉ gọi `run`.
   - Có key: Begin; nếu đã có response lưu thì unmarshal JSON vào `T` và trả `replayed=true`; nếu chưa thì `run`, marshal `T`, rồi Complete.
   - Service gói phần thân transaction bằng `Once`. Handler vẫn nhận giá trị kiểu `T`, nên `versioned`/ETag không phải sửa, chỉ thêm header `Idempotent-Replay`.
3. **Rủi ro cần test cho từng kiểu:** JSON của `Quote`, `Invoice`, `PurchaseOrder`, `SupplierDocument`, `Kit`, `AssemblyOrder` và danh sách giá phải round-trip đối xứng. Nếu kiểu nào có trường tính toán hoặc chỉ dùng để marshal thì phải lưu DTO thay cho kiểu đó.
4. Cập nhật OpenAPI (tham số `IdempotencyKey` không bắt buộc, cùng các mã 409/422 của key). Mỗi route thêm một test double-submit.

**Ước lượng:** khoảng 7 handler, 7 service, 1 helper, 7 test và OpenAPI. Nên giao cho một lượt riêng, có quyền sửa 4 domain trên.

## File đã sửa

Tất cả nằm trong `apps/api/`.
- `internal/crm/`: `loyalty.go`, `model.go`, `http.go`, `service.go`.
- `internal/sales/`: `ports.go`, `errors.go`, `service.go`, `pos.go`, `carts.go`, `offline_sync.go`.
- `internal/analytics/`: `qa.go`, `model_client.go`, `qa_test.go`.
- `internal/notification/`: `consumers.go`, `producer_debt.go`, `producer_ops.go`, `producer_stock.go`, `store.go`.
- `internal/platform/apperr/`: `apperr.go`, `apperr_test.go`.
- `internal/platform/httpx/`: `render.go`, `httpx_test.go`.
- `internal/app/`: `crm_ports.go`, `domains.go`, `campaign_reporting_integration_test.go`, `loyalty_integration_test.go` (mới).
- `api/openapi.yaml`.
- Từ lượt trước (H1/M3/M5/L1/L2/L4): `internal/reporting/*`, `internal/platform/db/tx.go`, migration `000043` (down) và `000046`.

## Gate (chạy trong apps/api)

- `gofmt -l .`: sạch.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run --build-tags integration ./...`: 0 issues.
- `go test -race ./...`: pass.
- `go test -tags integration -count=1 ./...`: pass.
- `docker ps --filter label=org.testcontainers=true`: rỗng.

**Số đo tải** (20.001 SKU, 200.001 move; `TALLY_REPORT_LOAD=1`):

| Báo cáo | Thời gian |
|---|---|
| NXT trang đầu, có totals | 186ms (kỳ 2 tuần) / 614ms (kỳ 2,5 tháng) |
| NXT các trang sau | 6–10ms |
| Tìm kiếm | 381ms |
| Tồn theo lô | 94ms |
| Export 20k dòng (1,29MB) | 1,089s |

## Ghi chú thiết kế và lệch spec

- **L7:** tên topic khác spec. Notification không dùng `obligation.due_changed` mà tiêu thụ các topic debt sẵn có, nên debt không phải phát thêm event. Hạn nợ tự trôi qua theo thời gian (không có event nào) vẫn do sweep mỗi giờ lo.
- **L8:** notice bị đọc hoặc snooze sau khi sweep bắt đầu (nên `updated_at` mới hơn) sẽ được clear ở lần sweep kế tiếp. Chấp nhận được.
- **M2:** timeout vẫn ghi log câu hỏi (chưa trả lời) rồi mới trả 504, để nhật ký đầy đủ.

## Status

Status: DONE_WITH_CONCERNS
Summary: Đã sửa M1, M2, M4, L6, L7 (kể cả lô của `stock.changed`), L8, L9, L10. Đã nối đổi điểm loyalty vào checkout, kèm hoàn điểm khi trả hàng hoặc đảo đơn. OpenAPI và test đã cập nhật, mọi gate xanh. Phần C chưa làm, có đề xuất thiết kế ở trên.
Concerns:
- Phần C cần quyền sửa sales, procurement, pricing và kits.
- Thêm kind `apperr.Timeout` (504) là sửa code platform dùng chung.
- Nếu sau này có thao tác đảo phiếu trả hàng thì phải bổ sung việc khôi phục điểm cho nó; hiện chưa có thao tác này.
- `gen:api` cần chạy lại vì OpenAPI đổi: thêm `loyalty_points`, `loyalty_redemption`, If-Match bắt buộc ở loyalty program, Idempotency-Key bắt buộc ở adjustments, response 504.

## C. Đã làm

Đã làm theo thiết kế được duyệt. 7 POST giờ nhận `Idempotency-Key` tuỳ chọn: `/purchase-orders`, `/supplier-documents`, `/invoices`, `/quotes`, `/skus/{id}/sale-price`, `/kits`, `/assembly-orders`. Không gửi header thì hành vi y như cũ.

### Thiết kế

- **`idempotency.Once[T]`** (`internal/platform/idempotency/guard.go`) giữ key trong cùng transaction với thao tác ghi.
  - Key nil: chạy create như cũ.
  - Đã có response lưu sẵn: decode vào `out` và trả `replayed=true`.
  - Còn lại: chạy create, marshal `out`, rồi `Complete`.
  - Create lỗi thì transaction rollback và key được nhả.
  - Lệch nhẹ so với đề xuất: chữ ký là `out *T, create func() error` thay vì `run func() (T, error)`. Cách này giữ thân hàm create gần như nguyên vẹn, diff nhỏ hơn.
- **`httpx`** (`bind.go`):
  - `OptionalIdempotencyKey`: header rỗng trả nil, key sai trả 422 `idempotency_key_invalid`.
  - `BindJSONKeyed`: đọc body, lấy key (băm trên body thô), rồi decode strict.
  - `MarkReplay`: đặt `Idempotent-Replay: true`. `WritePosted` giờ dùng chung hàm này.
- **Service:** mỗi service (sales, procurement, pricing, kits) có thêm field `idem`. 7 hàm create trả thêm `replayed bool`.
  - Handler gọi `bindKeyed` rồi `httpx.MarkReplay` trước response writer sẵn có, nên ETag lấy từ T đã decode và trùng với lần đầu.
  - Lời gọi nội bộ `crm_ports.go` truyền key nil.
- **Scope của key:**

  | Thao tác | Scope |
  |---|---|
  | quote, invoice | action audit sẵn có |
  | purchase order | `purchase_order.create` |
  | supplier document | `supplier_document.create` |
  | sale price | `sale_price.set` |
  | kit | `kit.create` |
  | assembly order | `assembly_order.create` |

- **Status:** sale-price lưu và replay status 200, các route còn lại 201.
- **PO:** phần kiểm tra phụ thuộc "hôm nay" của `CreateOrder` được dời vào trong `Once`. Nhờ vậy một retry rơi sang ngày hôm sau vẫn replay chứ không bị từ chối. Các kiểm tra còn lại trước transaction chỉ phụ thuộc input nên tất định.
- **Kiểu được lưu:** cả 7 kiểu domain đều round-trip JSON đối xứng qua view mà handler render, nên lưu thẳng T, không cần chuyển sang DTO.

### Test

- `idempotencytest` (mới):
  - `Filled[T]` điền mọi field exported bằng reflection, kể cả struct nhúng, slice, map và kiểu tự decode.
  - `AssertRoundTrip` so JSON của view trước và sau khi decode.
  - Có test riêng cho helper, chứng minh view bất đối xứng bị bắt.
- Test round-trip cho từng kiểu được lưu: `sales/idempotent_create_test.go`, `procurement/idempotent_create_test.go`, `pricing/idempotent_write_test.go`, `kits/idempotent_create_test.go`.
- `idempotency/guard_integration_test.go`: `TestOnceRunsACreateOncePerKey`. Kiểm tra không key thì mỗi lần đều chạy; cùng request thì replay; khác body thì `reused`; create lỗi thì nhả key.
- `httpx_test.go`: `TestBindJSONKeyed`.
- `app/idempotent_creates_integration_test.go`, chạy cho cả 7 route:
  - Gửi đúp chỉ tạo 1 bản ghi, cùng id, cùng ETag, body giống hệt. Lần đầu không có header replay, lần retry có.
  - Cùng key nhưng khác body: 409 `idempotency_key_reused`.
  - Key `has space`: 422 `idempotency_key_invalid`.
  - Số bản ghi không đổi sau các lần bị từ chối.
  - Thêm một case không key: hai lần gửi tạo hai bản ghi.
  - Mutation check: ép `OptionalIdempotencyKey` trả nil thì test đỏ như kỳ vọng. Đã khôi phục code.
- `openapi_contract_test.go`: `TestOpenAPIErrorResponses` giờ đòi mọi operation có `IdempotencyKeyOptional` liệt kê `idempotency_key_reused` ở 409 và `idempotency_key_invalid` ở 422. Đã mutation-check: bỏ code là test đỏ.

### OpenAPI

- Thêm parameter `IdempotencyKeyOptional` (`required: false`, cùng schema với `IdempotencyKey`). Giữ tách khỏi `IdempotencyKey` vì contract test coi `IdempotencyKey` là posting và đòi 403 `subscription_read_only`.
- Cả 7 operation có thêm:
  - tham chiếu tới parameter mới;
  - một câu mô tả việc replay trong description;
  - `idempotency_key_reused` và `idempotency_key_in_progress` ở 409 (thêm mới response 409 cho quote, invoice và assembly order);
  - `idempotency_key_invalid` ở 422.

### File đã sửa

- `internal/platform/idempotency/`: `guard.go`, `guard_integration_test.go`, `idempotencytest/roundtrip.go` (mới), `idempotencytest/roundtrip_test.go` (mới).
- `internal/platform/httpx/`: `bind.go`, `httpx_test.go`.
- `internal/sales/`: `service.go`, `quotes.go`, `invoices.go`, `http.go`, `idempotent_create_test.go` (mới).
- `internal/procurement/`: `service.go`, `orders.go`, `matching.go`, `http.go`, `idempotent_create_test.go` (mới).
- `internal/pricing/`: `service.go`, `writer.go`, `http.go`, `idempotent_write_test.go` (mới).
- `internal/kits/`: `service.go`, `assembly.go`, `recipe_service.go`, `http.go`, `idempotent_create_test.go` (mới).
- `internal/app/`:
  - `crm_ports.go`;
  - `pricing_integration_test.go`: cập nhật lời gọi `SetSalePrice`;
  - `idempotent_creates_integration_test.go` (mới);
  - `openapi_contract_test.go`.
- `api/openapi.yaml`.

### Gate (apps/api)

- `gofmt -l .`: sạch.
- `go vet ./...` và `go vet -tags integration ./...`: sạch.
- `golangci-lint run --build-tags integration ./...`: 0 issues.
- `go test -race ./...`: pass.
- `go test -tags integration -count=1 ./...`: pass.
- `docker ps --filter label=org.testcontainers=true`: rỗng.
- Không đụng `tally-postgres-1`, MinIO dev hay `apps/web`. Không có migration mới. Không commit.

### Status phần C

Status: DONE_WITH_CONCERNS
Summary: Cả 7 POST create nhận `Idempotency-Key` tuỳ chọn qua `idempotency.Once[T]`. Key được giữ trong cùng transaction với thao tác ghi. Retry trả lại cùng T, cùng ETag, kèm `Idempotent-Replay: true`; không gửi key thì hành vi không đổi. Đã có test round-trip cho từng kiểu, test gửi đúp cho từng route và cập nhật OpenAPI. Mọi gate xanh.
Concerns:
- `idempotency_key_in_progress` (hai request cùng key chạy song song) mới chỉ được test ở tầng platform, chưa có test đồng thời ở tầng app.
- Web client chưa gửi key cho 7 route này (ngoài phạm vi, không sửa `apps/web`). Cần chạy lại `gen:api` vì OpenAPI có parameter mới và response 409 mới cho quote, invoice và assembly order.
