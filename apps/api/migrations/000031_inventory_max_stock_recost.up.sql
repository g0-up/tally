SET search_path = tally;

-- Overstock signal: a SKU is over stock when on_hand > max_stock. NULL means no upper bound.
ALTER TABLE skus ADD COLUMN max_stock numeric(18,4);
ALTER TABLE skus ADD CONSTRAINT ck_skus__max_stock CHECK (max_stock IS NULL OR max_stock >= min_stock);

-- A confirmed backdate recost run books the cost delta of quantities already sold as a COGS adjustment.
ALTER TABLE cogs_adjustments DROP CONSTRAINT ck_cogs_adjustments__source_doc_type;
ALTER TABLE cogs_adjustments ADD CONSTRAINT ck_cogs_adjustments__source_doc_type_kinds
  CHECK (source_doc_type IN ('landed_cost_allocation','purchase_price_adjustment','cost_recalculation'));
