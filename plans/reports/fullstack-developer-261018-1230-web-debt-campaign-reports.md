# Báo cáo triển khai phần A phase 18 (web): công nợ, tổng quan, chiến dịch, CRM, báo cáo

## Kết quả

Đã làm xong toàn bộ phần A. Mỗi mục đều có code và unit test. Trong `apps/web`:

- `corepack pnpm typecheck` (`tsc -b`) không lỗi.
- `corepack pnpm lint` (`eslint .`) sạch.
- `corepack pnpm test --maxWorkers=8`: 101 file, 598 test đạt, chạy 2 lần liên tiếp đều đạt.
  - Baseline là 76 file / 492 test. Phần A thêm 25 file / 106 test: debt 27, dashboard và thông báo 11, chiến dịch và CRM 45, báo cáo 23.

Có hai test cũ không thuộc phần A bị chập chờn khi chạy `pnpm test` mặc định trên máy đang tải cao (load average 22–36):

- `features/inventory/pages/stock-page.test.tsx` › "narrows to the color picked in the URL…"
- `features/auth/components/logout-button.test.tsx`

Cả hai đều hỏng do `findBy…` quá thời gian chờ 1000ms. Chạy riêng thì đạt. Bản baseline 76 file khi bỏ các test của phần A cũng từng hỏng `logout-button` (lần chạy sau thì đạt). Tôi không sửa hai test này vì chúng thuộc feature chỉ được đọc.

Bundle chưa chạy vì hook chặn mọi lệnh có chữ "build".

E2E đã viết theo mẫu tự bỏ qua khi thiếu `E2E_OWNER_PASSWORD`, nhưng chưa chạy thật:

- `e2e/debt.spec.ts`
- `e2e/overview.spec.ts`
- `e2e/campaigns.spec.ts`
- `e2e/reports.spec.ts`

Phase 18: đã tick 5 mục Todo của phần A, có ghi Evidence. Mục "Unit + e2e tests" để chưa tick vì e2e chưa chạy và Settings, Onboarding, Admin chưa làm. Vẫn giữ `status: todo`.

Không commit. Không để lại tiến trình nền.

## File đã sửa/tạo

Tất cả nằm dưới `apps/web`:

- `src/features/debt/**`: schemas, `allocation.ts`, api (queries/mutations/adapters), components, containers (PayDialog, phân bổ ứng trước, ghi chú nhắc nợ, ghi giảm nợ, lập bảng kê, tranh chấp), pages (phải thu, phải trả, ứng trước, bảng kê, lịch thu/trả, in bảng kê), `print.css`, `doc-openers.ts`, `testing/fixtures.ts` và test.
- `src/features/dashboard/**` và `src/features/notifications/**`: schemas, api, components, pages và test.
- `src/features/campaign/**` và `src/features/crm/**`: schemas, api, components, containers, pages và test.
- `src/features/reports/**`:
  - `schemas.ts`, `api/*`
  - `use-report-filter.ts`, `use-export-job.ts`
  - components (report-table, report-summary, report-result, export-panel)
  - containers (report-filter, saved-reports-menu)
  - 6 trang và test
- `src/app/router.tsx`: thêm route in bảng kê.
- `src/app/layouts/app-shell.tsx`: gọi `useDebtDocOpeners()`.
- `e2e/{debt,overview,campaigns,reports}.spec.ts` (mới).

Không sửa `apps/api`, `schema.d.ts`, `src/test/msw/handlers.ts`, và không sửa các feature pos/sales/procurement/inventory/kits/settings.

## Theo từng mục

### Công nợ

- **Ghi sổ qua `useCommit`:**
  - Thu/chi (`POST /payments`) và phân bổ (`POST /allocations`) mỗi intent dùng một Idempotency-Key.
  - Ghi giảm nợ (`POST /debt-adjustments`) đi theo chuỗi: nhập lại mật khẩu → xác nhận ngoại lệ `debt_write_off` → gửi lại. Cả 3 lần gửi dùng chung một key. Test có kiểm tra điều này.
  - Bảng kê gửi If-Match, không gửi key, đúng theo OpenAPI.
- **Phân bổ tiền bằng Decimal**, theo thứ tự hạn cũ nhất trước. Khi sửa một dòng, các dòng chưa sửa tự chia lại. Phần chưa phân bổ thành tiền ứng trước. Các trường hợp vượt số tiền hoặc vượt số còn nợ đều bị chặn.
- **Truy vấn tuổi nợ và lịch thu/trả** chỉ chạy sau khi đã có ngày nghiệp vụ của tenant. Trước đây nó gọi bằng ngày của thiết bị; đây là lỗi thật và đã sửa.
- **Mở chứng từ theo số:** PT/PC mở màn ứng trước, DS mở bảng kê, DN mở drawer giải thích.

### Tổng quan

- **Dashboard** gọi `GET /reports/dashboard`; kỳ và ngày nằm trên URL. Mọi con số lấy từ API, client chỉ định dạng.
- **Thông báo:**
  - Có chip theo loại kèm số đếm, trong đó có "Lỗi đồng bộ POS".
  - Các thao tác read/resolve/snooze làm mới danh sách và `platformKeys.unreadCount()` (badge "Việc cần xử lý" trên util bar vốn đã có).
  - Khi trống hiện "Không có việc cần xử lý.".

### Chiến dịch và CRM

- **Chiến dịch:**
  - Thẻ chiến dịch; `:activate`/`:close` gửi If-Match.
  - Checklist sửa bằng `PATCH`.
  - Bảng "Nhu cầu hàng mùa…" phân trang bằng cursor.
- **CRM:**
  - Danh sách tìm theo "Tìm tên, điện thoại, nhóm…".
  - Hộp thoại "Thêm khách hàng"/"Sửa khách hàng": thêm bằng `POST`, sửa bằng `PATCH` kèm If-Match. Thay đổi đồng ý nhận tin gửi qua `POST /customers/{id}/consents`.
  - Nút "Tái đặt đơn" gọi `POST /customers/{id}/reorder-drafts`.

### Báo cáo

- Phân trang bằng cursor và giữ dòng tổng của trang đầu. Không tải toàn bộ dữ liệu vào trình duyệt.
- Bộ lọc Từ/Đến nằm trên URL, mặc định từ 01/10 tới ngày nghiệp vụ. Lỗi 422 `range_too_long` có câu báo tiếng Việt.
- **Bộ lọc đã lưu:** tạo, áp dụng, đổi tên, xóa; lỗi 409 trùng tên có câu báo riêng.
- **Xuất CSV qua export job:**
  - Dò trạng thái mỗi 2s, tối đa 90 lần mỗi đợt; dừng khi job xong hoặc khi rời màn hình.
  - Có nút "Hủy" và "Kiểm tra lại".
  - Lỗi 409 hàng đợi đầy và trường hợp tệp hết hạn (410) có câu báo riêng.
  - Trước khi tải, màn hình đọc lại trạng thái job.

## Lệch so với spec và chỗ còn thiếu (theo OpenAPI)

1. **Đường dẫn dashboard:** spec ghi `GET /dashboard`, OpenAPI là `GET /reports/dashboard`. Tôi làm theo OpenAPI.
2. **`action_path` của thông báo không phải route web** (ví dụ `/customers/{id}`, `/pos/sync-exceptions`). Vì vậy web tự chọn đích theo loại thông báo.
   - `export_ready` trỏ tới `/reports/exports/{id}`, nhưng web chưa có route này nên không có nút "Mở".
   - Cần chốt: sửa `action_path` phía API hay giữ cách hiện tại.
3. **Dashboard:**
   - Bỏ card "Theo loại" vì API không có số liệu theo loại cho cả kỳ.
   - Bảng top khách/NCC không có cột "Hạn gần nhất"; thay bằng "Quá hạn".
   - "Sắp phải trả" chỉ lấy `due_soon`, không cộng chi phí chưa trả như prototype.
4. **Ứng trước:** API không có danh sách payments, nên màn này đọc ledger của từng đối tác có ứng trước (N+1). PT/PC đã phân bổ hết thì mở ra báo "không tìm thấy".
5. **Bảng kê:** danh sách không có tên khách và số dư, nên mỗi dòng phải đọc thêm phiên bản hiện tại (N+1).
6. **DN, BT:** API không tra được DN theo số, nên DN chỉ mở drawer giải thích. BT chưa có cách mở. `DOC_PREFIXES` thiếu DN/BT nhưng tôi không sửa vì file nằm ngoài phạm vi.
7. **Công nợ:**
   - "Hạn mức còn lại" tính bằng `credit_limit − receivable` vì API không có endpoint dư nợ tín dụng.
   - Client không chặn quyền ghi giảm nợ; server tự kiểm.
   - Lịch thu/trả dự báo theo ngày (`/debts/forecast`), không liệt kê từng chứng từ như prototype.
   - Lời hẹn trả không có số tiền thì tính là 0.
8. **Báo cáo:**
   - Nhãn cột lấy từ server. Chưa có nút chọn cột.
   - Bộ lọc đã lưu chỉ lưu `columns` và `group_by`, không lưu ngày, vì API báo `unknown_filter` nếu gửi ngày.
   - Tuổi nợ phải gọi 2 lần (`receivable` và `payable`).
   - Thiếu số liệu cho: đối chiếu "Đã khớp…" của nxt, đối chiếu hóa đơn của doanh thu, "Sau chi phí hoạt động" của lãi gộp, và "Combo bán tại quầy" của giỏ quà.
   - Chưa dùng `GET /reports`.
9. **CRM:**
   - Cột "Doanh thu từ 01/10" lấy từ `GET /reports/revenue?group_by=customer`, đọc hết các trang (mỗi khách một dòng, mỗi trang 200).
   - Cột "Lần mua gần nhất" đọc timeline của từng khách đang hiển thị (N+1, mỗi khách 1 dòng).
   - Nếu báo cáo bị từ chối thì cột hiện "—".
10. **Contract loyalty mới:** phần A không có màn loyalty, checkout hay hỏi đáp AI, nên không bị ảnh hưởng. `tsc -b` đạt trên `schema.d.ts` mới. schema.d.ts không thiếu type nào cho phần A.

## Việc tiếp theo

- Settings ×3, Onboarding, Admin console và Sandbox reset đang chờ API phase 13.
- Chạy e2e khi có `E2E_OWNER_PASSWORD`. Bài xuất CSV cần worker xuất tệp chạy thật.
- Xem xét tăng timeout `findBy` hoặc giới hạn số worker của vitest cho hai test chập chờn nêu ở trên (thuộc inventory và auth).

## Câu hỏi còn mở

- `action_path` của thông báo nên sửa phía API thành route web, hay web tiếp tục tự chọn đích theo loại?
- Có cần endpoint danh sách payments, endpoint tra DN theo số, và thêm số liệu cho dashboard/báo cáo để bỏ các chỗ đọc N+1 và các ô còn thiếu không?

Status: DONE_WITH_CONCERNS
Summary: Đã làm xong toàn bộ phần A phase 18 (web): công nợ, dashboard, thông báo, chiến dịch, CRM và 6 báo cáo có export job. Typecheck và lint sạch; 598/598 test đạt khi chạy `--maxWorkers=8`.
Concerns/Blockers: Hai test cũ (inventory stock-page, auth logout-button) chập chờn khi chạy song song lúc máy tải cao. E2E chưa chạy vì thiếu `E2E_OWNER_PASSWORD`. Bundle chưa chạy. Còn các thiếu hụt API nêu ở mục "Lệch so với spec và chỗ còn thiếu".

## Review fixes

Lượt này sửa các finding phía client trong `plans/reports/code-reviewer-261018-1330-web-debt-campaign-reports-review.md`. Không sửa `apps/api`, `schema.d.ts` hay `src/test/msw/handlers.ts`; handler mới chỉ thêm trong từng test. Không commit, không chạy lệnh có chữ "build", không để lại tiến trình nền.

Gate trong `apps/web`:

- `corepack pnpm typecheck` (`tsc -b`) không lỗi.
- `corepack pnpm lint` (`eslint .`) sạch.
- `corepack pnpm test`: 106 file, 715 test đạt, chạy 3 lần liên tiếp đều đạt (trước lượt này là 101 file / 598 test).

### Theo từng finding

**H1 — export job sống qua điều hướng.**

- Job giữ ở store cấp app `features/reports/export-job-store.ts` (`useSyncExternalStore`), không gắn với vòng đời trang. Chỉ id của job được lưu vào sessionStorage, theo khóa `báo cáo[:sổ]`. Mọi truy cập storage bọc try/catch; nếu storage từ chối thì vẫn theo dõi trong bộ nhớ.
- `use-export-job.ts` đọc lại job đã lưu khi quay lại trang và poll tiếp. Job mới thay job cũ của cùng báo cáo.
- `use-export-download.ts` (export qua `features/reports/index.ts`): đọc lại trạng thái job ngay trước khi tải rồi mới mở `download_path`. Mã 409 và 410, hay trạng thái `expired`, hiện câu tiếng Việt. Không giữ URL tải nào; URL có chữ ký chỉ nằm trong redirect của server.
- Panel xuất dùng `<button>` thay cho `<a href>`, nên middle-click không bỏ qua được bước đọc lại.
- Thông báo `export_ready` có nút "Tải về" (`entity_id` là id export job, đã đối chiếu `notification/producer_ops.go`). Sau khi tải thì đánh dấu đã đọc.

**H2 — mã lỗi tiếng Việt.**

- `shared/api/api-error.ts`:
  - Thêm cờ `fromServer` và `withMessage(msg)`. `withMessage` giữ đủ thuộc tính, kể cả `retryAfterSeconds`.
  - Thêm câu cho các mã dùng chung: `not_found`, `invalid_cursor`, `idempotency_key_in_progress`, `idempotency_key_reused` (hướng người dùng tải lại để kiểm tra chứng từ đã ghi), `exception_confirmation_used`. Thêm một số lý do theo ô.
  - `errorMessage` đổi ngữ nghĩa: lỗi do client tạo (câu màn hình đã dịch, lỗi mạng) thì trả nguyên message. Lỗi từ server thì tra bảng mã; mã lạ fallback về "Máy chủ từ chối thao tác (mã X).". Không bao giờ hiện message tiếng Anh của server.
- Mã riêng của từng feature nằm ở adapter:
  - debt: thêm nhóm `payment_*`, `party_not_found`, `nothing_owed`, `obligation_*`, `credit_note_exists`, `refund_*`, `reversal_before_document`, `attachment_invalid`;
  - campaign và CRM: các mã còn thiếu;
  - reports: `report_unknown`.
- Hai chỗ tạo `ApiError` ngoài HTTP client được đánh dấu `fromServer` để đi qua bảng mã: `shared/api/http.test.ts` (helper test) và `features/pos/offline/sync-errors.ts` (lỗi đồng bộ POS lấy từ server). Đây là sửa 1 dòng ở pos, chỉ để giữ hành vi cũ dưới ngữ nghĩa mới.
- Test: `shared/api/api-error.test.ts`, `debt/api/adapters.test.ts` (mỗi mã debt ra câu tiếng Việt, mã lạ ra câu chung), bảng mã campaign và CRM.

**M1–M3 — giảm N+1 phía client.**

- `shared/lib/concurrency.ts`: `createLimiter(max)`, có test.
- CRM: lần mua gần nhất và consents chỉ đọc cho các dòng của trang đang hiển thị (50 dòng), 4 request đồng thời, `staleTime` 5 phút. Lưu khách chỉ invalidate danh sách và consents của khách đó.
- Ứng trước: đọc ledger của 20 đối tác đầu, 4 request đồng thời. Nút "Xem thêm (N đối tác)" đọc thêm 20. Deep link `?doc=` đọc hết, nhưng vẫn qua limiter.
- Bảng kê: phiên bản đã lập là bất biến, nên được cache vô hạn với key nằm ngoài `["debt"]` (ghi sổ không đọc lại chúng). Trang danh sách 50 dòng, đọc qua limiter.
- Mỗi hook có comment nói rõ endpoint danh sách sẽ thay cả hook, để sau này dễ thay.

**M4 — doanh thu mùa ở CRM.** Đọc tối đa 10 trang × 200 khách. Khách ngoài phạm vi hiện "—", kèm ghi chú "Doanh thu chỉ đọc 2.000 khách mua nhiều nhất mùa; khách khác hiện “—”.". Query chỉ chạy khi đã có ngày nghiệp vụ của tenant (cũng xử lý L1).

**M5 — "Thu tiền khách" / "Trả tiền NCC" ở header.**

- PayDialog dùng Combobox `kind="party"` tìm phía server (`GET /customers?q=` hoặc nguồn NCC đang hoạt động của `features/partners`), theo mẫu party picker sẵn có. Không còn select chứa toàn bộ đối tác.
- Chọn được mọi khách, kể cả khách chưa có số dư. Ledger không có chứng từ mở thì cả khoản ghi thành tiền ứng trước (`purpose: "advance"`).

**M6 — lỗi theo ô.**

- `debt/field-errors.ts`:
  - `useFieldErrors` map tên field của API sang ô của dialog;
  - `useAllocationErrors` map `allocations[i]` về đúng chứng từ theo thứ tự đã gửi.
- Các mutation debt nhận `onFieldError`. Các dialog được nối như sau:
  - PayDialog: đối tác, số tiền, phương thức, nội dung, từng dòng phân bổ (kèm callout liệt kê dòng bị từ chối);
  - phân bổ ứng trước: từng dòng;
  - ghi giảm nợ: số tiền, lý do;
  - ghi chú nhắc nợ: nội dung, ngày hẹn;
  - lập bảng kê: khách, từ ngày, đến ngày;
  - tranh chấp: nội dung.
- Campaign: checklist dùng `setFieldError`.
- CRM: customer dialog đã có đường riêng đánh dấu ô (có test từ trước).
- Test lỗi theo ô cho pay (số tiền và dòng phân bổ), ghi giảm nợ (lý do), ghi chú nhắc nợ (ngày hẹn), lập bảng kê (`period_to`) và checklist chiến dịch.

**M7 — poll dừng khi hết phiên.** Poll export không còn im lặng. Gặp 401 thì dừng và chuyển cho handler phiên chung; 403, 404 và 410 cũng dừng. Có test.

**M8 — liên kết PT/PC.** PT/PC không còn tiền chưa phân bổ thì mở drawer "Phiếu thu / phiếu chi". Drawer giải thích phiếu đã phân bổ hết hoặc đã hủy, chỉ tới sổ của đối tác, và ghi rõ đây là cách xem tạm cho tới khi tra được phiếu theo số. Thông báo `statement_disputed` mở đúng bảng kê qua `/debts/statements?id=`; id không còn thì drawer báo không tìm thấy.

### Low

- L1: sửa (cùng M4).
- L2: mặc định kỳ của "Lập bảng kê" đi theo `today` cho tới khi người dùng tự chọn; ghi chú nhắc nợ đã làm vậy từ trước.
- L3: bỏ qua. "Hoãn 1 ngày" vẫn tính theo giờ thiết bị vì client không có nguồn giờ server; cần API (xem API gap).
- L4: sửa. Thông báo hiện đủ 6 chip cố định, loại không có thì hiện 0.
- L5: sửa (cùng M8).
- L6: sửa. "Mở" điều hướng trước, rồi đánh dấu đã đọc ở nền; nếu lỗi thì toast "Chưa đánh dấu đã đọc.", không phải "Chưa ghi sổ.".
- L7: sửa (cùng H1).
- L8: sửa bằng `withMessage`.
- L9: sửa. Placeholder đổi thành "Tìm tên, điện thoại, mã số thuế…".
- L10: sửa. "Quá hạn 0 ₫" không tô đỏ.
- L11: sửa. Khi đổi kỳ, các band làm mờ và có `aria-busy` trong lúc tải; nếu lỗi thì không hiện số kỳ trước như số kỳ mới.
- L12: sửa. Dùng `isLoading`, nên tenant lỗi không làm bảng xoay mãi.
- L13: sửa. Mỗi dòng có tên truy cập riêng: "Thu tiền/Trả tiền {tên}", "Bảng kê {tên}", "Phân bổ {số chứng từ}", "Áp dụng/Đổi tên/Xóa {tên bộ lọc}".

### Bỏ qua (có lý do)

- Thông báo không có form field nên không có `setFieldError`. Kích hoạt và kết mùa chiến dịch cũng không có ô nhập.
- Đặt tên bộ lọc báo cáo đã lưu chưa nối lỗi theo ô; không nằm trong danh sách debt, campaign và CRM của lượt này.
- Retry lập bảng kê sau lỗi mạng: chưa test được vì `POST /statements` không nhận Idempotency-Key.

### Test thêm trong lượt này

- `shared/api/api-error.test.ts`, `shared/lib/concurrency.test.ts`, `shared/ui/period-bar.test.tsx` (chip kỳ, nhãn ngày theo kỳ, kỳ trước/sau, "Về hôm nay" chỉ hiện khi khác ngày nghiệp vụ, gõ ngày).
- `debt/api/adapters.test.ts`.
- `debt/containers/pay-dialog.test.tsx`: tìm khách chưa có số dư từ header và ghi thành ứng trước; đánh dấu ô số tiền và dòng phân bổ bị từ chối.
- `debt/containers/write-off-dialog.test.tsx`, `debt-note-dialog.test.tsx`: lỗi theo ô.
- `debt/pages/statements-page.test.tsx`:
  - `?id=` mở bảng kê;
  - `?id=` không còn thì hiện thông báo;
  - 412 khi tranh chấp giữ dialog và không có toast "Chưa ghi sổ.";
  - lập bảng kê bị từ chối `period_to`.
- `debt/pages/advances-page.test.tsx`: chỉ đọc 20 đối tác đầu, không quá 4 request đồng thời, "Xem thêm"; drawer PT/PC đã phân bổ hết.
- `reports/export-job-store.test.ts`, `use-export-job.test.tsx`:
  - quay lại trang thì theo tiếp job;
  - 401 thì dừng;
  - hai sổ của một báo cáo tách job;
  - storage bị từ chối.
- Notifications:
  - "Tải về" sau khi đọc lại trạng thái;
  - mở bảng kê bị tranh chấp;
  - mark-read lỗi vẫn điều hướng;
  - chip có số 0.
- CRM:
  - trần 10 trang doanh thu;
  - 4 request đồng thời;
  - invalidate hẹp sau khi lưu.
- Campaign: bảng mã lỗi, lỗi theo ô của checklist, 412 khi kết mùa và khi bắt đầu mùa.
- Các test và e2e cũ được đổi theo tên truy cập mới; không nới assert nào.

### Còn lại

- Lần mua gần nhất ở CRM có thể trễ tối đa 5 phút (`staleTime`), trừ khách vừa được lưu.
- E2E vẫn chưa chạy (thiếu `E2E_OWNER_PASSWORD`). Bundle chưa chạy.

### API gap cần backend

1. Idempotency-Key cho `POST /statements`, `POST /parties/{id}/collection-notes`, `POST /customers/{id}/reorder-drafts`, `POST /customers`. Web sẽ chuyển sang `useCommit` có key khi có.
2. Danh sách tiền chưa phân bổ: `GET /payments?unallocated=true` hoặc `GET /debts/advances`. Thay toàn bộ `useAdvances`.
3. Tra phiếu: `GET /payments/{id}` và `GET /payments?doc_no=`. Dùng để mở PT/PC thật thay cho drawer tạm.
4. `GET /statements/{id}`, và danh sách bảng kê kèm `party_name` cùng số dư của phiên bản hiện tại. Bỏ được N+1 đọc phiên bản.
5. Danh sách khách kèm `last_purchase_at` và consents. Bỏ được N+1 ở CRM.
6. Lọc doanh thu theo `customer_ids` (`GET /reports/revenue?group_by=customer&customer_ids=`). Bỏ được trần 2.000 khách.
7. Tra DN/BT theo số chứng từ.
8. `action_path` của thông báo là route web, hoặc kèm `doc_no`.
9. Giờ server (header `Date` đáng tin hoặc `tenant.now`), hoặc `snooze_days` cho "Hoãn 1 ngày".
10. Liệt kê export job của người dùng (`GET /export-jobs`), để tìm lại job khi sessionStorage đã mất.
11. Số liệu dashboard còn thiếu: theo loại cho cả kỳ, hạn gần nhất của top khách/NCC, chi phí chưa trả trong "Sắp phải trả".

Status: DONE_WITH_CONCERNS
Summary: Đã sửa H1, H2, M1–M8 và các mục Low rẻ (trừ L3). Typecheck và lint sạch; `pnpm test` 715/715 đạt 3 lần liên tiếp.
Concerns/Blockers: M1–M3 và M8 mới giảm nhẹ ở client và cần các API gap trên. L3 cần giờ server. E2E và bundle chưa chạy.
