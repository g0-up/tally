import { screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { testStockRow } from "../testing/fixtures";

const qd = (qty: string, display: string) => ({ qty, display });

/** A thùng-of-12 SKU whose card shows quantities in thùng + hộp. */
function cardServer(reconciled: boolean) {
  const queries: URLSearchParams[] = [];
  server.use(
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
    http.get(apiUrl("/stock/sku-1"), () => HttpResponse.json({ ...testStockRow({ on_hand: "28" }), lots: [] })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
    http.get(apiUrl("/stock-card"), ({ request }) => {
      const q = new URL(request.url).searchParams;
      queries.push(q);
      return HttpResponse.json({
        sku_id: "sku-1",
        sku_code: "SP001",
        sku_name: "Bánh quy bơ",
        base_unit: "hộp",
        from: q.get("from"),
        to: q.get("to"),
        opening: qd("4", "4 hộp"),
        in: qd("24", "2 thùng"),
        out: qd("0", "0"),
        closing: qd("28", "2 thùng 4 hộp"),
        on_hand: qd(reconciled ? "28" : "27", reconciled ? "2 thùng 4 hộp" : "2 thùng 3 hộp"),
        reconciled,
        rows: [{ id: "mv-1", lot_no: null, color_id: null, unit_cost: "10000", move_type: "receipt", doc_no: "PN-0001", business_date: q.get("to"), in: qd("24", "2 thùng"), out: qd("0", "0"), balance: qd("28", "2 thùng 4 hộp") }],
        next_cursor: null,
      });
    }),
  );
  return queries;
}

describe("Thẻ kho page", () => {
  it("shows the moves in mixed units with the base quantity and the reconciliation with stock on hand", async () => {
    const queries = cardServer(true);
    renderApp("/inventory/stock-card?sku=sku-1");

    const row = (await screen.findByText("Nhập mua")).closest("tr") as HTMLElement;
    expect(within(row).getByText("2 thùng")).toBeInTheDocument();
    expect(within(row).getByText("= 28 hộp")).toBeInTheDocument();
    expect(screen.getByText("Đã khớp sổ và tồn")).toBeInTheDocument();
    expect(screen.getByText("1 lần nhập")).toBeInTheDocument();
    await waitFor(() => expect(queries[0]?.get("unit_mode")).toBe("auto"));
    expect(queries[0]?.get("sku_id")).toBe("sku-1");
  });

  it("flags a card that does not match the stock on hand", async () => {
    cardServer(false);
    renderApp("/inventory/stock-card?sku=sku-1");
    expect(await screen.findByText("Lệch: thẻ kho 2 thùng 4 hộp, tồn thực 2 thùng 3 hộp")).toBeInTheDocument();
  });

  it("asks for a product first", async () => {
    server.use(http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })));
    renderApp("/inventory/stock-card");
    expect(await screen.findByText("Chọn mặt hàng để xem thẻ kho.")).toBeInTheDocument();
  });
});
