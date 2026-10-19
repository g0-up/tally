import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { INVENTORY_MESSAGES, RELEASE_REASON_MAX } from "../api/adapters";
import { useReleaseReservation } from "../api/mutations";
import type { Reservation } from "../schemas";

export interface ReleaseReservationDialogProps {
  reservation: Reservation;
  /** Product name shown in the sub line. */
  name: string;
  onClose: () => void;
}

/**
 * Prototype `resRelease`: frees an open hold so the quantity is sellable again. The reason is
 * required and stored on the hold; the API's refusal of it shows under the field.
 */
export function ReleaseReservationDialog({ reservation, name, onClose }: ReleaseReservationDialogProps) {
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const release = useReleaseReservation(onClose, (field, message, code) => {
    if (field !== "reason") return;
    setError(code === "required" ? INVENTORY_MESSAGES.reasonRequired : code === "too_long" ? INVENTORY_MESSAGES.reasonTooLong : message);
  });
  const source = reservation.source_doc_no ?? "Giữ thủ công";

  const submit = () => {
    const text = reason.trim();
    if (text === "") return setError(INVENTORY_MESSAGES.reasonRequired);
    if (text.length > RELEASE_REASON_MAX) return setError(INVENTORY_MESSAGES.reasonTooLong);
    void release.run({ id: reservation.id, reason: text });
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title="Giải phóng hàng giữ"
      sub={`${source} · ${name}. Lượng giải phóng trở lại “có thể bán”; dòng đơn chuyển thành thiếu hàng.`}
      submitLabel="Giải phóng"
      submitVariant="danger"
      onSubmit={submit}
      pending={release.pending}
    >
      <Field label="Lý do" error={error}>
        <Input
          value={reason}
          onChange={(e) => {
            setReason(e.target.value);
            setError(undefined);
          }}
          invalid={error !== undefined}
          maxLength={RELEASE_REASON_MAX}
          autoFocus
        />
      </Field>
    </CommitDialog>
  );
}
