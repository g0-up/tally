package admin

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

var feedbackKinds = []string{"idea", "bug", "praise", "nps"}

const (
	maxFeedbackBody = 2000
	maxPagePath     = 300
)

// Feedback is one product feedback entry. It holds the kind, score, text and screen path only:
// no document, product or party data.
type Feedback struct {
	ID         uuid.UUID `db:"id"`
	TenantID   uuid.UUID `db:"tenant_id"`
	TenantSlug string    `db:"tenant_slug"`
	Kind       string    `db:"kind"`
	Score      *int      `db:"score"`
	Body       *string   `db:"body"`
	PagePath   *string   `db:"page_path"`
	CreatedAt  time.Time `db:"created_at"`
}

// FeedbackInput is what a store owner sends. Score is required for nps and optional otherwise.
type FeedbackInput struct {
	Kind     string
	Score    *int
	Body     string
	PagePath string
}

// FeedbackBox records feedback from stores and lists it for admins.
type FeedbackBox struct {
	db    *db.DB
	clock clock.Clock
}

// NewFeedbackBox returns the service.
func NewFeedbackBox(d *db.DB, clk clock.Clock) *FeedbackBox { return &FeedbackBox{db: d, clock: clk} }

// pagePathOf keeps the path of a screen and drops its query and fragment, which may carry
// search terms or document ids.
func pagePathOf(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return p
}

func (in FeedbackInput) validate() (body, page *string, err error) {
	fields := map[string]string{}
	if !slices.Contains(feedbackKinds, in.Kind) {
		fields["kind"] = "invalid"
	}
	switch {
	case in.Score != nil && (*in.Score < 0 || *in.Score > 10):
		fields["score"] = "invalid"
	case in.Score == nil && in.Kind == "nps":
		fields["score"] = "required"
	}
	if b := strings.TrimSpace(in.Body); b != "" {
		if utf8.RuneCountInString(b) > maxFeedbackBody {
			fields["body"] = "too_long"
		}
		body = &b
	} else if in.Kind != "nps" {
		fields["body"] = "required"
	}
	if p := pagePathOf(in.PagePath); p != "" {
		if !strings.HasPrefix(p, "/") || len(p) > maxPagePath || !utf8.ValidString(p) {
			fields["page_path"] = "invalid"
		}
		page = &p
	}
	if len(fields) > 0 {
		return nil, nil, errInvalid(fields)
	}
	return body, page, nil
}

// Submit records feedback of tenantID.
func (s *FeedbackBox) Submit(ctx context.Context, tenantID uuid.UUID, in FeedbackInput) (uuid.UUID, error) {
	body, page, err := in.validate()
	if err != nil {
		return uuid.Nil, err
	}
	id := ids.New()
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO product_feedback (id, tenant_id, kind, score, body, page_path, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, tenantID, in.Kind, in.Score, body, page, s.clock.Now())
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("admin: insert feedback: %w", err)
	}
	return id, nil
}

// List returns one page of the feedback, newest first, optionally of one kind, without replaced
// sandboxes; it reads page.FetchLimit rows.
func (s *FeedbackBox) List(ctx context.Context, kind string, page httpx.Page) ([]Feedback, error) {
	if kind != "" && !slices.Contains(feedbackKinds, kind) {
		return nil, errInvalid(map[string]string{"kind": "invalid"})
	}
	after, afterID, err := page.AfterTime()
	if err != nil {
		return nil, err
	}
	args := []any{after, afterID, page.FetchLimit()}
	filter := ""
	if kind != "" {
		filter = ` AND f.kind = $4`
		args = append(args, kind)
	}
	out := []Feedback{}
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT f.id, f.tenant_id, t.slug AS tenant_slug, f.kind, f.score, f.body, f.page_path, f.created_at
			FROM product_feedback f JOIN tenants t ON t.id = f.tenant_id
			WHERE t.status <> 'replaced' AND ($1::timestamptz IS NULL OR (f.created_at, f.id) < ($1, $2))`+filter+`
			ORDER BY f.created_at DESC, f.id DESC LIMIT $3`, args...)
	})
	if err != nil {
		return nil, fmt.Errorf("admin: list feedback: %w", err)
	}
	return out, nil
}
