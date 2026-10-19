import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, vi } from "vitest";
import { resetHotkeys } from "../shared/hooks/use-hotkeys";
import { resetToasts } from "../shared/ui/toast-store";
import { server } from "./msw/server";

// jsdom lacks a few browser APIs Radix and the shell use; minimal stand-ins keep them working.
// Node-environment test files (lint rules, token checks) have no DOM and skip this.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

function installDomStubs(): void {
  if (!("ResizeObserver" in globalThis)) {
    Object.defineProperty(globalThis, "ResizeObserver", { value: ResizeObserverStub, writable: true, configurable: true });
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = function scrollIntoView() {};
  if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false;
  if (!Element.prototype.releasePointerCapture) Element.prototype.releasePointerCapture = function releasePointerCapture() {};
  if (!window.matchMedia) {
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      configurable: true,
      value: (query: string): MediaQueryList => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  }
  // jsdom implements scrollTo as a "not implemented" error; the shell scrolls to top on navigation.
  window.scrollTo = vi.fn() as unknown as typeof window.scrollTo;
}

// The first render of a routed page loads its lazy chunks; on a busy machine that alone can pass
// the 1 s default of findBy/waitFor, so wait longer for what does arrive (assertions are unchanged).
configure({ asyncUtilTimeout: 5000 });

const hasDom = typeof window !== "undefined" && typeof Element !== "undefined";
if (hasDom) installDomStubs();

beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  if (hasDom) cleanup();
  server.resetHandlers();
  resetHotkeys();
  resetToasts();
});
afterAll(() => server.close());
