package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Incident statuses and impacts.
const (
	IncidentInvestigating = "investigating"
	IncidentIdentified    = "identified"
	IncidentMonitoring    = "monitoring"
	IncidentResolved      = "resolved"
)

var (
	incidentStatuses = []string{IncidentInvestigating, IncidentIdentified, IncidentMonitoring, IncidentResolved}
	incidentImpacts  = []string{"none", "minor", "major", "critical"}
)

// statusWindow is how long a resolved incident stays on the public status.
const statusWindow = 7 * 24 * time.Hour

// IncidentUpdate is one public note of an incident.
type IncidentUpdate struct {
	At      time.Time `json:"at"`
	Status  string    `json:"status"`
	Message string    `json:"message"`
}

// Incident is a platform incident shown on the status page.
type Incident struct {
	ID         uuid.UUID                   `db:"id"`
	Title      string                      `db:"title"`
	Status     string                      `db:"status"`
	Impact     string                      `db:"impact"`
	StartedAt  time.Time                   `db:"started_at"`
	ResolvedAt *time.Time                  `db:"resolved_at"`
	Updates    db.JSONList[IncidentUpdate] `db:"updates"`
}

const incidentColumns = `id, title, status, impact, started_at, resolved_at, updates`

// Incidents runs incidents and answers the public status.
type Incidents struct {
	db    *db.DB
	clock clock.Clock
}

// NewIncidents returns the service.
func NewIncidents(d *db.DB, clk clock.Clock) *Incidents { return &Incidents{db: d, clock: clk} }

// NewIncident opens an incident; Status defaults to investigating.
type NewIncident struct {
	Title   string
	Impact  string
	Status  string
	Message string
}

// IncidentPatch changes an incident; Message, when set, is added as a public update.
type IncidentPatch struct {
	Title   *string
	Status  *string
	Impact  *string
	Message string
}

func checkMessage(fields map[string]string, message string, required bool) {
	m := strings.TrimSpace(message)
	if (required && m == "") || utf8.RuneCountInString(m) > 2000 {
		fields["message"] = "invalid"
	}
}

func checkTitle(fields map[string]string, title string) {
	if t := strings.TrimSpace(title); t == "" || utf8.RuneCountInString(t) > 200 {
		fields["title"] = "invalid"
	}
}

// Create opens an incident with its first update.
func (s *Incidents) Create(ctx context.Context, in NewIncident) (Incident, error) {
	if in.Status == "" {
		in.Status = IncidentInvestigating
	}
	fields := map[string]string{}
	checkTitle(fields, in.Title)
	if !slices.Contains(incidentImpacts, in.Impact) {
		fields["impact"] = "invalid"
	}
	if !slices.Contains(incidentStatuses, in.Status) {
		fields["status"] = "invalid"
	}
	checkMessage(fields, in.Message, true)
	if len(fields) > 0 {
		return Incident{}, errInvalid(fields)
	}
	now := s.clock.Now()
	out := Incident{
		ID: ids.New(), Title: strings.TrimSpace(in.Title), Status: in.Status, Impact: in.Impact, StartedAt: now,
		Updates: db.JSONList[IncidentUpdate]{{At: now, Status: in.Status, Message: strings.TrimSpace(in.Message)}},
	}
	if in.Status == IncidentResolved {
		out.ResolvedAt = &now
	}
	updates, err := json.Marshal([]IncidentUpdate(out.Updates))
	if err != nil {
		return Incident{}, fmt.Errorf("admin: encode incident updates: %w", err)
	}
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO incidents (id, title, status, impact, started_at, resolved_at, updates) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			out.ID, out.Title, out.Status, out.Impact, out.StartedAt, out.ResolvedAt, updates)
		return err
	})
	if err != nil {
		return Incident{}, fmt.Errorf("admin: insert incident: %w", err)
	}
	return out, nil
}

// Update changes an incident under a row lock. Resolving it stamps resolved_at; reopening it
// clears the stamp. A status change without a message still records an update.
func (s *Incidents) Update(ctx context.Context, id uuid.UUID, in IncidentPatch) (Incident, error) {
	fields := map[string]string{}
	if in.Title != nil {
		checkTitle(fields, *in.Title)
	}
	if in.Status != nil && !slices.Contains(incidentStatuses, *in.Status) {
		fields["status"] = "invalid"
	}
	if in.Impact != nil && !slices.Contains(incidentImpacts, *in.Impact) {
		fields["impact"] = "invalid"
	}
	checkMessage(fields, in.Message, false)
	if len(fields) > 0 {
		return Incident{}, errInvalid(fields)
	}
	now := s.clock.Now()
	var out Incident
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &out, `SELECT `+incidentColumns+` FROM incidents WHERE id = $1 FOR UPDATE`, id)
		if db.IsNoRows(err) {
			return apperr.NewNotFound("not_found", "Incident not found")
		}
		if err != nil {
			return fmt.Errorf("admin: read incident: %w", err)
		}
		statusChanged := in.Status != nil && *in.Status != out.Status
		if in.Title != nil {
			out.Title = strings.TrimSpace(*in.Title)
		}
		if in.Impact != nil {
			out.Impact = *in.Impact
		}
		if in.Status != nil {
			out.Status = *in.Status
		}
		switch {
		case out.Status == IncidentResolved && out.ResolvedAt == nil:
			out.ResolvedAt = &now
		case out.Status != IncidentResolved:
			out.ResolvedAt = nil
		}
		if msg := strings.TrimSpace(in.Message); msg != "" || statusChanged {
			out.Updates = append(out.Updates, IncidentUpdate{At: now, Status: out.Status, Message: msg})
		}
		updates, err := json.Marshal([]IncidentUpdate(out.Updates))
		if err != nil {
			return fmt.Errorf("admin: encode incident updates: %w", err)
		}
		_, err = q.ExecContext(ctx, `
			UPDATE incidents SET title = $2, status = $3, impact = $4, resolved_at = $5, updates = $6 WHERE id = $1`,
			id, out.Title, out.Status, out.Impact, out.ResolvedAt, updates)
		if err != nil {
			return fmt.Errorf("admin: update incident: %w", err)
		}
		return nil
	})
	return out, err
}

// IncidentsOpen filters the incident list to those not resolved yet, whatever their status.
const IncidentsOpen = "open"

// List returns one page of the incidents, newest first; it reads page.FetchLimit rows. status,
// when set, keeps the incidents of that status, or the unresolved ones for IncidentsOpen.
func (s *Incidents) List(ctx context.Context, page httpx.Page, status string) ([]Incident, error) {
	after, afterID, err := page.AfterTime()
	if err != nil {
		return nil, err
	}
	args := []any{after, afterID, page.FetchLimit()}
	filter := ""
	switch {
	case status == "":
	case status == IncidentsOpen:
		filter = ` AND resolved_at IS NULL`
	case slices.Contains(incidentStatuses, status):
		filter = ` AND status = $4`
		args = append(args, status)
	default:
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	out := []Incident{}
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+incidentColumns+` FROM incidents
			WHERE ($1::timestamptz IS NULL OR (started_at, id) < ($1, $2))`+filter+`
			ORDER BY started_at DESC, id DESC LIMIT $3`, args...)
	})
	if err != nil {
		return nil, fmt.Errorf("admin: list incidents: %w", err)
	}
	return out, nil
}

// Status is the public service status: the open incidents and those resolved in the last
// week, with an overall indicator.
type Status struct {
	// Indicator is "operational", "degraded" (open minor or no-impact incidents) or "outage"
	// (an open major or critical one).
	Indicator string
	Incidents []Incident
}

// Status answers the public status.
func (s *Incidents) Status(ctx context.Context) (Status, error) {
	out := Status{Indicator: "operational", Incidents: []Incident{}}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out.Incidents, `
			SELECT `+incidentColumns+` FROM incidents WHERE resolved_at IS NULL OR resolved_at > $1
			ORDER BY resolved_at IS NULL DESC, started_at DESC LIMIT 50`, s.clock.Now().Add(-statusWindow))
	})
	if err != nil {
		return Status{}, fmt.Errorf("admin: read status: %w", err)
	}
	for _, i := range out.Incidents {
		if i.ResolvedAt != nil {
			continue
		}
		if i.Impact == "major" || i.Impact == "critical" {
			out.Indicator = "outage"
			break
		}
		out.Indicator = "degraded"
	}
	return out, nil
}
