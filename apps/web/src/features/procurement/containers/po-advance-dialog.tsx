import { useState } from "react";
import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { MoneyInput } from "../../../shared/ui/money-input";
import { toast } from "../../../shared/ui/toast-store";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { useOrderAdvance } from "../api/mutations";
import { usePaymentMethods } from "../api/queries";
import type { Order } from "../schemas";

export interface PoAdvanceDialogProps {
  order: Order;
  onClose: () => void;
}

/** Prototype poAdvance: a PC paid to the supplier before the goods arrive, earmarked for the order. */
export function PoAdvanceDialog({ order, onClose }: PoAdvanceDialogProps) {
  const methods = usePaymentMethods();
  const [amount, setAmount] = useState<Money | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const fund = picked ?? methods.data?.[0]?.id ?? null;
  const advance = useOrderAdvance(onClose);

  const submit = () => {
    if (amount === null || !dec(amount).gt(0)) {
      toast.commitError(PROCUREMENT_MESSAGES.amountPositive);
      return;
    }
    if (fund === null) {
      toast.commitError(PROCUREMENT_MESSAGES.fundRequired);
      return;
    }
    void advance.run({ orderId: order.id, supplierId: order.supplier_party_id, amount, paymentMethodId: fund });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Ứng trước cho ${order.doc_no}`}
      sub="Thanh toán trước khi nhận là ứng trước NCC, chưa phải giảm nợ. Tự phân bổ khi phiếu nhận được ghi."
      submitLabel="Ghi phiếu chi"
      onSubmit={submit}
      pending={advance.pending}
    >
      <FormGrid>
        <Field label="Số tiền">
          <MoneyInput value={amount} onChange={setAmount} autoFocus />
        </Field>
        <Field label="Chi từ quỹ">
          <select className="in" value={fund ?? ""} onChange={(e) => setPicked(e.target.value)}>
            {(methods.data ?? []).map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </Field>
      </FormGrid>
    </CommitDialog>
  );
}
