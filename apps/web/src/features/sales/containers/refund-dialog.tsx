import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec, toDecimalString } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { ListLoading } from "../../../shared/ui/list-footer";
import { MoneyInput } from "../../../shared/ui/money-input";
import { toast } from "../../../shared/ui/toast-store";
import { useCustomerDirectory, useDebtLedger, usePaymentMethods } from "../api/queries";
import { usePostRefund } from "../api/return-mutations";
import type { SalesReturn } from "../schemas";

export interface RefundDialogProps {
  salesReturn: SalesReturn;
  onClose: () => void;
}

const SUB = "Quỹ chỉ giảm khi phiếu chi hoàn tiền được ghi.";

/** Prototype refund modal "Hoàn tiền cho X": pays back (part of) the refund due a return left. */
export function RefundDialog({ salesReturn, onClose }: RefundDialogProps) {
  const customers = useCustomerDirectory();
  const ledger = useDebtLedger(salesReturn.customer_party_id);
  const methods = usePaymentMethods().data ?? [];
  const [amount, setAmount] = useState<Money | null>(null);
  const [methodChoice, setMethod] = useState<string | null>(null);
  const post = usePostRefund(onClose);

  const title = `Hoàn tiền cho ${customers.name(salesReturn.customer_party_id)}`;
  const obligation = ledger.data?.documents.find((d) => d.source_id === salesReturn.id && d.status === "open" && dec(d.open).gt(0));
  const methodId = methodChoice ?? methods[0]?.id ?? null;
  const value = amount ?? obligation?.open ?? null;

  const submit = () => {
    if (!obligation) {
      toast.commitError(`${salesReturn.doc_no} không còn khoản cần hoàn.`);
      return;
    }
    if (value === null || dec(value).lte(0)) {
      toast.commitError("Nhập số tiền hoàn.");
      return;
    }
    if (dec(value).gt(obligation.open)) {
      toast.commitError(`Số tiền hoàn vượt khoản cần hoàn ${vnd(obligation.open)}.`);
      return;
    }
    if (methodId === null) {
      toast.commitError("Chọn quỹ chi để hoàn tiền cho khách.");
      return;
    }
    void post.run({ obligationId: obligation.id, paymentMethodId: methodId, amount: toDecimalString(value) });
  };

  if (ledger.isLoading) {
    return (
      <CommitDialog open onClose={onClose} title={title} sub={SUB}>
        <ListLoading />
      </CommitDialog>
    );
  }

  return (
    <CommitDialog open onClose={onClose} title={title} sub={SUB} pending={post.pending} onSubmit={submit} submitLabel="Ghi phiếu chi">
      {obligation ? (
        <div className="fg">
          <Field label="Số tiền" hint={`Còn cần hoàn theo ${salesReturn.doc_no}: ${vnd(obligation.open)}`}>
            <MoneyInput value={value} aria-label="Số tiền hoàn" onChange={setAmount} />
          </Field>
          <Field label="Chi từ quỹ">
            <select className="in" value={methodId ?? ""} onChange={(e) => setMethod(e.target.value)}>
              {methods.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
          </Field>
        </div>
      ) : (
        <p className="hint">{salesReturn.doc_no} không còn khoản cần hoàn cho khách.</p>
      )}
    </CommitDialog>
  );
}
