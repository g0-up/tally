import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { isSessionExpired } from "../../../shared/api/api-error";
import { platformKeys } from "../../../shared/api/query-keys";
import { META_BOOT, claimTenant, offlineDb, offlineStorageAvailable, wipeSessionData } from "./offline-db";

// What the shell needs to open the POS after a reload without a network: the host context and the
// session, tenant, subscription, entitlements and onboarding answers, as last seen online. The POS
// saves it while online; main.tsx seeds the query cache from it when the API cannot be reached at
// boot. CSRF tokens are never stored.

/** Session of this device as the POS reads it from GET /me (the shell's schema drops `session`). */
export const deviceSessionKey = () => [...platformKeys.me(), "device-session"] as const;

export const BOOT_SNAPSHOT_KEYS: readonly QueryKey[] = [
  platformKeys.me(),
  platformKeys.tenant(),
  platformKeys.subscription(),
  platformKeys.entitlements(),
  platformKeys.onboarding(),
  deviceSessionKey(),
];

export interface BootSnapshot {
  tenant_id: string;
  saved_at: string;
  host: unknown;
  queries: { key: QueryKey; data: unknown }[];
}

function withoutCsrf(data: unknown): unknown {
  if (typeof data !== "object" || data === null || !("csrf_token" in data)) return data;
  const copy: Record<string, unknown> = { ...(data as Record<string, unknown>) };
  delete copy.csrf_token;
  return copy;
}

/** Saves the shell's answers for an offline start; skipped until every one of them is cached. */
export async function saveBootSnapshot(queryClient: QueryClient, hostKey: QueryKey, tenantId: string, now: Date = new Date()): Promise<boolean> {
  const queries = BOOT_SNAPSHOT_KEYS.map((key) => ({ key, data: queryClient.getQueryData(key) }));
  const host = queryClient.getQueryData(hostKey);
  if (host === undefined || queries.some((q) => q.data === undefined)) return false;
  const value: BootSnapshot = {
    tenant_id: tenantId,
    saved_at: now.toISOString(),
    host: withoutCsrf(host),
    queries: queries.map((q) => ({ key: q.key, data: withoutCsrf(q.data) })),
  };
  const db = await offlineDb();
  await db.put("meta", { key: META_BOOT, value });
  return true;
}

function isBootSnapshot(v: unknown): v is BootSnapshot {
  if (typeof v !== "object" || v === null) return false;
  const s = v as Partial<BootSnapshot>;
  return typeof s.tenant_id === "string" && Array.isArray(s.queries) && s.host !== undefined;
}

export async function loadBootSnapshot(): Promise<BootSnapshot | null> {
  if (!offlineStorageAvailable()) return null;
  const db = await offlineDb();
  const value = (await db.get("meta", META_BOOT))?.value;
  return isBootSnapshot(value) ? value : null;
}

/** Seeds the cache so the guards and the shell render from the snapshot without asking the API. */
export function seedBootSnapshot(queryClient: QueryClient, hostKey: QueryKey, snapshot: BootSnapshot): void {
  queryClient.setQueryData(hostKey, snapshot.host);
  for (const q of snapshot.queries) queryClient.setQueryData(q.key, q.data);
}

const sameKey = (a: QueryKey, b: QueryKey) => a.length === b.length && a.every((x, i) => x === b[i]);

function tenantIdOf(me: unknown): string | null {
  const tenant = typeof me === "object" && me !== null ? (me as { tenant?: unknown }).tenant : undefined;
  const id = typeof tenant === "object" && tenant !== null ? (tenant as { id?: unknown }).id : undefined;
  return typeof id === "string" ? id : null;
}

/**
 * Applies the wipe rules to the session query of the shell: signing in or out (the session answer
 * is dropped) and an ended or revoked session (401) wipe the cached catalog and the boot snapshot;
 * a session of another tenant than the one stored claims the database for it. Returns the
 * unsubscribe function.
 */
export function watchSessionForOfflineData(queryClient: QueryClient, onError: (e: unknown) => void = () => undefined): () => void {
  if (!offlineStorageAvailable()) return () => undefined;
  const me = platformKeys.me();
  return queryClient.getQueryCache().subscribe((event) => {
    if (!sameKey(event.query.queryKey, me)) return;
    if (event.type === "removed") {
      wipeSessionData().catch(onError);
      return;
    }
    if (event.type !== "updated") return;
    if (event.action.type === "error" && isSessionExpired(event.action.error)) {
      wipeSessionData().catch(onError);
      return;
    }
    if (event.action.type === "success") {
      const tenantId = tenantIdOf(event.action.data);
      if (tenantId) claimTenant(tenantId).catch(onError);
    }
  });
}
