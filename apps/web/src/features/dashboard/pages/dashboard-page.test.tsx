import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { CUSTOMER_ID, at, paymentLookups, testLedger } from "../../debt/testing/fixtures";

const side = (over: Record<string, unknown> = {}) => ({
  balance: "0",
  increase: "0",
  settled: "0",
  prev_balance: "0",
  prev_increase: "0",
  prev_settled: "0",
  due_soon: "0",
  overdue: "0",
  top: [],
  link: "/reports/debt-aging",
  ...over,
});

const period = (kind: string, date: string, from: string, to: string) => ({ kind, date, from, to });

/** A month on the seeded store's numbers; figures are deliberately unrelated so each lands in one place. */
function monthDashboard() {
  return {
    period: period("month", "2026-10-12", "2026-10-01", "2026-10-31"),
    today: "2026-10-12",
    previous: period("month", "2026-09-30", "2026-09-01", "2026-09-30"),
    next: period("month", "2026-11-01", "2026-11-01", "2026-11-30"),
    cash: {
      in: "12500000",
      out: "4000000",
      net: "8500000",
      balance: "31000000",
      prev_in: "10000000",
      prev_out: "5000000",
      prev_net: "5000000",
      prev_balance: "22500000",
      funds: [{ id: "fund-1", name: "Két tiền mặt", kind: "cash", balance: "31000000" }],
      // The API's order is in plus out: each side of "Theo loại" sorts its own.
      by_category: [
        { category: "supplier_payment", label: "Trả NCC", in: "0", out: "3000000" },
        { category: "sale", label: "Bán lẻ", in: "2500000", out: "0" },
        { category: "customer_payment", label: "Thu nợ khách", in: "10000000", out: "1000000" },
      ],
      link: "/reports/cash-flow?from=2026-10-01&to=2026-10-31",
    },
    debt: {
      receivable: side({
        balance: "4500000",
        increase: "6000000",
        settled: "1500000",
        due_soon: "700000",
        overdue: "1200000",
        top: [
          { party_id: CUSTOMER_ID, name: "Tạp hóa Minh Châu", amount: "4500000", overdue: "1200000", next_due: "2026-09-28" },
          { party_id: "cus-2", name: "Cô Lan", amount: "300000", overdue: "0", next_due: "2026-10-20" },
        ],
      }),
      payable: side({ balance: "9000000", increase: "9000000", settled: "0", due_soon: "2500000", top: [{ party_id: "sup-1", name: "NPP Kinh Đô", amount: "9000000", overdue: "0", next_due: "2026-10-15" }] }),
      due_link: "/reports/debt-schedule",
    },
    series: [
      { label: "01/10", from: "2026-10-01", to: "2026-10-01", in: "0", out: "0" },
      { label: "02/10", from: "2026-10-02", to: "2026-10-02", in: "12500000", out: "4000000" },
    ],
    entries: [],
  };
}

function dayDashboard() {
  const m = monthDashboard();
  return {
    ...m,
    period: period("day", "2026-10-11", "2026-10-11", "2026-10-11"),
    previous: period("day", "2026-10-10", "2026-10-10", "2026-10-10"),
    next: period("day", "2026-10-12", "2026-10-12", "2026-10-12"),
    cash: {
      ...m.cash,
      in: "1300000",
      out: "700000",
      net: "600000",
      by_category: [
        { category: "customer_payment", label: "Thu nợ khách", in: "1300000", out: "0" },
        { category: "supplier_payment", label: "Trả NCC", in: "0", out: "700000" },
      ],
      link: "/reports/cash-flow?from=2026-10-11&to=2026-10-11",
    },
    series: [{ label: "11/10", from: "2026-10-11", to: "2026-10-11", in: "1300000", out: "700000" }],
    entries: [
      { id: "e-1", doc_no: "PT-0007", party: "Tạp hóa Minh Châu", fund: "Két tiền mặt", category: "Thu nợ khách", amount: "1500000", reversal: false },
      { id: "e-2", doc_no: "PC-0003", party: "NPP Kinh Đô", fund: "Vietcombank", category: "Trả NCC", amount: "-700000", reversal: false },
      { id: "e-3", doc_no: null, party: null, fund: "Két tiền mặt", category: "Thu nợ khách", amount: "-200000", reversal: true },
    ],
  };
}

const cells = (row: HTMLElement) => within(row).getAllByRole("cell").map((c) => (c.textContent ?? "").replace(/\s/g, " "));
const kpi = (label: string) => screen.getByText(label, { selector: ".k" }).closest(".kpi");
const card = (title: string) => {
  const found = screen.getByRole("heading", { name: title }).closest(".card");
  if (!(found instanceof HTMLElement)) throw new Error(`No card ${title}`);
  return found;
};
/** "Theo loại" one side at a time: its heading, then each kind with its amount and bar width. */
const byKind = (heading: string) => {
  const h = within(card("Theo loại")).getByRole("heading", { name: new RegExp(`^${heading}`) });
  const list = h.nextElementSibling;
  return {
    heading: (h.textContent ?? "").replace(/\s/g, " "),
    rows: list?.classList.contains("catlist")
      ? [...list.children].map((r) => [(r.textContent ?? "").replace(/\s/g, " "), (r.querySelector("i") as HTMLElement | null)?.style.width])
      : (list?.textContent ?? ""),
  };
};

/** Answers every dashboard request with `answer` and records the query strings it was asked with. */
function serveDashboard(answer: (q: URLSearchParams) => object) {
  const asked: URLSearchParams[] = [];
  server.use(
    http.get(apiUrl("/reports/dashboard"), ({ request }) => {
      const q = new URL(request.url).searchParams;
      asked.push(q);
      return HttpResponse.json(answer(q));
    }),
  );
  return asked;
}

describe("DashboardPage", () => {
  it("shows the month the API returned, figure for figure", async () => {
    const asked = serveDashboard(() => monthDashboard());
    renderApp("/overview/dashboard");

    expect(await screen.findByRole("heading", { level: 1, name: "Thu, chi và công nợ" })).toBeInTheDocument();
    expect(await screen.findByText("tháng 10/2026", { selector: "b" })).toBeInTheDocument();
    expect(screen.getByText(/Công nợ tính tại ngày 12\/10\/2026\./)).toBeInTheDocument();
    expect(at(asked, 0).get("period")).toBe("month");
    expect(at(asked, 0).has("date")).toBe(false);

    expect(screen.getByRole("button", { name: "Tháng" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Trong tháng có ngày")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Về hôm nay" })).not.toBeInTheDocument();

    expect(kpi("Tổng thu")).toHaveTextContent(/12\.500\.000\s₫/);
    expect(kpi("Tổng thu")).toHaveTextContent("25% so với kỳ trước");
    expect(kpi("Tổng thu")).toHaveAttribute("href", "/reports/cash-flow?from=2026-10-01&to=2026-10-31");
    expect(kpi("Tổng chi")).toHaveTextContent(/4\.000\.000\s₫/);
    expect(kpi("Tổng chi")).toHaveTextContent("20% so với kỳ trước");
    expect(kpi("Chênh lệch thu – chi")).toHaveTextContent(/8\.500\.000\s₫.*Dòng tiền dương trong kỳ/);
    expect(kpi("Tiền trong quỹ")).toHaveTextContent(/31\.000\.000\s₫.*Tại ngày 12\/10\/2026/);

    expect(kpi("Khách còn nợ")).toHaveTextContent(/4\.500\.000\s₫.*Quá hạn 1\.200\.000\s₫/);
    expect(kpi("Khách còn nợ")).toHaveAttribute("href", "/debts/receivables");
    expect(kpi("Nợ khách trong kỳ")).toHaveTextContent(/\+6\.000\.000\s₫.*Khách đã trả 1\.500\.000\s₫/);
    expect(kpi("Cửa hàng còn nợ NCC")).toHaveTextContent(/9\.000\.000\s₫.*Không có khoản quá hạn/);
    expect(kpi("Nợ NCC trong kỳ")).toHaveTextContent(/\+9\.000\.000\s₫.*Cửa hàng đã trả 0\s₫/);
    expect(kpi("Sắp phải thu (7 ngày tới)")).toHaveTextContent(/700\.000\s₫.*Nợ khách đến hạn · dự báo/);
    expect(kpi("Sắp phải trả (7 ngày tới)")).toHaveTextContent(/2\.500\.000\s₫/);
    expect(kpi("Sắp phải trả (7 ngày tới)")).toHaveAttribute("href", "/debts/schedule");

    // Bars by day, with only the days that moved in "Xem dạng bảng".
    expect(screen.getByRole("heading", { name: "Thu – chi theo ngày" })).toBeInTheDocument();
    expect(screen.getByText("Xem dạng bảng")).toBeInTheDocument();
    const bars = screen.getByRole("table", { name: "Thu – chi theo ngày" });
    const barRows = within(bars).getAllByRole("row").slice(1);
    expect(barRows).toHaveLength(1);
    expect(cells(at(barRows, 0))).toEqual(["02/10/2026", "12.500.000 ₫", "4.000.000 ₫", "8.500.000 ₫"]);

    // "Theo loại": each side's kinds, largest first, with their share of the side's total.
    expect(within(card("Theo loại")).getByText("tháng 10/2026")).toBeInTheDocument();
    expect(byKind("Thu")).toEqual({
      heading: "Thu 12.500.000 ₫",
      rows: [
        ["Thu nợ khách10.000.000 ₫", "80%"],
        ["Bán lẻ2.500.000 ₫", "20%"],
      ],
    });
    expect(byKind("Chi")).toEqual({
      heading: "Chi 4.000.000 ₫",
      rows: [
        ["Trả NCC3.000.000 ₫", "75%"],
        ["Thu nợ khách1.000.000 ₫", "25%"],
      ],
    });

    const customers = screen.getByRole("table", { name: "Khách nợ nhiều nhất" });
    const customerRows = within(customers).getAllByRole("row").slice(1);
    expect(within(at(customerRows, 0)).getByRole("link", { name: "Tạp hóa Minh Châu" })).toHaveAttribute("href", `/debts/receivables?party=${CUSTOMER_ID}`);
    expect(within(customers).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Khách", "Còn nợ", "Quá hạn", "Hạn gần nhất", ""]);
    expect(cells(at(customerRows, 0)).slice(1, 4)).toEqual(["4.500.000 ₫", "1.200.000 ₫", "28/09/2026"]);
    expect(cells(at(customerRows, 1)).slice(1, 4)).toEqual(["300.000 ₫", "—", "20/10/2026"]);
    const suppliers = screen.getByRole("table", { name: "Nợ nhà cung cấp" });
    expect(within(suppliers).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Nhà cung cấp", "Còn nợ", "Hạn gần nhất", ""]);
    expect(within(suppliers).getByRole("link", { name: "NPP Kinh Đô" })).toHaveAttribute("href", "/debts/payables?party=sup-1");
    expect(cells(at(within(suppliers).getAllByRole("row"), 1)).slice(1, 3)).toEqual(["9.000.000 ₫", "15/10/2026"]);
    expect(within(suppliers).queryByText("Quá hạn")).not.toBeInTheDocument();
    expect(within(suppliers).getByRole("button", { name: "Trả tiền – NPP Kinh Đô" })).toHaveTextContent("Trả tiền");
    expect(within(customers).getByRole("button", { name: "Thu tiền – Tạp hóa Minh Châu" })).toHaveTextContent("Thu tiền");
  });

  it("lists the day's entries on their side with the API's totals", async () => {
    const asked = serveDashboard(() => dayDashboard());
    renderApp("/overview/dashboard?period=day&date=2026-10-11");

    const table = await screen.findByRole("table", { name: "Giao dịch thu – chi trong ngày" });
    expect(at(asked, 0).get("period")).toBe("day");
    expect(at(asked, 0).get("date")).toBe("2026-10-11");
    expect(screen.getByText("ngày 11/10/2026", { selector: "b" })).toBeInTheDocument();
    expect(screen.getByText("Ngày", { selector: "label span" })).toBeInTheDocument();
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Chứng từ", "Loại", "Đối tượng", "Quỹ", "Thu", "Chi"]);
    const rows = within(table).getAllByRole("row");
    expect(cells(at(rows, 1))).toEqual(["PT-0007", "Thu nợ khách", "Tạp hóa Minh Châu", "Két tiền mặt", "1.500.000 ₫", ""]);
    expect(cells(at(rows, 2))).toEqual(["PC-0003", "Trả NCC", "NPP Kinh Đô", "Vietcombank", "", "700.000 ₫"]);
    // A reversed receipt takes back money in: it stays on the "Thu" side, negative.
    expect(cells(at(rows, 3))).toEqual(["—", "Thu nợ khách", "", "Két tiền mặt", "-200.000 ₫", ""]);
    expect(cells(at(rows, 4))).toEqual(["Tổng", "", "", "", "1.300.000 ₫", "700.000 ₫"]);
    expect(screen.getByRole("button", { name: "Về hôm nay" })).toBeInTheDocument();
  });

  it("moves through periods in the URL with the dates the API gave", async () => {
    const asked = serveDashboard((q) => (q.get("period") === "day" ? dayDashboard() : monthDashboard()));
    const user = userEvent.setup();
    const { router } = renderApp("/overview/dashboard");
    await screen.findByText("tháng 10/2026", { selector: "b" });

    await user.click(screen.getByRole("button", { name: "‹ Kỳ trước" }));
    await waitFor(() => expect(router.state.location.search).toBe("?date=2026-09-30"));
    await waitFor(() => expect(asked.at(-1)?.get("date")).toBe("2026-09-30"));
    await user.click(await screen.findByRole("button", { name: "Về hôm nay" }));
    await waitFor(() => expect(router.state.location.search).toBe(""));

    await user.click(screen.getByRole("button", { name: "Ngày" }));
    await waitFor(() => expect(router.state.location.search).toBe("?period=day"));
    await screen.findByRole("table", { name: "Giao dịch thu – chi trong ngày" });
    await user.click(screen.getByRole("button", { name: "Kỳ sau ›" }));
    await waitFor(() => expect(router.state.location.search).toBe("?period=day&date=2026-10-12"));
  });

  it("opens the receipt of a top customer and a blank one from the header", async () => {
    serveDashboard(() => monthDashboard());
    server.use(...paymentLookups, http.get(apiUrl(`/debts/parties/${CUSTOMER_ID}`), () => HttpResponse.json(testLedger())));
    const user = userEvent.setup();
    renderApp("/overview/dashboard");

    const customers = await screen.findByRole("table", { name: "Khách nợ nhiều nhất" });
    await user.click(within(customers).getByRole("button", { name: "Thu tiền – Tạp hóa Minh Châu" }));
    const dialog = await screen.findByRole("dialog", { name: "Thu tiền khách" });
    expect(await within(dialog).findByDisplayValue("Tạp hóa Minh Châu")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    server.use(http.get(apiUrl("/debts/parties"), () => HttpResponse.json({ data: [], next_cursor: null })));
    await user.click(screen.getByRole("button", { name: "Trả tiền NCC" }));
    const pay = await screen.findByRole("dialog", { name: "Trả tiền nhà cung cấp" });
    expect(within(pay).getByRole("combobox", { name: "Nhà cung cấp" })).toHaveValue("");
  });

  it("marks a supplier whose nearest due date has passed", async () => {
    const m = monthDashboard();
    const late = { party_id: "sup-1", name: "NPP Kinh Đô", amount: "9000000", overdue: "9000000", next_due: "2026-10-05" };
    serveDashboard(() => ({ ...m, debt: { ...m.debt, payable: { ...m.debt.payable, overdue: "9000000", top: [late] } } }));
    renderApp("/overview/dashboard");

    const suppliers = await screen.findByRole("table", { name: "Nợ nhà cung cấp" });
    const due = at(within(at(within(suppliers).getAllByRole("row"), 1)).getAllByRole("cell"), 2);
    expect(due).toHaveTextContent(/^05\/10\/2026/);
    expect(within(due).getByText("Quá hạn")).toHaveClass("st", "bad");
  });

  it("says when the period moved no money of any kind", async () => {
    const m = monthDashboard();
    serveDashboard(() => ({ ...m, cash: { ...m.cash, in: "0", out: "0", by_category: [] } }));
    renderApp("/overview/dashboard");

    await screen.findByText("tháng 10/2026", { selector: "b" });
    expect(byKind("Thu")).toEqual({ heading: "Thu 0 ₫", rows: "Không có giao dịch trong kỳ." });
    expect(byKind("Chi")).toEqual({ heading: "Chi 0 ₫", rows: "Không có giao dịch trong kỳ." });
  });

  it("does not flag customers' overdue when there is none", async () => {
    const m = monthDashboard();
    serveDashboard(() => ({ ...m, debt: { ...m.debt, receivable: { ...m.debt.receivable, overdue: "0" } } }));
    renderApp("/overview/dashboard");

    await screen.findByText("tháng 10/2026", { selector: "b" });
    const card = kpi("Khách còn nợ");
    expect(card).toHaveTextContent(/4\.500\.000\s₫.*Không có khoản quá hạn/);
    expect(card).not.toHaveTextContent("Quá hạn 0");
    expect(card?.querySelector("b")).toBeNull();
  });

  it("dims the previous period's figures while the next one loads and hides them once it fails", async () => {
    // The previous period answers only once the test has looked at the loading state.
    let answer = () => {};
    const answered = new Promise<void>((resolve) => {
      answer = resolve;
    });
    server.use(
      http.get(apiUrl("/reports/dashboard"), async ({ request }) => {
        if (!new URL(request.url).searchParams.has("date")) return HttpResponse.json(monthDashboard());
        await answered;
        return apiError(500, "internal_error", { message: "boom" });
      }),
    );
    const user = userEvent.setup();
    renderApp("/overview/dashboard");
    await screen.findByText("tháng 10/2026", { selector: "b" });
    expect(kpi("Tổng thu")).toHaveTextContent(/12\.500\.000\s₫/);
    // The status region is there before the switch, so its new text is announced.
    const status = screen.getByRole("status");
    expect(status).toBeEmptyDOMElement();

    await user.click(screen.getByRole("button", { name: "‹ Kỳ trước" }));
    // While the new period loads, the old figures stay dimmed and say whose they are.
    expect(await screen.findByText("Đang tải số liệu kỳ đã chọn, số bên dưới là của tháng 10/2026.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toBe(status);
    expect(status).toHaveTextContent("Đang tải số liệu kỳ đã chọn");
    expect(status.closest("[aria-busy]")).toBeNull();
    expect(kpi("Tổng thu")?.closest("[aria-busy]")).toHaveAttribute("aria-busy", "true");
    answer();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByText("Tổng thu", { selector: ".k" })).not.toBeInTheDocument();
    expect(screen.queryByText("Khách còn nợ", { selector: ".k" })).not.toBeInTheDocument();
    expect(screen.queryByText("tháng 10/2026", { selector: "b" })).not.toBeInTheDocument();
  });

  it("says why the dashboard could not load", async () => {
    let asked = new URLSearchParams();
    server.use(
      http.get(apiUrl("/reports/dashboard"), ({ request }) => {
        asked = new URL(request.url).searchParams;
        return apiError(422, "validation_failed", { message: "Kỳ không hợp lệ." });
      }),
    );
    renderApp("/overview/dashboard?period=year&date=12/10/2026");
    expect(await screen.findByRole("alert", {}, { timeout: 3000 })).toHaveTextContent("Kiểm tra lại các ô được đánh dấu.");
    // A malformed date is not sent: the API answers for today.
    expect(asked.get("period")).toBe("year");
    expect(asked.has("date")).toBe(false);
    expect(screen.getByRole("button", { name: "Năm" })).toHaveAttribute("aria-pressed", "true");
  });
});
