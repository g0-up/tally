import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../../../shared/ui/reauth-dialog";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { CUSTOMER_ID, testDocument } from "../testing/fixtures";
import { WriteOffDialog } from "./write-off-dialog";

const reasons = http.get(apiUrl("/reasons"), () =>
  HttpResponse.json({
    data: [
      { id: "rs-1", kind: "write_off", name: "Khách khó khăn", active: true },
      { id: "rs-2", kind: "write_off", name: "Chênh lệch làm tròn", active: true },
    ],
  }),
);

describe("WriteOffDialog", () => {
  it("asks for the password, then the owner's confirmation, and retries the same intent", async () => {
    const posts: { body: Record<string, unknown>; key: string | null }[] = [];
    const reauthBodies: unknown[] = [];
    let confirmation: unknown = null;
    server.use(
      reasons,
      http.post(apiUrl("/debt-adjustments"), async ({ request }) => {
        posts.push({ body: (await request.json()) as Record<string, unknown>, key: request.headers.get("Idempotency-Key") });
        if (posts.length === 1) return apiError(403, "reauth_required");
        if (posts.length === 2) return apiError(409, "exception_required", { exception_kind: "debt_write_off" });
        return HttpResponse.json({ id: "adj-1", doc_no: "DN-0001" }, { status: 201 });
      }),
      http.post(apiUrl(PATHS.reauth), async ({ request }) => {
        reauthBodies.push(await request.json());
        return HttpResponse.json({ reauthenticated_until: "2026-10-12T09:05:00Z" });
      }),
      http.post(apiUrl("/exception-confirmations"), async ({ request }) => {
        confirmation = await request.json();
        return HttpResponse.json({ id: "exc-1" });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<WriteOffDialog doc={testDocument()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Đề nghị giảm/xóa nợ SO-0001" });
    expect(within(dialog).getByRole("textbox", { name: "Số tiền" })).toHaveValue("3.000.000");
    await within(dialog).findByRole("option", { name: "Chênh lệch làm tròn" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: /^Lý do/ }), "rs-2");
    const amount = within(dialog).getByRole("textbox", { name: "Số tiền" });
    await user.clear(amount);
    await user.type(amount, "500000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi giảm nợ" }));

    const reauth = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    await user.type(within(reauth).getByLabelText("Mật khẩu"), "mat-khau");
    await user.click(within(reauth).getByRole("button", { name: REAUTH_SUBMIT }));

    const confirm = await screen.findByRole("dialog", { name: "Xác nhận xóa nợ SO-0001" });
    await user.click(within(confirm).getByRole("checkbox", { name: "Tôi xác nhận ghi giảm nợ" }));
    await user.click(within(confirm).getByRole("button", { name: "Ghi giảm nợ" }));

    expect(await screen.findByText("Đã ghi giảm nợ.")).toBeInTheDocument();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(reauthBodies).toEqual([{ password: "mat-khau" }]);
    expect(confirmation).toMatchObject({ kind: "debt_write_off", doc_type: "obligation", doc_id: "obl-1", doc_version: 2 });
    const intent = { party_id: CUSTOMER_ID, direction: "receivable", kind: "write_off", amount: "500000", target_obligation_id: "obl-1", reason_id: "rs-2" };
    expect(posts.map((p) => p.body)).toEqual([intent, intent, { ...intent, exception_confirmation_id: "exc-1" }]);
    expect(new Set(posts.map((p) => p.key)).size).toBe(1);
    expect(posts[0]?.key).toBeTruthy();
  });

  it("refuses more than the document still owes", async () => {
    let posted = false;
    server.use(
      reasons,
      http.post(apiUrl("/debt-adjustments"), () => {
        posted = true;
        return HttpResponse.json({}, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<WriteOffDialog doc={testDocument()} onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Đề nghị giảm/xóa nợ SO-0001" });
    await within(dialog).findByRole("option", { name: "Khách khó khăn" });
    const amount = within(dialog).getByRole("textbox", { name: "Số tiền" });
    const impact = (label: string) => (within(dialog).getByText(label).nextElementSibling?.textContent ?? "").replace(/\s/g, " ");
    expect(impact("Chứng từ còn nợ")).toBe("3.000.000 ₫");
    expect(impact("Còn nợ sau khi giảm")).toBe("0 ₫");
    await user.clear(amount);
    await user.type(amount, "1000000");
    expect(impact("Còn nợ sau khi giảm")).toBe("2.000.000 ₫");
    await user.clear(amount);
    await user.type(amount, "3000001");
    expect(impact("Còn nợ sau khi giảm")).toBe("—");
    await user.click(within(dialog).getByRole("button", { name: "Ghi giảm nợ" }));
    await waitFor(() => expect(amount).toHaveAccessibleDescription("Số tiền vượt số còn nợ của chứng từ."));
    expect(amount).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(amount).toHaveFocus());
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(posted).toBe(false);
  });

  it("marks the reason the server refuses and keeps the dialog", async () => {
    server.use(reasons, http.post(apiUrl("/debt-adjustments"), () => apiError(422, "validation_failed", { fields: { reason_id: "inactive" } })));
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<WriteOffDialog doc={testDocument()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Đề nghị giảm/xóa nợ SO-0001" });
    await within(dialog).findByRole("option", { name: "Khách khó khăn" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: /^Lý do/ }), "rs-1");
    await user.click(within(dialog).getByRole("button", { name: "Ghi giảm nợ" }));

    expect(await within(dialog).findByText("Đang ngừng dùng.")).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: /^Lý do/ })).toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).getByRole("textbox", { name: "Số tiền" })).not.toHaveAttribute("aria-invalid");
    expect(onClose).not.toHaveBeenCalled();
  });
});
