---
phase: 3
title: "Profile, catalog, confirmation and first-sale steps"
status: pending
effort: 1d
---

# Phase 3 — Profile, catalog, confirmation and first-sale steps

## Context

Prototype `OB_BODY.store_profile`, `catalog_setup`, `confirm_opening_balances`, `first_sale`, `A.onbSaveProfile`, `A.onbItem`, `A.onbDone`, `A.onbConfirm`. Every body renders inside the phase 2 `StepFrame` and uses its `cta` variant.

## Requirements

Store profile (`steps/store-profile-step.tsx`):
- `fieldset.onb-fs` "Pháp lý và liên hệ" with `.fg`: Tên pháp lý* (hint "Như trên giấy đăng ký kinh doanh.", maxlength 200, autocomplete organization), Tên hiển thị* (hint "Hiện trên hóa đơn và màn chính."), Mã số thuế (hint "10 số, hoặc 10 số-3 số với chi nhánh.", inputmode numeric), Số điện thoại (tel), Địa chỉ (`.f.wide`, street-address).
- `fieldset.onb-fs` "Ngày chốt số dư đầu kỳ" with `.onb-cut`: Ngày chốt* (max today, disabled once confirmed; hint "Đã khóa cùng số dư đầu kỳ." or "Không sau hôm nay ({today})."), callout "Tồn kho, công nợ và tiền ở các bước sau là số dư **đến hết ngày này**. Thường chọn ngày cuối tháng trước khi bắt đầu dùng phần mềm. Sau khi xác nhận số dư, ngày chốt không sửa được."
- Required mark `<em class="req" aria-hidden="true">*</em>` (extend `Field` with `required` if it lacks it).
- Messages: "Nhập tên pháp lý.", "Nhập tên hiển thị.", "Tối đa 200 ký tự.", "Mã số thuế gồm 10 số, hoặc 10 số-3 số (ví dụ 0312345678-001).", "Chọn ngày chốt số dư đầu kỳ.", "Ngày chốt không được sau hôm nay ({today})." via `useOnbFail`.
- Button "Lưu và tiếp tục" (first time) / "Lưu thay đổi". First save: PATCH, complete the step quietly, toast "Đã lưu thông tin cửa hàng. Số dư đầu kỳ tính đến hết ngày **{cutoff}**.", go to catalog_setup. Later saves: toast "Đã lưu thay đổi.", stay.

Catalog (`steps/catalog-setup-step.tsx`):
- Table (flat) of active SKUs from `useSkuList({ status: "active" })` (first page): Mã (`.doc`), Hàng hóa + `.sub` variant label when present, Đơn vị (base unit), Theo lô (Có/Không from `track_lot`), Giá bán (`retail_price`, money). Empty: `OnbEmpty` "Chưa có hàng hóa nào" / "Thêm vài mặt hàng chính bây giờ để nhập được tồn kho đầu kỳ."
- Buttons: "+ Thêm hàng hóa" (`btn-line` when the list has rows, else CTA) opens `QuickProductDialog`; "Xong, sang tồn kho" (CTA, when rows exist and the step is pending) → complete command, toast "Đã xong **Danh mục hàng hóa**.", go to opening_stock; `.lnk` "Mở danh mục đầy đủ" → `/catalog/products`.

Confirmation (`steps/confirm-opening-step.tsx`):
- Without cutoff and not confirmed: phase 2 gate callout.
- `.onb-sum` with `KeyValueGrid`: Ngày chốt, Giá trị tồn kho, Khách nợ (phải thu), Nợ nhà cung cấp (phải trả), one row per fund, Tổng tiền các quỹ (from `onboarding.totals`; advances dropped per user decision).
- `h3.sec-h` "Tồn kho theo màu" + table Hàng, Màu, Số lượng, Giá trị, grouped by SKU + color from the posted opening stock lines (phase 4 `useOpeningStockLines`); empty "Không có tồn kho đầu kỳ."
- Pending: warn callout "**Còn {n} bước bắt buộc chưa xong:**" + `ul.onb-ul` of `.lnk` to each blocker (union of local blockers and the API's refused step keys, as today).
- Info callout "**Khi xác nhận:**" list: "Tồn kho, công nợ, tiền đầu kỳ và ngày chốt bị khóa, không sửa được." / "Sổ khóa đến hết ngày {cutoff}. Sai lệch sau đó xử lý bằng chứng từ điều chỉnh." / "POS và các màn bán hàng được mở."
- `label.check` checkbox "Tôi đã kiểm tra các số dư trên và đồng ý khóa sổ đến hết ngày chốt."
- `btn-danger` "Xác nhận số dư đầu kỳ", disabled with `aria-describedby="ob-confirm-h"` while not agreed or blocked; hint text "Hoàn tất các bước còn thiếu trước." / "Đánh dấu ô đồng ý để xác nhận." / "Cần nhập lại mật khẩu khi xác nhận."
- Re-auth stays in `useCommit`. `ReauthDialog` (`shared/ui/reauth-dialog.tsx`) takes only `open/onClose/onSubmit` today: add optional `title`/`sub` props (shared copy stays the default) and a matching `reauth: { title, sub }` option on `useCommit`, then pass "Nhập lại mật khẩu để khóa sổ" / "Xác nhận số dư đầu kỳ đến hết ngày {cutoff}. Không hoàn tác được."
- Success: toast "Đã khóa số dư đầu kỳ. POS đã mở.", go to first_sale (not the dashboard).
- Confirmed: ok callout "**Đã xác nhận lúc {completed_at dd/mm/yyyy HH:mm}.** Sổ đã khóa đến hết ngày {cutoff}. POS và các màn bán hàng đã mở." + totals + stock table.

First sale (`steps/first-sale-step.tsx`):
- Completed: ok callout "**Đã có hóa đơn bán đầu tiên.** Cửa hàng đang hoạt động bình thường."
- Not confirmed: callout "**POS mở sau khi xác nhận số dư đầu kỳ.** Bán hàng trước khi khóa sổ sẽ làm lệch tồn kho và tiền đầu kỳ." + `.lnk` "Đến bước Xác nhận".
- Else `ol.onb-check` (3 items verbatim) + CTA button "Mở POS bán hàng" → `/sales/pos`.

## Files

Modify the four step files, `shared/ui/reauth-dialog.tsx` and the `useCommit` hook (optional copy); `shared/ui/field.tsx` only if a `required` mark is missing.

## Validation

`pnpm vitest run src/features/onboarding`, `pnpm typecheck`.
