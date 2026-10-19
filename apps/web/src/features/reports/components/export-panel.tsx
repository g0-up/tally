import { fdt } from "../../../shared/lib/dates";
import { num } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout, type CalloutTone } from "../../../shared/ui/callout";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { StatusTone } from "../../../shared/domain/status-tone";
import { EXPORT_STATUS_LABELS, REPORT_MESSAGES, REPORT_TEXT } from "../api/adapters";
import type { ExportStatus } from "../schemas";
import type { ExportJobState } from "../use-export-job";

const TONES: Readonly<Record<ExportStatus, CalloutTone>> = { queued: "info", running: "info", succeeded: "ok", failed: "bad", expired: "warn", cancelled: "warn" };
const PILL_TONES: Readonly<Record<ExportStatus, StatusTone>> = { queued: "info", running: "warn", succeeded: "ok", failed: "bad", expired: "bad", cancelled: "mute" };

/** "Xuất CSV" next to the filter; a second export waits for the first to end. */
export function ExportButton({ state, params }: { state: ExportJobState; params: Readonly<Record<string, string>> | null }) {
  const busy = state.job?.status === "queued" || state.job?.status === "running";
  return (
    <Button size="sm" onClick={() => (params ? state.start(params) : undefined)} disabled={params === null || busy} pending={state.starting} pendingLabel="Đang gửi…">
      {REPORT_TEXT.export}
    </Button>
  );
}

/** State of the last export: the file to download once written, or why there is none. */
export function ExportPanel({ state }: { state: ExportJobState }) {
  const { job, message } = state;
  if (job === undefined) {
    return message ? (
      <Callout tone="bad" role="alert">
        {message}
      </Callout>
    ) : null;
  }
  const active = job.status === "queued" || job.status === "running";
  const path = job.status === "succeeded" ? job.download_path : null;
  const failure = job.status === "failed" ? job.error : null;
  // The state is a polite update; why the export failed is one alert beside it, never nested in it.
  return (
    <>
      <Callout tone={TONES[job.status]} role="status" aria-live="polite" aria-label="Tệp xuất CSV">
        <ButtonRow>
          <StatusPill label={EXPORT_STATUS_LABELS[job.status]} tone={PILL_TONES[job.status]} />
          {job.row_count !== null && job.status === "succeeded" ? <span>{num(String(job.row_count))} dòng</span> : null}
          {path ? (
            // A button, not a link: every download goes through the fresh read of the export's state.
            <Button size="sm" variant="primary" onClick={() => void state.download()} pending={state.downloading} pendingLabel="Đang mở…">
              Tải tệp CSV
            </Button>
          ) : null}
          {active ? (
            <Button size="sm" onClick={state.cancel} pending={state.cancelling} pendingLabel="Đang hủy…">
              Hủy
            </Button>
          ) : null}
          {state.stalled ? (
            <Button size="sm" onClick={state.recheck}>
              Kiểm tra lại
            </Button>
          ) : null}
        </ButtonRow>
        {path && job.expires_at ? <p className="hint">Tải được đến {fdt(job.expires_at)}.</p> : null}
        {state.stalled ? <p>{REPORT_MESSAGES.exportStalled}</p> : null}
      </Callout>
      {failure || message ? (
        <Callout tone="bad" role="alert">
          {failure ? <p>{failure}</p> : null}
          {message ? <p>{message}</p> : null}
        </Callout>
      ) : null}
    </>
  );
}
