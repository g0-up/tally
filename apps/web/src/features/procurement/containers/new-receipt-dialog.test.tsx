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
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { SUPPLIER_ID, TEST_UNITS, UNIT_HOP, UNIT_THUNG, testOrder, testOrderLine, testSku } from "../testing/fixtures";
import { NewReceiptDialog } from "./new-receipt-dialog";

const lookups = [
  http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })),
  http.get(apiUrl("/colors"), () => HttpResponse.json({ data: [] })),
];

/** A row of GET /suppliers for the "Nhà cung cấp" picker of Nhập nhanh. */
const SUPPLIER = {
  id: SUPPLIER_ID,
  kind: "organization",
  name: "Công ty Bánh kẹo Hải Hà",
  phone: null,
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
  order_count: 0,
  last_receipt_date: null,
  payable_open: "0",
  can_stop: true,
  can_delete: false,
};

describe("NewReceiptDialog", () => {
  it("receives an order's pending lines in base units, then records freight and the invoice number", async () => {
    const posted: Record<string, unknown> = {};
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), async ({ request }) => {
        posted.receipt = await request.json();
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 });
      }),
      http.post(apiUrl("/receipts/rcv-9/landed-costs"), async ({ request }) => {
        posted.landed = await request.json();
        return HttpResponse.json({ id: "lc-1" }, { status: 201 });
      }),
      http.post(apiUrl("/supplier-documents"), async ({ request }) => {
        posted.invoice = await request.json();
        return HttpResponse.json({ id: "sd-1", document_no: "HD-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    // The loading dialog is replaced by the form's once the order's lines arrive, so find the form first.
    const qty = await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    expect(within(dialog).getByText("Công ty Bánh kẹo Hải Hà · số lượng theo đơn vị cơ sở")).toBeInTheDocument();
    expect(qty).toHaveValue("24");
    expect(within(dialog).queryByRole("button", { name: "+ Hàng có sẵn" })).not.toBeInTheDocument();

    await user.clear(qty);
    await user.type(qty, "26");
    expect(within(dialog).getByText("Có – sẽ bị chặn")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));
    expect(await screen.findByText(PROCUREMENT_MESSAGES.overTolerance("Bánh quy bơ", "26", "24"), { exact: false })).toBeInTheDocument();
    expect(posted.receipt).toBeUndefined();

    await user.clear(qty);
    await user.type(qty, "12");
    await user.type(within(dialog).getByRole("textbox", { name: "Số hóa đơn NCC (nếu có)" }), "HD-1");
    await user.type(within(dialog).getByRole("textbox", { name: /^Chi phí vận chuyển\/bốc xếp/ }), "30000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(posted.receipt).toEqual({ purchase_order_id: "po-1", paid_now_amount: "0", lines: [{ purchase_order_line_id: "pol-1", qty: "1" }] });
    expect(posted.landed).toEqual({ cost_type: "shipping", amount: "30000", method: "by_value", paid_now_amount: "0" });
    expect(posted.invoice).toEqual({ supplier_party_id: SUPPLIER_ID, document_no: "HD-1", goods_amount: "120000", tax_amount: "0", receipt_ids: ["rcv-9"] });
    expect(await screen.findByText("PN-0009")).toBeInTheDocument();
  });

  it("does not post the receipt twice when a step after it fails, and continues from that step", async () => {
    let receipts = 0;
    let landed = 0;
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), () => {
        receipts++;
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 });
      }),
      http.post(apiUrl("/receipts/rcv-9/landed-costs"), () => {
        landed++;
        return landed === 1 ? HttpResponse.error() : HttpResponse.json({ id: "lc-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    await user.type(within(dialog).getByRole("textbox", { name: /^Chi phí vận chuyển\/bốc xếp/ }), "30000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));

    expect(await screen.findByText(/nhưng chưa ghi chi phí vận chuyển/)).toBeInTheDocument();
    expect(screen.queryByText("Chưa ghi sổ.")).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.click(within(screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" })).getByRole("button", { name: "Ghi sổ nhập kho" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(receipts).toBe(1);
    expect(landed).toBe(2);
  });

  it("sends the invoice and price steps under keys derived from the receipt's, the same ones on a retry", async () => {
    const keys: Record<string, Array<string | null>> = { receipt: [], invoice: [], price: [] };
    const answer = (list: Array<string | null>, request: Request, body: Record<string, unknown>, status = 201) => {
      list.push(request.headers.get("Idempotency-Key"));
      return list.length === 1 ? HttpResponse.error() : HttpResponse.json(body, { status });
    };
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), ({ request }) => {
        keys.receipt?.push(request.headers.get("Idempotency-Key"));
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 });
      }),
      http.post(apiUrl("/supplier-documents"), ({ request }) => answer(keys.invoice ?? [], request, { id: "sd-1", document_no: "HD-1" })),
      http.post(apiUrl("/skus/sku-1/sale-price"), ({ request }) => answer(keys.price ?? [], request, {}, 200)),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = () => screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    await user.type(within(dialog()).getByRole("textbox", { name: "Số hóa đơn NCC (nếu có)" }), "HD-1");
    const sell = within(dialog()).getByRole("textbox", { name: "Giá bán lẻ – Bánh quy bơ" });
    await user.clear(sell);
    await user.type(sell, "18000");
    const post = () => user.click(within(dialog()).getByRole("button", { name: "Ghi sổ nhập kho" }));
    await post();
    expect(await screen.findByText(/nhưng chưa rõ đã gắn hóa đơn NCC hay chưa/)).toBeInTheDocument();
    await post();
    await waitFor(() => expect(keys.price).toHaveLength(1));
    await post();
    await waitFor(() => expect(onClose).toHaveBeenCalled());

    const [receipt] = keys.receipt ?? [];
    expect(keys.receipt).toEqual([receipt]);
    expect(receipt).toBeTruthy();
    expect(keys.invoice).toEqual([`${receipt}:invoice`, `${receipt}:invoice`]);
    expect(keys.price).toEqual([`${receipt}:sale-price:sku-1:retail:general:purchase_order:18000:1`, `${receipt}:sale-price:sku-1:retail:general:purchase_order:18000:1`]);
  });

  it("resends an invoice that got no answer even after its number was cleared, and claims the match only once it is answered", async () => {
    const invoices: Array<{ key: string | null; body: unknown }> = [];
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.post(apiUrl("/receipts"), () =>
        HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "120000", tax_amount: "0" }, { status: 201 }),
      ),
      http.post(apiUrl("/supplier-documents"), async ({ request }) => {
        invoices.push({ key: request.headers.get("Idempotency-Key"), body: await request.json() });
        return invoices.length === 1 ? HttpResponse.error() : HttpResponse.json({ id: "sd-1", document_no: "HD-1" }, { status: 201 });
      }),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={testOrder()} onClose={onClose} />);

    await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const dialog = () => screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    const invoiceNo = within(dialog()).getByRole("textbox", { name: "Số hóa đơn NCC (nếu có)" });
    await user.type(invoiceNo, "HD-1");
    const post = async () => user.click(await within(dialog()).findByRole("button", { name: "Ghi sổ nhập kho" }));
    await post();
    expect(await screen.findByText(/nhưng chưa rõ đã gắn hóa đơn NCC hay chưa/)).toBeInTheDocument();
    expect(screen.queryByText(PROCUREMENT_MESSAGES.invoiceMatchedAsSent("HD-1"), { exact: false })).not.toBeInTheDocument();

    await user.clear(invoiceNo);
    await post();
    expect(await screen.findByText(PROCUREMENT_MESSAGES.invoiceMatchedAsSent("HD-1"), { exact: false })).toBeInTheDocument();
    expect(invoices).toHaveLength(2);
    expect(invoices[1]).toEqual(invoices[0]);
    expect(invoices[0]?.body).toMatchObject({ document_no: "HD-1" });
    expect(onClose).not.toHaveBeenCalled();
  });

  it("asks before discarding a receipt with goods entered, and closes an untouched one at once", async () => {
    server.use(...lookups);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={null} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });

    await user.click(within(dialog).getByRole("button", { name: "+ Hàng hóa mới" }));
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(PROCUREMENT_MESSAGES.discardReceipt);
    expect(onClose).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Ở lại" }));
    expect(within(dialog).getByRole("textbox", { name: "Tên hàng mới" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(within(dialog).getByRole("alert")).toHaveTextContent(PROCUREMENT_MESSAGES.discardReceipt);
    await user.click(within(dialog).getByRole("button", { name: "Vẫn đóng" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes a quick receipt without asking while it holds no goods", async () => {
    server.use(...lookups);
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={null} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });

    await user.click(within(dialog).getByRole("button", { name: "+ Hàng có sẵn" }));
    await user.click(within(dialog).getByRole("button", { name: "Hủy" }));
    expect(screen.queryByText(PROCUREMENT_MESSAGES.discardReceipt)).not.toBeInTheDocument();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("asks a quick receipt for its supplier first", async () => {
    server.use(...lookups);
    const user = userEvent.setup();
    renderWithProviders(<NewReceiptDialog order={null} onClose={() => {}} />);

    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });
    expect(within(dialog).getByText("Không cần đơn nhập. Chọn hàng có sẵn hoặc thêm hàng hóa mới.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));
    expect(await screen.findByText(/Chọn nhà cung cấp\./)).toBeInTheDocument();
  });

  it("counts phone scans on the ordered line in base units, the first replacing the prefilled quantity, and nothing for a code outside the order", async () => {
    const other = testSku({ id: "sku-2", code: "SP002", display_name: "Kẹo dừa" });
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.get(apiUrl("/sku-lookup"), ({ request }) => {
        const code = new URL(request.url).searchParams.get("code");
        if (code === "18935000000014") return HttpResponse.json({ sku: testSku(), unit_id: UNIT_THUNG, unit_name: "thùng", factor: "12", matched_by: "barcode" });
        if (code === "SP002") return HttpResponse.json({ sku: other, unit_id: UNIT_HOP, unit_name: "hộp", factor: "1", matched_by: "sku_code" });
        return apiError(404, "code_not_found");
      }),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    renderWithProviders(withScanner(<NewReceiptDialog order={testOrder()} onClose={() => {}} />, streams, memorySessionStore("ss-1")));
    const qty = await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const stream = await pairedStream(streams, "Pixel 8");
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    expect(within(dialog).getByRole("region", { name: "Quét bằng điện thoại" })).toBeInTheDocument();

    expect(qty).toHaveValue("24");
    stream.emit("scan", { scan_id: "scan-1", code: "18935000000014", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_THUNG });
    expect(qty).toHaveValue("12");
    stream.emit("scan", { scan_id: "scan-2", code: "18935000000014", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(api.results[1]?.body).toEqual({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_THUNG });
    expect(qty).toHaveValue("24");

    stream.emit("scan", { scan_id: "scan-3", code: "SP002", ttl_ms: 10_000 });
    stream.emit("scan", { scan_id: "scan-4", code: "8936000111222", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(4));
    expect(api.results.slice(2).map((r) => r.body)).toEqual([{ outcome: "not_in_order" }, { outcome: "not_in_order" }]);
    expect(await screen.findByText(PROCUREMENT_MESSAGES.scanNotInOrder("SP002"))).toBeInTheDocument();
    expect(await screen.findByText(PROCUREMENT_MESSAGES.scanNotInOrder("8936000111222"))).toBeInTheDocument();
    expect(within(dialog).getAllByRole("textbox", { name: /^Số lượng – / })).toHaveLength(1);
    expect(qty).toHaveValue("24");
    expect(within(dialog).queryByRole("textbox", { name: "Tên hàng mới" })).not.toBeInTheDocument();
  });
  it("fills the ordered lines of one SKU in order, each up to what is pending, then adds to the first", async () => {
    const line = (id: string, colorId: string) => testOrderLine({ id, color_id: colorId, unit_id: UNIT_HOP, unit_name: "hộp", unit_factor: "1", qty: "2", base_qty: "2", unit_price: "10000", line_amount: "20000", pending_base_qty: "2" });
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [line("pol-1", "color-do"), line("pol-2", "color-xanh")] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.get(apiUrl("/sku-lookup"), () => HttpResponse.json({ sku: testSku(), unit_id: UNIT_HOP, unit_name: "hộp", factor: "1", matched_by: "barcode" })),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    renderWithProviders(withScanner(<NewReceiptDialog order={testOrder()} onClose={() => {}} />, streams, memorySessionStore("ss-1")));
    await screen.findAllByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const stream = await pairedStream(streams, "Pixel 8");
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });
    const qtys = () => within(dialog).getAllByRole<HTMLInputElement>("textbox", { name: "Số lượng – Bánh quy bơ" }).map((el) => el.value);
    expect(qtys()).toEqual(["2", "2"]);

    const seen: string[][] = [];
    for (let n = 1; n <= 5; n++) {
      stream.emit("scan", { scan_id: `scan-${n}`, code: "8935000000017", ttl_ms: 10_000 });
      await waitFor(() => expect(api.results).toHaveLength(n));
      seen.push(qtys());
    }
    expect(api.results.every((r) => r.body.outcome === "added")).toBe(true);
    expect(seen).toEqual([
      ["1", "2"],
      ["2", "2"],
      ["2", "1"],
      ["2", "2"],
      ["3", "2"],
    ]);
  });

  it("turns a phone's code away while the receipt is being posted, so no scanned goods are left out of it", async () => {
    const hold = () => {
      let release!: () => void;
      const answer = new Promise<void>((resolve) => {
        release = resolve;
      });
      return { answer, release };
    };
    const lookup = hold();
    const receipt = hold();
    const lookups_: string[] = [];
    const posted: unknown[] = [];
    server.use(
      ...lookups,
      http.get(apiUrl("/purchase-orders/po-1/receivable-lines"), () => HttpResponse.json({ data: [testOrderLine()] })),
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [testSku()] })),
      http.get(apiUrl("/sku-lookup"), async ({ request }) => {
        lookups_.push(new URL(request.url).searchParams.get("code") ?? "");
        await lookup.answer;
        return HttpResponse.json({ sku: testSku(), unit_id: UNIT_THUNG, unit_name: "thùng", factor: "12", matched_by: "barcode" });
      }),
      http.post(apiUrl("/receipts"), async ({ request }) => {
        posted.push(await request.json());
        await receipt.answer;
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "240000", tax_amount: "0" }, { status: 201 });
      }),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(withScanner(<NewReceiptDialog order={testOrder()} onClose={onClose} />, streams, memorySessionStore("ss-1")));
    const qty = await screen.findByRole("textbox", { name: "Số lượng – Bánh quy bơ" });
    const stream = await pairedStream(streams, "Pixel 8");
    const dialog = screen.getByRole("dialog", { name: "Nhận hàng theo PO-0001" });

    // A code still being looked up when the post starts, and one arriving while it runs (the
    // scanner hands codes over one at a time, so it waits for the first).
    stream.emit("scan", { scan_id: "scan-1", code: "18935000000014", ttl_ms: 10_000 });
    await waitFor(() => expect(lookups_).toHaveLength(1));
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    await waitFor(() => expect(dialog.querySelector('button[aria-busy="true"]')).not.toBeNull());
    stream.emit("scan", { scan_id: "scan-2", code: "18935000000014", ttl_ms: 10_000 });
    lookup.release();
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(api.results.map((r) => [r.scanId, r.body])).toEqual([
      ["scan-1", { outcome: "not_accepting" }],
      ["scan-2", { outcome: "not_accepting" }],
    ]);
    expect(lookups_).toHaveLength(1);
    expect(qty).toHaveValue("24");

    receipt.release();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(posted).toEqual([{ purchase_order_id: "po-1", paid_now_amount: "0", lines: [{ purchase_order_line_id: "pol-1", qty: "2" }] }]);
  });

  it("turns a phone's code away while another dialog is open over the receipt, and takes it again once that one closes", async () => {
    server.use(
      ...lookups,
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/sku-lookup"), () => HttpResponse.json({ sku: testSku(), unit_id: UNIT_HOP, unit_name: "hộp", factor: "1", matched_by: "barcode" })),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    const user = userEvent.setup();
    renderWithProviders(withScanner(<NewReceiptDialog order={null} onClose={() => {}} />, streams, memorySessionStore("ss-1")));
    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });
    const stream = await pairedStream(streams, "Pixel 8");

    await user.click(within(dialog).getByRole("button", { name: "+ NCC mới" }));
    const quick = await screen.findByRole("dialog", { name: "Thêm nhà cung cấp mới" });
    stream.emit("scan", { scan_id: "scan-1", code: "8935000000017", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "not_accepting" });

    await user.click(within(quick).getByRole("button", { name: "Quay lại phiếu" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Thêm nhà cung cấp mới" })).not.toBeInTheDocument());
    expect(within(dialog).queryByRole("textbox", { name: "Số lượng – Bánh quy bơ" })).not.toBeInTheDocument();
    stream.emit("scan", { scan_id: "scan-2", code: "8935000000017", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(api.results[1]?.body).toEqual({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_HOP });
    expect(await within(dialog).findByRole("textbox", { name: "Số lượng – Bánh quy bơ" })).toHaveValue("1");
  });

  it("posts a quick receipt with a new product opened by a phone scan, its code as the barcode", async () => {
    const posted: unknown[] = [];
    server.use(
      ...lookups,
      http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [] })),
      http.get(apiUrl("/suppliers"), () => HttpResponse.json({ data: [SUPPLIER], next_cursor: null })),
      http.get(apiUrl("/sku-lookup"), () => apiError(404, "code_not_found")),
      http.post(apiUrl("/receipts"), async ({ request }) => {
        posted.push(await request.json());
        return HttpResponse.json({ id: "rcv-9", doc_no: "PN-0009", supplier_party_id: SUPPLIER_ID, total_amount: "96000", tax_amount: "0" }, { status: 201 });
      }),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(withScanner(<NewReceiptDialog order={null} onClose={onClose} />, streams, memorySessionStore("ss-1")));
    const dialog = await screen.findByRole("dialog", { name: "Nhập nhanh" });
    const stream = await pairedStream(streams, "Pixel 8");

    await user.type(within(dialog).getByRole("combobox", { name: "Nhà cung cấp" }), "Hải Hà");
    await user.click(await screen.findByRole("option", { name: /Công ty Bánh kẹo Hải Hà/ }));

    stream.emit("scan", { scan_id: "scan-1", code: "8936000111222", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "new_product" });
    const name = await within(dialog).findByRole("textbox", { name: "Tên hàng mới" });
    await waitFor(() => expect(name).toHaveFocus());
    await user.type(name, "Kẹo dẻo");
    expect(within(dialog).getByRole("textbox", { name: "Mã vạch – Kẹo dẻo" })).toHaveValue("8936000111222");
    await user.click(within(dialog).getByRole("checkbox", { name: "Quản lý lô/hạn – Kẹo dẻo" }));
    await user.type(within(dialog).getByRole("textbox", { name: "Đơn giá – Kẹo dẻo" }), "96000");
    await user.click(within(dialog).getByRole("button", { name: "Ghi sổ nhập kho" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(posted).toEqual([
      {
        supplier_party_id: SUPPLIER_ID,
        paid_now_amount: "0",
        lines: [
          {
            unit_id: UNIT_HOP,
            qty: "1",
            unit_price: "96000",
            new_product: { name: "Kẹo dẻo", base_unit_id: UNIT_HOP, units: [{ unit_id: UNIT_THUNG, factor: "12" }], purchase_unit_id: UNIT_HOP, track_lot: false, barcode: "8936000111222" },
          },
        ],
      },
    ]);
  });
});
