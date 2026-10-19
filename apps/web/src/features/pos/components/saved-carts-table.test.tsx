import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../test/render";
import { posCartSchema } from "../../sales/schemas";
import { SavedCartsTable } from "./saved-carts-table";

const cart = (id: string, docNo: string) =>
  posCartSchema.parse({
    id,
    doc_no: docNo,
    customer_party_id: null,
    label: null,
    status: "open",
    total_amount: "50000",
    sales_document_id: null,
    copied_from_id: null,
    created_at: "2026-10-18T08:00:00Z",
    updated_at: "2026-10-18T08:00:00Z",
    version: 1,
  });

const COLORS = { codeOf: () => "none" as const, idOf: () => null };

function renderTable(lockedReason: string | null) {
  renderWithProviders(
    <SavedCartsTable
      carts={[cart("cart-1", "GH-0001"), cart("cart-2", "GH-0002")]}
      linesOf={() => undefined}
      customerName={() => "Khách lẻ"}
      colors={COLORS}
      onEdit={vi.fn()}
      onCopy={vi.fn()}
      onDelete={vi.fn()}
      copying={false}
      lockedReason={lockedReason}
    />,
  );
  return screen.getByRole("table", { name: "Giỏ hàng đã lưu" });
}

describe("SavedCartsTable", () => {
  it("names each command after its cart and reads the loading goods cell as text", () => {
    const table = renderTable(null);
    const row = within(table).getByText("GH-0002").closest("tr");
    if (row === null) throw new Error("No row for GH-0002");
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["Sửa giỏ GH-0002", "Sao chép giỏ GH-0002", "Xóa giỏ GH-0002"]);
    expect(within(row).getAllByText("Đang tải")[0]).toHaveClass("sr-only");
    for (const b of within(table).getAllByRole("button")) expect(b).toBeEnabled();
  });

  it("shows why the commands are locked and links the reason to every disabled button", () => {
    const table = renderTable("Cần kết nối mạng.");
    expect(screen.getByText("Cần kết nối mạng.")).toBeVisible();
    const buttons = within(table).getAllByRole("button");
    expect(buttons).toHaveLength(6);
    for (const b of buttons) {
      expect(b).toBeDisabled();
      expect(b).toHaveAccessibleDescription("Cần kết nối mạng.");
    }
  });
});
