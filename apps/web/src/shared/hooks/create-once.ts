import { useCallback, useState } from "react";
import { ERROR_CODES, isApiError, isReauthRequired } from "../api/api-error";
import { canonicalJson } from "../lib/canonical-hash";
import { toDecimalString } from "../lib/decimal";
import { PartialCommitError } from "./use-commit";

// The create of a multi-request intent, sent once under the intent's Idempotency-Key. The server
// stores a created document under its key and refuses that key with another body, while a refusal
// leaves nothing behind; so a body whose answer was lost is kept and sent again first:
//   - no answer (network, 5xx, unreadable 2xx, `idempotency_key_in_progress`): the body is kept and
//     the next attempt resends it under the same key, so the server answers what it stored or
//     creates it now;
//   - refused (any other 4xx): nothing is stored, the body is dropped and the form's goes out;
//     `reauth_required` keeps it (the retry after the password resends it), an expired session
//     drops it (the next session may be another user's);
//   - created from a kept body the form has moved on from: `update` writes the edits onto the
//     document the replay answered (its version), or the caller says they were not saved.
// The state lives in memory only, one per intent key; dropping the intent drops it. Signing out,
// an expired session and signing in as someone else all pass through /login, which unmounts the
// dialog holding it. Closing the dialog while a body is kept loses the check: ask first.

/** The toast of a create whose answer was lost: the next click of the same dialog checks it. */
export const OUTCOME_UNKNOWN_MESSAGE = "Chưa rõ đã ghi sổ hay chưa. Bấm lại để kiểm tra; không tạo chứng từ mới.";

/** Asked before closing a dialog whose create may have been stored without an answer. */
export const CLOSE_UNSETTLED_MESSAGE = "Có thể chứng từ đã được tạo. Đóng sẽ không kiểm tra lại được — vẫn đóng?";

/**
 * A create sent without an answer: it may be stored. Not "Chưa ghi sổ.", since that may be false;
 * the message is the toast text, so a follow-up step quoting it says the same.
 */
export class OutcomeUnknownError extends PartialCommitError {
  constructor(cause: unknown) {
    super(OUTCOME_UNKNOWN_MESSAGE);
    this.name = "OutcomeUnknownError";
    this.message = OUTCOME_UNKNOWN_MESSAGE;
    this.cause = cause;
  }
}

/** The create of one intent: the body that went out (or the document's last one) and the answer. */
export interface CreateState<B, T> {
  sent: B | null;
  result: T | null;
}

export function newCreateState<B, T>(): CreateState<B, T> {
  return { sent: null, result: null };
}

/** A body went out and no answer said whether it was stored. */
export function unsettled(state: CreateState<unknown, unknown> | null | undefined): boolean {
  return state != null && state.sent !== null && state.result === null;
}

/**
 * Whether a dialog's create may be stored without an answer, for its close question
 * (CLOSE_UNSETTLED_MESSAGE). `watch` runs a create of `state` and re-renders with what it left.
 */
export function useUnsettled() {
  const [value, setValue] = useState(false);
  const watch = useCallback(async <R>(state: CreateState<unknown, unknown>, run: () => Promise<R>): Promise<R> => {
    try {
      return await run();
    } finally {
      setValue(unsettled(state));
    }
  }, []);
  return { unsettled: value, watch };
}

/** No answer said whether the server stored the request, so it may have. */
export function outcomeUnknown(e: unknown): boolean {
  if (!isApiError(e)) return false;
  // A 2xx whose body could not be read was stored all the same.
  const unreadable = e.code === ERROR_CODES.badResponse && e.status >= 200 && e.status < 300;
  return e.isNetwork || e.status >= 500 || unreadable || e.code === "idempotency_key_in_progress";
}

/** The server answered that it did not store the request. */
function refused(e: unknown): boolean {
  return isApiError(e) && !outcomeUnknown(e) && !isReauthRequired(e);
}

/** Sends one body; a lost answer comes out as OutcomeUnknownError. */
async function sendOnce<B, T>(send: (body: B) => Promise<T>, body: B): Promise<T> {
  try {
    return await send(body);
  } catch (e) {
    throw outcomeUnknown(e) ? new OutcomeUnknownError(e) : e;
  }
}

const FRACTIONAL = /^-?[0-9]+\.[0-9]+$/;

/**
 * Writes fractional decimal strings the one way ("18000.0000" as "18000", "1.50" as "1.5"), so an
 * amount the API sent and the same amount typed again compare equal. Strings without a decimal
 * point stay as they are: "001" is a code, not the number 1.
 */
function trimDecimals(v: unknown): unknown {
  if (typeof v === "string") return FRACTIONAL.test(v) ? toDecimalString(v) : v;
  if (Array.isArray(v)) return v.map(trimDecimals);
  if (v !== null && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, trimDecimals(x)]));
  return v;
}

/** A request body as attempts compare it: canonical JSON of the trimmed decimals, so neither key order nor "18000.00" counts. */
export function requestJson(body: unknown): string {
  return canonicalJson(trimDecimals(body));
}

const same = (a: unknown, b: unknown) => requestJson(a) === requestJson(b);

export interface CreateOnceOptions<B, T> {
  /** Sends one create body under the intent's key. */
  send: (body: B) => Promise<T>;
  /**
   * Writes the form's body onto the document an earlier body created, from the version that answer
   * carried. Without it the caller gets `edited` and says the edits were not saved.
   */
  update?: (created: T, body: B) => Promise<T>;
}

export interface Created<T> {
  result: T;
  /** The document holds an earlier body than the form's and no `update` wrote the difference. */
  edited: boolean;
}

export async function createOnce<B, T>(state: CreateState<B, T>, body: B, { send, update }: CreateOnceOptions<B, T>): Promise<Created<T>> {
  if (state.result === null) {
    const kept = state.sent;
    let created = false;
    if (kept !== null && !same(kept, body)) {
      try {
        state.result = await sendOnce(send, kept);
        created = true;
      } catch (e) {
        if (!refused(e)) throw e;
        state.sent = null;
      }
    }
    if (!created) {
      // A kept body equal to the form's goes out as it was: the server binds the key to its strings.
      const out = state.sent ?? body;
      state.sent = out;
      try {
        state.result = await sendOnce(send, out);
      } catch (e) {
        if (refused(e)) state.sent = null;
        throw e;
      }
    }
  }
  const result = state.result as T;
  if (state.sent === null || same(state.sent, body)) return { result, edited: false };
  if (!update) return { result, edited: true };
  // A failed update keeps the created document and its body, so the next attempt updates again.
  state.result = await update(result, body);
  state.sent = body;
  return { result: state.result, edited: false };
}
