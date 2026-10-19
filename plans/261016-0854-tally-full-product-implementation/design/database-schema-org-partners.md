# Schema — Platform, Organization, Control, Partners

Conventions: [database-conventions.md](database-conventions.md). Every table in §2–§4 is a **tenant table**: its migration also runs the RLS block (conventions §4). Tables in §1 are **platform tables** (no RLS, accessed only through `identity`/`saas` repositories with explicit `tenant_id` filters). No defaults call functions; Go sets ids and timestamps.

Migrations: `000001_platform_identity`, `000002_org_core`, `000003_org_control`, `000004_partners`.

## 1. Platform & identity (no RLS)

```sql
CREATE TABLE tenants (
  id uuid PRIMARY KEY,
  slug text NOT NULL UNIQUE                           -- store subdomain <slug>.DOMAIN, immutable (architecture §7.2); a sandbox reset renames the replaced sandbox to reset-<id hex> and hands the address to the new one
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$' AND slug !~ '--'),
  legal_name text NOT NULL, display_name text NOT NULL,
  tax_code text, address text, phone text,
  currency char(3) NOT NULL DEFAULT 'VND',
  timezone text NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','read_only','closing','closed','replaced')),
    CHECK (status <> 'replaced' OR is_sandbox),       -- replaced: a sandbox superseded by a reset (ORG-06), kept read by nobody
  is_sandbox bool NOT NULL DEFAULT false,             -- ORG-06 demo store, excluded from real reports
  sandbox_of uuid REFERENCES tenants (id),            -- demo tenant linked to the real one
  opening_cutoff_date date,                           -- DEBT-02 / onboarding cutoff
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  version int NOT NULL DEFAULT 1
);

CREATE TABLE retired_tenant_slugs (                   -- slugs of purged tenants, never reissued
  slug text PRIMARY KEY,
  tenant_id uuid NOT NULL,                            -- no FK: the tenant row is gone after purge
  retired_at timestamptz NOT NULL
);

CREATE TABLE users (                                  -- the single owner account (SEC-01)
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants (id),
  email text NOT NULL, email_key text NOT NULL,       -- textnorm.EmailKey (lowercase, trimmed)
  phone text, full_name text NOT NULL,
  password_hash text NOT NULL,                        -- argon2id PHC string
  email_verified_at timestamptz,
  mfa_enabled bool NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('pending_verification','active','locked','disabled')),
  failed_login_count int NOT NULL DEFAULT 0, locked_until timestamptz,
  last_login_at timestamptz,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (email_key)
);
CREATE UNIQUE INDEX ux_users__one_active_owner ON users (tenant_id) WHERE status IN ('pending_verification','active','locked');

CREATE TABLE sessions (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL, user_id uuid NOT NULL,
  token_hash bytea NOT NULL UNIQUE,                   -- SHA-256 of opaque cookie token
  device_label text NOT NULL, user_agent text, ip inet,
  created_at timestamptz NOT NULL, last_seen_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  mfa_verified_at timestamptz, reauthenticated_at timestamptz,   -- SEC-06 sensitive actions
  offline_until timestamptz,                          -- POS offline credential horizon (§11.3)
  revoked_at timestamptz, revoked_reason text,
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);
CREATE INDEX ix_sessions__user ON sessions (tenant_id, user_id, revoked_at);

CREATE TABLE auth_tokens (                            -- single-use, expiring (ORG-01)
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL, user_id uuid NOT NULL,
  purpose text NOT NULL CHECK (purpose IN ('verify_email','reset_password','owner_transfer','support_consent')),
  token_hash bytea NOT NULL UNIQUE,
  payload jsonb NOT NULL DEFAULT '{}',
  expires_at timestamptz NOT NULL, used_at timestamptz, created_at timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);
CREATE INDEX ix_auth_tokens__user ON auth_tokens (tenant_id, user_id);   -- account moves (sandbox reset) and FK checks

CREATE TABLE mfa_totp_secrets (
  user_id uuid PRIMARY KEY REFERENCES users (id),
  secret_ciphertext bytea NOT NULL,                   -- AES-GCM with APP_ENCRYPTION_KEY
  confirmed_at timestamptz, created_at timestamptz NOT NULL,
  last_used_step bigint CHECK (last_used_step >= 0)  -- last accepted TOTP time step; a step <= it is refused (replay)
);

CREATE TABLE mfa_recovery_codes (
  id uuid PRIMARY KEY, user_id uuid NOT NULL REFERENCES users (id),
  code_hash text NOT NULL, used_at timestamptz, created_at timestamptz NOT NULL
);

CREATE TABLE login_attempts (                         -- brute-force throttle (ORG-01)
  id uuid PRIMARY KEY, email_key text NOT NULL, ip inet,
  succeeded bool NOT NULL, created_at timestamptz NOT NULL
);
CREATE INDEX ix_login_attempts__email_time ON login_attempts (email_key, created_at);
CREATE INDEX ix_login_attempts__ip_time ON login_attempts (ip, created_at);

CREATE TABLE platform_admins (                        -- SaaS operator accounts (SAAS-06)
  id uuid PRIMARY KEY, email text NOT NULL, email_key text NOT NULL UNIQUE,
  full_name text NOT NULL, password_hash text NOT NULL,
  mfa_secret_ciphertext bytea NOT NULL,               -- MFA mandatory for admins
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE platform_admin_sessions (
  id uuid PRIMARY KEY, admin_id uuid NOT NULL REFERENCES platform_admins (id),
  token_hash bytea NOT NULL UNIQUE, ip inet, created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL, revoked_at timestamptz
);
```

## 2. Organization (tenant tables)

```sql
CREATE TABLE stores (                                 -- exactly one per tenant (ORG-03)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, address text, phone text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id)
);

CREATE TABLE warehouses (                             -- exactly one; id kept on every stock row
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, store_id uuid NOT NULL,
  name text NOT NULL,                                 -- "Kho cửa hàng"
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id),
  FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);

CREATE TABLE tenant_settings (
  tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
  prices_include_tax bool NOT NULL DEFAULT true,      -- PRC-06
  rounding_mode text NOT NULL DEFAULT 'line' CHECK (rounding_mode IN ('line','document')),
  default_payment_terms_days int NOT NULL DEFAULT 0 CHECK (default_payment_terms_days >= 0),
  block_expired_lot_sale bool NOT NULL DEFAULT true,  -- INV-07
  expiry_warning_days int NOT NULL DEFAULT 30 CHECK (expiry_warning_days >= 0),
  offline_pos_enabled bool NOT NULL DEFAULT false,
  offline_pos_max_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (offline_pos_max_amount >= 0),
  offline_pos_max_hours int NOT NULL DEFAULT 12 CHECK (offline_pos_max_hours BETWEEN 0 AND 72),
  analytics_ai_enabled bool NOT NULL DEFAULT false,   -- RPT-11 / NFR-16 opt-in
  product_analytics_enabled bool NOT NULL DEFAULT false,  -- + SAAS-10 consent for usage analytics
  updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1
);

CREATE TABLE doc_sequences (                          -- ORG-05, row-locked issuer
  tenant_id uuid NOT NULL REFERENCES tenants (id),
  prefix text NOT NULL CHECK (prefix ~ '^[A-Z]{2,3}$'),
  next_value bigint NOT NULL DEFAULT 1 CHECK (next_value > 0),
  PRIMARY KEY (tenant_id, prefix)
);
```

`CHECK (prefix ~ '^[A-Z]{2,3}$')` uses the regex match operator, not a function; it passes the migration lint.

## 3. Control (tenant tables)

```sql
CREATE TABLE idempotency_keys (
  tenant_id uuid NOT NULL REFERENCES tenants (id),
  scope text NOT NULL,                                -- e.g. 'pos.checkout', 'stock_out.create'
  key text NOT NULL,
  request_hash bytea NOT NULL,
  response_status int, response_body jsonb,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, scope, key)
);
CREATE INDEX ix_idempotency_keys__expiry ON idempotency_keys (tenant_id, expires_at);   -- purge runs per tenant

CREATE TABLE audit_events (                           -- append-only (SEC-04)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  actor_type text NOT NULL CHECK (actor_type IN ('owner','support','system','platform_admin')),
  actor_id uuid,
  action text NOT NULL,                               -- 'invoice.confirm', 'stock_out.post', 'price.change', 'export.csv' …
  entity_type text NOT NULL, entity_id uuid, doc_no text,
  before jsonb, after jsonb,                          -- redacted by audit.Redactor (no secrets)
  source text NOT NULL CHECK (source IN ('web','pos','pos_offline_sync','cli','worker','support')),
  request_id text, ip inet,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id)
);
CREATE INDEX ix_audit_events__time ON audit_events (tenant_id, created_at DESC, id DESC);
CREATE INDEX ix_audit_events__entity ON audit_events (tenant_id, entity_type, entity_id);

CREATE TABLE period_locks (                           -- append-only event log (SEC-05)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  action text NOT NULL CHECK (action IN ('lock','reopen')),
  locked_through date,                                -- effective lock date after this event; NULL = fully reopened
  reason text,                                        -- required for reopen (Go)
  actor_id uuid NOT NULL, created_at timestamptz NOT NULL,
  CHECK (action = 'lock' OR reason IS NOT NULL),
  CONSTRAINT ck_period_locks__lock_date CHECK (action = 'reopen' OR locked_through IS NOT NULL)
);
CREATE INDEX ix_period_locks__latest ON period_locks (tenant_id, created_at DESC);
REVOKE UPDATE, DELETE ON period_locks FROM tally_app;

CREATE TABLE exception_confirmations (                -- append-only (SEC-03, PRC-04, DEBT-06)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL CHECK (kind IN ('credit_limit_exceeded','below_min_price','stock_disposal','count_variance',
                                     'debt_write_off','expired_lot_sale','quarantine_release','period_reopen')),
  doc_type text NOT NULL, doc_id uuid NOT NULL,
  doc_version int NOT NULL,                           -- invalid once the document version changes (§7.8)
  impact jsonb NOT NULL,                              -- what the owner saw: amounts, limits, prices
  confirmed_by uuid NOT NULL, confirmed_at timestamptz NOT NULL,
  payload_hash bytea NOT NULL,                        -- SHA-256 of the canonical JSON of the confirmed command (000027)
  note text,
  UNIQUE (tenant_id, id)
);
CREATE INDEX ix_exception_confirmations__doc ON exception_confirmations (tenant_id, doc_type, doc_id);

CREATE TABLE exception_confirmation_uses (            -- append-only; the primary key makes a second use fail (000027)
  tenant_id uuid NOT NULL, confirmation_id uuid NOT NULL,
  used_by_doc_type text NOT NULL, used_by_doc_id uuid NOT NULL, used_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, confirmation_id),
  FOREIGN KEY (tenant_id, confirmation_id) REFERENCES exception_confirmations (tenant_id, id)
);

CREATE TABLE attachments (                            -- DATA-05
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  owner_type text NOT NULL, owner_id uuid NOT NULL,   -- receipt, supplier_document, support_ticket, product …
  storage_key text NOT NULL,                          -- tenants/{tenant_id}/… in object storage
  file_name text NOT NULL, content_type text NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 20971520),
  sha256 bytea NOT NULL,
  scan_status text NOT NULL DEFAULT 'pending' CHECK (scan_status IN ('pending','clean','rejected')),
  version_no int NOT NULL DEFAULT 1, supersedes_id uuid,
  uploaded_by uuid NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (storage_key),
  FOREIGN KEY (tenant_id, supersedes_id) REFERENCES attachments (tenant_id, id)
);
CREATE INDEX ix_attachments__owner ON attachments (tenant_id, owner_type, owner_id);

CREATE TABLE outbox_events (                          -- transactional outbox for the worker
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  topic text NOT NULL,                                -- 'stock.changed', 'obligation.due_changed', 'lot.expiry_changed' …
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL, available_at timestamptz NOT NULL,
  attempts int NOT NULL DEFAULT 0, last_error text, processed_at timestamptz
);
CREATE INDEX ix_outbox_events__pending ON outbox_events (tenant_id, available_at) WHERE processed_at IS NULL;   -- dispatched per tenant

CREATE TABLE reasons (                                -- CAT-07
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL CHECK (kind IN ('sales_return','purchase_return','damage','loss','gift','internal_use',
                                     'count_variance','disposal','write_off','close_short','price_adjustment',
                                     'debt_adjustment','reopen','return_without_source')),
  name text NOT NULL, name_key text NOT NULL,
  is_system bool NOT NULL DEFAULT false, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, kind, name_key)
);
```

`payment_methods` and `fund_accounts` live in [debt & cash](database-schema-debt-cash-crm-saas.md) because they reference fund accounts.

## 4. Partners (tenant tables)

```sql
CREATE TABLE parties (                                -- one party may be customer and supplier (CAT-06)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL DEFAULT 'organization' CHECK (kind IN ('person','organization')),
  name text NOT NULL, name_key text NOT NULL,
  phone text, phone_key text,                         -- digits only, for search
  email text, tax_code text, address text, note text,
  search_norm text NOT NULL DEFAULT '',               -- name + phone + tax code, no diacritics (CAT-08)
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id)
);
CREATE INDEX ix_parties__name ON parties (tenant_id, name_key);
CREATE INDEX ix_parties__phone ON parties (tenant_id, phone_key);

CREATE TABLE customer_groups (                        -- seed: Khách lẻ (retail), Khách sỉ (wholesale)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  base_price_kind text NOT NULL CHECK (base_price_kind IN ('retail','wholesale')),
  is_system bool NOT NULL DEFAULT false, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key)
);

CREATE TABLE customer_profiles (
  tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  customer_group_id uuid NOT NULL,
  credit_limit numeric(19,4) CHECK (credit_limit IS NULL OR credit_limit >= 0),   -- NULL = no limit
  payment_terms_days int NOT NULL DEFAULT 0 CHECK (payment_terms_days >= 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  PRIMARY KEY (tenant_id, party_id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_group_id) REFERENCES customer_groups (tenant_id, id)
);

CREATE TABLE supplier_profiles (                      -- CAT-05: Đang giao dịch / Ngừng giao dịch
  tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  payment_terms_days int NOT NULL DEFAULT 0 CHECK (payment_terms_days >= 0),
  supplied_goods_note text,                           -- "mặt hàng cung cấp" free text, searchable
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  PRIMARY KEY (tenant_id, party_id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);

CREATE TABLE contacts (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  name text NOT NULL, phone text, email text, role_title text,
  is_primary bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_contacts__primary ON contacts (tenant_id, party_id) WHERE is_primary;

CREATE TABLE party_addresses (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  label text NOT NULL, address_line text NOT NULL, is_default bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_party_addresses__default ON party_addresses (tenant_id, party_id) WHERE is_default;
```

## 5. Invariants enforced in Go

| Invariant | Owner |
|---|---|
| Exactly one store and warehouse per tenant | `org.TenantProvisioner`, DB `UNIQUE (tenant_id)` |
| Supplier with documents cannot be deleted, only deactivated; inactive suppliers hidden from pickers but payable remains settleable (CAT-05) | `partner.Service.DeleteSupplier` checks `procurement.UsageChecker` port |
| Receivable and payable for the same party never net automatically (CAT-06) | `debt` keeps `direction`; offsets only through `debt_offsets` |
| Reopening a period needs reason and exception confirmation | `control.PeriodService` |
| Exception confirmation valid only for the confirmed `doc_version` | `control.ExceptionGuard` |
| Audit payload never contains password, token, secret (SEC-04) | `audit.Redactor` allowlist |
