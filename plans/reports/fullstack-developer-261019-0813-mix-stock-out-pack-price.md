# Thùng mix/combo: xuất kho nguyên thùng, đơn giá thùng, số phần nguyên (web)

Nguồn: plans/reports/code-reviewer-261019-0800-mix-incomplete-review.md, các mục H1, M1, L3. Chỉ sửa phía web; không đụng Go.

## Kết quả

Đã sửa cả ba mục. Typecheck và lint sạch. Toàn bộ test web pass: 156 file, 1109 test. e2e không cần đổi.

## H1. Hộp thoại xuất kho không còn gửi dòng con

- `apps/web/src/features/sales/stock-out-lines.ts`
  - `deliverRows` trả về `StockOutRow = DeliverRow | WholeRow`.
  - Dòng con (`parent_line_id` khác null) không bao giờ thành dòng riêng. Thùng mix và combo được gom thành một `WholeRow` theo dòng đầu, với `cap` là phần còn lại của dòng đầu (base) và `parts` là các vị hoặc thành phần còn phải giao.
  - `short` lặp lại `ready()` của server: chỉ xét dòng con giữ hàng mà còn phải giao, và lượng giữ phải bằng đúng lượng còn lại (so ở đơn vị base). Dòng đầu không còn dòng con nào giữ hàng thì không có dòng.
  - Dòng đã giao hoặc hủy hết thì không hiện.
- `apps/web/src/features/sales/containers/stock-out-dialog.tsx`
  - Dòng nguyên thùng hiện tên dòng đầu và nhãn "Thùng mix"/"Combo", kèm một dòng phụ chỉ đọc "Vị × số đơn vị · …".
  - Cột số lượng là một checkbox "Nguyên N Thùng" với tên truy cập `Xuất lần này – {tên}`, được tích sẵn khi đã giữ đủ.
  - Khi chưa giữ đủ, checkbox bị khóa, và lý do "Chưa giữ đủ hàng: Kẹo dừa giữ 1/2 Gói" vừa hiện ở cột đã giữ, vừa được gắn qua `aria-describedby`.
  - Khi gửi, chỉ có `{ invoice_line_id: <dòng đầu>, base_qty: <còn lại của dòng đầu> }`.
  - Giá trị xuất tính theo `line_amount` của dòng đầu.

## M1. Ô "đơn giá / thùng" khớp với giá server lưu và in

- `apps/web/src/features/sales/components/mix-pack-row.tsx`
  - Khi đã chọn vị, giá là gross trước chiết khấu chia cho số thùng, làm tròn như tiền.
    - Nguồn chính là `pricing.grossAmount(key)`, chính là `gross_amount` của calculator, bằng `gross(includeTax)` của server, kể cả cơ sở thuế và làm tròn chứng từ.
    - Khi chưa có kết quả tính, giá lấy Σ giá vị × dây × units_per_part.
  - Khi chưa chọn vị, giữ mặc định PRD: giá trung bình × units_per_pack.
  - Cột "Giá bán lẻ" của báo giá vẫn là giá tham khảo cho một thùng đầy.
  - Không dùng `lineAmount`: đó là số net, và ở chế độ giá chưa thuế thì nó đã gồm thuế.
- Test trong `line-items-editor.test.tsx`:
  - 2 thùng, 3+2 dây, cùng giá 10.000 cho ra 150.000 ₫/thùng;
  - hai vị khác giá cho ra 162.000 ₫/thùng;
  - gross lệch do làm tròn chứng từ cho ra 162.002 ₫/thùng;
  - chưa chọn vị cho ra 264.000 ₫/thùng;
  - preset báo giá kiểm cả hai cột.

## L3. Số dây/vỉ phải là số nguyên

`draftProblem` trong `line-items/draft.ts` chặn số lẻ với câu "Thùng mix Nước ngọt: số lốc vị Cam phải là số nguyên.". `draft.test.ts` có hai test: một cho trường hợp bị chặn, một cho thùng thiếu nhưng toàn số nguyên vẫn hợp lệ.

## Test thêm

- `stock-out-lines.test.ts`, 7 test mới:
  - gom mix theo dòng đầu;
  - vị thiếu hàng;
  - vị giữ nhiều hơn lượng còn lại cũng tính là thiếu, như server;
  - thùng đã giao một phần;
  - dòng đầu đã hết;
  - combo;
  - dòng thường đứng cạnh thùng mix.
- `containers/stock-out-dialog.test.tsx`, file mới, 5 test:
  - hiển thị dòng nguyên thùng và không có ô nhập cho vị hay thành phần;
  - gửi đúng dòng đầu;
  - combo thiếu hàng bị khóa và có mô tả lý do;
  - bỏ tích thì không gửi thùng;
  - không chọn gì thì báo lỗi và không gửi.

## Gate (apps/web)

- `corepack pnpm typecheck`: sạch.
- `corepack pnpm lint`: sạch.
- `corepack pnpm test`: 156 file, 1109 test, tất cả pass.
- e2e: `invoices.spec.ts` và `at-02` chỉ xuất dòng thường qua textbox `Xuất lần này – …`, và luồng đó không đổi. `at-17` gọi API `stock-outs` trực tiếp và không kiểm ô đơn giá thùng. Vì vậy không phải sửa e2e. Theo ràng buộc, chưa chạy e2e.

## Lưu ý

- Xuất thùng mix luôn là cả phần còn lại của dòng đầu (`base_qty` = phần còn lại). Server cũng chấp nhận `base_qty` = 0 với nghĩa tương tự, nhưng web không dùng cách đó.
- Một dòng của `mix-pack-row.tsx` (ô `<Input style={{ width: 140 }} …/>` của preset báo giá) có sẵn từ trước và lệch so với Prettier width 180. Tôi để nguyên vì nó nằm ngoài phạm vi.
