import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { STALE_DATA_MESSAGE } from "../../../shared/api/api-error";
import { COMMIT_FAILED } from "../../../shared/ui/toast-store";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { scannerServer } from "../../../test/msw/scanner";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf, type SkuListItem } from "../../catalog";
import { UNIT_HOP, testSku } from "../../procurement/testing/fixtures";
import { createFakeStreams } from "../../scanner/testing/fake-event-stream";
import { memorySessionStore, pairedStream, withScanner } from "../../scanner/testing/scanner-harness";
import type { Count } from "../schemas";
import { testCount, testCountLine } from "../testing/fixtures";
import { CountDrawer } from "./count-drawer";

const SKU_BARCODE = "8935000000017";
const SKU_SECOND_BARCODE = "8935000000024";
const CARTON_BARCODE = "18935000000014";

/** What GET /sku-lookup answers for code: the SKU by its code, its two hộp barcodes and its thùng-of-12 barcode. */
function lookupOf(code: string, sku: SkuListItem = testSku()) {
  const [hop, thung] = sku.units;
  const byCode: Record<string, { unit: SkuListItem["units"][number] | undefined; matched_by: "barcode" | "sku_code" }> = {
    [sku.code.toUpperCase()]: { unit: hop, matched_by: "sku_code" },
    [SKU_BARCODE]: { unit: hop, matched_by: "barcode" },
    [SKU_SECOND_BARCODE]: { unit: hop, matched_by: "barcode" },
    [CARTON_BARCODE]: { unit: thung, matched_by: "barcode" },
  };
  const m = byCode[code.toUpperCase()];
  if (!m?.unit) return null;
  return { sku, unit_id: m.unit.unit_id, unit_name: m.unit.unit_name, factor: m.unit.factor, matched_by: m.matched_by };
}

/**
 * Serves the count as it stands, applying each line save to its line, and records each save with
 * the version it was sent at. GET /sku-lookup answers through lookup.
 */
function countServer(initial: Count, lookup: (code: string) => object | null = (code) => lookupOf(code)) {
  let current = initial;
  const saves: Array<{ body: unknown; ifMatch: string | null }> = [];
  server.use(
    http.get(apiUrl("/stock-counts/cnt-1"), () => HttpResponse.json(current)),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
    http.get(apiUrl("/sku-lookup"), ({ request }) => {
      const match = lookup(new URL(request.url).searchParams.get("code") ?? "");
      return match ? HttpResponse.json(match) : apiError(404, "code_not_found");
    }),
    http.put(apiUrl("/stock-counts/cnt-1/lines"), async ({ request }) => {
      const body = (await request.json()) as { lines: Array<{ line_id: string; counted_qty: string }> };
      saves.push({ body, ifMatch: request.headers.get("If-Match") });
      const saved = new Map(body.lines.map((l) => [l.line_id, l.counted_qty]));
      const lines = (current.lines ?? []).map((l) => (saved.has(l.id) ? { ...l, counted_qty: saved.get(l.id) } : l));
      current = testCount({ version: current.version + 1, lines });
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

/** The drawer with a phone paired to this tab (session ss-1, approved); returns the stream its scans arrive on. */
async function renderDrawerWithPhone() {
  const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
  const streams = createFakeStreams();
  renderWithProviders(withScanner(<CountDrawer id="cnt-1" categoryName={() => null} onClose={() => {}} />, streams, memorySessionStore("ss-1")));
  const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
  const stream = await pairedStream(streams, "Pixel 8");
  return { api, stream, drawer };
}

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
    expect(scan).toHaveValue("XYZ");

    await user.clear(scan);
    await user.type(scan, "sp001{Enter}");
    await waitFor(() => expect(counts.saves).toHaveLength(1));
    expect(counts.saves[0]?.body).toEqual({ lines: [{ line_id: "cl-1", counted_qty: "1" }] });
    expect(scan).toHaveValue("");
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
    await waitFor(() => expect(status).toHaveTextContent("Đã cộng 1 – Bánh quy bơ (hộp), thực đếm 1"));
    expect(within(drawer).getByRole("status")).toBe(status);
  });

  it("counts a SKU by any of its barcodes, not only the first", async () => {
    const counts = countServer(testCount());
    server.use(http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku({ barcode: SKU_BARCODE })], next_cursor: null })));
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.type(within(drawer).getByRole("textbox", { name: "Quét mã vạch" }), `${SKU_SECOND_BARCODE}{Enter}`);
    await waitFor(() => expect(counts.saves).toHaveLength(1));
    expect(counts.saves[0]?.body).toEqual({ lines: [{ line_id: "cl-1", counted_qty: "1" }] });
  });

  it("counts a SKU beyond the first 200 of a whole-store count", async () => {
    // The ids sort so that the 201st SKU is the one left out of the SKU details (200 ids at most).
    const n = (i: number) => String(i).padStart(3, "0");
    const lines = Array.from({ length: 201 }, (_, i) => testCountLine({ id: `cl-${n(i + 1)}`, sku_id: `sku-${n(i + 1)}`, sku_code: `SP${n(i + 1)}`, sku_name: `Hàng ${n(i + 1)}` }));
    const last = testSku({ id: "sku-201", code: "SP201", display_name: "Hàng 201" });
    const counts = countServer(testCount({ lines }), (code) => lookupOf(code, last));
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.type(within(drawer).getByRole("textbox", { name: "Quét mã vạch" }), `${SKU_SECOND_BARCODE}{Enter}`);
    await waitFor(() => expect(counts.saves).toHaveLength(1));
    expect(counts.saves[0]?.body).toEqual({ lines: [{ line_id: "cl-201", counted_qty: "1" }] });
  });

  it("adds the base units of a carton to the count", async () => {
    const counts = countServer(testCount({ lines: [testCountLine({ counted_qty: "3" })] }));
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.type(within(drawer).getByRole("textbox", { name: "Quét mã vạch" }), `${CARTON_BARCODE}{Enter}`);
    await waitFor(() => expect(counts.saves).toHaveLength(1));
    expect(counts.saves[0]?.body).toEqual({ lines: [{ line_id: "cl-1", counted_qty: "15" }] });
    expect(within(drawer).getByRole("status")).toHaveTextContent("Đã cộng 12 – Bánh quy bơ (thùng), thực đếm 15");
  });

  it("adds up two scans that both answer before the drawer renders again", async () => {
    // Both lookups are held until the second is asked, so both scans start from the same render.
    let release = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let asked = 0;
    const counts = countServer(testCount());
    server.use(
      http.get(apiUrl("/sku-lookup"), async () => {
        asked += 1;
        if (asked === 2) release();
        await held;
        return HttpResponse.json(lookupOf("SP001"));
      }),
    );
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.type(within(drawer).getByRole("textbox", { name: "Quét mã vạch" }), "SP001{Enter}");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(counts.saves).toHaveLength(2));
    expect(counts.saves.map((s) => s.body)).toEqual([{ lines: [{ line_id: "cl-1", counted_qty: "1" }] }, { lines: [{ line_id: "cl-1", counted_qty: "2" }] }]);
    expect(within(drawer).getByRole("status")).toHaveTextContent("thực đếm 2");
  });

  it("counts three phone scans of one item that arrive back to back", async () => {
    const counts = countServer(testCount());
    const { api, stream, drawer } = await renderDrawerWithPhone();
    expect(within(drawer).getByRole("region", { name: "Quét bằng điện thoại" })).toHaveTextContent("Điện thoại đã kết nối · Pixel 8");
    const scan = within(drawer).getByRole("textbox", { name: "Quét mã vạch" });
    await userEvent.type(scan, "SP0");

    for (const id of ["scan-1", "scan-2", "scan-3"]) stream.emit("scan", { scan_id: id, code: SKU_BARCODE, ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(3));
    expect(api.results.map((r) => r.body)).toEqual(Array.from({ length: 3 }, () => ({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_HOP })));
    await waitFor(() => expect(counts.saves.at(-1)?.body).toEqual({ lines: [{ line_id: "cl-1", counted_qty: "3" }] }));
    expect(within(drawer).getByRole("textbox", { name: "Thực đếm – Bánh quy bơ" })).toHaveValue("3");
    expect(scan).toHaveValue("SP0");
  });

  it("tells the phone the count takes no codes while it is not being edited", async () => {
    const counts = countServer(testCount({ status: "pending_post", lines: [testCountLine({ system_qty: "24", counted_qty: "20", variance_qty: "-4" })] }));
    const { api, stream, drawer } = await renderDrawerWithPhone();
    expect(within(drawer).queryByRole("region", { name: "Quét bằng điện thoại" })).not.toBeInTheDocument();
    stream.emit("scan", { scan_id: "scan-1", code: SKU_BARCODE, ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "not_accepting" });
    expect(counts.saves).toHaveLength(0);
  });

  it("says when a code cannot be looked up", async () => {
    const counts = countServer(testCount());
    server.use(http.get(apiUrl("/sku-lookup"), () => apiError(500, "internal", { message: "Máy chủ lỗi" })));
    const user = userEvent.setup();
    renderDrawer();

    const drawer = await screen.findByRole("dialog", { name: "Kiểm kê KK-0001" });
    await user.type(within(drawer).getByRole("textbox", { name: "Quét mã vạch" }), "SP001{Enter}");
    expect(await screen.findByText(/^Không tra được mã SP001: /)).toBeInTheDocument();
    expect(counts.saves).toHaveLength(0);
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
