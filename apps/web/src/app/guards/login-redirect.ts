// Where a signed-out visitor is sent, keeping the page they asked for in `next`.

export const LOGIN_PATH = "/login";
export const ADMIN_LOGIN_PATH = "/admin/login";

export function loginPathFor(location: { pathname: string; search: string; hash: string }, loginPath: string = LOGIN_PATH): string {
  const next = location.pathname + location.search + location.hash;
  if (next === "/" || next === "" || next.startsWith(loginPath)) return loginPath;
  return `${loginPath}?next=${encodeURIComponent(next)}`;
}

/**
 * Validates a `next` parameter before navigating to it: only same-origin absolute paths are
 * followed, so a crafted login link cannot send the owner to another site.
 */
export function safeNextPath(next: string | null | undefined, fallback: string): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return fallback;
  return next;
}
