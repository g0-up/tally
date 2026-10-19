import type { HTMLAttributes } from "react";
import { cx } from "../lib/cx";

export type GridLayout = "g2" | "g3" | "g4" | "g-main";

/** Prototype `.grid.g2|g3|g4|g-main`; collapses at the 1100/860/520 breakpoints. */
export function Grid({ cols, className, ...rest }: HTMLAttributes<HTMLDivElement> & { cols: GridLayout }) {
  return <div className={cx("grid", cols, className)} {...rest} />;
}

/** Prototype `.stack`: vertical grid with the 20px gap. */
export function Stack({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cx("stack", className)} {...rest} />;
}
