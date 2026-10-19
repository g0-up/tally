import { diffDays } from "../../../shared/lib/dates";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button } from "../../../shared/ui/button";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { StatusPill } from "../../../shared/ui/status-pill";
import { DELIVERY_LABELS, INVOICE_STATUS_LABELS, PAYMENT_LABELS, deliveryCodeOf, invoiceEditable, invoicePayment, termsLabel } from "../api/adapters";
import type { Invoice } from "../schemas";

export interface InvoiceActsProps {
  invoice: Invoice;
  copying: boolean;
  onEdit: (invoice: Invoice) => void;
  onDelete: (invoice: Invoice) => void;
  onCopy: (invoice: Invoice) => void;
}

/**
 * Prototype `soActs`: "Sửa" and "Xóa" while the invoice is a draft, "Sao chép" always. Each name
 * carries the invoice number so a list of rows does not read as the same three buttons.
 */
export function InvoiceActs({ invoice, copying, onEdit, onDelete, onCopy }: InvoiceActsProps) {
  const no = invoice.doc_no;
  return (
    <>
      {invoiceEditable(invoice.status) ? (
        <>
          <Button variant="navy" size="sm" aria-label={`Sửa ${no}`} onClick={() => onEdit(invoice)}>
            Sửa
          </Button>{" "}
          <Button variant="danger" size="sm" aria-label={`Xóa ${no}`} onClick={() => onDelete(invoice)}>
            Xóa
          </Button>{" "}
        </>
      ) : null}
      <Button size="sm" pending={copying} aria-label={copying ? undefined : `Sao chép ${no}`} onClick={() => onCopy(invoice)}>
        Sao chép
      </Button>
    </>
  );
}

export interface InvoiceTableProps {
  invoices: readonly Invoice[];
  customerName: (partyId: string) => string;
  /** Invoice being copied. */
  copyingId: string | null;
  onEdit: (invoice: Invoice) => void;
  onDelete: (invoice: Invoice) => void;
  onCopy: (invoice: Invoice) => void;
}

/** Prototype invoices view: money columns, payment, delivery and status, footer totals. */
export function InvoiceTable({ invoices, customerName, copyingId, onEdit, onDelete, onCopy }: InvoiceTableProps) {
  const paid = sumDec(invoices.map((o) => invoicePayment(o).paid_amount));
  const open = sumDec(invoices.map((o) => invoicePayment(o).open_amount));
  const columns: Column<Invoice>[] = [
    {
      key: "no",
      header: "Số hóa đơn",
      cell: (o) => (
        <>
          <DocNo value={o.doc_no} />
          <CellSub>
            {fd(o.invoice_date)} · v{o.current_version}
          </CellSub>
        </>
      ),
      footer: `${num(invoices.length)} hóa đơn`,
    },
    {
      key: "customer",
      header: "Khách hàng",
      cell: (o) => (
        <>
          {customerName(o.customer_party_id)}
          <CellSub>{termsLabel(o.due_date ? diffDays(o.due_date, o.invoice_date) : 0)}</CellSub>
        </>
      ),
    },
    { key: "total", header: "Tổng tiền", numeric: true, cell: (o) => vnd(o.total_amount), footer: vnd(sumDec(invoices.map((o) => o.total_amount))) },
    {
      key: "paid",
      header: "Đã thanh toán",
      numeric: true,
      cell: (o) => vnd(invoicePayment(o).paid_amount),
      footer: vnd(paid),
    },
    {
      key: "open",
      header: "Còn lại",
      numeric: true,
      cell: (o) => {
        const p = invoicePayment(o);
        return dec(p.open_amount).gt(0) ? <b>{vnd(p.open_amount)}</b> : vnd(0);
      },
      footer: vnd(open),
    },
    {
      key: "pay",
      header: "Thanh toán",
      cell: (o) => <StatusPill label={PAYMENT_LABELS[invoicePayment(o).code]} />,
    },
    { key: "delivery", header: "Giao hàng", cell: (o) => <StatusPill label={DELIVERY_LABELS[deliveryCodeOf(o.status, o.delivered_amount)]} /> },
    { key: "status", header: "Trạng thái", cell: (o) => <StatusPill label={INVOICE_STATUS_LABELS[o.status]} /> },
    { key: "acts", header: "", className: "acts", cell: (o) => <InvoiceActs invoice={o} copying={copyingId === o.id} onEdit={onEdit} onDelete={onDelete} onCopy={onCopy} /> },
  ];
  return <DataTable caption="Hóa đơn" columns={columns} rows={invoices} rowKey={(o) => o.id} empty="Không có hóa đơn phù hợp bộ lọc." />;
}
