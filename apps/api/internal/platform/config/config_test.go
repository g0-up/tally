package config

import (
	"strings"
	"testing"
	"time"
)

func devEnv() map[string]string {
	return map[string]string{
		"APP_ENV":                 "development",
		"PUBLIC_BASE_URL":         "http://localhost:5173",
		"DATABASE_URL":            "postgres://tally_app:pw@localhost:5442/tally",
		"SMTP_HOST":               "localhost",
		"SMTP_PORT":               "1025",
		"MAIL_FROM":               "Tally <no-reply@tally.localhost>",
		"OBJECT_STORE_ENDPOINT":   "http://localhost:9000",
		"OBJECT_STORE_BUCKET":     "tally-dev",
		"OBJECT_STORE_ACCESS_KEY": "tally-dev",
		"OBJECT_STORE_SECRET_KEY": "very-secret-value",
	}
}

func lookupFrom(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadServeDevelopmentDefaults(t *testing.T) {
	c, err := LoadServe(lookupFrom(devEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != Development || c.IsProduction() || c.Location.String() != "Asia/Ho_Chi_Minh" || c.LogLevel != "info" {
		t.Errorf("base = %+v", c.Base)
	}
	if c.HTTP.PublicBaseURL.String() != "http://localhost:5173" || c.MetricsAddr != DefaultServeMetricsAddr {
		t.Errorf("http = %+v metrics=%s", c.HTTP, c.MetricsAddr)
	}
	if c.Database.MaxConns != 20 || c.Database.StatementTimeout != 15*time.Second {
		t.Errorf("database = %+v", c.Database)
	}
	if c.Mail.Port != 1025 || c.Mail.From.Address != "no-reply@tally.localhost" {
		t.Errorf("mail = %+v", c.Mail)
	}
	if c.ObjectStore.MaxUploadBytes != MaxUploadCeiling || c.ObjectStore.Region != "us-east-1" {
		t.Errorf("object store = %+v", c.ObjectStore)
	}
	if c.MigrateOnStart {
		t.Error("MigrateOnStart default should be false")
	}
	if want := []string{"SESSION_HASH_KEY", "CSRF_KEY", "APP_ENCRYPTION_KEY"}; strings.Join(c.Secrets.Defaulted, ",") != strings.Join(want, ",") {
		t.Errorf("defaulted secrets = %v, want %v", c.Secrets.Defaulted, want)
	}
	if c.Secrets.SessionHashKey == "" || c.Secrets.CSRFKey == "" || c.Secrets.EncryptionKey == "" ||
		c.Secrets.SessionHashKey == c.Secrets.CSRFKey || c.Secrets.CSRFKey == c.Secrets.EncryptionKey {
		t.Errorf("development secrets must be set and distinct")
	}
}

func TestLoadServeKeepsSetSecretsOutsideProduction(t *testing.T) {
	env := devEnv()
	env["CSRF_KEY"] = "my-own-csrf-key"
	c, err := LoadServe(lookupFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.Secrets.CSRFKey != "my-own-csrf-key" || strings.Join(c.Secrets.Defaulted, ",") != "SESSION_HASH_KEY,APP_ENCRYPTION_KEY" {
		t.Errorf("secrets = %+v", c.Secrets.Defaulted)
	}
}

func TestLoadProvision(t *testing.T) {
	env := devEnv()
	env["RESERVED_SLUGS"] = "grafana, status"
	c, err := LoadProvision(lookupFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.PublicBaseURL == nil || c.Database.URL == "" || c.Mail.From == nil || c.Secrets.SessionHashKey == "" ||
		strings.Join(c.HTTP.ReservedSlugs, ",") != "grafana,status" {
		t.Errorf("provision = %+v", c)
	}
	env["APP_ENV"] = "production"
	env["PUBLIC_BASE_URL"] = "https://tally.example"
	_, err = LoadProvision(lookupFrom(env))
	if err == nil || !strings.Contains(err.Error(), "SESSION_HASH_KEY must be set") {
		t.Fatalf("production without secrets: %v", err)
	}
	delete(env, "SMTP_HOST")
	if _, err := LoadProvision(lookupFrom(env)); err == nil || !strings.Contains(err.Error(), "SMTP_HOST is required") {
		t.Fatalf("missing SMTP_HOST: %v", err)
	}
}

func TestLoadServeListsEveryProblemWithoutValues(t *testing.T) {
	env := map[string]string{
		"APP_ENV":                 "staging",
		"APP_TZ":                  "Mars/Base",
		"LOG_LEVEL":               "chatty",
		"PUBLIC_BASE_URL":         "ftp://x",
		"DB_MAX_CONNS":            "0",
		"DB_STATEMENT_TIMEOUT":    "forever",
		"SMTP_USER":               "only-user",
		"MAIL_FROM":               "not an address",
		"OBJECT_STORE_ENDPOINT":   "localhost:9000/x",
		"TRUSTED_PROXY_CIDRS":     "10.0.0.0/8, banana",
		"METRICS_ADDR":            "nope",
		"MIGRATE_ON_START":        "maybe",
		"OBJECT_STORE_SECRET_KEY": "s3cr3t-value-never-printed",
	}
	_, err := LoadServe(lookupFrom(env))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"APP_ENV", "APP_TZ", "LOG_LEVEL", "PUBLIC_BASE_URL", "DATABASE_URL is required",
		"DB_MAX_CONNS", "DB_STATEMENT_TIMEOUT", "SMTP_HOST is required", "SMTP_USER and SMTP_PASSWORD", "MAIL_FROM",
		"OBJECT_STORE_ENDPOINT", "OBJECT_STORE_BUCKET is required", "TRUSTED_PROXY_CIDRS entry 2", "METRICS_ADDR",
		"MIGRATE_ON_START"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q: %s", want, msg)
		}
	}
	for _, secret := range []string{"s3cr3t", "banana", "only-user", "forever"} {
		if strings.Contains(msg, secret) {
			t.Errorf("error leaks value %q: %s", secret, msg)
		}
	}
}

func TestLoadServeProductionRules(t *testing.T) {
	env := devEnv()
	env["APP_ENV"] = "production"
	env["PUBLIC_BASE_URL"] = "http://sidecup.app"
	env["DOMAIN"] = "other.app"
	env["MIGRATE_ON_START"] = "true"
	env["SESSION_HASH_KEY"] = "short"
	_, err := LoadServe(lookupFrom(env))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"https in production", "DOMAIN must equal", "MIGRATE_ON_START must be false",
		"MIGRATE_DATABASE_URL is required", "SESSION_HASH_KEY must be set", "CSRF_KEY must be set", "APP_ENCRYPTION_KEY must be set",
		"TRUSTED_PROXY_CIDRS is required in production"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %s", want, err)
		}
	}

	secret := strings.Repeat("k", 44)
	env = devEnv()
	env["APP_ENV"] = "production"
	env["PUBLIC_BASE_URL"] = "https://sidecup.app/"
	env["DOMAIN"] = "sidecup.app"
	env["SESSION_HASH_KEY"], env["CSRF_KEY"], env["APP_ENCRYPTION_KEY"] = secret, secret, secret
	env["TRUSTED_PROXY_CIDRS"] = "172.21.0.0/16,10.0.0.5"
	env["RESERVED_SLUGS"] = "tiendouong, tiendouong-api,"
	c, err := LoadServe(lookupFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.PublicBaseURL.String() != "https://sidecup.app" || len(c.HTTP.TrustedProxyCIDRs) != 2 || len(c.HTTP.ReservedSlugs) != 2 {
		t.Errorf("http = %+v", c.HTTP)
	}
}

func TestLoadServeMigrateOnStartInDevelopment(t *testing.T) {
	env := devEnv()
	env["MIGRATE_ON_START"] = "true"
	env["MIGRATE_DATABASE_URL"] = "postgres://tally_owner:pw@localhost:5442/tally"
	c, err := LoadServe(lookupFrom(env))
	if err != nil || !c.MigrateOnStart || c.MigrateURL == "" {
		t.Fatalf("config = %+v, %v", c, err)
	}
}

func TestLoadWorkerMigrateCLIAndBase(t *testing.T) {
	w, err := LoadWorker(lookupFrom(devEnv()))
	if err != nil || w.MetricsAddr != DefaultWorkerMetricsAddr {
		t.Fatalf("worker = %+v, %v", w, err)
	}
	if _, err := LoadMigrate(lookupFrom(map[string]string{})); err == nil || !strings.Contains(err.Error(), "MIGRATE_DATABASE_URL is required") {
		t.Fatalf("migrate error = %v", err)
	}
	m, err := LoadMigrate(lookupFrom(map[string]string{"APP_ENV": "production", "MIGRATE_DATABASE_URL": "postgres://x"}))
	if err != nil || !m.IsProduction() || m.URL != "postgres://x" {
		t.Fatalf("migrate = %+v, %v", m, err)
	}
	if _, err := LoadCLI(lookupFrom(map[string]string{})); err == nil {
		t.Fatal("cli without DATABASE_URL accepted")
	}
	b, err := LoadBase(lookupFrom(map[string]string{"APP_ENV": "test", "LOG_LEVEL": "debug"}))
	if err != nil || b.Env != Test || b.LogLevel != "debug" {
		t.Fatalf("base = %+v, %v", b, err)
	}
}

func TestOSLookupAndNilLookup(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	b, err := LoadBase(nil)
	if err != nil || b.Env != Test {
		t.Fatalf("LoadBase(nil) = %+v, %v", b, err)
	}
}

func TestLoadServeAI(t *testing.T) {
	c, err := LoadServe(lookupFrom(devEnv()))
	if err != nil || c.AI.Enabled() {
		t.Fatalf("AI without env: enabled = %v, err = %v", c.AI.Enabled(), err)
	}
	env := devEnv()
	env["AI_BASE_URL"], env["AI_MODEL"], env["AI_API_KEY"] = "http://localhost:11434/v1", "qwen", "k"
	c, err = LoadServe(lookupFrom(env))
	if err != nil || !c.AI.Enabled() || c.AI.BaseURL.Host != "localhost:11434" || c.AI.APIKey != "k" {
		t.Fatalf("AI = %+v, err = %v", c.AI, err)
	}
	for _, tc := range []struct {
		set  map[string]string
		want string
	}{
		{map[string]string{"AI_MODEL": "qwen"}, "AI_BASE_URL and AI_MODEL must be set together"},
		{map[string]string{"AI_API_KEY": "k"}, "AI_API_KEY is set without"},
		{map[string]string{"AI_BASE_URL": "ftp://x", "AI_MODEL": "m"}, "AI_BASE_URL must be an http(s) URL"},
		{map[string]string{"APP_ENV": "production", "AI_BASE_URL": "http://ai.local/v1", "AI_MODEL": "m"}, "AI_BASE_URL must use https"},
	} {
		env := devEnv()
		for k, v := range tc.set {
			env[k] = v
		}
		if _, err := LoadServe(lookupFrom(env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.set, err, tc.want)
		}
	}
}

func TestLoadServeBilling(t *testing.T) {
	c, err := LoadServe(lookupFrom(devEnv()))
	if err != nil || c.Billing.WebhookProvider != "" {
		t.Fatalf("billing without env = %+v, err = %v", c.Billing, err)
	}
	env := devEnv()
	env["SAAS_BANK_ACCOUNT"] = "Vietcombank 0011 0000 1234 – Công ty Tally"
	env["SAAS_WEBHOOK_PROVIDER"], env["SAAS_WEBHOOK_SECRET"] = "payos", strings.Repeat("s", 32)
	c, err = LoadServe(lookupFrom(env))
	if err != nil || c.Billing.WebhookProvider != "payos" || c.Billing.BankAccount == "" {
		t.Fatalf("billing = %+v, err = %v", c.Billing, err)
	}
	for _, tc := range []struct {
		set  map[string]string
		want string
	}{
		{map[string]string{"SAAS_WEBHOOK_PROVIDER": "payos"}, "must be set together"},
		{map[string]string{"SAAS_WEBHOOK_SECRET": strings.Repeat("s", 32)}, "must be set together"},
		{map[string]string{"SAAS_WEBHOOK_PROVIDER": "manual", "SAAS_WEBHOOK_SECRET": strings.Repeat("s", 32)}, "not \"manual\""},
		{map[string]string{"SAAS_WEBHOOK_PROVIDER": "Pay OS", "SAAS_WEBHOOK_SECRET": strings.Repeat("s", 32)}, "lowercase letters"},
		{map[string]string{"SAAS_WEBHOOK_PROVIDER": "payos", "SAAS_WEBHOOK_SECRET": "short"}, "SAAS_WEBHOOK_SECRET must be at least"},
	} {
		env := devEnv()
		for k, v := range tc.set {
			env[k] = v
		}
		if _, err := LoadServe(lookupFrom(env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.set, err, tc.want)
		}
	}
}

func TestLoadServeHonoursFaultInjectionOnlyInTest(t *testing.T) {
	for _, appEnv := range []string{"development", "production"} {
		env := devEnv()
		env["APP_ENV"] = appEnv
		if appEnv == "production" {
			secret := strings.Repeat("k", 44)
			env["PUBLIC_BASE_URL"] = "https://sidecup.app"
			env["SESSION_HASH_KEY"], env["CSRF_KEY"], env["APP_ENCRYPTION_KEY"] = secret, secret, secret
			env["TRUSTED_PROXY_CIDRS"] = "10.0.0.5"
		}
		env["TALLY_FAULT_INJECT"] = "posting.before_commit:3"
		c, err := LoadServe(lookupFrom(env))
		if err != nil {
			t.Fatalf("%s: %v", appEnv, err)
		}
		if c.Faults != nil || !c.FaultsIgnored {
			t.Errorf("%s: faults = %v ignored = %v, want an inert hook reported as ignored", appEnv, c.Faults, c.FaultsIgnored)
		}
		// A spec that would be invalid under test is not even read elsewhere.
		env["TALLY_FAULT_INJECT"] = "nonsense"
		if _, err := LoadServe(lookupFrom(env)); err != nil {
			t.Errorf("%s: invalid spec rejected outside test: %v", appEnv, err)
		}
	}

	env := devEnv()
	env["APP_ENV"] = "test"
	env["TALLY_FAULT_INJECT"] = "posting.before_commit@pos.checkout"
	c, err := LoadServe(lookupFrom(env))
	if err != nil || c.Faults == nil || c.FaultsIgnored || !c.Faults.Armed() {
		t.Fatalf("test env: faults = %v ignored = %v err = %v", c.Faults, c.FaultsIgnored, err)
	}
	env["TALLY_FAULT_INJECT"] = "nonsense"
	if _, err := LoadServe(lookupFrom(env)); err == nil || !strings.Contains(err.Error(), "TALLY_FAULT_INJECT") {
		t.Errorf("test env: invalid spec = %v, want an error naming the variable", err)
	}
	delete(env, "TALLY_FAULT_INJECT")
	if c, err := LoadServe(lookupFrom(env)); err != nil || c.Faults != nil || c.FaultsIgnored {
		t.Errorf("unset: faults = %v ignored = %v err = %v", c.Faults, c.FaultsIgnored, err)
	}
}
