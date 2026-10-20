import { useEffect, useLayoutEffect, useRef } from "react";
import { useScanner } from "./scanner-context";
import type { ScanHandler } from "./scanner-controller";

/**
 * Makes the calling screen take the codes a paired phone scans. The handler is the screen's Enter
 * logic (without touching the text typed in its scan box) and is read from a ref updated on every
 * render, so a scan always sees the screen's current state.
 *
 * Targets form a stack in the order they registered, and the top one takes the codes: a dialog
 * opened over a page takes them, and the page takes them again once the dialog unmounts. The order
 * is the order of React's effects, which run children before parents, so when a parent and a child
 * that both call this hook mount in the same render the parent ends on top. A screen that nests
 * targets mounts the inner one later (as a dialog does) or passes `null` from the outer one while
 * the inner one is shown. `null` keeps the place but answers the phone that this screen does not
 * take codes right now.
 */
export function useScanTarget(handler: ScanHandler | null): void {
  const scanner = useScanner();
  const ref = useRef<ScanHandler | null>(handler);
  useLayoutEffect(() => {
    ref.current = handler;
  });
  useEffect(() => scanner?.registerTarget(ref), [scanner]);
}
