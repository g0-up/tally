import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { etagOf } from "../../catalog";
import { SUPPLIER_ID, testOrder, testOrderLine, testReceipt, testSku } from "../testing/fixtures";

const draft = testOrder({ id: "po-2", doc_no: "PO-0002", status: "draft" });
const late = testOrder({ id: "po-3", doc_no: "PO-0003", expected_date: "2026-10-01" });
const onTime = testOrder();

/** `GET /purchase-orders`: the list, or the rows whose number matches a `?q=` doc-number search. */
const ordersAre = (rows: unknown[]) =>
  http.get(apiUrl("/purchase-orders"), ({ request }) => {
    const q = new URL(request.url).searchParams.get("q") ?? "";
    const data = q.startsWith("PO-") ? rows.filter((r) => (r as { doc_no: string }).doc_no === q) : rows;
    return HttpResponse.json({ data, next_cursor: null });
  });

const rowOf = (docNo: string) => screen.getByRole("button", { name: docNo }).closest("tr") as HTMLElement;

describe("Đơn nhập page", () => {
  it("lists orders with their state and confirms a draft at its version", async () => {
    let ifMatch: string | null = null;
    server.use(
      ordersAre([draft, late, onTime]),
      http.post(/\/purchase-orders\/po-2:confirm$/, ({ request }) => {
        ifMatch = request.headers.get("If-Match");
        return HttpResponse.json({ id: "po-2", doc_no: "PO-0002", version: 4, status: "confirmed" });
      }),
    );
    const user = userEvent.setup();
    renderApp("/purchasing/orders");

    await screen.findByRole("button", { name: "PO-0003" });
    expect(within(rowOf("PO-0003")).getByText("Quá hạn giao")).toBeInTheDocument();
    expect(within(rowOf("PO-0001")).queryByText("Quá hạn giao")).not.toBeInTheDocument();
    expect(within(rowOf("PO-0001")).getByText("Đã chốt")).toBeInTheDocument();
    expect(within(rowOf("PO-0001")).getByRole("button", { name: "Nhận hàng" })).toBeInTheDocument();
    expect(within(rowOf("PO-0002")).getByText("Nháp")).toBeInTheDocument();
    expect(within(rowOf("PO-0002")).queryByRole("button", { name: "Nhận hàng" })).not.toBeInTheDocument();

    await user.click(within(rowOf("PO-0002")).getByRole("button", { name: "Chốt đơn" }));
    expect(await screen.findByText(/: Đã chốt\./)).toBeInTheDocument();
    expect(ifMatch).toBe(etagOf(3));
  });

  it("opens the order of a `?doc=` link with its receipts and earmarked advances", async () => {
    server.use(
      ordersAre([onTime]),
      http.get(apiUrl("/purchase-orders/po-1"), () => HttpResponse.json({ ...onTime, lines: [testOrderLine()] })),
      http.get(apiUrl("/receipts"), () => HttpResponse.json({ data: [testReceipt({ purchase_order_id: "po-1" })], next_cursor: null })),
      http.get(apiUrl("/receipts/rcv-1"), () => HttpResponse.json(testReceipt())),
      http.get(apiUrl(`/debts/parties/${SUPPLIER_ID}`), () =>
        HttpResponse.json({
          advances: [{ id: "pay-1", doc_no: "PC-0001", amount: "500000", allocated_amount: "0", source_type: "purchase", source_id: "po-1", status: "posted" }],
        }),
      ),
      http.get(apiUrl("/reasons"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
    );
    const user = userEvent.setup();
    const { router } = renderApp("/purchasing/orders?doc=PO-0001");

    const drawer = await screen.findByRole("dialog", { name: "Đơn nhập PO-0001" });
    expect(within(drawer).getByRole("button", { name: "Nhận hàng" })).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Ứng trước NCC" })).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Đóng phần còn thiếu" })).toBeInTheDocument();
    expect(await within(drawer).findByText("Chưa đối chiếu")).toBeInTheDocument();
    expect(await within(drawer).findByText(/Tổng ứng trước 500\.000/)).toBeInTheDocument();

    await user.click(within(drawer).getByRole("button", { name: "PN-0001" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/receipts"));
    expect(router.state.location.search).toBe("?doc=PN-0001");
  });

  it("says when no order carries the linked number, and drops the link on close", async () => {
    server.use(ordersAre([onTime]));
    const user = userEvent.setup();
    const { router } = renderApp("/purchasing/orders?doc=PO-9999");

    const drawer = await screen.findByRole("dialog", { name: "Đơn nhập" });
    expect(within(drawer).getByText("Không tìm thấy PO-9999.")).toBeInTheDocument();
    await user.click(within(drawer).getByRole("button", { name: "Đóng" }));
    await waitFor(() => expect(router.state.location.search).toBe(""));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
