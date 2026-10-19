SET search_path = tally;

-- Pages of the admin console lists, in the order each list answers.
CREATE INDEX ix_tenants__created ON tenants (created_at DESC, id DESC);
CREATE INDEX ix_saas_invoices__issued ON saas_invoices (issued_at DESC, id DESC);
CREATE INDEX ix_incidents__started ON incidents (started_at DESC, id DESC);
CREATE INDEX ix_partners__created ON partners (created_at DESC, id DESC);
CREATE INDEX ix_support_tickets__sla_due ON support_tickets (sla_due_at NULLS LAST, created_at, id);
CREATE INDEX ix_tenant_usage_daily__date ON tenant_usage_daily (usage_date, tenant_id);
CREATE INDEX ix_feature_flag_changes__key_created ON feature_flag_changes (key, created_at DESC, id DESC);
CREATE INDEX ix_platform_admins__full_name ON platform_admins (full_name, id);
