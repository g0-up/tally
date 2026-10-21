import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { scannerServer } from "../../../test/msw/scanner";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { createFakeStreams } from "../../scanner/testing/fake-event-stream";
import { memorySessionStore, pairedStream, withScanner } from "../../scanner/testing/scanner-harness";
import { UNIT_MESSAGES } from "../../catalog";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { SUPPLIER_ID, TEST_UNITS, UNIT_HOP, UNIT_THUNG, testSku } from "../testing/fixtures";
import { NewPoDialog } from "./new-po-dialog";

const BARCODE_TAKEN = "Mã vạch đã được dùng cho hàng khác.";

/** Lookups of the dialog, and POST /purchase-orders answered by `answer`; returns the bodies it was sent. */
function serveOrder(answer: () => Response) {
  const sent: unknown[] = [];
  server.use(
    http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })),
    http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
    http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [] })),
    http.post(apiUrl("/purchase-orders"), async ({ request }) => {
      sent.push(await request.json());
      return answer();
    }),
  );
  return sent;
}

async function renderDialog() {
  const user = userEvent.setup();
  renderWithProviders(<NewPoDialog supplier={{ id: SUPPLIER_ID, name: "Công ty Bánh kẹo Hải Hà" }} onClose={vi.fn()} />);
  const dialog = await screen.findByRole("dialog", { name: "Tạo đơn nhập" });
  // The units arrive before "+ Hàng hóa mới" can default its hộp and thùng.
  await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "Tên hàng – dòng 1" })).toBeInTheDocument());
  return { user, dialog };
}

/** Adds a new product named `name` at 96.000 ₫ with `barcode`, as the last row. */
async function addNewProduct(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, name: string, barcode: string) {
  await user.click(within(dialog).getByRole("button", { name: "+ Hàng hóa mới" }));
  const names = within(dialog).getAllByRole("textbox", { name: "Tên hàng mới" });
  const field = names.at(-1);
  if (field === undefined) throw new Error("no new product row");
  await user.type(field, name);
  await waitFor(() => expect(within(dialog).getByRole("combobox", { name: `Đơn vị cơ sở – ${name}` })).toHaveValue(UNIT_HOP));
  await user.type(within(dialog).getByRole("textbox", { name: `Mã vạch – ${name}` }), barcode);
  await user.type(within(dialog).getByRole("textbox", { name: `Đơn giá – ${name}` }), "96000");
}

describe("NewPoDialog", () => {
  it("sends a new product's barcode and shows the server's refusal of it under that row's Mã vạch until it is changed", async () => {
    const sent = serveOrder(() => apiError(409, "barcode_taken", { message: BARCODE_TAKEN, fields: { "lines.0.new_product.barcode": "barcode_taken" } }));
    const { user, dialog } = await renderDialog();
    await addNewProduct(user, dialog, "Kẹo dẻo", " 8936000111222 ");

    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({
      supplier_party_id: SUPPLIER_ID,
      lines: [{ unit_id: UNIT_THUNG, new_product: { name: "Kẹo dẻo", base_unit_id: UNIT_HOP, units: [{ unit_id: UNIT_THUNG, factor: "12" }], purchase_unit_id: UNIT_THUNG, barcode: "8936000111222" } }],
    });

    const barcode = within(dialog).getByRole("textbox", { name: "Mã vạch – Kẹo dẻo" });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(BARCODE_TAKEN);
    expect(barcode).toHaveAttribute("aria-invalid", "true");
    expect(barcode).toHaveAccessibleDescription(BARCODE_TAKEN);

    await user.type(barcode, "9");
    expect(within(dialog).queryByText(BARCODE_TAKEN)).not.toBeInTheDocument();
    expect(barcode).not.toHaveAttribute("aria-invalid", "true");
  });

  it("refuses two new products with one barcode before sending", async () => {
    const sent = serveOrder(() => HttpResponse.json({ id: "po-7", doc_no: "PO-0007", version: 1, status: "draft" }, { status: 201 }));
    const { user, dialog } = await renderDialog();
    await addNewProduct(user, dialog, "Kẹo dẻo", "8936000111222");
    await addNewProduct(user, dialog, "Kẹo gừng", "8936000111222 ");

    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    expect(await screen.findByText(PROCUREMENT_MESSAGES.barcodeShared("8936000111222"), { exact: false })).toBeInTheDocument();
    expect(sent).toHaveLength(0);
  });

  it("opens a new product's unit table with its errors marked when the order is refused for it", async () => {
    const sent = serveOrder(() => HttpResponse.json({ id: "po-7", doc_no: "PO-0007", version: 1, status: "draft" }, { status: 201 }));
    const { user, dialog } = await renderDialog();
    await addNewProduct(user, dialog, "Kẹo dẻo", "8936000111222");
    const toggle = within(dialog).getByRole("button", { name: "Đơn vị & giá… (2 đơn vị)" });
    await user.click(toggle);
    await user.clear(within(dialog).getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu hộp" }));
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    // The toast and the field both say it.
    await waitFor(() => expect(screen.getAllByText(UNIT_MESSAGES.factor)).toHaveLength(2));
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const factor = within(dialog).getByRole("textbox", { name: "Quy đổi: 1 thùng bằng bao nhiêu hộp" });
    expect(factor).toHaveAttribute("aria-invalid", "true");
    expect(factor).toHaveAccessibleDescription(UNIT_MESSAGES.factor);
    await waitFor(() => expect(factor).toHaveFocus());
    expect(sent).toHaveLength(0);
  });

  it("turns a phone's code away while the order is being saved, and adds no row", async () => {
    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const sent = serveOrder(() => HttpResponse.json({ id: "po-7", doc_no: "PO-0007", version: 1, status: "draft" }, { status: 201 }));
    server.use(
      http.get(apiUrl("/sku-lookup"), () => HttpResponse.json({ sku: testSku(), unit_id: UNIT_HOP, unit_name: "hộp", factor: "1", matched_by: "barcode" })),
      http.post(apiUrl("/purchase-orders"), async ({ request }) => {
        sent.push(await request.json());
        await held;
        return HttpResponse.json({ id: "po-7", doc_no: "PO-0007", version: 1, status: "draft" }, { status: 201 });
      }),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(withScanner(<NewPoDialog supplier={{ id: SUPPLIER_ID, name: "Công ty Bánh kẹo Hải Hà" }} onClose={onClose} />, streams, memorySessionStore("ss-1")));
    const dialog = await screen.findByRole("dialog", { name: "Tạo đơn nhập" });
    const stream = await pairedStream(streams, "Pixel 8");

    stream.emit("scan", { scan_id: "scan-1", code: "8935000000017", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_HOP });
    const rows = () => within(dialog).getAllByRole("textbox", { name: /^Số lượng – / });
    expect(rows()).toHaveLength(1);

    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    await waitFor(() => expect(dialog.querySelector('button[aria-busy="true"]')).not.toBeNull());
    stream.emit("scan", { scan_id: "scan-2", code: "8935000000017", ttl_ms: 10_000 });
    stream.emit("scan", { scan_id: "scan-3", code: "8936000999999", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(3));
    expect(api.results.slice(1).map((r) => r.body)).toEqual([{ outcome: "not_accepting" }, { outcome: "not_accepting" }]);
    expect(rows()).toHaveLength(1);
    expect(rows()[0]).toHaveValue("1");

    release();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent).toEqual([expect.objectContaining({ lines: [expect.objectContaining({ sku_id: "sku-1", unit_id: UNIT_HOP, qty: "1" })] })]);
  });
});
