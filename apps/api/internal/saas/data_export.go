package saas

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Full data export limits and texts. The export shares export_jobs, its quota and the download
// route with report exports.
const (
	// DataExportKind is export_jobs.kind of a full data export.
	DataExportKind = "full_data_export"
	// DataExportLinkTTL is how long a full export can be downloaded; longer than a report export,
	// since a closing store needs time to take its data.
	DataExportLinkTTL = 7 * 24 * time.Hour
	// CodeExportQueueFull reports a tenant already running its maximum of exports.
	CodeExportQueueFull = "export_queue_full"
	// CodeExportNotAllowed reports a store whose subscription no longer allows exports.
	CodeExportNotAllowed = "export_not_allowed"

	maxActiveExports       = 5
	maxDataExportBytes     = 512 << 20
	dataExportProgressRows = 5000
	dataExportTimeout      = 30 * time.Minute
	zipContentType         = "application/zip"
	dataExportFailedText   = "Không xuất được toàn bộ dữ liệu. Hãy thử lại."
	dataExportTooLargeText = "Tệp xuất toàn bộ dữ liệu vượt quá 512 MB. Hãy liên hệ bộ phận hỗ trợ."
	topicExportFinished    = "export.finished"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

var (
	errDataExportTooLarge = errors.New("saas: full data export exceeds the attachment size limit")
	errDataExportStopped  = errors.New("saas: full data export cancelled")
)

// DataExport is a queued full data export.
type DataExport struct {
	ID        uuid.UUID `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// dataExportParams is export_jobs.params of a full data export.
type dataExportParams struct {
	Reason           string     `json:"reason"`
	ClosureRequestID *uuid.UUID `json:"closure_request_id,omitempty"`
	// Round is the round of a closure's final export (ClosureExport.Round).
	Round int `json:"round,omitempty"`
}

// ScopeDataExportRequest is the idempotency scope of an owner's full data export request.
const ScopeDataExportRequest = "data_export.request"

// RequestDataExport queues a full data export of the actor's store. It is allowed while the
// store is read-only; only a closed store may not export. With a key it runs once per
// Idempotency-Key: a retry of the same request returns the queued job (replayed true) instead of
// queueing another.
func (s *Service) RequestDataExport(ctx context.Context, actor audit.Actor, key *idempotency.Key) (DataExport, bool, error) {
	var out DataExport
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeDataExportRequest, key, http.StatusAccepted, &out, func() error {
			e, err := s.reader.Resolve(ctx, q, actor.TenantID)
			if err != nil {
				return err
			}
			if !e.CanExport {
				return apperr.NewForbidden(CodeExportNotAllowed, "The store's subscription no longer allows exports")
			}
			out, err = s.queueDataExport(ctx, q, actor, actor.ID, dataExportParams{Reason: "owner"}, true)
			return err
		})
		return err
	})
	return out, replayed, err
}

// queueDataExport inserts a queued full data export requested by the user requestedBy (its file
// is theirs); withQuota applies the per-tenant limit of active exports (a closure's export always
// queues). q is the tenant transaction.
func (s *Service) queueDataExport(ctx context.Context, q db.Querier, actor audit.Actor, requestedBy uuid.UUID,
	params dataExportParams, withQuota bool) (DataExport, error) {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('export_quota:' || $1::text, 0))`,
		actor.TenantID); err != nil {
		return DataExport{}, fmt.Errorf("saas: lock export quota: %w", err)
	}
	if withQuota {
		var active int
		if err := sqlx.GetContext(ctx, q, &active,
			`SELECT count(*) FROM export_jobs WHERE status IN ('queued', 'running')`); err != nil {
			return DataExport{}, fmt.Errorf("saas: count active exports: %w", err)
		}
		if active >= maxActiveExports {
			return DataExport{}, apperr.NewConflict(CodeExportQueueFull, "Too many exports are already running; wait for one to finish")
		}
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return DataExport{}, fmt.Errorf("saas: encode export params: %w", err)
	}
	out := DataExport{ID: ids.New(), Status: "queued", CreatedAt: s.clock.Now()}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO export_jobs (id, tenant_id, kind, params, status, requested_by, created_at)
		VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, 'queued', $4, $5)`,
		out.ID, DataExportKind, encoded, requestedBy, out.CreatedAt); err != nil {
		return DataExport{}, fmt.Errorf("saas: insert export job: %w", err)
	}
	return out, s.audit.Record(ctx, q, actor, audit.Event{
		Action: "export_job.requested", EntityType: "export_job", EntityID: out.ID, After: params,
	})
}

// RunDataExports runs the queued full data exports of every store, oldest first, and returns how
// many finished. Without an object store they stay queued; a store held in maintenance waits for
// its release (ActiveTenantIDs).
func (s *Service) RunDataExports(ctx context.Context) (int, error) {
	if s.objects == nil {
		return 0, nil
	}
	tenants, err := s.db.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	var errs []error
	for _, tenantID := range tenants {
		for ctx.Err() == nil {
			job, ok, err := s.claimDataExport(ctx, tenantID)
			if err != nil {
				errs = append(errs, err)
				break
			}
			if !ok {
				break
			}
			finished, err := s.runDataExport(ctx, tenantID, job)
			if err != nil {
				errs = append(errs, fmt.Errorf("full export %s: %w", job.id, err))
				text := dataExportFailedText
				if errors.Is(err, errDataExportTooLarge) {
					text = dataExportTooLargeText
				}
				if markErr := s.failDataExport(ctx, tenantID, job.id, text); markErr != nil {
					errs = append(errs, markErr)
				}
				continue
			}
			if finished {
				done++
				s.logger.InfoContext(ctx, "full data export finished", "tenant_id", tenantID, "export_job_id", job.id)
			}
		}
	}
	return done, errors.Join(errs...)
}

type claimedExport struct {
	id          uuid.UUID
	requestedBy uuid.UUID
	slug        string
}

func (s *Service) claimDataExport(ctx context.Context, tenantID uuid.UUID) (claimedExport, bool, error) {
	var job claimedExport
	found := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			UPDATE export_jobs SET status = 'running', started_at = $1, heartbeat_at = $1, row_count = 0
			WHERE id = (
				SELECT id FROM export_jobs WHERE status = 'queued' AND kind = 'full_data_export'
				ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
			)
			RETURNING id, requested_by`, s.clock.Now()).Scan(&job.id, &job.requestedBy)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("saas: claim full export: %w", err)
		}
		found = true
		return q.QueryRowxContext(ctx, `SELECT slug FROM tenants WHERE id = $1`, tenantID).Scan(&job.slug)
	})
	return job, found, err
}

func (s *Service) failDataExport(ctx context.Context, tenantID, id uuid.UUID, reason string) error {
	return s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET status = 'failed', error = $2, finished_at = $3
			WHERE id = $1 AND status = 'running'`, id, reason, s.clock.Now()); err != nil {
			return fmt.Errorf("saas: fail full export: %w", err)
		}
		return nil
	})
}

// exportHeartbeat records the progress of a running export and reports whether it still runs.
func (s *Service) exportHeartbeat(ctx context.Context, tenantID, id uuid.UUID, rows int64) (bool, error) {
	running := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		res, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET row_count = $2, heartbeat_at = $3 WHERE id = $1 AND status = 'running'`,
			id, rows, s.clock.Now())
		if err != nil {
			return fmt.Errorf("saas: record export progress: %w", err)
		}
		n, err := res.RowsAffected()
		running = n == 1
		return err
	})
	return running, err
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// columnInfo is one column of the export's dictionary.
type columnInfo struct {
	Name     string `db:"name"`
	Type     string `db:"type"`
	Nullable bool   `db:"nullable"`
}

// runDataExport writes every tenant table of the store into a zip of UTF-8 CSV files (one per
// table, with a byte order mark so spreadsheet programs read Vietnamese text) and a README.csv
// column dictionary, all from one snapshot under the tenant's row security. It reports false
// when the job was cancelled meanwhile.
func (s *Service) runDataExport(ctx context.Context, tenantID uuid.UUID, job claimedExport) (bool, error) {
	f, err := os.CreateTemp("", "tally-full-export-*.zip")
	if err != nil {
		return false, fmt.Errorf("saas: create export file: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	hash := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(f, hash)}
	zw := zip.NewWriter(counter)
	var rows int64
	err = s.db.WithSnapshotTenantTx(ctx, tenantID, dataExportTimeout, func(q db.Querier) error {
		tables, err := db.TenantTables(ctx, q)
		if err != nil {
			return err
		}
		dict := [][]string{{"bảng", "cột", "kiểu dữ liệu", "cho phép trống"}}
		for _, t := range tables {
			cols, err := tableColumns(ctx, q, t.Name)
			if err != nil {
				return err
			}
			for _, c := range cols {
				nullable := "không"
				if c.Nullable {
					nullable = "có"
				}
				dict = append(dict, []string{t.Name, c.Name, c.Type, nullable})
			}
			n, err := s.writeTable(ctx, q, zw, t.Name, cols, func(total int64) error {
				if total%dataExportProgressRows != 0 {
					return nil
				}
				if counter.n > maxDataExportBytes {
					return errDataExportTooLarge
				}
				running, err := s.exportHeartbeat(ctx, tenantID, job.id, rows+total)
				if err != nil {
					return err
				}
				if !running {
					return errDataExportStopped
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("table %s: %w", t.Name, err)
			}
			rows += n
		}
		return writeCSVEntry(zw, "README.csv", dict)
	})
	if errors.Is(err, errDataExportStopped) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := zw.Close(); err != nil {
		return false, fmt.Errorf("saas: close export zip: %w", err)
	}
	if counter.n > maxDataExportBytes {
		return false, errDataExportTooLarge
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("saas: rewind export file: %w", err)
	}
	key := objectstore.TenantKey(tenantID, "exports", job.id.String()+".zip")
	if err := s.objects.Put(ctx, key, f, counter.n, zipContentType); err != nil {
		return false, fmt.Errorf("saas: store export file: %w", err)
	}
	fileName := "tally-du-lieu-" + job.slug + "-" + s.clock.Now().Format("20060102") + ".zip"
	finished, err := s.finishDataExport(ctx, tenantID, job, key, fileName, counter.n, hash.Sum(nil), rows)
	if err != nil || !finished {
		if delErr := s.objects.Delete(ctx, key); delErr != nil {
			err = errors.Join(err, fmt.Errorf("saas: delete unused export file: %w", delErr))
		}
	}
	return finished, err
}

func tableColumns(ctx context.Context, q db.Querier, table string) ([]columnInfo, error) {
	var cols []columnInfo
	if err := sqlx.SelectContext(ctx, q, &cols, `
		SELECT a.attname AS name, format_type(a.atttypid, a.atttypmod) AS type, NOT a.attnotnull AS nullable
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'tally' AND c.relname = $1 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, table); err != nil {
		return nil, fmt.Errorf("saas: read columns of %s: %w", table, err)
	}
	return cols, nil
}

// textType reports whether values of a column type are free text, which a spreadsheet could take
// for a formula.
func textType(t string) bool {
	return t == "text" || strings.HasPrefix(t, "character") || t == "citext"
}

// writeTable writes one table as <table>.csv: a header of column names, then every row the
// tenant's row security shows, each value in its PostgreSQL text form (empty for NULL).
func (s *Service) writeTable(ctx context.Context, q db.Querier, zw *zip.Writer, table string, cols []columnInfo,
	progress func(rows int64) error) (int64, error) {
	w, err := zw.Create(table + ".csv")
	if err != nil {
		return 0, fmt.Errorf("saas: add %s.csv: %w", table, err)
	}
	if _, err := w.Write(utf8BOM); err != nil {
		return 0, fmt.Errorf("saas: write %s.csv: %w", table, err)
	}
	cw := csv.NewWriter(w)
	header := make([]string, len(cols))
	selects := make([]string, len(cols))
	hasID := false
	for i, c := range cols {
		header[i] = c.Name
		selects[i] = pgx.Identifier{c.Name}.Sanitize() + "::text"
		hasID = hasID || c.Name == "id"
	}
	if err := cw.Write(header); err != nil {
		return 0, fmt.Errorf("saas: write %s.csv: %w", table, err)
	}
	query := `SELECT ` + strings.Join(selects, ", ") + ` FROM ` + pgx.Identifier{"tally", table}.Sanitize()
	if hasID {
		query += ` ORDER BY id`
	}
	rs, err := q.QueryxContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("saas: read %s: %w", table, err)
	}
	defer func() { _ = rs.Close() }()
	values := make([]sql.NullString, len(cols))
	dest := make([]any, len(cols))
	for i := range values {
		dest[i] = &values[i]
	}
	rec := make([]string, len(cols))
	var n int64
	for rs.Next() {
		if err := rs.Scan(dest...); err != nil {
			return n, fmt.Errorf("saas: scan %s: %w", table, err)
		}
		for i, v := range values {
			rec[i] = ""
			if !v.Valid {
				continue
			}
			rec[i] = v.String
			if textType(cols[i].Type) && v.String != "" && strings.ContainsAny(v.String[:1], "=+-@\t\r") {
				rec[i] = "'" + v.String
			}
		}
		if err := cw.Write(rec); err != nil {
			return n, fmt.Errorf("saas: write %s.csv: %w", table, err)
		}
		n++
		if err := progress(n); err != nil {
			return n, err
		}
	}
	if err := rs.Err(); err != nil {
		return n, fmt.Errorf("saas: read %s: %w", table, err)
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return n, fmt.Errorf("saas: write %s.csv: %w", table, err)
	}
	return n, nil
}

func writeCSVEntry(zw *zip.Writer, name string, records [][]string) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("saas: add %s: %w", name, err)
	}
	if _, err := w.Write(utf8BOM); err != nil {
		return fmt.Errorf("saas: write %s: %w", name, err)
	}
	cw := csv.NewWriter(w)
	if err := cw.WriteAll(records); err != nil {
		return fmt.Errorf("saas: write %s: %w", name, err)
	}
	return nil
}

func (s *Service) finishDataExport(ctx context.Context, tenantID uuid.UUID, job claimedExport, key, fileName string,
	size int64, sum []byte, rows int64) (bool, error) {
	finished := false
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var status string
		if err := q.QueryRowxContext(ctx, `SELECT status FROM export_jobs WHERE id = $1 FOR UPDATE`, job.id).
			Scan(&status); err != nil {
			return fmt.Errorf("saas: read full export: %w", err)
		}
		if status != "running" {
			return nil
		}
		now := s.clock.Now()
		attachmentID := ids.New()
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attachments (id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type,
				size_bytes, sha256, scan_status, uploaded_by, created_at)
			VALUES ($1, current_setting('app.tenant_id')::uuid, 'export_job', $2, $3, $4, $5, $6, $7, 'clean', $8, $9)`,
			attachmentID, job.id, key, fileName, zipContentType, size, sum, job.requestedBy, now); err != nil {
			return fmt.Errorf("saas: insert export attachment: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE export_jobs SET status = 'succeeded', attachment_id = $2, row_count = $3, finished_at = $4,
				expires_at = $5, error = NULL
			WHERE id = $1`, job.id, attachmentID, rows, now, now.Add(DataExportLinkTTL)); err != nil {
			return fmt.Errorf("saas: finish full export: %w", err)
		}
		finished = true
		if _, err := s.publisher.Publish(ctx, q, tenantID, outbox.Event{
			Topic: topicExportFinished, Payload: map[string]string{"export_job_id": job.id.String()},
		}); err != nil {
			return fmt.Errorf("saas: publish export finished: %w", err)
		}
		return nil
	})
	return finished, err
}
