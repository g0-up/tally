import { fd, num } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { LinkButton } from "../../../shared/ui/link-button";
import type { ColorDirectory } from "../../catalog";
import type { AssemblyDetail, Posting } from "../schemas";

export interface GenealogyRow {
  order: AssemblyDetail;
  posting: Posting;
}

export interface GenealogyTableProps {
  rows: readonly GenealogyRow[];
  colors: ColorDirectory;
  onOpen: (order: AssemblyDetail) => void;
}

/** Prototype "Truy xuất lô thành phần": each posting's kit lot back to the component lots it used. */
export function GenealogyTable({ rows, colors, onOpen }: GenealogyTableProps) {
  const columns: Column<GenealogyRow>[] = [
    { key: "order", header: "Lần đóng", cell: (r) => <LinkButton onClick={() => onOpen(r.order)}>{r.order.doc_no}</LinkButton> },
    { key: "date", header: "Ngày", cell: (r) => fd(r.posting.business_date) },
    { key: "lot", header: "Lô giỏ", cell: (r) => r.posting.output_lot_no },
    { key: "qty", header: "Số giỏ", numeric: true, cell: (r) => num(r.posting.produced_qty) },
    {
      key: "parts",
      header: "Lô thành phần",
      cell: (r) =>
        r.posting.items.map((u) => (
          <div key={u.id}>
            {r.order.items.find((i) => i.sku_id === u.sku_id)?.sku_name ?? ""}
            {u.color_id ? (
              <>
                {" "}
                <ColorChip color={colors.codeOf(u.color_id)} />
              </>
            ) : null}
            : {u.lot_no}
          </div>
        )),
    },
    { key: "exp", header: "Hạn bán giỏ", cell: (r) => fd(r.posting.output_expiry) },
  ];
  return <DataTable caption="Truy xuất lô thành phần" columns={columns} rows={rows} rowKey={(r) => r.posting.id} empty="Chưa có lần đóng." />;
}
