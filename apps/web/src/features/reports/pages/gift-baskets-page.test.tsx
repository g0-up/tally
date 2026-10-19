import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { at, col, reportPage, reportRoute } from "../testing/fixtures";

describe("GiftBasketsPage", () => {
  it("lists the server's basket figures as Chỉ tiêu / Giá trị above the baskets", async () => {
    const seen = reportRoute("kits", () =>
      reportPage({
        report_key: "kits",
        columns: [col("code", "Mã"), col("name", "Giỏ"), col("assembled_qty", "Đã đóng", "qty"), col("sold_qty", "Đã bán", "qty")],
        rows: [{ id: "kit-1", code: "GIO-01", name: "Giỏ Phúc Lộc", assembled_qty: "120", sold_qty: "95" }],
        summary: [
          { key: "assembled_qty", label: "Đã đóng trong kỳ", kind: "qty", value: "120" },
          { key: "sold_qty", label: "Đã bán trong kỳ", kind: "qty", value: "95" },
          { key: "to_assemble", label: "Còn phải đóng", kind: "qty", value: "30" },
          { key: "gross_margin", label: "Lãi gộp", kind: "money", value: "4750000" },
        ],
      }),
    );
    renderApp("/reports/gift-baskets");

    expect(await screen.findByText("Doanh thu giỏ ghi cho giỏ; thành phần đã tiêu hao lúc đóng nên không cộng doanh thu hay xuất kho lần nữa.")).toBeInTheDocument();
    const metrics = await screen.findByRole("table", { name: "Chỉ tiêu giỏ quà trong kỳ" });
    expect(within(metrics).getByRole("columnheader", { name: "Chỉ tiêu" })).toBeInTheDocument();
    expect(within(metrics).getByRole("columnheader", { name: "Giá trị" })).toBeInTheDocument();
    const rows = within(metrics).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.textContent)).toEqual(["Đã đóng trong kỳ120", "Đã bán trong kỳ95", "Còn phải đóng30", expect.stringMatching(/^Lãi gộp4\.750\.000\s₫$/)]);
    expect(within(screen.getByRole("table", { name: "Giỏ quà theo mã" })).getByText("Giỏ Phúc Lộc")).toBeInTheDocument();
    expect(at(seen, 0).get("from")).toBe("2026-10-01");
    expect(at(seen, 0).has("columns")).toBe(false);
  });
});
