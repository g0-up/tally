import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { testCount, testCountLine, testCountListItem } from "../testing/fixtures";
import type { CountListItem } from "../schemas";

const varianceCount = testCount({ status: "pending_post", version: 5, lines: [testCountLine({ system_qty: "24", counted_qty: "20", variance_qty: "-4" })] });
const varianceRow = testCountListItem({ status: "pending_post", version: 5, counted_lines: 1, variance_lines: 1 });

const lookups = [
  http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
  http.get(apiUrl("/categories"), () => HttpResponse.json({ data: [] })),
];

/** `GET /stock-counts` as the server answers it: the first page, or the count a `doc_no` names. */
function countList(rows: CountListItem[], queries: URLSearchParams[] = [], nextCursor: string | null = null) {
  return http.get(apiUrl("/stock-counts"), ({ request }) => {
    const query = new URL(request.url).searchParams;
    queries.push(query);
    const docNo = query.get("doc_no");
    if (docNo !== null) return HttpResponse.json({ data: rows.filter((c) => c.doc_no === docNo), next_cursor: null });
    return HttpResponse.json({ data: rows, next_cursor: nextCursor });
  });
}

const rowOf = (docNo: string) => screen.getByRole("button", { name: docNo }).closest("tr") as HTMLElement;

describe("Kiểm kê page", () => {
  it("starts a blind count and opens it straight away", async () => {
    let body: unknown = null;
    server.use(
      ...lookups,
      countList([]),
      http.post(apiUrl("/stock-counts"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(testCount());
      }),
      http.get(apiUrl("/stock-counts/cnt-1"), () => HttpResponse.json(testCount())),
    );
    const user = userEvent.setup();
    renderApp("/inventory/counts");

    await user.click(await screen.findByRole("button", { name: "Bắt đầu phiên kiểm kê" }));
    const dialog = await screen.findByRole("dialog", { name: "Bắt đầu kiểm kê" });
    await user.click(within(dialog).getByRole("button", { name: "Bắt đầu" }));

    expect(await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" })).toBeInTheDocument();
    expect(body).toEqual({ scope: "all", blind: true });
  });

  it("shows each count's progress from the list read alone", async () => {
    let detailReads = 0;
    server.use(
      ...lookups,
      countList([
        testCountListItem({ line_count: 12, counted_lines: 5 }),
        testCountListItem({ id: "cnt-2", doc_no: "KK-0002", blind: false, line_count: 8, counted_lines: 3, variance_lines: 1 }),
        testCountListItem({ id: "cnt-3", doc_no: "KK-0003", status: "pending_post", line_count: 8, counted_lines: 8, variance_lines: 2 }),
      ]),
      http.get(/\/stock-counts\/cnt-\d$/, () => {
        detailReads += 1;
        return HttpResponse.json(testCount());
      }),
    );
    renderApp("/inventory/counts");

    await screen.findByRole("button", { name: "KK-0003" });
    const progress = (docNo: string) => within(rowOf(docNo)).getAllByRole("cell").slice(2, 5).map((c) => c.textContent);
    // A blind count hides its variances while counting; any count shows none until counting ends.
    expect(progress("KK-0001")).toEqual(["12", "5", "—"]);
    expect(progress("KK-0002")).toEqual(["8", "3", "—"]);
    expect(progress("KK-0003")).toEqual(["8", "8", "2"]);
    expect(detailReads).toBe(0);
  });

  it("posts the variances of a linked count once the owner confirms them", async () => {
    const posts: unknown[] = [];
    let confirmation: unknown = null;
    server.use(
      ...lookups,
      countList([varianceRow]),
      http.get(apiUrl("/stock-counts/cnt-1"), () => HttpResponse.json(varianceCount)),
      http.post(/\/stock-counts\/cnt-1:post$/, async ({ request }) => {
        const body = await request.json();
        posts.push(body);
        if (posts.length === 1) return apiError(409, "exception_required", { exception_kind: "count_variance" });
        return HttpResponse.json(testCount({ status: "posted", version: 6, adjustment_no: "DC-0009" }));
      }),
      http.post(apiUrl("/exception-confirmations"), async ({ request }) => {
        confirmation = await request.json();
        return HttpResponse.json({ id: "exc-1" });
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/counts?doc=KK-0001");

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.click(await within(drawer).findByRole("button", { name: "Xác nhận & ghi sổ chênh lệch" }));
    const confirm = await screen.findByRole("dialog", { name: "Xác nhận ghi sổ chênh lệch KK-0001" });
    expect(within(confirm).getByText("Chênh lệch -4 hộp")).toBeInTheDocument();
    await user.click(within(confirm).getByRole("checkbox", { name: "Tôi xác nhận ghi sổ chênh lệch" }));
    await user.click(within(confirm).getByRole("button", { name: "Xác nhận & ghi sổ" }));

    expect(await screen.findByText("Đã ghi sổ chênh lệch kiểm kê.")).toBeInTheDocument();
    expect(confirmation).toMatchObject({ kind: "count_variance", doc_type: "stock_count", doc_id: "cnt-1", doc_version: 5 });
    await waitFor(() => expect(posts).toEqual([{}, { exception_confirmation_id: "exc-1" }]));
  });

  it("opens a linked count older than the loaded page by its number, without paging through the list", async () => {
    const queries: URLSearchParams[] = [];
    const recent = testCountListItem({ id: "cnt-9", doc_no: "KK-0009" });
    const old = testCountListItem({ id: "cnt-2", doc_no: "KK-0002", status: "posted", counted_lines: 1, variance_lines: 0 });
    server.use(
      ...lookups,
      http.get(apiUrl("/stock-counts"), ({ request }) => {
        const query = new URL(request.url).searchParams;
        queries.push(query);
        if (query.get("doc_no") === "KK-0002") return HttpResponse.json({ data: [old], next_cursor: null });
        return HttpResponse.json({ data: [recent], next_cursor: "c2" });
      }),
      http.get(apiUrl("/stock-counts/cnt-2"), () => HttpResponse.json(testCount({ id: "cnt-2", doc_no: "KK-0002", status: "posted" }))),
    );
    renderApp("/inventory/counts?doc=KK-0002");

    expect(await screen.findByRole("dialog", { name: "Kiểm kê KK-0002" })).toBeInTheDocument();
    expect(queries.some((q) => q.get("doc_no") === "KK-0002")).toBe(true);
    expect(queries.some((q) => q.has("cursor"))).toBe(false);
  });

  it("says when no count has the linked number", async () => {
    server.use(...lookups, countList([testCountListItem()], [], "c2"));
    renderApp("/inventory/counts?doc=KK-0404");
    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê" });
    expect(within(drawer).getByText("Không tìm thấy KK-0404.")).toBeInTheDocument();
  });
});
