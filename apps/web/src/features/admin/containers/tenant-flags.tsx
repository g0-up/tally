import { useState } from "react";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { ListLoading } from "../../../shared/ui/list-footer";
import { useRemoveTenantFlag, useSetFeatureFlag } from "../api/mutations";
import { useFeatureFlags } from "../api/queries";
import { LoadError } from "../components/load-error";
import { TypedConfirmDialog } from "../components/typed-confirm-dialog";
import { effectiveFor, flagRows, onOff, type FlagRow } from "../lib/flags";
import { SetFlagDialog, type FlagStore } from "./set-flag-dialog";

export const TENANT_FLAGS_TITLE = "Cờ tính năng của cửa hàng";
export const REMOVE_OVERRIDE_LABEL = "Gỡ giá trị riêng";

/** Every flag key with the value this store gets, a switch for its own value and its removal. */
export function TenantFlags({ store }: { store: FlagStore }) {
  const flags = useFeatureFlags();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<FlagRow | null>(null);
  // The flag whose own value is being switched: only that row's button shows the wait.
  const [switching, setSwitching] = useState<string | null>(null);
  const set = useSetFeatureFlag({
    success: (f) => (
      <>
        Đã {onOff(f.enabled).toLowerCase()} cờ <b>{f.key}</b> cho {store.slug}.
      </>
    ),
  });
  const remove = useRemoveTenantFlag(store.id, {
    success: ({ key }) => (
      <>
        Đã gỡ giá trị riêng của cờ <b>{key}</b> cho {store.slug}.
      </>
    ),
    after: () => setRemoving(null),
  });
  const rows = flags.data ? flagRows(flags.data) : [];
  const busy = set.pending || remove.pending;

  const columns: Column<FlagRow>[] = [
    { key: "key", header: "Mã cờ", cell: (r) => <code>{r.key}</code> },
    { key: "global", header: "Giá trị chung", cell: (r) => (r.global ? onOff(r.global.enabled) : "—") },
    {
      key: "store",
      header: "Cửa hàng này",
      cell: (r) => {
        const v = effectiveFor(r, store.id);
        return `${onOff(v.enabled)}${v.own ? " (riêng)" : " (theo giá trị chung)"}`;
      },
    },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (r) => {
        const v = effectiveFor(r, store.id);
        const next = !v.enabled;
        return (
          <ButtonRow>
            <Button
              size="sm"
              aria-label={`${onOff(next)} ${r.key} cho ${store.slug}`}
              pending={set.pending && switching === r.key}
              pendingLabel={next ? "Đang bật…" : "Đang tắt…"}
              disabled={busy}
              onClick={() => {
                setSwitching(r.key);
                void set.run({ key: r.key, tenant_id: store.id, enabled: next });
              }}
            >
              {onOff(next)}
            </Button>
            {v.own ? (
              <Button size="sm" variant="danger" aria-label={`${REMOVE_OVERRIDE_LABEL} ${r.key} cho ${store.slug}`} disabled={busy} onClick={() => setRemoving(r)}>
                {REMOVE_OVERRIDE_LABEL}
              </Button>
            ) : null}
          </ButtonRow>
        );
      },
    },
  ];

  return (
    <Card
      title={TENANT_FLAGS_TITLE}
      actions={
        <Button size="sm" onClick={() => setAdding(true)}>
          Đặt cờ khác
        </Button>
      }
    >
      {flags.isError ? <LoadError what="cờ tính năng" error={flags.error} onRetry={() => void flags.refetch()} /> : null}
      {flags.isPending ? <ListLoading /> : flags.data ? <DataTable flat caption={TENANT_FLAGS_TITLE} columns={columns} rows={rows} rowKey={(r) => r.key} empty="Chưa có cờ tính năng nào." /> : null}
      {adding ? <SetFlagDialog store={store} onClose={() => setAdding(false)} /> : null}
      {removing ? (
        <TypedConfirmDialog
          open
          onClose={() => setRemoving(null)}
          title={`${REMOVE_OVERRIDE_LABEL} · ${removing.key}`}
          phrase={removing.key}
          phraseLabel="mã cờ"
          submitLabel={REMOVE_OVERRIDE_LABEL}
          pending={remove.pending}
          onConfirm={() => void remove.run(removing.key)}
        >
          <p>
            {store.slug} sẽ theo giá trị chung của cờ này: {onOff(removing.global?.enabled ?? false)}.
          </p>
        </TypedConfirmDialog>
      ) : null}
    </Card>
  );
}
