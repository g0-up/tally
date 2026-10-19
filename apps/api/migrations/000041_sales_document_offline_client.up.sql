SET search_path = tally;

-- An offline POS sale is posted once whatever the number of times the device sends it.
CREATE UNIQUE INDEX ux_sales_documents__offline_client_id ON sales_documents (tenant_id, offline_client_id)
  WHERE offline_client_id IS NOT NULL;
