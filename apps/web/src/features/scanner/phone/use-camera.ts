import { useCallback, useEffect, useRef, useState, type RefObject } from "react";

// The phone's back camera behind one tap. It opens only from "Bật camera" (iOS plays camera video
// and sound only after a touch), and every track stops when the screen leaves, the tab is hidden or
// the page is put away, so the camera light never stays on behind the person's back. A hidden tab
// returns to the "Bật camera" button rather than reopening by itself.

export type CameraError = "denied" | "no_camera" | "busy" | "unsupported" | "failed";

export type CameraState = { kind: "off"; hidden?: true } | { kind: "starting" } | { kind: "on" } | { kind: "error"; error: CameraError };

export type GetUserMedia = (constraints: MediaStreamConstraints) => Promise<MediaStream>;

/** The back camera at up to 1080p: small barcodes need the pixels. */
export const CAMERA_CONSTRAINTS: MediaStreamConstraints = {
  video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 } },
  audio: false,
};

export const CAMERA_ERROR_COPY: Readonly<Record<CameraError, string>> = {
  denied:
    "Trang chưa được phép dùng camera. Trên iPhone (Safari): chạm aA trên thanh địa chỉ › Cài đặt trang web › Camera › Cho phép. Trên Android (Chrome): chạm biểu tượng bên trái địa chỉ trang › Quyền › Camera › Cho phép. Sau đó bấm Bật camera.",
  no_camera: "Không tìm thấy camera trên máy này.",
  busy: "Camera đang được ứng dụng khác dùng. Đóng ứng dụng đó rồi bấm Bật camera.",
  unsupported: "Trình duyệt này không mở được camera. Mở trang bằng Safari hoặc Chrome bản mới.",
  failed: "Không mở được camera. Bấm Bật camera để thử lại.",
};

export const CAMERA_HIDDEN_TEXT = "Camera đã tắt khi rời trang. Bấm Bật camera để quét tiếp.";

/** getUserMedia of this browser; null without camera access (an old browser, or a page not on https). */
export function defaultGetUserMedia(): GetUserMedia | null {
  const devices = typeof navigator !== "undefined" ? navigator.mediaDevices : undefined;
  return devices && typeof devices.getUserMedia === "function" ? (c) => devices.getUserMedia(c) : null;
}

/** Which sentence explains a getUserMedia or play() failure. */
export function cameraError(e: unknown): CameraError {
  const name = e instanceof Error || e instanceof DOMException ? e.name : "";
  switch (name) {
    case "NotAllowedError":
    case "SecurityError":
      return "denied";
    case "NotFoundError":
    case "OverconstrainedError":
      return "no_camera";
    case "NotReadableError":
    case "AbortError":
      return "busy";
    default:
      return "failed";
  }
}

function stopTracks(stream: MediaStream) {
  for (const track of stream.getTracks()) track.stop();
}

/** Keeps the lens refocusing as the phone moves, where the camera can (most Android phones). */
async function focusContinuously(stream: MediaStream) {
  const [track] = stream.getVideoTracks();
  if (!track || typeof track.getCapabilities !== "function") return;
  // focusMode is part of Media Capture Image, which the DOM lib does not type.
  const capabilities = track.getCapabilities() as MediaTrackCapabilities & { focusMode?: string[] };
  if (!capabilities.focusMode?.includes("continuous")) return;
  try {
    await track.applyConstraints({ advanced: [{ focusMode: "continuous" } as MediaTrackConstraintSet] });
  } catch {
    // The phone keeps its own focus.
  }
}

export interface Camera {
  state: CameraState;
  /** Opens the camera into the video; true once it plays. Call from a tap. */
  start(): Promise<boolean>;
  stop(): void;
}

export function useCamera(videoRef: RefObject<HTMLVideoElement | null>, getUserMedia: GetUserMedia | null): Camera {
  const [state, setState] = useState<CameraState>({ kind: "off" });
  const streamRef = useRef<MediaStream | null>(null);
  // Bumped by every start and stop, so an answer that arrives late is dropped.
  const runRef = useRef(0);

  const release = useCallback(() => {
    runRef.current++;
    if (streamRef.current) stopTracks(streamRef.current);
    streamRef.current = null;
    const video = videoRef.current;
    if (video) video.srcObject = null;
  }, [videoRef]);

  const stop = useCallback(() => {
    release();
    setState({ kind: "off" });
  }, [release]);

  const start = useCallback(async (): Promise<boolean> => {
    release();
    const run = runRef.current;
    if (getUserMedia === null) {
      setState({ kind: "error", error: "unsupported" });
      return false;
    }
    setState({ kind: "starting" });
    let stream: MediaStream;
    try {
      stream = await getUserMedia(CAMERA_CONSTRAINTS);
    } catch (e) {
      if (run === runRef.current) setState({ kind: "error", error: cameraError(e) });
      return false;
    }
    const video = videoRef.current;
    if (run !== runRef.current || video === null) {
      stopTracks(stream);
      return false;
    }
    streamRef.current = stream;
    video.muted = true;
    video.srcObject = stream;
    try {
      await video.play();
    } catch (e) {
      if (run === runRef.current) {
        release();
        setState({ kind: "error", error: cameraError(e) });
      }
      return false;
    }
    if (run !== runRef.current) return false;
    void focusContinuously(stream);
    setState({ kind: "on" });
    return true;
  }, [getUserMedia, release, videoRef]);

  useEffect(() => {
    const putAway = () => {
      release();
      setState((s) => (s.kind === "on" || s.kind === "starting" ? { kind: "off", hidden: true } : s));
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") putAway();
    };
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", putAway);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", putAway);
      release();
    };
  }, [release]);

  return { state, start, stop };
}
