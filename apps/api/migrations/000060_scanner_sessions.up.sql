SET search_path = tally;

-- A scanner session pairs one signed-in desktop tab with one phone. The claim code (shown as a QR
-- code) and the phone secret (kept in an HttpOnly cookie) are stored as SHA-256 hashes only. The
-- session state is derived from the timestamps; see the scanner package for the one predicate.
CREATE TABLE scanner_sessions (
  id uuid CONSTRAINT pk_scanner_sessions PRIMARY KEY,
  tenant_id uuid NOT NULL CONSTRAINT fk_scanner_sessions__tenant_id REFERENCES tenants (id),
  created_by uuid NOT NULL, login_session_id uuid NOT NULL, created_at timestamptz NOT NULL,
  claim_code_hash bytea, claim_expires_at timestamptz,  -- NULL once a phone claimed the session
  phone_secret_hash bytea, claimed_at timestamptz, device_label text,
  approved_at timestamptz,                              -- the desktop accepted the phone
  desktop_seen_at timestamptz NOT NULL,                 -- the desktop event stream was last alive
  revoked_at timestamptz,
  revoked_by text CONSTRAINT ck_scanner_sessions__revoked_by CHECK (revoked_by IN ('desktop','phone','system')),
  CONSTRAINT ux_scanner_sessions__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_scanner_sessions__claim_code_hash UNIQUE (tenant_id, claim_code_hash),
  CONSTRAINT ux_scanner_sessions__phone_secret_hash UNIQUE (tenant_id, phone_secret_hash)
);
CREATE INDEX ix_scanner_sessions__creator ON scanner_sessions (tenant_id, created_by, revoked_at);
ALTER TABLE scanner_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE scanner_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scanner_sessions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- One row per Idempotency-Key a phone sent; result stays NULL until the desktop answers.
CREATE TABLE scanner_scans (
  id uuid CONSTRAINT pk_scanner_scans PRIMARY KEY, tenant_id uuid NOT NULL, session_id uuid NOT NULL,
  idempotency_key text NOT NULL, request_hash bytea NOT NULL, code text NOT NULL,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  result jsonb, resolved_at timestamptz,
  CONSTRAINT ux_scanner_scans__session_key UNIQUE (tenant_id, session_id, idempotency_key),
  CONSTRAINT fk_scanner_scans__session_id FOREIGN KEY (tenant_id, session_id)
    REFERENCES scanner_sessions (tenant_id, id) ON DELETE CASCADE
);
ALTER TABLE scanner_scans ENABLE ROW LEVEL SECURITY;
ALTER TABLE scanner_scans FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scanner_scans
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON scanner_sessions, scanner_scans TO tally_app;
GRANT SELECT ON scanner_sessions, scanner_scans TO tally_readonly;
