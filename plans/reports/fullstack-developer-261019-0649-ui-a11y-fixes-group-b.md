# Sửa UI/UX và a11y nhóm B (phase 20)

Ngày: 2026-10-19. Nguồn phát hiện: `plans/reports/ui-ux-designer-261019-1839-ui-review-group-b.md`.
Quyết định chủ sản phẩm: mục "Owner decisions (2026-10-19)" trong `phase-20-ui-ux-review-accessibility.md`.

## Tóm tắt

Đã sửa toàn bộ phát hiện mức Cao và Trung bình, và bốn quyết định của chủ sản phẩm:

- "Khóa sổ" có hộp xác nhận nêu ngày và ảnh hưởng, không có ô lý do.
- "Gửi lời mời chuyển quyền" có hộp xác nhận nêu email, thời hạn 72 giờ và hệ quả.
- "Ngừng" NCC có hộp xác nhận. Lý do không ngừng được đọc qua bàn phím, không còn `disabled`/`title`.
- Hộp mã khôi phục MFA không đóng được (Esc, nền, ×, "Đóng") khi chưa tick "Tôi đã lưu mã". Có thêm "Sao chép" và "Tải .txt".

Phần lớn phát hiện mức Thấp cũng đã sửa. Những mục giữ nguyên hoặc chưa sửa đều có lý do trong bảng dưới.

Mỗi hàng gap matrix thuộc nhóm B đều có test unit khẳng định.

Gate trong phạm vi nhóm B đều sạch. Còn lỗi ở file ngoài phạm vi, xem mục Gate.

## 1. Bảng phát hiện

| Mã | Mức | Kết quả | Cách sửa / lý do |
|---|---|---|---|
| D-1 | Thấp | Đã sửa | Nút hàng bảng kê có tên kèm số bảng kê: "Gửi khách/Khách xác nhận/Tranh chấp {DS}" |
| D-2 | Thấp | Đã sửa | "Giảm/xóa nợ {số chứng từ}" |
| D-3 | Thấp | Đã sửa | Tiêu đề tạm "Công nợ · Đang tải…" |
| D-4 | Thấp | Đã sửa | "Phân bổ" cỡ sm, tên "Phân bổ {số phiếu}" |
| D-5 | Trung bình (lead xếp) | Đã sửa | Lỗi kiểm tra của pay, write-off, allocate-advance, new-statement, dispute nằm tại ô: `aria-invalid`, mô tả lỗi, focus vào ô sai đầu tiên (`useFocusFirstInvalid`) |
| D-6 | Thấp | Đã sửa | Xóa nợ có ImpactSummary "Còn nợ sau khi giảm" |
| D-7 | Thấp | Đã sửa | "(mở tab mới)" dạng sr-only trên link "In bảng kê" |
| D-8 | Thấp | Giữ nguyên | Đổi bộ cột lịch thu/trả là quyết định của chủ sản phẩm, chưa chốt |
| D-9 | Thấp | Giữ nguyên | Đổi "In phiếu" thành "In bảng kê" là quyết định nhãn, chưa chốt |
| C-1 | Thấp | Đã sửa | "Bắt đầu mùa {tên}", "Kết mùa {tên}" |
| C-2 | Thấp | Đã sửa một phần | Ô đang lưu giữ focus, có `aria-busy` và không bị disabled. Các ô khác vẫn chờ, vì mỗi câu trả lời mang cả checklist và gửi song song sẽ ghi đè nhau |
| C-3 | Thấp | Đã sửa | "Sửa/Tái đặt đơn/Tạo đơn {tên khách}" |
| C-4 | Thấp | Đã sửa | Chỉ hàng đang chạy có pending "Đang tạo đơn…". Các hàng khác bị khóa nhưng không báo bận |
| C-5 | Thấp | Đã sửa | Focus vào ô sai đầu tiên trong customer-dialog và customer-quick-dialog |
| R-1 | Thấp | Đã sửa | Lỗi job là một `role="alert"` riêng, nằm ngoài vùng status, không lồng live region |
| R-2 | Trung bình (lead xếp) | Đã sửa | Cột thao tác bộ lọc đã lưu có `acts` |
| K-1 | Trung bình | Đã sửa | Quy cách nhóm đã dùng là ô `readOnly` (không disabled), vẫn nhận focus, có mô tả lý do khóa |
| K-2 / O-4 | Trung bình | Đã sửa | Nhãn theo dòng: "Mặt hàng vị n", "Tên vị ngắn {mã}", "Gỡ vị {tên}". Onboarding: "{Hàng/Màu/Số lượng/Giá vốn/Số lô/Hạn dùng} {mã}", "Xóa dòng n · {mã}" |
| K-3 / K-16 | Trung bình | Đã sửa theo quyết định | "Ngừng" mở xác nhận "Ngừng giao dịch với {NCC}?". Khi còn đơn mở, nút không disabled, có mô tả lý do, và mở hộp "Chưa ngừng được giao dịch với {NCC}" có `role="alert"`. Không gửi gì |
| K-4 | Trung bình | Đã sửa | Bảng giá, nhóm khách và bậc thang giá có nhánh lỗi `role="alert"` kèm "Thử lại" |
| K-5 | Trung bình | Đã sửa | Lịch sử giá có nhánh lỗi riêng, không hiện "Chưa có thay đổi giá." |
| K-6 / O-1 | Trung bình (lead xếp) | Đã sửa | Caption "Các vị trong nhóm", "Tồn kho đầu kỳ", "Số dư quỹ đầu kỳ". Cột cuối có chữ sr-only "Thao tác" |
| K-7 / O-2 | Trung bình (lead xếp) | Đã sửa | `acts` ở bảng nhóm mix, bảng vị, NCC, tồn đầu kỳ, quỹ đầu kỳ |
| K-8 | Thấp | Đã sửa | Callout lỗi trong MixGroupLive có `role="alert"` |
| K-9 | Thấp | Đã sửa | Header sr-only "Trạng thái" |
| K-10 | Thấp | Đã sửa | MoneyInput, QtyInput, Combobox và Input của form hàng nhận `invalid` |
| K-11 | Thấp | Đã sửa | "Sửa/Mở lại/Tạo đơn nhập/Ngừng/Xóa {NCC}" |
| K-12 | Thấp | Đã sửa | Ô điện thoại ở form nhanh có `inputMode="tel"` |
| K-13 | Thấp | Đã sửa | Checkbox bị khóa có hint lý do, nối bằng `aria-describedby` |
| K-14 | Thấp | Đã sửa | Chỉ nhóm đang được "Dùng lại" có pending. Các hàng khác bị khóa nhưng không báo bận |
| K-15 | Thấp | Đã đóng nhờ shared | `m-live` của CommitDialog nay có `aria-live="polite"`, nên cảnh báo giá được đọc |
| K-17 | Thấp | Giữ nguyên | Ký hiệu "*" bắt buộc là quyết định nhãn, chưa chốt |
| S-1 | Trung bình | Đã sửa | "Phạm vi xem" là `role="group"` có `aria-labelledby`, không còn `label` lồng `label`. Lỗi phạm vi là mô tả của nhóm |
| S-2 | Trung bình | Đã sửa theo quyết định | Hộp mã khôi phục bị khóa đóng tới khi tick "Tôi đã lưu mã". Đóng sớm thì hiện cảnh báo `role="alert"` và focus về checkbox. Có "Sao chép" và "Tải .txt" |
| S-3 | Trung bình | Đã sửa theo quyết định | Hộp "Xác nhận khóa sổ" có ImpactSummary: đang khóa đến, khóa sổ đến, mở lại sau này. Không có ô lý do. Thiếu ngày thì báo tại ô |
| S-4 | Trung bình | Đã sửa theo quyết định | Hộp "Xác nhận chuyển quyền chủ cửa hàng" nêu email, 72 giờ và hệ quả (người nhận thành chủ; tài khoản hiện tại bị vô hiệu, đăng xuất mọi thiết bị, khớp API spec) |
| S-5 | Thấp | Đã sửa | "Đang tạo bản xuất…" có `role="status"`. Trạng thái job là status "Bản xuất dữ liệu". Lỗi là Callout `role="alert"` riêng |
| S-6 | Thấp | Đã sửa | `useReadOnlyHint`: hint hiện trên màn hình và nối với nút qua `aria-describedby` ở khóa sổ, hồ sơ cửa hàng và chính sách ngoại tuyến. "Cấp quyền hỗ trợ" nối với hint "cần ticket" |
| S-7 | Thấp | Đã sửa | Link otpauth nằm ngoài Field |
| S-8 | Thấp | Đã sửa | Nút "Sao chép khóa thiết lập" |
| A-1 | Thấp | Đã sửa | Focus vào `h1` sau đăng ký, và vào vùng xác nhận của form gửi email |
| A-2 | Thấp | Đã sửa | Bỏ `.f` lồng ở slug-field |
| A-3 | Thấp | Chưa sửa | Cải tiến tùy chọn, ngoài các mức lead yêu cầu |
| O-3 | Trung bình (lead xếp) | Đã sửa | Lỗi dòng tồn và quỹ đầu kỳ nằm tại ô (`aria-invalid` và mô tả), focus vào ô sai đầu tiên |
| O-5 | Thấp | Chưa sửa | Cải tiến focus khi thêm/xóa dòng. Ngoài các mức lead yêu cầu |
| O-6 | Thấp | Chưa sửa | Cải tiến focus vào region của bước. Ngoài các mức lead yêu cầu |
| O-7 | Thấp | Đã sửa | Callout "Các bước bắt buộc đã xong" có `role="status"` |
| O-8 | Thấp | Đã sửa | Select lý do có `aria-invalid` |
| O-9 | Thấp | Chỉ ghi nhận | Theo chỉ đạo. Điền sẵn số điện thoại cần API trả phone của chủ, ngoài phạm vi web |
| M-1 | Cao | Đã sửa | Vòng focus trên header admin dùng `--on-dark-focus` |
| M-2 | Thấp | Đã sửa | Viền nút Đăng xuất đủ tương phản, dùng `btn-sm` (44 px trên màn cảm ứng) |
| M-3 | Thấp | Đã sửa | Skip link "Bỏ qua tới nội dung" trỏ vào main |
| M-4 | Thấp | Đã sửa | Email quản trị có bản sr-only |
| M-5 | Thấp | Đã sửa | "(mở tab mới)" sr-only trên link cửa hàng |
| M-6 | Thấp | Đã sửa | Chỉ cờ hoặc nút phân công đang chạy có pending ("Đang bật…", "Đang tắt…", "Đang ghi sổ…"). Các nút cùng loại bị khóa nhưng không báo bận |

### Soát bốn file settings chưa soát

- `current-subscription-card.tsx`: Không có lỗi a11y. KeyValueGrid có nhãn, pill trạng thái có chữ, khi không có thuê bao thì có câu "Cửa hàng chưa có thuê bao.".
- `subscription-page.tsx`: Khi đọc thuê bao lỗi, thẻ "Thuê bao hiện tại" từng để trống. Nay thẻ ghi "Không tải được thuê bao." và LoadError `role="alert"` ở trên vẫn giữ.
- `account-closure-page.tsx`: Khi chủ đóng hộp mật khẩu mà API yêu cầu, trang từng hiện việc hủy như một lỗi `role="alert"`. Nay đóng hộp không báo gì, vì đó là lựa chọn của chủ.
- `account-page.tsx`: Không có lỗi riêng. Các thẻ con đã được xử lý ở S-3, S-4, S-5 và S-6.

## 2. Gap → test

| Gap | Test khẳng định |
|---|---|
| 2 (ngữ cảnh dòng) | `debt/containers/pay-dialog.test.tsx` và `allocate-advance-dialog.test.tsx`, khóa đúng tên đầy đủ "Phân bổ SO-0001"/"Phân bổ SO-0002"/"Phân bổ PT-0001". `debt/pages/statements-page.test.tsx` ("Gửi khách DS-0001"). `debt/pages/receivables-page.test.tsx` ("Giảm/xóa nợ {SO}"). `debt/pages/advances-page.test.tsx`. `catalog/components/mix-group-form.test.tsx` "names the flavor table and each line's inputs and buttons after the flavor". `onboarding/steps/opening-balances.test.tsx` "names its table, its columns and the inputs and buttons of each line". `partners/pages/suppliers-page.test.tsx` "names each row's buttons after the supplier". `catalog/pages/mix-groups-page.test.tsx` "names each row's buttons…" |
| 6 (lỗi không tự tắt) | `pricing/pages/price-lists-page.test.tsx` và `pricing/components/price-history-table.test.tsx`: 500 cho `role="alert"` kèm "Thử lại", không hiện chữ rỗng. Lỗi tại ô (debt dialogs, opening steps): `aria-invalid` và focus trong các test debt và onboarding ở trên. `settings/pages/subscription-page.test.tsx` "says the subscription could not be read instead of showing an empty card" |
| 9 (xác nhận nhóm mix) | `catalog/pages/mix-groups-page.test.tsx` "asks before stopping a group and sends nothing until it is confirmed". Cùng mẫu cho NCC: `suppliers-page.test.tsx` "asks before stopping a supplier…" |
| 11 (readOnly kèm lý do) | `catalog/components/mix-group-form.test.tsx` "keeps the fixed spec of a used group read-only, reachable by keyboard and saying why" và "lets the spec of a group no document uses be picked again". `suppliers-page.test.tsx` "reads out why a supplier with open orders cannot be stopped yet, and sends nothing". `account-page.test.tsx` "holds the lock while the store is read-only and says why next to the button" |
| 12 (caption) | `mix-group-form.test.tsx` (bảng "Các vị trong nhóm"). `opening-balances.test.tsx` (bảng "Tồn kho đầu kỳ", "Số dư quỹ đầu kỳ") |
| 13 (DatePicker dd/mm/yyyy) | `settings/pages/account-page.test.tsx` "locks the books through a date": gõ "10/10/2026", gửi `2026-10-10`. "asks for the lock date on the field before confirming anything" |
| 14 (xuất CSV) | `reports/use-export-job.test.tsx` "shows why an export failed" (alert riêng, không lồng). `account-page.test.tsx` "announces the export being written as a status and its failure as an alert" |
| 17 (toast sau 2xx) | `suppliers-page.test.tsx` "asks before stopping a supplier and confirms it only once the API stored it". `mix-groups-page.test.tsx` "shows the wait only on the group being reused and holds the other rows until it is stored". `admin/pages/feature-flags-page.test.tsx` "shows the wait on the flag being switched on and holds the other rows without claiming it". `admin/pages/tenant-page.test.tsx` "turns a flag on…". `admin/pages/support-ticket-page.test.tsx`. `crm/pages/customers-page.test.tsx` "shows the running re-order on its own row…". `campaign/pages/seasons-page.test.tsx` "keeps the item being saved focused and busy…" |
| 18 (vùng chạm `acts`) | `mix-groups-page.test.tsx`, `mix-group-form.test.tsx`, `suppliers-page.test.tsx`, `opening-balances.test.tsx`, `reports/pages/sales-page.test.tsx` "applies, renames and deletes a saved layout" (ô thao tác bộ lọc đã lưu) |

Các test khác theo §7 của báo cáo soát:

- `subscription-page.test.tsx` "groups the scope checkboxes under their own label and names an empty scope on the group": `role="group"` "Phạm vi xem" và không có `label label`.
- `account-page.test.tsx` "keeps the recovery codes open until the owner says they are saved, and offers copying and a file": Esc, ×, "Đóng" và nền đều bị chặn; "Sao chép" ghi đúng nội dung clipboard; "Tải .txt" có `download` và `href`.
- `account-page.test.tsx`: các test chuyển quyền chủ cửa hàng nay đi qua hộp xác nhận và khẳng định chưa gửi gì trước khi xác nhận.
- `account-page.test.tsx`: test mới "says the invitation is being sent…". Test giữ phản hồi gửi lời mời và khẳng định nút xác nhận hiện "Đang gửi…" với `aria-busy` và `aria-disabled`, focus vẫn ở nút, bấm lại không gửi thêm. Đã kiểm: bỏ `pendingLabel` thì test đỏ.
- Test này từng đỏ một lần trong toàn suite (uifix-a báo, không có thông báo lỗi). Đã điều tra trên bản gốc của test, tức là chưa thêm bước chờ focus:
  - Chạy riêng với `--repeats 20`: 21/21 xanh.
  - Toàn suite với `--sequence.shuffle`: 5 lần, đều 1090/1090.
  - Chạy 3 toàn suite song song, 3 vòng, tổng 9 lượt: test này xanh cả 9 lượt. Trong lúc đó có 1 đến 36 test khác đỏ ở mỗi lượt, rải khắp app (kits, sales, reports, onboarding, debt, guards…). Tất cả đều là `findBy*` hết hạn chờ 5 giây (`asyncUtilTimeout`), không có lần nào là `toHaveFocus`.
  - Kết luận: không tái hiện được lần đỏ đó. Lần đỏ ấy nhiều khả năng do máy bị nhiều agent cùng chạy suite, làm một `findBy*` quá 5 giây. Đây là rủi ro chung của suite chứ không riêng test này.
  - Test vẫn giữ một bước phòng hờ: chờ focus mở đầu nằm trong hộp thoại rồi mới bấm. Test cũng đã chờ trạng thái "Đang gửi…" trước khi kiểm focus. Nội dung assert không đổi, `toHaveFocus` không bọc `waitFor`.
- `account-closure-page.test.tsx` "reports nothing when the owner closes the password prompt the API asked for".
- `auth/pages/signup-page.test.tsx`: `h1` nhận focus. `email-request-pages.test.tsx`: vùng xác nhận nhận focus.
- `admin/console-shell.test.tsx`: `aria-current="page"`, vòng focus `var(--on-dark-focus)`, skip link, email sr-only.

Một số test auth cũ tìm nhãn chứa chữ lỗi. Field shared nay đưa lỗi ra ngoài label, nên các test này được đổi sang khẳng định `toHaveAccessibleDescription` kèm `aria-invalid`. Khẳng định mới tương đương hoặc chặt hơn, không có assert nào bị nới.

## 3. Nhãn mới

- Debt:
  - nút và trường: "Gửi khách {DS}", "Khách xác nhận {DS}", "Tranh chấp {DS}", "Giảm/xóa nợ {SO}", "Phân bổ {số phiếu}";
  - tiêu đề và ImpactSummary: "Công nợ · Đang tải…", "Chứng từ còn nợ", "Còn nợ sau khi giảm";
  - sr-only: "(mở tab mới)", "Thao tác", "Trạng thái".
- Campaign: "Bắt đầu mùa {tên}", "Kết mùa {tên}".
- CRM: "Sửa {tên}", "Tái đặt đơn {tên}", "Tạo đơn {tên}", pending "Đang tạo đơn…".
- Catalog:
  - nhãn dòng vị: "Mặt hàng vị n", "Tên vị ngắn {mã|n}", "Gỡ vị {tên|n}";
  - caption "Các vị trong nhóm";
  - lý do khóa "Nhóm đã dùng trên chứng từ nên không đổi được đơn vị và quy cách thùng.";
  - nút hàng: "Sửa/Ngừng dùng/Dùng lại/Xóa {nhóm}";
  - sr-only "Trạng thái".
- Partners:
  - nút hàng: "Sửa/Mở lại/Tạo đơn nhập/Ngừng/Xóa {NCC}";
  - hộp thoại: "Ngừng giao dịch với {NCC}?", "Chưa ngừng được giao dịch với {NCC}";
  - nút xác nhận "Ngừng giao dịch".
- Pricing: "Không tải được {bảng giá|nhóm khách|bậc thang giá|lịch sử giá}: …" kèm nút "Thử lại".
- Settings:
  - khóa sổ: "Xác nhận khóa sổ", "Chọn ngày khóa sổ.", và các nhãn ImpactSummary "Đang khóa đến", "Khóa sổ đến", "Mở lại sau này";
  - chuyển quyền: "Xác nhận chuyển quyền chủ cửa hàng", "Người nhận", "Thời hạn", "Sau khi chấp nhận", nhãn chờ "Đang gửi…";
  - MFA:
    - "Tôi đã lưu mã", "Sao chép", "Tải .txt" (tệp `tally-ma-khoi-phuc.txt`);
    - "Đã sao chép mã khôi phục.";
    - "Đánh dấu “Tôi đã lưu mã” trước khi đóng. Sau khi đóng không xem lại được các mã này.";
    - nút "Đóng";
    - "Sao chép khóa thiết lập", "Đã sao chép khóa thiết lập.";
  - "Không tải được thuê bao.";
  - status "Bản xuất dữ liệu".
- Onboarding:
  - caption "Tồn kho đầu kỳ", "Số dư quỹ đầu kỳ";
  - nhãn dòng: "{Hàng|Màu|Số lượng|Giá vốn|Số lô|Hạn dùng} {mã}", "Xóa dòng n · {mã}".
- Admin: "Đang bật…", "Đang tắt…", sr-only "(mở tab mới)" trên link cửa hàng, skip link "Bỏ qua tới nội dung".

## 4. Cập nhật e2e (lead giao; không chạy e2e theo ràng buộc)

- `e2e/procurement.spec.ts`:
  - Khi còn đơn mở, nút `Ngừng {NCC}` vẫn bấm được, có mô tả "Còn đơn nhập đang mở". Bấm vào mở hộp "Chưa ngừng được giao dịch với {NCC}": có alert, không có nút "Ngừng giao dịch", đóng bằng "Đóng".
  - Sau khi đóng phần còn thiếu, nút không còn mô tả. Bấm vào mở hộp "Ngừng giao dịch với {NCC}?", bấm "Ngừng giao dịch", chờ toast "Đã ngừng giao dịch…" và hộp đóng. Bước này nay thực sự ngừng NCC. Mỗi lần chạy dùng tên NCC mới nên không đụng lần sau.
- `e2e/settings.spec.ts`: "Khóa sổ" trên thẻ mở hộp "Xác nhận khóa sổ". Hộp ghi "Hết ngày {dd/mm/yyyy}". Bấm "Khóa sổ" trong hộp, rồi chờ toast và hộp đóng.
- `e2e/catalog.spec.ts`:
  - Đã đổi bộ chọn ô mặt hàng từ "Chọn mặt hàng cho vị" (nhãn cũ, nay không còn) sang `Mặt hàng vị {n}`.
  - Ô tên vị ngắn nay chọn theo `Tên vị ngắn {mã}` thay cho `.nth()`.
- Đã rà các spec còn lại (trừ `shell.spec.ts`, `a11y/`, `visual/`): debt, campaigns, admin, onboarding, reports, overview, pos, quotes, invoices, returns, inventory, gift-baskets, auth. Không cần sửa thêm. Các bộ chọn ở đó hoặc không dùng nhãn tôi đổi, hoặc dùng khớp chuỗi con của Playwright mà vẫn đúng với nhãn mới:
  - debt: `/^Phân bổ /` và "Gửi khách" trong drawer;
  - catalog: "Sửa", "Ngừng dùng", "Dùng lại", "Xóa" trong hàng;
  - admin: `^https?://{slug}\.` cho link cửa hàng;
  - reports: status "Tệp xuất CSV".

### Theo nhãn mới của nhóm A

Lead giao thêm việc này sau khi uifix-a đổi nhãn. Các bộ chọn đã được đối chiếu với `src` hiện tại.

Chỗ phải sửa vì `exact: true` sẽ hỏng:

- `e2e/invoices.spec.ts` và `e2e/quotes.spec.ts`:
  - ô số lượng đổi từ "Số lượng" (exact) sang `Số lượng – {tile.name}`; theo `rowLabel`, tên dòng là `display_name` của SKU, chính là nhãn `/search` mà `pos/bootstrap` cũng trả ở `name`;
  - combobox sản phẩm đổi sang `Tên sản phẩm – dòng 1` (exact).
- `e2e/returns.spec.ts`: dòng hàng lấy đổi chọn theo `Số lượng – {tileName} {màu B}` (exact). Tên dòng gồm `lineName` của dòng trả cộng nhãn màu đã chọn.

Chỗ vẫn chạy với khớp chuỗi con, nhưng đã đổi sang tên đầy đủ (exact) để không lẫn hàng:

- `e2e/pos.spec.ts`: `Sửa giỏ {số}`, `Sao chép giỏ {số}`, `Xóa giỏ {số}`.
- `e2e/pos-offline.spec.ts`: `Ghi sổ lại {nhãn giao dịch}`.
- `e2e/procurement.spec.ts`: checkbox chọn theo `labelled("Quản lý lô/hạn", productName)`, không dùng `.last()` nữa.
- `e2e/gift-baskets.spec.ts`: chọn theo `Thành phần {n}` và `Số lượng thành phần {n}` (exact). Khớp chuỗi con không phân biệt hoa thường, nên "Thành phần 1" có thể khớp cả "Màu thành phần 1" hay "Thành phần 10".

Đã rà, không sửa:

- `inventory.spec.ts:92`, `Thực đếm – {name}`: khớp tiền tố với `countLineName`. Không đổi sang exact vì tên dòng kiểm kê có thể kèm `variant_label`.
- `inventory.spec.ts:107`: caption `Thẻ kho {mã}` không đổi.
- `inventory.spec.ts:150`: combobox "Hàng" không đổi.
- `pos.spec.ts:143/159/165/209`: đã theo `Số lượng – {tên}`.
- `procurement.spec.ts:113–119/148`: "Tên hàng mới" không đổi, còn lại đã dùng `labelled()`.
- shell, a11y, visual: nhãn "Mở rộng/Thu gọn {nhóm}" đã đúng; các nút mở hộp thoại trong `parity-states.ts` không đổi.
- `e2e/acceptance/`: bỏ qua theo chỉ đạo.

## 5. Gate (apps/web)

| Gate | Kết quả |
|---|---|
| `corepack pnpm test` lần 1 | Đạt: 149/149 file, 1069/1069 test |
| `corepack pnpm test` lần 2 | Đạt: 149/149 file, 1069/1069 test |
| `corepack pnpm typecheck` | Phạm vi nhóm B sạch. Còn 7 lỗi ở 3 file ngoài phạm vi (xem dưới) |
| `corepack pnpm lint` | Phạm vi nhóm B sạch. Còn 2 lỗi ở 1 file ngoài phạm vi (xem dưới) |
| Lần hai, sau khi sửa e2e: `corepack pnpm typecheck` (gồm `e2e/**` qua `tsconfig.node.json`) | Các file e2e đã sửa sạch. Lỗi pos/sales đã hết. Còn 1 lỗi ngoài phạm vi: `src/shared/styles/target-size.test.ts(2,30)` thiếu kiểu cho `node:fs` |
| Lần hai, sau khi sửa e2e: `corepack pnpm lint` | Sạch toàn bộ |
| Lần ba, sau khi sửa e2e theo nhãn nhóm A: `corepack pnpm typecheck` | Sạch toàn bộ, kể cả `e2e/**` |
| Lần ba: `corepack pnpm lint` | Sạch toàn bộ |
| Lần ba: `npx playwright test --list` | Nạp được 42 file, 535 test, không lỗi |
| Lần bốn, sau khi thêm "Đang gửi…": `corepack pnpm vitest run` | Đạt: 155/155 file, 1090/1090 test |
| Lần bốn: `corepack pnpm typecheck` | Sạch toàn bộ |
| Lần bốn: `corepack pnpm lint` | Sạch toàn bộ |
| Lần năm, sau khi điều tra test chập chờn: `corepack pnpm vitest run` ×2 | Đạt cả hai lần: 155/155 file, 1090/1090 test |
| Lần năm: `corepack pnpm typecheck`, `corepack pnpm lint` | Sạch toàn bộ |

Lỗi ngoài phạm vi. Tôi không sửa, đã báo lead:

- Typecheck:
  - `src/features/pos/components/product-tile.test.tsx` (2 lỗi, brand `Money`);
  - `src/features/pos/offline/components/offline-callouts.test.tsx` (2 lỗi, `exactOptionalPropertyTypes`);
  - `src/features/sales/components/line-items/line-items-editor.test.tsx` (3 lỗi, brand `Qty`).
- Lint: `src/app/layouts/sidebar.tsx:11` và `:12`, lỗi `react-refresh/only-export-components`.
- Trong lúc chạy có một lỗi tạm: `src/features/kits/view-model.ts` khai báo `componentLabel` hai lần, do agent khác đang sửa dở. Lỗi này đã hết ở hai lần chạy cuối.

Test "says the subscription could not be read…" chờ tối đa 3 giây. Lý do: query thuê bao thử lại một lần sau khoảng 1 giây (`retryUnlessSignedOut` trong shared), lâu hơn mức chờ mặc định 1 giây.

## Status

Status: DONE
Summary: Đã sửa mọi phát hiện Cao/Trung bình và bốn quyết định chủ sản phẩm của nhóm B, cùng phần lớn mục Thấp. Mỗi gap thuộc nhóm B có test unit khẳng định. Lint và typecheck trong phạm vi nhóm B sạch.
Concerns: Khi nhiều agent chạy suite song song trên cùng máy, `findBy*` ở nhiều file có thể quá 5 giây và đỏ ngẫu nhiên. Lỗi này không riêng nhóm B. Các ghi chú dưới đây để lưu vết.

- Trước đây `useFocusFirstInvalid` export từ `features/debt/index.ts` và được crm, onboarding dùng lại. Còn Button shared khi `pending` thì thành `disabled` nên focus có thể rơi khỏi nút. Lead giao cả hai cho uifix-a.
- Nhãn chờ "Đang gửi…" cho hộp gửi lời mời chuyển quyền: đã xong. uifix-a đã thêm prop `pendingLabel` cho CommitDialog. `owner-transfer-card.tsx` truyền `pendingLabel="Đang gửi…"` và có test unit.
- Nhóm A đã xong `useFocusFirstInvalid` (chuyển lên shared) và Button pending (`aria-disabled`). Không có test nào của nhóm B bị ảnh hưởng.
