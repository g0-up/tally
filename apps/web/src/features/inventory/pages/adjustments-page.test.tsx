import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { COLOR_RED } from "../testing/fixtures";

// A row of `GET /stock-adjustments`: no lines, but their count, totals and the first three.
const adjustment = (id: string, docNo: string, over: Record<string, unknown> = {}) => ({
  id,
  doc_no: docNo,
  kind: "damage",
  reason_id: "rsn-1",
  stock_count_id: null,
  business_date: "2026-10-12",
  note: null,
  status: "posted",
  line_count: 1,
  total_base_qty: "2",
  value_in: "0",
  value_out: "18000",
  line_preview: [{ id: `${id}-l1`, name: "Bánh quy bơ", variant_label: "", color_id: null, lot_no: null }],
  ...over,
});

const preview = (n: number) => ({ id: `pl-${n}`, name: `Kẹo dẻo ${n}`, variant_label: "Hộp 200g", color_id: n === 1 ? COLOR_RED : null, lot_no: n === 2 ? "L2410" : null });

const ROWS = [
  adjustment("adj-1", "DC-0001"),
  adjustment("adj-2", "DC-0002", {
    kind: "internal_use",
    reason_id: null,
    note: "Dùng trưng bày",
    line_count: 5,
    total_base_qty: "12",
    value_out: "96000",
    line_preview: [preview(1), preview(2), preview(3)],
  }),
  // A move into quarantine takes out the value it brings in.
  adjustment("adj-3", "DC-0003", { kind: "quarantine_in", reason_id: null, note: "Nghi hỏng", value_in: "45000", value_out: "45000" }),
  // A count's lines can go both ways.
  adjustment("adj-4", "DC-0004", { kind: "count", reason_id: null, note: "Kiểm kê KK-0001", value_in: "20000", value_out: "50000" }),
];

/** `GET /stock-adjustments` as the server answers it: every row, or the one a `doc_no` names. */
function adjustmentApi() {
  const queries: URLSearchParams[] = [];
  const reads = { details: 0 };
  server.use(
    http.get(apiUrl("/stock-adjustments"), ({ request }) => {
      const query = new URL(request.url).searchParams;
      queries.push(query);
      const docNo = query.get("doc_no");
      return HttpResponse.json({ data: docNo === null ? ROWS : ROWS.filter((a) => a.doc_no === docNo), next_cursor: null });
    }),
    http.get(apiUrl("/stock-adjustments/:id"), () => {
      reads.details += 1;
      return HttpResponse.json(ROWS[0]);
    }),
    http.get(apiUrl("/reasons"), () => HttpResponse.json({ data: [{ id: "rsn-1", kind: "damage", name: "Bể vỡ khi vận chuyển", active: true }] })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [{ id: COLOR_RED, code: "do", name: "Đỏ", sort_order: 1, active: true }] })),
  );
  return { queries, reads };
}

const rowOf = (docNo: string) => screen.getByText(docNo).closest("tr") as HTMLElement;

describe("Điều chỉnh kho page", () => {
  it("asks the server for a linked adjustment by its number and shows every one again on request", async () => {
    const { queries, reads } = adjustmentApi();
    const user = userEvent.setup();
    const { router } = renderApp("/inventory/adjustments?doc=DC-0002");

    const row = (await screen.findByText("Dùng trưng bày")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Dùng nội bộ")).toBeInTheDocument();
    expect(screen.getByText("Phiếu DC-0002")).toBeInTheDocument();
    expect(screen.queryByText("DC-0001")).not.toBeInTheDocument();
    expect(queries.every((q) => q.get("doc_no") === "DC-0002")).toBe(true);

    await user.click(screen.getByRole("button", { name: "Xem tất cả" }));
    await waitFor(() => expect(router.state.location.search).toBe(""));
    expect(await screen.findByText("Bể vỡ khi vận chuyển")).toBeInTheDocument();
    expect(queries.at(-1)?.has("doc_no")).toBe(false);
    expect(reads.details).toBe(0);
  });

  it("shows each adjustment's first lines, total and value from the list read alone", async () => {
    const { reads } = adjustmentApi();
    renderApp("/inventory/adjustments");

    await screen.findByText("DC-0004");
    const cells = (docNo: string) => within(rowOf(docNo)).getAllByRole("cell");

    const many = cells("DC-0002");
    expect(many[2]).toHaveTextContent("Kẹo dẻo 1 · Hộp 200g");
    expect(within(many[2] as HTMLElement).getByText("Đỏ")).toBeInTheDocument();
    expect(many[2]).toHaveTextContent("Lô L2410");
    expect(many[2]).toHaveTextContent("Kẹo dẻo 3 · Hộp 200g");
    expect(within(many[2] as HTMLElement).getByText("và 2 dòng khác")).toBeInTheDocument();
    expect(many[3]).toHaveTextContent(/^12$/);
    expect(many[5]).toHaveTextContent(/^96\.000\s₫$/);

    const one = cells("DC-0001");
    expect(one[2]).toHaveTextContent(/^Bánh quy bơ$/);
    expect(one[5]).toHaveTextContent(/^18\.000\s₫$/);
    expect(cells("DC-0003")[5]).toHaveTextContent(/^45\.000\s₫$/);
    expect(cells("DC-0004")[5]).toHaveTextContent(/^\+20\.000\s₫−50\.000\s₫$/);
    expect(reads.details).toBe(0);
  });

  it("says when no adjustment has the linked number", async () => {
    adjustmentApi();
    renderApp("/inventory/adjustments?doc=DC-0404");
    expect(await screen.findByText("Không tìm thấy DC-0404.")).toBeInTheDocument();
  });
});
