import { useId } from "react";
import { READ_ONLY_REASON, useReadOnly } from "../../../shared/hooks/use-read-only";

/**
 * Why a posting button is off while the subscription is read-only, as visible text tied to the
 * button: a `title` never reaches keyboard or touch users.
 */
export function useReadOnlyHint() {
  const { readOnly, reason } = useReadOnly();
  const id = useId();
  return {
    readOnly,
    describedBy: readOnly ? id : undefined,
    hint: readOnly ? (
      <p className="hint" id={id}>
        {reason ?? READ_ONLY_REASON}
      </p>
    ) : null,
  };
}
