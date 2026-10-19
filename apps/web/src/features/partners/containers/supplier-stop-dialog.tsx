import { Callout } from "../../../shared/ui/callout";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { useSupplierStatus } from "../api/mutations";
import type { SupplierListItem } from "../schemas";
import { SUPPLIER_ERROR_MESSAGES } from "../supplier-form-values";

/**
 * Confirmation before "Ngừng" giao dịch with a supplier. While orders with it are still open the
 * dialog only says why it cannot be stopped yet. Mount per opening.
 */
export function SupplierStopDialog({ supplier, onClose }: { supplier: SupplierListItem; onClose: () => void }) {
  const status = useSupplierStatus(onClose);
  if (!supplier.can_stop) {
    return (
      <CommitDialog open onClose={onClose} title={`Chưa ngừng được giao dịch với ${supplier.name}`} cancelLabel="Đóng">
        <Callout tone="warn" role="alert">
          {SUPPLIER_ERROR_MESSAGES.supplier_has_open_orders}
        </Callout>
      </CommitDialog>
    );
  }
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Ngừng giao dịch với ${supplier.name}?`}
      sub="Nhà cung cấp được ẩn khỏi ô chọn khi tạo đơn nhập nhưng vẫn giữ lịch sử và vẫn trả nợ được."
      submitLabel="Ngừng giao dịch"
      submitVariant="danger"
      onSubmit={() => void status.run({ supplier, on: false })}
      pending={status.pending}
    >
      <p className="hint">Mở lại được bất cứ lúc nào bằng “Mở lại”.</p>
    </CommitDialog>
  );
}
