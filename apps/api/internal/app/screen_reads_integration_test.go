//go:build integration

package app_test

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// obligationOf is the open document a posting opened under docNo.
func obligationOf(t *testing.T, tenant uuid.UUID, docNo string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT id FROM obligations WHERE source_doc_no = $1`, docNo).Scan(&id)
	})
	return id
}

// openingObligationOf is the opening balance a party was recorded with.
func openingObligationOf(t *testing.T, tenant, party uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(),
			`SELECT id FROM obligations WHERE party_id = $1 AND source_type = $2`, party, debt.SourceOpeningBalance).Scan(&id)
	})
	return id
}

func withHeaders(hs ...http.Header) http.Header {
	out := http.Header{}
	for _, h := range hs {
		for k, v := range h {
			out[k] = v
		}
	}
	return out
}

// The creates and commands the web retries after a lost response accept an Idempotency-Key: the
// retry answers the first response and stores nothing new; a different request under the key is
// refused.
func TestScreenCommandsWithAnIdempotencyKeyRunOnce(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	sku := st.newSku(t, a, "IDEM-MAN", "10000")
	st.receive(t, a, sku, nil, "10", "8000")
	sale := creditSale(t, a, st.customer, saleLine(sku, "2", nil))
	note := map[string]any{"note": "khác"}

	quote := func() (string, http.Header) {
		rec := a.do(http.MethodPost, v1+"/quotes", map[string]any{"customer_party_id": st.customer, "valid_until": "2026-03-31",
			"lines": []map[string]any{saleLine(sku, "1", nil)}})
		expect(t, rec, http.StatusCreated, "")
		q := testkit.DecodeJSON[invoiceOut](t, rec)
		return "/quotes/" + q.ID.String() + ":send", ifMatchOf(q.Version)
	}
	kit := func() (string, http.Header) {
		rec := a.do(http.MethodPost, v1+"/kits", st.giftBasket("GIO-IDEM-"+strings.ToUpper(ids.New().String()[:6])))
		expect(t, rec, http.StatusCreated, kits.RecipeDraft)
		k := testkit.DecodeJSON[kitOut](t, rec)
		return "/kits/" + k.ProductID.String() + ":publish", ifMatchOf(k.Version)
	}
	party := st.customer.String()
	cases := []struct {
		name, path, table string
		body              map[string]any
		status            int
		other             map[string]any
		// target creates the record a command acts on and answers its path and If-Match.
		target func() (string, http.Header)
	}{
		{name: "customer", path: "/customers", table: "parties", body: map[string]any{"kind": "person", "name": "Anh Minh"},
			status: http.StatusCreated, other: map[string]any{"name": "Anh Nam"}},
		{name: "consent", path: "/customers/" + party + "/consents", table: "consents", body: map[string]any{
			"channel": crm.ChannelPhone, "purpose": crm.PurposeCare, "granted": true, "source": "Tại quầy"},
			status: http.StatusCreated, other: map[string]any{"granted": false}},
		{name: "reorder draft", path: "/customers/" + party + "/reorder-drafts", table: "invoices",
			body: map[string]any{"sales_document_id": sale.ID}, status: http.StatusCreated, other: map[string]any{"sales_document_id": nil}},
		{name: "statement", path: "/statements", table: "statements", body: map[string]any{"party_id": st.customer,
			"direction": "receivable", "period_from": "2026-03-01", "period_to": "2026-03-15"},
			status: http.StatusCreated, other: map[string]any{"period_from": "2026-03-02"}},
		{name: "collection note", path: "/parties/" + party + "/collection-notes", table: "collection_notes",
			body: map[string]any{"direction": "receivable", "kind": "note", "body": "Hẹn cuối tuần"}, status: http.StatusCreated,
			other: map[string]any{"body": "Hẹn đầu tuần"}},
		{name: "quote send", table: "quotes WHERE status = 'sent'", status: http.StatusOK, other: note, target: quote},
		{name: "kit publish", table: "products WHERE type = 'kit' AND status = 'active'", status: http.StatusOK, other: note, target: kit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, extra := tc.path, http.Header{}
			if tc.target != nil {
				path, extra = tc.target()
			}
			rows := `SELECT count(*) FROM ` + tc.table
			before := countRows(t, st.tenant, rows)
			key := withHeaders(extra, http.Header{"Idempotency-Key": {ids.New().String()}})

			first := a.doWith(http.MethodPost, v1+path, tc.body, key)
			expect(t, first, tc.status, "")
			if first.Header().Get("Idempotent-Replay") != "" {
				t.Errorf("first response is flagged as a replay")
			}
			again := a.doWith(http.MethodPost, v1+path, tc.body, key)
			expect(t, again, tc.status, "")
			if again.Header().Get("Idempotent-Replay") != "true" {
				t.Errorf("retry is not flagged as a replay: %v", again.Header())
			}
			if first.Header().Get("ETag") != again.Header().Get("ETag") {
				t.Errorf("ETag = %q, then %q", first.Header().Get("ETag"), again.Header().Get("ETag"))
			}
			if got, want := jsonOf(t, again), jsonOf(t, first); !reflect.DeepEqual(got, want) {
				t.Errorf("replayed body differs:\n first: %s\nreplay: %s", first.Body, again.Body)
			}
			if n := countRows(t, st.tenant, rows); n != before+1 {
				t.Errorf("%s rows = %d, want %d", tc.table, n, before+1)
			}

			changed := map[string]any{}
			for k, v := range tc.body {
				changed[k] = v
			}
			for k, v := range tc.other {
				changed[k] = v
			}
			expect(t, a.doWith(http.MethodPost, v1+path, changed, key), http.StatusConflict, idempotency.CodeKeyReused)
			expect(t, a.doWith(http.MethodPost, v1+path, tc.body, withHeaders(extra, http.Header{"Idempotency-Key": {"has space"}})),
				http.StatusUnprocessableEntity, "idempotency_key_invalid")
			if n := countRows(t, st.tenant, rows); n != before+1 {
				t.Errorf("%s rows after the refused retries = %d, want %d", tc.table, n, before+1)
			}
		})
	}
}

// A create refused inside its transaction releases its Idempotency-Key, so the corrected request
// under the same key is created; its retry replays the first created body, duplicate warnings
// included.
func TestARefusedCreateLeavesItsKeyFreeForTheCorrectedRequest(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	tenant := tenantOf(t, a)
	phone := "0912345678"
	expect(t, a.do(http.MethodPost, v1+"/customers", map[string]any{"kind": "person", "name": "Chị Lan", "phone": phone}),
		http.StatusCreated, "")

	rows := `SELECT count(*) FROM parties`
	before := countRows(t, tenant, rows)
	key := http.Header{"Idempotency-Key": {ids.New().String()}}
	body := map[string]any{"kind": "person", "name": "Chị Lan Anh", "phone": phone}
	refused := map[string]any{"kind": "person", "name": "Chị Lan Anh", "phone": phone, "customer_group_id": ids.New()}
	expect(t, a.doWith(http.MethodPost, v1+"/customers", refused, key), http.StatusUnprocessableEntity, "customer_group_id")

	first := a.doWith(http.MethodPost, v1+"/customers", body, key)
	expect(t, first, http.StatusCreated, "")
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Error("the corrected request is flagged as a replay")
	}
	if w, _ := jsonOf(t, first)["duplicate_warnings"].([]any); len(w) == 0 {
		t.Fatalf("customer with a known phone has no duplicate warnings: %s", first.Body)
	}
	again := a.doWith(http.MethodPost, v1+"/customers", body, key)
	expect(t, again, http.StatusCreated, "")
	if again.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("retry is not flagged as a replay: %v", again.Header())
	}
	if got, want := jsonOf(t, again), jsonOf(t, first); !reflect.DeepEqual(got, want) {
		t.Errorf("replayed body differs:\n first: %s\nreplay: %s", first.Body, again.Body)
	}
	if n := countRows(t, tenant, rows); n != before+1 {
		t.Errorf("parties = %d, want %d", n, before+1)
	}
}

type paymentRowOut struct {
	ID          uuid.UUID `json:"id"`
	DocNo       string    `json:"doc_no"`
	Status      string    `json:"status"`
	Unallocated string    `json:"unallocated"`
	PartyName   *string   `json:"party_name"`
	MethodName  string    `json:"method_name"`
	Settlements []struct {
		ObligationID    uuid.UUID `json:"obligation_id"`
		Amount          string    `json:"amount"`
		ObligationDocNo string    `json:"obligation_doc_no"`
	} `json:"settlements"`
}

type paymentPage struct {
	Data       []paymentRowOut `json:"data"`
	NextCursor *string         `json:"next_cursor"`
}

type debtDocOut struct {
	ID              uuid.UUID `json:"id"`
	DocNo           string    `json:"doc_no"`
	PartyName       string    `json:"party_name"`
	ReasonName      string    `json:"reason_name"`
	ReceivableDocNo string    `json:"receivable_doc_no"`
	PayableDocNo    string    `json:"payable_doc_no"`
	Obligation      *struct {
		ID          uuid.UUID `json:"id"`
		SourceDocNo string    `json:"source_doc_no"`
	} `json:"obligation"`
	Settlements []struct {
		ObligationDocNo string `json:"obligation_doc_no"`
	} `json:"settlements"`
}

type statementRowOut struct {
	ID             uuid.UUID `json:"id"`
	PartyName      string    `json:"party_name"`
	OpeningBalance string    `json:"opening_balance"`
	ClosingBalance string    `json:"closing_balance"`
	Version        int       `json:"version"`
}

// The debt screens find a payment, a DN or a BT by its number and open it with what it settled.
func TestDebtDocumentsAreFoundByNumberAndOpenWithTheirSettlements(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildLedgerStore(t, a)
	tenant := tenantOf(t, a)

	pay := func(body map[string]any) paymentRowOut {
		rec := post(a, "/payments", body)
		expect(t, rec, http.StatusCreated, "")
		return testkit.DecodeJSON[paymentRowOut](t, rec)
	}
	settled := pay(map[string]any{"direction": "in", "party_id": st.customer, "purpose": "settlement",
		"payment_method_id": st.cashMethod, "amount": "1000000",
		"allocations": []map[string]any{{"obligation_id": st.opening, "amount": "1000000"}}})
	advance := pay(map[string]any{"direction": "in", "party_id": st.customer, "purpose": "advance",
		"payment_method_id": st.cashMethod, "amount": "500000", "allocations": []map[string]any{}})
	pending := pay(map[string]any{"direction": "in", "party_id": st.customer, "purpose": "settlement",
		"payment_method_id": st.transferMethod, "amount": "200000",
		"allocations": []map[string]any{{"obligation_id": st.opening, "amount": "200000"}}})

	t.Run("the payment list names its party and method, finds the advances and a number, and pages", func(t *testing.T) {
		all := send[paymentPage](t, a, http.MethodGet, "/payments", nil).Data
		if len(all) != 3 {
			t.Fatalf("payments = %+v, want 3", all)
		}
		for _, p := range all {
			if p.PartyName == nil || *p.PartyName != "Chị Lan" || p.MethodName == "" {
				t.Errorf("payment %s names = %v / %q", p.DocNo, p.PartyName, p.MethodName)
			}
		}
		if got := send[paymentPage](t, a, http.MethodGet, "/payments?unallocated=true", nil).Data; len(got) != 1 ||
			got[0].ID != advance.ID || got[0].Unallocated != "500000" {
			t.Errorf("unallocated payments = %+v, want the advance alone", got)
		}
		if got := send[paymentPage](t, a, http.MethodGet, "/payments?doc_no="+url.QueryEscape(pending.DocNo), nil).Data; len(got) != 1 ||
			got[0].ID != pending.ID || got[0].Status != debt.PaymentPending {
			t.Errorf("payment %s = %+v", pending.DocNo, got)
		}
		first := send[paymentPage](t, a, http.MethodGet, "/payments?limit=2", nil)
		if len(first.Data) != 2 || first.NextCursor == nil {
			t.Fatalf("first page = %+v", first)
		}
		second := send[paymentPage](t, a, http.MethodGet, "/payments?limit=2&cursor="+url.QueryEscape(*first.NextCursor), nil)
		if len(second.Data) != 1 || second.NextCursor != nil || second.Data[0].ID == first.Data[0].ID || second.Data[0].ID == first.Data[1].ID {
			t.Errorf("second page = %+v after %+v", second, first)
		}
		rec := a.do(http.MethodGet, v1+"/payments?unallocated=maybe", nil)
		if rec.Code != http.StatusUnprocessableEntity || testkit.DecodeJSON[httpx.ErrorBody](t, rec).Error.Fields["unallocated"] != "invalid" {
			t.Errorf("unallocated=maybe = %d %s", rec.Code, rec.Body)
		}
	})

	var openingDN debtDocOut
	t.Run("a DN opens with the document it opened and is found by its number", func(t *testing.T) {
		list := send[dataOf[debtDocOut]](t, a, http.MethodGet, "/debt-adjustments", nil).Data
		if len(list) != 1 || list[0].PartyName != "Chị Lan" || list[0].ReasonName == "" {
			t.Fatalf("debt adjustments = %+v", list)
		}
		openingDN = list[0]
		if got := send[dataOf[debtDocOut]](t, a, http.MethodGet, "/debt-adjustments?doc_no="+url.QueryEscape(openingDN.DocNo), nil).Data; len(got) != 1 {
			t.Errorf("DN %s = %+v", openingDN.DocNo, got)
		}
		if got := send[dataOf[debtDocOut]](t, a, http.MethodGet, "/debt-adjustments?doc_no=DN-NONE", nil).Data; len(got) != 0 {
			t.Errorf("unknown DN = %+v", got)
		}
		rec := a.do(http.MethodGet, v1+"/debt-adjustments/"+openingDN.ID.String(), nil)
		expect(t, rec, http.StatusOK, "")
		d := testkit.DecodeJSON[debtDocOut](t, rec)
		if d.Obligation == nil || d.Obligation.ID != st.opening || d.ReasonName == "" {
			t.Errorf("DN detail = %s", rec.Body)
		}
		expect(t, a.do(http.MethodGet, v1+"/debt-adjustments/"+ids.New().String(), nil), http.StatusNotFound, "")
	})

	t.Run("a payment opens with what it settled", func(t *testing.T) {
		rec := a.do(http.MethodGet, v1+"/payments/"+settled.ID.String(), nil)
		expect(t, rec, http.StatusOK, "")
		p := testkit.DecodeJSON[paymentRowOut](t, rec)
		if rec.Header().Get("ETag") == "" || p.MethodName == "" || len(p.Settlements) != 1 ||
			p.Settlements[0].ObligationID != st.opening || p.Settlements[0].ObligationDocNo != openingDN.DocNo {
			t.Errorf("payment detail = %s (ETag %q), want one settlement of %s", rec.Body, rec.Header().Get("ETag"), openingDN.DocNo)
		}
		expect(t, a.do(http.MethodGet, v1+"/payments/"+ids.New().String(), nil), http.StatusNotFound, "")
	})

	t.Run("a BT opens with both documents it settled", func(t *testing.T) {
		obligation := func(direction string) uuid.UUID {
			rec := post(a, "/debt-adjustments", map[string]any{"party_id": st.customer, "direction": direction,
				"kind": debt.AdjustIncrease, "amount": "400000", "reason_id": st.reason})
			expect(t, rec, http.StatusCreated, "")
			return testkit.DecodeJSON[struct {
				Obligation withID `json:"obligation"`
			}](t, rec).Obligation.ID
		}
		receivable, payable := obligation(debt.Receivable), obligation(debt.Payable)
		record, _ := declareAttachment(t, a, debt.AttachmentOwnerParty, st.customer, []byte("%PDF-1.4 bien ban "+ids.New().String()))
		testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
			_, err := q.ExecContext(context.Background(), `UPDATE attachments SET scan_status = 'clean' WHERE id = $1`, record)
			return err
		})
		expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		rec := post(a, "/debt-offsets", map[string]any{"party_id": st.customer, "receivable_obligation_id": receivable,
			"payable_obligation_id": payable, "amount": "400000", "attachment_id": record})
		expect(t, rec, http.StatusCreated, "")
		id := testkit.DecodeJSON[withID](t, rec).ID

		list := send[dataOf[debtDocOut]](t, a, http.MethodGet, "/debt-offsets", nil).Data
		if len(list) != 1 || list[0].ID != id || list[0].PartyName != "Chị Lan" || list[0].ReceivableDocNo == "" || list[0].PayableDocNo == "" {
			t.Fatalf("debt offsets = %+v", list)
		}
		if got := send[dataOf[debtDocOut]](t, a, http.MethodGet, "/debt-offsets?doc_no="+url.QueryEscape(list[0].DocNo), nil).Data; len(got) != 1 {
			t.Errorf("BT %s = %+v", list[0].DocNo, got)
		}
		d := send[debtDocOut](t, a, http.MethodGet, "/debt-offsets/"+id.String(), nil)
		docs := map[string]bool{}
		for _, s := range d.Settlements {
			docs[s.ObligationDocNo] = true
		}
		if len(d.Settlements) != 2 || !docs[list[0].ReceivableDocNo] || !docs[list[0].PayableDocNo] {
			t.Errorf("BT detail = %+v", d)
		}
		expect(t, a.do(http.MethodGet, v1+"/debt-offsets/"+ids.New().String(), nil), http.StatusNotFound, "")
	})

	t.Run("a statement opens with its party and balances, as its list row shows them", func(t *testing.T) {
		rec := post(a, "/statements", map[string]any{"party_id": st.customer, "direction": "receivable",
			"period_from": "2026-03-01", "period_to": "2026-03-15"})
		expect(t, rec, http.StatusCreated, "")
		id := testkit.DecodeJSON[withID](t, rec).ID
		rec = a.do(http.MethodGet, v1+"/statements/"+id.String(), nil)
		expect(t, rec, http.StatusOK, "")
		got := testkit.DecodeJSON[statementRowOut](t, rec)
		if got.PartyName != "Chị Lan" || got.ClosingBalance == "" || got.OpeningBalance == "" ||
			rec.Header().Get("ETag") != `"`+strconv.Itoa(got.Version)+`"` {
			t.Errorf("statement = %s (ETag %q)", rec.Body, rec.Header().Get("ETag"))
		}
		list := send[dataOf[statementRowOut]](t, a, http.MethodGet, "/statements?party_id="+st.customer.String(), nil).Data
		if len(list) != 1 || !reflect.DeepEqual(list[0], got) {
			t.Errorf("statement list = %+v, want %+v", list, got)
		}
		expect(t, a.do(http.MethodGet, v1+"/statements/"+ids.New().String(), nil), http.StatusNotFound, "")
	})
}

type customerRowOut struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	LastPurchaseAt *string   `json:"last_purchase_at"`
	Consents       []struct {
		Channel string `json:"channel"`
		Purpose string `json:"purpose"`
		Granted bool   `json:"granted"`
	} `json:"consents"`
}

// The customer list shows when each customer last bought and what they consented to; the revenue
// report narrows to a set of customers.
func TestCustomerListAndRevenueByCustomer(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	mai := send[withID](t, a, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Chị Mai",
		"phone": "0912345678", "email": "mai@example.com"}).ID
	binh := send[withID](t, a, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Anh Bình"}).ID
	sku := st.newSku(t, a, "MUT-GUNG", "10000")
	st.receive(t, a, sku, nil, "10", "6000")
	creditSale(t, a, mai, saleLine(sku, "2", nil))
	creditSale(t, a, binh, saleLine(sku, "1", nil))
	send[map[string]any](t, a, http.MethodPost, "/customers/"+mai.String()+"/consents", map[string]any{
		"channel": crm.ChannelEmail, "purpose": crm.PurposeDebtReminder, "granted": true, "source": "Tại quầy"})
	lan := st.customer

	t.Run("each row carries its last purchase and consents", func(t *testing.T) {
		rows := map[uuid.UUID]customerRowOut{}
		for _, c := range send[dataOf[customerRowOut]](t, a, http.MethodGet, "/customers", nil).Data {
			rows[c.ID] = c
		}
		if m := rows[mai]; m.LastPurchaseAt == nil || len(m.Consents) != 1 || m.Consents[0].Channel != crm.ChannelEmail ||
			m.Consents[0].Purpose != crm.PurposeDebtReminder || !m.Consents[0].Granted {
			t.Errorf("Chị Mai = %+v", m)
		}
		if l, ok := rows[lan]; !ok || l.LastPurchaseAt != nil || l.Consents == nil || len(l.Consents) != 0 {
			t.Errorf("Chị Lan, who never bought = %+v (listed %v), want no purchase and []", l, ok)
		}
		if b := rows[binh]; b.LastPurchaseAt == nil || len(b.Consents) != 0 {
			t.Errorf("Anh Bình = %+v", b)
		}
	})

	t.Run("revenue narrows to the customers asked for", func(t *testing.T) {
		const period = "revenue?from=2026-03-15&to=2026-03-15"
		wantNum(t, "every customer", report(t, a, period).Totals["net_revenue"], "30000")
		wantNum(t, "Chị Mai", report(t, a, period+"&customer_ids="+mai.String()).Totals["net_revenue"], "20000")
		wantNum(t, "both", report(t, a, period+"&customer_ids="+mai.String()+","+binh.String()).Totals["net_revenue"], "30000")
		wantNum(t, "nobody who bought", report(t, a, period+"&customer_ids="+lan.String()).Totals["net_revenue"], "0")

		many := make([]string, 101)
		for i := range many {
			many[i] = ids.New().String()
		}
		for what, value := range map[string]string{"too_many": strings.Join(many, ","), "invalid": mai.String() + ",nope"} {
			rec := a.do(http.MethodGet, v1+"/reports/"+period+"&customer_ids="+value, nil)
			expect(t, rec, http.StatusUnprocessableEntity, what)
		}
	})
}

type adjustmentRowOut struct {
	ID           uuid.UUID `json:"id"`
	DocNo        string    `json:"doc_no"`
	LineCount    *int      `json:"line_count"`
	TotalBaseQty *string   `json:"total_base_qty"`
	ValueIn      *string   `json:"value_in"`
	ValueOut     *string   `json:"value_out"`
	LinePreview  []struct {
		Name string `json:"name"`
	} `json:"line_preview"`
}

type countRowOut struct {
	ID            uuid.UUID `json:"id"`
	DocNo         string    `json:"doc_no"`
	Blind         bool      `json:"blind"`
	LineCount     *int      `json:"line_count"`
	CountedLines  *int      `json:"counted_lines"`
	VarianceLines *int      `json:"variance_lines"`
}

func intIs(p *int, want int) bool { return p != nil && *p == want }

// The inventory and kit lists carry what their screens summarise, and the kit form and the
// reservation release take what the screens ask for.
func TestInventoryAndKitScreensReadTheirSummaries(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	red := st.colors["do"]

	t.Run("the adjustment list summarises the lines and finds a number", func(t *testing.T) {
		label := st.newSku(t, a, "TEM-DAN", "1000")
		rec := post(a, "/stock-adjustments", map[string]any{"kind": inventory.KindOpening,
			"lines": []map[string]any{{"sku_id": label, "qty": "10", "unit_cost": "500"}}})
		expect(t, rec, http.StatusCreated, "")
		id := testkit.DecodeJSON[withID](t, rec).ID
		var row *adjustmentRowOut
		for _, r := range send[dataOf[adjustmentRowOut]](t, a, http.MethodGet, "/stock-adjustments", nil).Data {
			if r.ID == id {
				row = &r
			}
		}
		if row == nil || !intIs(row.LineCount, 1) || row.TotalBaseQty == nil || !decimal.RequireFromString(*row.TotalBaseQty).Equal(decimal.NewFromInt(10)) ||
			row.ValueIn == nil || *row.ValueIn != "5000" || row.ValueOut == nil || *row.ValueOut != "0" ||
			len(row.LinePreview) != 1 || row.LinePreview[0].Name != "Hàng TEM-DAN" {
			t.Fatalf("adjustment row = %+v", row)
		}
		got := send[dataOf[adjustmentRowOut]](t, a, http.MethodGet, "/stock-adjustments?doc_no="+url.QueryEscape(row.DocNo), nil).Data
		if len(got) != 1 || got[0].ID != id {
			t.Errorf("adjustment %s = %+v", row.DocNo, got)
		}
	})

	t.Run("the count list summarises the sheet and hides the variance of a blind count", func(t *testing.T) {
		start := func(sku uuid.UUID, blind bool) countRowOut {
			return send[countRowOut](t, a, http.MethodPost, "/stock-counts", map[string]any{
				"scope": inventory.ScopeSelection, "sku_ids": []uuid.UUID{sku}, "blind": blind})
		}
		blind, open := start(st.tea, true), start(st.candy, false)
		rows := map[uuid.UUID]countRowOut{}
		for _, r := range send[dataOf[countRowOut]](t, a, http.MethodGet, "/stock-counts", nil).Data {
			rows[r.ID] = r
		}
		if b := rows[blind.ID]; !intIs(b.LineCount, 1) || !intIs(b.CountedLines, 0) || b.VarianceLines != nil {
			t.Errorf("blind count row = %+v, want 1 line, none counted, no variance", b)
		}
		if o := rows[open.ID]; !intIs(o.LineCount, 1) || !intIs(o.CountedLines, 0) || !intIs(o.VarianceLines, 0) {
			t.Errorf("open count row = %+v", o)
		}
		got := send[dataOf[countRowOut]](t, a, http.MethodGet, "/stock-counts?doc_no="+url.QueryEscape(open.DocNo), nil).Data
		if len(got) != 1 || got[0].ID != open.ID {
			t.Errorf("count %s = %+v", open.DocNo, got)
		}
	})

	t.Run("the stock list keeps the SKUs held without a color", func(t *testing.T) {
		listed := map[uuid.UUID]bool{}
		for _, r := range send[dataOf[struct {
			SkuID uuid.UUID `json:"sku_id"`
		}]](t, a, http.MethodGet, "/stock?uncolored=true", nil).Data {
			listed[r.SkuID] = true
		}
		if !listed[st.candy] || !listed[st.tea] || listed[st.ribbon] || listed[st.basket] {
			t.Errorf("uncolored stock = %v; want candy and tea, not the colored ribbon and basket", listed)
		}
		expect(t, a.do(http.MethodGet, v1+"/stock?uncolored=true&colored_only=true", nil), http.StatusUnprocessableEntity,
			"conflicts_with_uncolored")
	})

	t.Run("releasing a manual hold needs a reason, which is kept and audited", func(t *testing.T) {
		r := send[withID](t, a, http.MethodPost, "/reservations", map[string]any{"sku_id": st.ribbon, "qty": "2", "note": "Giữ cho khách"})
		path := v1 + "/reservations/" + r.ID.String() + ":release"
		rec := a.do(http.MethodPost, path, map[string]any{})
		if rec.Code != http.StatusUnprocessableEntity || testkit.DecodeJSON[httpx.ErrorBody](t, rec).Error.Fields["reason"] != "required" {
			t.Errorf("release without a reason = %d %s", rec.Code, rec.Body)
		}
		for _, tc := range []struct{ reason, code string }{{"   ", "required"}, {strings.Repeat("ư", 501), "too_long"}} {
			rec := a.do(http.MethodPost, path, map[string]any{"reason": tc.reason})
			if rec.Code != http.StatusUnprocessableEntity || testkit.DecodeJSON[httpx.ErrorBody](t, rec).Error.Fields["reason"] != tc.code {
				t.Errorf("release with a %d-rune reason = %d %s, want %s", len([]rune(tc.reason)), rec.Code, rec.Body, tc.code)
			}
		}
		expect(t, a.do(http.MethodPost, path, map[string]any{"reason": strings.Repeat("ư", 500)}), http.StatusOK, "")
		r = send[withID](t, a, http.MethodPost, "/reservations", map[string]any{"sku_id": st.ribbon, "qty": "2", "note": "Giữ cho khách"})
		path = v1 + "/reservations/" + r.ID.String() + ":release"
		rec = a.do(http.MethodPost, path, map[string]any{"reason": "Khách huỷ hẹn"})
		expect(t, rec, http.StatusOK, "")
		if got := testkit.DecodeJSON[struct {
			ReleaseReason *string `json:"release_reason"`
		}](t, rec).ReleaseReason; got == nil || *got != "Khách huỷ hẹn" {
			t.Errorf("released reservation = %s", rec.Body)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM audit_events WHERE action = 'reservation.release'
			AND entity_id = $1 AND strpos(after::text, 'Khách huỷ hẹn') > 0`, r.ID); n != 1 {
			t.Errorf("release audit events with the reason = %d, want 1", n)
		}
	})

	t.Run("a kit is created with its wholesale price and minimum stock", func(t *testing.T) {
		body := st.giftBasket("GIO-SI")
		body["wholesale_price"], body["min_stock"] = "180000", "5"
		rec := a.do(http.MethodPost, v1+"/kits", body)
		expect(t, rec, http.StatusCreated, kits.RecipeDraft)
		k := testkit.DecodeJSON[kitOut](t, rec)
		if n := countRows(t, st.tenant, `SELECT count(*) FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
			WHERE l.kind = 'wholesale' AND i.sku_id = $1 AND i.price = 180000`, k.SkuID); n != 1 {
			t.Errorf("wholesale price items = %d, want 1", n)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM skus WHERE id = $1 AND min_stock = 5`, k.SkuID); n != 1 {
			t.Errorf("kit SKU min stock is not 5")
		}

		combo := map[string]any{"type": "combo", "name": "Combo kẹo", "sku_code": "COMBO-KEO", "base_unit_id": st.units["cái"],
			"retail_price": "40000", "min_stock": "2", "recipe": map[string]any{"items": []map[string]any{
				{"kind": "goods", "sku_id": st.candy, "qty": "1"}}}}
		expect(t, a.do(http.MethodPost, v1+"/kits", combo), http.StatusUnprocessableEntity, "not_allowed")
		negative := st.giftBasket("GIO-AM")
		negative["wholesale_price"] = "-1"
		expect(t, a.do(http.MethodPost, v1+"/kits", negative), http.StatusUnprocessableEntity, "must_not_be_negative")
		negative = st.giftBasket("GIO-AM")
		negative["min_stock"] = "-1"
		expect(t, a.do(http.MethodPost, v1+"/kits", negative), http.StatusUnprocessableEntity, "must_not_be_negative")
	})

	t.Run("disassemblies are searched by order number and kit name", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-THAO"))
		order, p := st.assemble(t, a, kit, "1", red)
		rec := a.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+"/disassemblies", map[string]any{
			"kit_lot_id": p.OutputLotID, "qty": "1", "reason_id": st.damageReason, "damaged": []map[string]any{}}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		d := testkit.DecodeJSON[struct {
			ID         uuid.UUID `json:"id"`
			OrderDocNo string    `json:"order_doc_no"`
		}](t, rec)
		search := func(q string) []withID {
			return send[dataOf[withID]](t, a, http.MethodGet, "/disassemblies?q="+url.QueryEscape(q), nil).Data
		}
		for _, q := range []string{d.OrderDocNo, "Giỏ quà Tết GIO-THAO", "gio-thao"} {
			if got := search(q); len(got) != 1 || got[0].ID != d.ID {
				t.Errorf("disassemblies matching %q = %+v", q, got)
			}
		}
		if got := search("không có"); len(got) != 0 {
			t.Errorf("disassemblies matching nothing = %+v", got)
		}
	})
}

// The dashboard splits the period's cash by kind and shows when each top debtor next falls due;
// the season report shows the orders still to deliver and what its customers still owe.
func TestDashboardAndSeasonCards(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	tet := activeCampaign(t, a, "Tết 2026", campaign.KindTet, "2026-03-01", "2026-03-31")
	openCashFund(t, a, "1000000", "2026-03-14")
	openingReceivable(t, a, st.customer, "3000000", "2026-03-01")
	sku := st.newSku(t, a, "BANH-TET", "10000")
	st.receive(t, a, sku, nil, "20", "6000")
	checkout(t, a, st.cashSale("20000", saleLine(sku, "2", nil)))
	sale := creditSale(t, a, st.customer, saleLine(sku, "5", nil))
	collect := func(obligation uuid.UUID, amount string) {
		expect(t, post(a, "/payments", map[string]any{"direction": "in", "party_id": st.customer, "purpose": "settlement",
			"payment_method_id": st.cashMethod, "amount": amount,
			"allocations": []map[string]any{{"obligation_id": obligation, "amount": amount}}}), http.StatusCreated, "")
	}
	collect(openingObligationOf(t, st.tenant, st.customer), "30000")
	collect(obligationOf(t, st.tenant, sale.DocNo), "10000")
	rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(sku, "3", nil)}})
	expect(t, rec, http.StatusCreated, "")
	inv := testkit.DecodeJSON[invoiceOut](t, rec)
	expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK, "")

	t.Run("the dashboard splits cash by kind and dates the next due debt", func(t *testing.T) {
		d := send[struct {
			Cash struct {
				ByCategory []struct {
					Category string `json:"category"`
					Label    string `json:"label"`
					In       string `json:"in"`
					Out      string `json:"out"`
				} `json:"by_category"`
			} `json:"cash"`
			Debt struct {
				Receivable struct {
					Top []struct {
						PartyID uuid.UUID `json:"party_id"`
						NextDue string    `json:"next_due"`
					} `json:"top"`
				} `json:"receivable"`
			} `json:"debt"`
		}](t, a, http.MethodGet, "/reports/dashboard?period=month&date=2026-03-15", nil)
		got := d.Cash.ByCategory
		if len(got) != 2 || got[0].Category != "debt_collection" || got[0].In != "40000" || got[0].Label != "Thu nợ" ||
			got[1].Category != "sales" || got[1].In != "20000" || got[1].Out != "0" {
			t.Errorf("cash by category = %+v, want debt collection 40000 then sales 20000, no opening", got)
		}
		top := d.Debt.Receivable.Top
		if len(top) != 1 || top[0].PartyID != st.customer || top[0].NextDue != "2026-03-01" {
			t.Errorf("top debtors = %+v, want the customer next due 2026-03-01", top)
		}
	})

	seasonMetrics := func(t *testing.T) map[string]*string {
		t.Helper()
		season := send[struct {
			Summary []struct {
				Key   string  `json:"key"`
				Value *string `json:"value"`
			} `json:"summary"`
		}](t, a, http.MethodGet, "/reports/season?campaign_id="+tet.String(), nil)
		metrics := map[string]*string{}
		for _, m := range season.Summary {
			metrics[m.Key] = m.Value
		}
		return metrics
	}
	t.Run("the season report shows its open orders and what its customers still owe", func(t *testing.T) {
		metrics := seasonMetrics(t)
		wantNum(t, "open orders", metrics["open_orders"], "30000")
		wantNum(t, "season receivable", metrics["receivable"], "40000")
	})

	t.Run("a reversed delivery is an open order again and no longer owed", func(t *testing.T) {
		rec := post(a, "/invoices/"+inv.ID.String()+"/stock-outs", map[string]any{})
		expect(t, rec, http.StatusCreated, "")
		so := testkit.DecodeJSON[withID](t, rec)
		delivered := seasonMetrics(t)
		wantNum(t, "open orders after the delivery", delivered["open_orders"], "0")
		wantNum(t, "season receivable after the delivery", delivered["receivable"], "70000")

		expect(t, a.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		expect(t, post(a, "/stock-outs/"+so.ID.String()+":reverse", map[string]any{"reason": "Giao nhầm hàng"}),
			http.StatusCreated, "")
		reversed := seasonMetrics(t)
		wantNum(t, "open orders after the reversal", reversed["open_orders"], "30000")
		wantNum(t, "season receivable after the reversal", reversed["receivable"], "40000")
	})
}

// A purchase order is edited while it is a draft, under its version.
func TestPurchaseOrderDraftEdits(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "VAI-LUA", "50000")
	rec := a.do(http.MethodPost, v1+"/purchase-orders", map[string]any{"supplier_party_id": st.supplier,
		"lines": []map[string]any{{"sku_id": sku, "qty": "4", "unit_price": "30000"}}})
	expect(t, rec, http.StatusCreated, procurement.StatusDraft)
	order := testkit.DecodeJSON[purchaseOrderOut](t, rec)
	path := v1 + "/purchase-orders/" + order.ID.String()
	type draftOut struct {
		Note         *string `json:"note"`
		ExpectedDate *string `json:"expected_date"`
		Version      int     `json:"version"`
		Lines        []struct {
			BaseQty string `json:"base_qty"`
		} `json:"lines"`
	}
	patch := map[string]any{"note": "Giao trước Tết", "expected_date": "2026-03-20"}

	expect(t, a.do(http.MethodPatch, path, patch), http.StatusPreconditionRequired, "")
	expect(t, a.doWith(http.MethodPatch, path, patch, ifMatchOf(order.Version+1)), http.StatusPreconditionFailed, "")
	rec = a.doWith(http.MethodPatch, path, patch, ifMatchOf(order.Version))
	expect(t, rec, http.StatusOK, "")
	got := testkit.DecodeJSON[draftOut](t, rec)
	if got.Note == nil || *got.Note != "Giao trước Tết" || got.ExpectedDate == nil || *got.ExpectedDate != "2026-03-20" ||
		got.Version != order.Version+1 {
		t.Errorf("patched order = %s", rec.Body)
	}

	lines := map[string]any{"lines": []map[string]any{{"sku_id": sku, "qty": "6", "unit_price": "30000"}}}
	expect(t, a.do(http.MethodPut, path+"/lines", lines), http.StatusPreconditionRequired, "")
	rec = a.doWith(http.MethodPut, path+"/lines", lines, ifMatchOf(got.Version))
	expect(t, rec, http.StatusOK, "")
	got = testkit.DecodeJSON[draftOut](t, rec)
	if len(got.Lines) != 1 || !decimal.RequireFromString(got.Lines[0].BaseQty).Equal(decimal.NewFromInt(6)) {
		t.Errorf("replaced lines = %s", rec.Body)
	}

	rec = a.doWith(http.MethodPost, path+":confirm", nil, ifMatchOf(got.Version))
	expect(t, rec, http.StatusOK, procurement.StatusConfirmed)
	confirmed := testkit.DecodeJSON[purchaseOrderOut](t, rec)
	expect(t, a.doWith(http.MethodPatch, path, map[string]any{"note": "Sau khi chốt"}, ifMatchOf(confirmed.Version)),
		http.StatusConflict, "order_not_draft")
	expect(t, a.doWith(http.MethodPut, path+"/lines", lines, ifMatchOf(confirmed.Version)), http.StatusConflict, "order_not_draft")
}

// An assembly order is re-planned while nothing is held for it; once reserved only its note and
// date change.
func TestAssemblyOrderReplanning(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	kit := createKit(t, a, st.giftBasket("GIO-KH"))
	rec := a.do(http.MethodPost, v1+"/assembly-orders", map[string]any{"kit_id": kit.ProductID, "planned_qty": "1",
		"colors": []map[string]any{{"sku_id": st.basket, "color_id": st.colors["do"]}}})
	expect(t, rec, http.StatusCreated, kits.OrderDraft)
	order := testkit.DecodeJSON[assemblyOrderOut](t, rec)
	path := v1 + "/assembly-orders/" + order.ID.String()
	type orderOut struct {
		assemblyOrderOut
		Note        *string `json:"note"`
		PlannedDate *string `json:"planned_date"`
	}
	replan := map[string]any{"planned_qty": "2", "planned_date": "2026-03-20", "note": "Đơn chị Lan"}

	expect(t, a.do(http.MethodPatch, path, replan), http.StatusPreconditionRequired, "")
	expect(t, a.doWith(http.MethodPatch, path, replan, ifMatchOf(order.Version+1)), http.StatusPreconditionFailed, "")
	expect(t, a.doWith(http.MethodPatch, path, map[string]any{"planned_qty": "1.5"}, ifMatchOf(order.Version)),
		http.StatusUnprocessableEntity, "must_be_positive_whole")
	rec = a.doWith(http.MethodPatch, path, replan, ifMatchOf(order.Version))
	expect(t, rec, http.StatusOK, "")
	got := testkit.DecodeJSON[orderOut](t, rec)
	if !decimal.RequireFromString(got.PlannedQty).Equal(decimal.NewFromInt(2)) || got.Note == nil || *got.Note != "Đơn chị Lan" ||
		got.PlannedDate == nil || *got.PlannedDate != "2026-03-20" || len(got.Items) != len(order.Items) {
		t.Fatalf("re-planned order = %s", rec.Body)
	}
	for i, it := range got.Items {
		if want := decimal.RequireFromString(order.Items[i].PlannedQty).Mul(decimal.NewFromInt(2)); !decimal.RequireFromString(it.PlannedQty).Equal(want) {
			t.Errorf("item %s planned %s, want %s", it.SkuID, it.PlannedQty, want)
		}
	}

	rec = a.doWith(http.MethodPost, path+":reserve", nil, keyed(nil))
	expect(t, rec, http.StatusOK, kits.OrderReserved)
	reserved := testkit.DecodeJSON[assemblyOrderOut](t, rec)
	expect(t, a.doWith(http.MethodPatch, path, map[string]any{"planned_qty": "3"}, ifMatchOf(reserved.Version)),
		http.StatusConflict, "assembly_order_status_invalid")
	rec = a.doWith(http.MethodPatch, path, map[string]any{"note": "Giao sáng 20/03"}, ifMatchOf(reserved.Version))
	expect(t, rec, http.StatusOK, "")
	if got := testkit.DecodeJSON[orderOut](t, rec); got.Note == nil || *got.Note != "Giao sáng 20/03" ||
		!decimal.RequireFromString(got.PlannedQty).Equal(decimal.NewFromInt(2)) {
		t.Errorf("reserved order after a note = %s", rec.Body)
	}
}
