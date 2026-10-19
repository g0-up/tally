import { z } from "zod";
import { moneySchema, qtySchema } from "./decimal-schemas";
import type { components } from "./schema";
import type { operations as AdminOperations } from "./schema-admin";

// Paths and response schemas of the platform endpoints the shell itself depends on. Feature
// endpoints live in each feature's api/ folder. Schemas validate only what the shell reads and
// tolerate extra fields, so additive API changes do not break the client.

export const API_BASE = "/api/v1";
export const ADMIN_API_BASE = "/api/admin/v1";

export const PATHS = {
  hostContext: "/host-context",
  me: "/me",
  reauth: "/auth/reauth",
  logout: "/auth/logout",
  tenant: "/tenant",
  sandboxReset: "/tenant/sandbox/reset",
  onboarding: "/onboarding",
  subscription: "/subscription",
  entitlements: "/entitlements",
  unreadCount: "/notifications/unread-count",
  exceptionConfirmations: "/exception-confirmations",
  adminMe: "/me",
} as const;

export const hostKindSchema = z.enum(["root", "tenant", "admin"]);
export type HostKind = z.infer<typeof hostKindSchema>;

export const hostContextSchema = z.object({
  kind: hostKindSchema,
  /** Root host URL ("https://DOMAIN"), for cross-host links. */
  root_url: z.string().optional(),
  admin_url: z.string().optional(),
  /** Fresh CSRF token for this host, also set in its CSRF cookie. */
  csrf_token: z.string().optional(),
});
export type HostContext = z.infer<typeof hostContextSchema>;

export const meSchema = z.object({
  csrf_token: z.string().optional(),
  user: z.object({ id: z.string(), full_name: z.string() }),
  tenant: z
    .object({
      id: z.string(),
      slug: z.string(),
      display_name: z.string(),
      is_sandbox: z.boolean().default(false),
    })
    .optional(),
  /** The server's clock in the store's time zone; today's business date comes from it, not the device. */
  server_time: z.iso.datetime({ offset: true }),
});
export type Me = z.infer<typeof meSchema>;

/** The store profile (GET /tenant), shared by the shell, the settings screen and onboarding. */
export const tenantSchema = z.object({
  id: z.string(),
  slug: z.string(),
  legal_name: z.string(),
  display_name: z.string(),
  tax_code: z.string().nullable(),
  address: z.string().nullable(),
  phone: z.string().nullable(),
  currency: z.literal("VND"),
  timezone: z.string(),
  date_format: z.string(),
  status: z.string(),
  is_sandbox: z.boolean(),
  sandbox_of: z.string().nullable(),
  opening_cutoff_date: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
  version: z.number().int(),
}) satisfies z.ZodType<components["schemas"]["Tenant"]>;
export type Tenant = z.infer<typeof tenantSchema>;

export const ONBOARDING_STEP_KEYS = [
  "store_profile",
  "catalog_setup",
  "opening_stock",
  "opening_debt",
  "opening_cash",
  "first_sale",
  "confirm_opening_balances",
] as const;
export type OnboardingStepKey = (typeof ONBOARDING_STEP_KEYS)[number];

export const onboardingStepKeySchema = z.enum(ONBOARDING_STEP_KEYS);

export const openingTotalsSchema = z.object({
  cutoff: z.string(),
  stock_by_color: z.array(
    z.object({ color_id: z.string().nullable(), color_code: z.string().nullable(), color_name: z.string().nullable(), qty: qtySchema, value: moneySchema }),
  ),
  stock_value: moneySchema,
  receivables: moneySchema,
  payables: moneySchema,
  customer_advances: moneySchema,
  supplier_advances: moneySchema,
  funds: z.array(z.object({ id: z.string(), name: z.string(), kind: z.string(), balance: moneySchema })),
});
export type OpeningTotals = z.infer<typeof openingTotalsSchema>;

export const onboardingStepStatusSchema = z.enum(["pending", "completed", "skipped"]);
export type OnboardingStepStatus = z.infer<typeof onboardingStepStatusSchema>;

export const onboardingStepSchema = z.object({
  key: onboardingStepKeySchema,
  status: onboardingStepStatusSchema,
  skippable: z.boolean(),
  completed_at: z.string().nullable(),
  skipped_at: z.string().nullable(),
  totals: openingTotalsSchema.optional(),
});
export type OnboardingStep = z.infer<typeof onboardingStepSchema>;

/** GET /onboarding: the checklist, its progress, the period lock and the opening totals to confirm. */
export const onboardingSchema = z.object({
  steps: z.array(onboardingStepSchema),
  done: z.number().int(),
  total: z.number().int(),
  complete: z.boolean(),
  locked_through: z.string().nullable(),
  totals: openingTotalsSchema.nullable(),
});
export type Onboarding = z.infer<typeof onboardingSchema>;

export const PLAN_MODULES = ["pos", "kits", "campaign", "crm", "reports_advanced", "ai_qa"] as const;
export type PlanModule = (typeof PLAN_MODULES)[number];

export const planSchema = z.object({
  id: z.string(),
  code: z.string(),
  name: z.string(),
  kind: z.enum(["year_round", "seasonal", "addon", "trial"]),
  billing_period: z.enum(["month", "season", "year", "none"]),
  price: moneySchema,
  modules: z.array(z.string()),
  /** A missing key is unlimited. */
  limits: z.record(z.string(), z.number().int()),
  terms: z.object({
    trial_days: z.number().int(),
    grace_days: z.number().int(),
    read_only_days: z.number().int(),
    support_sla_hours: z.number().int(),
    period_days: z.number().int(),
  }),
  active: z.boolean(),
});
export type Plan = z.infer<typeof planSchema>;

export const SUBSCRIPTION_STATUSES = ["trialing", "active", "grace", "read_only", "closed"] as const;
export type SubscriptionStatus = (typeof SUBSCRIPTION_STATUSES)[number];

export const subscriptionSchema = z.object({
  id: z.string(),
  plan_id: z.string(),
  status: z.enum(SUBSCRIPTION_STATUSES),
  current_period_start: z.string(),
  current_period_end: z.string(),
  trial_ends_on: z.string().nullable(),
  grace_ends_on: z.string().nullable(),
  read_only_ends_on: z.string().nullable(),
  cancel_at_period_end: z.boolean(),
  version: z.number().int(),
});
export type Subscription = z.infer<typeof subscriptionSchema>;

/**
 * GET /entitlements, also embedded in GET /subscription. `modules` and `limits` are null for an
 * unrestricted store (no subscription); `status` is then empty.
 */
export const entitlementsSchema = z.object({
  status: z.union([z.enum(SUBSCRIPTION_STATUSES), z.literal("")]),
  modules: z.record(z.string(), z.boolean()).nullable(),
  limits: z.record(z.string(), z.number().int()).nullable(),
  can_post: z.boolean(),
  can_export: z.boolean(),
});
export type Entitlements = z.infer<typeof entitlementsSchema>;

/**
 * A paid add-on whose period holds the store's business date (the server picks them). `modules`
 * stays a string list like a plan's, so a module the web does not know yet still parses.
 */
export const activeAddonSchema = z.object({
  plan_id: z.string(),
  plan_name: z.string(),
  modules: z.array(z.string()),
  period_start: z.string(),
  period_end: z.string(),
}) satisfies z.ZodType<Omit<components["schemas"]["ActiveAddon"], "modules">>;
export type ActiveAddon = z.infer<typeof activeAddonSchema>;

/**
 * GET /subscription: `subscription` and `plan` are null for a store without a subscription;
 * `addons` lists the add-ons in force, earliest period first, and is empty when there are none.
 */
export const subscriptionViewSchema = z.object({
  subscription: subscriptionSchema.nullable(),
  plan: planSchema.nullable(),
  entitlements: entitlementsSchema,
  addons: z.array(activeAddonSchema),
});
export type SubscriptionView = z.infer<typeof subscriptionViewSchema>;

/** Whether the plan includes a module; an unrestricted store (`modules` null) has every module. */
export function hasModule(entitlements: Entitlements, module: PlanModule): boolean {
  return entitlements.modules === null || entitlements.modules[module] === true;
}

export const unreadCountSchema = z.object({ count: z.number().int().nonnegative() });

export const exceptionConfirmationSchema = z.object({ id: z.string() });

/** GET /me on the admin API: the signed-in platform admin and the CSRF token of the session. */
export const adminMeSchema = z.object({
  admin: z.object({ id: z.string(), full_name: z.string(), email: z.string() }),
  csrf_token: z.string(),
  expires_at: z.string(),
}) satisfies z.ZodType<AdminOperations["adminGetMe"]["responses"][200]["content"]["application/json"]>;
export type AdminMe = z.infer<typeof adminMeSchema>;
