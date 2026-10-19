import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { SUPPLIER_ID, testOrder } from "../testing/fixtures";
import { PoAdvanceDialog } from "./po-advance-dialog";

describe("PoAdvanceDialog", () => {
  it("pays the supplier from the chosen fund as an advance earmarked for the order", async () => {
    let body: unknown = null;
    let idempotencyKey: string | null = null;
    server.use(
      http.get(apiUrl("/payment-methods"), () =>
        HttpResponse.json({
          data: [
            { id: "pm-cash", code: "cash", name: "Tiền mặt", active: true },
            { id: "pm-bank", code: "bank", name: "Chuyển khoản", active: true },
          ],
        }),
      ),
      http.post(apiUrl("/payments"), async ({ request }) => {
        body = await request.json();
        idempotencyKey = request.headers.get("Idempotency-Key");
        return HttpResponse.json({ id: "pay-1", doc_no: "PC-0001" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<PoAdvanceDialog order={testOrder()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ứng trước cho PO-0001" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu chi" }));
    expect(await screen.findByText(/Số tiền phải lớn hơn 0\./)).toBeInTheDocument();

    await user.type(within(dialog).getByRole("textbox", { name: "Số tiền" }), "500000");
    await within(dialog).findByRole("option", { name: "Chuyển khoản" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: /^Chi từ quỹ/ }), "pm-bank");
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu chi" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({
      direction: "out",
      party_id: SUPPLIER_ID,
      purpose: "advance",
      payment_method_id: "pm-bank",
      amount: "500000",
      source_type: "purchase",
      source_id: "po-1",
    });
    expect(idempotencyKey).toBeTruthy();
    expect(await screen.findByText("PC-0001")).toBeInTheDocument();
  });
});
