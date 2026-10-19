//go:build integration

package app_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// The subscription lists an add-on once its invoice is paid, for the days of its period in the
// store's time zone: not while the invoice is open, and not from the day after its period.
func TestSubscriptionListsThePaidAddonsOfTheBusinessDay(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	tenant := tenantOf(t, b)
	if rec := b.do(http.MethodGet, v1+"/subscription", nil); jsonOf(t, rec)["addons"] == nil {
		t.Fatalf("subscription without add-ons = %s, want an empty addons list", rec.Body)
	}
	if got := subscriptionOf(t, b).Addons; len(got) != 0 {
		t.Fatalf("add-ons of a new store = %+v, want none", got)
	}

	adm, _ := s.adminSignIn(t)
	plan := adminSend[saas.PlanDTO](t, adm, http.MethodPost, "/plans", map[string]any{
		"code": "addon_" + unique(), "name": "Hỏi đáp và báo cáo", "kind": saas.KindAddon, "billing_period": saas.PeriodMonth,
		"price": "100000", "modules": []string{saas.ModuleReportsAdvanced, saas.ModuleAIQA}, "limits": map[string]int64{},
	})
	rec := b.do(http.MethodPost, v1+"/subscription:change", map[string]any{"plan_id": plan.ID, "accept_terms": true})
	expect(t, rec, http.StatusCreated, "")
	inv := jsonOf(t, rec)
	if inv["purpose"] != saas.PurposeAddon || inv["status"] != "open" {
		t.Fatalf("add-on invoice = %s, want an open add-on invoice", rec.Body)
	}
	if got := subscriptionOf(t, b).Addons; len(got) != 0 {
		t.Fatalf("add-ons with the invoice unpaid = %+v, want none", got)
	}

	paid := adminSend[saas.InvoiceDTO](t, adm, http.MethodPost, "/saas-invoices/"+inv["id"].(string)+":mark-paid",
		map[string]string{"reference": "CK-" + unique()})
	if paid.Status != saas.InvoicePaid {
		t.Fatalf("invoice after mark-paid = %+v", paid)
	}
	want := []saas.ActiveAddonDTO{{
		PlanID: plan.ID, PlanName: plan.Name, Modules: []string{saas.ModuleAIQA, saas.ModuleReportsAdvanced},
		PeriodStart: paid.PeriodStart, PeriodEnd: paid.PeriodEnd,
	}}
	if got := subscriptionOf(t, b).Addons; !reflect.DeepEqual(got, want) {
		t.Fatalf("add-ons after payment = %+v, want %+v", got, want)
	}
	if n := count(t, `SELECT count(*) FROM tally.entitlements WHERE tenant_id = $1 AND source_plan_id = $2`,
		tenant, plan.ID); n != 2 {
		t.Errorf("entitlement rows naming the add-on = %d, want 2", n)
	}
	// The trial's own row of a module the add-on also enables, starting the same day, keeps no end.
	if n := count(t, `SELECT count(*) FROM tally.entitlements WHERE tenant_id = $1 AND key = 'module:reports_advanced'
		AND source_subscription_id IS NOT NULL AND valid_to IS NULL AND source_plan_id IS NULL`, tenant); n != 1 {
		t.Errorf("open trial rows of the shared module = %d, want 1", n)
	}

	// The last day of the period ends at midnight in Asia/Ho_Chi_Minh, while the UTC date is
	// still that day.
	end, err := time.Parse(time.DateOnly, paid.PeriodEnd)
	if err != nil {
		t.Fatal(err)
	}
	zone, err := clock.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	lastEvening := time.Date(end.Year(), end.Month(), end.Day(), 23, 30, 0, 0, zone)
	s.clk.Set(lastEvening)
	b = s.signIn(t, o)
	if got := subscriptionOf(t, b).Addons; !reflect.DeepEqual(got, want) {
		t.Fatalf("add-ons on the last day of the period = %+v, want %+v", got, want)
	}
	s.clk.Set(lastEvening.Add(time.Hour))
	if utc := s.clk.Now().UTC(); utc.Day() != end.Day() {
		t.Fatalf("the hour after the period is %s in UTC, want the period's last day", utc)
	}
	if got := subscriptionOf(t, b).Addons; len(got) != 0 {
		t.Fatalf("add-ons after the period = %+v, want none", got)
	}
}

// Two add-on plans enabling the same module, bought for periods starting the same day, are listed
// apart, while the module they share is enabled once.
func TestAddonsSharingAModuleOnOneDayAreListedApart(t *testing.T) {
	s := newControlStack(t)
	b := s.signIn(t, s.signUp(t))
	tenant := tenantOf(t, b)
	adm, _ := s.adminSignIn(t)
	plan := adminSend[saas.PlanDTO](t, adm, http.MethodPost, "/plans", map[string]any{
		"code": "addon_" + unique(), "name": "Hỏi đáp mở rộng", "kind": saas.KindAddon, "billing_period": saas.PeriodMonth,
		"price": "0", "modules": []string{saas.ModuleAIQA}, "limits": map[string]int64{},
	})
	for _, id := range []string{addonAIQAPlanID, plan.ID.String()} {
		rec := b.do(http.MethodPost, v1+"/subscription:change", map[string]any{"plan_id": id, "accept_terms": true})
		expect(t, rec, http.StatusCreated, "")
		if inv := jsonOf(t, rec); inv["status"] != saas.InvoicePaid {
			t.Fatalf("free add-on invoice = %s, want it paid", rec.Body)
		}
	}

	got := subscriptionOf(t, b).Addons
	if len(got) != 2 {
		t.Fatalf("add-ons = %+v, want both", got)
	}
	plans := map[string]bool{}
	for _, a := range got {
		plans[a.PlanID.String()] = true
		if !reflect.DeepEqual(a.Modules, []string{saas.ModuleAIQA}) || a.PeriodStart != got[0].PeriodStart {
			t.Errorf("add-on %+v, want the AI Q&A from %s", a, got[0].PeriodStart)
		}
	}
	if !plans[addonAIQAPlanID] || !plans[plan.ID.String()] {
		t.Errorf("listed add-on plans = %v, want %s and %s", plans, addonAIQAPlanID, plan.ID)
	}
	if n := count(t, `SELECT count(*) FROM tally.entitlements WHERE tenant_id = $1 AND key = 'module:ai_qa'`, tenant); n != 2 {
		t.Errorf("AI Q&A entitlement rows = %d, want one per add-on", n)
	}
	rec := b.do(http.MethodGet, v1+"/entitlements", nil)
	expect(t, rec, http.StatusOK, "")
	if modules, _ := jsonOf(t, rec)["modules"].(map[string]any); modules[saas.ModuleAIQA] != true {
		t.Errorf("entitlements = %s, want the AI Q&A on", rec.Body)
	}
}

// Buying an add-on again extends it: the new period starts the day after the live one ends,
// whether bought the same day or in the middle of the period, and on the payment day once the
// add-on has lapsed. The preview quotes the days the invoice then carries, and the periods of the
// add-on never overlap.
func TestBuyingALiveAddonAgainExtendsIt(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	tenant := tenantOf(t, b)
	adm, _ := s.adminSignIn(t)
	plan := adminSend[saas.PlanDTO](t, adm, http.MethodPost, "/plans", map[string]any{
		"code": "addon_" + unique(), "name": "Hỏi đáp theo tháng", "kind": saas.KindAddon, "billing_period": saas.PeriodMonth,
		"price": "100000", "modules": []string{saas.ModuleAIQA}, "limits": map[string]int64{},
	})
	zone, err := clock.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	today := func() string { return s.clk.Now().In(zone).Format(time.DateOnly) }
	dayAfter := func(date string) string {
		t.Helper()
		d, err := time.Parse(time.DateOnly, date)
		if err != nil {
			t.Fatal(err)
		}
		return d.AddDate(0, 0, 1).Format(time.DateOnly)
	}
	// moveTo sets the clock to 10:00 of a business day; the sessions are signed in again.
	moveTo := func(date string) {
		t.Helper()
		d, err := time.ParseInLocation(time.DateOnly, date, zone)
		if err != nil {
			t.Fatal(err)
		}
		s.clk.Set(d.Add(10 * time.Hour))
		b = s.signIn(t, o)
		adm, _ = s.adminSignIn(t)
	}
	body := map[string]any{"plan_id": plan.ID, "accept_terms": true}
	buy := func(wantStart string) saas.InvoiceDTO {
		t.Helper()
		quote := send[struct {
			Start string `json:"period_start"`
			End   string `json:"period_end"`
		}](t, b, http.MethodPost, "/subscription:preview-change", body)
		if quote.Start != wantStart {
			t.Fatalf("quoted add-on period %s..%s, want it from %s", quote.Start, quote.End, wantStart)
		}
		inv := send[saas.InvoiceDTO](t, b, http.MethodPost, "/subscription:change", body)
		if inv.Status != saas.InvoiceOpen || inv.PeriodStart != quote.Start || inv.PeriodEnd != quote.End {
			t.Fatalf("add-on invoice = %+v, want open for the quoted %s..%s", inv, quote.Start, quote.End)
		}
		return inv
	}
	pay := func(inv saas.InvoiceDTO, wantStart string) saas.InvoiceDTO {
		t.Helper()
		paid := adminSend[saas.InvoiceDTO](t, adm, http.MethodPost, "/saas-invoices/"+inv.ID.String()+":mark-paid",
			map[string]string{"reference": "CK-" + unique()})
		if paid.Status != saas.InvoicePaid || paid.PeriodStart != wantStart {
			t.Fatalf("paid add-on invoice = %+v, want its period from %s", paid, wantStart)
		}
		return paid
	}

	opened := today()
	first := pay(buy(opened), opened)
	second := buy(dayAfter(first.PeriodEnd))
	second = pay(second, second.PeriodStart)
	want := []saas.ActiveAddonDTO{{PlanID: plan.ID, PlanName: plan.Name, Modules: []string{saas.ModuleAIQA},
		PeriodStart: first.PeriodStart, PeriodEnd: first.PeriodEnd}}
	if got := subscriptionOf(t, b).Addons; !reflect.DeepEqual(got, want) {
		t.Fatalf("add-ons after buying twice in a day = %+v, want only the live period %+v", got, want)
	}

	// In the middle of the live period a purchase follows the last period bought.
	moveTo(dayAfter(dayAfter(opened)))
	third := buy(dayAfter(second.PeriodEnd))
	third = pay(third, third.PeriodStart)

	// An invoice issued on the last day starts the next day; paid after the add-on lapsed, it
	// starts on the payment day, as a purchase on that day is quoted.
	moveTo(third.PeriodEnd)
	late := buy(dayAfter(third.PeriodEnd))
	moveTo(dayAfter(dayAfter(dayAfter(third.PeriodEnd))))
	if got := subscriptionOf(t, b).Addons; len(got) != 0 {
		t.Fatalf("add-ons after the last period = %+v, want none", got)
	}
	quote := send[struct {
		Start string `json:"period_start"`
	}](t, b, http.MethodPost, "/subscription:preview-change", body)
	if quote.Start != today() {
		t.Errorf("quoted lapsed add-on from %s, want today %s", quote.Start, today())
	}
	pay(late, today())

	if n := count(t, `SELECT count(*) FROM tally.entitlements WHERE tenant_id = $1 AND source_plan_id = $2`,
		tenant, plan.ID); n != 4 {
		t.Errorf("add-on periods = %d, want 4", n)
	}
	if n := count(t, `SELECT count(*) FROM tally.entitlements a JOIN tally.entitlements z ON z.tenant_id = a.tenant_id
		AND z.key = a.key AND z.source_plan_id = a.source_plan_id AND z.id <> a.id
		AND z.valid_from <= a.valid_to AND a.valid_from <= z.valid_to
		WHERE a.tenant_id = $1 AND a.source_plan_id = $2`, tenant, plan.ID); n != 0 {
		t.Errorf("overlapping add-on periods = %d, want none", n)
	}
}

// Buying an add-on voids only the open invoice of the same add-on: the open invoice of another
// add-on stays payable.
func TestBuyingAnAddonLeavesTheOpenInvoiceOfAnotherAddon(t *testing.T) {
	s := newControlStack(t)
	b := s.signIn(t, s.signUp(t))
	adm, _ := s.adminSignIn(t)
	addon := func(name, module string) saas.PlanDTO {
		return adminSend[saas.PlanDTO](t, adm, http.MethodPost, "/plans", map[string]any{
			"code": "addon_" + unique(), "name": name, "kind": saas.KindAddon, "billing_period": saas.PeriodMonth,
			"price": "100000", "modules": []string{module}, "limits": map[string]int64{},
		})
	}
	a, c := addon("Hỏi đáp", saas.ModuleAIQA), addon("Báo cáo nâng cao", saas.ModuleReportsAdvanced)
	buy := func(p saas.PlanDTO) saas.InvoiceDTO {
		t.Helper()
		return send[saas.InvoiceDTO](t, b, http.MethodPost, "/subscription:change", map[string]any{"plan_id": p.ID, "accept_terms": true})
	}
	status := func(inv saas.InvoiceDTO) string {
		t.Helper()
		var v string
		if err := testkit.SuperDB(t).Get(&v, `SELECT status FROM tally.saas_invoices WHERE id = $1`, inv.ID); err != nil {
			t.Fatal(err)
		}
		return v
	}

	ofA, firstOfC := buy(a), buy(c)
	if got := status(ofA); got != saas.InvoiceOpen {
		t.Fatalf("invoice of the first add-on after buying another = %s, want open", got)
	}
	secondOfC := buy(c)
	if got := status(firstOfC); got != saas.InvoiceVoid {
		t.Errorf("earlier invoice of the add-on bought again = %s, want void", got)
	}
	if got := status(ofA); got != saas.InvoiceOpen {
		t.Fatalf("invoice of the other add-on = %s, want open", got)
	}
	for _, inv := range []saas.InvoiceDTO{ofA, secondOfC} {
		paid := adminSend[saas.InvoiceDTO](t, adm, http.MethodPost, "/saas-invoices/"+inv.ID.String()+":mark-paid",
			map[string]string{"reference": "CK-" + unique()})
		if paid.Status != saas.InvoicePaid {
			t.Fatalf("invoice %s after mark-paid = %+v", inv.Number, paid)
		}
	}
	plans := map[uuid.UUID]bool{}
	for _, ad := range subscriptionOf(t, b).Addons {
		plans[ad.PlanID] = true
	}
	if !plans[a.ID] || !plans[c.ID] || len(plans) != 2 {
		t.Errorf("add-ons = %v, want %s and %s", plans, a.ID, c.ID)
	}
}
