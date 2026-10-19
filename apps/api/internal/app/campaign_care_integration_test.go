//go:build integration

package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// closeCampaign closes a campaign at its current version.
func closeCampaign(t *testing.T, b *browser, id uuid.UUID) closeOut {
	t.Helper()
	c := send[campaignOut](t, b, http.MethodGet, "/campaigns/"+id.String(), nil)
	rec := b.doWith(http.MethodPost, v1+"/campaigns/"+id.String()+":close", nil, ifMatchOf(c.Version))
	expect(t, rec, http.StatusOK, campaign.StatusClosed)
	return testkit.DecodeJSON[closeOut](t, rec)
}

type closeOut struct {
	Campaign  campaignOut `json:"campaign"`
	Checklist struct {
		Total int `json:"total"`
		Done  int `json:"done"`
		Open  []struct {
			Title string `json:"title"`
		} `json:"open"`
	} `json:"checklist"`
}

// openCashFund gives the cash fund an opening balance on date, before any entry.
func openCashFund(t *testing.T, b *browser, amount, date string) {
	t.Helper()
	for _, f := range send[dataOf[fundOut]](t, b, http.MethodGet, "/funds", nil).Data {
		if f.Kind == cash.FundCash {
			expect(t, b.doWith(http.MethodPatch, v1+"/funds/"+f.ID.String(),
				map[string]any{"opening_balance": amount, "opening_date": date}, ifMatchOf(f.Version)), http.StatusOK, "")
			return
		}
	}
	t.Fatal("no cash fund")
}

// openingReceivable records what party owed before the store started on Tally.
func openingReceivable(t *testing.T, b *browser, party uuid.UUID, amount, due string) {
	t.Helper()
	reason := send[dataOf[withID]](t, b, http.MethodGet, "/reasons?kind=debt_adjustment", nil).Data[0].ID
	expect(t, post(b, "/debt-adjustments", map[string]any{
		"party_id": party, "direction": "receivable", "kind": "opening_balance", "amount": amount,
		"reason_id": reason, "opening_cutoff_date": "2026-03-14", "due_date": due}), http.StatusCreated, "")
}

type checklistItemOut struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
	Done  bool      `json:"done"`
}

type demandOut struct {
	Data []struct {
		SkuID           uuid.UUID `json:"sku_id"`
		Available       string    `json:"available"`
		FirstShortageOn *string   `json:"first_shortage_on"`
		MaxShortage     string    `json:"max_shortage"`
		Days            []struct {
			Date      string `json:"date"`
			Demand    string `json:"demand"`
			Incoming  string `json:"incoming"`
			Projected string `json:"projected"`
			Shortage  string `json:"shortage"`
		} `json:"days"`
	} `json:"data"`
}

func TestCampaignChecklistDemandAndClose(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	tet := send[campaignOut](t, a, http.MethodPost, "/campaigns", map[string]any{
		"name": "Tết 2026", "kind": campaign.KindTet, "starts_on": "2026-03-15", "ends_on": "2026-03-31"})
	if tet.Status != campaign.StatusPlanning {
		t.Fatalf("new campaign = %+v", tet)
	}
	path := "/campaigns/" + tet.ID.String()

	seeded := send[dataOf[checklistItemOut]](t, a, http.MethodGet, path+"/checklist", nil).Data
	if len(seeded) == 0 {
		t.Fatal("a new campaign starts without its checklist")
	}
	items := send[dataOf[checklistItemOut]](t, a, http.MethodPatch, path+"/checklist", map[string]any{
		"items": []map[string]any{{"id": seeded[0].ID, "done": true}},
		"add":   []map[string]any{{"phase": campaign.PhaseClosing, "title": "Gọi lại khách mua giỏ quà"}}}).Data
	if len(items) != len(seeded)+1 {
		t.Fatalf("checklist after the change = %+v", items)
	}

	tet = send[campaignOut](t, a, http.MethodGet, path, nil)
	expect(t, a.doWith(http.MethodPost, v1+path+":activate", nil, ifMatchOf(tet.Version)), http.StatusOK, campaign.StatusActive)

	t.Run("demand shows the shortage until the pending purchase arrives", func(t *testing.T) {
		sku := st.newSku(t, a, "MUT-GUNG", "30000")
		st.receive(t, a, sku, nil, "3", "20000")
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(sku, "8", nil)}})
		expect(t, rec, http.StatusCreated, "")
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version))), http.StatusOK, "")
		confirmedOrder(t, a, map[string]any{"supplier_party_id": st.supplier, "expected_date": "2026-03-20",
			"lines": []map[string]any{{"sku_id": sku, "unit_id": st.units["cái"], "qty": "10", "unit_price": "20000"}}})

		d := send[demandOut](t, a, http.MethodGet, path+"/demand", nil)
		if len(d.Data) != 1 || d.Data[0].SkuID != sku {
			t.Fatalf("demand = %+v", d.Data)
		}
		row := d.Data[0]
		if row.Available != "3" || row.FirstShortageOn == nil || *row.FirstShortageOn != "2026-03-15" || row.MaxShortage != "5" {
			t.Errorf("demand row = %+v, want 3 available and a shortage of 5 from 15/03", row)
		}
		want := [][5]string{{"2026-03-15", "8", "0", "-5", "5"}, {"2026-03-20", "0", "10", "5", "0"}}
		if len(row.Days) != len(want) {
			t.Fatalf("demand days = %+v", row.Days)
		}
		for i, w := range want {
			d := row.Days[i]
			if got := [5]string{d.Date, d.Demand, d.Incoming, d.Projected, d.Shortage}; got != w {
				t.Errorf("day %d = %v, want %v", i, got, w)
			}
		}
	})

	closed := closeCampaign(t, a, tet.ID)
	if closed.Checklist.Total != len(seeded)+1 || closed.Checklist.Done != 1 || len(closed.Checklist.Open) != len(seeded) {
		t.Errorf("close summary = %+v", closed.Checklist)
	}
	// The closing reminders can still be ticked after the close; the campaign itself is final.
	after := send[dataOf[checklistItemOut]](t, a, http.MethodPatch, path+"/checklist",
		map[string]any{"items": []map[string]any{{"id": seeded[len(seeded)-1].ID, "done": true}}}).Data
	if !after[len(seeded)-1].Done {
		t.Errorf("checklist after the close = %+v", after)
	}
	expect(t, a.doWith(http.MethodPatch, v1+path, map[string]any{"name": "Tết 2026 (sửa)"}, ifMatchOf(closed.Campaign.Version)),
		http.StatusConflict, campaign.CodeClosed)
}

type activityOut struct {
	Kind  string     `json:"kind"`
	RefID *uuid.UUID `json:"ref_id"`
}

type careTaskOut struct {
	ID     uuid.UUID  `json:"id"`
	Status string     `json:"status"`
	DoneAt *string    `json:"done_at"`
	Source *uuid.UUID `json:"source_campaign_id"`
}

type reorderOut struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	TotalAmount string    `json:"total_amount"`
	Lines       []struct {
		SkuID  *uuid.UUID `json:"sku_id"`
		Qty    string     `json:"qty"`
		Copied bool       `json:"copied"`
		Flag   *string    `json:"flag"`
	} `json:"lines"`
}

func TestCustomerCareFromTimelineToReorder(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	tet := activeCampaign(t, a, "Tết 2026", campaign.KindTet, "2026-03-01", "2026-03-31")
	kept, dropped := st.newSku(t, a, "HAT-DUA", "10000"), st.newSku(t, a, "KEO-LAC", "20000")
	st.receive(t, a, kept, nil, "10", "6000")
	st.receive(t, a, dropped, nil, "1", "12000")
	sale := creditSale(t, a, st.customer, saleLine(kept, "2", nil), saleLine(dropped, "1", nil))
	customer := "/customers/" + st.customer.String()

	s.drainOutbox(t, st.tenant)
	expect(t, a.do(http.MethodPost, v1+customer+"/activities", map[string]any{"kind": crm.ActivityCareCall, "summary": "Hẹn mua lại trước Tết"}),
		http.StatusCreated, crm.ActivityCareCall)
	timeline := send[dataOf[activityOut]](t, a, http.MethodGet, customer+"/timeline", nil).Data
	var purchases, calls int
	for _, e := range timeline {
		switch e.Kind {
		case crm.ActivityPurchase:
			if e.RefID == nil || *e.RefID != sale.ID {
				t.Errorf("purchase entry = %+v, want it to point at the sale", e)
			}
			purchases++
		case crm.ActivityCareCall:
			calls++
		}
	}
	if purchases != 1 || calls != 1 {
		t.Errorf("timeline = %+v, want one purchase and one care call", timeline)
	}
	s.drainOutbox(t, st.tenant)
	if again := send[dataOf[activityOut]](t, a, http.MethodGet, customer+"/timeline", nil).Data; len(again) != len(timeline) {
		t.Errorf("a second drain changed the timeline: %+v", again)
	}

	expect(t, a.do(http.MethodGet, v1+"/customers/last-season", nil), http.StatusUnprocessableEntity, crm.CodeNoClosedSeason)
	closeCampaign(t, a, tet)
	task := send[careTaskOut](t, a, http.MethodPost, "/care-tasks", map[string]any{
		"party_id": st.customer, "due_on": "2026-03-20", "title": "Gọi chị Lan", "source_campaign_id": tet})
	if task.Status != crm.TaskOpen || task.Source == nil || *task.Source != tet {
		t.Errorf("care task = %+v", task)
	}
	type lastSeasonOut struct {
		CampaignID uuid.UUID `json:"campaign_id"`
		Data       []struct {
			PartyID    uuid.UUID `json:"party_id"`
			Documents  int       `json:"documents"`
			NetRevenue string    `json:"net_revenue"`
			OpenTasks  int       `json:"open_tasks"`
		} `json:"data"`
	}
	last := send[lastSeasonOut](t, a, http.MethodGet, "/customers/last-season", nil)
	if last.CampaignID != tet || len(last.Data) != 1 || last.Data[0].PartyID != st.customer || last.Data[0].Documents != 1 ||
		last.Data[0].NetRevenue != "40000" || last.Data[0].OpenTasks != 1 {
		t.Errorf("last season = %+v, want the customer with 40000 and one open task", last)
	}
	done := send[careTaskOut](t, a, http.MethodPatch, "/care-tasks/"+task.ID.String(), map[string]any{"status": crm.TaskDone})
	if done.Status != crm.TaskDone || done.DoneAt == nil {
		t.Errorf("finished task = %+v", done)
	}
	if open := send[dataOf[careTaskOut]](t, a, http.MethodGet, "/care-tasks?status=open", nil).Data; len(open) != 0 {
		t.Errorf("open tasks = %+v", open)
	}

	// The candy is no longer sold: the re-order copies the seeds alone and flags the candy.
	sku := send[struct {
		Version int `json:"version"`
	}](t, a, http.MethodGet, "/skus/"+dropped.String(), nil)
	expect(t, a.doWith(http.MethodPatch, v1+"/skus/"+dropped.String(), map[string]any{"status": "inactive"}, ifMatchOf(sku.Version)),
		http.StatusOK, "inactive")
	rec := a.do(http.MethodPost, v1+customer+"/reorder-drafts", nil)
	expect(t, rec, http.StatusCreated, "")
	draft := testkit.DecodeJSON[reorderOut](t, rec)
	if draft.TotalAmount != "20000" || len(draft.Lines) != 2 {
		t.Fatalf("re-order draft = %+v", draft)
	}
	for _, l := range draft.Lines {
		switch *l.SkuID {
		case kept:
			if !l.Copied || l.Qty != "2" || l.Flag != nil {
				t.Errorf("kept line = %+v", l)
			}
		case dropped:
			if l.Copied || l.Flag == nil || *l.Flag != crm.FlagDiscontinued {
				t.Errorf("dropped line = %+v", l)
			}
		}
	}
	inv := send[struct {
		Status string `json:"status"`
	}](t, a, http.MethodGet, "/invoices/"+draft.InvoiceID.String(), nil)
	if inv.Status != "draft" {
		t.Errorf("re-order invoice = %+v, want a draft", inv)
	}
}

type savedReportOut struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Config struct {
		Columns []string `json:"columns"`
		GroupBy string   `json:"group_by"`
	} `json:"config"`
}

func TestSavedReportsAndSuggestions(t *testing.T) {
	s := newReportingStack(t, nil)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	st := buildSalesStore(t, a)

	t.Run("a saved layout is checked against its report and stays in its store", func(t *testing.T) {
		expect(t, a.do(http.MethodPost, v1+"/saved-reports", map[string]any{"report_key": "revenue", "name": "Theo chứng từ",
			"config": map[string]any{"columns": []string{"code"}, "filters": map[string]string{"color": "do"}}}),
			http.StatusUnprocessableEntity, "config.filters.color")
		saved := send[savedReportOut](t, a, http.MethodPost, "/saved-reports", map[string]any{"report_key": "revenue",
			"name": "Theo chứng từ", "config": map[string]any{"columns": []string{"code", "net_revenue"}, "group_by": "document"}})
		if saved.Config.GroupBy != "document" || len(saved.Config.Columns) != 2 {
			t.Errorf("saved report = %+v", saved)
		}
		renamed := send[savedReportOut](t, a, http.MethodPatch, "/saved-reports/"+saved.ID.String(), map[string]any{"name": "Doanh thu BH"})
		if renamed.Name != "Doanh thu BH" || renamed.Config.GroupBy != "document" {
			t.Errorf("renamed report = %+v", renamed)
		}
		if list := send[dataOf[savedReportOut]](t, b, http.MethodGet, "/saved-reports", nil).Data; len(list) != 0 {
			t.Errorf("store B sees store A's layouts: %+v", list)
		}
		expect(t, b.do(http.MethodDelete, v1+"/saved-reports/"+saved.ID.String(), nil), http.StatusNotFound, "")
		expect(t, a.do(http.MethodDelete, v1+"/saved-reports/"+saved.ID.String(), nil), http.StatusNoContent, "")
		if list := send[dataOf[savedReportOut]](t, a, http.MethodGet, "/saved-reports", nil).Data; len(list) != 0 {
			t.Errorf("layouts after the delete = %+v", list)
		}
	})

	t.Run("buy and clearance suggestions follow sales velocity", func(t *testing.T) {
		fast, idle := st.newSku(t, a, "BIA-LON", "15000"), st.newSku(t, a, "RUOU-VANG", "250000")
		st.receive(t, a, fast, nil, "15", "10000")
		st.receive(t, a, idle, nil, "4", "180000")
		checkout(t, a, st.cashSale("150000", saleLine(fast, "10", nil)))
		confirmedOrder(t, a, map[string]any{"supplier_party_id": st.supplier, "expected_date": "2026-03-25",
			"lines": []map[string]any{{"sku_id": fast, "unit_id": st.units["cái"], "qty": "2", "unit_price": "10000"}}})

		buy := report(t, a, "suggestions/buy?cover_days=10&lookback_days=10")
		row := buy.rowOf(t, "BIA-LON")
		// 10 sold over 10 days covers 10 days with 10; 5 are on hand and 2 on order.
		wantNum(t, "average per day", row["avg_daily"], "1")
		wantNum(t, "available", row["available"], "5")
		wantNum(t, "on order", row["pending_po"], "2")
		wantNum(t, "suggested", row["suggested_qty"], "3")
		for _, r := range buy.Rows {
			if *r["code"] == "RUOU-VANG" {
				t.Errorf("an unsold item without a minimum is suggested for buying: %v", r)
			}
		}
		clearance := report(t, a, "suggestions/clearance?lookback_days=10")
		if reason := clearance.rowOf(t, "RUOU-VANG")["reason"]; reason == nil || !strings.Contains(*reason, "Không bán được trong 10 ngày") {
			t.Errorf("clearance reason = %v", reason)
		}
	})

	t.Run("cash forecast adds receivables falling due to the fund balance", func(t *testing.T) {
		fresh := buildSalesStore(t, b)
		openCashFund(t, b, "1000000", "2026-03-14")
		openingReceivable(t, b, fresh.customer, "3000000", "2026-03-20")
		sku := fresh.newSku(t, b, "NUOC-SUOI", "20000")
		fresh.receive(t, b, sku, nil, "5", "5000")
		checkout(t, b, fresh.cashSale("20000", saleLine(sku, "1", nil)))

		f := report(t, b, "cash-forecast?from=2026-03-15&to=2026-03-25")
		wantNum(t, "receivable due", f.Totals["receivable_due"], "3000000")
		var due map[string]*string
		for _, r := range f.Rows {
			if r["date"] != nil && *r["date"] == "2026-03-20" {
				due = r
			}
		}
		if due == nil {
			t.Fatalf("forecast rows = %+v, want 20/03", f.Rows)
		}
		wantNum(t, "due 20/03", due["receivable_due"], "3000000")
		wantNum(t, "projected 20/03", due["projected_balance"], "4020000")
	})
}
