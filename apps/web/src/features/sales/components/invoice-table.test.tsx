import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { invoiceSchema, type InvoiceStatus } from "../schemas";
import { InvoiceActs } from "./invoice-table";

const invoice = (status: InvoiceStatus) =>
  invoiceSchema.parse({
    id: "inv-1",
    doc_no: "SO-0007",
    customer_party_id: "cus-1",
    payer_party_id: "cus-1",
    invoice_date: "2026-10-10",
    due_date: null,
    status,
    delivered_amount: "0",
    quote_id: null,
    campaign_id: null,
    copied_from_id: null,
    note: null,
    current_version: 1,
    confirmed_at: null,
    confirmed_by: null,
    created_at: "2026-10-10T02:00:00Z",
    updated_at: "2026-10-10T02:00:00Z",
    version: 1,
    goods_amount: "100000",
    discount_amount: "0",
    tax_amount: "0",
    total_amount: "100000",
    paid_amount: "0",
  });

describe("InvoiceActs", () => {
  it("names each row action after its invoice while showing the short label", async () => {
    const onEdit = vi.fn();
    const draft = invoice("draft");
    render(<InvoiceActs invoice={draft} copying={false} onEdit={onEdit} onDelete={vi.fn()} onCopy={vi.fn()} />);

    const buttons = screen.getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual(["Sửa", "Xóa", "Sao chép"]);
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual(["Sửa SO-0007", "Xóa SO-0007", "Sao chép SO-0007"]);
    await userEvent.click(screen.getByRole("button", { name: "Sửa SO-0007" }));
    expect(onEdit).toHaveBeenCalledWith(draft);
  });

  it("announces the copy in flight instead of the row name", () => {
    render(<InvoiceActs invoice={invoice("confirmed")} copying onEdit={vi.fn()} onDelete={vi.fn()} onCopy={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Đang ghi sổ…" })).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByRole("button", { name: "Sửa SO-0007" })).not.toBeInTheDocument();
  });
});
