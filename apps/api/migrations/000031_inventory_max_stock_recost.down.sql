SET search_path = tally;

-- The old CHECK cannot hold the COGS adjustments of applied cost recalculations, and dropping them
-- would silently change the books. Refuse the rollback while any exists: this constraint fails
-- first, naming why; restore the backup taken before the migration instead. It is dropped again
-- right away when there is none.
ALTER TABLE cogs_adjustments ADD CONSTRAINT ck_cogs_adjustments__down_refused_cost_recalculation_applied
  CHECK (source_doc_type <> 'cost_recalculation');
ALTER TABLE cogs_adjustments DROP CONSTRAINT ck_cogs_adjustments__down_refused_cost_recalculation_applied;

ALTER TABLE cogs_adjustments DROP CONSTRAINT ck_cogs_adjustments__source_doc_type_kinds;
ALTER TABLE cogs_adjustments ADD CONSTRAINT ck_cogs_adjustments__source_doc_type
  CHECK (source_doc_type IN ('landed_cost_allocation','purchase_price_adjustment'));

ALTER TABLE skus DROP CONSTRAINT ck_skus__max_stock;
ALTER TABLE skus DROP COLUMN max_stock;
