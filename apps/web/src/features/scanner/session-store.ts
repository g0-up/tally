import { z } from "zod";

// The phone scanner session of this tab. Only the session id is kept, in sessionStorage, so a
// reload resumes the pairing and a second tab starts its own; the claim code that pairs a phone is
// never stored. When sessionStorage cannot be used (private mode, blocked storage) the session
// lives only as long as the page.

export const SESSION_STORAGE_KEY = "tally.scanner-session";

const storedSchema = z.object({ id: z.string().min(1) });

export interface SessionStore {
  load(): string | null;
  save(id: string): void;
  clear(): void;
}

export function createSessionStore(storage: () => Storage = () => window.sessionStorage): SessionStore {
  return {
    load() {
      try {
        const raw = storage().getItem(SESSION_STORAGE_KEY);
        if (raw === null) return null;
        const parsed = storedSchema.safeParse(JSON.parse(raw));
        return parsed.success ? parsed.data.id : null;
      } catch {
        return null;
      }
    },
    save(id) {
      try {
        storage().setItem(SESSION_STORAGE_KEY, JSON.stringify({ id }));
      } catch {
        // Storage refused: the session stays in memory for this page only.
      }
    },
    clear() {
      try {
        storage().removeItem(SESSION_STORAGE_KEY);
      } catch {
        // Nothing was stored.
      }
    },
  };
}
