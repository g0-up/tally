# Báo cáo: Acceptance specs AT-01..AT-18 (phase 21, bước 5)

**Ngày:** 2026-10-19
**Người thực hiện:** QA lead (tester)
**Phạm vi:** `apps/web/e2e/acceptance/` theo PRD §13. Không sửa `apps/web/src/**`, `apps/api`, `.github/workflows/*`, `shell.spec.ts`, `procurement.spec.ts`, `settings.spec.ts`, `catalog.spec.ts`, và không commit.

## Kết quả chính

Đã viết đủ 18 spec (AT-01 đến AT-18) kèm fixture dùng chung. AT-17 nay là spec thật: tạo nhóm mix và sản phẩm qua API, kiểm các bước giao diện về số thùng vượt sức chứa, đổi nhóm, và kiểm tồn theo từng vị qua API. Cổng kiểm tra đều đạt: `corepack pnpm typecheck` thoát 0, `corepack pnpm lint` thoát 0, và `corepack pnpm exec playwright test --list --project acceptance` liệt kê 18 spec acceptance cộng một test thiết lập phiên owner (19 test trong 19 file).

Chưa chạy spec nào trên stack thật vì môi trường này không có stack. Selector và hình dạng body API chưa được xác nhận bằng chạy thật. Danh sách điểm cần xác nhận nằm ở mục Concerns.

## Bảng AT → spec → kiểm tra → điều kiện skip

| AT | Spec | Kiểm tra chính | Skip khi |
|----|------|----------------|----------|
| AT-01 | `at-01-supplier-receipts-advance.spec.ts` | Nhập 2 lô cho hàng có màu, ứng trước NCC 100.000; tồn tăng đúng từng màu; tăng công nợ NCC cộng tạm ứng đã dùng bằng 500.000 | Thiếu dữ liệu seed (NCC đang hoạt động, hàng có màu) |
| AT-02 | `at-02-deposit-partial-delivery.spec.ts` | Đặt cọc, giao hai lần; tồn màu giảm đúng số đã giao; giá trị ghi nhận (công nợ giảm cộng tạm ứng dùng) bằng tổng hóa đơn | Thiếu hàng có màu và không màu đủ tồn, hoặc khách không có hạn mức |
| AT-03 | `at-03-concurrent-devices-stock.spec.ts` | Hai thiết bị (hai context) tranh số lượng cuối của một màu; đúng một bên thành công; tồn không âm; tồn giảm đúng số đã bán | Không có màu đủ tồn cho cuộc đua |
| AT-04 | `at-04-prepacked-gift-baskets.spec.ts` | Lệnh đóng giỏ tiêu thụ thành phần đúng một lần; bán giỏ không tiêu thụ thêm lần nữa; phần còn lại có thể tháo | Không có giỏ có công thức |
| AT-05 | `at-05-virtual-combo.spec.ts` | Combo ảo không có tồn vật lý; mỗi thành phần bị trừ đúng `qty` một lần | Không có combo ảo đang hoạt động |
| AT-06 | `at-06-multi-invoice-allocation-refund.spec.ts` | Thu 100.000 từ khách nợ ≥2 chứng từ; phân bổ cộng tạm ứng tăng bằng 100.000; công nợ không âm; quỹ tiền mặt tăng 100.000. Chưa kiểm hoàn tiền | Khách không hạn mức có dưới 2 chứng từ nợ |
| AT-07 | `at-07-exchange-cross-color.spec.ts` | Bán một đơn vị, đổi sang màu khác: màu trả +1, màu lấy −1; không có thu/trả chênh lệch | Không có sản phẩm ở hai màu đủ tồn |
| AT-08 | `at-08-color-count-expired-lot.spec.ts` | Mở phiên kiểm kê, đếm dòng đầu về 0, xác nhận ghi sổ chênh lệch; tiêu hủy lô quá hạn làm tồn màu giảm ít nhất bằng số lượng lô | Không có danh mục để đếm, hoặc không có lô quá hạn còn tồn |
| AT-09 | `at-09-multi-tenant-isolation.spec.ts` | Chủ cửa hàng B (context trình duyệt riêng) không thấy sản phẩm của cửa hàng A trong danh sách, trên trang tồn kho, và qua yêu cầu trực tiếp theo id | Thiếu `E2E_TENANT_B_URL`, `E2E_OWNER_B_EMAIL`, `E2E_OWNER_B_PASSWORD` |
| AT-10 | `at-10-posted-documents-period-lock.spec.ts` | Khóa sổ đến hết hôm nay qua thẻ khóa sổ; bán hàng trong kỳ bị 423 `period_locked`; tồn không đổi | Không phải `APP_ENV=test`; kho đã khóa đến hôm nay |
| AT-11 | `at-11-offline-crash-recovery.spec.ts` | Lần bán đầu bị lỗi cố ý (5xx), không trừ tồn; tải lại trang; thử lại cùng `Idempotency-Key` cho 201; tồn giảm đúng 1 | Không phải `APP_ENV=test`; chưa đặt `E2E_FAULT_INJECT_ARMED` |
| AT-12 | `at-12-cutover-opening-balance.spec.ts` | Trên cửa hàng mới: số dư quỹ tiền mặt bằng số tiền chủ nhập lúc cutover; có dòng tồn | Thiếu năm biến `E2E_FRESH_*`, `E2E_CUTOVER_*` |
| AT-13 | `at-13-season-close-late-cost.spec.ts` | Kết mùa qua màn hình chiến dịch; số dư quỹ tiền mặt không đổi. Chưa phủ phần chi phí đến muộn | Không phải `APP_ENV=test`; không có mùa đang mở |
| AT-14 | `at-14-subscription-expiry-export.spec.ts` | Cửa hàng hết hạn: bán bị 403 `subscription_read_only`; xác nhận mật khẩu, yêu cầu xuất dữ liệu (202), job chuyển sang `succeeded` | Thiếu `E2E_EXPIRED_*` |
| AT-15 | `at-15-restore-projection-rebuild.spec.ts` | Không chạy trong trình duyệt. Chạy bằng `infra/scripts/restore-drill.sh` trên bản sao đã khôi phục; báo cáo của script là bằng chứng | Luôn skip, có ghi chú tham chiếu script |
| AT-16 | `at-16-color-pos-combo.spec.ts` | API từ chối bán hàng nhiều màu không kèm màu (422 `color_required`), tồn không đổi; bán màu đã chọn: chỉ màu đó giảm | Không có sản phẩm ở hai màu đủ tồn |
| AT-17 | `at-17-mix-flavor-carton.spec.ts` | Xem mục AT-17 bên dưới: thùng đủ, thùng vượt, thùng chưa đủ, xuất nguyên thùng theo từng vị | Không có skip. Chỉ bước giao diện thiếu mới bị skip, và hiện không có bước nào thiếu |
| AT-18 | `at-18-color-price-stock-card.spec.ts` | Mở "Sửa giá bán" và thấy công tắc "Áp dụng cho tất cả màu"; thẻ kho hiện "Đã khớp sổ và tồn"; lịch sử giá trả về mảng | Không phải `APP_ENV=test`; không có sản phẩm có tồn |

### AT-17 (viết lại theo PRD §13, dòng 786–790)

- **Dữ liệu tạo qua API, có hậu tố duy nhất** (`uniqueSuffix()` trong fixture): ba đơn vị (hộp, thùng, dây), ba sản phẩm vị (`POST /products:quick-create`, giá bán 10.000 / 20.000 / 30.000 mỗi hộp), nhóm A (thùng 6 hộp, dây 2 hộp, 3 dây mỗi thùng, vị Dâu và Socola) và nhóm B (thùng 4 hộp, dây 2 hộp, 2 dây mỗi thùng, vị Vani). Tồn của ba vị được nhập qua `addStockApi`.
- **Bước giao diện** (trong `test.step`, dùng nhãn hiện có của `mix-pack-row.tsx`): chọn nhóm A, nhập 1 thùng và 2 + 2 dây (vượt sức chứa 3 dây) → callout "Giảm bớt 1" hiện, nút "Lưu & gửi khách" bị chặn bằng toast lỗi có "vượt"; sửa còn 2 + 1 → "đủ thùng". Đổi sang nhóm B → bảng vị chỉ còn Vani và giá thùng hiện 120.000 (30.000 × 4); đổi lại nhóm A, nhập 2 + 1, lưu quote.
- **Quote → hóa đơn → xuất kho qua API**: id báo giá lấy từ phản hồi `POST /quotes/{id}:send` của lần bấm "Lưu & gửi khách" (đăng ký trước khi bấm, không dò danh sách); `POST /quotes/{id}:convert` (201), `POST /invoices/{id}:confirm`, rồi xuất kho. Các bước này có giao diện tương ứng (quote → hóa đơn đã có trong `quotes.spec.ts`), nhưng AT-17 chạy qua API theo yêu cầu.
- **Xuất kho theo server** (`apps/api/internal/sales/stock_out.go`, dòng 313–320): dòng `mix_flavor` có `parent_line_id` nên gửi trực tiếp bị 422 `deliver_through_parent`; spec khẳng định điều này. Thùng được xuất nguyên thùng qua dòng header `mix_pack`, với `base_qty` bằng số còn lại của thùng (giao thiếu bị 422 `mix_pack_partial`).
- **Khẳng định**: tổng hóa đơn bằng 2×2×10.000 + 1×2×20.000 = 80.000 (giá vị × số lượng cộng lại, `finishMixHeaders` trong `lines.go`); tồn Dâu giảm đúng 4, Socola giảm đúng 2, Vani không đổi.
- **Thùng chưa đủ (bước thứ hai):** lưu qua UI thùng 1 với 1 + 1 dây (sức chứa 3 dây, còn trống 1). Tổng báo giá đọc từ phản hồi `:send` bằng 1×2×10.000 + 1×2×20.000 = 60.000, tức là giá vị × số dây thực chọn (PRD dòng 541). Báo giá được chuyển hóa đơn, xác nhận và xuất nguyên thùng; tồn Dâu và Socola mỗi vị giảm đúng 2 hộp.

## Chính sách dữ liệu và chạy lặp

- Id của chứng từ vừa tạo luôn lấy từ phản hồi của chính lần lưu, không dò danh sách. AT-17 đọc báo giá từ phản hồi `:send` (đăng ký bằng `page.waitForResponse` trước cú bấm "Lưu & gửi khách"); AT-02 đọc hóa đơn từ phản hồi `:confirm` của "Lưu & xác nhận bán". AT-01 tạo phiếu nhập qua API và dùng `doc_no` trả về để kiểm tra hiển thị trên giao diện, không tra cứu id.
- Mọi journey tạo dữ liệu có tên hoặc mã đều dùng hậu tố duy nhất, để chạy lại trên cùng một stack không va chạm. AT-01 dùng `Date.now()` cho số lô; AT-17 dùng `uniqueSuffix()`. Các journey còn lại chủ yếu đọc dữ liệu seed và khẳng định bằng delta, nên chịu được dữ liệu cũ.
- Khi chạy thủ công, nên dùng sandbox demo `cua-hang-a-demo` (tạo qua `tally seed demo --parent cua-hang-a`) thay vì `cua-hang-a`. Nightly CI dựng stack mới nên dùng `cua-hang-a` không ảnh hưởng gì.

## Biến môi trường

| Biến | Dùng cho | Ghi chú |
|------|----------|---------|
| `E2E_OWNER_PASSWORD` | Tất cả | Đã có sẵn trong `real-api.ts` |
| `E2E_LEDGER_VERIFY_CMD` | Tất cả | Khi không đặt, mỗi test ghi annotation `skipped-step`. Xem quy tắc PASS bên dưới |
| `APP_ENV=test` | AT-10, AT-11, AT-13, AT-18 | Stack dùng một lần; API phải chạy với `APP_ENV=test` |
| `E2E_FAULT_INJECT_ARMED` | AT-11 | Đặt `1` khi API khởi động với `TALLY_FAULT_INJECT=posting.before_commit@pos.checkout:1` |
| `E2E_TENANT_B_URL`, `E2E_OWNER_B_EMAIL`, `E2E_OWNER_B_PASSWORD` | AT-09 | Cửa hàng thứ hai và chủ của nó |
| `E2E_FRESH_TENANT_URL`, `E2E_FRESH_OWNER_EMAIL`, `E2E_FRESH_OWNER_PASSWORD`, `E2E_CUTOVER_OPENING_CASH`, `E2E_CUTOVER_DONE` | AT-12 | Cửa hàng chưa từng dùng; cutover chạy thủ công trước |
| `E2E_EXPIRED_TENANT_URL`, `E2E_EXPIRED_OWNER_EMAIL`, `E2E_EXPIRED_OWNER_PASSWORD` | AT-14 | Cửa hàng hết hạn |

## Quy tắc PASS của ledger

Một AT chỉ được tính PASS khi bước `tally ledger verify --tenant` đã chạy và báo `0 mismatches`. Nếu `E2E_LEDGER_VERIFY_CMD` không đặt, test vẫn có thể xanh trên Playwright, nhưng annotation `skipped-step` nghĩa là journey chưa kiểm sổ cái, và báo cáo kết quả không được ghi là PASS.

## Thay đổi cấu hình

- `apps/web/playwright.config.ts`: thêm project `acceptance` (`testMatch` `acceptance/**/*.spec.ts`, phụ thuộc `owner-session`, `storageState` là phiên owner đã lưu, `outputDir` là `e2e/acceptance/evidence/<ngày>`, bật trace và screenshot). Các project tenant (`chromium`, `firefox`, `webkit`, `mobile-390`) loại `acceptance/**` khỏi `testIgnore`, để spec không chạy hai lần.
- `apps/web/package.json`: script `e2e:acceptance` = `playwright test e2e/acceptance --project acceptance`. Makefile `acceptance` đã gọi script này (lead đã sửa).
- `.gitignore`: thêm `apps/web/e2e/acceptance/evidence/` cạnh dòng ignore ảnh prototype.
- `apps/web/e2e/acceptance/fixtures/acceptance.ts`: các helper dùng chung (kiểm tra môi trường, đọc tồn theo màu, số dư bên, quỹ tiền mặt, POST không khẳng định, mở cửa hàng khác, kiểm sổ cái, gom lỗi trang, hậu tố duy nhất).
- `HTML report`: `outputDir` chỉ đổi thư mục trace và screenshot. Báo cáo HTML vẫn ở `playwright-report/`, trừ khi đặt `PLAYWRIGHT_HTML_OUTPUT_DIR`.

## Cập nhật sau nhãn mới của uifix-a

Sau khi uifix-a đổi nhãn (xem mục 6 của `fullstack-developer-261019-0014-ui-a11y-fixes-group-a.md`), các selector sau đã được đổi:

- AT-02: ô số lượng của hóa đơn dùng "Số lượng – {tên}", khớp với `invoices.spec.ts`; ô số lượng khi giao hàng dùng `/^Số lượng/` vì chưa xác nhận nhãn của dialog "Xuất kho".
- AT-07: ô số lượng khi nhận màu mới dùng "Số lượng – {tên} {màu}", khớp với `returns.spec.ts`.
- AT-06: nút thu tiền dùng `/^Thu tiền/` vì nhãn mới là "Thu tiền – {khách}".
- AT-18: nút sửa giá và thẻ kho đi qua trang tồn kho bằng nhãn "Sửa giá – {tên}" và "Thẻ kho – {tên}" (`stock-table.tsx`), thay cho "Sửa giá bán" ở trang danh mục.
- AT-17: các nhãn "Chọn nhóm mix vị", "Số thùng", "Số {dây} vị {vị}", "+ Thêm thùng mix vị" lấy từ `mix-pack-row.tsx` và `line-items-editor.tsx`.

## Concerns

1. **Chưa chạy spec nào trên stack thật.** Selector và body API chưa được xác nhận bằng chạy thật. Những chỗ rủi ro nhất:
   - AT-17: body `POST /units` (`{name}`) và `POST /mix-groups`; giá bán của `quick-create` cho sản phẩm vị. Lỗi xuất kho đã được lead đối chiếu với server và spec đã sửa: xuất theo dòng `mix_pack`, không theo dòng vị. Chưa chạy thật nên các body này vẫn cần xác nhận khi chạy stack.
   - AT-17 và AT-02: rủi ro dò danh sách đã được bỏ. Id và số chứng từ lấy từ phản hồi lưu, nên không còn phụ thuộc vào số báo giá của khách. Cần xác nhận khi chạy thật rằng `:send` và `:confirm` là các yêu cầu cuối của lần lưu và trả về thân chứa `id`, `version`, `doc_no`, `total_amount`.
   - Thùng chưa đủ đã được server cho phép (`mix-incomplete`, xem `plans/reports/fullstack-developer-261019-0750-mix-incomplete-carton.md`). Spec AT-17 nay khẳng định cả thùng đủ, thùng vượt (422 `mix_parts_exceed_pack`) và thùng chưa đủ.
   - AT-17: bước giao diện giả định quote có khách hàng (dialog bắt buộc khách) và nhãn "Tạo báo giá" của nút mở dialog.
   - AT-07: nút "Đổi sang màu khác" và radiogroup "Màu khách lấy".
   - AT-08: "Bắt đầu phiên kiểm kê", "Hoàn tất đếm" và luồng lô quá hạn; "Xuất kho giao khách" (AT-02); các trường `days_to_expiry` của `/lots` và `code` của `/payment-methods`; `/stock` dạng danh sách (AT-12); định tuyến `/settings/account` cho thẻ khóa sổ (AT-10); các đường dẫn theo `menu.ts`.
2. **Nhãn chưa thấy trong src:** "Carton" và "Giá theo màu" không có trong src. Nhãn "Carton" đã được thay bằng luồng thùng mix vị thật ở AT-17. Phần đổi giá theo màu của AT-18 chỉ kiểm công tắc chứ chưa ghi giá.
3. **Phạm vi chưa phủ:**
   - AT-06: phần hoàn tiền cho hóa đơn bị trả.
   - AT-13: chi phí đến muộn.
   - AT-10: sửa chứng từ đã ghi và mở lại kỳ.
   - AT-12: màn hình wizard cutover (chỉ kiểm kết quả số dư).
   - AT-17: kịch bản quà giỏ có dòng mix (`kits/`) và phần giá theo từng vị trong giỏ; chỉ phủ thùng mix bán lẻ.
   - AT-15: chỉ skip.
4. **Kiểm kê (AT-08):** spec chưa khẳng định kết quả đếm theo từng màu bằng số đã xác nhận.
5. **Dữ liệu thay đổi:** AT-01 đến AT-08, AT-14 đến AT-17 tạo chứng từ thật. Nên chạy thủ công trên `cua-hang-a-demo`. AT-10, AT-11, AT-13 và AT-18 khóa sổ hoặc đổi giá, nên chỉ chạy trên stack `APP_ENV=test`.
6. **`E2E_LEDGER_VERIFY_CMD` không đặt:** bước kiểm sổ cái không chạy và chỉ để lại annotation. Theo quy tắc PASS ở trên, các AT khi đó không được ghi là PASS. Cần đặt biến này trong lần chạy acceptance chính thức.
7. **Trạng thái git:** `apps/web/e2e/acceptance/` là thư mục untracked; `apps/web/playwright.config.ts`, `apps/web/package.json` và `.gitignore` đã sửa. Chưa commit, theo yêu cầu.

## Trạng thái

**Status:** DONE_WITH_CONCERNS

**Summary:** AT-01 đến AT-18 đều có spec, AT-17 đã là journey thật (không còn skip) với dữ liệu tạo qua API có hậu tố duy nhất. Typecheck, lint và `--list --project acceptance` đều thoát 0 (18 spec acceptance cộng một test thiết lập). Chưa có spec nào được chạy trên stack thật.

**Concerns:** Xem mục Concerns ở trên. Mục 1 (chưa chạy thật, đặc biệt các giả định về body API của AT-17) và mục 6 (ledger chưa được đặt, nên chưa AT nào được tính PASS) là hai điểm cần xác nhận trước khi chạy `make acceptance`.
