import type { Money } from "../../../shared/domain/money";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { counterKey, inTransaction, offlineDb, type OutboxEntry, type OutboxException } from "./offline-db";

// The outbox of offline sales. A sale is written once, with its OFF-### label taken from the
// device counter in the same transaction, and leaves the outbox only when the server answered that
// it is posted. A sale the server parked stays, marked, until a manager settles it; one it refused
// for good stays blocked until the cashier sends it again or removes it.

/** "OFF-007"; past 999 the number simply grows. */
export function offlineLabel(n: number): string {
  return `OFF-${String(n).padStart(3, "0")}`;
}

export type NewOutboxEntry = Omit<OutboxEntry, "label" | "seq" | "status" | "exception" | "failures">;

/** The sale would take the tenant's unposted offline sales on this device past the store's cap. */
export class OutboxCapError extends Error {
  constructor() {
    super("Offline cap exceeded");
    this.name = "OutboxCapError";
  }
}

/**
 * Stores a new offline sale with the next OFF number of its tenant; the counter never goes back.
 * With `cap`, the sale is refused (OutboxCapError) when it and every sale of the tenant still on
 * the device (waiting, parked or blocked: none of them is posted yet) add up to more than the cap.
 * The check runs in the same transaction as the write, and IndexedDB runs read-write transactions
 * on the outbox one at a time across tabs, so two tabs never both fill the cap.
 */
export async function addToOutbox(entry: NewOutboxEntry, cap?: Money | string): Promise<OutboxEntry> {
  const db = await offlineDb();
  const tx = db.transaction(["meta", "outbox"], "readwrite");
  const write = async () => {
    const outbox = tx.objectStore("outbox");
    if (cap !== undefined) {
      const unposted = await outbox.index("by-tenant").getAll(entry.tenant_id);
      if (sumDec(unposted.map((r) => r.total)).plus(dec(entry.total)).gt(dec(cap))) throw new OutboxCapError();
    }
    const meta = tx.objectStore("meta");
    const key = counterKey(entry.tenant_id);
    const last = (await meta.get(key))?.value;
    const seq = (typeof last === "number" ? last : 0) + 1;
    const stored: OutboxEntry = { ...entry, label: offlineLabel(seq), seq, status: "pending" };
    await meta.put({ key, value: seq });
    await outbox.add(stored);
    return stored;
  };
  return inTransaction(write(), tx.done);
}

/** Every sale of a tenant still on this device, oldest first. */
export async function listOutbox(tenantId: string): Promise<OutboxEntry[]> {
  const db = await offlineDb();
  const rows = await db.getAllFromIndex("outbox", "by-tenant", tenantId);
  return rows.sort((a, b) => a.seq - b.seq);
}

/** Sales of other tenants on this device: they sync only after signing in to their own tenant. */
export async function countForeignSales(tenantId: string): Promise<number> {
  const db = await offlineDb();
  const total = await db.count("outbox");
  const mine = await db.countFromIndex("outbox", "by-tenant", tenantId);
  return total - mine;
}

/** The server posted it (or had posted it already), or the cashier removed a blocked sale. */
export async function removeFromOutbox(offlineClientId: string): Promise<void> {
  const db = await offlineDb();
  await db.delete("outbox", offlineClientId);
}

async function updateEntry(offlineClientId: string, change: (row: OutboxEntry) => OutboxEntry): Promise<OutboxEntry | null> {
  const db = await offlineDb();
  const tx = db.transaction("outbox", "readwrite");
  const write = async () => {
    const row = await tx.store.get(offlineClientId);
    if (!row) return null;
    const next = change(row);
    await tx.store.put(next);
    return next;
  };
  return inTransaction(write(), tx.done);
}

/** The server parked it in the exception queue. */
export async function markOutboxException(offlineClientId: string, exception: OutboxException): Promise<void> {
  await updateEntry(offlineClientId, (row) => ({ ...row, status: "exception", exception }));
}

/** The server refused it in a way sending it again does not fix: it waits for the cashier. */
export async function markOutboxBlocked(offlineClientId: string, exception: OutboxException): Promise<void> {
  await updateEntry(offlineClientId, (row) => ({ ...row, status: "blocked", exception }));
}

/** One more failed upload; returns how many it has had. */
export async function recordOutboxFailure(offlineClientId: string): Promise<number> {
  const row = await updateEntry(offlineClientId, (r) => ({ ...r, failures: (r.failures ?? 0) + 1 }));
  return row?.failures ?? 0;
}

/** "Gửi lại": a blocked sale waits again, with its failures forgotten. */
export async function requeueOutboxEntry(offlineClientId: string): Promise<void> {
  await updateEntry(offlineClientId, (row) => {
    if (row.status !== "blocked") return row;
    const { exception: _exception, failures: _failures, ...rest } = row;
    return { ...rest, status: "pending" };
  });
}

/**
 * Drops parked sales a manager settled (resolved or discarded): those no longer in `openIds`, the
 * client ids the server still lists as open.
 */
export async function pruneSettledExceptions(tenantId: string, openIds: ReadonlySet<string>): Promise<number> {
  const db = await offlineDb();
  const tx = db.transaction("outbox", "readwrite");
  const write = async () => {
    const rows = await tx.store.index("by-tenant").getAll(tenantId);
    const settled = rows.filter((r) => r.status === "exception" && !openIds.has(r.offline_client_id));
    await Promise.all(settled.map((r) => tx.store.delete(r.offline_client_id)));
    return settled.length;
  };
  const count = await inTransaction(write(), tx.done);
  return count;
}
