// Package config reads the process configuration from environment variables. Loading fails fast
// with every problem listed at once, and error messages name variables but never print values,
// because most of them are credentials.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/fault"
)

// Env is the deployment environment (APP_ENV).
type Env string

// Environments accepted in APP_ENV.
const (
	Development Env = "development"
	Test        Env = "test"
	Production  Env = "production"
)

// Default listen addresses of the internal Prometheus listeners (never routed by the edge).
const (
	DefaultServeMetricsAddr  = "127.0.0.1:9090"
	DefaultWorkerMetricsAddr = "127.0.0.1:9091"
)

// MaxUploadCeiling mirrors the attachments size CHECK (20 MiB).
const MaxUploadCeiling int64 = 20 << 20

// minSecretLen is the minimum length of production secrets (openssl rand -base64 32 gives 44).
const minSecretLen = 32

// devSecretPrefix starts the development fallback of an unset secret; the value is public, so it
// is never accepted in production.
const devSecretPrefix = "tally-insecure-development-only-"

// providerName is a payment provider key as it appears in the callback URL.
var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// Lookup reads one variable; os.LookupEnv in production, a map in tests.
type Lookup func(key string) (string, bool)

// Base is shared by every command.
type Base struct {
	Env      Env
	TZName   string
	Location *time.Location
	LogLevel string
}

// IsProduction reports APP_ENV=production.
func (b Base) IsProduction() bool { return b.Env == Production }

// HTTP configures the public HTTP surface.
type HTTP struct {
	// PublicBaseURL is the apex URL; tenant and admin hosts derive from its host.
	PublicBaseURL *url.URL
	// TrustedProxyCIDRs lists the proxies (Traefik) whose CF-Connecting-IP header is trusted.
	TrustedProxyCIDRs []string
	// ReservedSlugs are hostname labels other services use on the same zone.
	ReservedSlugs []string
}

// Database configures the application pool (role tally_app).
type Database struct {
	URL              string
	MaxConns         int
	StatementTimeout time.Duration
}

// Secrets holds keys used by sessions, CSRF tokens and encrypted columns. Required in production;
// outside production an unset key falls back to a fixed, publicly known development value so a
// fresh checkout runs, and Defaulted names the variables that did (the caller logs a warning).
type Secrets struct {
	SessionHashKey string
	CSRFKey        string
	EncryptionKey  string
	Defaulted      []string
}

// Mail configures the SMTP relay.
type Mail struct {
	Host     string
	Port     int
	User     string
	Password string
	From     *mail.Address
}

// ObjectStore configures the S3-compatible bucket.
type ObjectStore struct {
	Endpoint       *url.URL
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	MaxUploadBytes int64
}

// AI configures the optional language model behind the read-only analytics Q&A: an
// OpenAI-compatible chat completions endpoint. Without AI_BASE_URL and AI_MODEL the Q&A is off.
type AI struct {
	BaseURL *url.URL
	APIKey  string
	Model   string
}

// Enabled reports whether a model is configured.
func (a AI) Enabled() bool { return a.BaseURL != nil && a.Model != "" }

// Billing configures how stores pay their service invoices: the bank account shown with manual
// transfers and an optional signed-webhook payment provider (SAAS_WEBHOOK_PROVIDER names it in
// the callback URL, SAAS_WEBHOOK_SECRET signs its callbacks).
type Billing struct {
	BankAccount     string
	WebhookProvider string
	WebhookSecret   string
}

// Serve is the configuration of `tally serve`.
type Serve struct {
	Base
	HTTP        HTTP
	MetricsAddr string
	Database    Database
	Secrets     Secrets
	Mail        Mail
	ObjectStore ObjectStore
	AI          AI
	Billing     Billing
	// MigrateOnStart runs pending migrations before serving (development convenience only).
	MigrateOnStart bool
	MigrateURL     string
	// Faults are the failure points TALLY_FAULT_INJECT arms for acceptance tests; always nil
	// unless APP_ENV=test. FaultsIgnored reports a value that was set but ignored for that reason.
	Faults        *fault.Injector
	FaultsIgnored bool
}

// Worker is the configuration of `tally worker`.
type Worker struct {
	Base
	HTTP        HTTP
	MetricsAddr string
	Database    Database
	Mail        Mail
	ObjectStore ObjectStore
	// Secrets sign the links the worker mails (a closed store's final export).
	Secrets Secrets
}

// Migrate is the configuration of `tally migrate` (role tally_owner).
type Migrate struct {
	Base
	URL string
}

// CLI is the configuration of operator commands that only need the application database.
type CLI struct {
	Base
	Database Database
}

// Provision is the configuration of operator commands that create tenants or act on accounts
// (`tally tenant create`, `tally seed demo`, `tally maintenance revoke-sessions`): they build
// links, hash tokens and send the verification mail like the server.
type Provision struct {
	Base
	HTTP     HTTP
	Database Database
	Secrets  Secrets
	Mail     Mail
}

// LoadServe reads the serve configuration.
func LoadServe(lookup Lookup) (Serve, error) {
	r := newReader(lookup)
	c := Serve{
		Base:           r.base(),
		HTTP:           r.http(),
		MetricsAddr:    r.addr("METRICS_ADDR", DefaultServeMetricsAddr),
		Database:       r.database(),
		Mail:           r.mail(),
		ObjectStore:    r.objectStore(),
		MigrateOnStart: r.boolean("MIGRATE_ON_START", false),
	}
	c.Secrets = r.secrets(c.IsProduction())
	c.AI = r.ai(c.IsProduction())
	c.Billing = r.billing()
	if c.IsProduction() && len(c.HTTP.TrustedProxyCIDRs) == 0 {
		// Behind Traefik every TCP peer is the proxy: without a trusted range every client would
		// share the proxy's address, one rate-limit bucket and one audit IP.
		r.problem("TRUSTED_PROXY_CIDRS is required in production (the reverse proxy address or network)")
	}
	if c.MigrateOnStart {
		if c.IsProduction() {
			r.problem("MIGRATE_ON_START must be false in production (migrations run after a backup through `tally migrate up --backup-file`)")
		}
		c.MigrateURL = r.required("MIGRATE_DATABASE_URL")
	}
	c.Faults, c.FaultsIgnored = r.faults(c.Env)
	r.checkPublicURL(c.Base, c.HTTP)
	return c, r.err()
}

// faults reads TALLY_FAULT_INJECT, which only APP_ENV=test honours.
func (r *reader) faults(env Env) (*fault.Injector, bool) {
	spec := strings.TrimSpace(r.str(fault.EnvVar, ""))
	inj, err := fault.Parse(string(env), spec)
	if err != nil {
		r.problem("%v", err)
	}
	return inj, spec != "" && env != Test
}

// LoadWorker reads the worker configuration.
func LoadWorker(lookup Lookup) (Worker, error) {
	r := newReader(lookup)
	c := Worker{
		Base:        r.base(),
		HTTP:        r.http(),
		MetricsAddr: r.addr("METRICS_ADDR", DefaultWorkerMetricsAddr),
		Database:    r.database(),
		Mail:        r.mail(),
		ObjectStore: r.objectStore(),
	}
	c.Secrets = r.secrets(c.IsProduction())
	r.checkPublicURL(c.Base, c.HTTP)
	return c, r.err()
}

// LoadMigrate reads the migrate configuration.
func LoadMigrate(lookup Lookup) (Migrate, error) {
	r := newReader(lookup)
	c := Migrate{Base: r.base(), URL: r.required("MIGRATE_DATABASE_URL")}
	return c, r.err()
}

// LoadCLI reads the configuration of database-only operator commands.
func LoadCLI(lookup Lookup) (CLI, error) {
	r := newReader(lookup)
	c := CLI{Base: r.base(), Database: r.database()}
	return c, r.err()
}

// LoadProvision reads the configuration of tenant and account operator commands.
func LoadProvision(lookup Lookup) (Provision, error) {
	r := newReader(lookup)
	c := Provision{Base: r.base(), HTTP: r.http(), Database: r.database(), Mail: r.mail()}
	c.Secrets = r.secrets(c.IsProduction())
	r.checkPublicURL(c.Base, c.HTTP)
	return c, r.err()
}

// LoadBase reads only APP_ENV, APP_TZ and LOG_LEVEL.
func LoadBase(lookup Lookup) (Base, error) {
	r := newReader(lookup)
	b := r.base()
	return b, r.err()
}

// OSLookup reads the process environment.
func OSLookup(key string) (string, bool) { return os.LookupEnv(key) }

type reader struct {
	lookup   Lookup
	problems []string
}

func newReader(lookup Lookup) *reader {
	if lookup == nil {
		lookup = OSLookup
	}
	return &reader{lookup: lookup}
}

func (r *reader) problem(format string, args ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, args...))
}

func (r *reader) err() error {
	if len(r.problems) == 0 {
		return nil
	}
	return errors.New("invalid configuration: " + strings.Join(r.problems, "; "))
}

func (r *reader) str(key, def string) string {
	if v, ok := r.lookup(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *reader) required(key string) string {
	v := r.str(key, "")
	if v == "" {
		r.problem("%s is required", key)
	}
	return v
}

func (r *reader) integer(key string, def, minV, maxV int) int {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minV || n > maxV {
		r.problem("%s must be an integer between %d and %d", key, minV, maxV)
		return def
	}
	return n
}

func (r *reader) duration(key string, def, minV, maxV time.Duration) time.Duration {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < minV || d > maxV {
		r.problem("%s must be a duration between %s and %s", key, minV, maxV)
		return def
	}
	return d
}

func (r *reader) boolean(key string, def bool) bool {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		r.problem("%s must be true or false", key)
		return def
	}
	return b
}

func (r *reader) list(key string) []string {
	raw := r.str(key, "")
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r *reader) addr(key, def string) string {
	v := r.str(key, def)
	if _, port, err := net.SplitHostPort(v); err != nil || port == "" {
		r.problem("%s must be host:port", key)
	}
	return v
}

func (r *reader) base() Base {
	b := Base{
		Env:      Env(r.str("APP_ENV", string(Development))),
		TZName:   r.str("APP_TZ", "Asia/Ho_Chi_Minh"),
		LogLevel: r.str("LOG_LEVEL", "info"),
	}
	switch b.Env {
	case Development, Test, Production:
	default:
		r.problem("APP_ENV must be development, test or production")
	}
	loc, err := time.LoadLocation(b.TZName)
	if err != nil {
		r.problem("APP_TZ must be an IANA time zone")
		loc = time.UTC
	}
	b.Location = loc
	switch strings.ToLower(b.LogLevel) {
	case "debug", "info", "warn", "warning", "error":
	default:
		r.problem("LOG_LEVEL must be debug, info, warn or error")
	}
	return b
}

func (r *reader) http() HTTP {
	h := HTTP{ReservedSlugs: r.list("RESERVED_SLUGS")}
	if raw := r.required("PUBLIC_BASE_URL"); raw != "" {
		u, err := url.Parse(raw)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
			r.problem("PUBLIC_BASE_URL must be an absolute http(s) URL")
		case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
			r.problem("PUBLIC_BASE_URL must not contain a path, query, fragment or user info")
		default:
			u.Path = ""
			h.PublicBaseURL = u
		}
	}
	for _, c := range r.list("TRUSTED_PROXY_CIDRS") {
		if _, err := netip.ParsePrefix(c); err != nil {
			if _, err := netip.ParseAddr(c); err != nil {
				r.problem("TRUSTED_PROXY_CIDRS entry %d is not a CIDR or IP address", len(h.TrustedProxyCIDRs)+1)
				continue
			}
		}
		h.TrustedProxyCIDRs = append(h.TrustedProxyCIDRs, c)
	}
	return h
}

func (r *reader) checkPublicURL(b Base, h HTTP) {
	if h.PublicBaseURL == nil {
		return
	}
	if b.IsProduction() && h.PublicBaseURL.Scheme != "https" {
		r.problem("PUBLIC_BASE_URL must use https in production")
	}
	if domain := r.str("DOMAIN", ""); domain != "" && !strings.EqualFold(domain, h.PublicBaseURL.Hostname()) {
		r.problem("DOMAIN must equal the host of PUBLIC_BASE_URL")
	}
}

func (r *reader) database() Database {
	return Database{
		URL:              r.required("DATABASE_URL"),
		MaxConns:         r.integer("DB_MAX_CONNS", 20, 1, 500),
		StatementTimeout: r.duration("DB_STATEMENT_TIMEOUT", 15*time.Second, time.Second, 10*time.Minute),
	}
}

func (r *reader) secrets(production bool) Secrets {
	s := Secrets{
		SessionHashKey: r.str("SESSION_HASH_KEY", ""),
		CSRFKey:        r.str("CSRF_KEY", ""),
		EncryptionKey:  r.str("APP_ENCRYPTION_KEY", ""),
	}
	for _, f := range []struct {
		key string
		v   *string
	}{{"SESSION_HASH_KEY", &s.SessionHashKey}, {"CSRF_KEY", &s.CSRFKey}, {"APP_ENCRYPTION_KEY", &s.EncryptionKey}} {
		switch {
		case production && len(*f.v) < minSecretLen:
			r.problem("%s must be set to at least %d characters in production", f.key, minSecretLen)
		case !production && *f.v == "":
			*f.v = devSecretPrefix + strings.ToLower(f.key)
			s.Defaulted = append(s.Defaulted, f.key)
		}
	}
	return s
}

func (r *reader) mail() Mail {
	m := Mail{
		Host:     r.required("SMTP_HOST"),
		Port:     r.integer("SMTP_PORT", 587, 1, 65535),
		User:     r.str("SMTP_USER", ""),
		Password: r.str("SMTP_PASSWORD", ""),
	}
	if (m.User == "") != (m.Password == "") {
		r.problem("SMTP_USER and SMTP_PASSWORD must be set together")
	}
	if raw := r.required("MAIL_FROM"); raw != "" {
		addr, err := mail.ParseAddress(raw)
		if err != nil {
			r.problem("MAIL_FROM must be an address such as \"Tally <no-reply@example.com>\"")
		} else {
			m.From = addr
		}
	}
	return m
}

func (r *reader) ai(production bool) AI {
	a := AI{APIKey: r.str("AI_API_KEY", ""), Model: r.str("AI_MODEL", "")}
	raw := r.str("AI_BASE_URL", "")
	if raw == "" && a.Model == "" {
		if a.APIKey != "" {
			r.problem("AI_API_KEY is set without AI_BASE_URL and AI_MODEL")
		}
		return a
	}
	if raw == "" || a.Model == "" {
		r.problem("AI_BASE_URL and AI_MODEL must be set together")
		return AI{}
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		r.problem("AI_BASE_URL must be an http(s) URL")
	case production && u.Scheme != "https":
		r.problem("AI_BASE_URL must use https in production")
	default:
		a.BaseURL = u
	}
	return a
}

func (r *reader) billing() Billing {
	b := Billing{
		BankAccount: r.str("SAAS_BANK_ACCOUNT", ""), WebhookProvider: r.str("SAAS_WEBHOOK_PROVIDER", ""),
		WebhookSecret: r.str("SAAS_WEBHOOK_SECRET", ""),
	}
	switch {
	case (b.WebhookProvider == "") != (b.WebhookSecret == ""):
		r.problem("SAAS_WEBHOOK_PROVIDER and SAAS_WEBHOOK_SECRET must be set together")
	case b.WebhookProvider != "" && (!providerName.MatchString(b.WebhookProvider) || b.WebhookProvider == "manual"):
		r.problem("SAAS_WEBHOOK_PROVIDER must be lowercase letters, digits or dashes and not \"manual\"")
	case b.WebhookSecret != "" && len(b.WebhookSecret) < minSecretLen:
		r.problem("SAAS_WEBHOOK_SECRET must be at least %d characters", minSecretLen)
	}
	return b
}

func (r *reader) objectStore() ObjectStore {
	o := ObjectStore{
		Region:         r.str("OBJECT_STORE_REGION", "us-east-1"),
		Bucket:         r.required("OBJECT_STORE_BUCKET"),
		AccessKey:      r.required("OBJECT_STORE_ACCESS_KEY"),
		SecretKey:      r.required("OBJECT_STORE_SECRET_KEY"),
		MaxUploadBytes: int64(r.integer("OBJECT_STORE_MAX_UPLOAD_BYTES", int(MaxUploadCeiling), 1, int(MaxUploadCeiling))),
	}
	if raw := r.required("OBJECT_STORE_ENDPOINT"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			r.problem("OBJECT_STORE_ENDPOINT must be an http(s) URL without a path")
		} else {
			o.Endpoint = u
		}
	}
	return o
}
