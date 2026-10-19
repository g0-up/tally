package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerExposesTallyMetrics(t *testing.T) {
	m := New()
	m.ObserveHTTP("GET", "/api/v1/skus/:id", 200, 20*time.Millisecond)
	m.ObserveHTTP("GET", "", 404, time.Millisecond)
	m.ObservePosting("receipt.post", "ok", 30*time.Millisecond)
	m.SetOutboxLag(-time.Second)
	m.SetOutboxLag(3 * time.Second)
	m.CountOutbox("stock.changed", "processed")

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`tally_http_requests_total{method="GET",route="/api/v1/skus/:id",status="200"} 1`,
		`tally_http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`tally_http_request_duration_seconds_count{method="GET",route="/api/v1/skus/:id"} 1`,
		`tally_posting_duration_seconds_count{outcome="ok",scope="receipt.post"} 1`,
		`tally_outbox_lag_seconds 3`,
		`tally_outbox_events_total{outcome="processed",topic="stock.changed"} 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}

func TestNilMetricsIsNoop(t *testing.T) {
	var m *Metrics
	m.ObserveHTTP("GET", "/", 200, time.Millisecond)
	m.ObservePosting("x", "ok", time.Millisecond)
	m.SetOutboxLag(time.Second)
	m.CountOutbox("x", "dead")
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil handler status = %d", rec.Code)
	}
}
