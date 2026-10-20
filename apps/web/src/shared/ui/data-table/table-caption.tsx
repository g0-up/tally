import type { ReactNode } from "react";

/** Visually hidden caption naming the table for screen readers (usually the section title). */
export function TableCaption({ id, children }: { id?: string; children: ReactNode }) {
  return (
    <caption id={id} className="sr-only">
      {children}
    </caption>
  );
}
