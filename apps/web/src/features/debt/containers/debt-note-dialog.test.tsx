import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { CLOSE_UNSETTLED_MESSAGE, OUTCOME_UNKNOWN_MESSAGE } from "../../../shared/hooks/create-once";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { CUSTOMER_ID } from "../testing/fixtures";
import { DebtNoteDialog } from "./debt-note-dialog";

describe("DebtNoteDialog", () => {
  it("records the call with the date the customer promised, a week out by default", async () => {
    let body: unknown = null;
    server.use(
      http.get(apiUrl(`/customers/${CUSTOMER_ID}/consents`), () => HttpResponse.json({ current: [{ channel: "zalo", purpose: "marketing", granted: true }] })),
      http.post(apiUrl(`/parties/${CUSTOMER_ID}/collection-notes`), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "cn-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<DebtNoteDialog partyId={CUSTOMER_ID} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi chú nhắc nợ" });
    expect(await within(dialog).findByText("Khách chưa đồng ý nhận tin nhắn. Chỉ ghi chú liên hệ trực tiếp.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Lưu ghi chú" }));

    expect(await screen.findByText("Đã lưu ghi chú.")).toBeInTheDocument();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({ direction: "receivable", kind: "promise", body: "Đã gọi nhắc nợ", promised_date: "2026-10-19", channel: "phone" });
  });

  it("marks the promised date the server refuses and keeps the dialog", async () => {
    server.use(
      http.get(apiUrl(`/customers/${CUSTOMER_ID}/consents`), () => HttpResponse.json({ current: [] })),
      http.post(apiUrl(`/parties/${CUSTOMER_ID}/collection-notes`), () => apiError(422, "validation_failed", { fields: { promised_date: "invalid" } })),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<DebtNoteDialog partyId={CUSTOMER_ID} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi chú nhắc nợ" });
    await user.click(within(dialog).getByRole("button", { name: "Lưu ghi chú" }));

    expect(await within(dialog).findByText("Giá trị không hợp lệ.")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Khách hẹn trả ngày")).toHaveAttribute("aria-invalid", "true");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("saves the note again under the same Idempotency-Key after a lost answer", async () => {
    const keys: (string | null)[] = [];
    server.use(
      http.get(apiUrl(`/customers/${CUSTOMER_ID}/consents`), () => HttpResponse.json({ current: [] })),
      http.post(apiUrl(`/parties/${CUSTOMER_ID}/collection-notes`), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json({ id: "cn-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<DebtNoteDialog partyId={CUSTOMER_ID} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi chú nhắc nợ" });
    await user.click(within(dialog).getByRole("button", { name: "Lưu ghi chú" }));
    await waitFor(() => expect(keys).toHaveLength(1));
    await user.click(await within(dialog).findByRole("button", { name: "Lưu ghi chú" }));

    expect(await screen.findByText("Đã lưu ghi chú.")).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(/.+/);
    expect(keys[1]).toBe(keys[0]);
  });

  it("checks a note whose answer was lost before anything typed since, and says the edit was not saved", async () => {
    const bodies: unknown[] = [];
    const keys: (string | null)[] = [];
    server.use(
      http.get(apiUrl(`/customers/${CUSTOMER_ID}/consents`), () => HttpResponse.json({ current: [] })),
      http.post(apiUrl(`/parties/${CUSTOMER_ID}/collection-notes`), async ({ request }) => {
        bodies.push(await request.json());
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json({ id: "cn-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<DebtNoteDialog partyId={CUSTOMER_ID} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi chú nhắc nợ" });
    await user.click(within(dialog).getByRole("button", { name: "Lưu ghi chú" }));
    expect(await screen.findByText(OUTCOME_UNKNOWN_MESSAGE)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(CLOSE_UNSETTLED_MESSAGE);
    await user.click(within(dialog).getByRole("button", { name: "Ở lại" }));

    await user.type(within(dialog).getByLabelText("Nội dung"), "Khách hẹn chuyển khoản");
    await user.click(within(dialog).getByRole("button", { name: "Lưu ghi chú" }));

    expect(await screen.findByText("Đã lưu ghi chú của lần bấm trước, lần đó chưa nhận được trả lời. Nội dung sửa sau đó chưa được lưu.")).toBeInTheDocument();
    expect(bodies).toHaveLength(2);
    expect(bodies[1]).toEqual(bodies[0]);
    expect(keys[1]).toBe(keys[0]);
    expect(onClose).not.toHaveBeenCalled();
  });
});
