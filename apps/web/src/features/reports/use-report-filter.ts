import { useSearchParams } from "react-router";
import { useServerBusinessDate } from "../../shared/api/platform-queries";
import { isIsoDate, todayBusinessDate } from "../../shared/lib/dates";
import { REPORT_MESSAGES, seasonStart } from "./api/adapters";
import type { ReportConfig } from "./schemas";

// The filter of a report lives in the URL (`?from=&to=`, `?as_of=`, `?group_by=`, `?columns=`), so a
// view can be reloaded, shared and restored from a saved layout. Missing or malformed dates fall
// back to the Tết season so far: 01/10 of the season to the tenant's business date.

/** A report over a period (`from`–`to`) or at one date (`as_of`). */
export type RangeKind = "period" | "as_of";

export interface ReportFilterDefaults {
  range: RangeKind;
  /** Grouping sent when the URL names none; reports without groupings leave it out. */
  groupBy?: string;
  /** Columns sent when the URL names none; the server's full set when omitted. */
  columns?: readonly string[];
}

export interface ReportFilter {
  range: RangeKind;
  today: string;
  from: string;
  to: string;
  asOf: string;
  groupBy: string | null;
  columns: readonly string[] | null;
  /** The period is inverted; nothing is asked of the server until it is fixed. */
  invalid: string | null;
  /** Query parameters of the report and of its export; null while the business date loads or the period is invalid. */
  params: Readonly<Record<string, string>> | null;
  setFrom: (iso: string) => void;
  setTo: (iso: string) => void;
  setAsOf: (iso: string) => void;
  setGroupBy: (value: string) => void;
  /** The layout of a saved view: grouping and columns, the dates stay as they are. */
  applyConfig: (config: ReportConfig) => void;
  /** The current layout, as a saved view stores it. */
  config: ReportConfig;
}

const dateOr = (value: string | null, fallback: string) => (value !== null && isIsoDate(value) ? value : fallback);

export function useReportFilter(defaults: ReportFilterDefaults): ReportFilter {
  const serverToday = useServerBusinessDate();
  const today = serverToday ?? todayBusinessDate();
  const [search, setSearch] = useSearchParams();

  const from = dateOr(search.get("from"), seasonStart(today));
  const to = dateOr(search.get("to"), today);
  const asOf = dateOr(search.get("as_of"), today);
  const groupBy = defaults.groupBy === undefined ? null : search.get("group_by") || defaults.groupBy;
  const urlColumns = search.get("columns");
  const columns = urlColumns ? urlColumns.split(",").filter(Boolean) : (defaults.columns ?? null);
  const invalid = defaults.range === "period" && to < from ? REPORT_MESSAGES.periodInvalid : null;

  const write = (values: Readonly<Record<string, string | null>>) =>
    setSearch(
      (cur) => {
        const next = new URLSearchParams(cur);
        for (const [k, v] of Object.entries(values)) {
          if (v === null || v === "") next.delete(k);
          else next.set(k, v);
        }
        return next;
      },
      { replace: true },
    );

  const params: Record<string, string> = defaults.range === "period" ? { from, to } : { as_of: asOf };
  if (groupBy !== null) params.group_by = groupBy;
  if (columns !== null && columns.length > 0) params.columns = columns.join(",");

  return {
    range: defaults.range,
    today,
    from,
    to,
    asOf,
    groupBy,
    columns,
    invalid,
    params: serverToday === null || invalid !== null ? null : params,
    setFrom: (iso) => write({ from: iso }),
    setTo: (iso) => write({ to: iso }),
    setAsOf: (iso) => write({ as_of: iso }),
    setGroupBy: (value) => write({ group_by: value }),
    applyConfig: (config) => write({ group_by: config.group_by ?? null, columns: config.columns?.join(",") ?? null }),
    config: { columns: columns === null ? null : [...columns], group_by: groupBy, filters: {} },
  };
}
