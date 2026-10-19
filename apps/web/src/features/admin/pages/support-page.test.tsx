import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { ADMIN_ID, COLLEAGUE, FORMER_ADMIN, TENANT_ID, consoleHandlers, listBody, pageBody, testSession, testTenant, testTicket } from "../testing/fixtures";
import { SESSIONS_TITLE, TICKETS_TABLE } from "./support-page";

const MINE = testTicket({ assigned_admin_id: ADMIN_ID, status: "in_progress" });
const UNASSIGNED = testTicket({ id: "0193a000-0000-7000-8000-0000000000t2", number: "HT-0013", subject: "Sai tồn kho sau khi nhập hàng" });
const COLLEAGUES = testTicket({ id: "0193a000-0000-7000-8000-0000000000t3", number: "HT-0014", assigned_admin_id: COLLEAGUE.id });
const FORMERS = testTicket({ id: "0193a000-0000-7000-8000-0000000000t4", number: "HT-0015", assigned_admin_id: FORMER_ADMIN.id });

describe("SupportPage", () => {
  it("lists tickets named after their assignee and the sessions granted to this admin", async () => {
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/support-tickets"), pageBody([MINE, UNASSIGNED, COLLEAGUES, FORMERS])),
      http.get(adminApiUrl("/support-sessions"), listBody([testSession()])),
    );
    renderApp("/admin/support", "admin");

    const tickets = await screen.findByRole("table", { name: TICKETS_TABLE });
    const mine = await within(tickets).findByRole("row", { name: /HT-0012/ });
    expect(within(mine).getByRole("link", { name: "HT-0012" })).toHaveAttribute("href", `/admin/support/tickets/${MINE.id}`);
    expect(mine).toHaveTextContent("Đang xử lý");
    expect(mine).toHaveTextContent("Trần Quản Trị (bạn)");
    expect(within(tickets).getByRole("row", { name: /HT-0013/ })).toHaveTextContent("Chưa phân công");
    expect(within(tickets).getByRole("row", { name: /HT-0014/ })).toHaveTextContent("Lê Thu Hỗ Trợ");
    expect(within(tickets).getByRole("row", { name: /HT-0015/ })).toHaveTextContent("Phạm Văn Cũ (ngừng hoạt động)");

    const sessions = await screen.findByRole("table", { name: SESSIONS_TITLE });
    const session = await within(sessions).findByRole("row", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ });
    expect(session).toHaveTextContent("Đang hiệu lực");
    expect(within(session).getByRole("link")).toHaveAttribute("href", `/admin/support/tenants/${TENANT_ID}/catalog`);
  });

  it("asks the API for the tickets of the chosen status and of a store found by its slug", async () => {
    const queries: Record<string, string>[] = [];
    const searched: (string | null)[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/tenants"), ({ request }) => {
        searched.push(new URL(request.url).searchParams.get("q"));
        return HttpResponse.json({ data: [testTenant()], next_cursor: null });
      }),
      http.get(adminApiUrl("/support-tickets"), ({ request }) => {
        queries.push(Object.fromEntries(new URL(request.url).searchParams));
        return HttpResponse.json({ data: [], next_cursor: null });
      }),
    );
    renderApp("/admin/support", "admin");
    const user = userEvent.setup();
    await screen.findByText("Không có yêu cầu khớp bộ lọc.");

    await user.selectOptions(screen.getByLabelText("Trạng thái yêu cầu"), "open");
    await user.type(screen.getByRole("combobox", { name: "Cửa hàng" }), "hoa-phat");
    // The picker searches the store list by what is typed.
    await waitFor(() => expect(searched).toContain("hoa-phat"));
    await user.click(await screen.findByRole("option", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ }));

    await waitFor(() => expect(queries.at(-1)).toEqual({ status: "open", tenant_id: TENANT_ID, limit: "50" }));
    expect(queries[0]).toEqual({ limit: "50" });
  });

  it("names the failure when the tickets cannot be read", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/support-tickets"), () => apiError(500, "internal_error")));
    renderApp("/admin/support", "admin");

    const alert = await screen.findByText(/^Không tải được yêu cầu hỗ trợ:/);
    expect(alert).toHaveTextContent("Máy chủ gặp lỗi. Thử lại sau ít phút.");
    // A failed read is not an empty list.
    expect(screen.queryByText("Không có yêu cầu khớp bộ lọc.")).not.toBeInTheDocument();
    // The sessions card reads on its own and still shows.
    expect(await screen.findByText("Chưa có cửa hàng nào cấp phiên hỗ trợ cho bạn.")).toBeInTheDocument();
  });
});
