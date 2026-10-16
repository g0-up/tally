# Prototype v6.14 UI spec (React rebuild reference)

Source: `docs/design/prototype-v6.14.html` (4073 lines, single file, vanilla JS). The line numbers below point into that file.
Authority: `MENU` (line 1719) defines the live screens. When a view or helper is defined more than once, the **last definition wins**, and only that version is specified here. Every Vietnamese UI string is quoted verbatim.
Cross-reference: PRD §9.1 (menu), §9.2 (screen patterns), §9.3 (UX rules).

**Global column rule.** `table()` drops any column whose header is `Kho`, `Kho · phạm vi` or `Chi nhánh` when `oneStore()` is true (≤1 warehouse and ≤1 branch). The demo is one-store, so those columns are left out of every table below. The same applies to "Kho nhận" / "Kho đóng" form fields, which are hidden in one-store mode. The React build should keep one `oneStore` flag that drives all of these.

---

## 1. Tokens (exact CSS values, lines 9–25)

### 1.1 Color

| Token | Value | Use |
|---|---|---|
| `--navy-900` | `#0a0d53` | h1, `.doc`, open nav group bg, nav-sub bg, chip pressed |
| `--navy-700` | `#233c65` | sidebar bg, card h2, `.lnk`, focus ring, toast bg, btn-navy |
| `--navy-deep` | `#001a2d` | scrim base (rgba(0,26,45,…)) |
| `--accent-orange` | `#f45d29` | gradient start, series-2 |
| `--accent-crimson` | `#f2295b` | gradient end, toast err bar, preview price |
| `--on-accent` | `#eaeaea` | text on gradient |
| `--slate-600` | `#4b5863` | secondary text |
| `--gray-800` | `#333333` | strong text |
| `--gray-600` | `#555555` | body text |
| `--gray-500` | `#777777` | hints, eyebrow |
| `--gray-300` | `#c4c4c4` | input border, hatch |
| `--gray-200` | `#d8d8d8` | table row border, `.flat` border |
| `--surface-0` | `#fff` | cards, modal |
| `--surface-50` | `#f5f5f5` | util bar, tfoot |
| `--surface-100` | `#f2f2f2` | page bg |
| `--blue-50` | `#e8edf7` | th bg, btn-line hover, `.forecast` |
| `--blue-100` | `#d8e2ec` | sidebar tagline and sub-links |
| `--surface-dark` | `#23282d` | dark surface |
| `--series-1` | `#2f5da8` | chart series 1 |
| `--series-2` | `#f45d29` | chart series 2 |
| Row hover | `#fafbfd` | `tbody tr:hover` |

Status triplets (ink / bg / line):

| Status | Ink | Bg | Line | Glyph in `.st` |
|---|---|---|---|---|
| ok | `#1d6a3a` | `#e7f3eb` | `#b6dbc3` | ✓ |
| warn | `#7a5200` | `#fdf3dc` | `#efd28c` | ◷ |
| bad | `#a01431` | `#fde9ed` | `#f1b5c2` | ! |
| mute | gray | surface | gray | – |
| default (info) | navy on blue-50 | | | • |

Product colors (`COLORS`):

| Key | Hex | Label | Note |
|---|---|---|---|
| xanh | `#1f6fd1` | Xanh | |
| do | `#d62828` | Đỏ | |
| tim | `#7b3fbf` | Tím | |
| vang | `#f2c200` | Vàng | |
| nau | `#8b5a2b` | Nâu | |
| den | `#1f1f1f` | Đen | |
| trang | `#ffffff` | Trắng | gets a light border so it stays visible |
| none | hatch | Chưa gán màu | `repeating-linear-gradient(45deg, var(--gray-300) 0 2px, var(--surface-0) 2px 4px)` |

### 1.2 Effects, spacing, radius

| Token | Value |
|---|---|
| `--shadow-card` | `0 0 7px 0 rgba(0,0,0,.15)` |
| `--shadow-card-strong` | `0 0 7px 0 rgba(0,0,0,.2)` (hover on `.kpi.click`) |
| `--shadow-float` | `0 5px 15px 0 rgba(0,0,0,.15)` (drawer, mobile sidebar, combobox popup) |
| `--grad-accent` | `linear-gradient(260deg,#f45d29 0%,#f2295b 100%)` |
| `--sp-1 … --sp-7` | `2px, 6px, 10px, 18px, 24px, 35px, 60px` |
| `--r-xs` | `2px` (inputs) |
| `--r-sm` | `3px` |
| `--r-pill` | `50px` (buttons, chips, `.st`) |
| Modal scrim | `rgba(0,26,45,.45)` |
| Drawer scrim | `rgba(0,26,45,.25)` |

### 1.3 Typography

| Token | Value |
|---|---|
| `--font-sans` | `"Inter Tight",system-ui,sans-serif` (body) |
| `--font-display` | `Manrope` (brand, h1, modal h3, preview h4) |
| `--font-ui` | `Roboto` (nav groups, tabs) |
| Google Fonts | Inter Tight 400/500/600/700; Manrope 600/700; Roboto 400/700; `subset=vietnamese` |
| Body | 14px / 1.55, letter-spacing .2px, color gray-600 |
| h1 (`.ph h1`) | Manrope 30px / 1.15, 700, letter-spacing -.5px, navy-900 (25px at ≤860) |
| Card h2 | 18px, 600, navy-700 |
| Modal h3 | Manrope 22px |
| Eyebrow | 12px uppercase, letter-spacing .8px |
| Table th | 12px uppercase, 600 |
| KPI value | 25px, 600 (22px on Thẻ kho) |
| Cart total | 30px, 600 |
| Number cells | `.num`: right-aligned, `font-variant-numeric: tabular-nums`, nowrap; `.tab-n` for inline numbers |

### 1.4 Breakpoints and motion

| Rule | Behavior |
|---|---|
| ≤1100px | `.g4` becomes 2 columns; `.g-main` and `.pos` become 1 column |
| ≤860px | `.app` becomes 1 column. `.side` turns fixed, 280px wide, `translateX(-100%)`, `transition .2s`, z 45. `.side.open` sets transform none and adds shadow-float. `.menu-btn` is shown. Util padding 8px 16px; main padding 20px 16px 60px; `.g2`/`.g3` become 1 column; h1 25px |
| ≤520px | `.g4` becomes 1 column; `.hide-s` is hidden; `.btn` padding 12px 18px |
| `prefers-reduced-motion` | all transitions removed |
| `:focus-visible` | `outline: 2px solid var(--navy-700); outline-offset: 2px` |
| Button transition | .2s |
| `post()` latency | 380ms simulated |
| Toast lifetime | 4500ms (7000ms for `err`) |

---

## 2. App shell (lines 36–72, 324–337, 1742–1787)

| Region | Spec |
|---|---|
| `.app` | CSS grid `264px 1fr` |
| Sidebar `.side` | navy-700 bg, `position: sticky`, height 100vh, scrolls on its own |
| Brand | "Tết Sales" (Manrope 22px, white), tagline "Bán sỉ · giỏ quà · kho · công nợ" (12px, blue-100) |
| Nav group `.nav-g` (button) | Roboto 13px/700 uppercase, letter-spacing .6px, padding 10px 22px; hover `rgba(255,255,255,.06)`; `aria-expanded=true` gives navy-900 bg; optional `.cnt` gradient pill counter |
| Nav sub `.nav-sub a` | Container navy-900; link padding `6px 22px 6px 36px`, 14px, blue-100; `aria-current="page"` gives white text, 600, bg `rgba(255,255,255,.08)` |
| Nav behavior | Single-open accordion. Clicking a group navigates to its first sub and closes the mobile sidebar |
| Sidebar footer | "Bản mẫu tương tác theo PRD 1.0. Dữ liệu demo chỉ lưu trên trình duyệt này." |
| Util bar | surface-50, sticky top, z 20, padding 8px 28px, 13px |
| Main | padding 26px 28px 60px, max-width 1480px |

Util bar items, left to right:

| # | Item | Behavior |
|---|---|---|
| 1 | "☰ Menu" (`.menu-btn`) | Shown only at ≤860; toggles `.side.open` |
| 2 | Sandbox pill "Cửa hàng mẫu · dữ liệu demo" | Static |
| 3 | Tenant "Đại lý Bánh kẹo Tết Hòa Phát" | `.hide-s` |
| 4 | Branch select | Rendered only when there is more than one branch (never in the demo seed) |
| 5 | "Ngày nghiệp vụ dd/mm/yyyy" | `S.meta.date` |
| 6 | Spacer | flex 1 |
| 7 | "Chủ cửa hàng · Trần Thị Hòa" | Role label |
| 8 | "Việc cần xử lý **n**" | Navigates to `#tong-quan.thong-bao` (`goNotif`, line 1885) |
| 9 | "Đặt lại mẫu" | Opens the reset modal "Đặt lại dữ liệu mẫu?" (line 1887), which reseeds localStorage |

Page header `pageHead(eyebrow, title, lead, acts)`:

| Part | Spec |
|---|---|
| `.ph` | flex, `align-items: flex-end`, gap 14px 24px, margin-bottom 18px, wraps |
| Eyebrow | the MENU group name (e.g. "Bán hàng") |
| h1 | screen title |
| Lead | paragraph, max-width 72ch |
| Acts | right-side button row |

Tabs (`.tabs`): Roboto 13px/700 uppercase, padding 10px 14px, border-bottom 3px (active uses the accent color).

Router (lines 1742–1787, MOVED at 3471):

| Case | Behavior |
|---|---|
| Hash format | `#<mod>.<sub>` |
| Unknown sub | falls back to the first sub of the module |
| Missing view | renders "Đang xây dựng." |
| Render throws | callout bad "Không hiển thị được màn hình: <message>" |
| `hashchange` | closes drawer and modal, re-renders, scrolls to top |
| Redirects (`MOVED`) | `#thiet-lap.nha-cung-cap` to `#danh-muc.nha-cung-cap`; `#danh-muc.doi-tac` to `#chien-dich.khach-hang`; `#thiet-lap.tich-hop` and `#thiet-lap.du-lieu` to `#thiet-lap.tai-khoan`; `#kho.mau` to `#kho.ton`; `#tien.quy`, `#tien.thu-chi`, `#tien.chuyen-tien`, `#tien.sao-ke`, `#tien.ca`, `#tien.chi-phi` to `#bao-cao.dong-tien` |

Global keys: `Esc` closes the modal first, then the drawer, then the mobile sidebar. POS: `F2` focuses scan, `F4` holds the cart, `F9` pays.

Write pipeline `post(btn, fn, okMsg, after)` (the commit contract every submit uses):

| Step | Behavior |
|---|---|
| 1 | Disables the button and sets its label to "Đang ghi sổ…" |
| 2 | Waits 380ms, snapshots `S`, runs `fn()`, then `save()` |
| 3 | On success: toast `okMsg` (a string, or a function of the result). Closes the modal unless `after === false`; if `after` is a function, calls it with the result (e.g. reopening a drawer) |
| 4 | On throw: restores the snapshot, then shows err toast "<b>Chưa ghi sổ.</b> {message}" |

React: implement this as `useCommit()` returning `{ run, pending }`. Per PRD §9.3, show success only after the server confirms.

---

## 3. Shared component catalogue

| Prototype | Proposed React / shadcn | Props | Exact visual spec |
|---|---|---|---|
| `.btn` + variants, `btn(label, act, id, cls)` | `Button` (shadcn) with `variant: primary\|navy\|line\|danger`, `size: md\|sm` | `variant, size, loading, disabled, onClick` | inline-flex, gap 8, 13px/500, letter-spacing 1.3px, uppercase, padding 14px 26px, radius pill. primary = gradient, on-accent text, hover `filter: brightness(1.06)`. navy = navy-700 bg, hover navy-900. line = `box-shadow: inset 0 0 0 1px navy-700`, hover blue-50. danger = bad-ink text + inset bad-line. sm = 11px, padding 9px 14px (5px 10px inside `.acts`). disabled = opacity .55. Default helper class is `btn-line btn-sm` |
| `.lnk`, `lnk()` | `LinkButton` | `onClick, children` | navy-700, 600, underline on hover; a `<button>` styled as a link |
| `.card`, `.card-h` | `Card`, `CardHeader` | `title, actions` | bg white, shadow-card, padding 24; h2 18/600 navy-700; header is space-between |
| `.grid .g2/.g3/.g4/.g-main`, `.stack` | `Grid cols={2\|3\|4\|"main"}`, `Stack` | `cols, gap` | gap 20; `.g-main` = `1.65fr 1fr` |
| `.band` | `Band` | `title, children` | section band grouping KPIs (Dashboard) |
| `.kpi` | `KpiCard` | `label, value, sub, onClick?, forecast?, locked?` | padding 18px 20px; `.k` 12px uppercase; `.v` 25/600; `.s` 13px. `.click` hover = shadow-card-strong. `.forecast` = blue-50 bg + dashed border. `.lock` shows "Ẩn theo quyền" |
| `pageHead()` | `PageHeader` | `eyebrow, title, lead, actions` | see section 2 |
| `table(cols, rows, opts)` | `DataTable` (shadcn Table + TanStack) | `columns[{header, cell, numeric, hideWhenOneStore}], rows, footer, empty, rowKey, subRows` | th blue-50, 12px uppercase 600, padding 10px 12px; td border-top gray-200; row hover `#fafbfd`; tfoot 600 on surface-50; `.empty` padding 28, default text "Chưa có dữ liệu."; wrapper `.tw` scrolls horizontally; `.flat` = gray-200 border, no shadow |
| `.sub` | `CellSub` | children | 12px block under the main value |
| `.doc` | `DocNo` | `no, onOpen` | 600 navy-900; opens the drawer mapped by `docLink` |
| `.subrow`, `.tgl`, `.tgl-sp`, `.tname` | `ExpandableRow` | `expanded, onToggle, label` | toggle button 24×24, glyph ▸/▾, `aria-expanded`, `aria-label` "Xem chi tiết màu của X" / "Thu gọn màu của X"; `.tgl-sp` same-width spacer; subrow td padding-left 32, dashed top border; `.tname` min-width 340 |
| `.filters` | `FilterBar` | children | flex wrap, gap 10; search input width 280 |
| `.chip`, `chips(key, opts)` | `ToggleGroup` (shadcn, single) | `value, options[{value,label}], onChange` | pill; `aria-pressed=true` gives navy-900 bg and white text |
| `.st`, `st(text)` | `StatusPill` | `status` (text). Tone is derived from the prefix lists | 12px/18px, padding 1px 8px, pill, tone colors from §1.1 plus a glyph. ST_OK / ST_WARN / ST_BAD prefix lists at lines 1689–1691; later additions to ST_OK are "Đang dùng" and "Đã xuất kho". Mute list: Nháp, Đã hủy, Đã đảo, Đóng – thiếu, Chưa phát sinh, Đã khóa, Đã kết thúc, Chưa giao, Chưa kết nối, Ngừng |
| `.tag`, `.tag.alt` | `Badge` | `variant: default\|alt` | small label tag |
| `.fg`, `F(label, html, hint)` | `FormGrid`, `Field` (shadcn Form) | `label, hint, error, children` | grid `repeat(auto-fit,minmax(190px,1fr))`, gap 14; label span 12px uppercase; `.hint` 12px |
| `inp(path, val, type, attrs)`, `.in` | `Input` | `name, value, type, size: sm\|md` | padding 9px 11px, 1px gray-300 border, radius 2; `.in.sm` 96px right-aligned; `.in.md` 140px |
| `.check` | `Checkbox` + label | `checked, label, disabled` | inline label |
| `.callout` | `Callout` (shadcn Alert) | `tone: info\|warn\|bad\|ok` | padding 12px 14px, 13px |
| `impact([[k,v]])`, `.impact`, `.paybox` | `ImpactSummary` | `items[{label, value}], variant: default\|paybox` | grid `auto-fit minmax(170px,1fr)`; label `b` 11px uppercase; paybox enlarges the 3rd value to 18px. Used before every commit ("preview tác động trước ghi sổ") |
| `kv()`, `.kv` | `KeyValueGrid` | `items` | grid auto-fit 160px |
| `.meter`, `.acc` | `Progress` | `value, max, width` | 8px track, gradient fill |
| `.steps` (.on/.done) | `Stepper` | `steps, current` | horizontal steps |
| `.tl` | `Timeline` | `items[{time, text}]` | 92px time column |
| `.todo` | `TodoList` | `items` | checklist |
| `.matrix` | `MatrixTable` | | dense grid table |
| `.custbar` | `CustomerBar` | `customer, onChange` | POS customer strip |
| `.mixgrid` | `MixFlavorGrid` | `flavors[{sku, label, avail, qty}]` | grid auto-fill 150px |
| `.preview-card` | `KitPreviewCard` | `kit` | width 420; h4 Manrope 24; price 26px crimson; `.pv-save` savings line |
| `.periodbar` | `PeriodBar` | `period, date, onPrev, onNext, onToday` | segmented period control plus nav |
| `.dl` | `DeltaIndicator` | `dir: up\|down\|flat` | ▲ ▼ ■ |
| `.catlist` | `CategoryList` | `items` | label/value list |
| `barChart` (W640 H230), `groupedBars` (W680 H240), `.tip`, `.legend` | `BarChart`, `GroupedBarChart` (Recharts) | `data, series, valueFormat` | SVG; hover tooltip `.tip`; legend; axis labels "tr"/"k"; always followed by `<details>` "Xem dạng bảng" holding a table fallback |
| `.sw`, `pickColor` | `ColorSwatchGroup` (RadioGroup) | `value, options, onChange, label="Màu"` | swatch 22×22, `role=radio`, `aria-checked` gives a double ring; fallback text "Chưa chọn" |
| `colorChip`, `.sw-chip` | `ColorChip` | `color` | 12px square; hatch for none |
| `.cpill/.cdot` | `ColorQtyPill` | `color, qty` | 10px dot + qty; used inline in stock rows |
| `stockColorPick`, `.cpick/.cp` | `StockColorPicker` (RadioGroup) | `sku, wh, value, onChange, label="Màu khách lấy"` | `role=radiogroup`; each option shows color name + available qty; disabled when qty ≤ 0; `.cp-need` hint "Chọn màu" or "Màu này đã hết" |
| `combo(path, val, kind, pool, {label, page})`, `.cbx` | `Combobox` (shadcn Command + Popover) | `value, onChange, pool: sell\|buy\|stock\|kit\|party, placeholder, label` | Popup fixed, z 70, max-height 320, min-width 300, flips above near the viewport bottom. Accent-insensitive match, max 50 results, hint "Gõ thêm để thu hẹp kết quả…", empty "Không tìm thấy “x”.". Keys: ↑/↓, Enter, Esc; Tab auto-picks a single match. Product options show color dots (`cbOptHtml`, line 3781). Placeholders: party "Gõ tên hoặc số điện thoại để tìm…"; product "Gõ mã, tên hoặc mã vạch để tìm…" |
| `lineEditor`, `buyEditor`, `quoteEditor`, `compEditor` | `LineItemsEditor` with column presets | `lines, onChange, preset: sell\|buy\|quote\|kit, priceResolver` | table of editable lines plus a "+ Thêm …" button; color picker row under the name when the SKU has colors |
| `openModal(opts)`, `.ovl/.modal` | `Dialog` (shadcn) wrapped as `CommitDialog` | `title, sub, wide, defaultValues, body, live, onChange, extra, submitLabel="Ghi sổ", onSubmit` | scrim `rgba(0,26,45,.45)`, z 50, padding 5vh 16px; width 760 (`wide` 1040); header padding 20/24/14, h3 Manrope 22, close "×"; body padding 20px 24px, gap 18; footer buttons "Hủy" (line), extra, submit (primary). Overlay click closes. `role=dialog`, `aria-modal`, `aria-labelledby=m-title`; autofocuses the first input. `live` re-renders only the `.m-live` region; `data-rr` fields trigger a full body re-render plus `onChange` |
| `openDrawer(fn, id)`, `.drw` | `Sheet` (shadcn, side right) as `DetailDrawer` | `title, sub, children` | width `min(820px,100%)`, z 40; scrim `rgba(0,26,45,.25)` z 39; sticky header with title, sub, ×; body padding `0 24px 40px`, gap 20; keeps its scroll position across re-renders |
| `toast(msg, kind)` | `Toaster` (sonner) | `message, tone: default\|err` | fixed bottom-right 20px, z 60, max-width `min(440px, 100% - 32px)`, `aria-live=polite`. Default: navy-700 bg, white, padding 12px 16px. err: white bg, bad-ink text, 4px crimson inset left bar, `role=alert` |
| `.pos`, `.scan`, `.tiles`, `.tdots`, `.cart-total`, `kbd` | `PosLayout`, `ScanInput`, `ProductTile` | | `.pos` grid `.85fr minmax(320px,1.3fr)`; `.scan` 16px input with a 2px navy border; `.tiles` auto-fill 150px |
| `repFilter` | `ReportFilter` | `from, to, onExport` | Từ / Đến dates, hint, "Xuất CSV" |
| `debtTable` | `DebtAgingTable` | `side: AR\|AP, rows` | see Công nợ |
| `priceLogTable` | `PriceHistoryTable` | `rows` | see Danh sách hàng hóa |

---

## 4. Route map (MENU order; line = last definition)

| Hash | Vietnamese title (menu / h1) | Proposed path | View line |
|---|---|---|---|
| `#tong-quan.dashboard` | Bảng điều khiển / "Thu, chi và công nợ" | `/overview/dashboard` | 2528 (overrides 2023) |
| `#tong-quan.thong-bao` | Thông báo & việc cần làm | `/overview/notifications` | 2110 |
| `#ban-hang.pos` | POS & giỏ hàng | `/sales/pos` | 3834 (wraps `_posView`, 2216) |
| `#ban-hang.bao-gia` | Báo giá | `/sales/quotes` | 2444 |
| `#ban-hang.don-ban` | Hóa đơn | `/sales/invoices` | 2323 |
| `#ban-hang.tra-hang` | Trả hàng | `/sales/returns` | 4058 (wraps 2479) |
| `#mua-hang.po` | Đơn nhập | `/purchasing/orders` | 2701 |
| `#mua-hang.hang-hoa` | Danh sách hàng hóa | `/purchasing/products` | 2733 |
| `#mua-hang.nhan-hang` | Nhận hàng | `/purchasing/receipts` | 2756 |
| `#mua-hang.tra-ncc` | Trả NCC / "Trả nhà cung cấp" | `/purchasing/supplier-returns` | 2774 |
| `#kho.ton` | Tồn kho | `/inventory/stock` | 3910 (overrides 2784) |
| `#kho.giu-hang` | Giữ hàng | `/inventory/reservations` | 2804 |
| `#kho.kiem-ke` | Kiểm kê | `/inventory/counts` | 2816 |
| `#kho.lo-han` | Lô & hạn dùng | `/inventory/lots` | 2830 |
| `#kho.dieu-chinh` | Điều chỉnh / "Điều chỉnh kho" | `/inventory/adjustments` | 2840 |
| `#kho.the-kho` | Thẻ kho | `/inventory/stock-card` | 3671 (overrides 2795) |
| `#gio-qua.cong-thuc` | Giỏ quà & combo | `/gift-baskets/recipes` | 2845 |
| `#gio-qua.lenh-dong` | Lệnh đóng giỏ | `/gift-baskets/assembly-orders` | 2975 |
| `#gio-qua.tien-do` | Tiến độ & truy xuất lô | `/gift-baskets/progress` | 2989 |
| `#gio-qua.thao-gio` | Tháo giỏ | `/gift-baskets/disassembly` | 2997 |
| `#cong-no.khach` | Khách còn nợ | `/debts/receivables` | 3139 |
| `#cong-no.ncc` | Cửa hàng còn nợ NCC | `/debts/payables` | 3141 |
| `#cong-no.ung-truoc` | Ứng trước & cọc | `/debts/advances` | 3170 |
| `#cong-no.doi-soat` | Đối soát | `/debts/statements` | 3179 |
| `#cong-no.lich` | Lịch thu/trả | `/debts/schedule` | 3192 |
| `#chien-dich.mua` | Chiến dịch mùa vụ | `/campaigns/seasons` | 3246 |
| `#chien-dich.khach-hang` | Khách hàng (CRM) | `/campaigns/customers` | 3253 |
| `#bao-cao.nxt` | Nhập–xuất–tồn | `/reports/stock-movement` | 3264 |
| `#bao-cao.doanh-thu` | Bán hàng | `/reports/sales` | 3273 |
| `#bao-cao.lai-gop` | Lãi gộp / "Lãi gộp & kết quả quản trị" | `/reports/gross-profit` | 3288 |
| `#bao-cao.tuoi-no` | Tuổi nợ | `/reports/debt-aging` | 3301 |
| `#bao-cao.dong-tien` | Dòng tiền | `/reports/cash-flow` | 3310 |
| `#bao-cao.gio` | Giỏ quà | `/reports/gift-baskets` | 3321 |
| `#danh-muc.hang-hoa` | Hàng hóa | `/catalog/products` | 3337 |
| `#danh-muc.nha-cung-cap` | Nhà cung cấp | `/catalog/suppliers` | 3425 |
| `#danh-muc.bang-gia` | Bảng giá | `/catalog/price-lists` | 3353 |
| `#danh-muc.nhom-mix` | Nhóm mix vị | `/catalog/mix-groups` | 3574 |
| `#thiet-lap.tai-khoan` | Tài khoản & bảo mật | `/settings/account` | 3358 |
| `#thiet-lap.thue-bao` | Thuê bao | `/settings/subscription` | 3379 |
| `#thiet-lap.nhat-ky` | Nhật ký | `/settings/audit-log` | 3385 |

Group paths: Tổng quan `/overview`, Bán hàng `/sales`, Nhập hàng `/purchasing`, Kho `/inventory`, Giỏ quà `/gift-baskets`, Công nợ `/debts`, Chiến dịch & khách `/campaigns`, Báo cáo `/reports`, Danh mục `/catalog`, Thiết lập `/settings`. A group path should redirect to its first sub. Keep a legacy-hash redirect table that mirrors `MOVED`.

---

## 5. Per-screen spec

Conventions: "Acts" are page-header buttons. "Cols" are table columns in order. Row-action columns have no header. A modal named in a row or drawer is specified once, where it first appears. Each modal's submit goes through `post()`, so its button shows "Đang ghi sổ…".

### 5.1 Tổng quan

#### 5.1.1 Bảng điều khiển (2528)

| Element | Spec |
|---|---|
| Eyebrow / h1 | "Tổng quan" / "Thu, chi và công nợ" |
| Acts | "Thu tiền khách" (opens payModal for KH002), "Trả tiền NCC" (opens payModal for NCC001) |
| Periodbar | Chips Ngày / 7 ngày / Tháng / Năm. Date field label depends on period: "Ngày", "Đến ngày", "Trong tháng có ngày", "Trong năm có ngày". Buttons "‹ Kỳ trước", "Kỳ sau ›", "Về hôm nay" |
| Band "Thu – chi" | KPIs (click goes to `#bao-cao.dong-tien`): Tổng thu, Tổng chi, Chênh lệch thu – chi, Tiền trong quỹ |
| Day period | Table Cols: Chứng từ / Loại / Đối tượng / Quỹ / Thu / Chi |
| Other periods | `groupedBars` Thu vs Chi per bucket (series-1 / series-2), plus `<details>` "Xem dạng bảng" |
| Card "Theo loại" | Two `catlist`s (thu by type, chi by type) |
| Band "Công nợ" | KPIs: Khách còn nợ, Nợ khách trong kỳ, Cửa hàng còn nợ NCC, Nợ NCC trong kỳ. Forecast KPIs (`.forecast`): "Sắp phải thu (7 ngày tới)", "Sắp phải trả (7 ngày tới)" |
| Table "Khách nợ nhiều nhất" | Customer name, amount, row action "Thu tiền" (payModal prefilled) |
| Table "Nợ nhà cung cấp" | Supplier name, amount, row action "Trả tiền" (payModal AP) |
| Modals | payModal (see 5.6.1) |

#### 5.1.2 Thông báo & việc cần làm (2110)

| Element | Spec |
|---|---|
| Filter chips (with counts) | Chờ duyệt, Nợ quá hạn, Sắp phải trả NCC, Sắp hết hạn (≤30d), Hết hạn – chặn bán, Dưới tồn tối thiểu |
| Cols | Loại / Nội dung / row action "Mở" (navigates to the source screen or drawer) |
| Empty | "Không có việc cần xử lý." |
| Util badge | the total count feeds "Việc cần xử lý n" |

### 5.2 Bán hàng

#### 5.2.1 POS & giỏ hàng (3834 wrapping 2216)

The wrapper auto-opens shift `openShift('TM-CN1','K1',0)` when none is open. That is why the "Chưa có ca bán hàng đang mở. [Mở ca]" callout effectively never shows. Keep it as a fallback state.

| Element | Spec |
|---|---|
| Lead | "Quét mã vạch hoặc gõ mã rồi nhấn Enter. Phím tắt: F2 tìm hàng · F4 lưu giỏ · F9 thanh toán. Giỏ đã bán thành công bị khóa, chỉ sao chép được." (shortcut keys in `<kbd>`) |
| Acts | Toggle "Mô phỏng mất mạng" / "Kết nối lại mạng" (btn-line) |
| Offline callout (warn) | "**Đang ngoại tuyến.** Dùng bảng giá cache lúc dd/mm/yyyy 07:45. Chỉ bán thu tiền ngay; không bán chịu, không hoàn tiền. Tồn hiển thị có thể chưa chính xác. Giao dịch chờ đồng bộ: **n**." |
| Queue callout (online, queue > 0) | "Có n giao dịch ngoại tuyến chờ đồng bộ." + "Đồng bộ ngay" (btn-navy sm). Result toast "Đồng bộ xong: n giao dịch ghi sổ[, m vào hàng đợi ngoại lệ]. Mỗi giao dịch chỉ ghi một lần." |
| Exception callout (bad) | "**Hàng đợi ngoại lệ (n):** OFF-001: err · … . Giao dịch không bị mất, quản lý cần xử lý." |
| Layout | `.pos` grid; left column (scan + tiles), right column (cart card) |
| Scan | input placeholder "Mã vạch, mã hàng hoặc tên hàng…", aria-label "Tìm hoặc quét hàng"; button "Thêm" (btn-navy). Enter adds the first match; no match gives err toast "Không tìm thấy mã “x”." |
| Tiles (max 30, sellable SKUs matching the query) | `<button class="tile">`: bold name; "price / base unit"; "Có thể bán: N" or "Hết hàng có thể bán" (disabled); combos append " · ghép từ thành phần"; color dots (`tdots`); combos show "● chọn màu thành phần" |
| Cart header | "Giỏ hàng {id}" or "Giỏ hàng mới"; when editing a saved cart, st "Đang soạn" + "Giỏ mới" |
| Custbar | "Khách hàng" **name**, small phone + " · hạn mức nợ X · đang nợ Y"; native `<select>` aria "Đổi khách hàng" (customers only); hint "Nguồn giá: …" |
| Cart Cols | Tên sản phẩm / Giá / CK % (input 52px, aria "Chiết khấu %") / ĐVT (select, aria "Đơn vị") / SL (input 56px, aria "Số lượng") / Thành tiền / "Xóa" link. SL 0 removes the line |
| Color row (under a line) | Plain SKU: hint "Màu khách lấy:" + StockColorPicker + "+ Thêm màu khác" (splits the line). Combo: per-component color rows (`comboColorRows`; a component with one color shows "cố định theo combo") + "+ Thêm combo khác màu". Picking a color that matches another line merges them, with toast "Đã gộp vào dòng X Màu." / "Đã gộp vào dòng X cùng màu." |
| Empty cart | "Giỏ hàng trống. Quét mã hoặc chọn hàng bên trái." |
| Total | label "Khách cần trả" (12px uppercase) + `.cart-total` |
| Payment | "Tiền mặt khách đưa" and "Chuyển khoản / QR" (2-col fg; QR disabled offline with title "QR cần xác nhận từ ngân hàng, không dùng khi ngoại tuyến") |
| Cash chips | Up to 4 unique: total, round up to 50k, round up to 100k, 500k |
| Credit | checkbox "Bán chịu phần còn lại X" shown when owing; disabled offline "(không cho phép khi ngoại tuyến)" or without permission "(vai trò không có quyền)" |
| Change | callout ok "Trả lại khách: **X**" |
| Buttons | "Thanh toán · F9" (primary; disabled without shift or lines); "Lưu giỏ · F4" / "Lưu thay đổi · F4" (line) |
| Pay toast | "Đã ghi sổ **HD-xxxx** · X[ · trả lại Y]. Giỏ GH-xxxx đã khóa." Offline: "Đã lưu OFF-00n trên máy. Sẽ ghi sổ một lần khi có mạng."; offline underpay gives err "**Chưa lưu.** Ngoại tuyến chỉ cho bán thu đủ tiền mặt." |
| Hold toast | "Đã lưu giỏ **GH-xxxx** của {khách}. Giỏ lưu chưa giữ hàng." |
| Section "Giỏ hàng đã lưu" | chips "Đang soạn (n)" (default) / "Đã bán (n)" / "Tất cả" |
| Saved carts Cols | Giỏ (id + sub "dd/mm/yyyy · người[ · sao chép từ X]") / Khách hàng / Hàng (summary) / Tổng tiền / Trạng thái / acts Sửa · Sao chép · Xóa. Editing a sold cart gives err "Giỏ đã bán thành công, không sửa được." |
| Modal "Xóa giỏ hàng X?" | sub "{khách} · n mặt hàng · X. Giỏ chưa bán nên chưa có chứng từ kho hay tiền."; body hint "Thao tác được ghi nhật ký."; submit "Xóa giỏ" |

#### 5.2.2 Báo giá (2444)

| Element | Spec |
|---|---|
| Lead | "Vòng đời: Nháp → Đã gửi → Khách chấp nhận hoặc từ chối → Chuyển thành hóa đơn. Quá ngày hiệu lực tự chuyển “Hết hạn”. Báo giá không giữ tồn và không tạo doanh thu." |
| Acts | "Tạo báo giá" (primary) |
| Chips | "Tất cả (n)" + one per status with count: Nháp, Đã gửi, Khách chấp nhận, Khách từ chối, Hết hạn, Đã chuyển hóa đơn |
| Cols | Số (link opens drawer; sub "vN · dd/mm/yyyy") / Khách hàng / Hiệu lực đến / Giá trị / Trạng thái / qActs |
| qActs by status | Nháp: "Gửi khách" (navy), "Sửa", "Xóa" (danger). Đã gửi: "Khách chấp nhận" (navy), "Khách từ chối" (danger), "Phiên bản mới". Khách chấp nhận: "Chuyển thành hóa đơn" (navy). Khách từ chối: "Phiên bản mới". Hết hạn: "Gia hạn / phiên bản mới", "Khách từ chối". Đã chuyển hóa đơn: doc link to the SO. Every status also has "Sao chép" |
| Drawer "Báo giá X" | sub "{khách} · phiên bản N · hiệu lực đến dd/mm/yyyy"; st; qActs; `quoteLinesView` (foot "Tổng cộng"); impact Lãi gộp dự kiến (X (n%) or "Ẩn theo quyền") / Giữ tồn "Không" / Doanh thu "Chưa phát sinh"; note; "Lịch sử trạng thái & phiên bản" timeline. Deleted state: callout "Báo giá đã bị xóa." |
| quoteModal | Titles "Tạo báo giá" / "Sửa báo giá nháp X" / "Phiên bản mới cho X" (version sub "Hiện tại vN. Phiên bản cũ giữ trong lịch sử."). Fields: Khách hàng (party combobox), Hiệu lực đến (date, default +10 days), Ghi chú (or "Lý do thay đổi (bắt buộc)" in version mode). quoteEditor Cols: Tên sản phẩm / Đơn vị tính / Số lượng / Quy cách / Đơn giá / CK / Thành tiền / Giá bán lẻ. Mix row "Thùng mix vị" with a mix-group select and a mixgrid of "vị · còn N dây" qty inputs. Buttons "+ Thêm sản phẩm", "+ Thêm thùng mix vị". Hint "Đơn giá là giá báo cho khách…". Live impact: Giá trị báo giá / Số dòng / Giữ tồn "Không". Extra (new only) "Lưu nháp" (navy). Submit "Lưu & gửi khách" / "Lưu nháp" / "Lưu & gửi phiên bản mới". Toast "Đã lưu **BG-xxxx**." |
| Modal "Khách từ chối X" | field "Lý do khách từ chối", placeholder "VD: giá cao, đã mua nơi khác"; submit "Ghi nhận" |
| Other toasts | Send "Đã chuyển sang “Đã gửi”."; accept "Đã ghi nhận khách chấp nhận."; delete "Đã xóa báo giá nháp."; copy "Đã sao chép thành **X** (nháp)."; to SO "Đã tạo hóa đơn **X** (nháp) từ báo giá. Xác nhận bán để giữ hàng." and then opens the SO drawer |

#### 5.2.3 Hóa đơn (2323)

| Element | Spec |
|---|---|
| Acts | "Tạo hóa đơn" (primary) |
| Filters | search "Tìm số hóa đơn, khách hàng…"; status chips Tất cả / Nháp / Chưa xuất đủ / Hoàn tất; payment chips Tất cả / Chưa thanh toán / Một phần / Đã đủ |
| Cols | Số hóa đơn (doc link) / Khách hàng (sub "Nợ N ngày" or "Thanh toán ngay") / Tổng tiền / Đã thanh toán / Còn lại / Thanh toán (st) / Giao hàng (st) / Trạng thái (st) / acts. Footer totals for the money columns |
| Row acts (`soActs`) | When editable: "Sửa" (navy), "Xóa" (danger). Always: "Sao chép" (line) |
| soModal (wide) | Title "Tạo hóa đơn" / "Sửa hóa đơn X". Fields: Khách hàng (combobox), Số ngày nợ, Ghi chú / lịch giao. lineEditor Cols: Tên sản phẩm / Giá / Chiết khấu / Đơn vị tính / Số lượng / Thành tiền; each line shows its price source (priceSrc) and a color picker. Payment block: "Khách đã thanh toán" (edit mode "Khách trả thêm") + Vào quỹ. Override checkbox "Tôi xác nhận vẫn bán…" appears when a limit or min-price rule is breached. Extra "Lưu nháp"; submit "Lưu & xác nhận bán" |
| Drawer "Hóa đơn X" (`drawSO`, 2387) | sub "{khách} · phiên bản N · đã khóa sửa/xóa" (lock suffix only once posted). Status pills. Acts (by state): "Xác nhận bán", "Xuất kho giao khách", "Giữ thêm hàng", "Hủy phần chưa giao", "Ghi nhận thanh toán", Sửa / Xóa / Sao chép. Paybox impact: Tổng tiền / Đã thanh toán / Còn lại / Trạng thái thanh toán. kv info. Lines table with Đã giao / Còn giao columns. Sections "Phiếu xuất kho" (PX links), "Thanh toán" (receipts), "Lịch sử" (timeline). Deleted: "Hóa đơn đã bị xóa." |
| soConfirm | Confirms the sale and reserves stock; submit "Xác nhận bán" |
| soDeposit | Title "Ghi phiếu thu"; Số tiền, Vào quỹ; submit "Ghi phiếu thu" |
| soTopUp | Extra payment after confirm (Khách trả thêm), same fields as the deposit |
| soCancel | Cancels the undelivered part; required reason field; danger submit |
| soDel | Delete confirm for an editable invoice |
| newDLV (wide, final 3785) | Title "Xuất kho giao khách · {id}". Cols: Hàng / Đã giữ, chưa xuất / Xuất lần này (qty input). Hint "Dòng có “0”…" (zero lines are skipped). Impact: Giá trị xuất / Kho "Trừ ngay" / Công nợ khách / Trừ tiền trả trước. Submit "Ghi sổ xuất kho". Toast "Đã xuất kho PX-xxxx, ghi chứng từ bán BH-xxxx…", then reopens drawSO |
| Drawer "Phiếu xuất kho X" (final drawDLV) | kv Khách hàng / Chứng từ bán; Cols Hàng / SL / Lô xuất; impact Phải thu phát sinh / Đã thanh toán / Khách còn nợ |

#### 5.2.4 Trả hàng (4058 wrapping 2479)

| Element | Spec |
|---|---|
| Acts | "Đổi hàng" (btn-line), "Tạo phiếu trả" (primary) |
| Returns Cols | Phiếu / Chứng từ gốc / Khách / Hàng trả / Lý do / Giá trị / Giảm nợ / Cần hoàn / row act "Hoàn tiền" (when refund is due) |
| Section "Phiếu đổi hàng" | Cols Phiếu / Khách / Chứng từ gốc / Hàng trả / Hàng lấy / Thu / trả khách (text "khách trả thêm" / "hoàn khách" / "giảm nợ cũ") |
| newReturn (wide, 3995) | Title "Tạo phiếu trả hàng". "Chứng từ bán gốc" select (latest 40 sale docs). Lý do select: Hàng móp méo khi giao / Sai hàng / Khách đổi ý (trong chính sách) / Hàng gần hết hạn / Lỗi chất lượng. Cols Hàng / Màu (`retColorCell`: picker "Chọn màu khách trả", or note "Chứng từ cũ chưa ghi màu") / Còn được trả / SL trả. Empty "Chứng từ này không còn hàng được trả.". Checkbox "Đưa hàng trả vào khu cách ly chờ kiểm tra (bỏ tích nếu hàng còn nguyên, bán lại được ngay)". Impact: Giá trị trả (theo giá gốc) / Giảm nợ khách / Khoản cần hoàn / Kho. Submit "Ghi sổ trả hàng" |
| newExchange (wide, 4030) | Title "Đổi hàng". Fields Chứng từ bán gốc, Lý do (default "Đổi màu / đổi mẫu"), "Thu / trả chênh lệch qua" (fund). Section "1. Hàng khách trả lại": Cols Hàng / Màu / Giá đã bán / SL trả / "Đổi sang màu khác" (adds an inbound line with the same SKU). Checkbox "Hàng trả bị lỗi, đưa vào khu cách ly…". Section "2. Hàng khách lấy đổi": Cols Hàng (combobox + color pick) / SL / Đơn giá (+ price source) / Thành tiền / Xóa; empty hint "Bấm “Đổi sang màu khác”…"; button "+ Thêm hàng khác". Impact: Giá trị hàng trả / Giá trị hàng lấy / "Khách trả thêm" or "Cửa hàng trả lại khách" / Giảm nợ cũ của khách. Submit "Ghi sổ đổi hàng"; creates a DH- doc |
| Refund modal | Title "Ghi phiếu chi"; Số tiền, Chi từ quỹ; submit "Ghi phiếu chi" |
| Drawer sale doc (`openSDOC`) | Sale document detail with acts "Đổi hàng" and "Trả hàng" (both close the drawer first, then open newExchange / newReturnFor prefilled) |

### 5.3 Nhập hàng

#### 5.3.1 Đơn nhập (2701)

| Element | Spec |
|---|---|
| Acts | "Tạo đơn nhập" (primary) |
| Filters | search "Tìm số đơn, nhà cung cấp…" |
| Cols | Đơn nhập (doc) / Nhà cung cấp / Dự kiến giao (+ bad "Quá hạn giao") / Giá trị / Tiến độ nhận (meter 120px + "r / t đơn vị cơ sở") / Trạng thái (status "Đã duyệt" displays as "Đã chốt") / row act "Chốt đơn" (draft) or "Nhận hàng" (open) |
| newPO (wide) | Title "Tạo đơn nhập". Fields Nhà cung cấp (combobox) + "+ NCC mới" (opens supQuick); Kho nhận (hidden in one-store); Ngày dự kiến giao. buyEditor Cols: Tên sản phẩm / Đơn vị tính / Số lượng / Quy cách / Đơn giá / Thành tiền / Giá bán lẻ. An unknown name creates a new product inline. Impact: Giá trị đơn nhập / Hàng hóa mới sẽ tạo / Tồn / nợ NCC "Chưa phát sinh". Extra "Lưu nháp"; submit "Lưu & chốt đơn" |
| supQuick | Title "Thêm nhà cung cấp mới"; supplier fields (see 5.9.2); extra "Quay lại phiếu"; submit "Thêm & chọn" (returns to newPO with the new supplier selected) |
| Drawer PO (`openPO`) | Acts Nhận hàng / Ứng trước NCC / Đóng phần còn thiếu / Chốt đơn (by state). Lines incl. Đã nhận / Còn chờ. Sections "Phiếu nhận & nợ NCC", "Ứng trước NCC", "Lịch sử" |
| poAdvance | Title "Ứng trước cho X"; Số tiền, Chi từ quỹ; submit "Ghi phiếu chi" |
| poClose | Title "Đóng phần còn thiếu của X"; Lý do; submit "Đóng đơn" (status becomes "Đóng – thiếu") |

#### 5.3.2 Danh sách hàng hóa (2733)

| Element | Spec |
|---|---|
| Acts | "Thêm hàng hóa mới" |
| Filters | search; group chips |
| Cols | Mã · barcode / Tên hàng (+ color breakdown chips) / Đơn vị & quy đổi / Có thể bán / Giá nhập gần nhất / Giá vốn BQ / Giá bán lẻ / Giá sỉ / Biên lẻ / row act "Sửa giá bán" |
| Section "Lịch sử thay đổi giá bán" (`priceLogTable`) | Cols Ngày / Hàng / Giá lẻ (old → new) / Giá sỉ / Nơi sửa / Người sửa |
| newQuickProd | Title "Thêm hàng hóa mới". Fields Tên, Nhóm, ĐV cơ sở, ĐV lớn, Hệ số, Giá nhập, Giá lẻ, Giá sỉ, checkbox lot tracking; submit "Thêm" |
| editPrice (final 3879) | Fields Giá bán lẻ / Giá sỉ / Ghi chú. When color prices exist: checkbox "Áp dụng cho tất cả màu (bỏ giá riêng…)". Impact incl. Giá tối thiểu. Warn callout on a rule breach (sỉ > lẻ or below min). Embedded priceLogTable for the SKU. Submit "Lưu giá" |

#### 5.3.3 Nhận hàng (2756)

| Element | Spec |
|---|---|
| Acts | "Nhập nhanh"; one button per open PO "Nhận theo X · NCC" |
| Cols | Phiếu nhận / Nguồn (PO link or tag "Nhập nhanh") / NCC / Hóa đơn NCC (number or act "Đối chiếu HĐ") / Giá trị hàng / Chi phí phân bổ / Còn phải trả |
| newRCV | Title "Nhận hàng theo X" or "Nhập nhanh". Fields "Số hóa đơn NCC (nếu có)", "Chi phí vận chuyển/bốc xếp". buyEditor with an extra "Lô · hạn dùng" column for lot-tracked SKUs. Impact Nợ NCC tăng / Chi phí mua vào giá vốn / Hàng hóa mới sẽ tạo / Vượt dung sai 5%. Submit "Ghi sổ nhập kho" |
| rcvInvoice | Title "Đối chiếu hóa đơn NCC cho X"; submit "Lưu đối chiếu" |
| Drawer receipt (`openRCV`) | Act "Trả NCC từ phiếu này" (newPRT prefilled). Lines incl. Lô · hạn and Giá vốn sau phân bổ. Impact Nợ NCC phát sinh / Đã trả / giảm / Còn phải trả / Hạn trả. Settlements table |

#### 5.3.4 Trả NCC (2774)

| Element | Spec |
|---|---|
| h1 | "Trả nhà cung cấp" |
| Lead | "Xuất trả đúng lô đã nhận; giảm nợ NCC. Nếu đã trả tiền nhiều hơn số còn nợ, phần chênh ghi thành khoản NCC cần hoàn." |
| Acts | "Tạo phiếu trả NCC" (primary) |
| Cols | Phiếu / Phiếu nhận gốc / NCC / Hàng · lô / Lý do / Giá trị / Giảm nợ / NCC cần hoàn |
| newPRT (wide) | Title "Trả hàng nhà cung cấp". Fields Phiếu nhận gốc (select), Lý do (default "Hàng lỗi bao bì"). Cols Hàng / Lô / Tồn lô tại kho / SL trả. Impact Giá trị trả / Giảm nợ NCC / NCC cần hoàn. Submit "Ghi sổ xuất trả" |

### 5.4 Kho

#### 5.4.1 Tồn kho (3910)

| Element | Spec |
|---|---|
| Lead | "Hàng có màu hiện số tồn từng màu…" |
| Acts | "Điều chỉnh kho" (newADJ) |
| Filters | search "Tìm mã, tên, mã vạch…"; group chips; color chips "Mọi màu" / one per color "Màu (n)" / "Chưa gán màu"; scope chips "Tất cả hàng" / "Chỉ hàng có màu"; buttons "Mở tất cả màu" / "Thu gọn tất cả" |
| Cols | Hàng (`.tname`, ExpandableRow toggle with aria "Xem chi tiết/Thu gọn màu của X", inline color-qty pills) / Tồn thực / Có thể bán (sub "giữ N · cách ly N") / Giá bán (sub "sỉ X"; "giá chung mọi màu" or "N màu giá riêng" + min–max range) / Giá trị tồn / acts "Sửa giá" + "Thẻ kho" |
| Color subrows | One per color: color chip + name, qty columns, price with sub "giá riêng" or "theo giá chung"; "Sửa giá" opens colorPrice |
| Footer | "N mặt hàng · N có màu" |
| Hint | "Combo ghép tại quầy không có tồn riêng…" |
| colorPrice modal | Title "Giá bán {màu} · X". Fields Giá lẻ / Giá sỉ / Ghi chú; checkbox "Bỏ giá riêng, dùng giá chung…" (disables the inputs); impact; submit "Lưu giá màu" |
| editPrice | see 5.3.2 |

#### 5.4.2 Giữ hàng (2804)

| Element | Spec |
|---|---|
| Cols | Nguồn (doc) / Hàng / Lượng giữ / Từ ngày / row act "Giải phóng" |
| Empty | "Không có lượng giữ." |
| Modal "Giải phóng hàng giữ" | Lý do; submit "Giải phóng" |

#### 5.4.3 Kiểm kê (2816)

| Element | Spec |
|---|---|
| Acts | "Bắt đầu phiên kiểm kê" |
| Cols | Phiên (sub Cutoff time) / Số mã / Đã đếm / Dòng lệch / Trạng thái / row act "Nhập số đếm" (open) or "Xem & ghi sổ" |
| newCount | Title "Bắt đầu kiểm kê"; Nhóm hàng select (default "Toàn kho"); submit "Bắt đầu" |
| Drawer count (`drawCount`) | Buttons "Hoàn tất đếm" / "Xác nhận & ghi sổ chênh lệch" / "Đếm lại". Cols Hàng / Số hệ thống (shows "Ẩn khi đếm mù" while counting) / Thực đếm (input) / Chênh lệch / Giá trị lệch |

#### 5.4.4 Lô & hạn dùng (2830)

| Element | Spec |
|---|---|
| Chips | Tất cả lô / Hết hạn trong 90 ngày / Đã hết hạn / Khu cách ly |
| Cols | Hàng / Lô / Hạn dùng / Còn (days, or bad "Quá N ngày") / SL / Tình trạng (st: "Cách ly – chờ kiểm tra", "Hết hạn – chặn bán", "Sắp hết hạn", "Bán được") / acts |
| Row acts | Quarantine: "Đạt – cho bán", "Không đạt – hủy". Expired: "Tiêu hủy". Near expiry: "Xả hàng" (goes to `#chien-dich.mua`) |
| Modal "Tiêu hủy lô hết hạn" | submit "Ghi sổ tiêu hủy" |

#### 5.4.5 Điều chỉnh kho (2840)

| Element | Spec |
|---|---|
| Acts | "Tạo điều chỉnh" |
| Cols | Phiếu / Loại / Hàng / SL / Lý do / Giá trị / Trạng thái |
| newADJ | Title "Điều chỉnh giảm kho". Loại: Hỏng / Hao hụt / Biếu tặng / Dùng nội bộ. Hàng (combobox) + color pick; Số lượng; "Lý do (bắt buộc)". Impact Tồn bán được / Giá trị ghi giảm / Doanh thu "Không phát sinh". Submit "Ghi sổ điều chỉnh" |

#### 5.4.6 Thẻ kho (3671)

| Element | Spec |
|---|---|
| Filters | product combobox (pool stock); Từ / Đến dates; range chips Hôm nay / 7 ngày / Tháng này / Tháng trước / Từ đầu; color chips; unit chips "Quy đổi tự động" / "Theo {u} ({f} base)" |
| KPIs (value 22px) | Tồn đầu kỳ / Nhập trong kỳ / Xuất trong kỳ / Tồn cuối kỳ; reconciliation st "Đã khớp sổ và tồn" (ok) or "Lệch: thẻ kho X, tồn thực Y" (bad) |
| Cols | Ngày / Chứng từ (doc link) / Loại / Lô / Màu / Nhập / Xuất / Tồn / Đơn giá vốn. First row "Tồn đầu kỳ"; footer "Cộng kỳ" |
| Empty hint | "Không có biến động trong khoảng…" |
| Qty format | in auto mode, mixed units, e.g. "1 thùng 3 dây 2 hộp" (`qMix`) |

### 5.5 Giỏ quà

#### 5.5.1 Giỏ quà & combo (2845)

| Element | Spec |
|---|---|
| Acts | "Tạo giỏ / combo" |
| Chips | Tất cả / Nháp / Đang bán (with counts) |
| Layout | `.g2` cards |
| Card | Title "name · vN"; st + tag "Giỏ đóng sẵn" or "Combo ghép khi bán"; component table; impact Giá bán giỏ / Tổng giá bán lẻ thành phần / Khách tiết kiệm / Lãi gộp dự kiến |
| Card buttons | Draft: Xem trước / Sửa nháp / Phát hành / Xóa nháp. Active: Tạo lệnh đóng (kits only) / Tạo phiên bản mới / Sửa thành phần (combos) |
| kitModal (wide) | Title "Tạo giỏ quà / combo" / "Sửa nháp · X". Loại: "Giỏ đóng sẵn (có tồn, qua lệnh đóng)" / "Combo ghép khi bán (không có tồn)". Fields Mã, Tên, Giá bán lẻ, Giá sỉ / doanh nghiệp, Tồn tối thiểu (giỏ). compEditor Cols Thành phần / SL / giỏ / Giá bán / Thành tiền (giá bán) / Giá vốn / Có thể bán; buttons "+ Thêm thành phần", "+ Thêm thùng mix vị"; field "Chi phí đóng gói / giỏ…"; Ghi chú. Live impact plus warn callouts (price above the component retail total; margin below 15%). Extra "Lưu nháp"; submit "Lưu & phát hành" / "Lưu nháp" |
| kitPreview drawer | `preview-card` (customer-facing look) + section "Thông tin nội bộ" |
| recipeVer modal | "Lý do thay đổi (bắt buộc)"; submit "Lưu phiên bản" |
| comboEdit modal | component edit; submit "Lưu" |
| kitDel modal | submit "Xóa nháp" |

#### 5.5.2 Lệnh đóng giỏ (2975)

| Element | Spec |
|---|---|
| Acts | "Tạo lệnh đóng" (primary; newASM). The recipe card button "Tạo lệnh đóng" opens it prefilled |
| Cols | Lệnh / Giỏ / Kế hoạch / Hoàn thành / Tiến độ (meter) / Trạng thái (bad "Thiếu" when short) / row act "Chốt & giữ NL" or "Ghi nhận hoàn thành" |
| newASM | Fields Công thức, Kho đóng (hidden in one-store), Số giỏ kế hoạch, "Gắn đơn bán (tùy chọn)". Live table Nguyên liệu / Cần / Có thể bán / Thiếu or "Đủ". Submit "Tạo lệnh" |
| asmDone | qty done; submit "Ghi sổ hoàn thành" |
| Drawer (`openASM`) | Stepper Nháp / Chốt & giữ NL / Soạn NL / Đóng giỏ / QC / Nhập kho; genealogy cards (component lots into the finished lot) |

#### 5.5.3 Tiến độ & truy xuất lô (2989)

| Element | Spec |
|---|---|
| Card "Nhu cầu giỏ theo đơn" | Cols Đơn / Đặt / Đã giao / Đã giữ / Cần đóng thêm |
| Card "Tồn giỏ thành phẩm theo lô" | finished-basket lots with qty |
| Table "Truy xuất lô thành phần" | finished lot to component lots trace |

#### 5.5.4 Tháo giỏ (2997)

| Element | Spec |
|---|---|
| Acts | "Tháo giỏ" (primary; newDIS) |
| Cols | Phiếu / Giỏ / SL / Vật tư bao bì / Giá trị |
| newDIS | Title "Tháo giỏ quà"; checkbox "Giỏ mây, nơ bị hỏng khi tháo…"; submit "Ghi sổ tháo giỏ" |

### 5.6 Công nợ

#### 5.6.1 Khách còn nợ (3139) and Cửa hàng còn nợ NCC (3141)

Both use one view with `side = AR | AP`.

| Element | Spec |
|---|---|
| Chips | "Có số dư" / "Tất cả khách" (AP: "Tất cả NCC") |
| Cols (`debtTable`) | Name (lnk opens openParty) / Khách còn nợ (AP: Cửa hàng còn nợ NCC) / Trong hạn / Quá 1–30 / Quá 31–60 / Quá > 60 / Tiền khách ứng trước / acts "Thu tiền" + "Bảng kê" (AP: "Trả tiền"). Footer totals |
| Drawer party (`openParty`) | Acts "Thu tiền" / "Lập bảng kê" / "Ghi chú nhắc nợ" (AP: "Trả tiền"). Impact incl. Hạn mức còn lại. Callout when the party is both customer and supplier. Section "Chứng từ còn mở" with row act "Giảm/xóa nợ"; section "Thanh toán"; section "Ghi chú liên hệ" (timeline) |
| payModal (wide) | Title "Thu tiền khách" / "Trả tiền nhà cung cấp". Fields party select, Số tiền, "Vào quỹ" / "Chi từ quỹ", Nội dung. Allocation Cols Chứng từ / Hạn / Còn nợ / Phân bổ (input; auto-filled oldest first). Impact plus bad callouts "Tổng phân bổ vượt số tiền." and "Có dòng phân bổ vượt số còn nợ." (block submit). Submit "Ghi phiếu thu" / "Ghi phiếu chi" |
| debtNote | contact note; submit "Lưu ghi chú" |
| writeOff | amount + reason; submit "Ghi giảm nợ" |
| newSTM | see 5.6.3 |

#### 5.6.2 Ứng trước & cọc (3170)

| Element | Spec |
|---|---|
| Cols | Phiếu / Đối tác / Chiều / Loại / Số tiền / Chưa phân bổ / row act "Phân bổ" |
| allocAdv | allocate the advance to open docs; submit "Ghi phân bổ" |

#### 5.6.3 Đối soát (3179)

| Element | Spec |
|---|---|
| Acts | "Lập bảng kê" |
| Cols | Bảng kê (sub vN) / Khách / Kỳ / Đầu kỳ / Cuối kỳ / Trạng thái / acts "Gửi khách", or "Khách xác nhận" + "Tranh chấp" |
| newSTM | Fields Khách hàng / Từ ngày / Đến ngày; submit "Lập bảng kê" |
| Drawer (`drawSTM`) | running-balance table; "Lịch sử phiên bản" |
| stmDispute | dispute note; submit "Ghi nhận" |

#### 5.6.4 Lịch thu/trả (3192)

| Element | Spec |
|---|---|
| KPIs | Dự kiến thu 7 ngày / Dự kiến trả 7 ngày (forecast style) / Tiền đang có |
| Cols | Hạn / Chiều (Thu / Thu hộ về / Trả) / Đối tác / Chứng từ / Số tiền / Cam kết |

### 5.7 Chiến dịch & khách

#### 5.7.1 Chiến dịch mùa vụ (3246)

| Element | Spec |
|---|---|
| Layout | `.g2` campaign cards with campSummary (meter + impact) and checklist "Checklist trước cao điểm & kết mùa" (`.todo`) |
| Table "Nhu cầu hàng mùa Tết theo ngày" | Cols Hàng / Đơn đã chốt còn giao / Có thể bán / PO dự kiến về / Ngày về sớm nhất / Thiếu sau khi về |

#### 5.7.2 Khách hàng (CRM) (3253)

| Element | Spec |
|---|---|
| Filters | search "Tìm tên, điện thoại, nhóm…" |
| Cols | Khách / Nhóm / Doanh thu từ 01/10 / Lần mua gần nhất / Đồng ý nhận tin / row act "Tái đặt đơn" (has history) or "Tạo đơn" |
| Behavior | "Tái đặt đơn" opens soModal prefilled with the last order lines and shows a toast |

### 5.8 Báo cáo (shared `repFilter`)

`repFilter`: Từ / Đến dates (default 2026-10-01 to today); hint "Số liệu tạm tính…"; button "Xuất CSV" opens modal "Dữ liệu CSV: x" with a read-only textarea and "Sao chép".

| Screen | Content |
|---|---|
| Nhập–xuất–tồn (3264) | status pill; Cols Hàng / Đầu kỳ / Nhập / Xuất / Cuối kỳ / Giá trị cuối kỳ |
| Bán hàng (3273) | chips Theo hàng / Theo khách / Theo kênh; Cols Nhóm / Doanh thu / Trả hàng / Doanh thu thuần; hint that reconciles with the committed total |
| Lãi gộp & kết quả quản trị (3288) | KPIs Doanh thu thuần / Giá vốn / Lãi gộp (sub Biên) / Sau chi phí hoạt động; Cols Hàng / Doanh thu thuần / Giá vốn / Lãi gộp / Biên |
| Tuổi nợ (3301) | Two tables "Phải thu khách" and "Phải trả nhà cung cấp", Cols name / Trong hạn / 1–30 ngày / 31–60 ngày / > 60 ngày / Tổng |
| Dòng tiền (3310) | KPIs Đầu kỳ / Tổng thu / Tổng chi / Cuối kỳ; Cols Phân loại / Thu / Chi / Ròng |
| Giỏ quà (3321) | Table Chỉ tiêu / Giá trị (8 rows of basket KPIs) |

### 5.9 Danh mục

#### 5.9.1 Hàng hóa (3337)

| Element | Spec |
|---|---|
| Acts | "Thêm hàng hóa" (newSKU), "Tạo giỏ / combo" (kitModal) |
| Cols | Mã · mã vạch / Tên hàng / Loại (tag: Hàng hóa / Vật tư / Giỏ đóng sẵn / Combo ảo; + "mix vị") / Đơn vị & quy đổi / Lô/hạn ("Bắt buộc" / "Không") / Giá lẻ / Giá sỉ / Giá vốn BQ / Giá tối thiểu / Tồn tối thiểu / st |
| newSKU | Fields Mã SKU, Tên, Nhóm, Mã vạch, ĐV cơ sở, ĐV lớn, Hệ số (hint "Phải là số dương"), Giá bán lẻ, Giá sỉ, Giá mua tham khảo, Tồn tối thiểu, checkbox lot tracking. Submit "Lưu". Errors: "Cần mã và tên hàng." / "Mã SKU đã tồn tại trong cửa hàng." / "Mã vạch đã được dùng cho hàng khác." / "Hệ số quy đổi phải dương." |

#### 5.9.2 Nhà cung cấp (3425)

| Element | Spec |
|---|---|
| Acts | "Thêm nhà cung cấp" |
| Filters | search "Tìm tên, mã, số điện thoại, người liên hệ, mặt hàng…"; chips "Đang giao dịch (n)" / "Ngừng giao dịch (n)" / "Tất cả" |
| Cols | Nhà cung cấp (tag "kiêm khách hàng", sub MST) / Liên hệ / Mặt hàng cung cấp / Điều khoản / Đơn nhập (sub last receipt) / Cửa hàng còn nợ / Trạng thái / acts |
| Row acts | Active: Sửa, Tạo đơn nhập, Ngừng, Xóa. Stopped: Sửa, Mở lại |
| Empty | "Chưa có nhà cung cấp phù hợp." |
| supModal | Title "Thêm nhà cung cấp" / "Sửa nhà cung cấp · X". Fields "Tên nhà cung cấp *", Người liên hệ, Số điện thoại, Mã số thuế, Địa chỉ, Số ngày nợ được phép (hint "0 = trả ngay…"), Mặt hàng cung cấp, Tài khoản ngân hàng, Ghi chú, checkbox dual role (also a customer). Submit "Thêm nhà cung cấp" / "Lưu" |
| Validation | name required; duplicate name; phone `/^[0-9 .+()-]{8,20}$/`; duplicate phone; terms 0–365 |
| Business rules | "Ngừng" is blocked while open POs exist; "Xóa" is blocked once documents exist; ids are `NCC###` |
| supDel | confirm; submit "Xóa" |
| supQuick | see 5.3.1 |

#### 5.9.3 Bảng giá (3353)

| Element | Spec |
|---|---|
| Table 1 | Cols Ưu tiên / Bảng giá (sub "Bậc thang" for tiers) / Áp dụng / Hiệu lực ("không thời hạn") |
| Section "Giá theo hàng" | Cols Hàng / Giá lẻ / Giá sỉ / doanh nghiệp / Giá tối thiểu (cảnh báo nếu bán thấp hơn) / Giá vốn BQ |
| Modals | none (read-only screen) |

#### 5.9.4 Nhóm mix vị (3574)

| Element | Spec |
|---|---|
| Acts | "Thêm nhóm mix" |
| Cols | Nhóm mix / Quy cách / Các vị · tồn có thể bán / Giá lẻ / thùng / Đang dùng trên / Trạng thái / acts (`lnk`) "Sửa · Ngừng dùng" or "Dùng lại · Xóa" |
| Empty | "Chưa có nhóm mix vị nào. Bấm “Thêm nhóm mix”." |
| mixModal (wide) | Fields Tên nhóm mix, Đơn vị thùng, Số đơn vị lẻ / thùng, Đơn vị chọn vị, Số đơn vị lẻ / đơn vị chọn vị (locked once the group is used). Cols Mặt hàng (vị) / Tên vị ngắn / Đơn vị lẻ / Giá lẻ / Có thể bán / Gỡ (blocked with "đã bán"). Button "+ Thêm vị". Live callouts. Submit "Tạo nhóm" / "Lưu". Validation messages at lines 3524–3549 |
| Seeds | TH180 (48 per carton / 4 per pack); NG330 (thùng 24 / lốc 6; flavors Cola / Cam / Xá xị / Chanh) |

### 5.10 Thiết lập

| Screen | Content |
|---|---|
| Tài khoản & bảo mật (3358) | Card "Chủ cửa hàng": kv Họ tên / Cửa hàng / Đăng nhập gần nhất / Xác thực 2 lớp. Card "Thiết bị đang đăng nhập": rows with "Đăng xuất", or st "Thiết bị này" for the current device |
| Thuê bao (3379) | 4 plan cards Cửa hàng / Bán sỉ / Giỏ quà / Nhiều điểm, each marked "Đang dùng" or "Gói nền". "Thuê bao hiện tại" kv. "Truy cập hỗ trợ": "Cấp quyền hỗ trợ 2 giờ (chỉ đọc)", which then shows a callout and "Thu hồi" |
| Nhật ký (3385) | search "Tìm thao tác, chứng từ, người dùng…"; max 200 rows; Cols Thời điểm / Người dùng / Thao tác / Chứng từ / Chi tiết |

### 5.11 Global modal

| Modal | Spec |
|---|---|
| "Đặt lại dữ liệu mẫu?" (1887) | From util "Đặt lại mẫu"; resets localStorage to the seed |

---

## 6. Client data model and engine rules

### 6.1 State `S` (persisted)

| Key | Content |
|---|---|
| `meta` | `{date, branch, role, seq, ver}`; `seq` holds per-prefix counters |
| `products` | SKU: id, name, group, barcode, base unit, `uoms[{u,f}]`, type (`goods\|supply\|kit\|combo`), price, whole, cost (moving average), minStock, lot flag, colors, `colorPrices{k:{price, whole}}`, `flavor`, `mixGroup` |
| `parties` | customers and suppliers: isCust, isSup, phone, limit, terms, tax id, contact |
| `wh`, `branches` | warehouses, branches (one each in the seed) |
| `lots` | lot, exp, qty, status (quarantine / expired / sellable), color |
| `moves` | stock ledger (in/out, doc, lot, color, unit cost) |
| `res` | reservations (source doc, sku, qty, from date) |
| `so`, `quotes`, `carts` | invoices, quotes (version, history), POS carts (status Đang soạn / Đã bán) |
| `po`, `rcv`, `prt` | purchase orders, receipts, supplier returns |
| `dlv`, `sdoc`, `sret`, `exch` | deliveries (PX), sale docs (BH), returns, exchanges (DH) |
| `obl` | obligations `{AR, AP, COLL}` |
| `pay`, `funds`, `cash` | payments (PT/PC), funds (TM-CN1 cash, NH-01 bank), cash shifts |
| `counts`, `adj` | stock counts, adjustments |
| `recipes`, `asm`, `disasm`, `gen` | kit recipes (versions), assembly orders, disassembly, genealogy |
| `camps`, `stm` | campaigns, statements (versions) |
| `audit`, `users`, `devices`, `support` | audit log, users, devices, support grant |
| `pricelists`, `priceLog`, `mixGroups` | price lists, sell-price history, mix groups |

UI state `UI` (not persisted): `f` (filters per key), `pos` (cust, lines, cash, bank, credit, cartId, offline, queue, exc), open sets for expandable rows.

### 6.2 Rules

| Rule | Behavior |
|---|---|
| Available | `avail = sellable − reserved`; quarantine and expired lots are excluded. Combo avail = min over components (floor of component avail ÷ qty per combo) |
| Picking | FEFO (earliest expiry first), filtered by the chosen color |
| Costing | moving-average cost on receipt; freight/handling allocated into cost |
| Receiving | 5% over-receipt tolerance; lot + expiry required for lot-tracked SKUs |
| Price validity | `sỉ ≤ lẻ`; `minPrice = cost × 1.08`, rounded to 100 |
| Price priority | customer-specific price, then group price list, then retail; a per-color price overrides the base price for that color (`priceFor` override at 3836) |
| Mix price | average of the flavor retail prices × pack factor |
| Quote | Nháp, Đã gửi, Khách chấp nhận / Khách từ chối, Đã chuyển hóa đơn; passing `validTo` derives "Hết hạn"; no reservation and no revenue |
| Invoice | draft is editable; confirm reserves stock; delivery (PX) posts the sale doc (BH) and AR; once posted it is locked against edit/delete ("đã khóa sửa/xóa") |
| Cart | saving does not reserve; a sold cart is locked (copy only) |
| POS offline | cash-only full payment; queued with an idempotency key (`OFF-###` + timestamp); sync posts each once; failures go to the exception queue |
| Supplier | stop blocked by open POs; delete blocked by documents |
| Permissions | demo is Owner only; `can('viewCost')`, `can('sellCredit')` gate cost display and credit sales |

### 6.3 Persistence

localStorage key `tetsales-demo-v9`; `meta.ver` up to 11 with migrations seedV8 / seedV10 / seedV11. Seed business date `TODAY = 2026-10-12`.

---

## 7. Formatting and document numbers

| Formatter | Output |
|---|---|
| `vnd(n)` | `Intl.NumberFormat('vi-VN')` + NBSP + `₫` (e.g. `1.250.000 ₫`) |
| `money(n)` | `vnd` wrapped in `.tab-n` |
| `costV(n)` | "Ẩn theo quyền" when cost is hidden |
| `num(n)` | vi-VN grouping |
| `fd(d)` | `dd/mm/yyyy` |
| `fdS(d)` | `dd/mm` |
| Chart axis | millions as "tr", thousands as "k" |
| Mixed units | `qMix`: "1 thùng 3 dây 2 hộp" |

`nextNo(prefix)` gives `PREFIX-0001` (4-digit zero pad, per-prefix sequence). `docLink` maps prefixes to drawers.

| Prefix | Document |
|---|---|
| SO | invoice (hóa đơn) |
| BG | quote |
| GH | POS cart |
| BH | sale doc (chứng từ bán) |
| PX | delivery / stock issue (phiếu xuất kho) |
| HD | POS retail sale (`posSale`, line 740) |
| DH | exchange (đổi hàng) |
| PO | purchase order |
| PN | receipt (phiếu nhận) |
| XT | supplier return (xuất trả) |
| PT / PC | cash receipt / cash payment |
| CT | fund transfer (`fundTransfer`, dead Tiền module) |
| CH | delivery trip (`createTrip`, dead Giao nhận) |
| LD | assembly order (lệnh đóng) |
| TG | disassembly (tháo giỏ) |
| KK | stock count |
| DC | adjustment |
| TH | customer return (phiếu trả hàng, `salesReturn`, line 692) |
| CK | warehouse transfer (dead Chuyển kho) |
| CA | shift |
| CP | expense |
| DS | statement (bảng kê); DS-COD = COD settlement (dead Giao nhận) |
| YD | approval request (`requestApproval`) |
| SP | auto-created product id (`'SP'` + 3-digit pad, line 1389; not `nextNo`) |
| MX | mix group id (`'MX'` + 2-digit + base36 suffix, line 3559; not `nextNo`) |
| HT- | support-access grant id (line 3383; not `nextNo`) |
| OFF- | offline POS local id (3-digit pad, not `nextNo`) |

---

## 8. A11y and UX gaps (fix in the React build)

| # | Gap | Recommendation |
|---|---|---|
| 1 | POS customer uses a native `<select>`, unlike the combobox used everywhere else | Use `Combobox` (pool party) |
| 2 | Table inputs (CK %, SL, Thực đếm, Phân bổ) only have `aria-label`, with no row context | Make the label "SL – {tên hàng}" |
| 3 | "Chưa thanh toán" is listed twice in ST_WARN | Dedupe; define the status-tone map as one typed enum |
| 4 | Nav groups use `aria-expanded` but force a single-open accordion that navigates on click | Separate expand from navigate, or use `NavigationMenu`; keep `aria-current="page"` |
| 5 | The drawer has no focus trap; focus is not restored after a modal or drawer closes | Use the Radix Dialog/Sheet defaults (trap + restore) |
| 6 | Toasts auto-dismiss (4.5s / 7s), including errors | Keep error toasts until dismissed; show doc numbers in a persistent place |
| 7 | Chart tooltip is hover-only | Keep the "Xem dạng bảng" fallback; add keyboard focus on bars |
| 8 | The `.st` glyph is decorative, but status still reads from text | Keep the text; `aria-hidden` the glyph |
| 9 | Nhóm mix row actions use `lnk` while other screens use buttons; "Ngừng dùng" and "Xóa" post without confirmation | Use `Button size=sm`; add a confirm dialog |
| 10 | Swatch radios lack arrow-key roving | Use Radix RadioGroup |
| 11 | colorPrice uses `disabled` inputs, which drops them from tab order and lowers contrast | Use `readOnly` + explanation |
| 12 | Tables have no `<caption>` | Add a visually hidden caption from the section title |
| 13 | Native date inputs (format varies by locale) | Use a dd/mm/yyyy DatePicker |
| 14 | CSV export is copy-to-clipboard only | Add a real file download (PRD §9.2 long-running export with progress) |
| 15 | Full re-render on blur can swallow the click that follows (PRD §9.3) | Use controlled inputs; never re-render the whole tree on blur |
| 16 | POS shift auto-opens silently | Make shift open/close explicit in production |
| 17 | Success toast fires after the local mutation (simulated latency) | Per PRD §9.3, show success only after the server confirms |
| 18 | Touch targets: `.tgl` is 24×24 and `.btn-sm` inside `.acts` has 5px 10px padding | Use a 44px hit area on mobile |

---

## 9. Excluded / dead views (do not build)

These views are defined but are not in MENU, or are overridden by a later definition. Old hashes redirect via `MOVED` (section 2).

| View / function | Line | Reason |
|---|---|---|
| `mua-hang.de-nghi` (Đề nghị nhập) | 2612 | removed from menu (PRD §9.1) |
| `kho.chuyen-kho` + `newTRF`, `trfRecv`, `openTRF` | 2807 | removed (Chuyển kho) |
| `kho.mau` (Tồn theo màu) | 3046 | merged into Tồn kho; redirect |
| `giao-nhan.chuyen` | 3076 | Giao nhận removed |
| `giao-nhan.van-don` | 3110 | Giao nhận removed |
| `giao-nhan.hoan-hang` | 3113 | Giao nhận removed |
| `giao-nhan.cod` | 3121 | Giao nhận removed |
| `cong-no.thu-ho` | 3169 | not in MENU |
| `danh-muc.doi-tac` | 3346 | redirect to CRM |
| `thiet-lap.tich-hop` | 3363 | redirect to Tài khoản |
| `thiet-lap.du-lieu` (Import/Export) | 3371 | redirect to Tài khoản |
| `tien.quy` / `tien.thu-chi` / `tien.chuyen-tien` / `tien.sao-ke` / `tien.ca` / `tien.chi-phi` | 3203 / 3207 / 3215 / 3218 / 3224 / 3229 | Tiền module removed; redirect to Dòng tiền |
| old `kho.ton` | 2784 | overridden at 3910 |
| old `kho.the-kho` | 2795 | overridden at 3671 |
| old `tong-quan.dashboard` | 2023 | overridden at 2528 |
| old `newDLV` (recipient, carrier, schedule, "Thu hộ tiền (COD) khi giao", submit "Ghi phiếu giao") and old `drawDLV` | ~2420 / 3087 | overridden at 3785 / 3797 |
| old `editPrice` | before 3879 | overridden at 3879 |
| `V['mod.sub']` comment placeholder | 1731 | not a view |
