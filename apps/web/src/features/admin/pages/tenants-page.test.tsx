import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { LOAD_MORE_LABEL } from "../../../shared/ui/list-footer";
import { renderApp } from "../../../test/render";
import { adminApiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { consoleHandlers, testTenant } from "../testing/fixtures";
import { TENANTS_TABLE, TENANT_SEARCH_PLACEHOLDER } from "./tenants-page";

describe("TenantsPage", () => {
  it("finds a store by its slug and links to its own address", async () => {
    const asked: (string | null)[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/tenants"), ({ request }) => {
        const q = new URL(request.url).searchParams.get("q");
        asked.push(q);
        return HttpResponse.json({ data: q === "hoa-phat" ? [testTenant({ legal_hold: true })] : [], next_cursor: null });
      }),
    );
    renderApp("/admin/tenants", "admin");
    expect(await screen.findByText("Không có cửa hàng khớp bộ lọc.")).toBeInTheDocument();

    const box = screen.getByRole("searchbox", { name: TENANT_SEARCH_PLACEHOLDER });
    // The API takes at most 100 characters of search.
    expect(box).toHaveAttribute("maxlength", "100");
    await userEvent.setup().type(box, "hoa-phat");

    const table = await screen.findByRole("table", { name: TENANTS_TABLE });
    const row = within(table).getByRole("row", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ });
    expect(await within(row).findByRole("link", { name: "http://hoa-phat.localhost:5173 (mở tab mới)" })).toHaveAttribute("href", "http://hoa-phat.localhost:5173");
    expect(within(row).getByRole("link", { name: "Đại lý Bánh kẹo Tết Hòa Phát" })).toHaveAttribute("href", "/admin/tenants/0193a000-0000-7000-8000-000000000001");
    expect(row).toHaveTextContent("Gói Chuyên nghiệp");
    expect(row).toHaveTextContent("Đang dùng");
    expect(row).toHaveTextContent("Đang giữ");
    expect(asked).toContain("hoa-phat");
    expect(asked[0]).toBeNull();
  });

  it("reads 50 stores at a time and appends the next page on Xem thêm", async () => {
    const pages: { cursor: string | null; limit: string | null }[] = [];
    const second = testTenant({ id: "0193a000-0000-7000-8000-000000000002", slug: "minh-chau", display_name: "Tạp hóa Minh Châu" });
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/tenants"), ({ request }) => {
        const params = new URL(request.url).searchParams;
        const cursor = params.get("cursor");
        pages.push({ cursor, limit: params.get("limit") });
        return HttpResponse.json(cursor === null ? { data: [testTenant()], next_cursor: "page-2" } : { data: [second], next_cursor: null });
      }),
    );
    renderApp("/admin/tenants", "admin");
    const user = userEvent.setup();

    const table = await screen.findByRole("table", { name: TENANTS_TABLE });
    expect(within(table).getByRole("row", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: LOAD_MORE_LABEL }));

    expect(await within(table).findByRole("row", { name: /Tạp hóa Minh Châu/ })).toBeInTheDocument();
    expect(within(table).getByRole("row", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ })).toBeInTheDocument();
    expect(pages).toEqual([
      { cursor: null, limit: "50" },
      { cursor: "page-2", limit: "50" },
    ]);
    expect(screen.queryByRole("button", { name: LOAD_MORE_LABEL })).not.toBeInTheDocument();
  });
});
