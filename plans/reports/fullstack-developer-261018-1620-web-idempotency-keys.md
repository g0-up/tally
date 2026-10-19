# Web gửi Idempotency-Key cho 7 lệnh tạo

Ngày: 2026-10-18 (Asia/Saigon). Người làm: phase17-web. Bản cập nhật sau hai tin phản hồi của team-lead và bản review `code-reviewer-261018-1700-web-idempotency-keys-review.md` (xem mục "Review fixes").

## Kết quả

Mọi chỗ trong `apps/web/src/features` gọi 7 endpoint đều gửi `Idempotency-Key` sinh từ `CommitContext.idempotencyKey` của intent. Danh sách endpoint: `POST /purchase-orders`, `/supplier-documents`, `/invoices`, `/quotes`, `/skus/{id}/sale-price`, `/kits`, `/assembly-orders`. Ngoài 5 feature được giao, `src/` không còn chỗ nào POST tới 7 đường dẫn này.

Cả 3 gate trong `apps/web` đều xanh sau vòng sửa theo review: `corepack pnpm typecheck`, `corepack pnpm lint`, `corepack pnpm test` (110 file, 759 test).

## Thay đổi trong vòng này

1. **Helper chuyển sang `apps/web/src/shared/hooks/create-once.ts`**, test đi kèm ở `shared/hooks/create-once.test.ts`. File cũ `features/inventory/create-once.ts` đã xóa và dòng re-export trong `features/inventory/index.ts` đã bỏ. Sales, procurement và kits giờ import thẳng từ `shared/hooks/create-once`.
2. **API mới:** `createOnce(state, body, { send, update? })` trả `{ result, edited }`.
   - Nếu body đang giữ khác body của form, lần bấm sau gửi lại đúng body đang giữ dưới cùng key, rồi mới xét phần sửa.
   - Có `update`: chỉ gọi khi body hiện tại thật sự khác body đã tạo. Hai body được so bằng JSON chuẩn hóa (`canonicalJson`: khóa sắp xếp, tiền vẫn là chuỗi Decimal). `update` nhận đúng kết quả replay, nên dùng version của chứng từ đó. Update thất bại thì state giữ nguyên, lần sau update lại chứ không tạo lại.
   - Không có `update`: trả `edited: true` để chỗ gọi báo PartialCommitError. Không bao giờ sinh key mới để gửi lại.
3. **Sửa If-Match cũ ở hóa đơn** (`sales/api/invoice-mutations.ts`). Hàm cập nhật ghi `{key, id, version}` vào `progress` của intent trước khi PATCH, rồi nâng version ngay khi PATCH thành công. Vì thế nếu PUT lines lỗi, lần thử lại gửi cả PATCH lẫn PUT với version mà PATCH đã để lại. Lỗi này xảy ra cả khi sửa nháp có sẵn lẫn khi sửa hóa đơn vừa tạo.
4. **Lỗi update của chứng từ vừa tạo thành PartialCommitError.** Helper `editsNotSaved(docNo, e)` nằm trong `sales/api/mutations.ts` (thêm mới, không đổi gì sẵn có). Câu báo là "Đã tạo **{số}** nhưng chưa lưu phần sửa sau: {lý do} Bấm lại để lưu tiếp.". Lỗi cần nhập lại mật khẩu và lỗi phiên hết hạn vẫn đi theo đường xử lý cũ.
   - Hóa đơn: áp dụng khi intent này tạo ra hóa đơn và lỗi nằm ở bước lưu nháp. Lỗi ở bước ghi tiền khách trả giữ câu cũ. Danh sách hóa đơn được làm mới để thấy nháp vừa tạo.
   - Báo giá: PATCH bản nháp nằm trong `update`. Nếu PATCH lỗi thì không gửi khách.
5. **Bước giá bán ở procurement không còn bị bỏ qua khi giá trên form đã sửa.** Id của bước giờ có cả giá (`price:${skuId}:${retail}`). Giá đã sửa ra id mới nên được ghi lại, và key con cũng mới vì có kèm giá. Giá không đổi thì vẫn bỏ qua như trước. Test đã được kiểm tra hai chiều: bỏ phần sửa thì test đỏ.
6. **Thêm câu tiếng Việt cho `idempotency_key_invalid`** (server trả 422) trong `shared/api/api-error.ts`: "Yêu cầu không hợp lệ, hãy tải lại trang rồi thử lại.". Có test trong `api-error.test.ts`.

## Thiết kế

### Giá bán: đưa giá trị vào key (đã được duyệt)

Đã đổi ở vòng review: `${intent}:sale-price:${skuId}:${kind}:${color_id | all-colors | general}:${source}:${price | none}:${n}`, dựng trong `postSalePrice` (`pricing/api/mutations.ts`). Ý nghĩa của `n` nằm ở mục "Review fixes".
- Thử lại với cùng giá thì cùng key và cùng body, server replay.
- Sửa giá thì ra key mới, nên không bao giờ dùng lại key với body khác.

### Body đang giữ chỉ nằm trong bộ nhớ, theo từng intent key

- `CreateState` nằm trong `useRef` của hook, gắn với key của intent. Không có localStorage hay IndexedDB.
- Body đang giữ bị bỏ khi server từ chối thường (4xx khác `idempotency_key_in_progress`).
- Body được giữ khi lỗi mạng, ≥500, 2xx không đọc được, hoặc `idempotency_key_in_progress`.
- Một key mới (sau 2xx hoặc `resetKey`) tạo state mới, nên không bao giờ dùng body của intent khác. Đóng hộp thoại thì unmount, ref mất theo.
- Sau một lần 2xx, state chỉ còn là mốc để so phần sửa; không bao giờ gửi lại từ đó.

### Request cập nhật dùng If-Match, không có key con

"Đúng mẫu đang có" được hiểu là giữ cách cập nhật hiện tại.
- `PATCH /invoices/:id`, `PUT /invoices/:id/lines` và `PATCH /quotes/:id` dùng `bind` thường, không phải `bindKeyed` (`apps/api/internal/sales/http.go:55,69,71`). Server bỏ qua Idempotency-Key trên các đường này và chặn ghi trùng bằng If-Match.
- Nếu gửi một key con mà server bỏ qua, sau này khi route chuyển sang `bindKeyed`, cùng key con kèm body đã sửa sẽ thành 409. Vì vậy không gửi.
- Các bước sau đó có key riêng thì vẫn giữ như cũ: `:confirm`, `:paid:${amount}`, `:exception:…`, `-confirm` của PO.

## Chỗ báo PartialCommitError vì chưa có API cập nhật

Danh sách để team-lead đưa vào danh mục API còn thiếu:

| Chỗ gọi | Endpoint tạo | Câu báo (có số chứng từ) |
|---|---|---|
| `useSaveOrder` (`procurement/api/mutations.ts`) | `POST /purchase-orders` | `PROCUREMENT_MESSAGES.orderCreatedAsSent(docNo)`: đã lưu theo nội dung lần trước, phần sửa chưa lưu, đơn chưa chốt, kiểm tra đơn {số}. Không chốt và không ghi giá. |
| `useCreateAssembly` (`kits/api/mutations.ts`) | `POST /assembly-orders` | `KIT_MESSAGES.assemblyCreatedAsSent(docNo)` |
| `usePostReceipt`, bước gắn hóa đơn NCC | `POST /supplier-documents` | "Đã ghi **{số phiếu}**. " + `invoiceMatchedAsSent(soHoaDonCu)`. Server còn chặn đối chiếu lần hai (`receipt_already_matched`). |
| `useMatchInvoice` | `POST /supplier-documents` | `PROCUREMENT_MESSAGES.invoiceMatchedAsSent(soHoaDonCu)` |
| `useCreateKit`, khi loại, mã hoặc đơn vị cơ sở bị sửa | `POST /kits` | `KIT_MESSAGES.createdAsSent(name, code)`. Tên và công thức vẫn được cập nhật qua các bước sẵn có. |

Các câu này đều nêu số chứng từ, nhưng văn phong chưa giống hệt mẫu "Đã tạo {số}; chỉnh sửa sau chưa được lưu, mở chứng từ để sửa.". Tôi giữ nguyên các câu đã có test và đã được duyệt ở vòng trước.

## Test

Helper (`shared/hooks/create-once.test.ts`, 11 test). Có đủ 5 ca team-lead yêu cầu:
- lỗi mạng hoặc 5xx thì lần sau gửi lại đúng body cũ;
- có sửa thì gọi `update` với chứng từ replay ở version 1;
- không sửa thì không gọi `update`;
- 4xx thì bỏ body đang giữ;
- `idempotency_key_in_progress` thì giữ body.

Ngoài ra có thêm các ca:
- không có `update` thì trả `edited`;
- update lỗi thì lần sau update lại chứ không tạo lại;
- body cũ bị từ chối thì gửi body mới;
- phân loại `outcomeUnknown`.

Mỗi luồng có ít nhất một test đi qua helper:
- `sales/api/create-documents.test.tsx`, 3 test mới:
  - Sửa nháp có sẵn: PATCH thành công, PUT lỗi, lần thử lại gửi `PATCH "1", PUT "2", PATCH "2", PUT "2"`.
  - Hóa đơn vừa tạo, PUT lỗi: hiện "nhưng chưa lưu phần sửa sau" cùng số SO-0009. Lần sau dùng version mới và chỉ có 2 POST.
  - Báo giá, PATCH lỗi: báo kèm BG-0009, không gửi khách.
- `procurement/api/save-order.test.tsx`, test mới: giá của sku-1 đã ghi 18000, giá của sku-2 lỗi. Sửa sku-1 thành 19000 thì lần sau ghi sku-1 với key `…:19000:…`, còn sku-2 thử lại với cùng key.
- Kits: `kit-dialog`, `assembly-dialog` và `combo-edit-dialog` không đổi và vẫn xanh qua helper mới.
- `shared/api/api-error.test.ts`: thêm `idempotency_key_invalid`.

## File đã sửa hoặc tạo

- **shared:**
  - `hooks/create-once.ts` (mới)
  - `hooks/create-once.test.ts` (chuyển từ inventory, viết lại)
  - `api/api-error.ts` (thêm 1 dòng)
  - `api/api-error.test.ts` (thêm 1 ca)
- **inventory:**
  - `index.ts` (bỏ re-export)
  - `create-once.ts` và `create-once.test.ts` (đã xóa)
  - `api/mutations.ts`
  - `containers/color-price-dialog.test.tsx`
- **pricing:** `api/mutations.ts`, `index.ts`, `api/save-sale-price.test.tsx`.
- **procurement:**
  - `api/mutations.ts` (import, API mới, id của bước giá)
  - `api/adapters.ts`
  - `api/save-order.test.tsx`
  - `containers/new-receipt-dialog.test.tsx`
  - `containers/receipt-invoice-dialog.test.tsx`
- **sales:**
  - `api/invoice-mutations.ts`
  - `api/quote-mutations.ts`
  - `api/mutations.ts` (thêm `editsNotSaved`)
  - `api/create-documents.test.tsx`
- **kits:**
  - `api/mutations.ts`
  - `api/adapters.ts`
  - `containers/kit-dialog.test.tsx`
  - `containers/assembly-dialog.test.tsx`
  - `containers/combo-edit-dialog.test.tsx`

Tôi không sửa `schema.d.ts`, `apps/api` hay file nào của phase19. Không commit và không chạy dev server.

## Lưu ý

1. Với PO, lệnh đóng giỏ và đối chiếu hóa đơn, mỗi lần bấm lại đều báo cùng một câu cho tới khi đóng hộp thoại. Lý do là chưa có API cập nhật, và không được tạo key mới.
2. Sau khi intent có 2xx, state cũ còn trong ref cho tới lần chạy sau (lúc đó key mới thay state) hoặc tới khi hộp thoại unmount. State này không bao giờ được dùng để gửi.
3. (Đã sửa ở vòng review.) Giá giờ được chuẩn hóa trước khi so và trước khi đưa vào key.


## Review fixes

Phần này trả lời bản review `plans/reports/code-reviewer-261018-1700-web-idempotency-keys-review.md`. Không sửa server, `schema.d.ts` hay file của phase19. Mỗi test sửa lỗi đã được thử hai chiều: tạm bỏ phần sửa (sửa tạm bằng `sed`, chép lại file gốc sau đó) thì test đỏ, trả lại thì xanh. Các test này được liệt kê ở cuối từng mục.

### H1: hóa đơn và báo giá không gửi lại phần đã ghi

- `useSaveInvoice` (`sales/api/invoice-mutations.ts`) giữ tiến trình theo từng intent key. Tiến trình gồm hóa đơn mới nhất mà intent biết, header và lines mà server đang giữ sau lần ghi có trả lời gần nhất (JSON chuẩn hóa qua `requestJson`), cờ `stale`, và `confirming`.
- Mỗi lần bấm chỉ PATCH header nếu header khác bản đã ghi, và chỉ PUT lines nếu lines khác. Vì vậy khi khách đã trả tiền và `:confirm` bị từ chối, bấm lại không gửi PUT lines (server sẽ chặn bằng `invoice_has_payments`). Hóa đơn vừa tạo lấy header và lines từ chính body đã tạo ra nó, nên không còn PATCH thừa sau khi tạo.
- `confirmInvoice` nhận thêm callback `sending(ids)`, được gọi mỗi khi một body `:confirm` đi ra. Một khi `:confirm` đã đi ra mà không bị từ chối rõ ràng (mất trả lời hoặc ≥500), lần bấm sau chỉ gửi lại `:confirm` đó: cùng key, cùng If-Match, cùng exception ids, không chạy bước sửa và bước tiền. Nếu form đã sửa từ lần trước, câu thành công thêm "Phần sửa sau lần bấm trước chưa được lưu.". Khi mất trả lời ở bước này, câu báo là "Đã lưu nháp {số} nhưng chưa rõ đã xác nhận hay chưa. Bấm lại để kiểm tra; không xác nhận lần nữa.".
- Báo giá (`sales/api/quote-mutations.ts`, nhánh tạo mới) làm tương tự. Sau `createOnce` (không còn `update`), PATCH bản nháp chỉ khi header và lines khác bản đã ghi. Lỗi khi đó đi qua `editsNotSaved`.
- Test trong `sales/api/create-documents.test.tsx`:
  - "does not write the lines again once the customer paid, when a refused confirmation is retried"
  - "resends alone a confirmation that got no answer, under the same key, and says the edits since were not stored"
  - "updates the invoice an unanswered attempt created with the lines edited since": giờ chỉ có PUT lines, không có PATCH.

### M1: key giá có bộ đếm theo intent

- Mỗi slot `${sku}:${kind}:${scope}` của intent giữ `n`, `sent` (giá của lần ghi mất trả lời) và `written` (giá đã ghi có trả lời).
- Bỏ qua chỉ khi không có lần ghi nào đang treo và giá bằng `written`. Nếu giá khác `sent`, `n` tăng. Lần mất trả lời giữ nguyên `n` và giá, nên bấm lại sẽ replay. Lần bị từ chối xóa `sent`.
- PO, phiếu nhập, giá theo màu, combo và màn giá: mỗi intent giữ một `SalePriceWrites` riêng trong tiến trình của nó.
- Test "writes a price changed back to one the intent already saved again, under a new key" (`pricing/api/save-sale-price.test.tsx`): A→B→A ra 3 request với 3 key khác nhau. Test đỏ khi bỏ `n` khỏi key, và cũng đỏ khi quay lại cách bỏ qua theo giá đầu tiên.

### M2: cập nhật mất trả lời hoặc gặp 412 thì đọc lại chứng từ

- Ở hóa đơn: nếu PATCH hoặc PUT mất trả lời thì bật `stale`. Gặp 412 thì bật `stale` và xóa cả hai bản đã ghi. Lần bấm sau sẽ `GET /invoices/{id}` để lấy version và trạng thái thật, ghi header theo bản đọc được, rồi tính lại phần khác. Nếu bị từ chối thường thì trả lại bản đã ghi trước đó.
- Ở báo giá: cùng cách, đọc qua `GET /quotes/{id}`, sau đó luôn PATCH lại vì không suy được lines từ báo giá.
- Chỉ có một toast mỗi lần:
  - mất trả lời khi sửa hóa đơn có sẵn: một PartialCommitError "Chưa rõ đã lưu {số} hay chưa. Bấm lại để kiểm tra và lưu tiếp.";
  - 412: chỉ toast STALE chung (`editsNotSaved` và `translateSalesError` để nguyên 412 cho `handledElsewhere`).
- Test:
  - "reads the invoice again when a header update of an edit got no answer, instead of meeting a newer version": PATCH mất trả lời, bấm lại thì GET rồi PUT với version mới, không có 412, và đúng một `role=alert`.
  - "reads the invoice again when a lines update…"
  - "reads the quote again when the update of the lines edited since got no answer…"

### M3: câu báo khi chưa rõ kết quả, và hỏi trước khi đóng

- `createOnce` biến lỗi mất trả lời thành `OutcomeUnknownError` (một `PartialCommitError`) với câu "Chưa rõ đã ghi sổ hay chưa. Bấm lại để kiểm tra; không tạo chứng từ mới.". Câu "Chưa ghi sổ." không còn hiện khi kết quả chưa rõ.
- Ở bước gắn hóa đơn NCC của phiếu nhập, câu báo là "Đã ghi {số} nhưng chưa rõ đã gắn hóa đơn NCC hay chưa…".
- Ở bước sau tạo, `editsNotSaved` báo "chưa rõ đã lưu phần sửa sau hay chưa".
- Màn giá, khi mới ghi được một phần, báo "chưa rõ đã lưu {giá sỉ} hay chưa".
- `useUnsettled()` (`shared/hooks/create-once.ts`) là state hook, cho 7 hook tạo trả thêm `unsettled`.
  - 7 hộp thoại truyền `closeWarning` cho `CommitDialog`: `new-po-dialog`, `new-receipt-dialog`, `receipt-invoice-dialog`, `quote-dialog`, `invoice-dialog`, `assembly-dialog`, `kit-dialog`.
  - Khi đang có body chưa rõ kết quả, Hủy, × hay Esc không đóng ngay. Footer hiện `Callout` (`role="alert"`) với câu "Có thể chứng từ đã được tạo. Đóng sẽ không kiểm tra lại được — vẫn đóng?", cùng nút "Ở lại" (nhận focus) và "Vẫn đóng".
  - Esc hoặc scrim khi đang hỏi được hiểu là "Ở lại".
- Không làm lưu trữ ở mức module (tùy chọn).
- Test:
  - `shared/hooks/create-once.test.ts`: kiểm tra câu báo, `notice` và `cause`.
  - `kits/containers/assembly-dialog.test.tsx`:
    - "asks before closing while a create may have been stored without an answer": đỏ khi bỏ `closeWarning`, và cũng đỏ khi Esc lúc đang hỏi lại đóng hộp thoại;
    - "closes without asking when nothing may have been stored".

### M4: hóa đơn NCC mất trả lời, rồi người dùng xóa số

- Bước gắn hóa đơn của `usePostReceipt` dùng body đang giữ khi chưa rõ kết quả, bất kể form đang có gì, kể cả khi ô số đã trống. `createOnce` gửi đúng body đó dưới cùng key.
- Câu "Đã đối chiếu theo số … của lần bấm trước" chỉ hiện khi bước này đã có 2xx (`invoice.result !== null`), và chỉ hiện một lần.
- Test "resends an invoice that got no answer even after its number was cleared, and claims the match only once it is answered" (`procurement/containers/new-receipt-dialog.test.tsx`).

### Mục mức thấp

- **L1:** session hết hạn (401) giờ được tính là bị từ chối nên body bị bỏ. Nhập lại mật khẩu (403 `reauth_required`) vẫn giữ body.
  - Đăng xuất hoặc hết phiên thì `RequireAuth` chuyển sang `/login`, làm unmount hộp thoại và ref.
  - Web chưa có chỗ đổi tenant.
  - Test "keeps the body through a password prompt…" và "drops the body when the session ended…" đều đỏ khi bỏ phần sửa.
- **L2:** `useSaveSalePrice` báo PartialCommitError "Đã lưu một phần giá nhưng chưa lưu {giá sỉ}: {lý do} Bấm lại để lưu tiếp." khi slot khác đã ghi xong. Test "names the list it did not save when the other one was saved".
- **L3: bỏ qua.** `:send` (báo giá) và `:publish` (giỏ) chưa nhận key ở server (`bind` thường). Gửi lại các bước này dựa vào If-Match và chuyển trạng thái. Cần server nhận key thì mới làm được.
- **L4:** `KIT_MESSAGES.createdAsSent` giờ nêu "Tên, thành phần, giá và tồn tối thiểu vừa sửa chưa được lưu, giỏ chưa phát hành." Test thêm assert ở `kits/containers/kit-dialog.test.tsx`.
- **L5:**
  - `requestJson` chuẩn hóa chuỗi số thập phân (`/^-?[0-9]+\.[0-9]+$/`) trước khi so, và `postSalePrice` chuẩn hóa giá trước khi đưa vào key và body.
  - `createOnce` gửi lại body đang giữ nguyên văn khi nó bằng body của form.
  - Test `requestJson` và "treats an amount written with trailing zeros as the body it kept…" trong `create-once.test.ts`.
  - `Money` vốn đã được chuẩn hóa ở `parseMoney`/`toMoney`, nên ở màn giá không có đường nào đưa "18000.00" vào mà không ép kiểu.

### Test cũ đổi kỳ vọng

- Định dạng key giá mới: `save-sale-price`, `color-price-dialog`, `combo-edit-dialog`, `save-order`, `new-receipt-dialog`.
- Câu báo mất trả lời: `create-documents`, `assembly-dialog`, `new-receipt-dialog`.
- `create-documents`, do H1 và M2:
  - hóa đơn vừa tạo không còn PATCH thừa;
  - lần sửa bị từ chối dùng lỗi 409 thay cho 500, vì 500 giờ là "chưa rõ".

### Còn mở

1. `POST /receipts` và các POST tạo không đi qua `createOnce` khi mất trả lời vẫn báo "Chưa ghi sổ.". Cách gửi lại cùng key thì vẫn đúng.
2. Sửa hoặc ra phiên bản mới cho báo giá có sẵn (một PATCH hoặc `:revise`) chưa có cơ chế đọc lại. Nếu mất trả lời, bấm lại có thể gặp 412 (toast STALE, rồi tải lại).
3. Sau khi `:confirm` mất trả lời, nếu người dùng bấm "Lưu nháp", `:confirm` cũ vẫn được gửi lại, vì chỉ trả lời của nó mới cho biết hóa đơn đã xác nhận hay chưa.
4. Server nên nhận Idempotency-Key ở `PATCH /invoices/{id}`, `PUT /invoices/{id}/lines`, `PATCH /quotes/{id}`, `:send` và `:publish`. Khi đó web có thể bỏ bước GET đọc lại.
5. `trimDecimals` coi mọi chuỗi có dạng số thập phân là tiền. Một mã dạng "1.10" sẽ được so bằng với "1.1". Hiện chưa có body tạo nào có trường như vậy.

Status: DONE_WITH_CONCERNS
Summary: Đã sửa H1, M1–M4, L1, L2, L4 và L5 theo review. L3 bỏ qua vì server chưa nhận key ở `:send`/`:publish`. typecheck, lint và test đều xanh (759 test). Mỗi test sửa lỗi đã được thử đỏ khi tạm bỏ phần sửa.
Concerns/Blockers: Năm chỗ báo PartialCommitError vì chưa có API cập nhật (bảng ở trên), và năm điểm trong "Review fixes → Còn mở".
