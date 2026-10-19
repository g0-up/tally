import { useState } from "react";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { CellText } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import { revokedSessionsSchema, useSessions, type Session } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { LoadError } from "./load-error";

export const DEVICE_SIGNED_OUT = "Đã đăng xuất thiết bị.";
export const REVOKE_OTHERS_LABEL = "Đăng xuất các thiết bị khác";

/** Answer of "Đăng xuất các thiết bị khác". */
function othersSignedOutText(revoked: number): string {
  return revoked > 0 ? `Đã đăng xuất ${revoked} thiết bị khác.` : "Không còn thiết bị khác đang đăng nhập.";
}

/**
 * "Thiết bị đang đăng nhập": the owner's live sessions; any other device can be signed out, or all
 * of them at once after a confirmation. This device stays signed in either way.
 */
export function DevicesCard() {
  const client = useApiClient();
  const sessions = useSessions();
  const [signingOut, setSigningOut] = useState<string | null>(null);
  const revoke = useCommit({
    mutationFn: (id: string) => client.delete(`/sessions/${encodeURIComponent(id)}`),
    success: () => DEVICE_SIGNED_OUT,
    invalidate: [settingsKeys.sessions()],
  });
  const [confirmAll, setConfirmAll] = useState(false);
  const revokeAll = useCommit({
    mutationFn: () => client.post("/sessions:revoke-all", undefined, { schema: revokedSessionsSchema }),
    success: (r) => othersSignedOutText(r.revoked),
    invalidate: [settingsKeys.sessions()],
    after: () => setConfirmAll(false),
  });
  const others = sessions.data?.filter((s) => !s.current).length ?? 0;

  const columns: ReadonlyArray<Column<Session>> = [
    { key: "device", header: "Thiết bị", cell: (s) => <CellText main={s.device_label} sub={s.ip ?? undefined} /> },
    { key: "seen", header: "Lần cuối", cell: (s) => fdt(s.last_seen_at) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (s) =>
        s.current ? (
          <StatusPill label="Thiết bị này" />
        ) : (
          <Button
            variant="danger"
            size="sm"
            aria-label={`Đăng xuất ${s.device_label}`}
            pending={revoke.pending && signingOut === s.id}
            disabled={revoke.pending}
            onClick={() => {
              setSigningOut(s.id);
              void revoke.run(s.id);
            }}
          >
            Đăng xuất
          </Button>
        ),
    },
  ];

  return (
    <Card title="Thiết bị đang đăng nhập">
      {sessions.isPending ? (
        <ListLoading />
      ) : sessions.error ? (
        <LoadError what="danh sách thiết bị" error={sessions.error} />
      ) : (
        <>
          <DataTable caption="Thiết bị đang đăng nhập" columns={columns} rows={sessions.data} rowKey={(s) => s.id} flat />
          {others > 0 ? (
            <ButtonRow style={{ marginTop: 12 }}>
              <Button
                variant="danger"
                size="sm"
                disabled={revoke.pending}
                onClick={() => {
                  revokeAll.resetKey();
                  setConfirmAll(true);
                }}
              >
                {REVOKE_OTHERS_LABEL}
              </Button>
            </ButtonRow>
          ) : null}
        </>
      )}
      <CommitDialog
        open={confirmAll}
        onClose={() => setConfirmAll(false)}
        title={`${REVOKE_OTHERS_LABEL}?`}
        sub="Các thiết bị đó phải đăng nhập lại ở lần dùng tiếp theo. Thiết bị này vẫn giữ đăng nhập."
        onSubmit={() => void revokeAll.run()}
        submitLabel={REVOKE_OTHERS_LABEL}
        submitVariant="danger"
        pending={revokeAll.pending}
      >
        <p>Đăng xuất {others} thiết bị khác đang đăng nhập vào tài khoản này?</p>
      </CommitDialog>
    </Card>
  );
}
