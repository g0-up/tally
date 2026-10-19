import { isApiError } from "../../../shared/api/api-error";
import { offlineDb, offlineStorageAvailable, type CheckoutIntent } from "./offline-db";
import { publishOfflineEvent } from "./offline-events";

// Online checkouts whose answer never arrived. The server may have posted such a sale, so the cart
// is not sold again offline, and the checkout is sent again with the same Idempotency-Key once the
// network is back: the server then answers with the first attempt's result instead of posting twice.
// Storing or dropping one is told to every tab, so the sync of each sees it is there to send.

/**
 * The request may have reached the server and posted: no answer at all (network, timeout, abort),
 * or a gateway that gave up waiting for the API. Any other answer came from the API itself.
 */
export function isUncertainOutcome(e: unknown): boolean {
  if (!isApiError(e)) return true;
  return e.isNetwork || e.status === 502 || e.status === 504;
}

export async function rememberCheckoutIntent(intent: CheckoutIntent): Promise<void> {
  const db = await offlineDb();
  await db.put("intents", intent);
  publishOfflineEvent({ type: "changed", tenantId: intent.tenant_id });
}

export async function forgetCheckoutIntent(key: string): Promise<void> {
  if (!offlineStorageAvailable()) return;
  const db = await offlineDb();
  const row = await db.get("intents", key);
  if (!row) return;
  await db.delete("intents", key);
  publishOfflineEvent({ type: "changed", tenantId: row.tenant_id });
}

/** The tenant's unanswered checkouts, oldest first. */
export async function listCheckoutIntents(tenantId: string): Promise<CheckoutIntent[]> {
  const db = await offlineDb();
  const rows = await db.getAllFromIndex("intents", "by-tenant", tenantId);
  return rows.sort((a, b) => a.created_at.localeCompare(b.created_at));
}
