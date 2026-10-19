SET search_path = tally;

CREATE TABLE statements (                             -- DS
  id uuid CONSTRAINT pk_statements PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CONSTRAINT ck_statements__direction CHECK (direction IN ('receivable','payable')),
  period_from date NOT NULL, period_to date NOT NULL,
  status text NOT NULL DEFAULT 'draft' CONSTRAINT ck_statements__status CHECK (status IN ('draft','sent','confirmed','disputed','superseded')),
  current_version int NOT NULL DEFAULT 0, dispute_note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_statements__id UNIQUE (tenant_id, id), CONSTRAINT ux_statements__doc_no UNIQUE (tenant_id, doc_no),
  CONSTRAINT ck_statements__period_to_period_from CHECK (period_to >= period_from),
  CONSTRAINT fk_statements__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
ALTER TABLE statements ENABLE ROW LEVEL SECURITY;
ALTER TABLE statements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON statements
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE statement_versions (                     -- append-only: sent numbers never change silently
  id uuid CONSTRAINT pk_statement_versions PRIMARY KEY, tenant_id uuid NOT NULL, statement_id uuid NOT NULL,
  version_no int NOT NULL CONSTRAINT ck_statement_versions__version_no CHECK (version_no > 0),
  opening_balance numeric(19,4) NOT NULL, closing_balance numeric(19,4) NOT NULL,
  snapshot jsonb NOT NULL,                            -- transactions as of generation
  sent_at timestamptz, created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_statement_versions__statement_id_version_no UNIQUE (tenant_id, statement_id, version_no),
  CONSTRAINT fk_statement_versions__statement_id FOREIGN KEY (tenant_id, statement_id) REFERENCES statements (tenant_id, id),
  CONSTRAINT ux_statement_versions__id UNIQUE (tenant_id, id)
);
REVOKE UPDATE, DELETE ON statement_versions FROM tally_app;
ALTER TABLE statement_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE statement_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON statement_versions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE collection_notes (                       -- contact notes, payment promises
  id uuid CONSTRAINT pk_collection_notes PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CONSTRAINT ck_collection_notes__direction CHECK (direction IN ('receivable','payable')),
  kind text NOT NULL CONSTRAINT ck_collection_notes__kind CHECK (kind IN ('note','call','promise','reminder_sent')),
  body text, promised_date date, promised_amount numeric(19,4) CONSTRAINT ck_collection_notes__promised_amount CHECK (promised_amount IS NULL OR promised_amount > 0),
  channel text CONSTRAINT ck_collection_notes__channel CHECK (channel IN ('phone','sms','zalo','email','in_person')),
  dedupe_key text,                                    -- reminder retries never send twice
  created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_collection_notes__id UNIQUE (tenant_id, id),
  CONSTRAINT ck_collection_notes__kind_promised_date CHECK (kind <> 'promise' OR promised_date IS NOT NULL),
  CONSTRAINT fk_collection_notes__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_collection_notes__dedupe ON collection_notes (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
ALTER TABLE collection_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE collection_notes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON collection_notes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
