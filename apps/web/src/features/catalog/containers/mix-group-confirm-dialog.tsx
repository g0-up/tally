import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { mixGroupKeys, skuKeys } from "../api/queries";
import type { MixGroup } from "../schemas";

export type MixGroupConfirmKind = "deactivate" | "delete";

const COPY = {
  deactivate: {
    title: (name: string) => `Ngừng dùng nhóm mix ${name}?`,
    sub: "Nhóm không còn hiện trong ô chọn “Thùng mix vị”. Chứng từ cũ vẫn giữ nguyên.",
    submit: "Ngừng dùng",
    toast: "Đã ngừng dùng nhóm mix. Chứng từ cũ vẫn giữ nguyên.",
  },
  delete: {
    title: (name: string) => `Xóa nhóm mix ${name}?`,
    sub: "Chỉ xóa được vì nhóm chưa dùng trên chứng từ nào.",
    submit: "Xóa",
    toast: "Đã xóa nhóm mix.",
  },
} as const;

/** Confirmation before "Ngừng dùng" or "Xóa" of a mix group. Mount per opening. */
export function MixGroupConfirmDialog({ group, kind, onClose }: { group: MixGroup; kind: MixGroupConfirmKind; onClose: () => void }) {
  const client = useApiClient();
  const copy = COPY[kind];
  const commit = useCommit<unknown>({
    mutationFn: (_, { idempotencyKey }) =>
      kind === "delete" ? client.delete<unknown>(`/mix-groups/${group.id}`) : client.post<unknown>(`/mix-groups/${group.id}:deactivate`, undefined, { idempotencyKey }),
    success: () => copy.toast,
    invalidate: [mixGroupKeys.all, skuKeys.all],
    after: onClose,
  });
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={copy.title(group.name)}
      sub={copy.sub}
      submitLabel={copy.submit}
      submitVariant="danger"
      onSubmit={() => void commit.run()}
      pending={commit.pending}
    >
      <p className="hint">Thao tác được ghi nhật ký.</p>
    </CommitDialog>
  );
}
