import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { REPORT_MESSAGES, REPORT_TEXT } from "../api/adapters";
import { at, col, kpi, reportPage, reportRoute, savedReport } from "../testing/fixtures";

const REVENUE = reportPage({
  report_key: "revenue",
  title: "Doanh thu",
  group_by: "product",
  columns: [col("name", "Tên"), col("sales_amount", "Doanh thu bán", "money"), col("returns_amount", "Hàng trả lại", "money"), col("net_revenue", "Doanh thu thuần", "money")],
  rows: [{ id: "sku-1", name: "Giỏ quà Phúc Lộc", sales_amount: "12000000", returns_amount: "500000", net_revenue: "11500000" }],
  totals: { name: null, sales_amount: "12000000", returns_amount: "500000", net_revenue: "11500000" },
  summary: [
    { key: "net_revenue", label: "Doanh thu thuần", kind: "money", value: "11500000", previous: "9000000" },
    { key: "returns_amount", label: "Hàng trả lại", kind: "money", value: "500000" },
    { key: "documents", label: "Số chứng từ", kind: "int", value: "42" },
    { key: "average_document", label: "Bình quân một chứng từ", kind: "money", value: "273810" },
  ],
});

const searchOf = (router: { state: { location: { search: string } } }) => new URLSearchParams(router.state.location.search);

describe("SalesPage", () => {
  it("shows the server's summary and regroups the rows through the URL", async () => {
    const seen = reportRoute("revenue", (q) => ({ ...REVENUE, group_by: q.get("group_by") }));
    const { router } = renderApp("/reports/sales");

    expect(
      await screen.findByText("Doanh thu thực hiện theo chứng từ bán đã ghi sổ (xuất giao và POS), sau trả hàng. Không lấy tổng giá trị hóa đơn chưa giao làm doanh thu."),
    ).toBeInTheDocument();
    await screen.findByRole("table", { name: "Doanh thu bán hàng" });
    expect(kpi("Doanh thu thuần")).toHaveTextContent(/11\.500\.000\s₫/);
    expect(kpi("Doanh thu thuần")).toHaveTextContent(/Kỳ trước 9\.000\.000\s₫/);
    expect(kpi("Số chứng từ")).toHaveTextContent("42");
    expect(at(seen, 0).get("group_by")).toBe("product");
    expect(at(seen, 0).get("columns")).toBe("name,sales_amount,returns_amount,net_revenue");

    const chips = screen.getByRole("group", { name: "Nhóm theo" });
    expect(within(chips).getByRole("button", { name: "Theo hàng" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(within(chips).getByRole("button", { name: "Theo khách" }));
    await waitFor(() => expect(seen.some((q) => q.get("group_by") === "customer")).toBe(true));
    expect(searchOf(router).get("group_by")).toBe("customer");
    expect(within(chips).getByRole("button", { name: "Theo khách" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(within(chips).getByRole("button", { name: "Theo kênh" }));
    await waitFor(() => expect(seen.some((q) => q.get("group_by") === "channel")).toBe(true));
  });

  it("saves the layout on screen under a name and explains a name already taken", async () => {
    reportRoute("revenue", () => REVENUE);
    const bodies: unknown[] = [];
    server.use(
      http.post(apiUrl("/saved-reports"), async ({ request }) => {
        const body = (await request.json()) as { name: string };
        bodies.push(body);
        if (body.name === "Theo khách") return apiError(409, "saved_report_name_taken");
        return HttpResponse.json(savedReport({ name: body.name }), { status: 201 });
      }),
    );
    renderApp("/reports/sales?group_by=customer");
    await screen.findByRole("table", { name: "Doanh thu bán hàng" });

    await userEvent.click(screen.getByRole("button", { name: REPORT_TEXT.save }));
    const dialog = await screen.findByRole("dialog", { name: REPORT_TEXT.save });
    await userEvent.click(within(dialog).getByRole("button", { name: "Lưu" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(REPORT_MESSAGES.nameRequired);
    expect(bodies).toHaveLength(0);

    // The error sits inside the field's label, so the box is found by its role.
    const name = within(dialog).getByRole("textbox");
    await userEvent.type(name, "Theo khách");
    await userEvent.click(within(dialog).getByRole("button", { name: "Lưu" }));
    expect(await within(dialog).findByText(REPORT_MESSAGES.nameTaken)).toBeInTheDocument();

    await userEvent.clear(name);
    await userEvent.type(name, "Khách mùa Tết");
    await userEvent.click(within(dialog).getByRole("button", { name: "Lưu" }));
    expect(await screen.findByText("Đã lưu bộ lọc.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(bodies.at(-1)).toEqual({
      report_key: "revenue",
      name: "Khách mùa Tết",
      config: { columns: ["name", "sales_amount", "returns_amount", "net_revenue"], group_by: "customer", filters: {} },
    });
  });

  it("applies, renames and deletes a saved layout", async () => {
    const seen = reportRoute("revenue", () => REVENUE);
    let views = [savedReport(), savedReport({ id: "sv-2", name: "Theo kênh", config: { columns: ["name", "net_revenue"], group_by: "channel", filters: {} } })];
    const listed: string[] = [];
    const patched: unknown[] = [];
    const deleted: string[] = [];
    server.use(
      http.get(apiUrl("/saved-reports"), ({ request }) => {
        listed.push(new URL(request.url).searchParams.get("report_key") ?? "");
        return HttpResponse.json({ data: views });
      }),
      http.patch(apiUrl("/saved-reports/:id"), async ({ params, request }) => {
        const body = (await request.json()) as { name: string };
        patched.push({ id: params.id, ...body });
        views = views.map((v) => (v.id === params.id ? { ...v, name: body.name } : v));
        return HttpResponse.json(views.find((v) => v.id === params.id));
      }),
      http.delete(apiUrl("/saved-reports/:id"), ({ params }) => {
        deleted.push(String(params.id));
        views = views.filter((v) => v.id !== params.id);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { router } = renderApp("/reports/sales");
    await screen.findByRole("table", { name: "Doanh thu bán hàng" });

    const openList = async () => {
      await userEvent.click(screen.getByRole("button", { name: REPORT_TEXT.saved }));
      return screen.findByRole("dialog", { name: REPORT_TEXT.saved });
    };

    let list = await openList();
    const second = (await within(list).findByText("Theo kênh")).closest("tr");
    if (second === null) throw new Error("No row for the saved layout");
    expect(listed).toEqual(["revenue"]);
    // The row's buttons sit in the touch-sized actions cell.
    expect(within(second).getByRole("button", { name: "Áp dụng Theo kênh" }).closest("td")).toHaveClass("acts");
    await userEvent.click(within(second).getByRole("button", { name: "Áp dụng Theo kênh" }));
    await waitFor(() => expect(seen.some((q) => q.get("group_by") === "channel" && q.get("columns") === "name,net_revenue")).toBe(true));
    expect(searchOf(router).get("group_by")).toBe("channel");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    list = await openList();
    const first = (await within(list).findByText("Theo khách mùa Tết")).closest("tr");
    if (first === null) throw new Error("No row for the saved layout");
    await userEvent.click(within(first).getByRole("button", { name: "Đổi tên Theo khách mùa Tết" }));
    const rename = await screen.findByRole("dialog", { name: "Đổi tên bộ lọc" });
    expect(within(rename).getByLabelText("Tên bộ lọc")).toHaveValue("Theo khách mùa Tết");
    await userEvent.clear(within(rename).getByLabelText("Tên bộ lọc"));
    await userEvent.type(within(rename).getByLabelText("Tên bộ lọc"), "Khách sỉ");
    await userEvent.click(within(rename).getByRole("button", { name: "Lưu" }));
    list = await screen.findByRole("dialog", { name: REPORT_TEXT.saved });
    expect(await within(list).findByText("Khách sỉ")).toBeInTheDocument();
    expect(patched).toEqual([{ id: "sv-1", name: "Khách sỉ" }]);

    const renamed = within(list).getByText("Khách sỉ").closest("tr");
    if (renamed === null) throw new Error("No row for the saved layout");
    await userEvent.click(within(renamed).getByRole("button", { name: "Xóa Khách sỉ" }));
    const confirm = await screen.findByRole("dialog", { name: "Xóa bộ lọc" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Xóa" }));
    list = await screen.findByRole("dialog", { name: REPORT_TEXT.saved });
    await waitFor(() => expect(within(list).queryByText("Khách sỉ")).not.toBeInTheDocument());
    expect(deleted).toEqual(["sv-1"]);
    expect(screen.getByText("Đã xóa bộ lọc.")).toBeInTheDocument();
  });
});
