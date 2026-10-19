import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import { Button } from "../../../shared/ui/button";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { SUPPLIER_ID, testSku } from "../testing/fixtures";
import { PROCUREMENT_MESSAGES } from "./adapters";
import { useSaveOrder, type SaveOrderVars } from "./mutations";

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
