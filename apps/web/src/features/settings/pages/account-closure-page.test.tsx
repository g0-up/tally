import { screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../../../shared/ui/reauth-dialog";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testClosureRequest } from "../testing/fixtures";
import { CLOSURE_TOKEN_MISSING, CLOSURE_VERIFY_LABEL } from "./account-closure-page";

const PATH = "/settings/account-closure?token=tok-123";
const reauth = http.post(apiUrl(PATHS.reauth), () => HttpResponse.json({ reauthenticated_until: "2026-10-12T09:05:00Z" }));

async function confirmPassword(user: UserEvent) {
  await user.click(await screen.findByRole("button", { name: "Xác nhận mật khẩu" }));
  const dialog = await screen.findByRole("dialog", { name: REAUTH_TITLE });
  await user.type(within(dialog).getByLabelText("Mật khẩu"), "mat-khau");
  await user.click(within(dialog).getByRole("button", { name: REAUTH_SUBMIT }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: REAUTH_TITLE })).not.toBeInTheDocument());
}

describe("AccountClosurePage", () => {
  it("asks to reopen the e-mailed link when it carries no token", async () => {
    renderApp("/settings/account-closure");

    expect(await screen.findByText(CLOSURE_TOKEN_MISSING)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: CLOSURE_VERIFY_LABEL })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Về Tài khoản & bảo mật" })).toHaveAttribute("href", "/settings/account");
  });

  it("confirms the closure with the token after the password", async () => {
    const bodies: unknown[] = [];
    server.use(
      reauth,
      http.post(apiUrl("/account-closure:verify"), async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json(testClosureRequest({ status: "waiting", verified_at: "2026-10-12T03:00:00Z", waiting_until: "2026-11-11" }));
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await confirmPassword(user);
    await user.click(await screen.findByRole("button", { name: CLOSURE_VERIFY_LABEL }));

    expect(await screen.findByText("Đã xác nhận đóng tài khoản.")).toBeInTheDocument();
    expect(await screen.findByText(/Cửa hàng vẫn hoạt động đến hết ngày 11\/11\/2026/)).toBeInTheDocument();
    expect(bodies).toEqual([{ token: "tok-123" }]);
  });

  it("reports nothing when the owner closes the password prompt the API asked for", async () => {
    let asked = 0;
    server.use(
      reauth,
      http.post(apiUrl("/account-closure:verify"), () => {
        asked += 1;
        return apiError(403, "reauth_required");
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await confirmPassword(user);
    await user.click(await screen.findByRole("button", { name: CLOSURE_VERIFY_LABEL }));
    const prompt = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    await user.click(within(prompt).getByRole("button", { name: "Hủy" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: REAUTH_TITLE })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: CLOSURE_VERIFY_LABEL })).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(asked).toBe(1);
  });

  it.each([
    [422, "token_invalid", "Liên kết không hợp lệ hoặc đã hết hạn."],
    [409, "closure_state_invalid", "Yêu cầu đóng tài khoản đã chuyển sang bước khác. Tải lại để xem trạng thái mới."],
  ])("names a %i %s refusal on the page", async (status, code, message) => {
    server.use(
      reauth,
      http.post(apiUrl("/account-closure:verify"), () => apiError(status, code)),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await confirmPassword(user);
    await user.click(await screen.findByRole("button", { name: CLOSURE_VERIFY_LABEL }));

    await waitFor(() => expect(screen.getAllByText(message, { exact: false }).length).toBeGreaterThanOrEqual(2));
    expect(screen.getByRole("button", { name: CLOSURE_VERIFY_LABEL })).toBeEnabled();
    expect(screen.queryByText("Đã xác nhận đóng tài khoản.")).not.toBeInTheDocument();
  });
});
