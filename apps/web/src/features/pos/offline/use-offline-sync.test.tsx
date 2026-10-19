import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { listCheckoutIntents, rememberCheckoutIntent } from "./checkout-intents";
import { connectivity } from "./connectivity";
import { subscribeOfflineEvents, type OfflineEvent } from "./offline-events";
import { addToOutbox, listOutbox, markOutboxException } from "./outbox-repository";
import { resetOfflineDb } from "./testing/fake-offline-db";
import {
  OFFLINE_TENANT_ID,
  meHandler,
  newOutboxEntry,
  offlineSessionHandler,
  offlineSyncServer,
  syncExceptionsHandler,
  testCheckoutIntent,
  testPosSale,
  testSyncException,
} from "./testing/handlers";
import { blockedSalesMessage, foreignSalesMessage, useOfflineSync } from "./use-offline-sync";

function Probe() {
  const sync = useOfflineSync(OFFLINE_TENANT_ID);
  return (
    <>
      <p data-testid="counts">
        pending {sync.pending.length} parked {sync.parked.length} blocked {sync.blocked.length} intents {sync.intents.length} foreign {sync.foreign}
      </p>
      <button type="button" onClick={sync.sync}>
        Đồng bộ ngay
      </button>
      {sync.blocked.map((e) => (
        <button key={e.offline_client_id} type="button" onClick={() => void sync.requeue(e.offline_client_id)}>
          Gửi lại {e.label}
        </button>
      ))}
    </>
  );
}

async function addSales(count: number) {
  const out = [];
  for (let n = 1; n <= count; n++) out.push(await addToOutbox(newOutboxEntry(n)));
  return out;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

beforeEach(resetOfflineDb);
afterEach(() => connectivity.reset());

describe("useOfflineSync", () => {
  it("uploads the waiting sales once the API is reachable and announces the result", async () => {
    await addSales(3);
    const sync = offlineSyncServer();
    server.use(offlineSessionHandler(), sync.handler, syncExceptionsHandler(() => []));
    renderWithProviders(<Probe />);

    expect(await screen.findByText("Đồng bộ xong: 3 giao dịch ghi sổ. Mỗi giao dịch chỉ ghi một lần.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 0 parked 0"));
    expect(sync.uploads).toHaveLength(3);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("waits while offline and tells a manual sync why nothing was sent", async () => {
    await addSales(1);
    const sync = offlineSyncServer();
    server.use(offlineSessionHandler(), sync.handler, syncExceptionsHandler(() => []));
    connectivity.simulate(true);
    renderWithProviders(<Probe />);

    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 1"));
    await userEvent.click(screen.getByRole("button", { name: "Đồng bộ ngay" }));
    expect(await screen.findByText("Chưa kết nối được máy chủ. Giao dịch vẫn nằm trên máy và sẽ đồng bộ khi có mạng.")).toBeInTheDocument();
    expect(sync.uploads).toEqual([]);
  });

  it("keeps every sale on an ended session", async () => {
    await addSales(2);
    let uploads = 0;
    server.use(
      offlineSessionHandler(),
      http.post(apiUrl("/pos/offline-sync"), () => {
        uploads++;
        return apiError(401, "unauthenticated");
      }),
      syncExceptionsHandler(() => []),
    );
    renderWithProviders(<Probe />);

    await waitFor(() => expect(uploads).toBe(1));
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 2"));
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(2);
    expect(screen.queryByText(/Đồng bộ xong/)).not.toBeInTheDocument();
  });

  it("backs off on a server error while the API still answers, instead of retrying at once", async () => {
    await addSales(1);
    let uploads = 0;
    server.use(
      meHandler(),
      offlineSessionHandler(),
      http.post(apiUrl("/pos/offline-sync"), () => {
        uploads++;
        return apiError(500, "internal_error");
      }),
      syncExceptionsHandler(() => []),
    );
    const stop = connectivity.start();
    renderWithProviders(<Probe />);

    await sleep(5_000);
    stop();
    // Retries wait 1 s, then 2 s, then 4 s (plus jitter): three uploads at most in 5 s.
    expect(uploads).toBeGreaterThanOrEqual(2);
    expect(uploads).toBeLessThanOrEqual(3);
    expect(connectivity.getState()).toBe("online");
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  }, 10_000);

  it("sends only the signed-in tenant's sales and counts those another tenant left", async () => {
    const [mine] = await addSales(1);
    await addToOutbox(newOutboxEntry(2, { tenant_id: "other-tenant" }));
    const sync = offlineSyncServer();
    server.use(offlineSessionHandler(), sync.handler, syncExceptionsHandler(() => []));
    renderWithProviders(<Probe />);

    expect(await screen.findByText(/^Đồng bộ xong: 1 giao dịch ghi sổ\./)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 0 parked 0 blocked 0 intents 0 foreign 1"));
    expect(sync.uploads).toEqual([mine?.offline_client_id]);
    expect(await listOutbox("other-tenant")).toHaveLength(1);
    expect(foreignSalesMessage(1)).toMatch(/chỉ được gửi khi đăng nhập lại đúng cửa hàng đó/);
  });

  it("sets a refused sale aside so the next ones post, and sends it again on “Gửi lại”", async () => {
    const [first, second] = await addSales(2);
    let refuse = true;
    const sync = offlineSyncServer();
    server.use(
      offlineSessionHandler(),
      http.post(apiUrl("/pos/offline-sync"), async ({ request }) => {
        const body = (await request.clone().json()) as { sales: { offline_client_id: string }[] };
        return refuse && body.sales[0]?.offline_client_id === first?.offline_client_id ? apiError(422, "validation_failed", { message: "Dữ liệu không hợp lệ." }) : undefined;
      }),
      sync.handler,
      syncExceptionsHandler(() => []),
    );
    renderWithProviders(<Probe />);

    expect(await screen.findByText(blockedSalesMessage(1))).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 0 parked 0 blocked 1"));
    expect(sync.uploads).toEqual([second?.offline_client_id]);

    refuse = false;
    await userEvent.click(screen.getByRole("button", { name: "Gửi lại OFF-001" }));
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 0 parked 0 blocked 0"));
    expect(sync.uploads).toEqual([second?.offline_client_id, first?.offline_client_id]);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });

  it("sends an unanswered checkout again with its own key before the outbox and tells the cart", async () => {
    const intent = testCheckoutIntent(1);
    await rememberCheckoutIntent(intent);
    const keys: (string | null)[] = [];
    const events: OfflineEvent[] = [];
    const unsubscribe = subscribeOfflineEvents((e) => events.push(e));
    server.use(
      http.post(apiUrl("/pos/checkout"), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        return HttpResponse.json(testPosSale("HD-0042"), { status: 201 });
      }),
      syncExceptionsHandler(() => []),
    );
    renderWithProviders(<Probe />);

    expect(await screen.findByText(/^Đã ghi sổ/)).toHaveTextContent("HD-0042");
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("intents 0"));
    expect(keys).toEqual([intent.idempotency_key]);
    expect(events).toContainEqual({ type: "intent-settled", tenantId: OFFLINE_TENANT_ID, key: intent.idempotency_key, posted: true });
    expect(await listCheckoutIntents(OFFLINE_TENANT_ID)).toEqual([]);
    unsubscribe();
  });

  it("lists parked sales and drops those a manager settled on another device", async () => {
    const [a, b] = await addSales(2);
    if (!a || !b) throw new Error("sales missing");
    await markOutboxException(a.offline_client_id, { id: "sx-1", error_code: "sku_inactive", error_detail: null });
    await markOutboxException(b.offline_client_id, { id: "sx-2", error_code: "sku_inactive", error_detail: null });
    // Only the first is still open on the server.
    server.use(syncExceptionsHandler(() => [testSyncException(a, { id: "sx-1" })]));
    renderWithProviders(<Probe />);

    await waitFor(async () => expect((await listOutbox(OFFLINE_TENANT_ID)).map((e) => e.offline_client_id)).toEqual([a.offline_client_id]));
    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("pending 0 parked 1"));
  });

  it("does not prune when the queue cannot be read", async () => {
    const [a] = await addSales(1);
    if (!a) throw new Error("sale missing");
    await markOutboxException(a.offline_client_id, { id: "sx-1", error_code: "sku_inactive", error_detail: null });
    server.use(http.get(apiUrl("/pos/sync-exceptions"), () => HttpResponse.json({ error: { code: "forbidden", message: "x" } }, { status: 403 })));
    renderWithProviders(<Probe />);

    await waitFor(() => expect(screen.getByTestId("counts")).toHaveTextContent("parked 1"));
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(1);
  });
});
