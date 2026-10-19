import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { ADMIN_LOGOUT_LABEL, ADMIN_NAV_LABEL } from "../../app/layouts/admin-shell";
import { SKIP_LINK_LABEL } from "../../app/layouts/app-shell";
import { renderApp } from "../../test/render";
import { adminApiUrl } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { consoleHandlers } from "./testing/fixtures";

describe("admin console shell", () => {
  it("links every console page from its own navigation", async () => {
    server.use(...consoleHandlers);
    renderApp("/admin", "admin");
    const nav = await screen.findByRole("navigation", { name: ADMIN_NAV_LABEL });
    const links = Array.from(nav.querySelectorAll("a")).map((a) => [a.textContent, a.getAttribute("href")]);
    expect(links).toEqual([
      ["Cửa hàng", "/admin/tenants"],
      ["Mức sử dụng", "/admin/usage"],
      ["Hóa đơn thuê bao", "/admin/invoices"],
      ["Sự cố", "/admin/incidents"],
      ["Cờ tính năng", "/admin/feature-flags"],
      ["Hỗ trợ", "/admin/support"],
      ["Đối tác giới thiệu", "/admin/partners"],
    ]);
  });

  it("marks the open section and draws the focus ring in the colour that shows on the dark header", async () => {
    server.use(...consoleHandlers);
    renderApp("/admin/tenants", "admin");
    const nav = await screen.findByRole("navigation", { name: ADMIN_NAV_LABEL });
    expect(within(nav).getByRole("link", { name: "Cửa hàng" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", { name: "Sự cố" })).not.toHaveAttribute("aria-current");
    for (const link of within(nav).getAllByRole("link")) expect(link.style.outlineColor).toBe("var(--on-dark-focus)");
    expect(screen.getByRole("button", { name: ADMIN_LOGOUT_LABEL }).style.outlineColor).toBe("var(--on-dark-focus)");
  });

  it("starts with a skip link that moves focus to the page content", async () => {
    server.use(...consoleHandlers);
    renderApp("/admin/tenants", "admin");
    const user = userEvent.setup();
    await screen.findByRole("navigation", { name: ADMIN_NAV_LABEL });
    await user.tab();
    const skip = screen.getByRole("link", { name: SKIP_LINK_LABEL });
    expect(skip).toHaveFocus();
    await user.click(skip);
    expect(screen.getByRole("main")).toHaveFocus();
  });

  it("names the signed-in admin in the header, with the email on hover and for screen readers", async () => {
    server.use(...consoleHandlers);
    renderApp("/admin/tenants", "admin");
    expect(await screen.findByText("Trần Quản Trị")).toHaveAttribute("title", "quantri@tally.vn");
    expect(screen.getByText("quantri@tally.vn")).toHaveClass("sr-only");
  });

  it("signs out on the admin API and sends the next request with the new CSRF token", async () => {
    let loggedOut = 0;
    const loginTokens: (string | null)[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.post(adminApiUrl("/auth/logout"), () => {
        loggedOut += 1;
        return HttpResponse.json({ csrf_token: "admin-csrf-signed-out" });
      }),
      http.post(adminApiUrl("/auth/login"), ({ request }) => {
        loginTokens.push(request.headers.get("X-CSRF-Token"));
        return HttpResponse.json({ status: "mfa_required", challenge: "admin-challenge-2" });
      }),
    );
    const { router } = renderApp("/admin/tenants", "admin");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: ADMIN_LOGOUT_LABEL }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/login"));
    expect(loggedOut).toBe(1);

    await user.type(await screen.findByLabelText(/^Email/), "admin@example.test");
    await user.type(screen.getByLabelText(/^Mật khẩu/), "mat-khau-quan-tri");
    await user.click(screen.getByRole("button", { name: "Tiếp tục" }));
    await screen.findByLabelText(/^Mã xác thực/);
    expect(loginTokens).toEqual(["admin-csrf-signed-out"]);
  });
});
