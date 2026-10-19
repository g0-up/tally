import { useRef, useState } from "react";
import { useFocusFirstInvalid } from "../../shared/hooks/use-focus-first-invalid";

// Server refusals of single fields (`validation_failed`, `fields`), shown under the dialog's inputs
// so "Kiểm tra lại các ô được đánh dấu." always has a marked input to point at.

/** Errors of a dialog's own inputs, keyed by the dialog's names for the request fields. */
export function useFieldErrors<F extends string>(apiFields: Readonly<Record<string, F>>) {
  const [errors, setErrors] = useState<Partial<Record<F, string>>>({});
  const focusFirstInvalid = useFocusFirstInvalid();
  return {
    errors,
    /** For useCommit's setFieldError; false when the field is not one of this dialog's inputs. */
    onFieldError: (field: string, message: string): boolean => {
      const key = apiFields[field];
      if (key === undefined) return false;
      setErrors((cur) => ({ ...cur, [key]: message }));
      return true;
    },
    /** A check of the dialog's own failed: the message goes under that input, which takes focus. */
    fail: (key: F, message: string) => {
      setErrors({ [key]: message } as Partial<Record<F, string>>);
      focusFirstInvalid();
    },
    clear: (key: F) => setErrors((cur) => (cur[key] === undefined ? cur : { ...cur, [key]: undefined })),
    reset: () => setErrors({}),
  };
}

/** `allocations[2].amount` → 2. */
function allocationIndex(field: string): number | null {
  const m = /^allocations\[(\d+)\]/.exec(field);
  return m ? Number(m[1]) : null;
}

/**
 * Errors of allocation lines, by document. The server names a line by its index in the request,
 * so the documents sent are remembered in that order.
 */
export function useAllocationErrors() {
  const [errors, setErrors] = useState<ReadonlyMap<string, string>>(new Map());
  const sent = useRef<readonly string[]>([]);
  return {
    errors,
    /** Call with the documents of the allocations about to be sent, in request order. */
    sending: (obligationIds: readonly string[]) => {
      sent.current = obligationIds;
      setErrors(new Map());
    },
    onFieldError: (field: string, message: string): boolean => {
      const i = allocationIndex(field);
      const id = i !== null ? sent.current[i] : undefined;
      if (id === undefined) return false;
      setErrors((cur) => new Map(cur).set(id, message));
      return true;
    },
    clear: (obligationId: string) =>
      setErrors((cur) => {
        if (!cur.has(obligationId)) return cur;
        const next = new Map(cur);
        next.delete(obligationId);
        return next;
      }),
  };
}
