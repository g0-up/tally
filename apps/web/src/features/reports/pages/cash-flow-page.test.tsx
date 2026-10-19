import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { col, kpi, reportPage, reportRoute } from "../testing/fixtures";

describe("CashFlowPage", () => {
  it("shows opening, receipts, payments and closing of the funds as the prototype names them", async () => {
    reportRoute("cash_flow", () =>
      reportPage({
        report_key: "cash_flow",
        columns: [col("fund", "Quỹ"), col("category", "Loại thu chi"), col("in_amount", "Thu", "money"), col("out_amount", "Chi", "money"), col("net_amount", "Chênh lệch", "money")],
        rows: [{ fund: "Két tiền mặt", category: "Thu tiền bán hàng", in_amount: "8000000", out_amount: "0", net_amount: "8000000" }],
        summary: [
          { key: "opening", label: "Tồn quỹ đầu kỳ", kind: "money", value: "5000000" },
          { key: "in_amount", label: "Tổng thu", kind: "money", value: "8000000" },
          { key: "out_amount", label: "Tổng chi", kind: "money", value: "3000000" },
          { key: "closing", label: "Tồn quỹ cuối kỳ", kind: "money", value: "10000000" },
        ],
        sections: [{ key: "funds", title: "Số dư quỹ", columns: [col("fund", "Quỹ"), col("closing", "Cuối kỳ", "money")], rows: [{ fund: "Két tiền mặt", closing: "10000000" }], totals: null }],
      }),
    );
    renderApp("/reports/cash-flow");

    expect(await screen.findByText("Tiền thực vào/ra quỹ theo phân loại. Thu tiền bán hàng, tiền cọc, góp vốn và chuyển quỹ được tách riêng.")).toBeInTheDocument();
    await screen.findByRole("table", { name: "Thu chi theo loại" });
    expect(kpi("Đầu kỳ")).toHaveTextContent(/5\.000\.000\s₫/);
    expect(kpi("Tổng thu")).toHaveTextContent(/8\.000\.000\s₫/);
    expect(kpi("Tổng chi")).toHaveTextContent(/3\.000\.000\s₫/);
    expect(kpi("Cuối kỳ")).toHaveTextContent(/10\.000\.000\s₫/);
    const funds = screen.getByRole("table", { name: "Số dư quỹ" });
    expect(within(funds).getByText("Két tiền mặt")).toBeInTheDocument();
  });
});
