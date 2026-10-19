import { useState } from "react";
import { RequireRecentAuth } from "../../../app/guards/require-recent-auth";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit, type CommitResult } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { StatusPill } from "../../../shared/ui/status-pill";
import { dataExportSchema, useFullExportJob, useRecentFullExport, type DataExport } from "../api/account";
import { EXPORT_STATUS } from "../labels";
import { LoadError } from "./load-error";

export const DATA_EXPORT_TEXT = "Gồm danh mục, đối tác, chứng từ, sổ kho, công nợ, quỹ, nhật ký và danh sách tệp đính kèm, giữ nguyên mã và quan hệ.";
export const DATA_EXPORT_QUEUED = "Đã đưa bản xuất vào hàng chờ.";

function ExportJobStatus({ id }: { id: string }) {
  const job = useFullExportJob(id);
  if (job.isPending) {
    return (
      <p className="hint" role="status">
        Đang tạo bản xuất…
      </p>
    );
  }
  if (job.error) return <LoadError what="trạng thái bản xuất" error={job.error} />;
  const status = EXPORT_STATUS[job.data.status];
  // The state is announced as it moves; a failure is an alert beside it, not a polite update.
  return (
    <>
      <div role="status" aria-label="Bản xuất dữ liệu" style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", marginTop: 10 }}>
        <StatusPill {...status} />
        {job.data.status === "succeeded" && job.data.download_path ? (
          <>
            <a className="btn btn-primary btn-sm" href={job.data.download_path} download>
              Tải về
            </a>
            {job.data.expires_at ? <span className="hint">Tải được đến {fdt(job.data.expires_at)}.</span> : null}
          </>
        ) : null}
      </div>
      {job.data.status === "failed" ? (
        <Callout tone="bad" role="alert">
          Không tạo được bản xuất. Bấm Tạo bản xuất để thử lại.
        </Callout>
      ) : null}
    </>
  );
}

function ExportControls({ request, jobId }: { request: CommitResult<DataExport, void>; jobId: string | null }) {
  // Until an export is started here, the newest one the server still has running or downloadable.
  const recent = useRecentFullExport(jobId === null);
  const shownId = jobId ?? recent.data ?? null;
  return (
    <>
      <ButtonRow>
        <Button variant="navy" size="sm" pending={request.pending} pendingLabel="Đang gửi…" onClick={() => void request.run()}>
          Tạo bản xuất
        </Button>
      </ButtonRow>
      {shownId ? <ExportJobStatus id={shownId} /> : null}
    </>
  );
}

/**
 * "Xuất toàn bộ dữ liệu": a zip of every record, so the store is never locked in. The request and
 * its Idempotency-Key live outside the password gate, so a press after a lost answer, or after the
 * gate closed and opened again, resends the same intent instead of queuing a second export. After a
 * reload the card shows the last export still being written or downloadable.
 */
export function DataExportCard() {
  const client = useApiClient();
  const [jobId, setJobId] = useState<string | null>(null);
  const request = useCommit({
    mutationFn: (_, { idempotencyKey }) => client.post("/data-exports", undefined, { schema: dataExportSchema, idempotencyKey }),
    success: () => DATA_EXPORT_QUEUED,
    after: (r) => setJobId(r.id),
  });
  return (
    <Card title="Xuất toàn bộ dữ liệu">
      <p>{DATA_EXPORT_TEXT}</p>
      <p className="hint">Bản xuất là tệp zip, mỗi bảng một tệp CSV, tạo trong nền và tải được trong 7 ngày. Cửa hàng đang chỉ xem vẫn xuất được. Thao tác được ghi nhật ký.</p>
      <RequireRecentAuth text="Xuất toàn bộ dữ liệu cần xác nhận lại mật khẩu.">
        <ExportControls request={request} jobId={jobId} />
      </RequireRecentAuth>
    </Card>
  );
}
