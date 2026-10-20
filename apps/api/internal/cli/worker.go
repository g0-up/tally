package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/scanner"
)

// Worker schedules.
const (
	defaultDispatchInterval     = 2 * time.Second
	defaultPurgeInterval        = 24 * time.Hour
	defaultStockInterval        = time.Hour
	defaultShiftInterval        = time.Hour
	defaultSweepInterval        = time.Hour
	defaultExportInterval       = 15 * time.Second
	defaultSubscriptionInterval = time.Hour
	defaultUsageInterval        = 24 * time.Hour
)

type workerSchedule struct {
	dispatch time.Duration
	purge    time.Duration
	// stock is how often expired lots are rolled and stock signals detected.
	stock time.Duration
	// shift is how often the POS shifts left open from an earlier business date are closed.
	shift time.Duration
	// sweep is how often the notification centre re-checks every condition it raises.
	sweep time.Duration
	// export is how often queued report and full data exports are run and finished ones expired.
	export time.Duration
	// subscription is how often due subscription transitions and account closures are applied.
	// Both are due once a day in each store's timezone; running hourly applies them soon after.
	subscription time.Duration
	// usage is how often the daily usage counters are recorded.
	usage time.Duration
}

func (s workerSchedule) valid() bool {
	return s.dispatch > 0 && s.purge > 0 && s.stock > 0 && s.shift > 0 && s.sweep > 0 && s.export > 0 &&
		s.subscription > 0 && s.usage > 0
}

func newWorkerCommand(version string, lookup config.Lookup) *cobra.Command {
	sched := workerSchedule{}
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run background jobs: outbox dispatch and scheduled maintenance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !sched.valid() {
				return fmt.Errorf("--dispatch-interval, --purge-interval, --stock-interval, --shift-interval, --sweep-interval, " +
					"--export-interval, --subscription-interval and --usage-interval must be positive")
			}
			cfg, err := config.LoadWorker(lookup)
			if err != nil {
				return err
			}
			logger, err := logging.New(cmd.OutOrStdout(), cfg.LogLevel)
			if err != nil {
				return err
			}
			listeners, err := listen(cfg.MetricsAddr)
			if err != nil {
				return err
			}
			return runWorker(cmd.Context(), cfg, sched, logger.With("version", version), listeners[0])
		},
	}
	cmd.Flags().DurationVar(&sched.dispatch, "dispatch-interval", defaultDispatchInterval, "how often pending outbox events are dispatched")
	cmd.Flags().DurationVar(&sched.purge, "purge-interval", defaultPurgeInterval, "how often expired idempotency keys are purged (first run at start)")
	cmd.Flags().DurationVar(&sched.stock, "stock-interval", defaultStockInterval, "how often expired lots are rolled and stock signals detected, per tenant (first run at start)")
	cmd.Flags().DurationVar(&sched.shift, "shift-interval", defaultShiftInterval, "how often POS shifts left open from an earlier business date are closed, per tenant (first run at start)")
	cmd.Flags().DurationVar(&sched.sweep, "sweep-interval", defaultSweepInterval, "how often notifications are re-checked and resolved, per tenant (first run at start)")
	cmd.Flags().DurationVar(&sched.export, "export-interval", defaultExportInterval, "how often queued report and full data exports are run and finished ones expired (first run at start)")
	cmd.Flags().DurationVar(&sched.subscription, "subscription-interval", defaultSubscriptionInterval, "how often due subscription transitions and account closures are applied (first run at start)")
	cmd.Flags().DurationVar(&sched.usage, "usage-interval", defaultUsageInterval, "how often the daily usage counters are recorded (first run at start)")
	return cmd
}

// runWorker runs the outbox dispatcher, the scheduled jobs and the metrics listener until ctx is
// cancelled. Job failures are logged and retried on the next tick, never fatal.
func runWorker(ctx context.Context, cfg config.Worker, sched workerSchedule, logger *slog.Logger, metricsLn net.Listener) error {
	defer closeListeners(metricsLn)
	database, err := db.Open(ctx, cfg.Database.URL, db.Options{
		MaxConns:         cfg.Database.MaxConns,
		StatementTimeout: cfg.Database.StatementTimeout,
		ApplicationName:  "tally-worker",
	})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	clk := clock.System{}
	m := metrics.New()
	dispatcher := outbox.NewDispatcher(database, clk, m, logger)
	stock := inventory.NewService(inventory.Deps{DB: database, Clock: clk, Audit: audit.NewRecorder(clk)})
	dispatcher.Subscribe(inventory.TopicRecostSuggested, stock.HandleRecostSuggested)
	shifts := sales.NewService(sales.Deps{DB: database, Clock: clk, Audit: audit.NewRecorder(clk)})
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: cfg.ObjectStore.Endpoint, Region: cfg.ObjectStore.Region, Bucket: cfg.ObjectStore.Bucket,
		AccessKey: cfg.ObjectStore.AccessKey, SecretKey: cfg.ObjectStore.SecretKey,
	})
	if err != nil {
		return err
	}
	rec := audit.NewRecorder(clk)
	domains := app.NewDomains(database, clk, rec, app.WithObjectStore(objects))
	// The account closures the worker completes mail their owners the final export's link.
	smtp, err := newSMTP(cfg.Mail)
	if err != nil {
		return err
	}
	accounts, _, err := newIdentity(database, accountConfig{
		HTTP: cfg.HTTP, Secrets: cfg.Secrets, Mail: cfg.Mail, Location: cfg.Location,
	}, domains, clk, rec, smtp, logger, nil)
	if err != nil {
		return err
	}
	defer accounts.WaitMail()
	for topic, h := range domains.CRM.Handlers() {
		dispatcher.Subscribe(topic, h)
	}
	for topic, h := range domains.Notification.Handlers() {
		dispatcher.Subscribe(topic, h)
	}
	for topic, h := range org.OnboardingHandlers() {
		dispatcher.Subscribe(topic, h)
	}

	logger.Info("worker started", "metrics_addr", metricsLn.Addr().String(),
		"dispatch_interval", sched.dispatch.String(), "purge_interval", sched.purge.String(),
		"stock_interval", sched.stock.String(), "shift_interval", sched.shift.String(),
		"sweep_interval", sched.sweep.String(), "export_interval", sched.export.String(),
		"subscription_interval", sched.subscription.String(), "usage_interval", sched.usage.String())
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return dispatcher.Run(gctx, sched.dispatch) })
	g.Go(func() error {
		every(gctx, sched.purge, func(ctx context.Context) {
			n, err := purgeIdempotency(ctx, database, clk)
			switch {
			case err == nil:
				logger.Info("idempotency keys purged", "deleted", n)
			case ctx.Err() == nil:
				logger.Error("idempotency purge failed", "deleted", n, "error", err.Error())
			default:
				return
			}
			n, err = purgeScannerSessions(ctx, database, domains.Scanner)
			switch {
			case err == nil:
				logger.Info("scanner sessions purged", "deleted", n)
			case ctx.Err() == nil:
				logger.Error("scanner session purge failed", "deleted", n, "error", err.Error())
			}
		})
		return nil
	})
	g.Go(func() error {
		every(gctx, sched.stock, func(ctx context.Context) {
			if err := runStockJobs(ctx, database, stock, logger); err != nil && ctx.Err() == nil {
				logger.Error("stock jobs failed", "error", err.Error())
			}
		})
		return nil
	})
	g.Go(func() error {
		every(gctx, sched.shift, func(ctx context.Context) {
			if err := closeStaleShifts(ctx, database, shifts, logger); err != nil && ctx.Err() == nil {
				logger.Error("shift close failed", "error", err.Error())
			}
		})
		return nil
	})
	g.Go(func() error {
		every(gctx, sched.sweep, func(ctx context.Context) {
			if err := sweepNotifications(ctx, database, domains.Notification, logger); err != nil && ctx.Err() == nil {
				logger.Error("notification sweep failed", "error", err.Error())
			}
		})
		return nil
	})
	g.Go(func() error {
		every(gctx, sched.export, func(ctx context.Context) {
			if err := runExports(ctx, database, domains.Reporting, logger); err != nil && ctx.Err() == nil {
				logger.Error("report exports failed", "error", err.Error())
			}
			if n, err := domains.SaaS.RunDataExports(ctx); err != nil && ctx.Err() == nil {
				logger.Error("data exports failed", "finished", n, "error", err.Error())
			} else if n > 0 {
				logger.Info("data exports finished", "finished", n)
			}
		})
		return nil
	})
	g.Go(func() error {
		every(gctx, sched.subscription, func(ctx context.Context) {
			runSubscriptionJobs(ctx, domains.SaaS, logger)
		})
		return nil
	})
	g.Go(func() error {
		every(gctx, sched.usage, func(ctx context.Context) {
			if n, err := domains.SaaS.RecordUsage(ctx); err != nil && ctx.Err() == nil {
				logger.Error("usage recording failed", "recorded", n, "error", err.Error())
			} else if err == nil {
				logger.Info("usage recorded", "tenants", n)
			}
		})
		return nil
	})
	g.Go(func() error {
		return runHTTPServer(gctx, metricsLn, m.Handler(), nil, logger.With("server", "metrics"))
	})
	err = g.Wait()
	logger.Info("worker stopped")
	return err
}

// runSubscriptionJobs applies the due subscription transitions, then moves the account closures
// on (final export, completion). Failures are logged; the next tick retries.
func runSubscriptionJobs(ctx context.Context, svc *saas.Service, logger *slog.Logger) {
	if n, err := svc.RunLifecycle(ctx); err != nil && ctx.Err() == nil {
		logger.Error("subscription lifecycle failed", "moved", n, "error", err.Error())
	} else if n > 0 {
		logger.Info("subscription lifecycle ran", "moved", n)
	}
	if n, err := svc.CompleteClosures(ctx); err != nil && ctx.Err() == nil {
		logger.Error("account closures failed", "closed", n, "error", err.Error())
	} else if n > 0 {
		logger.Info("account closures completed", "closed", n)
	}
}

// every runs job immediately and then every interval until ctx is cancelled.
func every(ctx context.Context, interval time.Duration, job func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		job(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// purgeIdempotency deletes expired idempotency keys of every active tenant (a replaced sandbox is
// left alone), one transaction each so RLS scopes every delete, and returns how many were deleted. A tenant that fails is skipped; the
// others are still purged and every failure is returned joined.
func purgeIdempotency(ctx context.Context, database *db.DB, clk clock.Clock) (int64, error) {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	guard := idempotency.NewGuard(clk)
	var total int64
	err = eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		var n int64
		err := database.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
			var err error
			n, err = guard.Purge(ctx, q)
			return err
		})
		if err == nil {
			total += n // a failed transaction deleted nothing
		}
		return err
	})
	return total, err
}

// purgeScannerSessions deletes the scanner sessions of every active tenant that were revoked, or
// whose desktop went away, more than a day ago, with their scans; one transaction per tenant. A
// tenant that fails is skipped; failures are returned joined.
func purgeScannerSessions(ctx context.Context, database *db.DB, svc *scanner.Service) (int64, error) {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	err = eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		n, err := svc.Purge(ctx, tenantID)
		if err == nil {
			total += n
		}
		return err
	})
	return total, err
}

// runStockJobs rolls the expired lots, releases the expired manual holds and detects the stock
// signals of every active tenant (no job writes to a replaced sandbox), one transaction per tenant
// and job. A tenant that fails is skipped; failures are returned joined.
func runStockJobs(ctx context.Context, database *db.DB, stock *inventory.Service, logger *slog.Logger) error {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return err
	}
	return eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		rolled, rollErr := stock.RollExpiry(ctx, tenantID)
		released, releaseErr := stock.ReleaseExpiredReservations(ctx, tenantID)
		signals, signalErr := stock.DetectSignals(ctx, tenantID)
		if rolled > 0 || released > 0 || signals > 0 {
			logger.Info("stock jobs ran", "tenant_id", tenantID.String(), "lots_expired", rolled,
				"reservations_released", released, "signals", signals)
		}
		return errors.Join(rollErr, releaseErr, signalErr)
	})
}

// closeStaleShifts closes the POS shift each active tenant left open from an earlier business
// date, one transaction per tenant. A tenant that fails is skipped; failures are returned joined.
func closeStaleShifts(ctx context.Context, database *db.DB, shifts *sales.Service, logger *slog.Logger) error {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return err
	}
	return eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		closed, err := shifts.CloseStaleShifts(ctx, tenantID)
		if closed && err == nil {
			logger.Info("stale shift closed", "tenant_id", tenantID.String())
		}
		return err
	})
}

// sweepNotifications re-checks the notifications of every active tenant, one transaction per
// tenant. A tenant that fails is skipped; failures are returned joined.
func sweepNotifications(ctx context.Context, database *db.DB, notices *notification.Service, logger *slog.Logger) error {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return err
	}
	return eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		n, err := notices.Sweep(ctx, tenantID)
		if n > 0 && err == nil {
			logger.Debug("notifications swept", "tenant_id", tenantID.String(), "open", n)
		}
		return err
	})
}

// runExports runs the queued report exports of every active tenant. A tenant that fails is
// skipped; failures are returned joined.
func runExports(ctx context.Context, database *db.DB, reports *reporting.Service, logger *slog.Logger) error {
	tenants, err := database.ActiveTenantIDs(ctx)
	if err != nil {
		return err
	}
	return eachTenant(ctx, tenants, func(tenantID uuid.UUID) error {
		_, err := reports.RunExports(ctx, tenantID, logger)
		return err
	})
}

// eachTenant runs job for every tenant in order and keeps going when one fails; it stops early
// only when ctx is cancelled. Failures are returned joined, each naming its tenant.
func eachTenant(ctx context.Context, tenants []uuid.UUID, job func(tenantID uuid.UUID) error) error {
	var errs []error
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := job(tenantID); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", tenantID, err))
		}
	}
	return errors.Join(errs...)
}
