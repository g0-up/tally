import { vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { useDeleteInvoice } from "../api/invoice-mutations";
import type { Invoice } from "../schemas";

export interface InvoiceDeleteDialogProps {
  invoice: Invoice;
  customerName: string;
  onClose: () => void;
  onDeleted: () => void;
}

/** Prototype `soDel`: deletes a draft invoice that has no payment yet. */
export function InvoiceDeleteDialog({ invoice, customerName, onClose, onDeleted }: InvoiceDeleteDialogProps) {
  const del = useDeleteInvoice(onDeleted);
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Xóa hóa đơn ${invoice.doc_no}?`}
      sub={`${customerName} · ${vnd(invoice.total_amount)}. Chỉ xóa được hóa đơn nháp chưa có tiền khách trả.`}
      pending={del.pending}
      onSubmit={() => void del.run(invoice)}
      submitLabel="Xóa hóa đơn"
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
