import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { HOME_PATH } from "../../../app/menu";
import { renderApp } from "../../../test/render";
import { AUTH_TEST, TEST_ME, apiError, apiUrl } from "../../../test/msw/handlers";
import { PATHS } from "../../../shared/api/endpoints";
import { server } from "../../../test/msw/server";
import { exportKeys } from "../../reports/api/queries";
import { setStoredExport, storedExport } from "../../reports/export-job-store";
import { settingsKeys } from "../../settings";
import { NOTICES } from "../lib/auth-state";

describe("LogoutButton", () => {
  it("signs out with the session's CSRF token and lands on /login", async () => {
    const tokens: Array<string | null> = [];
    server.use(
      // The session answer carries no token here, so only the login answer can have set it.
      http.get(apiUrl(PATHS.me), () => HttpResponse.json({ ...TEST_ME, csrf_token: undefined })),
      http.post(apiUrl("/auth/logout"), ({ request }) => {
        tokens.push(request.headers.get("X-CSRF-Token"));
        return HttpResponse.json({ csrf_token: AUTH_TEST.signedOutCsrf });
      }),
    );
    // Signing in first proves the token the login answer carried is the one used afterwards.
    const { router } = renderApp("/login");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/^Email/), "chu@example.test");
    await user.type(screen.getByLabelText(/^Mật khẩu/), "mat-khau-dung");
    await user.click(screen.getByRole("button", { name: "Đăng nhập" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));

    await user.click(await screen.findByRole("button", { name: "Đăng xuất" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    // The lazy login route shows its own "Đang tải…" status first.
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(NOTICES.signedOut));
    expect(tokens).toEqual([AUTH_TEST.signedInCsrf]);
  });

  it("still lands on /login when the session had already ended", async () => {
    server.use(http.post(apiUrl("/auth/logout"), () => apiError(401, "unauthenticated")));
    const { router } = renderApp(HOME_PATH);
    await userEvent.setup().click(await screen.findByRole("button", { name: "Đăng xuất" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("stays and reports any other failure", async () => {
    server.use(http.post(apiUrl("/auth/logout"), () => apiError(500, "internal_error")));
    const { router } = renderApp(HOME_PATH);
    await userEvent.setup().click(await screen.findByRole("button", { name: "Đăng xuất" }));
    expect(await screen.findByText(/Không đăng xuất được/)).toBeInTheDocument();
    expect(router.state.location.pathname).toBe(HOME_PATH);
  });
});

describe("a change of session", () => {
  it("forgets the signed-out user's cached answers and the exports the tab was following", async () => {
    server.use(http.post(apiUrl("/auth/logout"), () => HttpResponse.json({ csrf_token: AUTH_TEST.signedOutCsrf })));
    const { router, platform } = renderApp(HOME_PATH);
    const logout = await screen.findByRole("button", { name: "Đăng xuất" });
    platform.queryClient.setQueryData(exportKeys.recent(), { data: [] });
    platform.queryClient.setQueryData(settingsKeys.recentExports(), { data: [] });
    setStoredExport("gross-profit", { id: "exp-1", round: 0 });
    expect(window.sessionStorage.getItem("tally.report-exports")).toContain("exp-1");

    await userEvent.setup().click(logout);
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    await waitFor(() => expect(platform.queryClient.getQueryData(exportKeys.recent())).toBeUndefined());
    expect(platform.queryClient.getQueryData(settingsKeys.recentExports())).toBeUndefined();
    expect(storedExport("gross-profit")).toBeNull();
    expect(window.sessionStorage.getItem("tally.report-exports")).toBeNull();
  });

  it("starts whoever signs in after an ended session without the previous user's exports", async () => {
    const { router, platform } = renderApp("/login");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/^Email/), "chu@example.test");
    platform.queryClient.setQueryData(exportKeys.recent(), { data: [] });
    setStoredExport("gross-profit", { id: "exp-1", round: 0 });

    await user.type(screen.getByLabelText(/^Mật khẩu/), "mat-khau-dung");
    await user.click(screen.getByRole("button", { name: "Đăng nhập" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));
    expect(platform.queryClient.getQueryData(exportKeys.recent())).toBeUndefined();
    expect(storedExport("gross-profit")).toBeNull();
    expect(window.sessionStorage.getItem("tally.report-exports")).toBeNull();
  });
});
