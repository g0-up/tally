SET search_path = tally;

-- Exception confirmations are bound to the exact command payload the owner confirmed (SHA-256 of
-- its canonical JSON) and can be consumed once. The use is a separate append-only row so the
-- confirmation itself stays immutable; the primary key makes a second use fail.

ALTER TABLE exception_confirmations ADD COLUMN payload_hash bytea NOT NULL;
ALTER TABLE exception_confirmations ADD COLUMN note text;

CREATE TABLE exception_confirmation_uses (            -- append-only, one row per consumed confirmation
  tenant_id uuid NOT NULL,
  confirmation_id uuid NOT NULL,
  used_by_doc_type text NOT NULL, used_by_doc_id uuid NOT NULL,
  used_at timestamptz NOT NULL,
  CONSTRAINT pk_exception_confirmation_uses PRIMARY KEY (tenant_id, confirmation_id),
  CONSTRAINT fk_exception_confirmation_uses__confirmation_id FOREIGN KEY (tenant_id, confirmation_id)
    REFERENCES exception_confirmations (tenant_id, id)
);
ALTER TABLE exception_confirmation_uses ENABLE ROW LEVEL SECURITY;
ALTER TABLE exception_confirmation_uses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON exception_confirmation_uses
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON exception_confirmation_uses TO tally_app;
REVOKE UPDATE, DELETE ON exception_confirmation_uses FROM tally_app;
GRANT SELECT ON exception_confirmation_uses TO tally_readonly;
