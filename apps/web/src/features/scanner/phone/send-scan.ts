import { isApiError } from "../../../shared/api/api-error";
import type { HttpClient } from "../../../shared/api/http";
import { phoneScanResultSchema, type PhoneScanResult } from "./outcome-copy";

// Sends one detected code to the desktop and waits for what it did. Each detection gets its own
// Idempotency-Key, made when the code is read and kept for every retry, so a request whose answer
// was lost can be sent again without the desktop applying the code twice: the server answers a
// stored result unchanged and asks the desktop again only for `desktop_unavailable` and `timeout`.
// Those two come back as outcomes and are never retried here: the desktop may have applied the code
// after all, so the person looks at the desktop before scanning again.

/** Waits before the second, third and fourth attempt when no answer came back. */
export const SEND_RETRY_DELAYS_MS = [500, 1_000, 2_000] as const;

/** One detection on its way to the desktop. */
export interface PendingScan {
  code: string;
  key: string;
}

export function newScan(code: string): PendingScan {
  return { code, key: crypto.randomUUID() };
}

/** No answer said what happened: the network failed or a server or proxy error stood in for one. */
function unanswered(e: unknown): boolean {
  return isApiError(e) && (e.isNetwork || e.status >= 500);
}

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

export interface SendScanOptions {
  sleep?: (ms: number) => Promise<void>;
}

/**
 * The desktop's answer to `scan`. Unanswered attempts are retried under the same key after 0.5, 1
 * and 2 s; any refusal (401 scan_session_ended, 409 scan_pending_approval, 422, 429…) and the last
 * unanswered attempt are thrown for the caller to report.
 */
export async function sendScan(client: HttpClient, scan: PendingScan, { sleep = wait }: SendScanOptions = {}): Promise<PhoneScanResult> {
  for (let attempt = 0; ; attempt++) {
    try {
      return await client.post("/scan/scans", { code: scan.code }, { idempotencyKey: scan.key, schema: phoneScanResultSchema, silent: true });
    } catch (e) {
      const delay = SEND_RETRY_DELAYS_MS[attempt];
      if (delay === undefined || !unanswered(e)) throw e;
      await sleep(delay);
    }
  }
}
