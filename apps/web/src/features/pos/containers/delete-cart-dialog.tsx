import { vnd } from "../../../shared/lib/format";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import type { PosCart } from "../../sales/schemas";
import { useDeleteCart } from "../api/mutations";

export interface DeleteCartDialogProps {
  cart: PosCart;
  customerName: string;
  /** Number of goods lines, when the cart detail is loaded. */
  lineCount: number | null;
  onClose: () => void;
  onDeleted: (cart: PosCart) => void;
}

/** "Xóa giỏ hàng GH-…?": an open cart has no stock or money documents yet, so it simply goes away. */
export function DeleteCartDialog({ cart, customerName, lineCount, onClose, onDeleted }: DeleteCartDialogProps) {
  const del = useDeleteCart(cart, () => {
    onDeleted(cart);
    onClose();
  });
  const count = lineCount === null ? "" : ` · ${lineCount} mặt hàng`;
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Xóa giỏ hàng ${cart.doc_no}?`}
      sub={`${customerName}${count} · ${vnd(cart.total_amount)}. Giỏ chưa bán nên chưa có chứng từ kho hay tiền.`}
      submitLabel="Xóa giỏ"
      pending={del.pending}
      onSubmit={() => void del.run()}
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
