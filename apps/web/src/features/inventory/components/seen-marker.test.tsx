import { act, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SeenMarker } from "./seen-marker";

/** An IntersectionObserver whose entries the test fires by hand. */
class FakeObserver {
  static last: FakeObserver | null = null;
  disconnected = false;
  constructor(readonly callback: IntersectionObserverCallback) {
    FakeObserver.last = this;
  }
  observe() {}
  disconnect() {
    this.disconnected = true;
  }
  fire(isIntersecting: boolean) {
    this.callback([{ isIntersecting } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
  }
}

describe("SeenMarker", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    FakeObserver.last = null;
  });

  it("reports its row only once the row comes into view", () => {
    vi.stubGlobal("IntersectionObserver", FakeObserver);
    const onSeen = vi.fn();
    render(<SeenMarker id="row-1" onSeen={onSeen} />);
    const io = FakeObserver.last as FakeObserver;

    act(() => io.fire(false));
    expect(onSeen).not.toHaveBeenCalled();
    act(() => io.fire(true));
    expect(onSeen).toHaveBeenCalledExactlyOnceWith("row-1");
    expect(io.disconnected).toBe(true);
  });

  it("reports its row at once where the browser cannot observe it", () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    const onSeen = vi.fn();
    render(<SeenMarker id="row-1" onSeen={onSeen} />);
    expect(onSeen).toHaveBeenCalledWith("row-1");
  });
});
