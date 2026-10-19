import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { isApiError } from "../../shared/api/api-error";
import { useApiClient } from "../../shared/api/api-context";
import { reportErrorMessage } from "./api/adapters";
import { exportKeys, fetchExportJob, fetchRecentExports } from "./api/queries";
import { clearStoredExport, setStoredExport, unknownExport, useStoredExport } from "./export-job-store";
import { exportJobSchema, type ExportJob, type ExportStatus, type ReportKey } from "./schemas";
import { exportReadMessage, useExportDownload } from "./use-export-download";

// "Xuất CSV": the server writes the file in the background (`POST /export-jobs`), the screen asks for
// its state every few seconds until it ends, and only a finished, unexpired file is offered. The
// export being followed outlives the screen, so coming back to it resumes the reads; a tab that kept
// nothing picks up the newest export of the report the server still has running or downloadable.

const TERMINAL: ReadonlySet<ExportStatus> = new Set(["succeeded", "failed", "expired", "cancelled"]);

export const isTerminal = (job: ExportJob | undefined) => job !== undefined && TERMINAL.has(job.status);

/** Refusals another read cannot change: signed out, not allowed, or the export is gone. */
const STOPS: ReadonlySet<number> = new Set([401, 403, 404, 410]);

const stopsReads = (e: unknown) => isApiError(e) && STOPS.has(e.status);

/** An export of this report (and ledger) worth picking up again: still being written, or written and downloadable. */
function resumable(job: ExportJob, reportKey: ReportKey, variant: string | undefined): boolean {
  if (job.kind !== "report_csv" || job.report_key !== reportKey) return false;
  if (variant !== undefined && job.params.direction !== variant) return false;
  return job.status === "queued" || job.status === "running" || (job.status === "succeeded" && job.download_path !== null);
}

export interface ExportJobOptions {
  /** Ledger of the screen exporting on its own, when one report has several: its `direction` parameter ("receivable", "payable"). */
  variant?: string;
  /** Pause between two state reads. */
  intervalMs?: number;
  /** State reads of one round; then the screen stops asking until "Kiểm tra lại". */
  maxPolls?: number;
  /** Opens the file; the browser follows the download redirect. */
  open?: (path: string) => void;
}

export interface ExportJobState {
  job: ExportJob | undefined;
  /** The round of reads ended before the export did. */
  stalled: boolean;
  /** A refusal, a read that stopped the polling or a file that can no longer be downloaded, in the screen's words. */
  message: string | null;
  starting: boolean;
  cancelling: boolean;
  /** The fresh read before opening the file is in flight. */
  downloading: boolean;
  start: (params: Readonly<Record<string, string>>) => void;
  cancel: () => void;
  /** Starts a new round of state reads. */
  recheck: () => void;
  /** Reads the state once more and opens the file only while it can still be downloaded. */
  download: () => Promise<void>;
}

/** One export of `reportKey` (and `variant`) at a time, polled at a bounded rate and for a bounded number of reads. */
export function useExportJob(reportKey: ReportKey, { variant, intervalMs = 2000, maxPolls = 90, open }: ExportJobOptions = {}): ExportJobState {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const scope = variant === undefined ? reportKey : `${reportKey}:${variant}`;
  const current = useStoredExport(scope);
  const files = useExportDownload(open);
  const [message, setMessage] = useState<string | null>(null);
  const key = exportKeys.job(current?.id ?? "", current?.round ?? 0);
  const unknown = current === null && unknownExport(scope);

  const recent = useQuery({
    queryKey: exportKeys.recent(),
    queryFn: ({ signal }) => fetchRecentExports(client, signal),
    enabled: unknown,
    retry: false,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (!unknown) return;
    // Newest first: the first match is the export last started.
    const found = recent.data?.data.find((j) => resumable(j, reportKey, variant));
    if (found === undefined) return;
    queryClient.setQueryData(exportKeys.job(found.id, 0), found);
    setStoredExport(scope, { id: found.id, round: 0 });
  }, [unknown, recent.data, reportKey, variant, scope, queryClient]);

  const job = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => fetchExportJob(client, current?.id ?? "", signal),
    enabled: current !== null,
    // A state just received (created, cancelled, re-read) waits a full interval before the next read;
    // a screen shown again resumes from the state it left.
    refetchOnMount: false,
    // The next read is the retry.
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (q) =>
      isTerminal(q.state.data) || stopsReads(q.state.error) || q.state.dataUpdateCount + q.state.errorUpdateCount >= maxPolls ? false : intervalMs,
  });
  // Read on every render so the end of each read re-renders, even when the state did not change.
  const fetching = job.isFetching;
  const state = queryClient.getQueryState(key);
  const reads = (state?.dataUpdateCount ?? 0) + (state?.errorUpdateCount ?? 0);
  const stopped = current !== null && !fetching && stopsReads(job.error);
  const stalled = current !== null && !fetching && !stopped && !isTerminal(job.data) && reads >= maxPolls;

  const show = (id: string, round: number, data: ExportJob) => {
    queryClient.setQueryData(exportKeys.job(id, round), data);
    setStoredExport(scope, { id, round });
  };

  const start = useMutation({
    mutationFn: (params: Readonly<Record<string, string>>) => client.post("/export-jobs", { report_key: reportKey, params }, { schema: exportJobSchema }),
    onMutate: () => {
      setMessage(null);
      clearStoredExport(scope);
    },
    onSuccess: (data) => show(data.id, 0, data),
    onError: (e) => setMessage(reportErrorMessage(e)),
  });

  const cancel = useMutation({
    mutationFn: (id: string) => client.post(`/export-jobs/${id}:cancel`, undefined, { schema: exportJobSchema }),
    onMutate: () => setMessage(null),
    onSuccess: (data) => show(data.id, (current?.round ?? 0) + 1, data),
    onError: (e) => {
      setMessage(reportErrorMessage(e));
      void job.refetch();
    },
  });

  const recheck = () => {
    if (current === null) return;
    setMessage(null);
    const last = job.data;
    if (last) show(current.id, current.round + 1, last);
    else setStoredExport(scope, { id: current.id, round: current.round + 1 });
  };

  const download = async () => {
    if (current === null) return;
    setMessage(null);
    const result = await files.download(current.id);
    if (result.job) show(current.id, current.round + 1, result.job);
    setMessage(result.message);
  };

  return {
    job: current === null ? undefined : job.data,
    stalled,
    message: message ?? (stopped ? exportReadMessage(job.error) : null),
    starting: start.isPending,
    cancelling: cancel.isPending,
    downloading: files.pending,
    start: (params) => start.mutate(params),
    cancel: () => (current === null ? undefined : cancel.mutate(current.id)),
    recheck,
    download,
  };
}
