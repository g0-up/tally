import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { toast } from "../../../shared/ui/toast-store";
import { PROCUREMENT_MESSAGES } from "../api/adapters";
import { useCloseShort } from "../api/mutations";
import { useReasons } from "../api/queries";
import type { Order } from "../schemas";

export interface PoCloseDialogProps {
  order: Order;
  onClose: () => void;
}

/**
 * Prototype poClose: stops waiting for what is still pending. The reason is one of the store's
 * "close_short" reasons (the API takes a reason id, not free text).
 */
export function PoCloseDialog({ order, onClose }: PoCloseDialogProps) {
  const reasons = useReasons("close_short");
  const [reasonId, setReasonId] = useState("");
  const close = useCloseShort(onClose);

  const submit = () => {
    if (reasonId === "") {
      toast.commitError(PROCUREMENT_MESSAGES.closeReasonRequired);
      return;
    }
    void close.run({ id: order.id, docNo: order.doc_no, version: order.version, reasonId });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Đóng phần còn thiếu của ${order.doc_no}`}
      sub="Phần đã nhận giữ nguyên. Lượng còn chờ không còn được tính là “dự kiến về”."
      submitLabel="Đóng đơn"
      onSubmit={submit}
      pending={close.pending}
    >
      <Field label="Lý do" hint={reasons.data?.length === 0 ? "Chưa có lý do đóng đơn. Gửi yêu cầu hỗ trợ ở Thiết lập › Thuê bao để thêm." : undefined}>
        <select className="in" value={reasonId} onChange={(e) => setReasonId(e.target.value)} autoFocus>
          <option value="">Chọn lý do</option>
          {(reasons.data ?? []).map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </Field>
    </CommitDialog>
  );
}
