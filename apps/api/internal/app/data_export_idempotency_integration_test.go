//go:build integration

package app_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// A data export request retried with the same Idempotency-Key answers the job it queued without
// queueing another, still behind a fresh password; the key sent with another body is refused.
func TestDataExportRequestRunsOncePerKey(t *testing.T) {
	s := newControlStack(t)
	b := s.signIn(t, s.signUp(t))
	tenant := tenantOf(t, b)
	jobs := func() int {
		return countRows(t, tenant, `SELECT count(*) FROM export_jobs WHERE kind = 'full_data_export'`)
	}
	requested := func() int {
		return countRows(t, tenant, `SELECT count(*) FROM audit_events WHERE action = 'export_job.requested'`)
	}

	key := http.Header{"Idempotency-Key": {ids.New().String()}}
	first := b.doWith(http.MethodPost, v1+"/data-exports", nil, key)
	expect(t, first, http.StatusAccepted, "queued")
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Error("the first request is flagged as a replay")
	}
	job := jsonOf(t, first)

	again := b.doWith(http.MethodPost, v1+"/data-exports", nil, key)
	expect(t, again, http.StatusAccepted, "queued")
	if again.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("retry is not flagged as a replay: %v", again.Header())
	}
	if got := jsonOf(t, again); !reflect.DeepEqual(got, job) {
		t.Errorf("replayed body differs:\n first: %s\nreplay: %s", first.Body, again.Body)
	}
	if n := jobs(); n != 1 {
		t.Errorf("export jobs after a retry = %d, want 1", n)
	}
	if n := requested(); n != 1 {
		t.Errorf("export requested audit events after a retry = %d, want 1", n)
	}

	expect(t, b.doWith(http.MethodPost, v1+"/data-exports", map[string]any{"reason": "other"}, key),
		http.StatusConflict, idempotency.CodeKeyReused)
	s.clk.Advance(middleware.ReauthWindow + time.Minute)
	expect(t, b.doWith(http.MethodPost, v1+"/data-exports", nil, key), http.StatusForbidden, "reauth_required")
	if n := jobs(); n != 1 {
		t.Errorf("export jobs after a reused key and a stale password = %d, want 1", n)
	}

	expect(t, b.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
	expect(t, b.doWith(http.MethodPost, v1+"/data-exports", nil, key), http.StatusAccepted, "queued")
	// Without a key every request queues.
	expect(t, b.do(http.MethodPost, v1+"/data-exports", nil), http.StatusAccepted, "queued")
	if n := jobs(); n != 2 {
		t.Errorf("export jobs after an unkeyed request = %d, want 2", n)
	}
}
