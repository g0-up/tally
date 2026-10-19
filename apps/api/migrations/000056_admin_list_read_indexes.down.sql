SET search_path = tally;

DROP INDEX ix_platform_admins__full_name;
DROP INDEX ix_feature_flag_changes__key_created;
DROP INDEX ix_tenant_usage_daily__date;
DROP INDEX ix_support_tickets__sla_due;
DROP INDEX ix_partners__created;
DROP INDEX ix_incidents__started;
DROP INDEX ix_saas_invoices__issued;
DROP INDEX ix_tenants__created;
