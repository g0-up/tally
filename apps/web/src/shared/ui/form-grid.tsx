import type { HTMLAttributes } from "react";
import { cx } from "../lib/cx";

/** Prototype `.fg`: auto-fit grid of fields (min 190px per column). */
export function FormGrid({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cx("fg", className)} {...rest} />;
}
