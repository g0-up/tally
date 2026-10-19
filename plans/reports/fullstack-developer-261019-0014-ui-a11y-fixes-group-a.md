# Sửa UI/UX và a11y — shared/ và nhóm module A

Ngày 2026-10-19, giờ Asia/Saigon (`TZ=Asia/Saigon date` → 261019 0014; chưa sang ngày 261020).

Phạm vi: `apps/web/src/shared/**`, `apps/web/src/app/**`, `features/{dashboard,notifications,pos,sales,procurement,inventory,kits}/**` (gồm trang in), `src/sw.ts`.
Nguồn: [báo cáo review nhóm A](ui-ux-designer-261019-1837-ui-review-group-a.md), [nhóm B](ui-ux-designer-261019-1839-ui-review-group-b.md) (phần shared), phase 20 (quyết định của chủ dự án, ma trận gap).

## 1. Kết quả

Đã sửa toàn bộ finding mức Cao và Trung bình trong phạm vi. Finding mức Thấp: sửa các lỗi rẻ, phần còn lại liệt kê ở mục 3.
Mọi dòng gap thuộc phạm vi đều có test khẳng định (mục 4). Typecheck, lint và test toàn app đều xanh (mục 7). Phần việc lead giao thêm ở mục 10.

## 2. Finding đã sửa

| Nhóm | Finding | Ghi chú |
|---|---|---|
| Shared | S1–S12, S14 | Token tương phản mới đặt sau marker prototype; giá trị prototype giữ nguyên. Có skip link, tiêu đề route, live region luôn gắn, trả focus khi đóng dialog/drawer, toast lỗi bền, cỡ vùng chạm, sidebar dạng link nhóm + chevron riêng. |
| POS | P1–P8 | P8: thêm vùng `role=status` sr-only cạnh tổng "Khách cần trả", debounce 500 ms. Vùng chỉ đọc khi giỏ có hàng. |
| POS offline | O1–O5 | Banner, kết quả sync, callout ngoại lệ dùng wrapper live luôn gắn. Focus về hàng kế tiếp hoặc vùng chính. |
| Bán hàng | B1–B6, B8, B9 | B5 theo quyết định: vùng live polite `m-live`. B9: nút có số hóa đơn. Ô "…" đang tải: ô hóa đơn không còn; ô lô ở drawer phiếu xuất dùng `aria-hidden` + sr-only "Đang tải", giống bảng giỏ đã lưu. |
| Trang in | R1, R2 | `document.title` theo số chứng từ; có nút đóng/quay lại. |
| Nhập hàng | N1–N3 | Hỏi "Bỏ phiếu đang nhập?" theo quyết định. |
| Kho | I1–I5 | |
| Combo/giỏ quà | K1, K2 | Mọi control trong dòng công thức mang "thành phần N". Trạng thái dòng mix nằm trong vùng live polite luôn gắn. |
| Tổng quan | D1, D2 | Vùng status đổi kỳ luôn gắn, nằm ngoài khối `aria-busy`. Nút "Thu tiền/Trả tiền – {tên}". |
| Thông báo | T1, T2 | Tên hành động kèm tiêu đề thông báo. Chỉ nút đang chạy hiện tiến trình. Bị từ chối thì focus về chính nút đó. Xử lý xong thì focus sang thông báo kế tiếp, hoặc vào vùng chính nếu hết. |

## 3. Finding chưa sửa (đều mức Thấp)

| Finding | Lý do |
|---|---|
| S13 — xác nhận ngoại lệ dùng toast | Cần đổi luồng xác nhận dùng chung (`use-confirm-exception`) sang dialog, ảnh hưởng nhiều màn của cả hai nhóm. Không rẻ, nên để phase sau. |
| S15 — `rowHeader` cho DataTable | Phải thêm API cột mới và chọn cột tiêu đề hàng cho từng bảng. Gap 2/12 đã được đáp ứng bằng tên control theo ngữ cảnh hàng. |
| B7 — lỗi kiểm tra hóa đơn báo qua toast | Cần chuyển sang lỗi inline theo từng trường, tức đổi luồng validate của form hóa đơn. |
| I6 — lỗi inline cho phiếu điều chỉnh | Cùng lý do với B7. |
| K3 — kit-dialog dùng `disabled` thay `readOnly` | Đổi sang readOnly kéo theo thay đổi hành vi submit và focus của form combo. Ngoài mức "rẻ". |

## 4. Gap → test khẳng định (phạm vi nhóm A)

Đường dẫn tính từ `apps/web/src/`.

| Gap | Test |
|---|---|
| 1 Combobox chọn khách bằng bàn phím | `features/pos/components/customer-bar.test.tsx` "picks the customer from a searchable list with the keyboard alone" |
| 2 Tên control theo ngữ cảnh hàng | `features/pos/containers/pos-cart-container.test.tsx` "names the cart table and each line's controls after the product". Cùng loại: `line-items-editor.test.tsx` (3 test), buy-preset, new-receipt-dialog, count-drawer, stock-table, return-dialog. `features/kits/containers/kit-dialog.test.tsx` "names every recipe line's controls by its position and announces a mix line going over its room". `dashboard-page.test.tsx` và `notifications-page.test.tsx` (tên "Thu tiền – …", "Mở – {tiêu đề}"). |
| 3 Một tông màu cho mỗi trạng thái | `shared/domain/status-tone.test.ts` |
| 4 Sidebar: link nhóm và chevron riêng | `app/layouts/app-shell.test.tsx` "folds and unfolds a group from its chevron…", "opens another group at its first screen…" |
| 5 Trả focus | `shared/ui/dialog-focus.test.tsx` (về nút mở; "moves focus to %s" khi nút mở mất). Focus trong `offline-callouts.test.tsx` và `sync-exception-drawer.test.tsx`. 3 test focus của `notifications-page.test.tsx`. |
| 6 Lỗi không tự tắt | `shared/ui/toaster.test.tsx`; `shared/hooks/use-commit.test.tsx` "keeps a persistent error toast…" |
| 8 Pill trạng thái không chỉ dựa vào màu | `shared/ui/status-pill.test.tsx` |
| 10 Chọn màu bằng phím mũi tên | `shared/ui/color/color-swatch-group.test.tsx`; `shared/ui/color/stock-color-picker.test.tsx` |
| 11 Dialog giá theo màu | `color-price-dialog.test.tsx` |
| 12 Tên bảng và cột thao tác | `shared/ui/data-table/data-table.test.tsx`; `line-items-editor` "names the table…"; `pos-cart-container`; buy-preset "names the table and its action column…" |
| 13 Date picker bàn phím/nhãn | `shared/ui/date-picker.test.tsx` (4 test) |
| 15 Ô tiền nhận từng phím | `shared/ui/money-input.test.tsx` |
| 16 Ca ngầm định (giữ theo quyết định) | `shift-fallback-callout.test.tsx` |
| 17 Nhãn đang chờ, chỉ báo thành công khi server trả lời | `use-commit.test.tsx` "shows the pending label and announces success only after the server answered" |
| 18 Cỡ vùng chạm | `shared/styles/tokens.test.ts` "gives every small control at least 24 CSS px everywhere", "raises the touch targets to 44 CSS px on narrow screens and touch pointers" |

Gap 7, 9, 14 thuộc uifix-b.

Có thêm `pos-cart-container.test.tsx` "announces the amount due once the cart's total settles", dành cho P8.

Ghi chú gap 18: test cỡ vùng chạm nằm trong `tokens.test.ts`. Trong vitest, `?raw` trên file `.css` trả về chuỗi rỗng. Còn `node:fs` chỉ typecheck được trong file mà `tsconfig.node.json` đã liệt kê, và tsconfig không thuộc phạm vi của tôi.

## 5. Tương phản (trước → sau)

| Cặp | Trước | Sau | Cần |
|---|---|---|---|
| Chữ trắng trên accent-orange → `accent-orange-strong` #cb4d22 | 3.26 | 4.54 | 4.5 |
| Chữ trắng trên accent-crimson → #e22655 | 4.0 | 4.52 | 4.5 |
| Viền control gray-300 → `input-line` #8a8a8a, trên nền trắng | 1.74 | 3.45 | 3 |
| Viền control → `input-line`, trên surface-50 | 1.6 | 3.17 | 3 |
| Placeholder gray-500 → #767676 | 4.48 | 4.54 | 4.5 |
| Ngày ngoài tháng / mũi tên combobox → `muted-strong` #767676 | 4.48 | 4.54 | 4.5 |
| Vòng focus navy-700 trên sidebar navy-700 → `on-dark-focus` #fff | 1.0 | 11.02 | 3 |
| `.bell b` accent-crimson → `bad-ink` | 4.0 | 7.96 | 4.5 |
| `.no` gray-300 → gray-600 | 1.74 | 7.46 | 4.5 |

Các cặp này được khẳng định trong `shared/styles/tokens.test.ts`. Test đó cũng khẳng định token prototype không đổi và giá trị cũ vẫn thiếu.

## 6. Nhãn mới

- Shell: skip link tới nội dung chính, tiêu đề tab theo route, "Mở rộng {nhóm}" / "Thu gọn {nhóm}", cột "Thao tác".
- POS offline: "Gửi lại {mã}", "Xóa {mã} khỏi máy", "Đang gửi…", "Ghi sổ lại {mã}", "Bỏ qua giao dịch {mã}".
- POS: vùng đọc "Khách cần trả {số tiền}" (sr-only).
- Bán hàng: "{nhãn} – dòng N", "Tên hàng – …", "Số lượng – {tên}", "Xóa – …", "Sửa/Xóa/Sao chép {số hóa đơn}", "Đang tải" (sr-only thay cho "…").
- Nhập hàng: "Hàng nhập", "Quản lý lô/hạn – …", "Bỏ phiếu đang nhập?".
- Kho: "Đã cộng 1 – {tên}, thực đếm {n}", "Thực đếm – {tên · màu · lô}", "Số lô – {tên · màu · lô}", "Sửa giá – {tên}", "Thẻ kho – {tên}".
- Combo/giỏ quà:
  - "Thành phần N"; "Màu thành phần N", "Số lượng thành phần N", "Xóa thành phần N";
  - "Chọn nhóm mix vị thành phần N", "Số lượng mix thành phần N", "Đơn vị mix thành phần N", nhóm "Số {gói} theo vị thành phần N".
  - Ô số theo vị lấy tên từ nhãn "{vị} · còn N {gói}".
- Tổng quan: "Thu tiền – {khách}", "Trả tiền – {NCC}".
- Thông báo: "Mở – {tiêu đề}", "Tải về – {tiêu đề}", "Nhắc lại mai – {tiêu đề}", "Đã xử lý – {tiêu đề}", "Đang lưu…".

## 7. Gate (`apps/web`)

Chạy lại sau phần việc bổ sung ở mục 10:

| Lệnh | Kết quả |
|---|---|
| `corepack pnpm typecheck` | 0 lỗi (gồm cả `e2e/shell.spec.ts`) |
| `corepack pnpm lint` | sạch |
| `corepack pnpm test` lần 1 | 155 file / 1089 test, xanh |
| `corepack pnpm test` lần 2 | 155 file / 1089 test, xanh |

Baseline là 132 file / 948 test. Ở lần chạy trước, typecheck và lint toàn app từng đỏ vì `e2e/acceptance/*`; chủ của thư mục đó đã sửa.

Kiểm lại theo gate lead nêu, sau khi uifix-b thêm test:

- `tsc -p tsconfig.app.json --noEmit`: 0 lỗi.
- `eslint src`: sạch.
- Cả hai lần `corepack pnpm test` đều 155 file / 1090 test, xanh.

Trong một lần chạy toàn suite trước đó, `settings/pages/account-page.test.tsx` "says the invitation is being sent while it is on its way and sends it once" đỏ một lần. Test này của uifix-b, viết theo hợp đồng `Button` mới. Sau đó nó xanh 5/5 lần khi chạy riêng và 4/4 lần trong toàn suite. Tôi không bắt được lỗi của lần đỏ đó và đã báo uifix-b. uifix-b nghi focus mở đầu của hộp thoại đến sau cú bấm submit, nên đã sửa test để chờ focus vào hộp thoại trước khi bấm, vẫn giữ assert `toHaveFocus()` chặt.

## 8. Thay đổi API dùng chung (tương thích ngược)

- Thêm vào shared:
  - `useRouteTitle` / `RouteTitle`, `useDocumentTitle`;
  - `useReturnFocus(fallback?)`, `mainFocusTarget()`, `focusLost()`;
  - prop tùy chọn `returnFocusFallback` cho `CommitDialog` và `DetailDrawer`;
  - `ConnectionStatus`;
  - các helper nhãn theo hàng.
- `ReturnColorCell`: thêm prop bắt buộc `name`. Đã sửa mọi nơi gọi.
- Kits: thêm `componentPosition(index)` vào view-model. `MixLineRow` thêm prop `index`.
- Sidebar: `expandGroupLabel` và `collapseGroupLabel` không còn được export, vì luật react-refresh không cho export hàm thường từ file `.tsx`.
- `Button` lúc `pending`: không còn `disabled` gốc mà dùng `aria-disabled="true"`, bỏ qua click và submit (mục 10).
- `CommitDialog`: thêm prop tùy chọn `pendingLabel`, mặc định "Đang ghi sổ…".
- `useFocusFirstInvalid`: chuyển sang `shared/hooks/use-focus-first-invalid.ts`; `features/debt` không còn export hook này.

## 9. Cần cập nhật e2e (không thuộc phạm vi của tôi)

| Spec | Cần đổi |
|---|---|
| `e2e/invoices.spec.ts:62-63`, `e2e/quotes.spec.ts:34-35` | "Số lượng" với `exact: true` → "Số lượng – {tên}" |
| `e2e/returns.spec.ts:97` | "Số lượng" với `exact: true` → tên mới theo hàng |
| `e2e/pos.spec.ts:176`, `e2e/inventory.spec.ts:92`, `e2e/procurement.spec.ts:115`, `e2e/gift-baskets.spec.ts:45` | Vẫn khớp nhờ so khớp chuỗi con hoặc `.last()`; nên rà lại |

## 10. Phần việc bổ sung theo yêu cầu của lead

- **`e2e/shell.spec.ts`** (lead giao thêm quyền sở hữu):
  - Nhóm sidebar giờ tìm bằng link tên nhóm, còn chevron tìm bằng nhãn đúng "Mở rộng {nhóm}" / "Thu gọn {nhóm}".
  - Link màn hình con được tìm bên trong danh sách của nhóm (`#nav-sub-{id}`, là phần chevron điều khiển), vì "Bán hàng" và "Giỏ quà" vừa là tên nhóm vừa là tên màn hình con.
  - Test thu gọn/mở rộng kiểm cả đổi nhãn chevron và danh sách ẩn/hiện.
  - Không chạy e2e; chỉ typecheck và lint.
- **`useFocusFirstInvalid`:**
  - Chuyển lên `shared/hooks/use-focus-first-invalid.ts`, có test riêng (`use-focus-first-invalid.test.tsx`, 2 test).
  - Sửa import ở `debt/field-errors.ts`, `debt/containers/allocate-advance-dialog.tsx`, `crm/containers/customer-dialog.tsx`, `crm/containers/customer-quick-dialog.tsx`, `onboarding/steps/opening-stock-step.tsx` và `opening-debt-step.tsx`.
- **`Button` lúc pending:**
  - Dùng `aria-disabled="true"` cùng `aria-busy` thay cho `disabled`; click bị nuốt bằng `preventDefault`, nên cả submit form (bấm nút hoặc Enter trong ô) cũng không chạy. Nhờ vậy focus ở lại nút đang chạy.
  - `pending` thắng `disabled` của nơi gọi. Vì thế nút đang chạy luôn giữ focus mà không phải sửa từng nơi gọi; các nút khác bị khóa trong lúc chờ vẫn `disabled` gốc.
  - Thêm style `.btn[aria-disabled="true"]` sau marker, giống `.btn[disabled]` của prototype.
  - Test: `shared/ui/button.test.tsx` (4 test).
  - Đã đổi các assert `toBeDisabled()` trên nút đang chạy sang hợp đồng mới: có `aria-disabled`, vẫn giữ focus, và bấm lại không gửi thêm request. Các file đã sửa là `use-commit.test.tsx`, `offline-callouts.test.tsx` (2 chỗ), `notifications-page.test.tsx` (đếm request snooze) và `quote-actions.test.tsx`.
  - Không spec e2e nào assert `toBeDisabled` trên nút đang chờ.
- **`CommitDialog`:** thêm prop `pendingLabel`, truyền xuống nút submit. Test: `shared/ui/commit-dialog.test.tsx` (2 test). Đã báo uifix-b để truyền "Đang gửi…".
- Các lỗi gate lead nêu (product-tile, offline-callouts, line-items-editor, `sidebar.tsx:11-12`) đã được sửa từ trước.

Status: DONE
Summary: Đã sửa toàn bộ finding Cao/Trung bình và phần lớn finding Thấp ở shared/ và nhóm A, cùng phần việc bổ sung (shell spec, chuyển hook, Button pending giữ focus, pendingLabel cho CommitDialog). Mọi gap trong phạm vi đều có test khẳng định. Typecheck và lint toàn app sạch; test xanh 2 lần (155 file / 1089 test).
Concerns/Blockers: Các spec e2e ở mục 9 cần chủ của chúng cập nhật. S13, S15, B7, I6, K3 để lại, lý do ở mục 3. Test owner-transfer của uifix-b từng đỏ một lần, chưa tái hiện được; uifix-b đã gia cố nó (xem mục 7).
