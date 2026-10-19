//go:build integration

package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// fakeBucket is an S3 endpoint that stores nothing: it records every key put or deleted and
// refuses to delete the keys holding "locked" until unlocked.
type fakeBucket struct {
	mu       sync.Mutex
	put      []string
	deleted  []string
	unlocked atomic.Bool
}

func (b *fakeBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutPrefix(r.URL.Path, "/tally-dev/")
	if ok && r.Method == http.MethodPut {
		_, _ = io.Copy(io.Discard, r.Body)
		b.mu.Lock()
		b.put = append(b.put, key)
		b.mu.Unlock()
		w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodDelete || !ok {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	if strings.Contains(key, "locked") && !b.unlocked.Load() {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
		return
	}
	b.mu.Lock()
	b.deleted = append(b.deleted, key)
	b.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (b *fakeBucket) deletedKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Sorted(slices.Values(b.deleted))
}

// insertExport adds an export job of the tenant; a job with a file name gets an attachment
// stored under that name.
func insertExport(t *testing.T, super *sqlx.DB, tenantID uuid.UUID, status, file string, startedAt, expiresAt *time.Time) (uuid.UUID, string) {
	t.Helper()
	job, now := ids.New(), time.Now().UTC()
	var attachment *uuid.UUID
	var key string
	if file != "" {
		id := ids.New()
		attachment, key = &id, objectstore.TenantKey(tenantID, "exports", file)
		if _, err := super.Exec(`
			INSERT INTO tally.attachments (id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type, size_bytes, sha256, uploaded_by, created_at)
			VALUES ($1, $2, 'export_job', $3, $4, $5, 'text/csv', 10, decode('00', 'hex'), $6, $7)`,
			id, tenantID, job, key, file, ids.New(), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := super.Exec(`
		INSERT INTO tally.export_jobs (id, tenant_id, kind, params, status, attachment_id, requested_by, created_at, started_at, expires_at)
		VALUES ($1, $2, 'report_csv', '{}', $3, $4, $5, $6, $7, $8)`,
		job, tenantID, status, attachment, ids.New(), now, startedAt, expiresAt); err != nil {
		t.Fatal(err)
	}
	return job, key
}

func putKeys(b *fakeBucket) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.put)
}

// attachmentsOf counts the attachment rows a tenant's storage quota sums.
func attachmentsOf(t *testing.T, super *sqlx.DB, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := super.Get(&n, `SELECT count(*) FROM tally.attachments WHERE tenant_id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}
	return n
}

func exportStatus(t *testing.T, super *sqlx.DB, job uuid.UUID) string {
	t.Helper()
	var status string
	if err := super.Get(&status, `SELECT status FROM tally.export_jobs WHERE id = $1`, job); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestMaintenanceExpireExports(t *testing.T) {
	bucket := &fakeBucket{}
	srv := httptest.NewServer(bucket)
	t.Cleanup(srv.Close)
	env := serviceEnv(t)
	env["OBJECT_STORE_ENDPOINT"] = srv.URL

	pool, super := testkit.DB(t), testkit.SuperDB(t)
	a, b := testkit.NewTenant(t, pool), testkit.NewTenant(t, pool)
	past, future, stale := time.Now().Add(-time.Hour), time.Now().Add(time.Hour), time.Now().Add(-2*time.Hour)

	expiredA, keyA := insertExport(t, super, a.ID, "succeeded", "a.csv", &stale, &past)
	current, _ := insertExport(t, super, a.ID, "succeeded", "current.csv", &stale, &future)
	interrupted, _ := insertExport(t, super, a.ID, "running", "", &stale, nil)
	locked, keyLocked := insertExport(t, super, a.ID, "succeeded", "locked.csv", &stale, &past)
	expiredB, keyB := insertExport(t, super, b.ID, "succeeded", "b.csv", &stale, &past)

	// The file that cannot be deleted keeps its export for the next run and fails the command;
	// the other tenant's exports still expire.
	out, err := run(t, env, "maintenance", "expire-exports")
	if err == nil || !strings.Contains(err.Error(), "delete expired export file") {
		t.Errorf("expire-exports with an undeletable file = %v, want a deletion failure", err)
	}
	if !strings.Contains(out, "expired 2 exports") {
		t.Errorf("output = %q, want 2 expired", out)
	}
	for job, want := range map[uuid.UUID]string{
		expiredA: "expired", expiredB: "expired", current: "succeeded", interrupted: "failed", locked: "succeeded",
	} {
		if got := exportStatus(t, super, job); got != want {
			t.Errorf("export %s status = %s, want %s", job, got, want)
		}
	}
	if got, want := bucket.deletedKeys(), slices.Sorted(slices.Values([]string{keyA, keyB})); !slices.Equal(got, want) {
		t.Errorf("deleted keys = %v, want %v", got, want)
	}
	// An expired export no longer counts toward the storage quota; the file still stored does.
	if n := attachmentsOf(t, super, a.ID); n != 2 {
		t.Errorf("attachments of tenant A = %d, want the current and the undeletable export's", n)
	}
	if n := attachmentsOf(t, super, b.ID); n != 0 {
		t.Errorf("attachments of tenant B = %d, want 0", n)
	}

	bucket.unlocked.Store(true)
	if out, err := run(t, env, "maintenance", "expire-exports"); err != nil || !strings.Contains(out, "expired 1 exports") {
		t.Errorf("retry = %q, %v; want the remaining export expired", out, err)
	}
	if got := exportStatus(t, super, locked); got != "expired" {
		t.Errorf("retried export status = %s, want expired", got)
	}
	if n := attachmentsOf(t, super, a.ID); n != 1 {
		t.Errorf("attachments of tenant A after the retry = %d, want the current export's", n)
	}
	if got := bucket.deletedKeys(); !slices.Contains(got, keyLocked) || len(got) != 3 {
		t.Errorf("deleted keys after the retry = %v", got)
	}
	if out, err := run(t, env, "maintenance", "expire-exports"); err != nil || !strings.Contains(out, "expired 0 exports") {
		t.Errorf("run with nothing due = %q, %v", out, err)
	}
}

// The worker expires files before it runs the queued exports; a file the store refuses to
// delete is reported but holds up none of them.
func TestRunExportsDespiteAnUndeletableExpiredFile(t *testing.T) {
	bucket := &fakeBucket{}
	srv := httptest.NewServer(bucket)
	t.Cleanup(srv.Close)
	endpoint, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: "tally-dev", AccessKey: "tally-dev", SecretKey: "tally-dev-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	pool, super := testkit.DB(t), testkit.SuperDB(t)
	clk := clock.System{}
	reports := app.NewDomains(pool, clk, audit.NewRecorder(clk), app.WithObjectStore(objects)).Reporting
	tn := testkit.NewTenant(t, pool)
	past, stale := time.Now().Add(-time.Hour), time.Now().Add(-2*time.Hour)
	locked, _ := insertExport(t, super, tn.ID, "succeeded", "locked.csv", &stale, &past)
	queued := ids.New()
	if _, err := super.Exec(`
		INSERT INTO tally.export_jobs (id, tenant_id, kind, params, status, requested_by, created_at)
		VALUES ($1, $2, 'report_csv', '{"report_key": "revenue", "query": {}}', 'queued', $3, now())`,
		queued, tn.ID, ids.New()); err != nil {
		t.Fatal(err)
	}

	n, err := reports.RunExports(context.Background(), tn.ID, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "delete expired export file") {
		t.Errorf("run exports = %v, want the deletion failure reported", err)
	}
	if n != 1 {
		t.Errorf("exports finished = %d, want 1", n)
	}
	for job, want := range map[uuid.UUID]string{queued: "succeeded", locked: "succeeded"} {
		if got := exportStatus(t, super, job); got != want {
			t.Errorf("export %s status = %s, want %s", job, got, want)
		}
	}
	if got := putKeys(bucket); len(got) != 1 {
		t.Errorf("stored files = %v, want the queued export's", got)
	}
}
