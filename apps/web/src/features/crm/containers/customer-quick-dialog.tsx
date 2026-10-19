import { useState } from "react";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { Button } from "../../../shared/ui/button";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input } from "../../../shared/ui/input";
import { toast } from "../../../shared/ui/toast-store";
import { useQuickCustomer } from "../api/mutations";
import { EMPTY_CUSTOMER, firstCustomerError, validateCustomerDraft, type CustomerDraft, type CustomerErrors } from "../customer-form-values";
import type { CustomerResult } from "../schemas";

export interface CustomerQuickDialogProps {
  /** Back to the form being filled without a new customer. */
  onClose: () => void;
  /** The new customer, to select on that form. */
  onCreated: (customer: CustomerResult) => void;
}

/** A customer added from another form (name, phone, terms), selected there on save. */
export function CustomerQuickDialog({ onClose, onCreated }: CustomerQuickDialogProps) {
  const [draft, setDraft] = useState<CustomerDraft>(EMPTY_CUSTOMER);
  const [errors, setErrors] = useState<CustomerErrors>({});
  const focusFirstInvalid = useFocusFirstInvalid();
  const save = useQuickCustomer({
    setErrors,
    success: (c) => (
      <>
        Đã thêm <b>{c.name}</b> và chọn sẵn.
      </>
    ),
    after: onCreated,
  });

  const change = (patch: Partial<CustomerDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const submit = () => {
    const failed = validateCustomerDraft(draft, []);
    const first = firstCustomerError(failed);
    setErrors(failed);
    if (first !== undefined) {
      toast.commitError(first);
      // The messages sit under the fields; focus goes to the first one marked.
      focusFirstInvalid();
      return;
    }
    void save.run(draft);
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Thêm khách hàng mới"
      sub="Lưu xong sẽ quay lại màn đang làm, khách mới được chọn sẵn. Thông tin chi tiết bổ sung sau ở Khách hàng."
      extra={
        // While the create may be stored without an answer, leaving goes through the close question.
        <Button variant="line" onClick={onClose} disabled={save.pending || save.unsettled}>
          Quay lại
        </Button>
      }
      submitLabel="Thêm & chọn"
      onSubmit={submit}
      pending={save.pending}
      {...(save.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
    >
      <FormGrid>
        <Field label="Tên khách hàng *" error={errors.name}>
          <Input value={draft.name} onChange={(e) => change({ name: e.target.value })} invalid={errors.name !== undefined} />
        </Field>
        <Field label="Số điện thoại" error={errors.phone}>
          <Input inputMode="tel" value={draft.phone} onChange={(e) => change({ phone: e.target.value })} invalid={errors.phone !== undefined} />
        </Field>
        <Field label="Số ngày nợ" error={errors.terms}>
          <Input inputMode="numeric" value={draft.terms} onChange={(e) => change({ terms: e.target.value })} invalid={errors.terms !== undefined} />
        </Field>
      </FormGrid>
    </CommitDialog>
  );
}
