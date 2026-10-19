import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { at, CUSTOMER_ID, testLedger, testPaymentDetail, testPaymentRow, testSettlement } from "../testing/fixtures";
import type { PaymentRow } from "../schemas";

const customerAdvance = testPaymentRow({ amount: "2000000", allocated_amount: "500000", unallocated: "1500000", purpose: "deposit" });
const supplierAdvance = testPaymentRow({
  id: "pay-2",
  doc_no: "PC-0004",
  direction: "out",
  party_id: "sup-1",
  party_name: "NCC Bánh Kẹo Hải Hà",
  method_name: "Chuyển khoản",
  amount: "800000",
  unallocated: "800000",
  business_date: "2026-10-05",
});
const refund = testPaymentRow({ id: "pay-4", doc_no: "PC-0007", direction: "out", purpose: "refund", unallocated: "300000" });

/** `GET /payments`: the unallocated list, or the payment a `doc_no` names. */
function paymentHandlers(listed: PaymentRow[] = [customerAdvance, supplierAdvance, refund], seen: URLSearchParams[] = []) {
  return [
    http.get(apiUrl("/payments"), ({ request }) => {
      const params = new URL(request.url).searchParams;
      seen.push(params);
      const docNo = params.get("doc_no");
      if (docNo !== null) return HttpResponse.json({ data: listed.filter((p) => p.doc_no === docNo), next_cursor: null });
      return HttpResponse.json({ data: listed, next_cursor: null });
    }),
  ];
}

describe("AdvancesPage", () => {
  it("lists the unallocated payments of both ledgers with their party, from one list read", async () => {
    const seen: URLSearchParams[] = [];
    let ledgerReads = 0;
    server.use(
      ...paymentHandlers(undefined, seen),
      http.get(apiUrl("/debts/parties/:id"), () => {
        ledgerReads += 1;
        return HttpResponse.json(testLedger());
      }),
    );
    renderApp("/debts/advances");

    expect(
      await screen.findByText("Tiền khách đặt cọc/ứng trước và tiền cửa hàng ứng trước NCC chưa phân bổ. Không phải doanh thu hay chi phí; phân bổ vào chứng từ khi có nghĩa vụ."),
    ).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Ứng trước & cọc" });
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(3));
    const rows = within(table).getAllByRole("row");
    const first = at(rows, 1);
    const second = at(rows, 2);
    expect(first).toHaveTextContent("PT-0001");
    expect(first).toHaveTextContent("Tạp hóa Minh Châu");
    expect(first).toHaveTextContent("Khách ứng trước");
    expect(first).toHaveTextContent("Đặt cọc");
    expect(first).toHaveTextContent("1.500.000");
    expect(within(first).getByRole("button", { name: "Phân bổ PT-0001" })).toHaveClass("btn-sm");
    expect(second).toHaveTextContent("PC-0004");
    expect(second).toHaveTextContent("NCC Bánh Kẹo Hải Hà");
    expect(second).toHaveTextContent("Cửa hàng ứng trước NCC");
    expect(within(table).queryByText("PC-0007")).not.toBeInTheDocument();
    expect(at(seen, 0).get("unallocated")).toBe("true");
    expect(ledgerReads).toBe(0);
  });

  it("reads the next page of advances on request", async () => {
    const next = testPaymentRow({ id: "pay-9", doc_no: "PT-0009", unallocated: "100000" });
    server.use(
      http.get(apiUrl("/payments"), ({ request }) =>
        new URL(request.url).searchParams.get("cursor") === "c2"
          ? HttpResponse.json({ data: [next], next_cursor: null })
          : HttpResponse.json({ data: [customerAdvance], next_cursor: "c2" }),
      ),
    );
    const user = userEvent.setup();
    renderApp("/debts/advances");

    const table = await screen.findByRole("table", { name: "Ứng trước & cọc" });
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(2));
    await user.click(screen.getByRole("button", { name: /^Xem thêm/ }));
    expect(await within(table).findByText("PT-0009")).toBeInTheDocument();
  });

  it("opens a linked receipt by its number with the documents it settled, and its allocation", async () => {
    const seen: URLSearchParams[] = [];
    const allocated = testPaymentDetail({
      amount: "2000000",
      allocated_amount: "500000",
      unallocated: "1500000",
      purpose: "deposit",
      note: "Cọc đơn Tết",
      settlements: [testSettlement()],
    });
    server.use(
      ...paymentHandlers(undefined, seen),
      http.get(apiUrl("/payments/pay-1"), () => HttpResponse.json(allocated)),
      http.get(apiUrl(`/debts/parties/${CUSTOMER_ID}`), () => HttpResponse.json(testLedger())),
    );
    const user = userEvent.setup();
    renderApp("/debts/advances?doc=PT-0001");

    const drawer = await screen.findByRole("dialog", { name: "Phiếu thu PT-0001" });
    expect(await within(drawer).findByText("Tạp hóa Minh Châu · 01/10/2026 · Tiền mặt")).toBeInTheDocument();
    expect(within(drawer).getByText("Đã ghi sổ")).toBeInTheDocument();
    expect(within(drawer).getByText("Đã phân bổ").nextSibling).toHaveTextContent("500.000");
    expect(within(drawer).getByText("Chưa phân bổ").nextSibling).toHaveTextContent("1.500.000");
    const settled = within(drawer).getByRole("table", { name: "Chứng từ PT-0001 đã phân bổ" });
    expect(at(within(settled).getAllByRole("row"), 1)).toHaveTextContent("SO-0001");
    expect(at(within(settled).getAllByRole("row"), 1)).toHaveTextContent("500.000");
    expect(within(drawer).getByText("Cọc đơn Tết")).toBeInTheDocument();
    expect(seen.find((p) => p.get("doc_no") === "PT-0001")).toBeDefined();

    await user.click(within(drawer).getByRole("button", { name: "Phân bổ" }));
    const dialog = await screen.findByRole("dialog", { name: "Phân bổ PT-0001" });
    expect(within(dialog).getByText("Còn 1.500.000 ₫ chưa phân bổ của Tạp hóa Minh Châu.")).toBeInTheDocument();
  });

  it("marks an allocation that was reversed and the negative row that reversed it", async () => {
    const detail = testPaymentDetail({
      settlements: [testSettlement(), testSettlement({ id: "set-2", amount: "-500000", business_date: "2026-10-03", reversal_of: "set-1", posted_at: "2026-10-03T03:00:00Z" })],
    });
    server.use(...paymentHandlers(), http.get(apiUrl("/payments/pay-1"), () => HttpResponse.json(detail)));
    renderApp("/debts/advances?doc=PT-0001");

    const drawer = await screen.findByRole("dialog", { name: "Phiếu thu PT-0001" });
    const settled = await within(drawer).findByRole("table", { name: "Chứng từ PT-0001 đã phân bổ" });
    const [original, reversal] = [at(within(settled).getAllByRole("row"), 1), at(within(settled).getAllByRole("row"), 2)];
    expect(original).toHaveTextContent("SO-0001Đã đảo");
    expect(original).not.toHaveTextContent("-500.000");
    expect(reversal).toHaveTextContent("SO-0001Đảo phân bổ");
    expect(reversal).toHaveTextContent("-500.000");
  });

  it("opens a fully allocated payment without the allocation", async () => {
    const spent = testPaymentDetail({ id: "pay-3", doc_no: "PC-0005", direction: "out", purpose: "settlement", allocated_amount: "1000000", unallocated: "0" });
    server.use(
      ...paymentHandlers([testPaymentRow({ id: "pay-3", doc_no: "PC-0005", direction: "out", unallocated: "0" })]),
      http.get(apiUrl("/payments/pay-3"), () => HttpResponse.json(spent)),
    );
    renderApp("/debts/advances?doc=PC-0005");

    const drawer = await screen.findByRole("dialog", { name: "Phiếu chi PC-0005" });
    expect(await within(drawer).findByText("Không phân bổ.")).toBeInTheDocument();
    expect(within(drawer).queryByRole("button", { name: "Phân bổ" })).not.toBeInTheDocument();
  });

  it("says when no payment has the linked number", async () => {
    server.use(...paymentHandlers());
    renderApp("/debts/advances?doc=PT-0099");
    const missing = await screen.findByRole("dialog", { name: "Phiếu thu PT-0099" });
    expect(await within(missing).findByText("Không tìm thấy PT-0099.")).toBeInTheDocument();
  });

  it("says when nothing is waiting for allocation", async () => {
    server.use(...paymentHandlers([]));
    renderApp("/debts/advances");
    expect(await screen.findByText("Không có khoản ứng trước chưa phân bổ.")).toBeInTheDocument();
  });
});
