package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// maxSavedReports bounds the saved layouts one list returns.
const maxSavedReports = 500

// maxSavedReportName bounds the name of a saved layout, in characters.
const maxSavedReportName = 120

// ReportConfig is a saved layout: the columns shown, the grouping and the filters. Dates are not
// saved; a layout is opened on the period the owner picks.
type ReportConfig struct {
	Columns []string          `json:"columns"`
	GroupBy string            `json:"group_by,omitempty"`
	Filters map[string]string `json:"filters"`
}

// SavedReport is a named layout of a report.
type SavedReport struct {
	ID        uuid.UUID    `db:"id"`
	ReportKey string       `db:"report_key"`
	Name      string       `db:"name"`
	Config    ReportConfig `db:"-"`
	RawConfig []byte       `db:"config"`
	CreatedAt time.Time    `db:"created_at"`
	UpdatedAt time.Time    `db:"updated_at"`
}

// SavedReportInput creates a saved layout.
type SavedReportInput struct {
	ReportKey string
	Name      string
	Config    ReportConfig
}

// SavedReportPatch changes a saved layout; nil fields stay.
type SavedReportPatch struct {
	Name   *string
	Config *ReportConfig
}

// checkConfig validates a layout against its report: known columns, an offered grouping and
// filters the report accepts with valid values. It returns the layout normalised.
func (r Report) checkConfig(cfg ReportConfig) (ReportConfig, error) {
	raw := map[string]string{}
	fields := map[string]string{}
	for k, v := range cfg.Filters {
		known := false
		for _, f := range r.Filters {
			known = known || f.Name == k
		}
		if !known {
			fields["config.filters."+k] = "unknown_filter"
			continue
		}
		raw[k] = v
	}
	if cfg.GroupBy != "" {
		raw[paramGroupBy] = cfg.GroupBy
	}
	if len(cfg.Columns) > 0 {
		raw[paramColumns] = strings.Join(cfg.Columns, ",")
	}
	if len(fields) > 0 {
		return ReportConfig{}, errInvalid(fields)
	}
	p, err := r.resolve(raw, time.Now())
	if err != nil {
		if e, ok := apperr.As(err); ok && len(e.Fields) > 0 {
			prefixed := make(map[string]string, len(e.Fields))
			for k, v := range e.Fields {
				prefixed["config."+k] = v
			}
			return ReportConfig{}, errInvalid(prefixed)
		}
		return ReportConfig{}, err
	}
	out := ReportConfig{Columns: p.Columns, Filters: p.Filters}
	if out.Columns == nil {
		out.Columns = []string{}
	}
	if len(r.GroupBys) > 0 {
		out.GroupBy = p.GroupBy
	}
	return out, nil
}

func checkSavedName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errInvalid(map[string]string{"name": "required"})
	}
	if utf8.RuneCountInString(name) > maxSavedReportName {
		return "", errInvalid(map[string]string{"name": "too_long"})
	}
	return name, nil
}

func (s *Service) saveableReport(key string) (Report, error) {
	rep, ok := s.registry.Lookup(key)
	if !ok || !rep.Saveable() {
		return Report{}, errInvalid(map[string]string{"report_key": "invalid"})
	}
	return rep, nil
}

func decodeSaved(r *SavedReport) error {
	if err := json.Unmarshal(r.RawConfig, &r.Config); err != nil {
		return fmt.Errorf("reporting: decode saved report config: %w", err)
	}
	if r.Config.Columns == nil {
		r.Config.Columns = []string{}
	}
	if r.Config.Filters == nil {
		r.Config.Filters = map[string]string{}
	}
	return nil
}

const savedColumns = `id, report_key, name, config, created_at, updated_at`

func loadSaved(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (SavedReport, error) {
	query := `SELECT ` + savedColumns + ` FROM saved_reports WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	var r SavedReport
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&r); err != nil {
		if db.IsNoRows(err) {
			return SavedReport{}, errNotFound("saved report")
		}
		return SavedReport{}, fmt.Errorf("reporting: read saved report: %w", err)
	}
	return r, decodeSaved(&r)
}

// ListSaved lists the saved layouts, of one report when key is set, by report and name.
func (s *Service) ListSaved(ctx context.Context, scope Scope, key string) ([]SavedReport, error) {
	if key != "" {
		rep, err := s.saveableReport(key)
		if err != nil {
			return nil, err
		}
		key = rep.Key
	}
	var out []SavedReport
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+savedColumns+` FROM saved_reports
			WHERE $1 = '' OR report_key = $1
			ORDER BY report_key, name, id LIMIT $2`, key, maxSavedReports); err != nil {
			return fmt.Errorf("reporting: list saved reports: %w", err)
		}
		for i := range out {
			if err := decodeSaved(&out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if out == nil {
		out = []SavedReport{}
	}
	return out, err
}

// CreateSaved saves a layout under a name unique within its report.
func (s *Service) CreateSaved(ctx context.Context, scope Scope, actor audit.Actor, in SavedReportInput) (SavedReport, error) {
	rep, err := s.saveableReport(in.ReportKey)
	if err != nil {
		return SavedReport{}, err
	}
	name, err := checkSavedName(in.Name)
	if err != nil {
		return SavedReport{}, err
	}
	cfg, err := rep.checkConfig(in.Config)
	if err != nil {
		return SavedReport{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return SavedReport{}, fmt.Errorf("reporting: encode saved report config: %w", err)
	}
	now := s.clock.Now()
	out := SavedReport{ID: ids.New(), ReportKey: rep.Key, Name: name, Config: cfg, RawConfig: raw, CreatedAt: now, UpdatedAt: now}
	err = s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO saved_reports (id, tenant_id, report_key, name, config, created_at, updated_at)
			VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $5)`,
			out.ID, out.ReportKey, out.Name, raw, now); err != nil {
			if db.IsUniqueViolation(err, "ux_saved_reports__report_key_name") {
				return apperr.NewConflict(CodeNameTaken, "A saved report of this report already has this name")
			}
			return fmt.Errorf("reporting: insert saved report: %w", err)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "saved_report.created", EntityType: "saved_report", EntityID: out.ID, After: out.Config,
		})
	})
	if err != nil {
		return SavedReport{}, err
	}
	return out, nil
}

// UpdateSaved renames a layout or replaces its configuration.
func (s *Service) UpdateSaved(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID, patch SavedReportPatch) (SavedReport, error) {
	var out SavedReport
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		cur, err := loadSaved(ctx, q, id, true)
		if err != nil {
			return err
		}
		rep, err := s.saveableReport(cur.ReportKey)
		if err != nil {
			return err
		}
		out = cur
		if patch.Name != nil {
			if out.Name, err = checkSavedName(*patch.Name); err != nil {
				return err
			}
		}
		if patch.Config != nil {
			if out.Config, err = rep.checkConfig(*patch.Config); err != nil {
				return err
			}
		}
		if out.RawConfig, err = json.Marshal(out.Config); err != nil {
			return fmt.Errorf("reporting: encode saved report config: %w", err)
		}
		out.UpdatedAt = s.clock.Now()
		if _, err := q.ExecContext(ctx, `UPDATE saved_reports SET name = $2, config = $3, updated_at = $4 WHERE id = $1`,
			id, out.Name, out.RawConfig, out.UpdatedAt); err != nil {
			if db.IsUniqueViolation(err, "ux_saved_reports__report_key_name") {
				return apperr.NewConflict(CodeNameTaken, "A saved report of this report already has this name")
			}
			return fmt.Errorf("reporting: update saved report: %w", err)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "saved_report.updated", EntityType: "saved_report", EntityID: id,
			Before: map[string]any{"name": cur.Name, "config": cur.Config},
			After:  map[string]any{"name": out.Name, "config": out.Config},
		})
	})
	if err != nil {
		return SavedReport{}, err
	}
	return out, nil
}

// DeleteSaved removes a layout.
func (s *Service) DeleteSaved(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID) error {
	return s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		cur, err := loadSaved(ctx, q, id, true)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM saved_reports WHERE id = $1`, id); err != nil {
			return fmt.Errorf("reporting: delete saved report: %w", err)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "saved_report.deleted", EntityType: "saved_report", EntityID: id,
			Before: map[string]any{"report_key": cur.ReportKey, "name": cur.Name, "config": cur.Config},
		})
	})
}
