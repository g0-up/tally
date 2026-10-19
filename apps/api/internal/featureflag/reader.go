// Package featureflag reads and writes the platform feature flags: a global value per key with
// optional per-store overrides, every change kept in the append-only feature_flag_changes.
package featureflag

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// CacheTTL is how long a read flag value is reused; a change through this process's Writer
// clears the cache at once, other processes see it within the TTL.
const CacheTTL = 30 * time.Second

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,79}$`)

// Flag is one stored value: TenantID nil is the global value. TenantSlug and TenantName name the
// store of an override and are nil on the global value.
type Flag struct {
	Key        string     `db:"key" json:"key"`
	TenantID   *uuid.UUID `db:"tenant_id" json:"tenant_id"`
	TenantSlug *string    `db:"tenant_slug" json:"tenant_slug"`
	TenantName *string    `db:"tenant_name" json:"tenant_name"`
	Enabled    bool       `db:"enabled" json:"enabled"`
	UpdatedAt  time.Time  `db:"updated_at" json:"updated_at"`
}

// Change is one entry of a flag's history. Enabled nil is the removal of a store override;
// TenantSlug and TenantName are nil on a change of the global value.
type Change struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	Key           string     `db:"key" json:"key"`
	TenantID      *uuid.UUID `db:"tenant_id" json:"tenant_id"`
	TenantSlug    *string    `db:"tenant_slug" json:"tenant_slug"`
	TenantName    *string    `db:"tenant_name" json:"tenant_name"`
	Enabled       *bool      `db:"enabled" json:"enabled"`
	AdminID       uuid.UUID  `db:"admin_id" json:"admin_id"`
	AdminFullName string     `db:"admin_full_name" json:"admin_full_name"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
}

type cacheKey struct {
	key    string
	tenant uuid.UUID
}

type cached struct {
	enabled, found bool
	at             time.Time
}

// Reader answers a flag for a store: the store's override, else the global value. It implements
// saas.FlagReader.
type Reader struct {
	clock clock.Clock
	mu    sync.Mutex
	cache map[cacheKey]cached
}

// NewReader returns a reader with an empty cache.
func NewReader(clk clock.Clock) *Reader {
	return &Reader{clock: clk, cache: map[cacheKey]cached{}}
}

// Flag returns the flag key for tenantID, read in q (feature_flags is a platform table, so any
// transaction can read it); found is false when neither an override nor a global value is set.
func (r *Reader) Flag(ctx context.Context, q db.Querier, key string, tenantID uuid.UUID) (bool, bool, error) {
	ck := cacheKey{key: key, tenant: tenantID}
	now := r.clock.Now()
	r.mu.Lock()
	if c, ok := r.cache[ck]; ok && now.Sub(c.at) < CacheTTL {
		r.mu.Unlock()
		return c.enabled, c.found, nil
	}
	r.mu.Unlock()
	var enabled bool
	err := sqlx.GetContext(ctx, q, &enabled, `
		SELECT enabled FROM feature_flags
		WHERE key = $1 AND (tenant_id = $2 OR tenant_id IS NULL)
		ORDER BY tenant_id NULLS LAST LIMIT 1`, key, tenantID)
	found := true
	if db.IsNoRows(err) {
		enabled, found, err = false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("featureflag: read %s: %w", key, err)
	}
	r.mu.Lock()
	r.cache[ck] = cached{enabled: enabled, found: found, at: now}
	r.mu.Unlock()
	return enabled, found, nil
}

// forget clears the cached values of key.
func (r *Reader) forget(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ck := range r.cache {
		if ck.key == key {
			delete(r.cache, ck)
		}
	}
}

// Writer changes flags for platform admins and keeps their history.
type Writer struct {
	db     *db.DB
	clock  clock.Clock
	reader *Reader
	audit  *audit.Recorder
}

// NewWriter returns the writer; reader (may be nil) is cleared on every change.
func NewWriter(d *db.DB, clk clock.Clock, reader *Reader) *Writer {
	return &Writer{db: d, clock: clk, reader: reader, audit: audit.NewRecorder(clk)}
}

// ValidKey reports whether key is a well-formed flag key.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// Set stores the flag value (the global one when tenantID is nil) and appends the change to its
// history, both in one transaction. A store override needs an existing store that is not a
// replaced sandbox; it also goes to that store's audit log with the value it replaced, in the
// same transaction. admin names the platform admin; its TenantID is taken from tenantID.
func (w *Writer) Set(ctx context.Context, admin audit.Actor, key string, tenantID *uuid.UUID, enabled bool) (Flag, error) {
	if !ValidKey(key) {
		return Flag{}, apperr.NewValidation("validation_failed", "Some fields are not valid", map[string]string{"key": "invalid"})
	}
	now := w.clock.Now()
	out := Flag{Key: key, TenantID: tenantID, Enabled: enabled, UpdatedAt: now}
	write := func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO feature_flags (key, tenant_id, enabled, updated_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT ON CONSTRAINT ux_feature_flags__key_tenant_id DO UPDATE
			  SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`,
			key, tenantID, enabled, now); err != nil {
			return fmt.Errorf("featureflag: write %s: %w", key, err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO feature_flag_changes (id, key, tenant_id, enabled, admin_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), key, tenantID, enabled, admin.ID, now); err != nil {
			return fmt.Errorf("featureflag: record change of %s: %w", key, err)
		}
		return nil
	}
	var err error
	if tenantID == nil {
		err = w.db.WithPlatformTx(ctx, write)
	} else {
		admin.TenantID = *tenantID
		err = w.db.WithTenantTx(ctx, *tenantID, func(q db.Querier) error {
			var store struct {
				Status string `db:"status"`
				Slug   string `db:"slug"`
				Name   string `db:"display_name"`
			}
			err := sqlx.GetContext(ctx, q, &store, `SELECT status, slug, display_name FROM tenants WHERE id = $1`, *tenantID)
			if db.IsNoRows(err) || store.Status == "replaced" {
				return apperr.NewNotFound("not_found", "Store not found")
			}
			if err != nil {
				return fmt.Errorf("featureflag: read tenant: %w", err)
			}
			out.TenantSlug, out.TenantName = &store.Slug, &store.Name
			var before map[string]any
			var was bool
			err = sqlx.GetContext(ctx, q, &was, `SELECT enabled FROM feature_flags WHERE key = $1 AND tenant_id = $2 FOR UPDATE`,
				key, *tenantID)
			switch {
			case err == nil:
				before = map[string]any{"key": key, "enabled": was}
			case !db.IsNoRows(err):
				return fmt.Errorf("featureflag: read override of %s: %w", key, err)
			}
			if err := write(q); err != nil {
				return err
			}
			return w.audit.Record(ctx, q, admin, audit.Event{
				Action: "feature_flag.override_set", EntityType: "feature_flag",
				Before: before, After: map[string]any{"key": key, "enabled": enabled},
			})
		})
	}
	if err != nil {
		return Flag{}, err
	}
	if w.reader != nil {
		w.reader.forget(key)
	}
	return out, nil
}

// Unset removes the override of key for the store of admin.TenantID, so the store follows the
// global value again (or no value). The removal goes to the flag history and to the store's audit
// log in one transaction; a store without that override answers 404.
func (w *Writer) Unset(ctx context.Context, admin audit.Actor, key string) error {
	if !ValidKey(key) {
		return apperr.NewNotFound("not_found", "Not found")
	}
	tenantID := admin.TenantID
	now := w.clock.Now()
	err := w.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var status string
		err := sqlx.GetContext(ctx, q, &status, `SELECT status FROM tenants WHERE id = $1`, tenantID)
		if db.IsNoRows(err) || status == "replaced" {
			return apperr.NewNotFound("not_found", "Store not found")
		}
		if err != nil {
			return fmt.Errorf("featureflag: read tenant: %w", err)
		}
		var removed Flag
		err = sqlx.GetContext(ctx, q, &removed, `
			DELETE FROM feature_flags WHERE key = $1 AND tenant_id = $2
			RETURNING key, tenant_id, enabled, updated_at`, key, tenantID)
		if db.IsNoRows(err) {
			return apperr.NewNotFound("flag_override_not_found", "This store has no override of the flag")
		}
		if err != nil {
			return fmt.Errorf("featureflag: remove override of %s: %w", key, err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO feature_flag_changes (id, key, tenant_id, enabled, admin_id, created_at)
			VALUES ($1, $2, $3, NULL, $4, $5)`, ids.New(), key, tenantID, admin.ID, now); err != nil {
			return fmt.Errorf("featureflag: record removal of %s: %w", key, err)
		}
		return w.audit.Record(ctx, q, admin, audit.Event{
			Action: "feature_flag.override_removed", EntityType: "feature_flag",
			Before: map[string]any{"key": removed.Key, "enabled": removed.Enabled},
		})
	})
	if err != nil {
		return err
	}
	if w.reader != nil {
		w.reader.forget(key)
	}
	return nil
}

// List returns every stored value, global ones first, without those of replaced sandboxes.
func (w *Writer) List(ctx context.Context) ([]Flag, error) {
	out := []Flag{}
	err := w.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT f.key, f.tenant_id, t.slug AS tenant_slug, t.display_name AS tenant_name, f.enabled, f.updated_at
			FROM feature_flags f LEFT JOIN tenants t ON t.id = f.tenant_id
			WHERE t.id IS NULL OR t.status <> 'replaced'
			ORDER BY f.key, f.tenant_id NULLS FIRST LIMIT 5000`)
	})
	if err != nil {
		return nil, fmt.Errorf("featureflag: list: %w", err)
	}
	return out, nil
}

// Changes returns the history of key, newest first, without that of replaced sandboxes.
func (w *Writer) Changes(ctx context.Context, key string) ([]Change, error) {
	out := []Change{}
	err := w.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT c.id, c.key, c.tenant_id, t.slug AS tenant_slug, t.display_name AS tenant_name, c.enabled, c.admin_id,
			  a.full_name AS admin_full_name, c.created_at
			FROM feature_flag_changes c JOIN platform_admins a ON a.id = c.admin_id
			  LEFT JOIN tenants t ON t.id = c.tenant_id
			WHERE c.key = $1 AND (t.id IS NULL OR t.status <> 'replaced') ORDER BY c.created_at DESC, c.id DESC LIMIT 1000`, key)
	})
	if err != nil {
		return nil, fmt.Errorf("featureflag: list changes: %w", err)
	}
	return out, nil
}
