import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { renderApp } from "../../../test/render";
import { adminApiUrl, apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { PLAN_PRO, TENANT_ID, consoleHandlers, listBody, pageBody, testInvoice, testTenant } from "../testing/fixtures";
import { INVOICES_TABLE } from "./invoices-page";

const INVOICE = testInvoice();
const { tenant_id: _t, tenant_slug: _s, tenant_name: _n, ...STORE_INVOICE } = INVOICE;

function withInvoice() {
  server.use(...consoleHandlers);
  server.use(http.get(adminApiUrl("/saas-invoices"), pageBody([INVOICE])));
}

async function openRow() {
  const table = await screen.findByRole("table", { name: INVOICES_TABLE });
  return within(table).getByRole("row", { name: /TB-2026-0007/ });
}

describe("InvoicesPage", () => {
  it("marks an open invoice paid with the bank reference", async () => {
    const bodies: unknown[] = [];
    withInvoice();
    server.use(
      http.post(/\/api\/admin\/v1\/saas-invoices\/[^/]+:mark-paid$/, async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ ...STORE_INVOICE, status: "paid", provider_ref: "FT26290123", paid_at: "2026-10-18T02:00:00Z" });
      }),
    );
    renderApp("/admin/invoices", "admin");
    const user = userEvent.setup();
    await user.click(within(await openRow()).getByRole("button", { name: "Ghi nhận thanh toán TB-2026-0007" }));

    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Ghi nhận đã thanh toán" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Nhập mã tham chiếu chuyển khoản.");
    expect(bodies).toEqual([]);

    await user.type(within(dialog).getByLabelText(/Mã tham chiếu chuyển khoản/), " FT26290123 ");
    await user.click(within(dialog).getByRole("button", { name: "Ghi nhận đã thanh toán" }));

    expect(await screen.findByText(/Đã ghi nhận thanh toán hóa đơn/)).toHaveTextContent("Đã ghi nhận thanh toán hóa đơn TB-2026-0007.");
    expect(bodies).toEqual([{ reference: "FT26290123" }]);
  });

  it("voids an invoice only after its number is typed", async () => {
    let voided = 0;
    withInvoice();
    server.use(
      http.post(/\/api\/admin\/v1\/saas-invoices\/[^/]+:void$/, () => {
        voided += 1;
        return HttpResponse.json({ ...STORE_INVOICE, status: "void" });
      }),
    );
    renderApp("/admin/invoices", "admin");
    const user = userEvent.setup();
    await user.click(within(await openRow()).getByRole("button", { name: "Hủy TB-2026-0007" }));

    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Hủy hóa đơn" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/Gõ số hóa đơn/), "TB-2026-0007");
    await user.click(submit);

    expect(await screen.findByText(/Đã hủy hóa đơn/)).toHaveTextContent("Đã hủy hóa đơn TB-2026-0007.");
    expect(voided).toBe(1);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the dialog and names the refusal when the invoice is no longer open", async () => {
    withInvoice();
    server.use(http.post(/\/api\/admin\/v1\/saas-invoices\/[^/]+:void$/, () => apiError(409, "invoice_not_open")));
    renderApp("/admin/invoices", "admin");
    const user = userEvent.setup();
    await user.click(within(await openRow()).getByRole("button", { name: "Hủy TB-2026-0007" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText(/Gõ số hóa đơn/), "TB-2026-0007");
    await user.click(within(dialog).getByRole("button", { name: "Hủy hóa đơn" }));

    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent("Chưa ghi sổ.");
    expect(toast).toHaveTextContent("Hóa đơn không còn ở trạng thái chờ thanh toán.");
    expect(screen.queryByText(/Đã hủy hóa đơn/)).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("filters by a store picked by name, and issues only to stores that are not sandboxes", async () => {
    const asked: (string | null)[] = [];
    const issued: unknown[] = [];
    const sandbox = testTenant({ id: "0193a000-0000-7000-8000-000000000003", slug: "hoa-phat-thu", display_name: "Hòa Phát (thử nghiệm)", is_sandbox: true, sandbox_of: TENANT_ID });
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/plans"), listBody([PLAN_PRO])),
      http.get(adminApiUrl("/tenants"), () => HttpResponse.json({ data: [testTenant(), sandbox], next_cursor: null })),
      http.get(adminApiUrl("/saas-invoices"), ({ request }) => {
        asked.push(new URL(request.url).searchParams.get("tenant_id"));
        return HttpResponse.json({ data: [INVOICE], next_cursor: null });
      }),
      http.post(adminApiUrl("/saas-invoices"), async ({ request }) => {
        issued.push(await request.json());
        return HttpResponse.json({ ...STORE_INVOICE, number: "TB-2026-0008" }, { status: 201 });
      }),
    );
    renderApp("/admin/invoices", "admin");
    const user = userEvent.setup();
    await openRow();

    await user.click(screen.getByRole("combobox", { name: "Cửa hàng" }));
    await user.click(await screen.findByRole("option", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ }));
    await waitFor(() => expect(asked).toContain(TENANT_ID));

    await user.click(screen.getByRole("button", { name: "Lập hóa đơn" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("combobox", { name: "Cửa hàng" }));
    expect(await within(dialog).findByRole("option", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ })).toBeInTheDocument();
    expect(within(dialog).queryByRole("option", { name: /thử nghiệm/ })).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("option", { name: /Đại lý Bánh kẹo Tết Hòa Phát/ }));
    await user.selectOptions(within(dialog).getByLabelText(/^Gói/), PLAN_PRO.id);
    await user.click(within(dialog).getByRole("button", { name: "Lập hóa đơn" }));

    expect(await screen.findByText(/Đã lập hóa đơn/)).toHaveTextContent("Đã lập hóa đơn TB-2026-0008.");
    expect(issued).toEqual([{ tenant_id: TENANT_ID, plan_id: PLAN_PRO.id }]);
  });

  it("searches stores with no more than the 100 characters the API accepts", async () => {
    const searched: string[] = [];
    server.use(...consoleHandlers);
    server.use(
      http.get(adminApiUrl("/tenants"), ({ request }) => {
        searched.push(new URL(request.url).searchParams.get("q") ?? "");
        return HttpResponse.json({ data: [], next_cursor: null });
      }),
    );
    renderApp("/admin/invoices", "admin");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("combobox", { name: "Cửa hàng" }));
    await user.paste("a".repeat(120));

    await waitFor(() => expect(searched).toContain("a".repeat(100)));
    expect(searched.every((q) => q.length <= 100)).toBe(true);
  });
});
