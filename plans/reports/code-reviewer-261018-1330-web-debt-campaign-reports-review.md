# Review phase 18 phần A (web): công nợ, tổng quan, thông báo, chiến dịch, CRM, báo cáo

Ngày: 2026-10-18 · Reviewer: code-reviewer · Chỉ review, không sửa code.

## Phạm vi

- Thư mục: `apps/web/src/features/{debt,dashboard,notifications,campaign,crm,reports}`.
- Đối chiếu với:
  - hợp đồng chung `shared/hooks/use-commit.ts`, `use-idempotent-mutation.ts`, `shared/api/{http,api-error}.ts`, `shared/lib/decimal.ts`;
  - `apps/api/api/openapi.yaml`, gồm debt, payments, debt-adjustments, statements, export-jobs, campaigns, customers;
  - server `apps/api/internal/{debt,reporting,notification,campaign}`;
  - phase file `phase-18-web-debt-campaign-reports-settings.md` và báo cáo implementer `fullstack-developer-261018-1230-web-debt-campaign-reports.md`.
- Chạy kiểm tra: `corepack pnpm exec vitest run src/features/{debt,dashboard,campaign,crm,notifications,reports}` cho 25 file và 106 test, tất cả đều qua. Không chạy gate và không chạy lệnh build.

## Đánh giá tổng quan

Phần tiền và idempotency được làm cẩn thận:

- Phân bổ dùng Decimal và đi từ hạn cũ nhất trước, xem `debt/allocation.ts`.
- Số liệu tổng quan và báo cáo lấy nguyên từ API.
- Header Idempotency-Key và If-Match đúng theo OpenAPI ở mọi endpoint đã kiểm.
- Write-off giữ cùng một key qua reauth và qua bước xác nhận ngoại lệ. Subject và hash khớp server (`debt/adjustments.go:160`), và có test chứng minh việc này (`write-off-dialog.test.tsx:23-73`).
- Không có số học float. Các chỗ dùng `Number()` chỉ áp cho ngày, số nguyên và số ngày hạn.

Rủi ro production tập trung ở ba chỗ:

1. Lỗi server chưa được map nên hiện nguyên câu tiếng Anh cho người dùng.
2. Export job mất khi rời trang, và không có đường nào khác để tải file.
3. Có nhiều chỗ N+1. Implementer chưa khai báo hết các chỗ này.

Không có finding Critical.

## Critical

Không có.

## High

### H1. File xuất bị mất khi rời trang báo cáo và không có đường tải lại

- Vị trí:
  - `apps/web/src/features/reports/use-export-job.ts:46`: `current` nằm trong `useState` của component.
  - `apps/web/src/features/notifications/kinds.ts:42-65`: `notificationTarget` trả `null` cho `export_ready`.
- Kịch bản lỗi:
  1. Chủ cửa hàng bấm "Xuất CSV" ở báo cáo bán hàng có khoảng thời gian dài, job ở trạng thái `queued`.
  2. Họ chuyển sang "Khách còn nợ" để làm việc khác trong lúc chờ.
  3. Khi quay lại, `current` đã về `null` và panel trống.
  4. Server vẫn tạo file rồi phát thông báo `export_ready`, nhưng thông báo này không có nút "Mở".
  5. Không có danh sách export job nào trên web, nên người dùng không có cách lấy file. Họ chỉ có thể xuất lại, và việc này chiếm thêm một chỗ trong hàng đợi (`export_queue_full`).
- Lệch spec: phase file ghi "job id kept in query cache, survives navigation".
- Cách sửa:
  1. Giữ job id đang chạy theo `reportKey` ở cấp trên component. Có thể dùng `queryClient.setQueryData(["reports","export-current",reportKey], {id, round})` rồi đọc lại bằng `useQuery` với `enabled:false`, hoặc lưu vào `sessionStorage`. `useExportJob` khởi tạo `current` từ chỗ lưu đó.
  2. Cho `export_ready` một target. Ví dụ `/reports/<trang của report_key>?export=<entity_id>`, và trang đó gọi `show(id)` rồi đọc lại trạng thái. Nếu chưa có cách map `report_key`, có thể làm trang `/reports/exports/:id` gọi `GET /export-jobs/{id}` rồi hiện nút tải.
  3. Thêm test: rời trang và quay lại thì job vẫn được poll tiếp; bấm "Mở" trên thông báo `export_ready` thì thấy nút tải.

### H2. Mã lỗi chưa map hiện nguyên câu tiếng Anh của server

- Vị trí:
  - `apps/web/src/shared/api/api-error.ts:214-218`: `CODE_MESSAGES[e.code] ?? e.message`.
  - `apps/web/src/features/debt/api/adapters.ts:96-115`: `DEBT_ERROR_MESSAGES`.
- Mã có thể trả về cho các màn này nhưng chưa có câu tiếng Việt. Câu tiếng Anh tương ứng lấy từ server:

  | Mã | Câu hiện ra hôm nay |
  |---|---|
  | `payment_reversed` | "The payment has been reversed" |
  | `payment_changed` | "The payment changed while it was being reversed; try again" |
  | `payment_pending_verification` | Câu tiếng Anh của server |
  | `payment_method_inactive` | "The payment method is not active" |
  | `payment_method_not_found` | Câu tiếng Anh của server |
  | `payment_not_found` | Câu tiếng Anh của server |
  | `party_not_found` | Câu tiếng Anh của server |
  | `nothing_owed` | "Nothing is open on this ledger for the party" |
  | `exception_confirmation_used` | "This confirmation was already used; confirm again" |
  | `idempotency_key_in_progress` | Câu tiếng Anh của server |
  | `idempotency_key_reused` | Câu tiếng Anh của server |
  | `not_found` (chung) | Câu tiếng Anh của server |
  | `report_unknown` (báo cáo) | Câu tiếng Anh của server |
  | `invalid_cursor` (báo cáo) | Câu tiếng Anh của server |

- Kịch bản lỗi:
  1. Ở tab A, chủ cửa hàng hủy phiếu thu PT-0012, là một khoản ứng trước.
  2. Ở tab B, trang "Ứng trước & cọc" vẫn còn dòng PT-0012. Họ bấm "Phân bổ" rồi "Ghi phân bổ".
  3. Toast hiện "**Chưa ghi sổ.** The payment has been reversed".
  4. Một trường hợp khác: trong `PayDialog`, phương thức đầu danh sách (`pay-dialog.tsx:49`) vừa bị tắt ở Cài đặt. Toast hiện "The payment method is not active".
  5. Bấm đúp nhanh qua hai tab với cùng một intent thì gặp `idempotency_key_in_progress`, cũng hiện tiếng Anh.
- Cách sửa:
  1. Thêm các mã debt ở trên vào `DEBT_ERROR_MESSAGES`. Ví dụ:
     - `payment_reversed`: "Phiếu đã bị hủy. Tải lại để xem số chưa phân bổ."
     - `payment_method_inactive`: "Phương thức thanh toán đã ngừng dùng. Chọn phương thức khác."
     - `nothing_owed`: "Đối tác không còn khoản nợ nào để giảm."
  2. Thêm các mã dùng chung (`idempotency_key_in_progress`, `idempotency_key_reused`, `not_found`, `exception_confirmation_used`) vào `CODE_MESSAGES` ở `shared/api/api-error.ts`, vì mọi feature đều gặp các mã này.
  3. Thêm `report_unknown` và `invalid_cursor` vào `REPORT_ERROR_MESSAGES`.
  4. Đổi fallback cuối của `errorMessage` thành câu chung tiếng Việt kèm mã lỗi để hỗ trợ tra cứu, thay vì `e.message`. Ví dụ: "Máy chủ từ chối thao tác (mã payment_reversed)."
  5. Thêm một test bảng: mỗi mã trong mục `responses` của OpenAPI cho các endpoint thuộc phase 18 đều có câu tiếng Việt.

## Medium

### M1. CRM: mỗi dòng khách gọi 2 request, tức 100 request cho 50 dòng

- Vị trí:
  - `apps/web/src/features/crm/pages/customers-page.tsx:55,93,98-99,106`: `ConsentPill` gọi `useConsents`, cột "Lần mua gần nhất" gọi `useLastPurchase`.
  - `apps/web/src/features/crm/api/queries.ts:68-87`.
- Kịch bản lỗi:
  1. Trang "Khách hàng" có 50 dòng mỗi trang, nên phát 50 lệnh `GET /customers/{id}/timeline?kind=purchase&limit=1` và 50 lệnh `GET .../consents`.
  2. Bấm "Tải thêm" thì thêm 100 lệnh nữa.
  3. Mỗi lần lưu khách hoặc reorder làm invalidate `["crm"]`, và toàn bộ các query trên chạy lại.
  4. Với rate limit theo tenant, người dùng sẽ gặp `rate_limited` ngay trên một màn chỉ để đọc. Trên mạng 3G, các cột sẽ nhấp nháy rất lâu.
- Implementer chỉ khai báo N+1 của timeline. N+1 của consents chưa được khai báo.
- Cách sửa:
  - API gap: bổ sung `last_purchase_on`, `last_purchase_doc_no` và `marketing_consents` vào response `GET /customers`, hoặc thêm `include=last_purchase,consents`.
  - Giải pháp tạm ở client:
    - đặt `staleTime` cho hai query này, ví dụ 5 phút;
    - chỉ invalidate đúng key của khách vừa sửa thay vì cả prefix `["crm"]`;
    - chỉ render `ConsentPill` và last purchase cho các dòng đang hiển thị.

### M2. "Ứng trước & cọc" tải toàn bộ đối tác rồi gọi ledger cho từng đối tác

- Vị trí: `apps/web/src/features/debt/api/queries.ts:102-127`.
- Cách hoạt động: `useLoadAll` chạy trên cả hai danh sách party, rồi `useQueries` gọi `GET /debts/parties/{id}` cho mỗi đối tác có ứng trước.
- Kịch bản lỗi:
  - Một cửa hàng có 300 khách đang có cọc mùa Tết sẽ phát 300 request khi mở trang, cộng thêm các request phân trang.
  - Sau mỗi phiếu thu, `POSTING_KEYS` invalidate `["debt"]` và tất cả chạy lại.
- Cách sửa:
  - API gap: thêm `GET /payments?unallocated=true&direction=` hoặc `GET /debts/advances` trả về các phiếu còn chưa phân bổ cùng tên đối tác. Ghi lại gap này cho backend.
  - Trước khi có endpoint: chỉ gọi ledger khi người dùng mở dialog phân bổ, vì trang danh sách chỉ cần `payment` và `partyName`, và hai giá trị này đã có trong `partySummary.advance`. Có thể hiện tổng ứng trước theo đối tác thay vì theo từng phiếu.

### M3. "Đối soát" gọi một GET phiên bản cho mỗi bảng kê

- Vị trí: `apps/web/src/features/debt/api/queries.ts:228-240`, với `staleTime: Infinity`.
- Kịch bản lỗi:
  - Mỗi dòng bảng kê gọi `GET /statements/{id}/versions/{v}`. Khi `useLoadAll` chạy vì có `?doc=` (`statements-page.tsx:31`), có thể lên tới 200 request.
  - `staleTime: Infinity` không cứu được trường hợp invalidate `["debt"]` sau mỗi lệnh gửi hoặc xác nhận bảng kê.
- Cách sửa:
  - API gap: response `GET /statements` nên kèm `party_name`, `opening_balance` và `closing_balance` của phiên bản hiện tại.
  - Ở client: loại key phiên bản (vốn bất biến) ra khỏi prefix bị invalidate, ví dụ đặt key `["statement-version", id, v]` không nằm dưới `["debt"]`.

### M4. Doanh thu theo khách đọc tuần tự mọi trang báo cáo, không có giới hạn

- Vị trí: `apps/web/src/features/crm/api/queries.ts:44-65`, mỗi trang 200 dòng.
- Kịch bản lỗi:
  - Cuối mùa, một cửa hàng có 3.000 khách phát sinh doanh thu sẽ phải chờ 15 request nối tiếp trước khi cột "Doanh thu mùa" hiện.
  - Nếu trang thứ 10 lỗi thì toàn bộ cột về "—".
  - Mỗi lần invalidate `["crm"]` lại đọc lại từ đầu.
- Cách sửa:
  - API gap: thêm `customer_ids=` (lọc theo danh sách id của trang đang xem) cho `GET /reports/revenue?group_by=customer`, hoặc thêm `season_revenue` vào `GET /customers`.
  - Giải pháp tạm: đặt trần số trang, ví dụ 10, rồi hiện "—" kèm ghi chú. Đưa key doanh thu ra ngoài `["crm"]` để lưu một khách không kéo theo việc đọc lại cả báo cáo.

### M5. "Thu tiền khách" ở header không chọn được khách chưa có số dư

- Vị trí: `apps/web/src/features/debt/containers/pay-dialog.tsx:41-45`. Danh sách chọn lấy từ `useDebtParties` (`GET /debts/parties`), vốn chỉ trả đối tác đang có số dư. Danh sách này được tải hết bằng `useLoadAll` vào một `<select>` native.
- Kịch bản lỗi:
  - Một khách mới đến đặt cọc 5.000.000 cho đơn Tết. Chủ cửa hàng bấm "Thu tiền khách" ở Tổng quan hoặc ở trang "Khách còn nợ".
  - Khách này không có trong danh sách, nên không thể ghi phiếu cọc.
  - Cách duy nhất còn lại là chuyển chip "Tất cả khách", mở drawer khách rồi bấm "Thu tiền". Prototype không có luồng vòng như vậy.
  - Ngoài ra, một `<select>` chứa hàng trăm đối tác thì không tìm kiếm được.
- Cách sửa: dùng `Combobox` với `customerSource` hoặc `supplierSource` như `new-statement-dialog.tsx:48`. Các nguồn này tìm trong toàn bộ danh bạ đang hoạt động, và dialog không cần tải hết danh sách party nữa.

### M6. `validation_failed` báo "Kiểm tra lại các ô được đánh dấu." nhưng không ô nào được đánh dấu

- Vị trí: 11 lời gọi `useCommit` trong `debt/api/mutations.ts`, `campaign/api/mutations.ts` và `notifications/api/mutations.ts` đều không truyền `setFieldError`. Phần CRM đã làm đúng.
- Kịch bản lỗi:
  - Trong `DebtNoteDialog`, khi `promised_date` nằm trước ngày nghiệp vụ, server trả `validation_failed` với `fields: {promised_date: "..."}`. Toast báo kiểm tra các ô được đánh dấu, nhưng ô "Khách hẹn trả ngày" không đỏ.
  - Tương tự với `note` quá dài hay `reason_id` trong write-off, và `until` khi hoãn thông báo.
- Cách sửa: truyền `setFieldError` từ các dialog có form (pay, write-off, debt-note, new-statement, dispute) và map tên field của API sang `Field.error`. Nếu không làm vậy, cần có câu riêng cho `validation_failed` nêu tên field, dựa trên `e.fields`.

### M7. Poll export im lặng: sau khi hết phiên vẫn poll 3 phút và không chuyển sang đăng nhập

- Vị trí:
  - `apps/web/src/features/reports/api/queries.ts:83-84`: `fetchExportJob` dùng `silent: true`.
  - `apps/web/src/features/reports/use-export-job.ts:57-59`: `retry: false` và `refetchInterval` chỉ dừng khi job kết thúc hoặc đủ 90 lần.
- Kịch bản lỗi:
  1. Phiên đăng nhập hết hạn trong lúc job đang chờ.
  2. Mỗi 2 giây nhận một 401. Handler toàn cục không chạy vì cờ silent, nên không có màn đăng nhập lại.
  3. Sau 90 lần, tức 3 phút, panel báo "stalled".
  4. Trường hợp 404 (job bị xóa) cũng tương tự.
- Cách sửa: trong `refetchInterval`, dừng khi `q.state.error` là ApiError có status 401, 403, 404 hoặc 410. Với lỗi 401, rethrow hoặc gọi handler phiên toàn cục, hoặc bỏ `silent` cho lỗi 401. Hiện thông báo theo mã lỗi bằng `reportErrorMessage`.

### M8. Liên kết PT/PC luôn mở "Ứng trước & cọc" nên phiếu đã phân bổ hết hiện "Không tìm thấy"

- Vị trí: `apps/web/src/features/debt/doc-openers.ts:10-13`, dẫn tới `pages/advances-page.tsx:18-22,62`.
- Kịch bản lỗi:
  1. Ở drawer khách, cột "Thanh toán", hay ở bất kỳ DocNo nào khác, người dùng bấm PT-0031. Đây là phiếu thu đã phân bổ hết vào hóa đơn.
  2. Ứng dụng điều hướng tới `/debts/advances?doc=PT-0031`.
  3. Danh sách chỉ có phiếu còn chưa phân bổ, nên hiện "Không tìm thấy PT-0031."
  4. Như vậy mọi phiếu thu bình thường đều không mở được. Implementer chỉ khai báo trường hợp DN/BT.
- Cách sửa:
  - API gap: thêm `GET /payments/{id}` và tra theo số phiếu, ví dụ `GET /payments?doc_no=`.
  - Trước khi có endpoint: khi không tìm thấy, `DocNotFoundDrawer` nên giải thích "Phiếu đã phân bổ hết; xem ở sổ của đối tác" thay vì "Không tìm thấy", giống cách ledger đang làm với DN (`debt-ledger-view.tsx:103-107`).

## Low

| # | Vị trí | Vấn đề và kịch bản | Cách sửa |
|---|---|---|---|
| L1 | `crm/pages/customers-page.tsx:31,35` | `today` dùng `todayBusinessDate()` (ngày trên thiết bị) cho tới khi tenant tải xong. `useRevenueByCustomer(today)` chạy với ngày thiết bị trước, rồi chạy lại với ngày nghiệp vụ. Nếu hai ngày rơi vào hai mùa khác nhau (quanh 01/10), cột sẽ nhảy số liệu. Phần debt đã sửa lỗi này. | Bật `enabled: tenant.data !== undefined` cho query doanh thu, giống `schedule-page.tsx:32`. |
| L2 | `debt/containers/new-statement-dialog.tsx:24-26`, `debt-note-dialog.tsx:22-26`, `statements-page.tsx:28` | `useState` khởi tạo kỳ hoặc ngày hẹn từ `today`, mà `today` có thể vẫn là ngày thiết bị nếu dialog mở trước khi tenant tải xong. Giá trị ban đầu bị kẹt theo ngày đó. | Chỉ render dialog khi đã có `tenant.data`, hoặc tính lại mặc định khi `today` đổi và người dùng chưa sửa. |
| L3 | `notifications/api/mutations.ts:46` | `until` của "Hoãn 1 ngày" tính bằng `Date.now()` trên thiết bị. Nếu đồng hồ máy lệch hơn một ngày, server trả `validation_failed` ("Kiểm tra lại các ô được đánh dấu.") mà không có ô nào. | Cho phép server nhận `snooze_days: 1`, hoặc tính từ thời điểm server (header `Date` hoặc `tenant.now`). |
| L4 | `notifications/kinds.ts:29-37` | Chip chỉ hiện các loại đang có thông báo, trong khi spec yêu cầu đủ bộ chip cố định có số đếm. Hệ quả nhỏ: bố cục chip thay đổi giữa các lần tải. | Render đủ `KIND_LABELS` theo thứ tự, loại nào không có thì hiện số 0. |
| L5 | `notifications/kinds.ts:58-59` | `statement_disputed` mở `/debts/statements` mà không mở đúng bảng kê, dù `entity_id` là id của bảng kê. Tương tự `lot_*` không truyền lô. | Hỗ trợ `?id=` trong `useDocParam` của trang đối soát (đã có `docs.open(id)`), hoặc yêu cầu API trả `doc_no` trong thông báo. |
| L6 | `notifications` (mở thông báo chưa đọc) | Nếu `:read` lỗi, toast hiện "Chưa ghi sổ." và không điều hướng. Đánh dấu đã đọc không phải là ghi sổ. | Điều hướng trước rồi mark-read ở nền, hoặc dùng toast riêng "Chưa đánh dấu đã đọc". |
| L7 | `reports/components/export-panel.tsx:35-47` | Thẻ `<a href={download_path}>` cho phép middle-click hoặc "mở tab mới" bỏ qua bước đọc lại trạng thái trong `download()`. Nếu link đã hết hạn, người dùng thấy trang JSON lỗi 410 tiếng Anh. | Dùng `<button>` hoặc `preventDefault` cho mọi kiểu click, hoặc server trả trang HTML tiếng Việt cho mã 410. |
| L8 | `debt/api/adapters.ts:111-115`, `campaign/api/adapters.ts:78-83` | `new ApiError({...})` không chép `retryAfterSeconds`, nên `rateLimitMessage` mất số giây cần chờ. | Chép đủ các thuộc tính, hoặc thêm `ApiError.withMessage(msg)`. |
| L9 | `crm/pages/customers-page.tsx` (placeholder ô tìm) | Placeholder nhắc tới "nhóm", nhưng `q` của server chỉ tìm theo tên, số điện thoại và mã số thuế. Gõ tên nhóm sẽ không ra kết quả nào. | Bỏ chữ "nhóm" khỏi placeholder, hoặc thêm bộ lọc nhóm. |
| L10 | `dashboard/components/debt-band.tsx:57` | "Quá hạn 0 ₫" vẫn tô đỏ ở thẻ "Khách còn nợ" (thẻ phía NCC đã xử lý trường hợp 0). | Dùng cùng điều kiện `dec(ar.overdue).gt(0)` như dòng 62. |
| L11 | `dashboard/pages/dashboard-page.tsx:40-41,88-97` | Khi đổi kỳ mà request lỗi, `shown` vẫn là dữ liệu của kỳ trước (placeholder) và nằm ngay dưới PeriodBar đang hiện kỳ mới, cạnh callout lỗi. Người dùng dễ đọc nhầm số. | Khi `dash.isError && dash.isPlaceholderData` thì ẩn các band hoặc làm mờ chúng. |
| L12 | `debt/containers/debt-ledger-view.tsx:45,86` | `aging.isPending` vẫn `true` khi `asOf === null`. Nếu tải tenant lỗi, bảng xoay mãi mà không có thông báo. | Hiện lỗi tenant, hoặc dùng `isLoading`, vốn là false khi query bị disabled. |
| L13 | A11y: `debt-party-table.tsx:61-71`, `saved-reports-menu.tsx:74-90`, `allocate-advance-dialog.tsx:64` | Mỗi dòng lặp lại nút "Thu tiền", "Bảng kê", "Áp dụng" hoặc ô nhập "Phân bổ" với cùng tên truy cập. Trình đọc màn hình không phân biệt được các dòng. | Thêm `aria-label={\`Thu tiền ${r.name}\`}` và `aria-label={\`Phân bổ ${d.source_doc_no}\`}` (pay-dialog có cùng mẫu). |

## API gap: thiếu Idempotency-Key, retry có thể tạo bản ghi trùng

Phía web đang làm đúng theo OpenAPI, nên đây là ghi nhận cho backend:

- `POST /statements`;
- `POST /customers/{id}/collection-notes`;
- `POST /customers/{id}/reorder-drafts`;
- `POST /customers`.

Kịch bản: mạng rớt sau khi server đã ghi. Người dùng bấm lại và tạo bảng kê thứ hai, ghi chú nhắc nợ thứ hai, hoặc hóa đơn nháp thứ hai.

Riêng `useSaveCustomer` đã chặn được trùng lặp trong cùng một dialog bằng ref `saved` (có test ở `customer-dialog.test.tsx:194`). Các luồng còn lại thì chưa.

Đề nghị: thêm tham số `IdempotencyKey` cho bốn endpoint này. Web chỉ cần đổi sang `useCommit` có key.

## Edge case do scout tìm ra

- **Invalidate rộng.** `POSTING_KEYS` invalidate cả `["debt"]`, `["sales"]`, `["reports"]` và `["dashboard"]`. Kết hợp với M1–M4, mỗi phiếu thu có thể kéo theo hàng trăm request đọc lại.
- **Sửa thân request sau một lần bị từ chối nhưng vẫn giữ key.** Ví dụ trong PayDialog: lần ghi đầu bị 422, người dùng sửa số tiền rồi ghi lại với cùng key. Theo `use-idempotent-mutation.ts:5-8`, server chỉ lưu các reply 2xx, nên trường hợp này an toàn.
  - Ngoại lệ: lần đầu mất mạng sau khi server đã ghi (2xx đã được lưu), người dùng sửa số tiền rồi bấm lại. Server trả `idempotency_key_reused`, và câu báo hiện bằng tiếng Anh (xem H2).
  - Câu tiếng Việt cho mã này nên hướng người dùng tải lại để kiểm tra phiếu đã ghi.
- **Bảng kê mới tạo.** `docs.open(s.id)` (`statements-page.tsx:111`) chỉ mở drawer sau khi danh sách refetch có dòng mới. Hiện tại bảng kê mới nằm ở trang đầu nên vẫn ổn. Nếu thứ tự sắp xếp đổi thì drawer sẽ không mở.
- **Checklist chiến dịch.** Ô checkbox bị disable khi `toggle.pending`, nên không mất click. Đây là cách làm đúng.

## Không phải lỗi (đã kiểm chứng)

- **Ẩn giá vốn và lãi gộp theo quyền.** PRD 1.1 chỉ có một vai trò là chủ cửa hàng, và server không có quyền riêng cho giá vốn. Trang lãi gộp không gate là đúng phạm vi hiện tại.
- **Write-off.**
  - Phần xác nhận ngoại lệ có `doc_type: "obligation"` và `doc_version`. Hash payload được tính trên thân request không có `exception_confirmation_id`, khớp `debt/adjustments.go:160`.
  - Key được giữ nguyên qua 403 reauth và 409 `exception_required`, đã test ở `write-off-dialog.test.tsx:69-71`.
- **Header theo OpenAPI.** Payments, allocations và debt-adjustments gửi key. Statements gửi If-Match và không gửi key. Campaign activate và close gửi If-Match. Checklist, notifications, export và saved reports không gửi header nào.
- **Link tải file.** `download_path` là `/api/v1/export-jobs/{id}/download`. URL có chữ ký chỉ nằm trong redirect 302, nên web không giữ URL dài hạn nào. Polling dừng khi unmount, không rò timer (có test ở `use-export-job.test.tsx:165`).
- **Số liệu.**
  - Tổng quan, tuổi nợ, lịch thu/trả và báo cáo lấy nguyên số từ API.
  - Riêng "Hạn mức còn lại" (`party-drawer.tsx:62-63`) được tính ở client bằng Decimal. Implementer đã khai báo việc này.
  - "Số dư ròng (giải thích)" cũng tính ở client nhưng chỉ là phép trừ để hiển thị.

## Test

- Các test chạy qua hành vi thật bằng msw và kiểm tra header và body, không phải test giả:
  - key giống nhau qua các lần retry;
  - không gửi Idempotency-Key cho checklist hay reorder;
  - If-Match theo version của bảng kê;
  - phân bổ theo hạn cũ nhất và re-flow các dòng chưa sửa;
  - cả hai callout đều chặn nút ghi.
- Ca quan trọng còn thiếu:
  1. Mã lỗi chưa map, ví dụ `payment_reversed` khi phân bổ, phải hiện câu tiếng Việt (H2).
  2. Export:
     - rời trang và quay lại (H1);
     - poll gặp 401 thì dừng (M7);
     - `download()` gặp 410 hoặc trạng thái `expired`. Đã có test ở dòng 133 cho trường hợp đã hết hạn lúc liệt kê, nhưng chưa có ca chuyển `succeeded` sang hết hạn ngay giữa lúc bấm.
  3. 412 trên lệnh bảng kê hoặc campaign: kiểm tra dialog vẫn giữ nguyên và không hiện toast "Chưa ghi sổ.".
  4. PayDialog mở từ header với khách chưa có số dư (M5).
  5. `validation_failed` có `fields` thì ô tương ứng được đánh dấu (M6).
  6. Retry tạo bảng kê sau lỗi mạng. Hiện chưa test được, vì thiếu key phía API.
- Phase file có nêu test `period-bar`, nhưng tôi không thấy `shared/ui/period-bar.test.*`. Hành vi chuyển kỳ hiện chỉ được kiểm gián tiếp qua `dashboard-page.test.tsx:170` và `stock-movement-page.test.tsx:75`.

## Hành động đề xuất (theo thứ tự ưu tiên)

1. H2: map các mã lỗi còn thiếu và đổi fallback của `errorMessage` thành câu tiếng Việt.
2. H1: giữ export job khi rời trang và làm target cho `export_ready`.
3. M7: dừng poll khi gặp 401, 404 hoặc 410.
4. M5: dùng Combobox danh bạ cho PayDialog.
5. M8: sửa câu thông báo khi không tìm thấy PT/PC, và ghi API gap tra phiếu theo số.
6. M6: nối `setFieldError` cho các dialog debt.
7. M1–M4: ghi các API gap (danh sách khách kèm last purchase và consents, endpoint advances, tóm tắt bảng kê, lọc doanh thu theo id). Ở client trước mắt: thu hẹp invalidate, đặt staleTime và trần số trang.
8. Các mục Low khi có dịp chạm lại file.

## Số liệu

- Typecheck và lint: không chạy lại vì lead đang chạy gate.
- Test: 25 file, 106 test qua trong phạm vi 6 thư mục feature.
- Không thấy `any`, `eslint-disable` hay số học float trong phạm vi review.

## Việc của plan

Phần A đã đủ màn hình theo phase 18. Hai mục tiêu nghiệm thu chưa đạt:

- "export job survives navigation" (H1);
- deep link "Mở" cho mọi loại thông báo (H1 và L5).

Thiếu test `period-bar` riêng như phase file nêu.

## Câu hỏi còn mở

- Backend có nhận thêm các API gap không? Gồm: Idempotency-Key cho 4 endpoint, endpoint advances, tóm tắt bảng kê, `GET /payments?doc_no=`, và danh sách khách kèm last purchase và consents. Nếu không, M1–M4 và M8 chỉ giảm nhẹ được ở client.
- Fallback cho mã lỗi lạ nên là câu chung có kèm mã lỗi, hay chỉ câu chung? Đây là quyết định UX/hỗ trợ.

Status: DONE_WITH_CONCERNS
Summary: Không có lỗi Critical. Tiền, idempotency và If-Match đúng hợp đồng. Có 2 lỗi High (file xuất mất khi rời trang và không có đường tải lại; mã lỗi chưa map hiện tiếng Anh) và 8 lỗi Medium, chủ yếu là N+1 và luồng chọn đối tác khi thu tiền.
Concerns/Blockers: M1–M4 và M8 cần backend bổ sung API để sửa triệt để.
