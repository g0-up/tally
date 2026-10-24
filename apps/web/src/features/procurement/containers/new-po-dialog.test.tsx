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
import { etagOf } from "../../catalog";
import { SUPPLIER_ID, TEST_UNITS, UNIT_HOP, UNIT_THUNG, testOrder, testOrderLine, testSku } from "../testing/fixtures";
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
  // The row counts in its default thùng once the units have arrived.
  const row = field.closest("tr");
  if (row === null) throw new Error("no new product row");
  await waitFor(() => expect(within(row).getByText("/thùng")).toBeInTheDocument());
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

describe("NewPoDialog on a draft", () => {
  const listed = testOrder({ id: "po-2", doc_no: "PO-0002", status: "draft", version: 3 });

  /** The draft with one line of 2 thùng, and its PUT …/lines; returns what was written. */
  function serveDraft() {
    const writes: Array<{ method: string; ifMatch: string | null; body: unknown }> = [];
    const record = async (request: Request) => {
      writes.push({ method: request.method, ifMatch: request.headers.get("If-Match"), body: await request.json() });
      return HttpResponse.json({ ...listed, version: 4, lines: [testOrderLine()] });
    };
    server.use(
      http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })),
      http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.get(apiUrl("/purchase-orders/po-2"), () => HttpResponse.json({ ...listed, lines: [testOrderLine({ spec_text: "24 gói" })] })),
      http.patch(apiUrl("/purchase-orders/po-2"), ({ request }) => record(request)),
      http.put(apiUrl("/purchase-orders/po-2/lines"), ({ request }) => record(request)),
    );
    return writes;
  }

  it("opens the draft with its supplier, expected date and lines, and writes only the lines changed", async () => {
    const writes = serveDraft();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewPoDialog order={listed} onClose={onClose} />);

    // The form replaces the loading dialog once the lines and their SKUs arrived.
    const qty = await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = screen.getByRole("dialog", { name: "Sửa đơn nhập PO-0002" });
    expect(qty).toHaveValue("2");
    expect(within(dialog).getByRole("combobox", { name: "Nhà cung cấp" })).toHaveValue("Công ty Bánh kẹo Hải Hà");
    expect(within(dialog).getByRole("textbox", { name: "Quy cách – Bánh quy bơ" })).toHaveValue("24 gói");
    expect(within(dialog).getByRole("button", { name: "Lưu & chốt đơn" })).toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "5");
    await user.click(within(dialog).getByRole("button", { name: "Lưu nháp" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(writes).toEqual([
      { method: "PUT", ifMatch: etagOf(3), body: { lines: [{ sku_id: "sku-1", unit_id: UNIT_THUNG, qty: "5", unit_price: "120000", discount_amount: "0", spec_text: "24 gói" }] } },
    ]);
    expect(await screen.findByText(/Đã lưu nháp/)).toHaveTextContent("Đã lưu nháp PO-0002.");
  });

  it("waits for the color list, so a save with nothing changed keeps the line's color", async () => {
    const writes = serveDraft();
    let skusServed = false;
    let colorsIn = () => {};
    const colorsGate = new Promise<void>((resolve) => (colorsIn = resolve));
    server.use(
      http.get(apiUrl("/colors"), async () => {
        await colorsGate;
        return HttpResponse.json({ data: [{ id: "c-do", code: "do", name: "Đỏ", sort_order: 1, active: true }] });
      }),
      http.get(apiUrl("/skus"), () => {
        skusServed = true;
        return HttpResponse.json({ data: [testSku()] });
      }),
      http.get(apiUrl("/purchase-orders/po-2"), () => HttpResponse.json({ ...listed, lines: [testOrderLine({ color_id: "c-do" })] })),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewPoDialog order={listed} onClose={onClose} />);

    await waitFor(() => expect(skusServed).toBe(true));
    expect(screen.getByRole("dialog", { name: "Sửa đơn nhập PO-0002" })).toHaveTextContent("Đang tải…");
    colorsIn();
    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    await user.click(within(screen.getByRole("dialog", { name: "Sửa đơn nhập PO-0002" })).getByRole("button", { name: "Lưu nháp" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(writes).toEqual([]);
  });

  it("does not open an order confirmed since it was listed", async () => {
    serveDraft();
    server.use(http.get(apiUrl("/purchase-orders/po-2"), () => HttpResponse.json({ ...listed, status: "confirmed", version: 4, lines: [testOrderLine()] })));
    renderWithProviders(<NewPoDialog order={listed} onClose={vi.fn()} />);

    expect(await screen.findByText(PROCUREMENT_MESSAGES.orderNotDraft)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Lưu nháp" })).not.toBeInTheDocument();
  });

  it("retries only the price after the edits and the confirm were stored", async () => {
    const writes = serveDraft();
    // What GET returns follows the writes, so the refresh after the partial save sees the confirmed order.
    let stored = { ...listed, lines: [testOrderLine()] };
    const confirms: Array<string | null> = [];
    const prices: unknown[] = [];
    server.use(
      http.get(apiUrl("/purchase-orders/po-2"), () => HttpResponse.json(stored)),
      http.put(apiUrl("/purchase-orders/po-2/lines"), async ({ request }) => {
        writes.push({ method: "PUT", ifMatch: request.headers.get("If-Match"), body: await request.json() });
        stored = { ...stored, version: 4, lines: [testOrderLine({ qty: "5" })] };
        return HttpResponse.json(stored);
      }),
      http.post(/\/purchase-orders\/po-2:confirm$/, ({ request }) => {
        confirms.push(request.headers.get("If-Match"));
        stored = { ...stored, version: 5, status: "confirmed" };
        return HttpResponse.json(stored);
      }),
      http.post(apiUrl("/skus/sku-1/sale-price"), async ({ request }) => {
        prices.push(await request.json());
        // The first price write fails after the order was confirmed.
        return prices.length === 1 ? HttpResponse.json({ code: "internal_error" }, { status: 500 }) : HttpResponse.json({});
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewPoDialog order={listed} onClose={onClose} />);

    const qty = await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = screen.getByRole("dialog", { name: "Sửa đơn nhập PO-0002" });
    await user.clear(qty);
    await user.type(qty, "5");
    const sell = within(dialog).getByRole("textbox", { name: "Giá bán lẻ – Bánh quy bơ" });
    await user.clear(sell);
    await user.type(sell, "18000");
    await user.click(within(dialog).getByRole("button", { name: "Lưu & chốt đơn" }));
    expect(await screen.findByText(/nhưng chưa cập nhật giá bán/)).toBeInTheDocument();

    // The form keeps what was typed through the refresh, and the retry writes only the price.
    expect(within(dialog).getByRole("textbox", { name: "Giá bán lẻ – Bánh quy bơ" })).toHaveValue("18.000");
    await user.click(await within(dialog).findByRole("button", { name: "Lưu & chốt đơn" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(writes.map((w) => `${w.method}@${w.ifMatch}`)).toEqual([`PUT@${etagOf(3)}`]);
    expect(confirms).toEqual([etagOf(4)]);
    expect(prices).toHaveLength(2);
    expect(prices[1]).toMatchObject({ kind: "retail", price: "18000" });
  });
});
