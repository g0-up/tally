package app

import (
	"context"

	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/reporting"
)

// analyticsReports is the report catalogue as the AI Q&A reads it: the reports and one page of a
// report run, nothing that writes.
type analyticsReports struct{ s *reporting.Service }

func (r analyticsReports) Catalogue() []analytics.ReportSpec {
	reports := r.s.Registry().Reports()
	out := make([]analytics.ReportSpec, len(reports))
	for i, rep := range reports {
		spec := analytics.ReportSpec{
			Key: rep.Key, Title: rep.Title, Range: rep.RangeName(), MaxDays: rep.MaxDays, GroupBys: rep.GroupBys,
			Filters: make([]analytics.FilterSpec, len(rep.Filters)),
		}
		for j, f := range rep.Filters {
			spec.Filters[j] = analytics.FilterSpec{
				Name: f.Name, Kind: f.Kind, Values: f.Values, Min: f.Min, Max: f.Max, Required: f.Required,
			}
		}
		out[i] = spec
	}
	return out
}

func (r analyticsReports) Run(ctx context.Context, scope analytics.Scope, key string, params map[string]string, limit int) (analytics.ReportResult, error) {
	t, err := r.s.Run(ctx, reporting.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}, key, params,
		reporting.Page{Limit: limit})
	if err != nil {
		return analytics.ReportResult{}, err
	}
	out := analytics.ReportResult{
		Key: t.ReportKey, Title: t.Title, From: t.From, To: t.To, AsOf: t.AsOf, Scope: t.Scope, Notes: t.Notes,
		Columns: make([]analytics.ReportColumn, len(t.Columns)), Personal: t.Personal,
		Rows: make([]map[string]*string, len(t.Rows)), Totals: t.Totals, Summary: make([]analytics.ReportMetric, len(t.Summary)),
		More: t.Next != nil,
	}
	for i, c := range t.Columns {
		out.Columns[i] = analytics.ReportColumn{Key: c.Key, Label: c.Label}
	}
	for i, row := range t.Rows {
		out.Rows[i] = row
	}
	for i, m := range t.Summary {
		out.Summary[i] = analytics.ReportMetric{Key: m.Key, Label: m.Label, Value: m.Value}
	}
	return out, nil
}
