import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it } from "vitest";
import { ApiError } from "../../../shared/api/api-error";
import { platformKeys } from "../../../shared/api/query-keys";
import { BOOT_SNAPSHOT_KEYS, loadBootSnapshot, saveBootSnapshot, seedBootSnapshot, watchSessionForOfflineData } from "./boot-snapshot";
import { loadBundle, saveBundle } from "./offline-bundle-repository";
import { META_OWNER, claimTenant, offlineDb, wipeSessionData } from "./offline-db";
import { listCheckoutIntents, rememberCheckoutIntent } from "./checkout-intents";
import { addToOutbox, listOutbox } from "./outbox-repository";
import { resetOfflineDb } from "./testing/fake-offline-db";
import { OFFLINE_SESSION_ID, OFFLINE_TENANT_ID, newOutboxEntry, testBundle, testCheckoutIntent } from "./testing/handlers";

const HOST_KEY = ["host-context"] as const;
const OWNER = { tenantId: OFFLINE_TENANT_ID, sessionId: OFFLINE_SESSION_ID };

async function addSale(n: number) {
  return addToOutbox(newOutboxEntry(n));
}

/** A cache holding every answer the boot snapshot needs. */
function filledCache(): QueryClient {
  const qc = new QueryClient();
  qc.setQueryData(HOST_KEY, { kind: "tenant", root_url: "http://demo.localhost", csrf_token: "secret" });
  for (const key of BOOT_SNAPSHOT_KEYS) qc.setQueryData(key, { csrf_token: "secret", tenant: { id: OFFLINE_TENANT_ID }, key });
  return qc;
}

beforeEach(resetOfflineDb);

describe("wipe rules", () => {
  it("signing out drops the catalog and the boot snapshot but keeps unsynced sales, unanswered checkouts and the counter", async () => {
    await saveBundle(testBundle());
    await saveBootSnapshot(filledCache(), HOST_KEY, OFFLINE_TENANT_ID);
    await addSale(1);
    await rememberCheckoutIntent(testCheckoutIntent(1));

    await wipeSessionData();

    expect((await listCheckoutIntents(OFFLINE_TENANT_ID)).map((i) => i.idempotency_key)).toEqual([testCheckoutIntent(1).idempotency_key]);

    expect(await loadBundle(OWNER)).toBeNull();
    expect(await loadBootSnapshot()).toBeNull();
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((e) => e.label)).toEqual(["OFF-001"]);
    expect((await addSale(2)).label).toBe("OFF-002");
  });

  it("another tenant signing in drops the previous tenant's catalog, never its sales", async () => {
    expect(await claimTenant(OFFLINE_TENANT_ID)).toBe(false);
    await saveBundle(testBundle());
    await saveBootSnapshot(filledCache(), HOST_KEY, OFFLINE_TENANT_ID);
    await addSale(1);

    expect(await claimTenant(OFFLINE_TENANT_ID)).toBe(false);
    expect(await loadBundle(OWNER)).not.toBeNull();

    expect(await claimTenant("other-tenant")).toBe(true);
    expect(await loadBundle(OWNER)).toBeNull();
    expect(await loadBootSnapshot()).toBeNull();
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
    expect(await listOutbox("other-tenant")).toEqual([]);
    expect(await listCheckoutIntents("other-tenant")).toEqual([]);
    const db = await offlineDb();
    expect((await db.get("meta", META_OWNER))?.value).toBe("other-tenant");
  });
});

describe("boot snapshot", () => {
  it("is saved without CSRF tokens and seeds the cache back", async () => {
    expect(await saveBootSnapshot(filledCache(), HOST_KEY, OFFLINE_TENANT_ID, new Date("2026-10-18T09:00:00Z"))).toBe(true);
    const snapshot = await loadBootSnapshot();
    expect(snapshot?.tenant_id).toBe(OFFLINE_TENANT_ID);
    expect(snapshot?.saved_at).toBe("2026-10-18T09:00:00.000Z");
    expect(JSON.stringify(snapshot)).not.toContain("secret");

    const fresh = new QueryClient();
    if (!snapshot) throw new Error("snapshot missing");
    seedBootSnapshot(fresh, HOST_KEY, snapshot);
    expect(fresh.getQueryData(HOST_KEY)).toEqual({ kind: "tenant", root_url: "http://demo.localhost" });
    expect(fresh.getQueryData(platformKeys.me())).toMatchObject({ tenant: { id: OFFLINE_TENANT_ID } });
  });

  it("is skipped until every shell answer is cached", async () => {
    const qc = filledCache();
    qc.removeQueries({ queryKey: platformKeys.onboarding(), exact: true });
    expect(await saveBootSnapshot(qc, HOST_KEY, OFFLINE_TENANT_ID)).toBe(false);
    expect(await loadBootSnapshot()).toBeNull();
  });
});

describe("watchSessionForOfflineData", () => {
  const settle = () => new Promise((r) => setTimeout(r, 20));

  it("wipes the catalog when the session ends (401) and claims the tenant on sign-in", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const errors: unknown[] = [];
    const stop = watchSessionForOfflineData(qc, (e) => errors.push(e));

    await qc.fetchQuery({ queryKey: platformKeys.me(), queryFn: () => ({ tenant: { id: OFFLINE_TENANT_ID } }) });
    await settle();
    const db = await offlineDb();
    expect((await db.get("meta", META_OWNER))?.value).toBe(OFFLINE_TENANT_ID);

    await saveBundle(testBundle());
    await qc
      .fetchQuery({ queryKey: platformKeys.me(), queryFn: () => Promise.reject(new ApiError({ status: 401, code: "unauthenticated", message: "x" })), staleTime: 0 })
      .catch(() => undefined);
    await settle();
    expect(await loadBundle(OWNER)).toBeNull();
    expect(errors).toEqual([]);
    stop();
  });

  it("wipes the catalog when the session answer is removed (sign-out)", async () => {
    const qc = new QueryClient();
    const stop = watchSessionForOfflineData(qc);
    qc.setQueryData(platformKeys.me(), { tenant: { id: OFFLINE_TENANT_ID } });
    await saveBundle(testBundle());
    qc.removeQueries({ queryKey: platformKeys.me(), exact: true });
    await settle();
    expect(await loadBundle(OWNER)).toBeNull();
    stop();
  });
});
