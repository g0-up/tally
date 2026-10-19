SET search_path = tally;

-- What a season still has to deliver: its confirmed sales orders not delivered in full.
CREATE INDEX ix_invoices__campaign_open ON invoices (tenant_id, campaign_id)
  WHERE campaign_id IS NOT NULL AND status IN ('confirmed', 'partially_delivered');

-- What customers still owe from a season's sales.
CREATE INDEX ix_obligations__campaign_open ON obligations (tenant_id, campaign_id, direction)
  WHERE campaign_id IS NOT NULL AND status = 'open';
