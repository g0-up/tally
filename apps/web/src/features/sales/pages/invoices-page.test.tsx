import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";

// The invoice list row as `GET /invoices` sends it: no lines, what the customer paid flat.
const invoiceRow = (over: Record<string, unknown>) => ({
  id: "inv-1",
  doc_no: "SO-0001",
  customer_party_id: "cus-1",
  payer_party_id: "cus-1",
  invoice_date: "2026-10-10",
  due_date: "2026-10-25",
  status: "confirmed",
  delivered_amount: "0",
  quote_id: null,
  campaign_id: null,
  copied_from_id: null,
  note: null,
  current_version: 1,
  confirmed_at: "2026-10-10T03:00:00Z",
  confirmed_by: null,
  created_at: "2026-10-10T02:00:00Z",
  updated_at: "2026-10-10T03:00:00Z",
  version: 2,
  goods_amount: "1000000",
  discount_amount: "0",
  tax_amount: "0",
  total_amount: "1000000",
  paid_amount: "0",
  ...over,
});

const ROWS = [
  invoiceRow({ id: "inv-1", doc_no: "SO-0001", paid_amount: "400000" }),
  invoiceRow({ id: "inv-2", doc_no: "SO-0002", paid_amount: "0" }),
  invoiceRow({ id: "inv-3", doc_no: "SO-0003", paid_amount: "1200000" }),
  // Half delivered, the rest cancelled: only the 500.000 ₫ delivered is owed, and it is paid.
  invoiceRow({ id: "inv-4", doc_no: "SO-0004", status: "cancelled_remaining", delivered_amount: "500000", paid_amount: "500000" }),
];

function invoiceApi(nextCursor: string | null = null) {
  const reads = { details: 0 };
  server.use(
    http.get(apiUrl("/invoices"), () => HttpResponse.json({ data: ROWS, next_cursor: nextCursor })),
    http.get(apiUrl("/invoices/:id"), () => {
      reads.details += 1;
      return HttpResponse.json(ROWS[0]);
    }),
    http.get(apiUrl("/customers"), () =>
      HttpResponse.json({
        data: [{ id: "cus-1", name: "Tạp hóa Minh Châu", phone: null, credit_limit: null, payment_terms_days: 15, status: "active" }],
        next_cursor: null,
      }),
    ),
  );
  return reads;
}

const rowOf = (table: HTMLElement, docNo: string) => {
  const row = within(table).getByText(docNo).closest("tr");
  if (row === null) throw new Error(`No row for ${docNo}`);
  return row;
};

describe("InvoicesPage", () => {
  it("shows what each invoice was paid and still owes from the list read alone", async () => {
    const reads = invoiceApi();
    renderApp("/sales/invoices");

    const table = await screen.findByRole("table", { name: "Hóa đơn" });
    await within(table).findByText("SO-0004");
    const partial = rowOf(table, "SO-0001");
    expect(partial).toHaveTextContent("400.000 ₫");
    expect(partial).toHaveTextContent("600.000 ₫");
    expect(within(partial).getByText("Thanh toán một phần")).toBeInTheDocument();
    expect(within(rowOf(table, "SO-0002")).getByText("Chưa thanh toán")).toBeInTheDocument();
    expect(within(rowOf(table, "SO-0003")).getByText("Thừa tiền")).toBeInTheDocument();
    expect(within(rowOf(table, "SO-0004")).getByText("Đã thanh toán đủ")).toBeInTheDocument();
    expect(within(table).queryByText("…")).not.toBeInTheDocument();

    // Footer: paid 400.000 + 0 + 1.200.000 + 500.000; open 600.000 + 1.000.000.
    const footer = within(table).getAllByRole("row").at(-1);
    if (footer === undefined) throw new Error("No footer row");
    expect(footer).toHaveTextContent("2.100.000 ₫");
    expect(footer).toHaveTextContent("1.600.000 ₫");
    expect(reads.details).toBe(0);
    expect(screen.queryByText(/chỉ tính trên các hóa đơn đã tải/)).not.toBeInTheDocument();
  });

  it("says the filters and the footer cover only the loaded invoices while more remain", async () => {
    invoiceApi("cur-2");
    renderApp("/sales/invoices");

    const table = await screen.findByRole("table", { name: "Hóa đơn" });
    await within(table).findByText("SO-0004");
    expect(screen.getByText("Bộ lọc và dòng tổng chỉ tính trên các hóa đơn đã tải; bấm “Xem thêm” để tính tiếp.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Xem thêm" })).toBeInTheDocument();
  });

  it("filters by payment state without reading each invoice", async () => {
    const reads = invoiceApi();
    const user = userEvent.setup();
    renderApp("/sales/invoices");

    const table = await screen.findByRole("table", { name: "Hóa đơn" });
    await within(table).findByText("SO-0004");
    await user.click(within(screen.getByRole("group", { name: "Lọc theo thanh toán" })).getByRole("button", { name: "Đã đủ" }));
    await waitFor(() => expect(within(table).queryByText("SO-0001")).not.toBeInTheDocument());
    expect(within(table).getByText("SO-0003")).toBeInTheDocument();
    expect(within(table).getByText("SO-0004")).toBeInTheDocument();
    expect(within(table).queryByText("SO-0002")).not.toBeInTheDocument();

    await user.click(within(screen.getByRole("group", { name: "Lọc theo thanh toán" })).getByRole("button", { name: "Chưa thanh toán" }));
    expect(await within(table).findByText("SO-0002")).toBeInTheDocument();
    expect(within(table).queryByText("SO-0003")).not.toBeInTheDocument();
    expect(reads.details).toBe(0);
  });
});
