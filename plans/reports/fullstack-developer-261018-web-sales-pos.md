# Báo cáo triển khai: Web bán hàng & POS (online)

- Ngày: 2026-10-18
- Plan: `plans/261016-0854-tally-full-product-implementation/phase-16-web-sales-pos.md`
- Trạng thái: DONE_WITH_CONCERNS. Đã viết xong mã và test, nhưng chưa chạy được bất kỳ lệnh kiểm tra nào (xem mục "Lệnh kiểm tra").

## Kết quả

Đã triển khai đủ phạm vi online của màn hình bán hàng:
- POS: quét mã, chọn màu, tiền thối, lưu/sửa/khóa giỏ, bán chịu, hộp xác nhận ngoại lệ và gửi lại khi `shift_changed` với cùng idempotency-key.
- Hóa đơn: tạo, xác nhận có ngoại lệ, đặt cọc, xuất kho nhiều lần, hủy phần chưa giao.
- Báo giá: vòng đời đến khi chuyển thành hóa đơn.
- Trả hàng có hoàn tiền; đổi hàng có xem trước chênh lệch và "Khách trả thêm".
- Trang in chứng từ.

Phần offline/PWA không làm (thuộc phase sau). Không sửa backend.

## File

Mã sản phẩm (từ các phiên trước):
- `apps/web/src/features/sales/**`: api, schemas, components (line-items, return-rows, return-color-cell…), containers (invoice/quote/return/exchange dialog, drawer, overlay host), hooks, pages (invoices, quotes, returns, print-sales-document).
- `apps/web/src/features/pos/**`: màn hình POS, `use-pos-cart`, `payment-method-panel`, giỏ đã lưu.

Unit test (Vitest + MSW):
- `use-pos-cart`, `payment-method-panel`, `invoice-dialog`, `quote-actions`, `return-dialog`, `print-sales-document-page`.

File dùng chung đã sửa (đã báo agent `phase15-web` trước khi sửa; chỉ thêm, không đổi phần có sẵn):
- `apps/web/src/app/router.tsx`: thêm route in `/print/sales-documents/:id`.
- `apps/web/src/app/router.test.tsx` và `apps/web/src/app/layouts/app-shell.test.tsx`: sửa hai test bị team-lead báo lỗi ("Bán hàng" phải đến `/sales/pos`).
  - `menu.ts` vẫn đúng: trang đầu của nhóm Bán hàng là `/sales/pos`.
  - Nguyên nhân dự đoán: mọi trang là lazy chunk, và trang POS giờ là chunk nặng. `router.state.location` chỉ đổi sau khi chunk tải xong, nên test kiểm tra pathname ngay sau click hoặc ngay sau h1 đầu tiên sẽ thấy đường dẫn cũ.
  - Cách sửa: `landsOn` chờ pathname bằng `waitFor` (10s) rồi mới tìm h1. Test app-shell chờ heading "POS & giỏ hàng" (10s) trước khi kiểm tra pathname.
  - Chưa chạy lại để xác nhận.

E2E (mới, phiên này):
- `apps/web/e2e/support/sales-data.ts`: dò dữ liệu seed và các bước dùng chung (bán tại POS, chọn option, đọc số tiền trên impact summary). Không sửa `e2e/support/real-api.ts`.
- `apps/web/e2e/pos.spec.ts`, 6 test:
  - quét → chọn màu → tiền mặt có tiền thối, toast HD chỉ hiện sau 2xx;
  - double click chỉ gửi 1 lần;
  - `shift_changed` gửi lại cùng key;
  - gõ số lượng rồi bấm ngay vẫn bán đúng số lượng;
  - lưu giỏ → sửa → bán, sau đó giỏ chỉ còn "Sao chép";
  - bán chịu vượt hạn mức → hộp ngoại lệ.
- `apps/web/e2e/invoices.spec.ts`, 2 test:
  - tạo → xác nhận → đặt cọc → giao 2 lần (lần đầu trừ cọc) → hủy phần còn lại;
  - vượt hạn mức chỉ xác nhận được khi tích ô xác nhận.
- `apps/web/e2e/quotes.spec.ts`, 1 test: báo giá → khách chấp nhận → chuyển hóa đơn → xác nhận → hủy phần còn lại.
- `apps/web/e2e/returns.spec.ts`, 2 test:
  - trả 1 đơn vị của đơn khách lẻ, hoàn tiền từ quỹ;
  - đổi sang màu khác, lấy thêm 1 và thu "Khách trả thêm".

Các test E2E dò dữ liệu seed, và bỏ qua (skip) kèm lý do khi seed thiếu dữ liệu. Các trường hợp trình duyệt không tự tạo được thì chặn bằng `page.route`, chỉ trên route checkout: `shift_changed` và ngoại lệ hạn mức ở POS.

Đã để nguyên plan file: frontmatter vẫn `todo` và không tick todo nào, vì chưa có bằng chứng chạy test.

## Lệnh kiểm tra

Chưa chạy được lệnh nào. Từ giữa phiên, mọi lệnh Bash đều lỗi trước khi khởi động shell:

```
Could not start /usr/bin/zsh: the command line plus environment exceed the OS exec argument limit (E2BIG). At spawn: command line 164.5KB ...
```

Vì vậy các lệnh sau chưa chạy:
- `pnpm lint`
- `pnpm typecheck`
- `pnpm test`
- `pnpm build`
- `pnpm exec playwright test`
- kiểm tra thủ công trên trình duyệt

Chưa có kết quả pass/fail cho bất kỳ lệnh nào. Không khởi động Vite hay API, nên không có tiến trình nào cần dừng. Không chạm container `tally-postgres-1`. Cần chạy lại toàn bộ trong một phiên có shell hoạt động.

## Sai khác so với plan

- Phần "hàng lấy" khi đổi hàng dùng preset bán (`LineItemsEditor` preset `sell`).
- Drawer chứng từ bán hiển thị dòng bằng `DocumentLines`.
- Lý do trả, đổi và hủy lấy từ API `/reasons`, không hard-code.
- Giá trị trả, giảm nợ và khoản hoàn trong hộp trả hàng là ước tính phía client. Server là nơi chốt số liệu.
- Dòng cha mix/combo không được trả trực tiếp.
- Nút "Hoàn tiền" hiện theo `refund_due_amount`.
- Bảng đổi hàng chỉ hiện số tiền, vì API không trả dòng hàng.
- Số chứng từ chỉ hiện được khi chứng từ nằm trong danh sách đã tải.
- Trang in mặc định A5, có nút chuyển sang giấy cuộn 80mm. Khi không có khách thì hiện "Khách lẻ".
- Mức vượt hạn mức trên hóa đơn được tính phía client để hiện ô xác nhận trước khi gửi.
- Lý do hủy phần chưa giao là bắt buộc trên UI nhưng không được lưu, vì API không nhận trường này.
- E2E dùng cách dò seed và chặn route thay vì fixture cố định.

## Thiếu sót API (không sửa backend)

- Không có trường lý do khi từ chối báo giá và khi hủy phần chưa giao.
- Không có `GET /payments` theo hóa đơn.
- Không tìm kiếm hóa đơn theo chữ.
- Danh sách hóa đơn thiếu trạng thái thanh toán, nên phải gọi thêm từng hóa đơn (N+1).
- Báo giá không có dữ liệu giá vốn.
- Chứng từ không kèm tên khách.
- Dòng xuất kho không có lô.
- `shift_unavailable` không bao giờ được trả về.
- Không tra cứu được theo `doc_no`.
- Giỏ hàng không có người tạo.
- Không sửa được giá thùng mix.
- Subject ngoại lệ:
  - `pos_checkout` dùng tenant id;
  - phiếu đổi dùng `doc_version` 0;
  - `doc_type` "invoice" là suy luận.
- Tên action trong lịch sử báo giá là đoán.
- Hậu tố idempotency-key tính theo từng lần gọi.
- Lần giao hàng không kèm số phiếu xuất.
- Đơn vị số lượng trả là giả định (đơn vị của dòng bán).
- `source_type` của sổ nợ chưa rõ.
- Ngữ nghĩa `unit_amount` của dòng còn trả chưa rõ.
- Danh sách phiếu trả thiếu dòng hàng và số còn phải hoàn.
- Phiếu đổi thiếu dòng hàng.
- Chứng từ bán thiếu ngày đến hạn.
- Payload in thiếu chứng từ nguồn và gợi ý khổ giấy.

## Rủi ro

- Không có lệnh nào được chạy, kể cả typecheck, nên có thể còn lỗi kiểu, lint hoặc unit test.
- Test vượt hạn mức trên hóa đơn cần server `:check` trả `credit_limit_exceeded`. Nếu không, toast sẽ thiếu đuôi "Ngoại lệ đã ghi nhật ký.".
- Tên accessible của option có thể kèm tên màu (title của chấm màu), nên regex SKU không neo đầu.
- Các bước "Đổi sang màu khác" và "Hoàn tiền từ quỹ" phụ thuộc vào thứ tự quỹ/màu trong seed.
- E2E mặc định lấy quỹ đầu tiên để hoàn tiền và thu chênh lệch.

## Câu hỏi còn mở

- Bộ sửa dòng hàng (line items) của bán hàng hiện nằm trong `apps/web/src/features/sales/components/line-items/`, gồm `line-items-editor.tsx`, `item-line-row.tsx`, `draft.ts` và `use-line-pricing`. Nó có preset `sell` và `quote`, được hóa đơn, báo giá và đổi hàng dùng chung.
  - Chỉ đạo phối hợp nói base chung sẽ ở `shared/ui/line-items` do phase15-web sở hữu, nhưng hiện chưa có file base nào ở đó.
  - Đã có quyết định của lead: giữ nguyên trong `features/sales`, không tạo preset mới. Việc hợp nhất với base chung (nếu có) để phase polish cân nhắc.

- Có bổ sung các trường API nêu trên (lý do hủy, tên khách trên chứng từ, dòng hàng của phiếu đổi/trả) ở phase backend nào không?
