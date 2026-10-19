// CSRF uses the double-submit pattern: the API sets a readable CSRF cookie next to the HttpOnly
// session cookie and rejects state-changing requests whose X-CSRF-Token header does not match it.
// The token is kept in memory (from the session response when the API includes it) and otherwise
// read from the cookie. Nothing is written to localStorage.

export interface CsrfSource {
  /** Current token, or null when the browser has no session yet. */
  get(): string | null;
  /** Stores a token returned by the API (session or login response). */
  set(token: string | null): void;
}

/** Cookie names per host kind: production uses __Host- cookies, dev on plain http does not. */
export const CSRF_COOKIES = {
  tenant: ["__Host-tally_csrf", "tally_csrf"],
  admin: ["__Host-tally_admin_csrf", "tally_admin_csrf"],
} as const;

export function readCookie(name: string, cookieString: string = typeof document === "undefined" ? "" : document.cookie): string | null {
  for (const part of cookieString.split(";")) {
    const i = part.indexOf("=");
    if (i < 0) continue;
    if (part.slice(0, i).trim() === name) {
      const raw = part.slice(i + 1).trim();
      try {
        return decodeURIComponent(raw);
      } catch {
        return raw;
      }
    }
  }
  return null;
}

export function createCsrfSource(cookieNames: readonly string[]): CsrfSource {
  let memory: string | null = null;
  return {
    get() {
      if (memory) return memory;
      for (const name of cookieNames) {
        const v = readCookie(name);
        if (v) return v;
      }
      return null;
    },
    set(token) {
      memory = token && token.trim() !== "" ? token : null;
    },
  };
}
