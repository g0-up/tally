---
title: "Phase 15: Web catalog & procurement"
status: in-progress
priority: P1
effort: 7d
dependsOn: [phase-14-web-foundation-design-system, phase-05-catalog-partners, phase-06-pricing, phase-08-procurement]
---

# Phase 15 — Web catalog & procurement

## Context links

- UI spec §5.9 Danh mục (5.9.1–5.9.4), §5.3 Nhập hàng (5.3.1–5.3.4), §6.2 rules (price validity, receiving tolerance, supplier stop/delete), §7 doc prefixes PO/PN/XT/PC
- Prototype `docs/design/prototype-v6.14.html`: views 3337, 3425, 3353, 3574, 2701, 2733, 2756, 2774; `saveMixGroup` 3524–3549; `editPrice` 3879; `buyEditor`; `supQuick`; `priceLogTable`
- Schema: `design/database-schema-catalog-pricing.md`, `design/database-schema-org-partners.md`, `design/database-schema-inventory-procurement.md`
- Phase 14 shared kit (Combobox, LineItemsEditor base, CommitDialog, DetailDrawer, ExceptionConfirmDialog, useCommit)

## Overview

Delivers the eight routes of Danh mục and Nhập hàng with prototype-identical markup: products, suppliers, read-only price lists, mix groups, purchase orders, the purchasing product list with sell-price editing, receipts, and supplier returns. Introduces the `buy` preset of `LineItemsEditor` and the price-edit dialog that phase 17 reuses from Tồn kho.

## Key insights

- Two places edit sell prices (PRC-08): `/purchasing/products` row act "Sửa giá bán" and Tồn kho (phase 17). Both use one `EditPriceDialog` exported from `features/pricing`; the server records `price_changes` with the source ("Nơi sửa").
- `buyEditor` lets an unknown product name create a new product inline (CAT-11). The UI sends new-product payloads inside the PO/receipt command; the server creates products atomically and returns ids. Impact row "Hàng hóa mới sẽ tạo" counts them client-side.
- Min price is server-owned (prototype demo used cost × 1.08). The dialog shows the server's `min_price` and warns; it never computes it.
- Supplier rules ("Ngừng" blocked by open POs, "Xóa" blocked by documents) are server errors; the UI shows them as error toasts and also pre-disables using `can_stop` / `can_delete` flags from the list DTO.
- Price lists are read-only in the prototype (no modals). Keep it read-only; editing is out of this phase (see Concerns in report).
- Document photos on receipts and supplier documents (PUR-05) use the shared attachment flow (`POST /attachments` with `owner_type` `receipt` or `supplier_document` → presigned PUT → `:complete`; limits 10 MB, jpeg/png/webp/pdf). Receipt and supplier-document detail responses already list clean attachments; the screens add the upload control and the download link (`GET /attachments/{id}/url`).
- Supplier-document matching compares the invoice with its receipts plus DG only; purchase returns are matched through their credit notes, so the UI shows returned value separately. An XT settled as replacement credits the payable now; the replacement goods arrive on a new PN at the original price.
- Mix-group validation messages are verbatim server error messages mapped by `code`; the client repeats only the cheap checks (required fields, integers) for instant feedback using identical wording.

## Requirements

| PRD ID | Behaviour |
|---|---|
| CAT-01, CAT-02, CAT-03 | `/catalog/products` list + newSKU dialog (code, name, group, barcode, base/large unit + factor, prices, reference cost, min stock, lot tracking) |
| CAT-04 | Attribute section in product dialog rendered through `AttributeFieldRegistry` from the product's attribute set (`text→Input`, `number→NumberInput + unit_label`, `boolean→Switch`, `date→DatePicker`, `select→Combobox`, `multiselect→MultiCombobox`) |
| CAT-05 | `/catalog/suppliers`: add, edit, stop, reopen, delete; search by name/phone/code/contact/item; dual-role checkbox |
| CAT-08 | All supplier/product pickers use the shared Combobox (`party`, `buy` pools); only active suppliers in PO picker (PUR-02) |
| CAT-10 | `/catalog/mix-groups` list + mixModal with verbatim validation; "Ngừng dùng" / "Xóa" behind confirm dialog (gap #9) |
| CAT-11 | Inline new product in buyEditor and "Thêm hàng hóa mới" on `/purchasing/products`; duplicate-name block |
| PRC-01, PRC-02 | `/catalog/price-lists` read-only: list priority/tiers/scope/validity + "Giá theo hàng" |
| PRC-07, PRC-08 | `EditPriceDialog` "Lưu giá" with color-price awareness and price history |
| PUR-02, PUR-11 | PO list/create/confirm ("Chốt đơn" → `:confirm`), overdue flag "Quá hạn giao", progress meter, close short "Đóng đơn" → "Đóng – thiếu" |
| PUR-03, PUR-04, PUR-05, PUR-06 | Receipt by PO or "Nhập nhanh", multi-receipt, 5% over-tolerance impact, lot + expiry column, color per line, freight/handling allocation |
| PUR-07 | "Ứng trước NCC" (poAdvance) → payment PC |
| PUR-08 | "Đối chiếu HĐ" (rcvInvoice) "Lưu đối chiếu" |
| PUR-09 | `/purchasing/supplier-returns` newPRT by receipt lot, "Ghi sổ xuất trả"; refund-due row "NCC cần hoàn" |
| PUR-10 | Receipt drawer shows "Giá vốn sau phân bổ"; final-price adjustment shown as a document in the receipt's settlements (server-produced) |

## Architecture

```
features/catalog      products, mix-groups, price-lists (read), attribute registry
features/partners     suppliers (party role supplier), supQuick dialog (exported)
features/pricing      EditPriceDialog, PriceHistoryTable, price-list queries (exported to inventory)
features/procurement  purchase-orders, purchasing-products, receipts, purchase-returns, buy LineItemsEditor preset
```

API (paths follow architecture §3.6; OpenAPI from phases 05/06/08 is authoritative):

| UI action | Request |
|---|---|
| Products list / create / edit | `GET /skus?q=&category_id=&cursor=` · `POST /products` · `PATCH /products/{id}` (If-Match) · `POST /products/{id}/variants:preview` · `POST /products/{id}/variants` |
| Mix groups | `GET/POST /mix-groups` · `PATCH /mix-groups/{id}` · `POST /mix-groups/{id}:deactivate|reactivate` · `DELETE /mix-groups/{id}` (unused only) |
| Price lists | `GET /price-lists` · `GET /price-lists/{id}/items` |
| Sell price edit / history | `POST /skus/{id}/sale-price` (Idempotency-Key, body `{kind, color_id?, apply_all_colors?, price, source}`, `source: purchasing_list|stock|po_line`) · `GET /skus/{id}/price-history` |
| Suppliers | `GET /suppliers?status=&q=` · `POST /suppliers` · `PATCH /suppliers/{id}` · `POST /suppliers/{id}:stop|reopen` · `DELETE /suppliers/{id}` (unused only) · picker `GET /search?type=supplier&q=` |
| PO | `GET/POST /purchase-orders` · `PATCH /purchase-orders/{id}` (draft, If-Match) · `PUT /purchase-orders/{id}/lines` · `POST /purchase-orders/{id}:confirm|close-short` · `GET /purchase-orders/{id}/receivable-lines` |
| Supplier advance | `POST /payments` (PC, `purchase_order_id` earmark, Idempotency-Key) |
| Receipt | `POST /receipts` posts in one call (Idempotency-Key; unsaved form state is kept client-side, no server draft) · `POST /receipts/{id}:reverse` · `POST /receipts/{id}/landed-costs` · `POST /supplier-documents` · `POST /supplier-documents/{id}/receipts` |
| Supplier return | `POST /purchase-returns` posts in one call (Idempotency-Key) |

## Screens

### `/catalog/products` — Hàng hóa (view 3337)

- Acts "Thêm hàng hóa" (newSKU), "Tạo giỏ / combo" (opens `KitDialog` from `features/kits` — phase 17 export; until then the button is hidden behind the kits entitlement).
- Cols: Mã · mã vạch / Tên hàng / Loại (tag Hàng hóa / Vật tư / Giỏ đóng sẵn / Combo ảo; + "mix vị") / Đơn vị & quy đổi / Lô/hạn ("Bắt buộc" / "Không") / Giá lẻ / Giá sỉ / Giá vốn BQ / Giá tối thiểu / Tồn tối thiểu / st.
- newSKU: fields Mã SKU, Tên, Nhóm, Mã vạch, ĐV cơ sở, ĐV lớn, Hệ số (hint "Phải là số dương"), Giá bán lẻ, Giá sỉ, Giá mua tham khảo, Tồn tối thiểu, lot-tracking checkbox; attribute section from registry; submit "Lưu". Errors verbatim: "Cần mã và tên hàng." / "Mã SKU đã tồn tại trong cửa hàng." / "Mã vạch đã được dùng cho hàng khác." / "Hệ số quy đổi phải dương.".

### `/catalog/suppliers` — Nhà cung cấp (view 3425)

- Acts "Thêm nhà cung cấp". Search "Tìm tên, mã, số điện thoại, người liên hệ, mặt hàng…"; chips "Đang giao dịch (n)" / "Ngừng giao dịch (n)" / "Tất cả".
- Cols: Nhà cung cấp (tag "kiêm khách hàng", sub MST) / Liên hệ / Mặt hàng cung cấp / Điều khoản / Đơn nhập (sub last receipt) / Cửa hàng còn nợ / Trạng thái / acts. Active: Sửa, Tạo đơn nhập, Ngừng, Xóa. Stopped: Sửa, Mở lại. Empty "Chưa có nhà cung cấp phù hợp.".
- supModal: title "Thêm nhà cung cấp" / "Sửa nhà cung cấp · X"; fields "Tên nhà cung cấp *", Người liên hệ, Số điện thoại, Mã số thuế, Địa chỉ, Số ngày nợ được phép (hint "0 = trả ngay…"), Mặt hàng cung cấp, Tài khoản ngân hàng, Ghi chú, dual-role checkbox; submit "Thêm nhà cung cấp" / "Lưu". zod: name required, phone `/^[0-9 .+()-]{8,20}$/`, terms 0–365; duplicate name/phone from server `fields`. supDel confirm submit "Xóa". Codes `NCC###` are server-issued.

### `/catalog/price-lists` — Bảng giá (view 3353)

- Table 1 Cols: Ưu tiên / Bảng giá (sub "Bậc thang" for tiers) / Áp dụng / Hiệu lực ("không thời hạn").
- Section "Giá theo hàng" Cols: Hàng / Giá lẻ / Giá sỉ / doanh nghiệp / Giá tối thiểu (cảnh báo nếu bán thấp hơn) / Giá vốn BQ. No modals.

### `/catalog/mix-groups` — Nhóm mix vị (view 3574)

- Acts "Thêm nhóm mix". Cols: Nhóm mix / Quy cách / Các vị · tồn có thể bán / Giá lẻ / thùng / Đang dùng trên / Trạng thái / acts as `Button size=sm` "Sửa", "Ngừng dùng" or "Dùng lại", "Xóa" (both destructive acts open a confirm dialog — gap #9). Empty "Chưa có nhóm mix vị nào. Bấm “Thêm nhóm mix”.".
- mixModal (wide): Tên nhóm mix, Đơn vị thùng, Số đơn vị lẻ / thùng, Đơn vị chọn vị, Số đơn vị lẻ / đơn vị chọn vị (read-only with explanation once used, gap #11). Cols Mặt hàng (vị) / Tên vị ngắn / Đơn vị lẻ / Giá lẻ / Có thể bán / Gỡ (blocked with "đã bán"). "+ Thêm vị". Submit "Tạo nhóm" / "Lưu". Live callouts and server errors verbatim: "Cần tên nhóm mix." · "Tên nhóm mix đã tồn tại." · "Cần đơn vị thùng và đơn vị chọn vị (VD: thùng / dây)." · "Số đơn vị lẻ trong 1 {part} phải là số nguyên dương." · "Số đơn vị lẻ trong 1 {pack} phải là số nguyên dương." · "1 {pack} ({packF}) phải chia hết cho 1 {part} ({partF})." · "1 {pack} phải gồm ít nhất 2 {part} thì mới mix được." · "Một mặt hàng đang được chọn hai lần." · "Nhóm mix cần ít nhất 2 vị." · "{name} không phải hàng hóa bán lẻ." · "Các vị phải cùng đơn vị lẻ, đang lẫn: {bases}." · "Cần tên vị ngắn cho {name}." · "Nhóm đã dùng trên chứng từ nên không đổi được quy cách thùng. Tạo nhóm mới nếu quy cách khác." · "Không gỡ được vị đã dùng trên chứng từ: {names}.".

### `/purchasing/orders` — Đơn nhập (view 2701)

- Acts "Tạo đơn nhập". Search "Tìm số đơn, nhà cung cấp…". Cols: Đơn nhập / Nhà cung cấp / Dự kiến giao (+ bad "Quá hạn giao") / Giá trị / Tiến độ nhận (meter 120px + "r / t đơn vị cơ sở") / Trạng thái (API `approved` displays "Đã chốt") / row act "Chốt đơn" (draft) or "Nhận hàng" (open).
- newPO (wide): title "Tạo đơn nhập"; Nhà cung cấp (Combobox, active only) + "+ NCC mới" (supQuick); Kho nhận hidden (oneStore); Ngày dự kiến giao (DatePicker). buyEditor Cols: Tên sản phẩm / Đơn vị tính / Số lượng / Quy cách / Đơn giá / Thành tiền / Giá bán lẻ; unknown name → inline new product row (CAT-11 fields: name, base unit, factor, cost, sell price). Impact: Giá trị đơn nhập / Hàng hóa mới sẽ tạo / Tồn / nợ NCC "Chưa phát sinh". Extra "Lưu nháp"; submit "Lưu & chốt đơn".
- supQuick: title "Thêm nhà cung cấp mới"; supplier fields; extra "Quay lại phiếu"; submit "Thêm & chọn" (returns to newPO with selection; PO draft state survives).
- Drawer PO: acts Nhận hàng / Ứng trước NCC / Đóng phần còn thiếu / Chốt đơn by state; lines incl. Đã nhận / Còn chờ; sections "Phiếu nhận & nợ NCC", "Ứng trước NCC", "Lịch sử".
- poAdvance: "Ứng trước cho X"; Số tiền, Chi từ quỹ; submit "Ghi phiếu chi". poClose: "Đóng phần còn thiếu của X"; Lý do; submit "Đóng đơn".

### `/purchasing/products` — Danh sách hàng hóa (view 2733)

- Acts "Thêm hàng hóa mới" (newQuickProd: Tên, Nhóm, ĐV cơ sở, ĐV lớn, Hệ số, Giá nhập, Giá lẻ, Giá sỉ, lot checkbox; submit "Thêm"). Search + group chips.
- Cols: Mã · barcode / Tên hàng (+ color breakdown `ColorQtyPill`s, CAT-11) / Đơn vị & quy đổi / Có thể bán / Giá nhập gần nhất / Giá vốn BQ / Giá bán lẻ / Giá sỉ / Biên lẻ / row act "Sửa giá bán".
- Section "Lịch sử thay đổi giá bán" (`PriceHistoryTable`): Ngày / Hàng / Giá lẻ (old → new) / Giá sỉ / Nơi sửa / Người sửa.
- `EditPriceDialog` (view 3879): Giá bán lẻ / Giá sỉ / Ghi chú; when color prices exist checkbox "Áp dụng cho tất cả màu (bỏ giá riêng…)"; impact incl. Giá tối thiểu; warn callout when sỉ > lẻ or below min; embedded history; submit "Lưu giá".

### `/purchasing/receipts` — Nhận hàng (view 2756)

- Acts "Nhập nhanh" + one button per open PO "Nhận theo X · NCC". Cols: Phiếu nhận / Nguồn (PO link or tag "Nhập nhanh") / NCC / Hóa đơn NCC (number or act "Đối chiếu HĐ") / Giá trị hàng / Chi phí phân bổ / Còn phải trả.
- newRCV: title "Nhận hàng theo X" / "Nhập nhanh"; "Số hóa đơn NCC (nếu có)", "Chi phí vận chuyển/bốc xếp"; buyEditor + "Lô · hạn dùng" column for lot-tracked SKUs + color swatch per line (PUR-05); impact Nợ NCC tăng / Chi phí mua vào giá vốn / Hàng hóa mới sẽ tạo / Vượt dung sai 5%; submit "Ghi sổ nhập kho". Over-tolerance returns `ExceptionRequired` → shared exception dialog.
- rcvInvoice: "Đối chiếu hóa đơn NCC cho X"; submit "Lưu đối chiếu".
- Drawer receipt: act "Trả NCC từ phiếu này"; lines with Lô · hạn and Giá vốn sau phân bổ; impact Nợ NCC phát sinh / Đã trả / giảm / Còn phải trả / Hạn trả; settlements table.

### `/purchasing/supplier-returns` — Trả NCC (view 2774)

- h1 "Trả nhà cung cấp"; lead "Xuất trả đúng lô đã nhận; giảm nợ NCC. Nếu đã trả tiền nhiều hơn số còn nợ, phần chênh ghi thành khoản NCC cần hoàn."; acts "Tạo phiếu trả NCC".
- Cols: Phiếu / Phiếu nhận gốc / NCC / Hàng · lô / Lý do / Giá trị / Giảm nợ / NCC cần hoàn.
- newPRT (wide): "Trả hàng nhà cung cấp"; Phiếu nhận gốc (Combobox over receipts), Lý do (default "Hàng lỗi bao bì"); Cols Hàng / Lô / Tồn lô tại kho / SL trả (aria "SL trả – {tên hàng}", gap #2); impact Giá trị trả / Giảm nợ NCC / NCC cần hoàn; submit "Ghi sổ xuất trả".

## Related files

Create under `apps/web/src/features/`:

- `catalog/api/{queries,mutations,adapters}.ts`, `catalog/schemas.ts`, `catalog/attribute-field-registry.tsx`, `catalog/components/{product-table,product-form,mix-group-table,mix-group-form,mix-flavor-rows,price-list-table,price-by-sku-table}.tsx`, `catalog/containers/*`, `catalog/pages/{products-page,price-lists-page,mix-groups-page}.tsx`, `catalog/mix-group-messages.ts`, `catalog/index.ts`
- `partners/api/*`, `partners/schemas.ts`, `partners/components/{supplier-table,supplier-form}.tsx`, `partners/containers/{supplier-dialog,supplier-quick-dialog}.tsx`, `partners/pages/suppliers-page.tsx`, `partners/index.ts`
- `pricing/api/*`, `pricing/components/{edit-price-dialog,price-history-table}.tsx`, `pricing/index.ts`
- `procurement/api/*`, `procurement/schemas.ts`, `procurement/components/{po-table,po-drawer,receipt-table,receipt-drawer,purchase-return-table,purchasing-product-table}.tsx`, `procurement/containers/{new-po-dialog,po-advance-dialog,po-close-dialog,new-receipt-dialog,receipt-invoice-dialog,new-purchase-return-dialog,new-quick-product-dialog}.tsx`, `procurement/pages/{orders-page,products-page,receipts-page,supplier-returns-page}.tsx`, `procurement/index.ts`
- `shared/ui/line-items/buy-preset.tsx` (+ `line-items-editor.tsx` base if phase 14 left only the shell)
- Tests next to each container; `e2e/catalog.spec.ts`, `e2e/procurement.spec.ts`

Modify: `src/app/menu.ts` (swap placeholders for real pages), `src/test/msw/handlers.ts`, `src/shared/domain/doc-no.ts` (register PO/PN/XT drawers).

## Implementation steps

1. Query-key factories: `skuKeys`, `mixGroupKeys`, `priceListKeys`, `priceChangeKeys`, `supplierKeys`, `poKeys`, `receiptKeys`, `purchaseReturnKeys`; adapters map decimal strings to `Money`/`Qty`, statuses to vi labels (`approved` → "Đã chốt", `closed_short` → "Đóng – thiếu").
2. Attribute registry + product form; supplier form + quick dialog (draft survives the nested dialog via lifted form state).
3. Mix groups: list, form with live client checks (same strings as server), confirm dialogs for deactivate/delete.
4. Price lists read view; `EditPriceDialog` + `PriceHistoryTable` exported from `features/pricing/index.ts`.
5. `buy` preset of `LineItemsEditor` (cols, inline new product, per-line `ColorSwatchGroup` when SKU tracks color, lot/expiry column for receipts) with row-context aria labels.
6. PO list/create/confirm/drawer/advance/close; receipts (by PO and quick), invoice matching, drawer; supplier returns.
7. Register PO/PN/XT/PC drawer openers in the `doc-no` registry so any `DocNo` link opens the right drawer.
8. Wire every submit through `useCommit` with the server toast (e.g. "Đã ghi sổ **PN-0007**…") and invalidate `skuKeys` + `poKeys` + debt keys on postings.

## Todo

- [x] Query keys + adapters for catalog, partners, pricing, procurement
- [x] Products page + newSKU + attribute registry
- [x] Suppliers page + supModal + supQuick + supDel + stop/reopen
- [x] Price lists read-only page
- [x] Mix groups page + mixModal + confirm dialogs
- [x] `EditPriceDialog`, `PriceHistoryTable` (exported)
- [x] `buy` LineItemsEditor preset
- [x] Orders page + newPO + drawer + poAdvance + poClose
- [x] Purchasing products page + newQuickProd
- [x] Receipts page + newRCV + rcvInvoice + drawer
- [x] Supplier returns page + newPRT
- [ ] Doc-no registry entries; unit + e2e tests

Evidence (2026-10-18): unit tests in `apps/web/src/features/{catalog,partners,pricing,procurement}/**`;
`corepack pnpm typecheck`, `lint` and `test` green in `apps/web` (43 files, 309 tests, run together
with phase 16). Playwright `e2e/catalog.spec.ts` and `e2e/procurement.spec.ts` are written but not run:
they need the local stack and `E2E_OWNER_PASSWORD`. The production bundle step was not run in this
session. Report: `plans/reports/fullstack-developer-261018-web-catalog-procurement.md`.

Review fixes (2026-10-18, after `plans/reports/code-reviewer-261018-0927-web-catalog-procurement-sales-review.md`):
`POST /purchase-orders`, `/supplier-documents` and `/skus/{id}/sale-price` take no Idempotency-Key, so
"Lưu & chốt đơn" and receipt posting remember the stored document and each finished follow-up step
(`runFollowUps` in `shared/hooks/use-commit.ts`); a failed follow-up says "Đã ghi … nhưng chưa …" and a
retry continues from that step without posting the document twice (`new-receipt-dialog.test.tsx`).

Idempotency (2026-10-18): the three POSTs above now take an optional Idempotency-Key and the web sends
one per intent (`shared/hooks/create-once.ts`, report `plans/reports/fullstack-developer-261018-1620-web-idempotency-keys.md`). Edits made after a lost
create answer cannot be applied to a purchase order or a matched supplier invoice (no update API); the web
says so with the document number.

Open:
- Receiving over the 5% tolerance is blocked on the client; the spec expects the exception dialog.
  Needs a product decision and a server exception kind.
- ~~The `PC` opener belongs to the cash screens (phase 18).~~ Closed 2026-10-19: PT/PC open from `GET /payments/{id}` through the doc-no registry (report `plans/reports/fullstack-developer-261019-0242-web-adopt-api-gaps.md`).
- A shared `shared/ui/line-items` base was not extracted; the buy and sell presets are self-contained.

## Success criteria & verification

- `pnpm -C apps/web typecheck && pnpm -C apps/web lint && pnpm -C apps/web test` green.
- Unit: `product-form.test.tsx` (four verbatim errors from API `fields`), `supplier-form.test.tsx` (phone regex, terms 0–365), `mix-group-form.test.tsx` (each of the 14 messages rendered for its API code; packF % partF client check), `edit-price-dialog.test.tsx` (sỉ > lẻ warns; color checkbox), `buy-preset.test.tsx` (inline new product, lot column only for lot-tracked).
- Playwright `procurement.spec.ts`: create PO with a new product → "Lưu & chốt đơn" → receive partially with lot/expiry/color → over 5% shows exception dialog → close short → return one lot to supplier; assert stock and AP via API. `catalog.spec.ts`: supplier stop blocked with open PO; mix group create/edit/deactivate with confirm.
- Visual parity vs prototype views 3337, 3425, 3353, 3574, 2701, 2733, 2756, 2774 (list states + primary dialog) at 1440 and 390 using the phase 20 thresholds.

## Risks

- Inline product creation inside PO/receipt spans two aggregates → server must create products within the posting transaction; UI must not create products first (orphans on cancel).
- Large supplier/product pickers → server search via Combobox adapter.
- Color per receipt line vs lot: one lot may carry one color; enforce in UI by splitting lines per color.

## Security

- Supplier bank account and tax id are tenant data; never logged in the browser console; forms send only changed fields with `If-Match`.
- Price-change endpoints require `Idempotency-Key`; audit is server-side (SEC-04).
- No client-side min-price computation that could be trusted.

## Rollback

Revert web image; pages fall back to the phase 14 placeholder through `menu.ts`. No data effects beyond API calls already audited server-side.

## Next

Phase 16 reuses `EditPriceDialog` price sources and `Combobox` pools for selling; phase 17 mounts `EditPriceDialog` and color-price editing in Tồn kho and consumes mix groups in kit recipes.
