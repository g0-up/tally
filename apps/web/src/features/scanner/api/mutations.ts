import type { HttpClient } from "../../../shared/api/http";
import type { ScanOutcome } from "../../../shared/lib/barcode";
import { sessionPath } from "./queries";
import { claimCodeSchema, scanResultSchema, sessionCreatedSchema, sessionStatusSchema, type ClaimCode, type DesktopOutcome, type SessionCreated, type SessionStatus } from "./schemas";

// Write side of a phone scanner session: open it, issue a new claim code, approve or reject the
// phone that claimed it, end it, and answer a scan. Every call carries the CSRF header (the HTTP
// client adds it to non-GET requests).

/** What a screen did with a scan, or `not_accepting` when no screen takes scans right now. */
export type ScanAnswer = ScanOutcome | { outcome: "not_accepting" };

export function createScannerSession(client: HttpClient): Promise<SessionCreated> {
  return client.post("/scanner-sessions", undefined, { schema: sessionCreatedSchema });
}

/** A new claim code for a session no phone claimed yet; 409 already_claimed otherwise. */
export function refreshClaimCode(client: HttpClient, id: string): Promise<ClaimCode> {
  return client.post(`${sessionPath(id)}:refresh-code`, undefined, { schema: claimCodeSchema });
}

/** Lets the pending phone scan; 409 not_pending when no phone waits. */
export function approveScannerSession(client: HttpClient, id: string): Promise<SessionStatus> {
  return client.post(`${sessionPath(id)}:approve`, undefined, { schema: sessionStatusSchema });
}

/** Refuses the pending phone and returns the new claim code; 409 not_pending when no phone waits. */
export function rejectScannerSession(client: HttpClient, id: string): Promise<ClaimCode> {
  return client.post(`${sessionPath(id)}:reject`, undefined, { schema: claimCodeSchema });
}

/** Ends the session; ending an ended session succeeds. */
export async function revokeScannerSession(client: HttpClient, id: string): Promise<void> {
  await client.delete(sessionPath(id));
}

function resultBody(answer: ScanAnswer): { outcome: DesktopOutcome; sku_id?: string; unit_id?: string } {
  return answer.outcome === "added" ? { outcome: "added", sku_id: answer.skuId, unit_id: answer.unitId } : { outcome: answer.outcome };
}

/** Tells the phone what the screen did with its scan; 409 scan_expired once the scan outlived its time. */
export async function resolveScan(client: HttpClient, id: string, scanId: string, answer: ScanAnswer): Promise<void> {
  await client.post(`${sessionPath(id)}/scans/${encodeURIComponent(scanId)}/result`, resultBody(answer), { schema: scanResultSchema });
}
