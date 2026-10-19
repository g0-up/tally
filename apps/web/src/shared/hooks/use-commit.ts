import { useContext, useRef, type ReactNode } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { ERROR_CODES, errorMessage, fieldReasonMessage, isApiError, isExceptionRequired, isReauthRequired, isSessionExpired, type ApiError } from "../api/api-error";
import { toast } from "../ui/toast-store";
import { ConfirmExceptionContext, type ExceptionPrompt } from "./use-confirm-exception";
import { useIdempotentMutation } from "./use-idempotent-mutation";
import { RecentAuthContext, type ReauthCopy } from "./use-recent-auth";

// The one write contract of the app (prototype `post(btn, fn, okMsg, after)`):
//   - the submit button shows "Đang ghi sổ…" and is disabled while the request is in flight;
//   - one Idempotency-Key per intent, reused by every retry of that intent;
//   - success is announced only after the server answered 2xx, with the server's document number;
//   - a refused write shows a persistent "<b>Chưa ghi sổ.</b> {message}" toast and keeps the dialog;
//   - a write that stored its document but not every follow-up step says so (PartialCommitError);
//   - `reauth_required` asks for the password and retries the same intent once;
//   - 409 + exception_kind asks the owner to confirm and retries with exception_confirmation_id;
//   - field reason codes are mapped onto the form.
// Session expiry and stale data (412) are handled by the app-wide error handler, so no toast here.

export interface CommitContext {
  idempotencyKey: string;
  /** Set on the retry after the owner confirmed an exception; add it to the request body. */
  exceptionConfirmationId: string | null;
}

export interface CommitOptions<TData, TVariables> {
  mutationFn: (variables: TVariables, context: CommitContext) => Promise<TData>;
  /** Success toast built from the server response (document number in bold). Omit for none. */
  success?: (data: TData, variables: TVariables) => ReactNode;
  /** Query-key prefixes refreshed after a 2xx. */
  invalidate?: readonly QueryKey[] | ((data: TData, variables: TVariables) => readonly QueryKey[]);
  /** Runs after a 2xx and the success toast (close the dialog, navigate, reset the form). */
  after?: (data: TData, variables: TVariables) => void;
  /**
   * Receives every `fields` entry of a refused write, already in Vietnamese, with the API's reason
   * code for a screen that words a field's limit itself.
   */
  setFieldError?: (field: string, message: string, reason: string) => void;
  /** Bold lead of the refusal toast when the write posts nothing to the books; "Chưa ghi sổ." otherwise. */
  failedLead?: string;
  /** Wording of the password prompt when the API asks for it; the shared wording otherwise. */
  reauth?: ReauthCopy;
  /** Describes the exception to confirm for a 409 + exception_kind answer; null shows the error instead. */
  exception?: (error: ApiError & { exceptionKind: string }, variables: TVariables) => ExceptionPrompt | null;
}

export interface CommitResult<TData, TVariables> {
  /** Sends the intent; resolves with the response, or undefined when it did not go through. */
  run: (variables: TVariables) => Promise<TData | undefined>;
  pending: boolean;
  error: unknown;
  /** Starts a new intent (new Idempotency-Key), e.g. when a dialog is opened again. */
  resetKey: () => void;
}

/** Thrown when the owner cancels the re-authentication or exception prompt. */
export class CommitCancelledError extends Error {
  constructor() {
    super("Commit cancelled");
    this.name = "CommitCancelledError";
  }
}

/**
 * A write that stored its main document but not every step after it (confirming, landed cost,
 * supplier invoice, prices). Its toast names the stored document and what is still missing instead
 * of "Chưa ghi sổ.", and the dialog stays open so the next click continues the same intent.
 */
export class PartialCommitError extends Error {
  constructor(readonly notice: ReactNode) {
    super("Partially committed");
    this.name = "PartialCommitError";
  }
}

export interface FollowUpStep {
  id: string;
  /** What is missing when it fails, e.g. "chốt đơn". */
  label: string;
  run: () => Promise<unknown>;
}

/**
 * Runs the steps that follow a stored document, skipping those an earlier attempt of the same
 * intent finished (`done`), so a retry neither repeats nor loses them. A failing step stops the
 * run with a PartialCommitError built by `stored`; a password prompt or an expired session still
 * reaches its usual handler.
 */
export async function runFollowUps(
  steps: readonly FollowUpStep[],
  done: Set<string>,
  stored: (missing: string, reason: string) => ReactNode,
  translate: (e: unknown) => unknown = (e) => e,
): Promise<void> {
  for (const step of steps) {
    if (done.has(step.id)) continue;
    try {
      await step.run();
    } catch (e) {
      if (isReauthRequired(e) || isSessionExpired(e)) throw e;
      throw new PartialCommitError(stored(step.label, errorMessage(translate(e))));
    }
    done.add(step.id);
  }
}

/** Errors that must not produce the "Chưa ghi sổ." toast because another layer reacts to them. */
function handledElsewhere(e: unknown): boolean {
  if (e instanceof CommitCancelledError) return true;
  if (isSessionExpired(e)) return true;
  return isApiError(e) && (e.status === 412 || e.code === ERROR_CODES.preconditionRequired);
}

export function useCommit<TData, TVariables = void>(options: CommitOptions<TData, TVariables>): CommitResult<TData, TVariables> {
  const queryClient = useQueryClient();
  const requestReauth = useContext(RecentAuthContext);
  const confirmException = useContext(ConfirmExceptionContext);

  const mutation = useIdempotentMutation<TData, unknown, TVariables>({
    mutationFn: async (variables, { idempotencyKey }) => {
      let exceptionConfirmationId: string | null = null;
      let reauthenticated = false;
      for (;;) {
        try {
          return await options.mutationFn(variables, { idempotencyKey, exceptionConfirmationId });
        } catch (e) {
          if (isReauthRequired(e) && !reauthenticated && requestReauth) {
            if (!(await requestReauth(options.reauth))) throw new CommitCancelledError();
            reauthenticated = true;
            continue;
          }
          if (isExceptionRequired(e) && exceptionConfirmationId === null && confirmException && options.exception) {
            const prompt = options.exception(e, variables);
            if (prompt) {
              const id = await confirmException(prompt);
              if (id === null) throw new CommitCancelledError();
              exceptionConfirmationId = id;
              continue;
            }
          }
          throw e;
        }
      }
    },
    onSuccess: async (data, variables) => {
      const message = options.success?.(data, variables);
      if (message !== undefined && message !== null) toast.success(message);
      const keys = typeof options.invalidate === "function" ? options.invalidate(data, variables) : (options.invalidate ?? []);
      await Promise.all(keys.map((queryKey) => queryClient.invalidateQueries({ queryKey })));
      options.after?.(data, variables);
    },
    onError: async (e) => {
      if (e instanceof PartialCommitError) {
        toast.error(e.notice);
        // The document is stored: show it, unless the keys depend on a response there is none of.
        if (options.invalidate && typeof options.invalidate !== "function") await Promise.all(options.invalidate.map((queryKey) => queryClient.invalidateQueries({ queryKey })));
        return;
      }
      if (isApiError(e) && options.setFieldError) {
        for (const [field, reason] of Object.entries(e.fields)) options.setFieldError(field, fieldReasonMessage(reason), reason);
      }
      if (!handledElsewhere(e)) toast.commitError(errorMessage(e), options.failedLead);
    },
  });

  // A second click that lands before the button re-renders disabled must not send twice.
  const inFlight = useRef(false);
  const { mutateAsync, isPending, error, resetKey } = mutation;
  return {
    run: async (variables) => {
      if (inFlight.current) return undefined;
      inFlight.current = true;
      try {
        return await mutateAsync(variables);
      } catch {
        // Already reported by onError (toast, field errors) or by the app-wide handler.
        return undefined;
      } finally {
        inFlight.current = false;
      }
    },
    pending: isPending,
    error,
    resetKey,
  };
}
