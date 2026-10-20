import { errorMessage, isApiError } from "../../shared/api/api-error";
import type { HttpClient } from "../../shared/api/http";
import { toast } from "../../shared/ui/toast-store";
import { approveScannerSession, createScannerSession, rejectScannerSession, resolveScan, revokeScannerSession, refreshClaimCode, type ScanAnswer } from "./api/mutations";
import { getScannerSession, scannerEventsUrl } from "./api/queries";
import { claimRequestedSchema, revokedEventSchema, scanEventSchema, sessionStatusSchema, type ClaimCode, type ScanEvent, type SessionStatus } from "./api/schemas";
import type { EventStream, OpenEventStream } from "./event-stream";
import { reconnectDelay } from "./reconnect";
import type { SessionStore } from "./session-store";

// One phone scanner session per tab, shared by every screen that takes scans. The controller lives
// outside React so its timers, stream and scan queue never read a stale render:
//   - it works only while a pairing card is shown or the tab holds a session;
//   - the `state` event the server sends whenever the stream opens is the only source of the card's
//     state; GET is asked before every (re)open, and only an `ended` session or a 404 the API wrote
//     ends the session here (a proxy's 404 page, 5xx and network failures are a restart passing by);
//   - a closed stream, or the `restarting` event, reopens after about 1, 2, 5, 10, then 30 s, forever;
//   - a session the server closed on its own (`revoked` by "system": the user opened too many, or
//     signed out) is not replaced by itself: the card offers to pair again, so tabs of one user
//     never keep closing each other's sessions;
//   - each `scan` is handled once per scan_id, in arrival order, by the screen in front, unless it
//     outlived its ttl_ms on this browser's clock, and the result goes back to the phone. A handler
//     that is still busy when the scan's time runs out is left behind, so later codes still run.

/** How long a claim code works (the server's lifetime). */
export const CLAIM_LIFETIME_MS = 120_000;
/** A new claim code is asked for this long before the old one expires, so the QR never shows a dead code. */
export const CLAIM_REFRESH_LEAD_MS = 5_000;
/** Wait before asking again for a claim code after a failed attempt (unless the server says how long). */
export const CLAIM_RETRY_MS = 5_000;
/** How long "Điện thoại đã ngắt kết nối" stays before a new QR code. */
export const REVOKED_NOTICE_MS = 4_000;
/** Scan ids remembered so a repeated scan is answered without handling it again. */
export const MAX_REMEMBERED_SCANS = 200;

/**
 * A screen's Enter handler for scanned codes. It answers `not_accepting` when the screen cannot take
 * a code at that moment (a sale being saved, a dialog over it), so the phone says so.
 */
export type ScanHandler = (code: string) => Promise<ScanAnswer> | ScanAnswer;

/** The answer of a screen that takes no codes right now. */
export const NOT_ACCEPTING: ScanAnswer = { outcome: "not_accepting" };

export interface ScanTargetRef {
  readonly current: ScanHandler | null;
}

/** What the pairing card shows. */
export type PairingView =
  | { kind: "inactive" }
  | { kind: "unsupported" }
  | { kind: "offline" }
  | { kind: "creating" }
  | { kind: "failed"; message: string }
  | { kind: "waiting"; claimCode: string | null }
  | { kind: "pending"; deviceLabel: string | null; verifyCode: string }
  | { kind: "claimed"; deviceLabel: string | null }
  | { kind: "reconnecting" }
  | { kind: "revoked" }
  | { kind: "replaced" }
  | { kind: "closed" };

export type PairingCommand = "approve" | "reject" | "revoke";

export interface PairingSnapshot {
  view: PairingView;
  /** The card command in flight. */
  busy: PairingCommand | null;
}

export interface ScannerController {
  subscribe(listener: () => void): () => void;
  getSnapshot(): PairingSnapshot;
  start(): void;
  stop(): void;
  setOffline(offline: boolean): void;
  /** A pairing card is on screen; returns the function that removes it. */
  showCard(): () => void;
  /** A screen takes scans; the target registered last gets them until it is removed. */
  registerTarget(ref: ScanTargetRef): () => void;
  approve(): Promise<void>;
  reject(): Promise<void>;
  revoke(): Promise<void>;
  /** Tries again to open a session after a failure. */
  retry(): void;
  /** Pairs again after the card stepped aside: another tab took the session, or the server closed it. */
  pairHere(): void;
}

export interface ScannerControllerOptions {
  client: HttpClient;
  /** Null when the browser has no EventSource: the card says phone scanning is unavailable. */
  openStream: OpenEventStream | null;
  store: SessionStore;
  now?: () => number;
  /** Spreads the reconnect waits; Math.random by default. */
  random?: () => number;
}

type Phase = "waiting" | "pending" | "claimed";

interface Claim {
  code: string;
  refreshAt: number;
}

/** What one scan resolved to; `ran` when a screen's handler ran for it. */
interface Resolved {
  answer: ScanAnswer;
  ran: boolean;
}

const TIMED_OUT = Symbol("timed out");

/** A 404 with the API's error body: the session is someone else's or gone. */
function isSessionGone(e: unknown): boolean {
  return isApiError(e) && e.status === 404 && e.fromServer;
}

function viewsEqual(a: PairingSnapshot, b: PairingSnapshot): boolean {
  return a.busy === b.busy && JSON.stringify(a.view) === JSON.stringify(b.view);
}

export function createScannerController({ client, openStream, store, now = () => Date.now(), random = Math.random }: ScannerControllerOptions): ScannerController {
  const listeners = new Set<() => void>();
  let started = false;
  let offline = false;
  let cards = 0;
  let sessionId: string | null = store.load();
  let phase: Phase | null = null;
  let deviceLabel: string | null = null;
  let verifyCode: string | null = null;
  let claim: Claim | null = null;
  let claimRetryAt = 0;
  let stream: EventStream | null = null;
  let checking = false;
  let reconnecting = sessionId !== null;
  let attempt = 0;
  let refreshing = false;
  let creating = false;
  let createError: string | null = null;
  let notice: "revoked" | "replaced" | "closed" | null = null;
  let busy: PairingCommand | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let claimTimer: ReturnType<typeof setTimeout> | null = null;
  let noticeTimer: ReturnType<typeof setTimeout> | null = null;
  const targets: ScanTargetRef[] = [];
  const results = new Map<string, Promise<Resolved | null>>();
  let queue: Promise<unknown> = Promise.resolve();

  const computeView = (): PairingView => {
    if (openStream === null) return { kind: "unsupported" };
    if (offline) return { kind: "offline" };
    if (notice !== null) return { kind: notice };
    if (sessionId === null) {
      if (createError !== null) return { kind: "failed", message: createError };
      return creating || cards > 0 ? { kind: "creating" } : { kind: "inactive" };
    }
    if (reconnecting || phase === null) return { kind: "reconnecting" };
    if (phase === "waiting") return { kind: "waiting", claimCode: claim?.code ?? null };
    if (phase === "pending") return { kind: "pending", deviceLabel, verifyCode: verifyCode ?? "" };
    return { kind: "claimed", deviceLabel };
  };
  let snapshot: PairingSnapshot = { view: computeView(), busy };
  const emit = () => {
    const next = { view: computeView(), busy };
    if (viewsEqual(next, snapshot)) return;
    snapshot = next;
    for (const l of [...listeners]) l();
  };

  const stopStream = () => {
    stream?.close();
    stream = null;
  };
  const stopReconnect = () => {
    if (reconnectTimer !== null) clearTimeout(reconnectTimer);
    reconnectTimer = null;
  };
  const stopClaimTimer = () => {
    if (claimTimer !== null) clearTimeout(claimTimer);
    claimTimer = null;
  };
  const stopNoticeTimer = () => {
    if (noticeTimer !== null) clearTimeout(noticeTimer);
    noticeTimer = null;
  };

  /** Drops the session on this tab: no stream, no timers, nothing stored. */
  const forget = () => {
    stopStream();
    stopReconnect();
    stopClaimTimer();
    sessionId = null;
    store.clear();
    phase = null;
    deviceLabel = null;
    verifyCode = null;
    claim = null;
    claimRetryAt = 0;
    reconnecting = false;
    attempt = 0;
  };

  const toClaim = (c: ClaimCode): Claim => {
    // The server's expiry, read on this browser's clock; a clock that disagrees falls back to the
    // code's lifetime from now.
    const left = Date.parse(c.claim_expires_at) - now();
    const lifetime = left > CLAIM_REFRESH_LEAD_MS && left <= CLAIM_LIFETIME_MS ? left : CLAIM_LIFETIME_MS;
    return { code: c.claim_code, refreshAt: now() + lifetime - CLAIM_REFRESH_LEAD_MS };
  };

  /** Keeps a live claim code behind the QR while a card shows a session no phone claimed. */
  const syncClaim = () => {
    stopClaimTimer();
    if (!started || offline || sessionId === null || phase !== "waiting" || cards === 0 || refreshing) return;
    const wait = Math.max(claim?.refreshAt ?? 0, claimRetryAt) - now();
    if (wait <= 0) {
      void refreshClaim();
      return;
    }
    claimTimer = setTimeout(() => {
      claimTimer = null;
      void refreshClaim();
    }, wait);
  };

  const applyStatus = (s: SessionStatus) => {
    if (s.state === "ended") {
      forget();
      reconcile();
      return;
    }
    phase = s.state;
    deviceLabel = s.device_label ?? null;
    verifyCode = s.verify_code ?? null;
    // A claimed code is spent; only a waiting session keeps the code it showed.
    if (s.state !== "waiting") claim = null;
    syncClaim();
  };

  const refetchStatus = async (id: string) => {
    try {
      const s = await getScannerSession(client, id);
      if (sessionId === id) applyStatus(s);
    } catch (e) {
      if (sessionId === id && isSessionGone(e)) {
        forget();
        reconcile();
      }
      // Otherwise the stream's next `state` event tells.
    }
    emit();
  };

  async function refreshClaim() {
    const id = sessionId;
    if (id === null || refreshing) return;
    refreshing = true;
    let failure: unknown = null;
    let fresh: ClaimCode | null = null;
    try {
      fresh = await refreshClaimCode(client, id);
    } catch (e) {
      failure = e;
    }
    refreshing = false;
    if (sessionId !== id) return;
    if (fresh !== null) {
      if (phase === "waiting") {
        claim = toClaim(fresh);
        claimRetryAt = 0;
      }
    } else if (isApiError(failure) && failure.code === "already_claimed") {
      // A phone claimed the code while the event was missed: the session's state says what next.
      await refetchStatus(id);
      return;
    } else if (isSessionGone(failure)) {
      forget();
      reconcile();
    } else {
      claim = null;
      const wait = isApiError(failure) && failure.retryAfterSeconds !== null ? failure.retryAfterSeconds * 1000 : CLAIM_RETRY_MS;
      claimRetryAt = now() + wait;
    }
    syncClaim();
    emit();
  }

  const scheduleReconnect = () => {
    stopStream();
    stopReconnect();
    reconnecting = true;
    if (started && !offline && sessionId !== null) {
      const delay = reconnectDelay(attempt, random);
      attempt++;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        void resume();
      }, delay);
    }
    emit();
  };

  /** Asks the session's state, then opens its stream; an ended or foreign session is dropped. */
  async function resume() {
    const id = sessionId;
    if (id === null || checking) return;
    checking = true;
    reconnecting = true;
    emit();
    let status: SessionStatus | null = null;
    let failure: unknown = null;
    try {
      status = await getScannerSession(client, id);
    } catch (e) {
      failure = e;
    }
    checking = false;
    if (sessionId !== id) return;
    if (status === null) {
      if (isSessionGone(failure)) {
        forget();
        emit();
        reconcile();
      } else {
        scheduleReconnect();
      }
      return;
    }
    applyStatus(status);
    if (sessionId === id && started && !offline && stream === null) openFor(id);
    emit();
  }

  const onScan = (ev: ScanEvent, id: string) => {
    const known = results.get(ev.scan_id);
    if (known) {
      // The same scan again (the phone retried): answer with the first result, never handle twice.
      void known.then((r) => (r ? sendResult(id, ev, r) : undefined));
      return;
    }
    const deadline = now() + ev.ttl_ms;
    const done = queue.then(() => processScan(ev, id, deadline));
    queue = done.catch(() => undefined);
    results.set(ev.scan_id, done);
    if (results.size > MAX_REMEMBERED_SCANS) {
      const oldest = results.keys().next().value;
      if (oldest !== undefined) results.delete(oldest);
    }
    void done.then((r) => (r ? sendResult(id, ev, r) : undefined));
  };

  async function processScan(ev: ScanEvent, id: string, deadline: number): Promise<Resolved | null> {
    // Too late to apply: the phone already gave up on it, so it is skipped rather than added late.
    if (sessionId !== id || now() > deadline) return null;
    const handler = targets.at(-1)?.current ?? null;
    if (handler === null) return { answer: NOT_ACCEPTING, ran: false };
    // A handler still busy when the scan's time runs out (a request that never answers) gives up
    // its turn, so the codes behind it are not skipped one after another.
    let timer: ReturnType<typeof setTimeout> | null = null;
    const late = new Promise<typeof TIMED_OUT>((resolve) => {
      timer = setTimeout(() => resolve(TIMED_OUT), Math.max(deadline - now(), 0));
    });
    try {
      const answer = await Promise.race([Promise.resolve(handler(ev.code)), late]);
      if (answer === TIMED_OUT) {
        toast.error(`Mã ${ev.code} từ điện thoại xử lý quá lâu nên đã bỏ qua. Kiểm tra lại xem dòng hàng có được thêm không.`);
        return null;
      }
      return { answer, ran: true };
    } catch (e) {
      toast.error(`Không xử lý được mã ${ev.code} từ điện thoại: ${errorMessage(e)}`);
      return { answer: { outcome: "not_found" }, ran: true };
    } finally {
      if (timer !== null) clearTimeout(timer);
    }
  }

  async function sendResult(id: string, ev: ScanEvent, r: Resolved) {
    try {
      await resolveScan(client, id, ev.scan_id, r.answer);
    } catch (e) {
      // The phone shows its own timeout. Only a line added here is news the cashier must check;
      // every other outcome was already reported on this screen by the handler.
      if (!r.ran || r.answer.outcome !== "added") return;
      if (isApiError(e) && e.code === "scan_expired") toast.error(`Mã ${ev.code} từ điện thoại đến muộn. Kiểm tra lại dòng vừa thêm.`);
      else toast.error(`Không báo được kết quả mã ${ev.code} về điện thoại: ${errorMessage(e)}`);
    }
  }

  function openFor(id: string) {
    if (openStream === null) return;
    let self: EventStream | null = null;
    const live = () => self !== null && stream === self;
    const on =
      <A extends unknown[]>(fn: (...args: A) => void) =>
      (...args: A) => {
        if (live()) fn(...args);
      };
    self = openStream(scannerEventsUrl(client, id), {
      events: {
        state: on((data) => {
          const parsed = sessionStatusSchema.safeParse(data);
          if (!parsed.success) return;
          attempt = 0;
          reconnecting = false;
          applyStatus(parsed.data);
          emit();
        }),
        claim_requested: on((data) => {
          const parsed = claimRequestedSchema.safeParse(data);
          if (!parsed.success) return;
          phase = "pending";
          deviceLabel = parsed.data.device_label ?? null;
          verifyCode = parsed.data.verify_code;
          claim = null;
          syncClaim();
          emit();
        }),
        scan: on((data) => {
          const parsed = scanEventSchema.safeParse(data);
          if (parsed.success) onScan(parsed.data, id);
        }),
        revoked: on((data) => {
          forget();
          stopNoticeTimer();
          if (revokedEventSchema.safeParse(data).data?.by === "system") {
            // Closed by the server, not by a person: opening a new session by itself would close
            // the oldest of the user's other sessions, which would do the same in turn.
            notice = "closed";
            emit();
            return;
          }
          notice = "revoked";
          noticeTimer = setTimeout(() => {
            noticeTimer = null;
            notice = null;
            emit();
            reconcile();
          }, REVOKED_NOTICE_MS);
          emit();
        }),
        replaced: on(() => {
          // Another tab (a duplicated one) took this session: step aside without opening a new one,
          // so the two tabs do not keep taking it from each other.
          forget();
          notice = "replaced";
          emit();
        }),
        restarting: on(() => scheduleReconnect()),
      },
      onError: on((state) => {
        if (state === "closed") {
          scheduleReconnect();
          return;
        }
        reconnecting = true;
        emit();
      }),
    });
    stream = self;
  }

  async function create() {
    creating = true;
    emit();
    try {
      const created = await createScannerSession(client);
      sessionId = created.id;
      store.save(created.id);
      phase = "waiting";
      deviceLabel = null;
      verifyCode = null;
      claim = toClaim(created);
      claimRetryAt = 0;
      reconnecting = false;
      attempt = 0;
      if (started && !offline) openFor(created.id);
      else reconnecting = true;
    } catch (e) {
      createError = `Không tạo được mã QR: ${errorMessage(e)}`;
    } finally {
      creating = false;
    }
    syncClaim();
    emit();
  }

  /** Starts whatever the current facts call for: resume the tab's session or open one for a card. */
  function reconcile() {
    if (!started || openStream === null || offline || notice !== null) return;
    if (sessionId !== null) {
      if (stream === null && !checking && reconnectTimer === null) void resume();
      syncClaim();
      return;
    }
    if (cards > 0 && !creating && createError === null) void create();
  }

  /** A refused card command: a stale state (409) is read again, a vanished session dropped. */
  const commandFailed = async (e: unknown, id: string, lead: string) => {
    if (isApiError(e) && e.status === 409) {
      await refetchStatus(id);
      return;
    }
    if (isSessionGone(e)) {
      forget();
      reconcile();
      return;
    }
    toast.error(`${lead}: ${errorMessage(e)}`);
  };

  const command = async (name: PairingCommand, run: (id: string) => Promise<void>, lead: string) => {
    const id = sessionId;
    if (id === null || busy !== null) return;
    busy = name;
    emit();
    try {
      await run(id);
    } catch (e) {
      if (sessionId === id) await commandFailed(e, id, lead);
    } finally {
      busy = null;
      emit();
    }
  };

  return {
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSnapshot: () => snapshot,
    start() {
      if (started) return;
      started = true;
      reconcile();
      emit();
    },
    stop() {
      started = false;
      stopStream();
      stopReconnect();
      stopClaimTimer();
      stopNoticeTimer();
      if (notice === "revoked") notice = null;
      emit();
    },
    setOffline(next) {
      if (offline === next) return;
      offline = next;
      if (next) {
        stopStream();
        stopReconnect();
        stopClaimTimer();
      } else {
        attempt = 0;
        createError = null;
        reconcile();
      }
      emit();
    },
    showCard() {
      cards++;
      reconcile();
      emit();
      let shown = true;
      return () => {
        if (!shown) return;
        shown = false;
        cards--;
        // A failure is forgotten with the last card; the next card tries again.
        if (cards === 0) createError = null;
        syncClaim();
        emit();
      };
    },
    registerTarget(ref) {
      // A wrapper per registration, so the same ref registered twice is removed one entry at a time.
      const entry: ScanTargetRef = {
        get current() {
          return ref.current;
        },
      };
      targets.push(entry);
      return () => {
        const i = targets.indexOf(entry);
        if (i >= 0) targets.splice(i, 1);
      };
    },
    approve: () =>
      command(
        "approve",
        async (id) => {
          const s = await approveScannerSession(client, id);
          if (sessionId === id) applyStatus(s);
        },
        "Không cho phép được điện thoại",
      ),
    reject: () =>
      command(
        "reject",
        async (id) => {
          const fresh = await rejectScannerSession(client, id);
          if (sessionId !== id) return;
          phase = "waiting";
          deviceLabel = null;
          verifyCode = null;
          claim = toClaim(fresh);
          claimRetryAt = 0;
          syncClaim();
        },
        "Không từ chối được điện thoại",
      ),
    revoke: () =>
      command(
        "revoke",
        async (id) => {
          await revokeScannerSession(client, id);
          if (sessionId !== id) return;
          forget();
          reconcile();
        },
        "Không ngắt được điện thoại",
      ),
    retry() {
      createError = null;
      reconcile();
      emit();
    },
    pairHere() {
      stopNoticeTimer();
      notice = null;
      reconcile();
      emit();
    },
  };
}
