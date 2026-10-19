import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { at, CUSTOMER_ID, paymentLookups, testLedger } from "../testing/fixtures";
import { PayDialog } from "./pay-dialog";

const ledger = http.get(apiUrl(`/debts/parties/${CUSTOMER_ID}`), () => HttpResponse.json(testLedger()));

describe("PayDialog", () => {
  it("collects everything owed by default, oldest due first, as one receipt", async () => {
    let body: unknown = null;
    let idempotencyKey: string | null = null;
    server.use(
      ...paymentLookups,
      ledger,
      http.post(apiUrl("/payments"), async ({ request }) => {
        body = await request.json();
        idempotencyKey = request.headers.get("Idempotency-Key");
        return HttpResponse.json({ id: "pay-9", doc_no: "PT-0009", amount: "4500000", unallocated: "0" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<PayDialog direction="receivable" partyId={CUSTOMER_ID} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Thu tiền khách" });
    expect(within(dialog).getByText("Gợi ý phân bổ theo hạn cũ nhất; bạn xem và sửa trước khi ghi. Phần chưa phân bổ thành tiền ứng trước.")).toBeInTheDocument();
    expect(await within(dialog).findByDisplayValue("Tạp hóa Minh Châu")).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByRole("textbox", { name: "Số tiền" })).toHaveValue("4.500.000"));
    const lines = within(dialog).getAllByRole("textbox", { name: /^Phân bổ / });
    expect(lines.map((l) => (l as HTMLInputElement).value)).toEqual(["1.500.000", "3.000.000"]);
    expect(within(dialog).getAllByRole("row")[1]).toHaveTextContent("SO-0002");
    // Each line is named by its full document number, so the lines can be told apart without the table.
    expect(lines.map((l) => l.getAttribute("aria-label"))).toEqual(["Phân bổ SO-0002", "Phân bổ SO-0001"]);
    expect(within(dialog).getByRole("textbox", { name: "Phân bổ SO-0001" })).toHaveValue("3.000.000");

    await within(dialog).findByRole("option", { name: "Chuyển khoản" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: /^Vào quỹ/ }), "pm-bank");
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu thu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({
      direction: "in",
      party_id: CUSTOMER_ID,
      purpose: "settlement",
      payment_method_id: "pm-bank",
      amount: "4500000",
      source_type: "debt",
      allocations: [
        { obligation_id: "obl-2", amount: "1500000", mode: "suggested_oldest_due" },
        { obligation_id: "obl-1", amount: "3000000", mode: "suggested_oldest_due" },
      ],
    });
    expect(idempotencyKey).toBeTruthy();
    expect(await screen.findByText("PT-0009")).toBeInTheDocument();
  });

  it("re-flows the untouched lines around an edited one and keeps the rest as an advance", async () => {
    let body: { allocations?: unknown } | null = null;
    server.use(
      ...paymentLookups,
      ledger,
      http.post(apiUrl("/payments"), async ({ request }) => {
        body = (await request.json()) as typeof body;
        return HttpResponse.json({ id: "pay-9", doc_no: "PT-0009", amount: "2000000", unallocated: "500000" }, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<PayDialog direction="receivable" partyId={CUSTOMER_ID} onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Thu tiền khách" });
    const amount = within(dialog).getByRole("textbox", { name: "Số tiền" });
    await waitFor(() => expect(amount).toHaveValue("4.500.000"));
    await user.clear(amount);
    await user.type(amount, "2000000");
    await waitFor(() => expect(within(dialog).getAllByRole("textbox", { name: /^Phân bổ / }).map((l) => (l as HTMLInputElement).value)).toEqual(["1.500.000", "500.000"]));

    const first = at(within(dialog).getAllByRole("textbox", { name: /^Phân bổ / }), 0);
    await user.clear(first);
    await user.type(first, "1000000");
    // The edited line keeps 1.000.000; the other takes what is left of the 2.000.000.
    await waitFor(() => expect((within(dialog).getAllByRole("textbox", { name: /^Phân bổ / })[1] as HTMLInputElement).value).toBe("1.000.000"));
    expect(within(dialog).getByText("Thành tiền ứng trước").parentElement).toHaveTextContent("0");

    await within(dialog).findByRole("option", { name: "Tiền mặt" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu thu" }));
    await waitFor(() =>
      expect(body?.allocations).toEqual([
        { obligation_id: "obl-2", amount: "1000000", mode: "manual" },
        { obligation_id: "obl-1", amount: "1000000", mode: "suggested_oldest_due" },
      ]),
    );
  });

  it("blocks a line over its open amount and an allocation over the payment", async () => {
    let posted = false;
    server.use(
      ...paymentLookups,
      ledger,
      http.post(apiUrl("/payments"), () => {
        posted = true;
        return HttpResponse.json({}, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<PayDialog direction="receivable" partyId={CUSTOMER_ID} onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Thu tiền khách" });
    const amount = within(dialog).getByRole("textbox", { name: "Số tiền" });
    await waitFor(() => expect(amount).toHaveValue("4.500.000"));
    const first = at(within(dialog).getAllByRole("textbox", { name: /^Phân bổ / }), 0);
    await user.clear(first);
    await user.type(first, "1600000");
    expect(await within(dialog).findByText("Có dòng phân bổ vượt số còn nợ.")).toBeInTheDocument();
    expect(within(dialog).queryByText("Tổng phân bổ vượt số tiền.")).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Ghi phiếu thu" })).toBeDisabled();

    await user.clear(amount);
    await user.type(amount, "1000000");
    expect(await within(dialog).findByText("Tổng phân bổ vượt số tiền.")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Ghi phiếu thu" })).toBeDisabled();
    expect(posted).toBe(false);
  });

  it("pays a supplier with nothing open as an advance and shows the server refusal", async () => {
    server.use(
      ...paymentLookups,
      http.get(apiUrl("/debts/parties/sup-1"), () =>
        HttpResponse.json(testLedger({ party: { id: "sup-1", name: "NCC Bánh Kẹo Hải Hà", phone: null }, receivable: "0", payable: "0", documents: [] })),
      ),
      http.post(apiUrl("/payments"), () => apiError(409, "allocation_party_mismatch")),
    );
    const user = userEvent.setup();
    renderWithProviders(<PayDialog direction="payable" partyId="sup-1" onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Trả tiền nhà cung cấp" });
    expect(await within(dialog).findByText("Không có chứng từ còn mở. Toàn bộ số tiền sẽ thành ứng trước.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu chi" }));
    const amount = within(dialog).getByRole("textbox", { name: "Số tiền" });
    await waitFor(() => expect(amount).toHaveAccessibleDescription("Số tiền phải lớn hơn 0."));
    expect(amount).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(amount).toHaveFocus());
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();

    await user.type(amount, "800000");
    await within(dialog).findByRole("option", { name: "Tiền mặt" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu chi" }));
    expect(await screen.findByText(/Chứng từ phân bổ không thuộc đối tác này\./)).toBeInTheDocument();
  });

  it("finds any customer from the header, one with no balance included, and records the money as an advance", async () => {
    let body: unknown = null;
    server.use(
      ...paymentLookups,
      // Only the server's search finds her: she is in no debt list.
      http.get(apiUrl("/customers"), ({ request }) =>
        HttpResponse.json({
          data: new URL(request.url).searchParams.get("q") === "Cô Ba" ? [{ id: "cus-new", name: "Quán Cô Ba", phone: "0911000111", customer_group_name: null, credit_limit: null }] : [],
          next_cursor: null,
        }),
      ),
      http.get(apiUrl("/debts/parties/cus-new"), () =>
        HttpResponse.json(testLedger({ party: { id: "cus-new", name: "Quán Cô Ba", phone: "0911000111" }, receivable: "0", payable: "0", documents: [], advances: [] })),
      ),
      http.post(apiUrl("/payments"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "pay-9", doc_no: "PT-0009", amount: "500000", unallocated: "500000" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<PayDialog direction="receivable" partyId={null} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Thu tiền khách" });
    const picker = within(dialog).getByRole("combobox", { name: "Khách hàng" });
    await user.click(picker);
    await user.type(picker, "Cô Ba");
    await user.click(await screen.findByRole("option", { name: /Quán Cô Ba/ }));
    expect(await within(dialog).findByText("Không có chứng từ còn mở. Toàn bộ số tiền sẽ thành ứng trước.")).toBeInTheDocument();

    await user.type(within(dialog).getByRole("textbox", { name: "Số tiền" }), "500000");
    await within(dialog).findByRole("option", { name: "Tiền mặt" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu thu" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toMatchObject({ direction: "in", party_id: "cus-new", purpose: "advance", amount: "500000" });
    expect(body).not.toHaveProperty("allocations");
  });

  it("marks the amount and the allocation line the server refuses", async () => {
    server.use(
      ...paymentLookups,
      ledger,
      http.post(apiUrl("/payments"), () => apiError(422, "validation_failed", { fields: { amount: "invalid", "allocations[1].amount": "exceeds_open" } })),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<PayDialog direction="receivable" partyId={CUSTOMER_ID} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Thu tiền khách" });
    const amount = within(dialog).getByRole("textbox", { name: "Số tiền" });
    await waitFor(() => expect(amount).toHaveValue("4.500.000"));
    await within(dialog).findByRole("option", { name: "Tiền mặt" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi phiếu thu" }));

    expect(await within(dialog).findByText("Giá trị không hợp lệ.")).toBeInTheDocument();
    expect(amount).toHaveAttribute("aria-invalid", "true");
    // The second line sent is SO-0001 (oldest due first puts SO-0002 before it).
    await waitFor(() => expect(within(dialog).getByRole("textbox", { name: "Phân bổ SO-0001" })).toHaveAttribute("aria-invalid", "true"));
    expect(within(dialog).getByRole("textbox", { name: "Phân bổ SO-0002" })).not.toHaveAttribute("aria-invalid");
    expect(within(dialog).getByText(/SO-0001: Vượt số còn nợ\./)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });
});
