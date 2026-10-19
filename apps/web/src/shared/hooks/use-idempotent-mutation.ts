import { useMutation, type UseMutationOptions, type UseMutationResult } from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import { randomId } from "../lib/canonical-hash";

// One Idempotency-Key per user intent. The key is created lazily when the intent first sends,
// reused by every retry of that intent (network failure, re-authentication, exception
// confirmation), and dropped only after the server answers 2xx, so the next intent gets a fresh
// key. The API stores only 2xx replies, so retrying a failed key is safe.

export interface IdempotencyContext {
  idempotencyKey: string;
}

export type IdempotentMutationOptions<TData, TError, TVariables> = Omit<UseMutationOptions<TData, TError, TVariables>, "mutationFn"> & {
  mutationFn: (variables: TVariables, context: IdempotencyContext) => Promise<TData>;
};

export type IdempotentMutationResult<TData, TError, TVariables> = UseMutationResult<TData, TError, TVariables> & {
  /** Key of the current intent (created on first use). */
  currentKey: () => string;
  /** Starts a new intent explicitly, e.g. when the user reopens a dialog. */
  resetKey: () => void;
};

export function newIdempotencyKey(): string {
  return randomId();
}

export function useIdempotentMutation<TData, TError = Error, TVariables = void>(
  options: IdempotentMutationOptions<TData, TError, TVariables>,
): IdempotentMutationResult<TData, TError, TVariables> {
  const keyRef = useRef<string | null>(null);
  const currentKey = useCallback(() => {
    keyRef.current ??= newIdempotencyKey();
    return keyRef.current;
  }, []);
  const resetKey = useCallback(() => {
    keyRef.current = null;
  }, []);

  const { mutationFn, onSuccess, ...rest } = options;
  const mutation = useMutation<TData, TError, TVariables>({
    ...rest,
    mutationFn: (variables) => mutationFn(variables, { idempotencyKey: currentKey() }),
    onSuccess: (...args) => {
      resetKey();
      return onSuccess?.(...args);
    },
  });
  return { ...mutation, currentKey, resetKey };
}
