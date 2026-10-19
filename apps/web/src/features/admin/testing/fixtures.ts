import { HttpResponse, http, type JsonBodyType } from "msw";
import { adminApiUrl } from "../../../test/msw/handlers";
import { TEST_ADMIN_ME } from "../../../test/msw/handlers";
import type { AdminEntry, AdminInvoice, AdminTicket, FeatureFlag, Incident, Partner, Plan, SupportSession, Tenant, TenantDetail, TicketView, UsageRow } from "../api/schemas";

// Admin API answers for the console tests. Each test starts from empty lists (consoleHandlers) and
// overrides the endpoints it exercises.

const ok = (body: JsonBodyType) => () => HttpResponse.json(body);
/** Resolver answering `{data: [...]}`, the shape of the unpaged admin lists. */
export const listBody = (data: readonly unknown[]) => ok({ data: data as JsonBodyType[] });
/** Resolver answering one page of a cursor-paged admin list. */
export const pageBody = (data: readonly unknown[], nextCursor: string | null = null) => ok({ data: data as JsonBodyType[], next_cursor: nextCursor });
/** Resolver answering `GET /incidents` as the API filters it: `status=open` keeps those not resolved. */
export const incidentsBody =
  (data: readonly Incident[]) =>
  ({ request }: { request: Request }) => {
    const status = new URL(request.url).searchParams.get("status");
    const kept = data.filter((i) => (status === null ? true : status === "open" ? i.status !== "resolved" : i.status === status));
    return HttpResponse.json({ data: kept, next_cursor: null });
  };
/** Resolver answering `GET /feature-flags`. */
export const flagsBody = (data: readonly FeatureFlag[], knownKeys: readonly string[] = []) => ok({ data: [...data], known_keys: [...knownKeys] });

export const TENANT_ID = "0193a000-0000-7000-8000-000000000001";
/** How the API names the test store on rows that belong to it. */
export const STORE_SCOPE = { tenant_id: TENANT_ID, tenant_slug: "hoa-phat", tenant_name: "Đại lý Bánh kẹo Tết Hòa Phát" } as const;
export const ADMIN_ID = TEST_ADMIN_ME.admin.id;

/** The admin directory: the signed-in admin, a colleague and one who left. */
export const ME_ENTRY: AdminEntry = { ...TEST_ADMIN_ME.admin, active: true };
export const COLLEAGUE: AdminEntry = { id: "0193a000-0000-7000-8000-0000000000ae", full_name: "Lê Thu Hỗ Trợ", email: "hotro@tally.vn", active: true };
export const FORMER_ADMIN: AdminEntry = { id: "0193a000-0000-7000-8000-0000000000af", full_name: "Phạm Văn Cũ", email: "cu@tally.vn", active: false };

export const PLAN_PRO: Plan = {
  id: "0193a000-0000-7000-8000-0000000000p1",
  code: "pro",
  name: "Gói Chuyên nghiệp",
  kind: "year_round",
  billing_period: "year",
  price: "4800000" as Plan["price"],
  modules: ["pos", "kits", "crm"],
  limits: { active_skus: 5000 },
  terms: {},
  active: true,
};
export const PLAN_SEASON: Plan = {
  id: "0193a000-0000-7000-8000-0000000000p2",
  code: "tet",
  name: "Gói Mùa Tết",
  kind: "seasonal",
  billing_period: "season",
  price: "1500000" as Plan["price"],
  modules: ["pos", "campaign"],
  limits: { active_skus: 1000 },
  terms: { season_months: 3 },
  active: true,
};

export function testTenant(over: Partial<Tenant> = {}): Tenant {
  return {
    id: TENANT_ID,
    slug: "hoa-phat",
    display_name: "Đại lý Bánh kẹo Tết Hòa Phát",
    status: "active",
    is_sandbox: false,
    sandbox_of: null,
    legal_hold: false,
    maintenance: false,
    created_at: "2026-09-01T02:00:00Z",
    subscription_status: "active",
    plan_code: "pro",
    plan_name: "Gói Chuyên nghiệp",
    current_period_end: "2027-09-01",
    usage_date: "2026-10-17",
    active_skus: 420,
    storage_bytes: 52_428_800,
    ...over,
  };
}

export function testTenantDetail(over: Partial<TenantDetail> = {}): TenantDetail {
  return {
    ...testTenant(),
    subscription: {
      subscription: {
        id: "0193a000-0000-7000-8000-0000000000s1",
        plan_id: PLAN_PRO.id,
        status: "active",
        current_period_start: "2026-09-01",
        current_period_end: "2027-09-01",
        trial_ends_on: null,
        grace_ends_on: null,
        read_only_ends_on: null,
        cancel_at_period_end: false,
        version: 2,
      },
      plan: PLAN_PRO,
      entitlements: { status: "active", modules: { pos: true, kits: true, crm: true }, limits: { active_skus: 5000 }, can_post: true, can_export: true },
    },
    usage: [{ tenant_id: TENANT_ID, usage_date: "2026-10-17", active_skus: 420, documents_posted: 37, storage_bytes: 52_428_800 }],
    closure: null,
    ...over,
  };
}

export function testInvoice(over: Partial<AdminInvoice> = {}): AdminInvoice {
  return {
    id: "0193a000-0000-7000-8000-0000000000i1",
    number: "TB-2026-0007",
    subscription_id: "0193a000-0000-7000-8000-0000000000s1",
    plan_id: PLAN_PRO.id,
    purpose: "renewal",
    period_start: "2026-09-01",
    period_end: "2027-09-01",
    amount: "4800000" as AdminInvoice["amount"],
    proration_note: null,
    status: "open",
    provider: null,
    provider_ref: null,
    issued_at: "2026-09-01T02:00:00Z",
    paid_at: null,
    tenant_id: TENANT_ID,
    tenant_slug: "hoa-phat",
    tenant_name: "Đại lý Bánh kẹo Tết Hòa Phát",
    ...over,
  };
}

export function testFlag(over: Partial<FeatureFlag> = {}): FeatureFlag {
  return { key: "ai_qa.beta", tenant_id: null, tenant_slug: null, tenant_name: null, enabled: false, updated_at: "2026-10-10T03:00:00Z", ...over };
}

export function testUsageRow(over: Partial<UsageRow> = {}): UsageRow {
  return { tenant_id: TENANT_ID, tenant_name: "Đại lý Bánh kẹo Tết Hòa Phát", slug: "hoa-phat", usage_date: "2026-10-17", active_skus: 420, documents_posted: 37, storage_bytes: 52_428_800, ...over };
}

export function testIncident(over: Partial<Incident> = {}): Incident {
  return {
    id: "0193a000-0000-7000-8000-0000000000c1",
    title: "Chậm khi ghi sổ hóa đơn",
    status: "investigating",
    impact: "minor",
    started_at: "2026-10-17T01:00:00Z",
    resolved_at: null,
    updates: [{ at: "2026-10-17T01:00:00Z", status: "investigating", message: "Đang kiểm tra máy chủ ghi sổ." }],
    ...over,
  };
}

export function testPartner(over: Partial<Partner> = {}): Partner {
  return { id: "0193a000-0000-7000-8000-0000000000r1", name: "Công ty Tư vấn Minh An", referral_code: "MINHAN", commission_percent: "10", active: true, created_at: "2026-08-01T02:00:00Z", referrals: 1, ...over };
}

export function testTicket(over: Partial<AdminTicket> = {}): AdminTicket {
  return {
    id: "0193a000-0000-7000-8000-0000000000t1",
    number: "HT-0012",
    subject: "Không in được hóa đơn",
    status: "open",
    priority: "normal",
    sla_due_at: "2026-10-18T09:00:00Z",
    assigned_admin_id: null,
    created_at: "2026-10-17T02:00:00Z",
    updated_at: "2026-10-17T02:00:00Z",
    tenant_id: TENANT_ID,
    tenant_slug: "hoa-phat",
    tenant_name: "Đại lý Bánh kẹo Tết Hòa Phát",
    last_message_at: "2026-10-17T02:00:00Z",
    ...over,
  };
}

export function testTicketView(over: Partial<TicketView> = {}): TicketView {
  const { tenant_slug: _slug, tenant_name: _name, last_message_at: _last, ...ticket } = testTicket();
  return {
    ...ticket,
    messages: [{ id: "0193a000-0000-7000-8000-0000000000m1", author_type: "owner", author_id: "0193a000-0000-7000-8000-0000000000u1", body: "Máy in báo lỗi khi in hóa đơn.", attachment_ids: [], created_at: "2026-10-17T02:00:00Z" }],
    ...over,
  };
}

export function testSession(over: Partial<SupportSession> = {}): SupportSession {
  return {
    id: "0193a000-0000-7000-8000-0000000000g1",
    ...STORE_SCOPE,
    admin_id: ADMIN_ID,
    ticket_id: null,
    scope: "read_only",
    modules: ["catalog"],
    state: "active",
    granted_at: "2026-10-17T02:00:00Z",
    expires_at: "2099-01-01T00:00:00Z",
    revoked_at: null,
    ...over,
  };
}

/** Empty answers of every console list and the admin directory, so a test only overrides what it exercises. */
export const consoleHandlers = [
  http.get(adminApiUrl("/admins"), pageBody([ME_ENTRY, COLLEAGUE, FORMER_ADMIN])),
  http.get(adminApiUrl("/tenants"), pageBody([])),
  http.get(adminApiUrl("/usage"), pageBody([])),
  http.get(adminApiUrl("/saas-invoices"), pageBody([])),
  http.get(adminApiUrl("/feature-flags"), flagsBody([])),
  http.get(adminApiUrl("/incidents"), incidentsBody([])),
  http.get(adminApiUrl("/partners"), pageBody([])),
  http.get(adminApiUrl("/support-tickets"), pageBody([])),
  http.get(adminApiUrl("/support-sessions"), listBody([])),
];

/** Holds a handler's answer until the test releases it, so a test can check the screen while a write is in flight. */
export function heldAnswer() {
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { held, release };
}
