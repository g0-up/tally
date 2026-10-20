import { useSyncExternalStore } from "react";
import { z } from "zod";
import { errorMessage, isApiError } from "../../../shared/api/api-error";
import { buildUrl, type HttpClient } from "../../../shared/api/http";
import { toast } from "../../../shared/ui/toast-store";
import { revokedEventSchema } from "../api/schemas";
import type { EventStream, OpenEventStream } from "../event-stream";
import { CLAIM_HASH_KEY } from "../pairing-url";
import { reconnectDelay } from "../reconnect";
import { outcomeCopy, scanFailureCopy, type ScanLine } from "./outcome-copy";
import { sendScan, type PendingScan, type SendScanOptions } from "./send-scan";

// The phone's side of a scanner session, outside React like the desktop's controller so its stream,
// timers and retries never read a stale render:
//   - the claim code is read from the URL fragment once and wiped from the address bar and history
//     at once; it lives on only in memory, for "Thử lại" after a failed claim. The session itself is
//     the HttpOnly cookie the claim sets;
//   - without a code, GET /scan/session says whether this phone still holds a session;
//   - the `state` event sent whenever the stream opens is the only source of the session's state;
//   - `restarting`, a closed stream and a GET that got no API answer (network, 5xx, a proxy page)
//     are a restart passing by: the camera stays on and the stream reopens after about 1, 2, 5, 10,
//     then 30 s, forever, asking GET first;
//   - a 401 the API wrote, on any request, means the session is over.

/** Who ended the session, for the sentence shown: this phone, or the desktop (or the server). */
export type EndedBy = "phone" | "desktop";

export type PhoneView =
  | { kind: "starting" }
  | { kind: "unsupported" }
  | { kind: "no_code" }
  | { kind: "claim_invalid" }
  | { kind: "failed"; message: string }
  | { kind: "pending"; verifyCode: string }
  | { kind: "claimed" }
  | { kind: "rejected" }
  | { kind: "ended"; by: EndedBy }
  | { kind: "replaced" };

export interface PhoneSnapshot {
  view: PhoneView;
  /** The stream dropped and is being reopened (pending and claimed only). */
  reconnecting: boolean;
  /** "Ngắt kết nối" is in flight. */
  leaving: boolean;
}

export interface PhoneSession {
  subscribe(listener: () => void): () => void;
  getSnapshot(): PhoneSnapshot;
  start(): void;
  stop(): void;
  /** Tries the failed claim, or the session check, again. */
  retry(): void;
  /** Takes the session back after another tab opened its stream. */
  takeOver(): void;
  disconnect(): Promise<void>;
  /** The line for one scan; null when the session is no longer approved (the view changed instead). */
  send(scan: PendingScan): Promise<ScanLine | null>;
}

export interface PhoneSessionOptions {
  client: HttpClient;
  /** Null when the browser has no EventSource: the page says phone scanning is unavailable. */
  openStream: OpenEventStream | null;
  sendOptions?: SendScanOptions;
  /** Spreads the reconnect waits; Math.random by default. */
  random?: () => number;
}

export const phoneStateSchema = z.object({
  state: z.enum(["pending", "claimed", "ended"]),
  verify_code: z.string().optional(),
});
export type PhoneState = z.infer<typeof phoneStateSchema>;

/** The session is over: a 401 the API wrote (scan_session_ended), not a proxy's. */
function isSessionOver(e: unknown): boolean {
  return isApiError(e) && e.status === 401 && e.fromServer;
}

/** Reads the claim code from the URL fragment and wipes the fragment from the address bar and history. */
export function takeClaimCode(): string | null {
  const { hash, pathname } = window.location;
  if (hash === "" || hash === "#") return null;
  const code = new URLSearchParams(hash.slice(1)).get(CLAIM_HASH_KEY)?.trim() ?? "";
  // The router's own history state is kept, so its back and forward still work.
  window.history.replaceState(window.history.state, "", pathname);
  return code === "" ? null : code;
}

function sameSnapshot(a: PhoneSnapshot, b: PhoneSnapshot): boolean {
  return a.reconnecting === b.reconnecting && a.leaving === b.leaving && JSON.stringify(a.view) === JSON.stringify(b.view);
}

export function createPhoneSession({ client, openStream, sendOptions, random = Math.random }: PhoneSessionOptions): PhoneSession {
  const listeners = new Set<() => void>();
  let started = false;
  let booted = false;
  let view: PhoneView = { kind: "starting" };
  let reconnecting = false;
  let leaving = false;
  let claimCode: string | null = null;
  let claiming = false;
  let checking = false;
  let stream: EventStream | null = null;
  let attempt = 0;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  // Bumped whenever the session ends or is taken over, so an answer for the old one is dropped.
  let epoch = 0;

  const live = () => view.kind === "pending" || view.kind === "claimed";

  let snapshot: PhoneSnapshot = { view, reconnecting, leaving };
  const emit = () => {
    const next = { view, reconnecting: reconnecting && live(), leaving };
    if (sameSnapshot(next, snapshot)) return;
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

  /** Leaves the session for good on this page: no stream, no timer. */
  const finish = (next: PhoneView) => {
    epoch++;
    stopStream();
    stopReconnect();
    reconnecting = false;
    attempt = 0;
    view = next;
    emit();
  };

  const applyState = (s: PhoneState) => {
    if (s.state === "ended") {
      finish({ kind: "ended", by: "desktop" });
      return;
    }
    view = s.state === "pending" ? { kind: "pending", verifyCode: s.verify_code ?? "" } : { kind: "claimed" };
  };

  const scheduleReconnect = () => {
    stopStream();
    stopReconnect();
    reconnecting = true;
    if (started && live()) {
      const delay = reconnectDelay(attempt, random);
      attempt++;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        void resume();
      }, delay);
    }
    emit();
  };

  function openFor(e: number) {
    if (openStream === null || stream !== null) return;
    let self: EventStream | null = null;
    const on =
      <A extends unknown[]>(fn: (...args: A) => void) =>
      (...args: A) => {
        if (self !== null && stream === self && epoch === e) fn(...args);
      };
    self = openStream(buildUrl(client.baseUrl, "/scan/events"), {
      events: {
        state: on((data) => {
          const parsed = phoneStateSchema.safeParse(data);
          if (!parsed.success) return;
          attempt = 0;
          reconnecting = false;
          applyState(parsed.data);
          emit();
        }),
        approved: on(() => {
          view = { kind: "claimed" };
          emit();
        }),
        rejected: on(() => finish({ kind: "rejected" })),
        revoked: on((data) => {
          const by = revokedEventSchema.safeParse(data);
          finish({ kind: "ended", by: by.success && by.data.by === "phone" ? "phone" : "desktop" });
        }),
        ended: on(() => finish({ kind: "ended", by: "desktop" })),
        // Another tab of this phone opened the session's stream: step aside without taking it back,
        // so the two tabs do not keep taking it from each other.
        replaced: on(() => finish({ kind: "replaced" })),
        restarting: on(() => scheduleReconnect()),
      },
      onError: on((state) => {
        if (state === "closed") {
          scheduleReconnect();
          return;
        }
        // The browser retries by itself; the next `state` event says where things stand.
        reconnecting = true;
        emit();
      }),
    });
    stream = self;
  }

  /**
   * Asks the session's state, then opens its stream. Before any session was seen, no session means
   * "scan the QR code" and no answer offers a retry; afterwards no answer is a reconnect.
   */
  async function resume() {
    if (checking) return;
    const e = epoch;
    const known = live();
    checking = true;
    let status: PhoneState | null = null;
    let failure: unknown = null;
    try {
      status = await client.get("/scan/session", { schema: phoneStateSchema, silent: true });
    } catch (err) {
      failure = err;
    }
    checking = false;
    if (epoch !== e) return;
    if (status === null) {
      if (isSessionOver(failure)) finish(known ? { kind: "ended", by: "desktop" } : { kind: "no_code" });
      else if (known) scheduleReconnect();
      else finish({ kind: "failed", message: errorMessage(failure) });
      return;
    }
    if (status.state === "ended" && !known) {
      finish({ kind: "no_code" });
      return;
    }
    applyState(status);
    if (epoch === e && started && live()) openFor(e);
    emit();
  }

  async function claim(code: string) {
    if (claiming) return;
    claiming = true;
    const e = epoch;
    view = { kind: "starting" };
    emit();
    let status: PhoneState | null = null;
    let failure: unknown = null;
    try {
      status = await client.post("/scan/claim", { code }, { schema: phoneStateSchema, silent: true });
    } catch (err) {
      failure = err;
    }
    claiming = false;
    if (epoch !== e) return;
    if (status === null) {
      // A code that is spent, expired, unknown or malformed is not worth retrying; anything else
      // (no network, maintenance, too many tries) keeps it for "Thử lại".
      const spent = isApiError(failure) && failure.fromServer && (failure.code === "claim_invalid" || failure.status === 410 || failure.status === 422);
      if (spent) claimCode = null;
      finish(spent ? { kind: "claim_invalid" } : { kind: "failed", message: errorMessage(failure) });
      return;
    }
    claimCode = null;
    applyState(status);
    if (epoch === e && started && live()) openFor(e);
    emit();
  }

  const boot = () => {
    booted = true;
    const code = takeClaimCode();
    if (openStream === null) {
      finish({ kind: "unsupported" });
      return;
    }
    claimCode = code;
    if (code !== null) void claim(code);
    else void resume();
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
      if (!booted) boot();
      else if (live()) void resume();
    },
    stop() {
      started = false;
      stopStream();
      stopReconnect();
    },
    retry() {
      if (view.kind !== "failed") return;
      epoch++;
      if (claimCode !== null) void claim(claimCode);
      else {
        view = { kind: "starting" };
        emit();
        void resume();
      }
    },
    takeOver() {
      if (view.kind !== "replaced") return;
      epoch++;
      view = { kind: "starting" };
      emit();
      void resume();
    },
    async disconnect() {
      if (leaving || !live()) return;
      leaving = true;
      emit();
      try {
        await client.delete("/scan/session", { silent: true });
        finish({ kind: "ended", by: "phone" });
      } catch (e) {
        // The cookie is cleared on a 401 too: the session was already over.
        if (isSessionOver(e)) finish({ kind: "ended", by: "phone" });
        else toast.error(`Không ngắt được kết nối: ${errorMessage(e)}`);
      } finally {
        leaving = false;
        emit();
      }
    },
    async send(scan) {
      const e = epoch;
      try {
        return outcomeCopy(await sendScan(client, scan, sendOptions));
      } catch (err) {
        if (epoch !== e) return null;
        if (isSessionOver(err)) {
          finish({ kind: "ended", by: "desktop" });
          return null;
        }
        if (isApiError(err) && err.code === "scan_pending_approval") {
          // The desktop has not approved this phone (yet, or again): back to the waiting screen,
          // whose code GET fills in.
          view = { kind: "pending", verifyCode: view.kind === "pending" ? view.verifyCode : "" };
          emit();
          void resume();
          return null;
        }
        return scanFailureCopy(err);
      }
    },
  };
}

export function usePhoneSession(session: PhoneSession): PhoneSnapshot {
  return useSyncExternalStore(session.subscribe, session.getSnapshot, session.getSnapshot);
}
