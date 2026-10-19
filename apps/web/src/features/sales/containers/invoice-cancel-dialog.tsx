import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { toast } from "../../../shared/ui/toast-store";
import { useCancelRemainder } from "../api/invoice-mutations";
import type { Invoice } from "../schemas";

/**
 * Prototype `soCancel`: releases what is still reserved for an invoice. The reason is required as
 * in the prototype; the cancel command has no field for it yet, so it is not stored.
 */
export function InvoiceCancelDialog({ invoice, onClose }: { invoice: Invoice; onClose: () => void }) {
  const [reason, setReason] = useState("");
  const cancel = useCancelRemainder(onClose);

  const submit = () => {
    if (!reason.trim()) {
      toast.commitError("Nhập lý do hủy phần còn lại.");
      return;
    }
    void cancel.run(invoice);
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Hủy phần chưa giao của ${invoice.doc_no}`}
      sub="Phần đã giao và đã ghi sổ giữ nguyên. Lượng đang giữ được giải phóng."
      pending={cancel.pending}
      onSubmit={submit}
      submitLabel="Hủy phần còn lại"
      submitVariant="danger"
    >
      <Field label="Lý do">
        <Input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
      </Field>
    </CommitDialog>
  );
}
