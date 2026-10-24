import { vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { useDeleteOrder } from "../api/mutations";
import type { Order } from "../schemas";

export interface PoDeleteDialogProps {
  order: Order;
  onClose: () => void;
  onDeleted: () => void;
}

/** "Xóa" on a draft Đơn nhập, asked first like a draft invoice's. */
export function PoDeleteDialog({ order, onClose, onDeleted }: PoDeleteDialogProps) {
  const del = useDeleteOrder(onDeleted);
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Xóa đơn nhập nháp ${order.doc_no}?`}
      sub={`${order.supplier_name} · ${vnd(order.total_amount)}. Đơn nháp chưa tạo tồn hay nợ NCC; chỉ xóa được đơn chưa chốt.`}
      pending={del.pending}
      onSubmit={() => void del.run({ id: order.id, docNo: order.doc_no, version: order.version })}
      submitLabel="Xóa nháp"
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
