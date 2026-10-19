import { useMemo, type ReactNode, type Ref, type SelectHTMLAttributes } from "react";
import { cx } from "../../../shared/lib/cx";
import { FieldContext, joinIds, useFieldControl } from "../../../shared/ui/field-context";
import { errRef } from "./field-errors";

/**
 * Prototype `obF()`: `.f` with a visible `<label for>`, the control, the hint and the error under
 * it. The control reads its `aria-describedby` and `aria-invalid` from the field context.
 */
export function OnbField({ id, label, required = false, hint, error, wide = false, children }: { id: string; label: ReactNode; required?: boolean; hint?: ReactNode; error?: string | undefined; wide?: boolean; children: ReactNode }) {
  const hintId = hint !== undefined ? `${id}-h` : undefined;
  const state = useMemo(() => ({ describedBy: joinIds(hintId, errRef(id, error)), invalid: Boolean(error) }), [id, hintId, error]);
  return (
    <div className={cx("f", wide && "wide")}>
      <label htmlFor={id}>
        {label}
        {required ? (
          <>
            {" "}
            <em className="req" aria-hidden="true">
              *
            </em>
          </>
        ) : null}
      </label>
      <FieldContext.Provider value={state}>{children}</FieldContext.Provider>
      {hint !== undefined ? (
        <span className="hint" id={hintId}>
          {hint}
        </span>
      ) : null}
      <OnbErr id={id} error={error} />
    </div>
  );
}

/** Prototype `obErr()`: the error under a control, kept in place (hidden) while there is none. */
export function OnbErr({ id, error }: { id: string; error: string | undefined }) {
  return (
    <span className="err" id={`${id}-e`} hidden={!error}>
      {error}
    </span>
  );
}

/** Native select that reads the enclosing field's description and error state. */
export function OnbSelect({ invalid, "aria-describedby": describedBy, ...rest }: SelectHTMLAttributes<HTMLSelectElement> & { invalid?: boolean; ref?: Ref<HTMLSelectElement> }) {
  const field = useFieldControl({ describedBy, invalid });
  return <select {...field} {...rest} />;
}
