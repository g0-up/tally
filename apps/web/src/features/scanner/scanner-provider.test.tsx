import { act, cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiError } from "../../test/msw/handlers";
import { scannerServer } from "../../test/msw/scanner";
import { renderWithProviders } from "../../test/render";
import { connectivity } from "../pos/offline/connectivity";
import { PAIRING_QR_LABEL } from "./components/pairing-qr";
import { CLOSED_TEXT, CONNECTED_TEXT, OFFLINE_TEXT, PAIR_AGAIN_LABEL, PhonePairingCard, RECONNECTING_TEXT, REVOKED_TEXT } from "./components/phone-pairing-card";
import { useScanner } from "./scanner-context";
import type { ScannerController } from "./scanner-controller";
import { SESSION_STORAGE_KEY, createSessionStore, type SessionStore } from "./session-store";
import { createFakeStreams } from "./testing/fake-event-stream";
import { memorySessionStore, withScanner } from "./testing/scanner-harness";

const fakeClock = () => vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"], shouldAdvanceTime: true });
const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
const qrPath = () => screen.getByRole("img", { name: PAIRING_QR_LABEL }).querySelector("path")?.getAttribute("d");

// Unmount first: a provider still mounted would see the network come back and open a session.
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  connectivity.reset();
  sessionStorage.clear();
});

function renderScanner({ store = memorySessionStore(), card = true }: { store?: SessionStore; card?: boolean } = {}) {
  const streams = createFakeStreams();
  renderWithProviders(withScanner(card ? <PhonePairingCard /> : <p>Màn hình không có card</p>, streams, store));
  return { streams };
}

describe("ScannerProvider startup", () => {
  it("stays idle without a card or a saved session", async () => {
    const api = scannerServer();
    const { streams } = renderScanner({ card: false });
    await screen.findByText("Màn hình không có card");
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(api.calls).toEqual([]);
    expect(streams.all).toHaveLength(0);
  });

  it("reopens the stream of a saved live session even with no card on screen", async () => {
    const api = scannerServer({ "ss-9": { state: "claimed", device_label: "Pixel 8" } });
    const { streams } = renderScanner({ store: memorySessionStore("ss-9"), card: false });
    await waitFor(() => expect(streams.live()).toHaveLength(1));
    expect(streams.last().url).toBe("/api/v1/scanner-sessions/ss-9/events");
    expect(api.calls).toEqual(["GET /scanner-sessions/ss-9"]);
  });

  it("shows a resumed session as the stream's opening state says, without opening another", async () => {
    const api = scannerServer({ "ss-9": { state: "claimed", device_label: "Pixel 8" } });
    const { streams } = renderScanner({ store: memorySessionStore("ss-9") });
    await waitFor(() => expect(streams.live()).toHaveLength(1));
    expect(screen.getByText(RECONNECTING_TEXT)).toBeInTheDocument();
    streams.last().emit("state", { state: "claimed", device_label: "Pixel 8" });
    expect(screen.getByText(CONNECTED_TEXT)).toBeInTheDocument();
    expect(screen.getByText(/Pixel 8/)).toBeInTheDocument();
    expect(api.calls).toEqual(["GET /scanner-sessions/ss-9"]);
  });

  it.each([
    ["ended", { "ss-9": { state: "ended" as const } }],
    ["unknown to the API (404)", {}],
  ])("drops a saved session that is %s and opens a new one for the card", async (_, fixtures) => {
    const api = scannerServer(fixtures);
    const store = memorySessionStore("ss-9");
    const { streams } = renderScanner({ store });
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["GET /scanner-sessions/ss-9", "POST /scanner-sessions"]);
    expect(store.id).toBe("ss-1");
    expect(streams.live().map((s) => s.url)).toEqual(["/api/v1/scanner-sessions/ss-1/events"]);
  });

  it("opens a session when a card shows and keeps only its id in sessionStorage", async () => {
    const api = scannerServer();
    const { streams } = renderScanner({ store: createSessionStore() });
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions"]);
    expect(streams.live()).toHaveLength(1);
    expect(JSON.parse(sessionStorage.getItem(SESSION_STORAGE_KEY) ?? "null")).toEqual({ id: "ss-1" });
    for (let i = 0; i < sessionStorage.length; i++) expect(sessionStorage.getItem(sessionStorage.key(i) ?? "")).not.toContain("CLAIM");
  });

  it("keeps the session in memory when sessionStorage refuses it", async () => {
    scannerServer();
    const blocked = () => {
      throw new DOMException("blocked", "SecurityError");
    };
    const store = createSessionStore(blocked);
    renderScanner({ store });
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(store.load()).toBeNull();
  });
});

describe("claim code", () => {
  it("asks for a new claim code before the current one expires and shows it", async () => {
    fakeClock();
    const api = scannerServer();
    renderScanner();
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    const first = qrPath();
    await advance(110_000);
    expect(api.calls).toEqual(["POST /scanner-sessions"]);
    await advance(5_000);
    await waitFor(() => expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions/ss-1:refresh-code"]));
    await waitFor(() => expect(qrPath()).not.toBe(first));
    expect(api.claimCodes()).toBe(2);
  });

  it("reads the session again, without an error, when a phone already claimed the code", async () => {
    fakeClock();
    const api = scannerServer();
    renderScanner();
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    // A phone claimed the code but its event never reached this tab.
    Object.assign(api.sessions.get("ss-1") ?? {}, { state: "pending", device_label: "iPhone", verify_code: "4821" });
    await advance(115_000);
    expect(await screen.findByText("4821")).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions/ss-1:refresh-code", "GET /scanner-sessions/ss-1"]);
    expect(screen.queryByText(/Không /)).not.toBeInTheDocument();
  });
});

describe("reconnecting", () => {
  it("takes the card's state from the `state` event sent when the browser reconnects by itself", async () => {
    scannerServer();
    const { streams } = renderScanner();
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    const stream = streams.last();
    stream.emit("state", { state: "waiting" });
    stream.fail("reconnecting");
    expect(screen.getByText(RECONNECTING_TEXT)).toBeInTheDocument();
    stream.emit("state", { state: "claimed", device_label: "Pixel 8" });
    expect(screen.getByText(CONNECTED_TEXT)).toBeInTheDocument();
    expect(streams.all).toHaveLength(1);
  });

  it("reopens after `restarting` once a second has passed, asking the session first", async () => {
    fakeClock();
    const api = scannerServer();
    const store = memorySessionStore();
    const { streams } = renderScanner({ store });
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    streams.last().emit("restarting");
    expect(streams.live()).toHaveLength(0);
    expect(screen.getByText(RECONNECTING_TEXT)).toBeInTheDocument();
    await advance(900);
    expect(api.calls).toEqual(["POST /scanner-sessions"]);
    await advance(200);
    await waitFor(() => expect(streams.live()).toHaveLength(1));
    expect(api.calls).toEqual(["POST /scanner-sessions", "GET /scanner-sessions/ss-1"]);
    expect(store.id).toBe("ss-1");
    streams.last().emit("state", { state: "waiting" });
    expect(screen.getByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
  });

  it.each([
    ["a proxy's 404 page", () => new HttpResponse("404 page not found", { status: 404, headers: { "Content-Type": "text/plain" } })],
    ["503 server_restarting", () => apiError(503, "server_restarting")],
  ])("keeps the session through %s and retries after 1, 2 then 5 seconds", async (_, failure) => {
    fakeClock();
    const api = scannerServer();
    const store = memorySessionStore();
    const { streams } = renderScanner({ store });
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    let failures = 2;
    api.onGet(() => (failures-- > 0 ? failure() : undefined));
    const gets = () => api.calls.filter((c) => c === "GET /scanner-sessions/ss-1").length;

    streams.last().fail("closed");
    await advance(1_100);
    await waitFor(() => expect(gets()).toBe(1));
    await advance(1_800);
    expect(gets()).toBe(1);
    await advance(400);
    await waitFor(() => expect(gets()).toBe(2));
    await advance(4_400);
    expect(gets()).toBe(2);
    expect(streams.live()).toHaveLength(0);
    await advance(500);
    await waitFor(() => expect(streams.live()).toHaveLength(1));
    expect(gets()).toBe(3);
    expect(store.id).toBe("ss-1");
    expect(api.calls.filter((c) => c === "POST /scanner-sessions")).toHaveLength(1);
    expect(screen.getByText(RECONNECTING_TEXT)).toBeInTheDocument();
  });

  it("drops the session when the stream closes because it ended, then opens a new one for the card", async () => {
    fakeClock();
    const api = scannerServer();
    const store = memorySessionStore();
    const { streams } = renderScanner({ store });
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    // The server answers an ended session's stream with 204, which closes EventSource for good.
    Object.assign(api.sessions.get("ss-1") ?? {}, { state: "ended" });
    streams.last().fail("closed");
    await advance(1_100);
    await waitFor(() => expect(store.id).toBe("ss-2"));
    expect(api.calls).toEqual(["POST /scanner-sessions", "GET /scanner-sessions/ss-1", "POST /scanner-sessions"]);
  });
});

describe("events that end the pairing", () => {
  it("steps aside without opening a session when another tab took this one", async () => {
    const api = scannerServer();
    const store = memorySessionStore();
    const { streams } = renderScanner({ store });
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    streams.last().emit("replaced");
    const pairHere = await screen.findByRole("button", { name: "Ghép đôi ở tab này" });
    expect(store.id).toBeNull();
    expect(streams.live()).toHaveLength(0);
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(api.calls).toEqual(["POST /scanner-sessions"]);

    await userEvent.click(pairHere);
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions"]);
    expect(store.id).toBe("ss-2");
  });

  it("says the phone disconnected for a few seconds, then shows a new QR code", async () => {
    fakeClock();
    const api = scannerServer();
    const store = memorySessionStore();
    const { streams } = renderScanner({ store });
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    streams.last().emit("revoked", { by: "phone" });
    expect(screen.getByText(REVOKED_TEXT)).toBeInTheDocument();
    expect(store.id).toBeNull();
    await advance(3_500);
    expect(api.calls).toEqual(["POST /scanner-sessions"]);
    await advance(600);
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(store.id).toBe("ss-2");
  });

  it("says the server closed the session and waits for the cashier to pair again", async () => {
    fakeClock();
    const api = scannerServer();
    const store = memorySessionStore();
    const { streams } = renderScanner({ store });
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    streams.last().emit("revoked", { by: "system" });
    expect(screen.getByText(CLOSED_TEXT)).toBeInTheDocument();
    expect(store.id).toBeNull();
    expect(streams.live()).toHaveLength(0);
    // Opening a session by itself would close another of the user's sessions, which would answer
    // the same way.
    await advance(10_000);
    expect(api.calls).toEqual(["POST /scanner-sessions"]);

    await userEvent.click(screen.getByRole("button", { name: PAIR_AGAIN_LABEL }));
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions"]);
    expect(store.id).toBe("ss-2");
  });

  it("keeps a later disconnect notice up for its full time, opening one session after it", async () => {
    fakeClock();
    const api = scannerServer();
    let controller: ScannerController | null = null;
    function Grab() {
      controller = useScanner();
      return null;
    }
    const streams = createFakeStreams();
    renderWithProviders(
      withScanner(
        <>
          <PhonePairingCard />
          <Grab />
        </>,
        streams,
        memorySessionStore(),
      ),
    );
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    streams.last().emit("revoked", { by: "phone" });
    await advance(2_000);
    act(() => controller?.pairHere());
    await waitFor(() => expect(streams.live()).toHaveLength(1));
    streams.last().emit("revoked", { by: "phone" });
    expect(screen.getByText(REVOKED_TEXT)).toBeInTheDocument();

    // The first notice's time is up here; only the second one's counts.
    await advance(2_500);
    expect(screen.getByText(REVOKED_TEXT)).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions"]);
    await advance(1_600);
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions", "POST /scanner-sessions"]);
  });
});

describe("offline", () => {
  it("opens no session without a network and starts again when it returns", async () => {
    act(() => connectivity.simulate(true));
    const api = scannerServer();
    renderScanner();
    expect(await screen.findByText(OFFLINE_TEXT)).toBeInTheDocument();
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(api.calls).toEqual([]);

    act(() => connectivity.simulate(false));
    expect(await screen.findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions"]);
  });

  it("closes the stream while offline and resumes the same session afterwards", async () => {
    const api = scannerServer();
    const { streams } = renderScanner();
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    act(() => connectivity.simulate(true));
    expect(screen.getByText(OFFLINE_TEXT)).toBeInTheDocument();
    expect(streams.live()).toHaveLength(0);

    act(() => connectivity.simulate(false));
    await waitFor(() => expect(streams.live()).toHaveLength(1));
    expect(streams.last().url).toBe("/api/v1/scanner-sessions/ss-1/events");
    expect(api.calls).toEqual(["POST /scanner-sessions", "GET /scanner-sessions/ss-1"]);
  });
});
