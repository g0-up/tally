import type { InputHTMLAttributes, Ref, TextareaHTMLAttributes } from "react";
import { cx } from "../lib/cx";
import { useFieldControl } from "./field-context";

export interface InputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "size"> {
  /** `sm` = 96px right-aligned table cell input, `md` = 140px. */
  size?: "sm" | "md";
  invalid?: boolean;
  ref?: Ref<HTMLInputElement>;
}

/** Prototype `.in` text input. Always controlled by the caller (no remount on re-render). */
export function Input({ size, invalid, className, type = "text", "aria-describedby": describedBy, ...rest }: InputProps) {
  const field = useFieldControl({ describedBy, invalid });
  return <input type={type} className={cx("in", size, className)} {...field} {...rest} />;
}

export function Textarea({
  className,
  invalid,
  "aria-describedby": describedBy,
  ...rest
}: TextareaHTMLAttributes<HTMLTextAreaElement> & { invalid?: boolean; ref?: Ref<HTMLTextAreaElement> }) {
  const field = useFieldControl({ describedBy, invalid });
  return <textarea className={cx("in", className)} {...field} {...rest} />;
}
