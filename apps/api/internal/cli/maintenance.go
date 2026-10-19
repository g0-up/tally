package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/reporting"
)

func newMaintenanceCommand(lookup config.Lookup) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Run maintenance jobs once (the worker also schedules them)",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "purge-idempotency",
			Short: "Delete expired idempotency keys of every tenant",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := config.LoadCLI(lookup)
				if err != nil {
					return err
				}
				database, err := db.Open(cmd.Context(), cfg.Database.URL, db.Options{
					MaxConns:         2,
					StatementTimeout: cfg.Database.StatementTimeout,
					ApplicationName:  "tally-cli",
				})
				if err != nil {
					return err
				}
				defer func() { _ = database.Close() }()
				n, purgeErr := purgeIdempotency(cmd.Context(), database, clock.System{})
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "deleted %d expired idempotency keys\n", n); err != nil {
					return errors.Join(purgeErr, err)
				}
				return purgeErr
			},
		},
		newRevokeSessionsCommand(lookup),
		newExpireExportsCommand(lookup),
	)
	return cmd
}

// newExpireExportsCommand runs the export retention of every tenant once, as the worker does
// before each export run: the files of finished exports past their link are deleted and the
// exports marked expired, and exports a crashed runner left running are marked failed.
func newExpireExportsCommand(lookup config.Lookup) *cobra.Command {
	return &cobra.Command{
		Use:   "expire-exports",
		Short: "Delete export files past their retention",
		Long: "Delete the files of finished exports whose download link has expired and mark them expired,\n" +
			"and mark failed the exports a crashed worker left running, for every tenant the worker serves.\n" +
			"It reads the worker configuration (database and OBJECT_STORE_*). A file that cannot be deleted\n" +
			"keeps its export unexpired for the next run, and the command fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadWorker(lookup)
			if err != nil {
				return err
			}
			objects, err := objectstore.NewS3(objectstore.S3Config{
				Endpoint: cfg.ObjectStore.Endpoint, Region: cfg.ObjectStore.Region, Bucket: cfg.ObjectStore.Bucket,
				AccessKey: cfg.ObjectStore.AccessKey, SecretKey: cfg.ObjectStore.SecretKey,
			})
			if err != nil {
				return err
			}
			database, err := db.Open(cmd.Context(), cfg.Database.URL, db.Options{
				MaxConns:         2,
				StatementTimeout: cfg.Database.StatementTimeout,
				ApplicationName:  "tally-cli",
			})
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			clk := clock.System{}
			reports := app.NewDomains(database, clk, audit.NewRecorder(clk), app.WithObjectStore(objects)).Reporting
			n, expireErr := expireExports(cmd.Context(), database, reports)
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "expired %d exports\n", n); err != nil {
				return errors.Join(expireErr, err)
			}
			return expireErr
		},
	}
}

// expireExports runs the export retention of every active tenant and returns how many exports
// it expired. A tenant that fails is skipped; failures are returned joined. A tenant an operator
// holds in maintenance and a replaced sandbox are left alone on purpose, as the worker leaves
// them: a restore is rewriting the held tenant's rows, its files wait for its release, and an
// expired link is refused by its expiry whether or not its file is gone.
func expireExports(ctx context.Context, database *db.DB, reports *reporting.Service) (int, error) {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	err = eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		n, err := reports.ExpireExports(ctx, tenantID)
		total += n
		return err
	})
	return total, err
}

// newRevokeSessionsCommand signs out every session of a tenant, for an incident or a defect in
// session handling; owners sign in again.
func newRevokeSessionsCommand(lookup config.Lookup) *cobra.Command {
	var tenant, reason string
	cmd := &cobra.Command{
		Use:   "revoke-sessions",
		Short: "Sign out every session of a tenant",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return errors.New("--reason must not be empty")
			}
			return withAccounts(cmd, lookup, func(ctx context.Context, env accountEnv) error {
				id, err := ids.Parse(tenant)
				if err != nil {
					var found bool
					if id, found, err = env.stack.identity.ResolveSlug(ctx, strings.ToLower(strings.TrimSpace(tenant))); err != nil {
						return err
					} else if !found {
						return fmt.Errorf("no tenant has the id or address %q", tenant)
					}
				}
				n, err := env.stack.identity.RevokeTenantSessions(ctx, id, reason)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "revoked %d sessions of tenant %s\n", n, id)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id or store address (slug)")
	cmd.Flags().StringVar(&reason, "reason", "revoked_by_operator", "reason recorded on the sessions and in the audit log")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}
