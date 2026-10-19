import type { HTMLAttributes, ReactNode } from "react";

export const EMPTY_TABLE_TEXT = "Chưa có dữ liệu.";

export interface Column<R> {
  key: string;
  header: ReactNode;
  cell: (row: R, index: number) => ReactNode;
  /** Right-aligned tabular numbers (`.num`). */
  numeric?: boolean;
  className?: string;
  /** Warehouse / branch columns ("Kho", "Kho · phạm vi", "Chi nhánh") disappear in a one-store tenant. */
  hideWhenOneStore?: boolean;
  /** Footer cell content; the footer row renders when any column defines one. */
  footer?: ReactNode;
}

export type RowProps = HTMLAttributes<HTMLTableRowElement>;

/** Visible columns after applying the one-store rule. */
export function visibleColumns<R>(columns: ReadonlyArray<Column<R>>, oneStore: boolean): ReadonlyArray<Column<R>> {
  return oneStore ? columns.filter((c) => !c.hideWhenOneStore) : columns;
}
