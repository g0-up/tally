import { businessDateOf } from "../../../shared/lib/dates";
import { fd, num } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button } from "../../../shared/ui/button";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { baseUnitName, type SkuListItem } from "../../catalog";
import { releasable } from "../api/adapters";
import type { Reservation } from "../schemas";

export interface ReservationTableProps {
  rows: readonly Reservation[];
  skus: ReadonlyMap<string, SkuListItem> | undefined;
  onRelease: (reservation: Reservation) => void;
}

/** Prototype `kho.giu-hang`: open holds per SKU with the document that holds them. */
export function ReservationTable({ rows, skus, onRelease }: ReservationTableProps) {
  const columns: Column<Reservation>[] = [
    { key: "source", header: "Nguồn", cell: (r) => (r.source_doc_no ? <DocNo value={r.source_doc_no} /> : <Badge alt>Giữ thủ công</Badge>) },
    { key: "sku", header: "Hàng", cell: (r) => skus?.get(r.sku_id)?.display_name ?? "—" },
    {
      key: "qty",
      header: "Lượng giữ",
      numeric: true,
      cell: (r) => {
        const sku = skus?.get(r.sku_id);
        return `${num(r.open_qty)}${sku ? ` ${baseUnitName(sku)}` : ""}`;
      },
    },
    { key: "date", header: "Từ ngày", cell: (r) => fd(businessDateOf(r.created_at)) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (r) =>
        releasable(r) ? (
          <Button variant="danger" size="sm" onClick={() => onRelease(r)}>
            Giải phóng
          </Button>
        ) : null,
    },
  ];
  return <DataTable caption="Giữ hàng" columns={columns} rows={rows} rowKey={(r) => r.id} empty="Không có lượng giữ." />;
}
