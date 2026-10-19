import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { STALE_DATA_MESSAGE } from "../../../shared/api/api-error";
import { COMMIT_FAILED } from "../../../shared/ui/toast-store";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { testSku } from "../../procurement/testing/fixtures";
import type { Count } from "../schemas";
import { testCount, testCountLine } from "../testing/fixtures";
import { CountDrawer } from "./count-drawer";

/** Serves the count as it stands and records each line save with the version it was sent at. */
function countServer(initial: Count) {
  let current = initial;
  const saves: Array<{ body: unknown; ifMatch: string | null }> = [];
  server.use(
    http.get(apiUrl("/stock-counts/cnt-1"), () => HttpResponse.json(current)),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
    http.put(apiUrl("/stock-counts/cnt-1/lines"), async ({ request }) => {
      const body = (await request.json()) as { lines: Array<{ counted_qty: string }> };
      saves.push({ body, ifMatch: request.headers.get("If-Match") });
      current = testCount({ version: current.version + 1, lines: [testCountLine({ counted_qty: body.lines[0]?.counted_qty })] });
      return HttpResponse.json(current);
    }),
  );
  return {
    saves,
    set: (c: Count) => {
      current = c;
    },
  };
}

const renderDrawer = () => renderWithProviders(<CountDrawer id="cnt-1" categoryName={() => null} onClose={() => {}} />);

describe("CountDrawer", () => {
  it("hides the books in a blind count, saves what is typed and finishes the count", async () => {
    const counts = countServer(testCount());
    let finishIfMatch: string | null = null;
    server.use(
      http.post(/\/stock-counts\/cnt-1:finish$/, ({ request }) => {
        finishIfMatch = request.headers.get("If-Match");
        const finished = testCount({ status: "pending_post", version: 4, lines: [testCountLine({ system_qty: "24", counted_qty: "20", variance_qty: "-4" })] });
        counts.set(finished);
        return HttpResponse.json(finished);
      }),
    );
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    expect(await within(drawer).findByText("Ẩn khi đếm mù")).toBeInTheDocument();
    const counted = within(drawer).getByRole("textbox", { name: "Thực đếm – Bánh quy bơ" });
    await user.type(counted, "20");
    await user.tab();
    await waitFor(() => expect(counts.saves).toHaveLength(1));
    expect(counts.saves[0]).toEqual({ body: { lines: [{ line_id: "cl-1", counted_qty: "20" }] }, ifMatch: etagOf(2) });

    await user.click(within(drawer).getByRole("button", { name: "Hoàn tất đếm" }));
    expect(await screen.findByText("Đã hoàn tất đếm. Kiểm tra chênh lệch rồi xác nhận ghi sổ.")).toBeInTheDocument();
    expect(finishIfMatch).toBe(etagOf(3));
    expect(await within(drawer).findByText("-4")).toBeInTheDocument();
    expect(within(drawer).queryByText("Ẩn khi đếm mù")).not.toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Xác nhận & ghi sổ chênh lệch" })).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Đếm lại" })).toBeInTheDocument();
  });

  it("leaves a stale line save to the app-wide notice, but reports any other failure", async () => {
    countServer(testCount());
    const statuses = [412, 422];
    server.use(
      http.put(apiUrl("/stock-counts/cnt-1/lines"), () => {
        const status = statuses.shift() ?? 422;
        return HttpResponse.json({ error: { code: status === 412 ? "precondition_failed" : "validation_failed", message: "refused" } }, { status });
      }),
    );
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    const counted = await within(drawer).findByRole("textbox", { name: "Thực đếm – Bánh quy bơ" });
    await user.type(counted, "20");
    await user.tab();
    await waitFor(() => expect(statuses).toHaveLength(1));
    await user.clear(counted);
    await user.type(counted, "21");
    await user.tab();
    expect(await screen.findAllByText(COMMIT_FAILED)).toHaveLength(1);
    // The app-wide stale notice shows once, with no "Chưa ghi sổ." copy of it.
    expect(screen.getAllByText(STALE_DATA_MESSAGE, { exact: false })).toHaveLength(1);
  });

  it("adds one to the line of a scanned code and says when the code is not in the count", async () => {
    const counts = countServer(testCount());
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    const scan = within(drawer).getByRole("textbox", { name: "Quét mã vạch" });
    await user.type(scan, "XYZ{Enter}");
    expect(await screen.findByText("Không có mã XYZ trong phiên kiểm kê.")).toBeInTheDocument();

    await user.clear(scan);
    await user.type(scan, "sp001{Enter}");
    await waitFor(() => expect(counts.saves).toHaveLength(1));
    expect(counts.saves[0]?.body).toEqual({ lines: [{ line_id: "cl-1", counted_qty: "1" }] });
  });

  it("says which line a scan added to through a status region that is there before the first scan", async () => {
    countServer(testCount());
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    const scan = within(drawer).getByRole("textbox", { name: "Quét mã vạch" });
    const status = within(drawer).getByRole("status");
    expect(status).toBeEmptyDOMElement();

    await user.type(scan, "sp001{Enter}");
    expect(status).toHaveTextContent("Đã cộng 1 – Bánh quy bơ, thực đếm 1");
    expect(within(drawer).getByRole("status")).toBe(status);
  });

  it("names each line's fields after its product, color and lot so two lines of one product differ", async () => {
    countServer(
      testCount({
        lines: [
          testCountLine({ color_id: "c-do", color_code: "red", color_name: "Đỏ", track_lot: true }),
          testCountLine({ id: "cl-2", color_id: "c-vang", color_code: "yellow", color_name: "Vàng", lot_id: "lot-1", lot_no: "L01" }),
        ],
      }),
    );
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    expect(await within(drawer).findByRole("textbox", { name: "Thực đếm – Bánh quy bơ · Đỏ" })).toBeInTheDocument();
    expect(within(drawer).getByRole("textbox", { name: "Số lô – Bánh quy bơ · Đỏ" })).toBeInTheDocument();
    expect(within(drawer).getByRole("textbox", { name: "Thực đếm – Bánh quy bơ · Vàng · lô L01" })).toBeInTheDocument();
  });

  it("announces that the count is loading", async () => {
    server.use(http.get(apiUrl("/stock-counts/cnt-1"), () => new Promise<never>(() => {})));
    renderDrawer();
    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê" });
    expect(within(drawer).getByRole("status")).toHaveTextContent("Đang tải…");
  });

  it("shows a count finished without differences when its posting fails, and posts it from there", async () => {
    const counts = countServer(testCount({ lines: [testCountLine({ counted_qty: "24" })] }));
    let finishes = 0;
    const posts: (string | null)[] = [];
    server.use(
      http.post(/\/stock-counts\/cnt-1:finish$/, () => {
        finishes += 1;
        const finished = testCount({ status: "pending_post", version: 4, lines: [testCountLine({ system_qty: "24", counted_qty: "24", variance_qty: "0" })] });
        counts.set(finished);
        return HttpResponse.json(finished);
      }),
      http.post(/\/stock-counts\/cnt-1:post$/, ({ request }) => {
        posts.push(request.headers.get("If-Match"));
        if (posts.length === 1) return HttpResponse.error();
        const posted = testCount({ status: "posted", version: 5 });
        counts.set(posted);
        return HttpResponse.json(posted);
      }),
    );
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.click(await within(drawer).findByRole("button", { name: "Hoàn tất đếm" }));

    expect(await screen.findByText(/nhưng chưa ghi sổ/)).toBeInTheDocument();
    expect(screen.queryByText("Số đếm khớp sổ, đã đóng phiên.")).not.toBeInTheDocument();
    await user.click(await within(drawer).findByRole("button", { name: "Xác nhận & ghi sổ chênh lệch" }));

    expect(await screen.findByText("Đã ghi sổ chênh lệch kiểm kê.")).toBeInTheDocument();
    expect(finishes).toBe(1);
    expect(posts).toEqual([etagOf(4), etagOf(4)]);
  });
});
