import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { LOAD_MORE_LABEL } from "../../../shared/ui/list-footer";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testLot } from "../../inventory/testing/fixtures";
import { testAssemblyDetail, testAssemblyOrder } from "../testing/fixtures";

const kitLot = testLot({ id: "lot-kit-1", sku_id: "sku-kit", sku_code: "GQAK", sku_name: "Giỏ An Khang", unit_name: "giỏ", lot_no: "GQ2601", expiry_date: "2027-01-31", qty: "4" });

function progressServer() {
  server.use(
    http.get(apiUrl("/kits/progress"), () =>
      HttpResponse.json({
        data: [
          { product_id: "kit-1", name: "Giỏ An Khang", sku_id: "sku-kit", sku_code: "GQAK", planned_qty: "10", completed_qty: "4", disassembled_qty: "1", on_hand: "3", invoice_demand: "8", open_orders: 1 },
        ],
        next_cursor: null,
      }),
    ),
    http.get(apiUrl("/lots"), () => HttpResponse.json({ data: [kitLot], next_cursor: null })),
    http.get(apiUrl("/assembly-orders"), () => HttpResponse.json({ data: [testAssemblyOrder(), testAssemblyOrder({ id: "asm-2", doc_no: "LD-0002", status: "draft", completed_qty: "0" })], next_cursor: null })),
    http.get(apiUrl("/assembly-orders/asm-1"), () => HttpResponse.json(testAssemblyDetail())),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.get(apiUrl("/invoices"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.get(apiUrl("/lots/lot-kit-1/genealogy"), () => HttpResponse.json({ lot_id: "lot-kit-1", components: [], used_in: [] })),
  );
}

describe("Tiến độ & truy xuất lô page", () => {
  it("shows what invoices still need, the finished lots, and traces a kit lot back to its order", async () => {
    progressServer();
    const user = userEvent.setup();
    const { router } = renderApp("/gift-baskets/progress");

    const demand = await screen.findByRole("table", { name: "Nhu cầu giỏ theo đơn" });
    const row = (await within(demand).findByText("Giỏ An Khang")).closest("tr") as HTMLElement;
    // 8 ordered, 4 made less 1 taken apart, 3 on hand: 5 more to make.
    expect(within(row).getAllByRole("cell").map((c) => c.textContent)).toEqual(expect.arrayContaining(["8", "3", "5"]));
    const lots = await screen.findByRole("table", { name: "Tồn giỏ thành phẩm theo lô" });
    expect(within(lots).getByText("GQ2601")).toBeInTheDocument();

    const trace = await screen.findByRole("table", { name: "Truy xuất lô thành phần" });
    expect(await within(trace).findByText("L2601", { exact: false })).toBeInTheDocument();
    expect(within(trace).queryByText("LD-0002")).not.toBeInTheDocument();
    await user.click(within(trace).getByRole("button", { name: "LD-0001" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/gift-baskets/assembly-orders"));
    expect(router.state.location.search).toBe("?doc=LD-0001");
  });

  it("pages older orders into the lot trace", async () => {
    progressServer();
    server.use(
      http.get(apiUrl("/assembly-orders"), ({ request }) =>
        new URL(request.url).searchParams.get("cursor") === null
          ? HttpResponse.json({ data: [testAssemblyOrder({ id: "asm-2", doc_no: "LD-0002", status: "draft", completed_qty: "0" })], next_cursor: "older" })
          : HttpResponse.json({ data: [testAssemblyOrder()], next_cursor: null }),
      ),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/progress");

    await user.click(await screen.findByRole("button", { name: LOAD_MORE_LABEL }));
    const trace = await screen.findByRole("table", { name: "Truy xuất lô thành phần" });
    expect(await within(trace).findByText("L2601", { exact: false })).toBeInTheDocument();
  });
});
