SET search_path = tally;

ALTER TABLE cogs_adjustments DROP CONSTRAINT ck_cogs_adjustments__source_doc_type_values;
ALTER TABLE cogs_adjustments ADD CONSTRAINT ck_cogs_adjustments__source_doc_types
  CHECK (source_doc_type IN ('landed_cost_allocation', 'purchase_price_adjustment', 'cost_recalculation', 'purchase_return'));
