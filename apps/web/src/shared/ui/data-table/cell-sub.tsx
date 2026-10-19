import type { ReactNode } from "react";

/** Prototype `<span class="sub">`: the small grey second line inside a table cell. */
export function CellSub({ children }: { children: ReactNode }) {
  return <span className="sub">{children}</span>;
}

/** Main text plus an optional second line. */
export function CellText({ main, sub }: { main: ReactNode; sub?: ReactNode }) {
  return (
    <>
      {main}
      {sub !== undefined && sub !== null && sub !== "" ? <CellSub>{sub}</CellSub> : null}
    </>
  );
}
