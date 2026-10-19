import { useSyncExternalStore } from "react";
import { onlineManager } from "@tanstack/react-query";
import { API_BASE, PATHS } from "../../../shared/api/endpoints";

// Whether the API can be reached, from `navigator.onLine` plus a heartbeat: the browser saying it
// is online proves nothing about the server, so a failed request or the `online` event makes the
// state "degraded" and the heartbeat asks the API every 15 s (3 s timeout) until it answers.
//   online    the API answered; sales go to the server;
//   degraded  the browser has a network but the API does not answer;
//   offline   the browser has no network, or the dev/sandbox toggle "Mô phỏng mất mạng" is on.
// Only "online" sells through the server; the other two sell offline.

export type ConnectivityState = "online" | "offline" | "degraded";

export const HEARTBEAT_INTERVAL_MS = 15_000;
export const HEARTBEAT_TIMEOUT_MS = 3_000;
/** First check after a failed request: soon, but never in the same breath as the failure. */
export const FAILURE_RECHECK_MS = 5_000;

export interface ConnectivityOptions {
  /** True when the API answered (any status below 500). */
  probe: () => Promise<boolean>;
  isOnLine: () => boolean;
  intervalMs?: number;
  failureRecheckMs?: number;
}

export interface Connectivity {
  getState(): ConnectivityState;
  /** The dev/sandbox toggle forces "offline". */
  isSimulated(): boolean;
  subscribe(listener: () => void): () => void;
  /** Follows the browser's online/offline events; returns the function that stops it. */
  start(target?: Pick<Window, "addEventListener" | "removeEventListener">): () => void;
  /** "Mô phỏng mất mạng" (true) / "Kết nối lại mạng" (false). */
  simulate(offline: boolean): void;
  /**
   * A request got no answer from the API: sell offline and check again after a pause. Not probed at
   * once, so a server that answers the heartbeat but fails the request is not hammered in a loop.
   */
  reportFailure(): void;
  /** Asks the API now and returns the new state. */
  check(): Promise<ConnectivityState>;
  /** Back to the browser's view without probing, timers stopped (tests). */
  reset(): void;
}

/** GET /api/v1/me with a timeout: reachable when the API answered at all (401 included). */
export function heartbeatProbe(fetchImpl: typeof fetch = (...args) => fetch(...args), timeoutMs = HEARTBEAT_TIMEOUT_MS): () => Promise<boolean> {
  return async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), timeoutMs);
    try {
      const res = await fetchImpl(API_BASE + PATHS.me, { credentials: "include", cache: "no-store", headers: { Accept: "application/json" }, signal: ctrl.signal });
      return res.status < 500;
    } catch {
      return false;
    } finally {
      clearTimeout(timer);
    }
  };
}

export function createConnectivity({ probe, isOnLine, intervalMs = HEARTBEAT_INTERVAL_MS, failureRecheckMs = FAILURE_RECHECK_MS }: ConnectivityOptions): Connectivity {
  const listeners = new Set<() => void>();
  let state: ConnectivityState = isOnLine() ? "online" : "offline";
  let forced = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let generation = 0;

  const set = (next: ConnectivityState) => {
    if (next === state) return;
    state = next;
    for (const l of [...listeners]) l();
  };
  const stopTimer = () => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  };
  const schedule = (delay = intervalMs) => {
    stopTimer();
    if (forced || state === "online") return;
    timer = setTimeout(() => void check(), delay);
  };

  const check = async (): Promise<ConnectivityState> => {
    if (forced) return state;
    stopTimer();
    const mine = ++generation;
    const ok = await probe();
    // A newer check, the toggle or a reset happened meanwhile: that one decides.
    if (mine !== generation || forced) return state;
    set(ok ? "online" : isOnLine() ? "degraded" : "offline");
    schedule();
    return state;
  };

  const degrade = () => {
    if (forced) return;
    set(isOnLine() ? "degraded" : "offline");
    void check();
  };

  return {
    getState: () => state,
    isSimulated: () => forced,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    start(target = window) {
      const onOnline = () => degrade();
      const onOffline = () => {
        if (forced) return;
        generation++;
        set("offline");
        schedule();
      };
      target.addEventListener("online", onOnline);
      target.addEventListener("offline", onOffline);
      if (!forced && state !== "online") schedule();
      return () => {
        target.removeEventListener("online", onOnline);
        target.removeEventListener("offline", onOffline);
        stopTimer();
      };
    },
    simulate(offline) {
      if (offline) {
        forced = true;
        generation++;
        stopTimer();
        set("offline");
        // Listeners also learn about the toggle itself when the state was already offline.
        for (const l of [...listeners]) l();
        return;
      }
      if (!forced) return;
      forced = false;
      for (const l of [...listeners]) l();
      degrade();
    },
    reportFailure() {
      if (forced || state !== "online") return;
      generation++;
      set(isOnLine() ? "degraded" : "offline");
      schedule(Math.min(failureRecheckMs, intervalMs));
    },
    check,
    reset() {
      forced = false;
      generation++;
      stopTimer();
      set(isOnLine() ? "online" : "offline");
    },
  };
}

/** The page's connectivity (one per tab), started by main.tsx on store hosts. */
export const connectivity = createConnectivity({
  probe: heartbeatProbe(),
  isOnLine: () => (typeof navigator === "undefined" ? true : navigator.onLine),
});

/**
 * Makes TanStack Query follow the heartbeat instead of `navigator.onLine`: queries pause (keeping
 * their data) while the API cannot be reached and refetch when it answers again.
 */
export function bindOnlineManager(source: Connectivity = connectivity): void {
  onlineManager.setEventListener((setOnline) => {
    setOnline(source.getState() === "online");
    return source.subscribe(() => setOnline(source.getState() === "online"));
  });
}

export interface ConnectivityView {
  state: ConnectivityState;
  simulated: boolean;
}

let lastView: ConnectivityView = { state: connectivity.getState(), simulated: connectivity.isSimulated() };
function snapshot(): ConnectivityView {
  const state = connectivity.getState();
  const simulated = connectivity.isSimulated();
  if (lastView.state !== state || lastView.simulated !== simulated) lastView = { state, simulated };
  return lastView;
}

/** Current connectivity for the POS and the util bar. */
export function useConnectivity(): ConnectivityView {
  return useSyncExternalStore(connectivity.subscribe, snapshot, snapshot);
}
