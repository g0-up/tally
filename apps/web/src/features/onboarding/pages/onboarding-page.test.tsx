import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { HOME_PATH } from "../../../app/menu";
import { PATHS } from "../../../shared/api/endpoints";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../../../shared/ui/reauth-dialog";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { CUTOFF, TEST_PROFILE, installOnboardingApi, type StepStatuses } from "../test-fixtures";

const ALL_BEFORE_CONFIRM: StepStatuses = {
  store_profile: "completed",
  catalog_setup: "skipped",
  opening_stock: "completed",
  opening_debt: "completed",
  opening_cash: "completed",
  first_sale: "skipped",
};

const region = (name: string) => screen.findByRole("region", { name });
const checklistRow = (name: RegExp) => screen.findByRole("button", { name });

describe("OnboardingPage", () => {
  it("lists the seven steps with their status and whether they are required, and opens the first pending one", async () => {
    installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "skipped" } });
    renderApp("/onboarding");

    expect(await screen.findByRole("heading", { name: "Thiết lập cửa hàng" })).toBeInTheDocument();
    expect(await checklistRow(/^Đã xong\s*Thông tin cửa hàng\s*Bắt buộc trước khi dùng cửa hàng/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Bỏ qua\s*Danh mục hàng hóa\s*Có thể bỏ qua/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Chưa làm\s*Tồn kho đầu kỳ\s*Bắt buộc/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Chưa làm\s*Bán đơn đầu tiên\s*Có thể bỏ qua/ })).toBeInTheDocument();
    expect(screen.getByText("Còn 5 bước")).toBeInTheDocument();

    const stock = await region("Tồn kho đầu kỳ");
    expect(within(stock).getByText("Tồn kho cuối ngày 30/09/2026")).toBeInTheDocument();
    expect(within(stock).getByRole("button", { name: "Đánh dấu xong" })).toBeInTheDocument();
    // A required step has no skip.
    expect(within(stock).queryByRole("button", { name: "Bỏ qua" })).not.toBeInTheDocument();
  });

  it("marks a step done and skips an optional one through the step commands", async () => {
    const api = installOnboardingApi({ steps: { store_profile: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const catalog = await region("Danh mục hàng hóa");
    expect(within(catalog).getByRole("link", { name: "Mở danh mục hàng hóa" })).toHaveAttribute("href", "/catalog/products");
    await user.click(within(catalog).getByRole("button", { name: "Đánh dấu xong" }));
    expect(await screen.findByText("Đã xong bước “Danh mục hàng hóa”.")).toBeInTheDocument();
    await waitFor(() => expect(within(catalog).queryByRole("button", { name: "Đánh dấu xong" })).not.toBeInTheDocument());
    expect(within(catalog).getByText("Đã xong")).toBeInTheDocument();

    await user.click(await checklistRow(/Bán đơn đầu tiên/));
    const sale = await region("Bán đơn đầu tiên");
    expect(within(sale).getByRole("link", { name: "Mở POS bán hàng" })).toHaveAttribute("href", "/sales/pos");
    // The first sale completes itself with the first invoice: no manual done.
    expect(within(sale).queryByRole("button", { name: "Đánh dấu xong" })).not.toBeInTheDocument();
    await user.click(within(sale).getByRole("button", { name: "Bỏ qua" }));
    expect(await screen.findByText("Đã bỏ qua bước “Bán đơn đầu tiên”.")).toBeInTheDocument();

    expect(api.calls.map((c) => [c.method, c.path])).toEqual([
      ["POST", "/onboarding/steps/catalog_setup:complete"],
      ["POST", "/onboarding/steps/first_sale:skip"],
    ]);
  });

  it("shows the API's refusal to skip a step", async () => {
    installOnboardingApi({ steps: { store_profile: "completed" } });
    server.use(http.post(apiUrl("/onboarding/steps/*"), () => apiError(409, "step_not_skippable")));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const catalog = await region("Danh mục hàng hóa");
    await user.click(within(catalog).getByRole("button", { name: "Bỏ qua" }));
    expect(await screen.findByText("Bước này bắt buộc, không bỏ qua được.")).toBeInTheDocument();
    // Nothing is posted by a step command, so the refusal names the step, not the books.
    expect(screen.getByText("Chưa bỏ qua bước “Danh mục hàng hóa”.")).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(within(catalog).getByText("Chưa làm")).toBeInTheDocument();
  });

  it("says the profile was saved but the step not marked done when only the completion is refused", async () => {
    installOnboardingApi();
    server.use(http.post(apiUrl("/onboarding/steps/store_profile:complete"), () => apiError(409, "step_not_ready")));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const profile = await region("Thông tin cửa hàng");
    await user.click(await within(profile).findByRole("button", { name: "Lưu và đánh dấu xong" }));

    expect(await screen.findByText("Đã lưu thông tin cửa hàng.")).toBeInTheDocument();
    expect(await screen.findByText("Chưa đánh dấu xong bước “Thông tin cửa hàng”.")).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
  });

  it("saves the store profile under If-Match, then completes the step", async () => {
    const api = installOnboardingApi({ profile: { ...TEST_PROFILE, opening_cutoff_date: null, tax_code: null } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const profile = await region("Thông tin cửa hàng");
    const taxCode = await within(profile).findByRole("textbox", { name: /^Mã số thuế/ });
    await user.type(taxCode, "12345");
    await user.click(within(profile).getByRole("button", { name: "Lưu và đánh dấu xong" }));
    expect(await within(profile).findByText("Mã số thuế gồm 10 chữ số, hoặc 10 chữ số kèm -3 chữ số chi nhánh.")).toBeInTheDocument();
    expect(within(profile).getByText("Chọn ngày chốt số dư đầu kỳ.")).toBeInTheDocument();
    expect(api.calls).toEqual([]);

    await user.clear(taxCode);
    await user.type(taxCode, "0101234567-001");
    await user.type(within(profile).getByRole("textbox", { name: "Ngày chốt số dư đầu kỳ" }), "30/09/2026");
    await user.click(within(profile).getByRole("button", { name: "Lưu và đánh dấu xong" }));

    expect(await screen.findByText("Đã lưu thông tin cửa hàng.")).toBeInTheDocument();
    await waitFor(() => expect(api.calls).toHaveLength(2));
    expect(api.calls[0]).toMatchObject({
      method: "PATCH",
      path: "/tenant",
      ifMatch: '"3"',
      body: {
        legal_name: "Công ty TNHH Bánh kẹo Hòa Phát",
        display_name: TEST_PROFILE.display_name,
        tax_code: "0101234567-001",
        address: "12 Hàng Đường, Hà Nội",
        phone: "0901234567",
        opening_cutoff_date: CUTOFF,
      },
    });
    expect(api.calls[1]).toMatchObject({ method: "POST", path: "/onboarding/steps/store_profile:complete" });
    expect(await within(profile).findByRole("button", { name: "Lưu thay đổi" })).toBeInTheDocument();
  });

  it("asks for the cutoff before the opening balances", async () => {
    installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "completed" }, profile: { ...TEST_PROFILE, opening_cutoff_date: null }, totals: null });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    expect(await within(stock).findByText(/Chọn ngày chốt số dư đầu kỳ trong Thông tin cửa hàng trước\./)).toBeInTheDocument();
    expect(within(stock).queryByRole("button", { name: "Ghi tồn kho đầu kỳ" })).not.toBeInTheDocument();
    await user.click(within(stock).getByRole("button", { name: "Mở Thông tin cửa hàng" }));
    expect(await region("Thông tin cửa hàng")).toBeInTheDocument();
  });
});

describe("Opening balances confirmation", () => {
  it("shows the opening totals exactly as the API sends them", async () => {
    installOnboardingApi({ steps: ALL_BEFORE_CONFIRM });
    renderApp("/onboarding");

    const confirm = await region("Xác nhận số dư đầu kỳ");
    const value = (label: string) => within(confirm).getByText(label).nextElementSibling?.textContent?.replace(/\s/g, " ");
    expect(value("Ngày chốt")).toBe("30/09/2026");
    expect(value("Giá trị tồn kho")).toBe("1.340.000 ₫");
    expect(value("Khách hàng nợ (phải thu)")).toBe("2.500.000 ₫");
    expect(value("Nợ nhà cung cấp (phải trả)")).toBe("750.000 ₫");
    expect(value("Khách ứng trước")).toBe("0 ₫");
    expect(value("Ứng trước nhà cung cấp")).toBe("120.000 ₫");
    expect(value("Tiền mặt tại cửa hàng")).toBe("5.000.000 ₫");
    expect(value("Tài khoản ngân hàng")).toBe("12.345.678 ₫");
    expect(value("Tổng tiền các quỹ")).toBe("17.345.678 ₫");

    const byColor = within(confirm).getByRole("table", { name: "Tồn kho đầu kỳ theo màu" });
    expect(within(byColor).getByRole("row", { name: /^Đỏ 24 1\.250\.000\s₫$/ })).toBeInTheDocument();
    expect(within(byColor).getByRole("row", { name: /^Chưa gán màu 10 90\.000\s₫$/ })).toBeInTheDocument();
  });

  it("confirms after the password check, then opens the dashboard", async () => {
    const api = installOnboardingApi({ steps: ALL_BEFORE_CONFIRM });
    let confirms = 0;
    const reauthBodies: unknown[] = [];
    server.use(
      http.post(apiUrl("/onboarding/steps/*"), () => {
        confirms++;
        // Falls through to the recorded step command after the password check.
        return confirms === 1 ? apiError(403, "reauth_required") : undefined;
      }),
      http.post(apiUrl(PATHS.reauth), async ({ request }) => {
        reauthBodies.push(await request.json());
        return HttpResponse.json({ reauthenticated_until: "2026-10-18T09:05:00Z" });
      }),
    );
    const user = userEvent.setup();
    const { router } = renderApp("/onboarding");

    const confirm = await region("Xác nhận số dư đầu kỳ");
    const button = within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" });
    expect(button).toBeDisabled();
    await user.click(within(confirm).getByRole("checkbox", { name: "Tôi đã kiểm tra các số dư trên và đồng ý khóa sổ đến hết ngày chốt." }));
    await user.click(button);

    const reauth = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    await user.type(within(reauth).getByLabelText("Mật khẩu"), "mat-khau");
    await user.click(within(reauth).getByRole("button", { name: REAUTH_SUBMIT }));

    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));
    expect(await screen.findByText("Đã xác nhận số dư đầu kỳ. Sổ sách đến hết ngày 30/09/2026 đã khóa.")).toBeInTheDocument();
    expect(reauthBodies).toEqual([{ password: "mat-khau" }]);
    expect(confirms).toBe(2);
    expect(api.to("/onboarding/steps/confirm_opening_balances:complete")).toHaveLength(1);
  });

  it("names the steps the API still sees pending", async () => {
    installOnboardingApi({ steps: ALL_BEFORE_CONFIRM });
    server.use(http.post(apiUrl("/onboarding/steps/*"), () => apiError(409, "onboarding_steps_pending", { fields: { opening_cash: "pending" } })));
    const user = userEvent.setup();
    const { router } = renderApp("/onboarding");

    const confirm = await region("Xác nhận số dư đầu kỳ");
    await user.click(within(confirm).getByRole("checkbox"));
    await user.click(within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" }));

    expect(await screen.findByText("Còn bước bắt buộc chưa xong. Hoàn tất các bước trước rồi xác nhận số dư.")).toBeInTheDocument();
    expect(await within(confirm).findByText(/Còn bước chưa xong:/)).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Tiền đầu kỳ" }));
    expect(await region("Tiền đầu kỳ")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/onboarding");
  });

  it.each([
    ["opening_cutoff_required", "Chọn ngày chốt số dư đầu kỳ trong Thông tin cửa hàng trước."],
    ["opening_cutoff_in_future", "Ngày chốt số dư đầu kỳ không được sau hôm nay."],
    ["confirmation_required", "Cần xác nhận số dư đầu kỳ."],
  ])("shows %s in Vietnamese", async (code, message) => {
    installOnboardingApi({ steps: ALL_BEFORE_CONFIRM });
    server.use(http.post(apiUrl("/onboarding/steps/*"), () => apiError(409, code)));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const confirm = await region("Xác nhận số dư đầu kỳ");
    await user.click(within(confirm).getByRole("checkbox"));
    await user.click(within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
  });

  it("offers the store once every required step is done", async () => {
    installOnboardingApi({ steps: { ...ALL_BEFORE_CONFIRM, confirm_opening_balances: "completed" } });
    const user = userEvent.setup();
    const { router } = renderApp("/onboarding");

    expect(await screen.findByText(/Các bước bắt buộc đã xong\./)).toBeInTheDocument();
    // Announced when the last required step is done.
    expect(screen.getByText(/Các bước bắt buộc đã xong\./).closest('[role="status"]')).not.toBeNull();
    await user.click(await checklistRow(/Xác nhận số dư đầu kỳ/));
    const confirm = await region("Xác nhận số dư đầu kỳ");
    expect(within(confirm).getByText("Sổ sách đã khóa đến hết ngày 30/09/2026")).toBeInTheDocument();
    expect(within(confirm).queryByRole("button", { name: "Xác nhận số dư đầu kỳ" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Vào cửa hàng" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));
  });
});
