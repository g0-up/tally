import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { colorPriceKey, type ColorPriceMap } from "../api/queries";
import { COLOR_BLUE, COLOR_RED, testColorStock, testStockRow } from "../testing/fixtures";
import { StockTable, type StockTableProps } from "./stock-table";

const colored = testStockRow({
  sku_id: "sku-2",
  code: "SP002",
  name: "Kẹo dẻo",
  on_hand: "30",
  available: "25",
  reserved: "5",
  quarantine: "2",
  colors: [testColorStock(), testColorStock({ color_id: COLOR_BLUE, color_code: "xanh", color_name: "Xanh", on_hand: "20", sellable: "20" })],
});
const plain = testStockRow();
const skus = new Map([
  ["sku-1", testSku()],
  ["sku-2", testSku({ id: "sku-2", code: "SP002", display_name: "Kẹo dẻo", retail_price: "20000" as never })],
]);

function renderTable(over: Partial<StockTableProps> = {}) {
  const props: StockTableProps = {
    rows: [plain, colored],
    skus,
    colorPrices: new Map(),
    onSeen: vi.fn(),
    partial: false,
    pick: "all",
    open: {},
    onToggle: vi.fn(),
    onEditPrice: vi.fn(),
    onColorPrice: vi.fn(),
    ...over,
  };
  renderWithProviders(<StockTable {...props} />);
  return props;
}

const rowOf = (text: string) => screen.getByText(text).closest("tr") as HTMLElement;
/** The expanded row of one color (the main row also names it in a stock pill). */
const colorRowOf = (label: string) => screen.getAllByText(label).find((el) => el.classList.contains("subrow"))?.closest("tr") as HTMLElement;

describe("StockTable", () => {
  it("shows stock, holds and prices per SKU, and totals the value", () => {
    renderTable();
    const row = rowOf("Kẹo dẻo");
    expect(within(row).getByText("30")).toBeInTheDocument();
    expect(within(row).getByText("25")).toBeInTheDocument();
    expect(within(row).getByText((_, el) => el?.textContent === "giữ 5 · cách ly 2")).toBeInTheDocument();
    expect(within(rowOf("Bánh quy bơ")).queryByText(/giữ|cách ly/)).not.toBeInTheDocument();
    expect(within(row).getByText("giá chung mọi màu")).toBeInTheDocument();
    expect(screen.getByText("2 mặt hàng · 1 có màu")).toBeInTheDocument();
    expect(within(rowOf("Bánh quy bơ")).getByRole("link", { name: "Thẻ kho – Bánh quy bơ" })).toHaveAttribute("href", "/inventory/stock-card?sku=sku-1");
  });

  it("lists the larger units under the name and converts the main row's stock into them", () => {
    renderTable({ open: { "sku-2": true } });
    const row = rowOf("Kẹo dẻo");
    expect(within(row).getByText((_, el) => el?.textContent === "SP002 · ĐV cơ sở: hộp · 1 thùng = 12 hộp")).toBeInTheDocument();
    expect(within(row).getByText("= 2 thùng 6 hộp")).toBeInTheDocument();
    expect(within(row).getByText("= 2 thùng 1 hộp")).toBeInTheDocument();
    expect(within(colorRowOf("Xanh")).queryByText(/thùng/)).not.toBeInTheDocument();
    expect(within(rowOf("Bánh quy bơ")).getAllByText("= 2 thùng")).toHaveLength(2);
  });

  it("converts nothing below the smallest larger unit or for a product of one unit", () => {
    const one = testSku({ units: testSku().units.filter((u) => u.is_base) });
    renderTable({ rows: [testStockRow({ on_hand: "11", available: "11" }), colored], skus: new Map([["sku-1", testSku()], ["sku-2", one]]) });
    expect(within(rowOf("Bánh quy bơ")).queryByText(/^= /)).not.toBeInTheDocument();
    expect(within(rowOf("Kẹo dẻo")).queryByText(/^= /)).not.toBeInTheDocument();
    expect(within(rowOf("Kẹo dẻo")).getByText((_, el) => el?.textContent === "SP002 · ĐV cơ sở: hộp")).toBeInTheDocument();
  });

  it("shows the base figures alone while the catalog rows load", () => {
    renderTable({ skus: undefined });
    expect(within(rowOf("Bánh quy bơ")).queryByText(/thùng/)).not.toBeInTheDocument();
  });

  it("tells quarantined from expired stock, and marks totals of a partly loaded list", () => {
    renderTable({ rows: [testStockRow({ quarantine: "2", expired: "3" })], partial: true });
    expect(within(rowOf("Bánh quy bơ")).getByText((_, el) => el?.textContent === "cách ly 2 · hết hạn 3")).toBeInTheDocument();
    expect(screen.getByText("1 mặt hàng (đã tải)")).toBeInTheDocument();
    expect(screen.getByText("trên các dòng đã tải")).toBeInTheDocument();
  });

  it("toggles the color rows and edits the price of the product or of one color", async () => {
    const user = userEvent.setup();
    const props = renderTable();
    await user.click(screen.getByRole("button", { name: "Xem chi tiết màu của Kẹo dẻo" }));
    expect(props.onToggle).toHaveBeenCalledWith("sku-2", true);
    await user.click(within(rowOf("Bánh quy bơ")).getByRole("button", { name: "Sửa giá – Bánh quy bơ" }));
    expect(props.onEditPrice).toHaveBeenCalledWith(skus.get("sku-1"));
  });

  it("opens the colors of the picked color filter and marks colors with their own price", async () => {
    const user = userEvent.setup();
    const colorPrices: ColorPriceMap = new Map([[colorPriceKey("sku-2", COLOR_RED), { retail: "22000" as never, wholesale: null }]]);
    const props = renderTable({ pick: "do", colorPrices });
    expect(screen.getByRole("button", { name: "Thu gọn màu của Kẹo dẻo" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(/^1 màu giá riêng/)).toBeInTheDocument();
    const red = colorRowOf("Đỏ");
    expect(within(red).getByText(/giá riêng$/)).toBeInTheDocument();
    expect(within(colorRowOf("Xanh")).getByText("theo giá chung")).toBeInTheDocument();
    await user.click(within(red).getByRole("button", { name: "Sửa giá – Kẹo dẻo · Đỏ" }));
    expect(props.onColorPrice).toHaveBeenCalledWith(colored, expect.objectContaining({ color_id: COLOR_RED }));
    expect(within(red).getByRole("link", { name: "Thẻ kho – Kẹo dẻo · Đỏ" })).toHaveAttribute("href", "/inventory/stock-card?sku=sku-2&color=do");
  });
});
