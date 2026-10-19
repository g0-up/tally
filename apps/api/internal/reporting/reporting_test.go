package reporting

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
)

func date(y int, m time.Month, d int) time.Time { return clock.Date(y, m, d) }

func TestNewPeriod(t *testing.T) {
	tests := []struct {
		kind     string
		at       time.Time
		from, to time.Time
		prevFrom time.Time
		buckets  int
	}{
		{PeriodDay, date(2026, 3, 1), date(2026, 3, 1), date(2026, 3, 1), date(2026, 2, 28), 1},
		{PeriodWeek, date(2026, 3, 3), date(2026, 2, 25), date(2026, 3, 3), date(2026, 2, 18), 7},
		{PeriodMonth, date(2026, 2, 14), date(2026, 2, 1), date(2026, 2, 28), date(2026, 1, 1), 28},
		{PeriodYear, date(2026, 7, 9), date(2026, 1, 1), date(2026, 12, 31), date(2025, 1, 1), 12},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			p, err := NewPeriod(tc.kind, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if !p.From.Equal(tc.from) || !p.To.Equal(tc.to) {
				t.Fatalf("period = %s..%s, want %s..%s", p.From, p.To, tc.from, tc.to)
			}
			if got := p.Prev().From; !got.Equal(tc.prevFrom) {
				t.Fatalf("prev from = %s, want %s", got, tc.prevFrom)
			}
			if got := p.Next().Prev(); !got.From.Equal(p.From) || !got.To.Equal(p.To) {
				t.Fatalf("next then prev = %s..%s, want %s..%s", got.From, got.To, p.From, p.To)
			}
			if got := len(p.Buckets()); got != tc.buckets {
				t.Fatalf("buckets = %d, want %d", got, tc.buckets)
			}
		})
	}
	if _, err := NewPeriod("quarter", date(2026, 1, 1)); apperr.KindOf(err) != apperr.Validation {
		t.Fatalf("unknown kind: err = %v, want a validation error", err)
	}
}

func testReport() Report {
	return Report{
		Key: "sample", Range: rangePeriod, MaxDays: 31, GroupBys: []string{"product", "customer"},
		Filters: []Filter{
			{Name: "warehouse_id", Kind: filterUUID},
			{Name: "days", Kind: filterInt, Min: 0, Max: 90},
			{Name: "direction", Kind: filterEnum, Values: []string{"receivable", "payable"}},
		},
		Columns: []Column{{Key: "code", Kind: KindText}, {Key: "amount", Kind: KindMoney}, {Key: "qty", Kind: KindQty}},
	}
}

func TestResolveDefaultsAndValidation(t *testing.T) {
	rep := testReport()
	today := date(2026, 10, 18)
	p, err := rep.resolve(map[string]string{"unknown": "x"}, today)
	if err != nil {
		t.Fatal(err)
	}
	if !p.From.Equal(date(2026, 10, 1)) || !p.To.Equal(today) || p.GroupBy != "product" {
		t.Fatalf("defaults = %s..%s group %q", p.From, p.To, p.GroupBy)
	}
	p, err = rep.resolve(map[string]string{"from": "2026-09-20"}, today)
	if err != nil || !p.To.Equal(today) {
		t.Fatalf("from only: to = %s err = %v", p.To, err)
	}
	if _, err := rep.resolve(map[string]string{"from": "2026-01-01", "to": "2026-03-01"}, today); !apperr.Is(err, CodeRangeTooLong) {
		t.Fatalf("long range: err = %v, want %s", err, CodeRangeTooLong)
	}
	bad := map[string]string{
		"to": "2026-09-01", "from": "2026-09-05", "group_by": "color", "warehouse_id": "nope", "days": "91",
		"direction": "sideways", "columns": "code,ghost",
	}
	_, err = rep.resolve(bad, today)
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("bad params: err = %v", err)
	}
	for _, f := range []string{"to", "group_by", "warehouse_id", "days", "direction", "columns"} {
		if e.Fields[f] == "" {
			t.Errorf("bad params: field %s not reported (%v)", f, e.Fields)
		}
	}
}

func TestCanonicalRoundTrips(t *testing.T) {
	rep := testReport()
	raw := map[string]string{"from": "2026-10-02", "to": "2026-10-10", "group_by": "customer", "days": "7",
		"columns": "qty, code,qty"}
	p, err := rep.resolve(raw, date(2026, 10, 18))
	if err != nil {
		t.Fatal(err)
	}
	c := rep.canonical(p)
	if c["columns"] != "qty,code" || c["days"] != "7" || c["from"] != "2026-10-02" {
		t.Fatalf("canonical = %v", c)
	}
	// Resolving the canonical parameters on another day gives the same run.
	again, err := rep.resolve(c, date(2027, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !again.From.Equal(p.From) || !again.To.Equal(p.To) || again.GroupBy != p.GroupBy ||
		strings.Join(again.Columns, ",") != strings.Join(p.Columns, ",") {
		t.Fatalf("round trip = %+v, want %+v", again, p)
	}
}

func TestProjectKeepsIDAndOrder(t *testing.T) {
	rep := testReport()
	tbl := Table{
		Columns: rep.Columns,
		Rows:    []Row{{rowID: text("r1"), "code": text("A"), "amount": text("10"), "qty": text("2")}},
		Totals:  Row{"amount": text("10"), "qty": text("2")},
	}
	got := project(tbl, []string{"qty", "code"})
	if len(got.Columns) != 2 || got.Columns[0].Key != "qty" || got.Columns[1].Key != "code" {
		t.Fatalf("columns = %v", got.Columns)
	}
	if _, ok := got.Rows[0]["amount"]; ok {
		t.Fatal("projected row keeps a hidden column")
	}
	if got.Rows[0][rowID] == nil || *got.Rows[0][rowID] != "r1" {
		t.Fatal("projected row lost its id")
	}
}

func TestCheckConfig(t *testing.T) {
	rep := testReport()
	cfg, err := rep.checkConfig(ReportConfig{Columns: []string{"amount"}, GroupBy: "customer",
		Filters: map[string]string{"direction": "payable"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GroupBy != "customer" || cfg.Filters["direction"] != "payable" || len(cfg.Columns) != 1 {
		t.Fatalf("config = %+v", cfg)
	}
	_, err = rep.checkConfig(ReportConfig{Filters: map[string]string{"from": "2026-01-01"}})
	if e, ok := apperr.As(err); !ok || e.Fields["config.filters.from"] != "unknown_filter" {
		t.Fatalf("date filter: err = %v, want unknown_filter", err)
	}
	_, err = rep.checkConfig(ReportConfig{Columns: []string{"ghost"}})
	if e, ok := apperr.As(err); !ok || e.Fields["config.columns"] != "unknown_column" {
		t.Fatalf("unknown column: err = %v", err)
	}
}

func TestCSVWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewCSVWriter(&buf)
	cols := []Column{{Key: "name", Label: "Tên", Kind: KindText}, {Key: "amount", Label: "Số tiền", Kind: KindMoney}}
	page1 := Table{Columns: cols, Rows: []Row{
		{"name": text("=HYPERLINK(\"x\")"), "amount": text("-5")},
		{"name": text("Bánh, kẹo"), "amount": nil},
	}}
	page2 := Table{Columns: cols, Rows: []Row{{"name": text("Trà"), "amount": text("7")}},
		Totals: Row{"amount": text("2")}}
	if err := w.WriteTable(page1, false); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteTable(page2, true); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "\xEF\xBB\xBF" + "Tên,Số tiền\n" + "\"'=HYPERLINK(\"\"x\"\")\",-5\n" + "\"Bánh, kẹo\",\n" + "Trà,7\n" +
		"Tổng cộng,2\n"
	if got := buf.String(); got != want {
		t.Fatalf("csv =\n%q\nwant\n%q", got, want)
	}
	if w.Rows() != 3 || w.Bytes() != int64(buf.Len()) {
		t.Fatalf("rows = %d bytes = %d (buffer %d)", w.Rows(), w.Bytes(), buf.Len())
	}
}

func TestDefaultRegistry(t *testing.T) {
	reg := DefaultRegistry()
	for _, key := range savedReportKeys {
		rep, ok := reg.Lookup(key)
		if !ok || !rep.Saveable() {
			t.Errorf("saved report key %s missing from the registry", key)
		}
		if _, ok := reg.Lookup(rep.Slug()); !ok {
			t.Errorf("slug of %s does not resolve", key)
		}
	}
	for _, rep := range reg.Reports() {
		if rep.run == nil || len(rep.Columns) == 0 {
			t.Errorf("report %s has no runner or columns", rep.Key)
		}
		seen := map[string]bool{}
		for _, c := range rep.Columns {
			if seen[c.Key] || c.Key == rowID {
				t.Errorf("report %s: column %q duplicated or reserved", rep.Key, c.Key)
			}
			seen[c.Key] = true
		}
	}
}

func TestToExportDTODownloadPath(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	j := ExportJob{Status: ExportSucceeded, ExpiresAt: &later}
	if toExportDTO(j, now).DownloadPath == nil {
		t.Fatal("finished export has no download path")
	}
	if toExportDTO(j, later).DownloadPath != nil {
		t.Fatal("expired export still has a download path")
	}
}

func TestUUIDListFilterCheck(t *testing.T) {
	f := Filter{Name: filterCustomerIDs, Kind: filterUUIDList, Max: 2}
	a, b := "0190d6a0-0000-7000-8000-000000000001", "0190d6a0-0000-7000-8000-000000000002"
	for v, want := range map[string]string{
		a:                     "",
		a + ", " + b:          "",
		a + "," + b + "," + a: "too_many",
		a + ",nope":           "invalid",
		"":                    "invalid",
	} {
		if got := f.check(v); got != want {
			t.Errorf("check(%q) = %q, want %q", v, got, want)
		}
	}
}
