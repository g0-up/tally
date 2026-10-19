import { dec, sumDec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { lineName, refundDue } from "../api/adapters";
import type { SalesReturn } from "../schemas";

export interface ReturnTableProps {
  returns: readonly SalesReturn[];
  customerName: (partyId: string | null) => string;
  /** Number of a loaded sale document by id. */
  docNoOf: (salesDocumentId: string | null) => string | null;
  reasonName: (reasonId: string) => string;
  onRefund: (salesReturn: SalesReturn) => void;
}

/** Prototype returns view: one row per return with what it reduced and what is still to pay back. */
export function ReturnTable({ returns, customerName, docNoOf, reasonName, onRefund }: ReturnTableProps) {
  const columns: Column<SalesReturn>[] = [
    {
      key: "no",
      header: "Phiếu",
      cell: (r) => (
        <>
          <DocNo value={r.doc_no} />
          <CellSub>{fd(r.business_date)}</CellSub>
        </>
      ),
      footer: `${num(returns.length)} phiếu`,
    },
    { key: "source", header: "Chứng từ gốc", cell: (r) => (r.sales_document_id ? <DocNo value={docNoOf(r.sales_document_id)} /> : <span className="hint">Không có</span>) },
    { key: "customer", header: "Khách", cell: (r) => customerName(r.customer_party_id) },
    {
      key: "lines",
      header: "Hàng trả",
      cell: (r) =>
        r.lines && r.lines.length > 0 ? r.lines.map((l) => <div key={l.id}>{`${num(l.qty)} ${l.unit_name} ${lineName(l)}`}</div>) : <span className="hint">—</span>,
    },
    { key: "reason", header: "Lý do", cell: (r) => reasonName(r.reason_id) },
    { key: "value", header: "Giá trị", numeric: true, cell: (r) => vnd(r.total_amount), footer: vnd(sumDec(returns.map((r) => r.total_amount))) },
    { key: "debt", header: "Giảm nợ", numeric: true, cell: (r) => vnd(r.debt_reduction_amount), footer: vnd(sumDec(returns.map((r) => r.debt_reduction_amount))) },
    { key: "refund", header: "Cần hoàn", numeric: true, cell: (r) => (dec(r.refund_due_amount).gt(0) ? <b>{vnd(r.refund_due_amount)}</b> : vnd(0)) },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (r) =>
        refundDue(r) ? (
          <Button size="sm" variant="navy" onClick={() => onRefund(r)}>
            Hoàn tiền
          </Button>
        ) : null,
    },
  ];
  return <DataTable caption="Phiếu trả hàng" columns={columns} rows={returns} rowKey={(r) => r.id} empty="Chưa có phiếu trả hàng." />;
}
