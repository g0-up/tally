import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { AUTH_TEST, apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { SLUG_STATUS } from "../lib/slug";
import { AUTH_MESSAGES } from "../lib/auth-errors";
import { MESSAGES } from "../schemas";

const slugInput = () => screen.getByLabelText(/^Địa chỉ cửa hàng/);
const slugStatus = () => document.getElementById("slug-status");

async function openSignup() {
  renderApp("/signup", "root");
  await screen.findByRole("heading", { level: 1, name: "Đăng ký cửa hàng" });
  return userEvent.setup();
}

async function fillAll(user: ReturnType<typeof userEvent.setup>, storeName = "Cửa hàng Mới") {
  await user.type(screen.getByLabelText(/^Họ tên/), "Nguyễn Văn An");
  await user.type(screen.getByLabelText(/^Tên cửa hàng/), storeName);
  await user.type(screen.getByLabelText(/^Email/), "an@example.test");
  await user.type(screen.getByLabelText(/^Mật khẩu mới/), "mat-khau-moi");
  await user.type(screen.getByLabelText(/^Nhập lại mật khẩu/), "mat-khau-moi");
}

describe("SignupPage", () => {
  it("focuses the first field and shows the store host after the address", async () => {
    await openSignup();
    expect(screen.getByLabelText(/^Họ tên/)).toHaveFocus();
    expect(await screen.findByText(".localhost:5173")).toBeInTheDocument();
  });

  it("derives the address from the store name until it is edited", async () => {
    const user = await openSignup();
    await user.type(screen.getByLabelText(/^Tên cửa hàng/), "Đại lý Hòa Phát");
    expect(slugInput()).toHaveValue("dai-ly-hoa-phat");
    expect(await screen.findByText(SLUG_STATUS.available)).toBeInTheDocument();
    await user.clear(slugInput());
    await user.type(slugInput(), "HP-Shop");
    expect(slugInput()).toHaveValue("hp-shop");
    await user.type(screen.getByLabelText(/^Tên cửa hàng/), " 2");
    expect(slugInput()).toHaveValue("hp-shop");
  });

  it("reports a taken address and applies the suggestion", async () => {
    const user = await openSignup();
    await user.type(slugInput(), AUTH_TEST.takenSlug);
    await waitFor(() => expect(slugStatus()).toHaveTextContent(MESSAGES.slugTaken));
    expect(slugInput()).toHaveAttribute("aria-invalid", "true");
    await user.click(screen.getByRole("button", { name: `Dùng “${AUTH_TEST.takenSuggestion}”` }));
    expect(slugInput()).toHaveValue(AUTH_TEST.takenSuggestion);
    expect(await screen.findByText(SLUG_STATUS.available)).toBeInTheDocument();
  });

  it("reports a reserved address", async () => {
    const user = await openSignup();
    await user.type(slugInput(), AUTH_TEST.reservedSlug);
    await waitFor(() => expect(slugStatus()).toHaveTextContent(MESSAGES.slugReserved));
  });

  it("explains a badly shaped address without asking the server", async () => {
    let asked = false;
    server.use(
      http.get(apiUrl("/signup/slug-check"), () => {
        asked = true;
        return HttpResponse.json({ available: true });
      }),
    );
    const user = await openSignup();
    await user.type(slugInput(), "ab--cd");
    expect(slugStatus()).toHaveTextContent(MESSAGES.slugInvalid);
    expect(asked).toBe(false);
  });

  it("creates the store and asks the owner to check the mail", async () => {
    let body: unknown;
    server.use(
      http.post(apiUrl("/auth/signup"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ store_url: AUTH_TEST.storeUrl }, { status: 202 });
      }),
    );
    const user = await openSignup();
    await fillAll(user);
    await user.click(screen.getByRole("button", { name: "Tạo cửa hàng" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Kiểm tra email của bạn" })).toHaveFocus();
    expect(screen.getByText(AUTH_TEST.storeUrl)).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("an@example.test");
    expect(body).toEqual({ full_name: "Nguyễn Văn An", store_name: "Cửa hàng Mới", slug: "cua-hang-moi", email: "an@example.test", password: "mat-khau-moi" });
  });

  it("puts a server-side address refusal on the address field", async () => {
    server.use(http.post(apiUrl("/auth/signup"), () => apiError(422, "slug_taken", { fields: { slug: "taken" } })));
    const user = await openSignup();
    await fillAll(user);
    await user.click(screen.getByRole("button", { name: "Tạo cửa hàng" }));
    expect(await screen.findByText(AUTH_MESSAGES.checkFields)).toHaveAttribute("role", "alert");
    await waitFor(() => expect(slugInput()).toHaveFocus());
    expect(screen.getAllByText(MESSAGES.slugTaken).length).toBeGreaterThan(0);
  });

  it("is not served on a store host", async () => {
    renderApp("/signup");
    expect(await screen.findByRole("heading", { level: 1, name: "Không tìm thấy trang" })).toBeInTheDocument();
  });
});
