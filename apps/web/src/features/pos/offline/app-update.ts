import { useSyncExternalStore } from "react";

// A new build is deployed while the app is open: the new service worker waits until the user
// reloads ("Đã có phiên bản mới. Tải lại để cập nhật."), so an open POS never changes under the
// cashier's hands. main.tsx reports the waiting worker here; the prompt reads it.

export const UPDATE_MESSAGE = "Đã có phiên bản mới. Tải lại để cập nhật.";

type Apply = () => Promise<void>;

let pending: Apply | null = null;
const listeners = new Set<() => void>();

function emit() {
  for (const l of [...listeners]) l();
}

/** A new version waits; `apply` activates it and reloads the page. */
export function announceUpdate(apply: Apply): void {
  pending = apply;
  emit();
}

/** "Để sau": the prompt leaves until the next new version or reload. */
export function dismissUpdate(): void {
  pending = null;
  emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The waiting update, or null. */
export function useAppUpdate(): Apply | null {
  return useSyncExternalStore(
    subscribe,
    () => pending,
    () => pending,
  );
}
