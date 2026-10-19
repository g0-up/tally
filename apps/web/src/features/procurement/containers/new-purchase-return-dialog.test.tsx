import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { UNIT_HOP, UNIT_THUNG, testReceipt, testSku } from "../testing/fixtures";
import { NewPurchaseReturnDialog } from "./new-purchase-return-dialog";

/** PN-0001: 2 thùng (24 hộp) of lot L01 at 10.000 ₫ a hộp; 40.000 ₫ of its payable is still open. */
const receiptDetail = {
  ...testReceipt(),
  lines: [
    {
      id: "rl-1",
      line_no: 1,
      sku_id: "sku-1",
      sku_code: "SP001",
      name: "Bánh quy bơ",
      unit_id: UNIT_THUNG,
      unit_name: "thùng",
      unit_factor: "12",
      qty: "2",
      base_qty: "24",
      condition: "accepted",
      lot_no: "L01",
      lot_id: "lot-1",
      unit_price: "120000",
      line_amount: "240000",
      returnable_base_qty: "24",
    },
  ],
  payable: { id: "ap-1", amount: "240000", settled_amount: "200000", open_amount: "40000", status: "partially_settled" },
};

describe("NewPurchaseReturnDialog", () => {
  it("returns base units out of the receipt's lot and reduces the payable before any refund", async () => {
    let body: unknown = null;
    server.use(
      http.get(apiUrl("/receipts"), () => HttpResponse.json({ data: [testReceipt()], next_cursor: null })),
      http.get(apiUrl("/receipts/rcv-1"), () => HttpResponse.json(receiptDetail)),
      http.get(apiUrl("/lots"), () =>
        HttpResponse.json({ data: [{ id: "lot-1", sku_id: "sku-1", lot_no: "L01", color_id: null, expiry_date: null, is_quarantine: false, qty: "20", state: "active" }] }),
      ),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/reasons"), () =>
        HttpResponse.json({
          data: [
            { id: "rs-1", kind: "purchase_return", name: "Hàng cận hạn", active: true },
            { id: "rs-2", kind: "purchase_return", name: "Hàng lỗi bao bì", active: true },
          ],
        }),
      ),
      http.post(apiUrl("/purchase-returns"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "xt-1", doc_no: "XT-0001" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewPurchaseReturnDialog receiptId="rcv-1" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Trả hàng nhà cung cấp" });
    const qty = await within(dialog).findByRole("textbox", { name: "SL trả – Bánh quy bơ" });
    const row = qty.closest("tr") as HTMLElement;
    await waitFor(() => expect(within(row).getByText("20")).toBeInTheDocument());
    expect(within(row).getByText("L01")).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: /^Lý do/ })).toHaveValue("rs-2"));

    await user.type(qty, "30");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ xuất trả" }));
    expect(await screen.findByText(/Bánh quy bơ: số lượng trả vượt số còn trả được của dòng nhận\./)).toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "6");
    const impact = (label: string) => within(dialog).getByText(label).parentElement as HTMLElement;
    expect(impact("Giá trị trả")).toHaveTextContent(/60\.000/);
    expect(impact("Giảm nợ NCC")).toHaveTextContent(/40\.000/);
    expect(impact("NCC cần hoàn")).toHaveTextContent(/20\.000/);

    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ xuất trả" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({ receipt_id: "rcv-1", reason_id: "rs-2", settlement: "credit_payable", lines: [{ receipt_line_id: "rl-1", unit_id: UNIT_HOP, qty: "6" }] });
    expect(await screen.findByText("Đã ghi phiếu trả NCC.")).toBeInTheDocument();
  });

  it("says when there is no receipt to return from", async () => {
    server.use(
      http.get(apiUrl("/receipts"), () => HttpResponse.json({ data: [], next_cursor: null })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/reasons"), () => HttpResponse.json({ data: [] })),
    );
    renderWithProviders(<NewPurchaseReturnDialog onClose={() => {}} />);
    expect(await screen.findByText("Chưa có phiếu nhận để trả.")).toBeInTheDocument();
  });
});
