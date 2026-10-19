import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { at, CUSTOMER_ID, testAdjustmentDetail, testLedger, testOffsetDetail, testPayment } from "../testing/fixtures";

/** Cell texts of a row, money with a plain space before ₫. */
const cells = (row: HTMLElement) => within(row).getAllByRole("cell").map((c) => (c.textContent ?? "").replace(/\s/g, " "));

const buckets = (over: Record<string, string>) => ({ current: "0", d1_30: "0", d31_60: "0", d61_90: "0", over_90: "0", total: "0", ...over });

const parties = [
  { party_id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", phone: "0903123456", direction: "receivable", open: "4500000", overdue: "4500000", oldest_overdue_due: "2026-09-05", open_documents: 2, advance: "0" },
  { party_id: "cus-2", name: "Shop Quà Tết An Khang", phone: "0912000111", direction: "receivable", open: "0", overdue: "0", oldest_overdue_due: null, open_documents: 0, advance: "750000" },
];

const ledgerHandlers = [
  http.get(apiUrl("/debts/parties"), ({ request }) => {
    expect(new URL(request.url).searchParams.get("direction")).toBe("receivable");
    return HttpResponse.json({ data: parties, next_cursor: null });
  }),
  http.get(apiUrl("/debts/aging"), ({ request }) => {
    expect(new URL(request.url).searchParams.get("as_of")).toBe("2026-10-12");
    return HttpResponse.json({
      direction: "receivable",
      as_of: "2026-10-12",
      rows: [{ party_id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", buckets: buckets({ d1_30: "4000000", d61_90: "300000", over_90: "200000", total: "4500000" }) }],
      totals: buckets({ d1_30: "4000000", d61_90: "300000", over_90: "200000", total: "4500000" }),
    });
  }),
];

describe("ReceivablesPage", () => {
  it("lists balances by age with the advance kept apart, and totals them", async () => {
    server.use(...ledgerHandlers);
    renderApp("/debts/receivables");

    expect(
      await screen.findByText(
        "Tuổi nợ tính theo hạn của phần chưa thanh toán, tại ngày 12/10/2026. Tiền khách ứng trước trình bày riêng, không gộp thành nợ âm. Phải thu và phải trả của cùng một đối tác không tự bù trừ.",
      ),
    ).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Sổ phải thu" });
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual([
      "Khách hàng",
      "Khách còn nợ",
      "Trong hạn",
      "Quá 1–30",
      "Quá 31–60",
      "Quá > 60",
      "Tiền khách ứng trước",
      "",
    ]);
    const rows = within(table).getAllByRole("row");
    const minhChau = at(rows, 1);
    const anKhang = at(rows, 2);
    const footer = at(rows, 3);
    expect(cells(minhChau).slice(0, 7)).toEqual(["Tạp hóa Minh Châu0903123456", "4.500.000 ₫", "0 ₫", "4.000.000 ₫", "0 ₫", "500.000 ₫", "0 ₫"]);
    expect(cells(anKhang).slice(1, 7)).toEqual(["0 ₫", "0 ₫", "0 ₫", "0 ₫", "0 ₫", "750.000 ₫"]);
    expect(cells(footer).slice(0, 7)).toEqual(["Tổng", "4.500.000 ₫", "0 ₫", "4.000.000 ₫", "0 ₫", "500.000 ₫", "750.000 ₫"]);
    expect(within(minhChau).getByRole("button", { name: /^Thu tiền / })).toBeInTheDocument();
    expect(within(minhChau).getByRole("button", { name: /^Bảng kê / })).toBeInTheDocument();
  });

  it("adds every active customer under the Tất cả khách chip", async () => {
    server.use(
      ...ledgerHandlers,
      http.get(apiUrl("/customers"), () =>
        HttpResponse.json({
          data: [
            { id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", phone: "0903123456", customer_group_name: "Đại lý cấp 2", credit_limit: "50000000" },
            { id: "cus-3", name: "Văn phòng Sao Mai", phone: null, customer_group_name: "Doanh nghiệp", credit_limit: null },
          ],
          next_cursor: null,
        }),
      ),
    );
    const user = userEvent.setup();
    renderApp("/debts/receivables");

    const table = await screen.findByRole("table", { name: "Sổ phải thu" });
    expect(within(table).queryByText("Văn phòng Sao Mai")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Tất cả khách" }));
    expect(await within(await screen.findByRole("table", { name: "Sổ phải thu" })).findByText("Văn phòng Sao Mai")).toBeInTheDocument();
    expect(screen.getByText("Đại lý cấp 2 · hạn mức 50.000.000 ₫")).toBeInTheDocument();
  });

  it("opens the customer's ledger from ?party with credit left, overdue documents and notes", async () => {
    server.use(
      ...ledgerHandlers,
      http.get(apiUrl(`/debts/parties/${CUSTOMER_ID}`), () =>
        HttpResponse.json(testLedger({ advances: [testPayment({ amount: "500000", unallocated: "500000" })] })),
      ),
      http.get(apiUrl(`/customers/${CUSTOMER_ID}`), () =>
        HttpResponse.json({ id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", customer_group_name: "Đại lý cấp 2", credit_limit: "10000000", is_supplier: true }),
      ),
      http.get(apiUrl(`/parties/${CUSTOMER_ID}/collection-notes`), () =>
        HttpResponse.json({
          data: [{ id: "cn-1", party_id: CUSTOMER_ID, direction: "receivable", kind: "promise", body: "Khách hẹn cuối tuần", promised_date: "2026-10-17", channel: "phone", created_at: "2026-10-10T03:00:00Z" }],
          next_cursor: null,
        }),
      ),
    );
    renderApp(`/debts/receivables?party=${CUSTOMER_ID}`);

    const drawer = await screen.findByRole("dialog", { name: "Tạp hóa Minh Châu" });
    expect(await within(drawer).findByText("Đại lý cấp 2 · 0903123456 · Sổ phải thu")).toBeInTheDocument();
    const impact = (label: string) => within(drawer).getByText(label).parentElement;
    expect(impact("Khách còn nợ")).toHaveTextContent("4.500.000 ₫");
    expect(impact("Ứng trước chưa phân bổ")).toHaveTextContent("500.000 ₫");
    expect(impact("Số dư ròng (giải thích)")).toHaveTextContent("4.000.000 ₫");
    expect(impact("Hạn mức còn lại")).toHaveTextContent("5.500.000 ₫");
    expect(within(drawer).getByText("Đối tác này vừa là khách vừa là NCC. Hai sổ tách biệt; bù trừ chỉ thực hiện bằng biên bản.")).toBeInTheDocument();
    expect(within(drawer).getByText("Quá hạn 27 ngày")).toBeInTheDocument();
    expect(within(drawer).getByText("Quá hạn 37 ngày")).toBeInTheDocument();
    expect(within(drawer).getAllByRole("button", { name: /^Giảm\/xóa nợ / }).map((b) => b.getAttribute("aria-label"))).toEqual(["Giảm/xóa nợ SO-0001", "Giảm/xóa nợ SO-0002"]);
    expect(within(drawer).getByText("PT-0001")).toBeInTheDocument();
    expect(await within(drawer).findByText("Khách hẹn cuối tuần · hẹn trả 17/10/2026")).toBeInTheDocument();
  });

  it("opens a debt adjustment by its number with its reason and the document it reduced", async () => {
    let lookedUp: string | null = null;
    server.use(
      ...ledgerHandlers,
      http.get(apiUrl("/debt-adjustments"), ({ request }) => {
        lookedUp = new URL(request.url).searchParams.get("doc_no");
        return HttpResponse.json({ data: [{ id: "adj-1" }], next_cursor: null });
      }),
      http.get(apiUrl("/debt-adjustments/adj-1"), () => HttpResponse.json(testAdjustmentDetail())),
    );
    renderApp("/debts/receivables?doc=DN-0001");

    const drawer = await screen.findByRole("dialog", { name: "Điều chỉnh công nợ DN-0001" });
    expect(await within(drawer).findByText("Tạp hóa Minh Châu · 10/10/2026")).toBeInTheDocument();
    expect(within(drawer).getByText("Loại").nextSibling).toHaveTextContent("Xóa nợ");
    expect(within(drawer).getByText("Lý do").nextSibling).toHaveTextContent("Bớt lẻ");
    expect(within(drawer).getByText("Sổ").nextSibling).toHaveTextContent("Sổ phải thu");
    const settled = within(drawer).getByRole("table", { name: "Chứng từ được giảm bởi DN-0001" });
    expect(at(within(settled).getAllByRole("row"), 1)).toHaveTextContent("SO-0001");
    expect(at(within(settled).getAllByRole("row"), 1)).toHaveTextContent("200.000");
    expect(within(drawer).getByText("Khách xin bớt phần lẻ")).toBeInTheDocument();
    expect(lookedUp).toBe("DN-0001");
  });

  it("opens an offset by its number with the two documents it netted", async () => {
    server.use(
      ...ledgerHandlers,
      http.get(apiUrl("/debt-offsets"), ({ request }) =>
        HttpResponse.json({ data: new URL(request.url).searchParams.get("doc_no") === "BT-0001" ? [{ id: "off-1" }] : [], next_cursor: null }),
      ),
      http.get(apiUrl("/debt-offsets/off-1"), () => HttpResponse.json(testOffsetDetail())),
    );
    renderApp("/debts/receivables?doc=BT-0001");

    const drawer = await screen.findByRole("dialog", { name: "Biên bản bù trừ BT-0001" });
    expect(await within(drawer).findByText("Phải thu")).toBeInTheDocument();
    expect(within(drawer).getByText("Phải thu").nextSibling).toHaveTextContent("SO-0001");
    expect(within(drawer).getByText("Phải trả").nextSibling).toHaveTextContent("PN-0003");
    expect(within(within(drawer).getByRole("table", { name: "Chứng từ được giảm bởi BT-0001" })).getAllByRole("row")).toHaveLength(3);
  });

  it("says when no adjustment has the linked number", async () => {
    server.use(...ledgerHandlers, http.get(apiUrl("/debt-adjustments"), () => HttpResponse.json({ data: [], next_cursor: null })));
    renderApp("/debts/receivables?doc=DN-0404");
    const drawer = await screen.findByRole("dialog", { name: "Điều chỉnh công nợ DN-0404" });
    expect(await within(drawer).findByText("Không tìm thấy DN-0404.")).toBeInTheDocument();
  });
});
