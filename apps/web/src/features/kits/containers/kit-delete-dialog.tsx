import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { useDeleteKit } from "../api/mutations";
import type { Kit } from "../schemas";

export interface KitDeleteDialogProps {
  kit: Kit;
  onClose: () => void;
  onDeleted: () => void;
}

/** Prototype `kitDel`: removes a draft that was never published. */
export function KitDeleteDialog({ kit, onClose, onDeleted }: KitDeleteDialogProps) {
  const del = useDeleteKit(onDeleted);
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Xóa nháp ${kit.name}?`}
      sub="Chỉ xóa được giỏ/combo nháp chưa phát hành."
      submitLabel="Xóa nháp"
      onSubmit={() => void del.run(kit)}
      pending={del.pending}
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
