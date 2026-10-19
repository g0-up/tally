import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import type { Invoice } from "../schemas";
import { StockOutDialog } from "./stock-out-dialog";

const SUBMIT = "Ghi sổ xuất kho";

function line(init: {
  id: string;
  line_no: number;
  kind: string;
  parent?: string;
  sku: string | null;
  name: string;
  unit: string;
  factor: string;
  qty: string;
  reserved: string;
  amount: string;
}) {
  const base = String(Number(init.qty) * Number(init.factor));
  return {
    id: init.id,
    line_no: init.line_no,
    sku_id: init.sku,
    sku_code: init.sku?.toUpperCase() ?? null,
    name: init.name,
    variant_label: "",
    unit_id: `unit-${init.unit}`,
    unit_name: init.unit,
    unit_factor: init.factor,
    qty: init.qty,
    base_qty: base,
    color_id: null,
    spec_text: null,
    line_kind: init.kind,
    parent_line_id: init.parent ?? null,
    mix_key: null,
    mix_group_id: null,
    unit_price: "0",
    discount_amount: "0",
    line_amount: init.amount,
    min_price: null,
    price_source: null,
    reserved_base_qty: init.reserved,
    backorder_base_qty: String(Number(base) - Number(init.reserved)),
    delivered_base_qty: "0",
    cancelled_base_qty: "0",
  };
}

const LINES = [
  line({ id: "l-banh", line_no: 1, kind: "item", sku: "sku-banh", name: "Bánh quy bơ", unit: "Thùng", factor: "24", qty: "1", reserved: "24", amount: "1200000" }),
  // 2 thùng mix of 24 lon, 6 lon a lốc: 3 lốc of cam and 5 of dâu, all held.
  line({ id: "l-mix", line_no: 2, kind: "mix_pack", sku: null, name: "Thùng mix nước ngọt", unit: "Thùng", factor: "1", qty: "2", reserved: "0", amount: "324000" }),
  line({ id: "l-cam", line_no: 3, kind: "mix_flavor", parent: "l-mix", sku: "sku-cam", name: "Nước cam", unit: "Lốc", factor: "6", qty: "3", reserved: "18", amount: "0" }),
  line({ id: "l-dau", line_no: 4, kind: "mix_flavor", parent: "l-mix", sku: "sku-dau", name: "Nước dâu", unit: "Lốc", factor: "6", qty: "5", reserved: "30", amount: "0" }),
  // A combo of a box of biscuits and two packs of candy, one pack still to come in.
  line({ id: "l-combo", line_no: 5, kind: "combo", sku: null, name: "Combo quà Tết", unit: "Combo", factor: "1", qty: "1", reserved: "0", amount: "250000" }),
  line({ id: "l-hop", line_no: 6, kind: "combo_component", parent: "l-combo", sku: "sku-hop", name: "Hộp bánh", unit: "Hộp", factor: "1", qty: "1", reserved: "1", amount: "0" }),
  line({ id: "l-keo", line_no: 7, kind: "combo_component", parent: "l-combo", sku: "sku-keo", name: "Kẹo dừa", unit: "Gói", factor: "1", qty: "2", reserved: "1", amount: "0" }),
];

const INVOICE = {
  id: "inv-1",
  doc_no: "HD-0001",
  customer_party_id: "cus-1",
  payer_party_id: "cus-1",
  invoice_date: "2026-10-12",
  due_date: null,
  status: "confirmed",
  delivered_amount: "0",
  quote_id: null,
  copied_from_id: null,
  note: null,
  current_version: 1,
  created_at: "2026-10-12T09:00:00Z",
  version: 1,
  goods_amount: "1774000",
  discount_amount: "0",
  tax_amount: "0",
  total_amount: "1774000",
};

/** The invoice with the given lines and the payer's ledger; returns the bodies posted. */
function setupStockOut(lines: unknown[] = LINES) {
  const posted: unknown[] = [];
  server.use(
    http.get(apiUrl("/invoices/inv-1"), () => HttpResponse.json({ ...INVOICE, lines })),
    http.get(apiUrl("/debts/parties/cus-1"), () =>
      HttpResponse.json({ party: { id: "cus-1", name: "Tạp hóa Minh Anh", phone: null }, receivable: "0", documents: [], advances: [], pending_verification: [] }),
    ),
    http.post(apiUrl("/invoices/inv-1/stock-outs"), async ({ request }) => {
      posted.push(await request.json());
      return HttpResponse.json({
        id: "so-1",
        doc_no: "XK-0001",
        invoice_id: "inv-1",
        business_date: "2026-10-12",
        note: null,
        status: "posted",
        sales_document_id: null,
        sales_document: null,
      });
    }),
  );
  return { posted };
}

function renderDialog() {
  const onPosted = vi.fn();
  renderWithProviders(<StockOutDialog invoice={{ ...INVOICE, lines: [] } as unknown as Invoice} onClose={vi.fn()} onPosted={onPosted} />);
  return { onPosted };
}

async function deliverRows(): Promise<HTMLElement[]> {
  const table = await screen.findByRole("table", { name: "Hàng xuất của HD-0001" });
  return within(table).getAllByRole("row").slice(1);
}

const outValue = () => screen.getByText("Giá trị xuất").nextElementSibling?.textContent;

describe("StockOutDialog", () => {
  it("lists a mix pack and a combo as whole rows under their header, never a flavor or component alone", async () => {
    setupStockOut();
    renderDialog();
    const rows = await deliverRows();

    expect(rows).toHaveLength(3);
    expect(rows[1]).toHaveTextContent("Thùng mix nước ngọt Thùng mix");
    expect(rows[1]).toHaveTextContent("Nước cam × 3 Lốc · Nước dâu × 5 Lốc");
    expect(rows[2]).toHaveTextContent("Combo quà Tết Combo");
    expect(rows[2]).toHaveTextContent("Hộp bánh × 1 Hộp · Kẹo dừa × 2 Gói");
    expect(screen.queryByRole("textbox", { name: "Xuất lần này – Nước cam" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Xuất lần này – Kẹo dừa" })).not.toBeInTheDocument();
  });

  it("picks a ready mix pack whole and sends only its header", async () => {
    const { posted } = setupStockOut();
    const { onPosted } = renderDialog();
    await deliverRows();

    const mix = screen.getByRole("checkbox", { name: "Xuất lần này – Thùng mix nước ngọt" });
    expect(mix).toBeChecked();
    expect(mix).toBeEnabled();
    expect(outValue()).toBe("1.524.000 ₫");

    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));

    await vi.waitFor(() => expect(onPosted).toHaveBeenCalled());
    expect(posted).toEqual([
      {
        lines: [
          { invoice_line_id: "l-banh", base_qty: "24" },
          { invoice_line_id: "l-mix", base_qty: "2" },
        ],
      },
    ]);
  });

  it("keeps a combo short of stock out of reach and says which component is missing", async () => {
    setupStockOut();
    renderDialog();
    await deliverRows();

    const combo = screen.getByRole("checkbox", { name: "Xuất lần này – Combo quà Tết" });
    expect(combo).not.toBeChecked();
    expect(combo).toBeDisabled();
    expect(combo).toHaveAccessibleDescription("Chưa giữ đủ hàng: Kẹo dừa giữ 1/2 Gói");
  });

  it("leaves out a mix pack the user unticks", async () => {
    const { posted } = setupStockOut();
    const { onPosted } = renderDialog();
    await deliverRows();

    await userEvent.click(screen.getByRole("checkbox", { name: "Xuất lần này – Thùng mix nước ngọt" }));
    expect(outValue()).toBe("1.200.000 ₫");
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));

    await vi.waitFor(() => expect(onPosted).toHaveBeenCalled());
    expect(posted).toEqual([{ lines: [{ invoice_line_id: "l-banh", base_qty: "24" }] }]);
  });

  it("posts nothing when the only ready pack is unticked", async () => {
    const { posted } = setupStockOut(LINES.slice(1));
    renderDialog();
    await deliverRows();

    await userEvent.click(screen.getByRole("checkbox", { name: "Xuất lần này – Thùng mix nước ngọt" }));
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));

    expect(await screen.findByText("Nhập số lượng xuất lần này.")).toBeInTheDocument();
    expect(posted).toEqual([]);
  });
});
