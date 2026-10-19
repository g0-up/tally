import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { COLOR_DO, CUSTOMER_ID, CUTOFF, DEBT_REASON_ID, NEW_CUSTOMER_ID, installOnboardingApi, testSkuItem } from "../test-fixtures";

const region = (name: string) => screen.findByRole("region", { name });

describe("Opening stock", () => {
  it("posts the lines with their lot as one opening document under an Idempotency-Key, then completes the step", async () => {
    const api = installOnboardingApi({
      steps: { store_profile: "completed", catalog_setup: "skipped" },
      skus: [testSkuItem({ track_lot: true, track_expiry: true })],
    });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    await user.click(within(stock).getByRole("button", { name: "Ghi tồn kho đầu kỳ" }));
    expect(await within(stock).findByRole("alert")).toHaveTextContent("Chọn hàng cho từng dòng.");
    const product = within(stock).getByRole("combobox", { name: "Hàng dòng 1" });
    expect(product).toHaveAttribute("aria-invalid", "true");
    expect(product).toHaveAccessibleDescription("Chọn hàng cho từng dòng.");
    await waitFor(() => expect(product).toHaveFocus());
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();

    await user.type(product, "bánh");
    await user.click(await screen.findByRole("option", { name: /Bánh quy bơ/ }));
    expect(within(stock).queryByRole("alert")).not.toBeInTheDocument();
    // Once a SKU is picked the line's inputs are named by its code.
    expect(within(stock).getByRole("combobox", { name: "Hàng SP001" })).not.toHaveAttribute("aria-invalid");
    // The cost starts from the SKU's reference cost.
    expect(within(stock).getByRole("textbox", { name: "Giá vốn SP001" })).toHaveValue("45.000");
    await user.click(within(stock).getByRole("button", { name: "Ghi tồn kho đầu kỳ" }));
    const qty = within(stock).getByRole("textbox", { name: "Số lượng SP001 (hộp)" });
    expect(await within(stock).findByRole("alert")).toHaveTextContent("Số lượng của SP001 phải lớn hơn 0.");
    expect(qty).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(qty).toHaveFocus());
    await user.type(qty, "24");
    await user.selectOptions(within(stock).getByRole("combobox", { name: "Màu SP001" }), "do");
    await user.type(within(stock).getByRole("textbox", { name: "Số lô SP001" }), "L2609");
    await user.type(within(stock).getByRole("textbox", { name: "Hạn dùng SP001" }), "31/03/2027");
    expect(within(stock).getAllByText("1.080.000 ₫")).toHaveLength(2);

    await user.click(within(stock).getByRole("button", { name: "Ghi tồn kho đầu kỳ" }));
    expect(await screen.findByText("DC-0001")).toBeInTheDocument();
    await waitFor(() => expect(api.to("/onboarding/steps/opening_stock:complete")).toHaveLength(1));

    const [post] = api.to("/stock-adjustments");
    expect(post?.idempotencyKey).toMatch(/\S+/);
    expect(post?.body).toEqual({
      kind: "opening",
      business_date: CUTOFF,
      lines: [{ sku_id: "sku-1", qty: "24", unit_cost: "45000", color_id: COLOR_DO, lot_no: "L2609", expiry_date: "2027-03-31" }],
    });
    // The owner stays on the step to post another document.
    expect(await within(stock).findByText("Đã xong")).toBeInTheDocument();
    expect(within(stock).getByRole("combobox", { name: "Hàng dòng 1" })).toHaveValue("");
  });
  it("names its table, its columns and the inputs and buttons of each line", async () => {
    installOnboardingApi({
      steps: { store_profile: "completed", catalog_setup: "skipped" },
      skus: [testSkuItem()],
    });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const stock = await region("Tồn kho đầu kỳ");
    const table = within(stock).getByRole("table", { name: "Tồn kho đầu kỳ" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Hàng", "Màu", "Số lượng", "Giá vốn", "Số lô · Hạn dùng", "Thành tiền", "Thao tác"]);
    expect(within(table).getByRole("button", { name: "Xóa dòng 1" }).closest("td")).toHaveClass("acts");

    await user.click(within(stock).getByRole("button", { name: "+ Thêm dòng" }));
    await user.type(within(stock).getByRole("combobox", { name: "Hàng dòng 2" }), "bánh");
    await user.click(await screen.findByRole("option", { name: /Bánh quy bơ/ }));
    expect(within(table).getByRole("button", { name: "Xóa dòng 2 · SP001" })).toBeEnabled();
    expect(within(table).getByRole("textbox", { name: "Giá vốn SP001" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Hàng dòng 1" })).toBeInTheDocument();

    // The same SKU on two lines keeps the line number next to the code.
    await user.type(within(stock).getByRole("combobox", { name: "Hàng dòng 1" }), "bánh");
    await user.click(await screen.findByRole("option", { name: /Bánh quy bơ/ }));
    expect(within(table).getByRole("textbox", { name: "Giá vốn SP001 dòng 1" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Giá vốn SP001 dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa dòng 1 · SP001" })).toBeInTheDocument();
  });
});

describe("Opening debt", () => {
  it("posts a customer's opening balance under an Idempotency-Key, then completes the step", async () => {
    const api = installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "skipped", opening_stock: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    expect(within(debt).getByRole("button", { name: "Khách hàng nợ cửa hàng" })).toHaveAttribute("aria-pressed", "true");
    await user.type(within(debt).getByRole("combobox", { name: "Khách hàng" }), "lan");
    await user.click(await screen.findByRole("option", { name: /Tạp hóa Cô Lan/ }));
    await user.type(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ }), "2500000");
    await waitFor(() => expect(within(debt).getByRole("combobox", { name: /^Lý do/ })).toHaveValue(DEBT_REASON_ID));
    await user.type(within(debt).getByRole("textbox", { name: /^Ghi chú/ }), "Nợ sổ tay cũ");
    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));

    expect(await screen.findByText("DCN-0001")).toBeInTheDocument();
    await waitFor(() => expect(api.to("/onboarding/steps/opening_debt:complete")).toHaveLength(1));
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
  });

  it("adds a customer on the spot under an Idempotency-Key and posts the balance for them", async () => {
    const api = installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "skipped", opening_stock: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    await user.click(within(debt).getByRole("button", { name: "+ Khách hàng mới" }));
    const dialog = await screen.findByRole("dialog", { name: "Thêm khách hàng mới" });
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));
    expect(await within(dialog).findByText("Nhập tên khách hàng.")).toBeInTheDocument();
    expect(api.to("/customers")).toEqual([]);

    await user.type(within(dialog).getByRole("textbox", { name: /^Tên khách hàng/ }), "Chị Hoa chợ Bến Thành");
    await user.type(within(dialog).getByRole("textbox", { name: /^Số điện thoại/ }), "0909111222");
    await user.click(within(dialog).getByRole("button", { name: "Thêm & chọn" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Thêm khách hàng mới" })).not.toBeInTheDocument());
    expect(screen.getByText("Chị Hoa chợ Bến Thành", { selector: "b" })).toBeInTheDocument();
    expect(within(debt).getByRole("combobox", { name: "Khách hàng" })).toHaveValue("Chị Hoa chợ Bến Thành");
    const [created] = api.to("/customers");
    expect(created?.idempotencyKey).toMatch(/\S+/);
    expect(created?.body).toMatchObject({ name: "Chị Hoa chợ Bến Thành", phone: "0909111222", payment_terms_days: 0 });

    await user.type(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ }), "800000");
    await waitFor(() => expect(within(debt).getByRole("combobox", { name: /^Lý do/ })).toHaveValue(DEBT_REASON_ID));
    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));
    await waitFor(() => expect(api.to("/debt-adjustments")).toHaveLength(1));
    expect(api.to("/debt-adjustments")[0]?.body).toMatchObject({ party_id: NEW_CUSTOMER_ID, direction: "receivable", amount: "800000" });
  });

  it("refuses a balance without an amount before sending it", async () => {
    const api = installOnboardingApi({ steps: { store_profile: "completed", catalog_setup: "skipped", opening_stock: "completed" } });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const debt = await region("Công nợ đầu kỳ");
    await user.click(within(debt).getByRole("button", { name: "Cửa hàng nợ nhà cung cấp" }));
    expect(within(debt).getByRole("combobox", { name: "Nhà cung cấp" })).toBeInTheDocument();
    await user.click(within(debt).getByRole("button", { name: "Ghi công nợ đầu kỳ" }));
    expect(await screen.findAllByText("Chọn nhà cung cấp.")).not.toHaveLength(0);
    expect(within(debt).getByText("Số tiền nợ phải lớn hơn 0.")).toBeInTheDocument();
    const supplier = within(debt).getByRole("combobox", { name: "Nhà cung cấp" });
    expect(supplier).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(supplier).toHaveFocus());
    expect(within(debt).getByRole("textbox", { name: /^Số tiền nợ/ })).toHaveAttribute("aria-invalid", "true");
    expect(api.to("/debt-adjustments")).toEqual([]);
  });
});

describe("Opening cash", () => {
  const before = { store_profile: "completed", catalog_setup: "skipped", opening_stock: "completed", opening_debt: "completed" } as const;

  it("saves a fund's opening balance under If-Match, then completes the step", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    const amount = await within(cash).findByRole("textbox", { name: "Số dư đầu kỳ Tiền mặt tại cửa hàng" });
    await user.clear(amount);
    await user.type(amount, "5000000");
    await user.click(within(cash).getByRole("button", { name: "Lưu số dư Tiền mặt tại cửa hàng" }));

    expect(await screen.findByText("Đã lưu số dư đầu kỳ của Tiền mặt tại cửa hàng.")).toBeInTheDocument();
    await waitFor(() => expect(api.to("/onboarding/steps/opening_cash:complete")).toHaveLength(1));
    expect(api.to("/funds/fund-cash")).toEqual([
      { method: "PATCH", path: "/funds/fund-cash", body: { opening_balance: "5000000", opening_date: CUTOFF }, idempotencyKey: null, ifMatch: '"1"' },
    ]);
    const row = within(cash).getByRole("textbox", { name: "Số dư đầu kỳ Tiền mặt tại cửa hàng" }).closest("tr");
    expect(row).not.toBeNull();
    if (row) expect(within(row).getByText("5.000.000 ₫")).toBeInTheDocument();
  });

  it("names its table and columns, and marks a fund saved without a balance at its input", async () => {
    const api = installOnboardingApi({ steps: before });
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    const table = await within(cash).findByRole("table", { name: "Số dư quỹ đầu kỳ" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Quỹ", "Số dư đầu kỳ đã lưu", "Số dư đầu kỳ", "Thao tác"]);
    const save = within(table).getByRole("button", { name: "Lưu số dư Tiền mặt tại cửa hàng" });
    expect(save.closest("td")).toHaveClass("acts");

    const amount = within(table).getByRole("textbox", { name: "Số dư đầu kỳ Tiền mặt tại cửa hàng" });
    await user.clear(amount);
    await user.click(save);
    expect(amount).toHaveFocus();
    expect(amount).toHaveAttribute("aria-invalid", "true");
    expect(amount).toHaveAccessibleDescription("Nhập số dư đầu kỳ của Tiền mặt tại cửa hàng.");
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(api.to("/funds/fund-cash")).toEqual([]);

    await user.type(amount, "1");
    expect(amount).not.toHaveAttribute("aria-invalid");
    expect(within(table).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("explains that a fund with entries keeps its opening balance", async () => {
    installOnboardingApi({ steps: before });
    server.use(http.patch(apiUrl("/funds/:id"), () => apiError(409, "fund_has_entries")));
    const user = userEvent.setup();
    renderApp("/onboarding");

    const cash = await region("Tiền đầu kỳ");
    const amount = await within(cash).findByRole("textbox", { name: "Số dư đầu kỳ Tài khoản ngân hàng" });
    await user.clear(amount);
    await user.type(amount, "1000000");
    await user.click(within(cash).getByRole("button", { name: "Lưu số dư Tài khoản ngân hàng" }));
    expect(await screen.findByText("Quỹ đã có phiếu thu chi nên không sửa số dư đầu kỳ được.")).toBeInTheDocument();
  });
});
