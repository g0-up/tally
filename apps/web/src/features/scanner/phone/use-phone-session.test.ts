import { act } from "@testing-library/react";
import { HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createCsrfSource } from "../../../shared/api/csrf";
import { API_BASE } from "../../../shared/api/endpoints";
import { createHttpClient } from "../../../shared/api/http";
import { getToasts } from "../../../shared/ui/toast-store";
import { apiError } from "../../../test/msw/handlers";
import { phoneScanServer, type PhoneFixtureState } from "../../../test/msw/scanner";
import { RECONNECT_DELAYS_MS } from "../reconnect";
import { createFakeStreams } from "../testing/fake-event-stream";
import { newScan } from "./send-scan";
import { createPhoneSession, takeClaimCode, type PhoneView } from "./use-phone-session";

const fakeClock = () => vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"], shouldAdvanceTime: true });
const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
const noSleep = { sleep: () => Promise.resolve() };

afterEach(() => {
  vi.useRealTimers();
  window.history.replaceState(null, "", "/");
});

function open(url: string) {
  window.history.replaceState(null, "", url);
}

function setup({ state = "none", streams = true }: { state?: PhoneFixtureState; streams?: boolean } = {}) {
  const api = phoneScanServer({ state });
  const fake = createFakeStreams();
  const client = createHttpClient({ baseUrl: API_BASE, csrf: createCsrfSource([]) });
  const session = createPhoneSession({ client, openStream: streams ? fake.open : null, sendOptions: noSleep, random: () => 0.5 });
  const view = () => session.getSnapshot().view;
  const until = (expected: PhoneView) => vi.waitFor(() => expect(view()).toEqual(expected));
  return { api, streams: fake, session, view, until };
}

describe("takeClaimCode", () => {
  it("reads the code from the fragment and wipes it from the address bar", () => {
    open("/scan#c=CLAIM%2D1");
    expect(takeClaimCode()).toBe("CLAIM-1");
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/scan");
  });

  it("finds no code in an empty or foreign fragment, and still wipes it", () => {
    open("/scan");
    expect(takeClaimCode()).toBeNull();
    open("/scan#x=1");
    expect(takeClaimCode()).toBeNull();
    expect(window.location.hash).toBe("");
  });
});

describe("createPhoneSession", () => {
  it("claims the code from the link, shows the verify code and waits for the desktop", async () => {
    open("/scan#c=CLAIM-1");
    const { api, streams, session, until } = setup();
    session.start();
    expect(window.location.hash).toBe("");
    await until({ kind: "pending", verifyCode: "4821" });
    expect(api.calls).toEqual(["POST /scan/claim"]);
    expect(streams.live().map((s) => s.url)).toEqual(["/api/v1/scan/events"]);

    streams.last().emit("state", { state: "pending", verify_code: "4821" });
    streams.last().emit("approved");
    await until({ kind: "claimed" });
    expect(session.getSnapshot().reconnecting).toBe(false);
  });

  it("stops for good when the desktop rejects the phone", async () => {
    open("/scan#c=CLAIM-1");
    const { streams, session, until } = setup();
    session.start();
    await until({ kind: "pending", verifyCode: "4821" });
    streams.last().emit("rejected");
    await until({ kind: "rejected" });
    expect(streams.live()).toEqual([]);
  });

  it("says a spent link cannot be used, with nothing to retry", async () => {
    open("/scan#c=OLD");
    const { api, session, until } = setup();
    session.start();
    await until({ kind: "claim_invalid" });
    session.retry();
    expect(api.calls).toEqual(["POST /scan/claim"]);
  });

  it("keeps the code after a claim that got no answer and claims it again on retry", async () => {
    open("/scan#c=CLAIM-1");
    const { api, session, view, until } = setup();
    let down = true;
    api.onClaim(() => (down ? HttpResponse.error() : undefined));
    session.start();
    await vi.waitFor(() => expect(view().kind).toBe("failed"));
    down = false;
    session.retry();
    await until({ kind: "pending", verifyCode: "4821" });
    expect(api.calls).toEqual(["POST /scan/claim", "POST /scan/claim"]);
  });

  it("asks to scan the QR code when opened without a code and without a session", async () => {
    open("/scan");
    const { api, streams, session, until } = setup();
    session.start();
    await until({ kind: "no_code" });
    expect(api.calls).toEqual(["GET /scan/session"]);
    expect(streams.all).toEqual([]);
  });

  it("resumes the session its cookie holds after a reload", async () => {
    open("/scan");
    const { streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    expect(streams.live()).toHaveLength(1);
  });

  it("says phone scanning is unavailable without EventSource, before any request", async () => {
    open("/scan#c=CLAIM-1");
    const { api, session, until } = setup({ streams: false });
    session.start();
    await until({ kind: "unsupported" });
    expect(api.calls).toEqual([]);
    expect(window.location.hash).toBe("");
  });

  it("goes back to waiting when a scan says the phone is no longer approved", async () => {
    open("/scan");
    const { api, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    api.session.state = "pending";
    expect(await session.send(newScan("123"))).toBeNull();
    await until({ kind: "pending", verifyCode: "4821" });
    expect(api.calls.filter((c) => c === "GET /scan/session")).toHaveLength(2);
  });

  it("ends the session when a scan gets the API's 401", async () => {
    open("/scan");
    const { api, streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    api.session.state = "ended";
    expect(await session.send(newScan("123"))).toBeNull();
    await until({ kind: "ended", by: "desktop" });
    expect(streams.live()).toEqual([]);
  });

  it("returns the outcome line of a scan while approved", async () => {
    open("/scan");
    const { session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    expect(await session.send(newScan("123"))).toEqual({ text: "Mứt gừng · Hộp", tone: "ok" });
  });

  it("rides out a server restart: stays claimed and reconnects after 1, then 2 s", async () => {
    fakeClock();
    open("/scan");
    const { api, streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    streams.last().emit("restarting");
    expect(session.getSnapshot()).toMatchObject({ view: { kind: "claimed" }, reconnecting: true });
    expect(streams.live()).toEqual([]);

    let down = true;
    api.onSession(() => (down ? new HttpResponse("<html>Bad gateway</html>", { status: 502 }) : undefined));
    await advance(RECONNECT_DELAYS_MS[0]);
    expect(api.calls.filter((c) => c === "GET /scan/session")).toHaveLength(2);
    expect(session.getSnapshot()).toMatchObject({ view: { kind: "claimed" }, reconnecting: true });

    down = false;
    // A margin for the real time the clock also counts.
    await advance(RECONNECT_DELAYS_MS[1] - 200);
    expect(api.calls.filter((c) => c === "GET /scan/session")).toHaveLength(2);
    await advance(200);
    await vi.waitFor(() => expect(streams.live()).toHaveLength(1));
    streams.last().emit("state", { state: "claimed" });
    expect(session.getSnapshot()).toMatchObject({ view: { kind: "claimed" }, reconnecting: false });
  });

  it("reconnects when the browser gives up on the stream", async () => {
    fakeClock();
    open("/scan");
    const { streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    streams.last().fail("closed");
    expect(session.getSnapshot().reconnecting).toBe(true);
    await advance(RECONNECT_DELAYS_MS[0]);
    await vi.waitFor(() => expect(streams.live()).toHaveLength(1));
    expect(streams.all).toHaveLength(2);
  });

  it("ends the session when the reconnect finds it over", async () => {
    fakeClock();
    open("/scan");
    const { api, streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    api.onSession(() => apiError(401, "scan_session_ended"));
    streams.last().emit("restarting");
    await advance(RECONNECT_DELAYS_MS[0]);
    await until({ kind: "ended", by: "desktop" });
  });

  it.each<[string, unknown, PhoneView]>([
    ["revoked by the phone", { by: "phone" }, { kind: "ended", by: "phone" }],
    ["revoked by the desktop", { by: "desktop" }, { kind: "ended", by: "desktop" }],
  ])("ends the session when %s", async (_, data, expected) => {
    open("/scan");
    const { streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    streams.last().emit("revoked", data);
    await until(expected);
    expect(streams.live()).toEqual([]);
  });

  it("ends the session on an ended event or an ended opening state", async () => {
    open("/scan");
    const { streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    streams.last().emit("state", { state: "ended" });
    await until({ kind: "ended", by: "desktop" });
  });

  it("steps aside for another tab and takes the session back on request", async () => {
    open("/scan");
    const { streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    streams.last().emit("replaced");
    await until({ kind: "replaced" });
    expect(streams.live()).toEqual([]);
    session.takeOver();
    await until({ kind: "claimed" });
    expect(streams.live()).toHaveLength(1);
  });

  it("disconnects with DELETE and closes the stream", async () => {
    open("/scan");
    const { api, streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    await act(() => session.disconnect());
    expect(session.getSnapshot()).toEqual({ view: { kind: "ended", by: "phone" }, reconnecting: false, leaving: false });
    expect(api.calls.at(-1)).toBe("DELETE /scan/session");
    expect(streams.live()).toEqual([]);
  });

  it("stays connected and says so when the disconnect fails", async () => {
    open("/scan");
    const { api, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    api.onDelete(() => apiError(503, "service_unavailable"));
    await act(() => session.disconnect());
    expect(session.getSnapshot()).toMatchObject({ view: { kind: "claimed" }, leaving: false });
    expect(getToasts().map((t) => t.kind)).toEqual(["err"]);
  });

  it("closes the stream and stops reconnecting when stopped", async () => {
    fakeClock();
    open("/scan");
    const { api, streams, session, until } = setup({ state: "claimed" });
    session.start();
    await until({ kind: "claimed" });
    streams.last().emit("restarting");
    session.stop();
    await advance(RECONNECT_DELAYS_MS[4]);
    expect(api.calls).toEqual(["GET /scan/session"]);
    expect(streams.live()).toEqual([]);
  });
});
