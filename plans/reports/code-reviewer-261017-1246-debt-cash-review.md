# Code Review — Phase 10: Debt & Cash

- Thời điểm: 2026-10-17 (Asia/Saigon)
- Reviewer: code-reviewer (read-only; không sửa code, không commit, không chạm DB dev `tally-postgres-1`)
- Đối chiếu:
  - `phase-10-debt-cash.md`
  - `design/database-conventions.md`, `design/database-schema-debt-cash-crm-saas.md` §1–2, `design/architecture.md`, `design/ubiquitous-language.md`
  - Báo cáo implementer `plans/reports/fullstack-developer-261017-1240-debt-cash.md`

## Phạm vi

- `apps/api/internal/debt/**`, `apps/api/internal/cash/**`
- `apps/api/migrations/000030_debt_document_columns.*`, `000034_debt_payment_planned_allocations.*` (đọc kèm `000020`, `000022` để đối chiếu RLS/grant/constraint)
- `apps/api/internal/app/`: phần debt/cash của `domains.go`, `routes.go`, `app.go`, `debt_cash_integration_test.go`
- `apps/api/internal/cli/`: `ledger.go` (verify), `serve.go`, `serve_test.go`, `ledger_integration_test.go`
- `apps/api/api/openapi.yaml` (phần debt/cash)
- Đọc thêm để kiểm hợp đồng: `internal/posting/{coordinator,locks,tx,command}.go`, `internal/platform/docseq/issuer.go`, `internal/platform/money`

Các lệnh đã chạy (testcontainers dùng Postgres riêng, không chạm DB dev):

| Lệnh | Kết quả |
|------|---------|
| `go vet` debt, cash, app, cli (có và không có `-tags integration`) | sạch |
| `golangci-lint run` debt, cash, app, cli (có và không có `--build-tags integration`) | 0 issue |
| `go test -race -count=1` debt, cash, cli | ok |
| `go test -race -count=1 -tags integration` debt, cash, cli | ok |
| `go test -race -count=1 [-tags integration] ./internal/app/...` | FAIL, chỉ ở `TestOpenAPIContract`: 24 route inventory (`/stock*`, `/lots*`, `/reservations*`, `/cost-recalculations*`) chưa có trong `openapi.yaml`. Không có route debt/cash nào thiếu. Đây là phần việc inventory đang làm song song, nằm ngoài phạm vi review này. |

## Đánh giá tổng quan

Phần lõi sổ cái làm chắc tay:

- Mọi projection đều đi qua `moveSettled`/`moveAllocated` (kiểm cận 0 ≤ x ≤ amount và suy ra status).
- Settlement, cash entry và statement version chỉ được append (RLS FORCE, `REVOKE UPDATE, DELETE`).
- Reversal dùng mirror âm với unique `ux_obligation_settlements__one_reversal`. Kiểm successor (`refund_of … status <> 'reversed'`) chạy trong lock của payment nguồn.
- Aging as-of replay đúng: chỉ lấy settlement và reversal có `business_date ≤ as_of`.
- Không có netting tự động: advance hiển thị bên cạnh số nợ, không trừ vào.
- Verifier cover obligation, credit note, payment, fund và các mirror.

Tôi không tìm thấy lỗi Critical.

Có một lỗi High: deposit đặt cọc cho hoá đơn mà còn chờ xác minh sẽ không bao giờ tự phân bổ. Đây là luồng mặc định vì phương thức chuyển khoản được seed với `requires_verification = true`.

Có hai lỗi Medium nằm ở port mà procurement/sales sắp dùng (`PaymentRecorder`). Nên sửa trước khi các phase đó gắn vào, vì sửa sau sẽ phải đổi hợp đồng của port.

## Critical

Không có.

## High

### H1. Deposit gắn hoá đơn (`earmarked_invoice_id`) còn `pending_verification` lúc ghi receivable thì không bao giờ được tự phân bổ

Bằng chứng:

- `internal/debt/factory.go:213-240`: `applyEarmarked` chỉ chọn những payment có `p.status = 'confirmed'`, và chỉ chạy đúng một lần, lúc `recordObligation` tạo receivable (`factory.go:189-190`).
- `internal/debt/payments.go:344-393`: `confirmPayment` chỉ áp `p.Planned`, rồi capped theo phần còn mở. Hàm này không đọc `EarmarkedInvoiceID`.
- `PostPayment` và `recordPayment` cũng không tự phân bổ một deposit đã confirmed vào receivable có sẵn của hoá đơn đó. Hiện chỉ có `credit_limit.go` dùng tới `earmarked_invoice_id`.
- `internal/cash/seed.go:206`: phương thức `bank_transfer` được seed với `verify = true`, nên mọi deposit chuyển khoản đều vào hàng chờ xác minh.

Kịch bản lỗi (luồng phổ biến nhất):

1. Khách chuyển khoản đặt cọc 1.500.000 cho hoá đơn X. Payment ở trạng thái `pending_verification`, `earmarked_invoice_id = X`.
2. Cửa hàng giao hàng. Sales gọi `Receivables().Record(... EarmarkedInvoiceID: X)`. `applyEarmarked` không thấy deposit (vì chưa confirmed), nên receivable X mở đủ 100%.
3. Tối đó chủ cửa hàng xác nhận chuyển khoản. `confirmPayment` không có `Planned`, nên deposit thành advance chung và X vẫn mở.

Hậu quả:

- X quá hạn theo `due_date` và xuất hiện trong aging, overdue và nhắc nợ, dù khách đã trả cọc cho chính hoá đơn này.
- `CreditLimitPolicy` của các đơn sau: open receivable gồm cả X, còn deposit chỉ được trừ khi `OrderID = X` (`credit_limit.go:93-95`). Kết quả là `credit_limit_exceeded` báo sai, đúng loại rủi ro plan đã ghi ("Credit limit false positives").
- Trường hợp ngược lại cũng lỗi tương tự: deposit tiền mặt (confirmed) ghi sau khi receivable X đã tồn tại thì cũng không tự phân bổ.

Plan yêu cầu "earmarked deposits allocate automatically to their invoice's documents (`auto_earmarked`)" (`phase-10-debt-cash.md:26`), và bước 12 có test "earmark auto allocation". `debt_integration_test.go` không có test nào cho `applyEarmarked` hay `auto_earmarked`. Grep chỉ ra test credit limit (`:711`).

Đề xuất:

- Trong `confirmPayment` (và trong `recordPayment` khi payment confirmed ngay), nếu `p.EarmarkedInvoiceID != nil` thì tìm các obligation receivable có source là invoice đó, cùng party, còn mở.
- Các obligation này phải được lock theo thứ tự: obligations trước payment. Vì vậy `paymentConfirm.LockKeys` cần biết danh sách này. Pre-read ở `ConfirmPayment` giống cách đang làm với `planned`, rồi kiểm lại trong `Apply` bằng `sameIDs`.
- Sau đó áp `Earmarked{}.Plan` với mode `auto_earmarked`.
- Thêm integration test cho cả ba thứ tự: deposit confirmed trước receivable, deposit pending rồi confirm sau receivable, và deposit confirmed sau receivable.

## Medium

### M1. `PaymentRecorder.Record` lấy số PT/PC (row lock `doc_sequences`) sau khi caller đã giữ các lock khác, nên vi phạm thứ tự lock toàn cục và có thể deadlock với `POST /payments`

Bằng chứng:

- `internal/platform/docseq/issuer.go:31-32`: "Call it inside the posting transaction, before any other row lock (doc_sequences comes first in the global lock order)". Row counter bị khoá tới hết transaction.
- `internal/posting/coordinator.go:168-173`: đường HTTP lấy docseq của `DocPrefix()` (PT/PC, `postings.go:106`) trước khi `Acquire(LockKeys)` (obligations, payments), rồi `Apply`, rồi `cashBook.Record`. Bước cuối gọi `cash.LockAllFunds` (`app/domains.go:131`).
- `internal/debt/recorders.go:76`: recorder gọi `r.s.docs.Next(..., paymentPrefix(...))` bên trong `Apply` của caller. Lúc đó caller đã giữ docseq của chứng từ chính và mọi lock trong `LockKeys`, kể cả obligations/payments mà doc comment `recorders.go:15-17` khuyên khai báo qua `PaymentInput.AddLocks`.

Kịch bản deadlock:

- A. Sales giao hàng thu nợ hoá đơn cũ O. Caller khai `AddLocks`, nên giữ lock O, rồi chờ docseq `PT`. Cùng lúc, chủ cửa hàng ở màn Công nợ `POST /payments` phân bổ vào O. Request này giữ `PT` (coordinator lấy trước) và chờ O. Hai bên chờ nhau thành vòng tròn.
- B. Một posting ghi hai payment khác prefix (ví dụ đổi hàng: hoàn tiền PC rồi thu PT). Sau payment PC, nó đã giữ toàn bộ fund (`LockAllFunds`) và chờ `PT`. Cùng lúc, bất kỳ `POST /payments` PT nào cũng giữ `PT` và chờ fund. Vòng `fund_accounts → doc_sequences` này xảy ra với mọi khoản thu đồng thời của tenant, không cần trùng chứng từ.
- C. Caller hoàn advance P (`AddLocks` giữ payment P) chờ docseq `PC`, trong khi `POST /refunds` cho cùng P giữ `PC` và chờ P.

Postgres phát hiện deadlock sau `deadlock_timeout` (mặc định 1 giây). Coordinator chỉ retry một lần (`coordinator.go:103-108`). Nếu lần retry lại va chạm, client nhận lỗi Internal (500) cho một posting bán hàng. Hiện không có test đồng thời nào cho debt/cash (grep `go func|WaitGroup|errgroup` không có kết quả).

Đề xuất (chọn một):

- (a) Cho command khai báo thêm prefix (ví dụ `ExtraDocPrefixes()`, hoặc một `LockSet` riêng cho doc_sequences). Coordinator lấy tất cả số chứng từ, sắp theo prefix, trước `Acquire`. Recorder nhận `DocNo` đã cấp sẵn qua `PaymentInput`.
- (b) Tối thiểu: nêu rõ trong hợp đồng port rằng caller phải gọi `docseq.Next` cho PT/PC trước mọi lock khác, rồi truyền số vào. `PaymentRecorder.Record` không tự lấy số nữa.

### M2. `PaymentRecorder.Record` replay theo `Key` mà không so giá trị, nên payment khác nội dung bị bỏ qua mà không báo lỗi

Bằng chứng:

- `internal/debt/recorders.go:62-70`: khi `in.Key != ""` và `paymentByKey` tìm thấy, hàm trả ngay payment cũ (`Replayed: true`, `payments.go:304-317`). Nó không so amount, party, method, direction, purpose hay allocations, và không lọc payment đã `reversed`.
- Hai recorder anh em làm khác: `existingObligation` trả `409 obligation_exists` khi khác party hoặc amount (test `debt_integration_test.go:951-972` khẳng định điều này), và `existingCreditNote` cũng trả 409 tương tự.

Kịch bản: sales thanh toán tách 2 phương thức (500.000 tiền mặt + 400.000 chuyển khoản) nhưng đặt cùng một key theo chứng từ (`"sale:<id>:payment"`), một lỗi dễ mắc khi viết caller.

- Lần gọi thứ hai trả về payment tiền mặt cũ với `Replayed = true`.
- Không có PT chuyển khoản, quỹ ngân hàng không ghi nhận gì, receivable còn mở 400.000.
- Không có lỗi nào được trả về, và `ledger verify` vẫn sạch (xem L1).

Trường hợp tương tự: key bị dùng lại sau khi payment cũ đã bị reverse thì cũng trả về payment `reversed` như thể đã thu.

Đề xuất: so các trường định danh (party, direction, purpose, method, amount, source, tập allocations) giống `existingObligation`. Nếu khác thì trả `409 payment_exists`. Thêm test "refuse a changed one" cho payment giống test của obligation.

## Low

### L1. `ledger verify` không đối chiếu payment với cash entry

`internal/debt/verify.go:42-52` kiểm `allocated_amount` của payment. `internal/cash/verify.go:23-33` kiểm `balance = opening + Σ entries`. Không có check nào khẳng định:

- mỗi payment confirmed (trừ `opening_advance`) có đúng một cash entry ±amount trên fund của method;
- mỗi payment reversed có mirror tương ứng;
- payment pending thì không có entry nào.

Kịch bản: một caller đi vòng hoặc lỗi ở `cashBook` (ví dụ lỗi kiểu M2, hoặc reversal bỏ sót `cash.ReversePayment`) làm quỹ lệch với sổ thu/chi, nhưng verify vẫn báo sạch vì cả hai phía đều tự khớp nội bộ. Bất biến chính "tiền vào quỹ = Σ PT/PC" chưa được kiểm.

Đề xuất: thêm vào `debt.Verify` (hoặc một verifier liên miền) truy vấn `payments p LEFT JOIN (SELECT payment_id, SUM(amount) …) e`, so `e.sum` với `CASE status WHEN 'confirmed' THEN ±amount ELSE 0 END`.

### L2. Forecast tính "đã nhận" theo ngày lập phiếu, còn quỹ ghi theo ngày xác nhận

- `internal/debt/queries.go` (Forecast, truy vấn `confirmed`) nhóm theo `p.business_date`, tức ngày lập PT.
- `confirmPayment` ghi cash entry và settlement ngày `w.date`, tức ngày xác nhận (`payments.go:332, 389`).

Kịch bản: PT chuyển khoản lập ngày 10, chủ xác nhận ngày 12. Forecast hiển thị `ConfirmedIn` vào ngày 10, trong khi quỹ ngân hàng và aging ghi nhận vào ngày 12. Hai màn hình lệch nhau 2 ngày cho cùng một khoản tiền.

Đề xuất: nhóm theo `COALESCE(e.business_date, …)` của cash entry, hoặc theo ngày xác nhận (`confirmed_at` quy về ngày nghiệp vụ của tenant).

## Điểm có thể xung đột với agent inventory (`internal/cli`, `internal/app`)

- `internal/cli/ledger.go:27-30`: inventory sẽ phải thêm `{name: "inventory", verify: inventory.Verify}` vào cùng slice `ledgerVerifiers`. Hai agent sửa cùng mấy dòng này, nên cần merge tay. `ledger_integration_test.go` hiện chỉ biết hai verifier debt và cash.
- `internal/app/domains.go:69-71`: inventory đã thêm `inventory.RegisterLockers(coord, clk)` cạnh `debt.RegisterLockers` và `LockFundAccounts`. `RegisterLocker` panic nếu một kind bị đăng ký hai lần. Hiện tại không trùng (inventory giữ `sku_stock`/`lots`/`lot_balances`/`reservations`), và test app/cli vẫn chạy được. Khi merge, cần giữ đúng một lời gọi cho mỗi domain.
- `apps/api/api/openapi.yaml`: `TestOpenAPIContract` đang đỏ vì 24 route inventory chưa có tài liệu. Đây không phải lỗi debt/cash, nhưng nghĩa là gate `internal/app` sẽ chưa xanh cho tới khi inventory cập nhật spec.
- Cả `internal/app/` và `internal/cli/` đều còn untracked (`??`). Commit của hai phase sẽ đụng chung các file này, nên cần thống nhất ai commit trước.

## Các hạng mục đã kiểm, không thấy lỗi

- Projection: `settle`/`reverseSettlement` → `moveSettled` (`repo.go`) kiểm 0 ≤ settled ≤ amount và suy ra status. `moveAllocated` kiểm cận. Verify lặp lại các bất biến này.
- Không netting tự động: advance và pending chỉ được liệt kê riêng (`queries.go` Ledger và Parties). Credit note dư chuyển thành obligation `refund_due` ở ledger ngược chiều, không trừ vào ledger kia.
- Aging as-of (`queries.go:278-330`): obligation và settlement cùng lọc `business_date ≤ as_of`. Mirror reversal mang ngày reversal (`repo.go:140`), nên posting sau ngày as-of không làm đổi kết quả. Phần chưa trả được chia theo installment, trả cũ trước, khớp với `overdueExpr`/`nextDueExpr`. Hoá đơn đến hạn đúng ngày as-of thuộc `current`, thống nhất với `due_date < date` ở overdue.
- Atomicity và idempotency: mọi lệnh ghi sổ đi qua `posting.Coordinator` (replay trước, rồi entitlement, period, claim key, docseq, lock, Apply, audit/outbox, Complete). Recorder chạy trong tx của caller và không tự commit.
- Race giữa confirm và reverse: `paymentReverse.Apply` đọc lại settlement đang đứng và so với tập đã pre-read (`errPaymentChanged`). `paymentConfirm` so `sameIDs` với planned. Thứ tự lock obligations → payments → fund_accounts được giữ trong đường HTTP.
- Credit note: khoá target cùng các obligation mở của party trong một câu `FOR UPDATE ORDER BY id`.
- Credit limit: chạy trong posting của caller, sau docseq của chứng từ bán, nên các lần xác nhận đơn của tenant được tuần tự hoá. Không có race vượt hạn mức.
- Reversal: chặn khi đã có advance_refund con chưa bị reverse. Mirror settlement và cash entry đều chỉ có một lần (unique index cùng verify).
- `UpdateFund`: lock fund `FOR UPDATE` rồi kiểm `fundHasEntries`. Posting ghi entry sau `LockAllFunds`, nên không có cửa sổ nào cho phép đổi opening balance sau entry đầu tiên.
- Tenant isolation: RLS FORCE trên mọi bảng debt/cash. Có test `TestTenantsCannotReachEachOthersDebts`. Principal support bị chặn ghi bởi `SupportReadOnly` trên cả `TenantAPI` và `TenantPostingAPI`.
- Re-auth: write-off (quyết định sau khi đọc body), offset và reversal đều có. Write-off còn tiêu thụ exception `debt_write_off` gắn với target version và payload hash.
- Tiền tệ: `money.Money` giới hạn scale 4 và |v| < 1e15, khớp `numeric(19,4)`. Không có phép chia hay làm tròn trong luồng ghi sổ (chỉ `Round(0)` khi render text nhắc nợ).
- Migration 000030/000034: CHECK `(status='reversed') = (reversed_on IS NOT NULL)`, `planned_allocations IS NULL OR status='pending_verification'`, `(purpose='advance_refund') = (refund_of IS NOT NULL)`. Code giữ các bất biến này (`reversePayment` xoá `planned_allocations`, `confirmPayment` set NULL).
- Nhắc nợ: dedupe bằng `ON CONFLICT (tenant_id, dedupe_key)`. Chỉ gửi mail khi consent `debt_reminder` qua email mới nhất là granted. Mail gửi sau khi log đã commit.

## Khoảng trống test

- Không có test cho `auto_earmarked` (H1), dù bước 12 của plan yêu cầu.
- Không có test đồng thời: deadlock giữa recorder và HTTP (M1), hay confirm song song với reverse.
- Không có test "payment recorder từ chối key trùng nhưng khác nội dung" (M2).
- Verifier chưa có test cho trường hợp payment lệch cash entry (L1).

## Danh sách sửa đề xuất (theo ưu tiên)

1. H1: tự phân bổ deposit earmarked khi confirm (và khi ghi deposit sau receivable). Thêm 3 test thứ tự.
2. M1: lấy số PT/PC trước mọi row lock khác (coordinator cấp nhiều prefix, hoặc caller cấp số và truyền vào). Phải xong trước khi procurement/sales dùng `PaymentRecorder`.
3. M2: `PaymentRecorder` trả 409 khi key trùng mà nội dung khác. Thêm test.
4. L1: verifier đối chiếu payment với cash entry.
5. L2: forecast "đã nhận" theo ngày xác nhận hoặc ngày cash entry.

## Theo dõi plan

- Các bước 1–11 của `phase-10-debt-cash.md` có code tương ứng. Route trong plan khớp `routes.go`/`http.go` và đều có trong `openapi.yaml`.
- Bước 12 (test) còn thiếu "earmark auto allocation", gắn với H1.
- Bước `ledger rebuild` vẫn `errNotImplemented` (`cli/ledger.go:114`). Implementer report đã ghi nhận, plan không yêu cầu trong phase này.
- Đề xuất cho lead: chưa đánh dấu Phase 10 hoàn tất cho tới khi xử lý H1 và M1 (M1 vì nó là hợp đồng port cho Phase procurement/sales).

## Câu hỏi còn mở

- Sales có ghi hai payment khác chiều (PC và PT) trong cùng một posting không, ví dụ đổi hàng? Nếu có, kịch bản B của M1 xảy ra với mọi khoản thu đồng thời, và M1 nên được nâng lên High.
- Statement hiện chỉ gồm obligation và settlement, không gồm advance chưa phân bổ. Nếu khách đối chiếu và hỏi "tôi đã chuyển cọc", bản đối chiếu sẽ không phản ánh khoản đó. Cần xác nhận đây là chủ ý sản phẩm.
