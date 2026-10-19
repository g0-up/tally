import type { ReactNode } from "react";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { REPORT_TEXT, formatCell, isNumericKind } from "../api/adapters";
import type { ReportColumn, ReportRow } from "../schemas";

/** Replaces the text of one cell (a code that opens the stock card); undefined keeps the text. */
export type CellLink = (column: ReportColumn, row: ReportRow, text: string) => ReactNode | undefined;

export interface ReportTableProps {
  caption: string;
  columns: readonly ReportColumn[];
  rows: readonly ReportRow[];
  /** Totals of the whole result, as the footer; the first column reads "Tổng" when the server leaves it empty. */
  totals?: ReportRow | null | undefined;
  link?: CellLink;
  flat?: boolean;
}

const footerOf = (c: ReportColumn, i: number, totals: ReportRow): string => {
  const v = totals[c.key];
  if (i === 0) return v ?? REPORT_TEXT.total;
  return v === null || v === undefined ? "" : formatCell(c.kind, v);
};

/** A report's rows under the server's column labels and kinds; every figure is the server's. */
export function ReportTable({ caption, columns, rows, totals, link, flat }: ReportTableProps) {
  const cols: Column<ReportRow>[] = columns.map((c, i) => ({
    key: c.key,
    header: c.label,
    numeric: isNumericKind(c.kind),
    cell: (row) => {
      const text = formatCell(c.kind, row[c.key]);
      return link?.(c, row, text) ?? text;
    },
    ...(totals ? { footer: footerOf(c, i, totals) } : {}),
  }));
  return <DataTable caption={caption} columns={cols} rows={rows} rowKey={(r, i) => `${r.id ?? ""}#${i}`} flat={flat ?? false} />;
}
