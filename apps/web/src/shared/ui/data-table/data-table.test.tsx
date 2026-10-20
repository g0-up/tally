import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ACTIONS_HEADER, DataTable, type Column } from "./data-table";

interface Row {
  id: string;
  name: string;
}

const COLUMNS: ReadonlyArray<Column<Row>> = [
  { key: "name", header: "Sản phẩm", cell: (r) => r.name },
  { key: "acts", header: "", cell: (r) => <button type="button">Sửa {r.name}</button> },
];

describe("DataTable", () => {
  it("is named by its caption and names the action column for screen readers only", () => {
    render(<DataTable caption="Tồn kho" columns={COLUMNS} rows={[{ id: "1", name: "Mứt gừng" }]} rowKey={(r) => r.id} />);
    const table = screen.getByRole("table", { name: "Tồn kho" });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual(["Sản phẩm", ""]);
    expect(headers[1]).toHaveAccessibleName(ACTIONS_HEADER);
    for (const h of headers) expect(h).toHaveAttribute("scope", "col");
  });
});

describe("DataTable scrolling", () => {
  // jsdom has no layout: the wrapper's widths are set per test to say whether the table overflows.
  function setWidths(scrollWidth: number, clientWidth: number) {
    vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(scrollWidth);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(clientWidth);
  }

  const table = () => <DataTable caption="Hàng hóa" columns={COLUMNS} rows={[{ id: "1", name: "Mứt gừng" }]} rowKey={(r) => r.id} />;

  it("lets the keyboard reach a table wider than its wrapper, as a region named by the caption", () => {
    setWidths(1600, 1200);
    render(table());
    const region = screen.getByRole("region", { name: "Hàng hóa" });
    expect(region).toHaveAttribute("tabindex", "0");
    expect(region).toContainElement(screen.getByRole("table", { name: "Hàng hóa" }));
  });

  it("adds no tab stop when the table fits", () => {
    setWidths(1200, 1200);
    const { container } = render(table());
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
    expect(container.querySelector(".tw")).not.toHaveAttribute("tabindex");
  });
});
