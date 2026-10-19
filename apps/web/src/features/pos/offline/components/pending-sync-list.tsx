import { fdt } from "../../../../shared/lib/dates";
import { vnd } from "../../../../shared/lib/format";
import { DataTable } from "../../../../shared/ui/data-table/data-table";
import { StatusPill } from "../../../../shared/ui/status-pill";
import type { OutboxEntry } from "../offline-db";

export const PENDING_STATUS = "Chờ đồng bộ";

/** Offline sales still on this device and not sent yet: OFF number, time, total, status. */
export function PendingSyncList({ id, entries }: { id?: string; entries: readonly OutboxEntry[] }) {
  return (
    <div id={id} style={{ marginBottom: 16 }}>
      <DataTable
        caption="Giao dịch chờ đồng bộ"
        rows={entries}
        rowKey={(e) => e.offline_client_id}
        empty="Không có giao dịch chờ đồng bộ."
        columns={[
          { key: "label", header: "Mã", cell: (e) => <b>{e.label}</b> },
          { key: "time", header: "Thời gian", cell: (e) => fdt(e.sale.created_at) },
          { key: "total", header: "Tổng tiền", numeric: true, cell: (e) => vnd(e.total) },
          { key: "status", header: "Trạng thái", cell: () => <StatusPill label={PENDING_STATUS} tone="warn" /> },
        ]}
      />
    </div>
  );
}
