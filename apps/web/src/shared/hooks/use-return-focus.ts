import { useRef } from "react";

/** The page's main region as a focus target, for when the control that had focus went away. */
export function mainFocusTarget(): HTMLElement | null {
  const main = document.getElementById("view") ?? document.querySelector("main");
  if (!(main instanceof HTMLElement)) return null;
  // A landmark is not focusable by default; -1 makes it a script-only focus target.
  if (!main.hasAttribute("tabindex")) main.tabIndex = -1;
  return main;
}

/** Where focus goes when the opener is gone: the caller's choice, else the page's main region. */
function fallbackTarget(fallback: (() => HTMLElement | null | undefined) | undefined): HTMLElement | null {
  const chosen = fallback?.();
  if (chosen?.isConnected) return chosen;
  return mainFocusTarget();
}

/** True when focus was dropped (its control was removed or disabled), so moving it steals nothing. */
export function focusLost(): boolean {
  const active = document.activeElement;
  return active === null || active === document.body || !active.isConnected;
}

/**
 * Radix dialogs hand focus back to their `Dialog.Trigger` on close. Ours are opened from code (a
 * button sets `open`), so there is no trigger and focus would drop to <body>, losing the keyboard
 * user's place. `remember` runs when the dialog takes focus (focus is still on the opener then);
 * `onCloseAutoFocus` puts it back on the opener, or, when the commit removed the opener (a deleted
 * row, a closed document), on `fallback` or the page's main region.
 */
export function useReturnFocus(fallback?: () => HTMLElement | null | undefined): {
  remember: () => void;
  onCloseAutoFocus: (event: Event) => void;
} {
  const opener = useRef<HTMLElement | null>(null);
  return {
    remember: () => {
      const active = document.activeElement;
      opener.current = active instanceof HTMLElement && active !== document.body ? active : null;
    },
    onCloseAutoFocus: (event) => {
      const target = opener.current;
      opener.current = null;
      const next = target?.isConnected ? target : target ? fallbackTarget(fallback) : null;
      if (!next) return;
      event.preventDefault();
      next.focus({ preventScroll: next !== target });
    },
  };
}
