import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { apiError, apiUrl } from "../../../test/msw/handlers";
import { scannerServer } from "../../../test/msw/scanner";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import { testSku } from "../../procurement/testing/fixtures";
import { createFakeStreams } from "../../scanner/testing/fake-event-stream";
import { memorySessionStore, pairedStream, withScanner } from "../../scanner/testing/scanner-harness";
import { bootstrapHandlers, syncExceptionsHandler } from "../offline/testing/handlers";
import PosPage from "./pos-page";

/** The handlers every POS render needs: the tiles, no saved carts, no exceptions, no prices. */
function posHandlers() {
  return [
    ...bootstrapHandlers(),
    syncExceptionsHandler(() => []),
    http.get(apiUrl("/pos/carts"), () => HttpResponse.json({ data: [], next_cursor: null })),
    http.post(apiUrl("/pricing/quote"), () => HttpResponse.json({ data: [] })),
  ];
}

const BANH_QUY = "8936000111222";
const BANH_GAO = "8936000333444";

/**
 * GET /sku-lookup knowing two barcodes no tile carries; every answer waits for `release()`. The
 * codes asked for are kept in order.
 */
function heldLookup() {
  const codes: string[] = [];
  let release: () => void = () => undefined;
  const answered = new Promise<void>((resolve) => {
    release = resolve;
  });
  const skus: Record<string, ReturnType<typeof testSku>> = {
    [BANH_QUY]: testSku(),
    [BANH_GAO]: testSku({ id: "sku-2", code: "SP002", display_name: "Bánh gạo" }),
  };
  server.use(
    http.get(apiUrl("/sku-lookup"), async ({ request }) => {
      const code = new URL(request.url).searchParams.get("code") ?? "";
      codes.push(code);
      await answered;
      const sku = skus[code];
      if (sku === undefined) return apiError(404, "code_not_found");
      const unit = sku.units[0];
      return HttpResponse.json({ sku, unit_id: unit?.unit_id, unit_name: unit?.unit_name, factor: "1", matched_by: "barcode" });
    }),
  );
  return { codes, release };
}

async function renderPos() {
  renderWithProviders(<PosPage />, { path: "/sales/pos" });
  const box = await screen.findByRole("textbox", { name: "Tìm hoặc quét hàng" });
  await screen.findByRole("button", { name: /Mứt gừng/ });
  return box;
}

describe("PosPage scan box", () => {
  it("sends a code once when Enter is pressed twice before the server answers", async () => {
    server.use(...posHandlers());
    const lookup = heldLookup();
    const box = await renderPos();

    await userEvent.type(box, `${BANH_QUY}{Enter}{Enter}`);
    expect(box).toHaveValue("");
    lookup.release();
    const cart = await screen.findByRole("table", { name: "Giỏ hàng" });
    expect(await within(cart).findByRole("textbox", { name: /^Số lượng – .*Bánh quy bơ/ })).toHaveValue("1");
    expect(lookup.codes).toEqual([BANH_QUY]);
  });

  it("adds two codes scanned one right after the other, each on its own line", async () => {
    server.use(...posHandlers());
    const lookup = heldLookup();
    const box = await renderPos();

    await userEvent.type(box, `${BANH_QUY}{Enter}`);
    await userEvent.type(box, `${BANH_GAO}{Enter}`);
    expect(box).toHaveValue("");
    lookup.release();
    const cart = await screen.findByRole("table", { name: "Giỏ hàng" });
    expect(await within(cart).findByRole("textbox", { name: /^Số lượng – .*Bánh gạo/ })).toHaveValue("1");
    expect(within(cart).getByRole("textbox", { name: /^Số lượng – .*Bánh quy bơ/ })).toHaveValue("1");
    expect(lookup.codes).toEqual([BANH_QUY, BANH_GAO]);
  });

  it("puts a code that adds nothing back in the box", async () => {
    server.use(...posHandlers());
    const lookup = heldLookup();
    const box = await renderPos();

    await userEvent.type(box, "8936000999999{Enter}");
    expect(box).toHaveValue("");
    lookup.release();
    await waitFor(() => expect(box).toHaveValue("8936000999999"));
    expect(lookup.codes).toEqual(["8936000999999"]);
  });

  it("finds a name typed with a space among the tiles without asking the server", async () => {
    server.use(...posHandlers());
    const lookup = heldLookup();
    const box = await renderPos();

    await userEvent.type(box, "kẹo dừa{Enter}");
    const cart = await screen.findByRole("table", { name: "Giỏ hàng" });
    expect(await within(cart).findByRole("button", { name: /^Xóa Kẹo dừa/ })).toBeInTheDocument();
    expect(box).toHaveValue("");
    expect(lookup.codes).toEqual([]);
  });
});

describe("PosPage with a paired phone", () => {
  it("adds the line a phone scans and leaves the text typed in the scan box alone", async () => {
    server.use(
      ...bootstrapHandlers(),
      syncExceptionsHandler(() => []),
      http.get(apiUrl("/pos/carts"), () => HttpResponse.json({ data: [], next_cursor: null })),
      http.post(apiUrl("/pricing/quote"), () => HttpResponse.json({ data: [] })),
    );
    const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
    const streams = createFakeStreams();
    renderWithProviders(withScanner(<PosPage />, streams, memorySessionStore("ss-1")), { path: "/sales/pos" });

    const box = await screen.findByRole("textbox", { name: "Tìm hoặc quét hàng" });
    await screen.findByRole("button", { name: /Mứt gừng/ });
    await userEvent.type(box, "kẹo");
    const stream = await pairedStream(streams, "Pixel 8");
    expect(screen.getByRole("region", { name: "Quét bằng điện thoại" })).toHaveTextContent("Điện thoại đã kết nối · Pixel 8");

    stream.emit("scan", { scan_id: "scan-1", code: "8935000000024", ttl_ms: 10_000 });
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]).toEqual({ sessionId: "ss-1", scanId: "scan-1", body: { outcome: "added", sku_id: "sku-mut", unit_id: "unit-hop" } });
    const cart = screen.getByRole("table", { name: "Giỏ hàng" });
    expect(await within(cart).findByRole("button", { name: /^Xóa Mứt gừng/ })).toBeInTheDocument();
    expect(within(cart).queryByRole("button", { name: /^Xóa Kẹo dừa/ })).not.toBeInTheDocument();
    expect(box).toHaveValue("kẹo");
  });
});
