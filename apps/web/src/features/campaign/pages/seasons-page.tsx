import { useState } from "react";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import { Grid } from "../../../shared/ui/grid";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useCampaigns } from "../api/queries";
import { CampaignCard } from "../components/campaign-card";
import { DemandTable } from "../components/demand-table";
import { CloseCampaignDialog } from "../containers/close-campaign-dialog";
import type { Campaign } from "../schemas";

/** The campaign whose demand the table projects: the running Tết season first, else a running or planned one. */
function demandCampaign(campaigns: readonly Campaign[]): Campaign | null {
  const open = campaigns.filter((c) => c.status !== "closed");
  const rank = (c: Campaign) => (c.status === "active" ? 0 : 2) + (c.kind === "tet" ? 0 : 1);
  return [...open].sort((a, b) => rank(a) - rank(b) || a.starts_on.localeCompare(b.starts_on))[0] ?? null;
}

/** Chiến dịch mùa vụ (/campaigns/seasons). */
export default function SeasonsPage() {
  const today = useBusinessDate();
  const campaigns = useCampaigns();
  const [closing, setClosing] = useState<Campaign | null>(null);

  return (
    <>
      <PageHeader lead="Chiến dịch là nhãn phân tích, không phải sổ riêng. Mỗi dòng doanh thu/chi phí có một chiến dịch chính. Kết mùa không xóa tồn, tiền hay nợ." />
      {campaigns.isLoading ? (
        <ListLoading />
      ) : campaigns.items.length === 0 && !campaigns.error ? (
        <p className="hint">Chưa có chiến dịch.</p>
      ) : (
        <Grid cols="g2">
          {campaigns.items.map((c) => (
            <CampaignCard key={c.id} campaign={c} today={today} onClose={setClosing} />
          ))}
        </Grid>
      )}
      <ListFooter list={campaigns} />
      {!campaigns.isLoading ? <DemandTable campaign={demandCampaign(campaigns.items)} /> : null}
      {closing !== null ? <CloseCampaignDialog key={closing.id} campaign={closing} onClose={() => setClosing(null)} /> : null}
    </>
  );
}
