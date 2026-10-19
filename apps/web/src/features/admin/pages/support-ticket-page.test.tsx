import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { ADMIN_ID, COLLEAGUE, FORMER_ADMIN, consoleHandlers, heldAnswer, testTicketView } from "../testing/fixtures";

const VIEW = testTicketView();
const { messages: _messages, ...TICKET } = VIEW;
const PATH = `/admin/support/tickets/${VIEW.id}`;

function withTicket(view = VIEW) {
  server.use(...consoleHandlers);
  server.use(http.get(adminApiUrl("/support-tickets/:id"), () => HttpResponse.json(view)));
}

function card(title: string): HTMLElement {
  return screen.getByRole("heading", { name: title }).closest(".card") as HTMLElement;
}

describe("SupportTicketPage", () => {
  it("shows the conversation and who the ticket is assigned to", async () => {
    withTicket();
    renderApp(PATH, "admin");

    expect(await screen.findByRole("heading", { name: "Không in được hóa đơn" })).toBeInTheDocument();
    expect(card("Trao đổi")).toHaveTextContent("Chủ cửa hàng");
    expect(card("Trao đổi")).toHaveTextContent("Máy in báo lỗi khi in hóa đơn.");
    expect(card("Thông tin")).toHaveTextContent("Mới");
    expect(card("Thông tin")).toHaveTextContent("Chưa phân công");
    expect(within(card("Phân công")).getByRole("button", { name: "Giao cho tôi" })).toBeInTheDocument();
  });

  it("names the signed-in admin as the assignee and offers no self-assignment", async () => {
    withTicket(testTicketView({ assigned_admin_id: ADMIN_ID }));
    renderApp(PATH, "admin");

    await screen.findByRole("heading", { name: "Không in được hóa đơn" });
    expect(card("Thông tin")).toHaveTextContent("Trần Quản Trị (bạn)");
    expect(within(card("Phân công")).queryByRole("button", { name: "Giao cho tôi" })).not.toBeInTheDocument();
    expect(within(card("Phân công")).getByRole("button", { name: "Bỏ phân công" })).toBeInTheDocument();
  });

  it("names the failure when the ticket cannot be read", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/support-tickets/:id"), () => apiError(404, "not_found")));
    renderApp(PATH, "admin");

    const alert = await screen.findByText(/^Không tải được yêu cầu hỗ trợ:/);
    expect(alert).toHaveTextContent("Không tìm thấy dữ liệu, có thể đã bị xóa.");
    expect(screen.queryByRole("heading", { name: "Trả lời" })).not.toBeInTheDocument();
  });

  it("assigns the ticket to the signed-in admin and confirms it only once the API stored it", async () => {
    const patches: { body: unknown; idempotencyKey: string | null }[] = [];
    const answer = heldAnswer();
    withTicket();
    server.use(
      http.patch(adminApiUrl("/support-tickets/:id"), async ({ request }) => {
        patches.push({ body: await request.json(), idempotencyKey: request.headers.get("Idempotency-Key") });
        await answer.held;
        return HttpResponse.json({ ...TICKET, assigned_admin_id: ADMIN_ID, updated_at: "2026-10-18T02:00:00Z" });
      }),
    );
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Không in được hóa đơn" });
    await user.click(within(card("Phân công")).getByRole("button", { name: "Giao cho tôi" }));

    await waitFor(() => expect(patches).toHaveLength(1));
    // The admin API takes no Idempotency-Key; the request goes out without one.
    expect(patches[0]).toEqual({ body: { assigned_admin_id: ADMIN_ID }, idempotencyKey: null });
    expect(screen.queryByText(/Đã phân công/)).not.toBeInTheDocument();
    // Only the button that sent the change waits; the other one is held without claiming the wait.
    const assignCard = card("Phân công");
    expect(within(assignCard).getByRole("button", { name: "Đang ghi sổ…" })).toHaveAttribute("aria-busy", "true");
    const assign = within(assignCard).getByRole("button", { name: "Phân công" });
    expect(assign).toBeDisabled();
    expect(assign).not.toHaveAttribute("aria-busy");

    answer.release();
    expect(await screen.findByText(/Đã phân công/)).toHaveTextContent("Đã phân công HT-0012.");
  });

  it("assigns the ticket to an active admin picked from the directory", async () => {
    const patches: unknown[] = [];
    withTicket();
    server.use(
      http.patch(adminApiUrl("/support-tickets/:id"), async ({ request }) => {
        patches.push(await request.json());
        return HttpResponse.json({ ...TICKET, assigned_admin_id: COLLEAGUE.id, updated_at: "2026-10-18T02:00:00Z" });
      }),
    );
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Không in được hóa đơn" });
    const assign = card("Phân công");

    await user.click(within(assign).getByRole("button", { name: "Phân công" }));
    expect(within(assign).getByRole("alert")).toHaveTextContent("Chọn người nhận việc.");
    expect(patches).toEqual([]);

    const picker = within(assign).getByLabelText(/^Người nhận việc/);
    await within(picker).findByRole("option", { name: "Lê Thu Hỗ Trợ (hotro@tally.vn)" });
    expect(within(picker).getByRole("option", { name: "Trần Quản Trị (quantri@tally.vn) (bạn)" })).toBeInTheDocument();
    // An admin who no longer works on the platform cannot receive work.
    expect(within(picker).queryByRole("option", { name: new RegExp(FORMER_ADMIN.full_name) })).not.toBeInTheDocument();
    await user.selectOptions(picker, COLLEAGUE.id);
    await user.click(within(assign).getByRole("button", { name: "Phân công" }));

    expect(await screen.findByText(/Đã phân công/)).toHaveTextContent("Đã phân công HT-0012.");
    expect(patches).toEqual([{ assigned_admin_id: COLLEAGUE.id }]);
  });

  it("sends a reply and clears the box", async () => {
    const posts: unknown[] = [];
    withTicket();
    server.use(
      http.post(adminApiUrl("/support-tickets/:id/messages"), async ({ request }) => {
        posts.push(await request.json());
        return HttpResponse.json(
          { id: "0193a000-0000-7000-8000-0000000000m2", author_type: "platform_admin", author_id: ADMIN_ID, body: "Cài lại máy in.", attachment_ids: [], created_at: "2026-10-18T02:00:00Z" },
          { status: 201 },
        );
      }),
    );
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Không in được hóa đơn" });
    const reply = within(card("Trả lời"));

    await user.click(reply.getByRole("button", { name: "Gửi trả lời" }));
    expect(reply.getByText("Nhập nội dung trả lời.")).toBeInTheDocument();
    expect(posts).toEqual([]);

    await user.type(reply.getByLabelText(/^Nội dung trả lời/), "  Cài lại máy in.  ");
    await user.click(reply.getByRole("button", { name: "Gửi trả lời" }));

    expect(await screen.findByText(/Đã gửi trả lời cho/)).toHaveTextContent("Đã gửi trả lời cho HT-0012.");
    expect(posts).toEqual([{ body: "Cài lại máy in." }]);
    await waitFor(() => expect(reply.getByLabelText(/^Nội dung trả lời/)).toHaveValue(""));
  });

  it("keeps the reply and names the refusal when the ticket was closed meanwhile", async () => {
    withTicket();
    server.use(http.post(adminApiUrl("/support-tickets/:id/messages"), () => apiError(409, "ticket_closed")));
    renderApp(PATH, "admin");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Không in được hóa đơn" });
    const reply = within(card("Trả lời"));
    await user.type(reply.getByLabelText(/^Nội dung trả lời/), "Đã kiểm tra xong.");
    await user.click(reply.getByRole("button", { name: "Gửi trả lời" }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent("Chưa ghi sổ.");
    expect(toast).toHaveTextContent("Yêu cầu hỗ trợ đã đóng.");
    expect(screen.queryByText(/Đã gửi trả lời cho/)).not.toBeInTheDocument();
    expect(reply.getByLabelText(/^Nội dung trả lời/)).toHaveValue("Đã kiểm tra xong.");
  });
});
