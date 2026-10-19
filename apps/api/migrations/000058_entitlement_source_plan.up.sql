SET search_path = tally;

-- The add-on plan whose purchase granted a module row; null on plan-sourced rows, so a row has
-- one source at most: the subscription of a plan or an add-on plan.
ALTER TABLE entitlements ADD COLUMN source_plan_id uuid
  CONSTRAINT fk_entitlements__source_plan_id REFERENCES plans (id);
ALTER TABLE entitlements ADD CONSTRAINT ck_entitlements__single_source
  CHECK (source_subscription_id IS NULL OR source_plan_id IS NULL);

-- A plan row and the rows of each add-on plan enabling the same module may start the same day;
-- each keeps its own period.
ALTER TABLE entitlements DROP CONSTRAINT ux_entitlements__key_valid_from;
ALTER TABLE entitlements ADD CONSTRAINT ux_entitlements__key_valid_from_source
  UNIQUE NULLS NOT DISTINCT (tenant_id, key, valid_from, source_subscription_id, source_plan_id);

-- Name the add-on of rows granted before the column, where the paid add-on invoices of the store
-- starting that day are all of one plan. Other rows keep enabling their module.
UPDATE entitlements e SET source_plan_id = i.plan_id
FROM saas_invoices i
  LEFT JOIN saas_invoices o ON o.tenant_id = i.tenant_id AND o.purpose = 'addon' AND o.status = 'paid'
    AND o.period_start = i.period_start AND o.plan_id <> i.plan_id
WHERE e.source_subscription_id IS NULL AND e.source_plan_id IS NULL AND e.key LIKE 'module:%'
  AND i.tenant_id = e.tenant_id AND i.purpose = 'addon' AND i.status = 'paid' AND i.plan_id IS NOT NULL
  AND i.period_start = e.valid_from AND o.id IS NULL;
