import type { ButtonHTMLAttributes, HTMLAttributes, Ref } from "react";
import { cx } from "../lib/cx";

export type ButtonVariant = "primary" | "navy" | "line" | "danger";

export const PENDING_LABEL = "Đang ghi sổ…";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: "md" | "sm";
  /**
   * A write is in flight: the label swaps to `pendingLabel` and clicks (and the form submit they
   * would start) are ignored, so nothing is sent twice. The button stays focusable, marked
   * `aria-disabled` even when `disabled` is also set, so the keyboard user's place is not lost
   * while waiting.
   */
  pending?: boolean;
  pendingLabel?: string;
  ref?: Ref<HTMLButtonElement>;
}

/** Prototype `.btn` with `.btn-primary | .btn-navy | .btn-line | .btn-danger` and `.btn-sm`. */
export function Button({ variant = "line", size = "md", pending = false, pendingLabel = PENDING_LABEL, className, disabled, children, type = "button", onClick, ...rest }: ButtonProps) {
  return (
    <button
      type={type}
      className={cx("btn", `btn-${variant}`, size === "sm" && "btn-sm", className)}
      disabled={pending ? undefined : disabled}
      aria-busy={pending || undefined}
      {...rest}
      aria-disabled={pending || rest["aria-disabled"]}
      onClick={(e) => {
        if (pending) e.preventDefault();
        else onClick?.(e);
      }}
    >
      {pending ? pendingLabel : children}
    </button>
  );
}

/** Prototype `.btn-row`: a wrapping row of buttons. */
export function ButtonRow({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cx("btn-row", className)} {...rest} />;
}
