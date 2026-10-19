import { useState } from "react";
import { toast } from "../../../shared/ui/toast-store";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ListLoading } from "../../../shared/ui/list-footer";
import { useSaveSupplier } from "../api/mutations";
import { usePartyContacts } from "../api/queries";
import { SupplierFormFields } from "../components/supplier-form";
import type { Contact, Supplier } from "../schemas";
import { EMPTY_SUPPLIER, supplierDraftOf, validateSupplierDraft, type SupplierDraft, type SupplierErrors } from "../supplier-form-values";

const EDIT_SUB = "Đổi điều khoản nợ chỉ áp dụng cho phiếu nhận mới; nợ đã ghi giữ hạn cũ.";
const NEW_SUB = "Mã được tạo tự động. Chỉ tên là bắt buộc.";

export interface SupplierDialogProps {
  /** Null adds a supplier. */
  supplier: Supplier | null;
  onClose: () => void;
}

/** Prototype supModal. Mount per opening. */
export function SupplierDialog({ supplier, onClose }: SupplierDialogProps) {
  const contacts = usePartyContacts(supplier?.id ?? null);
  const title = supplier ? `Sửa nhà cung cấp · ${supplier.name}` : "Thêm nhà cung cấp";
  if (supplier !== null && contacts.isPending) {
    return (
      <CommitDialog open onClose={onClose} title={title}>
        <ListLoading />
      </CommitDialog>
    );
  }
  const primary = contacts.data?.find((c) => c.is_primary) ?? null;
  return <SupplierForm supplier={supplier} contact={primary} title={title} onClose={onClose} />;
}

function SupplierForm({ supplier, contact, title, onClose }: SupplierDialogProps & { contact: Contact | null; title: string }) {
  const [draft, setDraft] = useState<SupplierDraft>(supplier ? supplierDraftOf(supplier, contact?.name ?? "") : EMPTY_SUPPLIER);
  const [errors, setErrors] = useState<SupplierErrors>({});
  const save = useSaveSupplier({
    supplier,
    contact,
    setErrors,
    success: (s) =>
      supplier ? (
        "Đã lưu nhà cung cấp."
      ) : (
        <>
          Đã thêm <b>{s.name}</b>. Có thể chọn ngay khi tạo đơn nhập.
        </>
      ),
    after: onClose,
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
      title={title}
      sub={supplier ? EDIT_SUB : NEW_SUB}
      submitLabel={supplier ? "Lưu" : "Thêm nhà cung cấp"}
      onSubmit={submit}
      pending={save.pending}
    >
      <SupplierFormFields draft={draft} onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))} errors={errors} customerLocked={supplier?.is_customer ?? false} />
    </CommitDialog>
  );
}
