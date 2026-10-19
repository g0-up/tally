import { z } from "zod";
import { isApiError } from "../../../shared/api/api-error";
import type { HttpClient } from "../../../shared/api/http";
import { colorSchema, listOf, moneySchema, posBootstrapSchema } from "../../sales/schemas";
import { bundleScope, offlineDb, type OfflineBundle } from "./offline-db";

// The catalog a device sells from while offline: the POS bootstrap (tiles with retail price and
// stock, payment methods, the store's offline settings) and the product colors, saved per tenant
// and login session. It is downloaded while online, on opening the POS and every 30 minutes, and
// carries how long it may be used: the offline window the server granted this session, never more
// than the store's "Số giờ tối đa không đồng bộ" after the download.

export const BUNDLE_REFRESH_MS = 30 * 60_000;

const HOUR_MS = 3_600_000;

export const posSettingsSchema = z.object({
  prices_include_tax: z.boolean(),
  offline_pos_enabled: z.boolean(),
  offline_pos_max_amount: moneySchema,
  offline_pos_max_hours: z.number().int(),
});

const offlineBootstrapSchema = posBootstrapSchema.extend({ settings: posSettingsSchema });
const offlineSessionSchema = z.object({ offline_until: z.string() });

/** Tenant and login session the catalog is downloaded for. */
export interface BundleOwner {
  tenantId: string;
  sessionId: string;
}

/** The offline window was refused because the store turned offline selling off. */
function offlineDisabled(e: unknown): boolean {
  return isApiError(e) && e.code === "offline_pos_disabled";
}

/**
 * Downloads the catalog and opens this session's offline window. Null when the store does not
 * sell offline (the caller then drops any saved catalog); network and session errors are thrown.
 */
export async function fetchOfflineBundle(client: HttpClient, owner: BundleOwner, now: Date = new Date()): Promise<OfflineBundle | null> {
  const boot = await client.request("GET", "/pos/bootstrap", { schema: offlineBootstrapSchema, silent: true });
  const settings = boot.data.settings;
  if (!settings.offline_pos_enabled) return null;
  let until: string;
  try {
    until = (await client.post("/pos/offline-session", undefined, { schema: offlineSessionSchema, silent: true })).offline_until;
  } catch (e) {
    if (offlineDisabled(e)) return null;
    throw e;
  }
  const colors = await client.get("/colors", { schema: listOf(colorSchema), silent: true });
  const cap = now.getTime() + settings.offline_pos_max_hours * HOUR_MS;
  const granted = Date.parse(until);
  const expires = Number.isNaN(granted) ? cap : Math.min(granted, cap);
  return {
    scope: bundleScope(owner.tenantId, owner.sessionId),
    tenant_id: owner.tenantId,
    device_session_id: owner.sessionId,
    version: boot.etag ?? boot.data.priced_at,
    cached_at: boot.data.priced_at,
    fetched_at: now.toISOString(),
    expires_at: new Date(expires).toISOString(),
    settings,
    payment_methods: boot.data.payment_methods,
    tiles: boot.data.tiles,
    colors: colors.data,
  };
}

export async function loadBundle(owner: BundleOwner): Promise<OfflineBundle | null> {
  const db = await offlineDb();
  return (await db.get("bundles", bundleScope(owner.tenantId, owner.sessionId))) ?? null;
}

export async function saveBundle(bundle: OfflineBundle): Promise<void> {
  const db = await offlineDb();
  await db.put("bundles", bundle);
}

export async function deleteBundle(owner: BundleOwner): Promise<void> {
  const db = await offlineDb();
  await db.delete("bundles", bundleScope(owner.tenantId, owner.sessionId));
}

/**
 * Downloads and stores the catalog, or drops the stored one when offline selling is off. When
 * `isCancelled` says so once the download is back (signed out meanwhile), nothing is written, so a
 * catalog never reappears after the sign-out wiped it; null is returned then.
 */
export async function refreshBundle(client: HttpClient, owner: BundleOwner, now: Date = new Date(), isCancelled: () => boolean = () => false): Promise<OfflineBundle | null> {
  const bundle = await fetchOfflineBundle(client, owner, now);
  if (isCancelled()) return null;
  if (bundle) await saveBundle(bundle);
  else await deleteBundle(owner);
  return bundle;
}

/** The catalog may still be sold from at `now`. */
export function bundleFresh(bundle: Pick<OfflineBundle, "expires_at">, now: Date = new Date()): boolean {
  return now.getTime() <= Date.parse(bundle.expires_at);
}
