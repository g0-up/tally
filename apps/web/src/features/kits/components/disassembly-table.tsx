import { dec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { StatusPill } from "../../../shared/ui/status-pill";
import { SeenMarker } from "../../inventory";
import type { Disassembly } from "../schemas";

export interface DisassemblyTableProps {
  rows: readonly Disassembly[];
  /** Kit name of an assembly order, when loaded. */
  kitName: (orderId: string) => string | undefined;
  /** A row came into view and needs its order's kit name. */
  onSeen: (id: string) => void;
  empty: string;
}

/** Value of a disassembly: every component taken out, returned or damaged, at its cost. */
const valueOf = (d: Disassembly) => d.lines.reduce((s, l) => s.plus(dec(l.return_qty).plus(l.damaged_qty).times(l.unit_cost)), dec(0));

/** Prototype `gio-qua.thao-gio`: disassemblies with what happened to the packing and their value. */
export function DisassemblyTable({ rows, kitName, onSeen, empty }: DisassemblyTableProps) {
  const columns: Column<Disassembly>[] = [
    {
      key: "doc",
      header: "Phiếu",
      cell: (d) => (
        <>
          <SeenMarker id={d.id} onSeen={onSeen} />
          <span className="doc">{d.doc_no}</span>
          <CellSub>{fd(d.business_date)}</CellSub>
        </>
      ),
    },
    {
      key: "kit",
      header: "Giỏ",
      cell: (d) => (
        <>
          {kitName(d.order_id) ?? d.order_doc_no}
          <CellSub>
            Lô {d.kit_lot_no} · {d.order_doc_no}
          </CellSub>
        </>
      ),
    },
    { key: "qty", header: "SL", numeric: true, cell: (d) => num(d.qty) },
    {
      key: "pkg",
      header: "Vật tư bao bì",
      cell: (d) => (d.lines.some((l) => dec(l.damaged_qty).gt(0)) ? <StatusPill label="Hỏng – không nhập lại" tone="warn" /> : <StatusPill label="Nhập lại" tone="ok" />),
    },
    { key: "value", header: "Giá trị", numeric: true, cell: (d) => vnd(valueOf(d)) },
  ];
  return <DataTable caption="Phiếu tháo giỏ" columns={columns} rows={rows} rowKey={(d) => d.id} empty={empty} />;
}
