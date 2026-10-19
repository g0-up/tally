import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import type { Invoice } from "../schemas";
import { InvoiceDialog } from "./invoice-dialog";

const OVERRIDE = "Tôi xác nhận vẫn bán nếu vượt hạn mức nợ hoặc dưới giá tối thiểu (ghi vào nhật ký)";
const SUBMIT = "Lưu & xác nhận bán";

const PAYMENT = { total_amount: "100000", paid_amount: "0", open_amount: "100000", pending_amount: "0", code: "unpaid", label: "Chưa thanh toán" };

function invoiceFixture(dueDate: string): Invoice {
  return {
    id: "inv-1",
    doc_no: "SO-0001",
    customer_party_id: "cus-1",
    payer_party_id: "cus-1",
    invoice_date: "2026-10-12",
    due_date: dueDate,
    status: "draft",
    delivered_amount: "0",
    quote_id: null,
    copied_from_id: null,
    note: null,
    current_version: 1,
    created_at: "2026-10-12T08:00:00Z",
    version: 1,
    goods_amount: "100000",
    discount_amount: "0",
    tax_amount: "0",
    total_amount: "100000",
    lines: [
      {
        id: "line-1",
        line_no: 1,
        sku_id: "sku-1",
        sku_code: "SP001",
        name: "Bánh quy bơ",
        variant_label: "",
        unit_id: "unit-hop",
        unit_name: "Hộp",
        unit_factor: "1",
        qty: "2",
        base_qty: "2",
        color_id: null,
        spec_text: null,
        line_kind: "item",
        parent_line_id: null,
        mix_key: null,
        mix_group_id: null,
        unit_price: "50000",
        discount_amount: "0",
        line_amount: "100000",
        min_price: "60000",
        price_source: { kind: "base_list", price_list_name: null, color_id: null },
        reserved_base_qty: "0",
        backorder_base_qty: "0",
        delivered_base_qty: "0",
        cancelled_base_qty: "0",
      },
    ],
    payment: PAYMENT,
  } as Invoice;
}

interface Scenario {
  /** Due date of the draft (its invoice date is 2026-10-12). */
  dueDate?: string;
  creditLimit?: string | null;
  termsDays?: number;
  receivable?: string;
  /** The pricing preview reports the line under its minimum price. */
  belowMin?: boolean;
}

/** Lookups, pricing and the draft invoice the dialog reads; returns the writes it receives. */
function setupScenario({ dueDate = "2026-10-12", creditLimit = null, termsDays = 0, receivable = "0", belowMin = false }: Scenario = {}) {
  const invoice = invoiceFixture(dueDate);
  const customer = { id: "cus-1", name: "Tạp hóa Minh Anh", phone: null, credit_limit: creditLimit, payment_terms_days: termsDays, status: "active" };
  const writes: Array<{ method: string; path: string; body: unknown }> = [];
  const record = async (request: Request) => {
    const text = await request.text();
    writes.push({ method: request.method, path: new URL(request.url).pathname, body: text ? (JSON.parse(text) as unknown) : null });
  };
  server.use(
    http.get(apiUrl("/invoices/inv-1"), () => HttpResponse.json(invoice, { headers: { ETag: '"1"' } })),
    http.get(apiUrl("/customers/cus-1"), () => HttpResponse.json(customer)),
    http.get(apiUrl("/customers"), () => HttpResponse.json({ data: [customer] })),
    http.get(apiUrl("/debts/parties/cus-1"), () =>
      HttpResponse.json({ party: { id: "cus-1", name: customer.name, phone: null }, receivable, documents: [], advances: [], pending_verification: [] }),
    ),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: [{ id: "unit-hop", name: "Hộp", active: true }] })),
    http.get(apiUrl("/mix-groups"), () => HttpResponse.json({ data: [] })),
    http.get(apiUrl("/payment-methods"), () =>
      HttpResponse.json({ data: [{ id: "pm-cash", code: "cash", name: "Tiền mặt", fund_account_id: "fa-cash", fund_kind: "cash", active: true }] }),
    ),
    http.get(apiUrl("/skus/sku-1"), () =>
      HttpResponse.json({
        id: "sku-1",
        code: "SP001",
        display_name: "Bánh quy bơ",
        variant_label: "",
        min_price: "60000",
        units: [{ unit_id: "unit-hop", unit_name: "Hộp", factor: "1", is_base: true, active: true, is_default_sale: true }],
      }),
    ),
    http.get(apiUrl("/stock/sku-1"), () => HttpResponse.json({ sku_id: "sku-1", unit_name: "Hộp", available: "40", colors: [{ color_id: null, sellable: "40" }] })),
    http.get(apiUrl("/stock"), () => HttpResponse.json({ data: [] })),
    http.post(apiUrl("/pricing/quote"), () =>
      HttpResponse.json({ data: [{ amount: "50000", unit_id: "unit-hop", price_source: { kind: "base_list", price_list_name: "Bảng giá chung", color_id: null } }] }),
    ),
    http.post(apiUrl("/pricing/calculate"), () =>
      HttpResponse.json({
        lines: [
          {
            draft_index: 0,
            line_kind: "item",
            sku_id: "sku-1",
            color_id: null,
            unit_id: "unit-hop",
            qty: "2",
            unit_price: "50000",
            gross_amount: "100000",
            discount_amount: "0",
            line_amount: "100000",
            below_min_price: belowMin,
            price_source: { kind: "base_list", price_list_name: "Bảng giá chung", color_id: null },
          },
        ],
        goods_amount: "100000",
        discount_amount: "0",
        tax_amount: "0",
        total_amount: "100000",
      }),
    ),
    // Saving the unchanged draft: header and lines have nothing new to store.
    http.patch(apiUrl("/invoices/inv-1"), async ({ request }) => {
      await record(request);
      return apiError(409, "nothing_changed");
    }),
    http.put(apiUrl("/invoices/inv-1/lines"), async ({ request }) => {
      await record(request);
      return apiError(409, "nothing_changed");
    }),
    // Custom methods ("inv-1:check", "inv-1:confirm") arrive as one path segment.
    http.post(apiUrl("/invoices/:command"), async ({ request, params }) => {
      await record(request);
      if (params.command === "inv-1:check") {
        return HttpResponse.json({
          color_required_line_nos: [],
          below_min_price_line_nos: belowMin ? [1] : [],
          credit_limit_exceeded: false,
          exceptions_required: belowMin ? ["below_min_price"] : [],
          lines: [],
          ready: true,
        });
      }
      if (params.command === "inv-1:confirm") return HttpResponse.json({ ...invoice, status: "confirmed", version: 2 });
      return apiError(404, "not_found");
    }),
    http.post(apiUrl("/exception-confirmations"), async ({ request }) => {
      await record(request);
      return HttpResponse.json({ id: "exc-1" });
    }),
  );
  return { invoice, writes };
}

function renderDialog(invoice: Invoice) {
  const onSaved = vi.fn();
  renderWithProviders(<InvoiceDialog invoice={invoice} onClose={vi.fn()} onSaved={onSaved} />);
  return { onSaved };
}

describe("InvoiceDialog owner confirmation", () => {
  it("asks for no confirmation when the sale is within limits", async () => {
    const { invoice } = setupScenario();
    renderDialog(invoice);
    expect(await screen.findByRole("cell", { name: /^100\.000\s₫$/ }, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: OVERRIDE })).not.toBeInTheDocument();
  });

  it("offers the override when the pricing preview reports a line under its minimum price", async () => {
    const { invoice } = setupScenario({ belowMin: true });
    renderDialog(invoice);
    expect(await screen.findByRole("checkbox", { name: OVERRIDE }, { timeout: 3000 })).not.toBeChecked();
    expect(screen.getByText("1 dòng có giá sau chiết khấu dưới mức tối thiểu.")).toBeInTheDocument();
  });

  it("offers the override when the open amount takes the customer over the credit limit", async () => {
    const { invoice } = setupScenario({ dueDate: "2026-11-11", creditLimit: "50000", termsDays: 30, receivable: "20000" });
    renderDialog(invoice);
    expect(await screen.findByRole("checkbox", { name: OVERRIDE }, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByText(/^Vượt hạn mức nợ: 120\.000\s₫ > 50\.000\s₫\.$/)).toBeInTheDocument();
  });

  it("announces the credit warning through the dialog's polite live region", async () => {
    const { invoice } = setupScenario({ dueDate: "2026-11-11", creditLimit: "50000", termsDays: 30, receivable: "20000" });
    renderDialog(invoice);
    const warning = await screen.findByText(/^Vượt hạn mức nợ:/, undefined, { timeout: 3000 });
    const region = warning.closest("[aria-live]");
    expect(region).toHaveAttribute("aria-live", "polite");
    expect(region).toBeVisible();
    expect(region?.parentElement?.closest("[aria-live]")).toBeNull();
  });

  it("blocks the confirmation until the override is ticked", async () => {
    const { invoice, writes } = setupScenario({ belowMin: true });
    const { onSaved } = renderDialog(invoice);
    await screen.findByRole("checkbox", { name: OVERRIDE }, { timeout: 3000 });

    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));
    expect(await screen.findByText(/^Tích “Tôi xác nhận vẫn bán” để tiếp tục\.$/)).toBeInTheDocument();
    expect(writes).toEqual([]);
    expect(onSaved).not.toHaveBeenCalled();
  });

  it("confirms with the owner's exception once the override is ticked", async () => {
    const { invoice, writes } = setupScenario({ belowMin: true });
    const { onSaved } = renderDialog(invoice);
    await userEvent.click(await screen.findByRole("checkbox", { name: OVERRIDE }, { timeout: 3000 }));
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));

    await vi.waitFor(() => expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: "inv-1", status: "confirmed" })), { timeout: 3000 });
    const exception = writes.find((w) => w.path.endsWith("/exception-confirmations"));
    expect(exception?.body).toMatchObject({ kind: "below_min_price", doc_type: "invoice", doc_id: "inv-1", doc_version: 1 });
    const confirm = writes.find((w) => w.path.endsWith(":confirm"));
    expect(confirm?.body).toEqual({ exception_confirmation_ids: ["exc-1"] });
  });
});
