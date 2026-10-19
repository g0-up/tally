import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { supplierListItemSchema, type SupplierListItem } from "../schemas";

const PATH = "/catalog/suppliers";
const STOPPED_TOAST = "Đã ngừng giao dịch. NCC không còn hiện khi tạo đơn nhập.";
const OPEN_ORDERS_REASON = "Còn đơn nhập đang mở với nhà cung cấp này. Nhận hết hoặc đóng đơn trước khi ngừng giao dịch.";

function testSupplier(over: Partial<SupplierListItem> = {}): SupplierListItem {
  return supplierListItemSchema.parse({
    id: "sup-1",
    kind: "organization",
    name: "Công ty Bánh kẹo Hòa Phát",
    phone: "0901234567",
    email: null,
    tax_code: null,
    address: null,
    note: null,
    payment_terms_days: 30,
    supplied_goods_note: null,
    status: "active",
    is_customer: false,
    version: 1,
    contact_name: null,
    order_count: 4,
    last_receipt_date: "2026-10-01",
    payable_open: "0",
    can_stop: true,
    can_delete: false,
    ...over,
  });
}

const WITH_OPEN_ORDERS = testSupplier({ id: "sup-2", name: "Đại lý Nước ngọt Minh Châu", can_stop: false });

function withSuppliers(rows: readonly SupplierListItem[]) {
  server.use(http.get(apiUrl("/suppliers"), () => HttpResponse.json({ data: [...rows], next_cursor: null })));
}

function held() {
  let release!: () => void;
  const answer = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { answer, release };
}

describe("SuppliersPage", () => {
  it("names each row's buttons after the supplier", async () => {
    withSuppliers([testSupplier({ can_delete: true }), WITH_OPEN_ORDERS]);
    renderApp(PATH);
    const table = await screen.findByRole("table", { name: "Nhà cung cấp" });
    const row = within(table).getByRole("row", { name: /Công ty Bánh kẹo Hòa Phát/ });
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual([
      "Sửa Công ty Bánh kẹo Hòa Phát",
      "Tạo đơn nhập Công ty Bánh kẹo Hòa Phát",
      "Ngừng Công ty Bánh kẹo Hòa Phát",
      "Xóa Công ty Bánh kẹo Hòa Phát",
    ]);
    expect(within(row).getByRole("button", { name: "Sửa Công ty Bánh kẹo Hòa Phát" }).closest("td")).toHaveClass("acts");
  });

  it("reads out why a supplier with open orders cannot be stopped yet, and sends nothing", async () => {
    const sent: string[] = [];
    withSuppliers([WITH_OPEN_ORDERS]);
    server.use(
      http.post(/\/suppliers\/[^/]+:stop$/, ({ request }) => {
        sent.push(request.url);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderApp(PATH);
    const table = await screen.findByRole("table", { name: "Nhà cung cấp" });
    const stop = within(table).getByRole("button", { name: "Ngừng Đại lý Nước ngọt Minh Châu" });
    // Not disabled: focus and touch still reach it and hear the reason.
    expect(stop).not.toBeDisabled();
    expect(stop).not.toHaveAttribute("title");
    expect(stop).toHaveAccessibleDescription(OPEN_ORDERS_REASON);

    await userEvent.setup().click(stop);
    const dialog = await screen.findByRole("dialog", { name: "Chưa ngừng được giao dịch với Đại lý Nước ngọt Minh Châu" });
    expect(within(dialog).getByRole("alert")).toHaveTextContent(OPEN_ORDERS_REASON);
    expect(within(dialog).queryByRole("button", { name: "Ngừng giao dịch" })).not.toBeInTheDocument();
    expect(sent).toHaveLength(0);
  });

  it("asks before stopping a supplier and confirms it only once the API stored it", async () => {
    const sent: string[] = [];
    const reply = held();
    withSuppliers([testSupplier()]);
    server.use(
      http.post(/\/suppliers\/sup-1:stop$/, async ({ request }) => {
        sent.push(request.url);
        await reply.answer;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderApp(PATH);
    const table = await screen.findByRole("table", { name: "Nhà cung cấp" });
    const stop = within(table).getByRole("button", { name: "Ngừng Công ty Bánh kẹo Hòa Phát" });
    expect(stop).not.toHaveAccessibleDescription();
    const user = userEvent.setup();
    await user.click(stop);

    const dialog = await screen.findByRole("dialog", { name: "Ngừng giao dịch với Công ty Bánh kẹo Hòa Phát?" });
    expect(dialog).toHaveTextContent("vẫn giữ lịch sử và vẫn trả nợ được");
    expect(sent).toHaveLength(0);

    await user.click(within(dialog).getByRole("button", { name: "Ngừng giao dịch" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(screen.queryByText(STOPPED_TOAST)).not.toBeInTheDocument();

    reply.release();
    expect(await screen.findByText(STOPPED_TOAST)).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});
