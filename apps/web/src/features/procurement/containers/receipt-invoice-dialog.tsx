import { useState } from "react";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { toast } from "../../../shared/ui/toast-store";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { useMatchInvoice } from "../api/mutations";
import type { ReceiptListItem } from "../schemas";

export interface ReceiptInvoiceDialogProps {
  receipt: Pick<ReceiptListItem, "id" | "doc_no" | "supplier_party_id" | "total_amount" | "tax_amount">;
  onClose: () => void;
}

/** Prototype rcvInvoice: the supplier invoice number matched to a receipt; the payable is not posted again. */
export function ReceiptInvoiceDialog({ receipt, onClose }: ReceiptInvoiceDialogProps) {
  const [documentNo, setDocumentNo] = useState("");
  const match = useMatchInvoice(onClose);

  const submit = () => {
    const no = documentNo.trim();
    if (no === "") {
      toast.commitError(PROCUREMENT_MESSAGES.invoiceRequired);
      return;
    }
    if (!receipt.supplier_party_id) {
      toast.commitError(PROCUREMENT_MESSAGES.supplierRequired);
      return;
    }
    void match.run({ supplierId: receipt.supplier_party_id, receipt: { id: receipt.id, total_amount: receipt.total_amount, tax_amount: receipt.tax_amount }, documentNo: no });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      {...(match.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
      title={`Đối chiếu hóa đơn NCC cho ${receipt.doc_no}`}
      sub="Gắn số hóa đơn là đối chiếu chứng từ; nợ NCC đã ghi lúc nhận hàng, không ghi lại."
      submitLabel="Lưu đối chiếu"
      onSubmit={submit}
      pending={match.pending}
    >
      <Field label="Số hóa đơn">
        <Input value={documentNo} onChange={(e) => setDocumentNo(e.target.value)} autoFocus />
      </Field>
    </CommitDialog>
  );
}
