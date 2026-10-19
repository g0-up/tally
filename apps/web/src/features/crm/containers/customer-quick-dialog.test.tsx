import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { CLOSE_UNSETTLED_MESSAGE, OUTCOME_UNKNOWN_MESSAGE } from "../../../shared/hooks/create-once";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { testCustomer } from "../testing/fixtures";
import { CustomerQuickDialog } from "./customer-quick-dialog";

describe("CustomerQuickDialog", () => {
  it("sends the create again under the same key when its answer was lost, and selects the customer once", async () => {
    const keys: (string | null)[] = [];
    server.use(
      http.post(apiUrl("/customers"), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        if (keys.length === 1) return HttpResponse.error();
        return HttpResponse.json({ ...testCustomer({ id: "cus-new", name: "Cô Ba Bình", version: 1 }), duplicate_warnings: [] }, { status: 201 });
      }),
    );
    const onCreated = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerQuickDialog onClose={() => {}} onCreated={onCreated} />);

    const dialog = await screen.findByRole("dialog", { name: "Thêm khách hàng mới" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Tên khách hàng \*/ }), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));
    await waitFor(() => expect(keys).toHaveLength(1));
    expect(onCreated).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));
    expect(await screen.findByText(/^Đã thêm/)).toHaveTextContent("Đã thêm Cô Ba Bình và chọn sẵn.");
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
    expect(onCreated.mock.calls[0]?.[0]).toMatchObject({ id: "cus-new" });
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(/.+/);
    expect(keys[1]).toBe(keys[0]);
  });

  it("checks a create whose answer was lost before writing the phone edited since onto the customer it stored", async () => {
    const posts: { key: string | null; body: unknown }[] = [];
    const patches: { ifMatch: string | null; body: unknown }[] = [];
    const stored = { ...testCustomer({ id: "cus-new", name: "Cô Ba Bình", phone: null, version: 1 }), duplicate_warnings: [] };
    server.use(
      http.post(apiUrl("/customers"), async ({ request }) => {
        posts.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        // The first one is stored, but its answer never arrives.
        return posts.length === 1 ? HttpResponse.error() : HttpResponse.json(stored, { status: 201 });
      }),
      http.patch(apiUrl("/customers/cus-new"), async ({ request }) => {
        const body = (await request.json()) as { phone: string };
        patches.push({ ifMatch: request.headers.get("If-Match"), body });
        return HttpResponse.json({ ...stored, phone: body.phone, version: 2 });
      }),
    );
    const onCreated = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerQuickDialog onClose={() => {}} onCreated={onCreated} />);

    const dialog = await screen.findByRole("dialog", { name: "Thêm khách hàng mới" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Tên khách hàng \*/ }), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));
    expect(await screen.findByText(OUTCOME_UNKNOWN_MESSAGE)).toBeInTheDocument();
    await user.type(within(dialog).getByRole("textbox", { name: /^Số điện thoại/ }), "0903123456");
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
    expect(onCreated.mock.calls[0]?.[0]).toMatchObject({ id: "cus-new", phone: "0903123456", version: 2 });
    expect(posts).toHaveLength(2);
    expect(posts[1]).toEqual(posts[0]);
    expect(posts[0]?.body).toMatchObject({ name: "Cô Ba Bình", phone: null });
    expect(patches).toEqual([{ ifMatch: etagOf(1), body: expect.objectContaining({ name: "Cô Ba Bình", phone: "0903123456" }) as unknown }]);
  });

  it("marks the name under its field and moves focus there when it is missing, with nothing sent", async () => {
    let posted = false;
    server.use(
      http.post(apiUrl("/customers"), () => {
        posted = true;
        return HttpResponse.json({}, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<CustomerQuickDialog onClose={vi.fn()} onCreated={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: "Thêm khách hàng mới" });
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));
    const name = within(dialog).getByRole("textbox", { name: /^Tên khách hàng \*/ });
    await waitFor(() => expect(name).toHaveFocus());
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription("Nhập tên khách hàng.");
    expect(posted).toBe(false);
  });

  it("asks before leaving while the create may have been stored without an answer", async () => {
    server.use(http.post(apiUrl("/customers"), () => HttpResponse.error()));
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerQuickDialog onClose={onClose} onCreated={() => {}} />);

    const dialog = await screen.findByRole("dialog", { name: "Thêm khách hàng mới" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Tên khách hàng \*/ }), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));
    expect(await screen.findByText(OUTCOME_UNKNOWN_MESSAGE)).toBeInTheDocument();
    expect(screen.queryByText(/Chưa ghi sổ/)).not.toBeInTheDocument();

    expect(within(dialog).getByRole("button", { name: "Quay lại" })).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(CLOSE_UNSETTLED_MESSAGE);
    expect(onClose).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Vẫn đóng" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
