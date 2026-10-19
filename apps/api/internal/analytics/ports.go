package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Scope is the tenant a question is asked in.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// Reports is the report catalogue as the Q&A may use it: list the reports and run one. It has no
// write path; the Q&A never sees SQL or a document table.
type Reports interface {
	Catalogue() []ReportSpec
	// Run runs a report with raw parameters and returns at most limit rows of it.
	Run(ctx context.Context, scope Scope, key string, params map[string]string, limit int) (ReportResult, error)
}

// Report date ranges, as ReportSpec.Range names them.
const (
	RangeNone   = "none"
	RangePeriod = "period"
	RangeAsOf   = "as_of"
	RangeFuture = "future"
)

// ReportSpec describes one report and its parameters.
type ReportSpec struct {
	Key      string
	Title    string
	Range    string
	MaxDays  int
	GroupBys []string
	Filters  []FilterSpec
}

// FilterSpec is a report parameter besides the dates and the grouping.
type FilterSpec struct {
	Name     string
	Kind     string // uuid, text, int or enum
	Values   []string
	Min, Max int
	Required bool
}

// ReportColumn is a column of a report result.
type ReportColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// ReportMetric is a headline figure of a report result.
type ReportMetric struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Value *string `json:"value"`
}

// ReportResult is one report run.
type ReportResult struct {
	Key, Title string
	From, To   *time.Time
	AsOf       *time.Time
	// Scope and Notes state what the figures count.
	Scope   string
	Notes   []string
	Columns []ReportColumn
	// Personal are the columns naming or reaching a person or business.
	Personal []string
	Rows     []map[string]*string
	Totals   map[string]*string
	Summary  []ReportMetric
	// More is set when rows beyond the limit exist.
	More bool
}
