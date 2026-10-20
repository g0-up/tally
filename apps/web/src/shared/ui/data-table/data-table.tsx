import { Fragment, useId, type ReactNode } from "react";
import { useOneStore } from "../../hooks/use-one-store";
import { cx } from "../../lib/cx";
import { TableCaption } from "./table-caption";
import { TableWrap } from "./table-wrap";
import { EMPTY_TABLE_TEXT, visibleColumns, type Column, type RowProps } from "./columns";

export type { Column } from "./columns";

/** Read-only name of a column left without a visible header (row actions). */
export const ACTIONS_HEADER = "Thao tác";

export interface DataTableProps<R> {
  /** Screen-reader caption, usually the section title. */
  caption: ReactNode;
  columns: ReadonlyArray<Column<R>>;
  rows: readonly R[];
  rowKey: (row: R, index: number) => string;
  /** Borderless variant inside cards and dialogs (`.tw.flat`). */
  flat?: boolean;
  empty?: ReactNode;
  /** Extra rows rendered right after a row (expanded color rows, inline pickers). */
  after?: (row: R, index: number, columnCount: number) => ReactNode;
  rowProps?: (row: R, index: number) => RowProps;
}

/** Prototype `table()`: `.tw > table.t` with numeric columns, optional footer and empty state. */
export function DataTable<R>({ caption, columns, rows, rowKey, flat = false, empty, after, rowProps }: DataTableProps<R>) {
  const oneStore = useOneStore();
  const captionId = useId();
  const cols = visibleColumns(columns, oneStore);
  if (rows.length === 0) {
    return (
      <div className={cx("tw", flat && "flat")}>
        <div className="empty">{empty ?? EMPTY_TABLE_TEXT}</div>
      </div>
    );
  }
  const hasFooter = cols.some((c) => c.footer !== undefined);
  return (
    <TableWrap flat={flat} labelledBy={captionId}>
      <table className="t">
        <TableCaption id={captionId}>{caption}</TableCaption>
        <thead>
          <tr>
            {cols.map((c) => (
              <th
                key={c.key}
                scope="col"
                className={c.numeric ? "num" : undefined}
                aria-label={c.header === "" ? ACTIONS_HEADER : undefined}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <Fragment key={rowKey(row, i)}>
              <tr {...rowProps?.(row, i)}>
                {cols.map((c) => (
                  <td key={c.key} className={cx(c.numeric && "num", c.className) || undefined}>
                    {c.cell(row, i)}
                  </td>
                ))}
              </tr>
              {after?.(row, i, cols.length)}
            </Fragment>
          ))}
        </tbody>
        {hasFooter ? (
          <tfoot>
            <tr>
              {cols.map((c) => (
                <td key={c.key} className={c.numeric ? "num" : undefined}>
                  {c.footer}
                </td>
              ))}
            </tr>
          </tfoot>
        ) : null}
      </table>
    </TableWrap>
  );
}
