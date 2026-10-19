import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { useCloseCampaign } from "../api/mutations";
import { useChecklist } from "../api/queries";
import type { Campaign } from "../schemas";

export interface CloseCampaignDialogProps {
  campaign: Campaign;
  onClose: () => void;
}

/** "Kết mùa": closing never waits for the checklist; the items still open are listed in the toast. */
export function CloseCampaignDialog({ campaign, onClose }: CloseCampaignDialogProps) {
  const checklist = useChecklist(campaign.id);
  const close = useCloseCampaign(onClose);
  const items = checklist.data?.data ?? [];
  const done = items.filter((x) => x.done).length;
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={`Kết mùa ${campaign.name}`}
      sub="Kết mùa không xóa tồn, tiền hay nợ. Số liệu của mùa vẫn xem được trong báo cáo."
      submitLabel="Kết mùa"
      onSubmit={() => void close.run(campaign)}
      pending={close.pending}
      live={
        <ImpactSummary
          items={[
            { label: "Checklist đã xong", value: checklist.data ? `${done}/${items.length}` : "—" },
            { label: "Tồn, tiền, công nợ", value: "Không thay đổi" },
          ]}
        />
      }
    >
      <p className="hint">Sau khi kết mùa, chiến dịch không nhận thêm thay đổi.</p>
    </CommitDialog>
  );
}
