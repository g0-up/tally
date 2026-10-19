import { useState } from "react";
import { isApiError } from "../../shared/api/api-error";
import { useApiClient } from "../../shared/api/api-context";
import { REPORT_MESSAGES, reportErrorMessage } from "./api/adapters";
import { fetchExportJob } from "./api/queries";
import type { ExportJob } from "./schemas";

// "Tải tệp CSV" and the "Tải về" of an export_ready notice: the file is opened only after a fresh read
// says it is written and unexpired, so a listing shown minutes ago never leads to a dead link.

const openInPlace = (path: string) => window.location.assign(path);

/** Why a read export cannot be downloaded now. */
function unavailableMessage(job: ExportJob): string {
  if (job.status === "queued" || job.status === "running") return REPORT_MESSAGES.exportNotReady;
  // A succeeded export without a download path has passed its expiry.
  if (job.status === "expired" || job.status === "succeeded") return REPORT_MESSAGES.exportExpired;
  return REPORT_MESSAGES.exportUnavailable;
}

/** A failed read of an export, in the screen's words; a missing export is not a missing report. */
export function exportReadMessage(e: unknown): string {
  if (isApiError(e) && e.status === 404) return REPORT_MESSAGES.exportGone;
  if (isApiError(e) && e.status === 410) return REPORT_MESSAGES.exportExpired;
  return reportErrorMessage(e);
}

export interface ExportDownloadResult {
  /** The state just read, or null when it could not be read. */
  job: ExportJob | null;
  /** Why nothing was opened, in the screen's words; null once the file is opening. */
  message: string | null;
}

export interface ExportDownload {
  download: (jobId: string) => Promise<ExportDownloadResult>;
  pending: boolean;
  /** Message of the last attempt. */
  message: string | null;
}

/** Reads `GET /export-jobs/{id}` and opens its download only while the file can still be downloaded. */
export function useExportDownload(open: (path: string) => void = openInPlace): ExportDownload {
  const client = useApiClient();
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const download = async (jobId: string): Promise<ExportDownloadResult> => {
    setPending(true);
    setMessage(null);
    let result: ExportDownloadResult;
    try {
      const job = await fetchExportJob(client, jobId);
      if (job.status === "succeeded" && job.download_path) {
        open(job.download_path);
        result = { job, message: null };
      } else {
        result = { job, message: unavailableMessage(job) };
      }
    } catch (e) {
      result = { job: null, message: exportReadMessage(e) };
    }
    setPending(false);
    setMessage(result.message);
    return result;
  };

  return { download, pending, message };
}
