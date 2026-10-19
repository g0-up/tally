import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { AUTH_TEST, adminApiUrl, apiError, apiUrl, rateLimitedError, signedOut } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { consoleHandlers } from "../../admin/testing/fixtures";
import { AUTH_MESSAGES } from "../lib/auth-errors";

async function passwordStep() {
  const user = userEvent.setup();
  const email = await screen.findByLabelText(/^Email/);
  expect(email).toHaveFocus();
  await user.type(email, "admin@example.test");
  await user.type(screen.getByLabelText(/^Mật khẩu/), "mat-khau-quan-tri");
  await user.click(screen.getByRole("button", { name: "Tiếp tục" }));
  return user;
}

describe("AdminLoginPage", () => {
  it("signs in with password then TOTP code and opens the console", async () => {
    const totpBodies: unknown[] = [];
    server.use(
      ...consoleHandlers,
      http.post(adminApiUrl("/auth/totp"), async ({ request }) => {
        totpBodies.push(await request.json());
        return HttpResponse.json({ status: "signed_in", csrf_token: AUTH_TEST.adminCsrf });
      }),
    );
    const { router } = renderApp("/admin/login", "admin");
    const user = await passwordStep();
    const code = await screen.findByLabelText(/^Mã xác thực/);
    await user.type(code, "654321");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/tenants"));
    expect(totpBodies).toEqual([{ challenge: AUTH_TEST.adminChallenge, code: "654321" }]);
  });

  it("sends a signed-out visitor of the console to the admin sign-in", async () => {
    server.use(...signedOut);
    const { router } = renderApp("/admin", "admin");
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/login"));
  });

  it("tells a wrong password apart from a wrong code", async () => {
    server.use(http.post(adminApiUrl("/auth/login"), () => apiError(401, "invalid_credentials")));
    renderApp("/admin/login", "admin");
    await passwordStep();
    expect(await screen.findByRole("alert")).toHaveTextContent(AUTH_MESSAGES.invalidCredentials);
    expect(screen.getByLabelText(/^Mật khẩu/)).toHaveValue("");

    server.resetHandlers();
    server.use(http.post(adminApiUrl("/auth/totp"), () => apiError(401, "invalid_code")));
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/^Mật khẩu/), "mat-khau-quan-tri");
    await user.click(screen.getByRole("button", { name: "Tiếp tục" }));
    await user.type(await screen.findByLabelText(/^Mã xác thực/), "000000");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    await screen.findByText(AUTH_MESSAGES.invalidCode);
    const code = screen.getByLabelText(/^Mã xác thực/);
    expect(code).toHaveAttribute("aria-invalid", "true");
    expect(code).toHaveAccessibleDescription(new RegExp(AUTH_MESSAGES.invalidCode));
    expect(code).toHaveValue("");
  });

  it("says how long to wait when sign-in attempts are throttled", async () => {
    server.use(http.post(adminApiUrl("/auth/login"), () => rateLimitedError("rate_limited", 120)));
    renderApp("/admin/login", "admin");
    await passwordStep();
    expect(await screen.findByRole("alert")).toHaveTextContent(`${AUTH_MESSAGES.tooManyAttempts} Thử lại sau 2 phút.`);
  });

  it("goes back to the password step when the challenge expired", async () => {
    server.use(http.post(adminApiUrl("/auth/totp"), () => apiError(401, "challenge_invalid")));
    renderApp("/admin/login", "admin");
    const user = await passwordStep();
    await user.type(await screen.findByLabelText(/^Mã xác thực/), "123456");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    expect(await screen.findByRole("status")).toHaveTextContent(AUTH_MESSAGES.challengeExpired);
    expect(screen.getByLabelText(/^Email/)).toHaveValue("admin@example.test");
  });

  it("never calls the store sign-in", async () => {
    let storeCalled = false;
    server.use(
      http.post(apiUrl("/auth/login"), () => {
        storeCalled = true;
        return HttpResponse.json({ status: "signed_in" });
      }),
    );
    renderApp("/admin/login", "admin");
    await passwordStep();
    await screen.findByLabelText(/^Mã xác thực/);
    expect(storeCalled).toBe(false);
  });
});
