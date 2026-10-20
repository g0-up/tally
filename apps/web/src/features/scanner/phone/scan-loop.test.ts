import { describe, expect, it, vi } from "vitest";
import type { BarcodeReader } from "./barcode-reader";
import { ABSENT_MS, MAX_READ_FAILURES, SCAN_INTERVAL_MS, createCodeFilter, startScanLoop, type FrameScheduler } from "./scan-loop";

describe("createCodeFilter", () => {
  it("sends a code once while it stays in view", () => {
    const filter = createCodeFilter();
    expect(filter(["8934563138165"], 0)).toEqual(["8934563138165"]);
    // Still in view frame after frame, long past the absence window counted from the first sight.
    for (let t = 125; t <= 5_000; t += 125) expect(filter(["8934563138165"], t)).toEqual([]);
  });

  it("sends it again after it was out of view for 800 ms", () => {
    const filter = createCodeFilter();
    expect(filter(["123"], 0)).toEqual(["123"]);
    expect(filter([], 400)).toEqual([]);
    expect(filter(["123"], ABSENT_MS - 1)).toEqual([]);
    expect(filter([], 1_000)).toEqual([]);
    expect(filter(["123"], ABSENT_MS - 1 + ABSENT_MS)).toEqual(["123"]);
  });

  it("measures the absence from the last time the code was seen", () => {
    const filter = createCodeFilter();
    filter(["123"], 0);
    filter(["123"], 700);
    expect(filter(["123"], 1_400)).toEqual([]);
    expect(filter(["123"], 2_200)).toEqual(["123"]);
  });

  it("keeps several codes of one frame in order, trimmed, once each, without empties", () => {
    const filter = createCodeFilter();
    expect(filter([" 111 ", "222", "111", "", "   "], 0)).toEqual(["111", "222"]);
    expect(filter(["222", "333"], 125)).toEqual(["333"]);
  });

  it("sends a code exactly as read apart from the spaces", () => {
    const filter = createCodeFilter();
    expect(filter(["0012345678905"], 0)).toEqual(["0012345678905"]);
    expect(filter(["012345678905"], 0)).toEqual(["012345678905"]);
  });
});

/** Frames driven by the test: `frame()` runs the callback the loop asked for. */
function manualFrames() {
  let pending: (() => void) | null = null;
  const requestFrame: FrameScheduler = (_video, cb) => {
    pending = cb;
    return () => {
      if (pending === cb) pending = null;
    };
  };
  return {
    requestFrame,
    frame() {
      const cb = pending;
      pending = null;
      cb?.();
    },
    get waiting() {
      return pending !== null;
    },
  };
}

function videoWithFrame(readyState = 4): HTMLVideoElement {
  const video = document.createElement("video");
  Object.defineProperty(video, "readyState", { value: readyState, configurable: true });
  return video;
}

const flush = () => new Promise<void>((r) => setTimeout(r, 0));

describe("startScanLoop", () => {
  it("reads at most about 8 times a second and passes new codes on", async () => {
    let now = 0;
    const detect = vi.fn<BarcodeReader["detect"]>().mockResolvedValue(["123"]);
    const frames = manualFrames();
    const onCodes = vi.fn();
    const stop = startScanLoop({ video: videoWithFrame(), reader: { kind: "wasm", detect }, onCodes, onFailure: vi.fn(), requestFrame: frames.requestFrame, now: () => now, isHidden: () => false });

    frames.frame();
    await flush();
    expect(detect).toHaveBeenCalledTimes(1);
    expect(onCodes).toHaveBeenCalledWith(["123"]);

    now = SCAN_INTERVAL_MS - 1;
    frames.frame();
    await flush();
    expect(detect).toHaveBeenCalledTimes(1);

    now = SCAN_INTERVAL_MS;
    frames.frame();
    await flush();
    expect(detect).toHaveBeenCalledTimes(2);
    // Same code still in view: not passed on again.
    expect(onCodes).toHaveBeenCalledTimes(1);
    stop();
    expect(frames.waiting).toBe(false);
  });

  it("never starts a read while one is running", async () => {
    let now = 0;
    let release: (codes: string[]) => void = () => undefined;
    const detect = vi.fn<BarcodeReader["detect"]>(() => new Promise<string[]>((r) => (release = r)));
    const frames = manualFrames();
    startScanLoop({ video: videoWithFrame(), reader: { kind: "native", detect }, onCodes: vi.fn(), onFailure: vi.fn(), requestFrame: frames.requestFrame, now: () => now, isHidden: () => false });
    frames.frame();
    now = 1_000;
    frames.frame();
    frames.frame();
    expect(detect).toHaveBeenCalledTimes(1);
    release([]);
    await flush();
    now = 2_000;
    frames.frame();
    expect(detect).toHaveBeenCalledTimes(2);
  });

  it("skips reading while the tab is hidden or the video has no frame", async () => {
    let hidden = true;
    const detect = vi.fn<BarcodeReader["detect"]>().mockResolvedValue([]);
    const frames = manualFrames();
    startScanLoop({ video: videoWithFrame(1), reader: { kind: "wasm", detect }, onCodes: vi.fn(), onFailure: vi.fn(), requestFrame: frames.requestFrame, isHidden: () => hidden });
    frames.frame();
    hidden = false;
    frames.frame();
    expect(detect).not.toHaveBeenCalled();
    expect(frames.waiting).toBe(true);
  });

  it("tolerates a failed read and stops after five in a row", async () => {
    let now = 0;
    const detect = vi.fn<BarcodeReader["detect"]>().mockRejectedValue(new Error("decode failed"));
    const frames = manualFrames();
    const onFailure = vi.fn();
    startScanLoop({ video: videoWithFrame(), reader: { kind: "wasm", detect }, onCodes: vi.fn(), onFailure, requestFrame: frames.requestFrame, now: () => now, isHidden: () => false });
    for (let i = 0; i < MAX_READ_FAILURES - 1; i++) {
      frames.frame();
      await flush();
      now += SCAN_INTERVAL_MS;
    }
    expect(onFailure).not.toHaveBeenCalled();
    frames.frame();
    await flush();
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(frames.waiting).toBe(false);
  });

  it("drops a read that finishes after the loop stopped", async () => {
    let release: (codes: string[]) => void = () => undefined;
    const detect = vi.fn<BarcodeReader["detect"]>(() => new Promise<string[]>((r) => (release = r)));
    const frames = manualFrames();
    const onCodes = vi.fn();
    const stop = startScanLoop({ video: videoWithFrame(), reader: { kind: "wasm", detect }, onCodes, onFailure: vi.fn(), requestFrame: frames.requestFrame, isHidden: () => false });
    frames.frame();
    stop();
    release(["123"]);
    await flush();
    expect(onCodes).not.toHaveBeenCalled();
  });
});
