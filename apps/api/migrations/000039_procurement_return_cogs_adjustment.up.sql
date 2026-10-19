SET search_path = tally;

-- A purchase return takes its goods out at the stock's average cost but credits the supplier at
-- the receipt cost. When the stock cannot carry the difference (it holds nothing any more, or the
-- difference would take its value below zero) the rest is a COGS adjustment of the return.
ALTER TABLE cogs_adjustments DROP CONSTRAINT ck_cogs_adjustments__source_doc_type_kinds;
ALTER TABLE cogs_adjustments ADD CONSTRAINT ck_cogs_adjustments__source_doc_types
  CHECK (source_doc_type IN ('landed_cost_allocation', 'purchase_price_adjustment', 'cost_recalculation', 'purchase_return'));
