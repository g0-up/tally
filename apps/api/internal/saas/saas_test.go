package saas

import (
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func dayPtr(s string) *time.Time {
	t := day(s)
	return &t
}

func optDay(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.DateOnly)
}

func TestSubscriptionLifecycleTransitionTable(t *testing.T) {
	plan := Plan{BillingPeriod: PeriodMonth, Limits: Limits{TermGraceDays: 7, TermReadOnlyDays: 60}}
	tests := []struct {
		name       string
		sub        Subscription
		today      string
		wantStatus string
		wantPath   []string
		wantOn     []string
		wantGrace  string
		wantRO     string
	}{
		{
			name:       "trial still running on its last day",
			sub:        Subscription{Status: StatusTrialing, CurrentPeriodEnd: day("2026-10-31"), TrialEndsOn: dayPtr("2026-10-31")},
			today:      "2026-10-31",
			wantStatus: StatusTrialing,
		},
		{
			name:       "trial ended enters grace the next day",
			sub:        Subscription{Status: StatusTrialing, CurrentPeriodEnd: day("2026-10-31"), TrialEndsOn: dayPtr("2026-10-31")},
			today:      "2026-11-01",
			wantStatus: StatusGrace,
			wantPath:   []string{StatusTrialing + ">" + StatusGrace},
			wantOn:     []string{"2026-11-01"},
			wantGrace:  "2026-11-07",
		},
		{
			name:       "active period end passed enters grace",
			sub:        Subscription{Status: StatusActive, CurrentPeriodEnd: day("2026-10-31")},
			today:      "2026-11-03",
			wantStatus: StatusGrace,
			wantPath:   []string{StatusActive + ">" + StatusGrace},
			wantOn:     []string{"2026-11-01"},
			wantGrace:  "2026-11-07",
		},
		{
			name:       "cancelled active skips grace",
			sub:        Subscription{Status: StatusActive, CurrentPeriodEnd: day("2026-10-31"), CancelAtPeriodEnd: true},
			today:      "2026-11-01",
			wantStatus: StatusReadOnly,
			wantPath:   []string{StatusActive + ">" + StatusReadOnly},
			wantOn:     []string{"2026-11-01"},
			wantRO:     "2026-12-30",
		},
		{
			name:       "grace end passed becomes read-only",
			sub:        Subscription{Status: StatusGrace, CurrentPeriodEnd: day("2026-10-31"), GraceEndsOn: dayPtr("2026-11-07")},
			today:      "2026-11-08",
			wantStatus: StatusReadOnly,
			wantPath:   []string{StatusGrace + ">" + StatusReadOnly},
			wantOn:     []string{"2026-11-08"},
			wantGrace:  "2026-11-07",
			wantRO:     "2027-01-06",
		},
		{
			name:       "read-only end passed closes",
			sub:        Subscription{Status: StatusReadOnly, CurrentPeriodEnd: day("2026-10-31"), ReadOnlyEndsOn: dayPtr("2027-01-06")},
			today:      "2027-01-07",
			wantStatus: StatusClosed,
			wantPath:   []string{StatusReadOnly + ">" + StatusClosed},
			wantOn:     []string{"2027-01-07"},
			wantRO:     "2027-01-06",
		},
		{
			name:       "a long lapse walks every state in one pass",
			sub:        Subscription{Status: StatusActive, CurrentPeriodEnd: day("2026-10-31")},
			today:      "2027-06-01",
			wantStatus: StatusClosed,
			wantPath: []string{StatusActive + ">" + StatusGrace, StatusGrace + ">" + StatusReadOnly,
				StatusReadOnly + ">" + StatusClosed},
			wantOn:    []string{"2026-11-01", "2026-11-08", "2027-01-07"},
			wantGrace: "2026-11-07",
			wantRO:    "2027-01-06",
		},
		{
			name:       "closed stays closed",
			sub:        Subscription{Status: StatusClosed, CurrentPeriodEnd: day("2026-10-31")},
			today:      "2030-01-01",
			wantStatus: StatusClosed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, steps := SubscriptionLifecycle{}.Advance(tc.sub, plan, day(tc.today))
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, tc.wantStatus)
			}
			if len(steps) != len(tc.wantPath) {
				t.Fatalf("transitions = %+v, want %v", steps, tc.wantPath)
			}
			for i, s := range steps {
				if s.From+">"+s.To != tc.wantPath[i] || s.On.Format(time.DateOnly) != tc.wantOn[i] {
					t.Errorf("transition %d = %s>%s on %s, want %s on %s", i, s.From, s.To,
						s.On.Format(time.DateOnly), tc.wantPath[i], tc.wantOn[i])
				}
			}
			if g := optDay(got.GraceEndsOn); g != tc.wantGrace {
				t.Errorf("grace_ends_on = %q, want %q", g, tc.wantGrace)
			}
			if r := optDay(got.ReadOnlyEndsOn); r != tc.wantRO {
				t.Errorf("read_only_ends_on = %q, want %q", r, tc.wantRO)
			}
		})
	}
}

func TestLifecycleStatesAllowWhatTheTermsPublish(t *testing.T) {
	tests := []struct {
		status       string
		post, export bool
	}{
		{StatusTrialing, true, true},
		{StatusActive, true, true},
		{StatusGrace, true, true},
		{StatusReadOnly, false, true},
		{StatusClosed, false, false},
		{"unknown", false, false},
	}
	for _, tc := range tests {
		st := SubscriptionLifecycle{}.StateOf(tc.status)
		if st.CanPost() != tc.post || st.CanExport() != tc.export {
			t.Errorf("%s: post/export = %v/%v, want %v/%v", tc.status, st.CanPost(), st.CanExport(), tc.post, tc.export)
		}
	}
	for status, want := range map[string]string{
		StatusTrialing: "active", StatusActive: "active", StatusGrace: "active", StatusReadOnly: "read_only",
		StatusClosed: "closed",
	} {
		if got := tenantStatusFor(status); got != want {
			t.Errorf("tenantStatusFor(%s) = %s, want %s", status, got, want)
		}
	}
}

func TestDaysInclusiveCountsBothEnds(t *testing.T) {
	tests := []struct {
		start, end string
		want       int64
	}{
		{"2026-10-18", "2026-10-18", 1},
		{"2026-10-01", "2026-10-31", 31},
		{"2026-02-01", "2026-02-28", 28},
		{"2026-10-18", "2027-10-17", 365},
	}
	for _, tc := range tests {
		if got := daysInclusive(day(tc.start), day(tc.end)); got != tc.want {
			t.Errorf("daysInclusive(%s, %s) = %d, want %d", tc.start, tc.end, got, tc.want)
		}
	}
	saigon := time.FixedZone("ICT", 7*3600)
	start := time.Date(2026, 10, 18, 23, 30, 0, 0, saigon)
	if got := daysInclusive(start, start.AddDate(0, 0, 9)); got != 10 {
		t.Errorf("daysInclusive ignores the time of day: got %d, want 10", got)
	}
}

func TestPlanPeriodEndAndTermsDefaults(t *testing.T) {
	start := day("2026-10-18")
	tests := []struct {
		plan Plan
		want string
	}{
		{Plan{BillingPeriod: PeriodMonth}, "2026-11-17"},
		{Plan{BillingPeriod: PeriodYear}, "2027-10-17"},
		{Plan{BillingPeriod: PeriodSeason}, "2027-02-14"},
		{Plan{BillingPeriod: PeriodSeason, Limits: Limits{TermPeriodDays: 90}}, "2027-01-15"},
		{Plan{BillingPeriod: PeriodNone}, "2026-10-31"},
		{Plan{BillingPeriod: PeriodNone, Limits: Limits{TermTrialDays: 30}}, "2026-11-16"},
	}
	for _, tc := range tests {
		if got := tc.plan.PeriodEnd(start).Format(time.DateOnly); got != tc.want {
			t.Errorf("PeriodEnd(%s, %v) = %s, want %s", tc.plan.BillingPeriod, tc.plan.Limits, got, tc.want)
		}
	}
	p := Plan{Limits: Limits{TermGraceDays: 3, TermReadOnlyDays: -1}}
	if got := p.Term(TermGraceDays); got != 3 {
		t.Errorf("grace term = %d, want 3", got)
	}
	if got := p.Term(TermReadOnlyDays); got != 60 {
		t.Errorf("a negative term falls back to the default: got %d, want 60", got)
	}
	if got := p.Term(TermSupportSLAHours); got != 48 {
		t.Errorf("missing SLA term = %d, want 48", got)
	}
}

func TestEntitlementsWithoutModulesAreUnrestricted(t *testing.T) {
	if !(Entitlements{}).Has(ModuleAIQA) {
		t.Error("a tenant without a subscription must keep every module")
	}
	e := Entitlements{Modules: map[string]bool{ModulePOS: true}}
	if !e.Has(ModulePOS) || e.Has(ModuleAIQA) {
		t.Errorf("Has = pos %v, ai_qa %v; want true, false", e.Has(ModulePOS), e.Has(ModuleAIQA))
	}
}

func TestPlanInputValidate(t *testing.T) {
	valid := PlanInput{
		Code: "year_basic", Name: "Cả năm", Kind: KindYearRound, BillingPeriod: PeriodYear,
		Price: money.MustMoney("2400000"), Modules: []string{ModulePOS}, Limits: map[string]int64{LimitSKUs: 3000, TermGraceDays: 10},
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
	tests := []struct {
		name  string
		edit  func(*PlanInput)
		field string
	}{
		{"code with upper case", func(p *PlanInput) { p.Code = "Year" }, "code"},
		{"code too short", func(p *PlanInput) { p.Code = "y" }, "code"},
		{"blank name", func(p *PlanInput) { p.Name = "  " }, "name"},
		{"unknown kind", func(p *PlanInput) { p.Kind = "lifetime" }, "kind"},
		{"unknown billing period", func(p *PlanInput) { p.BillingPeriod = "week" }, "billing_period"},
		{"negative price", func(p *PlanInput) { p.Price = money.MustMoney("-1") }, "price"},
		{"unknown module", func(p *PlanInput) { p.Modules = []string{"payroll"} }, "modules"},
		{"unknown limit", func(p *PlanInput) { p.Limits = map[string]int64{"users": 5} }, "limits"},
		{"negative limit", func(p *PlanInput) { p.Limits = map[string]int64{LimitSKUs: -1} }, "limits"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.edit(&in)
			e, ok := apperr.As(in.validate())
			if !ok || e.Code != "validation_failed" || e.Fields[tc.field] == "" {
				t.Fatalf("validate = %v, want validation_failed on %s", e, tc.field)
			}
		})
	}
	if got := uniqueModules([]string{ModuleAIQA, ModulePOS, ModulePOS, "payroll"}); len(got) != 2 ||
		got[0] != ModulePOS || got[1] != ModuleAIQA {
		t.Errorf("uniqueModules = %v, want [pos ai_qa]", got)
	}
}

func TestClosureExportIsDownloadableOnlyWhileItsSucceededLinkWorks(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Minute), now.Add(-time.Minute)
	cases := []struct {
		name string
		e    ClosureExport
		want bool
	}{
		{"link still open", ClosureExport{Status: "succeeded", ExpiresAt: &later}, true},
		{"link ends now", ClosureExport{Status: "succeeded", ExpiresAt: &now}, false},
		{"link ended", ClosureExport{Status: "succeeded", ExpiresAt: &earlier}, false},
		{"succeeded without a link end", ClosureExport{Status: "succeeded"}, false},
		{"expired", ClosureExport{Status: "expired", ExpiresAt: &later}, false},
		{"failed", ClosureExport{Status: "failed"}, false},
		{"running", ClosureExport{Status: "running"}, false},
	}
	for _, c := range cases {
		if got := c.e.downloadable(now); got != c.want {
			t.Errorf("%s: downloadable() = %v, want %v", c.name, got, c.want)
		}
	}
	if !(ClosureExport{Reason: exportReasonClosureFinal}).Final() || (ClosureExport{Reason: exportReasonClosure}).Final() {
		t.Error("only the export queued when the wait ended is final")
	}
}

func TestOnlyAClosureTakingItsFinalExportFreezesTheStore(t *testing.T) {
	for status, want := range map[string]bool{
		ClosureRequested: false, ClosureVerified: false, ClosureWaiting: false, ClosureExporting: true,
		ClosureExportFailed: true, ClosureCompleted: false, ClosureCancelled: false,
	} {
		if got := closureFreezes(status); got != want {
			t.Errorf("closureFreezes(%q) = %v, want %v", status, got, want)
		}
	}
}
