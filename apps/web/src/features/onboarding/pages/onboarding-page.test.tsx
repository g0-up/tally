import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { HOME_PATH } from "../../../app/menu";
import { PATHS } from "../../../shared/api/endpoints";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { COLOR_DO, CUTOFF, TEST_PROFILE, installOnboardingApi, testFund, testStockLine, type StepStatuses } from "../test-fixtures";

const ALL_BEFORE_CONFIRM: StepStatuses = {
  store_profile: "completed",
  catalog_setup: "skipped",
  opening_stock: "completed",
  opening_debt: "completed",
  opening_cash: "completed",
};

const region = (name: string) => screen.findByRole("region", { name });
const rail = () => screen.findByRole("navigation", { name: "Các bước thiết lập" });
/** A toast, whose message may hold bold parts. */
const toastText = (text: string) => screen.findByText((_, el) => el?.classList.contains("toast-msg") === true && el.textContent === text);

describe("OnboardingPage", () => {
  it("lists the seven steps in their groups with their status, and opens the first pending one", async () => {
    installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "skipped" } });
    renderApp("/onboarding");

    expect(await screen.findByRole("heading", { name: "Thiết lập cửa hàng" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("progressbar", { name: "Bước bắt buộc" })).toHaveAttribute("aria-valuetext", "1 trên 5 bước bắt buộc đã xong"));

    const nav = await rail();
    expect(within(nav).getByRole("list", { name: "Cửa hàng" })).toBeInTheDocument();
    expect(within(nav).getByRole("list", { name: /^Số dư đầu kỳ\s*Tính đến hết ngày 30\/09\/2026$/ })).toBeInTheDocument();
    expect(within(nav).getByRole("list", { name: /^Chạy thử\s*Sau khi khóa số dư$/ })).toBeInTheDocument();
    const names = within(nav)
      .getAllByRole("listitem")
      .map((li) => within(li).getByRole("button"))
      .map((b) => b.textContent?.replace(/^\d/, ""));
    expect(names).toEqual([
      "Bước 1: Thông tin cửa hàngĐã xong",
      "Bước 2: Danh mục hàng hóaĐã bỏ qua",
      "Bước 3: Tồn kho đầu kỳBắt buộc",
      "Bước 4: Công nợ đầu kỳBắt buộc",
      "Bước 5: Tiền đầu kỳBắt buộc",
      "Bước 6: Xác nhận và khóa số dưBắt buộc",
      "Bước 7: Bán đơn đầu tiênMở sau khi xác nhận số dư",
    ]);
    expect(within(nav).getByRole("button", { name: /^Bước \d+:\s*Tồn kho đầu kỳ/ })).toHaveAttribute("aria-current", "step");

    const stock = await region("Tồn kho đầu kỳ");
    expect(within(stock).getByText("Bước 3/7 · Số dư đầu kỳ")).toBeInTheDocument();
    expect(within(stock).getByText("Bắt buộc")).toBeInTheDocument();
    expect(within(stock).getByText("Chưa làm")).toBeInTheDocument();
    // A required step has no skip.
    expect(within(stock).queryByRole("button", { name: "Bỏ qua bước này" })).not.toBeInTheDocument();
  });

  it("moves between steps from the rail and the step navigation", async () => {
    installOnboardingApi({ steps: { store_profile: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    await region("Danh mục hàng hóa");
    await user.click(screen.getByRole("button", { name: "Bước tiếp →" }));
    expect(await region("Tồn kho đầu kỳ")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Tồn kho đầu kỳ" })).toHaveFocus();
    await user.click(screen.getByRole("button", { name: "← Bước trước" }));
    expect(await region("Danh mục hàng hóa")).toBeInTheDocument();
    await user.click(within(await rail()).getByRole("button", { name: /^Bước \d+:\s*Bán đơn đầu tiên/ }));
    const sale = await region("Bán đơn đầu tiên");
    expect(within(sale).getByText("Mở sau khi xác nhận số dư")).toBeInTheDocument();
    expect(within(sale).queryByRole("button", { name: "Bước tiếp →" })).not.toBeInTheDocument();
  });

  it("skips an optional step through the step command and moves on", async () => {
    const api = installOnboardingApi({ steps: { store_profile: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const catalog = await region("Danh mục hàng hóa");
    expect(within(catalog).getByText("Tùy chọn")).toBeInTheDocument();
    await user.click(within(catalog).getByRole("button", { name: "Bỏ qua bước này" }));
    expect(await toastText("Đã bỏ qua Danh mục hàng hóa. Có thể làm lại bất cứ lúc nào.")).toBeInTheDocument();
    expect(await region("Tồn kho đầu kỳ")).toBeInTheDocument();
    expect(within(await rail()).getByRole("button", { name: /^Bước \d+:\s*Danh mục hàng hóa/ })).toHaveTextContent("Đã bỏ qua");
    expect(api.calls.map((c) => [c.method, c.path])).toEqual([["POST", "/onboarding/steps/catalog_setup:skip"]]);
  });

  it("completes the catalog step from its product list", async () => {
    const api = installOnboardingApi({ steps: { store_profile: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const catalog = await region("Danh mục hàng hóa");
    const table = await within(catalog).findByRole("table", { name: "Hàng hóa" });
    expect(within(table).getByText("SP001")).toBeInTheDocument();
    expect(within(catalog).getByRole("link", { name: "Mở danh mục đầy đủ" })).toHaveAttribute("href", "/catalog/products");
    await user.click(within(catalog).getByRole("button", { name: "Xong, sang tồn kho" }));
    expect(await toastText("Đã xong Danh mục hàng hóa.")).toBeInTheDocument();
    expect(await region("Tồn kho đầu kỳ")).toBeInTheDocument();
    expect(api.to("/onboarding/steps/catalog_setup:complete")).toHaveLength(1);
  });

  it("shows the API's refusal to skip a step", async () => {
    installOnboardingApi({ steps: { store_profile: "completed" } });
    server.use(http.post(apiUrl("/onboarding/steps/*"), () => apiError(409, "step_not_skippable")));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const catalog = await region("Danh mục hàng hóa");
    await user.click(within(catalog).getByRole("button", { name: "Bỏ qua bước này" }));
    expect(await screen.findByText("Bước này bắt buộc, không bỏ qua được.")).toBeInTheDocument();
    // Nothing is posted by a step command, so the refusal names the step, not the books.
    expect(screen.getByText("Chưa bỏ qua bước “Danh mục hàng hóa”.")).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(within(catalog).getByText("Chưa làm")).toBeInTheDocument();
  });

  it("saves the store profile under If-Match, then completes the step and opens the catalog", async () => {
    const api = installOnboardingApi({ profile: { ...TEST_PROFILE, opening_cutoff_date: null, tax_code: null } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const profile = await region("Thông tin cửa hàng");
    const taxCode = await within(profile).findByRole("textbox", { name: "Mã số thuế" });
    await user.type(taxCode, "12345");
    await user.click(within(profile).getByRole("button", { name: "Lưu và tiếp tục" }));
    expect(taxCode).toHaveAccessibleDescription(/Mã số thuế gồm 10 số, hoặc 10 số-3 số \(ví dụ 0312345678-001\)\./);
    const cutoff = within(profile).getByRole("textbox", { name: /^Ngày chốt/ });
    expect(cutoff).toHaveAccessibleDescription(/Chọn ngày chốt số dư đầu kỳ\./);
    await waitFor(() => expect(taxCode).toHaveFocus());
    expect(await toastText("Chưa lưu. Còn 2 chỗ cần sửa, đã đưa con trỏ tới chỗ đầu tiên.")).toBeInTheDocument();
    expect(api.calls).toEqual([]);

    await user.clear(taxCode);
    await user.type(taxCode, "0101234567-001");
    await user.type(cutoff, "30/09/2026");
    await user.click(within(profile).getByRole("button", { name: "Lưu và tiếp tục" }));

    expect(await toastText("Đã lưu thông tin cửa hàng. Số dư đầu kỳ tính đến hết ngày 30/09/2026.")).toBeInTheDocument();
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
    expect(await region("Danh mục hàng hóa")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "← Bước trước" }));
    expect(await within(await region("Thông tin cửa hàng")).findByRole("button", { name: "Lưu thay đổi" })).toBeInTheDocument();
  });

  it("says the profile was saved but the step not marked done when only the completion is refused", async () => {
    installOnboardingApi();
    server.use(http.post(apiUrl("/onboarding/steps/store_profile:complete"), () => apiError(409, "step_not_ready")));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const profile = await region("Thông tin cửa hàng");
    await user.click(await within(profile).findByRole("button", { name: "Lưu và tiếp tục" }));

    expect(await toastText("Đã lưu thông tin cửa hàng. Số dư đầu kỳ tính đến hết ngày 30/09/2026.")).toBeInTheDocument();
    expect(await screen.findByText("Chưa đánh dấu xong bước “Thông tin cửa hàng”.")).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
  });

  it("asks for the cutoff before the opening balances", async () => {
    installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "completed" }, profile: { ...TEST_PROFILE, opening_cutoff_date: null }, totals: null });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    expect(within(stock).getByText("Cần ngày chốt ở bước 1")).toBeInTheDocument();
    expect(await within(stock).findByText("Cần ngày chốt số dư trước.")).toBeInTheDocument();
    expect(within(stock).queryByRole("button", { name: "Ghi tồn kho đầu kỳ" })).not.toBeInTheDocument();
    await user.click(within(stock).getByRole("button", { name: "Đến Thông tin cửa hàng" }));
    expect(await region("Thông tin cửa hàng")).toBeInTheDocument();
  });

  it("closes the step list on Escape and gives the focus back to its toggle", async () => {
    installOnboardingApi({ steps: { store_profile: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const toggle = within(await rail()).getByRole("button", { expanded: false });
    expect(toggle).toHaveTextContent("Bước 2/7 · Danh mục hàng hóa");
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    await user.keyboard("{Escape}");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveFocus();
  });
});

describe("Opening balances confirmation", () => {
  it("shows the opening totals as the API sends them, and the opening stock by color", async () => {
    installOnboardingApi({
      steps: ALL_BEFORE_CONFIRM,
      funds: [testFund({ opening_balance: "5000000", opening_date: CUTOFF }), testFund({ id: "fund-bank", kind: "bank", name: "Tài khoản ngân hàng", version: 2 })],
      stockDocs: [
        { id: "adj-1", lines: [testStockLine({ color_id: COLOR_DO }), testStockLine({ id: "adj-line-2", qty: "14", color_id: COLOR_DO })] },
        { id: "adj-2", lines: [testStockLine({ id: "adj-line-3", unit_cost: "9000" })] },
        // The reversal of a document posted by mistake (whose original is no longer posted) adds no stock.
        { id: "adj-4", reversal_of: "adj-3", lines: [testStockLine({ id: "adj-line-4", qty: "5" })] },
      ],
    });
    renderApp("/onboarding");

    const confirm = await region("Xác nhận và khóa số dư");
    const value = (label: string) => within(confirm).getByText(label, { selector: "b" }).nextElementSibling?.textContent?.replace(/\s/g, " ");
    expect(value("Ngày chốt")).toBe("30/09/2026");
    expect(value("Giá trị tồn kho")).toBe("1.340.000 ₫");
    expect(value("Khách nợ (phải thu)")).toBe("2.500.000 ₫");
    expect(value("Nợ nhà cung cấp (phải trả)")).toBe("750.000 ₫");
    expect(value("Tiền mặt tại cửa hàng")).toBe("5.000.000 ₫");
    // A fund the owner has not saved yet says so instead of a balance.
    await waitFor(() => expect(value("Tài khoản ngân hàng")).toBe("Chưa lưu"));
    expect(value("Tổng tiền các quỹ")).toBe("17.345.678 ₫");
    // Advances are not opening balances on this screen.
    expect(within(confirm).queryByText("Khách ứng trước")).not.toBeInTheDocument();
    expect(within(confirm).queryByText("Ứng trước nhà cung cấp")).not.toBeInTheDocument();

    const byColor = await within(confirm).findByRole("table", { name: "Tồn kho theo màu" });
    expect(await within(byColor).findByRole("row", { name: /^Bánh quy bơ Đỏ 24 1\.080\.000\s₫$/ })).toBeInTheDocument();
    expect(within(byColor).getByRole("row", { name: /^Bánh quy bơ — 10 90\.000\s₫$/ })).toBeInTheDocument();
  });

  it("confirms after the password check, then opens the first sale with the store ready", async () => {
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

    const confirm = await region("Xác nhận và khóa số dư");
    const button = within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" });
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleDescription("Đánh dấu ô đồng ý để xác nhận.");
    await user.click(within(confirm).getByRole("checkbox", { name: "Tôi đã kiểm tra các số dư trên và đồng ý khóa sổ đến hết ngày chốt." }));
    expect(button).toBeEnabled();
    await user.click(button);

    const reauth = await screen.findByRole("dialog", { name: "Nhập lại mật khẩu để khóa sổ" });
    expect(within(reauth).getByText("Xác nhận số dư đầu kỳ đến hết ngày 30/09/2026. Không hoàn tác được.")).toBeInTheDocument();
    await user.type(within(reauth).getByLabelText("Mật khẩu"), "mat-khau");
    await user.click(within(reauth).getByRole("button", { name: "Khóa sổ và xác nhận" }));

    expect(await toastText("Đã khóa số dư đầu kỳ. POS đã mở.")).toBeInTheDocument();
    const sale = await region("Bán đơn đầu tiên");
    expect(await screen.findByRole("heading", { name: "Cửa hàng đã sẵn sàng bán" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/onboarding");
    expect(reauthBodies).toEqual([{ password: "mat-khau" }]);
    expect(confirms).toBe(2);
    expect(api.to("/onboarding/steps/confirm_opening_balances:complete")).toHaveLength(1);

    await user.click(within(sale).getByRole("button", { name: "Mở POS bán hàng" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/sales/pos"));
  });

  it("names the required steps still pending and keeps the confirmation closed", async () => {
    installOnboardingApi({ steps: { ...ALL_BEFORE_CONFIRM, opening_debt: "pending" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    await user.click(within(await rail()).getByRole("button", { name: /^Bước \d+:\s*Xác nhận và khóa số dư/ }));
    const confirm = await region("Xác nhận và khóa số dư");
    expect(within(confirm).getByText("Còn 1 bước bắt buộc chưa xong:")).toBeInTheDocument();
    await user.click(within(confirm).getByRole("checkbox"));
    const button = within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" });
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleDescription("Hoàn tất các bước còn thiếu trước.");
    await user.click(within(confirm).getByRole("button", { name: "Công nợ đầu kỳ" }));
    expect(await region("Công nợ đầu kỳ")).toBeInTheDocument();
  });

  it("names the steps the API still sees pending and re-reads the checklist", async () => {
    installOnboardingApi({ steps: ALL_BEFORE_CONFIRM });
    let reads = 0;
    server.use(
      http.post(apiUrl("/onboarding/steps/*"), () => apiError(409, "onboarding_steps_pending", { fields: { opening_cash: "pending" } })),
      http.get(apiUrl(PATHS.onboarding), () => {
        reads++;
        return undefined;
      }),
    );
    const user = userEvent.setup();
    const { router } = renderApp("/onboarding");

    const confirm = await region("Xác nhận và khóa số dư");
    await user.click(within(confirm).getByRole("checkbox"));
    const readsBefore = reads;
    await user.click(within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" }));

    expect(await screen.findByText("Còn bước bắt buộc chưa xong. Hoàn tất các bước trước rồi xác nhận số dư.")).toBeInTheDocument();
    expect(await within(confirm).findByText("Còn 1 bước bắt buộc chưa xong:")).toBeInTheDocument();
    await waitFor(() => expect(reads).toBeGreaterThan(readsBefore));
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

    const confirm = await region("Xác nhận và khóa số dư");
    await user.click(within(confirm).getByRole("checkbox"));
    await user.click(within(confirm).getByRole("button", { name: "Xác nhận số dư đầu kỳ" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
  });

  it("offers the store once every required step is done, with the balances locked", async () => {
    installOnboardingApi({ steps: { ...ALL_BEFORE_CONFIRM, confirm_opening_balances: "completed" } });
    const user = userEvent.setup();
    const { router } = renderApp("/onboarding");

    expect(await screen.findByRole("region", { name: "Cửa hàng đã sẵn sàng bán" })).toBeInTheDocument();
    const nav = await rail();
    expect(within(nav).getByRole("button", { name: /^Bước \d+:\s*Tồn kho đầu kỳ/ })).toHaveTextContent("Đã xong · đã khóa sau xác nhận");
    await user.click(within(nav).getByRole("button", { name: /^Bước \d+:\s*Xác nhận và khóa số dư/ }));
    const confirm = await region("Xác nhận và khóa số dư");
    expect(within(confirm).getByText("Đã xác nhận lúc 01/10/2026 10:00.")).toBeInTheDocument();
    expect(within(confirm).getByText(/Sổ đã khóa đến hết ngày 30\/09\/2026\./)).toBeInTheDocument();
    expect(within(confirm).queryByRole("button", { name: "Xác nhận số dư đầu kỳ" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Vào cửa hàng" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(HOME_PATH));
  });
});
