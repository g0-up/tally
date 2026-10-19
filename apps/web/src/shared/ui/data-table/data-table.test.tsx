import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
