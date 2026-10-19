package campaign

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestValidate(t *testing.T) {
	start := clock.Date(2026, time.January, 10)
	neg, over := money.MustMoney("-1"), money.MustPercent("101")
	tests := []struct {
		name string
		c    Campaign
		want map[string]string
	}{
		{"valid", Campaign{Name: "Tết", Kind: KindTet, StartsOn: start, EndsOn: start}, map[string]string{}},
		{"missing fields", Campaign{Name: "  "}, map[string]string{
			"name": "required", "kind": "invalid", "starts_on": "required", "ends_on": "required"}},
		{"ends before it starts", Campaign{Name: "Tết", Kind: KindTet, StartsOn: start, EndsOn: start.AddDate(0, 0, -1)},
			map[string]string{"ends_on": "before_starts_on"}},
		{"amounts out of range", Campaign{Name: "Tết", Kind: KindOther, StartsOn: start, EndsOn: start,
			Budget: &neg, TargetRevenue: &neg, TargetMarginPercent: &over}, map[string]string{
			"budget_amount": "negative", "target_revenue": "negative", "target_margin_percent": "out_of_range"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validate(tt.c)
			if len(got) != len(tt.want) {
				t.Fatalf("validate = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestProjectCarriesBacklogAndFindsTheFirstShortage(t *testing.T) {
	sku := uuid.New()
	from := clock.Date(2026, time.January, 10)
	on := func(day int) *time.Time { d := clock.Date(2026, time.January, day); return &d }
	q := money.MustQty
	demand := []demandEvent{
		{SkuID: sku, On: on(5), Qty: q("2")}, // overdue delivery: backlog, due on the first day
		{SkuID: sku, On: nil, Qty: q("1")},   // reservation: backlog, due on the first day
		{SkuID: sku, On: on(12), Qty: q("10")},
	}
	incoming := []demandEvent{
		{SkuID: sku, On: on(8), Qty: q("3")}, // late receipt: counts on the first day
		{SkuID: sku, On: on(15), Qty: q("20")},
		{SkuID: sku, On: nil, Qty: q("4")}, // undated: kept out of the series
	}
	rows := project([]DemandRow{{SkuID: sku, Available: q("5")}}, demand, incoming, from)
	r := rows[0]
	if !r.Backlog.Equal(q("3")) || !r.UndatedIncoming.Equal(q("4")) {
		t.Errorf("backlog %s, undated %s; want 3 and 4", r.Backlog, r.UndatedIncoming)
	}
	want := []struct {
		day                                       int
		demand, incoming, projected, shortageWant string
	}{
		{10, "3", "3", "5", "0"},
		{12, "10", "0", "-5", "5"},
		{15, "0", "20", "15", "0"},
	}
	if len(r.Days) != len(want) {
		t.Fatalf("days = %+v", r.Days)
	}
	for i, w := range want {
		d := r.Days[i]
		if !d.Date.Equal(*on(w.day)) || !d.Demand.Equal(q(w.demand)) || !d.Incoming.Equal(q(w.incoming)) ||
			!d.Projected.Equal(q(w.projected)) || !d.Shortage.Equal(q(w.shortageWant)) {
			t.Errorf("day %d = %+v, want %+v", i, d, w)
		}
	}
	if r.FirstShortageOn == nil || !r.FirstShortageOn.Equal(*on(12)) || !r.MaxShortage.Equal(q("5")) {
		t.Errorf("first shortage %v, max %s; want 12/01 and 5", r.FirstShortageOn, r.MaxShortage)
	}
}

func TestProjectWithoutEventsListsNoDays(t *testing.T) {
	rows := project([]DemandRow{{SkuID: uuid.New(), Available: money.MustQty("1")}}, nil, nil, clock.Date(2026, time.January, 1))
	if rows[0].Days == nil || len(rows[0].Days) != 0 || rows[0].FirstShortageOn != nil {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestSummarize(t *testing.T) {
	done := time.Now()
	s := summarize([]ChecklistItem{{Title: "a", DoneAt: &done}, {Title: "b"}, {Title: "c"}})
	if s.Total != 3 || s.Done != 1 || len(s.Open) != 2 || s.Open[0].Title != "b" {
		t.Errorf("summary = %+v", s)
	}
	if empty := summarize(nil); empty.Open == nil || empty.Total != 0 {
		t.Errorf("empty summary = %+v", empty)
	}
}
