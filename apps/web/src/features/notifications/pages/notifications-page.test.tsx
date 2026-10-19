import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { TEST_ME, apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp, renderWithProviders } from "../../../test/render";
import NotificationsPage from "./notifications-page";

const PATH = "/overview/notifications";
const M_TITLE = "Tạp hóa Minh Châu quá hạn 5 ngày";

const note = (id: string, kind: string, title: string, over: Record<string, unknown> = {}) => ({
  id,
  kind,
  dedupe_key: `${kind}:${id}`,
  severity: "warn",
  title,
  body: null,
  entity_type: null,
  entity_id: null,
  color_id: null,
  action_path: null,
  doc_no: null,
  created_at: "2026-10-12T01:00:00Z",
  updated_at: "2026-10-12T01:00:00Z",
  read_at: null,
  resolved_at: null,
  resolved_by: null,
  snoozed_until: null,
  ...over,
});

const overdue = note("n-1", "receivable_overdue", "Tạp hóa Minh Châu quá hạn 5 ngày", {
  severity: "bad",
  body: "còn 1.200.000 ₫",
  entity_type: "party",
  entity_id: "cus-1",
  action_path: "/debts/receivables?party=cus-1",
  doc_no: "SO-0001",
});
const overdue2 = note("n-2", "receivable_overdue", "Cô Lan quá hạn 2 ngày", {
  severity: "bad",
  entity_id: "cus-2",
  action_path: "/debts/receivables?party=cus-2",
  read_at: "2026-10-12T02:00:00Z",
});
const posSync = note("n-3", "pos_sync_exception", "Đơn POS offline cần xử lý", { action_path: "/sales/pos?exceptions=open" });
// No action path: the file is downloaded through the export job.
const exportReady = note("n-4", "export_ready", "File xuất Tuổi nợ đã sẵn sàng", {
  severity: "info",
  entity_type: "export_job",
  entity_id: "exp-1",
});
const disputed = note("n-5", "statement_disputed", "Tạp hóa Minh Châu không khớp sao kê BK-0007", {
  entity_type: "statement",
  entity_id: "st-7",
  action_path: "/debts/statements?id=st-7",
  doc_no: "BK-0007",
});

const DOWNLOAD = "/api/v1/export-jobs/exp-1/download";

/** `GET /export-jobs/exp-1` as the server answers it now. */
const exportState = (status: string, download_path: string | null = null) => ({
  id: "exp-1",
  kind: "report_csv",
  report_key: "debt_aging",
  params: { as_of: "2026-10-12", direction: "receivable" },
  status,
  row_count: 12,
  error: null,
  created_at: "2026-10-12T01:00:00Z",
  finished_at: status === "succeeded" || status === "expired" ? "2026-10-12T01:01:00Z" : null,
  expires_at: "2026-10-13T01:01:00Z",
  download_path,
});

const rowOf = async (title: string) => {
  const row = (await screen.findByText(title)).closest("tr");
  if (row === null) throw new Error("row missing");
  return row;
};

/** Serves the active list; each request takes the next answer, the last one repeats. */
function serveList(...answers: unknown[][]) {
  const states: (string | null)[] = [];
  let i = 0;
  server.use(
    http.get(apiUrl("/notifications"), ({ request }) => {
      states.push(new URL(request.url).searchParams.get("state"));
      const data = answers[Math.min(i, answers.length - 1)] ?? [];
      i += 1;
      return HttpResponse.json({ data, next_cursor: null });
    }),
  );
  return states;
}

interface Posted {
  path: string;
  body: string;
  key: string | null;
}

function recordCommands(answer: (path: string) => Response | undefined = () => undefined) {
  const posted: Posted[] = [];
  server.use(
    http.post(apiUrl("/notifications/:command"), async ({ request }) => {
      const path = new URL(request.url).pathname.replace(/^.*\/notifications\//, "");
      posted.push({ path, body: await request.text(), key: request.headers.get("Idempotency-Key") });
      return answer(path) ?? HttpResponse.json({ ...overdue, read_at: "2026-10-12T03:00:00Z" });
    }),
  );
  return posted;
}

const chips = () => within(screen.getByRole("group", { name: "Loại thông báo" })).getAllByRole("button");

describe("NotificationsPage", () => {
  it("counts the active notifications by kind, keeping the owner's daily chips at 0, with no approval chip", async () => {
    const states = serveList([overdue, posSync, overdue2, exportReady]);
    const user = userEvent.setup();
    renderApp(PATH);

    expect(await screen.findByText("Tạp hóa Minh Châu quá hạn 5 ngày")).toBeInTheDocument();
    expect(screen.getByText("Thông báo không tự sinh chứng từ: ví dụ cảnh báo thiếu hàng chỉ gợi ý mua, không tự đặt PO.")).toBeInTheDocument();
    expect(chips().map((c) => c.textContent)).toEqual([
      "Tất cả (4)",
      "Nợ quá hạn (2)",
      "Sắp phải trả NCC (0)",
      "Sắp hết hạn (0)",
      "Hết hạn – chặn bán (0)",
      "Dưới tồn tối thiểu (0)",
      "Lỗi đồng bộ POS (1)",
      "File xuất sẵn sàng (1)",
    ]);
    expect(screen.queryByRole("button", { name: /Chờ duyệt/ })).not.toBeInTheDocument();
    expect(states).toEqual(["active"]);

    const table = screen.getByRole("table", { name: "Thông báo" });
    const first = within(table).getAllByRole("row")[1];
    expect(first).toHaveTextContent("Nợ quá hạn");
    expect(first).toHaveTextContent("SO-0001 · còn 1.200.000 ₫");
    expect(within(table).getAllByRole("row")[3]).not.toHaveTextContent("·");
    // Unread titles stand out; read ones do not.
    expect(screen.getByText("Tạp hóa Minh Châu quá hạn 5 ngày").tagName).toBe("B");
    expect(screen.getByText("Cô Lan quá hạn 2 ngày").tagName).not.toBe("B");

    await user.click(screen.getByRole("button", { name: "Lỗi đồng bộ POS (1)" }));
    expect(screen.getByRole("button", { name: "Lỗi đồng bộ POS (1)" })).toHaveAttribute("aria-pressed", "true");
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    expect(within(table).getByText("Đơn POS offline cần xử lý")).toBeInTheDocument();
  });

  it("opens the screen that handles an unread notification, then marks it read", async () => {
    serveList([overdue, overdue2, exportReady]);
    const posted = recordCommands();
    let unreadCalls = 0;
    server.use(
      http.get(apiUrl("/notifications/unread-count"), () => {
        unreadCalls += 1;
        return HttpResponse.json({ count: 2 });
      }),
    );
    const user = userEvent.setup();
    const { router } = renderApp(PATH);

    const row = (await screen.findByText("Tạp hóa Minh Châu quá hạn 5 ngày")).closest("tr");
    if (row === null) throw new Error("row missing");
    // An export is downloaded, not opened.
    const exportRow = screen.getByText("File xuất Tuổi nợ đã sẵn sàng").closest("tr");
    if (exportRow === null) throw new Error("row missing");
    expect(within(exportRow).queryByRole("button", { name: /^Mở/ })).not.toBeInTheDocument();
    expect(within(exportRow).getByRole("button", { name: "Tải về – File xuất Tuổi nợ đã sẵn sàng" })).toBeInTheDocument();
    const before = unreadCalls;

    await user.click(within(row).getByRole("button", { name: "Mở – Tạp hóa Minh Châu quá hạn 5 ngày" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/debts/receivables"));
    expect(router.state.location.search).toBe("?party=cus-1");
    await waitFor(() => expect(posted).toEqual([{ path: "n-1:read", body: "", key: null }]));
    await waitFor(() => expect(unreadCalls).toBeGreaterThan(before));
  });

  it("opens a read notification without marking it again", async () => {
    serveList([overdue2, posSync]);
    const posted = recordCommands();
    const user = userEvent.setup();
    const { router } = renderApp(PATH);

    const row = (await screen.findByText("Cô Lan quá hạn 2 ngày")).closest("tr");
    if (row === null) throw new Error("row missing");
    await user.click(within(row).getByRole("button", { name: "Mở – Cô Lan quá hạn 2 ngày" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/debts/receivables"));
    expect(router.state.location.search).toBe("?party=cus-2");
    expect(posted).toEqual([]);
  });

  it("snoozes for a day and resolves, then shows the refreshed list", async () => {
    serveList([overdue, posSync], [posSync], []);
    const posted = recordCommands();
    const user = userEvent.setup();
    renderApp(PATH);

    const row = (await screen.findByText("Tạp hóa Minh Châu quá hạn 5 ngày")).closest("tr");
    if (row === null) throw new Error("row missing");
    await user.click(within(row).getByRole("button", { name: "Nhắc lại mai – Tạp hóa Minh Châu quá hạn 5 ngày" }));

    expect(await screen.findByText("Sẽ nhắc lại sau 1 ngày.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Tạp hóa Minh Châu quá hạn 5 ngày")).not.toBeInTheDocument());
    const snooze = posted[0];
    expect(snooze?.path).toBe("n-1:snooze");
    expect(snooze?.key).toBeNull();
    // A day after the server's clock (GET /me `server_time`), whatever the device's says.
    const until = Date.parse((JSON.parse(snooze?.body ?? "{}") as { until: string }).until);
    const serverTime = Date.parse(TEST_ME.server_time);
    expect(until - serverTime).toBeGreaterThanOrEqual(24 * 60 * 60_000);
    expect(until - serverTime).toBeLessThanOrEqual(24 * 60 * 60_000 + 60_000);

    const pos = screen.getByText("Đơn POS offline cần xử lý").closest("tr");
    if (pos === null) throw new Error("row missing");
    await user.click(within(pos).getByRole("button", { name: "Đã xử lý – Đơn POS offline cần xử lý" }));

    expect(await screen.findByText("Đã đánh dấu đã xử lý.")).toBeInTheDocument();
    expect(posted[1]).toEqual({ path: "n-3:resolve", body: "", key: null });
    expect(await screen.findByText("Không có việc cần xử lý.")).toBeInTheDocument();
    expect(chips().map((c) => c.textContent)).toEqual(["Tất cả (0)", "Nợ quá hạn (0)", "Sắp phải trả NCC (0)", "Sắp hết hạn (0)", "Hết hạn – chặn bán (0)", "Dưới tồn tối thiểu (0)", "Lỗi đồng bộ POS (0)"]);
  });

  it("says so when the notification was already handled elsewhere", async () => {
    serveList([overdue]);
    recordCommands(() => apiError(409, "notification_resolved"));
    const user = userEvent.setup();
    renderApp(PATH);

    const row = (await screen.findByText("Tạp hóa Minh Châu quá hạn 5 ngày")).closest("tr");
    if (row === null) throw new Error("row missing");
    await user.click(within(row).getByRole("button", { name: "Nhắc lại mai – Tạp hóa Minh Châu quá hạn 5 ngày" }));

    const toast = await screen.findByText("Thông báo đã được xử lý.");
    expect(toast).toHaveTextContent("Chưa ghi sổ. Thông báo đã được xử lý.");
  });

  it("shows the empty state when nothing is waiting", async () => {
    serveList([]);
    renderApp(PATH);

    expect(await screen.findByText("Không có việc cần xử lý.")).toBeInTheDocument();
    expect(chips().map((c) => c.textContent)).toEqual(["Tất cả (0)", "Nợ quá hạn (0)", "Sắp phải trả NCC (0)", "Sắp hết hạn (0)", "Hết hạn – chặn bán (0)", "Dưới tồn tối thiểu (0)", "Lỗi đồng bộ POS (0)"]);
    // A chip at 0 still filters, to an empty list.
    await userEvent.setup().click(screen.getByRole("button", { name: "Sắp hết hạn (0)" }));
    expect(screen.getByRole("button", { name: "Sắp hết hạn (0)" })).toHaveAttribute("aria-pressed", "true");
  });

  it("opens the disputed statement itself", async () => {
    serveList([disputed]);
    recordCommands();
    const { router } = renderApp(PATH);

    await userEvent.setup().click(within(await rowOf("Tạp hóa Minh Châu không khớp sao kê BK-0007")).getByRole("button", { name: "Mở – Tạp hóa Minh Châu không khớp sao kê BK-0007" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/debts/statements"));
    expect(router.state.location.search).toBe("?id=st-7");
  });

  it("opens the route the notification names, and offers no Mở for a path outside the app", async () => {
    const low = note("n-6", "below_min_stock", "BQ454 dưới tồn tối thiểu", { entity_type: "sku", entity_id: "sku-1", action_path: "/inventory/stock-card?sku=sku-1" });
    const foreign = note("n-7", "surplus_stock", "KD350 vượt tồn tối đa", { action_path: "//example.com/stock" });
    serveList([low, foreign]);
    recordCommands();
    const { router } = renderApp(PATH);

    expect(within(await rowOf("KD350 vượt tồn tối đa")).queryByRole("button", { name: /^Mở/ })).not.toBeInTheDocument();
    await userEvent.setup().click(within(await rowOf("BQ454 dưới tồn tối thiểu")).getByRole("button", { name: "Mở – BQ454 dưới tồn tối thiểu" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/stock-card"));
    expect(router.state.location.search).toBe("?sku=sku-1");
  });

  it("still opens the screen when marking read fails, and says only that", async () => {
    serveList([overdue]);
    recordCommands(() => apiError(404, "not_found"));
    const { router } = renderApp(PATH);

    await userEvent.setup().click(within(await rowOf("Tạp hóa Minh Châu quá hạn 5 ngày")).getByRole("button", { name: "Mở – Tạp hóa Minh Châu quá hạn 5 ngày" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/debts/receivables"));
    const toast = await screen.findByText(/Chưa đánh dấu đã đọc\./);
    expect(toast).toHaveTextContent("Chưa đánh dấu đã đọc. Không tìm thấy thông báo. Tải lại để xem danh sách mới.");
    expect(screen.queryByText(/Chưa ghi sổ/)).not.toBeInTheDocument();
  });

  it("downloads a finished export after a fresh read, then marks the notice read", async () => {
    serveList([exportReady]);
    const posted = recordCommands();
    let reads = 0;
    server.use(
      http.get(apiUrl("/export-jobs/exp-1"), () => {
        reads += 1;
        return HttpResponse.json(exportState("succeeded", DOWNLOAD));
      }),
    );
    const openFile = vi.fn();
    renderWithProviders(<NotificationsPage openFile={openFile} />, { path: PATH });

    await userEvent.setup().click(within(await rowOf("File xuất Tuổi nợ đã sẵn sàng")).getByRole("button", { name: "Tải về – File xuất Tuổi nợ đã sẵn sàng" }));
    await waitFor(() => expect(openFile).toHaveBeenCalledWith(DOWNLOAD));
    expect(reads).toBe(1);
    await waitFor(() => expect(posted).toEqual([{ path: "n-4:read", body: "", key: null }]));
  });

  it.each([
    ["expired since it was listed", () => HttpResponse.json(exportState("expired"))],
    ["refused as gone", () => apiError(410, "export_expired")],
  ])("opens nothing for an export %s and says it expired", async (_, answer) => {
    serveList([exportReady]);
    const posted = recordCommands();
    server.use(http.get(apiUrl("/export-jobs/exp-1"), answer));
    const openFile = vi.fn();
    renderWithProviders(<NotificationsPage openFile={openFile} />, { path: PATH });

    await userEvent.setup().click(within(await rowOf("File xuất Tuổi nợ đã sẵn sàng")).getByRole("button", { name: "Tải về – File xuất Tuổi nợ đã sẵn sàng" }));
    expect(await screen.findByText("Tệp xuất đã hết hạn. Bấm “Xuất CSV” để xuất lại.")).toBeInTheDocument();
    expect(openFile).not.toHaveBeenCalled();
    expect(posted).toEqual([]);
  });

  it("shows progress only on the action running and hands focus back to it when it is refused", async () => {
    serveList([overdue, overdue2]);
    let refuse = () => {};
    const refused = new Promise<void>((resolve) => {
      refuse = resolve;
    });
    recordCommands();
    let snoozes = 0;
    server.use(
      http.post(apiUrl("/notifications/n-1:snooze"), async () => {
        snoozes++;
        await refused;
        return apiError(409, "notification_resolved");
      }),
    );
    const user = userEvent.setup();
    renderApp(PATH);

    const row = await rowOf(M_TITLE);
    const other = await rowOf("Cô Lan quá hạn 2 ngày");
    await user.click(within(row).getByRole("button", { name: `Nhắc lại mai – ${M_TITLE}` }));

    const running = await within(row).findByRole("button", { name: "Đang lưu…" });
    // It keeps focus while the server answers; pressing it again sends nothing.
    expect(running).toHaveAttribute("aria-disabled", "true");
    expect(running).toHaveAttribute("aria-busy", "true");
    expect(running).toHaveFocus();
    await user.click(running);
    await waitFor(() => expect(snoozes).toBe(1));
    expect(within(row).getByRole("button", { name: `Đã xử lý – ${M_TITLE}` })).toBeDisabled();
    const waiting = within(other).getByRole("button", { name: "Nhắc lại mai – Cô Lan quá hạn 2 ngày" });
    expect(waiting).toBeDisabled();
    expect(waiting).not.toHaveAttribute("aria-busy");
    expect(screen.getAllByRole("button", { name: "Đang lưu…" })).toHaveLength(1);

    refuse();
    expect(await screen.findByText("Thông báo đã được xử lý.", { exact: false })).toBeInTheDocument();
    const again = within(row).getByRole("button", { name: `Nhắc lại mai – ${M_TITLE}` });
    await waitFor(() => expect(again).toBeEnabled());
    expect(again).toHaveFocus();
  });

  it("moves focus to the next notification's first action once the handled one leaves the list", async () => {
    serveList([overdue, posSync], [posSync]);
    recordCommands();
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(within(await rowOf(M_TITLE)).getByRole("button", { name: `Đã xử lý – ${M_TITLE}` }));

    await waitFor(() => expect(screen.queryByText(M_TITLE)).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole("button", { name: "Mở – Đơn POS offline cần xử lý" })).toHaveFocus());
  });

  it("moves focus to the page once the last notification is handled", async () => {
    serveList([posSync], []);
    recordCommands();
    const user = userEvent.setup();
    renderApp(PATH);

    await user.click(within(await rowOf("Đơn POS offline cần xử lý")).getByRole("button", { name: "Đã xử lý – Đơn POS offline cần xử lý" }));

    expect(await screen.findByText("Không có việc cần xử lý.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("main")).toHaveFocus());
  });
});
