import { useState } from "react";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Stack } from "../../../shared/ui/grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useSetFeatureFlag } from "../api/mutations";
import { useFeatureFlagChanges, useFeatureFlags } from "../api/queries";
import type { FeatureFlag, FeatureFlagChange } from "../api/schemas";
import { LoadError } from "../components/load-error";
import { TypedConfirmDialog } from "../components/typed-confirm-dialog";
import { SetFlagDialog } from "../containers/set-flag-dialog";
import { changeText, flagRows, onOff, type FlagRow } from "../lib/flags";

export const FLAGS_TITLE = "Cờ tính năng";
export const FLAGS_TABLE = "Danh sách cờ tính năng";
export const FLAG_HISTORY_TITLE = "Lịch sử thay đổi";

/** Cờ tính năng (/admin/feature-flags): global values, per-store overrides and their change history. */
export default function FeatureFlagsPage() {
  const flags = useFeatureFlags();
  const [adding, setAdding] = useState(false);
  const [turningOff, setTurningOff] = useState<string | null>(null);
  const [historyKey, setHistoryKey] = useState<string | null>(null);
  // The flag being switched on from its row: only that button shows the wait.
  const [turningOn, setTurningOn] = useState<string | null>(null);

  const set = useSetFeatureFlag({
    success: (f) => (
      <>
        Đã {onOff(f.enabled).toLowerCase()} cờ <b>{f.key}</b> cho toàn hệ thống.
      </>
    ),
    after: () => setTurningOff(null),
  });

  const columns: Column<FlagRow>[] = [
    { key: "key", header: "Mã cờ", cell: (r) => <code>{r.key}</code> },
    {
      key: "global",
      header: "Giá trị chung",
      cell: (r) =>
        r.global ? (
          <>
            {onOff(r.global.enabled)}
            <CellSub>Đổi lúc {fdt(r.global.updated_at)}</CellSub>
          </>
        ) : (
          "Chưa đặt (tắt)"
        ),
    },
    {
      key: "stores",
      header: "Giá trị riêng của cửa hàng",
      cell: (r) => (r.stores.length === 0 ? "—" : r.stores.map((f) => `${scopeName(f)}: ${onOff(f.enabled)}`).join(", ")),
    },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (r) => {
        const on = r.global?.enabled ?? false;
        return (
          <ButtonRow>
            <Button
              size="sm"
              variant={on ? "danger" : "line"}
              aria-label={`${on ? "Tắt" : "Bật"} ${r.key} cho toàn hệ thống`}
              pending={set.pending && turningOn === r.key}
              pendingLabel="Đang bật…"
              disabled={set.pending}
              onClick={() => {
                if (on) return setTurningOff(r.key);
                setTurningOn(r.key);
                void set.run({ key: r.key, tenant_id: null, enabled: true });
              }}
            >
              {on ? "Tắt" : "Bật"}
            </Button>
            <Button size="sm" aria-label={`Lịch sử ${r.key}`} aria-pressed={historyKey === r.key} onClick={() => setHistoryKey(r.key)}>
              Lịch sử
            </Button>
          </ButtonRow>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Quản trị nền tảng"
        title={FLAGS_TITLE}
        lead="Giá trị riêng của một cửa hàng được ưu tiên hơn giá trị chung; cờ chưa đặt được coi là tắt."
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            Đặt cờ
          </Button>
        }
      />
      <Stack>
        {flags.isError ? <LoadError what="cờ tính năng" error={flags.error} onRetry={() => void flags.refetch()} /> : null}
        {flags.isPending ? <ListLoading /> : flags.data ? <DataTable caption={FLAGS_TABLE} columns={columns} rows={flagRows(flags.data)} rowKey={(r) => r.key} empty="Chưa có cờ tính năng nào." /> : null}
        {historyKey !== null ? <FlagHistory flagKey={historyKey} /> : null}
      </Stack>
      {adding ? <SetFlagDialog onClose={() => setAdding(false)} /> : null}
      {turningOff !== null ? (
        <TypedConfirmDialog
          open
          onClose={() => setTurningOff(null)}
          title={`Tắt cờ ${turningOff} cho toàn hệ thống`}
          phrase={turningOff}
          phraseLabel="mã cờ"
          submitLabel="Tắt cờ"
          pending={set.pending}
          onConfirm={() => void set.run({ key: turningOff, tenant_id: null, enabled: false })}
        >
          <p>Mọi cửa hàng không có giá trị riêng sẽ mất tính năng này ngay.</p>
        </TypedConfirmDialog>
      ) : null}
    </>
  );
}

/** "Toàn hệ thống" for the global value, else the store's slug. */
function scopeName(f: Pick<FeatureFlag, "tenant_id" | "tenant_slug">): string {
  if (f.tenant_id === null) return "Toàn hệ thống";
  return f.tenant_slug ?? f.tenant_id;
}

function FlagHistory({ flagKey }: { flagKey: string }) {
  const changes = useFeatureFlagChanges(flagKey);
  const columns: Column<FeatureFlagChange>[] = [
    { key: "at", header: "Lúc", cell: (c) => fdt(c.created_at) },
    { key: "scope", header: "Phạm vi", cell: (c) => scopeName(c) },
    { key: "value", header: "Giá trị", cell: (c) => changeText(c.enabled) },
    { key: "admin", header: "Người đổi", cell: (c) => c.admin_full_name },
  ];
  return (
    <Card title={`${FLAG_HISTORY_TITLE} · ${flagKey}`}>
      {changes.isError ? <LoadError what="lịch sử cờ" error={changes.error} onRetry={() => void changes.refetch()} /> : null}
      {changes.isPending ? <ListLoading /> : changes.data ? <DataTable flat caption={`${FLAG_HISTORY_TITLE} ${flagKey}`} columns={columns} rows={changes.data} rowKey={(c) => c.id} empty="Chưa có thay đổi nào." /> : null}
    </Card>
  );
}
