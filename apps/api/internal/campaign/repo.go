package campaign

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

const campaignColumns = `id, name, kind, starts_on, ends_on, budget_amount, target_revenue, target_margin_percent,
	status, closed_at, created_at, updated_at, version`

func errCursor() error {
	return apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
}

func insertCampaign(ctx context.Context, q db.Querier, tenantID uuid.UUID, c Campaign) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO campaigns (id, tenant_id, name, name_key, kind, starts_on, ends_on, budget_amount, target_revenue,
			target_margin_percent, status, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, 1)`,
		c.ID, tenantID, c.Name, textnorm.Key(c.Name), c.Kind, c.StartsOn, c.EndsOn, c.Budget, c.TargetRevenue,
		c.TargetMarginPercent, c.Status, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("campaign: insert: %w", err)
	}
	return nil
}

// loadCampaign reads a campaign, locking it for update when lock is set.
func loadCampaign(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (Campaign, error) {
	query := `SELECT ` + campaignColumns + ` FROM campaigns WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	var c Campaign
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&c); err != nil {
		if db.IsNoRows(err) {
			return Campaign{}, errNotFound("campaign")
		}
		return Campaign{}, fmt.Errorf("campaign: load: %w", err)
	}
	return c, nil
}

func loadDetail(ctx context.Context, q db.Querier, id uuid.UUID) (Detail, error) {
	c, err := loadCampaign(ctx, q, id, false)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Campaign: c, PriceListIDs: []uuid.UUID{}, PromotionIDs: []uuid.UUID{}}
	if err := sqlxSelect(ctx, q, &d.PriceListIDs, `SELECT id FROM price_lists WHERE campaign_id = $1 ORDER BY name_key, id`, id); err != nil {
		return Detail{}, err
	}
	if err := sqlxSelect(ctx, q, &d.PromotionIDs, `SELECT id FROM promotions WHERE campaign_id = $1 ORDER BY valid_from, id`, id); err != nil {
		return Detail{}, err
	}
	if d.Checklist, err = listChecklist(ctx, q, id); err != nil {
		return Detail{}, err
	}
	return d, nil
}

type listCursor struct {
	StartsOn time.Time
	ID       uuid.UUID
}

func listCampaigns(ctx context.Context, q db.Querier, f Filter, after *listCursor, limit int) ([]Campaign, error) {
	where := []string{"TRUE"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Status != "" {
		where = append(where, "status = "+arg(f.Status))
	}
	if f.Kind != "" {
		where = append(where, "kind = "+arg(f.Kind))
	}
	if s := textnorm.Key(f.Search); s != "" {
		where = append(where, "name_key LIKE "+arg("%"+escapeLike(s)+"%"))
	}
	if after != nil {
		where = append(where, fmt.Sprintf("(starts_on, id) < (%s, %s)", arg(after.StartsOn), arg(after.ID)))
	}
	query := `SELECT ` + campaignColumns + ` FROM campaigns WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY starts_on DESC, id DESC LIMIT ` + arg(limit)
	out := []Campaign{}
	if err := sqlxSelect(ctx, q, &out, query, args...); err != nil {
		return nil, err
	}
	return out, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func sqlxSelect(ctx context.Context, q db.Querier, dst any, query string, args ...any) error {
	if err := sqlx.SelectContext(ctx, q, dst, query, args...); err != nil {
		return fmt.Errorf("campaign: query: %w", err)
	}
	return nil
}

// linkPriceLists points the given price lists at the campaign. Every id must exist.
func linkPriceLists(ctx context.Context, q db.Querier, campaignID uuid.UUID, listIDs []uuid.UUID, now time.Time) error {
	return link(ctx, q, "price_lists", "price_list_ids", campaignID, listIDs, now)
}

func linkPromotions(ctx context.Context, q db.Querier, campaignID uuid.UUID, promoIDs []uuid.UUID, now time.Time) error {
	return link(ctx, q, "promotions", "promotion_ids", campaignID, promoIDs, now)
}

// link sets campaign_id on rows of table (a code constant). A row linked to another campaign
// moves to this one.
func link(ctx context.Context, q db.Querier, table, field string, campaignID uuid.UUID, rowIDs []uuid.UUID, now time.Time) error {
	if len(rowIDs) == 0 {
		return nil
	}
	want := map[uuid.UUID]bool{}
	for _, id := range rowIDs {
		want[id] = true
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET campaign_id = $1, updated_at = $2, version = version + 1
		WHERE id = ANY($3) AND campaign_id IS DISTINCT FROM $1`, campaignID, now, rowIDs); err != nil {
		return fmt.Errorf("campaign: link %s: %w", table, err)
	}
	var found int
	if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM `+table+` WHERE id = ANY($1)`, rowIDs).Scan(&found); err != nil {
		return fmt.Errorf("campaign: link %s: %w", table, err)
	}
	if found != len(want) {
		return errInvalid(map[string]string{field: "not_found"})
	}
	return nil
}

func unlinkPriceLists(ctx context.Context, q db.Querier, campaignID uuid.UUID, keep []uuid.UUID, now time.Time) error {
	return unlink(ctx, q, "price_lists", campaignID, keep, now)
}

func unlinkPromotions(ctx context.Context, q db.Querier, campaignID uuid.UUID, keep []uuid.UUID, now time.Time) error {
	return unlink(ctx, q, "promotions", campaignID, keep, now)
}

func unlink(ctx context.Context, q db.Querier, table string, campaignID uuid.UUID, keep []uuid.UUID, now time.Time) error {
	if keep == nil {
		keep = []uuid.UUID{}
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET campaign_id = NULL, updated_at = $2, version = version + 1
		WHERE campaign_id = $1 AND NOT (id = ANY($3))`, campaignID, now, keep); err != nil {
		return fmt.Errorf("campaign: unlink %s: %w", table, err)
	}
	return nil
}

func insertChecklistItem(ctx context.Context, q db.Querier, tenantID, campaignID uuid.UUID, phase, kind, title string, sortOrder int, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO campaign_checklist_items (id, tenant_id, campaign_id, phase, kind, title, sort_order, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ids.New(), tenantID, campaignID, phase, kind, title, sortOrder, now); err != nil {
		return fmt.Errorf("campaign: insert checklist item: %w", err)
	}
	return nil
}

func listChecklist(ctx context.Context, q db.Querier, campaignID uuid.UUID) ([]ChecklistItem, error) {
	out := []ChecklistItem{}
	err := sqlxSelect(ctx, q, &out, `
		SELECT id, phase, kind, title, done_at, done_by, sort_order, created_at
		FROM campaign_checklist_items WHERE campaign_id = $1
		ORDER BY CASE phase WHEN 'before_peak' THEN 0 ELSE 1 END, sort_order, id`, campaignID)
	return out, err
}

func setItemDone(ctx context.Context, q db.Querier, campaignID, itemID uuid.UUID, done bool, now time.Time, by uuid.UUID) (bool, error) {
	var res interface{ RowsAffected() (int64, error) }
	var err error
	if done {
		res, err = q.ExecContext(ctx, `UPDATE campaign_checklist_items SET done_at = COALESCE(done_at, $3), done_by = COALESCE(done_by, $4)
			WHERE campaign_id = $1 AND id = $2`, campaignID, itemID, now, by)
	} else {
		res, err = q.ExecContext(ctx, `UPDATE campaign_checklist_items SET done_at = NULL, done_by = NULL
			WHERE campaign_id = $1 AND id = $2`, campaignID, itemID)
	}
	if err != nil {
		return false, fmt.Errorf("campaign: update checklist item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("campaign: update checklist item: %w", err)
	}
	return n == 1, nil
}
