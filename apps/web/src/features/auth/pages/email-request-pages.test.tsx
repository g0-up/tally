import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { apiUrl, rateLimitedError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { MESSAGES } from "../schemas";

describe("ForgotPasswordPage", () => {
  it("ends on the same neutral confirmation whatever the e-mail", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/password-reset"), async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 202 });
      }),
    );
    renderApp("/forgot-password");
    const user = userEvent.setup();
    const email = await screen.findByLabelText(/^Email/);
    expect(email).toHaveFocus();
    await user.type(email, "chu@example.test");
    await user.click(screen.getByRole("button", { name: "Gửi liên kết đặt lại" }));
    const sent = await screen.findByRole("status");
    expect(sent).toHaveTextContent("Nếu chu@example.test có tài khoản ở cửa hàng này");
    // Focus lands on the confirmation so a screen reader reads it once the form is gone.
    expect(sent.parentElement).toHaveFocus();
    expect(body).toEqual({ email: "chu@example.test" });
  });

  it("validates the e-mail first", async () => {
    renderApp("/forgot-password");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/^Email/), "chu");
    await user.click(screen.getByRole("button", { name: "Gửi liên kết đặt lại" }));
    expect(await screen.findByText(MESSAGES.emailInvalid)).toBeInTheDocument();
  });

  it("states the wait when mails are throttled", async () => {
    server.use(http.post(apiUrl("/auth/password-reset"), () => rateLimitedError("rate_limited", 45)));
    renderApp("/forgot-password");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/^Email/), "chu@example.test");
    await user.click(screen.getByRole("button", { name: "Gửi liên kết đặt lại" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Bạn đã thử quá nhiều lần. Thử lại sau 45 giây.");
  });
});

describe("FindStorePage", () => {
  it("mails the store addresses and confirms neutrally", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/store-lookup"), async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 202 });
      }),
    );
    renderApp("/find-store", "root");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/^Email/), "chu@example.test");
    await user.click(screen.getByRole("button", { name: "Gửi địa chỉ cửa hàng" }));
    await waitFor(() => expect(body).toEqual({ email: "chu@example.test" }));
    expect(await screen.findByRole("status")).toHaveTextContent("thuộc một cửa hàng trên hệ thống");
  });

  it("is not served on a store host", async () => {
    renderApp("/find-store");
    expect(await screen.findByRole("heading", { level: 1, name: "Không tìm thấy trang" })).toBeInTheDocument();
  });
});
