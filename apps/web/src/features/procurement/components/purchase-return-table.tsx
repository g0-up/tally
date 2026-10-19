import { fd, money, num } from "../../../shared/lib/format";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { useColorDirectory } from "../../catalog";
import type { PurchaseReturn } from "../schemas";

export interface PurchaseReturnTableProps {
  rows: readonly PurchaseReturn[];
  /** Name of a purchase-return reason by id. */
  reasonName: (id: string | null | undefined) => string;
}

/** Prototype `mua-hang.tra-ncc` table: one row per XT with the lots sent back and what it settled. */
export function PurchaseReturnTable({ rows, reasonName }: PurchaseReturnTableProps) {
  const colors = useColorDirectory();
  const columns: Column<PurchaseReturn>[] = [
    {
      key: "doc",
      header: "Phiếu",
      cell: (r) => (
        <>
          <span className="doc">{r.doc_no}</span>
          <CellSub>{fd(r.business_date)}</CellSub>
        </>
      ),
    },
    { key: "receipt", header: "Phiếu nhận gốc", cell: (r) => (r.receipt_doc_no ? <DocNo value={r.receipt_doc_no} /> : "—") },
    { key: "supplier", header: "NCC", cell: (r) => r.supplier_name ?? "—" },
    {
      key: "lines",
      header: "Hàng · lô",
      cell: (r) =>
        (r.lines ?? []).map((l, i) => (
          <span key={l.id}>
            {i > 0 ? <br /> : null}
            {num(l.qty)} {l.name}
            {l.color_id ? (
              <>
                {" "}
                <ColorChip color={colors.codeOf(l.color_id)} />
              </>
            ) : null}{" "}
            · lô {l.lot_no ?? "—"}
          </span>
        )),
    },
    { key: "reason", header: "Lý do", cell: (r) => reasonName(r.reason_id) },
    { key: "amount", header: "Giá trị", numeric: true, cell: (r) => money(r.total_amount) },
    { key: "credit", header: "Giảm nợ", numeric: true, cell: (r) => money(r.credited_amount) },
    { key: "refund", header: "NCC cần hoàn", numeric: true, cell: (r) => money(r.refund_due_amount) },
  ];
  return <DataTable caption="Trả nhà cung cấp" columns={columns} rows={rows} rowKey={(r) => r.id} empty="Chưa có phiếu trả NCC." />;
}
