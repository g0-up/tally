SET search_path = tally;

DROP TABLE loyalty_entries;
DROP TABLE loyalty_programs;
DROP TABLE consents;
DROP TABLE customer_care_tasks;
DROP TABLE customer_activities;
DROP TABLE campaign_checklist_items;
DROP TABLE campaign_attributions;
ALTER TABLE obligations DROP CONSTRAINT fk_obligations__campaign_id;
ALTER TABLE assembly_orders DROP CONSTRAINT fk_assembly_orders__campaign_id;
ALTER TABLE sales_documents DROP CONSTRAINT fk_sales_documents__campaign_id;
ALTER TABLE invoices DROP CONSTRAINT fk_invoices__campaign_id;
ALTER TABLE quotes DROP CONSTRAINT fk_quotes__campaign_id;
ALTER TABLE sales_document_lines DROP CONSTRAINT fk_sales_document_lines__campaign_id;
ALTER TABLE promotions DROP CONSTRAINT fk_promotions__campaign_id;
ALTER TABLE price_lists DROP CONSTRAINT fk_price_lists__campaign_id;
DROP TABLE campaigns;
