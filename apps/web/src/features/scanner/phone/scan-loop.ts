import type { BarcodeReader } from "./barcode-reader";

// The loop that reads the camera: one read per new video frame (requestVideoFrameCallback, or
// requestAnimationFrame where it is missing), at most about 8 a second, never two at once, and none
// while the tab is hidden or the video has no frame. A code is sent once while it stays in view; it
// counts as new again only after it was out of view for ABSENT_MS, so holding the phone over a box
// adds it once and moving away and back adds it again.

/** Least time between two reads (about 8 a second). */
export const SCAN_INTERVAL_MS = 125;
/** How long a code must be out of view before reading it again counts as a new scan. */
export const ABSENT_MS = 800;
/** Reads that fail in a row before the loop gives up and reports. */
export const MAX_READ_FAILURES = 5;

/** HTMLMediaElement.HAVE_CURRENT_DATA: a frame can be read. */
const HAVE_CURRENT_DATA = 2;

/** Calls `cb` on the next frame; returns the function that cancels it. */
export type FrameScheduler = (video: HTMLVideoElement, cb: () => void) => () => void;

export const nextFrame: FrameScheduler = (video, cb) => {
  if (typeof video.requestVideoFrameCallback === "function") {
    const id = video.requestVideoFrameCallback(() => cb());
    return () => video.cancelVideoFrameCallback(id);
  }
  const id = requestAnimationFrame(() => cb());
  return () => cancelAnimationFrame(id);
};

/**
 * Remembers when each code was last seen and keeps, of the codes in one frame, those that are new:
 * trimmed, non-empty, once each, and not seen within the last `absentMs`.
 */
export function createCodeFilter(absentMs: number = ABSENT_MS): (codes: readonly string[], now: number) => string[] {
  const lastSeen = new Map<string, number>();
  return (codes, now) => {
    for (const [code, at] of lastSeen) if (now - at >= absentMs) lastSeen.delete(code);
    const fresh: string[] = [];
    for (const raw of codes) {
      const code = raw.trim();
      if (code === "" || fresh.includes(code)) continue;
      if (!lastSeen.has(code)) fresh.push(code);
      lastSeen.set(code, now);
    }
    return fresh;
  };
}

export interface ScanLoopOptions {
  video: HTMLVideoElement;
  reader: BarcodeReader;
  /** New codes of one frame, in the order read. */
  onCodes: (codes: string[]) => void;
  /** The reader failed MAX_READ_FAILURES times in a row; the loop has stopped. */
  onFailure: (error: unknown) => void;
  requestFrame?: FrameScheduler;
  now?: () => number;
  isHidden?: () => boolean;
}

/** Starts reading; returns the function that stops it. */
export function startScanLoop({
  video,
  reader,
  onCodes,
  onFailure,
  requestFrame = nextFrame,
  now = () => performance.now(),
  isHidden = () => document.hidden,
}: ScanLoopOptions): () => void {
  const filter = createCodeFilter();
  let stopped = false;
  let busy = false;
  let lastRead = -Infinity;
  let failures = 0;
  let cancel: (() => void) | null = null;

  const read = () => {
    busy = true;
    lastRead = now();
    reader.detect(video).then(
      (codes) => {
        busy = false;
        failures = 0;
        if (stopped) return;
        const fresh = filter(codes, now());
        if (fresh.length > 0) onCodes(fresh);
      },
      (e: unknown) => {
        busy = false;
        if (stopped) return;
        failures++;
        if (failures < MAX_READ_FAILURES) return;
        stop();
        onFailure(e);
      },
    );
  };

  const tick = () => {
    cancel = null;
    if (stopped) return;
    if (!busy && now() - lastRead >= SCAN_INTERVAL_MS && !isHidden() && video.readyState >= HAVE_CURRENT_DATA) read();
    cancel = requestFrame(video, tick);
  };

  function stop() {
    stopped = true;
    cancel?.();
    cancel = null;
  }

  cancel = requestFrame(video, tick);
  return stop;
}
