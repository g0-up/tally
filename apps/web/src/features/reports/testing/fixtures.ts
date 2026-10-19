import { HttpResponse, http } from "msw";
import { apiUrl } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import type { ExportJob, ReportColumn, ReportTable, SavedReport } from "../schemas";

// API payloads of the report screens, shaped as OpenAPI returns them (the test tenant's business
// date is 2026-10-12, so the Tết season starts on 2026-10-01).

/** The item at `i`, failing the test when the list is shorter. */
export function at<T>(items: readonly T[], i: number): T {
  const item = items[i];
  if (item === undefined) throw new Error(`No item at ${i} of ${items.length}`);
  return item;
}

export const col = (key: string, label: string, kind: ReportColumn["kind"] = "text"): ReportColumn => ({ key, label, kind });

export function reportPage(over: Partial<ReportTable> = {}): ReportTable {
  return {
    report_key: "nxt",
    title: "Nhập–xuất–tồn",
    from: "2026-10-01",
    to: "2026-10-12",
    as_of: null,
    group_by: null,
    scope: "",
    notes: [],
    provisional: false,
    columns: [],
    rows: [],
    totals: null,
    summary: [],
    sections: [],
    next_cursor: null,
    ...over,
  };
}

export function exportJob(over: Partial<ExportJob> = {}): ExportJob {
  return {
    id: "exp-1",
    kind: "report_csv",
    report_key: "nxt",
    params: { from: "2026-10-01", to: "2026-10-12" },
    status: "queued",
    row_count: null,
    error: null,
    created_at: "2026-10-12T02:00:00Z",
    finished_at: null,
    expires_at: null,
    download_path: null,
    ...over,
  };
}

export function savedReport(over: Partial<SavedReport> = {}): SavedReport {
  return {
    id: "sv-1",
    report_key: "revenue",
    name: "Theo khách mùa Tết",
    config: { columns: [], group_by: "customer", filters: {} },
    updated_at: "2026-10-10T03:00:00Z",
    ...over,
  };
}

/** Answers `GET /reports/{key}` and records the query string of every run. */
export function reportRoute(key: string, answer: (query: URLSearchParams) => ReportTable | Response): URLSearchParams[] {
  const seen: URLSearchParams[] = [];
  server.use(
    http.get(apiUrl(`/reports/${key}`), ({ request }) => {
      const query = new URL(request.url).searchParams;
      seen.push(query);
      const a = answer(query);
      return a instanceof Response ? a : HttpResponse.json(a);
    }),
  );
  return seen;
}

/** The KPI card whose label reads `label`. */
export function kpi(label: string): HTMLElement {
  const card = [...document.querySelectorAll<HTMLElement>(".kpi")].find((el) => el.querySelector(".k")?.textContent === label);
  if (card === undefined) throw new Error(`No KPI card "${label}"`);
  return card;
}
