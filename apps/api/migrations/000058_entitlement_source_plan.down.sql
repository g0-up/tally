SET search_path = tally;

-- One row per module and day again, as add-on grants kept it: of the add-on rows starting the same
-- day only the one ending last stays, and it folds into a plan row of its module starting that
-- day, the plan row ending when the later of the two does.
DELETE FROM entitlements a
USING entitlements b
WHERE a.source_subscription_id IS NULL AND b.source_subscription_id IS NULL AND b.id <> a.id
  AND b.tenant_id = a.tenant_id AND b.key = a.key AND b.valid_from = a.valid_from
  AND ((b.valid_to IS NULL AND a.valid_to IS NOT NULL) OR b.valid_to > a.valid_to
    OR (b.valid_to IS NOT DISTINCT FROM a.valid_to AND b.id > a.id));
UPDATE entitlements p
SET valid_to = CASE WHEN p.valid_to >= a.valid_to THEN p.valid_to ELSE a.valid_to END
FROM entitlements a
WHERE p.source_subscription_id IS NOT NULL AND a.source_subscription_id IS NULL
  AND a.tenant_id = p.tenant_id AND a.key = p.key AND a.valid_from = p.valid_from;
DELETE FROM entitlements a
USING entitlements p
WHERE p.source_subscription_id IS NOT NULL AND a.source_subscription_id IS NULL
  AND a.tenant_id = p.tenant_id AND a.key = p.key AND a.valid_from = p.valid_from;

ALTER TABLE entitlements DROP CONSTRAINT ux_entitlements__key_valid_from_source;
ALTER TABLE entitlements ADD CONSTRAINT ux_entitlements__key_valid_from UNIQUE (tenant_id, key, valid_from);

ALTER TABLE entitlements DROP CONSTRAINT ck_entitlements__single_source;
ALTER TABLE entitlements DROP COLUMN source_plan_id;
