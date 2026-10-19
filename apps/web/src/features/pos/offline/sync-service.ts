import { z } from "zod";
import { errorMessage, isApiError, isSessionExpired } from "../../../shared/api/api-error";
import type { HttpClient } from "../../../shared/api/http";
import type { Money } from "../../../shared/domain/money";
import type { CheckoutIntent, OutboxEntry, OutboxException } from "./offline-db";
import { syncErrorText } from "./sync-errors";

// One sync pass. First the online checkouts whose answer was lost are sent again, each with its own
// Idempotency-Key and body: the server answers a completed one with its stored result and posts
// nothing new, and runs one that never committed now. Then the outbox goes to POST
// /pos/offline-sync, one sale per request and in the order they were made, each body exactly as
// stored; the server posts a sale once per offline_client_id, so a resend after a lost answer comes
// back "replayed".
//   posted / replayed     → the sale leaves the outbox;
//   exception             → parked by the server for a manager; kept here, marked, until settled;
//   refused for good      → blocked: kept for the cashier, and the sales after it still go;
//   no answer             → the pass stops; the connection is checked and the pass retried later;
//   server error, busy    → the pass stops and is retried with backoff; repeated server failures
//                           of one sale block it, so it cannot hold the queue forever;
//   401                   → the pass stops until the owner signs in again; nothing is dropped.

export const BACKOFF_BASE_MS = 1_000;
export const BACKOFF_MAX_MS = 60_000;
/** A sync request that has not answered by then counts as no answer (half-open connections). */
export const SYNC_TIMEOUT_MS = 15_000;
/** Server failures of one sale after which it is blocked instead of retried. */
export const MAX_SALE_FAILURES = 5;

/** Wait before the n-th retry (0-based): 1 s doubling up to 60 s, with up to 50% random jitter. */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** Math.max(0, attempt));
  return Math.min(BACKOFF_MAX_MS, Math.round(base * (1 + random() * 0.5)));
}

const syncItemSchema = z.object({
  offline_client_id: z.string(),
  status: z.enum(["posted", "replayed", "exception", "failed"]),
  doc_no: z.string().nullish(),
  sync_exception_id: z.string().nullish(),
  error_code: z.string().nullish(),
  error_detail: z.string().nullish(),
});
const syncResultSchema = z.object({ items: z.array(syncItemSchema) });
const checkoutResultSchema = z.object({ sales_document: z.object({ doc_no: z.string() }).loose() }).loose();

const IN_PROGRESS = "idempotency_key_in_progress";
const KEY_REUSED = "idempotency_key_reused";

/**
 * Why a pass ended early:
 *   network    no answer: the connection is checked before the next pass;
 *   server     the API answered with a server error or asked to wait: retried with backoff;
 *   failed     a sale is not posted yet for a reason that clears by itself: retried with backoff;
 *   reauth     the session ended;
 *   read_only  the subscription or role refuses writes.
 */
export type SyncStop = "reauth" | "network" | "server" | "failed" | "read_only";

/** An online checkout without an answer that got one in this pass. */
export interface SettledCheckout {
  key: string;
  /** Posted now, or by the attempt whose answer was lost; false when refused, so nothing posted. */
  posted: boolean;
  docNo: string | null;
  /** What the customer paid at the counter. */
  total: Money;
  /** Vietnamese reason of a refusal. */
  error: string | null;
}

export interface SyncReport {
  posted: number;
  parked: number;
  blocked: number;
  checkouts: SettledCheckout[];
  /** The session's offline window was renewed (or the server said there is nothing to renew). */
  granted: boolean;
  stopped: SyncStop | null;
  /** Vietnamese reason of the stop. */
  error: string | null;
}

export interface SyncStore {
  list(): Promise<OutboxEntry[]>;
  remove(offlineClientId: string): Promise<void>;
  markException(offlineClientId: string, exception: OutboxException): Promise<void>;
  markBlocked(offlineClientId: string, exception: OutboxException): Promise<void>;
  /** Counts one more server failure of the sale; returns the total. */
  recordFailure(offlineClientId: string): Promise<number>;
  listIntents(): Promise<CheckoutIntent[]>;
  forgetIntent(key: string): Promise<void>;
}

export interface SyncOptions {
  /** Renew the offline window before the first sale; once per connection is enough. */
  grant: boolean;
  timeoutMs?: number;
}

type Outcome = SyncStop | "rejected";

/** How a request that threw ended; "rejected" is a definitive refusal of that one request. */
function outcomeOf(e: unknown): Outcome {
  if (!isApiError(e) || e.isNetwork) return "network";
  if (isSessionExpired(e) || e.status === 401) return "reauth";
  if (e.status === 403) return "read_only";
  if (e.status >= 500 || e.status === 408 || e.status === 429) return "server";
  if (e.code === IN_PROGRESS) return "failed";
  return "rejected";
}

const exceptionOf = (e: unknown): OutboxException =>
  isApiError(e) ? { id: null, error_code: e.code, error_detail: e.message } : { id: null, error_code: "", error_detail: errorMessage(e) };

const ignorableGrant = (e: unknown) => isApiError(e) && (e.code === "offline_pos_disabled" || e.status === 409 || e.status === 422);

export async function syncOutbox(client: HttpClient, store: SyncStore, options: SyncOptions = { grant: true }): Promise<SyncReport> {
  const report: SyncReport = { posted: 0, parked: 0, blocked: 0, checkouts: [], granted: false, stopped: null, error: null };
  const timeout = options.timeoutMs ?? SYNC_TIMEOUT_MS;
  const stop = (why: SyncStop, e: unknown) => {
    report.stopped = why;
    report.error = errorMessage(e);
    return report;
  };

  for (const intent of await store.listIntents()) {
    try {
      const res = await client.request("POST", intent.path, {
        body: intent.body,
        idempotencyKey: intent.idempotency_key,
        ifMatch: intent.if_match,
        silent: true,
        signal: AbortSignal.timeout(timeout),
      });
      const parsed = checkoutResultSchema.safeParse(res.data);
      await store.forgetIntent(intent.idempotency_key);
      report.checkouts.push({ key: intent.idempotency_key, posted: true, docNo: parsed.success ? parsed.data.sales_document.doc_no : null, total: intent.total, error: null });
    } catch (e) {
      const outcome = outcomeOf(e);
      if (outcome !== "rejected") return stop(outcome, e);
      // The API refused it: a failed command stores nothing, so the first attempt did not post
      // either. A key already used for another body means a different attempt of the same cart
      // reached the server; the message asks the cashier to check either way.
      await store.forgetIntent(intent.idempotency_key);
      report.checkouts.push({ key: intent.idempotency_key, posted: false, docNo: null, total: intent.total, error: errorMessage(e) });
    }
  }

  const pending = (await store.list()).filter((e) => e.status === "pending");
  if (pending.length === 0) return report;
  if (options.grant) {
    try {
      await client.post("/pos/offline-session", undefined, { silent: true, signal: AbortSignal.timeout(timeout) });
    } catch (e) {
      const outcome = outcomeOf(e);
      // Nothing to renew (offline selling now off, no shift, sales too old): the sales still go
      // and the server decides on each.
      if (!ignorableGrant(e) && outcome !== "rejected") return stop(outcome, e);
    }
    report.granted = true;
  }

  for (const entry of pending) {
    let item: z.infer<typeof syncItemSchema> | undefined;
    try {
      const result = await client.post("/pos/offline-sync", { sales: [entry.sale] }, { schema: syncResultSchema, silent: true, signal: AbortSignal.timeout(timeout) });
      item = result.items.find((i) => i.offline_client_id === entry.offline_client_id);
    } catch (e) {
      const outcome = outcomeOf(e);
      if (outcome === "rejected") {
        // The request itself is refused (the body no longer validates): resending it never helps.
        await store.markBlocked(entry.offline_client_id, exceptionOf(e));
        report.blocked++;
        continue;
      }
      if (outcome === "server" && isApiError(e) && e.status === 500 && (await store.recordFailure(entry.offline_client_id)) >= MAX_SALE_FAILURES) {
        await store.markBlocked(entry.offline_client_id, exceptionOf(e));
        report.blocked++;
        continue;
      }
      return stop(outcome, e);
    }
    if (!item) {
      report.stopped = "server";
      report.error = "Máy chủ không trả kết quả cho giao dịch này.";
      return report;
    }
    if (item.status === "posted" || item.status === "replayed") {
      await store.remove(entry.offline_client_id);
      report.posted++;
      continue;
    }
    const exception: OutboxException = { id: item.sync_exception_id ?? null, error_code: item.error_code ?? "", error_detail: item.error_detail ?? null };
    if (item.status === "exception") {
      await store.markException(entry.offline_client_id, exception);
      report.parked++;
      continue;
    }
    // "failed". Sent again with other contents under the same id: resending never helps. The same
    // sale still posting from another upload keeps its place and is not counted: that upload ends
    // soon and the next pass replays it. Anything else (a shift or goods change while it posted, a
    // server fault) is counted, and blocks the sale once it keeps failing.
    const code = item.error_code ?? "";
    const blocked = code === KEY_REUSED || (code !== IN_PROGRESS && (await store.recordFailure(entry.offline_client_id)) >= MAX_SALE_FAILURES);
    if (blocked) {
      await store.markBlocked(entry.offline_client_id, exception);
      report.blocked++;
      continue;
    }
    report.stopped = "failed";
    report.error = syncErrorText(item.error_code, item.error_detail);
    return report;
  }
  return report;
}
