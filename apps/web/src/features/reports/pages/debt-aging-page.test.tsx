import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { EXPORT_STATUS_LABELS, REPORT_TEXT } from "../api/adapters";
import { at, col, exportJob, reportPage, reportRoute } from "../testing/fixtures";

const COLUMNS = [col("party", "Đối tác"), col("current", "Trong hạn", "money"), col("over_60", "Trên 60 ngày", "money"), col("total", "Tổng nợ", "money")];

describe("DebtAgingPage", () => {
  it("ages both ledgers at one date, each with its own export", async () => {
    const seen = reportRoute("debt_aging", (q) =>
      q.get("direction") === "payable"
        ? reportPage({ report_key: "debt_aging", as_of: q.get("as_of"), columns: COLUMNS, rows: [{ id: "sup-1", party: "Công ty Kẹo Hải Hà", current: "0", over_60: "0", total: "4000000" }] })
        : reportPage({
            report_key: "debt_aging",
            as_of: q.get("as_of"),
            columns: COLUMNS,
            rows: [{ id: "cus-1", party: "Tạp hóa Minh Anh", current: "1000000", over_60: "2000000", total: "3000000" }],
            summary: [{ key: "overdue", label: "Quá hạn", kind: "money", value: "2000000" }],
            sections: [{ key: "advances", title: "Tiền ứng trước chưa cấn trừ", columns: [col("party", "Đối tác"), col("amount", "Số tiền", "money")], rows: [{ party: "Đại lý Hoa Mai", amount: "500000" }], totals: null }],
          }),
    );
    const posts: unknown[] = [];
    server.use(
      http.post(apiUrl("/export-jobs"), async ({ request }) => {
        posts.push(await request.json());
        return HttpResponse.json(exportJob({ report_key: "debt_aging" }), { status: 202 });
      }),
      http.get(apiUrl("/export-jobs/exp-1"), () => HttpResponse.json(exportJob({ report_key: "debt_aging" }))),
    );
    renderApp("/reports/debt-aging");

    expect(await screen.findByText("Tính tại ngày 12/10/2026 theo hạn của phần chưa thanh toán. Ứng trước trình bày ở màn hình riêng, không gộp thành nợ âm.")).toBeInTheDocument();
    expect(screen.getByLabelText(REPORT_TEXT.asOf)).toHaveValue("12/10/2026");
    const receivable = await screen.findByRole("table", { name: "Phải thu khách" });
    const payable = await screen.findByRole("table", { name: "Phải trả nhà cung cấp" });
    expect(screen.getByRole("heading", { name: "Phải thu khách" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Phải trả nhà cung cấp" })).toBeInTheDocument();
    expect(within(receivable).getByText("Tạp hóa Minh Anh")).toBeInTheDocument();
    expect(within(payable).getByText("Công ty Kẹo Hải Hà")).toBeInTheDocument();
    expect(within(screen.getByRole("table", { name: "Tiền ứng trước chưa cấn trừ" })).getByText("Đại lý Hoa Mai")).toBeInTheDocument();
    expect(seen.map((q) => [q.get("direction"), q.get("as_of"), q.has("from")]).sort()).toEqual([
      ["payable", "2026-10-12", false],
      ["receivable", "2026-10-12", false],
    ]);

    const exports = screen.getAllByRole("button", { name: REPORT_TEXT.export });
    expect(exports).toHaveLength(2);
    await userEvent.click(at(exports, 1));
    await waitFor(() => expect(posts).toEqual([{ report_key: "debt_aging", params: { as_of: "2026-10-12", direction: "payable" } }]));
    const status = await screen.findByRole("status", { name: "Tệp xuất CSV" });
    expect(status).toHaveTextContent(EXPORT_STATUS_LABELS.queued);
    // The export shows under the ledger it was started from.
    expect(screen.getByRole("heading", { name: "Phải trả nhà cung cấp" }).closest(".card")).toContainElement(status);
  });
});
