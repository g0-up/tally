import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { at, col, kpi, reportPage, reportRoute } from "../testing/fixtures";

describe("GrossProfitPage", () => {
  it("shows revenue, cost and margin as KPIs with the margin rate under the margin, and the provisional notes", async () => {
    const seen = reportRoute("gross_margin", () =>
      reportPage({
        report_key: "gross_margin",
        columns: [col("name", "Hàng"), col("net_revenue", "Doanh thu thuần", "money"), col("cogs", "Giá vốn", "money"), col("gross_margin", "Lãi gộp", "money"), col("margin_percent", "Tỷ suất lãi gộp", "percent")],
        rows: [{ id: "sku-1", name: "Giỏ quà Phúc Lộc", net_revenue: "10000000", cogs: "7450000", gross_margin: "2550000", margin_percent: "25.5" }],
        summary: [
          { key: "net_revenue", label: "Doanh thu thuần", kind: "money", value: "10000000" },
          { key: "cogs", label: "Giá vốn", kind: "money", value: "7450000" },
          { key: "gross_margin", label: "Lãi gộp", kind: "money", value: "2550000" },
          { key: "margin_percent", label: "Tỷ suất lãi gộp", kind: "percent", value: "25.5" },
        ],
        provisional: true,
        notes: ["Có phiếu xuất chưa chốt giá vốn; lãi gộp đang tạm tính."],
      }),
    );
    renderApp("/reports/gross-profit");

    expect(await screen.findByText("Giá vốn bình quân di động, chụp tại lúc ghi sổ xuất. Combo ghi doanh thu cho combo, giá vốn là tổng thành phần; không cộng trùng.")).toBeInTheDocument();
    await screen.findByRole("table", { name: "Lãi gộp theo hàng" });
    expect(at(seen, 0).get("columns")).toBe("name,net_revenue,cogs,gross_margin,margin_percent");
    expect(kpi("Doanh thu thuần")).toHaveTextContent(/10\.000\.000\s₫/);
    expect(kpi("Giá vốn")).toHaveTextContent(/7\.450\.000\s₫/);
    expect(kpi("Lãi gộp")).toHaveTextContent(/2\.550\.000\s₫/);
    expect(kpi("Lãi gộp")).toHaveTextContent("Biên 25,5%");
    expect(() => kpi("Tỷ suất lãi gộp")).toThrow();
    expect(screen.getByText("Có phiếu xuất chưa chốt giá vốn; lãi gộp đang tạm tính.").closest(".callout")).toHaveClass("warn");
    expect(screen.getByRole("cell", { name: "25,5%" })).toBeInTheDocument();
  });
});
