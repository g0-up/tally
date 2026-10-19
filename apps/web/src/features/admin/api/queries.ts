import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { consoleKeys } from "./keys";
import {
  adminInvoiceSchema,
  adminPageSchema,
  adminTicketSchema,
  featureFlagChangeListSchema,
  featureFlagListSchema,
  incidentSchema,
  partnerSchema,
  planListSchema,
  referralListSchema,
  supportSessionListSchema,
  tenantDetailSchema,
  tenantSchema,
  ticketViewSchema,
  usageRowSchema,
  type AdminEntry,
  type AdminPage,
  type IncidentStatus,
  type InvoiceStatus,
  type SupportModule,
  type TenantStatus,
  type TicketStatus,
} from "./schemas";
import { supportViewUrl } from "../lib/support-view";

// Read side of the admin console. Tenants, invoices, incidents, partners, tickets and usage are
// cursor-paged (50 rows a page, newest first, "Xem thêm" for the next); plans, flags, referrals and
// support sessions come whole.

/** "" means no filter. */
export type Filter<T extends string> = T | "";

/** Page size of the admin directory walk; the API maximum. */
const ADMIN_PAGE = 200;

export function usePlans() {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.plans(),
    queryFn: async ({ signal }) => (await client.get("/plans", { schema: planListSchema, signal })).data,
  });
}

/**
 * Every platform admin, inactive ones included (`active` false), for naming assignees and picking
 * one. The directory is small, so every page is read.
 */
export function useAdmins() {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.admins(),
    queryFn: async ({ signal }) => {
      const all: AdminEntry[] = [];
      let cursor: string | null = null;
      do {
        const page: AdminPage = await client.get("/admins", { query: { limit: ADMIN_PAGE, cursor }, schema: adminPageSchema, signal });
        all.push(...page.data);
        cursor = page.next_cursor;
      } while (cursor !== null);
      return all;
    },
  });
}

/** Longest `q` GET /tenants accepts. */
export const TENANT_SEARCH_MAX_LENGTH = 100;

/** `GET /tenants?q&status`: q matches the slug or the name. */
export function useTenants(q: string, status: Filter<TenantStatus>) {
  return useCursorList({ queryKey: consoleKeys.tenantList(), path: "/tenants", item: tenantSchema, params: { q, status } });
}

export function useTenant(id: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.tenant(id),
    queryFn: ({ signal }) => client.get(`/tenants/${id}`, { schema: tenantDetailSchema, signal }),
  });
}

/** `GET /usage?from&to`: daily counters of every store in the range (at most 366 days), named after the store. */
export function useUsage(from: string, to: string, enabled: boolean) {
  return useCursorList({ queryKey: consoleKeys.usage(), path: "/usage", item: usageRowSchema, params: { from, to }, enabled });
}

export function useInvoices(status: Filter<InvoiceStatus>, tenantId: string) {
  return useCursorList({ queryKey: consoleKeys.invoiceList(), path: "/saas-invoices", item: adminInvoiceSchema, params: { status, tenant_id: tenantId } });
}

/** Every stored flag value (the global ones have tenant_id null) and every key the server reads. */
export function useFeatureFlags() {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.flags(),
    queryFn: ({ signal }) => client.get("/feature-flags", { schema: featureFlagListSchema, signal }),
  });
}

export function useFeatureFlagChanges(key: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.flagChanges(key ?? ""),
    queryFn: async ({ signal }) => (await client.get(`/feature-flags/${encodeURIComponent(key ?? "")}/changes`, { schema: featureFlagChangeListSchema, signal })).data,
    enabled: key !== null,
  });
}

/** `GET /incidents?status`: one status, `open` for every one not resolved yet, "" for all. */
export function useIncidents(status: Filter<IncidentStatus | "open"> = "") {
  return useCursorList({ queryKey: consoleKeys.incidents(), path: "/incidents", item: incidentSchema, params: { status } });
}

export function usePartners() {
  return useCursorList({ queryKey: consoleKeys.partners(), path: "/partners", item: partnerSchema });
}

export function useReferrals(partnerId: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.referrals(partnerId ?? ""),
    queryFn: async ({ signal }) => (await client.get(`/partners/${partnerId}/referrals`, { schema: referralListSchema, signal })).data,
    enabled: partnerId !== null,
  });
}

export function useTickets(status: Filter<TicketStatus>, tenantId: string) {
  return useCursorList({ queryKey: consoleKeys.ticketList(), path: "/support-tickets", item: adminTicketSchema, params: { status, tenant_id: tenantId } });
}

export function useTicket(id: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.ticket(id),
    queryFn: ({ signal }) => client.get(`/support-tickets/${id}`, { schema: ticketViewSchema, signal }),
  });
}

/** The signed-in admin's own support sessions (granted by store owners). */
export function useSupportSessions() {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.sessions(),
    queryFn: async ({ signal }) => (await client.get("/support-sessions", { schema: supportSessionListSchema, signal })).data,
  });
}

/**
 * `GET /tenants/{id}/support-view/{module}/{path}`: a store API read through the admin's active
 * support session. The answer is the store API's JSON, shown as is; only asked while `enabled`.
 */
export function useSupportView(tenantId: string, module: SupportModule, path: string, enabled: boolean) {
  const client = useApiClient();
  return useQuery({
    queryKey: consoleKeys.supportView(tenantId, module, path),
    queryFn: ({ signal }) => {
      const [route = "", search = ""] = path.split("?", 2);
      const query = Object.fromEntries(new URLSearchParams(search));
      return client.get(supportViewUrl(tenantId, module, route), { query, schema: z.unknown(), signal, silent: true });
    },
    enabled,
    retry: false,
  });
}
