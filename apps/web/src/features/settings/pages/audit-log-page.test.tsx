import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { PATHS } from "../../../shared/api/endpoints";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import type { AuditEvent } from "../api/audit";
import { testAuditEvent, testOwner } from "../testing/fixtures";
import { AUDIT_LEAD, AUDIT_SEARCH_PLACEHOLDER } from "./audit-log-page";

const PATH = "/settings/audit-log";

const EVENTS: AuditEvent[] = [
  testAuditEvent(),
  testAuditEvent({ id: "ae-2", action: "sales_document.post", entity_type: "sales_document", entity_id: "so-1", doc_no: "HD-0001", source: "pos", ip: null, created_at: "2026-10-12T01:40:00Z" }),
  testAuditEvent({ id: "ae-3", actor_type: "support", actor_id: "adm-1", action: "support_session.access", entity_type: "support_session", ip: "10.0.0.5", source: "support" }),
  testAuditEvent({ id: "ae-4", actor_type: "system", actor_id: null, action: "widget.frobnicate", entity_type: "widget", source: "worker", ip: null }),
];

/** GET /audit-events in pages: the request's query is recorded and `pages[cursor ?? ""]` answered. */
function auditHandler(pages: Record<string, { data: AuditEvent[]; next_cursor: string | null }>) {
  const queries: Record<string, string>[] = [];
  const handler = http.get(apiUrl("/audit-events"), ({ request }) => {
    const query = Object.fromEntries(new URL(request.url).searchParams);
    queries.push(query);
    return HttpResponse.json(pages[query.cursor ?? ""] ?? { data: [], next_cursor: null });
  });
  return { handler, queries };
}

const owner = http.get(apiUrl(PATHS.me), () => HttpResponse.json(testOwner()));

describe("AuditLogPage", () => {
  it("lists the events with Vietnamese labels, the owner's name and the raw code of an unknown action", async () => {
    const audit = auditHandler({ "": { data: EVENTS, next_cursor: null } });
    server.use(owner, audit.handler);
    renderApp(PATH);

    expect(await screen.findByText(AUDIT_LEAD)).toBeInTheDocument();
    expect(screen.getByPlaceholderText(AUDIT_SEARCH_PLACEHOLDER)).toBeInTheDocument();
    // The search matches codes and names, so the box says what to type.
    expect(screen.getByText("Tìm theo số chứng từ, mã thao tác hoặc tên người.")).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Nhật ký" });
    expect(within(table).getAllByRole("columnheader").map((th) => th.textContent)).toEqual(["Thời điểm", "Người dùng", "Thao tác", "Chứng từ", "Chi tiết"]);

    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(4);
    expect(rows[0]).toHaveTextContent("Nguyễn Văn An");
    expect(rows[0]).toHaveTextContent("Khóa sổ kỳ");
    expect(rows[0]).toHaveTextContent("Kỳ khóa sổ");
    expect(rows[0]).toHaveTextContent("Trình duyệt · 113.161.4.20");
    expect(rows[1]).toHaveTextContent("HD-0001");
    expect(rows[1]).toHaveTextContent("Máy bán hàng");
    expect(rows[2]).toHaveTextContent("Nhân viên hỗ trợ");
    expect(rows[2]).toHaveTextContent("Hỗ trợ xem dữ liệu");
    expect(rows[3]).toHaveTextContent("Hệ thống");
    expect(rows[3]).toHaveTextContent("widget.frobnicate");
    expect(audit.queries).toEqual([{ limit: "200" }]);
  });

  it("asks the API for the text typed and shows only what it answers", async () => {
    const audit = auditHandler({ "": { data: EVENTS, next_cursor: null } });
    const searched = http.get(apiUrl("/audit-events"), ({ request }) => {
      const query = Object.fromEntries(new URL(request.url).searchParams);
      if (!query.q) return undefined;
      audit.queries.push(query);
      return HttpResponse.json(query.q === "HD-0001" ? { data: [EVENTS[1]], next_cursor: null } : { data: [], next_cursor: null });
    });
    server.use(owner, searched, audit.handler);
    const user = userEvent.setup();
    renderApp(PATH);

    await screen.findByRole("table", { name: "Nhật ký" });
    const box = screen.getByRole("searchbox", { name: "Tìm trong nhật ký" });
    // The API takes at most 100 characters of search.
    expect(box).toHaveAttribute("maxlength", "100");
    await user.type(box, " HD-0001 ");
    // The table is drawn again for the answer of the search.
    await waitFor(() => expect(within(screen.getByRole("table", { name: "Nhật ký" })).getAllByRole("row").slice(1).map((r) => r.textContent)).toEqual([expect.stringContaining("HD-0001")]));
    // The text is sent once the typing stops, trimmed.
    expect(audit.queries).toEqual([{ limit: "200" }, { limit: "200", q: "HD-0001" }]);

    await user.clear(box);
    await user.type(box, "không có");
    expect(await screen.findByText("Không có dòng nào khớp.")).toBeInTheDocument();
    expect(audit.queries.at(-1)).toEqual({ limit: "200", q: "không có" });
  });

  it("loads the next 200 of a search with Xem thêm, keeping the text", async () => {
    const audit = auditHandler({
      "": { data: [testAuditEvent()], next_cursor: "c1" },
      c2: { data: [testAuditEvent({ id: "ae-9", action: "tenant.update", entity_type: "tenant" })], next_cursor: null },
    });
    const firstSearchPage = http.get(apiUrl("/audit-events"), ({ request }) => {
      const query = Object.fromEntries(new URL(request.url).searchParams);
      if (query.q !== "tenant" || query.cursor) return undefined;
      audit.queries.push(query);
      return HttpResponse.json({ data: [testAuditEvent({ id: "ae-8", action: "tenant.create", entity_type: "tenant" })], next_cursor: "c2" });
    });
    server.use(owner, firstSearchPage, audit.handler);
    const user = userEvent.setup();
    renderApp(PATH);

    await screen.findByRole("table", { name: "Nhật ký" });
    await user.type(screen.getByRole("searchbox", { name: "Tìm trong nhật ký" }), "tenant");
    expect(await screen.findByText("Tạo cửa hàng")).toBeInTheDocument();
    // A new search starts from the first page, not from the cursor of the unfiltered list.
    expect(screen.queryByText("Khóa sổ kỳ")).not.toBeInTheDocument();
    expect(screen.queryByText(/dòng đã tải/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Xem thêm" }));

    expect(await screen.findByText("Sửa thông tin cửa hàng")).toBeInTheDocument();
    expect(audit.queries).toEqual([{ limit: "200" }, { limit: "200", q: "tenant" }, { limit: "200", q: "tenant", cursor: "c2" }]);
    expect(screen.queryByRole("button", { name: "Xem thêm" })).not.toBeInTheDocument();
  });

  it("says when nothing was logged yet", async () => {
    server.use(owner, auditHandler({}).handler);
    renderApp(PATH);

    expect(await screen.findByText("Chưa có thao tác nào.")).toBeInTheDocument();
  });
});
