// Server-sent events behind a small seam: the scanner provider opens its stream through an
// `OpenEventStream`, which is EventSource in the browser and a fake in tests (jsdom has none).

/**
 * Why the stream stopped: `reconnecting` while the browser retries by itself (a dropped
 * connection), `closed` once it gave up (a non-200 answer such as 204, 404 or 503) and only the
 * caller can open it again.
 */
export type StreamError = "reconnecting" | "closed";

export interface EventStreamHandlers {
  /** Named events to listen to; each gets its data parsed as JSON (undefined when unreadable). */
  events: Readonly<Record<string, (data: unknown) => void>>;
  onError: (state: StreamError) => void;
}

export interface EventStream {
  close(): void;
}

export type OpenEventStream = (url: string, handlers: EventStreamHandlers) => EventStream;

function parseData(raw: unknown): unknown {
  if (typeof raw !== "string") return undefined;
  try {
    return JSON.parse(raw) as unknown;
  } catch {
    return undefined;
  }
}

/** EventSource on a same-origin URL; the session cookie goes with it. */
export const openEventStream: OpenEventStream = (url, handlers) => {
  const source = new EventSource(url);
  for (const [name, handle] of Object.entries(handlers.events)) {
    source.addEventListener(name, (e) => handle(parseData((e as MessageEvent<unknown>).data)));
  }
  source.onerror = () => handlers.onError(source.readyState === EventSource.CLOSED ? "closed" : "reconnecting");
  return { close: () => source.close() };
};

/** The real stream, or null in a browser (or test DOM) without EventSource. */
export function defaultEventStream(): OpenEventStream | null {
  return typeof EventSource === "function" ? openEventStream : null;
}
