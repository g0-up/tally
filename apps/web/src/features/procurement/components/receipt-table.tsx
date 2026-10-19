import { dec } from "../../../shared/lib/decimal";
import { fd, money } from "../../../shared/lib/format";
import { Badge } from "../../../shared/ui/badge";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { LinkButton } from "../../../shared/ui/link-button";
import { receiptOpenAmount } from "../api/adapters";
import type { ReceiptListItem } from "../schemas";

export interface ReceiptTableProps {
  rows: readonly ReceiptListItem[];
  onOpen: (receipt: ReceiptListItem) => void;
  onMatchInvoice: (receipt: ReceiptListItem) => void;
}

/** Prototype `mua-hang.nhan-hang` table: one row per PN with its source, invoice match and what is owed. */
export function ReceiptTable({ rows, onOpen, onMatchInvoice }: ReceiptTableProps) {
  const columns: Column<ReceiptListItem>[] = [
    {
      key: "doc",
      header: "Phiếu nhận",
      cell: (r) => (
        <>
          <LinkButton onClick={() => onOpen(r)}>{r.doc_no}</LinkButton>
          <CellSub>{fd(r.business_date)}</CellSub>
        </>
      ),
    },
    { key: "source", header: "Nguồn", cell: (r) => (r.purchase_order_doc_no ? <DocNo value={r.purchase_order_doc_no} /> : <Badge alt>Nhập nhanh</Badge>) },
    { key: "supplier", header: "NCC", cell: (r) => r.supplier_name ?? "—" },
    {
      key: "invoice",
      header: "Hóa đơn NCC",
      cell: (r) =>
        r.supplier_document_no ? (
          r.supplier_document_no
        ) : (
          <Button size="sm" onClick={() => onMatchInvoice(r)}>
            Đối chiếu HĐ
          </Button>
        ),
    },
    { key: "goods", header: "Giá trị hàng", numeric: true, cell: (r) => money(r.goods_amount) },
    { key: "landed", header: "Chi phí phân bổ", numeric: true, cell: (r) => (dec(r.landed_cost_amount).isZero() ? "—" : money(r.landed_cost_amount)) },
    { key: "open", header: "Còn phải trả", numeric: true, cell: (r) => money(receiptOpenAmount(r)) },
  ];
  return <DataTable caption="Phiếu nhận" columns={columns} rows={rows} rowKey={(r) => r.id} />;
}
