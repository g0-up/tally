package reporting

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func dateText(t time.Time) string { return t.Format(time.DateOnly) }

func optDateText(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateText(*t)
	return &s
}

type reportDTO struct {
	Key      string         `json:"key"`
	Slug     string         `json:"slug"`
	Title    string         `json:"title"`
	Range    string         `json:"range"`
	MaxDays  int            `json:"max_days,omitempty"`
	GroupBys []string       `json:"group_bys"`
	Filters  []reportFilter `json:"filters"`
	Columns  []Column       `json:"columns"`
	Paged    bool           `json:"paged"`
	Saveable bool           `json:"saveable"`
}

type reportFilter struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Values   []string `json:"values,omitempty"`
	Min      *int     `json:"min,omitempty"`
	Max      *int     `json:"max,omitempty"`
	Required bool     `json:"required"`
}

var rangeNames = map[rangeKind]string{rangeNone: "none", rangePeriod: "period", rangeAsOf: "as_of", rangeFuture: "future"}

func toReportDTO(r Report) reportDTO {
	out := reportDTO{
		Key: r.Key, Slug: r.Slug(), Title: r.Title, Range: r.RangeName(), MaxDays: r.MaxDays,
		GroupBys: r.GroupBys, Filters: make([]reportFilter, len(r.Filters)), Columns: r.Columns, Paged: r.Paged,
		Saveable: r.Saveable(),
	}
	if out.GroupBys == nil {
		out.GroupBys = []string{}
	}
	for i, f := range r.Filters {
		out.Filters[i] = reportFilter{Name: f.Name, Kind: f.Kind, Values: f.Values, Required: f.Required}
		switch f.Kind {
		case filterInt:
			lo, hi := f.Min, f.Max
			out.Filters[i].Min, out.Filters[i].Max = &lo, &hi
		case filterUUIDList:
			hi := f.Max
			out.Filters[i].Max = &hi
		}
	}
	return out
}

type tableDTO struct {
	ReportKey   string    `json:"report_key"`
	Title       string    `json:"title"`
	From        *string   `json:"from"`
	To          *string   `json:"to"`
	AsOf        *string   `json:"as_of"`
	Days        int       `json:"days"`
	GroupBy     *string   `json:"group_by"`
	Scope       string    `json:"scope"`
	Notes       []string  `json:"notes"`
	Provisional bool      `json:"provisional"`
	Columns     []Column  `json:"columns"`
	Rows        []Row     `json:"rows"`
	Totals      Row       `json:"totals"`
	Summary     []Metric  `json:"summary"`
	Sections    []Section `json:"sections"`
	NextCursor  *string   `json:"next_cursor"`
}

func toTableDTO(t Table) tableDTO {
	out := tableDTO{
		ReportKey: t.ReportKey, Title: t.Title, From: optDateText(t.From), To: optDateText(t.To), AsOf: optDateText(t.AsOf),
		Days: t.Days, Scope: t.Scope, Notes: t.Notes, Provisional: t.Provisional, Columns: t.Columns, Rows: t.Rows,
		Totals: t.Totals, Summary: t.Summary, Sections: t.Sections,
	}
	if t.GroupBy != "" {
		g := t.GroupBy
		out.GroupBy = &g
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	if out.Columns == nil {
		out.Columns = []Column{}
	}
	if out.Rows == nil {
		out.Rows = []Row{}
	}
	if out.Summary == nil {
		out.Summary = []Metric{}
	}
	if out.Sections == nil {
		out.Sections = []Section{}
	}
	for i := range out.Sections {
		if out.Sections[i].Rows == nil {
			out.Sections[i].Rows = []Row{}
		}
	}
	if t.Next != nil {
		c := t.Next.Encode()
		out.NextCursor = &c
	}
	return out
}

type periodDTO struct {
	Kind string `json:"kind"`
	Date string `json:"date"`
	From string `json:"from"`
	To   string `json:"to"`
}

func toPeriodDTO(p Period) periodDTO {
	return periodDTO{Kind: p.Kind, Date: dateText(p.Date), From: dateText(p.From), To: dateText(p.To)}
}

type fundBalanceDTO struct {
	ID      uuid.UUID   `json:"id"`
	Name    string      `json:"name"`
	Kind    string      `json:"kind"`
	Balance money.Money `json:"balance"`
}

type cashCardDTO struct {
	In          money.Money       `json:"in"`
	Out         money.Money       `json:"out"`
	Net         money.Money       `json:"net"`
	Balance     money.Money       `json:"balance"`
	PrevIn      money.Money       `json:"prev_in"`
	PrevOut     money.Money       `json:"prev_out"`
	PrevNet     money.Money       `json:"prev_net"`
	PrevBalance money.Money       `json:"prev_balance"`
	Funds       []fundBalanceDTO  `json:"funds"`
	ByCategory  []categoryFlowDTO `json:"by_category"`
	Link        string            `json:"link"`
}

type categoryFlowDTO struct {
	Category string      `json:"category"`
	Label    string      `json:"label"`
	In       money.Money `json:"in"`
	Out      money.Money `json:"out"`
}

type partyBalanceDTO struct {
	PartyID uuid.UUID   `json:"party_id"`
	Name    string      `json:"name"`
	Amount  money.Money `json:"amount"`
	Overdue money.Money `json:"overdue"`
	NextDue string      `json:"next_due"`
}

type debtSideDTO struct {
	Balance      money.Money       `json:"balance"`
	Increase     money.Money       `json:"increase"`
	Settled      money.Money       `json:"settled"`
	PrevBalance  money.Money       `json:"prev_balance"`
	PrevIncrease money.Money       `json:"prev_increase"`
	PrevSettled  money.Money       `json:"prev_settled"`
	DueSoon      money.Money       `json:"due_soon"`
	Overdue      money.Money       `json:"overdue"`
	Top          []partyBalanceDTO `json:"top"`
	Link         string            `json:"link"`
}

type debtCardDTO struct {
	Receivable debtSideDTO `json:"receivable"`
	Payable    debtSideDTO `json:"payable"`
	DueLink    string      `json:"due_link"`
}

type seriesPointDTO struct {
	Label string      `json:"label"`
	From  string      `json:"from"`
	To    string      `json:"to"`
	In    money.Money `json:"in"`
	Out   money.Money `json:"out"`
}

type dashboardEntryDTO struct {
	ID       uuid.UUID   `json:"id"`
	DocNo    *string     `json:"doc_no"`
	Party    *string     `json:"party"`
	Fund     string      `json:"fund"`
	Category string      `json:"category"`
	Amount   money.Money `json:"amount"`
	Reversal bool        `json:"reversal"`
}

type dashboardDTO struct {
	Period   periodDTO           `json:"period"`
	Today    string              `json:"today"`
	Previous periodDTO           `json:"previous"`
	Next     periodDTO           `json:"next"`
	Cash     cashCardDTO         `json:"cash"`
	Debt     debtCardDTO         `json:"debt"`
	Series   []seriesPointDTO    `json:"series"`
	Entries  []dashboardEntryDTO `json:"entries"`
}

func toDebtSideDTO(d DebtSide) debtSideDTO {
	out := debtSideDTO{
		Balance: d.Balance, Increase: d.Increase, Settled: d.Settled, PrevBalance: d.PrevBalance,
		PrevIncrease: d.PrevIncrease, PrevSettled: d.PrevSettled, DueSoon: d.DueSoon, Overdue: d.Overdue,
		Top: make([]partyBalanceDTO, len(d.Top)), Link: d.Link,
	}
	for i, p := range d.Top {
		out.Top[i] = partyBalanceDTO{PartyID: p.PartyID, Name: p.Name, Amount: p.Amount, Overdue: p.Overdue, NextDue: dateText(p.NextDue)}
	}
	return out
}

func toDashboardDTO(d Dashboard) dashboardDTO {
	out := dashboardDTO{
		Period: toPeriodDTO(d.Period), Today: dateText(d.Today), Previous: toPeriodDTO(d.Previous), Next: toPeriodDTO(d.Next),
		Cash: cashCardDTO{
			In: d.Cash.In, Out: d.Cash.Out, Net: d.Cash.Net, Balance: d.Cash.Balance, PrevIn: d.Cash.PrevIn,
			PrevOut: d.Cash.PrevOut, PrevNet: d.Cash.PrevNet, PrevBalance: d.Cash.PrevBalance,
			Funds: make([]fundBalanceDTO, len(d.Cash.Funds)), ByCategory: make([]categoryFlowDTO, len(d.Cash.ByCategory)),
			Link: d.Cash.Link,
		},
		Debt: debtCardDTO{
			Receivable: toDebtSideDTO(d.Debt.Receivable), Payable: toDebtSideDTO(d.Debt.Payable), DueLink: d.Debt.DueLink,
		},
		Series:  make([]seriesPointDTO, len(d.Series)),
		Entries: make([]dashboardEntryDTO, len(d.Entries)),
	}
	for i, f := range d.Cash.Funds {
		out.Cash.Funds[i] = fundBalanceDTO(f)
	}
	for i, f := range d.Cash.ByCategory {
		out.Cash.ByCategory[i] = categoryFlowDTO(f)
	}
	for i, p := range d.Series {
		out.Series[i] = seriesPointDTO{Label: p.Label, From: dateText(p.From), To: dateText(p.To), In: p.In, Out: p.Out}
	}
	for i, e := range d.Entries {
		out.Entries[i] = dashboardEntryDTO(e)
	}
	return out
}

type savedReportDTO struct {
	ID        uuid.UUID    `json:"id"`
	ReportKey string       `json:"report_key"`
	Name      string       `json:"name"`
	Config    ReportConfig `json:"config"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func toSavedDTO(r SavedReport) savedReportDTO {
	return savedReportDTO{
		ID: r.ID, ReportKey: r.ReportKey, Name: r.Name, Config: r.Config, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

type savedReportRequest struct {
	ReportKey string       `json:"report_key" validate:"required"`
	Name      string       `json:"name" validate:"required"`
	Config    ReportConfig `json:"config"`
}

type savedReportPatchRequest struct {
	Name   *string       `json:"name"`
	Config *ReportConfig `json:"config"`
}

type exportRequest struct {
	ReportKey string            `json:"report_key" validate:"required"`
	Params    map[string]string `json:"params"`
}

type exportJobDTO struct {
	ID           uuid.UUID         `json:"id"`
	Kind         string            `json:"kind"`
	ReportKey    string            `json:"report_key"`
	Params       map[string]string `json:"params"`
	Status       string            `json:"status"`
	RowCount     *int64            `json:"row_count"`
	Error        *string           `json:"error"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at"`
	FinishedAt   *time.Time        `json:"finished_at"`
	ExpiresAt    *time.Time        `json:"expires_at"`
	DownloadPath *string           `json:"download_path"`
}

func toExportDTO(j ExportJob, now time.Time) exportJobDTO {
	out := exportJobDTO{
		ID: j.ID, Kind: j.Kind, ReportKey: j.ReportKey, Params: j.Query, Status: j.Status, RowCount: j.RowCount,
		Error: j.Error, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, ExpiresAt: j.ExpiresAt,
	}
	if out.Params == nil {
		out.Params = map[string]string{}
	}
	if j.Status == ExportSucceeded && j.ExpiresAt != nil && now.Before(*j.ExpiresAt) {
		path := "/api/v1/export-jobs/" + j.ID.String() + "/download"
		out.DownloadPath = &path
	}
	return out
}
