import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { useCategories } from "../../catalog";
import { useStartCount } from "../api/mutations";
import type { Count } from "../schemas";

export interface NewCountDialogProps {
  onClose: () => void;
  /** The opened count, shown in its drawer straight away. */
  onStarted: (count: Count) => void;
}

/** Prototype `newCount` "Bắt đầu kiểm kê": a blind count of the whole store or one category. */
export function NewCountDialog({ onClose, onStarted }: NewCountDialogProps) {
  const categories = useCategories();
  const [categoryId, setCategoryId] = useState("");
  const start = useStartCount((count) => {
    onClose();
    onStarted(count);
  });

  return (
    <CommitDialog open onClose={onClose} title="Bắt đầu kiểm kê" submitLabel="Bắt đầu" onSubmit={() => void start.run({ categoryId: categoryId === "" ? null : categoryId })} pending={start.pending}>
      <Field label="Nhóm hàng">
        <select className="in" value={categoryId} onChange={(e) => setCategoryId(e.target.value)}>
          <option value="">Toàn kho</option>
          {(categories.data ?? []).map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      </Field>
      <p className="hint">Hệ thống chụp số tồn tại thời điểm bắt đầu để đối chiếu.</p>
    </CommitDialog>
  );
}
