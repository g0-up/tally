import type { ReactNode } from "react";
import { cx } from "../lib/cx";

/** Prototype `.tag` (navy) and `.tag.alt` (slate) labels, e.g. "Giỏ quà", "Combo". */
export function Badge({ children, alt = false }: { children: ReactNode; alt?: boolean }) {
  return <span className={cx("tag", alt && "alt")}>{children}</span>;
}

/** Prototype `.lock`: small grey note for locked periods or read-only values. */
export function LockNote({ children }: { children: ReactNode }) {
  return <span className="lock">{children}</span>;
}
