import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderApp } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";

const hold = (over: Record<string, unknown>) => ({
  id: "res-1",
  sku_id: "sku-1",
  source_type: "manual",
  source_id: "src-1",
  source_doc_no: null,
  qty: "5",
  open_qty: "5",
  status: "active",
  note: null,
  created_at: "2026-10-12T02:00:00Z",
  version: 1,
  ...over,
});

describe("Giữ hàng page", () => {
  it("releases a manual hold once a reason is given, and leaves document holds alone", async () => {
    const bodies: unknown[] = [];
    server.use(
      http.get(apiUrl("/reservations"), () => HttpResponse.json({ data: [hold({}), hold({ id: "res-2", source_type: "invoice", source_doc_no: "HD-0007" })], next_cursor: null })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
      http.post(/\/reservations\/res-1:release$/, async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json(hold({ status: "released", open_qty: "0", version: 2 }));
      }),
    );
    const user = userEvent.setup();
    renderApp("/inventory/reservations");

    const manual = (await screen.findByText("Giữ thủ công")).closest("tr") as HTMLElement;
    const invoice = screen.getByText("HD-0007").closest("tr") as HTMLElement;
    expect(within(invoice).queryByRole("button", { name: "Giải phóng" })).not.toBeInTheDocument();

    await user.click(within(manual).getByRole("button", { name: "Giải phóng" }));
    const dialog = await screen.findByRole("dialog", { name: "Giải phóng hàng giữ" });
    await user.click(within(dialog).getByRole("button", { name: "Giải phóng" }));
    expect(within(dialog).getByText("Cần lý do.")).toBeInTheDocument();
    expect(bodies).toEqual([]);

    await user.type(within(dialog).getByRole("textbox", { name: /^Lý do/ }), "  Khách hủy ");
    await user.click(within(dialog).getByRole("button", { name: "Giải phóng" }));
    expect(await screen.findByText("Đã giải phóng.")).toBeInTheDocument();
    expect(bodies).toEqual([{ reason: "Khách hủy" }]);
  });

  it.each([
    ["required", "Cần lý do."],
    ["too_long", "Lý do tối đa 500 ký tự."],
  ])("keeps the dialog and words the reason the API refused as %s", async (reason, message) => {
    server.use(
      http.get(apiUrl("/reservations"), () => HttpResponse.json({ data: [hold({})], next_cursor: null })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()], next_cursor: null })),
      http.post(/\/reservations\/res-1:release$/, () => apiError(422, "validation_failed", { fields: { reason } })),
    );
    const user = userEvent.setup();
    renderApp("/inventory/reservations");

    const manual = (await screen.findByText("Giữ thủ công")).closest("tr") as HTMLElement;
    await user.click(within(manual).getByRole("button", { name: "Giải phóng" }));
    const dialog = await screen.findByRole("dialog", { name: "Giải phóng hàng giữ" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Lý do/ }), "Khách hủy");
    await user.click(within(dialog).getByRole("button", { name: "Giải phóng" }));

    expect(await within(dialog).findByText(message)).toBeInTheDocument();
    expect(screen.queryByText("Đã giải phóng.")).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Giải phóng hàng giữ" })).toBeInTheDocument();
  });
});
