import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { AUTH_MESSAGES } from "../lib/auth-errors";
import { NOTICES } from "../lib/auth-state";
import { MESSAGES } from "../schemas";

describe("VerifyPage", () => {
  it("waits for the button before using the link", async () => {
    const bodies: unknown[] = [];
    server.use(
      http.post(apiUrl("/auth/verify"), async ({ request }) => {
        bodies.push(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { router } = renderApp("/verify?token=tok-1");
    const button = await screen.findByRole("button", { name: "Xác minh email" });
    expect(bodies).toEqual([]);
    await userEvent.setup().click(button);
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(bodies).toEqual([{ token: "tok-1" }]);
    expect(await screen.findByRole("status")).toHaveTextContent(NOTICES.verified);
  });

  it("asks for a first password when the account has none", async () => {
    const bodies: unknown[] = [];
    server.use(
      http.post(apiUrl("/auth/verify"), async ({ request }) => {
        const body = (await request.json()) as { password?: string };
        bodies.push(body);
        if (!body.password) return apiError(422, "validation_failed", { fields: { password: "required" } });
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { router } = renderApp("/verify?token=tok-2");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Xác minh email" }));
    const password = await screen.findByLabelText(/^Mật khẩu mới/);
    expect(password).toHaveFocus();
    await user.type(password, "mat-khau-moi");
    await user.type(screen.getByLabelText(/^Nhập lại mật khẩu/), "mat-khau-khac");
    await user.click(screen.getByRole("button", { name: "Đặt mật khẩu và xác minh" }));
    expect(await screen.findByText(MESSAGES.passwordMismatch)).toBeInTheDocument();
    await user.clear(screen.getByLabelText(/^Nhập lại mật khẩu/));
    await user.type(screen.getByLabelText(/^Nhập lại mật khẩu/), "mat-khau-moi");
    await user.click(screen.getByRole("button", { name: "Đặt mật khẩu và xác minh" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(bodies).toEqual([{ token: "tok-2" }, { token: "tok-2", password: "mat-khau-moi" }]);
  });

  it("explains a used or expired link", async () => {
    server.use(http.post(apiUrl("/auth/verify"), () => apiError(422, "token_invalid")));
    renderApp("/verify?token=old");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Xác minh email" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(AUTH_MESSAGES.tokenInvalid);
    expect(screen.queryByRole("button", { name: "Xác minh email" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Quên mật khẩu/ })).toHaveAttribute("href", "/forgot-password");
  });

  it("explains a link without its token", async () => {
    renderApp("/verify");
    expect(await screen.findByRole("alert")).toHaveTextContent("Liên kết xác minh không đầy đủ");
  });
});
