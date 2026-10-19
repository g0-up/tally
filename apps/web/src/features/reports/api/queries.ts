import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { isApiError } from "../../../shared/api/api-error";
import { useApiClient } from "../../../shared/api/api-context";
import { listOf } from "../../../shared/api/decimal-schemas";
import type { HttpClient } from "../../../shared/api/http";
import { exportJobSchema, reportTableSchema, savedReportSchema, type ReportKey, type ReportRow, type ReportTable } from "../schemas";

// Read side of the Báo cáo screens. A report is read a page at a time with its cursor and never
// as a whole: the full result of a large store only leaves the server as an export file.

export const reportKeys = {
  all: ["reports"] as const,
  run: (key: ReportKey, params: Readonly<Record<string, string>>) => ["reports", "run", key, params] as const,
  saved: (key: ReportKey) => ["reports", "saved", key] as const,
};

/**
 * Exports sit outside the "reports" prefix that postings refresh: a new sale changes the figures,
 * not a file already written.
 */
export const exportKeys = {
  /** One round of state reads of an export; "Kiểm tra lại" starts the next round. */
  job: (id: string, round: number) => ["report-exports", id, round] as const,
  /** The signed-in user's newest exports, read to pick one up again. */
  recent: () => ["report-exports", "recent"] as const,
};

/** Rows one page of a report holds. */
const PAGE = 100;

/** A refusal of the request itself (range, validation) is not worth a second try. */
const retryServerErrors = (failures: number, e: unknown) => failures < 1 && !(isApiError(e) && e.status >= 400 && e.status < 500);

export interface ReportPages {
  /** The first page: columns, totals, summary, sections and notes of the whole result. */
  first: ReportTable | undefined;
  /** Rows of every page read so far. */
  rows: ReportRow[];
  isLoading: boolean;
  error: unknown;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => unknown;
  refetch: () => unknown;
}

/** Runs a report (`GET /reports/{reportKey}`) a page at a time; null params wait (invalid period). */
export function useReport(key: ReportKey, params: Readonly<Record<string, string>> | null): ReportPages {
  const client = useApiClient();
  const query = useInfiniteQuery({
    queryKey: reportKeys.run(key, params ?? {}),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => client.get(`/reports/${key}`, { query: { ...params, limit: PAGE, cursor: pageParam }, schema: reportTableSchema, signal }),
    getNextPageParam: (last) => last.next_cursor,
    enabled: params !== null,
    retry: retryServerErrors,
  });
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.rows) ?? [], [query.data]);
  return {
    // Later pages of the stock reports carry no totals; the first page's stand for the whole result.
    first: query.data?.pages[0],
    rows,
    isLoading: query.isPending && params !== null,
    error: query.error,
    hasMore: query.hasNextPage,
    loadingMore: query.isFetchingNextPage,
    loadMore: () => query.fetchNextPage(),
    refetch: () => query.refetch(),
  };
}

/** Saved layouts of one report, by name (`GET /saved-reports?report_key=`); read when the list opens. */
export function useSavedReports(key: ReportKey, enabled: boolean) {
  const client = useApiClient();
  return useQuery({
    queryKey: reportKeys.saved(key),
    queryFn: ({ signal }) => client.get("/saved-reports", { query: { report_key: key }, schema: listOf(savedReportSchema), signal }),
    enabled,
    select: (r) => r.data,
  });
}

/**
 * One export and its state (`GET /export-jobs/{id}`). Polled, yet it goes through the global error
 * handler so an expired session signs out at once: polling stops on that refusal, so the handler
 * sees it once, and it raises no toast for it.
 */
export const fetchExportJob = (client: HttpClient, id: string, signal?: AbortSignal) =>
  client.get(`/export-jobs/${id}`, { schema: exportJobSchema, ...(signal ? { signal } : {}) });

/** Exports read to pick one up again; a job older than this many newer ones is not resumed. */
const RECENT_EXPORTS = 50;

/**
 * The signed-in user's newest exports (`GET /export-jobs`), report CSVs and full data exports alike.
 * Silent: the screen works the same when this read fails, it just offers no earlier export.
 */
export const fetchRecentExports = (client: HttpClient, signal?: AbortSignal) =>
  client.get("/export-jobs", { query: { limit: RECENT_EXPORTS }, schema: listOf(exportJobSchema), silent: true, ...(signal ? { signal } : {}) });
