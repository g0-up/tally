package reporting

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Export job statuses (export_jobs.status).
const (
	ExportQueued    = "queued"
	ExportRunning   = "running"
	ExportSucceeded = "succeeded"
	ExportFailed    = "failed"
	ExportExpired   = "expired"
	ExportCancelled = "cancelled"
)

// Export limits.
const (
	// ExportLinkTTL is how long a finished export can be downloaded.
	ExportLinkTTL = 24 * time.Hour
	// ExportDownloadTTL bounds the signed download URL.
	ExportDownloadTTL = 5 * time.Minute
	// exportProgressEvery is how many rows the runner writes between recording its progress, which
	// also checks for a cancellation and refreshes the heartbeat.
	exportProgressEvery = 5000
	// exportStatementTimeout bounds each statement of an export's snapshot transaction instead of
	// DB_STATEMENT_TIMEOUT, which is sized for interactive requests: an export streams the whole
	// result of one statement, and the statement runs until its last row is read. It stays well
	// under exportStaleAfter, so a live export is never taken for interrupted.
	exportStatementTimeout = 10 * time.Minute
	// maxActiveExports bounds the queued and running exports of a tenant.
	maxActiveExports = 5
	// exportStaleAfter is how long a running export may go without a heartbeat before the runner
	// takes it for interrupted.
	exportStaleAfter = time.Hour
	// maxExportBytes is the largest export file attachments accept.
	maxExportBytes = 512 << 20
	// TopicExportFinished is published when an export file is ready.
	TopicExportFinished = "export.finished"
	// CodeExportQueueFull reports a tenant already running maxActiveExports exports.
	CodeExportQueueFull = "export_queue_full"
)

const (
	exportKindReport = "report_csv"
	exportOwnerType  = "export_job"
	exportFailedText = "Không xuất được báo cáo. Hãy thử lại."
	exportTooLarge   = "Tệp xuất vượt quá 512 MB. Hãy thu hẹp khoảng ngày hoặc bộ lọc."
	csvContentType   = "text/csv; charset=utf-8"
)

// utf8BOM lets spreadsheet programs read the CSV as UTF-8.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ExportJob is an asynchronous CSV export of a report. RowCount is the rows written so far while
// running, the total once finished.
type ExportJob struct {
	ID           uuid.UUID
	Kind         string
	ReportKey    string
	Query        map[string]string
	Status       string
	RowCount     *int64
	Error        *string
	AttachmentID *uuid.UUID
	RequestedBy  uuid.UUID
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	ExpiresAt    *time.Time
}

// exportParams is export_jobs.params of a report export.
type exportParams struct {
	ReportKey string            `json:"report_key"`
	Query     map[string]string `json:"query"`
}

type exportRow struct {
	ID           uuid.UUID  `db:"id"`
	Kind         string     `db:"kind"`
	Params       []byte     `db:"params"`
	Status       string     `db:"status"`
	RowCount     *int64     `db:"row_count"`
	Error        *string    `db:"error"`
	AttachmentID *uuid.UUID `db:"attachment_id"`
	RequestedBy  uuid.UUID  `db:"requested_by"`
	CreatedAt    time.Time  `db:"created_at"`
	StartedAt    *time.Time `db:"started_at"`
	FinishedAt   *time.Time `db:"finished_at"`
	ExpiresAt    *time.Time `db:"expires_at"`
}

const exportColumns = `id, kind, params, status, row_count, error, attachment_id, requested_by, created_at,
	started_at, finished_at, expires_at`

func (r exportRow) job() (ExportJob, error) {
	j := ExportJob{
		ID: r.ID, Kind: r.Kind, Status: r.Status, RowCount: r.RowCount, Error: r.Error, AttachmentID: r.AttachmentID,
		RequestedBy: r.RequestedBy, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		ExpiresAt: r.ExpiresAt, Query: map[string]string{},
	}
	if r.Kind != exportKindReport {
		return j, nil
	}
	var p exportParams
	if err := json.Unmarshal(r.Params, &p); err != nil {
		return ExportJob{}, fmt.Errorf("reporting: decode export params: %w", err)
	}
	j.ReportKey = p.ReportKey
	if p.Query != nil {
		j.Query = p.Query
	}
	return j, nil
}

func loadExport(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (ExportJob, error) {
	query := `SELECT ` + exportColumns + ` FROM export_jobs WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	var r exportRow
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&r); err != nil {
		if db.IsNoRows(err) {
			return ExportJob{}, errNotFound("export job")
		}
		return ExportJob{}, fmt.Errorf("reporting: read export job: %w", err)
	}
	return r.job()
}

// CreateExport queues a CSV export of the report named by key or slug with raw query
// parameters. The resolved parameters are stored, so the file holds the dates the owner saw.
func (s *Service) CreateExport(ctx context.Context, scope Scope, actor audit.Actor, name string, raw map[string]string) (ExportJob, error) {
	rep, ok := s.registry.Lookup(name)
	if !ok {
		return ExportJob{}, errInvalid(map[string]string{"report_key": "invalid"})
	}
	today, err := s.Today(ctx, scope)
	if err != nil {
		return ExportJob{}, err
	}
	p, err := rep.resolve(raw, today)
	if err != nil {
		return ExportJob{}, err
	}
	params := exportParams{ReportKey: rep.Key, Query: rep.canonical(p)}
	encoded, err := json.Marshal(params)
	if err != nil {
		return ExportJob{}, fmt.Errorf("reporting: encode export params: %w", err)
	}
	job := ExportJob{
		ID: ids.New(), Kind: exportKindReport, ReportKey: rep.Key, Query: params.Query, Status: ExportQueued,
		RequestedBy: actor.ID, CreatedAt: s.clock.Now(),
	}
	err = s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		// Concurrent requests of a tenant queue one at a time, so they cannot all pass the count.
		if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('export_quota:' || $1::text, 0))`,
			scope.TenantID); err != nil {
			return fmt.Errorf("reporting: lock export quota: %w", err)
		}
		var active int
		if err := q.QueryRowxContext(ctx, `
			SELECT count(*) FROM export_jobs WHERE status IN ('queued', 'running')`).Scan(&active); err != nil {
			return fmt.Errorf("reporting: count active exports: %w", err)
		}
		if active >= maxActiveExports {
			return apperr.NewConflict(CodeExportQueueFull, "Too many exports are already running; wait for one to finish")
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO export_jobs (id, tenant_id, kind, params, status, requested_by, created_at)
			VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, 'queued', $4, $5)`,
			job.ID, job.Kind, encoded, job.RequestedBy, job.CreatedAt); err != nil {
			return fmt.Errorf("reporting: insert export job: %w", err)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "export_job.requested", EntityType: "export_job", EntityID: job.ID, After: params,
		})
	})
	if err != nil {
		return ExportJob{}, err
	}
	return job, nil
}

// GetExport reads an export job.
func (s *Service) GetExport(ctx context.Context, scope Scope, id uuid.UUID) (ExportJob, error) {
	var job ExportJob
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		job, err = loadExport(ctx, q, id, false)
		return err
	})
	return job, err
}

// ExportFilter is a page of one user's export jobs, newest first.
type ExportFilter struct {
	RequestedBy uuid.UUID
	// After is the keyset position (created_at, id) of the last row of the previous page.
	AfterCreatedAt *time.Time
	AfterID        uuid.UUID
	Limit          int
}

// ListExports lists the export jobs a user requested, report CSVs and full data exports alike,
// newest first.
func (s *Service) ListExports(ctx context.Context, scope Scope, f ExportFilter) ([]ExportJob, error) {
	var rows []exportRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &rows, `SELECT `+exportColumns+` FROM export_jobs
			WHERE requested_by = $1 AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3::uuid))
			ORDER BY created_at DESC, id DESC LIMIT $4`, f.RequestedBy, f.AfterCreatedAt, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("reporting: list export jobs: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]ExportJob, len(rows))
	for i, r := range rows {
		if out[i], err = r.job(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CancelExport stops a queued or running export; the runner checks as it writes and discards
// what it wrote.
func (s *Service) CancelExport(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID) (ExportJob, error) {
	var job ExportJob
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		var err error
		if job, err = loadExport(ctx, q, id, true); err != nil {
			return err
		}
		if job.Status != ExportQueued && job.Status != ExportRunning {
			return apperr.NewConflict(CodeExportState, "Only a queued or running export can be cancelled")
		}
		before := job.Status
		now := s.clock.Now()
		if _, err := q.ExecContext(ctx, `UPDATE export_jobs SET status = 'cancelled', finished_at = $2 WHERE id = $1`, id, now); err != nil {
			return fmt.Errorf("reporting: cancel export job: %w", err)
		}
		job.Status, job.FinishedAt = ExportCancelled, &now
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "export_job.cancelled", EntityType: "export_job", EntityID: id,
			Before: map[string]string{"status": before}, After: map[string]string{"status": ExportCancelled},
		})
	})
	if err != nil {
		return ExportJob{}, err
	}
	return job, nil
}

// DownloadURL signs a short-lived URL of a finished export's file. An expired export is gone.
func (s *Service) DownloadURL(ctx context.Context, scope Scope, id uuid.UUID) (string, error) {
	if s.objects == nil {
		return "", errors.New("reporting: object store is not configured")
	}
	var key, fileName string
	err := s.read(ctx, scope, func(q db.Querier) error {
		job, err := loadExport(ctx, q, id, false)
		if err != nil {
			return err
		}
		if job.Status == ExportExpired || (job.ExpiresAt != nil && !s.clock.Now().Before(*job.ExpiresAt)) {
			return apperr.NewGone(CodeExportExpired, "The export link has expired; export again")
		}
		if job.Status != ExportSucceeded || job.AttachmentID == nil {
			return apperr.NewConflict(CodeExportNotDone, "The export is not finished")
		}
		if err := q.QueryRowxContext(ctx, `SELECT storage_key, file_name FROM attachments WHERE id = $1`,
			*job.AttachmentID).Scan(&key, &fileName); err != nil {
			return fmt.Errorf("reporting: read export file: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	url, err := s.objects.PresignGet(ctx, key, fileName, ExportDownloadTTL)
	if err != nil {
		return "", fmt.Errorf("reporting: sign export download: %w", err)
	}
	return url, nil
}

// RunExports runs the queued report exports of a tenant, oldest first, and returns how many
// finished. It first fails the exports a crashed runner left running and expires the finished
// ones past their link, deleting their files. A failed export is marked failed and the others
// still run; the failures are returned joined. A retention failure is returned with them but
// holds up no queued export: a file the store refuses to delete would otherwise stop the
// tenant's exports on every run until someone fixes the bucket.
func (s *Service) RunExports(ctx context.Context, tenantID uuid.UUID, logger *slog.Logger) (int, error) {
	if s.objects == nil {
		return 0, nil
	}
	done := 0
	var errs []error
	if _, err := s.ExpireExports(ctx, tenantID); err != nil {
		errs = append(errs, err)
	}
	for ctx.Err() == nil {
		job, ok, err := s.claimExport(ctx, tenantID)
		if err != nil {
			return done, errors.Join(append(errs, err)...)
		}
		if !ok {
			break
		}
		finished, err := s.runExport(ctx, tenantID, job)
		if err != nil {
			errs = append(errs, fmt.Errorf("export %s: %w", job.ID, err))
			if markErr := s.failExport(ctx, tenantID, job.ID, failureText(err)); markErr != nil {
				errs = append(errs, markErr)
			}
			continue
		}
		if finished {
			done++
			logger.Info("export finished", "tenant_id", tenantID.String(), "export_job_id", job.ID.String())
		}
	}
	return done, errors.Join(errs...)
}

// errExportTooLarge stops an export whose file outgrew what attachments accept.
var errExportTooLarge = errors.New("reporting: export exceeds the attachment size limit")

func failureText(err error) string {
	if errors.Is(err, errExportTooLarge) {
		return exportTooLarge
	}
	return exportFailedText
}

// ExpireExports fails the exports a crashed runner left running and expires the finished ones
// past their link, returning how many it expired. An expired export loses its attachment row
// with its file. A file is deleted before its job is marked expired, so a failed deletion is
// retried on the next run instead of leaving the file behind; the link is refused from its
// expiry on either way. It needs the object store.
func (s *Service) ExpireExports(ctx context.Context, tenantID uuid.UUID) (int, error) {
	if s.objects == nil {
		return 0, errors.New("reporting: expiring exports needs the object store")
	}
	now := s.clock.Now()
	type expiring struct {
		ID  uuid.UUID `db:"id"`
		Key string    `db:"storage_key"`
	}
	var due []expiring
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET status = 'failed', error = $2, finished_at = $1
			WHERE status = 'running' AND COALESCE(heartbeat_at, started_at) < $3`, now, exportFailedText, now.Add(-exportStaleAfter)); err != nil {
			return fmt.Errorf("reporting: fail interrupted exports: %w", err)
		}
		if err := sqlx.SelectContext(ctx, q, &due, `
			SELECT e.id, a.storage_key FROM export_jobs e JOIN attachments a ON a.id = e.attachment_id
			WHERE e.status = 'succeeded' AND e.expires_at <= $1`, now); err != nil {
			return fmt.Errorf("reporting: find expired exports: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	var errs []error
	deleted := make([]uuid.UUID, 0, len(due))
	for _, d := range due {
		if err := s.objects.Delete(ctx, d.Key); err != nil {
			errs = append(errs, fmt.Errorf("reporting: delete expired export file: %w", err))
			continue
		}
		deleted = append(deleted, d.ID)
	}
	expired := 0
	if len(deleted) > 0 {
		if err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
			// The attachment row of a deleted file goes with it: the storage quota sums the
			// attachments, and the file no longer takes any space.
			var attachments []uuid.UUID
			if err := sqlx.SelectContext(ctx, q, &attachments, `
				UPDATE export_jobs e SET status = 'expired', attachment_id = NULL
				FROM export_jobs old
				WHERE old.id = e.id AND e.id = ANY($1) AND e.status = 'succeeded'
				RETURNING old.attachment_id`, deleted); err != nil {
				return fmt.Errorf("reporting: expire exports: %w", err)
			}
			if _, err := q.ExecContext(ctx, `DELETE FROM attachments WHERE id = ANY($1)`, attachments); err != nil {
				return fmt.Errorf("reporting: delete expired export attachments: %w", err)
			}
			expired = len(attachments)
			return nil
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return expired, errors.Join(errs...)
}

// claimExport moves the oldest queued report export to running.
func (s *Service) claimExport(ctx context.Context, tenantID uuid.UUID) (ExportJob, bool, error) {
	var job ExportJob
	found := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var r exportRow
		err := q.QueryRowxContext(ctx, `
			UPDATE export_jobs SET status = 'running', started_at = $1, row_count = 0
			WHERE id = (
				SELECT id FROM export_jobs WHERE status = 'queued' AND kind = 'report_csv'
				ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
			)
			RETURNING `+exportColumns, s.clock.Now()).StructScan(&r)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reporting: claim export job: %w", err)
		}
		found = true
		job, err = r.job()
		return err
	})
	return job, found, err
}

func (s *Service) failExport(ctx context.Context, tenantID, id uuid.UUID, reason string) error {
	return s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET status = 'failed', error = $2, finished_at = $3
			WHERE id = $1 AND status = 'running'`, id, reason, s.clock.Now()); err != nil {
			return fmt.Errorf("reporting: fail export job: %w", err)
		}
		return nil
	})
}

// stillRunning records the progress and heartbeat of a running export and reports whether it was
// not cancelled meanwhile.
func (s *Service) stillRunning(ctx context.Context, tenantID, id uuid.UUID, rows int64) (bool, error) {
	running := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		res, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET row_count = $2, heartbeat_at = $3 WHERE id = $1 AND status = 'running'`,
			id, rows, s.clock.Now())
		if err != nil {
			return fmt.Errorf("reporting: record export progress: %w", err)
		}
		n, err := res.RowsAffected()
		running = n == 1
		return err
	})
	return running, err
}

// errExportCancelled stops the writing of an export that was cancelled meanwhile.
var errExportCancelled = errors.New("reporting: export cancelled")

// runExport writes the report of a claimed job into a temporary CSV file, stores it and finishes
// the job. It reports false when the job was cancelled meanwhile.
//
// The report runs once in one REPEATABLE READ READ ONLY transaction, so every row and the totals
// come from the same snapshot; a paged report streams its rows straight into the file instead of
// being read page by page. Progress is recorded in transactions of its own while the snapshot is
// open. Debt reports read their debt part through the debt domain, in a transaction of its own.
func (s *Service) runExport(ctx context.Context, tenantID uuid.UUID, job ExportJob) (bool, error) {
	rep, ok := s.registry.Lookup(job.ReportKey)
	if !ok {
		return false, fmt.Errorf("reporting: export of unknown report %q", job.ReportKey)
	}
	// The stored parameters are already resolved, so the date of the run does not matter.
	p, err := rep.resolve(job.Query, s.clock.Now())
	if err != nil {
		return false, fmt.Errorf("reporting: resolve export params: %w", err)
	}
	if running, err := s.stillRunning(ctx, tenantID, job.ID, 0); err != nil || !running {
		return false, err
	}
	f, err := os.CreateTemp("", "tally-export-*.csv")
	if err != nil {
		return false, fmt.Errorf("reporting: create export file: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	hash := sha256.New()
	out := NewCSVWriter(io.MultiWriter(f, hash))
	if err := out.Begin(project(Table{Columns: rep.Columns}, p.Columns).Columns); err != nil {
		return false, err
	}
	write := func(r Row) error {
		if err := out.WriteRow(r); err != nil {
			return err
		}
		if out.Rows()%exportProgressEvery != 0 {
			return nil
		}
		if out.Bytes() > maxExportBytes {
			return errExportTooLarge
		}
		running, err := s.stillRunning(ctx, tenantID, job.ID, out.Rows())
		if err != nil {
			return err
		}
		if !running {
			return errExportCancelled
		}
		return nil
	}
	err = s.db.WithSnapshotTenantTx(ctx, tenantID, exportStatementTimeout, func(q db.Querier) error {
		p.sink = write
		t, err := s.runResolved(ctx, Scope{TenantID: tenantID, q: q}, rep, p)
		if err != nil {
			return err
		}
		for _, r := range t.Rows {
			if err := write(r); err != nil {
				return err
			}
		}
		return out.WriteTotals(t.Totals)
	})
	if errors.Is(err, errExportCancelled) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := out.Flush(); err != nil {
		return false, err
	}
	size := out.Bytes()
	if size > maxExportBytes {
		return false, errExportTooLarge
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("reporting: rewind export file: %w", err)
	}
	key := objectstore.TenantKey(tenantID, "exports", job.ID.String()+".csv")
	if err := s.objects.Put(ctx, key, f, size, csvContentType); err != nil {
		return false, fmt.Errorf("reporting: store export file: %w", err)
	}
	finished, err := s.finishExport(ctx, tenantID, job, key, exportFileName(rep, p), size, hash.Sum(nil), out.Rows())
	if err != nil || !finished {
		if delErr := s.objects.Delete(ctx, key); delErr != nil {
			err = errors.Join(err, fmt.Errorf("reporting: delete unused export file: %w", delErr))
		}
	}
	return finished, err
}

func (s *Service) finishExport(ctx context.Context, tenantID uuid.UUID, job ExportJob, key, fileName string,
	size int64, sum []byte, rows int64) (bool, error) {
	finished := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		cur, err := loadExport(ctx, q, job.ID, true)
		if err != nil {
			return err
		}
		if cur.Status != ExportRunning {
			return nil
		}
		now := s.clock.Now()
		attachmentID := ids.New()
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attachments (id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type,
				size_bytes, sha256, scan_status, uploaded_by, created_at)
			VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7, $8, 'clean', $9, $10)`,
			attachmentID, exportOwnerType, job.ID, key, fileName, csvContentType, size, sum, job.RequestedBy, now); err != nil {
			return fmt.Errorf("reporting: insert export attachment: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET status = 'succeeded', attachment_id = $2, row_count = $3, finished_at = $4,
				expires_at = $5, error = NULL
			WHERE id = $1`, job.ID, attachmentID, rows, now, now.Add(ExportLinkTTL)); err != nil {
			return fmt.Errorf("reporting: finish export job: %w", err)
		}
		finished = true
		if _, err := s.publisher.Publish(ctx, q, tenantID, outbox.Event{
			Topic: TopicExportFinished, Payload: map[string]string{"export_job_id": job.ID.String()},
		}); err != nil {
			return fmt.Errorf("reporting: publish export finished: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return finished, nil
}

// exportFileName names the file after the report and its dates.
func exportFileName(rep Report, p Params) string {
	name := rep.Slug()
	switch rep.Range {
	case rangePeriod, rangeFuture:
		name += "-" + p.From.Format(time.DateOnly) + "-" + p.To.Format(time.DateOnly)
	case rangeAsOf:
		name += "-" + p.AsOf.Format(time.DateOnly)
	}
	return name + ".csv"
}

// CSVWriter writes report tables as CSV for spreadsheet programs: a UTF-8 byte order mark, one
// header row of column labels, the rows, and the totals at the end. Text cells that a spreadsheet
// would run as a formula are prefixed with an apostrophe.
type CSVWriter struct {
	w       io.Writer
	csv     *csv.Writer
	counter *countingWriter
	columns []Column
	rows    int64
}

// NewCSVWriter returns a writer to w.
func NewCSVWriter(w io.Writer) *CSVWriter { return &CSVWriter{w: w} }

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// WriteTable writes the rows of one page of t; the first page also writes the byte order mark
// and the header, and last writes the totals.
func (c *CSVWriter) WriteTable(t Table, last bool) error {
	if c.csv == nil {
		if err := c.Begin(t.Columns); err != nil {
			return err
		}
	}
	for _, r := range t.Rows {
		if err := c.WriteRow(r); err != nil {
			return err
		}
	}
	if last {
		if err := c.WriteTotals(t.Totals); err != nil {
			return err
		}
	}
	return c.Flush()
}

// Begin writes the byte order mark and the header of columns, which every row then follows.
func (c *CSVWriter) Begin(columns []Column) error {
	c.counter = &countingWriter{w: c.w}
	if _, err := c.counter.Write(utf8BOM); err != nil {
		return fmt.Errorf("reporting: write csv: %w", err)
	}
	c.csv = csv.NewWriter(c.counter)
	c.columns = columns
	header := make([]string, len(c.columns))
	for i, col := range c.columns {
		header[i] = col.Label
	}
	if err := c.csv.Write(header); err != nil {
		return fmt.Errorf("reporting: write csv: %w", err)
	}
	return nil
}

// WriteRow writes one table row; it is buffered until the next Flush.
func (c *CSVWriter) WriteRow(r Row) error {
	if err := c.csv.Write(c.record(r)); err != nil {
		return fmt.Errorf("reporting: write csv: %w", err)
	}
	c.rows++
	return nil
}

// WriteTotals writes the totals row, labelled in its first cell when that is empty. Empty totals
// write nothing.
func (c *CSVWriter) WriteTotals(totals Row) error {
	if len(totals) == 0 || len(c.columns) == 0 {
		return nil
	}
	rec := c.record(totals)
	if rec[0] == "" {
		rec[0] = "Tổng cộng"
	}
	if err := c.csv.Write(rec); err != nil {
		return fmt.Errorf("reporting: write csv: %w", err)
	}
	return nil
}

func (c *CSVWriter) record(r Row) []string {
	rec := make([]string, len(c.columns))
	for i, col := range c.columns {
		v := r[col.Key]
		if v == nil {
			continue
		}
		rec[i] = *v
		if col.Kind == KindText && strings.ContainsAny(firstChar(*v), "=+-@\t\r") {
			rec[i] = "'" + *v
		}
	}
	return rec
}

func firstChar(s string) string {
	if s == "" {
		return ""
	}
	return s[:1]
}

// Flush writes buffered data; WriteTable already flushes after every page.
func (c *CSVWriter) Flush() error {
	if c.csv == nil {
		return nil
	}
	c.csv.Flush()
	if err := c.csv.Error(); err != nil {
		return fmt.Errorf("reporting: write csv: %w", err)
	}
	return nil
}

// Rows is the number of table rows written, totals excluded.
func (c *CSVWriter) Rows() int64 { return c.rows }

// Bytes is the number of bytes written through; rows buffered since the last Flush are not
// counted yet.
func (c *CSVWriter) Bytes() int64 {
	if c.counter == nil {
		return 0
	}
	return c.counter.n
}
