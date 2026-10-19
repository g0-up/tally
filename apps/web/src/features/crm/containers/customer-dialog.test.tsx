import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { customerSchema } from "../schemas";
import { CUSTOMER_ID, GROUPS, RETAIL_GROUP_ID, WHOLESALE_GROUP_ID, testConsent, testCustomer, testPriceList } from "../testing/fixtures";
import { CustomerDialog } from "./customer-dialog";

const NEW_ID = "cus-new";

interface Sent {
  method: string;
  path: string;
  body: unknown;
  ifMatch: string | null;
  key: string | null;
}

/** Lookups of the form and a recorder of every customer write. */
function dialogHandlers({ consents = [testConsent()], saved = testCustomer({ id: NEW_ID, name: "Cô Ba Bình", version: 1 }) } = {}) {
  const sent: Sent[] = [];
  const record = async (request: Request) => {
    sent.push({ method: request.method, path: new URL(request.url).pathname.replace(/^.*\/customers/, "/customers"), body: await request.json(), ifMatch: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key") });
  };
  const handlers = [
    http.get(apiUrl("/customer-groups"), ({ request }) => {
      expect(new URL(request.url).searchParams.get("active")).toBe("true");
      return HttpResponse.json({ data: GROUPS });
    }),
    http.get(apiUrl("/price-lists"), () =>
      HttpResponse.json({ data: [testPriceList(), testPriceList({ id: "pl-own", name: "Giá riêng Minh An", kind: "customer", customer_group_id: null, party_id: CUSTOMER_ID, priority: 5 })], next_cursor: null }),
    ),
    http.get(apiUrl("/customers/:id/consents"), () => HttpResponse.json({ current: consents, history: [] })),
    http.post(apiUrl("/customers"), async ({ request }) => {
      await record(request);
      return HttpResponse.json({ ...saved, duplicate_warnings: [] }, { status: 201 });
    }),
    http.patch(apiUrl("/customers/:id"), async ({ request, params }) => {
      await record(request);
      return HttpResponse.json({ ...testCustomer({ id: params.id, version: 5 }), duplicate_warnings: [] });
    }),
    http.post(apiUrl("/customers/:id/consents"), async ({ request }) => {
      await record(request);
      return HttpResponse.json(testConsent(), { status: 201 });
    }),
  ];
  return { handlers, sent };
}

const field = (dialog: HTMLElement, name: RegExp) => within(dialog).getByRole("textbox", { name });

/** The dialog once its lookups have loaded and the form shows. */
async function formDialog(name: string): Promise<HTMLElement> {
  const nameBox = await screen.findByRole("textbox", { name: /^Tên khách hàng \*/ });
  const dialog = nameBox.closest<HTMLElement>("[role=dialog]");
  if (dialog === null) throw new Error("The form is not in a dialog");
  expect(dialog).toHaveAccessibleName(name);
  return dialog;
}

describe("CustomerDialog", () => {
  it("adds a customer, then records the consent with its source", async () => {
    const { handlers, sent } = dialogHandlers();
    server.use(...handlers);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={onClose} />);

    const dialog = await formDialog("Thêm khách hàng");
    expect(within(dialog).getByText("Mã được tạo tự động. Chỉ tên là bắt buộc.")).toBeInTheDocument();
    const group = within(dialog).getByRole("combobox", { name: /^Nhóm khách/ });
    expect(group).toHaveValue(RETAIL_GROUP_ID);
    expect(within(dialog).getByText("Bảng giá áp dụng: Giá bán lẻ")).toBeInTheDocument();
    expect(within(dialog).getByText("Chưa có đồng ý thì tin khuyến mãi và chiến dịch bỏ qua khách này.")).toBeInTheDocument();

    await user.type(field(dialog, /^Tên khách hàng \*/), "Cô Ba Bình");
    await user.type(field(dialog, /^Số điện thoại/), "0909 111 222");
    await user.selectOptions(group, WHOLESALE_GROUP_ID);
    expect(await within(dialog).findByText("Bảng giá áp dụng: Bảng giá sỉ mùa Tết → Giá bán sỉ")).toBeInTheDocument();
    await user.type(field(dialog, /^Hạn mức nợ/), "5000000");
    const terms = field(dialog, /^Số ngày nợ được phép/);
    await user.clear(terms);
    await user.type(terms, "7");
    await user.click(within(dialog).getByRole("checkbox", { name: "Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ" }));
    await user.selectOptions(within(dialog).getByRole("combobox", { name: /^Kênh nhận tin/ }), "sms");

    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Ghi nơi khách đồng ý hoặc rút đồng ý (tại quầy, qua điện thoại…).");
    expect(sent).toEqual([]);

    await user.type(field(dialog, /^Nguồn đồng ý/), "tại quầy");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));

    expect(await screen.findByText(/^Đã thêm/)).toHaveTextContent("Đã thêm Cô Ba Bình. Có thể chọn ngay khi tạo đơn bán.");
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent).toEqual([
      {
        method: "POST",
        path: "/customers",
        body: { kind: "person", name: "Cô Ba Bình", phone: "0909 111 222", email: null, tax_code: null, address: null, note: null, customer_group_id: WHOLESALE_GROUP_ID, credit_limit: "5000000", payment_terms_days: 7, status: "active" },
        ifMatch: null,
        key: expect.stringMatching(/.+/),
      },
      { method: "POST", path: `/customers/${NEW_ID}/consents`, body: { channel: "sms", purpose: "marketing", granted: true, source: "tại quầy" }, ifMatch: null, key: expect.stringMatching(/.+/) },
    ]);
    expect(sent[1]?.key).not.toBe(sent[0]?.key);
  });

  it("checks the fields before sending", async () => {
    const { handlers, sent } = dialogHandlers();
    server.use(...handlers);
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={() => {}} />);

    const dialog = await formDialog("Thêm khách hàng");
    await user.type(field(dialog, /^Số điện thoại/), "abc");
    await user.type(field(dialog, /^Mã số thuế/), "12345");
    await user.type(field(dialog, /^Email/), "an@");
    const terms = field(dialog, /^Số ngày nợ được phép/);
    await user.clear(terms);
    await user.type(terms, "400");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));

    const alerts = await within(dialog).findAllByRole("alert");
    expect(alerts.map((a) => a.textContent)).toEqual(["Nhập tên khách hàng.", "Số điện thoại không hợp lệ.", "Mã số thuế gồm 10 chữ số, có thể thêm “-” và 3 chữ số.", "Email không hợp lệ.", "Số ngày nợ từ 0 đến 365."]);
    expect(await screen.findByText("Chưa ghi sổ.")).toBeInTheDocument();
    await waitFor(() => expect(field(dialog, /^Tên khách hàng/)).toHaveFocus());
    expect(field(dialog, /^Tên khách hàng/)).toHaveAttribute("aria-invalid", "true");
    expect(sent).toEqual([]);
  });

  it("saves an edit at the customer's version and withdraws every granted consent", async () => {
    const { handlers, sent } = dialogHandlers({ consents: [testConsent(), testConsent({ channel: "email" }), testConsent({ channel: "sms", granted: false })] });
    server.use(...handlers);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={customerSchema.parse(testCustomer())} onClose={onClose} />);

    const dialog = await formDialog("Sửa khách hàng · Tạp hóa Minh An");
    expect(within(dialog).getByText("Đổi số ngày nợ chỉ áp dụng cho đơn bán mới; nợ đã ghi giữ hạn cũ.")).toBeInTheDocument();
    expect(field(dialog, /^Tên khách hàng \*/)).toHaveValue("Tạp hóa Minh An");
    expect(field(dialog, /^Hạn mức nợ/)).toHaveValue("50.000.000");
    expect(await within(dialog).findByText("Bảng giá áp dụng: Giá riêng Minh An → Bảng giá sỉ mùa Tết → Giá bán sỉ")).toBeInTheDocument();
    const consent = within(dialog).getByRole("checkbox", { name: "Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ" });
    expect(consent).toBeChecked();
    expect(within(dialog).getByRole("combobox", { name: /^Kênh nhận tin/ })).toHaveValue("zalo");

    const terms = field(dialog, /^Số ngày nợ được phép/);
    await user.clear(terms);
    await user.type(terms, "30");
    await user.click(consent);
    await user.type(field(dialog, /^Nguồn đồng ý/), "qua điện thoại");
    await user.click(within(dialog).getByRole("button", { name: "Lưu" }));

    expect(await screen.findByText("Đã lưu khách hàng.")).toBeInTheDocument();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent.map((s) => [s.method, s.path, s.ifMatch, s.key === null])).toEqual([
      ["PATCH", `/customers/${CUSTOMER_ID}`, etagOf(4), true],
      ["POST", `/customers/${CUSTOMER_ID}/consents`, null, false],
      ["POST", `/customers/${CUSTOMER_ID}/consents`, null, false],
    ]);
    expect(sent[1]?.key).not.toBe(sent[2]?.key);
    expect(sent[0]?.body).toMatchObject({ payment_terms_days: 30, customer_group_id: WHOLESALE_GROUP_ID, credit_limit: "50000000" });
    expect(sent.slice(1).map((s) => s.body)).toEqual([
      { channel: "zalo", purpose: "marketing", granted: false, source: "qua điện thoại" },
      { channel: "email", purpose: "marketing", granted: false, source: "qua điện thoại" },
    ]);
  });

  it("puts the server's field refusals under the fields and keeps the dialog open", async () => {
    const onClose = vi.fn();
    server.use(http.patch(apiUrl(`/customers/${CUSTOMER_ID}`), () => apiError(422, "validation_failed", { fields: { tax_code: "pattern" } })), ...dialogHandlers({ consents: [] }).handlers);
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={customerSchema.parse(testCustomer())} onClose={onClose} />);

    const dialog = await formDialog("Sửa khách hàng · Tạp hóa Minh An");
    await user.click(within(dialog).getByRole("button", { name: "Lưu" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Mã số thuế gồm 10 chữ số, có thể thêm “-” và 3 chữ số.");
    expect(field(dialog, /^Mã số thuế/)).toHaveAttribute("aria-invalid", "true");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("says when the partner is a customer already", async () => {
    server.use(http.post(apiUrl("/customers"), () => apiError(409, "already_customer")), ...dialogHandlers().handlers);
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={() => {}} />);

    const dialog = await formDialog("Thêm khách hàng");
    await user.type(field(dialog, /^Tên khách hàng \*/), "Tạp hóa Minh An");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    expect(await screen.findByText("Đối tác này đã là khách hàng.")).toBeInTheDocument();
  });

  it("names the stored customer when the consent is refused, and does not add it twice on retry", async () => {
    const consentKeys: (string | null)[] = [];
    const { handlers, sent } = dialogHandlers();
    server.use(
      http.post(apiUrl(`/customers/${NEW_ID}/consents`), async ({ request }) => {
        consentKeys.push(request.headers.get("Idempotency-Key"));
        if (consentKeys.length === 1) return apiError(422, "validation_failed", { message: "Nguồn quá dài" });
        sent.push({ method: "POST", path: `/customers/${NEW_ID}/consents`, body: await request.json(), ifMatch: null, key: null });
        return HttpResponse.json(testConsent({ party_id: NEW_ID }), { status: 201 });
      }),
      ...handlers,
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={onClose} />);

    const dialog = await formDialog("Thêm khách hàng");
    await user.type(field(dialog, /^Tên khách hàng \*/), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("checkbox", { name: "Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ" }));
    await user.type(field(dialog, /^Nguồn đồng ý/), "tại quầy");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));

    expect(await screen.findByText(/^Đã lưu Cô Ba Bình nhưng chưa ghi đồng ý nhận tin:/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.click(await within(dialog).findByRole("button", { name: "Thêm khách hàng" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent.map((s) => [s.method, s.path])).toEqual([
      ["POST", "/customers"],
      ["PATCH", `/customers/${NEW_ID}`],
      ["POST", `/customers/${NEW_ID}/consents`],
    ]);
    expect(sent[1]?.ifMatch).toBe(etagOf(1));
    expect(consentKeys).toHaveLength(2);
    expect(consentKeys[0]).toMatch(/.+/);
    expect(consentKeys[1]).toBe(consentKeys[0]);
  });

  it("records a consent whose source was edited after a lost answer under a key of its own", async () => {
    const consents: { key: string | null; body: unknown }[] = [];
    const { handlers } = dialogHandlers();
    server.use(
      http.post(apiUrl(`/customers/${NEW_ID}/consents`), async ({ request }) => {
        consents.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        return consents.length === 1 ? HttpResponse.error() : HttpResponse.json(testConsent({ party_id: NEW_ID }), { status: 201 });
      }),
      ...handlers,
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={onClose} />);

    const dialog = await formDialog("Thêm khách hàng");
    await user.type(field(dialog, /^Tên khách hàng \*/), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("checkbox", { name: "Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ" }));
    await user.type(field(dialog, /^Nguồn đồng ý/), "tại quầy");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    expect(await screen.findByText(/^Đã lưu Cô Ba Bình nhưng chưa ghi đồng ý nhận tin:/)).toBeInTheDocument();

    await user.type(field(dialog, /^Nguồn đồng ý/), ", qua Zalo");
    await user.click(await within(dialog).findByRole("button", { name: "Thêm khách hàng" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(consents.map((c) => c.body)).toMatchObject([{ source: "tại quầy" }, { source: "tại quầy, qua Zalo" }]);
    expect(consents[1]?.key).toMatch(/.+/);
    expect(consents[1]?.key).not.toBe(consents[0]?.key);
  });

  it("sends the create again under the same key when its answer was lost, and adds the customer once", async () => {
    const keys: (string | null)[] = [];
    const bodies: unknown[] = [];
    const { handlers } = dialogHandlers();
    server.use(
      http.post(apiUrl("/customers"), async ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        bodies.push(await request.json());
        if (keys.length === 1) return HttpResponse.error();
        return HttpResponse.json({ ...testCustomer({ id: NEW_ID, name: "Cô Ba Bình", version: 1 }), duplicate_warnings: [] }, { status: 201 });
      }),
      ...handlers,
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={onClose} />);

    const dialog = await formDialog("Thêm khách hàng");
    await user.type(field(dialog, /^Tên khách hàng \*/), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    expect(await screen.findByText("Chưa rõ đã ghi sổ hay chưa. Bấm lại để kiểm tra; không tạo chứng từ mới.")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    // Closing now would lose the check, so the dialog asks first.
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(CLOSE_UNSETTLED_MESSAGE);
    await user.click(within(dialog).getByRole("button", { name: "Ở lại" }));
    expect(onClose).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(/.+/);
    expect(keys[1]).toBe(keys[0]);
    expect(bodies[1]).toEqual(bodies[0]);
  });

  it("writes edits made after a lost answer onto the customer the replay answered", async () => {
    const keys: (string | null)[] = [];
    const { handlers, sent } = dialogHandlers();
    server.use(
      http.post(apiUrl("/customers"), async ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        const body = await request.json();
        if (keys.length === 1) return HttpResponse.error();
        sent.push({ method: "POST", path: "/customers", body, ifMatch: null, key: keys.at(-1) ?? null });
        return HttpResponse.json({ ...testCustomer({ id: NEW_ID, name: "Cô Ba Bình", version: 1 }), duplicate_warnings: [] }, { status: 201 });
      }),
      ...handlers,
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<CustomerDialog customer={null} onClose={onClose} />);

    const dialog = await formDialog("Thêm khách hàng");
    await user.type(field(dialog, /^Tên khách hàng \*/), "Cô Ba Bình");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    expect(await screen.findByText("Chưa rõ đã ghi sổ hay chưa. Bấm lại để kiểm tra; không tạo chứng từ mới.")).toBeInTheDocument();

    await user.type(field(dialog, /^Số điện thoại/), "0909111222");
    await user.click(within(dialog).getByRole("button", { name: "Thêm khách hàng" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(keys[1]).toBe(keys[0]);
    expect(sent.map((s) => [s.method, s.path, s.ifMatch])).toEqual([
      ["POST", "/customers", null],
      ["PATCH", `/customers/${NEW_ID}`, etagOf(1)],
    ]);
    expect(sent[0]?.body).toMatchObject({ phone: null });
    expect(sent[1]?.body).toMatchObject({ phone: "0909111222" });
  });
});
