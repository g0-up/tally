package reporting

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Column kinds tell the client how to format a cell. Every cell is a string (decimals keep their
// precision on the wire) or null.
const (
	KindText    = "text"
	KindMoney   = "money"
	KindQty     = "qty"
	KindPercent = "percent"
	KindDate    = "date"
	KindInt     = "int"
)

// Error codes of reporting.
const (
	CodeUnknownReport = "report_unknown"
	CodeRangeTooLong  = "range_too_long"
	CodeExportExpired = "export_expired"
	CodeExportState   = "export_status_invalid"
	CodeExportNotDone = "export_not_ready"
	CodeNameTaken     = "saved_report_name_taken"
)

// Column is one column of a report table.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// Row is one row of a report table by column key.
type Row map[string]*string

// Metric is a headline figure of a report, with the previous period's value when compared.
type Metric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Kind     string  `json:"kind"`
	Value    *string `json:"value"`
	Previous *string `json:"previous,omitempty"`
}

// Section is a secondary table of a report (a kit report's open assembly orders, the advances
// beside an aging). Sections are not paged and not exported.
type Section struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Columns []Column `json:"columns"`
	Rows    []Row    `json:"rows"`
	Totals  Row      `json:"totals,omitempty"`
}

// Table is the result of one report run: its main table (one page of it for paged reports), the
// totals of the whole result, headline metrics, notes stating what is counted, and sections.
type Table struct {
	ReportKey string
	Title     string
	From      *time.Time
	To        *time.Time
	AsOf      *time.Time
	Days      int
	GroupBy   string
	// Scope states what the figures count, in the owner's words.
	Scope string
	Notes []string
	// Provisional is set when a pending cost recalculation may still change the figures.
	Provisional bool
	Columns     []Column
	// Personal lists the columns of Rows that name or reach a person or business (a customer's
	// name or phone, a party), so a reader outside the tenant can leave them out.
	Personal []string
	Rows     []Row
	Totals   Row
	Summary  []Metric
	Sections []Section
	// Next is the position after the last row when more rows follow.
	Next *httpx.Cursor
}

// add appends a row of a paged report to the page, or hands it to the export's sink.
func (t *Table) add(p Params, r Row) error {
	if p.sink != nil {
		return p.sink(r)
	}
	t.Rows = append(t.Rows, r)
	return nil
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

func errRangeTooLong(field string) error {
	return apperr.NewValidation(CodeRangeTooLong, "The date range is too long", map[string]string{field: "too_far"})
}

// Cell constructors.

func text(s string) *string { return &s }

func optText(s *string) *string {
	if s == nil {
		return nil
	}
	return text(*s)
}

func dec(d decimal.Decimal) *string { return text(d.String()) }

func optDec(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	return dec(*d)
}

func amount(m money.Money) *string { return text(m.String()) }

func day(t time.Time) *string { return text(t.Format(time.DateOnly)) }

func optDay(t *time.Time) *string {
	if t == nil {
		return nil
	}
	return day(*t)
}

func integer(n int64) *string { return text(decimal.NewFromInt(n).String()) }

// percentOf is part / whole × 100 rounded to two decimals, or nil when whole is zero.
func percentOf(part, whole decimal.Decimal) *string {
	if whole.IsZero() {
		return nil
	}
	return dec(part.Div(whole).Mul(decimal.NewFromInt(100)).Round(2))
}

// metric is a headline figure without comparison.
func metric(key, label, kind string, v *string) Metric {
	return Metric{Key: key, Label: label, Kind: kind, Value: v}
}
