import { useState } from "react";
import { money } from "../../../shared/lib/format";
import type { Money } from "../../../shared/domain/money";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { MoneyInput } from "../../../shared/ui/money-input";
import { useIssueInvoice, useMarkInvoicePaid, useVoidInvoice } from "../api/mutations";
import { usePlans } from "../api/queries";
import type { AdminInvoice } from "../api/schemas";
import { TenantPicker, type PickedTenant } from "../components/tenant-picker";
import { TypedConfirmDialog } from "../components/typed-confirm-dialog";

const MAX_REFERENCE = 120;

/** Lập hóa đơn thuê bao: an open invoice for a store and a plan, at the plan price unless overridden. */
export function IssueInvoiceDialog({ onClose }: { onClose: () => void }) {
  const plans = usePlans();
  const [tenant, setTenant] = useState<PickedTenant | null>(null);
  const [planId, setPlanId] = useState("");
  const [amount, setAmount] = useState<Money | null>(null);
  const [errors, setErrors] = useState<{ tenant?: string | undefined; plan?: string | undefined }>({});
  const plan = plans.data?.find((p) => p.id === planId);
  const issue = useIssueInvoice({
    success: (inv) => (
      <>
        Đã lập hóa đơn <b>{inv.number}</b>.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    const next = { tenant: tenant ? undefined : "Chọn cửa hàng.", plan: planId ? undefined : "Chọn gói." };
    setErrors(next);
    if (!tenant || next.plan) return;
    void issue.run({ tenant_id: tenant.id, plan_id: planId, ...(amount !== null ? { amount } : {}) });
  };
  return (
    <CommitDialog open onClose={onClose} title="Lập hóa đơn thuê bao" onSubmit={submit} submitLabel="Lập hóa đơn" pending={issue.pending}>
      <Field label="Cửa hàng" error={errors.tenant}>
        <TenantPicker aria-label="Cửa hàng" billable value={tenant} onChange={setTenant} invalid={errors.tenant !== undefined} />
      </Field>
      <Field label="Gói" error={errors.plan}>
        <select className="in" value={planId} onChange={(e) => setPlanId(e.target.value)}>
          <option value="">Chọn gói</option>
          {(plans.data ?? [])
            .filter((p) => p.active)
            .map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.code})
              </option>
            ))}
        </select>
      </Field>
      <Field label="Số tiền" hint={plan ? `Để trống để lấy giá gói: ${money(plan.price)}.` : "Để trống để lấy giá gói."}>
        <MoneyInput value={amount} onChange={setAmount} />
      </Field>
    </CommitDialog>
  );
}

export function MarkPaidDialog({ invoice, onClose }: { invoice: AdminInvoice; onClose: () => void }) {
  const [reference, setReference] = useState("");
  const [error, setError] = useState<string>();
  const markPaid = useMarkInvoicePaid({
    success: (inv) => (
      <>
        Đã ghi nhận thanh toán hóa đơn <b>{inv.number}</b>.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    const ref = reference.trim();
    if (ref === "") return setError("Nhập mã tham chiếu chuyển khoản.");
    if (ref.length > MAX_REFERENCE) return setError(`Mã tham chiếu tối đa ${MAX_REFERENCE} ký tự.`);
    void markPaid.run({ id: invoice.id, reference: ref });
  };
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Ghi nhận thanh toán · ${invoice.number}`}
      sub={`${invoice.tenant_name} · ${money(invoice.amount)}`}
      onSubmit={submit}
      submitLabel="Ghi nhận đã thanh toán"
      pending={markPaid.pending}
    >
      <Field label="Mã tham chiếu chuyển khoản" error={error} hint="Mỗi mã chỉ dùng cho một hóa đơn.">
        <Input
          value={reference}
          invalid={error !== undefined}
          autoComplete="off"
          onChange={(e) => {
            setReference(e.target.value);
            setError(undefined);
          }}
        />
      </Field>
    </CommitDialog>
  );
}

export function VoidInvoiceDialog({ invoice, onClose }: { invoice: AdminInvoice; onClose: () => void }) {
  const voidInvoice = useVoidInvoice({
    success: (inv) => (
      <>
        Đã hủy hóa đơn <b>{inv.number}</b>.
      </>
    ),
    after: onClose,
  });
  return (
    <TypedConfirmDialog
      open
      onClose={onClose}
      title={`Hủy hóa đơn · ${invoice.number}`}
      phrase={invoice.number}
      phraseLabel="số hóa đơn"
      submitLabel="Hủy hóa đơn"
      pending={voidInvoice.pending}
      onConfirm={() => void voidInvoice.run(invoice.id)}
    >
      <p>
        Hóa đơn {money(invoice.amount)} của {invoice.tenant_name} sẽ không còn thu được. Thao tác này không hoàn tác.
      </p>
    </TypedConfirmDialog>
  );
}
