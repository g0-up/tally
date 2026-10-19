import type { HTMLAttributes } from "react";
import { cx } from "../lib/cx";

export type CalloutTone = "info" | "warn" | "bad" | "ok";

/** Prototype `.callout` (blue info) and `.callout.warn | .bad | .ok`. */
export function Callout({ tone = "info", className, ...rest }: HTMLAttributes<HTMLDivElement> & { tone?: CalloutTone }) {
  return <div className={cx("callout", tone !== "info" && tone, className)} {...rest} />;
}
