---
title: "Phase 17: Web inventory & gift baskets"
status: in-progress
priority: P1
effort: 7d
dependsOn: [phase-14-web-foundation-design-system, phase-07-inventory-ledger, phase-11-kits-assembly]
---

# Phase 17 — Web inventory & gift baskets

## Context links

- UI spec §5.4 Kho (5.4.1–5.4.6), §5.5 Giỏ quà (5.5.1–5.5.4), §5.3.2 editPrice, §6.2 rules (FEFO by color, avail, combo avail), §7 prefixes KK/DC/LD/TG/LOT, §8 gaps #2 (row-context input labels), #10 (swatch roving), #11 (colorPrice readOnly), #12 (table captions)
- Prototype views 3910 (Tồn kho), 2804, 2816 (`drawCount`), 2830, 2840 (`newADJ`), 3671 (Thẻ kho, `qMix`), 2845 (`kitModal`, `kitPreview`, `recipeVer`, `comboEdit`, `kitDel`), 2975 (`newASM`, `asmDone`, `openASM`), 2989, 2997 (`newDIS`)
- Schema: `design/database-schema-inventory-procurement.md` §1 ledger (lots, stock_moves, lot_balances, sku_stock, reservations), §2 counts/adjustments; `design/database-schema-sales-kits.md` §6 kits and assembly
- PRD INV-01..12, KIT-01..10, CAT-05 (color prices), PRC-01 (price edit), SEC-03 (owner exceptions)

## Overview

Builds the six Kho routes and four Giỏ quà routes against the inventory ledger and kits APIs: stock with per-color subrows and color prices, reservations release, blind counts, lots and expiry with quarantine decisions, decrease adjustments, the stock card, recipe/combo versioning, assembly orders with FEFO consumption, progress and lot genealogy, and disassembly. Exports `KitDialog` so the catalog products page (phase 15) can create kits from there.

## Key insights

- Every quantity on screen is a server projection (`sku_stock.on_hand / sellable / reserved / quarantine_qty / expired_qty`, `lot_balances`). The client never sums ledger rows; the stock card reconciliation badge comes from the API.
- Reservations are per SKU, not per color (OPEN-17); color subrows show on-hand/sellable per color but "giữ N" only on the parent row.
- Disposal, quarantine release and count variances require an `exception_confirmations` row bound to the document version (schema posting rule). These reuse phase 14 `ExceptionConfirmDialog` with props: "Tiêu hủy lô hết hạn", "Đạt – cho bán", "Xác nhận & ghi sổ chênh lệch".
- Blind count: the server omits `system_qty` while `status = counting`; the UI shows "Ẩn khi đếm mù" because the value is absent, not because it hides it.
- FEFO is the picking order, not a block (Lô & hạn lead "Soạn hàng theo FEFO (hết hạn trước, xuất trước). FEFO là thứ tự lấy hàng, không ph…"); expired lots are blocked by the server when `block_expired_lot_sale` is on.
- Kits have stock and go through assembly; combos have none and are composed at sale (KIT-03). Recipe versions are immutable once active; changes create "Tạo phiên bản mới" with mandatory reason.
- Assembly completion is one atomic posting: consume components FEFO + receive finished lot; kit expiry = earliest component expiry (asmDone sub, KIT-07).
- `qMix` formatting ("1 thùng 3 dây 2 hộp") lives in `shared/domain/qty.ts` from phase 14 and is used by the stock card in "Quy đổi tự động" mode.

## Requirements

| PRD ID | Behaviour |
|---|---|
| INV-01, INV-12 | `/inventory/stock` Tồn thực / Có thể bán (sub "giữ N · cách ly N"), color subrows, filters, Giá trị tồn |
| CAT-05, PRC-01 | "Sửa giá" → `EditPriceDialog` (from phase 15); color "Sửa giá" → colorPrice "Giá bán {màu} · X", "Lưu giá màu", checkbox "Bỏ giá riêng, dùng giá chung…" |
| INV-03 | `/inventory/reservations` list + "Giải phóng hàng giữ" with Lý do |
| INV-05 | `/inventory/counts` "Bắt đầu kiểm kê" (Nhóm hàng, default "Toàn kho"), blind drawer, "Hoàn tất đếm", "Xác nhận & ghi sổ chênh lệch" (owner exception), "Đếm lại" |
| INV-07, INV-11 | `/inventory/lots` chips, statuses, "Đạt – cho bán" / "Không đạt – hủy" / "Tiêu hủy" ("Ghi sổ tiêu hủy") / "Xả hàng" → `/campaigns/seasons` |
| INV-06 | `/inventory/adjustments` newADJ "Điều chỉnh giảm kho" (Hỏng / Hao hụt / Biếu tặng / Dùng nội bộ), "Lý do (bắt buộc)", Doanh thu "Không phát sinh" |
| INV-08 | `/inventory/stock-card` filters, KPIs, reconciliation badge, rows with lot and color, "Cộng kỳ" |
| KIT-01, KIT-03, KIT-09 | `/gift-baskets/recipes` cards, kitModal, preview drawer, publish, versions, combo edit, draft delete |
| KIT-04, KIT-05 | `/gift-baskets/assembly-orders` newASM (shortage table), "Chốt & giữ NL", stepper drawer |
| KIT-02, KIT-07 | asmDone "Ghi nhận hoàn thành {id}" → "Ghi sổ hoàn thành", toast "Đã nhập giỏ thành phẩm." |
| KIT-08, KIT-10 | `/gift-baskets/progress` demand by order, finished lots, component lot trace |
| KIT-06 | `/gift-baskets/disassembly` newDIS "Tháo giỏ quà", damaged packaging checkbox, "Ghi sổ tháo giỏ" |

## Architecture

```
features/inventory  stock, reservations, counts, lots, adjustments, stock-card
                    exports: StockColorPicker data hook (useSellableColors), StockCardLink
features/kits       recipes, combos, assembly, progress, disassembly
                    exports: KitDialog (catalog products page), KitPreviewDrawer
reuses              features/catalog EditPriceDialog, shared/ui ExceptionConfirmDialog, Combobox, ColorChip,
                    StockColorPicker, MixFlavorGrid, Meter, Stepper, ImpactPanel, ReasonField
```

API (phase 07/11 OpenAPI authoritative):

| UI action | Request |
|---|---|
| Stock list | `GET /stock?q=&category_id=&color_id=&colored_only=&below_min=&cursor=` (parent rows + color subrows, price range) · `GET /stock/{skuId}` |
| Color price | `POST /skus/{id}/sale-price` (`kind`, `color_id`, `source: stock`) · "Bỏ giá riêng" = the same `POST` with `price: null` (OpenAPI contract; falls back to the any-color price) |
| Reservations | `GET /reservations?status=active` · `POST /reservations/{id}:release` |
| Counts | `GET/POST /stock-counts` · `GET /stock-counts/{id}` · `PUT /stock-counts/{id}/lines` · `POST /stock-counts/{id}:finish|recount|post` |
| Lots | `GET /lots?state=all|expiring|expired|quarantine&expiring_within=90` · `GET /lots/{id}/trace` · `POST /stock-adjustments` (kind `quarantine_release` or `disposal`, with `exception_confirmation_id`) |
| Adjustments | `GET /stock-adjustments` · `POST /stock-adjustments` (kind damage/loss/gift/internal_use) |
| Stock card | `GET /stock-card?sku_id=&color_id=&from=&to=&unit_mode=auto|fixed&unit_id=` |
| Recipes | `GET /kits?status=` · `POST /kits` · `GET/PATCH /kits/{productId}` (draft) · `POST /kits/{productId}:publish` · `POST /kits/{productId}/recipes` (new version) · `DELETE /kits/{productId}` (draft, never sold) · `POST /kits:preview` (unsaved recipe economics) · `GET /kits/{productId}/preview` (card) · `POST /kits:suggest` |
| Assembly | `GET/POST /assembly-orders` · `POST /assembly-orders:preview` (shortage) · `GET /assembly-orders/{id}` (stepper) · `POST /assembly-orders/{id}:reserve|close` · `POST /assembly-orders/{id}/substitutions` · `POST /assembly-orders/{id}/postings` (complete batch) · `GET /assembly-orders/{id}/label` |
| Progress | `GET /kits/progress` (planned/completed/disassembled + demand from open invoices) · `GET /lots?kind=kit` · `GET /lots/{id}/genealogy` |
| Disassembly | `GET /disassemblies` · `POST /assembly-orders/{id}/disassemblies` |

## Screens

### `/inventory/stock` — Tồn kho (3910)

- Lead "Hàng có màu hiện số tồn từng màu…"; acts "Điều chỉnh kho" (opens newADJ).
- Filters: search "Tìm mã, tên, mã vạch…"; group chips; color chips "Mọi màu" / "Màu (n)" / "Chưa gán màu"; scope chips "Tất cả hàng" / "Chỉ hàng có màu"; "Mở tất cả màu" / "Thu gọn tất cả".
- Cols Hàng (`ExpandableRow`, aria "Xem chi tiết/Thu gọn màu của X", inline color-qty pills) / Tồn thực / Có thể bán (sub "giữ N · cách ly N") / Giá bán (sub "sỉ X"; "giá chung mọi màu" or "N màu giá riêng" + min–max) / Giá trị tồn / acts "Sửa giá" + "Thẻ kho" (→ `/inventory/stock-card?sku=`).
- Color subrows: ColorChip + name, qty, price sub "giá riêng" / "theo giá chung", "Sửa giá".
- Footer "N mặt hàng · N có màu"; hint "Combo ghép tại quầy không có tồn riêng…".
- colorPrice modal: title "Giá bán {màu} · X"; Giá lẻ / Giá sỉ / Ghi chú; checkbox "Bỏ giá riêng, dùng giá chung…" makes the price inputs `readOnly` with a visible note that the common price applies (gap #11: prototype used `disabled`, which drops tab order and contrast) and saving sends `price: null`; impact; submit "Lưu giá màu".

### `/inventory/reservations` — Giữ hàng (2804)

- Cols Nguồn (doc link via `DocLink`) / Hàng / Lượng giữ / Từ ngày / "Giải phóng". Empty "Không có lượng giữ.".
- Modal "Giải phóng hàng giữ": "Lý do" required; submit "Giải phóng".

### `/inventory/counts` — Kiểm kê (2816)

- Acts "Bắt đầu phiên kiểm kê". Cols Phiên (sub cutoff time, Asia/Saigon) / Số mã / Đã đếm / Dòng lệch / Trạng thái / "Nhập số đếm" (counting) or "Xem & ghi sổ".
- newCount: "Bắt đầu kiểm kê"; Nhóm hàng select (default "Toàn kho"); submit "Bắt đầu".
- Drawer: Cols Hàng (with color, lot when tracked) / Số hệ thống ("Ẩn khi đếm mù" while counting) / Thực đếm (numeric input, aria "Thực đếm – {tên}") / Chênh lệch / Giá trị lệch; buttons "Hoàn tất đếm" / "Xác nhận & ghi sổ chênh lệch" (opens exception dialog listing variance lines) / "Đếm lại". Line saves are debounced `PUT` batches with If-Match; scanner input adds +1 to the matching line.

### `/inventory/lots` — Lô & hạn dùng (2830)

- Lead starts "Soạn hàng theo FEFO (hết hạn trước, xuất trước). FEFO là thứ tự lấy hàng, không ph…" (verbatim from prototype).
- Chips Tất cả lô / Hết hạn trong 90 ngày / Đã hết hạn / Khu cách ly.
- Cols Hàng / Lô / Hạn dùng / Còn (days or bad "Quá N ngày") / SL / Tình trạng ("Cách ly – chờ kiểm tra", "Hết hạn – chặn bán", "Sắp hết hạn", "Bán được") / acts.
- Acts: quarantine "Đạt – cho bán" (exception dialog → release) and "Không đạt – hủy" (disposal); expired "Tiêu hủy" → modal "Tiêu hủy lô hết hạn" submit "Ghi sổ tiêu hủy" (then exception confirmation); near expiry "Xả hàng" → `/campaigns/seasons`.

### `/inventory/adjustments` — Điều chỉnh kho (2840)

- Acts "Tạo điều chỉnh". Cols Phiếu / Loại / Hàng / SL / Lý do / Giá trị / Trạng thái.
- newADJ: "Điều chỉnh giảm kho"; Loại Hỏng / Hao hụt / Biếu tặng / Dùng nội bộ; Hàng (Combobox pool stock) + `StockColorPicker`; Số lượng; "Lý do (bắt buộc)" (reason catalog when phase 05 exposes one, free text fallback); impact Tồn bán được / Giá trị ghi giảm / Doanh thu "Không phát sinh"; submit "Ghi sổ điều chỉnh".

### `/inventory/stock-card` — Thẻ kho (3671)

- Filters: product Combobox (pool stock), Từ / Đến dates, range chips Hôm nay / 7 ngày / Tháng này / Tháng trước / Từ đầu, color chips, unit chips "Quy đổi tự động" / "Theo {u} ({f} base)". Filter state lives in the URL query.
- KPIs (22px values) Tồn đầu kỳ / Nhập trong kỳ / Xuất trong kỳ / Tồn cuối kỳ; badge "Đã khớp sổ và tồn" (ok) or "Lệch: thẻ kho X, tồn thực Y" (bad).
- Cols Ngày / Chứng từ (DocLink) / Loại / Lô / Màu / Nhập / Xuất / Tồn / Đơn giá vốn; first row "Tồn đầu kỳ"; footer "Cộng kỳ"; empty "Không có biến động trong khoảng…". Cursor pagination for long ranges.

### `/gift-baskets/recipes` — Giỏ quà & combo (2845)

- Acts "Tạo giỏ / combo"; chips Tất cả / Nháp / Đang bán (counts); `.g2` cards.
- Card: "name · vN"; st + tag "Giỏ đóng sẵn" / "Combo ghép khi bán"; component table; impact Giá bán giỏ / Tổng giá bán lẻ thành phần / Khách tiết kiệm / Lãi gộp dự kiến. Draft buttons Xem trước / Sửa nháp / Phát hành / Xóa nháp; active Tạo lệnh đóng (kits) / Tạo phiên bản mới / Sửa thành phần (combos).
- kitModal (wide): "Tạo giỏ quà / combo" / "Sửa nháp · X"; Loại "Giỏ đóng sẵn (có tồn, qua lệnh đóng)" / "Combo ghép khi bán (không có tồn)"; Mã, Tên, Giá bán lẻ, Giá sỉ / doanh nghiệp, Tồn tối thiểu (giỏ); component editor (`LineItemsEditor` preset `recipe`) Cols Thành phần / SL / giỏ / Giá bán / Thành tiền (giá bán) / Giá vốn / Có thể bán; "+ Thêm thành phần", "+ Thêm thùng mix vị" (hint linking to Danh mục when no active mix group); "Chi phí đóng gói / giỏ…"; Ghi chú; live impact from `POST /kits/preview-economics` with warn callouts (price above component retail total; margin below 15%); extra "Lưu nháp"; submit "Lưu & phát hành" / "Lưu nháp".
- kitPreview drawer: customer-facing `preview-card` + "Thông tin nội bộ".
- recipeVer modal: "Lý do thay đổi (bắt buộc)"; submit "Lưu phiên bản". comboEdit modal submit "Lưu". kitDel modal submit "Xóa nháp".

### `/gift-baskets/assembly-orders` — Lệnh đóng giỏ (2975)

- Acts "Tạo lệnh đóng" (primary); card button opens it prefilled.
- Cols Lệnh / Giỏ / Kế hoạch / Hoàn thành / Tiến độ (`Meter`) / Trạng thái (sub "Thiếu: …", bad "Thiếu") / row act "Chốt & giữ NL" (Nháp) or "Ghi nhận hoàn thành" (Đang đóng).
- newASM: Công thức, Kho đóng (hidden when the tenant has one warehouse), Số giỏ kế hoạch, "Gắn đơn bán (tùy chọn)" (Combobox of open invoices); live table Nguyên liệu / Cần / Có thể bán / Thiếu or "Đủ" from preview; submit "Tạo lệnh".
- asmDone: title "Ghi nhận hoàn thành {id}"; sub "Xuất nguyên liệu theo FEFO và nhập giỏ thành phẩm trong cùng một lần ghi sổ. Hạn bán của giỏ = hạn sớm nhất của thành phần."; field "Số giỏ hoàn thành (còn N)"; impact Nguyên liệu xuất / Giỏ nhập kho / Giá vốn / giỏ (dự kiến); submit "Ghi sổ hoàn thành"; toast "Đã nhập giỏ thành phẩm.".
- Drawer: Stepper Nháp / Chốt & giữ NL / Soạn NL / Đóng giỏ / QC / Nhập kho; genealogy cards (component lots → finished lot).

### `/gift-baskets/progress` — Tiến độ & truy xuất lô (2989)

- Card "Nhu cầu giỏ theo đơn" Cols Đơn / Đặt / Đã giao / Đã giữ / Cần đóng thêm; card "Tồn giỏ thành phẩm theo lô"; table "Truy xuất lô thành phần" (finished lot → component lots, linking to stock card).

### `/gift-baskets/disassembly` — Tháo giỏ (2997)

- Acts "Tháo giỏ" (primary). Cols Phiếu / Giỏ / SL / Vật tư bao bì / Giá trị.
- newDIS: "Tháo giỏ quà"; finished lot select; checkbox "Giỏ mây, nơ bị hỏng khi tháo…" (damaged lines go to quarantine, never sellable); submit "Ghi sổ tháo giỏ".

## Related files

Create under `apps/web/src/features/`:

- `inventory/api/{queries,mutations,adapters}.ts`, `inventory/schemas.ts`
- `inventory/components/{stock-table,stock-color-subrow,stock-filters,reservation-table,count-table,count-drawer,lot-table,adjustment-table,stock-card-filters,stock-card-kpis,stock-card-table}.tsx`
- `inventory/containers/{color-price-dialog,release-reservation-dialog,new-count-dialog,dispose-lot-dialog,adjustment-dialog}.tsx`
- `inventory/pages/{stock-page,reservations-page,counts-page,lots-page,adjustments-page,stock-card-page}.tsx`, `inventory/index.ts`
- `kits/api/{queries,mutations,adapters}.ts`, `kits/schemas.ts`
- `kits/components/{recipe-card,recipe-economics,kit-preview-drawer,assembly-table,assembly-drawer,shortage-table,demand-card,finished-lots-card,genealogy-table,disassembly-table}.tsx`
- `kits/containers/{kit-dialog,recipe-version-dialog,combo-edit-dialog,kit-delete-dialog,assembly-dialog,assembly-complete-dialog,disassembly-dialog}.tsx`
- `kits/pages/{recipes-page,assembly-orders-page,progress-page,disassembly-page}.tsx`, `kits/index.ts`
- `shared/ui/line-items/recipe-preset.tsx`
- Tests beside each; `e2e/inventory.spec.ts`, `e2e/gift-baskets.spec.ts`

Modify: `src/app/router.tsx` (10 routes), `src/features/catalog/pages/products-page.tsx` (wire `KitDialog` import only), `src/shared/domain/doc-no.ts` (KK/DC/LD/TG drawers), `src/test/msw/handlers.ts`.

## Implementation steps

1. Query keys `stockKeys`, `reservationKeys`, `countKeys`, `lotKeys`, `adjustmentKeys`, `stockCardKeys`, `kitKeys`, `assemblyKeys`, `disassemblyKeys`; adapters for lot status and assembly status labels.
2. Stock page with expandable color subrows, filters in URL, color price dialog, EditPriceDialog wiring.
3. Reservations and release; adjustments dialog with reason field.
4. Counts: start dialog, drawer with debounced line saves, complete/recount, post via exception dialog.
5. Lots: chips, statuses, quarantine release/dispose via exception dialog, "Xả hàng" link.
6. Stock card: filters, KPIs, `qMix` units, pagination.
7. Recipes: cards, kit dialog with recipe preset and economics preview, publish/version/combo edit/delete, preview drawer; export `KitDialog`.
8. Assembly: create with shortage preview, reserve, complete (atomic posting), stepper drawer and genealogy.
9. Progress and disassembly.
10. Invalidate `stockKeys`, `lotKeys`, `stockCardKeys`, dashboard and notifications after every posting.

## Todo

- [x] Query keys + adapters (inventory, kits)
- [x] Stock page + color subrows + colorPrice + EditPriceDialog
- [x] Reservations + release
- [x] Counts (blind) + post with exception confirmation
- [x] Lots + quarantine/disposal flows
- [x] Adjustments dialog
- [x] Stock card with qMix and reconciliation badge
- [x] Recipes: cards, KitDialog, preview, versions, combo edit, delete
- [x] Assembly: create, reserve, complete, drawer
- [x] Progress + genealogy
- [x] Disassembly
- [ ] Unit + e2e tests (unit done; e2e written, not run)

Evidence (2026-10-18): unit tests in `apps/web/src/features/{inventory,kits}/**`; in `apps/web`,
`corepack pnpm typecheck`, `lint` and `test` green (76 files, 492 tests, run together with phases 15,
16 and 19). Playwright `e2e/inventory.spec.ts` and `e2e/gift-baskets.spec.ts` are written but not run:
they need the local stack and `E2E_OWNER_PASSWORD`. Report:
`plans/reports/fullstack-developer-261018-0927-web-inventory-kits.md`.

Review fixes (2026-10-18, after `plans/reports/code-reviewer-261018-1110-web-inventory-kits-review.md`):
- An assembly order asks for the color of every component whose recipe leaves the color open and sends
  `colors`; completing an older order can also pick colors.
- A selling kit with a newer draft recipe shows the selling version plus a callout to preview, edit or
  publish the draft.
- Multi-step saves remember each finished step, so a retry sends the user's latest edits and never
  posts a document twice.
- Each row action has its own Idempotency-Key.
- The color price dialog sends a wholesale price only when one is entered.
- Inventory and kit error codes are translated.
- A mix component whose group was deactivated is shown with a warning and blocks saving.
- Color filters run on the server, row details load on view, and documents opened by `?doc=` are found by `q`.

Open (API gaps):
- ~~`POST /kits` and `/assembly-orders` take no Idempotency-Key.~~ Closed 2026-10-18: the server takes an optional key and the web sends one per intent through `shared/hooks/create-once.ts` (report `plans/reports/fullstack-developer-261018-1620-web-idempotency-keys.md`, review `plans/reports/code-reviewer-261018-1700-web-idempotency-keys-review.md`). Edits made after a lost create answer cannot be applied to assembly orders or to a kit's kind, code or base unit; the web says so with the document number.
- ~~Count and adjustment lists carry no line summary and no `doc_no` filter; `/disassemblies` has no `q`; stock cannot filter "no color"; `KitRequest` has no wholesale price or minimum stock; reservation release takes no reason.~~ Closed 2026-10-19: the web reads the list summaries, filters by `doc_no` and `uncolored=true`, searches disassemblies by `q`, sends `wholesale_price`/`min_stock` and a release reason (report `plans/reports/fullstack-developer-261019-0242-web-adopt-api-gaps.md`).

## Success criteria & verification

- `pnpm -C apps/web typecheck && pnpm -C apps/web lint && pnpm -C apps/web test` green.
- Unit: `stock-table.test.tsx` (expand/collapse aria, "giữ N · cách ly N"), `color-price-dialog.test.tsx` (checkbox makes inputs readOnly, keeps them focusable, sends `price: null`), `count-drawer.test.tsx` ("Ẩn khi đếm mù" when `system_qty` absent), `lot-table.test.tsx` (acts per status), `kit-dialog.test.tsx` (warn callouts from economics, mix-pack component), `assembly-complete-dialog.test.tsx` (max = remaining).
- Playwright `inventory.spec.ts`: blind count → post variance with exception confirmation → stock card shows the count move and "Đã khớp sổ và tồn"; expired lot disposal; quarantine release; decrease adjustment shows Doanh thu "Không phát sinh". `gift-baskets.spec.ts`: create kit → publish → assembly with shortage shown → reserve → partial completion → finished lot appears in progress trace → disassembly with damaged packaging.
- Visual parity vs views 3910, 2804, 2816, 2830, 2840, 3671, 2845, 2975, 2989, 2997 and kitModal/asmDone/drawCount at 1440 and 390.

## Risks

- Large stock lists with color subrows → server pagination; subrows fetched with the parent page, not per row.
- Count drafts edited from two devices → If-Match on line batches; 412 shows the shared reload toast.
- Recipe economics preview chatter → debounce 300ms, cancel stale requests.

## Security

- Disposal, quarantine release and count posting require server-issued exception confirmations; audit entries are server-side.
- Cost columns (Giá vốn, Giá trị tồn) are visible to the owner only role in v1 (SEC-01 single owner), no client gating needed beyond session.
- All postings send `Idempotency-Key`.

## Rollback

Revert web image; inventory and kits routes fall back to placeholders. Ledger data is unaffected.

## Next

Phase 18 links "Xả hàng" into campaigns and reads kits figures for the Giỏ quà report. Phase 20 audits these screens.
