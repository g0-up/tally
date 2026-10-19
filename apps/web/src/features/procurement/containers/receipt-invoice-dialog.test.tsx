import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { SUPPLIER_ID, testReceipt } from "../testing/fixtures";
import { ReceiptInvoiceDialog } from "./receipt-invoice-dialog";

describe("ReceiptInvoiceDialog", () => {
  it("matches the supplier invoice to the receipt without posting the payable again", async () => {
    let body: unknown = null;
    server.use(
      http.post(apiUrl("/supplier-documents"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "sd-1", document_no: "HD-889" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ReceiptInvoiceDialog receipt={testReceipt({ total_amount: "264000", tax_amount: "24000" })} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Đối chiếu hóa đơn NCC cho PN-0001" });
    await user.click(within(dialog).getByRole("button", { name: "Lưu đối chiếu" }));
    expect(await screen.findByText(/Nhập số hóa đơn\./)).toBeInTheDocument();

    await user.type(within(dialog).getByRole("textbox", { name: "Số hóa đơn" }), " HD-889 ");
    await user.click(within(dialog).getByRole("button", { name: "Lưu đối chiếu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({ supplier_party_id: SUPPLIER_ID, document_no: "HD-889", goods_amount: "240000", tax_amount: "24000", receipt_ids: ["rcv-1"] });
    expect(await screen.findByText("Đã đối chiếu hóa đơn.")).toBeInTheDocument();
  });

  it("shows the prototype sentence when the invoice number is already recorded", async () => {
    server.use(http.post(apiUrl("/supplier-documents"), () => apiError(409, "supplier_document_exists")));
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ReceiptInvoiceDialog receipt={testReceipt()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Đối chiếu hóa đơn NCC cho PN-0001" });
    await user.type(within(dialog).getByRole("textbox", { name: "Số hóa đơn" }), "HD-889");
    await user.click(within(dialog).getByRole("button", { name: "Lưu đối chiếu" }));

    expect(await screen.findByText(/Số hóa đơn này đã được ghi cho nhà cung cấp\./)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("resends a match that got no answer under the same key and body", async () => {
    const sent: Array<{ key: string | null; body: unknown }> = [];
    server.use(
      http.post(apiUrl("/supplier-documents"), async ({ request }) => {
        sent.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        if (sent.length === 1) return HttpResponse.error();
        return HttpResponse.json({ id: "sd-1", document_no: "HD-889" }, { status: 201, headers: { "Idempotent-Replay": "true" } });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ReceiptInvoiceDialog receipt={testReceipt()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Đối chiếu hóa đơn NCC cho PN-0001" });
    const number = within(dialog).getByRole("textbox", { name: "Số hóa đơn" });
    await user.type(number, "HD-889");
    await user.click(within(dialog).getByRole("button", { name: "Lưu đối chiếu" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(onClose).not.toHaveBeenCalled();

    // The number edited after the lost answer is not sent under the key of the first one.
    await user.type(number, "0");
    await user.click(within(dialog).getByRole("button", { name: "Lưu đối chiếu" }));
    expect(await screen.findByText(PROCUREMENT_MESSAGES.invoiceMatchedAsSent("HD-889"))).toBeInTheDocument();
    expect(sent).toHaveLength(2);
    expect(sent[0]?.key).toBeTruthy();
    expect(sent[1]).toEqual(sent[0]);
    expect(onClose).not.toHaveBeenCalled();
  });
});
