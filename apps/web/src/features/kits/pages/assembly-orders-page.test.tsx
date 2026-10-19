import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { KIT_UNITS, testAssemblyDetail, testAssemblyOrder } from "../testing/fixtures";

const draft = testAssemblyOrder({ id: "asm-2", doc_no: "LD-0002", status: "draft", completed_qty: "0", invoice_id: null });
const open = testAssemblyOrder({ invoice_id: "inv-1" });
// 18 hộp still needed and only 10 held: the list flags the missing component.
const openDetail = testAssemblyDetail({
  invoice_id: "inv-1",
  items: [{ id: "ai-1", sku_id: "sku-1", sku_code: "SP001", sku_name: "Bánh quy bơ", planned_qty: "30", consumed_qty: "12", remaining_qty: "18", reserved_qty: "10" }],
});

/** Two orders (a draft and one under way for invoice SP-0007) and a recorder of reservations. */
function ordersServer() {
  const reserved: string[] = [];
  server.use(
    http.get(apiUrl("/assembly-orders"), () => HttpResponse.json({ data: [draft, open], next_cursor: null })),
    http.get(apiUrl("/assembly-orders/asm-1"), () => HttpResponse.json(openDetail)),
    http.post(/\/assembly-orders\/asm-2:reserve$/, () => {
      reserved.push("asm-2");
      return HttpResponse.json(testAssemblyDetail({ id: "asm-2", doc_no: "LD-0002", status: "reserved", completed_qty: "0", postings: [] }));
    }),
    http.get(apiUrl("/invoices"), ({ request }) =>
      HttpResponse.json({
        data: new URL(request.url).searchParams.get("status") === "confirmed" ? [{ id: "inv-1", doc_no: "SP-0007", invoice_date: "2026-10-10", status: "confirmed" }] : [],
        next_cursor: null,
      }),
    ),
    http.get(apiUrl("/lots/lot-kit-1/genealogy"), () =>
      HttpResponse.json({
        lot_id: "lot-kit-1",
        components: [
          {
            parent_lot_id: "lot-kit-1",
            parent_lot_no: "GQ2601",
            child_lot_id: "lot-1",
            child_lot_no: "L2601",
            sku_id: "sku-1",
            sku_code: "SP001",
            expiry_date: "2027-01-31",
            qty: "12",
            order_doc_no: "LD-0001",
          },
        ],
        used_in: [],
      }),
    ),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: KIT_UNITS, next_cursor: null })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
  );
  return reserved;
}

describe("Lệnh đóng giỏ page", () => {
  it("reserves a draft order's components and flags what an open order is short of", async () => {
    const reserved = ordersServer();
    const user = userEvent.setup();
    renderApp("/gift-baskets/assembly-orders");

    const table = await screen.findByRole("table", { name: "Lệnh đóng giỏ" });
    expect(await within(table).findByText("Thiếu: Bánh quy bơ")).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Ghi nhận hoàn thành" })).toBeInTheDocument();
    await user.click(within(table).getByRole("button", { name: "Chốt & giữ NL" }));

    await waitFor(() => expect(reserved).toEqual(["asm-2"]));
    expect(await screen.findByText("Đã chốt lệnh và giữ nguyên liệu.")).toBeInTheDocument();
  });

  it("gives each order its own reservation intent, even after a failed attempt on another", async () => {
    const other = testAssemblyOrder({ id: "asm-3", doc_no: "LD-0003", status: "draft", completed_qty: "0", invoice_id: null });
    const keys: { id: string; key: string | null }[] = [];
    server.use(
      http.get(apiUrl("/assembly-orders"), () => HttpResponse.json({ data: [draft, other], next_cursor: null })),
      http.get(apiUrl("/invoices"), () => HttpResponse.json({ data: [], next_cursor: null })),
      http.post(/\/assembly-orders\/asm-\d:reserve$/, ({ request }) => {
        const id = /asm-\d/.exec(request.url)?.[0] ?? "";
        keys.push({ id, key: request.headers.get("Idempotency-Key") });
        if (keys.length === 1) return HttpResponse.error();
        return HttpResponse.json(testAssemblyDetail({ id, status: "reserved", completed_qty: "0", postings: [] }));
      }),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/assembly-orders");

    const table = await screen.findByRole("table", { name: "Lệnh đóng giỏ" });
    const reserveOf = (docNo: string) => within(within(table).getByText(docNo).closest("tr") as HTMLElement).getByRole("button", { name: "Chốt & giữ NL" });
    await user.click(reserveOf("LD-0002"));
    await waitFor(() => expect(keys).toHaveLength(1));
    await waitFor(() => expect(reserveOf("LD-0003")).toBeEnabled());
    await user.click(reserveOf("LD-0003"));
    await waitFor(() => expect(keys).toHaveLength(2));
    await waitFor(() => expect(reserveOf("LD-0002")).toBeEnabled());
    await user.click(reserveOf("LD-0002"));
    await waitFor(() => expect(keys).toHaveLength(3));

    expect(keys.map((k) => k.id)).toEqual(["asm-2", "asm-3", "asm-2"]);
    expect(new Set(keys.map((k) => k.key)).size).toBe(3);
  });

  it("opens an order with its postings and the component lots they consumed", async () => {
    ordersServer();
    const user = userEvent.setup();
    renderApp("/gift-baskets/assembly-orders");

    await user.click(await screen.findByRole("button", { name: "LD-0001" }));

    const drawer = await screen.findByRole("dialog", { name: "Lệnh đóng LD-0001" });
    expect(within(drawer).getByText("12/10/2026 · 4 giỏ · lô giỏ GQ2601")).toBeInTheDocument();
    const lots = within(drawer).getByRole("table", { name: "Lô thành phần của lô giỏ GQ2601" });
    expect(within(lots).getByText("L2601")).toBeInTheDocument();
    expect(await within(lots).findByText("31/01/2027")).toBeInTheDocument();
    expect(await within(drawer).findByText(/phiếu giao của đơn SP-0007/)).toBeInTheDocument();
  });

  it("opens an order in the URL that is older than the loaded pages, found by its number", async () => {
    ordersServer();
    server.use(
      http.get(apiUrl("/assembly-orders"), ({ request }) =>
        HttpResponse.json({ data: new URL(request.url).searchParams.get("q") === "LD-0001" ? [open] : [draft], next_cursor: "older" }),
      ),
    );
    renderApp("/gift-baskets/assembly-orders?doc=LD-0001");

    expect(await screen.findByRole("dialog", { name: "Lệnh đóng LD-0001" })).toBeInTheDocument();
  });

  it("says when the order in the URL does not exist", async () => {
    ordersServer();
    renderApp("/gift-baskets/assembly-orders?doc=LD-0099");

    const drawer = await screen.findByRole("dialog", { name: "Lệnh đóng" });
    expect(within(drawer).getByText("Không tìm thấy LD-0099.")).toBeInTheDocument();
  });
});
