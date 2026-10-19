package cli

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/fault"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/urls"
	"github.com/g0-up/tally/apps/api/internal/saas/provider"
)

// billingProviders are the payment providers the configuration enables; the manual transfer
// carries the configured bank account.
func billingProviders(b config.Billing) ([]provider.Provider, error) {
	out := []provider.Provider{provider.NewManualTransfer(b.BankAccount)}
	if b.WebhookProvider != "" {
		hook, err := provider.NewSignedWebhook(b.WebhookProvider, []byte(b.WebhookSecret))
		if err != nil {
			return nil, err
		}
		out = append(out, hook)
	}
	return out, nil
}

// accountStack is the identity service and what it shares with the organisation service; the
// server and the tenant commands build it the same way.
type accountStack struct {
	clock       clock.Clock
	audit       *audit.Recorder
	provisioner *org.Provisioner
	identity    *identity.Service
	domains     app.Domains
}

// accountConfig is the part of the configuration the identity service needs.
type accountConfig struct {
	HTTP     config.HTTP
	Secrets  config.Secrets
	Mail     config.Mail
	Location *time.Location
	// Objects stores report exports; nil leaves exports queued (commands that never serve them).
	Objects objectstore.Store
	// Model answers the AI Q&A; nil leaves it disabled.
	Model analytics.ModelClient
	// Providers take service invoice payments besides the default manual transfer.
	Providers []provider.Provider
	// Faults are the failure points armed by acceptance tests; nil outside APP_ENV=test.
	Faults *fault.Injector
}

// newAccountStack assembles the identity service on database. tenantCreated is told the slug of
// every new tenant (the server purges its tenant host cache); it may be nil.
func newAccountStack(database *db.DB, cfg accountConfig, logger *slog.Logger, tenantCreated func(slug string)) (accountStack, error) {
	smtp, err := newSMTP(cfg.Mail)
	if err != nil {
		return accountStack{}, err
	}
	clk := clock.System{}
	rec := audit.NewRecorder(clk)
	// Default data seeders join the provisioner as their domains are built. Postings, plan
	// modules and plan limits follow each store's subscription.
	opts := []app.DomainOption{app.WithMailer(smtp), app.WithSubscriptionGate(), app.WithPaymentProviders(cfg.Providers...)}
	if cfg.Objects != nil {
		opts = append(opts, app.WithObjectStore(cfg.Objects))
	}
	if cfg.Model != nil {
		opts = append(opts, app.WithModel(cfg.Model))
	}
	if cfg.Faults != nil {
		opts = append(opts, app.WithFaults(cfg.Faults))
	}
	domains := app.NewDomains(database, clk, rec, opts...)
	svc, prov, err := newIdentity(database, cfg, domains, clk, rec, smtp, logger, tenantCreated)
	if err != nil {
		return accountStack{}, err
	}
	return accountStack{clock: clk, audit: rec, provisioner: prov, identity: svc, domains: domains}, nil
}

func newSMTP(cfg config.Mail) (*mailer.SMTP, error) {
	return mailer.NewSMTP(mailer.SMTPConfig{
		Host: cfg.Host, Port: cfg.Port, User: cfg.User, Password: cfg.Password, From: cfg.From,
	})
}

// newIdentity builds the identity service over domains and hands it the closure links of their
// subscription service.
func newIdentity(database *db.DB, cfg accountConfig, domains app.Domains, clk clock.Clock, rec *audit.Recorder,
	smtp mailer.Mailer, logger *slog.Logger, tenantCreated func(slug string)) (*identity.Service, *org.Provisioner, error) {
	warnDevelopmentSecrets(cfg.Secrets, logger)
	keys, err := identity.NewKeys(cfg.Secrets.SessionHashKey, cfg.Secrets.EncryptionKey)
	if err != nil {
		return nil, nil, err
	}
	hasher, err := authn.NewHasher(authn.DefaultParams)
	if err != nil {
		return nil, nil, err
	}
	builder, err := urls.New(cfg.HTTP.PublicBaseURL)
	if err != nil {
		return nil, nil, err
	}
	prov := org.NewProvisioner(org.NewSlugPolicy(cfg.HTTP.ReservedSlugs), rec, clk, domains.Seeders())
	svc, err := identity.NewService(identity.Deps{
		DB: database, Clock: clk, Audit: rec, Hasher: hasher, Keys: keys, Provisioner: app.NewTenantProvisioner(prov), URLs: builder,
		Mailer: smtp, Renderer: mailer.NewRenderer(mailer.Templates), Location: cfg.Location, Logger: logger,
		TenantCreated: tenantCreated,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("identity service: %w", err)
	}
	domains.SaaS.SetClosureTokens(svc)
	return svc, prov, nil
}

// warnDevelopmentSecrets logs the secrets that fell back to their public development values
// (config refuses that in production).
func warnDevelopmentSecrets(s config.Secrets, logger *slog.Logger) {
	if len(s.Defaulted) > 0 {
		logger.Warn("using public development values for unset secrets; set them outside local development",
			"variables", strings.Join(s.Defaulted, ","))
	}
}
