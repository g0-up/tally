import { buildUrl, type HttpClient } from "../../../shared/api/http";
import { sessionStatusSchema, type SessionStatus } from "./schemas";

// Read side of a phone scanner session. The pairing card follows the session through its event
// stream; GET is asked before (re)opening it and after a refused command, so it is a plain call
// rather than a cached query.

/** Path of one session; ids are server-issued but still encoded. */
export const sessionPath = (id: string) => `/scanner-sessions/${encodeURIComponent(id)}`;

/** The session's state; 404 when it belongs to another user or login session. */
export function getScannerSession(client: HttpClient, id: string): Promise<SessionStatus> {
  return client.get(sessionPath(id), { schema: sessionStatusSchema });
}

/** URL of the session's event stream (same origin, so the session cookie goes with it). */
export function scannerEventsUrl(client: HttpClient, id: string): string {
  return buildUrl(client.baseUrl, `${sessionPath(id)}/events`);
}
