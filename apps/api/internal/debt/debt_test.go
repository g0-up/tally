package debt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func vnd(s string) money.Money { return money.MustMoney(s) }

func candidate(due, dated, amount, settled string) Candidate {
	return Candidate{
		Obligation: Obligation{
			ID: ids.New(), BusinessDate: day(dated), DueDate: day(due), Amount: vnd(amount), SettledAmount: vnd(settled),
			Direction: Receivable, Status: ObligationOpen,
		},
		NextDue: day(due),
	}
}

func TestOldestDueFirstFillsTheEarliestDueDocumentFirst(t *testing.T) {
	later := candidate("2026-10-15", "2026-09-15", "4000000", "0")
	earlier := candidate("2026-10-01", "2026-09-20", "3000000", "0")
	got := OldestDueFirst{}.Plan(vnd("5000000"), []Candidate{later, earlier})
	if len(got) != 2 {
		t.Fatalf("plan = %+v, want two lines", got)
	}
	if got[0].ObligationID != earlier.ID || !got[0].Amount.Equal(vnd("3000000")) {
		t.Errorf("first line = %+v, want 3.000.000 on the document due 01/10", got[0])
	}
	if got[1].ObligationID != later.ID || !got[1].Amount.Equal(vnd("2000000")) {
		t.Errorf("second line = %+v, want 2.000.000 on the document due 15/10", got[1])
	}
	for _, l := range got {
		if l.Mode != ModeSuggested {
			t.Errorf("mode = %q, want %q", l.Mode, ModeSuggested)
		}
	}
}

func TestOldestDueFirstSkipsSettledDocumentsAndStopsWhenSpent(t *testing.T) {
	settled := candidate("2026-09-01", "2026-08-01", "1000000", "1000000")
	partly := candidate("2026-09-10", "2026-08-10", "1000000", "400000")
	last := candidate("2026-09-20", "2026-08-20", "1000000", "0")
	got := OldestDueFirst{}.Plan(vnd("600000"), []Candidate{last, settled, partly})
	if len(got) != 1 || got[0].ObligationID != partly.ID || !got[0].Amount.Equal(vnd("600000")) {
		t.Fatalf("plan = %+v, want 600.000 on the partly paid document only", got)
	}
	if got := (OldestDueFirst{}).Plan(vnd("0"), []Candidate{last}); len(got) != 0 {
		t.Errorf("plan of nothing = %+v, want none", got)
	}
}

func TestOldestDueFirstBreaksTiesByDocumentDate(t *testing.T) {
	newer := candidate("2026-10-01", "2026-09-10", "100", "0")
	older := candidate("2026-10-01", "2026-09-01", "100", "0")
	got := OldestDueFirst{}.Plan(vnd("100"), []Candidate{newer, older})
	if len(got) != 1 || got[0].ObligationID != older.ID {
		t.Fatalf("plan = %+v, want the older document", got)
	}
}

func TestEarmarkedAndManualModes(t *testing.T) {
	c := candidate("2026-10-01", "2026-09-01", "500", "0")
	if got := (Earmarked{}).Plan(vnd("800"), []Candidate{c}); len(got) != 1 || got[0].Mode != ModeAutoEarmarked || !got[0].Amount.Equal(vnd("500")) {
		t.Errorf("earmarked plan = %+v", got)
	}
	lines := Manual{Lines: []Allocation{{ObligationID: c.ID, Amount: vnd("10")}, {ObligationID: ids.New(), Amount: vnd("5"), Mode: ModeSuggested}}}.Plan(money.Money{}, nil)
	if lines[0].Mode != ModeManual || lines[1].Mode != ModeSuggested {
		t.Errorf("manual modes = %q, %q", lines[0].Mode, lines[1].Mode)
	}
}

func obligationsOf(cs ...Candidate) map[uuid.UUID]*Obligation {
	out := map[uuid.UUID]*Obligation{}
	for i := range cs {
		ob := cs[i].Obligation
		out[ob.ID] = &ob
	}
	return out
}

func TestCheckAllocationsGuardsEveryLine(t *testing.T) {
	party := ids.New()
	first := candidate("2026-10-01", "2026-09-01", "3000000", "0")
	second := candidate("2026-10-15", "2026-09-01", "4000000", "0")
	first.PartyID, second.PartyID = party, party
	other := candidate("2026-10-15", "2026-09-01", "4000000", "0")
	other.PartyID = ids.New()
	payable := candidate("2026-10-15", "2026-09-01", "4000000", "0")
	payable.PartyID, payable.Direction = party, Payable
	settled := candidate("2026-10-15", "2026-09-01", "4000000", "4000000")
	settled.PartyID = party
	obs := obligationsOf(first, second, other, payable, settled)

	cases := []struct {
		name   string
		lines  []Allocation
		budget string
		code   string
	}{
		{"within open and budget", []Allocation{{ObligationID: first.ID, Amount: vnd("3000000"), Mode: ModeManual}, {ObligationID: second.ID, Amount: vnd("2000000"), Mode: ModeManual}}, "5000000", ""},
		{"more than open", []Allocation{{ObligationID: second.ID, Amount: vnd("6000000"), Mode: ModeManual}}, "6000000", "allocation_exceeds_open"},
		{"more than the payment", []Allocation{{ObligationID: first.ID, Amount: vnd("3000000"), Mode: ModeManual}, {ObligationID: second.ID, Amount: vnd("3000000"), Mode: ModeManual}}, "5000000", "allocation_exceeds_payment"},
		{"another party", []Allocation{{ObligationID: other.ID, Amount: vnd("1"), Mode: ModeManual}}, "1", "allocation_party_mismatch"},
		{"other ledger", []Allocation{{ObligationID: payable.ID, Amount: vnd("1"), Mode: ModeManual}}, "1", "allocation_direction_mismatch"},
		{"settled document", []Allocation{{ObligationID: settled.ID, Amount: vnd("1"), Mode: ModeManual}}, "1", "obligation_settled"},
		{"unknown document", []Allocation{{ObligationID: ids.New(), Amount: vnd("1"), Mode: ModeManual}}, "1", "obligation_not_found"},
		{"duplicate line", []Allocation{{ObligationID: first.ID, Amount: vnd("1"), Mode: ModeManual}, {ObligationID: first.ID, Amount: vnd("1"), Mode: ModeManual}}, "2", "validation_failed"},
		{"zero amount", []Allocation{{ObligationID: first.ID, Amount: vnd("0"), Mode: ModeManual}}, "1", "validation_failed"},
		{"unknown mode", []Allocation{{ObligationID: first.ID, Amount: vnd("1"), Mode: "magic"}}, "1", "validation_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAllocations(tc.lines, party, Receivable, vnd(tc.budget), obs)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if !apperr.Is(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestBucketOf(t *testing.T) {
	asOf := day("2026-09-30")
	cases := map[string]string{
		"2026-10-05": BucketCurrent, "2026-09-30": BucketCurrent, "2026-09-29": Bucket1To30, "2026-08-31": Bucket1To30,
		"2026-08-30": Bucket31To60, "2026-08-01": Bucket31To60, "2026-07-31": Bucket61To90, "2026-07-02": Bucket61To90,
		"2026-07-01": BucketOver90,
	}
	for due, want := range cases {
		if got := bucketOf(day(due), asOf); got != want {
			t.Errorf("bucketOf(%s) = %s, want %s", due, got, want)
		}
	}
}

func TestAgedPartsPaysTheEarliestInstallmentsFirst(t *testing.T) {
	inst := []Installment{
		{InstallmentNo: 2, DueDate: day("2026-10-01"), Amount: vnd("400")},
		{InstallmentNo: 1, DueDate: day("2026-09-01"), Amount: vnd("300")},
		{InstallmentNo: 3, DueDate: day("2026-11-01"), Amount: vnd("300")},
	}
	got := agedParts(vnd("1000"), vnd("500"), day("2026-11-01"), inst)
	if len(got) != 2 {
		t.Fatalf("parts = %+v, want the rest of installment 2 and installment 3", got)
	}
	if !got[0].Due.Equal(day("2026-10-01")) || !got[0].Unpaid.Equal(vnd("200")) {
		t.Errorf("first part = %+v, want 200 due 01/10", got[0])
	}
	if !got[1].Due.Equal(day("2026-11-01")) || !got[1].Unpaid.Equal(vnd("300")) {
		t.Errorf("second part = %+v, want 300 due 01/11", got[1])
	}
	if parts := agedParts(vnd("1000"), vnd("400"), day("2026-10-01"), nil); len(parts) != 1 || !parts[0].Unpaid.Equal(vnd("600")) {
		t.Errorf("parts without schedule = %+v", parts)
	}
	if parts := agedParts(vnd("1000"), vnd("1000"), day("2026-10-01"), nil); len(parts) != 0 {
		t.Errorf("parts of a settled document = %+v", parts)
	}
}

func TestAgingBucketsTotal(t *testing.T) {
	var b AgingBuckets
	b.add(BucketCurrent, vnd("1"))
	b.add(Bucket1To30, vnd("2"))
	b.add(Bucket31To60, vnd("3"))
	b.add(Bucket61To90, vnd("4"))
	b.add(BucketOver90, vnd("5"))
	if !b.Total().Equal(vnd("15")) {
		t.Errorf("total = %s, want 15", b.Total())
	}
}

func TestFormatVND(t *testing.T) {
	cases := map[string]string{"0": "0 đ", "999": "999 đ", "1000": "1.000 đ", "1250000": "1.250.000 đ", "-45000": "-45.000 đ"}
	for in, want := range cases {
		if got := formatVND(vnd(in)); got != want {
			t.Errorf("formatVND(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestStatementTransitions(t *testing.T) {
	allowed := []struct{ action, from, to string }{
		{statementGenerate, StatementDraft, StatementDraft},
		{statementGenerate, StatementSent, StatementDraft},
		{statementGenerate, StatementDisputed, StatementDraft},
		{statementSend, StatementDraft, StatementSent},
		{statementConfirm, StatementSent, StatementConfirmed},
		{statementDispute, StatementSent, StatementDisputed},
	}
	for _, tc := range allowed {
		if got, err := checkTransition(tc.action, tc.from); err != nil || got != tc.to {
			t.Errorf("%s from %s = %q, %v; want %q", tc.action, tc.from, got, err, tc.to)
		}
	}
	for _, action := range []string{statementGenerate, statementSend, statementConfirm, statementDispute} {
		if _, err := checkTransition(action, StatementConfirmed); !apperr.Is(err, "statement_confirmed") {
			t.Errorf("%s of a confirmed statement: err = %v", action, err)
		}
	}
	if _, err := checkTransition(statementSend, StatementSent); !apperr.Is(err, "statement_invalid_state") {
		t.Errorf("sending a sent statement: err = %v", err)
	}
	if _, err := checkTransition(statementConfirm, StatementDraft); !apperr.Is(err, "statement_invalid_state") {
		t.Errorf("confirming a draft: err = %v", err)
	}
}

func TestVersionStatusMarksEarlierVersionsSuperseded(t *testing.T) {
	st := Statement{Status: StatementDraft, CurrentVersion: 2}
	if got := st.VersionStatus(1); got != StatementSuperseded {
		t.Errorf("v1 = %s, want superseded", got)
	}
	if got := st.VersionStatus(2); got != StatementDraft {
		t.Errorf("v2 = %s, want draft", got)
	}
}

func TestUnallocatedCountsConfirmedPaymentsOnly(t *testing.T) {
	p := Payment{Status: PaymentConfirmed, Amount: vnd("1000"), AllocatedAmount: vnd("300")}
	if !p.Unallocated().Equal(vnd("700")) {
		t.Errorf("unallocated = %s, want 700", p.Unallocated())
	}
	p.Status = PaymentPending
	if !p.Unallocated().IsZero() {
		t.Errorf("unallocated of a pending payment = %s, want 0", p.Unallocated())
	}
}

func TestLedgerDirections(t *testing.T) {
	if PaymentDirectionOf(Receivable) != In || PaymentDirectionOf(Payable) != Out {
		t.Error("payment direction of a ledger")
	}
	if LedgerOf(In) != Receivable || LedgerOf(Out) != Payable {
		t.Error("ledger of a payment direction")
	}
	if Opposite(Receivable) != Payable || Opposite(Payable) != Receivable {
		t.Error("opposite ledger")
	}
}

func TestObligationFactoryBuild(t *testing.T) {
	party, source := ids.New(), ids.New()
	base := ObligationInput{PartyID: party, SourceType: SourceSalesDocument, SourceID: source, SourceDocNo: "HD-1", Amount: vnd("1000")}

	ob, inst, err := ObligationFactory{}.Build(Receivable, base, day("2026-09-01"), 30)
	if err != nil {
		t.Fatal(err)
	}
	if !ob.DueDate.Equal(day("2026-10-01")) || len(inst) != 0 || ob.Status != ObligationOpen {
		t.Errorf("obligation = %+v, %d installments; want due on the payment terms", ob, len(inst))
	}

	scheduled := base
	scheduled.Schedule = []InstallmentInput{{DueDate: day("2026-09-15"), Amount: vnd("400")}, {DueDate: day("2026-10-15"), Amount: vnd("600")}}
	ob, inst, err = ObligationFactory{}.Build(Receivable, scheduled, day("2026-09-01"), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(inst) != 2 || !ob.DueDate.Equal(day("2026-10-15")) || inst[1].InstallmentNo != 2 || inst[0].ObligationID != ob.ID {
		t.Errorf("scheduled obligation = %+v, installments = %+v", ob, inst)
	}

	bad := []struct {
		name      string
		direction string
		mutate    func(*ObligationInput)
		field     string
	}{
		{"sales on payables", Payable, func(*ObligationInput) {}, "source_type"},
		{"no amount", Receivable, func(in *ObligationInput) { in.Amount = vnd("0") }, "amount"},
		{"no party", Receivable, func(in *ObligationInput) { in.PartyID = uuid.Nil }, "party_id"},
		{"schedule total", Receivable, func(in *ObligationInput) {
			in.Schedule = []InstallmentInput{{DueDate: day("2026-09-15"), Amount: vnd("400")}}
		}, "schedule"},
		{"schedule out of order", Receivable, func(in *ObligationInput) {
			in.Schedule = []InstallmentInput{{DueDate: day("2026-10-15"), Amount: vnd("400")}, {DueDate: day("2026-09-15"), Amount: vnd("600")}}
		}, "schedule[1].due_date"},
		{"due before the document", Receivable, func(in *ObligationInput) { d := day("2026-08-01"); in.DueDate = &d }, "due_date"},
		{"earmark on payables", Payable, func(in *ObligationInput) {
			in.SourceType = SourceReceipt
			id := ids.New()
			in.EarmarkedInvoiceID = &id
		}, "earmarked_invoice_id"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			_, _, err := ObligationFactory{}.Build(tc.direction, in, day("2026-09-01"), 30)
			e, ok := apperr.As(err)
			if !ok || e.Fields[tc.field] == "" {
				t.Fatalf("err = %v, want a %s field error", err, tc.field)
			}
		})
	}

	opening := base
	opening.SourceType = SourceOpeningBalance
	d := day("2026-08-01")
	opening.DueDate = &d
	if _, _, err := (ObligationFactory{}).Build(Receivable, opening, day("2026-09-01"), 0); err != nil {
		t.Errorf("an opening balance may be past due already: %v", err)
	}
}

func TestCreditCheckErr(t *testing.T) {
	limit := vnd("1000")
	if err := (CreditCheck{Limit: &limit, Exceeded: false}).Err(); err != nil {
		t.Errorf("within limit: %v", err)
	}
	if err := (CreditCheck{Exceeded: true}).Err(); err != nil {
		t.Errorf("no limit: %v", err)
	}
	err := CreditCheck{Limit: &limit, Exceeded: true, OpenReceivable: vnd("900"), Undelivered: vnd("200"), Exposure: vnd("1100")}.Err()
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.ExceptionRequired || e.ExceptionKind != "credit_limit_exceeded" {
		t.Fatalf("err = %v, want the credit_limit_exceeded exception", err)
	}
}

func TestCheckPaymentInput(t *testing.T) {
	party := ids.New()
	ok := PaymentInput{Direction: In, PartyID: &party, Purpose: PurposeSettlement, MethodID: ids.New(), Amount: vnd("100"), SourceType: PaymentSourceDebt}
	if err := checkPaymentInput(ok); err != nil {
		t.Fatalf("valid payment: %v", err)
	}
	invoice := ids.New()
	cases := []struct {
		name   string
		mutate func(*PaymentInput)
		field  string
	}{
		{"direction", func(in *PaymentInput) { in.Direction = "sideways" }, "direction"},
		{"amount", func(in *PaymentInput) { in.Amount = vnd("0") }, "amount"},
		{"method", func(in *PaymentInput) { in.MethodID = uuid.Nil }, "method_id"},
		{"anonymous debt payment", func(in *PaymentInput) { in.PartyID = nil }, "party_id"},
		{"advance refund without its payment", func(in *PaymentInput) { in.Purpose = PurposeAdvanceRefund }, "refund_of"},
		{"earmark on an advance", func(in *PaymentInput) { in.EarmarkedInvoiceID = &invoice; in.Purpose = PurposeAdvance }, "earmarked_invoice_id"},
		{"allocated opening advance", func(in *PaymentInput) {
			in.Purpose, in.SourceType = PurposeOpeningAdvance, ""
			in.Allocations = []Allocation{{ObligationID: ids.New(), Amount: vnd("1"), Mode: ModeManual}}
		}, "allocations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ok
			tc.mutate(&in)
			e, isApp := apperr.As(checkPaymentInput(in))
			if !isApp || e.Fields[tc.field] == "" {
				t.Fatalf("err = %v, want a %s field error", e, tc.field)
			}
		})
	}
	walkIn := ok
	walkIn.PartyID, walkIn.SourceType = nil, PaymentSourcePOS
	if err := checkPaymentInput(walkIn); err != nil {
		t.Errorf("a walk-in POS sale needs no party: %v", err)
	}
}

func TestLineModesRejectSystemModes(t *testing.T) {
	if err := lineModes([]allocationLine{{Mode: ""}, {Mode: ModeManual}, {Mode: ModeSuggested}}); err != nil {
		t.Errorf("client modes: %v", err)
	}
	e, ok := apperr.As(lineModes([]allocationLine{{Mode: ModeManual}, {Mode: ModeAutoEarmarked}}))
	if !ok || e.Fields["allocations[1].mode"] != "invalid" {
		t.Errorf("auto_earmarked from a client: %v", e)
	}
}

func TestPlannedAllocationsRoundTrip(t *testing.T) {
	in := PlannedAllocations{{ObligationID: ids.New(), Amount: vnd("1250000"), Mode: ModeSuggested}}
	v, err := in.Value()
	if err != nil {
		t.Fatal(err)
	}
	var out PlannedAllocations
	if err := out.Scan(v); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ObligationID != in[0].ObligationID || !out[0].Amount.Equal(in[0].Amount) || out[0].Mode != ModeSuggested {
		t.Errorf("round trip = %+v", out)
	}
	var empty PlannedAllocations
	if err := empty.Scan(nil); err != nil || len(empty) != 0 {
		t.Errorf("NULL plan = %+v, %v", empty, err)
	}
	if _, err := json.Marshal(toAllocationDTO(in[0])); err != nil {
		t.Error(err)
	}
}
