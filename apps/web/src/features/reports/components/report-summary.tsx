import type { ReactNode } from "react";
import { Grid } from "../../../shared/ui/grid";
import { KpiCard } from "../../../shared/ui/kpi-card";
import { formatCell } from "../api/adapters";
import type { Metric } from "../schemas";

export interface ReportSummaryProps {
  metrics: readonly Metric[];
  /** The screen's own label for a metric key, the server's otherwise. */
  labels?: Readonly<Record<string, string>>;
  /** A line under a metric; the previous period's figure when the server sends one. */
  sub?: (metric: Metric) => ReactNode;
}

const colsFor = (n: number) => (n >= 4 ? "g4" : n === 3 ? "g3" : "g2");

/** The summary metrics of a report as KPI cards. */
export function ReportSummary({ metrics, labels, sub }: ReportSummaryProps) {
  if (metrics.length === 0) return null;
  return (
    <Grid cols={colsFor(metrics.length)}>
      {metrics.map((m) => {
        const line = sub?.(m) ?? (m.previous ? `Kỳ trước ${formatCell(m.kind, m.previous)}` : undefined);
        return <KpiCard key={m.key} label={labels?.[m.key] ?? m.label} value={formatCell(m.kind, m.value)} {...(line !== undefined ? { sub: line } : {})} />;
      })}
    </Grid>
  );
}
