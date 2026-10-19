//go:build integration

package testkit

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Tenant is a tenant row created for one test.
type Tenant struct {
	ID   uuid.UUID
	Slug string
}

// NewTenant inserts an active tenant with a unique slug.
func NewTenant(tb testing.TB, d *db.DB) Tenant {
	tb.Helper()
	id := ids.New()
	slug := "t-" + strings.ReplaceAll(id.String(), "-", "")[20:]
	now := time.Now().UTC().Truncate(time.Microsecond)
	err := d.WithPlatformTx(context.Background(), func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `
			INSERT INTO tenants (id, slug, legal_name, display_name, created_at, updated_at)
			VALUES ($1, $2, $3, $3, $4, $4)`, id, slug, "Test store "+slug, now)
		return err
	})
	if err != nil {
		tb.Fatalf("testkit: create tenant: %v", err)
	}
	return Tenant{ID: id, Slug: slug}
}

// InTenant runs fn in a tenant transaction and fails the test on error.
func InTenant(tb testing.TB, d *db.DB, tenantID uuid.UUID, fn func(q db.Querier) error) {
	tb.Helper()
	if err := d.WithTenantTx(context.Background(), tenantID, fn); err != nil {
		tb.Fatalf("testkit: tenant transaction: %v", err)
	}
}
