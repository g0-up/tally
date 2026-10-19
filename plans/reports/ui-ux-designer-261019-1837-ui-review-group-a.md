# Rà soát UI/UX & a11y — nhóm A (Phase 20)

Ngày: 2026-10-19 18:37 (Asia/Saigon). Phạm vi: Tổng quan (dashboard, thông báo), Bán hàng (POS, hóa đơn/báo giá/trả hàng/giao hàng), Nhập hàng, Kho, Giỏ quà, POS offline/PWA, trang in `/print/...`, app shell, `shared/ui`, `app/layouts`.
Phương pháp: chỉ đọc code `apps/web/src` và đối chiếu với `plans/261016-0854-tally-full-product-implementation/phase-20-ui-ux-review-accessibility.md` (gap matrix, UI spec §8), PRD NFR-11, §9.2–9.3 và WCAG 2.2 AA. Không chạy dev server, e2e, axe runtime hay trình đọc màn hình. Đường dẫn `shared/…`, `app/…` tính từ `apps/web/src/`; đường dẫn module (`pos/…`, `sales/…`, `procurement/…`, `inventory/…`, `kits/…`, `dashboard/…`, `notifications/…`) tính từ `apps/web/src/features/`; `e2e/…` tính từ `apps/web/`.

## Kết luận nhanh

- **Gap matrix:** 9/14 dòng thuộc nhóm A đã đóng trong code (1, 6, 8, 10, 11, 13, 15, 16, 17). Dòng 2 và 12 mới đóng một phần: phần bán hàng và giỏ quà còn thiếu ngữ cảnh dòng, ba bảng thô chưa có caption. Dòng 4 cũng đóng một phần. Dòng 5 có Radix nhưng chưa có `focus.spec.ts`. Dòng 18 mới chỉ phủ hai selector dưới `pointer: coarse`.
- **Lỗ hổng kiểm thử:** chưa có `focus.spec.ts`, `touch-targets.spec.ts`, cũng chưa có unit test cho `DataTable`, `StatusPill`, `DatePicker`, `StockColorPicker`, `Field`, `Toaster`.
- **Phát hiện chính:**
  - Không có `document.title` theo route (2.4.2).
  - Sidebar mobile khi đóng vẫn nhận focus.
  - Thanh util sticky che focus (2.4.11).
  - Nhiều vùng chạm dưới 24px hoặc dưới 44px.
  - Callout có `role=status` được render có điều kiện, nên có thể không được đọc.
  - Hint nằm trong `<label>` làm bẩn tên truy cập.
  - Mất focus sau khi xóa dòng.
- **Chưa phát hiện vi phạm 3.3.7** (nhập lại dữ liệu) trong nhóm A. Xem mục Ghi chú tích cực.
- **3.3.8** (dán mật khẩu/TOTP) thuộc auth, tức nhóm B, nên không rà ở đây.

## 1. Gap matrix (các dòng thuộc nhóm A)

| # | Gap | Trạng thái | Bằng chứng (file:dòng) | Test |
|---|---|---|---|---|
| 1 | POS chọn khách bằng native select | **Đóng** | `features/pos/components/customer-bar.tsx:33-40` dùng `Combobox kind=party`, aria-label "Đổi khách hàng" | Một phần. `shared/ui/combobox.test.tsx` có test bàn phím ở mức chung. `e2e/pos.spec.ts:185` chọn qua `pickOption` (`e2e/support/sales-data.ts:138-142`) bằng **click**, không phải bàn phím như matrix yêu cầu |
| 2 | Ô nhập trong bảng thiếu ngữ cảnh dòng | **Một phần** | Đóng ở: POS `pos/components/cart-line-row.tsx:44,50,64`; nhập hàng `procurement/components/buy-preset.tsx:34-37` (helper `rowLabel`); kiểm kê `inventory/components/count-drawer.tsx:102,120`; trả/đổi/xuất `sales/containers/return-dialog.tsx:177,204,213`, `exchange-dialog.tsx:158`, `stock-out-dialog.tsx:88`. **Còn mở** ở `sales/components/line-items/item-line-row.tsx:73-145`, `sales/components/mix-pack-row.tsx:78-142`, `kits/components/recipe-editor.tsx:76,80`, `kits/components/mix-line-row.tsx:83-115`, cùng mọi `StockColorPicker`/`ColorSwatchGroup` không truyền label | Có cho nhập hàng (`buy-preset.test.tsx:37,52`, `new-receipt-dialog.test.tsx:42,88,124,163`) và kiểm kê (`count-drawer.test.tsx:57,85`). **Chưa có** cho POS cart line, line items hóa đơn/báo giá, giỏ quà |
| 4 | Nav accordion vừa mở vừa điều hướng | **Một phần** | `app/layouts/sidebar.tsx`: `aria-current` đúng, nhưng nút nhóm chưa active mang `aria-expanded=false` mà click lại điều hướng (F-S5) | `app-shell.test.tsx` (3 test), `e2e/shell.spec.ts:62-82` (assert `aria-current`) |
| 5 | Không trap/restore focus | **Đóng phần trap/restore cơ bản** (Radix Dialog/Sheet) | `shared/hooks/use-return-focus.ts:16-22` khôi phục về opener, nhưng **không có fallback** khi opener bị gỡ khỏi DOM (F-S6) | Restore: `dialog-focus.test.tsx`. Trap: chưa có test. **Không có `e2e/focus.spec.ts`** |
| 6 | Toast lỗi tự tắt | **Đóng** | `shared/ui/toast-store.ts`: toast `err` không hẹn giờ (ok 4500 ms) | `use-commit.test.tsx` |
| 8 | Glyph `.st` trang trí | **Đóng** | `shared/ui/status-pill.tsx:12` `aria-hidden` | **Chưa có** unit test. axe chưa chạy |
| 10 | Swatch radio không roving | **Đóng** | `shared/ui/color/color-swatch-group.tsx` (Radix RadioGroup) | `color-swatch-group.test.tsx` (phím mũi tên). `StockColorPicker` chưa có test riêng |
| 11 | colorPrice ô `disabled` | **Đóng** | `inventory/containers/color-price-dialog.tsx:109,115` `readOnly={draft.base}` + hint L121 | `color-price-dialog.test.tsx:72` `toHaveAttribute("readonly")`. Riêng `kits/containers/kit-dialog.tsx:155,164` vẫn dùng `disabled` (F-K3) |
| 12 | Bảng thiếu caption | **Một phần** | `shared/ui/data-table/data-table.tsx:39` bắt buộc `caption`. Bảng thô **không** caption: `pos/components/cart-panel.tsx:29`, `sales/components/line-items/line-items-editor.tsx:50`, `procurement/components/buy-preset.tsx:251` | **Chưa có** unit test `DataTable`. Kiểm tra tùy biến `table-caption` của axe chưa chạy |
| 13 | Date input native | **Đóng** | `shared/ui/date-picker.tsx` (dd/mm/yyyy + lưới lịch) | Gián tiếp qua `period-bar.test.tsx` (gõ ngày). **Chưa có** `date-picker.test.tsx` (bàn phím trong lưới, ngày sai) |
| 15 | Re-render khi blur nuốt click | **Đóng** | Input có điều khiển (`shared/ui/money-input.tsx`), `useSettledPricing` trong `pos/containers/pos-cart-container.tsx` | e2e `pos.spec.ts:135` (chưa chạy được). Không thấy unit test blur-rồi-click |
| 16 | POS mở ca âm thầm | **Đóng theo quyết định** (giữ ca ngầm định, chỉ callout fallback). **Chủ sản phẩm cần xác nhận** | `pos/components/shift-fallback-callout.tsx:8-14` `role=alert` "Chưa có ca bán hàng đang mở." khi `shift_unavailable` | **Chưa có** unit test (grep `shift_unavailable` trong test: 0 kết quả). e2e `pos.spec.ts:109` liên quan |
| 17 | Toast thành công trước khi ghi sổ | **Đóng** | `shared/hooks/use-commit.ts`: toast chỉ sau 2xx, nhãn chờ "Đang ghi sổ…" | `use-commit.test.tsx` (MSW trả chậm) |
| 18 | Vùng chạm nhỏ | **Một phần** | `shared/styles/globals.css:312-315` chỉ nâng `.tgl-hit` và `.btn-sm` trong bảng lên 44px dưới `@media (pointer: coarse)`, không theo `≤860px`. Nhiều target vẫn dưới 24px (F-S1) | **Không có `e2e/touch-targets.spec.ts`** |

Các dòng 3, 7, 9, 14 thuộc nhóm B nên không chấm ở đây.

## 2. Phát hiện theo module

Quy ước:
- **Loại:** `defect` (sửa trong folder feature, không đổi hành vi hay nhãn); `nhãn/hành vi` (chủ sản phẩm quyết); `enhancement` (đưa vào backlog).
- **Mức:** Cao / Trung bình / Thấp.

### 2.1 App shell, `shared/ui`, `app/layouts`

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-S1 | Cao | defect | `shared/styles/globals.css:65` `.lnk` (padding 0, cao ~21.7px); `:237` `.sw` 22×22; `:272` `.cp` (~26px); `:300` `.btn-sm` trong bảng (~21px); `:325` `.toast-x` (~14×18); `:146` `.x` (~30px); `:108` `.chip` (~32px) | `.lnk`, `.sw`, `.btn-sm` trong bảng và `.toast-x` **dưới 24×24** ở desktop. Ngoại lệ khoảng cách không áp được cho `.btn-sm` cạnh nhau trong cột thao tác. Rule 44px (`:312-315`) chỉ phủ `.tgl-hit` và `.btn-sm` trong bảng, lại chỉ dưới `pointer: coarse`. Chưa phủ: `.sw`, `.cp`, `.chip`, `.lnk`, `.x`, `.toast-x`, `.bell`, `.nav-sub a`, `.dp-btn` | WCAG 2.5.8; gap 18 ("≥24×24 everywhere, ≥44px hit area ≤860px") | Đặt `min-block-size`/`min-inline-size: 24px` cho các selector trên. Thêm `@media (max-width: 860px), (pointer: coarse)` để nâng vùng chạm lên 44px, dùng pseudo-element `::after { inset: -N }` để không đổi layout. Kiểm lại bằng `touch-targets.spec.ts` |
| F-S2 | Cao | defect | `shared/styles/globals.css:258-267`, `app/layouts/sidebar.tsx` | Ở ≤860px sidebar đóng chỉ `translateX` ra ngoài, không có `visibility:hidden` hay `inert`. Tab vẫn đi qua các link vô hình, focus nằm ngoài màn hình | WCAG 2.4.7 / 2.4.11, 2.4.3 | Khi đóng: `inert` trên `<nav>` (hoặc `visibility:hidden` sau transition). Khi mở: focus vào link đầu hoặc link hiện tại; Esc/đóng trả focus về nút menu |
| F-S3 | Trung bình | defect | `app/router.tsx:33-41` (`routeMeta.title` đã có trong `handle`); `app/layouts/app-shell.tsx` (không áp) | Mọi route giữ `<title>Tally</title>` (`index.html`). grep `document.title`: 0 kết quả. Trang in còn ra tên file PDF là "Tally" | WCAG 2.4.2 | Trong `AppShell`, dùng `useMatches()` lấy `handle.title` và đặt `document.title = \`${title} · Tally\``. Trang in đặt theo số chứng từ |
| F-S4 | Trung bình | defect | `shared/styles/globals.css:32` `.util` sticky; không có `scroll-padding-top` ở đâu | Tab xuống field gần đỉnh viewport bị thanh util sticky che (focus bị che toàn phần) | WCAG 2.4.11 | `html { scroll-padding-top: var(--util-h) }` (hoặc trên container cuộn của `main`) |
| F-S5 | Trung bình | nhãn/hành vi | `app/layouts/sidebar.tsx` | Nút nhóm chưa active có `aria-expanded=false` nhưng click lại điều hướng sang trang con đầu, tức cùng lúc là disclosure và link. Gap 4 yêu cầu tách hai việc này | UI spec §8 gap 4; WCAG 4.1.2 | Hoặc tách nút "mở rộng" (chevron) khỏi link nhóm, hoặc bỏ `aria-expanded` và render nhóm như link. Chủ sản phẩm chọn cách nào |
| F-S6 | Trung bình | defect | `shared/hooks/use-return-focus.ts:16-22` | Khi opener đã bị gỡ (xóa giỏ, bỏ giao dịch đồng bộ lỗi, xóa đơn chặn), hàm return sớm, focus rơi về `<body>`. Ảnh hưởng `pos/containers/delete-cart-dialog.tsx`, `pos/offline/components/sync-exception-drawer.tsx:104-114`, `blocked-sales-callout.tsx:68-82` | WCAG 2.4.3 | Nhận thêm `fallback` (dòng kế tiếp, tiêu đề bảng có `tabIndex=-1`, hoặc `main`) và focus vào đó khi `!opener.isConnected` |
| F-S7 | Trung bình | defect | `shared/ui/field.tsx:26-35` | `<label>` bọc control, hint và lỗi `role=alert`, nên tên truy cập gồm cả hint và lỗi. Không có `aria-describedby` hay `aria-invalid`. Ví dụ: "Số ngày nợ" ở `sales/containers/invoice-dialog.tsx:211-213` | WCAG 1.3.1, 2.5.3, 3.3.1 | Tách: `<label htmlFor>` chỉ chứa text nhãn; hint và lỗi có `id` và gắn vào control qua `aria-describedby`; khi có lỗi thì thêm `aria-invalid`. Mẫu đúng đã có ở `inventory/components/reason-field.tsx:19-31` |
| F-S8 | Trung bình | defect | `shared/ui/toaster.tsx` | `<ol aria-live=polite>` chứa từng `<li role=alert|status>`, tức live region lồng nhau. Một số tổ hợp SR đọc trùng hoặc bỏ qua | WCAG 4.1.3 | Hai vùng tách biệt, luôn có trong DOM: một `role=status` cho ok, một `role=alert` cho lỗi; `li` không mang role |
| F-S9 | Trung bình | defect | `shared/styles/globals.css:156` `.toasts` fixed góc phải dưới, z 60; `pos/offline/components/update-prompt.tsx:26` fixed góc trái dưới, z 60, maxWidth 420 | Trên mobile (≤420px), toast lỗi (persistent, tối đa 5) và UpdatePrompt có thể che nút "Thanh toán · F9" hoặc field đang focus ở đáy màn hình | WCAG 2.4.11 | Trên ≤860px, xếp toast và UpdatePrompt trong một stack chung và thêm `padding-bottom` cho `main` bằng chiều cao stack. Hoặc đặt toast ở đỉnh trên mobile |
| F-S10 | Trung bình | defect | `shared/ui/commit-dialog.tsx` (prop `live` render `<div className="m-live">`) | Tên `live` gợi ý vùng thông báo nhưng div không có `aria-live`/`role`. Nội dung đổi trong đó (preview, cảnh báo hạn mức) không được đọc | WCAG 4.1.3 | Đặt `role="status"` (hoặc `aria-live="polite"`) trên `.m-live`, luôn render wrapper và chỉ đổi nội dung bên trong |
| F-S11 | Thấp | defect | `app/layouts/app-shell.tsx` | Không có skip link; thanh util không phải landmark `header`; đổi route không chuyển focus hay thông báo | WCAG 2.4.1; khuyến nghị cho SPA | Thêm "Bỏ qua tới nội dung" trỏ `#main`. Bọc util bằng `<header>`. Khi đổi route, focus `h1` (`tabIndex=-1`) |
| F-S12 | Thấp | defect | `shared/ui/combobox.tsx` | `aria-label` mặc định đè nhãn hiển thị khi đặt trong `Field`. Trạng thái "đang tải" và "không có kết quả" trong listbox không được thông báo | WCAG 2.5.3, 4.1.3 | Chỉ đặt `aria-label` khi không có nhãn ngoài. Thêm vùng `role=status` sr-only báo "N kết quả" / "Không tìm thấy" |
| F-S13 | Thấp | defect | `shared/ui/exception-confirm-dialog.tsx` | Bấm xác nhận khi chưa tick ô thì báo bằng toast lỗi, không báo ngay tại ô | WCAG 3.3.1 | Lỗi inline gắn `aria-describedby` + `aria-invalid` vào checkbox, focus về checkbox |
| F-S14 | Thấp | defect | 13 cột `header: ""`, ví dụ `notifications/pages/notifications-page.tsx:66`, `inventory/components/stock-table.tsx:208`, `sales/components/invoice-table.tsx:115`. Cột **dữ liệu** không tiêu đề: `sales/components/invoice-drawer.tsx:88` (trạng thái thanh toán) | `<th>` rỗng (axe `empty-table-header`). Ở `invoice-drawer` cột trạng thái mất header nên SR đọc ô không có ngữ cảnh | axe best-practice; WCAG 1.3.1 (cột dữ liệu) | Cho `Column.header` nhận node và dùng `<span className="sr-only">Thao tác</span>` (mẫu đã có ở `kits/components/recipe-editor.tsx:120`). `invoice-drawer:88` dùng "Trạng thái" |
| F-S15 | Thấp | enhancement | `shared/ui/data-table/data-table.tsx:42` | Chỉ có `th scope=col`, ô đầu mỗi dòng không phải row header. Khi đi theo cột, SR không đọc tên hàng hay số chứng từ | WCAG 1.3.1 (khuyến nghị) | Cờ `rowHeader` trên `Column` để render `<th scope="row">` |

### 2.2 Bán hàng — POS

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-P1 | Trung bình | defect | `pos/components/cart-panel.tsx:29,38` | Bảng giỏ hàng là `<table>` thô, không caption; `<th />` rỗng | Gap 12; axe | Thêm `<caption className="sr-only">Giỏ hàng</caption>` và sr-only "Thao tác" |
| F-P2 | Trung bình | defect | `pos/components/cart-line-row.tsx:76-77` | `StockColorPicker` không truyền label nên mọi dòng mang cùng tên "Màu khách lấy" (`shared/ui/color/stock-color-picker.tsx:33`). "+ Thêm màu khác" cũng không có ngữ cảnh | Gap 2 | `label={\`Màu khách lấy – ${name}\`}`; nút thêm màu dùng `aria-label={\`Thêm màu khác – ${name}\`}` |
| F-P3 | Trung bình | defect | `pos/components/product-tile.tsx:31` | `aria-label` đặt trên `<span className="tdots">` không có role, nằm trong `<button>`. Tên nút không chứa danh sách màu (aria-label bị bỏ qua trên span generic) | ARIA 1.2 (cấm đặt tên cho generic); WCAG 1.1.1 | Bỏ `aria-label`, dùng `<span className="sr-only">Màu: …</span>`; các chấm `aria-hidden` |
| F-P4 | Trung bình | defect | `pos/components/payment-method-panel.tsx:59` (ô chuyển khoản offline `disabled` + `title`); `pos/components/saved-carts-table.tsx:101-111` (Sửa/Sao chép/Xóa khóa: `disabled` + `title`); `pos/containers/pos-cart-container.tsx` ("Lưu giỏ · F4" offline: `disabled` + `title`) | Lý do bị khóa chỉ nằm trong `title`. Control `disabled` không nhận focus nên người dùng bàn phím, cảm ứng và SR không đọc được lý do | WCAG 1.3.1 / 3.3.2; `title` không phải nguồn mô tả tin cậy | Hiện lý do bằng text kế bên (như hậu tố ở checkbox ghi nợ L67), hoặc dùng `aria-disabled="true"` + `aria-describedby` trỏ text lý do để control vẫn focus được |
| F-P5 | Thấp | defect | `pos/components/cart-line-row.tsx:68`; `pos/components/saved-carts-table.tsx:101-111` | "Xóa", "Sửa", "Sao chép" lặp lại mỗi dòng, không có ngữ cảnh | WCAG 2.4.6 / 2.4.4 (AAA cho link; khuyến nghị) | `aria-label={\`Xóa ${name}\`}` / `Sửa giỏ ${label}` |
| F-P6 | Thấp | defect | `pos/components/cash-chips.tsx:11` | Nút chip ~32px, đạt 24px nhưng dưới 44px ở ≤860px. POS là màn hình cảm ứng chính | Gap 18 | Gộp vào F-S1 |
| F-P7 | Thấp | enhancement | `pos/hooks/use-pos-hotkeys.ts` | F2/F4/F9 không khai báo `aria-keyshortcuts` trên các nút tương ứng | ARIA `aria-keyshortcuts` | Thêm `aria-keyshortcuts="F9"` vào "Thanh toán", v.v. |
| F-P8 | Thấp | enhancement | `pos/containers/pos-cart-container.tsx` | Tổng "Khách cần trả" đổi khi thêm hàng hay đổi chiết khấu nhưng không được thông báo | WCAG 4.1.3 (khuyến nghị) | Vùng `role=status` sr-only, debounce 500 ms, báo tổng mới |

### 2.3 POS offline / PWA

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-O1 | Trung bình | defect | `pos/offline/components/offline-banner.tsx:37`; `sync-queue-callout.tsx:8`; callout "chưa rõ kết quả thanh toán" và "khách offline" trong `pos/containers/pos-cart-container.tsx` | Các `Callout role=status` chỉ được render khi điều kiện đúng, tức vùng live được chèn **cùng lúc** với nội dung. NVDA/JAWS và VoiceOver thường không đọc trường hợp này, nên người dùng SR không biết mình vừa mất mạng hay giao dịch đang chờ | WCAG 4.1.3; yêu cầu task (banner và kết quả sync phải được thông báo) | Đặt một wrapper `role=status` luôn có trong DOM ở shell POS và chỉ thay nội dung bên trong. Callout hiển thị không cần role. Xác minh bằng SR thật (mục 4) |
| F-O2 | Trung bình | defect | `pos/offline/components/sync-exception-drawer.tsx:104-114`; `blocked-sales-callout.tsx:68-82` | Sau "Bỏ qua giao dịch" hoặc "Xóa khỏi máy", dòng biến mất làm focus rơi về `body` | WCAG 2.4.3 | Sửa qua F-S6 (fallback focus sang dòng kế hoặc tiêu đề drawer) |
| F-O3 | Thấp | defect | `pos/offline/components/blocked-sales-callout.tsx:56-61` | Khi đang gửi, mọi nút "Gửi lại"/"Xóa khỏi máy" bị `disabled` và dòng đang gửi không có nhãn chờ. Focus trên nút vừa bấm bị mất do nút disabled | WCAG 4.1.3, 2.4.3; quy ước `pending` của `Button` | Dùng `pending` + `pendingLabel="Đang gửi…"` cho dòng đang gửi. Các dòng khác giữ `aria-disabled` để không mất focus |
| F-O4 | Thấp | defect | `pos/offline/components/sync-exception-drawer.tsx:84-96` | "Ghi sổ lại"/"Bỏ qua giao dịch" không có ngữ cảnh dòng; khi không xử lý được thì `disabled` + `title` (cùng vấn đề F-P4) | WCAG 2.4.6, 1.3.1 | `aria-label` kèm số chứng từ tạm; lý do hiển thị thành text |
| F-O5 | Thấp | defect | `pos/offline/components/update-prompt.tsx:26` | `role=status` render có điều kiện (như F-O1) và có thể che nội dung (F-S9) | WCAG 4.1.3, 2.4.11 | Như F-O1 / F-S9 |

`sw.ts` (40 dòng) không có UI. Không có phát hiện a11y. Hành vi offline là precache shell, NetworkOnly cho `/api/`, `SKIP_WAITING` qua message, khớp mô tả trong file.

### 2.4 Bán hàng — hóa đơn / báo giá / trả hàng / giao hàng

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-B1 | Cao | defect | `sales/components/line-items/item-line-row.tsx:73` Combobox "Tên sản phẩm", `:85` `StockColorPicker` không label, `:87` `ColorSwatchGroup` không label, `:93` select "Đơn vị tính", `:105` `QtyInput` "Số lượng", `:108,126,145` `MoneyInput` "Giá"/"Đơn giá", `:111` `PercentInput` "Chiết khấu %", `:118` "Xóa", `:142` "Quy cách" | Hóa đơn, báo giá và dòng mới khi đổi hàng có N dòng, mọi ô cùng một tên. SR không phân biệt được dòng; gap 2 còn mở với luồng bán hàng chính | Gap 2 ("aria 'SL – {tên hàng}' …, 15–18 usage"); WCAG 2.4.6, 1.3.1 | Thêm helper giống `procurement/components/buy-preset.tsx:34-37` (`rowLabel(label, name, index)` — dòng chưa chọn hàng dùng "dòng {n}") cho mọi ô. Bổ sung unit test truy vấn `getByLabelText("Số lượng – …")` |
| F-B2 | Trung bình | defect | `sales/components/mix-pack-row.tsx:78` "Chọn nhóm mix vị", `:104` "Chiết khấu %", `:110` "Số thùng", `:120` "Xóa", `:142` "Quy cách" | Như F-B1 cho dòng mix. Riêng ô vị ở `:166` đã đúng mẫu | Gap 2 | Như F-B1, dùng tên nhóm mix làm ngữ cảnh |
| F-B3 | Trung bình | defect | `sales/containers/invoice-dialog.tsx:189` (Callout vượt hạn mức nợ / dưới giá tối thiểu, không role), nằm trong slot `live` `:178-198` (không phải live region, F-S10) | Cảnh báo xuất hiện khi đổi khách hay số lượng nhưng SR không được báo, nên người dùng SR có thể tiếp tục ghi sổ khi đang vượt hạn mức | WCAG 4.1.3 | Sửa F-S10 (wrapper `.m-live` luôn có `role=status`). Lỗi chặn ghi sổ thì dùng `role=alert` |
| F-B4 | Trung bình | defect | `sales/components/mix-pack-row.tsx:24` (Callout vượt cap, tone=bad, không role; `.m-live-mix` ở `:173` không live) | Vượt giới hạn vị trong thùng không được thông báo | WCAG 4.1.3 | `role=status` trên wrapper luôn render, hoặc `role=alert` nếu lỗi chặn |
| F-B5 | Trung bình | defect | `sales/containers/exchange-dialog.tsx:206` | Callout lỗi preview (tone=bad) không role | WCAG 4.1.3 | `role=alert` |
| F-B6 | Trung bình | defect | `sales/components/line-items/line-items-editor.tsx:50,60,72` | Bảng dòng hàng thô không caption, `th` rỗng | Gap 12 | Caption sr-only "Dòng hàng"; header sr-only "Thao tác" |
| F-B7 | Thấp | defect | `sales/containers/invoice-dialog.tsx:141,145` | Lỗi kiểm tra (thiếu khách, thiếu dòng) chỉ báo bằng toast lỗi; focus không đưa về field lỗi | WCAG 3.3.1 (đạt: lỗi bằng text); 3.3.3 khuyến nghị | Ngoài toast, đặt `error` trên `Field` tương ứng và focus field đầu tiên lỗi |
| F-B8 | Thấp | defect | `sales/containers/return-dialog.tsx:189` Combobox "Hàng trả"; `sales/components/return-color-cell.tsx` "Màu khách trả" | Thiếu ngữ cảnh dòng (các ô SL/đơn giá trả cùng dialog đã có) | Gap 2 | Thêm `– ${name}` / "dòng {n}" |
| F-B9 | Thấp | defect | `sales/components/invoice-table.tsx:26-36` (Sửa/Xóa/Sao chép), `:53` (ô đang tải là "…") | Nút lặp không ngữ cảnh. "…" trong ô được SR đọc thành "chấm chấm chấm" | WCAG 2.4.6 | `aria-label` kèm số hóa đơn; "…" thay bằng `aria-hidden` + sr-only "Đang tải" |

### 2.5 Trang in `/print/...`

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-R1 | Trung bình | defect | `sales/pages/print-sales-document-page.tsx:18-41` | Không có `document.title`, nên tab và tên file "Lưu PDF" đều là "Tally" (gộp F-S3). Không có nút quay lại/đóng; trang mở trực tiếp chỉ thoát được bằng nút Back của trình duyệt | WCAG 2.4.2 | `document.title = \`${doc_no} · Tally\``; thêm nút "Đóng" (`window.close()` nếu mở bằng `window.open`, ngược lại quay về chứng từ) trong `.print-bar` |
| F-R2 | Thấp | defect | `sales/components/print-document.tsx:38-39` | `th` không có `scope` (caption sr-only đã có) | WCAG 1.3.1 | `scope="col"` |

Điểm tốt: lỗi dùng `role=alert` (L37), đang tải dùng `role=status` (L41), ToggleGroup "Khổ giấy" có nhãn, `article` có aria-label, h1/h2 đúng cấp, `.print-bar` ẩn khi in (`sales/print.css:43`).

### 2.6 Nhập hàng

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-N1 | Trung bình | defect | `procurement/components/buy-preset.tsx:251,270` | Bảng thô không caption, `th` rỗng | Gap 12 | Caption sr-only "Hàng nhập"; header sr-only "Thao tác" |
| F-N2 | Thấp | defect | `procurement/components/buy-preset.tsx:133` Checkbox "Quản lý lô/hạn"; `:162,243` "Xóa" | Thiếu ngữ cảnh dòng (các ô khác đã dùng `rowLabel`) | Gap 2 | Dùng `rowLabel` có sẵn |
| F-N3 | Thấp | enhancement | `procurement/containers/new-receipt-dialog.tsx` (`closeWarning` chỉ khi ghi chưa xong) | Esc hoặc bấm ra ngoài khi form đã có dòng thì mất dữ liệu mà không hỏi | WCAG 3.3.4 (khuyến nghị); 3.3.7 không áp (không phải bước sau) | Xác nhận "Bỏ phiếu đang nhập?" khi form dirty. Đổi hành vi nên chủ sản phẩm quyết |

### 2.7 Kho

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-I1 | Trung bình | defect | `inventory/components/count-drawer.tsx:102,120` | Nhãn `Số lô – ${sku_name}` / `Thực đếm – ${sku_name}` không có màu, biến thể hay lô. Cùng SKU khác màu hoặc lô sẽ ra nhãn trùng nhau | Gap 2; WCAG 2.4.6 | Thêm màu và lô: `Thực đếm – ${sku_name} · ${color} · lô ${lot}` |
| F-I2 | Trung bình | defect | `inventory/components/count-drawer.tsx:185` (quét mã) | Quét thành công không có phản hồi cho SR (chỉ "không tìm thấy" mới ra toast); người quét không nhìn màn hình không biết dòng nào đã cộng | WCAG 4.1.3 | Vùng `role=status` luôn có trong DOM: "Đã cộng 1 – {tên hàng}, thực đếm {n}" |
| F-I3 | Thấp | defect | `inventory/components/count-drawer.tsx:178` | Trạng thái đang tải là `<p className="hint">` không có `role=status` | WCAG 4.1.3 | Dùng `ListLoading` (`shared/ui/list-footer.tsx:18-24`) |
| F-I4 | Thấp | defect | `inventory/components/stock-table.tsx:168,181` ("Sửa giá", ngữ cảnh chỉ nằm trong `title`); `:173,186` ("Thẻ kho" lặp) | Tên truy cập lặp nhau. `title` không được dùng làm tên khi đã có text | WCAG 2.4.6, 2.4.4 | `aria-label={\`Sửa giá – ${name}\`}`, `Thẻ kho – ${name}` |
| F-I5 | Thấp | defect | `inventory/containers/color-price-dialog.tsx:121` | Hint giải thích giá nào được áp không gắn `aria-describedby` vào ô `readOnly`, nên SR focus vào ô chỉ nghe "chỉ đọc" mà không biết vì sao | Gap 11 ("readOnly + explanation"); WCAG 1.3.1 | `id` cho hint + `aria-describedby` trên hai ô `:109,115` |
| F-I6 | Thấp | defect | `inventory/containers/adjustment-dialog.tsx:55-59` | Lỗi kiểm tra chỉ báo bằng toast | WCAG 3.3.1 (đạt), 3.3.3 khuyến nghị | Lỗi inline bằng `Field error` như `reason-field.tsx` |

### 2.8 Giỏ quà

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-K1 | Trung bình | defect | `kits/components/recipe-editor.tsx:76` (`ColorSwatchGroup` không label nên mặc định "Màu sắc"), `:80` "Số lượng thành phần", `:100` "Xóa" | Thiếu ngữ cảnh dòng. Combobox `:67` đã đúng (`Thành phần ${index+1}`) | Gap 2 | `Màu – thành phần ${index+1}`, `Số lượng – thành phần ${index+1}` (hoặc tên hàng đã chọn) |
| F-K2 | Trung bình | defect | `kits/components/mix-line-row.tsx:20` (Callout vượt cap không role); `:83,100,101,115` (nhãn không ngữ cảnh); `:125-126` (`Field` label + `QtyInput aria-label` đè lên nhãn hiển thị) | Không thông báo vượt cap; nhãn trùng giữa các dòng; tên truy cập khác nhãn hiển thị | WCAG 4.1.3, 2.4.6, 2.5.3 | Như F-B4 / F-B2; bỏ `aria-label` khi đã có `Field` |
| F-K3 | Thấp | defect | `kits/containers/kit-dialog.tsx:155,164` | Select "Loại" và ô "Mã" `disabled` khi không sửa được: không focus được, và hint giải thích (`:183`) chỉ hiện khi `create.stored` | Gap 11 (cùng mẫu: `readOnly` + giải thích) | `readOnly` (select: `aria-readonly` + chặn đổi) + hint luôn hiện, gắn `aria-describedby` |

### 2.9 Tổng quan — dashboard

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-D1 | Trung bình | defect | `dashboard/pages/dashboard-page.tsx:96-100` | `<p role=status>` "Đang tải số liệu kỳ đã chọn…" nằm **trong** `div aria-busy`. Nhiều SR tạm hoãn đọc subtree đang busy, nên status có thể không được đọc. Opacity 0.55 còn làm giảm tương phản chữ (đo tương phản thuộc nhóm B) | WCAG 4.1.3 | Đưa `role=status` ra ngoài vùng `aria-busy` |
| F-D2 | Thấp | defect | `dashboard/components/debt-band.tsx:46-48` | "Thu tiền"/"Trả tiền" lặp mỗi dòng không kèm tên khách/NCC | WCAG 2.4.6 | `aria-label={\`Thu tiền – ${party_name}\`}` |

Điểm tốt: `DeltaIndicator` có glyph `aria-hidden` + sr-only "Tăng/Giảm"; `KpiCard` mang nghĩa bằng text, không chỉ bằng màu; bảng và biểu đồ ở `cash-band.tsx:73,77` có caption; lỗi dùng `role=alert` (`dashboard-page.tsx:91`).

### 2.10 Tổng quan — thông báo

| ID | Mức | Loại | File:dòng | Vấn đề | Bằng chứng | Sửa |
|---|---|---|---|---|---|---|
| F-T1 | Thấp | defect | `notifications/pages/notifications-page.tsx:75-92` | "Mở", "Tải về", "Nhắc lại mai", "Đã xử lý" lặp mỗi dòng không ngữ cảnh. Khi `command.pending` thì **mọi** dòng bị `disabled` và không có nhãn chờ; dòng vừa xử lý biến mất nên focus rơi về `body` | WCAG 2.4.6, 2.4.3, 4.1.3 | `aria-label` kèm `n.title`; `pending`/`pendingLabel` chỉ cho dòng đang chạy; fallback focus (F-S6) |
| F-T2 | Thấp | defect | `notifications/pages/notifications-page.tsx:66` | Cột thao tác `header: ""` | axe `empty-table-header` | Gộp F-S14 |

## 3. Unit test nên bổ sung

1. `pos/components/cart-line-row.test.tsx`: `getByLabelText("Số lượng – {tên}")`, `"Chiết khấu % – {tên}"`, `"Đơn vị – {tên}"`, `"Màu khách lấy – {tên}"` (gap 2).
2. `pos/components/shift-fallback-callout.test.tsx` (hoặc trong `pos-page.test`): MSW trả `shift_unavailable` thì có `role=alert` "Chưa có ca bán hàng đang mở." (gap 16).
3. `sales/components/line-items/item-line-row.test.tsx` và `mix-pack-row.test.tsx`: nhãn mọi ô có tên hàng hoặc "dòng {n}"; hai dòng không trùng tên truy cập (gap 2).
4. `kits/components/recipe-editor.test.tsx`: nhãn số lượng và màu theo thành phần (gap 2).
5. `shared/ui/data-table/data-table.test.tsx`: caption sr-only luôn render; `th scope=col`; header rỗng được thay bằng sr-only (gap 12, F-S14).
6. Caption của bảng thô: `cart-panel`, `line-items-editor`, `buy-preset` có `getByRole("table", { name })` (gap 12).
7. `shared/ui/status-pill.test.tsx`: glyph `aria-hidden`, tên chỉ là nhãn (gap 8).
8. `shared/ui/date-picker.test.tsx`: gõ dd/mm/yyyy, ngày sai báo lỗi, phím mũi tên/PageUp/Home trong lưới, Esc trả focus (gap 13).
9. `shared/ui/color/stock-color-picker.test.tsx`: phím mũi tên roving, label tùy biến (gap 10).
10. `shared/ui/field.test.tsx`: tên truy cập chỉ là nhãn; hint và lỗi qua `aria-describedby`; `aria-invalid` (F-S7).
11. `shared/hooks/use-return-focus.test.tsx`: opener bị gỡ thì focus về fallback, không về `body` (gap 5, F-S6).
12. `app/layouts/sidebar.test.tsx`: ≤860px khi đóng `nav` có `inert`; mở thì focus vào trong, đóng thì về nút menu (F-S2).
13. `app/layouts/app-shell.test.tsx`: `document.title` theo `handle.title` của route (F-S3).
14. `shared/ui/toaster.test.tsx`: toast lỗi nằm trong vùng `role=alert`, toast ok trong `role=status`; không lồng live region (F-S8, gap 6).
15. Live region có điều kiện: `offline-callouts.test.tsx` và `pos-cart-container.test.tsx` assert wrapper `role=status` đã có trong DOM **trước** khi chuyển offline hoặc vào hàng đợi, rồi chỉ đổi text (F-O1).
16. `inventory/components/count-drawer.test.tsx`: quét thành công cập nhật vùng `role=status`; hai dòng cùng SKU khác màu có nhãn khác nhau (F-I1, F-I2).
17. `sales/containers/invoice-dialog.test.tsx`: cảnh báo vượt hạn mức nằm trong vùng live (F-B3).
18. e2e (khi có stack): `focus.spec.ts` (gap 5), `touch-targets.spec.ts` (gap 18), và sửa `pos.spec.ts:185` chọn khách bằng bàn phím thay cho `pickOption` click (gap 1).

## 4. Không thể kiểm bằng đọc code

- **Thiết bị thật và cảm ứng:** kích thước vùng chạm thực tế (hit area), cuộn trên iOS Safari/Android Chrome, bàn phím ảo che field (2.4.11). Cần `touch-targets.spec.ts` và chạy máy thật.
- **Trình đọc màn hình:** NVDA/JAWS + Chrome/Firefox, VoiceOver iOS/macOS. Cần xác nhận cách đọc của live region chèn có điều kiện (F-O1), live region lồng nhau (F-S8) và `aria-busy` (F-D1), cũng như thứ tự đọc của combobox.
- **axe-core runtime** trên mọi route, dialog, drawer (NFR-11), kể cả kiểm tra tùy biến `table-caption`.
- **Pixel parity với prototype** (`e2e/visual/parity.spec.ts`, ngưỡng ở phase 20 L78-81).
- **Máy quét HID** (độ trễ Enter, quét liên tiếp vào `scan-input.tsx`, `count-drawer.tsx:185`).
- **Máy in** A5, cuộn 80mm và A4: `@page a5`/`roll` và thuộc tính `page:` (`sales/print.css:3-4`) phụ thuộc trình duyệt. Cần in thử thật.
- **Tương phản màu** (nhóm B), kể cả vùng opacity 0.55 ở F-D1.
- **Hiệu năng:** phản hồi dưới 100 ms, CLS, offline thật với service worker (DevTools / Lighthouse).
- Các bước runtime của skill enhance-ux-ax (chụp màn hình, axe, SR) **chưa chạy** do ràng buộc không chạy dev server hay e2e.

## Ghi chú tích cực (3.3.7, kiểm có bằng chứng)

- `procurement/containers/new-receipt-dialog.tsx:113-131`: tạo nhanh NCC xong thì điền ngược vào form, không phải nhập lại.
- `kits/containers/kit-dialog.tsx:183`: lưu một phần xong vẫn giữ bản nháp.
- Các form nhiều bước trong nhóm A không yêu cầu nhập lại dữ liệu đã nhập.

## Câu hỏi còn mở

1. Gap 16: chủ sản phẩm xác nhận giữ ca ngầm định (CASH-06) và chỉ dùng callout fallback?
2. F-S5: tách chevron mở rộng khỏi link nhóm, hay bỏ hành vi accordion?
3. F-N3: có thêm xác nhận khi đóng phiếu nhập đang dở không?
4. F-S1: 44px áp theo `max-width: 860px` (đúng chữ của gap 18) hay chỉ theo `pointer: coarse` như hiện tại?

Status: DONE_WITH_CONCERNS
Summary: Đã rà toàn bộ module nhóm A bằng đọc code. 9/14 dòng gap đã đóng; gap 2, 4, 12, 18 đóng một phần; gap 5 thiếu fallback focus và `focus.spec`. Ghi 55 phát hiện (3 Cao, 26 Trung bình, 26 Thấp) kèm file:dòng và cách sửa, cùng 18 test nên bổ sung.
Concerns/Blockers: Không chạy được axe, SR, thiết bị thật, e2e nên các phát hiện về live region (F-O1, F-S8, F-D1) cần xác minh bằng SR. Gap 16 và F-S5 chờ chủ sản phẩm quyết. Chưa có `focus.spec.ts` và `touch-targets.spec.ts` mà matrix yêu cầu.
