import { fd, num } from "../../../shared/lib/format";
import { Card } from "../../../shared/ui/card";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import type { Lot } from "../../inventory";

/** Prototype "Tồn giỏ thành phẩm theo lô": kit lots holding stock, with their sell-by date. */
export function FinishedLotsCard({ lots }: { lots: readonly Lot[] }) {
  const columns: Column<Lot>[] = [
    {
      key: "lot",
      header: "Lô giỏ",
      cell: (l) => (
        <>
          <span className="doc">{l.lot_no}</span>
          <CellSub>{l.sku_name}</CellSub>
        </>
      ),
    },
    { key: "exp", header: "Hạn bán", cell: (l) => fd(l.expiry_date) },
    { key: "qty", header: "Tồn", numeric: true, cell: (l) => num(l.qty) },
  ];
  return (
    <Card title="Tồn giỏ thành phẩm theo lô">
      <DataTable caption="Tồn giỏ thành phẩm theo lô" columns={columns} rows={lots} rowKey={(l) => l.id} flat empty="Chưa có lô giỏ." />
    </Card>
  );
}
