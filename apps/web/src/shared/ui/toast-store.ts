import { Fragment, createElement, type ReactNode } from "react";

// Minimal toast store matching the prototype `toast(msg, kind)`: success toasts leave after 4.5 s,
// error toasts stay until the user dismisses them so the message can be read in full.

export type ToastKind = "ok" | "err";

export interface ToastItem {
  id: number;
  kind: ToastKind;
  message: ReactNode;
}

type Listener = (items: readonly ToastItem[]) => void;

export const TOAST_DURATION_MS = 4500;
const MAX_TOASTS = 5;

let items: readonly ToastItem[] = [];
let nextId = 1;
const listeners = new Set<Listener>();
const timers = new Map<number, ReturnType<typeof setTimeout>>();

function emit() {
  for (const l of listeners) l(items);
}

export function dismissToast(id: number): void {
  const t = timers.get(id);
  if (t) clearTimeout(t);
  timers.delete(id);
  if (!items.some((x) => x.id === id)) return;
  items = items.filter((x) => x.id !== id);
  emit();
}

export function showToast(message: ReactNode, kind: ToastKind = "ok"): number {
  const id = nextId++;
  items = [...items, { id, kind, message }];
  while (items.length > MAX_TOASTS) {
    const oldest = items[0];
    if (!oldest) break;
    dismissToast(oldest.id);
  }
  if (kind === "ok") timers.set(id, setTimeout(() => dismissToast(id), TOAST_DURATION_MS));
  emit();
  return id;
}

/** Bold lead of every failed write, as in the prototype `post()` pipeline. */
export const COMMIT_FAILED = "Chưa ghi sổ.";

/** "<b>Chưa ghi sổ.</b> {message}", or another bold lead for a write that posts nothing. */
export function commitErrorMessage(message: string, lead: string = COMMIT_FAILED): ReactNode {
  return createElement(Fragment, null, createElement("b", null, lead), " ", message);
}

export const toast = {
  success: (message: ReactNode) => showToast(message, "ok"),
  error: (message: ReactNode) => showToast(message, "err"),
  /** Persistent error toast for a write the server did not accept. */
  commitError: (message: string, lead?: string) => showToast(commitErrorMessage(message, lead), "err"),
};

export function subscribeToasts(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getToasts(): readonly ToastItem[] {
  return items;
}

/** Test helper: drops every toast and timer. */
export function resetToasts(): void {
  for (const t of timers.values()) clearTimeout(t);
  timers.clear();
  items = [];
  emit();
}
