import type { ColorCode } from "../../../shared/domain/color";
import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { StatusPill } from "../../../shared/ui/status-pill";
import { ADJUSTMENT_KIND_LABELS, ADJUSTMENT_STATUS_LABELS } from "../api/adapters";
import type { AdjustmentListItem } from "../schemas";

export interface AdjustmentTableProps {
  rows: readonly AdjustmentListItem[];
  reasonName: (id: string | null | undefined) => string | null;
  colorOf: (colorId: string | null | undefined) => ColorCode | "none";
  empty: string;
}

/**
 * The stock value an adjustment moved: what it brought in or took out, once when a move (into or
 * out of quarantine) took out what it brought in, or both sides when its lines went both ways.
 */
function valueCell(a: AdjustmentListItem) {
  const valueIn = dec(a.value_in);
  const valueOut = dec(a.value_out);
  if (valueIn.isZero() || valueOut.isZero() || valueIn.eq(valueOut)) return vnd(valueIn.isZero() ? valueOut : valueIn);
  return (
    <>
      +{vnd(valueIn)}
      <CellSub>−{vnd(valueOut)}</CellSub>
    </>
  );
}

/** Prototype `kho.dieu-chinh`: posted adjustments, newest first, valued at the cost they left at. */
export function AdjustmentTable({ rows, reasonName, colorOf, empty }: AdjustmentTableProps) {
  const columns: Column<AdjustmentListItem>[] = [
    {
      key: "doc",
      header: "Phiếu",
      cell: (a) => (
        <>
          <span className="doc">{a.doc_no}</span>
          <CellSub>{fd(a.business_date)}</CellSub>
        </>
      ),
    },
    { key: "kind", header: "Loại", cell: (a) => <span className="tag">{ADJUSTMENT_KIND_LABELS[a.kind]}</span> },
    {
      key: "sku",
      header: "Hàng",
      cell: (a) => (
        <>
          {a.line_preview.map((l) => (
            <div key={l.id}>
              {l.name}
              {l.variant_label ? ` · ${l.variant_label}` : ""}
              {l.color_id ? (
                <>
                  {" "}
                  <ColorChip color={colorOf(l.color_id)} />
                </>
              ) : null}
              {l.lot_no ? <CellSub>Lô {l.lot_no}</CellSub> : null}
            </div>
          ))}
          {a.line_count > a.line_preview.length ? <CellSub>và {a.line_count - a.line_preview.length} dòng khác</CellSub> : null}
        </>
      ),
    },
    { key: "qty", header: "SL", numeric: true, cell: (a) => num(a.total_base_qty) },
    { key: "reason", header: "Lý do", cell: (a) => reasonName(a.reason_id) ?? a.note ?? "" },
    { key: "value", header: "Giá trị", numeric: true, cell: valueCell },
    { key: "status", header: "Trạng thái", cell: (a) => <StatusPill label={ADJUSTMENT_STATUS_LABELS[a.status]} /> },
  ];
  return <DataTable caption="Phiếu điều chỉnh kho" columns={columns} rows={rows} rowKey={(a) => a.id} empty={empty} />;
}
