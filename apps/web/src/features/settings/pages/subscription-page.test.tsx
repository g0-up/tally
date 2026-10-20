import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { REAUTH_SUBMIT, REAUTH_TITLE } from "../../../shared/ui/reauth-dialog";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { SUPPORT_VIEW_MODULES } from "../api/support";
import { ADDONS_TITLE } from "../components/plan-cards";
import { ACCEPT_TERMS_LABEL, ADDON_BUY_SUBMIT, PLAN_CHANGE_SUBMIT } from "../components/plan-change-dialog";
import { NO_GRANTABLE_TICKET, SUPPORT_GRANTED, SUPPORT_GRANT_LABEL, SUPPORT_REVOKED } from "../components/support-access-card";
import { NEW_TICKET_LABEL, NOT_SENT, POSSIBLE_PASSWORD_WARNING, SEND_ANYWAY_LABEL, SEND_REPLY_LABEL, SEND_TICKET_LABEL } from "../components/support-tickets-card";
import {
  ADDON_PLAN,
  SEASON_PLAN,
  STORE_PLAN,
  subscriptionHandlers,
  testActiveAddon,
  testInvoice,
  testSubscriptionView,
  testSupportSession,
  testTicket,
  testTicketView,
} from "../testing/fixtures";
import { SUBSCRIPTION_LEAD } from "./subscription-page";

const PATH = "/settings/subscription";

const findCard = async (title: string) => (await screen.findByRole("heading", { name: title })).closest(".card") as HTMLElement;

const SEASON_QUOTE = {
  plan: SEASON_PLAN,
  purpose: "plan_change",
  period_start: "2026-10-12",
  period_end: "2027-01-09",
  amount: "850000",
  proration_note: "Trừ 140.000 ₫ còn lại của gói Cửa hàng.",
  terms: "Gói mùa không tự gia hạn.",
};

const ADDON_QUOTE = {
  plan: ADDON_PLAN,
  purpose: "addon",
  period_start: "2026-10-12",
  period_end: "2026-11-11",
  amount: "0",
  proration_note: "",
  terms: "Add-on dùng theo kỳ của thuê bao.",
};

/** The add-on's paid invoice, which is what puts it in force over its period. */
const addonInvoice = (over: Parameters<typeof testInvoice>[0] = {}) =>
  testInvoice({ id: "inv-2", number: "HD-2026-0008", plan_id: ADDON_PLAN.id, purpose: "addon", period_start: "2026-10-12", period_end: "2026-11-11", amount: "0", status: "paid", paid_at: "2026-10-12T02:00:00Z", ...over });

interface ChangeCall {
  body: unknown;
  key: string | null;
}

describe("SubscriptionPage", () => {
  it("shows the plans, the current one marked, and the subscription's terms", async () => {
    server.use(...subscriptionHandlers());
    renderApp(PATH);

    expect(await screen.findByText(SUBSCRIPTION_LEAD)).toBeInTheDocument();
    const store = await findCard(STORE_PLAN.name);
    expect(within(store).getByText("Đang dùng")).toBeInTheDocument();
    expect(within(store).queryByRole("button", { name: `Đổi sang gói này: ${STORE_PLAN.name}` })).not.toBeInTheDocument();
    expect(within(await findCard(SEASON_PLAN.name)).getByRole("button", { name: `Đổi sang gói này: ${SEASON_PLAN.name}` })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Dùng thử" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: `Đổi sang gói này: ${ADDON_PLAN.name}` })).not.toBeInTheDocument();
    const addons = await screen.findByRole("region", { name: ADDONS_TITLE });
    expect(await within(addons).findByRole("button", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` })).toBeInTheDocument();
    expect(addons).toHaveTextContent("Giá: 49.000 ₫ / tháng");

    const current = await findCard("Thuê bao hiện tại");
    expect(current).toHaveTextContent("01/10/2026 – 31/10/2026");
    expect(current).toHaveTextContent("Ân hạn 7 ngày, rồi Chỉ đọc 30 ngày, vẫn xuất được dữ liệu");
    expect(current).toHaveTextContent("2.000 SKU · 1.024 MB lưu trữ");
    expect(await within(await findCard("Hóa đơn thuê bao")).findByText("Chưa có hóa đơn thuê bao.")).toBeInTheDocument();
  });

  it("marks the base plan when the store is on another one", async () => {
    const base = testSubscriptionView();
    const subscription = base.subscription ? { ...base.subscription, plan_id: SEASON_PLAN.id } : null;
    server.use(...subscriptionHandlers({ view: { ...base, plan: SEASON_PLAN, subscription } }));
    renderApp(PATH);

    expect(within(await findCard(STORE_PLAN.name)).getByText("Gói nền")).toBeInTheDocument();
    expect(within(await findCard(SEASON_PLAN.name)).getByText("Đang dùng")).toBeInTheDocument();
  });

  it("changes the plan from the server's quote once the terms are accepted, then lists the invoice", async () => {
    const changes: ChangeCall[] = [];
    let invoices: unknown[] = [];
    server.use(
      // Handlers listed first answer first, so the overrides go before the defaults.
      http.get(apiUrl("/saas-invoices"), () => HttpResponse.json({ data: invoices })),
      ...subscriptionHandlers(),
      http.post(apiUrl("/subscription:preview-change"), () => HttpResponse.json(SEASON_QUOTE)),
      http.post(apiUrl("/subscription:change"), async ({ request }) => {
        changes.push({ body: await request.json(), key: request.headers.get("Idempotency-Key") });
        invoices = [testInvoice()];
        return HttpResponse.json(testInvoice(), { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: `Đổi sang gói này: ${SEASON_PLAN.name}` }));
    const dialog = await screen.findByRole("dialog", { name: `Đổi sang gói ${SEASON_PLAN.name}` });
    expect(await within(dialog).findByText("850.000 ₫")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Trừ 140.000 ₫ còn lại của gói Cửa hàng.");
    expect(within(dialog).getByRole("button", { name: PLAN_CHANGE_SUBMIT })).toBeDisabled();
    await user.click(within(dialog).getByRole("checkbox", { name: ACCEPT_TERMS_LABEL }));
    await user.click(within(dialog).getByRole("button", { name: PLAN_CHANGE_SUBMIT }));

    expect(await screen.findByText(/Đã tạo hóa đơn/)).toHaveTextContent("Đã tạo hóa đơn HD-2026-0007. Thanh toán để đổi gói.");
    expect(changes.map((c) => c.body)).toEqual([{ plan_id: SEASON_PLAN.id, accept_terms: true }]);
    expect(changes[0]?.key).toMatch(/\S/);
    expect(await screen.findByRole("button", { name: "Thanh toán HD-2026-0007" })).toBeInTheDocument();
  });

  it("asks for the password when the API wants it, then resends the change with the same key", async () => {
    const changes: ChangeCall[] = [];
    server.use(
      ...subscriptionHandlers(),
      http.post(apiUrl("/subscription:preview-change"), () => HttpResponse.json(SEASON_QUOTE)),
      http.post(apiUrl("/subscription:change"), async ({ request }) => {
        changes.push({ body: await request.json(), key: request.headers.get("Idempotency-Key") });
        if (changes.length === 1) return apiError(403, "reauth_required");
        return HttpResponse.json(testInvoice(), { status: 201 });
      }),
      http.post(apiUrl(PATHS.reauth), () => HttpResponse.json({ reauthenticated_until: "2026-10-12T09:05:00Z" })),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: `Đổi sang gói này: ${SEASON_PLAN.name}` }));
    const dialog = await screen.findByRole("dialog", { name: `Đổi sang gói ${SEASON_PLAN.name}` });
    await user.click(await within(dialog).findByRole("checkbox", { name: ACCEPT_TERMS_LABEL }));
    await user.click(within(dialog).getByRole("button", { name: PLAN_CHANGE_SUBMIT }));

    const reauth = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    await user.type(within(reauth).getByLabelText("Mật khẩu"), "mat-khau");
    await user.click(within(reauth).getByRole("button", { name: REAUTH_SUBMIT }));

    expect(await screen.findByText(/Đã tạo hóa đơn/)).toHaveTextContent("Đã tạo hóa đơn HD-2026-0007. Thanh toán để đổi gói.");
    expect(changes).toHaveLength(2);
    expect(changes[1]?.body).toEqual(changes[0]?.body);
    expect(changes[0]?.key).toMatch(/\S/);
    expect(changes[1]?.key).toBe(changes[0]?.key);
  });

  it("buys an add-on from its quote; a free one is in force at once, with its period", async () => {
    const changes: ChangeCall[] = [];
    const previews: unknown[] = [];
    let invoices: unknown[] = [];
    let view = testSubscriptionView();
    server.use(
      http.get(apiUrl("/saas-invoices"), () => HttpResponse.json({ data: invoices })),
      http.get(apiUrl(PATHS.subscription), () => HttpResponse.json(view)),
      ...subscriptionHandlers(),
      http.post(apiUrl("/subscription:preview-change"), async ({ request }) => {
        previews.push(await request.json());
        return HttpResponse.json(ADDON_QUOTE);
      }),
      http.post(apiUrl("/subscription:change"), async ({ request }) => {
        changes.push({ body: await request.json(), key: request.headers.get("Idempotency-Key") });
        invoices = [addonInvoice()];
        view = testSubscriptionView({ addons: [testActiveAddon()] });
        return HttpResponse.json(addonInvoice(), { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const addons = await screen.findByRole("region", { name: ADDONS_TITLE });
    await user.click(await within(addons).findByRole("button", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` }));
    const dialog = await screen.findByRole("dialog", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` });
    expect(await within(dialog).findByText("Gói bổ sung")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("12/10/2026 – 11/11/2026");
    expect(dialog).toHaveTextContent("Add-on dùng theo kỳ của thuê bao.");
    expect(within(dialog).getByRole("button", { name: ADDON_BUY_SUBMIT })).toBeDisabled();
    await user.click(within(dialog).getByRole("checkbox", { name: ACCEPT_TERMS_LABEL }));
    await user.click(within(dialog).getByRole("button", { name: ADDON_BUY_SUBMIT }));

    expect(await screen.findByText(/Đã bật add-on/)).toHaveTextContent("Đã bật add-on. Hóa đơn HD-2026-0008 miễn phí nên đã được thanh toán.");
    expect(previews).toEqual([{ plan_id: ADDON_PLAN.id }]);
    expect(changes.map((c) => c.body)).toEqual([{ plan_id: ADDON_PLAN.id, accept_terms: true }]);
    expect(changes[0]?.key).toMatch(/\S/);
    const card = await findCard(ADDON_PLAN.name);
    await waitFor(() => expect(within(card).getByText("Đang dùng")).toBeInTheDocument());
    expect(card).toHaveTextContent("Hiệu lực 12/10/2026 – 11/11/2026");
    expect(within(card).queryByRole("button", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` })).not.toBeInTheDocument();
  });

  it("asks to pay an add-on whose invoice is not free", async () => {
    server.use(
      ...subscriptionHandlers(),
      http.post(apiUrl("/subscription:preview-change"), () => HttpResponse.json({ ...ADDON_QUOTE, amount: "49000" })),
      http.post(apiUrl("/subscription:change"), () => HttpResponse.json(addonInvoice({ amount: "49000", status: "open", paid_at: null }), { status: 201 })),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` }));
    const dialog = await screen.findByRole("dialog", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` });
    await user.click(await within(dialog).findByRole("checkbox", { name: ACCEPT_TERMS_LABEL }));
    await user.click(within(dialog).getByRole("button", { name: ADDON_BUY_SUBMIT }));

    expect(await screen.findByText(/Đã tạo hóa đơn/)).toHaveTextContent("Đã tạo hóa đơn HD-2026-0008. Thanh toán để bật add-on.");
  });

  it("marks the add-ons the subscription lists as in force, one no longer sold too, and offers the others", async () => {
    const other = { ...ADDON_PLAN, id: "plan-crm", code: "crm_addon", name: "Chăm sóc khách" };
    server.use(
      http.get(apiUrl("/plans"), () => HttpResponse.json({ data: [STORE_PLAN, ADDON_PLAN, other] })),
      ...subscriptionHandlers({
        view: testSubscriptionView({
          addons: [testActiveAddon(), testActiveAddon({ plan_id: "plan-old", plan_name: "Add-on cũ", modules: ["campaign"], period_start: "2026-10-01", period_end: "2026-10-31" })],
        }),
        // A paid invoice of an add-on the subscription does not list leaves that add-on to buy.
        invoices: [addonInvoice({ id: "inv-3", number: "HD-2026-0003", plan_id: other.id })],
      }),
    );
    renderApp(PATH);

    const card = await findCard(ADDON_PLAN.name);
    expect(await within(card).findByText("Đang dùng")).toBeInTheDocument();
    expect(card).toHaveTextContent("Hiệu lực 12/10/2026 – 11/11/2026");
    expect(within(card).queryByRole("button", { name: `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}` })).not.toBeInTheDocument();

    const retired = await findCard("Add-on cũ");
    expect(within(retired).getByText("Đang dùng")).toBeInTheDocument();
    expect(retired).toHaveTextContent("Hiệu lực 01/10/2026 – 31/10/2026");
    expect(within(retired).queryByRole("button")).not.toBeInTheDocument();

    const offered = await findCard(other.name);
    expect(within(offered).queryByText("Đang dùng")).not.toBeInTheDocument();
    expect(within(offered).getByRole("button", { name: `${ADDON_BUY_SUBMIT} ${other.name}` })).toBeInTheDocument();
  });

  it("reopening the same plan after a lost answer resends under the same key; another plan gets a new one", async () => {
    const changes: ChangeCall[] = [];
    server.use(
      ...subscriptionHandlers(),
      http.post(apiUrl("/subscription:preview-change"), async ({ request }) => {
        const { plan_id } = (await request.json()) as { plan_id: string };
        return HttpResponse.json(plan_id === ADDON_PLAN.id ? ADDON_QUOTE : SEASON_QUOTE);
      }),
      http.post(apiUrl("/subscription:change"), async ({ request }) => {
        changes.push({ body: await request.json(), key: request.headers.get("Idempotency-Key") });
        return HttpResponse.error();
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const submit = async (open: string, title: string, label: string) => {
      await user.click(await screen.findByRole("button", { name: open }));
      const dialog = await screen.findByRole("dialog", { name: title });
      await user.click(await within(dialog).findByRole("checkbox", { name: ACCEPT_TERMS_LABEL }));
      const sent = changes.length;
      await user.click(within(dialog).getByRole("button", { name: label }));
      await waitFor(() => expect(changes).toHaveLength(sent + 1));
      await waitFor(() => expect(within(dialog).getByRole("button", { name: label })).toBeEnabled());
      await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
      await waitFor(() => expect(screen.queryByRole("dialog", { name: title })).not.toBeInTheDocument());
    };
    const season = [`Đổi sang gói này: ${SEASON_PLAN.name}`, `Đổi sang gói ${SEASON_PLAN.name}`, PLAN_CHANGE_SUBMIT] as const;
    await submit(...season);
    await submit(...season);
    await submit(`${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}`, `${ADDON_BUY_SUBMIT} ${ADDON_PLAN.name}`, ADDON_BUY_SUBMIT);

    expect(changes[0]?.key).toMatch(/\S/);
    expect(changes[1]?.key).toBe(changes[0]?.key);
    expect(changes[2]?.key).toMatch(/\S/);
    expect(changes[2]?.key).not.toBe(changes[0]?.key);
  });

  it("shows how to pay an open invoice", async () => {
    server.use(
      ...subscriptionHandlers({ invoices: [testInvoice(), testInvoice({ id: "inv-0", number: "HD-2026-0006", status: "paid", paid_at: "2026-10-01T02:00:00Z" })] }),
      http.post(apiUrl("/saas-invoices/inv-1:pay"), () =>
        HttpResponse.json({ invoice: testInvoice(), provider: "manual", instructions: "Chuyển khoản tới STK 0123456789.\nNội dung: HD-2026-0007" }),
      ),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    expect(await screen.findByText("Đã thanh toán")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Thanh toán HD-2026-0006" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Thanh toán HD-2026-0007" }));

    const dialog = await screen.findByRole("dialog", { name: "Thanh toán hóa đơn HD-2026-0007" });
    expect(dialog).toHaveTextContent("Chuyển khoản tới STK 0123456789.");
    expect(dialog).toHaveTextContent("850.000 ₫");
  });

  it("says the subscription could not be read instead of showing an empty card", async () => {
    server.use(http.get(apiUrl(PATHS.subscription), () => apiError(500, "internal")), ...subscriptionHandlers());
    renderApp(PATH);

    // The subscription read is retried once after a second before it is reported as failed.
    await waitFor(() => expect(screen.getAllByRole("alert").some((a) => a.textContent?.startsWith("Không tải được thuê bao:"))).toBe(true), { timeout: 3000 });
    expect(await findCard("Thuê bao hiện tại")).toHaveTextContent("Không tải được thuê bao.");
  });

  it("grants support read access on a ticket after the password the API asks for, then revokes it", async () => {
    const grants: unknown[] = [];
    let sessions: unknown[] = [];
    let revoked = 0;
    server.use(
      http.get(apiUrl("/support-sessions"), () => HttpResponse.json({ data: sessions })),
      ...subscriptionHandlers(),
      http.post(apiUrl("/support-sessions"), async ({ request }) => {
        grants.push(await request.json());
        if (grants.length === 1) return apiError(403, "reauth_required");
        sessions = [testSupportSession()];
        return HttpResponse.json(testSupportSession(), { status: 201 });
      }),
      http.post(apiUrl(PATHS.reauth), () => HttpResponse.json({ reauthenticated_until: "2026-10-12T09:05:00Z" })),
      http.post(apiUrl("/support-sessions/sup-1:revoke"), () => {
        revoked += 1;
        const ended = testSupportSession({ state: "revoked", revoked_at: "2026-10-12T02:30:00Z" });
        sessions = [ended];
        return HttpResponse.json(ended);
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const grant = await screen.findByRole("button", { name: SUPPORT_GRANT_LABEL });
    await waitFor(() => expect(grant).toBeEnabled());
    await user.click(grant);
    const dialog = await screen.findByRole("dialog", { name: SUPPORT_GRANT_LABEL });
    expect(within(dialog).getByRole("combobox", { name: "Yêu cầu hỗ trợ" })).toHaveValue("tk-1");
    await user.click(within(dialog).getByRole("button", { name: "Cấp quyền" }));

    const reauth = await screen.findByRole("dialog", { name: REAUTH_TITLE });
    await user.type(within(reauth).getByLabelText("Mật khẩu"), "mat-khau");
    await user.click(within(reauth).getByRole("button", { name: REAUTH_SUBMIT }));

    expect(await screen.findByText(SUPPORT_GRANTED)).toBeInTheDocument();
    expect(grants).toEqual([
      { ticket_id: "tk-1", modules: [...SUPPORT_VIEW_MODULES], hours: 2 },
      { ticket_id: "tk-1", modules: [...SUPPORT_VIEW_MODULES], hours: 2 },
    ]);
    expect(await screen.findByText(/Phiên hỗ trợ HT-0012 đang mở đến/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Thu hồi" }));
    expect(await screen.findByText(SUPPORT_REVOKED)).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText(/Phiên hỗ trợ HT-0012 đang mở đến/)).not.toBeInTheDocument());
    expect(revoked).toBe(1);
  });

  it("keeps the grant off without an open, assigned ticket", async () => {
    server.use(...subscriptionHandlers({ tickets: [testTicket({ assigned_admin_id: null }), testTicket({ id: "tk-2", number: "HT-0013", status: "resolved" })] }));
    renderApp(PATH);

    expect(await screen.findByText(NO_GRANTABLE_TICKET)).toBeInTheDocument();
    const grant = screen.getByRole("button", { name: SUPPORT_GRANT_LABEL });
    expect(grant).toBeDisabled();
    expect(grant).toHaveAccessibleDescription(NO_GRANTABLE_TICKET);
  });

  it("groups the scope checkboxes under their own label and names an empty scope on the group", async () => {
    const grants: unknown[] = [];
    server.use(
      ...subscriptionHandlers(),
      http.post(apiUrl("/support-sessions"), async ({ request }) => {
        grants.push(await request.json());
        return HttpResponse.json(testSupportSession(), { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const grant = await screen.findByRole("button", { name: SUPPORT_GRANT_LABEL });
    await waitFor(() => expect(grant).toBeEnabled());
    await user.click(grant);
    const dialog = await screen.findByRole("dialog", { name: SUPPORT_GRANT_LABEL });
    const scope = within(dialog).getByRole("group", { name: "Phạm vi xem" });
    const boxes = within(scope).getAllByRole("checkbox");
    expect(boxes).toHaveLength(SUPPORT_VIEW_MODULES.length);
    // No checkbox label nested inside another label.
    expect(dialog.querySelector("label label")).toBeNull();

    for (const box of boxes) if ((box as HTMLInputElement).checked) await user.click(box);
    await user.click(within(dialog).getByRole("button", { name: "Cấp quyền" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Chọn ít nhất một phần được xem.");
    expect(scope).toHaveAccessibleDescription("Chọn ít nhất một phần được xem.");
    expect(grants).toEqual([]);
  });

  it("warns about a possible password in a new ticket and sends it when the owner insists", async () => {
    const posts: Record<string, unknown>[] = [];
    server.use(
      ...subscriptionHandlers(),
      http.post(apiUrl("/support-tickets"), async ({ request }) => {
        posts.push((await request.json()) as Record<string, unknown>);
        if (posts.length === 1) return apiError(422, "possible_password");
        return HttpResponse.json({ ...testTicketView({ id: "tk-3", number: "HT-0014", status: "open", messages: [] }) }, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: NEW_TICKET_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: NEW_TICKET_LABEL });
    await user.type(within(dialog).getByRole("textbox", { name: "Tiêu đề" }), "Không đăng nhập được máy 2");
    await user.type(within(dialog).getByRole("textbox", { name: "Nội dung" }), "Mã máy là 482913");
    await user.click(within(dialog).getByRole("button", { name: SEND_TICKET_LABEL }));

    expect(await within(dialog).findByText(POSSIBLE_PASSWORD_WARNING)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: SEND_ANYWAY_LABEL }));

    expect(await screen.findByText(/Đã gửi yêu cầu hỗ trợ\. Số yêu cầu/)).toHaveTextContent("HT-0014");
    expect(posts.map((p) => p.acknowledge_warning)).toEqual([false, true]);
    expect(posts[1]).toMatchObject({ subject: "Không đăng nhập được máy 2", body: "Mã máy là 482913", priority: "normal" });
  });

  it("drops the possible-password warning once the text is edited, and calls a refused ticket not sent", async () => {
    const posts: Record<string, unknown>[] = [];
    server.use(
      ...subscriptionHandlers(),
      http.post(apiUrl("/support-tickets"), async ({ request }) => {
        posts.push((await request.json()) as Record<string, unknown>);
        return apiError(422, "possible_password");
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: NEW_TICKET_LABEL }));
    const dialog = await screen.findByRole("dialog", { name: NEW_TICKET_LABEL });
    await user.type(within(dialog).getByRole("textbox", { name: "Tiêu đề" }), "Máy 2");
    const body = within(dialog).getByRole("textbox", { name: "Nội dung" });
    await user.type(body, "Mã 482913");
    await user.click(within(dialog).getByRole("button", { name: SEND_TICKET_LABEL }));

    expect(await within(dialog).findByText(POSSIBLE_PASSWORD_WARNING)).toBeInTheDocument();
    const toast = await screen.findByText(NOT_SENT);
    expect(toast.tagName).toBe("B");
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();

    await user.clear(body);
    await user.type(body, "Máy báo lỗi");
    expect(within(dialog).queryByText(POSSIBLE_PASSWORD_WARNING)).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: SEND_ANYWAY_LABEL })).not.toBeInTheDocument();
    expect(posts).toHaveLength(1);
  });

  it("shows the conversation of a ticket and names a reply refused because the ticket closed", async () => {
    server.use(
      ...subscriptionHandlers(),
      http.get(apiUrl("/support-tickets/tk-1"), () => HttpResponse.json(testTicketView())),
      http.post(apiUrl("/support-tickets/tk-1/messages"), () => apiError(409, "ticket_closed")),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: "HT-0012" }));
    const dialog = await screen.findByRole("dialog", { name: "HT-0012 · Không in được hóa đơn" });
    const messages = await within(dialog).findByRole("list", { name: "Trao đổi" });
    expect(messages).toHaveTextContent("Anh gửi giúp tên máy in.");
    await user.type(within(dialog).getByRole("textbox", { name: "Trả lời" }), "Máy in Xprinter XP-58");
    await user.click(within(dialog).getByRole("button", { name: SEND_REPLY_LABEL }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent(NOT_SENT);
    expect(toast).toHaveTextContent("Yêu cầu hỗ trợ đã đóng.");
  });

  it("offers no reply on a finished ticket", async () => {
    server.use(
      ...subscriptionHandlers({ tickets: [testTicket({ status: "closed" })] }),
      http.get(apiUrl("/support-tickets/tk-1"), () => HttpResponse.json(testTicketView({ status: "closed" }))),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(await screen.findByRole("button", { name: "HT-0012" }));
    const dialog = await screen.findByRole("dialog", { name: "HT-0012 · Không in được hóa đơn" });
    expect(await within(dialog).findByText("Yêu cầu đã kết thúc. Tạo yêu cầu mới nếu cần hỗ trợ tiếp.")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: SEND_REPLY_LABEL })).not.toBeInTheDocument();
  });
});
