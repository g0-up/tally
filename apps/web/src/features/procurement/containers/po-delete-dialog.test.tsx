import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { etagOf } from "../../catalog";
import { testOrder } from "../testing/fixtures";
import { PoDeleteDialog } from "./po-delete-dialog";

const draft = testOrder({ id: "po-2", doc_no: "PO-0002", status: "draft", version: 3 });

function renderDialog() {
  const onClose = vi.fn();
  const onDeleted = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(<PoDeleteDialog order={draft} onClose={onClose} onDeleted={onDeleted} />);
  return { user, onClose, onDeleted };
}

describe("PoDeleteDialog", () => {
  it("deletes the draft at the version shown once confirmed", async () => {
    const deletes: Array<string | null> = [];
    server.use(
      http.delete(apiUrl("/purchase-orders/po-2"), ({ request }) => {
        deletes.push(request.headers.get("If-Match"));
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { user, onDeleted } = renderDialog();

    const dialog = await screen.findByRole("dialog", { name: "Xóa đơn nhập nháp PO-0002?" });
    expect(within(dialog).getByText(/chưa tạo tồn hay nợ NCC/)).toBeInTheDocument();
    expect(deletes).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Xóa nháp" }));
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
    expect(deletes).toEqual([etagOf(3)]);
    expect(await screen.findByText(/Đã xóa đơn nhập nháp/)).toHaveTextContent("Đã xóa đơn nhập nháp PO-0002.");
  });

  it("keeps the dialog open when the order was confirmed meanwhile", async () => {
    server.use(http.delete(apiUrl("/purchase-orders/po-2"), () => apiError(409, "order_not_draft")));
    const { user, onDeleted } = renderDialog();

    const dialog = await screen.findByRole("dialog", { name: "Xóa đơn nhập nháp PO-0002?" });
    await user.click(within(dialog).getByRole("button", { name: "Xóa nháp" }));
    expect(await screen.findByText("Đơn nhập đã chốt.")).toBeInTheDocument();
    expect(onDeleted).not.toHaveBeenCalled();
  });
});
