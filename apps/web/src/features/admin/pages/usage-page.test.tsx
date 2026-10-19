import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { addDays, todayBusinessDate } from "../../../shared/lib/dates";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { TENANT_ID, consoleHandlers, testUsageRow } from "../testing/fixtures";
import { USAGE_TABLE } from "./usage-page";

const OTHER_ID = "0193a000-0000-7000-8000-0000000000ff";

describe("UsagePage", () => {
  it("shows the last 30 days of every store, named by the usage rows themselves", async () => {
    const ranges: { from: string | null; to: string | null }[] = [];
    const tenantReads: string[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/tenants"), ({ request }) => {
        tenantReads.push(request.url);
        return HttpResponse.json({ data: [], next_cursor: null });
      }),
      http.get(adminApiUrl("/usage"), ({ request }) => {
        const url = new URL(request.url);
        ranges.push({ from: url.searchParams.get("from"), to: url.searchParams.get("to") });
        return HttpResponse.json({
          data: [testUsageRow(), testUsageRow({ tenant_id: OTHER_ID, tenant_name: "Tạp hóa Minh Châu", slug: "minh-chau", active_skus: 12, documents_posted: 3, storage_bytes: 1_048_576 })],
          next_cursor: null,
        });
      }),
    );
    renderApp("/admin/usage", "admin");

    const table = await screen.findByRole("table", { name: USAGE_TABLE });
    const store = await within(table).findByRole("row", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ });
    expect(within(store).getByRole("link", { name: "Đại lý Bánh kẹo Tết Hòa Phát" })).toHaveAttribute("href", `/admin/tenants/${TENANT_ID}`);
    expect(store).toHaveTextContent("hoa-phat");
    expect(store).toHaveTextContent("420");
    expect(store).toHaveTextContent("37");
    expect(store).toHaveTextContent("50 MB");
    const other = within(table).getByRole("row", { name: /Tạp hóa Minh Châu/ });
    expect(within(other).getByRole("link", { name: "Tạp hóa Minh Châu" })).toHaveAttribute("href", `/admin/tenants/${OTHER_ID}`);
    expect(other).toHaveTextContent("minh-chau");
    expect(other).toHaveTextContent("1 MB");
    // The names come with the rows; the store list is not read to join them.
    expect(tenantReads).toEqual([]);

    // The default range still follows the device clock.
    const today = todayBusinessDate();
    expect(ranges[0]).toEqual({ from: addDays(today, -29), to: today });
  });

  it("names the failure and offers a retry when the usage cannot be read", async () => {
    server.use(...consoleHandlers);
    server.use(http.get(adminApiUrl("/usage"), () => apiError(500, "internal_error")));
    renderApp("/admin/usage", "admin");

    const alert = await screen.findByText(/^Không tải được mức sử dụng:/);
    expect(alert).toHaveTextContent("Máy chủ gặp lỗi. Thử lại sau ít phút.");
    expect(within(alert).getByRole("button", { name: "Thử lại" })).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: USAGE_TABLE })).not.toBeInTheDocument();
  });
});
