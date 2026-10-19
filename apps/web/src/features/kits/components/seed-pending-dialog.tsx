import { Callout } from "../../../shared/ui/callout";
import { CommitDialog } from "../../../shared/ui/commit-dialog";

/** An editor dialog whose saved recipe is still loading, or failed to load. */
export function SeedPendingDialog({ title, failed, onClose }: { title: string; failed: boolean; onClose: () => void }) {
  return (
    <CommitDialog open onClose={onClose} title={title} wide>
      {failed ? <Callout tone="bad">Không tải được công thức. Đóng và thử lại.</Callout> : <p className="hint">Đang tải…</p>}
    </CommitDialog>
  );
}
