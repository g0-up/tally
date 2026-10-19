# Báo cáo triển khai — Debt & Cash

- Phase: `phase-10-debt-cash.md` (đã tick đủ 11 todo, `status: done`)
- Plan: `plans/261016-0854-tally-full-product-implementation/`
- Trạng thái: hoàn thành. `plan.md` vẫn ghi "In progress" ở hàng phase 10 vì file này do team-lead quản lý, tôi không sửa.

## 1. Việc đã làm

### Package `internal/debt` (độc quyền)

- **Model và factory**
  - Các kiểu: obligation, lịch trả (installment), payment (purpose/status), credit note, debt adjustment, debt offset, settlement, statement và các version của nó, collection note.
  - `ObligationFactory` là hàm thuần. Nó kiểm tra nguồn chứng từ theo từng sổ và tính hạn: lấy `DueDate`, nếu không có thì lấy kỳ cuối của lịch, nếu không có nữa thì lấy ngày nghiệp vụ cộng payment terms.
- **Payment**
  - Mỗi purpose có một mã chứng từ PT/PC.
  - Phương thức có `requires_verification` thì payment ở trạng thái `pending_verification`: chưa có cash entry, chưa phân bổ. Phân bổ dự kiến được giữ trong `planned_allocations`.
  - Khi confirm, tiền và phân bổ được ghi đúng một lần. Phân bổ bị chặn trần theo số còn mở tại thời điểm confirm.
- **Phân bổ**
  - `AllocationStrategy` có các chiến lược: oldest-due-first (hòa thì xét ngày chứng từ, bỏ qua chứng từ đã tất toán), earmarked (đặt cọc cho đơn) và manual.
  - Guard trả lỗi cho các trường hợp sau:

    | Mã | HTTP |
    |---|---|
    | `allocation_exceeds_open` | 422 |
    | `allocation_exceeds_payment` | 422 |
    | `allocation_party_mismatch` | 422 |
    | `allocation_direction_mismatch` | 422 |
    | `obligation_settled` | 422 |

  - Settlement chỉ append. Hai projection `settled_amount` và `allocated_amount` được cập nhật trong cùng transaction.
  - Không tự động cấn trừ: phần dư thành advance và chỉ được dùng khi có phân bổ tường minh.
- **Credit note**
  - Trừ vào `Targets` trước, sau đó trừ vào các chứng từ mở cũ nhất.
  - Phần còn dư thành obligation `refund_due` ở sổ ngược lại, được trả bằng refund.
  - Một advance chưa phân bổ được trả lại bằng `advance_refund`, có cột `refund_of`.
- **Debt adjustment và offset**
  - Có ba loại adjustment: opening balance, write-off (cần owner xác nhận qua exception) và adjustment có reason.
  - Một offset tất toán cả hai sổ cùng lúc.
- **Đảo payment**
  - Cần reauth.
  - Trả 409 `payment_has_successor` khi advance của payment đã bị refund. Phải đảo refund trước thì mới đảo được payment.
  - Khi đảo: ghi settlement đối ứng và cash entry đối ứng, đặt `reversed_on`. Số dư trở về giá trị trước khi có payment.
- **Hạn mức tín dụng**
  - `CreditLimitPolicy`: exposure = open receivable + undelivered − earmarked advance. Advance chung không được tính vào.
  - Có quá hạn thì chỉ cảnh báo, không chặn.
- **Statement**
  - Mỗi lần generate tạo một version mới, append-only.
  - Send đóng băng version đã gửi (`sent_version`, `sent_at`). Generate lại tạo v2 và v1 hiển thị `superseded`.
  - Confirm và dispute dùng If-Match/ETag.
- **Collection note và nhắc nợ**
  - Có note, promise-to-pay (dùng trong forecast) và remind qua email.
- **Query**
  - Sổ chi tiết theo đối tác, danh sách đối tác (open/overdue/suggestion).
  - Aging as-of: replay các settlement có ngày ≤ as_of và các bản đảo có `reversed_on` ≤ as_of, kỳ cũ nhất trước.
  - Forecast: lịch trả và promise so với tiền đã confirm. Ngoài ra có query advances.
- **Verifier**: `Verify` kiểm tra Σ settlements = `settled_amount` và Σ allocations = `allocated_amount`.
- **HTTP**: toàn bộ route của debt. Một party nêu trên URL mà không tồn tại hoặc thuộc tenant khác trả **404**. Party hoặc obligation nêu trong body trả 422 `party_not_found` / `obligation_not_found`.
- **Sửa lần này**
  - Thêm `pathParty` ở `repo.go` và dùng nó trong `Ledger`, `AddNote`, `Notes`, `Remind`. Trước đây các route này trả 422 thay vì 404 như spec đã ghi.
  - Đã viết lại integration test thành internal package vì depguard.

### Package `internal/cash` (độc quyền)

- Quỹ (cash/bank), payment methods và cash entries append-only. `Writer` cập nhật `balance` trong cùng transaction.
- `CategoryFor` phân loại khoản thu/chi theo direction, purpose và source.
- Opening balance chỉ sửa được khi quỹ chưa có entry. Sửa sau đó trả 409 `fund_has_entries`. PATCH dùng If-Match.
- Báo cáo dòng tiền theo category và theo ngày.
- `Seeder` tạo quỹ và phương thức mặc định khi onboarding. `Resetter` dùng cho sandbox reset.
- `LockFunds` là locker `posting.LockFundAccounts`.
- `Verify` kiểm tra fund balance = opening + Σ entries và kiểm tra mỗi entry đảo phản chiếu đúng entry gốc.

### File dùng chung đã chạm (chỉ bằng Edit, chèn khối nhỏ)

- `internal/app/domains.go`: tạo debt/cash service với coordinator dùng chung, `debt.RegisterLockers` và `cash.LockFunds`.
- `internal/app/routes.go`, `internal/app/app.go` (Modules): đăng ký handler debt và cash.
- `internal/app/openapi_contract_test.go` (`newContractApp`): thêm handler Debt và Cash.
- `api/openapi.yaml`: thêm các route `/debts/*`, `/payments*`, `/debt-adjustments`, `/debt-offsets`, `/refunds`, `/statements*`, `/parties/{id}/collection-notes`, `/funds*` và `/payment-methods`.
- `internal/cli/ledger.go`: lệnh `tally ledger verify [--tenant]`. Mỗi tenant được kiểm tra trong một read-only tenant tx. Mỗi chỗ lệch in ra một dòng và lệnh thoát khác 0. Lệnh `ledger rebuild` vẫn trả "not implemented".
- `internal/cli/serve.go`: wiring.
- `internal/cli/serve_test.go`: bỏ `{"ledger","verify"}` khỏi `TestCommandsNotImplementedYet`, vì lệnh này đã được triển khai.

### File test mới

- `internal/debt/debt_test.go`, `internal/cash/cash_test.go` (unit)
- `internal/debt/debt_integration_test.go`, `internal/cash/cash_integration_test.go` (integration, internal package)
- `internal/app/debt_cash_integration_test.go`: route test qua HTTP cho debt và cash dùng chung.
- `internal/cli/ledger_integration_test.go` (**file mới trong cli**): chạy `tally ledger verify` trên dữ liệu nhất quán, trên quỹ bị sửa tay và với `--tenant` sai. Tôi tạo file riêng để không phải sửa `cli_integration_test.go` dùng chung.

## 2. Migration

Cả hai migration chỉ dùng ALTER TABLE, CHECK, FK và INDEX. Không có trigger, function, procedure, view, rule, extension hay DO.

- **`000030_debt_document_columns`**
  - `payments.refund_of`: FK tới payments; CHECK `purpose='advance_refund'` ⇔ `refund_of` không null; có index.
  - `statements.sent_version` và `sent_at`, kèm CHECK.
  - `payments.reversed_on`: CHECK `status='reversed'` ⇔ cột không null.
  - `credit_notes.source_doc_no`.
- **`000032_debt_payment_planned_allocations`**
  - `payments.planned_allocations jsonb`, kèm CHECK chỉ cho phép khi `pending_verification`.
- Lint báo "version 000032 missing" là lỗi đã được báo trước, không phải lỗi của tôi.

## 3. Public port (dành cho procurement và sales)

Mọi recorder chạy trong `posting.Tx` của bên gọi, dùng ngày nghiệp vụ và actor của bên gọi, tự ghi audit và outbox vào tx đó, và không commit. Lock order là obligations → payments → fund_accounts. Bên gọi nên khai các dòng debt sẽ tất toán trong `LockKeys` (`PaymentInput.AddLocks`).

```go
func (s *debt.Service) Receivables() debt.ReceivableRecorder
func (debt.ReceivableRecorder) Record(ctx context.Context, tx posting.Tx, in debt.ObligationInput) (debt.ObligationResult, error)
func (s *debt.Service) Payables() debt.PayableRecorder
func (debt.PayableRecorder) Record(ctx context.Context, tx posting.Tx, in debt.ObligationInput) (debt.ObligationResult, error)
func (s *debt.Service) Payments() debt.PaymentRecorder
func (debt.PaymentRecorder) Record(ctx context.Context, tx posting.Tx, in debt.PaymentInput) (debt.PaymentResult, error)
func (debt.PaymentInput) DocPrefix() string // PT cho tiền vào, PC cho tiền ra
// Gói posting (bổ sung, không đổi chữ ký có sẵn):
type posting.ExtraDocs interface{ ExtraDocPrefixes() []string }
func posting.TakeDocNo(tx posting.Tx, prefix string) (string, error)
func (s *debt.Service) CreditNotes() debt.CreditNoteRecorder
func (debt.CreditNoteRecorder) Record(ctx context.Context, tx posting.Tx, in debt.CreditNoteInput) (debt.CreditNoteResult, error)
func (debt.ObligationFactory) Build(direction string, in debt.ObligationInput, businessDate time.Time, termsDays int) (debt.Obligation, []debt.Installment, error)
func (s *debt.Service) CreditLimit() debt.CreditLimitPolicy
func (debt.CreditLimitPolicy) Check(ctx context.Context, q db.Querier, in debt.CreditCheckInput) (debt.CreditCheck, error)
func (debt.CreditCheck) Err() error // exception credit_limit_exceeded when Exceeded
func debt.Verify(ctx context.Context, q db.Querier) ([]string, error)
func debt.RegisterLockers(c *posting.Coordinator)
func cash.Verify(ctx context.Context, q db.Querier) ([]string, error)
func cash.LockFunds(ctx context.Context, q db.Querier, ids []uuid.UUID) error
func (cash.Writer) Write(ctx context.Context, q db.Querier, tenantID uuid.UUID, in cash.EntryInput) (cash.Entry, error)
```

Ngữ nghĩa:

- **Receivable / Payable `Record`**
  - Tạo obligation của một chứng từ nguồn đúng một lần.
  - Gọi lại với cùng party và cùng amount thì trả `Replayed` và không ghi gì thêm. Gọi lại với giá trị khác trả 409 `obligation_exists`.
  - Receivable có `EarmarkedInvoiceID` tự áp các khoản cọc đã confirm của đơn đó ngay. Với khoản cọc còn pending, nó thêm một dòng `auto_earmarked` vào plan của khoản cọc, và dòng này được áp khi khoản cọc được confirm.
- **Payment `Record`**
  - Không tự cấp số nữa. Command của bên gọi phải implement `posting.ExtraDocs` và liệt kê `in.DocPrefix()` một lần cho mỗi payment nó ghi, ví dụ `{"PT"}` cho POS, `{"PC", "PT"}` cho đổi hàng. Coordinator cấp các số này ở bước docseq, trước mọi row lock, và recorder lấy số bằng `posting.TakeDocNo`. Thiếu số đặt trước thì lỗi. Số đã đặt trước mà Apply không dùng thì posting cũng lỗi, để sequence không có lỗ.
  - Payment đã confirm thì tất toán các phân bổ và ghi cash entry. Payment đi qua phương thức cần xác minh thì ở trạng thái pending và không làm thay đổi gì.
  - Có `Key` và cùng nội dung thì trả `Replayed` và không lấy số. Vì vậy posting replay không được đặt trước số, và bên gọi tự kiểm tra trạng thái chứng từ của mình để không ghi lại. Cùng `Key` nhưng khác nội dung (hướng, party, purpose, method, amount, nguồn, earmark, refund_of, và phân bổ khi chúng còn là bản gốc) trả 409 `payment_exists`. Key của payment đã đảo trả 409 `payment_reversed`.
- **CreditNote `Record`**
  - Trừ vào targets trước, sau đó (trừ khi đặt `OnlyTargets`) trừ vào các chứng từ mở cũ nhất. Phần dư thành `refund_due`.
  - Gọi lại với giá trị khác trả 409 `credit_note_exists`.
- **`CreditLimitPolicy.Check`**
  - Đọc trong tx của bên gọi.
  - `Undelivered` do sales tính và truyền vào, bao gồm cả đơn hiện tại.
  - Kết quả trả về mọi thành phần để hiển thị cho owner.

## 3b. Port bổ sung cho phase 08/09

### Đảo obligation theo chứng từ nguồn

```go
type debt.SourceRef struct {
	Direction  string    // debt.Receivable | debt.Payable
	SourceType string    // debt.SourceSalesDocument | SourceReceipt | SourceLandedCostAllocation | SourcePurchasePriceAdjustment
	SourceID   uuid.UUID
}
type debt.ObligationReversal struct {
	Obligation debt.Obligation   // đã có ReversedOn, ReversalReason, ReversalDocNo
	Released   []debt.Settlement // dòng đảo (âm) của các settlement bằng payment; tiền đó thành advance
	Closing    debt.Settlement   // settlement obligation_reversal đóng toàn bộ amount
	Replayed   bool
}
func (s *debt.Service) Obligations() debt.ObligationReverser
func (debt.ObligationReverser) ReverseBySource(ctx context.Context, tx posting.Tx, ref debt.SourceRef, reason string) (debt.ObligationReversal, error)
```

Ngữ nghĩa (append-only, không xoá hay sửa settlement nào):

- Chạy trong `posting.Tx` của bên gọi, dùng ngày nghiệp vụ, actor và số chứng từ của tx (`tx.DocNo()`, ví dụ số phiếu đảo; rỗng thì `reversal_doc_no` null). Tự ghi audit `obligation.reverse` và outbox `obligation.reversed`.
- Mỗi settlement bằng payment còn đứng trên obligation được ghi dòng đảo âm (`reversal_of`), và `allocated_amount` của payment giảm tương ứng. Tiền đó trở thành advance của đối tác: có thể phân bổ lại hoặc hoàn như mọi advance. Khoản cọc earmark sẽ tự áp lại khi đơn đó có obligation mới.
- Sau đó một settlement `obligation_reversal` (settled_by_id là chính obligation) đóng toàn bộ amount. Obligation thành `settled`, có `reversed_on`, `reversal_reason` và `reversal_doc_no`. Sổ cái và statement các kỳ trước vẫn giữ chứng từ. Statement kỳ đảo có dòng `obligation_reversal` mang số phiếu đảo, hoặc số chứng từ gốc khi không có số phiếu đảo.
- Gọi lại cho obligation đã đảo trả `Replayed` và không ghi gì.
- Ghi lại obligation cho cùng nguồn sau khi đã đảo (`Receivables/Payables().Record`) trả 409 `obligation_reversed`. Chứng từ thay thế phải có `SourceID` mới.
- Lỗi:
  - 422 `validation_failed` khi `source_type` không đảo được, chiều sổ sai, thiếu `source_id`, hoặc `reason` rỗng hay dài quá 500 ký tự.
  - 422 `obligation_not_found` (field `source_id`) khi nguồn không có obligation trên sổ đó, ví dụ khách lẻ không có công nợ. Bên gọi chỉ gọi khi chứng từ có ghi obligation.
  - 422 `reversal_before_document` khi tx có ngày trước ngày của obligation.
  - 409 `obligation_has_credits` khi có credit note, bù trừ (BT) hoặc điều chỉnh (DN) đang tất toán obligation. Các loại này chưa có thao tác đảo, nên chứng từ cũng không đảo được. Ví dụ: phiếu xuất đã có phiếu trả hàng phải chặn từ phía sales.
- Lock order: obligation, rồi các payment của nó theo id, đều được lock bên trong lời gọi. Bên gọi có thể khai `posting.LockObligations`, nhưng không nên khai các payment đó trong `LockKeys`.
- Huỷ phần còn lại của hoá đơn: phần chưa xuất chưa có obligation nên không cần đảo gì. Khoản cọc earmark vẫn là advance của khách và vẫn tính vào credit limit như trước.
- `tally ledger verify` có thêm một kiểm tra: obligation đã đảo phải có đúng một settlement `obligation_reversal` của chính nó với đủ amount, và obligation chưa đảo thì không có settlement loại này. Test cho thấy cả debt, cash và inventory verify đều sạch sau khi đảo rồi hoàn advance.

### `shift_id` cho bút toán quỹ: không cần migration

- `cash_entries.shift_id` đã có từ 000022, kèm FK tới `shifts`.
- Code đã đi đủ đường: `debt.PaymentInput.ShiftID` → cash entry của payment confirm → `cash.EntryInput.ShiftID`. Sales chỉ cần đặt `ShiftID` trong `PaymentInput` khi ghi payment của POS, trả hàng hay đổi hàng qua `Payments().Record`. Mỗi payment vẫn cần một prefix trong `ExtraDocPrefixes`, ví dụ `{"PC", "PT"}` cho đổi hàng.
- Bút toán đối ứng khi đảo payment không mang shift. Nó thuộc ca của ngày đảo, không thuộc ca của bút toán gốc.

### Migration `000033_debt_obligation_reversal`

- `obligations` có thêm `reversed_on date`, `reversal_reason text` và `reversal_doc_no text`, kèm `ck_obligations__reversal_fields`: reversed_on và reason cùng có hoặc cùng không; doc_no chỉ có khi đã đảo; đã đảo thì phải `settled`.
- `ck_obligation_settlements__settled_by_type` được thay bằng `ck_obligation_settlements__settled_by_type_kinds`, thêm loại `obligation_reversal`.
- Down đảo ngược toàn bộ. Không dùng trigger, function, view hay ON DELETE CASCADE. Lint và reversibility đều qua.

### Thay đổi kèm theo

- `debt.Obligation` có thêm ba trường trên. DTO và OpenAPI `Obligation` có thêm `reversed_on`, `reversal_reason` và `reversal_doc_no` (nullable). Enum `Settlement.settled_by_type` có thêm `obligation_reversal`.
- `sourceCommand` trong test debt có thêm `prefix`.
- Test mới:
  - `TestReversingASourceTurnsItsPaymentsIntoAdvancesAndKeepsEverySettlement` (debt).
  - `TestReversedSaleLeavesEveryLedgerVerified` (app). Test này dùng adapter cash thật, hoàn advance, rồi chạy cả ba verifier của `ledger verify`.
- Đã xoá hàm `tenantToday` không còn dùng trong `internal/cash/repo.go`. Hàm này mồ côi sau khi `cash/reset.go` bị xoá ngoài phạm vi của tôi, và để lại thì golangci-lint báo `unused`.

### Kết quả kiểm tra

Chạy trong `apps/api`, ngày 2026-10-17, tất cả đều qua:

- `go build ./...`
- `go vet ./...` và `go vet -tags integration ./...`
- `golangci-lint run` cho debt, cash, app và posting: 0 issues.
- `go test -race ./internal/app/ ./migrations/`, bao gồm contract OpenAPI và lint migration.
- `go test -tags integration -race` cho `./migrations/`, `./internal/cli/`, `./internal/app/`, `./internal/debt/`, `./internal/cash/` và `./internal/posting/`.

### Port phân bổ advance trong tx của bên gọi (procurement yêu cầu)

```go
func (s *Service) Allocations() AllocationRecorder
func (r AllocationRecorder) Record(ctx context.Context, tx posting.Tx, in AllocationInput) (PaymentResult, error)
func (in AllocationInput) AddLocks(set *posting.LockSet) // LockPayments(PaymentID) + LockObligations(ids của các dòng)
```

- Phần thân dùng chung với `PostAllocation`: `checkAllocationInput` kiểm tra input và mặc định mode là manual, còn `allocateAdvance` khoá và áp dụng. Port không lấy số chứng từ và không có idempotency key riêng; posting của bên gọi là đơn vị replay.
- `checkAllocationInput` clone các dòng, nên không sửa slice của bên gọi.
- Lỗi:
  - 422 `validation_failed`
  - 422 `payment_not_found`
  - 422 `payment_pending_verification`
  - 409 `payment_reversed`
  - 422 `payment_without_party`
  - Trên từng dòng: 422 `obligation_not_found`, `allocation_party_mismatch`, `allocation_direction_mismatch`, `obligation_settled`, `allocation_exceeds_open`, `allocation_exceeds_payment`.
- Có thể phân bổ vào obligation vừa được ghi trong cùng posting: dòng của nó đã thuộc tx, nên không cần khai trong LockKeys.
- Test `TestSupplierAdvanceIsAllocatedToAReceiptInsideItsPosting` (debt) bao gồm:
  - Ghi payable và phân bổ advance PC trong một posting.
  - Phân bổ vượt advance trả 422, và payable của phiếu bị từ chối bị rollback theo.
  - Số dư mở, advance còn lại, quỹ và verify đều đúng.
- Gate: `go build ./...`, `go vet ./...`, golangci-lint cho debt/cash (0 issues), `go test -race ./internal/debt/`, và `go test -tags integration -race` cho debt, cash và app. Tất cả đều qua.

## 4. Lệnh test và kết quả thực tế (chạy trong `apps/api`, ngày 2026-10-17)

| Lệnh | Kết quả |
|---|---|
| `go test -race -count=1 ./internal/debt/... ./internal/cash/... ./internal/app/... ./internal/cli/...` | `ok` cho debt, cash, app, cli |
| `go test -tags integration -race -count=1 ./internal/debt/... ./internal/cash/...` | `ok debt 8.3s`, `ok cash`. Có 12 integration test của debt và 6 của cash, tất cả PASS. |
| `go test -tags integration -race -count=1 ./internal/app/...` | `ok 16.9s`. `TestDebtAndCashOverHTTP` có 8 subtest, tất cả PASS. Toàn bộ suite app, kể cả OpenAPI contract, cũng PASS. |
| `go test -tags integration -race -count=1 ./internal/cli/...` | `ok 9.3s`, kể cả `TestLedgerVerifyCommand` |
| `go vet` (có và không có `-tags integration`) trên debt, cash, app, cli, cmd | sạch |
| `golangci-lint run ./internal/debt/... ./internal/cash/...` | `0 issues` |
| `golangci-lint run ./internal/cli/... ./internal/app/...` và cùng lệnh với `--build-tags integration` | `0 issues` |
| `go build ./...` | Chỉ lỗi trong `internal/inventory` (`toAdjustmentDTO`, `toCountDTO`, `toRecostDTO` undefined), thuộc teammate inventory. Package của tôi build sạch. |

Đối chiếu tiêu chí thành công với test chứng minh:

- **Gợi ý 3.000.000 + 2.000.000; phân bổ 6.000.000 vào chứng từ thứ hai trả 422 `allocation_exceeds_open`**
  - `TestAllocationSuggestsOldestDueFirstAndRejectsMoreThanOpen`
  - Subtest idempotency trong route test
- **Chuyển khoản pending không có entry và không có phân bổ; confirm tạo cả hai đúng một lần**
  - `TestPendingTransferMovesNothingUntilConfirmedOnce`
  - Subtest transfer queue
- **409 `payment_has_successor`; sau khi đảo refund thì đảo được payment và số dư trở về như cũ**
  - `TestReversalWaitsForTheRefundOfItsAdvanceAndRestoresBalances`
  - Subtest reauth: 403 `reauth_required`, sau đó `/auth/reauth`, sau đó 200
- **Aging as-of 30/09 giống nhau trước và sau các settlement ngày 05/10**
  - `TestAgingAsOfADateIgnoresLaterPostings`
- **Version đã gửi không đổi; regenerate tạo v2 và v1 hiển thị `superseded`**
  - `TestStatementVersionsFreezeWhatWasSent`
  - Subtest statements: If-Match trả 428/412/200
- **Verifier**
  - `TestVerifierReportsATamperedProjection`, `TestVerifierAndAppendOnlyEntries`
  - Subtest "both ledgers verify after every posting above"
  - `TestLedgerVerifyCommand`
- **Không tự động cấn trừ, refund due, advance refund**
  - `TestCreditNoteBeyondTheDebtBecomesARefundDueAndAdvancesAreNeverNetted`
- **Hạn mức tín dụng**
  - `TestCreditLimitCountsOpenUndeliveredAndEarmarkedDeposits`
  - `TestCreditCheckErr`
- **Idempotency và atomicity qua Coordinator**
  - `TestPostingsAreIdempotentAndAtomic`
  - Subtest idempotency: key bắt buộc, replay header, 409 `idempotency_key_reused`
- **Tenant isolation**
  - `TestTenantsCannotReachEachOthersDebts`, `TestFundsBelongToOneTenant`
  - Subtest "ids of another store answer 404"
- **Write-off và offset**
  - `TestWriteOffNeedsTheOwnersConfirmationAndOffsetSettlesBothLedgers`
- **Note và nhắc nợ**
  - `TestCollectionNotesAndReminders`

## 4b. Sửa theo review (`plans/reports/code-reviewer-261017-1246-debt-cash-review.md`)

- **H1: cọc earmarked đang pending.** `applyEarmarked` (`apps/api/internal/debt/factory.go`) khóa cả cọc confirmed lẫn cọc pending của đơn (`FOR UPDATE ORDER BY id`).
  - Cọc confirmed được tất toán trước.
  - Sau đó mỗi cọc pending nhận một dòng `auto_earmarked` trong `planned_allocations`, bằng min(phần plan còn trống, phần còn mở sau các cọc pending trước). `confirmPayment` áp dòng này, có cap theo phần còn mở lúc confirm.
  - Row lock trên payment xếp thứ tự việc này với việc confirm. Nếu một confirm đã đọc plan trước khi plan được thêm dòng, nó trả 409 `payment_changed` và client thử lại.
  - Không cần migration mới.
  - Test `TestEarmarkedDepositReachesItsInvoiceWhicheverIsConfirmedFirst` có ba trường hợp: cọc confirm trước rồi mới ghi hóa đơn; chuyển khoản pending, ghi hóa đơn, rồi confirm; hai cọc pending chia phần còn lại sau một cọc tiền mặt.
- **M1: số PT/PC cấp trước mọi lock.**
  - Thêm `posting.ExtraDocs` và `posting.TakeDocNo` vào `apps/api/internal/posting/command.go` và `tx.go`. Coordinator cấp số trong `issueDocNos` (`coordinator.go`): số của DocPrefix trước, rồi một số cho mỗi phần tử ExtraDocPrefixes, các prefix theo thứ tự tăng dần, tất cả trước `locker.Acquire`. Sau Apply, nếu còn số chưa được lấy thì posting lỗi.
  - Chữ ký có sẵn không đổi. Command không implement `ExtraDocs` chạy như cũ.
  - Doc comment của `ExtraDocs` hướng dẫn procurement và sales cách dùng.
  - `PaymentRecorder` lấy số bằng `TakeDocNo`.
  - Unit test mới trong `apps/api/internal/posting/extra_docs_test.go`: thứ tự cấp và cấp trước lock; số phụ cùng prefix đi sau số chính; số không được lấy làm posting lỗi; lấy số khi không đặt trước; prefix rỗng.
  - Toàn bộ test có sẵn của posting vẫn xanh.
- **M2: replay khác nội dung trả 409.** `replayPayment` và `sameLines` nằm trong `apps/api/internal/debt/payments.go`, `errPaymentExists` trong `errors.go`. Dòng `auto_earmarked` là của hệ thống nên không được so.
  - Test `TestPaymentRecorderTakesTheReservedNumberAndRefusesAChangedReplay` kiểm tra các trường hợp sau:
    - Ghi không đặt trước số thì lỗi.
    - Payment được ghi đúng số PT đã đặt.
    - Replay đúng nội dung trả `Replayed`; replay mà vẫn đặt số thì lỗi.
    - Đổi amount, method, phân bổ hoặc nguồn thì trả 409 `payment_exists`.
    - Sau khi đảo payment thì trả 409 `payment_reversed`.
- **L1: verify đối chiếu payment với cash entry.**
  - `debt.Verify` kiểm tra:
    - Payment confirmed có đúng một entry bằng số tiền có dấu.
    - Payment reversed sau khi đã confirm có entry gốc và entry đảo của nó, tổng bằng 0.
    - Payment pending và opening advance không có entry.
    - Mọi entry nằm trong quỹ của payment.
    - Entry đảo phải mirror một entry của chính payment đó.
  - Test `TestVerifierChecksPaymentsAgainstTheirCashEntries`.
- **L2: forecast theo ngày quỹ ghi nhận.**
  - `ConfirmedIn` và `ConfirmedOut` giờ là tổng các cash entry của payment theo `cash_entries.business_date`. Chuyển khoản tính vào ngày confirm. Khoản đảo trừ lại vào ngày đảo, nên một ngày chỉ có đảo sẽ có số âm, khớp với sổ quỹ.
  - Test `TestForecastCountsMoneyOnTheDayTheFundReceivedIt`.
- **`internal/cli/ledger.go` không cần sửa.**
- **Kết quả chạy lại (2026-10-17) trên debt, cash, posting, cli, app:** `go vet` có và không `-tags integration` sạch. `golangci-lint run` có và không `--build-tags integration` đều 0 issues. `go test -race -count=1` có và không `-tags integration` đều ok, kể cả `TestOpenAPIContract`. `go build ./...` giờ đã sạch.

## 5. Quyết định thiết kế, rủi ro và giới hạn

- **Opening balance của quỹ là một cột, không phải một entry.** Design doc ghi khác. Verifier đã tính cả cột này: balance = opening + Σ entries.
- **Integration test của debt dùng một CashBook double viết bằng SQL** (`fundBook`), vì depguard cấm `internal/debt` import `cash`, kể cả trong test. Adapter thật được kiểm tra qua route test ở `internal/app`.
- **`newLedgerStack` trong app test chép lại `newStack`**, vì `app_integration_test.go` không thuộc quyền sở hữu của tôi.
- **Adapter onboarding và reason seeder ghép nằm trong `app`.** Cash resetter chạy trước khi reset.
- **Chưa có sandbox resetter cho debt.** Sandbox reset hiện chỉ làm trống quỹ.
- **Đảo obligation sinh từ procurement/sales chưa được thiết kế.** Hiện tại chỉ đảo được payment.
- **Số PT/PC của recorder được đặt trước bởi coordinator** (`posting.ExtraDocs`). Bên gọi phải biết trước mình ghi bao nhiêu payment. Một posting replay payment theo `Key` không được đặt trước số.
- **Trường hợp "cọc confirmed được ghi sau khi đã có receivable" nằm ngoài luồng thật**, vì cọc được tạo khi hóa đơn còn nháp. Khi đó cọc chỉ là advance và có thể phân bổ tay. Hai thứ tự có trong luồng thật đều đã có test.
- **Reminder vẫn được ghi log khi gửi email thất bại.**
- **`Undelivered` trong credit check do sales cung cấp.** Nếu sales tính sai thì kết quả check sai.
- **Mỗi cash posting khóa toàn bộ quỹ của tenant** (`LockAllFunds`), nên các cash posting trong một tenant chạy tuần tự. Với quy mô cửa hàng nhỏ thì chấp nhận được.
- **`advance_refund` không bao giờ ở trạng thái pending.**
- **Statement chưa có mục advances.**
- **Không có entitlement guard trên các route debt/cash.**
- **`tally ledger rebuild` vẫn là "not implemented"**, vì không nằm trong phạm vi phase.

## 6. Câu hỏi chưa giải quyết

- Ai cập nhật hàng phase 10 trong `plan.md` thành "Done": team-lead hay tôi?
- Debt có cần sandbox resetter riêng không, và có cần đảo obligation khi procurement/sales hủy chứng từ không? Nếu có thì nên đưa vào phase 08/09 hay mở một việc riêng?
- Có cần đồng bộ design doc với quyết định opening balance là cột không?

Status: DONE_WITH_CONCERNS
Summary: Debt & Cash đã xong đủ 11 todo, và đã sửa H1, M1, M2, L1, L2 theo review, mỗi mục có test regression. Vet, lint và test race (có và không integration) của debt, cash, posting, cli, app đều sạch, `go build ./...` cũng sạch.
Concerns/Blockers: Procurement và sales phải implement `posting.ExtraDocs` cho mỗi PT/PC chúng ghi qua `PaymentRecorder`, nếu không thì posting lỗi. Các giới hạn còn lại ở mục 5: chưa có sandbox reset cho debt, chưa đảo được obligation nguồn, `Undelivered` do sales cung cấp.
