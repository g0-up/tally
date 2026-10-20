import { useState } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { scannerServer } from "../../../test/msw/scanner";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { createFakeStreams } from "../../scanner/testing/fake-event-stream";
import { memorySessionStore, pairedStream, withScanner } from "../../scanner/testing/scanner-harness";
import { emptyBuyLine, withSku, type BuyLine } from "../buy-lines";
import { TEST_UNITS, UNIT_HOP, UNIT_THUNG, testSku } from "../testing/fixtures";
import { BuyEditor } from "./buy-preset";

const HOP_BARCODE = "8935000000017";
const CARTON_BARCODE = "18935000000014";
const UNKNOWN_BARCODE = "8936000111222";

/** GET /sku-lookup: the test SKU by its code or hộp barcode in hộp, by its carton barcode in thùng of 12; any other code is not found. */
function serveLookup() {
  const sku = testSku();
  const [hop, thung] = sku.units;
  server.use(
    http.get(apiUrl("/sku-lookup"), ({ request }) => {
      const code = new URL(request.url).searchParams.get("code") ?? "";
      const unit = code === "SP001" || code === HOP_BARCODE ? hop : code === CARTON_BARCODE ? thung : undefined;
      if (unit === undefined) return apiError(404, "code_not_found");
      return HttpResponse.json({ sku, unit_id: unit.unit_id, unit_name: unit.unit_name, factor: unit.factor, matched_by: code === "SP001" ? "sku_code" : "barcode" });
    }),
  );
}

function Editor({ initial, receive = false }: { initial: BuyLine[]; receive?: boolean }) {
  const [lines, setLines] = useState(initial);
  return <BuyEditor lines={lines} onChange={setLines} receive={receive} />;
}

function renderEditor(initial: BuyLine[], receive = false) {
  server.use(http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })));
  return renderWithProviders(<Editor initial={initial} receive={receive} />);
}

/** The editor with a phone paired to this tab (session ss-1, approved); returns the stream its scans arrive on. */
async function renderEditorWithPhone(initial: BuyLine[]) {
  server.use(http.get(apiUrl("/units"), () => HttpResponse.json({ data: TEST_UNITS })));
  serveLookup();
  const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
  const streams = createFakeStreams();
  const { platform } = renderWithProviders(withScanner(<Editor initial={initial} />, streams, memorySessionStore("ss-1")));
  await waitFor(() => expect(platform.queryClient.getQueryState(["catalog", "lookup", "units"])?.status).toBe("success"));
  const stream = await pairedStream(streams, "Pixel 8");
  let n = 0;
  const scan = (code: string) => stream.emit("scan", { scan_id: `scan-${++n}`, code, ttl_ms: 10_000 });
  return { api, scan };
}

const qtys = (name: string) => screen.getAllByRole("textbox", { name: `Số lượng – ${name}` }).map((el) => (el as HTMLInputElement).value);

describe("BuyEditor", () => {
  it("adds an inline new product with the prototype units and names its row controls after it", async () => {
    const user = userEvent.setup();
    const { platform } = renderEditor([]);
    await waitFor(() => expect(platform.queryClient.getQueryState(["catalog", "lookup", "units"])?.status).toBe("success"));

    await user.click(screen.getByRole("button", { name: "+ Hàng hóa mới" }));

    expect(screen.getByText("Hàng mới", { selector: ".tag" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Đơn vị cơ sở – Hàng mới" })).toHaveValue(UNIT_HOP);
    expect(screen.getByRole("combobox", { name: "Đơn vị lớn – Hàng mới" })).toHaveValue(UNIT_THUNG);
    expect(screen.getByRole("checkbox", { name: "Quản lý lô/hạn – Hàng mới" })).toBeChecked();

    await user.type(screen.getByRole("textbox", { name: "Tên hàng mới" }), "Kẹo dẻo");
    expect(screen.getByRole("textbox", { name: "Số lượng – Kẹo dẻo" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Đơn giá – Kẹo dẻo" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Xóa – Kẹo dẻo" }));
    expect(screen.queryByRole("textbox", { name: "Tên hàng mới" })).not.toBeInTheDocument();
  });

  it("turns a search that finds nothing into a new product named after the typed text", async () => {
    const user = userEvent.setup();
    server.use(http.get(apiUrl("/skus"), () => HttpResponse.json({ data: [] })));
    const { platform } = renderEditor([emptyBuyLine()]);
    await waitFor(() => expect(platform.queryClient.getQueryState(["catalog", "lookup", "units"])?.status).toBe("success"));

    await user.click(screen.getByRole("combobox", { name: "Tên hàng – dòng 1" }));
    await user.type(screen.getByRole("combobox", { name: "Tên hàng – dòng 1" }), "Kẹo gừng");
    await user.click(await screen.findByRole("option", { name: "+ Tạo hàng mới “Kẹo gừng”" }));

    expect(screen.getByText("Hàng mới", { selector: ".tag" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Tên hàng mới" })).toHaveValue("Kẹo gừng");
    expect(screen.getByRole("textbox", { name: "Tên hàng mới" })).toHaveFocus();
    expect(screen.getByRole("combobox", { name: "Đơn vị cơ sở – Kẹo gừng" })).toHaveValue(UNIT_HOP);
    expect(screen.queryByRole("combobox", { name: "Tên hàng – dòng 1" })).not.toBeInTheDocument();
  });

  it("asks for lot and expiry only on lot-tracked rows when receiving", async () => {
    const tracked = withSku(emptyBuyLine(), testSku({ id: "sku-lot", display_name: "Kẹo sữa", track_lot: true, track_expiry: true }));
    const plain = withSku(emptyBuyLine(), testSku());
    renderEditor([tracked, plain], true);

    expect(screen.getByRole("columnheader", { name: "Lô · hạn dùng" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Số lô – Kẹo sữa" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Số lô – Bánh quy bơ" })).not.toBeInTheDocument();
    const plainRow = screen.getByRole("textbox", { name: "Số lượng – Bánh quy bơ" }).closest("tr");
    expect(plainRow).not.toBeNull();
    expect(within(plainRow as HTMLElement).getByText("—")).toBeInTheDocument();
  });

  it("names the table and its action column, and tells unpicked rows apart by number", async () => {
    const user = userEvent.setup();
    renderEditor([withSku(emptyBuyLine(), testSku()), emptyBuyLine()]);

    const table = screen.getByRole("table", { name: "Hàng nhập" });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.at(-1)).toHaveAccessibleName("Thao tác");
    expect(headers.every((h) => h.getAttribute("scope") === "col")).toBe(true);

    expect(within(table).getByRole("combobox", { name: "Tên hàng – Bánh quy bơ" })).toBeInTheDocument();
    expect(within(table).getByRole("combobox", { name: "Tên hàng – dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Số lượng – dòng 2" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Xóa – Bánh quy bơ" })).toBeInTheDocument();

    await user.click(within(table).getByRole("button", { name: "Xóa – dòng 2" }));
    expect(within(table).queryByRole("textbox", { name: "Số lượng – dòng 2" })).not.toBeInTheDocument();
    expect(within(table).getByRole("textbox", { name: "Số lượng – Bánh quy bơ" })).toBeInTheDocument();
  });

  it("has no lot column on an order", () => {
    renderEditor([withSku(emptyBuyLine(), testSku({ track_lot: true }))]);
    expect(screen.queryByRole("columnheader", { name: "Lô · hạn dùng" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /^Số lô/ })).not.toBeInTheDocument();
  });

  it("shows the phone pairing card above the lines", async () => {
    await renderEditorWithPhone([emptyBuyLine()]);
    const card = screen.getByRole("region", { name: "Quét bằng điện thoại" });
    expect(card).toHaveTextContent("Điện thoại đã kết nối · Pixel 8");
    expect(card.compareDocumentPosition(screen.getByRole("table", { name: "Hàng nhập" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("adds a scanned SKU in the unit of its code, counts it again on a rescan and keeps a carton code on its own row", async () => {
    const { api, scan } = await renderEditorWithPhone([emptyBuyLine()]);

    scan(HOP_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_HOP });
    expect(qtys("Bánh quy bơ")).toEqual(["1"]);
    expect(screen.getByRole("combobox", { name: "Đơn vị tính – Bánh quy bơ" })).toHaveValue(UNIT_HOP);
    expect(screen.queryByRole("combobox", { name: "Tên hàng – dòng 1" })).not.toBeInTheDocument();

    scan("SP001");
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(qtys("Bánh quy bơ")).toEqual(["2"]);

    scan(CARTON_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(3));
    expect(api.results[2]?.body).toEqual({ outcome: "added", sku_id: "sku-1", unit_id: UNIT_THUNG });
    expect(qtys("Bánh quy bơ")).toEqual(["2", "1"]);
    expect(screen.getAllByRole("combobox", { name: "Đơn vị tính – Bánh quy bơ" }).map((el) => (el as HTMLSelectElement).value)).toEqual([UNIT_HOP, UNIT_THUNG]);
  });

  it("opens an unknown code as a new product with that barcode, counts its rescans and leaves its big unit to the desktop", async () => {
    const user = userEvent.setup();
    const { api, scan } = await renderEditorWithPhone([emptyBuyLine()]);

    scan(UNKNOWN_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "new_product" });
    expect(screen.getByRole("textbox", { name: "Mã vạch – Hàng mới" })).toHaveValue(UNKNOWN_BARCODE);
    expect(screen.getByRole("textbox", { name: "Tên hàng mới" })).toHaveFocus();
    expect(screen.getByRole("combobox", { name: "Đơn vị tính – Hàng mới" })).toHaveValue("base");
    expect(qtys("Hàng mới")).toEqual(["1"]);

    scan(UNKNOWN_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(api.results[1]?.body).toEqual({ outcome: "new_product" });
    expect(qtys("Hàng mới")).toEqual(["2"]);

    await user.selectOptions(screen.getByRole("combobox", { name: "Đơn vị tính – Hàng mới" }), "big");
    scan(UNKNOWN_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(3));
    expect(api.results[2]?.body).toEqual({ outcome: "needs_desktop_edit" });
    expect(qtys("Hàng mới")).toEqual(["2"]);
    expect(screen.getAllByRole("textbox", { name: "Tên hàng mới" })).toHaveLength(1);
  });

  it("counts two codes that arrive back to back", async () => {
    const { api, scan } = await renderEditorWithPhone([emptyBuyLine()]);

    scan(UNKNOWN_BARCODE);
    scan(UNKNOWN_BARCODE);
    scan(HOP_BARCODE);
    scan(HOP_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(4));
    const outcomes = Object.fromEntries(api.results.map((r) => [r.scanId, r.body.outcome]));
    expect(outcomes).toEqual({ "scan-1": "new_product", "scan-2": "new_product", "scan-3": "added", "scan-4": "added" });
    expect(qtys("Hàng mới")).toEqual(["2"]);
    expect(qtys("Bánh quy bơ")).toEqual(["2"]);
  });

  it("says when a scanned code cannot be looked up and adds nothing", async () => {
    const { api, scan } = await renderEditorWithPhone([emptyBuyLine()]);
    server.use(http.get(apiUrl("/sku-lookup"), () => apiError(500, "internal", { message: "Máy chủ lỗi" })));

    scan(HOP_BARCODE);
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "not_found" });
    expect(await screen.findByText(new RegExp(`^Không tra được mã “${HOP_BARCODE}”: `))).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Tên hàng – dòng 1" })).toBeInTheDocument();
  });
});
