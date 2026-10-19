# Web dùng API thật cho 13 khoảng trống

Phạm vi: chỉ `apps/web` (src, test, e2e). Không sửa `apps/api`, không sửa file plan và không sửa `schema.d.ts` bằng tay; file này đã được sinh sẵn và có đủ mọi route, field cần dùng. Không commit.

Cả 13 mục đã làm xong:

- Mọi workaround (đọc chi tiết từng dòng, tải hết trang, trả lời "không tra được theo số") đã được thay bằng route thật.
- Schema zod có `satisfies` theo kiểu sinh từ OpenAPI.
- Handler MSW và test đã cập nhật theo.

Bốn mục 2, 4, 5 và 12 không còn N+1:

- Mỗi mục có test đếm số lần đọc chi tiết, và test đều khẳng định số đó bằng 0.
- Riêng doanh thu CRM, test khẳng định mỗi trang chỉ đọc một lần.

## Theo từng mục

### 1. Idempotency-Key trên 7 command

**Code**

- `features/crm/api/mutations.ts`:
  - `POST /customers`
  - `POST /customers/{id}/consents`
  - `POST /customers/{id}/reorder-drafts`
- `features/debt/api/mutations.ts`:
  - `POST /statements`
  - `POST /parties/{id}/collection-notes`
- `features/sales/api/quote-mutations.ts`: `POST /quotes/{id}:send`
- `features/kits/api/mutations.ts`: `POST /kits/{id}:publish`

Mỗi key gắn với một ý định của người dùng. Khi gửi lại sau một lần mất trả lời, request dùng lại đúng key và body cũ. Trước đây không có workaround "đọc lại sau :send/:publish" nào, nên mục này chỉ cần thêm key.

**Test gửi lại cùng key.** Lần gửi đầu trả `HttpResponse.error()`, test bấm lại và khẳng định key không đổi:

- `crm/containers/customer-dialog.test.tsx`:
  - "sends the create again under the same key when its answer was lost, and adds the customer once"
  - "names the stored customer when the consent is refused, and does not add it twice on retry" (key của consent giữ nguyên)
- `crm/containers/customer-quick-dialog.test.tsx` (mới): "sends the create again under the same key when its answer was lost, and selects the customer once"
- `crm/pages/customers-page.test.tsx`: "sends a re-order again under the same key when its answer was lost"
- `debt/pages/statements-page.test.tsx`: "sends a new statement again under the same Idempotency-Key after a lost answer"
- `debt/containers/debt-note-dialog.test.tsx`: "saves the note again under the same Idempotency-Key after a lost answer"
- `sales/api/create-documents.test.tsx`:
  - "resends a send of the new quote that got no answer under the same key"
  - "resends “Gửi khách” that got no answer under the same key"
- `kits/pages/recipes-page.test.tsx`: "resends a publish that got no answer under the same key"
- `kits/containers/kit-dialog.test.tsx`: "resends a publish that got no answer under its own key of the intent, so the kit is published once"

### 2. Ứng trước dùng `GET /payments?unallocated=true`

**File**

- `features/debt/api/queries.ts`: `useAdvances` đọc một danh sách phân trang. Mỗi dòng đã có `party_name`, `unallocated` và `purpose`, nên không còn phải đọc sổ của từng đối tác.
- `features/debt/schemas.ts`, `features/debt/testing/fixtures.ts`
- `features/debt/pages/advances-page.tsx`
- `features/debt/allocation.ts`: thêm `canAllocate`, chuyển từ file component sang để lint `react-refresh` sạch.
- `e2e/debt.spec.ts`: chỉ sửa câu chú thích mô tả luồng.

**Test** (`debt/pages/advances-page.test.tsx`)

- "lists the unallocated payments of both ledgers with their party, from one list read": `ledgerReads` bằng 0 và query có `unallocated=true`.
- "reads the next page of advances on request"

### 3. Mở PT/PC theo id hoặc `doc_no`

**File**

- `features/debt/containers/payment-drawer.tsx` (mới): drawer phiếu thu/chi, có các chứng từ đã phân bổ và nút "Phân bổ".
- `usePaymentByNumber` trong `features/debt/api/queries.ts`
- `features/debt/doc-openers.ts`: PT/PC mở drawer này, thay cho câu báo "không tra được".

**Test** (`advances-page.test.tsx`)

- "opens a linked receipt by its number with the documents it settled, and its allocation"
- "opens a fully allocated payment without the allocation"
- "says when no payment has the linked number"

### 4. Bảng kê: xem chi tiết, danh sách không N+1

**File**

- `features/debt/components/statement-drawer.tsx`
- `features/debt/pages/statements-page.tsx`
- `features/debt/schemas.ts`

Danh sách lấy khách và số dư từ chính dòng list. Bảng kê `?id=` được đọc theo id, kể cả khi nó nằm ngoài trang đầu.

**Test** (`debt/pages/statements-page.test.tsx`)

- "lists statements with the customer and balances the list carries, reading no version"
- "opens the statement of ?id, the link a disputed-statement notification carries, past the first page"
- "explains a statement of ?id that no longer exists"

### 5. Danh sách CRM không N+1

**File**

- `features/crm/api/queries.ts`, `features/crm/schemas.ts`
- `features/crm/pages/customers-page.tsx`
- `features/crm/customer-form-values.ts`, `features/crm/containers/customer-dialog.tsx`
- `features/crm/testing/fixtures.ts`

Lần mua cuối và trạng thái đồng ý nhận tin lấy thẳng từ dòng list.

**Test** (`crm/pages/customers-page.test.tsx`)

- "lists customers with season revenue, last purchase and consent from the list and one revenue read"
- "re-reads the list after a save, which carries the saved consent, without a read per row"

### 6. Doanh thu theo `customer_ids`, tối đa 100 mỗi lần

**File:** `features/crm/api/queries.ts`. Mỗi trang list gọi doanh thu một lần, chỉ cho các khách của trang đó; một trang có tối đa 100 khách.

**Test** (`customers-page.test.tsx`)

- "reads the revenue of each list page once, for the customers of that page alone"
- "leaves the revenue column at — when the report is refused, without a note"
- `it.each` cho hai lỗi `too_many` và `invalid`

### 7. Xem chi tiết DN/BT

**File**

- `features/debt/containers/debt-adjustment-drawer.tsx` (mới)
- `useAdjustmentByNumber` và `useOffsetByNumber` trong `features/debt/api/queries.ts`
- `features/debt/containers/debt-ledger-view.tsx`: bỏ câu "Không tra được … theo số chứng từ".
- `features/debt/api/adapters.ts`:
  - thêm `ADJUSTMENT_KIND_LABELS`;
  - chuyển `PAYMENT_STATUS_LABELS` từ `party-drawer.tsx` sang đây để dùng chung.
- `shared/domain/doc-no.ts`: thêm prefix DN và BT (hai dòng).
- `features/debt/doc-openers.ts`: thêm BT.

**Test** (`debt/pages/receivables-page.test.tsx`)

- "opens a debt adjustment by its number with its reason and the document it reduced"
- "opens an offset by its number with the two documents it netted"
- "says when no adjustment has the linked number"

### 8. Thông báo dùng `action_path` và `doc_no`

**File**

- `features/notifications/kinds.ts`, `features/notifications/schemas.ts`
- `features/notifications/pages/notifications-page.tsx`

Nút "Mở" đi theo `action_path` server gửi. Đường dẫn nằm ngoài app thì không có nút "Mở".

**Test** (`notifications/pages/notifications-page.test.tsx`)

- "opens the route the notification names, and offers no Mở for a path outside the app"
- "opens the disputed statement itself"
- các test mở và đánh dấu đã đọc

### 9. Hoãn nhắc dùng `/me.server_time`

**File**

- `shared/api/platform-queries.ts`: thêm hook `useServerNow`, vài dòng, dùng lại offset đồng hồ sẵn có.
- `features/notifications/api/mutations.ts`: "Nhắc lại mai" tính theo giờ server.

**Test:** "snoozes for a day and resolves, then shows the refreshed list".

### 10. Lấy lại job xuất file

**File**

- `features/reports/api/queries.ts`: `GET /export-jobs`, đọc 50 job gần nhất, lỗi thì bỏ qua và màn hình vẫn chạy như thường.
- `features/reports/use-export-job.ts`, `features/reports/export-job-store.ts`
- `features/reports/schemas.ts`, `features/reports/testing/fixtures.ts`
- `test/msw/handlers.ts`: thêm một handler mặc định `GET /export-jobs` trả danh sách rỗng.

**Test** (`reports/use-export-job.test.tsx`)

- "picks up the newest export of the report the server is still writing when the tab kept none"
- "picks up a written export only while it can still be downloaded"
- "follows the export the tab kept without asking the server's list"
- "does not bring back an earlier export after a new one was refused"
- "picks up each ledger's own export of a report"
- "keeps the two ledgers of one report on separate exports"

**Test** (`reports/export-job-store.test.ts`): các test lưu, xóa và tiếp tục theo dõi job.

**Xuất toàn bộ dữ liệu (Cài đặt › Tài khoản)**

- Bằng chứng API: `GET /export-jobs` (`listExportJobs`) trả mọi job của người gọi, gồm cả báo cáo CSV lẫn `full_data_export` (`apps/api/internal/reporting/export.go:226-235`, lọc theo `requested_by`).
- File:
  - `features/settings/api/account.ts`: thêm `useRecentFullExport`. Hook đọc 50 job gần nhất và chọn job `full_data_export` mới nhất còn đang chạy, hoặc đã xong mà còn `download_path`. Lỗi được bỏ qua (`silent`, không retry).
  - `features/settings/api/tenant-settings.ts`: thêm key `settingsKeys.recentExports()`.
  - `features/settings/components/data-export-card.tsx`: khi trang chưa giữ job nào, thẻ hiện job tìm lại được.
- Test (`settings/pages/account-page.test.tsx`):
  - "finds the full data export started before a reload and offers its file": danh sách có một job CSV đang chạy và một bản xuất toàn bộ đã xong. Thẻ chỉ hiện link "Tải về" của bản xuất toàn bộ và không POST thêm gì.
  - "does not bring back a full data export that can no longer be downloaded": job hết hạn hoặc thất bại thì không hiện lại, và không đọc trạng thái job nào.

### 11. Dashboard và mùa

**File**

- `features/dashboard/schemas.ts`
- `features/dashboard/components/cash-band.tsx`: thêm `cash.by_category`, hiện dưới tiêu đề "Theo loại".
- `features/dashboard/components/debt-band.tsx`: thêm cột "Hạn gần nhất" lấy từ `top[].next_due`.
- `features/campaign/components/campaign-card.tsx`: thêm `open_orders` và `receivable` của season summary.
- `features/campaign/testing/fixtures.ts`
- `e2e/overview.spec.ts`: kiểm tra có tiêu đề "Theo loại".

"Chi phí chưa trả", "Chi phí mùa" và "Kết quả quản trị" chưa có API (OPEN-13). Ba ô này được giữ nguyên như trước, không tự đặt ra con số. "Sắp phải trả" vẫn chỉ tính NCC.

**Test**

- `dashboard/pages/dashboard-page.test.tsx`:
  - "shows the month the API returned, figure for figure"
  - "marks a supplier whose nearest due date has passed"
- `campaign/pages/seasons-page.test.tsx`: "shows each campaign's season figures from the season report, with no figure the API lacks"

### 12. Danh sách hóa đơn dùng `paid_amount`

**File**

- `features/sales/schemas.ts`, `features/sales/api/queries.ts`
- `features/sales/api/adapters.ts`: `invoicePayment` suy trạng thái từ `paid_amount` giống cách server làm, cộng trừ bằng `shared/lib/decimal`.
- `features/sales/components/invoice-table.tsx`, `features/sales/pages/invoices-page.tsx`

**Test** (`sales/pages/invoices-page.test.tsx`, mới; cả hai test có `reads.details` bằng 0)

- "shows what each invoice was paid and still owes from the list read alone"
- "filters by payment state without reading each invoice"

### 13. Kho và kit

**Danh sách điều chỉnh**

- Thay đổi:
  - `adjustmentListItemSchema` có `line_count`, `total_base_qty`, `value_in`, `value_out` và `line_preview[]`, với `satisfies` theo `StockAdjustment`.
  - `useAdjustmentList(docNo)` gửi `doc_no`. Bỏ `useAdjustmentDetails`, `useLoadAll` và `useSeenIds`.
  - Bảng hiện vài dòng đầu kèm "và {n} dòng khác", tổng SL và giá trị tăng/giảm.
- File:
  - `features/inventory/schemas.ts`, `features/inventory/api/queries.ts`
  - `features/inventory/components/adjustment-table.tsx`, `features/inventory/pages/adjustments-page.tsx`
- Test (`inventory/pages/adjustments-page.test.tsx`):
  - "asks the server for a linked adjustment by its number and shows every one again on request"
  - "shows each adjustment's first lines, total and value from the list read alone"
  - "says when no adjustment has the linked number"

**Danh sách kiểm kê**

- Thay đổi:
  - `countListItemSchema` có `line_count` và `counted_lines`. `variance_lines` là trường tùy chọn: server không gửi khi đang đếm mù, và lúc đang đếm cột "Dòng lệch" hiện "—".
  - Mở `?doc=` theo `doc_no`, kể cả phiên nằm ngoài trang đầu, không phải lật hết danh sách. Bỏ `useCountDetails`.
- File:
  - `features/inventory/components/count-table.tsx`, `features/inventory/pages/counts-page.tsx`
  - `features/inventory/use-doc-param.ts` (chỉ sửa chú thích)
  - `features/inventory/testing/fixtures.ts`
- Test (`inventory/pages/counts-page.test.tsx`):
  - "shows each count's progress from the list read alone"
  - "opens a linked count older than the loaded page by its number, without paging through the list"
  - "says when no count has the linked number"

**Tồn "Không màu"**

- Thay đổi:
  - Chọn "Chưa gán màu" thì gửi `uncolored=true`.
  - Server trả `conflicts_with_uncolored` khi `uncolored` đi cùng `colored_only` hay `color_id`, nên web không bao giờ gửi chung các cặp đó.
  - Khi chọn cả "Chỉ hàng có màu" và "Chưa gán màu", bộ lọc "có màu" chạy trên các dòng đã tải, kèm câu hướng dẫn bấm "Xem thêm".
  - Nhãn nút vẫn là "Chưa gán màu" như prototype (`docs/design/prototype-v6.14.html:3947`).
- File: `features/inventory/pages/stock-page.tsx`, `features/inventory/api/queries.ts`
- Test (`inventory/pages/stock-page.test.tsx`; mock server trả 422 `conflicts_with_uncolored` nếu nhận được cặp bị cấm):
  - "asks the server for the stock without a color"
  - "keeps colored products with untagged stock without sending the server both filters"

**Tìm phiếu tháo giỏ (`q`)**

- Thay đổi:
  - `useDisassemblyList(q)`: `?doc=TG-…` gửi `q`. Server tìm cả theo số lô, số lệnh và tên giỏ, nên trang lọc lại cho khớp đúng số phiếu.
  - Bỏ `useLoadAll`.
- File: `features/kits/api/queries.ts`, `features/kits/pages/disassembly-page.tsx`
- Test (`kits/pages/disassembly-page.test.tsx`):
  - "searches the server for a linked disassembly by its number and shows every one again on request": khẳng định chỉ gửi `q=TG-0002` và không có `cursor`.
  - "says when no disassembly has the linked number"

**KitRequest: `wholesale_price` và `min_stock`**

- Thay đổi:
  - Request tạo kit gửi luôn giá sỉ, và tồn tối thiểu nếu là giỏ (combo không có).
  - Sau khi tạo không còn PATCH SKU nữa. Chỉ khi một giá trị bị sửa sau lần lưu dở thì mới ghi bù.
  - `body satisfies components["schemas"]["KitRequest"]`.
  - Lỗi 422 hiện ngay dưới đúng ô:
    - `wholesale_price` hoặc `min_stock` bị `must_not_be_negative`;
    - `min_stock` bị `not_allowed`.
  - `KitPatch` không có hai field này, nên luồng sửa nháp vẫn dùng PATCH giá và SKU như cũ (`apps/api/internal/kits/recipe_service.go:107-119`).
- File:
  - `features/kits/api/requests.ts`, `features/kits/view-model.ts`
  - `features/kits/api/mutations.ts`, `features/kits/api/adapters.ts`
  - `features/kits/containers/kit-dialog.tsx`
- Test (`kits/containers/kit-dialog.test.tsx`):
  - test tạo giỏ: body có `min_stock: "10"` và `skuPatches` bằng `[]`;
  - "creates the kit once when a later step fails, and sends the edits made before the retry": publish mất trả lời, người dùng sửa tên và tồn tối thiểu rồi bấm lại; lần sau gửi PATCH nháp, PATCH tồn tối thiểu kèm If-Match, rồi publish lại;
  - "sends a combo's wholesale price in the create request and no minimum stock";
  - `it.each` "words the API's refusal of %s as %s under its field" (3 trường hợp).

**Tồn "Không màu" trong e2e**

- `e2e/inventory.spec.ts` chọn "hàng thường" bằng `r.colors.length === 0`. Theo `apps/api/internal/inventory/queries.go:147-198` (`decorate`/`colorStocks`), hàng còn tồn luôn có ít nhất một dòng trong `colors`, và tồn không màu là dòng có `color_id: null`. Vì vậy điều kiện cũ chỉ đúng với hàng không có tồn, nên hai bài (`countableCategory` và bài điều chỉnh giảm) luôn bị skip.
- Đã đổi sang helper `uncolored`: mọi dòng `colors` đều có `color_id === null`. Kiểu `StockRow.colors` cũng đổi theo.
- Chưa chạy được e2e (thiếu `E2E_OWNER_PASSWORD`). Typecheck và lint của spec sạch.

**Nhả hàng giữ có lý do**

- Mục này đã đúng từ trước, so với snapshot gốc không cần đổi gì; đã kiểm tra lại:
  - `release-reservation-dialog.tsx` có ô "Lý do".
  - Lỗi `required` hiện "Cần lý do.", lỗi `too_long` hiện "Lý do tối đa 500 ký tự." (`RELEASE_REASON_MAX`).
  - Mutation gửi `{ reason } satisfies ReleaseBody`.
- Test (`inventory/pages/reservations-page.test.tsx`):
  - "releases a manual hold once a reason is given, and leaves document holds alone": khẳng định `bodies` bằng `[{ reason: "Khách hủy" }]`;
  - `it.each` lỗi 422 theo field.

## Nhãn mới

Không có nhãn mới cho phần xuất toàn bộ dữ liệu: thẻ dùng lại nhãn trạng thái và nút "Tải về" sẵn có.

**Công nợ, tiêu đề drawer**

- "Phiếu thu {số}" và "Phiếu chi {số}"
- "Điều chỉnh công nợ {số}"
- "Biên bản bù trừ {số}"

**Công nợ, câu hiện trong drawer**

- "Không tìm thấy {số}."
- "Chứng từ {số} đã phân bổ"
- "Không phân bổ."
- "Chứng từ được giảm bởi {số}"
- "Không giảm chứng từ nào."

**Công nợ, nhãn ô**

- "Số tiền bù trừ", "Phải thu", "Phải trả", "Sổ", "Lý do"

**Công nợ, nhãn loại điều chỉnh**

- "Tăng nợ", "Giảm nợ", "Xóa nợ"
- "Số dư đầu kỳ khai báo"
- "Điều chỉnh sau tranh chấp"

**Công nợ, tên loại chứng từ:** "Điều chỉnh công nợ" (DN) và "Biên bản bù trừ" (BT).

**Bảng kê:** "Không tìm thấy bảng kê này…" (khi `?id=` không còn tồn tại).

**CRM:** hai câu lỗi của báo cáo doanh thu, cho `customer_ids` quá 100 (`too_many`) và `customer_ids` không hợp lệ (`invalid`).

**Tổng quan** (lấy từ prototype): "Theo loại", "Hạn gần nhất".

**Kho**

- "và {n} dòng khác"
- “Chỉ hàng có màu” cùng “Chưa gán màu” chỉ lọc trên các dòng đã tải; bấm “Xem thêm” để lọc tiếp.
- "Không tìm thấy {số}." (danh sách điều chỉnh, kiểm kê và tháo giỏ)

**Giỏ quà**

- "Giá sỉ không được âm."
- "Tồn tối thiểu không được âm."
- "Combo không có tồn tối thiểu."

## Mục chưa làm và lý do

Không có mục nào trong 13 mục phải bỏ dở. Ba mục dashboard thiếu API (OPEN-13) vẫn được giữ nguyên, đúng như đề bài yêu cầu.

## Kết quả gate (`apps/web`)

- `corepack pnpm typecheck` (`tsc -b`): 0 lỗi.
- `corepack pnpm lint` (`eslint .`): sạch.
- `corepack pnpm test`: **132 file, 948 test, tất cả pass.** Mười lần chạy liên tiếp trên code cuối cùng đều xanh với exit 0, và không có unhandled error nào.
- Không chạy e2e (thiếu `E2E_OWNER_PASSWORD`). Không có lệnh nào chứa "build".

**Lỗi chập chờn đã sửa.** Trước khi sửa, khoảng 2 trên 14 lần chạy có exit 1 dù mọi test đều pass.

- Nguyên nhân là unhandled error `ReferenceError: document is not defined`, có từ trước. Timer blur của `shared/ui/combobox.tsx` (120 ms) vẫn chạy sau khi `features/admin/pages/feature-flags-page.test.tsx` đã đóng jsdom.
- Cách sửa: giữ id của timer trong một ref, hủy timer khi component unmount, và hủy timer cũ trước mỗi lần blur mới. Hành vi đóng popup khi rời ô không đổi.
- Không thêm test riêng cho phần này. Muốn tái hiện lỗi phải mô phỏng việc jsdom bị đóng, và test như vậy chỉ kiểm tra cách cài đặt. Bằng chứng là mười lần chạy toàn bộ suite đều sạch.

## Ghi chú

- Sau khi lead cho phép sửa mọi file dùng chung, đợt này có sửa thêm `shared/ui/combobox.tsx`, `features/settings/**` (mục 10) và `e2e/inventory.spec.ts`.
- Ngoài file báo cáo này, không có thay đổi nào ngoài `apps/web`. Không chạy `gen:api`.

## Sửa theo review

Review: `plans/reports/code-reviewer-261019-0604-web-adopt-api-gaps-review.md`. Mọi thay đổi nằm trong `apps/web/src`. Hợp đồng API giữ nguyên, không chạy `gen:api`, không sửa `e2e/`.

### M1. Khóa riêng cho từng dòng khi publish kit và gửi báo giá

- `features/kits/api/mutations.ts` (`usePublishKit`) gửi khóa `${idempotencyKey}:${product_id}`. `features/sales/api/quote-mutations.ts` (`useQuoteTransition`, chỉ lệnh `send`) gửi khóa `${idempotencyKey}:${quote.id}`. Cách làm giống `useReorder`.
- Test `kits/pages/recipes-page.test.tsx`: "publishes another kit while one publish has no answer, under a key of that kit". Server giả gắn mỗi khóa với path đầu tiên, gặp khóa cũ trên path khác thì trả 409.
- Test `sales/api/create-documents.test.tsx`: "sends another quote while one send has no answer, under a key of that quote".
- Cả hai test đều đỏ với code cũ (đã thử), xanh với code mới.
- Đánh đổi: khi dòng B thành công, khóa của hook đổi sang khóa mới. Lần bấm lại dòng A sau đó sẽ dùng khóa mới, không dùng lại khóa cũ của A. Nếu lần A trước đó thật ra đã ghi, `If-Match` theo version sẽ chặn lần ghi thứ hai (412) và màn hình tải lại dữ liệu. Vì vậy không thể publish hay gửi hai lần.

### M2. Thêm khách nhanh dùng `createOnce` và hỏi trước khi đóng khi chưa rõ đã ghi

- `features/crm/api/mutations.ts`: `useQuickCustomer` giữ body đã gửi bằng `createOnce`. Nếu body đổi sau khi mất câu trả lời thì PATCH kèm `If-Match` (dùng chung helper `updateCustomer` với `useSaveCustomer`). Hook trả thêm `unsettled`.
- Review cho rằng `useSaveCustomer` đã có cờ `unsettled`, nhưng thực tế chưa có, nên đã thêm cho cả hai hook. Sau đó cả `customer-quick-dialog.tsx` và `customer-dialog.tsx` đều truyền `closeWarning: CLOSE_UNSETTLED_MESSAGE`. Trong dialog nhanh, nút "Quay lại" bị khóa khi `unsettled`, vì nút này nằm ngoài câu hỏi đóng.
- Test trong `customer-quick-dialog.test.tsx`:
  - Mất câu trả lời, sửa số điện thoại, bấm lại: POST thứ hai giống hệt POST đầu, PATCH mang số mới với `If-Match` version 1.
  - Hỏi trước khi rời dialog khi chưa rõ đã ghi.
- `customer-dialog.test.tsx`: test mất câu trả lời giờ kiểm tra thêm câu hỏi đóng.

### M3. Xóa dữ liệu cũ khi đổi phiên

- Theo quyết định của lead, phía API giữ nguyên.
- `features/auth/api/mutations.ts`: `useSessionChanged` gọi `queryClient.clear()` và `resetStoredExports()` mỗi khi đổi phiên. Các trường hợp: đăng nhập (kể cả admin và bước TOTP), đăng xuất (kể cả khi phiên đã hết, 401), và đặt lại mật khẩu.
  - Trước đây chỉ bỏ các key auth/admin. Tham số `kind` không còn cần nên đã bỏ khỏi `useAuthStep`.
- `features/reports/index.ts` export thêm `resetStoredExports`.
- Test mới trong `auth/components/logout-button.test.tsx`:
  - đăng xuất thì quên cache và các bản xuất tab đang theo dõi;
  - người đăng nhập sau một phiên đã hết không thấy bản xuất của người trước.

### M4. `paymentCodeOf` kiểm tra theo đúng thứ tự của server

- `features/sales/api/adapters.ts` kiểm tra theo thứ tự `paymentStateOf` trong `apps/api/internal/sales/status.go`: trả thừa, rồi trả đủ (kể cả khi total bằng 0), rồi chưa trả, rồi trả một phần.
- File mới `features/sales/api/adapters.test.ts`:
  - sáu cặp total/paid, trong đó có `0/0`, cho kết quả "paid";
  - một hóa đơn `cancelled_remaining` chưa giao, chưa trả.
- Hệ quả cần biết: form hóa đơn mới để trống (tổng 0) giờ hiện "Đã thanh toán đủ", giống cách server đọc. Prototype `payStatus` thì hiện "Chưa thanh toán" vì nó xét `paid <= 0` trước.

### L1–L7

- **L1 (đã sửa).** `features/debt/api/mutations.ts`: `useCollectionNote` và `useCreateStatement` dùng `createOnce` theo khóa của intent, cùng mẫu với `useMatchInvoice` bên procurement.
  - Body đã mất câu trả lời được gửi lại trước, nên không còn bị 409.
  - Nếu form đã sửa sau đó, hook báo lỗi một phần với câu "…của lần bấm trước… chưa được lưu/lập".
  - Hai dialog `debt-note-dialog.tsx` và `new-statement-dialog.tsx` có thêm câu hỏi đóng khi chưa rõ đã ghi.
  - Test mới trong `debt-note-dialog.test.tsx` và `statements-page.test.tsx`: body thứ hai giống body đầu, cùng khóa, dialog vẫn mở.
- **L2 (đã sửa).** Khóa consent thành `${idempotencyKey}:consent-${channel}-${granted}-${16 ký tự hex đầu của sha256 body}`.
  - Không đưa nguyên văn nguồn vào khóa, vì server chỉ nhận ASCII in được và tối đa 200 ký tự, mà nguồn là chữ tự do có dấu.
  - Khi nguồn được sửa sau một lần mất câu trả lời, bản ghi đồng ý mới đi dưới khóa của chính nó. Có thể có hai bản ghi đồng ý nếu bản đầu thật ra đã lưu. Điều này chấp nhận được vì consent là lịch sử chỉ thêm, và bản mới nhất mang nguồn mà người dùng đã sửa.
  - Test mới trong `customer-dialog.test.tsx`.
- **L3 (không sửa).** `GET /payments` không có tham số `purpose` (chỉ có `direction`, `party_id`, `status`, `unallocated`, `doc_no`), nên không lọc ở server được nếu không đổi hợp đồng API. Review cũng chấp nhận cách hiện tại.
- **L4 (đã sửa).** `features/debt/containers/payment-drawer.tsx`: dòng phân bổ bị đảo hiện chú thích "Đã đảo". Dòng đảo hiện "Đảo phân bổ" kèm số âm, đúng như server lưu (`amount < 0` khi có `reversal_of`). Test mới trong `advances-page.test.tsx`.
- **L5 (đã sửa).** `features/sales/pages/invoices-page.tsx` hiện ghi chú "Bộ lọc và dòng tổng chỉ tính trên các hóa đơn đã tải; bấm “Xem thêm” để tính tiếp." khi còn trang chưa tải.
  - Không lọc ở server được: `/invoices` không lọc theo thanh toán, và mỗi lần chỉ nhận một `status`.
  - Test mới trong `invoices-page.test.tsx` kiểm tra có ghi chú khi còn trang, và không có khi đã hết.
- **L6 (không sửa).** Hai lần đọc `/export-jobs` dùng hai schema khác nhau, và việc tách là có chủ ý.
  - Thẻ xuất toàn bộ dữ liệu ở settings chỉ parse các field nó cần.
  - Nếu dùng chung `exportJobSchema` của reports (`report_key` là enum), một dòng xuất báo cáo có key mà bản web này chưa biết sẽ làm hỏng cả danh sách. Khi đó thẻ settings sẽ âm thầm không tìm lại được bản xuất.
  - Cái giá là thêm một request khi mở cả hai màn.
- **L7 (đã sửa).** `features/kits/containers/kit-dialog.tsx` đưa lỗi `retail_price` lên ô "Giá bán lẻ" với câu `KIT_MESSAGES.retailNegative` ("Giá bán lẻ không được âm."). Lỗi tự xóa khi sửa giá. Test thêm một dòng vào `it.each` của `kit-dialog.test.tsx`.

### Gate sau khi sửa (`apps/web`)

- Các suite liên quan đều xanh khi chạy riêng: crm 51, debt 83, kits 42, sales, auth/logout-button.
- `corepack pnpm typecheck`: **đỏ, nhưng không do phần này.** Các lỗi nằm ở `src/features/sales/components/line-items/line-items-editor.test.tsx` (dòng 75, 76, 81: chuỗi được truyền vào chỗ cần kiểu `Qty`). File này vừa được sửa lúc 06:24, không phải bởi tôi. Trước khi file đó đổi, `tsc -b` trên code của tôi đã sạch.
- `corepack pnpm lint`: **đỏ, nhưng không do phần này.** Có hai lỗi `react-refresh/only-export-components` ở `src/app/layouts/sidebar.tsx:11-12`, file mà uifix sửa lúc 06:03. Các file tôi sửa không có lỗi lint.
- `corepack pnpm test`, lần 1: 141 file, 1016 pass, 7 đỏ. Lần 2: 1014 pass, 9 đỏ. Mọi test đỏ nằm ở file mà uifix đang sửa dở:
  - `auth/pages/admin-login-page`, `login-page`, `mfa-page`, `reset-password-page`: test lỗi theo ô (chỉ lần 1). `shared/ui/field.tsx` đổi lúc 06:08. Chạy riêng lại thì các test này xanh.
  - `settings/pages/account-page`: "locks the books through a date", "sends the owner transfer invitation…", "shows no sent notice when the owner transfer invitation is refused". Không thấy toast/`role=status`. `period-lock-card.tsx` (06:12), `owner-transfer-card.tsx` (06:13), `toaster.tsx` (06:12) và `date-picker.tsx` (06:20) đều vừa đổi. Đã kiểm tra rằng các luồng ở trang này không gọi `useSessionChanged`.
  - `pos/offline/components/offline-callouts` (5 test, chỉ lần 2): không thấy `role=status`. Các component trong thư mục này đổi lúc 06:33, đúng lúc đang chạy lần 2.
  - `admin/pages/tenants-page` "finds a store by its slug…" (chỉ lần 2): file test đổi lúc 06:33. Chạy riêng lại thì xanh.
- Vì vậy chưa có hai lần chạy toàn bộ suite xanh liên tiếp. Cần chạy lại sau khi uifix-a/uifix-b xong.

Status: DONE_WITH_CONCERNS
Summary: Đã sửa M1–M4 và L1, L2, L4, L5, L7, mỗi mục có test, và các test đó xanh. L3 và L6 không sửa, lý do ghi ở trên. typecheck, lint và test toàn bộ còn đỏ ở các file uifix đang sửa dở, không do phần này.
Concerns/Blockers: Gate toàn bộ chưa xanh hai lần. Lỗi nằm ở `line-items-editor.test.tsx`, `sidebar.tsx`, account-page, offline-callouts và các test lỗi theo ô ở auth, tất cả là phần uifix đang sửa. Cần chạy lại sau khi họ xong. Ngoài ra, M4 làm form hóa đơn mới để trống hiện "Đã thanh toán đủ" (giống cách server đọc), lead có thể muốn xem lại.
