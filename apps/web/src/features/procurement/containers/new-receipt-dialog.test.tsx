import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { SUPPLIER_ID, TEST_UNITS, testOrder, testOrderLine, testSku } from "../testing/fixtures";
import { NewReceiptDialog } from "./new-receipt-dialog";

const lookups = [
  http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })),
  http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
];

describe("NewReceiptDialog", () => {
  it("receives an order's pending lines in base units, then records freight and the invoice number", async () => {
    const posted: Record<string, unknown> = {};
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), async ({ request }) => {
        posted.receipt = await request.json();
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 });
      }),
      http.post(apiUrl("/receipts/rcv-9/landed-costs"), async ({ request }) => {
        posted.landed = await request.json();
        return HttpResponse.json({ id: "lc-1" }, { status: 201 });
      }),
      http.post(apiUrl("/supplier-documents"), async ({ request }) => {
        posted.invoice = await request.json();
        return HttpResponse.json({ id: "sd-1", document_no: "HD-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    // The loading dialog is replaced by the form's once the order's lines arrive, so find the form first.
    const qty = await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    expect(within(dialog).getByText("Công ty Bánh kẹo Hải Hà · số lượng theo đơn vị cơ sở")).toBeInTheDocument();
    expect(qty).toHaveValue("24");
    expect(within(dialog).queryByRole("button", { name: "+ Hàng có sẵn" })).not.toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "26");
    expect(within(dialog).getByText("Có – sẽ bị chặn")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));
    expect(await screen.findByText(PROCUREMENT_MESSAGES.overTolerance("Bánh quy bơ", "26", "24"), { exact: false })).toBeInTheDocument();
    expect(posted.receipt).toBeUndefined();

    await user.clear(qty);
    await user.type(qty, "12");
    await user.type(within(dialog).getByRole("textbox", { name: "Số hóa đơn NCC (nếu có)" }), "HD-1");
    await user.type(within(dialog).getByRole("textbox", { name: /^Chi phí vận chuyển\/bốc xếp/ }), "30000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(posted.receipt).toEqual({ purchase_order_id: "po-1", paid_now_amount: "0", lines: [{ purchase_order_line_id: "pol-1", qty: "1" }] });
    expect(posted.landed).toEqual({ cost_type: "shipping", amount: "30000", method: "by_value", paid_now_amount: "0" });
    expect(posted.invoice).toEqual({ supplier_party_id: SUPPLIER_ID, document_no: "HD-1", goods_amount: "120000", tax_amount: "0", receipt_ids: ["rcv-9"] });
    expect(await screen.findByText("PN-0009")).toBeInTheDocument();
  });

  it("does not post the receipt twice when a step after it fails, and continues from that step", async () => {
    let receipts = 0;
    let landed = 0;
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), () => {
        receipts++;
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 });
      }),
      http.post(apiUrl("/receipts/rcv-9/landed-costs"), () => {
        landed++;
        return landed === 1 ? HttpResponse.error() : HttpResponse.json({ id: "lc-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Chi phí vận chuyển\/bốc xếp/ }), "30000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));

    expect(await screen.findByText(/nhưng chưa ghi chi phí vận chuyển/)).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.click(within(screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" })).getByRole("button", { name: "Ghi sổ nhập kho" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(receipts).toBe(1);
    expect(landed).toBe(2);
  });

  it("sends the invoice and price steps under keys derived from the receipt's, the same ones on a retry", async () => {
    const keys: Record<string, Array<string | null>> = { receipt: [], invoice: [], price: [] };
    const answer = (list: Array<string | null>, request: Request, body: Record<string, unknown>, status = 201) => {
      list.push(request.headers.get("Idempotency-Key"));
      return list.length === 1 ? HttpResponse.error() : HttpResponse.json(body, { status });
    };
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), ({ request }) => {
        keys.receipt?.push(request.headers.get("Idempotency-Key"));
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 });
      }),
      http.post(apiUrl("/supplier-documents"), ({ request }) => answer(keys.invoice ?? [], request, { id: "sd-1", document_no: "HD-1" })),
      http.post(apiUrl("/skus/sku-1/sale-price"), ({ request }) => answer(keys.price ?? [], request, {}, 200)),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = () => screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    await user.type(within(dialog()).getByRole("textbox", { name: "Số hóa đơn NCC (nếu có)" }), "HD-1");
    const sell = within(dialog()).getByRole("textbox", { name: "Giá bán lẻ – Bánh quy bơ" });
    await user.clear(sell);
    await user.type(sell, "18000");
    const post = () => user.click(within(dialog()).getByRole("button", { name: "Ghi sổ nhập kho" }));
    await post();
    expect(await screen.findByText(/nhưng chưa rõ đã gắn hóa đơn NCC hay chưa/)).toBeInTheDocument();
    await post();
    await waitFor(() => expect(keys.price).toHaveLength(1));
    await post();
    await waitFor(() => expect(onClose).toHaveBeenCalled());

    const [receipt] = keys.receipt ?? [];
    expect(keys.receipt).toEqual([receipt]);
    expect(receipt).toBeTruthy();
    expect(keys.invoice).toEqual([`${receipt}:invoice`, `${receipt}:invoice`]);
    expect(keys.price).toEqual([`${receipt}:sale-price:sku-1:retail:general:purchase_order:18000:1`, `${receipt}:sale-price:sku-1:retail:general:purchase_order:18000:1`]);
  });

  it("resends an invoice that got no answer even after its number was cleared, and claims the match only once it is answered", async () => {
    const invoices: Array<{ key: string | null; body: unknown }> = [];
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), () =>
        HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 }),
      ),
      http.post(apiUrl("/supplier-documents"), async ({ request }) => {
        invoices.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        return invoices.length === 1 ? HttpResponse.error() : HttpResponse.json({ id: "sd-1", document_no: "HD-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = () => screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    const invoiceNo = within(dialog()).getByRole("textbox", { name: "Số hóa đơn NCC (nếu có)" });
    await user.type(invoiceNo, "HD-1");
    const post = async () => user.click(await within(dialog()).findByRole("button", { name: "Ghi sổ nhập kho" }));
    await post();
    expect(await screen.findByText(/nhưng chưa rõ đã gắn hóa đơn NCC hay chưa/)).toBeInTheDocument();
    expect(screen.queryByText(PROCUREMENT_MESSAGES.invoiceMatchedAsSent("HD-1"), { exact: false })).not.toBeInTheDocument();

    await user.clear(invoiceNo);
    await post();
    expect(await screen.findByText(PROCUREMENT_MESSAGES.invoiceMatchedAsSent("HD-1"), { exact: false })).toBeInTheDocument();
    expect(invoices).toHaveLength(2);
    expect(invoices[1]).toEqual(invoices[0]);
    expect(invoices[0]?.body).toMatchObject({ document_no: "HD-1" });
    expect(onClose).not.toHaveBeenCalled();
  });

  it("asks before discarding a receipt with goods entered, and closes an untouched one at once", async () => {
    server.use(...lookups);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={null} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });

    await user.click(within(dialog).getByRole("button", { name: "+ Hàng hóa mới" }));
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(PROCUREMENT_MESSAGES.discardReceipt);
    expect(onClose).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Ở lại" }));
    expect(within(dialog).getByRole("textbox", { name: "Tên hàng mới" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(within(dialog).getByRole("alert")).toHaveTextContent(PROCUREMENT_MESSAGES.discardReceipt);
    await user.click(within(dialog).getByRole("button", { name: "Vẫn đóng" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes a quick receipt without asking while it holds no goods", async () => {
    server.use(...lookups);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={null} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });

    await user.click(within(dialog).getByRole("button", { name: "+ Hàng có sẵn" }));
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(screen.queryByText(PROCUREMENT_MESSAGES.discardReceipt)).not.toBeInTheDocument();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("asks a quick receipt for its supplier first", async () => {
    server.use(...lookups);
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={null} onClose={() => {}} />);

    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });
    expect(within(dialog).getByText("Không cần đơn nhập. Chọn hàng có sẵn hoặc thêm hàng hóa mới.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));
    expect(await screen.findByText(/Chọn nhà cung cấp\./)).toBeInTheDocument();
  });
});
