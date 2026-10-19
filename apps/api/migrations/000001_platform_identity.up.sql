SET search_path = tally;

-- Platform and identity tables. Authentication runs before a tenant is known, so these tables are
-- not under row level security; only the identity and saas repositories touch them and every query
-- filters tenant_id explicitly. Support read-only sessions must never read them.

CREATE TABLE tenants (
  id uuid CONSTRAINT pk_tenants PRIMARY KEY,
  slug text NOT NULL CONSTRAINT ux_tenants__slug UNIQUE                           -- store subdomain <slug>.DOMAIN, immutable
    CONSTRAINT ck_tenants__slug CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$' AND slug !~ '--'),
  legal_name text NOT NULL, display_name text NOT NULL,
  tax_code text, address text, phone text,
  currency char(3) NOT NULL DEFAULT 'VND',
  timezone text NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_tenants__status CHECK (status IN ('active','read_only','closing','closed')),
  is_sandbox bool NOT NULL DEFAULT false,             -- demo store, excluded from real reports
  sandbox_of uuid CONSTRAINT fk_tenants__sandbox_of REFERENCES tenants (id),            -- demo tenant linked to the real one
  opening_cutoff_date date,                           -- opening balances / onboarding cutoff
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  version int NOT NULL DEFAULT 1
);

CREATE TABLE retired_tenant_slugs (                   -- slugs of purged tenants, never reissued
  slug text CONSTRAINT pk_retired_tenant_slugs PRIMARY KEY,
  tenant_id uuid NOT NULL,                            -- no FK: the tenant row is gone after purge
  retired_at timestamptz NOT NULL
);

CREATE TABLE users (                                  -- the single owner account
  id uuid CONSTRAINT pk_users PRIMARY KEY,
  tenant_id uuid NOT NULL CONSTRAINT fk_users__tenant_id REFERENCES tenants (id),
  email text NOT NULL, email_key text NOT NULL,       -- textnorm.EmailKey (lowercase, trimmed)
  phone text, full_name text NOT NULL,
  password_hash text NOT NULL,                        -- argon2id PHC string
  email_verified_at timestamptz,
  mfa_enabled bool NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_users__status CHECK (status IN ('pending_verification','active','locked','disabled')),
  failed_login_count int NOT NULL DEFAULT 0, locked_until timestamptz,
  last_login_at timestamptz,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_users__id UNIQUE (tenant_id, id), CONSTRAINT ux_users__email_key UNIQUE (email_key)
);
CREATE UNIQUE INDEX ux_users__one_active_owner ON users (tenant_id) WHERE status IN ('pending_verification','active','locked');

CREATE TABLE sessions (
  id uuid CONSTRAINT pk_sessions PRIMARY KEY,
  tenant_id uuid NOT NULL, user_id uuid NOT NULL,
  token_hash bytea NOT NULL CONSTRAINT ux_sessions__token_hash UNIQUE,                   -- SHA-256 of opaque cookie token
  device_label text NOT NULL, user_agent text, ip inet,
  created_at timestamptz NOT NULL, last_seen_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  mfa_verified_at timestamptz, reauthenticated_at timestamptz,   -- sensitive actions
  offline_until timestamptz,                          -- POS offline credential horizon
  revoked_at timestamptz, revoked_reason text,
  CONSTRAINT fk_sessions__user_id FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);
CREATE INDEX ix_sessions__user ON sessions (tenant_id, user_id, revoked_at);

CREATE TABLE auth_tokens (                            -- single-use, expiring
  id uuid CONSTRAINT pk_auth_tokens PRIMARY KEY,
  tenant_id uuid NOT NULL, user_id uuid NOT NULL,
  purpose text NOT NULL CONSTRAINT ck_auth_tokens__purpose CHECK (purpose IN ('verify_email','reset_password','owner_transfer','support_consent')),
  token_hash bytea NOT NULL CONSTRAINT ux_auth_tokens__token_hash UNIQUE,
  payload jsonb NOT NULL DEFAULT '{}',
  expires_at timestamptz NOT NULL, used_at timestamptz, created_at timestamptz NOT NULL,
  CONSTRAINT fk_auth_tokens__user_id FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE TABLE mfa_totp_secrets (
  user_id uuid CONSTRAINT pk_mfa_totp_secrets PRIMARY KEY CONSTRAINT fk_mfa_totp_secrets__user_id REFERENCES users (id),
  secret_ciphertext bytea NOT NULL,                   -- AES-GCM with APP_ENCRYPTION_KEY
  confirmed_at timestamptz, created_at timestamptz NOT NULL
);

CREATE TABLE mfa_recovery_codes (
  id uuid CONSTRAINT pk_mfa_recovery_codes PRIMARY KEY, user_id uuid NOT NULL CONSTRAINT fk_mfa_recovery_codes__user_id REFERENCES users (id),
  code_hash text NOT NULL, used_at timestamptz, created_at timestamptz NOT NULL
);

CREATE TABLE login_attempts (                         -- brute-force throttle
  id uuid CONSTRAINT pk_login_attempts PRIMARY KEY, email_key text NOT NULL, ip inet,
  succeeded bool NOT NULL, created_at timestamptz NOT NULL
);
CREATE INDEX ix_login_attempts__email_time ON login_attempts (email_key, created_at);
CREATE INDEX ix_login_attempts__ip_time ON login_attempts (ip, created_at);

CREATE TABLE platform_admins (                        -- SaaS operator accounts
  id uuid CONSTRAINT pk_platform_admins PRIMARY KEY, email text NOT NULL, email_key text NOT NULL CONSTRAINT ux_platform_admins__email_key UNIQUE,
  full_name text NOT NULL, password_hash text NOT NULL,
  mfa_secret_ciphertext bytea NOT NULL,               -- MFA mandatory for admins
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_platform_admins__status CHECK (status IN ('active','disabled')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE platform_admin_sessions (
  id uuid CONSTRAINT pk_platform_admin_sessions PRIMARY KEY, admin_id uuid NOT NULL CONSTRAINT fk_platform_admin_sessions__admin_id REFERENCES platform_admins (id),
  token_hash bytea NOT NULL CONSTRAINT ux_platform_admin_sessions__token_hash UNIQUE, ip inet, created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL, revoked_at timestamptz
);

REVOKE ALL ON tenants, retired_tenant_slugs, users, sessions, auth_tokens, mfa_totp_secrets,
  mfa_recovery_codes, login_attempts, platform_admins, platform_admin_sessions FROM tally_readonly;
