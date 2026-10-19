import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { toQty } from "../../../shared/domain/qty";
import type { SkuListItem } from "../../catalog";
import { testSku } from "../../procurement/testing/fixtures";
import { KIT_MESSAGES } from "../api/adapters";
import { testAssemblyDetail, testAssemblyOrder, testPosting } from "../testing/fixtures";
import { AssemblyCompleteDialog } from "./assembly-complete-dialog";

/** The order's detail and SKUs, and a recorder of every posting body. */
function completeServer(sku: SkuListItem = testSku()) {
  const bodies: unknown[] = [];
  server.use(
    http.get(apiUrl("/assembly-orders/asm-1"), () => HttpResponse.json(testAssemblyDetail())),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [sku], next_cursor: null })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [{ id: "c-do", code: "do", name: "Đỏ", sort_order: 1, active: true }] })),
    http.post(apiUrl("/assembly-orders/asm-1/postings"), async ({ request }) => {
      bodies.push(await request.json());
      return HttpResponse.json({ posting: testPosting({ produced_qty: "5" }), order: testAssemblyDetail({ completed_qty: "9" }) }, { status: 201 });
    }),
  );
  return bodies;
}

describe("AssemblyCompleteDialog", () => {
  it("offers the kits the order still has to make and refuses more", async () => {
    const bodies = completeServer();
    const onClose = vi.fn();
    const user = userEvent.setup();
    // 10 planned, 4 made, 1 taken apart: 7 still to make.
    renderWithProviders(<AssemblyCompleteDialog order={testAssemblyOrder({ disassembled_qty: "1" })} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi nhận hoàn thành LD-0001" });
    const qty = within(dialog).getByRole("textbox", { name: "Số giỏ hoàn thành (còn 7)" });
    expect(qty).toHaveValue("7");
    expect(await within(dialog).findByText("21 hộp")).toBeInTheDocument();
    expect(within(dialog).getByText("7 giỏ")).toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "8");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ hoàn thành" }));
    expect(await screen.findByText("Số lượng hoàn thành phải từ 1 đến 7.")).toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "0");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ hoàn thành" }));
    expect(bodies).toEqual([]);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("posts the finished kits and closes", async () => {
    const bodies = completeServer();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyCompleteDialog order={testAssemblyOrder()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi nhận hoàn thành LD-0001" });
    const qty = within(dialog).getByRole("textbox", { name: "Số giỏ hoàn thành (còn 6)" });
    expect(await within(dialog).findByText("18 hộp")).toBeInTheDocument();
    await user.clear(qty);
    await user.type(qty, "5");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ hoàn thành" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(bodies).toEqual([{ produced_qty: "5" }]);
    expect(await screen.findByText("Đã nhập giỏ thành phẩm.")).toBeInTheDocument();
  });

  it("asks the color of a component planned without one that is now held in colors, and issues the batch's share in it", async () => {
    const bodies = completeServer(testSku({ on_hand_by_color: [{ color_id: "c-do", qty: toQty(30) }] }));
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<AssemblyCompleteDialog order={testAssemblyOrder()} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog", { name: "Ghi nhận hoàn thành LD-0001" });
    const swatches = await within(dialog).findByRole("radiogroup", { name: "Màu – Bánh quy bơ" });
    const qty = within(dialog).getByRole("textbox", { name: "Số giỏ hoàn thành (còn 6)" });
    await user.clear(qty);
    await user.type(qty, "4");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ hoàn thành" }));
    expect(await screen.findByText(KIT_MESSAGES.colorPick(["Bánh quy bơ"]), { exact: false })).toBeInTheDocument();
    expect(bodies).toEqual([]);

    await user.click(within(swatches).getByRole("radio", { name: "Đỏ" }));
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ hoàn thành" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    // 18 hộp left for 6 kits: 4 kits take 12.
    expect(bodies).toEqual([{ produced_qty: "4", lines: [{ order_item_id: "ai-1", qty: "12", color_id: "c-do" }] }]);
  });
});
