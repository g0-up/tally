# Review web phase 15 (Danh mục & Nhập hàng) + phase 16 (Bán hàng & POS)

Ngày: 2026-10-18 · Reviewer: code-reviewer · Chế độ: chỉ đọc, không sửa source.

## Phạm vi

- Phase 15: `apps/web/src/features/{catalog,partners,pricing,procurement}/**`, e2e `catalog/procurement`, cùng thay đổi backend (`spec_text`, `lot_no` ở dòng trả NCC, filter `in_mix_group`, openapi).
- Phase 16: `apps/web/src/features/{sales,pos}/**`, `apps/web/src/app/router.tsx`, e2e `pos/invoices/quotes/returns`.
- Không xem lại: typecheck, lint, vitest 303/303, Go vet/lint/race/integration (lead đã chạy). E2E và production bundle chưa chạy, nên báo cáo này không kết luận gì về chúng.
- Đã đối chiếu với server: `apps/api/internal/platform/idempotency/guard.go`, `platform/money/decimal.go`, `procurement/http.go`, `procurement/receipt_posting.go`, `pricing/http.go`, `sales/model.go`, `api/openapi.yaml`.

## Đánh giá chung

Phần cốt lõi đã làm đúng:

- POS checkout có idempotency và chặn double-click.
- Vòng xác nhận ngoại lệ (`withExceptions`) bind đúng `doc_type`/`doc_id`/`version`/payload hash, khớp server.
- Trang in không dùng `innerHTML`, CSS in được scope.
- Route in nằm trong `RequireAuth`, ngoài `AppShell`.

Rủi ro tập trung ở các **luồng nhiều bước gọi những endpoint server không có idempotency**. Code client ghi chú rằng "retry sẽ replay", nhưng điều đó sai với các endpoint này. Ngoài ra còn một lỗi **độ chính xác số lượng** ở phiếu xuất.

Không có finding Critical.

---

## High

### H1. Phiếu xuất gửi `base_qty` vượt 4 chữ số thập phân, server trả 422

**Vị trí:**
- `apps/web/src/features/sales/containers/stock-out-dialog.tsx:39`
- `apps/web/src/features/sales/containers/stock-out-dialog.tsx:84`
- `apps/web/src/features/sales/api/adapters.ts:68`

**Kịch bản lỗi:**
1. `max = inLineUnits(cap, unit_factor)` = `cap / factor` ở precision 40. Ví dụ dòng bán theo thùng (factor 24) đã giữ 10 cái, thì `max = 0.41666…6667` (40 chữ số).
2. Người dùng nhận giá trị gợi ý hoặc nhập lại đúng `max`.
3. `base_qty = q × 24` thành `10.0000…0008` hoặc một chuỗi dài tương tự.
4. Server `money.limits.check` (`apps/api/internal/platform/money/decimal.go:65-73`) từ chối vì vượt scale 4 và trả 422. Người dùng không thể xuất phần lẻ của dòng.

Trường hợp `remaining` không chia hết cho `factor` là bình thường: giữ hàng một phần, hoặc đã xuất lẻ trước đó.

**Đề xuất:** Lưu số lượng theo base trong state. Khi `q` bằng `max`, gửi thẳng `cap.toFixed()`. Các trường hợp khác làm tròn `q × factor` về 4 chữ số và kiểm tra `≤ cap`. Hiển thị `max` đã làm tròn xuống 4 chữ số. Làm giống `inOrderUnit` của procurement (`buy-lines.ts:297-300`), hàm này đã xử lý đúng việc này.

### H2. Danh sách hóa đơn bị N+1: mỗi lần tải gọi tới 200 GET chi tiết

**Vị trí:**
- `apps/web/src/features/sales/api/queries.ts:307-310`
- `apps/web/src/features/sales/pages/invoices-page.tsx:40`

**Kịch bản lỗi:**
1. `useInvoiceDetails(list.items.map(id))` tạo một query `GET /invoices/{id}` cho mỗi dòng, và `LIST_LIMIT` trang là 200.
2. Các mutation có invalidate `salesKeys.invoices()` / `POSTING_KEYS` (xác nhận, thu tiền, xuất kho, trả hàng) làm tất cả chi tiết đã stale refetch lại.
3. Với tenant có vài trăm hóa đơn, mỗi thao tác trên màn hình sinh ra hàng trăm request. Chúng tranh rate limit và connection pool của API, và màn hình bị giật.

Nguyên nhân gốc: DTO danh sách server không có trạng thái thanh toán (`apps/api/internal/sales/model.go:219`).

**Đề xuất:** Thêm `paid_amount`/`payment_status` (tóm tắt) vào `InvoiceListItem` ở API và openapi, rồi bỏ `useInvoiceDetails`. Nếu chưa sửa backend được, chỉ fetch chi tiết cho các dòng đang hiển thị hoặc đang mở drawer.

### H3. Lưu và chốt PO không idempotent, retry tạo PO trùng (có thể đã chốt)

**Vị trí:**
- `apps/web/src/features/procurement/api/mutations.ts:18-20` (comment khẳng định retry sẽ replay)
- `apps/web/src/features/procurement/api/mutations.ts:73-82`

**Server:**
- `POST /purchase-orders` không có idempotency (`apps/api/internal/procurement/http.go:311`, openapi không có header).
- `:confirm` dùng `CommandID` theo request (`http.go:399`).
- `POST /skus/{id}/sale-price` không có idempotency (`apps/api/internal/pricing/http.go:353`).

**Kịch bản lỗi:**
1. "Lưu & chốt đơn": POST tạo PO thành công, sau đó `:confirm` bị lỗi (mạng hoặc 412), hoặc `writeSellPrices` lỗi sau khi đã chốt.
2. `useCommit` giữ nguyên Idempotency-Key và báo "Chưa ghi sổ.", nên người dùng bấm lại.
3. POST tạo **PO thứ hai**. Nếu lỗi xảy ra ở bước giá bán thì cả hai PO đều đã chốt, và nợ dự kiến/hàng chờ nhận bị nhân đôi.

**Đề xuất:** Thêm `progress` ref giống `useSaveInvoice`: nhớ `id`/`version` của PO đã tạo, bước nào đã xong. Khi retry thì bỏ qua POST, chỉ chạy tiếp bước còn thiếu. Nếu lỗi xảy ra sau khi PO đã chốt thì báo "Đã chốt PO …, cập nhật giá bán thất bại" thay vì "Chưa ghi sổ". Tốt hơn nữa là server hỗ trợ `Idempotency-Key` cho `POST /purchase-orders`. Đồng thời sửa comment ở dòng 18-20 cho đúng.

### H4. Phiếu nhập nhiều bước: báo sai "Chưa ghi sổ" khi PN đã ghi sổ, retry kẹt hoặc trùng

**Vị trí:** `apps/web/src/features/procurement/api/mutations.ts:170-200`.

Chuỗi gọi: `POST /receipts` (idempotent), rồi `/receipts/{id}/landed-costs`, rồi `POST /supplier-documents`, rồi `sale-price`.

**Server:**
- `POST /supplier-documents` không có idempotency (`procurement/http.go:576`).
- Có UNIQUE `(tenant_id, supplier_party_id, document_no)` (`apps/api/migrations/000014_procurement_documents_returns.up.sql:47`).

**Kịch bản lỗi:**
1. PN đã ghi sổ (tồn kho và công nợ NCC đã tăng). Bước chứng từ NCC hoặc giá bán lỗi, toast hiện "Chưa ghi sổ." nên người dùng hiểu sai trạng thái.
2. Bấm lại cùng intent:
   - receipts và landed-costs được replay;
   - nếu lần trước `supplier-documents` đã tạo xong rồi mới lỗi ở bước giá bán, lần này nó trả 409 trùng số hóa đơn. Intent kẹt vĩnh viễn và mọi lần retry đều thất bại.
3. Nếu người dùng đóng hộp thoại rồi mở lại để nhập, key mới tạo **PN thứ hai**, nhân đôi tồn kho và nợ NCC.

**Đề xuất:**
- Dùng `progress` ref theo từng bước và bỏ qua bước đã xong.
- Coi 409 trùng số chứng từ ở bước này là đã xong.
- Khi PN đã ghi sổ thì phân biệt thông báo: "Đã ghi sổ PN-…; chưa gắn hóa đơn NCC / chưa cập nhật giá bán", và đóng hộp thoại.
- Về lâu dài: server nhận `Idempotency-Key` cho `supplier-documents`, hoặc gộp hóa đơn NCC vào lệnh tạo PN.

---

## Medium

### M1. Vượt dung sai 5% bị chặn cứng ở client, không có hộp thoại ngoại lệ như spec

**Vị trí:**
- `apps/web/src/features/procurement/buy-lines.ts:178-180`
- `apps/web/src/features/procurement/buy-lines.ts:322`

**Spec:** phase-15 dòng 117 ("Over-tolerance returns `ExceptionRequired` → shared exception dialog") và dòng 170 (e2e "over 5% shows exception dialog").

**Thực tế:**
- Client trả về `overTolerance` message, và không gửi request.
- Server không có loại ngoại lệ này. `checkOrderQty` trả 409 `receipt_exceeds_order` cho phần vượt pending không gắn cờ excess (`receipt_posting.go:337-352`). Ngoài ra server **không giới hạn** phần `condition: excess`, nên mức 5% chỉ được thực thi ở client.

**Kịch bản lỗi:**
- Chủ cửa hàng không thể nhận hàng vượt quá 5% kể cả khi chấp nhận ngoại lệ.
- Bước e2e trong spec sẽ fail.
- Client khác (API trực tiếp) thì bỏ qua được giới hạn 5%.

**Đề xuất:** Cần quyết định sản phẩm và backend:
- Hoặc server trả `ExceptionRequired` (kind `receipt_over_tolerance`) khi excess vượt 5%, và client dùng `withExceptions`;
- Hoặc sửa spec và e2e để ghi nhận chặn cứng là hành vi chủ đích.

### M2. `useSaveInvoice` retry sau khi confirm lỗi hoặc bị hủy: paid-amount 409 kẹt và vòng 412

**Vị trí:** `apps/web/src/features/sales/api/invoice-mutations.ts:111-145`.

Sau khi `:confirm` thất bại hoặc người dùng hủy hộp thoại ngoại lệ, `progress` và key được giữ lại, và form vẫn mở để sửa.

**Kịch bản lỗi:**
- (a) Người dùng sửa "Khách trả" rồi gửi lại. `PUT /paid-amount` dùng lại `${key}:paid` với body khác, server trả 409 `idempotency_key_reused` (guard so sánh request hash). Mọi lần retry sau đều 409 cho đến khi đóng hộp thoại.
- (b) Người dùng sửa dòng hàng hoặc header:
  1. PATCH/PUT tăng version lên V+1.
  2. `paid-amount` replay response cũ, có version V.
  3. `invoice` bị gán lại bằng bản cũ, nên `:confirm` gửi `If-Match: V` và nhận 412.
  4. Handler 412 toàn app refetch, nhưng `progress.version` lại bị ghi đè bằng V, nên lỗi lặp lại.

Thêm vào đó, `POST /invoices` không có idempotency. Nếu response bị mất ở bước tạo nháp (trước khi `progress` được ghi) thì retry tạo hóa đơn nháp thứ hai.

**Đề xuất:**
- Đưa `paidNow` (hoặc hash của amount) vào key của bước paid, hoặc đánh dấu `paidDone` trong `progress` và bỏ qua bước đó khi retry.
- Không gán `invoice` bằng response replay nếu `version` của nó nhỏ hơn version hiện có.
- Server: thêm `Idempotency-Key` cho `POST /invoices`.

### M3. Báo giá "Tạo & gửi" retry tạo báo giá trùng

**Vị trí:** `apps/web/src/features/sales/api/quote-mutations.ts:43-46`.

**Kịch bản lỗi:** `POST /quotes` thành công, `:send` lỗi (mạng hoặc 412). Bấm lại thì POST tạo báo giá thứ hai, vì endpoint này không có idempotency và mutation không có `progress` ref.

**Đề xuất:** Dùng `progress` ref như hóa đơn. Khi retry thì chỉ gọi `:send` cho báo giá đã tạo. Khi lỗi thì invalidate danh sách để nháp hiện ra.

### M4. Ngoại lệ hạn mức nợ / giá dưới sàn được tính và chặn ở client, không dựa vào `:check`

**Vị trí:** `apps/web/src/features/sales/containers/invoice-dialog.tsx:118-152`.

**Kịch bản lỗi:**
- Client tự tính `exposure > limit` từ dữ liệu có thể đã cũ (ledger cache, credit limit). Nó chặn submit nếu chưa tick ô ngoại lệ, kể cả khi server sẽ chấp nhận.
- Ngược lại, khi client tính sai theo hướng cho qua, luồng vẫn rơi vào `withExceptions`. Kết quả là hai cơ chế song song, và thông điệp có thể mâu thuẫn.
- Spec yêu cầu luồng do preview `:check` dẫn dắt.

**Đề xuất:** Lấy danh sách breach từ `POST /invoices/{id}:check` (đã có ở `formConfirmations`) để hiển thị và quyết định có hiện ô tick. Dữ liệu client chỉ dùng làm gợi ý, không dùng làm điều kiện chặn.

---

## Low

### L1. Chiết khấu bị trôi vài đồng khi lưu lại

`apps/web/src/features/sales/components/line-items/draft.ts:70-74,105,127`.

`percentOf` làm tròn % về 2 chữ số, sau đó `discountAmount` tính ngược ra tiền. Ví dụ CK 1.000đ trên 30.000đ: 3,33% × 30.000 = 999đ. Mở rồi lưu lại một hóa đơn hoặc báo giá không sửa gì vẫn đổi số tiền CK và tạo version mới.

**Đề xuất:** Giữ `discount_amount` gốc khi người dùng không sửa ô %, chỉ tính lại khi % thay đổi.

### L2. Danh bạ khách hàng chỉ lấy 200 bản ghi

`apps/web/src/features/sales/api/queries.ts:104`.

`useCustomerDirectory` dùng `limit: LIST_LIMIT`. Với hơn 200 khách, tên ở danh sách hoặc filter sẽ hiện thiếu hoặc rơi về id.

**Đề xuất:** Dùng tên có sẵn trong DTO danh sách, hoặc tra theo id khi thiếu.

### L3. POS: checkout giỏ đã lưu bị mất response

`apps/web/src/features/pos/api/mutations.ts`, luồng PATCH rồi `:checkout`.

Nếu response `:checkout` bị mất và người dùng retry, key `:checkout` replay được. Tuy nhiên bước PATCH chạy lại trên giỏ đã bán thì nhận `cart_sold`. Người dùng thấy lỗi thay vì số hóa đơn HD vừa tạo.

**Đề xuất:** Khi gặp `cart_sold` trong cùng intent, đọc lại giỏ hoặc hóa đơn để hiển thị kết quả thành công.

### L4. Header `Idempotency-Key` gửi cho endpoint không hỗ trợ

`apps/web/src/features/pricing/api/mutations.ts:25`, `procurement/api/mutations.ts:53-55`.

Header này cho cảm giác an toàn giả. `sale-price` là thao tác "đặt giá" nên retry ít gây hại, có thể chỉ thêm một dòng lịch sử giá. Chỉ cần sửa comment hoặc ghi chú, hoặc thêm hỗ trợ ở server.

---

## Đã kiểm tra, không thấy vấn đề

- **Băm và bind ngoại lệ:** `withExceptions`/`withRetry`, POS `pos_checkout`/`pos_cart`, đổi hàng bind theo doc gốc, hóa đơn bind `inv.Version`. Tất cả khớp `PayloadHash` của server (đã loại `exception_confirmation_ids`).
- **POS:**
  - Idempotency-Key cho mỗi intent, `inFlight` chặn double submit, `useSettledPricing` chờ giá xong mới cho thanh toán.
  - Tiền mặt bị cap ở số phải trả, kèm `cash_tendered_amount`.
  - Ghi nợ chỉ cho khách có tên.
- **Trả và đổi hàng, hoàn tiền, cọc, xuất kho:** đều có `Idempotency-Key` ở cả client và openapi.
- **Trả NCC:** qty theo base, có kiểm tra `returnable_base_qty`, gửi `lot_no`, `settlement: credit_payable`.
- **Phiếu nhận theo PO:** đổi về đơn vị đặt hàng có kiểm tra 4 chữ số (`inOrderUnit`), tách phần vượt thành dòng `excess`.
- **Bảo mật:**
  - Trang in render bằng JSX, không có `dangerouslySetInnerHTML`.
  - Route in yêu cầu đăng nhập.
  - Không thấy secret trong code hoặc fixture.
- **Xử lý 401/412:** dùng handler toàn app của `useCommit`. 409/422 đi qua `translateSalesError`/`procurementCall`, ra thông điệp tiếng Việt.

---

## Đánh giá Todo

E2E chưa chạy nên không tính.

### Phase 15

**Hoàn thành:**
- Trang Hàng hóa, Nhóm trộn, Nhà cung cấp, Bảng giá (sửa giá).
- Danh sách, tạo, chốt và đóng thiếu PO.
- Phiếu nhận theo PO và nhập nhanh (lô, hạn dùng, màu, chi phí vận chuyển).
- Trả NCC, khớp hóa đơn NCC.
- Backend: `spec_text`, `lot_no`, `in_mix_group` và openapi.

**Chưa đầy đủ:**
- Hộp thoại ngoại lệ khi vượt dung sai 5% (M1).
- `LineItemsEditor` base dùng chung chưa tách: preset mua nằm trong `features/procurement`.
- Doc opener cho tiền tố PC chưa đăng ký.
- Tính toàn vẹn khi retry các luồng PO và PN (H3, H4).

### Phase 16

**Hoàn thành:**
- POS (giỏ, giữ giỏ, copy, thanh toán, fallback ca).
- Hóa đơn (nháp, xác nhận, giữ thêm, hủy phần còn lại, cọc, thu tiền).
- Phiếu xuất, trả và đổi hàng, hoàn tiền.
- Báo giá (gửi, sửa, chuyển hóa đơn).
- Trang in và route.

**Chưa đầy đủ:**
- Preset bán và báo giá nằm trong `features/sales`, chưa nằm ở `shared/ui` như spec.
- Ngoại lệ hóa đơn chưa do `:check` dẫn dắt (M4).
- Lỗi precision phiếu xuất (H1).
- N+1 danh sách hóa đơn (H2).

## Hành động đề xuất (theo thứ tự)

1. H1: sửa `base_qty` phiếu xuất. Thay đổi nhỏ, chặn được 422 thực tế.
2. H3, H4: thêm `progress` ref cho PO và PN, phân biệt thông báo khi chứng từ đã ghi sổ. Lên kế hoạch thêm idempotency server cho `POST /purchase-orders` và `/supplier-documents`.
3. H2: thêm trạng thái thanh toán vào DTO danh sách hóa đơn.
4. M1: chốt quyết định sản phẩm về vượt dung sai (ngoại lệ server hay chặn cứng), rồi đồng bộ spec và e2e.
5. M2, M3: sửa key bước paid và chặn replay version cũ, thêm `progress` cho báo giá.

## Câu hỏi còn mở

- Vượt dung sai 5% nên là ngoại lệ chủ cửa hàng duyệt được (cần backend) hay chặn cứng? Spec và code hiện mâu thuẫn.
- Có chấp nhận thêm idempotency server cho `POST /invoices`, `/quotes`, `/purchase-orders`, `/supplier-documents` trong phạm vi hiện tại không?
