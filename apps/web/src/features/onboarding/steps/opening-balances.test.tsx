import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, delay, http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../../../shared/ui/reauth-dialog";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { COLOR_DO, CUSTOMER_ID, CUTOFF, DEBT_REASON_ID, NEW_CUSTOMER_ID, installOnboardingApi, testFund, testSkuItem, testStockLine } from "../test-fixtures";

const region = (name: string) => screen.findByRole("region", { name });

describe("Opening stock", () => {
  const before = { store_profile: "completed", catalog_setup: "skipped" } as const;

  it("posts the lines with their lot as one opening document under an Idempotency-Key, then completes the step", async () => {
    const api = installOnboardingApi({ steps: before, skus: [testSkuItem({ track_lot: true, track_expiry: true })] });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    await user.click(await within(stock).findByRole("button", { name: "Ghi tồn kho đầu kỳ" }));
    const product = within(stock).getByRole("combobox", { name: "Hàng dòng 1" });
    expect(product).toHaveAttribute("aria-invalid", "true");
    expect(product).toHaveAccessibleDescription("Chọn hàng cho ít nhất một dòng.");
    await waitFor(() => expect(product).toHaveFocus());
    expect(await screen.findByText(/Còn 1 chỗ cần sửa/)).toBeInTheDocument();

    await user.type(product, "bánh");
    await user.click(await screen.findByRole("option", { name: /Bánh quy bơ/ }));
    expect(product).not.toHaveAttribute("aria-invalid");
    // The cost starts from the SKU's reference cost.
    expect(within(stock).getByRole("textbox", { name: "Giá vốn dòng 1" })).toHaveValue("45.000");
    await user.click(within(stock).getByRole("button", { name: "Ghi tồn kho đầu kỳ" }));
    const qty = within(stock).getByRole("textbox", { name: "Số lượng dòng 1" });
    expect(qty).toHaveAccessibleDescription("Số lượng phải lớn hơn 0.");
    expect(within(stock).getByRole("textbox", { name: "Số lô dòng 1" })).toHaveAccessibleDescription("Nhập số lô.");
    await waitFor(() => expect(qty).toHaveFocus());

    await user.type(qty, "24");
    await user.selectOptions(within(stock).getByRole("combobox", { name: "Màu dòng 1" }), "do");
    await user.type(within(stock).getByRole("textbox", { name: "Số lô dòng 1" }), "L2609");
    await user.type(within(stock).getByRole("textbox", { name: "Hạn dùng dòng 1" }), "31/03/2027");
    expect(within(stock).getAllByText("1.080.000 ₫")).toHaveLength(2);

    await user.click(within(stock).getByRole("button", { name: "Ghi tồn kho đầu kỳ" }));
    await waitFor(() => expect(api.to("/stock-adjustments")).toHaveLength(1));
    const [post] = api.to("/stock-adjustments");
    expect(post?.idempotencyKey).toMatch(/\S+/);
    expect(post?.body).toEqual({
      kind: "opening",
      business_date: CUTOFF,
      lines: [{ sku_id: "sku-1", qty: "24", unit_cost: "45000", color_id: COLOR_DO, lot_no: "L2609", expiry_date: "2027-03-31" }],
    });

    // The owner stays on the step to post another document, and sees what is posted. The API
    // completes the step with the posting itself.
    expect(await within(stock).findByText("Đã xong")).toBeInTheDocument();
    expect(api.to("/onboarding/steps/opening_stock:complete")).toEqual([]);
    const posted = await within(stock).findByRole("table", { name: "Đã ghi tồn đầu kỳ" });
    expect(within(posted).getByText("L2609 · 31/03/2027")).toBeInTheDocument();
    expect(within(stock).getByRole("combobox", { name: "Hàng dòng 1" })).toHaveValue("");
    expect(within(stock).queryByRole("button", { name: "Không có tồn kho đầu kỳ" })).not.toBeInTheDocument();
  });

  it("names its table and columns, and removes a line by its number", async () => {
    installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    const table = await within(stock).findByRole("table", { name: "Tồn kho đầu kỳ đang nhập" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Hàng · Màu", "Số lượng", "Giá vốn (₫)", "Số lô · Hạn dùng", "Thành tiền", "Xóa dòng"]);
    // One line cannot be removed.
    expect(within(table).queryByRole("button", { name: "Xóa dòng 1" })).not.toBeInTheDocument();

    await user.click(within(stock).getByRole("button", { name: "+ Thêm dòng" }));
    await waitFor(() => expect(within(table).getByRole("combobox", { name: "Hàng dòng 2" })).toHaveFocus());
    await user.click(within(table).getByRole("button", { name: "Xóa dòng 2" }));
    expect(within(table).queryByRole("combobox", { name: "Hàng dòng 2" })).not.toBeInTheDocument();
    expect(await screen.findByText("Đã xóa dòng 2.")).toBeInTheDocument();
  });

  it("puts the API's refusal of a line's color under that color", async () => {
    installOnboardingApi({ steps: before });
    server.use(http.post(apiUrl("/stock-adjustments"), () => apiError(422, "validation_failed", { fields: { "lines[0].color_id": "inactive" } })));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    await user.type(await within(stock).findByRole("combobox", { name: "Hàng dòng 1" }), "bánh");
    await user.click(await screen.findByRole("option", { name: /Bánh quy bơ/ }));
    await user.type(within(stock).getByRole("textbox", { name: "Số lượng dòng 1" }), "5");
    await user.selectOptions(within(stock).getByRole("combobox", { name: "Màu dòng 1" }), "do");
    await user.click(within(stock).getByRole("button", { name: "Ghi tồn kho đầu kỳ" }));

    const color = within(stock).getByRole("combobox", { name: "Màu dòng 1" });
    await waitFor(() => expect(color).toHaveAccessibleDescription("Đang ngừng dùng."));
    expect(color).toHaveAttribute("aria-invalid", "true");
  });

  it("shows a posted line's lot or expiry date even when it has only one of them", async () => {
    installOnboardingApi({
      steps: before,
      stockDocs: [
        { id: "adj-1", lines: [testStockLine({ lot_no: "L2609" }), testStockLine({ id: "adj-line-2", expiry_date: "2027-03-31" })] },
      ],
    });
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    const posted = await within(stock).findByRole("table", { name: "Đã ghi tồn đầu kỳ" });
    expect(within(posted).getByText("L2609")).toBeInTheDocument();
    expect(within(posted).getByText("31/03/2027")).toBeInTheDocument();
  });

  it("completes the step with nothing posted after confirming there is no opening stock", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    await user.click(await within(stock).findByRole("button", { name: "Không có tồn kho đầu kỳ" }));
    const dialog = await screen.findByRole("dialog", { name: "Xác nhận không có tồn kho đầu kỳ?" });
    await user.click(within(dialog).getByRole("button", { name: "Đúng, không có tồn kho đầu kỳ" }));

    expect(await screen.findByText("Đã ghi nhận không có tồn kho đầu kỳ.")).toBeInTheDocument();
    expect(await region("Công nợ đầu kỳ")).toBeInTheDocument();
    expect(api.to("/onboarding/steps/opening_stock:complete")).toHaveLength(1);
    expect(api.to("/stock-adjustments")).toEqual([]);
  });
});

describe("Opening debt", () => {
  const before = { store_profile: "completed", catalog_setup: "skipped", opening_stock: "completed" } as const;

  it("posts a customer's opening balance under an Idempotency-Key, lists it, then completes the step", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    expect(within(debt).getByRole("button", { name: "Khách hàng nợ cửa hàng" })).toHaveAttribute("aria-pressed", "true");
    await user.type(within(debt).getByRole("combobox", { name: /^Khách hàng/ }), "lan");
    await user.click(await screen.findByRole("option", { name: /Tạp hóa Cô Lan/ }));
    await user.type(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ }), "2500000");
    await waitFor(() => expect(within(debt).getByRole("combobox", { name: "Lý do" })).toHaveValue(DEBT_REASON_ID));
    await user.type(within(debt).getByRole("textbox", { name: "Ghi chú" }), "Nợ sổ tay cũ");
    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));

    await waitFor(() => expect(api.to("/debt-adjustments")).toHaveLength(1));
    const [post] = api.to("/debt-adjustments");
    expect(post?.idempotencyKey).toMatch(/\S+/);
    expect(post?.body).toEqual({
      party_id: CUSTOMER_ID,
      direction: "receivable",
      kind: "opening_balance",
      amount: "2500000",
      reason_id: DEBT_REASON_ID,
      opening_cutoff_date: CUTOFF,
      note: "Nợ sổ tay cũ",
    });
    const posted = await within(debt).findByRole("table", { name: "Đã ghi công nợ đầu kỳ" });
    expect(within(posted).getByText("Tạp hóa Cô Lan")).toBeInTheDocument();
    expect(within(posted).getByText("Khách nợ")).toBeInTheDocument();
    expect(within(debt).getAllByText("2.500.000 ₫").length).toBeGreaterThan(0);
    // The form starts again on the same side, keeping the reason.
    expect(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ })).toHaveValue("");
    expect(within(debt).getByRole("combobox", { name: "Lý do" })).toHaveValue(DEBT_REASON_ID);
    expect(await within(debt).findByText("Đã xong")).toBeInTheDocument();
    expect(api.to("/onboarding/steps/opening_debt:complete")).toEqual([]);
  });

  it("waits for the adjustment reasons before posting", async () => {
    installOnboardingApi({ steps: before });
    server.use(http.get(apiUrl("/reasons"), () => delay("infinite")));
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    expect(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" })).toBeDisabled();
    expect(within(debt).queryByText("Chưa có lý do điều chỉnh công nợ nào đang dùng.")).not.toBeInTheDocument();
  });

  it("adds a customer on the spot under an Idempotency-Key and posts the balance for them", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    await user.click(within(debt).getByRole("button", { name: "+ Khách hàng mới" }));
    const dialog = await screen.findByRole("dialog", { name: "Thêm khách hàng mới" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Tên khách hàng/ }), "Chị Hoa chợ Bến Thành");
    await user.type(within(dialog).getByRole("textbox", { name: /^Số điện thoại/ }), "0909111222");
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Thêm khách hàng mới" })).not.toBeInTheDocument());
    expect(within(debt).getByRole("combobox", { name: /^Khách hàng/ })).toHaveValue("Chị Hoa chợ Bến Thành");
    const [created] = api.to("/customers");
    expect(created?.idempotencyKey).toMatch(/\S+/);
    expect(created?.body).toMatchObject({ name: "Chị Hoa chợ Bến Thành", phone: "0909111222", payment_terms_days: 0 });

    await user.type(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ }), "800000");
    await waitFor(() => expect(within(debt).getByRole("combobox", { name: "Lý do" })).toHaveValue(DEBT_REASON_ID));
    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));
    await waitFor(() => expect(api.to("/debt-adjustments")).toHaveLength(1));
    expect(api.to("/debt-adjustments")[0]?.body).toMatchObject({ party_id: NEW_CUSTOMER_ID, direction: "receivable", amount: "800000" });
  });

  it("keeps the amount when the side changes, and refuses a balance without a party before sending it", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    await user.type(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ }), "750000");
    await user.click(within(debt).getByRole("button", { name: "Cửa hàng nợ nhà cung cấp" }));
    expect(within(debt).getByRole("button", { name: "Cửa hàng nợ nhà cung cấp" })).toHaveAttribute("aria-pressed", "true");
    expect(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ })).toHaveValue("750.000");
    expect(within(debt).getByRole("button", { name: "+ Nhà cung cấp mới" })).toBeInTheDocument();

    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));
    const supplier = within(debt).getByRole("combobox", { name: /^Nhà cung cấp/ });
    expect(supplier).toHaveAttribute("aria-invalid", "true");
    expect(within(debt).getByText("Chọn nhà cung cấp.")).toBeVisible();
    await waitFor(() => expect(supplier).toHaveFocus());
    expect(api.to("/debt-adjustments")).toEqual([]);

    await user.clear(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ }));
    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));
    expect(within(debt).getByText("Số tiền nợ phải lớn hơn 0.")).toBeVisible();
    expect(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ })).toHaveAttribute("aria-invalid", "true");
  });
});

describe("Opening cash", () => {
  const before = { store_profile: "completed", catalog_setup: "skipped", opening_stock: "completed", opening_debt: "completed" } as const;

  it("saves every fund one after another under its If-Match, then completes the step and opens the confirmation", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    const table = await within(cash).findByRole("table", { name: "Số dư quỹ đầu kỳ" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Quỹ", "Đã lưu", "Số dư đầu kỳ (₫)"]);
    expect(within(table).getAllByText("Chưa lưu")).toHaveLength(2);
    await user.type(within(table).getByRole("textbox", { name: "Tiền mặt tại cửa hàng" }), "5000000");
    // A blank fund counts as 0 ₫.
    expect(within(table).getByRole("row", { name: /Tổng tiền các quỹ/ })).toHaveTextContent("5.000.000 ₫");
    await user.click(within(cash).getByRole("button", { name: "Lưu số dư các quỹ" }));

    expect(await screen.findByText("Đã lưu số dư 2 quỹ.")).toBeInTheDocument();
    await waitFor(() => expect(api.to("/onboarding/steps/opening_cash:complete")).toHaveLength(1));
    expect(api.calls.filter((c) => c.path.startsWith("/funds/"))).toEqual([
      { method: "PATCH", path: "/funds/fund-cash", body: { opening_balance: "5000000", opening_date: CUTOFF }, idempotencyKey: null, ifMatch: '"1"' },
      { method: "PATCH", path: "/funds/fund-bank", body: { opening_balance: "0", opening_date: CUTOFF }, idempotencyKey: null, ifMatch: '"2"' },
    ]);
    expect(await region("Xác nhận và khóa số dư")).toBeInTheDocument();
  });

  it("starts a saved fund from its balance and leaves it alone when unchanged", async () => {
    const api = installOnboardingApi({
      steps: before,
      funds: [testFund({ opening_balance: "3000000", opening_date: CUTOFF }), testFund({ id: "fund-bank", kind: "bank", name: "Tài khoản ngân hàng", version: 2 })],
    });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    expect(await within(cash).findByRole("textbox", { name: "Tiền mặt tại cửa hàng" })).toHaveValue("3.000.000");
    await user.click(within(cash).getByRole("button", { name: "Lưu số dư các quỹ" }));
    await waitFor(() => expect(api.to("/onboarding/steps/opening_cash:complete")).toHaveLength(1));
    expect(api.calls.filter((c) => c.path.startsWith("/funds/")).map((c) => c.path)).toEqual(["/funds/fund-bank"]);
  });

  it("skips the funds already saved when it retries after the password check", async () => {
    const api = installOnboardingApi({ steps: before });
    let bankTries = 0;
    server.use(
      http.patch(apiUrl("/funds/fund-bank"), () => (bankTries++ === 0 ? apiError(403, "reauth_required") : undefined)),
      http.post(apiUrl(PATHS.reauth), () => HttpResponse.json({ reauthenticated_until: "2026-10-18T09:05:00Z" })),
    );
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    await user.type(await within(cash).findByRole("textbox", { name: "Tiền mặt tại cửa hàng" }), "5000000");
    await user.click(within(cash).getByRole("button", { name: "Lưu số dư các quỹ" }));
    const reauth = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    await user.type(within(reauth).getByLabelText("Mật khẩu"), "mat-khau");
    await user.click(within(reauth).getByRole("button", { name: REAUTH_SUBMIT }));

    expect(await screen.findByText("Đã lưu số dư 2 quỹ.")).toBeInTheDocument();
    expect(api.calls.filter((c) => c.path.startsWith("/funds/")).map((c) => [c.path, c.ifMatch])).toEqual([
      ["/funds/fund-cash", '"1"'],
      ["/funds/fund-bank", '"2"'],
    ]);
  });

  it("explains that a fund with entries keeps its opening balance", async () => {
    installOnboardingApi({ steps: before });
    server.use(http.patch(apiUrl("/funds/:id"), () => apiError(409, "fund_has_entries")));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    await user.type(await within(cash).findByRole("textbox", { name: "Tài khoản ngân hàng" }), "1000000");
    await user.click(within(cash).getByRole("button", { name: "Lưu số dư các quỹ" }));
    expect(await screen.findByText(/Quỹ đã có phiếu thu chi nên không sửa số dư đầu kỳ được\./)).toBeInTheDocument();
  });
});
