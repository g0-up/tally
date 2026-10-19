# Review: web dùng 13 API mới (web-adopt-apis)

Ngày: 2026-10-19, 06:04 (Asia/Saigon). Chỉ đọc. Không sửa code.

## Phạm vi

- Bản đọc: `apps/web/src` (các file mới hơn `plans/reports/fullstack-developer-261018-2040-web-settings-onboarding-admin.md`) và `apps/web/e2e/inventory.spec.ts`.
- Đối chiếu với:
  - `plans/reports/fullstack-developer-261019-0242-web-adopt-api-gaps.md`
  - `plans/reports/fullstack-developer-261018-2040-api-gap-batch.md`
  - `apps/api/api/openapi.yaml`
  - mã Go ở `apps/api/internal/{platform/idempotency,reporting,sales,kits,inventory,notification}`
- `apps/web` chưa được commit (`git status` báo `??`), nên không có diff. Tôi đọc trực tiếp các file đã đổi.
- Nếu agent UI/a11y sửa file trong lúc review, báo cáo này ứng với bản tôi đã đọc.

## Cổng kiểm tra (chạy trong `apps/web`)

| Lệnh | Kết quả |
|---|---|
| `corepack pnpm typecheck` | exit 0 |
| `corepack pnpm lint` | exit 0 |
| `corepack pnpm test` | 132 file, 948 test đạt, exit 0 |

Không chạy e2e, dev server hay docker.

## Critical

Không có.

## High

Không có.

## Medium

### M1. Hai lệnh dùng chung một Idempotency-Key cho mọi dòng của danh sách

**Vị trí**

- `features/kits/pages/recipes-page.tsx:49`: tạo một `usePublishKit()` cho cả trang. Dòng 137 gọi `publish.run(kit)` cho từng thẻ.
- `features/kits/api/mutations.ts:278-288`: gửi thẳng `idempotencyKey` của useCommit (hàm `publish` ở dòng 163).
- `features/sales/hooks/use-quote-actions.ts:17`: tạo một `useQuoteTransition()`, dùng chung cho bảng `quotes-page.tsx` và `quote-drawer.tsx`.
- `features/sales/api/quote-mutations.ts:136-143`: gửi khóa này khi `action === "send"`.

useCommit chỉ đổi khóa khi nhận 2xx hoặc khi gọi `resetKey`. Vì vậy khóa của lần gọi trước vẫn còn khi người dùng chuyển sang dòng khác. Server (`platform/idempotency/guard.go`) gắn khóa với (tenant, scope, key), và hash gồm method, path, body. Cùng khóa nhưng khác path thì server trả 409 `idempotency_key_reused`.

**Kịch bản**

1. Bấm "Phát hành" kit A. Mạng rớt nên mất câu trả lời (OutcomeUnknown), và khóa K được giữ lại.
2. Người dùng bấm "Phát hành" kit B. Request đi với K nhưng path là `/kits/B:publish`.
3. Nếu A đã được ghi: server trả 409 cho B. B không phát hành được cho đến khi trang remount hoặc có một lần thành công. Toast báo lỗi khó hiểu.
4. Nếu A chưa được ghi: K không bị giữ, nên B chạy được và chiếm K. Khi đó bấm lại A thì A nhận 409.

Lệnh "Gửi báo giá" bị y như vậy giữa hai báo giá trên cùng bảng.

**Đề xuất**

Gắn khóa theo đối tượng, giống `useReorder` (`${idempotencyKey}:${c.id}`):

```ts
publish(client, v, `${idempotencyKey}:${v.product_id}`)
// quote
...(v.action === "send" ? { idempotencyKey: `${idempotencyKey}:${v.quote.id}` } : {})
```

Cách khác: gọi `resetKey()` khi đối tượng đích đổi. Nên thêm một test: mất câu trả lời ở dòng A, rồi thao tác dòng B, kiểm tra hai khóa khác nhau.

### M2. `useQuickCustomer` thiếu createOnce và chốt "chưa rõ đã ghi"

**Vị trí**

- `features/crm/api/mutations.ts:92-110`: chỉ gọi thẳng `client.post("/customers", ..., { idempotencyKey })`.
- Được dùng ở `features/crm/containers/customer-quick-dialog.tsx:23`.

`useSaveCustomer` thì có đủ `createOnce` + `useUnsettled` + `CLOSE_UNSETTLED_MESSAGE`. Dialog thêm nhanh khách (từ POS, đơn, báo giá) không có các chốt này.

**Kịch bản**

1. Khách được tạo nhưng câu trả lời bị mất.
2. Dialog hiện lỗi mạng chung, có thể là "Chưa ghi sổ", điều này có thể sai.
3. Người dùng sửa số điện thoại rồi bấm lại. Cùng khóa nhưng body khác, nên server trả 409 `idempotency_key_reused` và người dùng bị kẹt.
4. Hoặc người dùng đóng rồi mở lại dialog. Khóa mới được tạo và khách bị tạo hai lần, đúng với tình huống mà Idempotency-Key được thêm vào để chặn.

**Đề xuất**

Dùng lại đúng mẫu của `useSaveCustomer`:

- `createOnce(state, body, { send })` để giữ body đã gửi;
- `lost.watch(...)`;
- chặn đóng dialog bằng `CLOSE_UNSETTLED_MESSAGE` khi `unsettled`;
- báo rõ "Chưa rõ đã ghi hay chưa" thay vì "Chưa ghi sổ".

### M3. Đăng xuất rồi người khác đăng nhập trên cùng máy vẫn thấy và tải được file xuất của người trước

**Vị trí**

- `features/auth/api/mutations.ts:34-45`: `useSessionChanged` chỉ `removeQueries(["platform"])` (hoặc adminKeys). Comment lại ghi "drop every cached answer of the previous identity", nên comment và hành vi không khớp.
- `features/reports/use-export-job.ts:73`: dùng `exportKeys.recent()` = `["report-exports","recent"]`, không có user id trong key.
- `features/settings/api/account.ts:185`: dùng `settingsKeys.recentExports()`, cũng không có user id.
- `features/reports/export-job-store.ts`: lưu sessionStorage `tally.report-exports`. `resetStoredExports()` (dòng 83) chỉ được gọi trong test.
- Phía API, `reporting/http.go:280` lọc danh sách theo `requested_by`. Nhưng GET một job và tải file (`reporting/http.go` ~305-356) không kiểm tra người yêu cầu.

**Kịch bản**

1. Trên máy dùng chung của cửa hàng (cùng tenant), nhân viên A xuất "Lãi gộp" rồi đăng xuất.
2. Trong cùng tab, B đăng nhập trong vòng gcTime (5 phút) hoặc khi entry trong sessionStorage vẫn còn.
3. Màn báo cáo dùng `setStoredExport` để nối lại job của A, đọc `/export-jobs/{id}` rồi hiện "Tải về". B tải được file của A, dù B có thể không có quyền xem báo cáo đó.
4. Ở trang cài đặt, thẻ "bản xuất toàn bộ dữ liệu" cũng có thể hiện link của người trước từ cache.

**Đề xuất**

- Web:
  - Trong `useSessionChanged`, gọi `queryClient.clear()` (hoặc ít nhất `removeQueries` cho `["report-exports"]` và `["settings"]`) và `resetStoredExports()`.
  - Có thể thêm user id vào key `recent` và vào key của sessionStorage.
- API (báo cho nhóm API): `getExport`/`download` nên chỉ cho `requested_by = actor` hoặc người có quyền của loại báo cáo đó.

### M4. `paymentCodeOf` không khớp với server khi tổng bằng 0

**Vị trí**

- `features/sales/api/adapters.ts:36-43`: kiểm tra `p <= 0 → unpaid` trước tiên.
- Server (`apps/api/internal/sales/status.go:40-55`) kiểm tra theo thứ tự: `c > 0 → overpaid`, rồi `c == 0 → paid`, rồi `!paid.IsPositive() → unpaid`.
- `invoicePayment` (dòng 64-73) dùng `delivered_amount` làm tổng cho `cancelled_remaining`.

**Kịch bản**

Hóa đơn `cancelled_remaining` chưa giao gì, nên total = 0 và paid = 0. Hóa đơn 0 đồng (hàng tặng) cũng vậy. Danh sách hóa đơn hiện "Chưa thanh toán", còn drawer, nơi dùng `payment` của server, hiện "Đã thanh toán đủ". Bộ lọc "Chưa thanh toán" ở client cũng đếm nhầm hóa đơn này.

**Đề xuất**

Sắp lại thứ tự kiểm tra cho giống server:

```ts
const c = p.minus(t);
if (c.gt(0)) return "overpaid";
if (c.eq(0)) return "paid";
if (p.lte(0)) return "unpaid";
return "partial";
```

Thêm test cho trường hợp `total = 0, paid = 0`.

## Low

- **L1. Sửa body sau khi mất câu trả lời thì bị kẹt 409.**
  - Vị trí: `features/debt/api/mutations.ts`, `useCollectionNote` và `useCreateStatement`.
  - Kịch bản: mất câu trả lời, người dùng sửa ô ghi chú rồi bấm lại. Cùng khóa nhưng body khác, nên server trả 409 `idempotency_key_reused`. Thông báo chung không cho người dùng biết bản đầu có thể đã được lưu.
  - Đề xuất: dùng `createOnce` (giữ body đã gửi) như các lệnh tạo khác, hoặc khi gặp 409 `idempotency_key_reused` thì báo "Bản trước có thể đã lưu; tải lại để kiểm tra".
- **L2. Khóa consent bị trùng khi đổi nguồn.**
  - Vị trí: `features/crm/api/mutations.ts:61`. Khóa là `${idempotencyKey}:consent-${step}`, không gồm `consentSource`.
  - Kịch bản: mất câu trả lời, người dùng đổi "Nguồn đồng ý" rồi lưu lại. Cùng khóa nhưng body khác, nên server trả 409.
  - Đề xuất: đưa source vào khóa, hoặc giữ nguyên body consent đã gửi giống createOnce.
- **L3. Lọc phiếu hoàn tiền ở client làm trang lẻ dòng.**
  - Vị trí: `features/debt/pages/advances-page.tsx:24-25`. Phiếu `refund` bị lọc ra sau khi đã tải từng trang.
  - Kịch bản: một trang 50 dòng mà toàn là refund thì hiện trống nhưng vẫn có "Tải thêm".
  - Đề xuất: nếu API nhận `purpose` thì lọc ở server. Nếu không thì để như hiện tại, đây là chấp nhận được.
- **L4. Dòng đảo phân bổ không được đánh dấu.**
  - Vị trí: `features/debt/containers/payment-drawer.tsx:67`. Bảng "đã phân bổ" hiện cả dòng đảo phân bổ mà không có dấu hay số âm.
  - Hậu quả: người dùng đọc thành phân bổ trùng.
  - Đề xuất: đánh dấu "Đã đảo" hoặc hiện số âm theo field của `documentSettlement`.
- **L5. Bộ lọc và tổng của danh sách hóa đơn chỉ tính trên các trang đã tải.**
  - Vị trí: `features/sales/pages/invoices-page.tsx`.
  - Hành vi này có từ trước. Nhưng giờ dùng `paid_amount` thật nên người dùng dễ tin rằng tổng là của toàn bộ.
  - Đề xuất: ghi rõ "trên các dòng đã tải" giống `loadedOnly` ở `stock-page.tsx:48`, hoặc lọc ở server.
- **L6. Hai lần đọc `/export-jobs` gần như trùng nhau.**
  - Vị trí: `use-export-job.ts:73` và `settings/api/account.ts:185`, hai key khác nhau, mỗi lần `limit=50`.
  - Đây không phải lỗi đúng sai, chỉ tốn thêm một request khi mở cả hai màn.
- **L7. Lỗi `retail_price` của dialog kit chưa có ô hiện.**
  - Vị trí: `features/kits/containers/kit-dialog.tsx:84-91`. Chỉ map `wholesale_price` và `min_stock`.
  - Server (`kits/recipe_service.go:107`) cũng có thể trả `retail_price: must_not_be_negative`. Khi đó lỗi chỉ lên toast.
  - MoneyInput có lẽ đã chặn số âm, nên rủi ro thấp.

## Đã xác minh đúng

- **Schema và nullability của debt**
  - Các schema trong `features/debt/schemas.ts`: paymentRow/Detail, statementRow, debtAdjustment*, debtOffset*, documentSettlement.
  - Khớp OpenAPI: `/payments` (~3951), `/statements` (~4437).
- **Ứng trước**
  - `useAdvances` đọc một list `GET /payments?unallocated=true`.
  - Test `advances-page.test.tsx` kiểm tra `ledgerReads` và tham số `unallocated=true`. Không có N+1.
- **Mở chứng từ thật**
  - `useDocumentByNumber` đọc list `?doc_no=&limit=1`, rồi đọc chi tiết. Đây là 2 request cố định.
  - `statements-page.tsx` đọc `?id` qua `useStatement`. Lỗi 404 hiện "Không tìm thấy bảng kê này…".
- **CRM**
  - `customer_ids` chia nhóm ≤ 50 (`REVENUE_IDS_PER_READ = PAGE`, nhỏ hơn giới hạn 100) và gộp lại bằng `useQueries` + `combine`. Key có chứa danh sách id.
  - Test kiểm tra đúng chuỗi request revenue và `rowReads = []` (không đọc từng khách).
  - Lỗi 422 `customer_ids` hiện câu tiếng Việt.
  - `last_purchase_at` null hiện "—". `consents` được đọc từ list.
- **Thông báo**
  - `notificationTarget` (`notifications/kinds.ts:47-50`) chỉ nhận đường dẫn bắt đầu bằng `/` và không phải `//`, nên không có open redirect.
  - Các `action_path` của producer đều là route của app.
- **Giờ server cho "Nhắc lại mai"**
  - `until = serverNow() + 24h` (`notifications/api/mutations.ts:54-57`, `useServerNow`).
  - Test kiểm tra `until` nằm trong [server + 24h, server + 24h + 60s], không dựa vào giờ máy.
- **Vòng đời Idempotency-Key** (`use-commit.ts`, `use-idempotent-mutation.ts`, `create-once.ts`)
  - Khóa được tạo khi cần cho mỗi intent.
  - Khóa được giữ khi retry, khi re-auth và khi mất câu trả lời.
  - Khóa đổi sau 2xx.
  - Các bước theo sau có hậu tố riêng (`:publish`, `:send`, `:consent-…`, `:${id}`).
  - Lỗi của bước ra 422 `fields` được dịch sang tiếng Việt (`fieldReasonMessage`).
- **Invalidate sau khi ghi**
  - Kit: `KIT_KEYS`. Debt: `POSTING_KEYS` có `["debt"]`. CRM: `CUSTOMER_KEYS`.
  - Tôi đã kiểm tra các prefix, chúng phủ đúng các list liên quan.
- **Dashboard**
  - `by_category` và `top[].next_due` khớp OpenAPI (15384-15450). Tiền dùng `dec`.
  - Quá hạn được so với `data.today` của server, không dùng giờ máy.
- **OPEN-13**
  - "Chi phí mùa" và "Kết quả quản trị" hiện "—", không bịa số.
  - `open_orders` và `receivable` là `KindMoney` (`reporting/season.go:251-252`), nên dùng `vnd()` là đúng.
  - `seasonFigure` trả null khi không có số, không trả 0 giả.
- **Inventory**
  - `uncolored` không bao giờ được gửi cùng `colored_only` (`stock-page.tsx:39`). Khi chọn cả hai, trang lọc trên các dòng đã tải và báo `loadedOnly`.
  - `useAdjustmentList(docNo)` và `useCountList(docNo)` gửi `doc_no`. `?doc=` không thấy thì mở `DocNotFoundDrawer`.
  - `variance_lines` (exactOptional) hiện "—" khi đang đếm hoặc khi thiếu.
  - `line_preview` và "và {n} dòng khác" khớp OpenAPI.
- **Tháo giỏ**
  - `useDisassemblyList(q)` đưa `q` vào key.
  - Trang lọc lại theo `doc_no` chính xác, vì server tìm theo cả số lệnh, số lô và tên kit.
- **Kit**
  - `wholesale_price` và `min_stock` đi trong request tạo kit (`view-model.ts:154-155`). `min_stock` chỉ có khi là kit.
  - Lỗi 422 map đúng ô với mã của server (`must_not_be_negative`, `not_allowed`).
  - `body satisfies KitRequest`.
- **Nhả hàng giữ**
  - Đã trim và chặn rỗng.
  - Giới hạn 500 khớp `utf8.RuneCountInString(reason) > maxNoteLen` của server. JS đếm theo UTF-16, nên client chặn chặt hơn hoặc bằng server, không bao giờ lỏng hơn.
  - Lỗi `required` và `too_long` hiện dưới ô "Lý do".
- **Combobox**
  - `blurTimer` được xóa trước khi đặt lại và được dọn khi unmount (`combobox.tsx:161`). Lỗi "document is not defined" sau teardown đã hết.
- **`e2e/inventory.spec.ts`**
  - `uncolored = colors.every(c => c.color_id === null)` đúng với `inventory/queries.go` (dòng tồn không màu có `color_id: null`).
  - Bài điều chỉnh giảm thêm điều kiện `sellable >= 1`. Chưa chạy được vì thiếu `E2E_OWNER_PASSWORD`.
- **Test có assert thật**
  - Đã xem: advances (số lần đọc, tham số, ẩn refund), customers (chuỗi request, giá trị từng ô), notifications (khoảng `until`), dashboard (từng con số).
  - Không thấy assert bị nới lỏng.

## Việc nên làm theo thứ tự

1. M3: dọn cache và `tally.report-exports` khi đổi phiên. Báo nhóm API thêm kiểm tra người yêu cầu cho GET/download export.
2. M1: gắn khóa publish và send theo id của đối tượng, kèm test hai dòng.
3. M2: đưa `useQuickCustomer` về mẫu createOnce + unsettled.
4. M4: sắp lại `paymentCodeOf` giống server, kèm test tổng = 0.
5. L1 đến L7 khi tiện.

## Câu hỏi còn mở

- `GET /export-jobs/{id}` không kiểm tra `requested_by`: đây là chủ ý (mọi người trong tenant có quyền báo cáo đều xem được) hay là thiếu sót? Câu trả lời quyết định phần API của M3.

Status: DONE_WITH_CONCERNS
Summary: Cả 13 mục đều dùng đúng hợp đồng API. Typecheck, lint và 948 test đều xanh, không có lỗi Critical hay High. Có 4 lỗi Medium: khóa idempotency dùng chung giữa các dòng (publish kit, gửi báo giá); thêm nhanh khách thiếu chốt mất câu trả lời; cache và sessionStorage của file xuất không được dọn khi đổi người dùng; `paymentCodeOf` lệch server khi tổng bằng 0.
Concerns/Blockers: M3 cần nhóm API xác nhận quyền đọc export của người khác trong cùng tenant. e2e chưa chạy được (thiếu `E2E_OWNER_PASSWORD`).
