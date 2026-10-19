import { useLayoutEffect, useRef, useState } from "react";
import { errorMessage } from "../../../../shared/api/api-error";
import { focusLost, mainFocusTarget } from "../../../../shared/hooks/use-return-focus";
import { vnd } from "../../../../shared/lib/format";
import { Button } from "../../../../shared/ui/button";
import { Callout } from "../../../../shared/ui/callout";
import { CommitDialog } from "../../../../shared/ui/commit-dialog";
import { toast } from "../../../../shared/ui/toast-store";
import type { OutboxEntry } from "../offline-db";
import { syncErrorText } from "../sync-errors";

export interface BlockedSalesCalloutProps {
  blocked: readonly OutboxEntry[];
  onRequeue: (offlineClientId: string) => Promise<void>;
  onDrop: (offlineClientId: string) => Promise<void>;
}

const reasonOf = (e: OutboxEntry) => syncErrorText(e.exception?.error_code, e.exception?.error_detail);

/**
 * Offline sales the server refused in a way resending does not fix. They no longer hold up the
 * queue; each waits on this device until the cashier sends it again ("Gửi lại") or removes it.
 * Each button names its sale; only the row being sent shows progress, and its button stays
 * focusable while it works. A failed send hands focus back to that button if it moved away, and a
 * sale that leaves the list passes it to the next sale, else to the page.
 */
export function BlockedSalesCallout({ blocked, onRequeue, onDrop }: BlockedSalesCalloutProps) {
  const [dropping, setDropping] = useState<OutboxEntry | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const listRef = useRef<HTMLUListElement>(null);
  // The sale just acted on, and whether it is leaving the list (handled) or staying (the action failed).
  const refocus = useRef<{ id: string; leaves: boolean } | null>(null);
  const firstAction = () => listRef.current?.querySelector<HTMLButtonElement>("button:not(:disabled)");

  useLayoutEffect(() => {
    const after = refocus.current;
    if (after === null || busyId !== null) return;
    if (after.leaves && blocked.some((e) => e.offline_client_id === after.id)) return;
    refocus.current = null;
    if (!focusLost()) return;
    const own = after.leaves ? null : listRef.current?.querySelector<HTMLButtonElement>(`button[data-requeue="${CSS.escape(after.id)}"]`);
    (own ?? firstAction() ?? mainFocusTarget())?.focus();
  }, [blocked, busyId]);

  if (blocked.length === 0 && dropping === null) return null;

  const busy = busyId !== null;
  const act = async (entry: OutboxEntry, work: () => Promise<void>, failure: string) => {
    setBusyId(entry.offline_client_id);
    try {
      await work();
      refocus.current = { id: entry.offline_client_id, leaves: true };
      return true;
    } catch (e) {
      refocus.current = { id: entry.offline_client_id, leaves: false };
      toast.error(`${failure}: ${errorMessage(e)}`);
      return false;
    } finally {
      setBusyId(null);
    }
  };
  const drop = async (entry: OutboxEntry) => {
    if (await act(entry, () => onDrop(entry.offline_client_id), `Không xóa được ${entry.label}`)) {
      setDropping(null);
      toast.success(`Đã xóa ${entry.label} khỏi máy. Đối chiếu lại ${vnd(entry.total)} tiền đã thu.`);
    }
  };

  return (
    <>
      {blocked.length > 0 ? (
        <Callout tone="bad" role="alert" style={{ marginBottom: 16 }}>
          <b>Giao dịch ngoại tuyến không đồng bộ được ({blocked.length}):</b> máy chủ từ chối, gửi lại tự động không giúp được. Các giao dịch khác vẫn đồng bộ bình thường.
          <ul ref={listRef} style={{ margin: "8px 0 0", paddingLeft: 18 }}>
            {blocked.map((e) => {
              const sending = busyId === e.offline_client_id && dropping === null;
              return (
                <li key={e.offline_client_id} style={{ marginBottom: 4 }}>
                  <b>{e.label}</b> · {vnd(e.total)}: {reasonOf(e)}{" "}
                  <Button
                    variant="line"
                    size="sm"
                    disabled={busy}
                    pending={sending}
                    pendingLabel="Đang gửi…"
                    aria-label={sending ? undefined : `Gửi lại ${e.label}`}
                    data-requeue={e.offline_client_id}
                    onClick={() => void act(e, () => onRequeue(e.offline_client_id), `Không gửi lại được ${e.label}`)}
                  >
                    Gửi lại
                  </Button>{" "}
                  <Button variant="line" size="sm" disabled={busy} aria-label={`Xóa ${e.label} khỏi máy`} onClick={() => setDropping(e)}>
                    Xóa khỏi máy
                  </Button>
                </li>
              );
            })}
          </ul>
        </Callout>
      ) : null}
      {dropping !== null ? (
        <CommitDialog
          open
          onClose={() => setDropping(null)}
          title={`Xóa ${dropping.label} khỏi máy?`}
          onSubmit={() => void drop(dropping)}
          submitLabel="Xóa khỏi máy"
          submitVariant="danger"
          pending={busy}
          returnFocusFallback={firstAction}
        >
          <p>
            Giao dịch <b>{dropping.label}</b> ({vnd(dropping.total)}) chưa được ghi sổ và sẽ không bao giờ được đồng bộ sau khi xóa. Tiền đã thu của khách vẫn nằm trong két:
            đối chiếu và lập chứng từ bằng tay nếu cần.
          </p>
          <p>Lý do máy chủ từ chối: {reasonOf(dropping)}</p>
        </CommitDialog>
      ) : null}
    </>
  );
}
