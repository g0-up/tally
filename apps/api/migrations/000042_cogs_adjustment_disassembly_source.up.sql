SET search_path = tally;

-- A disassembly gives its components back at their cost and expenses the rest of the kits' value
-- (their packing cost) as a COGS adjustment of the kit SKU.
ALTER TABLE cogs_adjustments DROP CONSTRAINT ck_cogs_adjustments__source_doc_types;
ALTER TABLE cogs_adjustments ADD CONSTRAINT ck_cogs_adjustments__source_doc_type_values
  CHECK (source_doc_type IN ('landed_cost_allocation', 'purchase_price_adjustment', 'cost_recalculation', 'purchase_return',
                             'disassembly'));
