# Phase 20: soát UI/UX và trợ năng, nhóm B

Ngày soát: 2026-10-19, 18:39 (Asia/Saigon). Người soát: ui-ux-designer, chỉ đọc code.

## Tóm tắt

Nhóm B nhìn chung đã đi đúng hướng phase 14–18. Bảng dùng `DataTable` đều có caption. Ngày đều dùng `DatePicker`, không còn `type="date"` nào. Xuất báo cáo đã là job bất đồng bộ có test. Lỗi biểu mẫu auth và admin đều dùng `role="alert"`. Ô mật khẩu và mã TOTP không chặn dán và có `autocomplete` đúng.

Các lỗ hổng còn lại gồm ba nhóm:

- **Tương phản token.** Chữ trắng trên nút chính màu cam chỉ đạt 3.26:1. Viền ô nhập gray-300 chỉ đạt 1.74:1. Vòng focus trên header tối của admin chỉ đạt 1.35:1.
- **Gap 11 ở nhóm mix chưa đóng.** Ô bị khóa vẫn dùng `disabled` kèm `title`.
- **Gap 2 ở bảng vị mix chưa đóng.**

Ngoài ra có một loạt lỗi nhỏ, phần lớn là nút trong hàng thiếu ngữ cảnh. Ba thao tác không đảo ngược được (khóa sổ, chuyển quyền chủ, đóng hộp mã khôi phục) cần chủ sản phẩm quyết định có thêm bước xác nhận hay không.

## 1. Phạm vi

Đã đọc code các thư mục sau, trong `apps/web/src`:

- `features/debt`, `features/campaign`, `features/crm`, `features/reports`
- `features/catalog`, `features/partners`, `features/pricing`
- `features/settings`, `features/auth`, `features/onboarding`, `features/admin`
- `app/layouts/admin-shell.tsx`
- Để có ngữ cảnh: `app/layouts/auth-layout.tsx`, `app/layouts/onboarding-layout.tsx`, `shared/ui/reauth-dialog.tsx`, `shared/styles/tokens.css`, `shared/styles/globals.css`

Các file settings sau chỉ lướt, chưa soát từng dòng: `current-subscription-card`, `account-page`, `subscription-page`, `account-closure-page`.

Ngoài phạm vi:

- Nhóm A: Tổng quan, POS, Nhập hàng, Kho, Giỏ quà, offline, in, shell và `shared/ui`.
- Phần việc của agent web-adopt-apis: N+1, Idempotency-Key, schema, query, MSW, drawer thật, đồng ý CRM.

Phần AX và SEO trong skill không áp dụng, vì toàn bộ nhóm B nằm sau đăng nhập.

Phân loại phát hiện:

- **Lỗi**: sửa ngay trong thư mục feature, không đổi nhãn hay hành vi.
- **Đổi nhãn/hành vi**: cần chủ sản phẩm quyết định.
- **Cải tiến**: đưa vào backlog.

## 2. Gap matrix (các hàng giao cho nhóm B)

| # | Gap | Trạng thái nhóm B | Bằng chứng | Test unit |
|---|---|---|---|---|
| 2 | Ô nhập trong bảng thiếu ngữ cảnh hàng | **Đóng một phần** | Đã đóng: `debt/containers/pay-dialog.tsx:118` (`Phân bổ ${source_doc_no}`), `debt/containers/allocate-advance-dialog.tsx:70`, `onboarding/steps/opening-cash-step.tsx:82,85` (theo tên quỹ), `admin/pages/invoices-page.tsx:76,79`, `admin/pages/feature-flags-page.tsx:69,75`, `admin/containers/tenant-flags.tsx:59,63`, `admin/pages/partners-page.tsx:41`, `admin/pages/incidents-page.tsx:56`. Còn mở: `catalog/components/mix-group-form.tsx:101,105,114`. Một phần: `onboarding/steps/opening-stock-step.tsx:147–173` dùng "dòng n", chưa có tên hàng. | Có nhưng yếu: `pay-dialog.test.tsx:34` và `allocate-advance-dialog.test.tsx:31` chỉ khớp tiền tố `/^Phân bổ /`, không khóa số chứng từ. `advances-page.test.tsx:59` cũng vậy. Catalog và partners không có file test nào. |
| 6 | Toast lỗi tự tắt | **Đóng ở tầng chung (nhóm A)** | Toast lỗi giữ nguyên, toast ok tự tắt. Nhóm B gọi `toast.commitError`. Rủi ro còn lại là nhiều lỗi kiểm tra **chỉ** hiện ở toast, không có dòng lỗi tại ô (xem D-5, O-3). | Không có test trong module nhóm B. |
| 7 | Tooltip biểu đồ chỉ hiện khi hover | **Không áp dụng** | `features/reports` không có biểu đồ. Biểu đồ ở Tổng quan thuộc nhóm A. | — |
| 9 | Thao tác nhóm mix là link, không có xác nhận | **Đóng** | `catalog/pages/mix-groups-page.tsx:100–116` dùng `Button size="sm"`. `catalog/components/mix-group-confirm-dialog.tsx:25–48` là hộp xác nhận. "Dùng lại" ở `mix-groups-page.tsx:108` không có xác nhận. Thao tác này đảo ngược được nên chấp nhận được. | Không có (catalog có 0 test). `catalog.spec.ts` là e2e, không chạy. |
| 11 | Ô colorPrice dùng `disabled` | **Pricing đóng; nhóm mix mở** | Pricing: `pricing/components/edit-price-dialog.tsx:78–84` thay các ô giá theo màu bằng một Checkbox, và cả `features/pricing` không còn `disabled` nào. Nhóm mix: `catalog/components/mix-group-form.tsx:38` (`{ disabled: true, title: LOCK_TITLE }`), áp ở dòng 56 và 64. Hai Combobox ở dòng 52 và 60 cũng `disabled={locked}`, `title` nằm trên `span` bọc ngoài (dòng 51, 59). | Không có test nào khẳng định `readOnly`. |
| 12 | Bảng thiếu caption | **Đóng một phần** | Mọi `DataTable` trong nhóm B đều có `caption`. Có 3 bảng `<table>` thô thiếu caption: `catalog/components/mix-group-form.tsx:73`, `onboarding/steps/opening-stock-step.tsx:111`, `onboarding/steps/opening-cash-step.tsx:45`. | Có: `debt/pages/schedule-page.test.tsx:44`, bảng kê trong `statements-page.test`, ba bảng trong reports, bảng "Nhật ký" trong settings, bảng "Tồn kho đầu kỳ theo màu" trong onboarding. |
| 13 | Ô ngày native | **Đóng** | Không còn `type="date"` nào trong nhóm B. Các nơi dùng DatePicker: `reports/components/report-filter.tsx:22–28`, `admin/pages/usage-page.tsx:62,65`, `onboarding/steps/opening-debt-step.tsx:151`, `onboarding/steps/store-profile-step.tsx:147`, `onboarding/steps/opening-stock-step.tsx:168`, `settings/.../period-lock-card.tsx:71`. | Gián tiếp qua test của từng màn. Không có test riêng cho định dạng dd/mm/yyyy trong nhóm B. |
| 14 | CSV chỉ sao chép được | **Đóng** | `reports/components/export-panel.tsx`: job bất đồng bộ, trạng thái, hủy, "Kiểm tra lại", và chỉ tải sau khi đọc lại trạng thái mới. Schema không có tiến độ phần trăm nên UI chỉ hiện trạng thái. | Có: `reports/use-export-job.test.tsx`, 10 ca (dòng 65–218). |
| 17 | Toast thành công hiện trước khi commit | **Đóng (dựa vào `useCommit`)** | Nhóm B đi qua `useCommit`. `pricing/api/save-sale-price.test.tsx` kiểm khóa ghi và lưu một phần. | Không có test "toast sau 2xx, phản hồi trễ" trong nhóm B. Phần này dựa vào `shared/hooks/use-commit.test.tsx` của nhóm A. |
| 18 | Vùng chạm nhỏ | **Đóng một phần** | Thiếu `className: "acts"` nên không được rule 44 px ở `globals.css:312–315`: `reports/components/saved-reports-menu.tsx:72`, `catalog/pages/mix-groups-page.tsx:96`, `onboarding/steps/opening-stock-step.tsx:172`, `onboarding/steps/opening-cash-step.tsx:84`, `catalog/components/mix-group-form.tsx:110`. `.lnk` có padding 0 (`globals.css:65`), ảnh hưởng LinkButton "Gỡ", "Dùng ‹gợi ý›" (`auth/components/slug-field.tsx:76`) và mã ticket. | Không có test. `touch-targets.spec.ts` là e2e. |

## 3. Tương phản token (WCAG 2.2 AA)

Script tính: `/tmp/claude-1000/-home-cesc-Documents-personal-workspace-tally/eaaf4c42-506d-471c-8de3-651ce1852016/scratchpad/contrast.py`. Ngưỡng: 4.5:1 cho chữ thường, 3:1 cho chữ lớn và thành phần giao diện (1.4.11). `tokens.css` phải khớp từng byte với prototype, nên chỉ thêm token mới sau marker "named inline values" và không sửa giá trị cũ.

| Cặp màu | Nơi dùng | Tỉ lệ | Kết quả | Đề xuất |
|---|---|---|---|---|
| Chữ trắng / accent-orange `#f45d29` | `.btn-primary`, `.nav-g .cnt` | 3.26 | **Không đạt** (1.4.3) | Nền nút `#cb4d22` (4.54). Cần chủ sản phẩm duyệt vì đổi màu thương hiệu. |
| Chữ trắng / accent-crimson `#f2295b` | Nút và badge crimson | 4.00 | **Không đạt** với chữ thường; đạt với chữ lớn | `#e22655` (4.52) |
| on-accent `#eaeaea` / gradient cam–crimson | Nút gradient | 2.71 / 3.33 | **Không đạt** | Làm tối cả hai điểm dừng của gradient. Cần duyệt. |
| crimson, chữ 13 px | `.bell b` | 4.00 | **Không đạt** | Dùng bad-ink hoặc tăng lên chữ lớn |
| gray-500 / trắng | `.dp-day` ngoài tháng, `.cbx-caret` | 4.48 | **Không đạt** (sát ngưỡng) | `#767676` (4.54) |
| gray-300 làm màu chữ | `.no` | 1.74 | **Không đạt** | Dùng gray-600 cho chữ, giữ gray-300 cho hatch và chấm |
| Placeholder Safari (mặc định) | Mọi `.in` | 2.35 | **Không đạt** | `::placeholder { color: #767676; opacity: 1 }` |
| Placeholder Firefox (mặc định) | Mọi `.in` | 3.15 | **Không đạt** | Cùng rule trên |
| Placeholder Chrome | Mọi `.in` | 4.61 | Đạt | — |
| Viền gray-300 / trắng | input, select, chip, cp | 1.74 | **Không đạt** (1.4.11) | Token mới `--input-line: #8a8a8a` (3.45 trên trắng, 3.17 trên surface-50) |
| Viền gray-300 / surface-50 | Ô nhập trên nền xám | 1.60 | **Không đạt** | `#8e8e8e` hoặc `--input-line` ở trên |
| Vòng focus navy-700 / navy-700 | Sidebar (nhóm A) | 1.00 | **Không đạt** (2.4.7, 1.4.11) | Token mới `--on-dark-focus`: trắng (11.02) hoặc blue-100 (8.40) |
| Vòng focus navy-700 / navy-900 | Nền navy-900 | 1.59 | **Không đạt** | Vòng trắng (17.57) |
| Vòng focus navy-700 / surface-dark `#23282d` | Header admin, `app/layouts/admin-shell.tsx:25` | 1.35 | **Không đạt** | Vòng trắng (14.86) qua `--on-dark-focus` |
| Viền nút Đăng xuất admin (`on-navy-line`, trắng 12%) / surface-dark | `admin-shell.tsx:33` | 1.46 | **Không đạt** (1.4.11) | `#6d7174` (3.0) |
| Chữ link on-accent / surface-dark | Nav admin | 12.35 | Đạt | — |
| Tagline surface-blue-100 / surface-dark | Header admin | 11.33 | Đạt | — |
| series-1 / series-2 | Hai chuỗi biểu đồ cạnh nhau | 1.98 | **Không đạt** nếu chỉ phân biệt bằng màu | Thêm hatch hoặc nhãn (biểu đồ là nhóm A) |
| gray-600 / trắng và các nền sáng | Chữ phụ | 6.35–7.46 | Đạt | — |
| ok, warn, bad ink / nền tint tương ứng | Callout, pill | 5.80 / 6.27 / 6.84 | Đạt | — |
| bad-ink / trắng | `err-text` | 7.96 | Đạt | — |
| Vòng focus / nền sáng | Mọi màn sáng | 9.39–11.02 | Đạt | — |

Ghi chú:

- Lần soát trước tôi ghi lỗi vòng focus trên `.auth-brand` và `.onb-top`. Đọc lại thì `app/layouts/auth-layout.tsx:9–12` và `app/layouts/onboarding-layout.tsx:8–11` không có phần tử nào nhận focus, nên hiện tại cặp này **không phát sinh lỗi**. Nó chỉ thành lỗi nếu sau này thêm link vào hai khu đó.
- Viền bảng gray-200, warn-line và bad-line là trang trí, không bắt buộc đạt 3:1. Nút disabled (opacity .55) được miễn.

## 4. Phát hiện theo module

Mã phát hiện chỉ dùng trong báo cáo này.

### Công nợ (`features/debt`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| D-1 | Thấp | Lỗi | `pages/statements-page.tsx:76,81,84` | Nút trong hàng bảng kê không có `aria-label` chứa số bảng kê, nên trình đọc màn hình nghe nhiều nút trùng tên. | `aria-label={\`Xem ${doc_no}\`}` và tương tự cho các nút còn lại |
| D-2 | Thấp | Lỗi | `containers/party-drawer.tsx:104` | "Giảm/xóa nợ" không nêu đối tượng. | Gắn tên khách hoặc NCC vào `aria-label` |
| D-3 | Thấp | Lỗi | `containers/party-drawer.tsx:117` | Tiêu đề drawer rỗng khi đang tải, nên dialog không có tên truy cập. | Tiêu đề tạm, ví dụ "Công nợ · Đang tải…" |
| D-4 | Thấp | Lỗi | `pages/advances-page.tsx:55` | "Phân bổ" dùng cỡ md trong hàng bảng, các nút hàng khác dùng sm. | `size="sm"` |
| D-5 | Thấp | Cải tiến | `containers/pay-dialog.tsx:90–101`, `write-off-dialog.tsx:36–46`, `allocate-advance-dialog.tsx:41–52`, `new-statement-dialog.tsx:43–49`, `dispute-dialog.tsx:25` | Lỗi kiểm tra chỉ hiện ở toast, ô sai không có `aria-invalid` và không có dòng lỗi tại chỗ (3.3.1). | Đặt lỗi vào `Field error` và chuyển focus vào ô sai đầu tiên |
| D-6 | Thấp | Cải tiến | `containers/write-off-dialog.tsx` | Xóa nợ không có `ImpactSummary` (số dư trước và sau). | Thêm khối ảnh hưởng như `close-campaign-dialog` |
| D-7 | Thấp | Cải tiến | `containers/statement-drawer.tsx:79` | Link `target=_blank` không báo là mở tab mới. | Thêm "(mở tab mới)" dạng sr-only |
| D-8 | Thấp | Đổi nhãn/hành vi | `pages/schedule-page.tsx:51–58` | Cột lịch thu/trả khác spec §5.6.4. | Chủ sản phẩm chốt bộ cột |
| D-9 | Thấp | Đổi nhãn/hành vi | `pages/print-statement-page.tsx:123` | Nút ghi "In phiếu" trên trang bảng kê. | "In bảng kê" nếu chủ sản phẩm đồng ý |

### Chiến dịch và khách (`features/campaign`, `features/crm`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| C-1 | Thấp | Lỗi | `campaign/components/campaign-card.tsx:38,42` | Nhiều thẻ cùng có "Bắt đầu mùa" và "Kết mùa" mà không nêu tên mùa. | `aria-label={\`Bắt đầu ${name}\`}` |
| C-2 | Thấp | Lỗi | `campaign/components/campaign-card.tsx:127` | Một checkbox đang lưu làm mọi checkbox trong checklist bị `disabled`, và focus có thể rơi mất. | Chỉ khóa ô đang lưu, hoặc dùng `aria-busy` |
| C-3 | Thấp | Lỗi | `crm/pages/customers-page.tsx:63,112,118` | "Sửa", "Tái đặt đơn", "Tạo đơn" không có tên khách trong `aria-label`. | Thêm tên khách |
| C-4 | Thấp | Lỗi | `crm/pages/customers-page.tsx:112` | "Tái đặt đơn" dùng `disabled={pending}`: mọi hàng bị khóa và không hàng nào có `aria-busy`. | Dùng `pending` cho đúng hàng đang chạy |
| C-5 | Thấp | Cải tiến | `crm/containers/customer-dialog.tsx:59–68` | Lỗi đã hiện tại ô, nhưng focus không chuyển vào ô sai đầu tiên. | `setFocus` vào ô sai đầu tiên |

Điểm tốt: `close-campaign-dialog.tsx` có ImpactSummary. Lỗi checklist dùng `role="alert"` (`campaign-card.tsx:124–137`). `demand-table.tsx:64` có caption.

### Báo cáo (`features/reports`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| R-1 | Thấp | Lỗi | `components/export-panel.tsx:44` | Lỗi của job thất bại là `<p>` thường trong vùng status polite (dòng 37), chỉ được đọc lịch sự. Trong khi đó dòng 61 lồng một `role="alert"` vào chính vùng status. | Đưa lỗi job ra khỏi vùng status và dùng một `role="alert"`, tránh lồng live region |
| R-2 | Thấp | Lỗi | `components/saved-reports-menu.tsx:72` | Cột thao tác thiếu `className: "acts"`. | Thêm `className: "acts"` (xem thêm ghi chú nhóm A về `.view`) |

Điểm tốt: `report-result.tsx:59–66` có Callout `role="alert"` kèm "Thử lại". Các nút trong `saved-reports-menu.tsx:79,88,91` có `aria-label`. `NameDialog` hiện lỗi tại ô.

### Danh mục (`features/catalog`, `features/partners`, `features/pricing`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| K-1 | Trung bình | Lỗi | `catalog/components/mix-group-form.tsx:38,51–64` | Gap 11: ô quy cách bị khóa dùng `disabled` và `title`. Ô disabled ra khỏi thứ tự tab, nên người dùng bàn phím và cảm ứng không đọc được lý do khóa. | Input dùng `readOnly` và `aria-describedby` trỏ tới một hint hiển thị nội dung `LOCK_TITLE`. Với Combobox, hiện lý do bằng hint, hoặc thêm `readOnly` nếu Combobox của nhóm A hỗ trợ. |
| K-2 | Trung bình | Lỗi | `catalog/components/mix-group-form.tsx:101,105,114` | Gap 2: "Chọn mặt hàng cho vị", "Tên vị ngắn" và "Gỡ" giống hệt nhau ở mọi hàng. | `` `Mặt hàng vị ${i + 1}` ``, `` `Tên vị ngắn ${x.skuCode ?? i + 1}` ``, `` `Gỡ vị ${x.skuName ?? i + 1}` `` |
| K-3 | Trung bình | Lỗi | `partners/pages/suppliers-page.tsx:102–105` | "Ngừng" bị `disabled` còn lý do nằm trong `title`, nên bàn phím và cảm ứng không đọc được. | `aria-disabled` kèm hint hiển thị, hoặc `aria-describedby` |
| K-4 | Trung bình | Lỗi | `pricing/pages/price-lists-page.tsx:75,79` | Không có nhánh lỗi: khi đọc thất bại, trang hiện DataTable rỗng như thể chưa có dữ liệu. Lỗi của query `groups` và tier cũng bị im lặng. | Thêm `LoadError` hoặc Callout `role="alert"` kèm "Thử lại" như `PagedRows` của admin |
| K-5 | Trung bình | Lỗi | `pricing/components/price-history-table.tsx:75–78` | Khi `changes.error`, bảng hiện "Chưa có thay đổi giá.", tức lỗi bị hiển thị như danh sách rỗng. | Có nhánh lỗi riêng |
| K-6 | Thấp | Lỗi | `catalog/components/mix-group-form.tsx:73` | Bảng vị là `<table>` thô, không có caption (gap 12). | `TableCaption` "Các vị trong nhóm" |
| K-7 | Thấp | Lỗi | `catalog/pages/mix-groups-page.tsx:96`, `catalog/components/mix-group-form.tsx:110` | Cột thao tác thiếu `acts` (gap 18). | Thêm class |
| K-8 | Thấp | Lỗi | `catalog/components/mix-group-form.tsx:156,169` | Callout bad trong `MixGroupLive` không có role, mà `m-live` lại không phải live region. | `role="status"` hoặc `role="alert"` tùy mức |
| K-9 | Thấp | Lỗi | `catalog/pages/products-page.tsx:77` | Header cột trạng thái là `""`. | Chữ sr-only "Trạng thái" |
| K-10 | Thấp | Lỗi | `catalog/components/product-form.tsx:76,79,85,107` | MoneyInput và QtyInput không nhận `invalid`, nên thiếu `aria-invalid` dù dòng lỗi đang hiện. | Truyền `invalid={errors.x !== undefined}` |
| K-11 | Thấp | Lỗi | `partners/pages/suppliers-page.tsx:90,94,99,113` | Nút trong hàng không có tên NCC. | Thêm tên NCC vào `aria-label` |
| K-12 | Thấp | Lỗi | `partners/components/supplier-form.tsx:31` | Ô điện thoại ở form nhanh thiếu `inputMode: "tel"`, form đầy đủ ở dòng 53 có. | Thêm `inputMode` |
| K-13 | Thấp | Lỗi | `partners/components/supplier-form.tsx:75` | Checkbox bị disabled khi `customerLocked` mà không có lời giải thích. | Thêm hint nêu lý do |
| K-14 | Thấp | Lỗi | `catalog/pages/mix-groups-page.tsx:108` | "Dùng lại" dùng `disabled={reactivate.pending}`: mọi hàng bị khóa và không có `aria-busy`. | `pending` cho riêng hàng đó |
| K-15 | Thấp | Cải tiến | `pricing/components/edit-price-dialog.tsx:64–65` | Cảnh báo giá sỉ và giá tối thiểu nằm trong `m-live` không có `aria-live`, nên không được đọc khi xuất hiện. | `role="status"` trên Callout warn |
| K-16 | Thấp | Đổi nhãn/hành vi | `partners/pages/suppliers-page.tsx:106` | "Ngừng" NCC áp dụng ngay, trong khi thao tác tương đương ở nhóm mix có xác nhận. | Chủ sản phẩm chọn có thêm xác nhận hay không |
| K-17 | Thấp | Đổi nhãn/hành vi | `catalog/components/product-form.tsx:93,96` | "Mã SKU" và "Tên hàng" không có dấu "*", trong khi form khách và NCC có. | Thống nhất ký hiệu bắt buộc |

`attribute-field-registry.tsx:93` có comment xác nhận không được đặt nhóm checkbox có nhãn bên trong `<label>` của Field. Dòng 93–105 là mẫu đúng để K-1 và S-1 dùng lại.

### Thiết lập (`features/settings`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| S-1 | Trung bình | Lỗi | `.../support-access-card.tsx:95–101` | `<Field label="Phạm vi xem">` (một `<label>`) bọc nhóm Checkbox (mỗi cái cũng là `<label>`), tạo `label` lồng `label`. Bấm vào vùng nhãn ngoài có thể bật nhầm checkbox, và tên truy cập bị gộp. | `fieldset` và `legend`, hoặc mẫu ở `attribute-field-registry.tsx:93–105` |
| S-2 | Trung bình | Đổi nhãn/hành vi | `.../mfa-setup-dialog.tsx:53–62` | Hộp mã khôi phục đóng được bằng Esc hoặc bấm nền, và mã mất vĩnh viễn: không có cảnh báo, không có nút sao chép hay tải xuống. | Chặn đóng ngoài ý muốn, thêm "Sao chép" và "Tải .txt", thêm checkbox "Tôi đã lưu mã" trước khi đóng |
| S-3 | Trung bình | Đổi nhãn/hành vi | `.../period-lock-card.tsx:73` | "Khóa sổ" chạy ngay, không xác nhận và không có ImpactSummary. PRD §9.2 yêu cầu "xác nhận phạm vi, lý do, ảnh hưởng". | CommitDialog có ngày, lý do và ảnh hưởng, giống luồng mở khóa đã có |
| S-4 | Trung bình | Đổi nhãn/hành vi | `.../owner-transfer-card.tsx:64` | Nút danger "Gửi lời mời chuyển quyền" gửi ngay, không có hộp xác nhận. | Hộp xác nhận nêu người nhận và hệ quả |
| S-5 | Thấp | Lỗi | `.../data-export-card.tsx:19,33` | "Đang tạo bản xuất…" không có `role="status"`. Callout thất bại không có `role="alert"`. Đổi trạng thái không được đọc, khác với `ExportPanel` bên reports. | Thêm role như `export-panel.tsx:37` |
| S-6 | Thấp | Lỗi | `.../period-lock-card.tsx:73`, `.../support-access-card.tsx:127`, `.../store-profile-card.tsx:143`, `.../offline-policy-card.tsx:80` | Mẫu `disabled` kèm `title` để giải thích quyền chỉ xem: bàn phím và cảm ứng không đọc được. Hint ở `support-access-card.tsx:131` có hiện nhưng không gắn với nút. | Hint hiển thị và `aria-describedby` |
| S-7 | Thấp | Lỗi | `.../mfa-setup-dialog.tsx:78` | Link `<a>` nằm trong hint, mà hint nằm trong `<label>` của Field. Bấm vào link có thể kích hoạt label, và tên truy cập của ô bị kéo dài. | Đưa link ra ngoài Field |
| S-8 | Thấp | Cải tiến | `.../mfa-setup-dialog.tsx:79` | Ô secret là `readOnly` nhưng không có nút sao chép (store-profile đã có mẫu này). | Thêm "Sao chép" |

Điểm tốt:

- `mfa-setup-dialog.tsx:82`: ô mã có `inputMode="numeric"`, `autoComplete="one-time-code"`, bỏ khoảng trắng, không chặn dán.
- `load-error.tsx`, `devices-card.tsx`, `invoices-card.tsx` và `plan-cards.tsx:54,95` có `aria-label`.
- `account-closure-card.tsx` có hộp xác nhận.
- `audit-log-page.tsx:82,91` có nhãn ô tìm kiếm, caption và trạng thái rỗng.
- `support-tickets-card.tsx:110,204` dùng `role="alert"` cho cảnh báo mật khẩu.

### Auth (`features/auth`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| A-1 | Thấp | Lỗi | `pages/signup-page.tsx:71–84`, `components/email-request-form.tsx:48–54` | Khi gửi thành công, form bị thay bằng màn "Kiểm tra email". Nút đang focus biến mất nên focus rơi về `body` (2.4.3). Callout `role="status"` vẫn được đọc. | Chuyển focus vào `h1` mới (`tabIndex={-1}`) |
| A-2 | Thấp | Lỗi | `components/slug-field.tsx:49–50` | `div.f` bọc `Field`, mà `Field` cũng là `label.f`, nên class `.f` bị lồng hai lần và khoảng cách có thể lệch prototype. | Bỏ `className="f"` ở div ngoài, hoặc dùng wrapper không có class |
| A-3 | Thấp | Cải tiến | `pages/signup-page.tsx:78–81` | Địa chỉ cửa hàng hiện ra kèm lời dặn "Lưu lại địa chỉ này" nhưng không có nút sao chép. | Thêm "Sao chép địa chỉ" |

Kiểm 3.3.8 (Accessible Authentication, Minimum): **đạt**.

- Không có `onPaste` hay `preventDefault` nào trong auth, re-auth và admin.
- Đăng nhập: `login-page.tsx:80` dùng `autoComplete="username"`, dòng 83 dùng `"current-password"`.
- Đăng nhập admin: `admin-login-page.tsx:112` dùng `"username"`, dòng 115 dùng `"current-password"`.
- Mật khẩu mới: `new-password-fields.tsx:19,22` dùng `"new-password"`.
- TOTP: `mfa-code-forms.tsx:39` dùng `inputMode="numeric"`, `autoComplete="one-time-code"` và `maxLength={7}`. `maxLength` 7 chấp nhận dạng dán "123 456", và dòng 28 bỏ khoảng trắng trước khi gửi.
- Mã khôi phục: `mfa-code-forms.tsx:69` tắt autocapitalize và spellcheck.
- Re-auth: `shared/ui/reauth-dialog.tsx:58` dùng `"current-password"`.
- Không có CAPTCHA hay bài kiểm tra nhận thức nào.
- Ô "Nhập lại mật khẩu" được miễn trừ 3.3.7 vì lý do bảo mật.

Lỗi luôn có `role="alert"`: `form-alert.tsx:8`, `verify-page.tsx:60`, `reset-password-page.tsx:53`, và lỗi tại ô của `Field`. Các test sau khẳng định điều này: `login-page.test.tsx:77,88`, `admin-login-page.test.tsx:50,67`, `signup-page.test.tsx:100`, `verify-page.test.tsx:60,67`, `reset-password-page.test.tsx:60`, `email-request-pages.test.tsx:43`.

### Onboarding (`features/onboarding`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| O-1 | Thấp | Lỗi | `steps/opening-stock-step.tsx:111`, `steps/opening-cash-step.tsx:45` | Hai bảng `<table>` thô không có caption (gap 12). Header cột cuối rỗng ở `opening-stock-step.tsx:128` và `opening-cash-step.tsx:55`. | `TableCaption` "Tồn kho đầu kỳ" và "Số dư quỹ đầu kỳ"; chữ sr-only "Thao tác" cho cột cuối |
| O-2 | Thấp | Lỗi | `steps/opening-stock-step.tsx:172`, `steps/opening-cash-step.tsx:84` | Ô thao tác thiếu class `acts`, nên nút "×" và "Lưu số dư" cỡ sm không được 44 px trên màn cảm ứng (gap 18). | `<td className="acts">` |
| O-3 | Thấp | Cải tiến | `steps/opening-stock-step.tsx:89–91`, `steps/opening-cash-step.tsx:72` | Lỗi dòng chỉ hiện ở toast, không có `aria-invalid` hay dòng lỗi tại ô (3.3.1). Câu lỗi đã nêu mã hàng, đây là điểm tốt. | Đánh dấu `invalid` cho ô sai và focus vào ô đó |
| O-4 | Thấp | Cải tiến | `steps/opening-stock-step.tsx:147,151,161,164,167,168` | Gap 2 mới đóng một phần: nhãn dùng "dòng n". Khi đã chọn hàng, nên kèm mã hàng. | `` `${STOCK_LABELS.qty} ${l.sku?.code ?? `dòng ${n}`}` `` |
| O-5 | Thấp | Cải tiến | `steps/opening-stock-step.tsx:173,193` | Thêm dòng không chuyển focus sang ô hàng của dòng mới. Xóa dòng làm focus rơi mất. | Focus vào Combobox dòng mới; khi xóa thì focus vào dòng kế hoặc nút "+ Thêm dòng" |
| O-6 | Thấp | Cải tiến | `pages/onboarding-page.tsx:31,51–61` | Chọn một bước trong checklist thay nội dung bên dưới mà focus vẫn ở checklist, không có thông báo. | Focus vào region của bước (StepCard đã có `role="region"` ở `step-card.tsx:44–45`) |
| O-7 | Thấp | Lỗi | `pages/onboarding-page.tsx:44` | Callout "Các bước bắt buộc đã xong" xuất hiện mà không có `role="status"`. | Thêm `role="status"` |
| O-8 | Thấp | Lỗi | `steps/opening-debt-step.tsx:142` | Select lý do có thể hiện lỗi `errors.reason_id` nhưng không có `aria-invalid`. | `aria-invalid={errors.reason_id !== undefined \|\| undefined}` |
| O-9 | Thấp | Cải tiến (3.3.7) | `steps/store-profile-step.tsx:136–137` | Số điện thoại cửa hàng để trống, dù chủ đã nhập số của mình lúc đăng ký. Signup gửi phone vào owner (`apps/api/internal/identity/service.go:268–269`, `:144–162`), còn tenant chỉ nhận `DisplayName` (LegalName lấy theo DisplayName, `identity/ports.go:18`). Signup và onboarding cách nhau một bước xác minh email nên chưa chắc tính là "cùng một quy trình" theo 3.3.7. | Gợi ý điền sẵn số của chủ, kèm nút "Dùng số của chủ cửa hàng" |

Kiểm 3.3.7 (Redundant Entry): **đạt ở mức chính**.

- Tên pháp lý và tên hiển thị được điền sẵn từ tenant (`store-profile-step.tsx:41–48`, `identity/ports.go:18`).
- Ngày chốt nhập một lần rồi được dùng lại cho cả ba bước đầu kỳ (`onboarding-page.tsx:56`).
- Ở bước công nợ, lý do mặc định là "Số dư đầu kỳ" và được giữ lại sau mỗi lần ghi (`opening-debt-step.tsx:73,78`).
- Ở bước tồn kho, giá vốn được điền từ `ref_cost` hoặc `last_purchase_price` (`opening-stock-step.tsx:145`).
- Chỉ còn số điện thoại (O-9).

Điểm tốt:

- Bước xác nhận có ImpactSummary dạng KeyValueGrid, bảng có caption, checkbox đồng ý bắt buộc và re-auth (`confirm-opening-step.tsx:104–137`).
- `OpeningGate` giải thích và có link sang Thông tin cửa hàng.

### Admin (`features/admin`, `app/layouts/admin-shell.tsx`)

| Mã | Mức | Loại | Vị trí | Vấn đề | Đề xuất |
|---|---|---|---|---|---|
| M-1 | Cao | Lỗi | `app/layouts/admin-shell.tsx:25,27–32` | Vòng focus mặc định (navy-700) trên header `surface-dark` chỉ đạt 1.35:1. Người dùng bàn phím gần như không thấy focus trên 7 link nav và nút Đăng xuất (2.4.7, 1.4.11). | Thêm `outline` trắng cho `a:focus-visible` và `button:focus-visible` trong header, qua token mới `--on-dark-focus` (14.86:1) |
| M-2 | Thấp | Lỗi | `app/layouts/admin-shell.tsx:33` | Viền nút Đăng xuất (`on-navy-line`) đạt 1.46:1, nên ranh giới nút gần như không thấy (1.4.11). Padding `4px 12px` khiến chiều cao khoảng 26 px: đạt 24 px của 2.5.8 nhưng không đạt 44 px trên màn cảm ứng. | Viền `#6d7174`; `min-height: 44px` dưới 860 px |
| M-3 | Thấp | Cải tiến | `app/layouts/admin-shell.tsx:70–83` | Không có skip link trước 8 phần tử focus trong header. Landmark `header`, `nav` và `main#view` đã đủ cho 2.4.1, nên đây là cải tiến. | Link "Bỏ qua tới nội dung" trỏ `#view` |
| M-4 | Thấp | Cải tiến | `app/layouts/admin-shell.tsx:61` | Email quản trị chỉ hiện qua `title` khi hover. | Hiện email dạng chữ phụ hoặc sr-only |
| M-5 | Thấp | Cải tiến | `components/store-link.tsx:11` | Link `target=_blank` không báo mở tab mới. Link này lặp ở mọi hàng của `tenants-page.tsx:38`. | Chữ sr-only "(mở tab mới)" |
| M-6 | Thấp | Lỗi | `pages/feature-flags-page.tsx:70`, `containers/tenant-flags.tsx:59,63`, `pages/support-ticket-page.tsx:217,222` | Mẫu `disabled={pending}` khóa mọi nút cùng loại, không có `aria-busy`, và focus có thể rơi khi nút đang focus bị disabled. | `pending` cho đúng nút đang chạy |

Điểm tốt:

- `NavLink` tự gắn `aria-current="page"`. Nav có `aria-label`.
- Mọi nút trong hàng có ngữ cảnh: `invoices-page.tsx:76,79`, `feature-flags-page.tsx:69,75`, `partners-page.tsx:41` (kèm `aria-pressed`), `incidents-page.tsx:56`.
- Thao tác phá hủy dùng `TypedConfirmDialog` (gõ slug hoặc số hóa đơn) và ô gõ cho phép dán.
- `PagedRows` và `LoadError` dùng `role="alert"` kèm "Thử lại", không bao giờ hiện lỗi như danh sách rỗng.
- TOTP admin đạt 3.3.8 (xem phần Auth).
- Mọi bảng đều có caption.

## 5. WCAG 2.2: các tiêu chí mới

| Tiêu chí | Kết quả nhóm B | Ghi chú |
|---|---|---|
| 2.4.11 Focus Not Obscured (Minimum) | Không kết luận được bằng đọc code | Nhóm B không tự tạo header sticky. Header sticky của shell và drawer thuộc nhóm A. Cần kiểm thực tế khi tab qua bảng dài trong `.view`. |
| 2.5.8 Target Size (Minimum) | Đạt một phần | Nút sm và nút admin khoảng 26 px, đạt 24 px. `.lnk` padding 0 phụ thuộc ngoại lệ khoảng cách. Rule 44 px chỉ có cho `.view .t td.acts`, xem gap 18. |
| 3.3.7 Redundant Entry | Đạt (trừ O-9) | Xem phần Onboarding |
| 3.3.8 Accessible Authentication (Minimum) | Đạt | Xem phần Auth |

## 6. Ghi chú chuyển nhóm A (không tính là phát hiện của nhóm B)

- `shared/ui/field.tsx`: hint và lỗi nằm **trong** `<label>`, nên khi control không có `aria-label`, tên truy cập bị gộp cả hint và lỗi, và không có `aria-describedby`. Hệ quả ở nhóm B là S-7 và tên ô dài. Đề xuất: đưa hint và lỗi ra ngoài label và nối bằng `aria-describedby`.
- `globals.css:312–315`: rule 44 px cần tổ tiên `.view`, nên không áp dụng trong CommitDialog hay DetailDrawer (ví dụ `saved-reports-menu`, `pay-dialog`). Đề xuất: thêm selector cho `.m` và drawer.
- `globals.css:65`: `.lnk` padding 0. Đề xuất: `padding-block` và `min-height: 24px`.
- `shared/ui/commit-dialog.tsx:124`: `m-live` không có `aria-live`. Nếu thêm `aria-live="polite"` ở đây thì K-8 và K-15 tự đóng.
- `app/layouts/app-shell.tsx`: không có skip link nào trong toàn app.
- Vòng focus trên sidebar navy-700 đạt 1.00:1 (bảng 3).

## 7. Test unit nên thêm

1. `catalog/components/mix-group-form.test.tsx` (mới): khi `locked`, ô quy cách có `readOnly`, không `disabled`, vẫn nhận focus và có mô tả lý do. Mỗi hàng vị có nhãn riêng: `getByRole("combobox", { name: "Mặt hàng vị 1" })`, `getByRole("button", { name: /^Gỡ vị / })`. Bảng có tên: `getByRole("table", { name: "Các vị trong nhóm" })`.
2. `catalog/pages/mix-groups-page.test.tsx` (mới): "Ngừng" mở hộp xác nhận, không ghi ngay (gap 9).
3. `partners/pages/suppliers-page.test.tsx` (mới): lý do không ngừng được đọc qua `toHaveAccessibleDescription`. Nút hàng có tên NCC.
4. `pricing/pages/price-lists-page.test.tsx` và `price-history-table.test.tsx`: API trả 500 thì hiện `role="alert"` kèm "Thử lại", không hiện chữ rỗng.
5. Debt: sửa `pay-dialog.test.tsx:34` và `allocate-advance-dialog.test.tsx:31` để khóa đúng tên `"Phân bổ HD-0001"` thay vì regex tiền tố.
6. `settings/.../support-access-card.test.tsx`: `getByRole("group", { name: "Phạm vi xem" })` chứa các checkbox, và DOM không có `label label`.
7. `settings/.../mfa-setup-dialog.test.tsx`: Esc trên hộp mã khôi phục không đóng khi chưa xác nhận đã lưu, nếu chủ sản phẩm chấp nhận S-2.
8. `settings/.../data-export-card.test.tsx`: trạng thái "Đang tạo" có `role="status"`, thất bại có `role="alert"`.
9. `onboarding/steps/opening-balances.test.tsx`: thêm `getByRole("table", { name: "Tồn kho đầu kỳ" })` và `name: "Số dư quỹ đầu kỳ"`. Nhãn ô gồm mã hàng sau khi chọn hàng.
10. `auth/pages/signup-page.test.tsx`: sau khi đăng ký, `document.activeElement` là `h1` "Kiểm tra email của bạn".
11. `app/layouts/admin-shell` (test hiện có trong `features/admin/console-shell.test.tsx`): link nav đang mở có `aria-current="page"`.

## 8. Không soát được bằng đọc code

- Độ khớp hình ảnh với prototype v6.14 cho từng màn nhóm B.
- Chạy axe (`table-caption`, `label`, `nested-interactive`, `color-contrast`) và một lượt kiểm với trình đọc màn hình (NVDA và VoiceOver, tiếng Việt).
- Kích thước thật của `.lnk`, nút sm và nút admin, và ngoại lệ khoảng cách của 2.5.8 (cần đo bounding box).
- 2.4.11 với header sticky và drawer mở.
- Thiết bị có con trỏ thô (`pointer: coarse`) và breakpoint ≤860 px.
- Màu placeholder thật trên Safari và Firefox.
- Thứ tự đọc khi live region đổi nội dung (export, slug).
- Đường e2e `catalog.spec.ts`, `reports.spec.ts` và `touch-targets.spec.ts` (không được chạy e2e).

## 9. Câu hỏi chưa giải quyết

1. Bộ cột của lịch thu/trả (`schedule-page.tsx:51–58`) giữ như hiện tại hay theo spec §5.6.4?
2. Có chấp nhận làm tối accent-orange, crimson và gradient (thêm token mới sau marker, không sửa giá trị prototype) để chữ trắng đạt 4.5:1 không?
3. "Khóa sổ" (S-3), "Gửi lời mời chuyển quyền" (S-4) và "Ngừng" NCC (K-16) có cần hộp xác nhận không?
4. Hộp mã khôi phục (S-2) có chặn đóng cho tới khi chủ xác nhận đã lưu không?
5. "In phiếu" đổi thành "In bảng kê" (D-9), và "*" cho trường bắt buộc ở form hàng hóa (K-17)?
6. O-9: có điền sẵn số điện thoại của chủ vào hồ sơ cửa hàng không, hay hai số này cố ý tách riêng?

Status: DONE_WITH_CONCERNS
Summary: Đã soát nhóm B và đối chiếu đủ 10 hàng gap. Gap 7, 9, 13, 14 và 17 đã đóng, hoặc không áp dụng (gap 7). Gap 2, 11, 12 và 18 còn mở một phần, chủ yếu ở nhóm mix và hai bảng onboarding. 3.3.7 và 3.3.8 đạt, nhưng còn 1 lỗi mức cao (focus trên header admin) và 9 phát hiện mức trung bình.
Concerns/Blockers: Chữ trắng trên nút chính màu cam (3.26:1) và viền ô nhập (1.74:1) không đạt; sửa phải thêm token, cần chủ sản phẩm duyệt. Bốn file settings chưa soát từng dòng. Không chạy axe hay e2e nên tiêu chí 2.4.11 và 2.5.8 chưa kiểm được.
