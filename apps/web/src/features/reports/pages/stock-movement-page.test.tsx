import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { apiError } from "../../../test/msw/handlers";
import { renderApp } from "../../../test/render";
import { REPORT_MESSAGES, REPORT_TEXT } from "../api/adapters";
import { at, col, reportPage, reportRoute } from "../testing/fixtures";

const COLUMNS = [
  col("code", "Mã hàng"),
  col("name", "Hàng"),
  col("opening_qty", "Đầu kỳ", "qty"),
  col("in_qty", "Nhập", "qty"),
  col("out_qty", "Xuất", "qty"),
  col("closing_qty", "Cuối kỳ", "qty"),
  col("closing_value", "Giá trị cuối kỳ", "money"),
];

const row = (id: string, code: string, name: string, closing: string, value: string) => ({
  id,
  code,
  name,
  opening_qty: "10",
  in_qty: "100",
  out_qty: "40",
  closing_qty: closing,
  closing_value: value,
});

const FIRST = reportPage({
  columns: COLUMNS,
  rows: [row("sku-1", "KEO-01", "Kẹo dừa Bến Tre", "70", "3500000")],
  totals: { code: null, name: null, opening_qty: "20", in_qty: "200", out_qty: "80", closing_qty: "140", closing_value: "7000000" },
  scope: "Theo mã hàng (không tách màu), mọi kho trừ khi chọn kho; số lượng theo đơn vị cơ bản.",
  notes: ["Đầu kỳ + Nhập − Xuất = Cuối kỳ; giá trị theo giá vốn bình quân ghi trên sổ kho."],
  next_cursor: "c2",
});
const SECOND = reportPage({ columns: COLUMNS, rows: [row("sku-2", "MUT-02", "Mứt gừng", "70", "3500000")], totals: null, next_cursor: null });

describe("StockMovementPage", () => {
  it("reads the season so far a page at a time and keeps the totals of the first page", async () => {
    const seen = reportRoute("nxt", (q) => (q.get("cursor") === "c2" ? SECOND : FIRST));
    renderApp("/reports/stock-movement");

    expect(await screen.findByText("Đầu kỳ + nhập − xuất = cuối kỳ, tính từ sổ chuyển động theo đơn vị cơ sở. Bấm mã hàng để xem thẻ kho.")).toBeInTheDocument();
    expect(screen.getByText(REPORT_TEXT.hint)).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Nhập–xuất–tồn" });
    const q = at(seen, 0);
    expect(q.get("from")).toBe("2026-10-01");
    expect(q.get("to")).toBe("2026-10-12");
    expect(q.get("columns")).toBe("code,name,opening_qty,in_qty,out_qty,closing_qty,closing_value");
    expect(q.get("limit")).toBe("100");
    expect(q.has("cursor")).toBe(false);
    expect(screen.getByLabelText(REPORT_TEXT.from)).toHaveValue("01/10/2026");
    expect(screen.getByLabelText(REPORT_TEXT.to)).toHaveValue("12/10/2026");
    expect(screen.getByText(FIRST.scope)).toBeInTheDocument();
    // The period ends today: nothing to say about the current stock.
    expect(screen.queryByText("Kỳ kết thúc trước hôm nay nên không so với tồn hiện tại")).not.toBeInTheDocument();

    expect(within(table).getByRole("link", { name: "KEO-01" })).toHaveAttribute("href", "/inventory/stock-card?sku=sku-1");
    const footer = () => at(within(table).getAllByRole("row"), within(table).getAllByRole("row").length - 1);
    expect(footer()).toHaveTextContent(REPORT_TEXT.total);
    expect(footer()).toHaveTextContent(/7\.000\.000\s₫/);
    expect(footer()).toHaveTextContent("140");

    await userEvent.click(screen.getByRole("button", { name: "Xem thêm" }));
    expect(await within(table).findByText("Mứt gừng")).toBeInTheDocument();
    expect(at(seen, 1).get("cursor")).toBe("c2");
    expect(at(seen, 1).get("from")).toBe("2026-10-01");
    expect(within(table).getAllByRole("row")).toHaveLength(4);
    expect(footer()).toHaveTextContent(/7\.000\.000\s₫/);
    expect(screen.queryByRole("button", { name: "Xem thêm" })).not.toBeInTheDocument();
  });

  it("takes the period from the URL and writes a new date back to it", async () => {
    const seen = reportRoute("nxt", () => reportPage({ columns: COLUMNS }));
    const { router } = renderApp("/reports/stock-movement?from=2026-10-05&to=2026-10-10");

    await waitFor(() => expect(seen).toHaveLength(1));
    expect(at(seen, 0).get("from")).toBe("2026-10-05");
    expect(at(seen, 0).get("to")).toBe("2026-10-10");
    expect(screen.getByText("Kỳ kết thúc trước hôm nay nên không so với tồn hiện tại")).toBeInTheDocument();

    const from = screen.getByLabelText(REPORT_TEXT.from);
    await userEvent.clear(from);
    await userEvent.type(from, "03/10/2026");
    await waitFor(() => expect(seen.some((q) => q.get("from") === "2026-10-03")).toBe(true));
    expect(new URLSearchParams(router.state.location.search).get("from")).toBe("2026-10-03");
    expect(new URLSearchParams(router.state.location.search).get("to")).toBe("2026-10-10");
  });

  it("asks nothing of the server for an inverted period", async () => {
    const seen = reportRoute("nxt", () => reportPage({ columns: COLUMNS }));
    renderApp("/reports/stock-movement?from=2026-10-10&to=2026-10-05");

    expect(await screen.findByRole("alert")).toHaveTextContent(REPORT_MESSAGES.periodInvalid);
    expect(seen).toHaveLength(0);
  });

  it("explains a period too long for the report", async () => {
    reportRoute("nxt", () => apiError(422, "range_too_long", { message: "range too long" }));
    renderApp("/reports/stock-movement?from=2024-01-01");

    expect(await screen.findByRole("alert")).toHaveTextContent(REPORT_MESSAGES.rangeTooLong);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});
