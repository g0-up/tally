import { queryOptions, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import { isSessionExpired } from "../../../shared/api/api-error";
import { PATHS } from "../../../shared/api/endpoints";
import type { HttpClient } from "../../../shared/api/http";
import type { components } from "../../../shared/api/schema";
import { cursorPageSchema } from "../../../shared/hooks/use-cursor-list";
import type { SalesLineInput } from "../../sales/components/line-items/draft";
import { deviceSessionKey } from "./boot-snapshot";

// Read side of the offline POS. What lives on the device (catalog, outbox, unanswered checkouts) is read from IndexedDB
// under "pos-offline", with networkMode "always" so it is read with no network at all; the
// server's exception queue sits under "pos" so every posting refreshes it with the screen.

export const offlineKeys = {
  all: ["pos-offline"] as const,
  bundle: (tenantId: string, sessionId: string) => ["pos-offline", "bundle", tenantId, sessionId] as const,
  outbox: (tenantId: string) => ["pos-offline", "outbox", tenantId] as const,
  intents: (tenantId: string) => ["pos-offline", "intents", tenantId] as const,
  foreign: (tenantId: string) => ["pos-offline", "foreign", tenantId] as const,
  exceptions: () => ["pos", "sync-exceptions", "open"] as const,
};

const deviceSessionSchema = z.object({
  csrf_token: z.string().optional(),
  tenant: z.object({ id: z.string(), is_sandbox: z.boolean().default(false) }),
  session: z.object({ id: z.string() }),
});

/** Tenant and login session of this device; the offline catalog and outbox are scoped by them. */
export interface DeviceSession {
  tenant: { id: string; is_sandbox: boolean };
  session: { id: string };
}

/** GET /me read for its `session` (the shell's session query drops it). */
export function deviceSessionQuery(client: HttpClient) {
  return queryOptions({
    queryKey: deviceSessionKey(),
    queryFn: async ({ signal }): Promise<DeviceSession> => {
      const me = await client.get(PATHS.me, { schema: deviceSessionSchema, signal, silent: true });
      if (me.csrf_token) client.setCsrfToken(me.csrf_token);
      return { tenant: me.tenant, session: me.session };
    },
    staleTime: 60_000,
    retry: (failures, error) => !isSessionExpired(error) && failures < 1,
  });
}

export function useDeviceSession() {
  const client = useApiClient();
  return useQuery(deviceSessionQuery(client));
}

export const syncExceptionSchema = z.object({
  id: z.string(),
  offline_client_id: z.string(),
  device_session_id: z.string().nullable(),
  payload: z.unknown(),
  error_code: z.string(),
  error_detail: z.string().nullable(),
  status: z.enum(["open", "resolved", "discarded"]),
  created_at: z.string(),
});
export type SyncException = z.infer<typeof syncExceptionSchema>;

const EXCEPTION_PAGE = 200;
/** A queue longer than this many pages is shown as far as it was read. */
const EXCEPTION_MAX_PAGES = 20;

/** Every open exception of the store (all devices), oldest page first; `complete` once fully read. */
export async function fetchOpenSyncExceptions(client: HttpClient, signal: AbortSignal = new AbortController().signal): Promise<{ items: SyncException[]; complete: boolean }> {
  const schema = cursorPageSchema(syncExceptionSchema);
  const items: SyncException[] = [];
  let cursor: string | null = null;
  for (let page = 0; page < EXCEPTION_MAX_PAGES; page++) {
    const r: z.infer<typeof schema> = await client.get("/pos/sync-exceptions", { query: { status: "open", limit: EXCEPTION_PAGE, cursor }, schema, signal, silent: true });
    items.push(...r.data);
    cursor = r.next_cursor;
    if (cursor === null) return { items, complete: true };
  }
  return { items, complete: false };
}

/** The open exception queue; a cashier without access simply sees none. */
export function useOpenSyncExceptions(enabled: boolean) {
  const client = useApiClient();
  return useQuery({
    queryKey: offlineKeys.exceptions(),
    queryFn: ({ signal }) => fetchOpenSyncExceptions(client, signal),
    enabled,
    staleTime: 30_000,
    retry: false,
    meta: { silent: true },
  });
}

const parkedSaleSchema = z.object({
  created_at: z.string().optional(),
  customer_party_id: z.string().optional(),
  lines: z.array(z.looseObject({ qty: z.string() })).min(1),
  order_discount_amount: z.string().optional(),
  promotion_codes: z.array(z.string()).optional(),
  payments: z.array(z.object({ payment_method_id: z.string(), amount: z.string() })).optional(),
  cash_tendered_amount: z.string().optional(),
});

export type ResolveBody = Omit<components["schemas"]["PosResolveRequest"], "exception_confirmation_ids">;

/**
 * The sale as the device sent it, read back from an exception: when it was made and the body
 * "Ghi sổ lại" sends (the same lines, customer and payments). Null when it cannot be read.
 */
export function parkedSale(payload: unknown): { createdAt: string | null; body: ResolveBody } | null {
  const r = parkedSaleSchema.safeParse(payload);
  if (!r.success) return null;
  const d = r.data;
  const body: ResolveBody = { lines: d.lines as SalesLineInput[] };
  if (d.customer_party_id !== undefined) body.customer_party_id = d.customer_party_id;
  if (d.order_discount_amount !== undefined) body.order_discount_amount = d.order_discount_amount;
  if (d.promotion_codes !== undefined) body.promotion_codes = d.promotion_codes;
  if (d.payments !== undefined) body.payments = d.payments;
  if (d.cash_tendered_amount !== undefined) body.cash_tendered_amount = d.cash_tendered_amount;
  return { createdAt: d.created_at ?? null, body };
}
