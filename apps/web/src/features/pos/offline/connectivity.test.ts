import { onlineManager } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FAILURE_RECHECK_MS, bindOnlineManager, createConnectivity, heartbeatProbe, type ConnectivityState } from "./connectivity";

const INTERVAL = 15_000;

/** A connectivity driven by the test: `answer` decides each heartbeat, `onLine` the browser flag. */
function setup(init: { onLine?: boolean; answer?: boolean } = {}) {
  const env = { onLine: init.onLine ?? true, answer: init.answer ?? true, probes: 0 };
  const target = new EventTarget();
  const c = createConnectivity({
    probe: async () => {
      env.probes++;
      return env.answer;
    },
    isOnLine: () => env.onLine,
    intervalMs: INTERVAL,
  });
  const seen: ConnectivityState[] = [];
  c.subscribe(() => seen.push(c.getState()));
  const stop = c.start(target);
  return { c, env, target, seen, stop };
}

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("connectivity", () => {
  it("starts from the browser's view without probing", () => {
    expect(setup().c.getState()).toBe("online");
    expect(setup({ onLine: false }).c.getState()).toBe("offline");
  });

  it("degrades on a failed request, checks again after a pause, then every 15 s until the API answers", async () => {
    const { c, env, seen } = setup({ answer: false });
    c.reportFailure();
    await vi.advanceTimersByTimeAsync(0);
    expect(c.getState()).toBe("degraded");
    // Not probed in the same breath as the failure: a server failing requests while answering
    // the heartbeat would otherwise flip the state back at once and loop.
    expect(env.probes).toBe(0);
    await vi.advanceTimersByTimeAsync(FAILURE_RECHECK_MS);
    expect(env.probes).toBe(1);

    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(env.probes).toBe(2);
    expect(c.getState()).toBe("degraded");

    env.answer = true;
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(c.getState()).toBe("online");
    expect(seen).toEqual(["degraded", "online"]);

    // Online: the heartbeat stops.
    await vi.advanceTimersByTimeAsync(INTERVAL * 3);
    expect(env.probes).toBe(3);
  });

  it("goes offline on the browser's offline event and checks the API on its online event", async () => {
    const { c, env, target, seen } = setup({ answer: false });
    env.onLine = false;
    target.dispatchEvent(new Event("offline"));
    expect(c.getState()).toBe("offline");

    // The network is back but the API does not answer yet.
    env.onLine = true;
    target.dispatchEvent(new Event("online"));
    await vi.advanceTimersByTimeAsync(0);
    expect(c.getState()).toBe("degraded");

    env.answer = true;
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(c.getState()).toBe("online");
    expect(seen).toEqual(["offline", "degraded", "online"]);
  });

  it("an offline probe with the browser offline stays offline, not degraded", async () => {
    const { c, env } = setup({ onLine: false, answer: false });
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(env.probes).toBe(1);
    expect(c.getState()).toBe("offline");
  });

  it("ignores a failure reported while already offline", async () => {
    const { c, env, target } = setup({ answer: false });
    env.onLine = false;
    target.dispatchEvent(new Event("offline"));
    c.reportFailure();
    await vi.advanceTimersByTimeAsync(0);
    expect(env.probes).toBe(0);
  });

  it("the simulation forces offline without probing, and hands back to a real check", async () => {
    const { c, env, target } = setup();
    const notified = vi.fn();
    c.subscribe(notified);

    c.simulate(true);
    expect(c.getState()).toBe("offline");
    expect(c.isSimulated()).toBe(true);
    expect(notified).toHaveBeenCalled();

    // Browser events and failures are ignored while simulated.
    target.dispatchEvent(new Event("online"));
    c.reportFailure();
    await vi.advanceTimersByTimeAsync(INTERVAL * 2);
    expect(env.probes).toBe(0);
    expect(c.getState()).toBe("offline");
    expect(await c.check()).toBe("offline");

    c.simulate(false);
    expect(c.isSimulated()).toBe(false);
    await vi.advanceTimersByTimeAsync(0);
    expect(env.probes).toBe(1);
    expect(c.getState()).toBe("online");
  });

  it("a probe answering after the simulation was turned on does not override it", async () => {
    let release: (ok: boolean) => void = () => undefined;
    const c = createConnectivity({ probe: () => new Promise<boolean>((r) => (release = r)), isOnLine: () => true, intervalMs: INTERVAL });
    c.reportFailure();
    c.simulate(true);
    release(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(c.getState()).toBe("offline");
  });

  it("stops listening and probing once stopped", async () => {
    const { c, env, target, stop } = setup({ answer: false });
    c.reportFailure();
    await vi.advanceTimersByTimeAsync(FAILURE_RECHECK_MS);
    stop();
    await vi.advanceTimersByTimeAsync(INTERVAL * 2);
    expect(env.probes).toBe(1);
    env.onLine = false;
    target.dispatchEvent(new Event("offline"));
    expect(c.getState()).toBe("degraded");
  });

  it("drives TanStack Query's online state", async () => {
    const { c, env } = setup({ answer: false });
    bindOnlineManager(c);
    expect(onlineManager.isOnline()).toBe(true);
    c.reportFailure();
    await vi.advanceTimersByTimeAsync(0);
    expect(onlineManager.isOnline()).toBe(false);
    env.answer = true;
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(onlineManager.isOnline()).toBe(true);
    onlineManager.setEventListener(() => undefined);
    onlineManager.setOnline(true);
  });
});

describe("heartbeatProbe", () => {
  it("is reachable on any answer below 500, a 401 included", async () => {
    expect(await heartbeatProbe(async () => new Response(null, { status: 401 }))()).toBe(true);
    expect(await heartbeatProbe(async () => new Response(null, { status: 502 }))()).toBe(false);
    expect(await heartbeatProbe(() => Promise.reject(new TypeError("Failed to fetch")))()).toBe(false);
  });

  it("gives up after its timeout", async () => {
    const hanging: typeof fetch = (_input, init) =>
      new Promise((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      });
    const result = heartbeatProbe(hanging, 3_000)();
    await vi.advanceTimersByTimeAsync(3_000);
    expect(await result).toBe(false);
  });
});
