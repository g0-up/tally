// Package analytics is the optional read-only AI Q&A over the store's reports. A question is
// answered by a language model that may only call the report catalogue as tools; the answer cites
// the reports and document numbers it used, and every question is logged in analytics_queries.
// The Q&A runs only when a model is configured, the tenant setting analytics_ai_enabled is on and
// the subscription carries the ai_qa entitlement.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// Error codes.
const (
	CodeFeatureDisabled = "feature_disabled"
	CodeInvalidQuestion = "invalid_question"
	CodeTimeout         = "ai_timeout"
)

// EntitlementKey is the entitlement of the AI Q&A add-on.
const EntitlementKey = "ai_qa"

const (
	maxQuestionRunes = 500
	// maxSteps bounds the model turns of one question.
	maxSteps = 6
	// maxToolRows bounds the rows of a report result the model sees.
	maxToolRows = 50
	// defaultDeadline bounds the whole conversation of one question, below the server's write
	// timeout so the client gets an answer rather than a dropped connection.
	defaultDeadline = 45 * time.Second
)

// Deps are the collaborators of Service.
type Deps struct {
	DB      *db.DB
	Clock   clock.Clock
	Reports Reports
	// Model answers questions; nil leaves the Q&A disabled.
	Model  ModelClient
	Logger *slog.Logger
	// Deadline bounds the conversation of one question; zero means 45 s.
	Deadline time.Duration
}

// Service answers questions over the report catalogue and keeps their log.
type Service struct {
	db       *db.DB
	clock    clock.Clock
	reports  Reports
	model    ModelClient
	logger   *slog.Logger
	deadline time.Duration
}

// NewService returns the service.
func NewService(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Deadline <= 0 {
		d.Deadline = defaultDeadline
	}
	return &Service{db: d.DB, clock: d.Clock, reports: d.Reports, model: d.Model, logger: d.Logger, deadline: d.Deadline}
}

// Citation is a report a answer used, with the parameters it ran with and the row codes or
// document numbers of it the answer names.
type Citation struct {
	ReportKey string            `json:"report_key"`
	Title     string            `json:"title"`
	Params    map[string]string `json:"params"`
	Refs      []string          `json:"refs"`
}

// Query is one logged question. Answer is nil when the model gave none.
type Query struct {
	ID        uuid.UUID
	Question  string
	Answer    *string
	Citations []Citation
	Model     string
	AskedBy   uuid.UUID
	CreatedAt time.Time
}

type queryRow struct {
	ID        uuid.UUID `db:"id"`
	Question  string    `db:"question"`
	Answer    *string   `db:"answer"`
	CitedRefs []byte    `db:"cited_refs"`
	Model     string    `db:"model"`
	AskedBy   uuid.UUID `db:"asked_by"`
	CreatedAt time.Time `db:"created_at"`
}

func (r queryRow) query() (Query, error) {
	q := Query{
		ID: r.ID, Question: r.Question, Answer: r.Answer, Model: r.Model, AskedBy: r.AskedBy, CreatedAt: r.CreatedAt,
		Citations: []Citation{},
	}
	if err := json.Unmarshal(r.CitedRefs, &q.Citations); err != nil {
		return Query{}, fmt.Errorf("analytics: decode citations: %w", err)
	}
	return q, nil
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

func errDisabled(message string) error { return apperr.NewForbidden(CodeFeatureDisabled, message) }

// gate checks that the Q&A may run for the tenant and returns the tenant's business date.
func (s *Service) gate(ctx context.Context, q db.Querier, tenantID uuid.UUID) (time.Time, string, error) {
	var settings struct {
		Timezone string `db:"timezone"`
		Enabled  bool   `db:"analytics_ai_enabled"`
	}
	if err := sqlx.GetContext(ctx, q, &settings, `
		SELECT t.timezone, s.analytics_ai_enabled
		FROM tenants t JOIN tenant_settings s ON s.tenant_id = t.id WHERE t.id = $1`, tenantID); err != nil {
		return time.Time{}, "", fmt.Errorf("analytics: read settings: %w", err)
	}
	if !settings.Enabled {
		return time.Time{}, "", errDisabled("Hỏi đáp AI đang tắt trong cài đặt cửa hàng.")
	}
	loc, err := clock.LoadLocation(settings.Timezone)
	if err != nil {
		return time.Time{}, "", err
	}
	today := clock.BusinessDate(s.clock.Now(), loc)
	var entitled bool
	if err := q.QueryRowxContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM entitlements
			WHERE tenant_id = $1 AND key = $2 AND value = 'true'::jsonb
			  AND valid_from <= $3 AND (valid_to IS NULL OR valid_to >= $3))`,
		tenantID, EntitlementKey, today).Scan(&entitled); err != nil {
		return time.Time{}, "", fmt.Errorf("analytics: read entitlement: %w", err)
	}
	if !entitled {
		return time.Time{}, "", errDisabled("Gói dịch vụ hiện tại chưa có Hỏi đáp AI.")
	}
	return today, settings.Timezone, nil
}

// Ask answers a question and logs it. The model sees the question, the tool catalogue and the
// report results it asks for, without the columns naming people or businesses. A model failure
// is logged as an unanswered question, not returned as an error; a conversation that outlives the
// deadline is logged the same way and answered with ai_timeout. Whether the Q&A may run is
// checked before the question itself.
func (s *Service) Ask(ctx context.Context, scope Scope, actor audit.Actor, question string) (Query, error) {
	if scope.ReadOnly {
		return Query{}, apperr.NewForbidden("support_read_only", "Support sessions cannot ask questions")
	}
	if s.model == nil {
		return Query{}, errDisabled("Hỏi đáp AI chưa được cấu hình trên máy chủ.")
	}
	var today time.Time
	var zone string
	if err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		var err error
		today, zone, err = s.gate(ctx, q, scope.TenantID)
		return err
	}); err != nil {
		return Query{}, err
	}
	question = strings.TrimSpace(question)
	if n := utf8.RuneCountInString(question); n == 0 || n > maxQuestionRunes {
		return Query{}, apperr.NewValidation(CodeInvalidQuestion, "The question must have 1 to 500 characters",
			map[string]string{"question": "length"})
	}

	conv := conversation{model: s.model, reports: s.reports, scope: scope}
	runCtx, cancel := context.WithTimeout(ctx, s.deadline)
	answer, citations, err := conv.run(runCtx, question, today, zone)
	cancel()
	timedOut := false
	if err != nil {
		if ctx.Err() != nil {
			return Query{}, ctx.Err()
		}
		timedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
		s.logger.WarnContext(ctx, "analytics: question not answered", "tenant_id", scope.TenantID, "timed_out", timedOut, "error", err)
	}
	if citations == nil {
		citations = []Citation{}
	}
	refs, err := json.Marshal(citations)
	if err != nil {
		return Query{}, fmt.Errorf("analytics: encode citations: %w", err)
	}
	out := Query{
		ID: uuid.Must(uuid.NewV7()), Question: question, Answer: answer, Citations: citations, Model: s.model.Model(),
		AskedBy: actor.ID, CreatedAt: s.clock.Now(),
	}
	if err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO analytics_queries (id, tenant_id, question, answer, cited_refs, model, asked_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			out.ID, scope.TenantID, out.Question, out.Answer, refs, out.Model, out.AskedBy, out.CreatedAt)
		if err != nil {
			return fmt.Errorf("analytics: log question: %w", err)
		}
		return nil
	}); err != nil {
		return Query{}, err
	}
	if timedOut {
		return Query{}, apperr.NewTimeout(CodeTimeout, "Hỏi đáp AI không trả lời kịp; hãy thử lại hoặc hỏi ngắn gọn hơn.")
	}
	return out, nil
}

// QueryCursor is the keyset position of the query log, newest first.
type QueryCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// List reads the query log, newest first, limit+1 rows after the cursor.
func (s *Service) List(ctx context.Context, scope Scope, after *QueryCursor, limit int) ([]Query, error) {
	var rows []queryRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		var at *time.Time
		var id *uuid.UUID
		if after != nil {
			at, id = &after.CreatedAt, &after.ID
		}
		if err := sqlx.SelectContext(ctx, q, &rows, `
			SELECT id, question, answer, cited_refs, model, asked_by, created_at FROM analytics_queries
			WHERE $1::timestamptz IS NULL OR (created_at, id) < ($1::timestamptz, $2::uuid)
			ORDER BY created_at DESC, id DESC LIMIT $3`, at, id, limit+1); err != nil {
			return fmt.Errorf("analytics: list questions: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Query, len(rows))
	for i, r := range rows {
		if out[i], err = r.query(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// conversation is one question put to the model, with the report catalogue as its tools.
type conversation struct {
	model   ModelClient
	reports Reports
	scope   Scope
	// sent are the report results the model saw, for the citations.
	sent []sentResult
}

type sentResult struct {
	spec   ReportSpec
	params map[string]string
	rows   []map[string]*string
}

const systemPrompt = `You answer questions of a Vietnamese retail store owner about their own store.
Today is %s (time zone %s); "hôm qua" is the day before, "tháng này" is the current month.
Use only the report tools for figures: never guess or invent a number. Run as few reports as needed.
Revenue by sales document: the revenue report with group_by "document" lists document numbers (BH, HD).
Answer in Vietnamese, briefly, with amounts in đồng, and name the document numbers or row codes
(code column) your answer is based on. Columns naming people or businesses are withheld from you.
If the reports cannot answer, say so.`

func (c *conversation) run(ctx context.Context, question string, today time.Time, zone string) (*string, []Citation, error) {
	specs := c.reports.Catalogue()
	byName := make(map[string]ReportSpec, len(specs))
	tools := make([]Tool, 0, len(specs))
	for _, spec := range specs {
		byName[spec.Key] = spec
		tools = append(tools, toolOf(spec))
	}
	msgs := []Message{
		{Role: RoleSystem, Content: fmt.Sprintf(systemPrompt, today.Format(time.DateOnly), zone)},
		{Role: RoleUser, Content: question},
	}
	for range maxSteps {
		reply, err := c.model.Complete(ctx, Request{Messages: msgs, Tools: tools})
		if err != nil {
			return nil, c.citations(""), err
		}
		if len(reply.ToolCalls) == 0 {
			answer := strings.TrimSpace(reply.Content)
			if answer == "" {
				return nil, c.citations(""), errors.New("analytics: empty answer")
			}
			return &answer, c.citations(answer), nil
		}
		msgs = append(msgs, Message{Role: RoleAssistant, Content: reply.Content, ToolCalls: reply.ToolCalls})
		for _, call := range reply.ToolCalls {
			content, err := c.call(ctx, byName, call)
			if err != nil {
				return nil, c.citations(""), err
			}
			msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: content})
		}
	}
	return nil, c.citations(""), errors.New("analytics: no answer within the step limit")
}

// call runs one tool call and returns what the model reads back. A call the report refuses (an
// unknown report, a bad parameter) goes back to the model as an error to correct; anything else
// ends the conversation.
func (c *conversation) call(ctx context.Context, byName map[string]ReportSpec, call ToolCall) (string, error) {
	spec, ok := byName[call.Name]
	if !ok {
		return toolError("unknown tool "+call.Name, nil), nil
	}
	params := map[string]string{}
	for k, v := range call.Arguments {
		if k != "columns" {
			params[k] = v
		}
	}
	res, err := c.reports.Run(ctx, c.scope, spec.Key, params, maxToolRows)
	if err != nil {
		if e, ok := apperr.As(err); ok && (e.Kind == apperr.Validation || e.Kind == apperr.NotFound) {
			return toolError(e.Message, e.Fields), nil
		}
		return "", err
	}
	view := toolView(res)
	c.sent = append(c.sent, sentResult{spec: spec, params: params, rows: view.Rows})
	raw, err := json.Marshal(view)
	if err != nil {
		return "", fmt.Errorf("analytics: encode report result: %w", err)
	}
	return string(raw), nil
}

func toolError(message string, fields map[string]string) string {
	raw, _ := json.Marshal(map[string]any{"error": message, "fields": fields}) // strings always marshal
	return string(raw)
}

// resultView is a report result as the model reads it: no row ids, no personal columns.
type resultView struct {
	Report    string               `json:"report"`
	Title     string               `json:"title"`
	From      *string              `json:"from,omitempty"`
	To        *string              `json:"to,omitempty"`
	AsOf      *string              `json:"as_of,omitempty"`
	Scope     string               `json:"scope"`
	Notes     []string             `json:"notes,omitempty"`
	Columns   []ReportColumn       `json:"columns"`
	Rows      []map[string]*string `json:"rows"`
	Totals    map[string]*string   `json:"totals,omitempty"`
	Summary   []ReportMetric       `json:"summary,omitempty"`
	Truncated bool                 `json:"truncated,omitempty"`
	Withheld  []string             `json:"withheld_columns,omitempty"`
}

func optDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func toolView(r ReportResult) resultView {
	v := resultView{
		Report: r.Key, Title: r.Title, From: optDate(r.From), To: optDate(r.To), AsOf: optDate(r.AsOf), Scope: r.Scope,
		Notes: r.Notes, Totals: r.Totals, Summary: r.Summary, Truncated: r.More, Rows: make([]map[string]*string, 0, len(r.Rows)),
	}
	for _, col := range r.Columns {
		if slices.Contains(r.Personal, col.Key) {
			v.Withheld = append(v.Withheld, col.Key)
			continue
		}
		v.Columns = append(v.Columns, col)
	}
	for i, row := range r.Rows {
		if i == maxToolRows {
			v.Truncated = true
			break
		}
		out := make(map[string]*string, len(v.Columns))
		for _, col := range v.Columns {
			out[col.Key] = row[col.Key]
		}
		v.Rows = append(v.Rows, out)
	}
	if v.Totals != nil {
		totals := make(map[string]*string, len(v.Totals))
		for k, val := range v.Totals {
			if !slices.Contains(r.Personal, k) {
				totals[k] = val
			}
		}
		v.Totals = totals
	}
	return v
}

// refColumns are the row columns holding a code or document number an answer may cite.
var refColumns = []string{"code", "doc_no"}

// citations lists every report the model read, with the codes of its rows the answer names.
func (c *conversation) citations(answer string) []Citation {
	out := make([]Citation, 0, len(c.sent))
	for _, s := range c.sent {
		cit := Citation{ReportKey: s.spec.Key, Title: s.spec.Title, Params: s.params, Refs: []string{}}
		for _, row := range s.rows {
			for _, col := range refColumns {
				v := row[col]
				if v != nil && *v != "" && answer != "" && strings.Contains(answer, *v) && !slices.Contains(cit.Refs, *v) {
					cit.Refs = append(cit.Refs, *v)
				}
			}
		}
		out = append(out, cit)
	}
	return out
}

// toolOf describes a report as a tool: its dates, grouping and filters as JSON schema.
func toolOf(spec ReportSpec) Tool {
	props := map[string]any{}
	required := []string{}
	date := func(desc string) map[string]any {
		return map[string]any{"type": "string", "format": "date", "description": desc}
	}
	switch spec.Range {
	case RangePeriod, RangeFuture:
		props["from"] = date("First day, YYYY-MM-DD")
		props["to"] = date("Last day, YYYY-MM-DD")
	case RangeAsOf:
		props["as_of"] = date("Date the figures are taken at, YYYY-MM-DD (default today)")
	}
	if len(spec.GroupBys) > 0 {
		props["group_by"] = map[string]any{"type": "string", "enum": spec.GroupBys, "description": "Row grouping"}
	}
	for _, f := range spec.Filters {
		p := map[string]any{"type": "string"}
		switch f.Kind {
		case "int":
			p = map[string]any{"type": "integer", "minimum": f.Min, "maximum": f.Max}
		case "enum":
			p["enum"] = f.Values
		case "uuid":
			p["format"] = "uuid"
		}
		props[f.Name] = p
		if f.Required {
			required = append(required, f.Name)
		}
	}
	desc := spec.Title + " (read-only report)"
	if spec.MaxDays > 0 {
		desc += fmt.Sprintf("; a date range spans at most %d days", spec.MaxDays)
	}
	return Tool{
		Name: spec.Key, Description: desc,
		Parameters: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false},
	}
}

// cursorOf is the keyset position of a query in the log.
func cursorOf(q Query) httpx.Cursor {
	return httpx.Cursor{SortKey: q.CreatedAt.UTC().Format(time.RFC3339Nano), ID: q.ID}
}
