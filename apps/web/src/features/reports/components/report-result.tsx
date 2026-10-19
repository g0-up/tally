import type { ReactNode } from "react";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { reportErrorMessage } from "../api/adapters";
import type { ReportPages } from "../api/queries";
import type { ReportTable as ReportPage } from "../schemas";
import { ReportTable, type CellLink } from "./report-table";

export interface ReportResultProps {
  report: ReportPages;
  caption: string;
  /** The period is inverted: its message replaces the result. */
  invalid?: string | null;
  /** Above the rows: KPI cards or the screen's own block, from the first page. */
  summary?: (first: ReportPage) => ReactNode;
  link?: CellLink;
}

/** What the server says about the figures: scope and notes, as a warning while they are provisional. */
function ReportNotes({ first }: { first: ReportPage }) {
  const lines = [first.scope, ...first.notes].filter((l) => l !== "");
  if (lines.length === 0) return null;
  if (first.provisional) {
    return (
      <Callout tone="warn">
        {lines.map((l) => (
          <p key={l}>{l}</p>
        ))}
      </Callout>
    );
  }
  return (
    <div>
      {lines.map((l) => (
        <p key={l} className="hint">
          {l}
        </p>
      ))}
    </div>
  );
}

/**
 * One report run: summary, rows read so far with the totals of the whole result, "Xem thêm" while
 * pages remain, and the server's extra sections. A refused run shows why instead.
 */
export function ReportResult({ report, caption, invalid, summary, link }: ReportResultProps) {
  if (invalid) {
    return (
      <Callout tone="bad" role="alert">
        {invalid}
      </Callout>
    );
  }
  const first = report.first;
  if (first === undefined) {
    if (report.error) {
      return (
        <Callout tone="bad" role="alert">
          {reportErrorMessage(report.error)}{" "}
          <Button size="sm" onClick={() => void report.refetch()}>
            Thử lại
          </Button>
        </Callout>
      );
    }
    return <ListLoading />;
  }
  return (
    <>
      {summary?.(first)}
      <ReportNotes first={first} />
      <ReportTable caption={caption} columns={first.columns} rows={report.rows} totals={first.totals} {...(link ? { link } : {})} />
      <ListFooter list={report} />
      {first.sections.map((s) => (
        <Card key={s.key} title={s.title}>
          <ReportTable caption={s.title} columns={s.columns} rows={s.rows} totals={s.totals} flat />
        </Card>
      ))}
    </>
  );
}
