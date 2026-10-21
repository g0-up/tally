---
phase: 1
title: "Prototype v6.15: unit table (approval gate)"
status: done
priority: P1
effort: 1d
dependencies: []
---

# Phase 1: Prototype v6.15: unit table (approval gate)

## Goal

Build the unit table and the "Sửa đơn vị & giá" editor in `docs/design/prototype-v6.15.html`, strictly inside the prototype's design system, and get the user's approval before any app code changes.

## Context

- Source: `docs/design/prototype-v6.14.html`. It is the design system: `:root` tokens (lines 9–25), buttons `.btn-primary/-navy/-line/-danger/-sm` (74–88), form `.fg/.f/.in/.check/.callout` (142–158), tables, `.hint`, `.lnk`, `.tag`.
- Product model in the prototype: `p.base`, `p.uoms: [{ u, f }]`, `p.price`, `p.whole`. Price lookup `priceFor(sku, custId, color)` (2032) returns the base price; callers multiply by `uomF(sku, uom)` (2031) at 2487 and 3996.
- Create screens: `A.newSKU` (3464, Danh mục), `A.newQuickProd` (2874, Nhập hàng › Danh sách hàng hóa), the purchase-order new-product line (2770: inline base/big/factor inputs), `A.onbItem` (4480, onboarding).
- Lists: `V['danh-muc.hang-hoa']` (3458) and `V['mua-hang.hang-hoa']` (2854) show the "Đơn vị & quy đổi" column. `A.editPrice` (2867, 4000) edits base prices only.

## Requirements

1. **File**: copy v6.14 to `prototype-v6.15.html`; v6.14 stays byte-identical.
2. **Data**: `uoms` entries become `{ u, f, price?, whole? }` (price per unit, absent = auto). `priceFor(sku, custId, color, uom)` returns the unit's own price for the buyer's kind when set, else base price × `f`, and names the source ("Giá lẻ theo dây" / "Giá lẻ × 50"). Replace the `priceFor(...) * uomF(...)` call sites with the new argument so POS, quotes and invoices show the per-unit price.
3. **Shared unit table** `uomTable(d)` used by every create screen and by the editor:
   - Section title "Đơn vị tính" and the hint "Tồn kho lưu theo đơn vị cơ sở. Nhập theo đơn vị nào, chứng từ ghi đúng đơn vị đó và tự quy đổi."
   - Base row: "Đơn vị cơ sở", "Giá bán lẻ", "Giá sỉ".
   - Each extra row: "Tên đơn vị", "=", factor input, base unit name, "Giá bán lẻ", "Giá sỉ", and a remove button with `aria-label="Xóa đơn vị <tên>"`.
   - A blank price shows the computed value as placeholder text ("25.000 tự tính") and stays blank in the data.
   - "+ Thêm đơn vị" as `btn btn-line btn-sm`. The modal's submit button stays the only `btn-primary`.
   - Unit name is a text input with a `<datalist>` of known units. A name not in the directory shows the hint "Đơn vị mới, sẽ thêm vào danh mục đơn vị".
   - Errors under their field (`aria-invalid`, `aria-describedby`): factor must be positive; duplicate unit; a unit cannot equal the base unit.
   - A warning (`.callout.warn`, not blocking) when a unit's retail price is lower than the base unit's retail price.
   - At 390px each row stacks into a bordered block using `.fg`; at 1440px it is a single line.
4. **Create screens**: `A.newSKU`, `A.newQuickProd`, `A.onbItem` replace the "Đơn vị lớn / Hệ số quy đổi" fields with `uomTable`. The purchase-order new-product line keeps its compact inline inputs and gains an "Đơn vị & giá…" `.lnk` that opens `uomTable` for that line; the line's unit select lists every unit of the table.
5. **Editor** `A.editUnits(sku)`: a row action "Đơn vị & giá" on Danh mục › Hàng hóa (goods only, `can('settings')`) opens a modal with `uomTable` prefilled.
   - A unit that cannot be deleted shows its reason and offers "Ngừng dùng" instead of remove: "Đã dùng trên chứng từ" (on `S.po`, `S.sdoc`, receipts, POS lines), "Đã có lịch sử giá" (it ever had its own price) or "Có mã vạch".
   - A factor change on a used unit shows the hint "Chứng từ cũ giữ hệ số cũ."
   - "Ngừng dùng" on a unit with a barcode shows the hint "Mã vạch của đơn vị này sẽ không quét được ở bán hàng và kiểm kê." A factor change on a unit with a barcode shows "Kiểm kê quét mã này sẽ cộng theo hệ số mới."
   - "Ngừng dùng" on the default purchase unit moves the default to the next largest active unit.
6. **Lists**: the "Đơn vị & quy đổi" column shows `1 dây = 50 cái · 24.000` (the price, or "tự tính" in `.hint`).
7. **Kho › Tồn kho** (`V['kho.ton']`, 4031):
   - The name cell's sub line becomes "<code> · ĐV cơ sở: cái · 1 dây = 50 cái · 1 thùng = 500 cái" (active units by factor; unchanged for a single-unit product).
   - "Tồn thực" and "Có thể bán" on the main row keep the base figure and gain a `.sub` line "= 2 thùng 5 dây" from `qMix(sku, q)`, the function behind Thẻ kho's "Quy đổi tự động" (`qShow`, 3775). No line when the product has one unit or the quantity is below the smallest factor (the same test as Thẻ kho's `baseSub`, 3807). Color sub rows keep the base figure only.
   - The "Giá bán" column and the "Sửa giá" button do not change.
   - Thẻ kho (`V['kho.the-kho']`, 3792) needs no change; check its unit chips list the new units.
8. **Demo data**: give one seeded product three units with one explicit unit price and stock above the largest factor, so the reviewer sees the auto/explicit price and the converted stock (e.g. `BQ454`: hộp, lốc = 6 hộp with its own price, thùng = 12 hộp auto, 40 hộp on hand → "40" with "= 3 thùng 4 hộp").

## Tasks & Steps

- [x] Copy v6.14 to v6.15; bump any visible version label.
- [x] Extend the `uoms` shape, `priceFor`, and its multiply-by-factor call sites.
- [x] Add CSS for the unit table under a new `/* ---------- đơn vị tính & giá theo đơn vị ---------- */` block, using tokens and existing classes only.
- [x] Implement `uomTable`, its validation and its draft bindings (`data-b`, the "ghi vào nháp, không vẽ lại khi gõ" pattern at 4434).
- [x] Wire it into the 3 create screens and the purchase-order line.
- [x] Add `A.editUnits` and the list row action; update both list columns.
- [x] Add the unit line and the converted quantity to `V['kho.ton']`, reusing `qMix` rather than a second conversion function.
- [x] Seed the demo product.
- [x] Self-check in a browser at 1440px and 390px: keyboard-only flow (Tab order, Enter submits, Escape closes), focus moves to the first invalid field, no horizontal scroll at 390px.
- [x] Present it to the user: the file path, the screens to open (`#danh-muc.hang-hoa` → Thêm hàng hóa / Đơn vị & giá, `#mua-hang.hang-hoa`, `#ban-hang.pos` with the demo product, `#kho.ton` and `#kho.the-kho`), and 1440px/390px screenshots.

## Verification

- `grep -c 'đơn vị tính & giá theo đơn vị' docs/design/prototype-v6.15.html` returns 1; `git diff --stat docs/design/prototype-v6.14.html` is empty.
- Review the new CSS block line by line: every color, radius, shadow, spacing and font comes from a `var(--…)` token or an existing class; no hex, `rgb()` or `font-family` literal.
- The prototype opens from disk with no console errors.
- **Gate:** the user approves the prototype. Feedback rounds update v6.15 only. Phases 2–5 do not start before approval.

## Risks

- `priceFor` has 15 call sites; missing one leaves a screen pricing a unit as base × factor. Grep every `priceFor(` and `uomF(` after the change.
- Commit `prototype-v6.15.html` together with the first app phase, as the onboarding plan did, so the source and the port land together.
