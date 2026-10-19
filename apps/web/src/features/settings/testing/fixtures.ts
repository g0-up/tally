import { HttpResponse, http } from "msw";
import { TENANT_SETTINGS, tenantSettingsHandlers } from "../../pos/offline/testing/handlers";
import { PATHS } from "../../../shared/api/endpoints";
import { TEST_ME, TEST_TENANT, apiUrl } from "../../../test/msw/handlers";
import type { ClosureRequest, ClosureView, FullExportJob, Owner, PeriodLockEvent, PeriodLocks, Session } from "../api/account";
import type { AuditEvent } from "../api/audit";
import type { ActiveAddon, Plan, SubscriptionView, Tenant } from "../../../shared/api/endpoints";
import type { SaasInvoice } from "../api/billing";
import type { SupportSession, SupportTicket, SupportTicketView } from "../api/support";

// API payloads of the settings screens, shaped as OpenAPI returns them (the test tenant's business
// date is 2026-10-12). GET /me, /tenant and /subscription are shared with the shell, so their
// fixtures carry the shell's fields too.

/** Plans and invoices as the API sends them: money as plain decimal strings. */
export type WirePlan = Omit<Plan, "price"> & { price: string };
export type WireInvoice = Omit<SaasInvoice, "amount"> & { amount: string };
export type WireSubscriptionView = Omit<SubscriptionView, "plan"> & { plan: WirePlan | null };

export const OWNER_EMAIL = "an@hoaphat.vn";

export const CURRENT_SESSION: Session = {
  id: "ses-this",
  device_label: "Chrome trên Windows",
  ip: "113.161.4.20",
  created_at: "2026-10-12T01:00:00Z",
  last_seen_at: "2026-10-12T02:00:00Z",
  expires_at: "2026-10-19T01:00:00Z",
  current: true,
};

export const OTHER_SESSION: Session = {
  id: "ses-phone",
  device_label: "Safari trên iPhone",
  ip: "14.231.8.9",
  created_at: "2026-10-10T08:00:00Z",
  last_seen_at: "2026-10-11T09:30:00Z",
  expires_at: "2026-10-17T08:00:00Z",
  current: false,
};

export function testOwner(over: Partial<Owner["user"]> = {}): Owner & { csrf_token: string; server_time: string } {
  return {
    csrf_token: TEST_ME.csrf_token ?? "csrf-test-token",
    server_time: TEST_ME.server_time,
    user: { id: TEST_ME.user.id, email: OWNER_EMAIL, full_name: TEST_ME.user.full_name, status: "active", mfa_enabled: false, last_login_at: "2026-10-12T01:00:00Z", ...over },
    tenant: { id: TEST_TENANT.id, slug: TEST_TENANT.slug, display_name: TEST_TENANT.display_name, is_sandbox: false },
    session: CURRENT_SESSION,
  };
}

export function testStoreProfile(over: Partial<Tenant> = {}): Tenant {
  return {
    ...TEST_TENANT,
    legal_name: "Hộ kinh doanh Hòa Phát",
    tax_code: "0312345678",
    timezone: "Asia/Ho_Chi_Minh",
    ...over,
  };
}

export const LOCK_EVENT: PeriodLockEvent = {
  id: "pl-1",
  action: "lock",
  locked_through: "2026-09-30",
  reason: null,
  actor_id: TEST_ME.user.id,
  created_at: "2026-10-01T03:00:00Z",
};

export function testPeriodLocks(over: Partial<PeriodLocks> = {}): PeriodLocks {
  return { locked_through: "2026-09-30", version: 1, data: [LOCK_EVENT], next_cursor: null, ...over };
}

export const NO_CLOSURE: ClosureView = { request: null, export: null };

export function testClosureRequest(over: Partial<ClosureRequest> = {}): ClosureRequest {
  return {
    id: "acr-1",
    status: "requested",
    verified_at: null,
    export_job_id: null,
    waiting_until: "2026-11-11",
    legal_hold: false,
    created_at: "2026-10-12T02:00:00Z",
    ...over,
  };
}

export function testExportJob(over: Partial<FullExportJob> = {}): FullExportJob {
  return {
    id: "exp-1",
    kind: "full_data_export",
    status: "queued",
    error: null,
    created_at: "2026-10-12T02:00:00Z",
    finished_at: null,
    expires_at: null,
    download_path: null,
    ...over,
  };
}

/** Every endpoint /settings/account reads, answering a store with one lock and no closure. */
export function accountHandlers(
  opts: {
    owner?: ReturnType<typeof testOwner>;
    sessions?: Session[];
    profile?: ReturnType<typeof testStoreProfile>;
    locks?: PeriodLocks;
    closure?: ClosureView;
  } = {},
) {
  const profile = opts.profile ?? testStoreProfile();
  return [
    http.get(apiUrl(PATHS.me), () => HttpResponse.json(opts.owner ?? testOwner())),
    http.get(apiUrl("/sessions"), () => HttpResponse.json({ data: opts.sessions ?? [CURRENT_SESSION, OTHER_SESSION] })),
    http.get(apiUrl(PATHS.tenant), () => HttpResponse.json(profile, { headers: { ETag: `"${profile.version}"` } })),
    http.get(apiUrl("/period-locks"), () => HttpResponse.json(opts.locks ?? testPeriodLocks())),
    http.get(apiUrl("/account-closure"), () => HttpResponse.json(opts.closure ?? NO_CLOSURE)),
    ...tenantSettingsHandlers({ ...TENANT_SETTINGS }).handlers,
  ];
}

const PLAN_TERMS = { trial_days: 0, grace_days: 7, read_only_days: 30, support_sla_hours: 24, period_days: 30 };

export const STORE_PLAN: WirePlan = {
  id: "plan-store",
  code: "store",
  name: "Cửa hàng",
  kind: "year_round",
  billing_period: "month",
  price: "199000",
  modules: ["pos"],
  limits: { skus: 2000, storage_mb: 1024 },
  terms: PLAN_TERMS,
  active: true,
};

export const SEASON_PLAN: WirePlan = {
  id: "plan-tet",
  code: "tet_season",
  name: "Mùa Tết",
  kind: "seasonal",
  billing_period: "season",
  price: "990000",
  modules: ["pos", "kits", "campaign", "crm"],
  limits: {},
  terms: { ...PLAN_TERMS, period_days: 90 },
  active: true,
};

export const TRIAL_PLAN: WirePlan = { ...STORE_PLAN, id: "plan-trial", code: "trial", name: "Dùng thử", kind: "trial", billing_period: "none", price: "0" };

export const ADDON_PLAN: WirePlan = {
  ...STORE_PLAN,
  id: "plan-ai",
  code: "ai_qa",
  name: "Hỏi đáp AI",
  kind: "addon",
  price: "49000",
  modules: ["ai_qa"],
  limits: {},
};

/** ADDON_PLAN in force for the period its purchase on 12/10 opened. */
export function testActiveAddon(over: Partial<ActiveAddon> = {}): ActiveAddon {
  return { plan_id: ADDON_PLAN.id, plan_name: ADDON_PLAN.name, modules: ADDON_PLAN.modules, period_start: "2026-10-12", period_end: "2026-11-11", ...over };
}

export function testSubscriptionView(over: Partial<WireSubscriptionView> = {}): WireSubscriptionView {
  return {
    subscription: {
      id: "sub-1",
      plan_id: STORE_PLAN.id,
      status: "active",
      current_period_start: "2026-10-01",
      current_period_end: "2026-10-31",
      trial_ends_on: null,
      grace_ends_on: null,
      read_only_ends_on: null,
      cancel_at_period_end: false,
      version: 2,
    },
    plan: STORE_PLAN,
    entitlements: { status: "active", modules: { pos: true, kits: false }, limits: STORE_PLAN.limits, can_post: true, can_export: true },
    addons: [],
    ...over,
  };
}

export function testInvoice(over: Partial<WireInvoice> = {}): WireInvoice {
  return {
    id: "inv-1",
    number: "HD-2026-0007",
    plan_id: SEASON_PLAN.id,
    purpose: "plan_change",
    period_start: "2026-10-12",
    period_end: "2027-01-09",
    amount: "850000",
    proration_note: null,
    status: "open",
    issued_at: "2026-10-12T02:00:00Z",
    paid_at: null,
    ...over,
  };
}

export function testTicket(over: Partial<SupportTicket> = {}): SupportTicket {
  return {
    id: "tk-1",
    number: "HT-0012",
    subject: "Không in được hóa đơn",
    status: "in_progress",
    priority: "normal",
    sla_due_at: "2026-10-13T02:00:00Z",
    assigned_admin_id: "adm-1",
    created_at: "2026-10-11T02:00:00Z",
    updated_at: "2026-10-12T01:00:00Z",
    ...over,
  };
}

export function testTicketView(over: Partial<SupportTicketView> = {}): SupportTicketView {
  return {
    ...testTicket(),
    messages: [
      { id: "m-1", author_type: "owner", body: "Bấm In thì không ra gì.", created_at: "2026-10-11T02:00:00Z" },
      { id: "m-2", author_type: "platform_admin", body: "Anh gửi giúp tên máy in.", created_at: "2026-10-12T01:00:00Z" },
    ],
    ...over,
  };
}

export function testSupportSession(over: Partial<SupportSession> = {}): SupportSession {
  return {
    id: "sup-1",
    ticket_id: "tk-1",
    scope: "read_only",
    modules: ["sales"],
    state: "active",
    granted_at: "2026-10-12T02:00:00Z",
    expires_at: "2026-10-12T04:00:00Z",
    revoked_at: null,
    ...over,
  };
}

/** Every endpoint /settings/subscription reads, answering a store on the base plan. */
export function subscriptionHandlers(
  opts: { view?: WireSubscriptionView; invoices?: WireInvoice[]; tickets?: SupportTicket[]; sessions?: SupportSession[] } = {},
) {
  return [
    http.get(apiUrl(PATHS.me), () => HttpResponse.json(testOwner())),
    http.get(apiUrl("/plans"), () => HttpResponse.json({ data: [STORE_PLAN, SEASON_PLAN, TRIAL_PLAN, ADDON_PLAN] })),
    http.get(apiUrl(PATHS.subscription), () => HttpResponse.json(opts.view ?? testSubscriptionView())),
    http.get(apiUrl("/saas-invoices"), () => HttpResponse.json({ data: opts.invoices ?? [] })),
    http.get(apiUrl("/support-tickets"), () => HttpResponse.json({ data: opts.tickets ?? [testTicket()] })),
    http.get(apiUrl("/support-sessions"), () => HttpResponse.json({ data: opts.sessions ?? [] })),
  ];
}

export function testAuditEvent(over: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id: "ae-1",
    actor_type: "owner",
    actor_id: TEST_ME.user.id,
    action: "period.lock",
    entity_type: "period_lock",
    entity_id: null,
    doc_no: null,
    source: "web",
    ip: "113.161.4.20",
    created_at: "2026-10-12T02:15:00Z",
    ...over,
  };
}
