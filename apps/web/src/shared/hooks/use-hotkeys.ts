import { useEffect, useRef } from "react";

// One keydown listener for the whole app (installed by HotkeysProvider).
//
// Escape closes exactly one layer: the most recently opened one. Modals open after drawers and
// the drawer after the mobile sidebar, so the prototype order modal → drawer → sidebar falls out
// of the stack. Layers that cover the page ("blocking": modal, drawer, popover) also suspend the
// screen hotkeys, like the POS keys that only work when no modal is open.
//
// Screen hotkeys (F2/F4/F9 on POS) are registered by the page that owns them and disappear when
// the page unmounts, so they are scoped to the route.

export interface EscapeLayerOptions {
  /** Blocking layers cover the page and suspend screen hotkeys. Default true. */
  blocking?: boolean;
}

interface Layer {
  id: number;
  blocking: boolean;
  onEscape: () => void;
}

interface Binding {
  id: number;
  key: string;
  handler: (event: KeyboardEvent) => void;
}

let seq = 0;
const layers: Layer[] = [];
const bindings: Binding[] = [];

export function pushEscapeLayer(onEscape: () => void, options: EscapeLayerOptions = {}): () => void {
  const layer: Layer = { id: ++seq, blocking: options.blocking ?? true, onEscape };
  layers.push(layer);
  return () => {
    const i = layers.findIndex((l) => l.id === layer.id);
    if (i >= 0) layers.splice(i, 1);
  };
}

export function hasBlockingLayer(): boolean {
  return layers.some((l) => l.blocking);
}

function normalizeKey(key: string): string {
  return key.length === 1 ? key.toLowerCase() : key;
}

export function bindHotkey(key: string, handler: (event: KeyboardEvent) => void): () => void {
  const binding: Binding = { id: ++seq, key: normalizeKey(key), handler };
  bindings.push(binding);
  return () => {
    const i = bindings.findIndex((b) => b.id === binding.id);
    if (i >= 0) bindings.splice(i, 1);
  };
}

export function handleKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape") {
    const top = layers[layers.length - 1];
    if (!top) return;
    event.preventDefault();
    top.onEscape();
    return;
  }
  if (hasBlockingLayer() || event.isComposing) return;
  const key = normalizeKey(event.key);
  for (let i = bindings.length - 1; i >= 0; i--) {
    const b = bindings[i];
    if (b && b.key === key) {
      event.preventDefault();
      b.handler(event);
      return;
    }
  }
}

/** Installs the global listener; returns the uninstaller. */
export function installHotkeys(target: Document = document): () => void {
  target.addEventListener("keydown", handleKeydown);
  return () => target.removeEventListener("keydown", handleKeydown);
}

/** Test helper. */
export function resetHotkeys(): void {
  layers.length = 0;
  bindings.length = 0;
}

/** Registers an Escape layer while `open` is true. The latest handler is always used. */
export function useEscapeLayer(open: boolean, onEscape: () => void, options: EscapeLayerOptions = {}): void {
  const ref = useRef(onEscape);
  useEffect(() => {
    ref.current = onEscape;
  });
  const blocking = options.blocking ?? true;
  useEffect(() => {
    if (!open) return;
    return pushEscapeLayer(() => ref.current(), { blocking });
  }, [open, blocking]);
}

/** Binds screen hotkeys (e.g. { F2: focusScan, F9: pay }) for the lifetime of the component. */
export function useHotkeys(map: Readonly<Record<string, (event: KeyboardEvent) => void>>, enabled = true): void {
  const ref = useRef(map);
  useEffect(() => {
    ref.current = map;
  });
  const keys = Object.keys(map).sort().join("|");
  useEffect(() => {
    if (!enabled || keys === "") return;
    const offs = keys.split("|").map((k) => bindHotkey(k, (e) => ref.current[k]?.(e)));
    return () => offs.forEach((off) => off());
  }, [keys, enabled]);
}
