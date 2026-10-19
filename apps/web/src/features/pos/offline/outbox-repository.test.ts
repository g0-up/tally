import { beforeEach, describe, expect, it } from "vitest";
import { closeOfflineDb } from "./offline-db";
import { toMoney } from "../../../shared/domain/money";
import {
  OutboxCapError,
  addToOutbox,
  countForeignSales,
  listOutbox,
  markOutboxBlocked,
  markOutboxException,
  offlineLabel,
  pruneSettledExceptions,
  recordOutboxFailure,
  removeFromOutbox,
  requeueOutboxEntry,
  type NewOutboxEntry,
} from "./outbox-repository";
import { resetOfflineDb } from "./testing/fake-offline-db";
import { OFFLINE_TENANT_ID, newOutboxEntry } from "./testing/handlers";

function newEntry(n: number, tenantId = OFFLINE_TENANT_ID): NewOutboxEntry {
  return newOutboxEntry(n, { tenant_id: tenantId, offline_client_id: `${tenantId}-${n}` });
}

beforeEach(resetOfflineDb);

describe("offlineLabel", () => {
  it("pads to three digits and keeps growing past 999", () => {
    expect(offlineLabel(7)).toBe("OFF-007");
    expect(offlineLabel(42)).toBe("OFF-042");
    expect(offlineLabel(1234)).toBe("OFF-1234");
  });
});

describe("outbox repository", () => {
  it("numbers sales OFF-001, OFF-002… per tenant and stores them pending", async () => {
    const a = await addToOutbox(newEntry(1));
    const b = await addToOutbox(newEntry(2));
    const other = await addToOutbox(newEntry(1, "other-tenant"));
    expect([a.label, b.label]).toEqual(["OFF-001", "OFF-002"]);
    expect(other.label).toBe("OFF-001");
    expect(a.status).toBe("pending");
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((e) => e.label)).toEqual(["OFF-001", "OFF-002"]);
    expect((await listOutbox("other-tenant")).map((e) => e.offline_client_id)).toEqual(["other-tenant-1"]);
  });

  it("never reuses a number after sales leave the outbox, also after a reload", async () => {
    const first = await addToOutbox(newEntry(1));
    await addToOutbox(newEntry(2));
    await removeFromOutbox(first.offline_client_id);
    await removeFromOutbox(newEntry(2).offline_client_id);
    // A reload: the page opens the database again.
    await closeOfflineDb();
    const next = await addToOutbox(newEntry(3));
    expect(next.label).toBe("OFF-003");
    expect(next.seq).toBe(3);
  });

  it("keeps a sale stored once: the same offline_client_id cannot be added twice", async () => {
    await addToOutbox(newEntry(1));
    await expect(addToOutbox(newEntry(1))).rejects.toThrow();
    const rows = await listOutbox(OFFLINE_TENANT_ID);
    expect(rows).toHaveLength(1);
    // The refused write did not move the counter either.
    expect((await addToOutbox(newEntry(2))).label).toBe("OFF-002");
  });

  it("lists sales oldest first", async () => {
    for (const n of [1, 2, 3]) await addToOutbox(newEntry(n));
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((e) => e.seq)).toEqual([1, 2, 3]);
  });

  it("marks a parked sale and prunes it once the server no longer lists it as open", async () => {
    const a = await addToOutbox(newEntry(1));
    const b = await addToOutbox(newEntry(2));
    const c = await addToOutbox(newEntry(3));
    await markOutboxException(a.offline_client_id, { id: "sx-1", error_code: "sku_inactive", error_detail: null });
    await markOutboxException(b.offline_client_id, { id: "sx-2", error_code: "sku_inactive", error_detail: null });

    const rows = await listOutbox(OFFLINE_TENANT_ID);
    expect(rows.map((r) => r.status)).toEqual(["exception", "exception", "pending"]);
    expect(rows[0]?.exception).toEqual({ id: "sx-1", error_code: "sku_inactive", error_detail: null });

    const pruned = await pruneSettledExceptions(OFFLINE_TENANT_ID, new Set([b.offline_client_id]));
    expect(pruned).toBe(1);
    // A pending sale is never pruned, whatever the server's list says.
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((r) => r.offline_client_id)).toEqual([b.offline_client_id, c.offline_client_id]);
  });

  it("refuses a sale that takes the tenant's unposted sales past the cap, in the same write", async () => {
    // 100 000 đ each: two waiting, one parked, one blocked, and another tenant's sale that does not count.
    for (const n of [1, 2, 3, 4]) await addToOutbox(newEntry(n));
    await addToOutbox(newEntry(1, "other-tenant"));
    await markOutboxException(`${OFFLINE_TENANT_ID}-3`, { id: "sx-1", error_code: "sku_inactive", error_detail: null });
    await markOutboxBlocked(`${OFFLINE_TENANT_ID}-4`, { id: null, error_code: "validation_failed", error_detail: null });

    await expect(addToOutbox(newEntry(5), toMoney("450000"))).rejects.toBeInstanceOf(OutboxCapError);
    // Exactly at the cap is allowed.
    expect((await addToOutbox(newEntry(5), toMoney("500000"))).label).toBe("OFF-005");
    await expect(addToOutbox(newEntry(6), "500000")).rejects.toBeInstanceOf(OutboxCapError);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(5);
  });

  it("lets only one of two sales racing for the last room under the cap in", async () => {
    await addToOutbox(newEntry(1));
    const results = await Promise.allSettled([addToOutbox(newEntry(2), "200000"), addToOutbox(newEntry(3), "200000")]);
    expect(results.map((r) => r.status).sort()).toEqual(["fulfilled", "rejected"]);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(2);
  });

  it("blocks a sale, counts its failures, and sends it again with them forgotten", async () => {
    const a = await addToOutbox(newEntry(1));
    expect(await recordOutboxFailure(a.offline_client_id)).toBe(1);
    expect(await recordOutboxFailure(a.offline_client_id)).toBe(2);
    await markOutboxBlocked(a.offline_client_id, { id: null, error_code: "internal_error", error_detail: "boom" });
    expect((await listOutbox(OFFLINE_TENANT_ID))[0]).toMatchObject({ status: "blocked", failures: 2, exception: { error_code: "internal_error" } });

    await requeueOutboxEntry(a.offline_client_id);
    const [row] = await listOutbox(OFFLINE_TENANT_ID);
    expect(row?.status).toBe("pending");
    expect(row).not.toHaveProperty("failures");
    expect(row).not.toHaveProperty("exception");
    expect(row?.label).toBe("OFF-001");
  });

  it("does not send a parked sale again through requeue", async () => {
    const a = await addToOutbox(newEntry(1));
    await markOutboxException(a.offline_client_id, { id: "sx-1", error_code: "sku_inactive", error_detail: null });
    await requeueOutboxEntry(a.offline_client_id);
    expect((await listOutbox(OFFLINE_TENANT_ID))[0]?.status).toBe("exception");
  });

  it("never prunes a blocked sale: only the cashier removes it", async () => {
    const a = await addToOutbox(newEntry(1));
    await markOutboxBlocked(a.offline_client_id, { id: null, error_code: "validation_failed", error_detail: null });
    expect(await pruneSettledExceptions(OFFLINE_TENANT_ID, new Set())).toBe(0);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  });

  it("counts the sales other tenants left on the device", async () => {
    await addToOutbox(newEntry(1));
    await addToOutbox(newEntry(1, "other-tenant"));
    await addToOutbox(newEntry(2, "other-tenant"));
    expect(await countForeignSales(OFFLINE_TENANT_ID)).toBe(2);
    expect(await countForeignSales("other-tenant")).toBe(1);
  });

  it("ignores marking a sale that already left the outbox", async () => {
    await markOutboxException("missing", { id: null, error_code: "x", error_detail: null });
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });
});
