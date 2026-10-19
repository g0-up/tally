import { openDB, type DBSchema, type IDBPDatabase } from "idb";
import type { components } from "../../../shared/api/schema";
import type { Money } from "../../../shared/domain/money";
import type { ApiColor, PosPaymentMethod, PosTile } from "../../sales/schemas";

// IndexedDB of the offline POS. The browser already keeps it per store origin; every record is
// also keyed by tenant (and the catalog by login session) so nothing cached for one tenant or
// session is ever read by another.
//   bundles  catalog and prices a device sells from, one per `${tenant_id}:${device_session_id}`;
//   outbox   offline sales waiting for the server, keyed by their offline_client_id;
//   meta     the OFF-### counter per tenant, the tenant that owns the database, the boot snapshot;
//   intents  online checkouts whose answer never arrived, keyed by their Idempotency-Key.
// Signing out drops the catalog and the boot snapshot but never an unsynced sale or checkout: those
// wait for the next sign-in to the same tenant on this device.

export const OFFLINE_DB_NAME = "tally-pos-offline";
const OFFLINE_DB_VERSION = 2;

export type PosSettings = components["schemas"]["PosSettings"];
export type OfflineSaleBody = components["schemas"]["OfflineSale"];

export interface OfflineBundle {
  /** `${tenant_id}:${device_session_id}`. */
  scope: string;
  tenant_id: string;
  device_session_id: string;
  /** ETag of the bootstrap answer, or its price timestamp when the server sent none. */
  version: string;
  /** When the server priced the catalog (`priced_at`): the time shown in the banner. */
  cached_at: string;
  /** Last time this device downloaded it. */
  fetched_at: string;
  /** No offline sale after this instant: the server's offline window, capped by the store's hours. */
  expires_at: string;
  settings: PosSettings;
  payment_methods: PosPaymentMethod[];
  tiles: PosTile[];
  colors: ApiColor[];
}

/** One line of an offline sale as the screen showed it (the API body carries ids only). */
export interface OutboxLineView {
  name: string;
  qty: string;
  unit_name: string;
  amount: Money;
}

export interface OutboxException {
  id: string | null;
  error_code: string;
  error_detail: string | null;
}

export interface OutboxEntry {
  offline_client_id: string;
  tenant_id: string;
  device_session_id: string;
  /** "OFF-001": the device counter, for toasts and lists. */
  label: string;
  seq: number;
  /** Exactly what is sent to POST /pos/offline-sync; never rebuilt, so a resend is byte-identical. */
  sale: OfflineSaleBody;
  total: Money;
  bundle_version: string;
  lines: OutboxLineView[];
  /**
   * "pending" until the server answers; "exception" once it parked the sale for a manager;
   * "blocked" when the server refused it in a way sending again does not fix, so it waits for the
   * cashier ("Gửi lại" / "Xóa khỏi máy") and the sales after it still go.
   */
  status: "pending" | "exception" | "blocked";
  exception?: OutboxException;
  /** Uploads the server answered with a failure; past a limit the sale is blocked. */
  failures?: number;
}

/**
 * An online checkout that got no answer (network failure, gateway timeout): the server may have
 * posted it. It is sent again with the same Idempotency-Key and body before the outbox syncs, and
 * leaves only on a 2xx (posted, or replayed from the first attempt) or a refusal that proves it
 * did not post.
 */
export interface CheckoutIntent {
  idempotency_key: string;
  tenant_id: string;
  /** "/pos/checkout" or "/pos/carts/{id}:checkout". */
  path: string;
  /** Exactly the body that was sent. */
  body: unknown;
  if_match: string | null;
  /** What the customer paid at the counter (cash and transfer), for the messages. */
  total: Money;
  created_at: string;
}

export interface MetaRecord {
  key: string;
  value: unknown;
}

interface OfflineSchema extends DBSchema {
  bundles: { key: string; value: OfflineBundle };
  outbox: { key: string; value: OutboxEntry; indexes: { "by-tenant": string } };
  meta: { key: string; value: MetaRecord };
  intents: { key: string; value: CheckoutIntent; indexes: { "by-tenant": string } };
}

export type OfflineDb = IDBPDatabase<OfflineSchema>;

export const META_OWNER = "owner";
export const META_BOOT = "boot";
export const counterKey = (tenantId: string) => `counter:${tenantId}`;
export const bundleScope = (tenantId: string, sessionId: string) => `${tenantId}:${sessionId}`;

/** False where the browser has no IndexedDB (private modes of old browsers, server rendering). */
export function offlineStorageAvailable(): boolean {
  return typeof indexedDB !== "undefined";
}

let opening: Promise<OfflineDb> | null = null;

/** The database, opened once per page; a newer tab upgrading it closes this connection. */
export function offlineDb(): Promise<OfflineDb> {
  if (!offlineStorageAvailable()) return Promise.reject(new Error("IndexedDB không khả dụng trên trình duyệt này."));
  opening ??= openDB<OfflineSchema>(OFFLINE_DB_NAME, OFFLINE_DB_VERSION, {
    upgrade(db, oldVersion) {
      if (oldVersion < 1) {
        db.createObjectStore("bundles", { keyPath: "scope" });
        const outbox = db.createObjectStore("outbox", { keyPath: "offline_client_id" });
        outbox.createIndex("by-tenant", "tenant_id");
        db.createObjectStore("meta", { keyPath: "key" });
      }
      if (oldVersion < 2) {
        const intents = db.createObjectStore("intents", { keyPath: "idempotency_key" });
        intents.createIndex("by-tenant", "tenant_id");
      }
    },
    blocking() {
      void closeOfflineDb();
    },
    terminated() {
      opening = null;
    },
  }).catch((e: unknown) => {
    opening = null;
    throw e;
  });
  return opening;
}

/**
 * Waits for the writes of a transaction and its completion together: a failed write aborts the
 * transaction, and its `done` rejection is then handled here instead of escaping unobserved.
 */
export async function inTransaction<T>(writes: Promise<T>, done: Promise<void>): Promise<T> {
  const [result] = await Promise.all([writes, done]);
  return result;
}

/** Closes the connection; the next call opens it again (tests, version upgrades). */
export async function closeOfflineDb(): Promise<void> {
  const pending = opening;
  opening = null;
  if (!pending) return;
  try {
    (await pending).close();
  } catch {
    // It never opened: nothing to close.
  }
}

/**
 * Sign-out or a revoked session: drops every cached catalog and the boot snapshot. Unsynced sales,
 * unanswered checkouts and the OFF counter stay, keyed by their tenant: they are listed and sent
 * only after the next sign-in to that same tenant, since dropping them would lose cash already
 * taken.
 */
export async function wipeSessionData(): Promise<void> {
  if (!offlineStorageAvailable()) return;
  const db = await offlineDb();
  const tx = db.transaction(["bundles", "meta"], "readwrite");
  await Promise.all([tx.objectStore("bundles").clear(), tx.objectStore("meta").delete(META_BOOT), tx.done]);
}

/**
 * Records the tenant signed in on this device. Another tenant than the stored one drops every
 * catalog and the boot snapshot of the previous one; its unsynced sales and checkouts stay keyed by
 * its tenant id and are never listed or sent for this one. Returns true when the owner changed.
 */
export async function claimTenant(tenantId: string): Promise<boolean> {
  const db = await offlineDb();
  const tx = db.transaction(["bundles", "meta"], "readwrite");
  const write = async () => {
    const meta = tx.objectStore("meta");
    const owner = await meta.get(META_OWNER);
    const changed = owner !== undefined && owner.value !== tenantId;
    if (changed) {
      await tx.objectStore("bundles").clear();
      await meta.delete(META_BOOT);
    }
    if (owner === undefined || changed) await meta.put({ key: META_OWNER, value: tenantId });
    return changed;
  };
  const changed = await inTransaction(write(), tx.done);
  return changed;
}
