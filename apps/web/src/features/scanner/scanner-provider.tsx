import { useEffect, useState, type ReactNode } from "react";
import { useApiClient } from "../../shared/api/api-context";
import { useConnectivity } from "../pos";
import { defaultEventStream, type OpenEventStream } from "./event-stream";
import { ScannerContext } from "./scanner-context";
import { createScannerController } from "./scanner-controller";
import { createSessionStore, type SessionStore } from "./session-store";

export interface ScannerProviderProps {
  children: ReactNode;
  /** How the event stream is opened; EventSource by default, null when the browser has none. */
  openStream?: OpenEventStream | null;
  /** Where the tab's session id is kept; sessionStorage by default. */
  store?: SessionStore;
  /** Spreads the reconnect waits; Math.random by default (tests pin it). */
  random?: () => number;
}

/**
 * The tab's phone scanner session, shared by every screen of the store app: pairing cards show it,
 * screens that take scans register with `useScanTarget`. It does nothing until a card is shown or
 * the tab already holds a session, and pauses while the browser has no network.
 */
export function ScannerProvider({ children, openStream, store, random }: ScannerProviderProps) {
  const client = useApiClient();
  const offline = useConnectivity().state === "offline";
  const [controller] = useState(() =>
    createScannerController({
      client,
      openStream: openStream === undefined ? defaultEventStream() : openStream,
      store: store ?? createSessionStore(),
      ...(random ? { random } : {}),
    }),
  );

  // Declared before start, so a page that loads offline never opens a session first.
  useEffect(() => {
    controller.setOffline(offline);
  }, [controller, offline]);
  useEffect(() => {
    controller.start();
    return () => controller.stop();
  }, [controller]);

  return <ScannerContext.Provider value={controller}>{children}</ScannerContext.Provider>;
}
