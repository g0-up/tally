package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/seedload"
)

// The store of the development data set, the tenant host the browser suites sign in to.
const (
	devStoreSlug  = "cua-hang-a"
	devStoreName  = "Cửa hàng A"
	devOwnerName  = "Chủ cửa hàng A"
	devOwnerEmail = "chu-cua-hang-a@example.test"
	// devOwnerEmailEnv and devOwnerPasswordEnv are the variables the browser suites sign in with.
	devOwnerEmailEnv    = "E2E_OWNER_EMAIL"
	devOwnerPasswordEnv = "E2E_OWNER_PASSWORD" //nolint:gosec // the name of a variable, not a credential
)

// devSeedOptions is the small seedload run that fills the development store.
var devSeedOptions = seedload.Options{Tenants: 1, Skus: 40, Moves: 400, Seed: 1, SlugPrefix: "dev"}

func newSeedDevCommand(lookup config.Lookup) *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "Create the development store \"" + devStoreSlug + "\" with a little sample data",
		Long: "Create the store \"" + devStoreSlug + "\" (" + devStoreName + ") and fill it like `tally seed load` with\n" +
			fmt.Sprintf("%d SKUs and at least %d stock moves (seed %d). Its owner is %s (default %s);\n",
				devSeedOptions.Skus, devSeedOptions.Moves, devSeedOptions.Seed, devOwnerEmailEnv, devOwnerEmail) +
			"with " + devOwnerPasswordEnv + " set the owner is activated with that password, otherwise they stay\n" +
			"pending verification. The store is past onboarding: its opening balances are confirmed with\n" +
			"yesterday as the cutoff, so the app opens on its screens. When the store already exists nothing\n" +
			"is changed, so running it again is safe; it fails when that store holds less than the sample\n" +
			"data, since `tally tenant create` or a failed seed dev made it. Runs only with APP_ENV set to development or test (an unset APP_ENV is\n" +
			"refused; make seed sets development).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDevelopmentOrTest(lookup, "seed dev"); err != nil {
				return err
			}
			email := devOwnerEmail
			if v, ok := lookup(devOwnerEmailEnv); ok && v != "" {
				email = v
			}
			password, _ := lookup(devOwnerPasswordEnv)
			return withLoadAccounts(cmd, lookup, func(ctx context.Context, env accountEnv, policy *org.SlugPolicy) error {
				return runSeedDev(ctx, cmd.OutOrStdout(), env, policy, email, password)
			})
		},
	}
}

// runSeedDev creates and fills the development store unless it already exists.
func runSeedDev(ctx context.Context, w io.Writer, env accountEnv, policy *org.SlugPolicy, email, password string) error {
	if reason := policy.Reason(devStoreSlug); reason != "" {
		return fmt.Errorf("tenant address %q is not allowed (%s)", devStoreSlug, reason)
	}
	id, found, err := env.stack.identity.ResolveSlug(ctx, devStoreSlug)
	if err != nil {
		return err
	}
	if found {
		return reportExistingDevStore(ctx, w, env.database, id)
	}
	t, err := createSeedTenant(ctx, env, 1, identity.OwnerSpec{
		Tenant:   identity.TenantSpec{ID: ids.New(), Slug: devStoreSlug, DisplayName: devStoreName},
		Email:    email,
		FullName: devOwnerName,
		Source:   audit.SourceCLI,
	}, password)
	if err != nil {
		return err
	}
	state := "pending verification (set " + devOwnerPasswordEnv + " to activate)"
	if password != "" {
		state = "active"
	}
	if _, err := fmt.Fprintf(w, "created tenant %s id=%s owner=%s %s\n", t.Slug, t.ID, email, state); err != nil {
		return err
	}
	deps := seedDeps(env)
	s, err := seedload.Run(ctx, deps, devSeedOptions, t, nil)
	if err != nil {
		return fmt.Errorf("seed dev of tenant %s (created, partly filled): %w", t.Slug, err)
	}
	if err := finishDevOnboarding(ctx, deps.Org, env.stack.clock, t); err != nil {
		return fmt.Errorf("seed dev of tenant %s (filled): %w", t.Slug, err)
	}
	return reportSeedTenant(ctx, w, env, t, s)
}

// finishDevOnboarding takes the filled store through its onboarding checklist as its owner, the
// way the wizard does: the required steps are completed and the opening balances confirmed with
// yesterday as the cutoff, so the period lock leaves today's postings open. The browser suites
// sign in to this store and expect the app's screens, not the wizard the gate sends a new store to.
func finishDevOnboarding(ctx context.Context, svc *org.Service, clk clock.Clock, t seedload.Tenant) error {
	actor := audit.Actor{TenantID: t.ID, Type: audit.ActorOwner, ID: t.OwnerID, Source: audit.SourceCLI}
	tenant, err := svc.Tenant(ctx, t.ID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(tenant.Timezone)
	if err != nil {
		return fmt.Errorf("timezone of the store: %w", err)
	}
	cutoff := clock.BusinessDate(clk.Now(), loc).AddDate(0, 0, -1).Format(time.DateOnly)
	if _, err := svc.UpdateTenant(ctx, actor, tenant.Version, org.TenantPatch{OpeningCutoffDate: &cutoff}); err != nil {
		return fmt.Errorf("set the opening cutoff: %w", err)
	}
	for _, step := range []string{org.StepStoreProfile, org.StepCatalogSetup, org.StepOpeningStock, org.StepOpeningDebt, org.StepOpeningCash} {
		if _, err := svc.CompleteStep(ctx, actor, step); err != nil {
			return fmt.Errorf("complete onboarding step %s: %w", step, err)
		}
	}
	if _, _, err := svc.ConfirmOpeningBalances(ctx, actor); err != nil {
		return fmt.Errorf("confirm the opening balances: %w", err)
	}
	return nil
}

// reportExistingDevStore leaves an existing development store as it is. It fails when the store
// holds less than seed dev fills it with: another command created it or its seeding failed half
// way, and the suites that sign in to it would run against missing data.
func reportExistingDevStore(ctx context.Context, w io.Writer, database *db.DB, id uuid.UUID) error {
	var skus, moves int
	err := database.WithReadOnlyTenantTx(ctx, id, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT (SELECT count(*) FROM skus), (SELECT count(*) FROM stock_moves)`).Scan(&skus, &moves)
	})
	if err != nil {
		return fmt.Errorf("count the data of tenant %s: %w", devStoreSlug, err)
	}
	if skus < devSeedOptions.Skus || moves < devSeedOptions.Moves {
		return fmt.Errorf("tenant %s already exists id=%s with %d SKUs and %d stock moves, less than the %d and %d seed dev fills: "+
			"it was not made by seed dev or its seeding failed; remove it or reset the database, then run seed dev again",
			devStoreSlug, id, skus, moves, devSeedOptions.Skus, devSeedOptions.Moves)
	}
	_, err = fmt.Fprintf(w, "tenant %s already exists id=%s with %d SKUs and %d stock moves; nothing seeded\n", devStoreSlug, id, skus, moves)
	return err
}
