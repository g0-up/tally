package reporting

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// Report keys, as saved reports and export jobs store them. The URL slug of a key replaces its
// underscores with hyphens.
const (
	ReportNXT          = "nxt"
	ReportLotStock     = "lot_stock"
	ReportRevenue      = "revenue"
	ReportGrossMargin  = "gross_margin"
	ReportDebtAging    = "debt_aging"
	ReportDebtSchedule = "debt_schedule"
	ReportCashFlow     = "cash_flow"
	ReportKits         = "kits"
	ReportSeason       = "season"
	ReportBuy          = "buy_suggestions"
	ReportClearance    = "clearance_suggestions"
	ReportCashForecast = "cash_forecast"
)

// savedReportKeys are the report keys a layout may be saved for (saved_reports.report_key).
var savedReportKeys = []string{
	ReportNXT, ReportLotStock, ReportRevenue, ReportGrossMargin, ReportDebtAging, ReportDebtSchedule, ReportCashFlow,
	ReportKits, ReportSeason,
}

// rangeKind is how a report reads dates from its parameters.
type rangeKind int

const (
	// rangeNone: the report shows the current state.
	rangeNone rangeKind = iota
	// rangePeriod: from and to, by default the month to date.
	rangePeriod
	// rangeAsOf: as_of, by default today.
	rangeAsOf
	// rangeFuture: from and to, by default today and the 30 days after.
	rangeFuture
)

// Filter kinds.
const (
	filterUUID = "uuid"
	filterText = "text"
	filterInt  = "int"
	filterEnum = "enum"
	// filterUUIDList is a comma-separated list of at most Max UUIDs.
	filterUUIDList = "uuid_list"
)

// Filter is a query parameter a report accepts besides its dates and grouping.
type Filter struct {
	Name     string
	Kind     string
	Values   []string
	Min, Max int
	Required bool
}

// Params are the validated parameters of one report run.
type Params struct {
	From, To time.Time
	AsOf     time.Time
	Today    time.Time
	GroupBy  string
	Filters  map[string]string
	Columns  []string
	After    *httpx.Cursor
	Limit    int
	// sink receives every row of a paged report, in order, instead of one page of them: an export
	// streams the whole result of one statement and the runner leaves Rows and Next empty.
	sink func(Row) error
}

// firstPage reports whether the run returns the first page of a paged report, the one that
// carries the totals of the stock reports.
func (p Params) firstPage() bool { return p.After == nil }

// pageLimit is the LIMIT of a paged statement: the page plus one row to tell whether more follow,
// or NULL (every row) when the rows stream to a sink.
func (p Params) pageLimit() any {
	if p.sink != nil {
		return nil
	}
	return p.Limit + 1
}

func (p Params) filterUUID(name string) *uuid.UUID {
	v, ok := p.Filters[name]
	if !ok {
		return nil
	}
	id := uuid.MustParse(v) // validated by resolve
	return &id
}

func (p Params) filterUUIDs(name string) []uuid.UUID {
	v, ok := p.Filters[name]
	if !ok {
		return nil
	}
	var out []uuid.UUID
	for _, part := range strings.Split(v, ",") {
		out = append(out, uuid.MustParse(strings.TrimSpace(part))) // validated by resolve
	}
	return out
}

func (p Params) filterText(name string) *string {
	v, ok := p.Filters[name]
	if !ok {
		return nil
	}
	return &v
}

func (p Params) filterInt(name string, fallback int) int {
	v, ok := p.Filters[name]
	if !ok {
		return fallback
	}
	n, _ := strconv.Atoi(v) // validated by resolve
	return n
}

// Report is one entry of the report catalogue: what it reads, which parameters it takes and the
// columns it can show. A new report is a new entry; nothing else changes.
type Report struct {
	Key   string
	Title string
	Range rangeKind
	// MaxDays bounds a date range.
	MaxDays int
	// GroupBys are the groupings the report offers; the first is the default.
	GroupBys []string
	Filters  []Filter
	// Columns are every column the report can show, in display order.
	Columns []Column
	// Paged reports return a page of rows with a cursor; the others return every row.
	Paged bool
	run   func(ctx context.Context, s *Service, scope Scope, p Params) (Table, error)
}

// Slug is the URL form of the report key.
func (r Report) Slug() string { return strings.ReplaceAll(r.Key, "_", "-") }

// RangeName names how the report reads dates: none, period, as_of or future.
func (r Report) RangeName() string { return rangeNames[r.Range] }

// Saveable reports whether a layout of the report may be saved.
func (r Report) Saveable() bool { return slices.Contains(savedReportKeys, r.Key) }

func (r Report) column(key string) (Column, bool) {
	for _, c := range r.Columns {
		if c.Key == key {
			return c, true
		}
	}
	return Column{}, false
}

// Registry maps report keys and slugs to reports.
type Registry struct {
	reports []Report
	byName  map[string]int
}

// NewRegistry returns a registry of reports. Keys must be unique.
func NewRegistry(reports ...Report) *Registry {
	r := &Registry{byName: map[string]int{}}
	for _, rep := range reports {
		if _, dup := r.byName[rep.Key]; dup {
			panic("reporting: duplicate report " + rep.Key)
		}
		r.byName[rep.Key] = len(r.reports)
		r.byName[rep.Slug()] = len(r.reports)
		r.reports = append(r.reports, rep)
	}
	return r
}

// DefaultRegistry holds every built-in report.
func DefaultRegistry() *Registry {
	return NewRegistry(nxtReport(), lotStockReport(), revenueReport(), grossMarginReport(), debtAgingReport(),
		debtScheduleReport(), cashFlowReport(), kitsReport(), seasonReport(), buyReport(), clearanceReport(),
		cashForecastReport())
}

// Lookup finds a report by key or slug.
func (r *Registry) Lookup(name string) (Report, bool) {
	i, ok := r.byName[name]
	if !ok {
		return Report{}, false
	}
	return r.reports[i], true
}

// Reports lists the reports in registration order.
func (r *Registry) Reports() []Report { return slices.Clone(r.reports) }

// Raw parameter names every report understands.
const (
	paramFrom    = "from"
	paramTo      = "to"
	paramAsOf    = "as_of"
	paramGroupBy = "group_by"
	paramColumns = "columns"
)

// defaultFutureDays is the default length of a schedule or forecast.
const defaultFutureDays = 30

// resolve validates raw query parameters against the report and fills the defaults relative to
// today. Unknown parameters are ignored.
func (r Report) resolve(raw map[string]string, today time.Time) (Params, error) {
	p := Params{Today: today, Filters: map[string]string{}}
	fields := map[string]string{}
	date := func(name string) *time.Time {
		v := strings.TrimSpace(raw[name])
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			fields[name] = "invalid_date"
			return nil
		}
		d := clock.Date(t.Year(), t.Month(), t.Day())
		return &d
	}
	switch r.Range {
	case rangePeriod, rangeFuture:
		from, to := date(paramFrom), date(paramTo)
		if r.Range == rangePeriod {
			p.From, p.To = clock.Date(today.Year(), today.Month(), 1), today
		} else {
			p.From, p.To = today, today.AddDate(0, 0, defaultFutureDays)
		}
		if from != nil {
			p.From = *from
			if to == nil && p.To.Before(p.From) {
				p.To = p.From
			}
		}
		if to != nil {
			p.To = *to
		}
		if len(fields) == 0 && p.To.Before(p.From) {
			fields[paramTo] = "before_from"
		}
		if len(fields) == 0 && r.MaxDays > 0 && daysBetween(p.From, p.To) > r.MaxDays {
			return Params{}, errRangeTooLong(paramTo)
		}
	case rangeAsOf:
		p.AsOf = today
		if asOf := date(paramAsOf); asOf != nil {
			p.AsOf = *asOf
		}
	}
	if len(r.GroupBys) > 0 {
		p.GroupBy = r.GroupBys[0]
		if g := strings.TrimSpace(raw[paramGroupBy]); g != "" {
			if !slices.Contains(r.GroupBys, g) {
				fields[paramGroupBy] = "invalid"
			}
			p.GroupBy = g
		}
	}
	for _, f := range r.Filters {
		v := strings.TrimSpace(raw[f.Name])
		if v == "" {
			if f.Required {
				fields[f.Name] = "required"
			}
			continue
		}
		if msg := f.check(v); msg != "" {
			fields[f.Name] = msg
			continue
		}
		p.Filters[f.Name] = v
	}
	if cols := strings.TrimSpace(raw[paramColumns]); cols != "" {
		for _, c := range strings.Split(cols, ",") {
			c = strings.TrimSpace(c)
			if _, ok := r.column(c); !ok {
				fields[paramColumns] = "unknown_column"
				break
			}
			if !slices.Contains(p.Columns, c) {
				p.Columns = append(p.Columns, c)
			}
		}
	}
	if len(fields) > 0 {
		return Params{}, errInvalid(fields)
	}
	return p, nil
}

func (f Filter) check(v string) string {
	switch f.Kind {
	case filterUUID:
		if _, err := uuid.Parse(v); err != nil {
			return "invalid"
		}
	case filterInt:
		n, err := strconv.Atoi(v)
		if err != nil || n < f.Min || n > f.Max {
			return "out_of_range"
		}
	case filterEnum:
		if !slices.Contains(f.Values, v) {
			return "invalid"
		}
	case filterUUIDList:
		parts := strings.Split(v, ",")
		if len(parts) > f.Max {
			return "too_many"
		}
		for _, part := range parts {
			if _, err := uuid.Parse(strings.TrimSpace(part)); err != nil {
				return "invalid"
			}
		}
	case filterText:
		if len(v) > 200 {
			return "too_long"
		}
	}
	return ""
}

// canonical is the resolved parameters as raw query values: what an export job stores so that it
// runs with the dates the owner saw, whenever the worker picks it up.
func (r Report) canonical(p Params) map[string]string {
	out := map[string]string{}
	switch r.Range {
	case rangePeriod, rangeFuture:
		out[paramFrom], out[paramTo] = p.From.Format(time.DateOnly), p.To.Format(time.DateOnly)
	case rangeAsOf:
		out[paramAsOf] = p.AsOf.Format(time.DateOnly)
	}
	if p.GroupBy != "" {
		out[paramGroupBy] = p.GroupBy
	}
	for k, v := range p.Filters {
		out[k] = v
	}
	if len(p.Columns) > 0 {
		out[paramColumns] = strings.Join(p.Columns, ",")
	}
	return out
}

// rowID is the row key holding the id of what a row is about (a SKU, a party), for links. It is
// not a column and is kept whatever columns are chosen.
const rowID = "id"

// project keeps the chosen columns of a table, in the order chosen; no choice keeps them all.
func project(t Table, keys []string) Table {
	if len(keys) == 0 {
		return t
	}
	byKey := map[string]Column{}
	for _, c := range t.Columns {
		byKey[c.Key] = c
	}
	cols := make([]Column, 0, len(keys))
	for _, k := range keys {
		if c, ok := byKey[k]; ok {
			cols = append(cols, c)
		}
	}
	keep := func(r Row) Row {
		if r == nil {
			return nil
		}
		out := make(Row, len(cols)+1)
		if id, ok := r[rowID]; ok {
			out[rowID] = id
		}
		for _, c := range cols {
			out[c.Key] = r[c.Key]
		}
		return out
	}
	t.Columns = cols
	for i, r := range t.Rows {
		t.Rows[i] = keep(r)
	}
	t.Totals = keep(t.Totals)
	return t
}
