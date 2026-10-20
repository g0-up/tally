import { act } from "@testing-library/react";
import type { EventStreamHandlers, OpenEventStream, StreamError } from "../event-stream";

// Event streams for tests (jsdom has no EventSource): each open is recorded, and the test plays
// the server by emitting named events or failing the stream.

export interface FakeStream {
  readonly url: string;
  closed: boolean;
  /** Sends a named event with its JSON data, as the server would. */
  emit(name: string, data?: unknown): void;
  /** Fails the stream: `closed` when the browser gave up, `reconnecting` while it retries. */
  fail(state?: StreamError): void;
}

export interface FakeStreams {
  open: OpenEventStream;
  /** Every stream opened, oldest first. */
  readonly all: FakeStream[];
  /** The stream opened last; throws when none was. */
  last(): FakeStream;
  /** Streams not closed. */
  live(): FakeStream[];
}

export function createFakeStreams(): FakeStreams {
  const all: FakeStream[] = [];
  const open: OpenEventStream = (url: string, handlers: EventStreamHandlers) => {
    const stream: FakeStream = {
      url,
      closed: false,
      emit(name, data = {}) {
        if (stream.closed) throw new Error(`event "${name}" on a closed stream`);
        act(() => handlers.events[name]?.(data));
      },
      fail(state = "closed") {
        if (state === "closed") stream.closed = true;
        act(() => handlers.onError(state));
      },
    };
    all.push(stream);
    return {
      close: () => {
        stream.closed = true;
      },
    };
  };
  return {
    open,
    all,
    last() {
      const s = all.at(-1);
      if (!s) throw new Error("no event stream was opened");
      return s;
    },
    live: () => all.filter((s) => !s.closed),
  };
}
