import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { useDeleteSupplier } from "../api/mutations";
import type { Supplier } from "../schemas";

/** Prototype supDel. Mount per opening. */
export function SupplierDeleteDialog({ supplier, onClose }: { supplier: Supplier; onClose: () => void }) {
  const del = useDeleteSupplier(supplier, onClose);
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Xóa nhà cung cấp ${supplier.name}?`}
      sub="Chỉ xóa được vì NCC này chưa có chứng từ nào."
      submitLabel="Xóa"
      submitVariant="danger"
      onSubmit={() => void del.run()}
      pending={del.pending}
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
