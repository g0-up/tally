import type { ReactNode } from "react";
import { cx } from "../lib/cx";
import { TableCaption } from "./data-table/table-caption";

export interface MatrixTableProps<R, C> {
  caption: ReactNode;
  /** Header of the first (row label) column. */
  corner: ReactNode;
  rows: readonly R[];
  columns: readonly C[];
  rowKey: (row: R) => string;
  columnKey: (column: C) => string;
  rowHeader: (row: R) => ReactNode;
  columnHeader: (column: C) => ReactNode;
  cell: (row: R, column: C) => ReactNode;
  flat?: boolean;
}

/** Prototype `.matrix`: dense grid with centred cells and a left-aligned row header column. */
export function MatrixTable<R, C>({ caption, corner, rows, columns, rowKey, columnKey, rowHeader, columnHeader, cell, flat = false }: MatrixTableProps<R, C>) {
  return (
    <div className={cx("tw", flat && "flat")}>
      <table className="t matrix">
        <TableCaption>{caption}</TableCaption>
        <thead>
          <tr>
            <th scope="col">{corner}</th>
            {columns.map((c) => (
              <th key={columnKey(c)} scope="col">
                {columnHeader(c)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={rowKey(r)}>
              <th scope="row">{rowHeader(r)}</th>
              {columns.map((c) => (
                <td key={columnKey(c)}>{cell(r, c)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
