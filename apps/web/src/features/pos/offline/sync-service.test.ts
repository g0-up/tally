import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { createPlatform } from "../../../app/platform";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { forgetCheckoutIntent, listCheckoutIntents, rememberCheckoutIntent } from "./checkout-intents";
import { addToOutbox, listOutbox, markOutboxBlocked, markOutboxException, recordOutboxFailure, removeFromOutbox } from "./outbox-repository";
import { BACKOFF_MAX_MS, MAX_SALE_FAILURES, backoffDelay, syncOutbox, type SyncReport, type SyncStore } from "./sync-service";
import { resetOfflineDb } from "./testing/fake-offline-db";
import { OFFLINE_TENANT_ID, newOutboxEntry, offlineSessionHandler, offlineSyncServer, testCheckoutIntent, testPosSale } from "./testing/handlers";

const store: SyncStore = {
  list: () => listOutbox(OFFLINE_TENANT_ID),
  remove: removeFromOutbox,
  markException: markOutboxException,
  markBlocked: markOutboxBlocked,
  recordFailure: recordOutboxFailure,
  listIntents: () => listCheckoutIntents(OFFLINE_TENANT_ID),
  forgetIntent: forgetCheckoutIntent,
};

async function addSales(count: number) {
  const out = [];
  for (let n = 1; n <= count; n++) out.push(await addToOutbox(newOutboxEntry(n)));
  return out;
}

const done = (init: Partial<SyncReport> = {}): SyncReport => ({ posted: 0, parked: 0, blocked: 0, checkouts: [], granted: false, stopped: null, error: null, ...init });

const client = () => createPlatform("tenant").client;

beforeEach(resetOfflineDb);

describe("syncOutbox", () => {
  it("renews the offline window, then posts each sale once, oldest first, as stored", async () => {
    const sales = await addSales(3);
    const calls: string[] = [];
    const sent: unknown[] = [];
    const sync = offlineSyncServer();
    server.use(
      http.post(apiUrl("/pos/offline-session"), () => {
        calls.push("session");
        return HttpResponse.json({ offline_until: "2026-10-19T00:00:00Z" });
      }),
      http.post(apiUrl("/pos/offline-sync"), async ({ request }) => {
        calls.push("sync");
        sent.push(await request.clone().json());
        return undefined;
      }),
      sync.handler,
    );

    const report = await syncOutbox(client(), store);

    expect(report).toEqual(done({ posted: 3, granted: true }));
    expect(calls).toEqual(["session", "sync", "sync", "sync"]);
    expect(sync.uploads).toEqual(sales.map((s) => s.offline_client_id));
    expect(sent).toEqual(sales.map((s) => ({ sales: [s.sale] })));
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("does nothing without pending sales", async () => {
    const report = await syncOutbox(client(), store);
    expect(report).toEqual(done());
  });

  it("posts a sale once when the answer of the first upload was lost", async () => {
    const [sale] = await addSales(1);
    const sync = offlineSyncServer();
    let lose = true;
    server.use(
      offlineSessionHandler(),
      // The server posts the sale, but the answer never reaches the device.
      http.post(apiUrl("/pos/offline-sync"), async ({ request }) => {
        if (!lose) return undefined;
        lose = false;
        const body = (await request.json()) as { sales: { offline_client_id: string }[] };
        for (const s of body.sales) sync.posted.add(s.offline_client_id);
        return HttpResponse.error();
      }),
      sync.handler,
    );

    const first = await syncOutbox(client(), store);
    expect(first.stopped).toBe("network");
    expect(first.posted).toBe(0);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);

    const second = await syncOutbox(client(), store);
    expect(second).toEqual(done({ posted: 1, granted: true }));
    expect(sync.uploads).toEqual([sale?.offline_client_id]);
    expect(sync.posted.size).toBe(1);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("keeps a parked sale marked with its exception and goes on with the next ones", async () => {
    const [first, second] = await addSales(2);
    const sync = offlineSyncServer((id) => (id === first?.offline_client_id ? { status: "exception", sync_exception_id: "sx-1", error_code: "sku_inactive", error_detail: "sku is inactive" } : { status: "posted" }));
    server.use(offlineSessionHandler(), sync.handler);

    const report = await syncOutbox(client(), store);

    expect(report).toEqual(done({ posted: 1, parked: 1, granted: true }));
    const rows = await listOutbox(OFFLINE_TENANT_ID);
    expect(rows.map((r) => [r.offline_client_id, r.status])).toEqual([[first?.offline_client_id, "exception"]]);
    expect(rows[0]?.exception).toEqual({ id: "sx-1", error_code: "sku_inactive", error_detail: "sku is inactive" });
    expect(sync.uploads).toEqual([first?.offline_client_id, second?.offline_client_id]);

    // A parked sale is not uploaded again.
    expect(await syncOutbox(client(), store)).toEqual(done());
    expect(sync.uploads).toHaveLength(2);
  });

  it("pauses on an ended session (401) without dropping anything", async () => {
    await addSales(2);
    server.use(offlineSessionHandler(), http.post(apiUrl("/pos/offline-sync"), () => apiError(401, "unauthenticated")));
    const report = await syncOutbox(client(), store);
    expect(report.stopped).toBe("reauth");
    expect(report.posted).toBe(0);
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((r) => r.status)).toEqual(["pending", "pending"]);
  });

  it("pauses before uploading when renewing the window answers 401", async () => {
    await addSales(1);
    const sync = offlineSyncServer();
    server.use(http.post(apiUrl("/pos/offline-session"), () => apiError(401, "unauthenticated")), sync.handler);
    expect((await syncOutbox(client(), store)).stopped).toBe("reauth");
    expect(sync.uploads).toEqual([]);
  });

  it("still uploads when the window cannot be renewed because offline selling was turned off", async () => {
    await addSales(1);
    const sync = offlineSyncServer();
    server.use(http.post(apiUrl("/pos/offline-session"), () => apiError(422, "offline_pos_disabled")), sync.handler);
    expect(await syncOutbox(client(), store)).toEqual(done({ posted: 1, granted: true }));
  });

  it("stops on no answer as a network failure, on a server error for a retry, and on 403 as read-only", async () => {
    await addSales(1);
    server.use(offlineSessionHandler(), http.post(apiUrl("/pos/offline-sync"), () => HttpResponse.error()));
    expect((await syncOutbox(client(), store)).stopped).toBe("network");

    server.use(http.post(apiUrl("/pos/offline-sync"), () => apiError(503, "unavailable")));
    expect((await syncOutbox(client(), store)).stopped).toBe("server");

    server.use(http.post(apiUrl("/pos/offline-sync"), () => apiError(403, "subscription_read_only")));
    const report = await syncOutbox(client(), store);
    expect(report.stopped).toBe("read_only");
    expect(report.error).toBe("Thuê bao đang ở chế độ chỉ xem. Gia hạn để tiếp tục ghi sổ.");
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  });

  it("keeps a failed sale pending and stops with the reason in Vietnamese", async () => {
    const [first, second] = await addSales(2);
    const sync = offlineSyncServer(() => ({ status: "failed", error_code: "internal_error", error_detail: "boom" }));
    server.use(offlineSessionHandler(), sync.handler);

    const report = await syncOutbox(client(), store);

    expect(report).toEqual(done({ granted: true, stopped: "failed", error: "Máy chủ gặp lỗi. Thử lại sau ít phút." }));
    expect(sync.uploads).toEqual([first?.offline_client_id]);
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((r) => [r.offline_client_id, r.status])).toEqual([
      [first?.offline_client_id, "pending"],
      [second?.offline_client_id, "pending"],
    ]);
  });

  it("stops when the answer does not mention the sale", async () => {
    await addSales(1);
    server.use(offlineSessionHandler(), http.post(apiUrl("/pos/offline-sync"), () => HttpResponse.json({ items: [] })));
    const report = await syncOutbox(client(), store);
    expect(report.stopped).toBe("server");
    expect(report.error).toBe("Máy chủ không trả kết quả cho giao dịch này.");
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  });
});

describe("syncOutbox: sales the server keeps refusing", () => {
  it("blocks a sale whose request is refused for good and still posts the ones after it", async () => {
    const [first, second] = await addSales(2);
    const sync = offlineSyncServer();
    server.use(
      offlineSessionHandler(),
      http.post(apiUrl("/pos/offline-sync"), async ({ request }) => {
        const body = (await request.clone().json()) as { sales: { offline_client_id: string }[] };
        return body.sales[0]?.offline_client_id === first?.offline_client_id ? apiError(422, "validation_failed", { message: "Dữ liệu không hợp lệ." }) : undefined;
      }),
      sync.handler,
    );

    const report = await syncOutbox(client(), store);

    expect(report).toEqual(done({ posted: 1, blocked: 1, granted: true }));
    expect(sync.uploads).toEqual([second?.offline_client_id]);
    const rows = await listOutbox(OFFLINE_TENANT_ID);
    expect(rows.map((r) => [r.offline_client_id, r.status])).toEqual([[first?.offline_client_id, "blocked"]]);
    expect(rows[0]?.exception).toMatchObject({ id: null, error_code: "validation_failed" });

    // A blocked sale is not sent again by later passes.
    expect(await syncOutbox(client(), store)).toEqual(done());
  });

  it("blocks a sale sent again with other contents under the same id", async () => {
    const [first, second] = await addSales(2);
    const sync = offlineSyncServer((id) => (id === first?.offline_client_id ? { status: "failed", error_code: "idempotency_key_reused", error_detail: "reused" } : { status: "posted" }));
    server.use(offlineSessionHandler(), sync.handler);

    expect(await syncOutbox(client(), store)).toEqual(done({ posted: 1, blocked: 1, granted: true }));
    expect(sync.uploads).toEqual([first?.offline_client_id, second?.offline_client_id]);
    expect((await listOutbox(OFFLINE_TENANT_ID)).map((r) => r.status)).toEqual(["blocked"]);
  });

  it(`retries a sale that keeps failing on the server, then blocks it after ${MAX_SALE_FAILURES} failures so the queue moves on`, async () => {
    const [first, second] = await addSales(2);
    const sync = offlineSyncServer((id) => (id === first?.offline_client_id ? { status: "failed", error_code: "internal_error", error_detail: "boom" } : { status: "posted" }));
    server.use(offlineSessionHandler(), sync.handler);

    for (let n = 1; n < MAX_SALE_FAILURES; n++) {
      const report = await syncOutbox(client(), store, { grant: false });
      expect(report.stopped).toBe("failed");
      expect((await listOutbox(OFFLINE_TENANT_ID))[0]).toMatchObject({ status: "pending", failures: n });
    }
    expect(await syncOutbox(client(), store, { grant: false })).toEqual(done({ posted: 1, blocked: 1 }));
    expect(sync.uploads.filter((id) => id === first?.offline_client_id)).toHaveLength(MAX_SALE_FAILURES);
    expect(sync.uploads.filter((id) => id === second?.offline_client_id)).toHaveLength(1);
  });

  it("does not count a sale another upload is still posting", async () => {
    await addSales(1);
    const sync = offlineSyncServer(() => ({ status: "failed", error_code: "idempotency_key_in_progress", error_detail: null }));
    server.use(offlineSessionHandler(), sync.handler);
    for (let n = 0; n < MAX_SALE_FAILURES + 1; n++) expect((await syncOutbox(client(), store, { grant: false })).stopped).toBe("failed");
    const [row] = await listOutbox(OFFLINE_TENANT_ID);
    expect(row?.status).toBe("pending");
    expect(row?.failures).toBeUndefined();
  });

  it("renews the offline window only when asked", async () => {
    await addSales(1);
    let grants = 0;
    server.use(
      http.post(apiUrl("/pos/offline-session"), () => {
        grants++;
        return HttpResponse.json({ offline_until: "2026-10-19T00:00:00Z" });
      }),
      offlineSyncServer().handler,
    );
    expect(await syncOutbox(client(), store, { grant: false })).toEqual(done({ posted: 1 }));
    expect(grants).toBe(0);
  });

  it("gives up on a request that does not answer in time", async () => {
    await addSales(1);
    server.use(
      offlineSessionHandler(),
      http.post(apiUrl("/pos/offline-sync"), async () => {
        await new Promise((resolve) => setTimeout(resolve, 200));
        return HttpResponse.json({ items: [] });
      }),
    );
    const report = await syncOutbox(client(), store, { grant: false, timeoutMs: 20 });
    expect(report.stopped).toBe("network");
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  });
});

describe("syncOutbox: online checkouts without an answer", () => {
  interface CheckoutCall {
    key: string | null;
    body: unknown;
  }

  it("sends each again with its own Idempotency-Key and body before the outbox, and forgets it once answered", async () => {
    const intent = testCheckoutIntent(1);
    await rememberCheckoutIntent(intent);
    await addSales(1);
    const order: string[] = [];
    const checkouts: CheckoutCall[] = [];
    const sync = offlineSyncServer();
    server.use(
      offlineSessionHandler(),
      http.post(apiUrl("/pos/checkout"), async ({ request }) => {
        order.push("checkout");
        checkouts.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        return HttpResponse.json(testPosSale("HD-0042"), { status: 201 });
      }),
      http.post(apiUrl("/pos/offline-sync"), () => {
        order.push("sync");
        return undefined;
      }),
      sync.handler,
    );

    const report = await syncOutbox(client(), store);

    expect(order).toEqual(["checkout", "sync"]);
    expect(checkouts).toEqual([{ key: intent.idempotency_key, body: intent.body }]);
    expect(report).toEqual(done({ posted: 1, granted: true, checkouts: [{ key: intent.idempotency_key, posted: true, docNo: "HD-0042", total: intent.total, error: null }] }));
    expect(await listCheckoutIntents(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("forgets one the API refuses, since a refused command posts nothing, and says why", async () => {
    const intent = testCheckoutIntent(1);
    await rememberCheckoutIntent(intent);
    server.use(http.post(apiUrl("/pos/checkout"), () => apiError(409, "shift_closed", { message: "Ca đã đóng." })));

    const report = await syncOutbox(client(), store);

    expect(report.checkouts).toEqual([{ key: intent.idempotency_key, posted: false, docNo: null, total: intent.total, error: expect.any(String) }]);
    expect(report.stopped).toBeNull();
    expect(await listCheckoutIntents(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("keeps one without an answer or with a server error, and does not upload the outbox meanwhile", async () => {
    await rememberCheckoutIntent(testCheckoutIntent(1));
    await addSales(1);
    const sync = offlineSyncServer();
    server.use(offlineSessionHandler(), http.post(apiUrl("/pos/checkout"), () => HttpResponse.error()), sync.handler);
    expect((await syncOutbox(client(), store)).stopped).toBe("network");

    server.use(http.post(apiUrl("/pos/checkout"), () => apiError(502, "bad_gateway")));
    expect((await syncOutbox(client(), store)).stopped).toBe("server");

    server.use(http.post(apiUrl("/pos/checkout"), () => apiError(409, "idempotency_key_in_progress")));
    expect((await syncOutbox(client(), store)).stopped).toBe("failed");

    expect(await listCheckoutIntents(OFFLINE_TENANT_ID)).toHaveLength(1);
    expect(sync.uploads).toEqual([]);
  });
});

describe("backoffDelay", () => {
  it("doubles from 1 s with up to 50% jitter and caps at 60 s", () => {
    expect(backoffDelay(0, () => 0)).toBe(1_000);
    expect(backoffDelay(1, () => 0)).toBe(2_000);
    expect(backoffDelay(3, () => 0)).toBe(8_000);
    expect(backoffDelay(3, () => 1)).toBe(12_000);
    expect(backoffDelay(10, () => 0)).toBe(BACKOFF_MAX_MS);
    expect(backoffDelay(10, () => 1)).toBe(BACKOFF_MAX_MS);
  });
});
