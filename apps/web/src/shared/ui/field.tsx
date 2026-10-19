import { Children, cloneElement, isValidElement, useId, useMemo, type ReactElement, type ReactNode } from "react";
import { useOneStore } from "../hooks/use-one-store";
import { cx } from "../lib/cx";
import { FieldContext, joinIds } from "./field-context";

export interface FieldProps {
  label: ReactNode;
  children: ReactNode;
  hint?: ReactNode;
  /** Validation message (from zod or the API `fields` map). */
  error?: string | undefined;
  /** Inline layout (`.f.inline`): label and control on one line. */
  inline?: boolean;
  /** "Kho nhận", "Kho đóng" and similar fields disappear in a one-store tenant. */
  hideWhenOneStore?: boolean;
  className?: string;
}

type HostControlProps = { "aria-describedby"?: string | undefined; "aria-invalid"?: boolean | "true" | "false" | undefined };
const HOST_CONTROLS = new Set(["input", "select", "textarea"]);

/**
 * Native controls written directly in a Field cannot read its context, so they get the props here.
 * Always mapped (even with nothing to add) so the children keep the same keys and an error
 * appearing does not remount the control.
 */
function describeHostControls(children: ReactNode, describedBy: string | undefined, invalid: boolean): ReactNode {
  return Children.map(children, (child) => {
    if (!isValidElement<HostControlProps>(child) || typeof child.type !== "string" || !HOST_CONTROLS.has(child.type)) return child;
    if (describedBy === undefined && !invalid) return child;
    const own = child.props;
    return cloneElement(child as ReactElement<HostControlProps>, {
      "aria-describedby": joinIds(own["aria-describedby"], describedBy),
      ...(invalid ? { "aria-invalid": true } : {}),
    });
  });
}

/**
 * Prototype `F()`: `<label class="f"><span>label</span>control</label>`; wrapping the control in
 * the label gives it its accessible name. The hint and the error sit after the label, outside it,
 * so they are not part of the name, and the control points at them with `aria-describedby`
 * (shared/ui controls read it from context; native controls written directly here get it as props).
 * The wrapper is always rendered so an error appearing never remounts the control.
 */
export function Field({ label, children, hint, error, inline = false, hideWhenOneStore = false, className }: FieldProps) {
  const oneStore = useOneStore();
  const baseId = useId();
  const hintId = hint !== undefined ? `${baseId}-hint` : undefined;
  const errorId = error ? `${baseId}-err` : undefined;
  const describedBy = joinIds(hintId, errorId);
  const invalid = Boolean(error);
  const state = useMemo(() => ({ describedBy, invalid }), [describedBy, invalid]);
  if (hideWhenOneStore && oneStore) return null;
  return (
    <div className={cx("f", inline && "inline", className)}>
      <label className={cx("f", inline && "inline")}>
        <span>{label}</span>
        <FieldContext.Provider value={state}>{describeHostControls(children, describedBy, invalid)}</FieldContext.Provider>
      </label>
      {hint !== undefined ? (
        <span className="hint" id={hintId}>
          {hint}
        </span>
      ) : null}
      {error ? (
        <span className="err" role="alert" id={errorId}>
          {error}
        </span>
      ) : null}
    </div>
  );
}
