import { useState } from "react";
import { Button } from "../../../shared/ui/button";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { toast } from "../../../shared/ui/toast-store";
import { useSaveSupplier } from "../api/mutations";
import { SupplierFormFields } from "../components/supplier-form";
import type { SupplierResult } from "../schemas";
import { EMPTY_SUPPLIER, validateSupplierDraft, type SupplierDraft, type SupplierErrors } from "../supplier-form-values";

export interface SupplierQuickDialogProps {
  /** Back to the document being edited without a new supplier. */
  onClose: () => void;
  /** The new supplier, to select on the document. */
  onCreated: (supplier: SupplierResult) => void;
}

/** Prototype supQuick: a supplier added from a purchase document, selected there on save. */
export function SupplierQuickDialog({ onClose, onCreated }: SupplierQuickDialogProps) {
  const [draft, setDraft] = useState<SupplierDraft>(EMPTY_SUPPLIER);
  const [errors, setErrors] = useState<SupplierErrors>({});
  const save = useSaveSupplier({
    supplier: null,
    contact: null,
    quick: true,
    setErrors,
    success: (s) => (
      <>
        Đã thêm <b>{s.name}</b> và chọn cho phiếu.
      </>
    ),
    after: onCreated,
  });

  const submit = () => {
    const failed = validateSupplierDraft(draft);
    const first = failed.name ?? failed.phone ?? failed.terms;
    setErrors(failed);
    if (first !== undefined) {
      toast.commitError(first);
      return;
    }
    void save.run(draft);
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Thêm nhà cung cấp mới"
      sub="Lưu xong sẽ quay lại phiếu đang làm, NCC mới được chọn sẵn. Thông tin chi tiết bổ sung sau ở Danh mục → Nhà cung cấp."
      extra={
        <Button variant="line" onClick={onClose} disabled={save.pending}>
          Quay lại phiếu
        </Button>
      }
      submitLabel="Thêm & chọn"
      onSubmit={submit}
      pending={save.pending}
    >
      <SupplierFormFields draft={draft} onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))} errors={errors} quick />
    </CommitDialog>
  );
}
