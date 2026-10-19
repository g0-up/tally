---
phase: 4
title: "Opening stock, debt and cash steps"
status: pending
effort: 1d
---

# Phase 4 — Opening stock, debt and cash steps

## Context

Prototype `OB_BODY.opening_stock`, `opening_debt`, `opening_cash`, `A.onbPostStock`, `A.onbAddLine`, `A.onbDelLine`, `A.onbNone`, `A.onbDir`, `A.onbParty`, `A.onbPostDebt`, `A.onbSaveCash`, and the `.onb-tbl.onb-stack` mobile table CSS. Bodies render inside `StepFrame` and use the phase 2 gate, locked callout, `OnbEmpty`, `useOnbFail`.

## Requirements

Queries (`api/queries.ts`):
- `useOpeningStockLines()`: `GET /stock-adjustments?kind=opening&status=posted` (first page) then `GET /stock-adjustments/{id}` per document; flatten lines (sku_code, name, variant_label, color_id → color label via `useColorDirectory`, qty, unit_cost, lot_no, expiry_date). Invalidated by `usePostOpeningStock`.
- `useOpeningDebts()`: `GET /debt-adjustments` (first page) filtered to `kind === "opening_balance"`; rows carry `party_name`, `reason_name`. Invalidated by `usePostOpeningDebt` (already invalidates `debtKeys.all`; add the new key).

Opening stock (`steps/opening-stock-step.tsx`):
- Order: confirmed → locked callout + posted table; no cutoff → gate; no active SKU → `OnbEmpty` "Chưa có hàng hóa để nhập tồn" / "Tồn kho đầu kỳ cần hàng hóa trong danh mục. Thêm mặt hàng ngay tại đây hoặc ở bước Danh mục." with CTA "+ Thêm hàng hóa" and (nothing posted yet) `btn-line` "Không có tồn kho đầu kỳ", then the posted table.
- Entry table `.tw.flat > table.t.onb-tbl.onb-stack`, headers Hàng · Màu | Số lượng | Giá vốn (₫) | Số lô · Hạn dùng | Thành tiền | sr-only "Xóa dòng"; each `td[data-l]` for the stacked mobile layout.
  - Cell 1 `.onb-lot`: SKU `Combobox` (non-combo filter kept, cost prefill kept) + error, then the color select once a SKU is picked.
  - Qty `QtyInput.in.num`, cost `MoneyInput.in.num`, each with its error under it.
  - Lot cell `.onb-lot`: lot input (track_lot) + date (track_expiry), errors under each; else hint "Không theo lô".
  - Amount cell updates live; delete `btn btn-line onb-x` "×" only when more than one line, `aria-label="Xóa dòng {n}"`, toast "Đã xóa dòng {n}.".
  - tfoot "Tổng giá trị dòng đang nhập" + total.
- Rows: `btn-line btn-sm` "+ Thêm dòng" (focus the new SKU input), `.lnk` "+ Hàng hóa mới" (QuickProductDialog; put the new SKU on the first empty line).
- Actions: CTA "Ghi tồn kho đầu kỳ"; `btn-line` "Không có tồn kho đầu kỳ" while nothing posted.
- Per-field validation replacing the single problem callout: blank lines ignored; none filled → "Chọn hàng cho ít nhất một dòng." on line 1; "Chọn hàng.", "Số lượng phải lớn hơn 0.", "Nhập giá vốn (0 nếu hàng tặng).", "Nhập số lô.", "Chọn hạn dùng."; via `useOnbFail`.
- Success: reset to one empty line, complete quietly, toast "Đã ghi **{n} dòng** tồn kho đầu kỳ. Có thể ghi thêm trước khi khóa sổ." (keep the doc number if the toast helper allows both).
- Posted section: `h3.sec-h` "Đã ghi tồn đầu kỳ <small class="hint">{n} dòng</small>" + table Hàng | Màu | Số lượng | Giá vốn | Số lô · Hạn dùng | Thành tiền, tfoot "Tổng giá trị tồn đầu kỳ".
- "Không có …" (stock and debt): `CommitDialog` title "Xác nhận không có tồn kho đầu kỳ?" / "…công nợ đầu kỳ?", sub "Số dư đầu kỳ của mục này sẽ là 0 khi khóa sổ. Trước khi xác nhận vẫn có thể ghi thêm.", callout "Đánh dấu bước **{label}** là đã xong với số dư 0.", submit "Đúng, không có …" → complete command, toast "Đã ghi nhận không có …", go to next step.

Opening debt (`steps/opening-debt-step.tsx`):
- Locked / gate as above.
- `.onb-seg[role=group][aria-label="Chiều công nợ"]` of two `.chip` buttons with `aria-pressed`: "Khách hàng nợ cửa hàng" / "Cửa hàng nợ nhà cung cấp" (replaces `ToggleGroup`). Switching keeps amount, due date and note, clears party and errors.
- `.fg`: party field (Khách hàng* / Nhà cung cấp*, Combobox, hint = `.lnk` "+ Khách hàng mới" / "+ Nhà cung cấp mới" opening the quick dialogs), Số tiền nợ (₫)*, Lý do (reason select), Hạn thanh toán (hint "Để trống nếu chưa hẹn."), Ghi chú (`.f.wide`, textarea rows 2).
- Messages "Chọn khách hàng." / "Chọn nhà cung cấp.", "Số tiền nợ phải lớn hơn 0."; via `useOnbFail`.
- Actions: CTA "Ghi công nợ đầu kỳ"; `btn-line` "Không có công nợ đầu kỳ" while none posted.
- Success: reset form (same direction), complete quietly, toast "Đã ghi công nợ đầu kỳ **{amount}**. Ghi tiếp khoản khác hoặc sang bước sau."
- Posted: `h3.sec-h` "Đã ghi công nợ đầu kỳ <small>{n} khoản</small>" + table Chiều (tag "Khách nợ" / tag alt "Nợ NCC") | Đối tác | Lý do (+ note sub) | Hạn | Số tiền, then `KeyValueGrid` "Khách nợ cửa hàng (phải thu)" / "Cửa hàng nợ NCC (phải trả)".

Opening cash (`steps/opening-cash-step.tsx`):
- Confirmed: locked callout + table Quỹ (+ kind sub) | Số dư đầu kỳ, tfoot "Tổng tiền các quỹ".
- Else table `.onb-tbl.onb-stack`: Quỹ (label for the input: bold name + kind sub) | Đã lưu ("Chưa lưu" hint when the step is pending and the fund has no opening_date, else money) | Số dư đầu kỳ (₫) input placeholder "0"; tfoot live total.
- One CTA "Lưu số dư các quỹ" + hint "Lưu tất cả quỹ một lần. Ô để trống được tính là 0 ₫." Saves every fund in sequence (blank = 0) with `useSaveFundOpening` under each fund's version; then completes the step and goes to confirm. Toast "Đã lưu số dư {n} quỹ.". On a failure stop, toast which fund failed, keep entered values.
- Invalid: "Nhập số tiền từ 0 trở lên." under the fund input (MoneyInput already rejects negatives; keep the check for paste).
- Remove the per-row save buttons.

## Files

Modify the three step files, `api/queries.ts`, `api/mutations.ts` (invalidation keys, bulk fund save helper).

## Validation

`pnpm vitest run src/features/onboarding`, `pnpm typecheck`; manual check of the stacked table at 390px.

## Risk

Stock detail fetches are N+1; acceptable for a new store (one or a few opening documents). If the first page has `next_cursor`, show a hint "Chỉ hiện các chứng từ gần nhất." rather than paging.
