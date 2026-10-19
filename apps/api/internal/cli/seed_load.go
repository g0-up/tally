package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
	"github.com/g0-up/tally/apps/api/internal/seedload"
)

// seedLoadPasswordEnv names the variable holding the password the load owners are activated with.
const seedLoadPasswordEnv = "SEED_LOAD_OWNER_PASSWORD" //nolint:gosec // the name of a variable, not a credential

// seedLoadProgressEvery is how often a running tenant reports its progress.
const seedLoadProgressEvery = 30 * time.Second

func newSeedLoadCommand(lookup config.Lookup) *cobra.Command {
	opts := seedload.Options{
		Tenants: seedload.DefaultTenants, Skus: seedload.DefaultSkus, Moves: seedload.DefaultMoves,
		Seed: seedload.DefaultSeed, SlugPrefix: seedload.DefaultSlugPrefix,
	}
	cmd := &cobra.Command{
		Use:   "load",
		Short: "Create tenants filled with realistic load-test data",
		Long: "Create --tenants new tenants (\"<slug-prefix>-<seed>-<n>\") and fill each with --skus SKUs (kits and\n" +
			"combos included), suppliers, customers and rounds of receipts, POS sales, invoices with stock outs,\n" +
			"returns, payments, stock counts and kit assembly until it holds at least --moves stock moves. Every\n" +
			"document is posted through the domain services; the ledgers are verified at the end. The same\n" +
			"--seed gives the same documents and stock; row ids and timestamps differ.\n\n" +
			"The owners stay pending verification unless " + seedLoadPasswordEnv + " is set, in which case\n" +
			"they are activated with that password. An existing slug is an error. Runs only with APP_ENV set\n" +
			"to development or test (an unset APP_ENV is refused).\n\n" +
			"Each tenant posts about 95 stock moves a second, so the default 1 000 000 moves take about 3 hours;\n" +
			fmt.Sprintf("up to %d tenants post in parallel.", seedload.Concurrency),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if err := requireDevelopmentOrTest(lookup, "seed load"); err != nil {
				return err
			}
			return withLoadAccounts(cmd, lookup, func(ctx context.Context, env accountEnv, policy *org.SlugPolicy) error {
				password, _ := lookup(seedLoadPasswordEnv)
				return runSeedLoad(ctx, cmd.OutOrStdout(), env, policy, opts, password)
			})
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.Tenants, "tenants", opts.Tenants, "number of tenants to create")
	f.IntVar(&opts.Skus, "skus", opts.Skus, fmt.Sprintf("SKUs per tenant, kits included (%d..%d)", seedload.MinSkus, seedload.MaxSkus))
	f.IntVar(&opts.Moves, "moves", opts.Moves, "stock moves each tenant reaches at least")
	f.Int64Var(&opts.Seed, "seed", opts.Seed, "seed of the generated data")
	f.StringVar(&opts.SlugPrefix, "slug-prefix", opts.SlugPrefix, "first part of the tenant slugs")
	return cmd
}

// requireDevelopmentOrTest stops a command that creates throwaway tenants with generated data
// unless APP_ENV is set explicitly to development or test, so a shell that reaches a production
// database without APP_ENV is refused too.
func requireDevelopmentOrTest(lookup config.Lookup, command string) error {
	base, err := config.LoadBase(lookup)
	if err != nil {
		return err
	}
	if !explicitDevelopmentOrTest(lookup, base) {
		return fmt.Errorf("%s creates tenants with generated data and runs only with APP_ENV set to development or test", command)
	}
	return nil
}

// withLoadAccounts is withAccounts with a pool sized for the tenants posting in parallel, and the
// slug policy of the configuration.
func withLoadAccounts(cmd *cobra.Command, lookup config.Lookup, fn func(ctx context.Context, env accountEnv, policy *org.SlugPolicy) error) error {
	cfg, err := config.LoadProvision(lookup)
	if err != nil {
		return err
	}
	logger, err := logging.New(cmd.ErrOrStderr(), cfg.LogLevel)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	database, err := db.Open(ctx, cfg.Database.URL, db.Options{
		// Room for each tenant's posting to hold a few connections at once, plus the slug checks.
		MaxConns:         4*seedload.Concurrency + 2,
		StatementTimeout: cfg.Database.StatementTimeout,
		ApplicationName:  "tally-cli-seed-load",
	})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	stack, err := newAccountStack(database, accountConfig{
		HTTP: cfg.HTTP, Secrets: cfg.Secrets, Mail: cfg.Mail, Location: cfg.Location,
	}, logger, nil)
	if err != nil {
		return err
	}
	defer stack.identity.WaitMail()
	env := accountEnv{stack: stack, database: database, production: cfg.IsProduction()}
	return fn(ctx, env, org.NewSlugPolicy(cfg.HTTP.ReservedSlugs))
}

// runSeedLoad creates the tenants, fills them in parallel and verifies their ledgers.
func runSeedLoad(ctx context.Context, w io.Writer, env accountEnv, policy *org.SlugPolicy, opts seedload.Options, password string) error {
	if err := checkLoadSlugs(ctx, env, policy, opts); err != nil {
		return err
	}
	out := &syncWriter{w: w}
	tenants := make([]seedload.Tenant, opts.Tenants)
	for i := range tenants {
		t, err := createLoadTenant(ctx, env, opts, i+1, password)
		if err != nil {
			return err
		}
		state := "pending verification (set " + seedLoadPasswordEnv + " to activate)"
		if password != "" {
			state = "active"
		}
		out.printf("created tenant %s id=%s owner=%s %s\n", t.Slug, t.ID, seedload.OwnerEmail(t.Slug), state)
		tenants[i] = t
	}
	deps := seedDeps(env)
	summaries := make([]seedload.Summary, len(tenants))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(seedload.Concurrency)
	for i, t := range tenants {
		g.Go(func() error {
			next := time.Now().Add(seedLoadProgressEvery)
			progress := func(s seedload.Summary) {
				if time.Now().Before(next) {
					return
				}
				next = time.Now().Add(seedLoadProgressEvery)
				out.printf("progress %s: round %d, %d stock moves, %s\n", s.Slug, s.Rounds, s.Moves, s.Elapsed.Round(time.Second))
			}
			s, err := seedload.Run(gctx, deps, opts, t, progress)
			if err != nil {
				return fmt.Errorf("seed load of tenant %s (created, partly filled): %w", t.Slug, err)
			}
			summaries[i] = s
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for i, t := range tenants {
		if err := reportSeedTenant(ctx, out, env, t, summaries[i]); err != nil {
			return err
		}
	}
	return out.err
}

// seedDeps are the services seedload posts through.
func seedDeps(env accountEnv) seedload.Deps {
	return seedload.Deps{
		DB: env.database, Domains: env.stack.domains,
		Org: org.NewService(org.Deps{DB: env.database, Clock: env.stack.clock, Audit: env.stack.audit, Provisioner: env.stack.provisioner}),
	}
}

// reportSeedTenant verifies the ledgers of a filled tenant and prints its summary.
func reportSeedTenant(ctx context.Context, w io.Writer, env accountEnv, t seedload.Tenant, s seedload.Summary) error {
	if err := verifyLedgers(ctx, env.database, t.ID.String(), ledgerVerifiers, w); err != nil {
		return fmt.Errorf("tenant %s: %w", t.Slug, err)
	}
	skus, err := countSkus(ctx, env.database, t.ID)
	if err != nil {
		return err
	}
	s.Skus = skus
	return s.Write(w)
}

// checkLoadSlugs fails before anything is created when a slug of the run is not allowed or taken.
func checkLoadSlugs(ctx context.Context, env accountEnv, policy *org.SlugPolicy, opts seedload.Options) error {
	for n := 1; n <= opts.Tenants; n++ {
		slug := opts.Slug(n)
		if reason := policy.Reason(slug); reason != "" {
			return fmt.Errorf("tenant address %q is not allowed (%s); choose another --slug-prefix", slug, reason)
		}
		_, found, err := env.stack.identity.ResolveSlug(ctx, slug)
		if err != nil {
			return err
		}
		if found {
			return fmt.Errorf("tenant address %q already exists; choose another --seed or --slug-prefix", slug)
		}
	}
	return nil
}

// createLoadTenant creates tenant n of the run and its owner, activated with password when given.
func createLoadTenant(ctx context.Context, env accountEnv, opts seedload.Options, n int, password string) (seedload.Tenant, error) {
	slug := opts.Slug(n)
	return createSeedTenant(ctx, env, n, identity.OwnerSpec{
		Tenant:   identity.TenantSpec{ID: ids.New(), Slug: slug, DisplayName: fmt.Sprintf("Cửa hàng thử tải %d-%d", opts.Seed, n)},
		Email:    seedload.OwnerEmail(slug),
		FullName: fmt.Sprintf("Chủ cửa hàng %d-%d", opts.Seed, n),
		Source:   audit.SourceCLI,
	}, password)
}

// createSeedTenant creates a tenant and its owner for generated data, number n of its run, and
// activates the owner with password when given.
func createSeedTenant(ctx context.Context, env accountEnv, n int, spec identity.OwnerSpec, password string) (seedload.Tenant, error) {
	slug := spec.Tenant.Slug
	created, err := env.stack.identity.CreateTenantWithOwner(ctx, spec)
	if err != nil {
		return seedload.Tenant{}, fmt.Errorf("create tenant %s: %w", slug, err)
	}
	t := seedload.Tenant{Index: n, ID: created.Tenant.ID, OwnerID: created.Owner.ID, Slug: created.Tenant.Slug}
	if password == "" {
		return t, nil
	}
	token, err := verifyToken(created.VerifyURL)
	if err != nil {
		return seedload.Tenant{}, fmt.Errorf("tenant %s: %w", slug, err)
	}
	if err := env.stack.identity.VerifyEmail(ctx, identity.VerifyInput{HostTenant: t.ID, Token: token, Password: password}); err != nil {
		return seedload.Tenant{}, fmt.Errorf("activate the owner of tenant %s: %w", slug, err)
	}
	return t, nil
}

// verifyToken reads the token of a verification link. Its errors never quote the link, which
// holds the token.
func verifyToken(link string) (string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", errors.New("the verification link cannot be parsed")
	}
	token := u.Query().Get("token")
	if token == "" {
		return "", errors.New("the verification link holds no token")
	}
	return token, nil
}

// countSkus counts the SKUs of a tenant.
func countSkus(ctx context.Context, database *db.DB, tenantID uuid.UUID) (int, error) {
	var n int
	err := database.WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM skus`).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("count skus: %w", err)
	}
	return n, nil
}

// syncWriter serializes the output of the tenants running in parallel and keeps the first error.
type syncWriter struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.w.Write(p)
	if err != nil && s.err == nil {
		s.err = fmt.Errorf("write output: %w", err)
	}
	return n, err
}

func (s *syncWriter) printf(format string, args ...any) { _, _ = fmt.Fprintf(s, format, args...) }
