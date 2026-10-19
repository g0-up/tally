import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { AUTH_TEST, apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { AUTH_MESSAGES } from "../lib/auth-errors";
import { MESSAGES } from "../schemas";

/** Opens /mfa the way the password step does: with the challenge in the history state. */
async function openMfa(next: string | null = "/sales/invoices") {
  const rendered = renderApp("/login");
  await screen.findByRole("heading", { level: 1, name: "Đăng nhập" });
  await act(() => rendered.router.navigate("/mfa", { state: { challenge: AUTH_TEST.challenge, next } }));
  await screen.findByRole("heading", { level: 1, name: "Xác thực hai lớp" });
  return rendered;
}

describe("MfaPage", () => {
  it("sends a visitor without a challenge back to /login", async () => {
    const { router } = renderApp("/mfa");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("signs in with the 6-digit code and continues to next", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/mfa/totp/verify"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ status: "signed_in", csrf_token: AUTH_TEST.signedInCsrf });
      }),
    );
    const { router } = await openMfa();
    const user = userEvent.setup();
    const code = screen.getByLabelText(/^Mã xác thực/);
    expect(code).toHaveFocus();
    await user.type(code, "123 456");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/sales/invoices"));
    expect(body).toEqual({ challenge: AUTH_TEST.challenge, code: "123456" });
  });

  it("asks again after a wrong code", async () => {
    server.use(http.post(apiUrl("/auth/mfa/totp/verify"), () => apiError(401, "invalid_code")));
    await openMfa();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/^Mã xác thực/), "000000");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    await screen.findByText(AUTH_MESSAGES.invalidCode);
    const field = screen.getByLabelText(/^Mã xác thực/);
    expect(field).toHaveAttribute("aria-invalid", "true");
    expect(field).toHaveAccessibleDescription(new RegExp(AUTH_MESSAGES.invalidCode));
    expect(field).toHaveValue("");
    expect(field).toHaveFocus();
  });

  it("rejects a code that is not 6 digits without sending it", async () => {
    await openMfa();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/^Mã xác thực/), "12ab");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    expect(await screen.findByText(MESSAGES.codeInvalidFormat)).toBeInTheDocument();
  });

  it("returns to /login with a notice when the challenge expired", async () => {
    server.use(http.post(apiUrl("/auth/mfa/totp/verify"), () => apiError(401, "challenge_invalid")));
    const { router } = await openMfa("/sales/invoices");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/^Mã xác thực/), "123456");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toBe(`?next=${encodeURIComponent("/sales/invoices")}`);
    expect(await screen.findByRole("status")).toHaveTextContent(AUTH_MESSAGES.challengeExpired);
  });

  it("accepts a recovery code instead", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/mfa/totp/verify"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ status: "signed_in" });
      }),
    );
    await openMfa(null);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Dùng mã khôi phục" }));
    await user.type(screen.getByLabelText(/^Mã khôi phục/), " abcd-efgh ");
    await user.click(screen.getByRole("button", { name: "Xác thực" }));
    await waitFor(() => expect(body).toEqual({ challenge: AUTH_TEST.challenge, recovery_code: "abcd-efgh" }));
  });
});
