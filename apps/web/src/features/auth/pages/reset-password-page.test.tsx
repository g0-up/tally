import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { AUTH_TEST, apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { AUTH_MESSAGES } from "../lib/auth-errors";
import { NOTICES } from "../lib/auth-state";
import { MESSAGES } from "../schemas";

async function typePasswords(password: string, confirm: string) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(/^Mật khẩu mới/), password);
  await user.type(screen.getByLabelText(/^Nhập lại mật khẩu/), confirm);
  await user.click(screen.getByRole("button", { name: "Lưu mật khẩu mới" }));
}

describe("ResetPasswordPage", () => {
  it("sets the new password and sends the owner to sign in", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/password-reset/confirm"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ csrf_token: AUTH_TEST.signedOutCsrf });
      }),
    );
    const { router } = renderApp("/reset-password?token=tok-r");
    await typePasswords("mat-khau-moi", "mat-khau-moi");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(body).toEqual({ token: "tok-r", password: "mat-khau-moi" });
    expect(await screen.findByRole("status")).toHaveTextContent(NOTICES.passwordReset);
  });

  it("catches a mismatched confirmation before sending", async () => {
    let sent = false;
    server.use(
      http.post(apiUrl("/auth/password-reset/confirm"), () => {
        sent = true;
        return HttpResponse.json({ csrf_token: "x" });
      }),
    );
    renderApp("/reset-password?token=tok-r");
    await typePasswords("mat-khau-moi", "mat-khau-cu");
    await screen.findByText(MESSAGES.passwordMismatch);
    const confirm = screen.getByLabelText(/^Nhập lại mật khẩu/);
    expect(confirm).toHaveAttribute("aria-invalid", "true");
    expect(confirm).toHaveAccessibleDescription(new RegExp(MESSAGES.passwordMismatch));
    expect(sent).toBe(false);
  });

  it("shows the API's password rule on the field", async () => {
    server.use(http.post(apiUrl("/auth/password-reset/confirm"), () => apiError(422, "validation_failed", { fields: { password: "too_long" } })));
    renderApp("/reset-password?token=tok-r");
    await typePasswords("mat-khau-moi", "mat-khau-moi");
    expect(await screen.findByText(MESSAGES.passwordTooLong)).toBeInTheDocument();
  });

  it("offers a fresh link when this one is used or expired", async () => {
    server.use(http.post(apiUrl("/auth/password-reset/confirm"), () => apiError(422, "token_invalid")));
    renderApp("/reset-password?token=old");
    await typePasswords("mat-khau-moi", "mat-khau-moi");
    expect(await screen.findByRole("alert")).toHaveTextContent(AUTH_MESSAGES.tokenInvalid);
    expect(screen.getByRole("link", { name: "Gửi liên kết mới" })).toHaveAttribute("href", "/forgot-password");
  });

  it("points a link without token to the forgot-password form", async () => {
    renderApp("/reset-password");
    expect(await screen.findByRole("link", { name: "Gửi liên kết mới" })).toHaveAttribute("href", "/forgot-password");
    expect(screen.queryByLabelText(/^Mật khẩu mới/)).not.toBeInTheDocument();
  });
});
