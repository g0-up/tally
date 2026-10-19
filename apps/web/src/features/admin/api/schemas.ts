import { z } from "zod";
import { decimalSchema, listOf, moneySchema } from "../../../shared/api/decimal-schemas";
import type { components, operations } from "../../../shared/api/schema-admin";
import { cursorPageSchema } from "../../../shared/hooks/use-cursor-list";

// Response schemas of the admin console endpoints. openapi-admin is the authority: every object
// schema lists all fields of its schema-admin.d.ts type and `satisfies` it, so a spec change that
// adds, drops or retypes a field fails the typecheck.

type S = components["schemas"];
/** 200 JSON body of an admin operation. */
type ResponseOf<K extends keyof operations> = operations[K] extends { responses: { 200: { content: { "application/json": infer B } } } } ? B : never;
/** JSON body of an admin operation. */
export type BodyOf<K extends keyof operations> = operations[K] extends { requestBody: { content: { "application/json": infer B } } }
  ? B
  : operations[K] extends { requestBody?: { content: { "application/json": infer B } } }
    ? B
    : never;

const id = z.string();
const instant = z.string();
const date = z.string();

export const subscriptionStatusSchema = z.enum(["trialing", "active", "grace", "read_only", "closed"]) satisfies z.ZodType<S["SubscriptionStatus"]>;
export type SubscriptionStatus = z.infer<typeof subscriptionStatusSchema>;

export const tenantStatusSchema = z.enum(["active", "read_only", "closed"]) satisfies z.ZodType<S["Tenant"]["status"]>;
export type TenantStatus = z.infer<typeof tenantStatusSchema>;

const countsSchema = z.record(z.string(), z.number());

export const planModuleSchema = z.enum(["pos", "kits", "campaign", "crm", "reports_advanced", "ai_qa"]) satisfies z.ZodType<S["PlanModule"]>;

export const planSchema = z.object({
  id,
  code: z.string(),
  name: z.string(),
  kind: z.enum(["year_round", "seasonal", "addon", "trial"]),
  billing_period: z.enum(["month", "season", "year", "none"]),
  price: moneySchema,
  modules: z.array(planModuleSchema),
  limits: countsSchema,
  terms: countsSchema,
  active: z.boolean(),
}) satisfies z.ZodType<S["Plan"]>;
export type Plan = z.infer<typeof planSchema>;
export const planListSchema = listOf(planSchema) satisfies z.ZodType<ResponseOf<"adminListPlans">>;

export const adminEntrySchema = z.object({ id, full_name: z.string(), email: z.string(), active: z.boolean() }) satisfies z.ZodType<S["AdminEntry"]>;
export type AdminEntry = z.infer<typeof adminEntrySchema>;
export const adminPageSchema = cursorPageSchema(adminEntrySchema) satisfies z.ZodType<ResponseOf<"adminListAdmins">>;
export type AdminPage = z.infer<typeof adminPageSchema>;

export const tenantSchema = z.object({
  id,
  slug: z.string(),
  display_name: z.string(),
  status: tenantStatusSchema,
  is_sandbox: z.boolean(),
  sandbox_of: id.nullable(),
  legal_hold: z.boolean(),
  maintenance: z.boolean(),
  created_at: instant,
  subscription_status: subscriptionStatusSchema.nullable(),
  plan_code: z.string().nullable(),
  plan_name: z.string().nullable(),
  current_period_end: date.nullable(),
  usage_date: date.nullable(),
  active_skus: z.number().int().nullable(),
  storage_bytes: z.number().nullable(),
}) satisfies z.ZodType<S["Tenant"]>;
export type Tenant = z.infer<typeof tenantSchema>;

export const subscriptionSchema = z.object({
  id,
  plan_id: id,
  status: subscriptionStatusSchema,
  current_period_start: date,
  current_period_end: date,
  trial_ends_on: date.nullable(),
  grace_ends_on: date.nullable(),
  read_only_ends_on: date.nullable(),
  cancel_at_period_end: z.boolean(),
  version: z.number().int(),
}) satisfies z.ZodType<S["Subscription"]>;
export type Subscription = z.infer<typeof subscriptionSchema>;

export const usageSchema = z.object({
  tenant_id: id,
  usage_date: date,
  active_skus: z.number().int(),
  documents_posted: z.number().int(),
  storage_bytes: z.number(),
}) satisfies z.ZodType<S["Usage"]>;
export type Usage = z.infer<typeof usageSchema>;

/** A usage row of `GET /usage`, named after its store. */
export const usageRowSchema = usageSchema.extend({ tenant_name: z.string(), slug: z.string() }) satisfies z.ZodType<S["UsageRow"]>;
export type UsageRow = z.infer<typeof usageRowSchema>;

export const closureStatusSchema = z.enum(["requested", "verified", "waiting", "exporting", "export_failed", "completed", "cancelled"]) satisfies z.ZodType<
  S["AccountClosureRequest"]["status"]
>;
export type ClosureStatus = z.infer<typeof closureStatusSchema>;
export const exportStatusSchema = z.enum(["queued", "running", "succeeded", "failed", "expired", "cancelled"]) satisfies z.ZodType<S["AccountClosureExport"]["status"]>;
export type ExportStatus = z.infer<typeof exportStatusSchema>;

export const closureViewSchema = z.object({
  request: z
    .object({
      id,
      status: closureStatusSchema,
      verified_at: instant.nullable(),
      export_job_id: id.nullable(),
      waiting_until: date,
      legal_hold: z.boolean(),
      created_at: instant,
    })
    .nullable() satisfies z.ZodType<S["AccountClosureRequest"] | null>,
  export: z
    .object({
      id,
      status: exportStatusSchema,
      expires_at: instant.nullable(),
      final: z.boolean(),
      failed_attempts: z.number().int(),
    })
    .nullable() satisfies z.ZodType<S["AccountClosureExport"] | null>,
}) satisfies z.ZodType<S["AccountClosureView"]>;
export type ClosureView = z.infer<typeof closureViewSchema>;

export const entitlementsSchema = z.object({
  status: z.string(),
  modules: z.record(z.string(), z.boolean()).nullable(),
  limits: countsSchema.nullable(),
  can_post: z.boolean(),
  can_export: z.boolean(),
}) satisfies z.ZodType<S["Entitlements"]>;

export const tenantDetailSchema = tenantSchema.extend({
  subscription: z.object({
    subscription: subscriptionSchema.nullable(),
    plan: planSchema.nullable(),
    entitlements: entitlementsSchema,
  }) satisfies z.ZodType<S["SubscriptionView"]>,
  usage: z.array(usageSchema),
  closure: closureViewSchema.nullable(),
}) satisfies z.ZodType<S["TenantDetail"]>;
export type TenantDetail = z.infer<typeof tenantDetailSchema>;

export const invoiceStatusSchema = z.enum(["open", "paid", "void"]) satisfies z.ZodType<S["SaasInvoice"]["status"]>;
export type InvoiceStatus = z.infer<typeof invoiceStatusSchema>;

export const invoiceSchema = z.object({
  id,
  number: z.string(),
  subscription_id: id.nullable(),
  plan_id: id.nullable(),
  purpose: z.enum(["new_period", "renewal", "plan_change", "addon"]),
  period_start: date,
  period_end: date,
  amount: moneySchema,
  proration_note: z.string().nullable(),
  status: invoiceStatusSchema,
  provider: z.string().nullable(),
  provider_ref: z.string().nullable(),
  issued_at: instant,
  paid_at: instant.nullable(),
}) satisfies z.ZodType<S["SaasInvoice"]>;
export type Invoice = z.infer<typeof invoiceSchema>;

export const adminInvoiceSchema = invoiceSchema.extend({ tenant_id: id, tenant_slug: z.string(), tenant_name: z.string() }) satisfies z.ZodType<S["AdminSaasInvoice"]>;
export type AdminInvoice = z.infer<typeof adminInvoiceSchema>;

export const featureFlagSchema = z.object({
  key: z.string(),
  tenant_id: id.nullable(),
  tenant_slug: z.string().nullable(),
  tenant_name: z.string().nullable(),
  enabled: z.boolean(),
  updated_at: instant,
}) satisfies z.ZodType<S["FeatureFlag"]>;
export type FeatureFlag = z.infer<typeof featureFlagSchema>;
/** Stored flag values plus every key the server reads, set or not. */
export const featureFlagListSchema = z.object({ data: z.array(featureFlagSchema), known_keys: z.array(z.string()) }) satisfies z.ZodType<ResponseOf<"adminListFeatureFlags">>;
export type FeatureFlagList = z.infer<typeof featureFlagListSchema>;

/** One change of a flag value; `enabled` null is the removal of a store's own value. */
export const featureFlagChangeSchema = z.object({
  id,
  key: z.string(),
  tenant_id: id.nullable(),
  tenant_slug: z.string().nullable(),
  tenant_name: z.string().nullable(),
  enabled: z.boolean().nullable(),
  admin_id: id,
  admin_full_name: z.string(),
  created_at: instant,
}) satisfies z.ZodType<S["FeatureFlagChange"]>;
export type FeatureFlagChange = z.infer<typeof featureFlagChangeSchema>;
export const featureFlagChangeListSchema = listOf(featureFlagChangeSchema) satisfies z.ZodType<ResponseOf<"adminListFeatureFlagChanges">>;

export const incidentStatusSchema = z.enum(["investigating", "identified", "monitoring", "resolved"]) satisfies z.ZodType<S["IncidentStatus"]>;
export type IncidentStatus = z.infer<typeof incidentStatusSchema>;
export const incidentImpactSchema = z.enum(["none", "minor", "major", "critical"]) satisfies z.ZodType<S["IncidentImpact"]>;
export type IncidentImpact = z.infer<typeof incidentImpactSchema>;

export const incidentSchema = z.object({
  id,
  title: z.string(),
  status: incidentStatusSchema,
  impact: incidentImpactSchema,
  started_at: instant,
  resolved_at: instant.nullable(),
  updates: z.array(z.object({ at: instant, status: incidentStatusSchema, message: z.string() })),
}) satisfies z.ZodType<S["Incident"]>;
export type Incident = z.infer<typeof incidentSchema>;

export const partnerSchema = z.object({
  id,
  name: z.string(),
  referral_code: z.string(),
  commission_percent: decimalSchema,
  active: z.boolean(),
  created_at: instant,
  referrals: z.number().int(),
}) satisfies z.ZodType<S["Partner"]>;
export type Partner = z.infer<typeof partnerSchema>;

export const referralSchema = z.object({
  tenant_id: id,
  slug: z.string(),
  display_name: z.string(),
  referred_at: instant,
  subscription_status: subscriptionStatusSchema.nullable(),
  paid_invoices: moneySchema,
  commission: moneySchema,
}) satisfies z.ZodType<S["Referral"]>;
export type Referral = z.infer<typeof referralSchema>;
export const referralListSchema = z.object({ partner: partnerSchema, data: z.array(referralSchema) }) satisfies z.ZodType<ResponseOf<"adminListPartnerReferrals">>;

export const ticketStatusSchema = z.enum(["open", "waiting_customer", "in_progress", "resolved", "closed"]) satisfies z.ZodType<S["SupportTicketStatus"]>;
export type TicketStatus = z.infer<typeof ticketStatusSchema>;
export const ticketPrioritySchema = z.enum(["low", "normal", "high", "urgent"]) satisfies z.ZodType<S["SupportTicket"]["priority"]>;
export type TicketPriority = z.infer<typeof ticketPrioritySchema>;

const ticketBase = z.object({
  id,
  number: z.string(),
  subject: z.string(),
  status: ticketStatusSchema,
  priority: ticketPrioritySchema,
  sla_due_at: instant.nullable(),
  assigned_admin_id: id.nullable(),
  created_at: instant,
  updated_at: instant,
}) satisfies z.ZodType<S["SupportTicket"]>;
export const ticketSchema = ticketBase;
export type Ticket = z.infer<typeof ticketSchema>;

export const adminTicketSchema = ticketBase.extend({ tenant_id: id, tenant_slug: z.string(), tenant_name: z.string(), last_message_at: instant.nullable() }) satisfies z.ZodType<
  S["AdminSupportTicket"]
>;
export type AdminTicket = z.infer<typeof adminTicketSchema>;

export const ticketMessageSchema = z.object({
  id,
  author_type: z.enum(["owner", "platform_admin"]),
  author_id: id,
  body: z.string(),
  attachment_ids: z.array(z.string()),
  created_at: instant,
}) satisfies z.ZodType<S["SupportTicketMessage"]>;
export type TicketMessage = z.infer<typeof ticketMessageSchema>;

export const ticketViewSchema = ticketBase.extend({ tenant_id: id, messages: z.array(ticketMessageSchema) }) satisfies z.ZodType<ResponseOf<"adminGetSupportTicket">>;
export type TicketView = z.infer<typeof ticketViewSchema>;

export const supportModuleSchema = z.enum(["catalog", "partners", "inventory", "procurement", "sales", "debt_cash", "kits", "campaign", "crm", "reports", "settings"]) satisfies z.ZodType<
  S["SupportViewModule"]
>;
export type SupportModule = z.infer<typeof supportModuleSchema>;

export const supportSessionSchema = z.object({
  id,
  tenant_id: id,
  tenant_slug: z.string(),
  tenant_name: z.string(),
  admin_id: id,
  ticket_id: id.nullable(),
  scope: z.literal("read_only"),
  modules: z.array(supportModuleSchema),
  state: z.enum(["active", "expired", "revoked"]),
  granted_at: instant,
  expires_at: instant,
  revoked_at: instant.nullable(),
}) satisfies z.ZodType<S["SupportSession"]>;
export type SupportSession = z.infer<typeof supportSessionSchema>;
export const supportSessionListSchema = listOf(supportSessionSchema) satisfies z.ZodType<ResponseOf<"adminListSupportSessions">>;

export const signedOutSchema = z.object({ csrf_token: z.string().optional() });
