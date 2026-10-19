import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testStatementVersion } from "../testing/fixtures";

const snapshot = testStatementVersion().snapshot;

describe("PrintStatementPage", () => {
  it("prints the requested version from the server's print payload", async () => {
    let version: string | null = null;
    server.use(
      http.get(apiUrl("/print/statement/stm-1"), ({ request }) => {
        version = new URL(request.url).searchParams.get("version");
        return HttpResponse.json({
          label: "statement",
          title: "BẢNG KÊ ĐỐI SOÁT CÔNG NỢ",
          doc_no: "DS-0001",
          date: "2026-10-12",
          tenant: { name: "Hòa Phát", legal_name: "Công ty TNHH Hòa Phát", tax_code: "0312345678", address: "12 Lê Lợi, Q.1", phone: null },
          customer: { id: "cus-1", name: "Tạp hóa Minh Châu", phone: "0903123456", tax_code: null, address: null },
          supplier: null,
          version: 2,
          sent_at: null,
          printed_at: "2026-10-12T03:00:00Z",
          statement: { direction: snapshot.direction, period_from: snapshot.period_from, period_to: snapshot.period_to, opening_balance: snapshot.opening_balance, closing_balance: snapshot.closing_balance, lines: snapshot.lines },
        });
      }),
    );
    renderApp("/print/statements/stm-1?version=2");

    const sheet = await screen.findByRole("article", { name: "BẢNG KÊ ĐỐI SOÁT CÔNG NỢ DS-0001" });
    expect(version).toBe("2");
    expect(within(sheet).getByText("Công ty TNHH Hòa Phát")).toBeInTheDocument();
    expect(within(sheet).getByText("MST: 0312345678")).toBeInTheDocument();
    expect(within(sheet).getByText("Số DS-0001 · phiên bản 2 · kỳ 01/09/2026 – 12/10/2026")).toBeInTheDocument();
    expect(within(sheet).getByText("Tạp hóa Minh Châu")).toBeInTheDocument();
    expect(within(sheet).getByText("SO-0001")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "In phiếu" })).toBeEnabled();
  });

  it("ignores a malformed version and reports a failed load", async () => {
    let version: string | null = "unset";
    server.use(
      http.get(apiUrl("/print/statement/stm-1"), ({ request }) => {
        version = new URL(request.url).searchParams.get("version");
        return apiError(404, "not_found", { message: "Không tìm thấy bảng kê." });
      }),
    );
    renderApp("/print/statements/stm-1?version=abc");

    expect(await screen.findByText(/^Không tải được chứng từ để in:/, undefined, { timeout: 3000 })).toBeInTheDocument();
    expect(version).toBeNull();
    expect(screen.getByRole("button", { name: "In phiếu" })).toBeDisabled();
  });
});
