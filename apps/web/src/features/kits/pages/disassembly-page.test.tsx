import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testLot } from "../../inventory/testing/fixtures";
import { UNIT_HOP } from "../../procurement/testing/fixtures";
import { testAssemblyDetail, testAssemblyOrder, testDisassembly, testKit, testPosting } from "../testing/fixtures";

const kitLot = testLot({ id: "lot-kit-1", sku_id: "sku-kit", sku_code: "GQAK", sku_name: "Giỏ An Khang", unit_name: "giỏ", lot_no: "GQ2601", qty: "4" });
// Each kit holds 3 hộp of biscuits and 1 rattan basket.
const recipe = {
  ...testKit().latest,
  status: "active",
  items: [
    { id: "ri-1", line_no: 1, kind: "goods", sku_id: "sku-1", unit_id: UNIT_HOP, qty: "3", base_qty: "3", flavors: [] },
    { id: "ri-2", line_no: 2, kind: "basket", sku_id: "sku-basket", unit_id: UNIT_HOP, qty: "1", base_qty: "1", flavors: [] },
  ],
};
const posting = testPosting({
  items: [
    { id: "pi-1", order_item_id: "ai-1", sku_id: "sku-1", lot_id: "lot-1", lot_no: "L2601", consumed_qty: "12", scrap_qty: "0", unit_cost: "143333" },
    { id: "pi-2", order_item_id: "ai-2", sku_id: "sku-basket", lot_id: "lot-b1", lot_no: "M2601", consumed_qty: "4", scrap_qty: "0", unit_cost: "30000" },
  ],
});

/** The kit lot's link to one of its component lots, naming the order that assembled it. */
const genealogy = {
  lot_id: "lot-kit-1",
  components: [
    { parent_lot_id: "lot-kit-1", parent_lot_no: "GQ2601", child_lot_id: "lot-1", child_lot_no: "L2601", sku_id: "sku-1", sku_code: "SP001", color_id: null, expiry_date: null, qty: "12", order_doc_no: "LD-0001" },
  ],
  used_in: [],
};

/** One recorded disassembly, the kit lot it can still take apart, and a recorder of new ones. */
function disassemblyServer() {
  const bodies: unknown[] = [];
  server.use(
    http.get(apiUrl("/disassemblies"), () => HttpResponse.json({ data: [testDisassembly(), testDisassembly({ id: "dis-2", doc_no: "TG-0002", lines: [{ id: "dl-2", sku_id: "sku-basket", source_lot_id: "lot-b1", lot_no: "M2601", return_qty: "0", damaged_qty: "1", unit_cost: "30000" }] })], next_cursor: null })),
    http.get(apiUrl("/assembly-orders"), () => HttpResponse.json({ data: [testAssemblyOrder()], next_cursor: null })),
    http.get(apiUrl("/lots/lot-kit-1/genealogy"), () => HttpResponse.json(genealogy)),
    http.get(apiUrl("/assembly-orders/asm-1"), () => HttpResponse.json(testAssemblyDetail({ postings: [posting] }))),
    http.get(apiUrl("/lots"), () => HttpResponse.json({ data: [kitLot], next_cursor: null })),
    http.get(apiUrl("/kits/kit-1/recipes/1"), () => HttpResponse.json(recipe)),
    http.get(apiUrl("/reasons"), () =>
      HttpResponse.json({
        data: [
          { id: "rsn-1", kind: "damage", name: "Hỏng khi tháo", active: true },
          { id: "rsn-9", kind: "count_variance", name: "Chênh lệch kiểm kê", active: true },
        ],
      }),
    ),
    http.post(apiUrl("/assembly-orders/asm-1/disassemblies"), async ({ request }) => {
      bodies.push(await request.json());
      return HttpResponse.json(testDisassembly({ id: "dis-3", doc_no: "TG-0003" }), { status: 201 });
    }),
  );
  return bodies;
}

describe("Tháo giỏ page", () => {
  it("lists disassemblies with whether their packaging came back", async () => {
    disassemblyServer();
    renderApp("/gift-baskets/disassembly");

    const table = await screen.findByRole("table", { name: "Phiếu tháo giỏ" });
    const first = within(table).getByText("TG-0001").closest("tr") as HTMLElement;
    const second = within(table).getByText("TG-0002").closest("tr") as HTMLElement;
    expect(within(first).getByText("Nhập lại")).toBeInTheDocument();
    expect(within(second).getByText("Hỏng – không nhập lại")).toBeInTheDocument();
    expect(await within(first).findByText("Giỏ An Khang")).toBeInTheDocument();
  });

  it("searches the server for a linked disassembly by its number and shows every one again on request", async () => {
    disassemblyServer();
    const queries: URLSearchParams[] = [];
    const listed = [testDisassembly(), testDisassembly({ id: "dis-2", doc_no: "TG-0002" })];
    server.use(
      http.get(apiUrl("/disassemblies"), ({ request }) => {
        const query = new URL(request.url).searchParams;
        queries.push(query);
        // The search also matches lot and order numbers, so TG-0002 brings TG-0001 along here.
        return HttpResponse.json({ data: query.has("q") ? listed : [...listed, testDisassembly({ id: "dis-3", doc_no: "TG-0003" })], next_cursor: null });
      }),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/disassembly?doc=TG-0002");

    const table = await screen.findByRole("table", { name: "Phiếu tháo giỏ" });
    expect(await within(table).findByText("TG-0002")).toBeInTheDocument();
    expect(within(table).queryByText("TG-0001")).not.toBeInTheDocument();
    expect(queries.map((q) => q.get("q"))).toEqual(["TG-0002"]);
    expect(queries.some((q) => q.has("cursor"))).toBe(false);

    await user.click(screen.getByRole("button", { name: "Xem tất cả" }));
    expect(await screen.findByText("TG-0003")).toBeInTheDocument();
    expect(within(screen.getByRole("table", { name: "Phiếu tháo giỏ" })).getByText("TG-0001")).toBeInTheDocument();
    expect(queries.at(-1)?.has("q")).toBe(false);
  });

  it("says when no disassembly has the linked number", async () => {
    disassemblyServer();
    server.use(http.get(apiUrl("/disassemblies"), () => HttpResponse.json({ data: [], next_cursor: null })));
    renderApp("/gift-baskets/disassembly?doc=TG-0404");
    expect(await screen.findByText("Không tìm thấy TG-0404.")).toBeInTheDocument();
  });

  it("takes kits apart within the lot's stock, with a reason, writing the damaged basket off", async () => {
    const bodies = disassemblyServer();
    const user = userEvent.setup();
    renderApp("/gift-baskets/disassembly");

    await user.click(await screen.findByRole("button", { name: "Tháo giỏ" }));
    const dialog = await screen.findByRole("dialog", { name: "Tháo giỏ quà" });
    expect(await within(dialog).findByText("Có thể tháo: 4")).toBeInTheDocument();
    const reason = within(dialog).getByRole("combobox", { name: "Lý do tháo giỏ" });
    expect(within(reason).queryByRole("option", { name: "Chênh lệch kiểm kê" })).not.toBeInTheDocument();

    const qty = within(dialog).getByRole("textbox", { name: /^Số giỏ tháo/ });
    await user.clear(qty);
    await user.type(qty, "5");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ tháo giỏ" }));
    expect(await screen.findByText("Chỉ còn 4 giỏ có thể tháo.")).toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "2");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ tháo giỏ" }));
    expect(await screen.findByText("Chọn lý do tháo giỏ.")).toBeInTheDocument();
    expect(bodies).toEqual([]);

    await user.selectOptions(reason, "rsn-1");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ tháo giỏ" }));

    await waitFor(() =>
      expect(bodies).toEqual([{ kit_lot_id: "lot-kit-1", qty: "2", reason_id: "rsn-1", damaged: [{ sku_id: "sku-basket", qty: "2", source_lot_id: "lot-b1" }] }]),
    );
    expect(await screen.findByText("Đã tháo giỏ, thành phần về kho theo lô gốc.")).toBeInTheDocument();
  });

  it("takes apart a kit lot past the first page whose order is older than the order list's first page", async () => {
    const bodies = disassemblyServer();
    const orderQueries: (string | null)[] = [];
    const otherLot = testLot({ id: "lot-kit-0", sku_id: "sku-kit", sku_name: "Giỏ An Khang", unit_name: "giỏ", lot_no: "GQ2612", qty: "1" });
    server.use(
      http.get(apiUrl("/lots"), ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        return HttpResponse.json(cursor === null ? { data: [otherLot], next_cursor: "p2" } : { data: [kitLot], next_cursor: null });
      }),
      http.get(apiUrl("/assembly-orders"), ({ request }) => {
        const q = new URL(request.url).searchParams.get("q");
        orderQueries.push(q);
        return HttpResponse.json({ data: q === "LD-0001" ? [testAssemblyOrder()] : [], next_cursor: null });
      }),
    );
    const user = userEvent.setup();
    renderApp("/gift-baskets/disassembly");

    await user.click(await screen.findByRole("button", { name: "Tháo giỏ" }));
    const dialog = await screen.findByRole("dialog", { name: "Tháo giỏ quà" });
    const lot = within(dialog).getByRole("combobox", { name: "Giỏ" });
    await within(lot).findByRole("option", { name: /lô GQ2601/ });
    await user.selectOptions(lot, "lot-kit-1");
    await user.selectOptions(within(dialog).getByRole("combobox", { name: "Lý do tháo giỏ" }), "rsn-1");
    await user.click(within(dialog).getByRole("checkbox", { name: /bị hỏng khi tháo/ }));
    await waitFor(async () => {
      if (bodies.length === 0) await user.click(within(dialog).getByRole("button", { name: "Ghi sổ tháo giỏ" }));
      expect(bodies).toEqual([{ kit_lot_id: "lot-kit-1", qty: "1", reason_id: "rsn-1" }]);
    });
    expect(orderQueries).toContain("LD-0001");
  });
});
