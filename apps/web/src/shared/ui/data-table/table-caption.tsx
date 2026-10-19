import type { ReactNode } from "react";

/** Visually hidden caption naming the table for screen readers (usually the section title). */
export function TableCaption({ children }: { children: ReactNode }) {
  return <caption className="sr-only">{children}</caption>;
}
