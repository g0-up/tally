import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { STALE_DATA_MESSAGE } from "../../../shared/api/api-error";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { at, CUSTOMER_ID, testStatementRow, testStatementVersion } from "../testing/fixtures";
import type { StatementRow } from "../schemas";

const draft = testStatementRow();
const sent = testStatementRow({ id: "stm-2", doc_no: "DS-0002", status: "sent", current_version: 2, sent_version: 2, version: 7 });

function statementHandlers(statements: StatementRow[] = [draft, sent], versionReads: string[] = []) {
  return [
    http.get(apiUrl("/statements"), () => HttpResponse.json({ data: statements, next_cursor: null })),
    http.get(apiUrl("/statements/:id"), ({ params }) => {
      const s = statements.find((x) => x.id === params.id);
      return s ? HttpResponse.json(s) : apiError(404, "not_found");
    }),
    http.get(apiUrl("/statements/:id/versions/:n"), ({ params }) => {
      const s = at(statements.filter((x) => x.id === params.id), 0);
      const n = Number(params.n);
      versionReads.push(`${s.id}/${n}`);
      return HttpResponse.json(testStatementVersion({ statement_id: s.id, doc_no: s.doc_no, version_no: n, status: n < s.current_version ? "superseded" : s.status, created_at: `2026-10-0${n}T02:00:00Z` }));
    }),
  ];
}

describe("StatementsPage", () => {
  it("lists statements with the customer and balances the list carries, reading no version", async () => {
    const versionReads: string[] = [];
    server.use(...statementHandlers(undefined, versionReads));
    renderApp("/debts/statements");

    expect(await screen.findByText("Bảng kê có kỳ, số dư đầu, phát sinh và số dư cuối. Bản đã gửi là bất biến; điều chỉnh tạo phiên bản mới có lịch sử.")).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Bảng kê đối soát" });
    const rows = within(table).getAllByRole("row");
    const first = at(rows, 1);
    const second = at(rows, 2);
    await waitFor(() => expect(first).toHaveTextContent("Tạp hóa Minh Châu"));
    expect(first).toHaveTextContent("v1 · lập 12/10/2026");
    expect(first).toHaveTextContent("01/09/2026 – 12/10/2026");
    expect(first).toHaveTextContent("2.000.000");
    expect(first).toHaveTextContent("4.500.000");
    expect(within(first).getByText("Nháp")).toBeInTheDocument();
    expect(within(first).getByRole("button", { name: "Gửi khách DS-0001" })).toBeInTheDocument();
    expect(within(second).getByText("Đã gửi")).toBeInTheDocument();
    expect(within(second).getByRole("button", { name: "Khách xác nhận DS-0002" })).toBeInTheDocument();
    expect(within(second).getByRole("button", { name: "Tranh chấp DS-0002" })).toBeInTheDocument();
    expect(versionReads).toEqual([]);
  });

  it("sends a draft with its version as If-Match", async () => {
    let ifMatch: string | null = null;
    let idempotencyKey: string | null = "unset";
    server.use(
      ...statementHandlers(),
      http.post(apiUrl("/statements/stm-1:send"), ({ request }) => {
        ifMatch = request.headers.get("If-Match");
        idempotencyKey = request.headers.get("Idempotency-Key");
        return HttpResponse.json({ ...draft, status: "sent", sent_version: 1, version: 4 });
      }),
    );
    const user = userEvent.setup();
    renderApp("/debts/statements?doc=DS-0001");

    const drawer = await screen.findByRole("dialog", { name: "Bảng kê DS-0001 · v1" });
    expect(await within(drawer).findByText("Tạp hóa Minh Châu · 01/09/2026 – 12/10/2026")).toBeInTheDocument();
    expect(within(drawer).getByText("Số liệu bảng kê được chụp tại lúc lập. Gửi qua cổng khách hàng bằng liên kết có thời hạn, thu hồi được.")).toBeInTheDocument();
    const lines = within(drawer).getByRole("table", { name: "Bảng kê DS-0001" });
    const rows = within(lines).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("Số dư đầu kỳ");
    expect(rows[2]).toHaveTextContent("SO-0001");
    expect(rows[2]).toHaveTextContent("Bán hàng");
    expect(rows[3]).toHaveTextContent("Thanh toán");
    expect(rows.at(-1)).toHaveTextContent("Số dư cuối kỳ");
    expect(rows.at(-1)).toHaveTextContent("4.500.000");
    expect(within(drawer).getByRole("link", { name: "In bảng kê (mở tab mới)" })).toHaveAttribute("href", "/print/statements/stm-1?version=1");

    await user.click(within(drawer).getByRole("button", { name: "Gửi khách" }));
    expect(await screen.findByText("Đã gửi bảng kê. Bản đã gửi không bị thay đổi.")).toBeInTheDocument();
    expect(ifMatch).toBe('"3"');
    expect(idempotencyKey).toBeNull();
  });

  it("records what the customer disputes", async () => {
    let body: unknown = null;
    let ifMatch: string | null = null;
    server.use(
      ...statementHandlers(),
      http.post(apiUrl("/statements/stm-2:dispute"), async ({ request }) => {
        body = await request.json();
        ifMatch = request.headers.get("If-Match");
        return HttpResponse.json({ ...sent, status: "disputed", dispute_note: "Thiếu phiếu thu PT-0003", version: 8 });
      }),
    );
    const user = userEvent.setup();
    renderApp("/debts/statements");

    const table = await screen.findByRole("table", { name: "Bảng kê đối soát" });
    await user.click(await within(table).findByRole("button", { name: "Tranh chấp DS-0002" }));
    const dialog = await screen.findByRole("dialog", { name: "Ghi nhận tranh chấp DS-0002" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi nhận" }));
    expect(await screen.findByText(/Nhập nội dung khách phản hồi\./)).toBeInTheDocument();
    const note = within(dialog).getByRole("textbox", { name: /^Nội dung khách phản hồi/ });
    expect(note).toHaveAttribute("aria-invalid", "true");
    expect(note).toHaveAccessibleDescription("Nhập nội dung khách phản hồi.");
    await waitFor(() => expect(note).toHaveFocus());
    await user.type(within(dialog).getByRole("textbox", { name: "Nội dung khách phản hồi" }), "Thiếu phiếu thu PT-0003");
    await user.click(within(dialog).getByRole("button", { name: "Ghi nhận" }));

    expect(await screen.findByText("Đã ghi nhận tranh chấp. Lập phiên bản mới sau khi điều chỉnh.")).toBeInTheDocument();
    expect(body).toEqual({ note: "Thiếu phiếu thu PT-0003" });
    expect(ifMatch).toBe('"7"');
  });

  it("marks the customer field when a statement is asked for no one, with nothing sent", async () => {
    let posted = false;
    server.use(
      ...statementHandlers(),
      http.post(apiUrl("/statements"), () => {
        posted = true;
        return HttpResponse.json({}, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp("/debts/statements");

    await user.click(await screen.findByRole("button", { name: "Lập bảng kê" }));
    const dialog = await screen.findByRole("dialog", { name: "Lập bảng kê đối soát" });
    await user.click(within(dialog).getByRole("button", { name: "Lập bảng kê" }));
    const party = within(dialog).getByRole("combobox", { name: "Khách hàng" });
    await waitFor(() => expect(party).toHaveAttribute("aria-invalid", "true"));
    expect(party).toHaveAccessibleDescription("Chọn khách hàng.");
    await waitFor(() => expect(party).toHaveFocus());
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(posted).toBe(false);
  });

  it("opens a new statement for the customer of ?party, last month to today", async () => {
    let body: unknown = null;
    const created = testStatementRow({ id: "stm-3", doc_no: "DS-0003", period_from: "2026-09-01" });
    let statements: StatementRow[] = [draft];
    server.use(
      http.get(apiUrl("/statements"), () => HttpResponse.json({ data: statements, next_cursor: null })),
      http.get(apiUrl("/statements/:id/versions/:n"), ({ params }) => HttpResponse.json(testStatementVersion({ statement_id: String(params.id) }))),
      http.get(apiUrl(`/customers/${CUSTOMER_ID}`), () => HttpResponse.json({ id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", customer_group_name: null, credit_limit: null })),
      http.post(apiUrl("/statements"), async ({ request }) => {
        body = await request.json();
        statements = [created, draft];
        return HttpResponse.json(created, { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(`/debts/statements?party=${CUSTOMER_ID}`);

    const dialog = await screen.findByRole("dialog", { name: "Lập bảng kê đối soát" });
    await user.click(within(dialog).getByRole("button", { name: "Lập bảng kê" }));
    expect(await screen.findByText("DS-0003", { selector: "b" })).toBeInTheDocument();
    expect(body).toEqual({ party_id: CUSTOMER_ID, direction: "receivable", period_from: "2026-09-01", period_to: "2026-10-12" });
    expect(await screen.findByRole("dialog", { name: "Bảng kê DS-0003 · v1" })).toBeInTheDocument();
  });

  it("opens the statement of ?id, the link a disputed-statement notification carries, past the first page", async () => {
    const pages: string[] = [];
    server.use(
      http.get(apiUrl("/statements"), ({ request }) => {
        pages.push(new URL(request.url).searchParams.get("cursor") ?? "");
        return HttpResponse.json({ data: [draft], next_cursor: "c2" });
      }),
      ...statementHandlers(),
    );
    renderApp("/debts/statements?id=stm-2");

    const drawer = await screen.findByRole("dialog", { name: "Bảng kê DS-0002 · v2" });
    expect(await within(drawer).findByText("Tạp hóa Minh Châu · 01/09/2026 – 12/10/2026")).toBeInTheDocument();
    expect(pages).toEqual([""]);
  });

  it("explains a statement of ?id that no longer exists", async () => {
    server.use(...statementHandlers());
    renderApp("/debts/statements?id=stm-gone");

    expect(await screen.findByText("Không tìm thấy bảng kê này, có thể đã bị xóa. Tải lại để xem danh sách mới nhất.")).toBeInTheDocument();
  });

  it("leaves a stale dispute to the app-wide notice: the dialog stays open and no commit toast", async () => {
    server.use(...statementHandlers(), http.post(apiUrl("/statements/stm-2:dispute"), () => apiError(412, "precondition_failed")));
    const user = userEvent.setup();
    renderApp("/debts/statements");

    const table = await screen.findByRole("table", { name: "Bảng kê đối soát" });
    await user.click(await within(table).findByRole("button", { name: "Tranh chấp DS-0002" }));
    const dialog = await screen.findByRole("dialog", { name: "Ghi nhận tranh chấp DS-0002" });
    await user.type(within(dialog).getByRole("textbox", { name: "Nội dung khách phản hồi" }), "Thiếu phiếu thu PT-0003");
    await user.click(within(dialog).getByRole("button", { name: "Ghi nhận" }));

    expect(await screen.findByText(STALE_DATA_MESSAGE, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Ghi nhận tranh chấp DS-0002" })).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
  });

  it("sends a new statement again under the same Idempotency-Key after a lost answer", async () => {
    const keys: (string | null)[] = [];
    server.use(
      ...statementHandlers([draft]),
      http.get(apiUrl(`/customers/${CUSTOMER_ID}`), () => HttpResponse.json({ id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", customer_group_name: null, credit_limit: null })),
      http.post(apiUrl("/statements"), ({ request }) => {
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json(testStatementRow({ id: "stm-3", doc_no: "DS-0003" }), { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(`/debts/statements?party=${CUSTOMER_ID}`);

    const dialog = await screen.findByRole("dialog", { name: "Lập bảng kê đối soát" });
    await user.click(within(dialog).getByRole("button", { name: "Lập bảng kê" }));
    await waitFor(() => expect(keys).toHaveLength(1));
    await user.click(await within(dialog).findByRole("button", { name: "Lập bảng kê" }));
    expect(await screen.findByText("DS-0003", { selector: "b" })).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatch(/.+/);
    expect(keys[1]).toBe(keys[0]);
  });

  it("checks a statement whose answer was lost for the period first sent, and says the edited period was not made", async () => {
    const bodies: unknown[] = [];
    const keys: (string | null)[] = [];
    server.use(
      ...statementHandlers([draft]),
      http.get(apiUrl(`/customers/${CUSTOMER_ID}`), () => HttpResponse.json({ id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", customer_group_name: null, credit_limit: null })),
      http.post(apiUrl("/statements"), async ({ request }) => {
        bodies.push(await request.json());
        keys.push(request.headers.get("Idempotency-Key"));
        return keys.length === 1 ? HttpResponse.error() : HttpResponse.json(testStatementRow({ id: "stm-3", doc_no: "DS-0003" }), { status: 201 });
      }),
    );
    const user = userEvent.setup();
    renderApp(`/debts/statements?party=${CUSTOMER_ID}`);

    const dialog = await screen.findByRole("dialog", { name: "Lập bảng kê đối soát" });
    await user.click(within(dialog).getByRole("button", { name: "Lập bảng kê" }));
    await waitFor(() => expect(keys).toHaveLength(1));
    await user.clear(within(dialog).getByLabelText("Đến ngày"));
    await user.type(within(dialog).getByLabelText("Đến ngày"), "18/10/2026");
    await user.click(within(dialog).getByRole("button", { name: "Lập bảng kê" }));

    expect(await screen.findByText(/theo kỳ của lần bấm trước, lần đó chưa nhận được trả lời\. Kỳ vừa sửa chưa được lập\./)).toHaveTextContent("Đã lập DS-0003");
    expect(bodies).toHaveLength(2);
    expect(bodies[1]).toEqual(bodies[0]);
    expect(bodies[0]).toMatchObject({ period_to: "2026-10-12" });
    expect(keys[1]).toBe(keys[0]);
    expect(screen.getByRole("dialog", { name: "Lập bảng kê đối soát" })).toBeInTheDocument();
  });

  it("marks the period end the server refuses on a new statement", async () => {
    server.use(
      ...statementHandlers([draft]),
      http.get(apiUrl(`/customers/${CUSTOMER_ID}`), () => HttpResponse.json({ id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", customer_group_name: null, credit_limit: null })),
      http.post(apiUrl("/statements"), () => apiError(422, "validation_failed", { fields: { period_to: "invalid" } })),
    );
    const user = userEvent.setup();
    renderApp(`/debts/statements?party=${CUSTOMER_ID}`);

    const dialog = await screen.findByRole("dialog", { name: "Lập bảng kê đối soát" });
    await user.click(within(dialog).getByRole("button", { name: "Lập bảng kê" }));
    expect(await within(dialog).findByText("Giá trị không hợp lệ.")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Đến ngày")).toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).getByLabelText("Từ ngày")).not.toHaveAttribute("aria-invalid");
  });
});
