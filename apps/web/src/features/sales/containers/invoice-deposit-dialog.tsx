import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec, sumDec } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { StatusPill } from "../../../shared/ui/status-pill";
import { toast } from "../../../shared/ui/toast-store";
import { PAYMENT_LABELS, invoicePayment, paymentCodeOf } from "../api/adapters";
import { useInvoiceDeposit } from "../api/invoice-mutations";
import { usePaymentMethods } from "../api/queries";
import type { Invoice } from "../schemas";

/** Prototype `soDeposit`: money the customer pays against an invoice, before or after delivery. */
export function InvoiceDepositDialog({ invoice, onClose }: { invoice: Invoice; onClose: () => void }) {
  const m = invoicePayment(invoice);
  const [amount, setAmount] = useState<Money | null>(dec(m.open_amount).gt(0) ? m.open_amount : null);
  const [methodChoice, setMethod] = useState<string | null>(null);
  const [note, setNote] = useState(`Khách thanh toán ${invoice.doc_no}`);
  const methods = usePaymentMethods().data ?? [];
  const methodId = methodChoice ?? methods[0]?.id ?? null;
  const deposit = useInvoiceDeposit(onClose);

  const paid = sumDec([m.paid_amount, amount ?? "0"]);
  const remain = dec(m.total_amount).minus(paid);

  const submit = () => {
    if (amount === null || !dec(amount).gt(0)) {
      toast.commitError("Nhập số tiền khách trả.");
      return;
    }
    if (methodId === null) {
      toast.commitError("Chọn quỹ nhận tiền.");
      return;
    }
    void deposit.run({ invoice, amount, paymentMethodId: methodId, note });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Ghi nhận thanh toán ${invoice.doc_no}`}
      sub="Trước khi giao, tiền khách trả là ứng trước. Hệ thống tự trừ vào công nợ khi xuất kho giao và tự tính lại trạng thái thanh toán."
      pending={deposit.pending}
      onSubmit={submit}
      submitLabel="Ghi phiếu thu"
      live={
        <ImpactSummary
          items={[
            { label: "Tổng tiền", value: vnd(m.total_amount) },
            { label: "Đã thanh toán sau phiếu này", value: vnd(paid) },
            { label: "Còn lại", value: vnd(remain.gt(0) ? remain : 0) },
            { label: "Trạng thái", value: <StatusPill label={PAYMENT_LABELS[paymentCodeOf(m.total_amount, paid.toFixed())]} /> },
          ]}
        />
      }
    >
      <div className="fg">
        <Field label="Số tiền khách trả">
          <MoneyInput value={amount} onChange={setAmount} />
        </Field>
        <Field label="Vào quỹ">
          <select className="in" value={methodId ?? ""} onChange={(e) => setMethod(e.target.value)}>
            {methods.map((pm) => (
              <option key={pm.id} value={pm.id}>
                {pm.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Nội dung">
          <Input value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} />
        </Field>
      </div>
    </CommitDialog>
  );
}
