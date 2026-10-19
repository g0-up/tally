package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// ledgerVerifier checks the projections of the tenant of q against its ledgers and returns one
// line per mismatch. rebuild, when set, rewrites the rows the verify checks named by rebuilds
// find wrong to what those checks expect; a domain without it is only verified.
type ledgerVerifier struct {
	name     string
	verify   func(ctx context.Context, q db.Querier) ([]string, error)
	rebuilds func() []string
	rebuild  func(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) ([]posting.Rebuilt, error)
}

// ledgerVerifiers are the verifiers of every domain that keeps an append-only ledger. A rebuild
// runs them in this order; none of them reads a projection another one rewrites.
var ledgerVerifiers = []ledgerVerifier{
	{name: "debt", verify: debt.Verify, rebuilds: debt.RebuildChecks, rebuild: inTenant(debt.Rebuild)},
	{name: "cash", verify: cash.Verify, rebuilds: cash.RebuildChecks, rebuild: inTenant(cash.Rebuild)},
	{name: "inventory", verify: inventory.Verify, rebuilds: inventory.RebuildChecks, rebuild: inventory.Rebuild},
	{name: "procurement", verify: procurement.Verify, rebuilds: procurement.RebuildChecks, rebuild: inTenant(procurement.Rebuild)},
	{name: "sales", verify: sales.Verify, rebuilds: sales.RebuildChecks, rebuild: inTenant(sales.Rebuild)},
	{name: "kits", verify: kits.Verify, rebuilds: kits.RebuildChecks, rebuild: inTenant(kits.Rebuild)},
}

// inTenant adapts a rebuild that reads the tenant from the transaction scope alone.
func inTenant(rebuild func(context.Context, db.Querier, time.Time) ([]posting.Rebuilt, error)) func(context.Context, db.Querier, uuid.UUID, time.Time) ([]posting.Rebuilt, error) {
	return func(ctx context.Context, q db.Querier, _ uuid.UUID, now time.Time) ([]posting.Rebuilt, error) {
		return rebuild(ctx, q, now)
	}
}

// skuVerifiers narrows the run to the stock ledger of one SKU (--sku).
func skuVerifiers(sku string) ([]ledgerVerifier, error) {
	id, err := ids.Parse(sku)
	if err != nil {
		return nil, fmt.Errorf("--sku must be a sku id: %w", err)
	}
	verify := func(ctx context.Context, q db.Querier) ([]string, error) {
		return inventory.VerifySkus(ctx, q, []uuid.UUID{id})
	}
	return []ledgerVerifier{{name: "inventory", verify: verify}}, nil
}

// verifyLedgers runs every verifier for the tenant (every tenant when tenant is empty), each in a
// read-only tenant transaction, and prints one line per mismatch. It fails when a check fails or
// any projection disagrees with its ledger.
func verifyLedgers(ctx context.Context, database *db.DB, tenant string, verifiers []ledgerVerifier, w io.Writer) error {
	var tenants []uuid.UUID
	if tenant != "" {
		id, err := ids.Parse(tenant)
		if err != nil {
			return fmt.Errorf("--tenant must be a tenant id: %w", err)
		}
		tenants = []uuid.UUID{id}
	} else {
		all, err := database.TenantIDs(ctx)
		if err != nil {
			return err
		}
		tenants = all
	}
	mismatches := 0
	for _, tenantID := range tenants {
		err := database.WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
			for _, v := range verifiers {
				lines, err := v.verify(ctx, q)
				if err != nil {
					return err
				}
				for _, line := range lines {
					mismatches++
					if _, err := fmt.Fprintf(w, "tenant %s %s: %s\n", tenantID, v.name, line); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("verify tenant %s: %w", tenantID, err)
		}
	}
	if _, err := fmt.Fprintf(w, "verified %d tenants: %d mismatches\n", len(tenants), mismatches); err != nil {
		return err
	}
	if mismatches > 0 {
		return fmt.Errorf("%d projections disagree with their ledgers", mismatches)
	}
	return nil
}

func newLedgerCommand(lookup config.Lookup) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Check or rebuild projections from the append-only ledgers",
	}
	var verifyTenant, verifySku string
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Prove every projection equals the sum of its ledger (non-zero exit on mismatch)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			verifiers := ledgerVerifiers
			if verifySku != "" {
				var err error
				if verifiers, err = skuVerifiers(verifySku); err != nil {
					return err
				}
			}
			database, err := openLedgerDatabase(cmd.Context(), lookup)
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			return verifyLedgers(cmd.Context(), database, verifyTenant, verifiers, cmd.OutOrStdout())
		},
	}
	verify.Flags().StringVar(&verifyTenant, "tenant", "", "tenant id (default: every tenant)")
	verify.Flags().StringVar(&verifySku, "sku", "", "sku id: verify only the stock ledger of that sku")

	cmd.AddCommand(verify, newLedgerRebuildCommand(lookup))
	return cmd
}

// openLedgerDatabase opens the application database for a ledger command and refuses a role
// that bypasses row level security.
func openLedgerDatabase(ctx context.Context, lookup config.Lookup) (*db.DB, error) {
	cfg, err := config.LoadCLI(lookup)
	if err != nil {
		return nil, err
	}
	database, err := db.Open(ctx, cfg.Database.URL, db.Options{
		MaxConns:         2,
		StatementTimeout: cfg.Database.StatementTimeout,
		ApplicationName:  "tally-cli",
	})
	if err != nil {
		return nil, err
	}
	if err := requireAppRole(ctx, database); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// requireAppRole fails when the connected role is a superuser or has BYPASSRLS. The ledger
// commands scope a tenant by row level security; under such a role a verify would sum and a
// rebuild would rewrite the rows of every tenant.
func requireAppRole(ctx context.Context, database *db.DB) error {
	var role struct {
		Name     string `db:"rolname"`
		Bypasses bool   `db:"bypasses"`
	}
	err := database.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.GetContext(ctx, q, &role,
			`SELECT rolname, rolsuper OR rolbypassrls AS bypasses FROM pg_roles WHERE rolname = current_user`)
	})
	if err != nil {
		return fmt.Errorf("read the connected role: %w", err)
	}
	if role.Bypasses {
		return fmt.Errorf("role %s bypasses row level security; run the ledger commands as the application role (the DATABASE_URL of the API, not MIGRATE_DATABASE_URL or a superuser)", role.Name)
	}
	return nil
}
