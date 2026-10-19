# Review: web gửi Idempotency-Key cho 7 lệnh POST

- Người review: code-reviewer. Ngày: 2026-10-18.
- Đối tượng: `plans/reports/fullstack-developer-261018-1620-web-idempotency-keys.md`.
- Phạm vi đọc:
  - `apps/web/src/shared/hooks/create-once.ts` và test của nó.
  - `shared/api/api-error.ts`.
  - Các mutation trong `features/{sales,procurement,kits,pricing,inventory}/api`.
  - Phía server để đối chiếu: `apps/api/internal/platform/idempotency/guard.go`, `platform/httpx/bind.go`, `sales/invoices.go`, `pricing/http.go` và `pricing/writer.go`.
- `apps/web` đang ở trạng thái untracked nên không có `git diff`. Tôi đọc thẳng các file.
- Chỉ review, không sửa code.

## Kiểm tra đã chạy (trong `apps/web`)

| Lệnh | Kết quả |
|---|---|
| `corepack pnpm exec vitest run src/shared src/features/{sales/api,procurement,kits,pricing,inventory}` | 42 file, 187/187 pass |
| `corepack pnpm typecheck` | Sạch (exit 0) |
| `corepack pnpm exec eslint` trên các thư mục đã sửa | Sạch |

Tôi định thử mutation testing (sửa bản sao rồi xem test có đỏ không) trong scratchpad, nhưng hook chặn truy cập `node_modules`, và tôi không được sửa source trong repo. Vì vậy mục 6 được đánh giá bằng cách đọc assert.

## Đánh giá tổng quan

`createOnce` đúng với mô hình của server:
- Server chỉ lưu response 2xx. Lần bấm sau gửi lại body đang giữ trước, nên không có 409 `idempotency_key_reused` do chính client gây ra.
- Mỗi intent có state riêng. Hook nằm trong dialog được mount có điều kiện, nên body đang giữ không rò sang intent khác.

Lỗi thật nằm ở **các bước sau lệnh tạo**:
- Luồng hóa đơn lần nào thử lại cũng PATCH và PUT lại, nên kẹt vĩnh viễn trong hai kịch bản thường gặp.
- Key con của giá bán chứa giá trị, nên khi giá được đổi qua lại thì server replay và giá bị bỏ ghi mà không ai biết.
- Response bị mất ở PATCH hoặc PUT gây 412 giả mãi mãi.
- Khi chưa rõ kết quả, UI vẫn báo "Chưa ghi sổ.", và người dùng có thể đóng hộp thoại rồi tạo trùng.

---

## Critical

Không có.

## High

### H1. Hóa đơn: lần nào thử lại cũng PATCH và PUT lại, nên kẹt vĩnh viễn khi đã có tiền khách trả hoặc khi đã xác nhận

**Vị trí:**
- `features/sales/api/invoice-mutations.ts:127-138` (`target = earlier ?? …`, `update` luôn PATCH rồi PUT)
- `:152-153`
- `:176-182` (`editsNotSaved`)

**Tình huống A: đã thu tiền, xác nhận thất bại**
1. Tạo hóa đơn mới với "Khách đã thanh toán" 50.000 và bấm "Lưu & xác nhận bán".
2. `POST /invoices` thành công (v1). `PUT paid-amount` thành công (v2). `:confirm` bị từ chối, ví dụ thiếu tồn, chế độ chỉ đọc, chủ shop hủy hộp xác nhận ngoại lệ, hoặc mất mạng.
3. Bấm lại. `earlier` có giá trị nên chạy `update`. PATCH thành công (v3). PUT `/lines` bị server từ chối với `invoice_has_payments` (`sales/invoices.go:201-207, 301`).
4. `created !== null` và `paying === false`, nên client ném `editsNotSaved`. Toast hiện: "Đã tạo **SO-…** nhưng chưa lưu phần sửa sau: Hóa đơn đã có tiền khách trả nên không xóa được. Bấm lại để lưu tiếp."
5. Câu này sai cả hai ý: người dùng không sửa gì, và cũng không xóa gì. Câu "không xóa được" lấy từ `SALES_MESSAGES.invoice_has_payments` (`sales/api/mutations.ts:30`).
6. Bấm lại bao nhiêu lần cũng lặp đúng như vậy. Bước `:confirm` không bao giờ được chạy lại. Đường duy nhất là đóng hộp thoại rồi xác nhận từ drawer.

**Tình huống B: mất response của `:confirm`**
1. Tạo hoặc sửa hóa đơn. `:confirm` thực tế đã chạy trên server nhưng response bị mất.
2. Bấm lại thì PATCH nhận `invoice_not_editable` vì hóa đơn không còn là nháp (`invoices.go:178-179`).
3. Với hóa đơn vừa tạo, toast báo "Đã tạo SO-… nhưng chưa lưu phần sửa sau: …", trong khi thực tế hóa đơn đã xác nhận và đã giữ hàng.
4. Bước `:confirm` có key `${key}:confirm` và lẽ ra sẽ replay thành công, nhưng không bao giờ chạy tới.

Ghi chú thêm:
- Server luôn set `due_date` và `note` khi client gửi (`invoices.go:243-251`), nên `unlessUnchanged` không bao giờ bắt được `nothing_changed`.
- Hệ quả là mỗi lần bấm lại tăng version hai lần và ghi hai audit event `invoice.update` dù không có gì thay đổi.
- Phần logic "thử lại thì cập nhật lại" có thể đã có từ trước. Tuy vậy, thay đổi này nằm ngay trên đường đó (`editsNotSaved`, progress theo version) và làm câu báo sai thêm.

**Đề xuất:**
- Lưu vào `progress` cả header và lines mà intent đã ghi (so bằng `canonicalJson`).
- Khi thử lại, chỉ PATCH nếu header khác, chỉ PUT nếu lines khác. Nếu không khác gì thì bỏ qua cả hai và đi tiếp sang `paid-amount` (replay theo key) rồi `:confirm` (replay theo key).
- Thêm test cho hai tình huống: (A) đã thu tiền và confirm bị từ chối, sau đó thử lại; (B) confirm mất response, sau đó thử lại.

## Medium

### M1. Key con của giá bán chứa giá trị, nên đổi giá qua lại thì server replay và bỏ ghi lặng lẽ

**Vị trí:**
- `features/pricing/api/mutations.ts:27-35` (`salePriceKey`)
- `features/inventory/api/mutations.ts:86-101`
- `features/procurement/api/mutations.ts:57` (`id: price:${sku}:${retail}`)
- `features/kits/api/mutations.ts:106-125`

**Cơ chế:** `POST /skus/{id}/sale-price` chạy qua `idempotency.Once` (`pricing/writer.go:449`). Gửi cùng key và cùng body thì server trả lại response đã lưu và **không ghi gì**.

**Tình huống (hộp thoại giá bán, cùng một intent):**
1. Giá lẻ 18.000 ghi thành công với key `…:retail:general:18000:…`. Giá sỉ lỗi.
2. Sửa giá lẻ thành 19.000 và bấm lại. Key mới, giá được ghi. Giá sỉ lại lỗi.
3. Sửa giá lẻ về 18.000 và bấm lại. Key `…:18000:…` đã có trên server nên server replay. Giá trên server vẫn là 19.000.
4. Giá sỉ lần này thành công, toast báo "Đã cập nhật giá chung.".

Các luồng khác có cùng lỗi:
- **Procurement** còn tệ hơn: `done` đã chứa `price:sku:18000` nên bước bị bỏ qua ngay từ client.
- **Kit**: cũng xảy ra, sau ba lần đổi (A→B→A→B).
- **Giá màu**: `done` theo kind giữ 19.000, nên client vẫn gửi, nhưng gửi đúng key đã dùng và bị replay.

**Đề xuất:**
- Key nên đi theo "ô giá và số thứ tự lần ghi", không theo giá trị. Trong mỗi intent, giữ map `slot → {body, key}` với slot là `${sku}:${kind}:${scope}`.
  - Body giống lần gửi trước: dùng lại key (thử lại an toàn).
  - Body khác: tạo key mới `…:${slot}:${n+1}`.
- Ở procurement, `done` cần nhớ giá trị đã ghi cho từng SKU, thay vì giữ tập id có kèm giá.
- Thêm test cho chuỗi A→B→A.

### M2. Mất response của PATCH hoặc PUT dẫn tới 412 giả mãi mãi, kèm hai toast mâu thuẫn

**Vị trí:**
- `features/sales/api/invoice-mutations.ts:131-137`
- `features/sales/api/quote-mutations.ts:57-63`
- `features/sales/api/mutations.ts:114-117`

**Tình huống (hóa đơn vừa tạo, đang ở bước ghi phần sửa):**
1. PATCH thành công (v2, `progress` ghi v2). PUT `/lines` thực tế đã ghi (v3) nhưng response bị mất.
2. Toast: "Đã tạo SO-… nhưng chưa lưu phần sửa sau: Không kết nối được…".
3. Bấm lại: PATCH với If-Match "2" trong khi server đang ở v3, nên nhận 412.
4. Toast stale chung (`app/platform.ts:39`) báo "Dữ liệu đã thay đổi ở nơi khác. Tải lại để tiếp tục.".
5. Cùng lúc, toast PartialCommit báo "… chưa lưu phần sửa sau: Dữ liệu đã thay đổi ở nơi khác. Tải lại để tiếp tục. Bấm lại để lưu tiếp.". Lời khuyên "Tải lại" và "Bấm lại" mâu thuẫn nhau.
6. `progress` ghim v2 nên bấm lại luôn nhận 412, dù dữ liệu thực ra đã lưu đủ.

Báo giá bị y hệt: `update` dùng `q.version` từ `state.result`.

**Đề xuất:**
- Khi gặp 412 trong `update`: GET chứng từ. Nếu header và lines trên server đã khớp với form thì nhận version đó và đi tiếp. Nếu không khớp thì báo "chứng từ đã bị sửa ở nơi khác, mở lại để kiểm tra" và đừng bảo "Bấm lại".
- Hướng lâu dài: đề nghị API nhận Idempotency-Key cho `PATCH /invoices/{id}`, `PUT /invoices/{id}/lines`, `PATCH /quotes/{id}` (đưa vào danh mục API còn thiếu).
- Thêm test: PUT trả `HttpResponse.error()` nhưng mock vẫn tăng version.

### M3. Chưa rõ kết quả mà vẫn báo "Chưa ghi sổ.", người dùng đóng hộp thoại thì key mới ra đời và tạo trùng

**Vị trí:**
- `shared/hooks/create-once.ts:26-31, 71-76`
- `shared/ui/.../commit-dialog` (`onClose` chỉ bị chặn khi `pending`, dòng 60)
- Các dialog được mount có điều kiện, ví dụ `procurement/pages/orders-page.tsx:96`, `sales/containers/sales-overlay-host.tsx:74-82`

**Tình huống:**
1. `POST /purchase-orders` (hoặc `/invoices`, `/quotes`, `/kits`, `/assembly-orders`) mất response. Server có thể đã tạo chứng từ.
2. Toast hiện "**Chưa ghi sổ.** Không kết nối được máy chủ. Kiểm tra mạng rồi thử lại.".
3. Người dùng tin là chưa lưu, đóng hộp thoại và mở lại. Hook unmount nên `CreateState` và key mất theo.
4. Lần gửi mới dùng key mới, server tạo thêm một chứng từ thứ hai. Đây đúng là kịch bản "key mới sinh ra khi kết quả lần trước còn chưa rõ".
5. `createOnce` chỉ bảo vệ các lần bấm lại khi hộp thoại vẫn mở.

**Đề xuất:**
- Khi `outcomeUnknown(e)` xảy ra ở lệnh tạo, đổi câu báo, ví dụ: "Chưa rõ đã ghi hay chưa. Bấm lại (đừng đóng hộp thoại) để kiểm tra, hệ thống sẽ không tạo trùng."
- Cho `CommitDialog` hỏi xác nhận trước khi đóng khi intent còn body đang giữ (`state.sent !== null && state.result === null`).
- Không cần lưu xuống storage.

### M4. Phiếu nhập: xóa số hóa đơn NCC sau khi lần trước mất response thì intent kẹt và câu báo khẳng định một điều chưa biết

**Vị trí:** `features/procurement/api/mutations.ts:257-272`

**Tình huống:**
1. Bước "gắn hóa đơn NCC" với số `HD01` mất response. `stored.invoice.sent = HD01`, `result = null`, chưa rõ server đã lưu hay chưa.
2. Người dùng xóa số hóa đơn (ô có ghi "nếu có") và bấm lại. Bước invoice không được thêm vào, nên body đang giữ không được gửi lại và kết quả vẫn chưa rõ.
3. Dòng 270-271 thấy `matched.document_no ("HD01") !== ""` nên ném "Đã ghi PN-…. Đã đối chiếu theo số hóa đơn HD01 của lần bấm trước. Số vừa sửa chưa được lưu.". Câu này có thể sai, vì server có thể chưa lưu HD01.
4. Bấm lại bao nhiêu lần cũng ra đúng câu đó, intent không bao giờ xong.

**Đề xuất:**
- Chỉ khẳng định "đã đối chiếu" khi `stored.invoice.result !== null`.
- Khi `result === null` và `sent !== null`, vẫn chạy bước invoice để gửi lại body đang giữ và có câu trả lời, rồi mới so số.
- Thêm test cho trường hợp xóa số hóa đơn sau khi mất response.

## Low

| # | Vị trí | Vấn đề và tình huống | Đề xuất |
|---|---|---|---|
| L1 | `shared/hooks/create-once.ts:34-35, 71-76` | `reauth_required` và phiên hết hạn không được coi là "từ chối", nên body đầu tiên vẫn được giữ dù server không lưu gì (middleware chặn trước handler). Ví dụ: người dùng hủy hộp nhập mật khẩu, sửa form, bấm lại. Body cũ được gửi và **tạo** chứng từ theo nội dung cũ. Với PO và lệnh đóng giỏ, câu báo còn nói "theo nội dung lần bấm trước, lần đó chưa nhận được trả lời", điều này không đúng. | Khi lỗi xảy ra ở lần gửi đầu của một body (trước đó chưa có body nào đang giữ), bỏ body đang giữ đi với reauth và session. Khi đang gửi lại body cũ thì giữ nguyên như hiện tại. Thêm test. |
| L2 | `features/pricing/api/mutations.ts:47-55` | `useSaveSalePrice`: giá lẻ ghi xong, giá sỉ lỗi, toast vẫn báo "**Chưa ghi sổ.** …". Không invalidate nên bảng vẫn hiện giá lẻ cũ. | Dùng `runFollowUps` hoặc `PartialCommitError`, ví dụ "Đã lưu giá bán lẻ nhưng chưa lưu giá sỉ: …", với `invalidate` tĩnh. |
| L3 | `features/sales/api/quote-mutations.ts:67-71`, `kits/api/mutations.ts:160-171` | `:send` (báo giá) và `:publish` (kit) chỉ dùng If-Match, không có key. Mất response thì bấm lại nhận 412 hoặc lỗi trạng thái mãi, trong khi thực tế đã gửi hoặc đã phát hành. | Khoảng trống API. Tạm thời: khi gặp 412 thì GET lại, nếu trạng thái đã là `sent`/`active` thì coi bước đó là xong. |
| L4 | `features/kits/api/adapters.ts:83-84`, `mutations.ts:206-208` | Câu `createdAsSent` chỉ nói loại, mã và đơn vị không đổi được. Thực ra tên và công thức vừa sửa cũng chưa được lưu, vì lỗi được ném trước bước `draftStep`. | Thêm vào câu: "Tên và thành phần vừa sửa chưa được lưu." |
| L5 | `create-once.ts:39`, `procurement/api/mutations.ts:57`, `pricing/api/mutations.ts:29` | Tiền được so như chuỗi nguyên dạng: "18000" khác "18000.00". Hệ quả là ghi giá thừa một lần (dev đã ghi chú), hoặc PO và lệnh đóng giỏ báo nhầm "thay đổi sau đó chưa được lưu" dù giá trị không đổi. | Chuẩn hóa số tiền và số lượng bằng `dec(x).toString()` khi dựng body, hoặc khi dựng id và key của bước. |

## Trả lời 6 câu hỏi của team-lead

1. **Có tạo trùng chứng từ không?** Trong một intent thì không: key chỉ đổi sau 2xx, sau `resetKey` hoặc khi unmount. Body chưa rõ kết quả luôn được gửi lại trước. Rủi ro còn lại là M3 (đóng hộp thoại khi chưa rõ kết quả). `idempotency_key_in_progress` gần như không xảy ra, vì `Begin` chặn trên dòng key cho tới khi transaction đầu kết thúc. Client xử lý nó là "chưa rõ" là đúng.
2. **Có 409 hoặc 412 giả không?**
   - Không thấy 409 `idempotency_key_reused` nào client tự gây ra. Key giá bán chứa đủ các trường của body. Path có `skuId` và cũng nằm trong hash.
   - Độ dài key tệ nhất khoảng 160 ký tự, dưới giới hạn 200 (`httpx/bind.go:30`).
   - 412 giả có thật: M2 (mất response của PATCH hoặc PUT), L3, và hai trường hợp H1 tương tự.
   - Riêng chỗ team-lead hỏi, "hóa đơn PATCH thành công nhưng PUT lỗi": khi PUT bị **từ chối** thì đã đúng (test `PATCH "1", PUT "2", PATCH "2", PUT "2"`). Khi PUT **mất response** thì sai (M2).
   - Key con của giá không gây 409, nhưng gây replay sai (M1).
3. **Body đang giữ có được xóa đúng lúc không?** Có.
   - 4xx: bỏ body.
   - 2xx: key mới tạo state mới.
   - `resetKey` và unmount: ref mất theo.
   - Không rò sang intent khác vì mọi ref đều gắn theo key, và các dialog được mount có điều kiện. `InvoiceForm` của hóa đơn có sẵn còn được key theo `id:version`.
   - Ngoại lệ là reauth và session (L1).
4. **`canonicalJson` có đúng không?** Đúng cho mục đích so sánh.
   - Khóa được sắp xếp. Thuộc tính `undefined` bị bỏ, giống `JSON.stringify` của body thật gửi đi. Thứ tự mảng được giữ (thứ tự dòng có ý nghĩa). Tiền là chuỗi nên không có float.
   - Server tự tính hash trên bytes nhận được (`guard.go:61-80`, `UseNumber`). Client gửi lại đúng object đang giữ nên bytes giống hệt.
   - Hạn chế duy nhất là L5.
5. **Lời nhắn PartialCommitError có nêu số chứng từ và đúng tiếng Việt không?**
   - Có số chứng từ ở: `editsNotSaved` (SO-/BG-), `orderCreatedAsSent`, `assemblyCreatedAsSent`, `partlyStored`, và "Đã ghi PN-…" của phiếu nhập.
   - Riêng `useMatchInvoice` chỉ nêu số hóa đơn NCC. Chấp nhận được vì hộp thoại chỉ dành cho một phiếu.
   - Các câu sai hoặc mâu thuẫn: H1 ("không xóa được", "phần sửa sau" trong khi không có sửa gì), M2 ("Tải lại" cùng lúc với "Bấm lại"), M4 (khẳng định điều chưa biết), L4.
6. **Test có thật sự đỏ khi bỏ phần sửa không?** Đánh giá bằng cách đọc assert:
   - `create-once.test.ts` sẽ đỏ nếu bỏ một trong ba cơ chế: gửi lại body đang giữ (assert `names()` = `["A","A"]`), bỏ body khi 4xx (`state.sent === null`), hoặc gọi `update` theo version của bản replay (`toHaveBeenCalledWith(... version: 1 ...)`).
   - `create-documents.test.tsx` sẽ đỏ nếu bỏ phần nâng version (assert chuỗi If-Match).
   - `save-order.test.tsx` sẽ đỏ nếu bỏ giá khỏi id hoặc khỏi key (assert key có `19000`).
   - Còn thiếu test cho: reauth và session, thứ tự khóa của `same`, H1 (A và B), M1 (A→B→A), M2 (mất response của PUT), M4.
   - Mock trong test giả định "lỗi mạng nghĩa là server không ghi". Đó đúng là kịch bản mà M2 bỏ sót.

## Các phần đã kiểm và thấy ổn

- **Thứ tự trong `createOnce`:** body đang giữ được gửi trước. Nếu bị từ chối thì gửi body của form trong cùng lần bấm. Nếu `update` lỗi thì giữ `result` để lần sau update tiếp chứ không tạo lại.
- **Không còn POST nào tới 7 endpoint đi ngoài `createOnce` hoặc `postSalePrice`** (đã grep `src/`, trừ pos và settings).
- **`idempotency_key_invalid`** đã có câu tiếng Việt (`api-error.ts:238`). Key không thể vượt 200 ký tự.
- **Đổi qua lại giữa các nút trong cùng hộp thoại an toàn:** "Lưu nháp" và "Lưu & gửi" (báo giá), "Lưu nháp" và "Lưu & chốt" (PO) dùng chung key. Mode không nằm trong body nên không gây 409.
- **`usePerRowCommit`** (reserve, lô) đã sửa được lỗi dùng lại key giữa các dòng mà review phase 17 nêu.

## Việc nên làm theo thứ tự ưu tiên

1. H1: chỉ PATCH hoặc PUT phần thật sự khác so với cái intent đã lưu. Thêm hai test.
2. M1: key giá theo ô giá và số thứ tự lần ghi, kèm test A→B→A.
3. M2: xử lý 412 trong `update` bằng cách GET rồi so. Đưa PATCH/PUT có key vào danh mục API còn thiếu.
4. M3: câu báo riêng cho trường hợp chưa rõ kết quả, và hỏi xác nhận khi đóng hộp thoại.
5. M4, sau đó L1–L5.

Status: DONE_WITH_CONCERNS
Summary: `createOnce` đúng mô hình server: không tự gây 409, body đang giữ được xóa đúng lúc, không rò giữa các intent, `canonicalJson` ổn. Có 1 High: hóa đơn thử lại luôn PATCH và PUT, nên kẹt vĩnh viễn khi đã thu tiền hoặc khi đã xác nhận mà mất response. Có 4 Medium: key giá theo giá trị nên đổi qua lại thì bị replay và bỏ ghi; 412 giả khi PATCH hoặc PUT mất response; báo "Chưa ghi sổ." khi chưa rõ kết quả rồi đóng hộp thoại gây tạo trùng; phiếu nhập kẹt khi xóa số hóa đơn. Typecheck, lint sạch, 187/187 test liên quan pass.
Concerns/Blockers: Không chạy được mutation testing (hook chặn `node_modules` trong scratchpad, và không được sửa source), nên mục 6 dựa trên đọc assert. Logic "thử lại thì cập nhật lại" của H1 có thể đã có từ trước thay đổi này, nhưng vẫn nằm trên đường mà thay đổi vừa chạm vào.
