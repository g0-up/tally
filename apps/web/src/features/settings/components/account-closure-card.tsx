import { useState } from "react";
import { RequireRecentAuth } from "../../../app/guards/require-recent-auth";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { KeyValueGrid, type KeyValueItem } from "../../../shared/ui/key-value-grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { CANCELLABLE_CLOSURE, CLOSURE_EXPORT_ATTEMPTS, closureRequestSchema, useAccountClosure, type ClosureRequest, type ClosureView } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { CLOSURE_STATUS, EXPORT_STATUS } from "../labels";
import { LoadError } from "./load-error";

export const CLOSURE_REQUEST_LABEL = "Yêu cầu đóng tài khoản";
export const CLOSURE_RESEND_LABEL = "Gửi lại liên kết";
export const CLOSURE_CANCEL_LABEL = "Hủy yêu cầu đóng";
export const CLOSURE_LINK_SENT = "Đã gửi liên kết xác nhận đóng tài khoản tới email chủ cửa hàng.";
export const CLOSURE_CANCELLED = "Đã hủy yêu cầu đóng tài khoản.";
export const CLOSURE_TEXT =
  "Đóng tài khoản không xóa dữ liệu. Sau khi xác nhận qua email, cửa hàng vẫn hoạt động 30 ngày và có thể hủy trong thời gian này. Hết thời gian chờ, cửa hàng chuyển sang chỉ xem, hệ thống tạo bản xuất cuối và gửi liên kết tải về qua email rồi mới đóng.";

function closureItems(view: ClosureView, request: ClosureRequest): KeyValueItem[] {
  const status = CLOSURE_STATUS[request.status];
  const items: KeyValueItem[] = [
    { label: "Trạng thái", value: <StatusPill {...status} /> },
    { label: "Yêu cầu lúc", value: fdt(request.created_at) },
    { label: "Xác nhận lúc", value: request.verified_at ? fdt(request.verified_at) : "Chưa xác nhận" },
    { label: "Hết thời gian chờ", value: fd(request.waiting_until) },
  ];
  if (request.legal_hold) items.push({ label: "Giữ dữ liệu pháp lý", value: "Đang giữ: chưa đóng khi hết thời gian chờ" });
  if (view.export) {
    const exp = EXPORT_STATUS[view.export.status];
    items.push({
      label: view.export.final ? "Bản xuất cuối" : "Bản xuất",
      value: (
        <>
          <StatusPill {...exp} />
          {view.export.failed_attempts > 0 ? ` Lần lỗi ${view.export.failed_attempts}/${CLOSURE_EXPORT_ATTEMPTS}` : null}
          {view.export.expires_at ? ` · tải được đến ${fdt(view.export.expires_at)}` : null}
        </>
      ),
    });
  }
  return items;
}

/** Asking to close, or sending the e-mailed link again while it waits; both need the password. */
function RequestClosure({ resend }: { resend: boolean }) {
  const client = useApiClient();
  const [confirm, setConfirm] = useState(false);
  const ask = useCommit({
    mutationFn: () => client.post("/account-closure", undefined, { schema: closureRequestSchema }),
    success: () => CLOSURE_LINK_SENT,
    invalidate: [settingsKeys.closure()],
    after: () => setConfirm(false),
  });

  if (resend) {
    return (
      <Button size="sm" pending={ask.pending} pendingLabel="Đang gửi…" onClick={() => void ask.run()}>
        {CLOSURE_RESEND_LABEL}
      </Button>
    );
  }
  return (
    <>
      <Button
        variant="danger"
        size="sm"
        onClick={() => {
          ask.resetKey();
          setConfirm(true);
        }}
      >
        {CLOSURE_REQUEST_LABEL}
      </Button>
      <CommitDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title={CLOSURE_REQUEST_LABEL}
        sub="Hệ thống gửi liên kết xác nhận tới email chủ cửa hàng. Yêu cầu chỉ có hiệu lực sau khi bấm liên kết đó."
        onSubmit={() => void ask.run()}
        submitLabel={CLOSURE_REQUEST_LABEL}
        submitVariant="danger"
        pending={ask.pending}
      >
        <p>{CLOSURE_TEXT}</p>
      </CommitDialog>
    </>
  );
}

function CancelClosure() {
  const client = useApiClient();
  const [confirm, setConfirm] = useState(false);
  const cancel = useCommit({
    mutationFn: () => client.post("/account-closure:cancel", undefined, { schema: closureRequestSchema }),
    success: () => CLOSURE_CANCELLED,
    invalidate: [settingsKeys.closure()],
    after: () => setConfirm(false),
  });
  return (
    <>
      <Button
        size="sm"
        onClick={() => {
          cancel.resetKey();
          setConfirm(true);
        }}
      >
        {CLOSURE_CANCEL_LABEL}
      </Button>
      <CommitDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title={CLOSURE_CANCEL_LABEL}
        onSubmit={() => void cancel.run()}
        submitLabel={CLOSURE_CANCEL_LABEL}
        pending={cancel.pending}
        cancelLabel="Giữ yêu cầu"
      >
        <p>Cửa hàng tiếp tục hoạt động bình thường. Muốn đóng lại phải gửi yêu cầu mới.</p>
      </CommitDialog>
    </>
  );
}

function ClosureActions({ request }: { request: ClosureRequest | null }) {
  const canAsk = request === null || request.status === "cancelled";
  const canResend = request?.status === "requested";
  const canCancel = request !== null && CANCELLABLE_CLOSURE.has(request.status);
  return (
    <>
      {canCancel ? (
        <ButtonRow style={{ marginTop: 12 }}>
          <CancelClosure />
        </ButtonRow>
      ) : null}
      {canAsk || canResend ? (
        <div style={{ marginTop: 12 }}>
          <RequireRecentAuth text={canResend ? "Gửi lại liên kết xác nhận cần xác nhận lại mật khẩu." : "Đóng tài khoản cần xác nhận lại mật khẩu."}>
            <ButtonRow>
              <RequestClosure resend={canResend} />
            </ButtonRow>
          </RequireRecentAuth>
        </div>
      ) : null}
    </>
  );
}

/** "Đóng tài khoản": the closure request, its wait and its final export. */
export function AccountClosureCard() {
  const closure = useAccountClosure();
  return (
    <Card title="Đóng tài khoản">
      <p className="hint">{CLOSURE_TEXT}</p>
      {closure.isPending ? (
        <ListLoading />
      ) : closure.error ? (
        <LoadError what="yêu cầu đóng tài khoản" error={closure.error} />
      ) : (
        <>
          {closure.data.request ? <KeyValueGrid items={closureItems(closure.data, closure.data.request)} /> : null}
          <ClosureActions request={closure.data.request} />
        </>
      )}
    </Card>
  );
}
