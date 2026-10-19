import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp, renderWithProviders } from "../../../test/render";
import { testLot } from "../testing/fixtures";
import { LotTable, type LotTableProps } from "./lot-table";

const normal = testLot();
const expiring = testLot({ id: "lot-2", lot_no: "L2602", state: "expiring", days_to_expiry: 20 });
const expired = testLot({ id: "lot-3", lot_no: "L2603", state: "expired", days_to_expiry: -3 });
const quarantined = testLot({ id: "lot-4", lot_no: "L2604", state: "quarantine", is_quarantine: true, qty: "6" });

const rowOf = (lotNo: string) => screen.getByText(lotNo).closest("tr") as HTMLElement;

function renderTable(over: Partial<LotTableProps> = {}) {
  const props: LotTableProps = { rows: [normal, expiring, expired, quarantined], busy: false, onPass: vi.fn(), onReject: vi.fn(), onDispose: vi.fn(), onClearance: vi.fn(), ...over };
  renderWithProviders(<LotTable {...props} />);
  return props;
}

describe("LotTable", () => {
  it("offers the action each lot state allows", async () => {
    const user = userEvent.setup();
    const props = renderTable();

    expect(within(rowOf("L2601")).queryByRole("button")).not.toBeInTheDocument();
    expect(within(rowOf("L2602")).getByText("Sắp hết hạn")).toBeInTheDocument();
    await user.click(within(rowOf("L2602")).getByRole("button", { name: "Xả hàng" }));
    expect(props.onClearance).toHaveBeenCalled();

    expect(within(rowOf("L2603")).getByText("Quá 3 ngày")).toBeInTheDocument();
    await user.click(within(rowOf("L2603")).getByRole("button", { name: "Tiêu hủy" }));
    expect(props.onDispose).toHaveBeenCalledWith(expired);

    const q = rowOf("L2604");
    expect(within(q).getByText("Cách ly – chờ kiểm tra")).toBeInTheDocument();
    await user.click(within(q).getByRole("button", { name: "Đạt – cho bán" }));
    expect(props.onPass).toHaveBeenCalledWith(quarantined);
    await user.click(within(q).getByRole("button", { name: "Không đạt – hủy" }));
    expect(props.onReject).toHaveBeenCalledWith(quarantined);
  });

  it("holds the posting buttons while a posting is in flight", () => {
    renderTable({ busy: true });
    expect(within(rowOf("L2604")).getByRole("button", { name: "Đạt – cho bán" })).toBeDisabled();
    expect(within(rowOf("L2603")).getByRole("button", { name: "Tiêu hủy" })).toBeDisabled();
    expect(within(rowOf("L2602")).getByRole("button", { name: "Xả hàng" })).toBeEnabled();
  });
});

describe("Lô & hạn dùng page", () => {
  it("lists the quarantine chip from the URL and passes a lot back to sellable stock", async () => {
    let state: string | null = null;
    let body: unknown = null;
    server.use(
      http.get(apiUrl("/lots"), ({ request }) => {
        state = new URL(request.url).searchParams.get("state");
        return HttpResponse.json({ data: [quarantined], next_cursor: null });
      }),
      http.post(apiUrl("/stock-adjustments"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "adj-1", doc_no: "DC-0001", kind: "quarantine_release", business_date: "2026-10-12", status: "posted" });
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/lots?loc=quarantine");

    await screen.findByText("L2604");
    expect(state).toBe("quarantine");
    expect(screen.getByRole("button", { name: "Khu cách ly" })).toHaveAttribute("aria-pressed", "true");
    await user.click(within(rowOf("L2604")).getByRole("button", { name: "Đạt – cho bán" }));
    expect(await screen.findByText(/Hàng đã trở lại tồn bán được\./)).toBeInTheDocument();
    await waitFor(() => expect(body).toEqual({ kind: "quarantine_release", note: "Đạt kiểm tra", lines: [{ sku_id: "sku-1", qty: "6", lot_id: "lot-4", uncolored: true }] }));
  });

  it("writes an expired lot off whole, citing the expiry reason, with no revenue", async () => {
    let body: unknown = null;
    server.use(
      http.get(apiUrl("/lots"), () => HttpResponse.json({ data: [expired], next_cursor: null })),
      http.get(apiUrl("/reasons"), () =>
        HttpResponse.json({
          data: [
            { id: "rsn-dmg", kind: "damage", name: "Bể vỡ khi vận chuyển", active: true },
            { id: "rsn-exp", kind: "disposal", name: "Hết hạn sử dụng", active: true },
          ],
        }),
      ),
      http.post(apiUrl("/stock-adjustments"), async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: "adj-2", doc_no: "DC-0003", kind: "disposal", business_date: "2026-10-12", status: "posted" });
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/lots");

    await screen.findByText("L2603");
    await user.click(within(rowOf("L2603")).getByRole("button", { name: "Tiêu hủy" }));
    const dialog = await screen.findByRole("dialog", { name: "Tiêu hủy lô hết hạn" });
    expect(within(dialog).getByText("Bánh quy bơ · lô L2603 · 24 hộp")).toBeInTheDocument();
    expect(within(dialog).getByText("Không phát sinh")).toBeInTheDocument();
    await within(dialog).findByRole("option", { name: "Hết hạn sử dụng" });
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ tiêu hủy" }));

    expect(await screen.findByText(/Đã ghi tiêu hủy\./)).toBeInTheDocument();
    expect(body).toEqual({ kind: "disposal", reason_id: "rsn-exp", lines: [{ sku_id: "sku-1", qty: "24", lot_id: "lot-3", uncolored: true }] });
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
  });
});
