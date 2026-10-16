---
title: "Phase 16: Web sales & POS (online)"
status: todo
priority: P1
effort: 9d
dependsOn: [phase-14-web-foundation-design-system, phase-09-sales-pos, phase-10-debt-cash]
---

# Phase 16 — Web sales & POS (online)

## Context links

- UI spec §5.2 Bán hàng (5.2.1 POS, 5.2.2 Báo giá, 5.2.3 Hóa đơn, 5.2.4 Trả hàng), §6.2 rules (avail, FEFO by color, price priority, cart, combo avail), §7 prefixes SO/BG/GH/BH/PX/HD/DH/TH/PT/PC/CA
- Prototype views 3834 (wraps `_posView` 2216), 2444, 2323, 4058 (wraps 2479); `drawSO` 2387; `soConfirm` 2411; `soTopUp` 2413; `soDeposit` 2414; `soCancel` 2418; `soDel` 2385; `newDLV` 3785; `drawDLV` 3797; `newReturn` 3995; `newExchange` 4030; `comboColorRows`; `priceFor` override 3836
- Schema: `design/database-schema-sales-kits.md` (quotes, invoices + versions, stock_outs, sales_documents, shifts, pos_carts, pos_cart_lines, sales_returns, exchanges), `design/database-schema-debt-cash-crm-saas.md` (payments, fund accounts)
- PRD SAL-01..15, PRC-03..07, DEBT-05/06, CASH-01/02/04/06, SEC-03, KIT-03

## Overview

Builds the four Bán hàng routes against the real API: POS with saved carts (online path; offline is phase 19), quotes with versioning, invoices with confirm/reserve, multi-delivery stock-out, payments and cancellation, customer returns and exchanges, plus a print view for sale documents. Introduces the `sell` and `quote` presets of `LineItemsEditor`, color and mix-pack line handling, and the reusable owner exception flow for credit-limit and min-price breaches.

## Key insights

- Pricing is server-resolved (`PriceResolver`: customer price → group price list → retail; per-color override). The editor asks `POST /pricing/quote` with lines + customer and shows `price_source` via `PriceSourceBadge` ("Nguồn giá: …"); it never computes prices.
- Exceptions are never pre-blocked client-side: confirm/checkout returns `409 exception_required` (`credit_limit`, `below_min_price`) and `useConfirmException` opens "Xác nhận ngoại lệ cho {id}" with "Tôi xác nhận vẫn bán". The soModal override checkbox "Tôi xác nhận vẫn bán…" is the same flow inlined when the dry-run preview already reports a breach.
- Colored SKUs need a chosen color before confirm/sell; only colors with sellable stock are offered; a single remaining color auto-picks (server invariant `ColorRequirementSpec`). Picking a color that matches another line merges lines with toast "Đã gộp vào dòng X Màu." / "Đã gộp vào dòng X cùng màu.".
- Combos (KIT-03) have no stock; tile shows "· ghép từ thành phần", component color rows, "cố định theo combo" for single-color components, avail = min over components (server value).
- Mix-pack line "Thùng mix vị": mix-group select + `MixFlavorGrid` of "vị · còn N dây" qty inputs; price = server mix price; sum of flavor parts must equal one pack.
- Shift: CASH-06 says the shift opens implicitly on the first POS sale; the server opens it (`shifts` "opened implicitly by first POS sale of the day"). The UI never shows open/close controls; the callout "Chưa có ca bán hàng đang mở. [Mở ca]" is kept only as a fallback when the API reports `shift_unavailable`. UI spec gap #16 recommends explicit shifts — recorded as a decision to revisit, not built.
- POS checkout is one command: `POST /pos/carts/{id}:checkout` (or `POST /pos/checkout` for an unsaved cart) with `Idempotency-Key`; response carries `HD-` sale doc number, change and the locked cart `GH-`.
- Success toasts render only after the 2xx (gap #17). The POS cart uses controlled inputs so blur never swallows the "Thanh toán · F9" click (gap #15, PRD §9.3).

## Requirements

| PRD ID | Behaviour |
|---|---|
| SAL-01 | `/sales/invoices` list + soModal (customer, payer, terms, lines Tên SP/Giá/CK/ĐVT/SL/Thành tiền, price source, colors) |
| SAL-02, PRC-04, DEBT-06, SEC-03 | "Xác nhận bán" → server checks stock/limit/min price → exception dialog → reserve |
| SAL-03 | "Giữ thêm hàng" (soTopUp) reserves newly sellable stock; toast "Giữ thêm {n} cho {id}." / "Không có thêm tồn có thể bán để giữ." |
| SAL-04 | newDLV "Xuất kho giao khách · {id}" multi-delivery, FEFO by color server-side; drawDLV shows lots |
| SAL-05 | POS: scan/search, ≤30 tiles with color dots, customer Combobox, unit switch, line discount, cash/transfer, change, credit remainder |
| SAL-06, DEBT-05 | soDeposit "Ghi nhận thanh toán {id}" (advance before delivery, auto-applied on delivery); "Bán chịu phần còn lại X" at POS |
| SAL-07 | "In phiếu" on sale-document drawers → `/print/sales-documents/:id` (print stylesheet; addition beyond prototype) |
| SAL-08, SAL-09 | newReturn "Tạo phiếu trả hàng" (with source doc, per-color returnable, quarantine checkbox) → TH-; refund "Ghi phiếu chi" |
| SAL-10 | soCancel "Hủy phần chưa giao của {id}" with reason; invoice edit creates a new version (draft-only edit/delete; posted "đã khóa sửa/xóa") |
| SAL-14 | Saved carts: hold "Lưu giỏ · F4", chips Đang soạn/Đã bán/Tất cả, edit/copy/delete; sold cart locked |
| SAL-15 | newExchange "Đổi hàng" → DH-, from Trả hàng acts and sale-doc drawer |
| PRC-05 | Quote lifecycle chips/acts, versioning, reject reason, convert to invoice |
| PRC-03, PRC-07 | Line/order discount fields; per-color price via server resolver |
| CASH-02, CASH-04 | POS and invoice payments create PT/PC; change "Trả lại khách: X"; bank transfer recorded as pending verification when the server says so |
| CASH-06 | Implicit shift (decision above) |

## Architecture

```
features/sales   quotes, invoices, stock-outs, sales-documents, returns, exchanges, sell/quote presets, print page
features/pos     PosPage, PosLayout, ScanInput, ProductTile grid, CartPanel, CustomerBar, PaymentMethodPanel (Strategy),
                 SavedCartsTable, usePosHotkeys, pos/offline/* (phase 19 plugs in here)
features/debt    exports PayDialog pieces used for refunds (fund select) — owned by phase 18; phase 16 uses a local
                 FundSelect from shared/ui until then
```

API (phase 09/10 OpenAPI authoritative):

| UI action | Request |
|---|---|
| Tiles / scan | `GET /pos/bootstrap` (tiles, settings, price snapshot) · `GET /search?type=sku&q=&in_stock=&limit=30` (exact barcode/code first; sellable, on-hand by color, combo flag) |
| Line pricing | `POST /pricing/quote` (lines) · `POST /pricing/calculate` (document totals) |
| Carts | `GET /pos/carts?status=open|sold` · `POST /pos/carts` · `PATCH /pos/carts/{id}` (If-Match) · `POST /pos/carts/{id}:copy` · `DELETE /pos/carts/{id}` |
| Checkout | `POST /pos/carts/{id}:checkout` / `POST /pos/checkout` (Idempotency-Key, optional `exception_confirmation_ids[]`) |
| Quotes | `GET/POST /quotes` · `PATCH /quotes/{id}` · `POST /quotes/{id}:send|accept|reject|revise|copy|convert` · `GET /quotes/{id}/history` · `DELETE /quotes/{id}` |
| Invoices | `GET/POST /invoices` · `PATCH /invoices/{id}` · `PUT /invoices/{id}/lines` · `PUT /invoices/{id}/paid-amount` · `POST /invoices/{id}:check|confirm|revise|reserve-more|cancel-remainder|copy` (`:check` = credit-limit/min-price/color dry run) · `GET /invoices/{id}/versions` · `DELETE /invoices/{id}` (draft) |
| Delivery | `POST /invoices/{id}/stock-outs` posts PX + BH in one call (Idempotency-Key) · `GET /stock-outs/{id}` · `POST /stock-outs/{id}:reverse` |
| Payments | `POST /payments` (PT/PC, `fund_account_id`, optional allocations to the sales document / return) · `POST /allocations:suggest` |
| Returns / exchanges | `GET /sales-documents/{id}/returnable-lines` · `POST /sales-returns` (posts) · `POST /exchanges:preview` · `POST /exchanges` (posts) |
| Sale docs | `GET /sales-documents?cursor=` · `GET /sales-documents/{id}` |

## Screens

### `/sales/pos` — POS & giỏ hàng (view 3834 / 2216)

- Lead "Quét mã vạch hoặc gõ mã rồi nhấn Enter. Phím tắt: F2 tìm hàng · F4 lưu giỏ · F9 thanh toán. Giỏ đã bán thành công bị khóa, chỉ sao chép được." with `<kbd>`.
- Acts slot: offline toggle/status is phase 19 (the prototype "Mô phỏng mất mạng" button is a demo device; production shows connection state instead).
- Layout `.pos` grid `.85fr minmax(320px,1.3fr)`; 1 column ≤1100.
- Scan: placeholder "Mã vạch, mã hàng hoặc tên hàng…", aria-label "Tìm hoặc quét hàng", button "Thêm" (navy); Enter adds first match; miss → error toast "Không tìm thấy mã “x”.". F2 focuses it.
- Tiles (max 30): bold name; "price / base unit"; "Có thể bán: N" or "Hết hàng có thể bán" (disabled); combos " · ghép từ thành phần" and "● chọn màu thành phần"; color dots.
- Cart header "Giỏ hàng {GH-id}" / "Giỏ hàng mới"; editing a saved cart shows st "Đang soạn" + "Giỏ mới".
- CustomerBar: "Khách hàng" **name**, small phone + " · hạn mức nợ X · đang nợ Y"; customer picker is the shared Combobox pool `party` customers only (gap #1, replaces native select; aria "Đổi khách hàng"); hint "Nguồn giá: …".
- Cart Cols: Tên sản phẩm / Giá / CK % (aria "Chiết khấu % – {tên}") / ĐVT (select, aria "Đơn vị – {tên}") / SL (aria "Số lượng – {tên}") / Thành tiền / "Xóa". SL 0 removes.
- Color row: "Màu khách lấy:" + `StockColorPicker` + "+ Thêm màu khác"; combo: per-component rows + "+ Thêm combo khác màu".
- Empty "Giỏ hàng trống. Quét mã hoặc chọn hàng bên trái."; total "Khách cần trả" + `.cart-total` 30px.
- `PaymentMethodPanel` strategies: cash "Tiền mặt khách đưa" with chips (total, round up 50k, 100k, 500k; unique, ≤4); transfer "Chuyển khoản / QR". Credit checkbox "Bán chịu phần còn lại X" when owing and a named customer is set. Change callout ok "Trả lại khách: **X**".
- Buttons "Thanh toán · F9" (primary; disabled without lines), "Lưu giỏ · F4" / "Lưu thay đổi · F4".
- Toasts: pay "Đã ghi sổ **HD-xxxx** · X[ · trả lại Y]. Giỏ GH-xxxx đã khóa."; hold "Đã lưu giỏ **GH-xxxx** của {khách}. Giỏ lưu chưa giữ hàng."; sold-cart edit "Giỏ đã bán thành công, không sửa được.".
- Section "Giỏ hàng đã lưu": chips "Đang soạn (n)" (default) / "Đã bán (n)" / "Tất cả"; Cols Giỏ (id + sub "dd/mm/yyyy · người[ · sao chép từ X]") / Khách hàng / Hàng / Tổng tiền / Trạng thái / acts Sửa · Sao chép · Xóa.
- Modal "Xóa giỏ hàng X?": sub "{khách} · n mặt hàng · X. Giỏ chưa bán nên chưa có chứng từ kho hay tiền."; hint "Thao tác được ghi nhật ký."; submit "Xóa giỏ".

### `/sales/quotes` — Báo giá (view 2444)

- Lead "Vòng đời: Nháp → Đã gửi → Khách chấp nhận hoặc từ chối → Chuyển thành hóa đơn. Quá ngày hiệu lực tự chuyển “Hết hạn”. Báo giá không giữ tồn và không tạo doanh thu."; acts "Tạo báo giá".
- Chips "Tất cả (n)" + Nháp, Đã gửi, Khách chấp nhận, Khách từ chối, Hết hạn, Đã chuyển hóa đơn (with counts).
- Cols Số (sub "vN · dd/mm/yyyy") / Khách hàng / Hiệu lực đến / Giá trị / Trạng thái / qActs. qActs: Nháp "Gửi khách" (navy), "Sửa", "Xóa" (danger); Đã gửi "Khách chấp nhận" (navy), "Khách từ chối" (danger), "Phiên bản mới"; Khách chấp nhận "Chuyển thành hóa đơn" (navy); Khách từ chối "Phiên bản mới"; Hết hạn "Gia hạn / phiên bản mới", "Khách từ chối"; Đã chuyển hóa đơn → SO link; all "Sao chép".
- Drawer "Báo giá X": sub "{khách} · phiên bản N · hiệu lực đến dd/mm/yyyy"; lines foot "Tổng cộng"; impact Lãi gộp dự kiến / Giữ tồn "Không" / Doanh thu "Chưa phát sinh"; timeline "Lịch sử trạng thái & phiên bản"; deleted callout "Báo giá đã bị xóa.".
- quoteModal: titles "Tạo báo giá" / "Sửa báo giá nháp X" / "Phiên bản mới cho X" (sub "Hiện tại vN. Phiên bản cũ giữ trong lịch sử."); Khách hàng, Hiệu lực đến (default +10 days, Asia/Saigon), Ghi chú or "Lý do thay đổi (bắt buộc)"; quote preset Cols Tên sản phẩm / Đơn vị tính / Số lượng / Quy cách / Đơn giá / CK / Thành tiền / Giá bán lẻ; mix row "Thùng mix vị"; "+ Thêm sản phẩm", "+ Thêm thùng mix vị" (or hint linking to Danh mục when no active mix group); hint "Đơn giá là giá báo cho khách…"; impact Giá trị báo giá / Số dòng / Giữ tồn "Không"; extra "Lưu nháp"; submit "Lưu & gửi khách" / "Lưu nháp" / "Lưu & gửi phiên bản mới"; toast "Đã lưu **BG-xxxx**.".
- Reject modal "Khách từ chối X": "Lý do khách từ chối", placeholder "VD: giá cao, đã mua nơi khác"; submit "Ghi nhận". Toasts: "Đã chuyển sang “Đã gửi”." · "Đã ghi nhận khách chấp nhận." · "Đã xóa báo giá nháp." · "Đã sao chép thành **X** (nháp)." · "Đã tạo hóa đơn **X** (nháp) từ báo giá. Xác nhận bán để giữ hàng." then open SO drawer.

### `/sales/invoices` — Hóa đơn (view 2323)

- Acts "Tạo hóa đơn". Filters: search "Tìm số hóa đơn, khách hàng…"; status chips Tất cả / Nháp / Chưa xuất đủ / Hoàn tất; payment chips Tất cả / Chưa thanh toán / Một phần / Đã đủ.
- Cols Số hóa đơn / Khách hàng (sub "Nợ N ngày" / "Thanh toán ngay") / Tổng tiền / Đã thanh toán / Còn lại / Thanh toán / Giao hàng / Trạng thái / acts (editable: "Sửa" navy, "Xóa" danger; always "Sao chép"); footer money totals.
- soModal (wide): "Tạo hóa đơn" / "Sửa hóa đơn X"; Khách hàng, Số ngày nợ, Ghi chú / lịch giao; sell preset Cols Tên sản phẩm / Giá / Chiết khấu / Đơn vị tính / Số lượng / Thành tiền + price source + color picker + mix row; payment block "Khách đã thanh toán" (edit: "Khách trả thêm") + Vào quỹ; override checkbox "Tôi xác nhận vẫn bán…" when preview reports a breach; extra "Lưu nháp"; submit "Lưu & xác nhận bán".
- Drawer "Hóa đơn X": sub "{khách} · phiên bản N · đã khóa sửa/xóa" (suffix once posted); acts by state "Xác nhận bán", "Xuất kho giao khách", "Giữ thêm hàng", "Hủy phần chưa giao" (danger), "Ghi nhận thanh toán" (navy), Sửa / Xóa / Sao chép; paybox Tổng tiền / Đã thanh toán / Còn lại / Trạng thái thanh toán; kv; lines with Đã giao / Còn giao; sections "Phiếu xuất kho", "Thanh toán", "Lịch sử"; deleted "Hóa đơn đã bị xóa.".
- soConfirm: no breach → toast "Đã xác nhận và giữ hàng."; breach → exception dialog (phase 14) → toast "Đã xác nhận và giữ hàng. Ngoại lệ đã ghi nhật ký.".
- soTopUp ("Giữ thêm hàng"): direct command, toasts above, drawer stays open.
- soDeposit: title "Ghi nhận thanh toán {id}", sub "Trước khi giao, tiền khách trả là ứng trước. Hệ thống tự trừ vào công nợ khi xuất kho giao và tự tính lại trạng thái thanh toán."; fields "Số tiền khách trả", "Vào quỹ", "Nội dung" (default "Khách thanh toán {id}"); live impact Tổng tiền / Đã thanh toán sau phiếu này / Còn lại / Trạng thái (Chưa thanh toán / Thanh toán một phần / Đã thanh toán đủ / Thừa tiền); submit "Ghi phiếu thu"; toast "Đã ghi **PT-xxxx**.".
- soCancel: title "Hủy phần chưa giao của {id}", sub "Phần đã giao và đã ghi sổ giữ nguyên. Lượng đang giữ được giải phóng."; "Lý do" required; danger submit "Hủy phần còn lại"; toast "Đã hủy phần chưa giao.".
- soDel: title "Xóa hóa đơn {id}?", sub "{khách} · {tổng}. Chỉ xóa được hóa đơn nháp chưa có tiền khách trả.", hint "Thao tác được ghi nhật ký."; submit "Xóa hóa đơn"; toast "Đã xóa hóa đơn nháp.".
- newDLV (wide): "Xuất kho giao khách · {id}"; Cols Hàng / Đã giữ, chưa xuất / Xuất lần này (aria "Xuất lần này – {tên}"); hint "Dòng có “0”…"; impact Giá trị xuất / Kho "Trừ ngay" / Công nợ khách / Trừ tiền trả trước; submit "Ghi sổ xuất kho"; toast "Đã xuất kho PX-xxxx, ghi chứng từ bán BH-xxxx…" then reopen drawer.
- Drawer "Phiếu xuất kho X": kv Khách hàng / Chứng từ bán; Cols Hàng / SL / Lô xuất (FEFO lots with color); impact Phải thu phát sinh / Đã thanh toán / Khách còn nợ.

### `/sales/returns` — Trả hàng (view 4058 / 2479)

- Acts "Đổi hàng" (line), "Tạo phiếu trả" (primary). Returns Cols Phiếu / Chứng từ gốc / Khách / Hàng trả / Lý do / Giá trị / Giảm nợ / Cần hoàn / "Hoàn tiền" when refund due.
- Section "Phiếu đổi hàng": Cols Phiếu / Khách / Chứng từ gốc / Hàng trả / Hàng lấy / Thu / trả khách ("khách trả thêm" / "hoàn khách" / "giảm nợ cũ").
- newReturn (wide): "Tạo phiếu trả hàng"; "Chứng từ bán gốc" Combobox over sale docs (latest first; also "không có chứng từ gốc" path per SAL-09 when the server allows); Lý do: Hàng móp méo khi giao / Sai hàng / Khách đổi ý (trong chính sách) / Hàng gần hết hạn / Lỗi chất lượng; Cols Hàng / Màu (picker "Chọn màu khách trả" or note "Chứng từ cũ chưa ghi màu") / Còn được trả / SL trả; empty "Chứng từ này không còn hàng được trả."; checkbox "Đưa hàng trả vào khu cách ly chờ kiểm tra (bỏ tích nếu hàng còn nguyên, bán lại được ngay)"; impact Giá trị trả (theo giá gốc) / Giảm nợ khách / Khoản cần hoàn / Kho; submit "Ghi sổ trả hàng".
- newExchange (wide): "Đổi hàng"; Chứng từ bán gốc, Lý do (default "Đổi màu / đổi mẫu"), "Thu / trả chênh lệch qua"; "1. Hàng khách trả lại" Cols Hàng / Màu / Giá đã bán / SL trả / "Đổi sang màu khác"; checkbox "Hàng trả bị lỗi, đưa vào khu cách ly…"; "2. Hàng khách lấy đổi" Cols Hàng / SL / Đơn giá (+ nguồn giá) / Thành tiền / Xóa; empty hint "Bấm “Đổi sang màu khác”…"; "+ Thêm hàng khác"; impact Giá trị hàng trả / Giá trị hàng lấy / "Khách trả thêm" or "Cửa hàng trả lại khách" / Giảm nợ cũ của khách; submit "Ghi sổ đổi hàng".
- Refund modal "Ghi phiếu chi": Số tiền, Chi từ quỹ; submit "Ghi phiếu chi".
- Sale-doc drawer (`openSDOC`): acts "Đổi hàng", "Trả hàng" (close drawer, open prefilled dialog), "In phiếu" (new).

## Related files

Create under `apps/web/src/features/`:

- `sales/api/{queries,mutations,adapters}.ts`, `sales/schemas.ts`
- `sales/components/{quote-table,quote-drawer,quote-actions,invoice-table,invoice-drawer,stock-out-drawer,sales-document-drawer,return-table,exchange-table,price-source-badge,mix-pack-row}.tsx`
- `sales/containers/{quote-dialog,quote-reject-dialog,invoice-dialog,invoice-deposit-dialog,invoice-cancel-dialog,invoice-delete-dialog,stock-out-dialog,return-dialog,exchange-dialog,refund-dialog}.tsx`
- `sales/pages/{quotes-page,invoices-page,returns-page,print-sales-document-page}.tsx`, `sales/print.css`, `sales/index.ts`
- `shared/ui/line-items/{sell-preset,quote-preset,color-line-row,combo-color-rows}.tsx`
- `pos/api/{queries,mutations,adapters}.ts`, `pos/components/{pos-layout,scan-input,product-tile,tile-grid,cart-panel,cart-line-row,customer-bar,payment-method-panel,cash-chips,saved-carts-table,shift-fallback-callout}.tsx`, `pos/containers/{pos-cart-container,delete-cart-dialog}.tsx`, `pos/hooks/{use-pos-hotkeys,use-pos-cart}.ts`, `pos/pages/pos-page.tsx`, `pos/index.ts`
- Tests beside each; `e2e/pos.spec.ts`, `e2e/quotes.spec.ts`, `e2e/invoices.spec.ts`, `e2e/returns.spec.ts`

Modify: `src/app/menu.ts`, `src/app/router.tsx` (print route outside AppShell, inside RequireAuth), `src/shared/domain/doc-no.ts` (SO/BG/GH/BH/PX/HD/DH/TH/PT drawers), `src/test/msw/handlers.ts`.

## Implementation steps

1. Query keys: `quoteKeys`, `invoiceKeys`, `stockOutKeys`, `salesDocKeys`, `returnKeys`, `exchangeKeys`, `cartKeys`, `posCatalogKeys`; adapters for statuses (vi labels from the shared tone map) and money.
2. `sell`/`quote` presets: server pricing via debounced `POST /pricing/quote`, `PriceSourceBadge`, color rows, merge rule, mix-pack row with `MixFlavorGrid`, row-context aria labels.
3. Quotes page, drawer, dialogs, transitions.
4. Invoices page, soModal (with preview dry run feeding the inline override checkbox), drawer, confirm/top-up/deposit/cancel/delete, stock-out dialog and drawer.
5. Returns and exchanges, refund dialog, sale-doc drawer, print page (`@media print`, A5/80mm variants, document numbers, totals, no shell).
6. POS: layout, scan (barcode exact match first, then text), tiles (≤30 from server), cart state in a reducer (`use-pos-cart`) with controlled inputs, customer Combobox, payment strategies, credit remainder, change, saved carts CRUD, hotkeys F2/F4/F9 scoped to the page, checkout with one `Idempotency-Key` per checkout intent (kept on retry; new key after success).
7. Shift fallback callout bound to `shift_unavailable` error only.
8. Invalidate stock, debt and dashboard keys after every posting.

## Todo

- [ ] Query keys + adapters (sales, pos)
- [ ] `sell` / `quote` presets incl. color merge and mix-pack row
- [ ] Quotes: page, drawer, quoteModal, reject, transitions, convert
- [ ] Invoices: page, soModal, drawer, soConfirm via exception flow, soTopUp, soDeposit, soCancel, soDel
- [ ] Stock-out dialog + drawer
- [ ] Returns, exchanges, refund, sale-doc drawer
- [ ] Print page
- [ ] POS page: scan, tiles, cart, customer, payments, change, credit, hotkeys, saved carts, delete cart
- [ ] Shift fallback state
- [ ] Unit + e2e tests

## Success criteria & verification

- `pnpm -C apps/web typecheck && pnpm -C apps/web lint && pnpm -C apps/web test` green.
- Unit: `use-pos-cart.test.ts` (SL 0 removes, color merge toasts, cash chips unique ≤4), `payment-method-panel.test.tsx` (change, credit checkbox only with named customer), `invoice-dialog.test.tsx` (override checkbox appears from preview breach; submit blocked until ticked with "Tích “Tôi xác nhận vẫn bán” để tiếp tục."), `quote-actions.test.tsx` (acts per status), `return-dialog.test.tsx` (returnable per color, quarantine flag).
- Playwright `pos.spec.ts`: scan → color pick → cash with change → toast with `HD-` after server 2xx; double-click "Thanh toán · F9" posts once (same key); blur-then-click on SL input still pays; hold cart → edit → sold cart locked; credit sale beyond limit → exception dialog. `invoices.spec.ts`: create → confirm → partial delivery twice → deposit auto-applied → cancel remainder. `quotes.spec.ts`: lifecycle to invoice. `returns.spec.ts`: return with refund, exchange to another color.
- Visual parity vs views 3834, 2444, 2323, 4058 and their primary dialogs/drawers at 1440 and 390.

## Risks

- Pricing round-trips per keystroke → debounce 250ms, batch all lines, keep last response by request id.
- Retrying checkout after a timeout must reuse the key or a second `HD-` appears → key bound to the cart revision; tested.
- Bank transfer confirmation semantics (pending verification) may differ by fund → display server `status` label, never assume paid.

## Security

- Checkout, payments, stock-out, returns, exchanges always send `Idempotency-Key` and `If-Match` where versioned.
- Exception confirmations are created server-side and audited; the client cannot fabricate an id.
- Print page renders server data only; no customer data stored in browser storage (offline cache rules are phase 19).

## Rollback

Revert web image; routes fall back to placeholders. Saved carts and documents remain server-side and unaffected.

## Next

Phase 19 adds the offline outbox, connection banner and sync-exception handling to `features/pos`. Phase 18 reuses `useConfirmException`, payment dialogs and sale-doc drawers from the dashboard and debt screens.
