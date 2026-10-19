package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func str(s string) *string { return &s }

// fakeReports serves one revenue report by document with a personal name column.
type fakeReports struct {
	runs []map[string]string
}

func (f *fakeReports) Catalogue() []ReportSpec {
	return []ReportSpec{{Key: "revenue", Title: "Doanh thu", Range: RangePeriod, MaxDays: 366,
		GroupBys: []string{"product", "document"},
		Filters:  []FilterSpec{{Name: "days", Kind: "int", Min: 1, Max: 90}}}}
}

func (f *fakeReports) Run(_ context.Context, _ Scope, key string, params map[string]string, _ int) (ReportResult, error) {
	f.runs = append(f.runs, params)
	if params["from"] == "bad" {
		return ReportResult{}, apperr.NewValidation("invalid_report_params", "invalid", map[string]string{"from": "invalid_date"})
	}
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	return ReportResult{
		Key: key, Title: "Doanh thu", From: &day, To: &day, Scope: "posted sales",
		Columns:  []ReportColumn{{Key: "code", Label: "Mã"}, {Key: "name", Label: "Tên"}, {Key: "net_revenue", Label: "Doanh thu thuần"}},
		Personal: []string{"name"},
		Rows: []map[string]*string{
			{"id": str("x"), "code": str("BH-0001"), "name": str("Chị Lan"), "net_revenue": str("100000")},
			{"id": str("y"), "code": str("HD-0002"), "name": str("Anh Minh"), "net_revenue": str("50000")},
			{"id": str("z"), "code": str("BH-0003"), "name": str("Khách lẻ"), "net_revenue": str("1")},
		},
		Totals: map[string]*string{"net_revenue": str("150001")},
	}, nil
}

// scriptedModel first asks for a report with bad parameters, then a valid one, then answers by
// naming the document numbers it read.
type scriptedModel struct {
	requests []Request
}

func (m *scriptedModel) Model() string { return "scripted" }

func (m *scriptedModel) Complete(_ context.Context, req Request) (Reply, error) {
	m.requests = append(m.requests, req)
	switch len(m.requests) {
	case 1:
		return Reply{ToolCalls: []ToolCall{{ID: "c1", Name: "revenue", Arguments: map[string]string{"from": "bad"}}}}, nil
	case 2:
		return Reply{ToolCalls: []ToolCall{{ID: "c2", Name: "revenue",
			Arguments: map[string]string{"from": "2026-10-17", "to": "2026-10-17", "group_by": "document", "columns": "name"}}}}, nil
	}
	last := req.Messages[len(req.Messages)-1]
	var view resultView
	if err := json.Unmarshal([]byte(last.Content), &view); err != nil {
		return Reply{}, err
	}
	return Reply{Content: "Doanh thu hôm qua: " + *view.Rows[0]["code"] + " và " + *view.Rows[1]["code"]}, nil
}

func TestConversationCitesDocumentsAndWithholdsPersonalColumns(t *testing.T) {
	reports := &fakeReports{}
	model := &scriptedModel{}
	conv := conversation{model: model, reports: reports, scope: Scope{TenantID: uuid.New()}}
	answer, citations, err := conv.run(context.Background(), "Doanh thu hôm qua?", time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), "Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	if answer == nil || !strings.Contains(*answer, "BH-0001") {
		t.Fatalf("answer = %v", answer)
	}
	if !strings.Contains(model.requests[0].Messages[0].Content, "Today is 2026-10-18") {
		t.Fatal("the system prompt does not state today")
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "revenue" {
		t.Fatalf("tools = %+v", model.requests[0].Tools)
	}
	if got := model.requests[1].Messages[len(model.requests[1].Messages)-1].Content; !strings.Contains(got, "invalid_date") {
		t.Fatalf("a refused call does not go back to the model: %s", got)
	}
	if _, ok := reports.runs[1]["columns"]; ok {
		t.Fatal("the model chose report columns")
	}
	for _, req := range model.requests {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, "Chị Lan") || strings.Contains(msg.Content, `"id"`) {
				t.Fatalf("a personal column or row id reached the model: %s", msg.Content)
			}
		}
	}
	if len(citations) != 1 || citations[0].ReportKey != "revenue" || citations[0].Params["group_by"] != "document" {
		t.Fatalf("citations = %+v", citations)
	}
	if refs := strings.Join(citations[0].Refs, ","); refs != "BH-0001,HD-0002" {
		t.Fatalf("refs = %s, want the documents the answer names", refs)
	}
}

type failingModel struct{ calls int }

func (m *failingModel) Model() string { return "failing" }

func (m *failingModel) Complete(context.Context, Request) (Reply, error) {
	m.calls++
	if m.calls > 1 {
		return Reply{}, errors.New("upstream down")
	}
	return Reply{ToolCalls: []ToolCall{{ID: "c", Name: "ghost"}}}, nil
}

type loopingModel struct{ calls int }

func (m *loopingModel) Model() string { return "looping" }

func (m *loopingModel) Complete(context.Context, Request) (Reply, error) {
	m.calls++
	return Reply{ToolCalls: []ToolCall{{ID: "c", Name: "revenue", Arguments: map[string]string{}}}}, nil
}

func TestConversationFailures(t *testing.T) {
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	conv := conversation{model: &failingModel{}, reports: &fakeReports{}}
	if answer, _, err := conv.run(context.Background(), "q", today, "UTC"); err == nil || answer != nil {
		t.Fatalf("model failure: answer = %v err = %v", answer, err)
	}
	loop := &loopingModel{}
	conv = conversation{model: loop, reports: &fakeReports{}}
	answer, citations, err := conv.run(context.Background(), "q", today, "UTC")
	if err == nil || answer != nil || loop.calls != maxSteps {
		t.Fatalf("endless tools: answer = %v err = %v calls = %d", answer, err, loop.calls)
	}
	if len(citations) != maxSteps || len(citations[0].Refs) != 0 {
		t.Fatalf("unanswered citations = %+v", citations)
	}
}

func TestToolOf(t *testing.T) {
	tool := toolOf((&fakeReports{}).Catalogue()[0])
	props := tool.Parameters["properties"].(map[string]any)
	for _, k := range []string{"from", "to", "group_by", "days"} {
		if _, ok := props[k]; !ok {
			t.Errorf("tool lacks parameter %s", k)
		}
	}
	if _, ok := props["columns"]; ok {
		t.Error("tool offers columns")
	}
	asOf := toolOf(ReportSpec{Key: "debt_aging", Range: RangeAsOf, Filters: []FilterSpec{{Name: "direction", Kind: "enum", Values: []string{"receivable"}, Required: true}}})
	if p := asOf.Parameters["properties"].(map[string]any); p["as_of"] == nil || p["from"] != nil {
		t.Fatalf("as-of tool = %+v", p)
	}
	if req := asOf.Parameters["required"].([]string); len(req) != 1 || req[0] != "direction" {
		t.Fatalf("required = %v", req)
	}
}

func TestDecodeArguments(t *testing.T) {
	got, err := decodeArguments(`{"from":"2026-10-01","days":7,"flag":true,"none":null}`)
	if err != nil || got["from"] != "2026-10-01" || got["days"] != "7" || got["flag"] != "true" || len(got) != 3 {
		t.Fatalf("args = %v err = %v", got, err)
	}
	if _, err := decodeArguments(`{"x":[1]}`); err == nil {
		t.Fatal("a list argument was accepted")
	}
	if got, err := decodeArguments(" "); err != nil || len(got) != 0 {
		t.Fatalf("empty arguments = %v %v", got, err)
	}
}

func TestNewHTTPModelDisabledWithoutConfig(t *testing.T) {
	if NewHTTPModel(HTTPModelConfig{Model: "m"}) != nil {
		t.Fatal("a model without endpoint is enabled")
	}
	u, _ := url.Parse("http://localhost:1/v1")
	if NewHTTPModel(HTTPModelConfig{BaseURL: u}) != nil {
		t.Fatal("an endpoint without model is enabled")
	}
}

func TestHTTPModelSpeaksChatCompletions(t *testing.T) {
	var got wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"t1","type":"function","function":{"name":"revenue","arguments":"{\"group_by\":\"document\"}"}}]}}]}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/v1/")
	m := NewHTTPModel(HTTPModelConfig{BaseURL: u, APIKey: "secret", Model: "gpt-x", Client: srv.Client()})
	reply, err := m.Complete(context.Background(), Request{
		Messages: []Message{
			{Role: RoleUser, Content: "q"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "nxt", Arguments: map[string]string{"from": "2026-10-01"}}}},
			{Role: RoleTool, ToolCallID: "a", Content: "{}"},
		},
		Tools: []Tool{toolOf((&fakeReports{}).Catalogue()[0])},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Arguments["group_by"] != "document" || m.Model() != "gpt-x" {
		t.Fatalf("reply = %+v", reply)
	}
	if got.Model != "gpt-x" || len(got.Tools) != 1 || got.Messages[1].Content != nil ||
		got.Messages[1].ToolCalls[0].Function.Arguments != `{"from":"2026-10-01"}` || got.Messages[2].ToolCallID != "a" {
		t.Fatalf("request = %+v", got)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer failing.Close()
	u, _ = url.Parse(failing.URL)
	if _, err := NewHTTPModel(HTTPModelConfig{BaseURL: u, Model: "m", Client: failing.Client()}).Complete(context.Background(), Request{}); err == nil ||
		!strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want the HTTP status", err)
	}
}

func TestHTTPModelCallStopsAtItsTimeout(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer slow.Close()
	defer close(release)
	u, _ := url.Parse(slow.URL)
	m := NewHTTPModel(HTTPModelConfig{BaseURL: u, Model: "m", Client: slow.Client(), CallTimeout: 50 * time.Millisecond})
	start := time.Now()
	_, err := m.Complete(context.Background(), Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the call deadline", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("the call waited %s", waited)
	}
}

func TestToQueryDTOFillsEmptyLists(t *testing.T) {
	d := toQueryDTO(Query{Citations: []Citation{{ReportKey: "nxt"}}})
	if d.Citations[0].Params == nil || d.Citations[0].Refs == nil {
		t.Fatalf("dto = %+v", d)
	}
}
