import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { UNIT_THUNG, testSku } from "../../procurement/testing/fixtures";
import { COLOR_BLUE, COLOR_RED, testColorStock, testStockRow } from "../testing/fixtures";

const plain = testStockRow();
const colored = testStockRow({
  sku_id: "sku-2",
  code: "SP002",
  name: "Kẹo dẻo",
  on_hand: "30",
  available: "30",
  sellable: "30",
  colors: [testColorStock(), testColorStock({ color_id: COLOR_BLUE, color_code: "xanh", color_name: "Xanh", on_hand: "20", sellable: "20" })],
});
// Ribbon tagged Đỏ for most of its stock, with a few rolls not tagged with any color yet.
const mixed = testStockRow({
  sku_id: "sku-3",
  code: "SP003",
  name: "Ruy băng",
  on_hand: "15",
  available: "15",
  sellable: "15",
  colors: [testColorStock(), testColorStock({ color_id: null, color_code: null, color_name: null, on_hand: "5", sellable: "5", value: "50000" })],
});
const skus = [testSku(), testSku({ id: "sku-2", code: "SP002", display_name: "Kẹo dẻo" }), testSku({ id: "sku-3", code: "SP003", display_name: "Ruy băng" })];

const COLORS = [
  { id: COLOR_RED, code: "do", name: "Đỏ", sort_order: 1, active: true },
  { id: COLOR_BLUE, code: "xanh", name: "Xanh", sort_order: 2, active: true },
];

/** The stock page's lookups; returns the query of every `GET /stock`, filtered the way the server does. */
function stockServer() {
  const queries: URLSearchParams[] = [];
  server.use(
    http.get(apiUrl("/stock"), ({ request }) => {
      const query = new URL(request.url).searchParams;
      queries.push(query);
      const colorId = query.get("color_id");
      const coloredOnly = query.get("colored_only") === "true";
      const uncolored = query.get("uncolored") === "true";
      if (uncolored && colorId !== null) return apiError(422, "validation_failed", { fields: { color_id: "conflicts_with_uncolored" } });
      if (uncolored && coloredOnly) return apiError(422, "validation_failed", { fields: { colored_only: "conflicts_with_uncolored" } });
      const rows = [plain, colored, mixed].filter(
        (r) =>
          (!coloredOnly || r.colors.some((c) => c.color_id !== null)) &&
          (colorId === null || r.colors.some((c) => c.color_id === colorId)) &&
          (!uncolored || r.colors.some((c) => c.color_id === null)),
      );
      return HttpResponse.json({ data: rows, next_cursor: null });
    }),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: COLORS })),
    http.get(apiUrl("/stock/sku-1"), () => HttpResponse.json({ ...plain, lots: [] })),
    http.get(apiUrl("/skus"), ({ request }) => {
      const q = new URL(request.url).searchParams.get("q");
      return HttpResponse.json({ data: q === null ? skus : skus.filter((s) => s.display_name.toLowerCase().includes(q.toLowerCase())), next_cursor: null });
    }),
    http.get(apiUrl("/categories"), () => HttpResponse.json({ data: [] })),
    http.get(apiUrl("/reasons"), () => HttpResponse.json({ data: [{ id: "rsn-1", kind: "damage", name: "Bể vỡ khi vận chuyển", active: true }] })),
  );
  return queries;
}

describe("Tồn kho page", () => {
  it("narrows to the color picked in the URL with its rows open, and back to every product", async () => {
    const queries = stockServer();
    const user = userEvent.setup();
    const { router } = renderApp("/inventory/stock?color=xanh");

    expect(await screen.findByRole("button", { name: "Thu gọn màu của Kẹo dẻo" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.queryByText("Bánh quy bơ")).not.toBeInTheDocument();
    // The server filters every page, not only the loaded ones.
    await waitFor(() => expect(queries.some((q) => q.get("color_id") === COLOR_BLUE)).toBe(true));
    await user.click(screen.getByRole("button", { name: "Mọi màu" }));
    await waitFor(() => expect(router.state.location.search).toBe(""));
    expect(await screen.findByText("Bánh quy bơ")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Xem chi tiết màu của Kẹo dẻo" })).toHaveAttribute("aria-expanded", "false");
  });

  it("asks the server for colored products only when the scope says so", async () => {
    const queries = stockServer();
    const user = userEvent.setup();
    const { router } = renderApp("/inventory/stock");

    expect(await screen.findByText("Bánh quy bơ")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Chỉ hàng có màu" }));
    await waitFor(() => expect(router.state.location.search).toBe("?only=co"));
    await waitFor(() => expect(screen.queryByText("Bánh quy bơ")).not.toBeInTheDocument());
    expect(queries.at(-1)?.get("colored_only")).toBe("true");
    expect(screen.getByText("Kẹo dẻo")).toBeInTheDocument();
  });

  it("asks the server for the stock without a color", async () => {
    const queries = stockServer();
    const user = userEvent.setup();
    const { router } = renderApp("/inventory/stock");

    expect(await screen.findByText("Kẹo dẻo")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Chưa gán màu" }));
    await waitFor(() => expect(router.state.location.search).toBe("?color=none"));
    await waitFor(() => expect(screen.queryByText("Kẹo dẻo")).not.toBeInTheDocument());
    const last = queries.at(-1);
    expect(last?.get("uncolored")).toBe("true");
    expect(last?.has("colored_only")).toBe(false);
    expect(last?.has("color_id")).toBe(false);
    expect(screen.getByText("Ruy băng")).toBeInTheDocument();
  });

  it("keeps colored products with untagged stock without sending the server both filters", async () => {
    const queries = stockServer();
    renderApp("/inventory/stock?color=none&only=co");

    expect(await screen.findByText("Ruy băng")).toBeInTheDocument();
    // The server answers this pair with conflicts_with_uncolored, so "colored only" is applied here.
    expect(queries.every((q) => q.get("uncolored") === "true" && !q.has("colored_only") && !q.has("color_id"))).toBe(true);
    expect(screen.queryByText("Bánh quy bơ")).not.toBeInTheDocument();
    expect(screen.queryByText("Kẹo dẻo")).not.toBeInTheDocument();
    expect(screen.queryByText(/chỉ lọc trên các dòng đã tải/)).not.toBeInTheDocument();
  });

  it("writes damaged stock down with a reason from the adjustment dialog", async () => {
    stockServer();
    let body: unknown = null;
    server.use(
      http.post(apiUrl("/stock-adjustments"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "adj-1", doc_no: "DC-0002", kind: "damage", business_date: "2026-10-12", status: "posted" });
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/stock");

    await user.click(await screen.findByRole("button", { name: "Điều chỉnh kho" }));
    const dialog = await screen.findByRole("dialog", { name: "Điều chỉnh giảm kho" });
    await user.type(within(dialog).getByRole("combobox", { name: "Hàng" }), "bánh");
    await user.click(await screen.findByRole("option", { name: /Bánh quy bơ/ }));
    await user.type(within(dialog).getByRole("textbox", { name: "Số lượng (hộp)" }), "30");
    await user.selectOptions(await within(dialog).findByRole("combobox", { name: /^Lý do/ }), "rsn-1");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ điều chỉnh" }));
    expect(await screen.findByText(/Vượt tồn bán được \(24 hộp\)\./)).toBeInTheDocument();

    const qty = within(dialog).getByRole("textbox", { name: "Số lượng (hộp)" });
    await user.clear(qty);
    await user.type(qty, "2");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ điều chỉnh" }));
    expect(await screen.findByText("Đã ghi điều chỉnh.")).toBeInTheDocument();
    expect(body).toEqual({ kind: "damage", reason_id: "rsn-1", lines: [{ sku_id: "sku-1", qty: "2" }] });
  });

  it("sets a unit's own price from the sell-price dialog", async () => {
    stockServer();
    const bodies: unknown[] = [];
    server.use(
      http.get(apiUrl("/price-lists"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/skus/sku-1/price-history"), () => HttpResponse.json({ data: [], next_cursor: null })),
      http.post(apiUrl("/skus/sku-1/sale-price"), async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ data: [] });
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/stock");

    await user.click(await screen.findByRole("button", { name: "Sửa giá – Bánh quy bơ" }));
    const dialog = await screen.findByRole("dialog", { name: "Sửa giá bán · Bánh quy bơ" });
    const carton = within(dialog).getByLabelText("Giá bán lẻ / thùng");
    expect(carton).toHaveValue("");
    expect(carton).toHaveAttribute("placeholder", "180.000 tự tính");
    expect(dialog).toHaveTextContent("1 thùng = 12 hộp");

    await user.type(carton, "170000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu giá" }));

    await waitFor(() => expect(bodies).toEqual([{ kind: "retail", price: "170000", unit_id: UNIT_THUNG, source: "inventory" }]));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Sửa giá bán · Bánh quy bơ" })).not.toBeInTheDocument());
  });
});
