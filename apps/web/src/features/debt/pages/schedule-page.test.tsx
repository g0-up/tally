import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { at } from "../testing/fixtures";

const day = (date: string, over: Record<string, string> = {}) => ({ date, receivable_due: "0", payable_due: "0", promised: "0", confirmed_in: "0", confirmed_out: "0", ...over });

describe("SchedulePage", () => {
  it("adds the overdue balances to the next 7 days and lists only days with movement", async () => {
    let query = new URLSearchParams();
    server.use(
      http.get(apiUrl("/debts/forecast"), ({ request }) => {
        query = new URL(request.url).searchParams;
        return HttpResponse.json({
          from: "2026-10-12",
          to: "2026-11-11",
          overdue_receivable: "1000000",
          overdue_payable: "0",
          days: [
            day("2026-10-12", { confirmed_in: "200000" }),
            day("2026-10-13"),
            day("2026-10-15", { receivable_due: "3000000", promised: "1000000" }),
            day("2026-10-19", { payable_due: "4000000" }),
            day("2026-10-25", { receivable_due: "9000000" }),
          ],
        });
      }),
      http.get(apiUrl("/funds"), () =>
        HttpResponse.json({
          data: [
            { id: "fund-1", kind: "cash", name: "Két tiền mặt", balance: "5000000", active: true },
            { id: "fund-2", kind: "bank", name: "Vietcombank", balance: "12000000", active: true },
            { id: "fund-3", kind: "bank", name: "Quỹ cũ", balance: "99000000", active: false },
          ],
        }),
      ),
    );
    renderApp("/debts/schedule");

    expect(await screen.findByText("Dự kiến theo hạn và cam kết của khách. Đây là dự báo, khác với tiền chắc chắn đã nhận.")).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Lịch thu/trả 30 ngày tới" });
    expect(query.get("from")).toBe("2026-10-12");
    expect(query.get("to")).toBe("2026-11-11");
    expect(screen.getByText("Dự kiến thu 7 ngày").closest(".kpi")).toHaveTextContent(/4\.000\.000\s₫/);
    expect(screen.getByText("Dự kiến trả 7 ngày").closest(".kpi")).toHaveTextContent(/4\.000\.000\s₫/);
    expect(screen.getByText("Tiền đang có").closest(".kpi")).toHaveTextContent(/17\.000\.000\s₫/);

    const rows = within(table).getAllByRole("row").slice(1);
    expect(within(at(rows, 0)).getByText("Quá hạn")).toBeInTheDocument();
    expect(rows[0]).toHaveTextContent("1.000.000");
    expect(rows.slice(1).map((r) => at(within(r).getAllByRole("cell"), 0).textContent)).toEqual(["12/10/2026", "15/10/2026", "19/10/2026", "25/10/2026"]);
    expect(rows[2]).toHaveTextContent("3.000.000");
    expect(rows[2]).toHaveTextContent("1.000.000");
    expect(screen.queryByText(/Ưu tiên nhắc nợ/)).not.toBeInTheDocument();
  });
});
