import { HttpResponse, http, type JsonBodyType } from "msw";
import type { z } from "zod";
import {
  ADMIN_API_BASE,
  API_BASE,
  ONBOARDING_STEP_KEYS,
  PATHS,
  type AdminMe,
  type Me,
  type OnboardingStepKey,
  type OnboardingStepStatus,
  type Tenant,
  type entitlementsSchema,
  type onboardingSchema,
  type subscriptionViewSchema,
} from "../../shared/api/endpoints";

// Request handlers for the platform endpoints every screen depends on. Tests start from a signed-in
// store with onboarding done and every module in the plan, and override single endpoints with
// `server.use(...)` (or the helpers below) to exercise the other branches.

export const apiUrl = (path: string) => API_BASE + path;
export const adminApiUrl = (path: string) => ADMIN_API_BASE + path;

export const TEST_ME: Me = {
  csrf_token: "csrf-test-token",
  user: { id: "01J0000000000000000000USER", full_name: "Nguyễn Văn An" },
  tenant: { id: "01J000000000000000000TENANT", slug: "demo", display_name: "Đại lý Bánh kẹo Tết Hòa Phát", is_sandbox: false },
  // The test store's business date is 12/10/2026, whatever the machine running the tests says.
  server_time: "2026-10-12T09:00:00+07:00",
};

export const TEST_TENANT: Tenant = {
  id: "01J000000000000000000TENANT",
  slug: "demo",
  legal_name: "Công ty TNHH Bánh kẹo Hòa Phát",
  display_name: "Đại lý Bánh kẹo Tết Hòa Phát",
  tax_code: "0101234567",
  address: "12 Hàng Đường, Hà Nội",
  phone: "0901234567",
  currency: "VND",
  timezone: "Asia/Saigon",
  date_format: "dd/MM/yyyy",
  status: "active",
  is_sandbox: false,
  sandbox_of: null,
  opening_cutoff_date: "2026-09-30",
  created_at: "2026-01-05T02:00:00Z",
  updated_at: "2026-10-01T02:00:00Z",
  version: 3,
};

/** JSON bodies as the API sends them (money as decimal strings). */
export type OnboardingBody = z.input<typeof onboardingSchema>;
export type SubscriptionViewBody = z.input<typeof subscriptionViewSchema>;
export type EntitlementsBody = z.input<typeof entitlementsSchema>;

/** Every module of the plan enabled. */
export const TEST_ENTITLEMENTS: EntitlementsBody = {
  status: "active",
  modules: { pos: true, kits: true, campaign: true, crm: true, reports_advanced: true, ai_qa: false },
  limits: { skus: 5000 },
  can_post: true,
  can_export: true,
};

export const TEST_SUBSCRIPTION: SubscriptionViewBody = {
  subscription: {
    id: "01J00000000000000000000SUB1",
    plan_id: "01J00000000000000000000PLN1",
    status: "active",
    current_period_start: "2026-01-01",
    current_period_end: "2026-12-31",
    trial_ends_on: null,
    grace_ends_on: null,
    read_only_ends_on: null,
    cancel_at_period_end: false,
    version: 1,
  },
  plan: {
    id: "01J00000000000000000000PLN1",
    code: "store",
    name: "Cửa hàng",
    kind: "year_round",
    billing_period: "year",
    price: "4800000",
    modules: ["pos", "kits", "campaign", "crm", "reports_advanced"],
    limits: { skus: 5000 },
    terms: { trial_days: 14, grace_days: 7, read_only_days: 30, support_sla_hours: 24, period_days: 365 },
    active: true,
  },
  entitlements: TEST_ENTITLEMENTS,
  addons: [],
};

/** The checklist with every step settled, the given ones overridden. */
export function testOnboarding(statuses: Partial<Record<OnboardingStepKey, OnboardingStepStatus>> = {}): OnboardingBody {
  const steps = ONBOARDING_STEP_KEYS.map((key) => {
    const skippable = key === "catalog_setup" || key === "first_sale";
    const status = statuses[key] ?? (key === "first_sale" ? "skipped" : "completed");
    return {
      key,
      status,
      skippable,
      completed_at: status === "completed" ? "2026-10-12T02:00:00Z" : null,
      skipped_at: status === "skipped" ? "2026-10-12T02:00:00Z" : null,
    };
  });
  const done = steps.filter((s) => s.status !== "pending").length;
  return { steps, done, total: steps.length, complete: done === steps.length, locked_through: null, totals: null };
}

export const TEST_ONBOARDING_DONE: OnboardingBody = testOnboarding();

export const TEST_ADMIN_ME: AdminMe = {
  admin: { id: "0193a000-0000-7000-8000-0000000000ad", full_name: "Trần Quản Trị", email: "quantri@tally.vn" },
  csrf_token: "admin-csrf-test-token",
  expires_at: "2026-10-18T12:00:00Z",
};

/** Error envelope of the API. */
export function apiError(status: number, code: string, extra: { message?: string; fields?: Record<string, string>; exception_kind?: string } = {}) {
  return HttpResponse.json({ error: { code, message: extra.message ?? code, ...extra } }, { status });
}

const ok = (body: JsonBodyType) => () => HttpResponse.json(body);

export const platformHandlers = [
  http.get(apiUrl(PATHS.hostContext), ok({ kind: "tenant", root_url: "http://localhost:5173", csrf_token: "csrf-test-token" })),
  http.get(apiUrl(PATHS.me), ok(TEST_ME)),
  http.get(apiUrl(PATHS.tenant), ok(TEST_TENANT)),
  http.get(apiUrl(PATHS.subscription), ok(TEST_SUBSCRIPTION)),
  http.get(apiUrl(PATHS.entitlements), ok(TEST_ENTITLEMENTS)),
  http.get(apiUrl(PATHS.onboarding), ok(TEST_ONBOARDING_DONE)),
  http.get(apiUrl(PATHS.unreadCount), ok({ count: 3 })),
  http.get(adminApiUrl(PATHS.adminMe), () => HttpResponse.json(TEST_ADMIN_ME, { headers: { "Cache-Control": "no-store" } })),
];

// Identity endpoints of the auth screens, answering their success case. Store and root hosts share
// /api/v1; the admin sign-in sits under /api/admin/v1.
export const AUTH_TEST = {
  storeUrl: "http://cua-hang-moi.localhost:5173",
  signedInCsrf: "csrf-signed-in",
  signedOutCsrf: "csrf-signed-out",
  adminCsrf: "admin-csrf-signed-in",
  challenge: "mfa-challenge-1",
  adminChallenge: "admin-challenge-1",
  /** Store addresses the slug check reports as taken, with the suggestion it offers. */
  takenSlug: "hoa-phat",
  takenSuggestion: "hoa-phat-2",
  reservedSlug: "admin",
} as const;

const accepted = () => new HttpResponse(null, { status: 202 });

export const authHandlers = [
  http.post(apiUrl("/auth/login"), ok({ status: "signed_in", csrf_token: AUTH_TEST.signedInCsrf, expires_at: "2026-10-18T00:00:00Z" })),
  http.post(apiUrl("/auth/mfa/totp/verify"), ok({ status: "signed_in", csrf_token: AUTH_TEST.signedInCsrf })),
  http.post(apiUrl("/auth/logout"), ok({ csrf_token: AUTH_TEST.signedOutCsrf })),
  http.post(apiUrl("/auth/verify"), () => new HttpResponse(null, { status: 204 })),
  http.post(apiUrl("/auth/password-reset"), accepted),
  http.post(apiUrl("/auth/password-reset/confirm"), ok({ csrf_token: AUTH_TEST.signedOutCsrf })),
  http.post(apiUrl("/auth/signup"), () => HttpResponse.json({ store_url: AUTH_TEST.storeUrl }, { status: 202 })),
  http.post(apiUrl("/store-lookup"), accepted),
  http.get(apiUrl("/signup/slug-check"), ({ request }) => {
    const slug = new URL(request.url).searchParams.get("slug") ?? "";
    if (slug === AUTH_TEST.takenSlug) return HttpResponse.json({ available: false, reason: "taken", suggestion: AUTH_TEST.takenSuggestion });
    if (slug === AUTH_TEST.reservedSlug) return HttpResponse.json({ available: false, reason: "reserved", suggestion: null });
    return HttpResponse.json({ available: true, reason: null, suggestion: null });
  }),
  http.post(adminApiUrl("/auth/login"), ok({ status: "mfa_required", challenge: AUTH_TEST.adminChallenge })),
  http.post(adminApiUrl("/auth/totp"), ok({ status: "signed_in", csrf_token: AUTH_TEST.adminCsrf })),
];

/** Handlers shared by every test; per-test overrides go through `server.use`. */
export const handlers = [
  ...platformHandlers,
  ...authHandlers,
  // No export started yet: the report screens' look for one to pick up again finds none.
  http.get(apiUrl("/export-jobs"), ok({ data: [], next_cursor: null })),
];

/** Throttled request: 429 with the wait in Retry-After (seconds). */
export function rateLimitedError(code: string, retryAfterSeconds: number) {
  return HttpResponse.json({ error: { code, message: code } }, { status: 429, headers: { "Retry-After": String(retryAfterSeconds) } });
}

/** The visitor has no session on this host. */
export const signedOut = [http.get(apiUrl(PATHS.me), () => apiError(401, "unauthenticated")), http.get(adminApiUrl(PATHS.adminMe), () => apiError(401, "unauthenticated"))];

export function withOnboarding(onboarding: OnboardingBody) {
  return http.get(apiUrl(PATHS.onboarding), ok(onboarding));
}

export function withEntitlements(entitlements: Partial<EntitlementsBody>) {
  return http.get(apiUrl(PATHS.entitlements), ok({ ...TEST_ENTITLEMENTS, ...entitlements }));
}

export function withSubscription(subscription: SubscriptionViewBody) {
  return http.get(apiUrl(PATHS.subscription), ok(subscription));
}

export function withTenant(tenant: Tenant) {
  return http.get(apiUrl(PATHS.tenant), ok(tenant));
}
