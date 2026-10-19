package debt

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// date is a "YYYY-MM-DD" JSON string.
type date time.Time

// UnmarshalJSON reads a "YYYY-MM-DD" string.
func (d *date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: reflect.TypeFor[date]()}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string " + s, Type: reflect.TypeFor[date]()}
	}
	*d = date(clock.Date(t.Year(), t.Month(), t.Day()))
	return nil
}

func (d *date) time() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

func dateOf(t time.Time) string { return t.Format(time.DateOnly) }

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateOf(*t)
	return &s
}

func mapSlice[A, B any](in []A, fn func(A) B) []B {
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

// Requests.

type allocationLine struct {
	ObligationID uuid.UUID   `json:"obligation_id"`
	Amount       money.Money `json:"amount"`
	// Mode is manual (default) or suggested_oldest_due when the owner accepted the suggestion.
	Mode string `json:"mode"`
}

func toAllocations(lines []allocationLine) []Allocation {
	out := make([]Allocation, len(lines))
	for i, l := range lines {
		mode := l.Mode
		if mode == "" {
			mode = ModeManual
		}
		out[i] = Allocation{ObligationID: l.ObligationID, Amount: l.Amount, Mode: mode}
	}
	return out
}

// lineModes rejects the modes a client cannot choose (auto_earmarked is the system's).
func lineModes(lines []allocationLine) error {
	fields := map[string]string{}
	for i, l := range lines {
		if l.Mode != "" && l.Mode != ModeManual && l.Mode != ModeSuggested {
			fields[lineField(i, "mode")] = "invalid"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// paymentRequest is the body of POST /payments.
type paymentRequest struct {
	Direction          string           `json:"direction" validate:"required,oneof=in out"`
	PartyID            *uuid.UUID       `json:"party_id"`
	Purpose            string           `json:"purpose" validate:"required"`
	PaymentMethodID    uuid.UUID        `json:"payment_method_id" validate:"required"`
	Amount             money.Money      `json:"amount"`
	SourceType         string           `json:"source_type"`
	SourceID           *uuid.UUID       `json:"source_id"`
	EarmarkedInvoiceID *uuid.UUID       `json:"earmarked_invoice_id"`
	Reference          string           `json:"reference" validate:"max=100"`
	Note               string           `json:"note" validate:"max=1000"`
	Allocations        []allocationLine `json:"allocations" validate:"max=200"`
	// OpeningDate is the cutoff date of an opening advance (default: the store's opening cutoff).
	OpeningDate *date `json:"opening_date"`
}

func (r paymentRequest) input() (PaymentInput, *time.Time, error) {
	if err := lineModes(r.Allocations); err != nil {
		return PaymentInput{}, nil, err
	}
	return PaymentInput{
		Direction: r.Direction, PartyID: r.PartyID, Purpose: r.Purpose, MethodID: r.PaymentMethodID, Amount: r.Amount,
		SourceType: r.SourceType, SourceID: r.SourceID, EarmarkedInvoiceID: r.EarmarkedInvoiceID,
		Reference: r.Reference, Note: r.Note, Allocations: toAllocations(r.Allocations),
	}, r.OpeningDate.time(), nil
}

// allocationRequest is the body of POST /allocations.
type allocationRequest struct {
	PaymentID   uuid.UUID        `json:"payment_id" validate:"required"`
	Allocations []allocationLine `json:"allocations" validate:"required,min=1,max=200"`
}

func (r allocationRequest) input() (AllocationInput, error) {
	if err := lineModes(r.Allocations); err != nil {
		return AllocationInput{}, err
	}
	return AllocationInput{PaymentID: r.PaymentID, Allocations: toAllocations(r.Allocations)}, nil
}

// suggestRequest is the body of POST /allocations:suggest.
type suggestRequest struct {
	PartyID   uuid.UUID   `json:"party_id" validate:"required"`
	Direction string      `json:"direction" validate:"required,oneof=receivable payable"`
	Amount    money.Money `json:"amount"`
}

// adjustmentRequest is the body of POST /debt-adjustments.
type adjustmentRequest struct {
	PartyID            uuid.UUID   `json:"party_id" validate:"required"`
	Direction          string      `json:"direction" validate:"required,oneof=receivable payable"`
	Kind               string      `json:"kind" validate:"required"`
	Amount             money.Money `json:"amount"`
	TargetObligationID *uuid.UUID  `json:"target_obligation_id"`
	ReasonID           uuid.UUID   `json:"reason_id" validate:"required"`
	OpeningCutoffDate  *date       `json:"opening_cutoff_date"`
	DueDate            *date       `json:"due_date"`
	Note               string      `json:"note" validate:"max=1000"`
	// ExceptionConfirmationID is the owner's debt_write_off confirmation (write-offs only).
	ExceptionConfirmationID *uuid.UUID `json:"exception_confirmation_id"`
}

func (r adjustmentRequest) input(payloadHash []byte) AdjustmentInput {
	in := AdjustmentInput{
		PartyID: r.PartyID, Direction: r.Direction, Kind: r.Kind, Amount: r.Amount,
		TargetObligationID: r.TargetObligationID, ReasonID: r.ReasonID,
		OpeningCutoffDate: r.OpeningCutoffDate.time(), DueDate: r.DueDate.time(), Note: r.Note,
	}
	if r.ExceptionConfirmationID != nil {
		in.ConfirmationID = *r.ExceptionConfirmationID
		in.PayloadHash = payloadHash
	}
	return in
}

// offsetRequest is the body of POST /debt-offsets.
type offsetRequest struct {
	PartyID                uuid.UUID   `json:"party_id" validate:"required"`
	ReceivableObligationID uuid.UUID   `json:"receivable_obligation_id" validate:"required"`
	PayableObligationID    uuid.UUID   `json:"payable_obligation_id" validate:"required"`
	Amount                 money.Money `json:"amount"`
	AttachmentID           uuid.UUID   `json:"attachment_id" validate:"required"`
	Note                   string      `json:"note" validate:"max=1000"`
}

func (r offsetRequest) input() OffsetInput {
	return OffsetInput(r)
}

// refundRequest is the body of POST /refunds.
type refundRequest struct {
	ObligationID    *uuid.UUID   `json:"obligation_id"`
	PaymentID       *uuid.UUID   `json:"payment_id"`
	PaymentMethodID uuid.UUID    `json:"payment_method_id" validate:"required"`
	Amount          *money.Money `json:"amount"`
	Reference       string       `json:"reference" validate:"max=100"`
	Note            string       `json:"note" validate:"max=1000"`
}

func (r refundRequest) input() RefundInput {
	return RefundInput{
		ObligationID: r.ObligationID, PaymentID: r.PaymentID, MethodID: r.PaymentMethodID, Amount: r.Amount,
		Reference: r.Reference, Note: r.Note,
	}
}

// statementRequest is the body of POST /statements.
type statementRequest struct {
	PartyID    uuid.UUID `json:"party_id" validate:"required"`
	Direction  string    `json:"direction" validate:"required,oneof=receivable payable"`
	PeriodFrom *date     `json:"period_from" validate:"required"`
	PeriodTo   *date     `json:"period_to" validate:"required"`
}

func (r statementRequest) input() StatementInput {
	return StatementInput{PartyID: r.PartyID, Direction: r.Direction, PeriodFrom: *r.PeriodFrom.time(), PeriodTo: *r.PeriodTo.time()}
}

// disputeRequest is the body of POST /statements/{id}:dispute.
type disputeRequest struct {
	Note string `json:"note"`
}

// noteRequest is the body of POST /parties/{id}/collection-notes.
type noteRequest struct {
	Direction      string       `json:"direction"`
	Kind           string       `json:"kind" validate:"required"`
	Body           string       `json:"body"`
	PromisedDate   *date        `json:"promised_date"`
	PromisedAmount *money.Money `json:"promised_amount"`
	Channel        string       `json:"channel"`
}

func (r noteRequest) input(partyID uuid.UUID) NoteInput {
	return NoteInput{
		PartyID: partyID, Direction: r.Direction, Kind: r.Kind, Body: r.Body,
		PromisedDate: r.PromisedDate.time(), PromisedAmount: r.PromisedAmount, Channel: r.Channel,
	}
}

// reminderRequest is the body of POST /parties/{id}/reminders.
type reminderRequest struct {
	Channel string `json:"channel" validate:"required"`
}

// Responses.

type settlementDTO struct {
	ID             uuid.UUID   `json:"id"`
	ObligationID   uuid.UUID   `json:"obligation_id"`
	SettledByType  string      `json:"settled_by_type"`
	SettledByID    uuid.UUID   `json:"settled_by_id"`
	Amount         money.Money `json:"amount"`
	BusinessDate   string      `json:"business_date"`
	AllocationMode string      `json:"allocation_mode"`
	ReversalOf     *uuid.UUID  `json:"reversal_of"`
	PostedAt       time.Time   `json:"posted_at"`
}

func toSettlementDTO(s Settlement) settlementDTO {
	return settlementDTO{
		ID: s.ID, ObligationID: s.ObligationID, SettledByType: s.SettledByType, SettledByID: s.SettledByID,
		Amount: s.Amount, BusinessDate: dateOf(s.BusinessDate), AllocationMode: s.AllocationMode,
		ReversalOf: s.ReversalOf, PostedAt: s.PostedAt,
	}
}

type allocationDTO struct {
	ObligationID uuid.UUID   `json:"obligation_id"`
	Amount       money.Money `json:"amount"`
	Mode         string      `json:"mode"`
}

func toAllocationDTO(a Allocation) allocationDTO {
	return allocationDTO(a)
}

type paymentDTO struct {
	ID                 uuid.UUID       `json:"id"`
	DocNo              string          `json:"doc_no"`
	Direction          string          `json:"direction"`
	PartyID            *uuid.UUID      `json:"party_id"`
	Purpose            string          `json:"purpose"`
	FundAccountID      uuid.UUID       `json:"fund_account_id"`
	PaymentMethodID    uuid.UUID       `json:"payment_method_id"`
	Amount             money.Money     `json:"amount"`
	AllocatedAmount    money.Money     `json:"allocated_amount"`
	Unallocated        money.Money     `json:"unallocated"`
	Status             string          `json:"status"`
	EarmarkedInvoiceID *uuid.UUID      `json:"earmarked_invoice_id"`
	SourceType         *string         `json:"source_type"`
	SourceID           *uuid.UUID      `json:"source_id"`
	BusinessDate       string          `json:"business_date"`
	Reference          *string         `json:"reference"`
	Note               *string         `json:"note"`
	RefundOf           *uuid.UUID      `json:"refund_of"`
	PlannedAllocations []allocationDTO `json:"planned_allocations"`
	PostedAt           time.Time       `json:"posted_at"`
	ConfirmedAt        *time.Time      `json:"confirmed_at"`
	ReversedOn         *string         `json:"reversed_on"`
	Version            int             `json:"version"`
}

func toPaymentDTO(p Payment) paymentDTO {
	return paymentDTO{
		ID: p.ID, DocNo: p.DocNo, Direction: p.Direction, PartyID: p.PartyID, Purpose: p.Purpose,
		FundAccountID: p.FundAccountID, PaymentMethodID: p.PaymentMethodID, Amount: p.Amount,
		AllocatedAmount: p.AllocatedAmount, Unallocated: p.Unallocated(), Status: p.Status,
		EarmarkedInvoiceID: p.EarmarkedInvoiceID, SourceType: p.SourceType, SourceID: p.SourceID,
		BusinessDate: dateOf(p.BusinessDate), Reference: p.Reference, Note: p.Note, RefundOf: p.RefundOf,
		PlannedAllocations: mapSlice(p.Planned, toAllocationDTO), PostedAt: p.PostedAt, ConfirmedAt: p.ConfirmedAt,
		ReversedOn: dateString(p.ReversedOn), Version: p.Version,
	}
}

// paymentResponse is a posted payment with the settlements the posting wrote.
type paymentResponse struct {
	paymentDTO
	Settlements []settlementDTO `json:"settlements"`
}

func toPaymentResponse(r PaymentResult) paymentResponse {
	return paymentResponse{paymentDTO: toPaymentDTO(r.Payment), Settlements: mapSlice(r.Settlements, toSettlementDTO)}
}

type installmentDTO struct {
	InstallmentNo int         `json:"installment_no"`
	DueDate       string      `json:"due_date"`
	Amount        money.Money `json:"amount"`
}

func toInstallmentDTO(i Installment) installmentDTO {
	return installmentDTO{InstallmentNo: i.InstallmentNo, DueDate: dateOf(i.DueDate), Amount: i.Amount}
}

type obligationDTO struct {
	ID             uuid.UUID   `json:"id"`
	PartyID        uuid.UUID   `json:"party_id"`
	Direction      string      `json:"direction"`
	SourceType     string      `json:"source_type"`
	SourceID       uuid.UUID   `json:"source_id"`
	SourceDocNo    string      `json:"source_doc_no"`
	BusinessDate   string      `json:"business_date"`
	DueDate        string      `json:"due_date"`
	Amount         money.Money `json:"amount"`
	SettledAmount  money.Money `json:"settled_amount"`
	Open           money.Money `json:"open"`
	Status         string      `json:"status"`
	ReversedOn     *string     `json:"reversed_on"`
	ReversalReason *string     `json:"reversal_reason"`
	ReversalDocNo  *string     `json:"reversal_doc_no"`
	Version        int         `json:"version"`
}

func toObligationDTO(o Obligation) obligationDTO {
	return obligationDTO{
		ID: o.ID, PartyID: o.PartyID, Direction: o.Direction, SourceType: o.SourceType, SourceID: o.SourceID,
		SourceDocNo: o.SourceDocNo, BusinessDate: dateOf(o.BusinessDate), DueDate: dateOf(o.DueDate),
		Amount: o.Amount, SettledAmount: o.SettledAmount, Open: o.Open(), Status: o.Status,
		ReversedOn: dateString(o.ReversedOn), ReversalReason: o.ReversalReason, ReversalDocNo: o.ReversalDocNo, Version: o.Version,
	}
}

type adjustmentDTO struct {
	ID                 uuid.UUID   `json:"id"`
	DocNo              string      `json:"doc_no"`
	PartyID            uuid.UUID   `json:"party_id"`
	Direction          string      `json:"direction"`
	Kind               string      `json:"kind"`
	Amount             money.Money `json:"amount"`
	TargetObligationID *uuid.UUID  `json:"target_obligation_id"`
	ReasonID           uuid.UUID   `json:"reason_id"`
	OpeningCutoffDate  *string     `json:"opening_cutoff_date"`
	BusinessDate       string      `json:"business_date"`
	Note               *string     `json:"note"`
	PostedAt           time.Time   `json:"posted_at"`
}

// adjustmentResponse is a DN with the obligation it created or the settlement it wrote.
type adjustmentResponse struct {
	adjustmentDTO
	Obligation *obligationDTO `json:"obligation"`
	Settlement *settlementDTO `json:"settlement"`
}

func toAdjustmentDTO(a Adjustment) adjustmentDTO {
	return adjustmentDTO{
		ID: a.ID, DocNo: a.DocNo, PartyID: a.PartyID, Direction: a.Direction, Kind: a.Kind, Amount: a.Amount,
		TargetObligationID: a.TargetObligationID, ReasonID: a.ReasonID, OpeningCutoffDate: dateString(a.OpeningCutoffDate),
		BusinessDate: dateOf(a.BusinessDate), Note: a.Note, PostedAt: a.PostedAt,
	}
}

func toAdjustmentResponse(r AdjustmentResult) adjustmentResponse {
	out := adjustmentResponse{adjustmentDTO: toAdjustmentDTO(r.Adjustment)}
	if r.Obligation != nil {
		o := toObligationDTO(*r.Obligation)
		out.Obligation = &o
	}
	if r.Settlement != nil {
		s := toSettlementDTO(*r.Settlement)
		out.Settlement = &s
	}
	return out
}

type offsetDTO struct {
	ID                     uuid.UUID   `json:"id"`
	DocNo                  string      `json:"doc_no"`
	PartyID                uuid.UUID   `json:"party_id"`
	Amount                 money.Money `json:"amount"`
	ReceivableObligationID uuid.UUID   `json:"receivable_obligation_id"`
	PayableObligationID    uuid.UUID   `json:"payable_obligation_id"`
	AttachmentID           *uuid.UUID  `json:"attachment_id"`
	BusinessDate           string      `json:"business_date"`
	Note                   *string     `json:"note"`
	PostedAt               time.Time   `json:"posted_at"`
}

// offsetResponse is a BT with its two settlements (receivable first).
type offsetResponse struct {
	offsetDTO
	Settlements []settlementDTO `json:"settlements"`
}

func toOffsetResponse(r OffsetResult) offsetResponse {
	return offsetResponse{offsetDTO: toOffsetDTO(r.Offset), Settlements: mapSlice(r.Settlements, toSettlementDTO)}
}

func toOffsetDTO(o Offset) offsetDTO {
	return offsetDTO{
		ID: o.ID, DocNo: o.DocNo, PartyID: o.PartyID, Amount: o.Amount,
		ReceivableObligationID: o.ReceivableObligationID, PayableObligationID: o.PayableObligationID,
		AttachmentID: o.AttachmentID, BusinessDate: dateOf(o.BusinessDate), Note: o.Note, PostedAt: o.PostedAt,
	}
}

type partySummaryDTO struct {
	PartyID          uuid.UUID   `json:"party_id"`
	Name             string      `json:"name"`
	Phone            *string     `json:"phone"`
	Direction        string      `json:"direction"`
	Open             money.Money `json:"open"`
	Overdue          money.Money `json:"overdue"`
	OldestOverdueDue *string     `json:"oldest_overdue_due"`
	OpenDocuments    int         `json:"open_documents"`
	Advance          money.Money `json:"advance"`
}

func toPartySummaryDTO(p PartySummary) partySummaryDTO {
	return partySummaryDTO{
		PartyID: p.PartyID, Name: p.Name, Phone: p.Phone, Direction: p.Direction, Open: p.Open, Overdue: p.Overdue,
		OldestOverdueDue: dateString(p.OldestOverdueDue), OpenDocuments: p.OpenDocuments, Advance: p.Advance,
	}
}

type ledgerDocumentDTO struct {
	obligationDTO
	NextDue      string           `json:"next_due"`
	Overdue      money.Money      `json:"overdue"`
	Installments []installmentDTO `json:"installments"`
	Settlements  []settlementDTO  `json:"settlements"`
}

type partyLedgerDTO struct {
	Party      partyRef            `json:"party"`
	Receivable money.Money         `json:"receivable"`
	Payable    money.Money         `json:"payable"`
	Documents  []ledgerDocumentDTO `json:"documents"`
	Advances   []paymentDTO        `json:"advances"`
	Pending    []paymentDTO        `json:"pending_verification"`
}

func toPartyLedgerDTO(l PartyLedger) partyLedgerDTO {
	return partyLedgerDTO{
		Party: l.Party, Receivable: l.Receivable, Payable: l.Payable,
		Documents: mapSlice(l.Documents, func(d LedgerDocument) ledgerDocumentDTO {
			return ledgerDocumentDTO{
				obligationDTO: toObligationDTO(d.Obligation), NextDue: dateOf(d.NextDue), Overdue: d.Overdue,
				Installments: mapSlice(d.Installments, toInstallmentDTO), Settlements: mapSlice(d.Settlements, toSettlementDTO),
			}
		}),
		Advances: mapSlice(l.Advances, toPaymentDTO),
		Pending:  mapSlice(l.Pending, toPaymentDTO),
	}
}

type agingBucketsDTO struct {
	Current money.Money `json:"current"`
	D1To30  money.Money `json:"d1_30"`
	D31To60 money.Money `json:"d31_60"`
	D61To90 money.Money `json:"d61_90"`
	Over90  money.Money `json:"over_90"`
	Total   money.Money `json:"total"`
}

func toAgingBucketsDTO(b AgingBuckets) agingBucketsDTO {
	return agingBucketsDTO{Current: b.Current, D1To30: b.D1To30, D31To60: b.D31To60, D61To90: b.D61To90, Over90: b.Over90, Total: b.Total()}
}

type agingRowDTO struct {
	PartyID uuid.UUID       `json:"party_id"`
	Name    string          `json:"name"`
	Buckets agingBucketsDTO `json:"buckets"`
}

type agingDTO struct {
	Direction string          `json:"direction"`
	AsOf      string          `json:"as_of"`
	Rows      []agingRowDTO   `json:"rows"`
	Totals    agingBucketsDTO `json:"totals"`
}

func toAgingDTO(r AgingReport) agingDTO {
	return agingDTO{
		Direction: r.Direction, AsOf: dateOf(r.AsOf), Totals: toAgingBucketsDTO(r.Totals),
		Rows: mapSlice(r.Rows, func(x AgingRow) agingRowDTO {
			return agingRowDTO{PartyID: x.PartyID, Name: x.Name, Buckets: toAgingBucketsDTO(x.Buckets)}
		}),
	}
}

type forecastDayDTO struct {
	Date          string      `json:"date"`
	ReceivableDue money.Money `json:"receivable_due"`
	PayableDue    money.Money `json:"payable_due"`
	Promised      money.Money `json:"promised"`
	ConfirmedIn   money.Money `json:"confirmed_in"`
	ConfirmedOut  money.Money `json:"confirmed_out"`
}

type forecastDTO struct {
	From              string           `json:"from"`
	To                string           `json:"to"`
	OverdueReceivable money.Money      `json:"overdue_receivable"`
	OverduePayable    money.Money      `json:"overdue_payable"`
	Days              []forecastDayDTO `json:"days"`
}

func toForecastDTO(f Forecast) forecastDTO {
	return forecastDTO{
		From: dateOf(f.From), To: dateOf(f.To), OverdueReceivable: f.OverdueReceivable, OverduePayable: f.OverduePayable,
		Days: mapSlice(f.Days, func(d ForecastDay) forecastDayDTO {
			return forecastDayDTO{
				Date: dateOf(d.Date), ReceivableDue: d.ReceivableDue, PayableDue: d.PayableDue, Promised: d.Promised,
				ConfirmedIn: d.ConfirmedIn, ConfirmedOut: d.ConfirmedOut,
			}
		}),
	}
}

type pendingPaymentDTO struct {
	paymentDTO
	PartyName  *string `json:"party_name"`
	MethodName string  `json:"method_name"`
}

func toPendingPaymentDTO(p PendingPayment) pendingPaymentDTO {
	return pendingPaymentDTO{paymentDTO: toPaymentDTO(p.Payment), PartyName: p.PartyName, MethodName: p.MethodName}
}

type statementDTO struct {
	ID             uuid.UUID  `json:"id"`
	DocNo          string     `json:"doc_no"`
	PartyID        uuid.UUID  `json:"party_id"`
	Direction      string     `json:"direction"`
	PeriodFrom     string     `json:"period_from"`
	PeriodTo       string     `json:"period_to"`
	Status         string     `json:"status"`
	CurrentVersion int        `json:"current_version"`
	SentVersion    *int       `json:"sent_version"`
	SentAt         *time.Time `json:"sent_at"`
	DisputeNote    *string    `json:"dispute_note"`
	CreatedAt      time.Time  `json:"created_at"`
	Version        int        `json:"version"`
}

func toStatementDTO(s Statement) statementDTO {
	return statementDTO{
		ID: s.ID, DocNo: s.DocNo, PartyID: s.PartyID, Direction: s.Direction, PeriodFrom: dateOf(s.PeriodFrom),
		PeriodTo: dateOf(s.PeriodTo), Status: s.Status, CurrentVersion: s.CurrentVersion, SentVersion: s.SentVersion,
		SentAt: s.SentAt, DisputeNote: s.DisputeNote, CreatedAt: s.CreatedAt, Version: s.Version,
	}
}

// statementRowDTO is a statement with its party name and the balances of its current version.
type statementRowDTO struct {
	statementDTO
	PartyName      string      `json:"party_name"`
	OpeningBalance money.Money `json:"opening_balance"`
	ClosingBalance money.Money `json:"closing_balance"`
}

func toStatementRowDTO(r StatementRow) statementRowDTO {
	return statementRowDTO{
		statementDTO: toStatementDTO(r.Statement), PartyName: r.PartyName,
		OpeningBalance: r.OpeningBalance, ClosingBalance: r.ClosingBalance,
	}
}

type statementVersionDTO struct {
	StatementID    uuid.UUID       `json:"statement_id"`
	DocNo          string          `json:"doc_no"`
	VersionNo      int             `json:"version_no"`
	Status         string          `json:"status"`
	OpeningBalance money.Money     `json:"opening_balance"`
	ClosingBalance money.Money     `json:"closing_balance"`
	Snapshot       json.RawMessage `json:"snapshot"`
	CreatedAt      time.Time       `json:"created_at"`
}

func toStatementVersionDTO(v StatementVersionView) statementVersionDTO {
	return statementVersionDTO{
		StatementID: v.Statement.ID, DocNo: v.Statement.DocNo, VersionNo: v.Version.VersionNo, Status: v.Status,
		OpeningBalance: v.Version.OpeningBalance, ClosingBalance: v.Version.ClosingBalance,
		Snapshot: v.Version.Snapshot, CreatedAt: v.Version.CreatedAt,
	}
}

type collectionNoteDTO struct {
	ID             uuid.UUID    `json:"id"`
	PartyID        uuid.UUID    `json:"party_id"`
	Direction      string       `json:"direction"`
	Kind           string       `json:"kind"`
	Body           *string      `json:"body"`
	PromisedDate   *string      `json:"promised_date"`
	PromisedAmount *money.Money `json:"promised_amount"`
	Channel        *string      `json:"channel"`
	CreatedBy      uuid.UUID    `json:"created_by"`
	CreatedAt      time.Time    `json:"created_at"`
}

func toCollectionNoteDTO(n CollectionNote) collectionNoteDTO {
	return collectionNoteDTO{
		ID: n.ID, PartyID: n.PartyID, Direction: n.Direction, Kind: n.Kind, Body: n.Body,
		PromisedDate: dateString(n.PromisedDate), PromisedAmount: n.PromisedAmount, Channel: n.Channel,
		CreatedBy: n.CreatedBy, CreatedAt: n.CreatedAt,
	}
}

type reminderDTO struct {
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	Note    collectionNoteDTO `json:"note"`
	// Duplicate is true when a reminder on this channel was already logged today.
	Duplicate bool `json:"duplicate"`
	// Email is the outcome of the email delivery (not_requested unless the channel is email).
	Email string `json:"email"`
}

func toReminderDTO(r ReminderResult) reminderDTO {
	return reminderDTO{Subject: r.Subject, Text: r.Text, Note: toCollectionNoteDTO(r.Note), Duplicate: r.Duplicate, Email: r.Email}
}

// documentSettlementDTO is a settlement with the number of the document it settled.
type documentSettlementDTO struct {
	settlementDTO
	ObligationDocNo string `json:"obligation_doc_no"`
}

func toDocumentSettlementDTO(s DocumentSettlement) documentSettlementDTO {
	return documentSettlementDTO{settlementDTO: toSettlementDTO(s.Settlement), ObligationDocNo: s.ObligationDocNo}
}

// paymentDetailDTO is GET /payments/{id}: the payment, its names and every settlement it wrote.
type paymentDetailDTO struct {
	pendingPaymentDTO
	Settlements []documentSettlementDTO `json:"settlements"`
}

func toPaymentDetailDTO(p PaymentDetail) paymentDetailDTO {
	return paymentDetailDTO{
		pendingPaymentDTO: toPendingPaymentDTO(p.PendingPayment), Settlements: mapSlice(p.Settlements, toDocumentSettlementDTO),
	}
}

type adjustmentRowDTO struct {
	adjustmentDTO
	PartyName   string  `json:"party_name"`
	TargetDocNo *string `json:"target_doc_no"`
	ReasonName  string  `json:"reason_name"`
}

func toAdjustmentRowDTO(r AdjustmentRow) adjustmentRowDTO {
	return adjustmentRowDTO{
		adjustmentDTO: toAdjustmentDTO(r.Adjustment), PartyName: r.PartyName, TargetDocNo: r.TargetDocNo, ReasonName: r.ReasonName,
	}
}

// adjustmentDetailDTO is GET /debt-adjustments/{id}.
type adjustmentDetailDTO struct {
	adjustmentRowDTO
	Obligation  *obligationDTO          `json:"obligation"`
	Settlements []documentSettlementDTO `json:"settlements"`
}

func toAdjustmentDetailDTO(d AdjustmentDetail) adjustmentDetailDTO {
	out := adjustmentDetailDTO{
		adjustmentRowDTO: toAdjustmentRowDTO(d.AdjustmentRow), Settlements: mapSlice(d.Settlements, toDocumentSettlementDTO),
	}
	if d.Obligation != nil {
		o := toObligationDTO(*d.Obligation)
		out.Obligation = &o
	}
	return out
}

type offsetRowDTO struct {
	offsetDTO
	PartyName       string `json:"party_name"`
	ReceivableDocNo string `json:"receivable_doc_no"`
	PayableDocNo    string `json:"payable_doc_no"`
}

func toOffsetRowDTO(r OffsetRow) offsetRowDTO {
	return offsetRowDTO{
		offsetDTO: toOffsetDTO(r.Offset), PartyName: r.PartyName, ReceivableDocNo: r.ReceivableDocNo, PayableDocNo: r.PayableDocNo,
	}
}

// offsetDetailDTO is GET /debt-offsets/{id}.
type offsetDetailDTO struct {
	offsetRowDTO
	Settlements []documentSettlementDTO `json:"settlements"`
}

func toOffsetDetailDTO(d OffsetDetail) offsetDetailDTO {
	return offsetDetailDTO{offsetRowDTO: toOffsetRowDTO(d.OffsetRow), Settlements: mapSlice(d.Settlements, toDocumentSettlementDTO)}
}
