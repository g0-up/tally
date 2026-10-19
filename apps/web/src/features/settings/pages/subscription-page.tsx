import { useSubscription } from "../../../shared/api/platform-queries";
import { Card } from "../../../shared/ui/card";
import { Grid, Stack } from "../../../shared/ui/grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useOwner } from "../api/account";
import { CurrentSubscriptionCard } from "../components/current-subscription-card";
import { InvoicesCard } from "../components/invoices-card";
import { LoadError } from "../components/load-error";
import { PlanCards } from "../components/plan-cards";
import { SupportAccessCard } from "../components/support-access-card";
import { SupportTicketsCard } from "../components/support-tickets-card";

export const SUBSCRIPTION_LEAD = "Gói và quyền module được kiểm tra ở máy chủ. Hết hạn chuyển sang chỉ đọc theo chính sách, không xóa dữ liệu hay giấu nợ. Giá gói chưa chốt.";

/** Thuê bao (/settings/subscription): plans, the current subscription, its invoices and support. */
export default function SubscriptionPage() {
  const view = useSubscription();
  const owner = useOwner();
  return (
    <>
      <PageHeader lead={SUBSCRIPTION_LEAD} />
      {view.isPending ? (
        <ListLoading />
      ) : view.isError ? (
        <LoadError what="thuê bao" error={view.error} />
      ) : (
        <PlanCards currentPlanId={view.data.subscription?.plan_id ?? null} addons={view.data.addons} />
      )}
      <Grid cols="g2" style={{ marginTop: 20 }}>
        {view.data ? (
          <CurrentSubscriptionCard view={view.data} sandbox={owner.data?.tenant.is_sandbox ?? false} />
        ) : (
          <Card title="Thuê bao hiện tại">{view.isPending ? <ListLoading /> : <p className="hint">Không tải được thuê bao.</p>}</Card>
        )}
        <SupportAccessCard />
      </Grid>
      <Stack style={{ marginTop: 20 }}>
        <InvoicesCard />
        <SupportTicketsCard />
      </Stack>
    </>
  );
}
