import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { HOME_PATH } from "../../../app/menu";
import { renderApp } from "../../../test/render";
import { AUTH_TEST, apiError, apiUrl, rateLimitedError, signedOut } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { AUTH_MESSAGES } from "../lib/auth-errors";
import { NOTICES } from "../lib/auth-state";
import { MESSAGES } from "../schemas";

const LOGIN_LABEL = /^Email hoặc số điện thoại/;

async function signIn(login = "chu@example.test", password = "mat-khau-dung") {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(LOGIN_LABEL), login);
  await user.type(screen.getByLabelText(/^Mật khẩu/), password);
  await user.click(screen.getByRole("button", { name: "Đăng nhập" }));
  return user;
}

describe("LoginPage", () => {
  it("focuses the e-mail or phone field when it opens", async () => {
    renderApp("/login");
    expect(await screen.findByLabelText(LOGIN_LABEL)).toHaveFocus();
  });

  it("signs in and continues to the page asked for in next", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/login"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ status: "signed_in", csrf_token: AUTH_TEST.signedInCsrf });
      }),
    );
    const { router } = renderApp(`/login?next=${encodeURIComponent("/inventory/stock?tab=a")}`);
    await signIn(" chu@example.test ", "mat-khau-dung");
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/stock"));
    expect(router.state.location.search).toBe("?tab=a");
    expect(body).toEqual({ login: "chu@example.test", password: "mat-khau-dung" });
  });

  it("signs in with the owner's phone number", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/login"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ status: "signed_in", csrf_token: AUTH_TEST.signedInCsrf });
      }),
    );
    const { router } = renderApp("/login");
    await signIn(" 0912 418 226 ", "mat-khau-dung");
    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));
    expect(body).toEqual({ login: "0912 418 226", password: "mat-khau-dung" });
  });

  it("ignores a next that points to another site", async () => {
    const { router } = renderApp(`/login?next=${encodeURIComponent("//evil.example/x")}`);
    await signIn();
    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));
  });

  it("goes to the MFA step with the challenge when TOTP is on", async () => {
    server.use(http.post(apiUrl("/auth/login"), () => HttpResponse.json({ status: "mfa_required", challenge: AUTH_TEST.challenge })));
    const { router } = renderApp("/login?next=/sales/invoices");
    await signIn();
    await waitFor(() => expect(router.state.location.pathname).toBe("/mfa"));
    expect(router.state.location.state).toEqual({ challenge: AUTH_TEST.challenge, next: "/sales/invoices" });
    expect(await screen.findByRole("heading", { level: 1, name: "Xác thực hai lớp" })).toBeInTheDocument();
  });

  it("clears and focuses the password after a wrong password", async () => {
    server.use(http.post(apiUrl("/auth/login"), () => apiError(401, "invalid_credentials")));
    renderApp("/login");
    await signIn();
    expect(await screen.findByRole("alert")).toHaveTextContent(AUTH_MESSAGES.invalidCredentials);
    const password = screen.getByLabelText(/^Mật khẩu/);
    expect(password).toHaveValue("");
    expect(password).toHaveFocus();
    expect(screen.getByLabelText(LOGIN_LABEL)).toHaveValue("chu@example.test");
  });

  it("states the wait after too many attempts", async () => {
    server.use(http.post(apiUrl("/auth/login"), () => rateLimitedError("rate_limited", 900)));
    renderApp("/login");
    await signIn();
    expect(await screen.findByRole("alert")).toHaveTextContent("Bạn đã thử quá nhiều lần. Thử lại sau 15 phút.");
  });

  it("puts the API's field errors on the fields", async () => {
    server.use(http.post(apiUrl("/auth/login"), () => apiError(422, "validation_failed", { fields: { login: "invalid" } })));
    renderApp("/login");
    await signIn();
    await screen.findByText(MESSAGES.loginInvalid);
    const login = screen.getByLabelText(LOGIN_LABEL);
    expect(login).toHaveAttribute("aria-invalid", "true");
    expect(login).toHaveAccessibleDescription(new RegExp(MESSAGES.loginInvalid));
    expect(screen.getByText(AUTH_MESSAGES.checkFields)).toBeInTheDocument();
  });

  it("checks the fields before sending anything", async () => {
    let sent = false;
    server.use(
      http.post(apiUrl("/auth/login"), () => {
        sent = true;
        return HttpResponse.json({ status: "signed_in" });
      }),
    );
    renderApp("/login");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Đăng nhập" }));
    expect(await screen.findByText(MESSAGES.loginRequired)).toBeInTheDocument();
    expect(screen.getByText(MESSAGES.passwordRequired)).toBeInTheDocument();
    await user.type(screen.getByLabelText(LOGIN_LABEL), "0612 418 226");
    await user.click(screen.getByRole("button", { name: "Đăng nhập" }));
    expect(await screen.findByText(MESSAGES.loginInvalid)).toBeInTheDocument();
    expect(sent).toBe(false);
  });

  it("shows the notice left by the previous screen", async () => {
    server.use(...signedOut);
    const { router } = renderApp("/");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    await router.navigate("/login", { state: { notice: NOTICES.passwordReset } });
    expect(await screen.findByRole("status")).toHaveTextContent(NOTICES.passwordReset);
  });

  it("links to the store finder and sign-up on the root host", async () => {
    renderApp("/login");
    expect(await screen.findByRole("link", { name: "Tìm cửa hàng của bạn" })).toHaveAttribute("href", "http://localhost:5173/find-store");
    expect(screen.getByRole("link", { name: "Đăng ký cửa hàng mới" })).toHaveAttribute("href", "http://localhost:5173/signup");
  });
});
