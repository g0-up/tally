import { dec } from "../../../shared/lib/decimal";
import { num } from "../../../shared/lib/format";
import { Card } from "../../../shared/ui/card";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { assemblyNet } from "../api/adapters";
import type { KitProgress } from "../schemas";

/** Kits invoices still need beyond the kits in stock. */
const toAssemble = (k: KitProgress) => {
  const need = dec(k.invoice_demand).minus(k.on_hand);
  return need.gt(0) ? need : dec(0);
};

/**
 * Prototype "Nhu cầu giỏ theo đơn": the kits open invoices ask for, what was assembled, what is in
 * stock and what still has to be assembled (one row per kit; the API sums the invoices).
 */
export function DemandCard({ rows }: { rows: readonly KitProgress[] }) {
  const columns: Column<KitProgress>[] = [
    {
      key: "kit",
      header: "Giỏ",
      cell: (k) => (
        <>
          {k.name}
          <CellSub>
            {k.sku_code} · {k.open_orders} lệnh đang mở
          </CellSub>
        </>
      ),
    },
    { key: "demand", header: "Đặt", numeric: true, cell: (k) => num(k.invoice_demand) },
    { key: "done", header: "Đã đóng", numeric: true, cell: (k) => num(assemblyNet(k)) },
    { key: "stock", header: "Tồn giỏ", numeric: true, cell: (k) => num(k.on_hand) },
    { key: "need", header: "Cần đóng thêm", numeric: true, cell: (k) => <b>{num(toAssemble(k))}</b> },
  ];
  return (
    <Card title="Nhu cầu giỏ theo đơn">
      <DataTable caption="Nhu cầu giỏ theo đơn" columns={columns} rows={rows} rowKey={(k) => k.product_id} flat empty="Chưa có đơn cần giỏ." />
    </Card>
  );
}
