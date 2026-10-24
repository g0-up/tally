import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { STALE_DATA_MESSAGE } from "../../../shared/api/api-error";
import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import { Button } from "../../../shared/ui/button";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { storedOrderLineRequest } from "../buy-lines";
import type { Order } from "../schemas";
import { SUPPLIER_ID, testOrder, testOrderLine, testSku } from "../testing/fixtures";
import { PROCUREMENT_MESSAGES } from "./adapters";
import { useDeleteOrder, useSaveOrder, type SaveOrderVars } from "./mutations";

function OrderForm({ vars, after }: { vars: () => SaveOrderVars; after: () => void }) {
  const save = useSaveOrder(after);
  return (
    <Button variant="primary" pending={save.pending} onClick={() => void save.run(vars())}>
      Lưu & chốt đơn
    </Button>
  );
}

const order = (qty: number): SaveOrderVars => ({
  supplierId: SUPPLIER_ID,
  expectedDate: null,
  lines: [{ sku_id: "sku-1", unit_id: "unit-hop", qty: toQty(qty), unit_price: toMoney(9000), discount_amount: toMoney(0) }],
  sells: [],
  confirm: true,
});

/** The order and its confirmation; the first create loses its answer. */
function serveOrder() {
  const creates: Array<{ key: string | null; body: unknown }> = [];
  const confirms: Array<string | null> = [];
  const po = { id: "po-7", doc_no: "PO-0007", version: 1, status: "draft" };
  server.use(
    http.post(apiUrl("/purchase-orders"), async ({ request }) => {
      creates.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
      return creates.length === 1 ? HttpResponse.error() : HttpResponse.json(po, { status: 201 });
    }),
    http.post(/\/purchase-orders\/po-7:confirm$/, ({ request }) => {
      confirms.push(request.headers.get("Idempotency-Key"));
      return HttpResponse.json({ ...po, version: 2, status: "confirmed" });
    }),
  );
  return { creates, confirms };
}

describe("useSaveOrder", () => {
  it("resends a create that got no answer under the same key, and confirms under a key derived from it", async () => {
    const { creates, confirms } = serveOrder();
    const after = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<OrderForm vars={() => order(12)} after={after} />);

    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(creates).toHaveLength(1));
    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(after).toHaveBeenCalled());

    const key = creates[0]?.key;
    expect(key).toBeTruthy();
    expect(creates[1]).toEqual(creates[0]);
    expect(confirms).toEqual([`${key}-confirm`]);
  });

  it("says the order was stored as first sent when its lines changed after a lost answer, and does not confirm it", async () => {
    const { creates, confirms } = serveOrder();
    const after = vi.fn();
    let qty = 12;
    const user = userEvent.setup();
    renderWithProviders(<OrderForm vars={() => order(qty)} after={after} />);

    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(creates).toHaveLength(1));
    qty = 24;
    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));

    expect(await screen.findByText(PROCUREMENT_MESSAGES.orderCreatedAsSent("PO-0007"))).toBeInTheDocument();
    expect(creates[1]).toEqual(creates[0]);
    expect(confirms).toEqual([]);
    expect(after).not.toHaveBeenCalled();
  });

  it("writes a sale price again when it was edited after an earlier attempt wrote it", async () => {
    const { creates } = serveOrder();
    const prices: Record<string, Array<string | null>> = { "sku-1": [], "sku-2": [] };
    server.use(
      http.post(/\/skus\/(sku-[12])\/sale-price$/, ({ request }) => {
        const sku = /skus\/(sku-[12])/.exec(request.url)?.[1] ?? "";
        const keys = prices[sku] ?? [];
        keys.push(request.headers.get("Idempotency-Key"));
        // The second SKU's price fails once, after the first one's was written.
        return sku === "sku-2" && keys.length === 1 ? HttpResponse.json({ code: "internal_error" }, { status: 500 }) : HttpResponse.json({});
      }),
    );
    const after = vi.fn();
    let retail = "18000";
    const vars = (): SaveOrderVars => ({
      ...order(12),
      sells: [
        { sku: testSku({ wholesale_price: null }), retail: toMoney(retail) },
        { sku: testSku({ id: "sku-2", wholesale_price: null }), retail: toMoney("20000") },
      ],
    });
    const user = userEvent.setup();
    renderWithProviders(<OrderForm vars={vars} after={after} />);

    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(creates).toHaveLength(1));
    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));
    expect(await screen.findByText(/nhưng chưa cập nhật giá bán/)).toBeInTheDocument();
    retail = "19000";
    await user.click(screen.getByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(after).toHaveBeenCalled());

    const key = creates[0]?.key ?? "";
    const priceKey = (sku: string, price: string, n: number) => `${key}:sale-price:${sku}:retail:general:purchase_order:${price}:${n}`;
    expect(prices["sku-1"]).toEqual([priceKey("sku-1", "18000", 1), priceKey("sku-1", "19000", 2)]);
    expect(prices["sku-2"]).toEqual([priceKey("sku-2", "20000", 1), priceKey("sku-2", "20000", 1)]);
  });
});

interface DraftCall {
  method: string;
  path: string;
  ifMatch: string | null;
  body: unknown;
}

/**
 * A draft PO the server holds at version 3: every write is refused unless it names the current
 * version, and answers with the next one. `changedElsewhere` stands for an edit in another tab.
 */
function serveDraft() {
  const line = testOrderLine({ qty: "2" });
  const draft = testOrder({ status: "draft", version: 3, lines: [line] });
  let current: Order = draft;
  const calls: DraftCall[] = [];
  const write = async (request: Request, change: (body: Record<string, unknown>) => Partial<Order>) => {
    const body = (await request.json()) as Record<string, unknown>;
    const ifMatch = request.headers.get("If-Match");
    calls.push({ method: request.method, path: new URL(request.url).pathname.replace(/^.*\/purchase-orders\/po-1/, ""), ifMatch, body });
    if (ifMatch !== etagOf(current.version)) return apiError(412, "precondition_failed");
    current = { ...current, ...change(body), version: current.version + 1 };
    return HttpResponse.json(current);
  };
  server.use(
    http.get(apiUrl("/purchase-orders/po-1"), () => {
      calls.push({ method: "GET", path: "", ifMatch: null, body: null });
      return HttpResponse.json(current);
    }),
    http.patch(apiUrl("/purchase-orders/po-1"), ({ request }) => write(request, (b) => b as Partial<Order>)),
    http.put(apiUrl("/purchase-orders/po-1/lines"), ({ request }) => write(request, () => ({}))),
    http.post(/\/purchase-orders\/po-1:confirm$/, ({ request }) => write(request, () => ({ status: "confirmed" }))),
  );
  return {
    draft,
    calls,
    changedElsewhere: () => {
      current = { ...current, version: current.version + 1 };
    },
    /** The draft's lines as stored, with the first one's quantity changed. */
    lines: (qty: string = line.qty) => [{ ...storedOrderLineRequest(line), qty: toQty(qty) }],
  };
}

describe("useSaveOrder on a draft", () => {
  const edit = (draft: Order, over: Partial<SaveOrderVars>): SaveOrderVars => ({
    order: draft,
    supplierId: draft.supplier_party_id,
    expectedDate: draft.expected_date ?? null,
    lines: (draft.lines ?? []).map(storedOrderLineRequest),
    sells: [],
    confirm: false,
    ...over,
  });
  const save = async (vars: () => SaveOrderVars, after = vi.fn()) => {
    const user = userEvent.setup();
    renderWithProviders(<OrderForm vars={vars} after={after} />);
    await user.click(await screen.findByRole("button", { name: "Lưu & chốt đơn" }));
    return { user, after };
  };

  it("writes nothing but the confirmation when nothing changed, a cleared expected date included", async () => {
    const api = serveDraft();
    const { after } = await save(() => edit(api.draft, { expectedDate: null, confirm: true }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(api.calls.map((c) => [c.method, c.path, c.ifMatch])).toEqual([["POST", ":confirm", etagOf(3)]]);
  });

  it("sends only the header fields that changed", async () => {
    const api = serveDraft();
    const { after } = await save(() => edit(api.draft, { expectedDate: "2026-10-20" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(api.calls).toEqual([{ method: "PATCH", path: "", ifMatch: etagOf(3), body: { expected_date: "2026-10-20" } }]);
  });

  it("sends only the lines when only they changed", async () => {
    const api = serveDraft();
    const { after } = await save(() => edit(api.draft, { lines: api.lines("5") }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(api.calls).toEqual([{ method: "PUT", path: "/lines", ifMatch: etagOf(3), body: { lines: api.lines("5") } }]);
  });

  it("writes the header, the lines and the confirmation each at the version the previous answer left", async () => {
    const api = serveDraft();
    const { after } = await save(() => edit(api.draft, { supplierId: "sup-2", lines: api.lines("5"), confirm: true }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(api.calls.map((c) => [c.method, c.path, c.ifMatch, c.body])).toEqual([
      ["PATCH", "", etagOf(3), { supplier_party_id: "sup-2" }],
      ["PUT", "/lines", etagOf(4), { lines: api.lines("5") }],
      ["POST", ":confirm", etagOf(5), {}],
    ]);
  });

  it("stores nothing over a draft changed elsewhere, and reads it again before the next attempt writes", async () => {
    const api = serveDraft();
    api.changedElsewhere();
    const { user, after } = await save(() => edit(api.draft, { lines: api.lines("5") }));
    expect(await screen.findByText(STALE_DATA_MESSAGE)).toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(api.calls.map((c) => [c.method, c.path, c.ifMatch])).toEqual([
      ["PUT", "/lines", etagOf(3)],
      ["GET", "", null],
      ["PUT", "/lines", etagOf(4)],
    ]);
  });
  it("says it does not know whether a write without an answer was stored, and checks before writing again", async () => {
    const api = serveDraft();
    let lose = true;
    server.use(
      http.patch(apiUrl("/purchase-orders/po-1"), () => {
        if (!lose) return undefined;
        lose = false;
        return HttpResponse.error();
      }),
    );
    const { user, after } = await save(() => edit(api.draft, { expectedDate: "2026-10-20" }));
    expect(await screen.findByText(/hay chưa\. Bấm lại để kiểm tra và lưu tiếp\./)).toHaveTextContent("Chưa rõ đã lưu PO-0001 hay chưa.");

    await user.click(screen.getByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(api.calls.map((c) => [c.method, c.ifMatch, c.body])).toEqual([
      ["GET", null, null],
      ["PATCH", etagOf(3), { expected_date: "2026-10-20" }],
    ]);
  });
});

function DeleteButton({ after }: { after: () => void }) {
  const remove = useDeleteOrder(after);
  return (
    <Button variant="danger" pending={remove.pending} onClick={() => void remove.run({ id: "po-1", docNo: "PO-0001", version: 3 })}>
      Xóa nháp
    </Button>
  );
}

describe("useDeleteOrder", () => {
  it("deletes the draft at the version shown and names it", async () => {
    const deletes: Array<string | null> = [];
    server.use(
      http.delete(apiUrl("/purchase-orders/po-1"), ({ request }) => {
        deletes.push(request.headers.get("If-Match"));
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const after = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<DeleteButton after={after} />);

    await user.click(await screen.findByRole("button", { name: "Xóa nháp" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(deletes).toEqual([etagOf(3)]);
    expect(await screen.findByText(/Đã xóa đơn nhập nháp/)).toHaveTextContent("Đã xóa đơn nhập nháp PO-0001.");
  });
});
