import { useMatches } from "react-router";

// Menu routes carry their page metadata in the route `handle`, so headers, tabs and visual tests
// read one source (the app menu) instead of repeating titles in every page.

export interface RouteMeta {
  /** Prototype view id, "mod.sub" (e.g. "kho.ton"). */
  viewId: string;
  /** Group label shown as the page eyebrow. */
  eyebrow: string;
  /** Page h1. */
  title: string;
  /** Sibling routes of the same group, for module tabs. */
  tabs: ReadonlyArray<{ to: string; label: string }>;
  groupLabel: string;
}

export function isRouteMeta(value: unknown): value is RouteMeta {
  return typeof value === "object" && value !== null && typeof (value as RouteMeta).viewId === "string" && typeof (value as RouteMeta).title === "string";
}

/** Metadata of the deepest matched menu route, or null outside menu routes. */
export function useRouteMeta(): RouteMeta | null {
  const matches = useMatches();
  for (let i = matches.length - 1; i >= 0; i--) {
    const handle = matches[i]?.handle;
    if (isRouteMeta(handle)) return handle;
  }
  return null;
}

/** Route `handle` of a screen outside the menu (sign-in, onboarding) that only names the page. */
export interface RouteTitle {
  title: string;
}

/** Page name of the deepest matched route that has one (menu screens and named routes alike). */
export function useRouteTitle(): string | undefined {
  const matches = useMatches();
  for (let i = matches.length - 1; i >= 0; i--) {
    const handle = matches[i]?.handle;
    if (typeof handle === "object" && handle !== null && typeof (handle as RouteTitle).title === "string") return (handle as RouteTitle).title;
  }
  return undefined;
}
