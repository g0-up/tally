package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Raise upserts the notification of n by its dedupe key in the tenant of q. A notification that
// is still open only has its text refreshed; one the system resolved because its condition
// cleared reopens as a new occurrence (unread, unsnoozed, created now); one the owner resolved
// while the condition held stays resolved.
func Raise(ctx context.Context, q db.Querier, n Notice, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO notifications (id, tenant_id, kind, dedupe_key, severity, title, body, entity_type, entity_id, color_id,
		  action_path, doc_no, created_at, updated_at)
		VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		ON CONFLICT ON CONSTRAINT ux_notifications__dedupe_key DO UPDATE SET
		  severity = EXCLUDED.severity, title = EXCLUDED.title, body = EXCLUDED.body,
		  entity_type = EXCLUDED.entity_type, entity_id = EXCLUDED.entity_id, color_id = EXCLUDED.color_id,
		  action_path = EXCLUDED.action_path, doc_no = EXCLUDED.doc_no, updated_at = EXCLUDED.updated_at,
		  created_at = CASE WHEN notifications.resolved_by = 'system' THEN EXCLUDED.created_at ELSE notifications.created_at END,
		  read_at = CASE WHEN notifications.resolved_by = 'system' THEN NULL ELSE notifications.read_at END,
		  snoozed_until = CASE WHEN notifications.resolved_by = 'system' THEN NULL ELSE notifications.snoozed_until END,
		  resolved_at = CASE WHEN notifications.resolved_by = 'system' THEN NULL ELSE notifications.resolved_at END,
		  resolved_by = CASE WHEN notifications.resolved_by = 'system' THEN NULL ELSE notifications.resolved_by END`,
		ids.New(), n.Kind, n.DedupeKey, n.Severity, n.Title, n.Body, n.EntityType, n.EntityID, n.ColorID, n.ActionPath, n.DocNo, now); err != nil {
		return fmt.Errorf("notification: raise %s: %w", n.DedupeKey, err)
	}
	return nil
}

// Clear marks the notifications of keys as cleared by the system, so the condition coming back
// reopens them. A notification the owner already resolved keeps its resolution time.
func Clear(ctx context.Context, q db.Querier, keys []string, now time.Time) error {
	if len(keys) == 0 {
		return nil
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE notifications SET resolved_at = COALESCE(resolved_at, $2), resolved_by = 'system', updated_at = $2
		WHERE dedupe_key = ANY($1) AND resolved_by IS DISTINCT FROM 'system'`, keys, now); err != nil {
		return fmt.Errorf("notification: clear: %w", err)
	}
	return nil
}

// clearAbsent clears the notifications of kind whose condition is not among current. The sweep
// started at sweptAt: a notification updated since then was raised by a consumer that saw newer
// state than the sweep's scan, so it is left for the next sweep rather than cleared.
func clearAbsent(ctx context.Context, q db.Querier, kind string, current []string, sweptAt time.Time) error {
	if current == nil {
		current = []string{}
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE notifications SET resolved_at = COALESCE(resolved_at, $3), resolved_by = 'system', updated_at = $3
		WHERE kind = $1 AND resolved_by IS DISTINCT FROM 'system' AND NOT (dedupe_key = ANY($2)) AND updated_at < $3`,
		kind, current, sweptAt); err != nil {
		return fmt.Errorf("notification: clear stale %s: %w", kind, err)
	}
	return nil
}
