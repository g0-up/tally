import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { testOrder } from "../testing/fixtures";
import { PoCloseDialog } from "./po-close-dialog";

const reasonsAre = (data: unknown[]) =>
  http.get(apiUrl("/reasons"), ({ request }) => {
    expect(new URL(request.url).searchParams.get("kind")).toBe("close_short");
    return HttpResponse.json({ data });
  });

describe("PoCloseDialog", () => {
  it("asks for a reason, then closes what is pending at the order's version", async () => {
    let body: unknown = null;
    let ifMatch: string | null = null;
    server.use(
      reasonsAre([{ id: "rsn-1", kind: "close_short", name: "NCC hết hàng", active: true }]),
      http.post(/\/purchase-orders\/po-1:close-short$/, async ({ request }) => {
        body = await request.json();
        ifMatch = request.headers.get("If-Match");
        return HttpResponse.json({ id: "po-1", doc_no: "PO-0001", version: 4, status: "closed_short" });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<PoCloseDialog order={testOrder()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Đóng phần còn thiếu của PO-0001" });
    expect(within(dialog).getByText("Phần đã nhận giữ nguyên. Lượng còn chờ không còn được tính là “dự kiến về”.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Đóng đơn" }));
    expect(await screen.findByText(/Cần lý do đóng phần thiếu\./)).toBeInTheDocument();

    await within(dialog).findByRole("option", { name: "NCC hết hàng" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: /^Lý do/ }), "rsn-1");
    await user.click(within(dialog).getByRole("button", { name: "Đóng đơn" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(body).toEqual({ reason_id: "rsn-1" });
    expect(ifMatch).toBe(etagOf(3));
    expect(await screen.findByText("Đã đóng phần còn thiếu.")).toBeInTheDocument();
  });

  it("points to the settings when the store has no close reasons", async () => {
    server.use(reasonsAre([]));
    renderWithProviders(<PoCloseDialog order={testOrder()} onClose={() => {}} />);
    expect(await screen.findByText("Chưa có lý do đóng đơn. Gửi yêu cầu hỗ trợ ở Thiết lập › Thuê bao để thêm.")).toBeInTheDocument();
  });
});
