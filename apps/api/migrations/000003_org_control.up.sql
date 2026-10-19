SET search_path = tally;

CREATE TABLE idempotency_keys (
  tenant_id uuid NOT NULL CONSTRAINT fk_idempotency_keys__tenant_id REFERENCES tenants (id),
  scope text NOT NULL,                                -- e.g. 'pos.checkout', 'stock_out.create'
  key text NOT NULL,
  request_hash bytea NOT NULL,
  response_status int, response_body jsonb,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  CONSTRAINT pk_idempotency_keys PRIMARY KEY (tenant_id, scope, key)
);
CREATE INDEX ix_idempotency_keys__expiry ON idempotency_keys (tenant_id, expires_at);   -- purge runs per tenant
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON idempotency_keys
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE audit_events (                           -- append-only
  id uuid CONSTRAINT pk_audit_events PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_audit_events__tenant_id REFERENCES tenants (id),
  actor_type text NOT NULL CONSTRAINT ck_audit_events__actor_type CHECK (actor_type IN ('owner','support','system','platform_admin')),
  actor_id uuid,
  action text NOT NULL,                               -- 'invoice.confirm', 'stock_out.post', 'price.change', 'export.csv' …
  entity_type text NOT NULL, entity_id uuid, doc_no text,
  before jsonb, after jsonb,                          -- redacted by audit.Redactor (no secrets)
  source text NOT NULL CONSTRAINT ck_audit_events__source CHECK (source IN ('web','pos','pos_offline_sync','cli','worker','support')),
  request_id text, ip inet,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_audit_events__id UNIQUE (tenant_id, id)
);
CREATE INDEX ix_audit_events__time ON audit_events (tenant_id, created_at DESC, id DESC);
CREATE INDEX ix_audit_events__entity ON audit_events (tenant_id, entity_type, entity_id);
REVOKE UPDATE, DELETE ON audit_events FROM tally_app;
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_events
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE period_locks (                           -- append-only event log
  id uuid CONSTRAINT pk_period_locks PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_period_locks__tenant_id REFERENCES tenants (id),
  action text NOT NULL CONSTRAINT ck_period_locks__action CHECK (action IN ('lock','reopen')),
  locked_through date NOT NULL,                       -- effective lock date after this event
  reason text,                                        -- required for reopen (Go)
  actor_id uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ck_period_locks__action_reason CHECK (action = 'lock' OR reason IS NOT NULL)
);
CREATE INDEX ix_period_locks__latest ON period_locks (tenant_id, created_at DESC);
REVOKE UPDATE, DELETE ON period_locks FROM tally_app;
ALTER TABLE period_locks ENABLE ROW LEVEL SECURITY;
ALTER TABLE period_locks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON period_locks
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE exception_confirmations (                -- append-only
  id uuid CONSTRAINT pk_exception_confirmations PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_exception_confirmations__tenant_id REFERENCES tenants (id),
  kind text NOT NULL CONSTRAINT ck_exception_confirmations__kind CHECK (kind IN ('credit_limit_exceeded','below_min_price','stock_disposal','count_variance',
                                     'debt_write_off','expired_lot_sale','quarantine_release','period_reopen')),
  doc_type text NOT NULL, doc_id uuid NOT NULL,
  doc_version int NOT NULL,                           -- invalid once the document version changes
  impact jsonb NOT NULL,                              -- what the owner saw: amounts, limits, prices
  confirmed_by uuid NOT NULL, confirmed_at timestamptz NOT NULL,
  CONSTRAINT ux_exception_confirmations__id UNIQUE (tenant_id, id)
);
CREATE INDEX ix_exception_confirmations__doc ON exception_confirmations (tenant_id, doc_type, doc_id);
REVOKE UPDATE, DELETE ON exception_confirmations FROM tally_app;
ALTER TABLE exception_confirmations ENABLE ROW LEVEL SECURITY;
ALTER TABLE exception_confirmations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON exception_confirmations
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE attachments (
  id uuid CONSTRAINT pk_attachments PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_attachments__tenant_id REFERENCES tenants (id),
  owner_type text NOT NULL, owner_id uuid NOT NULL,   -- receipt, supplier_document, support_ticket, product …
  storage_key text NOT NULL,                          -- tenants/{tenant_id}/… in object storage
  file_name text NOT NULL, content_type text NOT NULL,
  size_bytes bigint NOT NULL CONSTRAINT ck_attachments__size_bytes CHECK (size_bytes > 0 AND size_bytes <= 20971520),
  sha256 bytea NOT NULL,
  scan_status text NOT NULL DEFAULT 'pending' CONSTRAINT ck_attachments__scan_status CHECK (scan_status IN ('pending','clean','rejected')),
  version_no int NOT NULL DEFAULT 1, supersedes_id uuid,
  uploaded_by uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_attachments__id UNIQUE (tenant_id, id), CONSTRAINT ux_attachments__storage_key UNIQUE (storage_key),
  CONSTRAINT fk_attachments__supersedes_id FOREIGN KEY (tenant_id, supersedes_id) REFERENCES attachments (tenant_id, id)
);
CREATE INDEX ix_attachments__owner ON attachments (tenant_id, owner_type, owner_id);
ALTER TABLE attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attachments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE outbox_events (                          -- transactional outbox for the worker
  id uuid CONSTRAINT pk_outbox_events PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_outbox_events__tenant_id REFERENCES tenants (id),
  topic text NOT NULL,                                -- 'stock.changed', 'obligation.due_changed', 'lot.expiry_changed' …
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL, available_at timestamptz NOT NULL,
  attempts int NOT NULL DEFAULT 0, last_error text, processed_at timestamptz
);
CREATE INDEX ix_outbox_events__pending ON outbox_events (tenant_id, available_at) WHERE processed_at IS NULL;   -- dispatched per tenant
ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON outbox_events
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE reasons (
  id uuid CONSTRAINT pk_reasons PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_reasons__tenant_id REFERENCES tenants (id),
  kind text NOT NULL CONSTRAINT ck_reasons__kind CHECK (kind IN ('sales_return','purchase_return','damage','loss','gift','internal_use',
                                     'count_variance','disposal','write_off','close_short','price_adjustment',
                                     'debt_adjustment','reopen','return_without_source')),
  name text NOT NULL, name_key text NOT NULL,
  is_system bool NOT NULL DEFAULT false, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_reasons__id UNIQUE (tenant_id, id), CONSTRAINT ux_reasons__kind_name_key UNIQUE (tenant_id, kind, name_key)
);
ALTER TABLE reasons ENABLE ROW LEVEL SECURITY;
ALTER TABLE reasons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON reasons
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
