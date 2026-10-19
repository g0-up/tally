import type { InputHTMLAttributes, ReactNode, Ref } from "react";
import { cx } from "../lib/cx";

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type"> {
  label: ReactNode;
  ref?: Ref<HTMLInputElement>;
}

/** Prototype `<label class="check"><input type="checkbox"> label</label>`. */
export function Checkbox({ label, className, ...rest }: CheckboxProps) {
  return (
    <label className={cx("check", className)}>
      <input type="checkbox" {...rest} /> {label}
    </label>
  );
}
