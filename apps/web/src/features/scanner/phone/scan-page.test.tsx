import { act, cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError } from "../../../test/msw/handlers";
import { phoneScanServer, type PhoneFixtureState } from "../../../test/msw/scanner";
import { renderApp, renderWithProviders } from "../../../test/render";
import { ScanScreen, type ScanDeps } from "../pages/scan-page";
import { createFakeStreams } from "../testing/fake-event-stream";
import type { BarcodeReader } from "./barcode-reader";
import type { Feedback } from "./feedback";
import type { FrameScheduler } from "./scan-loop";
import { CAMERA_CONSTRAINTS, CAMERA_ERROR_COPY } from "./use-camera";

beforeEach(() => {
  // jsdom plays no media: the video "plays" at once and always has a frame.
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
  vi.spyOn(HTMLMediaElement.prototype, "readyState", "get").mockReturnValue(4);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

/** A camera stream whose tracks record being stopped. */
function fakeCamera() {
  const track = { stop: vi.fn() };
  const stream = { getTracks: () => [track], getVideoTracks: () => [] } as unknown as MediaStream;
  return { getUserMedia: vi.fn(() => Promise.resolve(stream)), track };
}

/** A reader answering each read with the next batch of codes, then nothing. */
function fakeReader(...batches: string[][]): BarcodeReader {
  return { kind: "wasm", detect: () => Promise.resolve(batches.shift() ?? []) };
}

/** Video frames the test hands out one at a time. */
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
    frame: () =>
      act(() => {
        const cb = pending;
        pending = null;
        cb?.();
      }),
  };
}

function renderScan({ url = "/scan#c=CLAIM-1", state = "none", deps = {} }: { url?: string; state?: PhoneFixtureState; deps?: Partial<ScanDeps> } = {}) {
  window.history.replaceState(null, "", url);
  const api = phoneScanServer({ state });
  const streams = createFakeStreams();
  const camera = fakeCamera();
  const frames = manualFrames();
  const feedback = { unlock: vi.fn(), signal: vi.fn<Feedback["signal"]>() };
  renderWithProviders(
    <ScanScreen
      deps={{
        openStream: streams.open,
        getUserMedia: camera.getUserMedia,
        createReader: () => Promise.resolve(fakeReader(["8934563138165"])),
        createFeedback: (flash) => ({ unlock: feedback.unlock, signal: (tone) => (feedback.signal(tone), flash(tone)) }),
        requestFrame: frames.requestFrame,
        ...deps,
      }}
    />,
    { path: "/scan" },
  );
  return { api, streams, camera, frames, feedback };
}

const results = () => within(screen.getByRole("list"));

async function approvedWithCamera(options: Parameters<typeof renderScan>[0] = {}) {
  const ctx = renderScan(options);
  await screen.findByText("4821");
  // The desktop allows the phone: the server and its stream agree.
  ctx.api.session.state = "claimed";
  ctx.streams.last().emit("approved");
  await userEvent.click(screen.getByRole("button", { name: "Bật camera" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Bật camera" })).not.toBeInTheDocument());
  return ctx;
}

describe("ScanScreen", () => {
  it("shows the verify code and keeps the camera off until the desktop allows the phone", async () => {
    const { api, streams, camera } = renderScan();
    expect(await screen.findByText("4821")).toBeInTheDocument();
    expect(screen.getByText("Mã xác minh")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Chờ máy tính cho phép");
    expect(window.location.hash).toBe("");
    expect(screen.queryByRole("button", { name: "Bật camera" })).not.toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scan/claim"]);

    streams.last().emit("approved");
    expect(screen.getByRole("status")).toHaveTextContent("Đã kết nối với máy tính");
    expect(screen.getByRole("button", { name: "Bật camera" })).toBeInTheDocument();
    expect(camera.getUserMedia).not.toHaveBeenCalled();
  });

  it("opens the back camera from the tap, sends a read code and shows what the desktop did", async () => {
    const { api, camera, frames, feedback } = await approvedWithCamera();
    expect(camera.getUserMedia).toHaveBeenCalledWith(CAMERA_CONSTRAINTS);
    expect(feedback.unlock).toHaveBeenCalledTimes(1);

    await frames.frame();
    expect(await results().findByText("Mứt gừng · Hộp")).toBeInTheDocument();
    expect(results().getByText("8934563138165")).toBeInTheDocument();
    expect(api.scans.map((s) => s.code)).toEqual(["8934563138165"]);
    expect(feedback.signal).toHaveBeenCalledWith("ok");
    expect(document.querySelector(".scan-page-flash-ok")).not.toBeNull();
    expect(screen.getByRole("list")).toHaveAttribute("aria-live", "polite");
  });

  it("sends several codes of one frame in order and marks a refusal", async () => {
    const reader = fakeReader(["111", "222"]);
    const { api, frames, feedback } = await approvedWithCamera({ deps: { createReader: () => Promise.resolve(reader) } });
    api.answers.set("222", { outcome: "not_found" });
    await frames.frame();
    expect(await results().findByText("Không có trong danh mục")).toBeInTheDocument();
    expect(api.scans.map((s) => s.code)).toEqual(["111", "222"]);
    // Newest first.
    expect(results().getAllByRole("listitem").map((li) => li.textContent)).toEqual([expect.stringContaining("222"), expect.stringContaining("111")]);
    expect(feedback.signal.mock.calls).toEqual([["ok"], ["error"]]);
  });

  it("turns the camera off and says so when the session ends", async () => {
    const { api, camera, frames } = await approvedWithCamera();
    api.session.state = "ended";
    await frames.frame();
    expect(await screen.findByText("Máy tính đã ngắt kết nối. Quét lại mã QR để tiếp tục.")).toBeInTheDocument();
    expect(camera.track.stop).toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Ngắt kết nối" })).not.toBeInTheDocument();
  });

  it("goes back to the verify code, camera off, when a scan finds the phone not approved", async () => {
    const { api, camera, frames } = await approvedWithCamera();
    api.session.state = "pending";
    await frames.frame();
    expect(await screen.findByText("4821")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Chờ máy tính cho phép");
    expect(camera.track.stop).toHaveBeenCalled();
  });

  it("keeps the camera on while the server restarts", async () => {
    const { streams, camera } = await approvedWithCamera();
    streams.last().emit("restarting");
    expect(screen.getByRole("status")).toHaveTextContent("Đang kết nối lại…");
    expect(camera.track.stop).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Bật camera" })).not.toBeInTheDocument();
  });

  it("keeps the camera on while the API answers 503 and asks again", async () => {
    const { api, streams, camera } = await approvedWithCamera();
    api.onSession(() => apiError(503, "service_unavailable"));
    streams.last().fail("closed");
    await waitFor(() => expect(api.calls.filter((c) => c === "GET /scan/session")).toHaveLength(1), { timeout: 3_000 });
    expect(screen.getByRole("status")).toHaveTextContent("Đang kết nối lại…");
    expect(camera.track.stop).not.toHaveBeenCalled();
    expect(screen.getByRole("list")).toBeInTheDocument();
  });

  it("explains how to allow the camera when it was refused", async () => {
    const getUserMedia = vi.fn(() => Promise.reject(new DOMException("denied", "NotAllowedError")));
    const { streams } = renderScan({ deps: { getUserMedia } });
    await screen.findByText("4821");
    streams.last().emit("approved");
    await userEvent.click(screen.getByRole("button", { name: "Bật camera" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(CAMERA_ERROR_COPY.denied);
    expect(screen.getByRole("button", { name: "Bật camera" })).toBeInTheDocument();
  });

  it("turns the camera off when the barcode reader cannot load, and loads it again on the next tap", async () => {
    const createReader = vi.fn<ScanDeps["createReader"]>().mockRejectedValueOnce(new Error("wasm blocked")).mockResolvedValue(fakeReader());
    const { streams, camera } = renderScan({ deps: { createReader } });
    await screen.findByText("4821");
    streams.last().emit("approved");
    await userEvent.click(screen.getByRole("button", { name: "Bật camera" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Không tải được bộ đọc mã vạch.");
    expect(camera.track.stop).toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Bật camera" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Bật camera" })).not.toBeInTheDocument());
    expect(createReader).toHaveBeenCalledTimes(2);
  });

  it("disconnects from the phone", async () => {
    const { api, camera } = await approvedWithCamera();
    await userEvent.click(screen.getByRole("button", { name: "Ngắt kết nối" }));
    expect(await screen.findByText("Đã ngắt kết nối. Quét lại mã QR trên máy tính để tiếp tục.")).toBeInTheDocument();
    expect(api.calls.at(-1)).toBe("DELETE /scan/session");
    expect(camera.track.stop).toHaveBeenCalled();
  });

  it("asks to scan the QR code when opened without one", async () => {
    renderScan({ url: "/scan" });
    expect(await screen.findByText("Quét mã QR trên màn hình máy tính để bắt đầu.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Chưa kết nối");
  });

  it("says when the link was already used", async () => {
    renderScan({ url: "/scan#c=OLD" });
    expect(await screen.findByText("Mã QR đã hết hạn hoặc đã được dùng. Quét lại mã QR trên máy tính.")).toBeInTheDocument();
  });

  it("offers a retry when the claim got no answer", async () => {
    window.history.replaceState(null, "", "/scan#c=CLAIM-1");
    const api = phoneScanServer();
    let down = true;
    api.onClaim(() => (down ? apiError(503, "service_unavailable") : undefined));
    renderWithProviders(<ScanScreen deps={{ openStream: createFakeStreams().open }} />, { path: "/scan" });
    await screen.findByRole("alert");
    down = false;
    await userEvent.click(screen.getByRole("button", { name: "Thử lại" }));
    expect(await screen.findByText("4821")).toBeInTheDocument();
  });

  it("lets this tab take the session back from another tab", async () => {
    const { streams } = renderScan({ url: "/scan", state: "claimed" });
    await screen.findByRole("button", { name: "Bật camera" });
    streams.last().emit("replaced");
    await userEvent.click(await screen.findByRole("button", { name: "Quét ở tab này" }));
    expect(await screen.findByRole("button", { name: "Bật camera" })).toBeInTheDocument();
  });
});

describe("the /scan route", () => {
  it("opens without signing in, outside the app shell", async () => {
    window.history.replaceState(null, "", "/scan");
    const api = phoneScanServer();
    renderApp("/scan");
    expect(await screen.findByRole("heading", { level: 1, name: "Quét mã vạch" })).toBeInTheDocument();
    // jsdom has no EventSource: the page says so instead of claiming anything.
    expect(screen.getByText("Trình duyệt này không hỗ trợ quét bằng điện thoại. Mở trang bằng Safari hoặc Chrome bản mới.")).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
    expect(api.calls).toEqual([]);
  });
});
