import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ScanOutcome } from "../../shared/lib/barcode";
import { apiError } from "../../test/msw/handlers";
import { scannerServer } from "../../test/msw/scanner";
import { renderWithProviders } from "../../test/render";
import type { ScanHandler } from "./scanner-controller";
import { createFakeStreams, type FakeStream } from "./testing/fake-event-stream";
import { memorySessionStore, pairedStream, withScanner } from "./testing/scanner-harness";
import { useScanTarget } from "./use-scan-target";

afterEach(() => {
  vi.useRealTimers();
});

const added = (skuId: string): ScanOutcome => ({ outcome: "added", skuId, unitId: "unit-hop" });
const addedBody = (skuId: string) => ({ outcome: "added", sku_id: skuId, unit_id: "unit-hop" });

function Target({ handler }: { handler: ScanHandler | null }) {
  useScanTarget(handler);
  return null;
}

/** A screen paired with a phone (session ss-1, approved) and the stream its scans arrive on. */
async function paired(ui: ReactElement) {
  const api = scannerServer({ "ss-1": { state: "claimed", device_label: "Pixel 8" } });
  const streams = createFakeStreams();
  renderWithProviders(withScanner(ui, streams, memorySessionStore("ss-1")));
  const stream = await pairedStream(streams);
  return { api, stream };
}

let seq = 0;
function scan(stream: FakeStream, code: string, { scanId = `scan-${++seq}`, ttl = 10_000 }: { scanId?: string; ttl?: number } = {}) {
  stream.emit("scan", { scan_id: scanId, code, ttl_ms: ttl });
  return scanId;
}

/** A handler whose answers the test gives, one per call, in call order. */
function heldHandler() {
  const answers: Array<(o: ScanOutcome) => void> = [];
  const handler = vi.fn<ScanHandler>(() => new Promise<ScanOutcome>((resolve) => answers.push(resolve)));
  const answer = (i: number, o: ScanOutcome) => act(() => answers[i]?.(o));
  return { handler, answer, waiting: () => answers.length };
}

describe("useScanTarget", () => {
  it("sends codes to the screen mounted last and back to the one below once it unmounts", async () => {
    const seen: string[] = [];
    function Page() {
      const [dialog, setDialog] = useState(true);
      return (
        <>
          <Target handler={(code) => (seen.push(`trang:${code}`), added("sku-page"))} />
          {dialog ? <Target handler={(code) => (seen.push(`hộp thoại:${code}`), added("sku-dialog"))} /> : null}
          <button type="button" onClick={() => setDialog(false)}>
            Đóng hộp thoại
          </button>
        </>
      );
    }
    const { api, stream } = await paired(<Page />);

    const first = scan(stream, "111");
    await waitFor(() => expect(api.results).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Đóng hộp thoại" }));
    const second = scan(stream, "222");
    await waitFor(() => expect(api.results).toHaveLength(2));

    expect(seen).toEqual(["hộp thoại:111", "trang:222"]);
    expect(api.results).toEqual([
      { sessionId: "ss-1", scanId: first, body: addedBody("sku-dialog") },
      { sessionId: "ss-1", scanId: second, body: addedBody("sku-page") },
    ]);
  });

  it("answers not_accepting when no screen takes codes", async () => {
    const { api, stream } = await paired(<p>Báo cáo</p>);
    const id = scan(stream, "111");
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]).toEqual({ sessionId: "ss-1", scanId: id, body: { outcome: "not_accepting" } });
  });

  it("answers not_accepting while the screen on top passes null", async () => {
    const page = vi.fn<ScanHandler>(() => added("sku-page"));
    const { api, stream } = await paired(
      <>
        <Target handler={page} />
        <Target handler={null} />
      </>,
    );
    scan(stream, "111");
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "not_accepting" });
    expect(page).not.toHaveBeenCalled();
  });

  it("calls the handler of the latest render, not the one registered at mount", async () => {
    function Versioned() {
      const [version, setVersion] = useState(1);
      useScanTarget(() => added(`sku-v${version}`));
      return (
        <button type="button" onClick={() => setVersion(2)}>
          Đổi phiên bản
        </button>
      );
    }
    const { api, stream } = await paired(<Versioned />);
    scan(stream, "111");
    await waitFor(() => expect(api.results).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Đổi phiên bản" }));
    scan(stream, "111");
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(api.results.map((r) => r.body)).toEqual([addedBody("sku-v1"), addedBody("sku-v2")]);
  });

  it("handles a scan sent twice once, and answers both with the same result", async () => {
    const held = heldHandler();
    const { api, stream } = await paired(<Target handler={held.handler} />);
    scan(stream, "111", { scanId: "scan-dup" });
    await waitFor(() => expect(held.waiting()).toBe(1));
    // The phone sends it again while the first is still being handled.
    scan(stream, "111", { scanId: "scan-dup" });
    await held.answer(0, added("sku-1"));
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(held.handler).toHaveBeenCalledTimes(1);
    expect(api.results).toEqual([
      { sessionId: "ss-1", scanId: "scan-dup", body: addedBody("sku-1") },
      { sessionId: "ss-1", scanId: "scan-dup", body: addedBody("sku-1") },
    ]);

    // Again after it finished: the same answer, still no second handling.
    scan(stream, "111", { scanId: "scan-dup" });
    await waitFor(() => expect(api.results).toHaveLength(3));
    expect(held.handler).toHaveBeenCalledTimes(1);
  });

  it("handles codes one at a time, in the order they arrived", async () => {
    const held = heldHandler();
    const { api, stream } = await paired(<Target handler={held.handler} />);
    scan(stream, "AAA");
    scan(stream, "BBB");
    await waitFor(() => expect(held.waiting()).toBe(1));
    await act(() => new Promise((r) => setTimeout(r, 30)));
    expect(held.handler.mock.calls).toEqual([["AAA"]]);

    await held.answer(0, added("sku-a"));
    await waitFor(() => expect(held.waiting()).toBe(2));
    expect(held.handler.mock.calls).toEqual([["AAA"], ["BBB"]]);
    await held.answer(1, added("sku-b"));
    await waitFor(() => expect(api.results).toHaveLength(2));
    expect(api.results.map((r) => r.body.sku_id)).toEqual(["sku-a", "sku-b"]);
  });

  it("skips a code whose time ran out while it waited, without handling or answering it", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"], shouldAdvanceTime: true });
    const held = heldHandler();
    const { api, stream } = await paired(<Target handler={held.handler} />);
    const first = scan(stream, "AAA");
    scan(stream, "BBB", { ttl: 1_000 });
    await waitFor(() => expect(held.waiting()).toBe(1));
    await act(() => vi.advanceTimersByTimeAsync(1_500));
    await held.answer(0, added("sku-a"));
    await waitFor(() => expect(api.results).toHaveLength(1));
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(held.handler.mock.calls).toEqual([["AAA"]]);
    expect(api.results.map((r) => r.scanId)).toEqual([first]);
  });

  it("gives up on a code still being handled when its time runs out, so the next one is handled", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"], shouldAdvanceTime: true });
    const held = heldHandler();
    const { api, stream } = await paired(<Target handler={held.handler} />);
    scan(stream, "AAA", { ttl: 2_000 });
    const second = scan(stream, "BBB");
    await waitFor(() => expect(held.waiting()).toBe(1));

    await act(() => vi.advanceTimersByTimeAsync(2_100));
    expect(await screen.findByText(/^Mã AAA từ điện thoại xử lý quá lâu nên đã bỏ qua\./)).toBeInTheDocument();
    await waitFor(() => expect(held.waiting()).toBe(2));
    expect(held.handler.mock.calls).toEqual([["AAA"], ["BBB"]]);

    await held.answer(1, added("sku-b"));
    await held.answer(0, added("sku-a"));
    await waitFor(() => expect(api.results).toHaveLength(1));
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(api.results.map((r) => r.scanId)).toEqual([second]);
  });

  it("answers not_found and says so when the handler fails", async () => {
    const { api, stream } = await paired(
      <Target
        handler={() => {
          throw new Error("hỏng");
        }}
      />,
    );
    scan(stream, "111");
    await waitFor(() => expect(api.results).toHaveLength(1));
    expect(api.results[0]?.body).toEqual({ outcome: "not_found" });
    expect(await screen.findByText(/Không xử lý được mã 111 từ điện thoại/)).toBeInTheDocument();
  });

  it("asks the cashier to check the line when the phone had given up on the code", async () => {
    const { api, stream } = await paired(<Target handler={() => added("sku-1")} />);
    api.onResult(() => apiError(409, "scan_expired"));
    scan(stream, "8935000000024");
    expect(await screen.findByText("Mã 8935000000024 từ điện thoại đến muộn. Kiểm tra lại dòng vừa thêm.")).toBeInTheDocument();
  });
});
