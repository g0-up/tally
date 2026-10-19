import { useId, useLayoutEffect, useRef, useState } from "react";
import { focusLost } from "../../../../shared/hooks/use-return-focus";
import { fdt } from "../../../../shared/lib/dates";
import { vnd } from "../../../../shared/lib/format";
import { Button, ButtonRow } from "../../../../shared/ui/button";
import { CommitDialog } from "../../../../shared/ui/commit-dialog";
import { DataTable } from "../../../../shared/ui/data-table/data-table";
import { DetailDrawer } from "../../../../shared/ui/detail-drawer";
import type { ParkedSaleView } from "../parked-sales";
import { useDiscardSyncException, useResolveSyncException, type ParkedSaleRef } from "../sync-exception-mutations";

export const NOT_ACTIONABLE_TITLE = "Chưa đọc được hàng đợi trên máy chủ. Kết nối mạng rồi mở lại để xử lý.";

export interface SyncExceptionDrawerProps {
  open: boolean;
  onClose: () => void;
  parked: readonly ParkedSaleView[];
  tenantId: string | null;
}

function refOf(p: ParkedSaleView): ParkedSaleRef | null {
  return p.id !== null && p.body !== null ? { id: p.id, offlineClientId: p.offlineClientId, label: p.label, body: p.body } : null;
}

function LinesCell({ sale }: { sale: ParkedSaleView }) {
  if (sale.lines === null) return <>{sale.lineCount} dòng</>;
  return (
    <>
      {sale.lines.map((l, i) => (
        <div key={i}>
          {l.name} × {l.qty} {l.unit_name}
        </div>
      ))}
    </>
  );
}

/**
 * The exception queue (/sales/pos?exceptions=open): offline sales the server parked, with when they
 * were made, what was sold, the cash taken and why. "Ghi sổ lại" posts the sale as sent (an owner
 * exception may be asked for); "Bỏ qua giao dịch" closes it without a document, after a confirmation.
 * Each action names its sale, and a sale that cannot be handled yet says why on screen. The button
 * being posted stays focusable while it works; a failure hands focus back to it if it moved away,
 * and a handled sale leaving the queue passes it to the next sale's action, else to the drawer.
 */
export function SyncExceptionDrawer({ open, onClose, parked, tenantId }: SyncExceptionDrawerProps) {
  const [busy, setBusy] = useState<string | null>(null);
  const [discarding, setDiscarding] = useState<ParkedSaleRef | null>(null);
  const resolve = useResolveSyncException(tenantId, () => undefined);
  const discard = useDiscardSyncException(tenantId, () => setDiscarding(null));
  const reasonId = useId();
  const tableRef = useRef<HTMLDivElement>(null);
  const firstAction = () => tableRef.current?.querySelector<HTMLButtonElement>("button:not(:disabled)");

  // The sale just acted on, and whether it is leaving the queue (handled) or staying (the post failed).
  const refocus = useRef<{ id: string; leaves: boolean } | null>(null);
  useLayoutEffect(() => {
    const after = refocus.current;
    if (after === null || busy !== null) return;
    if (after.leaves && parked.some((p) => p.id === after.id)) return;
    refocus.current = null;
    if (!focusLost()) return;
    const own = after.leaves ? null : tableRef.current?.querySelector<HTMLButtonElement>(`button[data-resolve="${CSS.escape(after.id)}"]`);
    (own ?? firstAction() ?? tableRef.current?.closest<HTMLElement>(".drw"))?.focus();
  }, [parked, busy]);

  // One intent per parked sale: retrying the same sale keeps its key, another sale starts a new one.
  const lastResolved = useRef<string | null>(null);
  const runResolve = async (sale: ParkedSaleRef) => {
    if (lastResolved.current !== sale.id) resolve.resetKey();
    lastResolved.current = sale.id;
    setBusy(sale.id);
    try {
      const posted = await resolve.run(sale);
      refocus.current = { id: sale.id, leaves: posted !== undefined };
    } finally {
      setBusy(null);
    }
  };
  const runDiscard = async (sale: ParkedSaleRef) => {
    if ((await discard.run(sale)) !== undefined) refocus.current = { id: sale.id, leaves: true };
  };
  const anyBlocked = parked.some((p) => refOf(p) === null);

  return (
    <>
      <DetailDrawer open={open} onClose={onClose} title={`Hàng đợi ngoại lệ (${parked.length})`} sub="Giao dịch ngoại tuyến máy chủ chưa ghi sổ. Mỗi giao dịch chỉ ghi một lần.">
        {anyBlocked ? (
          <p className="hint" id={reasonId}>
            {NOT_ACTIONABLE_TITLE}
          </p>
        ) : null}
        <div ref={tableRef}>
          <DataTable
            caption="Hàng đợi ngoại lệ"
            rows={parked}
            rowKey={(p) => p.offlineClientId}
            empty="Không có giao dịch trong hàng đợi ngoại lệ."
            columns={[
              { key: "label", header: "Mã", cell: (p) => <b>{p.label}</b> },
              { key: "time", header: "Thời gian", cell: (p) => fdt(p.createdAt) },
              { key: "lines", header: "Hàng", cell: (p) => <LinesCell sale={p} /> },
              { key: "cash", header: "Tiền mặt", numeric: true, cell: (p) => (p.cash === null ? "—" : vnd(p.cash)) },
              { key: "error", header: "Lỗi", cell: (p) => p.error },
              {
                key: "acts",
                header: "",
                className: "acts",
                cell: (p) => {
                  const ref = refOf(p);
                  const title = ref === null ? NOT_ACTIONABLE_TITLE : undefined;
                  const described = ref === null ? reasonId : undefined;
                  const posting = ref !== null && busy === ref.id;
                  return (
                    <ButtonRow>
                      <Button
                        variant="primary"
                        size="sm"
                        disabled={ref === null || (busy !== null && busy !== ref.id)}
                        pending={posting}
                        title={title}
                        aria-label={posting ? undefined : `Ghi sổ lại ${p.label}`}
                        aria-describedby={described}
                        data-resolve={p.id ?? undefined}
                        onClick={() => (ref ? void runResolve(ref) : undefined)}
                      >
                        Ghi sổ lại
                      </Button>
                      <Button
                        variant="line"
                        size="sm"
                        disabled={ref === null || busy !== null}
                        title={title}
                        aria-label={`Bỏ qua giao dịch ${p.label}`}
                        aria-describedby={described}
                        onClick={() => setDiscarding(ref)}
                      >
                        Bỏ qua giao dịch
                      </Button>
                    </ButtonRow>
                  );
                },
              },
            ]}
          />
        </div>
      </DetailDrawer>
      <CommitDialog
        open={discarding !== null}
        onClose={() => setDiscarding(null)}
        title={discarding ? `Bỏ qua ${discarding.label}?` : ""}
        onSubmit={() => (discarding ? void runDiscard(discarding) : undefined)}
        submitLabel="Bỏ qua giao dịch"
        submitVariant="danger"
        pending={discard.pending}
        returnFocusFallback={() => firstAction() ?? tableRef.current?.closest<HTMLElement>(".drw")}
      >
        <p>Giao dịch sẽ không được ghi sổ và rời hàng đợi ngoại lệ. Tiền đã thu của giao dịch này cần được đối chiếu ngoài hệ thống.</p>
      </CommitDialog>
    </>
  );
}
