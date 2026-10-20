package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/fault"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/support"
)

const (
	defaultHTTPAddr   = ":8080"
	shutdownTimeout   = 15 * time.Second
	readHeaderTimeout = 5 * time.Second
)

func newServeCommand(version string, lookup config.Lookup) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API (and its internal metrics listener)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadServe(lookup)
			if err != nil {
				return err
			}
			logger, err := logging.New(cmd.OutOrStdout(), cfg.LogLevel)
			if err != nil {
				return err
			}
			listeners, err := listen(addr, cfg.MetricsAddr)
			if err != nil {
				return err
			}
			return runServe(cmd.Context(), cfg, logger.With("version", version), listeners[0], listeners[1])
		},
	}
	cmd.Flags().StringVar(&addr, "addr", defaultHTTPAddr, "TCP address the HTTP API listens on")
	return cmd
}

// runServe connects the database, assembles the application and serves the API and metrics
// listeners until ctx is cancelled. It closes both listeners.
func runServe(ctx context.Context, cfg config.Serve, logger *slog.Logger, apiLn, metricsLn net.Listener) error {
	defer closeListeners(apiLn, metricsLn)
	if cfg.MigrateOnStart {
		if err := migrateUp(cfg.MigrateURL, logger); err != nil {
			return err
		}
	}
	database, err := db.Open(ctx, cfg.Database.URL, db.Options{
		MaxConns:         cfg.Database.MaxConns,
		StatementTimeout: cfg.Database.StatementTimeout,
		ApplicationName:  "tally-api",
	})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	// A new store's host may have been cached as unknown, and a reset sandbox's host as its
	// replaced tenant; the cache exists once the app does, which is before the first request.
	var hosts *middleware.TenantHostCache
	purgeHost := func(slug string) {
		if hosts != nil {
			hosts.Purge(slug)
		}
	}
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: cfg.ObjectStore.Endpoint, Region: cfg.ObjectStore.Region, Bucket: cfg.ObjectStore.Bucket,
		AccessKey: cfg.ObjectStore.AccessKey, SecretKey: cfg.ObjectStore.SecretKey,
	})
	if err != nil {
		return err
	}
	model := analytics.NewHTTPModel(analytics.HTTPModelConfig{BaseURL: cfg.AI.BaseURL, APIKey: cfg.AI.APIKey, Model: cfg.AI.Model})
	if model == nil {
		logger.Info("AI Q&A disabled: AI_BASE_URL and AI_MODEL are not set")
	}
	providers, err := billingProviders(cfg.Billing)
	if err != nil {
		return err
	}
	if cfg.FaultsIgnored {
		logger.Warn(fault.EnvVar + " is ignored: failure injection is honoured only with APP_ENV=test")
	}
	if cfg.Faults != nil {
		logger.Warn("failure injection is armed", "variable", fault.EnvVar)
	}
	accounts, err := newAccountStack(database, accountConfig{
		HTTP: cfg.HTTP, Secrets: cfg.Secrets, Mail: cfg.Mail, Location: cfg.Location, Objects: objects, Model: model,
		Providers: providers, Faults: cfg.Faults,
	}, logger, purgeHost)
	if err != nil {
		return err
	}
	control, err := newControlStack(database, accounts, cfg.Secrets, logger)
	if err != nil {
		return err
	}
	defer accounts.identity.WaitMail()
	orgSvc := org.NewService(org.Deps{
		DB: database, Clock: accounts.clock, Audit: accounts.audit, Exceptions: exception.NewGuard(accounts.clock),
		Provisioner: accounts.provisioner, Objects: objects, AttachmentOwners: accounts.domains.AttachmentOwners(),
		AddressMoved: purgeHost, Limits: accounts.domains.OrgLimits(),
	})
	entitlements := accounts.domains.SaaS.Reader()

	m := metrics.New()
	a, err := app.New(app.Options{
		PublicBaseURL:     cfg.HTTP.PublicBaseURL,
		TrustedProxyCIDRs: cfg.HTTP.TrustedProxyCIDRs,
		// __Host- cookies need the Secure attribute, which browsers only keep on https.
		SecureCookies: cfg.HTTP.PublicBaseURL.Scheme == "https",
		Logger:        logger,
		Metrics:       m,
		Ready:         database.Ping,
		Ports: app.Ports{
			Sessions: accounts.identity, TenantHosts: accounts.identity, Entitlements: entitlements, Modules: entitlements,
			AdminSessions: control.auth, Maintenance: entitlements, LoginSessions: accounts.identity,
		},
		CSRFKey: []byte(cfg.Secrets.CSRFKey),
		Modules: app.Modules{
			Identity: identity.NewHandler(accounts.identity),
			Org:      org.NewHandler(orgSvc),
			Reasons:  org.NewReasonHandler(orgSvc),
			Catalog:  catalog.NewHandler(accounts.domains.Catalog),
			Partner:  partner.NewHandler(accounts.domains.Partner),
			Pricing:  pricing.NewHandler(accounts.domains.Pricing),
			Debt:     debt.NewHandler(accounts.domains.Debt),
			Cash:     cash.NewHandler(accounts.domains.Cash),

			Inventory:   inventory.NewHandler(accounts.domains.Inventory),
			Procurement: procurement.NewHandler(accounts.domains.Procurement),
			Sales:       sales.NewHandler(accounts.domains.Sales),
			Kits:        kits.NewHandler(accounts.domains.Kits),
			Campaign:    campaign.NewHandler(accounts.domains.Campaign),
			CRM:         crm.NewHandler(accounts.domains.CRM),

			Notification: notification.NewHandler(accounts.domains.Notification),
			Reporting:    reporting.NewHandler(accounts.domains.Reporting),
			Analytics:    analytics.NewHandler(accounts.domains.Analytics),
			Printing:     printing.NewHandler(accounts.domains.Printing),
			Scanner:      scanner.NewHandler(accounts.domains.Scanner),

			SaaS:         saas.NewHandler(accounts.domains.SaaS),
			Support:      support.NewHandler(control.support),
			SupportAdmin: control.support,
			Onboarding:   org.NewOnboardingHandler(orgSvc),
			Feedback:     admin.NewFeedbackHandler(control.feedback),
			Status:       admin.NewStatusHandler(control.incidents),
			Admin:        control.admin,
		},
	})
	if err != nil {
		return err
	}
	hosts = a.TenantHostCache()
	logger.Info("api listening", "addr", apiLn.Addr().String(), "metrics_addr", metricsLn.Addr().String())
	return runServers(ctx, logger,
		// Shutting down sends restarting to every open scanner stream and closes it, so the drain
		// does not wait for streams that never end on their own.
		namedServer{name: "api", ln: apiLn, handler: a.Handler(), onShutdown: accounts.domains.Scanner.Hub().CloseAll},
		namedServer{name: "metrics", ln: metricsLn, handler: m.Handler()},
	)
}

// migrateUp applies pending migrations at start-up (development only; config refuses it in
// production). A build without migrations only logs a warning.
func migrateUp(url string, logger *slog.Logger) error {
	mig, err := db.NewMigrator(url, migrationsFS)
	if errors.Is(err, db.ErrNoMigrations) {
		logger.Warn("MIGRATE_ON_START is set but this build has no migrations")
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = mig.Close() }()
	if err := mig.Up(); err != nil {
		return err
	}
	version, _, err := mig.Version()
	if err != nil {
		return err
	}
	logger.Info("migrations applied", "version", version)
	return nil
}

// listen opens one TCP listener per address, closing the opened ones on failure.
func listen(addrs ...string) ([]net.Listener, error) {
	out := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			closeListeners(out...)
			return nil, fmt.Errorf("listen on %s: %w", addr, err)
		}
		out = append(out, ln)
	}
	return out, nil
}

func closeListeners(lns ...net.Listener) {
	for _, ln := range lns {
		_ = ln.Close()
	}
}

type namedServer struct {
	name    string
	ln      net.Listener
	handler http.Handler
	// onShutdown, when set, runs as the graceful shutdown starts.
	onShutdown func()
}

// runServers serves every listener until ctx is cancelled or one server fails, then shuts all
// of them down gracefully.
func runServers(ctx context.Context, logger *slog.Logger, servers ...namedServer) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range servers {
		g.Go(func() error { return runHTTPServer(gctx, s.ln, s.handler, s.onShutdown, logger.With("server", s.name)) })
	}
	return g.Wait()
}

// runHTTPServer serves until ctx is cancelled, then drains in-flight requests. onShutdown, when
// not nil, runs as the drain starts (http.Server.RegisterOnShutdown).
func runHTTPServer(ctx context.Context, ln net.Listener, handler http.Handler, onShutdown func(), logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	if onShutdown != nil {
		srv.RegisterOnShutdown(onShutdown)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("http server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("http server stopped")
	return nil
}
