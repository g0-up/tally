import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { STALE_DATA_MESSAGE } from "../../../shared/api/api-error";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { etagOf } from "../../catalog";
import { at, CHECKLIST, SUMMER_ID, TET_ID, testCampaign, testDemandRow, testSeasonReport } from "../testing/fixtures";

const ROUTE = "/campaigns/seasons";

const tet = testCampaign();
const summer = testCampaign({
  id: SUMMER_ID,
  name: "Mùa hè 2027",
  kind: "season",
  starts_on: "2027-05-01",
  ends_on: "2027-07-31",
  budget_amount: null,
  target_revenue: null,
  status: "planning",
  version: 1,
});

/** Cell texts of a row, money with a plain space before ₫. */
const cells = (row: HTMLElement) => within(row).getAllByRole("cell").map((c) => (c.textContent ?? "").replace(/\s/g, " "));

/** The `.card` of a campaign by its heading. */
async function card(name: string): Promise<HTMLElement> {
  const heading = await screen.findByRole("heading", { name, level: 2 });
  const el = heading.closest<HTMLElement>(".card");
  if (el === null) throw new Error(`No card for ${name}`);
  return el;
}

function seasonHandlers({ campaigns = [tet, summer], checklist = CHECKLIST, demand = [testDemandRow(), testDemandRow({ sku_id: "sku-kd350", code: "KD350", name: "Kẹo dừa hộp 350g", available: "500", backlog: "0", undated_incoming: "30", first_shortage_on: null, max_shortage: "0", days: [{ date: "2026-10-01", demand: "200", incoming: "0", projected: "300", shortage: "0" }] })] } = {}) {
  const demandQueries: URLSearchParams[] = [];
  const handlers = [
    http.get(apiUrl("/campaigns"), () => HttpResponse.json({ data: campaigns, next_cursor: null })),
    http.get(apiUrl("/campaigns/:id/checklist"), ({ params }) => HttpResponse.json({ data: params.id === TET_ID ? checklist : [] })),
    http.get(apiUrl("/reports/season"), ({ request }) => {
      const id = new URL(request.url).searchParams.get("campaign_id");
      return HttpResponse.json(id === TET_ID ? testSeasonReport() : testSeasonReport({ net_revenue: "0", target_revenue: null, revenue_achieved: null, gross_margin: "0", budget: null, open_orders: "0", receivable: "0" }));
    }),
    http.get(apiUrl("/campaigns/:id/demand"), ({ params, request }) => {
      expect(params.id).toBe(TET_ID);
      demandQueries.push(new URL(request.url).searchParams);
      return HttpResponse.json({ data: demand, next_cursor: null });
    }),
  ];
  return { handlers, demandQueries };
}

describe("SeasonsPage", () => {
  it("shows each campaign's season figures from the season report, with no figure the API lacks", async () => {
    server.use(...seasonHandlers().handlers);
    renderApp(ROUTE);

    expect(await screen.findByText("Chiến dịch là nhãn phân tích, không phải sổ riêng. Mỗi dòng doanh thu/chi phí có một chiến dịch chính. Kết mùa không xóa tồn, tiền hay nợ.")).toBeInTheDocument();
    const tetCard = await card("Tết Đinh Mùi 2027");
    expect(within(tetCard).getByText("Đang chạy")).toBeInTheDocument();
    expect(within(tetCard).getByText("01/10/2026 – 28/02/2027")).toBeInTheDocument();
    expect(await within(tetCard).findByText("Doanh thu thuần / mục tiêu", { selector: "span" })).toBeInTheDocument();
    expect(within(tetCard).getByText(/^120\.000\.000\s₫ \/ 800\.000\.000\s₫$/)).toBeInTheDocument();
    expect(within(tetCard).getByRole("progressbar", { name: "Doanh thu thuần / mục tiêu" })).toHaveAttribute("aria-valuenow", "15");
    expect(within(tetCard).getByText("15% sau 12/151 ngày của mùa")).toBeInTheDocument();
    const impact = (label: string) => (within(tetCard).getByText(label).nextElementSibling?.textContent ?? "").replace(/\s/g, " ");
    expect(impact("Lãi gộp")).toBe("18.000.000 ₫");
    expect(impact("Chi phí mùa")).toBe("— / ngân sách 25.000.000 ₫");
    expect(impact("Kết quả quản trị")).toBe("—");
    expect(impact("Đơn còn phải giao")).toBe("30.000.000 ₫");
    expect(impact("Khách còn nợ từ đơn mùa")).toBe("40.000.000 ₫");
    expect(within(tetCard).getByRole("button", { name: "Kết mùa Tết Đinh Mùi 2027" })).toBeInTheDocument();

    const summerCard = await card("Mùa hè 2027");
    expect(within(summerCard).getByText("Kế hoạch")).toBeInTheDocument();
    expect(await within(summerCard).findByText("Chưa đặt doanh thu mục tiêu sau 0/92 ngày của mùa")).toBeInTheDocument();
    expect(within(summerCard).getByText(/^0\s₫ \/ —$/)).toBeInTheDocument();
    const summerImpact = (label: string) => (within(summerCard).getByText(label).nextElementSibling?.textContent ?? "").replace(/\s/g, " ");
    expect(summerImpact("Đơn còn phải giao")).toBe("0 ₫");
    expect(summerImpact("Khách còn nợ từ đơn mùa")).toBe("0 ₫");
    expect(within(summerCard).getByRole("button", { name: "Bắt đầu mùa Mùa hè 2027" })).toBeInTheDocument();
    expect(within(summerCard).queryByText("Checklist trước cao điểm & kết mùa")).not.toBeInTheDocument();
  });

  it("lists the checklist in order and saves a tick with PATCH, without If-Match or Idempotency-Key", async () => {
    const patches: { body: unknown; ifMatch: string | null; key: string | null }[] = [];
    server.use(
      ...seasonHandlers().handlers,
      http.patch(apiUrl(`/campaigns/${TET_ID}/checklist`), async ({ request }) => {
        patches.push({ body: await request.json(), ifMatch: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key") });
        return HttpResponse.json({ data: CHECKLIST.map((x) => (x.id === "chk-2" ? { ...x, done: true } : x)) });
      }),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const tetCard = await card("Tết Đinh Mùi 2027");
    expect(await within(tetCard).findByRole("heading", { name: "Checklist trước cao điểm & kết mùa", level: 3 })).toBeInTheDocument();
    const boxes = within(tetCard).getAllByRole("checkbox");
    expect(boxes.map((b) => b.closest("label")?.textContent?.trim())).toEqual(["Chốt bảng giá sỉ mùa Tết", "Đặt đủ hộp quà với nhà cung cấp", "Đối chiếu công nợ khách sỉ"]);
    expect(at(boxes, 0)).toBeChecked();
    expect(at(boxes, 1)).not.toBeChecked();

    await user.click(within(tetCard).getByRole("checkbox", { name: "Đặt đủ hộp quà với nhà cung cấp" }));
    await waitFor(() => expect(within(tetCard).getByRole("checkbox", { name: "Đặt đủ hộp quà với nhà cung cấp" })).toBeChecked());
    expect(patches).toEqual([{ body: { items: [{ id: "chk-2", done: true }] }, ifMatch: null, key: null }]);
  });

  it("keeps the item being saved focused and busy while the other items wait for its answer", async () => {
    let answer: () => void = () => undefined;
    server.use(
      ...seasonHandlers().handlers,
      http.patch(apiUrl(`/campaigns/${TET_ID}/checklist`), async () => {
        await new Promise<void>((resolve) => (answer = resolve));
        return HttpResponse.json({ data: CHECKLIST.map((x) => (x.id === "chk-2" ? { ...x, done: true } : x)) });
      }),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const tetCard = await card("Tết Đinh Mùi 2027");
    const box = (name: string) => within(tetCard).getByRole("checkbox", { name });
    await user.click(await within(tetCard).findByRole("checkbox", { name: "Đặt đủ hộp quà với nhà cung cấp" }));
    await waitFor(() => expect(box("Đặt đủ hộp quà với nhà cung cấp")).toHaveAttribute("aria-busy", "true"));
    expect(box("Đặt đủ hộp quà với nhà cung cấp")).toBeEnabled();
    expect(box("Đặt đủ hộp quà với nhà cung cấp")).toHaveFocus();
    expect(box("Chốt bảng giá sỉ mùa Tết")).toBeDisabled();
    expect(box("Đối chiếu công nợ khách sỉ")).toBeDisabled();

    answer();
    await waitFor(() => expect(box("Đặt đủ hộp quà với nhà cung cấp")).toBeChecked());
    expect(box("Đặt đủ hộp quà với nhà cung cấp")).not.toHaveAttribute("aria-busy");
    expect(box("Chốt bảng giá sỉ mùa Tết")).toBeEnabled();
    expect(box("Đối chiếu công nợ khách sỉ")).toBeEnabled();
  });

  it("says why a tick is refused on a season that was closed meanwhile", async () => {
    server.use(
      ...seasonHandlers().handlers,
      http.patch(apiUrl(`/campaigns/${TET_ID}/checklist`), () => apiError(409, "campaign_closed")),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const tetCard = await card("Tết Đinh Mùi 2027");
    await user.click(await within(tetCard).findByRole("checkbox", { name: "Đặt đủ hộp quà với nhà cung cấp" }));
    expect(await screen.findByText("Chiến dịch đã kết thúc, không sửa checklist được.", {}, { timeout: 3000 })).toBeInTheDocument();
    expect(within(tetCard).getByRole("checkbox", { name: "Đặt đủ hộp quà với nhà cung cấp" })).not.toBeChecked();
  });

  it("marks the checklist item the server refused and clears it on the next tick", async () => {
    let refuse = true;
    server.use(
      ...seasonHandlers().handlers,
      http.patch(apiUrl(`/campaigns/${TET_ID}/checklist`), () => {
        if (refuse) return apiError(422, "validation_failed", { fields: { "items.0.id": "not_found" } });
        return HttpResponse.json({ data: CHECKLIST.map((x) => (x.id === "chk-2" ? { ...x, done: true } : x)) });
      }),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const tetCard = await card("Tết Đinh Mùi 2027");
    const name = "Đặt đủ hộp quà với nhà cung cấp";
    await user.click(await within(tetCard).findByRole("checkbox", { name }));
    expect(await within(tetCard).findByRole("alert")).toHaveTextContent("Không tìm thấy.");
    expect(within(tetCard).getByRole("checkbox", { name })).toHaveAttribute("aria-invalid", "true");
    expect(within(tetCard).getByRole("checkbox", { name: "Đối chiếu công nợ khách sỉ" })).toHaveAttribute("aria-invalid", "false");
    expect(await screen.findByText("Kiểm tra lại các ô được đánh dấu.", {}, { timeout: 3000 })).toBeInTheDocument();

    refuse = false;
    await user.click(within(tetCard).getByRole("checkbox", { name }));
    await waitFor(() => expect(within(tetCard).getByRole("checkbox", { name })).toBeChecked());
    expect(within(tetCard).queryByRole("alert")).not.toBeInTheDocument();
    expect(within(tetCard).getByRole("checkbox", { name })).toHaveAttribute("aria-invalid", "false");
  });

  it("keeps a closed season's checklist read-only and offers no command", async () => {
    server.use(...seasonHandlers({ campaigns: [testCampaign({ status: "closed", closed_at: "2027-03-01T10:00:00Z" })] }).handlers);
    renderApp(ROUTE);

    const tetCard = await card("Tết Đinh Mùi 2027");
    expect(within(tetCard).getByText("Đã kết thúc")).toBeInTheDocument();
    const boxes = await within(tetCard).findAllByRole("checkbox");
    for (const b of boxes) expect(b).toBeDisabled();
    expect(within(tetCard).queryByRole("button", { name: "Kết mùa Tết Đinh Mùi 2027" })).not.toBeInTheDocument();
    expect(screen.getByText("Chưa có chiến dịch đang chạy hoặc sắp chạy.")).toBeInTheDocument();
  });

  it("starts a planned season at its version, with no body and no Idempotency-Key", async () => {
    const posts: { body: string; ifMatch: string | null; key: string | null }[] = [];
    server.use(
      ...seasonHandlers().handlers,
      http.post(/\/campaigns\/camp-he:activate$/, async ({ request }) => {
        posts.push({ body: await request.text(), ifMatch: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key") });
        return HttpResponse.json({ ...summer, status: "active", version: 2 });
      }),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(within(await card("Mùa hè 2027")).getByRole("button", { name: "Bắt đầu mùa Mùa hè 2027" }));
    expect(await screen.findByText("Đã bắt đầu Mùa hè 2027.")).toBeInTheDocument();
    expect(posts).toEqual([{ body: "", ifMatch: etagOf(1), key: null }]);
  });

  it("closes a season from its dialog and names the checklist items still open", async () => {
    const posts: { ifMatch: string | null; key: string | null }[] = [];
    server.use(
      ...seasonHandlers().handlers,
      http.post(/\/campaigns\/camp-tet:close$/, ({ request }) => {
        posts.push({ ifMatch: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key") });
        return HttpResponse.json({ campaign: { ...tet, status: "closed", version: 4 }, checklist: { total: 3, done: 1, open: CHECKLIST.slice(1) } });
      }),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(within(await card("Tết Đinh Mùi 2027")).getByRole("button", { name: "Kết mùa Tết Đinh Mùi 2027" }));
    const dialog = await screen.findByRole("dialog", { name: "Kết mùa Tết Đinh Mùi 2027" });
    expect(within(dialog).getByText("Kết mùa không xóa tồn, tiền hay nợ. Số liệu của mùa vẫn xem được trong báo cáo.")).toBeInTheDocument();
    expect(within(dialog).getByText("Sau khi kết mùa, chiến dịch không nhận thêm thay đổi.")).toBeInTheDocument();
    expect(within(dialog).getByText("Checklist đã xong").nextElementSibling).toHaveTextContent("1/3");
    expect(within(dialog).getByText("Tồn, tiền, công nợ").nextElementSibling).toHaveTextContent("Không thay đổi");
    await user.click(within(dialog).getByRole("button", { name: "Kết mùa" }));

    expect(await screen.findByText("Đã kết mùa Tết Đinh Mùi 2027. Còn 2 mục checklist chưa xong.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(posts).toEqual([{ ifMatch: etagOf(3), key: null }]);
  });

  it("keeps the close dialog open with the screen's sentence when the status changed", async () => {
    server.use(
      ...seasonHandlers().handlers,
      http.post(/\/campaigns\/camp-tet:close$/, () => apiError(409, "campaign_status_invalid")),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(within(await card("Tết Đinh Mùi 2027")).getByRole("button", { name: "Kết mùa Tết Đinh Mùi 2027" }));
    const dialog = await screen.findByRole("dialog", { name: "Kết mùa Tết Đinh Mùi 2027" });
    await user.click(within(dialog).getByRole("button", { name: "Kết mùa" }));
    expect(await screen.findByText("Trạng thái chiến dịch đã thay đổi. Tải lại rồi thử lại.", {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Kết mùa Tết Đinh Mùi 2027" })).toBeInTheDocument();
  });

  it("leaves a stale close to the app-wide notice: the dialog stays open and no commit toast", async () => {
    server.use(
      ...seasonHandlers().handlers,
      http.post(/\/campaigns\/camp-tet:close$/, () => apiError(412, "precondition_failed")),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(within(await card("Tết Đinh Mùi 2027")).getByRole("button", { name: "Kết mùa Tết Đinh Mùi 2027" }));
    const dialog = await screen.findByRole("dialog", { name: "Kết mùa Tết Đinh Mùi 2027" });
    await user.click(within(dialog).getByRole("button", { name: "Kết mùa" }));
    expect(await screen.findByText(STALE_DATA_MESSAGE, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Kết mùa Tết Đinh Mùi 2027" })).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
  });

  it("leaves a stale start to the app-wide notice, with no commit toast", async () => {
    server.use(
      ...seasonHandlers().handlers,
      http.post(/\/campaigns\/camp-he:activate$/, () => apiError(412, "precondition_failed")),
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    await user.click(within(await card("Mùa hè 2027")).getByRole("button", { name: "Bắt đầu mùa Mùa hè 2027" }));
    expect(await screen.findByText(STALE_DATA_MESSAGE, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(screen.queryByText("Đã bắt đầu Mùa hè 2027.")).not.toBeInTheDocument();
  });

  it("projects the running Tết season's demand per SKU from the API, paged by cursor", async () => {
    const { handlers, demandQueries } = seasonHandlers();
    server.use(...handlers);
    renderApp(ROUTE);

    const table = await screen.findByRole("table", { name: "Nhu cầu hàng mùa Tết theo ngày" });
    expect(screen.getByRole("heading", { name: "Nhu cầu hàng mùa Tết theo ngày", level: 3 })).toBeInTheDocument();
    expect(screen.getByText("Tết Đinh Mùi 2027 · 01/10/2026 – 28/02/2027")).toBeInTheDocument();
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual([
      "Hàng",
      "Đơn đã chốt còn giao",
      "Có thể bán",
      "PO dự kiến về",
      "Ngày về sớm nhất",
      "Thiếu sau khi về",
    ]);
    const rows = within(table).getAllByRole("row");
    expect(cells(at(rows, 1))).toEqual(["Bánh quy bơ hộp 454gBQ454", "180", "120", "50", "15/10/2026", "60từ 20/10/2026"]);
    expect(cells(at(rows, 2))).toEqual(["Kẹo dừa hộp 350gKD350", "200", "500", "0chưa hẹn ngày 30", "—", "0"]);
    expect(screen.getByText("Lượng dự kiến về không được cộng thành tồn thực.")).toBeInTheDocument();
    expect(demandQueries.map((q) => [q.get("limit"), q.get("cursor")])).toEqual([["50", null]]);
  });

  it("loads the next demand page with the cursor the API returned", async () => {
    const cursors: (string | null)[] = [];
    server.use(
      http.get(apiUrl("/campaigns/:id/demand"), ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        cursors.push(cursor);
        return cursor === null
          ? HttpResponse.json({ data: [testDemandRow()], next_cursor: "c-2" })
          : HttpResponse.json({ data: [testDemandRow({ sku_id: "sku-2", code: "MT200", name: "Mứt gừng 200g" })], next_cursor: null });
      }),
      ...seasonHandlers().handlers,
    );
    const user = userEvent.setup();
    renderApp(ROUTE);

    const table = await screen.findByRole("table", { name: "Nhu cầu hàng mùa Tết theo ngày" });
    await user.click(screen.getByRole("button", { name: "Xem thêm" }));
    expect(await within(table).findByText("Mứt gừng 200g")).toBeInTheDocument();
    expect(cursors).toEqual([null, "c-2"]);
  });

  it("says so when there is no campaign yet", async () => {
    server.use(...seasonHandlers({ campaigns: [] }).handlers);
    renderApp(ROUTE);

    expect(await screen.findByText("Chưa có chiến dịch.")).toBeInTheDocument();
    expect(screen.getByText("Chưa có chiến dịch đang chạy hoặc sắp chạy.")).toBeInTheDocument();
  });

  it("shows the reason when the season figures cannot be read", async () => {
    server.use(http.get(apiUrl("/reports/season"), () => apiError(403, "forbidden")), ...seasonHandlers({ campaigns: [tet] }).handlers);
    renderApp(ROUTE);

    const tetCard = await card("Tết Đinh Mùi 2027");
    expect(await within(tetCard).findByText(/^Không tải được số liệu mùa:/, {}, { timeout: 3000 })).toBeInTheDocument();
  });
});
