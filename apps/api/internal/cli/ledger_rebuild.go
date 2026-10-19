package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

const ledgerRebuildLong = `Recompute the projections of one tenant from its append-only ledgers. Every row a
` + "`tally ledger verify`" + ` check finds disagreeing with its ledger is rewritten to the value that
check expects: verify and rebuild share one definition of each projection.

The rebuild runs in one transaction of the tenant holding every posting lock of the tenant,
taken in the global lock order, so the tenant's postings wait until it ends. Inside that
transaction the tenant is verified again after the rewrite. A row whose ledger gives no valid
value (a negative sum, lots that disagree with the stock moves) keeps its stored value, and a
document that disagrees with its own lines is not a projection: both need a manual fix.

By default (or with --dry-run) the command is a dry run: it rebuilds, prints each difference
verify finds marked "rebuild" when the rebuild clears it or "manual" when it remains, and the
rows it would rewrite per check, then rolls back. Nothing is written.

Each statement of the rebuild may run for --statement-timeout (10m by default, 0 for no limit)
instead of the DB_STATEMENT_TIMEOUT of the API: the rebuild reads every ledger of the tenant.

--confirm writes. TAKE A BACKUP FIRST (make db-backup, pg_dump -Fc): only restoring one undoes
a rebuild. The run is recorded in the tenant's audit log as ledger.rebuild and committed only
when the verify after the rewrite is clean; when differences remain, everything is rolled
back and the command fails.`

// ledgerRebuildAction is the audit action of a rebuild.
const ledgerRebuildAction = "ledger.rebuild"

var (
	// errRebuildDryRun rolls back the transaction of a dry run.
	errRebuildDryRun = errors.New("dry run")
	// errRebuildLeftDifferences rolls back a rebuild the verify after it still finds wrong.
	errRebuildLeftDifferences = errors.New("differences remain")
)

func newLedgerRebuildCommand(lookup config.Lookup) *cobra.Command {
	var tenant string
	var dryRun, confirm bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Recompute the projections of one tenant from its ledgers (dry run unless --confirm; back up first)",
		Long:  ledgerRebuildLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tenantID, err := ids.Parse(tenant)
			if err != nil {
				return fmt.Errorf("--tenant must be a tenant id: %w", err)
			}
			if timeout < 0 {
				return errors.New("--statement-timeout must not be negative")
			}
			database, err := openLedgerDatabase(cmd.Context(), lookup)
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			if err := requireTenant(cmd.Context(), database, tenantID); err != nil {
				return err
			}
			if confirm {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: rewriting the projections of tenant %s; only a backup restores them\n", tenantID)
			}
			return rebuildLedgers(cmd.Context(), database, clock.System{}, tenantID, timeout, confirm, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the differences and the rows the rebuild would rewrite, writing nothing (the default)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "write the rebuilt projections (take a backup first)")
	cmd.Flags().DurationVar(&timeout, "statement-timeout", 10*time.Minute, "longest a statement of the rebuild may run (0: no limit)")
	_ = cmd.MarkFlagRequired("tenant")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "confirm")
	return cmd
}

// requireTenant fails unless the tenant exists: under RLS an unknown tenant reads as an empty one.
func requireTenant(ctx context.Context, database *db.DB, tenantID uuid.UUID) error {
	all, err := database.TenantIDs(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(all, tenantID) {
		return fmt.Errorf("no tenant has the id %s", tenantID)
	}
	return nil
}

// rebuildResult is what one rebuild of a tenant found and did.
type rebuildResult struct {
	before, after []string                     // verify lines before and after the rewrite
	rebuilt       map[string][]posting.Rebuilt // rows rewritten per domain
	total         int64
}

// rebuildTenant runs the rebuild of each domain that has one, in ledgerVerifiers order, on the
// tenant of q, verifying the tenant before and after. The caller holds the tenant's posting locks.
func rebuildTenant(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) (rebuildResult, error) {
	res := rebuildResult{rebuilt: map[string][]posting.Rebuilt{}}
	var err error
	if res.before, err = verifyTenant(ctx, q); err != nil {
		return res, err
	}
	for _, v := range ledgerVerifiers {
		if v.rebuild == nil {
			continue
		}
		done, err := v.rebuild(ctx, q, tenantID, now)
		if err != nil {
			return res, err
		}
		for _, r := range done {
			res.total += r.Rows
		}
		res.rebuilt[v.name] = done
	}
	res.after, err = verifyTenant(ctx, q)
	return res, err
}

// verifyTenant returns the difference lines of every verifier for the tenant of q, each opened
// by its domain.
func verifyTenant(ctx context.Context, q db.Querier) ([]string, error) {
	var out []string
	for _, v := range ledgerVerifiers {
		lines, err := v.verify(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			out = append(out, v.name+": "+line)
		}
	}
	return out, nil
}

// differenceKey identifies the row a difference line reports: the line up to and including its
// first id and the label in parentheses that follows it, since the stored and expected values
// that follow change with a rebuild. The label tells apart the rows of one document (the
// component SKUs of an assembly order).
func differenceKey(line string) string {
	end := 0
	for _, field := range strings.Fields(line) {
		end = strings.Index(line[end:], field) + end + len(field)
		if _, err := uuid.Parse(field); err != nil {
			continue
		}
		if rest := line[end:]; strings.HasPrefix(rest, " (") {
			if paren := strings.Index(rest, "): "); paren >= 0 {
				return line[:end+paren+1]
			}
		}
		return line[:end]
	}
	return line
}

// rebuildLedgers rebuilds the tenant in one tenant transaction holding every posting lock of the
// tenant and verifies it again in that transaction. A dry run (write false) always rolls back; a
// written run records the rebuild in the audit log and commits only when the verify is clean.
// It prints every difference found before the rebuild, marked by whether the rebuild clears it,
// and the rows rewritten per check. Each statement may run for timeout; 0 sets no limit.
func rebuildLedgers(ctx context.Context, database *db.DB, clk clock.Clock, tenantID uuid.UUID, timeout time.Duration, write bool, w io.Writer) error {
	var res rebuildResult
	err := database.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		ms := strconv.FormatInt(timeout.Milliseconds(), 10)
		if _, err := q.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, ms); err != nil {
			return fmt.Errorf("set statement timeout: %w", err)
		}
		if err := posting.LockTenant(ctx, q); err != nil {
			return err
		}
		var err error
		if res, err = rebuildTenant(ctx, q, tenantID, clk.Now()); err != nil {
			return err
		}
		if !write {
			return errRebuildDryRun
		}
		if len(res.after) > 0 {
			return errRebuildLeftDifferences
		}
		actor := audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: audit.SourceCLI}
		return audit.NewRecorder(clk).Record(ctx, q, actor, audit.Event{
			Action: ledgerRebuildAction, EntityType: "tenant", EntityID: tenantID, After: res.rebuilt,
		})
	})
	if err != nil && !errors.Is(err, errRebuildDryRun) && !errors.Is(err, errRebuildLeftDifferences) {
		return fmt.Errorf("rebuild tenant %s: %w", tenantID, err)
	}
	cleared, err := printRebuild(w, tenantID, res, write)
	if err != nil {
		return err
	}
	switch {
	case !write:
		_, err = fmt.Fprintf(w, "dry run of tenant %s: %d differences, %d to rebuild, %d to fix by hand; nothing written (take a backup, then pass --confirm to write)\n",
			tenantID, cleared+len(res.after), cleared, len(res.after))
		return err
	case len(res.after) > 0:
		return fmt.Errorf("rebuild of tenant %s rolled back, nothing written: %d differences remain that it cannot recompute (ledger or document errors, fix by hand)",
			tenantID, len(res.after))
	default:
		_, err = fmt.Fprintf(w, "rebuilt tenant %s: %d rows; verified: 0 differences\n", tenantID, res.total)
		return err
	}
}

// printRebuild prints the differences of a rebuild, each marked "rebuild" when the rebuild
// clears it and "manual" when it remains, then any the rebuild left that were not there before,
// then the rows rewritten per check. It returns how many differences the rebuild clears.
func printRebuild(w io.Writer, tenantID uuid.UUID, res rebuildResult, write bool) (int, error) {
	stays := make(map[string]bool, len(res.after))
	for _, line := range res.after {
		stays[differenceKey(line)] = true
	}
	seen := make(map[string]bool, len(res.before))
	cleared := 0
	for _, line := range res.before {
		seen[differenceKey(line)] = true
		mark := "rebuild"
		if stays[differenceKey(line)] {
			mark = "manual"
		} else {
			cleared++
		}
		if _, err := fmt.Fprintf(w, "%s tenant %s %s\n", mark, tenantID, line); err != nil {
			return 0, err
		}
	}
	for _, line := range res.after {
		if seen[differenceKey(line)] {
			continue
		}
		if _, err := fmt.Fprintf(w, "manual tenant %s %s (after the rebuild)\n", tenantID, line); err != nil {
			return 0, err
		}
	}
	verb := "would rebuild"
	if write && len(res.after) == 0 {
		verb = "rebuilt"
	}
	for _, v := range ledgerVerifiers {
		for _, r := range res.rebuilt[v.name] {
			if _, err := fmt.Fprintf(w, "%s tenant %s %s: %s: %d rows\n", verb, tenantID, v.name, r.Check, r.Rows); err != nil {
				return 0, err
			}
		}
	}
	return cleared, nil
}
