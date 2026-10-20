import { useCallback, useEffect, useRef, useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { cx } from "../../../shared/lib/cx";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { defaultEventStream, type OpenEventStream } from "../event-stream";
import { createBarcodeReader, type BarcodeReader } from "../phone/barcode-reader";
import { createFeedback, type Feedback } from "../phone/feedback";
import { scanFailureCopy, type ScanLine, type ScanTone } from "../phone/outcome-copy";
import { nextFrame, startScanLoop, type FrameScheduler } from "../phone/scan-loop";
import { newScan } from "../phone/send-scan";
import { CAMERA_ERROR_COPY, CAMERA_HIDDEN_TEXT, defaultGetUserMedia, useCamera, type GetUserMedia } from "../phone/use-camera";
import { createPhoneSession, usePhoneSession, type PhoneSession, type PhoneSnapshot, type PhoneView } from "../phone/use-phone-session";

// The page a phone opens from the desktop's pairing QR code (/scan#c=<code>). No sign-in and no app
// shell: it claims the session, shows the verify code until the desktop allows it, then reads
// barcodes with the camera and sends each to the desktop, showing what the desktop did with it.
// It shows nothing of the store but the product and unit names those answers carry.

/** What the page uses from the browser, replaceable in tests. */
export interface ScanDeps {
  openStream: OpenEventStream | null;
  getUserMedia: GetUserMedia | null;
  createReader: () => Promise<BarcodeReader>;
  createFeedback: (flash: (tone: ScanTone) => void) => Feedback;
  requestFrame: FrameScheduler;
}

const MAX_RESULTS = 5;
const FLASH_MS = 150;
const READER_LOAD_TEXT = "Không tải được bộ đọc mã vạch. Kiểm tra mạng rồi bấm Bật camera.";
const READER_FAILED_TEXT = "Không đọc được hình từ camera. Bấm Bật camera để thử lại.";

const ENDED_TEXT = { desktop: "Máy tính đã ngắt kết nối. Quét lại mã QR để tiếp tục.", phone: "Đã ngắt kết nối. Quét lại mã QR trên máy tính để tiếp tục." } as const;

function defaultDeps(): ScanDeps {
  return {
    openStream: defaultEventStream(),
    getUserMedia: defaultGetUserMedia(),
    createReader: () => createBarcodeReader(),
    createFeedback: (flash) => createFeedback({ flash }),
    requestFrame: nextFrame,
  };
}

function statusText({ view, reconnecting }: PhoneSnapshot): string {
  if (reconnecting) return "Đang kết nối lại…";
  switch (view.kind) {
    case "starting":
      return "Đang kết nối…";
    case "pending":
      return "Chờ máy tính cho phép";
    case "claimed":
      return "Đã kết nối với máy tính";
    default:
      return "Chưa kết nối";
  }
}

interface ResultEntry {
  id: string;
  code: string;
  /** Null while the desktop has not answered. */
  line: ScanLine | null;
}

function ScanResults({ results }: { results: readonly ResultEntry[] }) {
  return (
    <section className="scan-page-results" aria-labelledby="scan-results-title">
      <h2 id="scan-results-title" className="scan-page-sub">
        Kết quả gần nhất
      </h2>
      <ol className="scan-page-list" aria-live="polite">
        {results.map((r) => (
          <li key={r.id} className={cx("scan-page-item", r.line && `scan-page-item-${r.line.tone}`)}>
            <span className="scan-page-mark" aria-hidden="true">
              {r.line === null ? "…" : r.line.tone === "ok" ? "✓" : "!"}
            </span>
            <span className="scan-page-item-b">
              <span className="scan-page-item-t">{r.line?.text ?? "Đang gửi…"}</span>
              <span className="scan-page-item-code">{r.code}</span>
            </span>
          </li>
        ))}
      </ol>
      {results.length === 0 ? <p className="hint">Đưa mã vạch vào khung camera. Mỗi mã chỉ được gửi một lần khi còn trong khung.</p> : null}
    </section>
  );
}

/** The camera, its reader and the results, shown only while the desktop allows this phone. */
function ScannerPanel({ session, deps, feedback }: { session: PhoneSession; deps: ScanDeps; feedback: Feedback }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const camera = useCamera(videoRef, deps.getUserMedia);
  const stopCamera = camera.stop;
  const readerRef = useRef<Promise<BarcodeReader> | null>(null);
  const [reader, setReader] = useState<BarcodeReader | null>(null);
  const [readerError, setReaderError] = useState<string | null>(null);
  const [results, setResults] = useState<ResultEntry[]>([]);
  const queueRef = useRef<Promise<void>>(Promise.resolve());
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const loadReader = () => {
    // A failed load is forgotten so the next tap tries again.
    readerRef.current ??= deps.createReader().catch((e: unknown) => {
      readerRef.current = null;
      throw e;
    });
    return readerRef.current;
  };

  const enable = async () => {
    feedback.unlock();
    setReaderError(null);
    // The reader (maybe the WASM download) loads while the person answers the camera prompt.
    const loading = loadReader();
    loading.catch(() => undefined);
    if (!(await camera.start())) return;
    try {
      const r = await loading;
      if (mountedRef.current) setReader(r);
    } catch {
      if (!mountedRef.current) return;
      stopCamera();
      setReaderError(READER_LOAD_TEXT);
    }
  };

  // One detection at a time to the desktop, in the order read, so the cart gets them in that order.
  const enqueue = useCallback(
    (code: string) => {
      const scan = newScan(code);
      setResults((rs) => [{ id: scan.key, code, line: null }, ...rs].slice(0, MAX_RESULTS));
      queueRef.current = queueRef.current.then(async () => {
        let line: ScanLine | null;
        try {
          line = await session.send(scan);
        } catch (e) {
          line = scanFailureCopy(e);
        }
        if (!mountedRef.current) return;
        if (line === null) {
          setResults((rs) => rs.filter((r) => r.id !== scan.key));
          return;
        }
        const answered = line;
        setResults((rs) => rs.map((r) => (r.id === scan.key ? { ...r, line: answered } : r)));
        feedback.signal(answered.tone);
      });
    },
    [session, feedback],
  );

  const on = camera.state.kind === "on";
  useEffect(() => {
    const video = videoRef.current;
    if (!on || reader === null || video === null) return;
    return startScanLoop({
      video,
      reader,
      requestFrame: deps.requestFrame,
      onCodes: (codes) => codes.forEach(enqueue),
      onFailure: () => {
        stopCamera();
        setReaderError(READER_FAILED_TEXT);
      },
    });
  }, [on, reader, deps.requestFrame, enqueue, stopCamera]);

  const { state } = camera;
  const problem = state.kind === "error" ? CAMERA_ERROR_COPY[state.error] : readerError;
  return (
    <>
      <div className={cx("scan-page-view", on && "on")}>
        <video ref={videoRef} className="scan-page-video" playsInline muted autoPlay aria-label="Hình từ camera" />
        {on ? null : <p className="scan-page-view-off">Camera chưa bật</p>}
      </div>
      {on ? null : (
        <div className="scan-page-cta">
          {problem !== null ? (
            <Callout tone="bad" role="alert">
              {problem}
            </Callout>
          ) : state.kind === "off" && state.hidden ? (
            <p className="hint">{CAMERA_HIDDEN_TEXT}</p>
          ) : null}
          <Button variant="primary" className="scan-page-go" pending={state.kind === "starting"} pendingLabel="Đang mở camera…" onClick={() => void enable()}>
            Bật camera
          </Button>
        </div>
      )}
      <ScanResults results={results} />
    </>
  );
}

function Body({ view, session, deps, feedback }: { view: PhoneView; session: PhoneSession; deps: ScanDeps; feedback: Feedback }) {
  switch (view.kind) {
    case "starting":
      return <p className="hint">Đang kết nối với máy tính…</p>;
    case "unsupported":
      return <Callout tone="warn">Trình duyệt này không hỗ trợ quét bằng điện thoại. Mở trang bằng Safari hoặc Chrome bản mới.</Callout>;
    case "no_code":
      return <Callout>Quét mã QR trên màn hình máy tính để bắt đầu.</Callout>;
    case "claim_invalid":
      return <Callout tone="warn">Mã QR đã hết hạn hoặc đã được dùng. Quét lại mã QR trên máy tính.</Callout>;
    case "failed":
      return (
        <div className="scan-page-cta">
          <Callout tone="bad" role="alert">
            {view.message}
          </Callout>
          <Button variant="primary" onClick={() => session.retry()}>
            Thử lại
          </Button>
        </div>
      );
    case "pending":
      return (
        <section className="scan-page-wait" aria-labelledby="scan-verify-label">
          <p id="scan-verify-label" className="scan-page-sub">
            Mã xác minh
          </p>
          <p className="scan-page-verify">{view.verifyCode || "…"}</p>
          <p>Chờ máy tính cho phép… Kiểm tra mã trên máy tính giống mã này.</p>
        </section>
      );
    case "claimed":
      return <ScannerPanel session={session} deps={deps} feedback={feedback} />;
    case "rejected":
      return <Callout tone="bad">Máy tính đã từ chối kết nối. Quét lại mã QR nếu đây là điện thoại của bạn.</Callout>;
    case "ended":
      return <Callout>{ENDED_TEXT[view.by]}</Callout>;
    case "replaced":
      return (
        <div className="scan-page-cta">
          <Callout tone="warn">Trang quét đang mở ở tab khác trên điện thoại này.</Callout>
          <Button variant="primary" onClick={() => session.takeOver()}>
            Quét ở tab này
          </Button>
        </div>
      );
  }
}

/** The scan page with its browser seams; tests pass fakes for the camera, reader and stream. */
export function ScanScreen({ deps: overrides }: { deps?: Partial<ScanDeps> }) {
  const client = useApiClient();
  const [deps] = useState<ScanDeps>(() => ({ ...defaultDeps(), ...overrides }));
  const [session] = useState(() => createPhoneSession({ client, openStream: deps.openStream }));
  const snap = usePhoneSession(session);
  const [flash, setFlash] = useState<{ tone: ScanTone; n: number } | null>(null);
  const [feedback] = useState(() => deps.createFeedback((tone) => setFlash((f) => ({ tone, n: (f?.n ?? 0) + 1 }))));

  useEffect(() => {
    session.start();
    return () => session.stop();
  }, [session]);

  useEffect(() => {
    if (flash === null) return;
    const t = setTimeout(() => setFlash(null), FLASH_MS);
    return () => clearTimeout(t);
  }, [flash]);

  const { view } = snap;
  const live = view.kind === "pending" || view.kind === "claimed";
  return (
    <main className="scan-page" aria-labelledby="scan-title">
      <header className="scan-page-bar">
        <div className="scan-page-bar-b">
          <h1 id="scan-title" className="scan-page-title">
            Quét mã vạch
          </h1>
          <p role="status" className={cx("scan-page-status", view.kind === "claimed" && !snap.reconnecting && "ok")}>
            {statusText(snap)}
          </p>
        </div>
        {live ? (
          <Button size="sm" pending={snap.leaving} pendingLabel="Đang ngắt…" onClick={() => void session.disconnect()}>
            Ngắt kết nối
          </Button>
        ) : null}
      </header>
      <div className="scan-page-body">
        <Body view={view} session={session} deps={deps} feedback={feedback} />
      </div>
      {flash ? <div key={flash.n} className={cx("scan-page-flash", `scan-page-flash-${flash.tone}`)} aria-hidden="true" /> : null}
    </main>
  );
}

export default function ScanPage() {
  return <ScanScreen />;
}
