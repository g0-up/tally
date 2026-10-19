import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearDocOpeners, registerDocOpener } from "../../../shared/domain/doc-no";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { CUSTOMER_ID, GROUPS, RETAIL_GROUP_ID, testConsent, testCustomer, testListedConsent, testListedCustomer, testReorderDraft } from "../testing/fixtures";

const ROUTE = "/campaigns/customers";

const minhAn = testListedCustomer({ last_purchase_at: "2026-10-08T03:00:00Z", consents: [testListedConsent(), testListedConsent({ channel: "sms", purpose: "debt_reminder" })] });
const coBa = testListedCustomer({
  id: "cus-ba",
  kind: "person",
  name: "Cô Ba Bình",
  phone: null,
  address: "Chợ Bến Thành",
  customer_group_id: RETAIL_GROUP_ID,
  customer_group_name: "Khách lẻ",
  credit_limit: null,
  payment_terms_days: 0,
  version: 1,
  consents: [testListedConsent({ granted: false })],
});
const phuc = testListedCustomer({ id: "cus-phuc", name: "Đại lý Phúc", phone: "0281234567", address: null, status: "inactive", version: 2 });

/** Cell texts of a row, money with a plain space before ₫. */
const cells = (row: HTMLElement) => within(row).getAllByRole("cell").map((c) => (c.textContent ?? "").replace(/\s/g, " "));

async function row(name: string): Promise<HTMLElement> {
  const el = (await screen.findByText(name)).closest<HTMLElement>("tr");
  if (el === null) throw new Error(`No row for ${name}`);
  return el;
}

/** A page of the revenue report for the named customers; walk-in sales carry no id. */
function revenuePage(rows: Record<string, string | null>[], nextCursor: string | null = null) {
  return HttpResponse.json({ report_key: "revenue", title: "Doanh thu", summary: [], rows, next_cursor: nextCursor });
}

const SEASON_REVENUE: Readonly<Record<string, string>> = { [CUSTOMER_ID]: "4500000", "cus-ba": "1200000" };

function customerHandlers({ customers = [minhAn, coBa, phuc] } = {}) {
  const listQueries: URLSearchParams[] = [];
  const revenueQueries: URLSearchParams[] = [];
  const rowReads: string[] = [];
  const handlers = [
    http.get(apiUrl("/customers"), ({ request }) => {
      listQueries.push(new URL(request.url).searchParams);
      return HttpResponse.json({ data: customers, next_cursor: null });
    }),
    http.get(apiUrl("/reports/revenue"), ({ request }) => {
      const query = new URL(request.url).searchParams;
      revenueQueries.push(query);
      const ids = (query.get("customer_ids") ?? "").split(",");
      return revenuePage(ids.flatMap((id) => (SEASON_REVENUE[id] ? [{ id, code: null, name: id, net_revenue: SEASON_REVENUE[id] }] : [])));
    }),
    http.get(apiUrl("/customers/:id/timeline"), ({ params }) => {
      rowReads.push(`timeline ${String(params.id)}`);
      return HttpResponse.json({ data: [], next_cursor: null });
    }),
    http.get(apiUrl("/customers/:id/consents"), ({ params }) => {
      rowReads.push(`consents ${String(params.id)}`);
      return HttpResponse.json({ current: params.id === CUSTOMER_ID ? [testConsent()] : [], history: [] });
    }),
    http.get(apiUrl("/customer-groups"), () => HttpResponse.json({ data: GROUPS })),
    http.get(apiUrl("/price-lists"), () => HttpResponse.json({ data: [], next_cursor: null })),
  ];
  return { handlers, listQueries, revenueQueries, rowReads };
}

afterEach(() => clearDocOpeners());

describe("CustomersPage", () => {
  it("lists customers with season revenue, last purchase and consent from the list and one revenue read", async () => {
    const { handlers, listQueries, revenueQueries, rowReads } = customerHandlers();
    server.use(...handlers);
    renderApp(ROUTE);

    expect(await screen.findByText("Lịch sử mua, người phụ trách, đồng ý nhận tin. Tái đặt đơn lấy giá và quy cách hiện hành, cần xác nhận lại, không sao chép mù đơn cũ.")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Tìm tên, điện thoại, mã số thuế…")).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Khách hàng" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Khách", "Nhóm", "Doanh thu từ 01/10", "Lần mua gần nhất", "Đồng ý nhận tin", "Thao tác"]);

    const an = await row("Tạp hóa Minh An");
    await waitFor(() => expect(cells(an).slice(0, 5)).toEqual(["Tạp hóa Minh An0903123456 · 12 Lê Lợi, Q.1", "Khách sỉ", "4.500.000 ₫", "08/10/2026", "•Đã đồng ý"]));
    expect(within(an).getByRole("button", { name: "Tái đặt đơn Tạp hóa Minh An" })).toBeInTheDocument();

    const ba = await row("Cô Ba Bình");
    expect(cells(ba).slice(0, 5)).toEqual(["Cô Ba BìnhChợ Bến Thành", "Khách lẻ", "1.200.000 ₫", "—", "•Chưa đồng ý"]);
    expect(within(ba).getByRole("button", { name: "Tạo đơn Cô Ba Bình" })).toBeInTheDocument();

    const inactive = await row("Đại lý Phúc");
    expect(cells(inactive)[2]).toBe("0 ₫");
    expect(cells(inactive)[4]).toBe("•Chưa đồng ý");
    expect(within(inactive).queryByRole("button", { name: /Tạo đơn|Tái đặt đơn/ })).not.toBeInTheDocument();
    expect(within(inactive).getByRole("button", { name: "Sửa Đại lý Phúc" })).toBeInTheDocument();

    expect(listQueries[0]?.get("limit")).toBe("50");
    expect(revenueQueries.map((q) => [q.get("from"), q.get("to"), q.get("group_by"), q.get("customer_ids"), q.get("limit"), q.get("cursor")])).toEqual([
      ["2026-10-01", "2026-10-12", "customer", `${CUSTOMER_ID},cus-ba,cus-phuc`, "200", null],
    ]);
    expect(rowReads).toEqual([]);
  });

  it("leaves the revenue column at — when the report is refused, without a note", async () => {
    server.use(http.get(apiUrl("/reports/revenue"), () => apiError(403, "forbidden")), ...customerHandlers().handlers);
    renderApp(ROUTE);

    const an = await row("Tạp hóa Minh An");
    expect(cells(an)[4]).toBe("•Đã đồng ý");
    await waitFor(() => expect(cells(an)[2]).toBe("—"));
    expect(screen.queryByText(/cột doanh thu hiện/)).not.toBeInTheDocument();
  });

  it.each([
    ["too_many", "Mỗi lần chỉ đọc được doanh thu của tối đa 100 khách nên cột doanh thu hiện “—”."],
    ["invalid", "Danh sách khách để đọc doanh thu không hợp lệ nên cột doanh thu hiện “—”. Tải lại trang rồi thử lại."],
  ])("says why the revenue column reads — when the report refuses the customers it names (%s)", async (reason, message) => {
    server.use(http.get(apiUrl("/reports/revenue"), () => apiError(422, "validation_failed", { fields: { customer_ids: reason } })), ...customerHandlers().handlers);
    renderApp(ROUTE);

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(cells(await row("Tạp hóa Minh An"))[2]).toBe("—");
  });

  it("reads the revenue of each list page once, for the customers of that page alone", async () => {
    const first = Array.from({ length: 50 }, (_, i) => testListedCustomer({ id: `cus-${i}`, name: `Khách số ${i + 1}`, version: 1 }));
    const next = testListedCustomer({ id: "cus-next", name: "Khách trang sau", version: 1 });
    const revenueIds: string[][] = [];
    server.use(
      http.get(apiUrl("/customers"), ({ request }) =>
        new URL(request.url).searchParams.get("cursor") === "c2" ? HttpResponse.json({ data: [next], next_cursor: null }) : HttpResponse.json({ data: first, next_cursor: "c2" }),
      ),
      http.get(apiUrl("/reports/revenue"), ({ request }) => {
        const ids = (new URL(request.url).searchParams.get("customer_ids") ?? "").split(",");
        revenueIds.push(ids);
        return revenuePage(ids.includes("cus-next") ? [{ id: "cus-next", code: null, name: "Khách trang sau", net_revenue: "300000" }] : []);
      }),
      ...customerHandlers().handlers,
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await waitFor(() => expect(cells(screen.getByText("Khách số 50").closest("tr") as HTMLElement)[2]).toBe("0 ₫"));
    await user.click(screen.getByRole("button", { name: /^Xem thêm/ }));
    await waitFor(() => expect(cells(screen.getByText("Khách trang sau").closest("tr") as HTMLElement)[2]).toBe("300.000 ₫"));
    expect(revenueIds).toEqual([first.map((c) => c.id), ["cus-next"]]);
  });

  it("re-reads the list after a save, which carries the saved consent, without a read per row", async () => {
    const { handlers, listQueries, rowReads } = customerHandlers();
    let listed: unknown[] = [minhAn, coBa, phuc];
    server.use(
      http.get(apiUrl("/customers"), ({ request }) => {
        listQueries.push(new URL(request.url).searchParams);
        return HttpResponse.json({ data: listed, next_cursor: null });
      }),
      http.patch(apiUrl("/customers/cus-ba"), () => HttpResponse.json({ ...testCustomer({ id: "cus-ba", name: "Cô Ba Bình", version: 2 }), duplicate_warnings: [] })),
      http.post(apiUrl("/customers/cus-ba/consents"), () => {
        listed = [minhAn, { ...coBa, consents: [testListedConsent()] }, phuc];
        return HttpResponse.json(testConsent({ party_id: "cus-ba" }), { status: 201 });
      }),
      ...handlers,
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const ba = await row("Cô Ba Bình");
    expect(cells(ba)[4]).toBe("•Chưa đồng ý");
    const before = listQueries.length;

    await user.click(within(ba).getByRole("button", { name: "Sửa Cô Ba Bình" }));
    const dialog = await screen.findByRole("dialog", { name: "Sửa khách hàng · Cô Ba Bình" });
    await user.click(await within(dialog).findByRole("checkbox", { name: "Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ" }));
    await user.type(within(dialog).getByRole("textbox", { name: /^Nguồn đồng ý/ }), "tại quầy");
    await user.click(within(dialog).getByRole("button", { name: "Lưu" }));

    expect(await screen.findByText("Đã lưu khách hàng.")).toBeInTheDocument();
    await waitFor(() => expect(listQueries.length).toBeGreaterThan(before));
    await waitFor(() => expect(cells(screen.getByText("Cô Ba Bình").closest("tr") as HTMLElement)[4]).toBe("•Đã đồng ý"));
    // The dialog reads the consents of the customer it edits; the list itself reads no row.
    expect(rowReads.filter((r) => !r.endsWith("cus-ba"))).toEqual([]);
    expect(rowReads.some((r) => r.startsWith("timeline"))).toBe(false);
  });

  it("searches by the typed words", async () => {
    const { handlers, listQueries } = customerHandlers();
    server.use(...handlers);
    const user = userEvent.setup();
    renderApp(ROUTE);

    await row("Tạp hóa Minh An");
    await user.type(screen.getByPlaceholderText("Tìm tên, điện thoại, mã số thuế…"), "Minh");
    await waitFor(() => expect(listQueries.at(-1)?.get("q")).toBe("Minh"));
  });

  it("re-orders the latest purchase without a body, under an Idempotency-Key, and opens the draft invoice", async () => {
    const posts: { body: string; key: string | null }[] = [];
    const opened = vi.fn();
    registerDocOpener("SO", opened);
    server.use(
      http.post(apiUrl(`/customers/${CUSTOMER_ID}/reorder-drafts`), async ({ request }) => {
        posts.push({ body: await request.text(), key: request.headers.get("Idempotency-Key") });
        return HttpResponse.json(testReorderDraft(), { status: 201 });
      }),
      ...customerHandlers().handlers,
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(await within(await row("Tạp hóa Minh An")).findByRole("button", { name: "Tái đặt đơn Tạp hóa Minh An" }));
    expect(await screen.findByText(/^Đã nạp dòng hàng từ/)).toHaveTextContent(
      "Đã nạp dòng hàng từ SO-0042 vào SO-0077 với giá hiện hành. Kiểm tra lại trước khi xác nhận. 1 dòng không chép được (ngừng bán hoặc cần chọn lại).",
    );
    expect(posts).toEqual([{ body: "", key: expect.stringMatching(/.+/) }]);
    await waitFor(() => expect(opened).toHaveBeenCalledWith("SO-0077"));
  });

  it("shows the running re-order on its own row and leaves the other rows' buttons usable", async () => {
    let answer: () => void = () => undefined;
    registerDocOpener("SO", vi.fn());
    server.use(
      http.post(apiUrl(`/customers/${CUSTOMER_ID}/reorder-drafts`), async () => {
        await new Promise<void>((resolve) => (answer = resolve));
        return HttpResponse.json(testReorderDraft(), { status: 201 });
      }),
      ...customerHandlers().handlers,
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const reorder = await within(await row("Tạp hóa Minh An")).findByRole("button", { name: "Tái đặt đơn Tạp hóa Minh An" });
    await user.click(reorder);
    await waitFor(() => expect(reorder).toHaveAttribute("aria-busy", "true"));
    expect(reorder).toHaveTextContent("Đang tạo đơn…");
    expect(within(await row("Cô Ba Bình")).getByRole("button", { name: "Tạo đơn Cô Ba Bình" })).toBeEnabled();
    expect(within(await row("Cô Ba Bình")).getByRole("button", { name: "Sửa Cô Ba Bình" })).not.toHaveAttribute("aria-busy");
    expect(within(await row("Tạp hóa Minh An")).getByRole("button", { name: "Sửa Tạp hóa Minh An" })).toBeEnabled();

    answer();
    await waitFor(() => expect(reorder).not.toHaveAttribute("aria-busy"));
    expect(reorder).toHaveTextContent("Tái đặt đơn");
  });

  it("sends a re-order again under the same key when its answer was lost", async () => {
    const keys: (string | null)[] = [];
    const opened = vi.fn();
    registerDocOpener("SO", opened);
    server.use(
      http.post(apiUrl(`/customers/${CUSTOMER_ID}/reorder-drafts`), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        if (keys.length === 1) return HttpResponse.error();
        return HttpResponse.json(testReorderDraft(), { status: 201 });
      }),
      ...customerHandlers().handlers,
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const reorder = await within(await row("Tạp hóa Minh An")).findByRole("button", { name: "Tái đặt đơn Tạp hóa Minh An" });
    await user.click(reorder);
    await waitFor(() => expect(keys).toHaveLength(1));
    await waitFor(() => expect(reorder).toBeEnabled());
    await user.click(reorder);
    await waitFor(() => expect(opened).toHaveBeenCalledWith("SO-0077"));
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(/.+/);
    expect(keys[1]).toBe(keys[0]);
  });

  it("goes to the invoices when no screen opens the draft, without the skipped-lines note when all lines were taken", async () => {
    server.use(
      http.post(apiUrl(`/customers/${CUSTOMER_ID}/reorder-drafts`), () => HttpResponse.json(testReorderDraft({ lines: [{ sku_id: "sku-bq454", name: "Bánh quy bơ hộp 454g", qty: "10", copied: true, flag: null }] }), { status: 201 })),
      ...customerHandlers().handlers,
    );
    const user = userEvent.setup();
    const { router } = renderApp(ROUTE);

    await user.click(await within(await row("Tạp hóa Minh An")).findByRole("button", { name: "Tái đặt đơn Tạp hóa Minh An" }));
    expect(await screen.findByText(/^Đã nạp dòng hàng từ/)).toHaveTextContent("Đã nạp dòng hàng từ SO-0042 vào SO-0077 với giá hiện hành. Kiểm tra lại trước khi xác nhận.", { normalizeWhitespace: true });
    expect(screen.queryByText(/dòng không chép được/)).not.toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/sales/invoices"));
  });

  it("says why a re-order is refused", async () => {
    server.use(http.post(apiUrl(`/customers/${CUSTOMER_ID}/reorder-drafts`), () => apiError(409, "nothing_to_reorder")), ...customerHandlers().handlers);
    const user = userEvent.setup();
    const { router } = renderApp(ROUTE);

    await user.click(await within(await row("Tạp hóa Minh An")).findByRole("button", { name: "Tái đặt đơn Tạp hóa Minh An" }));
    expect(await screen.findByText("Không dòng nào của lần mua trước còn bán được. Dùng “Tạo đơn”.")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe(ROUTE);
  });

  it("starts a new invoice for a customer who has not bought yet", async () => {
    server.use(...customerHandlers().handlers);
    const user = userEvent.setup();
    const { router } = renderApp(ROUTE);

    await user.click(await within(await row("Cô Ba Bình")).findByRole("button", { name: "Tạo đơn Cô Ba Bình" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/sales/invoices"));
  });

  it("opens the customer dialog to add and to edit", async () => {
    server.use(...customerHandlers().handlers);
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(await screen.findByRole("button", { name: "Thêm khách hàng" }));
    expect(await screen.findByRole("dialog", { name: "Thêm khách hàng" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Hủy" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(within(await row("Tạp hóa Minh An")).getByRole("button", { name: "Sửa Tạp hóa Minh An" }));
    expect(await screen.findByRole("dialog", { name: "Sửa khách hàng · Tạp hóa Minh An" })).toBeInTheDocument();
  });

  it("says when no customer matches", async () => {
    server.use(...customerHandlers({ customers: [] }).handlers);
    renderApp(ROUTE);

    expect(await screen.findByText("Chưa có khách hàng phù hợp.")).toBeInTheDocument();
  });
});
