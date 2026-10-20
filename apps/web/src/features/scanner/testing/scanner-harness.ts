import { waitFor } from "@testing-library/react";
import { expect } from "vitest";
import { createElement, type ReactElement } from "react";
import { ScannerProvider } from "../scanner-provider";
import type { SessionStore } from "../session-store";
import type { FakeStream, FakeStreams } from "./fake-event-stream";

// Test wiring of the phone scanner: an in-memory session store, the provider around a screen with
// fake event streams, and a paired phone to send scans from.

export interface MemorySessionStore extends SessionStore {
  /** The id held now. */
  readonly id: string | null;
}

export function memorySessionStore(initial: string | null = null): MemorySessionStore {
  let id = initial;
  return {
    get id() {
      return id;
    },
    load: () => id,
    save: (next) => {
      id = next;
    },
    clear: () => {
      id = null;
    },
  };
}

/**
 * `ui` inside a ScannerProvider that opens fake streams and keeps its session in `store`; reconnect
 * waits are the exact steps (no spread), so tests can time them.
 */
export function withScanner(ui: ReactElement, streams: FakeStreams, store: SessionStore): ReactElement {
  return createElement(ScannerProvider, { openStream: streams.open, store, random: () => 0.5, children: ui });
}

/**
 * Waits for the provider to open the stream of the saved session, then plays the server's opening
 * `state` event of a session whose phone is approved.
 */
export async function pairedStream(streams: FakeStreams, deviceLabel = "iPhone · Safari"): Promise<FakeStream> {
  await waitFor(() => expect(streams.live()).toHaveLength(1));
  const stream = streams.last();
  stream.emit("state", { state: "claimed", device_label: deviceLabel });
  return stream;
}
