import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { CUSTOMER_ID, testLedger, testPayment } from "../testing/fixtures";
import { AllocateAdvanceDialog } from "./allocate-advance-dialog";

describe("AllocateAdvanceDialog", () => {
  const advance = testPayment({ amount: "2000000", unallocated: "2000000" });

  it("spreads what is left of the advance oldest due first", async () => {
    let body: unknown = null;
    let idempotencyKey: string | null = null;
    server.use(
      http.get(apiUrl(`/debts/parties/${CUSTOMER_ID}`), () => HttpResponse.json(testLedger({ advances: [advance] }))),
      http.post(apiUrl("/allocations"), async ({ request }) => {
        body = await request.json();
        idempotencyKey = request.headers.get("Idempotency-Key");
        return HttpResponse.json({ id: "pay-1", doc_no: "PT-0001", amount: "2000000", unallocated: "0" });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AllocateAdvanceDialog payment={advance} partyName="Tạp hóa Minh Châu" onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Phân bổ PT-0001" });
    expect(within(dialog).getByText("Còn 2.000.000 ₫ chưa phân bổ của Tạp hóa Minh Châu.")).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getAllByRole("textbox", { name: /^Phân bổ / }).map((l) => (l as HTMLInputElement).value)).toEqual(["1.500.000", "500.000"]));
    await user.click(within(dialog).getByRole("button", { name: "Ghi phân bổ" }));

    expect(await screen.findByText("Đã phân bổ.")).toBeInTheDocument();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({
      payment_id: "pay-1",
      allocations: [
        { obligation_id: "obl-2", amount: "1500000", mode: "suggested_oldest_due" },
        { obligation_id: "obl-1", amount: "500000", mode: "suggested_oldest_due" },
      ],
    });
    expect(idempotencyKey).toBeTruthy();
  });

  it("refuses more than the advance and more than a document owes", async () => {
    let posted = false;
    server.use(
      http.get(apiUrl(`/debts/parties/${CUSTOMER_ID}`), () => HttpResponse.json(testLedger({ advances: [advance] }))),
      http.post(apiUrl("/allocations"), () => {
        posted = true;
        return HttpResponse.json({});
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<AllocateAdvanceDialog payment={advance} partyName="Tạp hóa Minh Châu" onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Phân bổ PT-0001" });
    await waitFor(() => expect(within(dialog).getAllByRole("textbox", { name: /^Phân bổ / })).toHaveLength(2));
    const first = within(dialog).getByRole("textbox", { name: "Phân bổ SO-0002" });
    const second = within(dialog).getByRole("textbox", { name: "Phân bổ SO-0001" });
    expect(within(dialog).getAllByRole("textbox", { name: /^Phân bổ / })).toEqual([first, second]);
    await user.clear(second);
    await user.type(second, "1000000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi phân bổ" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Phân bổ vượt số tiền còn lại.");
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();

    await user.clear(second);
    await user.clear(first);
    await user.type(first, "1600000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi phân bổ" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(/Phân bổ vượt dư nợ SO-0002/);
    expect(first).toHaveAttribute("aria-invalid", "true");
    expect(second).not.toHaveAttribute("aria-invalid");
    await waitFor(() => expect(first).toHaveFocus());

    await user.clear(first);
    await user.click(within(dialog).getByRole("button", { name: "Ghi phân bổ" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Chưa nhập số tiền phân bổ.");
    expect(first).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(first).toHaveFocus());
    expect(posted).toBe(false);
  });
});
