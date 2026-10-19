import { useSyncExternalStore } from "react";

// The export each report screen last started, kept outside the page so leaving the screen and coming
// back (or reloading the tab) picks the same export up again. Only the export id is kept: the
// download path is read fresh from the server every time. A tab that holds nothing for a screen
// (new tab, storage cleared) asks the server's list of exports instead.

export interface StoredExport {
  id: string;
  /** Round of state reads; "Kiểm tra lại" starts the next one. Kept in memory only. */
  round: number;
}

const STORAGE_KEY = "tally.report-exports";

const listeners = new Set<() => void>();
let exports: ReadonlyMap<string, StoredExport> | null = null;
/** Scopes whose export this page started, cleared or picked up; the server's list is not asked for them. */
const settled = new Set<string>();

/** Ids saved in this tab; anything unreadable or of the wrong shape is dropped. */
function readStorage(): Map<string, StoredExport> {
  const out = new Map<string, StoredExport>();
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY);
    const parsed: unknown = raw === null ? null : JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return out;
    for (const [scope, id] of Object.entries(parsed)) {
      if (typeof id === "string" && id !== "") out.set(scope, { id, round: 0 });
    }
  } catch {
    // Storage blocked or holding something else: start with no export.
  }
  return out;
}

function writeStorage(map: ReadonlyMap<string, StoredExport>): void {
  try {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries([...map].map(([scope, e]) => [scope, e.id]))));
  } catch {
    // Storage full or blocked: the export is still followed until the tab closes.
  }
}

function current(): ReadonlyMap<string, StoredExport> {
  exports ??= readStorage();
  return exports;
}

function update(scope: string, next: StoredExport | null): void {
  const map = new Map(current());
  if (next === null) map.delete(scope);
  else map.set(scope, next);
  exports = map;
  settled.add(scope);
  writeStorage(map);
  listeners.forEach((l) => l());
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};

/** The export of `scope` being followed, or null. */
export const storedExport = (scope: string): StoredExport | null => current().get(scope) ?? null;

/** The export of `scope` being followed, or null; re-renders when it changes. */
export function useStoredExport(scope: string): StoredExport | null {
  return useSyncExternalStore(subscribe, () => storedExport(scope));
}

/** The tab knows nothing of `scope`'s export: none saved, and none started, cleared or picked up since the page loaded. */
export const unknownExport = (scope: string): boolean => !current().has(scope) && !settled.has(scope);

export const setStoredExport = (scope: string, value: StoredExport) => update(scope, value);

export const clearStoredExport = (scope: string) => update(scope, null);

/** Forgets every export, in memory and in the tab's storage. */
export function resetStoredExports(): void {
  exports = new Map();
  settled.clear();
  try {
    window.sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    // Nothing to forget.
  }
  listeners.forEach((l) => l());
}
