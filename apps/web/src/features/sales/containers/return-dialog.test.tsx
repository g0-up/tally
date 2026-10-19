import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { ReturnDialog } from "./return-dialog";

const QUARANTINE = "Đưa hàng trả vào khu cách ly chờ kiểm tra (bỏ tích nếu hàng còn nguyên, bán lại được ngay)";
const SUBMIT = "Ghi sổ trả hàng";

const TOTALS = { goods_amount: "140000", discount_amount: "0", tax_amount: "0", total_amount: "140000" };

const DOCUMENT = {
  id: "sd-1",
  doc_no: "BH-0001",
  source: "stock_out",
  stock_out_id: null,
  invoice_id: null,
  pos_cart_id: null,
  customer_party_id: "cus-1",
  payer_party_id: "cus-1",
  business_date: "2026-10-12",
  status: "posted",
  posted_at: "2026-10-12T09:00:00Z",
  cash_tendered_amount: "0",
  change_amount: "0",
  ...TOTALS,
};

const CUSTOMER = { id: "cus-1", name: "Tạp hóa Minh Anh", phone: null, credit_limit: null, payment_terms_days: 15, status: "active" };

function returnable(id: string, init: { line_no: number; line_kind?: string; sku_id: string; name: string; color_id: string | null; sold: string; returned: string; amount: string }) {
  const left = String(Number(init.sold) - Number(init.returned));
  return {
    sales_document_line_id: id,
    line_no: init.line_no,
    line_kind: init.line_kind ?? "item",
    parent_line_id: null,
    sku_id: init.sku_id,
    sku_code: init.sku_id.toUpperCase(),
    name: init.name,
    variant_label: "",
    color_id: init.color_id,
    unit_id: "unit-goi",
    unit_name: "Gói",
    unit_factor: "1",
    sold_base_qty: init.sold,
    returned_base_qty: init.returned,
    returnable_base_qty: left,
    line_amount: init.amount,
    unit_amount: String(Number(init.amount) / Number(init.sold)),
  };
}

const RETURNABLE = [
  returnable("l-1", { line_no: 1, sku_id: "sku-2", name: "Kẹo dừa", color_id: "c-do", sold: "5", returned: "2", amount: "100000" }),
  returnable("l-2", { line_no: 2, sku_id: "sku-2", name: "Kẹo dừa", color_id: "c-vang", sold: "2", returned: "0", amount: "40000" }),
  // A mix pack carries no stock of its own and a fully returned line has nothing left.
  returnable("l-3", { line_no: 3, line_kind: "mix_pack", sku_id: "sku-3", name: "Thùng mix", color_id: null, sold: "1", returned: "0", amount: "0" }),
  returnable("l-4", { line_no: 4, sku_id: "sku-4", name: "Mứt gừng", color_id: null, sold: "4", returned: "4", amount: "80000" }),
  // Sold by an older document without a color, for a product now stocked by color.
  returnable("l-5", { line_no: 5, sku_id: "sku-5", name: "Hạt dưa", color_id: null, sold: "6", returned: "0", amount: "60000" }),
];

/** The source document, its returnable lines and the lookups; returns the bodies posted. */
function setupReturn() {
  const posted: unknown[] = [];
  server.use(
    http.get(apiUrl("/sales-documents"), () => HttpResponse.json({ data: [DOCUMENT], next_cursor: null })),
    http.get(apiUrl("/sales-documents/sd-1"), () =>
      HttpResponse.json({ ...DOCUMENT, payment: { total_amount: "140000", paid_amount: "0", open_amount: "140000", pending_amount: "0", code: "unpaid", label: "Chưa thanh toán" } }),
    ),
    http.get(apiUrl("/sales-documents/sd-1/returnable-lines"), () => HttpResponse.json({ data: RETURNABLE })),
    http.get(apiUrl("/customers"), () => HttpResponse.json({ data: [CUSTOMER] })),
    http.get(apiUrl("/debts/parties/cus-1"), () =>
      HttpResponse.json({
        party: { id: "cus-1", name: CUSTOMER.name, phone: null },
        receivable: "140000",
        documents: [
          { id: "ob-1", direction: "receivable", source_type: "sales_document", source_id: "sd-1", source_doc_no: "BH-0001", amount: "140000", settled_amount: "0", open: "140000", status: "open" },
        ],
        advances: [],
        pending_verification: [],
      }),
    ),
    http.get(apiUrl("/reasons"), () => HttpResponse.json({ data: [{ id: "rs-1", kind: "sales_return", name: "Hàng lỗi", active: true }] })),
    http.get(apiUrl("/payment-methods"), () =>
      HttpResponse.json({ data: [{ id: "pm-cash", code: "cash", name: "Tiền mặt", fund_account_id: "fa-cash", fund_kind: "cash", active: true }] }),
    ),
    http.get(apiUrl("/colors"), () =>
      HttpResponse.json({
        data: [
          { id: "c-do", code: "do", name: "Đỏ", sort_order: 1, active: true },
          { id: "c-vang", code: "vang", name: "Vàng", sort_order: 2, active: true },
        ],
      }),
    ),
    http.get(apiUrl("/stock/sku-5"), () => HttpResponse.json({ sku_id: "sku-5", unit_name: "Gói", available: "10", colors: [{ color_id: "c-do", sellable: "10" }] })),
    http.post(apiUrl("/sales-returns"), async ({ request }) => {
      posted.push(await request.json());
      return HttpResponse.json({
        sales_return: {
          id: "sr-1",
          doc_no: "TH-0001",
          customer_party_id: "cus-1",
          sales_document_id: "sd-1",
          reason_id: "rs-1",
          business_date: "2026-10-12",
          total_amount: "40000",
          debt_reduction_amount: "40000",
          refund_due_amount: "0",
          exchange_id: null,
          status: "posted",
        },
        refund: null,
      });
    }),
  );
  return { posted };
}

function renderDialog() {
  const onPosted = vi.fn();
  renderWithProviders(<ReturnDialog documentId="sd-1" onClose={vi.fn()} onPosted={onPosted} />);
  return { onPosted };
}

/** Body rows of the returnable table, once the colors stocked for the uncolored line are known. */
async function returnableRows(): Promise<HTMLElement[]> {
  const table = await screen.findByRole("table", { name: "Hàng còn được trả" });
  await within(table).findByText("Chứng từ cũ chưa ghi màu");
  return within(table).getAllByRole("row").slice(1);
}

function qtyOf(rowName: RegExp): HTMLElement {
  const row = screen.getAllByRole("row").find((r) => rowName.test(r.textContent ?? ""));
  if (!row) throw new Error(`No row ${String(rowName)}`);
  return within(row).getByRole("textbox");
}

describe("ReturnDialog", () => {
  it("lists what is still returnable per color sold", async () => {
    setupReturn();
    renderDialog();
    const rows = await returnableRows();

    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringMatching(/^Kẹo dừaĐỏ3 Gói/),
      expect.stringMatching(/^Kẹo dừaVàng2 Gói/),
      expect.stringMatching(/^Hạt dưa.*Chứng từ cũ chưa ghi màu.*6 Gói/),
    ]);
    expect(screen.queryByText("Thùng mix")).not.toBeInTheDocument();
    expect(screen.queryByText("Mứt gừng")).not.toBeInTheDocument();
  });

  it("refuses more than the color still returnable", async () => {
    const { posted } = setupReturn();
    renderDialog();
    await returnableRows();

    await userEvent.type(qtyOf(/^Kẹo dừaĐỏ/), "4");
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));
    expect(await screen.findByText("Kẹo dừa Đỏ: chỉ còn được trả 3.")).toBeInTheDocument();
    expect(posted).toEqual([]);
  });

  it("asks which color comes back for a line sold without one", async () => {
    const { posted } = setupReturn();
    renderDialog();
    await returnableRows();

    await userEvent.type(qtyOf(/^Hạt dưa/), "1");
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));
    expect(await screen.findByText("Chọn màu khách trả cho Hạt dưa.")).toBeInTheDocument();
    expect(posted).toEqual([]);
  });

  it("sends the returned goods to quarantine by default", async () => {
    const { posted } = setupReturn();
    const { onPosted } = renderDialog();
    await returnableRows();
    expect(screen.getByRole("checkbox", { name: QUARANTINE })).toBeChecked();
    expect(screen.getByText("Vào khu cách ly")).toBeInTheDocument();

    await userEvent.type(qtyOf(/^Kẹo dừaVàng/), "2");
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));

    await vi.waitFor(() => expect(onPosted).toHaveBeenCalledWith(expect.objectContaining({ id: "sr-1" })));
    expect(posted).toEqual([
      {
        sales_document_id: "sd-1",
        reason_id: "rs-1",
        lines: [{ sales_document_line_id: "l-2", unit_id: "unit-goi", color_id: "c-vang", qty: "2", restock_to: "quarantine" }],
      },
    ]);
  });

  it("puts the goods back on sale when quarantine is unticked", async () => {
    const { posted } = setupReturn();
    const { onPosted } = renderDialog();
    await returnableRows();

    await userEvent.click(screen.getByRole("checkbox", { name: QUARANTINE }));
    expect(screen.getByText("Nhập lại, bán được ngay")).toBeInTheDocument();
    await userEvent.type(qtyOf(/^Kẹo dừaĐỏ/), "1");
    await userEvent.click(screen.getByRole("button", { name: SUBMIT }));

    await vi.waitFor(() => expect(onPosted).toHaveBeenCalled());
    expect(posted).toEqual([
      expect.objectContaining({ lines: [{ sales_document_line_id: "l-1", unit_id: "unit-goi", color_id: "c-do", qty: "1", restock_to: "sellable" }] }),
    ]);
  });

  it("names each row's controls after the product, or the row number for an unpicked line", async () => {
    setupReturn();
    renderDialog();
    await returnableRows();

    expect(screen.getByRole("radiogroup", { name: "Màu khách trả – Hạt dưa" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Số lượng trả – Hạt dưa" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("checkbox", { name: "Không có chứng từ gốc" }));
    await userEvent.click(screen.getByRole("button", { name: "+ Thêm hàng" }));
    const table = screen.getByRole("table", { name: "Hàng khách trả" });
    expect(within(table).getAllByRole("combobox").map((c) => c.getAttribute("aria-label"))).toEqual(["Hàng trả – dòng 1", "Hàng trả – dòng 2"]);
    expect(within(table).getByRole("textbox", { name: "Số lượng trả – dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Đơn giá trả – dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa – dòng 1" })).toBeInTheDocument();
  });
});
