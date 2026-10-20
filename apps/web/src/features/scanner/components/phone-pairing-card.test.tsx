import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { apiError } from "../../../test/msw/handlers";
import { scannerServer } from "../../../test/msw/scanner";
import { renderWithProviders } from "../../../test/render";
import { connectivity } from "../../pos/offline/connectivity";
import { pairingUrl } from "../pairing-url";
import { ScannerProvider } from "../scanner-provider";
import { createFakeStreams } from "../testing/fake-event-stream";
import { memorySessionStore, pairedStream, withScanner } from "../testing/scanner-harness";
import { PAIRING_QR_LABEL, PairingQr } from "./pairing-qr";
import { CONNECTED_TEXT, OFFLINE_TEXT, PAIRING_CARD_LABEL, PhonePairingCard, UNSUPPORTED_TEXT } from "./phone-pairing-card";

// Unmount first: a provider still mounted would see the network come back and open a session.
afterEach(() => {
  cleanup();
  connectivity.reset();
});

/** The path the QR code of `value` is drawn with. */
function qrPathOf(value: string): string | null | undefined {
  const { container, unmount } = render(<PairingQr value={value} />);
  const d = container.querySelector("path")?.getAttribute("d");
  unmount();
  return d;
}

const card = () => screen.getByRole("region", { name: PAIRING_CARD_LABEL });
const shownQrPath = () => within(card()).getByRole("img", { name: PAIRING_QR_LABEL }).querySelector("path")?.getAttribute("d");

function renderCard(saved: string | null = null) {
  const streams = createFakeStreams();
  const store = memorySessionStore(saved);
  renderWithProviders(withScanner(<PhonePairingCard />, streams, store));
  return { streams, store };
}

/** A card whose phone just claimed the code and waits for "Cho phép". */
async function askingCard() {
  const api = scannerServer();
  const { streams } = renderCard();
  await screen.findByRole("img", { name: PAIRING_QR_LABEL });
  Object.assign(api.sessions.get("ss-1") ?? {}, { state: "pending", device_label: "iPhone · Safari", verify_code: "4821" });
  streams.last().emit("claim_requested", { device_label: "iPhone · Safari", verify_code: "4821" });
  return { api, streams };
}

describe("PhonePairingCard", () => {
  it("shows the QR code of the pairing address while it waits for a phone", async () => {
    scannerServer();
    renderCard();
    expect(await within(await screen.findByRole("region", { name: PAIRING_CARD_LABEL })).findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(shownQrPath()).toBe(qrPathOf(pairingUrl("CLAIM-1")));
    expect(pairingUrl("CLAIM-1")).toBe(`${window.location.origin}/scan#c=CLAIM-1`);
    const status = within(card()).getByRole("status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveTextContent("Quét bằng điện thoại");
  });

  it("asks before letting a phone in, with its verify code, and puts the focus on Từ chối", async () => {
    await askingCard();
    expect(within(card()).getByRole("status")).toHaveTextContent("Điện thoại iPhone · Safari muốn kết nối. Mã xác minh 4821. Chỉ cho phép nếu mã trên điện thoại giống mã này.");
    expect(within(card()).getByRole("button", { name: "Cho phép" })).toBeInTheDocument();
    expect(within(card()).getByRole("button", { name: "Từ chối" })).toHaveFocus();
    expect(within(card()).queryByRole("img", { name: PAIRING_QR_LABEL })).not.toBeInTheDocument();
  });

  it("lets the phone in on Cho phép and then says it is connected", async () => {
    const { api } = await askingCard();
    await userEvent.click(within(card()).getByRole("button", { name: "Cho phép" }));
    expect(await within(card()).findByText(CONNECTED_TEXT)).toBeInTheDocument();
    expect(within(card()).getByRole("status")).toHaveTextContent(`${CONNECTED_TEXT} · iPhone · Safari`);
    expect(within(card()).getByRole("button", { name: "Ngắt" })).toBeInTheDocument();
    expect(api.calls).toContain("POST /scanner-sessions/ss-1:approve");
  });

  it("announces every change in one status element that stays in place", async () => {
    scannerServer();
    const { streams } = renderCard();
    await screen.findByRole("img", { name: PAIRING_QR_LABEL });
    const status = within(card()).getByRole("status");

    streams.last().emit("claim_requested", { device_label: "iPhone · Safari", verify_code: "4821" });
    expect(status).toHaveTextContent("Mã xác minh 4821");
    expect(within(card()).getByRole("status")).toBe(status);

    streams.last().emit("state", { state: "claimed", device_label: "iPhone · Safari" });
    expect(status).toHaveTextContent(`${CONNECTED_TEXT} · iPhone · Safari`);
    expect(within(card()).getByRole("status")).toBe(status);
    expect(status).toBeInTheDocument();
  });

  it("refuses the phone on Từ chối and shows a new QR code at once", async () => {
    const { api } = await askingCard();
    await userEvent.click(within(card()).getByRole("button", { name: "Từ chối" }));
    expect(await within(card()).findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toContain("POST /scanner-sessions/ss-1:reject");
    expect(shownQrPath()).toBe(qrPathOf(pairingUrl("CLAIM-2")));
  });

  it("reads the session again when the phone it asks about is already gone", async () => {
    const { api } = await askingCard();
    // The phone gave up meanwhile: the session waits again and approving it is refused.
    api.sessions.set("ss-1", { state: "waiting" });
    await userEvent.click(within(card()).getByRole("button", { name: "Cho phép" }));
    await waitFor(() => expect(api.calls).toContain("GET /scanner-sessions/ss-1"));
    expect(await within(card()).findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
  });

  it("ends the pairing on Ngắt and offers a new QR code", async () => {
    const api = scannerServer({ "ss-7": { state: "claimed", device_label: "Pixel 8" } });
    const { streams, store } = renderCard("ss-7");
    await pairedStream(streams, "Pixel 8");
    expect(within(card()).getByRole("status")).toHaveTextContent(`${CONNECTED_TEXT} · Pixel 8`);
    await userEvent.click(within(card()).getByRole("button", { name: "Ngắt" }));
    expect(await within(card()).findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["GET /scanner-sessions/ss-7", "DELETE /scanner-sessions/ss-7", "POST /scanner-sessions"]);
    expect(store.id).toBe("ss-1");
    expect(streams.all[0]?.closed).toBe(true);
  });

  it("says phone scanning needs the network while offline", async () => {
    act(() => connectivity.simulate(true));
    scannerServer();
    renderCard();
    expect(await within(await screen.findByRole("region", { name: PAIRING_CARD_LABEL })).findByRole("status")).toHaveTextContent(OFFLINE_TEXT);
  });

  it("offers to try again when the session cannot be opened", async () => {
    const api = scannerServer();
    api.onCreate(() => apiError(500, "internal", { message: "boom" }));
    renderCard();
    const retry = await screen.findByRole("button", { name: "Thử lại" });
    expect(within(card()).getByRole("status")).toHaveTextContent(/^Không tạo được mã QR: /);
    api.onCreate(null);
    await userEvent.click(retry);
    expect(await within(card()).findByRole("img", { name: PAIRING_QR_LABEL })).toBeInTheDocument();
    expect(api.calls).toEqual(["POST /scanner-sessions", "POST /scanner-sessions"]);
  });

  it("explains that a browser without event streams cannot pair, and asks nothing", async () => {
    const api = scannerServer();
    renderWithProviders(
      <ScannerProvider openStream={null} store={memorySessionStore()}>
        <PhonePairingCard />
      </ScannerProvider>,
    );
    expect(await screen.findByText(UNSUPPORTED_TEXT)).toBeInTheDocument();
    expect(api.calls).toEqual([]);
  });

  it("renders nothing outside a scanner provider", async () => {
    renderWithProviders(
      <>
        <p>Trang</p>
        <PhonePairingCard />
      </>,
    );
    await screen.findByText("Trang");
    expect(screen.queryByRole("region", { name: PAIRING_CARD_LABEL })).not.toBeInTheDocument();
  });
});
